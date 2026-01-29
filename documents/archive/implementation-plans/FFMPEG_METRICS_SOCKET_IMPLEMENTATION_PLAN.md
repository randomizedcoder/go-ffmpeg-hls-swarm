# FFmpeg Metrics Socket Implementation Plan

> **Status**: PROPOSED
> **Date**: 2026-01-22
> **Related**: [FFMPEG_METRICS_SOCKET_DESIGN.md](FFMPEG_METRICS_SOCKET_DESIGN.md)

---

## Table of Contents

- [Phase 1: Socket Reader Infrastructure](#phase-1-socket-reader-infrastructure)
- [Phase 2: Pipeline Integration](#phase-2-pipeline-integration)
- [Phase 3: Supervisor Changes](#phase-3-supervisor-changes)
- [Phase 4: FFmpeg Config Updates](#phase-4-ffmpeg-config-updates)
- [Phase 5: CLI Flag & Config Wiring](#phase-5-cli-flag--config-wiring)
- [Phase 6: Debug Parser Enhancement](#phase-6-debug-parser-enhancement)
- [Phase 7: Testing & Benchmarks](#phase-7-testing--benchmarks)
- [Phase 8: Documentation & Cleanup](#phase-8-documentation--cleanup)
- [Risk Mitigation Checklist](#risk-mitigation-checklist)

---

## Phase 1: Socket Reader Infrastructure

### 1.1 Create `internal/parser/socket_reader.go`

**File**: `internal/parser/socket_reader.go` (NEW)

#### Critical Invariants

| # | Invariant | Consequence if Violated |
|---|-----------|------------------------|
| **I1** | Pipeline channel MUST be closed on `Run()` exit | Goroutine leaks, stats never finalize |
| **I2** | Socket path MUST be ≤104 bytes | Silent socket creation failure at scale |
| **I3** | `Ready()` signal MUST be sent before FFmpeg starts | Race: "connection refused" intermittently |
| **I4** | Pipe mode MUST use same termination mechanism | Inconsistent behavior between modes |

#### Implementation

```go
package parser

import (
    "bufio"
    "fmt"
    "net"
    "os"
    "sync/atomic"
    "time"
)

const (
    // maxUnixSocketPathLen is the safe maximum path length for Unix sockets.
    // sockaddr_un.sun_path is typically 108 bytes; we use 104 for safety.
    maxUnixSocketPathLen = 104

    // socketConnectGrace is how long to wait for FFmpeg to connect.
    // If exceeded, assume FFmpeg doesn't support unix:// - caller should retry with pipe.
    socketConnectGrace = 3 * time.Second
)

// SocketReader reads FFmpeg progress output from a Unix domain socket.
type SocketReader struct {
    socketPath      string
    listener        net.Listener
    pipeline        *Pipeline
    logger          *slog.Logger
    readyChan       chan struct{}  // Closed when Accept() is ready
    failedToConnect atomic.Bool    // True if FFmpeg never connected

    // Stats
    bytesRead int64
    linesRead int64
}

// validateSocketPath checks that path is within Unix socket length limits.
func validateSocketPath(path string) error {
    if len(path) > maxUnixSocketPathLen {
        return fmt.Errorf("socket path too long (%d > %d bytes): use shorter TMPDIR",
            len(path), maxUnixSocketPathLen)
    }
    return nil
}

// NewSocketReader creates a Unix socket and returns a reader.
// Returns error if path is too long or socket creation fails.
func NewSocketReader(socketPath string, pipeline *Pipeline, logger *slog.Logger) (*SocketReader, error) {
    // I2: Validate path length BEFORE attempting to create
    if err := validateSocketPath(socketPath); err != nil {
        return nil, err  // Caller falls back to pipe mode
    }

    // Clean up stale socket from previous run (crash recovery)
    if _, err := os.Stat(socketPath); err == nil {
        os.Remove(socketPath)
    }

    listener, err := net.Listen("unix", socketPath)
    if err != nil {
        return nil, fmt.Errorf("failed to create socket: %w", err)
    }

    return &SocketReader{
        socketPath: socketPath,
        listener:   listener,
        pipeline:   pipeline,
        logger:     logger,
        readyChan:  make(chan struct{}),
    }, nil
}

// Ready returns a channel that is closed when the reader is accepting connections.
// I3: Callers MUST wait on this before starting FFmpeg to prevent races.
func (r *SocketReader) Ready() <-chan struct{} {
    return r.readyChan
}

// FailedToConnect returns true if FFmpeg never connected within grace period.
// Callers should fall back to pipe mode for subsequent restarts.
func (r *SocketReader) FailedToConnect() bool {
    return r.failedToConnect.Load()
}

// Run accepts one connection and reads lines until EOF.
// I1: ALWAYS closes pipeline channel on exit (prevents goroutine leaks).
// Must be called in a goroutine.
func (r *SocketReader) Run() {
    // I1: Pipeline channel MUST be closed on exit - this is THE source of truth
    defer r.pipeline.CloseChannel()
    defer r.cleanup()

    // I3: Signal that we're ready to accept connections
    close(r.readyChan)

    // Set deadline for FFmpeg to connect
    if ul, ok := r.listener.(*net.UnixListener); ok {
        ul.SetDeadline(time.Now().Add(socketConnectGrace))
    }

    conn, err := r.listener.Accept()
    if err != nil {
        r.failedToConnect.Store(true)
        if r.logger != nil {
            r.logger.Warn("socket_accept_timeout",
                "path", r.socketPath,
                "grace", socketConnectGrace,
                "error", err,
            )
        }
        return  // CloseChannel called via defer - I1 satisfied
    }
    defer conn.Close()

    // Clear deadline for reading
    conn.SetDeadline(time.Time{})

    scanner := bufio.NewScanner(conn)
    for scanner.Scan() {
        line := scanner.Text()
        atomic.AddInt64(&r.bytesRead, int64(len(line)+1))  // +1 for newline
        atomic.AddInt64(&r.linesRead, 1)
        r.pipeline.FeedLine(line)
    }
    // EOF reached, defers run, parser will drain and exit
}

// cleanup removes the socket file.
func (r *SocketReader) cleanup() {
    r.listener.Close()
    os.Remove(r.socketPath)
}

// Close stops the reader and cleans up.
func (r *SocketReader) Close() error {
    return r.listener.Close()
}

// Stats returns (bytesRead, linesRead, connected).
func (r *SocketReader) Stats() (int64, int64, bool) {
    return atomic.LoadInt64(&r.bytesRead),
           atomic.LoadInt64(&r.linesRead),
           !r.failedToConnect.Load()
}

// SocketPath returns the path to the socket file.
func (r *SocketReader) SocketPath() string {
    return r.socketPath
}
```

**Short deterministic socket paths** (to avoid R5):
```go
// Use PID + clientID for uniqueness, keep path short
socketPath := fmt.Sprintf("/tmp/hls_%d_%d.sock", os.Getpid(), clientID)
// Example: /tmp/hls_12345_42.sock (23 bytes - safe)
```

**Estimated lines**: ~200

### 1.2 Create `internal/parser/socket_reader_windows.go`

**File**: `internal/parser/socket_reader_windows.go` (NEW)

```go
//go:build windows

package parser

import "errors"

// NewSocketReader returns an error on Windows (no Unix socket support).
func NewSocketReader(socketPath string, pipeline *Pipeline) (*SocketReader, error) {
    return nil, errors.New("unix sockets not supported on Windows")
}

// SocketReader stub for Windows compilation.
type SocketReader struct{}

func (r *SocketReader) Run()           {}
func (r *SocketReader) Close() error   { return nil }
func (r *SocketReader) Stats() (int64, int64, bool) { return 0, 0, false }
func (r *SocketReader) SocketPath() string { return "" }
```

**Estimated lines**: ~25

### 1.3 Create `internal/parser/socket_reader_test.go`

**File**: `internal/parser/socket_reader_test.go` (NEW)

#### Basic Functionality Tests

| Test Function | Purpose |
|---------------|---------|
| `TestSocketReader_Basic` | Create socket, connect, read line, close |
| `TestSocketReader_MultipleLines` | Read 100 lines correctly |
| `TestSocketReader_Cleanup` | Verify socket file removed on Close() |
| `TestSocketReader_StaleSocket` | Remove existing socket before create |
| `TestSocketReader_Stats` | Verify bytesRead, linesRead counters |
| `TestSocketReader_CloseBeforeConnect` | Close before FFmpeg connects |
| `TestSocketReader_LongLines` | Handle 64KB+ lines |

#### Invariant Tests (Critical for Correctness)

| Test Function | Invariant Tested | What It Catches |
|---------------|------------------|-----------------|
| `TestSocketReader_ClosesChannelOnEOF` | I1 | Pipeline channel closed when connection ends normally |
| `TestSocketReader_ClosesChannelOnError` | I1 | Pipeline channel closed when Accept() fails |
| `TestSocketReader_ClosesChannelOnTimeout` | I1 | Pipeline channel closed when FFmpeg never connects |
| `TestSocketReader_NoGoroutineLeak` | I1 | No leaked goroutines after Run() exits |
| `TestSocketReader_PathTooLong` | I2 | Returns error for paths > 104 bytes |
| `TestSocketReader_PathExactlyAtLimit` | I2 | Works for paths exactly at limit |
| `TestSocketReader_ReadyBeforeAccept` | I3 | Ready() channel closed before Accept() blocks |
| `TestSocketReader_RaceFFmpegConnectsFirst` | I3 | Works even if FFmpeg connects "instantly" |

#### Edge Cases & Stress Tests

| Test Function | Purpose |
|---------------|---------|
| `TestSocketReader_ConnectGraceTimeout` | Verify 3s timeout, `FailedToConnect()` returns true |
| `TestSocketReader_ConcurrentClients` | 10 socket readers in parallel |
| `TestSocketReader_RepeatedCreateDestroy` | 100 create/destroy cycles (leak detection) |
| `TestSocketReader_LongTMPDIR` | Simulates container with long TMPDIR path |

#### Benchmarks

| Benchmark | Purpose |
|-----------|---------|
| `BenchmarkSocketReader_Throughput` | Lines/second |
| `BenchmarkSocketReader_Latency` | ns/line |
| `BenchmarkSocketReader_ShortLines` | 100-byte lines |
| `BenchmarkSocketReader_LongLines` | 64KB lines |

#### Example Invariant Test

```go
// TestSocketReader_NoGoroutineLeak verifies I1: no goroutine leaks
func TestSocketReader_NoGoroutineLeak(t *testing.T) {
    // Allow for test framework goroutines
    runtime.GC()
    time.Sleep(10 * time.Millisecond)
    before := runtime.NumGoroutine()

    // Create socket reader
    pipeline := NewPipeline("test", PipelineStdout, 100)
    socketPath := filepath.Join(t.TempDir(), "test.sock")
    reader, err := NewSocketReader(socketPath, pipeline, nil)
    require.NoError(t, err)

    // Start reader in goroutine
    done := make(chan struct{})
    go func() {
        reader.Run()
        close(done)
    }()

    // Wait for ready
    <-reader.Ready()

    // Close without connecting (simulates FFmpeg failure)
    reader.Close()

    // Wait for Run() to exit
    select {
    case <-done:
        // Good
    case <-time.After(5 * time.Second):
        t.Fatal("reader.Run() did not exit")
    }

    // Verify pipeline channel is closed (I1)
    select {
    case _, ok := <-pipeline.Lines():
        if ok {
            t.Fatal("pipeline channel should be closed")
        }
    default:
        t.Fatal("pipeline channel should be closed, not blocked")
    }

    // Check for goroutine leaks
    runtime.GC()
    time.Sleep(10 * time.Millisecond)
    after := runtime.NumGoroutine()

    if after > before {
        t.Errorf("goroutine leak: %d before, %d after (leaked %d)",
            before, after, after-before)
    }
}
```

**Estimated lines**: ~400

### 1.4 Phase 1 Definition of Done (DoD)

Before marking Phase 1 complete, verify ALL items:

#### Code Requirements
- [ ] `SocketReader` closes listener idempotently (safe to call `Close()` multiple times)
- [ ] `SocketReader` closes connection idempotently
- [ ] Socket file ALWAYS removed on `Close()` (explicit call)
- [ ] Socket file ALWAYS removed on `Run()` exit (via defer)
- [ ] `Ready()` channel closed BEFORE `Accept()` blocks
- [ ] `CloseChannel()` called on ALL exit paths from `Run()` (via defer)
- [ ] Path validation rejects paths > 104 bytes with clear error message
- [ ] Stats counters (`bytesRead`, `linesRead`) are atomic
- [ ] `FailedToConnect()` returns true after grace period timeout

#### Test Coverage
- [ ] `TestSocketReader_Basic` - create, connect, read, close
- [ ] `TestSocketReader_CloseBeforeConnect` - close before FFmpeg connects
- [ ] `TestSocketReader_CloseAfterConnect` - normal close after data
- [ ] `TestSocketReader_LongLines` - handle 64KB+ lines
- [ ] `TestSocketReader_Stats` - verify bytesRead, linesRead counters
- [ ] `TestSocketReader_PathTooLong` - rejects >104 byte paths
- [ ] `TestSocketReader_NoGoroutineLeak` - no leaked goroutines
- [ ] `TestSocketReader_ReadyBeforeAccept` - Ready() closed before Accept()
- [ ] All tests pass with `-race`

#### Documentation
- [ ] GoDoc comments on all exported functions
- [ ] Invariants documented in code comments

---

## Phase 2: Pipeline Integration

### 2.0 Unified LineSource Interface (Reduces Missed Cleanup)

**Problem**: Branching logic (socket vs pipe) is a common place to miss lifecycle steps.

**Solution**: Single interface that both `PipeReader` and `SocketReader` implement:

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

**Why this helps**:
- Supervisor code becomes uniform (no if/else branches for cleanup)
- Impossible to forget lifecycle steps - interface enforces them
- Easy to add new source types (e.g., TCP socket, named pipe)

**Supervisor usage** (uniform for both modes):

```go
func (s *Supervisor) runOnce(ctx context.Context) (...) {
    // Create source (either pipe or socket)
    var source parser.LineSource
    if s.useProgressSocket {
        source, err = parser.NewSocketReader(socketPath, pipeline, s.logger)
    } else {
        source = parser.NewPipeReader(stdout, pipeline)
    }
    if err != nil {
        return s.runOnceWithFallback(ctx)  // Fallback on socket error
    }

    // UNIFORM LIFECYCLE (same for both modes)
    defer source.Close()           // Step 3: cleanup
    go source.Run()                // Step 2: start reading
    <-source.Ready()               // Step 4: wait for ready

    // Now safe to start FFmpeg
    if err := cmd.Start(); err != nil {
        return ...
    }
    // ...
}
```

### 2.1 Modify `internal/parser/pipeline.go`

**File**: `internal/parser/pipeline.go`
**Current lines**: 161

| Change | Location | Description |
|--------|----------|-------------|
| Add `LineSource` interface | Before line 20 | Unified abstraction |
| Add `FeedLine()` method | After line 96 | Non-blocking line feed from socket |
| Add `CloseChannel()` method | After FeedLine | Signal parser to stop |
| Update `RunReader()` doc | Lines 69-72 | Clarify this is for pipe mode |

**New methods** (insert after line 96):

```go
// FeedLine adds a line to the pipeline from an external source (e.g., socket).
// Returns true if queued, false if dropped (channel full).
//
// This is the socket-mode equivalent of RunReader(). Instead of reading
// from an io.Reader, lines are fed directly from SocketReader.
func (p *Pipeline) FeedLine(line string) bool {
    atomic.AddInt64(&p.linesRead, 1)

    select {
    case p.lineChan <- line:
        return true
    default:
        atomic.AddInt64(&p.linesDropped, 1)
        return false
    }
}

// CloseChannel closes the line channel, signaling parser to stop.
// Must be called when the source (pipe or socket) is done.
//
// CRITICAL (I1): This MUST be called exactly once by the data source:
//   - Pipe mode: RunReader() calls close(p.lineChan) at EOF
//   - Socket mode: SocketReader.Run() calls CloseChannel() on exit
//
// This is the sole mechanism for parser goroutine termination.
// Failure to call this results in goroutine leaks.
func (p *Pipeline) CloseChannel() {
    p.closeOnce.Do(func() {
        close(p.lineChan)
    })
}
```

**I4 (Symmetry)**: Both pipe mode and socket mode MUST use the same termination mechanism:

| Mode | Who closes channel | When |
|------|-------------------|------|
| **Pipe** | `RunReader()` | After `scanner.Scan()` returns false (EOF) |
| **Socket** | `SocketReader.Run()` | On any exit path (defer) |

**Update existing `RunReader()`** to use `CloseChannel()` for consistency:

```go
// RunReader reads lines from an io.Reader (pipe mode).
// Closes the channel when reader returns EOF or error.
func (p *Pipeline) RunReader(r io.Reader) {
    defer p.CloseChannel()  // ← Use CloseChannel() for consistency (I4)

    scanner := bufio.NewScanner(r)
    for scanner.Scan() {
        // ... existing code
    }
}
```

**Add `sync.Once` to struct** (prevent double-close panic):

```go
type Pipeline struct {
    // ... existing fields
    closeOnce sync.Once  // Ensures CloseChannel() is idempotent
}
```

**Estimated changes**: +30 lines

### 2.2 Create `internal/parser/pipe_reader.go`

**File**: `internal/parser/pipe_reader.go` (NEW)

Wrapper around existing `RunReader()` to implement `LineSource` interface:

```go
package parser

import (
    "io"
    "sync/atomic"
)

// PipeReader reads lines from an io.Reader (FFmpeg stdout pipe).
// Implements LineSource interface for uniform lifecycle management.
type PipeReader struct {
    reader    io.Reader
    pipeline  *Pipeline
    readyChan chan struct{}
    closed    atomic.Bool

    // Stats
    bytesRead int64
    linesRead int64
}

// NewPipeReader creates a new pipe-based line source.
func NewPipeReader(r io.Reader, pipeline *Pipeline) *PipeReader {
    pr := &PipeReader{
        reader:    r,
        pipeline:  pipeline,
        readyChan: make(chan struct{}),
    }
    // Pipe is immediately ready (unlike socket which needs Accept)
    close(pr.readyChan)
    return pr
}

// Run reads lines until EOF. Implements LineSource.
// MUST call pipeline.CloseChannel() on exit (I1).
func (p *PipeReader) Run() {
    defer p.pipeline.CloseChannel()

    scanner := bufio.NewScanner(p.reader)
    for scanner.Scan() {
        line := scanner.Text()
        atomic.AddInt64(&p.bytesRead, int64(len(line)+1))
        atomic.AddInt64(&p.linesRead, 1)
        p.pipeline.FeedLine(line)
    }
}

// Ready returns immediately-closed channel (pipe is always ready).
func (p *PipeReader) Ready() <-chan struct{} {
    return p.readyChan
}

// Close is a no-op for pipes (reader closed by process exit).
func (p *PipeReader) Close() error {
    p.closed.Store(true)
    return nil
}

// Stats returns (bytesRead, linesRead, healthy).
func (p *PipeReader) Stats() (int64, int64, bool) {
    return atomic.LoadInt64(&p.bytesRead),
           atomic.LoadInt64(&p.linesRead),
           !p.closed.Load()
}
```

**Estimated lines**: ~70

### 2.3 Add Pipeline Tests for FeedLine

**File**: `internal/parser/pipeline_test.go`
**Current lines**: ~200

| New Test | Purpose |
|----------|---------|
| `TestPipeline_FeedLine_Basic` | Feed lines, verify parsed |
| `TestPipeline_FeedLine_Drops` | Feed when full, verify drops |
| `TestPipeline_FeedLine_CloseChannel` | Verify parser stops |
| `TestPipeReader_ImplementsLineSource` | Type assertion |
| `TestSocketReader_ImplementsLineSource` | Type assertion |

**Estimated changes**: +80 lines

### 2.4 Phase 2 Definition of Done (DoD)

Before marking Phase 2 complete, verify ALL items:

#### Code Requirements
- [ ] `LineSource` interface defined with `Run()`, `Ready()`, `Close()`, `Stats()`
- [ ] `PipeReader` implements `LineSource`
- [ ] `SocketReader` implements `LineSource`
- [ ] `FeedLine()` returns false when channel full (non-blocking)
- [ ] `CloseChannel()` is idempotent (uses `sync.Once`)
- [ ] `RunReader()` updated to call `CloseChannel()` for symmetry (I4)

#### Test Coverage
- [ ] `TestPipeline_FeedLine_Basic` passes
- [ ] `TestPipeline_FeedLine_Drops` passes
- [ ] `TestPipeline_FeedLine_CloseChannel` passes
- [ ] `TestPipeReader_ImplementsLineSource` - compile-time check
- [ ] `TestSocketReader_ImplementsLineSource` - compile-time check
- [ ] All tests pass with `-race`

#### Interface Compliance
```go
// These must compile:
var _ LineSource = (*PipeReader)(nil)
var _ LineSource = (*SocketReader)(nil)
```

---

## Phase 3: Supervisor Changes

### 3.1 Modify `internal/supervisor/supervisor.go`

**File**: `internal/supervisor/supervisor.go`
**Current lines**: 530

| Change | Location | Description |
|--------|----------|-------------|
| Add socket fields to struct | Lines 64-76 | `useProgressSocket`, `progressSocketPath`, `progressSocket` |
| Add to Config | Lines 79-95 | `UseProgressSocket bool` |
| Update New() | Lines 98-135 | Initialize socket fields |
| Modify runOnce() | Lines 207-333 | Socket creation and cleanup |
| Add socket stats logging | Lines 359-388 | Log socket stats |
| Add ProgressSocketPath() | After line 492 | Getter for socket path |

#### 3.1.1 Struct changes (lines 64-76)

```go
// Add after line 75:
// Socket-based progress (alternative to stdout pipe)
useProgressSocket  bool
progressSocketPath string
progressSocket     *parser.SocketReader
```

#### 3.1.2 Config changes (lines 79-95)

```go
// Add after line 94:
// UseProgressSocket enables Unix socket for progress instead of stdout pipe.
// This provides cleaner separation from stderr debug output.
UseProgressSocket bool
```

#### 3.1.3 New() changes (lines 98-135)

```go
// Add after line 132:
useProgressSocket: cfg.UseProgressSocket,
```

#### 3.1.4 runOnce() changes (lines 207-333)

This is the largest change. The logic flow changes:

**Current flow** (simplified):
```
1. Build command
2. Create stdout pipe (for progress)
3. Create stderr pipe
4. Start command
5. Start stdout reader goroutine
6. Start stderr reader goroutine
7. Wait for command
8. Drain parsers
```

**New flow** (with explicit ordering invariant):
```
1. Build command
2. IF useProgressSocket:
   a. Create socket path (short, deterministic: /tmp/hls_<pid>_<clientID>.sock)
   b. Create SocketReader (validates path length ≤104 bytes)
   c. Start socket reader goroutine
   d. *** WAIT for Ready() signal *** ← INVARIANT: accept() must be ready before FFmpeg starts
   ELSE:
   a. Create stdout pipe (current behavior)
3. Create stderr pipe
4. Start command ← ONLY after socket/pipe is ready
5. IF !useProgressSocket:
   a. Start stdout reader goroutine (current)
6. Start stderr reader goroutine
7. Wait for command
8. IF useProgressSocket:
   a. Close socket
   b. Clean up socket file (guaranteed by defer)
9. Drain parsers (channel close propagates EOF)
```

**INVARIANT (I3)**: The socket Accept() goroutine MUST be running BEFORE FFmpeg starts.
Otherwise: intermittent "connection refused" under load.

**Test to verify invariant**:
```go
func TestSupervisor_SocketReadyBeforeFFmpeg(t *testing.T) {
    // Stress test: restart client 100 times rapidly
    for i := 0; i < 100; i++ {
        // Each restart should succeed without "connection refused"
    }
}
```

**Key code block** (insert at line ~220):

```go
// Create progress source (socket or stdout pipe)
var stdout io.ReadCloser
var progressSocket *parser.SocketReader

if s.statsEnabled {
    if s.useProgressSocket {
        // Socket mode: create Unix socket for progress
        s.progressSocketPath = filepath.Join(os.TempDir(),
            fmt.Sprintf("hls_swarm_%d.sock", s.clientID))

        s.progressPipeline = parser.NewPipeline(
            s.clientID, "progress",
            s.statsBufferSize, s.statsDropThreshold,
        )

        var err error
        progressSocket, err = parser.NewSocketReader(
            s.progressSocketPath,
            s.progressPipeline,
        )
        if err != nil {
            s.logger.Warn("socket_creation_failed",
                "client_id", s.clientID,
                "error", err,
                "fallback", "pipe",
            )
            // Fall back to pipe mode for this run
            s.useProgressSocket = false
        } else {
            s.progressSocket = progressSocket
            // Start socket reader (non-blocking)
            go progressSocket.Run()
        }
    }

    // Pipe mode (default or fallback)
    if !s.useProgressSocket {
        stdout, err = cmd.StdoutPipe()
        if err != nil {
            s.logger.Error("failed_to_create_stdout_pipe",
                "client_id", s.clientID,
                "error", err,
            )
            return 1, 0, fmt.Errorf("stdout pipe: %w", err)
        }

        s.progressPipeline = parser.NewPipeline(
            s.clientID, "progress",
            s.statsBufferSize, s.statsDropThreshold,
        )
    }

    // stderr is always a pipe
    stderr, err = cmd.StderrPipe()
    if err != nil {
        if progressSocket != nil {
            progressSocket.Close()
        }
        s.logger.Error("failed_to_create_stderr_pipe",
            "client_id", s.clientID,
            "error", err,
        )
        return 1, 0, fmt.Errorf("stderr pipe: %w", err)
    }
}
```

**Cleanup code** (add before function return):

```go
// Cleanup socket (socket mode only)
if progressSocket != nil {
    if err := progressSocket.Close(); err != nil {
        s.logger.Warn("socket_cleanup_error",
            "client_id", s.clientID,
            "path", s.progressSocketPath,
            "error", err,
        )
    }
}
```

#### 3.1.5 New getter (after line 492)

```go
// ProgressSocketPath returns the path to the progress socket.
// Returns empty string if socket mode is not enabled.
func (s *Supervisor) ProgressSocketPath() string {
    return s.progressSocketPath
}

// UseProgressSocket returns whether socket mode is enabled.
func (s *Supervisor) UseProgressSocket() bool {
    return s.useProgressSocket
}
```

**Estimated changes**: +100 lines

### 3.2 Progress Watchdog (Prevents Silent Failures)

**Problem**: If FFmpeg can't connect to socket, or progress isn't received, metrics look "stuck" without obvious failure.

**Solution**: Require progress within timeout, otherwise mark client unhealthy.

```go
// progressWatchdog monitors for "no progress received" condition.
type progressWatchdog struct {
    timeout       time.Duration
    lastProgress  atomic.Int64  // Unix timestamp of last progress
    firstProgress atomic.Bool   // True after first progress received
    unhealthy     atomic.Bool
}

func newProgressWatchdog(timeout time.Duration) *progressWatchdog {
    return &progressWatchdog{timeout: timeout}
}

// RecordProgress is called by progress callback.
func (w *progressWatchdog) RecordProgress() {
    w.lastProgress.Store(time.Now().Unix())
    w.firstProgress.Store(true)
}

// Check returns true if progress is healthy, false if timeout exceeded.
func (w *progressWatchdog) Check() bool {
    if !w.firstProgress.Load() {
        // Haven't received first progress yet - check if timeout exceeded
        return true  // Give benefit of doubt during startup
    }

    last := time.Unix(w.lastProgress.Load(), 0)
    if time.Since(last) > w.timeout {
        w.unhealthy.Store(true)
        return false
    }
    return true
}

// IsUnhealthy returns true if watchdog has ever triggered.
func (w *progressWatchdog) IsUnhealthy() bool {
    return w.unhealthy.Load()
}
```

**Supervisor integration**:

```go
const (
    // progressFirstTimeout is how long to wait for FIRST progress after FFmpeg starts.
    progressFirstTimeout = 10 * time.Second

    // progressStaleTimeout is how long without progress before marking unhealthy.
    progressStaleTimeout = 30 * time.Second
)

func (s *Supervisor) runOnce(ctx context.Context) (...) {
    watchdog := newProgressWatchdog(progressStaleTimeout)

    // Wire up progress callback to feed watchdog
    s.progressCallback = func(p *parser.ProgressUpdate) {
        watchdog.RecordProgress()
        // ... existing callback logic
    }

    // After starting FFmpeg, start watchdog goroutine
    watchdogCtx, cancelWatchdog := context.WithCancel(ctx)
    defer cancelWatchdog()

    go func() {
        // Wait for first progress
        select {
        case <-time.After(progressFirstTimeout):
            if !watchdog.firstProgress.Load() {
                s.logger.Warn("no_progress_received",
                    "clientID", s.clientID,
                    "timeout", progressFirstTimeout,
                    "socketPath", s.progressSocketPath,
                    "action", "will_retry_with_pipe_mode",
                )
                // Mark for pipe mode fallback on next restart
                s.socketModeFailed.Store(true)
            }
        case <-watchdogCtx.Done():
            return
        }

        // Periodic check for stale progress
        ticker := time.NewTicker(5 * time.Second)
        defer ticker.Stop()

        for {
            select {
            case <-ticker.C:
                if !watchdog.Check() {
                    s.logger.Warn("progress_stale",
                        "clientID", s.clientID,
                        "lastProgress", time.Unix(watchdog.lastProgress.Load(), 0),
                        "staleFor", time.Since(time.Unix(watchdog.lastProgress.Load(), 0)),
                    )
                    // Update client stats
                    s.clientStats.SetUnhealthy("progress_stale")
                }
            case <-watchdogCtx.Done():
                return
            }
        }
    }()

    // ... rest of runOnce
}
```

**Fallback behavior**:

| Condition | Action | Log Event |
|-----------|--------|-----------|
| Socket connect timeout (3s) | Fall back to pipe for this run | `socket_accept_timeout` |
| No progress in 10s after start | Mark `socketModeFailed`, use pipe on restart | `no_progress_received` |
| Progress stale for 30s | Mark client unhealthy, continue | `progress_stale` |

**Metrics exposed**:
- `hls_swarm_client_unhealthy_total` (counter) - Clients marked unhealthy
- `hls_swarm_progress_watchdog_triggers_total` (counter) - Watchdog triggers
- Per-client `IsUnhealthy` in TUI detail view

### 3.3 Add Supervisor Socket Tests

**File**: `internal/supervisor/supervisor_test.go`
**Current lines**: ~400

| New Test | Purpose |
|----------|---------|
| `TestSupervisor_SocketMode_Basic` | Socket creation and cleanup |
| `TestSupervisor_SocketMode_Fallback` | Fall back when socket fails |
| `TestSupervisor_SocketMode_Integration` | Real FFmpeg with socket |
| `TestSupervisor_SocketMode_Cleanup` | Verify no stale sockets |
| `TestSupervisor_ProgressWatchdog_FirstTimeout` | No progress triggers fallback |
| `TestSupervisor_ProgressWatchdog_Stale` | Stale progress marks unhealthy |

**Estimated changes**: +200 lines

### 3.4 Phase 3 Definition of Done (DoD)

Before marking Phase 3 complete, verify ALL items:

#### Code Requirements
- [ ] Supervisor uses `LineSource` interface (uniform lifecycle)
- [ ] Socket mode creates socket BEFORE starting FFmpeg
- [ ] `<-source.Ready()` called BEFORE `cmd.Start()`
- [ ] `defer source.Close()` ensures cleanup on all exit paths
- [ ] Socket path uses short deterministic format: `/tmp/hls_<pid>_<clientID>.sock`
- [ ] Progress watchdog monitors for first progress (10s timeout)
- [ ] Progress watchdog monitors for stale progress (30s timeout)
- [ ] `socketModeFailed` flag triggers pipe fallback on restart
- [ ] Structured log events for all failure conditions

#### Test Coverage
- [ ] `TestSupervisor_SocketMode_Basic` - happy path
- [ ] `TestSupervisor_SocketMode_Fallback` - socket creation failure
- [ ] `TestSupervisor_SocketReadyBeforeFFmpeg` - ordering invariant (I3)
- [ ] `TestSupervisor_ClientRestartStress` - 100 rapid restarts
- [ ] `TestSupervisor_ProgressWatchdog_FirstTimeout` - no progress fallback
- [ ] `TestSupervisor_ProgressWatchdog_Stale` - stale detection
- [ ] All tests pass with `-race`

#### Observability
- [ ] `socket_accept_timeout` log event includes clientID, socketPath
- [ ] `no_progress_received` log event includes clientID, timeout, socketPath
- [ ] `progress_stale` log event includes clientID, lastProgress, staleFor
- [ ] Prometheus metrics for unhealthy clients and watchdog triggers

---

## Phase 4: FFmpeg Config Updates

### 4.1 Modify `internal/process/ffmpeg.go`

**File**: `internal/process/ffmpeg.go`
**Current lines**: 272

| Change | Location | Description |
|--------|----------|-------------|
| Add ProgressSocket to config | Lines 35-83 | New field |
| Add DebugLogging to config | Lines 35-83 | New field |
| Update buildArgs() | Lines 140-144 | Use socket URL |
| Update CommandString() | Lines 268-271 | Include socket path |

#### 4.1.1 Config changes (lines 35-83)

```go
// Add after line 82:

// ProgressSocket is the Unix socket path for progress output.
// If set, FFmpeg uses -progress unix://path instead of pipe:1.
// This enables cleaner separation from stderr debug logs.
ProgressSocket string

// DebugLogging enables -loglevel debug for detailed segment timing.
// Only effective when ProgressSocket is set (otherwise debug
// output would corrupt progress parsing on stderr).
DebugLogging bool
```

#### 4.1.2 buildArgs() changes (lines 140-144)

```go
// Replace lines 140-144 with:
if r.config.StatsEnabled {
    if r.config.ProgressSocket != "" {
        // Socket mode: progress goes to dedicated Unix socket
        args = append(args, "-progress", "unix://"+r.config.ProgressSocket)
    } else {
        // Pipe mode: progress goes to stdout
        args = append(args, "-progress", "pipe:1")
    }
    args = append(args, "-stats_period", "1")
}

// Update log level selection (around line 128):
logLevel := r.config.LogLevel
if r.config.StatsEnabled && r.config.StatsLogLevel != "" {
    logLevel = r.config.StatsLogLevel
}
// Enable debug logging only in socket mode (safe because progress is separated)
if r.config.DebugLogging && r.config.ProgressSocket != "" {
    logLevel = "debug"
}
```

**Estimated changes**: +30 lines

### 4.2 Per-Client User-Agent for Debugging

**File**: `internal/process/ffmpeg.go`
**Current lines**: 272

**Purpose**: Enable correlation between client-side metrics, origin logs, and packet captures.

**Changes**:

1. **Modify `BuildCommand()` signature** to use clientID for User-Agent:
```go
func (r *FFmpegRunner) BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error) {
    // Generate per-client User-Agent
    userAgent := fmt.Sprintf("%s/client-%d", r.config.UserAgent, clientID)
    args := r.buildArgsWithUserAgent(userAgent)
    // ...
}
```

2. **Update User-Agent construction** (around line 165):
```go
// Current:
args = append(args, "-user_agent", r.config.UserAgent)

// Proposed (in buildArgsWithUserAgent):
args = append(args, "-user_agent", userAgent)  // Includes client ID
```

**Format**: `go-ffmpeg-hls-swarm/1.0/client-{clientID}`

**Benefits**:

| Use Case | Command/Filter |
|----------|----------------|
| **tcpdump** | `tcpdump -A \| grep "client-42"` |
| **Wireshark** | `http.user_agent contains "client-42"` |
| **Nginx logs** | `grep "client-42" access.log` |
| **CDN debugging** | Identify per-client cache patterns |

**Estimated changes**: +15 lines

### 4.3 Update FFmpeg Tests

**File**: `internal/process/ffmpeg_test.go`
**Current lines**: ~300

| New/Updated Test | Purpose |
|------------------|---------|
| `TestFFmpegRunner_BuildArgs_SocketMode` | Verify unix:// URL |
| `TestFFmpegRunner_BuildArgs_DebugLogging` | Verify -loglevel debug |
| `TestFFmpegRunner_BuildArgs_SocketFallback` | Empty socket = pipe:1 |
| `TestFFmpegRunner_BuildArgs_UserAgent` | Verify per-client User-Agent format |

**Estimated changes**: +60 lines

### 4.4 Phase 4 Definition of Done (DoD)

Before marking Phase 4 complete, verify ALL items:

#### Code Requirements
- [ ] `FFmpegConfig.ProgressSocket` field added
- [ ] `FFmpegConfig.DebugLogging` field added
- [ ] `buildArgs()` uses `-progress unix://...` when ProgressSocket is set
- [ ] `buildArgs()` falls back to `-progress pipe:1` when ProgressSocket is empty
- [ ] `buildArgs()` uses `-loglevel debug` when DebugLogging is true
- [ ] Per-client User-Agent format: `go-ffmpeg-hls-swarm/1.0/client-{clientID}`

#### Test Coverage
- [ ] `TestFFmpegRunner_BuildArgs_SocketMode` - unix:// in output
- [ ] `TestFFmpegRunner_BuildArgs_DebugLogging` - loglevel debug
- [ ] `TestFFmpegRunner_BuildArgs_SocketFallback` - empty = pipe:1
- [ ] `TestFFmpegRunner_BuildArgs_UserAgent` - client ID in user agent
- [ ] All tests pass with `-race`

---

## Phase 5: CLI Flag & Config Wiring

### 5.1 Modify `internal/config/config.go`

**File**: `internal/config/config.go`
**Current lines**: ~100

| Change | Location | Description |
|--------|----------|-------------|
| Add UseProgressSocket | Config struct | New bool field |
| Add DebugLogging | Config struct | New bool field |
| Update DefaultConfig() | | Set defaults |

```go
// Add to Config struct:
UseProgressSocket bool // Use Unix socket for FFmpeg progress
DebugLogging      bool // Enable -loglevel debug (requires socket)
```

**Estimated changes**: +10 lines

### 5.2 Modify `internal/config/flags.go`

**File**: `internal/config/flags.go`
**Current lines**: ~150

| Change | Location | Description |
|--------|----------|-------------|
| Add -progress-socket flag | Flag definitions | Bool flag |
| Add -ffmpeg-debug flag | Flag definitions | Bool flag |
| Update flag.Usage | | Document new flags |

```go
// Add flags:
flag.BoolVar(&cfg.UseProgressSocket, "progress-socket", false,
    "Use Unix socket for FFmpeg progress output (experimental)")
flag.BoolVar(&cfg.DebugLogging, "ffmpeg-debug", false,
    "Enable FFmpeg debug logging for detailed segment timing (requires -progress-socket)")
```

**Estimated changes**: +15 lines

### 5.3 Modify `internal/orchestrator/client_manager.go`

**File**: `internal/orchestrator/client_manager.go`
**Current lines**: 572

| Change | Location | Description |
|--------|----------|-------------|
| Add useProgressSocket to struct | Lines 17-77 | New field |
| Update ManagerConfig | Lines 94-106 | New field |
| Update NewClientManager() | Lines 108-138 | Initialize field |
| Update StartClient() | Lines 140-221 | Pass to supervisor |

```go
// Add to ClientManager struct (around line 30):
useProgressSocket bool

// Add to ManagerConfig (around line 105):
UseProgressSocket bool

// Update NewClientManager() (around line 129):
useProgressSocket: cfg.UseProgressSocket,

// Update StartClient() supervisor creation (around line 180):
supervisor.Config{
    // ... existing fields ...
    UseProgressSocket: m.useProgressSocket,
}
```

**Estimated changes**: +15 lines

### 5.4 Modify `internal/orchestrator/orchestrator.go`

**File**: `internal/orchestrator/orchestrator.go`
**Current lines**: ~500

| Change | Location | Description |
|--------|----------|-------------|
| Pass UseProgressSocket to ClientManager | New() | Config propagation |
| Update FFmpeg config creation | | Add ProgressSocket path |

The orchestrator needs to coordinate between the supervisor (which creates the socket path) and the FFmpeg config (which needs to use it). However, since the socket path is per-client and created in supervisor.runOnce(), we need a slightly different approach:

**Option A**: Supervisor creates socket path and passes to FFmpeg builder ← **CHOSEN**
**Option B**: FFmpeg builder receives socket path via context

**DECISION: Option A - Supervisor owns socket lifecycle**

The Supervisor is responsible for:
1. **Creating** the socket path (deterministic: `/tmp/hls_<pid>_<clientID>.sock`)
2. **Passing** the path to `ProcessBuilder.SetProgressSocket()`
3. **Cleaning up** the socket file on exit

```go
// In supervisor.ProcessBuilder interface:
type ProcessBuilder interface {
    BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error)
    Name() string

    // SetProgressSocket sets the Unix socket path for progress output.
    // Called by supervisor before BuildCommand() when socket mode is enabled.
    SetProgressSocket(path string)
}
```

#### Socket Cleanup Strategy

**Three-layer cleanup ensures no stale sockets**:

```go
// Layer 1: SocketReader.cleanup() - called on every Run() exit
func (r *SocketReader) cleanup() {
    r.listener.Close()
    os.Remove(r.socketPath)  // Always attempt removal
}

// Layer 2: SocketReader.Run() - deferred cleanup
func (r *SocketReader) Run() {
    defer r.cleanup()  // Runs on ANY exit: normal, error, panic
    // ...
}

// Layer 3: Supervisor.runOnce() - belt-and-suspenders
func (s *Supervisor) runOnce(ctx context.Context) (...) {
    socketPath := fmt.Sprintf("/tmp/hls_%d_%d.sock", os.Getpid(), s.clientID)

    // Cleanup on supervisor exit (covers SocketReader creation failure)
    defer func() {
        if socketPath != "" {
            os.Remove(socketPath)  // Idempotent - ok if already removed
        }
    }()

    // ...
}
```

**Cleanup Matrix**:

| Scenario | Layer 1 | Layer 2 | Layer 3 | Socket Removed? |
|----------|---------|---------|---------|-----------------|
| Normal exit | ✅ | ✅ | ✅ | ✅ |
| FFmpeg crash | ✅ | ✅ | ✅ | ✅ |
| SocketReader error | ❌ | ✅ | ✅ | ✅ |
| SocketReader creation fail | ❌ | ❌ | ✅ | ✅ |
| Supervisor panic | ❌ | ❌ | ✅ | ✅ |
| SIGKILL (process killed) | ❌ | ❌ | ❌ | ❌ * |

**\* Stale socket handling**: On startup, `NewSocketReader()` always removes existing socket:

```go
func NewSocketReader(socketPath string, ...) (*SocketReader, error) {
    // Remove stale socket from previous run (crash recovery)
    if _, err := os.Stat(socketPath); err == nil {
        os.Remove(socketPath)
        log.Debug("removed_stale_socket", "path", socketPath)
    }
    // ...
}
```

**Periodic cleanup** (optional, for long-running orchestrator):

```go
// Orchestrator can periodically clean up orphaned sockets
func (o *Orchestrator) cleanupOrphanedSockets() {
    pattern := fmt.Sprintf("/tmp/hls_%d_*.sock", os.Getpid())
    matches, _ := filepath.Glob(pattern)

    activeClients := o.clientManager.ActiveClientIDs()

    for _, path := range matches {
        // Extract clientID from path
        var clientID int
        fmt.Sscanf(filepath.Base(path), "hls_%d_%d.sock", new(int), &clientID)

        if !contains(activeClients, clientID) {
            os.Remove(path)
            log.Info("removed_orphan_socket", "path", path)
        }
    }
}
```

**Estimated changes**: +20 lines

### 5.5 Modify `cmd/go-ffmpeg-hls-swarm/main.go`

**File**: `cmd/go-ffmpeg-hls-swarm/main.go`
**Current lines**: ~200

| Change | Location | Description |
|--------|----------|-------------|
| Pass UseProgressSocket to orchestrator | main() | Config propagation |
| Update printFFmpegCommand() | | Show socket mode |

```go
// Update orchestrator config:
orchCfg := orchestrator.Config{
    // ... existing fields ...
    UseProgressSocket: cfg.UseProgressSocket,
}

// Update printFFmpegCommand() output:
if cfg.UseProgressSocket {
    fmt.Println("# Progress mode: Unix socket (path generated per client)")
} else {
    fmt.Println("# Progress mode: stdout (pipe:1)")
}
```

**Estimated changes**: +15 lines

### 5.6 Phase 5 Definition of Done (DoD)

Before marking Phase 5 complete, verify ALL items:

#### Code Requirements
- [ ] `Config.UseProgressSocket` field added to config.go
- [ ] `--progress-socket` CLI flag parses correctly
- [ ] Flag defaults to `false` (socket mode is opt-in)
- [ ] `ClientManager` passes socket path to Supervisor
- [ ] `Orchestrator` passes config to ClientManager
- [ ] `main.go` propagates config to orchestrator
- [ ] `printFFmpegCommand()` shows socket mode status

#### End-to-End Verification
- [ ] `./go-ffmpeg-hls-swarm --progress-socket --print-cmd URL` shows `unix://` in output
- [ ] Without `--progress-socket`, shows `pipe:1`
- [ ] Help text (`--help`) documents the flag

#### Test Coverage
- [ ] `TestConfig_ParseFlags_ProgressSocket` - flag parsing
- [ ] Integration test: socket mode with real FFmpeg (if available)

---

## Phase 6: Debug Parser Enhancement (High-Value Metrics)

This phase implements the "gold mine" metrics from FFmpeg debug output:
1. **Segment Download Wall Time** - Primary metric (reliable under keep-alive)
2. **TCP Connect Latency** - Secondary metric (only for new connections)
3. **Playlist Jitter** - Deviation from expected refresh interval
4. **TCP Health Ratio** - Connection success/failure ratio

#### ⚠️ Metric Reliability Considerations

| Metric | Reliability | Notes |
|--------|-------------|-------|
| **Segment Wall Time** | ✅ HIGH | Always available, robust under keep-alive |
| **TCP Connect Latency** | ⚠️ MEDIUM | Only for new connections (not reused) |
| **"Request-to-Connect TTFB"** | ❌ REMOVED | Unreliable correlation with parallel fetching |

**Why we removed "TTFB" naming**:
- TCP connect time ≠ true TTFB (doesn't include TLS, HTTP overhead, server time)
- Keep-alive connections mean most segments have NO TCP connect events
- Parallel prefetching makes HLS-request → TCP-connect correlation unreliable

### 6.1 Create `internal/parser/debug_events.go`

**File**: `internal/parser/debug_events.go` (NEW)

```go
package parser

import (
    "regexp"
    "strconv"
    "strings"
    "sync"
    "sync/atomic"
    "time"
)

// DebugEventType identifies debug log events.
type DebugEventType int

const (
    // Segment fetch events (for TTFB calculation)
    DebugEventHLSRequest DebugEventType = iota  // [hls @ ...] HLS request for url
    DebugEventTCPStart                          // [tcp @ ...] Starting connection attempt
    DebugEventTCPConnected                      // [tcp @ ...] Successfully connected
    DebugEventTCPFailed                         // [tcp @ ...] Failed/refused/timeout

    // Playlist events (for jitter calculation)
    DebugEventPlaylistOpen                      // [hls @ ...] Opening '...m3u8' for reading
    DebugEventSequenceChange                    // [hls @ ...] Media sequence change
)

// DebugEvent represents a parsed debug log event.
type DebugEvent struct {
    Type      DebugEventType
    Timestamp time.Time
    URL       string
    IP        string
    Port      int
    OldSeq    int
    NewSeq    int
    FailReason string  // "refused", "timeout", "error"
}

// Pre-compiled regex patterns for performance.
var (
    // [hls @ 0x55...] HLS request for url 'http://.../seg00123.ts', offset 0, playlist 0
    reHLSRequest = regexp.MustCompile(`\[hls @ 0x[0-9a-f]+\] HLS request for url '([^']+)'`)

    // [tcp @ 0x55...] Starting connection attempt to 10.177.0.10 port 17080
    reTCPStart = regexp.MustCompile(`\[tcp @ 0x[0-9a-f]+\] Starting connection attempt to ([\d.]+) port (\d+)`)

    // [tcp @ 0x55...] Successfully connected to 10.177.0.10 port 17080
    reTCPConnected = regexp.MustCompile(`\[tcp @ 0x[0-9a-f]+\] Successfully connected to ([\d.]+) port (\d+)`)

    // [tcp @ 0x55...] Connection refused / timed out / Failed to connect
    reTCPFailed = regexp.MustCompile(`\[tcp @ 0x[0-9a-f]+\] (Connection refused|[Cc]onnection timed out|Failed to connect)`)

    // [hls @ 0x55...] Opening 'http://.../stream.m3u8' for reading
    rePlaylistOpen = regexp.MustCompile(`\[hls @ 0x[0-9a-f]+\] Opening '([^']+\.m3u8)' for reading`)

    // [hls @ 0x55...] Media sequence change (3433 -> 3438)
    reSequenceChange = regexp.MustCompile(`\[hls @ 0x[0-9a-f]+\] Media sequence change \((\d+) -> (\d+)\)`)
)

// DebugEventCallback is called for each parsed debug event.
type DebugEventCallback func(*DebugEvent)

// SegmentFetchState tracks TTFB for a single segment request.
type SegmentFetchState struct {
    URL          string
    RequestTime  time.Time
    ConnectStart time.Time
    ConnectEnd   time.Time
}

// TTFB returns Time to First Byte (request to connect complete).
func (s *SegmentFetchState) TTFB() time.Duration {
    if s.ConnectEnd.IsZero() || s.RequestTime.IsZero() {
        return 0
    }
    return s.ConnectEnd.Sub(s.RequestTime)
}

// ConnectTime returns pure TCP connect time.
func (s *SegmentFetchState) ConnectTime() time.Duration {
    if s.ConnectEnd.IsZero() || s.ConnectStart.IsZero() {
        return 0
    }
    return s.ConnectEnd.Sub(s.ConnectStart)
}

// DebugEventParser parses FFmpeg -loglevel debug output.
// Implements LineParser interface.
type DebugEventParser struct {
    clientID int
    callback DebugEventCallback

    mu sync.Mutex

    // Manifest bandwidth (parsed from FFmpeg debug output)
    // DECISION: Parse from "[hls @ ...] Stream variant found: BANDWIDTH=..."
    manifestBandwidth atomic.Int64  // bits per second

    // Segment Wall Time tracking (PRIMARY - reliable under keep-alive)
    currentSegment    *SegmentDownloadState  // Currently downloading segment
    segmentSamples    []time.Duration        // Ring buffer of wall times
    segmentCount      int64

    // TCP Connect tracking (SECONDARY - only for new connections)
    pendingTCPConnect map[string]time.Time   // "IP:port" -> connect start
    tcpConnectCount   int64
    tcpConnectTotal   time.Duration

    // Playlist jitter tracking
    lastPlaylistRefresh time.Time
    targetDuration      time.Duration  // From config or default 2s
    playlistRefreshes   int64
    playlistLateCount   int64
    playlistJitterSum   int64  // nanoseconds
    playlistMaxJitter   int64  // nanoseconds
    tcpSuccessCount     int64
    tcpFailureCount     int64
    tcpTimeoutCount     int64
    tcpRefusedCount     int64
    sequenceSkips       int64
    linesProcessed      int64
}

// Regex for parsing manifest BANDWIDTH
var reBandwidth = regexp.MustCompile(`BANDWIDTH=(\d+)`)

// GetManifestBandwidth returns the parsed BANDWIDTH value (bits/sec).
// Returns 0 if not yet parsed.
func (p *DebugEventParser) GetManifestBandwidth() int64 {
    return p.manifestBandwidth.Load()
}

// NewDebugEventParser creates a new debug event parser.
func NewDebugEventParser(clientID int, targetDuration time.Duration, callback DebugEventCallback) *DebugEventParser {
    if targetDuration <= 0 {
        targetDuration = 2 * time.Second  // HLS default
    }
    return &DebugEventParser{
        clientID:        clientID,
        callback:        callback,
        targetDuration:  targetDuration,
        pendingSegments: make(map[string]*SegmentFetchState),
        ttfbSamples:     make([]time.Duration, 0, 1000),
    }
}

// ParseLine implements LineParser interface.
func (p *DebugEventParser) ParseLine(line string) {
    atomic.AddInt64(&p.linesProcessed, 1)

    // Fast path: most lines don't match any pattern
    if !strings.Contains(line, " @ 0x") {
        return
    }

    now := time.Now()

    // Check patterns in order of expected frequency

    // 1. TCP Connected (completes TTFB)
    if m := reTCPConnected.FindStringSubmatch(line); m != nil {
        p.handleTCPConnected(now, m[1], m[2])
        return
    }

    // 2. HLS Request (starts TTFB tracking)
    if m := reHLSRequest.FindStringSubmatch(line); m != nil {
        p.handleHLSRequest(now, m[1])
        return
    }

    // 3. TCP Start
    if m := reTCPStart.FindStringSubmatch(line); m != nil {
        p.handleTCPStart(now, m[1], m[2])
        return
    }

    // 4. TCP Failed
    if m := reTCPFailed.FindStringSubmatch(line); m != nil {
        p.handleTCPFailed(now, m[1])
        return
    }

    // 5. Playlist Open (for jitter)
    if m := rePlaylistOpen.FindStringSubmatch(line); m != nil {
        p.handlePlaylistOpen(now, m[1])
        return
    }

    // 6. Sequence Change
    if m := reSequenceChange.FindStringSubmatch(line); m != nil {
        old, _ := strconv.Atoi(m[1])
        new, _ := strconv.Atoi(m[2])
        p.handleSequenceChange(now, old, new)
        return
    }
}

func (p *DebugEventParser) handleHLSRequest(t time.Time, url string) {
    // Only track .ts segments for TTFB
    if !strings.HasSuffix(strings.ToLower(url), ".ts") &&
       !strings.Contains(strings.ToLower(url), ".ts?") {
        return
    }

    p.mu.Lock()
    p.pendingSegments[url] = &SegmentFetchState{
        URL:         url,
        RequestTime: t,
    }
    p.mu.Unlock()

    if p.callback != nil {
        p.callback(&DebugEvent{
            Type:      DebugEventHLSRequest,
            Timestamp: t,
            URL:       url,
        })
    }
}

func (p *DebugEventParser) handleTCPStart(t time.Time, ip, port string) {
    portNum, _ := strconv.Atoi(port)

    p.mu.Lock()
    // Associate with most recent pending segment
    for _, state := range p.pendingSegments {
        if state.ConnectStart.IsZero() {
            state.ConnectStart = t
            p.pendingTCP = state
            break
        }
    }
    p.mu.Unlock()

    if p.callback != nil {
        p.callback(&DebugEvent{
            Type:      DebugEventTCPStart,
            Timestamp: t,
            IP:        ip,
            Port:      portNum,
        })
    }
}

func (p *DebugEventParser) handleTCPConnected(t time.Time, ip, port string) {
    atomic.AddInt64(&p.tcpSuccessCount, 1)
    portNum, _ := strconv.Atoi(port)

    p.mu.Lock()
    // Complete pending segment and record TTFB
    if p.pendingTCP != nil {
        p.pendingTCP.ConnectEnd = t
        ttfb := p.pendingTCP.TTFB()
        if ttfb > 0 {
            p.recordTTFB(ttfb)
        }
        delete(p.pendingSegments, p.pendingTCP.URL)
        p.pendingTCP = nil
    }
    p.mu.Unlock()

    if p.callback != nil {
        p.callback(&DebugEvent{
            Type:      DebugEventTCPConnected,
            Timestamp: t,
            IP:        ip,
            Port:      portNum,
        })
    }
}

func (p *DebugEventParser) handleTCPFailed(t time.Time, reason string) {
    atomic.AddInt64(&p.tcpFailureCount, 1)

    normalizedReason := "error"
    if strings.Contains(strings.ToLower(reason), "refused") {
        atomic.AddInt64(&p.tcpRefusedCount, 1)
        normalizedReason = "refused"
    } else if strings.Contains(strings.ToLower(reason), "timeout") ||
              strings.Contains(strings.ToLower(reason), "timed out") {
        atomic.AddInt64(&p.tcpTimeoutCount, 1)
        normalizedReason = "timeout"
    }

    // Clear pending TCP state
    p.mu.Lock()
    p.pendingTCP = nil
    p.mu.Unlock()

    if p.callback != nil {
        p.callback(&DebugEvent{
            Type:       DebugEventTCPFailed,
            Timestamp:  t,
            FailReason: normalizedReason,
        })
    }
}

func (p *DebugEventParser) handlePlaylistOpen(t time.Time, url string) {
    atomic.AddInt64(&p.playlistRefreshes, 1)

    p.mu.Lock()
    if !p.lastPlaylistRefresh.IsZero() {
        actual := t.Sub(p.lastPlaylistRefresh)
        jitter := actual - p.targetDuration
        if jitter < 0 {
            jitter = -jitter
        }

        atomic.AddInt64(&p.playlistJitterSum, int64(jitter))

        // Update max jitter
        for {
            current := atomic.LoadInt64(&p.playlistMaxJitter)
            if int64(jitter) <= current {
                break
            }
            if atomic.CompareAndSwapInt64(&p.playlistMaxJitter, current, int64(jitter)) {
                break
            }
        }

        // Late refresh: >150% of target
        if actual > p.targetDuration*3/2 {
            atomic.AddInt64(&p.playlistLateCount, 1)
        }
    }
    p.lastPlaylistRefresh = t
    p.mu.Unlock()

    if p.callback != nil {
        p.callback(&DebugEvent{
            Type:      DebugEventPlaylistOpen,
            Timestamp: t,
            URL:       url,
        })
    }
}

func (p *DebugEventParser) handleSequenceChange(t time.Time, oldSeq, newSeq int) {
    skipped := newSeq - oldSeq - 1
    if skipped > 0 {
        atomic.AddInt64(&p.sequenceSkips, int64(skipped))
    }

    if p.callback != nil {
        p.callback(&DebugEvent{
            Type:      DebugEventSequenceChange,
            Timestamp: t,
            OldSeq:    oldSeq,
            NewSeq:    newSeq,
        })
    }
}

// recordTTFB adds a TTFB sample (ring buffer).
func (p *DebugEventParser) recordTTFB(ttfb time.Duration) {
    atomic.AddInt64(&p.ttfbCount, 1)

    // Ring buffer (protected by mu, already held by caller)
    if len(p.ttfbSamples) >= 1000 {
        copy(p.ttfbSamples, p.ttfbSamples[1:])
        p.ttfbSamples[len(p.ttfbSamples)-1] = ttfb
    } else {
        p.ttfbSamples = append(p.ttfbSamples, ttfb)
    }
}

// DebugStats holds debug parser statistics.
type DebugStats struct {
    // TTFB metrics
    TTFBCount     int64
    TTFBP50       time.Duration
    TTFBP95       time.Duration
    TTFBP99       time.Duration
    TTFBMax       time.Duration

    // Playlist metrics
    PlaylistRefreshes  int64
    PlaylistLateCount  int64
    PlaylistJitterAvg  time.Duration
    PlaylistMaxJitter  time.Duration

    // TCP health metrics
    TCPSuccessCount  int64
    TCPFailureCount  int64
    TCPTimeoutCount  int64
    TCPRefusedCount  int64
    TCPHealthRatio   float64  // 0.0 to 1.0

    // Sequence
    SequenceSkips    int64

    // Parser health
    LinesProcessed   int64
}

// Stats returns current debug statistics.
func (p *DebugEventParser) Stats() DebugStats {
    p.mu.Lock()
    defer p.mu.Unlock()

    stats := DebugStats{
        TTFBCount:         atomic.LoadInt64(&p.ttfbCount),
        PlaylistRefreshes: atomic.LoadInt64(&p.playlistRefreshes),
        PlaylistLateCount: atomic.LoadInt64(&p.playlistLateCount),
        PlaylistMaxJitter: time.Duration(atomic.LoadInt64(&p.playlistMaxJitter)),
        TCPSuccessCount:   atomic.LoadInt64(&p.tcpSuccessCount),
        TCPFailureCount:   atomic.LoadInt64(&p.tcpFailureCount),
        TCPTimeoutCount:   atomic.LoadInt64(&p.tcpTimeoutCount),
        TCPRefusedCount:   atomic.LoadInt64(&p.tcpRefusedCount),
        SequenceSkips:     atomic.LoadInt64(&p.sequenceSkips),
        LinesProcessed:    atomic.LoadInt64(&p.linesProcessed),
    }

    // Calculate TTFB percentiles
    if len(p.ttfbSamples) > 0 {
        sorted := make([]time.Duration, len(p.ttfbSamples))
        copy(sorted, p.ttfbSamples)
        sortDurations(sorted)

        stats.TTFBP50 = sorted[len(sorted)*50/100]
        stats.TTFBP95 = sorted[len(sorted)*95/100]
        stats.TTFBP99 = sorted[len(sorted)*99/100]
        stats.TTFBMax = sorted[len(sorted)-1]
    }

    // Calculate playlist jitter avg
    if stats.PlaylistRefreshes > 1 {
        stats.PlaylistJitterAvg = time.Duration(
            atomic.LoadInt64(&p.playlistJitterSum) / (stats.PlaylistRefreshes - 1))
    }

    // Calculate TCP health ratio
    total := stats.TCPSuccessCount + stats.TCPFailureCount
    if total > 0 {
        stats.TCPHealthRatio = float64(stats.TCPSuccessCount) / float64(total)
    } else {
        stats.TCPHealthRatio = 1.0
    }

    return stats
}

// sortDurations sorts a slice of durations in place.
func sortDurations(d []time.Duration) {
    // Simple insertion sort for small slices
    for i := 1; i < len(d); i++ {
        for j := i; j > 0 && d[j] < d[j-1]; j-- {
            d[j], d[j-1] = d[j-1], d[j]
        }
    }
}
```

**Estimated lines**: ~400

### 6.2 Create `internal/parser/debug_events_test.go`

**File**: `internal/parser/debug_events_test.go` (NEW)

```go
package parser

import (
    "testing"
    "time"
)

func TestDebugEventParser_TTFB(t *testing.T) {
    tests := []struct {
        name     string
        lines    []string
        wantTTFB bool
    }{
        {
            name: "complete_segment_fetch",
            lines: []string{
                "[hls @ 0x55f8] HLS request for url 'http://origin/seg001.ts', offset 0, playlist 0",
                "[tcp @ 0x55f8] Starting connection attempt to 10.177.0.10 port 17080",
                "[tcp @ 0x55f8] Successfully connected to 10.177.0.10 port 17080",
            },
            wantTTFB: true,
        },
        {
            name: "manifest_ignored",
            lines: []string{
                "[hls @ 0x55f8] HLS request for url 'http://origin/stream.m3u8', offset 0, playlist 0",
                "[tcp @ 0x55f8] Successfully connected to 10.177.0.10 port 17080",
            },
            wantTTFB: false,  // Only .ts files tracked
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            var ttfbRecorded bool
            p := NewDebugEventParser(1, 2*time.Second, func(e *DebugEvent) {
                if e.Type == DebugEventTCPConnected {
                    // TTFB was recorded
                }
            })

            for _, line := range tt.lines {
                p.ParseLine(line)
            }

            stats := p.Stats()
            ttfbRecorded = stats.TTFBCount > 0

            if ttfbRecorded != tt.wantTTFB {
                t.Errorf("TTFB recorded = %v, want %v", ttfbRecorded, tt.wantTTFB)
            }
        })
    }
}

func TestDebugEventParser_PlaylistJitter(t *testing.T) {
    p := NewDebugEventParser(1, 2*time.Second, nil)

    // Simulate playlist refreshes
    lines := []string{
        "[hls @ 0x55f8] Opening 'http://origin/stream.m3u8' for reading",
    }

    p.ParseLine(lines[0])
    time.Sleep(2100 * time.Millisecond)  // 100ms late
    p.ParseLine(lines[0])

    stats := p.Stats()
    if stats.PlaylistRefreshes != 2 {
        t.Errorf("PlaylistRefreshes = %d, want 2", stats.PlaylistRefreshes)
    }
    // Jitter should be ~100ms
    if stats.PlaylistJitterAvg < 50*time.Millisecond {
        t.Errorf("PlaylistJitterAvg = %v, want >50ms", stats.PlaylistJitterAvg)
    }
}

func TestDebugEventParser_TCPHealth(t *testing.T) {
    p := NewDebugEventParser(1, 2*time.Second, nil)

    // 9 successes, 1 failure
    for i := 0; i < 9; i++ {
        p.ParseLine("[tcp @ 0x55f8] Successfully connected to 10.177.0.10 port 17080")
    }
    p.ParseLine("[tcp @ 0x55f8] Connection refused")

    stats := p.Stats()
    if stats.TCPSuccessCount != 9 {
        t.Errorf("TCPSuccessCount = %d, want 9", stats.TCPSuccessCount)
    }
    if stats.TCPRefusedCount != 1 {
        t.Errorf("TCPRefusedCount = %d, want 1", stats.TCPRefusedCount)
    }
    // Health should be 90%
    if stats.TCPHealthRatio < 0.89 || stats.TCPHealthRatio > 0.91 {
        t.Errorf("TCPHealthRatio = %f, want ~0.9", stats.TCPHealthRatio)
    }
}

func TestDebugEventParser_SequenceSkip(t *testing.T) {
    p := NewDebugEventParser(1, 2*time.Second, nil)

    // Skip 5 segments (3433 -> 3439 means 5 skipped)
    p.ParseLine("[hls @ 0x55f8] Media sequence change (3433 -> 3439) reflected in first_timestamp: ...")

    stats := p.Stats()
    if stats.SequenceSkips != 5 {
        t.Errorf("SequenceSkips = %d, want 5", stats.SequenceSkips)
    }
}

func TestDebugEventParser_RealOutput(t *testing.T) {
    // Parse real FFmpeg output from testdata
    // This tests the full flow with actual log format
}

func BenchmarkDebugEventParser_ParseLine(b *testing.B) {
    p := NewDebugEventParser(1, 2*time.Second, nil)
    line := "[tcp @ 0x55c32c0d7800] Successfully connected to 10.177.0.10 port 17080"

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        p.ParseLine(line)
    }
}

func BenchmarkDebugEventParser_NonMatchingLine(b *testing.B) {
    p := NewDebugEventParser(1, 2*time.Second, nil)
    line := "frame=47878 fps= 68 q=-1.0 size=N/A time=00:00:15.93 bitrate=N/A speed=2.28x"

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        p.ParseLine(line)
    }
}
```

**Estimated lines**: ~150

### 6.3 Update `testdata/ffmpeg_debug_output.txt`

**File**: `testdata/ffmpeg_debug_output.txt` (UPDATE)

Ensure the test fixture includes all parseable patterns:

```
# FFmpeg Debug Output Sample
# Version: ffmpeg version 8.0
# Date: 2026-01-22
# Command: ffmpeg -hide_banner -loglevel debug -progress unix://... -i http://origin/stream.m3u8 ...

# === Segment Download Wall Time (PRIMARY METRIC) ===
# Reliable: doesn't depend on TCP connect events
[hls @ 0x55c32c0c5700] Opening 'http://10.177.0.10:17080/seg03440.ts' for reading
# ... download happens (possibly over reused keep-alive connection) ...
[hls @ 0x55c32c0c5700] Opening 'http://10.177.0.10:17080/seg03441.ts' for reading  ← seg03440 ends here
# Wall Time = timestamp(seg03441 open) - timestamp(seg03440 open)

# === TCP Connect Latency (SECONDARY - only for NEW connections) ===
# WARNING: With keep-alive, most segments will NOT have TCP connect events!
[tcp @ 0x55c32c0d7800] Starting connection attempt to 10.177.0.10 port 17080
[tcp @ 0x55c32c0d7800] Successfully connected to 10.177.0.10 port 17080
# TCP Connect Latency = 2ms (only measures TCP handshake, NOT "TTFB")
# Does NOT include: TLS handshake, HTTP request, server processing, first byte

# === TCP Failures ===
[tcp @ 0x55c32c0d7800] Connection refused
[tcp @ 0x55c32c0d7800] Connection timed out
[tcp @ 0x55c32c0d7800] Failed to connect

# === Playlist Refresh ===
[hls @ 0x55c32c0c5700] Opening 'http://10.177.0.10:17080/stream.m3u8' for reading
[hls @ 0x55c32c0c5700] Skip ('#EXT-X-VERSION:3')

# === Sequence Change (5 segments skipped) ===
[hls @ 0x55c32c0c5700] Media sequence change (3433 -> 3439) reflected in first_timestamp: 6881421333 -> 6901421333

# === Non-matching lines (should be fast-path ignored) ===
frame=47878 fps= 68 q=-1.0 size=N/A time=00:00:15.93 bitrate=N/A speed=2.28x
[h264 @ 0x55c32c0e5440] nal_unit_type: 9(AUD), nal_ref_idc: 0
```

### 6.4 Update `internal/stats/client_stats.go`

**File**: `internal/stats/client_stats.go`
**Changes**: Add fields for debug metrics

```go
// Add to ClientStats struct:
// Debug parser metrics (Phase 6)
debugParser      *parser.DebugEventParser
debugEnabled     bool

// Add methods:
func (s *ClientStats) SetDebugParser(p *parser.DebugEventParser)
func (s *ClientStats) GetDebugStats() *parser.DebugStats
```

### 6.5 Update `internal/stats/aggregator.go`

**File**: `internal/stats/aggregator.go`
**Changes**: Aggregate debug metrics across clients

```go
// Add to AggregatedStats:
// Debug metrics (Phase 6 - requires -ffmpeg-debug flag)
TTFBP50           time.Duration
TTFBP95           time.Duration
TTFBP99           time.Duration
TCPHealthRatio    float64
PlaylistJitterAvg time.Duration
PlaylistLateCount int64
SequenceSkips     int64
```

### 6.6 Update TUI for Debug Metrics

**File**: `internal/tui/view.go`
**Changes**: Add Network Health panel

```go
func (m Model) renderNetworkHealth() string {
    if !m.debugEnabled {
        return dimStyle.Render("Network metrics require -ffmpeg-debug flag")
    }

    // Render TTFB, TCP health, playlist jitter
    // See design doc section 11.6 for layout
}
```

### 6.7 Create `internal/stats/qoe.go` (QoE Metrics)

**File**: `internal/stats/qoe.go` (NEW)

Implements Quality of Experience metrics from design doc §12:

```go
package stats

import (
    "math"
    "sync"
    "sync/atomic"
    "time"
)

// PlaybackHealth tracks buffer state using out_time vs wall clock.
type PlaybackHealth struct {
    startTime        time.Time
    currentOutTimeUS int64

    mu               sync.RWMutex
    healthScore      float64
    healthHistory    []float64  // Ring buffer for trending
    stallingEvents   int64

    isCurrentlyStalling bool
    stallStartTime      time.Time
    totalStallDuration  time.Duration
}

// NewPlaybackHealth creates a new health tracker.
func NewPlaybackHealth() *PlaybackHealth {
    return &PlaybackHealth{
        startTime:     time.Now(),
        healthHistory: make([]float64, 0, 100),
    }
}

// UpdateFromProgress updates health from FFmpeg progress.
// Formula: Health = out_time_us / (wall_time × 1,000,000)
func (h *PlaybackHealth) UpdateFromProgress(outTimeUS int64) {
    h.mu.Lock()
    defer h.mu.Unlock()

    elapsed := time.Since(h.startTime)
    elapsedUS := float64(elapsed.Microseconds())

    if elapsedUS > 0 {
        h.healthScore = float64(outTimeUS) / elapsedUS
        h.currentOutTimeUS = outTimeUS
    }

    // Track stalling (score < 0.9 for extended period)
    if h.healthScore < 0.9 && !h.isCurrentlyStalling {
        h.isCurrentlyStalling = true
        h.stallStartTime = time.Now()
        h.stallingEvents++
    } else if h.healthScore >= 0.95 && h.isCurrentlyStalling {
        h.isCurrentlyStalling = false
        h.totalStallDuration += time.Since(h.stallStartTime)
    }

    // Record sample for trending
    h.recordSample(h.healthScore)
}

// Score returns current health score.
func (h *PlaybackHealth) Score() float64 {
    h.mu.RLock()
    defer h.mu.RUnlock()
    return h.healthScore
}

// Status returns human-readable status.
func (h *PlaybackHealth) Status() string {
    score := h.Score()
    switch {
    case score >= 0.98:
        return "Perfect"
    case score >= 0.95:
        return "Healthy"
    case score >= 0.9:
        return "Marginal"
    case score > 0:
        return "Stalling"
    default:
        return "Unknown"
    }
}

// Trend returns health trend (slope over recent samples).
// Negative = degrading, Positive = improving
func (h *PlaybackHealth) Trend() float64 {
    h.mu.RLock()
    defer h.mu.RUnlock()

    if len(h.healthHistory) < 10 {
        return 0
    }
    return calculateSlope(h.healthHistory[len(h.healthHistory)-10:])
}

// StallingEvents returns total stalling event count.
func (h *PlaybackHealth) StallingEvents() int64 {
    return atomic.LoadInt64(&h.stallingEvents)
}

// TotalStallDuration returns cumulative stall time.
func (h *PlaybackHealth) TotalStallDuration() time.Duration {
    h.mu.RLock()
    defer h.mu.RUnlock()

    total := h.totalStallDuration
    if h.isCurrentlyStalling {
        total += time.Since(h.stallStartTime)
    }
    return total
}

func (h *PlaybackHealth) recordSample(score float64) {
    if len(h.healthHistory) >= 100 {
        copy(h.healthHistory, h.healthHistory[1:])
        h.healthHistory[len(h.healthHistory)-1] = score
    } else {
        h.healthHistory = append(h.healthHistory, score)
    }
}

// DownloadEfficiency compares actual bitrate to manifest BANDWIDTH.
//
// DECISION: manifestBandwidth is obtained from FFmpeg debug output.
// When FFmpeg opens an HLS stream with -loglevel debug, it logs:
//   [hls @ 0x...] Stream variant found: BANDWIDTH=2000000, RESOLUTION=1280x720
//
// This is parsed by DebugEventParser and passed to DownloadEfficiency.
// See: DebugEventParser.GetManifestBandwidth()
type DownloadEfficiency struct {
    manifestBandwidth atomic.Int64  // bits per second (from manifest, set dynamically)

    mu              sync.RWMutex
    prevTotalSize   int64
    prevTime        time.Time
    actualBitrate   float64  // bits per second
    efficiency      float64  // percentage (100 = perfect)
}

// NewDownloadEfficiency creates efficiency tracker.
// manifestBandwidth can be 0 initially - will be set when parsed from debug output.
func NewDownloadEfficiency() *DownloadEfficiency {
    return &DownloadEfficiency{}
}

// SetManifestBandwidth sets the bandwidth from the parsed manifest.
// Called by DebugEventParser when BANDWIDTH is found in debug output.
func (e *DownloadEfficiency) SetManifestBandwidth(bps int64) {
    e.manifestBandwidth.Store(bps)
}

// UpdateFromProgress calculates efficiency from total_size delta.
func (e *DownloadEfficiency) UpdateFromProgress(totalSize int64) {
    now := time.Now()

    e.mu.Lock()
    defer e.mu.Unlock()

    if !e.prevTime.IsZero() && totalSize > e.prevTotalSize {
        deltaBytes := totalSize - e.prevTotalSize
        deltaSec := now.Sub(e.prevTime).Seconds()

        if deltaSec > 0 {
            e.actualBitrate = float64(deltaBytes*8) / deltaSec

            manifestBW := e.manifestBandwidth.Load()
            if manifestBW > 0 {
                e.efficiency = (e.actualBitrate / float64(manifestBW)) * 100
            }
        }
    }

    e.prevTotalSize = totalSize
    e.prevTime = now
}

// Efficiency returns current efficiency percentage.
func (e *DownloadEfficiency) Efficiency() float64 {
    e.mu.RLock()
    defer e.mu.RUnlock()
    return e.efficiency
}

// ActualBitrate returns current download rate in bps.
func (e *DownloadEfficiency) ActualBitrate() float64 {
    e.mu.RLock()
    defer e.mu.RUnlock()
    return e.actualBitrate
}

// ContinuityState tracks DTS errors and corruption.
type ContinuityState struct {
    dtsErrors      int64
    corruptPackets int64

    mu             sync.Mutex
    recentErrors   []time.Time  // For rate calculation
}

// NewContinuityState creates continuity tracker.
func NewContinuityState() *ContinuityState {
    return &ContinuityState{
        recentErrors: make([]time.Time, 0, 100),
    }
}

// RecordDTSError records a DTS discontinuity.
func (c *ContinuityState) RecordDTSError() {
    atomic.AddInt64(&c.dtsErrors, 1)

    c.mu.Lock()
    c.recentErrors = append(c.recentErrors, time.Now())
    // Keep only last 100 for rate calculation
    if len(c.recentErrors) > 100 {
        c.recentErrors = c.recentErrors[1:]
    }
    c.mu.Unlock()
}

// RecordCorruptPacket records a discarded packet.
func (c *ContinuityState) RecordCorruptPacket() {
    atomic.AddInt64(&c.corruptPackets, 1)
}

// DTSErrors returns total DTS error count.
func (c *ContinuityState) DTSErrors() int64 {
    return atomic.LoadInt64(&c.dtsErrors)
}

// ErrorRate returns DTS errors per minute (recent window).
func (c *ContinuityState) ErrorRate() float64 {
    c.mu.Lock()
    defer c.mu.Unlock()

    cutoff := time.Now().Add(-60 * time.Second)
    count := 0
    for _, t := range c.recentErrors {
        if t.After(cutoff) {
            count++
        }
    }
    return float64(count)
}

// QoEScore represents composite quality score.
type QoEScore struct {
    PlaybackHealthScore int  // 0-100
    EfficiencyScore     int  // 0-100
    ContinuityScore     int  // 0-100
    OverallScore        int  // 0-100 weighted
    Rating              string
}

// CalculateQoE computes composite QoE from components.
func CalculateQoE(health *PlaybackHealth, efficiency *DownloadEfficiency, continuity *ContinuityState) QoEScore {
    qoe := QoEScore{}

    // Playback Health (40% weight)
    qoe.PlaybackHealthScore = int(math.Min(100, health.Score()*100))

    // Download Efficiency (30% weight)
    qoe.EfficiencyScore = int(math.Min(100, efficiency.Efficiency()))

    // Continuity (30% weight) - 0 errors = 100, 10+/min = 0
    errorRate := continuity.ErrorRate()
    qoe.ContinuityScore = int(math.Max(0, 100-errorRate*10))

    // Weighted average
    qoe.OverallScore = (qoe.PlaybackHealthScore*40 +
        qoe.EfficiencyScore*30 +
        qoe.ContinuityScore*30) / 100

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

// calculateSlope computes linear regression slope.
func calculateSlope(samples []float64) float64 {
    n := float64(len(samples))
    if n < 2 {
        return 0
    }

    var sumX, sumY, sumXY, sumX2 float64
    for i, y := range samples {
        x := float64(i)
        sumX += x
        sumY += y
        sumXY += x * y
        sumX2 += x * x
    }

    denom := n*sumX2 - sumX*sumX
    if denom == 0 {
        return 0
    }

    return (n*sumXY - sumX*sumY) / denom
}
```

**Estimated lines**: ~250

### 6.8 Create `internal/stats/qoe_test.go`

**File**: `internal/stats/qoe_test.go` (NEW)

```go
func TestPlaybackHealth_Score(t *testing.T) {
    tests := []struct {
        name       string
        outTimeUS  int64
        elapsed    time.Duration
        wantScore  float64
        wantStatus string
    }{
        {"perfect", 5_000_000, 5 * time.Second, 1.0, "Perfect"},
        {"stalling", 4_000_000, 5 * time.Second, 0.8, "Stalling"},
        {"catching_up", 6_000_000, 5 * time.Second, 1.2, "Healthy"},
    }
    // ...
}

func TestDownloadEfficiency(t *testing.T) {
    // Test efficiency calculation
}

func TestQoEScore(t *testing.T) {
    // Test composite scoring
}

func BenchmarkPlaybackHealth_Update(b *testing.B) {
    // Performance test
}
```

**Estimated lines**: ~150

### 6.9 Add Continuity Parsing to HLSEventParser

**File**: `internal/parser/hls_events.go`
**Changes**: Add DTS error detection

```go
// Add regex
var reDTSError = regexp.MustCompile(`Non-monotonous DTS|DTS .* out of order`)
var reCorruptPacket = regexp.MustCompile(`Discarding (corrupted|invalid) packet`)

// Add to parseLine():
if reDTSError.MatchString(line) {
    atomic.AddInt64(&p.dtsErrors, 1)
    // ...
}
```

**Estimated changes**: +30 lines

### 6.10 Phase 6 Definition of Done (DoD)

Before marking Phase 6 complete, verify ALL items:

#### Segment Wall Time (PRIMARY METRIC)
- [ ] `SegmentTracker` tracks open → open timing
- [ ] Wall time recorded on segment completion
- [ ] Ring buffer stores last N samples for percentile calculation
- [ ] `hls_swarm_segment_download_seconds` histogram exposed
- [ ] TUI shows P50/P95/P99 segment download time

#### TCP Connect Latency (SECONDARY METRIC)
- [ ] `TCPConnectTracker` tracks connect start → success
- [ ] Only tracks NEW connections (not keep-alive reuse)
- [ ] `hls_swarm_tcp_connect_seconds` histogram exposed
- [ ] TUI shows "TCP Connects: N (avg Xms)" when connections observed

#### Playlist Jitter
- [ ] `PlaylistRefreshState` tracks refresh intervals
- [ ] Jitter calculated as |actual - expected|
- [ ] Late refresh threshold: >150% of target duration
- [ ] `hls_swarm_playlist_jitter_seconds` gauge exposed

#### TCP Health Ratio
- [ ] Success/failure/timeout counts tracked
- [ ] Health ratio = success / (success + failure + timeout)
- [ ] `hls_swarm_tcp_health_ratio` gauge exposed
- [ ] TUI shows connection health percentage

#### QoE Metrics
- [ ] Playback Health Score calculated: `out_time_us / wall_time`
- [ ] Download Efficiency calculated (if manifest BANDWIDTH available)
- [ ] Continuity Errors tracked (DTS discontinuities)
- [ ] Composite QoE Score (0-100) calculated
- [ ] TUI QoE panel displays all metrics

#### Test Coverage
- [ ] `TestDebugEventParser_SegmentWallTime` - timing calculation
- [ ] `TestDebugEventParser_TCPConnect` - connect tracking
- [ ] `TestDebugEventParser_PlaylistJitter` - jitter calculation
- [ ] `TestDebugEventParser_KeepAlive` - no false TCP events
- [ ] `TestQoE_PlaybackHealth` - score calculation
- [ ] `TestQoE_Efficiency` - efficiency calculation
- [ ] All benchmarks run under 1μs/op for parse operations
- [ ] All tests pass with `-race`

#### Documentation
- [ ] Metric reliability table in GoDoc (HIGH/MEDIUM/LOW)
- [ ] Clear naming: `segment_download` not "TTFB"
- [ ] Clear naming: `tcp_connect` not "latency"

---

## Phase 7: Testing & Benchmarks

### 7.1 Integration Test: `tests/integration/socket_test.go`

**File**: `tests/integration/socket_test.go` (NEW)

```go
//go:build integration

func TestIntegration_SocketProgress_SingleClient(t *testing.T) {
    // 1. Create orchestrator with socket mode
    // 2. Start 1 client
    // 3. Wait 10 seconds
    // 4. Verify progress received
    // 5. Verify socket cleaned up
}

func TestIntegration_SocketProgress_100Clients(t *testing.T) {
    // Scale test with 100 clients
}

func TestIntegration_SocketFallback(t *testing.T) {
    // Simulate socket creation failure
    // Verify graceful fallback to pipe
}
```

**Estimated lines**: ~150

### 7.2 Benchmark Test: `internal/parser/benchmarks_test.go`

**File**: `internal/parser/benchmarks_test.go` (NEW or extend existing)

```go
func BenchmarkSocketReader_vs_PipeReader(b *testing.B) {
    // Compare throughput
}

func BenchmarkParseKeyValue(b *testing.B) {
    // Verify efficient string parsing
}

func BenchmarkProgressParser_FullBlock(b *testing.B) {
    // Parse complete progress block
}

func BenchmarkDebugEventParser_Line(b *testing.B) {
    // Parse single debug line
}
```

**Estimated lines**: ~100

### 7.3 Race Detection

All tests should pass with `-race`:

```bash
go test -race ./internal/parser/...
go test -race ./internal/supervisor/...
go test -race ./internal/orchestrator/...
```

### 7.4 Phase 7 Definition of Done (DoD)

Before marking Phase 7 complete, verify ALL items:

#### Integration Tests
- [ ] `TestIntegration_SocketProgress` - real FFmpeg with socket
- [ ] `TestIntegration_PipeProgress` - real FFmpeg with pipe
- [ ] `TestIntegration_SocketToPipeFallback` - fallback behavior
- [ ] `TestIntegration_100Clients` - scale test with both modes

#### Benchmarks Pass Performance Targets
| Benchmark | Target | Measured |
|-----------|--------|----------|
| `BenchmarkSocketReader_Throughput` | >1M lines/sec | _______ |
| `BenchmarkProgressParser_ParseLine` | <500ns/op | _______ |
| `BenchmarkDebugEventParser_Line` | <1μs/op | _______ |
| `BenchmarkSocket_vs_Pipe` | Socket ≤ Pipe | _______ |

#### Race Detection
- [ ] `go test -race ./internal/parser/...` passes
- [ ] `go test -race ./internal/supervisor/...` passes
- [ ] `go test -race ./internal/orchestrator/...` passes
- [ ] `go test -race ./internal/stats/...` passes

#### Load Test Verification
- [ ] 100-client test with socket mode completes
- [ ] No stale socket files in `/tmp` after test
- [ ] Drop rate ≤ pipe mode
- [ ] Memory usage stable (no leaks)

---

## Phase 8: Documentation & Cleanup

### 8.1 Update Documentation

| Document | Updates |
|----------|---------|
| `docs/FFMPEG_HLS_REFERENCE.md` | Add §14: Socket Mode |
| `docs/METRICS_ENHANCEMENT_DESIGN.md` | Reference socket design |
| `docs/TUI_DEFECTS.md` | Update Defect E, G status |
| `README.md` | Add -progress-socket flag |

### 8.2 Update Makefile

```makefile
# Add new targets:
test-socket:
	go test -v ./internal/parser/... -run Socket

test-socket-integration:
	go test -v -tags=integration ./tests/integration/... -run Socket

bench-socket:
	go test -bench=Socket -benchmem ./internal/parser/...
```

### 8.3 Code Cleanup

- Remove any TODO comments for socket implementation
- Update code comments to reference this implementation
- Ensure consistent error messages

### 8.4 Phase 8 Definition of Done (DoD)

Before marking Phase 8 (and entire implementation) complete:

#### Documentation
- [ ] `docs/FFMPEG_HLS_REFERENCE.md` updated with Socket Mode section
- [ ] `docs/METRICS_ENHANCEMENT_DESIGN.md` references socket design
- [ ] `docs/TUI_DEFECTS.md` Defect E marked FIXED
- [ ] `README.md` documents `--progress-socket` flag
- [ ] All exported functions have GoDoc comments
- [ ] Invariants (I1-I4) documented in code comments

#### Makefile
- [ ] `make test-socket` target added
- [ ] `make test-socket-integration` target added
- [ ] `make bench-socket` target added

#### Code Quality
- [ ] No TODO comments remaining for socket implementation
- [ ] `go vet ./...` passes
- [ ] `golint ./...` passes (or reasonable exceptions documented)
- [ ] `go mod tidy` run

#### Final Verification
- [ ] All Phase 1-7 DoDs completed
- [ ] 300-client load test passes with socket mode
- [ ] No regression from pipe mode performance
- [ ] Metrics dashboard shows all new metrics correctly

---

## Risk Mitigation Checklist

### Critical Invariants (Must Verify)

| # | Invariant | Test | Risk if Violated |
|---|-----------|------|------------------|
| **I1** | Pipeline channel closed on every Run() exit | `TestSocketReader_NoGoroutineLeak` | Goroutine leaks, flaky `-race` tests |
| **I2** | Socket path ≤104 bytes | `TestSocketReader_PathTooLong` | Silent failures in containers |
| **I3** | Ready() before FFmpeg starts | `TestSupervisor_SocketReadyBeforeFFmpeg` | Intermittent "connection refused" |
| **I4** | Pipe & socket use same termination | Code review | Inconsistent behavior |

### Before Implementation

- [ ] Review FFmpeg documentation for `unix://` URL support
- [ ] Test FFmpeg unix socket on target platforms (Linux, macOS)
- [ ] Verify `/tmp` permissions in all deployment environments
- [ ] Document rollback procedure
- [ ] **Verify TMPDIR length** in container environments (max 104 - socket filename length)

### During Implementation

- [ ] Implement feature flag first (default off)
- [ ] Add comprehensive logging for socket events
- [ ] Test socket cleanup on SIGKILL (process killed)
- [ ] Test with 300 clients before merging
- [ ] **Add `sync.Once` to CloseChannel()** to prevent double-close panic
- [ ] **Add Ready() channel** to SocketReader for synchronization
- [ ] **Verify socket path validation** rejects paths > 104 bytes

### FFmpeg Capability Detection

**⚠️ Warning**: The naive probe `ffmpeg -progress unix:///dev/null -version` is **unreliable** because:
- `-version` exits immediately
- FFmpeg may not actually attempt to open the progress URL

**Correct approach**: Use runtime fallback as primary mechanism:

```go
// If socket mode fails to receive connection within 3 seconds,
// assume FFmpeg doesn't support unix:// and fall back to pipe for this client.
const socketConnectGrace = 3 * time.Second
```

**Optional**: Real minimal probe (for capability caching):
```bash
# Synthetic minimal run that forces progress writer initialization
ffmpeg -f lavfi -i anullsrc=r=44100:cl=mono -t 0.1 \
       -progress unix:///tmp/probe_$$.sock \
       -f null - 2>/dev/null
```

### Invariant Tests (Add to Test Suite)

| Test | Purpose |
|------|---------|
| `TestSocketReader_NoGoroutineLeak` | Verify I1: no leaked goroutines |
| `TestSocketReader_PathTooLong` | Verify I2: rejects long paths |
| `TestSocketReader_PathExactlyAtLimit` | Verify I2: accepts 104-byte paths |
| `TestSocketReader_ReadyBeforeAccept` | Verify I3: Ready() closed before Accept() |
| `TestSupervisor_SocketReadyBeforeFFmpeg` | Verify I3: order enforced |
| `TestSupervisor_ClientRestartStress` | Verify I3: 100 rapid restarts |
| `TestPipeline_SymmetricTermination` | Verify I4: both modes close channel |

### After Implementation

- [ ] Run full test suite with `-race`
- [ ] **Run goroutine leak tests** 100x to catch flakes
- [ ] Compare drop rates: socket vs pipe
- [ ] Compare latency P99: socket vs pipe
- [ ] Monitor for stale socket files in /tmp
- [ ] Document any platform-specific issues
- [ ] **Test with long TMPDIR** (simulate container paths)

---

## Implementation Order Summary

| Phase | Files | Est. Lines | Dependencies |
|-------|-------|------------|--------------|
| **1** | `socket_reader.go`, `*_windows.go`, `*_test.go` | 475 | None |
| **2** | `pipeline.go`, `pipeline_test.go` | 85 | Phase 1 |
| **3** | `supervisor.go`, `supervisor_test.go` | 250 | Phase 1, 2 |
| **4** | `ffmpeg.go`, `ffmpeg_test.go` | 80 | None |
| **5** | `config.go`, `flags.go`, `client_manager.go`, `orchestrator.go`, `main.go` | 75 | Phase 3, 4 |
| **6** | `debug_events.go`, `qoe.go`, `hls_events.go`, tests, `view.go` | 1,080 | Phase 2 |
| **7** | Integration tests, benchmarks | 250 | Phase 1-6 |
| **8** | Documentation, Makefile | N/A | All |
| **Total** | | ~2,295 lines | |

---

## Estimated Timeline

| Phase | Duration | Notes |
|-------|----------|-------|
| Phase 1 | 2-3 hours | Core socket infrastructure |
| Phase 2 | 30 min | Small pipeline changes |
| Phase 3 | 2 hours | Most complex supervisor changes |
| Phase 4 | 1 hour | Straightforward FFmpeg config |
| Phase 5 | 1 hour | Wiring and flags |
| Phase 6 | 5-6 hours | **Network + QoE metrics** (TTFB, Jitter, TCP, Buffer, Efficiency, DTS) |
| Phase 7 | 2 hours | Testing and validation |
| Phase 8 | 1 hour | Documentation |
| **Total** | **~16 hours** | Can be split across sessions |

---

## Phase 6 Metrics Summary

Phase 6 implements **two categories** of high-value metrics:

### A. Network Metrics (from `-loglevel debug`)

| Metric | Source Pattern | Reliability | Value |
|--------|---------------|-------------|-------|
| **Segment Wall Time** | `Opening seg.ts` → next `Opening seg.ts` | ✅ HIGH | Actual download duration |
| **TCP Connect Latency** | `Starting connection` → `Successfully connected` | ⚠️ MEDIUM | TCP handshake only (keep-alive skips) |
| **Playlist Jitter** | Time between `[hls @ ...] Opening '*.m3u8'` events | ✅ HIGH | Early warning of stream failure |
| **TCP Health Ratio** | `Successfully connected` vs `Connection refused/timed out` | ✅ HIGH | Origin stress indicator |

### B. Quality of Experience (QoE) Metrics (from progress + stderr)

| Metric | Formula/Source | Value |
|--------|---------------|-------|
| **Playback Health** | `out_time_us / (wall_time × 1,000,000)` | Buffer state (1.0 = realtime) |
| **Download Efficiency** | `actual_bitrate / manifest_BANDWIDTH × 100` | Network headroom |
| **Continuity Score** | `Non-monotonous DTS` errors in stderr | Origin encoder quality |
| **Composite QoE** | Weighted: 40% health + 30% efficiency + 30% continuity | 0-100 score |

### TUI Panel: Network Health + QoE

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ Network Health (requires -ffmpeg-debug)                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│ Segment Download (wall time)          │ TCP Connections                     │
│   P50:    45ms                        │   New conns:        127             │
│   P95:   120ms                        │   Avg connect:    2.1ms             │
│   P99:   250ms                        │   (most reuse keep-alive)           │
│   Count: 14,523                       │                                     │
│                                       │ Connection Health                   │
│ Note: Wall time = open→open           │   Success:     14,523 (99.2%)       │
│ (includes segment generation time     │   Refused:         48 (0.3%)        │
│  for live HLS streams)                │   Timeout:         73 (0.5%)        │
├─────────────────────────────────────────────────────────────────────────────┤
│ Quality of Experience (QoE)                                                 │
├─────────────────────────────────────────────────────────────────────────────┤
│ Playback Health                       │ Download Efficiency                 │
│   Score:      0.98x ● Healthy         │   Manifest:  2.0 Mbps               │
│   Trend:      ↗ improving             │   Actual:    1.8 Mbps               │
│   Stalls:     3 (4.2s total)          │   Efficiency: 90% ● Good            │
│                                       │                                     │
│ Stream Continuity                     │ Overall QoE Score                   │
│   DTS Errors: 12 (0.2/min)            │                                     │
│   Status:     ● Minor Issues          │   ████████░░ 85/100 Good            │
└─────────────────────────────────────────────────────────────────────────────┘
QoE Distribution: 250 Excellent | 40 Good | 8 Fair | 2 Poor (out of 300 clients)
```

### Early Warning Thresholds

| Metric | ⚠️ Warning | 🔴 Critical | Indicates |
|--------|-----------|------------|-----------|
| TCP Health Ratio | <99% | <95% | Origin overload |
| TTFB P99 | >500ms | >2s | Network congestion |
| Playlist Max Jitter | >targetDuration | >2×targetDuration | Playlist delivery failing |
| **Playback Health** | <0.95 | <0.90 | Client buffering |
| **Download Efficiency** | <90% | <70% | Insufficient bandwidth |
| **DTS Error Rate** | >1/min | >5/min | Origin encoder issues |
| **QoE Score** | <75 | <50 | Poor viewer experience |

---

## Test Validation Checklist

Before marking complete:

```bash
# Unit tests
go test ./internal/parser/... -v
go test ./internal/supervisor/... -v
go test ./internal/process/... -v

# Race detection
go test -race ./...

# Integration (requires FFmpeg)
go test -tags=integration ./tests/integration/...

# Benchmarks
go test -bench=. -benchmem ./internal/parser/...

# Load test comparison
# 1. Without socket (baseline)
./go-ffmpeg-hls-swarm -clients 100 -duration 60s http://origin/stream.m3u8
# Record: drop rate, latency P99, total bytes

# 2. With socket
./go-ffmpeg-hls-swarm -clients 100 -duration 60s -progress-socket http://origin/stream.m3u8
# Compare: drop rate should be ≤ baseline
```
