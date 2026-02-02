package tui

import (
	"testing"

	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/stats"
)

// TestLayeredDashboardRendering tests that the layered dashboard (HLS/HTTP/TCP) renders correctly
func TestLayeredDashboardRendering(t *testing.T) {
	// Create a model with DebugStats
	model := New(Config{
		TargetClients: 10,
		StreamURL:     "http://example.com/stream.m3u8",
		MetricsAddr:   "localhost:9090",
	})
	model.width = 100
	model.height = 50

	// Create sample DebugStats with data
	debugStats := &stats.DebugStatsAggregate{
		// HLS Layer
		SegmentsDownloaded:   100,
		InstantSegmentsRate:  5.0,
		PlaylistsRefreshed:   50,
		InstantPlaylistsRate: 2.5,
		SegmentsFailed:       2,
		SegmentsSkipped:     1,
		SegmentsExpired:     3,
		PlaylistsFailed:     0,
		SegmentWallTimeAvg:   12.5,
		SegmentWallTimeMax:   45.0,
		PlaylistJitterMax:    15.0,

		// HTTP Layer
		HTTPOpenCount:           150,
		InstantHTTPRequestsRate:  7.5,
		HTTP4xxCount:            1,
		HTTP5xxCount:            2,
		ReconnectCount:          3,
		ErrorRate:               0.02, // 2%

		// TCP Layer
		TCPConnectCount:        20,
		InstantTCPConnectsRate: 1.0,
		TCPSuccessCount:        19,
		TCPRefusedCount:        0,
		TCPTimeoutCount:        1,
		TCPHealthRatio:         0.95, // 95%
		TCPConnectAvgMs:        0.8,
		TCPConnectMinMs:        0.2,
		TCPConnectMaxMs:        5.0,
	}

	model.debugStats = debugStats

	// Test that renderDebugMetrics doesn't panic and returns non-empty string
	output := model.renderDebugMetrics()
	if output == "" {
		t.Error("renderDebugMetrics() returned empty string")
	}

	// Test individual layer rendering
	hlsOutput := model.renderHLSLayer(debugStats)
	if hlsOutput == "" {
		t.Error("renderHLSLayer() returned empty string")
	}
	if len(hlsOutput) < 50 {
		t.Errorf("renderHLSLayer() output too short: %d bytes", len(hlsOutput))
	}

	httpOutput := model.renderHTTPLayer(debugStats)
	if httpOutput == "" {
		t.Error("renderHTTPLayer() returned empty string")
	}
	if len(httpOutput) < 50 {
		t.Errorf("renderHTTPLayer() output too short: %d bytes", len(httpOutput))
	}

	tcpOutput := model.renderTCPLayer(debugStats)
	if tcpOutput == "" {
		t.Error("renderTCPLayer() returned empty string")
	}
	if len(tcpOutput) < 50 {
		t.Errorf("renderTCPLayer() output too short: %d bytes", len(tcpOutput))
	}

	// Verify HLS layer contains expected metrics
	if !contains(hlsOutput, "HLS LAYER") {
		t.Error("HLS layer output missing 'HLS LAYER' header")
	}
	if !contains(hlsOutput, "Segments") {
		t.Error("HLS layer output missing 'Segments' column")
	}
	if !contains(hlsOutput, "Playlists") {
		t.Error("HLS layer output missing 'Playlists' column")
	}
	if !contains(hlsOutput, "Downloaded") {
		t.Error("HLS layer output missing 'Downloaded'")
	}
	if !contains(hlsOutput, "Refreshed") {
		t.Error("HLS layer output missing 'Refreshed'")
	}

	// Verify HTTP layer contains expected metrics
	if !contains(httpOutput, "HTTP LAYER") {
		t.Error("HTTP layer output missing 'HTTP LAYER' header")
	}
	if !contains(httpOutput, "Requests") {
		t.Error("HTTP layer output missing 'Requests' column")
	}
	if !contains(httpOutput, "Errors") {
		t.Error("HTTP layer output missing 'Errors' column")
	}

	// Verify TCP layer contains expected metrics
	if !contains(tcpOutput, "TCP LAYER") {
		t.Error("TCP layer output missing 'TCP LAYER' header")
	}
	if !contains(tcpOutput, "Connections") {
		t.Error("TCP layer output missing 'Connections' column")
	}
	if !contains(tcpOutput, "Connect Latency") {
		t.Error("TCP layer output missing 'Connect Latency' column")
	}
}

// TestLayeredDashboardWithEmptyStats tests that the dashboard handles empty stats gracefully
func TestLayeredDashboardWithEmptyStats(t *testing.T) {
	model := New(Config{TargetClients: 10})
	model.width = 100
	model.height = 50

	// Empty DebugStats (all zeros)
	emptyStats := &stats.DebugStatsAggregate{}
	model.debugStats = emptyStats

	// Should not panic and should return non-empty (even if minimal)
	output := model.renderDebugMetrics()
	if output == "" {
		t.Error("renderDebugMetrics() with empty stats returned empty string")
	}

	// Individual layers should handle empty stats
	hlsOutput := model.renderHLSLayer(emptyStats)
	if hlsOutput == "" {
		t.Error("renderHLSLayer() with empty stats returned empty string")
	}

	httpOutput := model.renderHTTPLayer(emptyStats)
	if httpOutput == "" {
		t.Error("renderHTTPLayer() with empty stats returned empty string")
	}

	tcpOutput := model.renderTCPLayer(emptyStats)
	if tcpOutput == "" {
		t.Error("renderTCPLayer() with empty stats returned empty string")
	}
}

// TestSuccessRateFormatting tests the rate formatting function
func TestSuccessRateFormatting(t *testing.T) {
	tests := []struct {
		rate     float64
		count    int64
		expected string
	}{
		{0.0, 0, "(stalled)"},
		{0.0, 100, "(calculating...)"}, // Has data but no rate yet
		{0.5, 10, "+0.5/s"},
		{1.0, 20, "+1/s"},
		{5.0, 50, "+5/s"},
		{10.0, 100, "+10/s"},
		{100.0, 1000, "+100/s"},
		{1000.0, 10000, "+1.0K/s"},
		{1500.0, 15000, "+1.5K/s"},
		{10000.0, 100000, "+10.0K/s"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatSuccessRate(tt.rate, tt.count)
			if result != tt.expected {
				t.Errorf("formatSuccessRate(%.1f, %d) = %s, want %s", tt.rate, tt.count, result, tt.expected)
			}
		})
	}
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
