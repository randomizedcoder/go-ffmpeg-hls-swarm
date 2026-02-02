// Package parser provides parsing for FFmpeg output streams.
//
// This file implements the HLSEventParser which parses FFmpeg stderr for
// HLS-specific events like URL requests, HTTP errors, reconnections, and timeouts.
//
// Tested FFmpeg Version:
//
//	ffmpeg version 8.0 Copyright (c) 2000-2025 the FFmpeg developers
//	built with gcc 15.2.0 (GCC)
//	libavutil      60.  8.100
//	libavcodec     62. 11.100
//	libavformat    62.  3.100
//
// If parsing breaks after an FFmpeg upgrade, the stderr format may have changed.
// See testdata/ffmpeg_stderr.txt for expected format.
//
// Example FFmpeg stderr output (verbose level):
//
//	[hls @ 0x55f8a1b2c3d0] Opening 'http://10.177.0.10:17080/stream.m3u8' for reading
//	[https @ 0x55f8a1b2c3e0] Opening 'http://10.177.0.10:17080/seg00123.ts' for reading
//	Server returned 503 Service Unavailable
//	Connection timed out
//	Reconnecting to http://example.com
package parser

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// URLType identifies the type of URL being requested.
type URLType int

const (
	URLTypeUnknown  URLType = iota // Unrecognized URL pattern (fallback bucket)
	URLTypeManifest                // .m3u8 playlist
	URLTypeSegment                 // .ts segment
	URLTypeInit                    // .mp4 init segment (fMP4)
)

// String returns a human-readable name for the URL type.
func (t URLType) String() string {
	switch t {
	case URLTypeManifest:
		return "manifest"
	case URLTypeSegment:
		return "segment"
	case URLTypeInit:
		return "init"
	default:
		return "unknown"
	}
}

// HLSEventType identifies the type of HLS event.
type HLSEventType int

const (
	EventUnknown    HLSEventType = iota // Unrecognized event
	EventRequest                        // URL opened for reading
	EventHTTPError                      // Server returned error
	EventReconnect                      // Reconnection attempt
	EventTimeout                        // Connection timeout
)

// String returns a human-readable name for the event type.
func (t HLSEventType) String() string {
	switch t {
	case EventRequest:
		return "request"
	case EventHTTPError:
		return "http_error"
	case EventReconnect:
		return "reconnect"
	case EventTimeout:
		return "timeout"
	default:
		return "unknown"
	}
}

// HLSEvent represents a parsed HLS event from FFmpeg stderr.
type HLSEvent struct {
	Type      HLSEventType
	URL       string
	URLType   URLType
	HTTPCode  int
	Timestamp time.Time
}

// HLSEventCallback is called for each parsed HLS event.
type HLSEventCallback func(*HLSEvent)

// Regex patterns for parsing FFmpeg stderr.
// These are compiled once at package init for performance.
var (
	// Opening 'http://example.com/stream.m3u8' for reading
	reOpening = regexp.MustCompile(`Opening '([^']+)' for reading`)

	// Server returned 503 Service Unavailable
	// Server returned 404 Not Found
	reHTTPErr = regexp.MustCompile(`Server returned (\d{3})`)

	// Reconnecting to http://example.com
	reReconn = regexp.MustCompile(`Reconnecting`)

	// Connection timed out
	// timed out
	// timeout
	reTimeout = regexp.MustCompile(`(?i)(timed? ?out|timeout)`)
)

// HLSEventParser parses FFmpeg stderr for HLS events.
//
// It implements the LineParser interface for use with Pipeline.
// Thread-safe: can be called from multiple goroutines.
type HLSEventParser struct {
	clientID int
	callback HLSEventCallback

	mu sync.Mutex

	// Counters (atomic for concurrent access)
	manifestRequests int64
	segmentRequests  int64
	initRequests     int64
	unknownRequests  int64 // Fallback bucket for unrecognized URL patterns
	reconnections    int64
	timeouts         int64

	// HTTP errors by status code
	httpErrors map[int]int64

	// Latency tracking - maps URL to request start time
	// Uses sync.Map for concurrent access without blocking
	inflightRequests sync.Map // map[string]time.Time

	// Latency samples (protected by mu)
	latencies []time.Duration

	// Stats
	linesProcessed int64
	eventsEmitted  int64
}

