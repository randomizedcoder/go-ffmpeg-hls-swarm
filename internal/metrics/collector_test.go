package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// =============================================================================
// Test Helpers
// =============================================================================

// newTestRegistry creates a new registry for isolated testing.
func newTestRegistry() *prometheus.Registry {
	return prometheus.NewRegistry()
}

// newTestCollector creates a collector with a test registry.
func newTestCollector(cfg CollectorConfig) (*Collector, *prometheus.Registry) {
	registry := newTestRegistry()
	c := NewCollectorWithRegistry(cfg, registry)
	return c, registry
}

// =============================================================================
// Tests: NewCollector
// =============================================================================

func TestNewCollector(t *testing.T) {
	tests := []struct {
		name string
		cfg  CollectorConfig
	}{
		{
			name: "basic config",
			cfg: CollectorConfig{
				TargetClients: 100,
				TestDuration:  time.Hour,
				StreamURL:     "http://example.com/stream.m3u8",
				Variant:       "all",
			},
		},
		{
			name: "with per-client metrics",
			cfg: CollectorConfig{
				TargetClients:    50,
				TestDuration:     30 * time.Minute,
				StreamURL:        "http://test.com/live.m3u8",
				Variant:          "highest",
				PerClientMetrics: true,
			},
		},
		{
			name: "zero duration (unlimited)",
			cfg: CollectorConfig{
				TargetClients: 10,
				TestDuration:  0,
				StreamURL:     "http://example.com/stream.m3u8",
				Variant:       "lowest",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestCollector(tt.cfg)

			if c == nil {
				t.Fatal("NewCollector returned nil")
			}
			if c.targetClients != tt.cfg.TargetClients {
				t.Errorf("targetClients = %d, want %d", c.targetClients, tt.cfg.TargetClients)
			}
			if c.perClientEnabled != tt.cfg.PerClientMetrics {
				t.Errorf("perClientEnabled = %v, want %v", c.perClientEnabled, tt.cfg.PerClientMetrics)
			}
		})
	}
}

// =============================================================================
// Tests: RecordStats
// =============================================================================

func TestCollector_RecordStats(t *testing.T) {
	c, _ := newTestCollector(CollectorConfig{
		TargetClients: 100,
		TestDuration:  time.Hour,
		StreamURL:     "http://example.com/stream.m3u8",
		Variant:       "all",
	})

	stats := &AggregatedStatsUpdate{
		ActiveClients:         50,
		StalledClients:        2,
		TotalManifestReqs:     1000,
		TotalSegmentReqs:      5000,
		TotalInitReqs:         100,
		TotalUnknownReqs:      10,
		TotalBytes:            100000000,
		ManifestReqRate:       10.5,
		SegmentReqRate:        50.2,
		ThroughputBytesPerSec: 1000000,
		TotalHTTPErrors:       map[int]int64{500: 5, 502: 3},
		TotalReconnections:    10,
		TotalTimeouts:         5,
		ErrorRate:             0.001,
		InferredLatencyP50:    50 * time.Millisecond,
		InferredLatencyP95:    100 * time.Millisecond,
		InferredLatencyP99:    200 * time.Millisecond,
		InferredLatencyMax:    500 * time.Millisecond,
		ClientsAboveRealtime:  45,
		ClientsBelowRealtime:  5,
		AverageSpeed:          1.05,
		ClientsWithHighDrift:  1,
		AverageDrift:          2 * time.Second,
		MaxDrift:              5 * time.Second,
		TotalLinesDropped:     100,
		TotalLinesRead:        10000,
		ClientsWithDrops:      3,
		MetricsDegraded:       false,
		PeakDropRate:          0.02,
		ProgressLinesDropped:  50,
		ProgressLinesRead:     5000,
		StderrLinesDropped:    50,
		StderrLinesRead:       5000,
		UptimeP50:             30 * time.Minute,
		UptimeP95:             55 * time.Minute,
		UptimeP99:             59 * time.Minute,
	}

	// Should not panic
	c.RecordStats(stats)

	// Verify peak active was updated
	if c.peakActive != 50 {
		t.Errorf("peakActive = %d, want 50", c.peakActive)
	}
}

