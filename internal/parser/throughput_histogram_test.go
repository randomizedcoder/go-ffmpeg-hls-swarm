package parser

import (
	"math"
	"sync"
	"sync/atomic"
	"testing"
)

func TestThroughputHistogram_Drain(t *testing.T) {
	h := NewThroughputHistogram()

	// Record some samples
	h.Record(1024 * 1024)      // 1 MB/s -> bucket ~10
	h.Record(10 * 1024 * 1024) // 10 MB/s -> bucket ~13
	h.Record(50 * 1024 * 1024) // 50 MB/s -> bucket ~15

	// First drain should return counts
	drained1 := h.Drain()
	total1 := uint64(0)
	for _, count := range drained1 {
		total1 += count
	}
	if total1 != 3 {
		t.Errorf("First drain: total = %d, want 3", total1)
	}

	// Second drain should return zeros (buckets were reset)
	drained2 := h.Drain()
	total2 := uint64(0)
	for _, count := range drained2 {
		total2 += count
	}
	if total2 != 0 {
		t.Errorf("Second drain: total = %d, want 0 (should be reset)", total2)
	}

	// Record more samples
	h.Record(100 * 1024 * 1024) // 100 MB/s

	// Third drain should only have the new sample
	drained3 := h.Drain()
	total3 := uint64(0)
	for _, count := range drained3 {
		total3 += count
	}
	if total3 != 1 {
		t.Errorf("Third drain: total = %d, want 1", total3)
	}
}

func TestThroughputHistogram_DrainConcurrent(t *testing.T) {
	h := NewThroughputHistogram()

	// Concurrent writers
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				h.Record(float64((j + 1) * 1024 * 1024)) // 1-1000 MB/s
			}
		}()
	}
	wg.Wait()

	// Drain should get all 10,000 samples
	drained := h.Drain()
	total := uint64(0)
	for _, count := range drained {
		total += count
	}
	if total != 10000 {
		t.Errorf("Concurrent drain: total = %d, want 10000", total)
	}
}

func TestThroughputHistogram_BucketFor(t *testing.T) {
	h := NewThroughputHistogram()

	tests := []struct {
		bytesPerSec float64
		wantBucket  int
	}{
		{512, 0},           // < 1 KB/s
		{1024, 0},          // 1 KB/s (bucket 0)
		{2048, 1},          // 2 KB/s
		{1024 * 1024, 10},  // 1 MB/s
		{10 * 1024 * 1024, 13}, // 10 MB/s
		{100 * 1024 * 1024, 16}, // 100 MB/s
		{1024 * 1024 * 1024, 20}, // 1 GB/s
		{10 * 1024 * 1024 * 1024, 23}, // 10 GB/s
		{1e15, 39}, // Very large value (log2(1e15/1024) ≈ 39.8)
	}

	for _, tt := range tests {
		got := h.bucketFor(tt.bytesPerSec)
		if got != tt.wantBucket {
			t.Errorf("bucketFor(%v) = %d, want %d", tt.bytesPerSec, got, tt.wantBucket)
		}
	}
}

func TestThroughputHistogram_CountAndSum(t *testing.T) {
	h := NewThroughputHistogram()

	// Record samples
	h.Record(1024 * 1024)      // 1 MB/s = 1024 KB/s
	h.Record(2 * 1024 * 1024)  // 2 MB/s = 2048 KB/s
	h.Record(3 * 1024 * 1024)  // 3 MB/s = 3072 KB/s

	if got := h.Count(); got != 3 {
		t.Errorf("Count() = %d, want 3", got)
	}

	// Sum should be 1024 + 2048 + 3072 = 6144 KB/s
	if got := h.Sum(); got != 6144 {
		t.Errorf("Sum() = %d, want 6144", got)
	}

	// After drain, count and sum should be reset
	h.Drain()
	if got := h.Count(); got != 0 {
		t.Errorf("Count() after drain = %d, want 0", got)
	}
	if got := h.Sum(); got != 0 {
		t.Errorf("Sum() after drain = %d, want 0", got)
	}
}

// TestMaxThroughputConcurrent verifies the CAS loop correctly tracks max under concurrency
func TestMaxThroughputConcurrent(t *testing.T) {
	var maxThroughput atomic.Uint64

	// updateMax mimics the CAS loop from recordThroughput
	updateMax := func(newVal float64) {
		newBits := math.Float64bits(newVal)
		for {
			oldBits := maxThroughput.Load()
			oldVal := math.Float64frombits(oldBits)
			if newVal <= oldVal {
				return
			}
			if maxThroughput.CompareAndSwap(oldBits, newBits) {
				return
			}
		}
	}

	// Concurrent updates with known max
	var wg sync.WaitGroup
	// Max value: j=99 + id=99*10 = 99 + 990 = 1089
	expectedMax := 1089.0
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				// Each goroutine writes values 0-99, plus its ID*10
				val := float64(j + id*10)
				updateMax(val)
			}
		}(i)
	}
	wg.Wait()

	gotBits := maxThroughput.Load()
	gotMax := math.Float64frombits(gotBits)
	if gotMax != expectedMax {
		t.Errorf("max = %v, want %v", gotMax, expectedMax)
	}
}

