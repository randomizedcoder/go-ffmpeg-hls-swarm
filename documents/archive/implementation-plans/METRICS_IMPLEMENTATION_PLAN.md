# Metrics Enhancement Implementation Plan

> **Status**: IMPLEMENTATION READY
> **Date**: 2026-01-22
> **Design Document**: [METRICS_ENHANCEMENT_DESIGN.md](METRICS_ENHANCEMENT_DESIGN.md)

---

## Key Design Refinements

Based on review feedback, this implementation includes:

| Issue | Solution | Phase |
|-------|----------|-------|
| **Lossy-by-Design Parsing** | Three-layer pipeline: Reader → Channel → Parser. Drop lines under pressure. | 1.6 |
| **Pipeline Status in TUI** | Yellow "Metrics" indicator when drops occur | 1.6, 7 |
| **Final Drain Problem** | Parsers read until EOF + 5s drain timeout | 1.7 |
| **Parallel Segment Fetches** | `sync.Map` for inflight requests (not single URL) | 4.1 |
| **Hanging Request Cleanup** | TTL (60s) on inflight requests to prevent memory leaks | 4.1 |
| **Inferred Latency Naming** | Use `inferred_segment_latency` to prevent misinterpretation | 4.1, 6 |
| **Unknown URL Tracking** | Fallback bucket for unrecognized URL patterns | 4.1 |
| **Configurable Drop Threshold** | `--stats-drop-threshold` + track peak drop rate | 1.6 |
| **Wall-Clock Drift** | Track `(Now - StartTime) - OutTimeUS` as health metric | 4.1 |
| **Segment Size** | Estimate from `total_size` delta between progress updates | 4.1 |
| **FFmpeg Version Compat** | Preflight check warns if version differs from tested | 3.4 |
| **TUI Performance** | "Summary Only" mode for 200+ clients | 7 |
| **Metrics Degradation Warning** | Track `stats_lines_dropped_total`, warn in exit summary | 1.6, 6 |
| **Prometheus Cardinality** | Two-tier: aggregate (default) + per-client (`--prom-client-metrics`) | 8 |
| **Memory: T-Digest Latencies** | Use T-Digest (~10KB) instead of raw slice (would be 288MB at scale) | 4.1 |
| **FFmpeg Restart Bytes Reset** | Track `bytesFromPreviousRuns` + `currentProcessBytes` separately | 4.1 |

---

## Table of Contents

