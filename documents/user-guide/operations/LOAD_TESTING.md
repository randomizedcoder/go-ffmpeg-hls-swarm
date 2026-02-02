# Load Testing Guide

> **Type**: User Documentation

Run HLS load tests using pre-configured scripts or direct CLI commands.

---

## Quick Start

Run a 100-client load test with a single command:

```bash
make load-test-100
```

This will:
1. Build the binary (if needed)
2. Start a local HLS origin (FFmpeg + HTTP server on port 17088)
3. Spawn 100 FFmpeg clients consuming the stream
4. Display real-time progress and final statistics
5. Clean up automatically when done

---

## Available Test Levels

| Command | Clients | Ramp Rate | Duration | Use Case |
|---------|---------|-----------|----------|----------|
| `make load-test-50` | 50 | 10/sec | 30s | Quick validation |
| `make load-test-100` | 100 | 20/sec | 30s | Standard testing |
| `make load-test-300` | 300 | 50/sec | 30s | Stress testing |
| `make load-test-500` | 500 | 100/sec | 30s | Heavy load |
| `make load-test-1000` | 1000 | 100/sec | 30s | Extreme testing |

---

## Customizing Test Duration

```bash
# 60 second test
make load-test-100 DURATION=60s

# 5 minute test
make load-test-300 DURATION=5m

# 10 minute stress test
make load-test-500 DURATION=10m
```

---

## Testing with MicroVM Origin

For production-like testing with Nginx:

### Terminal 1: Start MicroVM origin

```bash
make microvm-origin
```

### Terminal 2: Run load test

```bash
make load-test-100-microvm
```

Or manually:

```bash
./bin/go-ffmpeg-hls-swarm \
  -clients 100 \
  -duration 60s \
  -ramp-rate 20 \
  http://localhost:17080/stream.m3u8
```

---

## Testing with TAP Networking (High Performance)

For maximum throughput (~10 Gbps):

### One-time setup

```bash
make network-setup
```

### Start MicroVM with TAP

```bash
make microvm-start-tap
```

### Run load test

```bash
make load-test-100-with-metrics-tap
```

Or manually:

```bash
./bin/go-ffmpeg-hls-swarm -clients 100 -duration 60s -tui \
  -origin-metrics-host 10.177.0.10 \
  http://10.177.0.10:17080/stream.m3u8
```

---

## Testing Your Own HLS Server

```bash
# Build first
make build

# Run against your server
./bin/go-ffmpeg-hls-swarm \
  -clients 100 \
  -duration 60s \
  -ramp-rate 20 \
  https://your-origin.com/live/stream.m3u8
```

### With cache bypass (hit origin directly)

```bash
./bin/go-ffmpeg-hls-swarm -clients 50 -no-cache \
  https://your-cdn.com/live/master.m3u8
```

### With specific server by IP

```bash
./bin/go-ffmpeg-hls-swarm -clients 50 -resolve 192.168.1.100 --dangerous \
  https://your-cdn.com/live/master.m3u8
```

---

## Understanding Results

### Exit Summary Fields

| Field | Meaning |
|-------|---------|
| **Run Duration** | Total time the test ran |
| **Target Clients** | Requested client count |
| **Peak Active Clients** | Maximum concurrent clients achieved |
| **P50 (median)** | Half of clients ran at least this long |
| **P95** | 95% of clients ran at least this long |
| **P99** | 99% of clients ran at least this long |
| **Total Restarts** | How many clients crashed and restarted |

### Good Results

- **0 restarts** = All clients stable
- **P50 close to duration** = Clients running full test length
- **Exit code 137** = Normal shutdown (SIGKILL on test end)

### Warning Signs

- **High restart count** = Origin or network issues
- **Low P50** = Clients failing early
- **Exit codes other than 137** = Client errors

---

## Prometheus Metrics During Test

```bash
# In another terminal
curl -s http://localhost:17091/metrics | grep hls_swarm
```

Key metrics:
- `hls_swarm_active_clients` - Currently running clients
- `hls_swarm_client_starts_total` - Total clients launched
- `hls_swarm_client_restarts_total` - Crash/restart count
- `hls_swarm_segment_requests_per_second` - Request rate

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `ORIGIN_PORT` | 17088 | Port for local origin server |
| `METRICS_PORT` | 17091 | Port for Prometheus metrics |
| `MICROVM_HTTP_PORT` | 17080 | MicroVM Nginx port |
| `HLS_DIR` | /tmp/hls-test | Directory for HLS segments |

```bash
# Use different ports
ORIGIN_PORT=27088 METRICS_PORT=27091 make load-test-100
```

---

## Troubleshooting

### Origin not starting

Check port availability:

```bash
lsof -i :17088
```

Use different port:

```bash
ORIGIN_PORT=27088 make load-test-100
```

### Too many open files

Increase limit:

```bash
ulimit -n 8192
make load-test-500
```

### Clients failing immediately

Check FFmpeg:

```bash
ffmpeg -version
```

Check origin:

```bash
curl http://localhost:17088/stream.m3u8
```

---

## Running Scripts Directly

```bash
# Default settings
./scripts/100-clients/run.sh

# Custom duration
./scripts/100-clients/run.sh 60s

# Custom duration and ramp rate
./scripts/100-clients/run.sh 2m 50
```

---

## Next Steps

| Goal | Document |
|------|----------|
| Understand metrics | [METRICS.md](../observability/METRICS.md) |
| Tune for high concurrency | [OS_TUNING.md](OS_TUNING.md) |
| Deploy test origin | [TEST_ORIGIN_SERVER.md](../../contributor-guide/testing/TEST_ORIGIN_SERVER.md) |
