# Process Supervision & Lifecycle

> **Type**: Contributor Documentation
> **Related**: [DESIGN.md](DESIGN.md), [OBSERVABILITY.md](OBSERVABILITY.md)

This document specifies how `go-ffmpeg-hls-swarm` supervises FFmpeg processes: ramp-up strategy, restart policies, signal handling, and graceful shutdown.

---

## Table of Contents

- [1. Ramp-up Strategy](#1-ramp-up-strategy)
  - [1.1 Algorithm](#11-algorithm)
  - [1.2 Per-Client Seeded Jitter](#12-per-client-seeded-jitter-critical)
  - [1.3 Avoiding Playlist Refresh Synchronization](#13-avoiding-playlist-refresh-synchronization)
- [2. Client States](#2-client-states)
- [3. Restart Policy](#3-restart-policy)
  - [3.1 Exit Code Handling](#31-exit-code-handling)
  - [3.2 Backoff Calculation](#32-backoff-calculation)
  - [3.3 Backoff Reset Rules](#33-backoff-reset-rules)
  - [3.4 Maximum Restarts](#34-maximum-restarts-optional)
  - [3.5 Stop Conditions](#35-stop-conditions)
- [4. Signal Handling & Graceful Shutdown](#4-signal-handling--graceful-shutdown)
  - [4.1 Process Group Handling](#41-process-group-handling)
  - [4.2 Signal Semantics](#42-signal-semantics)
  - [4.3 Shutdown Sequence](#43-shutdown-sequence)
  - [4.4 Implementation](#44-implementation)
- [5. Client Health & Stall Detection](#5-client-health--stall-detection)
  - [5.1 MVP: Best-Effort Health](#51-mvp-best-effort-health)
  - [5.2 Phase 2: Liveness Watchdog](#52-phase-2-liveness-watchdog)
  - [5.3 Phase 3: OS-Level Checks](#53-phase-3-os-level-checks-optional)

---

## 1. Ramp-up Strategy

### 1.1 Algorithm

```go
for i := 0; i < targetClients; i++ {
    wait(1/rampRate seconds)
    jitter := clientJitter(i, rampJitter)  // Per-client seeded randomness
    sleep(jitter)
    startClient(i)
}

// clientJitter returns deterministic jitter for a client ID.
// Uses client ID as seed for stable, reproducible randomness.
func clientJitter(clientID int, maxJitter time.Duration) time.Duration {
    // Seed with client ID for reproducibility across runs
    rng := rand.New(rand.NewSource(int64(clientID) ^ configSeed))
    return time.Duration(rng.Int63n(int64(maxJitter)))
}
```

### 1.2 Per-Client Seeded Jitter (Critical)

**Problem**: Global `rand.Seed(time.Now())` causes all clients to have correlated randomness. If many clients restart simultaneously (e.g., origin hiccup), they reconverge even with jitter.

**Solution**: Seed jitter per client ID:
- Each client gets deterministic but different jitter
- Survives restarts — client 42 always gets the same jitter offset
- Combine client ID with a config-level seed for variation across test runs

```go
// config/jitter.go

type JitterSource struct {
    configSeed int64  // From CLI flag or random at startup
}

func (j *JitterSource) ForClient(clientID int) *rand.Rand {
    seed := int64(clientID) ^ j.configSeed
    return rand.New(rand.NewSource(seed))
}
```

### 1.3 Avoiding Playlist Refresh Synchronization

HLS playlists typically refresh every 2-6 seconds. If clients start at exactly `ramp-rate` per second, they can align on refresh intervals.

**Mitigations:**
1. **Per-client jitter** (above) spreads initial starts
2. **Restart jitter** uses same per-client seed, maintaining spread
3. **Future**: "Phase offset" option to artificially delay some clients' first playlist fetch

### Example Timeline

With `-clients 20 -ramp-rate 5 -ramp-jitter 200ms`:

```
t=0.0s:   Start clients 0-4 (with per-client jitter: 0→45ms, 1→189ms, 2→72ms, ...)
t=1.0s:   Start clients 5-9 (different jitter values per client)
t=2.0s:   Start clients 10-14
t=3.0s:   Start clients 15-19
t=3.2s:   All clients running (approximately)
```

---

## 2. Client States

```
┌─────────┐     start      ┌─────────┐
│ Created │ ──────────────▶│ Starting│
└─────────┘                └────┬────┘
                               │ process spawned
                               ▼
┌─────────┐    exit/crash  ┌─────────┐
│ Backoff │◀───────────────│ Running │
└────┬────┘                └─────────┘
     │ delay elapsed            │
     │                          │ shutdown signal
     ▼                          ▼
┌─────────┐                ┌─────────┐
│ Starting│                │ Stopped │
└─────────┘                └─────────┘
```

---

## 3. Restart Policy

### 3.1 Exit Code Handling

| Exit Code | Meaning | Action | Reset Backoff? |
|-----------|---------|--------|----------------|
| 0 | Clean exit (VOD ended, stream stopped) | Restart | Yes |
| 1 | Generic error | Restart with backoff | No |
| 137 | SIGKILL | Restart with backoff | No |
| 143 | SIGTERM (graceful) | Restart unless shutting down | No |
| Any other | Unexpected | Restart with backoff | No |

**All exits trigger restart** unless global shutdown is in progress.

> ⚠️ **Implementation note**: Exit code 0 from FFmpeg does **not** imply "successful completion" for live streams. It indicates stream end or graceful termination (e.g., `#EXT-X-ENDLIST` encountered, duration reached). For load testing purposes, exit code 0 is treated identically to other exits: trigger restart. Do not optimize this away.

### 3.2 Backoff Calculation

```go
delay := min(backoffMax, backoffInitial * pow(backoffMultiplier, attempts))
delay += delay * clientJitter(clientID, delay * 0.4)  // ±20% per-client jitter
```

Default values:
- `backoffInitial`: 250ms
- `backoffMax`: 5s
- `backoffMultiplier`: 1.7

### 3.3 Backoff Reset Rules

**When to reset `attempts` to 0:**

| Condition | Reset? | Rationale |
|-----------|--------|-----------|
| Client ran for > 30 seconds | ✅ Yes | Successful run indicates transient issue resolved |
| Clean exit (code 0) | ✅ Yes | Expected termination, not a failure |
| Exit code 1 after < 5 seconds | ❌ No | Likely persistent error |
| SIGTERM during shutdown | N/A | No restart attempted |

```go
// supervisor/backoff.go

const (
    BackoffResetThreshold = 30 * time.Second  // Reset after this much uptime
)

func (s *Supervisor) shouldResetBackoff(uptime time.Duration, exitCode int) bool {
    if uptime >= BackoffResetThreshold {
        return true
    }
    if exitCode == 0 {
        return true
    }
    return false
}
```

### 3.4 Maximum Restarts (Optional)

By default, restarts are unlimited (goal: find infrastructure limits).

Optional limit via config:
```go
type RestartPolicy struct {
    MaxRestarts int  // 0 = unlimited (default)
    // When limit reached: log warning, stop supervisor, keep others running
}
```

When max restarts reached for a client:
- Log: `{"level":"warn","msg":"max_restarts_reached","client_id":42,"restarts":100}`
- Client enters `Stopped` state permanently
- Other clients continue running
- Metric: `hlsswarm_clients_max_restarts_reached_total` incremented

### 3.5 Stop Conditions

1. Global context cancelled (`SIGINT`/`SIGTERM`)
2. Duration elapsed (if configured)
3. Max restarts reached (per-client, if configured)
4. **Not a stop condition**: Individual client failures (keep others running)

---

## 4. Signal Handling & Graceful Shutdown

**Critical requirement**: Clean shutdown with signal propagation.

### 4.1 Process Group Handling

FFmpeg may spawn helper processes (especially with certain codecs/protocols). To ensure complete cleanup:

```go
// process/ffmpeg.go

func (r *FFmpegRunner) BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error) {
    // ... build args ...

    cmd := exec.CommandContext(ctx, binaryPath, args...)

    // Start FFmpeg in its own process group
    // This allows us to signal the entire subtree
    cmd.SysProcAttr = &syscall.SysProcAttr{
        Setpgid: true,  // Create new process group
    }

    return cmd, nil
}

// killProcessGroup sends a signal to the entire process group
func killProcessGroup(pid int, sig syscall.Signal) error {
    // Negative PID signals the process group
    return syscall.Kill(-pid, sig)
}
```

### 4.1.1 Orphan Process Prevention (PR_SET_PDEATHSIG)

**Problem**: If the orchestrator crashes unexpectedly (OOM kill, `kill -9`, panic), the FFmpeg child processes become orphans and continue running indefinitely.

**Solution**: Use Linux's `PR_SET_PDEATHSIG` to automatically signal child processes when their parent dies:

```go
// process/ffmpeg.go

func (r *FFmpegRunner) BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error) {
    // ... build args ...

    cmd := exec.CommandContext(ctx, binaryPath, args...)

    cmd.SysProcAttr = &syscall.SysProcAttr{
        Setpgid:   true,           // Create new process group
        Pdeathsig: syscall.SIGKILL, // Kill child if parent dies
    }

    return cmd, nil
}
```

**How it works:**
1. When FFmpeg starts, `PR_SET_PDEATHSIG` tells the kernel to send `SIGKILL` to FFmpeg if its parent (go-ffmpeg-hls-swarm) exits
2. This works even if go-ffmpeg-hls-swarm is killed with `kill -9`
3. Prevents zombie FFmpeg processes accumulating after crashes

**Caveats:**
- Linux-only (darwin/BSD have different mechanisms)
- Only protects against parent death, not grandparent
- Use `SIGKILL` not `SIGTERM` since we can't guarantee cleanup code runs

```go
// Build for non-Linux systems
// +build !linux

func (r *FFmpegRunner) BuildCommand(...) (*exec.Cmd, error) {
    cmd.SysProcAttr = &syscall.SysProcAttr{
        Setpgid: true,
        // Pdeathsig not available on non-Linux
    }
    return cmd, nil
}
```

### 4.2 Signal Semantics

**Problem**: Go's `exec.CommandContext` sends `SIGKILL` when context is cancelled. This is too aggressive — we want graceful `SIGTERM` first.

**Solution**: Don't rely on context cancellation for process termination. Use manual signal handling:

```go
// supervisor/supervisor.go

type Supervisor struct {
    cmd     *exec.Cmd
    stopped chan struct{}  // Closed when we want to stop
}

func (s *Supervisor) Run(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            return s.gracefulStop()
        case <-s.stopped:
            return s.gracefulStop()
        default:
            s.runOnce(ctx)
        }
    }
}

func (s *Supervisor) gracefulStop() error {
    if s.cmd == nil || s.cmd.Process == nil {
        return nil
    }

    pid := s.cmd.Process.Pid

    // Step 1: SIGTERM to process group
    if err := killProcessGroup(pid, syscall.SIGTERM); err != nil {
        // Process might already be dead
        if !errors.Is(err, syscall.ESRCH) {
            log.Warn("SIGTERM failed", "pid", pid, "error", err)
        }
    }

    // Step 2: Wait up to 5 seconds
    done := make(chan error, 1)
    go func() { done <- s.cmd.Wait() }()

    select {
    case <-done:
        return nil  // Clean exit
    case <-time.After(5 * time.Second):
        // Step 3: SIGKILL
        log.Warn("process did not exit after SIGTERM, sending SIGKILL", "pid", pid)
        killProcessGroup(pid, syscall.SIGKILL)
        <-done
        return nil
    }
}
```

### 4.3 Shutdown Sequence

```
1. Receive SIGTERM or SIGINT
2. Log: "shutdown initiated"
3. Cancel global context (stops ramp scheduler, prevents new clients)
4. For each running process (in parallel):
   a. Send SIGTERM to process group (-pid)
   b. Wait up to 5 seconds for exit
   c. If still running, send SIGKILL to process group
5. Collect final metrics
6. Print exit summary (see OBSERVABILITY.md)
7. Exit with code 0
```

### 4.4 Implementation

```go
func (m *ClientManager) Shutdown(timeout time.Duration) error {
    ctx, cancel := context.WithTimeout(context.Background(), timeout)
    defer cancel()

    var wg sync.WaitGroup
    for id, sup := range m.supervisors {
        wg.Add(1)
        go func(id int, sup *Supervisor) {
            defer wg.Done()
            if err := sup.gracefulStop(); err != nil {
                log.Warn("client stop failed", "client_id", id, "error", err)
            }
        }(id, sup)
    }

    done := make(chan struct{})
    go func() {
        wg.Wait()
        close(done)
    }()

    select {
    case <-done:
        return nil
    case <-ctx.Done():
        // Force kill any remaining
        for _, sup := range m.supervisors {
            sup.forceKill()
        }
        return fmt.Errorf("shutdown timeout: forced kill on remaining processes")
    }
}
```

---

## 5. Client Health & Stall Detection

**Problem**: "Clients are running" doesn't mean "clients are generating load." FFmpeg can appear alive while:
- Stuck retrying a failed segment
- Waiting on DNS resolution
- Blocked behind a proxy
- Connected but server is slow-dripping data (1 byte every 10 seconds won't trigger `-rw_timeout`)

`-rw_timeout` is a socket-level timeout. It helps bound IO waits, but **doesn't prove forward progress**.

### 5.1 MVP Health Definition (Canonical)

**A client is considered "making progress" if `total_size` (bytes downloaded) increases within the stall threshold.**

> ⚠️ **Why `total_size` is the only reliable signal**: In HLS, FFmpeg can appear healthy while waiting for playlist updates. If the origin returns a valid playlist that hasn't advanced `#EXT-X-MEDIA-SEQUENCE`, FFmpeg enters a "wait and retry" loop—low CPU, active sockets, but no actual download progress. Only `total_size` distinguishes "waiting for live edge" (healthy) from "server stuck" (stalled).

**Stall threshold**: `2 × target_duration` (default: ~12-20 seconds for typical HLS streams)

This accounts for:
- Normal playlist refresh intervals (1× target_duration)
- Network jitter and CDN propagation delays
- Legitimate "waiting for next segment" pauses

**Fallback signals** (use only if `-progress` unavailable):

| Signal | Reliability | Notes |
|--------|-------------|-------|
| `out_time_us` increases | Medium | May lag behind `total_size` |
| `progress=continue` received | Low | Only indicates FFmpeg is running, not downloading |
| Recent stderr output | Low | Requires verbose mode; format varies |

**A client is considered "stalled" if `total_size` is static for > 2× target_duration.**

This definition is authoritative for implementation. Do not optimize or simplify without updating this specification.

### 5.1.1 Best-Effort Signals (Reference)

| Signal | How Detected | Reliability |
|--------|--------------|-------------|
| Process exists | `cmd.Process != nil` | High (but not useful alone) |
| Recent stderr | `time.Since(lastStderr) < threshold` | Medium (verbose mode only) |
| Exit code | Process terminated | High (but reactive) |

**MVP approach**: Consider a client "healthy" if the process exists. Accept that silent stalls won't be detected until timeout.

### 5.2 Progress-Based Liveness Watchdog (Recommended)

**Best approach**: Use FFmpeg's `-progress` flag instead of stderr parsing. See [FFMPEG_HLS_REFERENCE.md](FFMPEG_HLS_REFERENCE.md#10-progress-protocol-for-metrics).

Each supervisor tracks progress output:

```go
// supervisor/health.go

type ProgressLiveness struct {
    LastTotalSize    int64         // Last observed total_size (canonical signal)
    LastSizeChange   time.Time     // When total_size last increased
    TargetDuration   time.Duration // From playlist #EXT-X-TARGETDURATION (default: 6s)
    StallMultiplier  float64       // Stall after N × target_duration (default: 2.0)
}

func NewProgressLiveness(targetDuration time.Duration) *ProgressLiveness {
    if targetDuration == 0 {
        targetDuration = 6 * time.Second // HLS default
    }
    return &ProgressLiveness{
        LastSizeChange:  time.Now(),
        TargetDuration:  targetDuration,
        StallMultiplier: 2.0,
    }
}

func (p *ProgressLiveness) Update(totalSize int64) {
    if totalSize > p.LastTotalSize {
        p.LastTotalSize = totalSize
        p.LastSizeChange = time.Now()
    }
    // Note: totalSize staying the same is NOT an update
    // FFmpeg waiting for playlist refresh is expected behavior
}

func (p *ProgressLiveness) StallThreshold() time.Duration {
    return time.Duration(float64(p.TargetDuration) * p.StallMultiplier)
}

func (p *ProgressLiveness) IsStalled() bool {
    return time.Since(p.LastSizeChange) > p.StallThreshold()
}

func (p *ProgressLiveness) TimeSinceProgress() time.Duration {
    return time.Since(p.LastSizeChange)
}
```

**Why this design:**

| Aspect | Design Choice | Rationale |
|--------|---------------|-----------|
| Primary signal | `total_size` only | Only signal that proves bytes are flowing |
| Threshold | 2× target_duration | Allows for playlist refresh + network jitter |
| Configurable multiplier | `StallMultiplier` | Operators can tune for flaky origins |
| No `out_time_us` check | Removed from primary logic | Can lag; doesn't prove download activity |

**Why progress-based is better than stderr parsing:**

| Approach | CPU Cost | Catches Slow-Drip | Structured Data |
|----------|----------|-------------------|-----------------|
| Stderr regex | High | ❌ No | ❌ No |
| `-progress` protocol | Low | ✅ Yes | ✅ Yes |

**Stall detection logic:**

```go
// supervisor/supervisor.go

const (
    // Check more frequently than stall threshold to catch stalls promptly
    ProgressCheckInterval = 5 * time.Second
)

func (s *Supervisor) monitorProgress(ctx context.Context) {
    ticker := time.NewTicker(ProgressCheckInterval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            if s.liveness.IsStalled() {
                s.metrics.StalledTotal.Inc()
                s.logger.Warn("client_stalled",
                    "client_id", s.clientID,
                    "no_bytes_sec", s.liveness.TimeSinceProgress().Seconds(),
                    "stall_threshold_sec", s.liveness.StallThreshold().Seconds(),
                    "last_total_size", s.liveness.LastTotalSize,
                )
                // Optionally: kill and restart
                if s.config.RestartOnStall {
                    s.kill()
                }
            }
        }
    }
}
```

**Understanding the threshold:**

```
Timeline for 6s target_duration stream:
├── 0s: Segment N downloaded, total_size increases
├── 6s: Segment N+1 should arrive (1× target_duration)
├── 12s: Stall threshold hit (2× target_duration) ← ALERT
```

If the origin hasn't produced a new segment in 2× the expected interval, something is wrong—either the origin is stuck, or the CDN is serving stale playlists.

**Obtaining target_duration:**

| Method | Complexity | Accuracy |
|--------|------------|----------|
| CLI flag `-target-duration 6s` | Simple | Operator must know stream |
| Parse first playlist response | Medium | Automatic, requires HTTP client |
| Default to 6s (HLS spec default) | Trivial | Works for most streams |

For MVP, use a CLI flag with 6s default. Future enhancement: parse `#EXT-X-TARGETDURATION` from the playlist automatically.

**When liveness fails:**
1. Log warning: `{"msg":"client_stalled","client_id":42,"no_bytes_sec":14.2,"stall_threshold_sec":12}`
2. Increment metric: `hlsswarm_clients_stalled_total`
3. Optionally: kill and restart (configurable via `-restart-on-stall`)

### 5.3 Non-Blocking Progress Reader (Critical)

> ⚠️ **This section is critical for correctness at scale.** See [FFMPEG_HLS_REFERENCE.md](FFMPEG_HLS_REFERENCE.md#-critical-progress-pipe-blocking-risk) for background.

**The Problem**: FFmpeg's `-progress` writes are synchronous. If the orchestrator falls behind reading progress data, the OS pipe buffer fills, and FFmpeg's main loop **blocks**, stalling the actual download. This is a "Heisenbug" where measurement causes the failure.

**The Solution**: Non-blocking reader with drop semantics.

```go
// progress/reader.go

// ProgressData represents a single progress report from FFmpeg
type ProgressData struct {
    ClientID   int
    TotalSize  int64
    OutTimeUS  int64
    Speed      float64
    Progress   string // "continue" or "end"
}

// progressPool reduces GC pressure at scale (200+ clients)
var progressPool = sync.Pool{
    New: func() interface{} {
        return &ProgressData{}
    },
}

// NonBlockingReader reads FFmpeg progress without blocking the writer
type NonBlockingReader struct {
    pipe   io.Reader
    output chan *ProgressData
    done   chan struct{}
}

func NewNonBlockingReader(pipe io.Reader, bufferSize int) *NonBlockingReader {
    return &NonBlockingReader{
        pipe:   pipe,
        output: make(chan *ProgressData, bufferSize), // Default: 100
        done:   make(chan struct{}),
    }
}

func (r *NonBlockingReader) Start(clientID int) <-chan *ProgressData {
    go func() {
        defer close(r.output)
        scanner := bufio.NewScanner(r.pipe)

        data := progressPool.Get().(*ProgressData)
        data.ClientID = clientID

        for scanner.Scan() {
            line := scanner.Text()

            // Parse key=value pairs
            if strings.HasPrefix(line, "total_size=") {
                data.TotalSize, _ = strconv.ParseInt(line[11:], 10, 64)
            } else if strings.HasPrefix(line, "out_time_us=") {
                data.OutTimeUS, _ = strconv.ParseInt(line[12:], 10, 64)
            } else if strings.HasPrefix(line, "speed=") {
                data.Speed, _ = strconv.ParseFloat(strings.TrimSuffix(line[6:], "x"), 64)
            } else if strings.HasPrefix(line, "progress=") {
                data.Progress = line[9:]

                // End of progress block - try to send (non-blocking!)
                select {
                case r.output <- data:
                    // Sent successfully, get a fresh struct
                    data = progressPool.Get().(*ProgressData)
                    data.ClientID = clientID
                default:
                    // Channel full - drop this update, reuse struct
                    // This is acceptable: we only need eventual consistency
                }
            }
        }

        // Return the last struct to the pool
        progressPool.Put(data)
    }()

    return r.output
}
```

**Key design points:**

| Design Choice | Rationale |
|---------------|-----------|
| Buffered channel (size 100) | Absorbs bursts during CPU spikes |
| `select` with `default` | Never blocks on send; drops old data if full |
| `sync.Pool` for structs | Reduces GC pressure with 200+ concurrent readers |
| Pool reuse on drop | Dropped updates return struct to pool immediately |

**Why dropping is acceptable:**
- Progress updates arrive every ~500ms per client
- We only need to detect stalls (30s+ without progress)
- Missing one update doesn't affect stall detection
- Better to drop measurements than stall the measured process

**Fan-in pattern for central metrics collection:**

```go
// progress/collector.go

type ProgressCollector struct {
    inputs    []<-chan *ProgressData
    aggregate chan *ProgressData
}

func (c *ProgressCollector) Run(ctx context.Context) {
    var wg sync.WaitGroup
    for _, input := range c.inputs {
        wg.Add(1)
        go func(ch <-chan *ProgressData) {
            defer wg.Done()
            for {
                select {
                case <-ctx.Done():
                    return
                case data, ok := <-ch:
                    if !ok {
                        return
                    }
                    // Non-blocking send to aggregate
                    select {
                    case c.aggregate <- data:
                    default:
                        progressPool.Put(data) // Return to pool if dropped
                    }
                }
            }
        }(input)
    }
    wg.Wait()
}
```

### 5.4 Fallback: Stderr-Based Health (Alternative)

If `-progress` isn't available or reliable, fall back to stderr parsing:

```go
type StderrHealth struct {
    // At least one must be true within LivenessInterval
    PlaylistFetchObserved bool  // Saw "Opening '*.m3u8'" in stderr
    SegmentOpenObserved   bool  // Saw "Opening '*.ts'" in stderr
    RecentStderr          bool  // Any stderr in last N seconds
}

const LivenessInterval = 30 * time.Second

func (s *Supervisor) checkStderrLiveness() bool {
    h := s.stderrHealth
    return h.PlaylistFetchObserved || h.SegmentOpenObserved || h.RecentStderr
}
```

### 5.5 OS-Level Checks (Optional)

For maximum reliability without parsing logs:

| Check | How | Pros/Cons |
|-------|-----|-----------|
| CPU time increasing | `/proc/<pid>/stat` | Works without logs; noisy for idle streams |
| Socket bytes | `/proc/<pid>/net/tcp` | Direct measure; complex to implement |
| Open FD count | `/proc/<pid>/fd` | Indirect; FDs may stay open while stalled |

**Recommendation**: Use `-progress` protocol (5.2) with non-blocking reader (5.3). Fall back to stderr parsing (5.4) only if needed. OS-level checks are overkill for most use cases.
