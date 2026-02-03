# go-ffmpeg-hls-swarm

HLS load testing with FFmpeg process orchestration.

---

## Overview

go-ffmpeg-hls-swarm is a specialized load testing tool for HTTP Live Streaming (HLS) infrastructure. Unlike traditional HTTP load testers, it uses real FFmpeg processes to simulate actual video players, providing realistic traffic patterns and accurate measurements.

### Key Capabilities

- **Process-based simulation**: Real FFmpeg processes make actual HTTP requests
- **Scalable**: Run 100s of concurrent simulated viewers
- **Accurate metrics**: Prometheus metrics with segment-level tracking
- **Live dashboard**: Real-time TUI with throughput, latency, and health
- **Origin integration**: Scrape metrics from origin servers
- **Flexible deployment**: Native binary, containers, or MicroVMs

### What It Does

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│  go-ffmpeg-     │────▶│  HLS Origin     │────▶│  Prometheus     │
│  hls-swarm      │     │  or CDN         │     │  Metrics        │
│  (N clients)    │     │                 │     │                 │
└─────────────────┘     └─────────────────┘     └─────────────────┘
       │                                               │
       │         Live TUI Dashboard                    │
       └───────────────────────────────────────────────┘
```

Each client:
1. Fetches the HLS master playlist
2. Downloads media playlists
3. Downloads segments continuously
4. Reports metrics (speed, latency, errors)

---

## Quick Start

### Prerequisites

- **Nix** (recommended) or **Go 1.21+**
- **FFmpeg** (provided by Nix, or install separately)

### Install with Nix (Recommended)

```bash
# Clone the repository
git clone https://github.com/randomizedcoder/go-ffmpeg-hls-swarm.git
cd go-ffmpeg-hls-swarm

# Run the load tester
nix run .#run -- -clients 10 https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8
```

### Install with Go

```bash
# Clone and build
git clone https://github.com/randomizedcoder/go-ffmpeg-hls-swarm.git
cd go-ffmpeg-hls-swarm
go build -o swarm ./cmd/go-ffmpeg-hls-swarm

# Run (requires FFmpeg in PATH)
./swarm -clients 10 https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8
```

### Start a Test Origin (Optional)

If you need a test HLS stream:

```bash
# Terminal 1: Start test origin
nix run .#test-origin

# Terminal 2: Run load test
nix run .#run -- -clients 50 http://localhost:17080/stream.m3u8
```

---

## Usage

### Basic Command

```bash
go-ffmpeg-hls-swarm [flags] <HLS_URL>
```

### Essential Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-clients` | 10 | Number of concurrent clients |
| `-ramp-rate` | 5 | Clients to start per second |
| `-duration` | 0 | Run duration (0 = forever) |
| `-tui` | true | Show live dashboard |
| `-metrics` | 0.0.0.0:17091 | Prometheus metrics address |

### Examples

```bash
# Quick smoke test (5 clients, 30 seconds)
go-ffmpeg-hls-swarm -clients 5 -duration 30s https://example.com/stream.m3u8

# Standard load test (100 clients, 5 minutes)
go-ffmpeg-hls-swarm -clients 100 -duration 5m https://example.com/stream.m3u8

# Stress test (300 clients, fast ramp)
go-ffmpeg-hls-swarm -clients 300 -ramp-rate 50 https://example.com/stream.m3u8

# Cache bypass test
go-ffmpeg-hls-swarm -clients 100 -no-cache https://cdn.example.com/stream.m3u8

# Test specific server by IP
go-ffmpeg-hls-swarm -clients 50 -resolve 192.168.1.100 --dangerous \
  https://cdn.example.com/stream.m3u8
```

### Variant Selection

Control which quality levels to download:

| Mode | Description | Use Case |
|------|-------------|----------|
| `-variant all` | Download all qualities | Maximum CDN stress |
| `-variant highest` | Download highest only | Premium viewer simulation |
| `-variant lowest` | Download lowest only | Mobile viewer simulation |
| `-variant first` | Download first variant | Fast startup |

---

## TUI Dashboard

The live terminal dashboard (enabled by default) shows real-time metrics:

