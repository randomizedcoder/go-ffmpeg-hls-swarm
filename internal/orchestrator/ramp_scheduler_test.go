package orchestrator

import (
	"context"
	"testing"
	"time"
)

func TestNewRampScheduler(t *testing.T) {
	rs := NewRampScheduler(10, 500*time.Millisecond)
	if rs == nil {
		t.Fatal("NewRampScheduler returned nil")
	}
	if rs.rate != 10 {
		t.Errorf("rate = %d, want 10", rs.rate)
	}
	if rs.maxJitter != 500*time.Millisecond {
		t.Errorf("maxJitter = %v, want 500ms", rs.maxJitter)
	}
}

func TestNewRampSchedulerWithSeed(t *testing.T) {
	rs := NewRampSchedulerWithSeed(10, 500*time.Millisecond, 12345)
	if rs == nil {
		t.Fatal("NewRampSchedulerWithSeed returned nil")
	}
	if rs.jitter == nil {
		t.Error("jitter source should not be nil")
	}
}

func TestRampScheduler_Rate(t *testing.T) {
	rs := NewRampScheduler(42, 0)
	if rs.Rate() != 42 {
		t.Errorf("Rate() = %d, want 42", rs.Rate())
	}
}

func TestRampScheduler_MaxJitter(t *testing.T) {
	rs := NewRampScheduler(10, 123*time.Millisecond)
	if rs.MaxJitter() != 123*time.Millisecond {
		t.Errorf("MaxJitter() = %v, want 123ms", rs.MaxJitter())
	}
}

func TestRampScheduler_ScheduleImmediate(t *testing.T) {
	rs := NewRampScheduler(10, 500*time.Millisecond)

	start := time.Now()
	rs.ScheduleImmediate()
	elapsed := time.Since(start)

	// Should return immediately (less than 1ms)
	if elapsed > time.Millisecond {
		t.Errorf("ScheduleImmediate took too long: %v", elapsed)
	}
}

func TestRampScheduler_Schedule_RateLimit(t *testing.T) {
	// Rate of 5 = 200ms per client
	rs := NewRampSchedulerWithSeed(5, 0, 12345) // No jitter

	ctx := context.Background()

	start := time.Now()
	err := rs.Schedule(ctx, 1)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Schedule returned error: %v", err)
	}

	// Should wait approximately 200ms (rate=5 means 1 client per 200ms)
	// Allow some margin for timing
	if elapsed < 150*time.Millisecond || elapsed > 300*time.Millisecond {
		t.Errorf("Schedule elapsed = %v, want ~200ms", elapsed)
	}
}

func TestRampScheduler_Schedule_WithJitter(t *testing.T) {
	// Rate of 10 = 100ms per client, jitter up to 50ms
	rs := NewRampSchedulerWithSeed(10, 50*time.Millisecond, 12345)

	ctx := context.Background()

	start := time.Now()
	err := rs.Schedule(ctx, 1)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Schedule returned error: %v", err)
	}

	// Should wait 100ms base + up to 50ms jitter (but jitter is capped at 50% of baseDelay = 50ms)
	if elapsed < 100*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Errorf("Schedule elapsed = %v, want 100-200ms", elapsed)
	}
}

func TestRampScheduler_Schedule_ContextCancelled(t *testing.T) {
	rs := NewRampScheduler(1, 0) // Very slow rate: 1 per second

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	start := time.Now()
	err := rs.Schedule(ctx, 1)
	elapsed := time.Since(start)

	// Should return immediately with context.Canceled
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got: %v", err)
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("Should have returned immediately, took %v", elapsed)
	}
}

func TestRampScheduler_Schedule_ContextTimeout(t *testing.T) {
	rs := NewRampScheduler(1, 0) // Very slow rate: 1 per second

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := rs.Schedule(ctx, 1)
	elapsed := time.Since(start)

	// Should timeout after ~50ms
	if err != context.DeadlineExceeded {
		t.Errorf("Expected context.DeadlineExceeded, got: %v", err)
	}
	if elapsed < 40*time.Millisecond || elapsed > 100*time.Millisecond {
		t.Errorf("Should have timed out at ~50ms, took %v", elapsed)
	}
}

func TestRampScheduler_Schedule_ZeroRate(t *testing.T) {
	// NOTE: With rate=0, baseDelay is 0 but jitter is NOT capped (since baseDelay=0)
	// This means jitter still applies fully, which could be considered a bug
	// or intentional for thundering herd prevention
	rs := NewRampScheduler(0, 100*time.Millisecond)

	ctx := context.Background()

	start := time.Now()
	err := rs.Schedule(ctx, 1)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Schedule returned error: %v", err)
	}

	// Jitter still applies when rate=0 (up to maxJitter)
	if elapsed > 150*time.Millisecond {
		t.Errorf("Zero rate with 100ms jitter should be <=150ms, took %v", elapsed)
	}
}

func TestRampScheduler_Schedule_ZeroRate_NoJitter(t *testing.T) {
	// With rate=0 and no jitter, should return immediately
	rs := NewRampScheduler(0, 0)

	ctx := context.Background()

	start := time.Now()
	err := rs.Schedule(ctx, 1)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Schedule returned error: %v", err)
	}

	// Should be immediate
	if elapsed > 10*time.Millisecond {
		t.Errorf("Zero rate with no jitter should be immediate, took %v", elapsed)
	}
}

