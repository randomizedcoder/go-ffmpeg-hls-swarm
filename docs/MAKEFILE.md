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
make test-origin       # Run test HLS origin server
make swarm-client      # Run load test client
```

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              Makefile                                        │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐    │
│  │   Build &    │  │ Development  │  │   Testing    │  │  Deployment  │    │
│  │     Run      │  │              │  │              │  │              │    │
│  ├──────────────┤  ├──────────────┤  ├──────────────┤  ├──────────────┤    │
│  │ build        │  │ dev          │  │ test         │  │ test-origin  │    │
│  │ build-nix    │  │ shell        │  │ test-unit    │  │ swarm-client │    │
│  │ run          │  │ lint         │  │ test-race    │  │ container    │    │
│  │ run-nix      │  │ fmt          │  │ test-coverage│  │ swarm-container│  │
│  │ clean        │  │ check        │  │ test-integration│              │    │
│  └──────────────┘  └──────────────┘  └──────────────┘  └──────────────┘    │
│                                                                              │
│  Underlying Tools:                                                           │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐                        │
│  │   Go    │  │   Nix   │  │ Docker  │  │  Shell  │                        │
│  └─────────┘  └─────────┘  └─────────┘  └─────────┘                        │
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

| Target | Description | Profile |
|--------|-------------|---------|
| `test-origin` | Default profile | 720p, 2s segments |
| `test-origin-low-latency` | Low latency | 1s segments |
| `test-origin-4k-abr` | Multi-bitrate 4K | Multiple renditions |
| `test-origin-stress` | Stress testing | Maximum throughput |

**Usage Examples**:

```bash
# Start test origin server
make test-origin

# Stream available at: http://localhost:8080/stream.m3u8

# Use low-latency profile for testing aggressive caching
make test-origin-low-latency
```

**What It Does**:
1. Starts FFmpeg generating test pattern HLS stream
2. Starts Nginx serving the stream
3. Exposes stream at `http://localhost:8080/stream.m3u8`
4. Runs until Ctrl+C

See [TEST_ORIGIN.md](TEST_ORIGIN.md) for detailed documentation.

---

### 5. Swarm Client (Load Tester)

**Purpose**: Run the HLS load testing client with various intensity profiles.

| Target | Description | Clients | Ramp Rate |
|--------|-------------|---------|-----------|
| `swarm-client` | Default | 50 | 5/sec |
| `swarm-client-gentle` | Gentle warm-up | 20 | 1/sec |
| `swarm-client-stress` | High load | 200 | 20/sec |
| `swarm-client-burst` | Thundering herd | 100 | 50/sec |
| `swarm-client-extreme` | Maximum load | 500 | 50/sec |

**Usage Examples**:

```bash
# Default load test against local origin
make test-origin &
sleep 5
make swarm-client

# Stress test with custom URL
make swarm-client-stress STREAM_URL=http://cdn.example.com/live/master.m3u8

# Gentle testing for baseline
make swarm-client-gentle STREAM_URL=http://localhost:8080/stream.m3u8
```

**Configuration via Environment**:

```bash
# Override stream URL (default: http://localhost:8080/stream.m3u8)
STREAM_URL=http://origin:8080/master.m3u8 make swarm-client

# Override via command line
make swarm-client STREAM_URL=http://origin:8080/master.m3u8
```

See [CLIENT_DEPLOYMENT.md](CLIENT_DEPLOYMENT.md) for detailed documentation.

---

### 6. Containers

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

### 7. Utility Targets

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

# Streaming configuration
STREAM_URL  ?= http://localhost:8080/stream.m3u8

# Nix configuration
NIX         := nix
NIX_BUILD   := $(NIX) build
NIX_RUN     := $(NIX) run
```

**Override Examples**:

```bash
# Custom output directory
make build OUTPUT_DIR=/tmp/build

# Custom LDFLAGS for debugging
make build LDFLAGS=""

# Custom stream URL
make swarm-client STREAM_URL=https://example.com/live.m3u8

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

- [NIX_FLAKE_DESIGN.md](NIX_FLAKE_DESIGN.md) — Nix flake structure
- [CLIENT_DEPLOYMENT.md](CLIENT_DEPLOYMENT.md) — Client container/VM details
- [TEST_ORIGIN.md](TEST_ORIGIN.md) — Test origin server details
- [CONTRIBUTING.md](../CONTRIBUTING.md) — Contribution guidelines
