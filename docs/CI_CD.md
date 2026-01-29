# CI/CD Guide

> **Type**: CI/CD Documentation
> **Status**: Current
> **Related**: [README.md](../README.md), [REFERENCE.md](REFERENCE.md)

This document provides examples and instructions for setting up CI/CD pipelines, remote builders, and automated testing.

---

## Table of Contents

- [GitHub Actions Examples](#github-actions-examples)
- [Remote Builders](#remote-builders)
- [Tiered Checks](#tiered-checks)
- [ARM64 Builds](#arm64-builds)
- [Caching Strategy](#caching-strategy)

---

## GitHub Actions Examples

### Basic CI Pipeline

```yaml
name: CI

on:
  push:
    branches: [main, server]
  pull_request:
    branches: [main, server]

jobs:
  # Fast evaluation checks (~30s)
  eval:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: DeterminateSystems/nix-installer-action@main
      - name: Run evaluation tests
        run: ./scripts/nix-tests/test-eval.sh
      - name: Run gatekeeper
        run: ./scripts/nix-tests/gatekeeper.sh

  # Build checks (~10-15 min)
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: DeterminateSystems/nix-installer-action@main
      - name: Build packages
        run: ./scripts/nix-tests/test-packages.sh
      - name: Build containers
        run: ./scripts/nix-tests/test-containers.sh

  # Full test suite (~20-30 min)
  test:
    runs-on: ubuntu-latest
    needs: [eval, build]
    steps:
      - uses: actions/checkout@v4
      - uses: DeterminateSystems/nix-installer-action@main
      - name: Run all tests
        run: ./scripts/nix-tests/test-all.sh
```

### Multi-Platform Build

```yaml
name: Multi-Platform Build

on:
  push:
    tags: ['v*']

jobs:
  build:
    strategy:
      matrix:
        system: [x86_64-linux, aarch64-linux, x86_64-darwin, aarch64-darwin]
    runs-on: ${{ matrix.system == 'x86_64-darwin' && 'macos-12' || matrix.system == 'aarch64-darwin' && 'macos-14' || 'ubuntu-latest' }}
    steps:
      - uses: actions/checkout@v4
      - uses: DeterminateSystems/nix-installer-action@main
      - name: Build container
        run: nix build .#go-ffmpeg-hls-swarm-container
      - name: Upload artifact
        uses: actions/upload-artifact@v4
        with:
          name: container-${{ matrix.system }}
          path: result
```

### Container Testing

```yaml
name: Container Tests

on:
  push:
    branches: [main]

jobs:
  container-test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: DeterminateSystems/nix-installer-action@main
      - name: Build containers
        run: ./scripts/nix-tests/test-containers.sh
      - name: Start Docker
        uses: docker/setup-buildx-action@v3
      - name: Test container execution
        run: ./scripts/nix-tests/test-containers-env.sh
```

### MicroVM Tests (Linux only)

```yaml
name: MicroVM Tests

on:
  push:
    branches: [main]

jobs:
  microvm-test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: DeterminateSystems/nix-installer-action@main
      - name: Enable KVM
        run: |
          sudo modprobe kvm
          sudo chmod 666 /dev/kvm
      - name: Build MicroVMs
        run: ./scripts/nix-tests/test-microvms.sh
      - name: Test MicroVM networking
        run: sudo ./scripts/nix-tests/test-microvms-network.sh
```

---

## Remote Builders

### Tailscale Remote Builder

**Setup:**

1. Install Tailscale on both machines:
```bash
# On local machine
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up

# On remote builder
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up
```

2. Configure Nix remote builder:
```bash
# On local machine, add to /etc/nix/nix.conf or ~/.config/nix/nix.conf
builders = ssh-ng://builder@100.x.x.x?remote-store=auto&ssh-key=/path/to/key
```

3. Test connection:
```bash
nix build --builders 'ssh-ng://builder@100.x.x.x?remote-store=auto' .#go-ffmpeg-hls-swarm
```

**Benefits:**
- Fast builds on powerful remote machines
- Shared Nix store cache
- Secure via Tailscale VPN

### Determinate Systems Remote Builder

**Setup:**

1. Sign up at [Determinate Systems](https://determinate.systems/)
2. Configure remote builder:
```bash
# Add to /etc/nix/nix.conf
builders = https://remote-builder.determinate.systems?token=YOUR_TOKEN
```

3. Use remote builder:
```bash
nix build --builders 'https://remote-builder.determinate.systems?token=YOUR_TOKEN' .#go-ffmpeg-hls-swarm
```

**Benefits:**
- Managed remote builders
- Multi-architecture support
- Automatic scaling

### Local Remote Builder

**Setup:**

1. On remote machine, enable Nix remote builder:
```bash
# Add to /etc/nix/nix.conf
builders-use-substitutes = true
```

2. On local machine, configure:
```bash
# Add to /etc/nix/nix.conf or ~/.config/nix/nix.conf
builders = ssh://builder@remote-host
```

3. Test:
```bash
nix build --builders 'ssh://builder@remote-host' .#go-ffmpeg-hls-swarm
```

---

## Tiered Checks

The project uses tiered checks for efficient CI/CD:

### Tier 1: Fast Checks (~30s)

**Use for:** Every commit, PR validation

```bash
# Evaluation only (no builds)
./scripts/nix-tests/test-eval.sh

# Gatekeeper validation
./scripts/nix-tests/gatekeeper.sh
```

**What it checks:**
- Package evaluation (no broken derivations)
- Single source of truth integrity
- Profile validation

### Tier 2: Build Checks (~10-15 min)

**Use for:** Pre-merge validation, release candidates

```bash
# Build all packages
./scripts/nix-tests/test-packages.sh

# Build containers
./scripts/nix-tests/test-containers.sh
```

**What it checks:**
- All packages build successfully
- Container images build correctly
- Cross-platform compatibility

### Tier 3: Full Tests (~20-30 min)

**Use for:** Release validation, comprehensive testing

```bash
# Run all tests
./scripts/nix-tests/test-all.sh
```

**What it checks:**
- Package builds
- Container builds and execution
- MicroVM builds (Linux only)
- App execution
- Unified CLI
- Nginx config generator

### GitHub Actions Example

```yaml
name: Tiered CI

on: [push, pull_request]

jobs:
  fast:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: DeterminateSystems/nix-installer-action@main
      - run: ./scripts/nix-tests/test-eval.sh
      - run: ./scripts/nix-tests/gatekeeper.sh

  build:
    runs-on: ubuntu-latest
    if: github.event_name == 'pull_request' || startsWith(github.ref, 'refs/tags/')
    steps:
      - uses: actions/checkout@v4
      - uses: DeterminateSystems/nix-installer-action@main
      - run: ./scripts/nix-tests/test-packages.sh
      - run: ./scripts/nix-tests/test-containers.sh

  full:
    runs-on: ubuntu-latest
    if: startsWith(github.ref, 'refs/tags/')
    needs: [fast, build]
    steps:
      - uses: actions/checkout@v4
      - uses: DeterminateSystems/nix-installer-action@main
      - run: ./scripts/nix-tests/test-all.sh
```

---

## ARM64 Builds

### GitHub Actions (Self-Hosted)

```yaml
name: ARM64 Build

on:
  push:
    branches: [main]

jobs:
  arm64-build:
    runs-on: self-hosted
    steps:
      - uses: actions/checkout@v4
      - uses: DeterminateSystems/nix-installer-action@main
      - name: Build ARM64 container
        run: nix build .#go-ffmpeg-hls-swarm-container --system aarch64-linux
      - name: Upload artifact
        uses: actions/upload-artifact@v4
        with:
          name: container-aarch64-linux
          path: result
```

### Cross-Compilation

```bash
# Build for ARM64 from x86_64
nix build .#go-ffmpeg-hls-swarm-container --system aarch64-linux

# Build for multiple architectures
for system in x86_64-linux aarch64-linux; do
  nix build .#go-ffmpeg-hls-swarm-container --system $system
done
```

### Remote Builder (ARM64)

```bash
# Configure remote ARM64 builder
builders = ssh-ng://builder@arm64-host?remote-store=auto&systems=aarch64-linux

# Build on remote ARM64 machine
nix build --builders 'ssh-ng://builder@arm64-host?remote-store=auto' \
  .#go-ffmpeg-hls-swarm-container --system aarch64-linux
```

---

## Caching Strategy

### Nix Caching

**Local cache:**
```bash
# Enable local binary cache
nix.settings.substituters = [ "https://cache.nixos.org" ];
nix.settings.trusted-public-keys = [ "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=" ];
```

**Project-specific cache:**
```bash
# Add to flake.nix or nix.conf
nix.settings.substituters = [
  "https://cache.nixos.org"
  "https://microvm.cachix.org"  # For MicroVM packages
];
nix.settings.trusted-public-keys = [
  "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="
  "microvm.cachix.org-1:oXnBc6hRE3eX5rSY3By5xCXZT3L4RPhG4C1zJjbfRts="
];
```

### GitHub Actions Caching

```yaml
- uses: actions/cache@v3
  with:
    path: /nix/store
    key: nix-store-${{ runner.os }}-${{ hashFiles('flake.lock') }}
    restore-keys: |
      nix-store-${{ runner.os }}-
```

### Container Layer Caching

Containers use `buildLayeredImage` for efficient layer caching:

```nix
pkgs.dockerTools.buildLayeredImage {
  name = "go-ffmpeg-hls-swarm";
  maxLayers = 100;  # Optimize layer count
  # ...
}
```

**Benefits:**
- Shared layers across builds
- Faster rebuilds
- Smaller registry storage

---

## Release Workflow

### Automated Release

```yaml
name: Release

on:
  push:
    tags: ['v*']

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: DeterminateSystems/nix-installer-action@main

      # Build all platforms
      - name: Build x86_64-linux
        run: nix build .#go-ffmpeg-hls-swarm-container --system x86_64-linux
      - name: Build aarch64-linux
        run: nix build .#go-ffmpeg-hls-swarm-container --system aarch64-linux

      # Run full test suite
      - name: Run tests
        run: ./scripts/nix-tests/test-all.sh

      # Create release
      - uses: softprops/action-gh-release@v1
        with:
          files: |
            result-x86_64-linux
            result-aarch64-linux
```

---

## See Also

- [README.md](../README.md) - Quick start and overview
- [REFERENCE.md](REFERENCE.md) - Technical reference
- [NIX_CACHING_STRATEGY.md](NIX_CACHING_STRATEGY.md) - Detailed caching strategy
- [Nix Test Scripts README](../scripts/nix-tests/README.md) - Test scripts documentation
