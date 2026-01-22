package supervisor

import (
	"testing"
	"time"
)

// =============================================================================
// Table-Driven Tests: DefaultBackoffConfig
// =============================================================================

func TestDefaultBackoffConfig(t *testing.T) {
	cfg := DefaultBackoffConfig()

	if cfg.Initial != 250*time.Millisecond {
		t.Errorf("Initial = %v, want 250ms", cfg.Initial)
	}
	if cfg.Max != 5*time.Second {
		t.Errorf("Max = %v, want 5s", cfg.Max)
	}
	if cfg.Multiplier != 1.7 {
		t.Errorf("Multiplier = %v, want 1.7", cfg.Multiplier)
	}
	if cfg.JitterPct != 0.4 {
		t.Errorf("JitterPct = %v, want 0.4", cfg.JitterPct)
	}
}

// =============================================================================
// Table-Driven Tests: NewBackoff
// =============================================================================

func TestNewBackoff(t *testing.T) {
	tests := []struct {
		name       string
		clientID   int
		configSeed int64
		cfg        BackoffConfig
	}{
		{
			name:       "default config",
			clientID:   0,
			configSeed: 12345,
			cfg:        DefaultBackoffConfig(),
		},
		{
			name:       "custom config",
			clientID:   42,
			configSeed: 99999,
			cfg: BackoffConfig{
				Initial:    100 * time.Millisecond,
				Max:        10 * time.Second,
				Multiplier: 2.0,
				JitterPct:  0.2,
			},
		},
		{
			name:       "zero jitter",
			clientID:   1,
			configSeed: 0,
			cfg: BackoffConfig{
				Initial:    time.Second,
				Max:        time.Minute,
				Multiplier: 1.5,
				JitterPct:  0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewBackoff(tt.clientID, tt.configSeed, tt.cfg)

			if b == nil {
				t.Fatal("NewBackoff returned nil")
			}
			if b.attempts != 0 {
				t.Errorf("initial attempts = %d, want 0", b.attempts)
			}
			if b.config.Initial != tt.cfg.Initial {
				t.Errorf("config.Initial = %v, want %v", b.config.Initial, tt.cfg.Initial)
			}
			if b.rng == nil {
				t.Error("rng is nil")
			}
		})
	}
}

// =============================================================================
// Table-Driven Tests: Backoff.Calculate (no jitter)
// =============================================================================

func TestBackoff_Calculate_NoJitter(t *testing.T) {
	tests := []struct {
		name     string
		attempts int
		initial  time.Duration
		max      time.Duration
		mult     float64
		want     time.Duration
	}{
		{
			name:     "attempt 0",
			attempts: 0,
			initial:  100 * time.Millisecond,
			max:      10 * time.Second,
			mult:     2.0,
			want:     100 * time.Millisecond,
		},
		{
			name:     "attempt 1",
			attempts: 1,
			initial:  100 * time.Millisecond,
			max:      10 * time.Second,
			mult:     2.0,
			want:     200 * time.Millisecond,
		},
		{
			name:     "attempt 2",
			attempts: 2,
			initial:  100 * time.Millisecond,
			max:      10 * time.Second,
			mult:     2.0,
			want:     400 * time.Millisecond,
		},
		{
			name:     "attempt 3",
			attempts: 3,
			initial:  100 * time.Millisecond,
			max:      10 * time.Second,
			mult:     2.0,
			want:     800 * time.Millisecond,
		},
		{
			name:     "capped at max",
			attempts: 10,
			initial:  100 * time.Millisecond,
			max:      1 * time.Second,
			mult:     2.0,
			want:     1 * time.Second, // Would be 102.4s without cap
		},
		{
			name:     "multiplier 1.5",
			attempts: 2,
			initial:  100 * time.Millisecond,
			max:      10 * time.Second,
			mult:     1.5,
			want:     225 * time.Millisecond, // 100 * 1.5^2 = 225
		},
		{
			name:     "multiplier 1.0 (no growth)",
			attempts: 5,
			initial:  100 * time.Millisecond,
			max:      10 * time.Second,
			mult:     1.0,
			want:     100 * time.Millisecond, // Always same
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := BackoffConfig{
				Initial:    tt.initial,
				Max:        tt.max,
				Multiplier: tt.mult,
				JitterPct:  0, // No jitter for deterministic tests
			}
			b := NewBackoff(0, 0, cfg)
			b.SetAttempts(tt.attempts)

			got := b.Calculate()
			if got != tt.want {
				t.Errorf("Calculate() = %v, want %v", got, tt.want)
			}
		})
	}
}

