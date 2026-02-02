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
	"math"
	"sync"
	"sync/atomic"
	"time"
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

	// Note: Inferred latency removed - use DebugStats.SegmentWallTime* for accurate latency
	// from FFmpeg timestamps. See docs/REMOVE_INFERRED_LATENCY_ANALYSIS.md

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

// DebugStatsAggregate contains aggregated debug statistics across all clients.
// Organized by protocol layer (HLS/HTTP/TCP) for the layered TUI dashboard.
// All metrics come from DebugEventParser with accurate FFmpeg timestamps.
type DebugStatsAggregate struct {
	// HLS Layer (from DebugEventParser)
	SegmentsDownloaded int64
	SegmentsFailed     int64
	SegmentsSkipped    int64
	SegmentsExpired    int64
	PlaylistsRefreshed int64
	PlaylistsFailed    int64
	SegmentWallTimeAvg float64
	SegmentWallTimeMin float64
	SegmentWallTimeMax float64
	// Percentiles (from T-Digest, using accurate FFmpeg timestamps)
	SegmentWallTimeP25 time.Duration // 25th percentile
	SegmentWallTimeP50 time.Duration // 50th percentile (median)
	SegmentWallTimeP75 time.Duration // 75th percentile
	SegmentWallTimeP95 time.Duration // 95th percentile
	SegmentWallTimeP99 time.Duration // 99th percentile
	// Manifest wall time (using accurate FFmpeg timestamps)
	ManifestCount int64
	ManifestWallTimeAvg float64
	ManifestWallTimeMin float64
	ManifestWallTimeMax float64
	// Percentiles (from T-Digest, using accurate FFmpeg timestamps)
	ManifestWallTimeP25 time.Duration // 25th percentile
	ManifestWallTimeP50 time.Duration // 50th percentile (median)
	ManifestWallTimeP75 time.Duration // 75th percentile
	ManifestWallTimeP95 time.Duration // 95th percentile
	ManifestWallTimeP99 time.Duration // 99th percentile
	PlaylistJitterAvg  float64
	PlaylistJitterMax  float64
	PlaylistLateCount  int64  // Number of playlist refreshes that were late
	SequenceSkips      int64

	// HTTP Layer
	HTTPOpenCount  int64
	HTTP4xxCount   int64
	HTTP5xxCount   int64
	ReconnectCount int64
	ErrorRate      float64

	// TCP Layer
	TCPConnectCount int64
	TCPSuccessCount int64
	TCPRefusedCount int64
	TCPTimeoutCount int64
	TCPHealthRatio  float64
	TCPConnectAvgMs float64
	TCPConnectMinMs float64
	TCPConnectMaxMs float64

	// Timing accuracy
	TimestampsUsed int64
	LinesProcessed int64

	// Client count
	ClientsWithDebugStats int

	// Instantaneous rates (per second) - calculated from last snapshot (Phase 7.4)
	InstantSegmentsRate     float64 // Segments downloaded per second
	InstantPlaylistsRate    float64 // Playlists refreshed per second
	InstantHTTPRequestsRate float64 // HTTP requests per second
	InstantTCPConnectsRate  float64 // TCP connections per second

	// Segment Bytes & Throughput (from segment size tracking)
	TotalSegmentBytes           int64   // Total bytes downloaded (from segment scraper)
	SegmentThroughputAvg1s      float64 // bytes/sec over last 1 second
	SegmentThroughputAvg30s     float64 // bytes/sec over last 30 seconds
	SegmentThroughputAvg60s     float64 // bytes/sec over last 60 seconds
	SegmentThroughputAvg300s    float64 // bytes/sec over last 300 seconds (5 min)
	SegmentThroughputAvgOverall float64 // bytes/sec since start

	// Segment size lookup diagnostics
	SegmentSizeLookupAttempts  int64 // Total lookup attempts
	SegmentSizeLookupSuccesses int64 // Successful lookups
}

// StatsAggregator aggregates stats from multiple clients.
//
// Thread-safe: all methods can be called concurrently.
// Uses sync.Map for lock-free client management.
type StatsAggregator struct {
	clients sync.Map // map[int]*ClientStats (lock-free)
	startTime time.Time

	// For rate calculations (using atomic.Value for lock-free access)
	prevSnapshot atomic.Value // *rateSnapshot

	dropThreshold float64
	// peakDropRate uses atomic.Uint64 with bit manipulation for lock-free max operation
	peakDropRate atomic.Uint64 // math.Float64bits(peakDropRate)
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

	agg := &StatsAggregator{
		startTime:     time.Now(),
		dropThreshold: dropThreshold,
		// clients sync.Map is zero-initialized (ready to use)
	}
	// Initialize atomic.Value with initial snapshot
	agg.prevSnapshot.Store(&rateSnapshot{
		timestamp: time.Now(),
	})
	return agg
}

// AddClient registers a client for aggregation.
// Uses sync.Map for lock-free access.
func (a *StatsAggregator) AddClient(stats *ClientStats) {
	a.clients.Store(stats.ClientID, stats)
}

// RemoveClient unregisters a client.
// Uses sync.Map for lock-free access.
func (a *StatsAggregator) RemoveClient(clientID int) {
	a.clients.Delete(clientID)
}