1. [Current Code Structure](#current-code-structure)
2. [New Files to Create](#new-files-to-create)
3. [Existing Files to Modify](#existing-files-to-modify)
4. [Phase 1: Output Capture Foundation](#phase-1-output-capture-foundation)
5. [Phase 2: Progress Parser](#phase-2-progress-parser)
6. [Phase 3: HLS Event Parser](#phase-3-hls-event-parser)
7. [Phase 4: Latency Tracking](#phase-4-latency-tracking)
8. [Phase 5: Stats Aggregation](#phase-5-stats-aggregation)
9. [Phase 6: Enhanced Exit Summary](#phase-6-enhanced-exit-summary)
10. [Phase 7: TUI Dashboard](#phase-7-tui-dashboard)
11. [Phase 8: Prometheus Integration](#phase-8-prometheus-integration)

---

## Current Code Structure

```
internal/
├── config/
│   ├── config.go          # Config struct, defaults
│   ├── flags.go           # CLI flag parsing
│   └── validate.go        # Config validation
├── logging/
│   ├── logger.go          # slog setup
│   └── handler.go         # StderrHandler (exists but unused)
├── metrics/
│   ├── collector.go       # Prometheus metrics
│   └── server.go          # HTTP metrics server
├── orchestrator/
│   ├── orchestrator.go    # Main orchestration logic
│   ├── client_manager.go  # Manages supervisors
│   └── ramp_scheduler.go  # Ramp-up scheduling
├── preflight/
│   └── checks.go          # System checks
├── process/
│   ├── ffmpeg.go          # FFmpegRunner, BuildCommand
│   ├── probe.go           # ffprobe variant detection
│   └── runner.go          # ProcessRunner interface
└── supervisor/
    ├── supervisor.go      # Process lifecycle
    ├── state.go           # State enum
    ├── backoff.go         # Exponential backoff
    └── jitter.go          # Jitter source

cmd/go-ffmpeg-hls-swarm/
└── main.go                # Entry point
```

---

## New Files to Create

| File | Purpose | Phase |
|------|---------|-------|
| `internal/parser/pipeline.go` | **Three-layer lossy parsing pipeline** | 1 |
| `internal/parser/pipeline_test.go` | Tests for pipeline (drop behavior) | 1 |
| `internal/parser/progress.go` | Parse `-progress pipe:1` output | 2 |
| `internal/parser/progress_test.go` | Tests for progress parser | 2 |
| `internal/parser/hls_events.go` | Parse HLS stderr events | 3 |
| `internal/parser/hls_events_test.go` | Tests for HLS parser | 3 |
| `internal/preflight/ffmpeg_version.go` | FFmpeg version compatibility check | 3 |
| `internal/preflight/ffmpeg_version_test.go` | Tests for version check | 3 |
| `internal/stats/client_stats.go` | Per-client statistics | 4 |
| `internal/stats/client_stats_test.go` | Tests for client stats | 4 |
| `internal/stats/histogram.go` | Latency percentile calculation | 4 |
| `internal/stats/histogram_test.go` | Tests for histogram | 4 |
| `internal/stats/aggregator.go` | Cross-client aggregation | 5 |
| `internal/stats/aggregator_test.go` | Tests for aggregator | 5 |
| `internal/stats/summary.go` | Exit summary formatter | 6 |
| `internal/tui/model.go` | Bubble Tea model | 7 |
| `internal/tui/view.go` | TUI rendering | 7 |
| `internal/tui/styles.go` | Lipgloss styles | 7 |
| `internal/tui/model_test.go` | Tests for TUI | 7 |
| `testdata/ffmpeg_progress.txt` | Test fixture | 2 |
| `testdata/ffmpeg_hls_verbose.txt` | Test fixture | 3 |

---

## Existing Files to Modify

| File | Modification | Phase |
|------|--------------|-------|
| `internal/process/ffmpeg.go` | Add `-progress pipe:1`, `-loglevel verbose` | 1 |
| `internal/supervisor/supervisor.go` | Attach stdout/stderr pipes, use Pipeline | 1 |
| `internal/orchestrator/orchestrator.go` | Wire up stats, TUI | 5, 7 |
| `internal/orchestrator/client_manager.go` | Pass stats to supervisors | 5 |
| `internal/config/config.go` | Add `StatsBufferSize`, `PromClientMetrics` | 1, 8 |
| `internal/config/flags.go` | Add `-stats`, `-tui`, `-prom-client-metrics` flags | 1, 7, 8 |
| `internal/metrics/collector.go` | Two-tier Prometheus metrics | 8 |
| `internal/logging/handler.go` | Integrate with new parsers | 3 |
| `internal/preflight/checks.go` | Add FFmpeg version check | 3 |
| `cmd/go-ffmpeg-hls-swarm/main.go` | Enhanced exit summary | 6 |
| `go.mod` | Add tdigest (Phase 4), bubbletea, lipgloss (Phase 7) | 4, 7 |

---

## Phase 1: Output Capture Foundation

**Goal**: Modify FFmpeg command and supervisor to capture stdout/stderr

**Duration**: 1 day

### Step 1.1: Add Config Options

**File**: `internal/config/config.go`

**Add after line ~50** (in Config struct):
```go
// Stats collection
StatsEnabled       bool          `json:"stats_enabled"`
StatsLogLevel      string        `json:"stats_log_level"`      // "verbose" or "debug"
StatsBufferSize    int           `json:"stats_buffer_size"`    // Lines to buffer per client
StatsDropThreshold float64       `json:"stats_drop_threshold"` // Degradation threshold (default: 0.01 = 1%)
```

**Add to DefaultConfig()** (around line ~80):
```go
StatsEnabled:       true,
StatsDropThreshold: 0.01,  // 1% drop rate = degraded
StatsLogLevel:   "verbose",
StatsBufferSize: 1000,
```

### Step 1.2: Add CLI Flags

**File**: `internal/config/flags.go`

**Add after line ~95** (in flag definitions):
```go
flag.BoolVar(&cfg.StatsEnabled, "stats", true, "Enable FFmpeg output parsing for detailed stats")
flag.StringVar(&cfg.StatsLogLevel, "stats-loglevel", "verbose", "FFmpeg loglevel for stats: verbose|debug")
flag.IntVar(&cfg.StatsBufferSize, "stats-buffer", 1000, "Lines to buffer per client (increase if seeing drops)")
flag.Float64Var(&cfg.StatsDropThreshold, "stats-drop-threshold", 0.01, "Drop rate threshold for degraded metrics (hidden)")
```

Note: `--stats-drop-threshold` is intentionally undocumented (hidden flag) but configurable for advanced users.

### Step 1.3: Modify FFmpeg Args

**File**: `internal/process/ffmpeg.go`

**Modify buildArgs() function** (around line ~122):

```go
func (r *FFmpegRunner) buildArgs() []string {
    args := []string{
        "-hide_banner",
        "-nostdin",
        "-loglevel", r.config.LogLevel,
    }

    // ADD: Progress output to stdout (key=value format)
    if r.config.StatsEnabled {
        args = append(args, "-progress", "pipe:1")
        // Override loglevel if stats enabled
        args[3] = r.config.StatsLogLevel  // Replace "info" with "verbose"
    }

    // ... rest of function unchanged
}
```

**Add to FFmpegConfig struct** (around line ~35):
```go
// Stats collection (new)
StatsEnabled   bool
StatsLogLevel  string
```

### Step 1.4: Attach Output Pipes in Supervisor

**File**: `internal/supervisor/supervisor.go`

**Modify runOnce() function** (starting around line ~154):

```go
func (s *Supervisor) runOnce(ctx context.Context) (exitCode int, uptime time.Duration, err error) {
    s.setState(StateStarting)

    cmd, err := s.builder.BuildCommand(ctx, s.clientID)
    if err != nil {
        s.logger.Error("failed_to_build_command", "client_id", s.clientID, "error", err)
        return 1, 0, err
    }

    // NEW: Capture stdout for -progress output
    var stdout io.ReadCloser
    if s.statsEnabled {
        stdout, err = cmd.StdoutPipe()
        if err != nil {
            return 1, 0, fmt.Errorf("stdout pipe: %w", err)
        }
    }

    // NEW: Capture stderr for HLS events
    var stderr io.ReadCloser
    if s.statsEnabled {
        stderr, err = cmd.StderrPipe()
        if err != nil {
            return 1, 0, fmt.Errorf("stderr pipe: %w", err)
        }
    }

    cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

    s.cmdMu.Lock()
    s.cmd = cmd
    s.cmdMu.Unlock()

    s.startTime = time.Now()
    if err := cmd.Start(); err != nil {
        s.logger.Error("failed_to_start_process", "client_id", s.clientID, "error", err)
        return 1, 0, err
    }

    pid := cmd.Process.Pid
    s.setState(StateRunning)

    // NEW: Start output parsers in goroutines
    var parseWg sync.WaitGroup
    if s.statsEnabled && stdout != nil {
        parseWg.Add(1)
        go func() {
            defer parseWg.Done()
            s.parseProgress(stdout)
        }()
    }
    if s.statsEnabled && stderr != nil {
        parseWg.Add(1)
        go func() {
            defer parseWg.Done()
            s.parseStderr(stderr)
        }()
    }

    s.logger.Info("client_started", "client_id", s.clientID, "pid", pid)

    if s.callbacks.OnStart != nil {
        s.callbacks.OnStart(s.clientID, pid)
    }

    waitErr := cmd.Wait()
    uptime = time.Since(s.startTime)
    exitCode = extractExitCode(waitErr)

    // NEW: Wait for parsers to finish
    parseWg.Wait()

    // ... rest unchanged
}
```

**Add new fields to Supervisor struct** (around line ~40):
```go
type Supervisor struct {
    // ... existing fields ...

    // Stats collection (new)
    statsEnabled   bool
    progressParser *parser.ProgressParser
    hlsParser      *parser.HLSEventParser
    clientStats    *stats.ClientStats
}
```

**Add placeholder methods** (at end of file):
```go
// parseProgress handles -progress pipe:1 output
// TODO: Implement in Phase 2
func (s *Supervisor) parseProgress(r io.Reader) {
    // Placeholder - drain reader
    io.Copy(io.Discard, r)
}

// parseStderr handles HLS event logging
// TODO: Implement in Phase 3
func (s *Supervisor) parseStderr(r io.Reader) {
    // Placeholder - drain reader
    io.Copy(io.Discard, r)
}
```

### Step 1.5: Write Tests

**File**: `internal/supervisor/supervisor_test.go` (create if not exists)

```go
package supervisor

import (
    "context"
    "testing"
    "time"
)

func TestSupervisor_StatsEnabledCapturesOutput(t *testing.T) {
    // Test that stdout/stderr pipes are created when statsEnabled=true
    // This is a smoke test - detailed parsing tests are in parser package
}

func TestSupervisor_StatsDisabledNoCapture(t *testing.T) {
    // Test that no pipes created when statsEnabled=false
}
```

### Step 1.6: Three-Layer Lossy-by-Design Pipeline

At 200–1000 clients, parsing can't always keep up. The system must be "lossy by design"
to prevent the metrics feature from sabotaging the load test.

**File**: `internal/parser/pipeline.go` (NEW)

```go
package parser

import (
    "bufio"
    "io"
    "sync/atomic"
)

// LineParser is implemented by ProgressParser and HLSEventParser
type LineParser interface {
    ParseLine(line string)
}

// Pipeline implements three-layer lossy-by-design parsing
type Pipeline struct {
    clientID     int
    streamType   string  // "progress" or "stderr"
    bufferSize   int

    lineChan     chan string

    // Pipeline health metrics
    linesRead    int64
    linesDropped int64
    linesParsed  int64
}

// NewPipeline creates a lossy parsing pipeline
func NewPipeline(clientID int, streamType string, bufferSize int) *Pipeline {
    return &Pipeline{
        clientID:   clientID,
        streamType: streamType,
        bufferSize: bufferSize,
        lineChan:   make(chan string, bufferSize),
    }
}

// RunReader is Layer 1: reads lines fast, drops if channel full
// MUST run in dedicated goroutine. Never blocks on channel send.
func (p *Pipeline) RunReader(r io.Reader) {
    scanner := bufio.NewScanner(r)
    buf := make([]byte, 64*1024)
    scanner.Buffer(buf, 1024*1024)

    for scanner.Scan() {
        line := scanner.Text()
        atomic.AddInt64(&p.linesRead, 1)

        // Non-blocking send - drop if full
        select {
        case p.lineChan <- line:
            // OK
        default:
            // Channel full - drop intentionally
            atomic.AddInt64(&p.linesDropped, 1)
        }
    }
    close(p.lineChan)
}

// RunParser is Layer 2: consumes lines at own pace
// MUST run in dedicated goroutine.
func (p *Pipeline) RunParser(parser LineParser) {
    for line := range p.lineChan {
        parser.ParseLine(line)
        atomic.AddInt64(&p.linesParsed, 1)
    }
}

// Stats returns pipeline health metrics
func (p *Pipeline) Stats() (read, dropped, parsed int64) {
    return atomic.LoadInt64(&p.linesRead),
           atomic.LoadInt64(&p.linesDropped),
           atomic.LoadInt64(&p.linesParsed)
}

// IsDegraded returns true if >1% lines dropped
func (p *Pipeline) IsDegraded() bool {
    read, dropped, _ := p.Stats()
    if read == 0 {
        return false
    }
    return float64(dropped)/float64(read) > 0.01
}
```

**File**: `internal/parser/pipeline_test.go` (NEW)

```go
package parser

import (
    "strings"
    "sync"
    "testing"
    "time"
)

type slowParser struct {
    delay time.Duration
    lines []string
    mu    sync.Mutex
}

func (p *slowParser) ParseLine(line string) {
    time.Sleep(p.delay)
    p.mu.Lock()
    p.lines = append(p.lines, line)
    p.mu.Unlock()
}

func TestPipeline_DropsUnderPressure(t *testing.T) {
    // Small buffer, slow parser = should drop lines
    pipeline := NewPipeline(0, "test", 5)  // Only 5 line buffer
    parser := &slowParser{delay: 10 * time.Millisecond}

    // Generate 100 lines quickly
    input := strings.Repeat("line\n", 100)

    var wg sync.WaitGroup
    wg.Add(2)

    go func() {
        defer wg.Done()
        pipeline.RunReader(strings.NewReader(input))
    }()

    go func() {
        defer wg.Done()
        pipeline.RunParser(parser)
    }()

    wg.Wait()

    read, dropped, parsed := pipeline.Stats()

    if read != 100 {
        t.Errorf("read = %d, want 100", read)
    }
    if dropped == 0 {
        t.Error("expected some lines to be dropped")
    }
    if parsed+dropped != read {
        t.Errorf("parsed(%d) + dropped(%d) != read(%d)", parsed, dropped, read)
    }

    t.Logf("Pipeline stats: read=%d, dropped=%d (%.1f%%), parsed=%d",
        read, dropped, float64(dropped)/float64(read)*100, parsed)
}

func TestPipeline_NoDropsWhenFast(t *testing.T) {
    // Large buffer, fast parser = no drops
    pipeline := NewPipeline(0, "test", 1000)
    parser := &slowParser{delay: 0}

    input := strings.Repeat("line\n", 100)

    var wg sync.WaitGroup
    wg.Add(2)

    go func() {
        defer wg.Done()
        pipeline.RunReader(strings.NewReader(input))
    }()

    go func() {
        defer wg.Done()
        pipeline.RunParser(parser)
    }()

    wg.Wait()

    _, dropped, _ := pipeline.Stats()
    if dropped != 0 {
        t.Errorf("dropped = %d, want 0", dropped)
    }
}
```

**Update Supervisor** to use pipelines:

**File**: `internal/supervisor/supervisor.go`

**Add fields:**
```go
type Supervisor struct {
    // ... existing fields ...

    // Parsing pipelines
    progressPipeline *parser.Pipeline
    stderrPipeline   *parser.Pipeline
}
```

**Update runOnce():**
```go
func (s *Supervisor) runOnce(ctx context.Context) (...) {
    // CRITICAL: Accumulate bytes from previous FFmpeg process before starting new one
    // This handles the total_size reset when FFmpeg restarts
    if s.clientStats != nil {
        s.clientStats.OnProcessStart()
    }

    // ... cmd setup ...

    // Create pipelines with configured buffer size
    s.progressPipeline = parser.NewPipeline(
        s.clientID, "progress", s.config.StatsBufferSize,
    )
    s.stderrPipeline = parser.NewPipeline(
        s.clientID, "stderr", s.config.StatsBufferSize,
    )

    // Start Layer 1 (readers) - never block
    go s.progressPipeline.RunReader(stdout)
    go s.stderrPipeline.RunReader(stderr)

    // Start Layer 2 (parsers)
    var parseWg sync.WaitGroup
    parseWg.Add(2)
    go func() {
        defer parseWg.Done()
        s.progressPipeline.RunParser(s.progressParser)
    }()
    go func() {
        defer parseWg.Done()
        s.stderrPipeline.RunParser(s.hlsParser)
    }()

    // ... cmd.Wait() ...

    // Drain with timeout
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

    // Record pipeline health for exit summary
    if s.clientStats != nil {
        _, progDropped, _ := s.progressPipeline.Stats()
        _, stderrDropped, _ := s.stderrPipeline.Stats()
        s.clientStats.RecordDroppedLines(progDropped, stderrDropped)
    }
}
```

### Step 1.7: Drain Timeout for Graceful Shutdown

Ensure parsers get final bytes before exit summary is printed.

**File**: `internal/supervisor/supervisor.go`

**Modify runOnce() after cmd.Wait():**
```go
    waitErr := cmd.Wait()
    uptime = time.Since(s.startTime)
    exitCode = extractExitCode(waitErr)

    // CRITICAL: Wait for parsers to drain remaining pipe data
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
```

### Step 1.8: Write Tests

**File**: `internal/supervisor/supervisor_test.go` (create if not exists)

```go
package supervisor

import (
    "context"
    "testing"
    "time"
)

func TestSupervisor_StatsEnabledCapturesOutput(t *testing.T) {
    // Test that stdout/stderr pipes are created when statsEnabled=true
}

func TestSupervisor_StatsDisabledNoCapture(t *testing.T) {
    // Test that no pipes created when statsEnabled=false
}

func TestSupervisor_DrainTimeoutPreventsHang(t *testing.T) {
    // Test that supervisor doesn't hang if parser never finishes
}
```

### Step 1.9: Verification

```bash
# Build and run with stats enabled
go build ./cmd/go-ffmpeg-hls-swarm
./go-ffmpeg-hls-swarm -clients 1 -duration 10s -stats http://10.177.0.10:17080/stream.m3u8

# Should see FFmpeg progress output in logs (verbose)
```

---

## Phase 2: Progress Parser

**Goal**: Parse FFmpeg's `-progress pipe:1` structured output

**Duration**: 2 days

### Step 2.1: Create Parser Package

**File**: `internal/parser/progress.go`

```go
package parser

import (
    "bufio"
    "io"
    "strconv"
    "strings"
    "sync"
)

// ProgressUpdate represents a single progress report from FFmpeg
type ProgressUpdate struct {
    Frame       int64
    FPS         float64
    Bitrate     string
    TotalSize   int64   // bytes downloaded
    OutTimeUS   int64   // playback position in microseconds
    Speed       float64 // 1.0 = realtime
    Progress    string  // "continue" or "end"
}

// ProgressCallback is called for each complete progress update
type ProgressCallback func(*ProgressUpdate)

// ProgressParser parses FFmpeg -progress pipe:1 output
type ProgressParser struct {
    callback ProgressCallback

    mu      sync.Mutex
    current *ProgressUpdate
}

// NewProgressParser creates a new progress parser
func NewProgressParser(cb ProgressCallback) *ProgressParser {
    return &ProgressParser{
        callback: cb,
        current:  &ProgressUpdate{},
    }
}

// Parse reads from r and parses progress updates
// Blocks until r is closed or returns error
func (p *ProgressParser) Parse(r io.Reader) error {
    scanner := bufio.NewScanner(r)

    for scanner.Scan() {
        line := scanner.Text()
        p.parseLine(line)
    }

    return scanner.Err()
}

// parseLine handles a single line of progress output
func (p *ProgressParser) parseLine(line string) {
    key, value, ok := parseKeyValue(line)
    if !ok {
        return
    }

    p.mu.Lock()
    defer p.mu.Unlock()

    switch key {
    case "frame":
        p.current.Frame, _ = strconv.ParseInt(value, 10, 64)
    case "fps":
        p.current.FPS, _ = strconv.ParseFloat(value, 64)
    case "bitrate":
        p.current.Bitrate = value
    case "total_size":
        p.current.TotalSize, _ = strconv.ParseInt(value, 10, 64)
    case "out_time_us":
        p.current.OutTimeUS, _ = strconv.ParseInt(value, 10, 64)
    case "speed":
        p.current.Speed = parseSpeed(value)
    case "progress":
        p.current.Progress = value
        // End of block - emit update
        if p.callback != nil {
            update := *p.current // copy
            p.callback(&update)
        }
        p.current = &ProgressUpdate{}
    }
}

// parseKeyValue splits "key=value" into parts
func parseKeyValue(line string) (key, value string, ok bool) {
    idx := strings.Index(line, "=")
    if idx < 0 {
        return "", "", false
    }
    return line[:idx], line[idx+1:], true
}

// parseSpeed converts "1.00x" to 1.0
func parseSpeed(s string) float64 {
    s = strings.TrimSuffix(s, "x")
    if s == "N/A" || s == "" {
        return 0
    }
    f, _ := strconv.ParseFloat(s, 64)
    return f
}
```

### Step 2.2: Create Tests

**File**: `internal/parser/progress_test.go`

```go
package parser

import (
    "strings"
    "testing"
)

func TestParseKeyValue(t *testing.T) {
    tests := []struct {
        input   string
        wantKey string
        wantVal string
        wantOK  bool
    }{
        {"frame=100", "frame", "100", true},
        {"speed=1.00x", "speed", "1.00x", true},
        {"bitrate=N/A", "bitrate", "N/A", true},
        {"invalid", "", "", false},
        {"", "", "", false},
        {"=empty_key", "", "empty_key", true},
    }

    for _, tt := range tests {
        t.Run(tt.input, func(t *testing.T) {
            key, val, ok := parseKeyValue(tt.input)
            if ok != tt.wantOK {
                t.Errorf("ok = %v, want %v", ok, tt.wantOK)
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

func TestParseSpeed(t *testing.T) {
    tests := []struct {
        input string
        want  float64
    }{
        {"1.00x", 1.0},
        {"0.95x", 0.95},
        {"1.5x", 1.5},
        {"2.00x", 2.0},
        {"N/A", 0},
        {"", 0},
    }

    for _, tt := range tests {
        t.Run(tt.input, func(t *testing.T) {
            got := parseSpeed(tt.input)
            if got != tt.want {
                t.Errorf("parseSpeed(%q) = %v, want %v", tt.input, got, tt.want)
            }
        })
    }
}

func TestProgressParser_ParseBlock(t *testing.T) {
    input := `frame=0
fps=0.00
bitrate=N/A
total_size=0
out_time_us=0
speed=N/A
progress=continue
frame=60
fps=30.00
bitrate=512.0kbits/s
total_size=51324
out_time_us=2000000
speed=1.00x
progress=continue
`

    var updates []*ProgressUpdate
    p := NewProgressParser(func(u *ProgressUpdate) {
        updates = append(updates, u)
    })

    p.Parse(strings.NewReader(input))

    if len(updates) != 2 {
        t.Fatalf("got %d updates, want 2", len(updates))
    }

    // First update
    if updates[0].Frame != 0 {
        t.Errorf("update[0].Frame = %d, want 0", updates[0].Frame)
    }
    if updates[0].Speed != 0 {
        t.Errorf("update[0].Speed = %v, want 0", updates[0].Speed)
    }

    // Second update
    if updates[1].Frame != 60 {
        t.Errorf("update[1].Frame = %d, want 60", updates[1].Frame)
    }
    if updates[1].TotalSize != 51324 {
        t.Errorf("update[1].TotalSize = %d, want 51324", updates[1].TotalSize)
    }
    if updates[1].OutTimeUS != 2000000 {
        t.Errorf("update[1].OutTimeUS = %d, want 2000000", updates[1].OutTimeUS)
    }
    if updates[1].Speed != 1.0 {
        t.Errorf("update[1].Speed = %v, want 1.0", updates[1].Speed)
    }
}

func TestProgressParser_NoCallback(t *testing.T) {
    // Should not panic with nil callback
    p := NewProgressParser(nil)
    input := "frame=0\nprogress=continue\n"

    err := p.Parse(strings.NewReader(input))
    if err != nil {
        t.Errorf("unexpected error: %v", err)
    }
}
```

### Step 2.3: Create Test Fixture

**File**: `testdata/ffmpeg_progress.txt`

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

### Step 2.4: Wire Up in Supervisor

**File**: `internal/supervisor/supervisor.go`

**Update parseProgress method**:
```go
func (s *Supervisor) parseProgress(r io.Reader) {
    p := parser.NewProgressParser(func(update *parser.ProgressUpdate) {
        // Update client stats
        if s.clientStats != nil {
            s.clientStats.UpdateFromProgress(update)
        }
    })

    if err := p.Parse(r); err != nil {
        s.logger.Debug("progress_parser_error", "client_id", s.clientID, "error", err)
    }
}
```

### Step 2.5: Run Tests

```bash
# Run parser tests
go test -v ./internal/parser/

# Run with race detector
go test -race ./internal/parser/

# Check coverage
go test -cover ./internal/parser/
```

---

## Phase 3: HLS Event Parser

**Goal**: Parse FFmpeg stderr for HLS-specific events

**Duration**: 2 days

### Step 3.1: Create HLS Event Parser

**File**: `internal/parser/hls_events.go`

```go
package parser

import (
    "bufio"
    "io"
    "regexp"
    "strconv"
    "strings"
    "time"
)

// URLType identifies the type of URL being requested
type URLType int

const (
    URLTypeUnknown URLType = iota
    URLTypeManifest        // .m3u8
    URLTypeSegment         // .ts
    URLTypeInit            // .mp4 init segment
)

// HLSEvent represents a parsed HLS event
type HLSEvent struct {
    Type      HLSEventType
    URL       string
    URLType   URLType
    HTTPCode  int
    Timestamp time.Time
}

// HLSEventType identifies the type of event
type HLSEventType int

const (
    EventUnknown HLSEventType = iota
    EventRequest              // URL opened for reading
    EventHTTPError            // Server returned error
    EventReconnect            // Reconnection attempt
    EventTimeout              // Connection timeout
)

// StatsRecorder is the interface for recording stats
type StatsRecorder interface {
    IncrementManifestRequests()
    IncrementSegmentRequests()
    RecordHTTPError(code int)
    RecordReconnection()
    RecordTimeout()
    OnSegmentRequestStart(url string)
    OnSegmentRequestComplete()
}

// HLSEventParser parses FFmpeg stderr for HLS events
type HLSEventParser struct {
    clientID int
    stats    StatsRecorder
}

// Regex patterns for parsing
var (
    reOpening  = regexp.MustCompile(`Opening '([^']+)' for reading`)
    reHTTPErr  = regexp.MustCompile(`Server returned (\d{3})`)
    reReconn   = regexp.MustCompile(`Reconnecting`)
    reTimeout  = regexp.MustCompile(`(?i)(timed? ?out|timeout)`)
)

// NewHLSEventParser creates a new HLS event parser
func NewHLSEventParser(clientID int, stats StatsRecorder) *HLSEventParser {
    return &HLSEventParser{
        clientID: clientID,
        stats:    stats,
    }
}

// Parse reads from r and parses HLS events
func (p *HLSEventParser) Parse(r io.Reader) error {
    scanner := bufio.NewScanner(r)
    // Larger buffer for long FFmpeg lines
    buf := make([]byte, 0, 4096)
    scanner.Buffer(buf, 1024*1024)

    for scanner.Scan() {
        line := scanner.Text()
        p.parseLine(line)
    }

    return scanner.Err()
}

// parseLine handles a single line of stderr
func (p *HLSEventParser) parseLine(line string) {
    // Opening URL
    if m := reOpening.FindStringSubmatch(line); m != nil {
        url := m[1]
        urlType := classifyURL(url)

        switch urlType {
        case URLTypeManifest:
            p.stats.IncrementManifestRequests()
        case URLTypeSegment:
            p.stats.IncrementSegmentRequests()
            p.stats.OnSegmentRequestStart(url)
        }
        return
    }

    // HTTP error
    if m := reHTTPErr.FindStringSubmatch(line); m != nil {
        code, _ := strconv.Atoi(m[1])
        p.stats.RecordHTTPError(code)
        return
    }

    // Reconnection
    if reReconn.MatchString(line) {
        p.stats.RecordReconnection()
        return
    }

    // Timeout
    if reTimeout.MatchString(line) {
        p.stats.RecordTimeout()
        return
    }
}

// classifyURL determines the type of URL
func classifyURL(url string) URLType {
    lower := strings.ToLower(url)

    if strings.HasSuffix(lower, ".m3u8") {
        return URLTypeManifest
    }
    if strings.HasSuffix(lower, ".ts") {
        return URLTypeSegment
    }
    if strings.HasSuffix(lower, ".mp4") {
        return URLTypeInit
    }

    // Check for query strings
    if idx := strings.Index(lower, "?"); idx > 0 {
        path := lower[:idx]
        if strings.HasSuffix(path, ".m3u8") {
            return URLTypeManifest
        }
        if strings.HasSuffix(path, ".ts") {
            return URLTypeSegment
        }
    }

    return URLTypeUnknown
}
```

### Step 3.2: Create Tests

**File**: `internal/parser/hls_events_test.go`

```go
package parser

import (
    "strings"
    "sync/atomic"
    "testing"
)

// mockStats implements StatsRecorder for testing
type mockStats struct {
    manifestReqs   int64
    segmentReqs    int64
    httpErrors     map[int]int64
    reconnections  int64
    timeouts       int64
    segmentStarts  []string
}

func newMockStats() *mockStats {
    return &mockStats{httpErrors: make(map[int]int64)}
}

func (m *mockStats) IncrementManifestRequests()    { atomic.AddInt64(&m.manifestReqs, 1) }
func (m *mockStats) IncrementSegmentRequests()     { atomic.AddInt64(&m.segmentReqs, 1) }
func (m *mockStats) RecordHTTPError(code int)       { m.httpErrors[code]++ }
func (m *mockStats) RecordReconnection()            { atomic.AddInt64(&m.reconnections, 1) }
func (m *mockStats) RecordTimeout()                 { atomic.AddInt64(&m.timeouts, 1) }
func (m *mockStats) OnSegmentRequestStart(url string) { m.segmentStarts = append(m.segmentStarts, url) }
func (m *mockStats) OnSegmentRequestComplete()      {}

func TestClassifyURL(t *testing.T) {
    tests := []struct {
        url  string
        want URLType
    }{
        {"http://example.com/stream.m3u8", URLTypeManifest},
        {"http://example.com/720p.m3u8", URLTypeManifest},
        {"http://example.com/seg00001.ts", URLTypeSegment},
        {"http://example.com/init.mp4", URLTypeInit},
        {"http://example.com/stream.m3u8?token=abc", URLTypeManifest},
        {"http://example.com/segment.ts?v=123", URLTypeSegment},
        {"http://example.com/unknown", URLTypeUnknown},
    }

    for _, tt := range tests {
        t.Run(tt.url, func(t *testing.T) {
            got := classifyURL(tt.url)
            if got != tt.want {
                t.Errorf("classifyURL(%q) = %v, want %v", tt.url, got, tt.want)
            }
        })
    }
}

func TestHLSEventParser_Requests(t *testing.T) {
    input := `[hls @ 0x55f8] Opening 'http://example.com/stream.m3u8' for reading
[https @ 0x55f8] Opening 'http://example.com/seg00001.ts' for reading
[hls @ 0x55f8] Opening 'http://example.com/stream.m3u8' for reading
[https @ 0x55f8] Opening 'http://example.com/seg00002.ts' for reading
[https @ 0x55f8] Opening 'http://example.com/seg00003.ts' for reading
`

    stats := newMockStats()
    p := NewHLSEventParser(0, stats)
    p.Parse(strings.NewReader(input))

    if stats.manifestReqs != 2 {
        t.Errorf("manifestReqs = %d, want 2", stats.manifestReqs)
    }
    if stats.segmentReqs != 3 {
        t.Errorf("segmentReqs = %d, want 3", stats.segmentReqs)
    }
}

func TestHLSEventParser_Errors(t *testing.T) {
    input := `Server returned 503 Service Unavailable
Server returned 404 Not Found
Server returned 503 Service Unavailable
Connection timed out
Reconnecting to http://example.com
`

    stats := newMockStats()
    p := NewHLSEventParser(0, stats)
    p.Parse(strings.NewReader(input))

    if stats.httpErrors[503] != 2 {
        t.Errorf("httpErrors[503] = %d, want 2", stats.httpErrors[503])
    }
    if stats.httpErrors[404] != 1 {
        t.Errorf("httpErrors[404] = %d, want 1", stats.httpErrors[404])
    }
    if stats.timeouts != 1 {
        t.Errorf("timeouts = %d, want 1", stats.timeouts)
    }
    if stats.reconnections != 1 {
        t.Errorf("reconnections = %d, want 1", stats.reconnections)
    }
}
```

### Step 3.3: Wire Up in Supervisor

**File**: `internal/supervisor/supervisor.go`

**Update parseStderr method**:
```go
func (s *Supervisor) parseStderr(r io.Reader) {
    p := parser.NewHLSEventParser(s.clientID, s.clientStats)

    if err := p.Parse(r); err != nil {
        s.logger.Debug("stderr_parser_error", "client_id", s.clientID, "error", err)
    }
}
```

### Step 3.4: FFmpeg Version Compatibility Check

FFmpeg log formats vary between versions (6.x, 7.x, 8.x). Add a preflight warning.

**File**: `internal/preflight/ffmpeg_version.go` (new file)

```go
package preflight

import (
    "fmt"
    "os/exec"
    "regexp"
    "strings"
)

var (
    versionRegex      = regexp.MustCompile(`ffmpeg version (\d+\.\d+)`)
    supportedVersions = []string{"6.", "7.", "8."}
)

// CheckFFmpegVersion verifies FFmpeg version compatibility
func CheckFFmpegVersion(ffmpegPath string) (version string, warning string, err error) {
    out, err := exec.Command(ffmpegPath, "-version").Output()
    if err != nil {
        return "", "", fmt.Errorf("failed to get ffmpeg version: %w", err)
    }

    matches := versionRegex.FindStringSubmatch(string(out))
    if len(matches) < 2 {
        return "unknown", "Could not parse FFmpeg version; metrics parsing may be degraded", nil
    }

    version = matches[1]

    supported := false
    for _, v := range supportedVersions {
        if strings.HasPrefix(version, v) {
            supported = true
            break
        }
    }

    if !supported {
        warning = fmt.Sprintf("FFmpeg %s not tested; metrics parsing may be degraded", version)
    }

    return version, warning, nil
}
```

**File**: `internal/preflight/ffmpeg_version_test.go`

```go
package preflight

import "testing"

func TestParseFFmpegVersion(t *testing.T) {
    tests := []struct {
        output  string
        want    string
        wantErr bool
    }{
        {"ffmpeg version 8.0 Copyright (c) 2000-2025", "8.0", false},
        {"ffmpeg version 6.1.1 Copyright (c) 2000-2024", "6.1", false},
        {"ffmpeg version n7.0-dev", "7.0", false},
        {"invalid output", "unknown", false},
    }

    for _, tt := range tests {
        // Test version extraction logic
    }
}
```

**Wire up in preflight checks** (`internal/preflight/checks.go`):

```go
// Add to RunPreflightChecks()
version, warning, err := CheckFFmpegVersion(cfg.FFmpegPath)
if err != nil {
    return err
}
if warning != "" {
    logger.Warn("ffmpeg_version_warning", "version", version, "warning", warning)
}
```

---

## Phase 4: Latency Tracking

**Goal**: Track segment download latencies and calculate percentiles

**Duration**: 2-3 days

### Step 4.0: Add T-Digest Dependency

The T-Digest algorithm provides memory-efficient percentile calculation. Without it, storing
all latency samples would consume ~288MB for a 5000-client, 1-hour test.

```bash
go get github.com/influxdata/tdigest@latest
```

**Why T-Digest:**
- Constant memory (~10KB per histogram) regardless of sample count
- Accurate percentiles (within 1% for P50-P99)
- Mergeable (can combine histograms from multiple clients)
- Battle-tested (used by InfluxDB, Prometheus)

### Step 4.1: Create Client Stats

**File**: `internal/stats/client_stats.go`

```go
package stats

import (
    "strings"
    "sync"
    "sync/atomic"
    "time"

    "github.com/influxdata/tdigest"
)

const (
    StallThreshold     = 0.9   // Speed below this = stalling
    StallDuration      = 5 * time.Second
    HighDriftThreshold = 5 * time.Second
    SegmentSizeRingSize = 100  // Track last 100 segment sizes
)

// ClientStats holds per-client statistics
type ClientStats struct {
    ClientID  int
    StartTime time.Time

    // Request counts (atomic)
    ManifestRequests int64
    SegmentRequests  int64
    UnknownRequests  int64  // Fallback for unrecognized URL patterns

    // Bytes tracking - CRITICAL: handles FFmpeg restart resets
    // When FFmpeg restarts, total_size resets to 0. We must track
    // cumulative bytes across all FFmpeg instances for this client.
    bytesFromPreviousRuns int64  // Sum from all completed FFmpeg processes
    currentProcessBytes   int64  // Current FFmpeg's total_size
    bytesMu               sync.Mutex

    // Error counts
    HTTPErrors    map[int]int64
    httpErrorsMu  sync.Mutex
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
    LastPlaybackTime time.Duration  // OutTimeUS converted
    CurrentDrift     time.Duration  // Wall-clock - playback-clock
    MaxDrift         time.Duration
    driftMu          sync.Mutex

    // Pipeline health (lossy-by-design metrics)
    ProgressLinesDropped int64
    StderrLinesDropped   int64
    ProgressLinesRead    int64
    StderrLinesRead      int64
    PeakDropRate         float64  // Track peak, not just current
    peakDropMu           sync.Mutex
}

// RecordDroppedLines records lines dropped by parsing pipelines
// Also tracks peak drop rate for correlation with load spikes
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

// CurrentDropRate returns current drop rate (0.0 to 1.0)
func (s *ClientStats) CurrentDropRate() float64 {
    totalRead := atomic.LoadInt64(&s.ProgressLinesRead) + atomic.LoadInt64(&s.StderrLinesRead)
    totalDropped := atomic.LoadInt64(&s.ProgressLinesDropped) + atomic.LoadInt64(&s.StderrLinesDropped)
    if totalRead == 0 {
        return 0
    }
    return float64(totalDropped) / float64(totalRead)
}

// MetricsDegraded returns true if drop rate exceeds threshold
// threshold is typically 0.01 (1%) but can be configured
func (s *ClientStats) MetricsDegraded(threshold float64) bool {
    return s.CurrentDropRate() > threshold
}

// GetPeakDropRate returns the highest drop rate observed
func (s *ClientStats) GetPeakDropRate() float64 {
    s.peakDropMu.Lock()
    defer s.peakDropMu.Unlock()
    return s.PeakDropRate
}

// NewClientStats creates stats for a client
func NewClientStats(clientID int) *ClientStats {
    return &ClientStats{
        ClientID:              clientID,
        StartTime:             time.Now(),
        HTTPErrors:            make(map[int]int64),
        inferredLatencyDigest: tdigest.NewWithCompression(100), // ~100 centroids, ~10KB
        segmentSizes:          make([]int64, SegmentSizeRingSize),
    }
}

// --- URL Classification ---

// ClassifyAndCountURL classifies URL and increments appropriate counter
func (s *ClientStats) ClassifyAndCountURL(url string) {
    switch {
    case strings.HasSuffix(url, ".m3u8"),
         strings.Contains(url, ".m3u8?"),
         strings.HasSuffix(url, "/master.m3u8"),
         strings.HasSuffix(url, "/playlist.m3u8"):
        atomic.AddInt64(&s.ManifestRequests, 1)

    case strings.HasSuffix(url, ".ts"),
         strings.Contains(url, ".ts?"),
         strings.HasSuffix(url, ".mp4"),
         strings.Contains(url, ".mp4?"):
        atomic.AddInt64(&s.SegmentRequests, 1)

    default:
        // Fallback bucket for unrecognized patterns
        // Helps diagnose: byte-range playlists, signed URLs, weird CDN behavior
        atomic.AddInt64(&s.UnknownRequests, 1)
    }
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

// UpdateCurrentBytes updates bytes from current FFmpeg's total_size
func (s *ClientStats) UpdateCurrentBytes(totalSize int64) {
    s.bytesMu.Lock()
    s.currentProcessBytes = totalSize
    s.bytesMu.Unlock()
}

// TotalBytes returns cumulative bytes across all FFmpeg restarts
func (s *ClientStats) TotalBytes() int64 {
    s.bytesMu.Lock()
    defer s.bytesMu.Unlock()
    return s.bytesFromPreviousRuns + s.currentProcessBytes
}

// --- Inferred Latency Tracking (T-Digest for constant memory) ---
// IMPORTANT: Latency is INFERRED from FFmpeg events, not directly measured.
// Use for trend analysis, not absolute performance claims.

func (s *ClientStats) recordInferredLatency(d time.Duration) {
    s.inferredLatencyMu.Lock()
    s.inferredLatencyDigest.Add(float64(d.Nanoseconds()), 1)
    s.inferredLatencyCount++
    s.inferredLatencySum += d
    if d > s.inferredLatencyMax {
        s.inferredLatencyMax = d
    }
    s.inferredLatencyMu.Unlock()
}

func (s *ClientStats) InferredLatencyP50() time.Duration {
    s.inferredLatencyMu.Lock()
    defer s.inferredLatencyMu.Unlock()
    return time.Duration(s.inferredLatencyDigest.Quantile(0.50))
}

func (s *ClientStats) InferredLatencyP95() time.Duration {
    s.inferredLatencyMu.Lock()
    defer s.inferredLatencyMu.Unlock()
    return time.Duration(s.inferredLatencyDigest.Quantile(0.95))
}

func (s *ClientStats) InferredLatencyP99() time.Duration {
    s.inferredLatencyMu.Lock()
    defer s.inferredLatencyMu.Unlock()
    return time.Duration(s.inferredLatencyDigest.Quantile(0.99))
}

func (s *ClientStats) InferredLatencyMax() time.Duration {
    s.inferredLatencyMu.Lock()
    defer s.inferredLatencyMu.Unlock()
    return s.inferredLatencyMax
}

func (s *ClientStats) InferredLatencyCount() int64 {
    return atomic.LoadInt64(&s.inferredLatencyCount)
}

// Implement StatsRecorder interface

func (s *ClientStats) IncrementManifestRequests() {
    atomic.AddInt64(&s.ManifestRequests, 1)
}

func (s *ClientStats) IncrementSegmentRequests() {
    atomic.AddInt64(&s.SegmentRequests, 1)
}

func (s *ClientStats) RecordHTTPError(code int) {
    s.httpErrorsMu.Lock()
    s.HTTPErrors[code]++
    s.httpErrorsMu.Unlock()
}

func (s *ClientStats) RecordReconnection() {
    atomic.AddInt64(&s.Reconnections, 1)
}

func (s *ClientStats) RecordTimeout() {
    atomic.AddInt64(&s.Timeouts, 1)
}

// OnSegmentRequestStart tracks parallel segment fetches using sync.Map
func (s *ClientStats) OnSegmentRequestStart(url string) {
    s.inflightRequests.Store(url, time.Now())
}

// OnSegmentRequestComplete is called when we detect a segment completed
// Uses URL to find the matching inflight request
func (s *ClientStats) OnSegmentRequestComplete(url string) {
    if startTime, ok := s.inflightRequests.LoadAndDelete(url); ok {
        latency := time.Since(startTime.(time.Time))
        s.recordInferredLatency(latency)  // Note: INFERRED, not measured
    }
}

const (
    // HangingRequestTTL is the maximum time a request can be "inflight"
    // before we consider it a timeout and clean it up to prevent memory leaks
    HangingRequestTTL = 60 * time.Second
)

// CompleteOldestSegment completes the oldest inflight .ts request
// Called on progress updates when we don't know which segment completed.
// Also cleans up "hanging" requests older than TTL to prevent memory leaks.
func (s *ClientStats) CompleteOldestSegment() {
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

        // Find oldest segment request
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

    // Complete oldest segment if found
    if oldestURL != "" {
        s.OnSegmentRequestComplete(oldestURL)
    }
}

// InflightRequestCount returns the number of pending requests (for debugging)
func (s *ClientStats) InflightRequestCount() int {
    count := 0
    s.inflightRequests.Range(func(_, _ interface{}) bool {
        count++
        return true
    })
    return count
}

// UpdateFromProgress updates stats from FFmpeg progress output
func (s *ClientStats) UpdateFromProgress(p *parser.ProgressUpdate) {
    // Estimate segment size from total_size delta
    if s.lastTotalSize > 0 && p.TotalSize > s.lastTotalSize {
        segmentSize := p.TotalSize - s.lastTotalSize
        s.recordSegmentSize(segmentSize)
    }
    s.lastTotalSize = p.TotalSize

    // CRITICAL: Use UpdateCurrentBytes instead of atomic.StoreInt64
    // This properly handles FFmpeg restart resets
    s.UpdateCurrentBytes(p.TotalSize)

    // Update wall-clock drift
    // Drift = (Now - StartTime) - PlaybackTime
    if p.OutTimeUS > 0 {
        playbackTime := time.Duration(p.OutTimeUS) * time.Microsecond
        wallClockElapsed := time.Since(s.StartTime)

        s.driftMu.Lock()
        s.LastPlaybackTime = playbackTime
        s.CurrentDrift = wallClockElapsed - playbackTime
        if s.CurrentDrift > s.MaxDrift {
            s.MaxDrift = s.CurrentDrift
        }
        s.driftMu.Unlock()
    }

    // Update speed and check for stall
    s.speedMu.Lock()
    s.CurrentSpeed = p.Speed

    if p.Speed > 0 && p.Speed < StallThreshold {
        if s.speedBelowThresholdAt.IsZero() {
            s.speedBelowThresholdAt = time.Now()
        }
    } else {
        s.speedBelowThresholdAt = time.Time{}
    }
    s.speedMu.Unlock()

    // Complete oldest pending segment request on progress update
    s.CompleteOldestSegment()
}

func (s *ClientStats) recordSegmentSize(size int64) {
    s.segmentSizeMu.Lock()
    s.segmentSizes[s.segmentSizeIdx] = size
    s.segmentSizeIdx = (s.segmentSizeIdx + 1) % SegmentSizeRingSize
    s.segmentSizeMu.Unlock()
}

// HasHighDrift returns true if drift exceeds threshold
func (s *ClientStats) HasHighDrift() bool {
    s.driftMu.Lock()
    defer s.driftMu.Unlock()
    return s.CurrentDrift > HighDriftThreshold
}

// IsStalled returns true if client has been below speed threshold for too long
func (s *ClientStats) IsStalled() bool {
    s.speedMu.Lock()
    defer s.speedMu.Unlock()

    if s.speedBelowThresholdAt.IsZero() {
        return false
    }
    return time.Since(s.speedBelowThresholdAt) > StallDuration
}
```

### Step 4.2: Create Latency Histogram

**File**: `internal/stats/histogram.go`

```go
package stats

import (
    "sort"
    "time"
)

// LatencyHistogram calculates percentiles from latency samples
type LatencyHistogram struct {
    samples []time.Duration
}

// NewLatencyHistogram creates a new histogram
func NewLatencyHistogram() *LatencyHistogram {
    return &LatencyHistogram{
        samples: make([]time.Duration, 0, 10000),
    }
}

// Add adds a latency sample
func (h *LatencyHistogram) Add(d time.Duration) {
    h.samples = append(h.samples, d)
}

// AddAll adds multiple samples
func (h *LatencyHistogram) AddAll(ds []time.Duration) {
    h.samples = append(h.samples, ds...)
}

// Percentile returns the p-th percentile (0.0 to 1.0)
func (h *LatencyHistogram) Percentile(p float64) time.Duration {
    if len(h.samples) == 0 {
        return 0
    }

    // Sort samples
    sorted := make([]time.Duration, len(h.samples))
    copy(sorted, h.samples)
    sort.Slice(sorted, func(i, j int) bool {
        return sorted[i] < sorted[j]
    })

    // Calculate index
    idx := int(float64(len(sorted)-1) * p)
    return sorted[idx]
}

// Max returns the maximum latency
func (h *LatencyHistogram) Max() time.Duration {
    if len(h.samples) == 0 {
        return 0
    }

    max := h.samples[0]
    for _, s := range h.samples[1:] {
        if s > max {
            max = s
        }
    }
    return max
}

// Count returns the number of samples
func (h *LatencyHistogram) Count() int {
    return len(h.samples)
}

// Reset clears all samples
func (h *LatencyHistogram) Reset() {
    h.samples = h.samples[:0]
}
```

### Step 4.3: Create Tests

**File**: `internal/stats/client_stats_test.go`

```go
package stats

import (
    "sync"
    "testing"
    "time"
)

func TestClientStats_ConcurrentIncrements(t *testing.T) {
    stats := NewClientStats(0)

    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            stats.IncrementManifestRequests()
            stats.IncrementSegmentRequests()
        }()
    }
    wg.Wait()

    if stats.ManifestRequests != 100 {
        t.Errorf("ManifestRequests = %d, want 100", stats.ManifestRequests)
    }
    if stats.SegmentRequests != 100 {
        t.Errorf("SegmentRequests = %d, want 100", stats.SegmentRequests)
    }
}

func TestClientStats_LatencyTracking(t *testing.T) {
    stats := NewClientStats(0)

    // Simulate segment download
    stats.OnSegmentRequestStart("http://example.com/seg001.ts")
    time.Sleep(10 * time.Millisecond)
    stats.OnSegmentRequestComplete()

    latencies := stats.GetLatencies()
    if len(latencies) != 1 {
        t.Fatalf("got %d latencies, want 1", len(latencies))
    }

    if latencies[0] < 10*time.Millisecond {
        t.Errorf("latency = %v, want >= 10ms", latencies[0])
    }
}

func TestClientStats_StallDetection(t *testing.T) {
    stats := NewClientStats(0)

    // Not stalled at normal speed
    stats.CurrentSpeed = 1.0
    if stats.IsStalled() {
        t.Error("should not be stalled at speed 1.0")
    }

    // Drop below threshold
    stats.speedMu.Lock()
    stats.CurrentSpeed = 0.5
    stats.speedBelowThresholdAt = time.Now().Add(-6 * time.Second) // 6s ago
    stats.speedMu.Unlock()

    if !stats.IsStalled() {
        t.Error("should be stalled after 5s below threshold")
    }
}
```

**File**: `internal/stats/histogram_test.go`

```go
package stats

import (
    "testing"
    "time"
)

func TestLatencyHistogram_Percentiles(t *testing.T) {
    h := NewLatencyHistogram()

    // Add 100 samples: 1ms, 2ms, ..., 100ms
    for i := 1; i <= 100; i++ {
        h.Add(time.Duration(i) * time.Millisecond)
    }

    tests := []struct {
        p       float64
        wantMin time.Duration
        wantMax time.Duration
    }{
        {0.50, 49 * time.Millisecond, 51 * time.Millisecond},
        {0.95, 94 * time.Millisecond, 96 * time.Millisecond},
        {0.99, 98 * time.Millisecond, 100 * time.Millisecond},
    }

    for _, tt := range tests {
        got := h.Percentile(tt.p)
        if got < tt.wantMin || got > tt.wantMax {
            t.Errorf("P%.0f = %v, want %v-%v", tt.p*100, got, tt.wantMin, tt.wantMax)
        }
    }
}

func TestLatencyHistogram_Empty(t *testing.T) {
    h := NewLatencyHistogram()

    if h.Percentile(0.5) != 0 {
        t.Error("P50 of empty histogram should be 0")
    }
    if h.Max() != 0 {
        t.Error("Max of empty histogram should be 0")
    }
}
```

---

## Phase 5: Stats Aggregation

**Goal**: Aggregate statistics across all clients

**Duration**: 2 days

### Step 5.1: Create Aggregator

**File**: `internal/stats/aggregator.go`

```go
package stats

import (
    "sync"
    "time"
)

// AggregatedStats holds metrics across all clients
type AggregatedStats struct {
    // Client counts
    TotalClients   int
    ActiveClients  int
    StalledClients int

    // Request totals
    TotalManifestReqs    int64
    TotalSegmentReqs     int64
    TotalUnknownReqs     int64  // Fallback bucket for unrecognized URLs
    TotalBytesDownloaded int64

    // Rates (per second)
    ManifestReqRate       float64
    SegmentReqRate        float64
    ThroughputBytesPerSec float64

    // Errors
    TotalHTTPErrors    map[int]int64
    TotalReconnections int64
    TotalTimeouts      int64
    ErrorRate          float64 // errors / total requests

    // INFERRED Latency (T-Digest aggregated percentiles)
    // Note: Inferred from FFmpeg events - use for trends, not absolutes
    InferredLatencyP50 time.Duration
    InferredLatencyP95 time.Duration
    InferredLatencyP99 time.Duration
    InferredLatencyMax time.Duration

    // Segment Size (estimated)
    AvgSegmentSize int64
    MinSegmentSize int64
    MaxSegmentSize int64

    // Health
    ClientsAboveRealtime int
    ClientsBelowRealtime int
    AverageSpeed         float64

    // Wall-clock Drift (critical for HLS testing)
    AverageDrift        time.Duration
    MaxDrift            time.Duration
    ClientsWithHighDrift int  // Drift > 5 seconds

    // Pipeline health (lossy-by-design)
    TotalLinesDropped    int64
    TotalLinesRead       int64
    ClientsWithDrops     int
    MetricsDegraded      bool     // Drop rate > threshold (default 1%)
    PeakDropRate         float64  // Highest observed drop rate (correlate with load)

    // For TUI detailed mode (optional)
    PerClientStats []*ClientStats
}

// StatsAggregator aggregates stats from multiple clients
type StatsAggregator struct {
    mu        sync.RWMutex
    clients   map[int]*ClientStats
    startTime time.Time

    // Previous totals for rate calculation
    prevManifestReqs int64
    prevSegmentReqs  int64
    prevBytes        int64
    prevTime         time.Time
}

// NewStatsAggregator creates a new aggregator
func NewStatsAggregator() *StatsAggregator {
    return &StatsAggregator{
        clients:   make(map[int]*ClientStats),
        startTime: time.Now(),
        prevTime:  time.Now(),
    }
}

// AddClient registers a client for aggregation
func (a *StatsAggregator) AddClient(stats *ClientStats) {
    a.mu.Lock()
    a.clients[stats.ClientID] = stats
    a.mu.Unlock()
}

// RemoveClient unregisters a client
func (a *StatsAggregator) RemoveClient(clientID int) {
    a.mu.Lock()
    delete(a.clients, clientID)
    a.mu.Unlock()
}

// Aggregate computes aggregated statistics
func (a *StatsAggregator) Aggregate() *AggregatedStats {
    a.mu.RLock()
    defer a.mu.RUnlock()

    now := time.Now()
    elapsed := now.Sub(a.startTime).Seconds()
    rateElapsed := now.Sub(a.prevTime).Seconds()

    result := &AggregatedStats{
        TotalClients:    len(a.clients),
        TotalHTTPErrors: make(map[int]int64),
    }

    var totalSpeed float64
    histogram := NewLatencyHistogram()

    for _, c := range a.clients {
        result.ActiveClients++

        // Sum request counts
        result.TotalManifestReqs += c.ManifestRequests
        result.TotalSegmentReqs += c.SegmentRequests
        result.TotalBytesDownloaded += c.BytesDownloaded

        // Sum errors
        c.httpErrorsMu.Lock()
        for code, count := range c.HTTPErrors {
            result.TotalHTTPErrors[code] += count
        }
        c.httpErrorsMu.Unlock()
        result.TotalReconnections += c.Reconnections
        result.TotalTimeouts += c.Timeouts

        // Latencies
        histogram.AddAll(c.GetLatencies())

        // Speed/health
        c.speedMu.Lock()
        speed := c.CurrentSpeed
        c.speedMu.Unlock()

        if speed > 0 {
            totalSpeed += speed
            if speed >= 1.0 {
                result.ClientsAboveRealtime++
            } else {
                result.ClientsBelowRealtime++
            }
        }

        if c.IsStalled() {
            result.StalledClients++
        }
    }

    // Calculate rates
    if elapsed > 0 {
        result.ManifestReqRate = float64(result.TotalManifestReqs) / elapsed
        result.SegmentReqRate = float64(result.TotalSegmentReqs) / elapsed
        result.ThroughputBytesPerSec = float64(result.TotalBytesDownloaded) / elapsed
    }

    // Calculate percentiles
    result.SegmentLatencyP50 = histogram.Percentile(0.50)
    result.SegmentLatencyP95 = histogram.Percentile(0.95)
    result.SegmentLatencyP99 = histogram.Percentile(0.99)
    result.SegmentLatencyMax = histogram.Max()

    // Average speed
    if result.ActiveClients > 0 {
        result.AverageSpeed = totalSpeed / float64(result.ActiveClients)
    }

    // Error rate
    totalReqs := result.TotalManifestReqs + result.TotalSegmentReqs
    totalErrors := int64(0)
    for _, count := range result.TotalHTTPErrors {
        totalErrors += count
    }
    totalErrors += result.TotalTimeouts

    if totalReqs > 0 {
        result.ErrorRate = float64(totalErrors) / float64(totalReqs)
    }

    // Update prev for next rate calculation
    a.prevManifestReqs = result.TotalManifestReqs
    a.prevSegmentReqs = result.TotalSegmentReqs
    a.prevBytes = result.TotalBytesDownloaded
    a.prevTime = now

    return result
}
```

### Step 5.2: Wire Into Orchestrator

**File**: `internal/orchestrator/orchestrator.go`

**Add fields** (around line ~30):
```go
type Orchestrator struct {
    // ... existing fields ...

    // Stats aggregation (new)
    aggregator *stats.StatsAggregator
}
```

**Update NewOrchestrator** (add aggregator initialization):
```go
func NewOrchestrator(cfg *config.Config, ...) *Orchestrator {
    return &Orchestrator{
        // ... existing ...
        aggregator: stats.NewStatsAggregator(),
    }
}
```

**Update client creation** to pass ClientStats to supervisor.

---

## Phase 6: Enhanced Exit Summary

**Goal**: Display comprehensive statistics at program exit

**Duration**: 1 day

### Step 6.1: Create Summary Formatter

**File**: `internal/stats/summary.go`

```go
package stats

import (
    "fmt"
    "strings"
    "time"
)

// FormatExitSummary formats aggregated stats for display
func FormatExitSummary(stats *AggregatedStats, duration time.Duration, targetClients int) string {
    var b strings.Builder

    b.WriteString("\n")
    b.WriteString("═══════════════════════════════════════════════════════════════════════════════\n")
    b.WriteString("                        go-ffmpeg-hls-swarm Exit Summary\n")
    b.WriteString("═══════════════════════════════════════════════════════════════════════════════\n\n")

    // Metrics degradation warning (lossy-by-design feature)
    if stats.MetricsDegraded {
        b.WriteString("⚠️  METRICS DEGRADED: Parsing could not keep up with FFmpeg output\n")
        fmt.Fprintf(&b, "    Lines dropped: %s across %d clients\n",
            formatNumber(stats.TotalLinesDropped),
            stats.ClientsWithDrops,
        )
        b.WriteString("    Consider: --stats-buffer 2000 or fewer clients for accurate metrics\n\n")
    }

    // Run info
    fmt.Fprintf(&b, "Run Duration:           %s\n", formatDuration(duration))
    fmt.Fprintf(&b, "Target Clients:         %d\n", targetClients)
    fmt.Fprintf(&b, "Peak Active Clients:    %d\n\n", stats.TotalClients)

    // Request statistics
    b.WriteString("───────────────────────────────────────────────────────────────────────────────\n")
    b.WriteString("                              Request Statistics\n")
    b.WriteString("───────────────────────────────────────────────────────────────────────────────\n\n")

    fmt.Fprintf(&b, "  %-20s %12s %12s %12s\n", "Request Type", "Total", "Rate (/sec)", "Per Client")
    b.WriteString("  " + strings.Repeat("─", 58) + "\n")
    fmt.Fprintf(&b, "  %-20s %12s %12.1f %12d\n",
        "Manifest (.m3u8)",
        formatNumber(stats.TotalManifestReqs),
        stats.ManifestReqRate,
        stats.TotalManifestReqs/int64(max(stats.TotalClients, 1)),
    )
    fmt.Fprintf(&b, "  %-20s %12s %12.1f %12d\n",
        "Segments (.ts)",
        formatNumber(stats.TotalSegmentReqs),
        stats.SegmentReqRate,
        stats.TotalSegmentReqs/int64(max(stats.TotalClients, 1)),
    )
    fmt.Fprintf(&b, "\n  Total Bytes:          %s  (%s/s)\n\n",
        formatBytes(stats.TotalBytesDownloaded),
        formatBytes(int64(stats.ThroughputBytesPerSec)),
    )

    // Inferred Latency (not directly measured)
    b.WriteString("───────────────────────────────────────────────────────────────────────────────\n")
    b.WriteString("                          Inferred Segment Latency *\n")
    b.WriteString("───────────────────────────────────────────────────────────────────────────────\n\n")

    fmt.Fprintf(&b, "  %-12s %12s\n", "Percentile", "Latency")
    b.WriteString("  " + strings.Repeat("─", 26) + "\n")
    fmt.Fprintf(&b, "  %-12s %12s\n", "P50 (median)", formatMs(stats.InferredLatencyP50))
    fmt.Fprintf(&b, "  %-12s %12s\n", "P95", formatMs(stats.InferredLatencyP95))
    fmt.Fprintf(&b, "  %-12s %12s\n", "P99", formatMs(stats.InferredLatencyP99))
    fmt.Fprintf(&b, "  %-12s %12s\n", "Max", formatMs(stats.InferredLatencyMax))
    b.WriteString("\n  * Inferred from FFmpeg events; use for trends, not absolute values.\n\n")

    // Playback health
    b.WriteString("───────────────────────────────────────────────────────────────────────────────\n")
    b.WriteString("                              Playback Health\n")
    b.WriteString("───────────────────────────────────────────────────────────────────────────────\n\n")

    total := stats.ClientsAboveRealtime + stats.ClientsBelowRealtime
    if total > 0 {
        fmt.Fprintf(&b, "  >= 1.0x (healthy):    %d (%d%%)\n",
            stats.ClientsAboveRealtime,
            stats.ClientsAboveRealtime*100/total,
        )
        fmt.Fprintf(&b, "  < 1.0x (buffering):   %d (%d%%)\n",
            stats.ClientsBelowRealtime,
            stats.ClientsBelowRealtime*100/total,
        )
    }
    fmt.Fprintf(&b, "  Average Speed:        %.2fx\n", stats.AverageSpeed)
    fmt.Fprintf(&b, "  Stalled Clients:      %d\n\n", stats.StalledClients)

    // Errors
    b.WriteString("───────────────────────────────────────────────────────────────────────────────\n")
    b.WriteString("                                  Errors\n")
    b.WriteString("───────────────────────────────────────────────────────────────────────────────\n\n")

    totalErrors := int64(0)
    for code, count := range stats.TotalHTTPErrors {
        fmt.Fprintf(&b, "  HTTP %d:               %d\n", code, count)
        totalErrors += count
    }
    fmt.Fprintf(&b, "  Timeouts:             %d\n", stats.TotalTimeouts)
    fmt.Fprintf(&b, "  Reconnections:        %d\n", stats.TotalReconnections)
    fmt.Fprintf(&b, "  Error Rate:           %.4f%%\n\n", stats.ErrorRate*100)

    // Footnotes (diagnostic information)
    b.WriteString(renderFootnotes(stats))

    b.WriteString("═══════════════════════════════════════════════════════════════════════════════\n")

    return b.String()
}

// renderFootnotes adds diagnostic info that doesn't belong in main metrics
func renderFootnotes(stats *AggregatedStats) string {
    var footnotes []string

    // Always include latency disclaimer
    footnotes = append(footnotes,
        "[1] Latency is inferred from FFmpeg events; use for trends, not absolute values.")

    // Only include unknown URLs if any were observed
    if stats.TotalUnknownReqs > 0 {
        footnotes = append(footnotes, fmt.Sprintf(
            "[2] Unknown URL requests: %d (may indicate byte-range playlists, signed URLs)",
            stats.TotalUnknownReqs))
    }

    // Include peak drop rate if any drops occurred
    if stats.PeakDropRate > 0 {
        footnotes = append(footnotes, fmt.Sprintf(
            "[3] Peak metrics drop rate: %.1f%%",
            stats.PeakDropRate * 100))
    }

    if len(footnotes) == 0 {
        return ""
    }

    var b strings.Builder
    b.WriteString("───────────────────────────────────────────────────────────────────────────────\n")
    b.WriteString("                                 Footnotes\n")
    b.WriteString("───────────────────────────────────────────────────────────────────────────────\n\n")
    for _, fn := range footnotes {
        fmt.Fprintf(&b, "  %s\n", fn)
    }
    b.WriteString("\n")
    return b.String()
}

func formatDuration(d time.Duration) string {
    h := int(d.Hours())
    m := int(d.Minutes()) % 60
    s := int(d.Seconds()) % 60
    return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

func formatNumber(n int64) string {
    if n >= 1_000_000 {
        return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
    }
    if n >= 1_000 {
        return fmt.Sprintf("%.1fK", float64(n)/1_000)
    }
    return fmt.Sprintf("%d", n)
}

func formatBytes(n int64) string {
    if n >= 1_000_000_000 {
        return fmt.Sprintf("%.2f GB", float64(n)/1_000_000_000)
    }
    if n >= 1_000_000 {
        return fmt.Sprintf("%.2f MB", float64(n)/1_000_000)
    }
    if n >= 1_000 {
        return fmt.Sprintf("%.2f KB", float64(n)/1_000)
    }
    return fmt.Sprintf("%d B", n)
}

func formatMs(d time.Duration) string {
    return fmt.Sprintf("%d ms", d.Milliseconds())
}

func max(a, b int) int {
    if a > b {
        return a
    }
    return b
}
```

### Step 6.2: Update main.go

**File**: `cmd/go-ffmpeg-hls-swarm/main.go`

At the end of main(), after orchestrator stops:

```go
// Print enhanced exit summary
if cfg.StatsEnabled {
    stats := orchestrator.GetAggregatedStats()
    fmt.Println(stats.FormatExitSummary(stats, runDuration, cfg.Clients))
}
```

---

## Phase 7: TUI Dashboard

**Goal**: Live terminal dashboard with Bubble Tea

**Duration**: 3-4 days

See [METRICS_ENHANCEMENT_DESIGN.md](METRICS_ENHANCEMENT_DESIGN.md#live-dashboard-design) for TUI implementation details.

### Step 7.1: Add Dependencies

```bash
go get github.com/charmbracelet/bubbletea@latest
go get github.com/charmbracelet/lipgloss@latest
go get github.com/charmbracelet/bubbles@latest
```

### Step 7.2: Create TUI Package

Create files:
- `internal/tui/model.go` - Bubble Tea model
- `internal/tui/view.go` - Rendering functions
- `internal/tui/styles.go` - Lipgloss styles
- `internal/tui/model_test.go` - Tests

### Step 7.2.1: Pipeline Status Visual Feedback

When lines are dropped due to parser backpressure, give immediate visual feedback:

**File**: `internal/tui/styles.go`

```go
package tui

import "github.com/charmbracelet/lipgloss"

var (
    // Status colors
    statusOK       = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))  // Green
    statusWarning  = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // Yellow/Orange
    statusError    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")) // Red

    // Labels
    metricsLabelOK      = statusOK.Render("● Metrics")
    metricsLabelWarning = statusWarning.Render("● Metrics (degraded)")
    metricsLabelError   = statusError.Render("● Metrics (severely degraded)")
)

// GetMetricsLabel returns styled label based on drop rate
func GetMetricsLabel(dropRate float64) string {
    switch {
    case dropRate > 0.10:  // >10% dropped
        return metricsLabelError
    case dropRate > 0.0:   // Any drops
        return metricsLabelWarning
    default:
        return metricsLabelOK
    }
}
```

**File**: `internal/tui/view.go`

```go
func (m Model) renderHeader() string {
    // Show pipeline status in header
    metricsLabel := GetMetricsLabel(m.stats.PipelineDropRate)

    return fmt.Sprintf(
        "go-ffmpeg-hls-swarm | %s | Clients: %d/%d | Elapsed: %s",
        metricsLabel,
        m.stats.ActiveClients,
        m.stats.TargetClients,
        formatDuration(m.stats.Elapsed),
    )
}
```

**Visual result:**
```
┌─────────────────────────────────────────────────────────────────────────────┐
│ go-ffmpeg-hls-swarm | ● Metrics | Clients: 100/100 | Elapsed: 45s          │  ← Green (OK)
│ go-ffmpeg-hls-swarm | ● Metrics (degraded) | Clients: 500/500 | Elapsed: 2m│  ← Yellow (some drops)
│ go-ffmpeg-hls-swarm | ● Metrics (severely degraded) | ...                  │  ← Red (>10% drops)
└─────────────────────────────────────────────────────────────────────────────┘
```

This gives users immediate feedback that their **local system** (not the target) is struggling
to keep up with parsing FFmpeg output.

### Step 7.3: Add CLI Flag

**File**: `internal/config/flags.go`

```go
flag.BoolVar(&cfg.TUIEnabled, "tui", true, "Enable live terminal dashboard")
```

---

## Phase 8: Prometheus Integration

**Goal**: Export metrics to Prometheus

**Duration**: 1-2 days

### Step 8.1: Complete Prometheus Metrics for Grafana Dashboard

This section defines ALL metrics needed to build a full-featured Grafana dashboard.
Organized by dashboard panel for clarity.

**File**: `internal/metrics/collector.go`

---

#### Panel 1: Test Overview

```go
var (
    // Info metric - use labels for metadata (cardinality: 1)
    hlsSwarmInfo = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "hls_swarm_info",
            Help: "Information about the load test (value always 1)",
        },
        []string{"version", "stream_url", "profile"},
    )

    // Test configuration
    hlsTargetClients = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "hls_swarm_target_clients",
            Help: "Target number of clients to reach",
        },
    )
    hlsTestDurationSeconds = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "hls_swarm_test_duration_seconds",
            Help: "Configured test duration",
        },
    )

    // Test progress
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
            Help: "Seconds remaining until test ends",
        },
    )
)
```

---

#### Panel 2: Request Rates & Throughput

```go
var (
    // Request counters (use rate() in PromQL)
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
    hlsRequestsTotal = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "hls_swarm_requests_total",
            Help: "Total HTTP requests (manifests + segments)",
        },
    )

    // Throughput
    hlsBytesDownloadedTotal = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "hls_swarm_bytes_downloaded_total",
            Help: "Total bytes downloaded",
        },
    )

    // Current rates (convenience gauges, updated each tick)
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
```

---

#### Panel 3: Latency Distribution

```go
var (
    // Histogram for heatmaps and histogram_quantile()
    hlsSegmentLatencySeconds = prometheus.NewHistogram(
        prometheus.HistogramOpts{
            Name: "hls_swarm_segment_latency_seconds",
            Help: "Segment download latency distribution",
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
            Name: "hls_swarm_segment_latency_p50_seconds",
            Help: "Segment latency 50th percentile (median)",
        },
    )
    hlsLatencyP95Seconds = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "hls_swarm_segment_latency_p95_seconds",
            Help: "Segment latency 95th percentile",
        },
    )
    hlsLatencyP99Seconds = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "hls_swarm_segment_latency_p99_seconds",
            Help: "Segment latency 99th percentile",
        },
    )
    hlsLatencyMaxSeconds = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "hls_swarm_segment_latency_max_seconds",
            Help: "Maximum segment latency observed",
        },
    )
)
```

---

#### Panel 4: Client Health & Playback

```go
var (
    // Speed distribution
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
    hlsMinSpeed = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "hls_swarm_min_speed",
            Help: "Minimum client playback speed",
        },
    )
    hlsMaxSpeed = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "hls_swarm_max_speed",
            Help: "Maximum client playback speed",
        },
    )

    // Drift metrics
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
```

---

#### Panel 5: Errors & Recovery

```go
var (
    // HTTP errors by status code (low cardinality: ~5-10 codes)
    hlsHTTPErrorsTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "hls_swarm_http_errors_total",
            Help: "HTTP errors by status code",
        },
        []string{"status_code"},
    )

    // Network issues
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

    // Client lifecycle
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
        []string{"category"},  // "success", "error", "signal"
    )

    // Error rate (convenience gauge)
    hlsErrorRate = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "hls_swarm_error_rate",
            Help: "Current error rate (errors/total requests)",
        },
    )
)
```

---

#### Panel 6: Segment Statistics

```go
var (
    // Segment sizes (estimated from throughput delta)
    hlsAvgSegmentSizeBytes = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "hls_swarm_avg_segment_size_bytes",
            Help: "Average segment size (estimated)",
        },
    )
    hlsMinSegmentSizeBytes = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "hls_swarm_min_segment_size_bytes",
            Help: "Minimum segment size observed",
        },
    )
    hlsMaxSegmentSizeBytes = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "hls_swarm_max_segment_size_bytes",
            Help: "Maximum segment size observed",
        },
    )
)
```

---

#### Panel 7: Pipeline Health (Metrics System)

```go
var (
    // Lines dropped by stream type
    hlsStatsLinesDroppedTotal = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "hls_swarm_stats_lines_dropped_total",
            Help: "FFmpeg output lines dropped (parser backpressure)",
        },
        []string{"stream"},  // "progress" | "stderr"
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
)
```

---

#### Tier 2: Per-Client Metrics (Optional, `--prom-client-metrics`)

**Tier 2: Per-Client Metrics (Optional)**

Enabled with `--prom-client-metrics`. Use only for debugging with <200 clients.

```go
var (
    // Per-client gauges (only if enabled)
    hlsClientSpeed *prometheus.GaugeVec    // label: client_id
    hlsClientDrift *prometheus.GaugeVec    // label: client_id
    hlsClientBytes *prometheus.CounterVec  // label: client_id
)

