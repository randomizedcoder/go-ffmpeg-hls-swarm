package tui

import (
	"math"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/stats"
)

// =============================================================================
// Edge Case Tests: Window Sizing
// Common bugs: zero dimensions, very small, very large, negative
// =============================================================================

func TestModel_WindowSize_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		width       int
		height      int
		wantWidth   int
		wantHeight  int
		description string
	}{
		{
			name:        "zero dimensions",
			width:       0,
			height:      0,
			wantWidth:   0,
			wantHeight:  0,
			description: "Should accept zero dimensions without panic",
		},
		{
			name:        "negative width",
			width:       -100,
			height:      24,
			wantWidth:   -100, // Model stores as-is; view should handle
			wantHeight:  24,
			description: "Negative width should be stored (view handles)",
		},
		{
			name:        "negative height",
			width:       80,
			height:      -50,
			wantWidth:   80,
			wantHeight:  -50,
			description: "Negative height should be stored (view handles)",
		},
		{
			name:        "extremely small",
			width:       1,
			height:      1,
			wantWidth:   1,
			wantHeight:  1,
			description: "Single character terminal should not crash",
		},
		{
			name:        "extremely large",
			width:       10000,
			height:      5000,
			wantWidth:   10000,
			wantHeight:  5000,
			description: "Very large terminal should be accepted",
		},
		{
			name:        "very large width (realistic max)",
			width:       500, // Realistic large terminal
			height:      24,
			wantWidth:   500,
			wantHeight:  24,
			description: "Large terminal width should work",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := New(Config{TargetClients: 10})
			msg := tea.WindowSizeMsg{Width: tt.width, Height: tt.height}

			// Should not panic
			newModel, _ := model.Update(msg)
			m := newModel.(Model)

			if m.width != tt.wantWidth {
				t.Errorf("width = %d, want %d", m.width, tt.wantWidth)
			}
			if m.height != tt.wantHeight {
				t.Errorf("height = %d, want %d", m.height, tt.wantHeight)
			}

			// View should not panic even with bad dimensions
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("View() panicked with dimensions (%d, %d): %v",
						tt.width, tt.height, r)
				}
			}()
			_ = m.View()
		})
	}
}

// =============================================================================
// Edge Case Tests: Stats Values
// Common bugs: nil stats, zero values, overflow, negative values
// =============================================================================

func TestModel_Stats_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		stats       *stats.AggregatedStats
		description string
	}{
		{
			name:        "nil stats",
			stats:       nil,
			description: "Nil stats should render without panic",
		},
		{
			name: "all zeros",
			stats: &stats.AggregatedStats{
				ActiveClients:     0,
				TotalManifestReqs: 0,
				TotalSegmentReqs:  0,
				TotalBytes:        0,
			},
			description: "All zeros should render correctly",
		},
		{
			name: "large values",
			stats: &stats.AggregatedStats{
				ActiveClients:     10000,
				TotalManifestReqs: 999999999999,
				TotalSegmentReqs:  999999999999,
				TotalBytes:        999999999999999,
			},
			description: "Large values should display correctly",
		},
		{
			name: "negative values (invalid but defensive)",
			stats: &stats.AggregatedStats{
				ActiveClients:     -1,
				TotalManifestReqs: -100,
				TotalSegmentReqs:  -100,
				TotalBytes:        -1000,
			},
			description: "Negative values should render without panic",
		},
		{
			name: "very high error rate",
			stats: &stats.AggregatedStats{
				ActiveClients:      100,
				ErrorRate:          1.5, // 150% error rate (possible with reconnects)
				TotalHTTPErrors:    map[int]int64{500: 1000},
				TotalReconnections: 500,
				TotalTimeouts:      500,
			},
			description: "Error rate > 1.0 should render correctly",
		},
		{
			name: "NaN error rate",
			stats: &stats.AggregatedStats{
				ActiveClients: 10,
				ErrorRate:     math.NaN(),
			},
			description: "NaN error rate should render safely",
		},
		{
			name: "Inf throughput",
			stats: &stats.AggregatedStats{
				ActiveClients:         10,
				ThroughputBytesPerSec: math.Inf(1),
			},
			description: "Infinite throughput should render safely",
		},
		// Note: Latency edge case tests removed - latency is now in DebugStats, not AggregatedStats
		// See docs/REMOVE_INFERRED_LATENCY_ANALYSIS.md for details
		{
			name: "empty HTTP errors map",
			stats: &stats.AggregatedStats{
				ActiveClients:   100,
				TotalHTTPErrors: map[int]int64{},
			},
			description: "Empty HTTP errors map should render",
		},
		{
			name: "nil HTTP errors map",
			stats: &stats.AggregatedStats{
				ActiveClients:   100,
				TotalHTTPErrors: nil,
			},
			description: "Nil HTTP errors map should not panic",
		},
		{
			name: "many HTTP error codes",
			stats: &stats.AggregatedStats{
				ActiveClients: 100,
				TotalHTTPErrors: map[int]int64{
					400: 10, 401: 20, 403: 30, 404: 40, 500: 50,
					502: 60, 503: 70, 504: 80, 0: 1, -1: 1,
				},
			},
			description: "Many error codes including invalid should render",
		},
		{
			name: "high drop rate",
			stats: &stats.AggregatedStats{
				ActiveClients:     100,
				TotalLinesDropped: 999999,
				TotalLinesRead:    1000000,
				MetricsDegraded:   true,
				PeakDropRate:      0.999,
			},
			description: "99.9% drop rate should render warning",
		},
		{
			name: "zero lines read with drops",
			stats: &stats.AggregatedStats{
				ActiveClients:     10,
				TotalLinesDropped: 100,
				TotalLinesRead:    0, // Edge case: drops without reads
			},
			description: "Zero reads should avoid divide by zero",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := New(Config{TargetClients: 100})
			model.stats = tt.stats
			model.width = 80
			model.height = 24

			// View should not panic
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("View() panicked with stats: %v", r)
				}
			}()

			view := model.View()
			if view == "" && tt.stats != nil {
				t.Error("Expected non-empty view with stats")
			}
		})
	}
}

