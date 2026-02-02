package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestParseSegmentNumber tests the primary backward scan implementation
func TestParseSegmentNumber(t *testing.T) {
	tests := []struct {
		name       string
		filename   string
		expectedN  int64
		expectedOK bool
	}{
		{"standard format", "seg00017.ts", 17, true},
		{"high number", "seg99999.ts", 99999, true},
		{"zero padded", "seg00000.ts", 0, true},
		{"no padding", "seg5.ts", 5, true},
		{"different prefix", "segment_123.ts", 123, true},
		{"chunk prefix", "chunk-42.ts", 42, true},
		{"underscore prefix", "stream_00001.ts", 1, true},
		{"manifest file", "stream.m3u8", 0, false},
		{"no number", "video.ts", 0, false},
		{"empty", "", 0, false},
		{"different extension", "seg00001.mp4", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			num, ok := parseSegmentNumber(tt.filename)
			if ok != tt.expectedOK {
				t.Errorf("parseSegmentNumber(%q) ok = %v, want %v", tt.filename, ok, tt.expectedOK)
			}
			if num != tt.expectedN {
				t.Errorf("parseSegmentNumber(%q) = %d, want %d", tt.filename, num, tt.expectedN)
			}
		})
	}
}

// TestParseSegmentNumberFixed tests the fixed offset implementation (benchmark only)
func TestParseSegmentNumberFixed(t *testing.T) {
	tests := []struct {
		name       string
		filename   string
		expectedN  int64
		expectedOK bool
	}{
		{"standard format", "seg00017.ts", 17, true},
		{"high number", "seg99999.ts", 99999, true},
		{"zero padded", "seg00000.ts", 0, true},
		{"manifest file", "stream.m3u8", 0, false},
		{"wrong length", "seg001.ts", 0, false},       // Fixed requires exactly 11 chars
		{"too long", "seg000001.ts", 0, false},        // Fixed requires exactly 11 chars
		{"different prefix", "segment_123.ts", 0, false}, // Fixed only works with seg%05d
		{"empty", "", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			num, ok := parseSegmentNumberFixed(tt.filename)
			if ok != tt.expectedOK {
				t.Errorf("parseSegmentNumberFixed(%q) ok = %v, want %v", tt.filename, ok, tt.expectedOK)
			}
			if num != tt.expectedN {
				t.Errorf("parseSegmentNumberFixed(%q) = %d, want %d", tt.filename, num, tt.expectedN)
			}
		})
	}
}

func TestSegmentScraper_GetSegmentSize(t *testing.T) {
	scraper := NewSegmentScraper("http://test", 5*time.Second, 500*time.Millisecond, 30, nil)

	// Pre-populate cache
	scraper.segmentSizes.Store("seg00001.ts", int64(1281032))
	scraper.segmentSizes.Store("seg00002.ts", int64(1297764))

	tests := []struct {
		name         string
		lookup       string
		expectedSize int64
		expectedOK   bool
	}{
		{"existing segment", "seg00001.ts", 1281032, true},
		{"another existing", "seg00002.ts", 1297764, true},
		{"non-existent", "seg00003.ts", 0, false},
		{"manifest", "stream.m3u8", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			size, ok := scraper.GetSegmentSize(tt.lookup)
			if ok != tt.expectedOK {
				t.Errorf("GetSegmentSize(%q) ok = %v, want %v", tt.lookup, ok, tt.expectedOK)
			}
			if size != tt.expectedSize {
				t.Errorf("GetSegmentSize(%q) = %d, want %d", tt.lookup, size, tt.expectedSize)
			}
		})
	}
}

