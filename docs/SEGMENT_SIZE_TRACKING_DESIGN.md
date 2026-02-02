# Segment Size Tracking Design Document

> **Status**: Draft
> **Author**: Claude
> **Date**: 2026-01-29

## Overview

Add capability to track actual bytes downloaded per segment by fetching segment file sizes from the origin server's `/files/json/` endpoint. This enables accurate throughput measurement based on real segment sizes rather than estimates.

## Problem Statement

Currently, `go-ffmpeg-hls-swarm` tracks bytes from FFmpeg's `total_size` progress field, which represents cumulative network I/O. However, this doesn't correlate directly with segment downloads and doesn't tell us how much data each segment request transfers.

The origin server now exposes `/files/json/` with segment metadata:

```json
[
  { "name":"seg00017.ts", "type":"file", "mtime":"...", "size":1281032 },
  { "name":"seg00018.ts", "type":"file", "mtime":"...", "size":1297764 }
]
```

By fetching this data periodically and correlating it with FFmpeg's segment download events, we can track:
- Actual bytes downloaded per segment
- Accurate per-client download totals
- Aggregate throughput across all clients

---

## Architecture

### Data Flow

```
┌─────────────────────────────────────────────────────────────────┐
│ 1. SEGMENT SIZE SCRAPER (new component)                         │
│    internal/metrics/segment_scraper.go                          │
│    - Fetches /files/json/ every 5 seconds                       │
│    - Caches segment name → size mapping                         │
│    - Thread-safe with sync.Map (lock-free reads)                │
└─────────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────────┐
│ 2. PARSER ENHANCEMENT                                           │
│    internal/parser/hls_events.go                                │
│    - On segment request event, lookup size from cache           │
│    - Call callback with segment name + size                     │
└─────────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────────┐
│ 3. PER-CLIENT TRACKING                                          │
│    internal/stats/client_stats.go                               │
│    - New atomic counter: SegmentBytesDownloaded                 │
│    - Increment by segment size on each download                 │
└─────────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────────┐
│ 4. AGGREGATION                                                  │
│    internal/stats/aggregator.go                                 │
│    - Sum SegmentBytesDownloaded across all clients              │
│    - Calculate segment throughput rate                          │
└─────────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────────┐
│ 5. PROMETHEUS METRICS                                           │
│    internal/metrics/collector.go                                │
│    - hls_swarm_segment_bytes_downloaded_total (Counter)         │
│    - hls_swarm_segment_throughput_bytes_per_second (Gauge)      │
└─────────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────────┐
│ 6. TUI DISPLAY                                                  │
│    internal/tui/view.go                                         │
│    - Show "Segment Bytes" in Request Stats section              │
│    - Show "Segment Throughput" rate                             │
└─────────────────────────────────────────────────────────────────┘
```

---

## Phase 1: Segment Size Scraper

### Cache Lifecycle Management

The segment size cache must be bounded to prevent unbounded memory growth. Since HLS is a live stream with segments being continuously created and deleted, we use a **rolling window** based on segment numbers.

**Strategy:**
1. Parse segment number from filename (e.g., `seg00017.ts` → `17`)
2. Track the highest segment number seen (atomic int64)
3. On each scrape, evict entries where `segmentNumber < (highestSeen - windowSize)`
4. Default window size: 30 segments (configurable via `-segment-cache-window`)

**Why segment-number based eviction:**
- Aligns with HLS semantics (segments are sequential)
- Simple and predictable memory bound: `O(windowSize)` entries
- No time-based complexity (segment duration may vary)
- FFmpeg clients only request recent segments anyway

**Memory bound calculation:**
- 30 entries × ~50 bytes per entry (filename + int64) ≈ 1.5 KB
- Even with 1000 entry window: ~50 KB (negligible)

**sync.Map vs map+RWMutex trade-off:**

For small bounded maps (~30-1000 entries) with single writer / many readers,
`sync.Map` is not always faster than `map + sync.RWMutex`. We use `sync.Map` as
the default, but include comprehensive benchmarks to verify:

```go
// Run: go test -bench=BenchmarkSyncMapVsRWMutex -benchmem ./internal/metrics/...
// Compares:
// - BenchmarkSyncMapVsRWMutex_Read: Single-threaded read performance
// - BenchmarkSyncMapVsRWMutex_ParallelRead: Concurrent read performance (realistic)
// - BenchmarkSyncMapVsRWMutex_MixedWorkload: 99% reads, 1% writes (realistic)
```

If benchmarks show RWMutex is faster for your workload, the implementation
can be swapped with minimal code changes (same interface).

**Performance notes:**
- `time.After()` allocates a new timer each call → use `Timer.Reset()` instead
- Global `rand.Int63n()` uses a mutex → use local `*rand.Rand` per scraper

### New File: `internal/metrics/segment_scraper.go`

```go
// SegmentScraper fetches segment file sizes from the origin server.
// Uses a rolling window to bound memory usage.
//
// Thread-safety: Uses sync.Map for lock-free reads (optimized for read-heavy workloads).
// - Many parser goroutines read concurrently (every segment request)
// - One scraper goroutine writes infrequently (every 5 seconds)
// - sync.Map is ideal for this "write once, read many" pattern
type SegmentScraper struct {
    url           string                  // e.g., "http://10.177.0.10:17080/files/json/"
    interval      time.Duration           // Scrape interval (default: 5s)
    jitter        time.Duration           // Random jitter ±jitter (default: 500ms)
    windowSize    int64                   // Rolling window size (default: 30)
    client        *http.Client
    logger        *slog.Logger
    rng           *rand.Rand              // Local RNG to avoid global lock contention

    // Cache: filename → size (lock-free reads via sync.Map)
    // Key: string (filename, e.g., "seg00017.ts" or "stream.m3u8")
    // Value: int64 (size in bytes)
    // NOTE: All files are stored (segments AND manifests) for byte tracking
    segmentSizes  sync.Map

    // Rolling window tracking
    highestSegNum atomic.Int64            // Highest segment number seen

    // Stats
    lastScrape    atomic.Value            // time.Time (atomic for lock-free reads)
    scrapeErrors  atomic.Int64
    evictedCount  atomic.Int64            // Total entries evicted (for debugging)
}

// SegmentInfo represents a single entry from /files/json/
type SegmentInfo struct {
    Name  string `json:"name"`
    Type  string `json:"type"`
    Mtime string `json:"mtime"`
    Size  int64  `json:"size"`
}
```

**Key Functions:**

| Function | Line | Description |
|----------|------|-------------|
| `NewSegmentScraper(url, interval, jitter, windowSize, logger)` | ~20 | Constructor with jitter |
| `Run(ctx context.Context)` | ~50 | Background scraper loop with jitter |
| `WaitForFirstScrape(timeout) error` | ~70 | Block until first scrape completes (call before clients) |
| `GetSegmentSize(name string) (int64, bool)` | ~90 | Lock-free lookup via sync.Map.Load() |
| `scrape() error` | ~110 | Fetch, parse JSON, update cache via sync.Map.Store() |
| `parseSegmentNumber(name string) (int64, bool)` | ~140 | Extract number from "seg00017.ts" → 17 |
| `evictOldEntries()` | ~160 | Range over sync.Map, delete old segment entries |
| `GetHighestSegmentNumber() int64` | ~180 | Atomic read of highest seen |
| `CacheSize() int` | ~190 | Count entries via sync.Map.Range() |
| `EvictedCount() int64` | ~195 | Total entries evicted (for metrics) |

