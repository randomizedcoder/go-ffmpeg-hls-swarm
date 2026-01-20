package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/config"
	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/metrics"
	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/preflight"
	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/process"
	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/supervisor"
)

// Orchestrator coordinates all components for an HLS load test.
type Orchestrator struct {
	config *config.Config
	logger *slog.Logger

	runner        *process.FFmpegRunner
	clientManager *ClientManager
	rampScheduler *RampScheduler
	metrics       *metrics.Collector
	metricsServer *metrics.Server

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
	}
	runner := process.NewFFmpegRunner(ffmpegConfig)

	// Create ramp scheduler
	rampScheduler := NewRampScheduler(cfg.RampRate, cfg.RampJitter)

	// Create metrics
	collector := metrics.NewCollector(cfg.Clients)
	metricsServer := metrics.NewServer(cfg.MetricsAddr, logger)

	orch := &Orchestrator{
		config:        cfg,
		logger:        logger,
		runner:        runner,
		rampScheduler: rampScheduler,
		metrics:       collector,
		metricsServer: metricsServer,
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

	// Setup duration timer if configured
	var durationTimer <-chan time.Time
	if o.config.Duration > 0 {
		durationTimer = time.After(o.config.Duration)
	}

	// Wait for completion signal
	select {
	case sig := <-sigCh:
		o.logger.Info("received_signal", "signal", sig.String())
	case <-durationTimer:
		o.logger.Info("duration_elapsed", "duration", o.config.Duration.String())
	case <-ctx.Done():
		o.logger.Info("context_cancelled")
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
	summary := o.metrics.GenerateSummary()

	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════════════")
	fmt.Println("                        go-ffmpeg-hls-swarm Exit Summary")
	fmt.Println("═══════════════════════════════════════════════════════════════════")
	fmt.Printf("Run Duration:           %s\n", formatDuration(summary.Duration))
	fmt.Printf("Target Clients:         %d\n", summary.TargetClients)
	fmt.Printf("Peak Active Clients:    %d\n", summary.PeakActiveClients)
	fmt.Println()

	if summary.UptimeP50 > 0 || summary.UptimeP95 > 0 {
		fmt.Println("Uptime Distribution:")
		fmt.Printf("  P50 (median):         %s\n", formatDuration(summary.UptimeP50))
		fmt.Printf("  P95:                  %s\n", formatDuration(summary.UptimeP95))
		fmt.Printf("  P99:                  %s\n", formatDuration(summary.UptimeP99))
		fmt.Println()
	}

	fmt.Println("Lifecycle:")
	fmt.Printf("  Total Starts:         %d\n", summary.TotalStarts)
	fmt.Printf("  Total Restarts:       %d\n", summary.TotalRestarts)
	fmt.Println()

	if len(summary.ExitCodes) > 0 {
		fmt.Println("Exit Codes:")
		for code, count := range summary.ExitCodes {
			label := exitCodeLabel(code)
			fmt.Printf("  %3d %-16s %d\n", code, label, count)
		}
		fmt.Println()
	}

	fmt.Printf("Metrics endpoint was: http://%s/metrics\n", o.config.MetricsAddr)
	fmt.Println("═══════════════════════════════════════════════════════════════════")
}

// formatDuration formats a duration as HH:MM:SS.
func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

// exitCodeLabel returns a human-readable label for common exit codes.
func exitCodeLabel(code int) string {
	switch code {
	case 0:
		return "(clean)"
	case 1:
		return "(error)"
	case 137:
		return "(SIGKILL)"
	case 143:
		return "(SIGTERM)"
	default:
		return ""
	}
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