func TestCollector_RecordStats_Deltas(t *testing.T) {
	c, _ := newTestCollector(CollectorConfig{
		TargetClients: 100,
		StreamURL:     "http://example.com/stream.m3u8",
		Variant:       "all",
	})

	// First update
	stats1 := &AggregatedStatsUpdate{
		TotalManifestReqs: 100,
		TotalSegmentReqs:  500,
		TotalBytes:        1000000,
	}
	c.RecordStats(stats1)

	// Verify prev values stored
	if c.prevManifestReqs != 100 {
		t.Errorf("prevManifestReqs = %d, want 100", c.prevManifestReqs)
	}

	// Second update with higher values
	stats2 := &AggregatedStatsUpdate{
		TotalManifestReqs: 200,
		TotalSegmentReqs:  1000,
		TotalBytes:        2000000,
	}
	c.RecordStats(stats2)

	// Verify prev values updated
	if c.prevManifestReqs != 200 {
		t.Errorf("prevManifestReqs = %d, want 200", c.prevManifestReqs)
	}
}

func TestCollector_RecordStats_HTTPErrors(t *testing.T) {
	c, _ := newTestCollector(CollectorConfig{
		TargetClients: 100,
		StreamURL:     "http://example.com/stream.m3u8",
		Variant:       "all",
	})

	// First update with some errors
	stats1 := &AggregatedStatsUpdate{
		TotalHTTPErrors: map[int]int64{500: 5, 502: 3},
	}
	c.RecordStats(stats1)

	// Verify prev values stored
	if c.prevHTTPErrors[500] != 5 {
		t.Errorf("prevHTTPErrors[500] = %d, want 5", c.prevHTTPErrors[500])
	}

	// Second update with more errors
	stats2 := &AggregatedStatsUpdate{
		TotalHTTPErrors: map[int]int64{500: 10, 502: 5, 503: 2},
	}
	c.RecordStats(stats2)

	// Verify prev values updated
	if c.prevHTTPErrors[500] != 10 {
		t.Errorf("prevHTTPErrors[500] = %d, want 10", c.prevHTTPErrors[500])
	}
	if c.prevHTTPErrors[503] != 2 {
		t.Errorf("prevHTTPErrors[503] = %d, want 2", c.prevHTTPErrors[503])
	}
}

func TestCollector_RecordStats_PerClient(t *testing.T) {
	c, _ := newTestCollector(CollectorConfig{
		TargetClients:    10,
		StreamURL:        "http://example.com/stream.m3u8",
		Variant:          "all",
		PerClientMetrics: true,
	})

	stats := &AggregatedStatsUpdate{
		ActiveClients: 3,
		PerClientStats: []PerClientStatsUpdate{
			{ClientID: 1, CurrentSpeed: 1.0, CurrentDrift: time.Second, TotalBytes: 1000},
			{ClientID: 2, CurrentSpeed: 0.95, CurrentDrift: 2 * time.Second, TotalBytes: 2000},
			{ClientID: 3, CurrentSpeed: 1.1, CurrentDrift: 500 * time.Millisecond, TotalBytes: 3000},
		},
	}

	// Should not panic
	c.RecordStats(stats)

	// Verify client IDs registered
	if len(c.registeredClientIDs) != 3 {
		t.Errorf("registeredClientIDs count = %d, want 3", len(c.registeredClientIDs))
	}
}

func TestCollector_RecordStats_PerClientDisabled(t *testing.T) {
	c, _ := newTestCollector(CollectorConfig{
		TargetClients:    10,
		StreamURL:        "http://example.com/stream.m3u8",
		Variant:          "all",
		PerClientMetrics: false, // Disabled
	})

	stats := &AggregatedStatsUpdate{
		ActiveClients: 3,
		PerClientStats: []PerClientStatsUpdate{
			{ClientID: 1, CurrentSpeed: 1.0, CurrentDrift: time.Second, TotalBytes: 1000},
		},
	}

	// Should not panic and should not register clients
	c.RecordStats(stats)

	if len(c.registeredClientIDs) != 0 {
		t.Errorf("registeredClientIDs count = %d, want 0 (per-client disabled)", len(c.registeredClientIDs))
	}
}

// =============================================================================
// Tests: Event Recording
// =============================================================================

func TestCollector_ClientStarted(t *testing.T) {
	c, _ := newTestCollector(CollectorConfig{
		TargetClients: 10,
		StreamURL:     "http://example.com/stream.m3u8",
		Variant:       "all",
	})

	c.ClientStarted()
	c.ClientStarted()
	c.ClientStarted()

	if c.TotalStarts() != 3 {
		t.Errorf("TotalStarts() = %d, want 3", c.TotalStarts())
	}
}