// NewHLSEventParser creates a new HLS event parser.
//
// Parameters:
//   - clientID: Client identifier for logging
//   - callback: Optional callback for each parsed event (can be nil)
func NewHLSEventParser(clientID int, callback HLSEventCallback) *HLSEventParser {
	return &HLSEventParser{
		clientID:   clientID,
		callback:   callback,
		httpErrors: make(map[int]int64),
		latencies:  make([]time.Duration, 0, 100),
	}
}

// ParseLine implements the LineParser interface.
//
// This is the primary entry point when used with Pipeline.
func (p *HLSEventParser) ParseLine(line string) {
	p.parseLine(line)
}

// parseLine handles a single line of stderr.
func (p *HLSEventParser) parseLine(line string) {
	atomic.AddInt64(&p.linesProcessed, 1)

	// Opening URL - most common event, check first
	if m := reOpening.FindStringSubmatch(line); m != nil {
		url := m[1]
		urlType := classifyURL(url)

		event := &HLSEvent{
			Type:      EventRequest,
			URL:       url,
			URLType:   urlType,
			Timestamp: time.Now(),
		}

		switch urlType {
		case URLTypeManifest:
			atomic.AddInt64(&p.manifestRequests, 1)
		case URLTypeSegment:
			atomic.AddInt64(&p.segmentRequests, 1)
			// Track segment request start for latency calculation
			p.inflightRequests.Store(url, time.Now())
		case URLTypeInit:
			atomic.AddInt64(&p.initRequests, 1)
		default:
			// Fallback bucket for unrecognized URL patterns
			// Helps diagnose CDN behavior (byte-range playlists, signed URLs, etc.)
			atomic.AddInt64(&p.unknownRequests, 1)
		}

		p.emitEvent(event)
		return
	}

	// HTTP error
	if m := reHTTPErr.FindStringSubmatch(line); m != nil {
		code, _ := strconv.Atoi(m[1])

		p.mu.Lock()
		p.httpErrors[code]++
		p.mu.Unlock()

		event := &HLSEvent{
			Type:      EventHTTPError,
			HTTPCode:  code,
			Timestamp: time.Now(),
		}
		p.emitEvent(event)
		return
	}

	// Reconnection
	if reReconn.MatchString(line) {
		atomic.AddInt64(&p.reconnections, 1)

		event := &HLSEvent{
			Type:      EventReconnect,
			Timestamp: time.Now(),
		}
		p.emitEvent(event)
		return
	}

	// Timeout
	if reTimeout.MatchString(line) {
		atomic.AddInt64(&p.timeouts, 1)

		event := &HLSEvent{
			Type:      EventTimeout,
			Timestamp: time.Now(),
		}
		p.emitEvent(event)
		return
	}
}

// emitEvent calls the callback if set.
func (p *HLSEventParser) emitEvent(event *HLSEvent) {
	atomic.AddInt64(&p.eventsEmitted, 1)
	if p.callback != nil {
		p.callback(event)
	}
}

// HangingRequestTTL is the maximum time a request can be in-flight before
// being considered "hanging" and cleaned up. This prevents memory leaks
// from requests that start but never complete (e.g., connection dropped).
const HangingRequestTTL = 60 * time.Second

