# Prometheus & Grafana Setup

> **Type**: Integration Guide

How to set up Prometheus and Grafana for monitoring go-ffmpeg-hls-swarm load tests.

---

## Quick Start

### 1. Run Load Test with Metrics

```bash
go-ffmpeg-hls-swarm -clients 100 -metrics 0.0.0.0:17091 \
  http://origin:17080/stream.m3u8
```

### 2. Verify Metrics Endpoint

```bash
curl http://localhost:17091/metrics | head -20
```

---

## Prometheus Configuration

### Basic Setup

```yaml
# prometheus.yml
global:
  scrape_interval: 5s

scrape_configs:
  # HLS Swarm metrics
  - job_name: 'hls-swarm'
    static_configs:
      - targets: ['localhost:17091']
    scrape_interval: 5s
```

### With Origin Metrics

```yaml
scrape_configs:
  # HLS Swarm client metrics
  - job_name: 'hls-swarm'
    static_configs:
      - targets: ['swarm-host:17091']

  # Origin node_exporter
  - job_name: 'origin-node'
    static_configs:
      - targets: ['origin:9100']

  # Origin nginx_exporter
  - job_name: 'origin-nginx'
    static_configs:
      - targets: ['origin:9113']
```

### Docker Compose

```yaml
version: '3.8'
services:
  prometheus:
    image: prom/prometheus:latest
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml
    ports:
      - "9090:9090"
    command:
      - '--config.file=/etc/prometheus/prometheus.yml'
      - '--storage.tsdb.retention.time=7d'
```

---

## Grafana Dashboard

### Data Source Setup

