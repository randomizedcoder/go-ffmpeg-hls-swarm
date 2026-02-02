package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/config"
	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/metrics"
	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/preflight"
	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/process"
	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/stats"
	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/supervisor"
	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/tui"
)

// Orchestrator coordinates all components for an HLS load test.
type Orchestrator struct {
	config *config.Config
	logger *slog.Logger

	runner         *process.FFmpegRunner
	clientManager  *ClientManager
	rampScheduler  *RampScheduler
	metrics        *metrics.Collector
	metricsServer  *metrics.Server
	originScraper  *metrics.OriginScraper
	segmentScraper *metrics.SegmentScraper

	startTime time.Time
}

// New creates a new Orchestrator with the given configuration.
func New(cfg *config.Config, logger *slog.Logger) *Orchestrator {
	// Create FFmpeg runner
	ffmpegConfig := &process.FFmpegConfig{
		BinaryPath:        cfg.FFmpegPath,
		StreamURL:         cfg.StreamURL,
		Variant:           process.VariantSelection(cfg.Variant),
		UserAgent:         cfg.UserAgent,
		Timeout:           cfg.Timeout,
		Reconnect:         cfg.Reconnect,
		ReconnectDelayMax: cfg.ReconnectDelayMax,
		SegMaxRetry:       cfg.SegMaxRetry,
		LogLevel:          cfg.LogLevel,
		ResolveIP:         cfg.ResolveIP,
		DangerousMode:     cfg.DangerousMode,
		NoCache:           cfg.NoCache,
		Headers:           cfg.Headers,
		ProgramID:         -1,
		// Stats collection
		StatsEnabled:  cfg.StatsEnabled,
		StatsLogLevel: cfg.StatsLogLevel,
		DebugLogging:  cfg.DebugLogging,
	}
	runner := process.NewFFmpegRunner(ffmpegConfig)

	// Create ramp scheduler
	rampScheduler := NewRampScheduler(cfg.RampRate, cfg.RampJitter)

	// Create metrics
	collector := metrics.NewCollector(metrics.CollectorConfig{
		TargetClients:    cfg.Clients,
		TestDuration:     cfg.Duration,
		StreamURL:        cfg.StreamURL,
		Variant:          cfg.Variant,
		PerClientMetrics: cfg.PromClientMetrics,
	})
	metricsServer := metrics.NewServer(cfg.MetricsAddr, logger)

	// Initialize origin scraper if URLs are configured
	var originScraper *metrics.OriginScraper
	if cfg.OriginMetricsEnabled() {
		nodeURL, nginxURL := cfg.ResolveOriginMetricsURLs()
		originScraper = metrics.NewOriginScraper(
			nodeURL,
			nginxURL,
			cfg.OriginMetricsInterval,
			cfg.OriginMetricsWindow,
			logger,
		)
	}

	// Initialize segment scraper if configured (for accurate byte tracking)
	var segmentScraper *metrics.SegmentScraper
	if cfg.SegmentSizesEnabled() {
		segmentScraper = metrics.NewSegmentScraper(
			cfg.ResolveSegmentSizesURL(),
			cfg.SegmentSizesScrapeInterval,
			cfg.SegmentSizesScrapeJitter,
			cfg.SegmentCacheWindow,
			logger,
		)
		logger.Info("segment_scraper_initialized",
			"url", cfg.ResolveSegmentSizesURL(),
			"interval", cfg.SegmentSizesScrapeInterval,
		)
	} else {
		logger.Debug("segment_scraper_disabled",
			"origin_metrics_host", cfg.OriginMetricsHost,
			"segment_sizes_url", cfg.SegmentSizesURL,
		)
	}

	orch := &Orchestrator{
		config:         cfg,
		logger:         logger,
		runner:         runner,
		rampScheduler:  rampScheduler,
		metrics:        collector,
		metricsServer:  metricsServer,
		originScraper:  originScraper,
		segmentScraper: segmentScraper,
	}

	// Create client manager with callbacks
	managerCfg := ManagerConfig{
		Builder: runner,
		Logger:  logger,
		BackoffConfig: supervisor.BackoffConfig{
			Initial:    cfg.BackoffInitial,
			Max:        cfg.BackoffMax,
			Multiplier: cfg.BackoffMultiply,
			JitterPct:  0.4,
		},
		MaxRestarts: cfg.MaxRestarts,
		// Stats collection
		StatsEnabled:       cfg.StatsEnabled,
		StatsBufferSize:    cfg.StatsBufferSize,
		StatsDropThreshold: cfg.StatsDropThreshold,
		// Segment size lookup (for accurate byte tracking)
		SegmentSizeLookup: segmentScraper, // nil if not configured
		// FD mode is always enabled when stats are enabled
		Callbacks: ManagerCallbacks{
			OnClientStateChange: orch.onStateChange,
			OnClientStart:       orch.onStart,
			OnClientExit:        orch.onExit,
			OnClientRestart:     orch.onRestart,
		},
	}
	orch.clientManager = NewClientManager(managerCfg)

	return orch
}

