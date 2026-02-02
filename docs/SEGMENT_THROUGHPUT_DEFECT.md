# Segment Throughput Defect Report

> **Status**: Under Investigation
> **Created**: 2026-01-30
> **Related**: [SEGMENT_SIZE_TRACKING_DESIGN.md](./SEGMENT_SIZE_TRACKING_DESIGN.md) | [SEGMENT_SIZE_TRACKING_IMPLEMENTATION_PLAN.md](./SEGMENT_SIZE_TRACKING_IMPLEMENTATION_PLAN.md) | [SEGMENT_SIZE_TRACKING_IMPLEMENTATION_LOG.md](./SEGMENT_SIZE_TRACKING_IMPLEMENTATION_LOG.md)

---

## Symptom

After ramp-up completes (all 250 clients running), the "Segment Throughput" column in the TUI shows "(no data)" and "Segments Downloaded" shows "(stalled)" at 500, even though:

1. HTTP requests continue to increment at +249/s
2. Nginx on the origin shows 250 active connections and 250.5 req/sec
3. No HTTP errors are reported (Error Rate: 0.00%)

**Screenshot Analysis:**
- Ramp Progress: 250/250 clients running
- Segments Downloaded: 500 (stalled) - should be increasing
- Segment Throughput: (no data) - should show MB/s percentiles
- HTTP Requests Successful: 33,962 at +249/s - actively increasing
- TCP Connections Success: 1,000 (100.00%) - healthy

---

## Previously Fixed Issues

Three issues were already discovered and fixed during implementation:

### Issue #1: Double-Drain Race Condition
- **Symptom**: TUI's throughput showed 0 even when data was available
- **Cause**: Both TUI (500ms tick) and statsUpdateLoop (1s tick) called `GetDebugStats()`, which drains histograms. Whichever ran first got the data.
- **Fix**: Added 1-second cache to `GetDebugStats()` so both consumers see the same data.
- **Files**: `internal/orchestrator/client_manager.go`

### Issue #2: Time-Based Guard Breaking Chain
- **Symptom**: Rapid-fire segment requests weren't counted
- **Cause**: `trackSegmentFromHTTP()` used a 1ms time guard that deleted pending segments without counting them
- **Fix**: Changed to URL-based comparison - same segment = skip, different segment = always count
- **Files**: `internal/parser/debug_events.go`

### Issue #3: HTTP Keep-Alive Missing Tracking
- **Symptom**: Throughput worked during ramp-up but stopped after
- **Cause**: FFmpeg only logs `[http @ ...] Opening '...'` for new connections. After keep-alive is established, this line is NOT logged.
- **Fix**: Added `reHTTPRequestGET` pattern to match `[http @ ...] request: GET /...` which fires for ALL requests.
- **Files**: `internal/parser/debug_events.go`

---

## Architecture Overview

### Data Flow Diagram

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          SEGMENT THROUGHPUT DATA FLOW                            │
└─────────────────────────────────────────────────────────────────────────────────┘

┌──────────────────────┐
│ FFmpeg stderr        │
│ (debug log lines)    │
└──────────┬───────────┘
           │
           ▼