**Cache Operations with sync.Map:**
```go
func (s *SegmentScraper) scrape() error {
    // 1. Fetch JSON from origin
    segments, err := s.fetchSegments()
    if err != nil {
        return err
    }

    // 2. Update cache - store ALL files (segments AND manifests)
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

    // 3. Evict old SEGMENT entries (manifests are never evicted - no segment number)
    s.evictOldEntries(highest)

    // 4. Update highest segment number (single writer, so Store is sufficient)
    s.highestSegNum.Store(highest)

    return nil
}

// Run starts the background scraping loop with jitter.
// Jitter prevents "thundering herd" when multiple instances scrape the same origin.
func (s *SegmentScraper) Run(ctx context.Context) {
    // Initial scrape (no jitter)
    if err := s.scrape(); err != nil {
        s.logger.Warn("segment_scraper_initial_error", "error", err)
    }

    // Reuse timer to avoid allocation churn (time.After allocates each call)
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

// jitteredInterval returns interval ± random jitter.
// Uses local RNG to avoid global rand lock contention.
func (s *SegmentScraper) jitteredInterval() time.Duration {
    // [interval - jitter, interval + jitter]
    return s.interval + time.Duration(s.rng.Int63n(int64(2*s.jitter))) - s.jitter
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

    // Range over sync.Map and delete old entries
    var evicted int64
    s.segmentSizes.Range(func(key, value any) bool {
        name := key.(string)
        if num, ok := parseSegmentNumber(name); ok {
            if num < threshold {
                s.segmentSizes.Delete(name)
                evicted++
            }
        }
        return true  // Continue iteration
    })

    // Single atomic update
    if evicted > 0 {
        s.evictedCount.Add(evicted)
    }
}

// GetSegmentSize returns the size for a segment (lock-free read).
func (s *SegmentScraper) GetSegmentSize(name string) (int64, bool) {
    if value, ok := s.segmentSizes.Load(name); ok {
        return value.(int64), true
    }
    return 0, false
}

// CacheSize returns the number of entries in the cache.
// Note: sync.Map has no len() method, so we must iterate.
func (s *SegmentScraper) CacheSize() int {
    count := 0
    s.segmentSizes.Range(func(_, _ any) bool {
        count++
        return true
    })
    return count
}

// parseSegmentNumber extracts the number from segment filenames by scanning backwards.
// Primary implementation - robust across different HLS naming conventions:
// - "seg00017.ts" → 17
// - "segment_123.ts" → 123
// - "chunk-42.ts" → 42
// - "stream_%03d.ts" patterns → works
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

// Package-level compiled regex (for benchmarking comparison only)
var segmentNumberRe = regexp.MustCompile(`(\d+)\.ts$`)

// parseSegmentNumberRegex extracts the number using regex.
// Slowest but most flexible. Kept for benchmarking comparison - NOT used in production.
func parseSegmentNumberRegex(name string) (int64, bool) {
    matches := segmentNumberRe.FindStringSubmatch(name)
    if len(matches) < 2 {
        return 0, false
    }
    num, err := strconv.ParseInt(matches[1], 10, 64)
    if err != nil {
        return 0, false
    }
    return num, true
}
```

**Integration Point:**
- Created in `internal/orchestrator/orchestrator.go:76` alongside `originScraper`
- Requires new CLI flags:
  - `-segment-sizes-url` (or derived from `-origin-metrics-host`)
  - `-segment-sizes-interval` (default: 5s)
  - `-segment-sizes-jitter` (default: 500ms, prevents thundering herd)
  - `-segment-cache-window` (default: 30, keeps exactly N recent segments)

### Test File: `internal/metrics/segment_scraper_test.go`

Table-driven tests:

```go
func TestSegmentScraper_ParseJSON(t *testing.T) {
    tests := []struct {
        name     string
        json     string
        expected map[string]int64
        wantErr  bool
    }{
        {
            name: "valid segments",
            json: `[{"name":"seg00001.ts","type":"file","size":1281032}]`,
            expected: map[string]int64{"seg00001.ts": 1281032},
        },
        {
            name: "empty array",
            json: `[]`,
            expected: map[string]int64{},
        },
        {
            name: "mixed types",
            json: `[{"name":"seg00001.ts","type":"file","size":1281032},{"name":"stream.m3u8","type":"file","size":374}]`,
            expected: map[string]int64{"seg00001.ts": 1281032, "stream.m3u8": 374},
        },
        {
            name: "invalid json",
            json: `not json`,
            wantErr: true,
        },
    }
    // ... test implementation
}

func TestSegmentScraper_GetSegmentSize(t *testing.T) {
    tests := []struct {
        name         string
        cache        map[string]int64
        lookup       string
        expectedSize int64
        expectedOK   bool
    }{
        // ... test cases
    }
}

func TestParseSegmentNumber(t *testing.T) {
    tests := []struct {
        name       string
        filename   string
        expectedN  int64
        expectedOK bool
    }{
        {"standard format", "seg00017.ts", 17, true},
        {"high number", "seg99999.ts", 99999, true},
        {"no padding", "seg5.ts", 5, true},
        {"different prefix", "segment_123.ts", 123, true},
        {"chunk prefix", "chunk-42.ts", 42, true},
        {"manifest file", "stream.m3u8", 0, false},
        {"no number", "video.ts", 0, false},
        {"empty", "", 0, false},
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

func TestSegmentScraper_CacheEviction(t *testing.T) {
    tests := []struct {
        name             string
        windowSize       int64
        segments         []string  // Segment names to add
        expectedHighest  int64
        expectedInCache  []string  // Segments that should remain
        expectedEvicted  []string  // Segments that should be evicted
        expectedCacheSize int      // Expected cache size after eviction
    }{
        {
            name:       "no eviction when under window",
            windowSize: 30,
            segments:   []string{"seg00001.ts", "seg00002.ts", "seg00003.ts"},
            expectedHighest: 3,
            expectedInCache: []string{"seg00001.ts", "seg00002.ts", "seg00003.ts"},
            expectedEvicted: []string{},
            expectedCacheSize: 3,
        },
        {
            name:       "eviction when exceeding window",
            windowSize: 5,
            segments:   []string{"seg00001.ts", "seg00002.ts", "seg00003.ts", "seg00004.ts",
                                 "seg00005.ts", "seg00006.ts", "seg00007.ts", "seg00008.ts"},
            expectedHighest: 8,
            // threshold = 8 - 5 = 3, so seg00001, seg00002, seg00003 evicted
            expectedInCache: []string{"seg00004.ts", "seg00005.ts", "seg00006.ts",
                                       "seg00007.ts", "seg00008.ts"},
            expectedEvicted: []string{"seg00001.ts", "seg00002.ts", "seg00003.ts"},
            expectedCacheSize: 5,
        },
        {
            name:       "manifests not evicted (no segment number)",
            windowSize: 3,
            segments:   []string{"seg00001.ts", "seg00002.ts", "seg00003.ts",
                                 "seg00004.ts", "seg00005.ts", "stream.m3u8"},
            expectedHighest: 5,
            // threshold = 5 - 3 = 2, so seg00001 evicted, but stream.m3u8 stays
            expectedInCache: []string{"seg00003.ts", "seg00004.ts", "seg00005.ts", "stream.m3u8"},
            expectedEvicted: []string{"seg00001.ts", "seg00002.ts"},
            expectedCacheSize: 4,
        },
        {
            name:       "rolling window simulation",
            windowSize: 3,
            // Simulate: add 1-3, then 4-6, then 7-9
            segments:   []string{"seg00007.ts", "seg00008.ts", "seg00009.ts"},
            expectedHighest: 9,
            // threshold = 9 - 3 = 6, so everything < 6 evicted
            expectedInCache: []string{"seg00007.ts", "seg00008.ts", "seg00009.ts"},
            expectedEvicted: []string{},
            expectedCacheSize: 3,
        },
        {
            name:       "large batch exceeding default window",
            windowSize: 30,
            segments:   generateSegmentNames(0, 50),  // seg00000.ts through seg00049.ts
            expectedHighest: 49,
            // threshold = 49 - 30 = 19, so seg00000 through seg00018 evicted
            expectedInCache: generateSegmentNames(19, 50),  // seg00019.ts through seg00049.ts
            expectedEvicted: generateSegmentNames(0, 19),   // seg00000.ts through seg00018.ts
            expectedCacheSize: 31,  // 50 - 19 = 31
        },
        {
            name:       "100 segments with window 30",
            windowSize: 30,
            segments:   generateSegmentNames(0, 100),
            expectedHighest: 99,
            // threshold = 99 - 30 = 69
            expectedInCache: generateSegmentNames(69, 100),
            expectedEvicted: generateSegmentNames(0, 69),
            expectedCacheSize: 31,
        },
        {
            name:       "simulate continuous stream - multiple scrape cycles",
            windowSize: 10,
            // Simulates: first scrape gets 0-14, then 15-29, then 30-44
            // After third scrape: highest=44, threshold=34
            segments:   generateSegmentNames(30, 45),  // Only latest batch
            expectedHighest: 44,
            expectedInCache: generateSegmentNames(34, 45),
            expectedEvicted: generateSegmentNames(30, 34),
            expectedCacheSize: 11,
        },
    }

// Helper to generate segment names
func generateSegmentNames(start, end int) []string {
    names := make([]string, 0, end-start)
    for i := start; i < end; i++ {
        names = append(names, fmt.Sprintf("seg%05d.ts", i))
    }
    return names
}

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            scraper := NewSegmentScraper("http://test", 5*time.Second, tt.windowSize, nil)

            // Populate cache and find highest segment number
            var highest int64
            for _, seg := range tt.segments {
                scraper.segmentSizes.Store(seg, int64(1000))  // Size doesn't matter for this test
                if num, ok := parseSegmentNumber(seg); ok {
                    if num > highest {
                        highest = num
                    }
                }
            }
            // Update atomic and trigger eviction
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
                    t.Errorf("expected %q to be evicted, but found in cache", seg)
                }
            }

            // Verify evicted count
            expectedEvictedCount := int64(len(tt.expectedEvicted))
            if got := scraper.EvictedCount(); got != expectedEvictedCount {
                t.Errorf("EvictedCount() = %d, want %d", got, expectedEvictedCount)
            }
        })
    }
}
```

