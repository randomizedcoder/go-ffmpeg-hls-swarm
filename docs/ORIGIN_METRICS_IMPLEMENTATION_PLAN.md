# Origin Metrics Implementation Plan

## Overview

This document outlines the plan to implement origin server metrics collection and display in the TUI dashboard, as specified in `TUI_DEFECTS.md` Defect F. The implementation will:

1. Scrape metrics from origin server exporters (node_exporter, nginx_exporter) in background goroutines
2. Display origin metrics in the TUI dashboard
3. Expose origin metrics via Prometheus for external monitoring (Grafana)

## Current State

- TUI dashboard shows client-side metrics (HLS/HTTP/TCP layers)
- Prometheus metrics server exposes client-side metrics via `internal/metrics/server.go`
- Metrics are updated in `statsUpdateLoop()` goroutine (every 1 second)
- No origin server metrics are currently collected or displayed
- Screen real estate is available for origin metrics panel (as shown in screenshot)
- `TUI_DEFECTS.md` Defect F provides detailed requirements and proposed implementation

## Requirements

### Metrics to Collect

Based on `TUI_DEFECTS.md` Defect F, we need to collect:

#### Node Exporter Metrics
- CPU usage (`node_cpu_seconds_total`)
- Memory usage (`node_memory_MemAvailable_bytes`, `node_memory_MemTotal_bytes`)
- Network I/O (`node_network_transmit_bytes_total`, `node_network_receive_bytes_total`)
- Disk I/O (`node_disk_read_bytes_total`, `node_disk_written_bytes_total`)

#### Nginx Exporter Metrics
- Active connections (`nginx_connections_active`)
- Total requests (`nginx_http_requests_total`)
- Request duration (`nginx_http_request_duration_seconds`)
- Upstream metrics (if applicable)

### CLI Flags

```go
// In internal/config/config.go - add to Config struct:
OriginMetricsURL      string        `json:"origin_metrics_url"`      // node_exporter URL
NginxMetricsURL      string        `json:"nginx_metrics_url"`        // nginx_exporter URL
OriginMetricsInterval time.Duration `json:"origin_metrics_interval"`  // Scrape interval

// In internal/config/flags.go:
flag.StringVar(&cfg.OriginMetricsURL, "origin-metrics", "",
    "Origin node_exporter URL (e.g., http://10.177.0.10:9100/metrics)")
flag.StringVar(&cfg.NginxMetricsURL, "nginx-metrics", "",
    "Origin nginx_exporter URL (e.g., http://10.177.0.10:9113/metrics)")
flag.DurationVar(&cfg.OriginMetricsInterval, "origin-metrics-interval", 2*time.Second,
    "Interval for scraping origin metrics")
```

## Architecture

### Components

1. **Origin Metrics Scraper** (`internal/metrics/origin_scraper.go`)
   - Scrapes Prometheus metrics from node_exporter and nginx_exporter
   - Runs in background goroutine
   - Parses Prometheus text format
   - Caches latest values with timestamps

2. **Origin Metrics Collector** (`internal/metrics/origin_collector.go`)
   - Aggregates scraped metrics
   - Calculates rates (CPU, network, etc.)
   - Provides thread-safe access to metrics

3. **Prometheus Exporter** (extend `internal/metrics/collector.go`)
   - Exposes origin metrics as Prometheus gauges/histograms
   - Labels: `origin="<hostname>"`

4. **TUI Panel** (extend `internal/tui/view.go`)
   - New `renderOriginMetrics()` function
   - Displays CPU, Memory, Network, Nginx stats
   - Updates every refresh cycle

### Data Flow

```
Origin Server (node_exporter, nginx_exporter)
    ↓ (HTTP GET /metrics)
Origin Metrics Scraper (goroutine, every 2s)
    ↓ (parsed metrics)
Origin Metrics Collector (thread-safe cache)
    ↓
    ├─→ TUI Dashboard (renderOriginMetrics)
    └─→ Prometheus Exporter (expose as gauges)
```

## Implementation Steps

### Phase 1: Metrics Scraper

1. Create `internal/metrics/origin_scraper.go`:
   ```go
   type OriginScraper struct {
       nodeExporterURL string
       nginxExporterURL string
       interval time.Duration
       metrics *OriginMetrics
       mu sync.RWMutex
       logger *slog.Logger
       lastScrape time.Time
       lastError error
   }

   type OriginMetrics struct {
       // Node exporter metrics
       CPUPercent    float64
       MemUsed       int64
       MemTotal      int64
       MemPercent    float64
       NetInRate     float64  // bytes/sec
       NetOutRate    float64  // bytes/sec

       // Nginx exporter metrics
       NginxConnections int64
       NginxReqRate     float64  // requests/sec
       NginxReqDuration float64  // avg duration in seconds

       // Metadata
       LastUpdate time.Time
       Healthy    bool
   }
   ```
   - HTTP client with 5s timeout
   - Parse Prometheus text format (use `github.com/prometheus/common/expfmt`)
   - Extract specific metrics using regex or Prometheus parser
   - Cache with timestamp
   - Calculate rates (network, requests) from deltas

2. Add to `internal/config/config.go`:
   - `OriginMetricsURL string` (default: "")
   - `NginxMetricsURL string` (default: "")
   - `OriginMetricsInterval time.Duration` (default: 2*time.Second)

3. Add CLI flags in `internal/config/flags.go`

### Phase 2: Metrics Collector Integration

1. Integrate scraper with `Orchestrator`:
   - Add `originScraper *OriginScraper` field to `Orchestrator`
   - Initialize in `New()` if URLs are configured
   - Start scraper goroutine in `Run()`:
     ```go
     if o.originScraper != nil {
         go func() {
             o.originScraper.Run(ctx)
         }()
     }
     ```

