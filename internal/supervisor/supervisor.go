package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/parser"
)

// ProcessBuilder creates executable commands for clients.
// This interface allows the supervisor to be decoupled from FFmpeg specifics.
type ProcessBuilder interface {
	// BuildCommand returns a ready-to-start command for the given client.
	BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error)

	// Name returns a human-readable name for this process type.
	Name() string

	// SetProgressFD sets the file descriptor number for progress output.
	// Called by supervisor before BuildCommand() when FD mode is enabled.
	// The builder should use "-progress pipe:N" where N is the FD number.
	// FD 3 is the first ExtraFiles entry, FD 4 is the second, etc.
	SetProgressFD(fd int)
}

// Callbacks contains optional callback functions for supervisor events.
type Callbacks struct {
	// OnStateChange is called when the client state changes.
	OnStateChange func(clientID int, oldState, newState State)

	// OnStart is called when a client process starts.
	OnStart func(clientID int, pid int)

	// OnExit is called when a client process exits.
	OnExit func(clientID int, exitCode int, uptime time.Duration)

	// OnRestart is called before a restart attempt.
	OnRestart func(clientID int, attempt int, delay time.Duration)
}

// Supervisor manages the lifecycle of a single client process.
// It handles starting, monitoring, and restarting the process with backoff.
type Supervisor struct {
	clientID  int
	builder   ProcessBuilder
	backoff   *Backoff
	logger    *slog.Logger
	callbacks Callbacks

	// State management
	state     State
	stateMu   sync.RWMutex
	startTime time.Time

	// Current process
	cmd   *exec.Cmd
	cmdMu sync.Mutex

	// Configuration
	maxRestarts int // 0 = unlimited
	restarts    int

	// Stats collection (metrics enhancement)
	statsEnabled       bool
	statsBufferSize    int
	statsDropThreshold float64

	// FD-based progress is always used when stats are enabled
	// Provides clean separation from stderr without creating filesystem files

	// Parsing pipelines (created per runOnce)
	progressPipeline *parser.Pipeline
	stderrPipeline   *parser.Pipeline

	// Parsers (set externally or use defaults)
	progressParser parser.LineParser
	stderrParser   parser.LineParser
}

// Config holds configuration for creating a new Supervisor.
type Config struct {
	ClientID    int
	Builder     ProcessBuilder
	Backoff     *Backoff
	Logger      *slog.Logger
	Callbacks   Callbacks
	MaxRestarts int // 0 = unlimited

	// Stats collection
	StatsEnabled       bool
	StatsBufferSize    int
	StatsDropThreshold float64

	// Parsers (optional - defaults to NoopParser)
	ProgressParser parser.LineParser
	StderrParser   parser.LineParser
}

// New creates a new Supervisor with the given configuration.
func New(cfg Config) *Supervisor {
	// Use NoopParser if no parsers provided
	progressParser := cfg.ProgressParser
	if progressParser == nil {
		progressParser = parser.NoopParser{}
	}
	stderrParser := cfg.StderrParser
	if stderrParser == nil {
		stderrParser = parser.NoopParser{}
	}

	// Default buffer size
	bufferSize := cfg.StatsBufferSize
	if bufferSize <= 0 {
		bufferSize = 1000
	}

	// Default threshold
	threshold := cfg.StatsDropThreshold
	if threshold <= 0 {
		threshold = 0.01
	}

	return &Supervisor{
		clientID:           cfg.ClientID,
		builder:            cfg.Builder,
		backoff:            cfg.Backoff,
		logger:             cfg.Logger,
		callbacks:          cfg.Callbacks,
		state:              StateCreated,
		maxRestarts:        cfg.MaxRestarts,
		statsEnabled:       cfg.StatsEnabled,
		statsBufferSize:    bufferSize,
		statsDropThreshold: threshold,
		progressParser:     progressParser,
		stderrParser:       stderrParser,
	}
}

