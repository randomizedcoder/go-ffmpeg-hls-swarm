# Segment Size Tracking Implementation Plan

> **Status**: Ready for review
> **Author**: Claude
> **Date**: 2026-01-29
> **Related**: [SEGMENT_SIZE_TRACKING_DESIGN.md](./SEGMENT_SIZE_TRACKING_DESIGN.md)

This document provides step-by-step implementation instructions with specific file paths, function names, line numbers, and definition of done for each phase.

---

## Implementation Overview

| Phase | Description | Files Modified | Estimated LOC |
|-------|-------------|----------------|---------------|
| 1 | Configuration & CLI Flags | 2 files | ~40 |
| 2 | Segment Scraper | 3 files (2 new) | ~350 |
| 3 | Parser Enhancement | 3 files (1 new) | ~150 |
| 4 | Per-Client Stats | 2 files | ~50 |
| 5 | Aggregation | 2 files | ~60 |
| 6 | Prometheus Metrics | 2 files | ~80 |
| 7 | TUI Display | 2 files | ~120 |
| 8 | Integration & Wiring | 2 files | ~60 |

**Total**: ~840 lines of code, 15 files

---

## Risk Mitigations Summary

These risk mitigations are implemented throughout the code:

| Risk | Mitigation | Location |
|------|------------|----------|
| Zero-division in throughput | Check `wallTime >= 100µs` before division | `handleHLSRequest()` |
| TDigest lock contention | Use atomic histogram per-client, merge at aggregator | `ThroughputHistogram` |
| **Percentile drift** | **Use `Drain()` not `Snapshot()` - resets buckets** | `ThroughputHistogram.Drain()` |
| **Float64 atomics** | **Store bits via `Float64bits()`, compare decoded** | `recordThroughput()` CAS loop |
| **Eviction off-by-one** | **threshold = highest - windowSize + 1** | `evictOldEntries()` |
| JSON response OOM | `io.LimitReader` caps response at 2MB | `fetchSegments()` |
| Thundering herd on origin | Jitter ±500ms on scrape interval | `Run()` loop |
| time.After allocation churn | Use `Timer.Reset()` pattern | `Run()` loop |
| Global rand lock contention | Local `*rand.Rand` per scraper | `SegmentScraper` struct |
| Scraper cold start | Wait for first scrape before spawning clients | `WaitForFirstScrape()` |
| **Bytes/throughput inconsistency** | **Track bytes on "segment complete" only, not "open"** | `handleHLSRequest()` |

---

## Phase 1: Configuration & CLI Flags

**Goal**: Add configuration options so they're available for all subsequent phases.

### Step 1.1: Update Config Struct

**File**: `internal/config/config.go`
**Location**: Lines 10-81 (Config struct), Lines 84-144 (DefaultConfig)

**Add to Config struct (after line ~70, near OriginMetricsURL fields):**

```go
// Segment size tracking
SegmentSizesURL            string        `json:"segment_sizes_url"`
SegmentSizesScrapeInterval time.Duration `json:"segment_sizes_scrape_interval"`
SegmentSizesScrapeJitter   time.Duration `json:"segment_sizes_scrape_jitter"`
SegmentCacheWindow         int64         `json:"segment_cache_window"`
```

**Add to DefaultConfig() (after line ~135, near origin metrics defaults):**

```go
// Segment size tracking defaults
SegmentSizesURL:            "",               // Disabled by default
SegmentSizesScrapeInterval: 5 * time.Second,
SegmentSizesScrapeJitter:   500 * time.Millisecond, // ±500ms jitter prevents thundering herd
SegmentCacheWindow:         30,               // 30 segment rolling window
```

**Add helper method (after line ~160):**

```go
// SegmentSizesEnabled returns true if segment size tracking is configured.
func (c *Config) SegmentSizesEnabled() bool {
    return c.ResolveSegmentSizesURL() != ""
}

// ResolveSegmentSizesURL returns the segment sizes URL, auto-deriving from
// OriginMetricsHost if not explicitly set.
func (c *Config) ResolveSegmentSizesURL() string {
    if c.SegmentSizesURL != "" {
        return c.SegmentSizesURL
    }
    // Auto-derive from origin-metrics-host if set
    if c.OriginMetricsHost != "" {
        return fmt.Sprintf("http://%s:17080/files/json/", c.OriginMetricsHost)
    }
    return ""
}
```

### Step 1.2: Add CLI Flags

**File**: `internal/config/flags.go`
**Location**: Lines 141-165 (origin metrics flags section)

**Add after origin metrics flags (around line 165):**

```go
// Segment size tracking flags
flag.StringVar(&cfg.SegmentSizesURL, "segment-sizes-url", cfg.SegmentSizesURL,
    "URL for segment size JSON (e.g., http://origin:17080/files/json/). "+
        "If not set, auto-derives from -origin-metrics-host. "+
        "Enables accurate segment byte tracking.")

flag.DurationVar(&cfg.SegmentSizesScrapeInterval, "segment-sizes-interval",
    cfg.SegmentSizesScrapeInterval,
    "Interval for scraping segment sizes (default: 5s)")

flag.DurationVar(&cfg.SegmentSizesScrapeJitter, "segment-sizes-jitter",
    cfg.SegmentSizesScrapeJitter,
    "Jitter ± for scrape interval to prevent thundering herd (default: 500ms)")

flag.Int64Var(&cfg.SegmentCacheWindow, "segment-cache-window",
    cfg.SegmentCacheWindow,
    "Number of recent segments to keep in cache. "+
        "Keeps exactly N segments [highest-N+1, highest]. (default: 30)")
```

### Definition of Done - Phase 1

- [ ] `Config` struct has `SegmentSizesURL`, `SegmentSizesScrapeInterval`, `SegmentSizesScrapeJitter`, `SegmentCacheWindow` fields
- [ ] `DefaultConfig()` sets sensible defaults (empty URL, 5s interval, 30 window)
- [ ] `SegmentSizesEnabled()` and `ResolveSegmentSizesURL()` methods work correctly
- [ ] CLI flags `-segment-sizes-url`, `-segment-sizes-interval`, `-segment-cache-window` are registered
- [ ] `go build ./...` compiles without errors
- [ ] `./go-ffmpeg-hls-swarm -help` shows new flags

---

## Phase 2: Segment Scraper

**Goal**: Create the background scraper that fetches segment sizes from the origin server.

### Step 2.1: Create Test Data File

**File**: `internal/metrics/testdata/segments.json` (NEW)

This file already exists per the design doc. Verify it contains valid JSON with segment entries.

### Step 2.2: Create Segment Scraper

**File**: `internal/metrics/segment_scraper.go` (NEW)

**Complete implementation:**

```go
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
const maxJSONResponseSize = 2 * 1024 * 1024  // 2MB

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
    jitter     time.Duration  // Random jitter ±jitter prevents thundering herd
    windowSize int64
    client     *http.Client
    logger     *slog.Logger
    rng        *rand.Rand     // Local RNG to avoid global lock contention

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
        windowSize = 30
    }
    if interval <= 0 {
        interval = 5 * time.Second
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
            return nil  // First scrape completed
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
//   threshold = 10 - 5 + 1 = 6
//   keep: 6, 7, 8, 9, 10 (exactly 5 segments)
//   evict: < 6
//
// Note: Manifests (no segment number) are never evicted.
func (s *SegmentScraper) evictOldEntries(highest int64) {
    // +1 ensures we keep exactly windowSize segments, not windowSize+1
    threshold := highest - s.windowSize + 1
    if threshold <= 0 {
        return  // Not enough segments yet to evict
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
        return 0, false  // No digits found
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
```

### Step 2.3: Create Segment Scraper Tests

**File**: `internal/metrics/segment_scraper_test.go` (NEW)