// =============================================================================
// Edge Case Tests: Per-Client Summaries
// Common bugs: empty slice, nil entries, malformed data
// =============================================================================

func TestModel_PerClientSummaries_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		summaries   []stats.Summary
		description string
	}{
		{
			name:        "empty slice",
			summaries:   []stats.Summary{},
			description: "Empty summaries should show message",
		},
		{
			name:        "nil slice",
			summaries:   nil,
			description: "Nil summaries should not panic",
		},
		{
			name: "single client",
			summaries: []stats.Summary{
				{ClientID: 1, ManifestRequests: 100},
			},
			description: "Single client should render correctly",
		},
		{
			name: "many clients",
			summaries: func() []stats.Summary {
				s := make([]stats.Summary, 100)
				for i := range s {
					s[i] = stats.Summary{ClientID: i, ManifestRequests: int64(i * 10)}
				}
				return s
			}(),
			description: "Many clients should truncate display",
		},
		{
			name: "client with all zeros",
			summaries: []stats.Summary{
				{ClientID: 0, TotalBytes: 0, ManifestRequests: 0, CurrentSpeed: 0},
			},
			description: "All-zero client should render",
		},
		{
			name: "negative client ID",
			summaries: []stats.Summary{
				{ClientID: -1, ManifestRequests: 100},
			},
			description: "Negative client ID should render",
		},
		{
			name: "max client ID",
			summaries: []stats.Summary{
				{ClientID: math.MaxInt32, ManifestRequests: 100},
			},
			description: "Max int client ID should render",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := New(Config{TargetClients: 100})
			model.stats = &stats.AggregatedStats{
				ActiveClients:      len(tt.summaries),
				PerClientSummaries: tt.summaries,
			}
			model.detailedView = true
			model.width = 120
			model.height = 40

			// Should not panic
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("View() panicked: %v", r)
				}
			}()
			_ = model.View()
		})
	}
}

// =============================================================================
// Edge Case Tests: Formatting Functions
// Common bugs: overflow, precision loss, zero handling
// =============================================================================

