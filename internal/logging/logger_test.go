package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	testCases := []struct {
		input    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"Debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"", slog.LevelInfo},        // Default
		{"invalid", slog.LevelInfo}, // Default for unknown
		{"trace", slog.LevelInfo},   // Unknown level defaults to info
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := parseLevel(tc.input)
			if result != tc.expected {
				t.Errorf("parseLevel(%q) = %v, want %v", tc.input, result, tc.expected)
			}
		})
	}
}

func TestNewLogger_Formats(t *testing.T) {
	testCases := []string{"json", "text", "JSON", "TEXT", "", "invalid"}

	for _, format := range testCases {
		t.Run(format, func(t *testing.T) {
			// Should not panic
			logger := NewLogger(format, "info", false)
			if logger == nil {
				t.Error("NewLogger returned nil")
			}
		})
	}
}

func TestNewLogger_Levels(t *testing.T) {
	testCases := []string{"debug", "info", "warn", "error", "", "invalid"}

	for _, level := range testCases {
		t.Run(level, func(t *testing.T) {
			// Should not panic
			logger := NewLogger("json", level, false)
			if logger == nil {
				t.Error("NewLogger returned nil")
			}
		})
	}
}

func TestNewLogger_VerboseOverride(t *testing.T) {
	// When verbose=true, log level should be debug regardless of level param
	var buf bytes.Buffer

	// Create logger with writer to capture output
	logger := NewLoggerWithWriter(&buf, "text", "error")
	logger.Debug("debug message")

	// Error level logger should not log debug messages
	if strings.Contains(buf.String(), "debug message") {
		t.Error("Error-level logger should not log debug messages")
	}

	// Note: NewLogger's verbose flag can't be tested with NewLoggerWithWriter
	// since verbose only affects NewLogger. Just verify NewLogger doesn't panic.
	verboseLogger := NewLogger("text", "error", true)
	if verboseLogger == nil {
		t.Error("NewLogger with verbose=true returned nil")
	}
}

func TestNewLoggerWithWriter_JSON(t *testing.T) {
	var buf bytes.Buffer

	logger := NewLoggerWithWriter(&buf, "json", "info")
	logger.Info("test message", "key", "value")

	output := buf.String()

	// JSON format should contain JSON syntax
	if !strings.Contains(output, "{") || !strings.Contains(output, "}") {
		t.Errorf("Expected JSON format, got: %s", output)
	}
	if !strings.Contains(output, "test message") {
		t.Errorf("Expected message in output, got: %s", output)
	}
	if !strings.Contains(output, `"key"`) {
		t.Errorf("Expected key in output, got: %s", output)
	}
	if !strings.Contains(output, `"value"`) {
		t.Errorf("Expected value in output, got: %s", output)
	}
}

func TestNewLoggerWithWriter_Text(t *testing.T) {
	var buf bytes.Buffer

	logger := NewLoggerWithWriter(&buf, "text", "info")
	logger.Info("test message", "key", "value")

	output := buf.String()

	// Text format should contain readable log
	if !strings.Contains(output, "test message") {
		t.Errorf("Expected message in output, got: %s", output)
	}
	if !strings.Contains(output, "key=value") {
		t.Errorf("Expected key=value in output, got: %s", output)
	}
}

func TestNewLoggerWithWriter_LevelFiltering(t *testing.T) {
	t.Run("debug_logs_all", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewLoggerWithWriter(&buf, "text", "debug")

		logger.Debug("debug msg")
		logger.Info("info msg")
		logger.Warn("warn msg")
		logger.Error("error msg")

		output := buf.String()
		if !strings.Contains(output, "debug msg") {
			t.Error("Debug level should log debug messages")
		}
		if !strings.Contains(output, "info msg") {
			t.Error("Debug level should log info messages")
		}
		if !strings.Contains(output, "warn msg") {
			t.Error("Debug level should log warn messages")
		}
		if !strings.Contains(output, "error msg") {
			t.Error("Debug level should log error messages")
		}
	})

	t.Run("info_filters_debug", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewLoggerWithWriter(&buf, "text", "info")

		logger.Debug("debug msg")
		logger.Info("info msg")

		output := buf.String()
		if strings.Contains(output, "debug msg") {
			t.Error("Info level should not log debug messages")
		}
		if !strings.Contains(output, "info msg") {
			t.Error("Info level should log info messages")
		}
	})

	t.Run("warn_filters_info", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewLoggerWithWriter(&buf, "text", "warn")

		logger.Info("info msg")
		logger.Warn("warn msg")

		output := buf.String()
		if strings.Contains(output, "info msg") {
			t.Error("Warn level should not log info messages")
		}
		if !strings.Contains(output, "warn msg") {
			t.Error("Warn level should log warn messages")
		}
	})

	t.Run("error_filters_warn", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewLoggerWithWriter(&buf, "text", "error")

		logger.Warn("warn msg")
		logger.Error("error msg")

		output := buf.String()
		if strings.Contains(output, "warn msg") {
			t.Error("Error level should not log warn messages")
		}
		if !strings.Contains(output, "error msg") {
			t.Error("Error level should log error messages")
		}
	})
}

