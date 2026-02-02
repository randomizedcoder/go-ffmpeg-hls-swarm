// Package metrics provides Prometheus metrics for go-ffmpeg-hls-swarm.
//
// Metrics are organized into two tiers:
//   - Tier 1 (always enabled): Aggregate metrics safe for 1000+ clients
//   - Tier 2 (optional, --prom-client-metrics): Per-client metrics for debugging
//
// See docs/METRICS_IMPLEMENTATION_PLAN.md for the complete metrics reference.
package metrics

import (
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// =============================================================================
// Tier 1: Aggregate Metrics (Always Enabled)
// =============================================================================

// --- Panel 1: Test Overview ---
var (
	hlsSwarmInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "hls_swarm_info",
			Help: "Information about the load test (value always 1)",
		},
		[]string{"version", "stream_url", "variant"},
	)

	hlsTargetClients = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_target_clients",
			Help: "Target number of clients to reach",
		},
	)

	hlsTestDurationSeconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_test_duration_seconds",
			Help: "Configured test duration (0 = unlimited)",
		},
	)

	hlsActiveClients = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_active_clients",
			Help: "Currently running clients",
		},
	)

	hlsRampProgress = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_ramp_progress",
			Help: "Client ramp-up progress (0.0 to 1.0)",
		},
	)

	hlsTestElapsedSeconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_test_elapsed_seconds",
			Help: "Seconds since test started",
		},
	)

	hlsTestRemainingSeconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_test_remaining_seconds",
			Help: "Seconds remaining until test ends (-1 = unlimited)",
		},
	)
)

// --- Panel 2: Request Rates & Throughput ---
var (
	hlsManifestRequestsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "hls_swarm_manifest_requests_total",
			Help: "Total manifest (.m3u8) requests",
		},
	)

	hlsSegmentRequestsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "hls_swarm_segment_requests_total",
			Help: "Total segment (.ts) requests",
		},
	)

	hlsInitRequestsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "hls_swarm_init_requests_total",
			Help: "Total init segment requests",
		},
	)

	hlsUnknownRequestsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "hls_swarm_unknown_requests_total",
			Help: "Total unclassified URL requests",
		},
	)

	hlsBytesDownloadedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "hls_swarm_bytes_downloaded_total",
			Help: "Total bytes downloaded",
		},
	)

	hlsManifestRequestsPerSec = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_manifest_requests_per_second",
			Help: "Current manifest request rate",
		},
	)

	hlsSegmentRequestsPerSec = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_segment_requests_per_second",
			Help: "Current segment request rate",
		},
	)

	hlsThroughputBytesPerSec = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_throughput_bytes_per_second",
			Help: "Current download throughput",
		},
	)
)

// --- Panel 2b: Segment Throughput (from accurate segment sizes) ---
var (
	hlsSegmentBytesDownloadedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "hls_swarm_segment_bytes_downloaded_total",
			Help: "Total bytes downloaded from segments (based on actual segment sizes from origin)",
		},
	)

	hlsSegmentThroughputAvg1sBytesPerSec = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_segment_throughput_1s_bytes_per_second",
			Help: "Segment download throughput averaged over last 1 second",
		},
	)

	hlsSegmentThroughputAvg30sBytesPerSec = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_segment_throughput_30s_bytes_per_second",
			Help: "Segment download throughput averaged over last 30 seconds",
		},
	)

	hlsSegmentThroughputAvg60sBytesPerSec = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_segment_throughput_60s_bytes_per_second",
			Help: "Segment download throughput averaged over last 60 seconds",
		},
	)

	hlsSegmentThroughputAvg300sBytesPerSec = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_segment_throughput_300s_bytes_per_second",
			Help: "Segment download throughput averaged over last 5 minutes",
		},
	)
)

