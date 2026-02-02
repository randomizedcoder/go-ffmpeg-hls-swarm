# Segment Throughput Redesign

> **Status**: Draft - Awaiting Review
> **Date**: 2026-02-02
> **Related**: [Original Design](./SEGMENT_SIZE_TRACKING_DESIGN.md) | [Implementation Log](./SEGMENT_SIZE_TRACKING_IMPLEMENTATION_LOG.md)

## Executive Summary

The segment size tracking feature has been implemented, but the **percentile-based throughput display** is problematic. The TUI's "Segment Throughput" column flashes on and off due to the `Drain()` semantics causing intermittent empty data windows. This document proposes replacing the complex histogram/percentile approach with **simple rolling time-window averages**.

---

## Current State Assessment

### What's Working Well

| Component | Status | Notes |
|-----------|--------|-------|
| Segment Scraper | ✅ Working | Fetches `/files/json/` from origin, caches segment sizes |
| Per-client byte tracking | ✅ Working | `DebugEventParser` tracks bytes on segment complete |
| Total bytes aggregation | ✅ Working | `SegmentBytesDownloaded` aggregates across all clients |
| Prometheus metrics | ✅ Working | `hls_swarm_segment_bytes_downloaded_total` counter works |
| HTTP pattern matching | ✅ Working | Handles both "Opening" and "GET" patterns for keep-alive |

### What's NOT Working Well

#### Issue 1: TUI "Segment Throughput" Column Flashes On/Off

**Symptom**: The "Segment Throughput" column in the TUI alternates between showing data and "(no data)" every few seconds.

**Root Cause**: The current design uses `Drain()` semantics on `ThroughputHistogram`:

1. TUI ticks every 500ms, calling `GetDebugStats()`
2. `GetDebugStats()` drains all per-client histograms via `DrainThroughputHistogram()`
3. Percentiles are calculated from the drained buckets
4. If no segments completed in that 500ms window, all percentiles are 0
5. TUI checks `m.debugStats.SegmentThroughputP50 > 0` to decide whether to show data
6. Result: Column flashes between data and "(no data)"

**Code path**:
```
TUI tick (500ms)
  → GetDebugStats()
    → for each client: DrainThroughputHistogram()  // Destructive read
    → MergeBuckets()
    → PercentileFromBuckets()
  → if P50 > 0: show data, else: "(no data)"
```

**Why caching didn't fully fix it**: A 1-second cache TTL was added to prevent double-drain between TUI and Prometheus, but the fundamental issue remains: if no segments complete during the drain window, percentiles are legitimately 0.

#### Issue 2: Percentile Accuracy with Sparse Data

**Problem**: With histogram bucketing (64 logarithmic buckets from 1 KB/s to 10 GB/s), percentile interpolation is approximate. For low-frequency segment downloads (e.g., 6-second segments = ~0.17 segments/second/client), each aggregation window may have very few samples.

**Example**: With 100 clients and 6s segments:
- Segments/second = 100 / 6 ≈ 16.7
- Samples per 500ms window ≈ 8
- Percentile calculation from 8 samples across 64 buckets is statistically weak

#### Issue 3: Code Complexity

The current implementation involves:
- `ThroughputHistogram` struct with atomic buckets
- `Drain()` / `MergeBuckets()` / `PercentileFromBuckets()` functions
- `cachedDebugStats` with TTL to prevent double-drain
- Float64-as-uint64 atomic storage for max throughput tracking

This complexity exists to support per-segment throughput percentiles, but the value proposition is questionable given the sparse data and flashing UI.

---

## Proposed Redesign: Rolling Time-Window Averages

### Concept

Replace percentile-based throughput with **simple rolling averages** over fixed time windows:

| Window | Description | Expected Behavior |
|--------|-------------|-------------------|
| 1s | Very recent | Jumpy (reflects burst activity) |
| 30s | Short-term | More stable |
| 60s | Medium-term | Stable for steady-state tests |
| 300s | Long-term | Very stable baseline |

**Key insight**: For a long-running load test, the 30s/60s/300s averages should converge to the true throughput, while 1s shows instantaneous variation. This provides more actionable information than percentiles.

### Data Model

```go
// SegmentThroughputTracker tracks rolling average throughput
type SegmentThroughputTracker struct {
    // Cumulative bytes (atomic, never resets)
    totalBytes atomic.Int64

    // Ring buffer of (timestamp, cumulativeBytes) samples
    // Sampled once per second
    samples    []throughputSample
    sampleIdx  int
    sampleMu   sync.Mutex  // Only for ring buffer writes

    // Start time for overall average
    startTime  time.Time
}

type throughputSample struct {
    timestamp time.Time
    bytes     int64  // Cumulative bytes at this timestamp
}

// Computed values (not stored, calculated on demand)
type ThroughputStats struct {
    TotalBytes    int64   // Cumulative total
    Avg1s         float64 // Bytes/sec over last 1 second
    Avg30s        float64 // Bytes/sec over last 30 seconds
    Avg60s        float64 // Bytes/sec over last 60 seconds
    Avg300s       float64 // Bytes/sec over last 300 seconds
    AvgOverall    float64 // Bytes/sec since start
}
```

### Calculation