func TestFormatNumber_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		input  int64
		expect string
	}{
		{"zero", 0, "0"},
		{"negative", -1, "-1"},
		// Note: Negative values aren't converted to K/M (invalid for counts)
		{"negative thousand (raw)", -1000, "-1000"},
		{"negative million (raw)", -1000000, "-1000000"},
		{"max int64", math.MaxInt64, "9223372036854.8M"},
		// Note: MinInt64 stays raw (negative)
		{"min int64 (raw)", math.MinInt64, "-9223372036854775808"},
		{"boundary 999", 999, "999"},
		{"boundary 1000", 1000, "1.0K"},
		{"boundary 999999", 999999, "1000.0K"},
		{"boundary 1000000", 1000000, "1.0M"},
		{"precision edge", 1001, "1.0K"},
		{"precision edge 2", 1999, "2.0K"},
		{"large precision", 1234567, "1.2M"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatNumber(tt.input)
			if result != tt.expect {
				t.Errorf("formatNumber(%d) = %q, want %q", tt.input, result, tt.expect)
			}
		})
	}
}

func TestFormatBytes_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		input  int64
		expect string
	}{
		{"zero", 0, "0 B"},
		{"negative", -1, "-1 B"},
		// Note: Negative values aren't converted to KB/MB/GB (invalid for bytes)
		{"negative KB (raw)", -1024, "-1024 B"},
		{"max int64", math.MaxInt64, "9223372036.85 GB"},
		// Note: MinInt64 stays as raw bytes (negative)
		{"min int64 (raw)", math.MinInt64, "-9223372036854775808 B"},
		{"boundary 999", 999, "999 B"},
		{"boundary 1000", 1000, "1.00 KB"},
		{"boundary GB", 1000000000, "1.00 GB"},
		{"large GB", 1500000000000, "1500.00 GB"},
		{"precision", 1234567890, "1.23 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatBytes(tt.input)
			if result != tt.expect {
				t.Errorf("formatBytes(%d) = %q, want %q", tt.input, result, tt.expect)
			}
		})
	}
}

func TestFormatDuration_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		input  time.Duration
		expect string
	}{
		{"zero", 0, "00:00:00"},
		{"negative", -time.Hour, "-1:00:00"}, // Negative duration
		{"sub-second", 500 * time.Millisecond, "00:00:00"},
		{"exactly one second", time.Second, "00:00:01"},
		{"one hour minus one second", time.Hour - time.Second, "00:59:59"},
		{"24 hours", 24 * time.Hour, "24:00:00"},
		{"many days", 100 * 24 * time.Hour, "2400:00:00"},
		{"max duration", time.Duration(math.MaxInt64), "2562047:47:16"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDuration(tt.input)
			if result != tt.expect {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.input, result, tt.expect)
			}
		})
	}
}

func TestFormatMs_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		input  time.Duration
		expect string
	}{
		{"zero", 0, "0 ms"},
		{"one microsecond", time.Microsecond, "1 µs"},
		{"999 microseconds", 999 * time.Microsecond, "999 µs"},
		{"one millisecond", time.Millisecond, "1 ms"},
		{"negative", -time.Millisecond, "-1 ms"},
		{"large milliseconds", time.Hour, "3600000 ms"},
		{"sub-microsecond", time.Nanosecond, "0 µs"}, // Below display precision
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatMs(tt.input)
			if result != tt.expect {
				t.Errorf("formatMs(%v) = %q, want %q", tt.input, result, tt.expect)
			}
		})
	}
}

func TestFormatRate_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		input  float64
		expect string
	}{
		{"zero", 0, "0.00/s"},
		{"tiny", 0.001, "0.00/s"},
		{"small", 0.1, "0.10/s"},
		{"exactly one", 1.0, "1.0/s"},
		{"boundary 999.9", 999.9, "999.9/s"},
		{"boundary 1000", 1000, "1.0K/s"},
		{"large K", 50000, "50.0K/s"},
		// Note: formatRate uses .2f for < 1, .1f for 1-1000
		{"negative", -100, "-100.00/s"},
		{"very large", 1000000, "1000.0K/s"},
		// Note: Inf >= 1000 so gets K/s suffix
		{"infinity", math.Inf(1), "+InfK/s"},
		{"negative infinity", math.Inf(-1), "-Inf/s"},
		{"NaN", math.NaN(), "NaN/s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatRate(tt.input)
			if result != tt.expect {
				t.Errorf("formatRate(%v) = %q, want %q", tt.input, result, tt.expect)
			}
		})
	}
}