// --- Panel 3: Latency Distribution ---
var (
	// Histogram for heatmaps and histogram_quantile()
	// Note: These are INFERRED latencies from FFmpeg events
	hlsInferredLatencySeconds = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name: "hls_swarm_inferred_latency_seconds",
			Help: "Inferred segment download latency distribution (from FFmpeg events)",
			Buckets: []float64{
				0.005, 0.01, 0.025, 0.05, 0.075,
				0.1, 0.25, 0.5, 0.75,
				1.0, 2.5, 5.0, 10.0,
			},
		},
	)

	// Pre-calculated percentiles (convenience for simple panels)
	hlsLatencyP50Seconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_inferred_latency_p50_seconds",
			Help: "Inferred segment latency 50th percentile (median)",
		},
	)

	hlsLatencyP95Seconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_inferred_latency_p95_seconds",
			Help: "Inferred segment latency 95th percentile",
		},
	)

	hlsLatencyP99Seconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_inferred_latency_p99_seconds",
			Help: "Inferred segment latency 99th percentile",
		},
	)

	hlsLatencyMaxSeconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_inferred_latency_max_seconds",
			Help: "Maximum inferred segment latency observed",
		},
	)
)

// --- Panel 4: Client Health & Playback ---
var (
	hlsClientsAboveRealtime = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_clients_above_realtime",
			Help: "Clients with speed >= 1.0x (healthy)",
		},
	)

	hlsClientsBelowRealtime = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_clients_below_realtime",
			Help: "Clients with speed < 1.0x (buffering)",
		},
	)

	hlsStalledClients = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_stalled_clients",
			Help: "Clients with speed < 0.9x for >5 seconds",
		},
	)

	hlsAverageSpeed = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_average_speed",
			Help: "Average playback speed (1.0 = realtime)",
		},
	)

	hlsHighDriftClients = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_high_drift_clients",
			Help: "Clients with drift > 5 seconds",
		},
	)

	hlsAverageDriftSeconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_average_drift_seconds",
			Help: "Average wall-clock drift",
		},
	)

	hlsMaxDriftSeconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_max_drift_seconds",
			Help: "Maximum wall-clock drift",
		},
	)
)

// --- Panel 5: Errors & Recovery ---
var (
	// HTTP errors by status code (low cardinality: ~5-10 codes)
	hlsHTTPErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hls_swarm_http_errors_total",
			Help: "HTTP errors by status code",
		},
		[]string{"status_code"},
	)

	hlsTimeoutsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "hls_swarm_timeouts_total",
			Help: "Total connection/read timeouts",
		},
	)

	hlsReconnectionsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "hls_swarm_reconnections_total",
			Help: "Total FFmpeg reconnection attempts",
		},
	)

	hlsClientStartsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "hls_swarm_client_starts_total",
			Help: "Total client process starts",
		},
	)

	hlsClientRestartsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "hls_swarm_client_restarts_total",
			Help: "Total client restarts (after failure)",
		},
	)

	hlsClientExitsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hls_swarm_client_exits_total",
			Help: "Client exits by exit code category",
		},
		[]string{"category"}, // "success", "error", "signal"
	)

	hlsErrorRate = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_error_rate",
			Help: "Current error rate (errors/total requests)",
		},
	)
)

// --- Panel 6: Pipeline Health (Metrics System) ---
var (
	hlsStatsLinesDroppedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hls_swarm_stats_lines_dropped_total",
			Help: "FFmpeg output lines dropped (parser backpressure)",
		},
		[]string{"stream"}, // "progress" | "stderr"
	)

	hlsStatsLinesParsedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "hls_swarm_stats_lines_parsed_total",
			Help: "FFmpeg output lines successfully parsed",
		},
		[]string{"stream"},
	)

	hlsStatsClientsDegraded = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_stats_clients_degraded",
			Help: "Clients with >1% dropped lines",
		},
	)

	hlsStatsDropRate = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_stats_drop_rate",
			Help: "Overall metrics line drop rate (0.0-1.0)",
		},
	)

	hlsStatsPeakDropRate = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_stats_peak_drop_rate",
			Help: "Peak metrics line drop rate observed",
		},
	)
)

// --- Panel 7: Uptime Distribution ---
var (
	hlsClientUptimeSeconds = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "hls_swarm_client_uptime_seconds",
			Help:    "Client uptime before exit",
			Buckets: []float64{1, 5, 30, 60, 300, 600, 1800, 3600, 7200},
		},
	)

	hlsUptimeP50Seconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_uptime_p50_seconds",
			Help: "Client uptime 50th percentile",
		},
	)

	hlsUptimeP95Seconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_uptime_p95_seconds",
			Help: "Client uptime 95th percentile",
		},
	)

	hlsUptimeP99Seconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "hls_swarm_uptime_p99_seconds",
			Help: "Client uptime 99th percentile",
		},
	)
)

