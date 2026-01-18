# Memory Efficiency with Multiple FFmpeg Processes

> **TL;DR**: Linux automatically shares FFmpeg's code across all processes. Spawning 100 FFmpeg instances uses ~2GB, not ~7GB.

---

## The Concern

When spawning many FFmpeg subprocesses for load testing, a natural concern is:

> "Won't loading the same FFmpeg binary 100+ times waste memory?"

**Answer: No.** Linux handles this efficiently through memory-mapped shared libraries.

---

## How Linux Shares Memory

### Code Segments Are Shared Automatically

When you run the same executable multiple times, the kernel:

1. **Memory-maps the binary** as read-only
2. **Shares physical RAM pages** across all processes
3. **Only allocates per-process memory** for heap and stack

```
Process 1 ─┐
Process 2 ─┼──► Shared Code Pages (loaded once) ──► Physical RAM
Process 3 ─┘
     ↓
Per-Process Heap/Stack (separate for each)
```

### FFmpeg Uses Dynamic Linking

Nix's FFmpeg is dynamically linked with ~50+ shared libraries:

```
$ ldd $(which ffmpeg) | head -10
  libavdevice.so.62 => /nix/store/.../lib/libavdevice.so.62
  libavfilter.so.11 => /nix/store/.../lib/libavfilter.so.11
  libavformat.so.62 => /nix/store/.../lib/libavformat.so.62
  libavcodec.so.62  => /nix/store/.../lib/libavcodec.so.62
  libswresample.so.6 => /nix/store/.../lib/libswresample.so.6
  libswscale.so.9   => /nix/store/.../lib/libswscale.so.9
  libavutil.so.60   => /nix/store/.../lib/libavutil.so.60
  ...
```

These `.so` files are loaded **once** into physical memory and mapped into each process's virtual address space.

---

## Actual Memory Usage

### Measured Data (5 FFmpeg Processes)

```
$ ps -o pid,rss,vsz,comm -C ffmpeg
    PID   RSS    VSZ COMMAND
1554951 69312 880380 ffmpeg
1554952 71288 880380 ffmpeg
1554953 71500 880380 ffmpeg
1554954 69088 880380 ffmpeg
1554955 71408 880380 ffmpeg
```

### Memory Breakdown (per process)

```
$ cat /proc/<pid>/smaps_rollup
Rss:               71472 kB    # Total resident memory
Shared_Clean:      52404 kB    # ◄── Shared library code (loaded ONCE)
Shared_Dirty:          0 kB
Private_Clean:       152 kB
Private_Dirty:     18916 kB    # ◄── Per-process heap/buffers
```

| Category | Size | Description |
|----------|------|-------------|
| **Shared_Clean** | 52 MB | Library code, shared across all processes |
| **Private_Dirty** | 19 MB | Per-process working memory |

---

## Scaling Math

### Without Sharing (Naive)

```
100 processes × 70 MB each = 7.0 GB
```

### With Linux Memory Sharing (Actual)

```
52 MB (shared code, loaded once)
+ 100 × 19 MB (per-process heap)
= 1.95 GB
```

**Savings: ~72%**

### Scaling Table

| Processes | Naive | Actual | Savings |
|-----------|-------|--------|---------|
| 10 | 700 MB | 242 MB | 65% |
| 50 | 3.5 GB | 1.0 GB | 71% |
| 100 | 7.0 GB | 1.95 GB | 72% |
| 200 | 14 GB | 3.85 GB | 72% |

---

## Why This Works with Nix

Nix's package management enhances memory sharing:

1. **Content-addressed store**: Identical binaries have identical paths
2. **No version conflicts**: All processes use the exact same FFmpeg
3. **Dynamic linking preserved**: Unlike static builds, `.so` files are shared

```
/nix/store/<hash>-ffmpeg-full-8.0/bin/ffmpeg
                 └── All processes reference this exact binary
```

---

## Reducing Memory Further (Optional)

If you need to minimize memory further:

| Option | Reduction | Trade-off |
|--------|-----------|-----------|
| Use `ffmpeg` instead of `ffmpeg-full` | ~30% smaller | Fewer codecs/formats |
| Lower `-probesize` / `-analyzeduration` | Less heap | Slower stream detection |
| Reduce concurrent clients | Linear | Lower load generation |
| Use `-threads 1` per FFmpeg | Less thread stacks | Slower processing |

### Minimal FFmpeg Example

```nix
# In nix/lib.nix, replace ffmpeg-full with:
runtimeDeps = with pkgs; [ ffmpeg ];  # Smaller build
```

---

## Verifying on Your System

### Check Shared vs Private Memory

```bash
# Start multiple FFmpeg processes
for i in {1..5}; do
  ffmpeg -f lavfi -i nullsrc -f null - 2>/dev/null &
done

# Check memory breakdown
PID=$(pgrep -n ffmpeg)
grep -E "^(Rss|Shared|Private)" /proc/$PID/smaps_rollup

# Cleanup
pkill ffmpeg
```

### Monitor Total Memory

```bash
# Watch system memory while ramping up
watch -n1 'free -h; echo; ps aux --sort=-%mem | head -10'
```

---

## Key Takeaways

1. **No code changes needed** — Linux handles sharing automatically
2. **Dynamic linking is essential** — Nix's FFmpeg already does this
3. **Per-process cost is ~19 MB** — The shared 52 MB is "free" for additional processes
4. **Scaling is efficient** — 200 processes need ~4 GB, not 14 GB

---

## Related Documentation

- [DESIGN.md](DESIGN.md) — Architecture overview
- [OPERATIONS.md](OPERATIONS.md) — Resource tuning (ulimits, file descriptors)
- [NIX_FLAKE_DESIGN.md](NIX_FLAKE_DESIGN.md) — Nix build configuration
