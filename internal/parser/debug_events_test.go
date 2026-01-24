package parser

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Regex Pattern Tests
// =============================================================================

func TestDebugEventParser_RegexPatterns(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantRe  string
		wantLen int
	}{
		{
			name:    "hls_request",
			line:    "[hls @ 0x55c32c0c5700] HLS request for url 'http://10.177.0.10:17080/seg03440.ts', offset 0, playlist 0",
			wantRe:  "reHLSRequest",
			wantLen: 2,
		},
		{
			name:    "tcp_start",
			line:    "[tcp @ 0x55c32c0d7800] Starting connection attempt to 10.177.0.10 port 17080",
			wantRe:  "reTCPStart",
			wantLen: 3,
		},
		{
			name:    "tcp_connected",
			line:    "[tcp @ 0x55c32c0d7800] Successfully connected to 10.177.0.10 port 17080",
			wantRe:  "reTCPConnected",
			wantLen: 3,
		},
		{
			name:    "tcp_refused",
			line:    "[tcp @ 0x55c32c0d7800] Connection refused",
			wantRe:  "reTCPFailed",
			wantLen: 2,
		},
		{
			name:    "tcp_timeout",
			line:    "[tcp @ 0x55c32c0d7800] Connection timed out",
			wantRe:  "reTCPFailed",
			wantLen: 2,
		},
		{
			name:    "playlist_open_hls",
			line:    "[hls @ 0x55c32c0c5700] Opening 'http://10.177.0.10:17080/stream.m3u8' for reading",
			wantRe:  "rePlaylistOpen",
			wantLen: 2,
		},
		{
			name:    "playlist_open_avformatcontext",
			line:    "[AVFormatContext @ 0x558f5f5da200] Opening 'http://10.177.0.10:17080/stream.m3u8' for reading",
			wantRe:  "rePlaylistOpen",
			wantLen: 2,
		},
		{
			name:    "sequence_change",
			line:    "[hls @ 0x55c32c0c5700] Media sequence change (3433 -> 3438) reflected in first_timestamp",
			wantRe:  "reSequenceChange",
			wantLen: 3,
		},
		{
			name:    "bandwidth",
			line:    "BANDWIDTH=1234567",
			wantRe:  "reBandwidth",
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m []string
			switch tt.wantRe {
			case "reHLSRequest":
				m = reHLSRequest.FindStringSubmatch(tt.line)
			case "reTCPStart":
				m = reTCPStart.FindStringSubmatch(tt.line)
			case "reTCPConnected":
				m = reTCPConnected.FindStringSubmatch(tt.line)
			case "reTCPFailed":
				m = reTCPFailed.FindStringSubmatch(tt.line)
			case "rePlaylistOpen":
				m = rePlaylistOpen.FindStringSubmatch(tt.line)
			case "reSequenceChange":
				m = reSequenceChange.FindStringSubmatch(tt.line)
			case "reBandwidth":
				m = reBandwidth.FindStringSubmatch(tt.line)
			}

			if len(m) != tt.wantLen {
				t.Errorf("regex %s: got %d captures, want %d. Match: %v", tt.wantRe, len(m), tt.wantLen, m)
			}
		})
	}
}

// =============================================================================
// Basic Parser Tests
// =============================================================================

func TestNewDebugEventParser(t *testing.T) {
	p := NewDebugEventParser(42, 2*time.Second, nil)

	if p.clientID != 42 {
		t.Errorf("clientID = %d, want 42", p.clientID)
	}
	if p.targetDuration != 2*time.Second {
		t.Errorf("targetDuration = %v, want 2s", p.targetDuration)
	}
}

func TestDebugEventParser_DefaultTargetDuration(t *testing.T) {
	p := NewDebugEventParser(1, 0, nil)

	if p.targetDuration != 2*time.Second {
		t.Errorf("default targetDuration = %v, want 2s", p.targetDuration)
	}
}