// =============================================================================
// Tier 2: Per-Client Metrics (Optional, --prom-client-metrics)
// WARNING: High cardinality - use only with <200 clients
// =============================================================================

var (
	hlsClientSpeed *prometheus.GaugeVec
	hlsClientDrift *prometheus.GaugeVec
	hlsClientBytes *prometheus.GaugeVec
)

// initPerClientMetrics initializes Tier 2 metrics.
// Only called when --prom-client-metrics is enabled.
func initPerClientMetrics(registry prometheus.Registerer) {
	hlsClientSpeed = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "hls_swarm_client_speed",
			Help: "Per-client playback speed (requires --prom-client-metrics)",
		},
		[]string{"client_id"},
	)

	hlsClientDrift = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "hls_swarm_client_drift_seconds",
			Help: "Per-client wall-clock drift (requires --prom-client-metrics)",
		},
		[]string{"client_id"},
	)

	hlsClientBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "hls_swarm_client_bytes_total",
			Help: "Per-client bytes downloaded (requires --prom-client-metrics)",
		},
		[]string{"client_id"},
	)

	registry.MustRegister(hlsClientSpeed, hlsClientDrift, hlsClientBytes)
}

// =============================================================================
// Collector
// =============================================================================

// Collector manages all Prometheus metrics for the swarm.
type Collector struct {
	// Configuration
	perClientEnabled bool
	targetClients    int
	testDuration     time.Duration
	streamURL        string
	variant          string

	// Timing
	startTime time.Time

	// Internal tracking for delta calculations
	mu                   sync.Mutex
	prevManifestReqs     int64
	prevSegmentReqs      int64
	prevInitReqs         int64
	prevUnknownReqs      int64
	prevBytes            int64
	prevSegmentBytes     int64 // From segment scraper (accurate sizes)
	prevTimeouts         int64
	prevReconnections    int64
	prevHTTPErrors       map[int]int64
	prevProgressDropped  int64
	prevStderrDropped    int64
	prevProgressParsed   int64
	prevStderrParsed     int64

	// For summary generation
	peakActive    int
	totalStarts   int64
	totalRestarts int64
	exitCodes     map[int]int64
	uptimes       []time.Duration

	// Track registered client IDs for cleanup
	registeredClientIDs map[int]struct{}
}

// CollectorConfig holds configuration for the collector.
type CollectorConfig struct {
	TargetClients    int
	TestDuration     time.Duration
	StreamURL        string
	Variant          string
	PerClientMetrics bool
}

// NewCollector creates a new metrics collector.
func NewCollector(cfg CollectorConfig) *Collector {
	return NewCollectorWithRegistry(cfg, prometheus.DefaultRegisterer)
}