// GetClient returns the ClientStats for a specific client.
// Uses sync.Map for lock-free access.
func (a *StatsAggregator) GetClient(clientID int) *ClientStats {
	val, ok := a.clients.Load(clientID)
	if !ok {
		return nil
	}
	return val.(*ClientStats)
}

// ClientCount returns the number of registered clients.
// Uses sync.Map for lock-free access.
func (a *StatsAggregator) ClientCount() int {
	count := 0
	a.clients.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}

// Aggregate computes aggregated statistics across all clients.
//
// This creates a snapshot of current metrics. The returned struct is
// safe to use after the call returns.
// Uses sync.Map for lock-free client iteration.
func (a *StatsAggregator) Aggregate() *AggregatedStats {
	now := time.Now()
	elapsed := now.Sub(a.startTime).Seconds()

	// Get previous snapshot for rate calculations (lock-free)
	prevSnapshotPtr := a.prevSnapshot.Load()
	var prevSnapshot *rateSnapshot
	if prevSnapshotPtr != nil {
		prevSnapshot = prevSnapshotPtr.(*rateSnapshot)
	}

	// Snapshot clients into regular map for fast iteration
	clients := make(map[int]*ClientStats)
	clientCount := 0
	a.clients.Range(func(key, value interface{}) bool {
		clients[key.(int)] = value.(*ClientStats)
		clientCount++
		return true
	})

	result := &AggregatedStats{
		Timestamp:       now,
		TotalClients:    clientCount,
		TotalHTTPErrors: make(map[int]int64),
	}

	// Accumulators
	var totalSpeed float64
	var speedCount int
	var totalDrift time.Duration
	var driftCount int
	var totalUptime time.Duration

	for _, c := range clients {
		result.ActiveClients++

		// Sum request counts (lock-free atomic reads)
		result.TotalManifestReqs += c.ManifestRequests.Load()
		result.TotalSegmentReqs += c.SegmentRequests.Load()
		result.TotalInitReqs += c.InitRequests.Load()
		result.TotalUnknownReqs += c.UnknownRequests.Load()
		result.TotalBytes += c.TotalBytes()

		// Sum errors
		for code, count := range c.GetHTTPErrors() {
			result.TotalHTTPErrors[code] += count
		}
		result.TotalReconnections += c.Reconnections.Load()
		result.TotalTimeouts += c.Timeouts.Load()

		// Note: Inferred latency removed - use DebugStats for accurate latency
		// from FFmpeg timestamps. See docs/REMOVE_INFERRED_LATENCY_ANALYSIS.md

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

		// Pipeline health (lock-free atomic reads)
		progressRead := c.ProgressLinesRead.Load()
		progressDropped := c.ProgressLinesDropped.Load()
		stderrRead := c.StderrLinesRead.Load()
		stderrDropped := c.StderrLinesDropped.Load()

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

	// Note: Inferred latency percentiles removed - use DebugStats.SegmentWallTime*
	// for accurate latency from FFmpeg timestamps

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

	// Update peak drop rate using CAS loop for lock-free max operation
	currentRate := result.PeakDropRate
	for {
		oldBits := a.peakDropRate.Load()
		oldRate := math.Float64frombits(oldBits)
		if currentRate <= oldRate {
			break // No update needed
		}
		newBits := math.Float64bits(currentRate)
		if a.peakDropRate.CompareAndSwap(oldBits, newBits) {
			break // Successfully updated
		}
		// Retry on CAS failure (another goroutine updated it)
	}

	// Update previous snapshot for next rate calculation (lock-free)
	a.prevSnapshot.Store(&rateSnapshot{
		timestamp:    now,
		manifestReqs: result.TotalManifestReqs,
		segmentReqs:  result.TotalSegmentReqs,
		bytes:        result.TotalBytes,
	})

	return result
}

// GetPeakDropRate returns the highest drop rate observed across all aggregations.
// Uses atomic operations for lock-free access.
func (a *StatsAggregator) GetPeakDropRate() float64 {
	return math.Float64frombits(a.peakDropRate.Load())
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
// Uses sync.Map for lock-free access.
func (a *StatsAggregator) Reset() {
	// Clear all clients from sync.Map
	a.clients.Range(func(key, _ interface{}) bool {
		a.clients.Delete(key)
		return true
	})
	a.startTime = time.Now()
	a.prevSnapshot.Store(&rateSnapshot{
		timestamp: time.Now(),
	})

	a.peakDropRate.Store(math.Float64bits(0))
}

// ForEachClient calls the provided function for each client.
// Uses sync.Map for lock-free iteration.
func (a *StatsAggregator) ForEachClient(fn func(clientID int, stats *ClientStats)) {
	a.clients.Range(func(key, value interface{}) bool {
		fn(key.(int), value.(*ClientStats))
		return true
	})
}

// GetAllClientSummaries returns summaries for all clients.
// Uses sync.Map for lock-free iteration.
func (a *StatsAggregator) GetAllClientSummaries() []Summary {
	summaries := make([]Summary, 0)
	a.clients.Range(func(_, value interface{}) bool {
		stats := value.(*ClientStats)
		summaries = append(summaries, stats.GetSummary())
		return true
	})
	return summaries
}
