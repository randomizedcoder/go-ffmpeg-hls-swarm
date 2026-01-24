package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/parser"
)

// =============================================================================
// Mock ProcessBuilder for testing
// =============================================================================

// mockBuilder implements ProcessBuilder for testing.
type mockBuilder struct {
	name           string
	buildFn        func(ctx context.Context, clientID int) (*exec.Cmd, error)
	buildError     error
	progressSocket string // Captured for testing
}

func (m *mockBuilder) BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error) {
	if m.buildError != nil {
		return nil, m.buildError
	}
	if m.buildFn != nil {
		return m.buildFn(ctx, clientID)
	}
	// Default: simple echo command that exits quickly
	return exec.CommandContext(ctx, "echo", "hello"), nil
}

func (m *mockBuilder) Name() string {
	if m.name != "" {
		return m.name
	}
	return "mock"
}

func (m *mockBuilder) SetProgressSocket(path string) {
	m.progressSocket = path
}

// newEchoBuilder creates a builder that runs echo with given output.
func newEchoBuilder(output string) *mockBuilder {
	return &mockBuilder{
		buildFn: func(ctx context.Context, clientID int) (*exec.Cmd, error) {
			return exec.CommandContext(ctx, "echo", output), nil
		},
	}
}

// newSleepBuilder creates a builder that sleeps for the given duration.
func newSleepBuilder(duration time.Duration) *mockBuilder {
	return &mockBuilder{
		buildFn: func(ctx context.Context, clientID int) (*exec.Cmd, error) {
			return exec.CommandContext(ctx, "sleep", fmt.Sprintf("%.3f", duration.Seconds())), nil
		},
	}
}

// newFailingBuilder creates a builder that always fails to build.
func newFailingBuilder(err error) *mockBuilder {
	return &mockBuilder{buildError: err}
}

// newExitCodeBuilder creates a builder that exits with the given code.
func newExitCodeBuilder(code int) *mockBuilder {
	return &mockBuilder{
		buildFn: func(ctx context.Context, clientID int) (*exec.Cmd, error) {
			// Use bash to exit with specific code
			return exec.CommandContext(ctx, "bash", "-c", fmt.Sprintf("exit %d", code)), nil
		},
	}
}

// newStdoutBuilder creates a builder that writes to stdout.
func newStdoutBuilder(lines []string) *mockBuilder {
	return &mockBuilder{
		buildFn: func(ctx context.Context, clientID int) (*exec.Cmd, error) {
			// Use printf to write lines to stdout
			output := strings.Join(lines, "\n")
			return exec.CommandContext(ctx, "printf", "%s\n", output), nil
		},
	}
}

// newStderrBuilder creates a builder that writes to stderr.
func newStderrBuilder(lines []string) *mockBuilder {
	return &mockBuilder{
		buildFn: func(ctx context.Context, clientID int) (*exec.Cmd, error) {
			// Use bash to write to stderr
			output := strings.Join(lines, "\n")
			return exec.CommandContext(ctx, "bash", "-c", fmt.Sprintf("echo '%s' >&2", output)), nil
		},
	}
}

// =============================================================================
// Mock LineParser for testing
// =============================================================================

// mockParser implements parser.LineParser for testing.
type mockParser struct {
	mu         sync.Mutex
	lines      []string
	parseDelay time.Duration
	parseFn    func(line string)
}

func (m *mockParser) ParseLine(line string) {
	if m.parseDelay > 0 {
		time.Sleep(m.parseDelay)
	}
	m.mu.Lock()
	m.lines = append(m.lines, line)
	m.mu.Unlock()
	if m.parseFn != nil {
		m.parseFn(line)
	}
}

func (m *mockParser) Lines() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.lines))
	copy(result, m.lines)
	return result
}

func (m *mockParser) LineCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.lines)
}

// =============================================================================
// Test Helpers
// =============================================================================

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestBackoff() *Backoff {
	return NewBackoff(0, 12345, BackoffConfig{
		Initial:    10 * time.Millisecond,
		Max:        100 * time.Millisecond,
		Multiplier: 1.5,
		JitterPct:  0,
	})
}

