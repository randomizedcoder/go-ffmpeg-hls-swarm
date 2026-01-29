# go-ffmpeg-hls-swarm

**Find where your streaming infrastructure breaks — before your viewers do.**

A specialized HLS load testing tool that orchestrates FFmpeg processes to simulate concurrent viewers. Unlike generic HTTP load testers, this tool understands HLS protocol semantics: playlist parsing, variant selection, segment sequencing, and reconnection handling.

---

## Table of Contents

- [Overview](#overview)
- [Why This Tool?](#why-this-tool)
- [Quick Start](#quick-start)
- [Installation](#installation)
- [Usage](#usage)
- [Configuration](#configuration)
- [Test Origin Server](#test-origin-server)
- [Observability](#observability)
- [Deployment Options](#deployment-options)
- [Architecture](#architecture)
- [Documentation](#documentation)
- [Contributing](#contributing)
- [FAQ](#faq)
- [License](#license)

---

## Overview

### What It Does

go-ffmpeg-hls-swarm spawns and manages multiple FFmpeg processes, each acting as an HLS viewer:

- **Realistic HLS simulation**: Master playlist parsing, variant playlist following, segment downloading
- **Scalable load generation**: 50-500+ concurrent clients from a single machine
- **Configurable ramp-up**: Gradual client startup with jitter to avoid thundering herd
- **Automatic recovery**: Process supervision with exponential backoff restart
- **Rich observability**: Prometheus metrics, TUI dashboard, exit summary reports

### What It Is NOT

- Not a video player — downloads segments but doesn't decode/render video
- Not an ABR simulator — doesn't switch qualities based on bandwidth
- Not rate-limited — downloads at maximum speed (not playback rate)
- Not a DDoS tool — designed for authorized testing of your own infrastructure

### Key Capabilities

| Feature | Description |
|---------|-------------|
| Concurrent clients | 50-500+ from a single machine |
| Variant selection | all, highest, lowest, first |
| DNS override | Test specific servers by IP |
| Cache bypass | Add no-cache headers to stress origin |
| Prometheus metrics | 40+ metrics covering requests, latency, health |
| TUI dashboard | Live terminal UI with real-time stats |
| Origin metrics | Scrape node_exporter/nginx_exporter from origin |
| Graceful shutdown | Clean SIGTERM propagation to all processes |

---

## Why This Tool?

### Comparison with Alternatives

| Tool | HLS Protocol Aware | Segment Sequencing | Playlist Refresh | Reconnection | Best For |
|------|-------------------|--------------------|------------------|--------------|----------|
| **go-ffmpeg-hls-swarm** | Yes | Yes | Yes | Yes | HLS infrastructure testing |
| k6/Locust | No | No | No | No | Generic HTTP load testing |
| curl loops | No | No | No | No | Simple request testing |
| Video players | Yes | Yes | Yes | Yes | Single stream playback |

### Why FFmpeg?

FFmpeg's HLS demuxer handles all protocol edge cases:
- Master playlist parsing and variant enumeration
- Live playlist refresh (respects `#EXT-X-TARGETDURATION`)
- Segment download with retry logic
- AES-128 decryption (if stream is encrypted)
- Network error recovery and reconnection

Building this from scratch would require reimplementing FFmpeg's battle-tested HLS stack.

### Use Cases

| Scenario | How go-ffmpeg-hls-swarm Helps |
|----------|-------------------------------|
| **CDN capacity planning** | Find maximum concurrent viewers before major events |
| **Origin stress testing** | Bypass CDN cache, test origin directly |
| **Edge node validation** | Test specific edge servers by IP |
| **Failover testing** | See how infrastructure handles mass reconnection |
| **Regression testing** | Ensure changes don't degrade streaming performance |

### Limitations

- Downloads at maximum speed (not playback rate)
- Single stream URL per run
- No ABR quality switching simulation
- Linux recommended for high concurrency

---

## Quick Start

### Prerequisites

- Go 1.25+ (`go version`)
- FFmpeg with HLS support (`ffmpeg -version`)

### Build and Run

```bash
# Clone and build
git clone https://github.com/randomizedcoder/go-ffmpeg-hls-swarm.git
cd go-ffmpeg-hls-swarm
make build

# Run quick test (5 clients, 10 seconds)
./bin/go-ffmpeg-hls-swarm -clients 5 -duration 10s \
  https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8
```

### With Test Origin Server

```bash
# Terminal 1: Start local HLS origin
make test-origin

# Terminal 2: Run load test
./bin/go-ffmpeg-hls-swarm -clients 50 -tui \
  http://localhost:17080/stream.m3u8
```

### Quick Recipes

```bash
# Stress test CDN (all quality levels)
./bin/go-ffmpeg-hls-swarm -clients 100 -variant all https://cdn.example.com/master.m3u8

# Test origin directly (bypass cache)
./bin/go-ffmpeg-hls-swarm -clients 50 -no-cache https://cdn.example.com/master.m3u8

# Test specific server by IP
./bin/go-ffmpeg-hls-swarm -clients 50 -resolve 192.168.1.100 --dangerous https://cdn.example.com/master.m3u8

# With TUI and origin metrics
./bin/go-ffmpeg-hls-swarm -clients 100 -tui -origin-metrics-host 10.177.0.10 http://10.177.0.10:17080/stream.m3u8
```

---

## Installation

### From Source (Go)

```bash
git clone https://github.com/randomizedcoder/go-ffmpeg-hls-swarm.git
cd go-ffmpeg-hls-swarm
go build -o go-ffmpeg-hls-swarm ./cmd/go-ffmpeg-hls-swarm
```

### Using Nix (Recommended)

```bash
git clone https://github.com/randomizedcoder/go-ffmpeg-hls-swarm.git
cd go-ffmpeg-hls-swarm
nix build
# Binary at ./result/bin/go-ffmpeg-hls-swarm
```

Or run directly:

```bash
nix run github:randomizedcoder/go-ffmpeg-hls-swarm
```

### Using Makefile

```bash
make build          # Build binary to ./bin/
make build-nix      # Build with Nix (reproducible)
make dev            # Enter development shell
```

### Docker/Podman

```bash
nix build .#swarm-client-container
docker load < ./result
docker run --rm go-ffmpeg-hls-swarm:latest --help
```

---

## Usage

### Basic Syntax

```bash
go-ffmpeg-hls-swarm [flags] <HLS_URL>
```

### Orchestration Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-clients` | 10 | Number of concurrent clients |
| `-ramp-rate` | 5 | Clients to start per second |
| `-ramp-jitter` | 200ms | Random jitter per client start |
| `-duration` | 0 (forever) | Test duration (0 = run until Ctrl+C) |

### Variant Selection

| Flag | Default | Description |
|------|---------|-------------|
| `-variant` | "all" | Quality selection: "all", "highest", "lowest", "first" |
| `-probe-failure-policy` | "fallback" | On ffprobe failure: "fallback" or "fail" |

**Variant modes explained:**

| Mode | Behavior | Use Case |
|------|----------|----------|
| `all` | Download ALL quality levels simultaneously | Maximum CDN stress |
| `highest` | Highest bitrate only (via ffprobe) | Premium viewer simulation |
| `lowest` | Lowest bitrate only (via ffprobe) | Mobile viewer simulation |
| `first` | First listed variant (no probe) | Fast startup |

> **Note**: `-variant all` with 4 variants creates 4x the bandwidth per client.

### Network / Testing Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-resolve` | "" | Connect to specific IP (requires `--dangerous`) |
| `-no-cache` | false | Add no-cache headers to bypass CDN |
| `-header` | - | Add custom HTTP header (repeatable) |

### Safety Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--dangerous` | false | Required for `-resolve` (disables TLS verification) |
| `--print-cmd` | false | Print FFmpeg command and exit |
| `--check` | false | Validate config, run 1 client for 10s |
| `--skip-preflight` | false | Skip system checks |

### Observability Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-metrics` | "0.0.0.0:17091" | Prometheus metrics address |
| `-tui` | false | Enable live terminal dashboard |
| `-v` | false | Verbose logging |
| `-log-format` | "json" | Log format: "json" or "text" |

### Stats Collection Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-stats` | true | Enable FFmpeg output parsing |
| `-stats-loglevel` | "debug" | FFmpeg loglevel for stats |
| `-stats-buffer` | 1000 | Lines to buffer per client |
| `-ffmpeg-debug` | false | Enable FFmpeg debug logging |

### Origin Metrics Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-origin-metrics-host` | "" | Origin hostname for metrics |
| `-origin-metrics` | "" | node_exporter URL |
| `-nginx-metrics` | "" | nginx_exporter URL |
| `-origin-metrics-interval` | 2s | Scrape interval |
| `-origin-metrics-window` | 30s | Rolling window for percentiles |

---

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `ORIGIN_PORT` | 17088 | Local origin server port |
| `METRICS_PORT` | 17091 | Prometheus metrics port |
| `MICROVM_HTTP_PORT` | 17080 | MicroVM nginx port |

### Profiles

**Test Origin Profiles:**

| Profile | Description |
|---------|-------------|
| `default` | Standard 2s segments, 720p |
| `low-latency` | 1s segments, optimized for speed |
| `4k-abr` | Multi-bitrate 4K streaming |
| `stress` | Maximum throughput |
| `logged` | With segment logging |
| `debug` | Full debug logging |

**Swarm Client Profiles:**

| Profile | Clients | Ramp Rate |
|---------|---------|-----------|
| `default` | 50 | 5/sec |
| `gentle` | 20 | 1/sec |
| `burst` | 100 | 50/sec |
| `stress` | 200 | 20/sec |
| `extreme` | 500 | 50/sec |

---

## Test Origin Server

A complete HLS origin server for testing, using FFmpeg for stream generation and Nginx for serving.

### Quick Start

```bash
# Local runner
make test-origin

# With specific profile
make test-origin-low-latency
make test-origin-stress
```

### Deployment Options

| Type | Command | Requirements |
|------|---------|--------------|
| Runner | `make test-origin` | All platforms |
| Container | `make container-run` | Docker/Podman |
| MicroVM | `make microvm-origin` | Linux + KVM |

### MicroVM with TAP Networking

For high-performance testing (~10 Gbps):

```bash
# One-time setup
make network-setup

# Start MicroVM with TAP
make microvm-start-tap

# Access origin
curl http://10.177.0.10:17080/stream.m3u8
```

### Ports

| Port | Service |
|------|---------|
| 17080 | Nginx (HLS stream) |
| 17088 | Local origin (scripts) |
| 17091 | Swarm client metrics |
| 9100 | node_exporter (MicroVM) |
| 9113 | nginx_exporter (MicroVM) |

---

## Observability

### Prometheus Metrics

Exposed at `/metrics` (default port 17091).

**Key metrics:**

| Metric | Type | Description |
|--------|------|-------------|
| `hls_swarm_active_clients` | Gauge | Currently running clients |
| `hls_swarm_target_clients` | Gauge | Target client count |
| `hls_swarm_segment_requests_per_second` | Gauge | Current segment request rate |
| `hls_swarm_throughput_bytes_per_second` | Gauge | Download throughput |
| `hls_swarm_inferred_latency_p99_seconds` | Gauge | P99 latency |
| `hls_swarm_client_restarts_total` | Counter | Total restarts |
| `hls_swarm_average_speed` | Gauge | Average playback speed |

**Example queries:**

```promql
# Active clients
hls_swarm_active_clients

# Throughput in Mbps
hls_swarm_throughput_bytes_per_second * 8 / 1000000

# Restart rate per minute
rate(hls_swarm_client_restarts_total[1m]) * 60

# Error rate
hls_swarm_error_rate
```

### TUI Dashboard

Enable with `-tui`:

```bash
./bin/go-ffmpeg-hls-swarm -tui -clients 100 http://localhost:17080/stream.m3u8
```

Displays:
- Active clients, ramp progress
- Request rates, throughput
- Latency percentiles
- Client health status
- Error counts
- Origin metrics (if enabled)

### Exit Summary

On shutdown, prints a comprehensive summary:

```
═══════════════════════════════════════════════════════════════════
                        go-ffmpeg-hls-swarm Exit Summary
═══════════════════════════════════════════════════════════════════
Run Duration:           00:05:00
Target Clients:         100
Peak Active Clients:    100

Uptime Distribution:
  P50 (median):         04:55
  P95:                  04:58
  P99:                  04:59

Lifecycle:
  Total Starts:         100
  Total Restarts:       2

Metrics endpoint was: http://0.0.0.0:17091/metrics
═══════════════════════════════════════════════════════════════════
```

---

## Deployment Options

### Local Runner

```bash
make test-origin      # Start origin
make load-test-100    # Run 100-client test
```

### OCI Containers

```bash
# Build and load
make container-load
make swarm-container-load

# Run origin
make container-run-origin

# Run swarm
make swarm-container-run-100 STREAM_URL=http://host.docker.internal:17080/stream.m3u8
```

### MicroVMs (Linux + KVM)

```bash
# Check KVM availability
make microvm-check-kvm

# Start MicroVM
make microvm-origin

# With TAP networking (high performance)
make network-setup
make microvm-start-tap
```

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Orchestrator                              │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐              │
│  │  Supervisor │  │  Supervisor │  │  Supervisor │  ...         │
│  │  (Client 0) │  │  (Client 1) │  │  (Client N) │              │
│  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘              │
│         │                │                │                      │
│  ┌──────▼──────┐  ┌──────▼──────┐  ┌──────▼──────┐              │
│  │   FFmpeg    │  │   FFmpeg    │  │   FFmpeg    │              │
│  │  (HLS DL)   │  │  (HLS DL)   │  │  (HLS DL)   │              │
│  └─────────────┘  └─────────────┘  └─────────────┘              │
│                                                                  │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │                    Metrics Collector                        ││
│  │  • Prometheus /metrics endpoint                             ││
│  │  • TUI Dashboard (optional)                                 ││
│  │  • Origin metrics scraper (optional)                        ││
│  └─────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
```

### Components

| Component | Package | Responsibility |
|-----------|---------|----------------|
| Orchestrator | `internal/orchestrator` | Client lifecycle, ramp scheduling |
| Supervisor | `internal/supervisor` | Process management, restart logic |
| Process | `internal/process` | FFmpeg command building, execution |
| Metrics | `internal/metrics` | Prometheus collectors, origin scraping |
| Parser | `internal/parser` | FFmpeg output parsing |
| TUI | `internal/tui` | Terminal dashboard |
| Config | `internal/config` | CLI flags, configuration |
| Preflight | `internal/preflight` | System checks |

### Design Principles

1. **Process supervision**: Each client has a dedicated supervisor goroutine
2. **Graceful degradation**: Partial failures don't stop the test
3. **Memory efficiency**: Bounded buffers, no unbounded growth
4. **Observable**: Rich metrics for debugging and analysis

---

## Documentation

Full documentation in [`./documents/`](documents/):

### User Guide

- [Installation](documents/user-guide/getting-started/INSTALLATION.md)
- [Quick Start](documents/user-guide/getting-started/QUICKSTART.md)
- [CLI Reference](documents/user-guide/configuration/CLI_REFERENCE.md)
- [Profiles](documents/user-guide/configuration/PROFILES.md)
- [Load Testing](documents/user-guide/operations/LOAD_TESTING.md)
- [OS Tuning](documents/user-guide/operations/OS_TUNING.md)
- [Troubleshooting](documents/user-guide/operations/TROUBLESHOOTING.md)
- [Metrics](documents/user-guide/observability/METRICS.md)
- [TUI Dashboard](documents/user-guide/observability/TUI_DASHBOARD.md)

### Contributor Guide

- [Architecture](documents/contributor-guide/architecture/)
- [Components](documents/contributor-guide/components/)
- [Infrastructure](documents/contributor-guide/infrastructure/)

### Reference

- [CLI Flags](documents/reference/CLI_FLAGS.md)
- [Metrics Reference](documents/reference/METRICS_REFERENCE.md)
- [Ports](documents/reference/PORTS.md)

---

## Contributing

Contributions welcome! See [CONTRIBUTING.md](documents/contributor-guide/CONTRIBUTING.md).

### Development Setup

```bash
# Clone
git clone https://github.com/randomizedcoder/go-ffmpeg-hls-swarm.git
cd go-ffmpeg-hls-swarm

# Enter dev shell (provides all tools)
nix develop

# Or with make
make dev
```

### Running Tests

```bash
make test              # Go tests
make lint              # Linting
make check             # All checks
make test-nix-all      # Nix tests
```

---

## FAQ

### General

**Q: Can I use this today?**
A: Yes! The tool is production-ready and actively tested.

**Q: Why FFmpeg instead of a custom HLS client?**
A: FFmpeg's HLS demuxer handles all protocol edge cases (playlist parsing, segment retries, encryption, reconnection). Building this from scratch would be significant effort.

**Q: How many clients can I run?**
A: 50-500+ from a single machine depending on system resources. Each FFmpeg process uses ~20-50 MB.

**Q: Does this work on macOS/Windows?**
A: macOS: Yes, with limitations (no MicroVM support). Windows: Not tested, may work via WSL2.

**Q: Is this a DDoS tool?**
A: No. It's designed for authorized testing of your own infrastructure. Always get permission before load testing.

### Technical

**Q: Why does `-variant all` use so much bandwidth?**
A: It downloads ALL quality levels simultaneously. With 4 variants, that's 4x the bandwidth per client.

**Q: Why isn't the TUI showing?**
A: TUI requires a terminal (TTY). Check you're running interactively with `-tui` flag.

**Q: Why are clients restarting frequently?**
A: Usually means the origin is overloaded. Reduce client count or check origin logs.

**Q: What's the difference between ports 17080 and 17088?**
A: 17080 is MicroVM Nginx, 17088 is local origin scripts. See [PORTS.md](documents/reference/PORTS.md).

### Troubleshooting

**Q: "too many open files"**
A: Run `ulimit -n 8192` before the test.

**Q: No metrics at localhost:17091**
A: Check if port is in use (`lsof -i :17091`) or use different port (`-metrics 0.0.0.0:9091`).

**Q: FFmpeg exits immediately**
A: Test the URL directly: `ffmpeg -i <URL> -t 5 -f null -`

---

## License

MIT License - see [LICENSE](LICENSE) for details.

---

## Acknowledgments

- [FFmpeg](https://ffmpeg.org/) for the HLS demuxer
- [Prometheus](https://prometheus.io/) for metrics
- [Bubbletea](https://github.com/charmbracelet/bubbletea) for the TUI framework
- [Nix](https://nixos.org/) for reproducible builds

---

**Find where your streaming infrastructure breaks — before your viewers do.**