### Test Data: `internal/metrics/testdata/segments.json`

```json
[
  { "name":"seg00017.ts", "type":"file", "mtime":"Thu, 29 Jan 2026 22:11:08 GMT", "size":1281032 },
  { "name":"seg00018.ts", "type":"file", "mtime":"Thu, 29 Jan 2026 22:11:14 GMT", "size":1297764 },
  { "name":"seg00019.ts", "type":"file", "mtime":"Thu, 29 Jan 2026 22:11:20 GMT", "size":1361120 },
  { "name":"seg00020.ts", "type":"file", "mtime":"Thu, 29 Jan 2026 22:11:25 GMT", "size":1338372 },
  { "name":"seg00021.ts", "type":"file", "mtime":"Thu, 29 Jan 2026 22:11:31 GMT", "size":1341944 },
  { "name":"seg00022.ts", "type":"file", "mtime":"Thu, 29 Jan 2026 22:11:36 GMT", "size":1321640 },
  { "name":"seg00023.ts", "type":"file", "mtime":"Thu, 29 Jan 2026 22:11:42 GMT", "size":1285920 },
  { "name":"seg00024.ts", "type":"file", "mtime":"Thu, 29 Jan 2026 22:11:47 GMT", "size":1310924 },
  { "name":"seg00025.ts", "type":"file", "mtime":"Thu, 29 Jan 2026 22:11:53 GMT", "size":1375032 },
  { "name":"seg00026.ts", "type":"file", "mtime":"Thu, 29 Jan 2026 22:11:58 GMT", "size":1337432 },
  { "name":"seg00027.ts", "type":"file", "mtime":"Thu, 29 Jan 2026 22:12:04 GMT", "size":1337996 },
  { "name":"seg00028.ts", "type":"file", "mtime":"Thu, 29 Jan 2026 22:12:10 GMT", "size":1322016 },
  { "name":"seg00029.ts", "type":"file", "mtime":"Thu, 29 Jan 2026 22:12:15 GMT", "size":1300772 },
  { "name":"seg00030.ts", "type":"file", "mtime":"Thu, 29 Jan 2026 22:12:21 GMT", "size":1314308 },
  { "name":"seg00031.ts", "type":"file", "mtime":"Thu, 29 Jan 2026 22:12:26 GMT", "size":1376724 },
  { "name":"stream.m3u8", "type":"file", "mtime":"Thu, 29 Jan 2026 22:12:26 GMT", "size":374 }
]
```

### Benchmarks: `internal/metrics/segment_scraper_test.go`

**Required benchmarks** - all hot-path functions must be benchmarked:

```go
func BenchmarkParseSegmentNumber_Fixed(b *testing.B) {
    names := []string{"seg00017.ts", "seg99999.ts", "stream.m3u8"}
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        parseSegmentNumber(names[i%len(names)])  // fixed offset version
    }
}

func BenchmarkParseSegmentNumber_Scan(b *testing.B) {
    names := []string{"seg00017.ts", "seg99999.ts", "stream.m3u8"}
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        parseSegmentNumberScan(names[i%len(names)])  // backward scan version
    }
}

func BenchmarkParseSegmentNumber_Regex(b *testing.B) {
    names := []string{"seg00017.ts", "seg99999.ts", "stream.m3u8"}
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        parseSegmentNumberRegex(names[i%len(names)])
    }
}

func BenchmarkGetSegmentSize(b *testing.B) {
    scraper := NewSegmentScraper("http://test", 5*time.Second, 30, nil)
    // Pre-populate cache
    for i := 0; i < 30; i++ {
        scraper.segmentSizes.Store(fmt.Sprintf("seg%05d.ts", i), int64(1000000))
    }

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        scraper.GetSegmentSize(fmt.Sprintf("seg%05d.ts", i%30))
    }
}

func BenchmarkGetSegmentSize_Parallel(b *testing.B) {
    scraper := NewSegmentScraper("http://test", 5*time.Second, 30, nil)
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
            scraper := NewSegmentScraper("http://test", 5*time.Second, int64(size), nil)
            // Pre-populate with 2x window size entries
            for i := 0; i < size*2; i++ {
                scraper.segmentSizes.Store(fmt.Sprintf("seg%05d.ts", i), int64(1000000))
            }
            highest := int64(size * 2)

            b.ResetTimer()
            b.ReportAllocs()  // Track allocations
            for i := 0; i < b.N; i++ {
                scraper.evictOldEntries(highest)
            }
        })
    }
}

// BenchmarkEvictOldEntries_Allocations specifically tests for zero allocations
// during steady-state eviction (no new segments, just maintaining window)
func BenchmarkEvictOldEntries_SteadyState(b *testing.B) {
    scraper := NewSegmentScraper("http://test", 5*time.Second, 30, nil)
    // Pre-populate with exactly window size (no eviction needed)
    for i := 70; i < 100; i++ {
        scraper.segmentSizes.Store(fmt.Sprintf("seg%05d.ts", i), int64(1000000))
    }
    highest := int64(99)

    b.ResetTimer()
    b.ReportAllocs()
    for i := 0; i < b.N; i++ {
        scraper.evictOldEntries(highest)
    }
    // Target: 0 allocs/op when no eviction occurs
}

// BenchmarkScrapeSimulation simulates realistic scrape cycles
func BenchmarkScrapeSimulation(b *testing.B) {
    for _, scenario := range []struct {
        name       string
        windowSize int64
        batchSize  int  // Segments per scrape
    }{
        {"default_window", 30, 15},
        {"small_window", 10, 15},
        {"large_batch", 30, 50},
    } {
        b.Run(scenario.name, func(b *testing.B) {
            scraper := NewSegmentScraper("http://test", 5*time.Second, scenario.windowSize, nil)

            b.ResetTimer()
            b.ReportAllocs()
            for i := 0; i < b.N; i++ {
                // Simulate a scrape: add batch of new segments
                baseNum := int64(i * scenario.batchSize)
                highest := scraper.highestSegNum.Load()

                for j := 0; j < scenario.batchSize; j++ {
                    num := baseNum + int64(j)
                    name := fmt.Sprintf("seg%05d.ts", num)
                    scraper.segmentSizes.Store(name, int64(1000000+j))
                    if num > highest {
                        highest = num
                    }
                }

                scraper.evictOldEntries(highest)
                scraper.highestSegNum.Store(highest)
            }
        })
    }
}

// BenchmarkParseAndStore benchmarks the hot path: parse + store + evict
func BenchmarkParseAndStore(b *testing.B) {
    scraper := NewSegmentScraper("http://test", 5*time.Second, 30, nil)
    segments := make([]string, 100)
    for i := range segments {
        segments[i] = fmt.Sprintf("seg%05d.ts", i)
    }

    b.ResetTimer()
    b.ReportAllocs()
    for i := 0; i < b.N; i++ {
        name := segments[i%100]
        if num, ok := parseSegmentNumber(name); ok {
            scraper.segmentSizes.Store(name, int64(1000000))
            _ = num  // Use the parsed number
        }
    }
}
```

**Performance targets:**

| Function | Time Target | Alloc Target | Notes |
|----------|-------------|--------------|-------|
| `GetSegmentSize` | < 100ns | 0 allocs | Hot path, called per segment request |
| `parseSegmentNumber` (scan) | < 200ns | 0 allocs | Primary - backward scan, robust |
| `parseSegmentNumberFixed` | < 50ns | 0 allocs | Benchmark only - fragile if config changes |
| `parseSegmentNumberRegex` | < 500ns | 2+ allocs | Benchmark only - slowest |
| `evictOldEntries` (steady) | < 1µs | 0 allocs | No eviction needed |
| `evictOldEntries` (30 entries) | < 10µs | 0 allocs | Runs every 5s±jitter |
| `scrape` cycle | < 100µs | minimal | Full fetch + parse + evict |