// TestSegmentScraper_CacheEviction verifies "keep last N segments inclusive" semantics.
// Formula: threshold = highest - windowSize + 1, evict if num < threshold
// This ensures exactly windowSize segments are kept (not windowSize+1).
func TestSegmentScraper_CacheEviction(t *testing.T) {
	tests := []struct {
		name              string
		windowSize        int64
		segments          []string
		expectedHighest   int64
		expectedInCache   []string
		expectedEvicted   []string
		expectedCacheSize int // Must equal windowSize (or less if fewer segments)
	}{
		{
			name:              "no eviction when under window",
			windowSize:        30,
			segments:          []string{"seg00001.ts", "seg00002.ts", "seg00003.ts"},
			expectedHighest:   3,
			expectedInCache:   []string{"seg00001.ts", "seg00002.ts", "seg00003.ts"},
			expectedEvicted:   []string{},
			expectedCacheSize: 3, // Less than windowSize, no eviction
		},
		{
			// windowSize=5, highest=8
			// threshold = 8 - 5 + 1 = 4
			// keep: [4,8] = 4,5,6,7,8 (5 segments)
			// evict: <4 = 1,2,3 (3 segments)
			name:       "eviction when exceeding window",
			windowSize: 5,
			segments: []string{
				"seg00001.ts", "seg00002.ts", "seg00003.ts", "seg00004.ts",
				"seg00005.ts", "seg00006.ts", "seg00007.ts", "seg00008.ts",
			},
			expectedHighest:   8,
			expectedInCache:   []string{"seg00004.ts", "seg00005.ts", "seg00006.ts", "seg00007.ts", "seg00008.ts"},
			expectedEvicted:   []string{"seg00001.ts", "seg00002.ts", "seg00003.ts"},
			expectedCacheSize: 5, // Exactly windowSize
		},
		{
			// windowSize=30, highest=49
			// threshold = 49 - 30 + 1 = 20
			// keep: [20,49] = 30 segments
			// evict: [0,19] = 20 segments
			name:              "50 segments with window 30",
			windowSize:        30,
			segments:          generateSegmentNames(0, 50),
			expectedHighest:   49,
			expectedInCache:   generateSegmentNames(20, 50), // seg00020-seg00049
			expectedEvicted:   generateSegmentNames(0, 20),  // seg00000-seg00019
			expectedCacheSize: 30,                           // Exactly windowSize
		},
		{
			// windowSize=30, highest=99
			// threshold = 99 - 30 + 1 = 70
			// keep: [70,99] = 30 segments
			// evict: [0,69] = 70 segments
			name:              "100 segments with window 30",
			windowSize:        30,
			segments:          generateSegmentNames(0, 100),
			expectedHighest:   99,
			expectedInCache:   generateSegmentNames(70, 100), // seg00070-seg00099
			expectedEvicted:   generateSegmentNames(0, 70),   // seg00000-seg00069
			expectedCacheSize: 30,                            // Exactly windowSize
		},
		{
			// Edge case: windowSize=1, highest=5
			// threshold = 5 - 1 + 1 = 5
			// keep: [5,5] = 1 segment
			// evict: <5 = 1,2,3,4 (4 segments)
			name:              "window size 1 keeps only highest",
			windowSize:        1,
			segments:          generateSegmentNames(1, 6),
			expectedHighest:   5,
			expectedInCache:   []string{"seg00005.ts"},
			expectedEvicted:   []string{"seg00001.ts", "seg00002.ts", "seg00003.ts", "seg00004.ts"},
			expectedCacheSize: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scraper := NewSegmentScraper("http://test", 5*time.Second, 500*time.Millisecond, tt.windowSize, nil)

			// Populate cache and find highest
			var highest int64
			for _, seg := range tt.segments {
				scraper.segmentSizes.Store(seg, int64(1000))
				if num, ok := parseSegmentNumber(seg); ok {
					if num > highest {
						highest = num
					}
				}
			}
			scraper.highestSegNum.Store(highest)
			scraper.evictOldEntries(highest)

			// Verify highest
			if got := scraper.GetHighestSegmentNumber(); got != tt.expectedHighest {
				t.Errorf("highestSegNum = %d, want %d", got, tt.expectedHighest)
			}

			// Verify cache size
			if got := scraper.CacheSize(); got != tt.expectedCacheSize {
				t.Errorf("CacheSize() = %d, want %d", got, tt.expectedCacheSize)
			}

			// Verify cache contents
			for _, seg := range tt.expectedInCache {
				if _, ok := scraper.GetSegmentSize(seg); !ok {
					t.Errorf("expected %q in cache, but not found", seg)
				}
			}
			for _, seg := range tt.expectedEvicted {
				if _, ok := scraper.GetSegmentSize(seg); ok {
					t.Errorf("expected %q to be evicted, but found", seg)
				}
			}
		})
	}
}

