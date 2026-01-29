# Origin Metrics Rolling Window Design

> **Type**: Design Document
> **Status**: PROPOSAL
> **Related**: [ORIGIN_METRICS_IMPLEMENTATION_PLAN.md](ORIGIN_METRICS_IMPLEMENTATION_PLAN.md), [ORIGIN_METRICS_IMPLEMENTATION_LOG.md](ORIGIN_METRICS_IMPLEMENTATION_LOG.md)

This document discusses the design for adding rolling window percentiles to origin server network metrics to better handle bursty HLS traffic patterns.

---

## Table of Contents

1. [Problem Statement](#problem-statement)
2. [Proposed Solution](#proposed-solution)
3. [Design Requirements](#design-requirements)
4. [Implementation Options](#implementation-options)
5. [Recommended Approach](#recommended-approach)
6. [Data Structure Design](#data-structure-design)
7. [Algorithm Design](#algorithm-design)
8. [Performance Considerations](#performance-considerations)
9. [Memory Considerations](#memory-considerations)
10. [Thread Safety](#thread-safety)
11. [TUI Display Design](#tui-display-design)
12. [Testing Strategy](#testing-strategy)
13. [Alternative Approaches](#alternative-approaches)
14. [Future Enhancements](#future-enhancements)

---

## Problem Statement

### Current Behavior

The origin server network metrics (`NetInRate` and `NetOutRate`) display instantaneous values calculated from the difference between consecutive Prometheus scrapes (default: 2-second interval). This works well for steady traffic, but HLS traffic is inherently bursty:

1. **Manifest Refresh**: Every 2-6 seconds, all clients refresh the manifest file
2. **Segment Availability**: When a new segment becomes available, all clients (potentially 1000+) attempt to download it simultaneously
3. **Burst Pattern**: Traffic spikes to very high values (e.g., 500 MB/s) for a brief moment, then drops to near-zero

### User Experience Problem

The instantaneous metrics jump dramatically:
- **High spikes**: 500 MB/s when all clients download a segment
- **Low valleys**: <1 MB/s between segments
- **Hard to interpret**: Operators can't tell if the origin is handling 50 MB/s average or 500 MB/s average

### Example Timeline

```
Time    | Instantaneous Rate | What's Happening
--------|-------------------|------------------
00:00.0 | 2.3 MB/s          | Idle (manifest refreshes only)
00:00.2 | 487.2 MB/s        | Segment available - all clients downloading
00:00.4 | 523.1 MB/s        | Still downloading
00:00.6 | 12.4 MB/s         | Most clients finished
00:02.0 | 1.8 MB/s          | Back to idle
00:02.2 | 512.7 MB/s        | Next segment available - burst again
```

**Question**: Is the origin handling 500 MB/s average or 50 MB/s average? The instantaneous values don't answer this.

---

## Proposed Solution

### Rolling Window with Percentiles

Add a rolling window that tracks network rates over a configurable time window (default: 30 seconds) and reports:

1. **Instantaneous Rate** (current): Keep existing behavior
2. **P50 (Median)** over rolling window: Typical/representative rate
3. **Max** over rolling window: Peak burst rate

### Display Format

```
Net In:  45.2 MB/s (instant) | P50: 23.1 MB/s | Max: 487.3 MB/s (30s)
Net Out: 890.1 MB/s (instant) | P50: 445.2 MB/s | Max: 523.4 MB/s (30s)
```

**Window Duration**: Configurable via `-origin-metrics-window` flag
- Default: 30 seconds
- Minimum: 10 seconds (must be at least 2× scrape interval)
- Maximum: 300 seconds (5 minutes)
- Example: `-origin-metrics-window 60s` for 60-second window

This gives operators:
- **Instantaneous**: Current activity (is a burst happening now?)
- **P50**: Typical throughput (what's the normal load?)
- **Max**: Peak capacity needed (did we hit limits?)

### Benefits

1. **Better Interpretation**: P50 shows typical load, not just spikes
2. **Capacity Planning**: Max shows peak requirements
3. **Trend Visibility**: Can see if bursts are getting larger over time
4. **Minimal Overhead**: Only 15 float64 values per metric (120 bytes total)

---

## Design Requirements

### Functional Requirements

1. **Rolling Window**: Track last N seconds of data (default: 30 seconds, configurable)
2. **Percentile Calculation**: Compute P50 (median) and max efficiently
3. **Automatic Expiration**: Old samples (outside window) automatically removed
4. **Backward Compatible**: Keep existing instantaneous metrics
5. **Configurable Window**: Allow window size to be configured via CLI flag
   - Default: 30 seconds
   - Maximum: 300 seconds (5 minutes)
   - Minimum: 10 seconds (must be >= 2× scrape interval)

### Non-Functional Requirements

1. **Lock-Free Reads**: Metric reads must use atomic operations (no mutexes)
2. **Low Memory**: Minimal memory footprint (<1KB per metric)
3. **Fast Updates**: Window update must complete in <100µs
4. **Fast Queries**: Percentile calculation must complete in <10µs
5. **Thread-Safe**: Support concurrent reads and writes

### Performance Targets

- **Update Time**: <100µs per metric (2 metrics = <200µs total)
- **Query Time**: <10µs for P50 and max calculation
- **Memory**: <1KB per metric (2 metrics = <2KB total)
- **CPU**: <0.1% overhead during scraping

---

## Implementation Options

### Option 1: Ring Buffer with Atomic Index

**Concept**: Fixed-size circular buffer with atomic write index.

```go
type RollingWindow struct {
    samples [15]float64      // Fixed-size array
    times   [15]time.Time     // Timestamps for expiration
    writeIdx atomic.Int64     // Atomic write position (0-14)
    count   atomic.Int64      // Number of valid samples
}

func (rw *RollingWindow) Add(value float64, t time.Time) {
    idx := int(rw.writeIdx.Add(1) % 15)
    rw.samples[idx] = value
    rw.times[idx] = t
    rw.count.Store(min(rw.count.Load()+1, 15))
}

func (rw *RollingWindow) GetPercentiles(window time.Duration) (p50, max float64) {
    // Read all samples atomically (copy to local slice)
    // Filter by time window
    // Calculate percentiles
}
```

**Pros**:
- ✅ Lock-free writes (atomic index)
- ✅ Fixed memory (no allocations)
- ✅ Simple implementation
- ✅ Fast updates (O(1))

**Cons**:
- ⚠️ Reads require copying (but only 15 values)
- ⚠️ Percentile calculation requires sorting (but only 15 values)

**Memory**: 15 × (8 bytes float64 + 24 bytes time.Time) = 480 bytes per metric

### Option 2: Atomic Pointer Swap (Copy-on-Write)

**Concept**: Store entire window in struct, atomically swap pointer.

```go
type WindowSnapshot struct {
    samples []float64
    times   []time.Time
    valid   int  // Number of valid samples
}

type RollingWindow struct {
    current atomic.Value // *WindowSnapshot
}

func (rw *RollingWindow) Add(value float64, t time.Time) {
    old := rw.current.Load().(*WindowSnapshot)
    new := &WindowSnapshot{
        samples: make([]float64, len(old.samples)+1),
        times:   make([]time.Time, len(old.times)+1),
    }
    // Copy old samples, add new, trim to 15
    rw.current.Store(new)
}
```

**Pros**:
- ✅ Lock-free reads (atomic.Value)
- ✅ Simple read path (just Load())
- ✅ No copying needed for reads

**Cons**:
- ❌ Allocations on every update (GC pressure)
- ❌ More complex update logic
- ❌ Slower updates (O(n) copy)

**Memory**: Variable (allocations per update)

### Option 3: T-Digest with Time-Based Expiration (Recommended ✅)

**Concept**: Use T-Digest for percentile calculation with time-based sample expiration.

```go
import "github.com/influxdata/tdigest"

type RollingWindow struct {
    digest    *tdigest.TDigest
    mu        sync.Mutex  // T-Digest is not thread-safe
    window    time.Duration  // 30 seconds
    samples   []sample       // For time-based expiration
    lastClean time.Time      // Last cleanup time
}

type sample struct {
    value float64
    time  time.Time
}
```

**Pros**:
- ✅ Accurate percentiles (T-Digest algorithm)
- ✅ Memory efficient (~10KB per digest, constant)
- ✅ Already used in project (`debug_events.go` pattern)
- ✅ Less code (reuse existing patterns)
- ✅ Consistent with other percentile calculations
- ✅ Can calculate any percentile (P50, P95, P99, etc.)

**Cons**:
- ⚠️ Requires mutex (but low contention - updates every 2s)
- ⚠️ Need time-based expiration logic

**Memory**: ~10KB per digest (compressed, constant regardless of sample count)

**Pattern Match**: Identical to `segmentWallTimeDigest` in `debug_events.go`

### Option 4: Separate Ring Buffers with Atomic Indexes

**Concept**: Two separate ring buffers (one for in, one for out) with atomic indexes.

```go
type NetworkRollingWindow struct {
    inSamples  [15]float64
    inTimes    [15]time.Time
    inWriteIdx atomic.Int64
    inCount    atomic.Int64

    outSamples [15]float64
    outTimes   [15]time.Time
    outWriteIdx atomic.Int64
    outCount    atomic.Int64
}
```

**Pros**:
- ✅ Lock-free (atomic indexes)
- ✅ Fixed memory
- ✅ Simple
- ✅ Independent tracking per metric

**Cons**:
- ⚠️ Code duplication (can be abstracted)

**Memory**: 480 bytes × 2 = 960 bytes total

---

## Recommended Approach

### Option 3: T-Digest with Time-Based Expiration (Recommended ✅)

**Rationale**:
1. **Consistency**: Matches existing percentile implementation in `debug_events.go`
   - Same library: `github.com/influxdata/tdigest`
   - Same pattern: `digest.Add()` and `digest.Quantile()`
   - Same mutex protection pattern
2. **Less Code**: Reuse existing T-Digest patterns and infrastructure
   - No new data structures to design
   - Follow established patterns from `debug_events.go`
   - Estimated ~80 lines vs ~120 lines for ring buffer
3. **Accurate Percentiles**: T-Digest provides accurate percentile calculations
   - Within 1% accuracy for P50-P99
   - Battle-tested algorithm (used by InfluxDB, Prometheus)
4. **Memory Efficient**: Constant memory (~10KB) regardless of sample count
   - Compressed representation
   - Doesn't grow with number of samples
5. **Proven Pattern**: Already used for segment/manifest latency percentiles
   - Developers already familiar with the pattern
   - Consistent codebase style
6. **Mutex Acceptable**: Low contention (only during scraping, every 2 seconds)
   - Updates happen every 2 seconds (not high-frequency)
   - Lock duration: ~210ns (normal) or ~5.2µs (with cleanup)
   - Contention: Negligible

**Trade-off**: Uses mutex instead of pure atomics, but:
- ✅ Mutex contention is minimal (updates every 2 seconds, not per-event)
- ✅ Consistent with existing codebase patterns (`debug_events.go` uses mutex for T-Digest)
- ✅ Simpler implementation (less code to maintain)
- ✅ Better code reuse (same library, same patterns)

**Code Comparison**:
- **T-Digest**: ~80 lines (including cleanup, following existing pattern)
- **Ring Buffer**: ~120 lines (including sorting, index management, edge cases)
- **Savings**: ~40 lines of code, plus consistency benefits

### Implementation Strategy

1. **T-Digest with Time Tracking**: Use T-Digest for percentiles, track timestamps for expiration
2. **Two Instances**: One for `NetInRate`, one for `NetOutRate`
3. **Time-Based Expiration**: Periodically remove samples older than window
4. **Mutex Protection**: Protect T-Digest operations (low contention - updates every 2s)
5. **Follow Existing Pattern**: Match `segmentWallTimeDigest` implementation in `debug_events.go`

---

## Data Structure Design

### Core Types

```go
import (
    "sync"
    "time"
    "github.com/influxdata/tdigest"
)

// RollingWindow tracks a rolling window of float64 values using T-Digest.
// Provides accurate percentile calculation with time-based expiration.
// Thread-safe: uses mutex to protect T-Digest operations.
type RollingWindow struct {
    digest    *tdigest.TDigest
    mu        sync.Mutex  // T-Digest is not thread-safe
    window    time.Duration  // 30 seconds
    samples   []sample       // Track timestamps for expiration
    lastClean time.Time      // Last cleanup time
}

type sample struct {
    value float64
    time  time.Time
}

// NewRollingWindow creates a new rolling window.
// Follows the same pattern as segmentWallTimeDigest in debug_events.go.
func NewRollingWindow(window time.Duration) *RollingWindow {
    return &RollingWindow{
        digest:    tdigest.NewWithCompression(100), // ~100 centroids, ~10KB
        window:    window,
        samples:   make([]sample, 0, 20), // Pre-allocate for ~15 samples
        lastClean: time.Now(),
    }
}

// Add adds a new sample to the window.
// Thread-safe: uses mutex to protect T-Digest.
func (rw *RollingWindow) Add(value float64, t time.Time) {
    rw.mu.Lock()
    defer rw.mu.Unlock()

    // Add to T-Digest
    rw.digest.Add(value, 1)

    // Track timestamp for expiration
    rw.samples = append(rw.samples, sample{value: value, time: t})

    // Periodic cleanup (every 10 seconds or when samples > 20)
    if len(rw.samples) > 20 || time.Since(rw.lastClean) > 10*time.Second {
        rw.cleanup(t)
    }
}

// cleanup removes samples older than the window and rebuilds T-Digest.
// Must be called with mutex held.
func (rw *RollingWindow) cleanup(now time.Time) {
    cutoff := now.Add(-rw.window)

    // Filter valid samples
    validSamples := make([]sample, 0, len(rw.samples))
    for _, s := range rw.samples {
        if s.time.After(cutoff) {
            validSamples = append(validSamples, s)
        }
    }

    // Rebuild T-Digest with only valid samples
    rw.digest = tdigest.NewWithCompression(100)
    for _, s := range validSamples {
        rw.digest.Add(s.value, 1)
    }

    rw.samples = validSamples
    rw.lastClean = now
}

// GetPercentiles returns P50 (median) and max for samples within the time window.
// Thread-safe: uses mutex to protect T-Digest.
func (rw *RollingWindow) GetPercentiles(now time.Time) (p50, max float64, valid bool) {
    rw.mu.Lock()
    defer rw.mu.Unlock()

    // Cleanup before query
    rw.cleanup(now)

    if len(rw.samples) == 0 {
        return 0, 0, false
    }

    // Calculate P50 from T-Digest
    p50 = rw.digest.Quantile(0.50)

    // Calculate max from samples (T-Digest doesn't track max directly)
    max = rw.samples[0].value
    for _, s := range rw.samples {
        if s.value > max {
            max = s.value
        }
    }

    return p50, max, true
}
```

### Integration with OriginScraper

```go
import "github.com/influxdata/tdigest"

type OriginScraper struct {
    // ... existing fields ...

    // Rolling windows for network metrics (using T-Digest)
    netInDigest  *tdigest.TDigest
    netInSamples []networkSample
    netInMu      sync.Mutex

    netOutDigest  *tdigest.TDigest
    netOutSamples []networkSample
    netOutMu      sync.Mutex

    windowSize time.Duration  // 30 seconds
    lastClean  time.Time
}

type networkSample struct {
    value float64
    time  time.Time
}

func NewOriginScraper(nodeExporterURL, nginxExporterURL string, interval, windowSize time.Duration, logger *slog.Logger) *OriginScraper {
    if nodeExporterURL == "" && nginxExporterURL == "" {
        return nil // Feature disabled
    }

    // Window size is validated in config.Validate(), but we clamp here for safety
    if windowSize < 10*time.Second {
        windowSize = 10 * time.Second // Minimum: 10 seconds
    }
    if windowSize > 300*time.Second {
        windowSize = 300 * time.Second // Maximum: 300 seconds (5 minutes)
    }

    scraper := &OriginScraper{
        // ... existing initialization ...
        nodeExporterURL:  nodeExporterURL,
        nginxExporterURL: nginxExporterURL,
        interval:         interval,
        logger:           logger,
        netInDigest:      tdigest.NewWithCompression(100),
        netOutDigest:     tdigest.NewWithCompression(100),
        windowSize:       windowSize, // Configurable (default: 30s, max: 300s)
        lastClean:        time.Now(),
    }
    // ...
}

func (s *OriginScraper) extractNetwork(metrics map[string]*dto.MetricFamily) (inRate, outRate float64) {
    // ... existing rate calculation ...

    // Add to rolling windows (following debug_events.go pattern)
    now := time.Now()

    // Net In
    s.netInMu.Lock()
    s.netInDigest.Add(inRate, 1)
    s.netInSamples = append(s.netInSamples, networkSample{value: inRate, time: now})
    if len(s.netInSamples) > 20 || time.Since(s.lastClean) > 10*time.Second {
        s.cleanupNetworkWindow(&s.netInSamples, s.netInDigest, now)
    }
    s.netInMu.Unlock()

    // Net Out
    s.netOutMu.Lock()
    s.netOutDigest.Add(outRate, 1)
    s.netOutSamples = append(s.netOutSamples, networkSample{value: outRate, time: now})
    if len(s.netOutSamples) > 20 || time.Since(s.lastClean) > 10*time.Second {
        s.cleanupNetworkWindow(&s.netOutSamples, s.netOutDigest, now)
    }
    s.netOutMu.Unlock()

    s.lastClean = now

    return inRate, outRate
}

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

### Updated OriginMetrics Type

```go
type OriginMetrics struct {
    // ... existing fields ...

    // Instantaneous rates (existing)
    NetInRate  float64 // bytes/sec
    NetOutRate float64 // bytes/sec

    // Rolling window percentiles (new, from T-Digest)
    NetInP50  float64 // P50 (median) over rolling window (configurable, default: 30s)
    NetInMax  float64 // Max over last 30s
    NetOutP50 float64 // P50 (median) over last 30s
    NetOutMax float64 // Max over last 30s
}
```

### Updated GetMetrics() Method

```go
func (s *OriginScraper) GetMetrics() *OriginMetrics {
    if s == nil {
        return nil
    }

    ptr := s.metrics.Load()
    if ptr == nil {
        return nil
    }

    m := ptr.(*OriginMetrics)
    now := time.Now()

    // Calculate percentiles from T-Digest (following debug_events.go pattern)
    s.netInMu.Lock()
    s.cleanupNetworkWindow(&s.netInSamples, s.netInDigest, now)
    if len(s.netInSamples) > 0 {
        m.NetInP50 = s.netInDigest.Quantile(0.50)
        // Calculate max from samples
        max := s.netInSamples[0].value
        for _, s := range s.netInSamples {
            if s.value > max {
                max = s.value
            }
        }
        m.NetInMax = max
    }
    s.netInMu.Unlock()

    s.netOutMu.Lock()
    s.cleanupNetworkWindow(&s.netOutSamples, s.netOutDigest, now)
    if len(s.netOutSamples) > 0 {
        m.NetOutP50 = s.netOutDigest.Quantile(0.50)
        // Calculate max from samples
        max := s.netOutSamples[0].value
        for _, s := range s.netOutSamples {
            if s.value > max {
                max = s.value
            }
        }
        m.NetOutMax = max
    }
    s.netOutMu.Unlock()

    // Return copy (existing logic)
    return &OriginMetrics{
        // ... copy all fields including new percentiles ...
    }
}
```

---

## Algorithm Design

### Update Algorithm

```
1. Calculate instantaneous rate (existing logic)
2. Get current time
3. Lock mutex
4. Add value to T-Digest (digest.Add(value, 1))
5. Append sample with timestamp to samples slice
6. If samples > 20 OR time since last cleanup > 10s:
   a. Filter samples by timestamp (remove old)
   b. Rebuild T-Digest with valid samples only
7. Unlock mutex
```

**Time Complexity**: O(n) where n = number of samples (typically O(1) with periodic cleanup)
**Space Complexity**: O(n) where n ≤ 15 samples (grows to ~15, then stable)

### Query Algorithm

```
1. Lock mutex
2. Cleanup samples (remove old, rebuild T-Digest)
3. If no samples, return invalid
4. Calculate P50 from T-Digest (digest.Quantile(0.50))
5. Calculate max from samples slice (linear scan)
6. Unlock mutex
7. Return results
```

**Time Complexity**: O(n) where n ≤ 15 (cleanup + max calculation)
**Space Complexity**: O(1) (no additional allocations)

### Edge Cases

1. **Window Not Full**: Handle gracefully (use available samples)
2. **Stale Samples**: Filter by timestamp (automatic expiration)
3. **Concurrent Updates**: Atomic index ensures no corruption
4. **Concurrent Reads**: Copy to local slice (no race conditions)

---

## Performance Considerations

### Update Performance

**Target**: <100µs per metric update

**Analysis**:
- Mutex lock: ~50ns (low contention)
- T-Digest Add: ~100ns
- Slice append: ~10ns
- Cleanup (periodic): ~5µs (only every 10s or when >20 samples)
- Mutex unlock: ~50ns
- **Total (normal)**: ~210ns (well under target)
- **Total (with cleanup)**: ~5.2µs (still well under target)

### Query Performance

**Target**: <10µs for percentile calculation

**Analysis**:
- Mutex lock: ~50ns
- Cleanup (if needed): ~5µs (only if samples expired)
- T-Digest Quantile: ~200ns
- Max calculation (linear scan): ~150ns (15 samples)
- Mutex unlock: ~50ns
- **Total (normal)**: ~450ns (well under target)
- **Total (with cleanup)**: ~5.5µs (still well under target)

### Memory Performance

**Target**: <1KB per metric

**Analysis**:
- T-Digest: ~10KB (compressed, constant regardless of sample count)
- Samples slice: ~15 × 16 bytes (sample struct) = 240 bytes
- Mutex: 8 bytes
- **Total**: ~10.25KB per metric

**Note**: T-Digest uses more memory than ring buffer, but:
- Constant memory (doesn't grow with sample count)
- More accurate percentiles
- Consistent with existing codebase patterns
- Still acceptable for 2 metrics (~20KB total)

### CPU Overhead

**Target**: <0.1% CPU during scraping

**Analysis**:
- Scraping happens every 2 seconds
- Update takes ~40ns
- Query takes ~1.2µs (only when TUI reads)
- **Total**: Negligible (<0.001% CPU)

---

## Memory Considerations

### Fixed Memory Footprint

- **Per Metric**: 496 bytes
- **Total (2 metrics)**: 992 bytes
- **With Overhead**: <2KB total

### No Allocations

- **Updates**: Zero allocations (fixed arrays)
- **Queries**: One allocation per query (local slice, ~120 bytes)
  - Can be optimized with sync.Pool if needed
  - But 120 bytes is trivial for GC

### GC Pressure

- **Minimal**: Only cleanup-time allocations (rebuilding T-Digest and slice)
- **Frequency**: Only when samples > 20 or every 10 seconds
- **Size**: ~10KB per cleanup (T-Digest rebuild), ~240 bytes (slice)
- **Impact**: Negligible (cleanup happens infrequently)

---

## Thread Safety

### Write Path (Scraper Goroutine)

```go
// Single writer (scraper goroutine)
func (s *OriginScraper) extractNetwork(...) {
    // ... calculate rates ...

    s.netInMu.Lock()
    s.netInDigest.Add(inRate, 1)  // T-Digest operation
    s.netInSamples = append(s.netInSamples, ...)  // Slice append
    // ... cleanup if needed ...
    s.netInMu.Unlock()
}
```

**Safety**: ✅ Mutex protects T-Digest and slice operations

### Read Path (TUI Goroutine)

```go
// Multiple readers (TUI, metrics server, etc.)
func (s *OriginScraper) GetMetrics() *OriginMetrics {
    // ... existing code ...

    s.netInMu.Lock()
    s.cleanupNetworkWindow(...)  // Cleanup before query
    p50 := s.netInDigest.Quantile(0.50)  // T-Digest query
    max := calculateMax(s.netInSamples)   // Slice read
    s.netInMu.Unlock()

    // ... return copy ...
}
```

**Safety**: ✅ Mutex protects all T-Digest and slice operations

### Race Condition Analysis

| Operation | Writer | Reader | Safe? |
|-----------|--------|--------|-------|
| T-Digest Add | Mutex | Mutex | ✅ Yes |
| T-Digest Quantile | Mutex | Mutex | ✅ Yes |
| Slice append | Mutex | Mutex | ✅ Yes |
| Slice read | Mutex | Mutex | ✅ Yes |

**Conclusion**: ✅ Thread-safe with mutex protection (low contention - updates every 2s)

### Mutex Contention Analysis

**Update Frequency**: Every 2 seconds (scraping interval)
**Lock Duration**: ~210ns (normal) or ~5.2µs (with cleanup)
**Contention**: Very low (single writer, readers only during TUI updates)

**Impact**: Negligible - mutex is held for microseconds, updates happen every 2 seconds

---

## TUI Display Design

### Current Display

```
Net In:  45.2 MB/s
Net Out: 890.1 MB/s
```

### Proposed Display

```
Net In:  45.2 MB/s (instant) | P50: 23.1 MB/s | Max: 487.3 MB/s (30s)
Net Out: 890.1 MB/s (instant) | P50: 445.2 MB/s | Max: 523.4 MB/s (30s)
```

### Alternative (More Compact)

```
Net In:  45.2 MB/s | P50: 23.1 | Max: 487.3 (30s)
Net Out: 890.1 MB/s | P50: 445.2 | Max: 523.4 (30s)
```

### Implementation

```go
func renderOriginMetricRow(label, value, extra string) string {
    // ... existing code ...
}

// Updated call site:
renderOriginMetricRow("Net In:",
    fmt.Sprintf("%s (instant)", formatBytesRaw(int64(metrics.NetInRate))),
    fmt.Sprintf("| P50: %s | Max: %s (30s)",
        formatBytesRaw(int64(metrics.NetInP50)),
        formatBytesRaw(int64(metrics.NetInMax))))
```

---

## Testing Strategy

### Unit Tests

```go
func TestRollingWindow_Basic(t *testing.T) {
    rw := NewRollingWindow(30 * time.Second)

    // Add samples
    now := time.Now()
    for i := 0; i < 10; i++ {
        rw.Add(float64(i*10), now.Add(time.Duration(i)*2*time.Second))
    }

    // Get percentiles
    p50, max, valid := rw.GetPercentiles(now.Add(20*time.Second))
    assert.True(t, valid)
    // P50 should be around median (T-Digest approximation)
    assert.Greater(t, p50, 30.0)
    assert.Less(t, p50, 70.0)
    assert.Equal(t, 90.0, max)
}

func TestRollingWindow_WindowExpiration(t *testing.T) {
    rw := NewRollingWindow(30 * time.Second)

    // Add old sample
    oldTime := time.Now().Add(-40 * time.Second)
    rw.Add(100.0, oldTime)

    // Add recent sample
    recentTime := time.Now()
    rw.Add(50.0, recentTime)

    // Get percentiles (should only include recent after cleanup)
    p50, max, valid := rw.GetPercentiles(time.Now())
    assert.True(t, valid)
    assert.Equal(t, 50.0, p50)  // Only recent sample
    assert.Equal(t, 50.0, max)   // Old sample filtered out
}

func TestRollingWindow_Cleanup(t *testing.T) {
    rw := NewRollingWindow(30 * time.Second)

    // Add many samples to trigger cleanup
    now := time.Now()
    for i := 0; i < 25; i++ {
        rw.Add(float64(i), now.Add(time.Duration(i)*time.Second))
    }

    // Verify cleanup happened (samples should be <= 20)
    rw.mu.Lock()
    assert.LessOrEqual(t, len(rw.samples), 20)
    rw.mu.Unlock()
}

func TestRollingWindow_ConcurrentAccess(t *testing.T) {
    rw := NewRollingWindow(30 * time.Second)

    // Concurrent writes (mutex will serialize)
    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func(val float64) {
            defer wg.Done()
            rw.Add(val, time.Now())
        }(float64(i))
    }

    // Concurrent reads (mutex will serialize)
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            _, _, _ = rw.GetPercentiles(time.Now())
        }()
    }

    wg.Wait()
    // Should not panic or corrupt data
}
```

### Integration Tests

```go
func TestOriginScraper_RollingWindow(t *testing.T) {
    // Setup mock server
    nodeServer := mockPrometheusServer(t, sampleNodeExporterMetrics())
    defer nodeServer.Close()

    scraper := NewOriginScraper(
        nodeServer.URL+"/metrics",
        "",
        2*time.Second,
        nil,
    )

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go scraper.Run(ctx)

    // Wait for multiple scrapes (to fill window)
    time.Sleep(35 * time.Second)

    metrics := scraper.GetMetrics()
    assert.NotNil(t, metrics)

    // Verify percentiles are calculated
    if metrics.NetInP50 > 0 {
        assert.Greater(t, metrics.NetInMax, metrics.NetInP50)
    }
}
```

---

## Code Comparison

### T-Digest Approach (Recommended)

**Lines of Code**: ~80 lines (including cleanup logic)
**Dependencies**: Existing (`github.com/influxdata/tdigest`)
**Pattern Match**: Identical to `debug_events.go`

### Ring Buffer Approach (Alternative)

**Lines of Code**: ~120 lines (including sorting, index management)
**Dependencies**: None (pure Go)
**Pattern Match**: Similar to `segmentSizes` in `client_stats.go`

**Conclusion**: T-Digest is **less code** and **more consistent** with existing patterns.

---

## Alternative Approaches

### Alternative 1: Exponential Moving Average (EMA)

**Concept**: Use EMA instead of rolling window.

```go
type EMAMetric struct {
    alpha    float64  // Smoothing factor
    current  float64
    mu       sync.Mutex
}

func (e *EMAMetric) Update(value float64) {
    e.mu.Lock()
    e.current = e.alpha*value + (1-e.alpha)*e.current
    e.mu.Unlock()
}
```

**Pros**: Simple, O(1) updates, fixed memory
**Cons**: Requires mutex, doesn't provide max, less intuitive

### Alternative 2: Histogram Buckets

**Concept**: Use histogram with fixed buckets.

```go
type HistogramMetric struct {
    buckets [10]int64  // Fixed buckets
    mu      sync.Mutex
}
```

**Pros**: Can calculate any percentile
**Cons**: Requires mutex, less accurate, more complex

### Alternative 3: Keep Only Max (No P50)

**Concept**: Track only max value over window.

**Pros**: Simpler, less memory
**Cons**: Loses median information (less useful)

---

## Future Enhancements

### Potential Improvements

1. **Configurable Window Size**: Allow CLI flag to set window (default 30s)
2. **More Percentiles**: Add P95, P99 for better analysis
3. **Trend Indicators**: Show if rates are increasing/decreasing
4. **Multiple Windows**: Track 1min, 5min, 15min windows simultaneously
5. **Alerting**: Alert when max exceeds threshold
6. **Graph Visualization**: Show rolling window as mini graph in TUI

### Not in Scope (v1)

- Real-time graphing
- Historical data storage
- Export to external systems
- Custom percentile calculations

---

## Implementation Plan

### Phase 1: Add Configuration and T-Digest to OriginScraper

1. Add config field for window duration:
   - `OriginMetricsWindow time.Duration` in `Config` struct
   - Default: 30 seconds
   - Validation: min 10s, max 300s (in `config.Validate()`)
2. Add CLI flag:
   - `-origin-metrics-window duration` flag in `flags.go`
   - Default: 30s, max: 300s
   - Include in help output under "Origin Metrics" category
3. Add T-Digest fields to `OriginScraper` (following `debug_events.go` pattern):
   - `netInDigest *tdigest.TDigest`
   - `netInSamples []networkSample`
   - `netInMu sync.Mutex`
   - `windowSize time.Duration` (configurable)
   - Same for `netOut`
4. Update `NewOriginScraper()` signature:
   - Add `windowSize time.Duration` parameter
   - Clamp window size (min 10s, max 300s) for safety
   - Initialize T-Digests: `tdigest.NewWithCompression(100)`
5. Update `orchestrator.go`:
   - Pass `cfg.OriginMetricsWindow` to `NewOriginScraper()`
6. Add `cleanupNetworkWindow()` helper method

### Phase 2: Update extractNetwork()

1. Add samples to T-Digest in `extractNetwork()`:
   - Lock mutex
   - `digest.Add(value, 1)`
   - Append to samples slice
   - Trigger cleanup if needed
   - Unlock mutex
2. Follow same pattern as `debug_events.go` segment completion

### Phase 3: Update GetMetrics()

1. Calculate percentiles in `GetMetrics()`:
   - Lock mutex
   - Cleanup samples
   - `digest.Quantile(0.50)` for P50
   - Calculate max from samples
   - Unlock mutex
2. Update `OriginMetrics` struct with new fields

### Phase 4: TUI Display

1. Update `renderOriginMetrics()` to show percentiles
2. Format: `Net In: 45.2 MB/s (instant) | P50: 23.1 MB/s | Max: 487.3 MB/s (30s)`
3. Test with various window sizes

### Phase 5: Testing

1. Add unit tests (following existing T-Digest test patterns)
2. Test with bursty traffic patterns
3. Verify thread safety with race detector
4. Performance validation

---

## References

- [ORIGIN_METRICS_IMPLEMENTATION_PLAN.md](ORIGIN_METRICS_IMPLEMENTATION_PLAN.md) - Original implementation plan
- [ATOMIC_MIGRATION_PLAN.md](ATOMIC_MIGRATION_PLAN.md) - Atomic operations patterns
- `internal/stats/client_stats.go` - Ring buffer example (`segmentSizes`)
- `internal/parser/debug_events.go` - Ring buffer example (`segmentWallTimes`)

---

**Document Status**: PROPOSAL
**Last Updated**: 2026-01-22
**Author**: Design Document
**Review Status**: Pending