func initPerClientMetrics() {
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
    hlsClientBytes = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "hls_swarm_client_bytes_total",
            Help: "Per-client bytes downloaded (requires --prom-client-metrics)",
        },
        []string{"client_id"},
    )

    prometheus.MustRegister(hlsClientSpeed, hlsClientDrift, hlsClientBytes)
}
```

### Step 8.2: Update Collector

**File**: `internal/metrics/collector.go`

```go
type Collector struct {
    perClientEnabled bool

    // Track for cleanup
    registeredClientIDs map[int]struct{}
    mu                  sync.Mutex
}

func NewCollector(perClientMetrics bool) *Collector {
    c := &Collector{
        perClientEnabled:    perClientMetrics,
        registeredClientIDs: make(map[int]struct{}),
    }

    // Register tier 1 (always)
    prometheus.MustRegister(
        hlsManifestRequests,
        hlsSegmentRequests,
        hlsBytesDownloaded,
        hlsSegmentLatency,
        hlsHTTPErrors,
        hlsActiveClients,
        hlsStalledClients,
        hlsHighDriftClients,
        hlsAverageSpeed,
        hlsAverageDrift,
        statsLinesDropped,
        statsClientsDegraded,
    )

    // Register tier 2 (optional)
    if perClientMetrics {
        initPerClientMetrics()
    }

    return c
}