func TestDebugEventParser_ParseLine_HLSRequest(t *testing.T) {
	var received *DebugEvent
	p := NewDebugEventParser(1, 2*time.Second, func(e *DebugEvent) {
		received = e
	})

	line := "[hls @ 0x55c32c0c5700] HLS request for url 'http://10.177.0.10:17080/seg03440.ts', offset 0, playlist 0"
	p.ParseLine(line)

	if received == nil {
		t.Fatal("callback not called")
	}
	if received.Type != DebugEventHLSRequest {
		t.Errorf("Type = %v, want DebugEventHLSRequest", received.Type)
	}
	if received.URL != "http://10.177.0.10:17080/seg03440.ts" {
		t.Errorf("URL = %q, want seg03440.ts URL", received.URL)
	}
}

func TestDebugEventParser_ParseLine_TCPConnect(t *testing.T) {
	var events []*DebugEvent
	p := NewDebugEventParser(1, 2*time.Second, func(e *DebugEvent) {
		events = append(events, e)
	})

	// TCP start
	p.ParseLine("[tcp @ 0x55c32c0d7800] Starting connection attempt to 10.177.0.10 port 17080")

	// TCP connected
	time.Sleep(10 * time.Millisecond) // Ensure measurable time
	p.ParseLine("[tcp @ 0x55c32c0d7800] Successfully connected to 10.177.0.10 port 17080")

	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}

	if events[0].Type != DebugEventTCPStart {
		t.Errorf("event[0].Type = %v, want DebugEventTCPStart", events[0].Type)
	}
	if events[0].IP != "10.177.0.10" {
		t.Errorf("event[0].IP = %q, want 10.177.0.10", events[0].IP)
	}
	if events[0].Port != 17080 {
		t.Errorf("event[0].Port = %d, want 17080", events[0].Port)
	}

	if events[1].Type != DebugEventTCPConnected {
		t.Errorf("event[1].Type = %v, want DebugEventTCPConnected", events[1].Type)
	}
}

func TestDebugEventParser_ParseLine_TCPFailed(t *testing.T) {
	tests := []struct {
		line       string
		wantReason string
	}{
		{"[tcp @ 0x55c32c0d7800] Connection refused", "refused"},
		{"[tcp @ 0x55c32c0d7800] Connection timed out", "timeout"},
		{"[tcp @ 0x55c32c0d7800] Failed to connect to 10.0.0.1", "error"},
	}

	for _, tt := range tests {
		t.Run(tt.wantReason, func(t *testing.T) {
			var received *DebugEvent
			p := NewDebugEventParser(1, 2*time.Second, func(e *DebugEvent) {
				received = e
			})

			p.ParseLine(tt.line)

			if received == nil {
				t.Fatal("callback not called")
			}
			if received.Type != DebugEventTCPFailed {
				t.Errorf("Type = %v, want DebugEventTCPFailed", received.Type)
			}
			if received.FailReason != tt.wantReason {
				t.Errorf("FailReason = %q, want %q", received.FailReason, tt.wantReason)
			}
		})
	}
}

func TestDebugEventParser_ParseLine_Bandwidth(t *testing.T) {
	var received *DebugEvent
	p := NewDebugEventParser(1, 2*time.Second, func(e *DebugEvent) {
		received = e
	})

	p.ParseLine("BANDWIDTH=2000000")

	if received == nil {
		t.Fatal("callback not called")
	}
	if received.Type != DebugEventBandwidth {
		t.Errorf("Type = %v, want DebugEventBandwidth", received.Type)
	}
	if received.Bandwidth != 2000000 {
		t.Errorf("Bandwidth = %d, want 2000000", received.Bandwidth)
	}
	if p.GetManifestBandwidth() != 2000000 {
		t.Errorf("GetManifestBandwidth() = %d, want 2000000", p.GetManifestBandwidth())
	}
}

