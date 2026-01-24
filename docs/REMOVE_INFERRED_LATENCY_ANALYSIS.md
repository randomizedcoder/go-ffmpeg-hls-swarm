# Remove Inferred Latency Analysis

## Current State

### Two Latency Systems (Redundant)

1. **Inferred Latency (Old System)** - `internal/stats/client_stats.go`
   - Uses `OnSegmentRequestStart()` + `CompleteOldestSegment()`
   - Infers completion from progress updates (imprecise)
   - Uses T-Digest for percentiles (P50, P95, P99, Max)
   - **Problem**: Timing is inferred, not measured directly

2. **Segment Wall Time (New System)** - `internal/parser/debug_events.go`
   - Uses FFmpeg timestamps (`-loglevel repeat+level+datetime+debug`)
   - Measures actual segment download time from FFmpeg logs
   - Provides avg, min, max (but no percentiles)
   - **Advantage**: Accurate timing from FFmpeg timestamps

### Current Usage

**Inferred Latency is still being used:**
- `client_manager.go`: Calls `clientStats.CompleteOldestSegment()` on progress updates
- `aggregator.go`: Aggregates inferred latency percentiles
- `tui/view.go`: Displays "Inferred Segment Latency" with P50/P95/P99

**Segment Wall Time is also being used:**
- `tui/view.go`: Displays "Segment Wall Time" with avg/max in HLS layer

**Result**: We're showing both systems in the TUI, which is confusing and redundant.

---

## Analysis

### Why Inferred Latency is Obsolete

1. **Less Accurate**:
   - Inferred from progress updates (imprecise timing)
   - Uses Go's `time.Now()` which has channel processing delays
   - May not match actual segment download time

2. **Redundant**:
   - DebugEventParser already tracks accurate segment wall time
   - Uses FFmpeg's native timestamps (millisecond precision)
   - No channel delays or Go processing overhead

3. **Maintenance Burden**:
   - Two systems doing the same thing
   - More code to maintain
   - More mutex contention (`inferredLatencyMu`)

### What We Need to Keep

**Percentiles are valuable** for understanding latency distribution:
- P50 (median) - typical performance
- P95 - worst-case for most users
- P99 - tail latency (critical for load testing)

**Current DebugStats only provides:**
- Avg, Min, Max (no percentiles)

---

## Recommendation: Add Percentiles to DebugEventParser

### Option 1: Add T-Digest to DebugEventParser ✅ **RECOMMENDED**

**Strategy:**
1. Add T-Digest to `DebugEventParser` for percentile calculation
2. Use accurate segment wall times (from FFmpeg timestamps)
3. Calculate P50, P95, P99, Max from accurate data
4. Remove inferred latency system entirely

**Benefits:**
- ✅ Accurate percentiles (from FFmpeg timestamps)
- ✅ Single source of truth
- ✅ Remove `inferredLatencyMu` mutex (one less lock!)
- ✅ Cleaner codebase

**Implementation:**
```go
// In DebugEventParser
import "github.com/influxdata/tdigest"

type DebugEventParser struct {
    // ... existing fields ...

    // Segment wall time percentiles (using accurate timestamps)
    segmentWallTimeDigest *tdigest.TDigest
    segmentWallTimeMu     sync.Mutex // Only for TDigest (not thread-safe)
}

func (p *DebugEventParser) handleSegmentComplete(url string, wallTime time.Duration) {
    // ... existing code ...

    // Add to T-Digest for percentiles
    p.segmentWallTimeMu.Lock()
    p.segmentWallTimeDigest.Add(float64(wallTime.Nanoseconds()), 1)
    p.segmentWallTimeMu.Unlock()
}

// Add to DebugStats
type DebugStats struct {
    // ... existing fields ...

    // Percentiles (from accurate timestamps)
    SegmentWallTimeP50 time.Duration
    SegmentWallTimeP95 time.Duration
    SegmentWallTimeP99 time.Duration
}
```

**Migration Steps:**
1. Add T-Digest to `DebugEventParser`
2. Calculate percentiles in `Stats()` method
3. Add percentile fields to `DebugStats`
4. Update `DebugStatsAggregate` to include percentiles
5. Update TUI to show percentiles from `DebugStats` instead of `InferredLatency`
6. Remove inferred latency code:
   - Remove `inferredLatencyMu`, `inferredLatencyDigest`, etc. from `ClientStats`
   - Remove `OnSegmentRequestStart()`, `CompleteOldestSegment()` calls
   - Remove `InferredLatency*` fields from `AggregatedStats`
   - Remove `renderLatencyStats()` from TUI (or update to use DebugStats)

---

### Option 2: Just Use Avg/Min/Max (Simpler, but loses percentiles)

