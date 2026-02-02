// Package stats provides per-client and aggregated statistics for HLS load testing.
//
// This file implements ClientStats which tracks metrics for a single FFmpeg client:
// - Request counts (manifest, segment, init, unknown)
// - Bytes downloaded (handles FFmpeg restart resets)
// - HTTP errors, reconnections, timeouts
// - Wall-clock drift (playback vs real time)
// - Stall detection
// - Pipeline health (dropped lines)
//
// Note: Latency metrics are now provided by DebugEventParser using accurate FFmpeg timestamps.
// See docs/REMOVE_INFERRED_LATENCY_ANALYSIS.md for details.
package stats

import (
	"math"
	"sync/atomic"
	"time"
)

// Constants for stall and drift detection
const (
	// StallThreshold is the speed below which a client is considered stalling
	StallThreshold = 0.9

	// StallDuration is how long speed must be below threshold to be "stalled"
	StallDuration = 5 * time.Second

	// HighDriftThreshold is the drift above which we flag a client
	HighDriftThreshold = 5 * time.Second

	// SegmentSizeRingSize is the number of segment sizes to track
	SegmentSizeRingSize = 100
)

// Note: Removed struct swap pattern with sync.Pool - using individual atomics instead
// This eliminates race conditions, allocations, and complexity while maintaining lock-free operation

// ClientStats holds per-client statistics.
//
// Thread-safe: all fields are protected by mutexes or atomics.
type ClientStats struct {
	ClientID  int
	StartTime time.Time

	// Request counts (atomic, lock-free)
	ManifestRequests atomic.Int64
	SegmentRequests  atomic.Int64
	InitRequests     atomic.Int64
	UnknownRequests  atomic.Int64 // Fallback for unrecognized URL patterns

	// Bytes tracking - CRITICAL: handles FFmpeg restart resets
	// When FFmpeg restarts, total_size resets to 0. We must track
	// cumulative bytes across all FFmpeg instances for this client.
	// Using atomic operations for lock-free access (high frequency updates)
	bytesFromPreviousRuns atomic.Int64 // Sum from all completed FFmpeg processes
	currentProcessBytes   atomic.Int64 // Current FFmpeg's total_size

	// Error counts
	// HTTP error counters (atomic, lock-free)
	// Array indexed by status code: 0-199 = 400-599, 200 = "other"
	httpErrorCounts [201]atomic.Int64
	Reconnections   atomic.Int64
	Timeouts        atomic.Int64

	// Note: Inferred latency removed - use DebugEventParser for accurate latency
	// from FFmpeg timestamps. See docs/REMOVE_INFERRED_LATENCY_ANALYSIS.md

	// Segment size tracking (estimated from total_size delta)
	// Using individual atomics for lock-free access (no allocations)
	lastTotalSize  atomic.Int64 // Last total_size value (for delta calculation)
	segmentSizes   []int64      // Shared slice (read-only after init, protected by atomic index)
	segmentSizeIdx atomic.Int64 // Atomic index for ring buffer

	// Playback health
	// Using individual atomics for lock-free access (no allocations)
	speed            atomic.Uint64 // math.Float64bits(speed)
	belowThresholdAt atomic.Value  // time.Time

	// Wall-clock drift tracking
	// Using individual atomics for lock-free access (no allocations)
	lastPlaybackTime atomic.Int64 // time.Duration as nanoseconds
	currentDrift     atomic.Int64 // time.Duration as nanoseconds
	maxDrift         atomic.Int64 // time.Duration as nanoseconds

	// Pipeline health (lossy-by-design metrics, atomic, lock-free)
	ProgressLinesDropped atomic.Int64
	StderrLinesDropped   atomic.Int64
	ProgressLinesRead    atomic.Int64
	StderrLinesRead      atomic.Int64
	// PeakDropRate uses atomic.Uint64 with bit manipulation for lock-free max operation
	peakDropRate atomic.Uint64 // math.Float64bits(PeakDropRate)
}

