package supervisor

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// ProcessBuilder creates executable commands for clients.
// This interface allows the supervisor to be decoupled from FFmpeg specifics.
type ProcessBuilder interface {
	// BuildCommand returns a ready-to-start command for the given client.
	BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error)

	// Name returns a human-readable name for this process type.
	Name() string
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
	clientID int
	builder  ProcessBuilder
	backoff  *Backoff
	logger   *slog.Logger
	callbacks Callbacks

	// State management
	state     State
	stateMu   sync.RWMutex
	startTime time.Time

	// Current process
	cmd    *exec.Cmd
	cmdMu  sync.Mutex

	// Configuration
	maxRestarts int // 0 = unlimited
	restarts    int
}

// Config holds configuration for creating a new Supervisor.
type Config struct {
	ClientID    int
	Builder     ProcessBuilder
	Backoff     *Backoff
	Logger      *slog.Logger
	Callbacks   Callbacks
	MaxRestarts int // 0 = unlimited
}

// New creates a new Supervisor with the given configuration.
func New(cfg Config) *Supervisor {
	return &Supervisor{
		clientID:    cfg.ClientID,
		builder:     cfg.Builder,
		backoff:     cfg.Backoff,
		logger:      cfg.Logger,
		callbacks:   cfg.Callbacks,
		state:       StateCreated,
		maxRestarts: cfg.MaxRestarts,
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

	// Build the command
	cmd, err := s.builder.BuildCommand(ctx, s.clientID)
	if err != nil {
		s.logger.Error("failed_to_build_command",
			"client_id", s.clientID,
			"error", err,
		)
		return 1, 0, err
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
		return 1, 0, err
	}

	pid := cmd.Process.Pid
	s.setState(StateRunning)

	s.logger.Info("client_started",
		"client_id", s.clientID,
		"pid", pid,
	)

	// Notify callback
	if s.callbacks.OnStart != nil {
		s.callbacks.OnStart(s.clientID, pid)
	}

	// Wait for process to exit
	waitErr := cmd.Wait()
	uptime = time.Since(s.startTime)
	exitCode = extractExitCode(waitErr)

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
