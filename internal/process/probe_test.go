package process

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestFindFFprobe_ShortBinaryPath(t *testing.T) {
	// BUG: findFFprobe does BinaryPath[:len(BinaryPath)-6] which panics
	// if BinaryPath is less than 6 characters
	testCases := []string{
		"",       // Empty path
		"f",      // 1 char
		"ff",     // 2 chars
		"ffm",    // 3 chars
		"ffmp",   // 4 chars
		"ffmpe",  // 5 chars - still too short!
		"ffmpeg", // Exactly 6 chars - this is the default, should work
	}

	for _, path := range testCases {
		t.Run(path, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("findFFprobe panicked with BinaryPath=%q: %v", path, r)
				}
			}()

			r := &FFmpegRunner{
				config: &FFmpegConfig{
					BinaryPath: path,
				},
			}

			// Should not panic
			result := r.findFFprobe()
			_ = result
		})
	}
}

func TestFindFFprobe_NonFFmpegPath(t *testing.T) {
	// What if path doesn't end with "ffmpeg"?
	testCases := []string{
		"/usr/bin/avconv",           // Different binary name
		"/path/to/custom-ffmpeg",    // Hyphenated name
		"/path/to/ffmpeg-custom",    // Suffix added
		"/path/to/FFMPEG",           // Uppercase
		"/path/ending/with/ffprobe", // Already ffprobe!
	}

	for _, path := range testCases {
		t.Run(path, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("findFFprobe panicked with BinaryPath=%q: %v", path, r)
				}
			}()

			r := &FFmpegRunner{
				config: &FFmpegConfig{
					BinaryPath: path,
				},
			}

			// Should not panic
			result := r.findFFprobe()
			_ = result
		})
	}
}

func TestProbeVariants_VariantAll(t *testing.T) {
	// When variant is "all", no probing should happen
	r := &FFmpegRunner{
		config: &FFmpegConfig{
			Variant: VariantAll,
		},
	}

	err := r.ProbeVariants(context.Background())
	if err != nil {
		t.Errorf("ProbeVariants should return nil for VariantAll: %v", err)
	}
}

func TestProbeVariants_VariantFirst(t *testing.T) {
	// When variant is "first", no probing should happen
	r := &FFmpegRunner{
		config: &FFmpegConfig{
			Variant: VariantFirst,
		},
	}

	err := r.ProbeVariants(context.Background())
	if err != nil {
		t.Errorf("ProbeVariants should return nil for VariantFirst: %v", err)
	}
}

