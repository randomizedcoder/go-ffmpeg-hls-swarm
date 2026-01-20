package logging

import (
	"bufio"
	"io"
	"log/slog"
	"strings"
	"sync"
)

const (
	// MaxLineLength is the maximum length of a single log line before truncation.
	MaxLineLength = 4096

	// MaxBufferedLines is the maximum number of lines to buffer per client.
	MaxBufferedLines = 100
)

// StderrHandler handles stderr output from FFmpeg processes.
// It buffers recent lines for the exit summary and logs them.
type StderrHandler struct {
	clientID int
	logger   *slog.Logger
	verbose  bool

	// Circular buffer for recent lines
	buffer []string
	bufIdx int
	mu     sync.Mutex
}

// NewStderrHandler creates a new stderr handler for a client.
func NewStderrHandler(clientID int, logger *slog.Logger, verbose bool) *StderrHandler {
	return &StderrHandler{
		clientID: clientID,
		logger:   logger,
		verbose:  verbose,
		buffer:   make([]string, MaxBufferedLines),
	}
}

// HandleReader reads from an io.Reader and processes each line.
// This should be run in a goroutine.
func (h *StderrHandler) HandleReader(r io.Reader) {
	scanner := bufio.NewScanner(r)
	// Use a larger buffer for long FFmpeg output lines
	buf := make([]byte, MaxLineLength)
	scanner.Buffer(buf, MaxLineLength)

	for scanner.Scan() {
		line := scanner.Text()
		h.HandleLine(line)
	}
}

// HandleLine processes a single line of stderr output.
func (h *StderrHandler) HandleLine(line string) {
	// Truncate if too long
	if len(line) > MaxLineLength {
		line = line[:MaxLineLength] + "...(truncated)"
	}

	// Store in circular buffer
	h.mu.Lock()
	h.buffer[h.bufIdx] = line
	h.bufIdx = (h.bufIdx + 1) % MaxBufferedLines
	h.mu.Unlock()

	// Log based on content and verbosity
	h.logLine(line)
}

// logLine logs the line at appropriate level based on content.
func (h *StderrHandler) logLine(line string) {
	// Determine log level based on content
	level := h.classifyLine(line)

	// In non-verbose mode, only log warnings and errors
	if !h.verbose && level == slog.LevelDebug {
		return
	}

	h.logger.Log(nil, level, "ffmpeg_stderr",
		"client_id", h.clientID,
		"line", line,
	)
}

// classifyLine determines the log level for a line based on content.
func (h *StderrHandler) classifyLine(line string) slog.Level {
	lower := strings.ToLower(line)

	// Error patterns
	if strings.Contains(lower, "[error]") ||
		strings.Contains(lower, "error") && strings.Contains(lower, "failed") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "server returned") {
		return slog.LevelWarn
	}

	// Warning patterns
	if strings.Contains(lower, "[warning]") ||
		strings.Contains(lower, "skip") ||
		strings.Contains(lower, "reconnect") {
		return slog.LevelWarn
	}

	// Info patterns (progress, stats)
	if strings.Contains(lower, "frame=") ||
		strings.Contains(lower, "speed=") ||
		strings.Contains(lower, "time=") {
		return slog.LevelDebug
	}

	// Default to debug
	return slog.LevelDebug
}

// RecentLines returns the most recent lines from the buffer.
func (h *StderrHandler) RecentLines(n int) []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	if n > MaxBufferedLines {
		n = MaxBufferedLines
	}

	lines := make([]string, 0, n)

	// Read from circular buffer in order
	for i := 0; i < n; i++ {
		idx := (h.bufIdx - n + i + MaxBufferedLines) % MaxBufferedLines
		if h.buffer[idx] != "" {
			lines = append(lines, h.buffer[idx])
		}
	}

	return lines
}

// ErrorPatterns are common error patterns to extract for the exit summary.
var ErrorPatterns = []string{
	"Connection refused",
	"Server returned",
	"[hls] Skip",
	"Reconnecting",
	"timeout",
	"403",
	"404",
	"500",
	"503",
}

// CountErrors counts occurrences of error patterns in the buffer.
func (h *StderrHandler) CountErrors() map[string]int {
	h.mu.Lock()
	defer h.mu.Unlock()

	counts := make(map[string]int)

	for _, line := range h.buffer {
		if line == "" {
			continue
		}
		for _, pattern := range ErrorPatterns {
			if strings.Contains(line, pattern) {
				counts[pattern]++
			}
		}
	}

	return counts
}
