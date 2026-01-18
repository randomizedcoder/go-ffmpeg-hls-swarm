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

> ⚠️ **Be respectful**: Don't hammer public test streams with high concurrency. Use ≤10 clients. For serious load testing, use your own infrastructure.

---

## Prerequisites

Before starting, ensure you have:

| Requirement | How to Check | Install |
|-------------|--------------|---------|
| Go 1.21+ | `go version` | [golang.org/dl](https://golang.org/dl/) |
| FFmpeg | `ffmpeg -version` | `apt install ffmpeg` / `brew install ffmpeg` |

### Verify FFmpeg HLS Support

Before proceeding, confirm FFmpeg can fetch HLS streams:

```bash
# This should show playlist parsing and segment downloads
ffmpeg -hide_banner -loglevel info \
  -i "https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8" \
  -t 5 -c copy -f null -
```

If you see `Input #0, hls...` followed by segment info, you're ready. Press `Ctrl+C` to stop early.

---

## Step 1: Build

```bash
# Clone the repository (update URL when repo is published)
git clone https://github.com/randomizedcoder/go-ffmpeg-hls-swarm.git
cd go-ffmpeg-hls-swarm

# Build the binary
go build -o go-ffmpeg-hls-swarm ./cmd/go-ffmpeg-hls-swarm
```

**Or with Nix** (if you use Nix):
```bash
nix build
# Binary is at ./result/bin/hlsswarm
```

---

## Step 2: Test with a Public Stream

Use Mux's public test stream to verify everything works:

```bash
./go-ffmpeg-hls-swarm -clients 5 https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8
```

**Expected output:**
```
Preflight checks:
  ✓ ffmpeg: found at /usr/bin/ffmpeg (version 6.1)
  ✓ file_descriptors: 8192 available (need 200 for 5 clients)

Starting 5 clients at 5/sec...
  [00:00] client_started id=0 pid=12345
  [00:00] client_started id=1 pid=12346
  [00:00] client_started id=2 pid=12347
  [00:00] client_started id=3 pid=12348
  [00:01] client_started id=4 pid=12349
  [00:01] ramp_complete clients=5

Press Ctrl+C to stop.
```

> *Note: PIDs and exact timing will vary. The key indicator of success is seeing all clients start without errors.*

---

## Step 3: Check Metrics

In another terminal:

```bash
curl -s http://localhost:9090/metrics | grep hlsswarm
```

**Expected output:**
```
# HELP hlsswarm_clients_active Currently running FFmpeg processes
# TYPE hlsswarm_clients_active gauge
hlsswarm_clients_active 5
# HELP hlsswarm_clients_target Configured target client count
# TYPE hlsswarm_clients_target gauge
hlsswarm_clients_target 5
# HELP hlsswarm_clients_started_total Total clients started
# TYPE hlsswarm_clients_started_total counter
hlsswarm_clients_started_total 5
# HELP hlsswarm_clients_restarted_total Total restart events
# TYPE hlsswarm_clients_restarted_total counter
hlsswarm_clients_restarted_total 0
```

---

## Step 4: Stop the Test

Press `Ctrl+C` for graceful shutdown. All FFmpeg processes will receive SIGTERM and exit cleanly.

**Expected exit summary:**
```
^C
Received SIGINT, shutting down...
  [00:45] client_stopped id=0 uptime=44.2s
  [00:45] client_stopped id=1 uptime=44.1s
  [00:45] client_stopped id=2 uptime=44.0s
  [00:45] client_stopped id=3 uptime=43.9s
  [00:45] client_stopped id=4 uptime=43.8s

═══════════════════════════════════════════════════════════════════
                        go-ffmpeg-hls-swarm Exit Summary
═══════════════════════════════════════════════════════════════════
Run Duration:           00:00:45
Target Clients:         5
Peak Active Clients:    5
Median Active Clients:  5

Lifecycle:
  Total Starts:         5
  Total Restarts:       0
  Clean Exits (0):      0
  SIGTERM Exits (143):  5

Metrics endpoint was: http://0.0.0.0:9090/metrics
═══════════════════════════════════════════════════════════════════
```

---

## What Success Looks Like

A successful load test shows:

### During the Test
- **Ramp-up completes**: All clients start without errors
- **Stable active count**: `hlsswarm_clients_active` stays near your target
- **Low restart rate**: Few or no restarts indicate healthy connections

> 💡 **Tip**: Always correlate `clients_active` with `bytes_downloaded_total` to confirm real load generation. Active clients that aren't downloading bytes may be stalled or stuck in reconnect loops.

### In Your Infrastructure
- **Origin/CDN metrics show load**: Requests per second increase as expected
- **No 5xx errors**: Your infrastructure handles the load
- **Latency stays acceptable**: Response times don't spike

### Signs Something's Wrong

| Symptom | Likely Cause | Fix |
|---------|--------------|-----|
| Many restarts | Origin overloaded or returning errors | Lower client count, check origin logs |
| Clients can't start | Bad stream URL or FFmpeg issue | Test URL with `ffmpeg -i <URL> -t 5 -f null -` |
| "too many open files" | File descriptor limit too low | Run `ulimit -n 8192` |
| Memory growing | Too many clients for system | Lower client count |

---

## Common Next Steps

### Increase Load

```bash
# 50 clients, ramping up at 10 per second
./go-ffmpeg-hls-swarm -clients 50 -ramp-rate 10 https://your-cdn.com/live/master.m3u8
```

### Test Your Own Stream

Replace the URL with your HLS master playlist:

```bash
./go-ffmpeg-hls-swarm -clients 20 https://your-cdn.com/live/master.m3u8
```

### Simulate Different Viewer Types

```bash
# Premium viewers (highest quality)
./go-ffmpeg-hls-swarm -clients 50 -variant highest https://...

# Mobile viewers (lowest quality)
./go-ffmpeg-hls-swarm -clients 50 -variant lowest https://...

# Maximum stress (all qualities simultaneously — 4× bandwidth with 4 variants!)
./go-ffmpeg-hls-swarm -clients 50 -variant all https://...
```

### Bypass CDN Cache

Test your origin server directly:

```bash
./go-ffmpeg-hls-swarm -clients 20 -no-cache https://your-cdn.com/live/master.m3u8
```

### Test a Specific Server

Bypass DNS and hit a specific IP (requires `--dangerous` flag):

```bash
./go-ffmpeg-hls-swarm -clients 20 -resolve 192.168.1.100 --dangerous \
  https://your-cdn.com/live/master.m3u8
```

> ⚠️ **Warning**: `--dangerous` disables TLS certificate verification. Only use in controlled environments where you trust the network path.

---

## Troubleshooting

### "too many open files"

Increase your file descriptor limit:

```bash
ulimit -n 8192
```

Then retry. See [OPERATIONS.md](OPERATIONS.md) for permanent configuration.

### FFmpeg exits immediately

Check that:
1. The HLS URL is accessible: `curl -I https://your-url/master.m3u8`
2. FFmpeg can play it: `ffmpeg -i https://your-url/master.m3u8 -t 5 -f null -`

### No metrics at localhost:9090

The metrics server starts when the orchestrator starts. If it fails to bind:
- Check if port 9090 is already in use: `lsof -i :9090`
- Use a different port: `-metrics 0.0.0.0:9091`

### High restart rate

This usually means:
1. **Origin is overloaded** — reduce client count
2. **Network issues** — check connectivity to the stream
3. **Stream ended** — for VOD streams, this is expected

Check FFmpeg stderr output with `-v` flag:
```bash
./go-ffmpeg-hls-swarm -v -clients 5 https://...
```

---

## Next Steps

| Want to... | Read |
|------------|------|
| See all CLI flags | [CONFIGURATION.md](CONFIGURATION.md) |
| Tune for high concurrency | [OPERATIONS.md](OPERATIONS.md) |
| Understand the architecture | [DESIGN.md](DESIGN.md) |
| Interpret metrics | [OBSERVABILITY.md](OBSERVABILITY.md) |

