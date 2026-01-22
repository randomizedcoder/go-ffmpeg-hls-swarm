# Port Configuration Guide

This document describes all network ports used by `go-ffmpeg-hls-swarm` and how to configure them.

## Quick Reference

| Port | Service | Default | Description |
|------|---------|---------|-------------|
| **17080** | HLS Origin (MicroVM) | `MICROVM_HTTP_PORT` | Nginx serving HLS streams |
| **17088** | HLS Origin (Local) | `ORIGIN_PORT` | Python HTTP server for quick tests |
| **17091** | Swarm Metrics | `METRICS_PORT` | Prometheus metrics from swarm client |
| **17113** | Nginx Exporter | `MICROVM_METRICS_PORT` | Prometheus nginx-exporter |
| **17022** | MicroVM Console | — | TCP serial console for VM debugging |

### High-Performance Networking (TAP/Bridge)

When using TAP networking instead of user-mode NAT:

| Resource | Value | Purpose |
|----------|-------|---------|
| Bridge | `hlsbr0` | Dedicated bridge for HLS MicroVMs |
| Subnet | `10.177.0.0/24` | Isolated VM network |
| Gateway | `10.177.0.1` | Host address on bridge |
| VM IP | `10.177.0.10` | Static IP for origin VM |
| TAP Device | `hlstap0` | TAP interface for VM |

With TAP networking, port forwarding (via nftables) maps `localhost:17xxx` → `10.177.0.10:17xxx`.

See [MICROVM_NETWORKING.md](MICROVM_NETWORKING.md) for setup instructions.

> **Why these ports?** We use the `17xxx` range to avoid conflicts with common services (8080, 9090, etc.). The "17" prefix is memorable and unlikely to conflict with other applications.

## Port Details

### HLS Origin Ports

#### MicroVM/Container Origin (Nginx)

| Port | Env Variable | Purpose |
|------|--------------|---------|
| 17080 | `MICROVM_HTTP_PORT` | HLS stream delivery (m3u8, .ts segments) |
| 17113 | `MICROVM_METRICS_PORT` | Nginx prometheus exporter metrics |

Nginx also exposes `/nginx_status` on the same HTTP port for stub_status metrics.

#### Local Test Origin (Python)

| Port | Env Variable | Purpose |
|------|--------------|---------|
| 17088 | `ORIGIN_PORT` | Python `http.server` serving HLS files |

Used by `make load-test-*` scripts for quick local testing without MicroVM.

#### MicroVM Debug Console

| Port | Purpose |
|------|---------|
| 17022 | TCP serial console for accessing VM shell |

> **Important:** Unlike other ports, 17022 is **NOT** forwarded to the VM. It's QEMU listening directly on the host, providing access to the VM's serial console (ttyS0). This is configured via `qemu.extraArgs = [ "-serial" "tcp:0.0.0.0:17022,server=on,wait=off" ]` and `qemu.serialConsole = false`.

Connect to the VM console for debugging:

```bash
# Using netcat
nc localhost 17022

# Using socat (better line handling)
socat -,rawer TCP:localhost:17022

# Inside the VM, you can run:
journalctl -u hls-generator -f   # FFmpeg logs
journalctl -u nginx -f           # Nginx logs
systemctl status hls-generator   # Check FFmpeg service
ls -la /var/hls/                 # Check HLS files
```

### Swarm Client Ports

| Port | Env Variable | Purpose |
|------|--------------|---------|
| 17091 | `METRICS_PORT` | Prometheus metrics (`/metrics` endpoint) |

Exposed metrics include:
- `hls_swarm_clients_active` - Currently running clients
- `hls_swarm_clients_started_total` - Total clients launched
- `hls_swarm_clients_restarted_total` - Restart count

## Configuration Locations

### 1. Scripts (`scripts/lib/common.sh`)

```bash
# Primary configuration - edit these to change defaults
export ORIGIN_PORT="${ORIGIN_PORT:-17088}"
export METRICS_PORT="${METRICS_PORT:-17091}"
export MICROVM_HTTP_PORT="${MICROVM_HTTP_PORT:-17080}"
export MICROVM_METRICS_PORT="${MICROVM_METRICS_PORT:-17113}"
```

### 2. Makefile

```makefile
# MicroVM port checks reference these ports
microvm-check-ports:  # Checks 17080 and 17113
```

### 3. Nix Configuration (`nix/test-origin/config.nix`)

```nix
server = {
  port = 17080;  # Nginx listen port
};
```

### 4. MicroVM Port Forwarding (`nix/test-origin/microvm.nix`)

```nix
forwardPorts = [
  { from = "host"; host.port = 17080; guest.port = 17080; }
  { from = "host"; host.port = 17113; guest.port = 9113; }
];
```

### 5. Go Source (`cmd/go-ffmpeg-hls-swarm/main.go`)

```go
// Default metrics address
metricsAddr = flag.String("metrics", "0.0.0.0:17091", "Prometheus metrics address")
```

### 6. NixOS Module (`nix/test-origin/nixos-module.nix`)

```nix
services.nginx.virtualHosts."hls-origin".listen = [
  { addr = "0.0.0.0"; port = config.server.port; }  # 17080
];

services.prometheus.exporters.nginx = {
  port = 9113;  # Internal, forwarded to 17113 on host
};
```

## Changing Ports

