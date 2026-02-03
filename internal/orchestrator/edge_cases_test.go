package orchestrator

import (
	"sync"
	"testing"
	"time"

	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/parser"
)

// TestRampScheduler_EdgeCases tests edge cases in the ramp scheduler.
func TestRampScheduler_EdgeCases(t *testing.T) {
	t.Run("zero_rate", func(t *testing.T) {
		// Rate of 0 should be handled gracefully (no panic, no infinite loop)
		rs := NewRampScheduler(0, 0)
		if rs == nil {
			t.Fatal("NewRampScheduler(0, 0) returned nil")
		}
		// EstimatedRampDuration with 0 rate should not panic
		dur := rs.EstimatedRampDuration(10)
		_ = dur // Just verify no panic
	})

	t.Run("negative_rate", func(t *testing.T) {
		// Negative rate should be handled gracefully
		rs := NewRampScheduler(-5, 0)
		if rs == nil {
			t.Fatal("NewRampScheduler(-5, 0) returned nil")
		}
	})

	t.Run("zero_clients", func(t *testing.T) {
		rs := NewRampScheduler(10, 0)
		dur := rs.EstimatedRampDuration(0)
		// Should return 0 or a reasonable value, not panic
		_ = dur
	})

	t.Run("large_client_count", func(t *testing.T) {
		rs := NewRampScheduler(100, 0)
		// Very large client count - check for overflow
		dur := rs.EstimatedRampDuration(1000000)
		if dur < 0 {
			t.Error("Duration should not be negative (overflow?)")
		}
	})
}

// TestClientManager_ConcurrentStartStop tests concurrent client operations.
func TestClientManager_ConcurrentStartStop(t *testing.T) {
	cm := NewClientManager(ManagerConfig{
		Builder:         &mockProcessBuilder{},
		Logger:          nil,
		StatsEnabled:    true,
		StatsBufferSize: 1000,
	})

	// Concurrently access client manager methods
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// These should not panic
			_ = cm.ActiveCount()
			_ = cm.StartedCount()
			_ = cm.RestartCount()
			_ = cm.ClientCount()
			_ = cm.GetDebugStats()
			_ = cm.GetProgressStats()
			_ = cm.GetAggregatedStats()
		}()
	}
	wg.Wait()
}

// TestDebugEventParser_MalformedInput tests parser handling of malformed input.
func TestDebugEventParser_MalformedInput(t *testing.T) {
	testCases := []string{
		"",                    // Empty line
		"   ",                 // Whitespace only
		"\n\n\n",              // Newlines only
		"not a valid log",     // Random text
		"[hls @ ",             // Incomplete log
		"[hls @ 0x]",          // Invalid hex
		"[http @ 0x123] Opening", // Incomplete Opening log
		"2026-01-30 16:25:36.955", // Timestamp only
		"[debug] " + string(make([]byte, 10000)), // Very long line
		"\x00\x01\x02\x03",    // Binary data
		"[hls @ 0x123] HLS request for url '', offset 0, playlist 0", // Empty URL
		"[hls @ 0x123] HLS request for url 'invalid://url', offset -1, playlist -1", // Negative values
	}

	for _, tc := range testCases {
		t.Run(tc[:min(20, len(tc))], func(t *testing.T) {
			// Parser should not panic on any input
			dp := parser.NewDebugEventParser(1, 2*time.Second, nil)
			// Should not panic
			dp.ParseLine(tc)
			// Should return valid stats
			stats := dp.Stats()
			_ = stats
		})
	}
}

// TestDebugEventParser_HighVolume tests parser under high volume.
func TestDebugEventParser_HighVolume(t *testing.T) {
	dp := parser.NewDebugEventParser(1, 2*time.Second, nil)

	// Parse many lines quickly
	for i := 0; i < 100000; i++ {
		dp.ParseLine("[hls @ 0x123] HLS request for url 'http://example.com/seg.ts', offset 0, playlist 0")
	}

	stats := dp.Stats()
	if stats.LinesProcessed == 0 {
		t.Error("Expected lines to be processed")
	}
}

// TestClientManager_EmptyDebugParsers tests GetDebugStats with no parsers.
func TestClientManager_EmptyDebugParsers(t *testing.T) {
	cm := NewClientManager(ManagerConfig{
		Builder:         &mockProcessBuilder{},
		Logger:          nil,
		StatsEnabled:    true,
		StatsBufferSize: 1000,
	})

	// Get stats with no clients/parsers
	stats := cm.GetDebugStats()

	// Should return zero values, not panic
	if stats.SegmentsDownloaded != 0 {
		t.Errorf("Expected 0 segments, got %d", stats.SegmentsDownloaded)
	}
	if stats.TCPHealthRatio != 1.0 {
		// No connections = healthy (1.0)
		t.Errorf("Expected TCPHealthRatio=1.0 with no clients, got %f", stats.TCPHealthRatio)
	}
}

// TestClientManager_GetClientStats_InvalidID tests getting stats for non-existent client.
func TestClientManager_GetClientStats_InvalidID(t *testing.T) {
	cm := NewClientManager(ManagerConfig{
		Builder:         &mockProcessBuilder{},
		Logger:          nil,
		StatsEnabled:    true,
		StatsBufferSize: 1000,
	})

	// Get stats for client that doesn't exist
	stats := cm.GetClientStats(999)
	if stats != nil {
		t.Error("Expected nil for non-existent client")
	}

	progress := cm.GetClientProgress(999)
	if progress != nil {
		t.Error("Expected nil progress for non-existent client")
	}

	debugStats := cm.GetClientDebugStats(999)
	if debugStats != nil {
		t.Error("Expected nil debug stats for non-existent client")
	}
}

// TestClientManager_GetSupervisor_InvalidID tests getting supervisor for non-existent client.
func TestClientManager_GetSupervisor_InvalidID(t *testing.T) {
	cm := NewClientManager(ManagerConfig{
		Builder:         &mockProcessBuilder{},
		Logger:          nil,
		StatsEnabled:    false,
	})

	sup := cm.GetSupervisor(999)
	if sup != nil {
		t.Error("Expected nil supervisor for non-existent client")
	}
}

// TestClientManager_States_Empty tests States() with no clients.
func TestClientManager_States_Empty(t *testing.T) {
	cm := NewClientManager(ManagerConfig{
		Builder:         &mockProcessBuilder{},
		Logger:          nil,
		StatsEnabled:    false,
	})

	states := cm.States()
	if len(states) != 0 {
		t.Errorf("Expected empty states map, got %d entries", len(states))
	}
}

// min returns the minimum of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