func TestCollector_ClientRestarted(t *testing.T) {
	c, _ := newTestCollector(CollectorConfig{
		TargetClients: 10,
		StreamURL:     "http://example.com/stream.m3u8",
		Variant:       "all",
	})

	c.ClientRestarted()
	c.ClientRestarted()

	if c.TotalRestarts() != 2 {
		t.Errorf("TotalRestarts() = %d, want 2", c.TotalRestarts())
	}
}

func TestCollector_RecordExit(t *testing.T) {
	tests := []struct {
		name     string
		exitCode int
		uptime   time.Duration
	}{
		{"success", 0, 30 * time.Minute},
		{"error", 1, 5 * time.Minute},
		{"signal SIGTERM", 143, 10 * time.Minute},
		{"signal SIGKILL", 137, 1 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestCollector(CollectorConfig{
				TargetClients: 10,
				StreamURL:     "http://example.com/stream.m3u8",
				Variant:       "all",
			})

			c.RecordExit(tt.exitCode, tt.uptime)

			c.mu.Lock()
			if c.exitCodes[tt.exitCode] != 1 {
				t.Errorf("exitCodes[%d] = %d, want 1", tt.exitCode, c.exitCodes[tt.exitCode])
			}
			if len(c.uptimes) != 1 {
				t.Errorf("uptimes length = %d, want 1", len(c.uptimes))
			}
			c.mu.Unlock()
		})
	}
}

func TestCollector_SetActiveCount(t *testing.T) {
	c, _ := newTestCollector(CollectorConfig{
		TargetClients: 100,
		StreamURL:     "http://example.com/stream.m3u8",
		Variant:       "all",
	})

	c.SetActiveCount(50)
	if c.PeakActive() != 50 {
		t.Errorf("PeakActive() = %d, want 50", c.PeakActive())
	}

	c.SetActiveCount(75)
	if c.PeakActive() != 75 {
		t.Errorf("PeakActive() = %d, want 75", c.PeakActive())
	}

	// Lower count shouldn't change peak
	c.SetActiveCount(60)
	if c.PeakActive() != 75 {
		t.Errorf("PeakActive() = %d, want 75 (peak)", c.PeakActive())
	}
}

func TestCollector_SetRampProgress(t *testing.T) {
	c, _ := newTestCollector(CollectorConfig{
		TargetClients: 100,
		StreamURL:     "http://example.com/stream.m3u8",
		Variant:       "all",
	})

	// Should not panic
	c.SetRampProgress(0.5)
	c.SetRampProgress(1.0)
}

func TestCollector_RecordLatency(t *testing.T) {
	c, _ := newTestCollector(CollectorConfig{
		TargetClients: 10,
		StreamURL:     "http://example.com/stream.m3u8",
		Variant:       "all",
	})

	// Should not panic
	c.RecordLatency(50 * time.Millisecond)
	c.RecordLatency(100 * time.Millisecond)
	c.RecordLatency(200 * time.Millisecond)
}

// =============================================================================
// Tests: RemoveClient
// =============================================================================

func TestCollector_RemoveClient(t *testing.T) {
	c, _ := newTestCollector(CollectorConfig{
		TargetClients:    10,
		StreamURL:        "http://example.com/stream.m3u8",
		Variant:          "all",
		PerClientMetrics: true,
	})

	// Add some clients
	stats := &AggregatedStatsUpdate{
		ActiveClients: 3,
		PerClientStats: []PerClientStatsUpdate{
			{ClientID: 1, CurrentSpeed: 1.0},
			{ClientID: 2, CurrentSpeed: 1.0},
			{ClientID: 3, CurrentSpeed: 1.0},
		},
	}
	c.RecordStats(stats)

	// Remove one
	c.RemoveClient(2)

	c.mu.Lock()
	if _, exists := c.registeredClientIDs[2]; exists {
		t.Error("Client 2 should have been removed")
	}
	if len(c.registeredClientIDs) != 2 {
		t.Errorf("registeredClientIDs count = %d, want 2", len(c.registeredClientIDs))
	}
	c.mu.Unlock()
}

func TestCollector_RemoveClient_Disabled(t *testing.T) {
	c, _ := newTestCollector(CollectorConfig{
		TargetClients:    10,
		StreamURL:        "http://example.com/stream.m3u8",
		Variant:          "all",
		PerClientMetrics: false, // Disabled
	})

	// Should not panic even when per-client is disabled
	c.RemoveClient(1)
}

// =============================================================================
// Tests: GenerateSummary
// =============================================================================

