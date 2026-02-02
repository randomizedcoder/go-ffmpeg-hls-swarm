package supervisor

import (
	"math/rand"
	"time"
)

// JitterSource provides deterministic, per-client jitter values.
// Using a per-client seed ensures that clients maintain their relative
// timing offsets across restarts, preventing synchronization.
type JitterSource struct {
	configSeed int64
}

// NewJitterSource creates a new jitter source with the given config seed.
// The config seed should be consistent across a test run but can vary
// between runs to get different timing patterns.
func NewJitterSource(configSeed int64) *JitterSource {
	return &JitterSource{
		configSeed: configSeed,
	}
}

// NewJitterSourceFromTime creates a jitter source seeded from the current time.
func NewJitterSourceFromTime() *JitterSource {
	return NewJitterSource(time.Now().UnixNano())
}

// ForClient returns a random number generator seeded for a specific client.
// The same clientID will always produce the same sequence of random values.
func (j *JitterSource) ForClient(clientID int) *rand.Rand {
	seed := int64(clientID) ^ j.configSeed
	return rand.New(rand.NewSource(seed))
}

// ClientJitter returns a jitter duration for a specific client within [0, maxJitter).
func (j *JitterSource) ClientJitter(clientID int, maxJitter time.Duration) time.Duration {
	rng := j.ForClient(clientID)
	if maxJitter <= 0 {
		return 0
	}
	return time.Duration(rng.Int63n(int64(maxJitter)))
}
