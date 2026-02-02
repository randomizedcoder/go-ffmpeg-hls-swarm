# Prometheus Metrics Guide

> **Type**: User Documentation
> **Source**: Verified against `internal/metrics/collector.go`

Complete reference for all Prometheus metrics exposed by go-ffmpeg-hls-swarm.

---

## Metrics Endpoint

Default: `http://0.0.0.0:17091/metrics`

Change with `-metrics` flag:

```bash
go-ffmpeg-hls-swarm -metrics 0.0.0.0:9090 ...
```

---

## Metric Naming

All metrics use the `hls_swarm_` prefix.

> **Note**: Previous documentation may reference `hlsswarm_*` prefix. The correct prefix is `hls_swarm_*`.

---

## Tier 1 Metrics (Always Enabled)

These aggregate metrics are safe for 1000+ clients with low cardinality.

### Test Overview

| Metric | Type | Description |
|--------|------|-------------|
| `hls_swarm_info` | GaugeVec | Test metadata (labels: version, stream_url, variant) |
| `hls_swarm_target_clients` | Gauge | Configured target client count |
| `hls_swarm_test_duration_seconds` | Gauge | Configured test duration (0 = unlimited) |
| `hls_swarm_active_clients` | Gauge | Currently running clients |
| `hls_swarm_ramp_progress` | Gauge | Ramp-up progress (0.0 to 1.0) |
| `hls_swarm_test_elapsed_seconds` | Gauge | Seconds since test started |
| `hls_swarm_test_remaining_seconds` | Gauge | Seconds until test ends (-1 = unlimited) |

### Request Rates & Throughput

| Metric | Type | Description |
|--------|------|-------------|
| `hls_swarm_manifest_requests_total` | Counter | Total manifest (.m3u8) requests |
| `hls_swarm_segment_requests_total` | Counter | Total segment (.ts) requests |
| `hls_swarm_init_requests_total` | Counter | Total init segment requests |
| `hls_swarm_unknown_requests_total` | Counter | Total unclassified URL requests |
| `hls_swarm_bytes_downloaded_total` | Counter | Total bytes downloaded |
| `hls_swarm_manifest_requests_per_second` | Gauge | Current manifest request rate |
| `hls_swarm_segment_requests_per_second` | Gauge | Current segment request rate |
| `hls_swarm_throughput_bytes_per_second` | Gauge | Current download throughput |

### Latency Distribution

| Metric | Type | Description |
|--------|------|-------------|
| `hls_swarm_inferred_latency_seconds` | Histogram | Inferred segment download latency |
| `hls_swarm_inferred_latency_p50_seconds` | Gauge | Latency 50th percentile (median) |
| `hls_swarm_inferred_latency_p95_seconds` | Gauge | Latency 95th percentile |
| `hls_swarm_inferred_latency_p99_seconds` | Gauge | Latency 99th percentile |
| `hls_swarm_inferred_latency_max_seconds` | Gauge | Maximum observed latency |

**Histogram buckets**: 5ms, 10ms, 25ms, 50ms, 75ms, 100ms, 250ms, 500ms, 750ms, 1s, 2.5s, 5s, 10s

### Client Health & Playback

| Metric | Type | Description |
|--------|------|-------------|
| `hls_swarm_clients_above_realtime` | Gauge | Clients with speed >= 1.0x (healthy) |
| `hls_swarm_clients_below_realtime` | Gauge | Clients with speed < 1.0x (buffering) |
| `hls_swarm_stalled_clients` | Gauge | Clients with speed < 0.9x for >5 seconds |
| `hls_swarm_average_speed` | Gauge | Average playback speed (1.0 = realtime) |
| `hls_swarm_high_drift_clients` | Gauge | Clients with drift > 5 seconds |
| `hls_swarm_average_drift_seconds` | Gauge | Average wall-clock drift |
| `hls_swarm_max_drift_seconds` | Gauge | Maximum wall-clock drift |

### Errors & Recovery

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hls_swarm_http_errors_total` | CounterVec | status_code | HTTP errors by status code |
| `hls_swarm_timeouts_total` | Counter | - | Total connection/read timeouts |
| `hls_swarm_reconnections_total` | Counter | - | Total FFmpeg reconnection attempts |
| `hls_swarm_client_starts_total` | Counter | - | Total client process starts |
| `hls_swarm_client_restarts_total` | Counter | - | Total client restarts (after failure) |
| `hls_swarm_client_exits_total` | CounterVec | category | Exits by category: success, error, signal |
| `hls_swarm_error_rate` | Gauge | - | Current error rate (errors/total requests) |

### Pipeline Health (Metrics System)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hls_swarm_stats_lines_dropped_total` | CounterVec | stream | FFmpeg output lines dropped (parser backpressure) |
| `hls_swarm_stats_lines_parsed_total` | CounterVec | stream | FFmpeg output lines successfully parsed |
| `hls_swarm_stats_clients_degraded` | Gauge | - | Clients with >1% dropped lines |
| `hls_swarm_stats_drop_rate` | Gauge | - | Overall metrics line drop rate (0.0-1.0) |
| `hls_swarm_stats_peak_drop_rate` | Gauge | - | Peak metrics line drop rate observed |