func TestCollector_GenerateSummary(t *testing.T) {
	c, _ := newTestCollector(CollectorConfig{
		TargetClients: 100,
		StreamURL:     "http://example.com/stream.m3u8",
		Variant:       "all",
	})

	// Simulate some activity
	c.ClientStarted()
	c.ClientStarted()
	c.ClientRestarted()
	c.SetActiveCount(50)
	c.RecordExit(0, 30*time.Minute)
	c.RecordExit(1, 10*time.Minute)
	c.RecordExit(0, 45*time.Minute)

	// Wait a tiny bit for duration
	time.Sleep(10 * time.Millisecond)

	summary := c.GenerateSummary()

	if summary.TargetClients != 100 {
		t.Errorf("TargetClients = %d, want 100", summary.TargetClients)
	}
	if summary.PeakActiveClients != 50 {
		t.Errorf("PeakActiveClients = %d, want 50", summary.PeakActiveClients)
	}
	if summary.TotalStarts != 2 {
		t.Errorf("TotalStarts = %d, want 2", summary.TotalStarts)
	}
	if summary.TotalRestarts != 1 {
		t.Errorf("TotalRestarts = %d, want 1", summary.TotalRestarts)
	}
	if summary.Duration < 10*time.Millisecond {
		t.Errorf("Duration = %v, want >= 10ms", summary.Duration)
	}
	if summary.ExitCodes[0] != 2 {
		t.Errorf("ExitCodes[0] = %d, want 2", summary.ExitCodes[0])
	}
	if summary.ExitCodes[1] != 1 {
		t.Errorf("ExitCodes[1] = %d, want 1", summary.ExitCodes[1])
	}
}

func TestCollector_GenerateSummary_Empty(t *testing.T) {
	c, _ := newTestCollector(CollectorConfig{
		TargetClients: 10,
		StreamURL:     "http://example.com/stream.m3u8",
		Variant:       "all",
	})

	summary := c.GenerateSummary()

	if summary.TotalStarts != 0 {
		t.Errorf("TotalStarts = %d, want 0", summary.TotalStarts)
	}
	if summary.PeakActiveClients != 0 {
		t.Errorf("PeakActiveClients = %d, want 0", summary.PeakActiveClients)
	}
	if len(summary.ExitCodes) != 0 {
		t.Errorf("ExitCodes length = %d, want 0", len(summary.ExitCodes))
	}
}

// =============================================================================
// Tests: Accessors
// =============================================================================

func TestCollector_PerClientEnabled(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
	}{
		{"enabled", true},
		{"disabled", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestCollector(CollectorConfig{
				TargetClients:    10,
				StreamURL:        "http://example.com/stream.m3u8",
				Variant:          "all",
				PerClientMetrics: tt.enabled,
			})

			if c.PerClientEnabled() != tt.enabled {
				t.Errorf("PerClientEnabled() = %v, want %v", c.PerClientEnabled(), tt.enabled)
			}
		})
	}
}

// =============================================================================
// Tests: Helper Functions
// =============================================================================

func TestSortDurations(t *testing.T) {
	tests := []struct {
		name   string
		input  []time.Duration
		expect []time.Duration
	}{
		{
			name:   "empty",
			input:  []time.Duration{},
			expect: []time.Duration{},
		},
		{
			name:   "single",
			input:  []time.Duration{time.Second},
			expect: []time.Duration{time.Second},
		},
		{
			name:   "already sorted",
			input:  []time.Duration{time.Second, 2 * time.Second, 3 * time.Second},
			expect: []time.Duration{time.Second, 2 * time.Second, 3 * time.Second},
		},
		{
			name:   "reverse",
			input:  []time.Duration{3 * time.Second, 2 * time.Second, time.Second},
			expect: []time.Duration{time.Second, 2 * time.Second, 3 * time.Second},
		},
		{
			name:   "mixed",
			input:  []time.Duration{5 * time.Second, time.Second, 3 * time.Second, 2 * time.Second, 4 * time.Second},
			expect: []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 4 * time.Second, 5 * time.Second},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := make([]time.Duration, len(tt.input))
			copy(input, tt.input)
			sortDurations(input)

			for i, v := range input {
				if v != tt.expect[i] {
					t.Errorf("sortDurations result[%d] = %v, want %v", i, v, tt.expect[i])
				}
			}
		})
	}
}

