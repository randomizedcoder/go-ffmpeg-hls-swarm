# Origin Server Metrics Implementation Plan

> **Type**: Implementation Plan
> **Status**: PROPOSAL
> **Related**: [TUI_DEFECTS.md §Defect F](TUI_DEFECTS.md#defect-f-unused-screen-real-estate---missing-origin-metrics), [PORTS.md](PORTS.md), [ATOMIC_MIGRATION_PLAN.md](ATOMIC_MIGRATION_PLAN.md)

This document provides a detailed plan for implementing origin server metrics scraping and display in the TUI dashboard. The implementation will use Prometheus exporters (node_exporter and nginx_exporter) to collect system and application metrics from the origin server, with a focus on high-performance, lock-free operations using atomic operations.

---

## Table of Contents

1. [Overview](#overview)
2. [Requirements](#requirements)
3. [Architecture](#architecture)
4. [Implementation Details](#implementation-details)
5. [Feature Flag Design](#feature-flag-design)
6. [CLI Configuration](#cli-configuration)
7. [High-Performance Parsing](#high-performance-parsing)
8. [Atomic Operations Design](#atomic-operations-design)
9. [Testing Strategy](#testing-strategy)
10. [Benchmarking](#benchmarking)
11. [TUI Integration](#tui-integration)
12. [Implementation Phases](#implementation-phases)
13. [Performance Considerations](#performance-considerations)
14. [Error Handling](#error-handling)
15. [Future Enhancements](#future-enhancements)

---

## Overview

### Problem Statement

The TUI dashboard currently has unused screen real estate to the right of "Request Statistics" and "Inferred Segment Latency" sections. This space should be utilized to display origin server metrics, providing operators with real-time visibility into the origin server's resource utilization and performance during load tests.

### Goals

1. **Feature Flag Control**: Origin metrics scraping must be opt-in via feature flag, as not all origins will have Prometheus exporters available
2. **High Performance**: Use atomic operations instead of mutexes for lock-free metric access
3. **Configurable Ports**: Default to project-standard ports (from PORTS.md) but allow CLI overrides
4. **Go Idiomatic**: Follow Go best practices and project conventions
5. **Comprehensive Testing**: Include unit tests with mocked Prometheus endpoints
6. **Benchmarking**: Ensure parsing performance meets requirements for high-frequency scraping

### Success Criteria

- ✅ Origin metrics displayed in TUI when feature flag is enabled
- ✅ Zero lock contention on metric reads (atomic operations)
- ✅ Configurable via CLI flags with sensible defaults
- ✅ Graceful degradation when exporters are unavailable
- ✅ Test coverage >80% with mocked endpoints
- ✅ Parsing benchmarks show <1ms per scrape

---

## Requirements

### Functional Requirements

1. **Prometheus Scraping**
   - Scrape node_exporter metrics (CPU, memory, network, disk)
   - Scrape nginx_exporter metrics (connections, request rate, latency)
   - Support configurable scrape interval (default: 2 seconds)
   - Handle missing/unavailable exporters gracefully

2. **Feature Flag**
   - Metrics scraping disabled by default
   - Enable via `-origin-metrics` flag (URL required)
   - Optional `-nginx-metrics` flag for nginx exporter
   - When disabled, TUI shows "Origin metrics not configured"

3. **Port Configuration**
   - Default node_exporter port: `9100` (standard Prometheus port)
   - Default nginx_exporter port: `9113` (standard nginx-exporter port)
   - CLI flags to override ports: `-origin-metrics-port`, `-nginx-metrics-port`
   - Support for TAP networking (direct IP access) and user-mode networking (localhost forwarding)

4. **TUI Display**
   - Display CPU usage with progress bar
   - Display memory usage (used/total with percentage)
   - Display network rates (in/out in bytes/sec)
   - Display Nginx connections and request rate
   - Display Nginx request latency (P99)
   - Update every TUI tick (~500ms) from cached metrics

### Non-Functional Requirements

1. **Performance**
   - Metric reads must be lock-free (atomic operations)
   - Parsing must complete in <1ms per endpoint
   - Scraping goroutine must not block TUI updates
   - Memory footprint <10MB for metrics storage

2. **Reliability**
   - Handle network timeouts gracefully (5s timeout)
   - Continue operating if one exporter fails
   - Cache last known good values during outages
   - Log errors at debug level (not spam logs)

3. **Maintainability**
   - Go idiomatic code structure
   - Comprehensive test coverage
   - Clear separation of concerns (scraping, parsing, storage, display)

---

## Architecture

### Component Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                    go-ffmpeg-hls-swarm                           │
│                                                                  │
│  ┌──────────────────┐      ┌──────────────────────────────┐   │
│  │   Config/Flags   │─────▶│  OriginMetricsScraper        │   │
│  │                  │      │  (atomic-based storage)       │   │
│  │ -origin-metrics  │      │                                │   │
│  │ -nginx-metrics   │      │  ┌──────────────────────────┐ │   │
│  │ -origin-metrics- │      │  │ PrometheusParser        │ │   │
│  │   interval       │      │  │ (high-performance)       │ │   │
│  └──────────────────┘      │  └──────────────────────────┘ │   │
│                             │                                │   │
│                             │  ┌──────────────────────────┐ │   │
│                             │  │ AtomicMetricsStore       │ │   │
│                             │  │ (lock-free reads)        │ │   │
│                             │  └──────────────────────────┘ │   │
│                             └──────────────────────────────┘   │
│                                      │                          │
│                                      ▼                          │
│                             ┌──────────────────────────────┐   │
│                             │      TUI Model                │   │
│                             │  (reads via GetMetrics())    │   │
│                             └──────────────────────────────┘   │
│                                      │                          │
│                                      ▼                          │
│                             ┌──────────────────────────────┐   │
│                             │      TUI View                 │   │
│                             │  (renderOriginMetrics())      │   │
│                             └──────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Origin Server (MicroVM)                       │
│                                                                  │
│  ┌──────────────────┐      ┌──────────────────────────────┐   │
│  │ node_exporter    │      │  nginx_exporter              │   │
│  │ :9100/metrics    │      │  :9113/metrics               │   │
│  └──────────────────┘      └──────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

### Data Flow

1. **Initialization**: Config parsed → Scraper created with URLs → Scraper started in goroutine
2. **Scraping Loop**: Every `interval` seconds → HTTP GET to endpoints → Parse Prometheus text format → Extract metrics → Store atomically
3. **TUI Updates**: Every TUI tick (~500ms) → `GetMetrics()` → Read atomic values → Render to screen

### Key Design Decisions

1. **Atomic Storage**: Use `atomic.Value` for entire metrics struct (lock-free pointer swap)
2. **Separate Parsing**: High-performance parser separate from scraper logic
3. **Rate Calculation**: Track previous values atomically for rate calculations
4. **Error Isolation**: One exporter failure doesn't affect the other

---

## Implementation Details

### File Structure

```
internal/
├── metrics/
│   ├── origin_scraper.go          # Main scraper (refactor to atomics)
│   ├── origin_scraper_test.go     # Unit tests with mocks
│   ├── origin_scraper_bench_test.go  # Benchmarks
│   ├── prometheus_parser.go       # High-performance parser
│   ├── prometheus_parser_test.go  # Parser tests
│   └── prometheus_parser_bench_test.go  # Parser benchmarks
└── tui/
    ├── model.go                    # Add originMetrics field
    └── view.go                     # Add renderOriginMetrics()
```

### Core Types

```go
// OriginMetrics contains scraped metrics from origin server exporters.
// All fields are read-only after atomic store (no mutex needed).
type OriginMetrics struct {
    // Node exporter metrics
    CPUPercent float64
    MemUsed    int64
    MemTotal   int64
    MemPercent float64
    NetInRate  float64 // bytes/sec
    NetOutRate float64 // bytes/sec

    // Nginx exporter metrics
    NginxConnections int64
    NginxReqRate     float64 // requests/sec
    NginxReqDuration float64 // avg duration in seconds (P99)

    // Metadata
    LastUpdate time.Time
    Healthy    bool
    Error      string
}

// OriginScraper scrapes metrics from node_exporter and nginx_exporter.
// Uses atomic.Value for lock-free metric storage.
type OriginScraper struct {
    nodeExporterURL  string
    nginxExporterURL string
    interval         time.Duration
    logger           *slog.Logger
    httpClient       *http.Client

    // Atomic storage (lock-free reads)
    metrics atomic.Value // *OriginMetrics

    // Rate calculation state (atomic for lock-free updates)
    lastNetIn     atomic.Uint64 // float64 as bits
    lastNetOut    atomic.Uint64 // float64 as bits
    lastNetTime   atomic.Value  // time.Time
    lastNginxReqs atomic.Uint64 // float64 as bits
    lastNginxTime atomic.Value  // time.Time
}
```

### Scraper Implementation

```go
// NewOriginScraper creates a new origin metrics scraper.
// Returns nil if both URLs are empty (feature disabled).
func NewOriginScraper(nodeExporterURL, nginxExporterURL string, interval time.Duration, logger *slog.Logger) *OriginScraper {
    if nodeExporterURL == "" && nginxExporterURL == "" {
        return nil // Feature disabled
    }

    scraper := &OriginScraper{
        nodeExporterURL:  nodeExporterURL,
        nginxExporterURL: nginxExporterURL,
        interval:         interval,
        logger:           logger,
        httpClient: &http.Client{
            Timeout: 5 * time.Second,
        },
    }

    // Initialize with empty metrics
    scraper.metrics.Store(&OriginMetrics{
        Healthy: false,
        Error:   "Not yet scraped",
    })

    return scraper
}

// Run starts the scraper goroutine.
func (s *OriginScraper) Run(ctx context.Context) {
    if s == nil {
        return // Feature disabled
    }

    ticker := time.NewTicker(s.interval)
    defer ticker.Stop()

    // Initial scrape
    s.scrapeAll()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            s.scrapeAll()
        }
    }
}

// GetMetrics returns the current metrics (thread-safe, lock-free).
func (s *OriginScraper) GetMetrics() *OriginMetrics {
    if s == nil {
        return nil // Feature disabled
    }

    ptr := s.metrics.Load()
    if ptr == nil {
        return nil
    }

    // Return a copy to avoid race conditions
    m := ptr.(*OriginMetrics)
    return &OriginMetrics{
        CPUPercent:      m.CPUPercent,
        MemUsed:         m.MemUsed,
        MemTotal:        m.MemTotal,
        MemPercent:      m.MemPercent,
        NetInRate:       m.NetInRate,
        NetOutRate:      m.NetOutRate,
        NginxConnections: m.NginxConnections,
        NginxReqRate:    m.NginxReqRate,
        NginxReqDuration: m.NginxReqDuration,
        LastUpdate:      m.LastUpdate,
        Healthy:         m.Healthy,
        Error:           m.Error,
    }
}
```

---

## Feature Flag Design

### Configuration Flow

1. **Default State**: Feature disabled (both URLs empty)
2. **Enable Node Exporter**: Set `-origin-metrics <URL>` → Scraper created, only node metrics scraped
3. **Enable Both**: Set both `-origin-metrics` and `-nginx-metrics` → Both exporters scraped
4. **Partial Failure**: If one exporter fails, continue with the other

### Feature Flag Logic

```go
// In config/config.go
func (c *Config) OriginMetricsEnabled() bool {
    return c.OriginMetricsURL != "" || c.NginxMetricsURL != ""
}

// In cmd/main.go
if cfg.OriginMetricsEnabled() {
    scraper := metrics.NewOriginScraper(
        cfg.OriginMetricsURL,
        cfg.NginxMetricsURL,
        cfg.OriginMetricsInterval,
        logger,
    )
    if scraper != nil {
        go scraper.Run(ctx)
        // Pass to TUI model
        tuiModel.SetOriginScraper(scraper)
    }
}
```

### TUI Display Logic

```go
// In tui/view.go
func (m Model) renderOriginMetrics() string {
    if m.originScraper == nil {
        return dimStyle.Render("Origin metrics not configured")
    }

    metrics := m.originScraper.GetMetrics()
    if metrics == nil || !metrics.Healthy {
        return dimStyle.Render("Origin metrics unavailable")
    }

    // Render metrics...
}
```

---

## CLI Configuration

### Flag Definitions

```go
// In config/flags.go

// Origin Metrics
flag.StringVar(&cfg.OriginMetricsURL, "origin-metrics", cfg.OriginMetricsURL,
    "Origin node_exporter URL (e.g., http://10.177.0.10:9100/metrics). "+
    "If empty, origin metrics are disabled. "+
    "Defaults to empty (disabled).")

flag.StringVar(&cfg.NginxMetricsURL, "nginx-metrics", cfg.NginxMetricsURL,
    "Origin nginx_exporter URL (e.g., http://10.177.0.10:9113/metrics). "+
    "If empty, nginx metrics are disabled. "+
    "Defaults to empty (disabled).")

flag.DurationVar(&cfg.OriginMetricsInterval, "origin-metrics-interval", cfg.OriginMetricsInterval,
    "Interval for scraping origin metrics. "+
    "Default: 2s. "+
    "Lower values increase load on origin server.")

// Port override flags (convenience)
flag.StringVar(&cfg.OriginMetricsHost, "origin-metrics-host", "",
    "Origin server hostname/IP for metrics (e.g., 10.177.0.10). "+
    "If set, constructs URLs using default ports (9100 for node, 9113 for nginx). "+
    "Overrides -origin-metrics and -nginx-metrics if they are not explicitly set.")

flag.IntVar(&cfg.OriginMetricsNodePort, "origin-metrics-node-port", 9100,
    "Node exporter port (used with -origin-metrics-host). "+
    "Default: 9100 (standard Prometheus node_exporter port).")

flag.IntVar(&cfg.OriginMetricsNginxPort, "origin-metrics-nginx-port", 9113,
    "Nginx exporter port (used with -origin-metrics-host). "+
    "Default: 9113 (standard nginx_exporter port).")
```

### Default Port Resolution

```go
// In config/config.go
func (c *Config) ResolveOriginMetricsURLs() (nodeURL, nginxURL string) {
    // If explicit URLs provided, use them
    if c.OriginMetricsURL != "" {
        nodeURL = c.OriginMetricsURL
    }
    if c.NginxMetricsURL != "" {
        nginxURL = c.NginxMetricsURL
    }

    // If host provided, construct URLs from host + ports
    if c.OriginMetricsHost != "" {
        if nodeURL == "" {
            nodeURL = fmt.Sprintf("http://%s:%d/metrics",
                c.OriginMetricsHost, c.OriginMetricsNodePort)
        }
        if nginxURL == "" {
            nginxURL = fmt.Sprintf("http://%s:%d/metrics",
                c.OriginMetricsHost, c.OriginMetricsNginxPort)
        }
    }

    return nodeURL, nginxURL
}
```

### Usage Examples

```bash
# Disabled (default)
./go-ffmpeg-hls-swarm -clients 100 http://origin/stream.m3u8

# Enable with explicit URLs
./go-ffmpeg-hls-swarm -clients 100 \
    -origin-metrics http://10.177.0.10:9100/metrics \
    -nginx-metrics http://10.177.0.10:9113/metrics \
    http://origin/stream.m3u8

# Enable with host (uses default ports)
./go-ffmpeg-hls-swarm -clients 100 \
    -origin-metrics-host 10.177.0.10 \
    http://origin/stream.m3u8

# Override ports
./go-ffmpeg-hls-swarm -clients 100 \
    -origin-metrics-host 10.177.0.10 \
    -origin-metrics-node-port 19100 \
    -origin-metrics-nginx-port 19113 \
    http://origin/stream.m3u8

# Custom scrape interval
./go-ffmpeg-hls-swarm -clients 100 \
    -origin-metrics-host 10.177.0.10 \
    -origin-metrics-interval 5s \
    http://origin/stream.m3u8
```

---

## High-Performance Parsing

### Prometheus Text Format

Prometheus text format is line-based:
```
# HELP metric_name Description
# TYPE metric_name counter
metric_name{label="value"} 123.456
```

### Parsing Strategy

1. **Streaming Parser**: Parse line-by-line without loading entire response into memory
2. **Regex Compilation**: Pre-compile regex patterns (once at init)
3. **String Pooling**: Reuse string buffers where possible
4. **Early Exit**: Stop parsing once required metrics are found

### Parser Implementation

```go
// PrometheusParser provides high-performance parsing of Prometheus text format.
type PrometheusParser struct {
    // Pre-compiled regex patterns (compiled once at init)
    metricLineRe *regexp.Regexp
    labelRe      *regexp.Regexp
}

func NewPrometheusParser() *PrometheusParser {
    return &PrometheusParser{
        // Match: metric_name{label="value"} 123.456
        metricLineRe: regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)\s*(?:\{([^}]+)\})?\s+([0-9.+-eE]+)`),
        // Match: label="value"
        labelRe: regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)=("([^"]*)"|([^,}]+))`),
    }
}

// ParseMetrics parses Prometheus text format from reader.
// Returns map of metric name -> value (for single-value metrics).
// For multi-value metrics (e.g., node_cpu_seconds_total with mode labels),
// returns aggregated values.
func (p *PrometheusParser) ParseMetrics(r io.Reader) (map[string]float64, error) {
    metrics := make(map[string]float64)
    scanner := bufio.NewScanner(r)

    // Pre-allocate buffer for large responses
    const maxCapacity = 1024 * 1024 // 1MB
    buf := make([]byte, 0, 4096)
    scanner.Buffer(buf, maxCapacity)

    for scanner.Scan() {
        line := scanner.Bytes()
        if len(line) == 0 || line[0] == '#' {
            continue // Skip comments and empty lines
        }

        // Parse metric line
        matches := p.metricLineRe.FindSubmatch(line)
        if len(matches) < 4 {
            continue // Not a valid metric line
        }

        metricName := string(matches[1])
        labelsStr := string(matches[2])
        valueStr := string(matches[3])

        // Parse value
        value, err := strconv.ParseFloat(valueStr, 64)
        if err != nil {
            continue // Invalid value
        }

        // Handle labeled metrics (aggregate by metric name)
        if len(labelsStr) > 0 {
            // For node_cpu_seconds_total, aggregate by mode
            // For node_network_*, aggregate by device (skip lo)
            p.aggregateLabeledMetric(metrics, metricName, labelsStr, value)
        } else {
            // Simple metric, direct assignment
            metrics[metricName] = value
        }
    }

    if err := scanner.Err(); err != nil {
        return nil, fmt.Errorf("scanner error: %w", err)
    }

    return metrics, nil
}

// aggregateLabeledMetric aggregates labeled metrics based on metric type.
func (p *PrometheusParser) aggregateLabeledMetric(metrics map[string]float64, name, labelsStr string, value float64) {
    switch {
    case name == "node_cpu_seconds_total":
        // Aggregate by mode (idle vs others)
        labels := p.parseLabels(labelsStr)
        mode := labels["mode"]
        if mode == "idle" {
            metrics["node_cpu_idle_total"] += value
        }
        metrics["node_cpu_total"] += value

    case name == "node_network_receive_bytes_total" || name == "node_network_transmit_bytes_total":
        // Aggregate non-loopback interfaces
        labels := p.parseLabels(labelsStr)
        if labels["device"] != "lo" {
            metrics[name+"_total"] += value
        }

    default:
        // For other metrics, use first value or sum
        if _, exists := metrics[name]; !exists {
            metrics[name] = value
        } else {
            metrics[name] += value
        }
    }
}

// parseLabels parses label string into map.
func (p *PrometheusParser) parseLabels(labelsStr string) map[string]string {
    labels := make(map[string]string)
    matches := p.labelRe.FindAllStringSubmatch(labelsStr, -1)
    for _, match := range matches {
        if len(match) >= 3 {
            key := match[1]
            value := match[3]
            if value == "" {
                value = match[4] // Unquoted value
            }
            labels[key] = value
        }
    }
    return labels
}
```

### Performance Optimizations

1. **Pre-compiled Regex**: Compile once at init, reuse for all scrapes
2. **Buffer Pooling**: Reuse scanner buffers (if needed, use sync.Pool)
3. **Early Exit**: Stop parsing once all required metrics found
4. **String Avoidance**: Work with `[]byte` where possible, convert to string only when needed
5. **Map Pre-allocation**: Pre-allocate metric map with expected size

---

## Atomic Operations Design

### Current Implementation (Mutex-Based)

The existing `origin_scraper.go` uses `sync.RWMutex` for metric storage:

```go
type OriginScraper struct {
    metrics *OriginMetrics
    mu      sync.RWMutex
}

func (s *OriginScraper) GetMetrics() *OriginMetrics {
    s.mu.RLock()
    defer s.mu.RUnlock()
    // Return copy...
}
```

### Atomic Migration

Replace mutex with `atomic.Value` for lock-free reads:

```go
type OriginScraper struct {
    metrics atomic.Value // *OriginMetrics
}

func (s *OriginScraper) GetMetrics() *OriginMetrics {
    ptr := s.metrics.Load()
    if ptr == nil {
        return nil
    }
    m := ptr.(*OriginMetrics)
    // Return copy...
}
```

### Rate Calculation State

For rate calculations (network bytes/sec, requests/sec), we need to track previous values:

```go
type OriginScraper struct {
    // Rate calculation state (atomic for lock-free updates)
    lastNetIn     atomic.Uint64 // float64 as bits (math.Float64bits)
    lastNetOut    atomic.Uint64 // float64 as bits
    lastNetTime   atomic.Value  // time.Time
    lastNginxReqs atomic.Uint64 // float64 as bits
    lastNginxTime atomic.Value  // time.Time
}

// Helper functions for atomic float64
func storeFloat64(addr *atomic.Uint64, val float64) {
    addr.Store(math.Float64bits(val))
}

func loadFloat64(addr *atomic.Uint64) float64 {
    return math.Float64frombits(addr.Load())
}
```

### Update Pattern

```go
func (s *OriginScraper) scrapeAll() {
    now := time.Now()
    metrics := &OriginMetrics{}

    // Scrape node_exporter
    if s.nodeExporterURL != "" {
        nodeMetrics, err := s.scrapeNodeExporter()
        if err == nil {
            // Extract metrics...
            metrics.CPUPercent = extractCPU(nodeMetrics)
            metrics.MemUsed, metrics.MemTotal, metrics.MemPercent = extractMemory(nodeMetrics)

            // Calculate network rates (atomic reads)
            netInTotal, netOutTotal := extractNetworkTotals(nodeMetrics)
            lastNetIn := loadFloat64(&s.lastNetIn)
            lastNetOut := loadFloat64(&s.lastNetOut)
            lastNetTime := s.lastNetTime.Load()
            if lastNetTime != nil {
                deltaTime := now.Sub(lastNetTime.(time.Time)).Seconds()
                if deltaTime > 0 {
                    metrics.NetInRate = (netInTotal - lastNetIn) / deltaTime
                    metrics.NetOutRate = (netOutTotal - lastNetOut) / deltaTime
                }
            }
            // Atomic writes
            storeFloat64(&s.lastNetIn, netInTotal)
            storeFloat64(&s.lastNetOut, netOutTotal)
            s.lastNetTime.Store(now)
        }
    }

    // Scrape nginx_exporter
    if s.nginxExporterURL != "" {
        nginxMetrics, err := s.scrapeNginxExporter()
        if err == nil {
            // Extract metrics...
            metrics.NginxConnections = extractConnections(nginxMetrics)
            // Calculate request rate (atomic reads)
            reqTotal := extractRequestTotal(nginxMetrics)
            lastReqs := loadFloat64(&s.lastNginxReqs)
            lastTime := s.lastNginxTime.Load()
            if lastTime != nil {
                deltaTime := now.Sub(lastTime.(time.Time)).Seconds()
                if deltaTime > 0 {
                    metrics.NginxReqRate = (reqTotal - lastReqs) / deltaTime
                }
            }
            // Atomic writes
            storeFloat64(&s.lastNginxReqs, reqTotal)
            s.lastNginxTime.Store(now)
        }
    }

    metrics.LastUpdate = now
    metrics.Healthy = true

    // Atomic store (lock-free write)
    s.metrics.Store(metrics)
}
```

### Benefits of Atomic Approach

1. **Lock-Free Reads**: TUI can read metrics without blocking
2. **No Contention**: Multiple readers don't block each other
3. **Consistent with Project**: Matches patterns in `client_stats.go`
4. **Better Performance**: Atomic operations are faster than mutexes for simple reads

---

## Testing Strategy

### Unit Tests

#### Test Structure

```go
// origin_scraper_test.go
package metrics

import (
    "context"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"
)

// mockPrometheusServer creates an HTTP server that serves Prometheus metrics.
func mockPrometheusServer(t *testing.T, metrics string) *httptest.Server {
    handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path != "/metrics" {
            http.NotFound(w, r)
            return
        }
        w.Header().Set("Content-Type", "text/plain; version=0.0.4")
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(metrics))
    })
    return httptest.NewServer(handler)
}

// sampleNodeExporterMetrics returns sample node_exporter metrics.
func sampleNodeExporterMetrics() string {
    return `# HELP node_cpu_seconds_total Seconds the CPUs spent in each mode.
# TYPE node_cpu_seconds_total counter
node_cpu_seconds_total{cpu="0",mode="idle"} 12345.67
node_cpu_seconds_total{cpu="0",mode="user"} 1234.56
node_cpu_seconds_total{cpu="0",mode="system"} 567.89
node_cpu_seconds_total{cpu="1",mode="idle"} 12345.67
node_cpu_seconds_total{cpu="1",mode="user"} 1234.56
node_cpu_seconds_total{cpu="1",mode="system"} 567.89

# HELP node_memory_MemTotal_bytes Memory information field MemTotal_bytes.
# TYPE node_memory_MemTotal_bytes gauge
node_memory_MemTotal_bytes 4294967296

# HELP node_memory_MemAvailable_bytes Memory information field MemAvailable_bytes.
# TYPE node_memory_MemAvailable_bytes gauge
node_memory_MemAvailable_bytes 3000000000

# HELP node_network_receive_bytes_total Network device statistic receive_bytes.
# TYPE node_network_receive_bytes_total counter
node_network_receive_bytes_total{device="lo"} 1000000
node_network_receive_bytes_total{device="eth0"} 5000000000

# HELP node_network_transmit_bytes_total Network device statistic transmit_bytes.
# TYPE node_network_transmit_bytes_total counter
node_network_transmit_bytes_total{device="lo"} 1000000
node_network_transmit_bytes_total{device="eth0"} 8000000000
`
}

// sampleNginxExporterMetrics returns sample nginx_exporter metrics.
func sampleNginxExporterMetrics() string {
    return `# HELP nginx_connections_active Active client connections
# TYPE nginx_connections_active gauge
nginx_connections_active 42

# HELP nginx_http_requests_total Total number of HTTP requests
# TYPE nginx_http_requests_total counter
nginx_http_requests_total 123456

# HELP nginx_http_request_duration_seconds The HTTP request latencies in seconds.
# TYPE nginx_http_request_duration_seconds histogram
nginx_http_request_duration_seconds_bucket{le="0.005"} 1000
nginx_http_request_duration_seconds_bucket{le="0.01"} 2000
nginx_http_request_duration_seconds_bucket{le="0.025"} 3000
nginx_http_request_duration_seconds_bucket{le="0.05"} 4000
nginx_http_request_duration_seconds_bucket{le="0.1"} 5000
nginx_http_request_duration_seconds_bucket{le="0.25"} 6000
nginx_http_request_duration_seconds_bucket{le="0.5"} 7000
nginx_http_request_duration_seconds_bucket{le="1"} 8000
nginx_http_request_duration_seconds_bucket{le="2.5"} 9000
nginx_http_request_duration_seconds_bucket{le="5"} 10000
nginx_http_request_duration_seconds_bucket{le="+Inf"} 10000
nginx_http_request_duration_seconds_sum 500.0
nginx_http_request_duration_seconds_count 10000
`
}

func TestOriginScraper_NodeExporter(t *testing.T) {
    // Setup mock server
    nodeServer := mockPrometheusServer(t, sampleNodeExporterMetrics())
    defer nodeServer.Close()

    // Create scraper
    scraper := NewOriginScraper(
        nodeServer.URL+"/metrics",
        "", // No nginx
        1*time.Second,
        nil, // No logger for tests
    )
    defer scraper.Run(context.Background())

    // Wait for initial scrape
    time.Sleep(100 * time.Millisecond)

    // Get metrics
    metrics := scraper.GetMetrics()
    if metrics == nil {
        t.Fatal("Expected metrics, got nil")
    }

    // Verify CPU
    if metrics.CPUPercent <= 0 || metrics.CPUPercent >= 100 {
        t.Errorf("Expected CPU percent between 0-100, got %f", metrics.CPUPercent)
    }

    // Verify memory
    if metrics.MemTotal != 4294967296 {
        t.Errorf("Expected MemTotal 4294967296, got %d", metrics.MemTotal)
    }
    if metrics.MemUsed != 1294967296 { // 4GB - 3GB available
        t.Errorf("Expected MemUsed 1294967296, got %d", metrics.MemUsed)
    }

    // Verify network (should be 0 on first scrape, no rate yet)
    if metrics.NetInRate != 0 {
        t.Errorf("Expected NetInRate 0 on first scrape, got %f", metrics.NetInRate)
    }
}

func TestOriginScraper_RateCalculation(t *testing.T) {
    // Setup mock server
    nodeServer := mockPrometheusServer(t, sampleNodeExporterMetrics())
    defer nodeServer.Close()

    scraper := NewOriginScraper(
        nodeServer.URL+"/metrics",
        "",
        100*time.Millisecond, // Fast interval for test
        nil,
    )

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go scraper.Run(ctx)

    // Wait for two scrapes
    time.Sleep(250 * time.Millisecond)

    metrics := scraper.GetMetrics()
    if metrics == nil {
        t.Fatal("Expected metrics, got nil")
    }

    // After second scrape, should have rate
    if metrics.NetInRate <= 0 {
        t.Errorf("Expected NetInRate > 0 after second scrape, got %f", metrics.NetInRate)
    }
}

func TestOriginScraper_NginxExporter(t *testing.T) {
    nginxServer := mockPrometheusServer(t, sampleNginxExporterMetrics())
    defer nginxServer.Close()

    scraper := NewOriginScraper(
        "",
        nginxServer.URL+"/metrics",
        1*time.Second,
        nil,
    )

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go scraper.Run(ctx)

    time.Sleep(100 * time.Millisecond)

    metrics := scraper.GetMetrics()
    if metrics == nil {
        t.Fatal("Expected metrics, got nil")
    }

    if metrics.NginxConnections != 42 {
        t.Errorf("Expected NginxConnections 42, got %d", metrics.NginxConnections)
    }
}

func TestOriginScraper_ConcurrentReads(t *testing.T) {
    nodeServer := mockPrometheusServer(t, sampleNodeExporterMetrics())
    defer nodeServer.Close()

    scraper := NewOriginScraper(
        nodeServer.URL+"/metrics",
        "",
        1*time.Second,
        nil,
    )

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go scraper.Run(ctx)

    time.Sleep(100 * time.Millisecond)

    // Concurrent reads (simulating TUI updates)
    done := make(chan bool)
    for i := 0; i < 100; i++ {
        go func() {
            for j := 0; j < 100; j++ {
                metrics := scraper.GetMetrics()
                if metrics == nil {
                    t.Error("Expected metrics, got nil")
                }
            }
            done <- true
        }()
    }

    // Wait for all goroutines
    for i := 0; i < 100; i++ {
        <-done
    }
}

func TestOriginScraper_FeatureDisabled(t *testing.T) {
    scraper := NewOriginScraper("", "", 1*time.Second, nil)
    if scraper != nil {
        t.Error("Expected nil scraper when both URLs empty")
    }
}

func TestOriginScraper_HTTPError(t *testing.T) {
    // Server that returns 500
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusInternalServerError)
    }))
    defer server.Close()

    scraper := NewOriginScraper(
        server.URL+"/metrics",
        "",
        1*time.Second,
        nil,
    )

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go scraper.Run(ctx)

    time.Sleep(100 * time.Millisecond)

    metrics := scraper.GetMetrics()
    if metrics == nil {
        t.Fatal("Expected metrics (with error), got nil")
    }

    if metrics.Healthy {
        t.Error("Expected Healthy=false on HTTP error")
    }
    if metrics.Error == "" {
        t.Error("Expected error message on HTTP error")
    }
}
```

### Integration Tests

```go
// Test with real Prometheus exporters (if available in test environment)
func TestOriginScraper_Integration(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping integration test")
    }

    // Requires test origin server with exporters running
    scraper := NewOriginScraper(
        "http://localhost:9100/metrics",
        "http://localhost:9113/metrics",
        2*time.Second,
        nil,
    )

    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    go scraper.Run(ctx)

    time.Sleep(3 * time.Second) // Wait for scrape

    metrics := scraper.GetMetrics()
    if metrics == nil {
        t.Fatal("Expected metrics, got nil")
    }

    // Verify metrics are reasonable
    if metrics.CPUPercent < 0 || metrics.CPUPercent > 100 {
        t.Errorf("Invalid CPU percent: %f", metrics.CPUPercent)
    }
}
```

---

## Benchmarking

### Parser Benchmarks

```go
// prometheus_parser_bench_test.go
package metrics

import (
    "bytes"
    "testing"
)

func BenchmarkPrometheusParser_ParseMetrics(b *testing.B) {
    parser := NewPrometheusParser()
    metrics := sampleNodeExporterMetrics() + sampleNginxExporterMetrics()
    data := []byte(metrics)

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        r := bytes.NewReader(data)
        _, err := parser.ParseMetrics(r)
        if err != nil {
            b.Fatal(err)
        }
    }
}

func BenchmarkPrometheusParser_ParseMetrics_Large(b *testing.B) {
    parser := NewPrometheusParser()
    // Generate large metrics response (1000 metrics)
    var buf bytes.Buffer
    for i := 0; i < 1000; i++ {
        buf.WriteString("test_metric_")
        buf.WriteString(fmt.Sprintf("%d", i))
        buf.WriteString(" 123.456\n")
    }
    data := buf.Bytes()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        r := bytes.NewReader(data)
        _, err := parser.ParseMetrics(r)
        if err != nil {
            b.Fatal(err)
        }
    }
}
```

### Scraper Benchmarks

```go
// origin_scraper_bench_test.go
package metrics

import (
    "context"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"
)

func BenchmarkOriginScraper_GetMetrics(b *testing.B) {
    nodeServer := mockPrometheusServer(b, sampleNodeExporterMetrics())
    defer nodeServer.Close()

    scraper := NewOriginScraper(
        nodeServer.URL+"/metrics",
        "",
        1*time.Second,
        nil,
    )

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go scraper.Run(ctx)

    time.Sleep(100 * time.Millisecond) // Wait for initial scrape

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        metrics := scraper.GetMetrics()
        if metrics == nil {
            b.Fatal("Expected metrics, got nil")
        }
    }
}