1. Open Grafana (http://localhost:3000)
2. Go to Configuration → Data Sources
3. Add Prometheus data source
4. URL: `http://prometheus:9090`
5. Click "Save & Test"

### Dashboard Panels

#### Row 1: Test Overview

**Active Clients**
```promql
hls_swarm_active_clients
```
- Type: Stat
- Color mode: Background

**Ramp Progress**
```promql
hls_swarm_ramp_progress * 100
```
- Type: Gauge
- Unit: percent

**Test Duration**
```promql
hls_swarm_test_elapsed_seconds
```
- Type: Stat
- Unit: seconds

#### Row 2: Throughput

**Segment Throughput (MB/s)**
```promql
hls_swarm_segment_throughput_30s_bytes_per_second / 1024 / 1024
```
- Type: Time series
- Unit: MB/s

**Request Rates**
```promql
hls_swarm_segment_requests_per_second
hls_swarm_manifest_requests_per_second
```
- Type: Time series
- Unit: req/s

**Total Data Downloaded**
```promql
hls_swarm_segment_bytes_downloaded_total / 1024 / 1024 / 1024
```
- Type: Stat
- Unit: GB

#### Row 3: Latency

**Latency Percentiles**
```promql
hls_swarm_inferred_latency_p50_seconds
hls_swarm_inferred_latency_p95_seconds
hls_swarm_inferred_latency_p99_seconds
```
- Type: Time series
- Unit: seconds

**Latency Heatmap**
```promql
sum(rate(hls_swarm_inferred_latency_seconds_bucket[1m])) by (le)
```
- Type: Heatmap

#### Row 4: Health

**Client Health**
```promql
hls_swarm_clients_above_realtime
hls_swarm_clients_below_realtime
hls_swarm_stalled_clients
```
- Type: Time series

**Average Speed**
```promql
hls_swarm_average_speed
```
- Type: Gauge
- Thresholds: <0.9 red, <1.0 yellow, >=1.0 green

#### Row 5: Errors

**Error Rate**
```promql
hls_swarm_error_rate * 100
```
- Type: Gauge
- Unit: percent
- Thresholds: >1% red

**HTTP Errors by Code**
```promql
sum by (status_code) (rate(hls_swarm_http_errors_total[1m]))
```
- Type: Time series

**Timeouts & Reconnections**
```promql
rate(hls_swarm_timeouts_total[1m])
rate(hls_swarm_reconnections_total[1m])
```
- Type: Time series

#### Row 6: Pipeline Health

**Stats Drop Rate**
```promql
hls_swarm_stats_drop_rate * 100
```
- Type: Gauge
- Thresholds: >1% yellow, >5% red

**Lines Parsed vs Dropped**
```promql
sum(rate(hls_swarm_stats_lines_parsed_total[1m]))
sum(rate(hls_swarm_stats_lines_dropped_total[1m]))
```
- Type: Time series

#### Row 7: Origin Metrics (if enabled)

**Origin CPU**
```promql
# From node_exporter
100 - (avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[1m])) * 100)
```
- Type: Gauge

**Origin Network Out**
```promql
rate(node_network_transmit_bytes_total{device="eth0"}[1m]) / 1024 / 1024
```
- Type: Time series
- Unit: MB/s

---

## Alert Rules

### Prometheus Alert Rules

```yaml
# alerts.yml
groups:
  - name: hls-swarm
    rules:
      - alert: HLSSwarmHighErrorRate
        expr: hls_swarm_error_rate > 0.01
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "HLS load test error rate > 1%"
          description: "Error rate is {{ $value | humanizePercentage }}"

      - alert: HLSSwarmClientsStalling
        expr: hls_swarm_stalled_clients > 0
        for: 30s
        labels:
          severity: warning
        annotations:
          summary: "{{ $value }} clients are stalling"

      - alert: HLSSwarmHighLatency
        expr: hls_swarm_inferred_latency_p95_seconds > 1
        for: 2m
        labels:
          severity: warning
        annotations:
          summary: "P95 latency > 1 second"

      - alert: HLSSwarmMetricsDegraded
        expr: hls_swarm_stats_drop_rate > 0.01
        for: 1m
        labels:
          severity: warning
        annotations:
          summary: "Metrics pipeline dropping > 1% of lines"
```

### Alertmanager Configuration

```yaml
# alertmanager.yml
route:
  receiver: 'slack'
  group_by: ['alertname']
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 1h

receivers:
  - name: 'slack'
    slack_configs:
      - api_url: 'https://hooks.slack.com/services/...'
        channel: '#alerts'
```

---

## Complete Docker Compose

```yaml
version: '3.8'
services:
  prometheus:
    image: prom/prometheus:latest
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml
      - ./alerts.yml:/etc/prometheus/alerts.yml
    command:
      - '--config.file=/etc/prometheus/prometheus.yml'
      - '--storage.tsdb.retention.time=7d'
    ports:
      - "9090:9090"

  grafana:
    image: grafana/grafana:latest
    environment:
      - GF_SECURITY_ADMIN_PASSWORD=admin
    volumes:
      - grafana-storage:/var/lib/grafana
    ports:
      - "3000:3000"

  alertmanager:
    image: prom/alertmanager:latest
    volumes:
      - ./alertmanager.yml:/etc/alertmanager/alertmanager.yml
    ports:
      - "9093:9093"

volumes:
  grafana-storage:
```

---

## Useful PromQL Queries

### Test Summary

```promql
# Total segments downloaded
hls_swarm_segment_requests_total

# Average throughput over test
avg_over_time(hls_swarm_segment_throughput_30s_bytes_per_second[1h]) / 1024 / 1024
```

### Performance Analysis

```promql
# Latency trend
quantile_over_time(0.95, hls_swarm_inferred_latency_p95_seconds[5m])

# Error rate trend
avg_over_time(hls_swarm_error_rate[5m])
```

### Capacity Estimation

```promql
# Throughput per client
hls_swarm_segment_throughput_30s_bytes_per_second / hls_swarm_active_clients

# Requests per client per second
hls_swarm_segment_requests_per_second / hls_swarm_active_clients
```

---

## Related Documents

- [METRICS_REFERENCE.md](../../reference/METRICS_REFERENCE.md) - All metrics
- [TUI_DASHBOARD.md](./TUI_DASHBOARD.md) - Built-in TUI
- [METRICS.md](./METRICS.md) - Metrics overview