// =============================================================================
// Table-Driven Tests: New() Configuration
// =============================================================================

func TestNew_ConfigurationDefaults(t *testing.T) {
	tests := []struct {
		name               string
		config             Config
		wantStatsEnabled   bool
		wantBufferSize     int
		wantDropThreshold  float64
		wantProgressParser bool // true if not NoopParser
		wantStderrParser   bool
	}{
		{
			name: "all defaults",
			config: Config{
				ClientID: 1,
				Builder:  &mockBuilder{},
				Backoff:  newTestBackoff(),
				Logger:   newTestLogger(),
			},
			wantStatsEnabled:   false,
			wantBufferSize:     1000,
			wantDropThreshold:  0.01,
			wantProgressParser: false, // NoopParser
			wantStderrParser:   false,
		},
		{
			name: "stats enabled with defaults",
			config: Config{
				ClientID:     2,
				Builder:      &mockBuilder{},
				Backoff:      newTestBackoff(),
				Logger:       newTestLogger(),
				StatsEnabled: true,
			},
			wantStatsEnabled:   true,
			wantBufferSize:     1000,
			wantDropThreshold:  0.01,
			wantProgressParser: false,
			wantStderrParser:   false,
		},
		{
			name: "custom buffer size",
			config: Config{
				ClientID:        3,
				Builder:         &mockBuilder{},
				Backoff:         newTestBackoff(),
				Logger:          newTestLogger(),
				StatsEnabled:    true,
				StatsBufferSize: 5000,
			},
			wantStatsEnabled:   true,
			wantBufferSize:     5000,
			wantDropThreshold:  0.01,
			wantProgressParser: false,
			wantStderrParser:   false,
		},
		{
			name: "custom drop threshold",
			config: Config{
				ClientID:           4,
				Builder:            &mockBuilder{},
				Backoff:            newTestBackoff(),
				Logger:             newTestLogger(),
				StatsEnabled:       true,
				StatsDropThreshold: 0.05,
			},
			wantStatsEnabled:   true,
			wantBufferSize:     1000,
			wantDropThreshold:  0.05,
			wantProgressParser: false,
			wantStderrParser:   false,
		},
		{
			name: "with custom parsers",
			config: Config{
				ClientID:       5,
				Builder:        &mockBuilder{},
				Backoff:        newTestBackoff(),
				Logger:         newTestLogger(),
				StatsEnabled:   true,
				ProgressParser: &mockParser{},
				StderrParser:   &mockParser{},
			},
			wantStatsEnabled:   true,
			wantBufferSize:     1000,
			wantDropThreshold:  0.01,
			wantProgressParser: true,
			wantStderrParser:   true,
		},
		{
			name: "zero buffer size gets default",
			config: Config{
				ClientID:        6,
				Builder:         &mockBuilder{},
				Backoff:         newTestBackoff(),
				Logger:          newTestLogger(),
				StatsBufferSize: 0,
			},
			wantStatsEnabled:  false,
			wantBufferSize:    1000,
			wantDropThreshold: 0.01,
		},
		{
			name: "negative buffer size gets default",
			config: Config{
				ClientID:        7,
				Builder:         &mockBuilder{},
				Backoff:         newTestBackoff(),
				Logger:          newTestLogger(),
				StatsBufferSize: -100,
			},
			wantStatsEnabled:  false,
			wantBufferSize:    1000,
			wantDropThreshold: 0.01,
		},
		{
			name: "zero threshold gets default",
			config: Config{
				ClientID:           8,
				Builder:            &mockBuilder{},
				Backoff:            newTestBackoff(),
				Logger:             newTestLogger(),
				StatsDropThreshold: 0,
			},
			wantStatsEnabled:  false,
			wantBufferSize:    1000,
			wantDropThreshold: 0.01,
		},
		{
			name: "negative threshold gets default",
			config: Config{
				ClientID:           9,
				Builder:            &mockBuilder{},
				Backoff:            newTestBackoff(),
				Logger:             newTestLogger(),
				StatsDropThreshold: -0.5,
			},
			wantStatsEnabled:  false,
			wantBufferSize:    1000,
			wantDropThreshold: 0.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sup := New(tt.config)

			if sup.statsEnabled != tt.wantStatsEnabled {
				t.Errorf("statsEnabled = %v, want %v", sup.statsEnabled, tt.wantStatsEnabled)
			}
			if sup.statsBufferSize != tt.wantBufferSize {
				t.Errorf("statsBufferSize = %d, want %d", sup.statsBufferSize, tt.wantBufferSize)
			}
			if sup.statsDropThreshold != tt.wantDropThreshold {
				t.Errorf("statsDropThreshold = %v, want %v", sup.statsDropThreshold, tt.wantDropThreshold)
			}

			// Check parser types
			_, isNoop := sup.progressParser.(parser.NoopParser)
			if tt.wantProgressParser && isNoop {
				t.Error("progressParser should not be NoopParser")
			}
			if !tt.wantProgressParser && !isNoop {
				t.Error("progressParser should be NoopParser")
			}

			_, isNoop = sup.stderrParser.(parser.NoopParser)
			if tt.wantStderrParser && isNoop {
				t.Error("stderrParser should not be NoopParser")
			}
			if !tt.wantStderrParser && !isNoop {
				t.Error("stderrParser should be NoopParser")
			}
		})
	}
}