// Run executes the load test. It blocks until completion or signal.
func (o *Orchestrator) Run(ctx context.Context) error {
	o.startTime = time.Now()

	// Run preflight checks
	if !o.config.SkipPreflight {
		result := preflight.RunAll(o.config.Clients, o.config.FFmpegPath)
		preflight.PrintResults(result)
		if !result.Passed {
			return fmt.Errorf("preflight checks failed (use --skip-preflight to override)")
		}
	}

	// Probe variants if needed
	if o.config.Variant == "highest" || o.config.Variant == "lowest" {
		o.logger.Info("probing_variants", "url", o.config.StreamURL)
		if err := o.runner.ProbeVariants(ctx); err != nil {
			if o.config.ProbeFailurePolicy == "fail" {
				return fmt.Errorf("variant probe failed: %w", err)
			}
			o.logger.Warn("variant_probe_failed", "error", err, "fallback", "first")
		} else {
			o.logger.Info("variant_selected", "program_id", o.runner.Config().ProgramID)
		}
	}

	// Start metrics server
	if err := o.metricsServer.Start(); err != nil {
		return fmt.Errorf("failed to start metrics server: %w", err)
	}

	// Setup signal handling
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	// Start ramp-up
	o.logger.Info("ramp_starting",
		"clients", o.config.Clients,
		"rate", o.config.RampRate,
		"estimated_duration", o.rampScheduler.EstimatedRampDuration(o.config.Clients).String(),
	)

	rampDone := make(chan struct{})
	go func() {
		defer close(rampDone)
		o.rampUp(ctx)
	}()

	// Start stats update loop for Prometheus
	if o.config.StatsEnabled {
		go o.statsUpdateLoop(ctx)
	}

	// Start origin metrics scraper if configured
	if o.originScraper != nil {
		go func() {
			o.originScraper.Run(ctx)
		}()
		o.logger.Info("origin_metrics_scraper_started",
			"node_exporter", o.config.OriginMetricsURL != "",
			"nginx_exporter", o.config.NginxMetricsURL != "",
		)
	}

	// Start segment scraper if configured (for accurate byte tracking)
	if o.segmentScraper != nil {
		// Start background scraper goroutine
		go o.segmentScraper.Run(ctx)

		// Wait for first scrape to complete (with timeout)
		// This ensures cache is populated before most clients start requesting segments
		if err := o.segmentScraper.WaitForFirstScrape(5 * time.Second); err != nil {
			o.logger.Warn("segment_scraper_cold_start",
				"error", err,
				"note", "throughput tracking may show zeros initially")
		} else {
			o.logger.Info("segment_scraper_started",
				"url", o.config.ResolveSegmentSizesURL(),
				"cache_size", o.segmentScraper.CacheSize(),
				"interval", o.config.SegmentSizesScrapeInterval,
				"jitter", o.config.SegmentSizesScrapeJitter,
			)
		}
	}

	// Setup duration timer if configured
	var durationTimer <-chan time.Time
	if o.config.Duration > 0 {
		durationTimer = time.After(o.config.Duration)
	}

	// Wait for completion signal
	// If TUI is enabled, run TUI instead of simple signal wait
	if o.config.TUIEnabled {
		o.runWithTUI(ctx, cancel, sigCh, durationTimer)
	} else {
		select {
		case sig := <-sigCh:
			o.logger.Info("received_signal", "signal", sig.String())
		case <-durationTimer:
			o.logger.Info("duration_elapsed", "duration", o.config.Duration.String())
		case <-ctx.Done():
			o.logger.Info("context_cancelled")
		}
	}

	// Cancel context to stop all clients
	cancel()

	// Graceful shutdown with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := o.clientManager.Shutdown(shutdownCtx); err != nil {
		o.logger.Warn("shutdown_incomplete", "error", err)
	}

	if err := o.metricsServer.Shutdown(shutdownCtx); err != nil {
		o.logger.Warn("metrics_server_shutdown_error", "error", err)
	}

	// Print exit summary
	o.printExitSummary()

	return nil
}

