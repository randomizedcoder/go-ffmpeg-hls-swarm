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

	// UserAgent is the HTTP User-Agent header base.
	// Client ID will be appended for per-client identification.
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
	StatsEnabled  bool   // Enable -progress output
	StatsLogLevel string // Override LogLevel when stats enabled ("verbose" or "debug")

	// DebugLogging enables -loglevel debug for detailed segment timing.
	// Only safe when socket mode is enabled (otherwise debug output
	// would corrupt progress parsing on stdout).
	DebugLogging bool
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

	// progressFD is the file descriptor number for progress output.
	// Set by SetProgressFD() when stats are enabled.
	// When set, uses "-progress pipe:N" where N is the FD number.
	// FD 3 is the first ExtraFiles entry, FD 4 is the second, etc.
	progressFD int

	// clientID is set during BuildCommand for per-client User-Agent.
	// This enables correlation with origin logs and packet captures.
	clientID int
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

// SetProgressFD sets the file descriptor number for progress output.
// When set, FFmpeg will use "-progress pipe:N" where N is the FD number.
// FD 3 is the first ExtraFiles entry, FD 4 is the second, etc.
// This provides cleaner separation from stderr without creating files.
//
// Called by Supervisor before BuildCommand() when stats are enabled.
func (r *FFmpegRunner) SetProgressFD(fd int) {
	r.progressFD = fd
}

// BuildCommand creates an exec.Cmd for FFmpeg with all configured options.
func (r *FFmpegRunner) BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error) {
	r.clientID = clientID // Capture for per-client User-Agent
	args := r.buildArgs()
	cmd := exec.CommandContext(ctx, r.config.BinaryPath, args...)
	return cmd, nil
}

// buildArgs constructs the FFmpeg command-line arguments.
func (r *FFmpegRunner) buildArgs() []string {
	// Determine log level
	logLevel := r.config.LogLevel

	// Use StatsLogLevel when stats enabled
	if r.config.StatsEnabled && r.config.StatsLogLevel != "" {
		logLevel = r.config.StatsLogLevel
	}

	// When stats are enabled, ALWAYS use timestamped logging for accurate metrics.
	// Format: "repeat+level+datetime+<level>" provides:
	//   - repeat: Don't collapse repeated messages
	//   - level: Add [debug]/[verbose]/[info] tags for filtering
	//   - datetime: Millisecond-precision timestamps (YYYY-MM-DD HH:MM:SS.mmm)
	//
	// This is critical because timestamps come directly from FFmpeg, not from
	// when Go processes the lines. Even if logs back up in channels, we get
	// accurate timing for TCP connects, segment downloads, and playlist refreshes.
	if r.config.StatsEnabled {
		// Determine base level for timestamped logging
		// Default to "debug" to capture manifest refreshes ([hls @ ...] Opening '...m3u8')
		// which are logged at DEBUG level. Verbose only captures segment requests.
		baseLevel := "debug" // Default to debug for stats (required for manifest tracking)
		if r.config.DebugLogging {
			// Full debug when enabled (safe - progress is on separate FD)
			baseLevel = "debug"
		} else if r.config.StatsLogLevel != "" {
			// Use configured stats level (allows override to verbose if needed)
			baseLevel = r.config.StatsLogLevel
		}
		logLevel = "repeat+level+datetime+" + baseLevel
	}

	args := []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel", logLevel,
	}

	// Progress output for stats parsing
	// Always uses FD mode (pipe:3) when stats are enabled for clean separation from stderr
	if r.config.StatsEnabled {
		if r.progressFD > 0 {
			// FD mode: use file descriptor for cleaner separation from stderr
			// No filesystem files needed, completely ephemeral
			args = append(args, "-progress", fmt.Sprintf("pipe:%d", r.progressFD))
		} else {
			// Fallback to stdout if FD not set (should not happen in normal operation)
			args = append(args, "-progress", "pipe:1")
		}
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

	// User agent with per-client identification
	// Format: "go-ffmpeg-hls-swarm/1.0/client-42"
	// Enables correlation with origin logs and packet captures:
	// - tcpdump: tcpdump -A | grep "client-42"
	// - Wireshark: http.user_agent contains "client-42"
	// - Nginx: grep "client-42" access.log
	userAgent := r.config.UserAgent
	if r.clientID > 0 {
		userAgent = fmt.Sprintf("%s/client-%d", r.config.UserAgent, r.clientID)
	}
	args = append(args, "-user_agent", userAgent)

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
