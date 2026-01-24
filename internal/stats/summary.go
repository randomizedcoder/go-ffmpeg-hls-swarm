// Package stats provides per-client and aggregated statistics for HLS load testing.
//
// This file implements the exit summary formatter which displays comprehensive
// statistics at program exit.
package stats

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// SummaryConfig holds configuration for summary formatting.
type SummaryConfig struct {
	// TargetClients is the number of clients that were requested
	TargetClients int

	// Duration is the total run duration
	Duration time.Duration

	// MetricsAddr is the Prometheus metrics endpoint address
	MetricsAddr string

	// ShowPerClientStats enables detailed per-client statistics
	ShowPerClientStats bool

	// ExitCodes is a map of exit codes to counts (from metrics.Collector)
	ExitCodes map[int]int

	// TotalStarts is the total number of client starts
	TotalStarts int

	// TotalRestarts is the total number of client restarts
	TotalRestarts int

	// UptimeP50, UptimeP95, UptimeP99 are uptime percentiles
	UptimeP50 time.Duration
	UptimeP95 time.Duration
	UptimeP99 time.Duration
}

// FormatExitSummary formats aggregated stats for display at program exit.
//
// The summary includes:
// - Metrics degradation warning (if applicable)
// - Run information
// - Request statistics with rates
// - Inferred latency percentiles
// - Playback health metrics
// - Error statistics
// - Footnotes with diagnostic information
func FormatExitSummary(stats *AggregatedStats, cfg SummaryConfig) string {
	if stats == nil {
		return formatBasicSummary(cfg)
	}

	var b strings.Builder

	b.WriteString("\n")
	b.WriteString("═══════════════════════════════════════════════════════════════════════════════\n")
	b.WriteString("                        go-ffmpeg-hls-swarm Exit Summary\n")
	b.WriteString("═══════════════════════════════════════════════════════════════════════════════\n\n")

	// Metrics degradation warning (lossy-by-design feature)
	if stats.MetricsDegraded {
		b.WriteString("⚠️  METRICS DEGRADED: Parsing could not keep up with FFmpeg output\n")
		fmt.Fprintf(&b, "    Lines dropped: %s across %d clients\n",
			FormatNumber(stats.TotalLinesDropped),
			stats.ClientsWithDrops,
		)
		b.WriteString("    Consider: --stats-buffer 2000 or fewer clients for accurate metrics\n\n")
	}

	// Run info
	fmt.Fprintf(&b, "Run Duration:           %s\n", FormatDuration(cfg.Duration))
	fmt.Fprintf(&b, "Target Clients:         %d\n", cfg.TargetClients)
	fmt.Fprintf(&b, "Peak Active Clients:    %d\n\n", stats.TotalClients)

	// Request statistics
	b.WriteString("───────────────────────────────────────────────────────────────────────────────\n")
	b.WriteString("                              Request Statistics\n")
	b.WriteString("───────────────────────────────────────────────────────────────────────────────\n\n")

	perClient := int64(1)
	if stats.TotalClients > 0 {
		perClient = int64(stats.TotalClients)
	}

	fmt.Fprintf(&b, "  %-20s %12s %12s %12s\n", "Request Type", "Total", "Rate (/sec)", "Per Client")
	b.WriteString("  " + strings.Repeat("─", 58) + "\n")
	fmt.Fprintf(&b, "  %-20s %12s %12.1f %12d\n",
		"Manifest (.m3u8)",
		FormatNumber(stats.TotalManifestReqs),
		stats.ManifestReqRate,
		stats.TotalManifestReqs/perClient,
	)
	fmt.Fprintf(&b, "  %-20s %12s %12.1f %12d\n",
		"Segments (.ts)",
		FormatNumber(stats.TotalSegmentReqs),
		stats.SegmentReqRate,
		stats.TotalSegmentReqs/perClient,
	)
	if stats.TotalInitReqs > 0 {
		fmt.Fprintf(&b, "  %-20s %12s %12s %12d\n",
			"Init segments",
			FormatNumber(stats.TotalInitReqs),
			"-",
			stats.TotalInitReqs/perClient,
		)
	}
	fmt.Fprintf(&b, "\n  Total Bytes:          %s  (%s/s)\n\n",
		FormatBytes(stats.TotalBytes),
		FormatBytes(int64(stats.ThroughputBytesPerSec)),
	)

	// Note: Latency metrics removed - use DebugStats.SegmentWallTime* for accurate latency
	// from FFmpeg timestamps. See docs/REMOVE_INFERRED_LATENCY_ANALYSIS.md

	// Playback health
	b.WriteString("───────────────────────────────────────────────────────────────────────────────\n")
	b.WriteString("                              Playback Health\n")
	b.WriteString("───────────────────────────────────────────────────────────────────────────────\n\n")

	total := stats.ClientsAboveRealtime + stats.ClientsBelowRealtime
	if total > 0 {
		fmt.Fprintf(&b, "  >= 1.0x (healthy):    %d (%d%%)\n",
			stats.ClientsAboveRealtime,
			stats.ClientsAboveRealtime*100/total,
		)
		fmt.Fprintf(&b, "  < 1.0x (buffering):   %d (%d%%)\n",
			stats.ClientsBelowRealtime,
			stats.ClientsBelowRealtime*100/total,
		)
	}
	fmt.Fprintf(&b, "  Average Speed:        %.2fx\n", stats.AverageSpeed)
	fmt.Fprintf(&b, "  Stalled Clients:      %d\n", stats.StalledClients)

	// Drift
	if stats.MaxDrift > 0 {
		fmt.Fprintf(&b, "  Average Drift:        %s\n", FormatMs(stats.AverageDrift))
		fmt.Fprintf(&b, "  Max Drift:            %s\n", FormatMs(stats.MaxDrift))
		if stats.ClientsWithHighDrift > 0 {
			fmt.Fprintf(&b, "  High Drift Clients:   %d (>5s)\n", stats.ClientsWithHighDrift)
		}
	}
	b.WriteString("\n")

	// Uptime distribution (from metrics.Collector)
	if cfg.UptimeP50 > 0 || cfg.UptimeP95 > 0 {
		b.WriteString("───────────────────────────────────────────────────────────────────────────────\n")
		b.WriteString("                            Uptime Distribution\n")
		b.WriteString("───────────────────────────────────────────────────────────────────────────────\n\n")

		fmt.Fprintf(&b, "  P50 (median):         %s\n", FormatDuration(cfg.UptimeP50))
		fmt.Fprintf(&b, "  P95:                  %s\n", FormatDuration(cfg.UptimeP95))
		fmt.Fprintf(&b, "  P99:                  %s\n", FormatDuration(cfg.UptimeP99))
		b.WriteString("\n")
	}

	// Lifecycle
	if cfg.TotalStarts > 0 || cfg.TotalRestarts > 0 {
		b.WriteString("───────────────────────────────────────────────────────────────────────────────\n")
		b.WriteString("                                Lifecycle\n")
		b.WriteString("───────────────────────────────────────────────────────────────────────────────\n\n")

		fmt.Fprintf(&b, "  Total Starts:         %d\n", cfg.TotalStarts)
		fmt.Fprintf(&b, "  Total Restarts:       %d\n", cfg.TotalRestarts)
		b.WriteString("\n")
	}

	// Errors
	hasErrors := len(stats.TotalHTTPErrors) > 0 || stats.TotalTimeouts > 0 || stats.TotalReconnections > 0
	if hasErrors {
		b.WriteString("───────────────────────────────────────────────────────────────────────────────\n")
		b.WriteString("                                  Errors\n")
		b.WriteString("───────────────────────────────────────────────────────────────────────────────\n\n")

		// Sort HTTP error codes for consistent output
		codes := make([]int, 0, len(stats.TotalHTTPErrors))
		for code := range stats.TotalHTTPErrors {
			codes = append(codes, code)
		}
		sort.Ints(codes)

		for _, code := range codes {
			count := stats.TotalHTTPErrors[code]
			if code == 0 {
				// Code 0 is the sentinel for "other" (non-standard HTTP error codes)
				fmt.Fprintf(&b, "  HTTP Other:            %d\n", count)
			} else {
				fmt.Fprintf(&b, "  HTTP %d:               %d\n", code, count)
			}
		}
		if stats.TotalTimeouts > 0 {
			fmt.Fprintf(&b, "  Timeouts:             %d\n", stats.TotalTimeouts)
		}
		if stats.TotalReconnections > 0 {
			fmt.Fprintf(&b, "  Reconnections:        %d\n", stats.TotalReconnections)
		}
		fmt.Fprintf(&b, "  Error Rate:           %.4f%%\n\n", stats.ErrorRate*100)
	}

	// Exit codes (from metrics.Collector)
	if len(cfg.ExitCodes) > 0 {
		b.WriteString("───────────────────────────────────────────────────────────────────────────────\n")
		b.WriteString("                                Exit Codes\n")
		b.WriteString("───────────────────────────────────────────────────────────────────────────────\n\n")

		// Sort exit codes for consistent output
		codes := make([]int, 0, len(cfg.ExitCodes))
		for code := range cfg.ExitCodes {
			codes = append(codes, code)
		}
		sort.Ints(codes)

		for _, code := range codes {
			count := cfg.ExitCodes[code]
			label := exitCodeLabel(code)
			fmt.Fprintf(&b, "  %3d %-16s %d\n", code, label, count)
		}
		b.WriteString("\n")
	}

	// Footnotes (diagnostic information)
	footnotes := renderFootnotes(stats)
	if footnotes != "" {
		b.WriteString(footnotes)
	}

	// Metrics endpoint
	if cfg.MetricsAddr != "" {
		fmt.Fprintf(&b, "Metrics endpoint was: http://%s/metrics\n", cfg.MetricsAddr)
	}

	b.WriteString("═══════════════════════════════════════════════════════════════════════════════\n")

	return b.String()
}