// rampUp starts clients at the configured rate.
func (o *Orchestrator) rampUp(ctx context.Context) {
	for i := 0; i < o.config.Clients; i++ {
		// Check for cancellation
		select {
		case <-ctx.Done():
			o.logger.Info("ramp_cancelled", "started", i, "target", o.config.Clients)
			return
		default:
		}

		// Wait according to ramp schedule
		if i > 0 { // Don't wait for first client
			if err := o.rampScheduler.Schedule(ctx, i); err != nil {
				return
			}
		}

		// Start client
		o.clientManager.StartClient(ctx, i)
		o.metrics.ClientStarted()

		// Update ramp progress
		o.metrics.SetRampProgress(float64(i+1) / float64(o.config.Clients))

		// Log progress periodically
		if (i+1)%10 == 0 || i == o.config.Clients-1 {
			o.logger.Info("ramp_progress",
				"started", i+1,
				"target", o.config.Clients,
				"active", o.clientManager.ActiveCount(),
			)
		}
	}

	o.logger.Info("ramp_complete",
		"clients", o.config.Clients,
		"active", o.clientManager.ActiveCount(),
	)
}

// Callback handlers

func (o *Orchestrator) onStateChange(clientID int, oldState, newState supervisor.State) {
	// Update active count metric
	o.metrics.SetActiveCount(o.clientManager.ActiveCount())
}

func (o *Orchestrator) onStart(clientID int, pid int) {
	if o.config.Verbose {
		o.logger.Debug("client_process_started", "client_id", clientID, "pid", pid)
	}
}

func (o *Orchestrator) onExit(clientID int, exitCode int, uptime time.Duration) {
	o.metrics.RecordExit(exitCode, uptime)
}

func (o *Orchestrator) onRestart(clientID int, attempt int, delay time.Duration) {
	o.metrics.ClientRestarted()

	if o.config.Verbose {
		o.logger.Debug("client_restart_scheduled",
			"client_id", clientID,
			"attempt", attempt,
			"delay", delay.String(),
		)
	}
}

// printExitSummary prints a summary of the load test run.
func (o *Orchestrator) printExitSummary() {
	metricsSummary := o.metrics.GenerateSummary()

	// Build SummaryConfig from metrics collector data
	cfg := stats.SummaryConfig{
		TargetClients: metricsSummary.TargetClients,
		Duration:      metricsSummary.Duration,
		MetricsAddr:   o.config.MetricsAddr,
		TotalStarts:   int(metricsSummary.TotalStarts),
		TotalRestarts: int(metricsSummary.TotalRestarts),
		UptimeP50:     metricsSummary.UptimeP50,
		UptimeP95:     metricsSummary.UptimeP95,
		UptimeP99:     metricsSummary.UptimeP99,
	}

	// Convert exit codes from int64 to int
	if len(metricsSummary.ExitCodes) > 0 {
		cfg.ExitCodes = make(map[int]int, len(metricsSummary.ExitCodes))
		for code, count := range metricsSummary.ExitCodes {
			cfg.ExitCodes[code] = int(count)
		}
	}

	// Get aggregated stats if stats collection is enabled
	var aggregatedStats *stats.AggregatedStats
	if o.config.StatsEnabled {
		aggregatedStats = o.GetAggregatedStats()
	}

	// Print the enhanced exit summary
	fmt.Print(stats.FormatExitSummary(aggregatedStats, cfg))
}