func TestRampScheduler_Schedule_NegativeRate(t *testing.T) {
	// With negative rate, baseDelay is 0, but jitter still applies
	// This could be considered a bug or intentional (jitter still helps prevent thundering herd)
	rs := NewRampScheduler(-5, 100*time.Millisecond)

	ctx := context.Background()

	start := time.Now()
	err := rs.Schedule(ctx, 1)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Schedule returned error: %v", err)
	}

	// Negative rate: baseDelay=0 but jitter is uncapped (since baseDelay=0)
	// So it will wait up to maxJitter
	// This is arguably a bug - should negative rate mean no delay at all?
	if elapsed > 150*time.Millisecond {
		t.Errorf("Negative rate with 100ms jitter should be <=150ms, took %v", elapsed)
	}
}

func TestRampScheduler_Schedule_NegativeRate_NoJitter(t *testing.T) {
	// With negative rate and no jitter, should return immediately
	rs := NewRampScheduler(-5, 0)

	ctx := context.Background()

	start := time.Now()
	err := rs.Schedule(ctx, 1)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Schedule returned error: %v", err)
	}

	// Should be immediate
	if elapsed > 10*time.Millisecond {
		t.Errorf("Negative rate with no jitter should be immediate, took %v", elapsed)
	}
}

func TestRampScheduler_Schedule_ZeroJitter(t *testing.T) {
	rs := NewRampScheduler(10, 0) // No jitter

	ctx := context.Background()

	// Schedule multiple clients - timing should be consistent
	for i := 0; i < 3; i++ {
		start := time.Now()
		err := rs.Schedule(ctx, i)
		elapsed := time.Since(start)

		if err != nil {
			t.Errorf("Schedule returned error: %v", err)
		}

		// Should wait approximately 100ms (rate=10)
		if elapsed < 80*time.Millisecond || elapsed > 150*time.Millisecond {
			t.Errorf("Client %d: Schedule elapsed = %v, want ~100ms", i, elapsed)
		}
	}
}

func TestRampScheduler_Schedule_HighRate(t *testing.T) {
	rs := NewRampScheduler(1000, 0) // Very high rate: 1ms per client

	ctx := context.Background()

	start := time.Now()
	err := rs.Schedule(ctx, 1)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Schedule returned error: %v", err)
	}

	// Should be very fast
	if elapsed > 10*time.Millisecond {
		t.Errorf("High rate should be fast, took %v", elapsed)
	}
}

func TestRampScheduler_EstimatedRampDuration_ZeroRate(t *testing.T) {
	rs := NewRampScheduler(0, 500*time.Millisecond)

	dur := rs.EstimatedRampDuration(100)
	if dur != 0 {
		t.Errorf("EstimatedRampDuration with rate=0 should be 0, got %v", dur)
	}
}

func TestRampScheduler_EstimatedRampDuration_NegativeRate(t *testing.T) {
	rs := NewRampScheduler(-5, 500*time.Millisecond)

	dur := rs.EstimatedRampDuration(100)
	if dur != 0 {
		t.Errorf("EstimatedRampDuration with negative rate should be 0, got %v", dur)
	}
}

func TestRampScheduler_EstimatedRampDuration_Normal(t *testing.T) {
	rs := NewRampScheduler(10, 1*time.Second) // 10 per second, 1s max jitter

	dur := rs.EstimatedRampDuration(100)

	// Expected: 100 clients / 10 per sec = 10s + 0.5s avg jitter = 10.5s
	expected := 10*time.Second + 500*time.Millisecond
	if dur != expected {
		t.Errorf("EstimatedRampDuration = %v, want %v", dur, expected)
	}
}

func TestRampScheduler_EstimatedRampDuration_ZeroClients(t *testing.T) {
	rs := NewRampScheduler(10, 1*time.Second)

	dur := rs.EstimatedRampDuration(0)

	// 0 clients should still add avg jitter
	expected := 500 * time.Millisecond // avg jitter only
	if dur != expected {
		t.Errorf("EstimatedRampDuration(0) = %v, want %v", dur, expected)
	}
}

func TestRampScheduler_DeterministicJitter(t *testing.T) {
	// Same seed should produce same jitter sequence
	rs1 := NewRampSchedulerWithSeed(10, 100*time.Millisecond, 12345)
	rs2 := NewRampSchedulerWithSeed(10, 100*time.Millisecond, 12345)

	ctx := context.Background()

	// Schedule same client ID on both - should take same time
	start1 := time.Now()
	_ = rs1.Schedule(ctx, 5)
	elapsed1 := time.Since(start1)

	start2 := time.Now()
	_ = rs2.Schedule(ctx, 5)
	elapsed2 := time.Since(start2)

	// Should be very close (within 10ms)
	diff := elapsed1 - elapsed2
	if diff < 0 {
		diff = -diff
	}
	if diff > 15*time.Millisecond {
		t.Errorf("Same seed should produce similar timing: %v vs %v (diff=%v)", elapsed1, elapsed2, diff)
	}
}

func TestRampScheduler_JitterCapping(t *testing.T) {
	// With high rate and high jitter, jitter should be capped at 50% of baseDelay
	// rate=100 means baseDelay=10ms, so jitter should be capped at 5ms
	rs := NewRampSchedulerWithSeed(100, 1*time.Second, 12345) // 1s jitter but should be capped

	ctx := context.Background()

	var maxElapsed time.Duration
	for i := 0; i < 10; i++ {
		start := time.Now()
		_ = rs.Schedule(ctx, i)
		elapsed := time.Since(start)
		if elapsed > maxElapsed {
			maxElapsed = elapsed
		}
	}

	// With capped jitter, max should be around 10ms + 5ms = 15ms
	// Allow some margin
	if maxElapsed > 50*time.Millisecond {
		t.Errorf("Jitter should be capped, max elapsed = %v", maxElapsed)
	}
}
