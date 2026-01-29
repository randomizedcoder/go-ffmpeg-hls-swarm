# Technical Reference

> **Type**: Technical Reference
> **Status**: Current
> **Related**: [README.md](../README.md), [CONFIGURATION.md](CONFIGURATION.md)

This document provides a comprehensive technical reference for environment variables, flags, container healthchecks, platform support, and nginx configuration.

---

## Table of Contents

- [Environment Variables](#environment-variables)
- [Command-Line Flags](#command-line-flags)
- [Container Healthchecks](#container-healthchecks)
- [Platform Support Matrix](#platform-support-matrix)
- [Nginx Config Generator](#nginx-config-generator)
- [Profile Configuration](#profile-configuration)

---

## Environment Variables

### Swarm Client Container

The swarm client container accepts environment variables for configuration:

| Variable | Default | Description |
|----------|---------|-------------|
| `STREAM_URL` | **Required** | HLS stream URL (master playlist or variant) |
| `CLIENTS` | `50` | Number of concurrent clients |
| `RAMP_RATE` | `5` | Clients to start per second |
| `RAMP_JITTER` | `100` | Jitter in milliseconds for ramp-up |
| `METRICS_PORT` | `9090` | Prometheus metrics port |
| `LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `VARIANT` | `all` | Bitrate selection (all, highest, lowest, first) |
| `TIMEOUT` | `0` | Run duration in seconds (0 = forever) |
| `RECONNECT` | - | Enable reconnection (set to `1` to enable) |
| `NO_CACHE` | - | Add no-cache headers (set to `1` to enable) |
| `RESOLVE_IP` | - | Connect to specific IP (requires `DANGEROUS=1`) |
| `DANGEROUS` | - | Disable TLS verification (set to `1` to enable) |
| `EXTRA_ARGS` | - | Additional FFmpeg arguments |

**Example:**
```bash
docker run --rm \
  -e STREAM_URL=http://origin:8080/stream.m3u8 \
  -e CLIENTS=100 \
  -e RAMP_RATE=10 \
  -e VARIANT=highest \
  go-ffmpeg-hls-swarm:latest
```

### Test Origin Container

| Variable | Default | Description |
|----------|---------|-------------|
| `HLS_DIR` | `/var/hls` | Directory for HLS files |
| `PORT` | `17080` | HTTP server port |

---

## Command-Line Flags

### Swarm Client

**Orchestration:**
```bash
-clients int        Number of concurrent clients (default: 10)
-ramp-rate int      Clients to start per second (default: 5)
-ramp-jitter int    Jitter in milliseconds (default: 100)
-duration duration  Run duration, 0 = forever (default: 0)
```

**Variant Selection:**
```bash
-variant string     Bitrate selection: "all", "highest", "lowest", "first" (default: "all")
```

**Network / Testing:**
```bash
-resolve string     Connect to this IP (bypasses DNS, requires --dangerous)
-no-cache           Add no-cache headers (bypass CDN cache)
-header string      Add custom HTTP header (can repeat)
```

**Safety & Diagnostics:**
```bash
--dangerous         Required for -resolve (disables TLS verification)
--print-cmd         Print the FFmpeg command that would be run, then exit
--check             Validate config and run 1 client for 10 seconds, then exit
-v, --verbose       Verbose logging
```

**Metrics & Monitoring:**
```bash
-metrics-port int   Prometheus metrics port (default: 9090)
-tui                Enable TUI dashboard (interactive terminal only)
-origin-metrics-host string     Origin server hostname/IP for metrics
-origin-metrics-node-port int    Node exporter port (default: 9100)
-origin-metrics-nginx-port int   Nginx exporter port (default: 9113)
-origin-metrics-interval duration Scrape interval (default: 10s)
```

**Complete example:**
```bash
./go-ffmpeg-hls-swarm \
  -clients 100 \
  -ramp-rate 10 \
  -variant highest \
  -no-cache \
  -metrics-port 9090 \
  -tui \
  -origin-metrics-host 10.177.0.10 \
  http://10.177.0.10:17080/stream.m3u8
```

---

## Container Healthchecks

### Swarm Client Container

The swarm client container exposes Prometheus metrics for health monitoring:

```bash
# Health check via metrics endpoint
curl http://localhost:9090/metrics

# Check active clients
curl -s http://localhost:9090/metrics | grep swarm_clients_active
```

**Docker healthcheck:**
```dockerfile
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD curl -f http://localhost:9090/metrics || exit 1
```

### Test Origin Container

The test origin container exposes a health endpoint:

```bash
# Health check
curl http://localhost:17080/health

# Nginx status
curl http://localhost:17080/nginx_status
```

**Docker healthcheck:**
```dockerfile
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=3 \
  CMD curl -f http://localhost:17080/health || exit 1
```

**Enhanced container** (with systemd):
- Health endpoint: `http://localhost:17080/health`
- Nginx status: `http://localhost:17080/nginx_status`
- Nginx metrics: `http://localhost:9113/metrics` (Prometheus exporter)
- Node metrics: `http://localhost:9100/metrics` (Node exporter)

---

## Platform Support Matrix

| Package/Feature | x86_64-linux | aarch64-linux | x86_64-darwin | aarch64-darwin |
|----------------|--------------|---------------|---------------|----------------|
| **Go binary** | ✅ | ✅ | ✅ | ✅ |
| **Development shell** | ✅ | ✅ | ✅ | ✅ |
| **Test origin runner** | ✅ | ✅ | ✅ | ✅ |
| **Main binary container** | ✅ Build/Run | ✅ Build/Run | ✅ Build | ✅ Build |
| **Test origin container** | ✅ Build/Run | ✅ Build/Run | ✅ Build | ✅ Build |
| **Enhanced container** | ✅ Build/Run | ✅ Build/Run | ❌ | ❌ |
| **Test origin MicroVM** | ✅ | ⚠️ TBD | ❌ | ❌ |
| **Test origin ISO** | ✅ | ⚠️ TBD | ❌ | ❌ |
| **Swarm client container** | ✅ Build/Run | ✅ Build/Run | ✅ Build | ✅ Build |
| **Nginx config generator** | ✅ | ✅ | ✅ | ✅ |

**Notes:**
- **Build**: Can build the package on this platform
- **Run**: Can run the package on this platform
- **TBD**: To be determined (may work but not tested)

**Linux-only features:**
- Enhanced container (requires systemd)
- MicroVM (requires KVM)
- ISO image (requires NixOS)

---

## Nginx Config Generator

### Packages

View generated nginx configuration for any profile:

```bash
# Default profile
nix build .#test-origin-nginx-config
cat ./result

# Specific profiles
nix build .#test-origin-nginx-config-low-latency
nix build .#test-origin-nginx-config-4k-abr
nix build .#test-origin-nginx-config-stress
nix build .#test-origin-nginx-config-logged
nix build .#test-origin-nginx-config-debug
```

### App

Quick viewing via app:

```bash
# Default profile
nix run .#nginx-config

# Specific profile
nix run .#nginx-config low-latency
nix run .#nginx-config stress
nix run .#nginx-config 4k-abr
```

### Configuration Features

The generated nginx config includes:

**Performance optimizations:**
- `directio 4m` - Only use direct I/O for files > 4MB (allows OS page cache for segments)
- `keepalive_timeout 30` - Optimized for HLS polling
- `client_body_buffer_size 128k` - Minimal buffer (HLS origins don't accept POST)
- `aio threads=default` - Async I/O for high-load scenarios

**Security:**
- Method filtering - Only allows GET, HEAD, OPTIONS
- Returns 405 for other methods

**Caching:**
- Dynamic cache headers based on segment duration
- Master playlist: `max-age=5, stale-while-revalidate=10`
- Variant playlists: `max-age=1-2s, stale-while-revalidate=2-4s` (profile-dependent)
- Segments: `max-age=60, immutable`

**Logging:**
- Optional buffered logging for performance analysis
- Custom log formats for HLS performance metrics

See [Nginx Config Generator Design](NGINX_CONFIG_GENERATOR_DESIGN.md) for details.

---

## Profile Configuration

### Test Origin Profiles

| Profile | Segment Duration | Use Case |
|---------|-----------------|----------|
| `default` | 2s | Standard testing |
| `low-latency` | 1s | Low-latency testing |
| `4k-abr` | 2s | Multi-bitrate 4K streaming |
| `stress` | 2s | Maximum throughput |
| `logged` | 2s | With buffered segment logging |
| `debug` | 2s | Full logging with gzip compression |
| `tap` | 2s | High-performance TAP networking (MicroVM only) |
| `tap-logged` | 2s | TAP networking with logging |

### Swarm Client Profiles

| Profile | Clients | Ramp Rate | Use Case |
|---------|---------|-----------|----------|
| `default` | 50 | 5/sec | Standard testing |
| `stress` | 200 | 10/sec | Stress testing |
| `gentle` | 10 | 2/sec | Light testing |
| `burst` | 100 | 20/sec | Burst testing |
| `extreme` | 500 | 25/sec | Extreme stress testing |

### Profile Selection

**Via unified CLI:**
```bash
nix run .#up -- <profile> <type>
```

**Via direct packages:**
```bash
nix run .#test-origin-<profile>
nix run .#swarm-client-<profile>
```

**Examples:**
```bash
# Low-latency test origin
nix run .#up -- low-latency runner

# Stress test client
nix run .#up -- stress runner

# High-performance TAP MicroVM
nix run .#up -- tap vm
```

---

## FFmpeg HLS Flags

The test origin uses these FFmpeg HLS flags:

- `delete_segments` - Remove old .ts files automatically
- `omit_endlist` - Live stream (no #EXT-X-ENDLIST)
- `temp_file` - Atomic writes (write to .tmp then rename)
- `second_level_segment_index` - Better segment indexing for ABR

**Configuration:**
- Defined in `nix/test-origin/config/base.nix`
- Can be overridden per profile

---

## Network Configuration

### TAP Networking (High Performance)

For high-performance MicroVM networking:

**Setup:**
```bash
sudo ./scripts/network/setup.sh
```

**Configuration:**
- Bridge: `hlsbr0` (10.177.0.1/24)
- TAP device: `hlstap0` (multi-queue enabled)
- VM IP: `10.177.0.10` (static)
- Gateway: `10.177.0.1`

**Features:**
- Multi-queue TAP device for parallel packet processing
- vhost-net support for ~10 Gbps performance
- Direct IP access (no port forwarding)

**Teardown:**
```bash
./scripts/network/teardown.sh
```

See [MicroVM Networking](MICROVM_NETWORKING.md) for details.

---

## Port Reference

| Service | Port | Description |
|---------|------|-------------|
| HLS Origin | 17080 | Main HLS server |
| Swarm Client Metrics | 9090 | Prometheus metrics |
| Node Exporter | 9100 | System metrics (VM) |
| Nginx Exporter | 9113 | Nginx metrics (VM) |
| SSH (user-mode) | 17122 | SSH access (user-mode networking) |
| SSH (TAP) | 22 | SSH access (TAP networking) |
| Console | 17022 | QEMU console |

---

## See Also

- [README.md](../README.md) - Quick start and overview
- [CONFIGURATION.md](CONFIGURATION.md) - Detailed configuration guide
- [TEST_ORIGIN.md](TEST_ORIGIN.md) - Test origin server documentation
- [MICROVM_NETWORKING.md](MICROVM_NETWORKING.md) - MicroVM networking setup
- [NGINX_CONFIG_GENERATOR_DESIGN.md](NGINX_CONFIG_GENERATOR_DESIGN.md) - Nginx config generator design