┌──────────────────────────────────────────────────────────────────────────────────┐
│                        DebugEventParser.ParseLine()                              │
│                        internal/parser/debug_events.go:348                       │
│                                                                                  │
│  Matches 3 segment-related patterns:                                             │
│                                                                                  │
│  Pattern 1: reHLSRequest                                                         │
│  [hls @ 0x...] HLS request for url 'http://.../seg00123.ts'                     │
│  → handleHLSRequest() → pendingSegments[url] = now                              │
│  → Completes oldest pending segment → calculates wallTime                        │
│  NOTE: Only fires during INITIAL playlist parsing                               │
│                                                                                  │
│  Pattern 2: reHTTPOpen                                                           │
│  [http @ 0x...] Opening 'http://.../seg00123.ts' for reading                    │
│  → handleHTTPOpen() → trackSegmentFromHTTP()                                    │
│  NOTE: Only fires for NEW connections, not keep-alive                           │
│                                                                                  │
│  Pattern 3: reHTTPRequestGET (added to fix Issue #3)                            │
│  [http @ 0x...] request: GET /seg00123.ts HTTP/1.1                              │
│  → handleHTTPRequestGET() → trackSegmentFromHTTP()                              │
│  NOTE: Should fire for ALL requests including keep-alive                        │
└──────────────────────────────────────────────────────────────────────────────────┘
           │
           ▼
┌──────────────────────────────────────────────────────────────────────────────────┐
│                        trackSegmentFromHTTP()                                    │
│                        internal/parser/debug_events.go:827                       │
│                                                                                  │
│  1. Check if pendingSegments has entries                                         │
│  2. Find oldest pending segment                                                  │
│  3. Compare segment NAMES (not URLs) via extractSegmentName()                    │
│     - HLS uses full URL: http://10.177.0.10:17080/seg00001.ts                   │
│     - HTTP GET uses path: /seg00001.ts                                          │
│     - Both extract to: seg00001.ts                                              │
│  4. If SAME segment: skip (double-fire prevention)                              │
│  5. If DIFFERENT segment: complete oldest, calculate wallTime                    │
│  6. Lookup segment size from SegmentScraper                                      │
│  7. Calculate throughput = size / wallTime                                       │
│  8. Record to ThroughputHistogram                                               │
│  9. Add new segment to pendingSegments                                          │
└──────────────────────────────────────────────────────────────────────────────────┘
           │
           ▼
┌──────────────────────────────────────────────────────────────────────────────────┐
│                        ThroughputHistogram                                       │
│                        internal/parser/throughput_histogram.go                   │
│                                                                                  │
│  Lock-free histogram with 64 buckets (1 KB/s to 10 GB/s)                        │
│  - Record(bytesPerSec) → atomic increment bucket                                │
│  - Drain() → returns buckets AND RESETS to zero                                 │
│                                                                                  │
│  CRITICAL: Drain semantics ensure each aggregation window only                   │
│  contains recent samples, not cumulative history                                │
└──────────────────────────────────────────────────────────────────────────────────┘
           │
           ▼
┌──────────────────────────────────────────────────────────────────────────────────┐
│                        ClientManager.GetDebugStats()                             │
│                        internal/orchestrator/client_manager.go:581               │
│                                                                                  │
│  Called by: TUI (every 500ms), statsUpdateLoop (every 1s for Prometheus)        │
│                                                                                  │
│  1. Check cache (1s TTL to prevent double-drain)                                │
│  2. If stale, call computeDebugStats():                                         │
│     a. For each client's DebugEventParser:                                      │
│        - DrainThroughputHistogram() → get buckets, reset counters               │
│        - Collect into drainedHistograms slice                                   │
│     b. MergeBuckets() → combine all client histograms                           │
│     c. PercentileFromBuckets() → calculate P25, P50, P75, P95, P99              │
│  3. Store result in cache                                                       │
│  4. Return DebugStatsAggregate                                                  │
└──────────────────────────────────────────────────────────────────────────────────┘
           │
           ▼
┌──────────────────────────────────────────────────────────────────────────────────┐
│                        TUI renderLatencyStats()                                  │
│                        internal/tui/view.go:246                                  │
│                                                                                  │
│  Renders 3-column layout: Manifest Latency | Segment Latency | Segment Throughput│
│                                                                                  │
│  if m.debugStats.SegmentThroughputP50 > 0 {                                     │
│      // Show percentile data                                                    │
│  } else {                                                                        │
│      // Show "(no data)"                                                         │
│  }                                                                               │
│                                                                                  │
│  "(no data)" appears when SegmentThroughputP50 == 0                             │
└──────────────────────────────────────────────────────────────────────────────────┘
```

### Segment Size Lookup (Parallel Path)

```
┌──────────────────────────────────────────────────────────────────────────────────┐
│                        SegmentScraper                                            │
│                        internal/metrics/segment_scraper.go                       │
│                                                                                  │
│  Background goroutine that:                                                      │
│  - Fetches /files/json/ from origin every 5s ± 500ms jitter                     │
│  - Caches segment name → size in sync.Map                                       │
│  - Rolling window eviction (keeps last 30 segments)                             │
│                                                                                  │
│  GetSegmentSize(name string) (int64, bool):                                     │
│  - Lock-free lookup via sync.Map.Load()                                         │
│  - Returns (size, true) if found, (0, false) if not                            │
│                                                                                  │
│  Passed to DebugEventParser as SegmentSizeLookup interface                      │
└──────────────────────────────────────────────────────────────────────────────────┘
```

---

## ROOT CAUSE IDENTIFIED

**FFmpeg does NOT log segment requests during steady-state playback.**

### Evidence from FFmpeg Debug Capture

Captured with `make ffmpeg-debug-capture` (see `internal/parser/testdata/ffmpeg_stderr_log`):

```
# Initial parsing (all at 16:25:36.940) - segments ARE logged:
[hls @ 0x...] HLS request for url 'http://10.177.0.10:17080/seg00050.ts'
[hls @ 0x...] Opening 'http://10.177.0.10:17080/seg00050.ts' for reading
[http @ 0x...] request: GET /seg00050.ts HTTP/1.1
... (same for seg00051.ts, seg00052.ts)

# After initial parsing (16:25:38.981+) - ONLY manifests logged:
[hls @ 0x...] Opening 'http://10.177.0.10:17080/stream.m3u8' for reading
[http @ 0x...] request: GET /stream.m3u8 HTTP/1.1
... (repeats every ~1 second, NO segment logs)
```

### Why This Happens

FFmpeg's HLS demuxer has two phases:

1. **Initial format probing** (`avformat_find_stream_info`):
   - Opens multiple segments concurrently to detect codec, resolution, etc.
   - Logs HLS requests, HTTP opens, and HTTP GETs
   - All segments opened in the same millisecond (parallel downloads)

2. **Steady-state streaming**:
   - Reads from buffered segment data
   - Opens new segments as buffer depletes
   - **Does NOT log segment requests** (different code path, no verbose logging)

### Impact on Tracking

| Phase | New Clients | Segment Logs | Throughput Tracking |
|-------|-------------|--------------|---------------------|
| Ramp-up | Yes | Yes (initial parsing) | Works ✅ |
| Steady-state | No | No | Fails ❌ |

### Additional Issue: Concurrent Segment Opens

During initial parsing, segments are opened **concurrently**, not sequentially:
- seg00050, seg00051, seg00052 all opened at 16:25:36.940
- Wall time between requests ≈ 0ms
- Throughput = size / 0 = undefined (guard rejects)

---

## Hypotheses for Current Bug

### ~~Hypothesis 1: `reHTTPRequestGET` Pattern Not Matching~~ (DISPROVEN)

**Theory**: The regex pattern for HTTP GET requests may not be matching FFmpeg's actual log format.

**Evidence**: Pattern matches correctly during initial parsing. The issue is that FFmpeg doesn't log segment requests in steady-state at all.

### Hypothesis 2: Log Level Missing `request: GET` Lines

**Theory**: FFmpeg may only log `request: GET` at certain log levels. We may be running with a log level that doesn't include it.

**Evidence needed**:
- Check FFmpeg command line arguments for `-loglevel`
- Verify `request: GET` appears in raw stderr

**Action**: Test with `-loglevel debug` and `-loglevel verbose` to see if pattern appears.

### Hypothesis 3: pendingSegments Map Always Empty

**Theory**: After initial parsing, no events are adding new entries to `pendingSegments`, so `trackSegmentFromHTTP()` never completes any segments.

**Evidence needed**:
- Add debug counter for `len(pendingSegments)` at entry to `trackSegmentFromHTTP()`
- Check if map is always empty after ramp completes

**Action**: Add telemetry to track pendingSegments size over time.

### Hypothesis 4: Same-Segment Skip Logic Too Aggressive

**Theory**: The same-segment detection (`oldestSegment == newSegment`) may be incorrectly matching different segments.

**Evidence needed**:
- Log `extractSegmentName()` outputs for both oldestURL and new URL
- Verify they're extracting correctly from different URL formats

**Action**: Add trace logging to show segment name comparison.

### Hypothesis 5: HLS vs HTTP Events Out of Sync

**Theory**: During steady-state, maybe only HTTP GET events fire (not HLS Request or HTTP Open), but the timing/sequence is such that segments are never "completed".

Consider this sequence for a single client:
1. HTTP GET for seg00100.ts → adds to pendingSegments
2. HTTP GET for seg00101.ts → should complete seg00100.ts and add seg00101.ts
3. HTTP GET for seg00102.ts → should complete seg00101.ts and add seg00102.ts
...

If this chain is working, throughput should be calculated. But if:
- Only one segment ever gets added to pendingSegments
- OR the completion logic has a bug
- OR the histogram recording has a bug

Then throughput would be 0.

**Action**: Add counters for:
- How many times `trackSegmentFromHTTP()` is called
- How many times `pendingSegments` has > 0 entries
- How many times segments are actually completed
- How many times throughput is recorded

### Hypothesis 6: SegmentScraper Cache Miss

**Theory**: By the time segment completes, the segment may have been evicted from the scraper cache (rolling window too small).

**Evidence needed**:
- Compare segment numbers being requested vs. highest segment number in cache
- Check if `GetSegmentSize()` returns false for segments being completed

**Action**: Log cache hits/misses in `trackSegmentFromHTTP()`.

### Hypothesis 7: Wall Time Too Small (Guard Triggered)

**Theory**: The `minWallTimeForThroughput` guard (100µs) may be rejecting valid samples if wall time is very small.

**Evidence needed**:
- Log wall times being calculated
- Check if they're below 100µs

**Action**: Track and log wall times that pass/fail the guard.

---

## Investigation Plan

### Phase 1: Diagnostic Logging (Non-invasive)

Add debug logging without changing logic:

1. **In `ParseLine()`**: Count lines matching each segment-related pattern
2. **In `handleHTTPRequestGET()`**: Log the path being processed
3. **In `trackSegmentFromHTTP()`**:
   - Log `len(pendingSegments)` at entry
   - Log segment name comparison (oldest vs new)
   - Log when completion happens (with wallTime and size)
   - Log cache hit/miss for segment size lookup
4. **In `recordThroughput()`**: Log each throughput sample being recorded

### Phase 2: Unit Test Reproduction

Create a unit test that simulates steady-state behavior:

```go
func TestDebugEventParser_SteadyStateStreaming(t *testing.T) {
    // Setup with mock SegmentSizeLookup
    // Feed sequence of HTTP GET lines (only, no HLS or HTTP Open)
    // Verify throughput histogram has samples
    // Verify SegmentCount increments
}
```

### Phase 3: Live Debugging

If unit tests pass but live still fails:

1. Run with GODEBUG or pprof to capture runtime state
2. Add Prometheus metrics for internal counters
3. Consider adding a debug endpoint to dump parser state

### Phase 4: FFmpeg Log Analysis

Capture raw FFmpeg stderr and analyze:

```bash
# Run one client with stderr capture
./bin/go-ffmpeg-hls-swarm -clients=1 -duration=60s \
    -origin-metrics-host=10.177.0.10 2>&1 | tee ffmpeg_debug.log

# Search for patterns
grep -E '\[http @ .*\] request: GET' ffmpeg_debug.log
grep -E '\[http @ .*\] Opening' ffmpeg_debug.log
grep -E '\[hls @ .*\] HLS request' ffmpeg_debug.log
```

---

## Code Locations

| Component | File | Key Lines |
|-----------|------|-----------|
| Pattern matching | `internal/parser/debug_events.go` | 141 (reHTTPRequestGET) |
| ParseLine entry | `internal/parser/debug_events.go` | 348 |
| HTTP GET handler | `internal/parser/debug_events.go` | 813 |
| Segment tracking | `internal/parser/debug_events.go` | 827 |
| Throughput recording | `internal/parser/debug_events.go` | 1017 |
| Histogram | `internal/parser/throughput_histogram.go` | 28 (Record), 59 (Drain) |
| Aggregation | `internal/orchestrator/client_manager.go` | 609 |
| TUI display | `internal/tui/view.go` | 287 |

---

## Expected Behavior vs Actual Behavior

| Metric | Expected | Actual |
|--------|----------|--------|
| Segments Downloaded | Incrementing ~250 * (elapsed / segment_duration) | Stalled at 500 |
| Segment Throughput P50 | ~50 MB/s (based on segment size / latency) | 0 (no data) |
| HTTP Requests | Incrementing | +249/s (working) |
| Segment Latency | Should show percentiles | Shows data (working) |

**Key observation**: HTTP layer is working (requests incrementing), but HLS layer segment tracking is stalled.

---

## Potential Solutions

### Option 1: Use `-loglevel trace` (Highest Verbosity)

FFmpeg's `trace` level may include more HTTP-level logging. However:
- Produces massive amounts of output (~10x more than debug)
- May include raw packet data
- Performance impact on 250+ clients

**Action**: Test with `-loglevel repeat+level+datetime+trace` and check for segment logs.

### Option 2: Track Segments via Progress Output

FFmpeg's `-progress` output includes `total_size` which increases as bytes are downloaded.

**Approach**:
- Track `total_size` delta between progress updates
- Correlate with known segment sizes from origin
- Infer segment completion when delta ≈ segment size

**Limitations**:
- Less accurate timing (progress updates every ~1s)
- Can't distinguish individual segments
- Cumulative bytes, not per-segment

### Option 3: Estimate from Manifest + Origin Metrics

**Approach**:
- Count manifest refreshes (we track this accurately)
- Each manifest refresh typically means a new segment is needed
- Use origin's `nginx_http_requests_total` for ground truth
- Estimate throughput from `Total Bytes / Elapsed Time`

**Limitations**:
- Aggregated estimate, not per-segment percentiles
- Depends on origin metrics being available

### Option 4: Use FFmpeg's `-stats` Output

FFmpeg's real-time stats line includes bitrate:
```
frame=  358 fps= 20 q=-1.0 size=N/A time=00:00:05.96 bitrate=N/A speed=0.341x
```

**Approach**:
- Parse `bitrate` from stats line (when available)
- Use `size` and `time` to calculate average throughput

**Limitations**:
- Shows N/A for live streams (no total size known)
- Aggregated, not per-segment

### Option 5: Infer from Playback Progress

**Approach**:
- Track `out_time` from progress updates
- Each 2-second increment = 1 segment consumed
- Look up segment size from scraper cache
- Calculate throughput = segment_size / 2s (segment duration)

**Limitations**:
- Assumes segments are consumed at real-time rate
- Doesn't capture actual download time, just playback time

### Recommended Solution

**Hybrid approach**:
1. Keep current HLS/HTTP pattern matching for ramp-up (works well)
2. Add progress-based estimation for steady-state
3. Use origin metrics as validation/ground-truth

---

## Test Plan After Fix

1. Run 250 clients for 5 minutes
2. Verify Segment Throughput shows data throughout entire run
3. Verify Segments Downloaded continues incrementing after ramp completes
4. Verify metrics match: Origin's Net Out KB/s ≈ sum(Segment Throughput * segment_count)
5. Run with different client counts (10, 50, 100, 250) to verify scaling
6. Run unit tests with `-race` flag to check for data races