// NewCollectorWithRegistry creates a collector with a custom registry.
// Useful for testing.
func NewCollectorWithRegistry(cfg CollectorConfig, registry prometheus.Registerer) *Collector {
	c := &Collector{
		perClientEnabled:    cfg.PerClientMetrics,
		targetClients:       cfg.TargetClients,
		testDuration:        cfg.TestDuration,
		streamURL:           cfg.StreamURL,
		variant:             cfg.Variant,
		startTime:           time.Now(),
		prevHTTPErrors:      make(map[int]int64),
		exitCodes:           make(map[int]int64),
		uptimes:             make([]time.Duration, 0, cfg.TargetClients),
		registeredClientIDs: make(map[int]struct{}),
	}

	// Register Tier 1 metrics (always)
	registry.MustRegister(
		// Panel 1: Test Overview
		hlsSwarmInfo,
		hlsTargetClients,
		hlsTestDurationSeconds,
		hlsActiveClients,
		hlsRampProgress,
		hlsTestElapsedSeconds,
		hlsTestRemainingSeconds,

		// Panel 2: Request Rates
		hlsManifestRequestsTotal,
		hlsSegmentRequestsTotal,
		hlsInitRequestsTotal,
		hlsUnknownRequestsTotal,
		hlsBytesDownloadedTotal,
		hlsManifestRequestsPerSec,
		hlsSegmentRequestsPerSec,
		hlsThroughputBytesPerSec,

		// Panel 2b: Segment Throughput (from accurate segment sizes)
		hlsSegmentBytesDownloadedTotal,
		hlsSegmentThroughputAvg1sBytesPerSec,
		hlsSegmentThroughputAvg30sBytesPerSec,
		hlsSegmentThroughputAvg60sBytesPerSec,
		hlsSegmentThroughputAvg300sBytesPerSec,

		// Panel 3: Latency
		hlsInferredLatencySeconds,
		hlsLatencyP50Seconds,
		hlsLatencyP95Seconds,
		hlsLatencyP99Seconds,
		hlsLatencyMaxSeconds,

		// Panel 4: Health
		hlsClientsAboveRealtime,
		hlsClientsBelowRealtime,
		hlsStalledClients,
		hlsAverageSpeed,
		hlsHighDriftClients,
		hlsAverageDriftSeconds,
		hlsMaxDriftSeconds,

		// Panel 5: Errors
		hlsHTTPErrorsTotal,
		hlsTimeoutsTotal,
		hlsReconnectionsTotal,
		hlsClientStartsTotal,
		hlsClientRestartsTotal,
		hlsClientExitsTotal,
		hlsErrorRate,

		// Panel 6: Pipeline Health
		hlsStatsLinesDroppedTotal,
		hlsStatsLinesParsedTotal,
		hlsStatsClientsDegraded,
		hlsStatsDropRate,
		hlsStatsPeakDropRate,

		// Panel 7: Uptime
		hlsClientUptimeSeconds,
		hlsUptimeP50Seconds,
		hlsUptimeP95Seconds,
		hlsUptimeP99Seconds,
	)

	// Register Tier 2 metrics (optional)
	if cfg.PerClientMetrics {
		initPerClientMetrics(registry)
	}

	// Set initial values
	hlsSwarmInfo.WithLabelValues("1.0", cfg.StreamURL, cfg.Variant).Set(1)
	hlsTargetClients.Set(float64(cfg.TargetClients))
	hlsTestDurationSeconds.Set(cfg.TestDuration.Seconds())
	hlsTestRemainingSeconds.Set(-1) // -1 = unlimited

	return c
}

// =============================================================================
// Update Methods
// =============================================================================

// AggregatedStatsUpdate holds stats for updating metrics.
// This is a subset of stats.AggregatedStats to avoid circular imports.
type AggregatedStatsUpdate struct {
	// Client counts
	ActiveClients  int
	StalledClients int

	// Request totals
	TotalManifestReqs int64
	TotalSegmentReqs  int64
	TotalInitReqs     int64
	TotalUnknownReqs  int64
	TotalBytes        int64

	// Rates
	ManifestReqRate       float64
	SegmentReqRate        float64
	ThroughputBytesPerSec float64

	// Errors
	TotalHTTPErrors    map[int]int64
	TotalReconnections int64
	TotalTimeouts      int64
	ErrorRate          float64

	// Latency (inferred)
	InferredLatencyP50 time.Duration
	InferredLatencyP95 time.Duration
	InferredLatencyP99 time.Duration
	InferredLatencyMax time.Duration

	// Health
	ClientsAboveRealtime int
	ClientsBelowRealtime int
	AverageSpeed         float64
	ClientsWithHighDrift int
	AverageDrift         time.Duration
	MaxDrift             time.Duration

	// Pipeline health
	TotalLinesDropped    int64
	TotalLinesRead       int64
	ClientsWithDrops     int
	MetricsDegraded      bool
	PeakDropRate         float64
	ProgressLinesDropped int64
	ProgressLinesRead    int64
	StderrLinesDropped   int64
	StderrLinesRead      int64

	// Uptime
	UptimeP50 time.Duration
	UptimeP95 time.Duration
	UptimeP99 time.Duration

	// Segment-specific (from accurate segment sizes via segment scraper)
	TotalSegmentBytes          int64
	SegmentThroughputAvg1s     float64
	SegmentThroughputAvg30s    float64
	SegmentThroughputAvg60s    float64
	SegmentThroughputAvg300s   float64

	// Per-client (only if enabled)
	PerClientStats []PerClientStatsUpdate
}

// PerClientStatsUpdate holds per-client stats for Tier 2 metrics.
type PerClientStatsUpdate struct {
	ClientID     int
	CurrentSpeed float64
	CurrentDrift time.Duration
	TotalBytes   int64
}