func TestDebugEventParser_ParseLine_SequenceChange(t *testing.T) {
	var received *DebugEvent
	p := NewDebugEventParser(1, 2*time.Second, func(e *DebugEvent) {
		received = e
	})

	p.ParseLine("[hls @ 0x55c32c0c5700] Media sequence change (3433 -> 3438) reflected in first_timestamp")

	if received == nil {
		t.Fatal("callback not called")
	}
	if received.Type != DebugEventSequenceChange {
		t.Errorf("Type = %v, want DebugEventSequenceChange", received.Type)
	}
	if received.OldSeq != 3433 {
		t.Errorf("OldSeq = %d, want 3433", received.OldSeq)
	}
	if received.NewSeq != 3438 {
		t.Errorf("NewSeq = %d, want 3438", received.NewSeq)
	}
}

// =============================================================================
// Statistics Tests
// =============================================================================

func TestDebugEventParser_Stats_TCPConnect(t *testing.T) {
	p := NewDebugEventParser(1, 2*time.Second, nil)

	// Simulate TCP connections
	for i := 0; i < 5; i++ {
		p.ParseLine("[tcp @ 0x55c32c0d7800] Starting connection attempt to 10.177.0.10 port 17080")
		time.Sleep(5 * time.Millisecond)
		p.ParseLine("[tcp @ 0x55c32c0d7800] Successfully connected to 10.177.0.10 port 17080")
	}

	stats := p.Stats()

	if stats.TCPConnectCount != 5 {
		t.Errorf("TCPConnectCount = %d, want 5", stats.TCPConnectCount)
	}
	if stats.TCPSuccessCount != 5 {
		t.Errorf("TCPSuccessCount = %d, want 5", stats.TCPSuccessCount)
	}
	if stats.TCPHealthRatio != 1.0 {
		t.Errorf("TCPHealthRatio = %f, want 1.0", stats.TCPHealthRatio)
	}
	if stats.TCPConnectAvgMs < 1 {
		t.Errorf("TCPConnectAvgMs = %f, want >= 1ms", stats.TCPConnectAvgMs)
	}
}

func TestDebugEventParser_Stats_TCPHealth(t *testing.T) {
	p := NewDebugEventParser(1, 2*time.Second, nil)

	// 3 successes
	for i := 0; i < 3; i++ {
		p.ParseLine("[tcp @ 0x55c32c0d7800] Starting connection attempt to 10.177.0.10 port 17080")
		p.ParseLine("[tcp @ 0x55c32c0d7800] Successfully connected to 10.177.0.10 port 17080")
	}

	// 2 failures
	p.ParseLine("[tcp @ 0x55c32c0d7800] Connection refused")
	p.ParseLine("[tcp @ 0x55c32c0d7800] Connection timed out")

	stats := p.Stats()

	if stats.TCPSuccessCount != 3 {
		t.Errorf("TCPSuccessCount = %d, want 3", stats.TCPSuccessCount)
	}
	if stats.TCPFailureCount != 2 {
		t.Errorf("TCPFailureCount = %d, want 2", stats.TCPFailureCount)
	}
	if stats.TCPRefusedCount != 1 {
		t.Errorf("TCPRefusedCount = %d, want 1", stats.TCPRefusedCount)
	}
	if stats.TCPTimeoutCount != 1 {
		t.Errorf("TCPTimeoutCount = %d, want 1", stats.TCPTimeoutCount)
	}

	// Health ratio: 3 / (3 + 2) = 0.6
	expectedRatio := 0.6
	if stats.TCPHealthRatio != expectedRatio {
		t.Errorf("TCPHealthRatio = %f, want %f", stats.TCPHealthRatio, expectedRatio)
	}
}

func TestDebugEventParser_Stats_PlaylistJitter(t *testing.T) {
	p := NewDebugEventParser(1, 2*time.Second, nil)

	// First refresh (no jitter calculated)
	p.ParseLine("[hls @ 0x55c32c0c5700] Opening 'http://example.com/stream.m3u8' for reading")

	// Second refresh after ~2.1s (slightly late)
	time.Sleep(50 * time.Millisecond) // Simulate delay
	p.ParseLine("[hls @ 0x55c32c0c5700] Opening 'http://example.com/stream.m3u8' for reading")

	stats := p.Stats()

	if stats.PlaylistRefreshes != 2 {
		t.Errorf("PlaylistRefreshes = %d, want 2", stats.PlaylistRefreshes)
	}
	// Jitter should be non-zero (50ms sleep vs 2s target)
	// But we can't test exact values due to timing
}

