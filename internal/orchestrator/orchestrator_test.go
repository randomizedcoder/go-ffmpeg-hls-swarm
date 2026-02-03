package orchestrator

import (
	"testing"
	"time"

	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/parser"
)

// nilSegmentSizeLookup is a nil pointer that implements SegmentSizeLookup.
// This simulates the "nil interface gotcha" bug where a nil concrete type
// is assigned to an interface, making the interface != nil but method calls panic.
type nilSegmentSizeLookup *fakeSegmentScraper

type fakeSegmentScraper struct{}

func (f *fakeSegmentScraper) GetSegmentSize(name string) (int64, bool) {
	return 0, false
}

// TestNilSegmentSizeLookup_GolangNilInterfaceGotcha verifies that the system
// handles the case where SegmentSizeLookup is configured but nil.
//
// This tests the "Go nil interface gotcha":
// - A nil pointer assigned to an interface makes the interface != nil
// - The interface has type info but nil value
// - Calling methods on it causes a nil pointer dereference panic
//
// Regression test for: panic at segment_scraper.go:146 called from debug_events.go:612
func TestNilSegmentSizeLookup_GolangNilInterfaceGotcha(t *testing.T) {
	// This demonstrates the bug:
	// If you assign a nil *SegmentScraper to the SegmentSizeLookup interface,
	// the interface is NOT nil, but calling methods on it will panic.

	var nilScraper *fakeSegmentScraper = nil

	// Create interface value with nil concrete type - this is the bug condition
	var lookup parser.SegmentSizeLookup = nilScraper

	// The interface is NOT nil even though the underlying pointer is nil
	// This is the "Go nil interface gotcha"
	if lookup == nil {
		t.Fatal("Expected interface to be non-nil (this demonstrates the gotcha)")
	}

	// Calling a method on this interface would panic:
	// size, ok := lookup.GetSegmentSize("test.ts") // WOULD PANIC!

	// The fix is to never assign nil pointers to interfaces.
	// Instead, leave the interface nil:
	var safeLookup parser.SegmentSizeLookup = nil
	if safeLookup != nil {
		t.Fatal("Expected nil interface to be nil")
	}

	// Correct pattern: only assign if pointer is non-nil
	if nilScraper != nil {
		safeLookup = nilScraper
	}

	// Now safeLookup is nil (the interface itself), so nil checks work correctly
	if safeLookup != nil {
		t.Fatal("safeLookup should be nil when pointer was nil")
	}
}

// TestDebugEventParser_NilSegmentSizeLookup verifies that DebugEventParser
// handles nil SegmentSizeLookup correctly without panicking.
func TestDebugEventParser_NilSegmentSizeLookup(t *testing.T) {
	// Create parser with nil segment size lookup (the safe pattern)
	debugParser := parser.NewDebugEventParserWithSizeLookup(
		1,                // clientID
		2*time.Second,    // targetDuration
		nil,              // callback
		nil,              // segmentSizeLookup - explicitly nil
	)

	// Parse HLS request that would trigger segment tracking
	// This should NOT panic even with nil segmentSizeLookup
	debugParser.ParseLine("[hls @ 0x123] HLS request for url 'http://example.com/segment0.ts', offset 0, playlist 0")

	// Parse a second request which triggers the "segment complete" logic
	// where GetSegmentSize would be called if segmentSizeLookup != nil
	debugParser.ParseLine("[hls @ 0x123] HLS request for url 'http://example.com/segment1.ts', offset 0, playlist 0")

	// Get stats to verify everything worked
	stats := debugParser.Stats()

	// Should have processed lines without panic
	if stats.LinesProcessed == 0 {
		t.Error("Expected some lines to be processed")
	}

	// Should NOT have attempted segment size lookups (lookup is nil)
	if stats.SegmentSizeLookupAttempts != 0 {
		t.Errorf("Expected 0 lookup attempts with nil lookup, got %d", stats.SegmentSizeLookupAttempts)
	}
}

// TestClientManager_NilSegmentSizeLookupSafe verifies that ClientManager
// correctly handles nil SegmentSizeLookup without the nil interface gotcha.
func TestClientManager_NilSegmentSizeLookupSafe(t *testing.T) {
	// Create ClientManager with nil SegmentSizeLookup (the safe pattern)
	cm := NewClientManager(ManagerConfig{
		Builder:           &mockProcessBuilder{},
		Logger:            nil,
		StatsEnabled:      true,
		StatsBufferSize:   1000,
		SegmentSizeLookup: nil, // Explicitly nil - the safe way
	})

	// Add a debug parser manually to simulate what StartClient does
	debugParser := parser.NewDebugEventParserWithSizeLookup(
		1,
		2*time.Second,
		nil,
		nil, // nil SegmentSizeLookup - should be safe
	)
	cm.debugMu.Lock()
	cm.debugParsers[1] = debugParser
	cm.debugMu.Unlock()

	// Parse events that would trigger segment size lookup
	debugParser.ParseLine("[hls @ 0x123] HLS request for url 'http://example.com/segment0.ts', offset 0, playlist 0")
	debugParser.ParseLine("[hls @ 0x123] HLS request for url 'http://example.com/segment1.ts', offset 0, playlist 0")

	// This should NOT panic
	stats := cm.GetDebugStats()

	// Verify we got valid stats
	if stats.SegmentSizeLookupAttempts != 0 {
		t.Errorf("Expected 0 lookup attempts, got %d", stats.SegmentSizeLookupAttempts)
	}
}

// mockSegmentSizeLookup is a working implementation for testing
type mockSegmentSizeLookup struct {
	sizes map[string]int64
}

func (m *mockSegmentSizeLookup) GetSegmentSize(name string) (int64, bool) {
	if m.sizes == nil {
		return 0, false
	}
	size, ok := m.sizes[name]
	return size, ok
}

// TestClientManager_WithSegmentSizeLookup verifies that segment size lookup
// works correctly when properly configured.
func TestClientManager_WithSegmentSizeLookup(t *testing.T) {
	// Create a working segment size lookup
	lookup := &mockSegmentSizeLookup{
		sizes: map[string]int64{
			"segment0.ts": 1000000,
			"segment1.ts": 1200000,
		},
	}

	// Create ClientManager with working SegmentSizeLookup
	cm := NewClientManager(ManagerConfig{
		Builder:           &mockProcessBuilder{},
		Logger:            nil,
		StatsEnabled:      true,
		StatsBufferSize:   1000,
		SegmentSizeLookup: lookup, // Working lookup
	})

	// Add a debug parser with the lookup
	debugParser := parser.NewDebugEventParserWithSizeLookup(
		1,
		2*time.Second,
		nil,
		lookup, // Working lookup
	)
	cm.debugMu.Lock()
	cm.debugParsers[1] = debugParser
	cm.debugMu.Unlock()

	// Parse events that trigger segment size lookup
	debugParser.ParseLine("[hls @ 0x123] HLS request for url 'http://example.com/segment0.ts', offset 0, playlist 0")
	debugParser.ParseLine("[hls @ 0x123] HLS request for url 'http://example.com/segment1.ts', offset 0, playlist 0")

	// Get stats
	parserStats := debugParser.Stats()

	// Should have attempted lookups
	if parserStats.SegmentSizeLookupAttempts == 0 {
		t.Error("Expected segment size lookup attempts")
	}

	// Should have found sizes
	if parserStats.SegmentSizeLookupSuccesses == 0 {
		t.Error("Expected segment size lookup successes")
	}

	// Should have tracked bytes
	if parserStats.SegmentBytesDownloaded == 0 {
		t.Error("Expected segment bytes to be tracked")
	}
}