func TestFormatPercent_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		input  float64
		expect string
	}{
		{"zero", 0, "0.0%"},
		{"tiny", 0.0001, "0.0%"},
		{"small", 0.001, "0.1%"},
		{"half", 0.5, "50.0%"},
		{"full", 1.0, "100.0%"},
		{"over 100", 1.5, "150.0%"},
		{"negative", -0.1, "-10.0%"},
		{"infinity", math.Inf(1), "+Inf%"},
		{"NaN", math.NaN(), "NaN%"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatPercent(tt.input)
			if result != tt.expect {
				t.Errorf("formatPercent(%v) = %q, want %q", tt.input, result, tt.expect)
			}
		})
	}
}

// =============================================================================
// Edge Case Tests: Progress Bar
// Common bugs: boundary conditions, width edge cases
// =============================================================================

func TestRenderProgressBar_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		progress float64
		width    int
		checks   []string // Substrings that should appear
	}{
		{
			name:     "zero progress",
			progress: 0,
			width:    20,
			checks:   []string{"0%", "░"},
		},
		{
			name:     "100% progress",
			progress: 1.0,
			width:    20,
			checks:   []string{"100%", "█"},
		},
		{
			name:     "over 100%",
			progress: 1.5,
			width:    20,
			checks:   []string{"150%"}, // Should show actual percentage
		},
		{
			name:     "negative progress",
			progress: -0.5,
			width:    20,
			checks:   []string{"-50%", "░"}, // Should clamp filled to 0
		},
		{
			name:     "NaN progress",
			progress: math.NaN(),
			width:    20,
			checks:   []string{"NaN%"},
		},
		{
			name:     "infinity progress",
			progress: math.Inf(1),
			width:    20,
			checks:   []string{"+Inf%"},
		},
		{
			name:     "very small width",
			progress: 0.5,
			width:    1,
			checks:   []string{"%"}, // Should use minimum width
		},
		{
			name:     "zero width",
			progress: 0.5,
			width:    0,
			checks:   []string{"%"},
		},
		{
			name:     "negative width",
			progress: 0.5,
			width:    -10,
			checks:   []string{"%"},
		},
		{
			name:     "large width",
			progress: 0.5,
			width:    200,
			checks:   []string{"50%"},
		},
		{
			name:     "50% exactly",
			progress: 0.5,
			width:    20,
			checks:   []string{"50%"},
		},
		{
			name:     "precision edge 0.999",
			progress: 0.999,
			width:    20,
			checks:   []string{"100%"},
		},
		{
			name:     "precision edge 0.001",
			progress: 0.001,
			width:    20,
			checks:   []string{"0%"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("RenderProgressBar(%v, %d) panicked: %v",
						tt.progress, tt.width, r)
				}
			}()

			result := RenderProgressBar(tt.progress, tt.width)

			for _, check := range tt.checks {
				if !strings.Contains(result, check) {
					t.Errorf("RenderProgressBar(%v, %d) = %q, want to contain %q",
						tt.progress, tt.width, result, check)
				}
			}
		})
	}
}

// =============================================================================
// Edge Case Tests: Metrics Status
// Common bugs: threshold boundaries
// =============================================================================

