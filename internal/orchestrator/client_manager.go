package orchestrator

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/parser"
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

	// Stats collection
	statsEnabled       bool
	statsBufferSize    int
	statsDropThreshold float64

	// Per-client progress tracking (Phase 2)
	// Maps clientID -> latest ProgressUpdate
	latestProgress map[int]*parser.ProgressUpdate
	progressMu     sync.RWMutex

	// Aggregated stats (Phase 5 will expand this)
	totalBytesDownloaded atomic.Int64
	totalProgressUpdates atomic.Int64

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

	// Stats collection
	StatsEnabled       bool
	StatsBufferSize    int
	StatsDropThreshold float64
}

// NewClientManager creates a new ClientManager.
func NewClientManager(cfg ManagerConfig) *ClientManager {
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

	return &ClientManager{
		builder:            cfg.Builder,
		logger:             cfg.Logger,
		backoffConfig:      cfg.BackoffConfig,
		maxRestarts:        cfg.MaxRestarts,
		statsEnabled:       cfg.StatsEnabled,
		statsBufferSize:    bufferSize,
		statsDropThreshold: threshold,
		callbacks:          cfg.Callbacks,
		supervisors:        make(map[int]*supervisor.Supervisor),
		latestProgress:     make(map[int]*parser.ProgressUpdate),
		configSeed:         time.Now().UnixNano(),
	}
}

// StartClient creates and starts a new supervised client.
// The supervisor runs in a goroutine and will restart on failures.
func (m *ClientManager) StartClient(ctx context.Context, clientID int) {
	// Create backoff calculator for this client
	backoff := supervisor.NewBackoff(clientID, m.configSeed, m.backoffConfig)

	// Create progress parser for this client (Phase 2)
	var progressParser parser.LineParser
	if m.statsEnabled {
		progressParser = parser.NewProgressParser(m.createProgressCallback(clientID))
	}

	// Create supervisor with callbacks
	sup := supervisor.New(supervisor.Config{
		ClientID:    clientID,
		Builder:     m.builder,
		Backoff:     backoff,
		Logger:      m.logger,
		MaxRestarts: m.maxRestarts,
		// Stats collection
		StatsEnabled:       m.statsEnabled,
		StatsBufferSize:    m.statsBufferSize,
		StatsDropThreshold: m.statsDropThreshold,
		// Parsers (Phase 2 - ProgressParser, Phase 3 will add HLSEventParser)
		ProgressParser: progressParser,
		// StderrParser will be added in Phase 3
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

// createProgressCallback creates a callback for the ProgressParser.
// This callback is called for each complete progress block from FFmpeg.
func (m *ClientManager) createProgressCallback(clientID int) parser.ProgressCallback {
	return func(update *parser.ProgressUpdate) {
		m.totalProgressUpdates.Add(1)

		// Store latest progress for this client
		m.progressMu.Lock()
		prev := m.latestProgress[clientID]
		m.latestProgress[clientID] = update
		m.progressMu.Unlock()

		// Track bytes downloaded (delta from previous)
		// Note: total_size resets on FFmpeg restart, so we track deltas
		if prev != nil && update.TotalSize > prev.TotalSize {
			delta := update.TotalSize - prev.TotalSize
			m.totalBytesDownloaded.Add(delta)
		} else if prev == nil && update.TotalSize > 0 {
			// First update for this client
			m.totalBytesDownloaded.Add(update.TotalSize)
		}
		// If update.TotalSize < prev.TotalSize, FFmpeg restarted - don't subtract

		// Log stalling detection at debug level
		if update.IsStalling() {
			m.logger.Debug("client_stalling",
				"client_id", clientID,
				"speed", update.Speed,
				"playback_position", update.OutTimeDuration().String(),
			)
		}
	}
}

// ProgressStats returns aggregated progress statistics.
// This is a Phase 2 placeholder - Phase 5 will expand this significantly.
type ProgressStats struct {
	TotalBytesDownloaded int64
	TotalProgressUpdates int64
	ClientsWithProgress  int
	StallingClients      int
	AverageSpeed         float64
}

// GetProgressStats returns current progress statistics across all clients.
func (m *ClientManager) GetProgressStats() ProgressStats {
	m.progressMu.RLock()
	defer m.progressMu.RUnlock()

	stats := ProgressStats{
		TotalBytesDownloaded: m.totalBytesDownloaded.Load(),
		TotalProgressUpdates: m.totalProgressUpdates.Load(),
		ClientsWithProgress:  len(m.latestProgress),
	}

	// Calculate average speed and count stalling clients
	var totalSpeed float64
	var speedCount int
	for _, progress := range m.latestProgress {
		if progress.Speed > 0 {
			totalSpeed += progress.Speed
			speedCount++
		}
		if progress.IsStalling() {
			stats.StallingClients++
		}
	}

	if speedCount > 0 {
		stats.AverageSpeed = totalSpeed / float64(speedCount)
	}

	return stats
}

// GetClientProgress returns the latest progress for a specific client.
// Returns nil if no progress has been received for this client.
func (m *ClientManager) GetClientProgress(clientID int) *parser.ProgressUpdate {
	m.progressMu.RLock()
	defer m.progressMu.RUnlock()

	if progress, ok := m.latestProgress[clientID]; ok {
		// Return a copy to avoid race conditions
		copy := *progress
		return &copy
	}
	return nil
}
