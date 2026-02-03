package config

import (
	"flag"
	"strings"
	"testing"
	"time"
)

// Test headerList type
func TestHeaderList_String(t *testing.T) {
	testCases := []struct {
		input    headerList
		expected string
	}{
		{headerList{}, ""},
		{headerList{"X-Test: value"}, "X-Test: value"},
		{headerList{"X-Test: value", "X-Other: foo"}, "X-Test: value, X-Other: foo"},
	}

	for _, tc := range testCases {
		result := tc.input.String()
		if result != tc.expected {
			t.Errorf("String() = %q, want %q", result, tc.expected)
		}
	}
}

func TestHeaderList_Set(t *testing.T) {
	var h headerList

	// Set first value
	err := h.Set("X-Test: value")
	if err != nil {
		t.Errorf("Set returned error: %v", err)
	}
	if len(h) != 1 || h[0] != "X-Test: value" {
		t.Errorf("After first Set: %v", h)
	}

	// Set second value (should append)
	err = h.Set("X-Other: foo")
	if err != nil {
		t.Errorf("Set returned error: %v", err)
	}
	if len(h) != 2 || h[1] != "X-Other: foo" {
		t.Errorf("After second Set: %v", h)
	}

	// Empty string should still work
	err = h.Set("")
	if err != nil {
		t.Errorf("Set with empty string returned error: %v", err)
	}
	if len(h) != 3 {
		t.Errorf("Empty string should still be appended: %v", h)
	}
}

func TestFlagType(t *testing.T) {
	testCases := []struct {
		name     string
		defValue string
		expected string
	}{
		{"bool true", "true", ""},
		{"bool false", "false", ""},
		{"int", "42", "int"},
		{"string", "hello", "string"},
		{"duration seconds", "5s", "duration"},
		{"duration minutes", "5m", "duration"},
		{"duration hours", "1h", "duration"},
		{"float", "3.14", "int"}, // Sscanf parses "3" then stops at decimal
		{"empty", "", "string"},
		{"zero", "0", "int"},
		{"negative int", "-1", "int"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a mock flag
			f := &flag.Flag{
				Name:     "test",
				DefValue: tc.defValue,
			}
			result := flagType(f)
			if result != tc.expected {
				t.Errorf("flagType(%q) = %q, want %q", tc.defValue, result, tc.expected)
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	// Verify critical defaults
	if cfg.Clients != 10 {
		t.Errorf("Clients = %d, want 10", cfg.Clients)
	}
	if cfg.RampRate != 5 {
		t.Errorf("RampRate = %d, want 5", cfg.RampRate)
	}
	if cfg.FFmpegPath != "ffmpeg" {
		t.Errorf("FFmpegPath = %q, want %q", cfg.FFmpegPath, "ffmpeg")
	}
	if cfg.Variant != "all" {
		t.Errorf("Variant = %q, want %q", cfg.Variant, "all")
	}
	if cfg.StatsEnabled != true {
		t.Error("StatsEnabled should be true by default")
	}
	if cfg.TUIEnabled != true {
		t.Error("TUIEnabled should be true by default")
	}
	if cfg.MetricsAddr != "0.0.0.0:17091" {
		t.Errorf("MetricsAddr = %q, want %q", cfg.MetricsAddr, "0.0.0.0:17091")
	}
	if cfg.BackoffMultiply < 1.0 {
		t.Errorf("BackoffMultiply = %f, should be >= 1.0", cfg.BackoffMultiply)
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StreamURL = "http://example.com/stream.m3u8"

	err := Validate(cfg)
	if err != nil {
		t.Errorf("Valid config should not error: %v", err)
	}
}

func TestValidate_MissingStreamURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StreamURL = ""

	err := Validate(cfg)
	if err == nil {
		t.Error("Expected error for missing stream URL")
	}
	if !strings.Contains(err.Error(), "stream_url") {
		t.Errorf("Error should mention stream_url: %v", err)
	}
}

func TestValidate_PrintCmdAllowsNoURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StreamURL = ""
	cfg.PrintCmd = true

	err := Validate(cfg)
	if err != nil {
		t.Errorf("PrintCmd mode should allow empty URL: %v", err)
	}
}

func TestValidate_InvalidStreamURL(t *testing.T) {
	testCases := []struct {
		name string
		url  string
	}{
		{"empty_scheme", "example.com/stream.m3u8"},
		{"ftp_scheme", "ftp://example.com/stream.m3u8"},
		{"file_scheme", "file:///path/to/stream.m3u8"},
		{"no_host", "http:///stream.m3u8"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.StreamURL = tc.url

			err := Validate(cfg)
			if err == nil {
				t.Errorf("Expected error for invalid URL %q", tc.url)
			}
		})
	}
}

func TestValidate_InvalidClients(t *testing.T) {
	testCases := []int{0, -1, -100}

	for _, clients := range testCases {
		t.Run("", func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.StreamURL = "http://example.com/stream.m3u8"
			cfg.Clients = clients

			err := Validate(cfg)
			if err == nil {
				t.Errorf("Expected error for clients=%d", clients)
			}
			if !strings.Contains(err.Error(), "clients") {
				t.Errorf("Error should mention clients: %v", err)
			}
		})
	}
}

