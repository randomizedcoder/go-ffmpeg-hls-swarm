// Package main provides the go-ffmpeg-hls-swarm CLI entry point.
//
// go-ffmpeg-hls-swarm is a load testing tool that orchestrates a swarm of FFmpeg
// processes to stress-test HLS (HTTP Live Streaming) infrastructure.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/config"
	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/logging"
	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/orchestrator"
	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/process"
)

// version is set at build time via ldflags:
//
//	go build -ldflags "-X main.version=1.0.0" ./cmd/go-ffmpeg-hls-swarm
var version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	// Handle version flag early (before flag parsing)
	if len(os.Args) > 1 {
		arg := os.Args[1]
		if arg == "-version" || arg == "--version" || arg == "version" {
			fmt.Printf("go-ffmpeg-hls-swarm %s\n", version)
			return 0
		}
	}

	// Parse command-line flags
	cfg, err := config.ParseFlags()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		return 1
	}

	// Initialize logger
	// When TUI is enabled, suppress logs to avoid interfering with TUI rendering
	var logger *slog.Logger
	if cfg.TUIEnabled {
		// Use a null logger that discards all output
		logger = logging.NewLoggerWithWriter(io.Discard, "json", "info")
	} else {
		logger = logging.NewLogger(cfg.LogFormat, "info", cfg.Verbose)
	}
	logging.SetDefault(logger)

	// Validate configuration
	if err := config.Validate(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		return 1
	}

	// Apply --check mode modifications
	if cfg.Check {
		config.ApplyCheckMode(cfg)
		logger.Info("check_mode_enabled", "clients", cfg.Clients, "duration", cfg.Duration)
	}

	// Handle --print-cmd mode
	if cfg.PrintCmd {
		printFFmpegCommand(cfg)
		return 0
	}

	// Log startup
	logger.Info("starting",
		"version", version,
		"clients", cfg.Clients,
		"ramp_rate", cfg.RampRate,
		"stream_url", cfg.StreamURL,
		"variant", cfg.Variant,
		"metrics_addr", cfg.MetricsAddr,
	)

	// Print startup banner
	printBanner(cfg)

	// Create and run orchestrator
	orch := orchestrator.New(cfg, logger)
	if err := orch.Run(context.Background()); err != nil {
		logger.Error("orchestrator_failed", "error", err)
		return 1
	}

	return 0
}

// printBanner prints the startup banner.
func printBanner(cfg *config.Config) {
	fmt.Println()
	fmt.Println("╔═══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                     go-ffmpeg-hls-swarm                           ║")
	fmt.Println("║     HLS Load Testing with FFmpeg Process Orchestration            ║")
	fmt.Println("╚═══════════════════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("  Target:      %d clients at %d/sec\n", cfg.Clients, cfg.RampRate)
	fmt.Printf("  Stream:      %s\n", cfg.StreamURL)
	fmt.Printf("  Variant:     %s\n", cfg.Variant)
	fmt.Printf("  Metrics:     http://%s/metrics\n", cfg.MetricsAddr)
	if cfg.NoCache {
		fmt.Println("  Cache:       BYPASS (no-cache headers)")
	}
	if cfg.ResolveIP != "" {
		fmt.Printf("  Resolve:     %s (⚠️  TLS verification disabled)\n", cfg.ResolveIP)
	}
	fmt.Println()
	fmt.Println("Press Ctrl+C to stop.")
	fmt.Println()
}

// printFFmpegCommand prints the FFmpeg command that would be generated.
func printFFmpegCommand(cfg *config.Config) {
	// Create a runner to generate the command
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
	}

	runner := process.NewFFmpegRunner(ffmpegConfig)

	fmt.Println("# FFmpeg command that would be run for each client:")
	fmt.Println()
	fmt.Println(runner.CommandString())
}
