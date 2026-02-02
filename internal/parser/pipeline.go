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
	"sync"
	"sync/atomic"
)

// LineParser is implemented by ProgressParser and HLSEventParser.
type LineParser interface {
	ParseLine(line string)
}

// LineSource abstracts the source of lines for a Pipeline.
// Both PipeReader (stdout) and SocketReader (Unix socket) implement this.
//
// Lifecycle (MUST be followed by Supervisor):
//
//  1. source := NewXxxReader(...)
//  2. go source.Run()        // Start reading in goroutine
//  3. defer source.Close()   // Cleanup on exit
//  4. <-source.Ready()       // Wait for source to be accepting/reading
//  5. // ... start FFmpeg ...
//
// The source is responsible for calling pipeline.CloseChannel() on exit.
type LineSource interface {
	// Run starts reading lines and feeding them to the pipeline.
	// MUST call pipeline.CloseChannel() on exit (via defer).
	// Blocks until source is exhausted or closed.
	Run()

	// Ready returns a channel that is closed when the source is ready.
	// For PipeReader: closed immediately (pipe is always ready).
	// For SocketReader: closed when Accept() is about to block.
	Ready() <-chan struct{}

	// Close stops the source and releases resources.
	// Safe to call multiple times (idempotent).
	Close() error

	// Stats returns (bytesRead, linesRead, healthy).
	// healthy = true if source is working normally.
	Stats() (bytesRead int64, linesRead int64, healthy bool)
}

// Pipeline implements three-layer lossy-by-design parsing.
//
// It reads lines from an io.Reader into a bounded channel. If the parser
// cannot keep up, lines are dropped rather than blocking the writer (FFmpeg).
type Pipeline struct {
	clientID   int
	streamType string // "progress" or "stderr"
	bufferSize int

	lineChan  chan string
	closeOnce sync.Once // Ensures CloseChannel() is idempotent

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
//
// Note: For pipe mode, this is the standard reader. For socket mode,
// use SocketReader.Run() instead which calls FeedLine().
func (p *Pipeline) RunReader(r io.Reader) {
	// I4: Use CloseChannel() for symmetry with socket mode
	defer p.CloseChannel()

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
}

// FeedLine adds a line to the pipeline from an external source (e.g., socket).
// Returns true if queued, false if dropped (channel full).
//
// This is the socket-mode equivalent of the read loop in RunReader().
// Instead of reading from an io.Reader, lines are fed directly from SocketReader.
func (p *Pipeline) FeedLine(line string) bool {
	atomic.AddInt64(&p.linesRead, 1)

	select {
	case p.lineChan <- line:
		return true
	default:
		atomic.AddInt64(&p.linesDropped, 1)
		return false
	}
}

// CloseChannel closes the line channel, signaling parser to stop.
// Must be called when the source (pipe or socket) is done.
//
// CRITICAL (I1): This MUST be called exactly once by the data source:
//   - Pipe mode: RunReader() calls CloseChannel() at EOF (via defer)
//   - Socket mode: SocketReader.Run() calls CloseChannel() on exit (via defer)
//
// This is the sole mechanism for parser goroutine termination.
// Failure to call this results in goroutine leaks.
//
// Safe to call multiple times (idempotent via sync.Once).
func (p *Pipeline) CloseChannel() {
	p.closeOnce.Do(func() {
		close(p.lineChan)
	})
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