func TestValidate_InvalidRampRate(t *testing.T) {
	testCases := []int{0, -1, -100}

	for _, rate := range testCases {
		t.Run("", func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.StreamURL = "http://example.com/stream.m3u8"
			cfg.RampRate = rate

			err := Validate(cfg)
			if err == nil {
				t.Errorf("Expected error for ramp_rate=%d", rate)
			}
			if !strings.Contains(err.Error(), "ramp_rate") {
				t.Errorf("Error should mention ramp_rate: %v", err)
			}
		})
	}
}

func TestValidate_InvalidVariant(t *testing.T) {
	testCases := []string{"", "invalid", "ALL", "HIGHEST", "middle"}

	for _, variant := range testCases {
		t.Run(variant, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.StreamURL = "http://example.com/stream.m3u8"
			cfg.Variant = variant

			err := Validate(cfg)
			if err == nil {
				t.Errorf("Expected error for variant=%q", variant)
			}
			if !strings.Contains(err.Error(), "variant") {
				t.Errorf("Error should mention variant: %v", err)
			}
		})
	}
}

func TestValidate_ValidVariants(t *testing.T) {
	testCases := []string{"all", "highest", "lowest", "first"}

	for _, variant := range testCases {
		t.Run(variant, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.StreamURL = "http://example.com/stream.m3u8"
			cfg.Variant = variant

			err := Validate(cfg)
			if err != nil {
				t.Errorf("variant=%q should be valid: %v", variant, err)
			}
		})
	}
}

func TestValidate_ResolveRequiresDangerous(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StreamURL = "http://example.com/stream.m3u8"
	cfg.ResolveIP = "192.168.1.1"
	cfg.DangerousMode = false

	err := Validate(cfg)
	if err == nil {
		t.Error("Expected error when resolve is set without dangerous mode")
	}
	if !strings.Contains(err.Error(), "dangerous") {
		t.Errorf("Error should mention dangerous: %v", err)
	}
}

func TestValidate_ResolveWithDangerous(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StreamURL = "http://example.com/stream.m3u8"
	cfg.ResolveIP = "192.168.1.1"
	cfg.DangerousMode = true

	err := Validate(cfg)
	if err != nil {
		t.Errorf("Resolve with dangerous should be valid: %v", err)
	}
}

func TestValidate_InvalidResolveIP(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StreamURL = "http://example.com/stream.m3u8"
	cfg.ResolveIP = "http://192.168.1.1" // URL instead of IP
	cfg.DangerousMode = true

	err := Validate(cfg)
	if err == nil {
		t.Error("Expected error for URL in resolve field")
	}
}

func TestValidate_InvalidProbeFailurePolicy(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StreamURL = "http://example.com/stream.m3u8"
	cfg.ProbeFailurePolicy = "invalid"

	err := Validate(cfg)
	if err == nil {
		t.Error("Expected error for invalid probe_failure_policy")
	}
}

func TestValidate_InvalidLogFormat(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StreamURL = "http://example.com/stream.m3u8"
	cfg.LogFormat = "yaml"

	err := Validate(cfg)
	if err == nil {
		t.Error("Expected error for invalid log_format")
	}
}