func generateSegmentNames(start, end int) []string {
	names := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		names = append(names, fmt.Sprintf("seg%05d.ts", i))
	}
	return names
}

func TestSegmentScraper_HTTPServer(t *testing.T) {
	// Simulate origin JSON response
	serverSegments := []SegmentInfo{
		{Name: "seg00001.ts", Type: "file", Size: 1281032},
		{Name: "seg00002.ts", Type: "file", Size: 1297764},
		{Name: "stream.m3u8", Type: "file", Size: 374}, // Also cached for byte tracking
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(serverSegments)
	}))
	defer server.Close()

	scraper := NewSegmentScraper(server.URL, 100*time.Millisecond, 50*time.Millisecond, 30, nil)

	// Run single scrape
	err := scraper.scrape()
	if err != nil {
		t.Fatalf("scrape() error: %v", err)
	}

	// Verify ALL files are cached (segments AND manifest)
	for _, seg := range serverSegments {
		size, ok := scraper.GetSegmentSize(seg.Name)
		if !ok {
			t.Errorf("file %q should be in cache", seg.Name)
			continue
		}
		if size != seg.Size {
			t.Errorf("file %q size = %d, want %d", seg.Name, size, seg.Size)
		}
	}

	// Verify cache size is 3 (segments + manifest)
	if size := scraper.CacheSize(); size != 3 {
		t.Errorf("CacheSize() = %d, want 3", size)
	}

	// Verify highest segment number (manifest doesn't affect this)
	if highest := scraper.GetHighestSegmentNumber(); highest != 2 {
		t.Errorf("GetHighestSegmentNumber() = %d, want 2", highest)
	}
}

func TestSegmentScraper_ConcurrentReads(t *testing.T) {
	scraper := NewSegmentScraper("http://test", 5*time.Second, 500*time.Millisecond, 30, nil)

	// Pre-populate
	for i := 0; i < 30; i++ {
		scraper.segmentSizes.Store(fmt.Sprintf("seg%05d.ts", i), int64(1000000+i))
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				name := fmt.Sprintf("seg%05d.ts", j%30)
				scraper.GetSegmentSize(name)
			}
		}(i)
	}
	wg.Wait()
}

// ════════════════════════════════════════════════════════════════════════════════
// Race + Concurrency Tests
// ════════════════════════════════════════════════════════════════════════════════
//
// Run with: go test -race ./internal/metrics/... ./internal/parser/... ./internal/stats/...
//
// These tests verify:
// 1. No data races under concurrent access
// 2. Graceful shutdown (no deadlocks on context cancellation)
// 3. Correct behavior under realistic workloads

