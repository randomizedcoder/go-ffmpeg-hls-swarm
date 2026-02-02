# Troubleshooting Guide

> **Type**: User Documentation

Common issues and solutions for go-ffmpeg-hls-swarm.

---

## Quick Diagnosis

| Symptom | Likely Cause | Quick Fix |
|---------|--------------|-----------|
| "too many open files" | FD limit too low | `ulimit -n 8192` |
| FFmpeg exits immediately | Bad URL or FFmpeg issue | Test with `ffmpeg -i <URL> -t 5 -f null -` |
| No metrics | Port in use | Check `lsof -i :17091` |
| High restart rate | Origin overloaded | Reduce clients |
| Slow ramp-up | DNS overhead | Use `-resolve` flag |
| Memory growing | Too many clients | Reduce clients |

---

## Startup Issues

### "too many open files"

**Cause**: File descriptor limit too low for requested clients.

**Fix**:

```bash
# Temporary (current session)
ulimit -n 8192

# Permanent (add to /etc/security/limits.conf)
your-user soft nofile 8192
your-user hard nofile 16384
```

### "Cannot find ffmpeg"

**Cause**: FFmpeg not in PATH.

**Fix**:

```bash
# Check FFmpeg location
which ffmpeg

# Use explicit path
go-ffmpeg-hls-swarm -ffmpeg /usr/local/bin/ffmpeg ...
```

### "Preflight checks failed"

**Cause**: System resources insufficient.

**Options**:

1. Fix the underlying issue (see preflight output)
2. Skip checks: `--skip-preflight` (not recommended)

---

## Connection Issues

### FFmpeg exits immediately

**Cause**: Stream URL inaccessible or malformed.

**Diagnosis**:

```bash
# Test URL accessibility
curl -I https://your-url/master.m3u8

# Test FFmpeg directly
ffmpeg -i https://your-url/master.m3u8 -t 5 -f null -
```

**Common causes**:
- 404: URL doesn't exist
- 403: Access denied (auth required)
- Network issues: DNS, firewall, etc.

### "Connection refused"

**Cause**: Origin server not running or wrong port.

**Diagnosis**:

```bash
# Check if origin is running
curl http://localhost:17080/health

# Check port binding
lsof -i :17080
```

### Slow startup / DNS errors

**Cause**: DNS resolver overwhelmed by concurrent lookups.

**Fix**: Use IP directly:

```bash
go-ffmpeg-hls-swarm -resolve 10.0.0.50 --dangerous \
  https://cdn.example.com/live/master.m3u8
```

---

## Runtime Issues

### High restart rate

**Symptoms**: Many restarts, low P50 uptime.

**Causes**:
1. Origin overloaded
2. Network instability
3. Stream ended (VOD)

**Diagnosis**:

```bash
# Check verbose output
go-ffmpeg-hls-swarm -v -clients 5 ...

# Check origin logs
curl http://localhost:17080/health
```

**Fixes**:
- Reduce client count
- Check origin server capacity
- Increase `-reconnect-delay`

### Clients stalling

**Symptoms**: Active clients but no progress, high drift.

**Causes**:
1. Origin too slow
2. Network congestion
3. Insufficient bandwidth

**Diagnosis**:

```bash
# Check speed metrics
curl -s http://localhost:17091/metrics | grep hls_swarm_average_speed
```

**Fixes**:
- Reduce client count
- Use `-variant lowest` for less bandwidth
- Check origin server performance

### Memory growing

**Cause**: Too many clients for available memory.

**Diagnosis**:

```bash
# Check memory usage
ps aux | grep -E 'ffmpeg|go-ffmpeg' | awk '{sum+=$6} END {print sum/1024 " MB"}'
```

**Fixes**:
- Reduce client count
- Reduce `-stats-buffer` size
- Add more RAM

---

## Metrics Issues

### No metrics at localhost:17091

**Causes**:
1. Port already in use
2. Metrics server failed to start

**Diagnosis**:

```bash
# Check port usage
lsof -i :17091
```

**Fix**: Use different port:

```bash
go-ffmpeg-hls-swarm -metrics 0.0.0.0:9091 ...
```

### Metrics show 0 values

**Cause**: Stats collection disabled or no activity.

**Check**:
- Ensure `-stats` is true (default)
- Verify clients are actually running
- Check for errors in output

---

## Test Origin Issues

### Origin not starting

**Diagnosis**:

```bash
# Check port availability
lsof -i :17080
lsof -i :17088

# Check FFmpeg
ffmpeg -version
```

**Fix**: Use different port:

```bash
ORIGIN_PORT=27088 make test-origin
```

### MicroVM won't start

**Causes**:
1. KVM not available
2. Ports in use

**Diagnosis**:

```bash
# Check KVM
ls -la /dev/kvm
make microvm-check-kvm

# Check ports
make microvm-check-ports
```

**Fix**:
- Enable KVM: `sudo modprobe kvm_intel` (or `kvm_amd`)
- Kill conflicting processes

---

## Performance Issues

### Slow ramp-up

**Causes**:
1. DNS overhead
2. System resource limits
3. Origin can't handle rate

**Fixes**:
- Use `-resolve` to bypass DNS
- Increase file descriptor limits
- Reduce `-ramp-rate`

### Lower than expected throughput

**Diagnosis**:

```bash
# Check throughput metric
curl -s http://localhost:17091/metrics | grep throughput
```

**Causes**:
1. Origin bandwidth limited
2. Network congestion
3. Client-side CPU/memory constraints

**Fixes**:
- Use TAP networking for MicroVMs
- Check origin server capacity
- Verify network path

---

## Debug Mode

For detailed debugging:

```bash
# Verbose output
go-ffmpeg-hls-swarm -v -clients 5 ...

# FFmpeg debug logging
go-ffmpeg-hls-swarm -ffmpeg-debug -clients 5 ...

# Print FFmpeg command without running
go-ffmpeg-hls-swarm --print-cmd ...

# Validation run
go-ffmpeg-hls-swarm --check ...
```

---

## Getting Help

1. Check this troubleshooting guide
2. Review the [CLI Reference](../configuration/CLI_REFERENCE.md)
3. Check [OS Tuning](OS_TUNING.md) for system configuration
4. Review metrics in [METRICS.md](../observability/METRICS.md)
5. Open an issue at https://github.com/randomizedcoder/go-ffmpeg-hls-swarm/issues

When reporting issues, include:
- Command used
- Error message
- System info (`uname -a`, `ulimit -a`)
- FFmpeg version (`ffmpeg -version`)
