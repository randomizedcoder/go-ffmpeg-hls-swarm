//go:build !windows

// Package parser provides parsers for FFmpeg output streams.
package parser

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// maxUnixSocketPathLen is the safe maximum path length for Unix sockets.
	// sockaddr_un.sun_path is typically 108 bytes; we use 104 for safety.
	maxUnixSocketPathLen = 104

	// socketConnectGrace is how long to wait for FFmpeg to connect.
	// If exceeded, assume FFmpeg doesn't support unix:// - caller should retry with pipe.
	socketConnectGrace = 3 * time.Second
)

// SocketReader reads FFmpeg progress output from a Unix domain socket.
// Implements the LineSource interface for uniform lifecycle management.
//
// Critical Invariants:
//   - I1: Pipeline channel MUST be closed on Run() exit (prevents goroutine leaks)
//   - I2: Socket path MUST be ≤104 bytes (Unix socket path limit)
//   - I3: Ready() signal MUST be sent before FFmpeg starts (prevents races)
//
// Lifecycle:
//  1. reader, err := NewSocketReader(path, pipeline, logger)
//  2. go reader.Run()        // Start accepting in goroutine
//  3. defer reader.Close()   // Cleanup on exit
//  4. <-reader.Ready()       // Wait until Accept() is ready
//  5. // ... start FFmpeg ...
type SocketReader struct {
	socketPath string
	listener   net.Listener
	pipeline   *Pipeline
	logger     *slog.Logger

	// Synchronization
	readyChan  chan struct{} // Closed when Accept() is ready
	closedOnce sync.Once     // Ensures Close() is idempotent
	cleanedUp  atomic.Bool   // Tracks if cleanup has run

	// State
	failedToConnect atomic.Bool // True if FFmpeg never connected
	conn            net.Conn    // Active connection (if any)
	connMu          sync.Mutex  // Protects conn

	// Stats (atomic for thread-safety)
	bytesRead atomic.Int64
	linesRead atomic.Int64
}

// validateSocketPath checks that path is within Unix socket length limits.
func validateSocketPath(path string) error {
	if len(path) > maxUnixSocketPathLen {
		return fmt.Errorf("socket path too long (%d > %d bytes): use shorter TMPDIR: %s",
			len(path), maxUnixSocketPathLen, path)
	}
	return nil
}

// NewSocketReader creates a Unix socket and returns a reader.
// Returns error if path is too long or socket creation fails.
//
// The socket is created immediately; call Run() to start reading.
// The caller is responsible for calling Close() to clean up resources.
func NewSocketReader(socketPath string, pipeline *Pipeline, logger *slog.Logger) (*SocketReader, error) {
	// I2: Validate path length BEFORE attempting to create
	if err := validateSocketPath(socketPath); err != nil {
		return nil, err // Caller falls back to pipe mode
	}

	// Clean up stale socket from previous run (crash recovery)
	if _, err := os.Stat(socketPath); err == nil {
		if err := os.Remove(socketPath); err != nil {
			if logger != nil {
				logger.Debug("failed to remove stale socket",
					"path", socketPath,
					"error", err,
				)
			}
		} else if logger != nil {
			logger.Debug("removed stale socket", "path", socketPath)
		}
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create socket %s: %w", socketPath, err)
	}

	return &SocketReader{
		socketPath: socketPath,
		listener:   listener,
		pipeline:   pipeline,
		logger:     logger,
		readyChan:  make(chan struct{}),
	}, nil
}

// Ready returns a channel that is closed when the reader is accepting connections.
// I3: Callers MUST wait on this before starting FFmpeg to prevent races.
func (r *SocketReader) Ready() <-chan struct{} {
	return r.readyChan
}

// FailedToConnect returns true if FFmpeg never connected within grace period.
// Callers should fall back to pipe mode for subsequent restarts.
func (r *SocketReader) FailedToConnect() bool {
	return r.failedToConnect.Load()
}

