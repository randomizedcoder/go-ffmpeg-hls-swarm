// Package stats provides per-client and aggregated statistics for HLS load testing.
//
// This file implements ClientStats which tracks metrics for a single FFmpeg client:
// - Request counts (manifest, segment, init, unknown)
// - Bytes downloaded (handles FFmpeg restart resets)
// - HTTP errors, reconnections, timeouts
// - Inferred segment latency (T-Digest for memory-efficient percentiles)
// - Wall-clock drift (playback vs real time)
// - Stall detection
// - Pipeline health (dropped lines)
//
// IMPORTANT: Latency values are INFERRED from FFmpeg events, not directly measured.
// Use for trend analysis, not absolute performance claims.
package stats

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/influxdata/tdigest"
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

	// HangingRequestTTL is the maximum time a request can be "inflight"
	// before we consider it a timeout and clean it up to prevent memory leaks
	HangingRequestTTL = 60 * time.Second
)

// ClientStats holds per-client statistics.
//
// Thread-safe: all fields are protected by mutexes or atomics.
type ClientStats struct {
	ClientID  int
	StartTime time.Time

	// Request counts (atomic)
	ManifestRequests int64
	SegmentRequests  int64
	InitRequests     int64
	UnknownRequests  int64 // Fallback for unrecognized URL patterns

	// Bytes tracking - CRITICAL: handles FFmpeg restart resets
	// When FFmpeg restarts, total_size resets to 0. We must track
	// cumulative bytes across all FFmpeg instances for this client.
	bytesFromPreviousRuns int64 // Sum from all completed FFmpeg processes
	currentProcessBytes   int64 // Current FFmpeg's total_size
	bytesMu               sync.Mutex

	// Error counts
	HTTPErrors   map[int]int64
	httpErrorsMu sync.Mutex
	Reconnections int64
	Timeouts      int64

	// Latency tracking - uses sync.Map for parallel segment fetches
	// Key: URL string, Value: time.Time (request start)
	inflightRequests sync.Map

	// INFERRED latency tracking (T-Digest for memory efficiency)
	// IMPORTANT: This is inferred from FFmpeg events, not measured directly.
	// Use for trend analysis, not absolute values.
	inferredLatencyDigest *tdigest.TDigest
	inferredLatencyCount  int64
	inferredLatencySum    time.Duration
	inferredLatencyMax    time.Duration
	inferredLatencyMu     sync.Mutex

	// Segment size tracking (estimated from total_size delta)
	lastTotalSize  int64
	segmentSizes   []int64
	segmentSizeIdx int
	segmentSizeMu  sync.Mutex

	// Playback health
	CurrentSpeed          float64
	speedBelowThresholdAt time.Time
	speedMu               sync.Mutex

	// Wall-clock drift tracking
	LastPlaybackTime time.Duration // OutTimeUS converted
	CurrentDrift     time.Duration // Wall-clock - playback-clock
	MaxDrift         time.Duration
	driftMu          sync.Mutex

	// Pipeline health (lossy-by-design metrics)
	ProgressLinesDropped int64
	StderrLinesDropped   int64
	ProgressLinesRead    int64
	StderrLinesRead      int64
	PeakDropRate         float64 // Track peak, not just current
	peakDropMu           sync.Mutex
}

// NewClientStats creates stats for a client.
func NewClientStats(clientID int) *ClientStats {
	return &ClientStats{
		ClientID:              clientID,
		StartTime:             time.Now(),
		HTTPErrors:            make(map[int]int64),
		inferredLatencyDigest: tdigest.NewWithCompression(100), // ~100 centroids, ~10KB
		segmentSizes:          make([]int64, SegmentSizeRingSize),
	}
}

// --- Request Counting ---

// IncrementManifestRequests increments the manifest request counter.
func (s *ClientStats) IncrementManifestRequests() {
	atomic.AddInt64(&s.ManifestRequests, 1)
}

// IncrementSegmentRequests increments the segment request counter.
func (s *ClientStats) IncrementSegmentRequests() {
	atomic.AddInt64(&s.SegmentRequests, 1)
}

// IncrementInitRequests increments the init segment request counter.
func (s *ClientStats) IncrementInitRequests() {
	atomic.AddInt64(&s.InitRequests, 1)
}

