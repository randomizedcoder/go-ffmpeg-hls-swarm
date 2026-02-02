# FFmpeg Metrics Socket Design

> **Status**: IMPLEMENTED (Phase 6.6 Complete)
> **Date**: 2026-01-22 (Updated: 2026-01-23)
> **Author**: AI Assistant
> **Related Documents**:
> - [METRICS_ENHANCEMENT_DESIGN.md](METRICS_ENHANCEMENT_DESIGN.md)
> - [METRICS_IMPLEMENTATION_PLAN.md](METRICS_IMPLEMENTATION_PLAN.md)
> - [TUI_DEFECTS.md](TUI_DEFECTS.md) - Defect E, G
> - [FFMPEG_HLS_REFERENCE.md](FFMPEG_HLS_REFERENCE.md) - §13 (FFmpeg Source Code Log Events)
> - [FFMPEG_METRICS_SOCKET_IMPLEMENTATION_LOG.md](FFMPEG_METRICS_SOCKET_IMPLEMENTATION_LOG.md)

---

## Table of Contents

- [1. Executive Summary](#1-executive-summary)
- [2. Problem Statement](#2-problem-statement)
- [3. Solution Architecture](#3-solution-architecture)
- [4. Detailed Design](#4-detailed-design)
  - [4.2 FFmpeg Command Changes](#42-ffmpeg-command-changes)
  - [4.3 Per-Client User-Agent for Debugging](#43-per-client-user-agent-for-debugging)
  - [4.4 Unified LineSource Interface](#44-unified-linesource-interface)
- [5. Data Flow Diagrams](#5-data-flow-diagrams)
- [6. Performance Considerations](#6-performance-considerations)
- [7. Risk Analysis and Mitigation](#7-risk-analysis-and-mitigation)
- [8. Testing Strategy](#8-testing-strategy)
- [9. Rollback Plan](#9-rollback-plan)
- [10. Success Criteria](#10-success-criteria)
- [11. Debug Parser: High-Value Metrics](#11-debug-parser-high-value-metrics)
  - [11.4 Combined Debug Event Types](#114-combined-debug-event-types)
  - [11.4.1 Event Source Reference](#1141-event-source-reference-ffmpeg-source-code)
  - [11.4.2 Critical Events for Load Testing](#1142-critical-events-for-load-testing)
  - [11.5 Aggregated Debug Metrics](#115-aggregated-debug-metrics-in-debugstats)
  - [11.6 TUI Dashboard](#116-tui-dashboard-debug-metrics-panel)
  - [11.7 Early Warning Indicators](#117-early-warning-indicators)
- [12. Quality of Experience (QoE) Metrics](#12-quality-of-experience-qoe-metrics)

---

## 1. Executive Summary

This document proposes separating FFmpeg's `-progress` output from stderr using **Unix domain sockets**. This approach, inspired by [ffmpeg-go](https://github.com/u2takey/ffmpeg-go)'s `showProgress.go`, provides:

1. **Clean separation** - Progress key=value pairs on dedicated socket, stderr for events/logs
2. **Simpler parsing** - No regex to distinguish progress from debug logs
3. **Any log level** - Can use `-loglevel debug` without corrupting progress parsing
4. **Better TUI** - Raw debug logs won't break the TUI layout (Defect E)
5. **Richer metrics** - Enable `-loglevel debug` for per-segment timing (Defect G)
6. **Error tracking** - Parse HTTP errors, reconnects, segment failures for load test analysis
7. **Accurate timing** - Use FFmpeg's native timestamps (`-loglevel repeat+level+datetime+...`) for sub-millisecond accuracy

**Impact**: All 300+ client tests should see improved metrics accuracy, TUI stability, and comprehensive error tracking for origin stress testing.

---

## 2. Problem Statement

### Current Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                       FFmpeg Process                            │
├─────────────────────────────────────────────────────────────────┤
│  stdin   ←  /dev/null                                          │
│  stdout  ←  -progress pipe:1  ← Progress key=value blocks      │
│  stderr  ←  ALL logs          ← Events + debug + errors        │
└─────────────────────────────────────────────────────────────────┘
                │                              │
                ▼                              ▼
       ┌────────────────┐            ┌────────────────┐
       │ progressPipeline            │ stderrPipeline │
       │ (ProgressParser)│            │ (HLSEventParser)
       └────────────────┘            └────────────────┘
```

### Problems

| Problem | Impact | Current Workaround |
|---------|--------|-------------------|
| Progress on stdout, events on stderr | Two different code paths | Working, but inflexible |
| `-loglevel debug` mixed with progress | Cannot use debug for segment timing | Must use `-loglevel verbose` |
| Debug JSON in TUI | TUI renders raw escape chars (Defect E) | None |
| No per-segment download times | Can only infer latency (Defect G) | Inferred from progress updates |
| `pipe:1` (stdout) used for progress | Progress data goes to stdout | Works, but unusual |

### Why This Matters

1. **Defect E**: Raw debug logs in TUI make it unusable at `-loglevel debug`
2. **Defect G**: Without `-loglevel debug`, we cannot extract per-segment timing
3. **Scalability**: At 300+ clients, cleaner separation reduces parsing overhead

---

## 3. Solution Architecture

### Proposed Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           FFmpeg Process                                │
├─────────────────────────────────────────────────────────────────────────┤
│  stdin   ←  /dev/null                                                   │
│  stdout  →  (unused, -f null -)                                         │
│  stderr  →  stderrPipeline   → HLS events, HTTP errors, debug logs      │
│  -progress unix:///tmp/hls_N.sock → progressPipeline → key=value only   │
└─────────────────────────────────────────────────────────────────────────┘
                                           │                    │
                                           ▼                    ▼
                                  ┌────────────────┐   ┌────────────────┐
                                  │ ProgressParser │   │ HLSEventParser │
                                  │ (clean k=v)    │   │ + DebugParser  │
                                  └────────────────┘   └────────────────┘
                                           │                    │
                                           └──────────┬─────────┘
                                                      ▼
                                              ┌──────────────┐
                                              │ ClientStats  │
                                              └──────────────┘
```

### Key Changes

| Component | Current | Proposed |
|-----------|---------|----------|
| Progress source | `pipe:1` (stdout) | Unix socket `/tmp/hls_swarm_N.sock` |
| Stdout | Progress data | Unused (empty) |
| Stderr | Events only | Events + debug logs |
| Log level | `verbose` | Configurable (`verbose` or `debug`) |
| Segment timing | Inferred | Explicit from debug logs |

---

## 4. Detailed Design

### 4.1 Socket Lifecycle

```go
// Per-client socket lifecycle:
//
// 1. Supervisor.runOnce() creates socket before starting FFmpeg
// 2. Goroutine listens for connection (FFmpeg connects immediately)
// 3. Progress data flows through socket (clean key=value lines)
// 4. When FFmpeg exits, socket connection closes
// 5. Supervisor cleans up socket file
//
// Timeline:
//   t=0ms   Create Unix socket /tmp/hls_swarm_42.sock
//   t=1ms   Start FFmpeg with -progress unix:///tmp/hls_swarm_42.sock
//   t=2ms   FFmpeg connects to socket
//   t=3ms   First progress block received
//   ...
//   t=N     FFmpeg exits
//   t=N+1   Socket connection closes (EOF)
//   t=N+2   Remove socket file
```

### 4.2 FFmpeg Command Changes

**Current command** (`internal/process/ffmpeg.go` line 140-144):
```go
if r.config.StatsEnabled {
    args = append(args, "-progress", "pipe:1")
    args = append(args, "-stats_period", "1")
}
```

**Proposed command**:
```go
if r.config.StatsEnabled {
    if r.config.ProgressSocket != "" {
        args = append(args, "-progress", "unix://"+r.config.ProgressSocket)
    } else {
        args = append(args, "-progress", "pipe:1") // fallback
    }
    args = append(args, "-stats_period", "1")
}
```

### 4.3 Per-Client User-Agent for Debugging

**Purpose**: Enable correlation between client-side metrics, origin logs, and packet captures.

**Format**:
```
go-ffmpeg-hls-swarm/1.0/client-{clientID}
```

**Example User-Agent strings**:
```
go-ffmpeg-hls-swarm/1.0/client-0
go-ffmpeg-hls-swarm/1.0/client-42
go-ffmpeg-hls-swarm/1.0/client-299
```

**Implementation** (`internal/process/ffmpeg.go`):
```go
// BuildCommand now receives clientID to construct unique User-Agent
func (r *FFmpegRunner) BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error) {
    // Generate per-client User-Agent
    userAgent := fmt.Sprintf("%s/client-%d", r.config.UserAgent, clientID)

    args := r.buildArgsWithUserAgent(userAgent)
    // ...
}

func (r *FFmpegRunner) buildArgsWithUserAgent(userAgent string) []string {
    // ...
    args = append(args, "-user_agent", userAgent)
    // ...
}
```

**Benefits**:

| Use Case | How User-Agent Helps |
|----------|---------------------|
| **tcpdump analysis** | Filter: `tcpdump -A | grep "client-42"` |
| **Nginx access logs** | Identify slow clients: `grep "client-42" access.log` |
| **Origin debugging** | Correlate 5xx errors to specific clients |
| **Load balancer logs** | Track client distribution across backends |
| **CDN debugging** | Identify cache hit/miss patterns per client |

**Nginx Log Format** (to capture User-Agent):
```nginx
log_format detailed '$remote_addr - $remote_user [$time_local] '
                    '"$request" $status $body_bytes_sent '
                    '"$http_referer" "$http_user_agent" '
                    '$request_time $upstream_response_time';
```

**Example origin log entry**:
```
10.177.0.1 - - [22/Jan/2026:12:34:56 +0000] "GET /seg03440.ts HTTP/1.1" 200 51234 "-" "go-ffmpeg-hls-swarm/1.0/client-42" 0.012 0.011
```

**tcpdump correlation**:
```bash
# Capture traffic for specific client
tcpdump -i hlstap0 -A | grep -A5 "client-42"

# Or filter in Wireshark
http.user_agent contains "client-42"
```

### 4.3 Socket Reader Implementation

```go
// internal/parser/socket_reader.go

package parser

import (
    "bufio"
    "net"
    "os"
    "sync/atomic"
    "time"
)

// SocketReader reads progress data from a Unix domain socket.
// Replaces stdout pipe for progress when socket mode is enabled.
type SocketReader struct {
    socketPath string
    listener   net.Listener
    conn       net.Conn

    // Pipeline integration
    pipeline *Pipeline

    // State
    connected atomic.Bool
    closed    atomic.Bool

    // Stats
    bytesRead    atomic.Int64
    linesRead    atomic.Int64
    connectTime  time.Time
    disconnectTime time.Time
}

// NewSocketReader creates a Unix socket for progress data.
// The socket is created immediately; FFmpeg will connect to it.
func NewSocketReader(socketPath string, pipeline *Pipeline) (*SocketReader, error) {
    // Remove any stale socket file
    os.Remove(socketPath)

    listener, err := net.Listen("unix", socketPath)
    if err != nil {
        return nil, fmt.Errorf("create unix socket: %w", err)
    }

    return &SocketReader{
        socketPath: socketPath,
        listener:   listener,
        pipeline:   pipeline,
    }, nil
}

// Run accepts a connection and reads progress data.
// Blocks until the connection closes or Close() is called.
// MUST run in a goroutine.
func (r *SocketReader) Run() {
    defer r.cleanup()

    // Accept connection (FFmpeg connects immediately after starting)
    conn, err := r.listener.Accept()
    if err != nil {
        if !r.closed.Load() {
            // Log error only if not intentionally closed
        }
        return
    }

    r.conn = conn
    r.connected.Store(true)
    r.connectTime = time.Now()

    // Read lines and feed to pipeline
    scanner := bufio.NewScanner(conn)
    buf := make([]byte, 64*1024)
    scanner.Buffer(buf, 1024*1024)

    for scanner.Scan() {
        line := scanner.Text()
        r.linesRead.Add(1)
        r.bytesRead.Add(int64(len(line) + 1))

        // Feed to pipeline (non-blocking if full)
        r.pipeline.FeedLine(line)
    }

    r.disconnectTime = time.Now()
    r.connected.Store(false)
}

// Close stops the socket reader and cleans up resources.
func (r *SocketReader) Close() error {
    r.closed.Store(true)

    if r.conn != nil {
        r.conn.Close()
    }
    if r.listener != nil {
        r.listener.Close()
    }

    return r.cleanup()
}

// cleanup removes the socket file.
func (r *SocketReader) cleanup() error {
    return os.Remove(r.socketPath)
}

// Stats returns socket statistics.
func (r *SocketReader) Stats() (bytesRead, linesRead int64, connected bool) {
    return r.bytesRead.Load(), r.linesRead.Load(), r.connected.Load()
}

// SocketPath returns the socket file path.
func (r *SocketReader) SocketPath() string {
    return r.socketPath
}
```

### 4.4 Modified Supervisor

```go
// internal/supervisor/supervisor.go changes

// New fields in Supervisor struct:
type Supervisor struct {
    // ... existing fields ...

    // Socket-based progress (new)
    useProgressSocket  bool
    progressSocketPath string
    progressSocket     *parser.SocketReader
}

// New field in Config:
type Config struct {
    // ... existing fields ...

    // UseProgressSocket enables Unix socket for progress instead of stdout
    UseProgressSocket bool
}

// Modified runOnce() - key changes:
func (s *Supervisor) runOnce(ctx context.Context) (exitCode int, uptime time.Duration, err error) {
    // ... existing setup ...

    // CHANGE: Create progress socket instead of stdout pipe
    var progressSocket *parser.SocketReader
    if s.statsEnabled && s.useProgressSocket {
        s.progressSocketPath = filepath.Join(os.TempDir(),
            fmt.Sprintf("hls_swarm_%d.sock", s.clientID))

        s.progressPipeline = parser.NewPipeline(
            s.clientID, "progress",
            s.statsBufferSize, s.statsDropThreshold,
        )

        progressSocket, err = parser.NewSocketReader(
            s.progressSocketPath,
            s.progressPipeline,
        )
        if err != nil {
            return 1, 0, fmt.Errorf("create progress socket: %w", err)
        }
        s.progressSocket = progressSocket

        // Start socket reader goroutine
        go progressSocket.Run()
    }

    // CHANGE: Only create stderr pipe (stdout not needed)
    var stderr io.ReadCloser
    if s.statsEnabled {
        stderr, err = cmd.StderrPipe()
        if err != nil {
            if progressSocket != nil {
                progressSocket.Close()
            }
            return 1, 0, fmt.Errorf("stderr pipe: %w", err)
        }
    }

    // ... rest of existing code ...

    // CHANGE: Cleanup socket on exit
    defer func() {
        if progressSocket != nil {
            progressSocket.Close()
        }
    }()
}
```

### 4.5 FFmpegConfig Changes

```go
// internal/process/ffmpeg.go

type FFmpegConfig struct {
    // ... existing fields ...

    // ProgressSocket is the path to Unix socket for progress output.
    // If set, -progress unix://path is used instead of pipe:1.
    ProgressSocket string

    // DebugLogging enables -loglevel debug for detailed segment timing.
    // Only useful when ProgressSocket is set (otherwise debug logs
    // would mix with progress on stderr).
    DebugLogging bool
}

// buildArgs() changes:
func (r *FFmpegRunner) buildArgs() []string {
    // ... existing code ...

    // CHANGE: Use socket for progress if configured
    if r.config.StatsEnabled {
        if r.config.ProgressSocket != "" {
            args = append(args, "-progress", "unix://"+r.config.ProgressSocket)
        } else {
            args = append(args, "-progress", "pipe:1")
        }
        args = append(args, "-stats_period", "1")
    }

    // CHANGE: Support debug logging when socket mode enabled
    if r.config.DebugLogging && r.config.ProgressSocket != "" {
        logLevel = "debug"
    }

    // ... rest of existing code ...
}
```

### 4.4 Unified LineSource Interface

**Problem**: Branching logic (socket vs pipe) is a common source of missed cleanup steps.

**Solution**: Single interface for all line sources, ensuring uniform lifecycle management:

```go
// LineSource abstracts the source of lines for a Pipeline.
// Both PipeReader (stdout) and SocketReader (Unix socket) implement this.
//
// Lifecycle (MUST be followed by Supervisor):
//   1. source := NewXxxReader(...)
//   2. go source.Run()        // Start reading in goroutine
//   3. defer source.Close()   // Cleanup on exit
//   4. <-source.Ready()       // Wait for source to be accepting/reading
//   5. // ... start FFmpeg ...
//
// The source is responsible for calling pipeline.CloseChannel() on exit.
type LineSource interface {
    // Run starts reading lines and feeding them to the pipeline.
    // MUST call pipeline.CloseChannel() on exit (via defer).
    // Blocks until source is exhausted or closed.
    Run()

    // Ready returns a channel that is closed when the source is ready.
    // For PipeReader: closed immediately (pipe is always ready).
    // For SocketReader: closed when Accept() is about to block.
    Ready() <-chan struct{}

    // Close stops the source and releases resources.
    // Safe to call multiple times (idempotent).
    Close() error

    // Stats returns (bytesRead, linesRead, healthy).
    // healthy = true if source is working normally.
    Stats() (bytesRead int64, linesRead int64, healthy bool)
}
```

**Benefits**:

| Benefit | Without Interface | With Interface |
|---------|-------------------|----------------|
| Cleanup consistency | Easy to miss in one branch | Impossible to miss |
| Code readability | if/else throughout | Single code path |
| Testing | Mock both branches | Mock one interface |
| Extensibility | Add more if/else | Add new type |

**Supervisor Usage** (uniform for both modes):

```go
func (s *Supervisor) runOnce(ctx context.Context) (...) {
    // Create source (either pipe or socket)
    var source parser.LineSource
    if s.useProgressSocket {
        source, err = parser.NewSocketReader(socketPath, pipeline, s.logger)
        if err != nil {
            s.logger.Warn("socket_failed_using_pipe", "error", err)
            source = parser.NewPipeReader(stdout, pipeline)
        }
    } else {
        source = parser.NewPipeReader(stdout, pipeline)
    }

    // UNIFORM LIFECYCLE (identical for both modes)
    defer source.Close()           // Cleanup on any exit
    go source.Run()                // Start reading
    <-source.Ready()               // Wait for ready

    // Now safe to start FFmpeg
    if err := cmd.Start(); err != nil {
        return ...
    }
    // ...
}
```

---

## 5. Data Flow Diagrams

### 5.1 Current Flow (Mixed Streams)

```
                    ┌──────────────────┐
                    │   FFmpeg Process │
                    └────────┬─────────┘
                             │
              ┌──────────────┴──────────────┐
              │                             │
         ┌────▼────┐                   ┌────▼────┐
         │ STDOUT  │                   │ STDERR  │
         │ (pipe:1)│                   │ (pipe:2)│
         │         │                   │         │
         │ Progress│                   │ HLS evts│
         │ key=val │                   │ Errors  │
         │ blocks  │                   │ Debug*  │
         └────┬────┘                   └────┬────┘
              │                             │
              ▼                             ▼
    ┌──────────────────┐         ┌──────────────────┐
    │ progressPipeline │         │ stderrPipeline   │
    │ ProgressParser   │         │ HLSEventParser   │
    └────────┬─────────┘         └────────┬─────────┘
             │                            │
             └────────────┬───────────────┘
                          ▼
                  ┌──────────────┐
                  │ ClientStats  │
                  └──────────────┘

* Debug logs not currently captured (would corrupt progress)
```

### 5.2 Proposed Flow (Socket Separation)

```
                    ┌──────────────────┐
                    │   FFmpeg Process │
                    └─────────┬────────┘
                              │
       ┌──────────────────────┼──────────────────────┐
       │                      │                      │
  ┌────▼────┐            ┌────▼────┐           ┌────▼────┐
  │ STDOUT  │            │ STDERR  │           │ SOCKET  │
  │ (unused)│            │ (pipe:2)│           │ (unix)  │
  │         │            │         │           │         │
  │  empty  │            │ HLS evts│           │ Progress│
  │         │            │ Errors  │           │ key=val │
  │         │            │ Debug   │           │ clean   │
  │         │            │ JSON    │           │         │
  └─────────┘            └────┬────┘           └────┬────┘
                              │                     │
                              ▼                     ▼
                   ┌───────────────────┐  ┌──────────────────┐
                   │ stderrPipeline    │  │ socketPipeline   │
                   │ HLSEventParser    │  │ ProgressParser   │
                   │ + DebugParser     │  │ (clean parsing)  │
                   └────────┬──────────┘  └────────┬─────────┘
                            │                      │
                            └──────────┬───────────┘
                                       ▼
                               ┌──────────────┐
                               │ ClientStats  │
                               └──────────────┘
```

### 5.3 Socket Connection Timeline

```
Time     Host Process              Unix Socket           FFmpeg Process
─────────────────────────────────────────────────────────────────────────
t=0      Create socket             ◯ Created             (not started)
         net.Listen("unix", path)  listening

t=1      go socketReader.Run()     ◯ Waiting             (not started)
         blocks on Accept()        for connection

t=2      cmd.Start()               ◯ Waiting             Started
         FFmpeg starts             for connection

t=3      Accept() returns          ● Connected           Writing progress
         conn := ...               bidirectional

t=4      Read loop                 ● Connected           Writing progress
         scanner.Scan()            data flows            fps=30...

...      (normal operation)        ● Connected           Running

t=N      FFmpeg exits              ● EOF                 Exit(0)
         Read() returns EOF

t=N+1    Close socket              ✗ Closed              (exited)
         os.Remove(path)
```

---

## 6. Performance Considerations

### 6.1 Socket vs Pipe Performance

| Aspect | Pipe (current) | Unix Socket (proposed) |
|--------|---------------|------------------------|
| Latency | ~1µs | ~2µs |
| Throughput | ~100MB/s | ~80MB/s |
| Syscalls per line | 1 (read) | 2 (read + accept initially) |
| File descriptor count | 2 per client | 3 per client (socket + conn + listener) |
| Cleanup complexity | Automatic | Must remove socket file |

**Conclusion**: Negligible performance difference for progress data (~100 lines/sec per client).

### 6.2 String Parsing Optimization

Current `parseKeyValue` in `progress.go` (lines 177-183):
```go
func parseKeyValue(line string) (key, value string, ok bool) {
    idx := strings.Index(line, "=")
    if idx < 0 {
        return "", "", false
    }
    return line[:idx], line[idx+1:], true
}
```

This is already efficient:
- Single `strings.Index()` call
- No allocations for slice operations
- No regex

**Benchmark** (to be added):
```go
func BenchmarkParseKeyValue(b *testing.B) {
    line := "out_time_us=5933333"
    for i := 0; i < b.N; i++ {
        parseKeyValue(line)
    }
}
```

Expected: **~50ns/op** with zero allocations.

### 6.3 Socket Cleanup Under Load

Risk: At 300 clients with frequent restarts, socket file cleanup could lag.

Mitigation:
1. Use deterministic socket paths: `/tmp/hls_swarm_{clientID}.sock`
2. Remove stale socket before creating new one
3. Cleanup on both normal exit and panic (defer)
4. Add monitoring: log warning if socket file exists when creating

### 6.4 Memory Considerations

| Component | Current Memory | With Sockets |
|-----------|---------------|--------------|
| Per-client buffers | ~64KB (pipe) | ~64KB (socket) |
| Socket file | N/A | 0 bytes (inode only) |
| net.Listener | N/A | ~256 bytes |
| net.Conn | N/A | ~512 bytes |
| **Total per client** | ~64KB | ~65KB |

**Conclusion**: Memory increase is negligible (~1KB per client).

---

## 7. Risk Analysis and Mitigation

### 7.1 Risk Matrix

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Socket creation fails (permissions) | Low | High | Fall back to pipe:1 |
| Socket file not cleaned up | Medium | Medium | Cleanup on startup + defer |
| FFmpeg doesn't support unix:// | Very Low | High | Feature detection, fallback |
| Platform incompatibility (Windows) | High | Medium | Conditional compilation |
| Performance regression | Low | Medium | Benchmarks, A/B testing |
| Race condition on cleanup | Medium | Low | Mutex on socket state |

### 7.2 Detailed Risk Mitigation

#### R1: Socket Creation Failure

**Cause**: `/tmp` not writable, permissions, or socket limit reached.

**Mitigation**:
```go
progressSocket, err := parser.NewSocketReader(socketPath, pipeline)
if err != nil {
    s.logger.Warn("socket_creation_failed",
        "error", err,
        "fallback", "pipe:1",
    )
    // Fall back to current behavior
    s.useProgressSocket = false
    return s.runOnceWithPipe(ctx)
}
```

#### R2: Stale Socket Files

**Cause**: Process crash, SIGKILL, or power failure.

**Mitigation**:
```go
// In NewSocketReader, always clean up first:
func NewSocketReader(socketPath string, pipeline *Pipeline) (*SocketReader, error) {
    // Remove any stale socket from previous run
    if _, err := os.Stat(socketPath); err == nil {
        os.Remove(socketPath)
        log.Debug("removed_stale_socket", "path", socketPath)
    }
    // ... create new socket
}
```

#### R3: Platform Compatibility

**Cause**: Unix sockets not available on Windows.

**Mitigation**:
```go
// internal/parser/socket_reader_unix.go
//go:build !windows

// Full implementation here

// internal/parser/socket_reader_windows.go
//go:build windows

// Stub that always returns error
func NewSocketReader(socketPath string, pipeline *Pipeline) (*SocketReader, error) {
    return nil, errors.New("unix sockets not supported on Windows")
}
```

#### R4: FFmpeg Version Compatibility

**Cause**: Very old FFmpeg might not support `unix://` progress URLs.

**⚠️ Note**: Simple probing with `-version` is **unreliable** because FFmpeg may not actually attempt to open the progress URL when just printing version info.

**Correct Detection** (real minimal run):
```go
// probeFFmpegUnixSocketSupport performs a real minimal run to verify unix:// support.
func probeFFmpegUnixSocketSupport() bool {
    // Create temporary socket
    sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("ffmpeg_probe_%d.sock", os.Getpid()))
    defer os.Remove(sockPath)

    listener, err := net.Listen("unix", sockPath)
    if err != nil {
        return false
    }
    defer listener.Close()

    // Accept in background (with timeout)
    connected := make(chan bool, 1)
    go func() {
        listener.SetDeadline(time.Now().Add(5 * time.Second))
        conn, err := listener.Accept()
        if err == nil {
            conn.Close()
            connected <- true
        } else {
            connected <- false
        }
    }()

    // Run minimal FFmpeg command that forces progress writer initialization
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    cmd := exec.CommandContext(ctx, "ffmpeg",
        "-f", "lavfi",
        "-i", "anullsrc=r=44100:cl=mono",  // Synthetic null audio
        "-t", "0.1",                        // 100ms duration
        "-progress", "unix://"+sockPath,
        "-f", "null", "-",
    )
    cmd.Run()  // Ignore exit code

    select {
    case result := <-connected:
        return result
    case <-time.After(5 * time.Second):
        return false
    }
}
```

**Runtime Fallback** (primary mechanism):
```go
// Even with probe, use runtime fallback as the definitive mechanism:
// If socket mode fails to receive a connection within grace period, fall back to pipe.
const socketConnectGrace = 3 * time.Second

func (r *SocketReader) Run() {
    // Set accept deadline
    r.listener.(*net.UnixListener).SetDeadline(time.Now().Add(socketConnectGrace))

    conn, err := r.listener.Accept()
    if err != nil {
        // FFmpeg didn't connect - likely doesn't support unix://
        r.logger.Warn("socket_accept_timeout",
            "path", r.socketPath,
            "grace", socketConnectGrace,
            "fallback", "pipe mode will be used for next restart",
        )
        r.failedToConnect.Store(true)
        return
    }
    // ... continue reading
}
```

#### R5: Socket Path Length Limit (Unix-specific)

**Cause**: `sockaddr_un.sun_path` is limited to ~108 bytes on most systems. Long `TMPDIR` paths (common in containers) can exceed this.

**Example problematic path**:
```
/var/lib/containers/storage/overlay/abc123.../merged/tmp/hls_swarm_42.sock
└───────────────────────────── 90+ bytes ──────────────────────────────────┘
```

**Mitigation**:
```go
const maxUnixSocketPathLen = 104  // Safe limit (108 - some buffer)

func validateSocketPath(path string) error {
    if len(path) > maxUnixSocketPathLen {
        return fmt.Errorf("socket path too long (%d > %d bytes): %s",
            len(path), maxUnixSocketPathLen, path)
    }
    return nil
}

func NewSocketReader(socketPath string, pipeline *Pipeline) (*SocketReader, error) {
    // Validate path length BEFORE attempting to create
    if err := validateSocketPath(socketPath); err != nil {
        return nil, err  // Caller should fall back to pipe mode
    }
    // ...
}
```

**Short deterministic paths**:
```go
// Use PID + clientID for uniqueness, keep path short
socketPath := fmt.Sprintf("/tmp/hls_%d_%d.sock", os.Getpid(), clientID)
// Example: /tmp/hls_12345_42.sock (23 bytes - safe)
```

#### R6: Pipeline Termination (Goroutine Leak Prevention)

**Cause**: Parser goroutine waits on channel forever if channel is never closed.

**Invariant**: The source of data is responsible for closing the channel.

**Implementation**:
```go
// SocketReader.Run() - ALWAYS closes pipeline channel on exit
func (r *SocketReader) Run() {
    defer r.pipeline.CloseChannel()  // ← CRITICAL: signal parser to stop
    defer r.cleanup()

    conn, err := r.listener.Accept()
    if err != nil {
        return  // CloseChannel called via defer
    }

    // ... read loop ...

    // EOF reached, defers run, parser will drain and exit
}

// For symmetry, pipe mode (Pipeline.RunReader) already closes:
func (p *Pipeline) RunReader(r io.Reader) {
    // ... read loop ...
    close(p.lineChan)  // ← Already exists, but document explicitly
}
```

**Test for goroutine leaks**:
```go
func TestSocketReader_NoGoroutineLeak(t *testing.T) {
    before := runtime.NumGoroutine()

    // Create and run socket reader, then close
    // ...

    // Give time for cleanup
    time.Sleep(100 * time.Millisecond)

    after := runtime.NumGoroutine()
    if after > before {
        t.Errorf("goroutine leak: %d before, %d after", before, after)
    }
}
```

#### R7: Race Condition - FFmpeg Connects Before Listener Ready

**Cause**: If FFmpeg starts before `Accept()` goroutine is running, connection may fail.

**Required Order** (INVARIANT):
```
1. Create socket (net.Listen)
2. Start Accept() goroutine  ← MUST be running before step 3
3. Start FFmpeg process
```

**Implementation with synchronization**:
```go
func (s *Supervisor) runOnce(ctx context.Context) (...) {
    // Step 1: Create socket
    socketReader, err := parser.NewSocketReader(socketPath, pipeline)
    if err != nil {
        return s.runOnceWithPipe(ctx)  // Fallback
    }

    // Step 2: Start accept goroutine and WAIT for it to be ready
    acceptReady := make(chan struct{})
    go func() {
        close(acceptReady)  // Signal we're about to accept
        socketReader.Run()  // Blocks on Accept()
    }()
    <-acceptReady  // Wait for goroutine to start

    // Small sleep to ensure Accept() syscall is entered
    // (acceptReady only guarantees goroutine started, not syscall entered)
    time.Sleep(1 * time.Millisecond)

    // Step 3: NOW safe to start FFmpeg
    cmd, err := s.builder.BuildCommand(ctx, s.clientID)
    // ...
}
```

**Alternative - more robust**:
```go
// SocketReader provides a Ready() channel
func (r *SocketReader) Run() {
    close(r.readyChan)  // Signal listener is accepting
    conn, err := r.listener.Accept()
    // ...
}

// Supervisor waits on Ready() before starting FFmpeg
<-socketReader.Ready()
cmd.Start()
```

---

## 8. Testing Strategy

### 8.1 Unit Tests

| Test File | Test Function | Purpose |
|-----------|---------------|---------|
| `socket_reader_test.go` | `TestSocketReader_Basic` | Create, connect, read, close |
| `socket_reader_test.go` | `TestSocketReader_FFmpegConnect` | Simulate FFmpeg connecting |
| `socket_reader_test.go` | `TestSocketReader_Cleanup` | Verify socket file removed |
| `socket_reader_test.go` | `TestSocketReader_StaleSocket` | Remove stale before create |
| `socket_reader_test.go` | `TestSocketReader_Concurrent` | Multiple clients |
| `supervisor_test.go` | `TestSupervisor_SocketMode` | Full integration |
| `supervisor_test.go` | `TestSupervisor_SocketFallback` | Fallback to pipe |

### 8.2 Integration Tests

```go
func TestIntegration_SocketProgress(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    // Create supervisor with socket mode
    sup := supervisor.New(supervisor.Config{
        // ...
        UseProgressSocket: true,
        StatsEnabled:      true,
    })

    // Run for 5 seconds with real FFmpeg
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    var progressCount int64
    sup.SetProgressCallback(func(p *parser.ProgressUpdate) {
        atomic.AddInt64(&progressCount, 1)
    })

    go sup.Run(ctx)
    <-ctx.Done()

    // Verify progress was received
    if progressCount == 0 {
        t.Error("no progress updates received via socket")
    }

    // Verify socket cleaned up
    socketPath := sup.ProgressSocketPath()
    if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
        t.Error("socket file not cleaned up")
    }
}
```

### 8.3 Benchmark Tests

```go
// internal/parser/socket_reader_test.go

func BenchmarkSocketReader_Throughput(b *testing.B) {
    // Measure lines/second through socket
}

func BenchmarkSocketReader_Latency(b *testing.B) {
    // Measure time from write to callback
}

func BenchmarkSocketReader_vs_Pipe(b *testing.B) {
    // Compare socket vs pipe performance
}
```

### 8.4 Test Data Updates

| File | Purpose | Changes |
|------|---------|---------|
| `testdata/ffmpeg_progress.txt` | Progress block samples | No change |
| `testdata/ffmpeg_debug_output.txt` | Debug log samples | Already created |
| `testdata/ffmpeg_socket_progress.txt` | Socket-specific format | Create new (if different) |

### 8.5 Load Test Validation

```bash
# Before: Current implementation
make test-100-clients
# Record: bytes, latency, drop rate

# After: Socket implementation
USE_PROGRESS_SOCKET=1 make test-100-clients
# Compare: bytes, latency, drop rate

# Verify no regression
# - Total bytes should match
# - Drop rate should be equal or lower
# - Latency P99 should be within 10%
```

---

## 9. Rollback Plan

### 9.1 Feature Flag

The socket feature is controlled by a flag, enabling instant rollback:

```go
// Config flag
type Config struct {
    UseProgressSocket bool // Default: false
}

// CLI flag
flag.BoolVar(&cfg.UseProgressSocket, "progress-socket", false,
    "Use Unix socket for FFmpeg progress (experimental)")
```

### 9.2 Rollback Steps

1. **Immediate**: Set `UseProgressSocket: false` in config
2. **Restart**: No code changes needed
3. **Verify**: Check logs for "progress_source=pipe" vs "progress_source=socket"

### 9.3 Rollback Triggers

- Drop rate increases by >5% compared to pipe mode
- Latency P99 increases by >20%
- Socket creation failures on >10% of clients
- Any data corruption (missing progress updates)

---

## 10. Success Criteria

### 10.1 Functional Criteria

| Criterion | Measurement | Target |
|-----------|-------------|--------|
| Progress parsing works | Unit tests pass | 100% |
| Socket cleanup | No stale files after test | 0 files |
| Fallback works | Error injection test | Graceful fallback |
| TUI renders cleanly | Visual inspection | No raw escape chars |

### 10.2 Performance Criteria

| Criterion | Measurement | Target |
|-----------|-------------|--------|
| Drop rate | 300-client test | ≤ current rate |
| Latency overhead | Benchmark | < 5µs added |
| Memory overhead | `go tool pprof` | < 2KB/client |
| CPU overhead | `go tool pprof` | < 1% increase |

### 10.3 Quality Criteria

| Criterion | Measurement | Target |
|-----------|-------------|--------|
| Test coverage | `go test -cover` | > 80% for new code |
| Race conditions | `go test -race` | 0 races |
| Linter errors | `golangci-lint` | 0 errors |
| Documentation | Code comments | All public APIs |

---

## 11. Debug Parser: High-Value Metrics

With the socket separation in place, we can safely enable `-loglevel debug` and extract these gold-mine metrics from stderr:

### 11.1 Segment Fetch Timing Metrics

**Goal**: Measure network performance for segment downloads.

#### ⚠️ Important Limitations

| Issue | Impact | Mitigation |
|-------|--------|------------|
| **Keep-alive connections** | No TCP connect event per segment | Use segment wall time instead |
| **Parallel/prefetch fetching** | "Most recent pending" correlation unreliable | Track by URL, discard ambiguous |
| **HTTP overhead** | TLS, request send, server TTFB not in TCP connect | Label metric as `tcp_connect`, not "TTFB" |
| **Reused connections** | Most segments won't have TCP events | Segment wall time is primary metric |

#### Metric Hierarchy (Most Reliable → Least Reliable)

| Metric | Reliability | What It Measures | When Available |
|--------|-------------|------------------|----------------|
| **Segment Wall Time** | ✅ HIGH | `Opening seg.ts` → `EOF/next segment` | Always (if logs present) |
| **TCP Connect Latency** | ⚠️ MEDIUM | `Starting connection` → `Successfully connected` | Only new connections |
| **Request-to-Connect** | ❌ LOW | `HLS request` → `TCP connected` | Only new connections, unreliable correlation |

#### Primary Metric: Segment Download Wall Time

This is the **most robust metric** because it doesn't depend on TCP connect events.

**Event Pattern** (reliable):
```
t=0ms   [hls @ ...] Opening 'http://.../seg00123.ts' for reading
t=48ms  [hls @ ...] Opening 'http://.../seg00124.ts' for reading  ← seg00123 done
        ↑
        Segment Wall Time = 48ms
```

**Alternative end markers** (in order of preference):
1. Next segment `Opening` for same stream type
2. `[AVIOContext @ ...] Statistics:` line (bytes read)
3. Progress update with changed `total_size`

**State Machine**:
```go
type SegmentDownloadState struct {
    URL          string
    OpenTime     time.Time
    EndTime      time.Time     // Set when next segment opens or EOF detected
    BytesRead    int64         // From AVIOContext Statistics if available

    // Derived
    WallTime     time.Duration // EndTime - OpenTime
}

// Parser maintains:
type SegmentTracker struct {
    currentSegment *SegmentDownloadState  // Most recently opened segment
    completed      []*SegmentDownloadState // Ring buffer for latency calculation
}

func (t *SegmentTracker) OnSegmentOpen(url string, timestamp time.Time) {
    // Complete previous segment
    if t.currentSegment != nil {
        t.currentSegment.EndTime = timestamp
        t.currentSegment.WallTime = timestamp.Sub(t.currentSegment.OpenTime)
        t.completed = append(t.completed, t.currentSegment)
    }
    // Start tracking new segment
    t.currentSegment = &SegmentDownloadState{
        URL:      url,
        OpenTime: timestamp,
    }
}
```

#### Secondary Metric: TCP Connect Latency (When Available)

**Only available for new connections** (not reused keep-alive).

**Event Pattern**:
```
t=0ms   [tcp @ 0x55...] Starting connection attempt to 10.177.0.10 port 17080
t=2ms   [tcp @ 0x55...] Successfully connected to 10.177.0.10 port 17080
        ↑
        TCP Connect Latency = 2ms
```

**⚠️ NOT "TTFB"**: This measures only the TCP handshake, NOT:
- DNS resolution (usually cached)
- TLS handshake (if HTTPS)
- HTTP request/response time
- Server processing time

**State Machine**:
```go
type TCPConnectTracker struct {
    pendingConnects map[string]time.Time  // "IP:port" -> connect start time

    // Stats
    connectCount    int64
    totalLatency    time.Duration
    maxLatency      time.Duration
}

func (t *TCPConnectTracker) OnConnectStart(ip string, port int, timestamp time.Time) {
    key := fmt.Sprintf("%s:%d", ip, port)
    t.pendingConnects[key] = timestamp
}

func (t *TCPConnectTracker) OnConnectSuccess(ip string, port int, timestamp time.Time) {
    key := fmt.Sprintf("%s:%d", ip, port)
    if start, ok := t.pendingConnects[key]; ok {
        latency := timestamp.Sub(start)
        t.connectCount++
        t.totalLatency += latency
        if latency > t.maxLatency {
            t.maxLatency = latency
        }
        delete(t.pendingConnects, key)
    }
}
```

#### Metrics Exposed

**Primary (always available)**:
- `hls_swarm_segment_download_seconds` (histogram) - Wall time per segment download
- `hls_swarm_segment_download_bytes` (histogram) - Bytes per segment (if available)

**Secondary (only when new TCP connections occur)**:
- `hls_swarm_tcp_connect_seconds` (histogram) - Pure TCP handshake time
- `hls_swarm_tcp_connect_total` (counter) - Number of new TCP connections

**Deprecated/Removed** (unreliable):
- ~~`hls_swarm_segment_ttfb_seconds`~~ - Renamed to avoid implying true TTFB

#### TUI Display

```
Segment Download (wall time)
────────────────────────────────────────
P50: 45ms    P95: 120ms    P99: 250ms

TCP Connects: 15 new (avg 2.1ms)  ← Only shown if connections observed
```

### 11.2 Playlist Refresh Jitter

**Goal**: Detect when manifest refresh deviates from `EXT-X-TARGETDURATION`, which often precedes stream failures.

**Event Pattern**:
```
t=0s     [hls @ ...] Opening 'http://.../stream.m3u8' for reading
t=2.1s   [hls @ ...] Opening 'http://.../stream.m3u8' for reading  ← Expected at 2s
t=4.0s   [hls @ ...] Opening 'http://.../stream.m3u8' for reading  ← Expected at 4s
t=6.5s   [hls @ ...] Opening 'http://.../stream.m3u8' for reading  ← Expected at 6s, LATE!
         ↑
         Jitter = 0.5s (deviation from expected interval)
```

**State Machine**:
```go
type PlaylistRefreshState struct {
    LastRefreshTime    time.Time
    ExpectedInterval   time.Duration  // From EXT-X-TARGETDURATION (default 2s)

    RefreshCount       int64
    TotalJitter        time.Duration  // Sum of |actual - expected|
    MaxJitter          time.Duration
    LateRefreshes      int64          // Count where actual > expected * 1.5
}

// Jitter calculation:
func (s *PlaylistRefreshState) RecordRefresh(t time.Time) {
    if !s.LastRefreshTime.IsZero() {
        actual := t.Sub(s.LastRefreshTime)
        jitter := actual - s.ExpectedInterval
        if jitter < 0 {
            jitter = -jitter  // Absolute value
        }
        s.TotalJitter += jitter
        if jitter > s.MaxJitter {
            s.MaxJitter = jitter
        }
        if actual > s.ExpectedInterval * 3 / 2 {  // >150% of expected
            s.LateRefreshes++
        }
    }
    s.LastRefreshTime = t
    s.RefreshCount++
}
```

**Metrics Exposed**:
- `hls_swarm_playlist_jitter_seconds` (gauge) - Current jitter from expected
- `hls_swarm_playlist_late_refreshes_total` (counter) - Refreshes >150% of target
- `hls_swarm_playlist_refresh_interval_seconds` (histogram) - Actual intervals
- **TUI Alert**: "⚠️ Playlist Lag" when jitter > targetDuration

### 11.3 TCP Connection Health Ratio

**Goal**: Track connection success/failure ratio as an early indicator of origin stress.

**Event Patterns**:
```
[tcp @ 0x...] Successfully connected to 10.177.0.10 port 17080  ← Success
[tcp @ 0x...] Connection refused                                  ← Failure
[tcp @ 0x...] Connection timed out                                ← Failure
[tcp @ 0x...] Failed to connect                                   ← Failure
```

**State Machine**:
```go
type TCPHealthState struct {
    SuccessCount    int64
    FailureCount    int64
    TimeoutCount    int64
    RefusedCount    int64

    // Rolling window for real-time health
    recentResults   []bool  // Ring buffer of last N results
    windowSize      int     // e.g., 100
}

func (s *TCPHealthState) HealthRatio() float64 {
    total := s.SuccessCount + s.FailureCount
    if total == 0 {
        return 1.0
    }
    return float64(s.SuccessCount) / float64(total)
}

func (s *TCPHealthState) RecentHealthRatio() float64 {
    if len(s.recentResults) == 0 {
        return 1.0
    }
    successes := 0
    for _, ok := range s.recentResults {
        if ok {
            successes++
        }
    }
    return float64(successes) / float64(len(s.recentResults))
}
```

**Metrics Exposed**:
- `hls_swarm_tcp_connects_total` (counter, label: `result={success,refused,timeout,error}`)
- `hls_swarm_tcp_health_ratio` (gauge) - Success/total ratio (0.0-1.0)
- **TUI Color Coding**:
  - Green: >99% success
  - Yellow: 95-99% success
  - Red: <95% success

### 11.4 Combined Debug Event Types

Based on FFmpeg source code analysis (`libavformat/hls.c`, `http.c`, `network.c`), we parse the following event categories:

```go
// internal/parser/debug_events.go

type DebugEventType int

const (
    // === SEGMENT FETCH EVENTS (Primary Timing) ===
    DebugEventHLSRequest    DebugEventType = iota  // [hls @ ...] HLS request for url
    DebugEventHTTPOpen                              // [http @ ...] Opening '...' for reading
    DebugEventTCPStart                              // [tcp @ ...] Starting connection attempt
    DebugEventTCPConnected                          // [tcp @ ...] Successfully connected
    DebugEventTCPFailed                             // [tcp @ ...] Connection failed/refused/timeout

    // === PLAYLIST EVENTS (Jitter Tracking) ===
    DebugEventPlaylistOpen                          // [hls @ ...] Opening '...m3u8' for reading
    DebugEventSequenceChange                        // [hls @ ...] Media sequence change

    // === ERROR EVENTS (Critical for Load Testing) ===
    DebugEventHTTPError                             // [http @ ...] HTTP error 4xx/5xx
    DebugEventReconnect                             // Will reconnect at... in N second(s)
    DebugEventSegmentFailed                         // [hls @ ...] Failed to open segment
    DebugEventSegmentSkipped                        // [hls @ ...] Segment failed too many times, skipping
    DebugEventPlaylistFailed                        // [hls @ ...] Failed to reload playlist
    DebugEventSegmentsExpired                       // [hls @ ...] skipping N segments ahead, expired

    // === BANDWIDTH EVENTS ===
    DebugEventBandwidth                             // BANDWIDTH=... from manifest
)

type DebugEvent struct {
    Type      DebugEventType
    Timestamp time.Time

    // For HLS/HTTP/TCP events
    URL       string
    IP        string
    Port      int

    // For sequence events
    OldSeq    int
    NewSeq    int

    // For TCP failures
    FailReason string  // "refused", "timeout", "error"

    // For bandwidth
    Bandwidth  int64   // bits per second

    // For error events
    HTTPCode   int     // HTTP status code (4xx, 5xx)
    ErrorMsg   string  // Error message text
    SkipCount  int     // Number of segments skipped (for SegmentsExpired)
    PlaylistID int     // Playlist index
    SegmentID  int64   // Segment sequence number
}
```

### 11.4.1 Event Source Reference (FFmpeg Source Code)

| Event Type | FFmpeg Source | Line | Log Level | Pattern |
|------------|---------------|------|-----------|---------|
| `HLSRequest` | `hls.c` | L1392 | VERBOSE | `HLS request for url '%s'` |
| `HTTPOpen` | `http.c` | L563 | INFO | `Opening '%s' for reading` |
| `TCPStart` | `network.c` | L432 | VERBOSE | `Starting connection attempt to %s port %s` |
| `TCPConnected` | `network.c` | L488 | VERBOSE | `Successfully connected to %s port %s` |
| `TCPFailed` | `network.c` | L503, L519 | VERBOSE/ERROR | `Connection attempt to %s port %s failed` |
| `PlaylistOpen` | `hls.c` | (via http) | INFO | `Opening '...m3u8' for reading` |
| `SequenceChange` | `hls.c` | L1086 | DEBUG | `Media sequence change (%d -> %d)` |
| `HTTPError` | `http.c` | L873 | WARNING | `HTTP error %d %s` |
| `Reconnect` | `http.c` | L432, L1805 | WARNING | `Will reconnect at %d in %d second(s)` |
| `SegmentFailed` | `hls.c` | L1677, L1711 | WARNING | `Failed to open segment %d of playlist %d` |
| `SegmentSkipped` | `hls.c` | L1681 | WARNING | `Segment %d of playlist %d failed too many times, skipping` |
| `PlaylistFailed` | `hls.c` | L1594 | WARNING | `Failed to reload playlist %d` |
| `SegmentsExpired` | `hls.c` | L1604 | WARNING | `skipping %d segments ahead, expired from playlists` |

### 11.4.2 Critical Events for Load Testing

When stress-testing an origin server, these events indicate problems:

| Severity | Event | Pattern | Meaning | Action |
|----------|-------|---------|---------|--------|
| 🔴 **Critical** | `HTTPError` (5xx) | `HTTP error 503` | Origin failure | Alert immediately |
| 🔴 **Critical** | `SegmentSkipped` | `failed too many times, skipping` | Data loss | Track skip rate |
| 🔴 **Critical** | `PlaylistFailed` | `Failed to reload playlist` | Live edge lost | Check origin health |
| 🔴 **Critical** | `TCPFailed` (refused) | `Connection refused` | Origin overloaded | Reduce client count |
| ⚠️ **Warning** | `HTTPError` (4xx) | `HTTP error 404` | Missing content | Check origin config |
| ⚠️ **Warning** | `Reconnect` | `Will reconnect at` | Connection dropped | Monitor frequency |
| ⚠️ **Warning** | `SegmentsExpired` | `skipping segments ahead` | Client too slow | Check client capacity |
| ⚠️ **Warning** | `SegmentFailed` | `Failed to open segment` | Transient failure | Normal if recovers |
| ℹ️ **Info** | Segment time > 100ms | (calculated) | Origin slowing | Monitor trend |
| ℹ️ **Info** | New TCP connects | `Starting connection` | Keep-alive broke | Expected occasionally |

### 11.5 Aggregated Debug Metrics in DebugStats

```go
// internal/parser/debug_events.go

type DebugStats struct {
    // === PARSING METRICS ===
    LinesProcessed int64
    TimestampsUsed int64  // Lines using FFmpeg timestamps (more accurate)

    // === MANIFEST ===
    ManifestBandwidth int64  // bits per second

    // === SEGMENT TIMING (PRIMARY) ===
    SegmentCount int64
    SegmentAvgMs float64
    SegmentMinMs float64
    SegmentMaxMs float64

    // === TCP CONNECT TIMING (SECONDARY - only new connections) ===
    TCPConnectCount int64
    TCPConnectAvgMs float64
    TCPConnectMinMs float64
    TCPConnectMaxMs float64

    // === TCP HEALTH RATIO ===
    TCPSuccessCount int64
    TCPFailureCount int64
    TCPTimeoutCount int64
    TCPRefusedCount int64
    TCPHealthRatio  float64  // success / (success + failure)

    // === PLAYLIST JITTER ===
    PlaylistRefreshes   int64
    PlaylistLateCount   int64
    PlaylistAvgJitterMs float64
    PlaylistMaxJitterMs float64

    // === SEQUENCE TRACKING ===
    SequenceSkips int64

    // === ERROR EVENTS (CRITICAL FOR LOAD TESTING) ===
    HTTPErrorCount      int64   // Total HTTP 4xx/5xx errors
    HTTP4xxCount        int64   // Client errors (4xx)
    HTTP5xxCount        int64   // Server errors (5xx)
    ReconnectCount      int64   // Reconnection attempts
    SegmentFailedCount  int64   // Segment open failures
    SegmentSkippedCount int64   // Segments skipped after retries
    PlaylistFailedCount int64   // Playlist reload failures
    SegmentsExpiredSum  int64   // Total segments expired from playlist
    ErrorRate           float64 // (errors / total requests)

    // === SUCCESS COUNTERS (for TUI - should increment rapidly) ===
    HTTPOpenCount       int64  // Total HTTP opens (segments + manifests + other)
    SegmentCount        int64  // Segments downloaded (HLS requests for .ts files)
    PlaylistRefreshes   int64  // Manifests refreshed (opens for .m3u8 files)
    TCPConnectCount     int64  // New TCP connections (keep-alive means this stays low)
}
```

### 11.5.1 Error Rate Calculation

```go
// ErrorRate = (HTTP errors + segment failures) / total HTTP opens
if stats.HTTPOpenCount > 0 {
    totalErrors := stats.HTTPErrorCount + stats.SegmentFailedCount
    stats.ErrorRate = float64(totalErrors) / float64(stats.HTTPOpenCount)
}
```

### 11.5.2 Timestamp Accuracy Indicator

```go
// TimestampsUsed > 0 indicates FFmpeg timestamps are being used
// for timing calculations. This is MORE ACCURATE than wall clock
// because it's immune to Go channel processing delays.

accuracyIndicator := "⚠️ Wall Clock"
if stats.TimestampsUsed > 0 {
    pct := float64(stats.TimestampsUsed) / float64(stats.LinesProcessed) * 100
    accuracyIndicator = fmt.Sprintf("✅ FFmpeg Timestamps (%.1f%%)", pct)
}
```

### 11.6 TUI Dashboard: Debug Metrics Panel

Organized by protocol layer (HLS → HTTP → TCP) matching the FFmpeg source structure in [FFMPEG_HLS_REFERENCE.md §13](#13-ffmpeg-hls-source-code-log-events-reference).

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ Origin Load Test Dashboard          Timing: ✅ FFmpeg Timestamps (98.2%)    │
├─────────────────────────────────────────────────────────────────────────────┤
│ 📺 HLS LAYER (libavformat/hls.c)                                            │
├─────────────────────────────────────────────────────────────────────────────┤
│ Segments                              │ Playlists                           │
│   ✅ Downloaded:  45,892  (+127/s)    │   ✅ Refreshed:   8,234  (+4.2/s)   │
│   ⚠️ Failed:          12  (0.03%)     │   ⚠️ Failed:          0  (0.00%)    │
│   🔴 Skipped:          2  (data loss) │   ⏱️ Jitter:      45ms avg/312ms max│
│   ⏩ Expired:         45  (fell behind)│   ⏰ Late:         12  (0.4%)       │
│                                       │                                     │
│ Segment Wall Time                     │ Sequence                            │
│   Avg: 12ms  Min: 2ms  Max: 892ms     │   Current: 45892   Skips: 3         │
├─────────────────────────────────────────────────────────────────────────────┤
│ 🌐 HTTP LAYER (libavformat/http.c)                                          │
├─────────────────────────────────────────────────────────────────────────────┤
│ Requests                              │ Errors                              │
│   ✅ Successful: 54,103  (+142/s)     │   4xx Client:       5  (0.01%)      │
│   ⚠️ Failed:         23  (0.04%)      │   5xx Server:      18  (0.03%)      │
│   🔄 Reconnects:      8               │   Error Rate:   0.04%               │
│                                       │   Status:       ● Healthy           │
├─────────────────────────────────────────────────────────────────────────────┤
│ 🔌 TCP LAYER (libavformat/network.c)                                        │
├─────────────────────────────────────────────────────────────────────────────┤
│ Connections                           │ Connect Latency                     │
│   ✅ Success:    14,523  (99.2%)      │   Avg:   0.8ms                      │
│   🚫 Refused:        48  (0.3%)       │   Min:   0.2ms                      │
│   ⏱️ Timeout:        73  (0.5%)       │   Max:   45ms                       │
│   Health:    ●●●●●●●●○○  99.2%        │   (Note: Keep-alive = few connects) │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 11.6.1 Layer Explanation

| Layer | Source File | What It Measures | Key Metrics |
|-------|-------------|------------------|-------------|
| **HLS** | `hls.c` | Application-level streaming | Segments, playlists, sequence |
| **HTTP** | `http.c` | Request/response cycle | Success, errors, reconnects |
| **TCP** | `network.c` | Connection establishment | Connect time, success/failure |

### 11.6.2 Understanding the Metrics Flow

```
User Request → HLS Layer → HTTP Layer → TCP Layer → Origin Server
                  ↓            ↓            ↓
              Segment      HTTP 200     TCP Connect
              Request      or 5xx       or Refused
```

**Reading the dashboard top-to-bottom:**
1. **HLS Layer** - Are we getting segments? Is the playlist updating?
2. **HTTP Layer** - Are HTTP requests succeeding? Any 5xx errors?
3. **TCP Layer** - Can we establish connections? Any network issues?

### 11.6.3 Success Counter Expectations

| Counter | Expected Rate | Warning If |
|---------|---------------|------------|
| **Segments Downloaded** | ~0.5/s per client (2s segments) | Stalled or < expected |
| **Playlists Refreshed** | ~0.5/s per client (2s target) | Stalled |
| **HTTP Requests** | Sum of above | Flat |
| **TCP Connects** | Low (keep-alive reuses connections) | High = connection churn |

**Example for 100 clients with 2s segments:**
- Segments: ~50/s expected
- Playlists: ~50/s expected
- TCP Connects: ~100 initially, then near-zero (keep-alive)

### 11.6.4 Rate Calculation

```go
type RateTracker struct {
    lastCount     int64
    lastTime      time.Time
    currentRate   float64  // events per second
}

func (r *RateTracker) Update(newCount int64) float64 {
    now := time.Now()
    elapsed := now.Sub(r.lastTime).Seconds()
    if elapsed > 0 {
        r.currentRate = float64(newCount - r.lastCount) / elapsed
    }
    r.lastCount = newCount
    r.lastTime = now
    return r.currentRate
}

func formatRate(rate float64) string {
    if rate >= 1000 {
        return fmt.Sprintf("+%.1fK/s", rate/1000)
    } else if rate >= 1 {
        return fmt.Sprintf("+%.0f/s", rate)
    } else if rate > 0 {
        return fmt.Sprintf("+%.1f/s", rate)
    }
    return "(stalled)"
}
```

### 11.6.1 Success Counter Calculations

```go
// Success counters from DebugStats
type SuccessCounters struct {
    // From HLS request events
    SegmentsDownloaded int64  // Count of "HLS request for url '...ts'"

    // From playlist open events
    ManifestsRefreshed int64  // Count of "Opening '...m3u8' for reading"

    // From HTTP open events
    HTTPRequests       int64  // Total HTTP opens (segments + manifests + other)

    // From TCP events
    TCPConnects        int64  // New TCP connections established
}

// Rate calculation (per second)
type RateTracker struct {
    lastCount     int64
    lastTime      time.Time
    currentRate   float64  // events per second
}

func (r *RateTracker) Update(newCount int64) float64 {
    now := time.Now()
    elapsed := now.Sub(r.lastTime).Seconds()
    if elapsed > 0 {
        r.currentRate = float64(newCount - r.lastCount) / elapsed
    }
    r.lastCount = newCount
    r.lastTime = now
    return r.currentRate
}
```

### 11.6.2 Visual Indicators for Success Counters

| Counter | Good Sign | Warning Sign | What It Means |
|---------|-----------|--------------|---------------|
| **Segments Downloaded** | Incrementing rapidly | Stalled or slow | Active streaming |
| **Manifests Refreshed** | ~1 per target duration per client | Much less frequent | Playlist refresh working |
| **HTTP Requests** | Sum of above | Flat | Network activity |
| **TCP Connects** | Low (keep-alive working) | High (new connections) | Connection reuse |

**Rate display logic:**
```go
func formatRate(rate float64) string {
    if rate >= 1000 {
        return fmt.Sprintf("+%.1fK/s", rate/1000)
    } else if rate >= 1 {
        return fmt.Sprintf("+%.0f/s", rate)
    } else if rate > 0 {
        return fmt.Sprintf("+%.1f/s", rate)
    }
    return "(stalled)"
}
```

### 11.6.5 Status Indicators by Layer

#### HLS Layer Status
| Indicator | Condition | Meaning |
|-----------|-----------|---------|
| ● **Healthy** | Skipped = 0, Failed < 1% | Streaming normally |
| ● **Degraded** | Skipped = 0, Failed 1-5% | Some segment issues |
| ● **Unhealthy** | Skipped > 0 OR Failed > 5% | Data loss occurring |
| ● **Critical** | Playlist failures > 0 | Live edge lost |

#### HTTP Layer Status
| Indicator | Condition | Meaning |
|-----------|-----------|---------|
| ● **Healthy** | Error rate < 1%, 5xx = 0 | Origin responding well |
| ● **Degraded** | Error rate 1-5% OR 5xx < 10 | Some errors, monitor |
| ● **Unhealthy** | Error rate > 5% OR 5xx > 10 | Origin under stress |
| ● **Critical** | Error rate > 20% | Origin failing |

#### TCP Layer Status
| Indicator | Condition | Meaning |
|-----------|-----------|---------|
| ● **Healthy** | Health ratio > 99% | Network stable |
| ● **Degraded** | Health ratio 95-99% | Some connection issues |
| ● **Unhealthy** | Health ratio < 95% | Network problems |
| ● **Critical** | Refused > 10% | Origin rejecting connections |

### 11.7 Early Warning Indicators

The debug metrics enable proactive alerting:

| Metric | Warning Threshold | Critical Threshold | Indicates |
|--------|-------------------|-------------------|-----------|
| **TCP Health Ratio** | <99% | <95% | Origin overload |
| **Segment Wall Time Avg** | >100ms | >500ms | Origin slowing down |
| **Segment Wall Time Max** | >500ms | >2s | Worst-case latency |
| **Playlist Max Jitter** | >targetDuration | >2×targetDuration | Playlist delivery failing |
| **Late Refresh Rate** | >5% | >20% | Imminent stream failure |
| **Sequence Skips** | Any | >3 consecutive | Client fell behind live edge |
| **HTTP 5xx Errors** | Any | >10 | Server errors |
| **Error Rate** | >1% | >5% | Overall failure rate |
| **Segment Skipped** | Any | >3 | Data loss (retries exhausted) |
| **Playlist Failed** | Any | >1 | Live edge lost |
| **Reconnect Count** | >5/min | >20/min | Connection instability |
| **Segments Expired** | Any | >10 total | Clients can't keep up |

### 11.7.1 Aggregate Health Score

```go
// Calculate an overall health score (0-100%)
func CalculateHealthScore(stats DebugStats) float64 {
    score := 100.0

    // TCP health penalty (max -30%)
    if stats.TCPHealthRatio < 1.0 {
        score -= (1.0 - stats.TCPHealthRatio) * 30
    }

    // Error rate penalty (max -40%)
    if stats.ErrorRate > 0 {
        score -= stats.ErrorRate * 4000 // 1% error = -40%
    }

    // Segment skip penalty (critical, max -30%)
    if stats.SegmentSkippedCount > 0 {
        score -= float64(stats.SegmentSkippedCount) * 10
    }

    // Playlist failure penalty (critical)
    if stats.PlaylistFailedCount > 0 {
        score -= float64(stats.PlaylistFailedCount) * 20
    }

    if score < 0 {
        score = 0
    }
    return score
}
```

---

## 12. Quality of Experience (QoE) Metrics

Beyond network metrics, we can assess actual **playback quality** without a reference video by using heuristic QoE metrics.

### 12.1 Playback Health Score (Buffer State)

**Goal**: Determine if the client is keeping up with real-time playback.

**Formula**:
```
                     out_time_us
Health Score = ─────────────────────────────────
               (current_wall_time - start_time) × 1,000,000
```

**Interpretation**:

| Score | Status | Meaning |
|-------|--------|---------|
| **= 1.0** | ● Perfect | Real-time playback, no buffering |
| **< 1.0** | ● Buffering | Falling behind live edge |
| **> 1.0** | ● Catching up | Downloading faster than realtime (burst) |
| **< 0.9** | ● Stalling | Severe buffering, likely rebuffering |
| **> 1.5** | ● Burst | Network just recovered, catching up |

**Implementation**:
```go
type PlaybackHealth struct {
    startTime    time.Time

    // From progress updates
    currentOutTimeUS int64

    // Calculated
    healthScore      float64
    healthHistory    []float64  // Ring buffer for trending
    stallingEvents   int64      // Count of score < 0.9

    // State tracking
    isCurrentlyStalling bool
    stallStartTime      time.Time
    totalStallDuration  time.Duration
}

func (h *PlaybackHealth) UpdateFromProgress(p *ProgressUpdate) {
    elapsed := time.Since(h.startTime)
    elapsedUS := float64(elapsed.Microseconds())

    if elapsedUS > 0 {
        h.healthScore = float64(p.OutTimeUS) / elapsedUS
    }

    // Track stalling
    if h.healthScore < 0.9 && !h.isCurrentlyStalling {
        h.isCurrentlyStalling = true
        h.stallStartTime = time.Now()
        h.stallingEvents++
    } else if h.healthScore >= 0.95 && h.isCurrentlyStalling {
        h.isCurrentlyStalling = false
        h.totalStallDuration += time.Since(h.stallStartTime)
    }

    // Add to history (for trending)
    h.recordHealthSample(h.healthScore)
}

// Trend returns the health trend over the last N samples.
// Negative = degrading, Positive = improving
func (h *PlaybackHealth) Trend() float64 {
    if len(h.healthHistory) < 10 {
        return 0
    }
    // Linear regression slope
    return calculateSlope(h.healthHistory)
}
```

**Prometheus Metrics**:
```
hls_swarm_playback_health_score     gauge     Current health score (0.0-2.0+)
hls_swarm_playback_stall_events     counter   Total stalling events
hls_swarm_playback_stall_duration   counter   Total time spent stalling (seconds)
```

**TUI Display**:
```
Playback Health
  Current Score:    0.98x  ● Healthy
  Trend:            ↗ +0.02/min (improving)
  Stalling Events:  3
  Total Stall Time: 4.2s (0.7% of runtime)
```

### 12.2 Segment Download Efficiency

**Goal**: Compare actual download rate to manifest-declared BANDWIDTH.

**Formula**:
```
                          Δtotal_size (bytes) × 8
Actual Bitrate (bps) = ─────────────────────────────
                          Δtime (seconds)

                       Actual Bitrate
Efficiency (%) = ───────────────────── × 100
                  Manifest BANDWIDTH
```

**Interpretation**:

| Efficiency | Status | Meaning |
|------------|--------|---------|
| **> 100%** | ● Excellent | Network faster than needed |
| **90-100%** | ● Good | Adequate bandwidth |
| **70-90%** | ● Marginal | May experience buffering |
| **< 70%** | ● Poor | Network congested, will buffer |

**Implementation**:
```go
type DownloadEfficiency struct {
    // From manifest (parsed once at startup)
    manifestBandwidth int64  // bits per second

    // Tracking
    prevTotalSize    int64
    prevTime         time.Time

    // Calculated
    actualBitrate    float64  // bits per second
    efficiency       float64  // percentage

    // History
    efficiencyHistory []float64
}

func (e *DownloadEfficiency) UpdateFromProgress(p *ProgressUpdate) {
    now := time.Now()

    if !e.prevTime.IsZero() && p.TotalSize > e.prevTotalSize {
        deltaBytes := p.TotalSize - e.prevTotalSize
        deltaSec := now.Sub(e.prevTime).Seconds()

        if deltaSec > 0 {
            e.actualBitrate = float64(deltaBytes*8) / deltaSec

            if e.manifestBandwidth > 0 {
                e.efficiency = (e.actualBitrate / float64(e.manifestBandwidth)) * 100
            }
        }
    }

    e.prevTotalSize = p.TotalSize
    e.prevTime = now
    e.recordEfficiencySample(e.efficiency)
}

// SetManifestBandwidth is called once after parsing master playlist.
// bandwidth is in bits per second.
func (e *DownloadEfficiency) SetManifestBandwidth(bandwidth int64) {
    e.manifestBandwidth = bandwidth
}
```

**How to Get Manifest BANDWIDTH**:

**DECISION: Option 1 - Parse from FFmpeg debug output**

When FFmpeg opens an HLS stream with `-loglevel debug`, it logs the variant information:

```
[hls @ 0x...] Opening 'http://origin/stream.m3u8' for reading
[hls @ 0x...] Skip ('#EXT-X-VERSION:3')
[hls @ 0x...] Skip ('#EXT-X-TARGETDURATION:2')
[hls @ 0x...] Stream variant found: BANDWIDTH=2000000, RESOLUTION=1280x720
```

**Implementation** (`internal/parser/debug_events.go`):

```go
var reBandwidth = regexp.MustCompile(`BANDWIDTH=(\d+)`)

func (p *DebugEventParser) parseLine(line string) {
    // Parse manifest bandwidth (typically seen once at startup)
    if strings.Contains(line, "BANDWIDTH=") {
        if matches := reBandwidth.FindStringSubmatch(line); len(matches) >= 2 {
            if bw, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
                p.manifestBandwidth.Store(bw)
                if p.callback != nil {
                    p.callback(&DebugEvent{
                        Type:      EventManifestBandwidth,
                        Bandwidth: bw,
                    })
                }
            }
        }
    }
    // ... rest of parsing
}

// GetManifestBandwidth returns the parsed BANDWIDTH value (bits/sec).
// Returns 0 if not yet parsed.
func (p *DebugEventParser) GetManifestBandwidth() int64 {
    return p.manifestBandwidth.Load()
}
```

**Why Option 1 over Option 2**:
- No additional `ffprobe` call needed
- Works with any HLS stream (even without prior variant selection)
- Bandwidth is logged early in the stream, immediately available
- Single source of truth (FFmpeg's own parsing)

**Prometheus Metrics**:
```
hls_swarm_download_efficiency_percent  gauge     Current efficiency (%)
hls_swarm_actual_bitrate_bps          gauge     Current download rate (bits/sec)
hls_swarm_manifest_bandwidth_bps      gauge     Declared bandwidth (bits/sec)
```

**TUI Display**:
```
Download Efficiency
  Manifest Bandwidth: 2.0 Mbps
  Actual Bitrate:     1.8 Mbps
  Efficiency:         90% ● Good
  Network Headroom:   0.2 Mbps available
```

### 12.3 Continuity Errors (DTS Discontinuities)

**Goal**: Detect timestamp gaps in HLS segments that cause viewer stuttering.

**FFmpeg Stderr Pattern**:
```
[matroska @ 0x...] Non-monotonous DTS in output stream 0:0; previous: 12345, current: 11000
[mpegts @ 0x...] DTS 12345 < 11000 out of order
```

**Why This Matters**:
- Even with perfect bandwidth, DTS discontinuities cause **rebuffering/stuttering**
- Indicates **origin encoder problems** (not network)
- Critical for diagnosing "why are viewers complaining despite good throughput?"

**Implementation**:
```go
// Add to HLSEventParser or DebugEventParser

var (
    // Non-monotonous DTS in output stream
    reDTSError = regexp.MustCompile(`Non-monotonous DTS|DTS .* out of order`)

    // av_interleaved_write_frame(): Broken pipe (less severe)
    reBrokenPipe = regexp.MustCompile(`Broken pipe`)

    // Discarding corrupted packet
    reCorruptPacket = regexp.MustCompile(`Discarding (corrupted|invalid) packet`)
)

type ContinuityState struct {
    dtsErrors       int64
    brokenPipes     int64
    corruptPackets  int64

    // Recent errors (for alerting)
    recentDTSErrors []time.Time
}

func (c *ContinuityState) ParseLine(line string) {
    if reDTSError.MatchString(line) {
        atomic.AddInt64(&c.dtsErrors, 1)
        c.recordRecentError(time.Now())
        return
    }
    if reBrokenPipe.MatchString(line) {
        atomic.AddInt64(&c.brokenPipes, 1)
        return
    }
    if reCorruptPacket.MatchString(line) {
        atomic.AddInt64(&c.corruptPackets, 1)
        return
    }
}

// DTSErrorRate returns errors per minute (recent window).
func (c *ContinuityState) DTSErrorRate() float64 {
    // Count errors in last 60 seconds
    cutoff := time.Now().Add(-60 * time.Second)
    count := 0
    for _, t := range c.recentDTSErrors {
        if t.After(cutoff) {
            count++
        }
    }
    return float64(count) // per minute
}
```

**Prometheus Metrics**:
```
hls_swarm_dts_errors_total        counter   Non-monotonous DTS events
hls_swarm_corrupt_packets_total   counter   Discarded corrupt packets
hls_swarm_continuity_error_rate   gauge     DTS errors per minute (recent)
```

**TUI Display**:
```
Stream Continuity
  DTS Errors:        12 total (0.2/min)
  Corrupt Packets:   0
  Pipe Errors:       2
  Status:            ● Minor Issues

  ⚠️ Note: DTS errors indicate origin encoder problems,
     not network issues. Viewers may experience stuttering.
```

### 12.4 Combined QoE Dashboard Panel

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ Quality of Experience (QoE)                                                 │
├─────────────────────────────────────────────────────────────────────────────┤
│ Playback Health                       │ Download Efficiency                 │
│   Score:      0.98x ● Healthy         │   Manifest:  2.0 Mbps               │
│   Trend:      ↗ improving             │   Actual:    1.8 Mbps               │
│   Stalls:     3 (4.2s total)          │   Efficiency: 90% ● Good            │
│                                       │                                     │
│ Stream Continuity                     │ Overall QoE Score                   │
│   DTS Errors: 12 (0.2/min)            │                                     │
│   Status:     ● Minor Issues          │   ████████░░ 85/100                 │
│                                       │   "Good with minor issues"          │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 12.5 Composite QoE Score

Combine all quality metrics into a single 0-100 score:

```go
type QoEScore struct {
    // Component scores (0-100 each)
    PlaybackHealthScore  int
    DownloadEfficiency   int
    ContinuityScore      int

    // Weighted composite
    OverallScore         int
    Rating               string  // "Excellent", "Good", "Fair", "Poor"
}

func CalculateQoE(health *PlaybackHealth, efficiency *DownloadEfficiency, continuity *ContinuityState) QoEScore {
    qoe := QoEScore{}

    // Playback Health (40% weight)
    // 1.0 = 100, 0.9 = 80, 0.8 = 60, etc.
    qoe.PlaybackHealthScore = int(math.Min(100, health.healthScore * 100))

    // Download Efficiency (30% weight)
    // >100% = 100, 90% = 90, 70% = 70, etc.
    qoe.DownloadEfficiency = int(math.Min(100, efficiency.efficiency))

    // Continuity (30% weight)
    // 0 errors = 100, 1/min = 80, 5/min = 50, 10+/min = 0
    errorRate := continuity.DTSErrorRate()
    qoe.ContinuityScore = int(math.Max(0, 100 - errorRate*10))

    // Weighted average
    qoe.OverallScore = (qoe.PlaybackHealthScore * 40 +
                        qoe.DownloadEfficiency * 30 +
                        qoe.ContinuityScore * 30) / 100

    // Rating
    switch {
    case qoe.OverallScore >= 90:
        qoe.Rating = "Excellent"
    case qoe.OverallScore >= 75:
        qoe.Rating = "Good"
    case qoe.OverallScore >= 50:
        qoe.Rating = "Fair"
    default:
        qoe.Rating = "Poor"
    }

    return qoe
}
```

**Prometheus Metrics**:
```
hls_swarm_qoe_score                gauge     Composite QoE score (0-100)
hls_swarm_qoe_playback_score       gauge     Playback health component (0-100)
hls_swarm_qoe_efficiency_score     gauge     Download efficiency component (0-100)
hls_swarm_qoe_continuity_score     gauge     Stream continuity component (0-100)
```

### 12.6 QoE Aggregation Across Clients

For load testing, aggregate QoE across all clients:

```go
type AggregatedQoE struct {
    // Score distribution
    ClientsExcellent int  // QoE >= 90
    ClientsGood      int  // QoE 75-89
    ClientsFair      int  // QoE 50-74
    ClientsPoor      int  // QoE < 50

    // Averages
    AvgPlaybackHealth   float64
    AvgDownloadEfficiency float64
    AvgQoEScore         float64

    // Worst performers (for debugging)
    LowestQoEClientID   int
    LowestQoEScore      int
}
```

**TUI Summary Line**:
```
QoE Distribution: 250 Excellent | 40 Good | 8 Fair | 2 Poor (out of 300 clients)
```

---

## Appendix A: FFmpeg Progress Output Reference

### A.1 Key=Value Format

```
frame=60
fps=30.00
bitrate=512.0kbits/s
total_size=51324
out_time_us=2000000
out_time_ms=2000000
out_time=00:00:02.000000
dup_frames=0
drop_frames=0
speed=1.00x
progress=continue
```

### A.2 Live HLS Specific

```
total_size=N/A      # Always N/A for live streams
bitrate=N/A         # Often N/A
speed=0.989x        # <1.0 indicates waiting for segments
```

### A.3 Progress Block Termination

- `progress=continue` - More data coming
- `progress=end` - Stream finished

---

## Appendix B: Related FFmpeg Options

```bash
# Socket progress (proposed)
ffmpeg -progress unix:///tmp/hls_swarm_42.sock ...

# TCP progress (alternative)
ffmpeg -progress tcp://127.0.0.1:9999 ...

# File progress (for debugging)
ffmpeg -progress /tmp/progress.log ...

# Stats period (frequency)
ffmpeg -stats_period 1 ...   # Every 1 second
ffmpeg -stats_period 0.5 ... # Every 500ms
```