```go
func (t *SegmentThroughputTracker) GetStats() ThroughputStats {
    now := time.Now()
    currentBytes := t.totalBytes.Load()

    t.sampleMu.Lock()
    defer t.sampleMu.Unlock()

    return ThroughputStats{
        TotalBytes:  currentBytes,
        Avg1s:       t.avgOverWindow(now, 1*time.Second, currentBytes),
        Avg30s:      t.avgOverWindow(now, 30*time.Second, currentBytes),
        Avg60s:      t.avgOverWindow(now, 60*time.Second, currentBytes),
        Avg300s:     t.avgOverWindow(now, 300*time.Second, currentBytes),
        AvgOverall:  float64(currentBytes) / time.Since(t.startTime).Seconds(),
    }
}

func (t *SegmentThroughputTracker) avgOverWindow(now time.Time, window time.Duration, currentBytes int64) float64 {
    // Find sample closest to (now - window)
    targetTime := now.Add(-window)

    // Search ring buffer for sample at or before targetTime
    // Return (currentBytes - sampleBytes) / actualElapsed

    // If window is longer than available history, use oldest sample
}
```

### TUI Display

Replace the percentile columns with simple averages:

**Current (problematic)**:
```
│ Segment Throughput *           │
│ ──────────────────             │
│ P25:          48.2 MB/s        │
│ P50 (median): 52.3 MB/s        │
│ P75:          58.1 MB/s        │  ← Flashes "(no data)"
│ P95:          72.4 MB/s        │
│ P99:          85.6 MB/s        │
│ Max:          98.7 MB/s        │
```

**Proposed (stable)**:
```
│ Segment Throughput             │
│ ──────────────────             │
│ Last 1s:      52.3 MB/s        │  ← May vary
│ Last 30s:     48.7 MB/s        │  ← Stable
│ Last 60s:     49.1 MB/s        │  ← Very stable
│ Last 5m:      48.9 MB/s        │  ← Baseline
│ Overall:      47.2 MB/s        │  ← Since test start
│ Total:        14.2 GB          │
```

### Benefits

