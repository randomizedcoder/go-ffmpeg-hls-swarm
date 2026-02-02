// Package logging provides structured logging for go-ffmpeg-hls-swarm.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// NewLogger creates a new structured logger with the specified format and level.
// Format should be "json" or "text".
// Level should be "debug", "info", "warn", or "error".
func NewLogger(format, level string, verbose bool) *slog.Logger {
	var handler slog.Handler

	// Determine log level
	logLevel := parseLevel(level)
	if verbose {
		logLevel = slog.LevelDebug
	}

	opts := &slog.HandlerOptions{
		Level: logLevel,
		// Add source location for debug level
		AddSource: logLevel == slog.LevelDebug,
	}

	// Create handler based on format
	switch strings.ToLower(format) {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, opts)
	case "text":
		handler = slog.NewTextHandler(os.Stderr, opts)
	default:
		// Default to JSON for structured logging
		handler = slog.NewJSONHandler(os.Stderr, opts)
	}

	return slog.New(handler)
}

// NewLoggerWithWriter creates a logger that writes to a custom writer.
// Useful for testing.
func NewLoggerWithWriter(w io.Writer, format, level string) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: parseLevel(level),
	}

	var handler slog.Handler
	switch strings.ToLower(format) {
	case "json":
		handler = slog.NewJSONHandler(w, opts)
	default:
		handler = slog.NewTextHandler(w, opts)
	}

	return slog.New(handler)
}

// parseLevel converts a string level to slog.Level.
func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// SetDefault sets the default logger for the slog package.
func SetDefault(logger *slog.Logger) {
	slog.SetDefault(logger)
}
