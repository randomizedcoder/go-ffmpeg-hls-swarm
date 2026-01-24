//go:build !windows

package parser

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSocketReader_Basic(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	pipeline := NewPipeline(1, "progress", 100, 0.01)

	reader, err := NewSocketReader(socketPath, pipeline, nil)
	if err != nil {
		t.Fatalf("NewSocketReader failed: %v", err)
	}
	defer reader.Close()

	// Start reader in goroutine
	done := make(chan struct{})
	go func() {
		reader.Run()
		close(done)
	}()

	// Wait for ready
	select {
	case <-reader.Ready():
		// Good
	case <-time.After(time.Second):
		t.Fatal("Ready() not signaled within timeout")
	}

	// Connect and send data
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	testLines := []string{
		"frame=100",
		"fps=30.0",
		"progress=continue",
	}

	for _, line := range testLines {
		_, err := conn.Write([]byte(line + "\n"))
		if err != nil {
			t.Fatalf("Failed to write: %v", err)
		}
	}

	// Close connection to signal EOF
	conn.Close()

	// Wait for reader to finish
	select {
	case <-done:
		// Good
	case <-time.After(5 * time.Second):
		t.Fatal("reader.Run() did not exit")
	}

	// Verify stats
	bytesRead, linesRead, healthy := reader.Stats()
	if linesRead != int64(len(testLines)) {
		t.Errorf("Expected %d lines, got %d", len(testLines), linesRead)
	}
	if bytesRead == 0 {
		t.Error("Expected bytesRead > 0")
	}
	if healthy {
		t.Error("Expected healthy=false after cleanup")
	}
}

func TestSocketReader_MultipleLines(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	pipeline := NewPipeline(1, "progress", 1000, 0.01)

	reader, err := NewSocketReader(socketPath, pipeline, nil)
	if err != nil {
		t.Fatalf("NewSocketReader failed: %v", err)
	}
	defer reader.Close()

	done := make(chan struct{})
	go func() {
		reader.Run()
		close(done)
	}()

	<-reader.Ready()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// Send 100 lines
	const numLines = 100
	for i := 0; i < numLines; i++ {
		fmt.Fprintf(conn, "line=%d\n", i)
	}
	conn.Close()

	<-done

	_, linesRead, _ := reader.Stats()
	if linesRead != numLines {
		t.Errorf("Expected %d lines, got %d", numLines, linesRead)
	}
}

func TestSocketReader_Cleanup(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	pipeline := NewPipeline(1, "progress", 100, 0.01)

	reader, err := NewSocketReader(socketPath, pipeline, nil)
	if err != nil {
		t.Fatalf("NewSocketReader failed: %v", err)
	}

	// Verify socket file exists
	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("Socket file should exist: %v", err)
	}

	// Close reader
	reader.Close()

	// Verify socket file removed
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Error("Socket file should be removed after Close()")
	}
}

func TestSocketReader_StaleSocket(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "test.sock")

	// Create a stale socket file by creating a regular file
	// (net.Listen may remove socket on Close on some systems)
	f, err := os.Create(socketPath)
	if err != nil {
		t.Fatalf("Failed to create stale file: %v", err)
	}
	f.Close()

	// Verify stale file exists
	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("Stale file should exist: %v", err)
	}

	// Create new reader (should remove stale file and create socket)
	pipeline := NewPipeline(1, "progress", 100, 0.01)
	reader, err := NewSocketReader(socketPath, pipeline, nil)
	if err != nil {
		t.Fatalf("NewSocketReader should succeed with stale file: %v", err)
	}
	defer reader.Close()

	// Verify it's now a socket
	fi, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("Socket should exist: %v", err)
	}
	if fi.Mode()&os.ModeSocket == 0 {
		t.Error("Path should be a socket, not a regular file")
	}
}

