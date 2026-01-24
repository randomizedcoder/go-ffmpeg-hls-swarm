package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/stats"
)

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
		status = statusOK.Render("✓ All clients running")
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
	if m.debugStats == nil || m.debugStats.SegmentWallTimeP50 == 0 {
		return ""
	}

	rows := []string{
		renderLatencyRow("P50 (median)", m.debugStats.SegmentWallTimeP50),
		renderLatencyRow("P95", m.debugStats.SegmentWallTimeP95),
		renderLatencyRow("P99", m.debugStats.SegmentWallTimeP99),
		renderLatencyRow("Max", time.Duration(m.debugStats.SegmentWallTimeMax*float64(time.Millisecond))), // Convert ms to duration
	}

	// Note about accurate timestamps
	note := dimStyle.Render("* Using accurate FFmpeg timestamps")

	content := lipgloss.JoinVertical(lipgloss.Left,
		append([]string{sectionHeaderStyle.Render("Segment Latency *")}, rows...)...,
	)
	content = lipgloss.JoinVertical(lipgloss.Left, content, note)

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
	segRate := formatSuccessRate(ds.InstantSegmentsRate, ds.SegmentsDownloaded)
	leftCol = append(leftCol,
		lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Render("  ✅ Downloaded:"),
			segStyle.Render(fmt.Sprintf("  %s  (%s)", formatNumber(ds.SegmentsDownloaded), segRate)),
		),
	)

	// Segments Failed (always show, per design spec)
	percent := 0.0
	if ds.SegmentsDownloaded > 0 {
		percent = float64(ds.SegmentsFailed) / float64(ds.SegmentsDownloaded) * 100
	}
	failedStyle := valueStyle
	if ds.SegmentsFailed > 0 {
		failedStyle = valueBadStyle
	}
	leftCol = append(leftCol,
		lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Render("  ⚠️ Failed:"),
			failedStyle.Render(fmt.Sprintf("  %s  (%.2f%%)", formatNumber(ds.SegmentsFailed), percent)),
		),
	)

	// Segments Skipped (always show, per design spec)
	skippedStyle := valueStyle
	if ds.SegmentsSkipped > 0 {
		skippedStyle = valueBadStyle
	}
	leftCol = append(leftCol,
		lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Render("  🔴 Skipped:"),
			skippedStyle.Render(fmt.Sprintf("  %s  (data loss)", formatNumber(ds.SegmentsSkipped))),
		),
	)

	// Segments Expired (always show, per design spec)
	expiredStyle := valueStyle
	if ds.SegmentsExpired > 0 {
		expiredStyle = valueWarnStyle
	}
	leftCol = append(leftCol,
		lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Render("  ⏩ Expired:"),
			expiredStyle.Render(fmt.Sprintf("  %s  (fell behind)", formatNumber(ds.SegmentsExpired))),
		),
	)

	// Segment Wall Time (always show, per design spec)
	leftCol = append(leftCol, "") // Empty line separator
	leftCol = append(leftCol, labelStyle.Render("Segment Wall Time"))
	wallTimeStr := fmt.Sprintf("  Avg: %.0fms  Min: %.0fms  Max: %.0fms",
		ds.SegmentWallTimeAvg, ds.SegmentWallTimeMin, ds.SegmentWallTimeMax)
	if ds.SegmentWallTimeAvg == 0 {
		wallTimeStr = "  Avg: 0ms  Min: 0ms  Max: 0ms"
	}
	leftCol = append(leftCol, valueStyle.Render(wallTimeStr))

	// === RIGHT COLUMN: Playlists ===
	rightCol = append(rightCol, labelStyle.Render("Playlists"))

	// Playlists Refreshed
	playlistStyle := valueStyle
	if ds.PlaylistsRefreshed > 0 {
		playlistStyle = valueGoodStyle
	}
	playlistRate := formatSuccessRate(ds.InstantPlaylistsRate, ds.PlaylistsRefreshed)

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

	rightCol = append(rightCol,
		lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Render("  ✅ Refreshed:"),
			playlistStyle.Render(fmt.Sprintf("  %s  (%s)%s", formatNumber(ds.PlaylistsRefreshed), playlistRate, diagnostic)),
		),
	)

	// Playlists Failed (always show, per design spec)
	playlistFailedPercent := 0.0
	if ds.PlaylistsRefreshed > 0 {
		playlistFailedPercent = float64(ds.PlaylistsFailed) / float64(ds.PlaylistsRefreshed) * 100
	}
	playlistFailedStyle := valueStyle
	if ds.PlaylistsFailed > 0 {
		playlistFailedStyle = valueBadStyle
	}
	rightCol = append(rightCol,
		lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Render("  ⚠️ Failed:"),
			playlistFailedStyle.Render(fmt.Sprintf("  %s  (%.2f%%)", formatNumber(ds.PlaylistsFailed), playlistFailedPercent)),
		),
	)

	// Playlist Jitter (always show, per design spec)
	jitterStyle := valueStyle
	if ds.PlaylistJitterMax > 100 {
		jitterStyle = valueWarnStyle
	}
	jitterStr := fmt.Sprintf("  %.0fms avg/%.0fms max", ds.PlaylistJitterAvg, ds.PlaylistJitterMax)
	if ds.PlaylistJitterMax == 0 {
		jitterStr = "  0ms avg/0ms max"
	}
	rightCol = append(rightCol,
		lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Render("  ⏱️ Jitter:"),
			jitterStyle.Render(jitterStr),
		),
	)

	// Playlist Late (always show, per design spec)
	latePercent := 0.0
	if ds.PlaylistsRefreshed > 0 {
		latePercent = float64(ds.PlaylistLateCount) / float64(ds.PlaylistsRefreshed) * 100
	}
	lateStyle := valueStyle
	if ds.PlaylistLateCount > 0 {
		lateStyle = valueWarnStyle
	}
	rightCol = append(rightCol,
		lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Render("  ⏰ Late:"),
			lateStyle.Render(fmt.Sprintf("  %s  (%.1f%%)", formatNumber(ds.PlaylistLateCount), latePercent)),
		),
	)

	// Sequence (always show, per design spec)
	rightCol = append(rightCol, "") // Empty line separator
	rightCol = append(rightCol, labelStyle.Render("Sequence"))
	// Note: SequenceCurrent not yet tracked - using SegmentsDownloaded as approximation
	// In the future, we should track actual sequence numbers from DebugEventParser
	sequenceCurrent := ds.SegmentsDownloaded // Approximation until we track actual sequence
	rightCol = append(rightCol, valueStyle.Render(fmt.Sprintf("  Current: %s   Skips: %s",
		formatNumber(sequenceCurrent), formatNumber(ds.SequenceSkips))))

	// Render two columns
	twoColContent := renderTwoColumns(leftCol, rightCol, m.width-6) // Account for box padding

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
	httpRate := formatSuccessRate(ds.InstantHTTPRequestsRate, ds.HTTPOpenCount)
	leftCol = append(leftCol,
		lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Render("  ✅ Successful:"),
			httpStyle.Render(fmt.Sprintf("  %s  (%s)", formatNumber(ds.HTTPOpenCount), httpRate)),
		),
	)

	// Failed requests (always show, per design spec)
	failedCount := ds.HTTP4xxCount + ds.HTTP5xxCount
	failedPercent := 0.0
	if ds.HTTPOpenCount > 0 {
		failedPercent = float64(failedCount) / float64(ds.HTTPOpenCount) * 100
	}
	failedStyle := valueStyle
	if failedCount > 0 {
		failedStyle = valueBadStyle
	}
	leftCol = append(leftCol,
		lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Render("  ⚠️ Failed:"),
			failedStyle.Render(fmt.Sprintf("  %s  (%.2f%%)", formatNumber(failedCount), failedPercent)),
		),
	)

	// Reconnects (always show, per design spec)
	reconnectStyle := valueStyle
	if ds.ReconnectCount > 0 {
		reconnectStyle = valueWarnStyle
	}
	leftCol = append(leftCol,
		lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Render("  🔄 Reconnects:"),
			reconnectStyle.Render(fmt.Sprintf("  %s", formatNumber(ds.ReconnectCount))),
		),
	)

	// === RIGHT COLUMN: Errors ===
	rightCol = append(rightCol, labelStyle.Render("Errors"))

	// 4xx Client Errors (always show, per design spec)
	http4xxPercent := 0.0
	if ds.HTTPOpenCount > 0 {
		http4xxPercent = float64(ds.HTTP4xxCount) / float64(ds.HTTPOpenCount) * 100
	}
	http4xxStyle := valueStyle
	if ds.HTTP4xxCount > 0 {
		http4xxStyle = valueBadStyle
	}
	rightCol = append(rightCol,
		lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Render("  4xx Client:"),
			http4xxStyle.Render(fmt.Sprintf("  %s  (%.2f%%)", formatNumber(ds.HTTP4xxCount), http4xxPercent)),
		),
	)

	// 5xx Server Errors (always show, per design spec)
	http5xxPercent := 0.0
	if ds.HTTPOpenCount > 0 {
		http5xxPercent = float64(ds.HTTP5xxCount) / float64(ds.HTTPOpenCount) * 100
	}
	http5xxStyle := valueStyle
	if ds.HTTP5xxCount > 0 {
		http5xxStyle = valueBadStyle
	}
	rightCol = append(rightCol,
		lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Render("  5xx Server:"),
			http5xxStyle.Render(fmt.Sprintf("  %s  (%.2f%%)", formatNumber(ds.HTTP5xxCount), http5xxPercent)),
		),
	)

	// Error Rate (always show, per design spec)
	errorRateStyle := GetErrorRateStyle(ds.ErrorRate)
	rightCol = append(rightCol,
		lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Render("  Error Rate:"),
			errorRateStyle.Render(fmt.Sprintf("  %s", formatPercent(ds.ErrorRate))),
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
		lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Render("  Status:"),
			statusStyle.Render(fmt.Sprintf("  %s", statusText)),
		),
	)

	// Render two columns
	twoColContent := renderTwoColumns(leftCol, rightCol, m.width-6) // Account for box padding

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
		percent := float64(ds.TCPSuccessCount) / float64(totalTCP) * 100
		leftCol = append(leftCol,
			lipgloss.JoinHorizontal(lipgloss.Left,
				lipgloss.NewStyle().Render("  ✅ Success:"),
				valueGoodStyle.Render(fmt.Sprintf("  %s  (%.1f%%)", formatNumber(ds.TCPSuccessCount), percent)),
			),
		)
	}

	// TCP Refused (always show, per design spec)
	totalTCP := ds.TCPSuccessCount + ds.TCPRefusedCount + ds.TCPTimeoutCount
	tcpRefusedPercent := 0.0
	if totalTCP > 0 {
		tcpRefusedPercent = float64(ds.TCPRefusedCount) / float64(totalTCP) * 100
	}
	tcpRefusedStyle := valueStyle
	if ds.TCPRefusedCount > 0 {
		tcpRefusedStyle = valueBadStyle
	}
	leftCol = append(leftCol,
		lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Render("  🚫 Refused:"),
			tcpRefusedStyle.Render(fmt.Sprintf("  %s  (%.1f%%)", formatNumber(ds.TCPRefusedCount), tcpRefusedPercent)),
		),
	)

	// TCP Timeout (always show, per design spec)
	tcpTimeoutPercent := 0.0
	if totalTCP > 0 {
		tcpTimeoutPercent = float64(ds.TCPTimeoutCount) / float64(totalTCP) * 100
	}
	tcpTimeoutStyle := valueStyle
	if ds.TCPTimeoutCount > 0 {
		tcpTimeoutStyle = valueBadStyle
	}
	leftCol = append(leftCol,
		lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Render("  ⏱️ Timeout:"),
			tcpTimeoutStyle.Render(fmt.Sprintf("  %s  (%.1f%%)", formatNumber(ds.TCPTimeoutCount), tcpTimeoutPercent)),
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
	leftCol = append(leftCol,
		lipgloss.JoinHorizontal(lipgloss.Left,
			lipgloss.NewStyle().Render("  Health:"),
			healthStyle.Render(fmt.Sprintf("  %s  %s", healthBar, formatPercent(ds.TCPHealthRatio))),
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
	avgStr := fmt.Sprintf("  Avg:   %.1fms", ds.TCPConnectAvgMs)
	minStr := fmt.Sprintf("  Min:   %.1fms", ds.TCPConnectMinMs)
	maxStr := fmt.Sprintf("  Max:   %.1fms", ds.TCPConnectMaxMs)
	if ds.TCPConnectAvgMs == 0 {
		avgStr = "  Avg:   0.0ms"
		minStr = "  Min:   0.0ms"
		maxStr = "  Max:   0.0ms"
	}
	rightCol = append(rightCol, latencyStyle.Render(avgStr))
	rightCol = append(rightCol, latencyStyle.Render(minStr))
	rightCol = append(rightCol, latencyStyle.Render(maxStr))
	rightCol = append(rightCol, "") // Empty line
	rightCol = append(rightCol,
		mutedStyle.Render("  (Note: Keep-alive = few connects)"),
	)

	// Render two columns
	twoColContent := renderTwoColumns(leftCol, rightCol, m.width-6) // Account for box padding

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
func renderTwoColumns(left, right []string, totalWidth int) string {
	// Calculate column widths (account for separator and padding)
	separatorWidth := 3 // " │ "
	padding := 2        // Box padding
	availableWidth := totalWidth - separatorWidth - padding*2

	// Split width roughly in half, but ensure minimum width for each column
	leftWidth := availableWidth / 2
	rightWidth := availableWidth - leftWidth

	// Ensure minimum column width
	if leftWidth < 20 {
		leftWidth = 20
	}
	if rightWidth < 20 {
		rightWidth = 20
	}

	// Render left column
	leftContent := lipgloss.JoinVertical(lipgloss.Left, left...)

	// Render right column
	rightContent := lipgloss.JoinVertical(lipgloss.Left, right...)

	// Join with separator
	separator := mutedStyle.Render(" │ ")
	return lipgloss.JoinHorizontal(lipgloss.Top, leftContent, separator, rightContent)
}
