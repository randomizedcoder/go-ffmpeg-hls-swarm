# Architecture Design

> **Type**: Contributor Documentation

High-level architecture of go-ffmpeg-hls-swarm.

---

## Overview

go-ffmpeg-hls-swarm is a load testing tool that orchestrates a swarm of FFmpeg processes to stress-test HLS infrastructure.

```
┌─────────────────────────────────────────────────────────────────┐
│                         main.go                                  │
│                    (CLI entry point)                            │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                       Orchestrator                               │
│                 (coordinates all components)                    │
│                                                                 │
│  ┌─────────────┐  ┌──────────────┐  ┌─────────────────────┐    │
│  │ Supervisor  │  │ Stats        │  │ Metrics Collector   │    │
│  │ (processes) │  │ Aggregator   │  │ (Prometheus export) │    │
│  └─────────────┘  └──────────────┘  └─────────────────────┘    │
│         │                │                    │                 │
└─────────┼────────────────┼────────────────────┼─────────────────┘
          │                │                    │
          ▼                ▼                    ▼
┌─────────────┐    ┌──────────────┐    ┌─────────────────┐
│  FFmpeg     │    │   Parser     │    │   TUI           │
│  Processes  │    │   Pipeline   │    │   Dashboard     │
└─────────────┘    └──────────────┘    └─────────────────┘
```

---

## Core Principles

### 1. Process-Based Simulation

Unlike protocol-level load testers, go-ffmpeg-hls-swarm uses real FFmpeg processes:

- **Realistic**: Actual HTTP requests, protocol handling
- **Accurate**: Real TCP connections, timing
- **Scalable**: 100s of concurrent processes

### 2. Separation of Concerns

Each package has a single responsibility:

| Package | Responsibility |
|---------|----------------|
| `orchestrator` | High-level coordination, lifecycle |
| `supervisor` | Process management, restart logic |
| `process` | FFmpeg command generation |
| `parser` | FFmpeg output parsing |
| `stats` | Aggregation and calculation |
| `metrics` | Prometheus export |
| `tui` | Terminal dashboard |

### 3. Non-Blocking Design

- Channel-based communication
- Bounded buffers prevent backpressure
- Graceful degradation under load

---

## Package Overview

### cmd/go-ffmpeg-hls-swarm

Entry point. Parses flags, creates orchestrator, runs.

```go
func main() {
    os.Exit(run())
}

func run() int {
    cfg, err := config.ParseFlags()
    orch := orchestrator.New(cfg, logger)
    return orch.Run(ctx)
}
```

### internal/config

Configuration management.

- `config.go` - Config struct, defaults
- `flags.go` - Flag parsing
- `validate.go` - Validation rules

### internal/orchestrator

Coordinates all components.

- Manages supervisor lifecycle
- Aggregates stats from all clients
- Updates metrics collector
- Runs TUI if enabled

Key methods:
- `Run(ctx)` - Main loop
- `aggregateStats()` - Collect from clients
- `handleShutdown()` - Graceful termination

### internal/supervisor

Manages individual FFmpeg processes.

- Spawns processes with ramp-up
- Monitors health (stall detection)
- Handles restarts
- Reports stats back to orchestrator

Key concepts:
- Client lifecycle: `starting` → `running` → `stalled` → `stopped`
- Bounded restart policy (backoff)
- Per-client stat collection

### internal/process

FFmpeg command generation.

- `Runner` interface - process abstraction
- `FFmpegRunner` - FFmpeg implementation
- Command building with all options
- Probe integration

### internal/parser

FFmpeg output parsing.

- `progress.go` - Parse `-progress` output (frame, speed, time)
- `stderr.go` - Parse stderr (opens, errors, HTTP codes)
- Event emission for downstream consumption

### internal/stats

Statistics aggregation.

- Per-client stats collection
- Aggregate calculations (P50, P95, P99)
- Throughput tracking
- Time-series windows

### internal/metrics

Prometheus metrics.

- Tier 1: Aggregate metrics (always enabled)
- Tier 2: Per-client metrics (optional)
- HTTP endpoint for scraping

### internal/tui

Terminal dashboard.

- Real-time display (bubbletea)
- Origin metrics integration
- Keyboard controls

---

## Data Flow

### FFmpeg Output → Metrics

```
FFmpeg Process
    │
    ├── stdout (pipe:3) → Progress Parser → speed, frame, time
    │
    └── stderr → Stderr Parser → opens, errors, bytes
                      │
                      ▼
              Per-Client Stats
                      │
                      ▼
              Stats Aggregator
                      │
                      ▼
              Metrics Collector
                      │
                      ▼
              Prometheus /metrics
```

### Stats Collection Loop

```go
// Every 200ms
for {
    select {
    case <-ticker.C:
        // 1. Collect from all clients
        stats := supervisor.CollectStats()

        // 2. Aggregate
        agg := aggregate(stats)

        // 3. Update metrics
        collector.RecordStats(agg)

        // 4. Update TUI (if enabled)
        tui.Update(agg)
    }
}
```

---

## Concurrency Model

### Goroutines

| Goroutine | Purpose |
|-----------|---------|
| Main | Orchestrator loop |
| Supervisor | Process watcher per client |
| Progress parser | Parse progress output |
| Stderr parser | Parse stderr output |
| Metrics server | HTTP server |
| TUI | Terminal rendering |

### Channels

| Channel | Direction | Purpose |
|---------|-----------|---------|
| `statsC` | Supervisor → Orchestrator | Stats updates |
| `eventsC` | Parser → Supervisor | Parsed events |
| `shutdownC` | Orchestrator → All | Shutdown signal |

### Synchronization

- `sync.atomic` for counters (lock-free)
- `sync.Mutex` for complex state
- `sync.WaitGroup` for shutdown coordination

---

## Extension Points

### Custom Process Runner

Implement `process.Runner` interface:

```go
type Runner interface {
    BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error)
    Name() string
}
```

### Custom Metrics

Add to `metrics/collector.go`:

```go
var myMetric = prometheus.NewCounter(...)

func init() {
    prometheus.MustRegister(myMetric)
}
```

### Custom Parser

Implement parser interface:

```go
type Parser interface {
    Parse(line string) (Event, bool)
}
```

---

## Performance Considerations

### Memory

- Bounded buffers (configurable)
- Ring buffers for time series
- Efficient event structures

### CPU

- Minimal parsing overhead
- Batch stats collection
- Lazy calculation

### Network

- Connection reuse in FFmpeg
- Efficient Prometheus encoding
- Optional per-client metrics

---

## Related Documents

- [PACKAGE_STRUCTURE.md](./PACKAGE_STRUCTURE.md) - Package details
- [PROCESS_SUPERVISION.md](./PROCESS_SUPERVISION.md) - Supervision model
- [MEMORY_MODEL.md](./MEMORY_MODEL.md) - Memory management