```
┌────────────────────────────────────────────────────────────────┐
│  go-ffmpeg-hls-swarm                     Elapsed: 0:05:23      │
├────────────────────────────────────────────────────────────────┤
│  Clients        100/100  100%    Seg/s        234.5            │
│  Stalled        0                Manifest/s   117.2            │
│  Avg Speed      1.02x            Throughput   125.3 MB/s       │
│  Errors         0                HTTP 4xx     0                │
├────────────────────────────────────────────────────────────────┤
│  P50 Latency    45ms             Origin Net Out  128.1 MB/s    │
│  P95 Latency    120ms            Origin CPU      45%           │
│  P99 Latency    250ms            Lookup         100% (5432/5432)│
└────────────────────────────────────────────────────────────────┘
```

### Key Metrics Explained

| Metric | Healthy Value | Warning Signs |
|--------|---------------|---------------|
| **Clients** | 100% of target | Not reaching target |
| **Stalled** | 0 | Any stalled clients |
| **Avg Speed** | >= 1.0x | < 1.0x means buffering |
| **P95 Latency** | < 500ms | > 1s indicates issues |
| **Errors** | 0 | Any errors |

Press `q` to quit, `Ctrl+C` for graceful shutdown.

---

## Prometheus Metrics

Metrics are exposed at `http://localhost:17091/metrics`.

### Core Metrics

```promql
# Active clients
hls_swarm_active_clients

# Segment throughput (MB/s)
hls_swarm_segment_throughput_30s_bytes_per_second / 1024 / 1024

# Latency percentiles
hls_swarm_inferred_latency_p95_seconds

# Error rate
hls_swarm_error_rate
```

### All Metric Prefixes

| Prefix | Description |
|--------|-------------|
| `hls_swarm_active_*` | Client counts |
| `hls_swarm_segment_*` | Segment metrics |
| `hls_swarm_manifest_*` | Manifest metrics |
| `hls_swarm_inferred_latency_*` | Latency metrics |
| `hls_swarm_*_total` | Counters |
| `hls_swarm_error_*` | Error metrics |

See [METRICS_REFERENCE.md](./documents/reference/METRICS_REFERENCE.md) for the complete list.

---

## Origin Metrics Integration

Scrape metrics from your origin server for integrated monitoring:

```bash
# Using origin-metrics-host (recommended)
go-ffmpeg-hls-swarm -clients 100 \
  -origin-metrics-host 10.177.0.10 \
  http://10.177.0.10:17080/stream.m3u8

# Using explicit URLs
go-ffmpeg-hls-swarm -clients 100 \
  -origin-metrics http://origin:9100/metrics \
  -nginx-metrics http://origin:9113/metrics \
  http://origin:17080/stream.m3u8
```

The TUI will show origin CPU, network throughput, and connection counts.

---

## Test Origin Server

A built-in test HLS origin server is included for testing without external infrastructure.

### Running the Test Origin

```bash
# Default profile (2s segments, 1080p)
nix run .#test-origin

# Low-latency profile (1s segments)
nix run .#test-origin-low-latency

# Stress test profile (4K)
nix run .#test-origin-stress
```

### Available Profiles

| Profile | Segments | Resolution | Use Case |
|---------|----------|------------|----------|
| default | 2s | 1080p | General testing |
| low-latency | 1s | 720p | Latency testing |
| 4k-abr | 2s | 4K multi-bitrate | ABR testing |
| stress | 2s | 4K | Maximum throughput |
| logged | 2s | 1080p | With access logs |
| debug | 2s | 1080p | Full debug logs |

### Deployment Options

| Option | Command | Use Case |
|--------|---------|----------|
| Runner script | `nix run .#test-origin` | Local development |
| Container | `make container-run` | Docker/Podman |
| MicroVM | `make microvm-start` | Isolated testing |

---

## Container Deployment

### Build and Run Containers

```bash
# Build swarm client container
nix build .#swarm-client-container
docker load < ./result

# Run with CLI arguments
docker run --rm go-ffmpeg-hls-swarm \
  -clients 100 \
  http://host.docker.internal:17080/stream.m3u8

# Run with environment variables
docker run --rm \
  -e STREAM_URL=http://origin:17080/stream.m3u8 \
  -e CLIENTS=100 \
  -p 17091:17091 \
  go-ffmpeg-hls-swarm
```