// formatBasicSummary formats a basic summary when stats are not available.
func formatBasicSummary(cfg SummaryConfig) string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString("═══════════════════════════════════════════════════════════════════════════════\n")
	b.WriteString("                        go-ffmpeg-hls-swarm Exit Summary\n")
	b.WriteString("═══════════════════════════════════════════════════════════════════════════════\n\n")

	fmt.Fprintf(&b, "Run Duration:           %s\n", FormatDuration(cfg.Duration))
	fmt.Fprintf(&b, "Target Clients:         %d\n\n", cfg.TargetClients)

	b.WriteString("(Stats collection was disabled - use --stats to enable detailed metrics)\n\n")

	if cfg.MetricsAddr != "" {
		fmt.Fprintf(&b, "Metrics endpoint was: http://%s/metrics\n", cfg.MetricsAddr)
	}

	b.WriteString("═══════════════════════════════════════════════════════════════════════════════\n")

	return b.String()
}

// renderFootnotes adds diagnostic info that doesn't belong in main metrics.
func renderFootnotes(stats *AggregatedStats) string {
	var footnotes []string

	// Note: Latency footnote removed - use DebugStats for accurate latency from FFmpeg timestamps

	// Only include unknown URLs if any were observed
	if stats.TotalUnknownReqs > 0 {
		footnotes = append(footnotes, fmt.Sprintf(
			"[2] Unknown URL requests: %d (may indicate byte-range playlists, signed URLs)",
			stats.TotalUnknownReqs))
	}

	// Include peak drop rate if any drops occurred
	if stats.PeakDropRate > 0 {
		footnotes = append(footnotes, fmt.Sprintf(
			"[3] Peak metrics drop rate: %.1f%%",
			stats.PeakDropRate*100))
	}

	if len(footnotes) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("───────────────────────────────────────────────────────────────────────────────\n")
	b.WriteString("                                 Footnotes\n")
	b.WriteString("───────────────────────────────────────────────────────────────────────────────\n\n")
	for _, fn := range footnotes {
		fmt.Fprintf(&b, "  %s\n", fn)
	}
	b.WriteString("\n")
	return b.String()
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

// =============================================================================
// Formatting Helper Functions (exported for reuse)
// =============================================================================

// FormatDuration formats a duration as HH:MM:SS.
func FormatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

// FormatNumber formats a number with K/M suffixes for readability.
func FormatNumber(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

// FormatBytes formats bytes with KB/MB/GB suffixes.
func FormatBytes(n int64) string {
	if n >= 1_000_000_000 {
		return fmt.Sprintf("%.2f GB", float64(n)/1_000_000_000)
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.2f MB", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.2f KB", float64(n)/1_000)
	}
	return fmt.Sprintf("%d B", n)
}

// FormatMs formats a duration as milliseconds.
func FormatMs(d time.Duration) string {
	ms := d.Milliseconds()
	if ms == 0 && d > 0 {
		// Sub-millisecond, show microseconds
		return fmt.Sprintf("%d µs", d.Microseconds())
	}
	return fmt.Sprintf("%d ms", ms)
}

// FormatRate formats a rate with appropriate precision.
func FormatRate(rate float64) string {
	if rate >= 1000 {
		return fmt.Sprintf("%.1fK/s", rate/1000)
	}
	if rate >= 1 {
		return fmt.Sprintf("%.1f/s", rate)
	}
	return fmt.Sprintf("%.2f/s", rate)
}