**Three parseSegmentNumber implementations for benchmarking:**
| Implementation | Description | Expected Speed | Used In Production |
|----------------|-------------|----------------|-------------------|
| Backward scan | Scan for digits, flexible format | Medium (~100-200ns) | **Yes (primary)** |
| Fixed offset | `name[3:8]` - no loops, just slice + ParseInt | Fastest (~10-20ns) | No (fragile) |
| Regex | `(\d+)\.ts$` compiled regex | Slowest (~300-500ns) | No (allocates) |

**Allocation policy:** All hot-path functions must be zero-allocation. Use `b.ReportAllocs()`
in benchmarks and fail CI if allocations increase.

**Implementation decision:** Use backward scan as primary (robust across HLS naming conventions).
Keep fixed offset and regex for benchmarking only - proves the tradeoff between speed and robustness.
Delete benchmark-only functions after initial performance validation if desired.

---

## Phase 2: Parser Enhancement

### Design Decision: Track Bytes on "Segment Complete" Only

**Why NOT track bytes on "Opening URL" (HLSEventParser)?**

There are two potential places to track segment bytes:
1. `HLSEventParser` - fires on "Opening URL" (segment request started)
2. `DebugEventParser` - fires on "segment complete" with wall time (download finished)

**We choose option 2 exclusively.** Here's why:

| Approach | Pros | Cons |
|----------|------|------|
| Track on "Opening URL" | Simpler event, fires earlier | Counts bytes even if download fails |
| Track on "Segment Complete" | Bytes = successful downloads only | Requires wall time parsing |

**Key insight**: For throughput calculation, we MUST use the "complete" event (needs wall time).
If we counted bytes on "open" but throughput on "complete", we'd have inconsistent semantics:
- Failed downloads would inflate byte counts
- Throughput would be calculated on a different set of segments than bytes

**Recommendation applied**: Count bytes ONLY on "segment complete" in `DebugEventParser`.
This keeps semantics consistent: **bytes represent successful downloads**.

### File: `internal/parser/debug_events.go` (NOT hls_events.go)

All segment byte tracking happens in `DebugEventParser.handleHLSRequest()` when a segment
download completes successfully. This is the same place we calculate throughput.

```go
// extractSegmentName extracts the filename from a segment URL.
// Example: "http://10.177.0.10:17080/seg00017.ts" → "seg00017.ts"
func extractSegmentName(url string) string {
    if idx := strings.LastIndex(url, "/"); idx >= 0 {
        return url[idx+1:]
    }
    return url
}
```

### Test File: `internal/parser/debug_events_test.go`

Add table-driven tests for segment byte tracking on completion:

```go
func TestDebugEventParser_SegmentBytesOnComplete(t *testing.T) {
    tests := []struct {
        name           string
        segmentSizes   map[string]int64
        events         []HLSRequestEvent  // Complete events with wall time
        expectedBytes  int64
    }{
        {
            name: "successful download counts bytes",
            segmentSizes: map[string]int64{
                "seg00001.ts": 1281032,
            },
            events: []HLSRequestEvent{
                {RequestType: "segment", URL: "http://origin/seg00001.ts", WallTime: 100 * time.Millisecond},
            },
            expectedBytes: 1281032,
        },
        {
            name: "multiple segments",
            segmentSizes: map[string]int64{
                "seg00001.ts": 1281032,
                "seg00002.ts": 1297764,
            },
            lines: []string{
                `Opening 'http://origin:17080/seg00001.ts' for reading`,
                `Opening 'http://origin:17080/seg00002.ts' for reading`,
            },
            expectedBytes: 1281032 + 1297764,
            expectedReqs:  2,
        },
        {
            name: "unknown segment (not in cache)",
            segmentSizes: map[string]int64{},
            lines: []string{
                `Opening 'http://origin:17080/seg00001.ts' for reading`,
            },
            expectedBytes: 0,  // Unknown segments don't add bytes
            expectedReqs:  1,  // But request is still counted
        },
        {
            name: "multiple segments",
            segmentSizes: map[string]int64{
                "seg00001.ts": 1281032,
                "seg00002.ts": 1297764,
            },
            events: []HLSRequestEvent{
                {RequestType: "segment", URL: "http://origin/seg00001.ts", WallTime: 100 * time.Millisecond},
                {RequestType: "segment", URL: "http://origin/seg00002.ts", WallTime: 110 * time.Millisecond},
            },
            expectedBytes: 1281032 + 1297764,
        },
        {
            name: "unknown segment not counted",
            segmentSizes: map[string]int64{},  // Empty cache
            events: []HLSRequestEvent{
                {RequestType: "segment", URL: "http://origin/seg00001.ts", WallTime: 100 * time.Millisecond},
            },
            expectedBytes: 0,  // Unknown = not counted (failed to download or evicted)
        },
        {
            name: "manifest requests don't add segment bytes",
            segmentSizes: map[string]int64{
                "stream.m3u8": 374,
            },
            events: []HLSRequestEvent{
                {RequestType: "manifest", URL: "http://origin/stream.m3u8", WallTime: 50 * time.Millisecond},
            },
            expectedBytes: 0,  // Manifests don't count toward segment bytes
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            lookup := &mockSegmentSizeLookup{sizes: tt.segmentSizes}
            parser := NewDebugEventParser(1, nil, lookup)

            for _, event := range tt.events {
                parser.handleHLSRequest(event)
            }

            stats := parser.Stats()
            if stats.SegmentBytesDownloaded != tt.expectedBytes {
                t.Errorf("SegmentBytesDownloaded = %d, want %d", stats.SegmentBytesDownloaded, tt.expectedBytes)
            }
        })
    }
}

func TestExtractSegmentName(t *testing.T) {
    tests := []struct {
        url      string
        expected string
    }{
        {"http://10.177.0.10:17080/seg00017.ts", "seg00017.ts"},
        {"http://origin/path/to/seg00001.ts", "seg00001.ts"},
        {"seg00001.ts", "seg00001.ts"},
        {"/seg00001.ts", "seg00001.ts"},
        {"http://cdn.example.com/live/720p/seg12345.ts", "seg12345.ts"},
        {"http://origin:17080/stream.m3u8", "stream.m3u8"},
    }

    for _, tt := range tests {
        t.Run(tt.url, func(t *testing.T) {
            got := extractSegmentName(tt.url)
            if got != tt.expected {
                t.Errorf("extractSegmentName(%q) = %q, want %q", tt.url, got, tt.expected)
            }
        })
    }
}
```

---

## Phase 3: Per-Client Stats Update

### File: `internal/stats/client_stats.go`

**Current State (Lines 20-45):**
```go
type ClientStats struct {
    ClientID  int
    StartTime time.Time

    ManifestRequests atomic.Int64
    SegmentRequests  atomic.Int64
    // ...

    // Existing bytes tracking (from FFmpeg total_size)
    bytesFromPreviousRuns atomic.Int64
    currentProcessBytes   atomic.Int64
}
```

**Proposed Changes (Line ~45):**
```go
type ClientStats struct {
    // ... existing fields ...

    // NEW: Segment bytes tracking (from segment size lookup)
    segmentBytesDownloaded atomic.Int64  // Cumulative segment bytes
    segmentBytesFromPrevRuns atomic.Int64  // For FFmpeg restart handling
}
```

**New Methods (Line ~200):**
```go
// AddSegmentBytes adds downloaded segment bytes (atomic).
// Called when a segment download is detected and size is known.
func (cs *ClientStats) AddSegmentBytes(size int64) {
    cs.segmentBytesDownloaded.Add(size)
}

// TotalSegmentBytes returns cumulative segment bytes across FFmpeg restarts.
func (cs *ClientStats) TotalSegmentBytes() int64 {
    return cs.segmentBytesFromPrevRuns.Load() + cs.segmentBytesDownloaded.Load()
}

// OnProcessStart (update existing, Line ~185)
func (cs *ClientStats) OnProcessStart() {
    // Existing: accumulate FFmpeg total_size bytes
    prev := cs.currentProcessBytes.Swap(0)
    cs.bytesFromPreviousRuns.Add(prev)

    // NEW: No need to reset segmentBytesDownloaded - it's cumulative
    // Segment bytes are tracked independently of FFmpeg restarts
}
```

### Test File: `internal/stats/client_stats_test.go`

