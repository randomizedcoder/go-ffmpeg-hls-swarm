# TUI Vertical Alignment Analysis and Fix Plan

> **Type**: Analysis and Implementation Plan
> **Date**: 2026-01-23
> **Related**: [TUI_DASHBOARD_FIXED_WIDTH_PLAN.md](TUI_DASHBOARD_FIXED_WIDTH_PLAN.md)

---

## Executive Summary

This document analyzes vertical alignment issues in the TUI dashboard and proposes solutions. The dashboard uses a 3-column layout (label | value | bracket) within each section, but several issues prevent proper vertical alignment.

---

## Problem Analysis

### Issue 1: Double Width Application

**Problem**: `renderLabel()` applies `Width(labelColWidth)`, then `renderMetricRow()` applies `Width(labelColWidth)` again to the already-rendered label.

**Current Code**:
```go
func renderLabel(text string) string {
    return lipgloss.NewStyle().Width(labelColWidth).Render(text)
}

func renderMetricRow(label, value, bracket string) string {
    labelStyle := lipgloss.NewStyle().Width(labelColWidth)  // ← Applied again!
    // ...
}
```

**Impact**:
- Labels may be double-wrapped, causing inconsistent widths
- Lipgloss may truncate or pad differently when width is applied twice
- Visual misalignment between rows

**Evidence**: Labels that are already 18 chars wide get wrapped again, potentially causing overflow or inconsistent padding.

---

### Issue 2: Pre-formatted Values with Style Wrapping

**Problem**: Values are pre-formatted with `formatNumberFixed()` (which right-aligns), then wrapped in a style that also applies width and right alignment.

**Current Code**:
```go
segStyle.Render(formatNumberFixed(ds.SegmentsDownloaded, valueColWidth))
```

Where `formatNumberFixed()` returns:
```go
return fmt.Sprintf("%*s", width, formatted)  // Already right-aligned
```

Then wrapped in:
```go
valueStyle := lipgloss.NewStyle().Width(valueColWidth).Align(lipgloss.Right)
valueStyle.Render(...)  // Applies width and alignment again
```

**Impact**:
- Double alignment/width application can cause misalignment
- Lipgloss may handle pre-formatted strings differently
- Inconsistent spacing between value and bracket columns

---

### Issue 3: Inconsistent String Lengths

**Problem**: Some formatted strings may exceed their intended column widths.

**Examples**:
- `formatNumberFixed(999999999, 10)` → `"999,999,999"` (11 chars) exceeds `valueColWidth=10`
- `formatBracketRate(12345.6)` → `"(+12.3K/s)"` (10 chars) might fit, but `"(stalled)"` (9 chars) is shorter
- Jitter: `"0.0ms avg"` (9 chars) + `"/7892ms max"` (11 chars) may not fit in columns

**Impact**:
- Values overflow their columns
- Lipgloss truncates or wraps, breaking alignment
- Visual "jumping" as values change

---

### Issue 4: Empty Bracket Fields

**Problem**: Empty bracket fields (`""`) may not maintain proper column spacing.

**Current Code**:
```go
renderMetricRow(
    renderLabel("  Avg:"),
    valueStyle.Render(formatMsFixed(...)),
    "", // Empty bracket
)
```

**Impact**:
- Empty strings might not reserve space properly
- Column 3 might collapse, affecting alignment
- Inconsistent spacing between rows with/without brackets

---

### Issue 5: Special Format Cases

**Problem**: Some metrics don't follow the standard 3-column pattern:

1. **Jitter**: Uses custom format `"Xms avg/Yms max"` split across columns
2. **Sequence**: Uses inline format `"Current: X   Skips: Y"` instead of 3-column
3. **Health**: Health bar + percentage in value and bracket columns
4. **Status**: Text status in value column, empty bracket

**Impact**:
- These special cases break vertical alignment
- Different row structures prevent consistent column boundaries
- Visual inconsistency

---

### Issue 6: Section Headers and Separators

**Problem**: Section headers like "Segments", "Playlists", "Segment Wall Time" are not part of the 3-column layout.

**Current Code**:
```go
leftCol = append(leftCol, labelStyle.Render("Segments"))
leftCol = append(leftCol, "") // Empty line separator
leftCol = append(leftCol, labelStyle.Render("Segment Wall Time"))
```