// =============================================================================
// Table-Driven Tests: Backoff.Next
// =============================================================================

func TestBackoff_Next(t *testing.T) {
	cfg := BackoffConfig{
		Initial:    100 * time.Millisecond,
		Max:        10 * time.Second,
		Multiplier: 2.0,
		JitterPct:  0,
	}
	b := NewBackoff(0, 0, cfg)

	// First call
	d1 := b.Next()
	if d1 != 100*time.Millisecond {
		t.Errorf("Next() #1 = %v, want 100ms", d1)
	}
	if b.Attempts() != 1 {
		t.Errorf("Attempts() after #1 = %d, want 1", b.Attempts())
	}

	// Second call
	d2 := b.Next()
	if d2 != 200*time.Millisecond {
		t.Errorf("Next() #2 = %v, want 200ms", d2)
	}
	if b.Attempts() != 2 {
		t.Errorf("Attempts() after #2 = %d, want 2", b.Attempts())
	}

	// Third call
	d3 := b.Next()
	if d3 != 400*time.Millisecond {
		t.Errorf("Next() #3 = %v, want 400ms", d3)
	}
	if b.Attempts() != 3 {
		t.Errorf("Attempts() after #3 = %d, want 3", b.Attempts())
	}
}

// =============================================================================
// Table-Driven Tests: Backoff.Reset
// =============================================================================

func TestBackoff_Reset(t *testing.T) {
	cfg := BackoffConfig{
		Initial:    100 * time.Millisecond,
		Max:        10 * time.Second,
		Multiplier: 2.0,
		JitterPct:  0,
	}
	b := NewBackoff(0, 0, cfg)

	// Make some attempts
	b.Next()
	b.Next()
	b.Next()

	if b.Attempts() != 3 {
		t.Errorf("Attempts() before reset = %d, want 3", b.Attempts())
	}

	// Reset
	b.Reset()

	if b.Attempts() != 0 {
		t.Errorf("Attempts() after reset = %d, want 0", b.Attempts())
	}

	// Next should be back to initial
	d := b.Next()
	if d != 100*time.Millisecond {
		t.Errorf("Next() after reset = %v, want 100ms", d)
	}
}

// =============================================================================
// Table-Driven Tests: Backoff.SetAttempts
// =============================================================================

func TestBackoff_SetAttempts(t *testing.T) {
	tests := []struct {
		name     string
		set      int
		wantCalc time.Duration
	}{
		{"set to 0", 0, 100 * time.Millisecond},
		{"set to 1", 1, 200 * time.Millisecond},
		{"set to 5", 5, 3200 * time.Millisecond}, // 100 * 2^5 = 3200
		{"set to 10 (capped)", 10, 10 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := BackoffConfig{
				Initial:    100 * time.Millisecond,
				Max:        10 * time.Second,
				Multiplier: 2.0,
				JitterPct:  0,
			}
			b := NewBackoff(0, 0, cfg)
			b.SetAttempts(tt.set)

			if b.Attempts() != tt.set {
				t.Errorf("Attempts() = %d, want %d", b.Attempts(), tt.set)
			}
			if got := b.Calculate(); got != tt.wantCalc {
				t.Errorf("Calculate() = %v, want %v", got, tt.wantCalc)
			}
		})
	}
}

