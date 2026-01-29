# OS Tuning Guide

> **Type**: User Documentation

Resource management and OS configuration for running go-ffmpeg-hls-swarm at scale.

---

## Preflight Checks

The tool automatically checks system limits at startup:

**Success:**
```
Preflight checks:
  ✓ file_descriptors: 8192 available (need 2100 for 100 clients)
  ✓ process_limit: 4096 available (need 150)
  ✓ ffmpeg: found at /usr/bin/ffmpeg
Starting orchestrator...
```

**Failure:**
```
Preflight checks:
  ✗ file_descriptors: 1024 available (need 2100 for 100 clients)
    Fix: ulimit -n 8192 (or edit /etc/security/limits.conf)

ERROR: Preflight checks failed. Fix issues above or use --skip-preflight to override.
```

---

## Resource Checklist

| Resource | Recommendation | How to Set | Why It Matters |
|----------|---------------|------------|----------------|
| File descriptors | 8192+ | `ulimit -n 8192` | FFmpeg opens 10-20 FDs each |
| Process limit | 500+ | `ulimit -u 4096` | Each client = 1 process |
| Ephemeral ports | Monitor if NATed | `sysctl net.ipv4.ip_local_port_range` | Each connection needs a port |
| TCP TIME_WAIT | Reduce if recycling | `sysctl net.ipv4.tcp_tw_reuse=1` | Speeds up port recycling |

---

## Quick Fix (Current Session)

```bash
# Increase file descriptor limit
ulimit -n 8192

# Verify
ulimit -n
```

---

## Permanent Configuration

### Option 1: limits.conf

Edit `/etc/security/limits.conf`:

```bash
# /etc/security/limits.conf
your-user soft nofile 8192
your-user hard nofile 16384
your-user soft nproc  4096
your-user hard nproc  8192
```

Log out and back in for changes to take effect.

### Option 2: systemd Service

```ini
# /etc/systemd/system/go-ffmpeg-hls-swarm.service
[Unit]
Description=HLS Load Testing
After=network.target

[Service]
Type=simple
User=your-user
ExecStart=/path/to/go-ffmpeg-hls-swarm -clients 100 ...
LimitNOFILE=8192
LimitNPROC=4096
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

---

## Memory Considerations

### Per-Client Memory

Each FFmpeg process typically uses:
- ~20-50 MB RSS for HLS streaming
- More with `-variant all` (buffers for each variant)

### Orchestrator Overhead

- ~50 MB base
- ~1 KB per client for supervisor state
- Stderr buffers: configurable via `-stats-buffer`

### Estimates by Client Count

| Clients | FFmpeg Memory | Orchestrator | Total |
|---------|--------------|--------------|-------|
| 50 | ~1-2.5 GB | ~60 MB | ~1-2.5 GB |
| 100 | ~2-5 GB | ~70 MB | ~2-5 GB |
| 250 | ~5-12 GB | ~100 MB | ~5-12 GB |
| 500 | ~10-25 GB | ~150 MB | ~10-25 GB |

---

## DNS Resolution

**Problem**: 200+ clients hammer your DNS resolver during ramp-up.

**Symptoms:**
- Slow ramp-up
- "Name or service not known" errors
- Resolver becomes unresponsive

### Solution 1: Use -resolve (Recommended)

Bypass DNS entirely:

```bash
go-ffmpeg-hls-swarm -resolve 203.0.113.50 --dangerous \
  -clients 200 https://cdn.example.com/live/master.m3u8
```

### Solution 2: Local DNS Cache

Install nscd:

```bash
# Debian/Ubuntu
sudo apt install nscd
sudo systemctl enable nscd
```

### Solution 3: /etc/hosts

```bash
# /etc/hosts
203.0.113.50  cdn.example.com
```

---

## Network Tuning

### Ephemeral Port Range

Check current range:

```bash
cat /proc/sys/net/ipv4/ip_local_port_range
```

Expand if needed:

```bash
sudo sysctl -w net.ipv4.ip_local_port_range="1024 65535"
```

### TIME_WAIT Recycling

Enable faster port recycling:

```bash
sudo sysctl -w net.ipv4.tcp_tw_reuse=1
```

Make permanent in `/etc/sysctl.conf`:

```bash
net.ipv4.ip_local_port_range = 1024 65535
net.ipv4.tcp_tw_reuse = 1
```

---

## Debugging Issues

### FD Exhaustion

```bash
# Count FDs for orchestrator
ls /proc/$(pgrep go-ffmpeg-hls-swarm)/fd | wc -l

# Count for all ffmpeg processes
for pid in $(pgrep ffmpeg); do
  echo "PID $pid: $(ls /proc/$pid/fd 2>/dev/null | wc -l) FDs"
done

# System-wide usage
cat /proc/sys/fs/file-nr
```

### Port Exhaustion

```bash
# Check ephemeral ports
cat /proc/sys/net/ipv4/ip_local_port_range

# Check TIME_WAIT connections
ss -s | grep TIME-WAIT

# Socket stats
ss -ant | awk '{print $1}' | sort | uniq -c | sort -rn
```

### Memory Usage

```bash
# Per-process memory
ps aux | grep -E 'ffmpeg|go-ffmpeg' | awk '{sum+=$6} END {print sum/1024 " MB"}'

# Detailed memory map
pmap $(pgrep go-ffmpeg-hls-swarm) | tail -1
```

---

## Common Issues

| Symptom | Likely Cause | Verification | Fix |
|---------|--------------|--------------|-----|
| "connection reset" | FD exhaustion | `ls /proc/<pid>/fd \| wc -l` | `ulimit -n 8192` |
| "Cannot fork" | Process limit | `ulimit -u` | `ulimit -u 4096` |
| Slow startup | Port exhaustion | `ss -s` | Enable tcp_tw_reuse |
| OOM killed | Memory exhaustion | `dmesg \| grep -i oom` | Reduce clients |
| DNS errors | Resolver overload | Check resolver logs | Use -resolve flag |

---

## Recommended Settings for Scale

### 100 Clients

```bash
ulimit -n 4096
```

### 500 Clients

```bash
ulimit -n 16384
sudo sysctl -w net.ipv4.tcp_tw_reuse=1
```

### 1000+ Clients

```bash
ulimit -n 32768
ulimit -u 8192
sudo sysctl -w net.ipv4.ip_local_port_range="1024 65535"
sudo sysctl -w net.ipv4.tcp_tw_reuse=1
```

---

## Next Steps

| Goal | Document |
|------|----------|
| Run load tests | [LOAD_TESTING.md](LOAD_TESTING.md) |
| Troubleshoot issues | [TROUBLESHOOTING.md](TROUBLESHOOTING.md) |
| Understand metrics | [METRICS.md](../observability/METRICS.md) |
