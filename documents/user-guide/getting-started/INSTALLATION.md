# Installation Guide

> **Time**: 5-10 minutes
> **Goal**: Install go-ffmpeg-hls-swarm and verify it works

## Prerequisites

| Requirement | How to Check | Install |
|-------------|--------------|---------|
| Go 1.25+ | `go version` | [golang.org/dl](https://golang.org/dl/) |
| FFmpeg | `ffmpeg -version` | `apt install ffmpeg` / `brew install ffmpeg` |
| Nix (optional) | `nix --version` | [nixos.org/download](https://nixos.org/download/) |

### Verify FFmpeg HLS Support

Before proceeding, confirm FFmpeg can fetch HLS streams:

```bash
ffmpeg -hide_banner -loglevel info \
  -i "https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8" \
  -t 5 -c copy -f null -
```

If you see `Input #0, hls...` followed by segment info, you're ready. Press `Ctrl+C` to stop early.

---

## Installation Methods

### Method 1: Build from Source (Go)

```bash
# Clone the repository
git clone https://github.com/randomizedcoder/go-ffmpeg-hls-swarm.git
cd go-ffmpeg-hls-swarm

# Build the binary
go build -o go-ffmpeg-hls-swarm ./cmd/go-ffmpeg-hls-swarm

# Verify
./go-ffmpeg-hls-swarm --help
```

### Method 2: Using Nix (Recommended)

Nix provides reproducible builds with all dependencies:

```bash
# Clone and enter the development shell
git clone https://github.com/randomizedcoder/go-ffmpeg-hls-swarm.git
cd go-ffmpeg-hls-swarm

# Build with Nix
nix build

# Binary is at ./result/bin/go-ffmpeg-hls-swarm
./result/bin/go-ffmpeg-hls-swarm --help
```

Or run directly without cloning:

```bash
# Run directly from flake
nix run github:randomizedcoder/go-ffmpeg-hls-swarm
```

### Method 3: Using Makefile

```bash
# Clone and build
git clone https://github.com/randomizedcoder/go-ffmpeg-hls-swarm.git
cd go-ffmpeg-hls-swarm

# Build (requires nix develop shell or Go installed)
make build

# Binary is at ./bin/go-ffmpeg-hls-swarm
./bin/go-ffmpeg-hls-swarm --help
```

### Method 4: Docker/Podman

```bash
# Build container image
nix build .#swarm-client-container

# Load into Docker/Podman
docker load < ./result

# Run
docker run --rm go-ffmpeg-hls-swarm:latest --help
```

---

## Verify Installation

Run a quick test against a public stream:

```bash
./go-ffmpeg-hls-swarm -clients 3 -duration 10s \
  https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8
```

Expected output:

```
Preflight checks:
  ✓ ffmpeg: found at /usr/bin/ffmpeg
  ✓ file_descriptors: 8192 available (need 160 for 3 clients)

Starting 3 clients at 5/sec...
  client_started id=0
  client_started id=1
  client_started id=2
  ramp_complete clients=3

... (runs for 10 seconds) ...

═══════════════════════════════════════════════════════════════════
                        go-ffmpeg-hls-swarm Exit Summary
═══════════════════════════════════════════════════════════════════
Run Duration:           00:00:10
Target Clients:         3
Peak Active Clients:    3
```

---

## Platform Notes

### Linux (Recommended)

Linux is the recommended platform for high-concurrency load testing:
- Full KVM support for MicroVMs
- Better file descriptor limits
- TAP networking for high-performance testing

### macOS

Works with limitations:
- No MicroVM support (requires KVM)
- File descriptor limits may need adjustment
- Container support depends on Docker Desktop

### Windows

Not officially tested. May work via WSL2 with Linux instructions.

---

## Development Shell

For development, use the Nix development shell which provides all tools:

```bash
# Enter development shell
nix develop

# Or via Makefile
make dev
```

This provides:
- Go 1.25+
- FFmpeg with HLS support
- golangci-lint
- gofumpt
- All build dependencies

---

## Next Steps

| Goal | Document |
|------|----------|
| Run your first load test | [QUICKSTART.md](QUICKSTART.md) |
| See all CLI options | [CLI_REFERENCE.md](../configuration/CLI_REFERENCE.md) |
| Run pre-built load tests | [LOAD_TESTING.md](../operations/LOAD_TESTING.md) |
