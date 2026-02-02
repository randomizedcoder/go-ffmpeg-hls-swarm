// Package parser provides parsers for FFmpeg output streams.
package parser

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/influxdata/tdigest"
)

// SegmentSizeLookup is the interface for looking up segment sizes.
// Implemented by metrics.SegmentScraper.
type SegmentSizeLookup interface {
	GetSegmentSize(name string) (int64, bool)
}

// DebugEventType identifies debug log events from FFmpeg -loglevel debug.
type DebugEventType int

const (
	// Segment fetch events
	DebugEventHLSRequest   DebugEventType = iota // [hls @ ...] HLS request for url
	DebugEventHTTPOpen                           // [http @ ...] Opening '...' for reading
	DebugEventTCPStart                           // [tcp @ ...] Starting connection attempt
	DebugEventTCPConnected                       // [tcp @ ...] Successfully connected
	DebugEventTCPFailed                          // [tcp @ ...] Connection failed/refused/timeout

	// Playlist events (for jitter calculation)
	DebugEventPlaylistOpen   // [hls @ ...] Opening '...m3u8' for reading
	DebugEventSequenceChange // [hls @ ...] Media sequence change

	// Error events (critical for load testing)
	DebugEventHTTPError       // HTTP error 4xx/5xx
	DebugEventReconnect       // Will reconnect at...
	DebugEventSegmentFailed   // Failed to open segment
	DebugEventSegmentSkipped  // Segment failed too many times, skipping
	DebugEventPlaylistFailed  // Failed to reload playlist
	DebugEventSegmentsExpired // skipping N segments ahead, expired

	// Bandwidth events
	DebugEventBandwidth // BANDWIDTH=... from manifest parsing
)

// DebugEvent represents a parsed debug log event.
type DebugEvent struct {
	Type       DebugEventType
	Timestamp  time.Time
	URL        string
	IP         string
	Port       int
	OldSeq     int
	NewSeq     int
	FailReason string // "refused", "timeout", "error"
	Bandwidth  int64  // bits per second
	HTTPCode   int    // HTTP status code (4xx, 5xx)
	ErrorMsg   string // Error message text
	SkipCount  int    // Number of segments skipped
	PlaylistID int    // Playlist index
	SegmentID  int64  // Segment sequence number
	Bytes      int64  // Bytes downloaded (from Content-Length header)
}

// Pre-compiled regex patterns for performance.
// These match FFmpeg -loglevel debug output lines.
var (
	// FFmpeg timestamp prefix when using -loglevel repeat+level+datetime+debug
	// Format: "2026-01-23 08:12:52.613 [debug] " or "2026-01-23 08:12:52.613 [tcp @ 0x...] [verbose] "
	// We capture the timestamp and find where the actual message starts
	reTimestamp = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3}) `)

	// [hls @ 0x55...] HLS request for url 'http://.../seg00123.ts', offset 0, playlist 0
	reHLSRequest = regexp.MustCompile(`\[hls @ 0x[0-9a-f]+\] (?:\[(?:verbose|debug|info)\] )?HLS request for url '([^']+)'`)

	// [http @ 0x55...] Opening 'http://.../seg00123.ts' for reading
	// Captures the URL being opened - useful for HTTP-level timing
	reHTTPOpen = regexp.MustCompile(`\[http @ 0x[0-9a-f]+\] (?:\[(?:verbose|debug|info)\] )?Opening '([^']+)' for reading`)

	// [tcp @ 0x55...] Starting connection attempt to 10.177.0.10 port 17080
	reTCPStart = regexp.MustCompile(`\[tcp @ 0x[0-9a-f]+\] (?:\[(?:verbose|debug|info)\] )?Starting connection attempt to ([\d.]+) port (\d+)`)

	// [tcp @ 0x55...] Successfully connected to 10.177.0.10 port 17080
	reTCPConnected = regexp.MustCompile(`\[tcp @ 0x[0-9a-f]+\] (?:\[(?:verbose|debug|info)\] )?Successfully connected to ([\d.]+) port (\d+)`)

	// [tcp @ 0x55...] Connection refused / timed out / Failed to connect
	// Also matches: Connection attempt to ... failed: ...
	reTCPFailed = regexp.MustCompile(`(?i)\[tcp @ 0x[0-9a-f]+\] (?:\[(?:verbose|debug|info)\] )?(connection refused|connection timed out|failed to connect|connection attempt to .+ failed)`)

	// [hls @ 0x55...] Opening 'http://.../stream.m3u8' for reading
	// [AVFormatContext @ 0x55...] Opening 'http://.../stream.m3u8' for reading (initial open)
	// Also matches URLs with query strings like playlist.m3u8?token=xyz
	// Note: Initial manifest open uses AVFormatContext, refreshes use hls
	rePlaylistOpen = regexp.MustCompile(`\[(?:hls|AVFormatContext) @ 0x[0-9a-f]+\] (?:\[(?:verbose|debug|info)\] )?Opening '([^']+\.m3u8[^']*)' for reading`)

	// [hls @ 0x55...] Media sequence change (3433 -> 3438)
	reSequenceChange = regexp.MustCompile(`\[hls @ 0x[0-9a-f]+\] (?:\[(?:verbose|debug|info)\] )?Media sequence change \((\d+) -> (\d+)\)`)

	// BANDWIDTH=1234567 from manifest parsing
	reBandwidth = regexp.MustCompile(`BANDWIDTH=(\d+)`)

	// [hls @ 0x55...] Format hls probed with size=2048 and score=100
	// Indicates manifest download and parsing is complete (initial manifest only)
	reFormatProbed = regexp.MustCompile(`\[hls @ 0x[0-9a-f]+\] (?:\[(?:debug|verbose|info)\] )?Format hls probed with size=(\d+) and score=(\d+)`)

	// [hls @ 0x55...] Skip ('#EXT-X-VERSION:3')
	// Indicates manifest parsing has started (download complete) - appears on refreshes
	reManifestSkip = regexp.MustCompile(`\[hls @ 0x[0-9a-f]+\] (?:\[(?:verbose|debug|info)\] )?Skip \('#EXT-X-VERSION:`)

	// Error event patterns (critical for load testing)

	// [http @ 0x55...] HTTP error 503 Service Unavailable
	reHTTPError = regexp.MustCompile(`(?i)\[http @ 0x[0-9a-f]+\] (?:\[(?:warning|error)\] )?HTTP error (\d+) (.*)`)

	// Will reconnect at 12345 in 2 second(s)
	reReconnect = regexp.MustCompile(`(?i)Will reconnect at (\d+) in (\d+) second`)

	// [hls @ 0x55...] Failed to open segment 1234 of playlist 0
	reSegmentFailed = regexp.MustCompile(`\[hls @ 0x[0-9a-f]+\] (?:\[(?:warning|error)\] )?Failed to open segment (\d+) of playlist (\d+)`)

	// [hls @ 0x55...] Segment 1234 of playlist 0 failed too many times, skipping
	reSegmentSkipped = regexp.MustCompile(`\[hls @ 0x[0-9a-f]+\] (?:\[(?:warning|error)\] )?Segment (\d+) of playlist (\d+) failed too many times, skipping`)

	// [hls @ 0x55...] Failed to reload playlist 0
	rePlaylistFailed = regexp.MustCompile(`\[hls @ 0x[0-9a-f]+\] (?:\[(?:warning|error)\] )?Failed to reload playlist (\d+)`)

	// [hls @ 0x55...] skipping 5 segments ahead, expired from playlists
	reSegmentsExpired = regexp.MustCompile(`\[hls @ 0x[0-9a-f]+\] (?:\[(?:warning|error)\] )?skipping (\d+) segments? ahead, expired`)

	// [http @ 0x55...] header: Content-Length: 12345
	// Tracks bytes downloaded from HTTP responses (critical for live streams where total_size=N/A)
	reContentLength = regexp.MustCompile(`(?i)\[http @ 0x[0-9a-f]+\] (?:\[(?:trace|debug|verbose|info)\] )?header:.*Content-Length:\s*(\d+)`)

	// [http @ 0x55...] request: GET /seg00001.ts HTTP/1.1
	// Logged for EVERY HTTP request including keep-alive connections.
	// This is critical for tracking segment requests after initial parsing.
	// Captures the URL path (e.g., /seg00001.ts)
	reHTTPRequestGET = regexp.MustCompile(`\[http @ 0x[0-9a-f]+\] (?:\[(?:debug|verbose|info)\] )?request: GET ([^\s]+) HTTP/`)
)