func TestProbeResult_JSONParsing(t *testing.T) {
	testCases := []struct {
		name     string
		json     string
		wantErr  bool
		wantProgs int
	}{
		{
			name:     "empty_programs",
			json:     `{"programs":[]}`,
			wantErr:  false,
			wantProgs: 0,
		},
		{
			name:     "single_program",
			json:     `{"programs":[{"program_id":0,"program_num":1,"nb_streams":2,"tags":{"variant_bitrate":"5000000"}}]}`,
			wantErr:  false,
			wantProgs: 1,
		},
		{
			name:     "multiple_programs",
			json:     `{"programs":[{"program_id":0,"tags":{"variant_bitrate":"1000000"}},{"program_id":1,"tags":{"variant_bitrate":"5000000"}}]}`,
			wantErr:  false,
			wantProgs: 2,
		},
		{
			name:     "missing_bitrate_tag",
			json:     `{"programs":[{"program_id":0,"tags":{}}]}`,
			wantErr:  false,
			wantProgs: 1,
		},
		{
			name:     "non_numeric_bitrate",
			json:     `{"programs":[{"program_id":0,"tags":{"variant_bitrate":"not_a_number"}}]}`,
			wantErr:  false,
			wantProgs: 1, // Should parse but bitrate will be 0
		},
		{
			name:    "invalid_json",
			json:    `{not valid json`,
			wantErr: true,
		},
		{
			name:     "empty_object",
			json:     `{}`,
			wantErr:  false,
			wantProgs: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var result ProbeResult
			err := json.Unmarshal([]byte(tc.json), &result)

			if tc.wantErr {
				if err == nil {
					t.Error("Expected error for invalid JSON")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if len(result.Programs) != tc.wantProgs {
				t.Errorf("Got %d programs, want %d", len(result.Programs), tc.wantProgs)
			}
		})
	}
}

func TestProgramInfo_BitrateEdgeCases(t *testing.T) {
	testCases := []struct {
		bitrateStr string
		expected   int64
	}{
		{"5000000", 5000000},
		{"0", 0},
		{"-1", -1},
		{"", 0},
		{"abc", 0},
		{"9223372036854775807", 9223372036854775807}, // Max int64
		// Overflow: strconv.ParseInt returns max int64 and error; code ignores error
		{"9223372036854775808", 9223372036854775807},
	}

	for _, tc := range testCases {
		t.Run(tc.bitrateStr, func(t *testing.T) {
			program := Program{
				Tags: Tags{VariantBitrate: tc.bitrateStr},
			}

			// Simulate the parsing logic from probe()
			var bitrate int64
			if program.Tags.VariantBitrate != "" {
				parsed, _ := parseInt64(program.Tags.VariantBitrate)
				bitrate = parsed
			}

			if bitrate != tc.expected {
				t.Errorf("Bitrate = %d, want %d", bitrate, tc.expected)
			}
		})
	}
}

func parseInt64(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

func TestProbeAvailable(t *testing.T) {
	// This just tests that ProbeAvailable doesn't panic
	result := ProbeAvailable()
	// Result depends on whether ffprobe is installed
	_ = result
}

func TestEffectiveURL_EdgeCases(t *testing.T) {
	testCases := []struct {
		name       string
		streamURL  string
		resolveIP  string
		expectHost string // Expected host in the resulting URL
	}{
		{
			name:       "no_resolve",
			streamURL:  "http://example.com/stream.m3u8",
			resolveIP:  "",
			expectHost: "example.com",
		},
		{
			name:       "with_resolve",
			streamURL:  "http://example.com/stream.m3u8",
			resolveIP:  "192.168.1.1",
			expectHost: "192.168.1.1",
		},
		{
			name:       "https_with_resolve",
			streamURL:  "https://example.com/stream.m3u8",
			resolveIP:  "10.0.0.1",
			expectHost: "10.0.0.1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := &FFmpegRunner{
				config: &FFmpegConfig{
					StreamURL: tc.streamURL,
					ResolveIP: tc.resolveIP,
				},
			}

			result := r.effectiveURL()
			if tc.resolveIP != "" {
				if result == tc.streamURL {
					t.Errorf("effectiveURL should have replaced host with resolve IP")
				}
			}
		})
	}
}

func TestProbeVariants_ContextCancellation(t *testing.T) {
	// Test that context cancellation is handled
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	r := &FFmpegRunner{
		config: &FFmpegConfig{
			Variant:    VariantHighest,
			BinaryPath: "ffprobe", // Will fail because context is cancelled
		},
	}

	// Should return quickly due to cancelled context
	start := time.Now()
	_ = r.ProbeVariants(ctx)
	elapsed := time.Since(start)

	// Should return quickly (within 1 second)
	if elapsed > 2*time.Second {
		t.Errorf("ProbeVariants took too long with cancelled context: %v", elapsed)
	}
}

func TestProbeVariants_InvalidFFprobe(t *testing.T) {
	// Test with invalid ffprobe path
	r := &FFmpegRunner{
		config: &FFmpegConfig{
			Variant:    VariantHighest,
			BinaryPath: "/nonexistent/ffmpeg",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err := r.ProbeVariants(ctx)
	// Should fail because ffprobe doesn't exist
	if err == nil {
		// This might pass if system ffprobe is available
		t.Log("Warning: Test passed because system ffprobe was found")
	}
}

// Test that the probe correctly handles HTTP server responses
func TestProbe_HTTPServer(t *testing.T) {
	// Skip if ffprobe is not available
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not available")
	}

	// Create a test server that returns an HLS manifest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a simple m3u8 that ffprobe can parse
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Write([]byte(`#EXTM3U
#EXT-X-VERSION:3
#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=640x360
low.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=5000000,RESOLUTION=1920x1080
high.m3u8
`))
	}))
	defer server.Close()

	r := &FFmpegRunner{
		config: &FFmpegConfig{
			StreamURL:  server.URL + "/master.m3u8",
			Variant:    VariantHighest,
			BinaryPath: "ffmpeg",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Note: This will likely fail because ffprobe expects proper segment files
	// but it shouldn't panic
	_ = r.ProbeVariants(ctx)
}

// Test zero values for all Config fields used by probe
func TestProbe_ZeroConfig(t *testing.T) {
	r := &FFmpegRunner{
		config: &FFmpegConfig{}, // All zero values
	}

	// findFFprobe should not panic with empty config
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("findFFprobe panicked with zero config: %v", r)
		}
	}()

	result := r.findFFprobe()
	// With empty BinaryPath, should return "ffprobe"
	if result != "ffprobe" {
		t.Errorf("Expected 'ffprobe' for empty BinaryPath, got %q", result)
	}
}