func TestPercentile(t *testing.T) {
	tests := []struct {
		name   string
		sorted []time.Duration
		p      float64
		expect time.Duration
	}{
		{
			name:   "empty",
			sorted: []time.Duration{},
			p:      0.5,
			expect: 0,
		},
		{
			name:   "single",
			sorted: []time.Duration{time.Second},
			p:      0.5,
			expect: time.Second,
		},
		{
			name:   "p50 of 3",
			sorted: []time.Duration{time.Second, 2 * time.Second, 3 * time.Second},
			p:      0.5,
			expect: 2 * time.Second,
		},
		{
			name:   "p99 of 3",
			sorted: []time.Duration{time.Second, 2 * time.Second, 3 * time.Second},
			p:      0.99,
			expect: 2 * time.Second, // idx = int(2 * 0.99) = 1
		},
		{
			name:   "p0 of 3",
			sorted: []time.Duration{time.Second, 2 * time.Second, 3 * time.Second},
			p:      0.0,
			expect: time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := percentile(tt.sorted, tt.p)
			if result != tt.expect {
				t.Errorf("percentile(%v, %v) = %v, want %v", tt.sorted, tt.p, result, tt.expect)
			}
		})
	}
}

// =============================================================================
// Tests: Thread Safety
// =============================================================================

func TestCollector_ThreadSafety(t *testing.T) {
	c, _ := newTestCollector(CollectorConfig{
		TargetClients:    100,
		StreamURL:        "http://example.com/stream.m3u8",
		Variant:          "all",
		PerClientMetrics: true,
	})

	done := make(chan bool)

	// Concurrent RecordStats
	for i := 0; i < 5; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				c.RecordStats(&AggregatedStatsUpdate{
					ActiveClients:     id * 10,
					TotalManifestReqs: int64(j * 100),
					TotalHTTPErrors:   map[int]int64{500: int64(j)},
					PerClientStats: []PerClientStatsUpdate{
						{ClientID: id, CurrentSpeed: 1.0},
					},
				})
			}
			done <- true
		}(i)
	}

	// Concurrent event recording
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				c.ClientStarted()
				c.ClientRestarted()
				c.RecordExit(0, time.Second)
				c.SetActiveCount(j)
				c.RecordLatency(time.Millisecond * time.Duration(j))
			}
			done <- true
		}()
	}

	// Concurrent reads
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = c.PeakActive()
				_ = c.TotalStarts()
				_ = c.TotalRestarts()
				_ = c.PerClientEnabled()
				_ = c.GenerateSummary()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 15; i++ {
		<-done
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkCollector_RecordStats(b *testing.B) {
	c, _ := newTestCollector(CollectorConfig{
		TargetClients: 100,
		StreamURL:     "http://example.com/stream.m3u8",
		Variant:       "all",
	})

	stats := &AggregatedStatsUpdate{
		ActiveClients:         50,
		TotalManifestReqs:     1000,
		TotalSegmentReqs:      5000,
		TotalBytes:            100000000,
		TotalHTTPErrors:       map[int]int64{500: 5},
		InferredLatencyP50:    50 * time.Millisecond,
		ClientsAboveRealtime:  45,
		ClientsBelowRealtime:  5,
		AverageSpeed:          1.05,
		TotalLinesRead:        10000,
		TotalLinesDropped:     100,
		ProgressLinesRead:     5000,
		ProgressLinesDropped:  50,
		StderrLinesRead:       5000,
		StderrLinesDropped:    50,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.RecordStats(stats)
	}
}

func BenchmarkCollector_RecordStats_PerClient(b *testing.B) {
	c, _ := newTestCollector(CollectorConfig{
		TargetClients:    100,
		StreamURL:        "http://example.com/stream.m3u8",
		Variant:          "all",
		PerClientMetrics: true,
	})

	perClient := make([]PerClientStatsUpdate, 100)
	for i := range perClient {
		perClient[i] = PerClientStatsUpdate{
			ClientID:     i,
			CurrentSpeed: 1.0,
			CurrentDrift: time.Second,
			TotalBytes:   int64(i * 1000),
		}
	}

	stats := &AggregatedStatsUpdate{
		ActiveClients:  100,
		PerClientStats: perClient,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.RecordStats(stats)
	}
}

func BenchmarkCollector_GenerateSummary(b *testing.B) {
	c, _ := newTestCollector(CollectorConfig{
		TargetClients: 100,
		StreamURL:     "http://example.com/stream.m3u8",
		Variant:       "all",
	})

	// Add some data
	for i := 0; i < 100; i++ {
		c.ClientStarted()
		c.RecordExit(0, time.Duration(i)*time.Minute)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.GenerateSummary()
	}
}