```go
func TestClientStats_SegmentBytesTracking(t *testing.T) {
    tests := []struct {
        name           string
        operations     []func(*ClientStats)
        expectedBytes  int64
    }{
        {
            name: "single segment",
            operations: []func(*ClientStats){
                func(cs *ClientStats) { cs.AddSegmentBytes(1281032) },
            },
            expectedBytes: 1281032,
        },
        {
            name: "multiple segments",
            operations: []func(*ClientStats){
                func(cs *ClientStats) { cs.AddSegmentBytes(1281032) },
                func(cs *ClientStats) { cs.AddSegmentBytes(1297764) },
                func(cs *ClientStats) { cs.AddSegmentBytes(1361120) },
            },
            expectedBytes: 1281032 + 1297764 + 1361120,
        },
        {
            name: "survives FFmpeg restart",
            operations: []func(*ClientStats){
                func(cs *ClientStats) { cs.AddSegmentBytes(1000000) },
                func(cs *ClientStats) { cs.OnProcessStart() },  // FFmpeg restart
                func(cs *ClientStats) { cs.AddSegmentBytes(2000000) },
            },
            expectedBytes: 3000000,  // Both runs counted
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            cs := NewClientStats(1)
            for _, op := range tt.operations {
                op(cs)
            }
            if got := cs.TotalSegmentBytes(); got != tt.expectedBytes {
                t.Errorf("TotalSegmentBytes() = %d, want %d", got, tt.expectedBytes)
            }
        })
    }
}

func TestClientStats_SegmentBytesConcurrency(t *testing.T) {
    cs := NewClientStats(1)
    var wg sync.WaitGroup

    // 100 goroutines each adding 1000 bytes 100 times
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for j := 0; j < 100; j++ {
                cs.AddSegmentBytes(1000)
            }
        }()
    }

    wg.Wait()

    expected := int64(100 * 100 * 1000)  // 10,000,000
    if got := cs.TotalSegmentBytes(); got != expected {
        t.Errorf("TotalSegmentBytes() = %d, want %d (concurrent)", got, expected)
    }
}
```

---

## Phase 4: Aggregation Update

### File: `internal/stats/aggregator.go`

**Current State (Lines 30-60):**
```go
type AggregatedStats struct {
    // ... existing fields ...
    TotalBytes            int64
    ThroughputBytesPerSec float64
}
```

**Proposed Changes (Line ~50):**
```go
type AggregatedStats struct {
    // ... existing fields ...

    // Existing (from FFmpeg total_size)
    TotalBytes            int64
    ThroughputBytesPerSec float64

    // NEW: Segment-specific bytes (from segment size lookup)
    TotalSegmentBytes           int64
    SegmentThroughputBytesPerSec float64
}
```

**Update `Aggregate()` (Lines 230-419):**
```go
func (a *StatsAggregator) Aggregate() *AggregatedStats {
    result := &AggregatedStats{
        Timestamp: time.Now(),
        // ... existing initialization ...
    }

    // Iterate all clients
    a.clients.Range(func(key, value interface{}) bool {
        c := value.(*ClientStats)

        // Existing aggregations
        result.TotalManifestReqs += c.ManifestRequests.Load()
        result.TotalSegmentReqs += c.SegmentRequests.Load()
        result.TotalBytes += c.TotalBytes()

        // NEW: aggregate segment bytes
        result.TotalSegmentBytes += c.TotalSegmentBytes()

        return true
    })

    // Calculate rates
    elapsed := time.Since(a.startTime).Seconds()
    if elapsed > 0 {
        result.ThroughputBytesPerSec = float64(result.TotalBytes) / elapsed

        // NEW: segment throughput rate
        result.SegmentThroughputBytesPerSec = float64(result.TotalSegmentBytes) / elapsed
    }

    // Instantaneous rates from previous snapshot (Lines 350-380)
    // ... existing rate calculations ...

    // NEW: instantaneous segment rate
    if prev != nil && deltaTime > 0 {
        segmentBytesDelta := result.TotalSegmentBytes - prev.TotalSegmentBytes
        result.InstantSegmentThroughputRate = float64(segmentBytesDelta) / deltaTime
    }

    return result
}
```

### Test File: `internal/stats/aggregator_test.go`

```go
func TestAggregator_SegmentBytesAggregation(t *testing.T) {
    tests := []struct {
        name          string
        clientBytes   []int64  // bytes per client
        expectedTotal int64
    }{
        {
            name:          "single client",
            clientBytes:   []int64{1000000},
            expectedTotal: 1000000,
        },
        {
            name:          "multiple clients",
            clientBytes:   []int64{1000000, 2000000, 3000000},
            expectedTotal: 6000000,
        },
        {
            name:          "no clients",
            clientBytes:   []int64{},
            expectedTotal: 0,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            agg := NewStatsAggregator(0.5)

            for i, bytes := range tt.clientBytes {
                cs := NewClientStats(i)
                cs.AddSegmentBytes(bytes)
                agg.AddClient(cs)
            }

            stats := agg.Aggregate()
            if stats.TotalSegmentBytes != tt.expectedTotal {
                t.Errorf("TotalSegmentBytes = %d, want %d",
                    stats.TotalSegmentBytes, tt.expectedTotal)
            }
        })
    }
}
```

---

## Phase 5: Prometheus Metrics

### File: `internal/metrics/collector.go`

**Current State (Lines 50-100):**
```go
var (
    hlsBytesDownloadedTotal = prometheus.NewCounter(...)
    hlsThroughputBytesPerSec = prometheus.NewGauge(...)
)
```

**Proposed Changes (Line ~100):**
```go
var (
    // Existing
    hlsBytesDownloadedTotal = prometheus.NewCounter(...)
    hlsThroughputBytesPerSec = prometheus.NewGauge(...)

    // NEW: Segment-specific metrics
    hlsSegmentBytesDownloadedTotal = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "hls_swarm_segment_bytes_downloaded_total",
        Help: "Total bytes downloaded from segments (based on actual segment sizes)",
    })

    hlsSegmentThroughputBytesPerSec = prometheus.NewGauge(prometheus.GaugeOpts{
        Name: "hls_swarm_segment_throughput_bytes_per_second",
        Help: "Current segment download throughput in bytes per second",
    })
)
```

**Update `init()` (Line ~150):**
```go
func init() {
    // Existing registrations...
    prometheus.MustRegister(hlsBytesDownloadedTotal)
    prometheus.MustRegister(hlsThroughputBytesPerSec)

    // NEW
    prometheus.MustRegister(hlsSegmentBytesDownloadedTotal)
    prometheus.MustRegister(hlsSegmentThroughputBytesPerSec)
}
```

**Update `AggregatedStatsUpdate` (Line ~200):**
```go
type AggregatedStatsUpdate struct {
    // Existing
    TotalBytes            int64
    ThroughputBytesPerSec float64

    // NEW
    TotalSegmentBytes           int64
    SegmentThroughputBytesPerSec float64
}
```

**Update `RecordStats()` (Lines 618-780):**
```go
func (c *Collector) RecordStats(stats *AggregatedStatsUpdate) {
    c.mu.Lock()
    defer c.mu.Unlock()

    // Existing byte tracking
    bytesDelta := stats.TotalBytes - c.prevBytes
    if bytesDelta > 0 {
        hlsBytesDownloadedTotal.Add(float64(bytesDelta))
    }
    c.prevBytes = stats.TotalBytes

    // NEW: segment byte tracking
    segmentBytesDelta := stats.TotalSegmentBytes - c.prevSegmentBytes
    if segmentBytesDelta > 0 {
        hlsSegmentBytesDownloadedTotal.Add(float64(segmentBytesDelta))
    }
    c.prevSegmentBytes = stats.TotalSegmentBytes

    // Update gauges
    hlsThroughputBytesPerSec.Set(stats.ThroughputBytesPerSec)
    hlsSegmentThroughputBytesPerSec.Set(stats.SegmentThroughputBytesPerSec)  // NEW
}
```

**Add tracking field (Line ~250):**
```go
type Collector struct {
    // Existing
    prevBytes    int64

    // NEW
    prevSegmentBytes int64
}
```

### Test File: `internal/metrics/collector_test.go`

