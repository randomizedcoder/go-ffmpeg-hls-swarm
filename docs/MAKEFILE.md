# Makefile Design Document

> **Status**: Active
> **Location**: `./Makefile`
> **Related**: [NIX_FLAKE_DESIGN.md](NIX_FLAKE_DESIGN.md), [CLIENT_DEPLOYMENT.md](CLIENT_DEPLOYMENT.md), [TEST_ORIGIN.md](TEST_ORIGIN.md)

---

## Overview

The Makefile serves as the **primary developer interface** for the `go-ffmpeg-hls-swarm` project. It provides a consistent, discoverable way to build, test, and run all project components without memorizing Nix commands or Go toolchain invocations.

### Design Goals

| Goal | How It's Achieved |
|------|-------------------|
| **Discoverability** | `make help` shows all targets organized by category |
| **Consistency** | All operations use the same interface regardless of underlying tool |
| **Sensible Defaults** | Works out-of-the-box with reasonable defaults |
| **Overridability** | Key variables can be overridden via environment or command line |
| **Documentation** | Every target has a `## comment` that appears in help |

### Philosophy

```
Simple tasks should be simple.
Complex tasks should be possible.
Everything should be documented.
```

---

## Quick Reference

```bash
make help              # Show all available targets
make info              # Show project info and available profiles
make build             # Build Go binary
make test              # Run tests
make dev               # Enter Nix development shell

# Load testing (local origin - starts automatically)
make load-test-100     # 100-client test with local origin
make load-test-300     # 300-client stress test

# MicroVM origin (production-like Nginx)
make microvm-start     # Start MicroVM with health polling
make load-test-300-microvm  # Test against MicroVM
make microvm-stop      # Stop MicroVM

# High-performance networking (TAP + vhost-net)
make network-setup     # Create bridge, TAP, nftables rules
make network-check     # Verify network configuration
make network-teardown  # Remove networking setup
```

> **Port Reference**: See [PORTS.md](PORTS.md) for all port numbers and configuration.

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              Makefile                                        │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐    │
│  │   Build &    │  │ Load Testing │  │   MicroVM    │  │   Network    │    │
│  │     Run      │  │              │  │              │  │    Setup     │    │
│  ├──────────────┤  ├──────────────┤  ├──────────────┤  ├──────────────┤    │
│  │ build        │  │ load-test-50 │  │ microvm-start│  │ network-setup│    │
│  │ build-nix    │  │ load-test-100│  │ microvm-stop │  │ network-check│    │
│  │ run          │  │ load-test-300│  │ microvm-check│  │ network-     │    │
│  │ clean        │  │ load-test-*  │  │   -ports     │  │   teardown   │    │
│  │              │  │   -microvm   │  │              │  │              │    │
│  └──────────────┘  └──────────────┘  └──────────────┘  └──────────────┘    │
│                                                                              │
│  Scripts:                                                                    │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ scripts/50-clients/    scripts/microvm/     scripts/network/        │   │
│  │ scripts/100-clients/   ├── start.sh         ├── setup.sh            │   │
│  │ scripts/300-clients/   └── stop.sh          ├── teardown.sh         │   │
│  │ scripts/lib/common.sh                       └── check.sh            │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
│  Underlying Tools:                                                           │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐          │
│  │   Go    │  │   Nix   │  │  QEMU   │  │ nftables│  │  Shell  │          │
│  └─────────┘  └─────────┘  └─────────┘  └─────────┘  └─────────┘          │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Target Categories

### 1. Build & Run

**Purpose**: Compile and execute the Go binary.

| Target | Description | Underlying Command |
|--------|-------------|-------------------|
| `build` | Build Go binary locally | `go build` |
| `build-nix` | Build via Nix (reproducible) | `nix build` |
| `run` | Build and run | `go build && ./bin/go-ffmpeg-hls-swarm` |
| `run-nix` | Run via Nix | `nix run` |
| `run-with-args` | Run with custom arguments | `./bin/go-ffmpeg-hls-swarm $(ARGS)` |
| `clean` | Remove build artifacts | `rm -rf bin/ result` |
| `all` | Alias for `build` | — |