// NewClientStats creates stats for a client.
func NewClientStats(clientID int) *ClientStats {
	return &ClientStats{
		ClientID:     clientID,
		StartTime:    time.Now(),
		segmentSizes: make([]int64, SegmentSizeRingSize),
		// Atomic fields are zero-initialized (safe default values)
		// belowThresholdAt atomic.Value is nil (zero value) = time.Time{} when loaded
		// httpErrorCounts array is zero-initialized (all counters start at 0)
	}
}

// --- Request Counting ---

// IncrementManifestRequests increments the manifest request counter.
// Uses atomic operations for lock-free access.
func (s *ClientStats) IncrementManifestRequests() {
	s.ManifestRequests.Add(1)
}

// IncrementSegmentRequests increments the segment request counter.
// Uses atomic operations for lock-free access.
func (s *ClientStats) IncrementSegmentRequests() {
	s.SegmentRequests.Add(1)
}

// IncrementInitRequests increments the init segment request counter.
// Uses atomic operations for lock-free access.
func (s *ClientStats) IncrementInitRequests() {
	s.InitRequests.Add(1)
}

// IncrementUnknownRequests increments the unknown URL request counter.
// Uses atomic operations for lock-free access.
func (s *ClientStats) IncrementUnknownRequests() {
	s.UnknownRequests.Add(1)
}

// --- Error Recording ---

// RecordHTTPError records an HTTP error by status code.
// Uses atomic operations for lock-free access.
func (s *ClientStats) RecordHTTPError(code int) {
	if code >= 400 && code <= 599 {
		// Standard HTTP error codes (4xx, 5xx)
		s.httpErrorCounts[code-400].Add(1)
	} else {
		// Non-standard codes go to "other" bucket (index 200)
		s.httpErrorCounts[200].Add(1)
	}
}

// RecordReconnection records a reconnection attempt.
// Uses atomic operations for lock-free access.
func (s *ClientStats) RecordReconnection() {
	s.Reconnections.Add(1)
}

// RecordTimeout records a timeout event.
// Uses atomic operations for lock-free access.
func (s *ClientStats) RecordTimeout() {
	s.Timeouts.Add(1)
}

// GetHTTPErrors returns a map of HTTP error counts.
// Uses atomic operations for lock-free access.
// Only includes codes with non-zero counts.
func (s *ClientStats) GetHTTPErrors() map[int]int64 {
	result := make(map[int]int64)

	// Iterate over all standard error codes (400-599)
	for code := 400; code <= 599; code++ {
		if count := s.httpErrorCounts[code-400].Load(); count > 0 {
			result[code] = count
		}
	}

	// Include "other" errors if any (use 0 as sentinel)
	if otherCount := s.httpErrorCounts[200].Load(); otherCount > 0 {
		result[0] = otherCount
	}

	return result
}

// --- Bytes Tracking (handles FFmpeg restarts) ---

// OnProcessStart must be called when FFmpeg process starts/restarts.
// Accumulates bytes from the previous process before reset.
// Uses atomic operations for lock-free access.
func (s *ClientStats) OnProcessStart() {
	// Atomic read-modify-write: read current, reset to 0, add to previous
	prev := s.currentProcessBytes.Swap(0) // Swap returns old value and sets to 0
	s.bytesFromPreviousRuns.Add(prev)
}

// UpdateCurrentBytes updates bytes from current FFmpeg's total_size.
// Uses atomic operations for lock-free access.
func (s *ClientStats) UpdateCurrentBytes(totalSize int64) {
	s.currentProcessBytes.Store(totalSize)
}

// TotalBytes returns cumulative bytes across all FFmpeg restarts.
// Uses atomic operations for lock-free access.
func (s *ClientStats) TotalBytes() int64 {
	return s.bytesFromPreviousRuns.Load() + s.currentProcessBytes.Load()
}

// --- Inferred Latency Tracking (T-Digest for constant memory) ---
// IMPORTANT: Latency is INFERRED from FFmpeg events, not directly measured.
// Use for trend analysis, not absolute performance claims.

// Note: Inferred latency methods removed - use DebugEventParser for accurate latency
// from FFmpeg timestamps. See docs/REMOVE_INFERRED_LATENCY_ANALYSIS.md

// --- Wall-Clock Drift Tracking ---