// timestampLayout is the format FFmpeg uses with -loglevel datetime
const timestampLayout = "2006-01-02 15:04:05.000"

// parseTimestamp extracts a timestamp from an FFmpeg log line.
// Returns the timestamp and the remaining line content.
// If no timestamp is found, returns time.Time{} (zero) and the original line.
func parseTimestamp(line string) (time.Time, string) {
	if m := reTimestamp.FindStringSubmatch(line); m != nil {
		if ts, err := time.Parse(timestampLayout, m[1]); err == nil {
			// Strip timestamp from line for further processing
			return ts, line[len(m[0]):]
		}
	}
	return time.Time{}, line
}

// DebugEventCallback is called for each parsed debug event.
type DebugEventCallback func(*DebugEvent)

// SegmentDownloadState tracks wall time for a single segment download.
type SegmentDownloadState struct {
	URL       string
	StartTime time.Time
}

// DebugEventParser parses FFmpeg -loglevel debug output.
// Implements LineParser interface.
//
// Extracts high-value metrics:
//   - Segment Download Wall Time (PRIMARY - reliable under keep-alive)
//   - TCP Connect Latency (SECONDARY - only for new connections)
//   - Playlist Refresh Jitter
//   - TCP Connection Health Ratio
type DebugEventParser struct {
	clientID       int
	callback       DebugEventCallback
	targetDuration time.Duration

	mu sync.Mutex

	// Manifest bandwidth (parsed from FFmpeg debug output)
	manifestBandwidth atomic.Int64 // bits per second

	// Segment Wall Time tracking (PRIMARY - reliable under keep-alive)
	// Maps URL -> start time
	pendingSegments   map[string]time.Time
	segmentWallTimes  []time.Duration // Ring buffer (last N samples)
	segmentWallTimeP0 int             // Ring buffer position
	segmentCount      atomic.Int64

	// Segment wall time aggregates
	segmentWallTimeSum   int64 // nanoseconds
	segmentWallTimeMax   int64 // nanoseconds
	segmentWallTimeMin   int64 // nanoseconds (-1 = unset)

	// Segment wall time percentiles (using accurate FFmpeg timestamps)
	segmentWallTimeDigest *tdigest.TDigest
	segmentWallTimeDigestMu sync.Mutex // TDigest is not thread-safe

	// Manifest Wall Time tracking (similar to Segment Wall Time)
	// Maps URL -> start time
	pendingManifests   map[string]time.Time
	manifestWallTimes  []time.Duration // Ring buffer (last N samples)
	manifestWallTimeP0 int             // Ring buffer position
	manifestCount      atomic.Int64

	// Manifest wall time aggregates
	manifestWallTimeSum   int64 // nanoseconds
	manifestWallTimeMax   int64 // nanoseconds
	manifestWallTimeMin   int64 // nanoseconds (-1 = unset)

	// Manifest wall time percentiles (using accurate FFmpeg timestamps)
	manifestWallTimeDigest *tdigest.TDigest
	manifestWallTimeDigestMu sync.Mutex // TDigest is not thread-safe

	// TCP Connect tracking (SECONDARY - only for new connections)
	// Maps "IP:port" -> connect start time
	pendingTCPConnect  map[string]time.Time
	tcpConnectSamples  []time.Duration // Ring buffer
	tcpConnectP0       int             // Ring buffer position
	tcpConnectCount    atomic.Int64
	tcpConnectSum      int64 // nanoseconds
	tcpConnectMax      int64 // nanoseconds
	tcpConnectMin      int64 // nanoseconds (-1 = unset)

	// Timestamp parsing stats
	timestampsUsed atomic.Int64 // Lines where FFmpeg timestamp was used

	// TCP Health (success/failure ratio)
	tcpSuccessCount atomic.Int64
	tcpFailureCount atomic.Int64
	tcpTimeoutCount atomic.Int64
	tcpRefusedCount atomic.Int64

	// Playlist jitter tracking
	lastPlaylistRefresh time.Time
	playlistRefreshes   atomic.Int64
	playlistLateCount   atomic.Int64
	playlistJitterSum   int64 // nanoseconds (signed: early is negative)
	playlistJitterMax   int64 // nanoseconds (absolute max deviation)

	// Sequence tracking
	lastSequence  int
	sequenceSkips atomic.Int64

	// Error event counters (critical for load testing)
	httpErrorCount      atomic.Int64 // HTTP 4xx/5xx errors
	http4xxCount        atomic.Int64 // Client errors
	http5xxCount        atomic.Int64 // Server errors
	reconnectCount      atomic.Int64 // Reconnection attempts
	segmentFailedCount  atomic.Int64 // Segment open failures
	segmentSkippedCount atomic.Int64 // Segments skipped after retries
	playlistFailedCount atomic.Int64 // Playlist reload failures
	segmentsExpiredSum  atomic.Int64 // Total segments skipped due to expiry

	// HTTP open timing (for request vs download separation)
	pendingHTTPOpen   map[string]time.Time
	httpOpenCount     atomic.Int64
	httpOpenSum       int64 // nanoseconds
	httpOpenMax       int64 // nanoseconds

	// Bytes tracking (from HTTP Content-Length headers)
	// Critical for live streams where progress total_size=N/A
	bytesDownloaded atomic.Int64

	// Segment size lookup (injected dependency for accurate byte tracking)
	segmentSizeLookup SegmentSizeLookup

	// Segment bytes tracking (from segment scraper, accurate sizes)
	// This tracks bytes from COMPLETED segment downloads only
	segmentBytesDownloaded atomic.Int64

	// Segment size lookup diagnostics
	segmentSizeLookupAttempts  atomic.Int64 // Total lookup attempts
	segmentSizeLookupSuccesses atomic.Int64 // Successful lookups (size found)

	// Parser stats
	linesProcessed atomic.Int64
}