// TestSegmentScraper_RunConcurrentAccess tests that Run() + concurrent GetSegmentSize
// works correctly and terminates cleanly on context cancellation.
// This catches:
// - Data races between scraper writes and reader reads
// - Deadlocks on shutdown
// - Timer/channel issues
func TestSegmentScraper_RunConcurrentAccess(t *testing.T) {
	// Create a mock server that returns segment data
	segments := []SegmentInfo{
		{Name: "seg00001.ts", Type: "file", Size: 1000000},
		{Name: "seg00002.ts", Type: "file", Size: 1100000},
		{Name: "seg00003.ts", Type: "file", Size: 1200000},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(segments)
	}))
	defer server.Close()

	// Short interval for faster test execution
	scraper := NewSegmentScraper(server.URL, 50*time.Millisecond, 10*time.Millisecond, 30, nil)

	ctx, cancel := context.WithCancel(context.Background())

	// Start scraper in background
	scraperDone := make(chan struct{})
	go func() {
		scraper.Run(ctx)
		close(scraperDone)
	}()

	// Concurrent readers hammering GetSegmentSize
	var wg sync.WaitGroup
	readerCount := 50
	readsPerReader := 1000

	for i := 0; i < readerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < readsPerReader; j++ {
				// Mix of hits and misses
				names := []string{"seg00001.ts", "seg00002.ts", "seg00003.ts", "seg99999.ts"}
				scraper.GetSegmentSize(names[j%len(names)])
			}
		}()
	}

	// Let it run for a bit
	time.Sleep(200 * time.Millisecond)

	// Cancel context - scraper should terminate
	cancel()

	// Wait for scraper to stop (with timeout to detect deadlock)
	select {
	case <-scraperDone:
		// Good - scraper terminated
	case <-time.After(2 * time.Second):
		t.Fatal("scraper did not terminate within 2 seconds - possible deadlock")
	}

	// Wait for readers to finish
	wg.Wait()

	// Verify scraper ran successfully
	if scraper.LastScrape().IsZero() {
		t.Error("scraper never completed a scrape")
	}
}

// TestSegmentScraper_RaceConditions is designed specifically for -race detection.
// It exercises all concurrent code paths simultaneously.
func TestSegmentScraper_RaceConditions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return different data each time to exercise update paths
		segments := []SegmentInfo{
			{Name: fmt.Sprintf("seg%05d.ts", time.Now().UnixNano()%1000), Type: "file", Size: 1000000},
		}
		json.NewEncoder(w).Encode(segments)
	}))
	defer server.Close()

	scraper := NewSegmentScraper(server.URL, 10*time.Millisecond, 5*time.Millisecond, 10, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go scraper.Run(ctx)

	var wg sync.WaitGroup

	// Readers
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				scraper.GetSegmentSize(fmt.Sprintf("seg%05d.ts", j%100))
				scraper.CacheSize()
				scraper.GetHighestSegmentNumber()
				scraper.LastScrape()
				scraper.ScrapeErrors()
				scraper.EvictedCount()
			}
		}()
	}

	wg.Wait()
}

// ════════════════════════════════════════════════════════════════════════════════
// Fuzz Tests
// ════════════════════════════════════════════════════════════════════════════════
//
// Run with: go test -fuzz=FuzzParseSegmentNumber ./internal/metrics/...
//
// Fuzz tests find edge cases like:
// - Panics on unusual input (unicode, very long strings, empty strings)
// - Incorrect behavior on boundary conditions
// - Memory safety issues

// FuzzParseSegmentNumber tests parseSegmentNumber with random inputs.
// Looking for panics, infinite loops, or unexpected behavior.
func FuzzParseSegmentNumber(f *testing.F) {
	// Seed corpus with known patterns
	f.Add("seg00017.ts")
	f.Add("seg99999.ts")
	f.Add("segment_123.ts")
	f.Add("chunk-42.ts")
	f.Add("stream.m3u8")
	f.Add("")
	f.Add("a")
	f.Add(".ts")
	f.Add("0.ts")
	f.Add("seg.ts")
	f.Add("seg00000.ts")
	f.Add(strings.Repeat("a", 10000)) // Very long input
	f.Add("seg" + strings.Repeat("9", 100) + ".ts") // Very large number
	f.Add("日本語.ts") // Unicode
	f.Add("seg\x00001.ts") // Null byte
	f.Add("seg-123.ts")
	f.Add("seg_123.ts")
	f.Add("123.ts")
	f.Add("seg123")     // No extension
	f.Add("seg123.TS")  // Wrong case
	f.Add("seg123.ts.ts") // Double extension

	f.Fuzz(func(t *testing.T, input string) {
		// Should never panic
		num, ok := parseSegmentNumber(input)

		// If ok is true, num should be non-negative
		if ok && num < 0 {
			t.Errorf("parseSegmentNumber(%q) returned negative number: %d", input, num)
		}

		// If input doesn't end with .ts, should return false
		if !strings.HasSuffix(input, ".ts") && ok {
			t.Errorf("parseSegmentNumber(%q) returned true for non-.ts file", input)
		}
	})
}