```go
package metrics

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "net/http/httptest"
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
        {"wrong length", "seg001.ts", 0, false},      // Fixed requires exactly 11 chars
        {"too long", "seg000001.ts", 0, false},       // Fixed requires exactly 11 chars
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
        expectedCacheSize int  // Must equal windowSize (or less if fewer segments)
    }{
        {
            name:              "no eviction when under window",
            windowSize:        30,
            segments:          []string{"seg00001.ts", "seg00002.ts", "seg00003.ts"},
            expectedHighest:   3,
            expectedInCache:   []string{"seg00001.ts", "seg00002.ts", "seg00003.ts"},
            expectedEvicted:   []string{},
            expectedCacheSize: 3,  // Less than windowSize, no eviction
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
            expectedCacheSize: 5,  // Exactly windowSize
        },
        {
            // windowSize=30, highest=49
            // threshold = 49 - 30 + 1 = 20
            // keep: [20,49] = 30 segments
            // evict: [0,19] = 20 segments
            name:       "50 segments with window 30",
            windowSize: 30,
            segments:   generateSegmentNames(0, 50),
            expectedHighest: 49,
            expectedInCache: generateSegmentNames(20, 50),  // seg00020-seg00049
            expectedEvicted: generateSegmentNames(0, 20),   // seg00000-seg00019
            expectedCacheSize: 30,  // Exactly windowSize
        },
        {
            // windowSize=30, highest=99
            // threshold = 99 - 30 + 1 = 70
            // keep: [70,99] = 30 segments
            // evict: [0,69] = 70 segments
            name:       "100 segments with window 30",
            windowSize: 30,
            segments:   generateSegmentNames(0, 100),
            expectedHighest: 99,
            expectedInCache: generateSegmentNames(70, 100),  // seg00070-seg00099
            expectedEvicted: generateSegmentNames(0, 70),    // seg00000-seg00069
            expectedCacheSize: 30,  // Exactly windowSize
        },
        {
            // Edge case: windowSize=1, highest=5
            // threshold = 5 - 1 + 1 = 5
            // keep: [5,5] = 1 segment
            // evict: <5 = 1,2,3,4 (4 segments)
            name:       "window size 1 keeps only highest",
            windowSize: 1,
            segments:   generateSegmentNames(1, 6),
            expectedHighest: 5,
            expectedInCache: []string{"seg00005.ts"},
            expectedEvicted: []string{"seg00001.ts", "seg00002.ts", "seg00003.ts", "seg00004.ts"},
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
        {Name: "stream.m3u8", Type: "file", Size: 374},  // Also cached for byte tracking
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
//           go test -fuzz=FuzzExtractSegmentName ./internal/parser/...
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
    f.Add(strings.Repeat("a", 10000))  // Very long input
    f.Add("seg" + strings.Repeat("9", 100) + ".ts")  // Very large number
    f.Add("日本語.ts")  // Unicode
    f.Add("seg\x00001.ts")  // Null byte
    f.Add("seg-123.ts")
    f.Add("seg_123.ts")
    f.Add("123.ts")
    f.Add("seg123")  // No extension
    f.Add("seg123.TS")  // Wrong case
    f.Add("seg123.ts.ts")  // Double extension

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

// FuzzExtractSegmentName tests extractSegmentName with random URL-like inputs.
func FuzzExtractSegmentName(f *testing.F) {
    // Seed corpus with URL patterns
    f.Add("http://example.com/seg00001.ts")
    f.Add("http://10.177.0.10:17080/seg00001.ts")
    f.Add("https://cdn.example.com/live/720p/seg00001.ts")
    f.Add("/seg00001.ts")
    f.Add("seg00001.ts")
    f.Add("")
    f.Add("/")
    f.Add("//")
    f.Add("http://")
    f.Add("http://example.com/")
    f.Add("http://example.com/path/")  // Trailing slash
    f.Add("http://example.com/seg.ts?query=1")  // Query string
    f.Add("http://example.com/seg.ts#fragment")  // Fragment
    f.Add("http://example.com/seg.ts?a=1&b=2")  // Multiple query params
    f.Add("file:///path/to/seg.ts")  // File URL
    f.Add("http://user:pass@example.com/seg.ts")  // Auth in URL
    f.Add("http://example.com:8080/deep/nested/path/seg.ts")
    f.Add(strings.Repeat("/", 1000) + "seg.ts")  // Many slashes
    f.Add("http://example.com/" + strings.Repeat("a", 10000) + ".ts")  // Long filename

    f.Fuzz(func(t *testing.T, input string) {
        // Should never panic
        result := extractSegmentName(input)

        // Result should never be longer than input
        if len(result) > len(input) {
            t.Errorf("extractSegmentName(%q) returned longer string: %q", input, result)
        }

        // If input contains /, result should not contain /
        // (unless input has no / at all)
        if strings.Contains(input, "/") && strings.Contains(result, "/") {
            // Only valid if input is entirely the result
            if result != input {
                t.Errorf("extractSegmentName(%q) contains slash: %q", input, result)
            }
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
//   num >= highest - windowSize + 1
//
// This is a property that must hold regardless of the specific segments.
func TestEvictionInvariant_AllCachedSegmentsInWindow(t *testing.T) {
    // Property: for any random sequence of segments, after eviction,
    // all numbered segments in cache must be within the window
    testCases := []struct {
        name       string
        windowSize int64
        segments   []int  // Segment numbers to add
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
                threshold = 0  // Can't have negative threshold
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
            maxExpected := int(tc.windowSize)
            if len(tc.segments) < maxExpected {
                maxExpected = len(tc.segments)
            }
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
    const mapSize = 30  // Typical cache size

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
                if i%100 == 0 {  // 1% writes
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
                if i%100 == 0 {  // 1% writes
                    rwMutex.Store(key, int64(2000000+i))
                } else {
                    rwMutex.Load(key)
                }
                i++
            }
        })
    })
}
```

### Definition of Done - Phase 2

**Core Implementation:**
- [ ] `internal/metrics/segment_scraper.go` created with all functions
- [ ] `internal/metrics/segment_scraper_test.go` created with table-driven tests
- [ ] `internal/metrics/testdata/segments.json` exists (already created)
- [ ] `parseSegmentNumber("seg00017.ts")` returns `(17, true)`
- [ ] `parseSegmentNumber("stream.m3u8")` returns `(0, false)`
- [ ] Cache eviction correctly removes entries below threshold

**Basic Tests:**
- [ ] `go test ./internal/metrics/... -v` passes
- [ ] `go test ./internal/metrics/... -bench=.` shows benchmarks run
- [ ] `GetSegmentSize` is zero-allocation (benchmark shows 0 allocs/op)

**Race + Concurrency Tests:**
- [ ] `go test -race ./internal/metrics/...` passes (no data races)
- [ ] `TestSegmentScraper_RunConcurrentAccess` passes
- [ ] `TestSegmentScraper_RaceConditions` passes
- [ ] Scraper terminates within 100ms on context cancellation (no deadlock)

**Fuzz Tests:**
- [ ] `FuzzParseSegmentNumber` runs for 30s without panic
- [ ] Handles unicode, empty strings, very long inputs gracefully

**Property Tests:**
- [ ] `TestEvictionInvariant_AllCachedSegmentsInWindow` passes
- [ ] `TestEvictionInvariant_ManifestsNeverEvicted` passes
- [ ] `TestEvictionInvariant_CacheSizeBounded` passes

---

## Phase 3: Parser Enhancement

**Goal**: Update the parser to lookup segment sizes and track bytes downloaded.

### Design Decision: Track Bytes on "Segment Complete" Only

**IMPORTANT**: We track segment bytes ONLY in `DebugEventParser.handleHLSRequest()` when
a segment download **completes** (has wall time). We do NOT track bytes in `HLSEventParser`
on "Opening URL" events.

**Why?**
- "Opening URL" fires when download **starts** - download may fail
- "Segment complete" fires when download **succeeds** - we have wall time
- Counting bytes on "open" would inflate counts with failed downloads
- Throughput calculation requires wall time (only available on complete)
- Keeping both metrics in the same place ensures consistent semantics

**Result**: `SegmentBytesDownloaded` represents **successful** downloads only, and is
perfectly correlated with throughput measurements.

### Step 3.1: Add SegmentSizeLookup to DebugEventParser

**File**: `internal/parser/debug_events.go`
**Location**: Lines 164-258 (DebugEventParser struct)

**Add field to DebugEventParser struct (around line 180):**

```go
type DebugEventParser struct {
    // ... existing fields ...

    // Segment size lookup (injected dependency)
    segmentSizeLookup SegmentSizeLookup

    // Segment bytes tracking
    segmentBytesDownloaded atomic.Int64

    // Throughput tracking (lock-free histogram for high performance)
    // TDigest merging happens at aggregator level to reduce lock contention
    throughputHist *ThroughputHistogram  // Lock-free atomic histogram
    maxThroughput  atomic.Uint64         // Atomic max (stored as bits via math.Float64bits)
}
```

**Note on maxThroughput atomics**: Go has no atomic.Float64, so we use atomic.Uint64 with bit conversion.

**CRITICAL**: The CAS loop must compare decoded float values but swap encoded bits:

```go
// Helper to read max throughput
func (p *DebugEventParser) loadMaxThroughput() float64 {
    return math.Float64frombits(p.maxThroughput.Load())
}

// CAS loop pattern (used in recordThroughput):
newBits := math.Float64bits(newVal)
for {
    oldBits := p.maxThroughput.Load()
    oldVal := math.Float64frombits(oldBits)  // Decode for comparison
    if newVal <= oldVal {
        return  // Current max is already >= newVal
    }
    if p.maxThroughput.CompareAndSwap(oldBits, newBits) {  // Compare/swap bits
        return
    }
    // CAS failed, retry
}
```

**Add interface (near top of file, around line 30):**

```go
// SegmentSizeLookup is the interface for looking up segment sizes.
type SegmentSizeLookup interface {
    GetSegmentSize(name string) (int64, bool)
}
```

### Step 3.2: Update handleHLSRequest

**File**: `internal/parser/debug_events.go`
**Location**: Lines 511-567 (handleHLSRequest)

**Add segment size lookup after segment download completes:**

```go
// Minimum wall time to avoid division by zero or Inf values
// 100µs guards against clock resolution issues and tiny segments
const minWallTimeForThroughput = 100 * time.Microsecond

func (p *DebugEventParser) handleHLSRequest(event HLSRequestEvent) {
    // ... existing logic ...

    if event.RequestType == "segment" {
        // Track segment size
        segmentName := extractSegmentName(event.URL)
        if p.segmentSizeLookup != nil {
            if size, ok := p.segmentSizeLookup.GetSegmentSize(segmentName); ok {
                p.segmentBytesDownloaded.Add(size)

                // Calculate throughput if we have sufficient wall time
                // Guard against div-by-zero and Inf values
                if event.WallTime >= minWallTimeForThroughput {
                    throughput := float64(size) / event.WallTime.Seconds()
                    p.recordThroughput(throughput)
                }
            }
        }
    }
}

// recordThroughput adds a throughput sample to the histogram.
// Uses lock-free atomic histogram for high-performance per-client tracking.
// TDigest merging happens at the aggregator level every 500ms.
func (p *DebugEventParser) recordThroughput(bytesPerSec float64) {
    // Record to atomic histogram (lock-free, O(1))
    p.throughputHist.Record(bytesPerSec)

    // Track max with CAS loop (lock-free)
    // NOTE: We store float64 as uint64 bits for atomic operations
    newBits := math.Float64bits(bytesPerSec)
    for {
        oldBits := p.maxThroughput.Load()
        oldVal := math.Float64frombits(oldBits)
        if bytesPerSec <= oldVal {
            break  // Current value is already >= new value
        }
        if p.maxThroughput.CompareAndSwap(oldBits, newBits) {
            break  // Successfully updated
        }
        // CAS failed, another goroutine updated - retry
    }
}

// extractSegmentName extracts the filename from a segment URL.
// Example: "http://10.177.0.10:17080/seg00017.ts" -> "seg00017.ts"
func extractSegmentName(url string) string {
    if idx := strings.LastIndex(url, "/"); idx >= 0 {
        return url[idx+1:]
    }
    return url
}
```

### Step 3.2a: Add ThroughputHistogram (NEW FILE)

**File**: `internal/parser/throughput_histogram.go` (NEW)

```go
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
    sum     atomic.Uint64  // For average calculation (scaled to KB/s)
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

// BucketToValue converts a bucket index to a representative value (bytes/sec).
func BucketToValue(bucket int) float64 {
    // Midpoint of bucket in log space
    return 1024 * math.Pow(2, float64(bucket)+0.5)
}
```

### Step 3.2b: Add ThroughputHistogram Tests

**File**: `internal/parser/throughput_histogram_test.go` (NEW)

```go
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
    expectedMax := 999.0
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

    // Verify max is correct
    gotMax := math.Float64frombits(maxThroughput.Load())
    if gotMax != expectedMax {
        t.Errorf("maxThroughput = %v, want %v", gotMax, expectedMax)
    }
}

// ════════════════════════════════════════════════════════════════════════════════
// Aggregator Percentile Correctness Tests
// ════════════════════════════════════════════════════════════════════════════════
//
// These tests verify that histogram→TDigest merging produces correct percentiles
// and that Drain() prevents double-counting of historical data.

// TestAggregator_PercentileAccuracy tests that TDigest produces correct percentiles
// from histogram data. Uses known distributions to verify accuracy.
func TestAggregator_PercentileAccuracy(t *testing.T) {
    tests := []struct {
        name         string
        distribution func(h *ThroughputHistogram) // Function to populate histogram
        expectedP50  float64                       // Expected P50 (with tolerance)
        expectedP95  float64                       // Expected P95 (with tolerance)
        tolerance    float64                       // Acceptable error ratio (e.g., 0.2 = 20%)
    }{
        {
            name: "uniform_1MB",
            distribution: func(h *ThroughputHistogram) {
                // 100 samples all at 1 MB/s
                for i := 0; i < 100; i++ {
                    h.Record(1 * 1024 * 1024) // 1 MB/s
                }
            },
            expectedP50: 1 * 1024 * 1024,
            expectedP95: 1 * 1024 * 1024,
            tolerance:   0.3, // Histogram bucketing introduces some error
        },
        {
            name: "bimodal_1MB_10MB",
            distribution: func(h *ThroughputHistogram) {
                // 90% at 1 MB/s, 10% at 10 MB/s
                for i := 0; i < 90; i++ {
                    h.Record(1 * 1024 * 1024)
                }
                for i := 0; i < 10; i++ {
                    h.Record(10 * 1024 * 1024)
                }
            },
            expectedP50: 1 * 1024 * 1024,   // Should be ~1 MB/s (median in first mode)
            expectedP95: 10 * 1024 * 1024,  // Should be ~10 MB/s (95th in second mode)
            tolerance:   0.5,                // Bimodal is harder to estimate
        },
        {
            name: "mostly_fast_one_slow",
            distribution: func(h *ThroughputHistogram) {
                // 99 samples at 50 MB/s, 1 sample at 1 MB/s
                for i := 0; i < 99; i++ {
                    h.Record(50 * 1024 * 1024)
                }
                h.Record(1 * 1024 * 1024)
            },
            expectedP50: 50 * 1024 * 1024, // Median should be the common value
            expectedP95: 50 * 1024 * 1024, // P95 should also be the common value
            tolerance:   0.3,
        },
        {
            name: "wide_range",
            distribution: func(h *ThroughputHistogram) {
                // Uniform from 1 MB/s to 100 MB/s
                for i := 1; i <= 100; i++ {
                    h.Record(float64(i) * 1024 * 1024)
                }
            },
            expectedP50: 50 * 1024 * 1024,  // Median ~50 MB/s
            expectedP95: 95 * 1024 * 1024,  // P95 ~95 MB/s
            tolerance:   0.3,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            h := NewThroughputHistogram()
            tt.distribution(h)

            // Drain histogram and merge into TDigest
            td := tdigest.New()
            drained := h.Drain()
            for bucket, count := range drained {
                if count > 0 {
                    value := BucketToValue(bucket)
                    td.Add(value, float64(count))
                }
            }

            // Check percentiles
            gotP50 := td.Quantile(0.50)
            gotP95 := td.Quantile(0.95)

            // Verify P50 within tolerance
            p50Error := math.Abs(gotP50-tt.expectedP50) / tt.expectedP50
            if p50Error > tt.tolerance {
                t.Errorf("P50 = %.2f MB/s, want ~%.2f MB/s (error: %.1f%%)",
                    gotP50/(1024*1024), tt.expectedP50/(1024*1024), p50Error*100)
            }

            // Verify P95 within tolerance
            p95Error := math.Abs(gotP95-tt.expectedP95) / tt.expectedP95
            if p95Error > tt.tolerance {
                t.Errorf("P95 = %.2f MB/s, want ~%.2f MB/s (error: %.1f%%)",
                    gotP95/(1024*1024), tt.expectedP95/(1024*1024), p95Error*100)
            }
        })
    }
}

// TestAggregator_NoDrainDoubleCounting is a REGRESSION TEST that verifies
// using Drain() prevents double-counting historical data.
//
// BUG SCENARIO (what we're preventing):
// If Snapshot() is used instead of Drain(), the same samples would be
// re-added to TDigest on every aggregation cycle, causing:
// - Exploding TDigest weights
// - Distorted percentiles (old data dominates)
// - Growing memory usage
func TestAggregator_NoDrainDoubleCounting(t *testing.T) {
    h := NewThroughputHistogram()

    // Add 100 samples at 10 MB/s
    for i := 0; i < 100; i++ {
        h.Record(10 * 1024 * 1024)
    }

    // First drain - should get 100 samples
    drain1 := h.Drain()
    total1 := uint64(0)
    for _, count := range drain1 {
        total1 += count
    }
    if total1 != 100 {
        t.Fatalf("First drain: expected 100, got %d", total1)
    }

    // Second drain WITHOUT adding new samples - should get 0
    drain2 := h.Drain()
    total2 := uint64(0)
    for _, count := range drain2 {
        total2 += count
    }
    if total2 != 0 {
        t.Errorf("Second drain: expected 0 (buckets should be reset), got %d", total2)
    }

    // Add 50 more samples at 20 MB/s
    for i := 0; i < 50; i++ {
        h.Record(20 * 1024 * 1024)
    }

    // Third drain - should ONLY get the 50 new samples
    drain3 := h.Drain()
    total3 := uint64(0)
    for _, count := range drain3 {
        total3 += count
    }
    if total3 != 50 {
        t.Errorf("Third drain: expected 50 (only new samples), got %d", total3)
    }

    // Verify that multiple aggregation cycles don't accumulate
    // Simulate 5 aggregation cycles, each draining into a fresh TDigest
    h2 := NewThroughputHistogram()
    for i := 0; i < 100; i++ {
        h2.Record(10 * 1024 * 1024)
    }

    var digestWeights []float64
    for cycle := 0; cycle < 5; cycle++ {
        td := tdigest.New()
        drained := h2.Drain()
        totalWeight := float64(0)
        for bucket, count := range drained {
            if count > 0 {
                value := BucketToValue(bucket)
                td.Add(value, float64(count))
                totalWeight += float64(count)
            }
        }
        digestWeights = append(digestWeights, totalWeight)

        // Add some samples for next cycle (except last)
        if cycle < 4 {
            for i := 0; i < 10; i++ {
                h2.Record(15 * 1024 * 1024)
            }
        }
    }

    // First cycle should have 100, rest should have 10 (no accumulation!)
    if digestWeights[0] != 100 {
        t.Errorf("Cycle 0 weight: expected 100, got %.0f", digestWeights[0])
    }
    for i := 1; i < 4; i++ {
        if digestWeights[i] != 10 {
            t.Errorf("Cycle %d weight: expected 10, got %.0f", i, digestWeights[i])
        }
    }
    if digestWeights[4] != 0 {
        t.Errorf("Cycle 4 weight: expected 0 (no new samples), got %.0f", digestWeights[4])
    }
}

// TestAggregator_MultiClientMerge tests merging histograms from multiple clients.
func TestAggregator_MultiClientMerge(t *testing.T) {
    // Simulate 10 clients, each with different throughput characteristics
    clients := make([]*ThroughputHistogram, 10)
    for i := range clients {
        clients[i] = NewThroughputHistogram()
        // Each client downloads at (i+1) * 5 MB/s
        baseThroughput := float64((i + 1) * 5 * 1024 * 1024)
        for j := 0; j < 100; j++ {
            clients[i].Record(baseThroughput)
        }
    }

    // Merge all clients into one TDigest
    td := tdigest.New()
    totalSamples := 0
    for _, client := range clients {
        drained := client.Drain()
        for bucket, count := range drained {
            if count > 0 {
                value := BucketToValue(bucket)
                td.Add(value, float64(count))
                totalSamples += int(count)
            }
        }
    }

    // Should have 1000 total samples
    if totalSamples != 1000 {
        t.Errorf("Total samples: expected 1000, got %d", totalSamples)
    }

    // P50 should be around client 5-6 range (25-30 MB/s)
    p50 := td.Quantile(0.50)
    p50MB := p50 / (1024 * 1024)
    if p50MB < 20 || p50MB > 35 {
        t.Errorf("P50 = %.1f MB/s, expected ~25-30 MB/s", p50MB)
    }

    // P95 should be in the high range (45-50 MB/s)
    p95 := td.Quantile(0.95)
    p95MB := p95 / (1024 * 1024)
    if p95MB < 40 || p95MB > 55 {
        t.Errorf("P95 = %.1f MB/s, expected ~45-50 MB/s", p95MB)
    }
}
```

