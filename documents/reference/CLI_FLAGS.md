# CLI Flags Reference

> **Type**: Quick Reference
> **Source**: Verified against `internal/config/flags.go`
> **Full guide**: [CLI_REFERENCE.md](../user-guide/configuration/CLI_REFERENCE.md)

---

## All Flags (Alphabetical)

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--check` | bool | false | Validate config, run 1 client for 10s |
| `-clients` | int | 10 | Number of concurrent clients |
| `--dangerous` | bool | false | Required for -resolve (disables TLS verification) |
| `-duration` | duration | 0 | Run duration (0 = forever) |
| `-ffmpeg` | string | "ffmpeg" | Path to FFmpeg binary |
| `-ffmpeg-debug` | bool | false | Enable FFmpeg -loglevel debug |
| `-header` | string | (repeat) | Add custom HTTP header (can repeat) |
| `-log-format` | string | "json" | Log format: "json" or "text" |
| `-metrics` | string | "0.0.0.0:17091" | Prometheus metrics address |
| `-nginx-metrics` | string | "" | Origin nginx_exporter URL |
| `-no-cache` | bool | false | Add no-cache headers (bypass CDN cache) |
| `-origin-metrics` | string | "" | Origin node_exporter URL |
| `-origin-metrics-host` | string | "" | Origin hostname/IP for metrics |
| `-origin-metrics-interval` | duration | 2s | Interval for scraping origin metrics |
| `-origin-metrics-nginx-port` | int | 9113 | Nginx exporter port |
| `-origin-metrics-node-port` | int | 9100 | Node exporter port |
| `-origin-metrics-window` | duration | 30s | Rolling window for percentiles |
| `--print-cmd` | bool | false | Print FFmpeg command and exit |
| `-probe-failure-policy` | string | "fallback" | Behavior if ffprobe fails |
| `-prom-client-metrics` | bool | false | Enable per-client Prometheus metrics |
| `-ramp-jitter` | duration | 200ms | Random jitter per client start |
| `-ramp-rate` | int | 5 | Clients to start per second |
| `-reconnect` | bool | true | Enable FFmpeg reconnect flags |
| `-reconnect-delay` | int | 5 | Max reconnect delay in seconds |
| `-resolve` | string | "" | Connect to this IP (requires --dangerous) |
| `-restart-on-stall` | bool | false | Kill and restart stalled clients |
| `-seg-retry` | int | 3 | Segment download retry count |
| `-segment-cache-window` | int | 300 | Recent segments to keep in cache |
| `-segment-sizes-interval` | duration | 1s | Interval for scraping segment sizes |
| `-segment-sizes-jitter` | duration | 500ms | Jitter for segment size scraping |
| `-segment-sizes-url` | string | "" | URL for segment size JSON |
| `--skip-preflight` | bool | false | Skip preflight checks |
| `-stats` | bool | true | Enable FFmpeg output parsing |
| `-stats-buffer` | int | 1000 | Lines to buffer per client |
| `-stats-loglevel` | string | "debug" | FFmpeg loglevel for stats |
| `-target-duration` | duration | 6s | Expected HLS segment duration |
| `-timeout` | duration | 15s | Network read/write timeout |
| `-tui` | bool | true | Enable live terminal dashboard |
| `-user-agent` | string | "go-ffmpeg-hls-swarm/1.0" | HTTP User-Agent header |
| `-v` | bool | false | Verbose logging |
| `-variant` | string | "all" | Bitrate selection mode |

---

## Flag Categories

### Orchestration
`-clients`, `-ramp-rate`, `-ramp-jitter`, `-duration`

### Variant Selection
`-variant`, `-probe-failure-policy`

### Network/Testing
`-resolve`, `-no-cache`, `-header`

### Safety (double-dash)
`--dangerous`, `--print-cmd`, `--check`, `--skip-preflight`

### Observability
`-metrics`, `-v`, `-log-format`

### FFmpeg
`-ffmpeg`, `-user-agent`, `-timeout`, `-reconnect`, `-reconnect-delay`, `-seg-retry`

### Health/Stall
`-target-duration`, `-restart-on-stall`

### Stats Collection
`-stats`, `-stats-loglevel`, `-stats-buffer`, `-ffmpeg-debug`

### Dashboard
`-tui`, `-prom-client-metrics`

### Origin Metrics
`-origin-metrics`, `-nginx-metrics`, `-origin-metrics-host`, `-origin-metrics-interval`, `-origin-metrics-window`, `-origin-metrics-node-port`, `-origin-metrics-nginx-port`

### Segment Size Tracking
`-segment-sizes-url`, `-segment-sizes-interval`, `-segment-sizes-jitter`, `-segment-cache-window`

---

## Common Combinations

### Quick test
```bash
go-ffmpeg-hls-swarm -clients 5 URL
```

### Stress test with TUI
```bash
go-ffmpeg-hls-swarm -clients 100 -duration 5m URL
```

### With origin metrics (TAP mode)
```bash
go-ffmpeg-hls-swarm -clients 100 -origin-metrics-host 10.177.0.10 URL
```

### Direct IP (bypass DNS)
```bash
go-ffmpeg-hls-swarm -resolve 1.2.3.4 --dangerous URL
```

### Validation only
```bash
go-ffmpeg-hls-swarm --check URL
```
