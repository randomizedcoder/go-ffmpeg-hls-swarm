# Client Deployment — OCI Containers & MicroVMs

> **Status**: Design
> **Related**: [TEST_ORIGIN.md](TEST_ORIGIN.md), [MEMORY.md](MEMORY.md), [NIX_NGINX_REFERENCE.md](NIX_NGINX_REFERENCE.md)

---

## Overview

This document describes how to package and deploy `go-ffmpeg-hls-swarm` in:

1. **OCI Containers** — Portable, orchestrator-friendly (Docker, Podman, Kubernetes)
2. **MicroVMs** — Isolated, reproducible, full-kernel testing environment

Both deployment options mirror the [test origin server](TEST_ORIGIN.md) configuration for consistent, high-performance networking.

---

## Table of Contents

- [Architecture](#architecture)
- [Why Containerize the Client?](#why-containerize-the-client)
- [Configuration Profiles](#configuration-profiles)
- [Network Tuning](#network-tuning)
- [OCI Container](#oci-container)
- [MicroVM](#microvm)
- [NixOS Module](#nixos-module)
- [Modular File Structure](#modular-file-structure)
- [Nix Implementation](#nix-implementation)
- [Resource Sizing](#resource-sizing)
- [Integration Testing](#integration-testing)
- [Operational Considerations](#operational-considerations)

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    go-ffmpeg-hls-swarm Client Deployment                    │
└─────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────┐    ┌─────────────────────────────────┐
│         OCI Container               │    │           MicroVM               │
│  ┌───────────────────────────────┐  │    │  ┌───────────────────────────┐  │
│  │    go-ffmpeg-hls-swarm        │  │    │  │   Linux Kernel (tuned)    │  │
│  │         (binary)              │  │    │  │   • sysctl optimized      │  │
│  └──────────┬────────────────────┘  │    │  │   • high FD limits        │  │
│             │ spawns                 │    │  └───────────────────────────┘  │
│  ┌──────────▼────────────────────┐  │    │  ┌───────────────────────────┐  │
│  │      FFmpeg Processes         │  │    │  │  go-ffmpeg-hls-swarm      │  │
│  │  ┌───┐ ┌───┐ ┌───┐    ┌───┐  │  │    │  │       (systemd)           │  │
│  │  │ 0 │ │ 1 │ │ 2 │ .. │ N │  │  │    │  └───────────┬───────────────┘  │
│  │  └───┘ └───┘ └───┘    └───┘  │  │    │              │ spawns           │
│  └───────────────────────────────┘  │    │  ┌──────────▼────────────────┐  │
│  ┌───────────────────────────────┐  │    │  │    FFmpeg Processes       │  │
│  │   Prometheus Metrics (:9090)  │  │    │  │  ┌───┐ ┌───┐    ┌───┐    │  │
│  └───────────────────────────────┘  │    │  │  │ 0 │ │ 1 │ .. │ N │    │  │
└─────────────────────────────────────┘    │  │  └───┘ └───┘    └───┘    │  │
                                           │  └───────────────────────────┘  │
              │                            │  ┌───────────────────────────┐  │
              │                            │  │ Prometheus Metrics (:9090)│  │
              │        HTTP/HTTPS          │  └───────────────────────────┘  │
              │                            └─────────────────────────────────┘
              │                                          │
              └──────────────────┬───────────────────────┘
                                 │
                                 ▼
           ┌─────────────────────────────────────────────────────┐
           │                 HLS Origin Server                   │
           │       (CDN, Cache, or Test Origin MicroVM)          │
           │          http://origin:8080/stream.m3u8             │
           └─────────────────────────────────────────────────────┘
```

### Component Responsibilities

| Component | Responsibility |
|-----------|----------------|
| **go-ffmpeg-hls-swarm** | Orchestrate FFmpeg processes, expose metrics |
| **FFmpeg (N processes)** | Fetch HLS playlists and segments |
| **Prometheus Endpoint** | Export client count, errors, bytes, latency |
| **Sysctl Tuning** | Optimize TCP/IP stack for many connections |

---

## Why Containerize the Client?

| Benefit | Description |
|---------|-------------|
| **Reproducibility** | Same environment across dev, CI, and prod |
| **Scalability** | Spin up multiple containers for distributed testing |
| **Isolation** | Prevent resource contention with host system |
| **Orchestration** | Native Kubernetes/Docker Compose support |
| **Resource Limits** | Enforce CPU/memory bounds per container |
| **Portability** | Run on any container runtime |

### When to Use MicroVMs

| Scenario | Use MicroVM |
|----------|-------------|
| Full kernel sysctl tuning | ✓ |
| Network namespace isolation | ✓ |
| Testing with `tc qdisc` impairments | ✓ |
| CI/CD integration testing | ✓ |
| Maximum reproducibility | ✓ |
| Quick local development | Use container |
| Kubernetes deployment | Use container |

---

## Configuration Profiles

Mirror the test origin server profiles for consistent testing:

| Profile | Clients | Ramp Rate | Use Case |
|---------|---------|-----------|----------|
| `default` | 50 | 5/sec | Baseline testing |
| `stress` | 200 | 20/sec | High-load validation |
| `gentle` | 20 | 1/sec | Origin warm-up |
| `burst` | 100 | 50/sec | Thundering herd simulation |

### nix/swarm-client/config.nix

```nix
# Configuration for go-ffmpeg-hls-swarm client deployment
#
# Usage:
#   config = import ./config.nix { profile = "default"; };
#   config = import ./config.nix { profile = "stress"; overrides = { clients = 300; }; };
#
{ profile ? "default", overrides ? {} }:

let
  # ═══════════════════════════════════════════════════════════════════════════
  # Profile Definitions
  # ═══════════════════════════════════════════════════════════════════════════
  profiles = {
    default = {
      clients = 50;
      rampRate = 5;             # clients per second
      rampJitter = 100;         # milliseconds
      metricsPort = 9090;
      logLevel = "info";
      variant = "all";
      reconnect = true;
      reconnectDelayMax = 5;
      segMaxRetry = 3;
      timeout = 15;             # seconds
    };

    stress = {
      clients = 200;
      rampRate = 20;
      rampJitter = 50;
      metricsPort = 9090;
      logLevel = "warning";
      variant = "all";
      reconnect = true;
      reconnectDelayMax = 3;
      segMaxRetry = 2;
      timeout = 10;
    };

    gentle = {
      clients = 20;
      rampRate = 1;
      rampJitter = 500;
      metricsPort = 9090;
      logLevel = "info";
      variant = "first";
      reconnect = true;
      reconnectDelayMax = 10;
      segMaxRetry = 5;
      timeout = 30;
    };

    burst = {
      clients = 100;
      rampRate = 50;
      rampJitter = 10;
      metricsPort = 9090;
      logLevel = "warning";
      variant = "all";
      reconnect = false;        # No reconnect for burst testing
      reconnectDelayMax = 0;
      segMaxRetry = 1;
      timeout = 5;
    };
  };

  # Select profile
  base = profiles.${profile} or (throw "Unknown profile: ${profile}");

  # Apply overrides
  cfg = base // overrides;

in cfg // {
  # ═══════════════════════════════════════════════════════════════════════════
  # Derived Values
  # ═══════════════════════════════════════════════════════════════════════════
  derived = {
    # Estimated ramp-up duration
    rampDuration = cfg.clients / cfg.rampRate;

    # Memory estimate (see MEMORY.md)
    # ~19MB private + ~52MB shared (amortized across processes)
    estimatedMemoryMB = (cfg.clients * 19) + 64;

    # Recommended file descriptor limit
    # Each FFmpeg needs ~10 FDs, plus overhead
    recommendedFdLimit = (cfg.clients * 15) + 1000;

    # Recommended ephemeral ports
    # Each FFmpeg client uses 1-4 connections
    recommendedPorts = cfg.clients * 5;
  };

  # Profile metadata
  _profile = {
    name = profile;
    availableProfiles = builtins.attrNames profiles;
  };
}
```

---

## Network Tuning

### Client-Side Sysctl Considerations

The client needs similar tuning to the origin server, with emphasis on:

| Setting | Server Focus | Client Focus |
|---------|--------------|--------------|
| `tcp_rmem` / `tcp_wmem` | Large for segment delivery | Large for segment reception |
| `tcp_tw_reuse` | Important | **Critical** (many short connections) |
| `ip_local_port_range` | Less important | **Critical** (many outbound connections) |
| `somaxconn` | Inbound connections | Less important |
| `tcp_fin_timeout` | Connection cleanup | **Critical** (fast port recycling) |
| `tcp_fastopen` | Server-side TFO | Client-side TFO (mode=1) |

### nix/swarm-client/sysctl.nix

```nix
# High-performance network tuning for HLS client
# Mirrors test-origin sysctl.nix with client-specific optimizations
#
{ config, pkgs, lib, ... }:

{
  boot.kernel.sysctl = {
    # ═══════════════════════════════════════════════════════════════════════
    # Connection Limits (Critical for many outbound connections)
    # ═══════════════════════════════════════════════════════════════════════

    # Maximum open files - each FFmpeg needs ~10 FDs
    "fs.file-max" = 2097152;

    # Network device backlog
    "net.core.netdev_max_backlog" = 65535;

    # ═══════════════════════════════════════════════════════════════════════
    # Ephemeral Port Range (CRITICAL for load testing clients)
    # Default: 32768-60999 (~28k ports)
    # Tuned:   1026-65535 (~64k ports)
    #
    # With 200 clients × 4 connections each = 800 ports minimum
    # Need headroom for TIME_WAIT states
    # ═══════════════════════════════════════════════════════════════════════
    "net.ipv4.ip_local_port_range" = "1026 65535";

    # ═══════════════════════════════════════════════════════════════════════
    # TIME_WAIT Management (CRITICAL for high connection churn)
    # ═══════════════════════════════════════════════════════════════════════

    # Reuse TIME_WAIT sockets for new connections
    # Safe with tcp_timestamps enabled
    "net.ipv4.tcp_tw_reuse" = 1;

    # Faster FIN_WAIT_2 timeout (default: 60s)
    # Frees ports faster when origin closes connection
    "net.ipv4.tcp_fin_timeout" = 15;

    # ═══════════════════════════════════════════════════════════════════════
    # TCP Keepalive - Detect dead connections faster
    # ═══════════════════════════════════════════════════════════════════════
    "net.ipv4.tcp_keepalive_time" = 120;
    "net.ipv4.tcp_keepalive_intvl" = 30;
    "net.ipv4.tcp_keepalive_probes" = 4;

    # ═══════════════════════════════════════════════════════════════════════
    # TCP Buffer Sizes - Large buffers for segment downloads
    # ═══════════════════════════════════════════════════════════════════════
    "net.ipv4.tcp_rmem" = "4096 1000000 16000000";
    "net.ipv4.tcp_wmem" = "4096 1000000 16000000";
    "net.ipv6.tcp_rmem" = "4096 1000000 16000000";
    "net.ipv6.tcp_wmem" = "4096 1000000 16000000";
    "net.core.rmem_default" = 26214400;
    "net.core.rmem_max" = 26214400;
    "net.core.wmem_default" = 26214400;
    "net.core.wmem_max" = 26214400;

    # ═══════════════════════════════════════════════════════════════════════
    # TCP Performance Optimizations
    # ═══════════════════════════════════════════════════════════════════════

    # Don't reset congestion window after idle
    # Critical: FFmpeg fetches segments every 2-6 seconds
    "net.ipv4.tcp_slow_start_after_idle" = 0;

    # TCP Fast Open - client mode (1) or both (3)
    # Reduces latency for playlist fetches
    "net.ipv4.tcp_fastopen" = 1;

    # Enable timestamps (required for tcp_tw_reuse)
    "net.ipv4.tcp_timestamps" = 1;

    # Selective ACK for faster recovery
    "net.ipv4.tcp_sack" = 1;
    "net.ipv4.tcp_fack" = 1;

    # Window scaling for large transfers
    "net.ipv4.tcp_window_scaling" = 1;

    # ECN for congestion notification
    "net.ipv4.tcp_ecn" = 1;

    # ═══════════════════════════════════════════════════════════════════════
    # Low Latency Settings
    # ═══════════════════════════════════════════════════════════════════════

    # Trigger socket writability earlier
    "net.ipv4.tcp_notsent_lowat" = 131072;

    # Minimum RTO: 50ms (faster retransmits)
    "net.ipv4.tcp_rto_min_us" = 50000;

    # Save slow-start threshold in route cache
    "net.ipv4.tcp_no_ssthresh_metrics_save" = 0;

    # Reflect TOS in replies
    "net.ipv4.tcp_reflect_tos" = 1;

    # ═══════════════════════════════════════════════════════════════════════
    # Queue Discipline
    # ═══════════════════════════════════════════════════════════════════════
    "net.core.default_qdisc" = "cake";
    "net.ipv4.tcp_congestion_control" = "cubic";
  };

  # Increase process and file limits
  security.pam.loginLimits = [
    { domain = "*"; type = "soft"; item = "nofile"; value = "1048576"; }
    { domain = "*"; type = "hard"; item = "nofile"; value = "1048576"; }
    { domain = "*"; type = "soft"; item = "nproc"; value = "unlimited"; }
    { domain = "*"; type = "hard"; item = "nproc"; value = "unlimited"; }
  ];
}
```

---

## OCI Container

### Design Goals

1. **Minimal image** — Only include required binaries
2. **Non-root execution** — Security best practice
3. **Configurable** — Environment variables for runtime config
4. **Observable** — Prometheus metrics exposed

### nix/swarm-client/container.nix

```nix
# OCI container image for go-ffmpeg-hls-swarm
#
# Build: nix build .#swarm-client-container
# Load:  docker load < ./result
# Run:   docker run --rm -e STREAM_URL=http://origin:8080/stream.m3u8 swarm-client
#
{ pkgs, lib, config, swarmBinary }:

let
  # Wrapper script with environment variable support
  entrypoint = pkgs.writeShellApplication {
    name = "swarm-entrypoint";
    runtimeInputs = [ swarmBinary pkgs.ffmpeg-full pkgs.ffprobe ];
    text = ''
      set -euo pipefail

      # Required
      : "''${STREAM_URL:?STREAM_URL environment variable is required}"

      # Optional with defaults from config
      CLIENTS="''${CLIENTS:-${toString config.clients}}"
      RAMP_RATE="''${RAMP_RATE:-${toString config.rampRate}}"
      METRICS_PORT="''${METRICS_PORT:-${toString config.metricsPort}}"
      LOG_LEVEL="''${LOG_LEVEL:-${config.logLevel}}"
      VARIANT="''${VARIANT:-${config.variant}}"

      echo "╔═══════════════════════════════════════════════════════════════╗"
      echo "║              go-ffmpeg-hls-swarm (container)                  ║"
      echo "╚═══════════════════════════════════════════════════════════════╝"
      echo ""
      echo "Profile:     ${config._profile.name}"
      echo "Stream URL:  $STREAM_URL"
      echo "Clients:     $CLIENTS"
      echo "Ramp Rate:   $RAMP_RATE/sec"
      echo "Metrics:     :$METRICS_PORT/metrics"
      echo ""

      # Verify FFmpeg
      echo "Verifying FFmpeg..."
      ffmpeg -version | head -1
      echo ""

      # Build command
      exec go-ffmpeg-hls-swarm \
        -clients "$CLIENTS" \
        -ramp-rate "$RAMP_RATE" \
        -metrics-port "$METRICS_PORT" \
        -log-level "$LOG_LEVEL" \
        -variant "$VARIANT" \
        ''${EXTRA_ARGS:-} \
        "$STREAM_URL"
    '';
  };

in pkgs.dockerTools.buildLayeredImage {
  name = "go-ffmpeg-hls-swarm";
  tag = "latest";

  contents = [
    pkgs.ffmpeg-full
    swarmBinary
    entrypoint
    # Minimal shell utilities for debugging
    pkgs.busybox
    pkgs.cacert
  ];

  config = {
    Entrypoint = [ "${entrypoint}/bin/swarm-entrypoint" ];

    ExposedPorts = {
      "${toString config.metricsPort}/tcp" = {};
    };

    Env = [
      "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
      "CLIENTS=${toString config.clients}"
      "RAMP_RATE=${toString config.rampRate}"
      "METRICS_PORT=${toString config.metricsPort}"
      "LOG_LEVEL=${config.logLevel}"
      "VARIANT=${config.variant}"
    ];

    Labels = {
      "org.opencontainers.image.title" = "go-ffmpeg-hls-swarm";
      "org.opencontainers.image.description" = "HLS load testing with FFmpeg process orchestration";
      "org.opencontainers.image.source" = "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm";
      "org.opencontainers.image.documentation" = "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm/blob/main/docs/CLIENT_DEPLOYMENT.md";
    };
  };

  # Enable fakeRootCommands for file permissions
  fakeRootCommands = ''
    mkdir -p /tmp
    chmod 1777 /tmp
  '';
}
```

### Container Usage

```bash
# Build the container
nix build .#swarm-client-container

# Load into Docker
docker load < ./result

# Run against test origin
docker run --rm \
  -e STREAM_URL=http://host.docker.internal:8080/stream.m3u8 \
  -e CLIENTS=50 \
  -p 9090:9090 \
  go-ffmpeg-hls-swarm:latest

# Run with custom arguments
docker run --rm \
  -e STREAM_URL=http://origin:8080/master.m3u8 \
  -e CLIENTS=100 \
  -e EXTRA_ARGS="--no-cache --dangerous --resolve 192.168.1.50" \
  -p 9090:9090 \
  go-ffmpeg-hls-swarm:latest
```

### Docker Compose Example

```yaml
# docker-compose.yml
version: "3.8"

services:
  origin:
    image: test-hls-origin:latest
    ports:
      - "8080:80"
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost/health"]
      interval: 5s
      timeout: 3s
      retries: 3

  swarm-client:
    image: go-ffmpeg-hls-swarm:latest
    depends_on:
      origin:
        condition: service_healthy
    environment:
      STREAM_URL: http://origin/stream.m3u8
      CLIENTS: 100
      RAMP_RATE: 10
    ports:
      - "9090:9090"
    deploy:
      resources:
        limits:
          memory: 4G
          cpus: "2"

  prometheus:
    image: prom/prometheus:latest
    ports:
      - "9091:9090"
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml
    command:
      - --config.file=/etc/prometheus/prometheus.yml
```

---

## MicroVM

### Why MicroVM for Client Testing?

| Feature | Benefit |
|---------|---------|
| Full kernel control | Apply all sysctl tunings |
| Network namespacing | Isolated networking stack |
| Traffic shaping | `tc qdisc` for latency simulation |
| Reproducibility | Identical environment every run |
| Resource accounting | Accurate CPU/memory measurement |

### nix/swarm-client/microvm.nix

```nix
# MicroVM configuration for go-ffmpeg-hls-swarm
#
# Build: nix build .#swarm-client-microvm
# Run:   ./result/bin/microvm-run
#
{ pkgs, lib, config, swarmBinary, nixosModule }:

let
  # MicroVM configuration
  vmConfig = {
    microvm = {
      hypervisor = "qemu";

      # Resource allocation based on client count
      mem = config.derived.estimatedMemoryMB + 256;  # MB
      vcpu = 2;

      # Networking
      interfaces = [{
        type = "user";
        id = "eth0";
        mac = "02:00:00:00:00:01";
      }];

      # Forward metrics port
      forwardPorts = [
        { from = "host"; host.port = config.metricsPort; guest.port = config.metricsPort; }
      ];

      # Shared directory for logs (optional)
      shares = [{
        tag = "logs";
        source = "/tmp/swarm-logs";
        mountPoint = "/var/log/swarm";
      }];
    };
  };

in {
  # NixOS configuration for the VM
  nixosConfiguration = { pkgs, lib, ... }: {
    imports = [
      nixosModule
      ./sysctl.nix
    ];

    # Basic system configuration
    system.stateVersion = "24.05";

    networking.hostName = "swarm-client";
    networking.useDHCP = true;

    # Increase file descriptor limits
    security.pam.loginLimits = [
      { domain = "*"; type = "soft"; item = "nofile"; value = "1048576"; }
      { domain = "*"; type = "hard"; item = "nofile"; value = "1048576"; }
    ];

    # Environment packages
    environment.systemPackages = [
      swarmBinary
      pkgs.ffmpeg-full
      pkgs.curl
      pkgs.htop
      pkgs.iotop
    ];

    # Swarm client systemd service
    systemd.services.swarm-client = {
      description = "go-ffmpeg-hls-swarm HLS Load Tester";
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      wantedBy = [ "multi-user.target" ];

      environment = {
        STREAM_URL = "\${STREAM_URL:-http://origin:8080/stream.m3u8}";
      };

      serviceConfig = {
        Type = "simple";
        ExecStart = "${swarmBinary}/bin/go-ffmpeg-hls-swarm -clients ${toString config.clients} -ramp-rate ${toString config.rampRate} -metrics-port ${toString config.metricsPort} \${STREAM_URL}";
        Restart = "on-failure";
        RestartSec = 5;

        # Resource limits
        LimitNOFILE = config.derived.recommendedFdLimit;
        LimitNPROC = "infinity";

        # Security hardening (optional)
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
      };
    };

    # Open metrics port
    networking.firewall.allowedTCPPorts = [ config.metricsPort ];
  };

  # Inherit MicroVM config
  inherit vmConfig;
}
```

### MicroVM Usage

```bash
# Build the MicroVM
nix build .#swarm-client-microvm

# Run the VM
./result/bin/microvm-run

# Or with custom stream URL
STREAM_URL=http://192.168.1.100:8080/master.m3u8 ./result/bin/microvm-run

# Access metrics from host
curl http://localhost:9090/metrics
```

---

## NixOS Module

Shared module for both container and MicroVM deployments:

### nix/swarm-client/nixos-module.nix

```nix
# NixOS module for go-ffmpeg-hls-swarm
#
# Reusable across MicroVMs, containers, and NixOS tests.
#
{ config, swarmBinary }:

{ pkgs, lib, ... }:

let
  cfg = config;
in
{
  imports = [ ./sysctl.nix ];

  # Required packages
  environment.systemPackages = [
    swarmBinary
    pkgs.ffmpeg-full
  ];

  # Systemd service
  systemd.services.go-ffmpeg-hls-swarm = {
    description = "HLS Load Testing Client (${cfg._profile.name} profile)";
    after = [ "network-online.target" ];
    wants = [ "network-online.target" ];

    serviceConfig = {
      Type = "simple";

      # Command built from config
      ExecStart = lib.concatStringsSep " " [
        "${swarmBinary}/bin/go-ffmpeg-hls-swarm"
        "-clients" (toString cfg.clients)
        "-ramp-rate" (toString cfg.rampRate)
        "-ramp-jitter" (toString cfg.rampJitter)
        "-metrics-port" (toString cfg.metricsPort)
        "-log-level" cfg.logLevel
        "-variant" cfg.variant
        (lib.optionalString cfg.reconnect "-reconnect")
        (lib.optionalString (cfg.reconnectDelayMax > 0) "-reconnect-delay-max ${toString cfg.reconnectDelayMax}")
        "-seg-max-retry" (toString cfg.segMaxRetry)
        "-timeout" (toString cfg.timeout)
        "\${STREAM_URL}"
      ];

      Restart = "on-failure";
      RestartSec = 5;

      # Resource limits
      LimitNOFILE = cfg.derived.recommendedFdLimit;
      LimitNPROC = "infinity";
    };
  };

  # Firewall
  networking.firewall.allowedTCPPorts = [ cfg.metricsPort ];
}
```

---

## Modular File Structure

```
nix/
├── swarm-client/
│   ├── default.nix           # Entry point (aggregates components)
│   ├── config.nix            # Configuration profiles
│   ├── sysctl.nix            # Kernel network tuning
│   ├── container.nix         # OCI container definition
│   ├── microvm.nix           # MicroVM definition
│   ├── nixos-module.nix      # Shared NixOS module
│   └── runner.nix            # Local development runner
```

### nix/swarm-client/default.nix

```nix
# go-ffmpeg-hls-swarm client deployment - Entry point
#
# Usage:
#   swarmClient = import ./swarm-client { inherit pkgs lib swarmBinary; };
#   swarmClient = import ./swarm-client { inherit pkgs lib swarmBinary; profile = "stress"; };
#
{ pkgs, lib, swarmBinary, profile ? "default", configOverrides ? {} }:

let
  # Load configuration
  config = import ./config.nix {
    inherit profile;
    overrides = configOverrides;
  };

  # Initialize components
  container = import ./container.nix { inherit pkgs lib config swarmBinary; };
  nixosModule = import ./nixos-module.nix { inherit config swarmBinary; };

  # Kernel tuning (importable separately)
  sysctlModule = ./sysctl.nix;

in {
  # Export configuration
  inherit config;

  # OCI container image
  inherit container;

  # NixOS module for systemd service
  inherit nixosModule;

  # Standalone sysctl module
  inherit sysctlModule;

  # Convenience aliases
  ociImage = container;

  # Available profiles
  availableProfiles = config._profile.availableProfiles;
  currentProfile = config._profile.name;

  # Derived values for inspection
  derived = config.derived;
}
```

---

## Resource Sizing

### Memory Estimation

From [MEMORY.md](MEMORY.md), FFmpeg processes share code efficiently:

| Clients | Private Memory | Shared (amortized) | Total Estimate |
|---------|----------------|-------------------|----------------|
| 50 | 950 MB | ~52 MB | ~1 GB |
| 100 | 1.9 GB | ~52 MB | ~2 GB |
| 200 | 3.8 GB | ~52 MB | ~4 GB |

**Formula**: `(clients × 19MB) + 64MB overhead`

### File Descriptors

Each FFmpeg process needs approximately 10-15 file descriptors:

| Clients | FDs Needed | Recommended Limit |
|---------|------------|-------------------|
| 50 | ~750 | 2,000 |
| 100 | ~1,500 | 3,000 |
| 200 | ~3,000 | 5,000 |

**Formula**: `(clients × 15) + 1000`

### Ephemeral Ports

Each FFmpeg client may use 1-4 concurrent TCP connections:

| Clients | Ports Needed | Available (tuned) |
|---------|--------------|-------------------|
| 50 | ~200 | ~64,000 |
| 100 | ~400 | ~64,000 |
| 200 | ~800 | ~64,000 |

With `tcp_tw_reuse=1` and 15-second `tcp_fin_timeout`, port exhaustion is unlikely.

---

## Integration Testing

### Combined Test: Origin + Client MicroVMs

```nix
# nix/tests/origin-client-integration.nix
{ pkgs, self }:

pkgs.testers.nixosTest {
  name = "hls-origin-client-integration";

  nodes = {
    origin = { pkgs, ... }: {
      imports = [
        self.nixosModules.test-origin
        self.nixosModules.test-origin-sysctl
      ];
      virtualisation.memorySize = 1024;
    };

    client = { pkgs, ... }: {
      imports = [
        self.nixosModules.swarm-client
        self.nixosModules.swarm-client-sysctl
      ];
      virtualisation.memorySize = 2048;

      # Override stream URL to point to origin
      systemd.services.go-ffmpeg-hls-swarm.environment.STREAM_URL =
        "http://origin:8080/stream.m3u8";
    };
  };

  testScript = ''
    import json

    # Start origin first
    origin.start()
    origin.wait_for_unit("nginx.service")
    origin.wait_for_unit("hls-generator.service")

    # Wait for HLS stream to be ready
    origin.wait_until_succeeds("curl -sf http://localhost/stream.m3u8", timeout=30)

    # Start client
    client.start()
    client.wait_for_unit("network-online.target")

    # Start swarm client
    client.succeed("systemctl start go-ffmpeg-hls-swarm")

    # Wait for clients to ramp up
    import time
    time.sleep(15)  # Allow ramp-up

    # Verify metrics endpoint
    metrics = client.succeed("curl -sf http://localhost:9090/metrics")
    assert "hlsswarm_clients_active" in metrics, "Missing active clients metric"

    # Check origin is receiving requests
    nginx_status = origin.succeed("curl -sf http://localhost/nginx_status")
    assert "Active connections:" in nginx_status

    # Parse and verify connection count
    # (Implementation depends on actual metrics format)

    # Graceful shutdown
    client.succeed("systemctl stop go-ffmpeg-hls-swarm")

    # Verify clean exit
    client.succeed("journalctl -u go-ffmpeg-hls-swarm | grep -q 'shutdown complete'")
  '';
}
```

### Traffic Shaping Test

```nix
# Test with simulated network latency
nodes.client = { pkgs, ... }: {
  # ... base config ...

  # Add latency simulation
  systemd.services.network-impairment = {
    description = "Simulate network latency";
    after = [ "network-online.target" ];
    before = [ "go-ffmpeg-hls-swarm.service" ];
    wantedBy = [ "multi-user.target" ];

    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      ExecStart = "${pkgs.iproute2}/bin/tc qdisc add dev eth0 root netem delay 50ms 10ms";
      ExecStop = "${pkgs.iproute2}/bin/tc qdisc del dev eth0 root";
    };
  };
};
```

---

## Operational Considerations

### Monitoring

```yaml
# prometheus.yml scrape config
scrape_configs:
  - job_name: 'swarm-client'
    static_configs:
      - targets: ['swarm-client:9090']
    relabel_configs:
      - source_labels: [__address__]
        target_label: instance
        regex: '(.+):\d+'
        replacement: '${1}'
```

### Key Metrics to Watch

| Metric | Alert Threshold | Meaning |
|--------|-----------------|---------|
| `hlsswarm_clients_active` | < expected | Client crashes |
| `hlsswarm_restarts_total` | > 10/min | Stream issues |
| `hlsswarm_errors_total` | > 1% of requests | Network/origin problems |
| `hlsswarm_bytes_received_total` | Rate drops | Bandwidth saturation |

### Kubernetes Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: hls-swarm-client
spec:
  replicas: 3  # Scale horizontally
  selector:
    matchLabels:
      app: hls-swarm-client
  template:
    metadata:
      labels:
        app: hls-swarm-client
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "9090"
    spec:
      containers:
      - name: swarm
        image: go-ffmpeg-hls-swarm:latest
        env:
        - name: STREAM_URL
          value: "http://origin-service/stream.m3u8"
        - name: CLIENTS
          value: "100"
        ports:
        - containerPort: 9090
          name: metrics
        resources:
          requests:
            memory: "2Gi"
            cpu: "1"
          limits:
            memory: "4Gi"
            cpu: "2"
        securityContext:
          # Note: sysctl tuning requires privileged or init containers
          # in Kubernetes. Consider using node-level tuning instead.
          allowPrivilegeEscalation: false
```

---

## Related Documentation

- [TEST_ORIGIN.md](TEST_ORIGIN.md) — Test HLS origin server design
- [MEMORY.md](MEMORY.md) — FFmpeg memory efficiency analysis
- [NIX_NGINX_REFERENCE.md](NIX_NGINX_REFERENCE.md) — Nginx configuration in Nix
- [OBSERVABILITY.md](OBSERVABILITY.md) — Prometheus metrics reference
- [OPERATIONS.md](OPERATIONS.md) — Operational guidance
