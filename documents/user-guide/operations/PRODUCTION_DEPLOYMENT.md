# Production Deployment

> **Type**: Operations Guide

Checklist and best practices for deploying go-ffmpeg-hls-swarm in production load testing environments.

---

## Pre-Deployment Checklist

### System Requirements

- [ ] **CPU**: 1 core per ~100 concurrent clients (varies by content)
- [ ] **Memory**: ~50MB per 100 clients (FFmpeg processes are lightweight)
- [ ] **Network**: Sufficient bandwidth for target throughput
- [ ] **File descriptors**: At least 1024 + (clients × 5)

### OS Tuning

```bash
# Check current limits
ulimit -n

# Increase file descriptor limit
ulimit -n 65536

# Permanent: /etc/security/limits.conf
* soft nofile 65536
* hard nofile 65536
```

See [OS_TUNING.md](./OS_TUNING.md) for detailed tuning.

### Network Configuration

```bash
# Increase connection tracking (if using conntrack)
sysctl -w net.netfilter.nf_conntrack_max=262144

# Increase local port range
sysctl -w net.ipv4.ip_local_port_range="1024 65535"

# Enable TIME_WAIT reuse
sysctl -w net.ipv4.tcp_tw_reuse=1
```

---

## Deployment Options

### Option 1: Native Binary (Recommended for Performance)

```bash
# Build optimized binary
make build

# Run with tuned settings
./bin/go-ffmpeg-hls-swarm \
  -clients 500 \
  -ramp-rate 50 \
  -duration 1h \
  -metrics 0.0.0.0:17091 \
  -origin-metrics-host 10.177.0.10 \
  http://origin:17080/stream.m3u8
```

**Advantages:**
- Best performance
- Direct access to system resources
- Easier debugging

### Option 2: Container (Recommended for Isolation)

```bash
# Build container
nix build .#swarm-client-container
docker load < ./result

# Run
docker run --rm \
  --network host \
  -e STREAM_URL=http://origin:17080/stream.m3u8 \
  -e CLIENTS=500 \
  go-ffmpeg-hls-swarm:latest
```

**Advantages:**
- Isolated environment
- Reproducible deployments
- Easy orchestration

### Option 3: Kubernetes

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: hls-load-test
spec:
  replicas: 3  # Distribute load across nodes
  template:
    spec:
      containers:
      - name: swarm
        image: go-ffmpeg-hls-swarm:latest
        args:
          - "-clients"
          - "200"
          - "-ramp-rate"
          - "50"
          - "http://origin:17080/stream.m3u8"
        resources:
          requests:
            cpu: "2"
            memory: "1Gi"
          limits:
            cpu: "4"
            memory: "2Gi"
        ports:
        - containerPort: 17091
```

---

## Monitoring Setup

### Prometheus Scraping

```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'hls-swarm'
    static_configs:
      - targets: ['swarm-host:17091']
    scrape_interval: 5s

  - job_name: 'hls-origin'
    static_configs:
      - targets: ['origin:9100', 'origin:9113']
```

### Key Metrics to Monitor

| Metric | Healthy Value | Alert Threshold |
|--------|---------------|-----------------|
| `hls_swarm_active_clients` | Target count | < Target × 0.9 |
| `hls_swarm_stalled_clients` | 0 | > 0 for 30s |
| `hls_swarm_error_rate` | 0 | > 0.01 (1%) |
| `hls_swarm_inferred_latency_p95_seconds` | < 0.5s | > 1s |
| `hls_swarm_stats_drop_rate` | 0 | > 0.01 |

### Grafana Dashboard

Import the metrics and create panels for:
1. Active clients over time
2. Throughput (MB/s)
3. Latency percentiles
4. Error rate
5. Origin metrics (CPU, network)

---

## Scaling Guidelines

### Single-Host Limits

| Clients | CPU Cores | Memory | Notes |
|---------|-----------|--------|-------|
| 100 | 2 | 512MB | Smoke test |
| 300 | 4 | 1GB | Standard load |
| 500 | 8 | 2GB | Stress test |
| 1000 | 16 | 4GB | Extreme (requires tuning) |

### Multi-Host Scaling

For > 1000 clients, distribute across multiple hosts:

```bash
# Host 1: Clients 1-300
./bin/go-ffmpeg-hls-swarm -clients 300 ...