// ════════════════════════════════════════════════════════════════════════════════
// Property-Style Tests for Eviction Invariants
// ════════════════════════════════════════════════════════════════════════════════
//
// These tests verify invariants that must ALWAYS hold, not just specific scenarios.
// Run after any code change to ensure correctness.

// TestEvictionInvariant_AllCachedSegmentsInWindow verifies that after any
// scrape/evict cycle, ALL cached segments with parseable numbers satisfy:
//
//	num >= highest - windowSize + 1
//
// This is a property that must hold regardless of the specific segments.
func TestEvictionInvariant_AllCachedSegmentsInWindow(t *testing.T) {
	// Property: for any random sequence of segments, after eviction,
	// all numbered segments in cache must be within the window
	testCases := []struct {
		name       string
		windowSize int64
		segments   []int // Segment numbers to add
	}{
		{"sequential", 10, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}},
		{"gaps", 5, []int{1, 5, 10, 15, 20, 25}},
		{"reverse", 5, []int{100, 90, 80, 70, 60}},
		{"random_order", 10, []int{50, 10, 90, 30, 70, 20, 80, 40, 60, 100}},
		{"duplicates", 5, []int{1, 1, 2, 2, 3, 3, 10, 10, 10}},
		{"single_segment", 5, []int{42}},
		{"exactly_window", 5, []int{1, 2, 3, 4, 5}},
		{"window_plus_one", 5, []int{1, 2, 3, 4, 5, 6}},
		{"large_numbers", 10, []int{999990, 999991, 999992, 999993, 999994, 999995, 999996, 999997, 999998, 999999, 1000000}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			scraper := NewSegmentScraper("http://test", 5*time.Second, 500*time.Millisecond, tc.windowSize, nil)

			// Add segments
			var highest int64
			for _, num := range tc.segments {
				name := fmt.Sprintf("seg%05d.ts", num)
				scraper.segmentSizes.Store(name, int64(1000000))
				if int64(num) > highest {
					highest = int64(num)
				}
			}

			// Trigger eviction
			scraper.highestSegNum.Store(highest)
			scraper.evictOldEntries(highest)

			// INVARIANT CHECK: All remaining numbered segments must be >= threshold
			threshold := highest - tc.windowSize + 1
			if threshold < 0 {
				threshold = 0 // Can't have negative threshold
			}

			var violations []string
			var numberedCount int
			scraper.segmentSizes.Range(func(key, value any) bool {
				name := key.(string)
				if num, ok := parseSegmentNumber(name); ok {
					numberedCount++
					if num < threshold {
						violations = append(violations, fmt.Sprintf("%s (num=%d < threshold=%d)", name, num, threshold))
					}
				}
				return true
			})

			if len(violations) > 0 {
				t.Errorf("INVARIANT VIOLATED: segments outside window: %v", violations)
			}

			// INVARIANT CHECK: Numbered segment count <= windowSize
			// (Could be less if we started with fewer segments)
			if numberedCount > int(tc.windowSize) {
				t.Errorf("INVARIANT VIOLATED: numberedCount=%d > windowSize=%d", numberedCount, tc.windowSize)
			}
		})
	}
}

// TestEvictionInvariant_ManifestsNeverEvicted verifies that manifest files
// (files without segment numbers) are never evicted.
func TestEvictionInvariant_ManifestsNeverEvicted(t *testing.T) {
	scraper := NewSegmentScraper("http://test", 5*time.Second, 500*time.Millisecond, 5, nil)

	// Add manifests and segments
	manifests := []string{"stream.m3u8", "master.m3u8", "index.m3u8"}
	for _, m := range manifests {
		scraper.segmentSizes.Store(m, int64(500))
	}

	// Add many segments to trigger eviction
	for i := 1; i <= 100; i++ {
		scraper.segmentSizes.Store(fmt.Sprintf("seg%05d.ts", i), int64(1000000))
	}

	// Trigger eviction
	scraper.highestSegNum.Store(100)
	scraper.evictOldEntries(100)

	// INVARIANT: All manifests must still be present
	for _, m := range manifests {
		if _, ok := scraper.GetSegmentSize(m); !ok {
			t.Errorf("INVARIANT VIOLATED: manifest %q was evicted", m)
		}
	}
}