**Usage Examples**:

```bash
# Standard build (requires nix develop shell)
make build

# Reproducible build via Nix
make build-nix

# Run with arguments
make run-with-args ARGS="--clients 100 http://example.com/stream.m3u8"
```

**When to Use Which**:
- `build` — Fast iteration during development (requires `make dev` first)
- `build-nix` — CI/CD, release builds, ensuring reproducibility
- `run-nix` — Quick testing without entering dev shell

---

### 2. Development

**Purpose**: Set up development environment and maintain code quality.

| Target | Description | Underlying Command |
|--------|-------------|-------------------|
| `dev` | Enter Nix development shell | `nix develop` |
| `shell` | Alias for `dev` | — |
| `lint` | Run Go linters | `golangci-lint run` |
| `fmt` | Format Go code | `go fmt && gofumpt` |
| `fmt-nix` | Format Nix files | `nix fmt` |
| `fmt-all` | Format everything | `fmt` + `fmt-nix` |
| `check` | Run all checks | `check-go` + `check-nix` |
| `check-go` | Go checks via Nix | `nix build .#checks...` |
| `check-nix` | Validate flake (fast) | `nix flake check --no-build` |
| `check-nix-full` | Full flake check | `nix flake check` |

**Usage Examples**:

```bash
# Start development session
make dev

# Format before committing
make fmt-all

# Validate everything before PR
make check
```

**Development Workflow**:

```
1. make dev          # Enter shell with all tools
2. make build        # Build binary
3. make test         # Run tests
4. make fmt-all      # Format code
5. make check        # Final validation
6. git commit        # Commit changes
```

---

### 3. Testing

**Purpose**: Verify correctness at various levels.

| Target | Description | Underlying Command |
|--------|-------------|-------------------|
| `test` | Run all tests | `go test ./...` |
| `test-unit` | Run unit tests | `go test -v ./...` |
| `test-race` | Tests with race detector | `go test -race ./...` |
| `test-coverage` | Generate coverage report | `go test -coverprofile=...` |
| `test-integration` | NixOS VM tests (Linux) | `nix build .#checks...integration-test` |
| `test-integration-interactive` | Interactive VM tests | `nixos-test-driver --interactive` |

**Usage Examples**:

```bash
# Quick test during development
make test

# Thorough testing before merge
make test-race
make test-coverage

# Full integration test (Linux only, requires KVM)
make test-integration
```

**Test Pyramid**:

```
                    ┌───────────────┐
                    │ Integration   │  make test-integration
                    │    Tests      │  (NixOS VMs)
                    └───────────────┘
               ┌─────────────────────────┐
               │      Unit Tests         │  make test-unit
               │   (fast, isolated)      │  make test-race
               └─────────────────────────┘
          ┌───────────────────────────────────┐
          │         Static Analysis           │  make lint
          │      (linting, formatting)        │  make check
          └───────────────────────────────────┘
```

---

### 4. Test Origin Server

**Purpose**: Run the self-contained HLS origin server for testing.

#### Local Origin (Python HTTP Server)

For quick testing, uses FFmpeg + Python HTTP server:

| Target | Description | Profile |
|--------|-------------|---------|
| `test-origin` | Default profile | 720p, 2s segments |
| `test-origin-low-latency` | Low latency | 1s segments |
| `test-origin-4k-abr` | Multi-bitrate 4K | Multiple renditions |
| `test-origin-stress` | Stress testing | Maximum throughput |

```bash
# Start local test origin
make test-origin
# Stream at: http://localhost:17088/stream.m3u8
```

#### MicroVM Origin (Production-like Nginx)

For realistic testing with 4GB RAM, 4 vCPUs:

| Target | Description |
|--------|-------------|
| `microvm-start` | Start MicroVM with health polling (recommended) |
| `microvm-stop` | Stop the MicroVM |
| `microvm-check-ports` | Check if ports 17080/17113 are available |
| `microvm-check-kvm` | Verify KVM is available |

