# Segment Throughput Redesign: Implementation Plan

> **Status**: Ready for Review
> **Date**: 2026-02-02
> **Related**: [Redesign Doc](./SEGMENT_THROUGHPUT_REDESIGN.md) | [Original Design](./SEGMENT_SIZE_TRACKING_DESIGN.md)

## Decision: Build Custom Time Series Library

Given our simple requirements (store ~300 timestamped samples, compute rolling averages), we will build a custom internal library rather than adding external dependencies. The implementation is ~150 lines of well-tested Go code.

**Rationale:**
- Requirements are trivially simple (ring buffer + average calculation)
- Zero external dependencies for a core feature
- Full control over thread safety semantics
- ~10 KB memory footprint for 5-minute window

---

## Phase 1: Create Internal Time Series Library

### Definition of Done
- [ ] New package `internal/timeseries/` created
- [ ] `ThroughputTracker` type implemented with rolling averages
- [ ] Comprehensive test suite passes
- [ ] `go test -race` passes
- [ ] Benchmarks meet performance targets (<50ns AddBytes, <1µs GetStats)

### New Files

#### `internal/timeseries/throughput_tracker.go`

```go
// Package timeseries provides time-windowed metric tracking for HLS load testing.
//
// This is an internal library, not a general-purpose time series database.
// Designed for simplicity and testability over feature completeness.
package timeseries

// ThroughputTracker tracks cumulative bytes and computes rolling averages
// over configurable time windows (1s, 30s, 60s, 300s).
//
// Thread-safe: AddBytes() uses atomic int64, GetStats() acquires read lock.
// Memory: ~10KB for 300 samples (5 minute window at 1 sample/sec).
type ThroughputTracker struct { ... }

// Functions to implement:
func NewThroughputTracker() *ThroughputTracker
func NewThroughputTrackerWithClock(clock Clock) *ThroughputTracker  // For testing
func (t *ThroughputTracker) AddBytes(n int64)                       // Atomic, lock-free
func (t *ThroughputTracker) RecordSample()                          // Called by ticker
func (t *ThroughputTracker) GetStats() ThroughputStats              // Computes averages
func (t *ThroughputTracker) Reset()                                 // Clears all data

// ThroughputStats contains computed rolling averages
type ThroughputStats struct {
    TotalBytes   int64
    Avg1s        float64  // bytes/sec over last 1 second
    Avg30s       float64  // bytes/sec over last 30 seconds
    Avg60s       float64  // bytes/sec over last 60 seconds
    Avg300s      float64  // bytes/sec over last 300 seconds (5 min)
    AvgOverall   float64  // bytes/sec since start
}

// Clock interface for testing
type Clock interface {
    Now() time.Time
}
```

**Implementation notes:**
- `totalBytes` uses `atomic.Int64` for lock-free AddBytes()
- Ring buffer stores `{timestamp, cumulativeBytes}` samples
- Mutex only held during sample recording and stat calculation
- Clock interface allows deterministic testing

#### `internal/timeseries/throughput_tracker_test.go`

**Test categories (from redesign doc):**

| Test | Purpose |
|------|---------|
| `TestThroughputTracker_AddBytes` | Basic byte accumulation (table-driven) |
| `TestThroughputTracker_RollingAverage` | Average calculation for various patterns |
| `TestThroughputTracker_WindowEdgeCases` | No samples, single sample, boundaries |
| `TestThroughputTracker_RingBufferOverflow` | Buffer wraparound correctness |
| `TestThroughputTracker_ConcurrentAddBytes` | Race: 100 goroutines x 1000 adds |
| `TestThroughputTracker_ConcurrentAddAndRead` | Race: writers + readers |
| `TestThroughputTracker_ConcurrentSampling` | Race: realistic scenario |
| `TestThroughputTracker_TUIDoesNotFlash` | **Key test**: stats always available |
| `BenchmarkThroughputTracker_AddBytes` | Target: <50ns |
| `BenchmarkThroughputTracker_GetStats` | Target: <1µs |

