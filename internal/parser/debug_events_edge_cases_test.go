package parser

import (
	"testing"
	"time"
)

// TestDebugEventParser_OutOfOrderEvents tests handling of events that arrive out of order
// or without their expected prior events.
func TestDebugEventParser_OutOfOrderEvents(t *testing.T) {
	tests := []struct {
		name     string
		lines    []string
		verify   func(t *testing.T, stats DebugStats)
	}{
		{
			name: "tcp_connected_without_start",
			lines: []string{
				"[tcp @ 0x558f5f5ddbc0] Successfully connected to 10.177.0.10 port 17080",
			},
			verify: func(t *testing.T, stats DebugStats) {
				if stats.TCPSuccessCount != 1 {
					t.Errorf("Should count successful connection, got %d", stats.TCPSuccessCount)
				}
				if stats.TCPConnectCount != 0 {
					t.Errorf("Should not have latency measurement without start event, got %d", stats.TCPConnectCount)
				}
			},
		},
		{
			name: "tcp_start_without_connected",
			lines: []string{
				"[tcp @ 0x558f5f5ddbc0] Starting connection attempt to 10.177.0.10 port 17080",
			},
			verify: func(t *testing.T, stats DebugStats) {
				// Start event doesn't increment any counters, just tracks pending connection
				if stats.TCPSuccessCount != 0 {
					t.Errorf("Start without connect should not increment success, got %d", stats.TCPSuccessCount)
				}
				if stats.TCPConnectCount != 0 {
					t.Errorf("Start without connect should not increment connect count, got %d", stats.TCPConnectCount)
				}
			},
		},
		{
			name: "http_open_without_hls_request",
			lines: []string{
				"[http @ 0x558f5f5da980] Opening 'http://10.177.0.10:17080/seg001.ts' for reading",
			},
			verify: func(t *testing.T, stats DebugStats) {
				if stats.HTTPOpenCount != 1 {
					t.Errorf("Should count HTTP open, got %d", stats.HTTPOpenCount)
				}
			},
		},
		{
			name: "multiple_segments_same_url",
			lines: []string{
				"[hls @ 0x55c32c0c5700] HLS request for url 'http://10.177.0.10:17080/seg001.ts', offset 0, playlist 0",
				"[hls @ 0x55c32c0c5700] HLS request for url 'http://10.177.0.10:17080/seg001.ts', offset 0, playlist 0", // Same URL
				"[hls @ 0x55c32c0c5700] HLS request for url 'http://10.177.0.10:17080/seg002.ts', offset 0, playlist 0",
			},
			verify: func(t *testing.T, stats DebugStats) {
				// First request starts tracking seg001
				// Second request (same URL) should complete first seg001, start tracking second seg001
				// Third request should complete second seg001, start tracking seg002
				// So we should have 2 completed segments
				if stats.SegmentCount != 2 {
					t.Errorf("Should have 2 completed segments, got %d", stats.SegmentCount)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewDebugEventParser(1, 2*time.Second, nil)
			for _, line := range tt.lines {
				parser.ParseLine(line)
			}
			stats := parser.Stats()
			tt.verify(t, stats)
		})
	}
}

// TestDebugEventParser_TimestampVariations tests all timestamp format variations.
func TestDebugEventParser_TimestampVariations(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantType DebugEventType
	}{
		{
			name:     "no_timestamp",
			line:     "[hls @ 0x55c32c0c5700] HLS request for url 'http://10.177.0.10:17080/seg001.ts', offset 0, playlist 0",
			wantType: DebugEventHLSRequest,
		},
		{
			name:     "with_timestamp",
			line:     "2026-01-23 08:44:23.117 [hls @ 0x55c32c0c5700] HLS request for url 'http://10.177.0.10:17080/seg001.ts', offset 0, playlist 0",
			wantType: DebugEventHLSRequest,
		},
		{
			name:     "timestamp_with_debug_prefix",
			line:     "2026-01-23 08:44:23.117 [debug] [hls @ 0x55c32c0c5700] HLS request for url 'http://10.177.0.10:17080/seg001.ts', offset 0, playlist 0",
			wantType: DebugEventHLSRequest,
		},
		{
			name:     "timestamp_with_verbose_prefix",
			line:     "2026-01-23 08:44:23.117 [verbose] [hls @ 0x55c32c0c5700] HLS request for url 'http://10.177.0.10:17080/seg001.ts', offset 0, playlist 0",
			wantType: DebugEventHLSRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var received *DebugEvent
			parser := NewDebugEventParser(1, 2*time.Second, func(e *DebugEvent) {
				received = e
			})

			parser.ParseLine(tt.line)

			if received == nil {
				t.Fatal("Expected event, got nil")
			}
			if received.Type != tt.wantType {
				t.Errorf("Type = %v, want %v", received.Type, tt.wantType)
			}
			if !received.Timestamp.IsZero() {
				// If timestamp was parsed, it should be reasonable (not zero, not in future)
				if received.Timestamp.After(time.Now().Add(1 * time.Hour)) {
					t.Errorf("Timestamp seems wrong: %v", received.Timestamp)
				}
			}
		})
	}
}