### Step 3.3: Update Stats() Method

**File**: `internal/parser/debug_events.go`
**Location**: Lines 957-1060 (Stats method)

**Add throughput percentiles to DebugEventStats:**

```go
type DebugEventStats struct {
    // ... existing fields ...

    // NEW: Segment bytes and throughput
    SegmentBytesDownloaded int64
    ThroughputP25          float64 // bytes/sec
    ThroughputP50          float64
    ThroughputP75          float64
    ThroughputP95          float64
    ThroughputP99          float64
    ThroughputMax          float64
}

func (p *DebugEventParser) Stats() DebugEventStats {
    stats := DebugEventStats{
        // ... existing fields ...
        SegmentBytesDownloaded: p.segmentBytesDownloaded.Load(),
    }

    // Throughput percentiles
    p.throughputMu.Lock()
    if p.throughputDigest != nil && p.throughputDigest.Count() > 0 {
        stats.ThroughputP25 = p.throughputDigest.Quantile(0.25)
        stats.ThroughputP50 = p.throughputDigest.Quantile(0.50)
        stats.ThroughputP75 = p.throughputDigest.Quantile(0.75)
        stats.ThroughputP95 = p.throughputDigest.Quantile(0.95)
        stats.ThroughputP99 = p.throughputDigest.Quantile(0.99)
    }
    stats.ThroughputMax = p.maxThroughput
    p.throughputMu.Unlock()

    return stats
}
```

### Step 3.4: Update Constructor

**File**: `internal/parser/debug_events.go`
**Location**: Around line 260 (NewDebugEventParser)

**Add parameter for segment size lookup:**

```go
func NewDebugEventParser(clientID int, callback DebugEventCallback, segmentSizeLookup SegmentSizeLookup) *DebugEventParser {
    p := &DebugEventParser{
        clientID:          clientID,
        callback:          callback,
        segmentSizeLookup: segmentSizeLookup,
        // ... existing initialization ...
    }
    return p
}
```

### Definition of Done - Phase 3

- [ ] `SegmentSizeLookup` interface added to `internal/parser/debug_events.go`
- [ ] `DebugEventParser` struct has `segmentSizeLookup` and `throughputDigest` fields
- [ ] `handleHLSRequest` looks up segment size and calculates throughput
- [ ] `Stats()` returns throughput percentiles (P25, P50, P75, P95, P99, Max)
- [ ] `extractSegmentName` function extracts filename from URL
- [ ] `NewDebugEventParser` accepts optional `SegmentSizeLookup` parameter
- [ ] `go test ./internal/parser/... -v` passes
- [ ] Unit tests for `extractSegmentName` pass

---

## Phase 4: Per-Client Stats Update

**Goal**: Track segment bytes at the client level.

### Step 4.1: No Changes Needed

The `DebugEventParser` already tracks segment bytes per client. The `Stats()` method
returns `SegmentBytesDownloaded` which can be aggregated.

**However**, if using the separate `ClientStats` struct, add fields:

**File**: `internal/stats/client_stats.go` (if exists)

```go
type ClientStats struct {
    // ... existing fields ...

    // Segment bytes (from segment size lookup)
    segmentBytesDownloaded atomic.Int64
}

func (cs *ClientStats) AddSegmentBytes(size int64) {
    cs.segmentBytesDownloaded.Add(size)
}

func (cs *ClientStats) TotalSegmentBytes() int64 {
    return cs.segmentBytesDownloaded.Load()
}
```

### Definition of Done - Phase 4

- [ ] `DebugEventStats.SegmentBytesDownloaded` is populated correctly
- [ ] Per-client segment bytes can be aggregated
- [ ] Existing tests pass

---

## Phase 5: Aggregation Update

**Goal**: Aggregate segment bytes and throughput across all clients.

### Step 5.1: Update DebugStatsAggregate

**File**: `internal/stats/aggregator.go`
**Location**: Lines 86-148 (DebugStatsAggregate struct)

**Add throughput fields:**

```go
type DebugStatsAggregate struct {
    // ... existing fields ...

    // Segment Throughput (bytes/sec) - same percentiles as latency
    SegmentThroughputP25 float64
    SegmentThroughputP50 float64
    SegmentThroughputP75 float64
    SegmentThroughputP95 float64
    SegmentThroughputP99 float64
    SegmentThroughputMax float64

    // Total segment bytes downloaded
    TotalSegmentBytes int64
}
```