---

## Phase 2: Remove Histogram-Based Throughput Tracking

### Definition of Done
- [ ] `throughput_histogram.go` deleted
- [ ] `throughput_histogram_test.go` deleted
- [ ] All references to histogram removed from `debug_events.go`
- [ ] All references to histogram removed from `client_manager.go`
- [ ] Code compiles with no unused import warnings

### Files to Delete

| File | Lines | Purpose |
|------|-------|---------|
| `internal/parser/throughput_histogram.go` | 143 | Histogram implementation |
| `internal/parser/throughput_histogram_test.go` | 320 | Histogram tests |

### Files to Modify

#### `internal/parser/debug_events.go`

**Remove these fields from `DebugEventParser` struct (lines 276-279):**
```go
// REMOVE:
throughputHist *ThroughputHistogram
maxThroughput  atomic.Uint64 // Atomic max (stored as bits via math.Float64bits)
```

**Remove from constructor `NewDebugEventParserWithSizeLookup()` (line 340):**
```go
// REMOVE:
throughputHist: NewThroughputHistogram(),
```

**Remove `recordThroughput()` function (lines 1014-1037):**
```go
// REMOVE ENTIRE FUNCTION:
func (p *DebugEventParser) recordThroughput(bytesPerSec float64) { ... }
```

**Remove `loadMaxThroughput()` function (lines 1039-1042):**
```go
// REMOVE ENTIRE FUNCTION:
func (p *DebugEventParser) loadMaxThroughput() float64 { ... }
```

**Remove `DrainThroughputHistogram()` function (lines 1296-1305):**
```go
// REMOVE ENTIRE FUNCTION:
func (p *DebugEventParser) DrainThroughputHistogram() [64]uint64 { ... }
```

**Remove calls to `recordThroughput()` (lines 623-626):**
```go
// REMOVE these lines from handleHLSRequest():
if wallTime >= minWallTimeForThroughput {
    throughput := float64(size) / wallTime.Seconds()
    p.recordThroughput(throughput)
}
```

**Remove calls to `recordThroughput()` (lines 893-896):**
```go
// REMOVE these lines from trackSegmentFromHTTP():
if wallTime >= minWallTimeForThroughput {
    throughput := float64(size) / wallTime.Seconds()
    p.recordThroughput(throughput)
}
```

**Remove from `DebugStats` struct (line 1174):**
```go
// REMOVE:
MaxThroughput float64 // Max throughput observed (bytes/sec)
```

**Remove from `Stats()` method (line 1209):**
```go
// REMOVE:
MaxThroughput: p.loadMaxThroughput(),
```

**Remove import:**
```go
// REMOVE from imports if no longer needed:
"math"
```

#### `internal/parser/debug_events_test.go`

**Remove test (lines 1155-1200):**
```go
// REMOVE ENTIRE TEST:
func TestDebugEventParser_TrackSegmentFromHTTP_ThroughputHistogram(t *testing.T) { ... }
```

---

## Phase 3: Update Stats Aggregation

### Definition of Done
- [ ] `DebugStatsAggregate` uses rolling average fields
- [ ] `GetDebugStats()` in `client_manager.go` uses new tracker
- [ ] Old histogram drain/merge logic removed
- [ ] Code compiles and tests pass

### Files to Modify

#### `internal/stats/aggregator.go`

**Replace fields in `DebugStatsAggregate` (lines 150-158):**

```go
// REMOVE:
SegmentThroughputMax    float64 // Max throughput observed (bytes/sec)
SegmentThroughputP25    float64 // 25th percentile throughput (bytes/sec)
SegmentThroughputP50    float64 // 50th percentile throughput (bytes/sec)
SegmentThroughputP75    float64 // 75th percentile throughput (bytes/sec)
SegmentThroughputP95    float64 // 95th percentile throughput (bytes/sec)
SegmentThroughputP99    float64 // 99th percentile throughput (bytes/sec)
SegmentThroughputBuckets [64]uint64 // Merged histogram buckets

// ADD:
SegmentThroughputAvg1s    float64 // bytes/sec over last 1 second
SegmentThroughputAvg30s   float64 // bytes/sec over last 30 seconds
SegmentThroughputAvg60s   float64 // bytes/sec over last 60 seconds
SegmentThroughputAvg300s  float64 // bytes/sec over last 300 seconds
SegmentThroughputOverall  float64 // bytes/sec since start
```

