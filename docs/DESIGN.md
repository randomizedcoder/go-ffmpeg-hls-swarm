# HLS Live Load Client Orchestrator — Design Document

> **Status**: Draft (Design Phase)
> **Language**: Go
> **Binary**: `go-ffmpeg-hls-swarm`
> **Dependencies**: FFmpeg (external binary)

---

## About This Document

This is the **main design document** for contributors. For users, start with [README.md](../README.md) or [QUICKSTART.md](QUICKSTART.md).

> For the complete documentation map, see [README.md](../README.md#documentation).

---

## Table of Contents

- [Overview](#overview)
- [1. Goals](#1-goals)
- [2. Non-Goals](#2-non-goals)
- [3. Assumptions](#3-assumptions)
- [4. Architecture](#4-architecture)
- [5. Library Design](#5-library-design)
- [6. FFmpeg Execution Model](#6-ffmpeg-execution-model)
- [7. Implementation Plan](#7-implementation-plan)
- [8. Testing Strategy](#8-testing-strategy)
- [9. Future Enhancements](#9-future-enhancements)
- [Appendix: Quick Start](#appendix-quick-start)

---

## Overview

A **prototype** load testing tool that generates 50–200+ concurrent HLS live stream clients using FFmpeg subprocesses. Each client fetches playlists and segments without decoding, exercising CDN/origin infrastructure to find capacity limits.

**Design philosophy**: Keep options open. The core orchestration logic is generic—while FFmpeg is the initial process runner, the library design supports swapping in other tools (curl loops, custom binaries, etc.) without major refactoring.

---

## 1. Goals

- Generate N concurrent live HLS clients against a single stream URL
- Use FFmpeg's HLS implementation for maximum compatibility across manifest styles
- **No decode**: exercise playlist + segment fetching + container parsing only
- Controlled ramp-up to avoid thundering herd
- Robust process supervision: restarts with exponential backoff, clean shutdown
- Graceful signal handling: propagate `SIGTERM`/`SIGINT` to child processes
- Observability: Prometheus metrics + structured logs
- CLI-driven configuration for rapid iteration
- **Generic library design**: core orchestration decoupled from FFmpeg specifics
- **DNS override**: connect to specific IPs to test particular servers/edges
- **Cache bypass**: no-cache headers to stress origin servers directly

## 2. Non-Goals

- Accurate QoE modeling (buffer fullness, ABR heuristics)
- Embedding FFmpeg libraries (no FFI/cgo bindings)
- Per-segment byte-accurate accounting (best-effort from FFmpeg logs)
- Network impairment simulation (use external tools: `tc`, `netem`)
- Hot configuration reload
- Multi-instance coordination
- Session/cookie state management
- Token refresh or dynamic URL resolution
- **Fine-grained segment buffering control** — FFmpeg fetches segments as fast as possible (see [OPERATIONS.md](OPERATIONS.md#6-segment-buffering-limitations))

## 3. Assumptions

- Target is **live HLS** (sliding window playlists, periodic refresh)
- FFmpeg build includes: HTTP/HTTPS protocols, TLS, HLS demuxer
- Single stream URL per run (all clients target same endpoint)
- Runs on Linux with tunable process/FD limits
- FFmpeg auto-selects rendition (typically highest quality)
- **Memory efficient at scale**: Linux shares FFmpeg code across processes (~72% memory savings vs naive expectation—see [MEMORY.md](MEMORY.md))

---

## 4. Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         Orchestrator                            │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────┐  │
│  │ CLI / Config │  │ Ramp Scheduler│  │ Metrics Server       │  │
│  │   Parser     │  │              │  │ (Prometheus /metrics)│  │
│  └──────────────┘  └──────────────┘  └──────────────────────┘  │
│                           │                                     │
│                           ▼                                     │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │                   Client Manager                          │  │
│  │  ┌─────────┐ ┌─────────┐ ┌─────────┐       ┌─────────┐   │  │
│  │  │Client 0 │ │Client 1 │ │Client 2 │  ...  │Client N │   │  │
│  │  │Supervisor│ │Supervisor│ │Supervisor│     │Supervisor│   │  │
│  │  └────┬────┘ └────┬────┘ └────┬────┘       └────┬────┘   │  │
│  └───────┼───────────┼───────────┼─────────────────┼────────┘  │
└──────────┼───────────┼───────────┼─────────────────┼───────────┘
           │           │           │                 │
           ▼           ▼           ▼                 ▼
       ┌───────┐   ┌───────┐   ┌───────┐         ┌───────┐
       │Process│   │Process│   │Process│   ...   │Process│
       │(ffmpeg)│  │(ffmpeg)│  │(ffmpeg)│        │(ffmpeg)│
       └───────┘   └───────┘   └───────┘         └───────┘
```

### Components

| Component | Responsibility |
|-----------|----------------|
| **CLI Parser** | Parse flags, validate inputs, build runtime config |
| **Ramp Scheduler** | Control client start rate, apply per-client jitter |
| **Client Manager** | Track all client supervisors, coordinate shutdown |
| **Client Supervisor** | Spawn process, monitor health, restart on failure |
| **Metrics Server** | Expose Prometheus metrics at `/metrics` |

### Data Flow

1. CLI parsed → Config built → Preflight checks run
2. Orchestrator starts metrics server
3. Ramp scheduler starts clients at configured rate (with per-client jitter)
4. Each Client Supervisor spawns subprocess via `ProcessRunner`
5. Supervisor reads FFmpeg `-progress` output via non-blocking reader (drops data if consumer lags—see [SUPERVISION.md § 5.3](SUPERVISION.md#53-non-blocking-progress-reader-critical))
6. Progress data collected via fan-in pattern; `sync.Pool` reduces GC pressure at scale
7. On process exit: restart with backoff (unless shutdown)
8. On `SIGTERM`/`SIGINT`: Client Manager uses waitgroup to ensure all Supervisors have killed their process groups before orchestrator exits
9. Print exit summary with P95 uptime and throughput stats

### Package Structure

```
go-ffmpeg-hls-swarm/
├── cmd/
│   └── go-ffmpeg-hls-swarm/          # CLI binary
│       └── main.go
├── internal/
│   ├── config/                       # Configuration parsing
│   ├── orchestrator/                 # Core orchestration logic
│   │   ├── orchestrator.go
│   │   ├── client_manager.go
│   │   └── ramp_scheduler.go
│   ├── supervisor/                   # Process supervision
│   │   ├── supervisor.go
│   │   ├── backoff.go
│   │   └── liveness.go               # Progress-based stall detection
│   ├── process/                      # Process runner abstraction
│   │   ├── runner.go                 # Interface definition
│   │   ├── ffmpeg.go                 # FFmpeg implementation
│   │   └── progress.go               # FFmpeg -progress protocol parser
│   ├── metrics/                      # Prometheus metrics
│   ├── logging/                      # Structured logging
│   └── preflight/                    # Startup checks (FDs, ports, DNS)
├── docs/
│   ├── DESIGN.md                     # This file
│   ├── CONFIGURATION.md
│   ├── SUPERVISION.md
│   ├── OBSERVABILITY.md
│   ├── OPERATIONS.md
│   ├── SECURITY.md
│   ├── FFMPEG_HLS_REFERENCE.md
│   ├── NIX_FLAKE_DESIGN.md
│   └── MEMORY.md
├── README.md
├── CONTRIBUTING.md
├── go.mod
└── go.sum
```

---

## 5. Library Design

The core orchestration is **generic**. FFmpeg-specific logic is isolated behind interfaces.

### Core Interfaces

```go
// process/runner.go

// ProcessRunner creates runnable process configurations.
// Implementations: FFmpegRunner, CurlRunner, CustomRunner, etc.
type ProcessRunner interface {
    // BuildCommand returns the command and args for a client.
    BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error)

    // Name returns a human-readable name for this runner type.
    Name() string
}

// ProcessResult captures the outcome of a process execution.
type ProcessResult struct {
    ClientID  int
    ExitCode  int
    StartTime time.Time
    EndTime   time.Time
    Err       error
}

// OutputHandler processes stdout/stderr from running processes.
type OutputHandler interface {
    HandleOutput(clientID int, stream string, line string)
}
```

```go
// supervisor/supervisor.go

// Supervisor manages a single client's process lifecycle.
type Supervisor struct {
    clientID      int
    runner        ProcessRunner
    outputHandler OutputHandler
    backoff       *Backoff
    metrics       *metrics.Collector
}

// Run starts the supervision loop. Blocks until context cancelled.
func (s *Supervisor) Run(ctx context.Context) error
```

```go
// orchestrator/client_manager.go

// ClientManager coordinates multiple supervisors.
type ClientManager struct {
    runner        ProcessRunner
    supervisors   map[int]*Supervisor
    metrics       *metrics.Collector
}

// StartClient launches a new supervised client.
func (m *ClientManager) StartClient(ctx context.Context, clientID int) error

// Shutdown gracefully stops all clients.
func (m *ClientManager) Shutdown(timeout time.Duration) error
```

### FFmpeg Implementation

```go
// process/ffmpeg.go

type VariantSelection string

const (
    VariantAll     VariantSelection = "all"     // -map 0 (all variants)
    VariantHighest VariantSelection = "highest" // -map 0:p:{id} (via ffprobe)
    VariantLowest  VariantSelection = "lowest"  // -map 0:p:{id} (via ffprobe)
    VariantFirst   VariantSelection = "first"   // -map 0:v:0? -map 0:a:0?
)

type FFmpegConfig struct {
    BinaryPath         string
    StreamURL          string
    VariantSelection   VariantSelection
    UserAgent          string
    Timeout            time.Duration
    Reconnect          bool
    ReconnectDelayMax  int
    SegMaxRetry        int
    LogLevel           string
    ResolveIP          string   // DNS override (requires DangerousMode)
    DangerousMode      bool     // Required for ResolveIP
    NoCache            bool     // Add cache-busting headers
    ExtraHeaders       []string
    ProbeFailurePolicy string   // "fail" or "fallback"
}

func DefaultFFmpegConfig(streamURL string) *FFmpegConfig {
    return &FFmpegConfig{
        BinaryPath:         "ffmpeg",
        StreamURL:          streamURL,
        VariantSelection:   VariantAll,
        UserAgent:          "go-ffmpeg-hls-swarm/1.0",
        Timeout:            15 * time.Second,
        Reconnect:          true,
        ReconnectDelayMax:  5,
        SegMaxRetry:        3,
        LogLevel:           "info",
        ProbeFailurePolicy: "fallback",
    }
}

type FFmpegRunner struct {
    config *FFmpegConfig
}

func (r *FFmpegRunner) BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error)
func (r *FFmpegRunner) Name() string
func (r *FFmpegRunner) ProbeVariants(ctx context.Context) (int64, error)
```

**Extensibility**: To support a different process type, implement `ProcessRunner`:

```go
// Example: curl-based fetcher
type CurlRunner struct { URL string }

func (r *CurlRunner) BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error) {
    return exec.CommandContext(ctx, "curl", "-s", "-o", "/dev/null", r.URL), nil
}
```

---

## 6. FFmpeg Execution Model

> **Detailed reference**: See [FFMPEG_HLS_REFERENCE.md](FFMPEG_HLS_REFERENCE.md)

### Command Structure

```bash
ffmpeg [global opts] [input opts] -i <input> [output opts] <output>
```

**Position matters**: Options like `-reconnect` must come **before** `-i`.

### Standard Command (Load Testing)

```bash
ffmpeg \
  -hide_banner -nostdin -loglevel info \
  -reconnect 1 -reconnect_streamed 1 -reconnect_on_network_error 1 \
  -reconnect_delay_max 5 \
  -rw_timeout 15000000 \
  -user_agent "go-ffmpeg-hls-swarm/1.0" \
  -seg_max_retry 3 \
  -i "https://example.com/live/master.m3u8" \
  -map 0 \
  -c copy \
  -f null \
  -
```

### Key Options

| Option | Purpose |
|--------|---------|
| `-reconnect 1` | Reconnect on disconnect |
| `-reconnect_streamed 1` | Reconnect for non-seekable streams |
| `-rw_timeout 15000000` | Network timeout (microseconds) |
| `-seg_max_retry 3` | Retry failed segment downloads |
| `-map 0` | Download all streams/variants |
| `-c copy` | No decode/encode (copy only) |
| `-f null -` | Discard output |

See [CONFIGURATION.md](CONFIGURATION.md#6-generated-ffmpeg-commands) for complete command examples with all options.

---

## 7. Implementation Plan

### Phase 1: MVP

- [ ] Project structure with `cmd/go-ffmpeg-hls-swarm/`
- [ ] Core interfaces (`ProcessRunner`, `Supervisor`)
- [ ] FFmpeg runner implementation
- [ ] CLI argument parsing (flags + URL)
- [ ] Basic restart with exponential backoff
- [ ] Signal handling (SIGTERM/SIGINT → graceful shutdown)
- [ ] Ramp-up scheduler with per-client jitter
- [ ] Client manager coordinating N supervisors
- [ ] Prometheus metrics endpoint
- [ ] Structured JSON logging
- [ ] Preflight checks (ulimit, ffmpeg)
- [ ] Exit summary report

**Deliverable**: Working load generator with CLI interface

### Phase 2: Polish

- [ ] YAML config file support
- [ ] FFmpeg stderr classification (error/warn parsing)
- [ ] `/healthz` endpoint
- [ ] Improved logging (log sampling, rate limiting)
- [ ] Liveness watchdog (stall detection)

### Phase 3: Advanced (Optional)

- [ ] FFmpeg verbose log parsing (segment counts, bytes)
- [ ] Multiple stream URLs support
- [ ] Alternative process runners
- [ ] Remote control endpoints (POST /stop)

---

## 8. Testing Strategy

### Unit Tests

Focus on **what could go wrong**:
- Backoff calculation edge cases
- Config validation
- URL rewriting for IP override
- Header construction

### Integration Tests

- Spawn single FFmpeg against test stream
- Verify restart on simulated failure (`kill -9`)
- Verify graceful shutdown propagates `SIGTERM`
- Verify metrics update correctly

### Load Tests

| Clients | Expected Behavior |
|---------|-------------------|
| 10 | Baseline, verify everything works |
| 50 | Moderate load, check resource usage |
| 100 | Significant load, monitor FDs/memory |
| 200 | Target max, verify stability |

---

## 9. Future Enhancements

### Commonly Requested

- **Multiple URL support** with weights
- **Load test phases**: Warmup → Steady → Cooldown with configurable durations
- **Controlled ramp-down**: Gradual client reduction (inverse of ramp-up) for graceful load shedding
- **`--print-cmd`** and **`--check`** modes
- **HTTP proxy support**
- **Phase offset / desynchronization**

### Advanced

- ABR simulation (switch renditions under load)
- Geographic distribution
- Real-time dashboard (Grafana templates)
- Byte-level transfer metrics
- Latency measurement

See [CONFIGURATION.md](CONFIGURATION.md) for current CLI flags and examples.

---

## Appendix: Quick Start

```bash
# Build
go build -o go-ffmpeg-hls-swarm ./cmd/go-ffmpeg-hls-swarm

# Basic test (5 clients)
./go-ffmpeg-hls-swarm -clients 5 https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8

# Stress test (all variants)
./go-ffmpeg-hls-swarm -clients 100 -variant all https://cdn.example.com/live/master.m3u8

# Bypass cache
./go-ffmpeg-hls-swarm -clients 50 -no-cache https://cdn.example.com/live/master.m3u8

# Test specific server (⚠️ disables TLS)
./go-ffmpeg-hls-swarm -clients 50 -resolve 192.168.1.100 --dangerous \
  https://cdn.example.com/live/master.m3u8

# Monitor metrics
curl http://localhost:9090/metrics | grep hlsswarm
```

See [CONFIGURATION.md](CONFIGURATION.md) for comprehensive usage examples.
