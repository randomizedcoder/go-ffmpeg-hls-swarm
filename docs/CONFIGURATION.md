# Configuration Reference

> **Type**: User Documentation
> **Related**: [QUICKSTART.md](QUICKSTART.md), [DESIGN.md](DESIGN.md)

Complete reference for all CLI flags, configuration options, and usage examples.

**New here?** Start with the [Quick Start Guide](QUICKSTART.md) first.

---

## Table of Contents

- [1. CLI Flags](#1-cli-flags)
- [2. Flag Conventions](#2-flag-conventions)
- [3. Variant Selection Modes](#3-variant-selection-modes)
  - [3.1 Mode Comparison](#31-mode-comparison)
  - [3.2 ffprobe Behavior](#32-ffprobe-behavior)
- [4. Flag to FFmpeg Mapping](#4-flag-to-ffmpeg-mapping)
- [5. Common Recipes (Copy-Paste Ready)](#5-common-recipes-copy-paste-ready)
- [6. Example Usage (Detailed)](#6-example-usage-detailed)
  - [6.1 Basic Usage](#61-basic-usage)
  - [6.2 Variant Selection](#62-variant-selection)
  - [6.3 DNS Override](#63-dns-override-test-specific-server)
  - [6.4 Cache Bypass](#64-cache-bypass-stress-origin)
  - [6.5 Custom Headers](#65-custom-headers)
  - [6.6 Advanced Examples](#66-advanced-examples)
- [7. Generated FFmpeg Commands](#7-generated-ffmpeg-commands)
- [8. Config File (Future)](#8-config-file-future)

---

## 1. CLI Flags

```bash
go-ffmpeg-hls-swarm [flags] <HLS_URL>

Orchestration Flags:
  -clients int            Target number of concurrent clients (default 10)
  -ramp-rate int          Clients to start per second (default 5)
  -ramp-jitter duration   Random jitter per client start (default 200ms)
  -duration duration      Run duration, 0 = forever (default 0)

Variant Selection:
  -variant string              Which quality level(s) to download (default "all")
                               Options: "all", "highest", "lowest", "first"
  -probe-failure-policy string Behavior if ffprobe fails for highest/lowest (default "fallback")
                               Options: "fallback" (use first variant), "fail" (abort)

Network / DNS Override:
  -resolve string         Connect to this IP instead of DNS resolution
                          Requires --dangerous flag. Disables TLS verification.
                          Example: -resolve 192.168.1.100 --dangerous

HTTP Headers / Cache Control:
  -no-cache               Add no-cache headers to bypass CDN caches
                          Adds: Cache-Control: no-cache, no-store, must-revalidate
                                Pragma: no-cache
  -header string          Add custom HTTP header (can be specified multiple times)
                          Example: -header "X-Test-ID: load-test-001"

FFmpeg Flags:
  -ffmpeg string          Path to FFmpeg binary (default "ffmpeg")
  -user-agent string      HTTP User-Agent header (default "go-ffmpeg-hls-swarm/1.0")
  -timeout duration       Network read/write timeout (default 15s)
  -reconnect              Enable FFmpeg reconnect flags (default true)
  -reconnect-delay int    Max reconnect delay in seconds (default 5)
  -seg-retry int          Segment download retry count (default 3)

Health / Stall Detection:
  -target-duration duration  Expected HLS segment duration for stall detection (default 6s)
                             Stall threshold = 2× target-duration (default: 12s)
                             Tip: Match your stream's #EXT-X-TARGETDURATION
  -restart-on-stall          Kill and restart stalled clients (default false)
                             Stalled = no bytes downloaded for 2× target-duration

Observability Flags:
  -metrics string         Prometheus metrics address (default "0.0.0.0:9090")
  -v                      Verbose logging (includes FFmpeg stderr)
  -log-format string      Log format: "json" or "text" (default "json")

Safety & Diagnostic Flags:
  --dangerous             Required safety flag for security-sensitive options
                          Currently required for: -resolve
  --print-cmd             Print the FFmpeg command that would be run, then exit
  --check                 Validate config and run 1 client for 10 seconds, then exit
  --skip-preflight        Skip preflight checks (ulimit, ffmpeg existence)
```

---

## 2. Flag Conventions

go-ffmpeg-hls-swarm uses two flag styles with distinct purposes:

| Style | Example | Purpose |
|-------|---------|---------|
| **Single dash** | `-clients`, `-resolve`, `-v` | Normal operational flags |
| **Double dash** | `--dangerous`, `--check`, `--print-cmd` | Safety gates and diagnostic modes |

**Why the distinction?**

- **Double-dash flags** indicate "something unusual is about to happen":
  - `--dangerous` — Disables security features (TLS verification)
  - `--check` — Runs in validation mode instead of normal operation
  - `--print-cmd` — Prints and exits instead of running
  - `--skip-preflight` — Bypasses safety checks

This makes it harder to accidentally enable dangerous or unusual behavior.

---

## 3. Variant Selection Modes

### 3.1 Mode Comparison

| Mode | Description | FFmpeg Args | Use Case |
|------|-------------|-------------|----------|
| `all` | Download ALL quality levels simultaneously | `-map 0` | Maximum CDN stress |
| `highest` | Download highest bitrate only | `-map 0:p:{id}` (via ffprobe) | Simulating premium viewers |
| `lowest` | Download lowest bitrate only | `-map 0:p:{id}` (via ffprobe) | Simulating mobile/constrained viewers |
| `first` | Download first listed variant in playlist | `-map 0:v:0? -map 0:a:0?` | Fast startup, no probe needed |

> ⚠️ **Understanding `-variant all`**: Each client downloads **every quality level** simultaneously.
> This is NOT typical viewer behavior (viewers watch one rendition at a time).
> With 4 variants and 50 clients, you create **200 concurrent streams** (4× bandwidth per client).
> Use this for maximum CDN/origin stress testing, not realistic viewer simulation.

> ℹ️ **Understanding `-variant first`**: "First" means the first variant listed in the master playlist.
> Playlist ordering is **not standardized** — different encoders order variants differently:
> - Some list highest-to-lowest
> - Some list lowest-to-highest
> - Some use arbitrary order
>
> Use `first` when you want fast startup and don't care which quality level you get.
> For predictable quality selection, use `highest` or `lowest` instead.

### 3.2 ffprobe Behavior

For `highest` and `lowest` modes:

- **Run once at startup** (not per-client)
- Uses `variant_bitrate` tag from HLS playlist metadata
- **Not all CDNs/playlists include this tag** — some will fail

**Failure policy** (configurable via `-probe-failure-policy`):

| Policy | Behavior | When to Use |
|--------|----------|-------------|
| `fallback` (default) | Fall back to `first` variant, log warning | Graceful degradation |
| `fail` | Abort startup with error | Strict mode, require known-good playlists |

**Startup output:**
```
Probing variants from https://cdn.example.com/master.m3u8...
  Found 4 programs: 5000kbps, 2500kbps, 1200kbps, 600kbps
  Selected: program 0 (5000kbps) for "highest" mode
```

**On probe failure (fallback mode):**
```
⚠️  ffprobe failed: no variant_bitrate tags found
⚠️  Falling back to "first" variant mode
```

---

## 4. Flag to FFmpeg Mapping

| CLI Flag | FFmpeg Option | Notes |
|----------|---------------|-------|
| `-user-agent` | `-user_agent` | HTTP header |
| `-timeout` | `-rw_timeout` | Converted to microseconds |
| `-reconnect` | `-reconnect 1 -reconnect_streamed 1 -reconnect_on_network_error 1` | All-or-nothing |
| `-reconnect-delay` | `-reconnect_delay_max` | In seconds |
| `-seg-retry` | `-seg_max_retry` | HLS demuxer option |
| `-resolve` | URL rewrite + `-tls_verify 0` + `-headers "Host: ..."` | Requires `--dangerous` |
| `-no-cache` | `-headers "Cache-Control: ...\r\nPragma: ..."` | Cache-busting headers |
| `-header` | `-headers "..."` | Custom headers |

---

## 5. Common Recipes (Copy-Paste Ready)

Quick commands for common scenarios. Copy, modify the URL, and run.

### 🔥 "I want to stress test my CDN"

```bash
# 100 clients, all quality levels, 30-minute test
go-ffmpeg-hls-swarm -clients 100 -variant all -duration 30m \
  https://your-cdn.com/live/master.m3u8
```

### 🎯 "I want to test my origin server directly"

```bash
# Bypass CDN cache, hit origin with 50 clients
go-ffmpeg-hls-swarm -clients 50 -no-cache \
  https://your-cdn.com/live/master.m3u8
```

### 🖥️ "I want to test a specific edge server by IP"

```bash
# Direct IP connection (bypasses DNS, DISABLES TLS verification!)
go-ffmpeg-hls-swarm -clients 50 -resolve 10.0.0.50 --dangerous \
  https://your-cdn.com/live/master.m3u8
```

### 📱 "I want to simulate mobile viewers"

```bash
# Lowest quality only (typical mobile bandwidth)
go-ffmpeg-hls-swarm -clients 200 -variant lowest -duration 1h \
  https://your-cdn.com/live/master.m3u8
```

### 🎬 "I want to simulate premium viewers"

```bash
# Highest quality only (4K/HD viewers)
go-ffmpeg-hls-swarm -clients 100 -variant highest -duration 1h \
  https://your-cdn.com/live/master.m3u8
```

### 🔬 "I want to find the breaking point"

```bash
# Start with fewer clients, increase until you see failures
go-ffmpeg-hls-swarm -clients 50 -ramp-rate 5 -duration 5m https://...   # Stable?
go-ffmpeg-hls-swarm -clients 100 -ramp-rate 5 -duration 5m https://...  # Still stable?
go-ffmpeg-hls-swarm -clients 200 -ramp-rate 5 -duration 5m https://...  # Breaking yet?
```

---

## 6. Example Usage (Detailed)

### 6.1 Basic Usage

```bash
# Quick smoke test (5 clients, all variants)
go-ffmpeg-hls-swarm -clients 5 https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8

# Ramp up 100 clients at 10/sec
go-ffmpeg-hls-swarm -clients 100 -ramp-rate 10 https://cdn.example.com/live/master.m3u8
```

### 6.2 Variant Selection

```bash
# Maximum stress: download ALL quality levels (default)
go-ffmpeg-hls-swarm -clients 100 -variant all https://cdn.example.com/live/master.m3u8

# Simulate premium viewers: highest bitrate only
go-ffmpeg-hls-swarm -clients 100 -variant highest https://cdn.example.com/live/master.m3u8

# Simulate mobile/constrained viewers: lowest bitrate only
go-ffmpeg-hls-swarm -clients 100 -variant lowest https://cdn.example.com/live/master.m3u8

# Fast startup (no ffprobe): first listed variant (quality varies by playlist)
go-ffmpeg-hls-swarm -clients 100 -variant first https://cdn.example.com/live/master.m3u8
```

### 6.3 DNS Override (Test Specific Server)

```bash
# Test a specific CDN edge node by IP (DISABLES TLS VERIFICATION)
go-ffmpeg-hls-swarm -clients 50 \
  -resolve 192.168.1.100 --dangerous \
  https://cdn.example.com/live/master.m3u8

# Test origin server directly, bypassing CDN
go-ffmpeg-hls-swarm -clients 20 \
  -resolve 10.0.0.50 --dangerous \
  https://cdn.example.com/live/master.m3u8
```

### 6.4 Cache Bypass (Stress Origin)

```bash
# Bypass CDN cache with no-cache headers
go-ffmpeg-hls-swarm -clients 100 -no-cache \
  https://cdn.example.com/live/master.m3u8

# Combine: hit specific origin with cache bypass (maximum origin stress)
go-ffmpeg-hls-swarm -clients 50 \
  -resolve 10.0.0.50 --dangerous \
  -no-cache \
  https://cdn.example.com/live/master.m3u8
```

### 6.5 Custom Headers

```bash
# Add test identification header
go-ffmpeg-hls-swarm -clients 100 \
  -header "X-Test-ID: load-test-2026-01-17" \
  -header "X-Test-Client: go-ffmpeg-hls-swarm" \
  https://cdn.example.com/live/master.m3u8
```

### 6.6 Advanced Examples

```bash
# 30-minute load test with custom user agent
go-ffmpeg-hls-swarm -clients 200 -ramp-rate 5 -duration 30m \
  -user-agent "LoadTest/1.0" \
  https://cdn.example.com/live/master.m3u8

# Aggressive reconnection settings for unstable networks
go-ffmpeg-hls-swarm -clients 50 -timeout 30s -reconnect-delay 10 -seg-retry 5 \
  https://cdn.example.com/live/master.m3u8

# Custom FFmpeg path with verbose logging
go-ffmpeg-hls-swarm -ffmpeg /opt/ffmpeg/bin/ffmpeg -v -clients 10 \
  https://cdn.example.com/live/master.m3u8

# Disable reconnection (for testing failure behavior)
go-ffmpeg-hls-swarm -clients 10 -reconnect=false https://cdn.example.com/live/master.m3u8

# Full stress test: specific origin + cache bypass + all variants
go-ffmpeg-hls-swarm -clients 100 -variant all \
  -resolve 10.0.0.50 --dangerous \
  -no-cache \
  -header "X-Test-Run: stress-test-001" \
  -ramp-rate 5 \
  https://cdn.example.com/live/master.m3u8

# Dry run: see what FFmpeg command would be generated
go-ffmpeg-hls-swarm --print-cmd -clients 50 -variant highest \
  https://cdn.example.com/live/master.m3u8

# Validation run: test config with 1 client for 10 seconds
go-ffmpeg-hls-swarm --check https://cdn.example.com/live/master.m3u8
```

---

## 7. Generated FFmpeg Commands

### Standard (all variants, normal DNS)

```bash
ffmpeg -hide_banner -nostdin -loglevel info \
  -reconnect 1 -reconnect_streamed 1 -reconnect_on_network_error 1 \
  -reconnect_delay_max 5 \
  -rw_timeout 15000000 \
  -user_agent "go-ffmpeg-hls-swarm/1.0" \
  -seg_max_retry 3 \
  -i "https://example.com/master.m3u8" \
  -map 0 \
  -c copy -f null -
```

### With `-no-cache` (cache bypass)

```bash
ffmpeg -hide_banner -nostdin -loglevel info \
  -reconnect 1 -reconnect_streamed 1 -reconnect_on_network_error 1 \
  -reconnect_delay_max 5 \
  -rw_timeout 15000000 \
  -user_agent "go-ffmpeg-hls-swarm/1.0" \
  -headers "Cache-Control: no-cache, no-store, must-revalidate\r\nPragma: no-cache\r\n" \
  -seg_max_retry 3 \
  -i "https://example.com/master.m3u8" \
  -map 0 \
  -c copy -f null -
```

### With `-resolve 192.168.1.100 --dangerous` (IP override)

```bash
ffmpeg -hide_banner -nostdin -loglevel info \
  -tls_verify 0 \
  -reconnect 1 -reconnect_streamed 1 -reconnect_on_network_error 1 \
  -reconnect_delay_max 5 \
  -rw_timeout 15000000 \
  -user_agent "go-ffmpeg-hls-swarm/1.0" \
  -headers "Host: example.com\r\n" \
  -seg_max_retry 3 \
  -i "https://192.168.1.100/master.m3u8" \
  -map 0 \
  -c copy -f null -
```

### With both `-resolve` and `-no-cache`

```bash
ffmpeg -hide_banner -nostdin -loglevel info \
  -tls_verify 0 \
  -reconnect 1 -reconnect_streamed 1 -reconnect_on_network_error 1 \
  -reconnect_delay_max 5 \
  -rw_timeout 15000000 \
  -user_agent "go-ffmpeg-hls-swarm/1.0" \
  -headers "Host: example.com\r\nCache-Control: no-cache, no-store, must-revalidate\r\nPragma: no-cache\r\n" \
  -seg_max_retry 3 \
  -i "https://192.168.1.100/master.m3u8" \
  -map 0 \
  -c copy -f null -
```

### With `-variant highest` or `-variant lowest`

```bash
# After ffprobe identifies program ID (e.g., program 0 = highest)
ffmpeg ... \
  -i "https://example.com/master.m3u8" \
  -map 0:p:0 \
  -c copy -f null -
```

### With `-variant first`

```bash
ffmpeg ... \
  -i "https://example.com/master.m3u8" \
  -map 0:v:0? -map 0:a:0? \
  -c copy -f null -
```

Use `--print-cmd` to see the exact command that would be generated for your configuration.

---

## 8. Config File (Future)

For complex scenarios, YAML config file support is planned:

```yaml
# go-ffmpeg-hls-swarm.yaml (future feature)

# Orchestration
orchestration:
  clients: 200
  ramp_rate_per_sec: 5
  ramp_jitter: 200ms
  duration: 30m

# FFmpeg settings
ffmpeg:
  binary_path: /usr/bin/ffmpeg
  variant_selection: highest    # "all", "highest", "lowest", "first"
  user_agent: "go-ffmpeg-hls-swarm/1.0"
  timeout: 15s
  reconnect: true
  reconnect_delay_max: 5
  seg_max_retry: 3
  log_level: info

# Network / DNS override (DANGEROUS - disables TLS verification)
network:
  resolve_ip: ""              # Leave empty for normal DNS resolution
  dangerous_mode: false       # Must be true if resolve_ip is set

# HTTP headers
http:
  no_cache: false             # Add cache-busting headers
  extra_headers:              # Custom headers
    - "X-Test-ID: load-test-001"

# Restart policy
restart:
  max_restarts: 0          # 0 = unlimited
  backoff_initial: 250ms
  backoff_max: 5s
  backoff_multiplier: 1.7

# Health / Stall detection
health:
  target_duration: 6s      # Expected HLS segment duration
  stall_multiplier: 2.0    # Stall if no bytes for N × target_duration
  restart_on_stall: false  # Kill and restart stalled clients

# Observability
metrics:
  address: "0.0.0.0:9090"

logging:
  format: json
  level: info

# Stream (required)
stream:
  url: "https://cdn.example.com/live/master.m3u8"
```

Usage (future):
```bash
go-ffmpeg-hls-swarm -config go-ffmpeg-hls-swarm.yaml
```