// TestEvictionInvariant_CacheSizeBounded verifies cache size is always bounded.
func TestEvictionInvariant_CacheSizeBounded(t *testing.T) {
	windowSize := int64(10)
	scraper := NewSegmentScraper("http://test", 5*time.Second, 500*time.Millisecond, windowSize, nil)

	// Simulate many scrape cycles adding segments
	for cycle := 0; cycle < 100; cycle++ {
		// Each cycle adds 15 new segments
		baseNum := int64(cycle * 15)
		var highest int64
		for i := int64(0); i < 15; i++ {
			num := baseNum + i
			scraper.segmentSizes.Store(fmt.Sprintf("seg%05d.ts", num), int64(1000000))
			if num > highest {
				highest = num
			}
		}

		// Also keep a manifest
		scraper.segmentSizes.Store("stream.m3u8", int64(500))

		scraper.highestSegNum.Store(highest)
		scraper.evictOldEntries(highest)

		// INVARIANT: numbered segments <= windowSize
		var numberedCount int
		scraper.segmentSizes.Range(func(key, _ any) bool {
			if _, ok := parseSegmentNumber(key.(string)); ok {
				numberedCount++
			}
			return true
		})

		if numberedCount > int(windowSize) {
			t.Errorf("INVARIANT VIOLATED at cycle %d: numberedCount=%d > windowSize=%d",
				cycle, numberedCount, windowSize)
		}
	}
}

// Benchmarks

// BenchmarkParseSegmentNumber benchmarks the primary backward scan implementation
func BenchmarkParseSegmentNumber(b *testing.B) {
	names := []string{"seg00017.ts", "seg99999.ts", "stream.m3u8", "segment_123.ts"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parseSegmentNumber(names[i%len(names)])
	}
}

// BenchmarkParseSegmentNumberFixed benchmarks the fixed offset (for comparison only)
func BenchmarkParseSegmentNumberFixed(b *testing.B) {
	names := []string{"seg00017.ts", "seg99999.ts", "stream.m3u8"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parseSegmentNumberFixed(names[i%len(names)])
	}
}

func BenchmarkGetSegmentSize(b *testing.B) {
	scraper := NewSegmentScraper("http://test", 5*time.Second, 500*time.Millisecond, 30, nil)
	for i := 0; i < 30; i++ {
		scraper.segmentSizes.Store(fmt.Sprintf("seg%05d.ts", i), int64(1000000))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scraper.GetSegmentSize(fmt.Sprintf("seg%05d.ts", i%30))
	}
}

func BenchmarkGetSegmentSize_Parallel(b *testing.B) {
	scraper := NewSegmentScraper("http://test", 5*time.Second, 500*time.Millisecond, 30, nil)
	for i := 0; i < 30; i++ {
		scraper.segmentSizes.Store(fmt.Sprintf("seg%05d.ts", i), int64(1000000))
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			scraper.GetSegmentSize(fmt.Sprintf("seg%05d.ts", i%30))
			i++
		}
	})
}

func BenchmarkEvictOldEntries(b *testing.B) {
	for _, size := range []int{30, 100, 1000} {
		b.Run(fmt.Sprintf("window_%d", size), func(b *testing.B) {
			scraper := NewSegmentScraper("http://test", 5*time.Second, 500*time.Millisecond, int64(size), nil)
			for i := 0; i < size*2; i++ {
				scraper.segmentSizes.Store(fmt.Sprintf("seg%05d.ts", i), int64(1000000))
			}
			highest := int64(size * 2)

			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				scraper.evictOldEntries(highest)
			}
		})
	}
}