func TestSocketReader_CloseBeforeConnect(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	pipeline := NewPipeline(1, "progress", 100, 0.01)

	reader, err := NewSocketReader(socketPath, pipeline, nil)
	if err != nil {
		t.Fatalf("NewSocketReader failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		reader.Run()
		close(done)
	}()

	<-reader.Ready()

	// Close before connecting
	reader.Close()

	// Run should exit
	select {
	case <-done:
		// Good
	case <-time.After(5 * time.Second):
		t.Fatal("reader.Run() did not exit after Close()")
	}

	// Should report failed to connect (due to close, not timeout)
	// Note: May or may not be true depending on timing
}

func TestSocketReader_LongLines(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	pipeline := NewPipeline(1, "progress", 100, 0.01)

	reader, err := NewSocketReader(socketPath, pipeline, nil)
	if err != nil {
		t.Fatalf("NewSocketReader failed: %v", err)
	}
	defer reader.Close()

	done := make(chan struct{})
	go func() {
		reader.Run()
		close(done)
	}()

	<-reader.Ready()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// Send a 32KB line (well under 64KB limit)
	longLine := strings.Repeat("x", 32*1024)
	fmt.Fprintf(conn, "%s\n", longLine)
	conn.Close()

	<-done

	bytesRead, linesRead, _ := reader.Stats()
	if linesRead != 1 {
		t.Errorf("Expected 1 line, got %d", linesRead)
	}
	if bytesRead < int64(len(longLine)) {
		t.Errorf("Expected bytesRead >= %d, got %d", len(longLine), bytesRead)
	}
}

func TestSocketReader_Stats(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	pipeline := NewPipeline(1, "progress", 100, 0.01)

	reader, err := NewSocketReader(socketPath, pipeline, nil)
	if err != nil {
		t.Fatalf("NewSocketReader failed: %v", err)
	}
	defer reader.Close()

	// Initial stats
	bytesRead, linesRead, healthy := reader.Stats()
	if bytesRead != 0 || linesRead != 0 {
		t.Error("Initial stats should be zero")
	}
	if !healthy {
		t.Error("Initial healthy should be true")
	}

	done := make(chan struct{})
	go func() {
		reader.Run()
		close(done)
	}()

	<-reader.Ready()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// Send known data
	data := "test line\n"
	conn.Write([]byte(data))
	conn.Close()

	<-done

	bytesRead, linesRead, healthy = reader.Stats()
	if linesRead != 1 {
		t.Errorf("Expected 1 line, got %d", linesRead)
	}
	if bytesRead != int64(len(data)) {
		t.Errorf("Expected %d bytes, got %d", len(data), bytesRead)
	}
	// After cleanup, healthy should be false
	if healthy {
		t.Error("healthy should be false after cleanup")
	}
}

func TestSocketReader_PathTooLong(t *testing.T) {
	t.Parallel()

	// Create a path that exceeds 104 bytes
	longPath := "/tmp/" + strings.Repeat("x", 100) + ".sock"
	if len(longPath) <= maxUnixSocketPathLen {
		t.Fatalf("Test setup error: path should be > %d bytes", maxUnixSocketPathLen)
	}

	pipeline := NewPipeline(1, "progress", 100, 0.01)
	_, err := NewSocketReader(longPath, pipeline, nil)
	if err == nil {
		t.Fatal("Expected error for path > 104 bytes")
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("Expected 'too long' error, got: %v", err)
	}
}

func TestSocketReader_PathExactlyAtLimit(t *testing.T) {
	t.Parallel()

	// Create a path that is exactly 104 bytes
	// "/tmp/" is 5 bytes, ".sock" is 5 bytes, so we need 94 'x' chars
	tmpDir := "/tmp/"
	suffix := ".sock"
	padding := maxUnixSocketPathLen - len(tmpDir) - len(suffix)
	socketPath := tmpDir + strings.Repeat("x", padding) + suffix

	if len(socketPath) != maxUnixSocketPathLen {
		t.Fatalf("Test setup error: path should be exactly %d bytes, got %d", maxUnixSocketPathLen, len(socketPath))
	}

	pipeline := NewPipeline(1, "progress", 100, 0.01)
	reader, err := NewSocketReader(socketPath, pipeline, nil)
	if err != nil {
		t.Fatalf("Path at exactly %d bytes should be valid: %v", maxUnixSocketPathLen, err)
	}
	reader.Close()

	// Cleanup
	os.Remove(socketPath)
}

func TestSocketReader_NoGoroutineLeak(t *testing.T) {
	t.Parallel()

	// Allow for test framework goroutines
	runtime.GC()
	time.Sleep(10 * time.Millisecond)
	before := runtime.NumGoroutine()

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	pipeline := NewPipeline(1, "progress", 100, 0.01)

	reader, err := NewSocketReader(socketPath, pipeline, nil)
	if err != nil {
		t.Fatalf("NewSocketReader failed: %v", err)
	}

	// Start reader in goroutine
	done := make(chan struct{})
	go func() {
		reader.Run()
		close(done)
	}()

	// Wait for ready
	<-reader.Ready()

	// Close without connecting (simulates FFmpeg failure)
	reader.Close()

	// Wait for Run() to exit
	select {
	case <-done:
		// Good
	case <-time.After(5 * time.Second):
		t.Fatal("reader.Run() did not exit")
	}

	// Verify pipeline channel is closed (I1)
	select {
	case _, ok := <-pipeline.lineChan:
		if ok {
			t.Error("pipeline channel should be closed")
		}
	default:
		// Channel might be closed or empty - try receiving
		// This is a bit tricky because the channel might be closed
	}

	// Check for goroutine leaks
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()

	if after > before+1 { // Allow 1 for test timing variations
		t.Errorf("goroutine leak: %d before, %d after (leaked %d)",
			before, after, after-before)
	}
}

func TestSocketReader_ReadyBeforeAccept(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	pipeline := NewPipeline(1, "progress", 100, 0.01)

	reader, err := NewSocketReader(socketPath, pipeline, nil)
	if err != nil {
		t.Fatalf("NewSocketReader failed: %v", err)
	}
	defer reader.Close()

	// Track when Ready() is closed
	var readyTime time.Time
	var readySet atomic.Bool

	go func() {
		<-reader.Ready()
		readyTime = time.Now()
		readySet.Store(true)
	}()

	// Start reader
	startTime := time.Now()
	go reader.Run()

	// Wait for Ready to be set
	timeout := time.After(time.Second)
	for !readySet.Load() {
		select {
		case <-timeout:
			t.Fatal("Ready() was never closed")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	// Ready should be closed very quickly after Run starts
	if readyTime.Sub(startTime) > 100*time.Millisecond {
		t.Errorf("Ready() took too long to close: %v", readyTime.Sub(startTime))
	}
}

func TestSocketReader_ConnectGraceTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timeout test in short mode")
	}

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	pipeline := NewPipeline(1, "progress", 100, 0.01)

	reader, err := NewSocketReader(socketPath, pipeline, nil)
	if err != nil {
		t.Fatalf("NewSocketReader failed: %v", err)
	}
	defer reader.Close()

	start := time.Now()
	done := make(chan struct{})
	go func() {
		reader.Run()
		close(done)
	}()

	<-reader.Ready()

	// Don't connect - let it timeout
	<-done

	elapsed := time.Since(start)

	// Should timeout around socketConnectGrace (3s)
	if elapsed < 2*time.Second || elapsed > 5*time.Second {
		t.Errorf("Expected timeout around 3s, got %v", elapsed)
	}

	if !reader.FailedToConnect() {
		t.Error("FailedToConnect() should return true after timeout")
	}
}

func TestSocketReader_ConcurrentClients(t *testing.T) {
	t.Parallel()

	const numClients = 10
	var wg sync.WaitGroup
	errors := make(chan error, numClients)

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			socketPath := filepath.Join(t.TempDir(), fmt.Sprintf("test_%d.sock", clientID))
			pipeline := NewPipeline(clientID, "progress", 100, 0.01)

			reader, err := NewSocketReader(socketPath, pipeline, nil)
			if err != nil {
				errors <- fmt.Errorf("client %d: NewSocketReader failed: %w", clientID, err)
				return
			}
			defer reader.Close()

			done := make(chan struct{})
			go func() {
				reader.Run()
				close(done)
			}()

			<-reader.Ready()

			conn, err := net.Dial("unix", socketPath)
			if err != nil {
				errors <- fmt.Errorf("client %d: connect failed: %w", clientID, err)
				return
			}

			fmt.Fprintf(conn, "client=%d\n", clientID)
			conn.Close()

			<-done

			_, linesRead, _ := reader.Stats()
			if linesRead != 1 {
				errors <- fmt.Errorf("client %d: expected 1 line, got %d", clientID, linesRead)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}
}

func TestSocketReader_CloseIdempotent(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "test.sock")
	pipeline := NewPipeline(1, "progress", 100, 0.01)

	reader, err := NewSocketReader(socketPath, pipeline, nil)
	if err != nil {
		t.Fatalf("NewSocketReader failed: %v", err)
	}

	// Close multiple times - should not panic
	for i := 0; i < 5; i++ {
		err := reader.Close()
		if err != nil {
			t.Errorf("Close() #%d returned error: %v", i, err)
		}
	}
}