const (
	// defaultRingSize is the number of samples to keep for percentile calculations.
	defaultRingSize = 100
)

// extractSegmentName extracts the filename from a segment URL.
// Example: "http://10.177.0.10:17080/seg00017.ts" -> "seg00017.ts"
func extractSegmentName(url string) string {
	if idx := strings.LastIndex(url, "/"); idx >= 0 {
		return url[idx+1:]
	}
	return url
}

// NewDebugEventParser creates a new debug event parser.
//
// Parameters:
//   - clientID: Client identifier for logging
//   - targetDuration: Expected HLS segment duration (for jitter calculation)
//   - callback: Called for each parsed event (can be nil)
func NewDebugEventParser(clientID int, targetDuration time.Duration, callback DebugEventCallback) *DebugEventParser {
	return NewDebugEventParserWithSizeLookup(clientID, targetDuration, callback, nil)
}

// NewDebugEventParserWithSizeLookup creates a new debug event parser with segment size lookup.
//
// Parameters:
//   - clientID: Client identifier for logging
//   - targetDuration: Expected HLS segment duration (for jitter calculation)
//   - callback: Called for each parsed event (can be nil)
//   - sizeLookup: Segment size lookup for accurate byte tracking (can be nil)
func NewDebugEventParserWithSizeLookup(clientID int, targetDuration time.Duration, callback DebugEventCallback, sizeLookup SegmentSizeLookup) *DebugEventParser {
	if targetDuration <= 0 {
		targetDuration = 2 * time.Second // HLS default
	}
	return &DebugEventParser{
		clientID:               clientID,
		callback:               callback,
		targetDuration:         targetDuration,
		pendingSegments:        make(map[string]time.Time),
		segmentWallTimes:       make([]time.Duration, 0, defaultRingSize),
		pendingTCPConnect:      make(map[string]time.Time),
		tcpConnectSamples:      make([]time.Duration, 0, defaultRingSize),
		pendingHTTPOpen:        make(map[string]time.Time),
		segmentWallTimeMin:     -1, // -1 = unset
		tcpConnectMin:          -1, // -1 = unset
		segmentWallTimeDigest:  tdigest.NewWithCompression(100), // ~100 centroids, ~10KB
		pendingManifests:       make(map[string]time.Time),
		manifestWallTimeMin:    -1, // -1 = unset
		manifestWallTimeDigest: tdigest.NewWithCompression(100), // ~100 centroids, ~10KB
		segmentSizeLookup:      sizeLookup,
	}
}

