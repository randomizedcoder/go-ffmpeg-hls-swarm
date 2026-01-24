package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/stats"
)

// =============================================================================
// Messages
// =============================================================================

// TickMsg is sent periodically to update the display.
type TickMsg time.Time

// StatsMsg carries updated statistics.
type StatsMsg struct {
	Stats      *stats.AggregatedStats
	DebugStats *stats.DebugStatsAggregate
}

// QuitMsg signals the TUI should exit.
type QuitMsg struct{}

// =============================================================================
// Model
// =============================================================================

// Model represents the TUI state.
type Model struct {
	// Configuration
	targetClients int
	streamURL     string
	metricsAddr   string

	// Current state
	stats       *stats.AggregatedStats
	debugStats  *stats.DebugStatsAggregate
	startTime   time.Time
	lastUpdate  time.Time
	detailedView bool

	// Display options
	width  int
	height int

	// Stats source (for fetching updates)
	statsSource StatsSource

	// Debug stats source (optional - for layered metrics)
	debugStatsSource DebugStatsSource

	// Quit flag
	quitting bool
}

// StatsSource provides aggregated statistics.
type StatsSource interface {
	GetAggregatedStats() *stats.AggregatedStats
}

// DebugStatsSource provides layered debug statistics (HLS/HTTP/TCP).
// This is optional - if not provided, the layered dashboard won't be shown.
type DebugStatsSource interface {
	GetDebugStats() stats.DebugStatsAggregate
}

// Config holds TUI configuration.
type Config struct {
	TargetClients    int
	StreamURL        string
	MetricsAddr      string
	StatsSource      StatsSource
	DebugStatsSource DebugStatsSource
}

// New creates a new TUI model.
func New(cfg Config) Model {
	return Model{
		targetClients:    cfg.TargetClients,
		streamURL:        cfg.StreamURL,
		metricsAddr:      cfg.MetricsAddr,
		statsSource:      cfg.StatsSource,
		debugStatsSource: cfg.DebugStatsSource,
		startTime:        time.Now(),
		lastUpdate:       time.Now(),
		width:            80,
		height:           24,
	}
}

// =============================================================================
// Bubble Tea Interface
// =============================================================================

// Init initializes the model.
func (m Model) Init() tea.Cmd {
	// Note: tea.WithAltScreen() is passed when creating the program,
	// so we don't need tea.EnterAltScreen here.
	return tickCmd()
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.quitting = true
			return m, tea.Quit
		case "d":
			m.detailedView = !m.detailedView
			return m, nil
		case "r":
			// Force refresh
			return m, tickCmd()
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case TickMsg:
		// Fetch latest stats
		if m.statsSource != nil {
			m.stats = m.statsSource.GetAggregatedStats()
		}
		// Fetch debug stats for layered dashboard
		if m.debugStatsSource != nil {
			ds := m.debugStatsSource.GetDebugStats()
			m.debugStats = &ds
		}
		m.lastUpdate = time.Now()
		return m, tickCmd()

	case StatsMsg:
		m.stats = msg.Stats
		if msg.DebugStats != nil {
			m.debugStats = msg.DebugStats
		}
		m.lastUpdate = time.Now()
		return m, nil

	case QuitMsg:
		m.quitting = true
		return m, tea.Quit
	}

	return m, nil
}

// View renders the TUI.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	if m.detailedView && m.stats != nil && len(m.stats.PerClientSummaries) > 0 {
		return m.renderDetailedView()
	}
	return m.renderSummaryView()
}

// =============================================================================
// Commands
// =============================================================================

// tickCmd returns a command that sends a tick after 500ms.
func tickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

// =============================================================================
// Accessors
// =============================================================================

// Elapsed returns the time since the test started.
func (m Model) Elapsed() time.Duration {
	return time.Since(m.startTime)
}

// ActiveClients returns the current active client count.
func (m Model) ActiveClients() int {
	if m.stats == nil {
		return 0
	}
	return m.stats.ActiveClients
}

// TargetClients returns the target client count.
func (m Model) TargetClients() int {
	return m.targetClients
}

// RampProgress returns the ramp-up progress (0.0 to 1.0).
func (m Model) RampProgress() float64 {
	if m.targetClients == 0 {
		return 0
	}
	return float64(m.ActiveClients()) / float64(m.targetClients)
}

// DropRate returns the current pipeline drop rate.
func (m Model) DropRate() float64 {
	if m.stats == nil || m.stats.TotalLinesRead == 0 {
		return 0
	}
	return float64(m.stats.TotalLinesDropped) / float64(m.stats.TotalLinesRead)
}

// =============================================================================
// Helper for external use
// =============================================================================

// SendStats sends a stats update to the TUI.
func SendStats(p *tea.Program, stats *stats.AggregatedStats) {
	if p != nil {
		p.Send(StatsMsg{Stats: stats})
	}
}

// SendQuit sends a quit message to the TUI.
func SendQuit(p *tea.Program) {
	if p != nil {
		p.Send(QuitMsg{})
	}
}

// =============================================================================
// Formatting Helpers (used by view.go)
// =============================================================================

// formatDuration formats a duration as HH:MM:SS.
func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

// formatNumber formats a number with K/M suffixes.
func formatNumber(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// formatBytes formats bytes with KB/MB/GB suffixes.
func formatBytes(n int64) string {
	if n >= 1_000_000_000 {
		return fmt.Sprintf("%.2f GB", float64(n)/1_000_000_000)
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.2f MB", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.2f KB", float64(n)/1_000)
	}
	return fmt.Sprintf("%d B", n)
}

// formatMs formats a duration as milliseconds.
func formatMs(d time.Duration) string {
	ms := d.Milliseconds()
	if ms == 0 && d > 0 {
		return fmt.Sprintf("%d µs", d.Microseconds())
	}
	return fmt.Sprintf("%d ms", ms)
}

// formatRate formats a rate with appropriate precision.
func formatRate(rate float64) string {
	if rate >= 1000 {
		return fmt.Sprintf("%.1fK/s", rate/1000)
	}
	if rate >= 1 {
		return fmt.Sprintf("%.1f/s", rate)
	}
	return fmt.Sprintf("%.2f/s", rate)
}

// formatSuccessRate formats a success counter rate with + prefix and stalled indicator (Phase 7.4).
// If rate is 0 but count > 0, shows "calculating..." instead of "(stalled)" to indicate
// we have data but haven't calculated a rate yet (e.g., first TUI tick).
func formatSuccessRate(rate float64, count int64) string {
	if rate >= 1000 {
		return fmt.Sprintf("+%.1fK/s", rate/1000)
	}
	if rate >= 1 {
		return fmt.Sprintf("+%.0f/s", rate)
	}
	if rate > 0 {
		return fmt.Sprintf("+%.1f/s", rate)
	}
	// If we have data but no rate yet, show "calculating..." instead of "(stalled)"
	if count > 0 {
		return "(calculating...)"
	}
	return "(stalled)"
}

// formatPercent formats a percentage.
func formatPercent(value float64) string {
	return fmt.Sprintf("%.1f%%", value*100)
}