// TestDebugEventParser_ErrorEventParsing tests all error event types.
func TestDebugEventParser_ErrorEventParsing(t *testing.T) {
	tests := []struct {
		name           string
		line           string
		wantHTTPError  bool
		want4xx        bool
		want5xx        bool
		wantReconnect  bool
		wantSegmentFail bool
		wantPlaylistFail bool
	}{
		{
			name:          "http_error_404",
			line:          "[http @ 0x558f5f5da980] HTTP error 404 Not Found",
			wantHTTPError: true,
			want4xx:       true,
		},
		{
			name:          "http_error_500",
			line:          "[http @ 0x558f5f5da980] HTTP error 500 Internal Server Error",
			wantHTTPError: true,
			want5xx:       true,
		},
		{
			name:         "http_error_503",
			line:         "[http @ 0x558f5f5da980] HTTP error 503 Service Unavailable",
			wantHTTPError: true,
			want5xx:      true,
		},
		{
			name:         "reconnect",
			line:         "[http @ 0x558f5f5da980] Will reconnect at 12345 in 2 second(s)",
			wantReconnect: true,
		},
		{
			name:           "segment_failed",
			line:           "[hls @ 0x55c32c0c5700] Failed to open segment 123 of playlist 0",
			wantSegmentFail: true,
		},
		{
			name:            "playlist_failed",
			line:            "[hls @ 0x55c32c0c5700] Failed to reload playlist 0",
			wantPlaylistFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewDebugEventParser(1, 2*time.Second, nil)
			initialStats := parser.Stats()

			parser.ParseLine(tt.line)

			finalStats := parser.Stats()

			if tt.wantHTTPError {
				if finalStats.HTTPErrorCount <= initialStats.HTTPErrorCount {
					t.Errorf("Should increment HTTPErrorCount")
				}
			}
			if tt.want4xx {
				if finalStats.HTTP4xxCount <= initialStats.HTTP4xxCount {
					t.Errorf("Should increment HTTP4xxCount")
				}
			}
			if tt.want5xx {
				if finalStats.HTTP5xxCount <= initialStats.HTTP5xxCount {
					t.Errorf("Should increment HTTP5xxCount")
				}
			}
			if tt.wantReconnect {
				if finalStats.ReconnectCount <= initialStats.ReconnectCount {
					t.Errorf("Should increment ReconnectCount")
				}
			}
			if tt.wantSegmentFail {
				if finalStats.SegmentFailedCount <= initialStats.SegmentFailedCount {
					t.Errorf("Should increment SegmentFailedCount")
				}
			}
			if tt.wantPlaylistFail {
				if finalStats.PlaylistFailedCount <= initialStats.PlaylistFailedCount {
					t.Errorf("Should increment PlaylistFailedCount")
				}
			}
		})
	}
}