// ParseLine implements LineParser interface.
// Supports both timestamped logs (-loglevel repeat+level+datetime+debug)
// and non-timestamped logs. When timestamps are present, uses them
// for accurate timing instead of wall clock time.
func (p *DebugEventParser) ParseLine(line string) {
	p.linesProcessed.Add(1)

	// Fast path: most lines don't match any pattern
	// Check for common keywords to skip irrelevant lines quickly
	if !strings.Contains(line, " @ 0x") &&
		!strings.Contains(line, "BANDWIDTH=") &&
		!strings.Contains(line, "Format") &&
		!strings.Contains(line, "Skip") &&
		!strings.Contains(line, "HTTP error") &&
		!strings.Contains(line, "reconnect") &&
		!strings.Contains(line, "Failed to") &&
		!strings.Contains(line, "skipping") {
		return
	}

	// Parse FFmpeg timestamp if present (from -loglevel datetime)
	// This gives us accurate timing even if logs back up in channels
	parsedTs, line := parseTimestamp(line)

	var now time.Time
	if !parsedTs.IsZero() {
		now = parsedTs
		p.timestampsUsed.Add(1)
	} else {
		now = time.Now()
	}

	// Check patterns in order of expected frequency

	// 1. TCP Connected (completes TCP timing)
	if m := reTCPConnected.FindStringSubmatch(line); m != nil {
		p.handleTCPConnected(now, m[1], m[2])
		return
	}

	// 2. HLS Request (starts segment wall time tracking)
	if m := reHLSRequest.FindStringSubmatch(line); m != nil {
		p.handleHLSRequest(now, m[1])
		return
	}

	// 3. HTTP Open (for HTTP-level timing, mainly for new connections)
	if m := reHTTPOpen.FindStringSubmatch(line); m != nil {
		p.handleHTTPOpen(now, m[1])
		return
	}

	// 3b. HTTP GET request (for ALL requests including keep-alive)
	// This is critical for steady-state segment tracking after initial parsing.
	// The "Opening" line only fires for new connections, but "request: GET" fires for every request.
	if m := reHTTPRequestGET.FindStringSubmatch(line); m != nil {
		p.handleHTTPRequestGET(now, m[1])
		return
	}

	// 4. TCP Start (starts TCP connect timing)
	if m := reTCPStart.FindStringSubmatch(line); m != nil {
		p.handleTCPStart(now, m[1], m[2])
		return
	}

	// 5. TCP Failed
	if m := reTCPFailed.FindStringSubmatch(line); m != nil {
		p.handleTCPFailed(now, m[1])
		return
	}

	// 6. Playlist Open (for jitter tracking)
	if m := rePlaylistOpen.FindStringSubmatch(line); m != nil {
		p.handlePlaylistOpen(now, m[1])
		return
	}

	// 7. Sequence Change
	if m := reSequenceChange.FindStringSubmatch(line); m != nil {
		oldSeq, _ := strconv.Atoi(m[1])
		newSeq, _ := strconv.Atoi(m[2])
		p.handleSequenceChange(now, oldSeq, newSeq)
		return
	}

	// 8. Format Probed (manifest download and parsing complete - initial manifest only)
	if m := reFormatProbed.FindStringSubmatch(line); m != nil {
		p.handleFormatProbed(now)
		return
	}

	// 9. Manifest Skip (manifest parsing started - download complete, appears on refreshes)
	if reManifestSkip.MatchString(line) {
		p.handleFormatProbed(now) // Reuse same handler - completes pending manifest
		return
	}

	// 10. Bandwidth (can appear anywhere in manifest parsing)
	if m := reBandwidth.FindStringSubmatch(line); m != nil {
		bw, _ := strconv.ParseInt(m[1], 10, 64)
		p.manifestBandwidth.Store(bw)
		if p.callback != nil {
			p.callback(&DebugEvent{
				Type:      DebugEventBandwidth,
				Timestamp: now,
				Bandwidth: bw,
			})
		}
		return
	}

	// Error events (less frequent but critical for load testing)

	// 11. HTTP Error (4xx/5xx)
	if m := reHTTPError.FindStringSubmatch(line); m != nil {
		code, _ := strconv.Atoi(m[1])
		p.handleHTTPError(now, code, m[2])
		return
	}

	// 12. Content-Length header (tracks bytes downloaded - critical for live streams)
	if m := reContentLength.FindStringSubmatch(line); m != nil {
		if size, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			p.bytesDownloaded.Add(size)
			// Emit event for callback to update ClientStats
			if p.callback != nil {
				p.callback(&DebugEvent{
					Type:      DebugEventHTTPOpen, // Reuse HTTPOpen type
					Timestamp: now,
					Bytes:     size,
				})
			}
		}
		return
	}

	// 13. Reconnect attempt
	if m := reReconnect.FindStringSubmatch(line); m != nil {
		p.handleReconnect(now)
		return
	}

	// 14. Segment failed
	if m := reSegmentFailed.FindStringSubmatch(line); m != nil {
		segID, _ := strconv.ParseInt(m[1], 10, 64)
		playlistID, _ := strconv.Atoi(m[2])
		p.handleSegmentFailed(now, segID, playlistID)
		return
	}

	// 15. Segment skipped (after max retries)
	if m := reSegmentSkipped.FindStringSubmatch(line); m != nil {
		segID, _ := strconv.ParseInt(m[1], 10, 64)
		playlistID, _ := strconv.Atoi(m[2])
		p.handleSegmentSkipped(now, segID, playlistID)
		return
	}

	// 16. Playlist failed
	if m := rePlaylistFailed.FindStringSubmatch(line); m != nil {
		playlistID, _ := strconv.Atoi(m[1])
		p.handlePlaylistFailed(now, playlistID)
		return
	}

	// 17. Segments expired
	if m := reSegmentsExpired.FindStringSubmatch(line); m != nil {
		skipCount, _ := strconv.Atoi(m[1])
		p.handleSegmentsExpired(now, skipCount)
		return
	}
}

// handleFormatProbed is called when manifest format is probed.
// This indicates the manifest download and parsing is complete.
func (p *DebugEventParser) handleFormatProbed(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Complete oldest pending manifest (if any)
	// Format probed happens immediately after manifest download and parsing
	if len(p.pendingManifests) > 0 {
		var oldestURL string
		var oldestTime time.Time
		for u, t := range p.pendingManifests {
			if oldestTime.IsZero() || t.Before(oldestTime) {
				oldestURL = u
				oldestTime = t
			}
		}
		if oldestURL != "" {
			wallTime := now.Sub(oldestTime)
			delete(p.pendingManifests, oldestURL)

			// Record manifest wall time (similar to segment wall time)
			ns := int64(wallTime)
			p.manifestCount.Add(1)
			p.manifestWallTimeSum += ns

			if p.manifestWallTimeMin < 0 || ns < p.manifestWallTimeMin {
				p.manifestWallTimeMin = ns
			}
			if ns > p.manifestWallTimeMax {
				p.manifestWallTimeMax = ns
			}

			// Ring buffer
			if len(p.manifestWallTimes) < defaultRingSize {
				p.manifestWallTimes = append(p.manifestWallTimes, wallTime)
			} else {
				p.manifestWallTimes[p.manifestWallTimeP0] = wallTime
				p.manifestWallTimeP0 = (p.manifestWallTimeP0 + 1) % defaultRingSize
			}

			// Add to T-Digest for percentile calculation
			p.manifestWallTimeDigestMu.Lock()
			p.manifestWallTimeDigest.Add(float64(wallTime.Nanoseconds()), 1)
			p.manifestWallTimeDigestMu.Unlock()
		}
	}
}

