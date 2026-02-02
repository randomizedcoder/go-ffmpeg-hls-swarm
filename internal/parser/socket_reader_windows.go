//go:build windows

// Package parser provides parsers for FFmpeg output streams.
package parser

import (
	"errors"
	"log/slog"
)

// ErrUnixSocketsNotSupported is returned on Windows where Unix sockets are not available.
var ErrUnixSocketsNotSupported = errors.New("unix sockets not supported on Windows")

// SocketReader is a stub for Windows compilation.
// Unix sockets are not supported on Windows.
type SocketReader struct{}

// NewSocketReader returns an error on Windows (no Unix socket support).
func NewSocketReader(socketPath string, pipeline *Pipeline, logger *slog.Logger) (*SocketReader, error) {
	return nil, ErrUnixSocketsNotSupported
}

// Run is a no-op on Windows.
func (r *SocketReader) Run() {}

// Ready returns a closed channel on Windows.
func (r *SocketReader) Ready() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// Close is a no-op on Windows.
func (r *SocketReader) Close() error { return nil }

// Stats returns zero stats on Windows.
func (r *SocketReader) Stats() (int64, int64, bool) { return 0, 0, false }

// SocketPath returns empty string on Windows.
func (r *SocketReader) SocketPath() string { return "" }

// FailedToConnect always returns true on Windows.
func (r *SocketReader) FailedToConnect() bool { return true }

// Ensure SocketReader implements LineSource interface
var _ LineSource = (*SocketReader)(nil)
