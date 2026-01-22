// Package tui provides a live terminal dashboard for HLS load testing.
//
// The TUI uses Bubble Tea for the application framework and Lipgloss for styling.
// It displays real-time metrics including:
// - Client ramp-up progress
// - Request rates and throughput
// - Latency percentiles
// - Playback health
// - Error rates
package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// =============================================================================
// Color Palette
// =============================================================================

// Colors based on a modern dark theme
var (
	// Primary colors
	colorPrimary   = lipgloss.Color("#7C3AED") // Purple
	colorSecondary = lipgloss.Color("#06B6D4") // Cyan
	colorAccent    = lipgloss.Color("#F59E0B") // Amber

	// Status colors
	colorSuccess = lipgloss.Color("#10B981") // Green
	colorWarning = lipgloss.Color("#F59E0B") // Amber
	colorError   = lipgloss.Color("#EF4444") // Red
	colorInfo    = lipgloss.Color("#3B82F6") // Blue

	// Neutral colors
	colorText       = lipgloss.Color("#E5E7EB") // Light gray
	colorTextMuted  = lipgloss.Color("#9CA3AF") // Medium gray
	colorTextDim    = lipgloss.Color("#6B7280") // Dark gray
	colorBackground = lipgloss.Color("#1F2937") // Dark blue-gray
	colorBorder     = lipgloss.Color("#374151") // Border gray
)

// =============================================================================
// Base Styles
// =============================================================================

var (
	// Base text styles
	baseStyle = lipgloss.NewStyle().
			Foreground(colorText)

	mutedStyle = lipgloss.NewStyle().
			Foreground(colorTextMuted)

	dimStyle = lipgloss.NewStyle().
			Foreground(colorTextDim)

	// Bold text
	boldStyle = lipgloss.NewStyle().
			Foreground(colorText).
			Bold(true)

	// Title styles
	titleStyle = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(colorSecondary).
			Bold(true)
)

// =============================================================================
// Status Indicator Styles
// =============================================================================

var (
	// Status indicator styles
	statusOK = lipgloss.NewStyle().
			Foreground(colorSuccess).
			Bold(true)

	statusWarning = lipgloss.NewStyle().
			Foreground(colorWarning).
			Bold(true)

	statusError = lipgloss.NewStyle().
			Foreground(colorError).
			Bold(true)

	statusInfo = lipgloss.NewStyle().
			Foreground(colorInfo).
			Bold(true)
)

// =============================================================================
// Layout Styles
// =============================================================================

var (
	// Box/panel styles
	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)

	// Header style
	headerStyle = lipgloss.NewStyle().
			Foreground(colorText).
			Background(colorPrimary).
			Bold(true).
			Padding(0, 1).
			MarginBottom(1)

	// Section header style
	sectionHeaderStyle = lipgloss.NewStyle().
				Foreground(colorSecondary).
				Bold(true).
				BorderStyle(lipgloss.NormalBorder()).
				BorderBottom(true).
				BorderForeground(colorBorder).
				MarginTop(1)

	// Footer style
	footerStyle = lipgloss.NewStyle().
			Foreground(colorTextMuted).
			MarginTop(1)
)

// =============================================================================
// Value Styles
// =============================================================================

var (
	// Numeric value styles
	valueStyle = lipgloss.NewStyle().
			Foreground(colorText).
			Bold(true)

	valueGoodStyle = lipgloss.NewStyle().
			Foreground(colorSuccess).
			Bold(true)

	valueBadStyle = lipgloss.NewStyle().
			Foreground(colorError).
			Bold(true)

	valueWarnStyle = lipgloss.NewStyle().
			Foreground(colorWarning).
			Bold(true)

	// Label styles
	labelStyle = lipgloss.NewStyle().
			Foreground(colorTextMuted).
			Width(20)

	labelWideStyle = lipgloss.NewStyle().
			Foreground(colorTextMuted).
			Width(25)

	// Unit styles (for ms, KB, etc.)
	unitStyle = lipgloss.NewStyle().
			Foreground(colorTextDim)
)

// =============================================================================
// Progress Bar Styles
// =============================================================================

var (
	progressBarStyle = lipgloss.NewStyle().
				Foreground(colorPrimary)

	progressBarEmptyStyle = lipgloss.NewStyle().
				Foreground(colorBorder)

	progressPercentStyle = lipgloss.NewStyle().
				Foreground(colorText).
				Bold(true)
)