```bash
# Start MicroVM (polls until ready)
make microvm-start

# Stream at: http://localhost:17080/stream.m3u8
# Metrics at: http://localhost:17113/metrics

# Stop when done
make microvm-stop
```

**MicroVM Resources** (configurable in `nix/test-origin/microvm.nix`):
- RAM: 4096 MB (4GB)
- vCPUs: 4
- Nginx + FFmpeg running inside

See [TEST_ORIGIN.md](TEST_ORIGIN.md) and [PORTS.md](PORTS.md) for details.

---

### 5. Network Setup (High-Performance)

**Purpose**: Configure TAP + bridge networking with vhost-net for maximum MicroVM performance.

By default, MicroVMs use QEMU user-mode NAT (~500 Mbps). TAP + vhost-net provides ~10 Gbps with much lower CPU overhead.

| Target | Description | Requires sudo |
|--------|-------------|---------------|
| `network-setup` | Create bridge, TAP, nftables rules | Yes |
| `network-check` | Verify network configuration | Yes |
| `network-teardown` | Remove all networking setup | Yes |

**What gets created**:

| Resource | Name | Purpose |
|----------|------|---------|
| Bridge | `hlsbr0` | Virtual switch (10.177.0.1/24) |
| TAP device | `hlstap0` | VM network interface |
| nftables table | `hls_nat` | NAT + port forwarding |
| nftables table | `hls_filter` | Allow bridge traffic |

**Usage**:

```bash
# One-time setup (requires sudo)
make network-setup

# Verify configuration
make network-check

# Start MicroVM (now uses TAP networking)
make microvm-start

# When done with testing
make network-teardown
```

**Architecture**:

```
┌─────────────────────────────────────────────────────────────────────┐
│                           Host Machine                               │
│                                                                      │
│   ┌─────────────┐      ┌─────────────┐      ┌───────────────────┐  │
│   │   MicroVM   │══════│   hlstap0   │══════│      hlsbr0       │  │
│   │ 10.177.0.10 │      │    (TAP)    │      │     (Bridge)      │  │
│   └─────────────┘      └─────────────┘      │    10.177.0.1     │  │
│                                              └─────────┬─────────┘  │
│                                                        │            │
│                                          nftables NAT (masquerade)  │
│                                                        │            │
│                                                        ▼            │
│   Port forwarding:                           Physical NIC           │
│     localhost:17080 -> 10.177.0.10:17080                           │
│     localhost:17113 -> 10.177.0.10:17113                           │
│     localhost:17022 -> 10.177.0.10:17022                           │
└─────────────────────────────────────────────────────────────────────┘
```

See [MICROVM_NETWORKING.md](MICROVM_NETWORKING.md) for full documentation.

---

### 6. Load Tests (Recommended)

**Purpose**: Pre-configured load tests that automatically start the origin.

#### With Local Origin (All-in-One)

These targets start a local origin, run the test, and clean up:

| Target | Clients | Ramp Rate | Duration |
|--------|---------|-----------|----------|
| `load-test-50` | 50 | 10/sec | 30s |
| `load-test-100` | 100 | 20/sec | 30s |
| `load-test-300` | 300 | 50/sec | 30s |
| `load-test-500` | 500 | 100/sec | 30s |
| `load-test-1000` | 1000 | 100/sec | 30s |

```bash
# Quick 100-client test (single command!)
make load-test-100

# Longer test
make load-test-300 DURATION=60s

# 5-minute stress test
make load-test-500 DURATION=5m
```

#### Against MicroVM Origin

For production-like testing with Nginx:

| Target | Clients | Description |
|--------|---------|-------------|
| `load-test-100-microvm` | 100 | Standard test |
| `load-test-300-microvm` | 300 | Stress test |
| `load-test-500-microvm` | 500 | Heavy load |

```bash
# First, start the MicroVM
make microvm-start

# Then run tests
make load-test-300-microvm

# When done
make microvm-stop
```

### 7. Swarm Client (Direct Control)

