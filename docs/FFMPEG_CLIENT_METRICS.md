# FFmpeg Client Metrics

> **Type**: Reference Documentation
> **Related**: [METRICS_ENHANCEMENT_DESIGN.md](METRICS_ENHANCEMENT_DESIGN.md), [METRICS_ENHANCEMENT_IMPLEMENTATION_LOG.md](METRICS_ENHANCEMENT_IMPLEMENTATION_LOG.md)

This document provides a comprehensive reference of all metrics collected for each FFmpeg HLS client by parsing FFmpeg output streams.

---

## Table of Contents

1. [Overview](#overview)
2. [Metrics by Source](#metrics-by-source)
   - [2.1 Progress Parser (stdout)](#21-progress-parser-stdout)
   - [2.2 HLS Event Parser (stderr)](#22-hls-event-parser-stderr)
   - [2.3 Derived Metrics](#23-derived-metrics)
3. [Per-Client Metrics Structure](#per-client-metrics-structure)
4. [Aggregated Metrics](#aggregated-metrics)
5. [Prometheus Metrics](#prometheus-metrics)
6. [Metric Collection Flow](#metric-collection-flow)

---

## Overview

The metrics system extracts data from two FFmpeg output streams:

1. **stdout** (`-progress pipe:1`): Structured key=value progress updates
2. **stderr** (`-loglevel verbose`): HLS-specific events and HTTP protocol messages

These streams are parsed in real-time using a lossy-by-design pipeline that drops lines under backpressure to prevent blocking FFmpeg processes.

**Key Design Principles:**
- **Thread-safe**: All metrics use lock-free atomic operations (no mutexes)
- **Memory-efficient**: T-Digest for latency percentiles, ring buffers for recent samples
- **Restart-aware**: Handles FFmpeg process restarts correctly (bytes accumulate across restarts)
- **Lossy-by-design**: Drops lines rather than blocking under load
- **Lock-free**: All operations use `sync/atomic` for maximum concurrency performance

---

## Metrics by Source

### 2.1 Progress Parser (stdout)

Parses FFmpeg's `-progress pipe:1` output format (key=value pairs).

#### Raw Fields Extracted

| Field | Type | Source | Description |
|-------|------|--------|-------------|
| `Frame` | `int64` | `frame=` | Cumulative frame count |
| `FPS` | `float64` | `fps=` | Current frames per second |
| `Bitrate` | `string` | `bitrate=` | Current bitrate (e.g., "512.0kbits/s", "N/A") |
| `TotalSize` | `int64` | `total_size=` | **Total bytes downloaded** (cumulative, resets on FFmpeg restart) |
| `OutTimeUS` | `int64` | `out_time_us=` | **Playback position in microseconds** (cumulative) |
| `Speed` | `float64` | `speed=` | **Playback speed** (1.0 = realtime, < 1.0 = buffering, > 1.0 = catching up) |
| `Progress` | `string` | `progress=` | Status: "continue" or "end" |
| `ReceivedAt` | `time.Time` | (parsed) | Timestamp when update was received |

#### Notes

- **`total_size=N/A` for live HLS**: FFmpeg reports `N/A` instead of a numeric value for live streams. Bytes tracking falls back to HTTP Content-Length headers when available.
- **Speed parsing**: Converts "1.00x" → 1.0, "N/A" → 0.0
- **Update frequency**: Controlled by `-stats_period` (default: 1 second)

---

### 2.2 HLS Event Parser (stderr)

Parses FFmpeg stderr for HLS-specific events using regex patterns.

#### Request Counters

| Metric | Type | Source Pattern | Description |
|--------|------|---------------|-------------|
| `ManifestRequests` | `atomic.Int64` | `Opening '...m3u8' for reading` | Count of manifest (.m3u8) requests (lock-free) |
| `SegmentRequests` | `atomic.Int64` | `Opening '...ts' for reading` | Count of segment (.ts) requests (lock-free) |
| `InitRequests` | `atomic.Int64` | `Opening '...mp4' for reading` | Count of init segment (.mp4) requests (lock-free) |
| `UnknownRequests` | `atomic.Int64` | `Opening '...' for reading` (unmatched) | **Fallback bucket** for unrecognized URL patterns (lock-free) |

**Implementation**: `internal/stats/client_stats.go`
- `IncrementManifestRequests()` (line 111-113)
- `IncrementSegmentRequests()` (line 117-119)
- `IncrementInitRequests()` (line 123-125)
- `IncrementUnknownRequests()` (line 129-131)

#### Error Counters

| Metric | Type | Source Pattern | Description |
|--------|------|---------------|-------------|
| `HTTPErrors[code]` | `[201]atomic.Int64` | `Server returned 4xx` / `Server returned 5xx` | HTTP errors by status code (lock-free array: 0-199 = codes 400-599, 200 = "other") |
| `Reconnections` | `atomic.Int64` | `Reconnecting to ...` | FFmpeg reconnection attempts (lock-free) |
| `Timeouts` | `atomic.Int64` | `timed out` / `timeout` / `Connection timed out` | Connection/read timeout events (lock-free) |

**Implementation**: `internal/stats/client_stats.go`
- `RecordHTTPError(code int)` (line 137-145): Array-based atomic counter, O(1) access
- `GetHTTPErrors() map[int]int64` (line 162-176): Lock-free iteration, returns map with code 0 for "other" errors
- `RecordReconnection()` (line 149-151): Atomic increment
- `RecordTimeout()` (line 155-157): Atomic increment

#### Latency Tracking

| Metric | Type | Method | Description |
|--------|------|--------|-------------|
| **Inferred Latency** | `[]time.Duration` | Time between `Opening '...ts'` and next progress update | **IMPORTANT**: This is INFERRED from events, not directly measured. For live HLS, includes segment wait time. |

**Latency Calculation:**
1. When segment request starts (`Opening '...ts'`), store URL → `time.Now()` in `inflightRequests` map
2. On progress update, complete oldest segment request
3. Latency = `time.Now() - startTime`
4. Store in ring buffer (max 1000 samples)

**Hanging Request Cleanup:**
- Requests older than 60 seconds are cleaned up and recorded as timeouts
- Prevents memory leaks from dropped connections

#### Parser Statistics

| Metric | Type | Description |
|--------|------|-------------|
| `LinesProcessed` | `int64` | Total stderr lines processed |
| `EventsEmitted` | `int64` | Total events emitted to callback |

---

### 2.3 Derived Metrics

Metrics calculated from raw parsed data.

#### Bytes Tracking (Restart-Aware)

| Metric | Type | Calculation | Description |
|--------|------|-------------|-------------|
| `TotalBytes()` | `int64` | `bytesFromPreviousRuns + currentProcessBytes` | **Cumulative bytes across all FFmpeg restarts** (lock-free) |
| `bytesFromPreviousRuns` | `atomic.Int64` | Accumulated on `OnProcessStart()` | Sum from all completed FFmpeg processes (lock-free) |
| `currentProcessBytes` | `atomic.Int64` | Updated from `total_size` | Current FFmpeg's `total_size` value (lock-free) |

**Implementation**: `internal/stats/client_stats.go`
- `TotalBytes()` (line 178-180): Lock-free atomic loads and sum
- `UpdateCurrentBytes()` (line 184-201): Handles FFmpeg restart resets atomically

**Why needed:** When FFmpeg restarts (client failure + recovery), `total_size` resets to 0. We must track cumulative bytes across all instances.

#### Wall-Clock Drift

| Metric | Type | Calculation | Description |
|--------|------|-------------|-------------|
| `CurrentDrift` | `time.Duration` | `(Now - StartTime) - PlaybackTime` | Current drift (positive = behind real-time) |
| `MaxDrift` | `time.Duration` | Maximum drift observed | Peak drift value |
| `HasHighDrift()` | `bool` | `CurrentDrift > 5 seconds` | Flag for clients with excessive drift |

**Drift Formula:**
```
Drift = WallClockElapsed - PlaybackTime
      = (time.Now() - StartTime) - (OutTimeUS / 1_000_000)
```

**Interpretation:**
- Positive drift = network cannot keep up with stream bitrate
- Growing drift = early warning of capacity issues
- High drift (>5s) = client flagged for attention

#### Playback Speed & Stall Detection

| Metric | Type | Calculation | Description |
|--------|------|-------------|-------------|
| `CurrentSpeed` | `float64` | From `speed=` field | Current playback speed (1.0 = realtime) |
| `IsStalled()` | `bool` | `Speed < 0.9 for >5 seconds` | Client is stalling/buffering |
| `belowThresholdAt` | `time.Time` | Set when speed drops below 0.9 | Timestamp when stalling began |

**Stall Detection:**
- Threshold: `Speed < 0.9` (10% slower than realtime)
- Duration: Must persist for >5 seconds
- Used to flag clients in aggregated stats

#### Segment Size Estimation

| Metric | Type | Calculation | Description |
|--------|------|-------------|-------------|
| `AverageSegmentSize` | `int64` | Average of last 100 segment size deltas | Estimated average segment size |
| `segmentSizes[]` | `[]int64` | Ring buffer (100 samples) | Recent segment sizes from `total_size` deltas |
| `lastTotalSize` | `atomic.Int64` | Last `total_size` value | Used for delta calculation (lock-free) |

**Implementation**: `internal/stats/client_stats.go`
- `RecordSegmentSize()` (line 284-293): Atomic index update with ring buffer
- `GetAverageSegmentSize()` (line 297-313): Lock-free ring buffer iteration

**Calculation:**
- On progress update: `segmentSize = currentTotalSize - lastTotalSize`
- Store in ring buffer (max 100 samples)
- Average = sum of non-zero values / count

**Note:** Only works for VOD content. Live HLS reports `total_size=N/A`.

#### Pipeline Health (Lossy-by-Design Metrics)

| Metric | Type | Description |
|--------|------|-------------|
| `ProgressLinesRead` | `atomic.Int64` | Lines read from stdout (lock-free) |
| `ProgressLinesDropped` | `atomic.Int64` | Lines dropped due to full channel (lock-free) |
| `StderrLinesRead` | `atomic.Int64` | Lines read from stderr (lock-free) |
| `StderrLinesDropped` | `atomic.Int64` | Lines dropped due to full channel (lock-free) |
| `CurrentDropRate()` | `float64` | `(dropped / read) * 100` | Current drop rate percentage (lock-free calculation) |
| `PeakDropRate` | `atomic.Uint64` | Highest drop rate observed (lock-free max tracking) | Correlates with load spikes |
| `MetricsDegraded()` | `bool` | `CurrentDropRate() > threshold` | True if metrics are degraded (>1% default) |

**Implementation**: `internal/stats/client_stats.go`
- `RecordDroppedLines()` (line 340-345): Atomic store operations for all pipeline health counters
- `CurrentDropRate()` (line 364-370): Lock-free calculation using atomic loads

**Pipeline Architecture:**
- **Layer 1 (Reader)**: Fast reader, drops lines if channel full (never blocks FFmpeg)
- **Layer 2 (Parser)**: Processes at own pace from bounded channel
- **Layer 3 (Stats)**: Thread-safe stat storage

**Drop Rate Threshold:** Default 1% (configurable via `--stats-drop-threshold`)

---

## Per-Client Metrics Structure

The `ClientStats` struct (`internal/stats/client_stats.go`, lines 42-93) holds all per-client metrics:

```go
type ClientStats struct {
    // Identity (immutable after init)
    ClientID  int
    StartTime time.Time

    // Request counts (atomic, lock-free)
    ManifestRequests atomic.Int64
    SegmentRequests  atomic.Int64
    InitRequests     atomic.Int64
    UnknownRequests  atomic.Int64

    // Bytes tracking (atomic, restart-aware, lock-free)
    bytesFromPreviousRuns atomic.Int64
    currentProcessBytes   atomic.Int64

    // Error counts (atomic, lock-free)
    httpErrorCounts [201]atomic.Int64  // Array: 0-199 = codes 400-599, 200 = "other"
    Reconnections   atomic.Int64
    Timeouts        atomic.Int64

    // Playback health (atomic, lock-free)
    speed            atomic.Uint64  // math.Float64bits(speed)
    belowThresholdAt atomic.Value   // time.Time

    // Wall-clock drift (atomic, lock-free)
    lastPlaybackTime atomic.Int64  // time.Duration as nanoseconds
    currentDrift     atomic.Int64
    maxDrift         atomic.Int64

    // Segment size tracking (atomic index, lock-free)
    lastTotalSize  atomic.Int64
    segmentSizes   []int64      // Shared slice (read-only after init)
    segmentSizeIdx atomic.Int64 // Atomic index for ring buffer

    // Pipeline health (atomic, lock-free)
    ProgressLinesDropped atomic.Int64
    StderrLinesDropped   atomic.Int64
    ProgressLinesRead    atomic.Int64
    StderrLinesRead      atomic.Int64
    peakDropRate         atomic.Uint64  // math.Float64bits(PeakDropRate)
}
```

**Thread Safety:**
- ✅ **All fields use `sync/atomic` for lock-free access** (no mutexes)
- ✅ **HTTP errors**: Array-based atomic counters (`[201]atomic.Int64`) instead of mutex-protected map
- ✅ **Zero mutex contention**: All operations are lock-free
- ✅ **High concurrency**: Designed for 1000+ concurrent clients

**Key Methods** (`internal/stats/client_stats.go`):
- `NewClientStats(clientID int)` (line 96-105): Creates new stats instance
- `GetSummary() Summary` (line 410-442): Returns snapshot of all metrics (uses `.Load()` for atomic fields)
- `IncrementManifestRequests()` (line 111-113): Atomic increment
- `IncrementSegmentRequests()` (line 117-119): Atomic increment
- `IncrementInitRequests()` (line 123-125): Atomic increment
- `IncrementUnknownRequests()` (line 129-131): Atomic increment
- `RecordHTTPError(code int)` (line 137-145): Array-based atomic counter
- `GetHTTPErrors() map[int]int64` (line 162-176): Lock-free iteration, returns map
- `RecordReconnection()` (line 149-151): Atomic increment
- `RecordTimeout()` (line 155-157): Atomic increment
- `TotalBytes() int64` (line 178-180): Lock-free calculation
- `UpdateCurrentBytes(totalSize int64)` (line 184-201): Handles FFmpeg restart resets
- `UpdateSpeed(speed float64)` (line 237-258): Atomic speed update with stall detection
- `UpdateDrift(outTimeUS int64)` (line 315-338): Atomic drift calculation
- `RecordDroppedLines()` (line 340-345): Atomic pipeline health updates
- `CurrentDropRate() float64` (line 364-370): Lock-free drop rate calculation

---

## Aggregated Metrics

The `StatsAggregator` (`internal/stats/aggregator.go`, lines 137-151) aggregates metrics across all clients using lock-free operations:

**Structure** (`internal/stats/aggregator.go:141-151`):
```go
type StatsAggregator struct {
    clients sync.Map // map[int]*ClientStats (lock-free, no mutex)
    startTime time.Time
    prevSnapshot atomic.Value // *rateSnapshot (lock-free)
    dropThreshold float64
    peakDropRate atomic.Uint64 // math.Float64bits(PeakDropRate)
}
```

**Key Methods** (`internal/stats/aggregator.go`):
- `NewStatsAggregator(dropThreshold float64)` (line 162-177): Creates new aggregator
- `AddClient(stats *ClientStats)` (line 181-183): Lock-free `sync.Map.Store()`
- `RemoveClient(clientID int)` (line 187-189): Lock-free `sync.Map.Delete()`
- `GetClient(clientID int) *ClientStats` (line 193-198): Lock-free `sync.Map.Load()`
- `ClientCount() int` (line 203-209): Lock-free `sync.Map.Range()` iteration
- `Aggregate() *AggregatedStats` (line 217-410): **Lock-free aggregation** - snapshots clients into regular map, then iterates with atomic `.Load()` operations
- `ForEachClient(fn func(clientID int, stats *ClientStats))` (line 442-447): Lock-free iteration
- `GetAllClientSummaries() []Summary` (line 451-458): Lock-free iteration

**Aggregation Process** (`internal/stats/aggregator.go:217-410`):
1. **Snapshot clients** (line 229-235): Uses `sync.Map.Range()` to create regular map snapshot (lock-free)
2. **Iterate snapshot** (line 250-265): Reads all atomic fields using `.Load()`:
   - `c.ManifestRequests.Load()` (line 254)
   - `c.SegmentRequests.Load()` (line 255)
   - `c.InitRequests.Load()` (line 256)
   - `c.UnknownRequests.Load()` (line 257)
   - `c.Reconnections.Load()` (line 264)
   - `c.Timeouts.Load()` (line 265)
   - `c.GetHTTPErrors()` (line 260-262): Lock-free array iteration
3. **Calculate rates** (line 331-342): Computes overall and instantaneous rates
4. **Pipeline health** (line 288-291): Atomic loads for pipeline metrics

### AggregatedStats Structure

```go
type AggregatedStats struct {
    // Timestamp
    Timestamp time.Time

    // Client counts
    TotalClients   int
    ActiveClients  int
    StalledClients int

    // Request totals
    TotalManifestReqs int64
    TotalSegmentReqs  int64
    TotalInitReqs     int64
    TotalUnknownReqs  int64
    TotalBytes        int64

    // Rates (per second) - from start time
    ManifestReqRate       float64
    SegmentReqRate        float64
    ThroughputBytesPerSec float64

    // Instantaneous rates (per second) - from last snapshot
    InstantManifestRate   float64
    InstantSegmentRate    float64
    InstantThroughputRate float64

    // Errors
    TotalHTTPErrors    map[int]int64
    TotalReconnections int64
    TotalTimeouts      int64
    ErrorRate          float64  // errors / total requests

    // Health
    ClientsAboveRealtime int
    ClientsBelowRealtime int
    AverageSpeed          float64

    // Wall-clock Drift
    AverageDrift         time.Duration
    MaxDrift             time.Duration
    ClientsWithHighDrift int  // Drift > 5 seconds

    // Pipeline health
    TotalLinesDropped int64
    TotalLinesRead    int64
    ClientsWithDrops  int
    MetricsDegraded   bool
    PeakDropRate      float64

    // Uptime distribution
    MinUptime time.Duration
    MaxUptime time.Duration
    AvgUptime time.Duration

    // Per-client summaries (optional)
    PerClientSummaries []Summary
}
```

**Rate Calculations** (`internal/stats/aggregator.go:331-342`):
- **Overall rates**: `total / elapsed_seconds` (from aggregator start time)
- **Instantaneous rates**: `(current - previous) / snapshot_interval` (from last aggregation)

**Error Rate** (`internal/stats/aggregator.go:365-375`):
```
ErrorRate = (HTTPErrors + Timeouts) / (ManifestReqs + SegmentReqs + InitReqs)
```

**Lock-Free Design:**
- ✅ No `sync.RWMutex` - uses `sync.Map` for client storage
- ✅ All field reads use atomic `.Load()` operations
- ✅ Multiple goroutines can aggregate simultaneously without blocking
- ✅ Linear scalability with client count

---

## Prometheus Metrics

The `Collector` (`internal/metrics/collector.go`) exports metrics to Prometheus.

**Data Flow** (`internal/orchestrator/orchestrator.go:409-434`):
1. `statsUpdateLoop()` (line 410-433): Periodically calls `GetAggregatedStats()`
2. `convertToMetricsUpdate()` (line 437-510): Converts `stats.AggregatedStats` → `metrics.AggregatedStatsUpdate`
3. `RecordStats()` (line 618-780): Updates Prometheus metrics with delta calculations

**Field Mappings** (`internal/orchestrator/orchestrator.go:437-510`):
- Request totals (line 444-448): Direct mapping from `AggregatedStats`
- Error totals (line 456-459): Direct mapping, including `TotalHTTPErrors` map
- Pipeline health (line 477-488): Direct mapping with per-stream breakdown
- All values originate from atomic field reads via `.Load()` in `Aggregate()`

### Tier 1: Aggregate Metrics (Always Enabled)

**Panel 1: Test Overview** (7 metrics)
- `hls_swarm_info` (GaugeVec: version, stream_url, variant)
- `hls_swarm_target_clients` (Gauge)
- `hls_swarm_test_duration_seconds` (Gauge)
- `hls_swarm_active_clients` (Gauge)
- `hls_swarm_ramp_progress` (Gauge: 0.0-1.0)
- `hls_swarm_test_elapsed_seconds` (Gauge)
- `hls_swarm_test_remaining_seconds` (Gauge)

**Panel 2: Request Rates & Throughput** (8 metrics)
- `hls_swarm_manifest_requests_total` (Counter)
- `hls_swarm_segment_requests_total` (Counter)
- `hls_swarm_init_requests_total` (Counter)
- `hls_swarm_unknown_requests_total` (Counter)
- `hls_swarm_bytes_downloaded_total` (Counter)
- `hls_swarm_manifest_requests_per_second` (Gauge)
- `hls_swarm_segment_requests_per_second` (Gauge)
- `hls_swarm_throughput_bytes_per_second` (Gauge)

**Panel 3: Latency Distribution** (5 metrics)
- `hls_swarm_inferred_latency_seconds` (Histogram: 14 buckets)
- `hls_swarm_inferred_latency_p50_seconds` (Gauge)
- `hls_swarm_inferred_latency_p95_seconds` (Gauge)
- `hls_swarm_inferred_latency_p99_seconds` (Gauge)
- `hls_swarm_inferred_latency_max_seconds` (Gauge)

**Panel 4: Client Health & Playback** (7 metrics)
- `hls_swarm_clients_above_realtime` (Gauge)
- `hls_swarm_clients_below_realtime` (Gauge)
- `hls_swarm_stalled_clients` (Gauge)
- `hls_swarm_average_speed` (Gauge)
- `hls_swarm_high_drift_clients` (Gauge)
- `hls_swarm_average_drift_seconds` (Gauge)
- `hls_swarm_max_drift_seconds` (Gauge)

**Panel 5: Errors & Recovery** (7 metrics)
- `hls_swarm_http_errors_total` (CounterVec: status_code label)
- `hls_swarm_timeouts_total` (Counter)
- `hls_swarm_reconnections_total` (Counter)
- `hls_swarm_client_starts_total` (Counter)
- `hls_swarm_client_restarts_total` (Counter)
- `hls_swarm_client_exits_total` (CounterVec: category label)
- `hls_swarm_error_rate` (Gauge)

**Panel 6: Pipeline Health** (5 metrics)
- `hls_swarm_stats_lines_dropped_total` (CounterVec: stream label)
- `hls_swarm_stats_lines_parsed_total` (CounterVec: stream label)
- `hls_swarm_stats_clients_degraded` (Gauge)
- `hls_swarm_stats_drop_rate` (Gauge)
- `hls_swarm_stats_peak_drop_rate` (Gauge)

**Panel 7: Uptime Distribution** (4 metrics)
- `hls_swarm_client_uptime_seconds` (Histogram: 9 buckets)
- `hls_swarm_uptime_p50_seconds` (Gauge)
- `hls_swarm_uptime_p95_seconds` (Gauge)
- `hls_swarm_uptime_p99_seconds` (Gauge)

**Total Tier 1:** 43 metrics, ~50 time series (constant cardinality)

### Tier 2: Per-Client Metrics (Optional, `--prom-client-metrics`)

**WARNING:** High cardinality! Use only with <200 clients.

- `hls_swarm_client_speed` (GaugeVec: client_id label)
- `hls_swarm_client_drift_seconds` (GaugeVec: client_id label)
- `hls_swarm_client_bytes_total` (GaugeVec: client_id label)

**Total Tier 2:** 3 metrics, 3 × N time series (where N = client count)

---

## Metric Collection Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                    FFmpeg Process                                │
│                                                                   │
│  stdout: -progress pipe:1                                        │
│    │                                                              │
│    ├─► ProgressParser.ParseLine()                                │
│    │   └─► ProgressCallback()                                    │
│    │       └─► ClientStats.UpdateCurrentBytes()                  │
│    │       └─► ClientStats.UpdateSpeed()                         │
│    │       └─► ClientStats.UpdateDrift()                          │
│    │                                                              │
│  stderr: -loglevel verbose                                       │
│    │                                                              │
│    ├─► HLSEventParser.ParseLine()                                  │
│    │   ├─► Opening URL → IncrementManifestRequests()            │
│    │   ├─► Opening URL → IncrementSegmentRequests()              │
│    │   ├─► HTTP Error → RecordHTTPError()                        │
│    │   ├─► Reconnect → RecordReconnection()                      │
│    │   └─► Timeout → RecordTimeout()                             │
│    │                                                              │
│    └─► On Progress Update                                        │
│        └─► CompleteOldestSegment() → RecordLatency()             │
│                                                                   │
└─────────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│              ClientStats (Per-Client Storage)                     │
│                                                                   │
│  - Request counters (atomic.Int64, lock-free)                   │
│  - Bytes tracking (atomic.Int64, restart-aware, lock-free)       │
│  - Error counts (atomic.Int64 + [201]atomic.Int64 array)         │
│  - Speed & drift (atomic.Uint64/Int64, lock-free)                 │
│  - Pipeline health (atomic.Int64, lock-free)                    │
│                                                                   │
│  All operations: .Add(), .Load(), .Store() - no mutexes         │
│                                                                   │
└─────────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│              StatsAggregator.Aggregate()                         │
│              (Lock-free: sync.Map + atomic.Load())              │
│                                                                   │
│  1. Snapshot clients (sync.Map.Range())                          │
│  2. Iterate snapshot, read atomic fields (.Load())              │
│  3. Sum all client metrics (lock-free)                            │
│  4. Calculate rates                                               │
│  5. Track pipeline health                                        │
│                                                                   │
└─────────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│              Collector.RecordStats()                            │
│                                                                   │
│  - Update Prometheus metrics (delta-based)                       │
│  - Export to /metrics endpoint                                   │
│                                                                   │
└─────────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│              Exit Summary / TUI                                  │
│                                                                   │
│  - Format aggregated stats                                        │
│  - Display in terminal or exit report                            │
│                                                                   │
└─────────────────────────────────────────────────────────────────┘
```

---

## Summary

### Metrics Collected Per Client

**From Progress Parser (stdout):**
- Frame count, FPS, bitrate
- **Total bytes downloaded** (restart-aware)
- **Playback position** (OutTimeUS)
- **Playback speed** (for stall detection)
- Progress status

**From HLS Event Parser (stderr):**
- **Request counts**: Manifest, Segment, Init, Unknown
- **HTTP errors** (by status code)
- **Reconnections** and **timeouts**
- **Inferred latency** (from segment request timing)

**Derived Metrics:**
- **Wall-clock drift** (playback vs real-time)
- **Stall detection** (speed < 0.9 for >5s)
- **Segment size estimates** (from bytes delta)
- **Pipeline health** (dropped lines, drop rate)

**Total:** ~25 distinct metrics per client, aggregated across all clients for reporting.

### Key Characteristics

- **Thread-safe**: All metrics use **lock-free atomic operations** (no mutexes)
- **High performance**: ~8.8ns for counter increments, ~24μs for 100-client aggregation
- **Scalable**: Linear scaling with client count, no mutex contention
- **Restart-aware**: Bytes accumulate across FFmpeg process restarts
- **Memory-efficient**: T-Digest for percentiles, ring buffers for samples
- **Lossy-by-design**: Drops lines under backpressure to prevent blocking
- **Real-time**: Metrics updated as FFmpeg emits events
- **Comprehensive**: Covers requests, errors, latency, health, and pipeline status

### Atomic Migration Status

✅ **Completed** (2026-01-22): All fields migrated to atomic operations
- 12 fields converted to `atomic.Int64` or `atomic.Uint64`
- HTTP errors: `map[int]int64` + mutex → `[201]atomic.Int64` array
- Aggregator: `sync.RWMutex` → `sync.Map` (lock-free)
- See [ATOMIC_MIGRATION_PLAN.md](ATOMIC_MIGRATION_PLAN.md) for details
- See [ATOMIC_MIGRATION_PLAN_LOG.md](ATOMIC_MIGRATION_PLAN_LOG.md) for implementation log
- See [ATOMIC_MIGRATION_PERFORMANCE.md](ATOMIC_MIGRATION_PERFORMANCE.md) for benchmarks

---

## Related Documentation

- [METRICS_ENHANCEMENT_DESIGN.md](METRICS_ENHANCEMENT_DESIGN.md) - Design rationale and architecture
- [METRICS_ENHANCEMENT_IMPLEMENTATION_LOG.md](METRICS_ENHANCEMENT_IMPLEMENTATION_LOG.md) - Implementation details
- [OBSERVABILITY.md](OBSERVABILITY.md) - Prometheus metrics and logging
- [FFMPEG_HLS_REFERENCE.md](FFMPEG_HLS_REFERENCE.md) - FFmpeg HLS behavior reference