// =============================================================================
// Table-Driven Tests: State Management
// =============================================================================

func TestState_String(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StateCreated, "created"},
		{StateStarting, "starting"},
		{StateRunning, "running"},
		{StateBackoff, "backoff"},
		{StateStopped, "stopped"},
		{State(99), "unknown"},
		{State(-1), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.state.String(); got != tt.want {
				t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

func TestState_IsActive(t *testing.T) {
	tests := []struct {
		state State
		want  bool
	}{
		{StateCreated, false},
		{StateStarting, true},
		{StateRunning, true},
		{StateBackoff, true},
		{StateStopped, false},
		{State(99), false},
	}

	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			if got := tt.state.IsActive(); got != tt.want {
				t.Errorf("State(%d).IsActive() = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}

func TestState_IsTerminal(t *testing.T) {
	tests := []struct {
		state State
		want  bool
	}{
		{StateCreated, false},
		{StateStarting, false},
		{StateRunning, false},
		{StateBackoff, false},
		{StateStopped, true},
		{State(99), false},
	}

	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			if got := tt.state.IsTerminal(); got != tt.want {
				t.Errorf("State(%d).IsTerminal() = %v, want %v", tt.state, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Table-Driven Tests: Exit Code Extraction
// =============================================================================

func TestExtractExitCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
	}{
		{
			name:     "nil error",
			err:      nil,
			wantCode: 0,
		},
		{
			name:     "generic error",
			err:      errors.New("some error"),
			wantCode: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractExitCode(tt.err); got != tt.wantCode {
				t.Errorf("extractExitCode(%v) = %d, want %d", tt.err, got, tt.wantCode)
			}
		})
	}
}

// =============================================================================
// Table-Driven Tests: ShouldReset
// =============================================================================

func TestShouldReset(t *testing.T) {
	tests := []struct {
		name     string
		uptime   time.Duration
		exitCode int
		want     bool
	}{
		{
			name:     "short uptime, non-zero exit",
			uptime:   5 * time.Second,
			exitCode: 1,
			want:     false,
		},
		{
			name:     "long uptime, non-zero exit",
			uptime:   35 * time.Second,
			exitCode: 1,
			want:     true,
		},
		{
			name:     "exactly threshold uptime",
			uptime:   BackoffResetThreshold,
			exitCode: 1,
			want:     true,
		},
		{
			name:     "just under threshold",
			uptime:   BackoffResetThreshold - time.Millisecond,
			exitCode: 1,
			want:     false,
		},
		{
			name:     "clean exit, short uptime",
			uptime:   1 * time.Second,
			exitCode: 0,
			want:     true,
		},
		{
			name:     "clean exit, long uptime",
			uptime:   60 * time.Second,
			exitCode: 0,
			want:     true,
		},
		{
			name:     "zero uptime, error exit",
			uptime:   0,
			exitCode: 137,
			want:     false,
		},
		{
			name:     "SIGTERM exit (143), short uptime",
			uptime:   5 * time.Second,
			exitCode: 143,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldReset(tt.uptime, tt.exitCode); got != tt.want {
				t.Errorf("ShouldReset(%v, %d) = %v, want %v", tt.uptime, tt.exitCode, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Tests: Supervisor Lifecycle
// =============================================================================

func TestSupervisor_InitialState(t *testing.T) {
	sup := New(Config{
		ClientID: 1,
		Builder:  &mockBuilder{},
		Backoff:  newTestBackoff(),
		Logger:   newTestLogger(),
	})

	if sup.State() != StateCreated {
		t.Errorf("initial state = %v, want StateCreated", sup.State())
	}
	if sup.ClientID() != 1 {
		t.Errorf("ClientID() = %d, want 1", sup.ClientID())
	}
	if sup.Restarts() != 0 {
		t.Errorf("Restarts() = %d, want 0", sup.Restarts())
	}
	if sup.Uptime() != 0 {
		t.Errorf("Uptime() = %v, want 0", sup.Uptime())
	}
}

func TestSupervisor_RunOnce_Success(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var stateChanges []State
	var mu sync.Mutex

	sup := New(Config{
		ClientID:    1,
		Builder:     newEchoBuilder("test output"),
		Backoff:     newTestBackoff(),
		Logger:      newTestLogger(),
		MaxRestarts: 1, // Stop after first restart attempt
		Callbacks: Callbacks{
			OnStateChange: func(clientID int, oldState, newState State) {
				mu.Lock()
				stateChanges = append(stateChanges, newState)
				mu.Unlock()
			},
		},
	})

	// Run will exit after max restarts
	_ = sup.Run(ctx)

	mu.Lock()
	defer mu.Unlock()

	// Should have gone through: starting -> running -> backoff -> stopped
	if len(stateChanges) < 3 {
		t.Errorf("expected at least 3 state changes, got %d: %v", len(stateChanges), stateChanges)
	}
}

func TestSupervisor_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	sup := New(Config{
		ClientID: 1,
		Builder:  newSleepBuilder(10 * time.Second), // Long sleep
		Backoff:  newTestBackoff(),
		Logger:   newTestLogger(),
	})

	done := make(chan error)
	go func() {
		done <- sup.Run(ctx)
	}()

	// Wait for process to start
	time.Sleep(100 * time.Millisecond)

	// Cancel context
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("supervisor did not stop within timeout")
	}

	if sup.State() != StateStopped {
		t.Errorf("final state = %v, want StateStopped", sup.State())
	}
}

func TestSupervisor_MaxRestarts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sup := New(Config{
		ClientID:    1,
		Builder:     newExitCodeBuilder(1), // Always fail
		Backoff:     newTestBackoff(),
		Logger:      newTestLogger(),
		MaxRestarts: 3,
	})

	err := sup.Run(ctx)

	if err == nil || !strings.Contains(err.Error(), "max restarts") {
		t.Errorf("expected 'max restarts' error, got %v", err)
	}
	if sup.Restarts() != 3 {
		t.Errorf("Restarts() = %d, want 3", sup.Restarts())
	}
	if sup.State() != StateStopped {
		t.Errorf("final state = %v, want StateStopped", sup.State())
	}
}

func TestSupervisor_BuildError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	buildErr := errors.New("build failed")
	sup := New(Config{
		ClientID:    1,
		Builder:     newFailingBuilder(buildErr),
		Backoff:     newTestBackoff(),
		Logger:      newTestLogger(),
		MaxRestarts: 1,
	})

	err := sup.Run(ctx)

	// Should have hit max restarts after build failures
	if err == nil || !strings.Contains(err.Error(), "max restarts") {
		t.Errorf("expected 'max restarts' error, got %v", err)
	}
}

// =============================================================================
// Tests: Stats Collection
// =============================================================================

func TestSupervisor_StatsEnabled_CreatesPipes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	progressParser := &mockParser{}
	stderrParser := &mockParser{}

	sup := New(Config{
		ClientID:       1,
		Builder:        newEchoBuilder("progress line"),
		Backoff:        newTestBackoff(),
		Logger:         newTestLogger(),
		MaxRestarts:    1,
		StatsEnabled:   true,
		ProgressParser: progressParser,
		StderrParser:   stderrParser,
	})

	_ = sup.Run(ctx)

	// Progress parser should have received the echo output
	if progressParser.LineCount() == 0 {
		t.Error("progressParser received no lines")
	}

	// Verify StatsEnabled() accessor
	if !sup.StatsEnabled() {
		t.Error("StatsEnabled() should return true")
	}
}

func TestSupervisor_StatsDisabled_NoPipes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	progressParser := &mockParser{}
	stderrParser := &mockParser{}

	sup := New(Config{
		ClientID:       1,
		Builder:        newEchoBuilder("test output"),
		Backoff:        newTestBackoff(),
		Logger:         newTestLogger(),
		MaxRestarts:    1,
		StatsEnabled:   false, // Disabled
		ProgressParser: progressParser,
		StderrParser:   stderrParser,
	})

	_ = sup.Run(ctx)

	// Parsers should NOT receive any lines when stats disabled
	if progressParser.LineCount() != 0 {
		t.Errorf("progressParser received %d lines, want 0 (stats disabled)", progressParser.LineCount())
	}
	if stderrParser.LineCount() != 0 {
		t.Errorf("stderrParser received %d lines, want 0 (stats disabled)", stderrParser.LineCount())
	}

	// Verify StatsEnabled() accessor
	if sup.StatsEnabled() {
		t.Error("StatsEnabled() should return false")
	}
}

func TestSupervisor_PipelineStats(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sup := New(Config{
		ClientID:       1,
		Builder:        newEchoBuilder("line1\nline2\nline3"),
		Backoff:        newTestBackoff(),
		Logger:         newTestLogger(),
		MaxRestarts:    1,
		StatsEnabled:   true,
		ProgressParser: &mockParser{},
	})

	_ = sup.Run(ctx)

	progressRead, progressDropped, stderrRead, stderrDropped := sup.PipelineStats()

	// Should have read some lines
	if progressRead == 0 {
		t.Error("progressRead should be > 0")
	}
	// Should not have dropped any (small test)
	if progressDropped != 0 {
		t.Errorf("progressDropped = %d, want 0", progressDropped)
	}

	// Stderr should be 0 (echo doesn't write to stderr)
	if stderrRead != 0 {
		t.Errorf("stderrRead = %d, want 0", stderrRead)
	}
	if stderrDropped != 0 {
		t.Errorf("stderrDropped = %d, want 0", stderrDropped)
	}
}

func TestSupervisor_IsMetricsDegraded_NotDegraded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sup := New(Config{
		ClientID:       1,
		Builder:        newEchoBuilder("test"),
		Backoff:        newTestBackoff(),
		Logger:         newTestLogger(),
		MaxRestarts:    1,
		StatsEnabled:   true,
		ProgressParser: &mockParser{},
	})

	_ = sup.Run(ctx)

	// Small test should not be degraded
	if sup.IsMetricsDegraded() {
		t.Error("IsMetricsDegraded() should be false for small test")
	}
}

// =============================================================================
// Tests: Callbacks
// =============================================================================

func TestSupervisor_Callbacks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var (
		stateChanges []struct{ old, new State }
		startCalls   []struct{ clientID, pid int }
		exitCalls    []struct {
			clientID int
			exitCode int
			uptime   time.Duration
		}
		restartCalls []struct {
			clientID int
			attempt  int
			delay    time.Duration
		}
		mu sync.Mutex
	)

	sup := New(Config{
		ClientID:    42,
		Builder:     newEchoBuilder("test"),
		Backoff:     newTestBackoff(),
		Logger:      newTestLogger(),
		MaxRestarts: 2,
		Callbacks: Callbacks{
			OnStateChange: func(clientID int, oldState, newState State) {
				mu.Lock()
				stateChanges = append(stateChanges, struct{ old, new State }{oldState, newState})
				mu.Unlock()
			},
			OnStart: func(clientID int, pid int) {
				mu.Lock()
				startCalls = append(startCalls, struct{ clientID, pid int }{clientID, pid})
				mu.Unlock()
			},
			OnExit: func(clientID int, exitCode int, uptime time.Duration) {
				mu.Lock()
				exitCalls = append(exitCalls, struct {
					clientID int
					exitCode int
					uptime   time.Duration
				}{clientID, exitCode, uptime})
				mu.Unlock()
			},
			OnRestart: func(clientID int, attempt int, delay time.Duration) {
				mu.Lock()
				restartCalls = append(restartCalls, struct {
					clientID int
					attempt  int
					delay    time.Duration
				}{clientID, attempt, delay})
				mu.Unlock()
			},
		},
	})

	_ = sup.Run(ctx)

	mu.Lock()
	defer mu.Unlock()

	// Verify callbacks were called
	if len(stateChanges) == 0 {
		t.Error("OnStateChange was not called")
	}
	if len(startCalls) == 0 {
		t.Error("OnStart was not called")
	}
	for _, call := range startCalls {
		if call.clientID != 42 {
			t.Errorf("OnStart clientID = %d, want 42", call.clientID)
		}
		if call.pid <= 0 {
			t.Errorf("OnStart pid = %d, want > 0", call.pid)
		}
	}
	if len(exitCalls) == 0 {
		t.Error("OnExit was not called")
	}
	for _, call := range exitCalls {
		if call.clientID != 42 {
			t.Errorf("OnExit clientID = %d, want 42", call.clientID)
		}
	}
	if len(restartCalls) == 0 {
		t.Error("OnRestart was not called")
	}
}

// =============================================================================
// Tests: SetParsers
// =============================================================================

func TestSupervisor_SetParsers(t *testing.T) {
	sup := New(Config{
		ClientID: 1,
		Builder:  &mockBuilder{},
		Backoff:  newTestBackoff(),
		Logger:   newTestLogger(),
	})

	// Initially NoopParser
	_, isNoop := sup.progressParser.(parser.NoopParser)
	if !isNoop {
		t.Error("initial progressParser should be NoopParser")
	}

	// Set custom parsers
	progress := &mockParser{}
	stderr := &mockParser{}
	sup.SetParsers(progress, stderr)

	if sup.progressParser != progress {
		t.Error("progressParser not set correctly")
	}
	if sup.stderrParser != stderr {
		t.Error("stderrParser not set correctly")
	}

	// Set nil should not change
	sup.SetParsers(nil, nil)
	if sup.progressParser != progress {
		t.Error("nil should not change progressParser")
	}
	if sup.stderrParser != stderr {
		t.Error("nil should not change stderrParser")
	}
}

// =============================================================================
// Tests: Drain Timeout
// =============================================================================

func TestSupervisor_DrainTimeout_SlowParser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Create a parser that's very slow
	slowParser := &mockParser{
		parseDelay: 10 * time.Second, // Much longer than drain timeout
	}

	sup := New(Config{
		ClientID:       1,
		Builder:        newEchoBuilder("line1\nline2"),
		Backoff:        newTestBackoff(),
		Logger:         newTestLogger(),
		MaxRestarts:    1,
		StatsEnabled:   true,
		ProgressParser: slowParser,
	})

	start := time.Now()
	_ = sup.Run(ctx)
	elapsed := time.Since(start)

	// Should complete within drain timeout (5s) + some buffer
	// Not wait for all slow parsing to complete
	if elapsed > 8*time.Second {
		t.Errorf("elapsed = %v, expected < 8s (drain timeout should kick in)", elapsed)
	}
}

// =============================================================================
// Tests: Concurrent Access
// =============================================================================

func TestSupervisor_ConcurrentStateAccess(t *testing.T) {
	sup := New(Config{
		ClientID: 1,
		Builder:  &mockBuilder{},
		Backoff:  newTestBackoff(),
		Logger:   newTestLogger(),
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = sup.State()
			_ = sup.ClientID()
			_ = sup.Restarts()
			_ = sup.Uptime()
			_ = sup.StatsEnabled()
			_ = sup.IsMetricsDegraded()
			_, _, _, _ = sup.PipelineStats()
		}()
	}
	wg.Wait()
}

// =============================================================================
// Tests: Edge Cases and Negative Tests
// =============================================================================

func TestSupervisor_ZeroMaxRestarts_Unlimited(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var restartCount atomic.Int32

	sup := New(Config{
		ClientID:    1,
		Builder:     newExitCodeBuilder(1), // Always fail
		Backoff:     newTestBackoff(),
		Logger:      newTestLogger(),
		MaxRestarts: 0, // Unlimited
		Callbacks: Callbacks{
			OnRestart: func(clientID int, attempt int, delay time.Duration) {
				restartCount.Add(1)
			},
		},
	})

	// Run until context times out
	_ = sup.Run(ctx)

	// Should have restarted multiple times
	if restartCount.Load() < 2 {
		t.Errorf("expected multiple restarts with unlimited, got %d", restartCount.Load())
	}
}

func TestSupervisor_UptimeWhileRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sup := New(Config{
		ClientID: 1,
		Builder:  newSleepBuilder(10 * time.Second),
		Backoff:  newTestBackoff(),
		Logger:   newTestLogger(),
	})

	go sup.Run(ctx)

	// Wait for process to start
	time.Sleep(200 * time.Millisecond)

	// Should have non-zero uptime while running
	uptime := sup.Uptime()
	if uptime < 100*time.Millisecond {
		t.Errorf("Uptime() = %v while running, expected > 100ms", uptime)
	}

	cancel()
	time.Sleep(100 * time.Millisecond)

	// After stopping, uptime should be 0
	if sup.Uptime() != 0 {
		t.Errorf("Uptime() = %v after stop, expected 0", sup.Uptime())
	}
}