func TestSocketReader_ImplementsLineSource(t *testing.T) {
	t.Parallel()

	// Compile-time check that SocketReader implements LineSource
	var _ LineSource = (*SocketReader)(nil)
}

func TestPipeReader_ImplementsLineSource(t *testing.T) {
	t.Parallel()

	// Compile-time check that PipeReader implements LineSource
	var _ LineSource = (*PipeReader)(nil)
}

// Benchmarks

func BenchmarkSocketReader_Throughput(b *testing.B) {
	socketPath := filepath.Join(b.TempDir(), "bench.sock")
	pipeline := NewPipeline(1, "progress", 10000, 0.01)

	reader, err := NewSocketReader(socketPath, pipeline, nil)
	if err != nil {
		b.Fatalf("NewSocketReader failed: %v", err)
	}
	defer reader.Close()

	done := make(chan struct{})
	go func() {
		reader.Run()
		close(done)
	}()

	<-reader.Ready()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		b.Fatalf("Failed to connect: %v", err)
	}

	// Prepare test line
	line := "frame=100 fps=30.0 bitrate=2000.0kbits/s total_size=12345678 out_time_us=12345678 speed=1.00x progress=continue\n"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conn.Write([]byte(line))
	}
	b.StopTimer()

	conn.Close()
	<-done

	_, linesRead, _ := reader.Stats()
	b.ReportMetric(float64(linesRead), "lines")
}

func BenchmarkSocketReader_Latency(b *testing.B) {
	socketPath := filepath.Join(b.TempDir(), "bench.sock")
	pipeline := NewPipeline(1, "progress", 10000, 0.01)

	reader, err := NewSocketReader(socketPath, pipeline, nil)
	if err != nil {
		b.Fatalf("NewSocketReader failed: %v", err)
	}
	defer reader.Close()

	done := make(chan struct{})
	go func() {
		reader.Run()
		close(done)
	}()

	<-reader.Ready()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		b.Fatalf("Failed to connect: %v", err)
	}
	defer func() {
		conn.Close()
		<-done
	}()

	line := "frame=100\n"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conn.Write([]byte(line))
	}
}