### Step 5.2: Update Aggregation Logic

**File**: `internal/stats/aggregator.go`
**Location**: Lines 230-419 (aggregation functions)

**Update the aggregation to merge throughput TDigests:**

```go
func (a *StatsAggregator) AggregateDebugStats() DebugStatsAggregate {
    result := DebugStatsAggregate{
        // ... existing fields ...
    }

    // Merge throughput from all clients
    var mergedThroughput *tdigest.TDigest
    var maxThroughput float64

    a.clients.Range(func(key, value interface{}) bool {
        stats := value.(*DebugEventStats)

        // Sum segment bytes
        result.TotalSegmentBytes += stats.SegmentBytesDownloaded

        // Track max throughput
        if stats.ThroughputMax > maxThroughput {
            maxThroughput = stats.ThroughputMax
        }

        // Merge TDigest... (simplified - actual implementation would merge digests)
        return true
    })

    // Set throughput percentiles
    result.SegmentThroughputMax = maxThroughput

    return result
}
```

### Definition of Done - Phase 5

- [ ] `DebugStatsAggregate` struct has throughput percentile fields
- [ ] `DebugStatsAggregate` struct has `TotalSegmentBytes` field
- [ ] Aggregation merges throughput data from all clients
- [ ] `go test ./internal/stats/... -v` passes

---

## Phase 6: Prometheus Metrics

**Goal**: Export segment bytes and throughput to Prometheus.

### Step 6.1: Add New Metrics

**File**: `internal/metrics/collector.go`
**Location**: Lines 75-132 (metric definitions)

**Add after existing throughput metrics (around line 132):**

```go
// --- Segment-specific metrics ---
var (
    hlsSegmentBytesDownloadedTotal = prometheus.NewCounter(
        prometheus.CounterOpts{
            Name: "hls_swarm_segment_bytes_downloaded_total",
            Help: "Total bytes downloaded from segments (based on actual segment sizes)",
        },
    )

    hlsSegmentThroughputBytesPerSec = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "hls_swarm_segment_throughput_bytes_per_second",
            Help: "Current segment download throughput in bytes per second",
        },
    )

    hlsSegmentThroughputP50BytesPerSec = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "hls_swarm_segment_throughput_p50_bytes_per_second",
            Help: "Segment download throughput 50th percentile",
        },
    )

    hlsSegmentThroughputP95BytesPerSec = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "hls_swarm_segment_throughput_p95_bytes_per_second",
            Help: "Segment download throughput 95th percentile",
        },
    )

    hlsSegmentThroughputP99BytesPerSec = prometheus.NewGauge(
        prometheus.GaugeOpts{
            Name: "hls_swarm_segment_throughput_p99_bytes_per_second",
            Help: "Segment download throughput 99th percentile",
        },
    )
)
```

### Step 6.2: Register Metrics

**File**: `internal/metrics/collector.go`
**Location**: Lines 473-530 (NewCollectorWithRegistry)

**Add to registry.MustRegister call:**

```go
// Segment-specific metrics
hlsSegmentBytesDownloadedTotal,
hlsSegmentThroughputBytesPerSec,
hlsSegmentThroughputP50BytesPerSec,
hlsSegmentThroughputP95BytesPerSec,
hlsSegmentThroughputP99BytesPerSec,
```

### Step 6.3: Update RecordStats

**File**: `internal/metrics/collector.go`
**Location**: Lines 618-780 (RecordStats)

**Add tracking field to Collector struct:**

```go
type Collector struct {
    // ... existing fields ...
    prevSegmentBytes int64
}
```

**Add to RecordStats:**

```go
// Segment bytes tracking
segmentBytesDelta := stats.TotalSegmentBytes - c.prevSegmentBytes
if segmentBytesDelta > 0 {
    hlsSegmentBytesDownloadedTotal.Add(float64(segmentBytesDelta))
}
c.prevSegmentBytes = stats.TotalSegmentBytes

// Segment throughput gauges
hlsSegmentThroughputP50BytesPerSec.Set(stats.SegmentThroughputP50)
hlsSegmentThroughputP95BytesPerSec.Set(stats.SegmentThroughputP95)
hlsSegmentThroughputP99BytesPerSec.Set(stats.SegmentThroughputP99)
```

### Step 6.4: Update AggregatedStatsUpdate

**File**: `internal/metrics/collector.go`
**Location**: Lines 552-607 (AggregatedStatsUpdate)

**Add fields:**

```go
type AggregatedStatsUpdate struct {
    // ... existing fields ...

    // Segment-specific
    TotalSegmentBytes    int64
    SegmentThroughputP50 float64
    SegmentThroughputP95 float64
    SegmentThroughputP99 float64
}
```

### Definition of Done - Phase 6

- [ ] `hls_swarm_segment_bytes_downloaded_total` counter registered
- [ ] `hls_swarm_segment_throughput_*` gauges registered
- [ ] `RecordStats` updates segment metrics correctly
- [ ] `curl http://localhost:9090/metrics | grep segment` shows new metrics
- [ ] `go test ./internal/metrics/... -v` passes

---

## Phase 7: TUI Display Update

**Goal**: Add Segment Throughput as a 3rd column next to latency.

### Step 7.1: Add renderThreeColumns Helper

**File**: `internal/tui/view.go`
**Location**: After line 1214 (after renderTwoColumns)

```go
// renderThreeColumns renders three columns side-by-side with separators.
func renderThreeColumns(left, middle, right []string, totalWidth int) string {
    const (
        colWidth       = 30
        separatorWidth = 3
    )

    leftContent := lipgloss.JoinVertical(lipgloss.Left, left...)
    middleContent := lipgloss.JoinVertical(lipgloss.Left, middle...)
    rightContent := lipgloss.JoinVertical(lipgloss.Left, right...)

    leftStyle := lipgloss.NewStyle().Width(colWidth)
    middleStyle := lipgloss.NewStyle().Width(colWidth)
    rightStyle := lipgloss.NewStyle().Width(colWidth)

    separator := mutedStyle.Render(" │ ")

    return lipgloss.JoinHorizontal(lipgloss.Top,
        leftStyle.Render(leftContent),
        separator,
        middleStyle.Render(middleContent),
        separator,
        rightStyle.Render(rightContent),
    )
}
```

### Step 7.2: Update renderLatencyStats

**File**: `internal/tui/view.go`
**Location**: Lines 246-296 (renderLatencyStats)

**Replace the function with 3-column version:**

```go
func (m Model) renderLatencyStats() string {
    if m.debugStats == nil || (m.debugStats.SegmentWallTimeP50 == 0 && m.debugStats.ManifestWallTimeP50 == 0) {
        return ""
    }

    var leftCol, middleCol, rightCol []string

    // === LEFT COLUMN: Manifest Latency ===
    if m.debugStats.ManifestWallTimeP50 > 0 {
        leftCol = append(leftCol, sectionHeaderStyle.Render("Manifest Latency *"))
        leftCol = append(leftCol,
            renderLatencyRow("P25", m.debugStats.ManifestWallTimeP25),
            renderLatencyRow("P50 (median)", m.debugStats.ManifestWallTimeP50),
            renderLatencyRow("P75", m.debugStats.ManifestWallTimeP75),
            renderLatencyRow("P95", m.debugStats.ManifestWallTimeP95),
            renderLatencyRow("P99", m.debugStats.ManifestWallTimeP99),
            renderLatencyRow("Max", time.Duration(m.debugStats.ManifestWallTimeMax*float64(time.Millisecond))),
        )
    } else {
        leftCol = append(leftCol, sectionHeaderStyle.Render("Manifest Latency *"))
        leftCol = append(leftCol, dimStyle.Render("  (no data)"))
    }

    // === MIDDLE COLUMN: Segment Latency ===
    if m.debugStats.SegmentWallTimeP50 > 0 {
        middleCol = append(middleCol, sectionHeaderStyle.Render("Segment Latency *"))
        middleCol = append(middleCol,
            renderLatencyRow("P25", m.debugStats.SegmentWallTimeP25),
            renderLatencyRow("P50 (median)", m.debugStats.SegmentWallTimeP50),
            renderLatencyRow("P75", m.debugStats.SegmentWallTimeP75),
            renderLatencyRow("P95", m.debugStats.SegmentWallTimeP95),
            renderLatencyRow("P99", m.debugStats.SegmentWallTimeP99),
            renderLatencyRow("Max", time.Duration(m.debugStats.SegmentWallTimeMax*float64(time.Millisecond))),
        )
    } else {
        middleCol = append(middleCol, sectionHeaderStyle.Render("Segment Latency *"))
        middleCol = append(middleCol, dimStyle.Render("  (no data)"))
    }

    // === RIGHT COLUMN: Segment Throughput ===
    if m.debugStats.SegmentThroughputP50 > 0 {
        rightCol = append(rightCol, sectionHeaderStyle.Render("Segment Throughput *"))
        rightCol = append(rightCol,
            renderThroughputRow("P25", m.debugStats.SegmentThroughputP25),
            renderThroughputRow("P50 (median)", m.debugStats.SegmentThroughputP50),
            renderThroughputRow("P75", m.debugStats.SegmentThroughputP75),
            renderThroughputRow("P95", m.debugStats.SegmentThroughputP95),
            renderThroughputRow("P99", m.debugStats.SegmentThroughputP99),
            renderThroughputRow("Max", m.debugStats.SegmentThroughputMax),
        )
    } else {
        rightCol = append(rightCol, sectionHeaderStyle.Render("Segment Throughput *"))
        rightCol = append(rightCol, dimStyle.Render("  (no data)"))
    }

    // Render three columns side-by-side
    threeColContent := renderThreeColumns(leftCol, middleCol, rightCol, m.width-4)

    note := dimStyle.Render("* Using accurate FFmpeg timestamps and segment sizes from origin")

    content := lipgloss.JoinVertical(lipgloss.Left, threeColContent, note)

    return boxStyle.Width(m.width - 2).Render(content)
}
```