// CompleteOldestSegment completes the oldest in-flight segment request.
//
// This is called when we receive a progress update, which indicates that
// a segment download likely completed. Since FFmpeg doesn't emit explicit
// "download complete" events, we infer completion from progress updates.
//
// Returns the latency of the completed segment, or 0 if no segment was in-flight.
//
// IMPORTANT: This also cleans up "hanging" requests older than HangingRequestTTL
// to prevent memory leaks. Hanging requests are recorded as timeouts.
func (p *HLSEventParser) CompleteOldestSegment() time.Duration {
	var oldestURL string
	var oldestTime time.Time
	var hangingURLs []string
	now := time.Now()

	// Find oldest segment request and identify hanging requests
	p.inflightRequests.Range(func(key, value interface{}) bool {
		url := key.(string)
		startTime := value.(time.Time)

		// CRITICAL: Clean up hanging requests (older than TTL)
		// If a request starts but never completes (e.g., connection dropped
		// without error), it would stay in sync.Map forever, leaking memory.
		if now.Sub(startTime) > HangingRequestTTL {
			hangingURLs = append(hangingURLs, url)
			return true
		}

		// Only consider .ts segments for latency (not manifests)
		if strings.HasSuffix(strings.ToLower(url), ".ts") ||
			(strings.Contains(strings.ToLower(url), ".ts?")) {
			if oldestTime.IsZero() || startTime.Before(oldestTime) {
				oldestURL = url
				oldestTime = startTime
			}
		}
		return true
	})

	// Clean up hanging requests and record as timeouts
	for _, url := range hangingURLs {
		p.inflightRequests.Delete(url)
		atomic.AddInt64(&p.timeouts, 1)
	}

	// Complete the oldest segment
	if oldestURL != "" {
		p.inflightRequests.Delete(oldestURL)
		latency := now.Sub(oldestTime)
		p.recordLatency(latency)
		return latency
	}

	return 0
}

// recordLatency adds a latency sample.
func (p *HLSEventParser) recordLatency(latency time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Keep last 1000 samples (ring buffer behavior)
	if len(p.latencies) >= 1000 {
		// Shift left and append
		copy(p.latencies, p.latencies[1:])
		p.latencies[len(p.latencies)-1] = latency
	} else {
		p.latencies = append(p.latencies, latency)
	}
}

// Stats returns parser statistics.
type HLSStats struct {
	ManifestRequests int64
	SegmentRequests  int64
	InitRequests     int64
	UnknownRequests  int64 // Fallback bucket
	Reconnections    int64
	Timeouts         int64
	HTTPErrors       map[int]int64
	LinesProcessed   int64
	EventsEmitted    int64
}

// Stats returns current statistics.
func (p *HLSEventParser) Stats() HLSStats {
	p.mu.Lock()
	httpErrors := make(map[int]int64, len(p.httpErrors))
	for k, v := range p.httpErrors {
		httpErrors[k] = v
	}
	p.mu.Unlock()

	return HLSStats{
		ManifestRequests: atomic.LoadInt64(&p.manifestRequests),
		SegmentRequests:  atomic.LoadInt64(&p.segmentRequests),
		InitRequests:     atomic.LoadInt64(&p.initRequests),
		UnknownRequests:  atomic.LoadInt64(&p.unknownRequests),
		Reconnections:    atomic.LoadInt64(&p.reconnections),
		Timeouts:         atomic.LoadInt64(&p.timeouts),
		HTTPErrors:       httpErrors,
		LinesProcessed:   atomic.LoadInt64(&p.linesProcessed),
		EventsEmitted:    atomic.LoadInt64(&p.eventsEmitted),
	}
}

// Latencies returns a copy of the latency samples.
func (p *HLSEventParser) Latencies() []time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()

	result := make([]time.Duration, len(p.latencies))
	copy(result, p.latencies)
	return result
}

// InflightCount returns the number of in-flight requests.
func (p *HLSEventParser) InflightCount() int {
	count := 0
	p.inflightRequests.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}

// ClientID returns the client ID for this parser.
func (p *HLSEventParser) ClientID() int {
	return p.clientID
}

// classifyURL determines the type of URL based on file extension.
//
// Handles both plain URLs and URLs with query strings.
// Returns URLTypeUnknown for unrecognized patterns (fallback bucket).
func classifyURL(url string) URLType {
	lower := strings.ToLower(url)

	// Check for query strings - extract path before '?'
	path := lower
	if idx := strings.Index(lower, "?"); idx > 0 {
		path = lower[:idx]
	}

	// Check file extensions
	if strings.HasSuffix(path, ".m3u8") {
		return URLTypeManifest
	}
	if strings.HasSuffix(path, ".ts") {
		return URLTypeSegment
	}
	if strings.HasSuffix(path, ".mp4") {
		return URLTypeInit
	}

	// Fallback bucket for unrecognized patterns
	// This helps diagnose CDN behavior (byte-range playlists, signed URLs, etc.)
	return URLTypeUnknown
}