### Temporary Override (Environment Variables)

```bash
# Local origin tests
ORIGIN_PORT=9000 METRICS_PORT=9092 make load-test-100

# MicroVM (requires rebuild)
# See "Permanent Change" below
```

### Permanent Change

To permanently change ports, update these files:

| File | What to Change |
|------|----------------|
| `scripts/lib/common.sh` | Default environment variables |
| `nix/test-origin/config.nix` | `server.port` value |
| `nix/test-origin/microvm.nix` | `forwardPorts` host ports |
| `Makefile` | Port numbers in `microvm-check-ports` |
| `cmd/go-ffmpeg-hls-swarm/main.go` | Default `-metrics` flag value |

After changing Nix files, rebuild:

```bash
nix build .#test-origin-vm -o result
```

## Port Conflict Resolution

### Check What's Using a Port

```bash
# Using lsof
lsof -i :17080

# Using ss
ss -tlnp | grep 17080

# Using netstat
netstat -tlnp | grep 17080
```

### Free a Port

```bash
# Kill process using the port
sudo fuser -k 17080/tcp

# Kill previous MicroVM
pkill -f 'qemu.*hls-origin'

# Kill previous test origin
pkill -f 'http.server 17088'
pkill -f 'testsrc2.*hls-test'
```

### Use Alternative Ports

```bash
# If 17080 is taken, use 27080
MICROVM_HTTP_PORT=27080 make microvm-origin

# For local tests
ORIGIN_PORT=27088 make load-test-100
```

## Port Ranges

### Reserved Ranges (Avoid These)

| Range | Common Usage |
|-------|--------------|
| 80, 443 | HTTP/HTTPS (requires root) |
| 1-1023 | Privileged ports |
| 3000-3999 | Node.js dev servers |
| 5000-5999 | Flask, other dev servers |
| 8080-8089 | Common HTTP alternatives |
| 9090-9099 | Prometheus ecosystem |

### Our Default Range: 17000-17999

| Port | Service |
|------|---------|
| 17080 | HLS Origin (MicroVM) |
| 17088 | HLS Origin (Local) |
| 17091 | Swarm Prometheus metrics |
| 17113 | Nginx Prometheus exporter |
| 17114-17199 | Reserved for future use |

### Alternative Range: 27000-27999

If `17xxx` conflicts with something, use `27xxx`:

```bash
export MICROVM_HTTP_PORT=27080
export ORIGIN_PORT=27088
export METRICS_PORT=27091
export MICROVM_METRICS_PORT=27113
```

## Firewall Considerations

If running on a server with a firewall, open these ports:

```bash
# UFW (Ubuntu)
sudo ufw allow 17080/tcp
sudo ufw allow 17091/tcp
sudo ufw allow 17113/tcp

# firewalld (Fedora/RHEL)
sudo firewall-cmd --add-port=17080/tcp --permanent
sudo firewall-cmd --add-port=17091/tcp --permanent
sudo firewall-cmd --add-port=17113/tcp --permanent
sudo firewall-cmd --reload

# NixOS (configuration.nix)
networking.firewall.allowedTCPPorts = [ 17080 17091 17113 ];
```

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                        Host Machine                             │
│                                                                 │
│  ┌─────────────────────┐    ┌─────────────────────────────────┐│
│  │   Load Test Client  │    │         MicroVM / Container     ││
│  │                     │    │                                 ││
│  │  go-ffmpeg-hls-swarm│    │  ┌─────────┐    ┌────────────┐ ││
│  │         │           │    │  │  FFmpeg │───▶│   /var/hls │ ││
│  │         │           │    │  └─────────┘    └────────────┘ ││
│  │         ▼           │    │                       │        ││
│  │  ┌─────────────┐    │    │                       ▼        ││
│  │  │  Prometheus │    │    │              ┌────────────────┐││
│  │  │   :17091    │    │    │              │     Nginx      │││
│  │  └─────────────┘    │    │              │     :17080     │││
│  │                     │    │              └────────────────┘││
│  │         │           │    │                       │        ││
│  │         │ HTTP      │    │  ┌────────────────────┘        ││
│  │         ▼           │    │  │                             ││
│  │  ┌─────────────┐    │    │  │  ┌─────────────────────┐   ││
│  │  │   FFmpeg    │────┼────┼──┼─▶│   nginx-exporter    │   ││
│  │  │  (clients)  │    │    │  │  │       :17113        │   ││
│  │  └─────────────┘    │    │  │  └─────────────────────┘   ││
│  │                     │    │  │                             ││
│  └─────────────────────┘    └──┼─────────────────────────────┘│
│                                │                               │
│  ┌─────────────────────────────┼─────────────────────────────┐│
│  │              Prometheus Server (optional)                 ││
│  │  Scrapes:                                                 ││
│  │    - http://localhost:17091/metrics (swarm client)        ││
│  │    - http://localhost:17113/metrics (nginx exporter)      ││
│  └───────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
```

## See Also

- [MICROVM_NETWORKING.md](MICROVM_NETWORKING.md) - High-performance TAP/bridge networking
- [TEST_ORIGIN.md](TEST_ORIGIN.md) - HLS origin server configuration
- [OBSERVABILITY.md](OBSERVABILITY.md) - Prometheus metrics details
- [LOAD_TESTING.md](LOAD_TESTING.md) - Running load tests
