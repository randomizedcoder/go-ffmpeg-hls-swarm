# Metrics Enhancement Design: FFmpeg Output Parsing

> **Status**: DESIGN DRAFT
> **Date**: 2026-01-22
> **Goal**: Capture detailed HLS streaming metrics from FFmpeg output to provide comprehensive load test reports

---

## Table of Contents

1. [Overview](#overview)
2. [FFmpeg Output Sources](#ffmpeg-output-sources)
3. [Per-Client Metrics](#per-client-metrics)
4. [Aggregated Metrics](#aggregated-metrics)
5. [Implementation Architecture](#implementation-architecture)
6. [Exit Summary Report](#exit-summary-report)
7. [Prometheus Integration](#prometheus-integration)
8. [Implementation Phases](#implementation-phases)

---

## Overview

### Problem Statement

Currently, `go-ffmpeg-hls-swarm` reports only:
- Client start/stop events
- Exit codes
- Uptime per client
- Restart counts

This misses critical information for load testing:
- **How many requests** were made (manifests vs segments)?
- **What was the throughput** (bytes/second)?
- **Were there errors** (HTTP 4xx/5xx, timeouts)?
- **What was the latency** for segment downloads?

### Solution

FFmpeg provides detailed progress information via two mechanisms:
1. **`-progress pipe:1`** → Structured key=value pairs to stdout
2. **`-loglevel verbose`** → HLS-specific events to stderr

By capturing and parsing this output, we can provide comprehensive metrics.

---

## FFmpeg Output Sources

### Source 1: Progress Output (`-progress pipe:1`)

FFmpeg writes structured progress data to stdout when `-progress pipe:1` is used:

```
frame=0
fps=0.00
stream_0_0_q=-1.0
bitrate=N/A
total_size=51324
out_time_us=2000000
out_time_ms=2000
out_time=00:00:02.000000
dup_frames=0
drop_frames=0
speed=1.00x
progress=continue
```

**Key fields for HLS load testing:**

| Field | Description | Use Case |
|-------|-------------|----------|
| `total_size` | Total bytes downloaded | Throughput calculation |
| `out_time_us` | Playback position (microseconds) | Stream health |
| `speed` | Playback speed (1.00x = real-time) | Detect buffering/stalls |
| `bitrate` | Current bitrate | Bandwidth usage |
| `progress` | `continue` or `end` | Stream state |

**Output frequency:** Every ~500ms by default, configurable with `-stats_period`

### Source 2: HLS Events (`-loglevel verbose`)

With verbose logging, FFmpeg logs HLS-specific events to stderr:

```
[hls @ 0x55f8a1b2c3d0] Opening 'http://10.177.0.10:17080/stream.m3u8' for reading
[hls @ 0x55f8a1b2c3d0] HLS request for url 'http://10.177.0.10:17080/stream.m3u8', offset 0
[https @ 0x55f8a1b2c3e0] Opening 'http://10.177.0.10:17080/seg00123.ts' for reading
[hls @ 0x55f8a1b2c3d0] HLS request for url 'http://10.177.0.10:17080/seg00123.ts', offset 0
```

**Parseable events:**

| Pattern | Event Type | Data Extracted |
|---------|------------|----------------|
| `Opening '...' for reading` | HTTP Request | URL, type (manifest/segment) |
| `HLS request for url` | HLS Fetch | URL, offset |
| `Skip ('#EXT-X-BYTERANGE' tag)` | Parse Warning | Unsupported feature |
| `Server returned 4xx` | HTTP Error | Status code |
| `Reconnecting to ...` | Reconnection | Retry event |
| `Connection timed out` | Timeout | Network issue |

### Source 3: HTTP Protocol Stats (`-loglevel debug`)

Even more detailed with debug level (high volume):

```
[tcp @ 0x...] Starting connection attempt to 10.177.0.10 port 17080
[tcp @ 0x...] Successfully connected to 10.177.0.10 port 17080
[http @ 0x...] request: GET /stream.m3u8 HTTP/1.1
[http @ 0x...] header: HTTP/1.1 200 OK
[http @ 0x...] header: Content-Length: 375
```

> ⚠️ **Note**: Debug level generates ~100 lines/second per client. Use selectively.

---

## Per-Client Metrics

### Proposed Client Stats Structure

```go
// ClientStats holds metrics for a single FFmpeg client
type ClientStats struct {
    ClientID    int
    StartTime   time.Time

    // From -progress output
    BytesDownloaded   int64     // total_size
    PlaybackPosition  time.Duration // out_time_us
    CurrentSpeed      float64   // speed (1.0 = realtime)
    CurrentBitrate    string    // bitrate

    // From stderr parsing
    ManifestRequests  int64     // Count of .m3u8 requests
    SegmentRequests   int64     // Count of .ts requests
    HTTPErrors        map[int]int64  // Status code -> count
    Reconnections     int64
    Timeouts          int64

    // Calculated
    SegmentLatencies  []time.Duration // Ring buffer of recent latencies

    // State
    LastUpdate        time.Time
    IsStalled         bool      // speed < 0.9 for > 5 seconds
}
```

### Metrics to Track Per Client

| Metric | Source | Collection Method |
|--------|--------|-------------------|
| Bytes downloaded | `-progress` stdout | Parse `total_size` |
| Playback position | `-progress` stdout | Parse `out_time_us` |
| Playback speed | `-progress` stdout | Parse `speed` |
| Manifest requests | stderr | Count `Opening '...m3u8'` |
| Segment requests | stderr | Count `Opening '...ts'` |
| HTTP 4xx errors | stderr | Parse `Server returned 4xx` |
| HTTP 5xx errors | stderr | Parse `Server returned 5xx` |
| Reconnections | stderr | Count `Reconnecting` |
| Timeouts | stderr | Count `timed out` |
| Segment latency | stderr | Time between request start/complete |

### Latency Measurement

To measure segment download latency, we need to correlate:
1. `Opening 'http://.../seg00123.ts' for reading` (start)
2. Next `Opening` or progress update (end)

This requires state tracking per client:

```go
type LatencyTracker struct {
    currentSegment string
    requestStart   time.Time
}

func (t *LatencyTracker) OnOpen(url string) {
    if strings.HasSuffix(url, ".ts") {
        t.currentSegment = url
        t.requestStart = time.Now()
    }
}

func (t *LatencyTracker) OnProgress() time.Duration {
    if t.currentSegment != "" {
        latency := time.Since(t.requestStart)
        t.currentSegment = ""
        return latency
    }
    return 0
}
```

---

## Aggregated Metrics

### Real-Time Aggregation

The orchestrator should aggregate stats across all clients in real-time:

```go
// AggregatedStats holds metrics across all clients
type AggregatedStats struct {
    // Counts
    TotalClients        int
    ActiveClients       int
    StalledClients      int

    // Request totals
    TotalManifestReqs   int64
    TotalSegmentReqs    int64
    TotalBytesDownloaded int64

    // Request rates (per second)
    ManifestReqRate     float64
    SegmentReqRate      float64
    ThroughputBytesPerSec float64

    // Errors
    TotalHTTPErrors     map[int]int64  // Status code -> count
    TotalReconnections  int64
    TotalTimeouts       int64

    // Latency distribution (calculated from all clients)
    SegmentLatencyP50   time.Duration
    SegmentLatencyP95   time.Duration
    SegmentLatencyP99   time.Duration
    SegmentLatencyMax   time.Duration

    // Health
    ClientsAboveRealtime int  // speed >= 1.0
    ClientsBelowRealtime int  // speed < 1.0
    AverageSpeed         float64
}
```

### Percentile Calculation

For latency percentiles, use a streaming algorithm or sample reservoir:

```go
// Using a T-Digest or HDR Histogram for memory-efficient percentiles
type LatencyHistogram struct {
    digest *tdigest.TDigest
}

func (h *LatencyHistogram) Add(d time.Duration) {
    h.digest.Add(float64(d.Microseconds()), 1)
}

func (h *LatencyHistogram) Percentile(p float64) time.Duration {
    return time.Duration(h.digest.Quantile(p)) * time.Microsecond
}
```

---

## Implementation Architecture

### Data Flow

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           Per FFmpeg Process                            │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌─────────────┐      stdout        ┌──────────────────────┐           │
│  │   FFmpeg    │──────────────────▶│  ProgressParser      │           │
│  │  -progress  │                    │  (parse key=value)   │           │
│  │   pipe:1    │      stderr        ├──────────────────────┤           │
│  │  -loglevel  │──────────────────▶│  StderrParser        │           │
│  │   verbose   │                    │  (parse HLS events)  │           │
│  └─────────────┘                    └──────────┬───────────┘           │
│                                                 │                       │
│                                                 ▼                       │
│                                     ┌──────────────────────┐           │
│                                     │    ClientStats       │           │
│                                     │  (per-client state)  │           │
│                                     └──────────┬───────────┘           │
│                                                 │                       │
└─────────────────────────────────────────────────┼───────────────────────┘
                                                  │
                                                  │ events/stats
                                                  ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                            Orchestrator                                 │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌──────────────────┐        ┌──────────────────┐                      │
│  │  StatsCollector  │◀──────│  Client 0 Stats   │                      │
│  │                  │◀──────│  Client 1 Stats   │                      │
│  │  - aggregate     │◀──────│  Client 2 Stats   │                      │
│  │  - percentiles   │◀──────│  ...              │                      │
│  │  - rates         │        └──────────────────┘                      │
│  └────────┬─────────┘                                                  │
│           │                                                            │
│           ▼                                                            │
│  ┌──────────────────┐        ┌──────────────────┐                      │
│  │ Prometheus       │        │  Exit Summary    │                      │
│  │ Metrics          │        │  Report          │                      │
│  └──────────────────┘        └──────────────────┘                      │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### Modified Supervisor

```go
// supervisor/supervisor.go - modifications needed

func (s *Supervisor) runOnce(ctx context.Context) (exitCode int, uptime time.Duration, err error) {
    cmd, err := s.builder.BuildCommand(ctx, s.clientID)
    if err != nil {
        return 1, 0, err
    }

    // NEW: Capture stdout for -progress output
    stdout, err := cmd.StdoutPipe()
    if err != nil {
        return 1, 0, err
    }

    // NEW: Capture stderr for HLS events
    stderr, err := cmd.StderrPipe()
    if err != nil {
        return 1, 0, err
    }

    cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

    if err := cmd.Start(); err != nil {
        return 1, 0, err
    }

    // NEW: Start output parsers in goroutines
    var wg sync.WaitGroup

    wg.Add(1)
    go func() {
        defer wg.Done()
        s.progressParser.Parse(stdout)  // Parse -progress output
    }()

    wg.Add(1)
    go func() {
        defer wg.Done()
        s.stderrParser.Parse(stderr)    // Parse HLS events
    }()

    // Wait for process to exit
    waitErr := cmd.Wait()

    // Wait for parsers to finish
    wg.Wait()

    // ... rest of exit handling
}
```

### Progress Parser

```go
// parser/progress.go

type ProgressParser struct {
    clientID int
    stats    *ClientStats
    callback func(*ProgressUpdate)
}

type ProgressUpdate struct {
    TotalSize    int64
    OutTimeUS    int64
    Speed        float64
    Bitrate      string
    Progress     string  // "continue" or "end"
}

func (p *ProgressParser) Parse(r io.Reader) {
    scanner := bufio.NewScanner(r)
    update := &ProgressUpdate{}

    for scanner.Scan() {
        line := scanner.Text()

        // Parse key=value format
        if idx := strings.Index(line, "="); idx > 0 {
            key := line[:idx]
            value := line[idx+1:]

            switch key {
            case "total_size":
                update.TotalSize, _ = strconv.ParseInt(value, 10, 64)
            case "out_time_us":
                update.OutTimeUS, _ = strconv.ParseInt(value, 10, 64)
            case "speed":
                // Parse "1.00x" -> 1.00
                update.Speed, _ = strconv.ParseFloat(strings.TrimSuffix(value, "x"), 64)
            case "bitrate":
                update.Bitrate = value
            case "progress":
                update.Progress = value
                // End of update block, emit
                if p.callback != nil {
                    p.callback(update)
                }
                update = &ProgressUpdate{}
            }
        }
    }
}
```

### HLS Event Parser

```go
// parser/hls_events.go

type HLSEventParser struct {
    clientID int
    stats    *ClientStats

    // For latency tracking
    currentRequest   string
    requestStartTime time.Time
}

var (
    reOpening = regexp.MustCompile(`Opening '([^']+)' for reading`)
    reHLSReq  = regexp.MustCompile(`HLS request for url '([^']+)'`)
    reHTTPErr = regexp.MustCompile(`Server returned (\d{3})`)
    reReconn  = regexp.MustCompile(`Reconnecting to`)
    reTimeout = regexp.MustCompile(`(timed out|timeout)`)
)

func (p *HLSEventParser) Parse(r io.Reader) {
    scanner := bufio.NewScanner(r)

    for scanner.Scan() {
        line := scanner.Text()
        p.parseLine(line)
    }
}

func (p *HLSEventParser) parseLine(line string) {
    // Opening URL for reading
    if m := reOpening.FindStringSubmatch(line); m != nil {
        url := m[1]

        // Complete previous request (calculate latency)
        if p.currentRequest != "" && strings.HasSuffix(p.currentRequest, ".ts") {
            latency := time.Since(p.requestStartTime)
            p.stats.RecordSegmentLatency(latency)
        }

        // Track new request
        p.currentRequest = url
        p.requestStartTime = time.Now()

        // Count by type
        if strings.Contains(url, ".m3u8") {
            atomic.AddInt64(&p.stats.ManifestRequests, 1)
        } else if strings.HasSuffix(url, ".ts") {
            atomic.AddInt64(&p.stats.SegmentRequests, 1)
        }
        return
    }

    // HTTP errors
    if m := reHTTPErr.FindStringSubmatch(line); m != nil {
        code, _ := strconv.Atoi(m[1])
        p.stats.RecordHTTPError(code)
        return
    }

    // Reconnections
    if reReconn.MatchString(line) {
        atomic.AddInt64(&p.stats.Reconnections, 1)
        return
    }

    // Timeouts
    if reTimeout.MatchString(line) {
        atomic.AddInt64(&p.stats.Timeouts, 1)
        return
    }
}
```

---

## Exit Summary Report

### Enhanced Exit Summary

```
═══════════════════════════════════════════════════════════════════════════════
                        go-ffmpeg-hls-swarm Exit Summary
═══════════════════════════════════════════════════════════════════════════════

Run Configuration:
  Duration:              00:05:00
  Target Clients:        100
  Ramp Rate:             20/sec
  Origin:                http://10.177.0.10:17080/stream.m3u8

Client Health:
  Peak Active:           100
  Final Active:          100
  Total Restarts:        0
  Stalled Clients:       0

───────────────────────────────────────────────────────────────────────────────
                              Request Statistics
───────────────────────────────────────────────────────────────────────────────

  Request Type          Total        Rate (/sec)     Per Client
  ─────────────────────────────────────────────────────────────
  Manifest (.m3u8)      15,000       50.0            150
  Segments (.ts)        75,000       250.0           750

  Total Bytes:          3.75 GB      12.5 MB/s       37.5 MB/client

───────────────────────────────────────────────────────────────────────────────
                             Segment Latency
───────────────────────────────────────────────────────────────────────────────

  Percentile            Latency
  ─────────────────────────────
  P50 (median)          12 ms
  P75                   18 ms
  P90                   25 ms
  P95                   35 ms
  P99                   52 ms
  Max                   210 ms

  Distribution:
    0-10ms   ████████████████████░░░░░░░░░░  40%
    10-20ms  ████████████████░░░░░░░░░░░░░░  35%
    20-50ms  ████████░░░░░░░░░░░░░░░░░░░░░░  20%
    50-100ms ██░░░░░░░░░░░░░░░░░░░░░░░░░░░░   4%
    >100ms   ░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░   1%

───────────────────────────────────────────────────────────────────────────────
                            Playback Health
───────────────────────────────────────────────────────────────────────────────

  Speed Distribution:
    >= 1.0x (healthy)    98 clients (98%)
    0.9-1.0x (marginal)   2 clients  (2%)
    < 0.9x (buffering)    0 clients  (0%)

  Average Speed:         1.02x

───────────────────────────────────────────────────────────────────────────────
                               Errors
───────────────────────────────────────────────────────────────────────────────

  Type                  Count       Rate (/min)
  ───────────────────────────────────────────────
  HTTP 404              0           0.0
  HTTP 503              0           0.0
  Timeouts              2           0.4
  Reconnections         3           0.6

  Error Rate:           0.007% of requests

───────────────────────────────────────────────────────────────────────────────
                           Client Uptime
───────────────────────────────────────────────────────────────────────────────

  P50:                   04:58
  P95:                   04:59
  P99:                   05:00

  Exit Codes:
    137 (SIGKILL)        100 (graceful shutdown)

═══════════════════════════════════════════════════════════════════════════════
Prometheus metrics were available at: http://0.0.0.0:17091/metrics
═══════════════════════════════════════════════════════════════════════════════
```

---

## Prometheus Integration

### New Metrics

```go
// metrics/collector.go additions

var (
    // Request counters
    hlsManifestRequests = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "hls_swarm_manifest_requests_total",
            Help: "Total number of manifest (.m3u8) requests",
        },
        []string{"client_id"},
    )

    hlsSegmentRequests = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "hls_swarm_segment_requests_total",
            Help: "Total number of segment (.ts) requests",
        },
        []string{"client_id"},
    )

    // Bytes downloaded
    hlsBytesDownloaded = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "hls_swarm_bytes_downloaded_total",
            Help: "Total bytes downloaded",
        },
        []string{"client_id"},
    )

    // Inferred latency histogram (from FFmpeg events, not directly measured)
    hlsInferredLatency = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "hls_swarm_inferred_latency_seconds",
            Help:    "Inferred segment download latency in seconds (from FFmpeg events; use for trends)",
            Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5},
        },
        []string{"client_id"},
    )

    // Playback speed
    hlsPlaybackSpeed = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "hls_swarm_playback_speed",
            Help: "Current playback speed (1.0 = realtime)",
        },
        []string{"client_id"},
    )

    // Error counters
    hlsHTTPErrors = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "hls_swarm_http_errors_total",
            Help: "HTTP errors by status code",
        },
        []string{"client_id", "status_code"},
    )

    hlsReconnections = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "hls_swarm_reconnections_total",
            Help: "Number of reconnection attempts",
        },
        []string{"client_id"},
    )
)
```

### Example Prometheus Queries

```promql
# Request rate across all clients
sum(rate(hls_swarm_segment_requests_total[1m]))