#### `internal/orchestrator/client_manager.go`

**Add `ThroughputTracker` field to `ClientManager`:**
```go
// ADD to ClientManager struct:
throughputTracker *timeseries.ThroughputTracker
```

**Initialize in constructor:**
```go
// ADD to NewClientManager():
throughputTracker: timeseries.NewThroughputTracker(),
```

**Wire byte additions from parsers:**
```go
// When segment completes (in client's goroutine):
if p.segmentSizeLookup != nil {
    if size, ok := p.segmentSizeLookup.GetSegmentSize(segmentName); ok {
        cm.throughputTracker.AddBytes(size)  // Thread-safe atomic add
    }
}
```

**Start sample ticker in `Start()` method:**
```go
// ADD to Start():
go func() {
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()
    for {
        select {
        case <-cm.ctx.Done():
            return
        case <-ticker.C:
            cm.throughputTracker.RecordSample()
        }
    }
}()
```

**Update `GetDebugStats()` (around lines 620-780):**

```go
// REMOVE all this histogram logic:
drained := dp.DrainThroughputHistogram()
drainedHistograms = append(drainedHistograms, drained)
// ...
agg.SegmentThroughputBuckets = parser.MergeBuckets(drainedHistograms...)
agg.SegmentThroughputP25 = parser.PercentileFromBuckets(...)
// etc.

// ADD:
throughputStats := cm.throughputTracker.GetStats()
agg.SegmentThroughputAvg1s = throughputStats.Avg1s
agg.SegmentThroughputAvg30s = throughputStats.Avg30s
agg.SegmentThroughputAvg60s = throughputStats.Avg60s
agg.SegmentThroughputAvg300s = throughputStats.Avg300s
agg.SegmentThroughputOverall = throughputStats.AvgOverall
agg.TotalSegmentBytes = throughputStats.TotalBytes
```

**Remove cached stats logic (around lines 575-615):**
```go
// REMOVE:
type cachedDebugStatsEntry struct { ... }
cachedDebugStats atomic.Value
debugStatsCacheTTL time.Duration
// And all cache-related code in GetDebugStats()
```

---

## Phase 4: Update TUI Display

### Definition of Done
- [ ] "Segment Throughput" column shows rolling averages
- [ ] Column never shows "(no data)" after first sample
- [ ] TUI doesn't flash

### Files to Modify

#### `internal/tui/view.go`

**Replace segment throughput display (lines 286-300):**

```go
// REMOVE:
if m.debugStats.SegmentThroughputP50 > 0 {
    rightCol = append(rightCol, sectionHeaderStyle.Render("Segment Throughput *"))
    rightCol = append(rightCol,
        renderThroughputRow("P25", m.debugStats.SegmentThroughputP25),
        renderThroughputRow("P50 (median)", m.debugStats.SegmentThroughputP50),
        // ...
    )
} else {
    rightCol = append(rightCol, dimStyle.Render("  (no data)"))
}

// ADD:
rightCol = append(rightCol, sectionHeaderStyle.Render("Segment Throughput *"))
if m.debugStats.TotalSegmentBytes > 0 {
    rightCol = append(rightCol,
        renderThroughputRow("Last 1s", m.debugStats.SegmentThroughputAvg1s),
        renderThroughputRow("Last 30s", m.debugStats.SegmentThroughputAvg30s),
        renderThroughputRow("Last 60s", m.debugStats.SegmentThroughputAvg60s),
        renderThroughputRow("Last 5m", m.debugStats.SegmentThroughputAvg300s),
        renderThroughputRow("Overall", m.debugStats.SegmentThroughputOverall),
        renderBytesRow("Total", m.debugStats.TotalSegmentBytes),
    )
} else {
    rightCol = append(rightCol, dimStyle.Render("  (warming up)"))
}
```