func TestValidate_InvalidTimeout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StreamURL = "http://example.com/stream.m3u8"
	cfg.Timeout = 0

	err := Validate(cfg)
	if err == nil {
		t.Error("Expected error for zero timeout")
	}

	cfg.Timeout = -1 * time.Second
	err = Validate(cfg)
	if err == nil {
		t.Error("Expected error for negative timeout")
	}
}

func TestValidate_InvalidBackoff(t *testing.T) {
	t.Run("zero_initial", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.StreamURL = "http://example.com/stream.m3u8"
		cfg.BackoffInitial = 0

		err := Validate(cfg)
		if err == nil {
			t.Error("Expected error for zero backoff_initial")
		}
	})

	t.Run("max_less_than_initial", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.StreamURL = "http://example.com/stream.m3u8"
		cfg.BackoffInitial = 5 * time.Second
		cfg.BackoffMax = 1 * time.Second

		err := Validate(cfg)
		if err == nil {
			t.Error("Expected error when backoff_max < backoff_initial")
		}
	})

	t.Run("multiply_less_than_one", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.StreamURL = "http://example.com/stream.m3u8"
		cfg.BackoffMultiply = 0.5

		err := Validate(cfg)
		if err == nil {
			t.Error("Expected error when backoff_multiply < 1.0")
		}
	})
}

func TestValidate_OriginMetricsWindow(t *testing.T) {
	t.Run("window_too_small", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.StreamURL = "http://example.com/stream.m3u8"
		cfg.OriginMetricsURL = "http://origin:9100/metrics"
		cfg.OriginMetricsWindow = 5 * time.Second // Less than 10s minimum

		err := Validate(cfg)
		if err == nil {
			t.Error("Expected error for window < 10s")
		}
	})

	t.Run("window_too_large", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.StreamURL = "http://example.com/stream.m3u8"
		cfg.OriginMetricsURL = "http://origin:9100/metrics"
		cfg.OriginMetricsWindow = 600 * time.Second // More than 300s max

		err := Validate(cfg)
		if err == nil {
			t.Error("Expected error for window > 300s")
		}
	})

	t.Run("window_less_than_2x_interval", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.StreamURL = "http://example.com/stream.m3u8"
		cfg.OriginMetricsURL = "http://origin:9100/metrics"
		cfg.OriginMetricsInterval = 10 * time.Second
		cfg.OriginMetricsWindow = 15 * time.Second // Less than 2 × 10s = 20s

		err := Validate(cfg)
		if err == nil {
			t.Error("Expected error when window < 2 × interval")
		}
	})
}

func TestValidate_MultipleErrors(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StreamURL = ""
	cfg.Clients = 0
	cfg.RampRate = 0

	err := Validate(cfg)
	if err == nil {
		t.Fatal("Expected multiple errors")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "stream_url") {
		t.Error("Error should mention stream_url")
	}
	if !strings.Contains(errStr, "clients") {
		t.Error("Error should mention clients")
	}
	if !strings.Contains(errStr, "ramp_rate") {
		t.Error("Error should mention ramp_rate")
	}
}

func TestOriginMetricsEnabled(t *testing.T) {
	t.Run("disabled_by_default", func(t *testing.T) {
		cfg := DefaultConfig()
		if cfg.OriginMetricsEnabled() {
			t.Error("Origin metrics should be disabled by default")
		}
	})

	t.Run("enabled_by_url", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.OriginMetricsURL = "http://origin:9100/metrics"
		if !cfg.OriginMetricsEnabled() {
			t.Error("Origin metrics should be enabled when URL is set")
		}
	})

	t.Run("enabled_by_nginx_url", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.NginxMetricsURL = "http://origin:9113/metrics"
		if !cfg.OriginMetricsEnabled() {
			t.Error("Origin metrics should be enabled when nginx URL is set")
		}
	})

	t.Run("enabled_by_host", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.OriginMetricsHost = "10.177.0.10"
		if !cfg.OriginMetricsEnabled() {
			t.Error("Origin metrics should be enabled when host is set")
		}
	})
}