// IncrementUnknownRequests increments the unknown URL request counter.
func (s *ClientStats) IncrementUnknownRequests() {
	atomic.AddInt64(&s.UnknownRequests, 1)
}

// --- Error Recording ---

// RecordHTTPError records an HTTP error by status code.
func (s *ClientStats) RecordHTTPError(code int) {
	s.httpErrorsMu.Lock()
	s.HTTPErrors[code]++
	s.httpErrorsMu.Unlock()
}

// RecordReconnection records a reconnection attempt.
func (s *ClientStats) RecordReconnection() {
	atomic.AddInt64(&s.Reconnections, 1)
}

// RecordTimeout records a timeout event.
func (s *ClientStats) RecordTimeout() {
	atomic.AddInt64(&s.Timeouts, 1)
}

// GetHTTPErrors returns a copy of the HTTP errors map.
func (s *ClientStats) GetHTTPErrors() map[int]int64 {
	s.httpErrorsMu.Lock()
	defer s.httpErrorsMu.Unlock()

	result := make(map[int]int64, len(s.HTTPErrors))
	for k, v := range s.HTTPErrors {
		result[k] = v
	}
	return result
}

// --- Bytes Tracking (handles FFmpeg restarts) ---

// OnProcessStart must be called when FFmpeg process starts/restarts.
// Accumulates bytes from the previous process before reset.
func (s *ClientStats) OnProcessStart() {
	s.bytesMu.Lock()
	s.bytesFromPreviousRuns += s.currentProcessBytes
	s.currentProcessBytes = 0
	s.bytesMu.Unlock()
}

// UpdateCurrentBytes updates bytes from current FFmpeg's total_size.
func (s *ClientStats) UpdateCurrentBytes(totalSize int64) {
	s.bytesMu.Lock()
	s.currentProcessBytes = totalSize
	s.bytesMu.Unlock()
}

// TotalBytes returns cumulative bytes across all FFmpeg restarts.
func (s *ClientStats) TotalBytes() int64 {
	s.bytesMu.Lock()
	defer s.bytesMu.Unlock()
	return s.bytesFromPreviousRuns + s.currentProcessBytes
}

// --- Inferred Latency Tracking (T-Digest for constant memory) ---
// IMPORTANT: Latency is INFERRED from FFmpeg events, not directly measured.
// Use for trend analysis, not absolute performance claims.

// OnSegmentRequestStart tracks the start of a segment download.
func (s *ClientStats) OnSegmentRequestStart(url string) {
	s.inflightRequests.Store(url, time.Now())
}

// OnSegmentRequestComplete completes a segment download by URL.
func (s *ClientStats) OnSegmentRequestComplete(url string) {
	if startTime, ok := s.inflightRequests.LoadAndDelete(url); ok {
		latency := time.Since(startTime.(time.Time))
		s.recordInferredLatency(latency)
	}
}

// CompleteOldestSegment completes the oldest inflight .ts request.
// Called on progress updates when we don't know which segment completed.
// Also cleans up "hanging" requests older than TTL to prevent memory leaks.
func (s *ClientStats) CompleteOldestSegment() time.Duration {
	var oldestURL string
	var oldestTime time.Time
	var hangingURLs []string
	now := time.Now()

	s.inflightRequests.Range(func(key, value interface{}) bool {
		url := key.(string)
		startTime := value.(time.Time)

		// Check for hanging requests (older than TTL)
		if now.Sub(startTime) > HangingRequestTTL {
			hangingURLs = append(hangingURLs, url)
			return true // Continue iteration
		}

		// Find oldest segment request (.ts files only)
		lowerURL := strings.ToLower(url)
		if strings.HasSuffix(lowerURL, ".ts") || strings.Contains(lowerURL, ".ts?") {
			if oldestTime.IsZero() || startTime.Before(oldestTime) {
				oldestURL = url
				oldestTime = startTime
			}
		}
		return true
	})

	// Clean up hanging requests and record as timeouts
	for _, url := range hangingURLs {
		s.inflightRequests.Delete(url)
		atomic.AddInt64(&s.Timeouts, 1)
	}

	// Complete oldest segment if found
	if oldestURL != "" {
		s.inflightRequests.Delete(oldestURL)
		latency := now.Sub(oldestTime)
		s.recordInferredLatency(latency)
		return latency
	}

	return 0
}

