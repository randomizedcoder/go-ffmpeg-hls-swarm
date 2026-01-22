// Package stats provides per-client and aggregated statistics for HLS load testing.
//
// This file implements StatsAggregator which aggregates metrics across all clients:
// - Request counts and rates
// - Bytes downloaded and throughput
// - Inferred latency percentiles (T-Digest merged)
// - Playback health (speed, stalls, drift)
// - Pipeline health (dropped lines)
// - Error rates
package stats

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/influxdata/tdigest"
)

// AggregatedStats holds metrics across all clients.
//
// This is a snapshot - values are computed at the time of Aggregate() call.
type AggregatedStats struct {
	// Timestamp when this snapshot was taken
	Timestamp time.Time

	// Client counts
	TotalClients   int
	ActiveClients  int
	StalledClients int

	// Request totals
	TotalManifestReqs int64
	TotalSegmentReqs  int64
	TotalInitReqs     int64
	TotalUnknownReqs  int64 // Fallback bucket for unrecognized URLs
	TotalBytes        int64

	// Rates (per second) - calculated from start time
	ManifestReqRate       float64
	SegmentReqRate        float64
	ThroughputBytesPerSec float64

	// Instantaneous rates (per second) - calculated from last snapshot
	InstantManifestRate   float64
	InstantSegmentRate    float64
	InstantThroughputRate float64

	// Errors
	TotalHTTPErrors    map[int]int64
	TotalReconnections int64
	TotalTimeouts      int64
	ErrorRate          float64 // errors / total requests

	// INFERRED Latency (T-Digest aggregated percentiles)
	// Note: Inferred from FFmpeg events - use for trends, not absolutes
	InferredLatencyP50   time.Duration
	InferredLatencyP95   time.Duration
	InferredLatencyP99   time.Duration
	InferredLatencyMax   time.Duration
	InferredLatencyCount int64

	// Health
	ClientsAboveRealtime int
	ClientsBelowRealtime int
	AverageSpeed         float64

	// Wall-clock Drift (critical for HLS testing)
	AverageDrift         time.Duration
	MaxDrift             time.Duration
	ClientsWithHighDrift int // Drift > 5 seconds

	// Pipeline health (lossy-by-design)
	TotalLinesDropped int64
	TotalLinesRead    int64
	ClientsWithDrops  int
	MetricsDegraded   bool    // Drop rate > threshold (default 1%)
	PeakDropRate      float64 // Highest observed drop rate (correlate with load)

	// Uptime distribution
	MinUptime time.Duration
	MaxUptime time.Duration
	AvgUptime time.Duration

	// Per-client summaries (optional, for detailed TUI view)
	PerClientSummaries []Summary
}

// StatsAggregator aggregates stats from multiple clients.
//
// Thread-safe: all methods can be called concurrently.
type StatsAggregator struct {
	mu        sync.RWMutex
	clients   map[int]*ClientStats
	startTime time.Time

	// For rate calculations (protected by snapshotMu)
	snapshotMu   sync.Mutex
	prevSnapshot *rateSnapshot

	dropThreshold  float64
	peakDropRate   float64
	peakDropRateMu sync.Mutex
}

// rateSnapshot holds values for calculating instantaneous rates
type rateSnapshot struct {
	timestamp    time.Time
	manifestReqs int64
	segmentReqs  int64
	bytes        int64
}

// NewStatsAggregator creates a new aggregator.
func NewStatsAggregator(dropThreshold float64) *StatsAggregator {
	if dropThreshold <= 0 {
		dropThreshold = 0.01 // Default 1%
	}

	return &StatsAggregator{
		clients:       make(map[int]*ClientStats),
		startTime:     time.Now(),
		dropThreshold: dropThreshold,
		prevSnapshot: &rateSnapshot{
			timestamp: time.Now(),
		},
	}
}

// AddClient registers a client for aggregation.
func (a *StatsAggregator) AddClient(stats *ClientStats) {
	a.mu.Lock()
	a.clients[stats.ClientID] = stats
	a.mu.Unlock()
}

// RemoveClient unregisters a client.
func (a *StatsAggregator) RemoveClient(clientID int) {
	a.mu.Lock()
	delete(a.clients, clientID)
	a.mu.Unlock()
}

// GetClient returns the ClientStats for a specific client.
func (a *StatsAggregator) GetClient(clientID int) *ClientStats {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.clients[clientID]
}

// ClientCount returns the number of registered clients.
func (a *StatsAggregator) ClientCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.clients)
}

