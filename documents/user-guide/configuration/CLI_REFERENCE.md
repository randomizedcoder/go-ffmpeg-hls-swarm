# CLI Reference

> **Type**: User Documentation
> **Source**: Verified against `internal/config/flags.go` and `internal/config/config.go`

Complete reference for all CLI flags with accurate defaults from the source code.

---

## Usage

```bash
go-ffmpeg-hls-swarm [flags] <HLS_URL>
```

---

## Flag Conventions

| Style | Example | Purpose |
|-------|---------|---------|
| **Single dash** | `-clients`, `-resolve`, `-v` | Normal operational flags |
| **Double dash** | `--dangerous`, `--check`, `--print-cmd` | Safety gates and diagnostic modes |

Double-dash flags indicate "something unusual is about to happen":
- `--dangerous` — Disables security features (TLS verification)
- `--check` — Runs in validation mode instead of normal operation
- `--print-cmd` — Prints and exits instead of running
- `--skip-preflight` — Bypasses safety checks

---

## Orchestration Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-clients` | int | 10 | Number of concurrent clients |
| `-ramp-rate` | int | 5 | Clients to start per second |
| `-ramp-jitter` | duration | 200ms | Random jitter per client start |
| `-duration` | duration | 0 (forever) | Run duration (0 = run until Ctrl+C) |

---

## Variant Selection

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-variant` | string | "all" | Which quality level(s) to download |
| `-probe-failure-policy` | string | "fallback" | Behavior if ffprobe fails |

**Variant options:**

| Mode | Description | FFmpeg Args | Use Case |
|------|-------------|-------------|----------|
| `all` | Download ALL quality levels simultaneously | `-map 0` | Maximum CDN stress |
| `highest` | Download highest bitrate only | `-map 0:p:{id}` (via ffprobe) | Simulate premium viewers |
| `lowest` | Download lowest bitrate only | `-map 0:p:{id}` (via ffprobe) | Simulate mobile viewers |
| `first` | Download first listed variant | `-map 0:v:0? -map 0:a:0?` | Fast startup, no probe |

**Probe failure policies:**

| Policy | Behavior | Use Case |
|--------|----------|----------|
| `fallback` | Fall back to `first` variant, log warning | Graceful degradation |
| `fail` | Abort startup with error | Strict mode |

---

## Network / Testing

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-resolve` | string | "" | Connect to this IP instead of DNS resolution |
| `-no-cache` | bool | false | Add no-cache headers to bypass CDN caches |
| `-header` | string | (repeatable) | Add custom HTTP header (can repeat) |

**Examples:**

```bash
# Bypass CDN cache
-no-cache

# Custom headers
-header "X-Test-ID: load-test-001" -header "X-Client: swarm"

# Direct IP connection (DISABLES TLS VERIFICATION!)
-resolve 192.168.1.100 --dangerous
```

---

## Safety & Diagnostics

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--dangerous` | bool | false | Required for -resolve (disables TLS verification) |
| `--print-cmd` | bool | false | Print FFmpeg command and exit |
| `--check` | bool | false | Validate config, run 1 client for 10 seconds |
| `--skip-preflight` | bool | false | Skip preflight checks (ulimit, FFmpeg existence) |

---

## Observability

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-metrics` | string | "0.0.0.0:17091" | Prometheus metrics address |
| `-v` | bool | false | Verbose logging |
| `-log-format` | string | "json" | Log format: "json" or "text" |

---

## FFmpeg Settings

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-ffmpeg` | string | "ffmpeg" | Path to FFmpeg binary |
| `-user-agent` | string | "go-ffmpeg-hls-swarm/1.0" | HTTP User-Agent header |
| `-timeout` | duration | 15s | Network read/write timeout |
| `-reconnect` | bool | true | Enable FFmpeg reconnect flags |
| `-reconnect-delay` | int | 5 | Max reconnect delay in seconds |
| `-seg-retry` | int | 3 | Segment download retry count |

---

## Health / Stall Detection

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-target-duration` | duration | 6s | Expected HLS segment duration for stall detection |
| `-restart-on-stall` | bool | false | Kill and restart stalled clients |

