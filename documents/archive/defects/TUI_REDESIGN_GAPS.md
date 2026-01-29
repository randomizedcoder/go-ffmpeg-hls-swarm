# TUI Dashboard Redesign Gaps

> **Date**: 2026-01-23
> **Status**: Analysis Complete - Ready for Implementation
> **Reference**: [FFMPEG_METRICS_SOCKET_DESIGN.md §11.6](FFMPEG_METRICS_SOCKET_DESIGN.md#116-tui-dashboard-debug-metrics-panel)

---

## Executive Summary

The current TUI implementation has the basic layered structure (HLS/HTTP/TCP) but **does not match the design specification** in several critical ways:

1. **Layout**: Single-column vertical vs. two-column side-by-side
2. **Visual Indicators**: Missing emoji indicators (✅, ⚠️, 🔴, etc.)
3. **Status Indicators**: Missing health status (● Healthy, etc.)
4. **Health Bars**: Missing visual health bars (●●●●●●●●○○)
5. **Organization**: Metrics not grouped logically (Segments vs Playlists)
6. **Missing Metrics**: Sequence info, Late playlists, percentages
7. **Rate Display**: Showing "(stalled)" when data exists (bug)

---

## Detailed Gap Analysis

### 1. Layout Structure

**Design Specification** (from §11.6):
```
│ Segments                              │ Playlists                           │
│   ✅ Downloaded:  45,892  (+127/s)    │   ✅ Refreshed:   8,234  (+4.2/s)   │
│   ⚠️ Failed:          12  (0.03%)     │   ⚠️ Failed:          0  (0.00%)    │
```

**Current Implementation**:
- Single-column vertical layout
- All metrics stacked vertically
- No side-by-side grouping

**Required Change**: Implement two-column layout using `lipgloss.JoinHorizontal()` with proper width calculation.

---

### 2. Visual Indicators (Emojis)

**Design Specification**:
- ✅ Success indicators
- ⚠️ Warning indicators
- 🔴 Critical indicators
- ⏩ Expired indicators
- ⏱️ Timing indicators
- ⏰ Late indicators

**Current Implementation**:
- No emoji indicators
- Only text labels

**Required Change**: Add emoji prefixes to all metrics as per design.

---

### 3. Status Indicators

**Design Specification**:
```
│   Status:       ● Healthy           │
```

**Current Implementation**:
- No status indicators
- No health assessment per layer

**Required Change**: Calculate and display health status for each layer:
- HLS: Healthy/Degraded/Unhealthy/Critical
- HTTP: Healthy/Degraded/Unhealthy/Critical
- TCP: Healthy/Degraded/Unhealthy/Critical

---

### 4. Visual Health Bars

**Design Specification**:
```
│   Health:    ●●●●●●●●○○  99.2%        │
```

**Current Implementation**:
- Only percentage shown
- No visual bar representation

**Required Change**: Add visual health bar using filled/empty circles (●/○) representing percentage.

---

### 5. Metric Organization

**Design Specification**:
- **HLS Layer**: Segments (left) | Playlists (right)
- **HTTP Layer**: Requests (left) | Errors (right)
- **TCP Layer**: Connections (left) | Connect Latency (right)

**Current Implementation**:
- All metrics in single vertical column
- No logical grouping

**Required Change**: Reorganize into two-column layout with proper grouping.

---

### 6. Missing Metrics

**Design Specification** includes:
- Sequence info: "Current: 45892   Skips: 3"
- Late playlists: "⏰ Late: 12 (0.4%)"
- Percentage calculations for all error metrics
- Min/Max timing values

**Current Implementation**:
- Missing sequence tracking
- Missing "late" playlist calculation
- Some percentages missing

**Required Change**: Add missing metrics to `DebugStatsAggregate` and display them.

---

### 7. Rate Display Bug

**Observed Issue** (from screenshot):
- Many counters show "(stalled)" even when data exists
- Example: "Segments Downloaded: 285 ((stalled))"
- This suggests rate calculation is broken or not updating

**Required Investigation**:
1. Check `formatSuccessRate()` function
2. Verify `InstantSegmentsRate` is being calculated correctly
3. Check if rate snapshot mechanism is working
4. Verify TUI tick is calling `GetDebugStats()` regularly

---

### 8. Dashboard Header

**Design Specification**:
```
│ Origin Load Test Dashboard          Timing: ✅ FFmpeg Timestamps (98.2%)    │
```

**Current Implementation**:
- Different header format
- No timing indicator

**Required Change**: Add timing indicator showing percentage of events with FFmpeg timestamps.

---

## Implementation Plan

### Phase 1: Fix Rate Calculation (Critical) ✅ COMPLETED
1. ✅ Investigated why rates show "(stalled)" - First tick has no previous snapshot
2. ✅ Fixed rate display - Shows "(calculating...)" when data exists but no rate yet
3. ✅ Updated formatSuccessRate() to accept count parameter
4. ✅ Updated all calls to formatSuccessRate() in view.go
5. ✅ Updated tests to match new signature

### Phase 2: Two-Column Layout (High Priority) ✅ COMPLETED
1. ✅ Created `renderTwoColumns()` helper function
2. ✅ Reorganized HLS layer (Segments | Playlists)
3. ✅ Reorganized HTTP layer (Requests | Errors)
4. ✅ Reorganized TCP layer (Connections | Latency)
5. ✅ Added emoji indicators to all metrics
6. ✅ Added visual health bar for TCP Health Ratio
7. ✅ Added status indicators for HTTP layer

### Phase 3: Visual Enhancements (Medium Priority)
1. Add emoji indicators to all metrics
2. Add status indicators per layer
3. Add visual health bars
4. Add dashboard header with timing indicator

### Phase 4: Missing Metrics (Medium Priority)
1. Add sequence tracking to DebugStats
2. Add "late" playlist calculation
3. Add percentage calculations for errors
4. Display all missing metrics

---

## Files to Modify

1. **`internal/tui/view.go`**:
   - Rewrite `renderHLSLayer()` with two-column layout
   - Rewrite `renderHTTPLayer()` with two-column layout
   - Rewrite `renderTCPLayer()` with two-column layout
   - Add helper functions for emoji indicators, status indicators, health bars

2. **`internal/stats/aggregator.go`** or **`internal/stats/client_stats.go`**:
   - Add missing metrics (sequence, late playlists, etc.)

3. **`internal/orchestrator/client_manager.go`**:
   - Fix rate calculation if issue is in snapshot mechanism

4. **`internal/tui/model.go`**:
   - Add timing indicator calculation
   - Fix TUI tick if not calling GetDebugStats() regularly

---

## Success Criteria

✅ **Layout**: Two-column side-by-side layout matches design
✅ **Visuals**: All emoji indicators present
✅ **Status**: Health status shown for each layer
✅ **Health Bars**: Visual health bars displayed
✅ **Rates**: All rates showing correctly (not "(stalled)" when data exists)
✅ **Metrics**: All metrics from design specification displayed
✅ **Header**: Dashboard header with timing indicator

---

## References

- [FFMPEG_METRICS_SOCKET_DESIGN.md §11.6](FFMPEG_METRICS_SOCKET_DESIGN.md#116-tui-dashboard-debug-metrics-panel) - Design specification
- [TUI_DEFECTS.md](TUI_DEFECTS.md) - Original defect tracking
- [FFMPEG_METRICS_SOCKET_IMPLEMENTATION_LOG.md](FFMPEG_METRICS_SOCKET_IMPLEMENTATION_LOG.md) - Implementation history