// Run accepts one connection and reads lines until EOF.
// I1: ALWAYS closes pipeline channel on exit (prevents goroutine leaks).
// Must be called in a goroutine.
func (r *SocketReader) Run() {
	// I1: Pipeline channel MUST be closed on exit - this is THE source of truth
	defer r.pipeline.CloseChannel()
	defer r.cleanup()

	// I3: Signal that we're ready to accept connections
	close(r.readyChan)

	// Set deadline for FFmpeg to connect
	if ul, ok := r.listener.(*net.UnixListener); ok {
		if err := ul.SetDeadline(time.Now().Add(socketConnectGrace)); err != nil {
			if r.logger != nil {
				r.logger.Warn("failed to set accept deadline",
					"path", r.socketPath,
					"error", err,
				)
			}
		}
	}

	conn, err := r.listener.Accept()
	if err != nil {
		// FFmpeg didn't connect in time - likely doesn't support unix://
		r.failedToConnect.Store(true)
		if r.logger != nil {
			r.logger.Warn("socket accept timeout",
				"path", r.socketPath,
				"grace", socketConnectGrace,
				"error", err,
			)
		}
		return // CloseChannel called via defer - I1 satisfied
	}

	// Store connection for potential Close() call
	r.connMu.Lock()
	r.conn = conn
	r.connMu.Unlock()

	defer func() {
		r.connMu.Lock()
		if r.conn != nil {
			r.conn.Close()
			r.conn = nil
		}
		r.connMu.Unlock()
	}()

	// Clear deadline for reading (was only for Accept)
	if err := conn.SetDeadline(time.Time{}); err != nil {
		if r.logger != nil {
			r.logger.Debug("failed to clear read deadline",
				"path", r.socketPath,
				"error", err,
			)
		}
	}

	if r.logger != nil {
		r.logger.Debug("socket connection accepted", "path", r.socketPath)
	}

	scanner := bufio.NewScanner(conn)

	// Increase buffer for long lines (default is 64KB)
	const maxLineSize = 64 * 1024
	scanner.Buffer(make([]byte, maxLineSize), maxLineSize)

	for scanner.Scan() {
		line := scanner.Text()
		r.bytesRead.Add(int64(len(line) + 1)) // +1 for newline
		r.linesRead.Add(1)
		r.pipeline.FeedLine(line)
	}

	if err := scanner.Err(); err != nil {
		if r.logger != nil {
			r.logger.Debug("socket scanner error",
				"path", r.socketPath,
				"error", err,
				"bytesRead", r.bytesRead.Load(),
				"linesRead", r.linesRead.Load(),
			)
		}
	}

	// EOF reached, defers run, parser will drain and exit
	if r.logger != nil {
		r.logger.Debug("socket reader finished",
			"path", r.socketPath,
			"bytesRead", r.bytesRead.Load(),
			"linesRead", r.linesRead.Load(),
		)
	}
}

// cleanup removes the socket file and closes the listener.
// Safe to call multiple times (idempotent via cleanedUp flag).
func (r *SocketReader) cleanup() {
	if r.cleanedUp.Swap(true) {
		return // Already cleaned up
	}

	// Close listener
	if r.listener != nil {
		r.listener.Close()
	}

	// Remove socket file
	if err := os.Remove(r.socketPath); err != nil && !os.IsNotExist(err) {
		if r.logger != nil {
			r.logger.Debug("failed to remove socket file",
				"path", r.socketPath,
				"error", err,
			)
		}
	}
}

// Close stops the reader and cleans up resources.
// Safe to call multiple times (idempotent).
func (r *SocketReader) Close() error {
	r.closedOnce.Do(func() {
		// Close connection if open
		r.connMu.Lock()
		if r.conn != nil {
			r.conn.Close()
			r.conn = nil
		}
		r.connMu.Unlock()

		// Close listener (will unblock Accept)
		if r.listener != nil {
			r.listener.Close()
		}

		// Cleanup socket file
		r.cleanup()
	})
	return nil
}

// Stats returns (bytesRead, linesRead, healthy).
// healthy = true if source connected and is working normally.
func (r *SocketReader) Stats() (bytesRead int64, linesRead int64, healthy bool) {
	return r.bytesRead.Load(),
		r.linesRead.Load(),
		!r.failedToConnect.Load() && !r.cleanedUp.Load()
}

// SocketPath returns the path to the socket file.
func (r *SocketReader) SocketPath() string {
	return r.socketPath
}

// Ensure SocketReader implements LineSource interface
var _ LineSource = (*SocketReader)(nil)
