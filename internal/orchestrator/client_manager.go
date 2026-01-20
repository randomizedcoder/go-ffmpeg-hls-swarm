package orchestrator

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/supervisor"
)

// ClientManager coordinates multiple client supervisors.
// It handles starting clients, tracking their state, and coordinating shutdown.
type ClientManager struct {
	builder     supervisor.ProcessBuilder
	logger      *slog.Logger
	configSeed  int64

	// Backoff configuration
	backoffConfig supervisor.BackoffConfig

	// Maximum restarts per client (0 = unlimited)
	maxRestarts int

	// Supervisors indexed by client ID
	supervisors map[int]*supervisor.Supervisor
	mu          sync.RWMutex

	// WaitGroup for all supervisor goroutines
	wg sync.WaitGroup

	// Callbacks for external metrics/logging
	callbacks ManagerCallbacks

	// Counters
	activeCount   atomic.Int64
	startedCount  atomic.Int64
	restartCount  atomic.Int64
}

// ManagerCallbacks contains optional callbacks for manager events.
type ManagerCallbacks struct {
	// OnClientStateChange is called when any client changes state.
	OnClientStateChange func(clientID int, oldState, newState supervisor.State)

	// OnClientStart is called when a client process starts.
	OnClientStart func(clientID int, pid int)

	// OnClientExit is called when a client process exits.
	OnClientExit func(clientID int, exitCode int, uptime time.Duration)

	// OnClientRestart is called when a client is about to restart.
	OnClientRestart func(clientID int, attempt int, delay time.Duration)
}

// ManagerConfig holds configuration for the ClientManager.
type ManagerConfig struct {
	Builder       supervisor.ProcessBuilder
	Logger        *slog.Logger
	BackoffConfig supervisor.BackoffConfig
	MaxRestarts   int
	Callbacks     ManagerCallbacks
}

// NewClientManager creates a new ClientManager.
func NewClientManager(cfg ManagerConfig) *ClientManager {
	return &ClientManager{
		builder:       cfg.Builder,
		logger:        cfg.Logger,
		backoffConfig: cfg.BackoffConfig,
		maxRestarts:   cfg.MaxRestarts,
		callbacks:     cfg.Callbacks,
		supervisors:   make(map[int]*supervisor.Supervisor),
		configSeed:    time.Now().UnixNano(),
	}
}

// StartClient creates and starts a new supervised client.
// The supervisor runs in a goroutine and will restart on failures.
func (m *ClientManager) StartClient(ctx context.Context, clientID int) {
	// Create backoff calculator for this client
	backoff := supervisor.NewBackoff(clientID, m.configSeed, m.backoffConfig)

	// Create supervisor with callbacks
	sup := supervisor.New(supervisor.Config{
		ClientID:    clientID,
		Builder:     m.builder,
		Backoff:     backoff,
		Logger:      m.logger,
		MaxRestarts: m.maxRestarts,
		Callbacks: supervisor.Callbacks{
			OnStateChange: m.handleStateChange,
			OnStart:       m.handleStart,
			OnExit:        m.handleExit,
			OnRestart:     m.handleRestart,
		},
	})

	// Register supervisor
	m.mu.Lock()
	m.supervisors[clientID] = sup
	m.mu.Unlock()

	// Track started count
	m.startedCount.Add(1)

	// Start supervisor in goroutine
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		if err := sup.Run(ctx); err != nil {
			// Context cancelled or max restarts reached
			m.logger.Debug("supervisor_ended",
				"client_id", clientID,
				"error", err,
			)
		}
	}()
}

// handleStateChange processes state changes from supervisors.
func (m *ClientManager) handleStateChange(clientID int, oldState, newState supervisor.State) {
	// Update active count
	wasActive := oldState == supervisor.StateRunning
	isActive := newState == supervisor.StateRunning

	if !wasActive && isActive {
		m.activeCount.Add(1)
	} else if wasActive && !isActive {
		m.activeCount.Add(-1)
	}

	// Forward to external callback
	if m.callbacks.OnClientStateChange != nil {
		m.callbacks.OnClientStateChange(clientID, oldState, newState)
	}
}

// handleStart processes client start events.
func (m *ClientManager) handleStart(clientID int, pid int) {
	if m.callbacks.OnClientStart != nil {
		m.callbacks.OnClientStart(clientID, pid)
	}
}

// handleExit processes client exit events.
func (m *ClientManager) handleExit(clientID int, exitCode int, uptime time.Duration) {
	if m.callbacks.OnClientExit != nil {
		m.callbacks.OnClientExit(clientID, exitCode, uptime)
	}
}

// handleRestart processes restart events.
func (m *ClientManager) handleRestart(clientID int, attempt int, delay time.Duration) {
	m.restartCount.Add(1)

	if m.callbacks.OnClientRestart != nil {
		m.callbacks.OnClientRestart(clientID, attempt, delay)
	}
}

// Shutdown gracefully stops all clients.
// It waits for all supervisors to stop, with a timeout.
func (m *ClientManager) Shutdown(ctx context.Context) error {
	m.logger.Info("shutdown_initiated", "active_clients", m.ActiveCount())

	// Wait for all supervisors to finish
	// They should stop because the context passed to StartClient is cancelled
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		m.logger.Info("all_clients_stopped")
		return nil
	case <-ctx.Done():
		m.logger.Warn("shutdown_timeout")
		return ctx.Err()
	}
}

// ActiveCount returns the number of currently running clients.
func (m *ClientManager) ActiveCount() int {
	return int(m.activeCount.Load())
}

// StartedCount returns the total number of clients that have been started.
func (m *ClientManager) StartedCount() int {
	return int(m.startedCount.Load())
}

// RestartCount returns the total number of restart events.
func (m *ClientManager) RestartCount() int {
	return int(m.restartCount.Load())
}

// ClientCount returns the number of registered supervisors.
func (m *ClientManager) ClientCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.supervisors)
}

// GetSupervisor returns the supervisor for a specific client ID.
func (m *ClientManager) GetSupervisor(clientID int) *supervisor.Supervisor {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.supervisors[clientID]
}

// States returns a map of client IDs to their current states.
func (m *ClientManager) States() map[int]supervisor.State {
	m.mu.RLock()
	defer m.mu.RUnlock()

	states := make(map[int]supervisor.State, len(m.supervisors))
	for id, sup := range m.supervisors {
		states[id] = sup.State()
	}
	return states
}