func TestDebugEventParser_Stats_SequenceSkips(t *testing.T) {
	p := NewDebugEventParser(1, 2*time.Second, nil)

	// Normal sequence
	p.ParseLine("[hls @ 0x55c32c0c5700] Media sequence change (100 -> 101)")
	p.ParseLine("[hls @ 0x55c32c0c5700] Media sequence change (101 -> 102)")

	// Skip (102 -> 105, skipped 103, 104)
	p.ParseLine("[hls @ 0x55c32c0c5700] Media sequence change (102 -> 105)")

	stats := p.Stats()

	if stats.SequenceSkips != 1 {
		t.Errorf("SequenceSkips = %d, want 1", stats.SequenceSkips)
	}
}

func TestDebugEventParser_Stats_LinesProcessed(t *testing.T) {
	p := NewDebugEventParser(1, 2*time.Second, nil)

	lines := []string{
		"[hls @ 0x55c32c0c5700] HLS request for url 'http://example.com/seg1.ts'",
		"[tcp @ 0x55c32c0d7800] Starting connection attempt to 10.0.0.1 port 80",
		"[tcp @ 0x55c32c0d7800] Successfully connected to 10.0.0.1 port 80",
		"some random line that won't match",
		"another non-matching line",
	}

	for _, line := range lines {
		p.ParseLine(line)
	}

	stats := p.Stats()
	if stats.LinesProcessed != int64(len(lines)) {
		t.Errorf("LinesProcessed = %d, want %d", stats.LinesProcessed, len(lines))
	}
}

// =============================================================================
// Fast Path Tests
// =============================================================================

func TestDebugEventParser_FastPath(t *testing.T) {
	var callCount atomic.Int32
	p := NewDebugEventParser(1, 2*time.Second, func(e *DebugEvent) {
		callCount.Add(1)
	})

	// These should be skipped by fast path (no " @ 0x" or "BANDWIDTH=")
	fastPathLines := []string{
		"frame=17878 fps=178 q=-1.0",
		"fps=177.99",
		"stream_0_0_q=-1.0",
		"bitrate=N/A",
		"progress=continue",
	}

	for _, line := range fastPathLines {
		p.ParseLine(line)
	}

	if callCount.Load() != 0 {
		t.Errorf("callback called %d times, want 0 (fast path)", callCount.Load())
	}
}

// =============================================================================
// Thread Safety Tests
// =============================================================================

func TestDebugEventParser_ThreadSafety(t *testing.T) {
	p := NewDebugEventParser(1, 2*time.Second, nil)

	var wg sync.WaitGroup
	const goroutines = 10
	const linesPerGoroutine = 100

	lines := []string{
		"[hls @ 0x55c32c0c5700] HLS request for url 'http://example.com/seg1.ts'",
		"[tcp @ 0x55c32c0d7800] Starting connection attempt to 10.0.0.1 port 80",
		"[tcp @ 0x55c32c0d7800] Successfully connected to 10.0.0.1 port 80",
		"[hls @ 0x55c32c0c5700] Opening 'http://example.com/stream.m3u8' for reading",
		"[hls @ 0x55c32c0c5700] Media sequence change (100 -> 101)",
	}

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < linesPerGoroutine; j++ {
				line := lines[j%len(lines)]
				p.ParseLine(line)
			}
		}()
	}

	// Also read stats concurrently
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = p.Stats()
		}
	}()

	wg.Wait()

	stats := p.Stats()
	expectedLines := int64(goroutines * linesPerGoroutine)
	if stats.LinesProcessed != expectedLines {
		t.Errorf("LinesProcessed = %d, want %d", stats.LinesProcessed, expectedLines)
	}
}

// =============================================================================
// Benchmark
// =============================================================================

