# Package Structure

> **Type**: Contributor Documentation

Guide to the internal package structure of go-ffmpeg-hls-swarm.

---

## Directory Layout

```
go-ffmpeg-hls-swarm/
├── cmd/
│   └── go-ffmpeg-hls-swarm/
│       └── main.go           # CLI entry point
├── internal/
│   ├── config/               # Configuration management
│   ├── logging/              # Structured logging
│   ├── metrics/              # Prometheus metrics
│   ├── orchestrator/         # High-level coordination
│   ├── parser/               # FFmpeg output parsing
│   ├── preflight/            # Pre-run checks
│   ├── process/              # Process abstraction
│   ├── stats/                # Statistics aggregation
│   ├── supervisor/           # Process supervision
│   ├── timeseries/           # Time-series data structures
│   └── tui/                  # Terminal dashboard
├── nix/                      # Nix build system
├── scripts/                  # Shell scripts
├── docs/                     # Legacy documentation
└── documents/                # New documentation (you are here)
```

---

## Package Details

### internal/config

Configuration parsing and validation.

| File | Purpose |
|------|---------|
| `config.go` | Config struct, defaults |
| `flags.go` | CLI flag parsing |
| `validate.go` | Validation rules |

Key types:
```go
type Config struct {
    Clients    int
    RampRate   int
    StreamURL  string
    // ... 40+ fields
}
```

### internal/logging

Structured logging with slog.

| File | Purpose |
|------|---------|
| `logger.go` | Logger factory |

Usage:
```go
logger := logging.NewLogger("json", "info", verbose)
```

### internal/metrics

Prometheus metrics collection.

| File | Purpose |
|------|---------|
| `collector.go` | Metrics definitions, recording |
| `segment_scraper.go` | Origin segment size scraping |
| `throughput_tracker.go` | Accurate throughput calculation |

Key exports:
```go
func NewCollector(cfg CollectorConfig) *Collector
func (c *Collector) RecordStats(stats *AggregatedStatsUpdate)
```

### internal/orchestrator

High-level coordination.

| File | Purpose |
|------|---------|
| `orchestrator.go` | Main orchestration logic |
| `client_manager.go` | Client lifecycle management |
| `aggregator.go` | Stats aggregation |

Key exports:
```go
func New(cfg *config.Config, logger *slog.Logger) *Orchestrator
func (o *Orchestrator) Run(ctx context.Context) error
```

### internal/parser

FFmpeg output parsing.

| File | Purpose |
|------|---------|
| `progress.go` | Parse progress output |
| `stderr.go` | Parse stderr logs |
| `events.go` | Event types |

Key types:
```go
type ProgressEvent struct {
    Frame     int64
    Fps       float64
    Speed     float64
    OutTimeUs int64
}

type StderrEvent struct {
    Type      EventType  // Open, Error, HTTPCode
    URL       string
    Timestamp time.Time
}
```

### internal/preflight

Pre-run validation checks.

| File | Purpose |
|------|---------|
| `checks.go` | Preflight check implementations |

Checks:
- FFmpeg binary exists
- File descriptor limit sufficient
- Stream URL accessible

### internal/process

Process abstraction.

| File | Purpose |
|------|---------|
| `runner.go` | Runner interface |
| `ffmpeg.go` | FFmpeg implementation |
| `probe.go` | FFprobe variant detection |

Key types:
```go
type Runner interface {
    BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error)
    Name() string
}

type FFmpegRunner struct {
    config *FFmpegConfig
}
```

### internal/stats

Statistics aggregation.

| File | Purpose |
|------|---------|
| `aggregator.go` | Per-client stats |
| `types.go` | Stat structures |
| `percentiles.go` | Percentile calculations |

Key types:
```go
type ClientStats struct {
    ClientID        int
    CurrentSpeed    float64
    TotalBytes      int64
    SegmentCount    int64
    // ...
}

type AggregatedStats struct {
    ActiveClients   int
    AverageSpeed    float64
    P50Latency      time.Duration
    P95Latency      time.Duration
    // ...
}
```

### internal/supervisor

Process supervision.

| File | Purpose |
|------|---------|
| `supervisor.go` | Supervisor implementation |
| `client.go` | Client state machine |
| `health.go` | Stall detection |

Key exports:
```go
func New(cfg SupervisorConfig) *Supervisor
func (s *Supervisor) Start(ctx context.Context)
func (s *Supervisor) CollectStats() []ClientStats
```

### internal/timeseries

Time-series data structures.

| File | Purpose |
|------|---------|
| `ring.go` | Ring buffer |
| `window.go` | Sliding window |

Used for:
- Rolling percentile calculation
- Throughput tracking
- Rate limiting

### internal/tui

Terminal user interface.

| File | Purpose |
|------|---------|
| `model.go` | Bubbletea model |
| `view.go` | Rendering |
| `update.go` | Event handling |
| `origin.go` | Origin metrics scraping |

Uses [bubbletea](https://github.com/charmbracelet/bubbletea) framework.

---

## Dependencies Between Packages

```
main
  └── orchestrator
        ├── supervisor
        │     ├── process
        │     └── parser
        ├── stats
        ├── metrics
        │     └── timeseries
        ├── tui
        ├── preflight
        └── config

(logging is used throughout)
```

---

## Import Rules

1. **No circular imports**: Packages flow downward
2. **internal/** packages are private to the module
3. **config** is shared by all packages
4. **logging** is a utility, used everywhere

---

## Adding a New Package

1. Create directory under `internal/`
2. Add package doc comment
3. Export only what's needed (capital letters)
4. Add tests (`*_test.go`)
5. Document in this file

Example:
```go
// Package myfeature provides awesome functionality.
package myfeature

// PublicFunc is exported.
func PublicFunc() {}

// privateFunc is internal.
func privateFunc() {}
```

---

## Testing

Each package has tests:

```bash
# Run all tests
go test ./...

# Run specific package tests
go test ./internal/parser/...

# With coverage
go test -cover ./...
```

---

## Related Documents

- [DESIGN.md](./DESIGN.md) - Architecture overview
- [CONTRIBUTING.md](../CONTRIBUTING.md) - Contribution guide
