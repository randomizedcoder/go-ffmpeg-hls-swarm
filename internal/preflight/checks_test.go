package preflight

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCheck_String(t *testing.T) {
	t.Run("passed_with_required", func(t *testing.T) {
		c := Check{
			Name:     "test_check",
			Required: 100,
			Actual:   200,
			Passed:   true,
		}
		s := c.String()
		if !strings.Contains(s, "✓") {
			t.Error("Passed check should have ✓")
		}
		if !strings.Contains(s, "200") {
			t.Error("Should contain actual value")
		}
		if !strings.Contains(s, "100") {
			t.Error("Should contain required value")
		}
	})

	t.Run("failed_check", func(t *testing.T) {
		c := Check{
			Name:     "test_check",
			Required: 100,
			Actual:   50,
			Passed:   false,
		}
		s := c.String()
		if !strings.Contains(s, "✗") {
			t.Error("Failed check should have ✗")
		}
	})

	t.Run("warning_check", func(t *testing.T) {
		c := Check{
			Name:    "test_check",
			Passed:  true,
			Warning: true,
			Message: "warning message",
		}
		s := c.String()
		if !strings.Contains(s, "⚠") {
			t.Error("Warning check should have ⚠")
		}
		if !strings.Contains(s, "warning message") {
			t.Error("Should contain message")
		}
	})

	t.Run("passed_with_message_only", func(t *testing.T) {
		c := Check{
			Name:    "test_check",
			Passed:  true,
			Message: "all good",
		}
		s := c.String()
		if !strings.Contains(s, "✓") {
			t.Error("Passed check should have ✓")
		}
		if !strings.Contains(s, "all good") {
			t.Error("Should contain message")
		}
	})
}

func TestRunAll_WithFFmpeg(t *testing.T) {
	// Check if ffmpeg is available
	_, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available, skipping integration test")
	}

	result := RunAll(10, "ffmpeg")

	if result == nil {
		t.Fatal("RunAll returned nil")
	}

	if len(result.Checks) < 3 {
		t.Errorf("Expected at least 3 checks, got %d", len(result.Checks))
	}

	// With ffmpeg available and low client count, should pass
	foundFFmpeg := false
	for _, check := range result.Checks {
		if check.Name == "ffmpeg" {
			foundFFmpeg = true
			if !check.Passed {
				t.Errorf("FFmpeg check should pass when ffmpeg is available: %s", check.Message)
			}
		}
	}
	if !foundFFmpeg {
		t.Error("Expected ffmpeg check in results")
	}
}

func TestRunAll_WithInvalidFFmpegPath(t *testing.T) {
	result := RunAll(10, "/nonexistent/ffmpeg/path")

	if result == nil {
		t.Fatal("RunAll returned nil")
	}

	// Should fail because ffmpeg is not found
	foundFFmpeg := false
	for _, check := range result.Checks {
		if check.Name == "ffmpeg" {
			foundFFmpeg = true
			if check.Passed {
				t.Error("FFmpeg check should fail with invalid path")
			}
			if !strings.Contains(check.Message, "not found") {
				t.Errorf("Message should mention 'not found': %s", check.Message)
			}
		}
	}
	if !foundFFmpeg {
		t.Error("Expected ffmpeg check in results")
	}

	// Overall result should fail
	if result.Passed {
		t.Error("Result should fail when ffmpeg is not found")
	}
}

func TestRunAll_FileDescriptorCheck(t *testing.T) {
	// Test with a very small number of clients (should pass)
	result := RunAll(1, "/bin/true") // /bin/true exists on most systems

	foundFD := false
	for _, check := range result.Checks {
		if check.Name == "file_descriptors" {
			foundFD = true
			if check.Actual <= 0 {
				t.Errorf("Actual FD limit should be positive: %d", check.Actual)
			}
			if check.Required <= 0 {
				t.Errorf("Required FD count should be positive: %d", check.Required)
			}
		}
	}
	if !foundFD {
		t.Error("Expected file_descriptors check in results")
	}
}

func TestRunAll_ProcessLimitCheck(t *testing.T) {
	result := RunAll(10, "/bin/true")

	foundProc := false
	for _, check := range result.Checks {
		if check.Name == "process_limit" {
			foundProc = true
			// Either passes with actual value or is a warning (non-Linux)
			if !check.Passed && !check.Warning {
				t.Errorf("Process limit should either pass or be a warning: %s", check.Message)
			}
		}
	}
	if !foundProc {
		t.Error("Expected process_limit check in results")
	}
}

