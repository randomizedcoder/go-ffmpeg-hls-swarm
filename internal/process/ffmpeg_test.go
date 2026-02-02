package process

import (
	"context"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// Table-Driven Tests: DefaultFFmpegConfig
// =============================================================================

func TestDefaultFFmpegConfig(t *testing.T) {
	cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")

	tests := []struct {
		name string
		got  interface{}
		want interface{}
	}{
		{"BinaryPath", cfg.BinaryPath, "ffmpeg"},
		{"StreamURL", cfg.StreamURL, "http://example.com/stream.m3u8"},
		{"Variant", cfg.Variant, VariantAll},
		{"UserAgent", cfg.UserAgent, "go-ffmpeg-hls-swarm/1.0"},
		{"Timeout", cfg.Timeout, 15 * time.Second},
		{"Reconnect", cfg.Reconnect, true},
		{"ReconnectDelayMax", cfg.ReconnectDelayMax, 5},
		{"SegMaxRetry", cfg.SegMaxRetry, 3},
		{"LogLevel", cfg.LogLevel, "info"},
		{"ProgramID", cfg.ProgramID, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

// =============================================================================
// Table-Driven Tests: VariantSelection
// =============================================================================

func TestVariantSelection_Constants(t *testing.T) {
	tests := []struct {
		variant VariantSelection
		want    string
	}{
		{VariantAll, "all"},
		{VariantHighest, "highest"},
		{VariantLowest, "lowest"},
		{VariantFirst, "first"},
	}

	for _, tt := range tests {
		t.Run(string(tt.variant), func(t *testing.T) {
			if string(tt.variant) != tt.want {
				t.Errorf("VariantSelection = %q, want %q", tt.variant, tt.want)
			}
		})
	}
}

// =============================================================================
// Table-Driven Tests: buildArgs
// =============================================================================

func TestFFmpegRunner_buildArgs_Basic(t *testing.T) {
	cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
	runner := NewFFmpegRunner(cfg)
	args := runner.buildArgs()

	// Check required args are present
	requiredArgs := []string{
		"-hide_banner",
		"-nostdin",
		"-loglevel", "info",
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_on_network_error", "1",
		"-reconnect_delay_max", "5",
		"-user_agent", "go-ffmpeg-hls-swarm/1.0",
		"-seg_max_retry", "3",
		"-i", "http://example.com/stream.m3u8",
		"-map", "0",
		"-c", "copy",
		"-f", "null",
		"-",
	}

	argsStr := strings.Join(args, " ")
	for i := 0; i < len(requiredArgs); i++ {
		if !strings.Contains(argsStr, requiredArgs[i]) {
			t.Errorf("missing required arg: %s", requiredArgs[i])
		}
	}
}

func TestFFmpegRunner_buildArgs_StatsEnabled(t *testing.T) {
	tests := []struct {
		name          string
		statsEnabled  bool
		statsLogLevel string
		wantProgress  bool
		wantLogLevel  string // Expected level (with timestamped prefix when stats enabled)
	}{
		{
			name:          "stats disabled",
			statsEnabled:  false,
			statsLogLevel: "",
			wantProgress:  false,
			wantLogLevel:  "info", // No timestamp prefix when stats disabled
		},
		{
			name:          "stats enabled default loglevel",
			statsEnabled:  true,
			statsLogLevel: "",
			wantProgress:  true,
			wantLogLevel:  "repeat+level+datetime+verbose", // Timestamped, defaults to verbose
		},
		{
			name:          "stats enabled verbose",
			statsEnabled:  true,
			statsLogLevel: "verbose",
			wantProgress:  true,
			wantLogLevel:  "repeat+level+datetime+verbose", // Timestamped
		},
		{
			name:          "stats enabled debug",
			statsEnabled:  true,
			statsLogLevel: "debug",
			wantProgress:  true,
			wantLogLevel:  "repeat+level+datetime+debug", // Timestamped
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
			cfg.StatsEnabled = tt.statsEnabled
			cfg.StatsLogLevel = tt.statsLogLevel
			runner := NewFFmpegRunner(cfg)
			args := runner.buildArgs()
			argsStr := strings.Join(args, " ")

			// Check -progress pipe:1
			hasProgress := strings.Contains(argsStr, "-progress pipe:1")
			if hasProgress != tt.wantProgress {
				t.Errorf("progress flag: got %v, want %v", hasProgress, tt.wantProgress)
			}

			// Check -stats_period 1
			hasStatsPeriod := strings.Contains(argsStr, "-stats_period 1")
			if hasStatsPeriod != tt.wantProgress {
				t.Errorf("stats_period flag: got %v, want %v", hasStatsPeriod, tt.wantProgress)
			}

			// Check loglevel
			if !strings.Contains(argsStr, "-loglevel "+tt.wantLogLevel) {
				t.Errorf("loglevel: want %s in args: %s", tt.wantLogLevel, argsStr)
			}
		})
	}
}

func TestFFmpegRunner_buildArgs_Variants(t *testing.T) {
	tests := []struct {
		name      string
		variant   VariantSelection
		programID int
		wantMap   string
	}{
		{"all", VariantAll, -1, "-map 0"},
		{"first", VariantFirst, -1, "-map 0:v:0? -map 0:a:0?"},
		{"highest no probe", VariantHighest, -1, "-map 0:v:0? -map 0:a:0?"},
		{"highest with probe", VariantHighest, 3, "-map 0:p:3"},
		{"lowest no probe", VariantLowest, -1, "-map 0:v:0? -map 0:a:0?"},
		{"lowest with probe", VariantLowest, 5, "-map 0:p:5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
			cfg.Variant = tt.variant
			cfg.ProgramID = tt.programID
			runner := NewFFmpegRunner(cfg)
			args := runner.buildArgs()
			argsStr := strings.Join(args, " ")

			if !strings.Contains(argsStr, tt.wantMap) {
				t.Errorf("want %q in args: %s", tt.wantMap, argsStr)
			}
		})
	}
}

func TestFFmpegRunner_buildArgs_Reconnect(t *testing.T) {
	tests := []struct {
		name      string
		reconnect bool
		wantFlags bool
	}{
		{"reconnect enabled", true, true},
		{"reconnect disabled", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
			cfg.Reconnect = tt.reconnect
			runner := NewFFmpegRunner(cfg)
			args := runner.buildArgs()
			argsStr := strings.Join(args, " ")

			hasReconnect := strings.Contains(argsStr, "-reconnect 1")
			if hasReconnect != tt.wantFlags {
				t.Errorf("reconnect flags: got %v, want %v", hasReconnect, tt.wantFlags)
			}
		})
	}
}

func TestFFmpegRunner_buildArgs_DangerousMode(t *testing.T) {
	tests := []struct {
		name          string
		dangerousMode bool
		resolveIP     string
		wantTLSVerify bool
	}{
		{"safe mode", false, "", false},
		{"dangerous without IP", true, "", false},
		{"dangerous with IP", true, "192.168.1.1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultFFmpegConfig("https://example.com/stream.m3u8")
			cfg.DangerousMode = tt.dangerousMode
			cfg.ResolveIP = tt.resolveIP
			runner := NewFFmpegRunner(cfg)
			args := runner.buildArgs()
			argsStr := strings.Join(args, " ")

			hasTLSVerify := strings.Contains(argsStr, "-tls_verify 0")
			if hasTLSVerify != tt.wantTLSVerify {
				t.Errorf("tls_verify: got %v, want %v", hasTLSVerify, tt.wantTLSVerify)
			}
		})
	}
}