// RecordStats updates all metrics from aggregated stats.
func (c *Collector) RecordStats(stats *AggregatedStatsUpdate) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// --- Panel 1: Test Overview ---
	hlsActiveClients.Set(float64(stats.ActiveClients))
	if stats.ActiveClients > c.peakActive {
		c.peakActive = stats.ActiveClients
	}

	rampProgress := float64(0)
	if c.targetClients > 0 {
		rampProgress = float64(stats.ActiveClients) / float64(c.targetClients)
		if rampProgress > 1.0 {
			rampProgress = 1.0
		}
	}
	hlsRampProgress.Set(rampProgress)

	elapsed := time.Since(c.startTime)
	hlsTestElapsedSeconds.Set(elapsed.Seconds())

	if c.testDuration > 0 {
		remaining := c.testDuration - elapsed
		if remaining < 0 {
			remaining = 0
		}
		hlsTestRemainingSeconds.Set(remaining.Seconds())
	}

	// --- Panel 2: Request Rates ---
	// Calculate deltas and add to counters
	manifestDelta := stats.TotalManifestReqs - c.prevManifestReqs
	segmentDelta := stats.TotalSegmentReqs - c.prevSegmentReqs
	initDelta := stats.TotalInitReqs - c.prevInitReqs
	unknownDelta := stats.TotalUnknownReqs - c.prevUnknownReqs
	bytesDelta := stats.TotalBytes - c.prevBytes

	if manifestDelta > 0 {
		hlsManifestRequestsTotal.Add(float64(manifestDelta))
	}
	if segmentDelta > 0 {
		hlsSegmentRequestsTotal.Add(float64(segmentDelta))
	}
	if initDelta > 0 {
		hlsInitRequestsTotal.Add(float64(initDelta))
	}
	if unknownDelta > 0 {
		hlsUnknownRequestsTotal.Add(float64(unknownDelta))
	}
	if bytesDelta > 0 {
		hlsBytesDownloadedTotal.Add(float64(bytesDelta))
	}

	c.prevManifestReqs = stats.TotalManifestReqs
	c.prevSegmentReqs = stats.TotalSegmentReqs
	c.prevInitReqs = stats.TotalInitReqs
	c.prevUnknownReqs = stats.TotalUnknownReqs
	c.prevBytes = stats.TotalBytes

	// Current rates
	hlsManifestRequestsPerSec.Set(stats.ManifestReqRate)
	hlsSegmentRequestsPerSec.Set(stats.SegmentReqRate)
	hlsThroughputBytesPerSec.Set(stats.ThroughputBytesPerSec)

	// --- Panel 2b: Segment Throughput (from accurate segment sizes) ---
	segmentBytesDelta := stats.TotalSegmentBytes - c.prevSegmentBytes
	if segmentBytesDelta > 0 {
		hlsSegmentBytesDownloadedTotal.Add(float64(segmentBytesDelta))
	}
	c.prevSegmentBytes = stats.TotalSegmentBytes

	hlsSegmentThroughputAvg1sBytesPerSec.Set(stats.SegmentThroughputAvg1s)
	hlsSegmentThroughputAvg30sBytesPerSec.Set(stats.SegmentThroughputAvg30s)
	hlsSegmentThroughputAvg60sBytesPerSec.Set(stats.SegmentThroughputAvg60s)
	hlsSegmentThroughputAvg300sBytesPerSec.Set(stats.SegmentThroughputAvg300s)

	// --- Panel 3: Latency ---
	hlsLatencyP50Seconds.Set(stats.InferredLatencyP50.Seconds())
	hlsLatencyP95Seconds.Set(stats.InferredLatencyP95.Seconds())
	hlsLatencyP99Seconds.Set(stats.InferredLatencyP99.Seconds())
	hlsLatencyMaxSeconds.Set(stats.InferredLatencyMax.Seconds())

	// --- Panel 4: Health ---
	hlsClientsAboveRealtime.Set(float64(stats.ClientsAboveRealtime))
	hlsClientsBelowRealtime.Set(float64(stats.ClientsBelowRealtime))
	hlsStalledClients.Set(float64(stats.StalledClients))
	hlsAverageSpeed.Set(stats.AverageSpeed)
	hlsHighDriftClients.Set(float64(stats.ClientsWithHighDrift))
	hlsAverageDriftSeconds.Set(stats.AverageDrift.Seconds())
	hlsMaxDriftSeconds.Set(stats.MaxDrift.Seconds())

	// --- Panel 5: Errors ---
	// HTTP errors by status code (delta)
	for code, count := range stats.TotalHTTPErrors {
		prevCount := c.prevHTTPErrors[code]
		delta := count - prevCount
		if delta > 0 {
			if code == 0 {
				// Code 0 is the sentinel for "other" (non-standard HTTP error codes)
				hlsHTTPErrorsTotal.WithLabelValues("other").Add(float64(delta))
			} else {
				hlsHTTPErrorsTotal.WithLabelValues(strconv.Itoa(code)).Add(float64(delta))
			}
		}
		c.prevHTTPErrors[code] = count
	}

	// Timeouts and reconnections (delta)
	timeoutDelta := stats.TotalTimeouts - c.prevTimeouts
	reconnectDelta := stats.TotalReconnections - c.prevReconnections
	if timeoutDelta > 0 {
		hlsTimeoutsTotal.Add(float64(timeoutDelta))
	}
	if reconnectDelta > 0 {
		hlsReconnectionsTotal.Add(float64(reconnectDelta))
	}
	c.prevTimeouts = stats.TotalTimeouts
	c.prevReconnections = stats.TotalReconnections

	hlsErrorRate.Set(stats.ErrorRate)

	// --- Panel 6: Pipeline Health ---
	// Progress stream
	progressDroppedDelta := stats.ProgressLinesDropped - c.prevProgressDropped
	progressParsedDelta := stats.ProgressLinesRead - stats.ProgressLinesDropped - c.prevProgressParsed
	if progressDroppedDelta > 0 {
		hlsStatsLinesDroppedTotal.WithLabelValues("progress").Add(float64(progressDroppedDelta))
	}
	if progressParsedDelta > 0 {
		hlsStatsLinesParsedTotal.WithLabelValues("progress").Add(float64(progressParsedDelta))
	}
	c.prevProgressDropped = stats.ProgressLinesDropped
	c.prevProgressParsed = stats.ProgressLinesRead - stats.ProgressLinesDropped

	// Stderr stream
	stderrDroppedDelta := stats.StderrLinesDropped - c.prevStderrDropped
	stderrParsedDelta := stats.StderrLinesRead - stats.StderrLinesDropped - c.prevStderrParsed
	if stderrDroppedDelta > 0 {
		hlsStatsLinesDroppedTotal.WithLabelValues("stderr").Add(float64(stderrDroppedDelta))
	}
	if stderrParsedDelta > 0 {
		hlsStatsLinesParsedTotal.WithLabelValues("stderr").Add(float64(stderrParsedDelta))
	}
	c.prevStderrDropped = stats.StderrLinesDropped
	c.prevStderrParsed = stats.StderrLinesRead - stats.StderrLinesDropped

	hlsStatsClientsDegraded.Set(float64(stats.ClientsWithDrops))

	// Calculate overall drop rate
	totalRead := stats.TotalLinesRead
	totalDropped := stats.TotalLinesDropped
	dropRate := float64(0)
	if totalRead > 0 {
		dropRate = float64(totalDropped) / float64(totalRead)
	}
	hlsStatsDropRate.Set(dropRate)
	hlsStatsPeakDropRate.Set(stats.PeakDropRate)

	// --- Panel 7: Uptime ---
	hlsUptimeP50Seconds.Set(stats.UptimeP50.Seconds())
	hlsUptimeP95Seconds.Set(stats.UptimeP95.Seconds())
	hlsUptimeP99Seconds.Set(stats.UptimeP99.Seconds())

	// --- Tier 2: Per-client metrics ---
	if c.perClientEnabled && len(stats.PerClientStats) > 0 {
		for _, cs := range stats.PerClientStats {
			clientID := strconv.Itoa(cs.ClientID)
			hlsClientSpeed.WithLabelValues(clientID).Set(cs.CurrentSpeed)
			hlsClientDrift.WithLabelValues(clientID).Set(cs.CurrentDrift.Seconds())
			hlsClientBytes.WithLabelValues(clientID).Set(float64(cs.TotalBytes))
			c.registeredClientIDs[cs.ClientID] = struct{}{}
		}
	}
}

