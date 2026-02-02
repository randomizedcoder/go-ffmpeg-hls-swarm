package config

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// headerList is a custom flag type for repeatable -header flags.
type headerList []string

func (h *headerList) String() string {
	return strings.Join(*h, ", ")
}

func (h *headerList) Set(value string) error {
	*h = append(*h, value)
	return nil
}

// ParseFlags parses command-line flags and returns a Config.
// Returns an error if required arguments are missing or invalid.
func ParseFlags() (*Config, error) {
	cfg := DefaultConfig()
	var headers headerList

	// Custom usage message
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `go-ffmpeg-hls-swarm - HLS load testing with FFmpeg process orchestration

Usage:
  go-ffmpeg-hls-swarm [flags] <HLS_URL>

Orchestration Flags:
`)
		// Print flags by category
		printFlagCategory([]string{"clients", "ramp-rate", "ramp-jitter", "duration"})

		fmt.Fprintf(os.Stderr, "\nVariant Selection:\n")
		printFlagCategory([]string{"variant", "probe-failure-policy"})

		fmt.Fprintf(os.Stderr, "\nNetwork / Testing:\n")
		printFlagCategory([]string{"resolve", "no-cache", "header"})

		fmt.Fprintf(os.Stderr, "\nSafety & Diagnostics:\n")
		printFlagCategory([]string{"dangerous", "print-cmd", "check", "skip-preflight"})

		fmt.Fprintf(os.Stderr, "\nObservability:\n")
		printFlagCategory([]string{"metrics", "v", "log-format"})

		fmt.Fprintf(os.Stderr, "\nFFmpeg:\n")
		printFlagCategory([]string{"ffmpeg", "user-agent", "timeout", "reconnect", "reconnect-delay", "seg-retry"})

		fmt.Fprintf(os.Stderr, "\nHealth / Stall Detection:\n")
		printFlagCategory([]string{"target-duration", "restart-on-stall"})

		fmt.Fprintf(os.Stderr, "\nStats Collection:\n")
		printFlagCategory([]string{"stats", "stats-loglevel", "stats-buffer", "progress-socket", "ffmpeg-debug"})

		fmt.Fprintf(os.Stderr, "\nDashboard:\n")
		printFlagCategory([]string{"tui", "prom-client-metrics"})

		fmt.Fprintf(os.Stderr, "\nOrigin Metrics:\n")
		printFlagCategory([]string{"origin-metrics", "nginx-metrics", "origin-metrics-interval", "origin-metrics-window"})

		fmt.Fprintf(os.Stderr, "\nSegment Size Tracking:\n")
		printFlagCategory([]string{"segment-sizes-url", "segment-sizes-interval", "segment-sizes-jitter", "segment-cache-window"})

		fmt.Fprintf(os.Stderr, `
Flag Convention:
  Single-dash flags (-clients, -resolve) are normal options.
  Double-dash flags (--dangerous, --check) are safety gates or diagnostic modes.

Examples:
  # Quick smoke test
  go-ffmpeg-hls-swarm -clients 5 https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8

  # Stress test with cache bypass
  go-ffmpeg-hls-swarm -clients 100 -no-cache https://cdn.example.com/live/master.m3u8

  # Test specific server by IP
  go-ffmpeg-hls-swarm -clients 50 -resolve 192.168.1.100 --dangerous https://cdn.example.com/live/master.m3u8

`)
	}

	// Orchestration flags
	flag.IntVar(&cfg.Clients, "clients", cfg.Clients, "Number of concurrent clients")
	flag.IntVar(&cfg.RampRate, "ramp-rate", cfg.RampRate, "Clients to start per second")
	flag.DurationVar(&cfg.RampJitter, "ramp-jitter", cfg.RampJitter, "Random jitter per client start")
	flag.DurationVar(&cfg.Duration, "duration", cfg.Duration, "Run duration (0 = forever)")

	// Variant selection
	flag.StringVar(&cfg.Variant, "variant", cfg.Variant, `Bitrate selection: "all", "highest", "lowest", "first"`)
	flag.StringVar(&cfg.ProbeFailurePolicy, "probe-failure-policy", cfg.ProbeFailurePolicy, `Behavior if ffprobe fails: "fallback", "fail"`)

	// Network / Testing
	flag.StringVar(&cfg.ResolveIP, "resolve", cfg.ResolveIP, "Connect to this IP (requires --dangerous)")
	flag.BoolVar(&cfg.NoCache, "no-cache", cfg.NoCache, "Add no-cache headers (bypass CDN cache)")
	flag.Var(&headers, "header", "Add custom HTTP header (can repeat)")

	// Safety & Diagnostics (double-dash convention)
	flag.BoolVar(&cfg.DangerousMode, "dangerous", cfg.DangerousMode, "Required for -resolve (disables TLS verification)")
	flag.BoolVar(&cfg.PrintCmd, "print-cmd", cfg.PrintCmd, "Print FFmpeg command and exit")
	flag.BoolVar(&cfg.Check, "check", cfg.Check, "Validate config and run 1 client for 10 seconds")
	flag.BoolVar(&cfg.SkipPreflight, "skip-preflight", cfg.SkipPreflight, "Skip preflight checks")

	// Observability
	flag.StringVar(&cfg.MetricsAddr, "metrics", cfg.MetricsAddr, "Prometheus metrics address")
	flag.BoolVar(&cfg.Verbose, "v", cfg.Verbose, "Verbose logging")
	flag.StringVar(&cfg.LogFormat, "log-format", cfg.LogFormat, `Log format: "json" or "text"`)

	// FFmpeg
	flag.StringVar(&cfg.FFmpegPath, "ffmpeg", cfg.FFmpegPath, "Path to FFmpeg binary")
	flag.StringVar(&cfg.UserAgent, "user-agent", cfg.UserAgent, "HTTP User-Agent header")
	flag.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout, "Network read/write timeout")
	flag.BoolVar(&cfg.Reconnect, "reconnect", cfg.Reconnect, "Enable FFmpeg reconnect flags")
	flag.IntVar(&cfg.ReconnectDelayMax, "reconnect-delay", cfg.ReconnectDelayMax, "Max reconnect delay in seconds")
	flag.IntVar(&cfg.SegMaxRetry, "seg-retry", cfg.SegMaxRetry, "Segment download retry count")

	// Health / Stall Detection
	flag.DurationVar(&cfg.TargetDuration, "target-duration", cfg.TargetDuration, "Expected HLS segment duration for stall detection")
	flag.BoolVar(&cfg.RestartOnStall, "restart-on-stall", cfg.RestartOnStall, "Kill and restart stalled clients")

	// Stats Collection
	flag.BoolVar(&cfg.StatsEnabled, "stats", cfg.StatsEnabled, "Enable FFmpeg output parsing for detailed stats")
	flag.StringVar(&cfg.StatsLogLevel, "stats-loglevel", cfg.StatsLogLevel, `FFmpeg loglevel for stats: "verbose" or "debug"`)
	flag.IntVar(&cfg.StatsBufferSize, "stats-buffer", cfg.StatsBufferSize, "Lines to buffer per client (increase if seeing drops)")
	// Note: stats-drop-threshold is intentionally not documented (hidden advanced flag)
	flag.Float64Var(&cfg.StatsDropThreshold, "stats-drop-threshold", cfg.StatsDropThreshold, "")

	// Debug logging (FD mode is always enabled when stats are enabled)
	flag.BoolVar(&cfg.DebugLogging, "ffmpeg-debug", cfg.DebugLogging,
		"Enable FFmpeg -loglevel debug for detailed segment timing (safe with FD-based progress)")

	// TUI (Terminal User Interface)
	flag.BoolVar(&cfg.TUIEnabled, "tui", cfg.TUIEnabled, "Enable live terminal dashboard (default: true, use -tui=false to disable)")

	// Prometheus
	flag.BoolVar(&cfg.PromClientMetrics, "prom-client-metrics", cfg.PromClientMetrics,
		"Enable per-client Prometheus metrics (WARNING: high cardinality, use with <200 clients)")

	// Origin Metrics
	flag.StringVar(&cfg.OriginMetricsURL, "origin-metrics", cfg.OriginMetricsURL,
		"Origin node_exporter URL (e.g., http://10.177.0.10:9100/metrics). "+
			"If empty, origin metrics are disabled. Defaults to empty (disabled).")
	flag.StringVar(&cfg.NginxMetricsURL, "nginx-metrics", cfg.NginxMetricsURL,
		"Origin nginx_exporter URL (e.g., http://10.177.0.10:9113/metrics). "+
			"If empty, nginx metrics are disabled. Defaults to empty (disabled).")
	flag.DurationVar(&cfg.OriginMetricsInterval, "origin-metrics-interval", cfg.OriginMetricsInterval,
		"Interval for scraping origin metrics. Default: 2s. "+
			"Lower values increase load on origin server.")
	flag.DurationVar(&cfg.OriginMetricsWindow, "origin-metrics-window", cfg.OriginMetricsWindow,
		"Rolling window duration for network rate percentiles. "+
			"Default: 30s. "+
			"Range: 10s-300s (5 minutes). "+
			"Longer windows provide smoother percentiles but use more memory.")
	flag.StringVar(&cfg.OriginMetricsHost, "origin-metrics-host", cfg.OriginMetricsHost,
		"Origin server hostname/IP for metrics (e.g., 10.177.0.10). "+
			"If set, constructs URLs using default ports (9100 for node, 9113 for nginx). "+
			"Overrides -origin-metrics and -nginx-metrics if they are not explicitly set.")
	flag.IntVar(&cfg.OriginMetricsNodePort, "origin-metrics-node-port", cfg.OriginMetricsNodePort,
		"Node exporter port (used with -origin-metrics-host). "+
			"Default: 9100 (standard Prometheus node_exporter port).")
	flag.IntVar(&cfg.OriginMetricsNginxPort, "origin-metrics-nginx-port", cfg.OriginMetricsNginxPort,
		"Nginx exporter port (used with -origin-metrics-host). "+
			"Default: 9113 (standard nginx_exporter port).")

	// Segment Size Tracking
	flag.StringVar(&cfg.SegmentSizesURL, "segment-sizes-url", cfg.SegmentSizesURL,
		"URL for segment size JSON (e.g., http://origin:17080/files/json/). "+
			"If not set, auto-derives from -origin-metrics-host. "+
			"Enables accurate segment byte tracking and throughput calculation.")
	flag.DurationVar(&cfg.SegmentSizesScrapeInterval, "segment-sizes-interval", cfg.SegmentSizesScrapeInterval,
		"Interval for scraping segment sizes. Default: 1s.")
	flag.DurationVar(&cfg.SegmentSizesScrapeJitter, "segment-sizes-jitter", cfg.SegmentSizesScrapeJitter,
		"Jitter ± for scrape interval to prevent thundering herd. Default: 500ms.")
	flag.Int64Var(&cfg.SegmentCacheWindow, "segment-cache-window", cfg.SegmentCacheWindow,
		"Number of recent segments to keep in cache. "+
			"Keeps exactly N segments [highest-N+1, highest]. Default: 300.")

	// Parse
	flag.Parse()

	// Copy headers
	cfg.Headers = headers

	// Positional argument: stream URL
	args := flag.Args()
	if len(args) >= 1 {
		cfg.StreamURL = args[0]
	}

	return cfg, nil
}

// printFlagCategory prints flags matching the given names (helper for usage).
func printFlagCategory(names []string) {
	flag.VisitAll(func(f *flag.Flag) {
		for _, name := range names {
			if f.Name == name {
				fmt.Fprintf(os.Stderr, "  -%s %s\n    \t%s", f.Name, flagType(f), f.Usage)
				if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" && f.DefValue != "0s" && f.DefValue != "[]" {
					fmt.Fprintf(os.Stderr, " (default %s)", f.DefValue)
				}
				fmt.Fprintln(os.Stderr)
				return
			}
		}
	})
}

// flagType returns a type hint for the flag value.
func flagType(f *flag.Flag) string {
	// Infer type from default value format
	switch f.DefValue {
	case "true", "false":
		return ""
	}

	// Check if it looks like a duration
	if strings.HasSuffix(f.DefValue, "s") || strings.HasSuffix(f.DefValue, "m") || strings.HasSuffix(f.DefValue, "h") {
		return "duration"
	}

	// Check if numeric
	if _, err := fmt.Sscanf(f.DefValue, "%d", new(int)); err == nil {
		return "int"
	}

	return "string"
}
