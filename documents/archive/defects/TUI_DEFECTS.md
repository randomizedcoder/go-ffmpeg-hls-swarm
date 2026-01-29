# TUI Dashboard Defects

> **Status**: OPEN
> **Date**: 2026-01-22
> **References**:
> - [METRICS_ENHANCEMENT_DESIGN.md](METRICS_ENHANCEMENT_DESIGN.md)
> - [METRICS_IMPLEMENTATION_PLAN.md](METRICS_IMPLEMENTATION_PLAN.md)
> - [METRICS_ENHANCEMENT_IMPLEMENTATION_LOG.md](METRICS_ENHANCEMENT_IMPLEMENTATION_LOG.md)

---

## Summary

During 300-client load testing, multiple TUI rendering and logic issues were observed. This document catalogs the defects and proposes fixes.

---

## Defects

### Defect A: Duplicate/Conflicting Ramp Progress Display

**Severity**: High
**Component**: `internal/tui/view.go`

**Observed Behavior**:
- Two separate progress indicators displayed:
  1. "Ramping up... 165/300" (stuck/stale value)
  2. "All clients running" (separate indicator)
- These should be unified into a single progress meter

**Expected Behavior**:
- Single progress meter showing current state:
  - During ramp: "Ramping up: 165/300 (55%)" with progress bar
  - After ramp: "Running: 300/300 (100%)" with full progress bar

**Root Cause Analysis**:
- The `renderRampProgress()` function in `view.go` appears to have separate text labels that are not properly synchronized
- The "Ramping up" text may be coming from a different state than the progress bar

**Proposed Fix**:
```go
// In view.go - renderRampProgress()
func (m Model) renderRampProgress() string {
    active := m.stats.ActiveClients
    target := m.targetClients
    progress := float64(active) / float64(target)

    var statusText string
    if active >= target {
        statusText = fmt.Sprintf("Running: %d/%d", active, target)
    } else {
        statusText = fmt.Sprintf("Ramping up: %d/%d", active, target)
    }

    return lipgloss.JoinVertical(lipgloss.Left,
        statusText,
        m.rampProgress.ViewAs(progress),
    )
}
```

---

### Defect B: "All clients running" Missing Client Count

**Severity**: Medium
**Component**: `internal/tui/view.go`

**Observed Behavior**:
- Display shows "All clients running" without count
- User cannot tell how many "All" means

**Expected Behavior**:
- "All 300 clients running" OR
- "Running: 300/300 clients"

**Proposed Fix**:
Consolidate with Defect A fix - single unified status line.

---

### Defect C: Request Statistics Labels Unclear

**Severity**: Medium
**Component**: `internal/tui/view.go`

**Observed Behavior**:
```
Request Statistics
Manifest Requests:    1.3K    (56.1/s)
Segment Requests:     1.6K    (70.0/s)
Manifest Requests:    4.7K    (116.2/s)   ← Duplicate label!
Segment Requests:    14.2K    (186.1/s)   ← Duplicate label!
Manifest Requests:   11.6K    (152.3/s)   ← Duplicate label!
```

Multiple rows with same labels - user cannot distinguish what each represents.

**Expected Behavior**:
Labels should clearly differentiate the metric type:
```
Request Statistics
─────────────────────────────────────────────
Current Period (last 1s):
  Manifests:          1.3K    (56.1/s)
  Segments:           1.6K    (70.0/s)

Cumulative Total:
  Manifests:          4.7K    (116.2/s avg)
  Segments:          14.2K    (186.1/s avg)

Per-Client Average:
  Manifests:          47      (1.9/s)
  Segments:          142      (6.2/s)
```

**Root Cause Analysis**:
- The view is rendering multiple stat rows without clear section headers
- Instantaneous vs cumulative vs per-client metrics are mixed