```go
func TestCollector_SegmentBytesMetrics(t *testing.T) {
    tests := []struct {
        name                string
        updates             []AggregatedStatsUpdate
        expectedTotalDelta  float64
        expectedThroughput  float64
    }{
        {
            name: "first update",
            updates: []AggregatedStatsUpdate{
                {TotalSegmentBytes: 1000000, SegmentThroughputBytesPerSec: 500000},
            },
            expectedTotalDelta: 1000000,
            expectedThroughput: 500000,
        },
        {
            name: "incremental updates",
            updates: []AggregatedStatsUpdate{
                {TotalSegmentBytes: 1000000, SegmentThroughputBytesPerSec: 500000},
                {TotalSegmentBytes: 2500000, SegmentThroughputBytesPerSec: 750000},
            },
            expectedTotalDelta: 2500000,  // Cumulative delta
            expectedThroughput: 750000,    // Latest throughput
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Reset metrics for test isolation
            // ... test implementation ...
        })
    }
}
```

---

## Phase 7: TUI Display Update

### Current Latency Section Layout (Two Columns)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ Manifest Latency *                         │ Segment Latency *              │
│ ──────────────────                           ─────────────────              │
│ P25:                2 ms                     P25:                2 ms       │
│ P50 (median):       520 ms                   P50 (median):       13 ms      │
│ P75:                1039 ms                  P75:                27 ms      │
│ P95:                1039 ms                  P95:                27 ms      │
│ P99:                1039 ms                  P99:                27 ms      │
│ Max:                1039 ms                  Max:                27 ms      │
│ * Using accurate FFmpeg timestamps                                          │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Proposed: Add Segment Throughput as 3rd Column

Add "Segment Throughput" as a third column to the right of "Segment Latency".
Same percentiles as latency (P25, P50, P75, P95, P99, Max) for easy comparison.
Users can compare with origin metrics displayed in the Origin Server section.

```
┌───────────────────────────────────────────────────────────────────────────────────────────────────┐
│ Manifest Latency *             │ Segment Latency *              │ Segment Throughput *           │
│ ──────────────────               ─────────────────                ──────────────────             │
│ P25:            2 ms             P25:              2 ms           P25:          48.2 MB/s        │
│ P50 (median):  520 ms            P50 (median):    13 ms           P50 (median): 52.3 MB/s        │
│ P75:          1039 ms            P75:             27 ms           P75:          58.1 MB/s        │
│ P95:          1039 ms            P95:             27 ms           P95:          72.4 MB/s        │
│ P99:          1039 ms            P99:             27 ms           P99:          85.6 MB/s        │
│ Max:          1039 ms            Max:             27 ms           Max:          98.7 MB/s        │
│ * Using accurate FFmpeg timestamps and segment sizes from origin                                  │
└───────────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Rate Calculation

**Per-Segment Download Rate:**
```go
// For each segment download, calculate: size / wall_time
// size:      from origin /files/json/ (cached in SegmentScraper)
// wall_time: from FFmpeg timestamps (already tracked in DebugEventParser)

// Minimum wall time to avoid division by zero or Inf values
// 100µs guards against clock resolution issues and tiny segments
const minWallTimeForThroughput = 100 * time.Microsecond

// When segment download completes in DebugEventParser:
func (p *DebugEventParser) onSegmentComplete(segmentName string, wallTime time.Duration) {
    // Guard: Skip if wall time is too small (avoids div-by-zero and Inf)
    if wallTime < minWallTimeForThroughput {
        return
    }

    segmentSize, ok := p.segmentScraper.GetSegmentSize(segmentName)
    if !ok {
        return  // Segment not in cache
    }

    downloadRate := float64(segmentSize) / wallTime.Seconds()  // bytes/sec

    // Record to per-client histogram (lock-free, high performance)
    p.throughputHist.Record(downloadRate)

    // Track max separately using atomic uint64 with float64 bits
    // NOTE: Go has no atomic.Float64, so we store bits and decode for comparison
    newBits := math.Float64bits(downloadRate)
    for {
        oldBits := p.maxThroughput.Load()
        oldVal := math.Float64frombits(oldBits)  // Decode for comparison
        if downloadRate <= oldVal {
            break
        }
        if p.maxThroughput.CompareAndSwap(oldBits, newBits) {  // Compare/swap bits
            break
        }
        // CAS failed, retry
    }
}
```

**Two-Level Throughput Tracking (Performance Optimization):**

To handle 100+ clients downloading segments concurrently without lock contention:

1. **Per-client**: Use atomic bucket-based histogram (lock-free)
2. **Aggregator**: Merge histograms into TDigest every 500ms

```go
// internal/parser/throughput_histogram.go

// ThroughputHistogram is a lock-free histogram for per-client throughput tracking.
// Uses atomic counters for O(1) recording with no locks.
// Buckets cover 1 KB/s to 10 GB/s in logarithmic steps.
//
// IMPORTANT: Use Drain() not Snapshot() for aggregation!
// Drain() resets counters so each aggregation window only contains recent samples.
type ThroughputHistogram struct {
    buckets [64]atomic.Uint64  // 64 buckets covering wide range
    count   atomic.Uint64
    sum     atomic.Uint64      // For average calculation
}

// Record adds a throughput sample (bytes/sec) to the histogram.
// Lock-free, safe for concurrent use.
func (h *ThroughputHistogram) Record(bytesPerSec float64) {
    bucket := h.bucketFor(bytesPerSec)
    h.buckets[bucket].Add(1)
    h.count.Add(1)
    h.sum.Add(uint64(bytesPerSec / 1024))  // Store KB/s to avoid overflow
}

// Drain returns bucket counts AND RESETS them to zero atomically.
// This ensures each aggregation window only contains samples since the last Drain().
//
// CRITICAL: Use this instead of a simple snapshot!
// If you don't reset, you'll re-add all historical counts on every aggregation,
// causing exploding TDigest weights and distorted percentiles.
func (h *ThroughputHistogram) Drain() [64]uint64 {
    var drained [64]uint64
    for i := range h.buckets {
        drained[i] = h.buckets[i].Swap(0)  // Swap returns old value, sets to 0
    }
    h.count.Swap(0)
    h.sum.Swap(0)
    return drained
}
```

**Aggregator-Level TDigest Merge:**
```go
// internal/stats/debug_aggregator.go

