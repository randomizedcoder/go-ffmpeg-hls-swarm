// Package orchestrator provides the core orchestration logic for go-ffmpeg-hls-swarm.
package orchestrator

import (
	"context"
	"time"

	"github.com/randomizedcoder/go-ffmpeg-hls-swarm/internal/supervisor"
)

// RampScheduler controls the rate at which clients are started.
// It ensures clients don't all start at once (thundering herd)
// and adds per-client jitter to prevent synchronization.
type RampScheduler struct {
	rate       int                      // clients per second
	maxJitter  time.Duration            // maximum jitter per client
	jitter     *supervisor.JitterSource // deterministic jitter source
}

// NewRampScheduler creates a new scheduler with the given rate and jitter.
func NewRampScheduler(rate int, maxJitter time.Duration) *RampScheduler {
	return &RampScheduler{
		rate:      rate,
		maxJitter: maxJitter,
		jitter:    supervisor.NewJitterSourceFromTime(),
	}
}

// NewRampSchedulerWithSeed creates a scheduler with a specific seed for reproducibility.
func NewRampSchedulerWithSeed(rate int, maxJitter time.Duration, seed int64) *RampScheduler {
	return &RampScheduler{
		rate:      rate,
		maxJitter: maxJitter,
		jitter:    supervisor.NewJitterSource(seed),
	}
}

// Schedule waits the appropriate amount of time before starting client N.
// Returns nil on success, or context error if cancelled.
func (r *RampScheduler) Schedule(ctx context.Context, clientID int) error {
	// Calculate base delay from rate
	// rate=5 means 1 client per 200ms
	var baseDelay time.Duration
	if r.rate > 0 {
		baseDelay = time.Second / time.Duration(r.rate)
	}

	// Add per-client jitter
	jitter := r.jitter.ClientJitter(clientID, r.maxJitter)

	// Total delay
	totalDelay := baseDelay + jitter

	// Wait
	if totalDelay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(totalDelay):
			return nil
		}
	}

	return nil
}

// ScheduleImmediate returns immediately without waiting.
// Useful for the first client.
func (r *RampScheduler) ScheduleImmediate() {
	// No-op, returns immediately
}

// EstimatedRampDuration returns the estimated time to start all clients.
func (r *RampScheduler) EstimatedRampDuration(totalClients int) time.Duration {
	if r.rate <= 0 {
		return 0
	}
	// Time = clients / rate + avg jitter
	baseTime := time.Duration(totalClients) * time.Second / time.Duration(r.rate)
	avgJitter := r.maxJitter / 2
	return baseTime + avgJitter
}

// Rate returns the configured rate (clients per second).
func (r *RampScheduler) Rate() int {
	return r.rate
}

// MaxJitter returns the configured maximum jitter.
func (r *RampScheduler) MaxJitter() time.Duration {
	return r.maxJitter
}
