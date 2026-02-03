# Metrics Reference

> **Type**: Reference Documentation
> **Source**: Verified against `internal/metrics/collector.go`

Complete reference for all Prometheus metrics exposed by go-ffmpeg-hls-swarm.

---

## Overview

All metrics use the `hls_swarm_` prefix and are organized into panels for logical grouping.

**Metrics endpoint**: Default `http://0.0.0.0:17091/metrics`

**Metric tiers**:
- **Tier 1** (always enabled): Aggregate metrics safe for 1000+ clients
- **Tier 2** (optional, `--prom-client-metrics`): Per-client metrics for debugging (<200 clients)

---

## Panel 1: Test Overview

| Metric | Type | Description |
|--------|------|-------------|
| `hls_swarm_info` | GaugeVec | Information about the load test (value always 1). Labels: `version`, `stream_url`, `variant` |
| `hls_swarm_target_clients` | Gauge | Target number of clients to reach |
| `hls_swarm_test_duration_seconds` | Gauge | Configured test duration (0 = unlimited) |
| `hls_swarm_active_clients` | Gauge | Currently running clients |
| `hls_swarm_ramp_progress` | Gauge | Client ramp-up progress (0.0 to 1.0) |
| `hls_swarm_test_elapsed_seconds` | Gauge | Seconds since test started |
| `hls_swarm_test_remaining_seconds` | Gauge | Seconds remaining until test ends (-1 = unlimited) |

---

## Panel 2: Request Rates & Throughput

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

---

## Panel 2b: Segment Throughput (Accurate)

Based on actual segment sizes from origin server (via `/files/json/` endpoint):

| Metric | Type | Description |
|--------|------|-------------|
| `hls_swarm_segment_bytes_downloaded_total` | Counter | Total bytes downloaded from segments (accurate sizes) |
| `hls_swarm_segment_throughput_1s_bytes_per_second` | Gauge | Segment throughput averaged over last 1 second |
| `hls_swarm_segment_throughput_30s_bytes_per_second` | Gauge | Segment throughput averaged over last 30 seconds |
| `hls_swarm_segment_throughput_60s_bytes_per_second` | Gauge | Segment throughput averaged over last 60 seconds |
| `hls_swarm_segment_throughput_300s_bytes_per_second` | Gauge | Segment throughput averaged over last 5 minutes |

---

## Panel 3: Latency Distribution

All latency metrics are *inferred* from FFmpeg event timing (not actual HTTP timing):

| Metric | Type | Description |
|--------|------|-------------|
| `hls_swarm_inferred_latency_seconds` | Histogram | Inferred segment download latency distribution |
| `hls_swarm_inferred_latency_p50_seconds` | Gauge | 50th percentile (median) |
| `hls_swarm_inferred_latency_p95_seconds` | Gauge | 95th percentile |
| `hls_swarm_inferred_latency_p99_seconds` | Gauge | 99th percentile |
| `hls_swarm_inferred_latency_max_seconds` | Gauge | Maximum latency observed |

**Histogram buckets**: 0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1.0, 2.5, 5.0, 10.0 seconds

---

## Panel 4: Client Health & Playback

| Metric | Type | Description |
|--------|------|-------------|
| `hls_swarm_clients_above_realtime` | Gauge | Clients with speed >= 1.0x (healthy) |
| `hls_swarm_clients_below_realtime` | Gauge | Clients with speed < 1.0x (buffering) |
| `hls_swarm_stalled_clients` | Gauge | Clients with speed < 0.9x for >5 seconds |
| `hls_swarm_average_speed` | Gauge | Average playback speed (1.0 = realtime) |
| `hls_swarm_high_drift_clients` | Gauge | Clients with drift > 5 seconds |
| `hls_swarm_average_drift_seconds` | Gauge | Average wall-clock drift |
| `hls_swarm_max_drift_seconds` | Gauge | Maximum wall-clock drift |

---

## Panel 5: Errors & Recovery