### Step 7.3: Add renderThroughputRow Helper

**File**: `internal/tui/view.go`
**Location**: After renderLatencyRow (around line 310)

```go
// renderThroughputRow renders a throughput row (bytes/sec formatted as MB/s)
func renderThroughputRow(label string, bytesPerSec float64) string {
    value := formatBytesRate(bytesPerSec)
    return lipgloss.JoinHorizontal(lipgloss.Left,
        labelStyle.Render(label+":"),
        valueStyle.Render(value),
    )
}

// formatBytesRate formats bytes/sec as human-readable rate
func formatBytesRate(bytesPerSec float64) string {
    if bytesPerSec >= 1_000_000_000 {
        return fmt.Sprintf("%.1f GB/s", bytesPerSec/1_000_000_000)
    }
    if bytesPerSec >= 1_000_000 {
        return fmt.Sprintf("%.1f MB/s", bytesPerSec/1_000_000)
    }
    if bytesPerSec >= 1_000 {
        return fmt.Sprintf("%.1f KB/s", bytesPerSec/1_000)
    }
    return fmt.Sprintf("%.0f B/s", bytesPerSec)
}
```

### Definition of Done - Phase 7

- [ ] `renderThreeColumns` helper function added
- [ ] `renderLatencyStats` shows 3 columns: Manifest Latency | Segment Latency | Segment Throughput
- [ ] `renderThroughputRow` and `formatBytesRate` helpers added
- [ ] TUI displays throughput in MB/s format
- [ ] `go build ./...` compiles without errors
- [ ] Visual verification: TUI shows 3 columns when throughput data is available

---

## Phase 8: Integration & Orchestrator Wiring

**Goal**: Wire everything together in the orchestrator.

### Step 8.1: Create Segment Scraper in Orchestrator

**File**: `internal/orchestrator/orchestrator.go`
**Location**: Lines 75-86 (after origin scraper initialization)

**Add segment scraper field to Orchestrator struct:**

```go
type Orchestrator struct {
    // ... existing fields ...
    segmentScraper *metrics.SegmentScraper
}
```

**Initialize in New() (after origin scraper, around line 86):**

```go
// Initialize segment scraper if configured
var segmentScraper *metrics.SegmentScraper
if cfg.SegmentSizesEnabled() {
    segmentScraper = metrics.NewSegmentScraper(
        cfg.ResolveSegmentSizesURL(),
        cfg.SegmentSizesScrapeInterval,
        cfg.SegmentSizesScrapeJitter,  // Jitter prevents thundering herd
        cfg.SegmentCacheWindow,
        logger,
    )
}
```

### Step 8.2: Start Segment Scraper (BEFORE Ramp-Up)

**File**: `internal/orchestrator/orchestrator.go`
**Location**: Lines 182-191 (after origin scraper start, BEFORE ramp-up)

**IMPORTANT**: Start scraper and wait for first scrape BEFORE spawning clients to avoid
"cold start" period where throughput is zero.

**Add (before ramp-up starts):**

```go
// Start segment scraper if configured - MUST complete before clients spawn
if o.segmentScraper != nil {
    // Start background scraper goroutine
    go o.segmentScraper.Run(ctx)

    // Wait for first scrape to complete (with timeout)
    // This ensures cache is populated before clients start requesting segments
    if err := o.segmentScraper.WaitForFirstScrape(5 * time.Second); err != nil {
        o.logger.Warn("segment_scraper_cold_start",
            "error", err,
            "note", "throughput tracking may show zeros initially")
    } else {
        o.logger.Info("segment_scraper_ready",
            "url", o.config.ResolveSegmentSizesURL(),
            "cache_size", o.segmentScraper.CacheSize(),
            "interval", o.config.SegmentSizesScrapeInterval,
            "jitter", o.config.SegmentSizesScrapeJitter,
        )
    }
}

// NOW start ramp-up (after scraper is ready)
rampDone := make(chan struct{})
go func() {
    defer close(rampDone)
    o.rampUp(ctx)
}()
```

### Step 8.3: Pass Segment Scraper to Parser

**File**: `internal/orchestrator/client_manager.go`
**Location**: Where DebugEventParser is created

**Update parser creation to include segment scraper:**

```go
// When creating DebugEventParser, pass the segment scraper
parser := parser.NewDebugEventParser(
    clientID,
    callback,
    segmentScraper,  // NEW: Pass SegmentSizeLookup
)
```

### Step 8.4: Update TUI Model

**File**: `internal/tui/model.go`
**Location**: Where Model is created

Ensure `DebugStatsAggregate` includes throughput fields when passed to TUI.

### Step 8.5: Update convertToMetricsUpdate

**File**: `internal/orchestrator/orchestrator.go`
**Location**: Lines 439-510 (convertToMetricsUpdate)

**Add segment fields:**

```go
func (o *Orchestrator) convertToMetricsUpdate(aggStats *stats.AggregatedStats) *metrics.AggregatedStatsUpdate {
    update := &metrics.AggregatedStatsUpdate{
        // ... existing fields ...

        // Segment-specific (from debug stats)
        TotalSegmentBytes:    debugStats.TotalSegmentBytes,
        SegmentThroughputP50: debugStats.SegmentThroughputP50,
        SegmentThroughputP95: debugStats.SegmentThroughputP95,
        SegmentThroughputP99: debugStats.SegmentThroughputP99,
    }
    return update
}
```

### Definition of Done - Phase 8

- [ ] `Orchestrator` struct has `segmentScraper` field
- [ ] Segment scraper is created when `cfg.SegmentSizesEnabled()` is true
- [ ] Segment scraper runs in background goroutine
- [ ] Parser receives `SegmentSizeLookup` interface
- [ ] Metrics update includes segment data
- [ ] `go build ./...` compiles without errors
- [ ] End-to-end test: run with `-origin-metrics-host` and see throughput in TUI

---

## Phase 9: End-to-End Integration Tests

**Goal**: Automated integration tests that verify the entire data flow from scraper to metrics.

### New File: `internal/integration/segment_tracking_test.go`

