package orchestrator

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/parser"
	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/stats"
	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/supervisor"
)

// debugRateSnapshot holds values for calculating instantaneous rates for debug stats.
type debugRateSnapshot struct {
	timestamp    time.Time
	segments     int64
	playlists    int64
	httpRequests int64
	tcpConnects  int64
}

// ClientManager coordinates multiple client supervisors.
// It handles starting clients, tracking their state, and coordinating shutdown.
type ClientManager struct {
	builder    supervisor.ProcessBuilder
	logger     *slog.Logger
	configSeed int64

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

	// Per-client debug event tracking (Phase 7 - replaces HLSEventParser)
	// Maps clientID -> DebugEventParser (for layered metrics: HLS/HTTP/TCP)
	debugParsers map[int]*parser.DebugEventParser
	debugMu      sync.RWMutex

	// Rate tracking for debug stats (Phase 7.4) - Lock-free using atomic.Value
	prevDebugSnapshot atomic.Value // *debugRateSnapshot

	// Per-client stats (Phase 4/5)
	// Maps clientID -> ClientStats
	clientStats   map[int]*stats.ClientStats
	clientStatsMu sync.RWMutex

	// Stats aggregator (Phase 5)
	aggregator *stats.StatsAggregator

	// Aggregated stats (legacy - kept for backward compatibility)
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
	activeCount  atomic.Int64
	startedCount atomic.Int64
	restartCount atomic.Int64
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

	// FD mode is always enabled when stats are enabled (no flag needed)
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

	cm := &ClientManager{
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
		debugParsers:       make(map[int]*parser.DebugEventParser),
		clientStats:        make(map[int]*stats.ClientStats),
		aggregator:         stats.NewStatsAggregator(threshold),
		configSeed:         time.Now().UnixNano(),
	}
	// Initialize atomic.Value with first snapshot (lock-free)
	cm.prevDebugSnapshot.Store(&debugRateSnapshot{timestamp: time.Now()})
	return cm
}

