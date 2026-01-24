package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/stats"
)

// Fixed-width column widths for 3-column layout within each section
// Column 1: Label (left-aligned)
// Column 2: Value (right-aligned)
// Column 3: Bracket field (right-aligned)
const (
	labelColWidth   = 18 // Longest label is "  ✅ Downloaded:" (17 chars) + 1 padding
	valueColWidth   = 12 // For "999,999,999" (11 chars) + 1 padding
	bracketColWidth = 12 // For "(+12.3K/s)" or "(stalled)" - sufficient
)

// renderMetricRow renders a 3-column metric row: label | value | bracket
// All width and alignment is applied here for consistency.
// Inputs should be raw strings (not pre-rendered with lipgloss styles).
// valueStyle and bracketStyle are optional lipgloss.Style objects to apply styling - if nil, default styles are used.
func renderMetricRow(labelText, valueText, bracketText string, valueStyle, bracketStyle *lipgloss.Style) string {
	// Label: left-aligned, fixed width (no styling applied to labels)
	labelStyle := lipgloss.NewStyle().Width(labelColWidth)
	renderedLabel := labelStyle.Render(labelText)

	// Value: chain width/alignment with styling so lipgloss handles ANSI codes correctly
	var valueStyleChain lipgloss.Style
	if valueStyle != nil {
		valueStyleChain = valueStyle.Copy().Width(valueColWidth).Align(lipgloss.Right)
	} else {
		valueStyleChain = lipgloss.NewStyle().Width(valueColWidth).Align(lipgloss.Right)
	}
	renderedValue := valueStyleChain.Render(valueText)

	// Bracket: chain width/alignment with styling so lipgloss handles ANSI codes correctly
	var bracketStyleChain lipgloss.Style
	if bracketStyle != nil {
		bracketStyleChain = bracketStyle.Copy().Width(bracketColWidth).Align(lipgloss.Right)
	} else {
		bracketStyleChain = lipgloss.NewStyle().Width(bracketColWidth).Align(lipgloss.Right)
	}
	renderedBracket := bracketStyleChain.Render(bracketText)

	return lipgloss.JoinHorizontal(lipgloss.Left,
		renderedLabel,
		renderedValue,
		renderedBracket,
	)
}

// formatBracketRate formats a rate for the bracket column (includes parentheses).
// Returns raw string - width/alignment will be applied by renderMetricRow.
func formatBracketRate(rate float64) string {
	if rate >= 1000 {
		return fmt.Sprintf("+%.1fK/s", rate/1000)
	} else if rate >= 1 {
		return fmt.Sprintf("+%.0f/s", rate)
	} else if rate > 0 {
		return fmt.Sprintf("+%.1f/s", rate)
	}
	return "(stalled)"
}

// formatBracketPercent formats a percentage for the bracket column (includes parentheses).
// Returns raw string - width/alignment will be applied by renderMetricRow.
func formatBracketPercent(percent float64) string {
	return fmt.Sprintf("(%.2f%%)", percent*100)
}

// formatNumberRaw formats a number with commas but without width/alignment.
// Width/alignment will be applied by renderMetricRow.
func formatNumberRaw(n int64) string {
	return formatNumberWithCommas(n)
}

// formatMsRaw formats milliseconds without width/alignment.
// Width/alignment will be applied by renderMetricRow.
func formatMsRaw(ms float64) string {
	if ms == 0 {
		return "0.0ms"
	}
	if ms < 1 {
		return fmt.Sprintf("%.1fms", ms)
	}
	if ms < 1000 {
		return fmt.Sprintf("%.0fms", ms)
	}
	return fmt.Sprintf("%.0fms", ms)
}

// formatPercentRaw formats a percentage without width/alignment.
// Width/alignment will be applied by renderMetricRow.
func formatPercentRaw(value float64) string {
	return fmt.Sprintf("%.2f%%", value*100)
}

// =============================================================================
// Main View Rendering
// =============================================================================