// AggregateDebugStats merges per-client histograms into a TDigest.
// Called every 500ms by the stats update loop.
// Uses Drain() to get only samples since last aggregation (recent window).
func (a *DebugStatsAggregator) AggregateDebugStats() DebugStatsAggregate {
    // Create fresh TDigest for this aggregation cycle
    throughputDigest := tdigest.New()
    var maxThroughput float64

    a.clients.Range(func(key, value interface{}) bool {
        client := value.(*ClientDebugStats)

        // DRAIN histogram into TDigest (resets buckets for next window)
        drained := client.ThroughputHist.Drain()
        for bucket, count := range drained {
            if count > 0 {
                // Convert bucket to representative value
                value := bucketToValue(bucket)
                throughputDigest.Add(value, float64(count))
            }
        }

        // Track max (read via bits conversion)
        clientMax := math.Float64frombits(client.MaxThroughput.Load())
        if clientMax > maxThroughput {
            maxThroughput = clientMax
        }

        return true
    })

    return DebugStatsAggregate{
        // ... other fields ...
        SegmentThroughputP25: throughputDigest.Quantile(0.25),
        SegmentThroughputP50: throughputDigest.Quantile(0.50),
        SegmentThroughputP75: throughputDigest.Quantile(0.75),
        SegmentThroughputP95: throughputDigest.Quantile(0.95),
        SegmentThroughputP99: throughputDigest.Quantile(0.99),
        SegmentThroughputMax: maxThroughput,
    }
}
```

### File: `internal/tui/view.go`

**Update `renderLatencyStats()` to use 3-column layout:**

The existing function uses `renderTwoColumns()`. We need a new `renderThreeColumns()` helper
and update `renderLatencyStats()` to include the throughput column.

```go
// renderThreeColumns renders three columns side-by-side with separators.
// Used for latency + throughput display.
func renderThreeColumns(left, middle, right []string, totalWidth int) string {
    const (
        colWidth       = 30  // Each column width
        separatorWidth = 3   // " │ "
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

// renderLatencyStats renders latency and throughput in 3-column layout.
func (m Model) renderLatencyStats() string {
    if m.debugStats == nil {
        return ""
    }

    ds := m.debugStats
    var leftCol, middleCol, rightCol []string

    // === LEFT COLUMN: Manifest Latency ===
    leftCol = append(leftCol, sectionHeaderStyle.Render("Manifest Latency *"))
    leftCol = append(leftCol,
        renderLatencyRow("P25", m.debugStats.ManifestWallTimeP25),
        renderLatencyRow("P50 (median)", m.debugStats.ManifestWallTimeP50),
        renderLatencyRow("P75", m.debugStats.ManifestWallTimeP75),
        renderLatencyRow("P95", m.debugStats.ManifestWallTimeP95),
        renderLatencyRow("P99", m.debugStats.ManifestWallTimeP99),
        renderLatencyRow("Max", time.Duration(m.debugStats.ManifestWallTimeMax*float64(time.Millisecond))),
    )

    // === MIDDLE COLUMN: Segment Latency ===
    middleCol = append(middleCol, sectionHeaderStyle.Render("Segment Latency *"))
    middleCol = append(middleCol,
        renderLatencyRow("P25", m.debugStats.SegmentWallTimeP25),
        renderLatencyRow("P50 (median)", m.debugStats.SegmentWallTimeP50),
        renderLatencyRow("P75", m.debugStats.SegmentWallTimeP75),
        renderLatencyRow("P95", m.debugStats.SegmentWallTimeP95),
        renderLatencyRow("P99", m.debugStats.SegmentWallTimeP99),
        renderLatencyRow("Max", time.Duration(m.debugStats.SegmentWallTimeMax*float64(time.Millisecond))),
    )

    // === RIGHT COLUMN: Segment Throughput ===
    rightCol = append(rightCol, sectionHeaderStyle.Render("Segment Throughput *"))
    rightCol = append(rightCol,
        renderThroughputRow("P25", ds.SegmentThroughputP25),
        renderThroughputRow("P50 (median)", ds.SegmentThroughputP50),
        renderThroughputRow("P75", ds.SegmentThroughputP75),
        renderThroughputRow("P95", ds.SegmentThroughputP95),
        renderThroughputRow("P99", ds.SegmentThroughputP99),
        renderThroughputRow("Max", ds.SegmentThroughputMax),
    )

    // Render three columns
    threeColContent := renderThreeColumns(leftCol, middleCol, rightCol, m.width-4)

    // Note about data sources
    note := dimStyle.Render("* Using accurate FFmpeg timestamps and segment sizes from origin")

    content := lipgloss.JoinVertical(lipgloss.Left, threeColContent, note)

    return boxStyle.Width(m.width - 2).Render(content)
}

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

### New Fields in DebugStatsAggregate

Add throughput percentiles (same as latency percentiles):

```go
// internal/stats/debug_aggregator.go

type DebugStatsAggregate struct {
    // ... existing latency fields ...

    // Segment Latency (existing)
    SegmentWallTimeP25  time.Duration
    SegmentWallTimeP50  time.Duration
    SegmentWallTimeP75  time.Duration
    SegmentWallTimeP95  time.Duration
    SegmentWallTimeP99  time.Duration
    SegmentWallTimeMax  float64

    // NEW: Segment Throughput (bytes/sec) - same percentiles as latency
    // Calculated from: segment_size / wall_time for each segment
    SegmentThroughputP25 float64
    SegmentThroughputP50 float64
    SegmentThroughputP75 float64
    SegmentThroughputP95 float64
    SegmentThroughputP99 float64
    SegmentThroughputMax float64
}
```

### Test File: `internal/tui/view_test.go`

```go
func TestRenderLatencyStats_ThreeColumns(t *testing.T) {
    tests := []struct {
        name         string
        debugStats   *stats.DebugStatsAggregate
        wantContains []string
    }{
        {
            name: "displays all three columns",
            debugStats: &stats.DebugStatsAggregate{
                ManifestWallTimeP50: 500 * time.Millisecond,
                SegmentWallTimeP50:  20 * time.Millisecond,
                SegmentThroughputP50: 52.3 * 1024 * 1024,  // 52.3 MB/s
            },
            wantContains: []string{
                "Manifest Latency",
                "Segment Latency",
                "Segment Throughput",
                "500 ms",
                "20 ms",
                "52.3 MB/s",
            },
        },
        {
            name: "displays all percentiles for throughput",
            debugStats: &stats.DebugStatsAggregate{
                SegmentThroughputP25: 48.2 * 1024 * 1024,
                SegmentThroughputP50: 52.3 * 1024 * 1024,
                SegmentThroughputP75: 58.1 * 1024 * 1024,
                SegmentThroughputP95: 72.4 * 1024 * 1024,
                SegmentThroughputP99: 85.6 * 1024 * 1024,
                SegmentThroughputMax: 98.7 * 1024 * 1024,
            },
            wantContains: []string{
                "P25:",  "48.2 MB/s",
                "P50 (median):", "52.3 MB/s",
                "P75:",  "58.1 MB/s",
                "P95:",  "72.4 MB/s",
                "P99:",  "85.6 MB/s",
                "Max:",  "98.7 MB/s",
            },
        },
        {
            name: "handles zero throughput",
            debugStats: &stats.DebugStatsAggregate{
                SegmentWallTimeP50:   20 * time.Millisecond,
                SegmentThroughputP50: 0,
            },
            wantContains: []string{
                "Segment Throughput",
                "0 B/s",
            },
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            m := Model{debugStats: tt.debugStats, width: 120}
            output := m.renderLatencyStats()

            for _, want := range tt.wantContains {
                if !strings.Contains(output, want) {
                    t.Errorf("output missing %q\nGot: %s", want, output)
                }
            }
        })
    }
}
```

---

## Phase 7: Configuration & CLI Flags

### File: `internal/config/flags.go`

**Add new flag (Line ~160):**
```go
flag.StringVar(&cfg.SegmentSizesURL, "segment-sizes-url", cfg.SegmentSizesURL,
    "URL for segment size JSON (e.g., http://origin:17080/files/json/). "+
        "Enables accurate segment byte tracking.")

flag.DurationVar(&cfg.SegmentSizesScrapeInterval, "segment-sizes-interval",
    cfg.SegmentSizesScrapeInterval,
    "Interval for scraping segment sizes (default: 5s)")
```

### File: `internal/config/config.go`

**Add fields (Line ~80):**
```go
type Config struct {
    // ... existing fields ...

    // NEW: Segment size tracking
    SegmentSizesURL            string        `json:"segment_sizes_url"`
    SegmentSizesScrapeInterval time.Duration `json:"segment_sizes_scrape_interval"`
}

// Defaults (Line ~140)
func DefaultConfig() *Config {
    return &Config{
        // ... existing defaults ...
        SegmentSizesURL:            "",              // Disabled by default
        SegmentSizesScrapeInterval: 5 * time.Second,
    }
}
```

**Auto-derive from origin-metrics-host (Line ~160):**
```go
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

---

## Risk Mitigations

### 1. Zero-Division Guard in Throughput Calculation

**Risk**: If FFmpeg reports a WallTime of zero (clock resolution issues or extremely small segments),
the parser will panic or produce Inf values.

**Mitigation**: Check `wallTime >= 100µs` before performing division.

```go
const minWallTimeForThroughput = 100 * time.Microsecond

if wallTime < minWallTimeForThroughput {
    return  // Skip this sample
}
```

### 2. TDigest Thread Safety & Performance

**Risk**: `tdigest.Add()` is computationally expensive if called thousands of times per second
(100+ clients downloading small segments). Lock contention on `throughputMu` degrades performance.

**Mitigation**: Two-level tracking architecture:
- **Per-client**: Atomic bucket-based histogram (lock-free, O(1) recording)
- **Aggregator**: Merge histograms into TDigest every 500ms (single goroutine)

This eliminates per-sample lock contention while preserving percentile accuracy.

### 2a. Histogram Drain vs Snapshot (Correctness)

**Risk**: If `Snapshot()` only reads bucket counts without resetting, then every aggregation
cycle re-adds ALL historical counts to the TDigest. This causes:
- Exploding TDigest weights
- Distorted percentiles (old data dominates)
- Growing CPU usage over time

**Mitigation**: Use `Drain()` which atomically reads AND resets buckets via `Swap(0)`.
This ensures each TDigest only contains samples from the most recent aggregation window (~500ms).

```go
// WRONG: Snapshot accumulates forever
snapshot := client.ThroughputHist.Snapshot()  // DON'T DO THIS

// CORRECT: Drain resets for next window
drained := client.ThroughputHist.Drain()  // Use this
```

### 2b. Float64 Atomic Operations (Correctness)

**Risk**: Go has no `atomic.Float64`. If you store max throughput as bits but compare as float
directly, the CAS loop will fail silently or behave incorrectly.

**Mitigation**: Store as `atomic.Uint64` using `math.Float64bits()`. CAS loop must:
1. Load bits
2. Decode to float for comparison
3. Swap bits (not float values)

```go
newBits := math.Float64bits(newVal)
for {
    oldBits := maxThroughput.Load()
    oldVal := math.Float64frombits(oldBits)  // Decode for comparison
    if newVal <= oldVal {
        return
    }
    if maxThroughput.CompareAndSwap(oldBits, newBits) {  // Swap bits
        return
    }
}
```

### 2c. Cache Eviction Off-by-One (Correctness)

**Risk**: Using `threshold := highest - windowSize` and evicting `< threshold` keeps
`windowSize+1` entries, not `windowSize`.

**Mitigation**: Use `threshold := highest - windowSize + 1` to keep exactly `windowSize` segments.

Semantics: "keep last N segments inclusive" = keep `[highest-N+1, highest]`

```go
// Example: windowSize=5, highest=10
// threshold = 10 - 5 + 1 = 6
// keep: 6, 7, 8, 9, 10 (exactly 5 segments)
// evict: < 6

func (s *SegmentScraper) evictOldEntries(highest int64) {
    threshold := highest - s.windowSize + 1  // +1 is critical!
    if threshold <= 0 {
        return
    }
    // ... evict entries where num < threshold
}
```

### 2d. Double-Drain Race Condition (Discovered Post-Implementation)

**Risk**: Multiple consumers call `GetDebugStats()` which drains throughput histograms.
The TUI ticks every 500ms and statsUpdateLoop (for Prometheus) ticks every 1s.
Whichever runs first drains the histograms, leaving empty data for the other consumer.
Symptoms: `SegmentThroughputP50 == 0` in TUI despite segments completing.

**Mitigation**: Cache the aggregated `DebugStatsAggregate` result for 1 second.
Both consumers see the same cached data. The cache TTL matches the Prometheus update interval.

```go
// cachedDebugStatsEntry holds cached debug stats to avoid double-drain.
type cachedDebugStatsEntry struct {
    stats     stats.DebugStatsAggregate
    timestamp time.Time
}

func (m *ClientManager) GetDebugStats() stats.DebugStatsAggregate {
    // Check cache first (lock-free read)
    if cached := m.cachedDebugStats.Load(); cached != nil {
        entry := cached.(*cachedDebugStatsEntry)
        if time.Since(entry.timestamp) < m.debugStatsCacheTTL {
            return entry.stats
        }
    }
    // Cache miss or stale - compute fresh stats
    return m.computeDebugStats()
}
```

**Design Lesson**: When using drain semantics (destructive reads), ensure exactly one
consumer drains at any given interval. If multiple consumers need the same data,
use caching or publish/subscribe pattern rather than direct drain calls.

---

### 3. JSON Response Size Limit

**Risk**: If `/files/json/` returns a massive response (e.g., DVR window with 1000+ segments,
or malformed data), the scraper could consume excessive CPU/memory.

**Mitigation**: Use `io.LimitReader` to cap response size at 2MB.

```go
const maxJSONResponseSize = 2 * 1024 * 1024  // 2MB

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
```

### 4. Scraper Lifecycle - Avoid Cold Start

**Risk**: If clients start requesting segments before the first scrape completes, throughput
will be reported as zero during the "cold start" period (cache is empty).

**Mitigation**: Start scraper and wait for first successful scrape BEFORE spawning any clients.

```go
// In orchestrator.go Run() - BEFORE ramp-up
if o.segmentScraper != nil {
    // Start scraper goroutine
    go o.segmentScraper.Run(ctx)

    // Wait for first successful scrape (with timeout)
    if err := o.segmentScraper.WaitForFirstScrape(5 * time.Second); err != nil {
        o.logger.Warn("segment_scraper_cold_start",
            "error", err,
            "note", "throughput tracking may show zeros initially")
    } else {
        o.logger.Info("segment_scraper_ready",
            "cache_size", o.segmentScraper.CacheSize())
    }
}

// NOW start ramp-up
o.rampUp(ctx)
```

**SegmentScraper addition:**
```go
// WaitForFirstScrape blocks until the first successful scrape completes or timeout.
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
```

---

## Implementation Order

1. **Phase 1: Configuration & CLI Flags**
   - Update `internal/config/config.go` - add config fields
   - Update `internal/config/flags.go` - add CLI flags
   - Config options available for all subsequent phases

2. **Phase 2: Segment Scraper**
   - Create `internal/metrics/segment_scraper.go`
   - Create `internal/metrics/segment_scraper_test.go`
   - Create `internal/metrics/testdata/segments.json`
   - Run benchmarks to validate performance targets

3. **Phase 3: Parser Enhancement**
   - Update `internal/parser/hls_events.go`
   - Update `internal/parser/hls_events_test.go`

4. **Phase 4: Per-Client Stats**
   - Update `internal/stats/client_stats.go`
   - Update `internal/stats/client_stats_test.go`

5. **Phase 5: Aggregation**
   - Update `internal/stats/aggregator.go`
   - Update `internal/stats/aggregator_test.go`

6. **Phase 6: Prometheus Metrics**
   - Update `internal/metrics/collector.go`
   - Update `internal/metrics/collector_test.go`

7. **Phase 7: TUI Display**
   - Update `internal/tui/view.go`
   - Update `internal/tui/view_test.go`

8. **Phase 8: Integration & Orchestrator Wiring**
   - Wire up in `internal/orchestrator/orchestrator.go`
   - End-to-end testing

---

## Summary of Changes

| File | Changes |
|------|---------|
| `internal/metrics/segment_scraper.go` | **NEW** - Fetches segment sizes from origin |
| `internal/metrics/segment_scraper_test.go` | **NEW** - Table-driven tests |
| `internal/metrics/testdata/segments.json` | **NEW** - Test fixture |
| `internal/parser/hls_events.go` | Add `SegmentSizeLookup` interface, `segmentBytes` counter |
| `internal/parser/hls_events_test.go` | Add segment bytes tests |
| `internal/stats/client_stats.go` | Add `segmentBytesDownloaded` atomic counter |
| `internal/stats/client_stats_test.go` | Add segment bytes tests |
| `internal/stats/aggregator.go` | Add `TotalSegmentBytes`, `SegmentThroughputBytesPerSec` |
| `internal/stats/aggregator_test.go` | Add aggregation tests |
| `internal/metrics/collector.go` | Add `hls_swarm_segment_bytes_*` metrics |
| `internal/metrics/collector_test.go` | Add metrics tests |
| `internal/tui/view.go` | Add "Segment Bytes" row |
| `internal/tui/view_test.go` | Add display tests |
| `internal/config/flags.go` | Add `-segment-sizes-url` flag |
| `internal/config/config.go` | Add `SegmentSizesURL` config field |
| `internal/orchestrator/orchestrator.go` | Wire up scraper, inject into parsers |

---

## Resolved Design Decisions

1. **Cache lifecycle**: ✅ RESOLVED
   - Use rolling window based on segment number
   - Track `highestSegNum` (atomic int64)
   - Evict entries where `segmentNumber < (highestSeen - windowSize)`
   - Default window: 30 segments (configurable via `-segment-cache-window`)
   - Memory bound: O(windowSize) entries ≈ 1.5 KB for 30 entries

---

## Resolved Design Decisions

2. **Parsing strategy**: ✅ RESOLVED
   - Use backward scan as primary `parseSegmentNumber()` - robust across naming conventions
   - Fixed offset and regex kept for benchmarking comparison only
   - Handles: `seg00017.ts`, `segment_123.ts`, `chunk-42.ts`, `stream_%03d.ts`

3. **Manifest files**: ✅ RESOLVED
   - **Store ALL files** including manifests (stream.m3u8) in sync.Map
   - Enables tracking manifest download bytes
   - Manifests are never evicted (no segment number to compare)
   - Typically only 1 manifest file, so negligible memory impact

4. **Scrape interval jitter**: ✅ RESOLVED
   - Add ±500ms jitter (configurable) to scrape interval
   - Prevents "thundering herd" when multiple swarm instances scrape same origin
   - Default: 5s ± 500ms → effective interval 4.5s to 5.5s

---

## Open Questions

1. **Fallback**: What to do when segment size is unknown?
   - Recommendation: Don't add to bytes counter, but still count the request

2. **Manifest bytes**: Should we also track manifest download bytes?
   - Recommendation: Track separately as `TotalManifestBytes` in future enhancement

3. **Per-client segment bytes in Prometheus**: Add per-client gauge?
   - Recommendation: Only if `--prom-client-metrics` is enabled (Tier 2)