# Inferred segment latency P99 (use for trends, not absolutes)
histogram_quantile(0.99, sum(rate(hls_swarm_inferred_latency_seconds_bucket[1m])) by (le))

# Throughput in MB/s
sum(rate(hls_swarm_bytes_downloaded_total[1m])) / 1024 / 1024

# Clients with playback issues (speed < 0.9)
count(hls_swarm_playback_speed < 0.9)

# Error rate percentage
sum(rate(hls_swarm_http_errors_total[1m])) / sum(rate(hls_swarm_segment_requests_total[1m])) * 100
```

---

## Implementation Phases

### Phase 1: Basic Output Capture (Foundation)

**Scope:**
- Attach stdout/stderr pipes to FFmpeg processes
- Add `-progress pipe:1` and `-loglevel verbose` to FFmpeg args
- Parse progress output for `total_size`, `speed`
- Display basic stats in exit summary

**Effort:** 2-3 days

**Exit Summary adds:**
- Total bytes downloaded
- Average playback speed

### Phase 2: HLS Event Parsing

**Scope:**
- Implement HLS stderr parser
- Count manifest/segment requests
- Track HTTP errors, reconnections, timeouts
- Add request counts to exit summary

**Effort:** 2-3 days

**Exit Summary adds:**
- Request counts (manifest/segment)
- Error counts and rates

### Phase 3: Latency Measurement

**Scope:**
- Implement latency tracker (request start → complete)
- Use streaming percentile algorithm (T-Digest)
- Display latency distribution in exit summary

**Effort:** 2-3 days

**Exit Summary adds:**
- Segment latency P50/P95/P99
- Latency histogram visualization

### Phase 4: Real-Time Aggregation

**Scope:**
- Implement StatsCollector for real-time aggregation
- Add live throughput and request rate calculation
- Calculate request rates per second

**Effort:** 2-3 days

**Exit Summary adds:**
- Request rates (per second)
- Throughput (MB/s)

### Phase 5: Prometheus Integration

**Scope:**
- Add new Prometheus metrics
- Wire up collectors to stats updates
- Document Prometheus queries

**Effort:** 1-2 days

**Adds:**
- Prometheus metrics for Grafana dashboards
- Real-time monitoring capability

### Phase 6: Live Terminal Dashboard

**Scope:**
- Real-time TUI showing live stats using Bubble Tea
- Update every 500ms during test
- Show client ramp progress, live metrics, error counts
- Graceful fallback to simple output when not a TTY

**Effort:** 3-5 days

See [Live Dashboard Design](#live-dashboard-design) section below for details.

---

## CLI Flags

New flags to control metrics collection:

```
Metrics Collection:
  -stats-enabled        Enable detailed FFmpeg output parsing (default: true)
  -stats-loglevel       FFmpeg loglevel for parsing: verbose|debug (default: verbose)
  -stats-period         Progress update period in seconds (default: 0.5)
  -latency-buckets      Custom latency histogram buckets (default: standard)
```

---

## Critical Design Considerations

### 1. The "Final Drain" Problem

When FFmpeg exits, the OS pipe buffer may still contain unread data. If parsers exit
prematurely when `cmd.Wait()` returns, the exit summary will miss final segment counts.

**Solution:** Parsers must read until EOF, not until the process exits:

```go
func (s *Supervisor) runOnce(ctx context.Context) (...) {
    // ... start process ...

    // Start parsers BEFORE cmd.Start() returns
    var parseWg sync.WaitGroup
    parseWg.Add(2)

    go func() {
        defer parseWg.Done()
        // Parse will block until EOF (pipe closed by kernel after process exit)
        s.progressParser.Parse(stdout)
    }()

    go func() {
        defer parseWg.Done()
        s.stderrParser.Parse(stderr)
    }()

    // Wait for process to exit
    waitErr := cmd.Wait()

    // CRITICAL: Wait for parsers to drain remaining pipe data
    // Add timeout to prevent hanging on malformed output
    done := make(chan struct{})
    go func() {
        parseWg.Wait()
        close(done)
    }()

    const drainTimeout = 5 * time.Second

    select {
    case <-done:
        // Parsers finished normally
    case <-time.After(drainTimeout):
        s.logger.Warn("parser_drain_timeout",
            "client_id", s.clientID,
            "timeout", drainTimeout.String(),
            "reason", "parsers did not finish reading pipe data within timeout",
        )
    }

    // Now safe to read final stats
}
```

### 2. Parallel Segment Fetch Handling

FFmpeg occasionally pre-fetches segments or refreshes manifests in parallel. Using a
single `currentSegmentURL` will produce inaccurate latencies.

**Solution:** Use a map to track multiple inflight requests:

```go
// ClientStats - updated for parallel requests
type ClientStats struct {
    // ... existing fields ...

    // Track multiple inflight requests
    inflightRequests sync.Map  // map[string]time.Time (URL -> start time)
}

