# TUI Dashboard Guide

> **Type**: User Documentation

The Terminal User Interface (TUI) provides a live dashboard for monitoring load tests.

---

## Enabling the TUI

```bash
go-ffmpeg-hls-swarm -tui -clients 50 http://localhost:17080/stream.m3u8
```

---

## Dashboard Sections

The TUI displays several sections of information:

### Test Overview

- Target clients
- Active clients
- Ramp progress
- Test duration / elapsed time

### Request Metrics

- Manifest requests/sec
- Segment requests/sec
- Total throughput (MB/s)

### Client Health

- Clients above realtime (healthy)
- Clients below realtime (buffering)
- Stalled clients
- Average speed

### Latency

- P50, P95, P99 latencies
- Max latency

### Errors

- Error rate
- HTTP error breakdown
- Restart count

### Origin Metrics (if enabled)

When using `-origin-metrics-host`:
- Origin CPU usage
- Origin memory usage
- Network in/out rates
- Nginx connections
- Nginx request rate

---

## Enabling Origin Metrics in TUI

```bash
go-ffmpeg-hls-swarm -tui -clients 100 \
  -origin-metrics-host 10.177.0.10 \
  http://10.177.0.10:17080/stream.m3u8
```

This scrapes metrics from:
- node_exporter (default port 9100)
- nginx_exporter (default port 9113)

---

## Keyboard Controls

| Key | Action |
|-----|--------|
| `q` | Quit (graceful shutdown) |
| `Ctrl+C` | Quit (graceful shutdown) |

---

## Requirements

The TUI requires:
- A terminal that supports ANSI escape codes
- Sufficient terminal width (80+ characters recommended)
- TTY (won't work in non-interactive environments)

---

## Non-Interactive Mode

If TUI is enabled but no TTY is available, the tool falls back to standard logging.

For CI/CD or scripted usage, omit the `-tui` flag:

```bash
# No TUI, standard output
go-ffmpeg-hls-swarm -clients 100 -duration 30s \
  http://localhost:17080/stream.m3u8
```

---

## TUI with Prometheus

The TUI and Prometheus metrics work independently. You can use both:

```bash
go-ffmpeg-hls-swarm -tui -metrics 0.0.0.0:17091 -clients 100 \
  http://localhost:17080/stream.m3u8
```

Then scrape metrics in another terminal:

```bash
curl -s http://localhost:17091/metrics | grep hls_swarm
```

---

## Troubleshooting

### TUI not appearing

- Ensure `-tui` flag is set
- Check you're running in a terminal (TTY)
- Try increasing terminal size

### Garbled display

- Terminal may not support ANSI codes
- Try a different terminal emulator
- Ensure terminal width is sufficient

### Origin metrics not showing

- Verify origin server has exporters running
- Check exporter URLs are accessible:
  ```bash
  curl http://10.177.0.10:9100/metrics
  curl http://10.177.0.10:9113/metrics
  ```

---

## Alternative: Prometheus + Grafana

For longer tests or remote monitoring, use Prometheus and Grafana instead of TUI:

1. Configure Prometheus to scrape `http://localhost:17091/metrics`
2. Import a Grafana dashboard
3. Run without `-tui` for lower overhead

See [METRICS.md](METRICS.md) for Prometheus configuration.
