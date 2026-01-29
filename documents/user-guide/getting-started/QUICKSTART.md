# Quick Start Guide

> **Time**: 5 minutes
> **Goal**: Run your first load test against an HLS stream

This tool simulates viewer behavior at the **HTTP/HLS protocol layer**—fetching playlists and segments—not video playback or decoding. It exercises your CDN and origin infrastructure without consuming GPU/CPU for video rendering.

---

## Test Streams for Practice

Use these public streams to verify everything works before testing your own infrastructure:

| Stream | URL | Notes |
|--------|-----|-------|
| **Mux Test** | `https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8` | VOD, multiple variants |
| **Apple Bipbop** | `https://devstreaming-cdn.apple.com/videos/streaming/examples/bipbop_4x3/bipbop_4x3_variant.m3u8` | Apple's test stream |

> Be respectful: Don't hammer public test streams with high concurrency. Use 10 clients or fewer. For serious load testing, use your own infrastructure or the included test origin server.

---

## Step 1: Build (if not already done)

```bash
cd go-ffmpeg-hls-swarm
make build
```

Or with Nix:

```bash
nix build
```

---

## Step 2: Run a Test

### Basic Test (5 clients)

```bash
./bin/go-ffmpeg-hls-swarm -clients 5 \
  https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8
```

**Expected output:**

```
Preflight checks:
  ✓ ffmpeg: found at /usr/bin/ffmpeg
  ✓ file_descriptors: 8192 available (need 200 for 5 clients)

Starting 5 clients at 5/sec...
  client_started id=0 pid=12345
  client_started id=1 pid=12346
  ...
  ramp_complete clients=5

Press Ctrl+C to stop.
```

### With TUI Dashboard

For a live visual dashboard:

```bash
./bin/go-ffmpeg-hls-swarm -clients 10 -tui \
  https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8
```

### Timed Test

Run for a specific duration:

```bash
./bin/go-ffmpeg-hls-swarm -clients 20 -duration 30s \
  https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8
```

---

## Step 3: Check Metrics

In another terminal while the test runs:

```bash
curl -s http://localhost:17091/metrics | grep hls_swarm
```

**Expected output:**

```
# HELP hls_swarm_active_clients Currently running clients
# TYPE hls_swarm_active_clients gauge
hls_swarm_active_clients 5
# HELP hls_swarm_target_clients Target number of clients
# TYPE hls_swarm_target_clients gauge
hls_swarm_target_clients 5
# HELP hls_swarm_client_starts_total Total clients started
# TYPE hls_swarm_client_starts_total counter
hls_swarm_client_starts_total 5
```

---

## Step 4: Stop the Test

Press `Ctrl+C` for graceful shutdown. All FFmpeg processes will receive SIGTERM and exit cleanly.

**Exit summary:**

```
═══════════════════════════════════════════════════════════════════
                        go-ffmpeg-hls-swarm Exit Summary
═══════════════════════════════════════════════════════════════════
Run Duration:           00:00:45
Target Clients:         5
Peak Active Clients:    5

Lifecycle:
  Total Starts:         5
  Total Restarts:       0

Metrics endpoint was: http://0.0.0.0:17091/metrics
═══════════════════════════════════════════════════════════════════
```

---

## Using the Test Origin Server

Instead of testing against external streams, you can run a local test origin:

### Terminal 1: Start the test origin

```bash
make test-origin
```

This starts:
- FFmpeg generating a live HLS stream
- Nginx serving the stream on port 17080

### Terminal 2: Run load test

```bash
./bin/go-ffmpeg-hls-swarm -clients 50 -tui \
  http://localhost:17080/stream.m3u8
```

---

## Quick Recipes

### Stress Test (all quality levels)

```bash
./bin/go-ffmpeg-hls-swarm -clients 100 -variant all \
  https://your-cdn.com/live/master.m3u8
```

### Bypass CDN Cache (test origin directly)

```bash
./bin/go-ffmpeg-hls-swarm -clients 50 -no-cache \
  https://your-cdn.com/live/master.m3u8
```

### Test Specific Server by IP

```bash
./bin/go-ffmpeg-hls-swarm -clients 50 -resolve 192.168.1.100 --dangerous \
  https://your-cdn.com/live/master.m3u8
```

### With Origin Server Metrics

```bash
./bin/go-ffmpeg-hls-swarm -clients 100 -tui \
  -origin-metrics-host 10.177.0.10 \
  http://10.177.0.10:17080/stream.m3u8
```

---

## Troubleshooting

### "too many open files"

Increase file descriptor limit:

```bash
ulimit -n 8192
```

### FFmpeg exits immediately

Check that:
1. The HLS URL is accessible: `curl -I https://your-url/master.m3u8`
2. FFmpeg can play it: `ffmpeg -i https://your-url/master.m3u8 -t 5 -f null -`

### No metrics at localhost:17091

- Check if port 17091 is in use: `lsof -i :17091`
- Use a different port: `-metrics 0.0.0.0:9091`

### High restart rate

This usually means:
1. Origin is overloaded — reduce client count
2. Network issues — check connectivity
3. Stream ended — expected for VOD streams

Check FFmpeg stderr with `-v` flag:

```bash
./bin/go-ffmpeg-hls-swarm -v -clients 5 https://...
```

---

## Next Steps

| Want to... | Read |
|------------|------|
| See all CLI flags | [CLI_REFERENCE.md](../configuration/CLI_REFERENCE.md) |
| Run pre-built load tests | [LOAD_TESTING.md](../operations/LOAD_TESTING.md) |
| Tune for high concurrency | [OS_TUNING.md](../operations/OS_TUNING.md) |
| Understand metrics | [METRICS.md](../observability/METRICS.md) |