func (s *ClientStats) OnSegmentRequestStart(url string) {
    s.inflightRequests.Store(url, time.Now())
    atomic.AddInt64(&s.SegmentRequests, 1)
}

func (s *ClientStats) OnSegmentRequestComplete(url string) {
    if startTime, ok := s.inflightRequests.LoadAndDelete(url); ok {
        latency := time.Since(startTime.(time.Time))
        s.recordLatency(latency)
    }
}

const HangingRequestTTL = 60 * time.Second

// Called when we see progress update (segment likely completed)
// Also cleans up "hanging" requests to prevent memory leaks
func (s *ClientStats) OnProgressUpdate(totalSize int64) {
    var oldestURL string
    var oldestTime time.Time
    var hangingURLs []string
    now := time.Now()

    s.inflightRequests.Range(func(key, value interface{}) bool {
        url := key.(string)
        startTime := value.(time.Time)

        // CRITICAL: Clean up hanging requests (older than TTL)
        // If a request starts but never completes (e.g., connection dropped
        // without error), it would stay in sync.Map forever, leaking memory.
        if now.Sub(startTime) > HangingRequestTTL {
            hangingURLs = append(hangingURLs, url)
            return true
        }

        if strings.HasSuffix(url, ".ts") {
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

    if oldestURL != "" {
        s.OnSegmentRequestComplete(oldestURL)
    }
}
```

### 3. Wall-Clock Drift Metric

For HLS load testing, **drift** is a critical "breaking point" indicator:

```
Drift = (time.Now() - StartTime) - (OutTimeUS / 1_000_000)
```

If drift grows, the network cannot keep up with the stream's bitrate, even before
FFmpeg reports errors.

**Add to ClientStats:**

```go
type ClientStats struct {
    // ... existing fields ...

    // Drift tracking
    StartTime         time.Time
    LastPlaybackTime  time.Duration  // OutTimeUS converted
    CurrentDrift      time.Duration  // Wall-clock - playback-clock
    MaxDrift          time.Duration
}

func (s *ClientStats) UpdateDrift(outTimeUS int64) {
    playbackTime := time.Duration(outTimeUS) * time.Microsecond
    s.LastPlaybackTime = playbackTime

    wallClockElapsed := time.Since(s.StartTime)
    s.CurrentDrift = wallClockElapsed - playbackTime

    if s.CurrentDrift > s.MaxDrift {
        s.MaxDrift = s.CurrentDrift
    }
}
```

**Add to AggregatedStats:**

```go
type AggregatedStats struct {
    // ... existing fields ...

    // Drift metrics
    AverageDrift time.Duration
    MaxDrift     time.Duration
    ClientsWithHighDrift int  // Drift > 5 seconds
}
```

### 4. FFmpeg `total_size` Reset on Restart

When FFmpeg restarts (client failure + recovery), the `-progress` pipe's `total_size` resets
to zero. Without handling this, aggregated "Total Bytes Downloaded" will drop or jitter.

**Problem:**
```
Client 1: total_size = 50MB → restart → total_size = 2MB
Aggregator sees: 50MB → 2MB (WRONG - should be 52MB)
```

**Solution:** Track cumulative bytes across restarts in `ClientStats`:

```go
type ClientStats struct {
    // ... other fields ...

    // Bytes tracking (handles FFmpeg restarts)
    bytesFromPreviousRuns int64  // Sum of all previous FFmpeg instances
    currentProcessBytes   int64  // Current FFmpeg's total_size
}

// Called when FFmpeg process starts
func (s *ClientStats) OnProcessStart() {
    // Accumulate bytes from the process that just ended
    s.bytesFromPreviousRuns += s.currentProcessBytes
    s.currentProcessBytes = 0
}

// Called on each progress update
func (s *ClientStats) UpdateBytes(totalSize int64) {
    s.currentProcessBytes = totalSize
}

// Returns true cumulative bytes for aggregation
func (s *ClientStats) TotalBytes() int64 {
    return s.bytesFromPreviousRuns + s.currentProcessBytes
}
```

**Supervisor integration:**
```go
func (s *Supervisor) runOnce(ctx context.Context) (...) {
    // Before starting new process, accumulate previous bytes
    if s.clientStats != nil {
        s.clientStats.OnProcessStart()
    }

    // ... start FFmpeg ...
}
```

### 5. Segment Size Estimation

FFmpeg doesn't report individual segment sizes, but we can estimate from `total_size` deltas:

```go
type ClientStats struct {
    // ... existing fields ...

    // Segment size tracking
    lastTotalSize     int64
    segmentSizes      []int64  // Ring buffer
    segmentSizeIdx    int
}

func (s *ClientStats) UpdateFromProgress(p *ProgressUpdate) {
    // Estimate segment size from total_size delta
    if s.lastTotalSize > 0 && p.TotalSize > s.lastTotalSize {
        segmentSize := p.TotalSize - s.lastTotalSize
        s.recordSegmentSize(segmentSize)
    }
    s.lastTotalSize = p.TotalSize

    // ... rest of update logic ...
}

// Add to exit summary
// Average Segment Size: 51.2 KB
// Min/Max: 48.1 KB / 54.3 KB
```

### 5. Lossy-by-Design Parsing Architecture

At 200–1000 clients, parsing bursts can't always keep up. The metrics system must never
sabotage the load test itself.

**Three-Layer Architecture:**

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          Per-Client Pipeline                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│   FFmpeg Process                                                            │
│        │                                                                    │
│        │ stdout/stderr (OS pipe)                                            │
│        ▼                                                                    │
│   ┌─────────────────┐                                                       │
│   │  Reader Goroutine│  ← Fast: only reads lines, never blocks              │
│   │  (Layer 1)       │                                                      │
│   └────────┬────────┘                                                       │
│            │                                                                │
│            │ bounded channel (StatsBufferSize lines)                        │
│            │ ← DROPS lines if full, increments counter                      │
│            ▼                                                                │
│   ┌─────────────────┐                                                       │
│   │  Parser Goroutine│  ← Parses at its own pace                            │
│   │  (Layer 2)       │                                                      │
│   └────────┬────────┘                                                       │
│            │                                                                │
│            │ stat updates (atomic/channel)                                  │
│            ▼                                                                │
│   ┌─────────────────┐                                                       │
│   │  ClientStats     │  ← Thread-safe stat storage                          │
│   │  (Layer 3)       │                                                      │
│   └─────────────────┘                                                       │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

**Key Principle:** When the bounded channel fills, **drop lines intentionally** rather than
blocking the reader (which would block FFmpeg).

**Implementation:**

```go
// internal/parser/pipeline.go

// Pipeline manages the three-layer parsing architecture
type Pipeline struct {
    clientID     int
    streamType   string  // "progress" or "stderr"
    bufferSize   int

    lineChan     chan string
    stats        *stats.ClientStats

    // Metrics about the pipeline itself
    linesRead    int64
    linesDropped int64
    linesParsed  int64
}

// NewPipeline creates a lossy-by-design parsing pipeline
func NewPipeline(clientID int, streamType string, bufferSize int, stats *stats.ClientStats) *Pipeline {
    return &Pipeline{
        clientID:   clientID,
        streamType: streamType,
        bufferSize: bufferSize,
        lineChan:   make(chan string, bufferSize),
        stats:      stats,
    }
}

// Layer 1: Reader - runs in dedicated goroutine, never blocks
func (p *Pipeline) RunReader(r io.Reader) {
    scanner := bufio.NewScanner(r)
    buf := make([]byte, 64*1024)
    scanner.Buffer(buf, 1024*1024)

    for scanner.Scan() {
        line := scanner.Text()
        atomic.AddInt64(&p.linesRead, 1)

        // Non-blocking send - drop if channel full
        select {
        case p.lineChan <- line:
            // Sent successfully
        default:
            // Channel full - drop line intentionally
            atomic.AddInt64(&p.linesDropped, 1)
        }
    }

    // Signal EOF
    close(p.lineChan)
}

// Layer 2: Parser - runs in dedicated goroutine, processes at own pace
func (p *Pipeline) RunParser(parser LineParser) {
    for line := range p.lineChan {
        parser.ParseLine(line)
        atomic.AddInt64(&p.linesParsed, 1)
    }
}

// GetDropRate returns the percentage of lines dropped
func (p *Pipeline) GetDropRate() float64 {
    read := atomic.LoadInt64(&p.linesRead)
    dropped := atomic.LoadInt64(&p.linesDropped)
    if read == 0 {
        return 0
    }
    return float64(dropped) / float64(read) * 100
}

// IsDegraded returns true if drop rate exceeds configured threshold (default: 1%)
// The threshold is configurable via --stats-drop-threshold (hidden flag)
func (p *Pipeline) IsDegraded() bool {
    return p.GetDropRate() > 1.0
}
```

**Supervisor Integration:**

```go
func (s *Supervisor) runOnce(ctx context.Context) (...) {
    // Create pipelines
    progressPipeline := parser.NewPipeline(
        s.clientID, "progress", s.config.StatsBufferSize, s.clientStats,
    )
    stderrPipeline := parser.NewPipeline(
        s.clientID, "stderr", s.config.StatsBufferSize, s.clientStats,
    )

    // Start Layer 1 (readers) - must never block
    go progressPipeline.RunReader(stdout)
    go stderrPipeline.RunReader(stderr)

    // Start Layer 2 (parsers) - process at own pace
    var parseWg sync.WaitGroup
    parseWg.Add(2)
    go func() {
        defer parseWg.Done()
        progressPipeline.RunParser(s.progressParser)
    }()
    go func() {
        defer parseWg.Done()
        stderrPipeline.RunParser(s.hlsParser)
    }()

    // ... wait for process, then drain ...

    // Record pipeline health
    s.clientStats.RecordPipelineStats(
        progressPipeline.linesDropped,
        stderrPipeline.linesDropped,
    )
}
```

**Prometheus Metrics (Aggregate only - no client_id):**

```go
var (
    // Aggregate pipeline health (no client_id - safe for 1000+ clients)
    statsLinesDropped = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "hls_swarm_stats_lines_dropped_total",
            Help: "Lines dropped due to parser backpressure (all clients)",
        },
        []string{"stream"},  // stream: "progress" | "stderr"
    )

    statsLinesParsed = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "hls_swarm_stats_lines_parsed_total",
            Help: "Lines successfully parsed (all clients)",
        },
        []string{"stream"},
    )

    statsClientsDegraded = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "hls_swarm_stats_clients_degraded",
            Help: "Number of clients with >1% dropped lines",
        },
    )
)
```

**Exit Summary Warning:**

```
═══════════════════════════════════════════════════════════════════════════════
                        go-ffmpeg-hls-swarm Exit Summary
═══════════════════════════════════════════════════════════════════════════════

⚠️  METRICS DEGRADED: 2.3% of stderr lines dropped (12,345 / 536,000)
    Consider increasing -stats-buffer or reducing client count for accurate metrics.

Request Statistics:
  ...
```

**Config Integration:**

`StatsBufferSize` now controls the bounded channel size per client:

```go
// internal/config/config.go