// RecordStats updates metrics from aggregated stats
func (c *Collector) RecordStats(stats *AggregatedStats) {
    // Tier 1: Aggregate metrics
    hlsManifestRequests.Add(float64(stats.ManifestReqsDelta))
    hlsSegmentRequests.Add(float64(stats.SegmentReqsDelta))
    hlsBytesDownloaded.Add(float64(stats.BytesDelta))

    hlsActiveClients.Set(float64(stats.ActiveClients))
    hlsStalledClients.Set(float64(stats.StalledClients))
    hlsHighDriftClients.Set(float64(stats.ClientsWithHighDrift))
    hlsAverageSpeed.Set(stats.AverageSpeed)
    hlsAverageDrift.Set(stats.AverageDrift.Seconds())

    // HTTP errors by status code
    for code, count := range stats.HTTPErrorsDelta {
        hlsHTTPErrors.WithLabelValues(strconv.Itoa(code)).Add(float64(count))
    }

    // Tier 2: Per-client (only if enabled)
    if c.perClientEnabled && stats.PerClientStats != nil {
        for _, cs := range stats.PerClientStats {
            clientID := strconv.Itoa(cs.ClientID)
            hlsClientSpeed.WithLabelValues(clientID).Set(cs.CurrentSpeed)
            hlsClientDrift.WithLabelValues(clientID).Set(cs.CurrentDrift.Seconds())
            hlsClientBytes.WithLabelValues(clientID).Add(float64(cs.BytesDelta))
        }
    }
}
```

### Step 8.3: Add Config Flag

**File**: `internal/config/flags.go`

```go
flag.BoolVar(&cfg.PromClientMetrics, "prom-client-metrics", false,
    "Enable per-client Prometheus metrics (WARNING: high cardinality, use with <200 clients)")
