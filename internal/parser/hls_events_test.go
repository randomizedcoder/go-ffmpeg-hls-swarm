package parser

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClassifyURL(t *testing.T) {
	tests := []struct {
		url  string
		want URLType
	}{
		// Manifest URLs
		{"http://example.com/stream.m3u8", URLTypeManifest},
		{"http://example.com/720p.m3u8", URLTypeManifest},
		{"http://example.com/master.M3U8", URLTypeManifest}, // Case insensitive
		{"http://example.com/stream.m3u8?token=abc", URLTypeManifest},
		{"http://example.com/stream.m3u8?v=123&auth=xyz", URLTypeManifest},

		// Segment URLs
		{"http://example.com/seg00001.ts", URLTypeSegment},
		{"http://example.com/segment_001.TS", URLTypeSegment}, // Case insensitive
		{"http://example.com/segment.ts?v=123", URLTypeSegment},
		{"http://cdn.example.com/hls/1080p/seg_00042.ts?token=abc123", URLTypeSegment},

		// Init segment URLs (fMP4)
		{"http://example.com/init.mp4", URLTypeInit},
		{"http://example.com/init_720p.MP4", URLTypeInit}, // Case insensitive
		{"http://example.com/init.mp4?v=1", URLTypeInit},

		// Unknown URLs (fallback bucket)
		{"http://example.com/unknown", URLTypeUnknown},
		{"http://example.com/video.webm", URLTypeUnknown},
		{"http://example.com/stream", URLTypeUnknown},
		{"http://example.com/", URLTypeUnknown},
		{"", URLTypeUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := classifyURL(tt.url)
			if got != tt.want {
				t.Errorf("classifyURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestURLType_String(t *testing.T) {
	tests := []struct {
		urlType URLType
		want    string
	}{
		{URLTypeManifest, "manifest"},
		{URLTypeSegment, "segment"},
		{URLTypeInit, "init"},
		{URLTypeUnknown, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.urlType.String(); got != tt.want {
				t.Errorf("URLType.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHLSEventType_String(t *testing.T) {
	tests := []struct {
		eventType HLSEventType
		want      string
	}{
		{EventRequest, "request"},
		{EventHTTPError, "http_error"},
		{EventReconnect, "reconnect"},
		{EventTimeout, "timeout"},
		{EventUnknown, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.eventType.String(); got != tt.want {
				t.Errorf("HLSEventType.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHLSEventParser_Requests(t *testing.T) {
	input := `[hls @ 0x55f8] Opening 'http://example.com/stream.m3u8' for reading
[https @ 0x55f8] Opening 'http://example.com/seg00001.ts' for reading
[hls @ 0x55f8] Opening 'http://example.com/stream.m3u8' for reading
[https @ 0x55f8] Opening 'http://example.com/seg00002.ts' for reading
[https @ 0x55f8] Opening 'http://example.com/seg00003.ts' for reading
[https @ 0x55f8] Opening 'http://example.com/init.mp4' for reading
`

	var events []*HLSEvent
	var mu sync.Mutex

	p := NewHLSEventParser(1, func(e *HLSEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})

	for _, line := range strings.Split(input, "\n") {
		if line != "" {
			p.ParseLine(line)
		}
	}

	stats := p.Stats()

	if stats.ManifestRequests != 2 {
		t.Errorf("ManifestRequests = %d, want 2", stats.ManifestRequests)
	}
	if stats.SegmentRequests != 3 {
		t.Errorf("SegmentRequests = %d, want 3", stats.SegmentRequests)
	}
	if stats.InitRequests != 1 {
		t.Errorf("InitRequests = %d, want 1", stats.InitRequests)
	}

	mu.Lock()
	if len(events) != 6 {
		t.Errorf("events = %d, want 6", len(events))
	}
	mu.Unlock()
}

func TestHLSEventParser_UnknownURLs(t *testing.T) {
	input := `[hls @ 0x55f8] Opening 'http://example.com/stream.m3u8' for reading
[https @ 0x55f8] Opening 'http://example.com/unknown_resource' for reading
[https @ 0x55f8] Opening 'http://example.com/video.webm' for reading
[https @ 0x55f8] Opening 'http://example.com/seg00001.ts' for reading
`

	p := NewHLSEventParser(1, nil)

	for _, line := range strings.Split(input, "\n") {
		if line != "" {
			p.ParseLine(line)
		}
	}

	stats := p.Stats()

	if stats.ManifestRequests != 1 {
		t.Errorf("ManifestRequests = %d, want 1", stats.ManifestRequests)
	}
	if stats.SegmentRequests != 1 {
		t.Errorf("SegmentRequests = %d, want 1", stats.SegmentRequests)
	}
	if stats.UnknownRequests != 2 {
		t.Errorf("UnknownRequests = %d, want 2", stats.UnknownRequests)
	}
}

func TestHLSEventParser_HTTPErrors(t *testing.T) {
	input := `Server returned 503 Service Unavailable
Server returned 404 Not Found
Server returned 503 Service Unavailable
Server returned 500 Internal Server Error
`

	p := NewHLSEventParser(1, nil)

	for _, line := range strings.Split(input, "\n") {
		if line != "" {
			p.ParseLine(line)
		}
	}

	stats := p.Stats()

	if stats.HTTPErrors[503] != 2 {
		t.Errorf("HTTPErrors[503] = %d, want 2", stats.HTTPErrors[503])
	}
	if stats.HTTPErrors[404] != 1 {
		t.Errorf("HTTPErrors[404] = %d, want 1", stats.HTTPErrors[404])
	}
	if stats.HTTPErrors[500] != 1 {
		t.Errorf("HTTPErrors[500] = %d, want 1", stats.HTTPErrors[500])
	}
}

func TestHLSEventParser_Timeouts(t *testing.T) {
	input := `Connection timed out
Operation timed out
timeout waiting for response
Timed Out
`

	p := NewHLSEventParser(1, nil)

	for _, line := range strings.Split(input, "\n") {
		if line != "" {
			p.ParseLine(line)
		}
	}

	stats := p.Stats()

	if stats.Timeouts != 4 {
		t.Errorf("Timeouts = %d, want 4", stats.Timeouts)
	}
}

func TestHLSEventParser_Reconnections(t *testing.T) {
	input := `Reconnecting to http://example.com
Some other log line
Reconnecting to http://example.com/stream.m3u8
`

	p := NewHLSEventParser(1, nil)

	for _, line := range strings.Split(input, "\n") {
		if line != "" {
			p.ParseLine(line)
		}
	}

	stats := p.Stats()

	if stats.Reconnections != 2 {
		t.Errorf("Reconnections = %d, want 2", stats.Reconnections)
	}
}

func TestHLSEventParser_MixedEvents(t *testing.T) {
	input := `[hls @ 0x55f8] Opening 'http://example.com/stream.m3u8' for reading
[https @ 0x55f8] Opening 'http://example.com/seg00001.ts' for reading
Server returned 503 Service Unavailable
Connection timed out
Reconnecting to http://example.com
[https @ 0x55f8] Opening 'http://example.com/seg00002.ts' for reading
Server returned 404 Not Found
`

	p := NewHLSEventParser(1, nil)

	for _, line := range strings.Split(input, "\n") {
		if line != "" {
			p.ParseLine(line)
		}
	}

	stats := p.Stats()

	if stats.ManifestRequests != 1 {
		t.Errorf("ManifestRequests = %d, want 1", stats.ManifestRequests)
	}
	if stats.SegmentRequests != 2 {
		t.Errorf("SegmentRequests = %d, want 2", stats.SegmentRequests)
	}
	if stats.HTTPErrors[503] != 1 {
		t.Errorf("HTTPErrors[503] = %d, want 1", stats.HTTPErrors[503])
	}
	if stats.HTTPErrors[404] != 1 {
		t.Errorf("HTTPErrors[404] = %d, want 1", stats.HTTPErrors[404])
	}
	if stats.Timeouts != 1 {
		t.Errorf("Timeouts = %d, want 1", stats.Timeouts)
	}
	if stats.Reconnections != 1 {
		t.Errorf("Reconnections = %d, want 1", stats.Reconnections)
	}
	if stats.LinesProcessed != 7 {
		t.Errorf("LinesProcessed = %d, want 7", stats.LinesProcessed)
	}
	if stats.EventsEmitted != 7 {
		t.Errorf("EventsEmitted = %d, want 7", stats.EventsEmitted)
	}
}

func TestHLSEventParser_NoCallback(t *testing.T) {
	// Should not panic with nil callback
	p := NewHLSEventParser(1, nil)

	input := `[hls @ 0x55f8] Opening 'http://example.com/stream.m3u8' for reading
Server returned 503 Service Unavailable
`

	for _, line := range strings.Split(input, "\n") {
		if line != "" {
			p.ParseLine(line)
		}
	}

	stats := p.Stats()
	if stats.ManifestRequests != 1 {
		t.Errorf("ManifestRequests = %d, want 1", stats.ManifestRequests)
	}
}

func TestHLSEventParser_LatencyTracking(t *testing.T) {
	p := NewHLSEventParser(1, nil)

	// Start a segment request
	p.ParseLine("[https @ 0x55f8] Opening 'http://example.com/seg00001.ts' for reading")

	// Should have 1 in-flight request
	if p.InflightCount() != 1 {
		t.Errorf("InflightCount = %d, want 1", p.InflightCount())
	}

	// Wait a bit
	time.Sleep(10 * time.Millisecond)

	// Complete the oldest segment
	latency := p.CompleteOldestSegment()

	// Should have recorded latency
	if latency < 10*time.Millisecond {
		t.Errorf("latency = %v, want >= 10ms", latency)
	}

	// Should have 0 in-flight requests now
	if p.InflightCount() != 0 {
		t.Errorf("InflightCount = %d, want 0", p.InflightCount())
	}

	// Should have 1 latency sample
	latencies := p.Latencies()
	if len(latencies) != 1 {
		t.Errorf("len(latencies) = %d, want 1", len(latencies))
	}
}

func TestHLSEventParser_HangingRequestCleanup(t *testing.T) {
	p := NewHLSEventParser(1, nil)

	// Manually add a "hanging" request that's older than TTL
	oldTime := time.Now().Add(-2 * HangingRequestTTL)
	p.inflightRequests.Store("http://example.com/old_segment.ts", oldTime)

	// Add a recent request
	p.ParseLine("[https @ 0x55f8] Opening 'http://example.com/seg00001.ts' for reading")

	// Should have 2 in-flight requests
	if p.InflightCount() != 2 {
		t.Errorf("InflightCount = %d, want 2", p.InflightCount())
	}

	// Complete oldest segment (should clean up hanging request)
	p.CompleteOldestSegment()

	// Hanging request should be cleaned up and recorded as timeout
	stats := p.Stats()
	if stats.Timeouts != 1 {
		t.Errorf("Timeouts = %d, want 1 (hanging request)", stats.Timeouts)
	}

	// Should have 0 in-flight requests now (both cleaned up)
	if p.InflightCount() != 0 {
		t.Errorf("InflightCount = %d, want 0", p.InflightCount())
	}
}

func TestHLSEventParser_MultipleSegments(t *testing.T) {
	p := NewHLSEventParser(1, nil)

	// Start multiple segment requests
	p.ParseLine("[https @ 0x55f8] Opening 'http://example.com/seg00001.ts' for reading")
	time.Sleep(5 * time.Millisecond)
	p.ParseLine("[https @ 0x55f8] Opening 'http://example.com/seg00002.ts' for reading")
	time.Sleep(5 * time.Millisecond)
	p.ParseLine("[https @ 0x55f8] Opening 'http://example.com/seg00003.ts' for reading")

	// Should have 3 in-flight requests
	if p.InflightCount() != 3 {
		t.Errorf("InflightCount = %d, want 3", p.InflightCount())
	}

	// Complete oldest (should be seg00001)
	latency1 := p.CompleteOldestSegment()
	if latency1 < 10*time.Millisecond {
		t.Errorf("latency1 = %v, want >= 10ms", latency1)
	}

	// Complete next oldest (should be seg00002)
	latency2 := p.CompleteOldestSegment()
	if latency2 < 5*time.Millisecond {
		t.Errorf("latency2 = %v, want >= 5ms", latency2)
	}

	// Complete last (should be seg00003)
	latency3 := p.CompleteOldestSegment()
	if latency3 == 0 {
		t.Error("latency3 = 0, want > 0")
	}

	// Should have 0 in-flight requests
	if p.InflightCount() != 0 {
		t.Errorf("InflightCount = %d, want 0", p.InflightCount())
	}

	// Should have 3 latency samples
	latencies := p.Latencies()
	if len(latencies) != 3 {
		t.Errorf("len(latencies) = %d, want 3", len(latencies))
	}
}

func TestHLSEventParser_ClientID(t *testing.T) {
	p := NewHLSEventParser(42, nil)
	if p.ClientID() != 42 {
		t.Errorf("ClientID() = %d, want 42", p.ClientID())
	}
}

func TestHLSEventParser_ThreadSafety(t *testing.T) {
	p := NewHLSEventParser(1, nil)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				p.ParseLine("[hls @ 0x55f8] Opening 'http://example.com/stream.m3u8' for reading")
				p.ParseLine("[https @ 0x55f8] Opening 'http://example.com/seg00001.ts' for reading")
				p.ParseLine("Server returned 503 Service Unavailable")
				p.CompleteOldestSegment()
			}
		}(i)
	}

	wg.Wait()

	stats := p.Stats()
	// 10 goroutines * 100 iterations = 1000 of each
	if stats.ManifestRequests != 1000 {
		t.Errorf("ManifestRequests = %d, want 1000", stats.ManifestRequests)
	}
	if stats.SegmentRequests != 1000 {
		t.Errorf("SegmentRequests = %d, want 1000", stats.SegmentRequests)
	}
}

func TestHLSEventParser_RealWorldOutput(t *testing.T) {
	// Real FFmpeg output with various log prefixes
	input := `[hls @ 0x55f8a1b2c3d0] Opening 'http://10.177.0.10:17080/stream.m3u8' for reading
[hls @ 0x55f8a1b2c3d0] HLS request for url 'http://10.177.0.10:17080/stream.m3u8', offset 0
[https @ 0x55f8a1b2c3e0] Opening 'http://10.177.0.10:17080/seg00001.ts' for reading
[hls @ 0x55f8a1b2c3d0] HLS request for url 'http://10.177.0.10:17080/seg00001.ts', offset 0
frame=   30 fps=0.0 q=-1.0 size=N/A time=00:00:01.00 bitrate=N/A speed=N/A
[https @ 0x55f8a1b2c3e0] Opening 'http://10.177.0.10:17080/seg00002.ts' for reading
[hls @ 0x55f8a1b2c3d0] HLS request for url 'http://10.177.0.10:17080/seg00002.ts', offset 0
frame=   60 fps=30.0 q=-1.0 size=N/A time=00:00:02.00 bitrate=N/A speed=1.00x
`

	p := NewHLSEventParser(1, nil)

	for _, line := range strings.Split(input, "\n") {
		if line != "" {
			p.ParseLine(line)
		}
	}

	stats := p.Stats()

	// Should parse Opening lines correctly
	if stats.ManifestRequests != 1 {
		t.Errorf("ManifestRequests = %d, want 1", stats.ManifestRequests)
	}
	if stats.SegmentRequests != 2 {
		t.Errorf("SegmentRequests = %d, want 2", stats.SegmentRequests)
	}
}

func BenchmarkHLSEventParser_ParseLine(b *testing.B) {
	p := NewHLSEventParser(1, nil)

	lines := []string{
		"[hls @ 0x55f8] Opening 'http://example.com/stream.m3u8' for reading",
		"[https @ 0x55f8] Opening 'http://example.com/seg00001.ts' for reading",
		"Server returned 503 Service Unavailable",
		"Connection timed out",
		"Reconnecting to http://example.com",
		"frame=   60 fps=30.0 q=-1.0 size=N/A time=00:00:02.00 bitrate=N/A speed=1.00x",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, line := range lines {
			p.ParseLine(line)
		}
	}
}

func BenchmarkClassifyURL(b *testing.B) {
	urls := []string{
		"http://example.com/stream.m3u8",
		"http://example.com/seg00001.ts",
		"http://example.com/init.mp4",
		"http://example.com/stream.m3u8?token=abc",
		"http://example.com/unknown",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, url := range urls {
			classifyURL(url)
		}
	}
}
