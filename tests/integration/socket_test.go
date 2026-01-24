//go:build integration

// Package integration contains end-to-end tests that require external dependencies
// (FFmpeg, network access to test origin). Run with: go test -tags=integration ./tests/integration/...
package integration

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/parser"
)

// testOriginURL is the HLS stream URL for integration tests.
// Set via TEST_ORIGIN_URL environment variable.
func testOriginURL(t *testing.T) string {
	url := os.Getenv("TEST_ORIGIN_URL")
	if url == "" {
		t.Skip("TEST_ORIGIN_URL not set - skipping integration test")
	}
	return url
}

// requireFFmpeg skips the test if FFmpeg is not available.
func requireFFmpeg(t *testing.T) {
	_, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not found in PATH - skipping integration test")
	}
}

// TestIntegration_SocketReader_RealSocket tests the SocketReader with a real Unix socket.
func TestIntegration_SocketReader_RealSocket(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "progress.sock")

	var linesReceived int64
	var wg sync.WaitGroup

	// Create socket reader
	reader, err := parser.NewSocketReader(socketPath, 1024)
	if err != nil {
		t.Fatalf("NewSocketReader failed: %v", err)
	}

	// Start reader goroutine
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wg.Add(1)
	go func() {
		defer wg.Done()
		reader.Run(ctx, func(line string) {
			atomic.AddInt64(&linesReceived, 1)
		})
	}()

	// Wait for socket to be ready
	select {
	case <-reader.Ready():
		// Good
	case <-time.After(2 * time.Second):
		t.Fatal("Socket not ready after 2 seconds")
	}

	// Verify socket file exists
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Errorf("Socket file should exist at %s", socketPath)
	}

	// Send test data to socket using netcat or Go client
	go func() {
		time.Sleep(100 * time.Millisecond)
		// Use net.Dial to send data
		conn, err := dialUnix(socketPath)
		if err != nil {
			t.Logf("Failed to connect to socket: %v", err)
			return
		}
		defer conn.Close()

		// Send progress-like lines
		lines := []string{
			"frame=100",
			"fps=30.00",
			"stream_0_0_q=-1.0",
			"bitrate=N/A",
			"total_size=N/A",
			"out_time_us=3333333",
			"out_time=00:00:03.333333",
			"speed=1.00x",
			"progress=continue",
		}
		for _, line := range lines {
			conn.Write([]byte(line + "\n"))
		}
	}()

	// Wait for lines to be received
	time.Sleep(500 * time.Millisecond)
	reader.Close()
	wg.Wait()

	received := atomic.LoadInt64(&linesReceived)
	if received < 5 {
		t.Errorf("Expected at least 5 lines received, got %d", received)
	}

	// Verify socket file is cleaned up
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Errorf("Socket file should be cleaned up at %s", socketPath)
	}
}

// dialUnix connects to a Unix socket.
func dialUnix(path string) (net.Conn, error) {
	return net.Dial("unix", path)
}

// TestIntegration_SocketProgress_SingleClient tests a single FFmpeg client with socket progress.
func TestIntegration_SocketProgress_SingleClient(t *testing.T) {
	requireFFmpeg(t)
	streamURL := testOriginURL(t)

	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "progress.sock")

	var progressCount int64
	var wg sync.WaitGroup

	// Create socket reader
	reader, err := parser.NewSocketReader(socketPath, 1024)
	if err != nil {
		t.Fatalf("NewSocketReader failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Start reader goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		reader.Run(ctx, func(line string) {
			if strings.HasPrefix(line, "progress=") {
				atomic.AddInt64(&progressCount, 1)
			}
		})
	}()

	// Wait for socket to be ready
	select {
	case <-reader.Ready():
		t.Log("Socket ready")
	case <-time.After(2 * time.Second):
		t.Fatal("Socket not ready")
	}

	// Start FFmpeg with socket progress
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner",
		"-nostdin",
		"-loglevel", "warning",
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_on_network_error", "1",
		"-rw_timeout", "10000000",
		"-progress", "unix://"+socketPath,
		"-i", streamURL,
		"-map", "0",
		"-c", "copy",
		"-f", "null",
		"-",
	)

	cmd.Stderr = os.Stderr // Show FFmpeg errors

	if err := cmd.Start(); err != nil {
		t.Fatalf("FFmpeg start failed: %v", err)
	}

	// Wait for some progress
	time.Sleep(10 * time.Second)

	// Kill FFmpeg
	cmd.Process.Kill()
	cmd.Wait()

	// Close reader
	reader.Close()
	wg.Wait()

	progress := atomic.LoadInt64(&progressCount)
	t.Logf("Received %d progress updates", progress)

	if progress < 3 {
		t.Errorf("Expected at least 3 progress updates, got %d", progress)
	}

	// Verify cleanup
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Errorf("Socket should be cleaned up")
	}
}