// renderSummaryView renders the main summary dashboard.
func (m Model) renderSummaryView() string {
	var sections []string

	// Header
	sections = append(sections, m.renderHeader())

	// Progress section
	sections = append(sections, m.renderProgress())

	// Stats sections (only if we have stats)
	if m.stats != nil {
		sections = append(sections, m.renderRequestStats())
		sections = append(sections, m.renderLatencyStats())
		sections = append(sections, m.renderHealthStats())

		// Errors section (only if there are errors)
		if m.hasErrors() {
			sections = append(sections, m.renderErrorStats())
		}
	}

	// Layered debug metrics (HLS/HTTP/TCP) - Phase 7
	if m.debugStats != nil {
		sections = append(sections, m.renderDebugMetrics())
	}

	// Footer
	sections = append(sections, m.renderFooter())

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// renderDetailedView renders per-client details.
func (m Model) renderDetailedView() string {
	var sections []string

	// Header
	sections = append(sections, m.renderHeader())

	// Per-client table
	sections = append(sections, m.renderClientTable())

	// Footer
	sections = append(sections, m.renderFooter())

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// =============================================================================
// Header
// =============================================================================

func (m Model) renderHeader() string {
	// Pipeline status indicator
	metricsLabel := GetMetricsLabel(m.DropRate())

	// Build header line
	header := fmt.Sprintf(
		" go-ffmpeg-hls-swarm │ %s │ Clients: %d/%d │ Elapsed: %s ",
		metricsLabel,
		m.ActiveClients(),
		m.targetClients,
		formatDuration(m.Elapsed()),
	)

	return headerStyle.Width(m.width).Render(header)
}

// =============================================================================
// Progress Section
// =============================================================================

func (m Model) renderProgress() string {
	progress := m.RampProgress()

	// Progress bar
	barWidth := m.width - 30
	if barWidth < 20 {
		barWidth = 20
	}
	progressBar := RenderProgressBar(progress, barWidth)

	// Status text
	var status string
	if progress >= 1.0 {
		status = statusOK.Render(fmt.Sprintf("✓ All clients running (%d / %d)", m.ActiveClients(), m.targetClients))
	} else {
		status = statusInfo.Render(fmt.Sprintf("Ramping up... %d/%d", m.ActiveClients(), m.targetClients))
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		sectionHeaderStyle.Render("Ramp Progress"),
		progressBar,
		status,
	)

	return boxStyle.Width(m.width - 2).Render(content)
}

// =============================================================================
// Request Statistics
// =============================================================================

func (m Model) renderRequestStats() string {
	if m.stats == nil {
		return ""
	}

	s := m.stats

	// Build rows
	rows := []string{
		renderStatRow("Manifest Requests", formatNumber(s.TotalManifestReqs), formatRate(s.ManifestReqRate)),
		renderStatRow("Segment Requests", formatNumber(s.TotalSegmentReqs), formatRate(s.SegmentReqRate)),
		renderStatRow("Total Bytes", formatBytes(s.TotalBytes), formatBytes(int64(s.ThroughputBytesPerSec))+"/s"),
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		append([]string{sectionHeaderStyle.Render("Request Statistics")}, rows...)...,
	)

	return boxStyle.Width(m.width - 2).Render(content)
}

func renderStatRow(label, value, rate string) string {
	return lipgloss.JoinHorizontal(lipgloss.Left,
		labelWideStyle.Render(label+":"),
		valueStyle.Width(12).Render(value),
		mutedStyle.Render(" ("),
		valueStyle.Render(rate),
		mutedStyle.Render(")"),
	)
}

// =============================================================================
// Latency Statistics
// =============================================================================

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

func renderLatencyRow(label string, d time.Duration) string {
	value := formatMsFromDuration(d)

	// Color based on latency
	style := valueStyle
	// Could add color coding here based on thresholds

	return lipgloss.JoinHorizontal(lipgloss.Left,
		labelStyle.Render(label+":"),
		style.Render(value),
	)
}

// =============================================================================
// Health Statistics
// =============================================================================

func (m Model) renderHealthStats() string {
	if m.stats == nil {
		return ""
	}

	s := m.stats

	// Speed distribution
	total := s.ClientsAboveRealtime + s.ClientsBelowRealtime
	var speedDist string
	if total > 0 {
		healthyPct := float64(s.ClientsAboveRealtime) / float64(total) * 100
		speedDist = fmt.Sprintf("%d healthy (%.0f%%), %d buffering",
			s.ClientsAboveRealtime, healthyPct, s.ClientsBelowRealtime)
	} else {
		speedDist = "N/A"
	}

	// Average speed with color
	avgSpeedStyle := GetSpeedStyle(s.AverageSpeed)
	avgSpeed := avgSpeedStyle.Render(fmt.Sprintf("%.2fx", s.AverageSpeed))

	// Stalled clients
	stalledStyle := valueStyle
	if s.StalledClients > 0 {
		stalledStyle = valueBadStyle
	}
	stalled := stalledStyle.Render(fmt.Sprintf("%d", s.StalledClients))

	rows := []string{
		RenderKeyValue("Speed Distribution", speedDist),
		lipgloss.JoinHorizontal(lipgloss.Left,
			labelStyle.Render("Average Speed:"),
			avgSpeed,
		),
		lipgloss.JoinHorizontal(lipgloss.Left,
			labelStyle.Render("Stalled Clients:"),
			stalled,
		),
	}

	// Add drift info if available
	if s.MaxDrift > 0 {
		driftStyle := valueStyle
		if s.ClientsWithHighDrift > 0 {
			driftStyle = valueWarnStyle
		}
		rows = append(rows,
			lipgloss.JoinHorizontal(lipgloss.Left,
				labelStyle.Render("Max Drift:"),
				driftStyle.Render(formatMsFromDuration(s.MaxDrift)),
			),
		)
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		append([]string{sectionHeaderStyle.Render("Playback Health")}, rows...)...,
	)

	return boxStyle.Width(m.width - 2).Render(content)
}

// =============================================================================
// Error Statistics
// =============================================================================

func (m Model) hasErrors() bool {
	if m.stats == nil {
		return false
	}
	return len(m.stats.TotalHTTPErrors) > 0 ||
		m.stats.TotalTimeouts > 0 ||
		m.stats.TotalReconnections > 0
}

func (m Model) renderErrorStats() string {
	if m.stats == nil {
		return ""
	}

	s := m.stats

	var rows []string

	// HTTP errors
	for code, count := range s.TotalHTTPErrors {
		rows = append(rows,
			lipgloss.JoinHorizontal(lipgloss.Left,
				labelStyle.Render(fmt.Sprintf("HTTP %d:", code)),
				valueBadStyle.Render(fmt.Sprintf("%d", count)),
			),
		)
	}

	// Timeouts
	if s.TotalTimeouts > 0 {
		rows = append(rows,
			lipgloss.JoinHorizontal(lipgloss.Left,
				labelStyle.Render("Timeouts:"),
				valueBadStyle.Render(fmt.Sprintf("%d", s.TotalTimeouts)),
			),
		)
	}

	// Reconnections
	if s.TotalReconnections > 0 {
		rows = append(rows,
			lipgloss.JoinHorizontal(lipgloss.Left,
				labelStyle.Render("Reconnections:"),
				valueWarnStyle.Render(fmt.Sprintf("%d", s.TotalReconnections)),
			),
		)
	}

	// Error rate
	errorRateStyle := GetErrorRateStyle(s.ErrorRate)
	rows = append(rows,
		lipgloss.JoinHorizontal(lipgloss.Left,
			labelStyle.Render("Error Rate:"),
			errorRateStyle.Render(formatPercent(s.ErrorRate)),
		),
	)

	content := lipgloss.JoinVertical(lipgloss.Left,
		append([]string{sectionHeaderStyle.Render("Errors")}, rows...)...,
	)

	return boxStyle.Width(m.width - 2).Render(content)
}

// =============================================================================
// Client Table (Detailed View)
// =============================================================================

func (m Model) renderClientTable() string {
	if m.stats == nil || len(m.stats.PerClientSummaries) == 0 {
		return boxStyle.Width(m.width - 2).Render(
			dimStyle.Render("No per-client data available. Press 'd' to toggle."),
		)
	}

	// Table header
	header := tableHeaderStyle.Render(
		fmt.Sprintf("%-6s %-10s %-10s %-10s %-8s %-8s",
			"ID", "Manifests", "Segments", "Bytes", "Speed", "Errors"),
	)

	// Table rows (limit to fit screen)
	maxRows := m.height - 10
	if maxRows < 5 {
		maxRows = 5
	}

	var rows []string
	for i, client := range m.stats.PerClientSummaries {
		if i >= maxRows {
			rows = append(rows, dimStyle.Render(fmt.Sprintf("... and %d more clients", len(m.stats.PerClientSummaries)-maxRows)))
			break
		}

		// Calculate total errors for this client
		totalErrors := int64(0)
		for _, count := range client.HTTPErrors {
			totalErrors += count
		}
		totalErrors += client.Timeouts + client.Reconnections

		// Style based on health
		rowStyle := tableRowEvenStyle
		if i%2 == 1 {
			rowStyle = tableRowOddStyle
		}

		speedStyle := GetSpeedStyle(client.CurrentSpeed)

		row := fmt.Sprintf("%-6d %-10s %-10s %-10s %-8s %-8d",
			client.ClientID,
			formatNumber(client.ManifestRequests),
			formatNumber(client.SegmentRequests),
			formatBytes(client.TotalBytes),
			speedStyle.Render(fmt.Sprintf("%.2fx", client.CurrentSpeed)),
			totalErrors,
		)
		rows = append(rows, rowStyle.Render(row))
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		append([]string{
			sectionHeaderStyle.Render("Per-Client Statistics"),
			header,
		}, rows...)...,
	)

	return boxStyle.Width(m.width - 2).Render(content)
}

// =============================================================================
// Layered Debug Metrics (Phase 7)
// =============================================================================

// renderDebugMetrics renders the layered dashboard (HLS/HTTP/TCP) with box borders.
// Matches design in FFMPEG_METRICS_SOCKET_DESIGN.md section 11.6.
func (m Model) renderDebugMetrics() string {
	if m.debugStats == nil {
		return ""
	}

	ds := m.debugStats
	var sections []string

	// Dashboard header with timing indicator
	header := m.renderDebugMetricsHeader(ds)
	sections = append(sections, header)

	// HLS Layer
	sections = append(sections, m.renderHLSLayer(ds))

	// HTTP Layer
	sections = append(sections, m.renderHTTPLayer(ds))

	// TCP Layer
	sections = append(sections, m.renderTCPLayer(ds))

	// Join all sections
	content := lipgloss.JoinVertical(lipgloss.Left, sections...)

	// Wrap in box border
	boxWidth := m.width - 2
	if boxWidth < 60 {
		boxWidth = 60
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Width(boxWidth).
		Padding(0, 1).
		Render(content)
}

// renderDebugMetricsHeader renders the dashboard header with title and timing indicator.
func (m Model) renderDebugMetricsHeader(ds *stats.DebugStatsAggregate) string {
	// Calculate timing accuracy percentage
	timingPercent := 0.0
	if ds.LinesProcessed > 0 {
		timingPercent = float64(ds.TimestampsUsed) / float64(ds.LinesProcessed) * 100
	}

	timingText := fmt.Sprintf("Timing: ✅ FFmpeg Timestamps (%.1f%%)", timingPercent)
	if timingPercent < 50 {
		timingText = fmt.Sprintf("Timing: ⚠️ Mixed (%.1f%% timestamps)", timingPercent)
	}

	// Title and timing on same line
	title := "Origin Load Test Dashboard"
	spacing := m.width - 2 - lipgloss.Width(title) - lipgloss.Width(timingText) - 4
	if spacing < 1 {
		spacing = 1
	}

	headerLine := lipgloss.JoinHorizontal(lipgloss.Left,
		titleStyle.Render(title),
		strings.Repeat(" ", spacing),
		dimStyle.Render(timingText),
	)

	// Separator line
	separator := strings.Repeat("─", m.width-4)

	return lipgloss.JoinVertical(lipgloss.Left,
		headerLine,
		separator,
	)
}

// renderHLSLayer renders HLS layer metrics in two-column layout (Phase 8.6).
// Left column: Segments | Right column: Playlists
func (m Model) renderHLSLayer(ds *stats.DebugStatsAggregate) string {
	var leftCol, rightCol []string

	// === LEFT COLUMN: Segments ===
	leftCol = append(leftCol, labelStyle.Render("Segments"))

	// Segments Downloaded
	segStyle := valueStyle
	if ds.SegmentsDownloaded > 0 {
		segStyle = valueGoodStyle
	}
	leftCol = append(leftCol,
		renderMetricRow(
			"  ✅ Downloaded:",
			formatNumberRaw(ds.SegmentsDownloaded),
			formatBracketRate(ds.InstantSegmentsRate),
			&segStyle,
			&segStyle,
		),
	)

	// Segments Failed (always show, per design spec)
	percent := 0.0
	if ds.SegmentsDownloaded > 0 {
		percent = float64(ds.SegmentsFailed) / float64(ds.SegmentsDownloaded)
	}
	failedStyle := valueStyle
	if ds.SegmentsFailed > 0 {
		failedStyle = valueBadStyle
	}
	leftCol = append(leftCol,
		renderMetricRow(
			"  ⚠️ Failed:",
			formatNumberRaw(ds.SegmentsFailed),
			formatBracketPercent(percent),
			&failedStyle,
			&failedStyle,
		),
	)

	// Segments Skipped (always show, per design spec)
	skippedStyle := valueStyle
	if ds.SegmentsSkipped > 0 {
		skippedStyle = valueBadStyle
	}
	leftCol = append(leftCol,
		renderMetricRow(
			"  🔴 Skipped:",
			formatNumberRaw(ds.SegmentsSkipped),
			"(data loss)",
			&skippedStyle,
			&mutedStyle,
		),
	)

	// Segments Expired (always show, per design spec)
	expiredStyle := valueStyle
	if ds.SegmentsExpired > 0 {
		expiredStyle = valueWarnStyle
	}
	leftCol = append(leftCol,
		renderMetricRow(
			"  ⏩ Expired:",
			formatNumberRaw(ds.SegmentsExpired),
			"(fell behind)",
			&expiredStyle,
			&mutedStyle,
		),
	)

	// Segment Wall Time (always show, per design spec)
	leftCol = append(leftCol, "") // Empty line separator
	leftCol = append(leftCol, labelStyle.Render("Segment Wall Time"))
	// Use 3-column layout for proper alignment
	leftCol = append(leftCol,
		renderMetricRow(
			"  Avg:",
			formatMsRaw(ds.SegmentWallTimeAvg),
			"",
			&valueStyle,
			nil,
		),
	)
	leftCol = append(leftCol,
		renderMetricRow(
			"  Min:",
			formatMsRaw(ds.SegmentWallTimeMin),
			"",
			&valueStyle,
			nil,
		),
	)
	leftCol = append(leftCol,
		renderMetricRow(
			"  Max:",
			formatMsRaw(ds.SegmentWallTimeMax),
			"",
			&valueStyle,
			nil,
		),
	)

	// === RIGHT COLUMN: Playlists ===
	rightCol = append(rightCol, labelStyle.Render("Playlists"))

	// Playlists Refreshed
	playlistStyle := valueStyle
	if ds.PlaylistsRefreshed > 0 {
		playlistStyle = valueGoodStyle
	}

	// Diagnostic: Show parser info if no playlists but we have segments (indicates parsing issue)
	diagnostic := ""
	if ds.PlaylistsRefreshed == 0 && ds.SegmentsDownloaded > 0 && ds.ClientsWithDebugStats > 0 {
		// We have segments but no playlists - this suggests playlist events aren't being parsed
		// Possible causes:
		// 1. Log level too low (try -stats-loglevel debug)
		// 2. VOD stream (only one initial open, might have happened before parser started)
		// 3. Events not in logs (check FFmpeg stderr for "Opening.*m3u8")
		diagnostic = fmt.Sprintf(" (parsers: %d, lines: %s, try: -stats-loglevel debug)",
			ds.ClientsWithDebugStats, formatNumber(ds.LinesProcessed))
	}

	// Render the row first
	row := renderMetricRow(
		"  ✅ Refreshed:",
		formatNumberRaw(ds.PlaylistsRefreshed),
		formatBracketRate(ds.InstantPlaylistsRate),
		&playlistStyle,
		&playlistStyle,
	)
	// Append diagnostic on a new line to avoid affecting column alignment
	if diagnostic != "" {
		rightCol = append(rightCol, row)
		rightCol = append(rightCol, dimStyle.Render(diagnostic))
	} else {
		rightCol = append(rightCol, row)
	}

	// Playlists Failed (always show, per design spec)
	playlistFailedPercent := 0.0
	if ds.PlaylistsRefreshed > 0 {
		playlistFailedPercent = float64(ds.PlaylistsFailed) / float64(ds.PlaylistsRefreshed)
	}
	playlistFailedStyle := valueStyle
	if ds.PlaylistsFailed > 0 {
		playlistFailedStyle = valueBadStyle
	}
	rightCol = append(rightCol,
		renderMetricRow(
			"  ⚠️ Failed:",
			formatNumberRaw(ds.PlaylistsFailed),
			formatBracketPercent(playlistFailedPercent),
			&playlistFailedStyle,
			&playlistFailedStyle,
		),
	)

	// Playlist Jitter (always show, per design spec)
	jitterStyle := valueStyle
	if ds.PlaylistJitterMax > 100 {
		jitterStyle = valueWarnStyle
	}
	// Jitter: format as "Xms avg/Yms max" - put in value column, max in bracket
	avgStr := formatMsRaw(ds.PlaylistJitterAvg) + " avg"
	maxStr := "/" + formatMsRaw(ds.PlaylistJitterMax) + " max"
	rightCol = append(rightCol,
		renderMetricRow(
			"  ⏱️ Jitter:",
			avgStr,
			maxStr,
			&jitterStyle,
			&jitterStyle,
		),
	)

	// Playlist Late (always show, per design spec)
	latePercent := 0.0
	if ds.PlaylistsRefreshed > 0 {
		latePercent = float64(ds.PlaylistLateCount) / float64(ds.PlaylistsRefreshed)
	}
	lateStyle := valueStyle
	if ds.PlaylistLateCount > 0 {
		lateStyle = valueWarnStyle
	}
	rightCol = append(rightCol,
		renderMetricRow(
			"  ⏰ Late:",
			formatNumberRaw(ds.PlaylistLateCount),
			formatBracketPercent(latePercent),
			&lateStyle,
			&lateStyle,
		),
	)

	// Sequence (always show, per design spec)
	rightCol = append(rightCol, "") // Empty line separator
	rightCol = append(rightCol, labelStyle.Render("Sequence"))
	// Note: SequenceCurrent not yet tracked - using SegmentsDownloaded as approximation
	// In the future, we should track actual sequence numbers from DebugEventParser
	sequenceCurrent := ds.SegmentsDownloaded // Approximation until we track actual sequence
	// Use 3-column layout for proper alignment
	rightCol = append(rightCol,
		renderMetricRow(
			"  Current:",
			formatNumberRaw(sequenceCurrent),
			"",
			&valueStyle,
			nil,
		),
	)
	rightCol = append(rightCol,
		renderMetricRow(
			"  Skips:",
			formatNumberRaw(ds.SequenceSkips),
			"",
			&valueStyle,
			nil,
		),
	)

	// Render two columns
	// Available width: m.width - 2 (borders) - 2 (padding) = m.width - 4
	twoColContent := renderTwoColumns(leftCol, rightCol, m.width-4) // Account for box borders and padding

	// Combine with header and separator
	separator := strings.Repeat("─", m.width-4)
	content := lipgloss.JoinVertical(lipgloss.Left,
		sectionHeaderStyle.Render("📺 HLS LAYER (libavformat/hls.c)"),
		separator,
		twoColContent,
	)

	return content
}

// renderHTTPLayer renders HTTP layer metrics in two-column layout (Phase 8.6).
// Left column: Requests | Right column: Errors
func (m Model) renderHTTPLayer(ds *stats.DebugStatsAggregate) string {
	var leftCol, rightCol []string

	// === LEFT COLUMN: Requests ===
	leftCol = append(leftCol, labelStyle.Render("Requests"))

	// HTTP Requests (Successful)
	httpStyle := valueStyle
	if ds.HTTPOpenCount > 0 {
		httpStyle = valueGoodStyle
	}
	leftCol = append(leftCol,
		renderMetricRow(
			"  ✅ Successful:",
			formatNumberRaw(ds.HTTPOpenCount),
			formatBracketRate(ds.InstantHTTPRequestsRate),
			&httpStyle,
			&httpStyle,
		),
	)

	// Failed requests (always show, per design spec)
	failedCount := ds.HTTP4xxCount + ds.HTTP5xxCount
	failedPercent := 0.0
	if ds.HTTPOpenCount > 0 {
		failedPercent = float64(failedCount) / float64(ds.HTTPOpenCount)
	}
	failedStyle := valueStyle
	if failedCount > 0 {
		failedStyle = valueBadStyle
	}
	leftCol = append(leftCol,
		renderMetricRow(
			"  ⚠️ Failed:",
			formatNumberRaw(failedCount),
			formatBracketPercent(failedPercent),
			&failedStyle,
			&failedStyle,
		),
	)

	// Reconnects (always show, per design spec)
	reconnectStyle := valueStyle
	if ds.ReconnectCount > 0 {
		reconnectStyle = valueWarnStyle
	}
	leftCol = append(leftCol,
		renderMetricRow(
			"  🔄 Reconnects:",
			formatNumberRaw(ds.ReconnectCount),
			"",
			&reconnectStyle,
			nil,
		),
	)

	// === RIGHT COLUMN: Errors ===
	rightCol = append(rightCol, labelStyle.Render("Errors"))

	// 4xx Client Errors (always show, per design spec)
	http4xxPercent := 0.0
	if ds.HTTPOpenCount > 0 {
		http4xxPercent = float64(ds.HTTP4xxCount) / float64(ds.HTTPOpenCount)
	}
	http4xxStyle := valueStyle
	if ds.HTTP4xxCount > 0 {
		http4xxStyle = valueBadStyle
	}
	rightCol = append(rightCol,
		renderMetricRow(
			"  4xx Client:",
			formatNumberRaw(ds.HTTP4xxCount),
			formatBracketPercent(http4xxPercent),
			&http4xxStyle,
			&http4xxStyle,
		),
	)

	// 5xx Server Errors (always show, per design spec)
	http5xxPercent := 0.0
	if ds.HTTPOpenCount > 0 {
		http5xxPercent = float64(ds.HTTP5xxCount) / float64(ds.HTTPOpenCount)
	}
	http5xxStyle := valueStyle
	if ds.HTTP5xxCount > 0 {
		http5xxStyle = valueBadStyle
	}
	rightCol = append(rightCol,
		renderMetricRow(
			"  5xx Server:",
			formatNumberRaw(ds.HTTP5xxCount),
			formatBracketPercent(http5xxPercent),
			&http5xxStyle,
			&http5xxStyle,
		),
	)

	// Error Rate (always show, per design spec)
	errorRateStyle := GetErrorRateStyle(ds.ErrorRate)
	rightCol = append(rightCol,
		renderMetricRow(
			"  Error Rate:",
			formatPercentRaw(ds.ErrorRate),
			"",
			&errorRateStyle,
			nil,
		),
	)

	// Status indicator (placeholder - will calculate based on error rate)
	statusText := "● Healthy"
	statusStyle := valueGoodStyle
	if ds.ErrorRate > 0.20 {
		statusText = "● Critical"
		statusStyle = valueBadStyle
	} else if ds.ErrorRate > 0.05 {
		statusText = "● Unhealthy"
		statusStyle = valueBadStyle
	} else if ds.ErrorRate > 0.01 {
		statusText = "● Degraded"
		statusStyle = valueWarnStyle
	}
	rightCol = append(rightCol, "") // Empty line separator
	rightCol = append(rightCol,
		renderMetricRow(
			"  Status:",
			statusText,
			"",
			&statusStyle,
			nil,
		),
	)

	// Render two columns
	// Available width: m.width - 2 (borders) - 2 (padding) = m.width - 4
	twoColContent := renderTwoColumns(leftCol, rightCol, m.width-4) // Account for box borders and padding

	// Combine with header and separator
	separator := strings.Repeat("─", m.width-4)
	content := lipgloss.JoinVertical(lipgloss.Left,
		sectionHeaderStyle.Render("🌐 HTTP LAYER (libavformat/http.c)"),
		separator,
		twoColContent,
	)

	return content
}

// renderTCPLayer renders TCP layer metrics in two-column layout (Phase 8.6).
// Left column: Connections | Right column: Connect Latency
func (m Model) renderTCPLayer(ds *stats.DebugStatsAggregate) string {
	var leftCol, rightCol []string

	// === LEFT COLUMN: Connections ===
	leftCol = append(leftCol, labelStyle.Render("Connections"))

	// TCP Success
	if ds.TCPSuccessCount > 0 {
		totalTCP := ds.TCPSuccessCount + ds.TCPRefusedCount + ds.TCPTimeoutCount
		percent := float64(ds.TCPSuccessCount) / float64(totalTCP)
		leftCol = append(leftCol,
			renderMetricRow(
				"  ✅ Success:",
				formatNumberRaw(ds.TCPSuccessCount),
				formatBracketPercent(percent),
				&valueGoodStyle,
				&valueGoodStyle,
			),
		)
	}

	// TCP Refused (always show, per design spec)
	totalTCP := ds.TCPSuccessCount + ds.TCPRefusedCount + ds.TCPTimeoutCount
	tcpRefusedPercent := 0.0
	if totalTCP > 0 {
		tcpRefusedPercent = float64(ds.TCPRefusedCount) / float64(totalTCP)
	}
	tcpRefusedStyle := valueStyle
	if ds.TCPRefusedCount > 0 {
		tcpRefusedStyle = valueBadStyle
	}
	leftCol = append(leftCol,
		renderMetricRow(
			"  🚫 Refused:",
			formatNumberRaw(ds.TCPRefusedCount),
			formatBracketPercent(tcpRefusedPercent),
			&tcpRefusedStyle,
			&tcpRefusedStyle,
		),
	)

	// TCP Timeout (always show, per design spec)
	tcpTimeoutPercent := 0.0
	if totalTCP > 0 {
		tcpTimeoutPercent = float64(ds.TCPTimeoutCount) / float64(totalTCP)
	}
	tcpTimeoutStyle := valueStyle
	if ds.TCPTimeoutCount > 0 {
		tcpTimeoutStyle = valueBadStyle
	}
	leftCol = append(leftCol,
		renderMetricRow(
			"  ⏱️ Timeout:",
			formatNumberRaw(ds.TCPTimeoutCount),
			formatBracketPercent(tcpTimeoutPercent),
			&tcpTimeoutStyle,
			&tcpTimeoutStyle,
		),
	)

	// Health bar (always show, per design spec)
	healthBar := renderHealthBar(ds.TCPHealthRatio, 10)
	healthStyle := valueStyle
	if ds.TCPHealthRatio < 0.9 {
		healthStyle = valueWarnStyle
	}
	if ds.TCPHealthRatio < 0.5 {
		healthStyle = valueBadStyle
	}
	leftCol = append(leftCol, "") // Empty line separator
	// Health: bar in value column, percentage in bracket column
	healthPercentStr := formatPercentRaw(ds.TCPHealthRatio)
	leftCol = append(leftCol,
		renderMetricRow(
			"  Health:",
			healthBar,
			healthPercentStr,
			&healthStyle,
			&healthStyle,
		),
	)

	// === RIGHT COLUMN: Connect Latency ===
	rightCol = append(rightCol, labelStyle.Render("Connect Latency"))

	// Connect Latency (always show, per design spec)
	latencyStyle := valueStyle
	if ds.TCPConnectAvgMs > 100 {
		latencyStyle = valueWarnStyle
	}
	if ds.TCPConnectAvgMs > 500 {
		latencyStyle = valueBadStyle
	}
	// Use 3-column layout for proper alignment
	rightCol = append(rightCol,
		renderMetricRow(
			"  Avg:",
			formatMsRaw(ds.TCPConnectAvgMs),
			"",
			&latencyStyle,
			nil,
		),
	)
	rightCol = append(rightCol,
		renderMetricRow(
			"  Min:",
			formatMsRaw(ds.TCPConnectMinMs),
			"",
			&latencyStyle,
			nil,
		),
	)
	rightCol = append(rightCol,
		renderMetricRow(
			"  Max:",
			formatMsRaw(ds.TCPConnectMaxMs),
			"",
			&latencyStyle,
			nil,
		),
	)
	rightCol = append(rightCol, "") // Empty line
	rightCol = append(rightCol,
		mutedStyle.Render("  (Note: Keep-alive = few connects)"),
	)

	// Render two columns
	// Available width: m.width - 2 (borders) - 2 (padding) = m.width - 4
	twoColContent := renderTwoColumns(leftCol, rightCol, m.width-4) // Account for box borders and padding

	// Combine with header and separator
	separator := strings.Repeat("─", m.width-4)
	content := lipgloss.JoinVertical(lipgloss.Left,
		sectionHeaderStyle.Render("🔌 TCP LAYER (libavformat/network.c)"),
		separator,
		twoColContent,
	)

	return content
}

// renderHealthBar renders a visual health bar using filled/empty circles (Phase 8.6).
// Example: "●●●●●●●●○○" for 80% health with 10 total circles.
func renderHealthBar(ratio float64, totalCircles int) string {
	filled := int(ratio * float64(totalCircles))
	if filled > totalCircles {
		filled = totalCircles
	}
	if filled < 0 {
		filled = 0
	}
	empty := totalCircles - filled
	return strings.Repeat("●", filled) + strings.Repeat("○", empty)
}

// =============================================================================
// Footer
// =============================================================================

func (m Model) renderFooter() string {
	// Keyboard shortcuts
	shortcuts := []string{
		"q: quit",
		"d: toggle details",
		"r: refresh",
	}

	// Stream URL (truncated if needed)
	url := m.streamURL
	maxURLLen := m.width - 60
	if len(url) > maxURLLen && maxURLLen > 10 {
		url = url[:maxURLLen-3] + "..."
	}

	left := dimStyle.Render(strings.Join(shortcuts, " │ "))
	right := dimStyle.Render("Stream: " + url)

	// Pad to fill width
	padding := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if padding < 1 {
		padding = 1
	}

	return footerStyle.Render(
		lipgloss.JoinHorizontal(lipgloss.Left,
			left,
			strings.Repeat(" ", padding),
			right,
		),
	)
}

// =============================================================================
// Helper for time.Duration formatting
// =============================================================================

func formatMsFromDuration(d time.Duration) string {
	ms := d.Milliseconds()
	if ms == 0 && d > 0 {
		return fmt.Sprintf("%d µs", d.Microseconds())
	}
	return fmt.Sprintf("%d ms", ms)
}

// =============================================================================
// Two-Column Layout Helper (Phase 8.6)
// =============================================================================

// renderTwoColumns renders two columns side-by-side with a separator.
// Used for layered dashboard (HLS/HTTP/TCP) to match design specification.
// Fixed widths: left=42, right=42, separator=3, total=87.
// Increased to 42+42 to accommodate 3-column layout within each section:
//   - labelColWidth (18) + valueColWidth (10) + bracketColWidth (12) = 40 chars
//   - Plus 2 chars padding = 42 chars per section
func renderTwoColumns(left, right []string, totalWidth int) string {
	// Fixed column widths - increased to accommodate 3-column layout (label|value|bracket)
	// Each section needs: 18 (label) + 10 (value) + 12 (bracket) = 40 chars, plus 2 chars padding
	const (
		leftColWidth  = 42
		rightColWidth = 42
		separatorWidth = 3 // " │ "
	)

	// Render left column with fixed width
	leftContent := lipgloss.JoinVertical(lipgloss.Left, left...)
	leftStyle := lipgloss.NewStyle().Width(leftColWidth)

	// Render right column with fixed width
	rightContent := lipgloss.JoinVertical(lipgloss.Left, right...)
	rightStyle := lipgloss.NewStyle().Width(rightColWidth)

	// Join with separator
	separator := mutedStyle.Render(" │ ")
	return lipgloss.JoinHorizontal(lipgloss.Top,
		leftStyle.Render(leftContent),
		separator,
		rightStyle.Render(rightContent),
	)
}
