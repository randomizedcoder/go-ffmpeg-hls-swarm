# Port Configuration Guide

This document describes all network ports used by `go-ffmpeg-hls-swarm` and how to configure them.

## Quick Reference

| Port | Service | Default | Description |
|------|---------|---------|-------------|
| **17080** | HLS Origin (MicroVM) | `MICROVM_HTTP_PORT` | Nginx serving HLS streams |
| **17088** | HLS Origin (Local) | `ORIGIN_PORT` | Python HTTP server for quick tests |
| **17091** | Swarm Metrics | `METRICS_PORT` | Prometheus metrics from swarm client |
| **17100** | Node Exporter | — | Prometheus node-exporter (system metrics) |
| **17113** | Nginx Exporter | `MICROVM_METRICS_PORT` | Prometheus nginx-exporter |
| **17122** | SSH | — | SSH access to MicroVM |
| **17022** | MicroVM Console | — | TCP serial console for VM debugging |

### High-Performance Networking (TAP/Bridge)

When using TAP networking instead of user-mode NAT, the VM has a real routable IP address. **No port forwarding needed** - access services directly:

| Resource | Value | Purpose |
|----------|-------|---------|
| Bridge | `hlsbr0` | Dedicated bridge for HLS MicroVMs |
| Subnet | `10.177.0.0/24` | Isolated VM network |
| Gateway | `10.177.0.1` | Host address on bridge |
| VM IP | `10.177.0.10` | Static IP for origin VM |
| TAP Device | `hlstap0` | TAP interface for VM |

**Direct access via TAP (recommended):**
```bash
curl http://10.177.0.10:17080/health     # HLS origin
curl http://10.177.0.10:9100/metrics     # Node exporter (standard port)
curl http://10.177.0.10:9113/metrics     # Nginx exporter (standard port)
ssh root@10.177.0.10                      # SSH (standard port 22)
```

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

#### MicroVM Debug Console (Serial)

| Port | Purpose |
|------|---------|
| 17022 | TCP serial console for accessing VM shell |

> **Important:** Unlike other ports, 17022 is **NOT** forwarded to the VM. It's QEMU listening directly on the host, providing access to the VM's serial console (ttyS0). This is configured via `qemu.extraArgs = [ "-serial" "tcp:0.0.0.0:17022,server=on,wait=off" ]` and `qemu.serialConsole = false`.

The serial console is exposed by QEMU via this argument (visible in `ps ax | grep kvm`):

```
-serial tcp:0.0.0.0:17022,server=on,wait=off
```

This configures:
- **`tcp:0.0.0.0:17022`** — Listen on all interfaces, port 17022
- **`server=on`** — QEMU acts as server (clients connect to it)
- **`wait=off`** — VM boots immediately, doesn't wait for console connection

#### Connecting to the Serial Console

```bash
# Simple connection with netcat
nc localhost 17022

# Better terminal handling with socat (recommended)
socat -,rawer TCP:localhost:17022

# Or with telnet
telnet localhost 17022
```

After connecting, press **Enter** to get a login prompt. The VM uses `root` with no password by default.

#### Example Session

```bash
$ nc localhost 17022

hls-origin-vm login: root

[root@hls-origin-vm:~]# systemctl status hls-generator
● hls-generator.service - HLS Test Stream Generator
     Active: active (running) since ...

[root@hls-origin-vm:~]# journalctl -u hls-generator -f
-- FFmpeg encoding progress logs --

[root@hls-origin-vm:~]# journalctl -u nginx -f
-- Nginx access/error logs --

[root@hls-origin-vm:~]# ls -la /var/hls/
total 1234
drwxr-xr-x 2 ffmpeg nginx  4096 Jan 22 12:00 .
-rw-r--r-- 1 ffmpeg nginx   456 Jan 22 12:00 stream.m3u8
-rw-r--r-- 1 ffmpeg nginx 98765 Jan 22 12:00 segment000.ts
...

[root@hls-origin-vm:~]# curl -I localhost:17080/stream.m3u8
HTTP/1.1 200 OK
```

#### Useful Commands Inside the VM

```bash
# Check service status
systemctl status hls-generator   # FFmpeg HLS encoder
systemctl status nginx           # Nginx web server

# View logs
journalctl -u hls-generator -f   # FFmpeg progress/errors
journalctl -u nginx -f           # Nginx access logs
journalctl -xe                   # Recent system logs

# Check HLS output
ls -la /var/hls/                 # List HLS files
cat /var/hls/stream.m3u8         # View playlist

# Network diagnostics
ip addr                          # Check VM IP (10.177.0.10)
ss -tlnp                         # Listening ports
curl localhost:17080/stream.m3u8 # Test nginx locally

# System info
free -h                          # Memory usage
top                              # Process monitor
df -h                            # Disk usage
```

#### Troubleshooting Console Connection

If `nc localhost 17022` hangs with no output:

1. **Check the VM is running:**
   ```bash
   ps ax | grep 'microvm@hls-origin'
   ```

