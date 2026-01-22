# Load Testing Guide

This guide shows you how to run HLS load tests using the pre-configured scripts.

## Quick Start

Run a 100-client load test with a single command:

```bash
make load-test-100
```

That's it! This single command will:
1. **Build** the `go-ffmpeg-hls-swarm` binary (if needed)
2. **Start a local HLS origin** (FFmpeg generates stream, Python serves it on port 8888)
3. **Spawn 100 FFmpeg clients** consuming the stream
4. **Display real-time progress** and final statistics
5. **Clean up automatically** when done (Ctrl+C or test completion)

> **Note:** The scripts use a lightweight local origin (FFmpeg + Python HTTP server) for quick testing. For production-like testing, use the MicroVM origin: `make microvm-origin` (see [Testing with MicroVM](#testing-with-microvm-origin) below).

### Expected Output

```
╔════════════════════════════════════════════════════════════════════════╗
║             100-Client Load Test (Standard)                            ║
╚════════════════════════════════════════════════════════════════════════╝

[INFO] Starting HLS origin on port 17088...
[OK] HLS stream ready
[OK] Origin server running at http://localhost:17088

╔════════════════════════════════════════════════════════════════════════╗
║                    HLS Load Test - 100 Clients                         ║
╠════════════════════════════════════════════════════════════════════════╣
║ Stream URL:  http://localhost:17088/stream.m3u8
║ Metrics:     http://localhost:17091/metrics
║ Duration:    30s
║ Ramp Rate:   20 clients/sec
╚════════════════════════════════════════════════════════════════════════╝

... client startup logs ...

═══════════════════════════════════════════════════════════════════
                        go-ffmpeg-hls-swarm Exit Summary
═══════════════════════════════════════════════════════════════════
Run Duration:           00:00:30
Target Clients:         100
Peak Active Clients:    100

Uptime Distribution:
  P50 (median):         00:00:25
  P95:                  00:00:29
  P99:                  00:00:30

Lifecycle:
  Total Starts:         100
  Total Restarts:       0

Exit Codes:
  137 (SIGKILL)        100
═══════════════════════════════════════════════════════════════════
```

## Available Test Levels

| Command | Clients | Ramp Rate | Duration | Use Case |
|---------|---------|-----------|----------|----------|
| `make load-test-50` | 50 | 10/sec | 30s | Quick validation |
| `make load-test-100` | 100 | 20/sec | 30s | Standard testing |
| `make load-test-300` | 300 | 50/sec | 30s | Stress testing |
| `make load-test-500` | 500 | 100/sec | 30s | Heavy load |
| `make load-test-1000` | 1000 | 100/sec | 30s | Extreme testing |

## Customizing Test Duration

The default test duration is 30 seconds. To run longer tests:

```bash
# 60 second test
make load-test-100 DURATION=60s

# 5 minute test
make load-test-300 DURATION=5m

# 10 minute stress test
make load-test-500 DURATION=10m
```

## Running Scripts Directly

For more control, run the scripts directly:

```bash
# Default settings
./scripts/100-clients/run.sh

# Custom duration (60 seconds)
./scripts/100-clients/run.sh 60s

# Custom duration and ramp rate
./scripts/100-clients/run.sh 2m 50
```

## Testing with MicroVM Origin

For production-like testing with Nginx (proper HTTP server), use the MicroVM:

```bash
# Terminal 1: Start the MicroVM origin (requires KVM)
make microvm-origin

# Terminal 2: Run load test against it
./bin/go-ffmpeg-hls-swarm \
  -clients 100 \
  -duration 60s \
  -ramp-rate 20 \
  http://localhost:17080/stream.m3u8
```

The MicroVM provides:
- **Nginx** with proper HTTP/1.1 and caching headers
- **Isolated environment** (doesn't interfere with host)
- **Production-like configuration** (worker processes, connection limits)
- **Prometheus metrics** at `http://localhost:17113/metrics`

> **Port Reference**: See [PORTS.md](PORTS.md) for all port numbers and configuration.

See [TEST_ORIGIN.md](TEST_ORIGIN.md) for MicroVM details.

---

## Testing Your Own HLS Server

To test against an external HLS origin instead of the local test server:

```bash
# Build first
make build

# Run directly against your server
./bin/go-ffmpeg-hls-swarm \
  -clients 100 \
  -duration 60s \
  -ramp-rate 20 \
  https://your-origin.com/live/stream.m3u8
```

## Understanding the Results

### Exit Summary Fields

| Field | Meaning |
|-------|---------|
| **Run Duration** | Total time the test ran |
| **Target Clients** | How many clients were requested |
| **Peak Active Clients** | Maximum concurrent clients achieved |
| **P50 (median)** | Half of clients ran at least this long |
| **P95** | 95% of clients ran at least this long |
| **P99** | 99% of clients ran at least this long |
| **Total Restarts** | How many clients crashed and restarted |

### What Good Results Look Like

- **0 restarts** = All clients stable, no crashes
- **P50 close to duration** = Clients running full test length
- **Exit code 137** = Normal shutdown (SIGKILL on test end)

### Warning Signs

- **High restart count** = Origin or network issues
- **Low P50** = Clients failing early
- **Exit codes other than 137** = Client errors

## Prometheus Metrics

During the test, metrics are available at `http://localhost:17091/metrics`:

```bash
# In another terminal during the test
curl -s http://localhost:17091/metrics | grep hls
```

Key metrics:
- `hls_swarm_clients_active` - Currently running clients
- `hls_swarm_clients_started_total` - Total clients launched
- `hls_swarm_clients_restarted_total` - Crash/restart count

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `ORIGIN_PORT` | 17088 | Port for local origin server |
| `METRICS_PORT` | 17091 | Port for Prometheus metrics |
| `MICROVM_HTTP_PORT` | 17080 | MicroVM Nginx port |
| `MICROVM_METRICS_PORT` | 17113 | MicroVM Prometheus exporter port |
| `HLS_DIR` | /tmp/hls-test | Directory for HLS segments |

Example:

```bash
# Use different ports (e.g., if 17xxx conflicts)
ORIGIN_PORT=27088 METRICS_PORT=27091 make load-test-100
```

> **Full Port Documentation**: See [PORTS.md](PORTS.md) for complete port reference.

## Troubleshooting

### "Origin not starting"

Check if port 17088 is already in use:

```bash
lsof -i :17088
```

Use a different port:

```bash
ORIGIN_PORT=27088 make load-test-100
```

### "Too many open files"

Increase file descriptor limit:

```bash
ulimit -n 8192
make load-test-500
```

### "Clients failing immediately"

Check FFmpeg is available:

```bash
ffmpeg -version
```

Check the origin is serving:

```bash
curl http://localhost:17088/stream.m3u8
```

## Script Architecture

```
scripts/
├── lib/
│   └── common.sh          # Shared functions
│       ├── start_origin   # Starts FFmpeg + HTTP server
│       ├── run_load_test  # Runs go-ffmpeg-hls-swarm
│       ├── full_test      # Combines both with cleanup
│       └── cleanup        # Kills all test processes
├── 50-clients/run.sh
├── 100-clients/run.sh
├── 300-clients/run.sh
├── 500-clients/run.sh
└── 1000-clients/run.sh
```

Each test script:
1. Sources `lib/common.sh` for shared functions
2. Sets client count, duration, and ramp rate defaults
3. Calls `full_test` which handles everything

## Next Steps

- [TEST_ORIGIN.md](TEST_ORIGIN.md) - Details on the HLS origin server
- [OBSERVABILITY.md](OBSERVABILITY.md) - Prometheus metrics and monitoring
- [CLIENT_DEPLOYMENT.md](CLIENT_DEPLOYMENT.md) - Advanced deployment options