**Impact**:
- Headers don't align with metric rows
- Empty lines add inconsistent spacing
- Breaks visual flow

---

## Root Cause Analysis

### Primary Issue: Lipgloss Width/Align Behavior

When `Width()` and `Align()` are applied to already-formatted strings:

1. **Width()** truncates or pads strings to the specified width
2. **Align()** applies alignment within that width
3. If a string is already formatted with `fmt.Sprintf("%*s", width, text)`, applying `Width()` again can cause:
   - Double padding (if string is shorter than width)
   - Truncation (if string is longer than width)
   - Inconsistent behavior depending on string content

### Secondary Issue: Column Width Mismatch

The 3-column layout requires:
- Label: 18 chars
- Value: 10 chars
- Bracket: 12 chars
- **Total**: 40 chars

But the section width is 42 chars, leaving 2 chars for spacing. However, if any column overflows, the entire row alignment breaks.

---

## Proposed Solutions

### Solution 1: Remove Double Width Application

**Change**: Don't apply width in `renderMetricRow()` if the input is already formatted.

**Option A**: Pass raw strings to `renderMetricRow()`, let it handle all formatting:
```go
func renderMetricRow(labelText, valueText, bracketText string) string {
    labelStyle := lipgloss.NewStyle().Width(labelColWidth)
    valueStyle := lipgloss.NewStyle().Width(valueColWidth).Align(lipgloss.Right)
    bracketStyle := lipgloss.NewStyle().Width(bracketColWidth).Align(lipgloss.Right)

    return lipgloss.JoinHorizontal(lipgloss.Left,
        labelStyle.Render(labelText),  // Raw text, not pre-rendered
        valueStyle.Render(valueText),  // Raw text, not pre-formatted
        bracketStyle.Render(bracketText),
    )
}
```

**Option B**: Remove width from `renderLabel()`, only apply in `renderMetricRow()`:
```go
func renderLabel(text string) string {
    return text  // Just return the text, no width applied
}

func renderMetricRow(label, value, bracket string) string {
    labelStyle := lipgloss.NewStyle().Width(labelColWidth)
    // ... apply width here only
}
```

**Recommendation**: Option A - simpler, more consistent.

---

### Solution 2: Ensure All Values Fit in Columns

**Change**: Verify all formatted values fit within their column widths.

**Actions**:
1. Increase `valueColWidth` from 10 to 11 or 12 to accommodate large numbers with commas
2. Verify `bracketColWidth=12` is sufficient for all bracket formats
3. Add validation/truncation if values exceed width

**Column Width Recommendations**:
```go
const (
    labelColWidth   = 18 // "  ✅ Downloaded:" (17 chars) + 1 padding
    valueColWidth   = 12 // For "999,999,999" (11 chars) + 1 padding
    bracketColWidth = 12 // For "(+12.3K/s)" or "(stalled)" - sufficient
)
```

**Total per section**: 18 + 12 + 12 = 42 chars ✓ (matches current section width)

---

### Solution 3: Standardize All Rows to 3-Column Layout

**Change**: Make all metric rows use the same 3-column structure.

**Current Issues**:
- Jitter: Custom format
- Sequence: Inline format
- Health: Special format

**Proposed Fixes**:

**Jitter**:
```go
// Current: "Xms avg/Yms max" split across columns
// Proposed: Keep as single value in value column, or split properly
renderMetricRow(
    "  ⏱️ Jitter:",
    formatMsFixed(avg, 6) + " avg",
    "/" + formatMsFixed(max, 6) + " max",
)
```

**Sequence**:
```go
// Current: "Current: X   Skips: Y" (inline)
// Proposed: Two separate rows or use 3-column for each
renderMetricRow("  Current:", formatNumberFixed(current, valueColWidth), "")
renderMetricRow("  Skips:", formatNumberFixed(skips, valueColWidth), "")
```

**Health**:
```go
// Current: Health bar + percentage
// Proposed: Health bar in value, percentage in bracket
renderMetricRow(
    "  Health:",
    healthBar,  // "●●●●●●●●○○" (10 chars)
    formatPercentFixed(ratio, bracketColWidth),
)
```

---