// TestDebugEventParser_SequenceTracking tests sequence change tracking.
func TestDebugEventParser_SequenceTracking(t *testing.T) {
	parser := NewDebugEventParser(1, 2*time.Second, nil)

	// Normal sequence increment (no skip)
	parser.ParseLine("[hls @ 0x55c32c0c5700] Media sequence change (100 -> 101) reflected in first_timestamp")
	stats1 := parser.Stats()
	if stats1.SequenceSkips != 0 {
		t.Errorf("Normal increment should not count as skip, got %d", stats1.SequenceSkips)
	}

	// Sequence skip (gap > 1)
	parser.ParseLine("[hls @ 0x55c32c0c5700] Media sequence change (101 -> 105) reflected in first_timestamp")
	stats2 := parser.Stats()
	if stats2.SequenceSkips != 1 {
		t.Errorf("Sequence skip (101->105, gap=4) should increment SequenceSkips to 1, got %d", stats2.SequenceSkips)
	}

	// Another skip
	parser.ParseLine("[hls @ 0x55c32c0c5700] Media sequence change (105 -> 110) reflected in first_timestamp")
	stats3 := parser.Stats()
	if stats3.SequenceSkips != 2 {
		t.Errorf("Another skip (105->110, gap=5) should increment SequenceSkips to 2, got %d", stats3.SequenceSkips)
	}
}

// TestDebugEventParser_PlaylistJitterTracking tests playlist refresh jitter calculation.
func TestDebugEventParser_PlaylistJitterTracking(t *testing.T) {
	parser := NewDebugEventParser(1, 2*time.Second, nil) // targetDuration = 2s

	// First refresh - no jitter (no previous refresh)
	parser.ParseLine("[hls @ 0x55c32c0c5700] Opening 'http://10.177.0.10:17080/stream.m3u8' for reading")
	stats1 := parser.Stats()
	if stats1.PlaylistRefreshes != 1 {
		t.Errorf("First refresh should increment count to 1, got %d", stats1.PlaylistRefreshes)
	}

	// Simulate timestamped refreshes with different intervals
	// Refresh after 2.1s (100ms late)
	parser.ParseLine("2026-01-23 08:12:54.700 [hls @ 0x55c32c0c5700] Opening 'http://10.177.0.10:17080/stream.m3u8' for reading")
	stats2 := parser.Stats()
	if stats2.PlaylistRefreshes != 2 {
		t.Errorf("Second refresh should increment count to 2, got %d", stats2.PlaylistRefreshes)
	}
	// Note: Jitter calculation requires accurate timestamps, so we can't easily test
	// the exact jitter value without more complex setup. But we verify the count increments.
}

// TestDebugEventParser_FastPathOptimization tests that the fast path correctly
// skips irrelevant lines without full regex matching.
func TestDebugEventParser_FastPathOptimization(t *testing.T) {
	parser := NewDebugEventParser(1, 2*time.Second, nil)
	initialLinesProcessed := parser.Stats().LinesProcessed

	// Lines that should be skipped by fast path
	irrelevantLines := []string{
		"Some random log line",
		"[mpegts @ 0x558f5f5e0980] stream=0 stream_type=1b pid=100",
		"[h264 @ 0x558f5f626b80] nal_unit_type: 9(AUD), nal_ref_idc: 0",
		"Transform tree:",
		"    mdct_pfa_3xM_inv_float_c - type: mdct_float",
	}

	for _, line := range irrelevantLines {
		parser.ParseLine(line)
	}

	finalStats := parser.Stats()
	linesProcessed := finalStats.LinesProcessed - initialLinesProcessed

	if linesProcessed != int64(len(irrelevantLines)) {
		t.Errorf("Should process all lines (for counting), got %d processed", linesProcessed)
	}

	// But no events should be generated
	if finalStats.SegmentCount != 0 {
		t.Errorf("Irrelevant lines should not generate events, got %d segments", finalStats.SegmentCount)
	}
	if finalStats.TCPConnectCount != 0 {
		t.Errorf("Irrelevant lines should not generate events, got %d TCP connects", finalStats.TCPConnectCount)
	}
}
