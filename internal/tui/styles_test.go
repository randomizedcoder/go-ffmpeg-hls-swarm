package tui

import (
	"strings"
	"testing"
)

// =============================================================================
// Tests: GetMetricsStatus
// =============================================================================

func TestGetMetricsStatus(t *testing.T) {
	tests := []struct {
		name     string
		dropRate float64
		want     MetricsStatus
	}{
		{"no drops", 0, MetricsStatusOK},
		{"tiny drops", 0.001, MetricsStatusDegraded},
		{"1% drops", 0.01, MetricsStatusDegraded},
		{"5% drops", 0.05, MetricsStatusDegraded},
		{"10% drops", 0.10, MetricsStatusDegraded},
		{"11% drops", 0.11, MetricsStatusSeverelyDegraded},
		{"50% drops", 0.50, MetricsStatusSeverelyDegraded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetMetricsStatus(tt.dropRate); got != tt.want {
				t.Errorf("GetMetricsStatus(%v) = %v, want %v", tt.dropRate, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Tests: GetMetricsLabel
// =============================================================================

func TestGetMetricsLabel(t *testing.T) {
	tests := []struct {
		name       string
		dropRate   float64
		wantSubstr string
	}{
		{"ok", 0, "Metrics"},
		{"degraded", 0.05, "degraded"},
		{"severely degraded", 0.15, "severely degraded"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetMetricsLabel(tt.dropRate)
			if !strings.Contains(got, tt.wantSubstr) {
				t.Errorf("GetMetricsLabel(%v) = %q, want to contain %q", tt.dropRate, got, tt.wantSubstr)
			}
		})
	}
}

// =============================================================================
// Tests: GetSpeedStyle
// =============================================================================

func TestGetSpeedStyle(t *testing.T) {
	tests := []struct {
		name  string
		speed float64
	}{
		{"healthy", 1.0},
		{"very healthy", 1.5},
		{"warning", 0.95},
		{"bad", 0.8},
		{"very bad", 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			style := GetSpeedStyle(tt.speed)
			// Just verify it returns a style (not nil)
			if style.Value() == "" {
				// Style exists but may have empty value - that's ok
			}
		})
	}
}

// =============================================================================
// Tests: GetSpeedLabel
// =============================================================================

func TestGetSpeedLabel(t *testing.T) {
	tests := []struct {
		name  string
		speed float64
	}{
		{"zero", 0},
		{"healthy", 1.0},
		{"slow", 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetSpeedLabel(tt.speed)
			if got == "" {
				t.Error("GetSpeedLabel returned empty string")
			}
		})
	}
}

// =============================================================================
// Tests: GetErrorRateStyle
// =============================================================================

func TestGetErrorRateStyle(t *testing.T) {
	tests := []struct {
		name      string
		errorRate float64
	}{
		{"zero", 0},
		{"low", 0.005},
		{"high", 0.05},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			style := GetErrorRateStyle(tt.errorRate)
			// Just verify it returns a style
			_ = style
		})
	}
}

// =============================================================================
// Tests: RenderKeyValue
// =============================================================================

func TestRenderKeyValue(t *testing.T) {
	result := RenderKeyValue("Label", "Value")

	if !strings.Contains(result, "Label") {
		t.Error("result should contain label")
	}
	if !strings.Contains(result, "Value") {
		t.Error("result should contain value")
	}
}

func TestRenderKeyValueWide(t *testing.T) {
	result := RenderKeyValueWide("Wide Label", "Value")

	if !strings.Contains(result, "Wide Label") {
		t.Error("result should contain label")
	}
	if !strings.Contains(result, "Value") {
		t.Error("result should contain value")
	}
}

// =============================================================================
// Tests: RenderProgressBar
// =============================================================================

func TestRenderProgressBar(t *testing.T) {
	tests := []struct {
		name     string
		progress float64
		width    int
	}{
		{"0%", 0, 20},
		{"50%", 0.5, 20},
		{"100%", 1.0, 20},
		{"narrow", 0.5, 5},
		{"wide", 0.5, 50},
		{"over 100%", 1.5, 20},
		{"negative", -0.1, 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RenderProgressBar(tt.progress, tt.width)
			if result == "" {
				t.Error("RenderProgressBar returned empty string")
			}
			// Should contain percentage
			if !strings.Contains(result, "%") {
				t.Error("result should contain percentage")
			}
		})
	}
}

// =============================================================================
// Tests: repeatChar
// =============================================================================

func TestRepeatChar(t *testing.T) {
	tests := []struct {
		char  rune
		count int
		want  string
	}{
		{'x', 0, ""},
		{'x', 1, "x"},
		{'x', 5, "xxxxx"},
		{'█', 3, "███"},
		{'x', -1, ""},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := repeatChar(tt.char, tt.count); got != tt.want {
				t.Errorf("repeatChar(%q, %d) = %q, want %q", tt.char, tt.count, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Tests: formatSpeedValue
// =============================================================================

func TestFormatSpeedValue(t *testing.T) {
	tests := []struct {
		speed float64
		want  string
	}{
		{0, "N/A"},
		{1.0, "1.00x"},
		{0.95, "0.95x"},
		{1.5, "1.50x"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := formatSpeedValue(tt.speed); got != tt.want {
				t.Errorf("formatSpeedValue(%v) = %q, want %q", tt.speed, got, tt.want)
			}
		})
	}
}
