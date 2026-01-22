// Package parser provides lossy-by-design parsing pipelines for FFmpeg output.
//
// At 200–1000 clients, parsing bursts can't always keep up. The pipeline
// architecture ensures the metrics feature never sabotages the load test itself
// by blocking FFmpeg's stdout/stderr.
//
// Three-Layer Architecture:
//
//	Layer 1 (Reader): Reads lines fast, drops if channel full - never blocks
//	Layer 2 (Parser): Consumes from channel at own pace
//	Layer 3 (Stats):  Records what was parsed for aggregation
package parser

import (
	"bufio"
	"io"
	"sync/atomic"
)

// LineParser is implemented by ProgressParser and HLSEventParser.
type LineParser interface {
	ParseLine(line string)
}

// Pipeline implements three-layer lossy-by-design parsing.
//
// It reads lines from an io.Reader into a bounded channel. If the parser
// cannot keep up, lines are dropped rather than blocking the writer (FFmpeg).
type Pipeline struct {
	clientID   int
	streamType string // "progress" or "stderr"
	bufferSize int

	lineChan chan string

	// Pipeline health metrics (atomic for concurrent access)
	linesRead    int64
	linesDropped int64
	linesParsed  int64

	// Configurable threshold for degradation detection
	dropThreshold float64
}

// NewPipeline creates a lossy parsing pipeline.
//
// Parameters:
//   - clientID: Client identifier for logging
//   - streamType: "progress" or "stderr" for identification
//   - bufferSize: Channel buffer size (lines)
//   - dropThreshold: Fraction (0.0-1.0) above which metrics are degraded
func NewPipeline(clientID int, streamType string, bufferSize int, dropThreshold float64) *Pipeline {
	if bufferSize < 1 {
		bufferSize = 1000 // Default
	}
	if dropThreshold <= 0 {
		dropThreshold = 0.01 // Default 1%
	}

	return &Pipeline{
		clientID:      clientID,
		streamType:    streamType,
		bufferSize:    bufferSize,
		lineChan:      make(chan string, bufferSize),
		dropThreshold: dropThreshold,
	}
}

// RunReader is Layer 1: reads lines fast, drops if channel full.
//
// MUST run in dedicated goroutine. Never blocks on channel send.
// Closes lineChan when reader reaches EOF.
func (p *Pipeline) RunReader(r io.Reader) {
	scanner := bufio.NewScanner(r)

	// Use a larger buffer for long FFmpeg output lines
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		atomic.AddInt64(&p.linesRead, 1)

		// Non-blocking send - drop if channel full
		select {
		case p.lineChan <- line:
			// Successfully queued
		default:
			// Channel full - drop intentionally to avoid blocking FFmpeg
			atomic.AddInt64(&p.linesDropped, 1)
		}
	}

	// Close channel to signal parser to stop
	close(p.lineChan)
}

// RunParser is Layer 2: consumes lines at own pace.
//
// MUST run in dedicated goroutine. Blocks until lineChan is closed.
func (p *Pipeline) RunParser(parser LineParser) {
	for line := range p.lineChan {
		parser.ParseLine(line)
		atomic.AddInt64(&p.linesParsed, 1)
	}
}

// Stats returns pipeline health metrics.
//
// Returns:
//   - read: Total lines read from io.Reader
//   - dropped: Lines dropped due to full channel
//   - parsed: Lines successfully parsed
func (p *Pipeline) Stats() (read, dropped, parsed int64) {
	return atomic.LoadInt64(&p.linesRead),
		atomic.LoadInt64(&p.linesDropped),
		atomic.LoadInt64(&p.linesParsed)
}

// DropRate returns the current drop rate as a fraction (0.0 to 1.0).
func (p *Pipeline) DropRate() float64 {
	read := atomic.LoadInt64(&p.linesRead)
	if read == 0 {
		return 0
	}
	dropped := atomic.LoadInt64(&p.linesDropped)
	return float64(dropped) / float64(read)
}

// IsDegraded returns true if drop rate exceeds the configured threshold.
//
// Default threshold is 1% (0.01). When degraded, metrics may be incomplete
// and should be treated with caution.
func (p *Pipeline) IsDegraded() bool {
	return p.DropRate() > p.dropThreshold
}

// ClientID returns the client ID for this pipeline.
func (p *Pipeline) ClientID() int {
	return p.clientID
}

// StreamType returns "progress" or "stderr".
func (p *Pipeline) StreamType() string {
	return p.streamType
}

// DrainChannel reads and discards any remaining lines in the channel.
// Useful for cleanup when you don't care about remaining data.
func (p *Pipeline) DrainChannel() {
	for range p.lineChan {
		// Discard
	}
}

// NoopParser is a parser that does nothing (for testing/placeholder use).
type NoopParser struct{}

// ParseLine does nothing.
func (NoopParser) ParseLine(string) {}