// ClientManager returns the client manager for external access.
func (o *Orchestrator) ClientManager() *ClientManager {
	return o.clientManager
}

// Runner returns the FFmpeg runner for external access.
func (o *Orchestrator) Runner() *process.FFmpegRunner {
	return o.runner
}

// Metrics returns the metrics collector for external access.
func (o *Orchestrator) Metrics() *metrics.Collector {
	return o.metrics
}

// GetAggregatedStats returns aggregated statistics across all clients.
// This is the primary method for getting comprehensive stats (Phase 5).
func (o *Orchestrator) GetAggregatedStats() *stats.AggregatedStats {
	return o.clientManager.GetAggregatedStats()
}

// GetStatsAggregator returns the stats aggregator for direct access.
func (o *Orchestrator) GetStatsAggregator() *stats.StatsAggregator {
	return o.clientManager.GetStatsAggregator()
}

// GetDebugStats returns aggregated debug statistics (HLS/HTTP/TCP layers).
// This is the primary method for the layered TUI dashboard (Phase 7).
func (o *Orchestrator) GetDebugStats() stats.DebugStatsAggregate {
	return o.clientManager.GetDebugStats()
}

// runWithTUI runs the orchestrator with the TUI dashboard.
func (o *Orchestrator) runWithTUI(ctx context.Context, cancel context.CancelFunc, sigCh <-chan os.Signal, durationTimer <-chan time.Time) {
	// Create TUI model
	tuiModel := tui.New(tui.Config{
		TargetClients:    o.config.Clients,
		StreamURL:        o.config.StreamURL,
		MetricsAddr:      o.config.MetricsAddr,
		StatsSource:      o,
		DebugStatsSource: o,
		OriginScraper:    o.originScraper,
	})

	// Create Bubble Tea program
	p := tea.NewProgram(tuiModel, tea.WithAltScreen())

	// Monitor for external quit signals in background
	go func() {
		select {
		case sig := <-sigCh:
			o.logger.Info("received_signal", "signal", sig.String())
			p.Send(tui.QuitMsg{})
		case <-durationTimer:
			o.logger.Info("duration_elapsed", "duration", o.config.Duration.String())
			p.Send(tui.QuitMsg{})
		case <-ctx.Done():
			o.logger.Info("context_cancelled")
			p.Send(tui.QuitMsg{})
		}
	}()

	// Run TUI (blocks until user quits or external signal)
	if _, err := p.Run(); err != nil {
		o.logger.Error("tui_error", "error", err)
	}

	// TUI has exited, trigger shutdown
	cancel()
}

// statsUpdateLoop periodically updates Prometheus metrics from aggregated stats.
func (o *Orchestrator) statsUpdateLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second) // Update every second
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			aggStats := o.GetAggregatedStats()
			if aggStats == nil {
				continue
			}

			// Get debug stats for segment throughput (from segment scraper)
			debugStats := o.GetDebugStats()

			// Convert stats.AggregatedStats to metrics.AggregatedStatsUpdate
			update := o.convertToMetricsUpdate(aggStats, &debugStats)
			o.metrics.RecordStats(update)

			// Also record latency samples to histogram
			// Note: T-Digest percentiles are approximate, so we use the P50 as a proxy
			// for histogram observation. The histogram buckets will provide more
			// accurate distribution data for Grafana.
		}
	}
}

