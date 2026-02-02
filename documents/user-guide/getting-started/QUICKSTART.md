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

Instead of testing against external streams, you can run a local test origin. The test origin uses FFmpeg to generate a live HLS stream and Nginx to serve it.

### Option 1: Local Runner (All Platforms)

The simplest option, works on Linux and macOS:

**Terminal 1: Start the test origin**

```bash
make test-origin
```

This starts:
- FFmpeg generating a live HLS stream
- Nginx serving the stream on port 17080

**Terminal 2: Run load test**

```bash
./bin/go-ffmpeg-hls-swarm -clients 50 -tui \
  http://localhost:17080/stream.m3u8
```

### Option 2: Container (Docker/Podman)

For isolated testing with containers:

**Terminal 1: Build and run the origin container**

```bash
# Build the container image
make container-load

# Run the origin container in background
make container-run-origin
```

Or manually with Docker/Podman:

```bash
# Build
nix build .#test-origin-container
docker load < ./result

# Run (detached)
docker run -d --name hls-origin -p 17080:17080 go-ffmpeg-hls-swarm-test-origin:latest
```

**Terminal 2: Run load test**

```bash
./bin/go-ffmpeg-hls-swarm -clients 50 -tui \
  http://localhost:17080/stream.m3u8
```

**Cleanup:**

```bash
docker stop hls-origin && docker rm hls-origin
```

### Option 3: MicroVM (Linux + KVM)

For production-like testing with full VM isolation and Prometheus exporters:

**Prerequisites:**
- Linux with KVM enabled
- Verify with: `make microvm-check-kvm`

**Start the MicroVM origin (background, recommended):**

```bash
make microvm-start
```

This builds and starts the VM in the background with health polling. Once ready, you'll see connection info.

The MicroVM includes:
- FFmpeg generating a live HLS stream
- Nginx serving the stream on port 17080
- node_exporter on port 9100 (forwarded to 17100)
- nginx_exporter on port 9113 (forwarded to 17113)

**Run load test:**

```bash
./bin/go-ffmpeg-hls-swarm -clients 50 -tui \
  http://localhost:17080/stream.m3u8
```

**With origin metrics (recommended):**

```bash
./bin/go-ffmpeg-hls-swarm -clients 50 -tui \
  -origin-metrics http://localhost:17100/metrics \
  -nginx-metrics http://localhost:17113/metrics \
  http://localhost:17080/stream.m3u8
```

**Stop the MicroVM:**

```bash
make microvm-stop
```

**Alternative: Interactive mode (foreground):**

```bash
make microvm-origin
```

> **Note:** Interactive mode runs QEMU in the foreground. To exit: press `Ctrl+A` then release, then press `X`. If the VM hangs, kill it from another terminal with: `pkill -f 'qemu.*hls-origin'`

### Option 4: MicroVM with TAP Networking (High Performance)

For maximum throughput (~10 Gbps) with direct network access. **Recommended for serious load testing.**

**One-time network setup (requires sudo):**

```bash
make network-setup
```

This creates:
- Bridge `hlsbr0` (10.177.0.1/24)
- TAP device `hlstap0` with multiqueue (owned by your user)
- nftables rules for NAT

> **Important:** The network setup needs sudo, but it creates the TAP device owned by your regular user. Do NOT use sudo for the subsequent VM start commands.

**Start MicroVM with TAP networking (NO sudo):**

```bash
# Do NOT use sudo here - your user owns the TAP device
make microvm-start-tap
```

The script:
1. Verifies TAP network is configured
2. Builds the TAP-enabled VM
3. Starts it in the background
4. Polls health endpoint until ready
5. Shows connection info when ready

The VM gets its own IP address: `10.177.0.10`

**Run load test:**

```bash
./bin/go-ffmpeg-hls-swarm -clients 100 -tui \
  -origin-metrics-host 10.177.0.10 \
  http://10.177.0.10:17080/stream.m3u8
```

**Stop the MicroVM:**

```bash
make microvm-stop
```

**Teardown networking (when done):**

```bash
make network-teardown
```

**Alternative: Interactive mode (foreground):**

```bash
make microvm-origin-tap
```

> **Note:** Interactive mode runs QEMU in the foreground. To exit: press `Ctrl+A` then release, then press `X`. If the VM hangs, kill it from another terminal with: `pkill -f 'qemu.*hls-origin'`

### MicroVM Troubleshooting

**VM hangs or won't exit:**

```bash
# Kill from another terminal
pkill -f 'qemu.*hls-origin'

# Or more forcefully
pkill -9 -f 'qemu.*hls-origin'
```

**Ports in use:**

```bash
# Check what's using the ports
lsof -i :17080
lsof -i :17022

# Free ports
sudo fuser -k 17080/tcp 17022/tcp
```

**TAP networking not working:**

```bash
# Verify network setup
make network-check

# Reset and reconfigure
make network-teardown
make network-setup
```

**"Operation not permitted" on TAP device:**

This happens if you used `sudo` to start the VM, or if the TAP was created with wrong ownership.

```bash
# Check TAP ownership (should show your username or UID)
ip tuntap show
# Should show: hlstap0: tap multi_queue user <your-uid>

# If owned by root, recreate for your user
sudo make network-teardown
make network-setup

# Clean up any root-owned temp files
sudo rm -f /tmp/microvm-origin.log /tmp/microvm-origin.pid

# Start WITHOUT sudo
make microvm-start-tap
```

**Check VM logs:**

```bash
# View log file
cat /tmp/microvm-origin.log

# Tail recent output
tail -50 /tmp/microvm-origin.log
```

### Deployment Comparison

| Option | Platform | Isolation | Metrics | Performance | Setup |
|--------|----------|-----------|---------|-------------|-------|
| Local Runner | All | None | No | Good | Easiest |
| Container | Linux | Container | No | Good | Easy |
| MicroVM | Linux+KVM | Full VM | Yes | Good | Moderate |
| MicroVM+TAP | Linux+KVM | Full VM | Yes | Best (~10Gbps) | Advanced |

For quick testing, use the **Local Runner**. For production-like testing with origin metrics, use **MicroVM**.

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