type Config struct {
    // ...

    // StatsBufferSize is the bounded channel size for parsing pipeline.
    // Higher = more memory, fewer drops. Lower = less memory, more drops under load.
    // Default: 1000 lines per client (~100KB per client at 100 bytes/line)
    StatsBufferSize int `json:"stats_buffer_size"`
}

func DefaultConfig() Config {
    return Config{
        // ...
        StatsBufferSize: 1000,  // 1000 lines per client
    }
}
```

**Memory Budget:**

| Clients | Buffer Size | Lines | Memory (est.) |
|---------|-------------|-------|---------------|
| 100 | 1000 | 100K | ~10 MB |
| 500 | 1000 | 500K | ~50 MB |
| 1000 | 500 | 500K | ~50 MB |
| 1000 | 1000 | 1M | ~100 MB |

**Tuning Guidance:**

- **High accuracy needed:** `--stats-buffer 2000` (more memory)
- **Memory constrained:** `--stats-buffer 500` (accept more drops)
- **1000+ clients:** `--stats-buffer 500` recommended

### 6. FFmpeg Version Compatibility

FFmpeg log formats vary between versions (6.x, 7.x, 8.x).

**Add version check to preflight:**

```go
// internal/preflight/ffmpeg_version.go

var supportedVersions = []string{"6.", "7.", "8."}

func CheckFFmpegVersion(ffmpegPath string) (string, error) {
    out, err := exec.Command(ffmpegPath, "-version").Output()
    if err != nil {
        return "", err
    }

    // Parse "ffmpeg version 8.0 ..."
    version := parseVersion(string(out))

    supported := false
    for _, v := range supportedVersions {
        if strings.HasPrefix(version, v) {
            supported = true
            break
        }
    }

    if !supported {
        return version, fmt.Errorf("FFmpeg %s not tested; metrics parsing may be degraded", version)
    }

    return version, nil
}
```

---

## Performance Considerations

### Memory Usage

#### The Problem with Raw Latency Slices

Storing all latency samples in a `[]time.Duration` slice is dangerous at scale:

```
5,000 clients × 2 segments/sec × 3,600 seconds = 36,000,000 samples
36M samples × 8 bytes = 288 MB just for latency data!
```

#### Solution: T-Digest for Constant Memory

Use the T-Digest algorithm for percentile calculation with bounded memory:

```go
// internal/stats/histogram.go

import "github.com/influxdata/tdigest"

// LatencyHistogram provides memory-efficient percentile calculation
// Memory usage: ~5-10KB regardless of sample count
type LatencyHistogram struct {
    digest *tdigest.TDigest
    count  int64
    sum    time.Duration
    max    time.Duration
    mu     sync.Mutex
}

func NewLatencyHistogram() *LatencyHistogram {
    return &LatencyHistogram{
        digest: tdigest.NewWithCompression(100), // ~100 centroids
    }
}

func (h *LatencyHistogram) Add(d time.Duration) {
    h.mu.Lock()
    defer h.mu.Unlock()

    h.digest.Add(float64(d.Nanoseconds()), 1)
    h.count++
    h.sum += d
    if d > h.max {
        h.max = d
    }
}

func (h *LatencyHistogram) Quantile(q float64) time.Duration {
    h.mu.Lock()
    defer h.mu.Unlock()

    ns := h.digest.Quantile(q)
    return time.Duration(ns)
}

func (h *LatencyHistogram) P50() time.Duration { return h.Quantile(0.50) }
func (h *LatencyHistogram) P95() time.Duration { return h.Quantile(0.95) }
func (h *LatencyHistogram) P99() time.Duration { return h.Quantile(0.99) }
func (h *LatencyHistogram) Max() time.Duration { return h.max }
func (h *LatencyHistogram) Count() int64       { return h.count }
func (h *LatencyHistogram) Mean() time.Duration {
    if h.count == 0 {
        return 0
    }
    return h.sum / time.Duration(h.count)
}
```

#### Updated Memory Budget

| Component | Per Client | 1000 Clients |
|-----------|------------|--------------|
| ClientStats struct | ~500 bytes | 500 KB |
| T-Digest histogram | ~10 KB | 10 MB |
| Inflight requests map | ~100 bytes | 100 KB |
| Segment size ring (100) | ~800 bytes | 800 KB |
| Pipeline buffers | ~100 KB | 100 MB |
| **Total** | **~110 KB** | **~112 MB** |

**Key insight:** Memory is now **bounded** regardless of test duration.

### CPU Usage

- **Parsing overhead:** ~0.1% CPU per client for regex parsing
- **T-Digest updates:** O(log n) per sample, negligible
- **Aggregation:** Negligible (atomic operations + T-Digest merge)

### Disk I/O

- **No disk writes** for stats collection
- All in-memory

---

## Observability Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                            go-ffmpeg-hls-swarm                              │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│   Orchestrator ──► Stats Aggregator ──► Prometheus Collector                │
│                          │                      │                           │
│                          │                      ▼                           │
│                          │              /metrics endpoint (:17090)          │
│                          │                      │                           │
│                          ▼                      │                           │
│                   Built-in TUI                  │                           │
│              (zero infrastructure)              │                           │
│                                                 │                           │
└─────────────────────────────────────────────────┼───────────────────────────┘
                                                  │
                    ┌─────────────────────────────┼─────────────────────────┐
                    │                             │                         │
                    ▼                             ▼                         ▼
             Prometheus Server              curl/wget               Grafana Cloud
             (scrapes /metrics)          (quick checks)            (direct remote
                    │                                                write)
                    ▼
              Grafana Dashboard
           (PromQL rate calculations,
            historical analysis,
            alerting)
```

### Three Observability Tiers

| Tier | Tool | Infrastructure | Best For |
|------|------|----------------|----------|
| **1. Quick** | Built-in TUI | None | Dev testing, quick runs, CI |
| **2. Standard** | Prometheus + Grafana | Local containers | Detailed analysis, historical data |
| **3. Production** | Grafana Cloud | Managed | Multi-site testing, long retention |

### Design Philosophy

- **Prometheus is the source of truth** - All metrics flow through `/metrics`
- **Rate calculations in PromQL** - Grafana uses `rate()`, `increase()`, not the Go code
- **TUI is zero-infrastructure** - Works out of the box, no external dependencies
- **TUI reads internal stats directly** - Avoids HTTP overhead for responsiveness

### When to Use Each Tier

**Tier 1: Built-in TUI**
```bash
# Quick test - just run it
./go-ffmpeg-hls-swarm -clients 100 http://origin/stream.m3u8
```
- No setup required
- Instant feedback
- Great for development and quick validation
- Limited to current test session

**Tier 2: Prometheus + Grafana**
```bash
# Start with Prometheus scraping
./go-ffmpeg-hls-swarm -clients 500 -metrics-addr 0.0.0.0:17090 http://origin/stream.m3u8

# Prometheus scrape config
scrape_configs:
  - job_name: 'hls-swarm'
    static_configs:
      - targets: ['localhost:17090']
```
- Historical data across multiple test runs
- PromQL for complex queries: `rate(hls_swarm_segment_requests_total[1m])`
- Grafana dashboards with alerting
- Compare tests side-by-side

**Tier 3: Grafana Cloud (Remote Write)**
```bash
# Future: Direct remote write to Grafana Cloud
./go-ffmpeg-hls-swarm -remote-write https://prometheus-us-central1.grafana.net/api/prom/push ...
```
- Multi-site load testing
- Long-term retention
- Team visibility
- No local infrastructure

### Example PromQL Queries

```promql
# Request rate over time
rate(hls_swarm_segment_requests_total[1m])

# Inferred latency P99 from histogram (use for trends, not absolutes)
histogram_quantile(0.99, rate(hls_swarm_inferred_latency_seconds_bucket[5m]))

# Error rate
sum(rate(hls_swarm_http_errors_total[1m])) / rate(hls_swarm_segment_requests_total[1m])

# Clients struggling (speed < 1.0)
hls_swarm_stalled_clients / hls_swarm_active_clients

# Drift trend
rate(hls_swarm_average_drift_seconds[5m])
```

---

## Live Dashboard Design (Built-in TUI)

The TUI is a **"built-in Grafana lite"** - same metrics, zero infrastructure.

### Library Choice: Bubble Tea + Lipgloss

We recommend the **Charm** stack for the TUI:

| Package | Purpose |
|---------|---------|
| [bubbletea](https://github.com/charmbracelet/bubbletea) | TUI framework (Elm architecture) |
| [lipgloss](https://github.com/charmbracelet/lipgloss) | Styling (colors, borders, layout) |
| [bubbles](https://github.com/charmbracelet/bubbles) | Pre-built components (progress bars, spinners) |

**Why Bubble Tea over alternatives:**
- Modern Go idioms (functional, immutable state)
- Beautiful defaults out of the box
- Handles terminal resize automatically
- Clean separation of model/view/update
- Active development and community
- Works with piped output (graceful degradation)

### Dashboard Layout

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        go-ffmpeg-hls-swarm v1.0.0                           │
│                    http://10.177.0.10:17080/stream.m3u8                     │
├─────────────────────────────────────────────────────────────────────────────┤
│  Elapsed: 00:02:34 / 00:05:00                         [Ctrl+C to stop]     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  CLIENTS                           REQUESTS                                 │
│  ════════                          ════════                                 │
│  Target:     100                   Manifests:    4,521  (30.1/s)           │
│  Active:     100  ████████████     Segments:    22,605  (150.7/s)          │
│  Ramping:      0                   Bytes:       1.13 GB (7.5 MB/s)         │
│  Stalled:      0                                                            │
│  Restarts:     0                   ERRORS                                   │
│                                    ══════                                   │
│  LATENCY (segments)                HTTP 4xx:         0                      │
│  ═══════════════════               HTTP 5xx:         0                      │
│  P50:    12ms                      Timeouts:         2                      │
│  P95:    35ms                      Reconnects:       1                      │
│  P99:    52ms                      Error Rate:    0.01%                     │
│  Max:   210ms                                                               │
│                                    PLAYBACK                                 │
│  ┌─────────────────────────┐       ════════                                 │
│  │▓▓▓▓▓▓▓▓▓▓▓▓▓░░░░░░░░░░░│       >= 1.0x:   98 (98%)                     │
│  │0    25    50    75  100│       < 1.0x:     2  (2%)                      │
│  └─────────────────────────┘       Avg Speed:  1.02x                        │
│   Latency Distribution (ms)                                                 │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│  Recent Events:                                                             │
│  16:48:32  Client 47 started (pid 12345)                                   │
│  16:48:31  Client 46 started (pid 12344)                                   │
│  16:48:30  All 100 clients active                                          │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Architecture with TUI

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              Main Program                                   │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌───────────────┐    stats channel    ┌────────────────────┐              │
│  │               │ ─────────────────▶ │                    │              │
│  │  Orchestrator │                     │   Bubble Tea       │              │
│  │               │ ◀───────────────── │   Program          │              │
│  │  - supervisors│    commands         │                    │              │
│  │  - collectors │                     │  - Model (state)   │              │
│  └───────┬───────┘                     │  - View (render)   │              │
│          │                             │  - Update (events) │              │
│          │ client events               │                    │              │
│          ▼                             └─────────┬──────────┘              │
│  ┌───────────────┐                               │                         │
│  │ StatsAggregator│                               │ tick (500ms)           │
│  │               │                               ▼                         │
│  │ - per-client  │                     ┌────────────────────┐              │
│  │ - aggregated  │ ──────────────────▶ │   Terminal         │              │
│  │ - percentiles │      render         │   (stdout)         │              │
│  └───────────────┘                     └────────────────────┘              │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Bubble Tea Implementation

```go
// internal/tui/model.go

package tui

import (
    "time"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
)

// Model holds all TUI state
type Model struct {
    // Configuration
    targetClients int
    duration      time.Duration
    streamURL     string

    // Live stats (updated via messages)
    stats         *AggregatedStats

    // UI state
    startTime     time.Time
    width         int
    height        int
    recentEvents  []string

    // Control
    quitting      bool
}

// Messages
type statsUpdateMsg struct {
    stats *AggregatedStats
}

type tickMsg time.Time

type clientEventMsg struct {
    event string
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
    return tea.Batch(
        tickCmd(),           // Start tick timer
        waitForStats(m),     // Listen for stats updates
    )
}

// Update handles messages
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {

    case tea.KeyMsg:
        switch msg.String() {
        case "ctrl+c", "q":
            m.quitting = true
            return m, tea.Quit
        }

    case tea.WindowSizeMsg:
        m.width = msg.Width
        m.height = msg.Height
        return m, nil

    case tickMsg:
        // Refresh display
        return m, tickCmd()

    case statsUpdateMsg:
        m.stats = msg.stats
        return m, waitForStats(m)

    case clientEventMsg:
        // Add to recent events (keep last 5)
        m.recentEvents = append([]string{msg.event}, m.recentEvents...)
        if len(m.recentEvents) > 5 {
            m.recentEvents = m.recentEvents[:5]
        }
        return m, nil
    }

    return m, nil
}

// View renders the UI
func (m Model) View() string {
    if m.quitting {
        return ""
    }

    // Styles
    titleStyle := lipgloss.NewStyle().
        Bold(true).
        Foreground(lipgloss.Color("39")).
        Background(lipgloss.Color("236")).
        Padding(0, 1).
        Width(m.width)

    sectionStyle := lipgloss.NewStyle().
        Bold(true).
        Foreground(lipgloss.Color("212"))

    valueStyle := lipgloss.NewStyle().
        Foreground(lipgloss.Color("86"))

    errorStyle := lipgloss.NewStyle().
        Foreground(lipgloss.Color("196"))

    // Build view
    var s strings.Builder

    // Title bar
    s.WriteString(titleStyle.Render(
        fmt.Sprintf("go-ffmpeg-hls-swarm • %s", m.streamURL),
    ))
    s.WriteString("\n\n")

    // Elapsed time
    elapsed := time.Since(m.startTime)
    s.WriteString(fmt.Sprintf("  Elapsed: %s / %s\n\n",
        formatDuration(elapsed),
        formatDuration(m.duration),
    ))

    // Two-column layout
    left := m.renderClientsSection(sectionStyle, valueStyle)
    right := m.renderRequestsSection(sectionStyle, valueStyle, errorStyle)

    s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, "    ", right))
    s.WriteString("\n\n")

    // Recent events
    s.WriteString(sectionStyle.Render("Recent Events:"))
    s.WriteString("\n")
    for _, event := range m.recentEvents {
        s.WriteString(fmt.Sprintf("  %s\n", event))
    }

    return s.String()
}

func (m Model) renderClientsSection(section, value lipgloss.Style) string {
    var s strings.Builder

    s.WriteString(section.Render("CLIENTS"))
    s.WriteString("\n")
    s.WriteString(fmt.Sprintf("  Target:   %s\n", value.Render(fmt.Sprintf("%d", m.targetClients))))
    s.WriteString(fmt.Sprintf("  Active:   %s  %s\n",
        value.Render(fmt.Sprintf("%d", m.stats.ActiveClients)),
        m.renderProgressBar(m.stats.ActiveClients, m.targetClients, 12),
    ))
    s.WriteString(fmt.Sprintf("  Stalled:  %s\n", value.Render(fmt.Sprintf("%d", m.stats.StalledClients))))
    s.WriteString(fmt.Sprintf("  Restarts: %s\n", value.Render(fmt.Sprintf("%d", m.stats.TotalRestarts))))

    s.WriteString("\n")
    s.WriteString(section.Render("LATENCY"))
    s.WriteString("\n")
    s.WriteString(fmt.Sprintf("  P50:  %s\n", value.Render(formatMs(m.stats.SegmentLatencyP50))))
    s.WriteString(fmt.Sprintf("  P95:  %s\n", value.Render(formatMs(m.stats.SegmentLatencyP95))))
    s.WriteString(fmt.Sprintf("  P99:  %s\n", value.Render(formatMs(m.stats.SegmentLatencyP99))))

    return s.String()
}

func (m Model) renderRequestsSection(section, value, errStyle lipgloss.Style) string {
    var s strings.Builder

    s.WriteString(section.Render("REQUESTS"))
    s.WriteString("\n")
    s.WriteString(fmt.Sprintf("  Manifests: %s  (%s/s)\n",
        value.Render(formatNumber(m.stats.TotalManifestReqs)),
        value.Render(fmt.Sprintf("%.1f", m.stats.ManifestReqRate)),
    ))
    s.WriteString(fmt.Sprintf("  Segments:  %s  (%s/s)\n",
        value.Render(formatNumber(m.stats.TotalSegmentReqs)),
        value.Render(fmt.Sprintf("%.1f", m.stats.SegmentReqRate)),
    ))
    s.WriteString(fmt.Sprintf("  Bytes:     %s  (%s/s)\n",
        value.Render(formatBytes(m.stats.TotalBytesDownloaded)),
        value.Render(formatBytes(int64(m.stats.ThroughputBytesPerSec))),
    ))

    s.WriteString("\n")
    s.WriteString(section.Render("ERRORS"))
    s.WriteString("\n")

    httpErrs := m.stats.TotalHTTPErrors[500] + m.stats.TotalHTTPErrors[503]
    if httpErrs > 0 {
        s.WriteString(fmt.Sprintf("  HTTP 5xx:    %s\n", errStyle.Render(fmt.Sprintf("%d", httpErrs))))
    } else {
        s.WriteString(fmt.Sprintf("  HTTP 5xx:    %s\n", value.Render("0")))
    }
    s.WriteString(fmt.Sprintf("  Timeouts:    %s\n", value.Render(fmt.Sprintf("%d", m.stats.TotalTimeouts))))
    s.WriteString(fmt.Sprintf("  Reconnects:  %s\n", value.Render(fmt.Sprintf("%d", m.stats.TotalReconnections))))

    return s.String()
}

func (m Model) renderProgressBar(current, total, width int) string {
    if total == 0 {
        return strings.Repeat("░", width)
    }

    filled := (current * width) / total
    if filled > width {
        filled = width
    }

    bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
    return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(bar)
}

// Commands
func tickCmd() tea.Cmd {
    return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
        return tickMsg(t)
    })
}

func waitForStats(m Model) tea.Cmd {
    return func() tea.Msg {
        // This would receive from a channel in practice
        stats := <-m.statsChan
        return statsUpdateMsg{stats: stats}
    }
}
```

### Integration with Orchestrator

```go
// internal/orchestrator/orchestrator.go modifications

type Orchestrator struct {
    // ... existing fields ...

    // TUI integration
    tuiEnabled    bool
    statsChannel  chan *AggregatedStats
    eventChannel  chan string
}

func (o *Orchestrator) Run(ctx context.Context) error {
    // Start stats aggregation goroutine
    go o.aggregateStats(ctx)

    if o.tuiEnabled && isTerminal() {
        // Run with TUI
        return o.runWithTUI(ctx)
    }

    // Run without TUI (original behavior)
    return o.runSimple(ctx)
}

func (o *Orchestrator) runWithTUI(ctx context.Context) error {
    // Create Bubble Tea program
    model := tui.NewModel(tui.Config{
        TargetClients: o.config.Clients,
        Duration:      o.config.Duration,
        StreamURL:     o.config.StreamURL,
        StatsChan:     o.statsChannel,
        EventChan:     o.eventChannel,
    })

    program := tea.NewProgram(model, tea.WithAltScreen())

    // Run orchestration in background
    go func() {
        o.runOrchestration(ctx)
        program.Quit()
    }()

    // Run TUI (blocks until quit)
    if _, err := program.Run(); err != nil {
        return err
    }

    // Print final summary to stdout
    o.printExitSummary()

    return nil
}

func (o *Orchestrator) aggregateStats(ctx context.Context) {
    ticker := time.NewTicker(500 * time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            stats := o.collector.Aggregate()

            // Non-blocking send to TUI
            select {
            case o.statsChannel <- stats:
            default:
                // TUI not keeping up, skip this update
            }
        }
    }
}

func isTerminal() bool {
    fileInfo, _ := os.Stdout.Stat()
    return (fileInfo.Mode() & os.ModeCharDevice) != 0
}
```

### CLI Flag for TUI

```go
// New flag
var (
    flagTUI = flag.Bool("tui", true, "Enable live terminal dashboard (auto-disabled if not a TTY)")
)
```

### Graceful Degradation

When stdout is not a TTY (e.g., piped to a file), the TUI is automatically disabled:

```bash
# TUI enabled (interactive)
go-ffmpeg-hls-swarm -clients 100 http://example.com/stream.m3u8

# TUI disabled (piped)
go-ffmpeg-hls-swarm -clients 100 http://example.com/stream.m3u8 | tee output.log

# TUI disabled (redirected)
go-ffmpeg-hls-swarm -clients 100 http://example.com/stream.m3u8 > output.log

# Explicitly disable TUI
go-ffmpeg-hls-swarm -tui=false -clients 100 http://example.com/stream.m3u8
```

### Alternative: Simple Progress Mode

For simpler output without full TUI:

```bash
go-ffmpeg-hls-swarm -progress -clients 100 http://example.com/stream.m3u8
```

Output (updates in place):

```
[00:02:34] Clients: 100/100 | Req: 22,605 (150/s) | Bytes: 1.13GB | P99: 52ms | Errors: 0
```

### Dependencies

Add to `go.mod`:

```go
require (
    github.com/charmbracelet/bubbletea v1.2.4
    github.com/charmbracelet/lipgloss v1.0.0
    github.com/charmbracelet/bubbles v0.20.0
)
```

### Performance Considerations

- **Update frequency:** 500ms is a good balance (2 updates/sec)
- **Memory:** TUI adds ~1-2MB for rendering buffers
- **CPU:** Minimal (<1% for rendering)
- **No impact on FFmpeg processes** - TUI runs in separate goroutine

### TUI Performance Optimization for Large Client Counts

Updating 200+ clients' individual stats every 500ms can cause high CPU usage.

**Solution:** Implement "Summary Only" mode:

```go
// internal/stats/aggregator.go

type AggregationMode int

const (
    ModeSummaryOnly AggregationMode = iota  // Default for TUI
    ModeDetailed                            // When user presses 'd'
)

func (a *StatsAggregator) Aggregate(mode AggregationMode) *AggregatedStats {
    result := &AggregatedStats{...}

    // Always compute totals (fast, O(n) atomic reads)
    for _, c := range a.clients {
        result.TotalManifestReqs += atomic.LoadInt64(&c.ManifestRequests)
        result.TotalSegmentReqs += atomic.LoadInt64(&c.SegmentRequests)
        // ...
    }

    // Only compute expensive per-client stats in detailed mode
    if mode == ModeDetailed {
        result.PerClientStats = make([]*ClientStats, 0, len(a.clients))
        for _, c := range a.clients {
            result.PerClientStats = append(result.PerClientStats, c.Snapshot())
        }
    }

    return result
}
```

**TUI toggle:**

```go
// internal/tui/model.go

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        switch msg.String() {
        case "d":
            m.detailedView = !m.detailedView
            return m, nil
        }
    }
    // ...
}

func (m Model) View() string {
    if m.detailedView && len(m.stats.PerClientStats) > 0 {
        return m.renderDetailedView()
    }
    return m.renderSummaryView()
}
```

---

## Testing Strategy

### Overview

Each component should be tested in isolation before integration. We use a **test-driven development (TDD)** approach:

1. **Write failing tests first** - Define expected behavior
2. **Implement minimal code** - Make tests pass
3. **Refactor** - Clean up while tests stay green
4. **Integration tests** - Verify components work together

### Test Categories

| Category | Purpose | Location |
|----------|---------|----------|
| Unit tests | Test individual functions/types | `*_test.go` alongside code |
| Table-driven tests | Test multiple inputs/outputs | Same file, `TestXxx` functions |
| Integration tests | Test component interactions | `internal/*/integration_test.go` |
| Golden files | Test complex output formats | `testdata/*.golden` |
| Benchmark tests | Performance validation | `*_test.go` with `BenchmarkXxx` |

### Component Test Plans

#### 1. Progress Parser (`internal/parser/progress_test.go`)

**What to test:**
- Parsing valid key=value pairs
- Handling malformed input
- Extracting all supported fields
- Handling partial updates

```go
package parser

import (
    "strings"
    "testing"
)

func TestProgressParser_ParseLine(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        wantKey  string
        wantVal  string
        wantErr  bool
    }{
        {
            name:    "valid total_size",
            input:   "total_size=51324",
            wantKey: "total_size",
            wantVal: "51324",
        },
        {
            name:    "valid speed with suffix",
            input:   "speed=1.00x",
            wantKey: "speed",
            wantVal: "1.00x",
        },
        {
            name:    "empty value",
            input:   "bitrate=N/A",
            wantKey: "bitrate",
            wantVal: "N/A",
        },
        {
            name:    "malformed no equals",
            input:   "invalid line",
            wantErr: true,
        },
        {
            name:    "empty line",
            input:   "",
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            key, val, err := parseLine(tt.input)

            if tt.wantErr {
                if err == nil {
                    t.Errorf("expected error, got nil")
                }
                return
            }

            if err != nil {
                t.Errorf("unexpected error: %v", err)
                return
            }

            if key != tt.wantKey {
                t.Errorf("key = %q, want %q", key, tt.wantKey)
            }
            if val != tt.wantVal {
                t.Errorf("val = %q, want %q", val, tt.wantVal)
            }
        })
    }
}

func TestProgressParser_ParseBlock(t *testing.T) {
    input := `frame=0
fps=0.00
total_size=51324
out_time_us=2000000
speed=1.00x
progress=continue
`

    p := NewProgressParser(nil)
    reader := strings.NewReader(input)

    updates := make([]*ProgressUpdate, 0)
    p.SetCallback(func(u *ProgressUpdate) {
        updates = append(updates, u)
    })

    p.Parse(reader)

    if len(updates) != 1 {
        t.Fatalf("expected 1 update, got %d", len(updates))
    }

    u := updates[0]
    if u.TotalSize != 51324 {
        t.Errorf("TotalSize = %d, want 51324", u.TotalSize)
    }
    if u.OutTimeUS != 2000000 {
        t.Errorf("OutTimeUS = %d, want 2000000", u.OutTimeUS)
    }
    if u.Speed != 1.0 {
        t.Errorf("Speed = %f, want 1.0", u.Speed)
    }
    if u.Progress != "continue" {
        t.Errorf("Progress = %q, want 'continue'", u.Progress)
    }
}

func TestProgressParser_SpeedParsing(t *testing.T) {
    tests := []struct {
        input string
        want  float64
    }{
        {"1.00x", 1.0},
        {"0.95x", 0.95},
        {"1.5x", 1.5},
        {"N/A", 0.0},      // Invalid should return 0
        {"", 0.0},
    }

    for _, tt := range tests {
        t.Run(tt.input, func(t *testing.T) {
            got := parseSpeed(tt.input)
            if got != tt.want {
                t.Errorf("parseSpeed(%q) = %f, want %f", tt.input, got, tt.want)
            }
        })
    }
}
```

**Golden file test for full output:**

```go
func TestProgressParser_GoldenOutput(t *testing.T) {
    input, err := os.ReadFile("testdata/ffmpeg_progress_sample.txt")
    if err != nil {
        t.Fatalf("failed to read input: %v", err)
    }

    p := NewProgressParser(nil)
    var updates []*ProgressUpdate
    p.SetCallback(func(u *ProgressUpdate) {
        updates = append(updates, u)
    })

    p.Parse(bytes.NewReader(input))

    // Compare against golden file
    got := formatUpdates(updates)
    golden := filepath.Join("testdata", t.Name()+".golden")

    if *update {
        os.WriteFile(golden, []byte(got), 0644)
    }

    want, _ := os.ReadFile(golden)
    if got != string(want) {
        t.Errorf("output mismatch:\n%s", diff(string(want), got))
    }
}
```

#### 2. HLS Event Parser (`internal/parser/hls_events_test.go`)

**What to test:**
- Regex pattern matching for all event types
- URL extraction from log lines
- HTTP status code extraction
- Counter increments

```go
package parser

import (
    "strings"
    "testing"
)

func TestHLSEventParser_OpeningURLs(t *testing.T) {
    tests := []struct {
        name        string
        line        string
        wantURL     string
        wantType    URLType
        wantMatched bool
    }{
        {
            name:        "manifest request",
            line:        "[hls @ 0x55f8] Opening 'http://example.com/stream.m3u8' for reading",
            wantURL:     "http://example.com/stream.m3u8",
            wantType:    URLTypeManifest,
            wantMatched: true,
        },
        {
            name:        "segment request",
            line:        "[https @ 0x55f8] Opening 'http://example.com/seg00123.ts' for reading",
            wantURL:     "http://example.com/seg00123.ts",
            wantType:    URLTypeSegment,
            wantMatched: true,
        },
        {
            name:        "variant manifest",
            line:        "[hls @ 0x55f8] Opening 'http://example.com/720p.m3u8' for reading",
            wantURL:     "http://example.com/720p.m3u8",
            wantType:    URLTypeManifest,
            wantMatched: true,
        },
        {
            name:        "unrelated line",
            line:        "frame= 1234 fps= 30",
            wantMatched: false,
        },
        {
            name:        "init segment",
            line:        "[hls @ 0x55f8] Opening 'http://example.com/init.mp4' for reading",
            wantURL:     "http://example.com/init.mp4",
            wantType:    URLTypeInit,
            wantMatched: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            url, urlType, matched := parseOpeningURL(tt.line)

            if matched != tt.wantMatched {
                t.Errorf("matched = %v, want %v", matched, tt.wantMatched)
                return
            }

            if !matched {
                return
            }

            if url != tt.wantURL {
                t.Errorf("url = %q, want %q", url, tt.wantURL)
            }
            if urlType != tt.wantType {
                t.Errorf("urlType = %v, want %v", urlType, tt.wantType)
            }
        })
    }
}

func TestHLSEventParser_HTTPErrors(t *testing.T) {
    tests := []struct {
        line     string
        wantCode int
        wantOK   bool
    }{
        {"Server returned 404 Not Found", 404, true},
        {"Server returned 503 Service Unavailable", 503, true},
        {"Server returned 200 OK", 200, true},
        {"Connection refused", 0, false},
        {"random line", 0, false},
    }

    for _, tt := range tests {
        t.Run(tt.line, func(t *testing.T) {
            code, ok := parseHTTPError(tt.line)
            if ok != tt.wantOK {
                t.Errorf("ok = %v, want %v", ok, tt.wantOK)
            }
            if code != tt.wantCode {
                t.Errorf("code = %d, want %d", code, tt.wantCode)
            }
        })
    }
}

func TestHLSEventParser_CounterIncrements(t *testing.T) {
    input := `[hls @ 0x55f8] Opening 'http://example.com/stream.m3u8' for reading
[https @ 0x55f8] Opening 'http://example.com/seg00001.ts' for reading
[https @ 0x55f8] Opening 'http://example.com/seg00002.ts' for reading
[hls @ 0x55f8] Opening 'http://example.com/stream.m3u8' for reading
[https @ 0x55f8] Opening 'http://example.com/seg00003.ts' for reading
Server returned 503 Service Unavailable
Reconnecting to http://example.com/stream.m3u8
`

    stats := NewClientStats(0)
    p := NewHLSEventParser(0, stats)
    p.Parse(strings.NewReader(input))

    // Verify counts
    if stats.ManifestRequests != 2 {
        t.Errorf("ManifestRequests = %d, want 2", stats.ManifestRequests)
    }
    if stats.SegmentRequests != 3 {
        t.Errorf("SegmentRequests = %d, want 3", stats.SegmentRequests)
    }
    if stats.HTTPErrors[503] != 1 {
        t.Errorf("HTTPErrors[503] = %d, want 1", stats.HTTPErrors[503])
    }
    if stats.Reconnections != 1 {
        t.Errorf("Reconnections = %d, want 1", stats.Reconnections)
    }
}
```

#### 3. Latency Tracker (`internal/parser/latency_test.go`)

**What to test:**
- Latency calculation between request start/complete
- Handling overlapping requests
- Ring buffer behavior
- Percentile calculations

```go
package parser

import (
    "testing"
    "time"
)

func TestLatencyTracker_BasicLatency(t *testing.T) {
    tracker := NewLatencyTracker()

    // Simulate request start
    tracker.OnRequestStart("http://example.com/seg001.ts")

    // Simulate 50ms delay
    time.Sleep(50 * time.Millisecond)

    // Simulate request complete
    latency := tracker.OnRequestComplete()

    // Should be ~50ms (allow 10ms tolerance)
    if latency < 40*time.Millisecond || latency > 70*time.Millisecond {
        t.Errorf("latency = %v, want ~50ms", latency)
    }
}

func TestLatencyTracker_NoActiveRequest(t *testing.T) {
    tracker := NewLatencyTracker()

    // Complete without start should return 0
    latency := tracker.OnRequestComplete()

    if latency != 0 {
        t.Errorf("latency = %v, want 0 (no active request)", latency)
    }
}

func TestLatencyTracker_OnlySegments(t *testing.T) {
    tracker := NewLatencyTracker()

    // Manifest requests should not be tracked for latency
    tracker.OnRequestStart("http://example.com/stream.m3u8")
    time.Sleep(10 * time.Millisecond)

    latency := tracker.OnRequestComplete()

    // Should return 0 for non-segment requests
    if latency != 0 {
        t.Errorf("latency = %v, want 0 (manifest request)", latency)
    }
}

func TestLatencyHistogram_Percentiles(t *testing.T) {
    h := NewLatencyHistogram()

    // Add 100 samples: 1ms, 2ms, ..., 100ms
    for i := 1; i <= 100; i++ {
        h.Add(time.Duration(i) * time.Millisecond)
    }

    tests := []struct {
        percentile float64
        wantMin    time.Duration
        wantMax    time.Duration
    }{
        {0.50, 49 * time.Millisecond, 51 * time.Millisecond},
        {0.95, 94 * time.Millisecond, 96 * time.Millisecond},
        {0.99, 98 * time.Millisecond, 100 * time.Millisecond},
    }

    for _, tt := range tests {
        t.Run(fmt.Sprintf("P%.0f", tt.percentile*100), func(t *testing.T) {
            got := h.Percentile(tt.percentile)
            if got < tt.wantMin || got > tt.wantMax {
                t.Errorf("P%.0f = %v, want between %v and %v",
                    tt.percentile*100, got, tt.wantMin, tt.wantMax)
            }
        })
    }
}
```

#### 4. Client Stats (`internal/stats/client_stats_test.go`)

**What to test:**
- Thread-safe counter increments
- Rate calculations
- Stall detection

```go
package stats

import (
    "sync"
    "testing"
    "time"
)

func TestClientStats_ConcurrentIncrements(t *testing.T) {
    stats := NewClientStats(0)

    // Simulate concurrent updates from multiple goroutines
    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            stats.IncrementManifestRequests()
            stats.IncrementSegmentRequests()
            stats.AddBytesDownloaded(1024)
        }()
    }
    wg.Wait()

    if stats.ManifestRequests != 100 {
        t.Errorf("ManifestRequests = %d, want 100", stats.ManifestRequests)
    }
    if stats.SegmentRequests != 100 {
        t.Errorf("SegmentRequests = %d, want 100", stats.SegmentRequests)
    }
    if stats.BytesDownloaded != 102400 {
        t.Errorf("BytesDownloaded = %d, want 102400", stats.BytesDownloaded)
    }
}

func TestClientStats_StallDetection(t *testing.T) {
    stats := NewClientStats(0)

    // Speed above threshold - not stalled
    stats.UpdateSpeed(1.0)
    if stats.IsStalled() {
        t.Error("expected not stalled at speed 1.0")
    }

    // Speed below threshold but not long enough
    stats.UpdateSpeed(0.8)
    if stats.IsStalled() {
        t.Error("expected not stalled immediately after speed drop")
    }

    // Simulate being below threshold for 5+ seconds
    stats.speedBelowThresholdSince = time.Now().Add(-6 * time.Second)
    if !stats.IsStalled() {
        t.Error("expected stalled after 5s below threshold")
    }
}

func TestClientStats_RecordHTTPError(t *testing.T) {
    stats := NewClientStats(0)

    stats.RecordHTTPError(503)
    stats.RecordHTTPError(503)
    stats.RecordHTTPError(404)

    if stats.HTTPErrors[503] != 2 {
        t.Errorf("HTTPErrors[503] = %d, want 2", stats.HTTPErrors[503])
    }
    if stats.HTTPErrors[404] != 1 {
        t.Errorf("HTTPErrors[404] = %d, want 1", stats.HTTPErrors[404])
    }
}
```

#### 5. Stats Aggregator (`internal/stats/aggregator_test.go`)

**What to test:**
- Aggregation across multiple clients
- Rate calculations
- Percentile merging

```go
package stats

import (
    "testing"
    "time"
)

func TestStatsAggregator_AggregateClients(t *testing.T) {
    agg := NewStatsAggregator()

    // Add 3 clients with different stats
    clients := []*ClientStats{
        {ManifestRequests: 10, SegmentRequests: 50, BytesDownloaded: 1000},
        {ManifestRequests: 15, SegmentRequests: 75, BytesDownloaded: 1500},
        {ManifestRequests: 20, SegmentRequests: 100, BytesDownloaded: 2000},
    }

    for i, c := range clients {
        c.ClientID = i
        agg.AddClient(c)
    }

    result := agg.Aggregate()

    if result.TotalManifestReqs != 45 {
        t.Errorf("TotalManifestReqs = %d, want 45", result.TotalManifestReqs)
    }
    if result.TotalSegmentReqs != 225 {
        t.Errorf("TotalSegmentReqs = %d, want 225", result.TotalSegmentReqs)
    }
    if result.TotalBytesDownloaded != 4500 {
        t.Errorf("TotalBytesDownloaded = %d, want 4500", result.TotalBytesDownloaded)
    }
    if result.TotalClients != 3 {
        t.Errorf("TotalClients = %d, want 3", result.TotalClients)
    }
}

func TestStatsAggregator_RateCalculation(t *testing.T) {
    agg := NewStatsAggregator()
    agg.startTime = time.Now().Add(-10 * time.Second) // 10 seconds ago

    client := &ClientStats{
        ClientID:        0,
        SegmentRequests: 100,
        BytesDownloaded: 10000,
    }
    agg.AddClient(client)

    result := agg.Aggregate()

    // 100 segments / 10 seconds = 10/sec
    if result.SegmentReqRate < 9.5 || result.SegmentReqRate > 10.5 {
        t.Errorf("SegmentReqRate = %.2f, want ~10.0", result.SegmentReqRate)
    }

    // 10000 bytes / 10 seconds = 1000 bytes/sec
    if result.ThroughputBytesPerSec < 950 || result.ThroughputBytesPerSec > 1050 {
        t.Errorf("ThroughputBytesPerSec = %.2f, want ~1000", result.ThroughputBytesPerSec)
    }
}

func TestStatsAggregator_StalledClients(t *testing.T) {
    agg := NewStatsAggregator()

    // 2 healthy, 1 stalled
    healthy1 := &ClientStats{ClientID: 0, currentSpeed: 1.0}
    healthy2 := &ClientStats{ClientID: 1, currentSpeed: 1.05}
    stalled := &ClientStats{
        ClientID:                   2,
        currentSpeed:               0.7,
        speedBelowThresholdSince:   time.Now().Add(-10 * time.Second),
    }

    agg.AddClient(healthy1)
    agg.AddClient(healthy2)
    agg.AddClient(stalled)

    result := agg.Aggregate()

    if result.StalledClients != 1 {
        t.Errorf("StalledClients = %d, want 1", result.StalledClients)
    }
    if result.ClientsAboveRealtime != 2 {
        t.Errorf("ClientsAboveRealtime = %d, want 2", result.ClientsAboveRealtime)
    }
}
```

#### 6. TUI Model (`internal/tui/model_test.go`)

**What to test:**
- Model initialization
- Message handling
- View rendering

```go
package tui

import (
    "strings"
    "testing"
    "time"

    tea "github.com/charmbracelet/bubbletea"
)

func TestModel_Init(t *testing.T) {
    m := NewModel(Config{
        TargetClients: 100,
        Duration:      5 * time.Minute,
        StreamURL:     "http://example.com/stream.m3u8",
    })

    cmd := m.Init()
    if cmd == nil {
        t.Error("Init() should return a command")
    }
}

func TestModel_UpdateWindowSize(t *testing.T) {
    m := NewModel(Config{TargetClients: 100})

    newModel, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
    model := newModel.(Model)

    if model.width != 120 {
        t.Errorf("width = %d, want 120", model.width)
    }
    if model.height != 40 {
        t.Errorf("height = %d, want 40", model.height)
    }
}

func TestModel_UpdateStats(t *testing.T) {
    m := NewModel(Config{TargetClients: 100})

    stats := &AggregatedStats{
        ActiveClients:     50,
        TotalSegmentReqs:  1000,
        SegmentLatencyP99: 50 * time.Millisecond,
    }

    newModel, _ := m.Update(statsUpdateMsg{stats: stats})
    model := newModel.(Model)

    if model.stats.ActiveClients != 50 {
        t.Errorf("ActiveClients = %d, want 50", model.stats.ActiveClients)
    }
}

func TestModel_UpdateQuit(t *testing.T) {
    m := NewModel(Config{TargetClients: 100})

    newModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
    model := newModel.(Model)

    if !model.quitting {
        t.Error("expected quitting=true after Ctrl+C")
    }
    if cmd == nil {
        t.Error("expected quit command")
    }
}

func TestModel_ViewContainsElements(t *testing.T) {
    m := NewModel(Config{
        TargetClients: 100,
        StreamURL:     "http://example.com/stream.m3u8",
    })
    m.width = 80
    m.height = 24
    m.stats = &AggregatedStats{
        ActiveClients:      75,
        TotalSegmentReqs:   1500,
        SegmentLatencyP99:  45 * time.Millisecond,
    }

    view := m.View()

    // Check for expected elements
    checks := []string{
        "example.com",    // Stream URL
        "100",            // Target clients
        "75",             // Active clients
        "1500",           // Segment requests (or formatted version)
        "LATENCY",        // Section header
        "CLIENTS",        // Section header
    }

    for _, want := range checks {
        if !strings.Contains(view, want) {
            t.Errorf("view missing %q", want)
        }
    }
}

func TestModel_RenderProgressBar(t *testing.T) {
    m := NewModel(Config{})

    tests := []struct {
        current int
        total   int
        width   int
        wantLen int
    }{
        {50, 100, 10, 10},   // 50% filled
        {0, 100, 10, 10},    // 0% filled
        {100, 100, 10, 10},  // 100% filled
        {75, 100, 20, 20},   // 75% filled, wider bar
    }

    for _, tt := range tests {
        bar := m.renderProgressBar(tt.current, tt.total, tt.width)
        // Strip ANSI codes for length check
        stripped := stripAnsi(bar)
        if len(stripped) != tt.wantLen {
            t.Errorf("bar length = %d, want %d", len(stripped), tt.wantLen)
        }
    }
}
```

### Test Fixtures (`testdata/`)

Create test fixtures for realistic FFmpeg output:

```
testdata/
├── ffmpeg_progress_sample.txt      # Real -progress output
├── ffmpeg_hls_verbose.txt          # Real -loglevel verbose output
├── ffmpeg_hls_with_errors.txt      # Output with HTTP errors
├── ffmpeg_reconnection.txt         # Output showing reconnection
└── golden/
    ├── TestProgressParser_GoldenOutput.golden
    └── TestExitSummary_Format.golden
```

**Example `testdata/ffmpeg_progress_sample.txt`:**

```
frame=0
fps=0.00
stream_0_0_q=-1.0
bitrate=N/A
total_size=0
out_time_us=0
out_time_ms=0
out_time=00:00:00.000000
dup_frames=0
drop_frames=0
speed=N/A
progress=continue
frame=30
fps=30.00
stream_0_0_q=-1.0
bitrate=512.0kbits/s
total_size=51324
out_time_us=2000000
out_time_ms=2000
out_time=00:00:02.000000
dup_frames=0
drop_frames=0
speed=1.00x
progress=continue
frame=60
fps=30.00
stream_0_0_q=-1.0
bitrate=500.5kbits/s
total_size=102648
out_time_us=4000000
out_time_ms=4000
out_time=00:00:04.000000
dup_frames=0
drop_frames=0
speed=1.01x
progress=continue
```

### Mock Interfaces

Define interfaces to allow mocking in tests:

```go
// internal/parser/interfaces.go

// StatsRecorder is implemented by ClientStats
type StatsRecorder interface {
    IncrementManifestRequests()
    IncrementSegmentRequests()
    AddBytesDownloaded(n int64)
    RecordHTTPError(code int)
    RecordReconnection()
    RecordTimeout()
    RecordSegmentLatency(d time.Duration)
    UpdateSpeed(speed float64)
}

// ProgressCallback receives parsed progress updates
type ProgressCallback func(*ProgressUpdate)

// EventCallback receives parsed HLS events
type EventCallback func(Event)
```

```go
// internal/parser/mocks_test.go

type mockStatsRecorder struct {
    manifestReqs   int64
    segmentReqs    int64
    bytesDownloaded int64
    httpErrors     map[int]int64
    reconnections  int64
    latencies      []time.Duration
}

func (m *mockStatsRecorder) IncrementManifestRequests() {
    atomic.AddInt64(&m.manifestReqs, 1)
}

func (m *mockStatsRecorder) IncrementSegmentRequests() {
    atomic.AddInt64(&m.segmentReqs, 1)
}

// ... etc
```

### Running Tests

```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...

# Run specific package
go test ./internal/parser/...

# Run with race detector (important for concurrent code!)
go test -race ./...

# Run benchmarks
go test -bench=. ./internal/parser/

# Update golden files
go test ./... -update

# Verbose output
go test -v ./internal/parser/
```

### Coverage Goals

| Package | Target Coverage |
|---------|-----------------|
| `internal/parser` | 90%+ |
| `internal/stats` | 85%+ |
| `internal/tui` | 70%+ (UI hard to test) |
| `internal/supervisor` | 80%+ |
| Overall | 80%+ |

### CI/CD (Future)

> **Note**: CI/CD is out of scope for initial implementation. When added, it will be via **Nix**
> (consistent with this project's infrastructure), not GitHub Actions. A Nix flake check could
> run tests as part of `nix flake check`.

### TDD Workflow Example

For implementing the Progress Parser:

```bash
# 1. Write test first
vim internal/parser/progress_test.go

# 2. Run test (should fail - function doesn't exist)
go test ./internal/parser/
# --- FAIL: TestProgressParser_ParseLine

# 3. Implement minimal code to pass
vim internal/parser/progress.go

# 4. Run test again
go test ./internal/parser/
# PASS

# 5. Add more test cases
vim internal/parser/progress_test.go

# 6. Run with race detector
go test -race ./internal/parser/

# 7. Check coverage
go test -cover ./internal/parser/
# coverage: 92.5% of statements
```

---

## Prometheus Cardinality Management

### The Problem

Per-client `client_id` labels cause cardinality explosion at scale:

| Clients | Metrics | Time Series | TSDB Impact |
|---------|---------|-------------|-------------|
| 50 | 10 | 500 | ✅ Fine |
| 200 | 10 | 2,000 | ⚠️ Noticeable |
| 1,000 | 10 | 10,000 | ❌ Expensive |
| 5,000 | 10 | 50,000 | ❌ Painful |

### Two-Tier Metrics Architecture

**Tier 1: Aggregate Metrics (Default)**

No `client_id` label. Safe for 1000+ clients. Always enabled.

```go
// Aggregate metrics - O(1) cardinality regardless of client count
var (
    hlsManifestRequestsTotal = prometheus.NewCounter(...)      // No labels
    hlsSegmentRequestsTotal = prometheus.NewCounter(...)       // No labels
    hlsBytesDownloadedTotal = prometheus.NewCounter(...)       // No labels
    hlsSegmentLatency = prometheus.NewHistogram(...)           // No labels
    hlsHTTPErrors = prometheus.NewCounterVec(..., []string{"status_code"})  // Low cardinality

    // Gauges for current state
    hlsActiveClients = prometheus.NewGauge(...)
    hlsStalledClients = prometheus.NewGauge(...)
    hlsClientsWithHighDrift = prometheus.NewGauge(...)
    hlsAverageSpeed = prometheus.NewGauge(...)
    hlsAverageDrift = prometheus.NewGauge(...)
)
```

**Tier 2: Per-Client Metrics (Optional)**

Enabled with `--prom-client-metrics`. Use only for debugging with <200 clients.

```go
// Per-client metrics - only when explicitly enabled
var (
    hlsClientSpeed = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "hls_swarm_client_speed",
            Help: "Per-client playback speed",
        },
        []string{"client_id"},
    )
    hlsClientDrift = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "hls_swarm_client_drift_seconds",
            Help: "Per-client wall-clock drift",
        },
        []string{"client_id"},
    )
    hlsClientBytes = prometheus.NewCounterVec(
        prometheus.GaugeOpts{
            Name: "hls_swarm_client_bytes_total",
            Help: "Per-client bytes downloaded",
        },
        []string{"client_id"},
    )
)
```

### Metric Registration

```go
// internal/metrics/collector.go

type Collector struct {
    perClientEnabled bool

    // Tier 1: Always registered
    manifestReqs      prometheus.Counter
    segmentReqs       prometheus.Counter
    bytesDownloaded   prometheus.Counter
    segmentLatency    prometheus.Histogram
    httpErrors        *prometheus.CounterVec  // label: status_code
    activeClients     prometheus.Gauge
    stalledClients    prometheus.Gauge
    highDriftClients  prometheus.Gauge

    // Tier 2: Only if perClientEnabled
    clientSpeed       *prometheus.GaugeVec    // label: client_id
    clientDrift       *prometheus.GaugeVec    // label: client_id
    clientBytes       *prometheus.CounterVec  // label: client_id
}

func NewCollector(perClientMetrics bool) *Collector {
    c := &Collector{
        perClientEnabled: perClientMetrics,
        // ... init tier 1 metrics ...
    }

    // Register tier 1 (always)
    prometheus.MustRegister(c.manifestReqs, c.segmentReqs, ...)

    // Register tier 2 (optional)
    if perClientMetrics {
        c.clientSpeed = prometheus.NewGaugeVec(...)
        c.clientDrift = prometheus.NewGaugeVec(...)
        c.clientBytes = prometheus.NewCounterVec(...)
        prometheus.MustRegister(c.clientSpeed, c.clientDrift, c.clientBytes)
    }

    return c
}
```

### Config Flag

```go
// internal/config/config.go

type Config struct {
    // ...

    // PromClientMetrics enables per-client Prometheus metrics.
    // WARNING: High cardinality! Only use with <200 clients.
    // Default: false
    PromClientMetrics bool `json:"prom_client_metrics"`
}
```

```bash
# Default: aggregate only (safe for any scale)
./go-ffmpeg-hls-swarm -clients 1000 ...

# Per-client metrics (debugging, <200 clients)
./go-ffmpeg-hls-swarm -clients 50 -prom-client-metrics ...
```

### Additional Metrics for External Dashboards

To make `/metrics` complete for Grafana (without needing the TUI), we add test metadata
and pre-calculated percentiles:

```go
// Test metadata (enables Grafana dashboards without internal access)
hlsTargetClients = prometheus.NewGauge(
    prometheus.GaugeOpts{
        Name: "hls_swarm_target_clients",
        Help: "Target number of clients configured",
    },
)
hlsRampProgress = prometheus.NewGauge(
    prometheus.GaugeOpts{
        Name: "hls_swarm_ramp_progress",
        Help: "Client ramp progress (0.0 to 1.0)",
    },
)
hlsTestDurationSeconds = prometheus.NewGauge(
    prometheus.GaugeOpts{
        Name: "hls_swarm_test_duration_seconds",
        Help: "Configured test duration in seconds",
    },
)
hlsTestElapsedSeconds = prometheus.NewGauge(
    prometheus.GaugeOpts{
        Name: "hls_swarm_test_elapsed_seconds",
        Help: "Elapsed test time in seconds",
    },
)

// Pre-calculated percentiles (avoids histogram math in Grafana for simple dashboards)
// IMPORTANT: Latency is INFERRED from FFmpeg events, not directly measured.
// Use for trend analysis, not absolute performance claims.
// Note: histogram_quantile() in PromQL is more accurate for time-range queries
hlsInferredLatencyP50Seconds = prometheus.NewGauge(
    prometheus.GaugeOpts{
        Name: "hls_swarm_inferred_latency_p50_seconds",
        Help: "Inferred segment latency 50th percentile (from FFmpeg events)",
    },
)
hlsInferredLatencyP95Seconds = prometheus.NewGauge(
    prometheus.GaugeOpts{
        Name: "hls_swarm_inferred_latency_p95_seconds",
        Help: "Inferred segment latency 95th percentile (from FFmpeg events)",
    },
)
hlsInferredLatencyP99Seconds = prometheus.NewGauge(
    prometheus.GaugeOpts{
        Name: "hls_swarm_inferred_latency_p99_seconds",
        Help: "Inferred segment latency 99th percentile (from FFmpeg events)",
    },
)
```

### Cardinality Summary

| Category | Metric Count | Time Series |
|----------|--------------|-------------|
| Test Overview | 7 | 7 |
| Request Rates & Throughput | 7 | 7 |
| Latency Distribution | 5 | 5 + histogram buckets |
| Client Health & Playback | 9 | 9 |
| Errors & Recovery | 7 | ~12 (status codes vary) |
| Segment Statistics | 3 | 3 |
| Pipeline Health | 4 | 6 (2 stream labels) |
| **Tier 1 Total** | **42 metrics** | **~50 time series** |
| Per-Client (Tier 2) | 3 | 3 × N |

**Tier 1 Total Cardinality:** ~50 time series (constant, regardless of client count)
**Tier 2 Additional:** N × 3 time series (where N = client count)

For the complete list of all 42 metrics organized by Grafana dashboard panel, see
[METRICS_IMPLEMENTATION_PLAN.md](METRICS_IMPLEMENTATION_PLAN.md#step-85-complete-metrics-reference).

---

## Metrics Accuracy Assessment

| Metric | Accuracy | Source | Notes |
|--------|----------|--------|-------|
| **Throughput** | ✅ High | `total_size` from stdout | Direct from FFmpeg |
| **Request Counts** | ✅ High | stderr `Opening` events | Reliable pattern |
| **HTTP Errors** | ✅ High | stderr `Server returned` | Reliable pattern |
| **Stall Detection** | ✅ High | `speed < 1.0` for X seconds | Direct from FFmpeg |
| **Inferred Latency** | ⚠️ Estimated | Time between stderr events | Use for trends, not absolutes |
| **Segment Size** | ⚠️ Estimated | Delta of `total_size` | Approximation |
| **Wall-Clock Drift** | ✅ High | `out_time_us` vs wall clock | Calculated |
| **Playback Position** | ✅ High | `out_time_us` from stdout | Direct from FFmpeg |
| **Unknown URLs** | ✅ High | Fallback bucket | Helps diagnose CDN behavior |

---

## Minor Refinements for Clarity

### 1. "Inferred Latency" Naming

All latency metrics are derived from FFmpeg events, not directly measured. To prevent misuse:

- **Variable names**: `inferredLatencyDigest`, `InferredLatencyP50()`, etc.
- **Prometheus metrics**: `hls_swarm_inferred_latency_seconds`
- **Exit summary label**: "Inferred Segment Latency *"
- **Footnote**: "Inferred from FFmpeg events; use for trends, not absolute values."

### 2. Unknown URL Classification

URLs that don't match `.m3u8`, `.ts`, or `.mp4` patterns go to a fallback bucket:

```go
// In ClientStats
UnknownRequests int64  // Fallback for unrecognized URL patterns

// In ClassifyAndCountURL()
default:
    // Helps diagnose: byte-range playlists, signed URLs, CDN oddities
    atomic.AddInt64(&s.UnknownRequests, 1)
```

This surfaces only in debug logs and exit summary footnotes:
```
Footnotes:
  [2] Unknown URL requests: 42 (may indicate byte-range playlists, signed URLs)
```

### 3. Configurable Pipeline Health Thresholds

```go
// Config
StatsDropThreshold float64  // Default: 0.01 (1%)

// ClientStats
PeakDropRate float64  // Highest drop rate observed (not just current)

// MetricsDegraded now takes threshold parameter
func (s *ClientStats) MetricsDegraded(threshold float64) bool {
    return s.CurrentDropRate() > threshold
}
```

Benefits:
- **Operators can tune** via `--stats-drop-threshold 0.02` (2%)
- **Peak drop rate** correlates with load spikes (visible in summary)
- **Not just boolean** — know exactly how bad it got

---

## Summary

This enhancement will transform the exit summary from:

**Before:**
```
Total Starts:         100
Total Restarts:       0
Exit Codes:           137 (SIGKILL) 100
```

**After:**
```
Manifest Requests:    15,000 (50.0/sec)
Segment Requests:     75,000 (250.0/sec)
Total Bytes:          3.75 GB (12.5 MB/s)
Segment Latency P99:  52 ms
Error Rate:           0.007%
```

This provides operators with actionable insights for load testing HLS infrastructure.
