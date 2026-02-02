package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/stats"
)

// =============================================================================
// Mock StatsSource
// =============================================================================

type mockStatsSource struct {
	stats *stats.AggregatedStats
}

func (m *mockStatsSource) GetAggregatedStats() *stats.AggregatedStats {
	return m.stats
}

// =============================================================================
// Tests: New
// =============================================================================

func TestNew(t *testing.T) {
	cfg := Config{
		TargetClients: 100,
		StreamURL:     "http://example.com/stream.m3u8",
		MetricsAddr:   "localhost:9090",
	}

	model := New(cfg)

	if model.targetClients != 100 {
		t.Errorf("targetClients = %d, want 100", model.targetClients)
	}
	if model.streamURL != "http://example.com/stream.m3u8" {
		t.Errorf("streamURL = %s, want http://example.com/stream.m3u8", model.streamURL)
	}
	if model.metricsAddr != "localhost:9090" {
		t.Errorf("metricsAddr = %s, want localhost:9090", model.metricsAddr)
	}
	if model.width != 80 {
		t.Errorf("width = %d, want 80", model.width)
	}
	if model.height != 24 {
		t.Errorf("height = %d, want 24", model.height)
	}
}

// =============================================================================
// Tests: Init
// =============================================================================

func TestModel_Init(t *testing.T) {
	model := New(Config{TargetClients: 10})
	cmd := model.Init()

	if cmd == nil {
		t.Error("Init() returned nil cmd")
	}
}

// =============================================================================
// Tests: Update - Key Messages
// =============================================================================

func TestModel_Update_QuitKeys(t *testing.T) {
	tests := []struct {
		key      string
		wantQuit bool
	}{
		{"q", true},
		{"ctrl+c", true},
		{"esc", true},
		{"d", false},
		{"r", false},
		{"x", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			model := New(Config{TargetClients: 10})
			msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tt.key)}
			if tt.key == "ctrl+c" {
				msg = tea.KeyMsg{Type: tea.KeyCtrlC}
			} else if tt.key == "esc" {
				msg = tea.KeyMsg{Type: tea.KeyEsc}
			}

			newModel, cmd := model.Update(msg)
			m := newModel.(Model)

			if m.quitting != tt.wantQuit {
				t.Errorf("quitting = %v, want %v", m.quitting, tt.wantQuit)
			}

			if tt.wantQuit && cmd == nil {
				t.Error("expected tea.Quit cmd")
			}
		})
	}
}

func TestModel_Update_ToggleDetailedView(t *testing.T) {
	model := New(Config{TargetClients: 10})

	// Initially not detailed
	if model.detailedView {
		t.Error("detailedView should be false initially")
	}

	// Press 'd' to toggle
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")}
	newModel, _ := model.Update(msg)
	m := newModel.(Model)

	if !m.detailedView {
		t.Error("detailedView should be true after pressing 'd'")
	}

	// Press 'd' again to toggle back
	newModel, _ = m.Update(msg)
	m = newModel.(Model)

	if m.detailedView {
		t.Error("detailedView should be false after pressing 'd' again")
	}
}

// =============================================================================
// Tests: Update - Window Size
// =============================================================================

func TestModel_Update_WindowSize(t *testing.T) {
	model := New(Config{TargetClients: 10})

	msg := tea.WindowSizeMsg{Width: 120, Height: 40}
	newModel, _ := model.Update(msg)
	m := newModel.(Model)

	if m.width != 120 {
		t.Errorf("width = %d, want 120", m.width)
	}
	if m.height != 40 {
		t.Errorf("height = %d, want 40", m.height)
	}
}

// =============================================================================
// Tests: Update - Tick
// =============================================================================

func TestModel_Update_Tick(t *testing.T) {
	mockStats := &stats.AggregatedStats{
		ActiveClients:     50,
		TotalManifestReqs: 1000,
	}
	source := &mockStatsSource{stats: mockStats}

	model := New(Config{
		TargetClients: 100,
		StatsSource:   source,
	})

	msg := TickMsg(time.Now())
	newModel, cmd := model.Update(msg)
	m := newModel.(Model)

	if m.stats == nil {
		t.Error("stats should be set after tick")
	}
	if m.stats.ActiveClients != 50 {
		t.Errorf("ActiveClients = %d, want 50", m.stats.ActiveClients)
	}
	if cmd == nil {
		t.Error("expected tick cmd to be returned")
	}
}

// =============================================================================
// Tests: Update - Stats Message
// =============================================================================

func TestModel_Update_StatsMsg(t *testing.T) {
	model := New(Config{TargetClients: 100})

	mockStats := &stats.AggregatedStats{
		ActiveClients:     75,
		TotalSegmentReqs:  5000,
	}

	msg := StatsMsg{Stats: mockStats}
	newModel, _ := model.Update(msg)
	m := newModel.(Model)

	if m.stats == nil {
		t.Error("stats should be set")
	}
	if m.stats.ActiveClients != 75 {
		t.Errorf("ActiveClients = %d, want 75", m.stats.ActiveClients)
	}
}

// =============================================================================
// Tests: Update - Quit Message
// =============================================================================