// TestIntegration_PipeProgress_SingleClient tests a single FFmpeg client with pipe progress.
func TestIntegration_PipeProgress_SingleClient(t *testing.T) {
	requireFFmpeg(t)
	streamURL := testOriginURL(t)

	var progressCount int64
	var mu sync.Mutex
	var lines []string

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Start FFmpeg with pipe progress (stdout)
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner",
		"-nostdin",
		"-loglevel", "warning",
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_on_network_error", "1",
		"-rw_timeout", "10000000",
		"-progress", "pipe:1",
		"-i", streamURL,
		"-map", "0",
		"-c", "copy",
		"-f", "null",
		"-",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe failed: %v", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("FFmpeg start failed: %v", err)
	}

	// Read progress from stdout
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdout.Read(buf)
			if err != nil {
				return
			}
			chunk := string(buf[:n])
			for _, line := range strings.Split(chunk, "\n") {
				if strings.HasPrefix(line, "progress=") {
					atomic.AddInt64(&progressCount, 1)
				}
				if line != "" {
					mu.Lock()
					lines = append(lines, line)
					mu.Unlock()
				}
			}
		}
	}()

	// Wait for some progress
	time.Sleep(10 * time.Second)

	// Kill FFmpeg
	cmd.Process.Kill()
	cmd.Wait()

	progress := atomic.LoadInt64(&progressCount)
	t.Logf("Received %d progress updates", progress)

	mu.Lock()
	t.Logf("Total lines: %d", len(lines))
	mu.Unlock()

	if progress < 3 {
		t.Errorf("Expected at least 3 progress updates, got %d", progress)
	}
}

// TestIntegration_DebugParser_RealFFmpeg tests the DebugEventParser with real FFmpeg debug output.
func TestIntegration_DebugParser_RealFFmpeg(t *testing.T) {
	requireFFmpeg(t)
	streamURL := testOriginURL(t)

	var hlsRequests, tcpConnects int64
	p := parser.NewDebugEventParser(1, 2*time.Second, func(e *parser.DebugEvent) {
		switch e.Type {
		case parser.DebugEventHLSRequest:
			atomic.AddInt64(&hlsRequests, 1)
		case parser.DebugEventTCPConnected:
			atomic.AddInt64(&tcpConnects, 1)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Start FFmpeg with debug logging
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner",
		"-nostdin",
		"-loglevel", "debug",
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_on_network_error", "1",
		"-rw_timeout", "10000000",
		"-i", streamURL,
		"-map", "0",
		"-c", "copy",
		"-f", "null",
		"-",
	)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("StderrPipe failed: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("FFmpeg start failed: %v", err)
	}

	// Parse stderr
	go func() {
		buf := make([]byte, 16384)
		var lineBuf strings.Builder
		for {
			n, err := stderr.Read(buf)
			if err != nil {
				return
			}
			chunk := string(buf[:n])
			for _, c := range chunk {
				if c == '\n' {
					p.ParseLine(lineBuf.String())
					lineBuf.Reset()
				} else {
					lineBuf.WriteRune(c)
				}
			}
		}
	}()

	// Wait for parsing
	time.Sleep(15 * time.Second)

	// Kill FFmpeg
	cmd.Process.Kill()
	cmd.Wait()

	stats := p.Stats()
	t.Logf("DebugParser stats:")
	t.Logf("  Lines processed: %d", stats.LinesProcessed)
	t.Logf("  HLS requests: %d", atomic.LoadInt64(&hlsRequests))
	t.Logf("  TCP connects: %d", atomic.LoadInt64(&tcpConnects))
	t.Logf("  TCP success: %d, failure: %d", stats.TCPSuccessCount, stats.TCPFailureCount)
	t.Logf("  Playlist refreshes: %d", stats.PlaylistRefreshes)
	t.Logf("  Sequence skips: %d", stats.SequenceSkips)
	t.Logf("  TCP health ratio: %.2f", stats.TCPHealthRatio)

	if stats.LinesProcessed < 100 {
		t.Errorf("Expected at least 100 lines processed, got %d", stats.LinesProcessed)
	}
	if atomic.LoadInt64(&hlsRequests) < 3 {
		t.Errorf("Expected at least 3 HLS requests, got %d", atomic.LoadInt64(&hlsRequests))
	}
}