2. Pass metrics to TUI:
   - Add `GetOriginMetrics() *OriginMetrics` method to `Orchestrator`
   - Update `StatsSource` interface in `internal/tui/model.go` to include origin metrics
   - TUI model fetches origin metrics in update loop

### Phase 3: Prometheus Export

1. Extend `internal/metrics/collector.go`:
   - Add origin metrics gauges (similar to existing `hls_swarm_*` metrics):
     ```go
     var (
         originCPUUsageRatio = prometheus.NewGauge(
             prometheus.GaugeOpts{
                 Name: "hls_swarm_origin_cpu_usage_ratio",
                 Help: "Origin server CPU usage ratio (0.0-1.0)",
             },
         )
         originMemoryUsageRatio = prometheus.NewGauge(...)
         originNetworkReceiveBytes = prometheus.NewGaugeVec(...)
         originNetworkTransmitBytes = prometheus.NewGaugeVec(...)
         originNginxConnectionsActive = prometheus.NewGauge(...)
         originNginxRequestsTotal = prometheus.NewCounterVec(...)
         originNginxRequestDuration = prometheus.NewHistogramVec(...)
     )
     ```

2. Update `statsUpdateLoop()` in `internal/orchestrator/orchestrator.go`:
   - After updating client metrics, also update origin metrics:
     ```go
     if o.originScraper != nil {
         originMetrics := o.originScraper.GetMetrics()
         o.metrics.RecordOriginMetrics(originMetrics)
     }
     ```

3. Add `RecordOriginMetrics()` method to `Collector`:
   - Update all Prometheus gauges/counters with scraped values
   - Use labels: `origin="<hostname>"` (extracted from URL)

### Phase 4: TUI Integration

1. Update `StatsSource` interface in `internal/tui/model.go`:
   ```go
   type StatsSource interface {
       GetAggregatedStats() *stats.AggregatedStats
       GetDebugStatsAggregate() *stats.DebugStatsAggregate
       GetOriginMetrics() *metrics.OriginMetrics  // NEW
   }
   ```

2. Update `Orchestrator` to implement `GetOriginMetrics()`:
   ```go
   func (o *Orchestrator) GetOriginMetrics() *metrics.OriginMetrics {
       if o.originScraper == nil {
           return nil
       }
       return o.originScraper.GetMetrics()
   }
   ```

3. Update `Model` in `internal/tui/model.go`:
   - Add `originMetrics *metrics.OriginMetrics` field
   - Update in `updateStats()` method:
     ```go
     if source, ok := m.statsSource.(interface{ GetOriginMetrics() *metrics.OriginMetrics }); ok {
         m.originMetrics = source.GetOriginMetrics()
     }
     ```

4. Create `renderOriginMetrics()` in `internal/tui/view.go`:
   - Match design from `TUI_DEFECTS.md` Defect F
   - Two-column layout: System | Nginx
   - Display CPU usage with progress bar
   - Display Memory usage (used/total, percentage)
   - Display Network I/O (receive/transmit, bytes/s)
   - Display Nginx stats (connections, requests/sec, duration)

5. Add to `renderSummaryView()`:
   - Insert origin metrics panel after debug metrics panel
   - Use same box border style as debug metrics

### Phase 5: Error Handling

1. Handle scraper errors gracefully:
   - Log errors but don't crash
   - Show "N/A" or "Error" in TUI if scrape fails
   - Track last successful scrape time

2. Handle missing exporters:
   - If URL not provided, skip that exporter
   - Show "Not configured" in TUI

## TUI Layout

Add new panel after the debug metrics panel:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ 🖥️  ORIGIN SERVER METRICS                                                   │
├─────────────────────────────────────────────────────────────────────────────┤
│ System                              │ Nginx                                │
│   CPU:    45.2%                     │   Connections:   1,234  (active)    │
│   Memory: 2.1GB / 8.0GB  (26.2%)    │   Requests:      12.5K  (+125/s)    │
│   Network RX: 1.2GB/s               │   Avg Duration:  2.3ms               │
│   Network TX: 850MB/s                │   Status:        ● Healthy           │
│   Status:  ● Healthy                 │                                     │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Prometheus Metrics

All origin metrics will be exposed with labels:
- `origin="<hostname>"` (extracted from URL)
- `exporter="node"` or `exporter="nginx"`

Example queries for Grafana:
```promql
# CPU usage
hls_swarm_origin_cpu_usage_ratio{origin="10.177.0.10"}

# Memory usage
hls_swarm_origin_memory_usage_ratio{origin="10.177.0.10"}

# Network throughput
rate(hls_swarm_origin_network_receive_bytes_total{origin="10.177.0.10"}[5m])

# Nginx requests per second
rate(hls_swarm_origin_nginx_requests_total{origin="10.177.0.10"}[5m])
```

## Testing

1. Unit tests for scraper:
   - Test Prometheus text format parsing
   - Test error handling (timeout, invalid URL)
   - Test rate calculations

2. Integration tests:
   - Mock HTTP server with Prometheus metrics
   - Verify metrics are scraped and cached
   - Verify Prometheus export

3. Manual testing:
   - Run with real node_exporter and nginx_exporter
   - Verify TUI display updates
   - Verify Prometheus metrics endpoint

## Future Enhancements

1. Support multiple origin servers (load balancer scenario)
2. Alerting thresholds (CPU > 80%, memory > 90%)
3. Historical trending (last 1m, 5m, 15m)
4. Disk I/O metrics
5. Custom metric labels (environment, region)