func TestFFmpegRunner_buildArgs_NoCache(t *testing.T) {
	tests := []struct {
		name        string
		noCache     bool
		wantHeaders bool
	}{
		{"cache enabled", false, false},
		{"cache disabled", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
			cfg.NoCache = tt.noCache
			runner := NewFFmpegRunner(cfg)
			args := runner.buildArgs()
			argsStr := strings.Join(args, " ")

			hasNoCache := strings.Contains(argsStr, "Cache-Control: no-cache")
			if hasNoCache != tt.wantHeaders {
				t.Errorf("no-cache headers: got %v, want %v", hasNoCache, tt.wantHeaders)
			}
		})
	}
}

func TestFFmpegRunner_buildArgs_CustomHeaders(t *testing.T) {
	cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
	cfg.Headers = []string{"X-Custom: value1", "X-Another: value2"}
	runner := NewFFmpegRunner(cfg)
	args := runner.buildArgs()
	argsStr := strings.Join(args, " ")

	if !strings.Contains(argsStr, "X-Custom: value1") {
		t.Error("missing custom header X-Custom")
	}
	if !strings.Contains(argsStr, "X-Another: value2") {
		t.Error("missing custom header X-Another")
	}
}

// =============================================================================
// Table-Driven Tests: effectiveURL
// =============================================================================

