# Origin Metrics Rolling Window Percentiles - Implementation Plan

**Status**: Ready for Review
**Date**: 2026-01-22
**Related**: [ORIGIN_METRICS_ROLLING_WINDOW_DESIGN.md](ORIGIN_METRICS_ROLLING_WINDOW_DESIGN.md)

---

## Table of Contents

1. [Overview](#overview)
2. [Implementation Phases](#implementation-phases)
3. [Detailed Code Changes](#detailed-code-changes)
4. [Unit Tests](#unit-tests)
5. [Integration Points](#integration-points)
6. [Testing Strategy](#testing-strategy)
7. [Edge Cases & Error Handling](#edge-cases--error-handling)
8. [Performance Validation](#performance-validation)

---

## Overview

This plan implements rolling window percentiles (P50 and Max) for network rate metrics (Net In/Out) in the origin metrics scraper. The implementation uses T-Digest (following existing patterns in `debug_events.go`) with a configurable time window (default: 30s, max: 300s).

### Key Requirements

- ✅ Configurable window duration (10s-300s, default: 30s)
- ✅ T-Digest for percentile calculation (consistent with existing codebase)
- ✅ Time-based sample expiration
- ✅ Thread-safe with mutex protection
- ✅ Backward compatible (instantaneous metrics preserved)
- ✅ Comprehensive unit tests
- ✅ TUI display integration

---

## Implementation Phases

### Phase 1: Update OriginScraper Structure
**Files**: `internal/metrics/origin_scraper.go`

**Changes**:
1. Add T-Digest dependencies and fields
2. Update `OriginMetrics` struct with percentile fields
3. Update `NewOriginScraper()` signature
4. Add helper methods for cleanup

**Estimated Time**: 30 minutes

### Phase 2: Implement Rolling Window Logic
**Files**: `internal/metrics/origin_scraper.go`

**Changes**:
1. Update `extractNetwork()` to add samples to T-Digest
2. Implement `cleanupNetworkWindow()` helper
3. Update `GetMetrics()` to calculate percentiles

**Estimated Time**: 45 minutes

### Phase 3: Update Orchestrator Integration
**Files**: `internal/orchestrator/orchestrator.go`

**Changes**:
1. Pass `cfg.OriginMetricsWindow` to `NewOriginScraper()`

**Estimated Time**: 5 minutes

### Phase 4: Update TUI Display
**Files**: `internal/tui/view.go`

**Changes**:
1. Update `renderOriginMetrics()` to display percentiles
2. Format: `Net In: 45.2 MB/s (instant) | P50: 23.1 MB/s | Max: 487.3 MB/s (30s)`

**Estimated Time**: 20 minutes

### Phase 5: Comprehensive Unit Tests
**Files**: `internal/metrics/origin_scraper_test.go`

**Changes**:
1. Test rolling window with various window sizes
2. Test percentile calculation accuracy
3. Test time-based expiration
4. Test thread safety
5. Test edge cases

**Estimated Time**: 90 minutes

### Phase 6: Integration Testing
**Files**: Manual testing, Makefile targets

**Changes**:
1. Test with real Prometheus exporters
2. Verify TUI display
3. Test with different window sizes
4. Performance validation

**Estimated Time**: 30 minutes

**Total Estimated Time**: ~3.5 hours

---

## Detailed Code Changes

### 1. Update `internal/metrics/origin_scraper.go`

#### 1.1 Add Imports

```go
import (
    // ... existing imports ...
    "sync"  // For sync.Mutex
    "github.com/influxdata/tdigest"  // For percentile calculation
)
```

#### 1.2 Update `OriginMetrics` Struct

```go
type OriginMetrics struct {
    // ... existing fields ...

    // Instantaneous rates (existing)
    NetInRate  float64 // bytes/sec
    NetOutRate float64 // bytes/sec

    // Rolling window percentiles (new)
    NetInP50  float64 // P50 (median) over rolling window
    NetInMax  float64 // Max over rolling window
    NetOutP50 float64 // P50 (median) over rolling window
    NetOutMax float64 // Max over rolling window
    NetWindowSeconds int // Window size in seconds (for display)
}
```

#### 1.3 Update `OriginScraper` Struct

```go
type OriginScraper struct {
    // ... existing fields ...

    // Rolling windows for network metrics (using T-Digest)
    netInDigest  *tdigest.TDigest
    netInSamples []networkSample
    netInMu      sync.Mutex

    netOutDigest  *tdigest.TDigest
    netOutSamples []networkSample
    netOutMu      sync.Mutex

    windowSize time.Duration // Configurable window size
    lastClean  time.Time     // Last cleanup time
}

type networkSample struct {
    value float64
    time  time.Time
}
```

#### 1.4 Update `NewOriginScraper()` Function

```go
// NewOriginScraper creates a new origin metrics scraper.
// Returns nil if both URLs are empty (feature disabled).
func NewOriginScraper(nodeExporterURL, nginxExporterURL string, interval, windowSize time.Duration, logger *slog.Logger) *OriginScraper {
    if nodeExporterURL == "" && nginxExporterURL == "" {
        return nil // Feature disabled
    }

    // Clamp window size for safety (validation also done in config.Validate())
    if windowSize < 10*time.Second {
        windowSize = 10 * time.Second
    }
    if windowSize > 300*time.Second {
        windowSize = 300 * time.Second
    }

    scraper := &OriginScraper{
        nodeExporterURL:  nodeExporterURL,
        nginxExporterURL: nginxExporterURL,
        interval:         interval,
        logger:           logger,
        httpClient: &http.Client{
            Timeout: 5 * time.Second,
        },
        // Initialize T-Digests
        netInDigest:  tdigest.NewWithCompression(100),
        netOutDigest: tdigest.NewWithCompression(100),
        windowSize:   windowSize,
        lastClean:    time.Now(),
    }

    // Initialize with empty metrics
    scraper.metrics.Store(&OriginMetrics{
        Healthy: false,
        Error:   "Not yet scraped",
    })

    return scraper
}
```

#### 1.5 Update `extractNetwork()` Method

```go
// extractNetwork extracts network metrics and calculates rates.
func (s *OriginScraper) extractNetwork(metrics map[string]*dto.MetricFamily) (inRate, outRate float64) {
    now := time.Now()

    // ... existing network extraction code (lines 342-382) ...

    // Calculate rates (atomic reads)
    lastNetIn := loadFloat64(&s.lastNetIn)
    lastNetOut := loadFloat64(&s.lastNetOut)
    lastNetTimeVal := s.lastNetTime.Load()

    if lastNetTimeVal != nil {
        lastNetTime := lastNetTimeVal.(time.Time)
        if !lastNetTime.IsZero() {
            deltaTime := now.Sub(lastNetTime).Seconds()
            if deltaTime > 0 {
                inRate = (netInTotal - lastNetIn) / deltaTime
                outRate = (netOutTotal - lastNetOut) / deltaTime
            }
        }
    }

    // Atomic writes
    storeFloat64(&s.lastNetIn, netInTotal)
    storeFloat64(&s.lastNetOut, netOutTotal)
    s.lastNetTime.Store(now)

    // Add to rolling windows (following debug_events.go pattern)
    // Net In
    s.netInMu.Lock()
    s.netInDigest.Add(inRate, 1)
    s.netInSamples = append(s.netInSamples, networkSample{value: inRate, time: now})
    // Trigger cleanup if needed (every 10s or when >20 samples)
    if len(s.netInSamples) > 20 || time.Since(s.lastClean) > 10*time.Second {
        s.cleanupNetworkWindow(&s.netInSamples, s.netInDigest, now)
    }
    s.netInMu.Unlock()

    // Net Out
    s.netOutMu.Lock()
    s.netOutDigest.Add(outRate, 1)
    s.netOutSamples = append(s.netOutSamples, networkSample{value: outRate, time: now})
    // Trigger cleanup if needed
    if len(s.netOutSamples) > 20 || time.Since(s.lastClean) > 10*time.Second {
        s.cleanupNetworkWindow(&s.netOutSamples, s.netOutDigest, now)
    }
    s.netOutMu.Unlock()

    s.lastClean = now

    return inRate, outRate
}
```

#### 1.6 Add `cleanupNetworkWindow()` Helper Method

```go
// cleanupNetworkWindow removes samples older than window and rebuilds T-Digest.
// Optimized: Only rebuilds T-Digest when samples actually expire.
func (s *OriginScraper) cleanupNetworkWindow(samples *[]networkSample, digest *tdigest.TDigest, now time.Time) {
    cutoff := now.Add(-s.windowSize)

    // Filter valid samples (keep only those within window)
    valid := make([]networkSample, 0, len(*samples))
    expiredCount := 0
    for _, sample := range *samples {
        if sample.time.After(cutoff) {
            valid = append(valid, sample)
        } else {
            expiredCount++
        }
    }

    // Only rebuild T-Digest if samples expired
    if expiredCount > 0 {
        *digest = *tdigest.NewWithCompression(100)
        for _, sample := range valid {
            digest.Add(sample.value, 1)
        }
    }

    *samples = valid
}
```

#### 1.7 Update `GetMetrics()` Method

```go
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
    now := time.Now()

    // Calculate percentiles from T-Digest (following debug_events.go pattern)
    // Net In
    s.netInMu.Lock()
    s.cleanupNetworkWindow(&s.netInSamples, s.netInDigest, now)
    var netInP50, netInMax float64
    if len(s.netInSamples) > 0 {
        netInP50 = s.netInDigest.Quantile(0.50)
        // Calculate max from samples
        netInMax = s.netInSamples[0].value
        for _, sample := range s.netInSamples {
            if sample.value > netInMax {
                netInMax = sample.value
            }
        }
    }
    s.netInMu.Unlock()

    // Net Out
    s.netOutMu.Lock()
    s.cleanupNetworkWindow(&s.netOutSamples, s.netOutDigest, now)
    var netOutP50, netOutMax float64
    if len(s.netOutSamples) > 0 {
        netOutP50 = s.netOutDigest.Quantile(0.50)
        // Calculate max from samples
        netOutMax = s.netOutSamples[0].value
        for _, sample := range s.netOutSamples {
            if sample.value > netOutMax {
                netOutMax = sample.value
            }
        }
    }
    s.netOutMu.Unlock()

    return &OriginMetrics{
        CPUPercent:      m.CPUPercent,
        MemUsed:         m.MemUsed,
        MemTotal:        m.MemTotal,
        MemPercent:      m.MemPercent,
        NetInRate:       m.NetInRate,
        NetOutRate:      m.NetOutRate,
        NetInP50:        netInP50,
        NetInMax:        netInMax,
        NetOutP50:       netOutP50,
        NetOutMax:       netOutMax,
        NetWindowSeconds: int(s.windowSize.Seconds()),
        NginxConnections: m.NginxConnections,
        NginxReqRate:    m.NginxReqRate,
        NginxReqDuration: m.NginxReqDuration,
        LastUpdate:      m.LastUpdate,
        Healthy:         m.Healthy,
        Error:           m.Error,
    }
}
```

### 2. Update `internal/orchestrator/orchestrator.go`

```go
// Around line 79
originScraper = metrics.NewOriginScraper(
    nodeURL,
    nginxURL,
    cfg.OriginMetricsInterval,
    cfg.OriginMetricsWindow,  // Add this parameter
    logger,
)
```

### 3. Update `internal/tui/view.go`

#### 3.1 Update `renderOriginMetrics()` Function

Find the function (around line 1220) and update the network metrics display section:

```go
// In renderOriginMetrics() function, find lines 1251-1252:
// OLD:
leftCol = append(leftCol, renderOriginMetricRow("Net In:", formatBytesRaw(int64(metrics.NetInRate))+"/s", ""))
leftCol = append(leftCol, renderOriginMetricRow("Net Out:", formatBytesRaw(int64(metrics.NetOutRate))+"/s", ""))

// NEW:
// Build percentile strings for Net In
netInBracket := ""
if metrics.NetInP50 > 0 || metrics.NetInMax > 0 {
    parts := []string{}
    if metrics.NetInP50 > 0 {
        parts = append(parts, fmt.Sprintf("P50: %s/s", formatBytesRaw(int64(metrics.NetInP50))))
    }
    if metrics.NetInMax > 0 {
        parts = append(parts, fmt.Sprintf("Max: %s/s", formatBytesRaw(int64(metrics.NetInMax))))
    }
    if len(parts) > 0 {
        netInBracket = fmt.Sprintf("(%s, %ds)", strings.Join(parts, ", "), metrics.NetWindowSeconds)
    }
}
leftCol = append(leftCol, renderOriginMetricRow("Net In:", formatBytesRaw(int64(metrics.NetInRate))+"/s", netInBracket))

// Build percentile strings for Net Out
netOutBracket := ""
if metrics.NetOutP50 > 0 || metrics.NetOutMax > 0 {
    parts := []string{}
    if metrics.NetOutP50 > 0 {
        parts = append(parts, fmt.Sprintf("P50: %s/s", formatBytesRaw(int64(metrics.NetOutP50))))
    }
    if metrics.NetOutMax > 0 {
        parts = append(parts, fmt.Sprintf("Max: %s/s", formatBytesRaw(int64(metrics.NetOutMax))))
    }
    if len(parts) > 0 {
        netOutBracket = fmt.Sprintf("(%s, %ds)", strings.Join(parts, ", "), metrics.NetWindowSeconds)
    }
}
leftCol = append(leftCol, renderOriginMetricRow("Net Out:", formatBytesRaw(int64(metrics.NetOutRate))+"/s", netOutBracket))
```

**Note**: The `renderOriginMetricRow` function takes `(label, value, bracket)` where bracket is displayed in the third column. This matches the existing pattern used for other metrics.

---

## Unit Tests

### Test File: `internal/metrics/origin_scraper_test.go`

### Test 1: Rolling Window Basic Functionality

```go
func TestOriginScraper_RollingWindow_Basic(t *testing.T) {
    nodeServer := mockPrometheusServer(t, sampleNodeExporterMetrics())
    defer nodeServer.Close()

    windowSize := 30 * time.Second
    scraper := NewOriginScraper(
        nodeServer.URL+"/metrics",
        "",
        100*time.Millisecond,
        windowSize,
        nil,
    )

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    go scraper.Run(ctx)

    // Wait for initial scrape
    time.Sleep(200 * time.Millisecond)

    // Get metrics multiple times to build up window
    var metrics []*OriginMetrics
    for i := 0; i < 5; i++ {
        time.Sleep(200 * time.Millisecond)
        m := scraper.GetMetrics()
        if m != nil {
            metrics = append(metrics, m)
        }
    }

    // Check that we have some metrics
    if len(metrics) == 0 {
        t.Fatal("No metrics collected")
    }

    // Last metric should have percentile data if window has samples
    last := metrics[len(metrics)-1]
    if last.NetWindowSeconds != int(windowSize.Seconds()) {
        t.Errorf("Expected window size %d, got %d", int(windowSize.Seconds()), last.NetWindowSeconds)
    }
}
```

### Test 2: Percentile Calculation Accuracy

```go
func TestOriginScraper_RollingWindow_Percentiles(t *testing.T) {
    // Create a scraper with a short window for testing
    windowSize := 5 * time.Second
    scraper := &OriginScraper{
        netInDigest:  tdigest.NewWithCompression(100),
        netOutDigest: tdigest.NewWithCompression(100),
        windowSize:   windowSize,
        lastClean:    time.Now(),
    }

    now := time.Now()
    // Add known values to test percentile calculation
    testValues := []float64{10.0, 20.0, 30.0, 40.0, 50.0, 60.0, 70.0, 80.0, 90.0, 100.0}

    scraper.netInMu.Lock()
    for i, val := range testValues {
        sampleTime := now.Add(time.Duration(i) * time.Second)
        scraper.netInDigest.Add(val, 1)
        scraper.netInSamples = append(scraper.netInSamples, networkSample{
            value: val,
            time:  sampleTime,
        })
    }
    scraper.netInMu.Unlock()

    // Get percentiles
    scraper.netInMu.Lock()
    scraper.cleanupNetworkWindow(&scraper.netInSamples, scraper.netInDigest, now.Add(10*time.Second))
    p50 := scraper.netInDigest.Quantile(0.50)
    max := scraper.netInSamples[0].value
    for _, s := range scraper.netInSamples {
        if s.value > max {
            max = s.value
        }
    }
    scraper.netInMu.Unlock()

    // P50 should be around 50-60 (median of 10 values)
    if p50 < 45 || p50 > 65 {
        t.Errorf("Expected P50 around 50-60, got %f", p50)
    }

    // Max should be 100.0
    if max != 100.0 {
        t.Errorf("Expected max 100.0, got %f", max)
    }
}
```

### Test 3: Time-Based Expiration

```go
func TestOriginScraper_RollingWindow_Expiration(t *testing.T) {
    windowSize := 5 * time.Second
    scraper := &OriginScraper{
        netInDigest:  tdigest.NewWithCompression(100),
        netInSamples: make([]networkSample, 0),
        windowSize:   windowSize,
        lastClean:    time.Now(),
    }

    baseTime := time.Now()

    // Add samples at different times
    scraper.netInMu.Lock()
    // Old samples (should expire)
    scraper.netInSamples = append(scraper.netInSamples, networkSample{
        value: 10.0,
        time:  baseTime.Add(-10 * time.Second),
    })
    scraper.netInSamples = append(scraper.netInSamples, networkSample{
        value: 20.0,
        time:  baseTime.Add(-8 * time.Second),
    })
    // Recent samples (should keep)
    scraper.netInSamples = append(scraper.netInSamples, networkSample{
        value: 30.0,
        time:  baseTime.Add(-2 * time.Second),
    })
    scraper.netInSamples = append(scraper.netInSamples, networkSample{
        value: 40.0,
        time:  baseTime.Add(-1 * time.Second),
    })

    // Add to digest
    for _, s := range scraper.netInSamples {
        scraper.netInDigest.Add(s.value, 1)
    }
    initialCount := len(scraper.netInSamples)
    scraper.netInMu.Unlock()

    // Cleanup (current time = baseTime)
    scraper.netInMu.Lock()
    scraper.cleanupNetworkWindow(&scraper.netInSamples, scraper.netInDigest, baseTime)
    finalCount := len(scraper.netInSamples)
    scraper.netInMu.Unlock()

    // Should have 2 samples remaining (recent ones)
    if finalCount != 2 {
        t.Errorf("Expected 2 samples after cleanup, got %d (initial: %d)", finalCount, initialCount)
    }

    // Verify old samples are gone
    for _, s := range scraper.netInSamples {
        if s.time.Before(baseTime.Add(-windowSize)) {
            t.Errorf("Found expired sample: %v", s)
        }
    }
}
```

### Test 4: Thread Safety

```go
func TestOriginScraper_RollingWindow_ConcurrentAccess(t *testing.T) {
    nodeServer := mockPrometheusServer(t, sampleNodeExporterMetrics())
    defer nodeServer.Close()

    windowSize := 30 * time.Second
    scraper := NewOriginScraper(
        nodeServer.URL+"/metrics",
        "",
        50*time.Millisecond,
        windowSize,
        nil,
    )

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    go scraper.Run(ctx)

    // Concurrent reads
    done := make(chan bool)
    numReaders := 10
    for i := 0; i < numReaders; i++ {
        go func() {
            for j := 0; j < 100; j++ {
                m := scraper.GetMetrics()
                if m != nil {
                    _ = m.NetInP50
                    _ = m.NetInMax
                    _ = m.NetOutP50
                    _ = m.NetOutMax
                }
                time.Sleep(1 * time.Millisecond)
            }
            done <- true
        }()
    }

    // Wait for all readers
    for i := 0; i < numReaders; i++ {
        <-done
    }
}
```

### Test 5: Window Size Configuration

```go
func TestOriginScraper_RollingWindow_ConfigurableWindow(t *testing.T) {
    testCases := []struct {
        name       string
        windowSize time.Duration
        expected   int // Expected window size in seconds
    }{
        {"default_30s", 30 * time.Second, 30},
        {"custom_60s", 60 * time.Second, 60},
        {"max_300s", 300 * time.Second, 300},
        {"clamped_min", 5 * time.Second, 10},   // Should clamp to 10s
        {"clamped_max", 500 * time.Second, 300}, // Should clamp to 300s
    }

    for _, tc := range testCases {
        t.Run(tc.name, func(t *testing.T) {
            scraper := NewOriginScraper(
                "http://localhost:9100/metrics",
                "",
                2*time.Second,
                tc.windowSize,
                nil,
            )

            if scraper == nil {
                t.Fatal("Scraper should not be nil")
            }

            // Check window size was set correctly (after clamping)
            if int(scraper.windowSize.Seconds()) != tc.expected {
                t.Errorf("Expected window size %d, got %d",
                    tc.expected, int(scraper.windowSize.Seconds()))
            }
        })
    }
}
```

### Test 6: Empty Window Handling

```go
func TestOriginScraper_RollingWindow_EmptyWindow(t *testing.T) {
    windowSize := 30 * time.Second
    scraper := &OriginScraper{
        netInDigest:  tdigest.NewWithCompression(100),
        netOutDigest: tdigest.NewWithCompression(100),
        windowSize:   windowSize,
        lastClean:    time.Now(),
    }

    // Get metrics with empty window
    metrics := scraper.GetMetrics()
    if metrics != nil {
        // Percentiles should be 0 when no samples
        if metrics.NetInP50 != 0 || metrics.NetInMax != 0 {
            t.Errorf("Expected zero percentiles for empty window, got P50=%f Max=%f",
                metrics.NetInP50, metrics.NetInMax)
        }
    }
}
```

### Test 7: Bursty Traffic Pattern

```go
func TestOriginScraper_RollingWindow_BurstyTraffic(t *testing.T) {
    windowSize := 10 * time.Second
    scraper := &OriginScraper{
        netInDigest:  tdigest.NewWithCompression(100),
        netInSamples: make([]networkSample, 0),
        windowSize:   windowSize,
        lastClean:    time.Now(),
    }

    baseTime := time.Now()

    // Simulate bursty traffic: low baseline, occasional spikes
    // Baseline: 10 MB/s
    // Bursts: 500 MB/s
    scraper.netInMu.Lock()
    for i := 0; i < 10; i++ {
        var value float64
        if i%3 == 0 {
            value = 500.0 * 1024 * 1024 // Burst: 500 MB/s
        } else {
            value = 10.0 * 1024 * 1024 // Baseline: 10 MB/s
        }
        sampleTime := baseTime.Add(time.Duration(i) * time.Second)
        scraper.netInDigest.Add(value, 1)
        scraper.netInSamples = append(scraper.netInSamples, networkSample{
            value: value,
            time:  sampleTime,
        })
    }
    scraper.netInMu.Unlock()

    // Get percentiles
    scraper.netInMu.Lock()
    scraper.cleanupNetworkWindow(&scraper.netInSamples, scraper.netInDigest, baseTime.Add(10*time.Second))
    p50 := scraper.netInDigest.Quantile(0.50)
    max := scraper.netInSamples[0].value
    for _, s := range scraper.netInSamples {
        if s.value > max {
            max = s.value
        }
    }
    scraper.netInMu.Unlock()

    // P50 should reflect median (likely around baseline)
    if p50 < 0 {
        t.Errorf("P50 should be positive, got %f", p50)
    }

    // Max should be 500 MB/s
    expectedMax := 500.0 * 1024 * 1024
    if max != expectedMax {
        t.Errorf("Expected max %f, got %f", expectedMax, max)
    }
}
```

---

## Integration Points

### 1. Orchestrator Integration

**File**: `internal/orchestrator/orchestrator.go`

**Change**: Update `NewOriginScraper()` call to include window size parameter.

**Validation**: Ensure `cfg.OriginMetricsWindow` is passed correctly.

### 2. TUI Integration

**File**: `internal/tui/view.go`

**Change**: Update `renderOriginMetrics()` to display percentile metrics.

**Validation**:
- Verify display format matches design
- Test with various window sizes
- Ensure backward compatibility (works when percentiles are 0)

### 3. Config Validation

**File**: `internal/config/validate.go`

**Status**: Already implemented (window size validation)

**Validation**: Ensure validation works correctly for edge cases.

---

## Testing Strategy

### Unit Tests

1. **Basic Functionality**: Test rolling window with mock Prometheus server
2. **Percentile Accuracy**: Verify P50 and Max calculations
3. **Time Expiration**: Test sample expiration based on window size
4. **Thread Safety**: Concurrent reads/writes with race detector
5. **Window Configuration**: Test various window sizes (10s, 30s, 60s, 300s)
6. **Edge Cases**: Empty window, single sample, all samples expired
7. **Bursty Traffic**: Simulate HLS burst patterns

### Integration Tests

1. **Real Prometheus Exporters**: Test with actual node_exporter and nginx_exporter
2. **TUI Display**: Verify metrics appear correctly in dashboard
3. **Window Size Changes**: Test with different `-origin-metrics-window` values
4. **Long-Running**: Run for extended period to verify cleanup works

### Performance Tests

1. **Memory Usage**: Verify constant memory footprint
2. **CPU Overhead**: Measure impact of T-Digest operations
3. **Cleanup Efficiency**: Verify cleanup doesn't cause spikes

### Race Detector

Run all tests with `-race` flag:
```bash
go test -race ./internal/metrics/...
```

---

## Edge Cases & Error Handling

### Edge Case 1: Empty Window (No Samples)

**Scenario**: Scraper just started, no samples collected yet.

**Handling**:
- `GetMetrics()` returns `NetInP50 = 0`, `NetInMax = 0`
- TUI displays only instantaneous rate
- No error, graceful degradation

### Edge Case 2: All Samples Expired

**Scenario**: Scraper paused/resumed, all samples outside window.

**Handling**:
- `cleanupNetworkWindow()` removes all expired samples
- T-Digest rebuilt empty
- Percentiles return 0 until new samples collected

### Edge Case 3: Single Sample

**Scenario**: Only one sample in window.

**Handling**:
- P50 = sample value (median of 1)
- Max = sample value
- Works correctly

### Edge Case 4: Window Size Changes

**Scenario**: User changes `-origin-metrics-window` flag (requires restart).

**Handling**:
- Window size is set at scraper creation
- Changes require application restart
- Old samples may be outside new window (handled by cleanup)

### Edge Case 5: Scraper Disabled

**Scenario**: Origin metrics disabled (`-origin-metrics` not set).

**Handling**:
- `NewOriginScraper()` returns `nil`
- `GetMetrics()` returns `nil`
- TUI handles `nil` gracefully (no display)

### Edge Case 6: Network Rate Calculation Fails

**Scenario**: First scrape, no previous values for rate calculation.

**Handling**:
- `inRate = 0`, `outRate = 0` (expected)
- Samples added with 0 value
- Percentiles calculated correctly (may be 0 initially)

### Edge Case 7: T-Digest Rebuild During Read

**Scenario**: `GetMetrics()` called while cleanup rebuilds T-Digest.

**Handling**:
- Mutex protects T-Digest operations
- Reader waits for cleanup to complete
- No race conditions

---

## Performance Validation

### Memory Targets

- **Per Metric**: ~10KB (T-Digest) + ~240 bytes (samples) = ~10.25KB
- **Total (2 metrics)**: ~20.5KB
- **Acceptable**: <50KB total

### CPU Targets

- **Update (normal)**: <1µs
- **Update (with cleanup)**: <10µs
- **Query (GetMetrics)**: <10µs
- **Total Overhead**: <0.1% CPU

### Validation Commands

```bash
# Memory profiling
go test -memprofile=mem.prof ./internal/metrics/...
go tool pprof mem.prof

# CPU profiling
go test -cpuprofile=cpu.prof ./internal/metrics/...
go tool pprof cpu.prof

# Benchmark
go test -bench=. -benchmem ./internal/metrics/...
```

---

## Success Criteria

✅ All unit tests pass
✅ Race detector passes
✅ TUI displays percentiles correctly
✅ Window size configuration works (10s-300s)
✅ Time-based expiration works correctly
✅ Memory usage within targets
✅ CPU overhead within targets
✅ Backward compatible (instantaneous metrics preserved)
✅ Documentation updated (if needed)

---

## Rollback Plan

If issues arise:

1. **Disable Feature**: Set `-origin-metrics-window 0` (or don't set flag, uses default)
2. **Code Rollback**: Revert commits for this feature
3. **Data**: No persistent data, no cleanup needed

---

## Next Steps After Implementation

1. Update `ORIGIN_METRICS_IMPLEMENTATION_LOG.md` with progress
2. Test with real HLS load test scenarios
3. Monitor performance in production-like environment
4. Gather user feedback on TUI display
5. Consider adding P95/P99 if needed

---

**Document Status**: READY FOR REVIEW
**Last Updated**: 2026-01-22