// convertToMetricsUpdate converts stats.AggregatedStats to metrics.AggregatedStatsUpdate.
// debugStats is optional and provides segment throughput data from the segment scraper.
func (o *Orchestrator) convertToMetricsUpdate(aggStats *stats.AggregatedStats, debugStats *stats.DebugStatsAggregate) *metrics.AggregatedStatsUpdate {
	update := &metrics.AggregatedStatsUpdate{
		// Client counts
		ActiveClients:  aggStats.ActiveClients,
		StalledClients: aggStats.StalledClients,

		// Request totals
		TotalManifestReqs: aggStats.TotalManifestReqs,
		TotalSegmentReqs:  aggStats.TotalSegmentReqs,
		TotalInitReqs:     aggStats.TotalInitReqs,
		TotalUnknownReqs:  aggStats.TotalUnknownReqs,
		TotalBytes:        aggStats.TotalBytes,

		// Rates
		ManifestReqRate:       aggStats.InstantManifestRate,
		SegmentReqRate:        aggStats.InstantSegmentRate,
		ThroughputBytesPerSec: aggStats.InstantThroughputRate,

		// Errors
		TotalHTTPErrors:    aggStats.TotalHTTPErrors,
		TotalReconnections: aggStats.TotalReconnections,
		TotalTimeouts:      aggStats.TotalTimeouts,
		ErrorRate:          aggStats.ErrorRate,

		// Latency (inferred) - removed, use DebugStats for accurate latency
		// Setting to zero for backward compatibility with metrics package
		InferredLatencyP50: 0,
		InferredLatencyP95: 0,
		InferredLatencyP99: 0,
		InferredLatencyMax: 0,

		// Health
		ClientsAboveRealtime: aggStats.ClientsAboveRealtime,
		ClientsBelowRealtime: aggStats.ClientsBelowRealtime,
		AverageSpeed:         aggStats.AverageSpeed,
		ClientsWithHighDrift: aggStats.ClientsWithHighDrift,
		AverageDrift:         aggStats.AverageDrift,
		MaxDrift:             aggStats.MaxDrift,

		// Pipeline health
		TotalLinesDropped: aggStats.TotalLinesDropped,
		TotalLinesRead:    aggStats.TotalLinesRead,
		ClientsWithDrops:  aggStats.ClientsWithDrops,
		MetricsDegraded:   aggStats.MetricsDegraded,
		PeakDropRate:      aggStats.PeakDropRate,

		// Per-stream breakdown (approximation: assume 50/50 split)
		// The aggregator doesn't track per-stream, but Prometheus needs it
		ProgressLinesDropped: aggStats.TotalLinesDropped / 2,
		ProgressLinesRead:    aggStats.TotalLinesRead / 2,
		StderrLinesDropped:   aggStats.TotalLinesDropped - aggStats.TotalLinesDropped/2,
		StderrLinesRead:      aggStats.TotalLinesRead - aggStats.TotalLinesRead/2,

		// Note: Uptime percentiles are tracked separately by metrics.Collector
		// via RecordExit() calls, not from aggregated stats
	}

	// Add segment throughput data from debug stats (from segment scraper)
	if debugStats != nil {
		update.TotalSegmentBytes = debugStats.TotalSegmentBytes
		update.SegmentThroughputAvg1s = debugStats.SegmentThroughputAvg1s
		update.SegmentThroughputAvg30s = debugStats.SegmentThroughputAvg30s
		update.SegmentThroughputAvg60s = debugStats.SegmentThroughputAvg60s
		update.SegmentThroughputAvg300s = debugStats.SegmentThroughputAvg300s
	}

	// Add per-client stats if enabled
	if o.metrics.PerClientEnabled() && len(aggStats.PerClientSummaries) > 0 {
		update.PerClientStats = make([]metrics.PerClientStatsUpdate, len(aggStats.PerClientSummaries))
		for i, summary := range aggStats.PerClientSummaries {
			update.PerClientStats[i] = metrics.PerClientStatsUpdate{
				ClientID:     summary.ClientID,
				CurrentSpeed: summary.CurrentSpeed,
				CurrentDrift: summary.CurrentDrift,
				TotalBytes:   summary.TotalBytes,
			}
		}
	}

	return update
}
