package process

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// VariantSelection specifies which HLS variants to download.
type VariantSelection string

const (
	// VariantAll downloads all quality levels simultaneously.
	// Maximum bandwidth usage but unrealistic viewer behavior.
	VariantAll VariantSelection = "all"

	// VariantHighest downloads only the highest bitrate variant.
	// Requires ffprobe to determine variant.
	VariantHighest VariantSelection = "highest"

	// VariantLowest downloads only the lowest bitrate variant.
	// Requires ffprobe to determine variant.
	VariantLowest VariantSelection = "lowest"

	// VariantFirst downloads the first variant in the playlist.
	// Fast startup, no ffprobe needed, but quality is unpredictable.
	VariantFirst VariantSelection = "first"
)

// FFmpegConfig holds configuration for FFmpeg process execution.
type FFmpegConfig struct {
	// BinaryPath is the path to the FFmpeg binary.
	BinaryPath string

	// StreamURL is the HLS stream URL to fetch.
	StreamURL string

	// Variant specifies which quality level(s) to download.
	Variant VariantSelection

	// UserAgent is the HTTP User-Agent header.
	UserAgent string

	// Timeout is the network read/write timeout.
	Timeout time.Duration

	// Reconnect enables FFmpeg's reconnection flags.
	Reconnect bool

	// ReconnectDelayMax is the maximum reconnection delay in seconds.
	ReconnectDelayMax int

	// SegMaxRetry is the segment download retry count.
	SegMaxRetry int

	// LogLevel is the FFmpeg log level (error, warning, info, verbose, debug).
	LogLevel string

	// ResolveIP overrides DNS resolution to connect to this IP.
	// Requires DangerousMode to be enabled.
	ResolveIP string

	// DangerousMode disables TLS verification. Required for ResolveIP.
	DangerousMode bool

	// NoCache adds cache-busting headers to bypass CDN caches.
	NoCache bool

	// Headers are additional HTTP headers to send.
	Headers []string

	// ProgramID is the HLS program ID for highest/lowest variant selection.
	// Set by ProbeVariants().
	ProgramID int

	// Stats collection
	StatsEnabled  bool   // Enable -progress pipe:1 output
	StatsLogLevel string // Override LogLevel when stats enabled ("verbose" or "debug")
}

// DefaultFFmpegConfig returns an FFmpegConfig with sensible defaults.
func DefaultFFmpegConfig(streamURL string) *FFmpegConfig {
	return &FFmpegConfig{
		BinaryPath:        "ffmpeg",
		StreamURL:         streamURL,
		Variant:           VariantAll,
		UserAgent:         "go-ffmpeg-hls-swarm/1.0",
		Timeout:           15 * time.Second,
		Reconnect:         true,
		ReconnectDelayMax: 5,
		SegMaxRetry:       3,
		LogLevel:          "info",
		ProgramID:         -1, // Not set
	}
}

// FFmpegRunner implements Runner for FFmpeg processes.
type FFmpegRunner struct {
	config *FFmpegConfig
}

// NewFFmpegRunner creates a new FFmpeg runner with the given configuration.
func NewFFmpegRunner(cfg *FFmpegConfig) *FFmpegRunner {
	return &FFmpegRunner{
		config: cfg,
	}
}

// Name returns "ffmpeg".
func (r *FFmpegRunner) Name() string {
	return "ffmpeg"
}

// BuildCommand creates an exec.Cmd for FFmpeg with all configured options.
func (r *FFmpegRunner) BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error) {
	args := r.buildArgs()
	cmd := exec.CommandContext(ctx, r.config.BinaryPath, args...)
	return cmd, nil
}