func BenchmarkDebugEventParser_ParseLine(b *testing.B) {
	p := NewDebugEventParser(1, 2*time.Second, nil)

	lines := []string{
		"[hls @ 0x55c32c0c5700] HLS request for url 'http://10.177.0.10:17080/seg03440.ts', offset 0, playlist 0",
		"[tcp @ 0x55c32c0d7800] Starting connection attempt to 10.177.0.10 port 17080",
		"[tcp @ 0x55c32c0d7800] Successfully connected to 10.177.0.10 port 17080",
		"frame=17878 fps=178 q=-1.0 size=N/A time=00:00:05.93 bitrate=N/A speed=5.93x",
		"fps=177.99",
		"progress=continue",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		line := lines[i%len(lines)]
		p.ParseLine(line)
	}
}

func BenchmarkDebugEventParser_FastPath(b *testing.B) {
	p := NewDebugEventParser(1, 2*time.Second, nil)

	// Lines that should hit fast path (no match)
	line := "frame=17878 fps=178 q=-1.0 size=N/A time=00:00:05.93"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.ParseLine(line)
	}
}

// =============================================================================
// Real Testdata File Tests
// =============================================================================

func TestDebugEventParser_ParseTestdataFile(t *testing.T) {
	// Read the comprehensive testdata file
	data, err := os.ReadFile("../../testdata/ffmpeg_debug_comprehensive.txt")
	if err != nil {
		t.Skipf("Skipping testdata test: %v", err)
	}

	var events []*DebugEvent
	p := NewDebugEventParser(1, 2*time.Second, func(e *DebugEvent) {
		events = append(events, e)
	})

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		p.ParseLine(line)
	}

	stats := p.Stats()

	// Verify expected counts from comprehensive testdata
	t.Run("hls_requests", func(t *testing.T) {
		hlsRequests := 0
		for _, e := range events {
			if e.Type == DebugEventHLSRequest {
				hlsRequests++
			}
		}
		if hlsRequests < 10 {
			t.Errorf("Expected >= 10 HLS requests, got %d", hlsRequests)
		}
	})

	t.Run("tcp_connections", func(t *testing.T) {
		if stats.TCPSuccessCount < 5 {
			t.Errorf("Expected >= 5 TCP successes, got %d", stats.TCPSuccessCount)
		}
	})

	t.Run("tcp_failures", func(t *testing.T) {
		if stats.TCPFailureCount < 3 {
			t.Errorf("Expected >= 3 TCP failures (refused/timeout/error), got %d", stats.TCPFailureCount)
		}
	})

	t.Run("playlist_refreshes", func(t *testing.T) {
		if stats.PlaylistRefreshes < 3 {
			t.Errorf("Expected >= 3 playlist refreshes, got %d", stats.PlaylistRefreshes)
		}
	})

	t.Run("sequence_skips", func(t *testing.T) {
		// The comprehensive testdata has 102->105 skip
		if stats.SequenceSkips < 1 {
			t.Errorf("Expected >= 1 sequence skip, got %d", stats.SequenceSkips)
		}
	})

	t.Run("bandwidth_parsed", func(t *testing.T) {
		if stats.ManifestBandwidth == 0 {
			t.Error("Expected ManifestBandwidth to be parsed")
		}
		// Parser stores the last BANDWIDTH= value seen (500000 in testdata)
		// This is correct behavior - the last value wins
		if stats.ManifestBandwidth != 500000 {
			t.Errorf("Expected ManifestBandwidth=500000 (last in file), got %d", stats.ManifestBandwidth)
		}
	})

	t.Run("lines_processed", func(t *testing.T) {
		if stats.LinesProcessed != int64(len(lines)) {
			t.Errorf("LinesProcessed=%d, want %d", stats.LinesProcessed, len(lines))
		}
	})

	t.Run("tcp_health_ratio", func(t *testing.T) {
		// We have successes and failures, so ratio should be between 0 and 1
		if stats.TCPHealthRatio <= 0 || stats.TCPHealthRatio >= 1 {
			t.Errorf("TCPHealthRatio=%f, expected 0 < ratio < 1", stats.TCPHealthRatio)
		}
	})
}

