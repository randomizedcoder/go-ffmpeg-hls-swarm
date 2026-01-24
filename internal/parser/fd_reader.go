// Package parser provides parsers for FFmpeg output streams.
package parser

import (
	"bufio"
	"os"
	"sync/atomic"
)

// FDReader reads lines from a file descriptor (passed via cmd.ExtraFiles).
// Implements the LineSource interface for uniform lifecycle management.
//
// Unlike SocketReader, FDReader is immediately ready since the FD is already
// open and connected. Unlike PipeReader, FDReader reads from an *os.File
// that was passed to the child process via ExtraFiles.
//
// Usage:
//  1. pr, pw, _ := os.Pipe()
//  2. reader := NewFDReader(pr, pipeline)
//  3. cmd.ExtraFiles = []*os.File{pw}
//  4. go reader.Run()
//  5. cmd.Start()
//  6. pw.Close() // Close parent's write-end after Start()
type FDReader struct {
	file      *os.File
	pipeline  *Pipeline
	readyChan chan struct{}
	closed    atomic.Bool

	// Stats (atomic for thread-safety)
	bytesRead atomic.Int64
	linesRead atomic.Int64
}

// NewFDReader creates a new FD-based line source.
//
// The file is the read-end of a pipe that was passed to the child process
// via cmd.ExtraFiles. The child writes to FD 3 (or 4, 5, etc. depending on
// ExtraFiles index), and this reader reads from the parent's read-end.
func NewFDReader(file *os.File, pipeline *Pipeline) *FDReader {
	fr := &FDReader{
		file:      file,
		pipeline:  pipeline,
		readyChan: make(chan struct{}),
	}
	// FD is immediately ready (unlike socket which needs Accept)
	close(fr.readyChan)
	return fr
}

// Run reads lines until EOF. Implements LineSource.
// MUST call pipeline.CloseChannel() on exit (I1).
func (f *FDReader) Run() {
	// I1: Pipeline channel MUST be closed on exit
	defer f.pipeline.CloseChannel()

	scanner := bufio.NewScanner(f.file)

	// Use a larger buffer for long FFmpeg output lines
	const maxLineSize = 64 * 1024
	scanner.Buffer(make([]byte, maxLineSize), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		f.bytesRead.Add(int64(len(line) + 1)) // +1 for newline
		f.linesRead.Add(1)
		f.pipeline.FeedLine(line)
	}
}

// Ready returns immediately-closed channel (FD is always ready).
// Implements LineSource.
func (f *FDReader) Ready() <-chan struct{} {
	return f.readyChan
}

// Close closes the file descriptor and marks the reader as closed.
// Implements LineSource.
func (f *FDReader) Close() error {
	if !f.closed.Swap(true) {
		f.file.Close()
	}
	return nil
}

// Stats returns (bytesRead, linesRead, healthy).
// Implements LineSource.
func (f *FDReader) Stats() (bytesRead int64, linesRead int64, healthy bool) {
	return f.bytesRead.Load(),
		f.linesRead.Load(),
		!f.closed.Load()
}

// Ensure FDReader implements LineSource interface
var _ LineSource = (*FDReader)(nil)