// Run starts the supervision loop. It blocks until the context is cancelled.
// The supervisor will continuously restart the process on failure until:
// - The context is cancelled
// - MaxRestarts is reached (if configured)
func (s *Supervisor) Run(ctx context.Context) error {
	s.logger.Debug("supervisor_starting", "client_id", s.clientID)

	for {
		// Check if we should stop
		select {
		case <-ctx.Done():
			s.setState(StateStopped)
			s.logger.Debug("supervisor_stopped", "client_id", s.clientID, "reason", "context_cancelled")
			return ctx.Err()
		default:
		}

		// Check max restarts
		if s.maxRestarts > 0 && s.restarts >= s.maxRestarts {
			s.setState(StateStopped)
			s.logger.Warn("max_restarts_reached",
				"client_id", s.clientID,
				"restarts", s.restarts,
				"max", s.maxRestarts,
			)
			return errors.New("max restarts reached")
		}

		// Start the process
		exitCode, uptime, err := s.runOnce(ctx)
		if err != nil && ctx.Err() != nil {
			// Context cancelled during execution
			s.setState(StateStopped)
			return ctx.Err()
		}

		// Process exited, determine if we should reset backoff
		if ShouldReset(uptime, exitCode) {
			s.backoff.Reset()
		}

		// Calculate backoff delay
		delay := s.backoff.Next()
		s.restarts++

		// Notify callback
		if s.callbacks.OnRestart != nil {
			s.callbacks.OnRestart(s.clientID, s.restarts, delay)
		}

		s.logger.Info("client_restart_scheduled",
			"client_id", s.clientID,
			"attempt", s.restarts,
			"delay", delay.String(),
		)

		// Wait with backoff
		s.setState(StateBackoff)
		select {
		case <-ctx.Done():
			s.setState(StateStopped)
			return ctx.Err()
		case <-time.After(delay):
			// Continue to restart
		}
	}
}

