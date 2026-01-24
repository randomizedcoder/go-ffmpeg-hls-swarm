# Combined Latency Metrics Implementation Plan

## Table of Contents

1. [Overview](#overview)
2. [Implementation Summary](#implementation-summary)
3. [Phase 1: Segment Latency P25/P75](#phase-1-segment-latency-p25p75)
4. [Phase 2: Manifest Latency Tracking](#phase-2-manifest-latency-tracking)
5. [Phase 3: TUI Dashboard Updates](#phase-3-tui-dashboard-updates)
6. [Test Implementation](#test-implementation)
7. [Verification Checklist](#verification-checklist)
8. [References](#references)

---

## Overview

This document provides a detailed, step-by-step implementation plan combining two features:

1. **Segment Latency P25/P75**: Add P25 and P75 percentiles to existing segment latency tracking
2. **Manifest Latency Tracking**: Add complete manifest latency tracking with all percentiles (P25, P50, P75, P95, P99, Max)

### Design Documents

- **Segment Latency P25/P75**: `docs/SEGMENT_LATENCY_P25_P75_DESIGN.md`
- **Manifest Latency Tracking**: `docs/MANIFEST_LATENCY_TRACKING_DESIGN.md`

### Implementation Order

1. **Phase 1**: Add P25/P75 to segment latency (simpler, builds on existing)
2. **Phase 2**: Add manifest latency tracking (new feature, similar pattern)
3. **Phase 3**: Update TUI to two-column layout (combines both features)

---

## Implementation Summary

### Files to Modify

| File | Purpose | Functions Modified | Lines Affected |
|------|---------|-------------------|----------------|
| `internal/parser/debug_events.go` | Core latency tracking | `NewDebugEventParser()`, `handleHLSRequest()`, `handlePlaylistOpen()`, `Stats()` | ~166-180, ~240-262, ~304-308, ~418-477, ~547-583, ~783-845, ~847-928 |
| `internal/stats/aggregator.go` | Aggregate across clients | `Aggregate()` | ~86-135, ~137-460 |
| `internal/orchestrator/client_manager.go` | Client aggregation (if needed) | `AggregateDebugStats()` or similar | ~580-595 (verify) |
| `internal/tui/view.go` | Dashboard rendering | `renderLatencyStats()` | ~242-268 |

### Test Files to Update

| File | Tests to Add/Update | Type |
|------|-------------------|------|
| `internal/parser/debug_events_test.go` | `TestDebugEventParser_Percentiles`, `TestDebugEventParser_PercentilesOrdering`, `TestDebugEventParser_ManifestLatency`, `TestDebugEventParser_ManifestLatencyPercentiles` | Table-driven, unit |
| `internal/stats/aggregator_test.go` | `TestStatsAggregator_AggregatePercentiles`, `TestStatsAggregator_AggregateManifestLatency` | Table-driven, unit |
| `internal/tui/view_test.go` | `TestRenderLatencyStats_TwoColumns`, `TestRenderLatencyStats_EmptyStats` | Unit |

---

## Phase 1: Segment Latency P25/P75

### Goal
Add P25 and P75 percentile calculations to existing segment latency tracking.

### Step 1.1: Update `DebugStats` Struct

**File**: `internal/parser/debug_events.go`
**Location**: Line ~802-804
**Function**: N/A (struct definition)

**Current Code**:
```go
// Percentiles (from T-Digest, using accurate timestamps)
SegmentWallTimeP50 time.Duration // 50th percentile (median)
SegmentWallTimeP95 time.Duration // 95th percentile
SegmentWallTimeP99 time.Duration // 99th percentile
```

**Updated Code**:
```go
// Percentiles (from T-Digest, using accurate timestamps)
SegmentWallTimeP25 time.Duration // 25th percentile
SegmentWallTimeP50 time.Duration // 50th percentile (median)
SegmentWallTimeP75 time.Duration // 75th percentile
SegmentWallTimeP95 time.Duration // 95th percentile
SegmentWallTimeP99 time.Duration // 99th percentile
```

**Action**: Add `SegmentWallTimeP25` and `SegmentWallTimeP75` fields.

---

### Step 1.2: Update `Stats()` Method - Calculate P25/P75

**File**: `internal/parser/debug_events.go`
**Location**: Line ~887-894
**Function**: `Stats()` (line ~848)

**Current Code**:
```go
// Calculate percentiles from T-Digest (using accurate FFmpeg timestamps)
p.segmentWallTimeDigestMu.Lock()
if p.segmentWallTimeDigest != nil {
    stats.SegmentWallTimeP50 = time.Duration(p.segmentWallTimeDigest.Quantile(0.50))
    stats.SegmentWallTimeP95 = time.Duration(p.segmentWallTimeDigest.Quantile(0.95))
    stats.SegmentWallTimeP99 = time.Duration(p.segmentWallTimeDigest.Quantile(0.99))
}
p.segmentWallTimeDigestMu.Unlock()
```

**Updated Code**:
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

**Action**: Add two lines to calculate P25 and P75 using `Quantile(0.25)` and `Quantile(0.75)`.

---

### Step 1.3: Update `DebugStatsAggregate` Struct

**File**: `internal/stats/aggregator.go`
**Location**: Line ~97-100
**Function**: N/A (struct definition)

**Current Code**:
```go
// Percentiles (from T-Digest, using accurate FFmpeg timestamps)
SegmentWallTimeP50 time.Duration // 50th percentile (median)
SegmentWallTimeP95 time.Duration // 95th percentile
SegmentWallTimeP99 time.Duration // 99th percentile
```

**Updated Code**:
```go
// Percentiles (from T-Digest, using accurate FFmpeg timestamps)
SegmentWallTimeP25 time.Duration // 25th percentile
SegmentWallTimeP50 time.Duration // 50th percentile (median)
SegmentWallTimeP75 time.Duration // 75th percentile
SegmentWallTimeP95 time.Duration // 95th percentile
SegmentWallTimeP99 time.Duration // 99th percentile
```

**Action**: Add `SegmentWallTimeP25` and `SegmentWallTimeP75` fields.

---

### Step 1.4: Update `Aggregate()` Method - Aggregate P25/P75

**File**: `internal/stats/aggregator.go`
**Location**: Line ~586-595
**Function**: `Aggregate()` (line ~137)

**Current Code**:
```go
// Aggregate percentiles (take max across clients - worst-case is useful for load testing)
if stats.SegmentWallTimeP50 > agg.SegmentWallTimeP50 {
    agg.SegmentWallTimeP50 = stats.SegmentWallTimeP50
}
if stats.SegmentWallTimeP95 > agg.SegmentWallTimeP95 {
    agg.SegmentWallTimeP95 = stats.SegmentWallTimeP95
}
if stats.SegmentWallTimeP99 > agg.SegmentWallTimeP99 {
    agg.SegmentWallTimeP99 = stats.SegmentWallTimeP99
}
```

**Updated Code**:
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

**Action**: Add aggregation logic for P25 and P75 (max aggregation).

---

### Step 1.5: Verify `client_manager.go` (Optional)

**File**: `internal/orchestrator/client_manager.go`
**Location**: Line ~586-595
**Function**: `AggregateDebugStats()` or similar (verify if exists)

**Action**:
1. Search for segment percentile aggregation in this file
2. If found, add P25 and P75 aggregation following the same pattern
3. If not found, skip this step

---

## Phase 2: Manifest Latency Tracking

### Goal
Add complete manifest latency tracking with percentiles (P25, P50, P75, P95, P99, Max).

### Step 2.1: Add Manifest Tracking Fields to `DebugEventParser`

**File**: `internal/parser/debug_events.go`
**Location**: Line ~166-180 (after segment tracking fields)
**Function**: N/A (struct definition)

**Action**: Add the following fields to `DebugEventParser` struct:

```go
// Manifest Wall Time tracking (similar to Segment Wall Time)
// Maps URL -> start time
pendingManifests   map[string]time.Time
manifestWallTimes  []time.Duration // Ring buffer (last N samples)
manifestWallTimeP0 int             // Ring buffer position
manifestCount      atomic.Int64

// Manifest wall time aggregates
manifestWallTimeSum   int64 // nanoseconds
manifestWallTimeMax   int64 // nanoseconds
manifestWallTimeMin   int64 // nanoseconds (-1 = unset)

// Manifest wall time percentiles (using accurate FFmpeg timestamps)
manifestWallTimeDigest *tdigest.TDigest
manifestWallTimeDigestMu sync.Mutex // TDigest is not thread-safe
```

**Note**: Place these fields after the segment tracking fields for consistency.

---

### Step 2.2: Initialize Manifest Tracking in `NewDebugEventParser()`

**File**: `internal/parser/debug_events.go`
**Location**: Line ~262 (in `NewDebugEventParser()` function)
**Function**: `NewDebugEventParser()`

**Action**: Add initialization code:

```go
manifestWallTimeDigest: tdigest.NewWithCompression(100), // ~100 centroids, ~10KB
pendingManifests: make(map[string]time.Time),
```

**Note**: Initialize alongside `segmentWallTimeDigest` for consistency.

---

### Step 2.3: Update `handleHLSRequest()` - Complete Pending Manifests

**File**: `internal/parser/debug_events.go`
**Location**: Line ~418-477 (in `handleHLSRequest()` function, at the start, before segment completion)
**Function**: `handleHLSRequest()`

**Action**: Add manifest completion logic at the **beginning** of `handleHLSRequest()`, before segment completion:

```go
// Complete oldest pending manifest (if any) before starting new segment
// This indicates the manifest download is complete
p.mu.Lock()
if len(p.pendingManifests) > 0 {
    var oldestURL string
    var oldestTime time.Time
    for u, t := range p.pendingManifests {
        if oldestTime.IsZero() || t.Before(oldestTime) {
            oldestURL = u
            oldestTime = t
        }
    }
    if oldestURL != "" {
        wallTime := now.Sub(oldestTime)
        delete(p.pendingManifests, oldestURL)

        // Record manifest wall time (similar to segment wall time)
        ns := int64(wallTime)
        p.manifestCount.Add(1)
        p.manifestWallTimeSum += ns

        if p.manifestWallTimeMin < 0 || ns < p.manifestWallTimeMin {
            p.manifestWallTimeMin = ns
        }
        if ns > p.manifestWallTimeMax {
            p.manifestWallTimeMax = ns
        }

        // Ring buffer
        if len(p.manifestWallTimes) < defaultRingSize {
            p.manifestWallTimes = append(p.manifestWallTimes, wallTime)
        } else {
            p.manifestWallTimes[p.manifestWallTimeP0] = wallTime
            p.manifestWallTimeP0 = (p.manifestWallTimeP0 + 1) % defaultRingSize
        }

        // Add to T-Digest for percentile calculation
        p.manifestWallTimeDigestMu.Lock()
        p.manifestWallTimeDigest.Add(float64(wallTime.Nanoseconds()), 1)
        p.manifestWallTimeDigestMu.Unlock()
    }
}
p.mu.Unlock()
```

**Note**: This must execute **before** the existing segment completion logic in `handleHLSRequest()`.

---

### Step 2.4: Update `handlePlaylistOpen()` - Track Manifest Start

**File**: `internal/parser/debug_events.go`
**Location**: Line ~547-583 (in `handlePlaylistOpen()` function, at the start)
**Function**: `handlePlaylistOpen()`

**Action**: Add manifest start tracking at the **beginning** of `handlePlaylistOpen()`, before jitter tracking:

```go
// Track manifest download start time
p.mu.Lock()
p.pendingManifests[url] = now
p.mu.Unlock()
```

**Note**: Place this immediately after `p.playlistRefreshes.Add(1)` and before the existing jitter tracking code.

---

### Step 2.5: Add Manifest Latency Fields to `DebugStats` Struct

**File**: `internal/parser/debug_events.go`
**Location**: Line ~783-845 (in `DebugStats` struct, after segment fields)
**Function**: N/A (struct definition)

**Action**: Add the following fields after segment latency fields:

```go
// Manifest wall time (using accurate FFmpeg timestamps)
ManifestCount int64
ManifestAvgMs float64
ManifestMinMs float64
ManifestMaxMs float64
// Percentiles (from T-Digest, using accurate timestamps)
ManifestWallTimeP25 time.Duration // 25th percentile
ManifestWallTimeP50 time.Duration // 50th percentile (median)
ManifestWallTimeP75 time.Duration // 75th percentile
ManifestWallTimeP95 time.Duration // 95th percentile
ManifestWallTimeP99 time.Duration // 99th percentile
```

---

### Step 2.6: Update `Stats()` Method - Calculate Manifest Percentiles

**File**: `internal/parser/debug_events.go`
**Location**: Line ~847-928 (in `Stats()` method, after segment percentile calculation)
**Function**: `Stats()`

**Action**: Add manifest percentile calculation after segment percentile calculation:

```go
// Manifest wall time averages
if stats.ManifestCount > 0 {
    stats.ManifestAvgMs = float64(p.manifestWallTimeSum) / float64(stats.ManifestCount) / 1e6
    if p.manifestWallTimeMin >= 0 {
        stats.ManifestMinMs = float64(p.manifestWallTimeMin) / 1e6
    }
    stats.ManifestMaxMs = float64(p.manifestWallTimeMax) / 1e6

    // Calculate percentiles from T-Digest (using accurate FFmpeg timestamps)
    p.manifestWallTimeDigestMu.Lock()
    if p.manifestWallTimeDigest != nil {
        stats.ManifestWallTimeP25 = time.Duration(p.manifestWallTimeDigest.Quantile(0.25))
        stats.ManifestWallTimeP50 = time.Duration(p.manifestWallTimeDigest.Quantile(0.50))
        stats.ManifestWallTimeP75 = time.Duration(p.manifestWallTimeDigest.Quantile(0.75))
        stats.ManifestWallTimeP95 = time.Duration(p.manifestWallTimeDigest.Quantile(0.95))
        stats.ManifestWallTimeP99 = time.Duration(p.manifestWallTimeDigest.Quantile(0.99))
    }
    p.manifestWallTimeDigestMu.Unlock()
}
```

**Note**: Also initialize `stats.ManifestCount` in the struct initialization section of `Stats()`:
```go
ManifestCount: p.manifestCount.Load(),
```

---

### Step 2.7: Add Manifest Latency Fields to `DebugStatsAggregate` Struct

**File**: `internal/stats/aggregator.go`
**Location**: Line ~86-135 (in `DebugStatsAggregate` struct, after segment fields)
**Function**: N/A (struct definition)

**Action**: Add the following fields after segment latency fields:

```go
// Manifest wall time (using accurate FFmpeg timestamps)
ManifestCount int64
ManifestWallTimeAvg float64
ManifestWallTimeMin float64
ManifestWallTimeMax float64
// Percentiles (from T-Digest, using accurate FFmpeg timestamps)
ManifestWallTimeP25 time.Duration // 25th percentile
ManifestWallTimeP50 time.Duration // 50th percentile (median)
ManifestWallTimeP75 time.Duration // 75th percentile
ManifestWallTimeP95 time.Duration // 95th percentile
ManifestWallTimeP99 time.Duration // 99th percentile
```

---

### Step 2.8: Update `Aggregate()` Method - Aggregate Manifest Latency

**File**: `internal/stats/aggregator.go`
**Location**: Line ~137-460 (in `Aggregate()` method, after segment aggregation)
**Function**: `Aggregate()`

**Action**: Add manifest latency aggregation after segment aggregation:

```go
// Aggregate manifest wall time
agg.ManifestCount += stats.ManifestCount
if stats.ManifestCount > 0 {
    // Weighted average
    totalCount := agg.ManifestCount
    if totalCount > 0 {
        agg.ManifestWallTimeAvg = (agg.ManifestWallTimeAvg*float64(agg.ManifestCount-stats.ManifestCount) +
            stats.ManifestAvgMs*float64(stats.ManifestCount)) / float64(totalCount)
    }

    // Min/Max
    if stats.ManifestMaxMs > agg.ManifestWallTimeMax {
        agg.ManifestWallTimeMax = stats.ManifestMaxMs
    }
    if agg.ManifestWallTimeMin == 0 || stats.ManifestMinMs < agg.ManifestWallTimeMin {
        agg.ManifestWallTimeMin = stats.ManifestMinMs
    }

    // Aggregate percentiles (take max across clients - worst-case is useful for load testing)
    if stats.ManifestWallTimeP25 > agg.ManifestWallTimeP25 {
        agg.ManifestWallTimeP25 = stats.ManifestWallTimeP25
    }
    if stats.ManifestWallTimeP50 > agg.ManifestWallTimeP50 {
        agg.ManifestWallTimeP50 = stats.ManifestWallTimeP50
    }
    if stats.ManifestWallTimeP75 > agg.ManifestWallTimeP75 {
        agg.ManifestWallTimeP75 = stats.ManifestWallTimeP75
    }
    if stats.ManifestWallTimeP95 > agg.ManifestWallTimeP95 {
        agg.ManifestWallTimeP95 = stats.ManifestWallTimeP95
    }
    if stats.ManifestWallTimeP99 > agg.ManifestWallTimeP99 {
        agg.ManifestWallTimeP99 = stats.ManifestWallTimeP99
    }
}
```

---

### Step 2.9: Verify `client_manager.go` (Optional)

**File**: `internal/orchestrator/client_manager.go`
**Location**: Line ~580-595
**Function**: `AggregateDebugStats()` or similar (verify if exists)

**Action**:
1. Search for segment percentile aggregation in this file
2. If found, add manifest percentile aggregation following the same pattern
3. If not found, skip this step

---

## Phase 3: TUI Dashboard Updates

### Goal
Update TUI to display both manifest and segment latency in a two-column layout.

### Step 3.1: Update `renderLatencyStats()` Function

**File**: `internal/tui/view.go`
**Location**: Line ~242-268
**Function**: `renderLatencyStats()`

**Current Code** (simplified):
```go
func (m Model) renderLatencyStats() string {
    if m.debugStats == nil || m.debugStats.SegmentWallTimeP50 == 0 {
        return ""
    }

    rows := []string{
        renderLatencyRow("P50 (median)", m.debugStats.SegmentWallTimeP50),
        renderLatencyRow("P95", m.debugStats.SegmentWallTimeP95),
        renderLatencyRow("P99", m.debugStats.SegmentWallTimeP99),
        renderLatencyRow("Max", time.Duration(m.debugStats.SegmentWallTimeMax*float64(time.Millisecond))),
    }

    note := dimStyle.Render("* Using accurate FFmpeg timestamps")
    content := lipgloss.JoinVertical(lipgloss.Left,
        append([]string{sectionHeaderStyle.Render("Segment Latency *")}, rows...)...,
    )
    content = lipgloss.JoinVertical(lipgloss.Left, content, note)

    return boxStyle.Width(m.width - 2).Render(content)
}
```

**Updated Code**:
```go
func (m Model) renderLatencyStats() string {
    // Use DebugStats percentiles (accurate timestamps from FFmpeg)
    if m.debugStats == nil || (m.debugStats.SegmentWallTimeP50 == 0 && m.debugStats.ManifestWallTimeP50 == 0) {
        return ""
    }

    var leftCol, rightCol []string

    // === LEFT COLUMN: Manifest Latency ===
    if m.debugStats.ManifestWallTimeP50 > 0 {
        leftCol = append(leftCol, sectionHeaderStyle.Render("Manifest Latency *"))
        leftCol = append(leftCol,
            renderLatencyRow("P25", m.debugStats.ManifestWallTimeP25),
            renderLatencyRow("P50 (median)", m.debugStats.ManifestWallTimeP50),
            renderLatencyRow("P75", m.debugStats.ManifestWallTimeP75),
            renderLatencyRow("P95", m.debugStats.ManifestWallTimeP95),
            renderLatencyRow("P99", m.debugStats.ManifestWallTimeP99),
            renderLatencyRow("Max", time.Duration(m.debugStats.ManifestWallTimeMax*float64(time.Millisecond))),
        )
    } else {
        leftCol = append(leftCol, sectionHeaderStyle.Render("Manifest Latency *"))
        leftCol = append(leftCol, dimStyle.Render("  (no data)"))
    }

    // === RIGHT COLUMN: Segment Latency ===
    if m.debugStats.SegmentWallTimeP50 > 0 {
        rightCol = append(rightCol, sectionHeaderStyle.Render("Segment Latency *"))
        rightCol = append(rightCol,
            renderLatencyRow("P25", m.debugStats.SegmentWallTimeP25),
            renderLatencyRow("P50 (median)", m.debugStats.SegmentWallTimeP50),
            renderLatencyRow("P75", m.debugStats.SegmentWallTimeP75),
            renderLatencyRow("P95", m.debugStats.SegmentWallTimeP95),
            renderLatencyRow("P99", m.debugStats.SegmentWallTimeP99),
            renderLatencyRow("Max", time.Duration(m.debugStats.SegmentWallTimeMax*float64(time.Millisecond))),
        )
    } else {
        rightCol = append(rightCol, sectionHeaderStyle.Render("Segment Latency *"))
        rightCol = append(rightCol, dimStyle.Render("  (no data)"))
    }

    // Render two columns side-by-side
    // Available width: m.width - 2 (borders) - 2 (padding) = m.width - 4
    twoColContent := renderTwoColumns(leftCol, rightCol, m.width-4)

    // Note about accurate timestamps
    note := dimStyle.Render("* Using accurate FFmpeg timestamps")

    content := lipgloss.JoinVertical(lipgloss.Left, twoColContent, note)

    return boxStyle.Width(m.width - 2).Render(content)
}
```

**Action**: Replace entire function with two-column layout implementation.

---

## Test Implementation

### Test File 1: `internal/parser/debug_events_test.go`

#### Test 1.1: Segment Percentiles (P25, P50, P75, P95, P99)

**Function Name**: `TestDebugEventParser_Percentiles`
**Type**: Table-driven test
**Location**: Add to existing test file

**Test Cases**:
1. Uniform distribution (100 samples: 1ms, 2ms, ..., 100ms)
2. Exponential distribution (many fast, few slow)
3. Single value
4. Empty digest

**Verification**:
- All percentiles (P25, P50, P75, P95, P99) calculated correctly
- Values within tolerance (T-Digest approximation)

---

#### Test 1.2: Percentile Ordering

**Function Name**: `TestDebugEventParser_PercentilesOrdering`
**Type**: Unit test
**Location**: Add to existing test file

**Verification**:
- P25 ≤ P50 ≤ P75 ≤ P95 ≤ P99 for both segments and manifests

---

#### Test 1.3: Manifest Latency Tracking

**Function Name**: `TestDebugEventParser_ManifestLatency`
**Type**: Table-driven test
**Location**: Add to existing test file

**Test Cases**:
1. Single manifest to segment (1ms latency)
2. Multiple manifests (2 manifests, 1ms each)
3. Manifest with delay (49ms latency)
4. Empty (no manifests)

**Verification**:
- Manifest count correct
- P50 and P95 calculated correctly
- Latency matches expected values

---

#### Test 1.4: Manifest Percentiles

**Function Name**: `TestDebugEventParser_ManifestLatencyPercentiles`
**Type**: Unit test
**Location**: Add to existing test file

**Test Data**: 100 manifests with varying latencies (1ms, 2ms, ..., 100ms)

**Verification**:
- All percentiles (P25, P50, P75, P95, P99) calculated correctly
- Values within tolerance

---

### Test File 2: `internal/stats/aggregator_test.go`

#### Test 2.1: Segment Percentile Aggregation

**Function Name**: `TestStatsAggregator_AggregatePercentiles`
**Type**: Table-driven test
**Location**: Add to existing test file

**Test Cases**:
1. Single client
2. Multiple clients (max aggregation)
3. Zero values

**Verification**:
- P25, P50, P75, P95, P99 aggregated correctly
- Max aggregation strategy (worst-case across clients)

---

#### Test 2.2: Manifest Latency Aggregation

**Function Name**: `TestStatsAggregator_AggregateManifestLatency`
**Type**: Table-driven test
**Location**: Add to existing test file

**Test Cases**:
1. Single client
2. Multiple clients (max aggregation)
3. Zero values

**Verification**:
- Manifest count aggregated correctly
- Min/Max/Avg aggregated correctly
- Percentiles aggregated correctly (max aggregation)

---

### Test File 3: `internal/tui/view_test.go`

#### Test 3.1: Two-Column Layout

**Function Name**: `TestRenderLatencyStats_TwoColumns`
**Type**: Unit test
**Location**: Add to existing test file or create new

**Verification**:
- Both column headers present ("Manifest Latency *", "Segment Latency *")
- Column separator (`│`) present
- Manifest Latency appears before Segment Latency (left column)
- All percentiles (P25, P50, P75, P95, P99, Max) present in both columns

---

#### Test 3.2: Empty Stats Handling

**Function Name**: `TestRenderLatencyStats_EmptyStats`
**Type**: Unit test
**Location**: Add to existing test file or create new

**Verification**:
- Empty output for nil stats
- "(no data)" message when one column has no data

---

## Verification Checklist

### Phase 1: Segment Latency P25/P75

- [ ] `DebugStats` struct updated with P25 and P75 fields
- [ ] `Stats()` method calculates P25 and P75
- [ ] `DebugStatsAggregate` struct updated with P25 and P75 fields
- [ ] `Aggregate()` method aggregates P25 and P75
- [ ] `client_manager.go` updated (if needed)
- [ ] Unit tests pass: `TestDebugEventParser_Percentiles`
- [ ] Unit tests pass: `TestDebugEventParser_PercentilesOrdering`
- [ ] Unit tests pass: `TestStatsAggregator_AggregatePercentiles`
- [ ] No race conditions: `go test ./internal/parser/... -race`
- [ ] No race conditions: `go test ./internal/stats/... -race`

### Phase 2: Manifest Latency Tracking

- [ ] `DebugEventParser` struct updated with manifest tracking fields
- [ ] `NewDebugEventParser()` initializes manifest tracking
- [ ] `handleHLSRequest()` completes pending manifests
- [ ] `handlePlaylistOpen()` tracks manifest start
- [ ] `DebugStats` struct updated with manifest latency fields
- [ ] `Stats()` method calculates manifest percentiles
- [ ] `DebugStatsAggregate` struct updated with manifest latency fields
- [ ] `Aggregate()` method aggregates manifest latency
- [ ] `client_manager.go` updated (if needed)
- [ ] Unit tests pass: `TestDebugEventParser_ManifestLatency`
- [ ] Unit tests pass: `TestDebugEventParser_ManifestLatencyPercentiles`
- [ ] Unit tests pass: `TestStatsAggregator_AggregateManifestLatency`
- [ ] No race conditions: `go test ./internal/parser/... -race`
- [ ] No race conditions: `go test ./internal/stats/... -race`

### Phase 3: TUI Dashboard

- [ ] `renderLatencyStats()` updated to two-column layout
- [ ] Manifest Latency appears in left column
- [ ] Segment Latency appears in right column
- [ ] All percentiles (P25, P50, P75, P95, P99, Max) displayed in both columns
- [ ] Column separator visible
- [ ] Empty stats handled gracefully
- [ ] Unit tests pass: `TestRenderLatencyStats_TwoColumns`
- [ ] Unit tests pass: `TestRenderLatencyStats_EmptyStats`
- [ ] Visual verification: Run application and verify dashboard display

### Integration Testing

- [ ] Run application: `./bin/go-ffmpeg-hls-swarm -tui -clients 1000 -ramp-rate 1000 http://10.177.0.10:17080/stream.m3u8`
- [ ] Verify manifest latency values are reasonable (typically < 100ms)
- [ ] Verify segment latency values are reasonable (typically > 100ms)
- [ ] Verify percentiles are in ascending order (P25 < P50 < P75 < P95 < P99 < Max)
- [ ] Verify two-column layout displays correctly
- [ ] Verify no performance regression (memory, CPU)
- [ ] Verify no crashes or panics

---

## References

### Design Documents
- `docs/SEGMENT_LATENCY_P25_P75_DESIGN.md` - Segment latency P25/P75 design
- `docs/MANIFEST_LATENCY_TRACKING_DESIGN.md` - Manifest latency tracking design

### Source Files
- `internal/parser/debug_events.go` - Core latency tracking
- `internal/stats/aggregator.go` - Stats aggregation
- `internal/orchestrator/client_manager.go` - Client aggregation (verify)
- `internal/tui/view.go` - TUI dashboard rendering

### Test Files
- `internal/parser/debug_events_test.go` - Parser tests
- `internal/stats/aggregator_test.go` - Aggregator tests
- `internal/tui/view_test.go` - TUI tests

### Testdata Files
- `testdata/ffmpeg_timestamped_1.txt` - Timestamped FFmpeg output
- `testdata/ffmpeg_timestamped_2.txt` - Timestamped FFmpeg output
- `testdata/ffmpeg_debug_comprehensive.txt` - Comprehensive debug output
- `testdata/ffmpeg_debug_output.txt` - Debug output sample

---

## Implementation Notes

### Order of Operations

1. **Phase 1 First**: Add P25/P75 to segments (simpler, builds on existing)
2. **Phase 2 Second**: Add manifest tracking (new feature, similar pattern)
3. **Phase 3 Last**: Update TUI (combines both features)

### Testing Strategy

1. **Unit Tests First**: Write tests before implementation (TDD approach)
2. **Incremental Testing**: Test each phase before moving to next
3. **Integration Testing**: Test complete feature with real application

### Performance Considerations

- **Memory**: ~10KB per T-Digest (constant, regardless of sample count)
- **CPU**: Minimal increase (additional `Quantile()` calls)
- **Lock Contention**: No change (same mutex pattern)

### Thread Safety

- All T-Digest operations protected by mutexes
- Same pattern as existing segment latency tracking
- No additional synchronization needed

---

## Summary

This implementation plan combines two features:
1. **Segment Latency P25/P75**: Adds quartile percentiles to existing segment tracking
2. **Manifest Latency Tracking**: Adds complete manifest latency tracking with all percentiles

**Total Files Modified**: 4
- `internal/parser/debug_events.go` (main changes)
- `internal/stats/aggregator.go` (aggregation)
- `internal/orchestrator/client_manager.go` (verify/optional)
- `internal/tui/view.go` (dashboard)

**Total Tests Added/Updated**: 8
- 4 parser tests (segment + manifest)
- 2 aggregator tests (segment + manifest)
- 2 TUI tests (two-column layout)

**Estimated Implementation Time**: 4-6 hours
- Phase 1: 1-2 hours
- Phase 2: 2-3 hours
- Phase 3: 1 hour