2. **Check port is listening:**
   ```bash
   ss -tlnp | grep 17022
   # Should show: LISTEN 0 1 0.0.0.0:17022 ...
   ```

3. **Check for existing connections** (only one client can connect at a time):
   ```bash
   ss -tnp | grep 17022
   # Kill any existing connection if needed
   ```

4. **Try pressing Enter** after connecting — the console may be waiting at a login prompt

### SSH Access (MicroVM)

SSH is enabled with root login (empty password) for testing and debugging purposes.

**User Mode (QEMU port forwarding):**
| Port | Purpose |
|------|---------|
| 17122 | SSH access via localhost (forwarded to VM port 22) |

```bash
ssh -o StrictHostKeyChecking=no -p 17122 root@localhost
scp -P 17122 root@localhost:/var/log/nginx/access.log ./
```

**TAP Mode (direct access):**
```bash
ssh root@10.177.0.10                      # Standard port 22
scp root@10.177.0.10:/var/log/nginx/access.log ./
```

> **Security Note**: SSH with empty password is intended for local testing only. Do not expose to untrusted networks.

### Prometheus Node Exporter (MicroVM)

The node exporter provides system-level metrics for monitoring VM resource usage during load tests.

**User Mode (QEMU port forwarding):**
| Port | Purpose |
|------|---------|
| 17100 | Node exporter via localhost (forwarded to VM port 9100) |

```bash
curl -s http://localhost:17100/metrics | head -20
```

**TAP Mode (direct access):**
```bash
curl -s http://10.177.0.10:9100/metrics | head -20   # Standard port
```

**Example queries:**
```bash
# CPU usage
curl -s http://<host>/metrics | grep node_cpu_seconds_total
# Memory
curl -s http://<host>/metrics | grep node_memory_MemAvailable_bytes
# Network
curl -s http://<host>/metrics | grep node_network_receive_bytes_total
```

**Key Metrics for Load Testing:**

| Metric | Description |
|--------|-------------|
| `node_cpu_seconds_total` | CPU time spent in each mode |
| `node_memory_MemAvailable_bytes` | Available memory |
| `node_network_receive_bytes_total` | Network bytes received |
| `node_network_transmit_bytes_total` | Network bytes sent |
| `node_disk_read_bytes_total` | Disk read bytes |
| `node_disk_written_bytes_total` | Disk write bytes |
| `node_load1` | 1-minute load average |

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

**User-mode networking only** - TAP mode doesn't need forwarding (direct access to VM IP):

```nix
# Only active when useTap = false
forwardPorts = [
  { from = "host"; host.port = 17080; guest.port = 17080; }  # HLS origin
  { from = "host"; host.port = 17122; guest.port = 22; }     # SSH
  { from = "host"; host.port = 17113; guest.port = 9113; }   # Nginx exporter
  { from = "host"; host.port = 17100; guest.port = 9100; }   # Node exporter
];
```

### 5. Go Source (`cmd/go-ffmpeg-hls-swarm/main.go`)

```go
// Default metrics address
metricsAddr = flag.String("metrics", "0.0.0.0:17091", "Prometheus metrics address")
```

### 6. NixOS Module (`nix/test-origin/nixos-module.nix`)

```nix
# HLS origin (uses custom port 17080)
services.nginx.virtualHosts."hls-origin".listen = [
  { addr = "0.0.0.0"; port = config.server.port; }  # 17080
];

# SSH (standard port 22)
services.openssh.enable = true;
services.openssh.settings.PermitRootLogin = "yes";

# Prometheus exporters (standard ports)
services.prometheus.exporters.nginx = {
  port = 9113;  # Standard nginx exporter port
};

services.prometheus.exporters.node = {
  port = 9100;  # Standard node exporter port
};
```

> **Note:** With TAP networking, access these directly at `10.177.0.10:<port>`.
> With user-mode networking, QEMU forwards `localhost:17xxx` to VM ports.

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

**Host Ports (always accessible via localhost):**

| Port | Service | Notes |
|------|---------|-------|
| 17022 | MicroVM serial console | QEMU listens directly on host |
| 17080 | HLS Origin | Same port inside VM |
| 17088 | HLS Origin (Local Python) | For quick local tests without VM |
| 17091 | Swarm Prometheus metrics | Go client metrics |

**User-Mode Port Forwarding (localhost → VM):**

| Host Port | VM Port | Service |
|-----------|---------|---------|
| 17100 | 9100 | Node exporter |
| 17113 | 9113 | Nginx exporter |
| 17122 | 22 | SSH |

> **TAP Mode:** No port forwarding needed. Access VM directly at `10.177.0.10` with standard ports (22, 9100, 9113).

### Alternative Range: 27000-27999

If `17xxx` conflicts with something, use `27xxx`:

```bash
export MICROVM_HTTP_PORT=27080
export ORIGIN_PORT=27088
export METRICS_PORT=27091
export MICROVM_METRICS_PORT=27113
```

## Firewall Considerations