// handleHLSRequest is called when a segment request starts.
// Automatically completes the oldest pending segment (if any) using the timestamp
// from this log line for accurate timing.
func (p *DebugEventParser) handleHLSRequest(now time.Time, url string) {
	p.mu.Lock()

	// Complete oldest pending segment (if any) before starting new one
	// This uses the timestamp from the log line for accurate timing
	if len(p.pendingSegments) > 0 {
		var oldestURL string
		var oldestTime time.Time
		for u, t := range p.pendingSegments {
			if oldestTime.IsZero() || t.Before(oldestTime) {
				oldestURL = u
				oldestTime = t
			}
		}
		if oldestURL != "" {
			// Complete using timestamp from log (accurate)
			wallTime := now.Sub(oldestTime)
			delete(p.pendingSegments, oldestURL)

			ns := int64(wallTime)
			p.segmentCount.Add(1)
			p.segmentWallTimeSum += ns

			if p.segmentWallTimeMin < 0 || ns < p.segmentWallTimeMin {
				p.segmentWallTimeMin = ns
			}
			if ns > p.segmentWallTimeMax {
				p.segmentWallTimeMax = ns
			}

			// Ring buffer
			if len(p.segmentWallTimes) < defaultRingSize {
				p.segmentWallTimes = append(p.segmentWallTimes, wallTime)
			} else {
				p.segmentWallTimes[p.segmentWallTimeP0] = wallTime
				p.segmentWallTimeP0 = (p.segmentWallTimeP0 + 1) % defaultRingSize
			}

			// Add to T-Digest for percentile calculation (using accurate timestamps)
			p.segmentWallTimeDigestMu.Lock()
			p.segmentWallTimeDigest.Add(float64(wallTime.Nanoseconds()), 1)
			p.segmentWallTimeDigestMu.Unlock()

			// Track segment bytes from scraper (accurate sizes for completed downloads)
			// Design decision: Count bytes only on "segment complete" to ensure
			// bytes represent successful downloads only (see SEGMENT_SIZE_TRACKING_DESIGN.md)
			if p.segmentSizeLookup != nil {
				segmentName := extractSegmentName(oldestURL)
				p.segmentSizeLookupAttempts.Add(1)
				if size, ok := p.segmentSizeLookup.GetSegmentSize(segmentName); ok {
					p.segmentBytesDownloaded.Add(size)
					p.segmentSizeLookupSuccesses.Add(1)
				}
			}
		}
	}

	// Start tracking new segment
	p.pendingSegments[url] = now
	p.mu.Unlock()

	if p.callback != nil {
		p.callback(&DebugEvent{
			Type:      DebugEventHLSRequest,
			Timestamp: now,
			URL:       url,
		})
	}
}

// handleTCPStart is called when TCP connection starts.
func (p *DebugEventParser) handleTCPStart(now time.Time, ip, portStr string) {
	port, _ := strconv.Atoi(portStr)
	key := ip + ":" + portStr

	p.mu.Lock()
	p.pendingTCPConnect[key] = now
	p.mu.Unlock()

	if p.callback != nil {
		p.callback(&DebugEvent{
			Type:      DebugEventTCPStart,
			Timestamp: now,
			IP:        ip,
			Port:      port,
		})
	}
}

// handleTCPConnected is called when TCP connection succeeds.
func (p *DebugEventParser) handleTCPConnected(now time.Time, ip, portStr string) {
	port, _ := strconv.Atoi(portStr)
	key := ip + ":" + portStr

	p.tcpSuccessCount.Add(1)

	p.mu.Lock()
	if startTime, ok := p.pendingTCPConnect[key]; ok {
		connectTime := now.Sub(startTime)
		delete(p.pendingTCPConnect, key)

		// Record TCP connect sample
		p.recordTCPConnect(connectTime)
	}
	p.mu.Unlock()

	if p.callback != nil {
		p.callback(&DebugEvent{
			Type:      DebugEventTCPConnected,
			Timestamp: now,
			IP:        ip,
			Port:      port,
		})
	}
}

// handleTCPFailed is called when TCP connection fails.
func (p *DebugEventParser) handleTCPFailed(now time.Time, reason string) {
	p.tcpFailureCount.Add(1)

	failReason := "error"
	if strings.Contains(strings.ToLower(reason), "refused") {
		failReason = "refused"
		p.tcpRefusedCount.Add(1)
	} else if strings.Contains(strings.ToLower(reason), "timeout") || strings.Contains(strings.ToLower(reason), "timed out") {
		failReason = "timeout"
		p.tcpTimeoutCount.Add(1)
	}

	if p.callback != nil {
		p.callback(&DebugEvent{
			Type:       DebugEventTCPFailed,
			Timestamp:  now,
			FailReason: failReason,
		})
	}
}

// handlePlaylistOpen is called when manifest is refreshed.
func (p *DebugEventParser) handlePlaylistOpen(now time.Time, url string) {
	p.playlistRefreshes.Add(1)

	// Track manifest download start time
	p.mu.Lock()
	p.pendingManifests[url] = now
	p.mu.Unlock()

	p.mu.Lock()
	if !p.lastPlaylistRefresh.IsZero() {
		interval := now.Sub(p.lastPlaylistRefresh)
		jitter := interval - p.targetDuration

		// Track jitter sum (signed)
		p.playlistJitterSum += int64(jitter)

		// Track max absolute jitter
		absJitter := jitter
		if absJitter < 0 {
			absJitter = -absJitter
		}
		if int64(absJitter) > p.playlistJitterMax {
			p.playlistJitterMax = int64(absJitter)
		}

		// Track late refreshes (> targetDuration)
		if jitter > 0 {
			p.playlistLateCount.Add(1)
		}
	}
	p.lastPlaylistRefresh = now
	p.mu.Unlock()

	if p.callback != nil {
		p.callback(&DebugEvent{
			Type:      DebugEventPlaylistOpen,
			Timestamp: now,
			URL:       url,
		})
	}
}

// handleSequenceChange is called when media sequence changes.
func (p *DebugEventParser) handleSequenceChange(now time.Time, oldSeq, newSeq int) {
	p.mu.Lock()
	if p.lastSequence > 0 {
		expected := p.lastSequence + 1
		if newSeq != expected {
			// Sequence skip detected
			p.sequenceSkips.Add(1)
		}
	}
	p.lastSequence = newSeq
	p.mu.Unlock()

	if p.callback != nil {
		p.callback(&DebugEvent{
			Type:      DebugEventSequenceChange,
			Timestamp: now,
			OldSeq:    oldSeq,
			NewSeq:    newSeq,
		})
	}
}

// handleHTTPOpen is called when HTTP protocol opens a URL.
// This is useful for measuring HTTP-level timing separate from HLS-level.
//
// IMPORTANT: FFmpeg only logs "HLS request for url" during initial playlist parsing.
// After that, segment downloads are only visible at HTTP layer. So we ALSO track
// segment completions here for .ts files to ensure throughput tracking works
// throughout the test, not just during ramp-up.
func (p *DebugEventParser) handleHTTPOpen(now time.Time, url string) {
	p.httpOpenCount.Add(1)

	// Track segment downloads from HTTP layer (backup for HLS layer)
	// FFmpeg logs "HLS request for url" only during initial parsing, but continues
	// logging HTTP opens for all subsequent segment fetches.
	if strings.HasSuffix(url, ".ts") || strings.Contains(url, ".ts?") {
		p.trackSegmentFromHTTP(now, url)
	}

	// Track HTTP open for potential timing (from HLS request to HTTP open)
	p.mu.Lock()
	p.pendingHTTPOpen[url] = now
	p.mu.Unlock()

	if p.callback != nil {
		p.callback(&DebugEvent{
			Type:      DebugEventHTTPOpen,
			Timestamp: now,
			URL:       url,
		})
	}
}