**Add helper function:**
```go
// ADD:
func renderBytesRow(label string, bytes int64) string {
    value := formatBytes(bytes)
    return lipgloss.JoinHorizontal(lipgloss.Left,
        labelStyle.Render(label+":"),
        valueStyle.Render(value),
    )
}
```

---

## Phase 5: Update Prometheus Metrics

### Definition of Done
- [ ] Percentile gauges removed
- [ ] Rolling average gauges added
- [ ] `RecordStats()` updated
- [ ] Grafana dashboards still work (different metric names)

### Files to Modify

#### `internal/metrics/collector.go`

**Remove gauge declarations (lines 143-164):**
```go
// REMOVE:
hlsSegmentThroughputMaxBytesPerSec = prometheus.NewGauge(...)
hlsSegmentThroughputP50BytesPerSec = prometheus.NewGauge(...)
hlsSegmentThroughputP95BytesPerSec = prometheus.NewGauge(...)
hlsSegmentThroughputP99BytesPerSec = prometheus.NewGauge(...)
```

**Add new gauge declarations:**
```go
// ADD:
hlsSegmentThroughputAvg1sBytesPerSec = prometheus.NewGauge(prometheus.GaugeOpts{
    Name: "hls_swarm_segment_throughput_1s_bytes_per_second",
    Help: "Segment download throughput averaged over last 1 second",
})
hlsSegmentThroughputAvg30sBytesPerSec = prometheus.NewGauge(prometheus.GaugeOpts{
    Name: "hls_swarm_segment_throughput_30s_bytes_per_second",
    Help: "Segment download throughput averaged over last 30 seconds",
})
hlsSegmentThroughputAvg60sBytesPerSec = prometheus.NewGauge(prometheus.GaugeOpts{
    Name: "hls_swarm_segment_throughput_60s_bytes_per_second",
    Help: "Segment download throughput averaged over last 60 seconds",
})
hlsSegmentThroughputAvg300sBytesPerSec = prometheus.NewGauge(prometheus.GaugeOpts{
    Name: "hls_swarm_segment_throughput_300s_bytes_per_second",
    Help: "Segment download throughput averaged over last 5 minutes",
})
```

**Update registrations (lines 534-537):**
```go
// REMOVE old registrations, ADD new ones
```

**Update `AggregatedStatsUpdate` struct (lines 653-658):**
```go
// REMOVE:
SegmentThroughputMax    float64
SegmentThroughputP25    float64
SegmentThroughputP50    float64
SegmentThroughputP75    float64
SegmentThroughputP95    float64
SegmentThroughputP99    float64

// ADD:
SegmentThroughputAvg1s    float64
SegmentThroughputAvg30s   float64
SegmentThroughputAvg60s   float64
SegmentThroughputAvg300s  float64
```

**Update `RecordStats()` (lines 745-748):**
```go
// REMOVE:
hlsSegmentThroughputMaxBytesPerSec.Set(stats.SegmentThroughputMax)
hlsSegmentThroughputP50BytesPerSec.Set(stats.SegmentThroughputP50)
hlsSegmentThroughputP95BytesPerSec.Set(stats.SegmentThroughputP95)
hlsSegmentThroughputP99BytesPerSec.Set(stats.SegmentThroughputP99)

// ADD:
hlsSegmentThroughputAvg1sBytesPerSec.Set(stats.SegmentThroughputAvg1s)
hlsSegmentThroughputAvg30sBytesPerSec.Set(stats.SegmentThroughputAvg30s)
hlsSegmentThroughputAvg60sBytesPerSec.Set(stats.SegmentThroughputAvg60s)
hlsSegmentThroughputAvg300sBytesPerSec.Set(stats.SegmentThroughputAvg300s)
```

