package parser

import (
	"math"
	"sync/atomic"
)

// ThroughputHistogram is a lock-free histogram for per-client throughput tracking.
// Uses atomic counters for O(1) recording with no locks.
// Buckets cover 1 KB/s to 10 GB/s in logarithmic steps (64 buckets).
//
// IMPORTANT: Use Drain() not Snapshot() for aggregation!
// Drain() resets counters so each aggregation window only contains recent samples.
// Snapshot() would accumulate all historical data, distorting percentiles.
type ThroughputHistogram struct {
	buckets [64]atomic.Uint64
	count   atomic.Uint64
	sum     atomic.Uint64 // For average calculation (scaled to KB/s)
}

// NewThroughputHistogram creates a new histogram.
func NewThroughputHistogram() *ThroughputHistogram {
	return &ThroughputHistogram{}
}

// Record adds a throughput sample (bytes/sec) to the histogram.
// Lock-free, safe for concurrent use from hot path.
func (h *ThroughputHistogram) Record(bytesPerSec float64) {
	bucket := h.bucketFor(bytesPerSec)
	h.buckets[bucket].Add(1)
	h.count.Add(1)
	// Store sum in KB/s to avoid overflow
	h.sum.Add(uint64(bytesPerSec / 1024))
}

// bucketFor returns the bucket index for a throughput value.
// Uses logarithmic bucketing: bucket = log2(bytesPerSec / 1024)
// Covers 1 KB/s (bucket 0) to ~10 GB/s (bucket 63)
func (h *ThroughputHistogram) bucketFor(bytesPerSec float64) int {
	if bytesPerSec < 1024 {
		return 0
	}
	bucket := int(math.Log2(bytesPerSec / 1024))
	if bucket > 63 {
		bucket = 63
	}
	return bucket
}

// Drain returns bucket counts AND RESETS them to zero atomically.
// This ensures each aggregation window only contains samples since the last Drain().
//
// CRITICAL: Use this instead of Snapshot() for aggregation!
// If you use Snapshot() (which doesn't reset), you'll re-add all historical
// counts on every aggregation cycle, causing:
// - Exploding TDigest weights
// - Distorted percentiles (old data dominates)
// - Growing CPU usage
func (h *ThroughputHistogram) Drain() [64]uint64 {
	var drained [64]uint64
	for i := range h.buckets {
		// Swap returns old value and sets to 0 atomically
		drained[i] = h.buckets[i].Swap(0)
	}
	// Also reset count and sum for the new window
	h.count.Swap(0)
	h.sum.Swap(0)
	return drained
}

// Count returns total samples recorded (since last Drain).
func (h *ThroughputHistogram) Count() uint64 {
	return h.count.Load()
}

// Sum returns the sum of samples in KB/s (since last Drain).
func (h *ThroughputHistogram) Sum() uint64 {
	return h.sum.Load()
}

// BucketToValue converts a bucket index to a representative value (bytes/sec).
func BucketToValue(bucket int) float64 {
	// Midpoint of bucket in log space
	return 1024 * math.Pow(2, float64(bucket)+0.5)
}

// PercentileFromBuckets computes a percentile from drained histogram buckets.
// The buckets can be from a single histogram or merged from multiple histograms.
// Uses linear interpolation within bucket ranges for better accuracy.
func PercentileFromBuckets(buckets [64]uint64, p float64) float64 {
	// Count total samples
	var total uint64
	for _, count := range buckets {
		total += count
	}
	if total == 0 {
		return 0
	}

	// Target rank for percentile
	target := p * float64(total)

	// Walk through buckets to find the target rank
	var cumulative uint64
	for i, count := range buckets {
		if count == 0 {
			continue
		}
		prevCumulative := cumulative
		cumulative += count

		if float64(cumulative) >= target {
			// Target is in this bucket - interpolate
			bucketStart := 1024 * math.Pow(2, float64(i))
			bucketEnd := 1024 * math.Pow(2, float64(i+1))

			// How far into this bucket is the target?
			bucketProgress := (target - float64(prevCumulative)) / float64(count)

			// Linear interpolation in log space for more accuracy
			logStart := math.Log2(bucketStart)
			logEnd := math.Log2(bucketEnd)
			logValue := logStart + bucketProgress*(logEnd-logStart)

			return math.Pow(2, logValue)
		}
	}

	// Should not reach here, but return max bucket value
	return BucketToValue(63)
}

// MergeBuckets merges multiple drained histogram bucket arrays into one.
func MergeBuckets(histograms ...[64]uint64) [64]uint64 {
	var merged [64]uint64
	for _, h := range histograms {
		for i, count := range h {
			merged[i] += count
		}
	}
	return merged
}