### Build Test Origin Container

```bash
nix build .#test-origin-container
docker load < ./result

docker run --rm -p 17080:17080 go-ffmpeg-hls-swarm-test-origin
```

### Docker Compose

```yaml
version: '3.8'
services:
  origin:
    image: go-ffmpeg-hls-swarm-test-origin:latest
    ports:
      - "17080:17080"

  swarm:
    image: go-ffmpeg-hls-swarm:latest
    environment:
      STREAM_URL: http://origin:17080/stream.m3u8
      CLIENTS: 100
    ports:
      - "17091:17091"
    depends_on:
      - origin
```

---

## MicroVM Deployment

For isolated testing with KVM-based MicroVMs:

### Prerequisites

- Linux with KVM support
- `/dev/kvm` accessible

### Basic Usage

```bash
# Check KVM availability
make microvm-check-kvm

# Start MicroVM (user-mode networking)
make microvm-start

# Stop MicroVM
make microvm-stop
```

### TAP Networking (High Performance)

For near-native networking performance (~10 Gbps):

```bash
# Setup TAP networking (requires sudo)
make network-setup

# Start MicroVM with TAP
make microvm-start-tap

# Run load test
go-ffmpeg-hls-swarm -clients 300 http://10.177.0.10:17080/stream.m3u8

# Cleanup
make network-teardown
```

---

## Configuration Reference

### All CLI Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-clients` | int | 10 | Concurrent clients |
| `-ramp-rate` | int | 5 | Clients per second |
| `-ramp-jitter` | duration | 200ms | Startup jitter |
| `-duration` | duration | 0 | Test duration |
| `-variant` | string | "all" | Bitrate selection |
| `-tui` | bool | true | Enable TUI |
| `-metrics` | string | 0.0.0.0:17091 | Metrics address |
| `-v` | bool | false | Verbose logging |
| `-timeout` | duration | 15s | Network timeout |
| `-reconnect` | bool | true | Enable reconnect |
| `-no-cache` | bool | false | Cache bypass |
| `-resolve` | string | "" | IP override |
| `--dangerous` | bool | false | Allow unsafe opts |
| `--check` | bool | false | Validation mode |
| `--print-cmd` | bool | false | Print FFmpeg cmd |

See [CLI_REFERENCE.md](./documents/user-guide/configuration/CLI_REFERENCE.md) for complete documentation.

---

## Architecture

```
cmd/go-ffmpeg-hls-swarm/main.go
        │
        ▼
┌───────────────────────────────────────────────────────────────┐
│                        Orchestrator                            │
│  - Manages lifecycle                                          │
│  - Aggregates stats                                           │
│  - Updates metrics                                            │
│                                                               │
│  ┌─────────────┐  ┌──────────────┐  ┌─────────────────────┐  │
│  │ Supervisor  │  │ Stats Agg    │  │ Metrics Collector   │  │
│  └─────────────┘  └──────────────┘  └─────────────────────┘  │
└───────────────────────────────────────────────────────────────┘
        │                    │                    │
        ▼                    ▼                    ▼
┌─────────────┐      ┌──────────────┐     ┌─────────────────┐
│  FFmpeg     │ ───▶ │   Parser     │ ───▶│   TUI           │
│  Processes  │      │   Pipeline   │     │   Dashboard     │
└─────────────┘      └──────────────┘     └─────────────────┘
```

### Package Structure

| Package | Purpose |
|---------|---------|
| `cmd/` | CLI entry point |
| `internal/orchestrator` | High-level coordination |
| `internal/supervisor` | Process management |
| `internal/process` | FFmpeg command generation |
| `internal/parser` | Output parsing |
| `internal/stats` | Aggregation |
| `internal/metrics` | Prometheus export |
| `internal/tui` | Terminal dashboard |

---

## Make Targets

### Build & Run

```bash
make build              # Build Go binary
make run                # Build and run
make dev                # Enter Nix dev shell
```

### Testing

```bash
make test               # Run unit tests
make test-race          # With race detector
make check              # All checks (lint, test, vet)
```

### Test Origin

```bash
make test-origin        # Run default origin
make microvm-start      # Start MicroVM origin
make container-run      # Run container origin
```

### Load Tests