func TestResolveOriginMetricsURLs(t *testing.T) {
	t.Run("explicit_urls", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.OriginMetricsURL = "http://custom:1111/metrics"
		cfg.NginxMetricsURL = "http://custom:2222/metrics"

		nodeURL, nginxURL := cfg.ResolveOriginMetricsURLs()
		if nodeURL != "http://custom:1111/metrics" {
			t.Errorf("nodeURL = %q, want explicit URL", nodeURL)
		}
		if nginxURL != "http://custom:2222/metrics" {
			t.Errorf("nginxURL = %q, want explicit URL", nginxURL)
		}
	})

	t.Run("host_based_urls", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.OriginMetricsHost = "10.177.0.10"

		nodeURL, nginxURL := cfg.ResolveOriginMetricsURLs()
		if nodeURL != "http://10.177.0.10:9100/metrics" {
			t.Errorf("nodeURL = %q, want host-based URL", nodeURL)
		}
		if nginxURL != "http://10.177.0.10:9113/metrics" {
			t.Errorf("nginxURL = %q, want host-based URL", nginxURL)
		}
	})

	t.Run("explicit_url_takes_precedence", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.OriginMetricsURL = "http://explicit:9100/metrics"
		cfg.OriginMetricsHost = "10.177.0.10"

		nodeURL, _ := cfg.ResolveOriginMetricsURLs()
		if nodeURL != "http://explicit:9100/metrics" {
			t.Errorf("Explicit URL should take precedence: %q", nodeURL)
		}
	})

	t.Run("custom_ports", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.OriginMetricsHost = "origin"
		cfg.OriginMetricsNodePort = 19100
		cfg.OriginMetricsNginxPort = 19113

		nodeURL, nginxURL := cfg.ResolveOriginMetricsURLs()
		if nodeURL != "http://origin:19100/metrics" {
			t.Errorf("nodeURL should use custom port: %q", nodeURL)
		}
		if nginxURL != "http://origin:19113/metrics" {
			t.Errorf("nginxURL should use custom port: %q", nginxURL)
		}
	})
}

func TestSegmentSizesEnabled(t *testing.T) {
	t.Run("disabled_by_default", func(t *testing.T) {
		cfg := DefaultConfig()
		if cfg.SegmentSizesEnabled() {
			t.Error("Segment sizes should be disabled by default")
		}
	})

	t.Run("enabled_by_url", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.SegmentSizesURL = "http://origin:17080/files/json/"
		if !cfg.SegmentSizesEnabled() {
			t.Error("Segment sizes should be enabled when URL is set")
		}
	})

	t.Run("auto_enabled_by_host", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.OriginMetricsHost = "10.177.0.10"
		if !cfg.SegmentSizesEnabled() {
			t.Error("Segment sizes should be auto-enabled when origin host is set")
		}
	})
}

func TestResolveSegmentSizesURL(t *testing.T) {
	t.Run("explicit_url", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.SegmentSizesURL = "http://custom:8080/files/json/"

		url := cfg.ResolveSegmentSizesURL()
		if url != "http://custom:8080/files/json/" {
			t.Errorf("Should return explicit URL: %q", url)
		}
	})

	t.Run("auto_derived_from_host", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.OriginMetricsHost = "10.177.0.10"

		url := cfg.ResolveSegmentSizesURL()
		if url != "http://10.177.0.10:17080/files/json/" {
			t.Errorf("Should auto-derive URL: %q", url)
		}
	})

	t.Run("explicit_takes_precedence", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.SegmentSizesURL = "http://explicit/files/json/"
		cfg.OriginMetricsHost = "10.177.0.10"

		url := cfg.ResolveSegmentSizesURL()
		if url != "http://explicit/files/json/" {
			t.Errorf("Explicit URL should take precedence: %q", url)
		}
	})
}

func TestApplyCheckMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Clients = 100
	cfg.Verbose = false

	ApplyCheckMode(cfg)

	if cfg.Clients != 1 {
		t.Errorf("Check mode should set clients=1, got %d", cfg.Clients)
	}
	if !cfg.Verbose {
		t.Error("Check mode should enable verbose")
	}
	if cfg.Duration != 10*1e9 {
		t.Errorf("Check mode should set duration=10s, got %v", cfg.Duration)
	}
}

func TestValidationError_Error(t *testing.T) {
	err := ValidationError{
		Field:   "test_field",
		Message: "test message",
	}

	errStr := err.Error()
	if errStr != "test_field: test message" {
		t.Errorf("Error string = %q, want %q", errStr, "test_field: test message")
	}
}
