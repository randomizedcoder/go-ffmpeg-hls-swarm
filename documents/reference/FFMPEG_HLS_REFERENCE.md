# FFmpeg HLS Reference

> **Type**: Reference Documentation

Technical reference for FFmpeg HLS demuxer behavior and HLS protocol internals.

---

## HLS Protocol Overview

HTTP Live Streaming (HLS) is Apple's adaptive bitrate streaming protocol.

### Components

| Component | File Extension | Purpose |
|-----------|----------------|---------|
| Master Playlist | `.m3u8` | Lists available quality variants |
| Media Playlist | `.m3u8` | Lists segment URLs for a variant |
| Media Segment | `.ts` (MPEG-TS) | Actual video/audio data |
| Init Segment | `.mp4` | Initialization data (fMP4) |

### Example Master Playlist

```
#EXTM3U
#EXT-X-VERSION:3
#EXT-X-STREAM-INF:BANDWIDTH=2000000,RESOLUTION=1280x720
720p/stream.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=500000,RESOLUTION=640x360
360p/stream.m3u8
```

### Example Media Playlist

```
#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:1000
#EXTINF:2.000,
seg00001.ts
#EXTINF:2.000,
seg00002.ts
```

---

## FFmpeg HLS Demuxer Options

These options control how FFmpeg fetches HLS streams.

### Segment Retry

```bash
-seg_max_retry 3
```

Number of times to retry a segment download on failure. Default: 0 (no retry).

### Live Start Index

```bash
-live_start_index -3
```

Where to start in a live playlist:
- `-3` (default): Start 3 segments from end
- `0`: Start from beginning (VOD behavior)
- `-1`: Start from last segment

### Allow Cache

```bash
-allowed_extensions ALL
```

Allow caching of playlist and segments.

### HTTP Persistent

```bash
-http_persistent 1
```

Enable HTTP persistent connections (keep-alive).

---

## Playlist Refresh Behavior

FFmpeg refreshes the media playlist based on `#EXT-X-TARGETDURATION`:

| Target Duration | Refresh Interval |
|-----------------|------------------|
| 2s | ~2s |
| 4s | ~4s |
| 6s | ~6s |

For live streams, FFmpeg expects new segments to appear at this interval.

---

## Segment Download Pattern

1. FFmpeg downloads master playlist
2. Selects variant based on `-map` option
3. Downloads media playlist
4. Starts downloading segments
5. Periodically refreshes media playlist
6. Downloads new segments as they appear

### Debug Log Messages

```
[hls @ 0x...] Opening 'http://example.com/stream.m3u8' for reading
[hls @ 0x...] Opening 'http://example.com/seg00001.ts' for reading
[hls @ 0x...] Opening 'http://example.com/stream.m3u8' for reading
```

---

## Reconnection Behavior

When network issues occur:

1. FFmpeg detects failure (timeout/error)
2. Waits initial delay (exponential backoff)
3. Retries connection
4. Continues up to `reconnect_delay_max`
5. Either resumes or exits

### Reconnection Options

| Option | Purpose |
|--------|---------|
| `-reconnect 1` | Enable basic reconnection |
| `-reconnect_streamed 1` | Reconnect for streaming protocols |
| `-reconnect_on_network_error 1` | Reconnect on network errors |
| `-reconnect_delay_max N` | Maximum delay between retries |

---

## Variant Selection

### Program Mapping

HLS variants are represented as "programs" in FFmpeg:

```bash
# Map all programs (all variants)
-map 0

# Map specific program
-map 0:p:0  # First program (typically highest)
```

### Determining Programs

Use ffprobe to list programs:

```bash
ffprobe -v quiet -print_format json -show_programs stream.m3u8
```

Response includes:
- Program ID
- Bitrate
- Resolution
- Codecs

---

## Latency Considerations

### Playlist Caching

CDNs cache playlists, which affects latency:
- High cache TTL = higher latency
- Low cache TTL = more origin load

### Segment Duration Impact

| Segment Duration | Live Latency |
|------------------|--------------|
| 2s | ~6-10s |
| 4s | ~12-16s |
| 6s | ~18-24s |

Latency ≈ 3-4 × segment duration

### Low-Latency HLS (LL-HLS)

LL-HLS uses:
- Partial segments (0.33s)
- Blocking playlist reloads
- Preload hints

Not currently tested by go-ffmpeg-hls-swarm.

---

## Common HLS Errors

### 404 Segment Not Found

Segment expired from playlist before download completed.

**Causes:**
- Network latency
- Slow CDN propagation
- Segment duration too short

**Solutions:**
- Increase `-seg_max_retry`
- Increase playlist list size
- Increase segment duration

### Playlist Stale

No new segments appearing in expected time.

**Causes:**
- Encoder issues
- Origin server problems
- CDN caching issues

### Timeout

Connection or read timeout.

**Causes:**
- Network congestion
- Server overload
- Firewall issues

**Solutions:**
- Increase `-rw_timeout`
- Enable reconnection

---

## Segment Timing

### Ideal Segment Characteristics

| Property | Recommended |
|----------|-------------|
| Duration | 2-6 seconds |
| Start with keyframe | Yes (IDR) |
| GOP alignment | Consistent |

### CBR vs VBR

| Mode | Segment Behavior |
|------|------------------|
| CBR | Consistent sizes |
| VBR | Variable sizes |

For load testing, CBR is preferred for predictable throughput.

---

## Multi-Bitrate (ABR) Testing

### All Variants (-variant all)

Downloads all quality levels simultaneously.
- Maximum bandwidth usage
- Maximum segment requests
- Unrealistic viewer behavior
- Best for stress testing CDN

### Single Variant

Downloads one quality level.
- Realistic viewer simulation
- Lower bandwidth per client
- Higher client count possible

### Adaptive

Not supported by go-ffmpeg-hls-swarm. FFmpeg HLS demuxer doesn't do adaptive bitrate switching.

---

## Origin Server Requirements

For load testing, the HLS origin must:

1. Generate segments continuously
2. Update playlist atomically
3. Delete old segments (rolling window)
4. Handle concurrent connections
5. Serve consistent bitrates

The test-origin server in this project provides these capabilities.

---

## Related Documents

- [FFMPEG_COMMANDS.md](./FFMPEG_COMMANDS.md) - FFmpeg command reference
- [METRICS_REFERENCE.md](./METRICS_REFERENCE.md) - Prometheus metrics
- [CLI_FLAGS.md](./CLI_FLAGS.md) - CLI flag reference
