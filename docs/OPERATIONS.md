# Operations Guide

> **Type**: User Documentation
> **Related**: [QUICKSTART.md](QUICKSTART.md), [CONFIGURATION.md](CONFIGURATION.md)

This guide covers resource management, preflight checks, OS tuning, and failure mode handling for running `go-ffmpeg-hls-swarm` at scale.

**Prerequisites**: You should be comfortable with the basics from [QUICKSTART.md](QUICKSTART.md) before diving into operational concerns.

---

## Table of Contents

- [1. Preflight Checks](#1-preflight-checks)
  - [1.1 Startup Guardrails](#11-startup-guardrails)
  - [1.2 Implementation](#12-implementation)
- [2. OS Tuning](#2-os-tuning)
  - [2.1 Checklist](#21-checklist)
  - [2.2 Memory Considerations](#22-memory-considerations)
- [3. Common Issues](#3-common-issues)
  - [3.1 Mysterious Flakiness Causes](#31-mysterious-flakiness-causes)
- [4. Failure Modes & Mitigations](#4-failure-modes--mitigations)
- [5. Finding the Limit](#5-finding-the-limit)
- [6. Segment Buffering Limitations](#6-segment-buffering-limitations)

---

## 1. Preflight Checks

### 1.1 Startup Guardrails

**Refuse to start** if system limits are insufficient:

**Startup output (success):**
```
Preflight checks:
  ✓ file_descriptors: 8192 available (need 2100 for 100 clients)
  ✓ process_limit: 4096 available (need 150)
  ✓ ffmpeg: found at /usr/bin/ffmpeg (version 6.1)
Starting orchestrator...
```

**On failure:**
```
Preflight checks:
  ✗ file_descriptors: 1024 available (need 2100 for 100 clients)
    Fix: ulimit -n 8192 (or edit /etc/security/limits.conf)
  ✓ process_limit: 4096 available (need 150)

ERROR: Preflight checks failed. Fix issues above or use --skip-preflight to override.
```

### 1.2 Implementation

```go
// internal/preflight/checks.go

type PreflightCheck struct {
    Name     string
    Required int
    Actual   int
    Passed   bool
    Warning  bool   // Non-fatal but noteworthy
    Message  string // Additional context
}

func RunPreflightChecks(targetClients int) ([]PreflightCheck, error) {
    checks := []PreflightCheck{}

    // File descriptor check
    // Each FFmpeg needs ~10-20 FDs (sockets, files, pipes)
    // Plus our overhead (metrics server, logging, etc.)
    requiredFDs := targetClients*20 + 100
    actualFDs := getUlimitNofile()
    checks = append(checks, PreflightCheck{
        Name:     "file_descriptors",
        Required: requiredFDs,
        Actual:   actualFDs,
        Passed:   actualFDs >= requiredFDs,
    })

    // Process limit check
    requiredProcs := targetClients + 50  // FFmpeg + our processes
    actualProcs := getUlimitNproc()
    checks = append(checks, PreflightCheck{
        Name:     "process_limit",
        Required: requiredProcs,
        Actual:   actualProcs,
        Passed:   actualProcs >= requiredProcs,
    })

    // Ephemeral port range check (warning only)
    portRangeSize := getEphemeralPortRangeSize()
    // Each client with restarts can consume many ports in TIME_WAIT
    // Rule of thumb: 4 ports per client with 60s TIME_WAIT
    recommendedPorts := targetClients * 4
    checks = append(checks, PreflightCheck{
        Name:     "ephemeral_ports",
        Required: recommendedPorts,
        Actual:   portRangeSize,
        Passed:   true, // Don't fail on this
        Warning:  portRangeSize < recommendedPorts,
        Message:  "High restart rates may exhaust ephemeral ports",
    })

    // Check for failures
    for _, c := range checks {
        if !c.Passed {
            return checks, fmt.Errorf("preflight check failed: %s (need %d, have %d)",
                c.Name, c.Required, c.Actual)
        }
    }

    return checks, nil
}

func getUlimitNofile() int {
    var limit syscall.Rlimit
    syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit)
    return int(limit.Cur)
}

func getUlimitNproc() int {
    var limit syscall.Rlimit
    syscall.Getrlimit(syscall.RLIMIT_NPROC, &limit)
    return int(limit.Cur)
}

func getEphemeralPortRangeSize() int {
    // Read /proc/sys/net/ipv4/ip_local_port_range
    // Format: "32768\t60999"
    data, err := os.ReadFile("/proc/sys/net/ipv4/ip_local_port_range")
    if err != nil {
        return 28231 // Default range size
    }
    var low, high int
    fmt.Sscanf(string(data), "%d %d", &low, &high)
    return high - low
}
```

**Startup output with warnings:**
```
Preflight checks:
  ✓ file_descriptors: 8192 available (need 2100 for 100 clients)
  ✓ process_limit: 4096 available (need 150)
  ⚠ ephemeral_ports: 28231 available (recommend 400 for 100 clients)
    Note: High restart rates may exhaust ephemeral ports
  ✓ ffmpeg: found at /usr/bin/ffmpeg (version 6.1)
Starting orchestrator...
```

---

## 2. OS Tuning

### 2.1 Checklist

| Resource | Recommendation | How to Set | Why It Matters |
|----------|---------------|------------|----------------|
| File descriptors | 8192+ | `ulimit -n 8192` | FFmpeg opens 10-20 FDs each (sockets, segments) |
| Process limit | 500+ | `ulimit -u` | Each client = 1 process (sometimes more with helpers) |
| Ephemeral ports | Monitor if NATed | `sysctl net.ipv4.ip_local_port_range` | Each connection needs a port |
| TCP TIME_WAIT | Reduce if recycling ports | `sysctl net.ipv4.tcp_tw_reuse=1` | Speeds up port recycling |

#### Permanent Configuration

For permanent changes, edit `/etc/security/limits.conf`:

```bash
# /etc/security/limits.conf
your-user soft nofile 8192
your-user hard nofile 16384
your-user soft nproc  4096
your-user hard nproc  8192
```

Or use systemd service limits:

```ini
# /etc/systemd/system/go-ffmpeg-hls-swarm.service
[Service]
LimitNOFILE=8192
LimitNPROC=4096
```

### 2.2 Memory Considerations

Each FFmpeg process typically uses:
- ~20-50 MB RSS for HLS streaming
- Varies with segment size and buffering
- More with `-variant all` (buffers for each variant)

**Orchestrator overhead:**
- ~50 MB base
- ~1 KB per client for supervisor state
- Stderr buffers: `MaxBufferedLines × MaxLineLength × clients` (see [OBSERVABILITY.md](OBSERVABILITY.md))

**Estimates by client count:**

| Clients | FFmpeg Memory | Orchestrator | Total |
|---------|--------------|--------------|-------|
| 50 | ~1-2.5 GB | ~60 MB | ~1-2.5 GB |
| 100 | ~2-5 GB | ~70 MB | ~2-5 GB |
| 250 | ~5-12 GB | ~100 MB | ~5-12 GB |

### 2.3 DNS Resolution

**Problem**: When not using `-resolve`, 200+ clients will hammer your local DNS resolver during ramp-up. Each FFmpeg process resolves the hostname independently.

**Symptoms:**
- Slow ramp-up (DNS becomes bottleneck)
- Intermittent "Name or service not known" errors
- Your recursive resolver becomes unresponsive

**Solutions:**

#### Option 1: Use `-resolve` (Recommended for Load Testing)

Bypass DNS entirely by specifying the target IP:

```bash
go-ffmpeg-hls-swarm -resolve 203.0.113.50 --dangerous \
  -clients 200 https://cdn.example.com/live/master.m3u8
```

This is ideal for load testing because:
- No DNS overhead
- Consistent target (no CDN load balancing)
- Faster ramp-up

#### Option 2: Enable Local DNS Caching

Install and configure `nscd` (Name Service Cache Daemon):

```bash
# Debian/Ubuntu
sudo apt install nscd
sudo systemctl enable nscd

# Verify caching is working
nscd -g | grep "hosts cache"
```

Or use `systemd-resolved` with caching:

```bash
# /etc/systemd/resolved.conf
[Resolve]
Cache=yes
CacheFromLocalhost=yes
```

#### Option 3: Use /etc/hosts (Simple)

For testing specific servers, add entries directly:

```bash
# /etc/hosts
203.0.113.50  cdn.example.com
```

**Future Enhancement**: go-ffmpeg-hls-swarm may implement an internal DNS cache to resolve once at startup and reuse for all clients. This would avoid DDoSing your own resolver during ramp-up.

---

## 3. Common Issues

### 3.1 Mysterious Flakiness Causes

| Symptom | Likely Cause | How to Verify |
|---------|--------------|---------------|
| Random "connection reset" | FD exhaustion | `ls /proc/<pid>/fd \| wc -l` |
| "Cannot fork" errors | Process limit | `ulimit -u` |
| Slow startup, then failures | Ephemeral port exhaustion | `ss -s` |
| Increasing memory, then OOM | Unbounded stderr buffering | Monitor RSS over time |
| CPU spikes on verbose mode | Log processing overhead | Profile or reduce `-loglevel` |

#### Debugging FD Exhaustion

```bash
# Count FDs for the orchestrator
ls /proc/$(pgrep go-ffmpeg-hls-swarm)/fd | wc -l

# Count FDs for all ffmpeg processes
for pid in $(pgrep ffmpeg); do
  echo "PID $pid: $(ls /proc/$pid/fd 2>/dev/null | wc -l) FDs"
done

# System-wide FD usage
cat /proc/sys/fs/file-nr
# Format: allocated  free  max
```

#### Debugging Port Exhaustion

```bash
# Check ephemeral port range
cat /proc/sys/net/ipv4/ip_local_port_range

# Check TIME_WAIT connections
ss -s | grep TIME-WAIT

# Detailed socket stats
ss -ant | awk '{print $1}' | sort | uniq -c | sort -rn
```

---

## 4. Failure Modes & Mitigations

| Failure | Impact | Mitigation |
|---------|--------|------------|
| CDN/origin outage | Mass process exits | Exponential backoff + per-client jitter prevents restart storm |
| Process hangs | Client appears running but stuck | `-rw_timeout` flag; Phase 2: liveness watchdog |
| All clients fail | Load test ineffective | Log clearly; keep trying (goal: find limits) |
| Metrics endpoint slow | Scrape timeouts | Aggregate metrics; no high-cardinality labels |
| Orchestrator OOM | Process killed | Bounded stderr buffers; monitor memory |
| FD exhaustion | Random "connection reset" | Preflight checks; refuse to start if ulimit too low |

---

## 5. Finding the Limit

When load testing to find CDN capacity:

- **Expect some clients to fail** as limit approached
- **Keep running** with remaining clients — don't abort on partial failure
- Log failure rate for analysis
- Metric `hlsswarm_clients_active` shows effective concurrency
- Exit summary shows median active clients (not just target)

### Interpreting Results

```
Target: 200 clients
Active: 150 clients (steady)
Restart rate: 10/min
```

This means the infrastructure can sustain ~150 concurrent connections. The 50 failing clients are finding the breaking point.

### Gradual Ramp Strategy

To find exact limits:

```bash
# Start with a known-good number
go-ffmpeg-hls-swarm -clients 50 -ramp-rate 5 -duration 5m ...

# If stable, increase
go-ffmpeg-hls-swarm -clients 100 -ramp-rate 5 -duration 5m ...

# Continue until you see significant failure rates
go-ffmpeg-hls-swarm -clients 200 -ramp-rate 5 -duration 5m ...
```

---

## 6. Segment Buffering Limitations

**Q: Can I control how many HLS segments are buffered?**

**A: Not really.** FFmpeg's HLS demuxer doesn't have a "buffer N segments ahead" concept like a video player.

### How FFmpeg HLS Demuxer Works

1. Fetches the playlist (`.m3u8`)
2. Identifies available segments
3. Downloads segments **as fast as the network allows**
4. Passes data to the output (in our case, discarded via `-f null`)

There's no pacing, no buffer limit, no "download one segment at a time" mode.

### Available Options (Limited Usefulness)

| Option | What It Does | Load Testing Relevance |
|--------|--------------|----------------------|
| `-live_start_index N` | Start N segments from live edge (default: -3) | Controls starting position only |
| `-probesize N` | Bytes to read for stream analysis | Affects startup, not ongoing fetch |
| `-analyzeduration N` | Microseconds to analyze | Affects startup, not ongoing fetch |

### Why This Doesn't Matter for Load Testing

For stress testing CDN/origin infrastructure, you typically **want** maximum throughput:
- Fetch segments as fast as possible
- Saturate available bandwidth
- Find the infrastructure breaking point

If you need to simulate realistic player behavior (one segment every N seconds), you'd need:
- A custom HLS client (not FFmpeg)
- Rate limiting at the process level (e.g., `tc`, traffic shaping)
- A different tool entirely

### Workaround: External Rate Limiting

If you must limit bandwidth per client:

```bash
# Using trickle (per-process bandwidth limiter)
trickle -d 1000 ffmpeg -i https://... -f null -  # Limit to ~1 Mbps download

# Using tc (Linux traffic control) - complex, system-wide
# Using cgroups v2 network bandwidth limits
```

These are **out of scope** for this tool. Use external tooling if needed.