// StartClient creates and starts a new supervised client.
// The supervisor runs in a goroutine and will restart on failures.
func (m *ClientManager) StartClient(ctx context.Context, clientID int) {
	// Create backoff calculator for this client
	backoff := supervisor.NewBackoff(clientID, m.configSeed, m.backoffConfig)

	// Create ClientStats for this client (Phase 4/5)
	var clientStats *stats.ClientStats
	if m.statsEnabled {
		clientStats = stats.NewClientStats(clientID)

		// Register with aggregator
		m.aggregator.AddClient(clientStats)

		// Store reference for direct access
		m.clientStatsMu.Lock()
		m.clientStats[clientID] = clientStats
		m.clientStatsMu.Unlock()
	}

	// Create progress parser for this client (Phase 2)
	var progressParser parser.LineParser
	if m.statsEnabled {
		progressParser = parser.NewProgressParser(m.createProgressCallback(clientID, clientStats))
	}

	// Create debug event parser for this client (Phase 7 - layered metrics)
	// Replaces HLSEventParser with comprehensive HLS/HTTP/TCP tracking
	var stderrParser parser.LineParser
	var debugParser *parser.DebugEventParser
	if m.statsEnabled {
		// Target duration for jitter calculation (2s is HLS default)
		targetDuration := 2 * time.Second
		debugParser = parser.NewDebugEventParser(
			clientID,
			targetDuration,
			m.createDebugEventCallback(clientID, clientStats),
		)
		stderrParser = debugParser

		// Store reference for stats aggregation
		m.debugMu.Lock()
		m.debugParsers[clientID] = debugParser
		m.debugMu.Unlock()
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
		// FD mode is always enabled when stats are enabled
		// Parsers (Phase 2 - ProgressParser, Phase 7 - DebugEventParser)
		ProgressParser: progressParser,
		StderrParser:   stderrParser,
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
func (m *ClientManager) createProgressCallback(clientID int, clientStats *stats.ClientStats) parser.ProgressCallback {
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

		// Update ClientStats (Phase 4/5)
		if clientStats != nil {
			// Update bytes - ClientStats handles FFmpeg restart resets internally
			clientStats.UpdateCurrentBytes(update.TotalSize)

			// Update speed and drift
			clientStats.UpdateSpeed(update.Speed)
			clientStats.UpdateDrift(update.OutTimeUS)

			// Note: Segment completion is now handled automatically by DebugEventParser
			// when it sees a new HLS request. No need for inferred latency tracking.
		}

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

// createDebugEventCallback creates a callback for the DebugEventParser.
// This callback handles all debug events from the HLS/HTTP/TCP layers.
func (m *ClientManager) createDebugEventCallback(clientID int, clientStats *stats.ClientStats) parser.DebugEventCallback {
	return func(event *parser.DebugEvent) {
		// Track bytes from Content-Length headers (for live streams where total_size=N/A)
		// Note: Content-Length headers are logged at TRACE level, so may not be available
		// For now, we'll track bytes when available, and estimate from segments as fallback
		if event.Bytes > 0 && clientStats != nil {
			// Add bytes to current process total
			// Note: This accumulates bytes from Content-Length headers
			currentBytes := clientStats.TotalBytes()
			clientStats.UpdateCurrentBytes(currentBytes + event.Bytes)
		}

		// Update ClientStats based on event type (accurate metrics from DebugEventParser)
		switch event.Type {
		// HLS Layer events
		case parser.DebugEventHLSRequest:
			// Segment request
			if clientStats != nil {
				clientStats.IncrementSegmentRequests()
				// Note: Segment tracking is handled by DebugEventParser automatically
				// No need for OnSegmentRequestStart() - DebugEventParser tracks start/end
			}

		case parser.DebugEventPlaylistOpen:
			// Manifest refresh
			if clientStats != nil {
				clientStats.IncrementManifestRequests()
			}
			// Debug: Log first few playlist opens to verify they're being detected
			// Use DebugStats to get accurate count instead of legacy counter
			m.debugMu.RLock()
			if debugParser, ok := m.debugParsers[clientID]; ok {
				stats := debugParser.Stats()
				if stats.PlaylistRefreshes <= 3 {
					m.logger.Debug("playlist_open_detected",
						"client_id", clientID,
						"url", event.URL,
						"playlist_refreshes", stats.PlaylistRefreshes,
					)
				}
			}
			m.debugMu.RUnlock()

		// HTTP Layer events
		case parser.DebugEventHTTPError:
			if clientStats != nil {
				clientStats.RecordHTTPError(event.HTTPCode)
			}

		case parser.DebugEventReconnect:
			if clientStats != nil {
				clientStats.RecordReconnection()
			}

		// TCP Layer events
		case parser.DebugEventTCPFailed:
			if event.FailReason == "timeout" {
				if clientStats != nil {
					clientStats.RecordTimeout()
				}
			}

		// Error events (critical for load testing)
		case parser.DebugEventSegmentFailed:
			// Segment open failures tracked via DebugStats
			m.logger.Debug("segment_failed",
				"client_id", clientID,
				"segment_id", event.SegmentID,
				"playlist_id", event.PlaylistID,
			)

		case parser.DebugEventSegmentSkipped:
			// Data loss! Segment skipped after retries
			m.logger.Warn("segment_skipped",
				"client_id", clientID,
				"segment_id", event.SegmentID,
				"playlist_id", event.PlaylistID,
			)

		case parser.DebugEventPlaylistFailed:
			// Live edge lost!
			m.logger.Warn("playlist_failed",
				"client_id", clientID,
				"playlist_id", event.PlaylistID,
			)

		case parser.DebugEventSegmentsExpired:
			// Client fell behind
			m.logger.Warn("segments_expired",
				"client_id", clientID,
				"skip_count", event.SkipCount,
			)
		}
	}
}

// GetDebugStats returns aggregated debug statistics across all clients.
// This is the primary method for the layered TUI dashboard (Phase 7).
func (m *ClientManager) GetDebugStats() stats.DebugStatsAggregate {
	m.debugMu.RLock()
	defer m.debugMu.RUnlock()

	agg := stats.DebugStatsAggregate{
		ClientsWithDebugStats: len(m.debugParsers),
	}

	// Aggregate stats from all debug parsers
	var totalSegWallTime, totalTCPConnect float64
	var segWallTimeCount, tcpConnectCount int64

	for _, dp := range m.debugParsers {
		stats := dp.Stats()

		// HLS Layer
		agg.SegmentsDownloaded += stats.SegmentCount
		agg.SegmentsFailed += stats.SegmentFailedCount
		agg.SegmentsSkipped += stats.SegmentSkippedCount
		agg.SegmentsExpired += stats.SegmentsExpiredSum
		agg.PlaylistsRefreshed += stats.PlaylistRefreshes
		agg.PlaylistsFailed += stats.PlaylistFailedCount
		agg.PlaylistLateCount += stats.PlaylistLateCount
		agg.SequenceSkips += stats.SequenceSkips

		// Debug: Log parser stats for diagnostics (only when TUI is not enabled to avoid log spam)
		// This helps identify if events are being parsed but not counted

		// Aggregate wall time (weighted average)
		if stats.SegmentCount > 0 {
			totalSegWallTime += stats.SegmentAvgMs * float64(stats.SegmentCount)
			segWallTimeCount += stats.SegmentCount
			if stats.SegmentMaxMs > agg.SegmentWallTimeMax {
				agg.SegmentWallTimeMax = stats.SegmentMaxMs
			}
			if agg.SegmentWallTimeMin == 0 || stats.SegmentMinMs < agg.SegmentWallTimeMin {
				agg.SegmentWallTimeMin = stats.SegmentMinMs
			}

			// Aggregate percentiles (take max across clients - worst-case is useful for load testing)
			if stats.SegmentWallTimeP25 > agg.SegmentWallTimeP25 {
				agg.SegmentWallTimeP25 = stats.SegmentWallTimeP25
			}
			if stats.SegmentWallTimeP50 > agg.SegmentWallTimeP50 {
				agg.SegmentWallTimeP50 = stats.SegmentWallTimeP50
			}
			if stats.SegmentWallTimeP75 > agg.SegmentWallTimeP75 {
				agg.SegmentWallTimeP75 = stats.SegmentWallTimeP75
			}
			if stats.SegmentWallTimeP95 > agg.SegmentWallTimeP95 {
				agg.SegmentWallTimeP95 = stats.SegmentWallTimeP95
			}
			if stats.SegmentWallTimeP99 > agg.SegmentWallTimeP99 {
				agg.SegmentWallTimeP99 = stats.SegmentWallTimeP99
			}
		}

		// Aggregate manifest wall time
		agg.ManifestCount += stats.ManifestCount
		if stats.ManifestCount > 0 {
			// Weighted average
			totalCount := agg.ManifestCount
			if totalCount > 0 {
				agg.ManifestWallTimeAvg = (agg.ManifestWallTimeAvg*float64(agg.ManifestCount-stats.ManifestCount) +
					stats.ManifestAvgMs*float64(stats.ManifestCount)) / float64(totalCount)
			}

			// Min/Max
			if stats.ManifestMaxMs > agg.ManifestWallTimeMax {
				agg.ManifestWallTimeMax = stats.ManifestMaxMs
			}
			if agg.ManifestWallTimeMin == 0 || stats.ManifestMinMs < agg.ManifestWallTimeMin {
				agg.ManifestWallTimeMin = stats.ManifestMinMs
			}

			// Aggregate percentiles (take max across clients - worst-case is useful for load testing)
			if stats.ManifestWallTimeP25 > agg.ManifestWallTimeP25 {
				agg.ManifestWallTimeP25 = stats.ManifestWallTimeP25
			}
			if stats.ManifestWallTimeP50 > agg.ManifestWallTimeP50 {
				agg.ManifestWallTimeP50 = stats.ManifestWallTimeP50
			}
			if stats.ManifestWallTimeP75 > agg.ManifestWallTimeP75 {
				agg.ManifestWallTimeP75 = stats.ManifestWallTimeP75
			}
			if stats.ManifestWallTimeP95 > agg.ManifestWallTimeP95 {
				agg.ManifestWallTimeP95 = stats.ManifestWallTimeP95
			}
			if stats.ManifestWallTimeP99 > agg.ManifestWallTimeP99 {
				agg.ManifestWallTimeP99 = stats.ManifestWallTimeP99
			}
		}

		// Aggregate jitter
		if stats.PlaylistMaxJitterMs > agg.PlaylistJitterMax {
			agg.PlaylistJitterMax = stats.PlaylistMaxJitterMs
		}

		// HTTP Layer
		agg.HTTPOpenCount += stats.HTTPOpenCount
		agg.HTTP4xxCount += stats.HTTP4xxCount
		agg.HTTP5xxCount += stats.HTTP5xxCount
		agg.ReconnectCount += stats.ReconnectCount

		// TCP Layer
		agg.TCPConnectCount += stats.TCPConnectCount
		agg.TCPSuccessCount += stats.TCPSuccessCount
		agg.TCPRefusedCount += stats.TCPRefusedCount
		agg.TCPTimeoutCount += stats.TCPTimeoutCount

		// Aggregate TCP connect time (weighted average)
		if stats.TCPConnectCount > 0 {
			totalTCPConnect += stats.TCPConnectAvgMs * float64(stats.TCPConnectCount)
			tcpConnectCount += stats.TCPConnectCount
			if stats.TCPConnectMaxMs > agg.TCPConnectMaxMs {
				agg.TCPConnectMaxMs = stats.TCPConnectMaxMs
			}
			if agg.TCPConnectMinMs == 0 || stats.TCPConnectMinMs < agg.TCPConnectMinMs {
				agg.TCPConnectMinMs = stats.TCPConnectMinMs
			}
		}

		// Timing accuracy
		agg.TimestampsUsed += stats.TimestampsUsed
		agg.LinesProcessed += stats.LinesProcessed
	}

	// Calculate averages
	if segWallTimeCount > 0 {
		agg.SegmentWallTimeAvg = totalSegWallTime / float64(segWallTimeCount)
	}
	if tcpConnectCount > 0 {
		agg.TCPConnectAvgMs = totalTCPConnect / float64(tcpConnectCount)
	}

	// Calculate TCP health ratio
	totalTCP := agg.TCPSuccessCount + agg.TCPRefusedCount + agg.TCPTimeoutCount
	if totalTCP > 0 {
		agg.TCPHealthRatio = float64(agg.TCPSuccessCount) / float64(totalTCP)
	} else {
		agg.TCPHealthRatio = 1.0 // No connections = healthy
	}

	// Calculate error rate
	if agg.HTTPOpenCount > 0 {
		totalErrors := agg.HTTP4xxCount + agg.HTTP5xxCount + agg.SegmentsFailed
		agg.ErrorRate = float64(totalErrors) / float64(agg.HTTPOpenCount)
	}

	// Calculate instantaneous rates (Phase 7.4) - Lock-free using atomic.Value
	now := time.Now()
	// Lock-free read
	prevSnapshotPtr := m.prevDebugSnapshot.Load()
	if prevSnapshotPtr != nil {
		prevSnapshot := prevSnapshotPtr.(*debugRateSnapshot)
		elapsed := now.Sub(prevSnapshot.timestamp).Seconds()
		if elapsed > 0 {
			agg.InstantSegmentsRate = float64(agg.SegmentsDownloaded-prevSnapshot.segments) / elapsed
			agg.InstantPlaylistsRate = float64(agg.PlaylistsRefreshed-prevSnapshot.playlists) / elapsed
			agg.InstantHTTPRequestsRate = float64(agg.HTTPOpenCount-prevSnapshot.httpRequests) / elapsed
			agg.InstantTCPConnectsRate = float64(agg.TCPConnectCount-prevSnapshot.tcpConnects) / elapsed
		}
	}
	// Lock-free write - atomically swap snapshot pointer
	newSnapshot := &debugRateSnapshot{
		timestamp:    now,
		segments:     agg.SegmentsDownloaded,
		playlists:    agg.PlaylistsRefreshed,
		httpRequests: agg.HTTPOpenCount,
		tcpConnects:  agg.TCPConnectCount,
	}
	m.prevDebugSnapshot.Store(newSnapshot)

	return agg
}

// GetClientDebugStats returns debug statistics for a specific client.
// Returns nil if no debug parser exists for this client.
func (m *ClientManager) GetClientDebugStats(clientID int) *parser.DebugStats {
	m.debugMu.RLock()
	defer m.debugMu.RUnlock()

	if debugParser, ok := m.debugParsers[clientID]; ok {
		stats := debugParser.Stats()
		return &stats
	}
	return nil
}

// Legacy methods removed - use GetDebugStats() for accurate metrics from DebugEventParser

// GetAggregatedStats returns aggregated statistics across all clients.
// This is the primary method for getting comprehensive stats (Phase 5).
func (m *ClientManager) GetAggregatedStats() *stats.AggregatedStats {
	if m.aggregator == nil {
		return nil
	}
	return m.aggregator.Aggregate()
}

// GetStatsAggregator returns the stats aggregator for direct access.
func (m *ClientManager) GetStatsAggregator() *stats.StatsAggregator {
	return m.aggregator
}

// GetClientStats returns the ClientStats for a specific client.
// Returns nil if stats are not enabled or client doesn't exist.
func (m *ClientManager) GetClientStats(clientID int) *stats.ClientStats {
	m.clientStatsMu.RLock()
	defer m.clientStatsMu.RUnlock()
	return m.clientStats[clientID]
}