```

### Step 8.4: Test Metadata Metrics (for Grafana dashboards)

These metrics make `/metrics` self-contained for external dashboards:

```go
// Test configuration and progress
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
        Help: "Configured test duration",
    },
)
hlsTestElapsedSeconds = prometheus.NewGauge(
    prometheus.GaugeOpts{
        Name: "hls_swarm_test_elapsed_seconds",
        Help: "Elapsed test time",
    },
)

// Pre-calculated percentiles (convenience for simple Grafana panels)
hlsLatencyP50Seconds = prometheus.NewGauge(...)
hlsLatencyP95Seconds = prometheus.NewGauge(...)
hlsLatencyP99Seconds = prometheus.NewGauge(...)
```

### Step 8.5: Complete Metrics Reference

#### All Tier 1 Metrics (Safe for 1000+ clients)

| Panel | Metric | Type | Labels | Description |
|-------|--------|------|--------|-------------|
| **Overview** | | | | |
| | `hls_swarm_info` | Gauge | version, stream_url, profile | Test metadata (always 1) |
| | `hls_swarm_target_clients` | Gauge | - | Target client count |
| | `hls_swarm_active_clients` | Gauge | - | Currently running clients |
| | `hls_swarm_ramp_progress` | Gauge | - | Ramp progress (0.0-1.0) |
| | `hls_swarm_test_duration_seconds` | Gauge | - | Configured duration |
| | `hls_swarm_test_elapsed_seconds` | Gauge | - | Time since start |
| | `hls_swarm_test_remaining_seconds` | Gauge | - | Time until end |
| **Requests** | | | | |
| | `hls_swarm_manifest_requests_total` | Counter | - | Total .m3u8 requests |
| | `hls_swarm_segment_requests_total` | Counter | - | Total .ts requests |
| | `hls_swarm_requests_total` | Counter | - | All HTTP requests |
| | `hls_swarm_bytes_downloaded_total` | Counter | - | Total bytes |
| | `hls_swarm_manifest_requests_per_second` | Gauge | - | Current manifest rate |
| | `hls_swarm_segment_requests_per_second` | Gauge | - | Current segment rate |
| | `hls_swarm_throughput_bytes_per_second` | Gauge | - | Current throughput |
| **Latency** | | | | |
| | `hls_swarm_segment_latency_seconds` | Histogram | - | Latency distribution |
| | `hls_swarm_segment_latency_p50_seconds` | Gauge | - | P50 (median) |
| | `hls_swarm_segment_latency_p95_seconds` | Gauge | - | P95 |
| | `hls_swarm_segment_latency_p99_seconds` | Gauge | - | P99 |
| | `hls_swarm_segment_latency_max_seconds` | Gauge | - | Maximum |
| **Health** | | | | |
| | `hls_swarm_clients_above_realtime` | Gauge | - | Speed >= 1.0 |
| | `hls_swarm_clients_below_realtime` | Gauge | - | Speed < 1.0 |
| | `hls_swarm_stalled_clients` | Gauge | - | Stalled (>5s slow) |
| | `hls_swarm_average_speed` | Gauge | - | Mean speed |
| | `hls_swarm_min_speed` | Gauge | - | Slowest client |
| | `hls_swarm_max_speed` | Gauge | - | Fastest client |
| | `hls_swarm_high_drift_clients` | Gauge | - | Drift > 5s |
| | `hls_swarm_average_drift_seconds` | Gauge | - | Mean drift |
| | `hls_swarm_max_drift_seconds` | Gauge | - | Max drift |
| **Errors** | | | | |
| | `hls_swarm_http_errors_total` | Counter | status_code | By HTTP code |
| | `hls_swarm_timeouts_total` | Counter | - | Network timeouts |
| | `hls_swarm_reconnections_total` | Counter | - | FFmpeg reconnects |
| | `hls_swarm_client_starts_total` | Counter | - | Process starts |
| | `hls_swarm_client_restarts_total` | Counter | - | Restart attempts |
| | `hls_swarm_client_exits_total` | Counter | category | Exit categories |
| | `hls_swarm_error_rate` | Gauge | - | Current error rate |
| **Segments** | | | | |
| | `hls_swarm_avg_segment_size_bytes` | Gauge | - | Mean segment size |
| | `hls_swarm_min_segment_size_bytes` | Gauge | - | Min segment size |
| | `hls_swarm_max_segment_size_bytes` | Gauge | - | Max segment size |
| **Pipeline** | | | | |
| | `hls_swarm_stats_lines_dropped_total` | Counter | stream | Dropped lines |
| | `hls_swarm_stats_lines_parsed_total` | Counter | stream | Parsed lines |
| | `hls_swarm_stats_clients_degraded` | Gauge | - | Degraded clients |
| | `hls_swarm_stats_drop_rate` | Gauge | - | Overall drop rate |

#### Tier 2 Metrics (Optional, `--prom-client-metrics`)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hls_swarm_client_speed` | Gauge | client_id | Per-client speed |
| `hls_swarm_client_drift_seconds` | Gauge | client_id | Per-client drift |
| `hls_swarm_client_bytes_total` | Counter | client_id | Per-client bytes |

