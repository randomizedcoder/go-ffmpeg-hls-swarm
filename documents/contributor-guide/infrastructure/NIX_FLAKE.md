# Nix Flake Guide

> **Type**: Contributor Documentation

Guide to the Nix flake structure and how to work with Nix in this project.

---

## Overview

This project uses Nix flakes for reproducible builds, development environments, and deployments.

### Key Features

- **Reproducible builds**: Same inputs → same outputs
- **Development shells**: Consistent tooling
- **Multiple outputs**: Binary, containers, MicroVMs
- **Profile system**: Different configurations

---

## Flake Structure

```
flake.nix                     # Main flake definition
├── nix/
│   ├── apps.nix              # Runnable applications
│   ├── checks.nix            # CI checks
│   ├── package.nix           # Go binary package
│   ├── container.nix         # Container utilities
│   ├── swarm-client/         # Swarm client configs
│   │   ├── config.nix        # Profile definitions
│   │   ├── container.nix     # Container build
│   │   └── runner.nix        # Shell scripts
│   └── test-origin/          # Test origin server
│       ├── default.nix       # Main export
│       ├── config.nix        # Profile config
│       ├── config/           # Per-profile configs
│       ├── container.nix     # Container build
│       ├── ffmpeg.nix        # FFmpeg command generation
│       ├── nginx.nix         # Nginx configuration
│       ├── nixos-module.nix  # NixOS module
│       └── runner.nix        # Shell scripts
```

---

## Common Commands

### Development

```bash
# Enter development shell
nix develop

# Format Nix files
nix fmt

# Check flake validity
nix flake check --no-build
```

### Building

```bash
# Build Go binary
nix build

# Build specific output
nix build .#test-origin-container
nix build .#swarm-client-container

# List all outputs
nix flake show
```

### Running

```bash
# Run default app
nix run

# Run specific app
nix run .#test-origin
nix run .#test-origin-low-latency
nix run .#swarm-client
```

---

## Outputs Reference

### Packages

| Output | Description |
|--------|-------------|
| `.#default` | Go binary (go-ffmpeg-hls-swarm) |
| `.#test-origin-container` | Test origin OCI image |
| `.#swarm-client-container` | Swarm client OCI image |
| `.#test-origin-vm` | Test origin MicroVM |

### Apps

| Output | Description |
|--------|-------------|
| `.#default` | Welcome message |
| `.#run` | Run swarm client binary |
| `.#test-origin` | Run test origin (default profile) |
| `.#test-origin-low-latency` | Low-latency profile |
| `.#test-origin-4k-abr` | 4K ABR profile |
| `.#test-origin-stress` | Stress test profile |
| `.#up` | Unified deployment CLI |

### Development Shells

```bash
nix develop           # Default shell with Go, FFmpeg, etc.
```

### Checks

```bash
nix flake check       # Run all checks
```

---

## Profile System

Profiles define configuration variants for the test origin:

### Available Profiles

| Profile | Segment Duration | Resolution | Use Case |
|---------|------------------|------------|----------|
| `default` | 2s | 1080p | Standard testing |
| `low-latency` | 1s | 720p | Latency testing |
| `4k-abr` | 2s | 4K ABR | Multi-bitrate testing |
| `stress` | 2s | 4K | Maximum throughput |
| `logged` | 2s | 1080p | With logging |
| `debug` | 2s | 1080p | Full debug logging |

### Adding a New Profile

1. Add profile to `nix/test-origin/config/profile-list.nix`
2. Create config in `nix/test-origin/config/<profile>.nix`
3. Profile is automatically available as `.#test-origin-<profile>`

---

## Container Builds

### Building

```bash
# Build container
nix build .#test-origin-container

# Load into Docker
docker load < ./result

# Run
docker run --rm -p 17080:17080 go-ffmpeg-hls-swarm-test-origin:latest
```

### How It Works

Uses `dockerTools.buildLayeredImage`:

```nix
pkgs.dockerTools.buildLayeredImage {
  name = "go-ffmpeg-hls-swarm-test-origin";
  tag = "latest";

  contents = [
    etcContents   # User/group files
    pkgs.ffmpeg-full
    pkgs.nginx
    # ...
  ];

  config = {
    Cmd = [ "${entrypoint}" ];
    User = "nginx";
    # ...
  };
}
```

---

## MicroVM Builds

### Building

```bash
# Build MicroVM
nix build .#test-origin-vm

# Run
nix run .#test-origin-vm
```

### How It Works

Uses `microvm.nix` to create a QEMU-based VM:

```nix
microvm = {
  hypervisor = "qemu";
  mem = 4096;
  vcpu = 4;

  interfaces = [{
    type = "user";
    id = "eth0";
  }];

  shares = [{
    source = "/nix/store";
    mountPoint = "/nix/.ro-store";
  }];
};
```

---

## Adding New Features

### New Package

1. Create `nix/my-feature.nix`
2. Import in `flake.nix`
3. Add to `packages` output

### New App

1. Add to `nix/apps.nix`:

```nix
my-app = mkApp (pkgs.writeShellApplication {
  name = "my-app";
  text = ''
    echo "Hello from my app"
  '';
});
```

### New Check

1. Add to `nix/checks.nix`:

```nix
my-check = pkgs.runCommand "my-check" {} ''
  echo "Running check..."
  touch $out
'';
```

---

## Debugging

### Evaluate Expression

```bash
# Evaluate Nix expression
nix eval .#packages.x86_64-linux.default

# Show derivation
nix show-derivation .#default
```

### Build with Logs

```bash
# Show build logs
nix build -L .#test-origin-container
```

### Enter Build Environment

```bash
# Debug a failing build
nix develop .#test-origin-container
```

---

## Common Issues

### "experimental-features" Error

Enable flakes in Nix config:

```bash
# ~/.config/nix/nix.conf
experimental-features = nix-command flakes
```

### "Hash mismatch" Error

Update flake.lock:

```bash
nix flake update
```

### Container Too Large

Check layers:

```bash
# Inspect image layers
docker history go-ffmpeg-hls-swarm:latest
```

---

## Related Documents

- [CONTAINERS.md](./CONTAINERS.md) - Container guide
- [MICROVMS.md](./MICROVMS.md) - MicroVM guide
- [CI_CD.md](./CI_CD.md) - CI/CD pipeline