Stall threshold = 2x target-duration (default: 12s without progress = stalled).

---

## Stats Collection

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-stats` | bool | true | Enable FFmpeg output parsing for detailed stats |
| `-stats-loglevel` | string | "debug" | FFmpeg loglevel for stats: "verbose" or "debug" |
| `-stats-buffer` | int | 1000 | Lines to buffer per client pipeline |
| `-ffmpeg-debug` | bool | false | Enable FFmpeg -loglevel debug for detailed segment timing |

---

## Dashboard

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-tui` | bool | false | Enable live terminal dashboard |
| `-prom-client-metrics` | bool | false | Enable per-client Prometheus metrics (high cardinality!) |

> **Warning**: `-prom-client-metrics` creates high cardinality. Only use with <200 clients.

---

## Origin Metrics

Scrape metrics from the HLS origin server for integrated monitoring:

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-origin-metrics` | string | "" | Origin node_exporter URL (e.g., http://10.177.0.10:9100/metrics) |
| `-nginx-metrics` | string | "" | Origin nginx_exporter URL (e.g., http://10.177.0.10:9113/metrics) |
| `-origin-metrics-interval` | duration | 2s | Interval for scraping origin metrics |
| `-origin-metrics-window` | duration | 30s | Rolling window for percentiles (10s-300s) |
| `-origin-metrics-host` | string | "" | Hostname/IP for metrics (constructs URLs with default ports) |
| `-origin-metrics-node-port` | int | 9100 | Node exporter port (used with -origin-metrics-host) |
| `-origin-metrics-nginx-port` | int | 9113 | Nginx exporter port (used with -origin-metrics-host) |

**Examples:**

```bash
# Using explicit URLs
-origin-metrics http://10.177.0.10:9100/metrics \
-nginx-metrics http://10.177.0.10:9113/metrics

# Using host shorthand (uses default ports 9100, 9113)
-origin-metrics-host 10.177.0.10

# Custom ports with host
-origin-metrics-host 10.177.0.10 \
-origin-metrics-node-port 19100 \
-origin-metrics-nginx-port 19113
```

---

## Common Recipes

### Quick smoke test

```bash
go-ffmpeg-hls-swarm -clients 5 https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8
```

### Stress test CDN

```bash
go-ffmpeg-hls-swarm -clients 100 -variant all -duration 30m \
  https://your-cdn.com/live/master.m3u8
```

### Test origin directly (bypass cache)

```bash
go-ffmpeg-hls-swarm -clients 50 -no-cache \
  https://your-cdn.com/live/master.m3u8
```

### Test specific server by IP

```bash
go-ffmpeg-hls-swarm -clients 50 -resolve 192.168.1.100 --dangerous \
  https://your-cdn.com/live/master.m3u8
```

### Simulate mobile viewers

```bash
go-ffmpeg-hls-swarm -clients 200 -variant lowest -duration 1h \
  https://your-cdn.com/live/master.m3u8
```

### Find breaking point

```bash
# Start low, increase until failures
go-ffmpeg-hls-swarm -clients 50 -ramp-rate 5 -duration 5m https://...
go-ffmpeg-hls-swarm -clients 100 -ramp-rate 5 -duration 5m https://...
go-ffmpeg-hls-swarm -clients 200 -ramp-rate 5 -duration 5m https://...
```

### With TUI and origin metrics

```bash
go-ffmpeg-hls-swarm -clients 100 -tui \
  -origin-metrics-host 10.177.0.10 \
  http://10.177.0.10:17080/stream.m3u8
```

### Dry run (see FFmpeg command)

```bash
go-ffmpeg-hls-swarm --print-cmd -clients 50 -variant highest \
  https://cdn.example.com/live/master.m3u8
```

### Validation run

```bash
go-ffmpeg-hls-swarm --check https://cdn.example.com/live/master.m3u8
```

---

## Flag to FFmpeg Mapping

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