func TestDebugEventParser_ParseOriginalTestdata(t *testing.T) {
	// Parse the original debug output file
	data, err := os.ReadFile("../../testdata/ffmpeg_debug_output.txt")
	if err != nil {
		t.Skipf("Skipping testdata test: %v", err)
	}

	p := NewDebugEventParser(1, 2*time.Second, nil)

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		p.ParseLine(line)
	}

	stats := p.Stats()

	// Basic sanity checks
	if stats.LinesProcessed == 0 {
		t.Error("No lines processed")
	}

	t.Logf("Original testdata stats:")
	t.Logf("  Lines: %d", stats.LinesProcessed)
	t.Logf("  HLS Segments: %d", stats.SegmentCount)
	t.Logf("  TCP Success: %d, Failure: %d", stats.TCPSuccessCount, stats.TCPFailureCount)
	t.Logf("  Playlist Refreshes: %d", stats.PlaylistRefreshes)
	t.Logf("  Sequence Skips: %d", stats.SequenceSkips)
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestDebugEventParser_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantType DebugEventType
		wantSkip bool
	}{
		{
			name:     "hls_request_with_special_chars",
			line:     "[hls @ 0xabc123] HLS request for url 'http://cdn.example.com/path/to/seg-123_456.ts?token=abc', offset 0, playlist 0",
			wantType: DebugEventHLSRequest,
		},
		{
			name:     "tcp_ipv6_address",
			line:     "[tcp @ 0xdef456] Starting connection attempt to 2001:db8::1 port 443",
			wantSkip: true, // Current regex only handles IPv4
		},
		{
			name:     "playlist_with_query_string",
			line:     "[hls @ 0xabc123] Opening 'http://cdn.example.com/live/playlist.m3u8?token=xyz' for reading",
			wantType: DebugEventPlaylistOpen,
		},
		{
			name:     "playlist_avformatcontext_initial",
			line:     "[AVFormatContext @ 0x558f5f5da200] Opening 'http://10.177.0.10:17080/stream.m3u8' for reading",
			wantType: DebugEventPlaylistOpen,
		},
		{
			name:     "playlist_avformatcontext_with_debug",
			line:     "2026-01-23 08:12:52.614 [AVFormatContext @ 0x5647feb5a900] [debug] Opening 'http://10.177.0.10:17080/stream.m3u8' for reading",
			wantType: DebugEventPlaylistOpen,
		},
		{
			name:     "bandwidth_in_context",
			line:     "#EXT-X-STREAM-INF:BANDWIDTH=5000000,RESOLUTION=1920x1080",
			wantType: DebugEventBandwidth,
		},
		{
			name:     "sequence_large_numbers",
			line:     "[hls @ 0xabc123] Media sequence change (999999 -> 1000005) reflected",
			wantType: DebugEventSequenceChange,
		},
		{
			name:     "empty_line",
			line:     "",
			wantSkip: true,
		},
		{
			name:     "comment_line",
			line:     "# This is a comment",
			wantSkip: true,
		},
		{
			name:     "partial_match_hls",
			line:     "[hls @ 0xabc] Some other HLS message without request",
			wantSkip: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var received *DebugEvent
			p := NewDebugEventParser(1, 2*time.Second, func(e *DebugEvent) {
				received = e
			})

			p.ParseLine(tt.line)

			if tt.wantSkip {
				if received != nil {
					t.Errorf("Expected line to be skipped, but got event type %v", received.Type)
				}
			} else {
				if received == nil {
					t.Fatal("Expected event, got nil")
				}
				if received.Type != tt.wantType {
					t.Errorf("Type = %v, want %v", received.Type, tt.wantType)
				}
			}
		})
	}
}