# Host 2: Clients 301-600
./bin/go-ffmpeg-hls-swarm -clients 300 ...

# Host 3: Clients 601-900
./bin/go-ffmpeg-hls-swarm -clients 300 ...
```

Aggregate metrics via Prometheus federation.

---

## Test Execution

### Pre-Test Validation

```bash
# 1. Verify stream accessibility
curl -I http://origin:17080/stream.m3u8

# 2. Validate configuration
go-ffmpeg-hls-swarm --check http://origin:17080/stream.m3u8

# 3. Quick smoke test
go-ffmpeg-hls-swarm -clients 5 -duration 30s http://origin:17080/stream.m3u8
```

### Ramp-Up Strategy

Start conservative and increase:

1. **Baseline**: 50 clients, 5/sec ramp
2. **Standard**: 100 clients, 10/sec ramp
3. **Stress**: 300 clients, 30/sec ramp
4. **Break**: Increase until failures

```bash
# Progressive load test
for clients in 50 100 200 300 400 500; do
  echo "Testing with $clients clients..."
  ./bin/go-ffmpeg-hls-swarm \
    -clients $clients \
    -duration 5m \
    -tui=false \
    http://origin:17080/stream.m3u8
  sleep 30  # Cool-down between tests
done
```

### Long-Running Tests

For endurance testing:

```bash
./bin/go-ffmpeg-hls-swarm \
  -clients 200 \
  -duration 24h \
  -restart-on-stall \
  -metrics 0.0.0.0:17091 \
  http://origin:17080/stream.m3u8
```

Monitor for:
- Memory growth (potential leaks)
- Gradual latency increase
- Periodic failures (time-based issues)

---

## Troubleshooting

### Clients Not Reaching Target

1. Check file descriptor limits
2. Verify network capacity
3. Check origin server capacity
4. Review ramp rate (too fast?)

### High Error Rate

1. Check stream URL accessibility
2. Verify origin server health
3. Check for network issues
4. Review timeout settings

### Metrics Not Appearing

1. Check metrics port is exposed
2. Verify Prometheus scraping
3. Check firewall rules
4. Verify `-stats` is enabled

### Performance Degradation

1. Check CPU usage (top)
2. Check memory (free -h)
3. Check network (iftop)
4. Review stats drop rate

---

## Post-Test Analysis

### Collect Results

```bash
# Export metrics snapshot
curl http://localhost:17091/metrics > results.prom

# Generate summary
./bin/go-ffmpeg-hls-swarm ... | tee test-log.txt
```

### Key Questions

1. Did we reach target client count?
2. What was the sustained throughput?
3. Were there any stalls or errors?
4. What were the latency percentiles?
5. How did the origin perform?

### Success Criteria Example

| Metric | Requirement |
|--------|-------------|
| Active clients | 100% of target |
| Stalled clients | 0 |
| Error rate | < 0.1% |
| P95 latency | < 500ms |
| Throughput | ≥ Expected |

---

## Security Considerations

### Network Isolation

Run load tests in isolated networks to prevent:
- Accidental production impact
- Interference with other tests
- Security boundary violations

### TLS Testing

For TLS endpoints:
```bash
# Normal TLS verification
go-ffmpeg-hls-swarm -clients 100 https://cdn.example.com/stream.m3u8

# Skip verification (test only!)
go-ffmpeg-hls-swarm -clients 100 -resolve 1.2.3.4 --dangerous \
  https://cdn.example.com/stream.m3u8
```

### Credential Management

Never embed credentials in commands. Use:
- Environment variables
- Kubernetes secrets
- Vault integration

---

## Related Documents

- [OS_TUNING.md](./OS_TUNING.md) - System tuning
- [TROUBLESHOOTING.md](./TROUBLESHOOTING.md) - Problem resolution
- [METRICS.md](../observability/METRICS.md) - Metrics guide