### Solution 4: Handle Empty Bracket Fields Consistently

**Change**: Always reserve space for bracket column, even when empty.

**Current**: Empty string `""` might not reserve space
**Proposed**: Use space padding or explicit empty string with width:
```go
func renderMetricRow(label, value, bracket string) string {
    // ...
    if bracket == "" {
        bracket = strings.Repeat(" ", bracketColWidth)  // Reserve space
    }
    // ...
}
```

Or use lipgloss to ensure width:
```go
bracketStyle := lipgloss.NewStyle().Width(bracketColWidth).Align(lipgloss.Right)
bracketStyle.Render(bracket)  // Empty string still gets width applied
```

---

### Solution 5: Fix Section Headers Alignment

**Change**: Make section headers align with the 3-column layout.

**Option A**: Use full-width header spanning all 3 columns:
```go
headerStyle := lipgloss.NewStyle().Width(labelColWidth + valueColWidth + bracketColWidth)
leftCol = append(leftCol, headerStyle.Render("Segments"))
```

**Option B**: Use 3-column layout with empty value/bracket:
```go
leftCol = append(leftCol,
    renderMetricRow("Segments", "", ""),
)
```

**Recommendation**: Option A - headers should span full width for visual separation.

---

## Implementation Plan

### Phase 1: Fix Double Width Application

1. **Modify `renderMetricRow()`**:
   - Accept raw strings (not pre-rendered)
   - Apply width and alignment only in `renderMetricRow()`
   - Remove width from `renderLabel()` or make it optional

2. **Update all call sites**:
   - Change `renderLabel("...")` to pass raw text
   - Change `formatNumberFixed(..., valueColWidth)` to pass raw number, format in `renderMetricRow()`
   - Or keep formatting but don't apply width twice

**Files**: `internal/tui/view.go`

---

### Phase 2: Adjust Column Widths

1. **Increase `valueColWidth`** from 10 to 12:
   - Accommodates "999,999,999" (11 chars)
   - Provides 1 char padding

2. **Verify `bracketColWidth=12`** is sufficient:
   - Test with longest bracket format: "(+12.3K/s)" (10 chars) ✓
   - Test with "(stalled)" (9 chars) ✓

3. **Update section width** if needed:
   - Current: 42 chars
   - New: 18 + 12 + 12 = 42 chars ✓ (no change needed)

**Files**: `internal/tui/view.go`

---

### Phase 3: Standardize Special Cases

1. **Fix Jitter formatting**:
   - Ensure "avg" and "max" fit in their columns
   - Test with various values (0.0ms, 7892ms, etc.)

2. **Fix Sequence formatting**:
   - Use 3-column layout for "Current" and "Skips" separately
   - Or combine in a way that maintains alignment

3. **Fix Health formatting**:
   - Health bar (10 dots) in value column
   - Percentage in bracket column

**Files**: `internal/tui/view.go`

---

### Phase 4: Handle Empty Fields

1. **Ensure empty bracket fields reserve space**:
   - Use lipgloss width even for empty strings
   - Or pad with spaces explicitly

2. **Test all rows with empty brackets**:
   - Avg/Min/Max rows
   - Error Rate row
   - Status row
   - Reconnects row

**Files**: `internal/tui/view.go`

---

### Phase 5: Fix Section Headers

1. **Make headers span full width**:
   - Use width = labelColWidth + valueColWidth + bracketColWidth
   - Or use 3-column layout with header text in label column

2. **Handle empty line separators**:
   - Use consistent spacing
   - Or remove if not needed

**Files**: `internal/tui/view.go`

---

## Detailed Fix Implementation

### Fix 1: Refactor renderMetricRow()

```go
// renderMetricRow renders a 3-column metric row: label | value | bracket
// All formatting and width application happens here for consistency
func renderMetricRow(labelText, valueText, bracketText string) string {
    // Label: left-aligned, fixed width
    labelStyle := lipgloss.NewStyle().Width(labelColWidth)

    // Value: right-aligned, fixed width
    valueStyle := lipgloss.NewStyle().Width(valueColWidth).Align(lipgloss.Right)

    // Bracket: right-aligned, fixed width (even if empty)
    bracketStyle := lipgloss.NewStyle().Width(bracketColWidth).Align(lipgloss.Right)

    return lipgloss.JoinHorizontal(lipgloss.Left,
        labelStyle.Render(labelText),
        valueStyle.Render(valueText),
        bracketStyle.Render(bracketText),
    )
}
```