// handleHTTPRequestGET is called for HTTP GET requests.
// This fires for EVERY HTTP request including keep-alive connections.
// Critical for tracking segment requests in steady state after initial parsing.
func (p *DebugEventParser) handleHTTPRequestGET(now time.Time, path string) {
	// Track segment downloads from HTTP layer
	// The path is like /seg00001.ts or /stream.m3u8
	if strings.HasSuffix(path, ".ts") || strings.Contains(path, ".ts?") {
		p.trackSegmentFromHTTP(now, path)
	}

	// Note: We don't increment httpOpenCount here to avoid double-counting
	// with handleHTTPOpen for the same request on new connections.
}

// trackSegmentFromHTTP tracks segment completions based on HTTP events.
// This mirrors handleHLSRequest logic but triggers from HTTP layer.
// Needed because FFmpeg only logs HLS-specific events during initial playlist parsing.
func (p *DebugEventParser) trackSegmentFromHTTP(now time.Time, url string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Complete oldest pending segment (if any) before starting new one
	if len(p.pendingSegments) > 0 {
		var oldestURL string
		var oldestTime time.Time
		for u, t := range p.pendingSegments {
			if oldestTime.IsZero() || t.Before(oldestTime) {
				oldestURL = u
				oldestTime = t
			}
		}
		if oldestURL != "" {
			// Compare segment NAMES instead of raw URLs to handle different URL formats:
			// - HLS Request: http://10.177.0.10:17080/seg00001.ts (full URL)
			// - HTTP Open:   http://10.177.0.10:17080/seg00001.ts (full URL)
			// - HTTP GET:    /seg00001.ts (path only)
			// All extract to "seg00001.ts" via extractSegmentName
			oldestSegment := extractSegmentName(oldestURL)
			newSegment := extractSegmentName(url)

			// Skip if this is the SAME segment (double-fire: HLS then HTTP for same segment)
			// But ALWAYS complete if it's a DIFFERENT segment (new segment arriving)
			if oldestSegment == newSegment {
				// Same segment - likely HLS/HTTP Open event just fired, skip to avoid double-counting
				// Just update the timestamp and URL to the latest event
				delete(p.pendingSegments, oldestURL)
				p.pendingSegments[url] = now
				return
			}

			// Different segment - complete the old one
			wallTime := now.Sub(oldestTime)
			delete(p.pendingSegments, oldestURL)

			ns := int64(wallTime)
			p.segmentCount.Add(1)
			p.segmentWallTimeSum += ns

			if p.segmentWallTimeMin < 0 || ns < p.segmentWallTimeMin {
				p.segmentWallTimeMin = ns
			}
			if ns > p.segmentWallTimeMax {
				p.segmentWallTimeMax = ns
			}

			// Ring buffer for wall times
			if len(p.segmentWallTimes) < defaultRingSize {
				p.segmentWallTimes = append(p.segmentWallTimes, wallTime)
			} else {
				p.segmentWallTimes[p.segmentWallTimeP0] = wallTime
				p.segmentWallTimeP0 = (p.segmentWallTimeP0 + 1) % defaultRingSize
			}

			// Add to T-Digest for percentile calculation
			p.segmentWallTimeDigestMu.Lock()
			p.segmentWallTimeDigest.Add(float64(wallTime.Nanoseconds()), 1)
			p.segmentWallTimeDigestMu.Unlock()

			// Track segment bytes from scraper (accurate sizes for completed downloads)
			if p.segmentSizeLookup != nil {
				segmentName := extractSegmentName(oldestURL)
				p.segmentSizeLookupAttempts.Add(1)
				if size, ok := p.segmentSizeLookup.GetSegmentSize(segmentName); ok {
					p.segmentBytesDownloaded.Add(size)
					p.segmentSizeLookupSuccesses.Add(1)
				}
			}
		}
	}

	// Start tracking new segment
	p.pendingSegments[url] = now
}

// handleHTTPError is called when HTTP 4xx/5xx error occurs.
func (p *DebugEventParser) handleHTTPError(now time.Time, code int, message string) {
	p.httpErrorCount.Add(1)
	if code >= 400 && code < 500 {
		p.http4xxCount.Add(1)
	} else if code >= 500 {
		p.http5xxCount.Add(1)
	}

	if p.callback != nil {
		p.callback(&DebugEvent{
			Type:      DebugEventHTTPError,
			Timestamp: now,
			HTTPCode:  code,
			ErrorMsg:  message,
		})
	}
}

// handleReconnect is called when FFmpeg attempts reconnection.
func (p *DebugEventParser) handleReconnect(now time.Time) {
	p.reconnectCount.Add(1)

	if p.callback != nil {
		p.callback(&DebugEvent{
			Type:      DebugEventReconnect,
			Timestamp: now,
		})
	}
}

// handleSegmentFailed is called when segment open fails.
func (p *DebugEventParser) handleSegmentFailed(now time.Time, segmentID int64, playlistID int) {
	p.segmentFailedCount.Add(1)

	if p.callback != nil {
		p.callback(&DebugEvent{
			Type:       DebugEventSegmentFailed,
			Timestamp:  now,
			SegmentID:  segmentID,
			PlaylistID: playlistID,
		})
	}
}

// handleSegmentSkipped is called when segment is skipped after max retries.
func (p *DebugEventParser) handleSegmentSkipped(now time.Time, segmentID int64, playlistID int) {
	p.segmentSkippedCount.Add(1)

	if p.callback != nil {
		p.callback(&DebugEvent{
			Type:       DebugEventSegmentSkipped,
			Timestamp:  now,
			SegmentID:  segmentID,
			PlaylistID: playlistID,
		})
	}
}

// handlePlaylistFailed is called when playlist reload fails.
func (p *DebugEventParser) handlePlaylistFailed(now time.Time, playlistID int) {
	p.playlistFailedCount.Add(1)

	if p.callback != nil {
		p.callback(&DebugEvent{
			Type:       DebugEventPlaylistFailed,
			Timestamp:  now,
			PlaylistID: playlistID,
		})
	}
}