// buildArgs constructs the FFmpeg command-line arguments.
func (r *FFmpegRunner) buildArgs() []string {
	// Determine log level - use StatsLogLevel when stats enabled
	logLevel := r.config.LogLevel
	if r.config.StatsEnabled && r.config.StatsLogLevel != "" {
		logLevel = r.config.StatsLogLevel
	}

	args := []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel", logLevel,
	}

	// Progress output to stdout (key=value format) for stats parsing
	if r.config.StatsEnabled {
		args = append(args, "-progress", "pipe:1")
		// Also add -stats_period for more frequent updates (every 1 second)
		args = append(args, "-stats_period", "1")
	}

	// TLS verification (must be early, before input options)
	if r.config.DangerousMode && r.config.ResolveIP != "" {
		args = append(args, "-tls_verify", "0")
	}

	// Reconnection flags (must come before -i)
	if r.config.Reconnect {
		args = append(args,
			"-reconnect", "1",
			"-reconnect_streamed", "1",
			"-reconnect_on_network_error", "1",
			"-reconnect_delay_max", strconv.Itoa(r.config.ReconnectDelayMax),
		)
	}

	// Network timeout (in microseconds)
	args = append(args, "-rw_timeout", strconv.FormatInt(r.config.Timeout.Microseconds(), 10))

	// User agent
	args = append(args, "-user_agent", r.config.UserAgent)

	// HTTP headers
	headers := r.buildHeaders()
	if len(headers) > 0 {
		args = append(args, "-headers", strings.Join(headers, "\r\n")+"\r\n")
	}

	// Segment retry
	args = append(args, "-seg_max_retry", strconv.Itoa(r.config.SegMaxRetry))

	// Input URL (potentially rewritten for IP override)
	inputURL := r.effectiveURL()
	args = append(args, "-i", inputURL)

	// Output mapping based on variant selection
	args = append(args, r.mapArgs()...)

	// Output: copy streams to null (no decode)
	args = append(args, "-c", "copy", "-f", "null", "-")

	return args
}

// buildHeaders constructs HTTP headers based on configuration.
func (r *FFmpegRunner) buildHeaders() []string {
	var headers []string

	// Host header for IP override (preserve original hostname)
	if r.config.ResolveIP != "" {
		u, err := url.Parse(r.config.StreamURL)
		if err == nil {
			headers = append(headers, fmt.Sprintf("Host: %s", u.Host))
		}
	}

	// Cache bypass headers
	if r.config.NoCache {
		headers = append(headers,
			"Cache-Control: no-cache, no-store, must-revalidate",
			"Pragma: no-cache",
		)
	}

	// Custom headers
	headers = append(headers, r.config.Headers...)

	return headers
}

// effectiveURL returns the URL to use, potentially with IP override.
func (r *FFmpegRunner) effectiveURL() string {
	if r.config.ResolveIP == "" {
		return r.config.StreamURL
	}

	// Replace hostname with IP address
	u, err := url.Parse(r.config.StreamURL)
	if err != nil {
		return r.config.StreamURL
	}

	// Preserve port if specified
	port := u.Port()
	if port != "" {
		u.Host = r.config.ResolveIP + ":" + port
	} else {
		u.Host = r.config.ResolveIP
	}

	return u.String()
}

// mapArgs returns the -map arguments based on variant selection.
func (r *FFmpegRunner) mapArgs() []string {
	switch r.config.Variant {
	case VariantAll:
		// Map all streams
		return []string{"-map", "0"}

	case VariantFirst:
		// Map first video and first audio (if present)
		return []string{"-map", "0:v:0?", "-map", "0:a:0?"}

	case VariantHighest, VariantLowest:
		// Map specific program (determined by ffprobe)
		if r.config.ProgramID >= 0 {
			return []string{"-map", fmt.Sprintf("0:p:%d", r.config.ProgramID)}
		}
		// Fallback to first variant if not probed
		return []string{"-map", "0:v:0?", "-map", "0:a:0?"}

	default:
		return []string{"-map", "0"}
	}
}

// Config returns the FFmpeg configuration.
func (r *FFmpegRunner) Config() *FFmpegConfig {
	return r.config
}

// CommandString returns the command that would be executed (for debugging).
func (r *FFmpegRunner) CommandString() string {
	args := r.buildArgs()
	return r.config.BinaryPath + " " + strings.Join(args, " ")
}
