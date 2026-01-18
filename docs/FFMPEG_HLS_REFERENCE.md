# FFmpeg HLS Reference for go-ffmpeg-hls-swarm

> **Type**: Reference Documentation
> **Audience**: Contributors, advanced users
> **Related**: [DESIGN.md](DESIGN.md), [CONFIGURATION.md](CONFIGURATION.md)

This document provides a technical deep dive into FFmpeg's HLS implementation, based on source code analysis of `libavformat/hls.c`, `http.c`, and `hlsproto.c`.

---

## Table of Contents

- [Overview](#overview)
- [1. HLS Demuxer vs HLS Protocol](#1-hls-demuxer-vs-hls-protocol)
- [2. Recommended Command for Load Testing](#2-recommended-command-for-load-testing)
- [3. HLS Demuxer Options](#3-hls-demuxer-options)
- [4. HTTP Protocol Options](#4-http-protocol-options)
- [5. Variant/Rendition Selection](#5-variantrendition-selection)
- [6. Redirect Handling](#6-redirect-handling)
- [7. Live Stream Behavior](#7-live-stream-behavior)
- [8. Error Handling & Reconnection](#8-error-handling--reconnection)
- [9. Implementation Details](#9-implementation-details)
- [10. Progress Protocol for Metrics](#10-progress-protocol-for-metrics)
- [11. Command Construction for go-ffmpeg-hls-swarm](#11-command-construction-for-go-ffmpeg-hls-swarm)

---

## Overview

FFmpeg provides two ways to consume HLS streams:

1. **HLS Demuxer** (`libavformat/hls.c`) - Full-featured, recommended
2. **HLS Protocol** (`libavformat/hlsproto.c`) - Deprecated, limited

For load testing, we use the **HLS Demuxer** which:
- Automatically parses master playlists
- Handles variant selection
- Follows HTTP redirects via the HTTP protocol layer
- Supports live stream playlist refresh
- Handles segment encryption (AES-128, SAMPLE-AES)

---

## 1. HLS Demuxer vs HLS Protocol

### HLS Demuxer (Recommended)

```c
// From libavformat/hls.c
const FFInputFormat ff_hls_demuxer = {
    .p.name         = "hls",
    .p.long_name    = "Apple HTTP Live Streaming",
    // ... full implementation
};
```

**Use by**: Providing direct URL to `.m3u8` file

```bash
ffmpeg -i https://example.com/live/master.m3u8 ...
```

### HLS Protocol (Deprecated)

```c
// From libavformat/hlsproto.c - line 208-213
av_log(h, AV_LOG_WARNING,
       "Using the hls protocol is discouraged, please try using the "
       "hls demuxer instead. The hls demuxer should be more complete "
       "and work as well as the protocol implementation.");
```

**Key difference**: The protocol handler (`hls+http://...`) only selects the highest bandwidth variant. The demuxer exposes all variants and lets you choose.

---

## 2. Recommended Command for Load Testing

### Basic Command (Highest Quality, All Streams)

```bash
ffmpeg -hide_banner -nostdin -loglevel info \
  -i "https://example.com/live/master.m3u8" \
  -map 0 -c copy -f null -
```

### With Reconnection (Recommended for Load Testing)

```bash
ffmpeg -hide_banner -nostdin -loglevel info \
  -reconnect 1 \
  -reconnect_streamed 1 \
  -reconnect_delay_max 5 \
  -rw_timeout 15000000 \
  -user_agent "go-ffmpeg-hls-swarm/1.0" \
  -i "https://example.com/live/master.m3u8" \
  -map 0 -c copy -f null -
```

### Explanation

| Option | Purpose |
|--------|---------|
| `-hide_banner` | Suppress FFmpeg build info |
| `-nostdin` | Don't read from stdin (prevents blocking) |
| `-loglevel info` | Show useful progress without being too verbose |
| `-reconnect 1` | Auto-reconnect on disconnect |
| `-reconnect_streamed 1` | Reconnect even for non-seekable streams |
| `-reconnect_delay_max 5` | Max 5 seconds between reconnect attempts |
| `-rw_timeout 15000000` | 15 second network timeout (microseconds) |
| `-user_agent` | Custom User-Agent header |
| `-map 0` | Map all streams from input |
| `-c copy` | Copy without decoding (passthrough) |
| `-f null -` | Discard output (null muxer) |

---

## 3. HLS Demuxer Options

> ⚠️ **Critical implementation note**: All HLS demuxer options (e.g., `-live_start_index`, `-http_persistent`, `-seg_max_retry`) **must appear before `-i`** in the FFmpeg command line. Options placed after `-i` apply to outputs, not inputs, and will be silently ignored. This is a common source of bugs.

From `libavformat/hls.c` lines 2795-2833:

```c
static const AVOption hls_options[] = {
    {"live_start_index", "segment index to start live streams at (negative values are from the end)",
        OFFSET(live_start_index), AV_OPT_TYPE_INT, {.i64 = -3}, INT_MIN, INT_MAX, FLAGS},
    {"prefer_x_start", "prefer to use #EXT-X-START if it's in playlist instead of live_start_index",
        OFFSET(prefer_x_start), AV_OPT_TYPE_BOOL, { .i64 = 0 }, 0, 1, FLAGS},
    {"allowed_extensions", "List of file extensions that hls is allowed to access",
        OFFSET(allowed_extensions), AV_OPT_TYPE_STRING, {.str = "..."}, ...},
    {"max_reload", "Maximum number of times a insufficient list is attempted to be reloaded",
        OFFSET(max_reload), AV_OPT_TYPE_INT, {.i64 = 100}, 0, INT_MAX, FLAGS},
    {"m3u8_hold_counters", "The maximum number of times to load m3u8 when it refreshes without new segments",
        OFFSET(m3u8_hold_counters), AV_OPT_TYPE_INT, {.i64 = 1000}, 0, INT_MAX, FLAGS},
    {"http_persistent", "Use persistent HTTP connections",
        OFFSET(http_persistent), AV_OPT_TYPE_BOOL, {.i64 = 1}, 0, 1, FLAGS },
    {"http_multiple", "Use multiple HTTP connections for fetching segments",
        OFFSET(http_multiple), AV_OPT_TYPE_BOOL, {.i64 = -1}, -1, 1, FLAGS},
    {"http_seekable", "Use HTTP partial requests, 0 = disable, 1 = enable, -1 = auto",
        OFFSET(http_seekable), AV_OPT_TYPE_BOOL, { .i64 = -1}, -1, 1, FLAGS},
    {"seg_max_retry", "Maximum number of times to reload a segment on error.",
     OFFSET(seg_max_retry), AV_OPT_TYPE_INT, {.i64 = 0}, 0, INT_MAX, FLAGS},
};
```

### Usage in FFmpeg Command

```bash
# Set HLS-specific options before -i
ffmpeg -live_start_index -1 \       # Start from most recent segment
       -http_persistent 1 \          # Keep HTTP connections open
       -i "https://example.com/master.m3u8" \
       -c copy -f null -
```

### Key Options for Load Testing

| Option | Default | Recommended | Notes |
|--------|---------|-------------|-------|
| `live_start_index` | -3 | -3 or -1 | Segments from end for live streams |
| `http_persistent` | 1 | 1 | Reuse HTTP connections |
| `http_multiple` | -1 (auto) | -1 | Multiple connections for segments |
| `max_reload` | 100 | 100 | Playlist reload attempts |
| `m3u8_hold_counters` | 1000 | 1000 | Wait cycles for new segments |
| `seg_max_retry` | 0 | 3 | Segment download retries |

---

## 4. HTTP Protocol Options

From `libavformat/http.c` lines 156-194:

### Reconnection Options

```c
{ "reconnect", "auto reconnect after disconnect before EOF",
    OFFSET(reconnect), AV_OPT_TYPE_BOOL, { .i64 = 0 }, 0, 1, D },
{ "reconnect_at_eof", "auto reconnect at EOF",
    OFFSET(reconnect_at_eof), AV_OPT_TYPE_BOOL, { .i64 = 0 }, 0, 1, D },
{ "reconnect_on_network_error", "auto reconnect in case of tcp/tls error during connect",
    OFFSET(reconnect_on_network_error), AV_OPT_TYPE_BOOL, { .i64 = 0 }, 0, 1, D },
{ "reconnect_on_http_error", "list of http status codes to reconnect on",
    OFFSET(reconnect_on_http_error), AV_OPT_TYPE_STRING, { .str = NULL }, 0, 0, D },
{ "reconnect_streamed", "auto reconnect streamed / non seekable streams",
    OFFSET(reconnect_streamed), AV_OPT_TYPE_BOOL, { .i64 = 0 }, 0, 1, D },
{ "reconnect_delay_max", "max reconnect delay in seconds after which to give up",
    OFFSET(reconnect_delay_max), AV_OPT_TYPE_INT, { .i64 = 120 }, 0, UINT_MAX/1000/1000, D },
{ "reconnect_max_retries", "the max number of times to retry a connection",
    OFFSET(reconnect_max_retries), AV_OPT_TYPE_INT, { .i64 = -1 }, -1, INT_MAX, D },
{ "reconnect_delay_total_max", "max total reconnect delay in seconds after which to give up",
    OFFSET(reconnect_delay_total_max), AV_OPT_TYPE_INT, { .i64 = 256 }, 0, UINT_MAX/1000/1000, D },
```

### Usage

```bash
ffmpeg -reconnect 1 \
       -reconnect_streamed 1 \
       -reconnect_on_network_error 1 \
       -reconnect_on_http_error "5xx,4xx" \
       -reconnect_delay_max 10 \
       -reconnect_max_retries 5 \
       -i "https://example.com/master.m3u8" \
       -c copy -f null -
```

### Timeout Option

```c
// From doc/protocols.texi line 47-49
@item rw_timeout
Maximum time to wait for (network) read/write operations to complete,
in microseconds.
```

```bash
# 15 second timeout
ffmpeg -rw_timeout 15000000 -i "https://..." -c copy -f null -
```

---

## 5. Variant/Rendition Selection

### How FFmpeg Handles Master Playlists

From `libavformat/hls.c` lines 548-554 (documentation):

> This demuxer presents all AVStreams from all variant streams.
> The id field is set to the bitrate variant index number. By setting
> the discard flags on AVStreams (by pressing 'a' or 'v' in ffplay),
> the caller can decide which variant streams to actually receive.

**Default behavior**: All variants are loaded and streamed.

### Selecting Specific Streams

```bash
# Map all streams (default behavior)
ffmpeg -i "https://example.com/master.m3u8" -map 0 -c copy -f null -

# Map only video stream 0 (usually highest quality)
ffmpeg -i "https://example.com/master.m3u8" -map 0:v:0 -c copy -f null -

# Map only audio stream 0
ffmpeg -i "https://example.com/master.m3u8" -map 0:a:0 -c copy -f null -

# Map first video and first audio
ffmpeg -i "https://example.com/master.m3u8" -map 0:v:0 -map 0:a:0 -c copy -f null -
```

### Understanding Stream Selection

When FFmpeg opens a master playlist:

1. Parses all `#EXT-X-STREAM-INF` entries
2. Creates a variant for each bandwidth level
3. Exposes all streams from all variants
4. By default, streams are marked as "needed" (will be fetched)

From `hls.c` lines 1513-1557 (`playlist_needed` function):
- Checks `discard` flags on streams
- If all streams in a playlist are discarded, playlist is not needed
- Programs (variants) can also be discarded

### For Our Use Case (Highest Quality)

**Using `-map 0` downloads ALL variants** which is fine for load testing as it maximizes CDN load. However, if you want to simulate real viewer behavior (single variant):

```bash
# This will fetch segments from all quality levels
ffmpeg -i "https://example.com/master.m3u8" -map 0 -c copy -f null -

# To select only the video from the first program (often highest bitrate)
# Use ffprobe first to understand the stream layout
ffprobe -v quiet -print_format json -show_programs "https://example.com/master.m3u8"
```

---

## 6. Redirect Handling

### HTTP Redirect Behavior

From `libavformat/http.c`:

```c
#define MAX_REDIRECTS 8
#define MAX_CACHED_REDIRECTS 32
```

FFmpeg automatically:
1. Follows HTTP 301/302/303/307/308 redirects
2. Caches redirect mappings for efficiency
3. Limits to 8 consecutive redirects

**No special configuration needed** - redirects are handled automatically when using `https://` or `http://` URLs.

### Redirect Caching

From `http.c` lines 345-382:
- FFmpeg caches redirect destinations
- Cache entries have expiry times
- Avoids repeated redirect lookups

---

## 7. Live Stream Behavior

### Playlist Refresh

From `hls.c` lines 1561-1620 (`read_data` function):

```c
// Playlist refresh logic
if (!v->finished) {
    int64_t now = av_gettime_relative();
    if (now - v->last_load_time >= reload_interval) {
        // Reload playlist
    }
}
```

Key behaviors:
- **Live streams** (`#EXT-X-ENDLIST` absent): Playlist refreshed periodically
- **VOD streams** (`#EXT-X-ENDLIST` present): Single playlist fetch
- Refresh interval based on `#EXT-X-TARGETDURATION` or last segment duration

### Live Start Position

```c
// Default: start 3 segments from the end
{"live_start_index", ..., {.i64 = -3}, ...}
```

This is HLS specification compliant - clients should buffer 3 target durations.

### Segment Expiry Handling

From `hlsproto.c` lines 280-285:

```c
if (s->cur_seq_no < s->start_seq_no) {
    av_log(h, AV_LOG_WARNING,
           "skipping %d segments ahead, expired from playlist\n",
           s->start_seq_no - s->cur_seq_no);
    s->cur_seq_no = s->start_seq_no;
}
```

FFmpeg handles segment expiry gracefully - if segments are removed from playlist before being fetched, it skips ahead.

---

## 8. Error Handling & Reconnection

### Segment Fetch Errors

From `hls.c` lines 1679-1725:

```c
if (ret < 0) {
    // Segment fetch failed
    if (c->seg_max_retry > 0 && seg_reload_count < c->seg_max_retry) {
        // Retry
    } else {
        // Mark playlist as broken, continue with others
    }
}
```

### HTTP Connection Errors

The HTTP protocol layer handles:
- Connection refused
- Connection timeout
- TLS errors
- HTTP error codes (4xx, 5xx)

With reconnection enabled:
```bash
ffmpeg -reconnect 1 -reconnect_streamed 1 -reconnect_on_network_error 1 ...
```

FFmpeg will:
1. Wait with exponential backoff
2. Retry up to `reconnect_max_retries` times
3. Respect `Retry-After` header if present
4. Give up after `reconnect_delay_total_max` total delay

---

## 9. Implementation Details

### Key Data Structures

From `hls.c`:

```c
struct segment {
    int64_t duration;
    int64_t url_offset;
    int64_t size;
    char *url;
    char *key;
    enum KeyType key_type;
    uint8_t iv[16];
    struct segment *init_section;  // fMP4 initialization
};

struct variant {
    int bandwidth;
    int n_playlists;
    struct playlist **playlists;
    char audio_group[MAX_FIELD_LEN];
    char video_group[MAX_FIELD_LEN];
    char subtitles_group[MAX_FIELD_LEN];
};

typedef struct HLSContext {
    int n_variants;
    struct variant **variants;
    int n_playlists;
    struct playlist **playlists;
    int live_start_index;
    int http_persistent;
    int http_multiple;
    // ... more fields
} HLSContext;
```

### Playlist Parsing

From `hls.c`, the demuxer parses:
- `#EXTM3U` - Playlist marker
- `#EXT-X-STREAM-INF` - Variant info (bandwidth, resolution, codecs)
- `#EXT-X-MEDIA` - Alternative renditions (audio, subtitles)
- `#EXT-X-TARGETDURATION` - Segment duration hint
- `#EXT-X-MEDIA-SEQUENCE` - First segment number
- `#EXT-X-ENDLIST` - VOD marker (no more segments)
- `#EXTINF` - Segment duration
- `#EXT-X-KEY` - Encryption info
- `#EXT-X-MAP` - Initialization segment (fMP4)
- `#EXT-X-START` - Preferred start position

---

## 10. Progress Protocol for Metrics

FFmpeg provides a `-progress` flag that outputs machine-readable key-value pairs. This is **significantly more efficient** than parsing stderr with regex for every client.

### Basic Usage

```bash
# Output progress to a file
ffmpeg ... -progress /tmp/progress.txt -f null -

# Output progress to a Unix socket
ffmpeg ... -progress unix:///tmp/ffmpeg-progress.sock -f null -

# Output progress to a URL (HTTP POST)
ffmpeg ... -progress http://localhost:8080/progress -f null -
```

### Progress Output Format

FFmpeg outputs progress in key-value format, updating periodically:

```
frame=0
fps=0.00
stream_0_0_q=-1.0
bitrate=N/A
total_size=1234567
out_time_us=10000000
out_time_ms=10000
out_time=00:00:10.000000
dup_frames=0
drop_frames=0
speed=1.00x
progress=continue
```

### Key Fields for Load Testing

| Field | Description | Use Case |
|-------|-------------|----------|
| `total_size` | Bytes downloaded | Track bandwidth consumption |
| `out_time_us` | Output timestamp (microseconds) | Detect stalls (value not increasing) |
| `speed` | Playback speed (1.0x = realtime) | FFmpeg is keeping up if ≥1.0x |
| `progress` | `continue` or `end` | Detect stream completion |

### Implementation Strategy

For go-ffmpeg-hls-swarm, each supervisor can:

1. **Create a unique pipe/socket** per client (e.g., `/tmp/go-ffmpeg-hls-swarm-progress-{clientID}.sock`)
2. **Pass `-progress unix://{socket}`** to FFmpeg
3. **Read key-value pairs** asynchronously in a goroutine
4. **Update metrics** without string-parsing stderr

```go
// supervisor/progress.go

type ProgressData struct {
    TotalSize   int64  // Bytes downloaded
    OutTimeUS   int64  // Output timestamp in microseconds
    Speed       float64
    IsComplete  bool
}

// ReadProgress reads FFmpeg progress from a Unix socket
func (s *Supervisor) ReadProgress(ctx context.Context, socketPath string) <-chan ProgressData {
    ch := make(chan ProgressData)
    go func() {
        defer close(ch)
        // Listen on Unix socket, parse key=value lines
        // Send ProgressData on channel
    }()
    return ch
}
```

### Benefits Over Stderr Parsing

| Approach | CPU Overhead | Reliability | Data Richness |
|----------|--------------|-------------|---------------|
| Stderr regex | High (per-line parsing) | Medium (format varies) | Low |
| `-progress` protocol | Low (structured KV) | High (stable format) | High (bytes, speed, time) |

### ⚠️ Critical: Progress Pipe Blocking Risk

**The Problem**: FFmpeg's `-progress` writes are synchronous. If the orchestrator's progress-reading goroutine lags (e.g., during a CPU spike while managing 200+ clients), the OS pipe buffer (typically 64KB on Linux) fills up.

**The Risk**: When the pipe buffer is full, FFmpeg's main loop **blocks on the write()** syscall. This stalls the entire HLS download thread for that client. The measurement tool causes the very problem it's trying to measure—a classic "Heisenbug."

**The Fix**: Implement a non-blocking reader with drop semantics. See [SUPERVISION.md](SUPERVISION.md#53-non-blocking-progress-reader) for the canonical implementation.

Key requirements:
- Reader goroutine must **never block** on channel sends
- Use buffered channels with explicit drop-on-full
- Consider `sync.Pool` for progress struct allocation at scale (200+ clients)
- Accept that dropped progress packets are acceptable (we only need eventual consistency)

### Stall Detection with Progress

Track `total_size` or `out_time_us` to detect stalled clients:

```go
func (s *Supervisor) detectStall(prev, curr ProgressData, interval time.Duration) bool {
    // If bytes haven't increased in the interval, client is stalled
    return curr.TotalSize == prev.TotalSize && interval > 30*time.Second
}
```

This is more reliable than `-rw_timeout` because:
- `-rw_timeout` only triggers on socket-level timeouts
- A server sending 1 byte every 10 seconds won't trigger `-rw_timeout`
- Progress-based stall detection catches "slow drip" servers

---

## 11. Command Construction for go-ffmpeg-hls-swarm

### Minimal Load Test Command

```go
func (r *FFmpegRunner) BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error) {
    args := []string{
        "-hide_banner",
        "-nostdin",
        "-loglevel", "info",
        "-i", r.StreamURL,
        "-map", "0",
        "-c", "copy",
        "-f", "null",
        "-",
    }
    return exec.CommandContext(ctx, r.BinaryPath, args...), nil
}
```

### Full-Featured Load Test Command

```go
func (r *FFmpegRunner) BuildCommand(ctx context.Context, clientID int) (*exec.Cmd, error) {
    args := []string{
        "-hide_banner",
        "-nostdin",
        "-loglevel", "info",
    }

    // Reconnection settings (applied to HTTP protocol)
    if r.Reconnect {
        args = append(args,
            "-reconnect", "1",
            "-reconnect_streamed", "1",
            "-reconnect_on_network_error", "1",
            "-reconnect_delay_max", "5",
        )
    }

    // Network timeout
    if r.Timeout > 0 {
        args = append(args, "-rw_timeout", fmt.Sprintf("%d", r.Timeout.Microseconds()))
    }

    // User-Agent
    if r.UserAgent != "" {
        args = append(args, "-user_agent", r.UserAgent)
    }

    // HLS demuxer options (before -i)
    if r.SegMaxRetry > 0 {
        args = append(args, "-seg_max_retry", fmt.Sprintf("%d", r.SegMaxRetry))
    }

    // Input URL
    args = append(args, "-i", r.StreamURL)

    // Output mapping
    args = append(args,
        "-map", "0",     // All streams
        "-c", "copy",    // No transcoding
        "-f", "null",    // Null output
        "-",
    )

    return exec.CommandContext(ctx, r.BinaryPath, args...), nil
}
```

### Argument Order Matters

FFmpeg options are position-sensitive:

```
[global options] [input options] -i input [output options] output
```

- **Before `-i`**: Input options (reconnect, timeout, HLS options)
- **After `-i`**: Output options (-map, -c, -f)

### Complete Example

```bash
ffmpeg \
  -hide_banner \
  -nostdin \
  -loglevel info \
  -reconnect 1 \
  -reconnect_streamed 1 \
  -reconnect_on_network_error 1 \
  -reconnect_delay_max 5 \
  -rw_timeout 15000000 \
  -user_agent "go-ffmpeg-hls-swarm/1.0" \
  -seg_max_retry 3 \
  -i "https://example.com/live/master.m3u8" \
  -map 0 \
  -c copy \
  -f null \
  -
```

---

## Appendix: Quick Reference

### Exit Codes

| Code | Meaning | Action |
|------|---------|--------|
| 0 | Success / stream ended | Restart if live |
| 1 | Generic error | Restart with backoff |
| 137 | SIGKILL | External kill |
| 143 | SIGTERM | Graceful stop |

### Useful Environment Variables

```bash
# Increase HTTP buffer size
export FFMPEG_HTTP_BUFFER_SIZE=1048576

# Enable debug output
export AV_LOG_FORCE_COLOR=1
```

### stderr Patterns to Watch

```
[hls] Skip segment ...                    # Segment expired
[http] Opening '...' for reading          # New segment fetch
[hls] No longer receiving playlist ...    # Variant disabled
[https] Reconnecting ...                  # Connection retry
```