// recordInferredLatency adds a latency sample to the T-Digest.
func (s *ClientStats) recordInferredLatency(d time.Duration) {
	s.inferredLatencyMu.Lock()
	defer s.inferredLatencyMu.Unlock()

	s.inferredLatencyDigest.Add(float64(d.Nanoseconds()), 1)
	s.inferredLatencyCount++
	s.inferredLatencySum += d
	if d > s.inferredLatencyMax {
		s.inferredLatencyMax = d
	}
}

// InferredLatencyP50 returns the 50th percentile (median) latency.
func (s *ClientStats) InferredLatencyP50() time.Duration {
	s.inferredLatencyMu.Lock()
	defer s.inferredLatencyMu.Unlock()
	return time.Duration(s.inferredLatencyDigest.Quantile(0.50))
}

// InferredLatencyP95 returns the 95th percentile latency.
func (s *ClientStats) InferredLatencyP95() time.Duration {
	s.inferredLatencyMu.Lock()
	defer s.inferredLatencyMu.Unlock()
	return time.Duration(s.inferredLatencyDigest.Quantile(0.95))
}

// InferredLatencyP99 returns the 99th percentile latency.
func (s *ClientStats) InferredLatencyP99() time.Duration {
	s.inferredLatencyMu.Lock()
	defer s.inferredLatencyMu.Unlock()
	return time.Duration(s.inferredLatencyDigest.Quantile(0.99))
}

// InferredLatencyMax returns the maximum observed latency.
func (s *ClientStats) InferredLatencyMax() time.Duration {
	s.inferredLatencyMu.Lock()
	defer s.inferredLatencyMu.Unlock()
	return s.inferredLatencyMax
}

// InferredLatencyAvg returns the average latency.
func (s *ClientStats) InferredLatencyAvg() time.Duration {
	s.inferredLatencyMu.Lock()
	defer s.inferredLatencyMu.Unlock()

	if s.inferredLatencyCount == 0 {
		return 0
	}
	return s.inferredLatencySum / time.Duration(s.inferredLatencyCount)
}

// InferredLatencyCount returns the number of latency samples.
func (s *ClientStats) InferredLatencyCount() int64 {
	s.inferredLatencyMu.Lock()
	defer s.inferredLatencyMu.Unlock()
	return s.inferredLatencyCount
}

// InflightRequestCount returns the number of pending requests.
func (s *ClientStats) InflightRequestCount() int {
	count := 0
	s.inflightRequests.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}

// --- Wall-Clock Drift Tracking ---

// UpdateDrift updates the wall-clock drift from playback position.
// Drift = (Now - StartTime) - PlaybackTime
func (s *ClientStats) UpdateDrift(outTimeUS int64) {
	if outTimeUS <= 0 {
		return
	}

	playbackTime := time.Duration(outTimeUS) * time.Microsecond
	wallClockElapsed := time.Since(s.StartTime)

	s.driftMu.Lock()
	s.LastPlaybackTime = playbackTime
	s.CurrentDrift = wallClockElapsed - playbackTime
	if s.CurrentDrift > s.MaxDrift {
		s.MaxDrift = s.CurrentDrift
	}
	s.driftMu.Unlock()
}

// GetDrift returns current and max drift values.
func (s *ClientStats) GetDrift() (current, max time.Duration) {
	s.driftMu.Lock()
	defer s.driftMu.Unlock()
	return s.CurrentDrift, s.MaxDrift
}

// HasHighDrift returns true if drift exceeds threshold.
func (s *ClientStats) HasHighDrift() bool {
	s.driftMu.Lock()
	defer s.driftMu.Unlock()
	return s.CurrentDrift > HighDriftThreshold
}

// --- Speed and Stall Detection ---

// UpdateSpeed updates the current playback speed.
func (s *ClientStats) UpdateSpeed(speed float64) {
	s.speedMu.Lock()
	defer s.speedMu.Unlock()

	s.CurrentSpeed = speed

	if speed > 0 && speed < StallThreshold {
		if s.speedBelowThresholdAt.IsZero() {
			s.speedBelowThresholdAt = time.Now()
		}
	} else {
		s.speedBelowThresholdAt = time.Time{}
	}
}

// GetSpeed returns the current playback speed.
func (s *ClientStats) GetSpeed() float64 {
	s.speedMu.Lock()
	defer s.speedMu.Unlock()
	return s.CurrentSpeed
}