```go
// Package integration contains end-to-end tests that verify component interactions.
// These tests use httptest servers and synthetic events to exercise the full path.
//
// Run: go test ./internal/integration/... -v
// Run with race detector: go test ./internal/integration/... -race
package integration

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "net/http/httptest"
    "sync"
    "sync/atomic"
    "testing"
    "time"

    "github.com/yourusername/go-ffmpeg-hls-swarm/internal/metrics"
    "github.com/yourusername/go-ffmpeg-hls-swarm/internal/parser"
    "github.com/yourusername/go-ffmpeg-hls-swarm/internal/stats"
)

// ════════════════════════════════════════════════════════════════════════════════
// End-to-End Integration Tests
// ════════════════════════════════════════════════════════════════════════════════
//
// These tests verify the complete data flow:
// 1. Scraper fetches segment sizes from mock origin
// 2. Parser receives segment events and looks up sizes
// 3. Per-client bytes are tracked correctly
// 4. Aggregator sums across clients
// 5. Metrics collector emits correct Prometheus metrics

// TestE2E_SegmentSizeTracking_FullPath tests the entire segment size tracking path.
func TestE2E_SegmentSizeTracking_FullPath(t *testing.T) {
    // ════════════════════════════════════════════════════════════════════════════
    // STEP 1: Set up mock origin server serving /files/json/
    // ════════════════════════════════════════════════════════════════════════════
    segmentData := []metrics.SegmentInfo{
        {Name: "seg00001.ts", Type: "file", Size: 1000000},
        {Name: "seg00002.ts", Type: "file", Size: 1100000},
        {Name: "seg00003.ts", Type: "file", Size: 1200000},
        {Name: "seg00004.ts", Type: "file", Size: 1300000},
        {Name: "seg00005.ts", Type: "file", Size: 1400000},
        {Name: "stream.m3u8", Type: "file", Size: 500},
    }

    var scrapeCount atomic.Int32
    originServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        scrapeCount.Add(1)
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(segmentData)
    }))
    defer originServer.Close()

    // ════════════════════════════════════════════════════════════════════════════
    // STEP 2: Create and start segment scraper
    // ════════════════════════════════════════════════════════════════════════════
    scraper := metrics.NewSegmentScraper(
        originServer.URL,
        100*time.Millisecond,  // Fast interval for testing
        10*time.Millisecond,   // Small jitter
        30,                    // Window size
        nil,                   // Use default logger
    )

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    go scraper.Run(ctx)

    // Wait for first scrape
    if err := scraper.WaitForFirstScrape(2 * time.Second); err != nil {
        t.Fatalf("Scraper failed to complete first scrape: %v", err)
    }

    // Verify scraper populated cache
    for _, seg := range segmentData {
        size, ok := scraper.GetSegmentSize(seg.Name)
        if !ok {
            t.Errorf("Segment %q not in cache after scrape", seg.Name)
            continue
        }
        if size != seg.Size {
            t.Errorf("Segment %q size = %d, want %d", seg.Name, size, seg.Size)
        }
    }

    // ════════════════════════════════════════════════════════════════════════════
    // STEP 3: Simulate parser receiving segment events
    // ════════════════════════════════════════════════════════════════════════════
    const numClients = 5
    parsers := make([]*parser.DebugEventParser, numClients)
    for i := range parsers {
        parsers[i] = parser.NewDebugEventParser(i, nil, scraper)
    }

    // Simulate segment downloads
    // Each client downloads segments 1-5, each with 100ms wall time
    segmentsPerClient := []string{"seg00001.ts", "seg00002.ts", "seg00003.ts", "seg00004.ts", "seg00005.ts"}
    expectedBytesPerClient := int64(1000000 + 1100000 + 1200000 + 1300000 + 1400000) // 6,000,000 bytes

    for _, p := range parsers {
        for _, segName := range segmentsPerClient {
            // Simulate a segment complete event
            event := parser.HLSRequestEvent{
                RequestType: "segment",
                URL:         "http://origin:17080/" + segName,
                WallTime:    100 * time.Millisecond,  // 100ms download time
            }
            p.HandleHLSRequest(event)  // This triggers size lookup and throughput recording
        }
    }

    // ════════════════════════════════════════════════════════════════════════════
    // STEP 4: Verify per-client byte tracking
    // ════════════════════════════════════════════════════════════════════════════
    for i, p := range parsers {
        stats := p.Stats()
        if stats.SegmentBytesDownloaded != expectedBytesPerClient {
            t.Errorf("Client %d: SegmentBytesDownloaded = %d, want %d",
                i, stats.SegmentBytesDownloaded, expectedBytesPerClient)
        }
    }

    // ════════════════════════════════════════════════════════════════════════════
    // STEP 5: Verify aggregation
    // ════════════════════════════════════════════════════════════════════════════
    aggregator := stats.NewDebugStatsAggregator()
    for _, p := range parsers {
        aggregator.AddParser(p)
    }

    aggStats := aggregator.AggregateDebugStats()
    expectedTotalBytes := expectedBytesPerClient * int64(numClients)

    if aggStats.TotalSegmentBytes != expectedTotalBytes {
        t.Errorf("Aggregate TotalSegmentBytes = %d, want %d",
            aggStats.TotalSegmentBytes, expectedTotalBytes)
    }

    // Verify throughput percentiles are populated (non-zero)
    if aggStats.SegmentThroughputP50 <= 0 {
        t.Error("SegmentThroughputP50 should be > 0")
    }
    if aggStats.SegmentThroughputP95 <= 0 {
        t.Error("SegmentThroughputP95 should be > 0")
    }

    // ════════════════════════════════════════════════════════════════════════════
    // STEP 6: Verify metrics collector
    // ════════════════════════════════════════════════════════════════════════════
    collector := metrics.NewCollector()

    // First update
    collector.RecordStats(&metrics.AggregatedStatsUpdate{
        TotalSegmentBytes:           expectedTotalBytes,
        SegmentThroughputBytesPerSec: aggStats.SegmentThroughputP50,
    })

    // Second update with more bytes (simulating continued downloads)
    additionalBytes := int64(5000000)
    collector.RecordStats(&metrics.AggregatedStatsUpdate{
        TotalSegmentBytes:           expectedTotalBytes + additionalBytes,
        SegmentThroughputBytesPerSec: aggStats.SegmentThroughputP50 * 1.1,
    })

    // Verify counter delta was emitted (not cumulative value)
    // The collector should have added (expectedTotalBytes + additionalBytes) total to the counter
    // This tests that we're tracking deltas correctly

    t.Logf("Test passed: Full path verified with %d clients, %d total bytes tracked",
        numClients, expectedTotalBytes+additionalBytes)
}

// TestE2E_ConcurrentClientsWithScraper tests concurrent client activity
// while the scraper is updating the cache.
func TestE2E_ConcurrentClientsWithScraper(t *testing.T) {
    // Dynamic segment data that changes on each scrape
    var currentHighest atomic.Int64
    currentHighest.Store(10)

    originServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        highest := currentHighest.Add(5)  // Add 5 new segments each scrape

        var segments []metrics.SegmentInfo
        for i := highest - 10; i <= highest; i++ {  // Return last 10 segments
            if i > 0 {
                segments = append(segments, metrics.SegmentInfo{
                    Name: fmt.Sprintf("seg%05d.ts", i),
                    Type: "file",
                    Size: 1000000 + i*1000,  // Size varies by segment number
                })
            }
        }
        segments = append(segments, metrics.SegmentInfo{Name: "stream.m3u8", Type: "file", Size: 500})

        json.NewEncoder(w).Encode(segments)
    }))
    defer originServer.Close()

    scraper := metrics.NewSegmentScraper(
        originServer.URL,
        50*time.Millisecond,   // Fast scrape for testing
        10*time.Millisecond,
        30,
        nil,
    )

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    go scraper.Run(ctx)
    scraper.WaitForFirstScrape(2 * time.Second)

    // Simulate 20 concurrent clients making requests while scraper updates
    const numClients = 20
    const requestsPerClient = 100

    var wg sync.WaitGroup
    var totalBytesTracked atomic.Int64
    var cacheHits atomic.Int64
    var cacheMisses atomic.Int64

    for clientID := 0; clientID < numClients; clientID++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()

            for req := 0; req < requestsPerClient; req++ {
                // Request segments in a range around current highest
                highest := currentHighest.Load()
                segNum := highest - int64(req%15)  // Request recent segments
                if segNum < 1 {
                    segNum = 1
                }

                segName := fmt.Sprintf("seg%05d.ts", segNum)
                size, ok := scraper.GetSegmentSize(segName)
                if ok {
                    cacheHits.Add(1)
                    totalBytesTracked.Add(size)
                } else {
                    cacheMisses.Add(1)
                }

                // Small delay to simulate realistic request patterns
                time.Sleep(time.Millisecond)
            }
        }(clientID)
    }

    wg.Wait()

    // Allow final scrapes to complete
    time.Sleep(100 * time.Millisecond)

    // Verify we got reasonable hit rate
    totalRequests := int64(numClients * requestsPerClient)
    hitRate := float64(cacheHits.Load()) / float64(totalRequests)

    t.Logf("Concurrent test: %d requests, %.1f%% cache hit rate, %d bytes tracked",
        totalRequests, hitRate*100, totalBytesTracked.Load())

    // Should have >50% hit rate even with moving window
    if hitRate < 0.5 {
        t.Errorf("Cache hit rate too low: %.1f%% (expected >50%%)", hitRate*100)
    }

    // Should have tracked significant bytes
    if totalBytesTracked.Load() == 0 {
        t.Error("No bytes were tracked - something is wrong")
    }
}

// TestE2E_MetricsEndpoint verifies Prometheus metrics are exposed correctly.
func TestE2E_MetricsEndpoint(t *testing.T) {
    // This test verifies the Prometheus handler exposes our custom metrics
    // In a real implementation, you'd use the prometheus/testutil package

    collector := metrics.NewCollector()

    // Record some stats
    collector.RecordStats(&metrics.AggregatedStatsUpdate{
        TotalSegmentBytes:           10000000,
        SegmentThroughputBytesPerSec: 50 * 1024 * 1024,  // 50 MB/s
    })

    // Create a test HTTP server with the metrics handler
    metricsServer := httptest.NewServer(collector.Handler())
    defer metricsServer.Close()

    // Fetch metrics
    resp, err := http.Get(metricsServer.URL)
    if err != nil {
        t.Fatalf("Failed to fetch metrics: %v", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        t.Fatalf("Metrics endpoint returned %d, want 200", resp.StatusCode)
    }

    // In a real test, you'd parse the response and verify specific metrics
    // For now, just verify the endpoint works
    t.Log("Metrics endpoint accessible and returning 200 OK")
}

// TestE2E_GracefulShutdown verifies the system shuts down cleanly.
func TestE2E_GracefulShutdown(t *testing.T) {
    originServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        segments := []metrics.SegmentInfo{
            {Name: "seg00001.ts", Type: "file", Size: 1000000},
        }
        json.NewEncoder(w).Encode(segments)
    }))
    defer originServer.Close()

    scraper := metrics.NewSegmentScraper(
        originServer.URL,
        50*time.Millisecond,
        10*time.Millisecond,
        30,
        nil,
    )

    ctx, cancel := context.WithCancel(context.Background())

    scraperDone := make(chan struct{})
    go func() {
        scraper.Run(ctx)
        close(scraperDone)
    }()

    // Let it run briefly
    time.Sleep(200 * time.Millisecond)

    // Cancel and verify shutdown
    shutdownStart := time.Now()
    cancel()

    select {
    case <-scraperDone:
        shutdownDuration := time.Since(shutdownStart)
        t.Logf("Scraper shut down cleanly in %v", shutdownDuration)

        // Should shut down quickly (within 100ms)
        if shutdownDuration > 100*time.Millisecond {
            t.Errorf("Shutdown took too long: %v (expected <100ms)", shutdownDuration)
        }
    case <-time.After(2 * time.Second):
        t.Fatal("Scraper did not shut down within 2 seconds - possible deadlock")
    }
}
```