**Strategy:**
1. Remove inferred latency system
2. Use only avg/min/max from DebugStats
3. Update TUI to show "Segment Wall Time" instead of "Inferred Latency"

**Benefits:**
- ✅ Simpler (no T-Digest needed)
- ✅ Removes inferred latency code
- ✅ Single source of truth

**Drawbacks:**
- ❌ Loses percentile information (P50, P95, P99)
- ❌ Less useful for load testing (can't see tail latency)

**Recommendation**: ❌ **Not recommended** - Percentiles are valuable for load testing.

---

## Code to Remove

### Files to Modify

1. **`internal/stats/client_stats.go`**:
   - Remove `inferredLatencyMu sync.Mutex`
   - Remove `inferredLatencyDigest *tdigest.TDigest`
   - Remove `inferredLatencyCount`, `inferredLatencySum`, `inferredLatencyMax`
   - Remove `OnSegmentRequestStart()`, `OnSegmentRequestComplete()`, `CompleteOldestSegment()`
   - Remove `recordInferredLatency()`
   - Remove `InferredLatencyP50()`, `InferredLatencyP95()`, `InferredLatencyP99()`, etc.

2. **`internal/stats/aggregator.go`**:
   - Remove `InferredLatencyP50`, `InferredLatencyP95`, `InferredLatencyP99`, `InferredLatencyMax`, `InferredLatencyCount` from `AggregatedStats`
   - Remove T-Digest merging logic for inferred latency

3. **`internal/orchestrator/client_manager.go`**:
   - Remove `clientStats.CompleteOldestSegment()` call
   - Remove `m.CompleteSegmentForClient()` call (legacy HLS parser)

4. **`internal/tui/view.go`**:
   - Remove or update `renderLatencyStats()` to use DebugStats percentiles
   - Update to show "Segment Latency" (not "Inferred")

5. **`internal/parser/hls_events.go`**:
   - Remove `CompleteOldestSegment()` (legacy, being replaced by DebugEventParser)

6. **Tests**:
   - Remove inferred latency tests
   - Update tests to use DebugStats

---

## Implementation Plan

### Phase 1: Add Percentiles to DebugEventParser

1. Add T-Digest to `DebugEventParser`
2. Track segment wall times in T-Digest
3. Calculate percentiles in `Stats()` method
4. Add percentile fields to `DebugStats` and `DebugStatsAggregate`

**Estimated Effort**: 1-2 hours

### Phase 2: Update TUI

1. Update `renderLatencyStats()` to use `DebugStats.SegmentWallTimeP50/P95/P99`
2. Change label from "Inferred Segment Latency" to "Segment Latency"
3. Remove note about "inferred"

**Estimated Effort**: 30 minutes

### Phase 3: Remove Inferred Latency Code

1. Remove inferred latency from `ClientStats`
2. Remove inferred latency from `AggregatedStats`
3. Remove calls to `CompleteOldestSegment()`
4. Remove legacy HLS parser methods
5. Update/remove tests

**Estimated Effort**: 1-2 hours

**Total Estimated Effort**: 3-4 hours

---

## Benefits Summary

### Code Quality
- ✅ Single source of truth for latency metrics
- ✅ Removes ~200 lines of redundant code
- ✅ Removes one mutex (`inferredLatencyMu`)
- ✅ Cleaner, more maintainable codebase

### Accuracy
- ✅ Percentiles calculated from accurate FFmpeg timestamps
- ✅ No channel processing delays
- ✅ Millisecond precision from FFmpeg

### Performance
- ✅ One less mutex to contend with
- ✅ Fewer function calls (no `CompleteOldestSegment()` on every progress update)
- ✅ Less memory (no `inflightRequests` sync.Map)

---

## Risks and Mitigation

### Risk: Breaking Existing Tests
- **Mitigation**: Update tests to use DebugStats before removing inferred latency

### Risk: TUI Shows Different Values
- **Mitigation**: Run side-by-side comparison before removing old system
- **Note**: Values may differ (new system is more accurate)

### Risk: Missing Percentiles During Transition
- **Mitigation**: Add percentiles to DebugStats first, then remove old system

---

## Conclusion

**Recommendation**: ✅ **Proceed with Option 1** (Add T-Digest to DebugEventParser)

**Rationale:**
1. Inferred latency is obsolete (we have accurate timestamps)
2. Percentiles are valuable for load testing
3. Removes redundant code and one mutex
4. Single source of truth is cleaner

**Next Steps:**
1. Review this analysis
2. If approved, implement Phase 1 (add percentiles to DebugEventParser)
3. Test side-by-side comparison
4. Implement Phase 2 (update TUI)
5. Implement Phase 3 (remove old code)