func TestFFmpegRunner_effectiveURL(t *testing.T) {
	tests := []struct {
		name      string
		streamURL string
		resolveIP string
		want      string
	}{
		{
			name:      "no override",
			streamURL: "http://example.com/stream.m3u8",
			resolveIP: "",
			want:      "http://example.com/stream.m3u8",
		},
		{
			name:      "IP override",
			streamURL: "http://example.com/stream.m3u8",
			resolveIP: "192.168.1.1",
			want:      "http://192.168.1.1/stream.m3u8",
		},
		{
			name:      "IP override with port",
			streamURL: "http://example.com:8080/stream.m3u8",
			resolveIP: "192.168.1.1",
			want:      "http://192.168.1.1:8080/stream.m3u8",
		},
		{
			name:      "HTTPS override",
			streamURL: "https://example.com/stream.m3u8",
			resolveIP: "10.0.0.1",
			want:      "https://10.0.0.1/stream.m3u8",
		},
		{
			name:      "relative URL (parsed as path)",
			streamURL: "not-a-valid-url",
			resolveIP: "192.168.1.1",
			want:      "//192.168.1.1/not-a-valid-url", // url.Parse treats this as path
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &FFmpegConfig{
				StreamURL: tt.streamURL,
				ResolveIP: tt.resolveIP,
			}
			runner := &FFmpegRunner{config: cfg}
			got := runner.effectiveURL()
			if got != tt.want {
				t.Errorf("effectiveURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// =============================================================================
// Table-Driven Tests: buildHeaders
// =============================================================================

func TestFFmpegRunner_buildHeaders(t *testing.T) {
	tests := []struct {
		name      string
		resolveIP string
		noCache   bool
		headers   []string
		wantLen   int
		wantHost  bool
		wantCache bool
	}{
		{
			name:      "no headers",
			resolveIP: "",
			noCache:   false,
			headers:   nil,
			wantLen:   0,
			wantHost:  false,
			wantCache: false,
		},
		{
			name:      "host header for IP override",
			resolveIP: "192.168.1.1",
			noCache:   false,
			headers:   nil,
			wantLen:   1,
			wantHost:  true,
			wantCache: false,
		},
		{
			name:      "cache headers",
			resolveIP: "",
			noCache:   true,
			headers:   nil,
			wantLen:   2,
			wantHost:  false,
			wantCache: true,
		},
		{
			name:      "custom headers",
			resolveIP: "",
			noCache:   false,
			headers:   []string{"X-Custom: value"},
			wantLen:   1,
			wantHost:  false,
			wantCache: false,
		},
		{
			name:      "all headers",
			resolveIP: "192.168.1.1",
			noCache:   true,
			headers:   []string{"X-Custom: value"},
			wantLen:   4, // Host + 2 cache + 1 custom
			wantHost:  true,
			wantCache: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &FFmpegConfig{
				StreamURL: "http://example.com/stream.m3u8",
				ResolveIP: tt.resolveIP,
				NoCache:   tt.noCache,
				Headers:   tt.headers,
			}
			runner := &FFmpegRunner{config: cfg}
			headers := runner.buildHeaders()

			if len(headers) != tt.wantLen {
				t.Errorf("len(headers) = %d, want %d", len(headers), tt.wantLen)
			}

			headersStr := strings.Join(headers, " ")
			hasHost := strings.Contains(headersStr, "Host:")
			if hasHost != tt.wantHost {
				t.Errorf("Host header: got %v, want %v", hasHost, tt.wantHost)
			}

			hasCache := strings.Contains(headersStr, "Cache-Control:")
			if hasCache != tt.wantCache {
				t.Errorf("Cache-Control header: got %v, want %v", hasCache, tt.wantCache)
			}
		})
	}
}

// =============================================================================
// Table-Driven Tests: mapArgs
// =============================================================================

func TestFFmpegRunner_mapArgs(t *testing.T) {
	tests := []struct {
		name      string
		variant   VariantSelection
		programID int
		want      []string
	}{
		{"all", VariantAll, -1, []string{"-map", "0"}},
		{"first", VariantFirst, -1, []string{"-map", "0:v:0?", "-map", "0:a:0?"}},
		{"highest no probe", VariantHighest, -1, []string{"-map", "0:v:0?", "-map", "0:a:0?"}},
		{"highest probed", VariantHighest, 2, []string{"-map", "0:p:2"}},
		{"lowest no probe", VariantLowest, -1, []string{"-map", "0:v:0?", "-map", "0:a:0?"}},
		{"lowest probed", VariantLowest, 0, []string{"-map", "0:p:0"}},
		{"unknown variant", VariantSelection("unknown"), -1, []string{"-map", "0"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &FFmpegConfig{
				Variant:   tt.variant,
				ProgramID: tt.programID,
			}
			runner := &FFmpegRunner{config: cfg}
			got := runner.mapArgs()

			if len(got) != len(tt.want) {
				t.Errorf("mapArgs() len = %d, want %d", len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("mapArgs()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// =============================================================================
// Tests: BuildCommand
// =============================================================================

func TestFFmpegRunner_BuildCommand(t *testing.T) {
	cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
	runner := NewFFmpegRunner(cfg)

	ctx := context.Background()
	cmd, err := runner.BuildCommand(ctx, 42)

	if err != nil {
		t.Fatalf("BuildCommand() error = %v", err)
	}
	if cmd == nil {
		t.Fatal("BuildCommand() returned nil cmd")
	}
	if cmd.Path == "" {
		t.Error("cmd.Path is empty")
	}
	if len(cmd.Args) == 0 {
		t.Error("cmd.Args is empty")
	}
}

// =============================================================================
// Tests: Name and Config accessors
// =============================================================================

func TestFFmpegRunner_Name(t *testing.T) {
	runner := NewFFmpegRunner(DefaultFFmpegConfig("http://example.com/stream.m3u8"))
	if runner.Name() != "ffmpeg" {
		t.Errorf("Name() = %q, want %q", runner.Name(), "ffmpeg")
	}
}

func TestFFmpegRunner_Config(t *testing.T) {
	cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
	runner := NewFFmpegRunner(cfg)
	if runner.Config() != cfg {
		t.Error("Config() did not return the same config")
	}
}

func TestFFmpegRunner_CommandString(t *testing.T) {
	cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
	runner := NewFFmpegRunner(cfg)
	cmdStr := runner.CommandString()

	if !strings.HasPrefix(cmdStr, "ffmpeg ") {
		t.Errorf("CommandString() should start with 'ffmpeg ', got: %s", cmdStr)
	}
	if !strings.Contains(cmdStr, "http://example.com/stream.m3u8") {
		t.Error("CommandString() should contain stream URL")
	}
}

// =============================================================================
// Socket Mode Tests
// =============================================================================

func TestFFmpegRunner_SetProgressFD(t *testing.T) {
	t.Run("fd_mode_uses_pipe_fd", func(t *testing.T) {
		cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
		cfg.StatsEnabled = true
		runner := NewFFmpegRunner(cfg)

		// Before setting FD, should fallback to pipe:1
		args := runner.buildArgs()
		cmdStr := strings.Join(args, " ")
		if !strings.Contains(cmdStr, "-progress pipe:1") {
			t.Errorf("Without FD set, should use pipe:1, got: %s", cmdStr)
		}

		// Set FD 3
		runner.SetProgressFD(3)

		// After setting FD, should use pipe:3
		args = runner.buildArgs()
		cmdStr = strings.Join(args, " ")
		if !strings.Contains(cmdStr, "-progress pipe:3") {
			t.Errorf("With FD 3, should use pipe:3, got: %s", cmdStr)
		}
		if strings.Contains(cmdStr, "pipe:1") {
			t.Error("With FD set, should not use pipe:1")
		}
	})

	t.Run("fd_cleared_falls_back_to_pipe1", func(t *testing.T) {
		cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
		cfg.StatsEnabled = true
		runner := NewFFmpegRunner(cfg)

		runner.SetProgressFD(3)
		runner.SetProgressFD(0) // Clear FD

		args := runner.buildArgs()
		cmdStr := strings.Join(args, " ")
		if !strings.Contains(cmdStr, "-progress pipe:1") {
			t.Errorf("After clearing FD, should use pipe:1, got: %s", cmdStr)
		}
	})

	t.Run("stats_disabled_no_progress_flag", func(t *testing.T) {
		cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
		cfg.StatsEnabled = false
		runner := NewFFmpegRunner(cfg)

		runner.SetProgressFD(3)

		args := runner.buildArgs()
		cmdStr := strings.Join(args, " ")
		if strings.Contains(cmdStr, "-progress") {
			t.Errorf("With stats disabled, should not have -progress flag, got: %s", cmdStr)
		}
	})
}

func TestFFmpegRunner_DebugLogging(t *testing.T) {
	t.Run("debug_logging_with_fd_mode", func(t *testing.T) {
		cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
		cfg.StatsEnabled = true
		cfg.DebugLogging = true
		runner := NewFFmpegRunner(cfg)
		runner.SetProgressFD(3) // FD mode always used when stats enabled

		// With debug logging, should use timestamped debug
		// Uses "repeat+level+datetime+debug" for accurate timing
		args := runner.buildArgs()
		cmdStr := strings.Join(args, " ")
		if !strings.Contains(cmdStr, "repeat+level+datetime+debug") {
			t.Errorf("With debug logging, should use -loglevel repeat+level+datetime+debug, got: %s", cmdStr)
		}
	})

	t.Run("debug_logging_disabled_uses_normal_level", func(t *testing.T) {
		cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
		cfg.StatsEnabled = true
		cfg.DebugLogging = false
		runner := NewFFmpegRunner(cfg)
		runner.SetProgressFD(3) // FD mode always used when stats enabled

		args := runner.buildArgs()
		cmdStr := strings.Join(args, " ")
		// Without debug logging, should use timestamped debug (default for stats)
		if !strings.Contains(cmdStr, "datetime+debug") {
			t.Error("With stats enabled, should use debug level by default for manifest tracking")
		}
		// Should still use timestamped logging when stats enabled
		if !strings.Contains(cmdStr, "repeat+level+datetime+verbose") {
			t.Errorf("Should use timestamped verbose level, got: %s", cmdStr)
		}
	})
}

func TestFFmpegRunner_PerClientUserAgent(t *testing.T) {
	t.Run("user_agent_includes_client_id", func(t *testing.T) {
		cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
		runner := NewFFmpegRunner(cfg)

		// BuildCommand sets the clientID
		_, err := runner.BuildCommand(context.Background(), 42)
		if err != nil {
			t.Fatalf("BuildCommand failed: %v", err)
		}

		args := runner.buildArgs()
		cmdStr := strings.Join(args, " ")
		if !strings.Contains(cmdStr, "go-ffmpeg-hls-swarm/1.0/client-42") {
			t.Errorf("User-Agent should include client ID, got: %s", cmdStr)
		}
	})

	t.Run("user_agent_zero_client_id", func(t *testing.T) {
		cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
		runner := NewFFmpegRunner(cfg)

		// Client ID 0 should use base user agent only
		_, err := runner.BuildCommand(context.Background(), 0)
		if err != nil {
			t.Fatalf("BuildCommand failed: %v", err)
		}

		args := runner.buildArgs()
		cmdStr := strings.Join(args, " ")
		if strings.Contains(cmdStr, "/client-0") {
			t.Error("Client ID 0 should not append /client-0")
		}
		if !strings.Contains(cmdStr, "-user_agent go-ffmpeg-hls-swarm/1.0") {
			t.Errorf("Should use base user agent, got: %s", cmdStr)
		}
	})

	t.Run("custom_user_agent_with_client_id", func(t *testing.T) {
		cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
		cfg.UserAgent = "MyApp/2.0"
		runner := NewFFmpegRunner(cfg)

		_, err := runner.BuildCommand(context.Background(), 100)
		if err != nil {
			t.Fatalf("BuildCommand failed: %v", err)
		}

		args := runner.buildArgs()
		cmdStr := strings.Join(args, " ")
		if !strings.Contains(cmdStr, "MyApp/2.0/client-100") {
			t.Errorf("Custom user agent should include client ID, got: %s", cmdStr)
		}
	})
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestFFmpegRunner_EmptyStreamURL(t *testing.T) {
	cfg := DefaultFFmpegConfig("")
	runner := NewFFmpegRunner(cfg)
	args := runner.buildArgs()

	// Should still build args, just with empty URL
	found := false
	for i, arg := range args {
		if arg == "-i" && i+1 < len(args) {
			found = true
			if args[i+1] != "" {
				t.Errorf("expected empty URL after -i, got %q", args[i+1])
			}
		}
	}
	if !found {
		t.Error("-i flag not found in args")
	}
}

func TestFFmpegRunner_Timeout(t *testing.T) {
	cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
	cfg.Timeout = 30 * time.Second
	runner := NewFFmpegRunner(cfg)
	args := runner.buildArgs()
	argsStr := strings.Join(args, " ")

	// 30 seconds = 30,000,000 microseconds
	if !strings.Contains(argsStr, "-rw_timeout 30000000") {
		t.Errorf("expected -rw_timeout 30000000, got: %s", argsStr)
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkFFmpegRunner_buildArgs(b *testing.B) {
	cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
	cfg.StatsEnabled = true
	cfg.StatsLogLevel = "verbose"
	cfg.NoCache = true
	cfg.Headers = []string{"X-Custom: value"}
	runner := NewFFmpegRunner(cfg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = runner.buildArgs()
	}
}

func BenchmarkFFmpegRunner_BuildCommand(b *testing.B) {
	cfg := DefaultFFmpegConfig("http://example.com/stream.m3u8")
	runner := NewFFmpegRunner(cfg)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = runner.BuildCommand(ctx, i)
	}
}
