package stats

import (
	"strings"
	"testing"
	"time"
)

// =============================================================================
// Table-Driven Tests: Formatting Functions
// =============================================================================

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{"zero", 0, "00:00:00"},
		{"one second", time.Second, "00:00:01"},
		{"one minute", time.Minute, "00:01:00"},
		{"one hour", time.Hour, "01:00:00"},
		{"mixed", 2*time.Hour + 30*time.Minute + 45*time.Second, "02:30:45"},
		{"24 hours", 24 * time.Hour, "24:00:00"},
		{"sub-second", 500 * time.Millisecond, "00:00:00"},
		{"59 seconds", 59 * time.Second, "00:00:59"},
		{"59 minutes", 59 * time.Minute, "00:59:00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatDuration(tt.duration); got != tt.want {
				t.Errorf("FormatDuration(%v) = %q, want %q", tt.duration, got, tt.want)
			}
		})
	}
}

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		name string
		n    int64
		want string
	}{
		{"zero", 0, "0"},
		{"small", 123, "123"},
		{"999", 999, "999"},
		{"1K", 1000, "1.0K"},
		{"1.5K", 1500, "1.5K"},
		{"10K", 10000, "10.0K"},
		{"999K", 999000, "999.0K"},
		{"1M", 1000000, "1.0M"},
		{"1.5M", 1500000, "1.5M"},
		{"10M", 10000000, "10.0M"},
		{"negative", -100, "-100"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatNumber(tt.n); got != tt.want {
				t.Errorf("FormatNumber(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		name string
		n    int64
		want string
	}{
		{"zero", 0, "0 B"},
		{"small", 123, "123 B"},
		{"999 bytes", 999, "999 B"},
		{"1 KB", 1000, "1.00 KB"},
		{"1.5 KB", 1500, "1.50 KB"},
		{"10 KB", 10000, "10.00 KB"},
		{"1 MB", 1000000, "1.00 MB"},
		{"1.5 MB", 1500000, "1.50 MB"},
		{"1 GB", 1000000000, "1.00 GB"},
		{"1.5 GB", 1500000000, "1.50 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatBytes(tt.n); got != tt.want {
				t.Errorf("FormatBytes(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}

func TestFormatMs(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{"zero", 0, "0 ms"},
		{"1 ms", time.Millisecond, "1 ms"},
		{"100 ms", 100 * time.Millisecond, "100 ms"},
		{"1 second", time.Second, "1000 ms"},
		{"sub-ms", 500 * time.Microsecond, "500 µs"},
		{"1 us", time.Microsecond, "1 µs"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatMs(tt.duration); got != tt.want {
				t.Errorf("FormatMs(%v) = %q, want %q", tt.duration, got, tt.want)
			}
		})
	}
}

func TestFormatRate(t *testing.T) {
	tests := []struct {
		name string
		rate float64
		want string
	}{
		{"zero", 0, "0.00/s"},
		{"small", 0.5, "0.50/s"},
		{"one", 1.0, "1.0/s"},
		{"ten", 10.0, "10.0/s"},
		{"hundred", 100.0, "100.0/s"},
		{"thousand", 1000.0, "1.0K/s"},
		{"1.5K", 1500.0, "1.5K/s"},
		{"10K", 10000.0, "10.0K/s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatRate(tt.rate); got != tt.want {
				t.Errorf("FormatRate(%v) = %q, want %q", tt.rate, got, tt.want)
			}
		})
	}
}

func TestExitCodeLabel(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{0, "(clean)"},
		{1, "(error)"},
		{137, "(SIGKILL)"},
		{143, "(SIGTERM)"},
		{2, ""},
		{-1, ""},
		{255, ""},
	}

	for _, tt := range tests {
		t.Run(string(rune(tt.code)), func(t *testing.T) {
			if got := exitCodeLabel(tt.code); got != tt.want {
				t.Errorf("exitCodeLabel(%d) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Tests: FormatExitSummary
// =============================================================================

func TestFormatExitSummary_NilStats(t *testing.T) {
	cfg := SummaryConfig{
		TargetClients: 100,
		Duration:      5 * time.Minute,
		MetricsAddr:   "localhost:9090",
	}

	result := FormatExitSummary(nil, cfg)

	// Should show basic summary with stats disabled message
	if !strings.Contains(result, "go-ffmpeg-hls-swarm Exit Summary") {
		t.Error("missing title")
	}
	if !strings.Contains(result, "Stats collection was disabled") {
		t.Error("missing disabled message")
	}
	if !strings.Contains(result, "Target Clients:         100") {
		t.Error("missing target clients")
	}
	if !strings.Contains(result, "00:05:00") {
		t.Error("missing duration")
	}
}

func TestFormatExitSummary_BasicStats(t *testing.T) {
	stats := &AggregatedStats{
		TotalClients:      50,
		TotalManifestReqs: 1000,
		TotalSegmentReqs:  5000,
		TotalBytes:        100000000, // 100 MB
		ManifestReqRate:   10.0,
		SegmentReqRate:    50.0,
		ThroughputBytesPerSec: 1000000, // 1 MB/s
		AverageSpeed:      1.05,
		ClientsAboveRealtime: 45,
		ClientsBelowRealtime: 5,
	}

	cfg := SummaryConfig{
		TargetClients: 50,
		Duration:      10 * time.Minute,
		MetricsAddr:   "localhost:9090",
	}

	result := FormatExitSummary(stats, cfg)

	// Check for key sections
	if !strings.Contains(result, "Request Statistics") {
		t.Error("missing Request Statistics section")
	}
	if !strings.Contains(result, "Playback Health") {
		t.Error("missing Playback Health section")
	}
	if !strings.Contains(result, "Manifest (.m3u8)") {
		t.Error("missing manifest row")
	}
	if !strings.Contains(result, "Segments (.ts)") {
		t.Error("missing segments row")
	}
	if !strings.Contains(result, "100.00 MB") {
		t.Error("missing total bytes")
	}
	if !strings.Contains(result, "1.05x") {
		t.Error("missing average speed")
	}
}

func TestFormatExitSummary_WithLatency(t *testing.T) {
	stats := &AggregatedStats{
		TotalClients:         10,
		InferredLatencyP50:   50 * time.Millisecond,
		InferredLatencyP95:   150 * time.Millisecond,
		InferredLatencyP99:   300 * time.Millisecond,
		InferredLatencyMax:   500 * time.Millisecond,
		InferredLatencyCount: 1000,
	}

	cfg := SummaryConfig{
		TargetClients: 10,
		Duration:      time.Minute,
	}

	result := FormatExitSummary(stats, cfg)

	if !strings.Contains(result, "Inferred Segment Latency") {
		t.Error("missing latency section")
	}
	if !strings.Contains(result, "P50 (median)") {
		t.Error("missing P50")
	}
	if !strings.Contains(result, "50 ms") {
		t.Error("missing P50 value")
	}
	if !strings.Contains(result, "Inferred from FFmpeg events") {
		t.Error("missing latency disclaimer")
	}
}

func TestFormatExitSummary_WithErrors(t *testing.T) {
	stats := &AggregatedStats{
		TotalClients: 10,
		TotalHTTPErrors: map[int]int64{
			503: 5,
			404: 2,
		},
		TotalTimeouts:      3,
		TotalReconnections: 10,
		ErrorRate:          0.05, // 5%
	}

	cfg := SummaryConfig{
		TargetClients: 10,
		Duration:      time.Minute,
	}

	result := FormatExitSummary(stats, cfg)

	if !strings.Contains(result, "Errors") {
		t.Error("missing Errors section")
	}
	if !strings.Contains(result, "HTTP 503") {
		t.Error("missing HTTP 503")
	}
	if !strings.Contains(result, "HTTP 404") {
		t.Error("missing HTTP 404")
	}
	if !strings.Contains(result, "Timeouts:") {
		t.Error("missing timeouts")
	}
	if !strings.Contains(result, "Reconnections:") {
		t.Error("missing reconnections")
	}
	if !strings.Contains(result, "5.0000%") {
		t.Error("missing error rate")
	}
}

func TestFormatExitSummary_WithDegradation(t *testing.T) {
	stats := &AggregatedStats{
		TotalClients:      100,
		MetricsDegraded:   true,
		TotalLinesDropped: 5000,
		ClientsWithDrops:  20,
		PeakDropRate:      0.05, // 5%
	}

	cfg := SummaryConfig{
		TargetClients: 100,
		Duration:      time.Minute,
	}

	result := FormatExitSummary(stats, cfg)

	if !strings.Contains(result, "METRICS DEGRADED") {
		t.Error("missing degradation warning")
	}
	if !strings.Contains(result, "5.0K") {
		t.Error("missing lines dropped count")
	}
	if !strings.Contains(result, "20 clients") {
		t.Error("missing clients with drops")
	}
	if !strings.Contains(result, "--stats-buffer") {
		t.Error("missing buffer suggestion")
	}
}

func TestFormatExitSummary_WithDrift(t *testing.T) {
	stats := &AggregatedStats{
		TotalClients:         10,
		AverageDrift:         2 * time.Second,
		MaxDrift:             8 * time.Second,
		ClientsWithHighDrift: 2,
	}

	cfg := SummaryConfig{
		TargetClients: 10,
		Duration:      time.Minute,
	}

	result := FormatExitSummary(stats, cfg)

	if !strings.Contains(result, "Average Drift:") {
		t.Error("missing average drift")
	}
	if !strings.Contains(result, "Max Drift:") {
		t.Error("missing max drift")
	}
	if !strings.Contains(result, "High Drift Clients:") {
		t.Error("missing high drift clients")
	}
}

func TestFormatExitSummary_WithUptime(t *testing.T) {
	stats := &AggregatedStats{
		TotalClients: 10,
	}

	cfg := SummaryConfig{
		TargetClients: 10,
		Duration:      time.Minute,
		UptimeP50:     30 * time.Second,
		UptimeP95:     55 * time.Second,
		UptimeP99:     58 * time.Second,
	}

	result := FormatExitSummary(stats, cfg)

	if !strings.Contains(result, "Uptime Distribution") {
		t.Error("missing uptime section")
	}
	if !strings.Contains(result, "P50 (median):") {
		t.Error("missing P50 uptime")
	}
}

func TestFormatExitSummary_WithExitCodes(t *testing.T) {
	stats := &AggregatedStats{
		TotalClients: 10,
	}

	cfg := SummaryConfig{
		TargetClients: 10,
		Duration:      time.Minute,
		ExitCodes: map[int]int{
			0:   8,
			1:   1,
			143: 1,
		},
	}

	result := FormatExitSummary(stats, cfg)

	if !strings.Contains(result, "Exit Codes") {
		t.Error("missing exit codes section")
	}
	if !strings.Contains(result, "(clean)") {
		t.Error("missing clean exit label")
	}
	if !strings.Contains(result, "(SIGTERM)") {
		t.Error("missing SIGTERM label")
	}
}

func TestFormatExitSummary_WithLifecycle(t *testing.T) {
	stats := &AggregatedStats{
		TotalClients: 10,
	}

	cfg := SummaryConfig{
		TargetClients: 10,
		Duration:      time.Minute,
		TotalStarts:   15,
		TotalRestarts: 5,
	}

	result := FormatExitSummary(stats, cfg)

	if !strings.Contains(result, "Lifecycle") {
		t.Error("missing lifecycle section")
	}
	if !strings.Contains(result, "Total Starts:         15") {
		t.Error("missing total starts")
	}
	if !strings.Contains(result, "Total Restarts:       5") {
		t.Error("missing total restarts")
	}
}

func TestFormatExitSummary_WithUnknownURLs(t *testing.T) {
	stats := &AggregatedStats{
		TotalClients:     10,
		TotalUnknownReqs: 50,
	}

	cfg := SummaryConfig{
		TargetClients: 10,
		Duration:      time.Minute,
	}

	result := FormatExitSummary(stats, cfg)

	if !strings.Contains(result, "Footnotes") {
		t.Error("missing footnotes section")
	}
	if !strings.Contains(result, "Unknown URL requests: 50") {
		t.Error("missing unknown URL footnote")
	}
}

func TestFormatExitSummary_WithInitSegments(t *testing.T) {
	stats := &AggregatedStats{
		TotalClients:  10,
		TotalInitReqs: 100,
	}

	cfg := SummaryConfig{
		TargetClients: 10,
		Duration:      time.Minute,
	}

	result := FormatExitSummary(stats, cfg)

	if !strings.Contains(result, "Init segments") {
		t.Error("missing init segments row")
	}
}

// =============================================================================
// Tests: renderFootnotes
// =============================================================================

func TestRenderFootnotes_Empty(t *testing.T) {
	stats := &AggregatedStats{
		TotalClients: 10,
	}

	result := renderFootnotes(stats)

	if result != "" {
		t.Errorf("expected empty footnotes, got %q", result)
	}
}

func TestRenderFootnotes_WithLatency(t *testing.T) {
	stats := &AggregatedStats{
		TotalClients:         10,
		InferredLatencyCount: 100,
	}

	result := renderFootnotes(stats)

	if !strings.Contains(result, "[1] Latency is inferred") {
		t.Error("missing latency footnote")
	}
}

func TestRenderFootnotes_AllFootnotes(t *testing.T) {
	stats := &AggregatedStats{
		TotalClients:         10,
		InferredLatencyCount: 100,
		TotalUnknownReqs:     50,
		PeakDropRate:         0.05,
	}

	result := renderFootnotes(stats)

	if !strings.Contains(result, "[1]") {
		t.Error("missing footnote 1")
	}
	if !strings.Contains(result, "[2]") {
		t.Error("missing footnote 2")
	}
	if !strings.Contains(result, "[3]") {
		t.Error("missing footnote 3")
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkFormatExitSummary(b *testing.B) {
	stats := &AggregatedStats{
		TotalClients:      100,
		TotalManifestReqs: 10000,
		TotalSegmentReqs:  50000,
		TotalBytes:        1000000000,
		ManifestReqRate:   100.0,
		SegmentReqRate:    500.0,
		ThroughputBytesPerSec: 10000000,
		AverageSpeed:      1.05,
		ClientsAboveRealtime: 90,
		ClientsBelowRealtime: 10,
		InferredLatencyP50:   50 * time.Millisecond,
		InferredLatencyP95:   150 * time.Millisecond,
		InferredLatencyP99:   300 * time.Millisecond,
		InferredLatencyMax:   500 * time.Millisecond,
		InferredLatencyCount: 10000,
		TotalHTTPErrors: map[int]int64{
			503: 10,
			404: 5,
		},
		TotalTimeouts:      5,
		TotalReconnections: 20,
		ErrorRate:          0.01,
	}

	cfg := SummaryConfig{
		TargetClients: 100,
		Duration:      10 * time.Minute,
		MetricsAddr:   "localhost:9090",
		TotalStarts:   120,
		TotalRestarts: 20,
		UptimeP50:     5 * time.Minute,
		UptimeP95:     9 * time.Minute,
		UptimeP99:     10 * time.Minute,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = FormatExitSummary(stats, cfg)
	}
}

func BenchmarkFormatNumber(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = FormatNumber(1234567)
	}
}

func BenchmarkFormatBytes(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = FormatBytes(1234567890)
	}
}