### Step 8.6: Cardinality Summary

| Category | Metrics | Cardinality |
|----------|---------|-------------|
| Overview | 7 | 7 |
| Requests | 7 | 7 |
| Latency | 5 | 5 + histogram buckets |
| Health | 9 | 9 |
| Errors | 7 | ~12 (status codes vary) |
| Segments | 3 | 3 |
| Pipeline | 4 | 6 (2 stream labels) |
| **Tier 1 Total** | **42** | **~50** |
| Per-Client (Tier 2) | 3 | **3 × N** |

**Default (Tier 1):** ~50 time series (constant)
**With --prom-client-metrics:** ~50 + 3×N time series

### Step 8.7: Example Grafana Dashboard JSON

The metrics above support this dashboard layout:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ Row 1: Test Overview                                                        │
├─────────────────┬─────────────────┬─────────────────┬─────────────────────┤
│ Target: 1000    │ Active: 847     │ Ramp: 84.7%     │ Remaining: 2m 15s   │
├─────────────────┴─────────────────┴─────────────────┴─────────────────────┤
│ Row 2: Request Rates (Time Series)                                         │
├───────────────────────────────────┬───────────────────────────────────────┤
│ [Manifest/Segment req/s graph]    │ [Throughput MB/s graph]               │
├───────────────────────────────────┴───────────────────────────────────────┤
│ Row 3: Latency                                                             │
├───────────────────────────────────┬───────────────────────────────────────┤
│ [Latency Heatmap]                 │ P50: 45ms  P95: 120ms  P99: 340ms     │
├───────────────────────────────────┴───────────────────────────────────────┤
│ Row 4: Client Health                                                       │
├─────────────────┬─────────────────┬─────────────────┬─────────────────────┤
│ Healthy: 812    │ Buffering: 35   │ Stalled: 0      │ High Drift: 2       │
│ (95.8%)         │ (4.1%)          │ (0%)            │ (0.2%)              │
├─────────────────┴─────────────────┴─────────────────┴─────────────────────┤
│ Row 5: Speed & Drift (Time Series)                                         │
├───────────────────────────────────┬───────────────────────────────────────┤
│ [Avg/Min/Max Speed graph]         │ [Avg/Max Drift graph]                 │
├───────────────────────────────────┴───────────────────────────────────────┤
│ Row 6: Errors                                                              │
├───────────────────────────────────┬───────────────────────────────────────┤
│ [HTTP Errors by Code stacked]     │ [Timeouts/Reconnects graph]           │
├───────────────────────────────────┴───────────────────────────────────────┤
│ Row 7: System Health                                                       │
├───────────────────────────────────┬───────────────────────────────────────┤
│ [Metrics Drop Rate]               │ Degraded Clients: 0                   │
└───────────────────────────────────┴───────────────────────────────────────┘
```

### Step 8.8: Example PromQL Queries

```promql
# Row 1: Overview
hls_swarm_active_clients
hls_swarm_target_clients
hls_swarm_ramp_progress * 100
hls_swarm_test_remaining_seconds

