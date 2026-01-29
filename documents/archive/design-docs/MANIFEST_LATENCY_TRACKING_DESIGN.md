# Manifest Latency Tracking Design

## Table of Contents

1. [Overview](#overview)
2. [Testdata Analysis](#testdata-analysis)
3. [Current State](#current-state)
4. [Design Changes](#design-changes)
5. [Files to Update](#files-to-update)
6. [Implementation Details](#implementation-details)
7. [Test Plan](#test-plan)
8. [TUI Dashboard Updates](#tui-dashboard-updates)
9. [Verification Steps](#verification-steps)

---

## Overview

This document describes the design for adding manifest (playlist) download latency tracking with percentiles (P25, P50, P75, P95, P99, Max), similar to the existing segment latency tracking system.

### Goals

- Track manifest download wall time (time from request start to completion)
- Calculate percentiles (P25, P50, P75, P95, P99) using T-Digest
- Display manifest latency metrics in the TUI dashboard
- Maintain consistency with segment latency tracking implementation
- Ensure thread-safety and performance characteristics remain unchanged

### Benefits

- **Complete Latency Picture**: Track both segment and manifest download times
- **Network Diagnostics**: Manifest latency helps identify CDN/network issues
- **Load Testing Insights**: Understand manifest download performance under load
- **Consistency**: Same percentile tracking for both segments and manifests

---

## Testdata Analysis

### ✅ Testdata Availability: CONFIRMED

**Analysis of testdata files:**

1. **`testdata/ffmpeg_timestamped_1.txt`** and **`testdata/ffmpeg_timestamped_2.txt`**:
   - ✅ Contains timestamped manifest open events: `[AVFormatContext @ ...] Opening '...stream.m3u8' for reading`
   - ✅ Contains timestamped HTTP request events: `[http @ ...] request: GET /stream.m3u8 HTTP/1.1`
   - ✅ Contains subsequent segment requests that indicate manifest completion

2. **`testdata/ffmpeg_debug_comprehensive.txt`**:
   - ✅ Contains multiple manifest refresh cycles
   - ✅ Shows pattern: Manifest open → HTTP request → Format probed → Segment requests

3. **`testdata/ffmpeg_debug_output.txt`**:
   - ✅ Contains manifest refresh events with timing

### Timing Pattern Observed

From `testdata/ffmpeg_timestamped_1.txt`:

```
2026-01-23 08:12:29.601 [AVFormatContext @ 0x55813302c900] Opening 'http://10.177.0.10:17080/stream.m3u8' for reading
2026-01-23 08:12:29.601 [http @ 0x55813302d400] request: GET /stream.m3u8 HTTP/1.1
2026-01-23 08:12:29.601 [hls @ 0x55813302c900] Format hls probed with size=2048 and score=100
2026-01-23 08:12:29.601 [hls @ 0x55813302c900] Skip ('#EXT-X-VERSION:3')
2026-01-23 08:12:29.602 [hls @ 0x55813302c900] HLS request for url 'http://10.177.0.10:17080/seg38012.ts', offset 0, playlist 0
```

**Pattern:**
1. Manifest open event (start time)
2. HTTP request event (confirms request sent)
3. Format probed / Skip events (manifest parsing begins)
4. First segment request (manifest fully downloaded and parsed - completion time)

### Completion Detection Strategy

**Option 1: First Segment Request After Manifest Open** ✅ **RECOMMENDED**
- **Start**: `[AVFormatContext @ ...] Opening '...m3u8'` or `[hls @ ...] Opening '...m3u8'`
- **End**: First `[hls @ ...] HLS request for url '...seg*.ts'` after manifest open
- **Rationale**: Segment request indicates manifest is fully downloaded and parsed
- **Accuracy**: High - segment request only happens after manifest is ready

**Option 2: Format Probed Event**
- **Start**: Manifest open
- **End**: `Format hls probed with size=...`
- **Rationale**: Indicates manifest download complete and format detected
- **Accuracy**: Medium - may occur slightly before parsing complete

**Option 3: HTTP Request to First Segment Request**
- **Start**: `[http @ ...] request: GET /stream.m3u8`
- **End**: First segment request
- **Rationale**: Measures HTTP-level latency
- **Accuracy**: Medium - doesn't include parsing time

**Decision**: Use **Option 1** (Manifest Open → First Segment Request) as it provides the most complete picture of manifest processing time, including download and parsing.

---

## Current State

### Segment Latency Tracking (Reference Implementation)

**Current Implementation:**
- ✅ T-Digest for percentile calculation
- ✅ Wall time tracking (request start → completion)
- ✅ Percentiles: P50, P95, P99 (P25, P75 being added)
- ✅ Min, Max, Avg statistics
- ✅ Uses accurate FFmpeg timestamps

**Code Location:**
- `internal/parser/debug_events.go`: `handleHLSRequest()` tracks segment wall times
- `internal/stats/aggregator.go`: Aggregates segment percentiles
- `internal/tui/view.go`: Displays segment latency in dashboard

### Manifest/Playlist Tracking (Current)

**What's Tracked:**
- ✅ Count of playlist refreshes
- ✅ Count of failed playlist refreshes
- ✅ Playlist jitter (interval between refreshes, not download latency)
- ❌ **NO wall time tracking for manifest downloads**
- ❌ **NO percentiles for manifest download latency**
- ❌ **NO Min/Max/Avg for manifest download latency**

**Code Location:**
- `internal/parser/debug_events.go`: `handlePlaylistOpen()` only tracks jitter
- `internal/stats/aggregator.go`: Only aggregates jitter metrics
- `internal/tui/view.go`: Only displays jitter in "Playlists" section

---

## Design Changes

### Summary of Changes

1. **Add manifest wall time tracking** to `DebugEventParser`
   - Track pending manifest requests (similar to `pendingSegments`)
   - Complete manifest when first segment request appears after manifest open
   - Add T-Digest for manifest wall time percentiles

2. **Add manifest latency fields** to `DebugStats` struct
   - Add P25, P50, P75, P95, P99, Min, Max, Avg fields

3. **Add manifest latency fields** to `DebugStatsAggregate` struct
   - Same fields as `DebugStats`

4. **Update aggregation logic** to aggregate manifest percentiles
   - Use max aggregation (worst-case across clients)

5. **Update TUI dashboard** to display manifest latency
   - Update `renderLatencyStats()` to use two-column layout
   - Manifest Latency on left, Segment Latency on right

### Completion Detection Logic

```go
// When manifest opens:
pendingManifests[url] = now

// When first segment request appears after manifest open:
if len(pendingManifests) > 0 {
    // Complete oldest pending manifest
    wallTime := now - manifestStartTime
    // Add to T-Digest, update stats
}
```

**Edge Cases:**
- Multiple manifest opens before first segment: Complete oldest manifest
- No segment request after manifest open: Clean up after timeout (60s)
- Manifest open but no segments: Track as failed (already handled by `PlaylistFailedCount`)

---

## Files to Update

### 1. `internal/parser/debug_events.go`

**Purpose**: Core manifest latency tracking and percentile calculation

#### Changes:

**Line ~166-180**: Add manifest wall time tracking fields to `DebugEventParser` struct
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

**Line ~262**: Initialize manifest tracking in `NewDebugEventParser()`
```go
manifestWallTimeDigest: tdigest.NewWithCompression(100), // ~100 centroids, ~10KB
pendingManifests: make(map[string]time.Time),
```

**Line ~304-308**: Update `handleHLSRequest()` to complete pending manifests
```go
// Complete oldest pending manifest (if any) before starting new segment
// This indicates the manifest download is complete
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
```

**Line ~547-583**: Update `handlePlaylistOpen()` to track manifest start
```go
func (p *DebugEventParser) handlePlaylistOpen(now time.Time, url string) {
    p.playlistRefreshes.Add(1)

    // Track manifest download start time
    p.mu.Lock()
    p.pendingManifests[url] = now
    p.mu.Unlock()

    // ... existing jitter tracking code ...
}
```

**Line ~783-845**: Add manifest latency fields to `DebugStats` struct
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

**Line ~847-928**: Update `Stats()` method to calculate manifest percentiles
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

**Functions Modified**:
- `NewDebugEventParser()` (line ~240): Initialize manifest tracking
- `handleHLSRequest()` (line ~418): Complete pending manifests
- `handlePlaylistOpen()` (line ~547): Track manifest start
- `Stats()` (line ~847): Calculate manifest percentiles

---

### 2. `internal/stats/aggregator.go`

**Purpose**: Aggregate manifest latency across multiple clients

#### Changes:

**Line ~86-135**: Add manifest latency fields to `DebugStatsAggregate` struct
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

**Line ~137-460**: Update `Aggregate()` method to aggregate manifest latency
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

**Functions Modified**:
- `Aggregate()` (line ~137): Add manifest latency aggregation

---

### 3. `internal/orchestrator/client_manager.go`

**Purpose**: Client manager aggregation (if needed)

#### Changes:

**Line ~580-595**: Add manifest percentile aggregation (if this file also aggregates percentiles)

**Note**: Verify if `client_manager.go` needs updates based on how it aggregates `DebugStats`.

**Functions Modified**:
- `AggregateDebugStats()` or similar (if exists): Add manifest percentile aggregation

---

### 4. `internal/tui/view.go`

**Purpose**: TUI dashboard rendering

#### Changes:

**Line ~242-268**: Update `renderLatencyStats()` to use two-column layout
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

**Line ~108-138**: No changes needed - `renderLatencyStats()` now includes both latencies

**Functions Modified**:
- `renderLatencyStats()` (line ~246): Update to two-column layout with Manifest (left) and Segment (right)

---

## Implementation Details

### Completion Detection

**Strategy**: Complete manifest when first segment request appears after manifest open.

**Rationale**:
- Segment request only happens after manifest is fully downloaded and parsed
- Provides complete picture of manifest processing time (download + parsing)
- Consistent with how segment completion is detected (next segment request)

**Edge Cases**:
1. **Multiple manifests before segment**: Complete oldest manifest
2. **No segment after manifest**: Clean up after 60s timeout (similar to segments)
3. **Manifest open but no segments**: Already tracked as `PlaylistFailedCount`

### T-Digest Quantile Calculation

Same as segment latency:
- `Quantile(0.25)` for P25
- `Quantile(0.50)` for P50
- `Quantile(0.75)` for P75
- `Quantile(0.95)` for P95
- `Quantile(0.99)` for P99

### Thread Safety

- **Reading** (in `Stats()`): Locked before calling `Quantile()`
- **Writing** (in `handleHLSRequest()` and `handlePlaylistOpen()`): Locked before calling `Add()`
- Uses same mutex pattern as segment latency tracking

### Aggregation Strategy

**Max Aggregation** (worst-case across clients):
- If client A has P50=10ms and client B has P50=15ms, aggregate P50=15ms
- Shows worst-case performance across all clients
- Appropriate for load testing scenarios

### Performance Impact

- **Memory**: ~10KB per T-Digest (constant, regardless of sample count)
- **CPU**: Minimal increase (additional `Quantile()` calls per `Stats()` invocation)
- **Lock Contention**: No change (same mutex pattern, minimal lock duration)

---

## Test Plan

### Test Files to Create/Update

#### 1. `internal/parser/debug_events_test.go` (Update Existing)

**Test Function**: `TestDebugEventParser_ManifestLatency`

**Purpose**: Verify manifest latency tracking and percentile calculation

**Table-Driven Test Cases**:

```go
func TestDebugEventParser_ManifestLatency(t *testing.T) {
    tests := []struct {
        name       string
        events     []struct {
            eventType string // "manifest_open", "segment_request"
            timestamp  time.Time
            url       string
        }
        wantCount  int64
        wantP50    time.Duration
        wantP95    time.Duration
        tolerance  time.Duration
    }{
        {
            name: "single_manifest_to_segment",
            events: []struct {
                eventType string
                timestamp time.Time
                url       string
            }{
                {"manifest_open", time.Date(2026, 1, 23, 8, 12, 29, 601000000, time.UTC), "stream.m3u8"},
                {"segment_request", time.Date(2026, 1, 23, 8, 12, 29, 602000000, time.UTC), "seg001.ts"},
            },
            wantCount: 1,
            wantP50:   1 * time.Millisecond,
            wantP95:   1 * time.Millisecond,
            tolerance: 0,
        },
        {
            name: "multiple_manifests",
            events: []struct {
                eventType string
                timestamp time.Time
                url       string
            }{
                {"manifest_open", time.Date(2026, 1, 23, 8, 12, 29, 601000000, time.UTC), "stream.m3u8"},
                {"segment_request", time.Date(2026, 1, 23, 8, 12, 29, 602000000, time.UTC), "seg001.ts"},
                {"manifest_open", time.Date(2026, 1, 23, 8, 12, 31, 616000000, time.UTC), "stream.m3u8"},
                {"segment_request", time.Date(2026, 1, 23, 8, 12, 31, 617000000, time.UTC), "seg002.ts"},
            },
            wantCount: 2,
            wantP50:   1 * time.Millisecond,
            wantP95:   1 * time.Millisecond,
            tolerance: 0,
        },
        {
            name: "manifest_with_delay",
            events: []struct {
                eventType string
                timestamp time.Time
                url       string
            }{
                {"manifest_open", time.Date(2026, 1, 23, 8, 12, 29, 601000000, time.UTC), "stream.m3u8"},
                {"segment_request", time.Date(2026, 1, 23, 8, 12, 29, 650000000, time.UTC), "seg001.ts"}, // 49ms delay
            },
            wantCount: 1,
            wantP50:   49 * time.Millisecond,
            wantP95:   49 * time.Millisecond,
            tolerance: 0,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            p := NewDebugEventParser(1, nil)

            for _, event := range tt.events {
                switch event.eventType {
                case "manifest_open":
                    p.handlePlaylistOpen(event.timestamp, event.url)
                case "segment_request":
                    p.handleHLSRequest(event.timestamp, event.url)
                }
            }

            stats := p.Stats()

            if stats.ManifestCount != tt.wantCount {
                t.Errorf("ManifestCount = %d, want %d", stats.ManifestCount, tt.wantCount)
            }

            if !withinTolerance(stats.ManifestWallTimeP50, tt.wantP50, tt.tolerance) {
                t.Errorf("P50 = %v, want %v (tolerance: %v)",
                    stats.ManifestWallTimeP50, tt.wantP50, tt.tolerance)
            }

            if !withinTolerance(stats.ManifestWallTimeP95, tt.wantP95, tt.tolerance) {
                t.Errorf("P95 = %v, want %v (tolerance: %v)",
                    stats.ManifestWallTimeP95, tt.wantP95, tt.tolerance)
            }
        })
    }
}
```

**Test Function**: `TestDebugEventParser_ManifestLatencyPercentiles`

**Purpose**: Verify all percentiles (P25, P50, P75, P95, P99) are calculated correctly

```go
func TestDebugEventParser_ManifestLatencyPercentiles(t *testing.T) {
    p := NewDebugEventParser(1, nil)

    baseTime := time.Date(2026, 1, 23, 8, 12, 29, 0, time.UTC)

    // Add 100 manifests with varying latencies: 1ms, 2ms, ..., 100ms
    for i := 1; i <= 100; i++ {
        manifestTime := baseTime.Add(time.Duration(i) * time.Millisecond)
        segmentTime := manifestTime.Add(time.Duration(i) * time.Millisecond)

        p.handlePlaylistOpen(manifestTime, "stream.m3u8")
        p.handleHLSRequest(segmentTime, fmt.Sprintf("seg%03d.ts", i))
    }

    stats := p.Stats()

    // Verify percentiles (with tolerance for T-Digest approximation)
    if stats.ManifestWallTimeP25 < 20*time.Millisecond || stats.ManifestWallTimeP25 > 30*time.Millisecond {
        t.Errorf("P25 = %v, want ~25ms", stats.ManifestWallTimeP25)
    }
    if stats.ManifestWallTimeP50 < 45*time.Millisecond || stats.ManifestWallTimeP50 > 55*time.Millisecond {
        t.Errorf("P50 = %v, want ~50ms", stats.ManifestWallTimeP50)
    }
    if stats.ManifestWallTimeP75 < 70*time.Millisecond || stats.ManifestWallTimeP75 > 80*time.Millisecond {
        t.Errorf("P75 = %v, want ~75ms", stats.ManifestWallTimeP75)
    }
    if stats.ManifestWallTimeP95 < 90*time.Millisecond || stats.ManifestWallTimeP95 > 100*time.Millisecond {
        t.Errorf("P95 = %v, want ~95ms", stats.ManifestWallTimeP95)
    }
    if stats.ManifestWallTimeP99 < 95*time.Millisecond || stats.ManifestWallTimeP99 > 100*time.Millisecond {
        t.Errorf("P99 = %v, want ~99ms", stats.ManifestWallTimeP99)
    }
}
```

---

#### 2. `internal/stats/aggregator_test.go` (Update Existing)

**Test Function**: `TestStatsAggregator_AggregateManifestLatency`

**Purpose**: Verify manifest latency aggregation across multiple clients

```go
func TestStatsAggregator_AggregateManifestLatency(t *testing.T) {
    agg := NewStatsAggregator(0.01)

    // Create mock DebugStats for multiple clients
    stats1 := &parser.DebugStats{
        ManifestCount: 10,
        ManifestAvgMs: 5.0,
        ManifestMinMs: 1.0,
        ManifestMaxMs: 10.0,
        ManifestWallTimeP25: 2 * time.Millisecond,
        ManifestWallTimeP50: 5 * time.Millisecond,
        ManifestWallTimeP75: 7 * time.Millisecond,
        ManifestWallTimeP95: 9 * time.Millisecond,
        ManifestWallTimeP99: 10 * time.Millisecond,
    }

    stats2 := &parser.DebugStats{
        ManifestCount: 20,
        ManifestAvgMs: 8.0,
        ManifestMinMs: 2.0,
        ManifestMaxMs: 15.0,
        ManifestWallTimeP25: 4 * time.Millisecond,
        ManifestWallTimeP50: 8 * time.Millisecond,
        ManifestWallTimeP75: 12 * time.Millisecond,
        ManifestWallTimeP95: 14 * time.Millisecond,
        ManifestWallTimeP99: 15 * time.Millisecond,
    }

    // Aggregate (adjust based on actual API)
    agg.AggregateDebugStats(stats1)
    agg.AggregateDebugStats(stats2)

    result := agg.Aggregate()

    // Verify aggregation
    if result.ManifestCount != 30 {
        t.Errorf("ManifestCount = %d, want 30", result.ManifestCount)
    }

    // Verify max aggregation for percentiles
    if result.ManifestWallTimeP50 != 8*time.Millisecond {
        t.Errorf("P50 = %v, want 8ms (max aggregation)", result.ManifestWallTimeP50)
    }
    if result.ManifestWallTimeP95 != 14*time.Millisecond {
        t.Errorf("P95 = %v, want 14ms (max aggregation)", result.ManifestWallTimeP95)
    }
}
```

---

#### 3. `internal/tui/view_test.go` (Update Existing or Create)

**Test Function**: `TestRenderLatencyStats_TwoColumns`

**Purpose**: Verify TUI rendering includes both manifest and segment latency in two columns

```go
func TestRenderLatencyStats_TwoColumns(t *testing.T) {
    model := Model{
        width: 80,
        debugStats: &stats.DebugStatsAggregate{
            // Manifest latency
            ManifestWallTimeP25: 1 * time.Millisecond,
            ManifestWallTimeP50: 2 * time.Millisecond,
            ManifestWallTimeP75: 3 * time.Millisecond,
            ManifestWallTimeP95: 5 * time.Millisecond,
            ManifestWallTimeP99: 10 * time.Millisecond,
            ManifestWallTimeMax: 15.0, // milliseconds
            // Segment latency
            SegmentWallTimeP25: 1000 * time.Millisecond,
            SegmentWallTimeP50: 2000 * time.Millisecond,
            SegmentWallTimeP75: 3000 * time.Millisecond,
            SegmentWallTimeP95: 5000 * time.Millisecond,
            SegmentWallTimeP99: 10000 * time.Millisecond,
            SegmentWallTimeMax: 15000.0, // milliseconds
        },
    }

    output := model.renderLatencyStats()

    // Verify both column headers are present
    if !strings.Contains(output, "Manifest Latency") {
        t.Error("output missing Manifest Latency header")
    }
    if !strings.Contains(output, "Segment Latency") {
        t.Error("output missing Segment Latency header")
    }

    // Verify column separator is present
    if !strings.Contains(output, "│") {
        t.Error("output missing column separator")
    }

    // Verify manifest percentiles are present
    manifestP50Idx := strings.Index(output, "Manifest Latency")
    segmentP50Idx := strings.Index(output, "Segment Latency")
    if manifestP50Idx >= segmentP50Idx {
        t.Error("Manifest Latency should appear before Segment Latency (left column)")
    }

    // Verify all percentiles are present
    for _, percentile := range []string{"P25", "P50", "P75", "P95", "P99", "Max"} {
        if !strings.Contains(output, percentile) {
            t.Errorf("output missing %s", percentile)
        }
    }
}
```

---

## TUI Dashboard Updates

### Current Display

**Segment Latency Section (single column, left-justified):**
```
Segment Latency *
P50 (median):    2128 ms
P95:             7851 ms
P99:             7851 ms
Max:             7851 ms
* Using accurate FFmpeg timestamps
```

### Updated Display

**Two-Column Latency Layout:**
```
Manifest Latency *        │ Segment Latency *
P25:             1 ms     │ P25:          1050 ms
P50 (median):    2 ms     │ P50 (median): 2128 ms
P75:             3 ms     │ P75:          3200 ms
P95:             5 ms     │ P95:          7851 ms
P99:             10 ms    │ P99:          7851 ms
Max:             15 ms    │ Max:          7851 ms
* Using accurate FFmpeg timestamps
```

### Layout

The latency section will use a **two-column layout**:

1. Request Statistics
2. **Latency Statistics** (two columns: Manifest on left, Segment on right)
3. Playback Health
4. Errors

### Visual Design

- **Two-Column Layout**: Uses `renderTwoColumns()` helper function (same as HLS/HTTP/TCP layers)
- **Left Column**: Manifest Latency (P25, P50, P75, P95, P99, Max)
- **Right Column**: Segment Latency (P25, P50, P75, P95, P99, Max)
- **Separator**: ` │ ` (vertical bar with spaces) between columns
- **Header**: Each column has its own header ("Manifest Latency *" and "Segment Latency *")
- **Note**: Single note at bottom applies to both columns
- **Column Width**: Fixed width columns (42 chars each) to match other two-column sections
- **Order**: Ascending percentile order (P25 → P50 → P75 → P95 → P99 → Max) in both columns
- **Fallback**: If one column has no data, show "(no data)" message

---

## Verification Steps

### 1. Unit Tests

```bash
# Test parser manifest latency calculations
go test ./internal/parser/... -v -run TestDebugEventParser_ManifestLatency

# Test aggregator manifest latency aggregation
go test ./internal/stats/... -v -run TestStatsAggregator_AggregateManifestLatency

# Test TUI rendering
go test ./internal/tui/... -v -run TestRenderLatencyStats
```

### 2. Integration Test

Run the application with a test stream:

```bash
./bin/go-ffmpeg-hls-swarm -tui -clients 1000 -ramp-rate 1000 http://10.177.0.10:17080/stream.m3u8
```

**Verify**:
- Latency section uses two-column layout
- Manifest Latency appears in left column
- Segment Latency appears in right column
- Column separator (`│`) is visible between columns
- Percentiles are in ascending order in both columns (P25 < P50 < P75 < P95 < P99 < Max)
- Values are reasonable (manifest latency typically much smaller than segment latency)
- Formatting matches other two-column sections (HLS/HTTP/TCP layers)

### 3. Testdata Verification

Verify against existing testdata:

```bash
# Run parser tests with testdata files
go test ./internal/parser/... -v -run TestDebugEventParser
```

**Verify**:
- Manifest latency is calculated from testdata files
- Percentiles match expected values from testdata

### 4. Performance Test

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

---

## Summary

### Files Modified

1. **`internal/parser/debug_events.go`**
   - Add manifest wall time tracking fields (line ~166-180)
   - Initialize manifest tracking (line ~262)
   - Update `handleHLSRequest()` to complete manifests (line ~304-308)
   - Update `handlePlaylistOpen()` to track manifest start (line ~547-583)
   - Add manifest latency fields to `DebugStats` (line ~783-845)
   - Calculate manifest percentiles in `Stats()` (line ~847-928)

2. **`internal/stats/aggregator.go`**
   - Add manifest latency fields to `DebugStatsAggregate` (line ~86-135)
   - Aggregate manifest latency in `Aggregate()` (line ~137-460)

3. **`internal/orchestrator/client_manager.go`** (if needed)
   - Add manifest percentile aggregation

4. **`internal/tui/view.go`**
   - Update `renderLatencyStats()` to two-column layout (line ~246)
   - Manifest Latency on left, Segment Latency on right

### Tests Added

1. **`internal/parser/debug_events_test.go`**
   - `TestDebugEventParser_ManifestLatency` - Table-driven test
   - `TestDebugEventParser_ManifestLatencyPercentiles` - Percentile verification

2. **`internal/stats/aggregator_test.go`**
   - `TestStatsAggregator_AggregateManifestLatency` - Aggregation test

3. **`internal/tui/view_test.go`**
   - `TestRenderLatencyStats_TwoColumns` - TUI two-column rendering test

### Expected Outcome

After implementation:
- TUI dashboard displays manifest latency metrics: P25, P50, P75, P95, P99, Max
- All percentiles calculated from accurate FFmpeg timestamps
- Percentiles aggregated correctly across multiple clients
- Comprehensive test coverage ensures correctness
- No performance regression

### Testdata Availability: ✅ CONFIRMED

The testdata files contain all necessary timing information:
- ✅ Manifest open events with timestamps
- ✅ HTTP request events
- ✅ Subsequent segment requests (completion indicators)

**Conclusion**: Implementation is feasible and can proceed.

---

## References

- Segment Latency Design: `docs/SEGMENT_LATENCY_P25_P75_DESIGN.md`
- T-Digest Library: `github.com/influxdata/tdigest`
- Current Implementation: `internal/parser/debug_events.go`
- TUI Dashboard: `internal/tui/view.go`
- Stats Aggregation: `internal/stats/aggregator.go`
- Testdata Files: `testdata/ffmpeg_timestamped_*.txt`