1. **No flashing**: Averages are always computable (as long as there's any history)
2. **More intuitive**: "52 MB/s over last 30s" is easier to understand than "P50 throughput"
3. **Simpler code**: No histogram, no drain semantics, no percentile interpolation
4. **Better for load testing**: Rolling averages show steady-state behavior more clearly

---

## Code Changes Required

### Files to Modify

| File | Change |
|------|--------|
| `internal/parser/throughput_histogram.go` | **DELETE** or archive |
| `internal/parser/throughput_histogram_test.go` | **DELETE** or archive |
| `internal/parser/debug_events.go` | Remove `throughputHist`, `DrainThroughputHistogram()`, simplify to just track bytes |
| `internal/orchestrator/client_manager.go` | Remove histogram merging, percentile calculation, caching logic |
| `internal/stats/aggregator.go` | Remove `SegmentThroughputP25/P50/P75/P95/P99/Max` fields |
| `internal/metrics/collector.go` | Remove `SegmentThroughputP*` Prometheus gauges |
| `internal/tui/view.go` | Update `renderLatencyStats()` to show rolling averages |

### New Files to Create

| File | Purpose |
|------|---------|
| `internal/stats/throughput_tracker.go` | New rolling average tracker |
| `internal/stats/throughput_tracker_test.go` | Tests for rolling averages |

### Fields to Add

**In `DebugStatsAggregate`**:
```go
// Replace percentile fields with:
SegmentThroughputAvg1s    float64
SegmentThroughputAvg30s   float64
SegmentThroughputAvg60s   float64
SegmentThroughputAvg300s  float64
SegmentThroughputOverall  float64
SegmentBytesTotal         int64
```

**In `AggregatedStatsUpdate` (for Prometheus)**:
```go
// Replace percentile fields with:
SegmentThroughputAvg1s    float64
SegmentThroughputAvg30s   float64
SegmentThroughputAvg60s   float64
SegmentThroughputAvg300s  float64
```

### Prometheus Metrics to Update

**Remove**:
- `hls_swarm_segment_throughput_p25_bytes_per_second`
- `hls_swarm_segment_throughput_p50_bytes_per_second`
- `hls_swarm_segment_throughput_p75_bytes_per_second`
- `hls_swarm_segment_throughput_p95_bytes_per_second`
- `hls_swarm_segment_throughput_p99_bytes_per_second`
- `hls_swarm_segment_throughput_max_bytes_per_second`

**Add**:
- `hls_swarm_segment_throughput_1s_bytes_per_second` (Gauge)
- `hls_swarm_segment_throughput_30s_bytes_per_second` (Gauge)
- `hls_swarm_segment_throughput_60s_bytes_per_second` (Gauge)
- `hls_swarm_segment_throughput_300s_bytes_per_second` (Gauge)

**Keep**:
- `hls_swarm_segment_bytes_downloaded_total` (Counter) - already working

---

## Implementation Plan

### Phase 1: Create Rolling Average Tracker

1. Create `internal/stats/throughput_tracker.go`
2. Implement ring buffer with 300 samples (5 min at 1 sample/sec)
3. Implement `AddBytes()` for atomic byte tracking
4. Implement `GetStats()` for computing averages
5. Create comprehensive tests

### Phase 2: Update Aggregation

1. Create single `ThroughputTracker` in `ClientManager` (not per-client)
2. Wire byte additions from all parsers to the tracker
3. Update `GetDebugStats()` to return rolling averages instead of percentiles
4. Remove histogram drain/merge logic

### Phase 3: Update TUI

1. Update `renderLatencyStats()` to show rolling averages
2. Always show the column (no "(no data)" flashing)
3. Show "warming up" for windows with insufficient data

### Phase 4: Update Prometheus

1. Remove percentile gauge metrics
2. Add rolling average gauge metrics
3. Update `RecordStats()` to set new metrics

### Phase 5: Cleanup

1. Delete `throughput_histogram.go` and tests
2. Remove unused fields from structs
3. Update design docs to reflect new approach

---

## Testing Strategy

### Test File: `internal/stats/throughput_tracker_test.go`

---

### Table-Driven Unit Tests

#### TestThroughputTracker_AddBytes

```go
func TestThroughputTracker_AddBytes(t *testing.T) {
    tests := []struct {
        name           string
        bytesToAdd     []int64
        expectedTotal  int64
    }{
        {
            name:          "single addition",
            bytesToAdd:    []int64{1000},
            expectedTotal: 1000,
        },
        {
            name:          "multiple additions",
            bytesToAdd:    []int64{1000, 2000, 3000},
            expectedTotal: 6000,
        },
        {
            name:          "zero bytes",
            bytesToAdd:    []int64{0, 0, 0},
            expectedTotal: 0,
        },
        {
            name:          "large values",
            bytesToAdd:    []int64{1 << 30, 1 << 30, 1 << 30}, // 3 GB
            expectedTotal: 3 << 30,
        },
        {
            name:          "mixed sizes (realistic segments)",
            bytesToAdd:    []int64{1281032, 1297764, 1361120, 1338372, 1341944},
            expectedTotal: 6620232,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            tracker := NewThroughputTracker()
            for _, b := range tt.bytesToAdd {
                tracker.AddBytes(b)
            }
            stats := tracker.GetStats()
            if stats.TotalBytes != tt.expectedTotal {
                t.Errorf("TotalBytes = %d, want %d", stats.TotalBytes, tt.expectedTotal)
            }
        })
    }
}
```

#### TestThroughputTracker_RollingAverage

```go
func TestThroughputTracker_RollingAverage(t *testing.T) {
    tests := []struct {
        name           string
        samples        []testSample // {offsetSeconds, cumulativeBytes}
        queryAt        time.Duration // offset from start to query
        window         time.Duration
        expectedAvg    float64
        tolerance      float64 // acceptable error %
    }{
        {
            name: "constant rate 1MB/s over 10s",
            samples: []testSample{
                {0, 0},
                {1, 1 * MB},
                {2, 2 * MB},
                {3, 3 * MB},
                {4, 4 * MB},
                {5, 5 * MB},
            },
            queryAt:     5 * time.Second,
            window:      5 * time.Second,
            expectedAvg: 1 * MB, // 1 MB/s
            tolerance:   0.01,
        },
        {
            name: "burst then idle",
            samples: []testSample{
                {0, 0},
                {1, 10 * MB}, // 10 MB in first second
                {2, 10 * MB}, // idle
                {3, 10 * MB}, // idle
                {4, 10 * MB}, // idle
                {5, 10 * MB}, // idle
            },
            queryAt:     5 * time.Second,
            window:      5 * time.Second,
            expectedAvg: 2 * MB, // 10 MB / 5s = 2 MB/s
            tolerance:   0.01,
        },
        {
            name: "accelerating rate",
            samples: []testSample{
                {0, 0},
                {1, 1 * MB},
                {2, 3 * MB},  // +2 MB
                {3, 6 * MB},  // +3 MB
                {4, 10 * MB}, // +4 MB
                {5, 15 * MB}, // +5 MB
            },
            queryAt:     5 * time.Second,
            window:      5 * time.Second,
            expectedAvg: 3 * MB, // 15 MB / 5s = 3 MB/s
            tolerance:   0.01,
        },
        {
            name: "window shorter than history",
            samples: []testSample{
                {0, 0},
                {1, 1 * MB},
                {2, 2 * MB},
                {3, 3 * MB},
                {4, 4 * MB},
                {5, 10 * MB}, // 6 MB in last second
            },
            queryAt:     5 * time.Second,
            window:      1 * time.Second,
            expectedAvg: 6 * MB, // (10-4) MB / 1s = 6 MB/s
            tolerance:   0.01,
        },
        {
            name: "window longer than history (uses oldest)",
            samples: []testSample{
                {0, 0},
                {1, 5 * MB},
                {2, 10 * MB},
            },
            queryAt:     2 * time.Second,
            window:      60 * time.Second, // Request 60s but only 2s available
            expectedAvg: 5 * MB,           // 10 MB / 2s = 5 MB/s
            tolerance:   0.01,
        },
        {
            name: "30 second window",
            samples: generateConstantRateSamples(30, 2*MB), // 2 MB/s for 30s
            queryAt:     30 * time.Second,
            window:      30 * time.Second,
            expectedAvg: 2 * MB,
            tolerance:   0.05, // Slightly higher tolerance for interpolation
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            tracker := newTestTracker(tt.samples)
            stats := tracker.getStatsAt(tt.queryAt)

            avg := stats.avgForWindow(tt.window)
            diff := math.Abs(avg - tt.expectedAvg) / tt.expectedAvg
            if diff > tt.tolerance {
                t.Errorf("avg over %v = %.2f, want %.2f (±%.0f%%)",
                    tt.window, avg, tt.expectedAvg, tt.tolerance*100)
            }
        })
    }
}
```

#### TestThroughputTracker_WindowEdgeCases

```go
func TestThroughputTracker_WindowEdgeCases(t *testing.T) {
    tests := []struct {
        name        string
        setup       func() *ThroughputTracker
        window      time.Duration
        expectZero  bool
        expectValid bool
    }{
        {
            name: "no samples yet",
            setup: func() *ThroughputTracker {
                return NewThroughputTracker()
            },
            window:      1 * time.Second,
            expectZero:  true,
            expectValid: false,
        },
        {
            name: "single sample (need at least 2 for rate)",
            setup: func() *ThroughputTracker {
                t := NewThroughputTracker()
                t.AddBytes(1000)
                return t
            },
            window:      1 * time.Second,
            expectZero:  false, // Overall should work
            expectValid: true,
        },
        {
            name: "exactly at window boundary",
            setup: func() *ThroughputTracker {
                t := newTestTrackerWithSamples([]testSample{
                    {0, 0},
                    {30, 30 * MB},
                })
                return t
            },
            window:      30 * time.Second,
            expectZero:  false,
            expectValid: true,
        },
        {
            name: "zero elapsed time guard",
            setup: func() *ThroughputTracker {
                t := NewThroughputTracker()
                // Add bytes but don't advance time
                t.totalBytes.Store(1000)
                return t
            },
            window:      1 * time.Second,
            expectZero:  true, // Should return 0, not Inf
            expectValid: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            tracker := tt.setup()
            stats := tracker.GetStats()

            var avg float64
            switch tt.window {
            case 1 * time.Second:
                avg = stats.Avg1s
            case 30 * time.Second:
                avg = stats.Avg30s
            }

            if tt.expectZero && avg != 0 {
                t.Errorf("expected zero, got %v", avg)
            }
            if math.IsInf(avg, 0) || math.IsNaN(avg) {
                t.Error("got Inf or NaN")
            }
        })
    }
}
```

#### TestThroughputTracker_RingBufferOverflow

```go
func TestThroughputTracker_RingBufferOverflow(t *testing.T) {
    tests := []struct {
        name          string
        sampleCount   int    // How many samples to add
        bufferSize    int    // Ring buffer capacity (300 for 5 min)
        queryWindow   time.Duration
        expectCorrect bool
    }{
        {
            name:          "buffer not full",
            sampleCount:   100,
            bufferSize:    300,
            queryWindow:   60 * time.Second,
            expectCorrect: true,
        },
        {
            name:          "buffer exactly full",
            sampleCount:   300,
            bufferSize:    300,
            queryWindow:   300 * time.Second,
            expectCorrect: true,
        },
        {
            name:          "buffer overflow by 1",
            sampleCount:   301,
            bufferSize:    300,
            queryWindow:   300 * time.Second,
            expectCorrect: true,
        },
        {
            name:          "buffer overflow by many (2x)",
            sampleCount:   600,
            bufferSize:    300,
            queryWindow:   300 * time.Second,
            expectCorrect: true,
        },
        {
            name:          "extreme overflow (10x)",
            sampleCount:   3000,
            bufferSize:    300,
            queryWindow:   60 * time.Second,
            expectCorrect: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            tracker := NewThroughputTracker()

            // Add samples at 1 per second with constant 1 MB/s rate
            for i := 0; i < tt.sampleCount; i++ {
                tracker.recordSampleAt(
                    time.Duration(i)*time.Second,
                    int64(i)*MB,
                )
            }

            stats := tracker.GetStats()

            // After overflow, should still return valid (non-Inf, non-NaN) results
            if math.IsInf(stats.Avg30s, 0) || math.IsNaN(stats.Avg30s) {
                t.Error("got Inf or NaN after overflow")
            }

            // Average should be approximately 1 MB/s (constant rate)
            if tt.expectCorrect {
                tolerance := 0.1 // 10%
                if math.Abs(stats.Avg30s-MB)/MB > tolerance {
                    t.Errorf("Avg30s = %.2f, want ~%.2f MB/s", stats.Avg30s/MB, 1.0)
                }
            }
        })
    }
}
```

---

### Race Condition Tests

#### TestThroughputTracker_ConcurrentAddBytes

```go
func TestThroughputTracker_ConcurrentAddBytes(t *testing.T) {
    tracker := NewThroughputTracker()

    var wg sync.WaitGroup
    numGoroutines := 100
    addsPerGoroutine := 1000
    bytesPerAdd := int64(1000)

    for i := 0; i < numGoroutines; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for j := 0; j < addsPerGoroutine; j++ {
                tracker.AddBytes(bytesPerAdd)
            }
        }()
    }

    wg.Wait()

    expected := int64(numGoroutines * addsPerGoroutine * bytesPerAdd)
    stats := tracker.GetStats()
    if stats.TotalBytes != expected {
        t.Errorf("TotalBytes = %d, want %d", stats.TotalBytes, expected)
    }
}
```

#### TestThroughputTracker_ConcurrentAddAndRead

```go
func TestThroughputTracker_ConcurrentAddAndRead(t *testing.T) {
    tracker := NewThroughputTracker()
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()

    var wg sync.WaitGroup

    // Writers: continuously add bytes
    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for {
                select {
                case <-ctx.Done():
                    return
                default:
                    tracker.AddBytes(1000)
                }
            }
        }()
    }

    // Readers: continuously read stats
    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for {
                select {
                case <-ctx.Done():
                    return
                default:
                    stats := tracker.GetStats()
                    // Verify no panics and values are sane
                    if stats.TotalBytes < 0 {
                        t.Error("negative TotalBytes")
                    }
                    if math.IsNaN(stats.Avg1s) || math.IsInf(stats.Avg1s, 0) {
                        // Don't fail - just check we don't panic
                    }
                }
            }
        }()
    }

    wg.Wait()
}
```

#### TestThroughputTracker_ConcurrentSampling

```go
func TestThroughputTracker_ConcurrentSampling(t *testing.T) {
    // Simulates the real scenario: sample ticker + concurrent byte adds + reads
    tracker := NewThroughputTracker()
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()

    var wg sync.WaitGroup

    // Sample ticker (mimics real 1s sampling)
    wg.Add(1)
    go func() {
        defer wg.Done()
        ticker := time.NewTicker(10 * time.Millisecond) // Fast for testing
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done():
                return
            case <-ticker.C:
                tracker.RecordSample()
            }
        }
    }()

    // Byte adders (mimics segment downloads)
    for i := 0; i < 50; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for {
                select {
                case <-ctx.Done():
                    return
                default:
                    tracker.AddBytes(int64(1 + rand.Intn(2*MB)))
                    time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)
                }
            }
        }()
    }

    // Stats readers (mimics TUI + Prometheus)
    for i := 0; i < 5; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for {
                select {
                case <-ctx.Done():
                    return
                default:
                    _ = tracker.GetStats()
                    time.Sleep(50 * time.Millisecond)
                }
            }
        }()
    }

    wg.Wait()

    // Final sanity check
    stats := tracker.GetStats()
    if stats.TotalBytes <= 0 {
        t.Error("expected positive TotalBytes after concurrent operations")
    }
}
```

---

### Benchmark Tests

```go
func BenchmarkThroughputTracker_AddBytes(b *testing.B) {
    tracker := NewThroughputTracker()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        tracker.AddBytes(1000000)
    }
}

func BenchmarkThroughputTracker_AddBytes_Parallel(b *testing.B) {
    tracker := NewThroughputTracker()
    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            tracker.AddBytes(1000000)
        }
    })
}

func BenchmarkThroughputTracker_GetStats(b *testing.B) {
    tracker := NewThroughputTracker()
    // Pre-populate with realistic data
    for i := 0; i < 300; i++ {
        tracker.recordSampleAt(time.Duration(i)*time.Second, int64(i)*MB)
    }
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _ = tracker.GetStats()
    }
}

func BenchmarkThroughputTracker_GetStats_Parallel(b *testing.B) {
    tracker := NewThroughputTracker()
    for i := 0; i < 300; i++ {
        tracker.recordSampleAt(time.Duration(i)*time.Second, int64(i)*MB)
    }
    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            _ = tracker.GetStats()
        }
    })
}

func BenchmarkThroughputTracker_MixedWorkload(b *testing.B) {
    tracker := NewThroughputTracker()
    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        i := 0
        for pb.Next() {
            if i%100 == 0 {
                _ = tracker.GetStats() // 1% reads
            } else {
                tracker.AddBytes(1000000) // 99% writes
            }
            i++
        }
    })
}
```

---

### Fuzz Tests

```go
func FuzzThroughputTracker_AddBytes(f *testing.F) {
    // Seed corpus
    f.Add(int64(0))
    f.Add(int64(1))
    f.Add(int64(1000))
    f.Add(int64(1 << 30))
    f.Add(int64(-1)) // Should be handled gracefully

    tracker := NewThroughputTracker()

    f.Fuzz(func(t *testing.T, bytes int64) {
        // Should not panic
        tracker.AddBytes(bytes)
        stats := tracker.GetStats()

        // Sanity checks
        if math.IsNaN(stats.Avg1s) {
            t.Error("NaN in Avg1s")
        }
        if math.IsInf(stats.Avg1s, 0) {
            t.Error("Inf in Avg1s")
        }
    })
}

func FuzzThroughputTracker_WindowCalculation(f *testing.F) {
    // Seed with various sample patterns
    f.Add(1, int64(1000))
    f.Add(10, int64(1000000))
    f.Add(300, int64(1000000000))

    f.Fuzz(func(t *testing.T, numSamples int, bytesPerSample int64) {
        if numSamples < 0 || numSamples > 1000 {
            return // Bound the input
        }
        if bytesPerSample < 0 {
            return
        }

        tracker := NewThroughputTracker()
        for i := 0; i < numSamples; i++ {
            tracker.AddBytes(bytesPerSample)
            tracker.RecordSample()
        }

        stats := tracker.GetStats()

        // All averages should be non-negative
        if stats.Avg1s < 0 || stats.Avg30s < 0 || stats.Avg60s < 0 || stats.Avg300s < 0 {
            t.Error("negative average")
        }
    })
}
```

---

### Integration-Style Tests

```go
func TestThroughputTracker_RealisticSegmentPattern(t *testing.T) {
    // Simulates 100 clients downloading 6-second segments (~1.3 MB each)
    tracker := NewThroughputTracker()

    segmentSize := int64(1300000) // ~1.3 MB
    segmentDuration := 6 * time.Second
    numClients := 100
    testDuration := 60 * time.Second

    // Simulate segment downloads
    segmentsPerClient := int(testDuration / segmentDuration)
    for i := 0; i < numClients*segmentsPerClient; i++ {
        tracker.AddBytes(segmentSize)
    }

    // Simulate time passing with samples
    for i := 0; i <= int(testDuration.Seconds()); i++ {
        currentBytes := int64(i) * segmentSize * int64(numClients) / int64(segmentDuration.Seconds())
        tracker.recordSampleAt(time.Duration(i)*time.Second, currentBytes)
    }

    stats := tracker.GetStats()

    // Expected throughput: 100 clients * 1.3 MB / 6s ≈ 21.7 MB/s
    expectedThroughput := float64(numClients) * float64(segmentSize) / segmentDuration.Seconds()
    tolerance := 0.2 // 20% tolerance for simulation artifacts

    if math.Abs(stats.Avg60s-expectedThroughput)/expectedThroughput > tolerance {
        t.Errorf("Avg60s = %.2f MB/s, expected ~%.2f MB/s",
            stats.Avg60s/MB, expectedThroughput/MB)
    }
}

func TestThroughputTracker_TUIDoesNotFlash(t *testing.T) {
    // Verify that stats are always available (no "(no data)" scenario)
    tracker := NewThroughputTracker()

    // Add some initial data
    tracker.AddBytes(1000000)
    tracker.RecordSample()

    // Simulate TUI polling every 500ms for 10 seconds
    consecutiveValid := 0
    for i := 0; i < 20; i++ {
        time.Sleep(50 * time.Millisecond) // Accelerated for testing

        stats := tracker.GetStats()

        // Check that we always have data (unlike histogram drain approach)
        if stats.TotalBytes > 0 {
            consecutiveValid++
        }
    }

    // All polls should return valid data
    if consecutiveValid != 20 {
        t.Errorf("only %d/20 polls had valid data (flashing detected)", consecutiveValid)
    }
}
```

---

### Test Helpers

```go
const MB = 1024 * 1024

type testSample struct {
    offsetSeconds int
    bytes         int64
}

func generateConstantRateSamples(durationSec int, bytesPerSec int64) []testSample {
    samples := make([]testSample, durationSec+1)
    for i := 0; i <= durationSec; i++ {
        samples[i] = testSample{i, int64(i) * bytesPerSec}
    }
    return samples
}

func newTestTrackerWithSamples(samples []testSample) *ThroughputTracker {
    t := NewThroughputTracker()
    for _, s := range samples {
        t.recordSampleAt(time.Duration(s.offsetSeconds)*time.Second, s.bytes)
    }
    return t
}
```

---

### Performance Targets

| Operation | Target | Notes |
|-----------|--------|-------|
| `AddBytes()` | < 50ns | Hot path, called per segment |
| `AddBytes()` parallel | < 100ns | With contention |
| `GetStats()` | < 1µs | Called by TUI every 500ms |
| `RecordSample()` | < 500ns | Called once per second |

### Test Commands

```bash
# Run all tests
go test ./internal/stats/... -v

# Run with race detector
go test -race ./internal/stats/...

# Run benchmarks
go test ./internal/stats/... -bench=. -benchmem

# Run fuzz tests (60 seconds)
go test ./internal/stats/... -fuzz=FuzzThroughputTracker -fuzztime=60s

# Coverage
go test ./internal/stats/... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

---

## Migration Notes

### Backward Compatibility

- Prometheus metric names change (percentiles → rolling averages)
- Any dashboards using old metric names need updating
- No config changes required

### Rollback Plan

If issues arise, the histogram-based approach can be restored from git history. The key files are:
- `internal/parser/throughput_histogram.go`
- Relevant sections in `debug_events.go` and `client_manager.go`

---

## Ring Buffer Size Analysis

### Memory Calculation

For our proposed ring buffer (300 samples for 5 minutes at 1 sample/sec):

```go
type throughputSample struct {
    timestamp time.Time  // 24 bytes (on 64-bit)
    bytes     int64      // 8 bytes
}
// Total per sample: 32 bytes

// Ring buffer: 300 samples × 32 bytes = 9,600 bytes (~9.4 KB)
// Plus overhead: slice header (24 bytes), index (8 bytes), mutex (8 bytes)
// Total: ~10 KB per tracker
```

**For the entire swarm (1000 clients):**
- If per-client: 1000 × 10 KB = **10 MB** (probably overkill)
- If single global tracker: **~10 KB** (recommended)

This is negligible memory usage. A custom ring buffer is reasonable.

---

## Library Evaluation

### Option 1: Custom Ring Buffer (Minimal)

**Size:** ~100-150 lines of Go code

**Pros:**
- Zero dependencies
- Exactly what we need, nothing more
- Full control, easy to debug
- Thread-safe with atomic int64 + mutex for samples

**Cons:**
- "Reinventing the wheel"
- Need to write tests ourselves

### Option 2: [asecurityteam/rolling](https://github.com/asecurityteam/rolling)

**What it is:** Rolling window library with built-in aggregations (Count, Avg, Min, Max, Sum, Percentile).

```go
// Time window: 3000 buckets, 1ms per bucket = 3 seconds
tw := rolling.NewTimeWindow(rolling.NewWindow(3000), time.Millisecond)
tw.Append(value)
avg := tw.Reduce(rolling.Avg)
```

**Pros:**
- Well-tested (Atlassian origin)
- Built-in aggregations including percentiles
- Apache 2.0 license
- Last release: April 2025

**Cons:**
- Another dependency
- TimeWindow API is bucket-based, not sample-based
- May not perfectly match our "last N seconds" semantics

### Option 3: [codesuki/go-time-series](https://github.com/codesuki/go-time-series)

**What it is:** Multi-granularity time series (seconds, minutes, hours).

```go
ts, _ := timeseries.NewTimeSeries()
ts.Increase(5)
recent := ts.Recent(30 * time.Second)
```

**Pros:**
- Clean API
- Multi-granularity built-in
- MIT license

**Cons:**
- Designed for counters (Increase), not arbitrary values
- Less active maintenance

### Option 4: golang.org/x/net/internal/timeseries

**What it is:** Official Go package for multi-granularity time series (1 second to 16 weeks).

**Status:** ❌ **Cannot be used** - it's an `internal` package, not exported for external use.

### Option 5: Prometheus TSDB

**What it is:** Full time-series database with disk persistence, WAL, compaction.

**Status:** ❌ **Massive overkill** - designed for storing billions of samples with retention policies. We need ~300 samples in memory.

---

## Recommendation Matrix

| Criteria | Custom | asecurityteam/rolling | go-time-series |
|----------|--------|----------------------|----------------|
| Memory overhead | Minimal | Low | Low |
| Dependencies | None | +1 | +1 |
| Maintenance burden | Medium | Low | Low |
| API fit | Perfect | Good | Fair |
| Thread safety | Our responsibility | Built-in | Unknown |
| Percentile support | No | Yes | No |

### Verdict: Custom Ring Buffer

**Rationale:**
1. Our requirements are simple: store (timestamp, bytes) pairs, compute averages
2. ~100 lines of well-tested code is manageable
3. Zero external dependencies for a core feature
4. Full control over thread safety semantics
5. External libraries add complexity for minimal benefit in this case

If we wanted percentiles, `asecurityteam/rolling` would be worth considering. But since we're moving away from percentiles (that was the original problem), a custom ring buffer is the right choice.

---

## Alternative: DDSketch Evaluation

The [DataDog DDSketch](https://github.com/DataDog/sketches-go) library provides a quantile sketch with relative-error guarantees. This section evaluates whether DDSketch is a better approach than a simple ring buffer.

### What is DDSketch?

DDSketch is a data structure that:
- Provides **relative-error quantile estimates** (e.g., 1% error means P50 of 100 MB/s reports 99-101 MB/s)
- Is **fully mergeable** (combine sketches from distributed systems)
- Has **bounded memory** with collapsing stores
- Is used in production at Datadog for metrics aggregation

```go
// DDSketch usage
sketch, _ := ddsketch.NewDefaultDDSketch(0.01)  // 1% relative accuracy
sketch.Add(52.3 * MB)  // Add throughput sample
p50, _ := sketch.GetValueAtQuantile(0.50)  // Get P50
```

### DDSketch vs Ring Buffer: Comparison

| Aspect | DDSketch | Ring Buffer |
|--------|----------|-------------|
| **What it tracks** | Distribution of values | Time-series of samples |
| **Time windows** | ❌ No built-in support | ✅ Natural fit |
| **Percentiles** | ✅ Excellent (relative error guarantees) | ❌ Requires separate histogram |
| **Rolling averages** | ❌ Not designed for this | ✅ Perfect fit |
| **Memory** | ~2KB for 2048 bins | ~2.4KB for 300 samples |
| **Thread safety** | ❌ NOT thread-safe (needs mutex) | ✅ Can use atomic.Int64 |
| **Mergeability** | ✅ Excellent (distributed systems) | ❌ Not mergeable |
| **Complexity** | Medium (external dependency) | Low (simple slice) |

### Key Insight: Different Problem Domains

**DDSketch answers**: "What is the distribution of throughput values?"
- P50 = 52 MB/s, P95 = 78 MB/s, P99 = 95 MB/s

**Ring Buffer answers**: "What was the average throughput over the last N seconds?"
- Last 1s = 55 MB/s, Last 30s = 52 MB/s, Last 5m = 51 MB/s

These are **different questions**. The TUI flashing issue stems from trying to show **instantaneous percentiles** which are empty when no segments complete in a window. Rolling averages solve this because they always have historical data.

### Option A: Pure Ring Buffer (Recommended)

Use ring buffer for rolling averages only. No percentiles.

**Pros:**
- Simplest implementation
- Always has data (no flashing)
- Most useful for load testing (steady-state verification)

**Cons:**
- No percentile information

### Option B: DDSketch for Percentiles + Ring Buffer for Averages

Use DDSketch for per-segment throughput percentiles, ring buffer for rolling byte totals.

```go
type ThroughputTracker struct {
    // For rolling averages (total bytes over time)
    totalBytes  atomic.Int64
    samples     []throughputSample  // Ring buffer

    // For percentiles (distribution of per-segment rates)
    sketchMu    sync.Mutex
    sketch      *ddsketch.DDSketch
}
```

**Pros:**
- Get both rolling averages AND percentiles
- DDSketch percentiles are accurate (relative error guarantee)
- Useful for identifying outliers (P99 shows worst-case segment downloads)

**Cons:**
- More complex
- DDSketch requires mutex (not lock-free)
- Still need drain/reset semantics for time windows

### Option C: Time-Windowed DDSketches

Maintain multiple DDSketches, one per time window (last 1s, last 30s, etc.).

```go
type ThroughputTracker struct {
    windows map[time.Duration]*windowedSketch
}

type windowedSketch struct {
    mu      sync.Mutex
    sketch  *ddsketch.DDSketch
    samples []timedSample  // To rebuild sketch on window shift
}
```

**Pros:**
- Accurate percentiles within each time window
- Shows how percentiles change over time

**Cons:**
- Most complex
- Significant memory overhead (one sketch per window)
- Overkill for load testing use case

### Recommendation

**Option A (Pure Ring Buffer)** for simplicity and reliability.

**Rationale:**
1. The primary user need is "is throughput stable?" - rolling averages answer this perfectly
2. Percentiles are more useful for latency (where outliers matter for user experience)
3. For throughput, P50 vs P95 is less actionable than "average over last 30s"
4. Simpler code = fewer bugs = less maintenance

**If percentiles are important**, consider **Option B** as a future enhancement after the core rolling average implementation is stable.

### DDSketch Implementation Notes (if used)

If we decide to use DDSketch later, note these important details:

1. **Not thread-safe**: Wrap all operations in a mutex
   ```go
   s.sketchMu.Lock()
   s.sketch.Add(value)
   s.sketchMu.Unlock()
   ```

2. **Time window management**: Clear() periodically or maintain rolling sketches
   ```go
   // Option: Clear every aggregation window
   s.sketchMu.Lock()
   p50, _ := s.sketch.GetValueAtQuantile(0.50)
   s.sketch.Clear()  // Reset for next window
   s.sketchMu.Unlock()
   ```

3. **Relative accuracy**: Use 0.01 (1%) for good balance of accuracy vs memory
   ```go
   sketch, _ := ddsketch.NewDefaultDDSketch(0.01)
   ```

4. **Collapsing store**: For bounded memory with unknown data ranges
   ```go
   sketch, _ := ddsketch.LogCollapsingLowestDenseDDSketch(0.01, 2048)
   ```

---

## Open Questions

1. **Should we keep any percentile metrics?**
   - Recommendation: No for initial implementation
   - Can add DDSketch-based percentiles later if needed

2. **Ring buffer size?**
   - 300 samples (5 min at 1 sample/sec) seems sufficient
   - Could make configurable if needed

3. **Sample interval?**
   - 1 second matches TUI tick rate
   - Could sample more frequently for smoother 1s average

4. **DDSketch as future enhancement?**
   - If users request percentiles, add Option B
   - Keep the ring buffer for averages, add DDSketch for percentiles

---

## Appendix: Current Code to Remove

### `internal/parser/throughput_histogram.go` (entire file)

```go
// ThroughputHistogram is a lock-free histogram for per-client throughput tracking.
// Uses atomic counters for O(1) recording with no locks.
// ...
type ThroughputHistogram struct {
    buckets [64]atomic.Uint64
    count   atomic.Uint64
    sum     atomic.Uint64
}
// ... all methods
```

### `internal/parser/debug_events.go` (sections to remove)

```go
// Remove:
throughputHist    *ThroughputHistogram
maxThroughput     atomic.Uint64  // stored as float64 bits

// Remove methods:
func (p *DebugEventParser) recordThroughput(...)
func (p *DebugEventParser) loadMaxThroughput() float64
func (p *DebugEventParser) DrainThroughputHistogram() [64]uint64
```

### `internal/orchestrator/client_manager.go` (sections to remove)

```go
// Remove:
type cachedDebugStatsEntry struct { ... }
cachedDebugStats atomic.Value
debugStatsCacheTTL time.Duration

// Remove from GetDebugStats():
// - Histogram draining loop
// - MergeBuckets() call
// - PercentileFromBuckets() calls
// - Cache logic
```

### `internal/stats/aggregator.go` (fields to remove)

```go
// Remove from DebugStatsAggregate:
SegmentThroughputP25 float64
SegmentThroughputP50 float64
SegmentThroughputP75 float64
SegmentThroughputP95 float64
SegmentThroughputP99 float64
SegmentThroughputMax float64
SegmentThroughputBuckets [64]uint64
```

---

## Decision Required

Please review this proposal and confirm:

1. ✅ Proceed with rolling average approach?
2. ✅ Window sizes (1s, 30s, 60s, 300s) acceptable?
3. ✅ OK to remove percentile Prometheus metrics?
4. ✅ Any additional requirements for the TUI display?
