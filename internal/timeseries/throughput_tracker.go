// Package timeseries provides time-windowed metric tracking for HLS load testing.
//
// This is an internal library designed for simplicity and testability.
// It tracks cumulative bytes and computes rolling averages over configurable
// time windows (1s, 30s, 60s, 300s).
//
// Thread-safe: AddBytes() uses atomic int64, GetStats() acquires read lock.
// Memory: ~10KB for 300 samples (5 minute window at 1 sample/sec).
package timeseries

import (
	"sync"
	"sync/atomic"
	"time"
)

const (
	// ringBufferSize is the number of samples to retain (5 minutes at 1 sample/sec)
	ringBufferSize = 300

	// Window durations for rolling averages
	window1s   = 1 * time.Second
	window30s  = 30 * time.Second
	window60s  = 60 * time.Second
	window300s = 300 * time.Second
)

// Clock interface for testing with deterministic time.
type Clock interface {
	Now() time.Time
}

// realClock uses time.Now() for production.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// sample represents a point-in-time snapshot of cumulative bytes.
type sample struct {
	timestamp time.Time
	bytes     int64
}

// ThroughputTracker tracks cumulative bytes downloaded and computes rolling
// averages over configurable time windows.
//
// Usage:
//
//	tracker := NewThroughputTracker()
//	tracker.AddBytes(1024)  // Called per segment (thread-safe, lock-free)
//	// ... periodic sampling (e.g., every 1s via ticker)
//	tracker.RecordSample()
//	// ... get stats for TUI/Prometheus
//	stats := tracker.GetStats()
type ThroughputTracker struct {
	// totalBytes is the cumulative byte count (atomic for lock-free AddBytes)
	totalBytes atomic.Int64

	// Ring buffer of samples for rolling average calculation
	samples  []sample
	writeIdx int // Next write position in ring buffer
	mu       sync.RWMutex

	// Start time for overall average calculation
	startTime time.Time

	// Clock for testability
	clock Clock
}

// ThroughputStats contains computed rolling averages at a point in time.
type ThroughputStats struct {
	// TotalBytes is the cumulative bytes downloaded since start
	TotalBytes int64

	// Rolling averages (bytes per second)
	Avg1s   float64 // Average over last 1 second
	Avg30s  float64 // Average over last 30 seconds
	Avg60s  float64 // Average over last 60 seconds
	Avg300s float64 // Average over last 300 seconds (5 minutes)

	// AvgOverall is the average throughput since tracking started
	AvgOverall float64
}

// NewThroughputTracker creates a new tracker with real clock.
func NewThroughputTracker() *ThroughputTracker {
	return NewThroughputTrackerWithClock(realClock{})
}

// NewThroughputTrackerWithClock creates a tracker with custom clock for testing.
func NewThroughputTrackerWithClock(clock Clock) *ThroughputTracker {
	now := clock.Now()
	t := &ThroughputTracker{
		samples:   make([]sample, 0, ringBufferSize),
		startTime: now,
		clock:     clock,
	}
	// Record initial sample at t=0 with 0 bytes
	t.samples = append(t.samples, sample{timestamp: now, bytes: 0})
	return t
}

// AddBytes adds bytes to the cumulative total.
// Thread-safe and lock-free (uses atomic int64).
// Call this when a segment download completes.
func (t *ThroughputTracker) AddBytes(n int64) {
	if n > 0 {
		t.totalBytes.Add(n)
	}
}

// RecordSample records the current cumulative bytes with a timestamp.
// Call this periodically (e.g., every 1 second via ticker).
// Thread-safe (acquires write lock on ring buffer only).
func (t *ThroughputTracker) RecordSample() {
	now := t.clock.Now()
	currentBytes := t.totalBytes.Load()

	t.mu.Lock()
	defer t.mu.Unlock()

	newSample := sample{timestamp: now, bytes: currentBytes}

	if len(t.samples) < ringBufferSize {
		// Buffer not yet full - append
		t.samples = append(t.samples, newSample)
	} else {
		// Buffer full - overwrite oldest
		t.samples[t.writeIdx] = newSample
		t.writeIdx = (t.writeIdx + 1) % ringBufferSize
	}
}

// GetStats computes and returns current throughput statistics.
// Thread-safe (acquires read lock). Always returns valid data
// (never returns "no data" - uses available history).
func (t *ThroughputTracker) GetStats() ThroughputStats {
	now := t.clock.Now()
	currentBytes := t.totalBytes.Load()

	t.mu.RLock()
	defer t.mu.RUnlock()

	stats := ThroughputStats{
		TotalBytes: currentBytes,
	}

	// Calculate overall average
	elapsed := now.Sub(t.startTime).Seconds()
	if elapsed > 0 {
		stats.AvgOverall = float64(currentBytes) / elapsed
	}

	// Calculate rolling averages for each window
	stats.Avg1s = t.avgOverWindow(now, currentBytes, window1s)
	stats.Avg30s = t.avgOverWindow(now, currentBytes, window30s)
	stats.Avg60s = t.avgOverWindow(now, currentBytes, window60s)
	stats.Avg300s = t.avgOverWindow(now, currentBytes, window300s)

	return stats
}

// avgOverWindow calculates average bytes/sec over the specified window.
// Must be called with mu held (at least RLock).
func (t *ThroughputTracker) avgOverWindow(now time.Time, currentBytes int64, window time.Duration) float64 {
	if len(t.samples) == 0 {
		return 0
	}

	targetTime := now.Add(-window)

	// Find the sample closest to (but not after) targetTime
	// Search from newest to oldest for efficiency (likely match is recent)
	var bestSample *sample
	var bestDiff time.Duration = -1

	for i := range t.samples {
		s := &t.samples[i]
		if s.timestamp.After(targetTime) {
			continue // Sample is within the window, skip
		}
		diff := targetTime.Sub(s.timestamp)
		if bestDiff < 0 || diff < bestDiff {
			bestSample = s
			bestDiff = diff
		}
	}

	// If no sample before targetTime, use the oldest sample we have
	if bestSample == nil {
		bestSample = t.oldestSample()
	}

	if bestSample == nil {
		return 0
	}

	// Calculate bytes transferred and actual elapsed time
	bytesTransferred := currentBytes - bestSample.bytes
	actualElapsed := now.Sub(bestSample.timestamp).Seconds()

	if actualElapsed <= 0 {
		return 0 // Avoid division by zero
	}

	return float64(bytesTransferred) / actualElapsed
}

// oldestSample returns the oldest sample in the ring buffer.
// Must be called with mu held.
func (t *ThroughputTracker) oldestSample() *sample {
	if len(t.samples) == 0 {
		return nil
	}

	if len(t.samples) < ringBufferSize {
		// Buffer not full yet - oldest is at index 0
		return &t.samples[0]
	}

	// Buffer full - oldest is at writeIdx (next to be overwritten)
	return &t.samples[t.writeIdx]
}

// Reset clears all data and restarts tracking.
// Thread-safe.
func (t *ThroughputTracker) Reset() {
	now := t.clock.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	t.totalBytes.Store(0)
	t.samples = t.samples[:0]
	t.samples = append(t.samples, sample{timestamp: now, bytes: 0})
	t.writeIdx = 0
	t.startTime = now
}

// SampleCount returns the number of samples in the ring buffer.
// Useful for testing.
func (t *ThroughputTracker) SampleCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.samples)
}