// =============================================================================
// Tests: Jitter Behavior
// =============================================================================

func TestBackoff_Jitter(t *testing.T) {
	cfg := BackoffConfig{
		Initial:    1 * time.Second,
		Max:        10 * time.Second,
		Multiplier: 1.0, // No growth, just jitter
		JitterPct:  0.4, // ±20%
	}

	// Test that different client IDs produce different jitter
	b1 := NewBackoff(1, 12345, cfg)
	b2 := NewBackoff(2, 12345, cfg)

	// Get several samples
	var samples1, samples2 []time.Duration
	for i := 0; i < 10; i++ {
		samples1 = append(samples1, b1.Calculate())
		samples2 = append(samples2, b2.Calculate())
	}

	// Samples should be different (different seeds)
	allSame := true
	for i := range samples1 {
		if samples1[i] != samples2[i] {
			allSame = false
			break
		}
	}
	if allSame {
		t.Error("different client IDs should produce different jitter")
	}

	// All samples should be within ±20% of base (1s)
	for i, d := range samples1 {
		if d < 800*time.Millisecond || d > 1200*time.Millisecond {
			t.Errorf("sample1[%d] = %v, want between 800ms and 1200ms", i, d)
		}
	}
}

func TestBackoff_DeterministicJitter(t *testing.T) {
	cfg := BackoffConfig{
		Initial:    1 * time.Second,
		Max:        10 * time.Second,
		Multiplier: 1.0,
		JitterPct:  0.4,
	}

	// Same client ID and seed should produce same sequence
	b1 := NewBackoff(42, 12345, cfg)
	b2 := NewBackoff(42, 12345, cfg)

	for i := 0; i < 10; i++ {
		d1 := b1.Calculate()
		d2 := b2.Calculate()
		if d1 != d2 {
			t.Errorf("iteration %d: d1=%v != d2=%v (should be deterministic)", i, d1, d2)
		}
	}
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestBackoff_ZeroInitial(t *testing.T) {
	cfg := BackoffConfig{
		Initial:    0,
		Max:        10 * time.Second,
		Multiplier: 2.0,
		JitterPct:  0,
	}
	b := NewBackoff(0, 0, cfg)

	// Should return 0
	if d := b.Calculate(); d != 0 {
		t.Errorf("Calculate() with zero initial = %v, want 0", d)
	}
}

func TestBackoff_VeryLargeAttempts(t *testing.T) {
	cfg := BackoffConfig{
		Initial:    100 * time.Millisecond,
		Max:        5 * time.Second,
		Multiplier: 2.0,
		JitterPct:  0,
	}
	b := NewBackoff(0, 0, cfg)
	b.SetAttempts(1000) // Very large

	// Should be capped at max
	if d := b.Calculate(); d != 5*time.Second {
		t.Errorf("Calculate() with 1000 attempts = %v, want 5s (capped)", d)
	}
}

func TestBackoff_NegativeAttempts(t *testing.T) {
	cfg := BackoffConfig{
		Initial:    100 * time.Millisecond,
		Max:        5 * time.Second,
		Multiplier: 2.0,
		JitterPct:  0,
	}
	b := NewBackoff(0, 0, cfg)
	b.SetAttempts(-1) // Negative

	// Should handle gracefully (math.Pow with negative exponent)
	d := b.Calculate()
	// 100ms * 2^-1 = 50ms
	if d != 50*time.Millisecond {
		t.Errorf("Calculate() with -1 attempts = %v, want 50ms", d)
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkBackoff_Calculate(b *testing.B) {
	cfg := DefaultBackoffConfig()
	backoff := NewBackoff(0, 12345, cfg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = backoff.Calculate()
	}
}

func BenchmarkBackoff_Next(b *testing.B) {
	cfg := DefaultBackoffConfig()
	backoff := NewBackoff(0, 12345, cfg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = backoff.Next()
		if backoff.Attempts() > 100 {
			backoff.Reset()
		}
	}
}
