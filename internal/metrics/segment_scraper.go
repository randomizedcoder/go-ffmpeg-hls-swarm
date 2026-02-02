// Package metrics provides Prometheus metrics for go-ffmpeg-hls-swarm.
package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Maximum JSON response size to prevent OOM from malformed/massive responses
const maxJSONResponseSize = 2 * 1024 * 1024 // 2MB

// SegmentSizeLookup is the interface for looking up segment sizes.
// Implemented by SegmentScraper.
type SegmentSizeLookup interface {
	GetSegmentSize(name string) (int64, bool)
}

// SegmentScraper fetches segment file sizes from the origin server.
// Uses a rolling window to bound memory usage.
//
// Thread-safety: Uses sync.Map for lock-free reads (optimized for read-heavy workloads).
// - Many parser goroutines read concurrently (every segment request)
// - One scraper goroutine writes infrequently (every 5 seconds)
// - sync.Map is ideal for this "write once, read many" pattern
//
// NOTE: sync.Map is chosen over RWMutex because:
// - Our workload is 1000:1 reads:writes (many parsers reading, 1 scraper writing)
// - sync.Map avoids lock contention on reads entirely
// - For small maps (<100 entries), performance difference is minimal
// - See BenchmarkSyncMapVsRWMutex in tests for comparison
//
// NOTE: Only segments with parseable numbers are cached. Manifests and non-standard
// files are excluded to keep the cache clean and bounded.
type SegmentScraper struct {
	url        string
	interval   time.Duration
	jitter     time.Duration // Random jitter ±jitter prevents thundering herd
	windowSize int64
	client     *http.Client
	logger     *slog.Logger
	rng        *rand.Rand // Local RNG to avoid global lock contention

	// Cache: filename -> size (lock-free reads via sync.Map)
	// Stores ALL files (segments AND manifests) for byte tracking
	segmentSizes sync.Map

	// Rolling window tracking
	highestSegNum atomic.Int64

	// Stats
	lastScrape   atomic.Value // time.Time
	scrapeErrors atomic.Int64
	evictedCount atomic.Int64
}

// SegmentInfo represents a single entry from /files/json/
type SegmentInfo struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Mtime string `json:"mtime"`
	Size  int64  `json:"size"`
}

// NewSegmentScraper creates a new segment size scraper.
// Uses a local rand source to avoid global lock contention from math/rand.
func NewSegmentScraper(url string, interval, jitter time.Duration, windowSize int64, logger *slog.Logger) *SegmentScraper {
	if logger == nil {
		logger = slog.Default()
	}
	if windowSize <= 0 {
		windowSize = 300
	}
	if interval <= 0 {
		interval = 1 * time.Second
	}
	if jitter <= 0 {
		jitter = 500 * time.Millisecond
	}

	s := &SegmentScraper{
		url:        url,
		interval:   interval,
		jitter:     jitter,
		windowSize: windowSize,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		logger: logger,
		// Local RNG avoids global rand lock contention
		// Use UnixNano for seed since scraper is created once at startup
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	s.lastScrape.Store(time.Time{})
	return s
}

// Run starts the background scraping loop with jitter.
// Jitter prevents "thundering herd" when multiple instances scrape the same origin.
//
// Uses Timer.Reset() instead of time.After() to avoid allocation churn.
// time.After creates a new timer each iteration that won't be GC'd until it fires.
// Timer.Reset() reuses the same timer, reducing GC pressure.
func (s *SegmentScraper) Run(ctx context.Context) {
	// Initial scrape (no jitter)
	if err := s.scrape(); err != nil {
		s.logger.Warn("segment_scraper_initial_error", "error", err)
	}

	// Create timer once, reset each iteration to avoid allocation churn
	timer := time.NewTimer(s.jitteredInterval())
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := s.scrape(); err != nil {
				s.scrapeErrors.Add(1)
				s.logger.Debug("segment_scraper_error", "error", err)
			}
			// Reset timer with new jittered interval
			timer.Reset(s.jitteredInterval())
		}
	}
}

// jitteredInterval returns the scrape interval with random jitter applied.
// Uses local rng to avoid global rand lock contention.
// Returns: [interval - jitter, interval + jitter]
func (s *SegmentScraper) jitteredInterval() time.Duration {
	return s.interval + time.Duration(s.rng.Int63n(int64(2*s.jitter))) - s.jitter
}

// GetSegmentSize returns the size for a segment (lock-free read).
func (s *SegmentScraper) GetSegmentSize(name string) (int64, bool) {
	if value, ok := s.segmentSizes.Load(name); ok {
		return value.(int64), true
	}
	return 0, false
}

