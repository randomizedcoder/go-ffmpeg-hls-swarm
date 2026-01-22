# Load Test Scripts

Pre-configured load test scripts for HLS origin testing.

## Quick Start

```bash
# Run a quick 300-client test (30 seconds)
make load-test-300

# Run a longer test (60 seconds)
make load-test-300 DURATION=60s

# Run an extended stress test (5 minutes)
make load-test-500 DURATION=5m
```

## Available Tests

| Make Target | Clients | Ramp Rate | Use Case |
|-------------|---------|-----------|----------|
| `load-test-50` | 50 | 10/sec | Development, quick validation |
| `load-test-100` | 100 | 20/sec | Standard testing |
| `load-test-300` | 300 | 50/sec | Stress testing |
| `load-test-500` | 500 | 100/sec | High-load testing |
| `load-test-1000` | 1000 | 100/sec | Extreme testing |

## Running Scripts Directly

```bash
# Basic usage (uses defaults)
./scripts/300-clients/run.sh

# Custom duration
./scripts/300-clients/run.sh 60s

# Custom duration and ramp rate
./scripts/300-clients/run.sh 2m 100
```

## What the Scripts Do

1. **Start local origin** - FFmpeg generates HLS stream, Python serves it
2. **Run load test** - Spawns FFmpeg clients to consume the stream
3. **Clean up** - Stops all processes on exit (Ctrl+C or completion)

## Configuration

Environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `ORIGIN_PORT` | 8888 | HTTP port for origin server |
| `HLS_DIR` | /tmp/hls-test | Directory for HLS segments |
| `METRICS_PORT` | 9091 | Prometheus metrics port |

Example:

```bash
ORIGIN_PORT=9000 make load-test-100
```

## Script Structure

```
scripts/
├── lib/
│   └── common.sh      # Shared functions
├── 50-clients/
│   └── run.sh
├── 100-clients/
│   └── run.sh
├── 300-clients/
│   └── run.sh
├── 500-clients/
│   └── run.sh
└── 1000-clients/
    └── run.sh
```

## Testing Against External Origins

To test against your own HLS server:

```bash
./go-ffmpeg-hls-swarm -clients 300 -duration 60s https://your-origin.com/stream.m3u8
```

The scripts start a local origin automatically; use the binary directly for external targets.