func TestNewLoggerWithWriter_DefaultFormat(t *testing.T) {
	var buf bytes.Buffer

	// Invalid format should default to text
	logger := NewLoggerWithWriter(&buf, "invalid", "info")
	logger.Info("test message")

	output := buf.String()

	// Text format uses key=value, not JSON
	if strings.HasPrefix(strings.TrimSpace(output), "{") {
		t.Error("Default format should be text, not JSON")
	}
}

func TestSetDefault(t *testing.T) {
	// Save original default logger to restore later
	originalDefault := slog.Default()
	defer slog.SetDefault(originalDefault)

	var buf bytes.Buffer
	logger := NewLoggerWithWriter(&buf, "text", "info")

	// Should not panic
	SetDefault(logger)

	// Verify it was set
	slog.Info("from default logger")
	if !strings.Contains(buf.String(), "from default logger") {
		t.Error("SetDefault did not set the default logger")
	}
}

func TestNewLoggerWithWriter_NilWriter(t *testing.T) {
	// This will panic at runtime when trying to log, but creation should work
	// (or we could check that it panics)
	defer func() {
		// We're just checking that NewLoggerWithWriter doesn't panic
		// Logging to nil writer would panic, but that's expected
		_ = recover()
	}()

	logger := NewLoggerWithWriter(nil, "text", "info")
	if logger == nil {
		t.Error("NewLoggerWithWriter returned nil")
	}

	// This would panic, which is expected behavior
	logger.Info("this will panic")
}

func TestNewLoggerWithWriter_EmptyStrings(t *testing.T) {
	var buf bytes.Buffer

	// Empty format and level should use defaults
	logger := NewLoggerWithWriter(&buf, "", "")
	if logger == nil {
		t.Error("NewLoggerWithWriter returned nil")
	}

	logger.Info("test message")
	if !strings.Contains(buf.String(), "test message") {
		t.Error("Logger with empty strings should still work")
	}
}

// StderrHandler tests

func TestNewStderrHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter(&buf, "text", "debug")

	h := NewStderrHandler(1, logger, false)
	if h == nil {
		t.Fatal("NewStderrHandler returned nil")
	}
	if h.clientID != 1 {
		t.Errorf("clientID = %d, want 1", h.clientID)
	}
	if len(h.buffer) != MaxBufferedLines {
		t.Errorf("buffer length = %d, want %d", len(h.buffer), MaxBufferedLines)
	}
}

func TestStderrHandler_HandleLine(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter(&buf, "text", "debug")

	h := NewStderrHandler(1, logger, true)

	h.HandleLine("test line")

	// Line should be in buffer
	lines := h.RecentLines(1)
	if len(lines) != 1 {
		t.Fatalf("Expected 1 line, got %d", len(lines))
	}
	if lines[0] != "test line" {
		t.Errorf("Line = %q, want %q", lines[0], "test line")
	}
}

func TestStderrHandler_HandleLine_Truncation(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter(&buf, "text", "debug")

	h := NewStderrHandler(1, logger, true)

	// Create a line longer than MaxLineLength
	longLine := strings.Repeat("x", MaxLineLength+100)
	h.HandleLine(longLine)

	lines := h.RecentLines(1)
	if len(lines) != 1 {
		t.Fatalf("Expected 1 line, got %d", len(lines))
	}

	// Line should be truncated
	if len(lines[0]) > MaxLineLength+20 { // +20 for "(truncated)"
		t.Errorf("Line should be truncated, got length %d", len(lines[0]))
	}
	if !strings.HasSuffix(lines[0], "...(truncated)") {
		t.Error("Truncated line should end with '...(truncated)'")
	}
}

func TestStderrHandler_CircularBuffer(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter(&buf, "text", "debug")

	h := NewStderrHandler(1, logger, false)

	// Add more lines than buffer size
	for i := 0; i < MaxBufferedLines+50; i++ {
		h.HandleLine(strings.Repeat("x", i))
	}

	// Should only have MaxBufferedLines
	lines := h.RecentLines(MaxBufferedLines + 10)
	if len(lines) > MaxBufferedLines {
		t.Errorf("Got %d lines, max should be %d", len(lines), MaxBufferedLines)
	}
}

func TestStderrHandler_RecentLines(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter(&buf, "text", "debug")

	h := NewStderrHandler(1, logger, false)

	// Add 5 lines
	for i := 0; i < 5; i++ {
		h.HandleLine("line" + string(rune('0'+i)))
	}

	// Request 3 most recent
	lines := h.RecentLines(3)
	if len(lines) != 3 {
		t.Fatalf("Expected 3 lines, got %d", len(lines))
	}

	// Should be last 3 lines
	if lines[0] != "line2" || lines[1] != "line3" || lines[2] != "line4" {
		t.Errorf("Unexpected lines: %v", lines)
	}
}

