# Observability

> **Type**: Contributor Documentation
> **Related**: [DESIGN.md](DESIGN.md), [SUPERVISION.md](SUPERVISION.md), [OPERATIONS.md](OPERATIONS.md)

This document specifies Prometheus metrics, structured logging, and the exit summary report for `go-ffmpeg-hls-swarm`.

---

## Table of Contents

- [1. Prometheus Metrics](#1-prometheus-metrics)
  - [1.1 Available Metrics](#11-available-metrics)
  - [1.2 Metric Definitions](#12-metric-definitions-precise)
  - [1.3 Example Queries](#13-example-queries)
- [2. Logging](#2-logging)
  - [2.1 Structured JSON Logs](#21-structured-json-logs)
  - [2.2 Log Handling Contract](#22-log-handling-contract)
  - [2.3 Buffer Limits](#23-buffer-limits)
- [3. Exit Summary Report](#3-exit-summary-report)
  - [3.1 Report Format](#31-report-format)
  - [3.2 Implementation](#32-implementation)

---

## 1. Prometheus Metrics

Exposed at `/metrics` (default `0.0.0.0:9090`).

### 1.1 Available Metrics

#### Core Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hlsswarm_clients_target` | Gauge | - | Configured target client count |
| `hlsswarm_clients_active` | Gauge | - | Currently running processes |
| `hlsswarm_clients_started_total` | Counter | - | Total clients started |
| `hlsswarm_clients_restarted_total` | Counter | - | Total restart events |
| `hlsswarm_process_exits_total` | Counter | `code` | Process exits by exit code |
| `hlsswarm_client_uptime_seconds` | Histogram | - | Client uptime before exit |
| `hlsswarm_ramp_progress` | Gauge | - | Ramp-up progress (0.0 to 1.0) |
| `hlsswarm_clients_max_restarts_reached_total` | Counter | - | Clients that hit max restart limit |

#### Throughput Metrics (via `-progress` protocol)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hlsswarm_bytes_downloaded_total` | Counter | - | Total bytes downloaded across all clients |
| `hlsswarm_segments_downloaded_total` | Counter | - | Total HLS segments downloaded |
| `hlsswarm_clients_stalled_total` | Counter | - | Clients detected as stalled (no progress) |

These throughput metrics help correlate CDN bandwidth spikes with client counts. "Active clients" is a flat metric—throughput shows actual load generated.

**Cardinality note**: No per-client labels. Aggregate metrics only.

### 1.2 Metric Definitions (Precise)

| Metric | Precise Definition |
|--------|-------------------|
| `clients_active` | Count of FFmpeg processes with `state == Running`. Does NOT include processes in backoff or starting. |
| `client_uptime_seconds` | Time from process spawn to process exit. Does NOT include time in backoff. Histogram buckets: 1s, 5s, 30s, 60s, 300s, 600s, 1800s, 3600s. |
| `ramp_progress` | `clients_started / clients_target`. Based on "attempted to start", not "currently active". Reaches 1.0 when all clients have been started at least once. |
| `process_exits_total` | Incremented when FFmpeg process exits. Labels by exit code. Includes expected (143/SIGTERM during shutdown) and unexpected exits. |

### 1.3 Example Queries

```promql
# Current active clients
hlsswarm_clients_active

# Restart rate (per minute)
rate(hlsswarm_clients_restarted_total[1m]) * 60

# Client failure rate
rate(hlsswarm_process_exits_total{code!="0"}[1m])

# Median client uptime (P50)
histogram_quantile(0.5, rate(hlsswarm_client_uptime_seconds_bucket[5m]))

# P95 client uptime - critical for detecting connection-dropping infrastructure
histogram_quantile(0.95, rate(hlsswarm_client_uptime_seconds_bucket[5m]))

# Percentage of target clients currently active
hlsswarm_clients_active / hlsswarm_clients_target * 100

# Error exits as percentage of total
sum(rate(hlsswarm_process_exits_total{code!="0"}[5m])) / sum(rate(hlsswarm_process_exits_total[5m])) * 100

# Total download bandwidth (bytes/sec)
rate(hlsswarm_bytes_downloaded_total[1m])

# Segments downloaded per second
rate(hlsswarm_segments_downloaded_total[1m])

# Stall rate (stalls per minute)
rate(hlsswarm_clients_stalled_total[1m]) * 60

# Bytes per active client (are all clients pulling weight?)
rate(hlsswarm_bytes_downloaded_total[1m]) / hlsswarm_clients_active
```

### 1.4 Interpreting Uptime Metrics

The `hlsswarm_client_uptime_seconds` histogram is critical for understanding infrastructure health:

| P95 Uptime | Interpretation |
|------------|----------------|
| > 5 minutes | Healthy — infrastructure sustaining connections |
| 30s - 5min | Possible issues — check for rate limiting or connection drops |
| < 30 seconds | Problem — infrastructure likely rate-limiting or rejecting connections |

**Example**: 100 active clients with P95 uptime of 10 seconds means most connections are being dropped quickly, even though "active" looks healthy at any point in time.

---

## 2. Logging

### 2.1 Structured JSON Logs

Structured JSON logs to stderr:

```json
{"level":"info","ts":"2026-01-17T10:00:00Z","msg":"client_started","client_id":0,"pid":12345}
{"level":"info","ts":"2026-01-17T10:00:05Z","msg":"client_exited","client_id":0,"pid":12345,"exit_code":1,"uptime_sec":5.2}
{"level":"info","ts":"2026-01-17T10:00:05Z","msg":"client_restart_scheduled","client_id":0,"backoff_ms":250}
{"level":"warn","ts":"2026-01-17T10:00:10Z","msg":"process_stderr","client_id":0,"line":"[error] Connection refused"}
```

### 2.2 Log Handling Contract

#### What We Capture

| Stream | Captured? | Purpose |
|--------|-----------|---------|
| FFmpeg stderr | ✅ Yes | Errors, warnings, progress info |
| FFmpeg stdout | ❌ No | Goes to null muxer anyway |

#### Streaming (Never ReadAll)

**Critical**: Never use `ioutil.ReadAll` or `bufio.Scanner` without limits on FFmpeg output.

```go
// WRONG: Can OOM with verbose FFmpeg output
output, _ := cmd.StderrPipe()
all, _ := ioutil.ReadAll(output)  // DON'T DO THIS

// RIGHT: Stream line-by-line with limits
scanner := bufio.NewScanner(stderr)
scanner.Buffer(make([]byte, MaxLineLength), MaxLineLength)
for scanner.Scan() {
    handler.HandleLine(scanner.Text())
}
```

### 2.3 Buffer Limits

Prevent memory exhaustion with bounded buffers:

```go
// logging/handler.go

const (
    MaxLineLength    = 4096      // Truncate lines longer than this
    MaxBufferedLines = 100       // Per-client circular buffer
    LogSampleRate    = 0.1       // Under load: log 10% of lines (Phase 2)
)

type StderrHandler struct {
    clientID int
    buffer   *ring.Buffer  // Circular buffer, fixed size
    logger   *slog.Logger
}

func (h *StderrHandler) HandleLine(line string) {
    // Truncate if too long
    if len(line) > MaxLineLength {
        line = line[:MaxLineLength] + "...(truncated)"
    }

    // Store in circular buffer (for exit summary)
    h.buffer.Write(line)

    // Log (with optional sampling under load)
    if h.shouldLog() {
        h.logger.Debug("process_stderr", "client_id", h.clientID, "line", line)
    }
}
```

---

## 3. Exit Summary Report

On shutdown, print a human-readable summary.

### 3.1 Report Format

```
═══════════════════════════════════════════════════════════════════
                        go-ffmpeg-hls-swarm Exit Summary
═══════════════════════════════════════════════════════════════════
Run Duration:           00:32:15
Target Clients:         100
Peak Active Clients:    98
Median Active Clients:  95

Uptime Distribution:
  P50 (median):         04:23     ✓ Good: connections lasting minutes
  P95:                  00:45     ⚠ Warning: top 5% die within 45 seconds
  P99:                  00:12     ✗ Problem: some connections very short-lived

Throughput:
  Total Bytes:          45.2 GB
  Total Segments:       12,847
  Avg Bytes/Client/Sec: 1.2 MB/s

Lifecycle:
  Total Starts:         247
  Total Restarts:       147
  Stalls Detected:      23
  Clean Exits (0):      12
  Error Exits (1):      135

Exit Codes:
  0   (clean):          12  (4.9%)
  1   (error):          135 (54.7%)
  143 (SIGTERM):        100 (40.5%)

Top Errors (from stderr):
  "Connection refused":         45
  "Server returned 503":        32
  "[hls] Skip segment":         28

Metrics endpoint was: http://0.0.0.0:9090/metrics
═══════════════════════════════════════════════════════════════════
```

**Interpreting the Exit Summary:**

| Metric | Good | Concerning | Action |
|--------|------|------------|--------|
| P95 uptime | > 5 min | < 1 min | Check for rate limiting |
| Restart rate | < 1/min | > 10/min | Origin may be overloaded |
| Stalls detected | 0 | > 5% of clients | Check for slow-drip servers |
| Error exits | < 10% | > 50% | Review top errors |

The exit summary is critical because Prometheus scrapes are periodic (e.g., every 15s). Short-lived crash loops might be missed in graphs but will be captured here.

### 3.2 Implementation

```go
// metrics/summary.go

type RunSummary struct {
    Duration           time.Duration
    TargetClients      int
    PeakActiveClients  int
    MedianActive       float64

    // Uptime distribution - critical for detecting connection drops
    UptimeP50          time.Duration
    UptimeP95          time.Duration
    UptimeP99          time.Duration

    // Throughput (from -progress protocol)
    TotalBytes         int64
    TotalSegments      int64

    // Lifecycle
    TotalStarts        int64
    TotalRestarts      int64
    TotalStalls        int64
    ExitCodes          map[int]int64
    TopErrors          []ErrorCount  // Top 5 most frequent stderr patterns
}

type ErrorCount struct {
    Pattern string
    Count   int
}

func (m *Collector) GenerateSummary() *RunSummary {
    // Aggregate from prometheus metrics + internal counters
    return &RunSummary{
        Duration:          time.Since(m.startTime),
        TargetClients:     m.targetClients,
        PeakActiveClients: m.peakActive,
        MedianActive:      m.calculateMedianActive(),
        UptimeP50:         m.calculateUptimePercentile(0.50),
        UptimeP95:         m.calculateUptimePercentile(0.95),
        UptimeP99:         m.calculateUptimePercentile(0.99),
        TotalBytes:        m.totalBytes.Load(),
        TotalSegments:     m.totalSegments.Load(),
        TotalStarts:       m.totalStarts.Load(),
        TotalRestarts:     m.totalRestarts.Load(),
        TotalStalls:       m.totalStalls.Load(),
        ExitCodes:         m.exitCodeCounts,
        TopErrors:         m.topErrors(5),
    }
}

func (s *RunSummary) Print(w io.Writer) {
    fmt.Fprintln(w, "═══════════════════════════════════════════════════════════════════")
    fmt.Fprintln(w, "                        go-ffmpeg-hls-swarm Exit Summary")
    fmt.Fprintln(w, "═══════════════════════════════════════════════════════════════════")
    fmt.Fprintf(w, "Run Duration:           %s\n", formatDuration(s.Duration))
    fmt.Fprintf(w, "Target Clients:         %d\n", s.TargetClients)
    fmt.Fprintf(w, "Peak Active Clients:    %d\n", s.PeakActiveClients)
    fmt.Fprintf(w, "Median Active Clients:  %.0f\n", s.MedianActive)
    fmt.Fprintln(w)

    // ... lifecycle stats, exit codes, top errors ...

    fmt.Fprintln(w, "═══════════════════════════════════════════════════════════════════")
}

func formatDuration(d time.Duration) string {
    h := int(d.Hours())
    m := int(d.Minutes()) % 60
    s := int(d.Seconds()) % 60
    return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}
```

### Error Pattern Detection

The exit summary extracts common error patterns from stderr:

```go
// metrics/errors.go

var errorPatterns = []struct {
    Pattern *regexp.Regexp
    Name    string
}{
    {regexp.MustCompile(`Connection refused`), "Connection refused"},
    {regexp.MustCompile(`Server returned (\d+)`), "Server returned {code}"},
    {regexp.MustCompile(`\[hls\] Skip segment`), "[hls] Skip segment"},
    {regexp.MustCompile(`Reconnecting`), "Reconnecting"},
    {regexp.MustCompile(`timeout`), "Timeout"},
}

func (m *Collector) classifyError(line string) string {
    for _, p := range errorPatterns {
        if p.Pattern.MatchString(line) {
            return p.Name
        }
    }
    return ""
}
```