**Proposed Fix**:
```go
func (m Model) renderRequestStats() string {
    var sections []string

    // Section 1: Current rates (instantaneous)
    sections = append(sections,
        sectionSubtitle.Render("Current Rates"),
        fmt.Sprintf("  Manifests: %s/s", formatRate(m.stats.InstantManifestRate)),
        fmt.Sprintf("  Segments:  %s/s", formatRate(m.stats.InstantSegmentRate)),
    )

    // Section 2: Cumulative totals
    sections = append(sections,
        sectionSubtitle.Render("Totals"),
        fmt.Sprintf("  Manifests: %s", formatNumber(m.stats.TotalManifestReqs)),
        fmt.Sprintf("  Segments:  %s", formatNumber(m.stats.TotalSegmentReqs)),
        fmt.Sprintf("  Bytes:     %s", formatBytes(m.stats.TotalBytes)),
    )

    return lipgloss.JoinVertical(lipgloss.Left, sections...)
}
```

---

### Defect D: Playback Health Numbers Don't Add Up

**Severity**: High
**Component**: `internal/stats/aggregator.go` or `internal/tui/view.go`

**Observed Behavior**:
```
Playback Health
Speed Distribution: 150 healthy (100%), 0 buffering
Average Speed:      2.37x
Speed Distribution: 141 healthy (55%), 117 buffering   ← Wait, 141+117=258, not 300
Speed Distribution: 277 healthy (99%), 3 buffering     ← 277+3=280, not 300
```

Three different "Speed Distribution" lines with inconsistent totals.

**Expected Behavior**:
Single, accurate playback health summary:
```
Playback Health
─────────────────────────────────────────────
Active Clients:     300
  >= 1.0x (healthy):  277 (92%)
  < 1.0x (buffering):  23 (8%)
Average Speed:      1.10x
Stalled (0x):         0
```

**Root Cause Analysis**:
1. Multiple calls to render playback health stats
2. Stats may be from different aggregation windows
3. Race condition between stats update and render

**Proposed Fix**:
1. Ensure `AggregatedStats` has a single, consistent snapshot
2. Remove duplicate rendering calls
3. Add validation: `assert(healthy + buffering == active_clients)`

```go
func (m Model) renderPlaybackHealth() string {
    if m.stats == nil {
        return "No data"
    }

    active := m.stats.ActiveClients
    healthy := m.stats.ClientsAboveRealtime
    buffering := m.stats.ClientsBelowRealtime

    // Sanity check
    if healthy + buffering != active {
        // Log warning, but show what we have
        log.Printf("WARN: health stats inconsistent: %d + %d != %d",
            healthy, buffering, active)
    }

    return lipgloss.JoinVertical(lipgloss.Left,
        fmt.Sprintf("Active Clients: %d", active),
        fmt.Sprintf("  Healthy (>=1.0x): %d (%d%%)", healthy, pct(healthy, active)),
        fmt.Sprintf("  Buffering (<1.0x): %d (%d%%)", buffering, pct(buffering, active)),
        fmt.Sprintf("Average Speed: %.2fx", m.stats.AverageSpeed),
        fmt.Sprintf("Stalled (0x): %d", m.stats.StalledClients),
    )
}
```

---

### Defect E: Log Output Contains Raw Terminal Escape Characters

**Severity**: High
**Component**: TUI log rendering / log capture, `internal/supervisor/`

**Observed Behavior**:
- JSON log lines appear in TUI with raw escape sequences
- Layout is broken by unprocessed ANSI codes
- Text overflows and wraps incorrectly
- Progress data mixed with debug logs when using `-progress pipe:2`

**Screenshot Evidence**:
```
{"time":"2026-01-22T12:34:33.133401314-08:00","level":"INFO","msg":"client_started"...
{"time":"2026-01-22T12:34:33.15..."client_id":279,"pid":2808820...
```

**Expected Behavior**:
- Clean, formatted log display
- OR no log display in TUI (logs go to file/stderr instead)
- Progress and events should be cleanly separated

**Root Cause Analysis**:
- The TUI is likely capturing stdout/stderr and attempting to display it
- JSON logs are not being filtered/formatted for TUI display
- ANSI escape sequences from log coloring are passed through raw
- Progress data and debug logs both go to stderr when using `-progress pipe:2`

**Recommended Solution: Unix Domain Socket for Progress**