// IsStalled returns true if client has been below speed threshold for too long.
func (s *ClientStats) IsStalled() bool {
	s.speedMu.Lock()
	defer s.speedMu.Unlock()

	if s.speedBelowThresholdAt.IsZero() {
		return false
	}
	return time.Since(s.speedBelowThresholdAt) > StallDuration
}

// --- Segment Size Tracking ---

// RecordSegmentSize records an estimated segment size.
func (s *ClientStats) RecordSegmentSize(size int64) {
	s.segmentSizeMu.Lock()
	s.segmentSizes[s.segmentSizeIdx] = size
	s.segmentSizeIdx = (s.segmentSizeIdx + 1) % SegmentSizeRingSize
	s.segmentSizeMu.Unlock()
}

// GetAverageSegmentSize returns the average of recent segment sizes.
func (s *ClientStats) GetAverageSegmentSize() int64 {
	s.segmentSizeMu.Lock()
	defer s.segmentSizeMu.Unlock()

	var sum int64
	var count int64
	for _, size := range s.segmentSizes {
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
func (s *ClientStats) RecordDroppedLines(progressRead, progressDropped, stderrRead, stderrDropped int64) {
	atomic.StoreInt64(&s.ProgressLinesRead, progressRead)
	atomic.StoreInt64(&s.ProgressLinesDropped, progressDropped)
	atomic.StoreInt64(&s.StderrLinesRead, stderrRead)
	atomic.StoreInt64(&s.StderrLinesDropped, stderrDropped)

	// Track peak drop rate
	currentRate := s.CurrentDropRate()
	s.peakDropMu.Lock()
	if currentRate > s.PeakDropRate {
		s.PeakDropRate = currentRate
	}
	s.peakDropMu.Unlock()
}

// CurrentDropRate returns current drop rate (0.0 to 1.0).
func (s *ClientStats) CurrentDropRate() float64 {
	totalRead := atomic.LoadInt64(&s.ProgressLinesRead) + atomic.LoadInt64(&s.StderrLinesRead)
	totalDropped := atomic.LoadInt64(&s.ProgressLinesDropped) + atomic.LoadInt64(&s.StderrLinesDropped)
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
func (s *ClientStats) GetPeakDropRate() float64 {
	s.peakDropMu.Lock()
	defer s.peakDropMu.Unlock()
	return s.PeakDropRate
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
	LatencyP50       time.Duration
	LatencyP95       time.Duration
	LatencyP99       time.Duration
	LatencyMax       time.Duration
	LatencyCount     int64
	CurrentSpeed     float64
	IsStalled        bool
	CurrentDrift     time.Duration
	MaxDrift         time.Duration
	HasHighDrift     bool
	DropRate         float64
	PeakDropRate     float64
}

// GetSummary returns a snapshot of all key metrics.
func (s *ClientStats) GetSummary() Summary {
	// Get drift values with lock
	currentDrift, maxDrift := s.GetDrift()

	return Summary{
		ClientID:         s.ClientID,
		Uptime:           s.Uptime(),
		TotalBytes:       s.TotalBytes(),
		ManifestRequests: atomic.LoadInt64(&s.ManifestRequests),
		SegmentRequests:  atomic.LoadInt64(&s.SegmentRequests),
		InitRequests:     atomic.LoadInt64(&s.InitRequests),
		UnknownRequests:  atomic.LoadInt64(&s.UnknownRequests),
		Reconnections:    atomic.LoadInt64(&s.Reconnections),
		Timeouts:         atomic.LoadInt64(&s.Timeouts),
		HTTPErrors:       s.GetHTTPErrors(),
		LatencyP50:       s.InferredLatencyP50(),
		LatencyP95:       s.InferredLatencyP95(),
		LatencyP99:       s.InferredLatencyP99(),
		LatencyMax:       s.InferredLatencyMax(),
		LatencyCount:     s.InferredLatencyCount(),
		CurrentSpeed:     s.GetSpeed(),
		IsStalled:        s.IsStalled(),
		CurrentDrift:     currentDrift,
		MaxDrift:         maxDrift,
		HasHighDrift:     s.HasHighDrift(),
		DropRate:         s.CurrentDropRate(),
		PeakDropRate:     s.GetPeakDropRate(),
	}
}