// runOnce runs the process once and waits for it to exit.
// Returns the exit code, uptime, and any error.
func (s *Supervisor) runOnce(ctx context.Context) (exitCode int, uptime time.Duration, err error) {
	s.setState(StateStarting)

	// Create pipelines for this run
	if s.statsEnabled {
		s.progressPipeline = parser.NewPipeline(
			s.clientID, "progress",
			s.statsBufferSize, s.statsDropThreshold,
		)
		s.stderrPipeline = parser.NewPipeline(
			s.clientID, "stderr",
			s.statsBufferSize, s.statsDropThreshold,
		)
	}

	// Create progress source using FD mode (always when stats enabled)
	// No filesystem files needed - completely ephemeral
	var progressSource parser.LineSource
	var progressFDRead *os.File
	var progressFDWrite *os.File

	if s.statsEnabled {
		// Create an anonymous pipe for progress output
		var fdErr error
		progressFDRead, progressFDWrite, fdErr = os.Pipe()
		if fdErr != nil {
			s.logger.Error("fd_creation_failed",
				"client_id", s.clientID,
				"error", fdErr,
			)
			return 1, 0, fmt.Errorf("failed to create progress pipe: %w", fdErr)
		}

		// Create FD reader for progress
		fdReader := parser.NewFDReader(progressFDRead, s.progressPipeline)
		progressSource = fdReader

		// Tell the builder to use FD 3 instead of pipe:1
		// ExtraFiles[0] becomes FD 3 in the child process
		s.builder.SetProgressFD(3)
	}

	// Build the command (after setting FD if applicable)
	cmd, err := s.builder.BuildCommand(ctx, s.clientID)
	if err != nil {
		s.logger.Error("failed_to_build_command",
			"client_id", s.clientID,
			"error", err,
		)
		// Clean up FD if created
		if progressFDRead != nil {
			progressFDRead.Close()
		}
		if progressFDWrite != nil {
			progressFDWrite.Close()
		}
		return 1, 0, err
	}

	// Set up ExtraFiles for FD mode (always when stats enabled)
	if s.statsEnabled && progressFDWrite != nil {
		// Pass the write-end to the child process as FD 3
		cmd.ExtraFiles = []*os.File{progressFDWrite}
	}

	// stderr is always a pipe
	var stderr io.ReadCloser
	if s.statsEnabled {
		stderr, err = cmd.StderrPipe()
		if err != nil {
			s.logger.Error("failed_to_create_stderr_pipe",
				"client_id", s.clientID,
				"error", err,
			)
			if progressFDRead != nil {
				progressFDRead.Close()
			}
			if progressFDWrite != nil {
				progressFDWrite.Close()
			}
			return 1, 0, fmt.Errorf("stderr pipe: %w", err)
		}
	}

	// Set process group for clean shutdown
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Store command reference
	s.cmdMu.Lock()
	s.cmd = cmd
	s.cmdMu.Unlock()

	// Start the process
	s.startTime = time.Now()
	if err := cmd.Start(); err != nil {
		s.logger.Error("failed_to_start_process",
			"client_id", s.clientID,
			"error", err,
		)
		if progressFDRead != nil {
			progressFDRead.Close()
		}
		if progressFDWrite != nil {
			progressFDWrite.Close()
		}
		return 1, 0, err
	}

	// IMPORTANT: Close parent's write-end after Start()
	// This ensures EOF behavior is correct when FFmpeg exits
	if progressFDWrite != nil {
		progressFDWrite.Close()
		progressFDWrite = nil // Clear reference
	}

	pid := cmd.Process.Pid
	s.setState(StateRunning)

	s.logger.Info("client_started",
		"client_id", s.clientID,
		"pid", pid,
		"stats_enabled", s.statsEnabled,
	)

	// Start parsing pipelines if stats enabled
	var parseWg sync.WaitGroup
	if s.statsEnabled {
		// Start progress reader (FD mode or pipe mode)
		if progressSource != nil {
			go progressSource.Run()
		}

		// Start stderr reader (always pipe)
		stderrSource := parser.NewPipeReader(stderr, s.stderrPipeline)
		go stderrSource.Run()

		// Start Layer 2 (parsers)
		parseWg.Add(2)
		go func() {
			defer parseWg.Done()
			s.progressPipeline.RunParser(s.progressParser)
		}()
		go func() {
			defer parseWg.Done()
			s.stderrPipeline.RunParser(s.stderrParser)
		}()
	}

	// Notify callback
	if s.callbacks.OnStart != nil {
		s.callbacks.OnStart(s.clientID, pid)
	}

	// Wait for process to exit
	waitErr := cmd.Wait()
	uptime = time.Since(s.startTime)
	exitCode = extractExitCode(waitErr)

	// Close FD reader (if used) - this will close the pipeline channel
	if progressFDRead != nil {
		progressFDRead.Close()
	}

	// Wait for parsers to drain remaining data (with timeout)
	if s.statsEnabled {
		s.drainParsers(&parseWg)
	}

	s.logger.Info("client_exited",
		"client_id", s.clientID,
		"pid", pid,
		"exit_code", exitCode,
		"uptime", uptime.String(),
	)

	// Clear command reference
	s.cmdMu.Lock()
	s.cmd = nil
	s.cmdMu.Unlock()

	// Notify callback
	if s.callbacks.OnExit != nil {
		s.callbacks.OnExit(s.clientID, exitCode, uptime)
	}

	return exitCode, uptime, waitErr
}

// drainParsers waits for parsing pipelines to finish with a timeout.
func (s *Supervisor) drainParsers(parseWg *sync.WaitGroup) {
	const drainTimeout = 5 * time.Second

	done := make(chan struct{})
	go func() {
		parseWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Parsers finished normally
		s.logPipelineStats()
	case <-time.After(drainTimeout):
		s.logger.Warn("parser_drain_timeout",
			"client_id", s.clientID,
			"timeout", drainTimeout.String(),
			"reason", "parsers did not finish reading pipe data within timeout",
		)
		s.logPipelineStats()
	}
}

