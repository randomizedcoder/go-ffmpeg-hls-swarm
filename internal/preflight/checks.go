// Package preflight provides startup validation checks.
package preflight

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// Note: syscall.RLIMIT_NPROC is not exported in Go's syscall package,
// so we read process limits from /proc/self/limits instead.

// Check represents the result of a single preflight check.
type Check struct {
	Name     string // Name of the check
	Required int    // Required value (if applicable)
	Actual   int    // Actual value found
	Passed   bool   // Whether the check passed
	Warning  bool   // True if it's a warning (non-fatal)
	Message  string // Additional context
}

// Result holds the results of all preflight checks.
type Result struct {
	Checks []Check
	Passed bool
}

// String returns a human-readable summary of the check.
func (c Check) String() string {
	status := "✓"
	if !c.Passed {
		status = "✗"
	} else if c.Warning {
		status = "⚠"
	}

	if c.Required > 0 {
		return fmt.Sprintf("  %s %s: %d available (need %d)", status, c.Name, c.Actual, c.Required)
	}
	return fmt.Sprintf("  %s %s: %s", status, c.Name, c.Message)
}

// RunAll executes all preflight checks.
func RunAll(targetClients int, ffmpegPath string) *Result {
	result := &Result{
		Checks: make([]Check, 0, 4),
		Passed: true,
	}

	// File descriptor check
	fdCheck := checkFileDescriptors(targetClients)
	result.Checks = append(result.Checks, fdCheck)
	if !fdCheck.Passed {
		result.Passed = false
	}

	// Process limit check
	procCheck := checkProcessLimit(targetClients)
	result.Checks = append(result.Checks, procCheck)
	if !procCheck.Passed {
		result.Passed = false
	}

	// FFmpeg check
	ffmpegCheck := checkFFmpeg(ffmpegPath)
	result.Checks = append(result.Checks, ffmpegCheck)
	if !ffmpegCheck.Passed {
		result.Passed = false
	}

	// Ephemeral port check (warning only)
	portCheck := checkEphemeralPorts(targetClients)
	result.Checks = append(result.Checks, portCheck)
	// Don't fail on port warning

	return result
}

// checkFileDescriptors verifies sufficient file descriptors are available.
func checkFileDescriptors(clients int) Check {
	var limit syscall.Rlimit
	syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit)

	// Each FFmpeg needs ~10-20 FDs (sockets, files, pipes)
	// Plus orchestrator overhead (metrics server, logging, etc.)
	required := clients*20 + 100
	actual := int(limit.Cur)

	return Check{
		Name:     "file_descriptors",
		Required: required,
		Actual:   actual,
		Passed:   actual >= required,
		Message:  fmt.Sprintf("ulimit -n %d (need %d for %d clients)", actual, required, clients),
	}
}

// checkProcessLimit verifies sufficient process slots are available.
func checkProcessLimit(clients int) Check {
	// RLIMIT_NPROC is not available on all systems
	// On Linux, it's typically 7 (but syscall package doesn't export it)
	// Try to read from /proc instead
	required := clients + 50

	// Read soft limit from /proc/self/limits
	data, err := os.ReadFile("/proc/self/limits")
	if err != nil {
		// Non-Linux or restricted access, assume OK
		return Check{
			Name:    "process_limit",
			Passed:  true,
			Warning: true,
			Message: "unable to check (non-Linux or restricted)",
		}
	}

	// Parse "Max processes" line
	actual := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Max processes") {
			fields := strings.Fields(line)
			if len(fields) >= 4 {
				if fields[3] == "unlimited" {
					actual = 1000000
				} else {
					fmt.Sscanf(fields[3], "%d", &actual)
				}
			}
			break
		}
	}

	if actual == 0 {
		return Check{
			Name:    "process_limit",
			Passed:  true,
			Warning: true,
			Message: "unable to determine (assuming OK)",
		}
	}

	return Check{
		Name:     "process_limit",
		Required: required,
		Actual:   actual,
		Passed:   actual >= required,
		Message:  fmt.Sprintf("ulimit -u %d (need %d)", actual, required),
	}
}

// checkFFmpeg verifies FFmpeg is available and working.
func checkFFmpeg(path string) Check {
	cmd := exec.Command(path, "-version")
	output, err := cmd.Output()

	if err != nil {
		return Check{
			Name:    "ffmpeg",
			Passed:  false,
			Message: fmt.Sprintf("not found at %s: %v", path, err),
		}
	}

	// Extract version from first line
	lines := strings.Split(string(output), "\n")
	version := "unknown"
	if len(lines) > 0 {
		// "ffmpeg version 6.1 Copyright ..."
		parts := strings.Fields(lines[0])
		if len(parts) >= 3 {
			version = parts[2]
		}
	}

	return Check{
		Name:    "ffmpeg",
		Passed:  true,
		Message: fmt.Sprintf("found at %s (version %s)", path, version),
	}
}

// checkEphemeralPorts checks if enough ephemeral ports are available.
func checkEphemeralPorts(clients int) Check {
	// Read ephemeral port range
	data, err := os.ReadFile("/proc/sys/net/ipv4/ip_local_port_range")
	if err != nil {
		return Check{
			Name:    "ephemeral_ports",
			Passed:  true,
			Warning: true,
			Message: "unable to read port range (non-Linux?)",
		}
	}

	var low, high int
	fmt.Sscanf(string(data), "%d %d", &low, &high)
	available := high - low

	// Each client may use 1-4 connections, need headroom for TIME_WAIT
	recommended := clients * 4

	return Check{
		Name:     "ephemeral_ports",
		Required: recommended,
		Actual:   available,
		Passed:   true, // Don't fail on this
		Warning:  available < recommended,
		Message:  fmt.Sprintf("%d-%d (%d available, recommend %d)", low, high, available, recommended),
	}
}

// PrintResults prints the preflight check results to stdout.
func PrintResults(result *Result) {
	fmt.Println("Preflight checks:")
	for _, check := range result.Checks {
		fmt.Println(check.String())
		if !check.Passed {
			fmt.Printf("    Fix: %s\n", suggestFix(check.Name))
		}
	}
	fmt.Println()
}

// suggestFix returns a suggestion for fixing a failed check.
func suggestFix(name string) string {
	switch name {
	case "file_descriptors":
		return "ulimit -n 8192 (or edit /etc/security/limits.conf)"
	case "process_limit":
		return "ulimit -u 4096 (or edit /etc/security/limits.conf)"
	case "ffmpeg":
		return "install ffmpeg (apt install ffmpeg / brew install ffmpeg)"
	default:
		return "see documentation"
	}
}