Stream labels: `progress`, `stderr`

### Uptime Distribution

| Metric | Type | Description |
|--------|------|-------------|
| `hls_swarm_client_uptime_seconds` | Histogram | Client uptime before exit |
| `hls_swarm_uptime_p50_seconds` | Gauge | Uptime 50th percentile |
| `hls_swarm_uptime_p95_seconds` | Gauge | Uptime 95th percentile |
| `hls_swarm_uptime_p99_seconds` | Gauge | Uptime 99th percentile |

**Histogram buckets**: 1s, 5s, 30s, 60s, 300s (5m), 600s (10m), 1800s (30m), 3600s (1h), 7200s (2h)

---

## Tier 2 Metrics (Optional)

Enable with `-prom-client-metrics`. **Warning**: High cardinality - use only with <200 clients.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `hls_swarm_client_speed` | GaugeVec | client_id | Per-client playback speed |
| `hls_swarm_client_drift_seconds` | GaugeVec | client_id | Per-client wall-clock drift |
| `hls_swarm_client_bytes_total` | GaugeVec | client_id | Per-client bytes downloaded |

---

## Example PromQL Queries

### Current State

```promql
# Active clients
hls_swarm_active_clients

# Ramp-up progress (0-100%)
hls_swarm_ramp_progress * 100

# Time remaining
hls_swarm_test_remaining_seconds
```

### Request Rates

```promql
# Manifest requests per second
hls_swarm_manifest_requests_per_second

# Segment requests per second
hls_swarm_segment_requests_per_second

# Total throughput (bytes/sec)
hls_swarm_throughput_bytes_per_second

# Throughput in Mbps
hls_swarm_throughput_bytes_per_second * 8 / 1000000
```

### Error Analysis

```promql
# Current error rate
hls_swarm_error_rate

# Restart rate (per minute)
rate(hls_swarm_client_restarts_total[1m]) * 60

# HTTP errors by status code
sum by (status_code) (rate(hls_swarm_http_errors_total[5m]))

# Error exits as percentage
sum(rate(hls_swarm_client_exits_total{category="error"}[5m])) /
sum(rate(hls_swarm_client_exits_total[5m])) * 100
```

### Latency Analysis

```promql
# P99 latency
hls_swarm_inferred_latency_p99_seconds

# Latency heatmap (using histogram)
histogram_quantile(0.99, rate(hls_swarm_inferred_latency_seconds_bucket[5m]))

# Max latency over time
max_over_time(hls_swarm_inferred_latency_max_seconds[5m])
```

### Client Health

```promql
# Healthy clients percentage
hls_swarm_clients_above_realtime / hls_swarm_active_clients * 100

# Stalled clients
hls_swarm_stalled_clients

# Average speed (should be ~1.0)
hls_swarm_average_speed
```

### Uptime Analysis

```promql
# Median client uptime
hls_swarm_uptime_p50_seconds

# P95 uptime (critical for detecting connection drops)
hls_swarm_uptime_p95_seconds

# Uptime distribution (using histogram)
histogram_quantile(0.95, rate(hls_swarm_client_uptime_seconds_bucket[5m]))
```

---

## Interpreting Metrics

### Uptime Metrics

| P95 Uptime | Interpretation |
|------------|----------------|
| > 5 minutes | Healthy — infrastructure sustaining connections |
| 30s - 5min | Possible issues — check for rate limiting |
| < 30 seconds | Problem — connections being dropped quickly |

### Error Rate

| Error Rate | Interpretation |
|------------|----------------|
| < 1% | Normal operation |
| 1-5% | Minor issues, monitor |
| > 5% | Significant problems, investigate |

### Speed Metrics

| Average Speed | Interpretation |
|---------------|----------------|
| >= 1.0 | Healthy — downloading at or above realtime |
| 0.9 - 1.0 | Marginal — some buffering possible |
| < 0.9 | Degraded — significant buffering expected |

---

## Grafana Dashboard

Key panels for monitoring:

1. **Active Clients vs Target** - Line graph
2. **Request Rates** - Stacked area (manifest + segment)
3. **Throughput** - Line graph in Mbps
4. **Error Rate** - Gauge with thresholds
5. **Latency P50/P95/P99** - Line graph
6. **Client Health** - Pie chart (healthy vs buffering vs stalled)
7. **Uptime Distribution** - Heatmap
8. **HTTP Errors by Code** - Bar chart

---

## Scrape Configuration

Prometheus scrape config:

```yaml
scrape_configs:
  - job_name: 'hls-swarm'
    static_configs:
      - targets: ['localhost:17091']
    scrape_interval: 5s
    scrape_timeout: 5s
```

For high-frequency monitoring during tests:

```yaml
scrape_configs:
  - job_name: 'hls-swarm-fast'
    static_configs:
      - targets: ['localhost:17091']
    scrape_interval: 1s
    scrape_timeout: 1s
```
