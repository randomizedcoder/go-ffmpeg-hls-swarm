package timeseries

import (
	"sync"
	"testing"
	"time"
)

// mockClock provides deterministic time for testing.
type mockClock struct {
	mu   sync.Mutex
	time time.Time
}

func newMockClock(t time.Time) *mockClock {
	return &mockClock{time: t}
}

func (c *mockClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.time
}

func (c *mockClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.time = c.time.Add(d)
}

func (c *mockClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.time = t
}

// TestThroughputTracker_AddBytes tests basic byte accumulation using table-driven tests.
func TestThroughputTracker_AddBytes(t *testing.T) {
	tests := []struct {
		name     string
		adds     []int64
		expected int64
	}{
		{
			name:     "single add",
			adds:     []int64{1024},
			expected: 1024,
		},
		{
			name:     "multiple adds",
			adds:     []int64{100, 200, 300},
			expected: 600,
		},
		{
			name:     "large values",
			adds:     []int64{1_000_000, 2_000_000, 3_000_000},
			expected: 6_000_000,
		},
		{
			name:     "mixed sizes",
			adds:     []int64{1, 10, 100, 1000, 10000},
			expected: 11111,
		},
		{
			name:     "zero value ignored",
			adds:     []int64{100, 0, 200},
			expected: 300,
		},
		{
			name:     "negative value ignored",
			adds:     []int64{100, -50, 200},
			expected: 300,
		},
		{
			name:     "empty",
			adds:     []int64{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := newMockClock(time.Now())
			tracker := NewThroughputTrackerWithClock(clock)

			for _, n := range tt.adds {
				tracker.AddBytes(n)
			}

			stats := tracker.GetStats()
			if stats.TotalBytes != tt.expected {
				t.Errorf("TotalBytes = %d, want %d", stats.TotalBytes, tt.expected)
			}
		})
	}
}

// TestThroughputTracker_RollingAverage tests average calculation for various patterns.
func TestThroughputTracker_RollingAverage(t *testing.T) {
	t.Run("constant rate", func(t *testing.T) {
		baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		clock := newMockClock(baseTime)
		tracker := NewThroughputTrackerWithClock(clock)

		// Simulate 100 bytes/second for 10 seconds
		for i := 0; i < 10; i++ {
			tracker.AddBytes(100)
			clock.Advance(1 * time.Second)
			tracker.RecordSample()
		}

		stats := tracker.GetStats()

		// Should be approximately 100 bytes/sec
		if stats.Avg1s < 90 || stats.Avg1s > 110 {
			t.Errorf("Avg1s = %f, want ~100", stats.Avg1s)
		}

		// Overall should also be ~100 bytes/sec
		if stats.AvgOverall < 90 || stats.AvgOverall > 110 {
			t.Errorf("AvgOverall = %f, want ~100", stats.AvgOverall)
		}
	})

	t.Run("increasing rate", func(t *testing.T) {
		baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		clock := newMockClock(baseTime)
		tracker := NewThroughputTrackerWithClock(clock)

		// Simulate increasing rate: 100, 200, 300, ... bytes/sec
		for i := 1; i <= 10; i++ {
			tracker.AddBytes(int64(i * 100))
			clock.Advance(1 * time.Second)
			tracker.RecordSample()
		}

		stats := tracker.GetStats()

		// Last 1s should be close to 1000 (the last increment)
		if stats.Avg1s < 900 || stats.Avg1s > 1100 {
			t.Errorf("Avg1s = %f, want ~1000", stats.Avg1s)
		}

		// Total bytes = 100+200+...+1000 = 5500
		if stats.TotalBytes != 5500 {
			t.Errorf("TotalBytes = %d, want 5500", stats.TotalBytes)
		}
	})

	t.Run("burst then idle", func(t *testing.T) {
		baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		clock := newMockClock(baseTime)
		tracker := NewThroughputTrackerWithClock(clock)

		// Big burst at start
		tracker.AddBytes(10000)
		tracker.RecordSample()

		// Then idle for 10 seconds
		for i := 0; i < 10; i++ {
			clock.Advance(1 * time.Second)
			tracker.RecordSample()
		}

		stats := tracker.GetStats()

		// 1s average should be ~0 (no bytes in last second)
		if stats.Avg1s > 1 {
			t.Errorf("Avg1s = %f, want ~0", stats.Avg1s)
		}

		// Overall should reflect the burst spread over time
		if stats.TotalBytes != 10000 {
			t.Errorf("TotalBytes = %d, want 10000", stats.TotalBytes)
		}
	})
}