| Metric | Type | Description |
|--------|------|-------------|
| `hls_swarm_http_errors_total` | CounterVec | HTTP errors by status code. Label: `status_code` (e.g., "404", "503", "other") |
| `hls_swarm_timeouts_total` | Counter | Total connection/read timeouts |
| `hls_swarm_reconnections_total` | Counter | Total FFmpeg reconnection attempts |
| `hls_swarm_client_starts_total` | Counter | Total client process starts |
| `hls_swarm_client_restarts_total` | Counter | Total client restarts (after failure) |
| `hls_swarm_client_exits_total` | CounterVec | Client exits by category. Label: `category` ("success", "error", "signal") |
| `hls_swarm_error_rate` | Gauge | Current error rate (errors/total requests) |

---

## Panel 6: Pipeline Health

Monitors the metrics collection system itself:

| Metric | Type | Description |
|--------|------|-------------|
| `hls_swarm_stats_lines_dropped_total` | CounterVec | FFmpeg output lines dropped (parser backpressure). Label: `stream` ("progress", "stderr") |
| `hls_swarm_stats_lines_parsed_total` | CounterVec | FFmpeg output lines successfully parsed. Label: `stream` |
| `hls_swarm_stats_clients_degraded` | Gauge | Clients with >1% dropped lines |
| `hls_swarm_stats_drop_rate` | Gauge | Overall metrics line drop rate (0.0-1.0) |
| `hls_swarm_stats_peak_drop_rate` | Gauge | Peak metrics line drop rate observed |

---

## Panel 7: Uptime Distribution

| Metric | Type | Description |
|--------|------|-------------|
| `hls_swarm_client_uptime_seconds` | Histogram | Client uptime before exit |
| `hls_swarm_uptime_p50_seconds` | Gauge | 50th percentile uptime |
| `hls_swarm_uptime_p95_seconds` | Gauge | 95th percentile uptime |
| `hls_swarm_uptime_p99_seconds` | Gauge | 99th percentile uptime |

**Uptime histogram buckets**: 1, 5, 30, 60, 300, 600, 1800, 3600, 7200 seconds

---

## Tier 2: Per-Client Metrics

**Warning**: High cardinality. Only use with <200 clients. Enable with `--prom-client-metrics`.

| Metric | Type | Description |
|--------|------|-------------|
| `hls_swarm_client_speed` | GaugeVec | Per-client playback speed. Label: `client_id` |
| `hls_swarm_client_drift_seconds` | GaugeVec | Per-client wall-clock drift. Label: `client_id` |
| `hls_swarm_client_bytes_total` | GaugeVec | Per-client bytes downloaded. Label: `client_id` |

---

## Grafana Dashboard Queries

### Active clients over time

```promql
hls_swarm_active_clients
```

### Request rate

```promql
rate(hls_swarm_segment_requests_total[1m])
```

### Throughput (MB/s)

```promql
hls_swarm_segment_throughput_30s_bytes_per_second / 1024 / 1024
```

### Latency P95

```promql
hls_swarm_inferred_latency_p95_seconds
```

### Error rate percentage

```promql
hls_swarm_error_rate * 100
```

### Clients buffering percentage

```promql
hls_swarm_clients_below_realtime / hls_swarm_active_clients * 100
```

### Pipeline health (drop rate)

```promql
hls_swarm_stats_drop_rate * 100
```

---

## Alert Examples

### High error rate

```yaml
- alert: HLSSwarmHighErrorRate
  expr: hls_swarm_error_rate > 0.01
  for: 1m
  labels:
    severity: warning
  annotations:
    summary: "HLS load test error rate > 1%"
```

### Clients stalling

```yaml
- alert: HLSSwarmClientsStalling
  expr: hls_swarm_stalled_clients > 0
  for: 30s
  labels:
    severity: warning
  annotations:
    summary: "{{ $value }} HLS clients are stalling"
```

### Metrics pipeline degraded

```yaml
- alert: HLSSwarmMetricsDegraded
  expr: hls_swarm_stats_drop_rate > 0.01
  for: 1m
  labels:
    severity: warning
  annotations:
    summary: "HLS metrics pipeline dropping > 1% of lines"
```