**Purpose**: Run the load testing client directly (for custom configurations).

| Target | Description | Clients | Ramp Rate |
|--------|-------------|---------|-----------|
| `swarm-client` | Default | 50 | 5/sec |
| `swarm-client-gentle` | Gentle warm-up | 20 | 1/sec |
| `swarm-client-stress` | High load | 200 | 20/sec |
| `swarm-client-burst` | Thundering herd | 100 | 50/sec |
| `swarm-client-extreme` | Maximum load | 500 | 50/sec |

```bash
# Custom URL (requires origin to be running separately)
make swarm-client-stress STREAM_URL=http://cdn.example.com/live/master.m3u8
```

See [LOAD_TESTING.md](LOAD_TESTING.md) and [CLIENT_DEPLOYMENT.md](CLIENT_DEPLOYMENT.md) for details.

---

### 8. Containers

**Purpose**: Build and run OCI container images.

| Target | Description | Image |
|--------|-------------|-------|
| `container` | Build test origin container | `test-hls-origin` |
| `container-load` | Load into Docker | — |
| `container-run` | Run test origin container | Port 8080 |
| `swarm-container` | Build swarm client container | `go-ffmpeg-hls-swarm` |
| `swarm-container-load` | Load into Docker | — |
| `swarm-container-run` | Run swarm client container | Port 9090 |

**Usage Examples**:

```bash
# Build and run test origin in Docker
make container-run

# Build and run swarm client in Docker
make swarm-container-run STREAM_URL=http://host.docker.internal:8080/stream.m3u8
```

**Docker Compose Workflow**:

```bash
# Build both containers
make container
make swarm-container

# Load into Docker
docker load < result  # (run after each build)

# Use docker-compose (see CLIENT_DEPLOYMENT.md for example)
docker-compose up
```

---

### 9. Utility Targets

**Purpose**: Helper commands for common operations.

| Target | Description |
|--------|-------------|
| `help` | Show all targets with descriptions |
| `info` | Show project info and available profiles |
| `quick-test` | Build and show version |
| `ci` | Run full CI pipeline locally |
| `full-test` | Instructions for origin + client test |
| `git-add` | Stage all files for git |

**Usage Examples**:

```bash
# See what's available
make help

# See profile options
make info

# Run what CI runs
make ci
```

---

## Configuration Variables

Variables can be overridden via environment or command line:

```makefile
# Build configuration
BINARY_NAME := go-ffmpeg-hls-swarm
OUTPUT_DIR  := bin
GOFLAGS     ?=
LDFLAGS     ?= -s -w

# Test duration (for load tests)
DURATION    ?= 30s

# Nix configuration
NIX         := nix
NIX_BUILD   := $(NIX) build
NIX_RUN     := $(NIX) run
```

### Port Configuration

All ports use the `17xxx` range to avoid conflicts. See [PORTS.md](PORTS.md) for full documentation.

| Variable | Default | Description |
|----------|---------|-------------|
| `ORIGIN_PORT` | 17088 | Local Python HTTP origin |
| `METRICS_PORT` | 17091 | Swarm client Prometheus metrics |
| `MICROVM_HTTP_PORT` | 17080 | MicroVM Nginx port |
| `MICROVM_METRICS_PORT` | 17113 | MicroVM Prometheus exporter |

**Override Examples**:

```bash
# Custom test duration
make load-test-300 DURATION=60s

# Use alternative ports (if 17xxx conflicts)
ORIGIN_PORT=27088 make load-test-100

# Custom output directory
make build OUTPUT_DIR=/tmp/build

# Use different Nix command (e.g., for remote builders)
make build-nix NIX="nix --builders 'ssh://builder'"
```

---

## Adding New Targets

### Anatomy of a Target

```makefile
target-name: dependencies ## Help text shown in 'make help'
	@echo "Optional: silent command (@ prefix)"
	command-to-run
	another-command
```

**Key Points**:
- `##` comment becomes help text (must be on same line as target)
- `@` prefix suppresses command echo
- Use `$(VARIABLE)` for configurable values
- Add to `.PHONY` if not creating a file