func BenchmarkOriginScraper_GetMetrics_Concurrent(b *testing.B) {
    nodeServer := mockPrometheusServer(b, sampleNodeExporterMetrics())
    defer nodeServer.Close()

    scraper := NewOriginScraper(
        nodeServer.URL+"/metrics",
        "",
        1*time.Second,
        nil,
    )

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go scraper.Run(ctx)

    time.Sleep(100 * time.Millisecond)

    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            metrics := scraper.GetMetrics()
            if metrics == nil {
                b.Fatal("Expected metrics, got nil")
            }
        }
    })
}
```

### Performance Targets

- **Parser**: <1ms per 1000 metrics
- **GetMetrics()**: <100ns per call (atomic read)
- **Concurrent Reads**: No degradation with 100+ concurrent readers

---

## TUI Integration

### Model Updates

```go
// In internal/tui/model.go
type Model struct {
    // ... existing fields ...
    originScraper *metrics.OriginScraper
}

func (m *Model) SetOriginScraper(scraper *metrics.OriginScraper) {
    m.originScraper = scraper
}
```

### View Updates

```go
// In internal/tui/view.go
func (m Model) renderOriginMetrics() string {
    if m.originScraper == nil {
        return dimStyle.Render("Origin metrics not configured")
    }

    metrics := m.originScraper.GetMetrics()
    if metrics == nil || !metrics.Healthy {
        if metrics != nil && metrics.Error != "" {
            return dimStyle.Render(fmt.Sprintf("Origin metrics: %s", metrics.Error))
        }
        return dimStyle.Render("Origin metrics unavailable")
    }

    var sections []string

    // Origin Server section
    sections = append(sections,
        sectionTitle.Render("Origin Server"),
        renderMetricRow("CPU:", formatPercentRaw(metrics.CPUPercent/100),
            renderProgressBar(metrics.CPUPercent/100, 10), nil, nil),
        renderMetricRow("Memory:",
            fmt.Sprintf("%s / %s", formatBytesRaw(metrics.MemUsed), formatBytesRaw(metrics.MemTotal)),
            formatBracketPercent(metrics.MemPercent/100), nil, nil),
        renderMetricRow("Net In:", formatBytesRaw(int64(metrics.NetInRate))+"/s", "", nil, nil),
        renderMetricRow("Net Out:", formatBytesRaw(int64(metrics.NetOutRate))+"/s", "", nil, nil),
        "",
    )

    // Nginx section
    if metrics.NginxConnections > 0 || metrics.NginxReqRate > 0 {
        sections = append(sections,
            sectionTitle.Render("Nginx"),
            renderMetricRow("Connections:", formatNumberRaw(metrics.NginxConnections), "", nil, nil),
            renderMetricRow("Req/sec:", fmt.Sprintf("%.1f", metrics.NginxReqRate), "", nil, nil),
        )
        if metrics.NginxReqDuration > 0 {
            sections = append(sections,
                renderMetricRow("Req Time P99:", formatMsRaw(metrics.NginxReqDuration*1000), "", nil, nil),
            )
        }
    }

    return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// renderProgressBar renders a simple text progress bar.
func renderProgressBar(progress float64, width int) string {
    filled := int(progress * float64(width))
    if filled > width {
        filled = width
    }
    bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
    return fmt.Sprintf("[%s]", bar)
}
```

### Layout Integration

Update the main TUI layout to include origin metrics in the right column:

```go
// In internal/tui/view.go
func (m Model) View() string {
    // ... existing layout code ...

    // Right column: Origin metrics
    rightCol := m.renderOriginMetrics()

    // Combine columns
    return lipgloss.JoinHorizontal(lipgloss.Top,
        leftCol,
        "  ", // Spacing
        rightCol,
    )
}
```

---

## Implementation Phases

### Phase 1: Core Scraper with Atomics (Week 1)

**Tasks:**
1. Refactor `origin_scraper.go` to use `atomic.Value` instead of mutex
2. Implement atomic rate calculation helpers
3. Update `GetMetrics()` to return copy (thread-safe)
4. Add unit tests for atomic operations

**Deliverables:**
- ✅ `origin_scraper.go` with atomic storage
- ✅ Unit tests passing
- ✅ Race detector clean

### Phase 2: High-Performance Parser (Week 1)

**Tasks:**
1. Create `prometheus_parser.go` with pre-compiled regex
2. Implement streaming parser
3. Add parser benchmarks
4. Optimize based on benchmark results

**Deliverables:**
- ✅ `prometheus_parser.go` with <1ms parsing
- ✅ Benchmarks showing performance targets met

### Phase 3: Feature Flag and CLI (Week 1)

**Tasks:**
1. Add CLI flags to `config/flags.go`
2. Implement default port resolution
3. Add host-based URL construction
4. Update config validation

**Deliverables:**
- ✅ CLI flags working
- ✅ Default ports from PORTS.md
- ✅ Port override flags

### Phase 4: Testing and Mocks (Week 2)

**Tasks:**
1. Create mock Prometheus servers for tests
2. Add comprehensive unit tests
3. Add concurrent read tests
4. Add error handling tests

**Deliverables:**
- ✅ Test coverage >80%
- ✅ All tests passing
- ✅ Mock servers reusable

### Phase 5: TUI Integration (Week 2)

**Tasks:**
1. Update TUI model to accept scraper
2. Implement `renderOriginMetrics()`
3. Integrate into main layout
4. Test with various terminal sizes

**Deliverables:**
- ✅ Origin metrics displayed in TUI
- ✅ Graceful degradation when disabled
- ✅ Layout works at different sizes

### Phase 6: Documentation and Polish (Week 2)

**Tasks:**
1. Update README with usage examples
2. Add inline code comments
3. Update PORTS.md if needed
4. Performance validation

**Deliverables:**
- ✅ Documentation complete
- ✅ Code reviewed
- ✅ Ready for merge

---

## Performance Considerations

### Scraping Frequency

- **Default**: 2 seconds (balance between freshness and load)
- **High-load scenarios**: 5 seconds (reduce origin server load)
- **Debug scenarios**: 1 second (more frequent updates)

### Memory Usage

- **Metrics struct**: ~200 bytes
- **Parser buffers**: ~4KB per scrape (reused)
- **Total overhead**: <10MB for scraper

### CPU Usage

- **Parsing**: <1ms per scrape (negligible)
- **Atomic reads**: <100ns (negligible)
- **HTTP requests**: ~5-10ms (network bound, async)

### Network Impact

- **Request size**: ~50KB per endpoint (Prometheus text format)
- **Bandwidth**: ~100KB/sec per endpoint (at 2s interval)
- **Connection overhead**: HTTP/1.1 keep-alive (reuse connections)

---

## Error Handling

### Error Categories

1. **Network Errors**: Timeout, connection refused, DNS failure
2. **HTTP Errors**: 404, 500, etc.
3. **Parse Errors**: Invalid Prometheus format
4. **Missing Metrics**: Required metrics not found in response

### Error Handling Strategy

```go
func (s *OriginScraper) scrapeAll() {
    now := time.Now()
    healthy := true
    var errors []string

    // Scrape node_exporter
    if s.nodeExporterURL != "" {
        if err := s.scrapeNodeExporter(); err != nil {
            healthy = false
            errors = append(errors, fmt.Sprintf("node_exporter: %v", err))
            s.logger.Debug("node_exporter_scrape_error", "error", err)
        }
    }

    // Scrape nginx_exporter
    if s.nginxExporterURL != "" {
        if err := s.scrapeNginxExporter(); err != nil {
            healthy = false
            errors = append(errors, fmt.Sprintf("nginx_exporter: %v", err))
            s.logger.Debug("nginx_exporter_scrape_error", "error", err)
        }
    }

    // Update metrics (preserve last known good values if scrape failed)
    current := s.metrics.Load()
    if current != nil {
        lastMetrics := current.(*OriginMetrics)
        // Only update if scrape succeeded, otherwise keep last values
        if healthy {
            // Store new metrics...
        } else {
            // Update error but keep last values
            newMetrics := *lastMetrics
            newMetrics.Healthy = false
            newMetrics.Error = strings.Join(errors, "; ")
            s.metrics.Store(&newMetrics)
        }
    }
}
```

### Graceful Degradation

- **One exporter fails**: Continue with the other
- **Both fail**: Show last known good values with error indicator
- **Never scraped**: Show "Not yet scraped" message
- **Feature disabled**: Show "Origin metrics not configured"

---

## Future Enhancements

### Potential Improvements

1. **Metric Caching**: Cache parsed metrics to reduce parsing overhead
2. **Histogram Support**: Extract P50, P95, P99 from nginx latency histograms
3. **Disk Metrics**: Add disk I/O metrics from node_exporter
4. **Custom Metrics**: Support custom Prometheus endpoints
5. **TLS Support**: Add TLS client certificate support for secure exporters
6. **Authentication**: Support Basic Auth or Bearer token for exporters
7. **Metric Filtering**: Allow filtering which metrics to scrape (reduce parsing)
8. **Alerting**: Alert when origin metrics exceed thresholds

### Not in Scope (v1)

- Prometheus server integration (this is a scraper, not a server)
- Metric aggregation across multiple origins
- Historical metric storage
- Grafana integration

---

## References

- [TUI_DEFECTS.md §Defect F](TUI_DEFECTS.md#defect-f-unused-screen-real-estate---missing-origin-metrics)
- [PORTS.md](PORTS.md) - Port configuration reference
- [ATOMIC_MIGRATION_PLAN.md](ATOMIC_MIGRATION_PLAN.md) - Atomic operations patterns
- [Prometheus Text Format](https://prometheus.io/docs/instrumenting/exposition_formats/#text-based-format)
- [node_exporter Metrics](https://github.com/prometheus/node_exporter#collectors)
- [nginx_exporter Metrics](https://github.com/nginxinc/nginx-prometheus-exporter)

---

## Appendix: Example Prometheus Responses

### Node Exporter Sample

```
# HELP node_cpu_seconds_total Seconds the CPUs spent in each mode.
# TYPE node_cpu_seconds_total counter
node_cpu_seconds_total{cpu="0",mode="idle"} 12345.67
node_cpu_seconds_total{cpu="0",mode="user"} 1234.56
node_cpu_seconds_total{cpu="0",mode="system"} 567.89

# HELP node_memory_MemTotal_bytes Memory information field MemTotal_bytes.
# TYPE node_memory_MemTotal_bytes gauge
node_memory_MemTotal_bytes 4294967296

# HELP node_memory_MemAvailable_bytes Memory information field MemAvailable_bytes.
# TYPE node_memory_MemAvailable_bytes gauge
node_memory_MemAvailable_bytes 3000000000

# HELP node_network_receive_bytes_total Network device statistic receive_bytes.
# TYPE node_network_receive_bytes_total counter
node_network_receive_bytes_total{device="eth0"} 5000000000

# HELP node_network_transmit_bytes_total Network device statistic transmit_bytes.
# TYPE node_network_transmit_bytes_total counter
node_network_transmit_bytes_total{device="eth0"} 8000000000
```

### Nginx Exporter Sample

```
# HELP nginx_connections_active Active client connections
# TYPE nginx_connections_active gauge
nginx_connections_active 42

# HELP nginx_http_requests_total Total number of HTTP requests
# TYPE nginx_http_requests_total counter
nginx_http_requests_total 123456

# HELP nginx_http_request_duration_seconds The HTTP request latencies in seconds.
# TYPE nginx_http_request_duration_seconds histogram
nginx_http_request_duration_seconds_bucket{le="0.005"} 1000
nginx_http_request_duration_seconds_bucket{le="0.01"} 2000
nginx_http_request_duration_seconds_bucket{le="+Inf"} 10000
nginx_http_request_duration_seconds_sum 500.0
nginx_http_request_duration_seconds_count 10000
```

---

**Document Status**: PROPOSAL
**Last Updated**: 2026-01-22
**Author**: Implementation Plan
**Review Status**: Pending
