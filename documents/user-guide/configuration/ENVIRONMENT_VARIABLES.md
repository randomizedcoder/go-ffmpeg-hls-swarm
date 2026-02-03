# Environment Variables

> **Type**: Configuration Reference
> **Source**: Verified against `nix/swarm-client/container.nix`

This document covers environment variables used to configure go-ffmpeg-hls-swarm containers.

---

## Container Environment Variables

When running the swarm-client container in environment variable mode, these variables configure the load test:

### Required Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `STREAM_URL` | HLS stream URL to test | `http://origin:17080/stream.m3u8` |

### Optional Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CLIENTS` | 50 | Number of concurrent clients |
| `RAMP_RATE` | 5 | Clients to start per second |
| `RAMP_JITTER` | 100 | Jitter in milliseconds (or duration: "100ms") |
| `METRICS_PORT` | 17091 | Prometheus metrics port |
| `LOG_FORMAT` | text | Log format: "json" or "text" |
| `VARIANT` | all | Variant selection: "all", "highest", "lowest", "first" |
| `TIMEOUT` | 15 | Network timeout in seconds (or duration: "15s") |

### Flag Variables (set to any value to enable)

| Variable | Effect |
|----------|--------|
| `TUI` | Enable TUI dashboard (`-tui`) |
| `RECONNECT` | Enable reconnection (`-reconnect`) |
| `NO_CACHE` | Bypass CDN cache (`-no-cache`) |
| `RESOLVE_IP` | Connect to specific IP (value used) |
| `DANGEROUS` | Enable dangerous mode (`--dangerous`) |
| `EXTRA_ARGS` | Additional CLI arguments |

---

## Usage Examples

### Basic Container Run

```bash
docker run --rm \
  -e STREAM_URL=http://origin:17080/stream.m3u8 \
  go-ffmpeg-hls-swarm:latest
```

### High-Concurrency Test

```bash
docker run --rm \
  -e STREAM_URL=http://origin:17080/stream.m3u8 \
  -e CLIENTS=300 \
  -e RAMP_RATE=50 \
  -e METRICS_PORT=17091 \
  -p 17091:17091 \
  go-ffmpeg-hls-swarm:latest
```

### With TUI Dashboard

```bash
docker run --rm -it \
  -e STREAM_URL=http://origin:17080/stream.m3u8 \
  -e TUI=1 \
  go-ffmpeg-hls-swarm:latest
```

### Cache Bypass Testing

```bash
docker run --rm \
  -e STREAM_URL=http://cdn.example.com/stream.m3u8 \
  -e CLIENTS=100 \
  -e NO_CACHE=1 \
  go-ffmpeg-hls-swarm:latest
```

### Direct IP Connection

```bash
docker run --rm \
  -e STREAM_URL=http://192.168.1.100:17080/stream.m3u8 \
  -e RESOLVE_IP=192.168.1.100 \
  -e DANGEROUS=1 \
  go-ffmpeg-hls-swarm:latest
```

### With Extra Arguments

```bash
docker run --rm \
  -e STREAM_URL=http://origin:17080/stream.m3u8 \
  -e CLIENTS=100 \
  -e EXTRA_ARGS="-duration 5m -restart-on-stall" \
  go-ffmpeg-hls-swarm:latest
```

---

## CLI Mode vs Environment Mode

The container supports two modes:

### CLI Mode (Recommended for Flexibility)

Pass arguments directly after the image name:

```bash
docker run --rm go-ffmpeg-hls-swarm:latest \
  -clients 300 \
  -ramp-rate 50 \
  -duration 5m \
  http://origin:17080/stream.m3u8
```

**Advantages:**
- Full access to all CLI flags
- Same syntax as native binary
- Easier scripting

### Environment Mode

Configure via environment variables:

```bash
docker run --rm \
  -e STREAM_URL=http://origin:17080/stream.m3u8 \
  -e CLIENTS=300 \
  go-ffmpeg-hls-swarm:latest
```

**Advantages:**
- Easier for docker-compose
- Configuration via orchestration platforms (K8s, ECS)
- Default values from container build

---

## Docker Compose Example

```yaml
version: '3.8'
services:
  swarm-client:
    image: go-ffmpeg-hls-swarm:latest
    environment:
      STREAM_URL: http://origin:17080/stream.m3u8
      CLIENTS: 100
      RAMP_RATE: 20
      METRICS_PORT: 17091
      NO_CACHE: 1
    ports:
      - "17091:17091"
    depends_on:
      - origin

  origin:
    image: go-ffmpeg-hls-swarm-test-origin:latest
    ports:
      - "17080:17080"
```

---

## Kubernetes ConfigMap Example

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: swarm-config
data:
  STREAM_URL: "http://origin:17080/stream.m3u8"
  CLIENTS: "200"
  RAMP_RATE: "50"
  METRICS_PORT: "17091"
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: swarm-client
spec:
  replicas: 1
  template:
    spec:
      containers:
      - name: swarm
        image: go-ffmpeg-hls-swarm:latest
        envFrom:
        - configMapRef:
            name: swarm-config
        ports:
        - containerPort: 17091
```

---

## Duration/Jitter Format

Some variables accept durations:

| Format | Meaning |
|--------|---------|
| `100` | 100 milliseconds (jitter) or seconds (timeout) |
| `100ms` | 100 milliseconds |
| `15s` | 15 seconds |
| `5m` | 5 minutes |

The container entrypoint automatically adds the unit suffix if a bare number is provided.

---

## Related Documents

- [CLI_REFERENCE.md](./CLI_REFERENCE.md) - Full CLI flag reference
- [PROFILES.md](./PROFILES.md) - Profile configurations
