// Package config provides configuration management for go-ffmpeg-hls-swarm.
package config

import "time"

// Config holds all configuration options for the orchestrator.
type Config struct {
	// Orchestration
	Clients    int           `json:"clients"`
	RampRate   int           `json:"ramp_rate"`
	RampJitter time.Duration `json:"ramp_jitter"`
	Duration   time.Duration `json:"duration"` // 0 = forever

	// FFmpeg
	FFmpegPath        string        `json:"ffmpeg_path"`
	StreamURL         string        `json:"stream_url"`
	Variant           string        `json:"variant"` // all, highest, lowest, first
	UserAgent         string        `json:"user_agent"`
	Timeout           time.Duration `json:"timeout"`
	Reconnect         bool          `json:"reconnect"`
	ReconnectDelayMax int           `json:"reconnect_delay_max"`
	SegMaxRetry       int           `json:"seg_max_retry"`
	LogLevel          string        `json:"ffmpeg_log_level"`

	// Network
	ResolveIP     string   `json:"resolve_ip"`
	DangerousMode bool     `json:"dangerous_mode"`
	NoCache       bool     `json:"no_cache"`
	Headers       []string `json:"headers"`

	// Health / Stall Detection
	TargetDuration time.Duration `json:"target_duration"`
	RestartOnStall bool          `json:"restart_on_stall"`

	// Observability
	MetricsAddr string `json:"metrics_addr"`
	Verbose     bool   `json:"verbose"`
	LogFormat   string `json:"log_format"` // json, text

	// Diagnostic modes
	PrintCmd      bool `json:"print_cmd"`
	Check         bool `json:"check"`
	SkipPreflight bool `json:"skip_preflight"`

	// Restart policy
	MaxRestarts     int           `json:"max_restarts"` // 0 = unlimited
	BackoffInitial  time.Duration `json:"backoff_initial"`
	BackoffMax      time.Duration `json:"backoff_max"`
	BackoffMultiply float64       `json:"backoff_multiply"`

	// Probe failure policy
	ProbeFailurePolicy string `json:"probe_failure_policy"` // "fail" or "fallback"

	// Stats collection (metrics enhancement)
	StatsEnabled       bool    `json:"stats_enabled"`        // Enable FFmpeg output parsing
	StatsLogLevel      string  `json:"stats_log_level"`      // FFmpeg loglevel: "verbose" or "debug"
	StatsBufferSize    int     `json:"stats_buffer_size"`    // Lines to buffer per client pipeline
	StatsDropThreshold float64 `json:"stats_drop_threshold"` // Degradation threshold (0.01 = 1%)
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		// Orchestration
		Clients:    10,
		RampRate:   5,
		RampJitter: 200 * time.Millisecond,
		Duration:   0, // Forever

		// FFmpeg
		FFmpegPath:        "ffmpeg",
		Variant:           "all",
		UserAgent:         "go-ffmpeg-hls-swarm/1.0",
		Timeout:           15 * time.Second,
		Reconnect:         true,
		ReconnectDelayMax: 5,
		SegMaxRetry:       3,
		LogLevel:          "info",

		// Health
		TargetDuration: 6 * time.Second,
		RestartOnStall: false,

		// Observability
		MetricsAddr: "0.0.0.0:17091",  // See docs/PORTS.md
		Verbose:     false,
		LogFormat:   "json",

		// Restart policy
		MaxRestarts:     0, // Unlimited
		BackoffInitial:  250 * time.Millisecond,
		BackoffMax:      5 * time.Second,
		BackoffMultiply: 1.7,

		// Probe
		ProbeFailurePolicy: "fallback",

		// Stats collection
		StatsEnabled:       true,
		StatsLogLevel:      "verbose",
		StatsBufferSize:    1000,
		StatsDropThreshold: 0.01, // 1% drop rate = degraded
	}
}
