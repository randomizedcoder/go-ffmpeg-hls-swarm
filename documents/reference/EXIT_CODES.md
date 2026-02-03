# Exit Codes Reference

> **Type**: Reference Documentation
> **Source**: Verified against `cmd/go-ffmpeg-hls-swarm/main.go` and `internal/metrics/collector.go`

---

## go-ffmpeg-hls-swarm Exit Codes

| Code | Meaning | When |
|------|---------|------|
| 0 | Success | Normal completion, `--version`, `--print-cmd` |
| 1 | Error | Configuration error, validation error, orchestrator failure |

### Exit Code 0 Cases

- Test completed successfully within duration
- User interrupted with Ctrl+C (graceful shutdown)
- `--version` or `-version` flag used
- `--print-cmd` flag used (command printed and exit)

### Exit Code 1 Cases

- Invalid command-line flags
- Missing required HLS URL
- Configuration validation failed
- FFmpeg binary not found (preflight check)
- Insufficient file descriptors (preflight check)
- Orchestrator startup failure

---

## FFmpeg Process Exit Codes

FFmpeg processes spawned by go-ffmpeg-hls-swarm can exit with various codes. These are tracked in metrics:

### Standard Exit Codes

| Code | Category | Meaning |
|------|----------|---------|
| 0 | success | FFmpeg completed successfully |
| 1 | error | Generic error |
| 255 | error | FFmpeg internal error |

### Signal-Based Exit Codes (128 + signal)

When FFmpeg is killed by a signal, the exit code is 128 + signal number:

| Code | Category | Signal | Meaning |
|------|----------|--------|---------|
| 129 | signal | SIGHUP (1) | Hangup |
| 130 | signal | SIGINT (2) | Interrupt (Ctrl+C) |
| 131 | signal | SIGQUIT (3) | Quit |
| 137 | signal | SIGKILL (9) | Killed |
| 143 | signal | SIGTERM (15) | Terminated |

### Prometheus Metric

Exit codes are categorized and tracked in:

```promql
hls_swarm_client_exits_total{category="success|error|signal"}
```

The collector categorizes exit codes as:
- `success`: Exit code 0
- `error`: Exit codes 1-127 (normal errors)
- `signal`: Exit codes > 128 (terminated by signal)

---

## Common Exit Scenarios

### Clean Shutdown (Ctrl+C)

1. User presses Ctrl+C
2. SIGINT sent to go-ffmpeg-hls-swarm
3. Orchestrator catches signal, initiates shutdown
4. FFmpeg processes receive SIGTERM
5. Processes exit with code 143 (128 + 15)
6. Orchestrator waits for all processes
7. go-ffmpeg-hls-swarm exits with code 0

### Duration Complete

1. Test duration elapsed
2. Orchestrator stops spawning new clients
3. Waits for ramp-down of existing clients
4. go-ffmpeg-hls-swarm exits with code 0

### Network Error (FFmpeg)

1. FFmpeg loses connection to stream
2. Reconnection attempts fail
3. FFmpeg exits with code 1
4. Tracked as `error` category in metrics
5. If `-restart-on-stall` enabled, new process spawned

### Stall Detection

1. FFmpeg reports speed < 0.9x for >5 seconds
2. Client marked as stalled
3. If `-restart-on-stall` enabled:
   - FFmpeg sent SIGTERM (exit 143)
   - New process spawned
   - Tracked as restart

---

## Debugging Exit Issues

### Check exit code distribution

```promql
sum by (category) (hls_swarm_client_exits_total)
```

### High error exits

If `error` category is high:
- Check network connectivity
- Verify stream URL is accessible
- Check FFmpeg version compatibility
- Review `-timeout` and `-reconnect` settings

### High signal exits

If `signal` category is high during normal operation (not shutdown):
- May indicate resource exhaustion (OOM killer)
- Check system memory and file descriptors
- Review `-restart-on-stall` behavior

### Uptime distribution

```promql
histogram_quantile(0.5, hls_swarm_client_uptime_seconds)
```

Short uptimes with error exits indicate persistent stream issues.