// =============================================================================
// Table Styles
// =============================================================================

var (
	tableHeaderStyle = lipgloss.NewStyle().
				Foreground(colorSecondary).
				Bold(true).
				BorderStyle(lipgloss.NormalBorder()).
				BorderBottom(true).
				BorderForeground(colorBorder)

	tableCellStyle = lipgloss.NewStyle().
			Foreground(colorText).
			PaddingRight(2)

	tableRowEvenStyle = lipgloss.NewStyle().
				Foreground(colorText)

	tableRowOddStyle = lipgloss.NewStyle().
				Foreground(colorTextMuted)
)

// =============================================================================
// Metrics Status Indicator
// =============================================================================

// MetricsStatus represents the health of the metrics pipeline.
type MetricsStatus int

const (
	MetricsStatusOK MetricsStatus = iota
	MetricsStatusDegraded
	MetricsStatusSeverelyDegraded
)

// GetMetricsStatus returns the status based on drop rate.
func GetMetricsStatus(dropRate float64) MetricsStatus {
	switch {
	case dropRate > 0.10: // >10% dropped
		return MetricsStatusSeverelyDegraded
	case dropRate > 0.0: // Any drops
		return MetricsStatusDegraded
	default:
		return MetricsStatusOK
	}
}

// GetMetricsLabel returns a styled label based on drop rate.
func GetMetricsLabel(dropRate float64) string {
	switch GetMetricsStatus(dropRate) {
	case MetricsStatusSeverelyDegraded:
		return statusError.Render("● Metrics (severely degraded)")
	case MetricsStatusDegraded:
		return statusWarning.Render("● Metrics (degraded)")
	default:
		return statusOK.Render("● Metrics")
	}
}

// GetMetricsStyle returns the appropriate style for the metrics status.
func GetMetricsStyle(status MetricsStatus) lipgloss.Style {
	switch status {
	case MetricsStatusSeverelyDegraded:
		return statusError
	case MetricsStatusDegraded:
		return statusWarning
	default:
		return statusOK
	}
}

// =============================================================================
// Speed Status Indicator
// =============================================================================

// GetSpeedStyle returns a style based on playback speed.
func GetSpeedStyle(speed float64) lipgloss.Style {
	switch {
	case speed >= 1.0:
		return valueGoodStyle
	case speed >= 0.9:
		return valueWarnStyle
	default:
		return valueBadStyle
	}
}

// GetSpeedLabel returns a styled speed value.
func GetSpeedLabel(speed float64) string {
	style := GetSpeedStyle(speed)
	return style.Render(formatSpeedValue(speed))
}

func formatSpeedValue(speed float64) string {
	if speed == 0 {
		return "N/A"
	}
	return fmt.Sprintf("%.2fx", speed)
}

// =============================================================================
// Error Rate Indicator
// =============================================================================

// GetErrorRateStyle returns a style based on error rate.
func GetErrorRateStyle(errorRate float64) lipgloss.Style {
	switch {
	case errorRate == 0:
		return valueGoodStyle
	case errorRate < 0.01: // <1%
		return valueWarnStyle
	default:
		return valueBadStyle
	}
}

// =============================================================================
// Helper Functions
// =============================================================================

// RenderKeyValue renders a label-value pair.
func RenderKeyValue(label string, value string) string {
	return lipgloss.JoinHorizontal(lipgloss.Left,
		labelStyle.Render(label+":"),
		valueStyle.Render(value),
	)
}

// RenderKeyValueWide renders a label-value pair with wider label.
func RenderKeyValueWide(label string, value string) string {
	return lipgloss.JoinHorizontal(lipgloss.Left,
		labelWideStyle.Render(label+":"),
		valueStyle.Render(value),
	)
}

// RenderProgressBar renders a progress bar.
func RenderProgressBar(progress float64, width int) string {
	if width < 10 {
		width = 10
	}

	filled := int(progress * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}

	bar := progressBarStyle.Render(repeatChar('█', filled)) +
		progressBarEmptyStyle.Render(repeatChar('░', width-filled))

	percent := progressPercentStyle.Render(fmt.Sprintf(" %3.0f%%", progress*100))

	return bar + percent
}

func repeatChar(char rune, count int) string {
	if count <= 0 {
		return ""
	}
	result := make([]rune, count)
	for i := range result {
		result[i] = char
	}
	return string(result)
}