```bash
make load-test-50       # 50 clients, 30s
make load-test-100      # 100 clients, 30s
make load-test-300      # 300 clients, 30s
```

---

## Documentation

### User Guide

- [INSTALLATION.md](./documents/user-guide/getting-started/INSTALLATION.md) - Installation guide
- [QUICKSTART.md](./documents/user-guide/getting-started/QUICKSTART.md) - Quick start tutorial
- [FIRST_LOAD_TEST.md](./documents/user-guide/getting-started/FIRST_LOAD_TEST.md) - First test walkthrough
- [CLI_REFERENCE.md](./documents/user-guide/configuration/CLI_REFERENCE.md) - CLI flags
- [PROFILES.md](./documents/user-guide/configuration/PROFILES.md) - Profiles guide
- [LOAD_TESTING.md](./documents/user-guide/operations/LOAD_TESTING.md) - Testing guide
- [OS_TUNING.md](./documents/user-guide/operations/OS_TUNING.md) - System tuning
- [METRICS.md](./documents/user-guide/observability/METRICS.md) - Metrics guide
- [TUI_DASHBOARD.md](./documents/user-guide/observability/TUI_DASHBOARD.md) - TUI guide

### Reference

- [CLI_FLAGS.md](./documents/reference/CLI_FLAGS.md) - All flags
- [METRICS_REFERENCE.md](./documents/reference/METRICS_REFERENCE.md) - All metrics
- [PORTS.md](./documents/reference/PORTS.md) - Port assignments
- [EXIT_CODES.md](./documents/reference/EXIT_CODES.md) - Exit codes
- [FFMPEG_COMMANDS.md](./documents/reference/FFMPEG_COMMANDS.md) - FFmpeg commands

### Contributor Guide

- [DESIGN.md](./documents/contributor-guide/architecture/DESIGN.md) - Architecture
- [PACKAGE_STRUCTURE.md](./documents/contributor-guide/architecture/PACKAGE_STRUCTURE.md) - Packages
- [NIX_FLAKE.md](./documents/contributor-guide/infrastructure/NIX_FLAKE.md) - Nix guide
- [CONTRIBUTING.md](./documents/contributor-guide/CONTRIBUTING.md) - How to contribute

---

## FAQ

### How many clients can I run?

Depends on hardware. Rule of thumb:
- 100 clients: 2 CPU cores, 512MB RAM
- 300 clients: 4 CPU cores, 1GB RAM
- 500+ clients: 8+ CPU cores, 2GB+ RAM

Increase file descriptor limits (`ulimit -n 65536`).

### Why use FFmpeg instead of HTTP load testing?

FFmpeg provides:
- Real HLS protocol handling
- Actual segment parsing
- Realistic timing patterns
- Accurate throughput measurement

HTTP testers miss HLS-specific behaviors.

### What's the difference between TUI throughput and origin metrics?

- **TUI throughput**: Calculated from segment downloads on client side
- **Origin metrics**: Actual bytes served by the origin server

They should align closely when segment size tracking is working (100% lookup rate).

### How do I test behind a CDN?

```bash
# Bypass cache
go-ffmpeg-hls-swarm -clients 100 -no-cache https://cdn.example.com/stream.m3u8

# Hit specific edge
go-ffmpeg-hls-swarm -clients 100 -resolve 1.2.3.4 --dangerous \
  https://cdn.example.com/stream.m3u8
```

### Why are clients stalling?

Common causes:
1. Origin server overloaded
2. Network bottleneck
3. Too aggressive ramp rate
4. Insufficient bandwidth

Check: Origin CPU, network throughput, error rate.

### How do I run headless (no TUI)?

```bash
go-ffmpeg-hls-swarm -tui=false -clients 100 ...
```

### How do I get Grafana dashboards?

See [PROMETHEUS_GRAFANA.md](./documents/user-guide/observability/PROMETHEUS_GRAFANA.md) for:
- Prometheus setup
- Grafana dashboard panels
- Alert rules

---

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make changes
4. Run tests: `make check`
5. Submit a pull request

See [CONTRIBUTING.md](./documents/contributor-guide/CONTRIBUTING.md) for details.

---

## License

MIT License - see [LICENSE](./LICENSE) for details.
