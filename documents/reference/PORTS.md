# Port Reference

> **Type**: Reference Documentation

Complete reference for all ports used by go-ffmpeg-hls-swarm.

---

## Quick Reference

| Port | Service | Component |
|------|---------|-----------|
| 17080 | Nginx HLS stream | MicroVM origin |
| 17088 | Local origin | Script-based origin |
| 17091 | Prometheus metrics | Swarm client |
| 9100 | node_exporter | MicroVM (origin metrics) |
| 9113 | nginx_exporter | MicroVM (nginx metrics) |
| 17022 | SSH | MicroVM |

---

## Swarm Client Ports

| Port | Default | Flag | Description |
|------|---------|------|-------------|
| 17091 | Yes | `-metrics` | Prometheus metrics endpoint |

Change with:

```bash
go-ffmpeg-hls-swarm -metrics 0.0.0.0:9090 ...
```

---

## Test Origin Ports

### Local Runner (Scripts)

| Port | Variable | Default | Description |
|------|----------|---------|-------------|
| 17088 | `ORIGIN_PORT` | 17088 | HTTP server for HLS stream |

Change with:

```bash
ORIGIN_PORT=27088 make test-origin
```

### MicroVM

| Port | Host | VM | Description |
|------|------|----|----|
| 17080 | 17080 | 17080 | Nginx HLS stream |
| 17100 | 17100 | 9100 | node_exporter (forwarded) |
| 17113 | 17113 | 9113 | nginx_exporter (forwarded) |
| 17022 | 17022 | 22 | SSH |

### TAP Networking (MicroVM)

With TAP networking, the VM has its own IP (default: 10.177.0.10):

| Service | URL |
|---------|-----|
| HLS stream | http://10.177.0.10:17080/stream.m3u8 |
| node_exporter | http://10.177.0.10:9100/metrics |
| nginx_exporter | http://10.177.0.10:9113/metrics |

---

## Container Ports

### Test Origin Container

| Internal | External | Description |
|----------|----------|-------------|
| 17080 | 17080 | Nginx HLS stream |

### Swarm Client Container

| Internal | External | Description |
|----------|----------|-------------|
| 17091 | 17091 | Prometheus metrics |

---

## Port Conflicts

If default ports conflict, use environment variables or flags:

```bash
# Different origin port
ORIGIN_PORT=27088 make test-origin

# Different metrics port
go-ffmpeg-hls-swarm -metrics 0.0.0.0:27091 ...

# Check port usage
lsof -i :17080
lsof -i :17091
```

---

## Firewall Rules

For remote access to metrics during load tests:

```bash
# Allow metrics port
sudo ufw allow 17091/tcp

# Allow origin port
sudo ufw allow 17080/tcp
```

---

## Port Allocation Conventions

The 17xxx port range is used to avoid conflicts with common services:

| Range | Purpose |
|-------|---------|
| 17000-17099 | HTTP services (origin, stream) |
| 17100-17199 | Monitoring (exporters) |
| 17000-17099 | Management (SSH, etc.) |

Standard ports (9090, 9100, 9113) are used inside MicroVMs to maintain compatibility with standard Prometheus configurations.