// TestThroughputTracker_WindowEdgeCases tests edge cases for window calculations.
func TestThroughputTracker_WindowEdgeCases(t *testing.T) {
	t.Run("fresh tracker has zero rates", func(t *testing.T) {
		clock := newMockClock(time.Now())
		tracker := NewThroughputTrackerWithClock(clock)

		stats := tracker.GetStats()

		if stats.TotalBytes != 0 {
			t.Errorf("TotalBytes = %d, want 0", stats.TotalBytes)
		}
		if stats.Avg1s != 0 {
			t.Errorf("Avg1s = %f, want 0", stats.Avg1s)
		}
	})

	t.Run("single sample", func(t *testing.T) {
		baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		clock := newMockClock(baseTime)
		tracker := NewThroughputTrackerWithClock(clock)

		tracker.AddBytes(1000)
		clock.Advance(1 * time.Second)
		tracker.RecordSample()

		stats := tracker.GetStats()

		if stats.TotalBytes != 1000 {
			t.Errorf("TotalBytes = %d, want 1000", stats.TotalBytes)
		}
		// With 1000 bytes over 1 second, should be ~1000 bytes/sec
		if stats.Avg1s < 900 || stats.Avg1s > 1100 {
			t.Errorf("Avg1s = %f, want ~1000", stats.Avg1s)
		}
	})

	t.Run("window boundary exact", func(t *testing.T) {
		baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		clock := newMockClock(baseTime)
		tracker := NewThroughputTrackerWithClock(clock)

		// Add exactly 30 samples (30 seconds)
		for i := 0; i < 30; i++ {
			tracker.AddBytes(100)
			clock.Advance(1 * time.Second)
			tracker.RecordSample()
		}

		stats := tracker.GetStats()

		// Avg30s should be close to 100 bytes/sec
		if stats.Avg30s < 90 || stats.Avg30s > 110 {
			t.Errorf("Avg30s = %f, want ~100", stats.Avg30s)
		}
	})

	t.Run("all windows consistent", func(t *testing.T) {
		baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		clock := newMockClock(baseTime)
		tracker := NewThroughputTrackerWithClock(clock)

		// Add samples for 60 seconds at constant rate
		for i := 0; i < 60; i++ {
			tracker.AddBytes(1000)
			clock.Advance(1 * time.Second)
			tracker.RecordSample()
		}

		stats := tracker.GetStats()

		// All windows should show ~1000 bytes/sec
		windows := []struct {
			name string
			avg  float64
		}{
			{"Avg1s", stats.Avg1s},
			{"Avg30s", stats.Avg30s},
			{"Avg60s", stats.Avg60s},
			{"AvgOverall", stats.AvgOverall},
		}

		for _, w := range windows {
			if w.avg < 900 || w.avg > 1100 {
				t.Errorf("%s = %f, want ~1000", w.name, w.avg)
			}
		}
	})
}

