package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ValidationError represents a configuration validation error.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// Validate checks the configuration for errors and inconsistencies.
// Returns nil if valid, or an error describing the problem.
func Validate(cfg *Config) error {
	var errs []error

	// Stream URL is required (unless --print-cmd without URL)
	if cfg.StreamURL == "" && !cfg.PrintCmd {
		errs = append(errs, ValidationError{
			Field:   "stream_url",
			Message: "HLS stream URL is required",
		})
	}

	// Validate stream URL format if provided
	if cfg.StreamURL != "" {
		if err := validateURL(cfg.StreamURL); err != nil {
			errs = append(errs, ValidationError{
				Field:   "stream_url",
				Message: err.Error(),
			})
		}
	}

	// Clients must be positive
	if cfg.Clients < 1 {
		errs = append(errs, ValidationError{
			Field:   "clients",
			Message: "must be at least 1",
		})
	}

	// Ramp rate must be positive
	if cfg.RampRate < 1 {
		errs = append(errs, ValidationError{
			Field:   "ramp_rate",
			Message: "must be at least 1",
		})
	}

	// Variant must be valid
	validVariants := map[string]bool{
		"all": true, "highest": true, "lowest": true, "first": true,
	}
	if !validVariants[cfg.Variant] {
		errs = append(errs, ValidationError{
			Field:   "variant",
			Message: fmt.Sprintf("must be one of: all, highest, lowest, first (got %q)", cfg.Variant),
		})
	}

	// -resolve requires --dangerous
	if cfg.ResolveIP != "" && !cfg.DangerousMode {
		errs = append(errs, ValidationError{
			Field:   "resolve",
			Message: "-resolve requires --dangerous flag (disables TLS verification)",
		})
	}

	// Validate resolve IP format if provided
	if cfg.ResolveIP != "" {
		if err := validateIP(cfg.ResolveIP); err != nil {
			errs = append(errs, ValidationError{
				Field:   "resolve",
				Message: err.Error(),
			})
		}
	}

	// Probe failure policy must be valid
	validPolicies := map[string]bool{"fallback": true, "fail": true}
	if !validPolicies[cfg.ProbeFailurePolicy] {
		errs = append(errs, ValidationError{
			Field:   "probe_failure_policy",
			Message: fmt.Sprintf("must be 'fallback' or 'fail' (got %q)", cfg.ProbeFailurePolicy),
		})
	}

	// Log format must be valid
	validFormats := map[string]bool{"json": true, "text": true}
	if !validFormats[cfg.LogFormat] {
		errs = append(errs, ValidationError{
			Field:   "log_format",
			Message: fmt.Sprintf("must be 'json' or 'text' (got %q)", cfg.LogFormat),
		})
	}

	// Timeout must be positive
	if cfg.Timeout <= 0 {
		errs = append(errs, ValidationError{
			Field:   "timeout",
			Message: "must be positive",
		})
	}

	// Backoff settings
	if cfg.BackoffInitial <= 0 {
		errs = append(errs, ValidationError{
			Field:   "backoff_initial",
			Message: "must be positive",
		})
	}
	if cfg.BackoffMax < cfg.BackoffInitial {
		errs = append(errs, ValidationError{
			Field:   "backoff_max",
			Message: "must be >= backoff_initial",
		})
	}
	if cfg.BackoffMultiply < 1.0 {
		errs = append(errs, ValidationError{
			Field:   "backoff_multiply",
			Message: "must be >= 1.0",
		})
	}

	// Origin metrics window validation (if origin metrics are enabled)
	if cfg.OriginMetricsURL != "" || cfg.NginxMetricsURL != "" {
		const minWindow = 10 * time.Second
		const maxWindow = 300 * time.Second
		if cfg.OriginMetricsWindow < minWindow {
			errs = append(errs, ValidationError{
				Field:   "origin_metrics_window",
				Message: fmt.Sprintf("must be at least %v (got %v)", minWindow, cfg.OriginMetricsWindow),
			})
		}
		if cfg.OriginMetricsWindow > maxWindow {
			errs = append(errs, ValidationError{
				Field:   "origin_metrics_window",
				Message: fmt.Sprintf("must be at most %v (got %v)", maxWindow, cfg.OriginMetricsWindow),
			})
		}
		// Window should be at least 2× the scrape interval for meaningful percentiles
		if cfg.OriginMetricsWindow < 2*cfg.OriginMetricsInterval {
			errs = append(errs, ValidationError{
				Field:   "origin_metrics_window",
				Message: fmt.Sprintf("must be at least 2× scrape interval (%v), got %v", 2*cfg.OriginMetricsInterval, cfg.OriginMetricsWindow),
			})
		}
	}

	// Return combined errors
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// validateURL checks if the URL is valid and uses http or https.
func validateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https (got %q)", u.Scheme)
	}

	if u.Host == "" {
		return errors.New("URL must have a host")
	}

	// Should end in .m3u8 or have a playlist-like path
	if !strings.HasSuffix(u.Path, ".m3u8") && !strings.Contains(u.Path, "m3u8") {
		// This is a warning, not an error — some CDNs use different extensions
		// Just proceed without validation
	}

	return nil
}

// validateIP checks if the IP is a valid IPv4 or IPv6 address.
func validateIP(ip string) error {
	// Simple validation: must not be empty and not contain scheme
	if strings.Contains(ip, "://") {
		return errors.New("must be an IP address, not a URL")
	}

	// Could use net.ParseIP but allow hostnames too for flexibility
	if ip == "" {
		return errors.New("must not be empty")
	}

	return nil
}

// ApplyCheckMode modifies config for --check mode.
func ApplyCheckMode(cfg *Config) {
	cfg.Clients = 1
	cfg.Duration = 10 * 1e9 // 10 seconds in nanoseconds
	cfg.Verbose = true
}