// RecordLatency records a single latency observation to the histogram.
func (c *Collector) RecordLatency(d time.Duration) {
	hlsInferredLatencySeconds.Observe(d.Seconds())
}

// =============================================================================
// Event Recording Methods
// =============================================================================

// ClientStarted records a client start event.
func (c *Collector) ClientStarted() {
	hlsClientStartsTotal.Inc()

	c.mu.Lock()
	c.totalStarts++
	c.mu.Unlock()
}

// ClientRestarted records a client restart event.
func (c *Collector) ClientRestarted() {
	hlsClientRestartsTotal.Inc()

	c.mu.Lock()
	c.totalRestarts++
	c.mu.Unlock()
}

// RecordExit records a process exit event.
func (c *Collector) RecordExit(exitCode int, uptime time.Duration) {
	// Categorize exit code
	category := "error"
	if exitCode == 0 {
		category = "success"
	} else if exitCode > 128 {
		category = "signal"
	}
	hlsClientExitsTotal.WithLabelValues(category).Inc()

	// Record uptime
	hlsClientUptimeSeconds.Observe(uptime.Seconds())

	c.mu.Lock()
	c.exitCodes[exitCode]++
	c.uptimes = append(c.uptimes, uptime)
	c.mu.Unlock()
}

// SetActiveCount updates the active client count (for backward compatibility).
func (c *Collector) SetActiveCount(count int) {
	hlsActiveClients.Set(float64(count))

	c.mu.Lock()
	if count > c.peakActive {
		c.peakActive = count
	}
	c.mu.Unlock()
}

