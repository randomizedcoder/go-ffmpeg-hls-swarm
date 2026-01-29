# Nix Test Scripts

> **Type**: Test Documentation  
> **Status**: Current  
> **Related**: [nix_test_scripts_design.md](../../docs/nix_test_scripts_design.md)

This directory contains automated test scripts for validating Nix flake outputs.

---

## Quick Start

### Run All Tests

```bash
./scripts/nix-tests/test-all.sh
```

### Run Individual Test Categories

```bash
# Fast evaluation tests (no builds)
./scripts/nix-tests/test-eval.sh

# Gatekeeper validation
./scripts/nix-tests/gatekeeper.sh

# Package builds
./scripts/nix-tests/test-packages.sh

# Container builds
./scripts/nix-tests/test-containers.sh

# Container execution (requires Docker)
./scripts/nix-tests/test-containers-env.sh

# MicroVM builds
./scripts/nix-tests/test-microvms.sh

# MicroVM network tests (requires KVM + sudo)
./scripts/nix-tests/test-microvms-network.sh

# ISO builds
./scripts/nix-tests/test-iso.sh

# App execution
./scripts/nix-tests/test-apps.sh

# Unified CLI
./scripts/nix-tests/test-cli.sh
```

---

## Network Setup for Container/MicroVM Testing

### Testing Enhanced Container or MicroVM

The enhanced container and MicroVMs (especially TAP networking) require network setup:

```bash
# 1. Teardown existing network (if any)
./scripts/network/teardown.sh

# 2. Setup fresh network
sudo ./scripts/network/setup.sh

# 3. Verify network
./scripts/network/check.sh

# 4. Run tests
./scripts/nix-tests/test-containers-env.sh
./scripts/nix-tests/test-microvms-network.sh

# 5. Cleanup (optional)
./scripts/network/teardown.sh
```

### Automated Network Testing

The `test-microvms-network.sh` script automatically:
- Tears down existing network
- Sets up fresh network
- Runs tests
- Cleans up network

**Note**: Requires sudo access for network setup/teardown.

---

## Test Scripts Overview

| Script | Purpose | Requirements | Time |
|--------|---------|--------------|------|
| `test-eval.sh` | Fast evaluation (no builds) | None | ~30s |
| `gatekeeper.sh` | Single source of truth validation | None | ~10s |
| `test-profiles.sh` | Profile accessibility | None | ~30s |
| `test-packages.sh` | Package builds | None | ~5-10min |
| `test-containers.sh` | Container builds | None | ~5-10min |
| `test-containers-env.sh` | Container execution | Docker | ~2-5min |
| `test-microvms.sh` | MicroVM builds | KVM | ~10-15min |
| `test-microvms-network.sh` | MicroVM network tests | KVM + sudo | ~1-2min |
| `test-iso.sh` | ISO builds | KVM (optional) | ~10-15min |
| `test-apps.sh` | App execution | None | ~1-2min |
| `test-cli.sh` | Unified CLI | None | ~30s |
| `test-all.sh` | Run all tests | Various | ~10-20min |

---

## Prerequisites

### For All Tests
- Nix installed and configured
- Git repository (for source tracking)

### For Container Execution Tests
- Docker installed and running
- `docker info` must succeed

### For MicroVM Tests
- Linux system
- KVM available (`/dev/kvm` exists and is readable/writable)
- For network tests: sudo access

### For ISO Tests
- Linux system
- KVM (optional, but speeds up builds)

---

## Common Issues

### "Git tree is dirty" Warning

**Cause**: Uncommitted changes in repository

**Solution**: Commit changes or ignore (warning is harmless)

### "KVM not available"

**Cause**: KVM not enabled or permissions incorrect

**Solution**:
```bash
# Check KVM module
lsmod | grep kvm

# Load KVM module (if needed)
sudo modprobe kvm_intel  # or kvm_amd

# Check permissions
ls -l /dev/kvm
# Should be: crw-rw-rw- ... /dev/kvm

# Fix permissions (if needed)
sudo chmod 666 /dev/kvm
```

### "Docker daemon not running"

**Cause**: Docker service not started

**Solution**:
```bash
# Start Docker (systemd)
sudo systemctl start docker

# Or check Docker status
sudo systemctl status docker
```

### "Network setup failed"

**Cause**: Network already exists or permissions issue

**Solution**:
```bash
# Teardown existing network first
./scripts/network/teardown.sh

# Then setup fresh
sudo ./scripts/network/setup.sh
```

---

## Test Results Interpretation

### Test Summary Format

```
════════════════════════════════════════════════════════════
Test Summary
════════════════════════════════════════════════════════════
Passed:  X
Failed:  Y
Skipped: Z
```

- **Passed**: Test succeeded
- **Failed**: Test failed (needs attention)
- **Skipped**: Test skipped (platform/requirements not met)

### When Tests Are Skipped

Tests are skipped (not failed) when:
- Platform requirements not met (e.g., Linux-only tests on macOS)
- Prerequisites missing (e.g., Docker, KVM)
- Optional features not available

**This is expected behavior** - skipped tests don't indicate problems.

---

## Continuous Integration

### Quick Checks (CI Fast Path)

```bash
# Fast evaluation only (~30s)
./scripts/nix-tests/test-eval.sh
./scripts/nix-tests/gatekeeper.sh
```

### Full Build Checks (CI Build Path)

```bash
# Build all packages (~10-15min)
./scripts/nix-tests/test-packages.sh
./scripts/nix-tests/test-containers.sh
```

### Full Test Suite (CI Full Path)

```bash
# Everything (~20-30min)
./scripts/nix-tests/test-all.sh
```

---

## Manual Testing Workflow

### Testing New Container

```bash
# 1. Build container
nix build .#go-ffmpeg-hls-swarm-container

# 2. Load into Docker
docker load < ./result

# 3. Run container
docker run --rm go-ffmpeg-hls-swarm:latest --help

# 4. Test with environment variables
docker run --rm \
  -e CLIENTS=10 \
  -e STREAM_URL=http://origin:8080/stream.m3u8 \
  go-ffmpeg-hls-swarm:latest
```

### Testing Enhanced Container

```bash
# 1. Setup network (if needed)
sudo ./scripts/network/setup.sh

# 2. Build container
nix build .#test-origin-container-enhanced

# 3. Load into Docker
docker load < ./result

# 4. Run container (needs special flags)
docker run --rm \
  --cap-add SYS_ADMIN \
  --tmpfs /tmp --tmpfs /run --tmpfs /run/lock \
  -p 8080:17080 \
  go-ffmpeg-hls-swarm-test-origin-enhanced:latest

# 5. Test health endpoint
curl http://localhost:8080/health

# 6. Cleanup
./scripts/network/teardown.sh
```

### Testing MicroVM

```bash
# 1. Setup network (for TAP networking)
sudo ./scripts/network/setup.sh

# 2. Build MicroVM
nix build .#test-origin-vm-tap

# 3. Run MicroVM
nix run .#test-origin-vm-tap

# 4. Test connectivity
curl http://10.177.0.10:17080/health

# 5. Cleanup
./scripts/network/teardown.sh
```

---

## See Also

- [Nix Test Scripts Design](../../docs/nix_test_scripts_design.md)
- [MicroVM Networking](../../docs/MICROVM_NETWORKING.md)
- [Network Scripts](../network/README.md)