func TestDebugEventParser_TCPFailureTypes(t *testing.T) {
	tests := []struct {
		line        string
		wantReason  string
		wantTimeout bool
		wantRefused bool
	}{
		{
			line:        "[tcp @ 0xabc] Connection refused",
			wantReason:  "refused",
			wantRefused: true,
		},
		{
			line:        "[tcp @ 0xabc] connection refused",
			wantReason:  "refused",
			wantRefused: true,
		},
		{
			line:        "[tcp @ 0xabc] Connection timed out",
			wantReason:  "timeout",
			wantTimeout: true,
		},
		{
			line:        "[tcp @ 0xabc] connection timed out",
			wantReason:  "timeout",
			wantTimeout: true,
		},
		{
			line:       "[tcp @ 0xabc] Failed to connect to 10.0.0.1",
			wantReason: "error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.wantReason, func(t *testing.T) {
			p := NewDebugEventParser(1, 2*time.Second, nil)
			p.ParseLine(tt.line)

			stats := p.Stats()
			if stats.TCPFailureCount != 1 {
				t.Errorf("TCPFailureCount = %d, want 1", stats.TCPFailureCount)
			}
			if tt.wantTimeout && stats.TCPTimeoutCount != 1 {
				t.Errorf("TCPTimeoutCount = %d, want 1", stats.TCPTimeoutCount)
			}
			if tt.wantRefused && stats.TCPRefusedCount != 1 {
				t.Errorf("TCPRefusedCount = %d, want 1", stats.TCPRefusedCount)
			}
		})
	}
}

// =============================================================================
// Timestamp Parsing Tests
// =============================================================================

func TestDebugEventParser_TimestampParsing(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		wantTs      bool
		wantType    DebugEventType
		wantURL     string
	}{
		{
			name:     "timestamped_hls_request",
			line:     "2026-01-23 08:12:52.615 [hls @ 0x5647feb5a900] [verbose] HLS request for url 'http://10.177.0.10:17080/seg38024.ts', offset 0, playlist 0",
			wantTs:   true,
			wantType: DebugEventHLSRequest,
			wantURL:  "http://10.177.0.10:17080/seg38024.ts",
		},
		{
			name:     "timestamped_tcp_start",
			line:     "2026-01-23 08:12:52.614 [tcp @ 0x5647feb5e100] [verbose] Starting connection attempt to 10.177.0.10 port 17080",
			wantTs:   true,
			wantType: DebugEventTCPStart,
		},
		{
			name:     "timestamped_tcp_connected",
			line:     "2026-01-23 08:12:52.614 [tcp @ 0x5647feb5e100] [verbose] Successfully connected to 10.177.0.10 port 17080",
			wantTs:   true,
			wantType: DebugEventTCPConnected,
		},
		{
			name:     "timestamped_playlist_open",
			line:     "2026-01-23 08:12:54.628 [hls @ 0x5647feb5a900] [debug] Opening 'http://10.177.0.10:17080/stream.m3u8' for reading",
			wantTs:   true,
			wantType: DebugEventPlaylistOpen,
		},
		{
			name:     "timestamped_sequence_change",
			line:     "2026-01-23 08:13:02.638 [hls @ 0x5647feb5a900] [debug] Media sequence change (38017 -> 38022) reflected in first_timestamp: 76049421333 -> 76059421333",
			wantTs:   true,
			wantType: DebugEventSequenceChange,
		},
		{
			name:     "non_timestamped_fallback",
			line:     "[hls @ 0x55f8] HLS request for url 'http://origin/seg00001.ts', offset 0, playlist 0",
			wantTs:   false,
			wantType: DebugEventHLSRequest,
			wantURL:  "http://origin/seg00001.ts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var received *DebugEvent
			p := NewDebugEventParser(1, 2*time.Second, func(e *DebugEvent) {
				received = e
			})

			p.ParseLine(tt.line)
			stats := p.Stats()

			if tt.wantTs && stats.TimestampsUsed != 1 {
				t.Errorf("TimestampsUsed = %d, want 1", stats.TimestampsUsed)
			}
			if !tt.wantTs && stats.TimestampsUsed != 0 {
				t.Errorf("TimestampsUsed = %d, want 0", stats.TimestampsUsed)
			}

			if received == nil {
				t.Fatal("Expected event, got nil")
			}

			if received.Type != tt.wantType {
				t.Errorf("Type = %v, want %v", received.Type, tt.wantType)
			}

			if tt.wantURL != "" && received.URL != tt.wantURL {
				t.Errorf("URL = %q, want %q", received.URL, tt.wantURL)
			}

			// Verify timestamp was parsed correctly for timestamped lines
			if tt.wantTs {
				// Check it's not zero
				if received.Timestamp.Year() != 2026 {
					t.Errorf("Timestamp year = %d, want 2026", received.Timestamp.Year())
				}
			}
		})
	}
}