// handleSegmentsExpired is called when segments expire from playlist.
func (p *DebugEventParser) handleSegmentsExpired(now time.Time, skipCount int) {
	p.segmentsExpiredSum.Add(int64(skipCount))

	if p.callback != nil {
		p.callback(&DebugEvent{
			Type:      DebugEventSegmentsExpired,
			Timestamp: now,
			SkipCount: skipCount,
		})
	}
}

// recordTCPConnect records a TCP connect time sample.
// MUST be called with mu held.
func (p *DebugEventParser) recordTCPConnect(d time.Duration) {
	ns := int64(d)
	p.tcpConnectCount.Add(1)
	p.tcpConnectSum += ns

	if p.tcpConnectMin < 0 || ns < p.tcpConnectMin {
		p.tcpConnectMin = ns
	}
	if ns > p.tcpConnectMax {
		p.tcpConnectMax = ns
	}

	// Ring buffer
	if len(p.tcpConnectSamples) < defaultRingSize {
		p.tcpConnectSamples = append(p.tcpConnectSamples, d)
	} else {
		p.tcpConnectSamples[p.tcpConnectP0] = d
		p.tcpConnectP0 = (p.tcpConnectP0 + 1) % defaultRingSize
	}
}

// CompleteSegment records segment wall time when segment download completes.
// Call this when you detect segment completion (e.g., next HLS request for same playlist).
//
// This is called externally because FFmpeg debug logs don't have explicit
// "segment complete" events. The caller must infer completion.
//
// Uses time.Now() for timing. For better accuracy with FFmpeg timestamps,
// use CompleteSegmentWithTimestamp() instead.
func (p *DebugEventParser) CompleteSegment(url string) {
	p.CompleteSegmentWithTimestamp(url, time.Now())
}

// CompleteSegmentWithTimestamp records segment wall time using the provided timestamp.
// This is more accurate when the timestamp comes from FFmpeg log timestamps.
func (p *DebugEventParser) CompleteSegmentWithTimestamp(url string, endTime time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if startTime, ok := p.pendingSegments[url]; ok {
		wallTime := endTime.Sub(startTime)
		delete(p.pendingSegments, url)

		ns := int64(wallTime)
		p.segmentCount.Add(1)
		p.segmentWallTimeSum += ns

		if p.segmentWallTimeMin < 0 || ns < p.segmentWallTimeMin {
			p.segmentWallTimeMin = ns
		}
		if ns > p.segmentWallTimeMax {
			p.segmentWallTimeMax = ns
		}

		// Ring buffer
		if len(p.segmentWallTimes) < defaultRingSize {
			p.segmentWallTimes = append(p.segmentWallTimes, wallTime)
		} else {
			p.segmentWallTimes[p.segmentWallTimeP0] = wallTime
			p.segmentWallTimeP0 = (p.segmentWallTimeP0 + 1) % defaultRingSize
		}

		// Add to T-Digest for percentile calculation (using accurate timestamps)
		p.segmentWallTimeDigestMu.Lock()
		p.segmentWallTimeDigest.Add(float64(wallTime.Nanoseconds()), 1)
		p.segmentWallTimeDigestMu.Unlock()
	}
}

// DebugStats contains aggregated debug parser statistics.
type DebugStats struct {
	// Lines processed
	LinesProcessed int64

	// Timestamp usage (for accuracy tracking)
	// When > 0, timing is based on FFmpeg timestamps (more accurate)
	// When 0, timing is based on wall clock (may have channel delay)
	TimestampsUsed int64

	// Manifest bandwidth (bits per second)
	ManifestBandwidth int64

	// Segment wall time (PRIMARY metric - using accurate FFmpeg timestamps)
	SegmentCount int64
	SegmentAvgMs float64
	SegmentMinMs float64
	SegmentMaxMs float64
	// Percentiles (from T-Digest, using accurate timestamps)
	SegmentWallTimeP25 time.Duration // 25th percentile
	SegmentWallTimeP50 time.Duration // 50th percentile (median)
	SegmentWallTimeP75 time.Duration // 75th percentile
	SegmentWallTimeP95 time.Duration // 95th percentile
	SegmentWallTimeP99 time.Duration // 99th percentile

	// Manifest wall time (using accurate FFmpeg timestamps)
	ManifestCount int64
	ManifestAvgMs float64
	ManifestMinMs float64
	ManifestMaxMs float64
	// Percentiles (from T-Digest, using accurate timestamps)
	ManifestWallTimeP25 time.Duration // 25th percentile
	ManifestWallTimeP50 time.Duration // 50th percentile (median)
	ManifestWallTimeP75 time.Duration // 75th percentile
	ManifestWallTimeP95 time.Duration // 95th percentile
	ManifestWallTimeP99 time.Duration // 99th percentile

	// TCP connect time (SECONDARY metric - only new connections)
	TCPConnectCount int64
	TCPConnectAvgMs float64
	TCPConnectMinMs float64
	TCPConnectMaxMs float64

	// TCP health ratio
	TCPSuccessCount int64
	TCPFailureCount int64
	TCPTimeoutCount int64
	TCPRefusedCount int64
	TCPHealthRatio  float64 // success / (success + failure)

	// Playlist jitter
	PlaylistRefreshes   int64
	PlaylistLateCount   int64
	PlaylistAvgJitterMs float64
	PlaylistMaxJitterMs float64

	// Sequence tracking
	SequenceSkips int64

	// Error events (critical for load testing)
	HTTPErrorCount      int64   // Total HTTP 4xx/5xx errors
	HTTP4xxCount        int64   // Client errors (4xx)
	HTTP5xxCount        int64   // Server errors (5xx)
	ReconnectCount      int64   // Reconnection attempts
	SegmentFailedCount  int64   // Segment open failures
	SegmentSkippedCount int64   // Segments skipped after retries
	PlaylistFailedCount int64   // Playlist reload failures
	SegmentsExpiredSum  int64   // Total segments expired from playlist
	ErrorRate           float64 // (errors / total requests) if calculable

	// HTTP open count (for request tracking)
	HTTPOpenCount int64

	// Bytes downloaded (from HTTP Content-Length headers)
	// Critical for live streams where progress total_size=N/A
	BytesDownloaded int64

	// Segment bytes downloaded (from segment scraper, accurate sizes)
	// Only counts bytes from completed segment downloads (not failed attempts)
	SegmentBytesDownloaded int64

	// Segment size lookup diagnostics
	SegmentSizeLookupAttempts  int64 // Total lookup attempts
	SegmentSizeLookupSuccesses int64 // Successful lookups
}