// UpdateDrift updates the wall-clock drift from playback position.
// Drift = (Now - StartTime) - PlaybackTime
// Uses individual atomics for lock-free access (no allocations, no race conditions).
func (s *ClientStats) UpdateDrift(outTimeUS int64) {
	if outTimeUS <= 0 {
		return
	}

	playbackTime := time.Duration(outTimeUS) * time.Microsecond
	wallClockElapsed := time.Since(s.StartTime)
	current := wallClockElapsed - playbackTime

	// Update atomically (lock-free)
	s.lastPlaybackTime.Store(int64(playbackTime))
	s.currentDrift.Store(int64(current))

	// Update max drift using CAS loop (like peakDropRate)
	for {
		oldMax := s.maxDrift.Load()
		if int64(current) <= oldMax {
			break // No update needed
		}
		if s.maxDrift.CompareAndSwap(oldMax, int64(current)) {
			break // Successfully updated
		}
		// Retry on CAS failure (another goroutine updated it)
	}
}

// GetDrift returns current and max drift values.
// Uses atomic operations for lock-free access.
func (s *ClientStats) GetDrift() (current, max time.Duration) {
	return time.Duration(s.currentDrift.Load()), time.Duration(s.maxDrift.Load())
}

// HasHighDrift returns true if drift exceeds threshold.
// Uses atomic operations for lock-free access.
func (s *ClientStats) HasHighDrift() bool {
	return time.Duration(s.currentDrift.Load()) > HighDriftThreshold
}

// --- Speed and Stall Detection ---

// UpdateSpeed updates the current playback speed.
// Uses individual atomics for lock-free access (no allocations, no race conditions).
func (s *ClientStats) UpdateSpeed(speed float64) {
	// Load current speed to check if we're crossing the threshold
	currentSpeedBits := s.speed.Load()
	currentSpeed := math.Float64frombits(currentSpeedBits)

	// Update speed atomically (lock-free)
	s.speed.Store(math.Float64bits(speed))

	// Update belowThresholdAt based on speed transition
	// Note: Brief out-of-sync with speed is acceptable for stall detection
	if speed > 0 && speed < StallThreshold {
		// Speed is below threshold
		if currentSpeed >= StallThreshold {
			// Just crossed below threshold - set timestamp
			s.belowThresholdAt.Store(time.Now())
		}
		// If already below threshold, keep existing timestamp (don't overwrite)
	} else {
		// Speed is above threshold - clear timestamp
		s.belowThresholdAt.Store(time.Time{})
	}
}

// GetSpeed returns the current playback speed.
// Uses atomic operations for lock-free access.
func (s *ClientStats) GetSpeed() float64 {
	return math.Float64frombits(s.speed.Load())
}

// IsStalled returns true if client has been below speed threshold for too long.
// Uses atomic operations for lock-free access.
func (s *ClientStats) IsStalled() bool {
	thresholdTimePtr := s.belowThresholdAt.Load()
	if thresholdTimePtr == nil {
		return false
	}
	thresholdTime := thresholdTimePtr.(time.Time)
	if thresholdTime.IsZero() {
		return false
	}
	return time.Since(thresholdTime) > StallDuration
}

// --- Segment Size Tracking ---

// RecordSegmentSize records an estimated segment size.
// Uses atomic index with shared slice for lock-free access (no allocations, no race conditions).
func (s *ClientStats) RecordSegmentSize(size int64) {
	// Atomically increment index (wraps around using modulo)
	oldIdx := s.segmentSizeIdx.Load()
	newIdx := (oldIdx + 1) % SegmentSizeRingSize
	s.segmentSizeIdx.Store(newIdx)

	// Write to shared slice at new index
	// Note: Brief inconsistency between index update and write is acceptable
	s.segmentSizes[newIdx] = size
}