func TestGetMetricsStatus_Boundaries(t *testing.T) {
	tests := []struct {
		name     string
		dropRate float64
		want     MetricsStatus
	}{
		// Exact boundaries
		{"exactly 0", 0.0, MetricsStatusOK},
		{"just above 0", 0.000001, MetricsStatusDegraded},
		{"exactly 10%", 0.10, MetricsStatusDegraded},
		{"just above 10%", 0.100001, MetricsStatusSeverelyDegraded},

		// Edge cases
		{"negative (invalid)", -0.1, MetricsStatusOK},
		{"over 100%", 1.5, MetricsStatusSeverelyDegraded},
		{"NaN", math.NaN(), MetricsStatusOK}, // NaN > 0.10 is false
		{"infinity", math.Inf(1), MetricsStatusSeverelyDegraded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetMetricsStatus(tt.dropRate)
			if got != tt.want {
				t.Errorf("GetMetricsStatus(%v) = %v, want %v",
					tt.dropRate, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Edge Case Tests: Speed Status
// Common bugs: speed boundaries, special values
// =============================================================================

func TestGetSpeedStyle_Boundaries(t *testing.T) {
	tests := []struct {
		name  string
		speed float64
	}{
		{"exactly 1.0", 1.0},
		{"just below 1.0", 0.999999},
		{"exactly 0.9", 0.9},
		{"just below 0.9", 0.899999},
		{"zero", 0},
		{"negative", -1.0},
		{"very high", 10.0},
		{"NaN", math.NaN()},
		{"infinity", math.Inf(1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("GetSpeedStyle(%v) panicked: %v", tt.speed, r)
				}
			}()
			_ = GetSpeedStyle(tt.speed)
		})
	}
}

// =============================================================================
// Edge Case Tests: Key Handling
// Common bugs: unknown keys, special key sequences
// =============================================================================

func TestModel_Update_KeyEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		keyType     tea.KeyType
		runes       []rune
		shouldQuit  bool
		description string
	}{
		{
			name:        "empty runes",
			keyType:     tea.KeyRunes,
			runes:       []rune{},
			shouldQuit:  false,
			description: "Empty runes should not crash",
		},
		{
			name:        "null character",
			keyType:     tea.KeyRunes,
			runes:       []rune{0},
			shouldQuit:  false,
			description: "Null character should be ignored",
		},
		{
			name:        "unicode character",
			keyType:     tea.KeyRunes,
			runes:       []rune{'中'},
			shouldQuit:  false,
			description: "Unicode should not crash",
		},
		{
			name:        "emoji",
			keyType:     tea.KeyRunes,
			runes:       []rune{'🔥'},
			shouldQuit:  false,
			description: "Emoji should not crash",
		},
		{
			name:        "escape key",
			keyType:     tea.KeyEsc,
			runes:       nil,
			shouldQuit:  true,
			description: "Escape should quit",
		},
		{
			name:        "unknown key type",
			keyType:     tea.KeyType(255), // Invalid type
			runes:       nil,
			shouldQuit:  false,
			description: "Unknown key type should be ignored",
		},
		{
			name:        "tab key",
			keyType:     tea.KeyTab,
			runes:       nil,
			shouldQuit:  false,
			description: "Tab should be ignored",
		},
		{
			name:        "enter key",
			keyType:     tea.KeyEnter,
			runes:       nil,
			shouldQuit:  false,
			description: "Enter should be ignored",
		},
		{
			name:        "backspace",
			keyType:     tea.KeyBackspace,
			runes:       nil,
			shouldQuit:  false,
			description: "Backspace should be ignored",
		},
		{
			name:        "delete",
			keyType:     tea.KeyDelete,
			runes:       nil,
			shouldQuit:  false,
			description: "Delete should be ignored",
		},
		{
			name:        "arrow up",
			keyType:     tea.KeyUp,
			runes:       nil,
			shouldQuit:  false,
			description: "Arrow up should be ignored",
		},
		{
			name:        "arrow down",
			keyType:     tea.KeyDown,
			runes:       nil,
			shouldQuit:  false,
			description: "Arrow down should be ignored",
		},
		{
			name:        "ctrl+d",
			keyType:     tea.KeyCtrlD,
			runes:       nil,
			shouldQuit:  false,
			description: "Ctrl+D should be ignored (not quit)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := New(Config{TargetClients: 10})
			msg := tea.KeyMsg{Type: tt.keyType, Runes: tt.runes}

			// Should not panic
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Update() panicked: %v", r)
				}
			}()

			newModel, cmd := model.Update(msg)
			m := newModel.(Model)

			if m.quitting != tt.shouldQuit {
				t.Errorf("quitting = %v, want %v", m.quitting, tt.shouldQuit)
			}

			// Check cmd is tea.Quit if shouldQuit
			if tt.shouldQuit && cmd == nil {
				t.Error("expected tea.Quit cmd")
			}
		})
	}
}

// =============================================================================
// Edge Case Tests: Message Types
// Common bugs: nil messages, unknown message types
// =============================================================================