// ════════════════════════════════════════════════════════════════════════════════
// sync.Map vs RWMutex Comparison Benchmarks
// ════════════════════════════════════════════════════════════════════════════════
//
// These benchmarks compare sync.Map.Load vs RLock+map[key] for our use case:
// - Small map (30-100 entries)
// - Read-heavy workload (1000:1 reads to writes)
// - Concurrent readers (100+ goroutines)
//
// Run: go test -bench=BenchmarkSyncMapVsRWMutex -benchmem ./internal/metrics/...

// RWMutexCache is the alternative implementation for comparison
type RWMutexCache struct {
	mu   sync.RWMutex
	data map[string]int64
}

func NewRWMutexCache() *RWMutexCache {
	return &RWMutexCache{data: make(map[string]int64)}
}

func (c *RWMutexCache) Load(key string) (int64, bool) {
	c.mu.RLock()
	val, ok := c.data[key]
	c.mu.RUnlock()
	return val, ok
}

func (c *RWMutexCache) Store(key string, val int64) {
	c.mu.Lock()
	c.data[key] = val
	c.mu.Unlock()
}

// BenchmarkSyncMapVsRWMutex_Read compares read performance
func BenchmarkSyncMapVsRWMutex_Read(b *testing.B) {
	const mapSize = 30 // Typical cache size

	// Setup: populate both caches
	syncMap := &sync.Map{}
	rwMutex := NewRWMutexCache()
	for i := 0; i < mapSize; i++ {
		key := fmt.Sprintf("seg%05d.ts", i)
		syncMap.Store(key, int64(1000000+i))
		rwMutex.Store(key, int64(1000000+i))
	}

	b.Run("sync.Map", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			key := fmt.Sprintf("seg%05d.ts", i%mapSize)
			syncMap.Load(key)
		}
	})

	b.Run("RWMutex", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			key := fmt.Sprintf("seg%05d.ts", i%mapSize)
			rwMutex.Load(key)
		}
	})
}

// BenchmarkSyncMapVsRWMutex_ParallelRead compares concurrent read performance
// This is the realistic workload: many parsers reading simultaneously
func BenchmarkSyncMapVsRWMutex_ParallelRead(b *testing.B) {
	const mapSize = 30

	syncMap := &sync.Map{}
	rwMutex := NewRWMutexCache()
	for i := 0; i < mapSize; i++ {
		key := fmt.Sprintf("seg%05d.ts", i)
		syncMap.Store(key, int64(1000000+i))
		rwMutex.Store(key, int64(1000000+i))
	}

	b.Run("sync.Map_parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				key := fmt.Sprintf("seg%05d.ts", i%mapSize)
				syncMap.Load(key)
				i++
			}
		})
	})

	b.Run("RWMutex_parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				key := fmt.Sprintf("seg%05d.ts", i%mapSize)
				rwMutex.Load(key)
				i++
			}
		})
	})
}

// BenchmarkSyncMapVsRWMutex_MixedWorkload simulates realistic read/write mix
// 99% reads, 1% writes (scraper updates every 5s, parsers read constantly)
func BenchmarkSyncMapVsRWMutex_MixedWorkload(b *testing.B) {
	const mapSize = 30

	syncMap := &sync.Map{}
	rwMutex := NewRWMutexCache()
	for i := 0; i < mapSize; i++ {
		key := fmt.Sprintf("seg%05d.ts", i)
		syncMap.Store(key, int64(1000000+i))
		rwMutex.Store(key, int64(1000000+i))
	}

	b.Run("sync.Map_mixed", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				key := fmt.Sprintf("seg%05d.ts", i%mapSize)
				if i%100 == 0 { // 1% writes
					syncMap.Store(key, int64(2000000+i))
				} else {
					syncMap.Load(key)
				}
				i++
			}
		})
	})

	b.Run("RWMutex_mixed", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				key := fmt.Sprintf("seg%05d.ts", i%mapSize)
				if i%100 == 0 { // 1% writes
					rwMutex.Store(key, int64(2000000+i))
				} else {
					rwMutex.Load(key)
				}
				i++
			}
		})
	})
}