For **local development**, typically no firewall changes needed - traffic stays on localhost or the bridge network.

For **remote access** to the VM services, open appropriate ports:

**User Mode (localhost forwarding):**
```bash
# UFW (Ubuntu)
sudo ufw allow 17080/tcp    # HLS origin
sudo ufw allow 17091/tcp    # Swarm metrics
sudo ufw allow 17100/tcp    # Node exporter (forwarded)
sudo ufw allow 17113/tcp    # Nginx exporter (forwarded)
sudo ufw allow 17122/tcp    # SSH (forwarded)
```

**TAP Mode (direct VM access):**
The VM is on a private bridge network (`10.177.0.0/24`). For external access, you'd need to route/NAT from your host's external interface. For local testing, no firewall changes needed.

```bash
# NixOS (configuration.nix) - for user mode
networking.firewall.allowedTCPPorts = [ 17080 17091 17100 17113 17122 ];
```

## Architecture Diagram

```
┌───────────────────────────────────────────────────────────────────────────┐
│                              Host Machine                                 │
│                                                                           │
│  ┌──────────────────────────┐    ┌──────────────────────────────────────┐│
│  │    Load Test Client      │    │     MicroVM (IP: 10.177.0.10)        ││
│  │                          │    │                                      ││
│  │   go-ffmpeg-hls-swarm    │    │   ┌─────────┐      ┌────────────┐   ││
│  │          │               │    │   │  FFmpeg │─────▶│  /var/hls  │   ││
│  │          │ spawns        │    │   │(encoder)│      └────────────┘   ││
│  │          ▼               │    │   └─────────┘            │          ││
│  │   ┌─────────────┐        │    │                          ▼          ││
│  │   │   FFmpeg    │        │    │                 ┌────────────────┐  ││
│  │   │  (clients)  │────────┼────┼─── HLS GET ────▶│     Nginx      │  ││
│  │   └─────────────┘        │    │                 │     :17080     │  ││
│  │                          │    │                 └───────┬────────┘  ││
│  │          │               │    │                         │           ││
│  │   ┌──────┴──────┐        │    │                  /nginx_status      ││
│  │   │  Metrics    │        │    │                         │           ││
│  │   │   :17091    │        │    │                         ▼           ││
│  │   └─────────────┘        │    │                ┌─────────────────┐  ││
│  │                          │    │                │  nginx-exporter │  ││
│  └──────────────────────────┘    │                │     :9113       │  ││
│                                  │                └─────────────────┘  ││
│                                  │                ┌─────────────────┐  ││
│                                  │                │  node-exporter  │  ││
│                                  │                │     :9100       │  ││
│                                  │                └─────────────────┘  ││
│                                  │                ┌─────────────────┐  ││
│                                  │                │      SSHD       │  ││
│                                  │                │     :22         │  ││
│                                  │                └─────────────────┘  ││
│                                  └──────────────────────────────────────┘│
│                                                                           │
│  ┌───────────────────────────────────────────────────────────────────────┐│
│  │                    Networking Modes                                   ││
│  │                                                                       ││
│  │  TAP Mode (recommended):          User Mode (fallback):               ││
│  │    Direct access to VM IP           QEMU port forwarding              ││
│  │    http://10.177.0.10:17080         http://localhost:17080            ││
│  │    http://10.177.0.10:9100          http://localhost:17100 → :9100    ││
│  │    http://10.177.0.10:9113          http://localhost:17113 → :9113    ││
│  │    ssh root@10.177.0.10             ssh -p 17122 root@localhost       ││
│  └───────────────────────────────────────────────────────────────────────┘│
│                                                                           │
│  ┌───────────────────────────────────────────────────────────────────────┐│
│  │                    Access Methods                                     ││
│  │                                                                       ││
│  │  Serial:  nc localhost:17022  ─────▶  QEMU ttyS0 ─────▶ VM shell      ││
│  │  SSH:     ssh root@10.177.0.10 (TAP) or ssh -p 17122 localhost (user) ││
│  └───────────────────────────────────────────────────────────────────────┘│
│                                                                           │
│  ┌───────────────────────────────────────────────────────────────────────┐│
│  │                    Prometheus Server (optional)                       ││
│  │                                                                       ││
│  │  Scrapes (TAP mode):                                                  ││
│  │    - http://localhost:17091/metrics      ← swarm client stats         ││
│  │    - http://10.177.0.10:9100/metrics     ← VM system metrics          ││
│  │    - http://10.177.0.10:9113/metrics     ← nginx stats                ││
│  └───────────────────────────────────────────────────────────────────────┘│
└───────────────────────────────────────────────────────────────────────────┘
```

## See Also

- [MICROVM_NETWORKING.md](MICROVM_NETWORKING.md) - High-performance TAP/bridge networking
- [TEST_ORIGIN.md](TEST_ORIGIN.md) - HLS origin server configuration
- [OBSERVABILITY.md](OBSERVABILITY.md) - Prometheus metrics details
- [LOAD_TESTING.md](LOAD_TESTING.md) - Running load tests