func TestModel_Update_MessageEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		msg         tea.Msg
		description string
	}{
		{
			name:        "nil message",
			msg:         nil,
			description: "Nil message should be handled gracefully",
		},
		{
			name:        "int message",
			msg:         42,
			description: "Unknown int message should be ignored",
		},
		{
			name:        "string message",
			msg:         "hello",
			description: "Unknown string message should be ignored",
		},
		{
			name:        "struct message",
			msg:         struct{ foo string }{foo: "bar"},
			description: "Unknown struct message should be ignored",
		},
		{
			name:        "empty StatsMsg",
			msg:         StatsMsg{Stats: nil},
			description: "StatsMsg with nil Stats should be handled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := New(Config{TargetClients: 10})

			// Should not panic
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Update() panicked with msg type %T: %v", tt.msg, r)
				}
			}()

			newModel, _ := model.Update(tt.msg)
			_ = newModel.(Model)
		})
	}
}

// =============================================================================
// Edge Case Tests: String Truncation and Long Strings
// Common bugs: extremely long URLs, special characters in URLs
// =============================================================================

func TestModel_View_LongStrings(t *testing.T) {
	tests := []struct {
		name      string
		streamURL string
		width     int
	}{
		{
			name:      "very long URL",
			streamURL: "http://example.com/" + strings.Repeat("a", 1000) + "/stream.m3u8",
			width:     80,
		},
		{
			name:      "URL with special chars",
			streamURL: "http://example.com/stream?token=abc&foo=bar%20baz&x=<script>",
			width:     80,
		},
		{
			name:      "URL with unicode",
			streamURL: "http://例え.jp/ストリーム.m3u8",
			width:     80,
		},
		{
			name:      "empty URL",
			streamURL: "",
			width:     80,
		},
		{
			name:      "narrow terminal with long URL",
			streamURL: "http://example.com/very/long/path/to/stream.m3u8",
			width:     20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := New(Config{
				TargetClients: 100,
				StreamURL:     tt.streamURL,
			})
			model.width = tt.width
			model.height = 24
			model.stats = &stats.AggregatedStats{ActiveClients: 50}

			// Should not panic
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("View() panicked: %v", r)
				}
			}()

			view := model.View()
			if view == "" {
				t.Error("Expected non-empty view")
			}
		})
	}
}

// =============================================================================
// Edge Case Tests: Config Edge Cases
// =============================================================================

func TestNew_ConfigEdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{
			name:   "zero clients",
			config: Config{TargetClients: 0},
		},
		{
			name:   "negative clients",
			config: Config{TargetClients: -100},
		},
		{
			name:   "max clients",
			config: Config{TargetClients: math.MaxInt32},
		},
		{
			name:   "nil stats source",
			config: Config{TargetClients: 100, StatsSource: nil},
		},
		{
			name: "all fields empty",
			config: Config{
				TargetClients: 0,
				StreamURL:     "",
				MetricsAddr:   "",
				StatsSource:   nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("New() panicked: %v", r)
				}
			}()

			model := New(tt.config)

			// View should also not panic
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("View() panicked: %v", r)
				}
			}()
			_ = model.View()
		})
	}
}

// =============================================================================
// Edge Case Tests: Concurrent Access (Race Conditions)
// Note: This test is for read-only accessors which should be safe.
// The Model is not designed for concurrent mutation - that's handled
// by the Bubble Tea framework.
// =============================================================================

func TestModel_ConcurrentReadAccess(t *testing.T) {
	model := New(Config{TargetClients: 100})
	model.stats = &stats.AggregatedStats{ActiveClients: 50}
	model.width = 80
	model.height = 24

	// Run multiple goroutines reading the model (read-only)
	done := make(chan bool)
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				_ = model.ActiveClients()
				_ = model.RampProgress()
				_ = model.DropRate()
				_ = model.Elapsed()
				_ = model.TargetClients()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 5; i++ {
		<-done
	}
}

// =============================================================================
// Edge Case Tests: repeatChar helper
// =============================================================================

func TestRepeatChar_EdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		char  rune
		count int
		want  string
	}{
		{"zero count", 'x', 0, ""},
		{"negative count", 'x', -1, ""},
		{"large negative", 'x', -1000, ""},
		{"reasonable large", 'x', 500, strings.Repeat("x", 500)},
		{"unicode char", '中', 3, "中中中"},
		{"emoji", '🔥', 2, "🔥🔥"},
		{"null char", 0, 3, "\x00\x00\x00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := repeatChar(tt.char, tt.count)
			if result != tt.want {
				t.Errorf("repeatChar(%q, %d) = %q, want %q",
					tt.char, tt.count, result, tt.want)
			}
		})
	}
}
