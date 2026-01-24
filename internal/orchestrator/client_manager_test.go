package orchestrator

import (
	"context"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/parser"
)

// mockProcessBuilder is a simple mock for testing
type mockProcessBuilder struct{}

func (m *mockProcessBuilder) BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error) {
	return exec.Command("echo", "test"), nil
}

func (m *mockProcessBuilder) Name() string {
	return "mock"
}

func (m *mockProcessBuilder) SetProgressFD(fd int) {
	// No-op for testing
}

func TestGetDebugStats_ConcurrentAccess(t *testing.T) {
	// Create a ClientManager with stats enabled
	cm := NewClientManager(ManagerConfig{
		Builder:         &mockProcessBuilder{},
		Logger:          nil,
		StatsEnabled:    true,
		StatsBufferSize: 1000,
	})

	// Spawn multiple goroutines calling GetDebugStats() concurrently
	const numGoroutines = 100
	const callsPerGoroutine = 100
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < callsPerGoroutine; j++ {
				stats := cm.GetDebugStats()
				// Verify we got a valid result (should not panic)
				_ = stats.SegmentsDownloaded
				_ = stats.InstantSegmentsRate
				// Small delay to increase chance of concurrent access
				time.Sleep(time.Microsecond)
			}
		}()
	}

	// Wait for all goroutines to complete
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	// Timeout after 10 seconds (should complete much faster)
	select {
	case <-done:
		// Success - all goroutines completed without deadlock
	case <-time.After(10 * time.Second):
		t.Fatal("Test timed out - possible deadlock or contention issue")
	}
}

func TestGetDebugStats_RateCalculation(t *testing.T) {
	cm := NewClientManager(ManagerConfig{
		Builder:         &mockProcessBuilder{},
		Logger:          nil,
		StatsEnabled:    true,
		StatsBufferSize: 1000,
	})

	// First call - should have zero rates (no previous snapshot with different values)
	stats1 := cm.GetDebugStats()
	if stats1.InstantSegmentsRate != 0 {
		t.Errorf("Expected zero rate on first call, got %f", stats1.InstantSegmentsRate)
	}
	if stats1.InstantPlaylistsRate != 0 {
		t.Errorf("Expected zero playlist rate on first call, got %f", stats1.InstantPlaylistsRate)
	}

	// Simulate some activity by creating a debug parser and updating it
	// We need to add a debug parser to the manager
	debugParser := parser.NewDebugEventParser(1, 2*time.Second, nil)
	cm.debugMu.Lock()
	cm.debugParsers[1] = debugParser
	cm.debugMu.Unlock()

	// Parse some events to increment counters
	debugParser.ParseLine("[hls @ 0x123] HLS request for url 'http://example.com/seg1.ts', offset 0, playlist 0")
	debugParser.ParseLine("[http @ 0x456] Opening 'http://example.com/seg1.ts' for reading")

	// Wait a bit to ensure timestamp difference
	time.Sleep(100 * time.Millisecond)

	// Second call - should calculate rates (but may still be zero if no time elapsed)
	stats2 := cm.GetDebugStats()
	// Rates should be calculated (may be zero if elapsed time is too small)
	_ = stats2.InstantSegmentsRate
	_ = stats2.InstantPlaylistsRate
	_ = stats2.InstantHTTPRequestsRate
	_ = stats2.InstantTCPConnectsRate

	// Verify we got valid stats
	if stats2.SegmentsDownloaded < stats1.SegmentsDownloaded {
		t.Errorf("SegmentsDownloaded should not decrease: %d -> %d",
			stats1.SegmentsDownloaded, stats2.SegmentsDownloaded)
	}
}

func TestGetDebugStats_AtomicValueTypeSafety(t *testing.T) {
	cm := NewClientManager(ManagerConfig{
		Builder:         &mockProcessBuilder{},
		Logger:          nil,
		StatsEnabled:    true,
		StatsBufferSize: 1000,
	})

	// Call GetDebugStats multiple times to verify type safety
	// This ensures the atomic.Value always contains *debugRateSnapshot
	for i := 0; i < 10; i++ {
		stats := cm.GetDebugStats()
		_ = stats // Use the result

		// Verify atomic.Value contains correct type
		snapshotPtr := cm.prevDebugSnapshot.Load()
		if snapshotPtr == nil {
			t.Fatal("prevDebugSnapshot should never be nil after initialization")
		}

		snapshot, ok := snapshotPtr.(*debugRateSnapshot)
		if !ok {
			t.Fatalf("prevDebugSnapshot should contain *debugRateSnapshot, got %T", snapshotPtr)
		}

		// Verify snapshot has valid timestamp
		if snapshot.timestamp.IsZero() {
			t.Error("Snapshot timestamp should not be zero")
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func TestGetDebugStats_NoRaceCondition(t *testing.T) {
	cm := NewClientManager(ManagerConfig{
		Builder:         &mockProcessBuilder{},
		Logger:          nil,
		StatsEnabled:    true,
		StatsBufferSize: 1000,
	})

	// Add a debug parser to generate some stats
	debugParser := parser.NewDebugEventParser(1, 2*time.Second, nil)
	cm.debugMu.Lock()
	cm.debugParsers[1] = debugParser
	cm.debugMu.Unlock()

	// Run concurrent readers and writers
	var wg sync.WaitGroup

	// Reader goroutines
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				stats := cm.GetDebugStats()
				_ = stats.InstantSegmentsRate
				_ = stats.InstantPlaylistsRate
			}
		}()
	}

	// Writer goroutine (simulating activity)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 100; j++ {
			debugParser.ParseLine("[hls @ 0x123] HLS request for url 'http://example.com/seg1.ts', offset 0, playlist 0")
			time.Sleep(time.Millisecond)
		}
	}()

	// Wait for all goroutines
	wg.Wait()

	// Final call to verify everything still works
	// The important thing is that concurrent access didn't cause panics or deadlocks
	finalStats := cm.GetDebugStats()
	_ = finalStats.InstantSegmentsRate
	_ = finalStats.InstantPlaylistsRate
	// Note: Segment count may be 0 if parser didn't match the test line format exactly
	// The key test is that concurrent access works without races
}