// SetRampProgress updates the ramp-up progress (for backward compatibility).
func (c *Collector) SetRampProgress(progress float64) {
	hlsRampProgress.Set(progress)
}

// =============================================================================
// Cleanup Methods
// =============================================================================

// RemoveClient removes per-client metrics for a client.
// Only relevant when per-client metrics are enabled.
func (c *Collector) RemoveClient(clientID int) {
	if !c.perClientEnabled {
		return
	}

	c.mu.Lock()
	delete(c.registeredClientIDs, clientID)
	c.mu.Unlock()

	clientIDStr := strconv.Itoa(clientID)
	hlsClientSpeed.DeleteLabelValues(clientIDStr)
	hlsClientDrift.DeleteLabelValues(clientIDStr)
	hlsClientBytes.DeleteLabelValues(clientIDStr)
}

// =============================================================================
// Summary Generation
// =============================================================================

// Summary holds the data for generating an exit summary.
type Summary struct {
	Duration          time.Duration
	TargetClients     int
	PeakActiveClients int
	TotalStarts       int64
	TotalRestarts     int64
	ExitCodes         map[int]int64
	UptimeP50         time.Duration
	UptimeP95         time.Duration
	UptimeP99         time.Duration
}

// GenerateSummary creates a summary of the run.
func (c *Collector) GenerateSummary() *Summary {
	c.mu.Lock()
	defer c.mu.Unlock()

	s := &Summary{
		Duration:          time.Since(c.startTime),
		TargetClients:     c.targetClients,
		PeakActiveClients: c.peakActive,
		TotalStarts:       c.totalStarts,
		TotalRestarts:     c.totalRestarts,
		ExitCodes:         make(map[int]int64),
	}

	// Copy exit codes
	for code, count := range c.exitCodes {
		s.ExitCodes[code] = count
	}

	// Calculate percentiles
	if len(c.uptimes) > 0 {
		sorted := make([]time.Duration, len(c.uptimes))
		copy(sorted, c.uptimes)
		sortDurations(sorted)

		s.UptimeP50 = percentile(sorted, 0.50)
		s.UptimeP95 = percentile(sorted, 0.95)
		s.UptimeP99 = percentile(sorted, 0.99)
	}

	return s
}

// PeakActive returns the peak active client count.
func (c *Collector) PeakActive() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peakActive
}

// TotalStarts returns the total number of client starts.
func (c *Collector) TotalStarts() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.totalStarts
}

// TotalRestarts returns the total number of restarts.
func (c *Collector) TotalRestarts() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.totalRestarts
}

// PerClientEnabled returns whether per-client metrics are enabled.
func (c *Collector) PerClientEnabled() bool {
	return c.perClientEnabled
}

// =============================================================================
// Helper Functions
// =============================================================================

// sortDurations sorts a slice of durations in place.
func sortDurations(d []time.Duration) {
	for i := 1; i < len(d); i++ {
		for j := i; j > 0 && d[j] < d[j-1]; j-- {
			d[j], d[j-1] = d[j-1], d[j]
		}
	}
}

// percentile returns the value at the given percentile (0.0-1.0).
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