// scrape fetches segment data from the origin server.
func (s *SegmentScraper) scrape() error {
	segments, err := s.fetchSegments()
	if err != nil {
		return err
	}

	// Update cache - store ALL files (segments AND manifests)
	// Manifests are stored for byte tracking (typically only 1 manifest file)
	highest := s.highestSegNum.Load()
	for _, seg := range segments {
		// Store all files for byte tracking
		s.segmentSizes.Store(seg.Name, seg.Size)

		// Track highest segment number for eviction (manifests don't have numbers)
		if num, ok := parseSegmentNumber(seg.Name); ok {
			if num > highest {
				highest = num
			}
		}
	}

	// Evict old SEGMENT entries (manifests are never evicted - no segment number)
	s.evictOldEntries(highest)

	// Update tracking
	s.highestSegNum.Store(highest)
	s.lastScrape.Store(time.Now())

	return nil
}

// WaitForFirstScrape blocks until the first successful scrape completes or timeout.
// Call this before spawning clients to avoid "cold start" zero throughput.
func (s *SegmentScraper) WaitForFirstScrape(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !s.LastScrape().IsZero() {
			return nil // First scrape completed
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for first scrape")
}

// fetchSegments fetches and parses the JSON from the origin server.
// Uses LimitReader to prevent OOM from malformed/massive responses.
func (s *SegmentScraper) fetchSegments() ([]SegmentInfo, error) {
	resp, err := s.client.Get(s.url)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	// Limit response size to prevent OOM from malformed/massive responses
	limitedReader := io.LimitReader(resp.Body, maxJSONResponseSize)

	var segments []SegmentInfo
	if err := json.NewDecoder(limitedReader).Decode(&segments); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}

	return segments, nil
}

// evictOldEntries removes segment entries outside the rolling window.
// Semantics: "keep last N segments inclusive" = keep [highest-N+1, highest]
//
// Example: windowSize=5, highest=10
//
//	threshold = 10 - 5 + 1 = 6
//	keep: 6, 7, 8, 9, 10 (exactly 5 segments)
//	evict: < 6
//
// Note: Manifests (no segment number) are never evicted.
func (s *SegmentScraper) evictOldEntries(highest int64) {
	// +1 ensures we keep exactly windowSize segments, not windowSize+1
	threshold := highest - s.windowSize + 1
	if threshold <= 0 {
		return // Not enough segments yet to evict
	}

	var evicted int64
	s.segmentSizes.Range(func(key, value any) bool {
		name := key.(string)
		if num, ok := parseSegmentNumber(name); ok {
			if num < threshold {
				s.segmentSizes.Delete(name)
				evicted++
			}
		}
		return true
	})

	if evicted > 0 {
		s.evictedCount.Add(evicted)
	}
}

// GetHighestSegmentNumber returns the highest segment number seen.
func (s *SegmentScraper) GetHighestSegmentNumber() int64 {
	return s.highestSegNum.Load()
}

// CacheSize returns the number of entries in the cache.
func (s *SegmentScraper) CacheSize() int {
	count := 0
	s.segmentSizes.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// EvictedCount returns the total number of entries evicted.
func (s *SegmentScraper) EvictedCount() int64 {
	return s.evictedCount.Load()
}

// LastScrape returns the time of the last successful scrape.
func (s *SegmentScraper) LastScrape() time.Time {
	if t, ok := s.lastScrape.Load().(time.Time); ok {
		return t
	}
	return time.Time{}
}

// ScrapeErrors returns the total number of scrape errors.
func (s *SegmentScraper) ScrapeErrors() int64 {
	return s.scrapeErrors.Load()
}

// parseSegmentNumber extracts the number from segment filenames by scanning backwards.
// Primary implementation - robust across different HLS naming conventions:
// - "seg00017.ts" -> 17
// - "segment_123.ts" -> 123
// - "chunk-42.ts" -> 42
// - "stream_%03d.ts" patterns -> works
// Returns (0, false) for manifests (stream.m3u8) and files without numbers.
func parseSegmentNumber(name string) (int64, bool) {
	n := len(name)
	if n < 4 {
		return 0, false
	}

	// Quick check: last char must be 's' (for .ts)
	if name[n-1] != 's' {
		return 0, false
	}

	// Verify ".ts" suffix
	if name[n-3:] != ".ts" {
		return 0, false
	}

	// Scan backwards from before ".ts" to find digits
	end := n - 3
	start := end
	for start > 0 && name[start-1] >= '0' && name[start-1] <= '9' {
		start--
	}

	if start == end {
		return 0, false // No digits found
	}

	num, err := strconv.ParseInt(name[start:end], 10, 64)
	if err != nil {
		return 0, false
	}
	return num, true
}

// parseSegmentNumberFixed extracts the number using fixed offsets.
// Optimized for exactly "seg%05d.ts" pattern (11 chars, digits at [3:8]).
// Faster than backward scan but fragile if FFmpeg config changes.
// Kept for benchmarking comparison - NOT used in production.
func parseSegmentNumberFixed(name string) (int64, bool) {
	// seg00017.ts = 11 chars exactly
	if len(name) != 11 || name[10] != 's' {
		return 0, false
	}

	num, err := strconv.ParseInt(name[3:8], 10, 64)
	if err != nil {
		return 0, false
	}
	return num, true
}
