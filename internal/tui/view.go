package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
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
	if m.stats == nil {
		return ""
	}

	s := m.stats

	// Only show if we have latency data
	if s.InferredLatencyCount == 0 {
		return ""
	}

	rows := []string{
		renderLatencyRow("P50 (median)", s.InferredLatencyP50),
		renderLatencyRow("P95", s.InferredLatencyP95),
		renderLatencyRow("P99", s.InferredLatencyP99),
		renderLatencyRow("Max", s.InferredLatencyMax),
	}

	// Add note about inferred latency
	note := dimStyle.Render("* Inferred from FFmpeg events")

	content := lipgloss.JoinVertical(lipgloss.Left,
		append([]string{sectionHeaderStyle.Render("Inferred Segment Latency *")}, rows...)...,
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