func TestDebugEventParser_TimestampedTCPTiming(t *testing.T) {
	p := NewDebugEventParser(1, 2*time.Second, nil)

	// Parse TCP start with timestamp
	p.ParseLine("2026-01-23 08:12:52.614 [tcp @ 0x5647feb5e100] [verbose] Starting connection attempt to 10.177.0.10 port 17080")

	// Parse TCP connected 1ms later
	p.ParseLine("2026-01-23 08:12:52.615 [tcp @ 0x5647feb5e100] [verbose] Successfully connected to 10.177.0.10 port 17080")

	stats := p.Stats()

	if stats.TimestampsUsed != 2 {
		t.Errorf("TimestampsUsed = %d, want 2", stats.TimestampsUsed)
	}

	if stats.TCPConnectCount != 1 {
		t.Errorf("TCPConnectCount = %d, want 1", stats.TCPConnectCount)
	}

	// Should be ~1ms (from the FFmpeg timestamps, not wall clock)
	if stats.TCPConnectAvgMs < 0.5 || stats.TCPConnectAvgMs > 1.5 {
		t.Errorf("TCPConnectAvgMs = %f, want ~1.0", stats.TCPConnectAvgMs)
	}
}

func TestDebugEventParser_ParseTimestampedTestdata(t *testing.T) {
	// Read the timestamped testdata file
	data, err := os.ReadFile("../../testdata/ffmpeg_timestamped_2.txt")
	if err != nil {
		t.Skipf("Skipping timestamped testdata test: %v", err)
	}

	p := NewDebugEventParser(1, 2*time.Second, nil)

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		p.ParseLine(line)
	}

	stats := p.Stats()

	t.Logf("Timestamped testdata stats:")
	t.Logf("  Lines: %d", stats.LinesProcessed)
	t.Logf("  Timestamps used: %d (%.1f%%)", stats.TimestampsUsed, float64(stats.TimestampsUsed)/float64(stats.LinesProcessed)*100)
	t.Logf("  HLS segments: %d", stats.SegmentCount)
	t.Logf("  TCP connects: %d", stats.TCPConnectCount)
	t.Logf("  TCP connect avg: %.2fms", stats.TCPConnectAvgMs)
	t.Logf("  Playlist refreshes: %d", stats.PlaylistRefreshes)
	t.Logf("  Sequence skips: %d", stats.SequenceSkips)

	// Should have timestamps in most lines
	if stats.TimestampsUsed == 0 {
		t.Error("Expected TimestampsUsed > 0 for timestamped testdata")
	}

	// Verify some key metrics
	if stats.TCPConnectCount < 3 {
		t.Errorf("Expected at least 3 TCP connects, got %d", stats.TCPConnectCount)
	}

	// Playlist refresh count depends on capture duration
	// At least 1 should be captured
	if stats.PlaylistRefreshes < 1 {
		t.Errorf("Expected at least 1 playlist refresh, got %d", stats.PlaylistRefreshes)
	}
}

// =============================================================================
// Benchmark with Real Testdata
// =============================================================================

func BenchmarkDebugEventParser_RealTestdata(b *testing.B) {
	data, err := os.ReadFile("../../testdata/ffmpeg_debug_comprehensive.txt")
	if err != nil {
		b.Skipf("Skipping benchmark: %v", err)
	}

	lines := strings.Split(string(data), "\n")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := NewDebugEventParser(1, 2*time.Second, nil)
		for _, line := range lines {
			p.ParseLine(line)
		}
	}

	b.ReportMetric(float64(len(lines)), "lines/op")
}