# Row 2: Request rates (from counters)
rate(hls_swarm_segment_requests_total[1m])
rate(hls_swarm_manifest_requests_total[1m])
rate(hls_swarm_bytes_downloaded_total[1m]) / 1024 / 1024  # MB/s

# Row 3: Latency heatmap
sum(rate(hls_swarm_segment_latency_seconds_bucket[1m])) by (le)
# Or use pre-calculated:
hls_swarm_segment_latency_p99_seconds * 1000  # ms

# Row 4: Client health percentages
hls_swarm_clients_above_realtime / hls_swarm_active_clients * 100
hls_swarm_clients_below_realtime / hls_swarm_active_clients * 100

# Row 5: Speed distribution
hls_swarm_average_speed
hls_swarm_min_speed
hls_swarm_max_speed

# Row 6: Error rate
sum(rate(hls_swarm_http_errors_total[1m]))
  / rate(hls_swarm_requests_total[1m]) * 100

# Row 7: Metrics health
hls_swarm_stats_drop_rate * 100
```

### Step 8.9: Prometheus Scrape Interval Configuration

**Important:** The Prometheus scrape interval must be configured correctly relative to
the FFmpeg progress update frequency.

**FFmpeg Progress Update Period:**
- Default: ~500ms (FFmpeg updates `-progress` output roughly twice per second)
- Configurable via `-stats_period` (e.g., `-stats_period 1` for 1 second)

**Recommended Scrape Intervals:**

| FFmpeg Period | Min Scrape Interval | Recommended | Why |
|---------------|---------------------|-------------|-----|
| 500ms | 1s | 2s | Avoid stale-looking graphs |
| 1s | 2s | 5s | Standard for most use cases |
| 2s | 5s | 10s | High client counts, lower overhead |

**Rule of Thumb:** Scrape interval should be at least **2x the FFmpeg progress period**.

**Example Prometheus Configuration:**

```yaml
# prometheus.yml
global:
  scrape_interval: 5s      # Default
  evaluation_interval: 5s