func TestSupervisor_PipelineStats_BeforeRun(t *testing.T) {
	sup := New(Config{
		ClientID:     1,
		Builder:      &mockBuilder{},
		Backoff:      newTestBackoff(),
		Logger:       newTestLogger(),
		StatsEnabled: true,
	})

	// Before Run(), pipelines don't exist
	progressRead, progressDropped, stderrRead, stderrDropped := sup.PipelineStats()

	if progressRead != 0 || progressDropped != 0 || stderrRead != 0 || stderrDropped != 0 {
		t.Error("PipelineStats should return zeros before Run()")
	}
}

func TestSupervisor_IsMetricsDegraded_BeforeRun(t *testing.T) {
	sup := New(Config{
		ClientID:     1,
		Builder:      &mockBuilder{},
		Backoff:      newTestBackoff(),
		Logger:       newTestLogger(),
		StatsEnabled: true,
	})

	// Before Run(), should not be degraded
	if sup.IsMetricsDegraded() {
		t.Error("IsMetricsDegraded() should be false before Run()")
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkSupervisor_StateAccess(b *testing.B) {
	sup := New(Config{
		ClientID: 1,
		Builder:  &mockBuilder{},
		Backoff:  newTestBackoff(),
		Logger:   newTestLogger(),
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sup.State()
	}
}

func BenchmarkSupervisor_New(b *testing.B) {
	builder := &mockBuilder{}
	backoff := newTestBackoff()
	logger := newTestLogger()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = New(Config{
			ClientID: i,
			Builder:  builder,
			Backoff:  backoff,
			Logger:   logger,
		})
	}
}
