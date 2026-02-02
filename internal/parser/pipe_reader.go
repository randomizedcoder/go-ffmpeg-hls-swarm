// Package parser provides parsers for FFmpeg output streams.
package parser

import (
	"bufio"
	"io"
	"sync/atomic"
)

// PipeReader reads lines from an io.Reader (FFmpeg stdout/stderr pipe).
// Implements the LineSource interface for uniform lifecycle management.
//
// Unlike SocketReader, PipeReader is immediately ready since pipes don't
// require a connection handshake.
type PipeReader struct {
	reader    io.Reader
	pipeline  *Pipeline
	readyChan chan struct{}
	closed    atomic.Bool

	// Stats (atomic for thread-safety)
	bytesRead atomic.Int64
	linesRead atomic.Int64
}

// NewPipeReader creates a new pipe-based line source.
//
// The reader is typically cmd.StdoutPipe() or cmd.StderrPipe().
func NewPipeReader(r io.Reader, pipeline *Pipeline) *PipeReader {
	pr := &PipeReader{
		reader:    r,
		pipeline:  pipeline,
		readyChan: make(chan struct{}),
	}
	// Pipe is immediately ready (unlike socket which needs Accept)
	close(pr.readyChan)
	return pr
}

// Run reads lines until EOF. Implements LineSource.
// MUST call pipeline.CloseChannel() on exit (I1).
func (p *PipeReader) Run() {
	// I1: Pipeline channel MUST be closed on exit
	defer p.pipeline.CloseChannel()

	scanner := bufio.NewScanner(p.reader)

	// Use a larger buffer for long FFmpeg output lines
	const maxLineSize = 64 * 1024
	scanner.Buffer(make([]byte, maxLineSize), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		p.bytesRead.Add(int64(len(line) + 1)) // +1 for newline
		p.linesRead.Add(1)
		p.pipeline.FeedLine(line)
	}
}

// Ready returns immediately-closed channel (pipe is always ready).
// Implements LineSource.
func (p *PipeReader) Ready() <-chan struct{} {
	return p.readyChan
}

// Close marks the reader as closed.
// Note: The underlying reader is typically closed by the process exiting.
// Implements LineSource.
func (p *PipeReader) Close() error {
	p.closed.Store(true)
	return nil
}

// Stats returns (bytesRead, linesRead, healthy).
// Implements LineSource.
func (p *PipeReader) Stats() (bytesRead int64, linesRead int64, healthy bool) {
	return p.bytesRead.Load(),
		p.linesRead.Load(),
		!p.closed.Load()
}

// Ensure PipeReader implements LineSource interface
var _ LineSource = (*PipeReader)(nil)