// Stats returns aggregated debug parser statistics.
func (p *DebugEventParser) Stats() DebugStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	stats := DebugStats{
		LinesProcessed:    p.linesProcessed.Load(),
		TimestampsUsed:    p.timestampsUsed.Load(),
		ManifestBandwidth: p.manifestBandwidth.Load(),
		SegmentCount:      p.segmentCount.Load(),
		TCPConnectCount:   p.tcpConnectCount.Load(),
		TCPSuccessCount:   p.tcpSuccessCount.Load(),
		TCPFailureCount:   p.tcpFailureCount.Load(),
		TCPTimeoutCount:   p.tcpTimeoutCount.Load(),
		TCPRefusedCount:   p.tcpRefusedCount.Load(),
		PlaylistRefreshes: p.playlistRefreshes.Load(),
		PlaylistLateCount: p.playlistLateCount.Load(),
		SequenceSkips:     p.sequenceSkips.Load(),
		ManifestCount:     p.manifestCount.Load(),

		// Error metrics
		HTTPErrorCount:      p.httpErrorCount.Load(),
		HTTP4xxCount:        p.http4xxCount.Load(),
		HTTP5xxCount:        p.http5xxCount.Load(),
		ReconnectCount:      p.reconnectCount.Load(),
		SegmentFailedCount:  p.segmentFailedCount.Load(),
		SegmentSkippedCount: p.segmentSkippedCount.Load(),
		PlaylistFailedCount: p.playlistFailedCount.Load(),
		SegmentsExpiredSum:  p.segmentsExpiredSum.Load(),
		HTTPOpenCount:              p.httpOpenCount.Load(),
		BytesDownloaded:            p.bytesDownloaded.Load(),
		SegmentBytesDownloaded:     p.segmentBytesDownloaded.Load(),
		SegmentSizeLookupAttempts:  p.segmentSizeLookupAttempts.Load(),
		SegmentSizeLookupSuccesses: p.segmentSizeLookupSuccesses.Load(),
	}

	// Segment wall time averages
	if stats.SegmentCount > 0 {
		stats.SegmentAvgMs = float64(p.segmentWallTimeSum) / float64(stats.SegmentCount) / 1e6
		if p.segmentWallTimeMin >= 0 {
			stats.SegmentMinMs = float64(p.segmentWallTimeMin) / 1e6
		}
		stats.SegmentMaxMs = float64(p.segmentWallTimeMax) / 1e6

		// Calculate percentiles from T-Digest (using accurate FFmpeg timestamps)
		p.segmentWallTimeDigestMu.Lock()
		if p.segmentWallTimeDigest != nil {
			stats.SegmentWallTimeP25 = time.Duration(p.segmentWallTimeDigest.Quantile(0.25))
			stats.SegmentWallTimeP50 = time.Duration(p.segmentWallTimeDigest.Quantile(0.50))
			stats.SegmentWallTimeP75 = time.Duration(p.segmentWallTimeDigest.Quantile(0.75))
			stats.SegmentWallTimeP95 = time.Duration(p.segmentWallTimeDigest.Quantile(0.95))
			stats.SegmentWallTimeP99 = time.Duration(p.segmentWallTimeDigest.Quantile(0.99))
		}
		p.segmentWallTimeDigestMu.Unlock()
	}

	// TCP connect averages
	if stats.TCPConnectCount > 0 {
		stats.TCPConnectAvgMs = float64(p.tcpConnectSum) / float64(stats.TCPConnectCount) / 1e6
		if p.tcpConnectMin >= 0 {
			stats.TCPConnectMinMs = float64(p.tcpConnectMin) / 1e6
		}
		stats.TCPConnectMaxMs = float64(p.tcpConnectMax) / 1e6
	}

	// TCP health ratio
	total := stats.TCPSuccessCount + stats.TCPFailureCount
	if total > 0 {
		stats.TCPHealthRatio = float64(stats.TCPSuccessCount) / float64(total)
	} else {
		stats.TCPHealthRatio = 1.0 // No failures = healthy
	}

	// Manifest wall time averages
	if stats.ManifestCount > 0 {
		stats.ManifestAvgMs = float64(p.manifestWallTimeSum) / float64(stats.ManifestCount) / 1e6
		if p.manifestWallTimeMin >= 0 {
			stats.ManifestMinMs = float64(p.manifestWallTimeMin) / 1e6
		}
		stats.ManifestMaxMs = float64(p.manifestWallTimeMax) / 1e6

		// Calculate percentiles from T-Digest (using accurate FFmpeg timestamps)
		p.manifestWallTimeDigestMu.Lock()
		if p.manifestWallTimeDigest != nil {
			stats.ManifestWallTimeP25 = time.Duration(p.manifestWallTimeDigest.Quantile(0.25))
			stats.ManifestWallTimeP50 = time.Duration(p.manifestWallTimeDigest.Quantile(0.50))
			stats.ManifestWallTimeP75 = time.Duration(p.manifestWallTimeDigest.Quantile(0.75))
			stats.ManifestWallTimeP95 = time.Duration(p.manifestWallTimeDigest.Quantile(0.95))
			stats.ManifestWallTimeP99 = time.Duration(p.manifestWallTimeDigest.Quantile(0.99))
		}
		p.manifestWallTimeDigestMu.Unlock()
	}

	// Playlist jitter
	if stats.PlaylistRefreshes > 1 {
		// Average jitter (can be negative if mostly early)
		stats.PlaylistAvgJitterMs = float64(p.playlistJitterSum) / float64(stats.PlaylistRefreshes-1) / 1e6
		stats.PlaylistMaxJitterMs = float64(p.playlistJitterMax) / 1e6
	}

	// Error rate: (HTTP errors + segment failures) / total HTTP opens
	if stats.HTTPOpenCount > 0 {
		totalErrors := stats.HTTPErrorCount + stats.SegmentFailedCount
		stats.ErrorRate = float64(totalErrors) / float64(stats.HTTPOpenCount)
	}

	return stats
}

// GetManifestBandwidth returns the parsed BANDWIDTH value (bits/sec).
// Returns 0 if not yet parsed.
func (p *DebugEventParser) GetManifestBandwidth() int64 {
	return p.manifestBandwidth.Load()
}

// ClientID returns the client ID for this parser.
func (p *DebugEventParser) ClientID() int {
	return p.clientID
}