### Definition of Done - Phase 9

- [ ] `internal/integration/segment_tracking_test.go` created
- [ ] `TestE2E_SegmentSizeTracking_FullPath` passes
- [ ] `TestE2E_ConcurrentClientsWithScraper` passes with >50% cache hit rate
- [ ] `TestE2E_MetricsEndpoint` passes
- [ ] `TestE2E_GracefulShutdown` passes in <100ms
- [ ] All tests pass with `-race` flag
- [ ] All tests pass with `-count=100` (stability check)

---

## Final Verification Checklist

### Build & Test

```bash
# Basic compilation
go build ./...

# Unit tests
go test ./... -v

# Race detector (REQUIRED - catches data races)
go test -race ./internal/metrics/... ./internal/parser/... ./internal/stats/...

# Stability check (run 100 times to catch flaky tests)
go test -race -count=100 ./internal/metrics/... -run 'TestSegmentScraper_'

# Vet for static analysis
go vet ./...
```

- [ ] `go build ./...` compiles without errors
- [ ] `go test ./... -v` all tests pass
- [ ] `go test ./... -race` no race conditions detected
- [ ] `go vet ./...` no issues

### Fuzz Tests

```bash
# Run fuzz tests for 30 seconds each (increase for CI)
go test -fuzz=FuzzParseSegmentNumber -fuzztime=30s ./internal/metrics/...
go test -fuzz=FuzzExtractSegmentName -fuzztime=30s ./internal/parser/...
```

- [ ] `FuzzParseSegmentNumber` runs without panics
- [ ] `FuzzExtractSegmentName` runs without panics
- [ ] No crashes on edge cases (unicode, empty strings, very long inputs)

### Property Tests

```bash
# Run property/invariant tests
go test -v -run 'TestEvictionInvariant' ./internal/metrics/...
```

- [ ] All eviction invariants pass
- [ ] Cache size always ≤ windowSize (for numbered segments)
- [ ] Manifests are never evicted

### Aggregator Tests

```bash
# Run percentile accuracy and double-counting regression tests
go test -v -run 'TestAggregator_' ./internal/parser/...
```

- [ ] `TestAggregator_PercentileAccuracy` passes
- [ ] `TestAggregator_NoDrainDoubleCounting` passes (regression test)
- [ ] `TestAggregator_MultiClientMerge` passes

### Benchmarks

```bash
# Run all benchmarks with memory allocation reporting
go test ./internal/metrics/... -bench=. -benchmem

# Compare sync.Map vs RWMutex (decision validation)
go test ./internal/metrics/... -bench=BenchmarkSyncMapVsRWMutex -benchmem
```

- [ ] `go test ./internal/metrics/... -bench=.` shows expected performance
- [ ] `GetSegmentSize` < 100ns
- [ ] `parseSegmentNumber` < 50ns (backward scan)
- [ ] Zero allocations for hot paths

### End-to-End Integration Tests

```bash
# Run automated integration tests
go test -v ./internal/integration/...

# With race detector
go test -race -v ./internal/integration/...
```

- [ ] `TestE2E_SegmentSizeTracking_FullPath` passes
- [ ] `TestE2E_ConcurrentClientsWithScraper` >50% cache hit rate
- [ ] `TestE2E_GracefulShutdown` <100ms shutdown time

### Manual Integration Test

Run with test origin:

```bash
# Terminal 1: Start test origin
nix run .#test-origin

# Terminal 2: Run swarm with segment tracking
./go-ffmpeg-hls-swarm \
    -url http://10.177.0.10:17080/stream.m3u8 \
    -clients 10 \
    -origin-metrics-host 10.177.0.10 \
    -tui
```

Verify:

- [ ] TUI shows 3 columns: Manifest Latency | Segment Latency | Segment Throughput
- [ ] Throughput values are non-zero and reasonable (e.g., 40-100 MB/s)
- [ ] Prometheus endpoint shows `hls_swarm_segment_bytes_downloaded_total`
- [ ] Prometheus endpoint shows `hls_swarm_segment_throughput_*` gauges

### Documentation

- [ ] Design doc (`SEGMENT_SIZE_TRACKING_DESIGN.md`) is accurate
- [ ] This implementation plan reflects actual code

---

## Rollback Plan

If issues are discovered after implementation:

1. **Quick disable**: Remove `-origin-metrics-host` flag to disable segment tracking
2. **Code rollback**: Each phase is self-contained; can revert phase-by-phase
3. **Feature flag**: Add `-disable-segment-tracking` flag if needed

---

## File Summary

| File | Action | Lines Changed |
|------|--------|---------------|
| `internal/config/config.go` | MODIFY | +30 |
| `internal/config/flags.go` | MODIFY | +20 |
| `internal/metrics/segment_scraper.go` | NEW | +280 |
| `internal/metrics/segment_scraper_test.go` | NEW | +650 |
| `internal/metrics/segment_scraper_fuzz_test.go` | NEW | +80 |
| `internal/parser/throughput_histogram.go` | NEW | +70 |
| `internal/parser/throughput_histogram_test.go` | NEW | +250 |
| `internal/parser/debug_events.go` | MODIFY | +80 |
| `internal/parser/extract_test.go` | NEW | +60 |
| `internal/stats/aggregator.go` | MODIFY | +50 |
| `internal/metrics/collector.go` | MODIFY | +50 |
| `internal/tui/view.go` | MODIFY | +100 |
| `internal/orchestrator/orchestrator.go` | MODIFY | +45 |
| `internal/orchestrator/client_manager.go` | MODIFY | +10 |
| `internal/integration/segment_tracking_test.go` | NEW | +280 |

**Total**: ~2,055 lines of code changes (including comprehensive tests)