// TestThroughputTracker_RingBufferOverflow tests buffer wraparound correctness.
func TestThroughputTracker_RingBufferOverflow(t *testing.T) {
	t.Run("buffer fills exactly", func(t *testing.T) {
		baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		clock := newMockClock(baseTime)
		tracker := NewThroughputTrackerWithClock(clock)

		// Fill buffer exactly (initial sample + 299 more = 300)
		for i := 0; i < ringBufferSize-1; i++ {
			tracker.AddBytes(100)
			clock.Advance(1 * time.Second)
			tracker.RecordSample()
		}

		if tracker.SampleCount() != ringBufferSize {
			t.Errorf("SampleCount = %d, want %d", tracker.SampleCount(), ringBufferSize)
		}
	})

	t.Run("buffer overflows", func(t *testing.T) {
		baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		clock := newMockClock(baseTime)
		tracker := NewThroughputTrackerWithClock(clock)

		// Fill buffer + extra samples (300 + 50 = 350)
		for i := 0; i < ringBufferSize+50; i++ {
			tracker.AddBytes(100)
			clock.Advance(1 * time.Second)
			tracker.RecordSample()
		}

		// Buffer should still be at max capacity
		if tracker.SampleCount() != ringBufferSize {
			t.Errorf("SampleCount = %d, want %d (buffer should not grow)", tracker.SampleCount(), ringBufferSize)
		}

		stats := tracker.GetStats()

		// Should still work correctly
		if stats.TotalBytes != int64(ringBufferSize+50)*100 {
			t.Errorf("TotalBytes = %d, want %d", stats.TotalBytes, (ringBufferSize+50)*100)
		}

		// 300s average should be ~100 bytes/sec
		if stats.Avg300s < 90 || stats.Avg300s > 110 {
			t.Errorf("Avg300s = %f, want ~100", stats.Avg300s)
		}
	})

	t.Run("buffer wraps multiple times", func(t *testing.T) {
		baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		clock := newMockClock(baseTime)
		tracker := NewThroughputTrackerWithClock(clock)

		// Run for 10 minutes (600 seconds, 2x buffer size)
		for i := 0; i < 600; i++ {
			tracker.AddBytes(1000)
			clock.Advance(1 * time.Second)
			tracker.RecordSample()
		}

		if tracker.SampleCount() != ringBufferSize {
			t.Errorf("SampleCount = %d, want %d", tracker.SampleCount(), ringBufferSize)
		}

		stats := tracker.GetStats()

		// Avg300s should be ~1000 bytes/sec (last 5 minutes)
		if stats.Avg300s < 900 || stats.Avg300s > 1100 {
			t.Errorf("Avg300s = %f, want ~1000", stats.Avg300s)
		}
	})
}

// TestThroughputTracker_ConcurrentAddBytes tests thread safety with many concurrent writers.
func TestThroughputTracker_ConcurrentAddBytes(t *testing.T) {
	clock := newMockClock(time.Now())
	tracker := NewThroughputTrackerWithClock(clock)

	const goroutines = 100
	const addsPerGoroutine = 1000
	const bytesPerAdd = int64(100)

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < addsPerGoroutine; j++ {
				tracker.AddBytes(bytesPerAdd)
			}
		}()
	}

	wg.Wait()

	stats := tracker.GetStats()
	expected := int64(goroutines * addsPerGoroutine * bytesPerAdd)

	if stats.TotalBytes != expected {
		t.Errorf("TotalBytes = %d, want %d (lost bytes in concurrent access)", stats.TotalBytes, expected)
	}
}

// TestThroughputTracker_ConcurrentAddAndRead tests concurrent writers and readers.
func TestThroughputTracker_ConcurrentAddAndRead(t *testing.T) {
	clock := newMockClock(time.Now())
	tracker := NewThroughputTrackerWithClock(clock)

	const writers = 10
	const readers = 10
	const opsPerGoroutine = 1000

	var wg sync.WaitGroup
	wg.Add(writers + readers)

	// Writers
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				tracker.AddBytes(100)
			}
		}()
	}

	// Readers
	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				stats := tracker.GetStats()
				// Just ensure we can read without panic
				_ = stats.TotalBytes
				_ = stats.Avg1s
			}
		}()
	}

	wg.Wait()

	// Verify final count is correct
	stats := tracker.GetStats()
	expected := int64(writers * opsPerGoroutine * 100)

	if stats.TotalBytes != expected {
		t.Errorf("TotalBytes = %d, want %d", stats.TotalBytes, expected)
	}
}