// logPipelineStats logs pipeline health metrics.
func (s *Supervisor) logPipelineStats() {
	if s.progressPipeline != nil {
		read, dropped, parsed := s.progressPipeline.Stats()
		if dropped > 0 || s.logger.Enabled(nil, slog.LevelDebug) {
			s.logger.Info("pipeline_stats",
				"client_id", s.clientID,
				"stream", "progress",
				"lines_read", read,
				"lines_dropped", dropped,
				"lines_parsed", parsed,
				"degraded", s.progressPipeline.IsDegraded(),
			)
		}
	}

	if s.stderrPipeline != nil {
		read, dropped, parsed := s.stderrPipeline.Stats()
		if dropped > 0 || s.logger.Enabled(nil, slog.LevelDebug) {
			s.logger.Info("pipeline_stats",
				"client_id", s.clientID,
				"stream", "stderr",
				"lines_read", read,
				"lines_dropped", dropped,
				"lines_parsed", parsed,
				"degraded", s.stderrPipeline.IsDegraded(),
			)
		}
	}
}

// Stop gracefully stops the supervised process.
// It first sends SIGTERM, then SIGKILL if the process doesn't exit.
func (s *Supervisor) Stop(timeout time.Duration) error {
	s.cmdMu.Lock()
	cmd := s.cmd
	s.cmdMu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}

	// Send SIGTERM to the process group
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil {
		syscall.Kill(-pgid, syscall.SIGTERM)
	} else {
		cmd.Process.Signal(syscall.SIGTERM)
	}

	// Wait for graceful shutdown
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		// Force kill
		s.logger.Warn("force_killing_process",
			"client_id", s.clientID,
			"pid", cmd.Process.Pid,
		)
		if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
			syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			cmd.Process.Kill()
		}
		return errors.New("process did not exit gracefully")
	}
}

// State returns the current state of the supervisor.
func (s *Supervisor) State() State {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.state
}

// setState updates the state and calls the callback if registered.
func (s *Supervisor) setState(newState State) {
	s.stateMu.Lock()
	oldState := s.state
	s.state = newState
	s.stateMu.Unlock()

	if s.callbacks.OnStateChange != nil && oldState != newState {
		s.callbacks.OnStateChange(s.clientID, oldState, newState)
	}
}

// ClientID returns the client ID for this supervisor.
func (s *Supervisor) ClientID() int {
	return s.clientID
}

// Restarts returns the number of restarts that have occurred.
func (s *Supervisor) Restarts() int {
	return s.restarts
}

// Uptime returns the current uptime if running, or 0 if not.
func (s *Supervisor) Uptime() time.Duration {
	if s.State() != StateRunning {
		return 0
	}
	return time.Since(s.startTime)
}

// SetParsers sets the line parsers for progress and stderr streams.
// Must be called before Run() for the parsers to be used.
func (s *Supervisor) SetParsers(progress, stderr parser.LineParser) {
	if progress != nil {
		s.progressParser = progress
	}
	if stderr != nil {
		s.stderrParser = stderr
	}
}

// PipelineStats returns the pipeline statistics for both streams.
// Returns zeros if stats collection is disabled or pipelines haven't run.
func (s *Supervisor) PipelineStats() (progressRead, progressDropped, stderrRead, stderrDropped int64) {
	if s.progressPipeline != nil {
		progressRead, progressDropped, _ = s.progressPipeline.Stats()
	}
	if s.stderrPipeline != nil {
		stderrRead, stderrDropped, _ = s.stderrPipeline.Stats()
	}
	return
}

// IsMetricsDegraded returns true if either pipeline has dropped >threshold% of lines.
func (s *Supervisor) IsMetricsDegraded() bool {
	if s.progressPipeline != nil && s.progressPipeline.IsDegraded() {
		return true
	}
	if s.stderrPipeline != nil && s.stderrPipeline.IsDegraded() {
		return true
	}
	return false
}

// StatsEnabled returns whether stats collection is enabled.
func (s *Supervisor) StatsEnabled() bool {
	return s.statsEnabled
}

// extractExitCode extracts the exit code from a Wait() error.
func extractExitCode(err error) int {
	if err == nil {
		return 0
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() {
				// Signal exit: 128 + signal number
				return 128 + int(status.Signal())
			}
			return status.ExitStatus()
		}
	}

	// Unknown error, assume exit code 1
	return 1
}
