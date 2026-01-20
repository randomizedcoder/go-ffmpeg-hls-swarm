package supervisor

import (
	"math"
	"math/rand"
	"time"
)

// BackoffConfig holds the configuration for exponential backoff.
type BackoffConfig struct {
	Initial    time.Duration // Initial backoff delay (default: 250ms)
	Max        time.Duration // Maximum backoff delay (default: 5s)
	Multiplier float64       // Multiplier for each attempt (default: 1.7)
	JitterPct  float64       // Jitter as a percentage of delay (default: 0.4 = ±20%)
}

// DefaultBackoffConfig returns sensible defaults for backoff.
func DefaultBackoffConfig() BackoffConfig {
	return BackoffConfig{
		Initial:    250 * time.Millisecond,
		Max:        5 * time.Second,
		Multiplier: 1.7,
		JitterPct:  0.4, // ±20% jitter
	}
}

// Backoff calculates exponential backoff delays with jitter.
// Each instance is tied to a specific client for deterministic jitter.
type Backoff struct {
	config   BackoffConfig
	attempts int
	rng      *rand.Rand
}

// NewBackoff creates a new Backoff calculator for a specific client.
// The clientID and configSeed are used to create deterministic jitter.
func NewBackoff(clientID int, configSeed int64, cfg BackoffConfig) *Backoff {
	seed := int64(clientID) ^ configSeed
	return &Backoff{
		config:   cfg,
		attempts: 0,
		rng:      rand.New(rand.NewSource(seed)),
	}
}

// Next returns the next backoff delay and increments the attempt counter.
func (b *Backoff) Next() time.Duration {
	delay := b.Calculate()
	b.attempts++
	return delay
}

// Calculate returns the current backoff delay without incrementing attempts.
func (b *Backoff) Calculate() time.Duration {
	// Calculate base delay: initial * multiplier^attempts
	delay := float64(b.config.Initial) * math.Pow(b.config.Multiplier, float64(b.attempts))

	// Cap at maximum
	if delay > float64(b.config.Max) {
		delay = float64(b.config.Max)
	}

	// Add jitter: ±(JitterPct/2) of the delay
	// e.g., JitterPct=0.4 means ±20% jitter
	if b.config.JitterPct > 0 {
		jitterRange := delay * b.config.JitterPct
		jitter := jitterRange*b.rng.Float64() - jitterRange/2
		delay += jitter
	}

	// Ensure non-negative
	if delay < 0 {
		delay = 0
	}

	return time.Duration(delay)
}

// Reset resets the attempt counter to zero.
func (b *Backoff) Reset() {
	b.attempts = 0
}

// Attempts returns the current attempt count.
func (b *Backoff) Attempts() int {
	return b.attempts
}

// SetAttempts sets the attempt counter (useful for testing or recovery).
func (b *Backoff) SetAttempts(n int) {
	b.attempts = n
}

// BackoffResetThreshold is the minimum uptime before backoff is reset.
// If a client runs for longer than this, it's considered stable and
// the backoff counter resets on the next failure.
const BackoffResetThreshold = 30 * time.Second

// ShouldReset determines if backoff should be reset based on uptime and exit code.
func ShouldReset(uptime time.Duration, exitCode int) bool {
	// Reset if client ran for a reasonable time (indicates transient issue resolved)
	if uptime >= BackoffResetThreshold {
		return true
	}

	// Reset on clean exit (code 0) - expected for VOD streams or duration limits
	if exitCode == 0 {
		return true
	}

	return false
}