scrape_configs:
  - job_name: 'hls-swarm'
    scrape_interval: 2s    # Override: faster for real-time dashboard
    static_configs:
      - targets: ['localhost:17090']
```

**Why This Matters:**

1. **Too slow scraping** (e.g., 15s with 500ms progress): Graphs look "steppy" and delayed
2. **Too fast scraping** (e.g., 100ms): Unnecessary load on both sides, no benefit
3. **Correct scraping** (2-5s): Smooth graphs, reasonable load

**Grafana Dashboard Settings:**

```
# For live dashboards, set:
- Refresh interval: 5s (or 2s for real-time feel)
- Time range: "Last 5 minutes" for live tests
- Use instant queries for gauges, range queries for rate()
```

### Step 8.10: Wire Up

Update Collector to receive stats updates and increment Prometheus metrics.

---

## Verification Checklist

### After Each Phase

- [ ] All new tests pass: `go test ./...`
- [ ] Race detector passes: `go test -race ./...`
- [ ] Coverage meets target: `go test -cover ./...`
- [ ] Code compiles: `go build ./...`
- [ ] Manual test with real FFmpeg

### Final Verification

- [ ] Run 100-client test with stats enabled
- [ ] Verify exit summary shows request counts
- [ ] Verify latency percentiles are reasonable
- [ ] Verify Prometheus metrics are exported
- [ ] TUI displays and updates correctly
- [ ] Run 500-client test, verify lossy pipeline doesn't sabotage test
- [ ] Verify "METRICS DEGRADED" warning appears when drops occur
- [ ] Verify `hls_swarm_stats_lines_dropped_total` Prometheus metric works

---

## Summary

| Phase | Focus | Files Created | Files Modified | Tests | Duration |
|-------|-------|--------------|----------------|-------|----------|
| 1 | **Lossy Pipeline** + Output Capture | 2 | 4 | 4 | 2 days |
| 2 | Progress Parser | 2 | 1 | 3 | 2 days |
| 3 | HLS Events + Version Check | 3 | 2 | 3 | 2 days |
| 4 | Latency + Drift + Segment Size | 4 | 0 | 5 | 2-3 days |
| 5 | Stats Aggregation | 2 | 2 | 2 | 2 days |
| 6 | Enhanced Exit Summary + Degradation Warning | 1 | 1 | 1 | 1 day |
| 7 | TUI Dashboard (Summary Mode) | 4 | 3 | 2 | 3-4 days |
| 8 | Prometheus Integration + Pipeline Metrics | 0 | 1 | 2 | 1-2 days |

**Total: ~16-21 days**

### Metrics Accuracy Summary

| Metric | Accuracy | Notes |
|--------|----------|-------|
| Throughput | ✅ High | Direct from `total_size` |
| Request Counts | ✅ High | Reliable stderr patterns |
| HTTP Errors | ✅ High | Reliable stderr patterns |
| Stall Detection | ✅ High | Direct from `speed` |
| **Inferred** Latency | ⚠️ Estimated | From FFmpeg events; use for trends |
| Segment Size | ⚠️ Estimated | Delta of `total_size` |
| Wall-Clock Drift | ✅ High | Calculated from `out_time_us` |

### Exit Summary Footnotes

The exit summary includes footnotes for diagnostic information that doesn't belong in main metrics:

```
═══════════════════════════════════════════════════════════════════════════════

Footnotes:
  [1] Latency is inferred from FFmpeg events, not measured. Use for trends.
  [2] Unknown URL requests: 42 (may indicate byte-range playlists, signed URLs)
  [3] Peak metrics drop rate: 2.3% (during ramp-up at 0:45)

═══════════════════════════════════════════════════════════════════════════════
```

**Code:**
```go
// Add footnotes section at end of exit summary
func (s *ExitSummary) renderFootnotes(stats *AggregatedStats) string {
    var footnotes []string

    // Always include latency disclaimer
    footnotes = append(footnotes,
        "[1] Latency is inferred from FFmpeg events, not measured. Use for trends.")

    // Only include unknown URLs if any were observed
    if stats.TotalUnknownRequests > 0 {
        footnotes = append(footnotes, fmt.Sprintf(
            "[2] Unknown URL requests: %d (may indicate byte-range playlists, signed URLs)",
            stats.TotalUnknownRequests))
    }

    // Include peak drop rate if any drops occurred
    if stats.PeakDropRate > 0 {
        footnotes = append(footnotes, fmt.Sprintf(
            "[3] Peak metrics drop rate: %.1f%%",
            stats.PeakDropRate * 100))
    }

    if len(footnotes) == 0 {
        return ""
    }

    var b strings.Builder
    b.WriteString("\nFootnotes:\n")
    for _, fn := range footnotes {
        fmt.Fprintf(&b, "  %s\n", fn)
    }
    return b.String()
}
```