### Fix 2: Update Column Widths

```go
const (
    labelColWidth   = 18 // "  ✅ Downloaded:" (17 chars) + 1 padding
    valueColWidth   = 12 // For "999,999,999" (11 chars) + 1 padding
    bracketColWidth = 12 // For "(+12.3K/s)" or "(stalled)" - sufficient
)
```

### Fix 3: Update Formatting Functions

```go
// formatNumberFixed - returns raw formatted string (no width applied)
func formatNumberFixed(n int64, width int) string {
    formatted := formatNumberWithCommas(n)
    return fmt.Sprintf("%*s", width, formatted)  // Right-align in specified width
}

// Usage in renderMetricRow:
renderMetricRow(
    "  ✅ Downloaded:",  // Raw text
    formatNumberFixed(ds.SegmentsDownloaded, valueColWidth),  // Pre-formatted, right-aligned
    formatBracketRate(ds.InstantSegmentsRate),  // Pre-formatted, right-aligned
)
```

**Note**: The pre-formatted strings will be wrapped in lipgloss styles that apply width again. This is intentional - the pre-formatting ensures right-alignment, and lipgloss width ensures the column width is maintained.

---

## Testing Strategy

### Visual Alignment Tests

1. **Label Column Alignment**:
   - All labels should start at the same horizontal position
   - Test with various label lengths
   - Verify emoji labels align correctly

2. **Value Column Alignment**:
   - All values should end at the same horizontal position
   - Test with 1-digit, 2-digit, 3-digit, and large numbers
   - Verify right-alignment is consistent

3. **Bracket Column Alignment**:
   - All brackets should end at the same horizontal position
   - Test with rates, percentages, and empty brackets
   - Verify right-alignment is consistent

### Edge Case Tests

1. **Large Numbers**:
   - Test with 999,999,999 (11 chars with commas)
   - Verify no truncation or wrapping

2. **Long Bracket Text**:
   - Test with "(+12.3K/s)" (10 chars)
   - Test with "(stalled)" (9 chars)
   - Verify both fit in bracketColWidth=12

3. **Empty Fields**:
   - Test rows with empty bracket fields
   - Verify column spacing is maintained

4. **Special Formats**:
   - Test Jitter with various values
   - Test Sequence with large numbers
   - Test Health bar display

---

## Expected Outcomes

After implementing these fixes:

1. ✅ **All labels align vertically** - Same starting position
2. ✅ **All values align vertically** - Same ending position
3. ✅ **All brackets align vertically** - Same ending position
4. ✅ **No wrapping** - All content fits in columns
5. ✅ **Consistent spacing** - Uniform gaps between columns
6. ✅ **Professional appearance** - Clean, aligned dashboard

---

## Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Column widths too narrow | Medium | High | Test with maximum values, increase widths if needed |
| Double width causes issues | High | Medium | Remove double application, test thoroughly |
| Special cases break alignment | Medium | Medium | Standardize all rows to 3-column layout |
| Performance regression | Low | Low | Minimal changes, should be negligible |

---

## Success Criteria

- [ ] All metric rows use consistent 3-column layout
- [ ] Labels start at the same horizontal position (column 1)
- [ ] Values end at the same horizontal position (column 2)
- [ ] Brackets end at the same horizontal position (column 3)
- [ ] No text wrapping or truncation
- [ ] Visual inspection confirms perfect alignment
- [ ] Code compiles without errors
- [ ] No performance regressions

---

## Related Documentation

- [TUI_DASHBOARD_FIXED_WIDTH_PLAN.md](TUI_DASHBOARD_FIXED_WIDTH_PLAN.md) - Original fixed-width plan
- [TUI_DASHBOARD_FIXED_WIDTH_PLAN_LOG.md](TUI_DASHBOARD_FIXED_WIDTH_PLAN_LOG.md) - Implementation log
- [FFMPEG_METRICS_SOCKET_DESIGN.md](FFMPEG_METRICS_SOCKET_DESIGN.md) - Section 11.6 (Design Specification)

---

**End of Document**