// GetAverageSegmentSize returns the average of recent segment sizes.
// Uses atomic operations for lock-free access.
func (s *ClientStats) GetAverageSegmentSize() int64 {
	// Read all elements from shared slice
	// Note: Brief inconsistency is acceptable - worst case is we include/exclude one element
	var sum int64
	var count int64
	for i := 0; i < SegmentSizeRingSize; i++ {
		size := s.segmentSizes[i]
		if size > 0 {
			sum += size
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return sum / count
}

// --- Pipeline Health ---

// RecordDroppedLines records lines dropped by parsing pipelines.
// Also tracks peak drop rate for correlation with load spikes.
// Uses atomic operations for lock-free access.
func (s *ClientStats) RecordDroppedLines(progressRead, progressDropped, stderrRead, stderrDropped int64) {
	s.ProgressLinesRead.Store(progressRead)
	s.ProgressLinesDropped.Store(progressDropped)
	s.StderrLinesRead.Store(stderrRead)
	s.StderrLinesDropped.Store(stderrDropped)

	// Track peak drop rate using CAS loop for lock-free max operation
	currentRate := s.CurrentDropRate()
	for {
		oldBits := s.peakDropRate.Load()
		oldRate := math.Float64frombits(oldBits)
		if currentRate <= oldRate {
			break // No update needed
		}
		newBits := math.Float64bits(currentRate)
		if s.peakDropRate.CompareAndSwap(oldBits, newBits) {
			break // Successfully updated
		}
		// Retry on CAS failure (another goroutine updated it)
	}
}

// CurrentDropRate returns current drop rate (0.0 to 1.0).
// Uses atomic operations for lock-free access.
func (s *ClientStats) CurrentDropRate() float64 {
	totalRead := s.ProgressLinesRead.Load() + s.StderrLinesRead.Load()
	totalDropped := s.ProgressLinesDropped.Load() + s.StderrLinesDropped.Load()
	if totalRead == 0 {
		return 0
	}
	return float64(totalDropped) / float64(totalRead)
}

// MetricsDegraded returns true if drop rate exceeds threshold.
// threshold is typically 0.01 (1%) but can be configured.
func (s *ClientStats) MetricsDegraded(threshold float64) bool {
	return s.CurrentDropRate() > threshold
}

// GetPeakDropRate returns the highest drop rate observed.
// Uses atomic operations for lock-free access.
func (s *ClientStats) GetPeakDropRate() float64 {
	return math.Float64frombits(s.peakDropRate.Load())
}

// --- Uptime ---

// Uptime returns how long this client has been running.
func (s *ClientStats) Uptime() time.Duration {
	return time.Since(s.StartTime)
}

// --- Summary ---

// Summary returns a snapshot of key metrics.
type Summary struct {
	ClientID         int
	Uptime           time.Duration
	TotalBytes       int64
	ManifestRequests int64
	SegmentRequests  int64
	InitRequests     int64
	UnknownRequests  int64
	Reconnections    int64
	Timeouts         int64
	HTTPErrors       map[int]int64
	// Note: Latency metrics removed - use DebugStats.SegmentWallTime* for accurate latency
	CurrentSpeed float64
	IsStalled    bool
	CurrentDrift time.Duration
	MaxDrift     time.Duration
	HasHighDrift bool
	DropRate     float64
	PeakDropRate float64
}

// GetSummary returns a snapshot of all key metrics.
func (s *ClientStats) GetSummary() Summary {
	// Get drift values with lock
	currentDrift, maxDrift := s.GetDrift()

	return Summary{
		ClientID:         s.ClientID,
		Uptime:           s.Uptime(),
		TotalBytes:       s.TotalBytes(),
		ManifestRequests: s.ManifestRequests.Load(),
		SegmentRequests:  s.SegmentRequests.Load(),
		InitRequests:     s.InitRequests.Load(),
		UnknownRequests:  s.UnknownRequests.Load(),
		Reconnections:    s.Reconnections.Load(),
		Timeouts:         s.Timeouts.Load(),
		HTTPErrors:       s.GetHTTPErrors(),
		// Latency metrics removed - use DebugStats for accurate latency from FFmpeg timestamps
		CurrentSpeed: s.GetSpeed(),
		IsStalled:    s.IsStalled(),
		CurrentDrift: currentDrift,
		MaxDrift:     maxDrift,
		HasHighDrift: s.HasHighDrift(),
		DropRate:     s.CurrentDropRate(),
		PeakDropRate: s.GetPeakDropRate(),
	}
}