Inspired by [ffmpeg-go](https://github.com/u2takey/ffmpeg-go)'s `showProgress.go` example.
See [FFMPEG_HLS_REFERENCE.md §13](FFMPEG_HLS_REFERENCE.md#13-clean-output-separation-strategies) for full details.

```
FFmpeg Process
├── stderr  →  Pipeline A: HLS events, errors, debug only
└── -progress unix:///tmp/hls_N.sock  →  Pipeline B: Clean key=value progress
```

**Benefits**:
- Progress is **completely isolated** from stderr debug output
- No regex needed to separate progress from logs
- Easy to parse key=value format on its own channel
- Works with any `-loglevel` setting

**Implementation**:
```go
// Create Unix socket for each client
sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("hls_swarm_%d.sock", clientID))
progressListener, _ := net.Listen("unix", sockPath)

// FFmpeg writes progress to socket, not stderr
cmd := exec.Command("ffmpeg",
    "-progress", "unix://"+sockPath,  // Clean progress channel
    "-loglevel", "verbose",           // Debug to stderr only
    // ...
)

// Read progress from socket in goroutine
go func() {
    conn, _ := progressListener.Accept()
    scanner := bufio.NewScanner(conn)
    for scanner.Scan() {
        // Clean key=value lines, no mixing!
    }
}()
```

**Fallback Fix Options (if not using sockets)**:

**Option 1: Remove log display from TUI entirely**
- Simplest fix
- Logs go to stderr/file, TUI only shows metrics
- Add `-log-file` flag to redirect logs

**Option 2: Create dedicated log panel with sanitization**
```go
import "regexp"

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func sanitizeLogLine(line string) string {
    // Strip ANSI escape codes
    clean := ansiEscape.ReplaceAllString(line, "")
    // Truncate to reasonable length
    if len(clean) > 80 {
        clean = clean[:77] + "..."
    }
    return clean
}

func (m Model) renderLogPanel() string {
    var lines []string
    for _, log := range m.recentLogs {
        lines = append(lines, sanitizeLogLine(log))
    }
    return lipgloss.JoinVertical(lipgloss.Left, lines...)
}
```

**Option 3: Parse JSON logs and format nicely**
```go
type LogEntry struct {
    Time  string `json:"time"`
    Level string `json:"level"`
    Msg   string `json:"msg"`
}

func formatLogEntry(jsonLine string) string {
    var entry LogEntry
    if err := json.Unmarshal([]byte(jsonLine), &entry); err != nil {
        return sanitizeLogLine(jsonLine) // Fallback
    }

    // Extract just time and message
    t, _ := time.Parse(time.RFC3339Nano, entry.Time)
    return fmt.Sprintf("%s %s %s",
        t.Format("15:04:05"),
        levelStyle(entry.Level).Render(entry.Level),
        entry.Msg,
    )
}
```

**Recommendation**: Option 1 (simplest) or Option 3 (best UX)

---

### Defect F: Unused Screen Real Estate - Missing Origin Metrics

**Severity**: Low (Enhancement)
**Component**: New feature

**Observed Behavior**:
- Large blank area to the right of "Request Statistics" and "Inferred Segment Latency"
- Valuable screen space wasted

**Expected Behavior**:
Display origin server metrics in the blank space:
```
┌─────────────────────────────────────────────────────────────────────────────┐
│ Request Statistics          │ Origin Server Metrics                         │
│ ─────────────────────       │ ─────────────────────────────────────────     │
│ Manifests: 4.7K (116/s)     │ CPU:     23%  [████░░░░░░]                    │
│ Segments: 14.2K (186/s)     │ Memory:  1.2G / 4.0G (30%)                    │
│                             │ Network: 45 MB/s in, 890 MB/s out             │
│ Inferred Segment Latency    │                                               │
│ ─────────────────────       │ Nginx Status                                  │
│ P50: 4132 ms                │ ─────────────────────────────────────────     │
│ P95: 5592 ms                │ Active Connections: 312                       │
│ P99: 5588 ms                │ Requests/sec: 186.2                           │
│                             │ Request Time P99: 12ms                        │
└─────────────────────────────────────────────────────────────────────────────┘
```

**Proposed Implementation**:

1. **Add Prometheus scraper goroutine**:
```go
// internal/prometheus/scraper.go
type PrometheusScraper struct {
    endpoints map[string]string // name -> URL
    interval  time.Duration
    metrics   map[string]float64
    mu        sync.RWMutex
}

func NewScraper(cfg ScraperConfig) *PrometheusScraper {
    return &PrometheusScraper{
        endpoints: map[string]string{
            "swarm":      cfg.SwarmMetricsURL,    // http://localhost:17091/metrics
            "origin":     cfg.OriginNodeExporter, // http://10.177.0.10:9100/metrics
            "nginx":      cfg.NginxExporter,      // http://10.177.0.10:9113/metrics
        },
        interval: 2 * time.Second,
        metrics:  make(map[string]float64),
    }
}

func (s *PrometheusScraper) Run(ctx context.Context) {
    ticker := time.NewTicker(s.interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            s.scrapeAll()
        }
    }
}

func (s *PrometheusScraper) scrapeAll() {
    for name, url := range s.endpoints {
        metrics, err := scrapeEndpoint(url)
        if err != nil {
            continue
        }
        s.mu.Lock()
        for k, v := range metrics {
            s.metrics[name+"_"+k] = v
        }
        s.mu.Unlock()
    }
}

func (s *PrometheusScraper) Get(key string) float64 {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.metrics[key]
}
```

2. **Key metrics to scrape**:

| Source | Metric | Description |
|--------|--------|-------------|
| Node Exporter | `node_cpu_seconds_total` | Origin CPU usage |
| Node Exporter | `node_memory_MemAvailable_bytes` | Origin available memory |
| Node Exporter | `node_network_receive_bytes_total` | Network in |
| Node Exporter | `node_network_transmit_bytes_total` | Network out |
| Nginx Exporter | `nginx_connections_active` | Active connections |
| Nginx Exporter | `nginx_http_requests_total` | Total requests |
| Nginx Exporter | `nginx_http_request_duration_seconds` | Request latency |

3. **Add CLI flags**:
```go
// In config/flags.go
flag.StringVar(&cfg.OriginMetricsURL, "origin-metrics", "",
    "Origin node_exporter URL (e.g., http://10.177.0.10:9100/metrics)")
flag.StringVar(&cfg.NginxMetricsURL, "nginx-metrics", "",
    "Origin nginx_exporter URL (e.g., http://10.177.0.10:9113/metrics)")
```

4. **Add TUI panel**:
```go
// In tui/view.go
func (m Model) renderOriginMetrics() string {
    if m.originMetrics == nil {
        return dimStyle.Render("Origin metrics not configured")
    }

    return lipgloss.JoinVertical(lipgloss.Left,
        sectionTitle.Render("Origin Server"),
        fmt.Sprintf("CPU:     %.1f%% %s",
            m.originMetrics.CPUPercent,
            progressBar(m.originMetrics.CPUPercent/100, 10)),
        fmt.Sprintf("Memory:  %s / %s (%.0f%%)",
            formatBytes(m.originMetrics.MemUsed),
            formatBytes(m.originMetrics.MemTotal),
            m.originMetrics.MemPercent),
        fmt.Sprintf("Net In:  %s/s", formatBytes(m.originMetrics.NetInRate)),
        fmt.Sprintf("Net Out: %s/s", formatBytes(m.originMetrics.NetOutRate)),
        "",
        sectionTitle.Render("Nginx"),
        fmt.Sprintf("Connections: %d", m.originMetrics.NginxConnections),
        fmt.Sprintf("Req/sec:     %.1f", m.originMetrics.NginxReqRate),
    )
}
```

---

---

### Defect G: Insufficient FFmpeg Client Detail for Load Testing

**Severity**: Medium (Enhancement)
**Component**: `internal/process/ffmpeg.go`, `internal/parser/`

**Observed Behavior**:
- Current FFmpeg command uses `-loglevel verbose` which provides limited detail
- Cannot see individual segment download times
- Cannot see TCP connection attempts/timing
- Cannot see HTTP request/response details
- Progress output doesn't include segment-level metrics

**Expected Behavior**:
For a load testing tool, we need granular metrics per FFmpeg client:
- **Per-segment download times** (start request → complete)
- **TCP connection timing** (DNS, connect, TLS handshake)
- **HTTP details** (request headers, response status, Content-Length)
- **Manifest refresh timing**
- **Media sequence tracking** (detect skipped segments)

**Proposed FFmpeg Command Enhancement**:

```bash
ffmpeg -hide_banner -nostdin \
  -loglevel debug \                              # ← Full debug output
  -reconnect 1 \
  -reconnect_streamed 1 \
  -reconnect_on_network_error 1 \
  -rw_timeout 15000000 \                         # 15s timeout (microseconds)
  -progress pipe:2 \                             # Progress to stderr
  -stats -stats_period 1 \                       # Stats every 1 second
  -i "http://origin/stream.m3u8" \
  -map 0 -c copy -f null -
```

**Debug Output Patterns to Parse**:

Sample output captured in `testdata/ffmpeg_debug_output.txt`:

| Pattern | Regex | Data Extracted |
|---------|-------|----------------|
| Segment request | `\[hls @ \w+\] HLS request for url '([^']+)', offset (\d+), playlist (\d+)` | URL, offset, playlist |
| Segment opening | `\[hls @ \w+\] Opening '([^']+)' for reading` | URL |
| TCP connect start | `\[tcp @ \w+\] Starting connection attempt to ([\d.]+) port (\d+)` | IP, port |
| TCP connected | `\[tcp @ \w+\] Successfully connected to ([\d.]+) port (\d+)` | IP, port |
| HTTP request | `\[http @ \w+\] request: (GET\|HEAD) ([^ ]+) HTTP/[\d.]+` | Method, path |
| Manifest refresh | `\[hls @ \w+\] Opening '.*\.m3u8' for reading` | URL |
| Sequence change | `\[hls @ \w+\] Media sequence change \((\d+) -> (\d+)\)` | Old seq, new seq |
| Bytes read | `\[AVIOContext @ \w+\] Statistics: (\d+) bytes read` | Bytes |
| Final stats | `Total: (\d+) packets \((\d+) bytes\) (demuxed\|muxed)` | Packets, bytes |

**Sample Parsed Events Timeline**:

```
00:00:00.001  TCP_CONNECT_START  10.177.0.10:17080
00:00:00.002  TCP_CONNECTED      10.177.0.10:17080  (1ms)
00:00:00.003  HTTP_REQUEST       GET /stream.m3u8
00:00:00.005  MANIFEST_LOADED    10 segments
00:00:00.006  TCP_CONNECT_START  10.177.0.10:17080
00:00:00.007  TCP_CONNECTED      10.177.0.10:17080  (1ms)
00:00:00.008  HTTP_REQUEST       GET /seg03440.ts
00:00:00.055  SEGMENT_COMPLETE   seg03440.ts  (47ms, 51KB)
00:00:00.056  HTTP_REQUEST       GET /seg03441.ts
00:00:00.102  SEGMENT_COMPLETE   seg03441.ts  (46ms, 51KB)
...
00:00:02.001  MANIFEST_REFRESH   (sequence 3433 -> 3438)
```

**Implementation Steps**:

1. **Update `FFmpegConfig`**:
```go
type FFmpegConfig struct {
    // ... existing fields ...
    DebugLogging    bool   // Use -loglevel debug
    StatsEnabled    bool   // Use -stats -stats_period 1
    RWTimeout       time.Duration  // -rw_timeout in microseconds
    ReconnectMax    int    // -reconnect_max_retries
}
```

2. **Create new parser: `internal/parser/debug_events.go`**:
```go
type DebugEvent struct {
    Timestamp   time.Time
    EventType   DebugEventType
    URL         string
    IP          string
    Port        int
    BytesRead   int64
    Duration    time.Duration
    SequenceOld int
    SequenceNew int
}

type DebugEventType int

const (
    EventTCPConnectStart DebugEventType = iota
    EventTCPConnected
    EventHTTPRequest
    EventSegmentRequest
    EventSegmentComplete
    EventManifestRefresh
    EventSequenceChange
    EventBytesRead
)

type DebugEventParser struct {
    // Regex patterns
    hlsRequestRe     *regexp.Regexp
    tcpConnectStartRe *regexp.Regexp
    tcpConnectedRe   *regexp.Regexp
    // ... more patterns

    // State for timing
    pendingRequests map[string]time.Time  // URL -> request start time
}
```

3. **Add per-segment timing to `ClientStats`**:
```go
type ClientStats struct {
    // ... existing fields ...

    // Per-segment metrics
    SegmentDownloads     int64
    SegmentBytesTotal    int64
    SegmentLatencyDigest *tdigest.TDigest  // Download time distribution

    // Connection metrics
    TCPConnections       int64
    TCPConnectLatencyP50 time.Duration
    TCPConnectLatencyP99 time.Duration

    // Manifest refresh
    ManifestRefreshes    int64
    SequenceSkips        int64  // Segments skipped due to expired playlist
}
```

4. **Add TUI panel for per-client detail**:
```
┌─────────────────────────────────────────────────────────────────────┐
│ Client #42 Detail (press 'd' to toggle)                            │
├─────────────────────────────────────────────────────────────────────┤
│ Segments Downloaded:  125      (avg 48ms, p99 120ms)               │
│ TCP Connections:      15       (avg 2ms connect time)              │
│ Manifest Refreshes:   12       (every 2.1s avg)                    │
│ Sequence Skips:       0        (no segments missed)                │
│ Total Bytes:          6.2 MB   (avg 50KB/segment)                  │
│ Current Speed:        1.02x    (healthy)                           │
│ Recent Events:                                                      │
│   12:34:56.789  GET seg03445.ts  51KB  45ms                        │
│   12:34:56.834  GET seg03446.ts  52KB  48ms                        │
│   12:34:58.002  REFRESH stream.m3u8                                │
│   12:34:58.050  GET seg03447.ts  51KB  42ms                        │
└─────────────────────────────────────────────────────────────────────┘
```

**Testing**:
- Sample debug output saved to: `testdata/ffmpeg_debug_output.txt`
- Update `internal/parser/hls_events_test.go` with debug patterns
- Add benchmark to ensure debug parsing doesn't impact 300+ client performance

**Trade-offs**:

| Mode | CPU Overhead | Detail Level | Recommended For |
|------|--------------|--------------|-----------------|
| `-loglevel info` | Low | Basic | Production, 500+ clients |
| `-loglevel verbose` | Medium | Good | Standard testing |
| `-loglevel debug` | High | Full | Deep analysis, <100 clients |

**Config Flag**:
```bash
./go-ffmpeg-hls-swarm -clients 50 -ffmpeg-debug http://origin/stream.m3u8
```

---

## Implementation Priority

| Defect | Priority | Effort | Impact |
|--------|----------|--------|--------|
| D | P0 | Medium | Stats showing wrong values is critical |
| E | P0 | Low | Log garbage makes TUI unusable |
| A+B | P1 | Low | Confusing but functional |
| C | P1 | Medium | Misleading but usable |
| F | P2 | High | Nice-to-have enhancement |
| G | P2 | High | Enhanced detail for serious load testing |

---

## Testing Checklist

- [ ] Run 300-client test and verify single progress indicator
- [ ] Verify playback health numbers sum to active clients
- [ ] Verify request stats have clear labels
- [ ] Verify no raw escape characters in TUI
- [ ] Test with origin metrics endpoints configured
- [ ] Test TUI with various terminal sizes
- [ ] Test TUI with terminal that doesn't support colors

---

## Files to Modify

| File | Defects |
|------|---------|
| `internal/tui/view.go` | A, B, C, D, F |
| `internal/tui/model.go` | E, F |
| `internal/tui/styles.go` | C |
| `internal/stats/aggregator.go` | D |
| `internal/prometheus/scraper.go` | F (new file) |
| `internal/config/config.go` | F |
| `internal/config/flags.go` | F |
| `cmd/go-ffmpeg-hls-swarm/main.go` | F |

---

## References

- [Bubble Tea Documentation](https://github.com/charmbracelet/bubbletea)
- [Lipgloss Styling](https://github.com/charmbracelet/lipgloss)
- [Prometheus Go Client](https://github.com/prometheus/client_golang)
- [ffmpeg-go](https://github.com/u2takey/ffmpeg-go) - Go FFmpeg wrapper with Unix socket progress example
- Phase 7 Implementation: `METRICS_IMPLEMENTATION_PLAN.md` Section 7
- [FFMPEG_HLS_REFERENCE.md §13](FFMPEG_HLS_REFERENCE.md#13-clean-output-separation-strategies) - Clean output separation strategies