#### `internal/orchestrator/orchestrator.go`

**Update stats update (lines 549-554):**
```go
// REMOVE:
update.SegmentThroughputMax = debugStats.SegmentThroughputMax
update.SegmentThroughputP25 = debugStats.SegmentThroughputP25
// etc.

// ADD:
update.SegmentThroughputAvg1s = debugStats.SegmentThroughputAvg1s
update.SegmentThroughputAvg30s = debugStats.SegmentThroughputAvg30s
update.SegmentThroughputAvg60s = debugStats.SegmentThroughputAvg60s
update.SegmentThroughputAvg300s = debugStats.SegmentThroughputAvg300s
```

---

## Phase 6: Cleanup and Documentation

### Definition of Done
- [ ] All tests pass (`go test ./...`)
- [ ] Race detector passes (`go test -race ./...`)
- [ ] Linter passes (`golangci-lint run`)
- [ ] Design docs updated
- [ ] CHANGELOG updated

### Tasks

1. **Remove unused imports** from all modified files
2. **Update design docs:**
   - Mark `SEGMENT_SIZE_TRACKING_DESIGN.md` as superseded
   - Update `SEGMENT_THROUGHPUT_REDESIGN.md` status to "Implemented"
3. **Add CHANGELOG entry:**
   ```
   ### Changed
   - Segment throughput tracking: replaced percentiles with rolling time-window
     averages (1s, 30s, 60s, 300s) to fix TUI flashing issue
   - Prometheus metrics: `hls_swarm_segment_throughput_p*` replaced with
     `hls_swarm_segment_throughput_*s_bytes_per_second`
   ```

---

## Summary of Changes

### Files to Delete (2 files, ~463 lines)

| File | Lines |
|------|-------|
| `internal/parser/throughput_histogram.go` | 143 |
| `internal/parser/throughput_histogram_test.go` | 320 |

### Files to Create (2 files, ~400 lines estimated)

| File | Est. Lines | Purpose |
|------|------------|---------|
| `internal/timeseries/throughput_tracker.go` | ~150 | Ring buffer + rolling averages |
| `internal/timeseries/throughput_tracker_test.go` | ~250 | Comprehensive tests |

### Files to Modify (7 files)

| File | Change Type |
|------|-------------|
| `internal/parser/debug_events.go` | Remove histogram fields, methods, calls |
| `internal/parser/debug_events_test.go` | Remove histogram test |
| `internal/stats/aggregator.go` | Replace percentile fields with avg fields |
| `internal/orchestrator/client_manager.go` | Use ThroughputTracker, remove histogram logic |
| `internal/orchestrator/orchestrator.go` | Update stats mapping |
| `internal/tui/view.go` | Display rolling averages |
| `internal/metrics/collector.go` | Replace percentile metrics with avg metrics |

---

## Testing Checklist

### Unit Tests
- [ ] `go test ./internal/timeseries/...` passes
- [ ] `go test ./internal/parser/...` passes
- [ ] `go test ./internal/stats/...` passes
- [ ] `go test ./internal/orchestrator/...` passes
- [ ] `go test ./internal/tui/...` passes
- [ ] `go test ./internal/metrics/...` passes

### Race Detection
- [ ] `go test -race ./internal/timeseries/...` passes
- [ ] `go test -race ./...` passes (full suite)

### Integration
- [ ] Run swarm with 10 clients, verify TUI doesn't flash
- [ ] Run swarm with 100 clients, verify metrics update
- [ ] Verify Prometheus metrics scrape correctly

### Performance
- [ ] Benchmark `AddBytes` < 50ns
- [ ] Benchmark `GetStats` < 1µs
- [ ] Memory usage stable over 5+ minute run

---

## Rollback Plan

If issues arise:
1. Restore `throughput_histogram.go` from git
2. Revert changes to `debug_events.go`, `client_manager.go`
3. Revert TUI and metrics changes

All changes are isolated to throughput tracking; latency tracking is unaffected.