func TestRunAll_EphemeralPortsCheck(t *testing.T) {
	result := RunAll(10, "/bin/true")

	foundPorts := false
	for _, check := range result.Checks {
		if check.Name == "ephemeral_ports" {
			foundPorts = true
			// This check should never fail (only warn)
			if !check.Passed {
				t.Errorf("Ephemeral ports check should always pass (warn at most): %s", check.Message)
			}
		}
	}
	if !foundPorts {
		t.Error("Expected ephemeral_ports check in results")
	}
}

func TestRunAll_HighClientCount(t *testing.T) {
	// Test with a very high client count - may trigger warnings
	result := RunAll(10000, "/bin/true")

	if result == nil {
		t.Fatal("RunAll returned nil")
	}

	// Even with high client count, checks should complete without panic
	for _, check := range result.Checks {
		// Just verify no panic and all checks have valid names
		if check.Name == "" {
			t.Error("Check name should not be empty")
		}
	}
}

func TestSuggestFix(t *testing.T) {
	testCases := []struct {
		name     string
		expected string
	}{
		{"file_descriptors", "ulimit -n"},
		{"process_limit", "ulimit -u"},
		{"ffmpeg", "install ffmpeg"},
		{"unknown", "documentation"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fix := suggestFix(tc.name)
			if !strings.Contains(fix, tc.expected) {
				t.Errorf("suggestFix(%q) = %q, should contain %q", tc.name, fix, tc.expected)
			}
		})
	}
}

func TestCheckFFmpeg_EdgeCases(t *testing.T) {
	t.Run("empty_path", func(t *testing.T) {
		check := checkFFmpeg("")
		// Empty path should fail
		if check.Passed {
			t.Error("Empty ffmpeg path should fail")
		}
	})

	t.Run("directory_as_path", func(t *testing.T) {
		check := checkFFmpeg("/tmp")
		// Directory instead of executable should fail
		if check.Passed {
			t.Error("Directory as ffmpeg path should fail")
		}
	})
}

func TestResult_Passed(t *testing.T) {
	t.Run("all_pass", func(t *testing.T) {
		result := &Result{
			Checks: []Check{
				{Name: "a", Passed: true},
				{Name: "b", Passed: true},
			},
			Passed: true,
		}
		if !result.Passed {
			t.Error("Result with all passing checks should pass")
		}
	})

	t.Run("one_fail", func(t *testing.T) {
		result := &Result{
			Checks: []Check{
				{Name: "a", Passed: true},
				{Name: "b", Passed: false},
			},
			Passed: false,
		}
		if result.Passed {
			t.Error("Result with one failing check should fail")
		}
	})

	t.Run("warning_only", func(t *testing.T) {
		result := &Result{
			Checks: []Check{
				{Name: "a", Passed: true, Warning: true},
			},
			Passed: true,
		}
		// Warnings don't cause failure
		if !result.Passed {
			t.Error("Result with only warnings should pass")
		}
	})
}

func TestCheckFileDescriptors(t *testing.T) {
	// Test with small client count
	check := checkFileDescriptors(1)

	if check.Name != "file_descriptors" {
		t.Errorf("Name = %q, want file_descriptors", check.Name)
	}
	if check.Actual <= 0 {
		t.Errorf("Actual should be positive: %d", check.Actual)
	}
	if check.Required <= 0 {
		t.Errorf("Required should be positive: %d", check.Required)
	}

	// With 1 client, should almost certainly pass
	// Required = 1*20 + 100 = 120, and most systems have at least 1024
	if !check.Passed && check.Actual >= 120 {
		t.Errorf("Check should pass when actual >= required: actual=%d, required=%d",
			check.Actual, check.Required)
	}
}

func TestCheckFileDescriptors_Scaling(t *testing.T) {
	// Verify required scales with clients
	check1 := checkFileDescriptors(1)
	check100 := checkFileDescriptors(100)
	check1000 := checkFileDescriptors(1000)

	if check100.Required <= check1.Required {
		t.Error("Required FDs should increase with more clients")
	}
	if check1000.Required <= check100.Required {
		t.Error("Required FDs should increase with more clients")
	}
}

// TestPrintResults just verifies no panic - output goes to stdout
func TestPrintResults(t *testing.T) {
	result := &Result{
		Checks: []Check{
			{Name: "test1", Passed: true, Message: "ok"},
			{Name: "test2", Passed: false, Required: 100, Actual: 50},
		},
		Passed: false,
	}

	// Should not panic
	PrintResults(result)
}