func TestStderrHandler_RecentLines_Empty(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter(&buf, "text", "debug")

	h := NewStderrHandler(1, logger, false)

	lines := h.RecentLines(10)
	if len(lines) != 0 {
		t.Errorf("Expected 0 lines for empty buffer, got %d", len(lines))
	}
}

func TestStderrHandler_ClassifyLine(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter(&buf, "text", "debug")

	h := NewStderrHandler(1, logger, true)

	testCases := []struct {
		line     string
		expected slog.Level
	}{
		// Error patterns - should be Warn
		{"[error] something failed", slog.LevelWarn},
		{"Error: connection failed", slog.LevelWarn},
		{"Connection refused", slog.LevelWarn},
		{"Server returned 500", slog.LevelWarn},

		// Warning patterns
		{"[warning] something", slog.LevelWarn},
		{"Reconnecting to server", slog.LevelWarn},
		{"skip frame", slog.LevelWarn},

		// Progress patterns - should be Debug
		{"frame= 1234", slog.LevelDebug},
		{"speed=1.5x", slog.LevelDebug},
		{"time=00:01:23", slog.LevelDebug},

		// Default - should be Debug
		{"some random output", slog.LevelDebug},
		{"Input #0", slog.LevelDebug},
	}

	for _, tc := range testCases {
		t.Run(tc.line[:min(20, len(tc.line))], func(t *testing.T) {
			level := h.classifyLine(tc.line)
			if level != tc.expected {
				t.Errorf("classifyLine(%q) = %v, want %v", tc.line, level, tc.expected)
			}
		})
	}
}

func TestStderrHandler_CountErrors(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter(&buf, "text", "debug")

	h := NewStderrHandler(1, logger, false)

	// Add lines with error patterns
	h.HandleLine("Connection refused")
	h.HandleLine("Connection refused again")
	h.HandleLine("Server returned 404")
	h.HandleLine("normal line")
	h.HandleLine("timeout occurred")

	counts := h.CountErrors()

	if counts["Connection refused"] != 2 {
		t.Errorf("Connection refused count = %d, want 2", counts["Connection refused"])
	}
	if counts["404"] != 1 {
		t.Errorf("404 count = %d, want 1", counts["404"])
	}
	if counts["timeout"] != 1 {
		t.Errorf("timeout count = %d, want 1", counts["timeout"])
	}
}

func TestStderrHandler_CountErrors_Empty(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter(&buf, "text", "debug")

	h := NewStderrHandler(1, logger, false)

	counts := h.CountErrors()
	if len(counts) != 0 {
		t.Errorf("Expected empty counts, got %v", counts)
	}
}

func TestStderrHandler_VerboseLogging(t *testing.T) {
	t.Run("verbose_true", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewLoggerWithWriter(&buf, "text", "debug")
		h := NewStderrHandler(1, logger, true)

		h.HandleLine("debug line")

		if !strings.Contains(buf.String(), "debug line") {
			t.Error("Verbose mode should log debug lines")
		}
	})

	t.Run("verbose_false", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewLoggerWithWriter(&buf, "text", "debug")
		h := NewStderrHandler(1, logger, false)

		h.HandleLine("debug line")

		if strings.Contains(buf.String(), "debug line") {
			t.Error("Non-verbose mode should not log debug lines")
		}
	})

	t.Run("verbose_false_logs_errors", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewLoggerWithWriter(&buf, "text", "debug")
		h := NewStderrHandler(1, logger, false)

		h.HandleLine("[error] something failed")

		if !strings.Contains(buf.String(), "[error] something failed") {
			t.Error("Non-verbose mode should still log errors")
		}
	})
}

func TestStderrHandler_HandleReader(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter(&buf, "text", "debug")
	h := NewStderrHandler(1, logger, true)

	// Create a reader with multiple lines
	input := "line1\nline2\nline3\n"
	reader := strings.NewReader(input)

	h.HandleReader(reader)

	lines := h.RecentLines(3)
	if len(lines) != 3 {
		t.Fatalf("Expected 3 lines, got %d", len(lines))
	}
}

func TestStderrHandler_HandleReader_EmptyInput(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter(&buf, "text", "debug")
	h := NewStderrHandler(1, logger, true)

	reader := strings.NewReader("")
	h.HandleReader(reader)

	lines := h.RecentLines(10)
	if len(lines) != 0 {
		t.Errorf("Expected 0 lines for empty input, got %d", len(lines))
	}
}

func TestStderrHandler_Concurrent(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter(&buf, "text", "debug")
	h := NewStderrHandler(1, logger, false)

	// Concurrent access should not panic
	done := make(chan bool)

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			h.HandleLine("concurrent line")
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			_ = h.RecentLines(10)
			_ = h.CountErrors()
		}
		done <- true
	}()

	<-done
	<-done
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