func TestModel_Update_QuitMsg(t *testing.T) {
	model := New(Config{TargetClients: 10})

	msg := QuitMsg{}
	newModel, cmd := model.Update(msg)
	m := newModel.(Model)

	if !m.quitting {
		t.Error("quitting should be true")
	}
	if cmd == nil {
		t.Error("expected tea.Quit cmd")
	}
}

// =============================================================================
// Tests: View
// =============================================================================

func TestModel_View_Quitting(t *testing.T) {
	model := New(Config{TargetClients: 10})
	model.quitting = true

	view := model.View()
	if view != "" {
		t.Errorf("View() when quitting should be empty, got %q", view)
	}
}

func TestModel_View_Summary(t *testing.T) {
	model := New(Config{
		TargetClients: 100,
		StreamURL:     "http://example.com/stream.m3u8",
	})
	model.stats = &stats.AggregatedStats{
		ActiveClients:     50,
		TotalManifestReqs: 1000,
		TotalSegmentReqs:  5000,
		TotalBytes:        100000000,
	}

	view := model.View()

	// Should contain key elements
	if len(view) == 0 {
		t.Error("View() returned empty string")
	}
}

// =============================================================================
// Tests: Accessors
// =============================================================================

func TestModel_Elapsed(t *testing.T) {
	model := New(Config{TargetClients: 10})
	time.Sleep(10 * time.Millisecond)

	elapsed := model.Elapsed()
	if elapsed < 10*time.Millisecond {
		t.Errorf("Elapsed() = %v, want >= 10ms", elapsed)
	}
}

func TestModel_ActiveClients(t *testing.T) {
	model := New(Config{TargetClients: 100})

	// Without stats
	if model.ActiveClients() != 0 {
		t.Errorf("ActiveClients() without stats = %d, want 0", model.ActiveClients())
	}

	// With stats
	model.stats = &stats.AggregatedStats{ActiveClients: 50}
	if model.ActiveClients() != 50 {
		t.Errorf("ActiveClients() = %d, want 50", model.ActiveClients())
	}
}

func TestModel_RampProgress(t *testing.T) {
	tests := []struct {
		name          string
		targetClients int
		activeClients int
		want          float64
	}{
		{"zero target", 0, 0, 0},
		{"zero active", 100, 0, 0},
		{"half", 100, 50, 0.5},
		{"full", 100, 100, 1.0},
		{"over", 100, 150, 1.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := New(Config{TargetClients: tt.targetClients})
			if tt.activeClients > 0 {
				model.stats = &stats.AggregatedStats{ActiveClients: tt.activeClients}
			}

			got := model.RampProgress()
			if got != tt.want {
				t.Errorf("RampProgress() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestModel_DropRate(t *testing.T) {
	tests := []struct {
		name    string
		read    int64
		dropped int64
		want    float64
	}{
		{"no data", 0, 0, 0},
		{"no drops", 1000, 0, 0},
		{"some drops", 1000, 10, 0.01},
		{"all dropped", 100, 100, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := New(Config{TargetClients: 10})
			model.stats = &stats.AggregatedStats{
				TotalLinesRead:    tt.read,
				TotalLinesDropped: tt.dropped,
			}

			got := model.DropRate()
			if got != tt.want {
				t.Errorf("DropRate() = %v, want %v", got, tt.want)
			}
		})
	}
}

// =============================================================================
// Tests: Formatting Helpers
// =============================================================================

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "00:00:00"},
		{time.Second, "00:00:01"},
		{time.Minute, "00:01:00"},
		{time.Hour, "01:00:00"},
		{2*time.Hour + 30*time.Minute + 45*time.Second, "02:30:45"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := formatDuration(tt.d); got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{1000000, "1.0M"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := formatNumber(tt.n); got != tt.want {
				t.Errorf("formatNumber(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{999, "999 B"},
		{1000, "1.00 KB"},
		{1000000, "1.00 MB"},
		{1000000000, "1.00 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := formatBytes(tt.n); got != tt.want {
				t.Errorf("formatBytes(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}

func TestFormatMs(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0 ms"},
		{time.Millisecond, "1 ms"},
		{100 * time.Millisecond, "100 ms"},
		{500 * time.Microsecond, "500 µs"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := formatMs(tt.d); got != tt.want {
				t.Errorf("formatMs(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestFormatRate(t *testing.T) {
	tests := []struct {
		rate float64
		want string
	}{
		{0, "0.00/s"},
		{0.5, "0.50/s"},
		{10, "10.0/s"},
		{1000, "1.0K/s"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := formatRate(tt.rate); got != tt.want {
				t.Errorf("formatRate(%v) = %q, want %q", tt.rate, got, tt.want)
			}
		})
	}
}

func TestFormatPercent(t *testing.T) {
	tests := []struct {
		value float64
		want  string
	}{
		{0, "0.0%"},
		{0.5, "50.0%"},
		{1.0, "100.0%"},
		{0.015, "1.5%"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := formatPercent(tt.value); got != tt.want {
				t.Errorf("formatPercent(%v) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}
