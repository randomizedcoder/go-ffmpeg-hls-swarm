# Your First Load Test

> **Type**: Tutorial
> **Time**: 10 minutes
> **Prerequisites**: [INSTALLATION.md](./INSTALLATION.md) complete

This tutorial walks you through running your first HLS load test with go-ffmpeg-hls-swarm.

---

## Step 1: Start a Test Origin (Optional)

If you don't have an HLS stream to test, start the included test origin:

```bash
# Terminal 1: Start test origin
nix run .#test-origin
```

Wait for output like:
```
Starting HLS generator...
Stream ready at http://localhost:17080/stream.m3u8
```

**Alternative:** Use a public test stream:
```
https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8
```

---

## Step 2: Run a Quick Test

Open a new terminal and run:

```bash
# 5 clients for 30 seconds
nix run .#run -- -clients 5 -duration 30s http://localhost:17080/stream.m3u8
```

You should see:
- Startup banner with configuration
- Live TUI dashboard (enabled by default)
- Real-time metrics

---

## Step 3: Understanding the TUI Dashboard

The TUI dashboard shows:

```
┌────────────────────────────────────────────────────────────────┐
│  go-ffmpeg-hls-swarm                     Elapsed: 0:00:15      │
├────────────────────────────────────────────────────────────────┤
│  Clients        5/5    100%    Seg/s        12.3               │
│  Stalled        0              Manifest/s   5.1                │
│  Avg Speed      1.02x          Throughput   45.2 MB/s          │
│  Errors         0              HTTP 4xx     0                  │
├────────────────────────────────────────────────────────────────┤
│  P50 Latency    45ms           Origin Net Out  48.1 MB/s       │
│  P95 Latency    120ms          Origin CPU      23%             │
│  P99 Latency    250ms          Lookup         100% (120/120)   │
└────────────────────────────────────────────────────────────────┘
```

### Key Metrics

| Metric | Meaning | Healthy Value |
|--------|---------|---------------|
| Clients | Active / Target | Should reach 100% |
| Stalled | Clients buffering | Should be 0 |
| Avg Speed | Playback speed | Should be >= 1.0x |
| Throughput | Data downloaded | Varies by content |
| P95 Latency | 95th percentile | < 500ms typical |
| Errors | Total errors | Should be 0 |

---

## Step 4: Increase Load

Now increase the load gradually:

```bash
# 50 clients
nix run .#run -- -clients 50 -ramp-rate 10 -duration 1m \
  http://localhost:17080/stream.m3u8
```

Watch for:
- Stalled clients (indicates capacity issues)
- Increasing latency
- Error rate

---

## Step 5: Enable Origin Metrics (Advanced)

For deeper insights, enable origin metrics. This requires a MicroVM with Prometheus exporters:

```bash
# Terminal 1: Start MicroVM with TAP networking
make microvm-start-tap

# Terminal 2: Run load test with origin metrics
nix run .#run -- -clients 100 \
  -origin-metrics-host 10.177.0.10 \
  http://10.177.0.10:17080/stream.m3u8
```

The TUI now shows origin server metrics:
- Origin CPU usage
- Origin network out (actual bytes served)
- Nginx connection count

---

## Step 6: View Prometheus Metrics

While the test runs, access Prometheus metrics:

```bash
curl http://localhost:17091/metrics | grep hls_swarm
```

Key metrics:
```
hls_swarm_active_clients 50
hls_swarm_segment_requests_total 15000
hls_swarm_segment_throughput_30s_bytes_per_second 50000000
hls_swarm_inferred_latency_p95_seconds 0.15
```

---

## Step 7: Stop the Test

Press `Ctrl+C` to stop the test gracefully.

You'll see a summary:
```
Test completed.
  Duration: 1m0s
  Peak clients: 50
  Total segments: 15000
  Errors: 0
```

---

## Common Issues

### "Stream not found" error

Verify the stream URL is accessible:
```bash
curl -I http://localhost:17080/stream.m3u8
```

### Clients stalling

Causes:
- Origin server overloaded
- Network bottleneck
- FFmpeg CPU-bound

Solutions:
- Reduce client count
- Use faster hardware
- Check origin server logs

### High latency

Causes:
- Origin server slow
- Network latency
- DNS resolution (use `-resolve`)

Solutions:
- Test with IP directly
- Increase `-timeout`
- Check network path

---

## Next Steps

- **Scale up**: Try 100, 300, 500 clients
- **Stress test**: Use `nix run .#swarm-client-stress`
- **Cache testing**: Use `-no-cache` to bypass CDN
- **Production**: See [PRODUCTION_DEPLOYMENT.md](../operations/PRODUCTION_DEPLOYMENT.md)

---

## Quick Reference

### Common Commands

```bash
# Quick smoke test
nix run .#run -- -clients 5 http://origin/stream.m3u8

# Standard load test
nix run .#run -- -clients 100 -duration 5m http://origin/stream.m3u8

# Stress test
nix run .#run -- -clients 300 -ramp-rate 50 http://origin/stream.m3u8

# With origin metrics
nix run .#run -- -clients 100 -origin-metrics-host 10.177.0.10 \
  http://10.177.0.10:17080/stream.m3u8

# Disable TUI (for CI/scripts)
nix run .#run -- -clients 100 -tui=false http://origin/stream.m3u8
```

### Make Targets

```bash
make load-test-50      # 50 clients, 30s
make load-test-100     # 100 clients, 30s
make load-test-300     # 300 clients, 30s
```