// TestThroughputTracker_ConcurrentSampling tests concurrent AddBytes and RecordSample.
func TestThroughputTracker_ConcurrentSampling(t *testing.T) {
	clock := newMockClock(time.Now())
	tracker := NewThroughputTrackerWithClock(clock)

	const duration = 100 * time.Millisecond

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Writer goroutines
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					tracker.AddBytes(100)
				}
			}
		}()
	}

	// Sampler goroutine (like the real ticker)
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				clock.Advance(10 * time.Millisecond)
				tracker.RecordSample()
			}
		}
	}()

	// Reader goroutine (like the TUI)
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				stats := tracker.GetStats()
				_ = stats.Avg1s
			}
		}
	}()

	time.Sleep(duration)
	close(done)
	wg.Wait()

	// Should complete without race conditions or panics
	stats := tracker.GetStats()
	if stats.TotalBytes == 0 {
		t.Error("TotalBytes should be > 0 after concurrent operations")
	}
}

// TestThroughputTracker_TUIDoesNotFlash is the KEY test ensuring stats are always available.
func TestThroughputTracker_TUIDoesNotFlash(t *testing.T) {
	t.Run("stats available immediately after bytes added", func(t *testing.T) {
		baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		clock := newMockClock(baseTime)
		tracker := NewThroughputTrackerWithClock(clock)

		// Add some bytes
		tracker.AddBytes(1000)

		// Advance time slightly and record sample
		clock.Advance(500 * time.Millisecond)
		tracker.RecordSample()

		stats := tracker.GetStats()

		// Should have non-zero data (the key fix for flashing)
		if stats.TotalBytes == 0 {
			t.Error("TotalBytes should be available immediately")
		}
		if stats.Avg1s == 0 {
			t.Error("Avg1s should be available immediately (not zero)")
		}
	})

	t.Run("stats never become zero after data exists", func(t *testing.T) {
		baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		clock := newMockClock(baseTime)
		tracker := NewThroughputTrackerWithClock(clock)

		// Add bytes and record sample
		tracker.AddBytes(10000)
		clock.Advance(1 * time.Second)
		tracker.RecordSample()

		// Simulate TUI polling every 500ms for 10 seconds
		for i := 0; i < 20; i++ {
			clock.Advance(500 * time.Millisecond)

			stats := tracker.GetStats()

			// TotalBytes should never go back to zero
			if stats.TotalBytes == 0 {
				t.Errorf("TotalBytes became 0 at poll %d", i)
			}

			// At least one average should be non-zero (no "(no data)" in TUI)
			if stats.Avg1s == 0 && stats.Avg30s == 0 && stats.Avg60s == 0 && stats.Avg300s == 0 && stats.AvgOverall == 0 {
				t.Errorf("All averages are 0 at poll %d - TUI would flash!", i)
			}
		}
	})

	t.Run("simulated TUI 500ms tick pattern", func(t *testing.T) {
		baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		clock := newMockClock(baseTime)
		tracker := NewThroughputTrackerWithClock(clock)

		// Simulate realistic pattern:
		// - Segments complete sporadically
		// - TUI polls every 500ms
		// - Sample records every 1s

		segmentSizes := []int64{0, 1024, 0, 2048, 0, 0, 4096, 0, 0, 0}
		tuiFlashCount := 0

		for i, size := range segmentSizes {
			if size > 0 {
				tracker.AddBytes(size)
			}

			// TUI poll at 500ms
			clock.Advance(500 * time.Millisecond)
			stats1 := tracker.GetStats()

			// Sample at 1s
			clock.Advance(500 * time.Millisecond)
			tracker.RecordSample()
			stats2 := tracker.GetStats()

			// Check for flashing (both stats having zero averages)
			if i > 0 { // Skip first tick where no data exists yet
				if stats1.TotalBytes > 0 && stats1.Avg1s == 0 && stats1.Avg30s == 0 && stats1.AvgOverall == 0 {
					tuiFlashCount++
				}
				if stats2.TotalBytes > 0 && stats2.Avg1s == 0 && stats2.Avg30s == 0 && stats2.AvgOverall == 0 {
					tuiFlashCount++
				}
			}
		}

		if tuiFlashCount > 0 {
			t.Errorf("TUI would flash %d times - averages went to 0 while data existed", tuiFlashCount)
		}
	})
}