### Adding a New Category

1. Add `.PHONY` declarations:
   ```makefile
   .PHONY: new-target another-new-target
   ```

2. Add help section in `help` target:
   ```makefile
   @echo "$(GREEN)New Category:$(RESET)"
   @grep -E '^new-[^:]*:.*##' $(MAKEFILE_LIST) | ...
   ```

3. Add the targets:
   ```makefile
   # ============================================================================
   # New Category
   # ============================================================================

   new-target: ## Description of new target
   	command-here
   ```

### Example: Adding a Benchmark Target

```makefile
# In .PHONY section:
.PHONY: bench bench-cpu bench-mem

# In appropriate section:
# ============================================================================
# Benchmarking
# ============================================================================

bench: ## Run all benchmarks
	go test -bench=. -benchmem ./...

bench-cpu: ## Run benchmarks with CPU profiling
	go test -bench=. -cpuprofile=cpu.prof ./...
	go tool pprof -http=:8080 cpu.prof

bench-mem: ## Run benchmarks with memory profiling
	go test -bench=. -memprofile=mem.prof ./...
	go tool pprof -http=:8080 mem.prof
```

---

## Integration with CI/CD

The Makefile is designed to work seamlessly with CI/CD systems:

### GitHub Actions Example

```yaml
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: cachix/install-nix-action@v27
      - run: make check-nix
      - run: make build-nix
      - run: make test-integration
```

### Local CI Simulation

```bash
# Run exactly what CI runs
make ci

# This runs:
# 1. make check-nix    - Validate Nix flake
# 2. make fmt-nix      - Check Nix formatting
# 3. make lint         - Run Go linters
# 4. make test-unit    - Run Go tests
```

---

## Troubleshooting

### "Command not found" errors

```bash
# Most commands require the Nix development shell
make dev
# Then run your command
make build
```

### Nix evaluation errors

```bash
# Validate the flake first
make check-nix

# If it fails, check git status (Nix needs tracked files)
git add -A
make check-nix
```

### Container build failures

```bash
# Ensure Docker is running
docker ps

# Check Nix can build
make build-nix

# Then build container
make container
```

### Integration tests fail to start

```bash
# Integration tests require KVM (Linux only)
ls /dev/kvm

# Check virtualization is enabled
grep -E 'vmx|svm' /proc/cpuinfo
```

---

## Design Decisions

### Why Make over Just/Task/etc.?

1. **Universal availability**: Make is pre-installed on most systems
2. **No learning curve**: Most developers know basic Make syntax
3. **IDE support**: Syntax highlighting and completion widely available
4. **Simplicity**: Our needs don't require advanced task runner features

### Why Wrap Nix Commands?

1. **Discoverability**: `make help` is easier than reading `flake.nix`
2. **Consistency**: Same interface whether using Nix features or not
3. **Defaults**: Reasonable defaults for common operations
4. **Abstraction**: Users don't need to know Nix to use the project

### Why Profile-Based Targets?

1. **Reduced cognitive load**: `make swarm-client-stress` vs. remembering flags
2. **Tested configurations**: Profiles are pre-validated combinations
3. **Documentation**: Profile names are self-documenting
4. **Extensibility**: Easy to add new profiles as needs evolve

---

## Related Documentation

- [PORTS.md](PORTS.md) — **Port configuration** (all ports, defaults, how to change)
- [MICROVM_NETWORKING.md](MICROVM_NETWORKING.md) — **High-performance TAP networking** for MicroVMs
- [LOAD_TESTING.md](LOAD_TESTING.md) — **Load testing guide** (scripts, expected output)
- [NIX_FLAKE_DESIGN.md](NIX_FLAKE_DESIGN.md) — Nix flake structure
- [CLIENT_DEPLOYMENT.md](CLIENT_DEPLOYMENT.md) — Client container/VM details
- [TEST_ORIGIN.md](TEST_ORIGIN.md) — Test origin server details
- [CONTRIBUTING.md](../CONTRIBUTING.md) — Contribution guidelines
