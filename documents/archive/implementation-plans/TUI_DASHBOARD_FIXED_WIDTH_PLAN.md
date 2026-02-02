# TUI Dashboard Fixed-Width Formatting Plan

> **Type**: Implementation Plan
> **Date**: 2026-01-23
> **Related Documents**:
> - [FFMPEG_METRICS_SOCKET_DESIGN.md](FFMPEG_METRICS_SOCKET_DESIGN.md) - Section 11.6 (Design Specification)
> - [FFMPEG_CLIENT_METRICS.md](FFMPEG_CLIENT_METRICS.md) - Metrics Collection Reference
> - [FFMPEG_HLS_REFERENCE.md](FFMPEG_HLS_REFERENCE.md) - Section 13 (Metrics Categories)
> - [TUI_REDESIGN_GAPS.md](TUI_REDESIGN_GAPS.md) - Current Implementation Gaps

---

## Table of Contents

1. [Executive Summary](#1-executive-summary)
2. [Current State Analysis](#2-current-state-analysis)
3. [Design Requirements](#3-design-requirements)
4. [Fixed-Width Field Strategy](#4-fixed-width-field-strategy)
5. [Field Width Calculations](#5-field-width-calculations)
6. [Right-Justification Implementation](#6-right-justification-implementation)
7. [Layout Specifications](#7-layout-specifications)
8. [Implementation Plan](#8-implementation-plan)
9. [Testing Strategy](#9-testing-strategy)

---

## 1. Executive Summary

This document plans the implementation of fixed-width, right-justified formatting for the TUI dashboard debug metrics panel to match the design specification in `FFMPEG_METRICS_SOCKET_DESIGN.md` section 11.6.

**Key Requirements:**
- **Dashboard Width**: 80 characters (fixed)
- **Field Alignment**: Right-justified numeric values
- **Stability**: Numbers must not shift left/right as values increase
- **Design Match**: Exact match to section 11.6 specification

**Benefits:**
- Smooth visual experience as metrics update
- Professional appearance matching design spec
- Easier to scan and compare values
- Consistent layout regardless of value magnitude

---

## 2. Current State Analysis

### 2.1 Current Implementation

The current TUI implementation (`internal/tui/view.go`) has:
- Basic two-column layout for HLS/HTTP/TCP layers
- Left-aligned values with variable widths
- Formatting functions that produce variable-length strings
- No fixed-width constraints

### 2.2 Current Formatting Functions

Located in `internal/tui/model.go`:

| Function | Current Behavior | Issue |
|----------|-----------------|-------|
| `formatNumber(n)` | `"45.2K"`, `"1.2M"`, `"123"` | Variable width (3-6 chars) |
| `formatBytes(n)` | `"512.34 KB"`, `"1.23 MB"` | Variable width (4-10 chars) |
| `formatRate(rate)` | `"123.4/s"`, `"1.2K/s"` | Variable width (5-8 chars) |
| `formatPercent(p)` | `"98.2%"` | Variable width (4-6 chars) |
| `formatSuccessRate(rate, count)` | `"+123/s"`, `"+1.2K/s"`, `"(stalled)"` | Variable width (9-10 chars) |

**Problem**: As numbers grow, the formatted strings change length, causing visual "jumping" in the dashboard.

### 2.3 Current Layout Issues

**Example from current code** (line 488 in `view.go`):
```go
segStyle.Render(fmt.Sprintf("  %s  (%s)", formatNumber(ds.SegmentsDownloaded), segRate))
```

**Output examples:**
```
  ✅ Downloaded:  45  (+127/s)     ← 2-digit number
  ✅ Downloaded:  285  (+127/s)     ← 3-digit number (shifts right)
  ✅ Downloaded:  2.4K  (+127/s)    ← K suffix (shifts left)
  ✅ Downloaded:  45.2K  (+127/s)  ← Decimal K (shifts left again)
```

**Result**: Visual instability as metrics update.

---

## 3. Design Requirements

### 3.1 Design Specification Reference

From `FFMPEG_METRICS_SOCKET_DESIGN.md` section 11.6:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ Origin Load Test Dashboard          Timing: ✅ FFmpeg Timestamps (98.2%)    │
├─────────────────────────────────────────────────────────────────────────────┤
│ 📺 HLS LAYER (libavformat/hls.c)                                            │
├─────────────────────────────────────────────────────────────────────────────┤
│ Segments                              │ Playlists                           │
│   ✅ Downloaded:  45,892  (+127/s)    │   ✅ Refreshed:   8,234  (+4.2/s)   │
│   ⚠️ Failed:          12  (0.03%)     │   ⚠️ Failed:          0  (0.00%)    │
│   🔴 Skipped:          2  (data loss) │   ⏱️ Jitter:      45ms avg/312ms max│
│   ⏩ Expired:         45  (fell behind)│   ⏰ Late:         12  (0.4%)       │
│                                       │                                     │
│ Segment Wall Time                     │ Sequence                            │
│   Avg: 12ms  Min: 2ms  Max: 892ms     │   Current: 45892   Skips: 3         │
├─────────────────────────────────────────────────────────────────────────────┤
│ 🌐 HTTP LAYER (libavformat/http.c)                                          │
├─────────────────────────────────────────────────────────────────────────────┤
│ Requests                              │ Errors                              │
│   ✅ Successful: 54,103  (+142/s)     │   4xx Client:       5  (0.01%)      │
│   ⚠️ Failed:         23  (0.04%)       │   5xx Server:      18  (0.03%)      │
│   🔄 Reconnects:      8               │   Error Rate:   0.04%               │
│                                       │   Status:       ● Healthy           │
├─────────────────────────────────────────────────────────────────────────────┤
│ 🔌 TCP LAYER (libavformat/network.c)                                        │
├─────────────────────────────────────────────────────────────────────────────┤
│ Connections                           │ Connect Latency                     │
│   ✅ Success:    14,523  (99.2%)      │   Avg:   0.8ms                      │
│   🚫 Refused:        48  (0.3%)       │   Min:   0.2ms                      │
│   ⏱️ Timeout:        73  (0.5%)       │   Max:  45ms                        │
│   Health:    ●●●●●●●●○○  99.2%        │   (Note: Keep-alive = few connects) │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 3.2 Key Observations from Design

1. **Numbers are right-aligned** within fixed-width fields
2. **Commas used for thousands** (e.g., `45,892` not `45.9K`)
3. **Consistent field widths** across similar metrics
4. **Rate values** use `+` prefix and fixed format
5. **Percentages** always show 2 decimal places (e.g., `0.03%`, `99.2%`)
6. **Milliseconds** use fixed format (e.g., `12ms`, `0.8ms`)

### 3.3 Field Width Requirements

Based on the design spec, we need:

| Metric Type | Example | Field Width | Justification |
|-------------|---------|-------------|---------------|
| **Large Counters** | `45,892` | 8 chars | Up to 99,999,999 (8 digits + commas) |
| **Medium Counters** | `8,234` | 7 chars | Up to 9,999,999 |
| **Small Counters** | `12` | 6 chars | Up to 999,999 |
| **Rate Values** | `+127/s` | 8 chars | `+999.9K/s` max |
| **Percentages** | `0.03%` | 7 chars | `100.00%` max |
| **Milliseconds** | `12ms` | 6 chars | `999ms` max |
| **Status Text** | `● Healthy` | 12 chars | Variable text |

---

## 4. Fixed-Width Field Strategy

### 4.1 Core Principle

**All numeric values must occupy a fixed-width field, right-aligned, regardless of actual value.**

### 4.2 Formatting Rules

#### Rule 1: Large Counters (8 characters)
- Format: Comma-separated integers (no K/M suffixes)
- Range: 0 to 99,999,999
- Example: `45,892` (8 chars), `  1,234` (8 chars, right-aligned)

#### Rule 2: Medium Counters (7 characters)
- Format: Comma-separated integers
- Range: 0 to 9,999,999
- Example: `8,234` (7 chars), `    12` (7 chars, right-aligned)

#### Rule 3: Small Counters (6 characters)
- Format: Plain integers (no commas for < 1000)
- Range: 0 to 999,999
- Example: `12` (6 chars, right-aligned), `   0` (6 chars)

#### Rule 4: Rate Values (8 characters)
- Format: `+NNN.N/s` or `+N.NK/s` or `+N.NM/s`
- Examples: `+127/s` (6 chars, pad to 8), `+1.2K/s` (7 chars, pad to 8)
- Special: `(stalled)` (9 chars, but acceptable as it's a status)

#### Rule 5: Percentages (7 characters)
- Format: `NN.NN%` (always 2 decimal places)
- Range: 0.00% to 100.00%
- Example: `  0.03%` (7 chars), ` 99.20%` (7 chars)

#### Rule 6: Milliseconds (6 characters)
- Format: `NNNms` or `N.Nms` for sub-millisecond
- Range: 0ms to 999ms (or 0.0ms to 99.9ms)
- Example: ` 12ms` (6 chars), `0.8ms` (6 chars)

### 4.3 Right-Justification Method

Use Go's `fmt` package width and alignment specifiers:

```go
// Right-align in 8-character field
fmt.Sprintf("%8s", value)

// Right-align integer with commas (custom function needed)
fmt.Sprintf("%8s", formatNumberFixed(n, 8))
```

---

## 5. Field Width Calculations

### 5.1 Dashboard Width Breakdown

**Total Width**: 80 characters

**Breakdown:**
- Box border: 2 chars (left + right)
- Box padding: 2 chars (1 char each side)
- **Available content width**: 76 characters

**Two-Column Layout:**
- Column separator: 3 chars (` │ `)
- Left column: ~36 chars
- Right column: ~36 chars
- Total: 36 + 3 + 36 = 75 chars (within 76 limit)

### 5.2 Column Content Width

**Left Column (36 chars):**
- Label prefix: `"  ✅ Downloaded:"` = 17 chars
- Value field: 8 chars (right-aligned)
- Rate field: `"  (+127/s)"` = 9 chars
- **Total**: 17 + 8 + 9 = 34 chars ✓

**Right Column (36 chars):**
- Label prefix: `"  ✅ Refreshed:"` = 16 chars
- Value field: 8 chars (right-aligned)
- Rate field: `"  (+4.2/s)"` = 9 chars
- **Total**: 16 + 8 + 9 = 33 chars ✓

### 5.3 Specific Field Widths

| Field | Width | Example (Right-Aligned) |
|-------|-------|-------------------------|
| Large counter | 8 | ` 45,892` |
| Medium counter | 7 | `  8,234` |
| Small counter | 6 | `    12` |
| Rate (`+NNN/s`) | 8 | ` +127/s` |
| Percentage | 7 | `  0.03%` |
| Milliseconds | 6 | `  12ms` |
| Status | 12 | ` ● Healthy` |

---

## 6. Right-Justification Implementation

### 6.1 New Formatting Functions

Create fixed-width formatting functions in `internal/tui/model.go`:

```go
// formatNumberFixed formats a number with commas in a fixed-width field (right-aligned).
// width: total field width (including commas and padding)
func formatNumberFixed(n int64, width int) string {
    // Format with commas
    formatted := formatNumberWithCommas(n)

    // Right-align in field
    return fmt.Sprintf("%*s", width, formatted)
}

// formatNumberWithCommas formats a number with thousand separators (no K/M suffixes).
func formatNumberWithCommas(n int64) string {
    if n < 1000 {
        return fmt.Sprintf("%d", n)
    }

    // Add commas every 3 digits
    str := fmt.Sprintf("%d", n)
    result := ""
    for i, c := range str {
        if i > 0 && (len(str)-i)%3 == 0 {
            result += ","
        }
        result += string(c)
    }
    return result
}

// formatRateFixed formats a rate in a fixed-width field (right-aligned).
func formatRateFixed(rate float64, width int) string {
    var formatted string
    if rate >= 1000 {
        formatted = fmt.Sprintf("+%.1fK/s", rate/1000)
    } else if rate >= 1 {
        formatted = fmt.Sprintf("+%.0f/s", rate)
    } else if rate > 0 {
        formatted = fmt.Sprintf("+%.1f/s", rate)
    } else {
        formatted = "(stalled)"
    }

    // Right-align in field
    return fmt.Sprintf("%*s", width, formatted)
}

// formatPercentFixed formats a percentage in a fixed-width field (right-aligned).
func formatPercentFixed(value float64, width int) string {
    formatted := fmt.Sprintf("%.2f%%", value*100)
    return fmt.Sprintf("%*s", width, formatted)
}

// formatMsFixed formats milliseconds in a fixed-width field (right-aligned).
func formatMsFixed(ms float64, width int) string {
    var formatted string
    if ms < 1.0 {
        formatted = fmt.Sprintf("%.1fms", ms)
    } else {
        formatted = fmt.Sprintf("%.0fms", ms)
    }
    return fmt.Sprintf("%*s", width, formatted)
}
```

### 6.2 Usage in View Functions

**Before** (current, variable width):
```go
segStyle.Render(fmt.Sprintf("  %s  (%s)",
    formatNumber(ds.SegmentsDownloaded),
    formatSuccessRate(ds.InstantSegmentsRate, ds.SegmentsDownloaded)))
```

**After** (fixed width, right-aligned):
```go
segStyle.Render(fmt.Sprintf("  ✅ Downloaded: %s  (%s)",
    formatNumberFixed(ds.SegmentsDownloaded, 8),
    formatRateFixed(ds.InstantSegmentsRate, 8)))
```

### 6.3 Emoji and Label Positioning

**Design Pattern:**
```
"  ✅ Downloaded: " + [8-char right-aligned value] + "  (" + [8-char rate] + ")"
```

**Implementation:**
```go
// Consistent label width for alignment
const (
    labelSegmentsDownloaded = "  ✅ Downloaded:"
    labelSegmentsFailed     = "  ⚠️ Failed:"
    labelSegmentsSkipped    = "  🔴 Skipped:"
    labelSegmentsExpired     = "  ⏩ Expired:"
    // ... etc
)

// Usage
leftCol = append(leftCol,
    lipgloss.JoinHorizontal(lipgloss.Left,
        labelStyle.Render(labelSegmentsDownloaded),
        segStyle.Render(formatNumberFixed(ds.SegmentsDownloaded, 8)),
        mutedStyle.Render("  ("),
        segStyle.Render(formatRateFixed(ds.InstantSegmentsRate, 8)),
        mutedStyle.Render(")"),
    ),
)
```

---

## 7. Layout Specifications

### 7.1 HLS Layer Layout

**Left Column: Segments (36 chars)**
```
  ✅ Downloaded: [8-char]  ([8-char])
  ⚠️ Failed:     [8-char]  ([7-char]%)
  🔴 Skipped:    [8-char]  (data loss)
  ⏩ Expired:    [8-char]  (fell behind)

Segment Wall Time
  Avg: [6-char]  Min: [6-char]  Max: [6-char]
```

**Right Column: Playlists (36 chars)**
```
  ✅ Refreshed:  [8-char]  ([8-char])
  ⚠️ Failed:     [8-char]  ([7-char]%)
  ⏱️ Jitter:     [6-char] avg/[6-char] max
  ⏰ Late:       [8-char]  ([7-char]%)

Sequence
  Current: [8-char]   Skips: [6-char]
```

### 7.2 HTTP Layer Layout

**Left Column: Requests (36 chars)**
```
  ✅ Successful: [8-char]  ([8-char])
  ⚠️ Failed:     [8-char]  ([7-char]%)
  🔄 Reconnects: [8-char]
```

**Right Column: Errors (36 chars)**
```
  4xx Client:   [8-char]  ([7-char]%)
  5xx Server:   [8-char]  ([7-char]%)
  Error Rate:   [7-char]%
  Status:       [12-char]
```

### 7.3 TCP Layer Layout

**Left Column: Connections (36 chars)**
```
  ✅ Success:    [8-char]  ([7-char]%)
  🚫 Refused:    [8-char]  ([7-char]%)
  ⏱️ Timeout:    [8-char]  ([7-char]%)
  Health:       [10-char]  [7-char]%
```

**Right Column: Connect Latency (36 chars)**
```
  Avg:   [6-char]
  Min:   [6-char]
  Max:   [6-char]

  (Note: Keep-alive = few connects)
```

### 7.4 Complete Layout Example

**80-character width dashboard:**

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ Origin Load Test Dashboard          Timing: ✅ FFmpeg Timestamps (98.2%)    │
├──────────────────────────────────────────────────────────────────────────────┤
│ 📺 HLS LAYER (libavformat/hls.c)                                             │
├──────────────────────────────────────────────────────────────────────────────┤
│ Segments                              │ Playlists                           │
│   ✅ Downloaded:  45,892  (+127/s)    │   ✅ Refreshed:   8,234  (+4.2/s)   │
│   ⚠️ Failed:          12  (0.03%)     │   ⚠️ Failed:          0  (0.00%)    │
│   🔴 Skipped:          2  (data loss)  │   ⏱️ Jitter:      45ms avg/312ms max│
│   ⏩ Expired:         45  (fell behind)│   ⏰ Late:         12  (0.4%)       │
│                                       │                                     │
│ Segment Wall Time                     │ Sequence                            │
│   Avg: 12ms  Min: 2ms  Max: 892ms     │   Current: 45892   Skips: 3         │
├──────────────────────────────────────────────────────────────────────────────┤
│ 🌐 HTTP LAYER (libavformat/http.c)                                           │
├──────────────────────────────────────────────────────────────────────────────┤
│ Requests                              │ Errors                              │
│   ✅ Successful: 54,103  (+142/s)     │   4xx Client:       5  (0.01%)      │
│   ⚠️ Failed:         23  (0.04%)      │   5xx Server:      18  (0.03%)      │
│   🔄 Reconnects:      8               │   Error Rate:   0.04%               │
│                                       │   Status:       ● Healthy           │
├──────────────────────────────────────────────────────────────────────────────┤
│ 🔌 TCP LAYER (libavformat/network.c)                                         │
├──────────────────────────────────────────────────────────────────────────────┤
│ Connections                           │ Connect Latency                     │
│   ✅ Success:    14,523  (99.2%)      │   Avg:   0.8ms                      │
│   🚫 Refused:        48  (0.3%)       │   Min:   0.2ms                      │
│   ⏱️ Timeout:        73  (0.5%)       │   Max:  45ms                        │
│   Health:    ●●●●●●●●○○  99.2%        │   (Note: Keep-alive = few connects) │
└──────────────────────────────────────────────────────────────────────────────┘
```

**Note**: Actual width is 80 chars including borders. Content width is 76 chars.

---

## 8. Implementation Plan

### 8.1 Phase 1: Formatting Functions

**File**: `internal/tui/model.go`

**Tasks**:
1. Add `formatNumberWithCommas(n int64) string`
2. Add `formatNumberFixed(n int64, width int) string`
3. Add `formatRateFixed(rate float64, width int) string`
4. Add `formatPercentFixed(value float64, width int) string`
5. Add `formatMsFixed(ms float64, width int) string`
6. Update `formatSuccessRate()` to use fixed width

**Testing**:
- Unit tests for each function
- Verify right-alignment
- Verify field width consistency
- Test edge cases (0, negative, very large numbers)

### 8.2 Phase 2: View Function Updates

**File**: `internal/tui/view.go`

**Tasks**:
1. Update `renderHLSLayer()`:
   - Replace `formatNumber()` with `formatNumberFixed(..., 8)`
   - Replace `formatSuccessRate()` with `formatRateFixed(..., 8)`
   - Replace percentage formatting with `formatPercentFixed(..., 7)`
   - Replace millisecond formatting with `formatMsFixed(..., 6)`

2. Update `renderHTTPLayer()`:
   - Same replacements as HLS layer

3. Update `renderTCPLayer()`:
   - Same replacements as HLS layer

**Testing**:
- Visual inspection of dashboard
- Verify no left/right shifting as values update
- Verify alignment matches design spec

### 8.3 Phase 3: Label Constants

**File**: `internal/tui/view.go` (or new `constants.go`)

**Tasks**:
1. Define label constants for consistent spacing:
   ```go
   const (
       labelSegmentsDownloaded = "  ✅ Downloaded:"
       labelSegmentsFailed     = "  ⚠️ Failed:"
       // ... etc
   )
   ```

2. Use constants in view functions for consistent alignment

### 8.4 Phase 4: Column Width Calculation

**File**: `internal/tui/view.go`

**Tasks**:
1. Update `renderTwoColumns()` to use fixed column widths:
   - Left column: 36 chars
   - Right column: 36 chars
   - Separator: 3 chars (` │ `)
   - Total: 75 chars (within 76-char content width)

2. Ensure proper text wrapping for long labels

### 8.5 Phase 5: Testing and Validation

**Tasks**:
1. Visual comparison with design spec
2. Test with various metric values:
   - Small numbers (0-99)
   - Medium numbers (100-9,999)
   - Large numbers (10,000-99,999,999)
   - Edge cases (0, negative if applicable)
3. Test rate calculations:
   - Stalled (0 rate)
   - Low rate (< 1/s)
   - Medium rate (1-999/s)
   - High rate (1000+/s)
4. Verify no visual "jumping" during updates

---

## 9. Testing Strategy

### 9.1 Unit Tests

**File**: `internal/tui/model_test.go`

**Test Cases**:

```go
func TestFormatNumberFixed(t *testing.T) {
    tests := []struct {
        n      int64
        width  int
        expect string
    }{
        {0, 8, "       0"},
        {12, 8, "      12"},
        {1234, 8, "   1,234"},
        {12345, 8, "  12,345"},
        {123456, 8, " 123,456"},
        {1234567, 8, "1,234,567"},
        {12345678, 8, "12,345,678"},
    }
    // ... test implementation
}

func TestFormatRateFixed(t *testing.T) {
    tests := []struct {
        rate   float64
        width  int
        expect string
    }{
        {0.0, 8, "(stalled)"},
        {0.5, 8, "  +0.5/s"},
        {12.0, 8, "   +12/s"},
        {123.4, 8, "  +123/s"},
        {1234.5, 8, "+1.2K/s"},
        {12345.6, 8, "+12.3K/s"},
    }
    // ... test implementation
}

func TestFormatPercentFixed(t *testing.T) {
    tests := []struct {
        value  float64
        width  int
        expect string
    }{
        {0.0, 7, "  0.00%"},
        {0.0003, 7, "  0.03%"},
        {0.992, 7, " 99.20%"},
        {1.0, 7, "100.00%"},
    }
    // ... test implementation
}

func TestFormatMsFixed(t *testing.T) {
    tests := []struct {
        ms     float64
        width  int
        expect string
    }{
        {0.0, 6, "   0ms"},
        {0.8, 6, " 0.8ms"},
        {12.0, 6, "  12ms"},
        {123.0, 6, " 123ms"},
    }
    // ... test implementation
}
```

### 9.2 Integration Tests

**File**: `internal/tui/view_debug_test.go`

**Test Cases**:
1. Verify dashboard width is exactly 80 characters
2. Verify column widths are correct (36 + 3 + 36 = 75)
3. Verify all numeric fields are right-aligned
4. Verify no visual shifting as values update

### 9.3 Visual Testing

**Manual Testing Checklist**:
- [ ] Dashboard width is 80 characters
- [ ] All numbers are right-aligned
- [ ] Numbers don't shift as values increase
- [ ] Layout matches design spec exactly
- [ ] Two-column layout is balanced
- [ ] Separator (` │ `) is correctly positioned
- [ ] Box borders render correctly
- [ ] All metrics display correctly at various values

### 9.4 Edge Case Testing

**Test Scenarios**:
1. **Zero values**: All metrics at 0
2. **Small values**: 1-99 range
3. **Medium values**: 100-9,999 range
4. **Large values**: 10,000-99,999,999 range
5. **Very large values**: > 100M (should still fit in 8 chars with commas)
6. **Rate transitions**: Rate changing from 0 → low → high → stalled
7. **Percentage edge cases**: 0.00%, 0.01%, 99.99%, 100.00%
8. **Millisecond edge cases**: 0ms, 0.1ms, 999ms

---

## 10. Implementation Checklist

### Phase 1: Formatting Functions
- [ ] Add `formatNumberWithCommas()`
- [ ] Add `formatNumberFixed()`
- [ ] Add `formatRateFixed()`
- [ ] Add `formatPercentFixed()`
- [ ] Add `formatMsFixed()`
- [ ] Write unit tests for all functions
- [ ] Verify tests pass

### Phase 2: View Updates
- [ ] Update `renderHLSLayer()` with fixed-width formatting
- [ ] Update `renderHTTPLayer()` with fixed-width formatting
- [ ] Update `renderTCPLayer()` with fixed-width formatting
- [ ] Verify visual alignment

### Phase 3: Constants and Organization
- [ ] Define label constants
- [ ] Update view functions to use constants
- [ ] Verify consistent spacing

### Phase 4: Column Layout
- [ ] Update `renderTwoColumns()` with fixed widths
- [ ] Verify column widths (36 + 3 + 36 = 75)
- [ ] Test with various screen sizes

### Phase 5: Testing
- [ ] Run unit tests
- [ ] Run integration tests
- [ ] Visual inspection
- [ ] Edge case testing
- [ ] Performance testing (no regressions)

---

## 11. Success Criteria

### Functional Requirements
- ✅ All numeric values are right-aligned in fixed-width fields
- ✅ No visual "jumping" as values update
- ✅ Dashboard width is exactly 80 characters
- ✅ Layout matches design spec section 11.6 exactly

### Quality Requirements
- ✅ All formatting functions have unit tests (>90% coverage)
- ✅ No performance regressions
- ✅ Code is maintainable and well-documented
- ✅ Visual appearance is professional and consistent

### Design Requirements
- ✅ Numbers use comma separators (not K/M suffixes for main counters)
- ✅ Percentages show 2 decimal places
- ✅ Rates use `+` prefix and fixed format
- ✅ Two-column layout is balanced and readable

---

## 12. Related Documentation

- [FFMPEG_METRICS_SOCKET_DESIGN.md](FFMPEG_METRICS_SOCKET_DESIGN.md) - Section 11.6 (Design Specification)
- [FFMPEG_CLIENT_METRICS.md](FFMPEG_CLIENT_METRICS.md) - Metrics Collection Reference
- [FFMPEG_HLS_REFERENCE.md](FFMPEG_HLS_REFERENCE.md) - Section 13 (Metrics Categories)
- [TUI_REDESIGN_GAPS.md](TUI_REDESIGN_GAPS.md) - Current Implementation Gaps
- [internal/tui/view.go](../internal/tui/view.go) - Current TUI Implementation
- [internal/tui/model.go](../internal/tui/model.go) - Formatting Functions

---

## Appendix A: Formatting Function Examples

### A.1 formatNumberFixed Examples

| Input | Width | Output | Notes |
|-------|-------|--------|-------|
| 0 | 8 | `       0` | Right-aligned |
| 12 | 8 | `      12` | Right-aligned |
| 1234 | 8 | `   1,234` | Comma separator |
| 12345 | 8 | `  12,345` | Comma separator |
| 123456 | 8 | ` 123,456` | Comma separator |
| 1234567 | 8 | `1,234,567` | Multiple commas |
| 12345678 | 8 | `12,345,678` | Multiple commas |

### A.2 formatRateFixed Examples

| Input | Width | Output | Notes |
|-------|-------|--------|-------|
| 0.0 | 8 | `(stalled)` | Special case |
| 0.5 | 8 | `  +0.5/s` | Right-aligned |
| 12.0 | 8 | `   +12/s` | Right-aligned |
| 123.4 | 8 | `  +123/s` | Right-aligned |
| 1234.5 | 8 | `+1.2K/s` | K suffix |
| 12345.6 | 8 | `+12.3K/s` | K suffix |

### A.3 formatPercentFixed Examples

| Input | Width | Output | Notes |
|-------|-------|--------|-------|
| 0.0 | 7 | `  0.00%` | Right-aligned |
| 0.0003 | 7 | `  0.03%` | 2 decimal places |
| 0.992 | 7 | ` 99.20%` | 2 decimal places |
| 1.0 | 7 | `100.00%` | 2 decimal places |

### A.4 formatMsFixed Examples

| Input | Width | Output | Notes |
|-------|-------|--------|-------|
| 0.0 | 6 | `   0ms` | Right-aligned |
| 0.8 | 6 | ` 0.8ms` | 1 decimal place |
| 12.0 | 6 | `  12ms` | Integer |
| 123.0 | 6 | ` 123ms` | Integer |

---

## Appendix B: Column Width Calculation Details

### B.1 Left Column (Segments) - 36 chars

```
"  ✅ Downloaded: " = 17 chars
[8-char value]       =  8 chars
"  ("                =  3 chars
[8-char rate]        =  8 chars
")"                  =  1 char
─────────────────────────────
Total                = 37 chars (slight overflow acceptable)
```

**Adjustment**: Reduce label or value width slightly if needed.

### B.2 Right Column (Playlists) - 36 chars

```
"  ✅ Refreshed: " = 16 chars
[8-char value]     =  8 chars
"  ("              =  3 chars
[8-char rate]      =  8 chars
")"                =  1 char
─────────────────────────────
Total              = 36 chars ✓
```

---

**End of Document**
