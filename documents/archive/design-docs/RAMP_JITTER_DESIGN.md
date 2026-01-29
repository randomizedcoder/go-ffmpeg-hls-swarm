# Ramp Jitter Design

## Overview

Ramp jitter is a critical feature for preventing client synchronization during load testing. Without jitter, clients started at a fixed rate can align on HLS playlist refresh intervals, causing thundering herd effects and unrealistic load patterns.

## Current Implementation

### Go Code

The jitter implementation is fully functional in the Go codebase:

1. **Flag**: `-ramp-jitter duration` (e.g., `-ramp-jitter 100ms`)
   - Defined in `internal/config/flags.go`
   - Type: `time.Duration` (parsed by Go's standard library)

2. **Scheduler**: `internal/orchestrator/ramp_scheduler.go`
   - `RampScheduler` controls client start timing
   - Combines base delay (from `-ramp-rate`) with per-client jitter
   - Uses `JitterSource` for deterministic, per-client randomness

3. **Jitter Source**: `internal/supervisor/jitter.go`
   - `JitterSource` provides deterministic jitter per client ID
   - Seeds randomness with `clientID ^ configSeed`
   - Ensures same client always gets same jitter offset (survives restarts)

### How It Works

```
With -clients 20 -ramp-rate 5 -ramp-jitter 200ms:

t=0.0s:   Start clients 0-4
          Client 0: baseDelay=200ms + jitter=45ms  = 245ms total
          Client 1: baseDelay=200ms + jitter=189ms = 389ms total
          Client 2: baseDelay=200ms + jitter=72ms  = 272ms total
          Client 3: baseDelay=200ms + jitter=156ms = 356ms total
          Client 4: baseDelay=200ms + jitter=23ms  = 223ms total

t=1.0s:   Start clients 5-9 (different jitter per client)
t=2.0s:   Start clients 10-14
t=3.0s:   Start clients 15-19
t=3.2s:   All clients running (approximately)
```

### Key Features

1. **Deterministic Per-Client**: Each client ID gets a consistent jitter value
   - Client 42 always gets the same jitter offset
   - Survives restarts (prevents reconvergence)

2. **Config Seed**: Global seed allows variation across test runs
   - Default: seeded from current time (`time.Now().UnixNano()`)
   - Can be made configurable for reproducibility

3. **Rate-Aware Capping**: At high ramp rates, jitter is capped to 50% of base delay
   - Prevents jitter from dominating timing
   - Maintains target ramp rate

4. **Jitter Range**: `[0, maxJitter)`
   - Uniform distribution within the range
   - Applied per client independently

## Container Integration

### Configuration

In `nix/swarm-client/config.nix`:
```nix
rampJitter = 100;  # milliseconds
```

### Container Script

The container entrypoint script (`nix/swarm-client/container.nix`) converts the numeric value to a duration:

```bash
# Convert milliseconds to duration format
RAMP_JITTER_RAW="100"
if echo "$RAMP_JITTER_RAW" | grep -qE '^[0-9]+$'; then
  RAMP_JITTER="${RAMP_JITTER_RAW}ms"  # "100ms"
else
  RAMP_JITTER="$RAMP_JITTER_RAW"      # Already has unit
fi

# Pass to Go binary
go-ffmpeg-hls-swarm -ramp-jitter "$RAMP_JITTER" ...
```

### Environment Variable Override

Users can override via environment variable:
```bash
docker run -e RAMP_JITTER=200ms go-ffmpeg-hls-swarm:latest ...
# or
docker run -e RAMP_JITTER=200 go-ffmpeg-hls-swarm:latest ...  # auto-converts to "200ms"
```

## Benefits

1. **Prevents Synchronization**: Clients don't all start at exact intervals
2. **Realistic Load**: Mimics real-world client behavior (staggered arrivals)
3. **Playlist Refresh Avoidance**: Reduces chance of all clients hitting playlist refresh simultaneously
4. **Restart Stability**: Per-client jitter survives restarts (no reconvergence)

## Configuration Recommendations

### Default Values

- **Low Load** (< 50 clients): `rampJitter = 100ms`
- **Medium Load** (50-200 clients): `rampJitter = 200ms`
- **High Load** (> 200 clients): `rampJitter = 500ms`

### Tuning Guidelines

1. **Too Low Jitter**: Clients may still synchronize
   - Symptom: Periodic spikes in origin load
   - Fix: Increase jitter to 200-500ms

2. **Too High Jitter**: Ramp takes too long, rate becomes unpredictable
   - Symptom: Clients take much longer than `clients / rampRate` to start
   - Fix: Decrease jitter or increase ramp rate

3. **Playlist Refresh Alignment**: If playlist refreshes every 6 seconds
   - With `ramp-rate=5`, clients start every 200ms
   - 6 seconds = 30 clients → potential alignment
   - Solution: Use jitter ≥ 200ms to break alignment

## Future Enhancements

### 1. Configurable Seed

Allow setting a seed for reproducibility:
```bash
go-ffmpeg-hls-swarm -ramp-jitter 200ms -jitter-seed 12345 ...
```

### 2. Jitter Distribution

Support different distributions (uniform, normal, exponential):
```bash
go-ffmpeg-hls-swarm -ramp-jitter 200ms -jitter-distribution normal ...
```

### 3. Phase Offset

Add artificial phase offset to delay some clients' first playlist fetch:
```bash
go-ffmpeg-hls-swarm -playlist-phase-offset 2s ...
```

This would stagger when clients first fetch the playlist, even if they start at similar times.

## Testing

### Verify Jitter Works

1. Run with verbose logging:
   ```bash
   go-ffmpeg-hls-swarm -clients 10 -ramp-rate 5 -ramp-jitter 200ms -v ...
   ```

2. Check logs for client start times - they should be staggered, not exact intervals

3. Monitor origin metrics - should see smooth load increase, not spikes

### Test Determinism

1. Run with same config twice:
   ```bash
   go-ffmpeg-hls-swarm -clients 10 -ramp-rate 5 -ramp-jitter 200ms ...
   ```

2. Compare client start times - should be identical (same jitter per client ID)

## Related Documentation

- [SUPERVISION.md](SUPERVISION.md) - Client lifecycle and restart behavior
- [LOAD_TESTING.md](LOAD_TESTING.md) - Load testing best practices
- [OBSERVABILITY.md](OBSERVABILITY.md) - Metrics and monitoring
