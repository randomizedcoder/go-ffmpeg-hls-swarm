# Segment Latency P25 and P75 Percentile Design

## Table of Contents

1. [Overview](#overview)
2. [Current Implementation](#current-implementation)
3. [Design Changes](#design-changes)
4. [Files to Update](#files-to-update)
5. [Implementation Details](#implementation-details)
6. [Test Plan](#test-plan)
7. [TUI Dashboard Updates](#tui-dashboard-updates)
8. [Verification Steps](#verification-steps)

---

## Overview

This document describes the design for adding P25 (25th percentile) and P75 (75th percentile) metrics to the Segment Latency section of the TUI dashboard. Currently, the dashboard displays P50 (median), P95, P99, and Max. Adding P25 and P75 will provide better visibility into the distribution of segment latency, particularly for understanding the lower quartile and upper quartile performance.

### Goals

- Add P25 and P75 percentile calculations to segment latency tracking
- Display P25 and P75 in the TUI dashboard "Segment Latency" section
- Maintain consistency with existing percentile implementation (P50, P95, P99)
- Ensure thread-safety and performance characteristics remain unchanged
- Add comprehensive table-driven tests for the new percentiles

### Benefits

- **Better Distribution Understanding**: P25 and P75 provide quartile boundaries, helping identify:
  - Lower quartile performance (P25) - best-case scenarios for 25% of requests
  - Upper quartile performance (P75) - performance for 75% of requests
- **Improved Monitoring**: More granular percentile data helps identify latency distribution patterns
- **Consistency**: Completes the standard percentile set (P25, P50, P75, P95, P99)

---

## Current Implementation

### Architecture

The segment latency percentile system uses:

1. **T-Digest Algorithm**: Memory-efficient percentile calculation using `github.com/influxdata/tdigest`
2. **DebugEventParser**: Tracks segment wall times from FFmpeg timestamps
3. **Stats Aggregation**: Aggregates percentiles across multiple clients
4. **TUI Rendering**: Displays percentiles in the dashboard

### Current Percentiles

- **P50 (median)**: 50th percentile - typical performance
- **P95**: 95th percentile - worst-case for most users
- **P99**: 99th percentile - tail latency (critical for load testing)
- **Max**: Maximum observed latency

### Data Flow

```
FFmpeg Debug Output
    ↓
DebugEventParser.handleHLSRequest() / CompleteSegment()
    ↓
T-Digest (segmentWallTimeDigest.Add())
    ↓
DebugEventParser.Stats() → DebugStats
    ↓
StatsAggregator.Aggregate() → DebugStatsAggregate
    ↓
TUI renderLatencyStats() → Dashboard Display
```

---

## Design Changes

### Summary of Changes

1. **Add P25 and P75 fields** to `DebugStats` struct
2. **Add P25 and P75 fields** to `DebugStatsAggregate` struct
3. **Calculate P25 and P75** in `DebugEventParser.Stats()` method
4. **Aggregate P25 and P75** in `StatsAggregator.Aggregate()` method
5. **Update TUI rendering** to display P25 and P75 in the latency section
6. **Add comprehensive tests** for percentile calculations and aggregation

### Percentile Calculation

The T-Digest library already supports calculating any percentile (0.0 to 1.0). We simply need to:
- Call `segmentWallTimeDigest.Quantile(0.25)` for P25
- Call `segmentWallTimeDigest.Quantile(0.75)` for P75

No changes to the T-Digest initialization or data collection are needed.

---

## Files to Update

### 1. `internal/parser/debug_events.go`

**Purpose**: Core percentile calculation from T-Digest

#### Changes:

**Line ~802-804**: Add P25 and P75 fields to `DebugStats` struct
```go
// Percentiles (from T-Digest, using accurate timestamps)
SegmentWallTimeP25 time.Duration // 25th percentile
SegmentWallTimeP50 time.Duration // 50th percentile (median)
SegmentWallTimeP75 time.Duration // 75th percentile
SegmentWallTimeP95 time.Duration // 95th percentile
SegmentWallTimeP99 time.Duration // 99th percentile
```

**Line ~887-894**: Update `Stats()` method to calculate P25 and P75
```go
// Calculate percentiles from T-Digest (using accurate FFmpeg timestamps)
p.segmentWallTimeDigestMu.Lock()
if p.segmentWallTimeDigest != nil {
    stats.SegmentWallTimeP25 = time.Duration(p.segmentWallTimeDigest.Quantile(0.25))
    stats.SegmentWallTimeP50 = time.Duration(p.segmentWallTimeDigest.Quantile(0.50))
    stats.SegmentWallTimeP75 = time.Duration(p.segmentWallTimeDigest.Quantile(0.75))
    stats.SegmentWallTimeP95 = time.Duration(p.segmentWallTimeDigest.Quantile(0.95))
    stats.SegmentWallTimeP99 = time.Duration(p.segmentWallTimeDigest.Quantile(0.99))
}
p.segmentWallTimeDigestMu.Unlock()
```

**Functions Modified**:
- `Stats()` (line ~848): Add P25 and P75 calculation

---

### 2. `internal/stats/aggregator.go`

**Purpose**: Aggregate percentiles across multiple clients

#### Changes:

**Line ~97-100**: Add P25 and P75 fields to `DebugStatsAggregate` struct
```go
// Percentiles (from T-Digest, using accurate FFmpeg timestamps)
SegmentWallTimeP25 time.Duration // 25th percentile
SegmentWallTimeP50 time.Duration // 50th percentile (median)
SegmentWallTimeP75 time.Duration // 75th percentile
SegmentWallTimeP95 time.Duration // 95th percentile
SegmentWallTimeP99 time.Duration // 99th percentile
```

**Line ~586-595**: Update aggregation logic in `Aggregate()` method
```go
// Aggregate percentiles (take max across clients - worst-case is useful for load testing)
if stats.SegmentWallTimeP25 > agg.SegmentWallTimeP25 {
    agg.SegmentWallTimeP25 = stats.SegmentWallTimeP25
}
if stats.SegmentWallTimeP50 > agg.SegmentWallTimeP50 {
    agg.SegmentWallTimeP50 = stats.SegmentWallTimeP50
}
if stats.SegmentWallTimeP75 > agg.SegmentWallTimeP75 {
    agg.SegmentWallTimeP75 = stats.SegmentWallTimeP75
}
if stats.SegmentWallTimeP95 > agg.SegmentWallTimeP95 {
    agg.SegmentWallTimeP95 = stats.SegmentWallTimeP95
}
if stats.SegmentWallTimeP99 > agg.SegmentWallTimeP99 {
    agg.SegmentWallTimeP99 = stats.SegmentWallTimeP99
}
```

**Functions Modified**:
- `Aggregate()` (line ~137): Add P25 and P75 aggregation

---

### 3. `internal/orchestrator/client_manager.go`

**Purpose**: Client manager aggregation (if needed)

#### Changes:

**Line ~586-595**: Add P25 and P75 aggregation (if this file also aggregates percentiles)

**Note**: Based on the codebase search, this file appears to aggregate percentiles. Verify if it needs updates.

**Functions Modified**:
- `AggregateDebugStats()` or similar (if exists): Add P25 and P75 aggregation

---

### 4. `internal/tui/view.go`

**Purpose**: TUI dashboard rendering

#### Changes:

**Line ~252-257**: Update `renderLatencyStats()` to include P25 and P75
```go
rows := []string{
    renderLatencyRow("P25", m.debugStats.SegmentWallTimeP25),
    renderLatencyRow("P50 (median)", m.debugStats.SegmentWallTimeP50),
    renderLatencyRow("P75", m.debugStats.SegmentWallTimeP75),
    renderLatencyRow("P95", m.debugStats.SegmentWallTimeP95),
    renderLatencyRow("P99", m.debugStats.SegmentWallTimeP99),
    renderLatencyRow("Max", time.Duration(m.debugStats.SegmentWallTimeMax*float64(time.Millisecond))),
}
```

**Line ~248**: Update condition to check P25 instead of P50 (or keep P50 check)
```go
if m.debugStats == nil || m.debugStats.SegmentWallTimeP50 == 0 {
    return ""
}
```

**Functions Modified**:
- `renderLatencyStats()` (line ~246): Add P25 and P75 rows

---

## Implementation Details

### T-Digest Quantile Calculation

The T-Digest library provides the `Quantile(q float64)` method where `q` is between 0.0 and 1.0:
- `Quantile(0.25)` returns the 25th percentile
- `Quantile(0.75)` returns the 75th percentile

The T-Digest is already initialized in `NewDebugEventParser()`:
```go
segmentWallTimeDigest: tdigest.NewWithCompression(100), // ~100 centroids, ~10KB
```

### Thread Safety

The T-Digest operations are already protected by `segmentWallTimeDigestMu` mutex:
- **Reading** (in `Stats()`): Locked before calling `Quantile()`
- **Writing** (in `handleHLSRequest()` and `CompleteSegment()`): Locked before calling `Add()`

No additional synchronization needed.

### Aggregation Strategy

The current implementation uses **max aggregation** (worst-case across clients), which is appropriate for load testing:
- If client A has P25=10ms and client B has P25=15ms, aggregate P25=15ms
- This shows the worst-case performance across all clients

This strategy is maintained for P25 and P75.

### Performance Impact

- **Memory**: No change (T-Digest size is constant)
- **CPU**: Minimal increase (2 additional `Quantile()` calls per `Stats()` invocation)
- **Lock Contention**: No change (same mutex, same lock duration)

---

## Test Plan

### Test Files to Create/Update

#### 1. `internal/parser/debug_events_test.go` (Update Existing)

**Test Function**: `TestDebugEventParser_Percentiles`

**Purpose**: Verify P25, P50, P75, P95, P99 calculations from T-Digest

**Table-Driven Test Cases**:

```go
func TestDebugEventParser_Percentiles(t *testing.T) {
    tests := []struct {
        name       string
        values     []time.Duration // Input values to add to T-Digest
        wantP25    time.Duration   // Expected P25 (with tolerance)
        wantP50    time.Duration   // Expected P50 (with tolerance)
        wantP75    time.Duration   // Expected P75 (with tolerance)
        wantP95    time.Duration   // Expected P95 (with tolerance)
        wantP99    time.Duration   // Expected P99 (with tolerance)
        tolerance  time.Duration   // Acceptable error margin
    }{
        {
            name: "uniform_distribution_100_samples",
            // Add 100 samples: 1ms, 2ms, ..., 100ms
            values: func() []time.Duration {
                var vals []time.Duration
                for i := 1; i <= 100; i++ {
                    vals = append(vals, time.Duration(i)*time.Millisecond)
                }
                return vals
            }(),
            wantP25: 25 * time.Millisecond,
            wantP50: 50 * time.Millisecond,
            wantP75: 75 * time.Millisecond,
            wantP95: 95 * time.Millisecond,
            wantP99: 99 * time.Millisecond,
            tolerance: 2 * time.Millisecond, // T-Digest approximation
        },
        {
            name: "exponential_distribution",
            // Simulate exponential distribution: many fast, few slow
            values: []time.Duration{
                1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond,
                2 * time.Millisecond, 2 * time.Millisecond,
                5 * time.Millisecond,
                10 * time.Millisecond,
                20 * time.Millisecond,
                50 * time.Millisecond,
                100 * time.Millisecond,
            },
            wantP25: 1 * time.Millisecond,
            wantP50: 2 * time.Millisecond,
            wantP75: 5 * time.Millisecond,
            wantP95: 20 * time.Millisecond,
            wantP99: 100 * time.Millisecond,
            tolerance: 1 * time.Millisecond,
        },
        {
            name: "single_value",
            values: []time.Duration{10 * time.Millisecond},
            wantP25: 10 * time.Millisecond,
            wantP50: 10 * time.Millisecond,
            wantP75: 10 * time.Millisecond,
            wantP95: 10 * time.Millisecond,
            wantP99: 10 * time.Millisecond,
            tolerance: 0,
        },
        {
            name: "empty_digest",
            values: []time.Duration{},
            wantP25: 0,
            wantP50: 0,
            wantP75: 0,
            wantP95: 0,
            wantP99: 0,
            tolerance: 0,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            p := NewDebugEventParser(1, nil)

            // Add values to T-Digest
            for _, val := range tt.values {
                p.segmentWallTimeDigestMu.Lock()
                p.segmentWallTimeDigest.Add(float64(val.Nanoseconds()), 1)
                p.segmentWallTimeDigestMu.Unlock()
            }

            // Get stats
            stats := p.Stats()

            // Verify percentiles
            if !withinTolerance(stats.SegmentWallTimeP25, tt.wantP25, tt.tolerance) {
                t.Errorf("P25 = %v, want %v (tolerance: %v)",
                    stats.SegmentWallTimeP25, tt.wantP25, tt.tolerance)
            }
            if !withinTolerance(stats.SegmentWallTimeP50, tt.wantP50, tt.tolerance) {
                t.Errorf("P50 = %v, want %v (tolerance: %v)",
                    stats.SegmentWallTimeP50, tt.wantP50, tt.tolerance)
            }
            if !withinTolerance(stats.SegmentWallTimeP75, tt.wantP75, tt.tolerance) {
                t.Errorf("P75 = %v, want %v (tolerance: %v)",
                    stats.SegmentWallTimeP75, tt.wantP75, tt.tolerance)
            }
            if !withinTolerance(stats.SegmentWallTimeP95, tt.wantP95, tt.tolerance) {
                t.Errorf("P95 = %v, want %v (tolerance: %v)",
                    stats.SegmentWallTimeP95, tt.wantP95, tt.tolerance)
            }
            if !withinTolerance(stats.SegmentWallTimeP99, tt.wantP99, tt.tolerance) {
                t.Errorf("P99 = %v, want %v (tolerance: %v)",
                    stats.SegmentWallTimeP99, tt.wantP99, tt.tolerance)
            }
        })
    }
}

func withinTolerance(actual, expected, tolerance time.Duration) bool {
    diff := actual - expected
    if diff < 0 {
        diff = -diff
    }
    return diff <= tolerance
}
```

**Test Function**: `TestDebugEventParser_PercentilesOrdering`

**Purpose**: Verify P25 ≤ P50 ≤ P75 ≤ P95 ≤ P99

```go
func TestDebugEventParser_PercentilesOrdering(t *testing.T) {
    p := NewDebugEventParser(1, nil)

    // Add varied values
    values := []time.Duration{
        1 * time.Millisecond, 5 * time.Millisecond, 10 * time.Millisecond,
        20 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond,
        200 * time.Millisecond, 500 * time.Millisecond,
    }

    for _, val := range values {
        p.segmentWallTimeDigestMu.Lock()
        p.segmentWallTimeDigest.Add(float64(val.Nanoseconds()), 1)
        p.segmentWallTimeDigestMu.Unlock()
    }

    stats := p.Stats()

    // Verify ordering
    if stats.SegmentWallTimeP25 > stats.SegmentWallTimeP50 {
        t.Errorf("P25 (%v) > P50 (%v), should be ≤",
            stats.SegmentWallTimeP25, stats.SegmentWallTimeP50)
    }
    if stats.SegmentWallTimeP50 > stats.SegmentWallTimeP75 {
        t.Errorf("P50 (%v) > P75 (%v), should be ≤",
            stats.SegmentWallTimeP50, stats.SegmentWallTimeP75)
    }
    if stats.SegmentWallTimeP75 > stats.SegmentWallTimeP95 {
        t.Errorf("P75 (%v) > P95 (%v), should be ≤",
            stats.SegmentWallTimeP75, stats.SegmentWallTimeP95)
    }
    if stats.SegmentWallTimeP95 > stats.SegmentWallTimeP99 {
        t.Errorf("P95 (%v) > P99 (%v), should be ≤",
            stats.SegmentWallTimeP95, stats.SegmentWallTimeP99)
    }
}
```

---

#### 2. `internal/stats/aggregator_test.go` (Update Existing)

**Test Function**: `TestStatsAggregator_AggregatePercentiles`

**Purpose**: Verify P25 and P75 aggregation across multiple clients

**Table-Driven Test Cases**:

```go
func TestStatsAggregator_AggregatePercentiles(t *testing.T) {
    tests := []struct {
        name      string
        clients   []struct {
            p25 time.Duration
            p50 time.Duration
            p75 time.Duration
            p95 time.Duration
            p99 time.Duration
        }
        wantP25 time.Duration
        wantP50 time.Duration
        wantP75 time.Duration
        wantP95 time.Duration
        wantP99 time.Duration
    }{
        {
            name: "single_client",
            clients: []struct {
                p25, p50, p75, p95, p99 time.Duration
            }{
                {10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond,
                 50 * time.Millisecond, 100 * time.Millisecond},
            },
            wantP25: 10 * time.Millisecond,
            wantP50: 20 * time.Millisecond,
            wantP75: 30 * time.Millisecond,
            wantP95: 50 * time.Millisecond,
            wantP99: 100 * time.Millisecond,
        },
        {
            name: "multiple_clients_max_aggregation",
            clients: []struct {
                p25, p50, p75, p95, p99 time.Duration
            }{
                {10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond,
                 50 * time.Millisecond, 100 * time.Millisecond},
                {15 * time.Millisecond, 25 * time.Millisecond, 35 * time.Millisecond,
                 60 * time.Millisecond, 120 * time.Millisecond},
                {5 * time.Millisecond, 15 * time.Millisecond, 25 * time.Millisecond,
                 40 * time.Millisecond, 80 * time.Millisecond},
            },
            // Max aggregation: take worst-case across clients
            wantP25: 15 * time.Millisecond, // max(10, 15, 5)
            wantP50: 25 * time.Millisecond, // max(20, 25, 15)
            wantP75: 35 * time.Millisecond, // max(30, 35, 25)
            wantP95: 60 * time.Millisecond, // max(50, 60, 40)
            wantP99: 120 * time.Millisecond, // max(100, 120, 80)
        },
        {
            name: "zero_values",
            clients: []struct {
                p25, p50, p75, p95, p99 time.Duration
            }{
                {0, 0, 0, 0, 0},
            },
            wantP25: 0,
            wantP50: 0,
            wantP75: 0,
            wantP95: 0,
            wantP99: 0,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            agg := NewStatsAggregator(0.01)

            // Create mock DebugStats for each client
            for i, client := range tt.clients {
                stats := &parser.DebugStats{
                    SegmentWallTimeP25: client.p25,
                    SegmentWallTimeP50: client.p50,
                    SegmentWallTimeP75: client.p75,
                    SegmentWallTimeP95: client.p95,
                    SegmentWallTimeP99: client.p99,
                }

                // Use internal method to aggregate (or create test helper)
                // This depends on the actual aggregation implementation
                // For now, assume we can call AggregateDebugStats directly
                agg.AggregateDebugStats(stats) // Adjust based on actual API
            }

            result := agg.Aggregate()

            if result.SegmentWallTimeP25 != tt.wantP25 {
                t.Errorf("P25 = %v, want %v", result.SegmentWallTimeP25, tt.wantP25)
            }
            if result.SegmentWallTimeP50 != tt.wantP50 {
                t.Errorf("P50 = %v, want %v", result.SegmentWallTimeP50, tt.wantP50)
            }
            if result.SegmentWallTimeP75 != tt.wantP75 {
                t.Errorf("P75 = %v, want %v", result.SegmentWallTimeP75, tt.wantP75)
            }
            if result.SegmentWallTimeP95 != tt.wantP95 {
                t.Errorf("P95 = %v, want %v", result.SegmentWallTimeP95, tt.wantP95)
            }
            if result.SegmentWallTimeP99 != tt.wantP99 {
                t.Errorf("P99 = %v, want %v", result.SegmentWallTimeP99, tt.wantP99)
            }
        })
    }
}
```

**Note**: The exact test implementation depends on the `StatsAggregator` API. Adjust based on how `AggregateDebugStats()` or similar methods work.

---

#### 3. `internal/tui/view_test.go` (Update Existing or Create)

**Test Function**: `TestRenderLatencyStats_IncludesP25P75`

**Purpose**: Verify TUI rendering includes P25 and P75

```go
func TestRenderLatencyStats_IncludesP25P75(t *testing.T) {
    model := Model{
        width: 80,
        debugStats: &stats.DebugStatsAggregate{
            SegmentWallTimeP25: 10 * time.Millisecond,
            SegmentWallTimeP50: 20 * time.Millisecond,
            SegmentWallTimeP75: 30 * time.Millisecond,
            SegmentWallTimeP95: 50 * time.Millisecond,
            SegmentWallTimeP99: 100 * time.Millisecond,
            SegmentWallTimeMax: 200.0, // milliseconds
        },
    }

    output := model.renderLatencyStats()

    // Verify all percentiles are present
    if !strings.Contains(output, "P25") {
        t.Error("output missing P25")
    }
    if !strings.Contains(output, "P50") {
        t.Error("output missing P50")
    }
    if !strings.Contains(output, "P75") {
        t.Error("output missing P75")
    }
    if !strings.Contains(output, "P95") {
        t.Error("output missing P95")
    }
    if !strings.Contains(output, "P99") {
        t.Error("output missing P99")
    }
    if !strings.Contains(output, "Max") {
        t.Error("output missing Max")
    }

    // Verify ordering: P25 appears before P50, P50 before P75, etc.
    p25Idx := strings.Index(output, "P25")
    p50Idx := strings.Index(output, "P50")
    p75Idx := strings.Index(output, "P75")
    p95Idx := strings.Index(output, "P95")
    p99Idx := strings.Index(output, "P99")
    maxIdx := strings.Index(output, "Max")

    if p25Idx >= p50Idx {
        t.Error("P25 should appear before P50")
    }
    if p50Idx >= p75Idx {
        t.Error("P50 should appear before P75")
    }
    if p75Idx >= p95Idx {
        t.Error("P75 should appear before P95")
    }
    if p95Idx >= p99Idx {
        t.Error("P95 should appear before P99")
    }
    if p99Idx >= maxIdx {
        t.Error("P99 should appear before Max")
    }
}
```

**Test Function**: `TestRenderLatencyStats_EmptyStats`

**Purpose**: Verify empty stats don't crash

```go
func TestRenderLatencyStats_EmptyStats(t *testing.T) {
    model := Model{
        width: 80,
        debugStats: nil,
    }

    output := model.renderLatencyStats()

    if output != "" {
        t.Errorf("expected empty output for nil stats, got: %q", output)
    }
}
```

---

## TUI Dashboard Updates

### Current Display

```
Segment Latency *
P50 (median):    2128 ms
P95:             7851 ms
P99:             7851 ms
Max:             7851 ms
* Using accurate FFmpeg timestamps
```

### Updated Display

```
Segment Latency *
P25:             1050 ms
P50 (median):    2128 ms
P75:             3200 ms
P95:             7851 ms
P99:             7851 ms
Max:             7851 ms
* Using accurate FFmpeg timestamps
```

### Visual Layout

The percentiles will be displayed in ascending order:
1. **P25** - Lower quartile (25% of requests are faster)
2. **P50 (median)** - Median (50% of requests are faster)
3. **P75** - Upper quartile (75% of requests are faster)
4. **P95** - 95th percentile (95% of requests are faster)
5. **P99** - 99th percentile (99% of requests are faster)
6. **Max** - Maximum observed latency

### Formatting

- **Label**: Same style as existing percentiles (`labelStyle`)
- **Value**: Same formatting as existing (`formatMsFromDuration()`)
- **Order**: Ascending percentile order (P25 → P50 → P75 → P95 → P99 → Max)
- **Spacing**: Consistent with existing rows

### Code Changes Summary

**File**: `internal/tui/view.go`
- **Function**: `renderLatencyStats()` (line ~246)
- **Change**: Add two new `renderLatencyRow()` calls for P25 and P75
- **Order**: Insert P25 before P50, P75 after P50

---

## Verification Steps

### 1. Unit Tests

Run all tests to verify percentile calculations:

```bash
# Test parser percentile calculations
go test ./internal/parser/... -v -run TestDebugEventParser_Percentiles

# Test aggregator percentile aggregation
go test ./internal/stats/... -v -run TestStatsAggregator_AggregatePercentiles

# Test TUI rendering
go test ./internal/tui/... -v -run TestRenderLatencyStats
```

### 2. Integration Test

Run the application with a test stream:

```bash
./bin/go-ffmpeg-hls-swarm -tui -clients 1000 -ramp-rate 1000 http://10.177.0.10:17080/stream.m3u8
```

**Verify**:
- P25 and P75 appear in the "Segment Latency" section
- Percentiles are in ascending order (P25 < P50 < P75 < P95 < P99 < Max)
- Values are reasonable (P25 ≤ P50 ≤ P75 ≤ P95 ≤ P99 ≤ Max)
- Formatting matches existing percentiles

### 3. Visual Inspection

**Check Dashboard**:
- [ ] P25 row appears before P50
- [ ] P75 row appears after P50
- [ ] All percentiles are displayed
- [ ] Values are formatted consistently (ms units)
- [ ] Note about "accurate FFmpeg timestamps" is still present

### 4. Performance Test

Verify no performance regression:

```bash
# Run with race detector
go test ./internal/parser/... -race -count=1

# Run with 1000 clients for 1 minute
./bin/go-ffmpeg-hls-swarm -tui -clients 1000 -ramp-rate 1000 -duration 60s http://10.177.0.10:17080/stream.m3u8
```

**Verify**:
- No race conditions
- No performance degradation
- Memory usage remains stable

### 5. Edge Cases

**Test with**:
- [ ] Single client (verify percentiles are calculated)
- [ ] Zero segments (verify no crash, empty display)
- [ ] Very few segments (1-5, verify percentiles are reasonable)
- [ ] Many segments (1000+, verify percentiles are accurate)

---

## Summary

### Files Modified

1. **`internal/parser/debug_events.go`**
   - Add `SegmentWallTimeP25` and `SegmentWallTimeP75` to `DebugStats` struct (line ~802-804)
   - Calculate P25 and P75 in `Stats()` method (line ~887-894)

2. **`internal/stats/aggregator.go`**
   - Add `SegmentWallTimeP25` and `SegmentWallTimeP75` to `DebugStatsAggregate` struct (line ~97-100)
   - Aggregate P25 and P75 in `Aggregate()` method (line ~586-595)

3. **`internal/orchestrator/client_manager.go`** (if needed)
   - Add P25 and P75 aggregation (line ~586-595)

4. **`internal/tui/view.go`**
   - Update `renderLatencyStats()` to display P25 and P75 (line ~252-257)

### Tests Added

1. **`internal/parser/debug_events_test.go`**
   - `TestDebugEventParser_Percentiles` - Table-driven test for all percentiles
   - `TestDebugEventParser_PercentilesOrdering` - Verify P25 ≤ P50 ≤ P75 ≤ P95 ≤ P99

2. **`internal/stats/aggregator_test.go`**
   - `TestStatsAggregator_AggregatePercentiles` - Table-driven test for aggregation

3. **`internal/tui/view_test.go`**
   - `TestRenderLatencyStats_IncludesP25P75` - Verify TUI rendering
   - `TestRenderLatencyStats_EmptyStats` - Verify edge case handling

### Expected Outcome

After implementation:
- TUI dashboard displays 6 latency metrics: P25, P50, P75, P95, P99, Max
- All percentiles are calculated from accurate FFmpeg timestamps
- Percentiles are aggregated correctly across multiple clients
- Comprehensive test coverage ensures correctness
- No performance regression

---

## References

- T-Digest Library: `github.com/influxdata/tdigest`
- Current Implementation: `internal/parser/debug_events.go`
- TUI Dashboard: `internal/tui/view.go`
- Stats Aggregation: `internal/stats/aggregator.go`