// Aggregate computes aggregated statistics across all clients.
//
// This creates a snapshot of current metrics. The returned struct is
// safe to use after the call returns.
func (a *StatsAggregator) Aggregate() *AggregatedStats {
	a.mu.RLock()
	defer a.mu.RUnlock()

	now := time.Now()
	elapsed := now.Sub(a.startTime).Seconds()

	// Get previous snapshot for rate calculations (with lock)
	a.snapshotMu.Lock()
	prevSnapshot := a.prevSnapshot
	a.snapshotMu.Unlock()

	result := &AggregatedStats{
		Timestamp:       now,
		TotalClients:    len(a.clients),
		TotalHTTPErrors: make(map[int]int64),
	}

	// Merged T-Digest for global percentiles
	mergedDigest := tdigest.NewWithCompression(100)
	var totalLatencyCount int64

	// Accumulators
	var totalSpeed float64
	var speedCount int
	var totalDrift time.Duration
	var driftCount int
	var totalUptime time.Duration

	for _, c := range a.clients {
		result.ActiveClients++

		// Sum request counts
		result.TotalManifestReqs += atomic.LoadInt64(&c.ManifestRequests)
		result.TotalSegmentReqs += atomic.LoadInt64(&c.SegmentRequests)
		result.TotalInitReqs += atomic.LoadInt64(&c.InitRequests)
		result.TotalUnknownReqs += atomic.LoadInt64(&c.UnknownRequests)
		result.TotalBytes += c.TotalBytes()

		// Sum errors
		for code, count := range c.GetHTTPErrors() {
			result.TotalHTTPErrors[code] += count
		}
		result.TotalReconnections += atomic.LoadInt64(&c.Reconnections)
		result.TotalTimeouts += atomic.LoadInt64(&c.Timeouts)

		// Merge latency T-Digests
		// Note: We can't directly merge T-Digests in this library,
		// so we use the percentiles from each client to approximate
		c.inferredLatencyMu.Lock()
		if c.inferredLatencyCount > 0 {
			// Add samples at key percentiles to approximate the distribution
			// This is an approximation - true T-Digest merging would be better
			mergedDigest.Add(float64(c.inferredLatencyDigest.Quantile(0.50)), float64(c.inferredLatencyCount)/4)
			mergedDigest.Add(float64(c.inferredLatencyDigest.Quantile(0.75)), float64(c.inferredLatencyCount)/4)
			mergedDigest.Add(float64(c.inferredLatencyDigest.Quantile(0.90)), float64(c.inferredLatencyCount)/4)
			mergedDigest.Add(float64(c.inferredLatencyDigest.Quantile(0.99)), float64(c.inferredLatencyCount)/4)
			totalLatencyCount += c.inferredLatencyCount

			// Track max latency
			if c.inferredLatencyMax > result.InferredLatencyMax {
				result.InferredLatencyMax = c.inferredLatencyMax
			}
		}
		c.inferredLatencyMu.Unlock()

		// Speed/health
		speed := c.GetSpeed()
		if speed > 0 {
			totalSpeed += speed
			speedCount++
			if speed >= 1.0 {
				result.ClientsAboveRealtime++
			} else {
				result.ClientsBelowRealtime++
			}
		}

		if c.IsStalled() {
			result.StalledClients++
		}

		// Drift
		currentDrift, maxDrift := c.GetDrift()
		if currentDrift > 0 {
			totalDrift += currentDrift
			driftCount++
		}
		if maxDrift > result.MaxDrift {
			result.MaxDrift = maxDrift
		}
		if c.HasHighDrift() {
			result.ClientsWithHighDrift++
		}

		// Pipeline health
		progressRead := atomic.LoadInt64(&c.ProgressLinesRead)
		progressDropped := atomic.LoadInt64(&c.ProgressLinesDropped)
		stderrRead := atomic.LoadInt64(&c.StderrLinesRead)
		stderrDropped := atomic.LoadInt64(&c.StderrLinesDropped)

		result.TotalLinesRead += progressRead + stderrRead
		result.TotalLinesDropped += progressDropped + stderrDropped

		if progressDropped > 0 || stderrDropped > 0 {
			result.ClientsWithDrops++
		}

		// Track peak drop rate
		peakDrop := c.GetPeakDropRate()
		if peakDrop > result.PeakDropRate {
			result.PeakDropRate = peakDrop
		}

		// Uptime
		uptime := c.Uptime()
		totalUptime += uptime
		if result.MinUptime == 0 || uptime < result.MinUptime {
			result.MinUptime = uptime
		}
		if uptime > result.MaxUptime {
			result.MaxUptime = uptime
		}
	}

	// Calculate rates from start time
	if elapsed > 0 {
		result.ManifestReqRate = float64(result.TotalManifestReqs) / elapsed
		result.SegmentReqRate = float64(result.TotalSegmentReqs) / elapsed
		result.ThroughputBytesPerSec = float64(result.TotalBytes) / elapsed
	}

	// Calculate instantaneous rates from previous snapshot
	if prevSnapshot != nil {
		snapElapsed := now.Sub(prevSnapshot.timestamp).Seconds()
		if snapElapsed > 0 {
			result.InstantManifestRate = float64(result.TotalManifestReqs-prevSnapshot.manifestReqs) / snapElapsed
			result.InstantSegmentRate = float64(result.TotalSegmentReqs-prevSnapshot.segmentReqs) / snapElapsed
			result.InstantThroughputRate = float64(result.TotalBytes-prevSnapshot.bytes) / snapElapsed
		}
	}

	// Calculate latency percentiles from merged digest
	result.InferredLatencyCount = totalLatencyCount
	if totalLatencyCount > 0 {
		result.InferredLatencyP50 = time.Duration(mergedDigest.Quantile(0.50))
		result.InferredLatencyP95 = time.Duration(mergedDigest.Quantile(0.95))
		result.InferredLatencyP99 = time.Duration(mergedDigest.Quantile(0.99))
	}

	// Average speed
	if speedCount > 0 {
		result.AverageSpeed = totalSpeed / float64(speedCount)
	}

	// Average drift
	if driftCount > 0 {
		result.AverageDrift = totalDrift / time.Duration(driftCount)
	}

	// Average uptime
	if result.ActiveClients > 0 {
		result.AvgUptime = totalUptime / time.Duration(result.ActiveClients)
	}

	// Error rate
	totalReqs := result.TotalManifestReqs + result.TotalSegmentReqs + result.TotalInitReqs
	var totalErrors int64
	for _, count := range result.TotalHTTPErrors {
		totalErrors += count
	}
	totalErrors += result.TotalTimeouts

	if totalReqs > 0 {
		result.ErrorRate = float64(totalErrors) / float64(totalReqs)
	}

	// Pipeline health check
	if result.TotalLinesRead > 0 {
		dropRate := float64(result.TotalLinesDropped) / float64(result.TotalLinesRead)
		result.MetricsDegraded = dropRate > a.dropThreshold
	}

	// Update peak drop rate
	a.peakDropRateMu.Lock()
	if result.PeakDropRate > a.peakDropRate {
		a.peakDropRate = result.PeakDropRate
	}
	a.peakDropRateMu.Unlock()

	// Update previous snapshot for next rate calculation (with lock)
	a.snapshotMu.Lock()
	a.prevSnapshot = &rateSnapshot{
		timestamp:    now,
		manifestReqs: result.TotalManifestReqs,
		segmentReqs:  result.TotalSegmentReqs,
		bytes:        result.TotalBytes,
	}
	a.snapshotMu.Unlock()

	return result
}