func TestBucketToValue(t *testing.T) {
	// Verify bucket values are in expected ranges
	for bucket := 0; bucket <= 63; bucket++ {
		val := BucketToValue(bucket)
		if val <= 0 {
			t.Errorf("BucketToValue(%d) = %v, want > 0", bucket, val)
		}
	}

	// Bucket 0 should be around 1.4 KB/s (midpoint in log space)
	if val := BucketToValue(0); val < 1024 || val > 2048 {
		t.Errorf("BucketToValue(0) = %v, want ~1.4 KB/s", val)
	}

	// Bucket 10 should be around 1.4 MB/s
	if val := BucketToValue(10); val < 1024*1024 || val > 2*1024*1024 {
		t.Errorf("BucketToValue(10) = %v, want ~1.4 MB/s", val)
	}
}

func BenchmarkThroughputHistogram_Record(b *testing.B) {
	h := NewThroughputHistogram()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Record(float64((i%100 + 1) * 1024 * 1024)) // 1-100 MB/s
	}
}

func BenchmarkThroughputHistogram_Record_Parallel(b *testing.B) {
	h := NewThroughputHistogram()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			h.Record(float64((i%100 + 1) * 1024 * 1024))
			i++
		}
	})
}

func TestPercentileFromBuckets_Empty(t *testing.T) {
	var buckets [64]uint64
	if got := PercentileFromBuckets(buckets, 0.50); got != 0 {
		t.Errorf("P50 of empty = %v, want 0", got)
	}
}

func TestPercentileFromBuckets_SingleBucket(t *testing.T) {
	var buckets [64]uint64
	buckets[10] = 100 // All samples in bucket 10 (~1-2 MB/s)

	// Bucket 10 corresponds to ~1 MB/s to ~2 MB/s range
	p50 := PercentileFromBuckets(buckets, 0.50)
	// Should be midpoint of bucket 10
	if p50 < 1024*1024 || p50 > 2*1024*1024 {
		t.Errorf("P50 = %v, want ~1-2 MB/s", p50)
	}
}

func TestPercentileFromBuckets_TwoBuckets(t *testing.T) {
	var buckets [64]uint64
	buckets[10] = 50 // 50 samples in bucket 10 (~1 MB/s)
	buckets[13] = 50 // 50 samples in bucket 13 (~10 MB/s)

	// P50 should be at the boundary (50th sample)
	p50 := PercentileFromBuckets(buckets, 0.50)
	// Should be in bucket 10 or at its end
	if p50 < 1024*1024 || p50 > 2*1024*1024 {
		t.Errorf("P50 = %v, want ~1-2 MB/s (in bucket 10)", p50)
	}

	// P75 should be in bucket 13
	p75 := PercentileFromBuckets(buckets, 0.75)
	if p75 < 8*1024*1024 || p75 > 16*1024*1024 {
		t.Errorf("P75 = %v, want ~10 MB/s (in bucket 13)", p75)
	}
}

func TestPercentileFromBuckets_Distribution(t *testing.T) {
	var buckets [64]uint64
	// Create a spread distribution
	buckets[5] = 10  // ~32 KB/s
	buckets[10] = 30 // ~1 MB/s
	buckets[15] = 40 // ~32 MB/s
	buckets[20] = 20 // ~1 GB/s
	// Total: 100 samples

	p25 := PercentileFromBuckets(buckets, 0.25)
	p50 := PercentileFromBuckets(buckets, 0.50)
	p75 := PercentileFromBuckets(buckets, 0.75)
	p99 := PercentileFromBuckets(buckets, 0.99)

	// P25 at sample 25: should be in bucket 10
	if p25 < 512*1024 || p25 > 2*1024*1024 {
		t.Errorf("P25 = %v, expected around bucket 10", p25)
	}

	// P50 at sample 50: should be in bucket 15 (40 after first 40)
	if p50 < 16*1024*1024 || p50 > 64*1024*1024 {
		t.Errorf("P50 = %v, expected around bucket 15", p50)
	}

	// P75 at sample 75: should be in bucket 15
	if p75 < 16*1024*1024 || p75 > 64*1024*1024 {
		t.Errorf("P75 = %v, expected around bucket 15", p75)
	}

	// P99 at sample 99: should be in bucket 20
	if p99 < 512*1024*1024 || p99 > 2*1024*1024*1024 {
		t.Errorf("P99 = %v, expected around bucket 20", p99)
	}

	// Percentiles should be monotonically increasing
	if p25 > p50 || p50 > p75 || p75 > p99 {
		t.Errorf("Percentiles not monotonic: P25=%v, P50=%v, P75=%v, P99=%v", p25, p50, p75, p99)
	}
}

func TestMergeBuckets(t *testing.T) {
	var h1, h2, h3 [64]uint64
	h1[10] = 100
	h2[10] = 50
	h2[15] = 25
	h3[20] = 30

	merged := MergeBuckets(h1, h2, h3)

	if merged[10] != 150 {
		t.Errorf("merged[10] = %d, want 150", merged[10])
	}
	if merged[15] != 25 {
		t.Errorf("merged[15] = %d, want 25", merged[15])
	}
	if merged[20] != 30 {
		t.Errorf("merged[20] = %d, want 30", merged[20])
	}
}

func TestMergeBuckets_Empty(t *testing.T) {
	merged := MergeBuckets()
	var expected [64]uint64
	if merged != expected {
		t.Error("MergeBuckets() with no args should return zero array")
	}
}