// TestThroughputTracker_Reset tests the Reset functionality.
func TestThroughputTracker_Reset(t *testing.T) {
	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := newMockClock(baseTime)
	tracker := NewThroughputTrackerWithClock(clock)

	// Add data
	for i := 0; i < 100; i++ {
		tracker.AddBytes(1000)
		clock.Advance(1 * time.Second)
		tracker.RecordSample()
	}

	// Verify data exists
	stats := tracker.GetStats()
	if stats.TotalBytes == 0 {
		t.Error("Should have data before reset")
	}

	// Reset
	tracker.Reset()

	// Verify data cleared
	stats = tracker.GetStats()
	if stats.TotalBytes != 0 {
		t.Errorf("TotalBytes = %d after reset, want 0", stats.TotalBytes)
	}
	if stats.Avg1s != 0 {
		t.Errorf("Avg1s = %f after reset, want 0", stats.Avg1s)
	}
	if tracker.SampleCount() != 1 {
		t.Errorf("SampleCount = %d after reset, want 1 (initial sample)", tracker.SampleCount())
	}
}

// TestThroughputTracker_Accuracy tests mathematical accuracy of average calculations.
func TestThroughputTracker_Accuracy(t *testing.T) {
	t.Run("exact 1 second window", func(t *testing.T) {
		baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		clock := newMockClock(baseTime)
		tracker := NewThroughputTrackerWithClock(clock)

		// Add exactly 1000 bytes
		tracker.AddBytes(1000)

		// Advance exactly 1 second and sample
		clock.Advance(1 * time.Second)
		tracker.RecordSample()

		stats := tracker.GetStats()

		// Should be exactly 1000 bytes/sec
		if stats.Avg1s != 1000.0 {
			t.Errorf("Avg1s = %f, want 1000.0", stats.Avg1s)
		}
	})

	t.Run("exact 30 second window", func(t *testing.T) {
		baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		clock := newMockClock(baseTime)
		tracker := NewThroughputTrackerWithClock(clock)

		// Add 30000 bytes over 30 seconds (1000/sec)
		for i := 0; i < 30; i++ {
			tracker.AddBytes(1000)
			clock.Advance(1 * time.Second)
			tracker.RecordSample()
		}

		stats := tracker.GetStats()

		// Should be exactly 1000 bytes/sec
		tolerance := 1.0 // Allow tiny floating point variance
		if stats.Avg30s < 1000.0-tolerance || stats.Avg30s > 1000.0+tolerance {
			t.Errorf("Avg30s = %f, want ~1000.0", stats.Avg30s)
		}
	})
}

// BenchmarkThroughputTracker_AddBytes benchmarks the AddBytes hot path.
// Target: <50ns
func BenchmarkThroughputTracker_AddBytes(b *testing.B) {
	tracker := NewThroughputTracker()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tracker.AddBytes(1024)
	}
}

// BenchmarkThroughputTracker_GetStats benchmarks getting stats.
// Target: <1µs
func BenchmarkThroughputTracker_GetStats(b *testing.B) {
	tracker := NewThroughputTracker()

	// Pre-fill with some data
	for i := 0; i < 100; i++ {
		tracker.AddBytes(1000)
		tracker.RecordSample()
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = tracker.GetStats()
	}
}

// BenchmarkThroughputTracker_RecordSample benchmarks sample recording.
func BenchmarkThroughputTracker_RecordSample(b *testing.B) {
	tracker := NewThroughputTracker()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tracker.RecordSample()
	}
}

// BenchmarkThroughputTracker_FullBuffer benchmarks GetStats with a full buffer.
func BenchmarkThroughputTracker_FullBuffer(b *testing.B) {
	clock := newMockClock(time.Now())
	tracker := NewThroughputTrackerWithClock(clock)

	// Fill the buffer completely
	for i := 0; i < ringBufferSize; i++ {
		tracker.AddBytes(1000)
		clock.Advance(1 * time.Second)
		tracker.RecordSample()
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = tracker.GetStats()
	}
}

// BenchmarkThroughputTracker_ConcurrentAddBytes benchmarks concurrent adds.
func BenchmarkThroughputTracker_ConcurrentAddBytes(b *testing.B) {
	tracker := NewThroughputTracker()

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tracker.AddBytes(1024)
		}
	})
}