// GetPeakDropRate returns the highest drop rate observed across all aggregations.
func (a *StatsAggregator) GetPeakDropRate() float64 {
	a.peakDropRateMu.Lock()
	defer a.peakDropRateMu.Unlock()
	return a.peakDropRate
}

// StartTime returns when the aggregator was created.
func (a *StatsAggregator) StartTime() time.Time {
	return a.startTime
}

// Elapsed returns the duration since the aggregator was created.
func (a *StatsAggregator) Elapsed() time.Duration {
	return time.Since(a.startTime)
}

// Reset clears all clients and resets the start time.
func (a *StatsAggregator) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.clients = make(map[int]*ClientStats)
	a.startTime = time.Now()
	a.prevSnapshot = &rateSnapshot{
		timestamp: time.Now(),
	}

	a.peakDropRateMu.Lock()
	a.peakDropRate = 0
	a.peakDropRateMu.Unlock()
}

// ForEachClient calls the provided function for each client.
// The function is called while holding the read lock.
func (a *StatsAggregator) ForEachClient(fn func(clientID int, stats *ClientStats)) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	for id, stats := range a.clients {
		fn(id, stats)
	}
}

// GetAllClientSummaries returns summaries for all clients.
func (a *StatsAggregator) GetAllClientSummaries() []Summary {
	a.mu.RLock()
	defer a.mu.RUnlock()

	summaries := make([]Summary, 0, len(a.clients))
	for _, stats := range a.clients {
		summaries = append(summaries, stats.GetSummary())
	}
	return summaries
}
