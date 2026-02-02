# Nginx HLS Caching Design

> **Status**: Design Complete - Ready for Implementation
> **Created**: 2026-01-30
> **Issue**: Manifest updates delayed by ~10 seconds due to open_file_cache
> **Solution**: Tiered caching - 10s for segments (immutable), 500ms for manifests

---

## Executive Summary

The test origin's Nginx configuration has a caching conflict: `open_file_cache_valid 10s` checks file freshness every 10 seconds, but HLS manifests update every 2 seconds (segment duration). This causes clients to receive stale manifests for up to 10 seconds.

---

## 1. How FFmpeg Writes HLS Files

### File Write Pattern

FFmpeg with `-hls_flags temp_file` uses atomic writes:

```
1. Write segment data to:     /var/hls/seg00123.ts.tmp
2. Rename (atomic) to:        /var/hls/seg00123.ts
3. Write manifest to:         /var/hls/stream.m3u8.tmp
4. Rename (atomic) to:        /var/hls/stream.m3u8
```

### File Lifecycle

| File Type | Write Frequency | Mutability | Lifetime |
|-----------|-----------------|------------|----------|
| Segments (.ts) | Once per segment (every 2s) | **Immutable** after creation | `(listSize + deleteThreshold) * segmentDuration` = 30s |
| Manifest (.m3u8) | Every segment (every 2s) | **Mutable** - content changes | Persistent (always exists) |
| Master playlist | Once at startup | **Immutable** (unless variants change) | Persistent |

### Current Configuration

From `nix/test-origin/config/base.nix`:
```nix
hls = {
  segmentDuration = 2;   # 2 seconds per segment
  listSize = 10;         # 10 segments in manifest
  deleteThreshold = 5;   # Delete after 5 more segments
  flags = [ "delete_segments" "omit_endlist" "temp_file" ];
};
```

---

## 2. Current Nginx Caching Configuration

### open_file_cache (File Metadata/Descriptor Cache)

From `nix/test-origin/nginx.nix`:
```nginx
# File descriptor caching (reduces stat() syscalls)
open_file_cache          max=10000 inactive=30s;
open_file_cache_valid    10s;    # <-- PROBLEM: Check freshness every 10s
open_file_cache_min_uses 1;
open_file_cache_errors   on;
```

**What `open_file_cache` actually caches:**
- File descriptors (open file handles)
- File size
- File modification time (mtime)
- Directory existence

**What `open_file_cache_valid` controls:**
- How often Nginx re-stats the file to check if mtime/size changed
- During this interval, Nginx serves from the cached file descriptor
- **If the file is rewritten, Nginx won't notice for up to `open_file_cache_valid` seconds**

### Cache-Control Headers (Client/CDN Cache)

From `nix/test-origin/config/cache.nix`:
```nix
# Segments: immutable, cache for full lifetime
segment = {
  maxAge = 60;           # Cache for 60 seconds
  immutable = true;      # Hint: content never changes
  public = true;
};

# Manifests: short TTL with stale-while-revalidate
manifest = {
  maxAge = 1;                    # segmentDuration / 2 = 1s
  staleWhileRevalidate = 2;      # segmentDuration = 2s
  public = true;
};
```

**Generated Headers:**
- Segments: `Cache-Control: public, max-age=60, immutable, no-transform`
- Manifests: `Cache-Control: public, max-age=1, stale-while-revalidate=2, no-transform`

---

## 3. The Problem: Caching Layer Mismatch

### Timeline of a Manifest Update

```
T+0.0s: FFmpeg writes stream.m3u8.tmp
T+0.0s: FFmpeg renames to stream.m3u8 (atomic, mtime updated)
T+0.0s: File on disk is now fresh

T+0.1s: Client requests /stream.m3u8
        Nginx checks open_file_cache → cache entry exists, still valid (10s window)
        Nginx serves STALE manifest from cached file descriptor

T+2.0s: FFmpeg writes new manifest (mtime updated on disk)

T+5.0s: Client requests /stream.m3u8
        Nginx checks open_file_cache → still within 10s window
        Nginx serves STALE manifest (now 5 seconds old)

T+10.0s: open_file_cache_valid expires
         Nginx re-stats the file, sees new mtime
         Nginx opens fresh file descriptor
         Next request gets FRESH manifest
```

### Observed Behavior

- Manifest sequence stuck at 51 for ~10 seconds
- Then jumped to 55 (4 segments = 8 seconds of content)
- Matches exactly with `open_file_cache_valid 10s`

---

## 4. Understanding Nginx Caching Layers

### Layer 1: open_file_cache (Server-Side File Cache)

| Directive | Purpose | Current | Issue |
|-----------|---------|---------|-------|
| `open_file_cache max=N inactive=Ts` | Cache up to N file descriptors, evict if unused for T seconds | `max=10000 inactive=30s` | OK |
| `open_file_cache_valid Ts` | Re-stat files every T seconds to check freshness | `10s` | **Too long for 2s manifests** |
| `open_file_cache_min_uses N` | Only cache after N accesses | `1` | OK |
| `open_file_cache_errors on` | Cache negative lookups (404s) | `on` | OK |

### Layer 2: Cache-Control Headers (Client/CDN Cache)

The Cache-Control headers are **correctly configured**:
- Manifests: `max-age=1, stale-while-revalidate=2` (appropriate for 2s segments)
- Segments: `max-age=60, immutable` (segments never change)

**But**: Cache-Control only affects downstream caches (browsers, CDNs). It doesn't affect Nginx's internal `open_file_cache`.

### Layer 3: OS Page Cache

- Nginx uses `sendfile on` which leverages OS page cache
- `directio 4m` bypasses page cache only for files > 4MB
- HLS segments (~1.3MB) use page cache (good for performance)
- Manifests (~400 bytes) definitely use page cache

---

## 5. Proposed Solution

### Option A: Reduce open_file_cache_valid for Manifests

**Problem**: `open_file_cache_valid` is a global setting - can't set different values per location.

**Workaround**: Use a very short global value:
```nginx
open_file_cache_valid 1s;  # Check file freshness every 1 second
```

**Trade-off**: More `stat()` syscalls for segments (which don't need frequent checks).

### Option B: Disable open_file_cache for Manifests Only

Nginx doesn't support per-location `open_file_cache` settings, but we can use a workaround:

```nginx
location ~ \.m3u8$ {
    open_file_cache off;  # Disable caching for manifests
    # ... rest of config
}
```

**Trade-off**: Slightly more overhead for manifest requests, but manifests are tiny (~400 bytes).

### Option C: Use if-modified-since with open_file_cache_valid

Keep the current `open_file_cache_valid 10s` but ensure clients send `If-Modified-Since` headers. When they do, Nginx will still serve from cache but include proper `Last-Modified` headers.

**Problem**: Doesn't help when Nginx itself doesn't know the file changed.

### Option D: Reduce open_file_cache_valid Globally (Recommended)

Since segments are immutable and manifests are small:
- The overhead of more frequent `stat()` calls is minimal
- Modern SSDs can handle millions of stat() calls per second
- tmpfs (used for /var/hls) has near-zero stat() overhead

**Recommendation**:
```nginx
open_file_cache          max=10000 inactive=30s;
open_file_cache_valid    1s;   # Check every 1 second (matches manifest TTL)
open_file_cache_min_uses 1;
open_file_cache_errors   on;
```

---

## 6. Analysis: Impact of Shorter open_file_cache_valid

### Stat() Syscall Overhead

With `open_file_cache_valid 1s`:
- Each file gets stat()'d at most once per second
- With 250 clients polling manifests every 500ms:
  - Without cache: 500 stat() calls/second
  - With 1s cache: 1 stat() call/second (per file)

**Conclusion**: `open_file_cache` is still highly effective at 1s validity.

### Segment Files

Segments are immutable, so checking them every 1s vs 10s has no functional impact - just slightly more syscalls. On tmpfs, this overhead is negligible.

### Manifest Files

Manifests update every 2 seconds. With `open_file_cache_valid 1s`:
- Worst case staleness: 1 second (acceptable)
- Average staleness: 0.5 seconds (good)

With `open_file_cache_valid 10s`:
- Worst case staleness: 10 seconds (unacceptable for live streaming)
- Average staleness: 5 seconds (causes playback stalls)

---

## 7. Alternative Consideration: Disable open_file_cache for Manifests

Per the Nginx documentation, `open_file_cache off` can be set per-location:

```nginx
location ~ \.m3u8$ {
    open_file_cache off;
    # Manifests are ~400 bytes, overhead is negligible
}

location ~ \.ts$ {
    # Segments use global open_file_cache (aggressive caching OK)
}
```

**Pros**:
- Manifests always fresh (0 staleness)
- Segments still benefit from aggressive caching

**Cons**:
- Additional stat() and open() for every manifest request
- With 250 clients × 2 polls/second = 500 syscalls/second
- Still acceptable on tmpfs

---

## 8. Recommendation: Tiered Caching Strategy

### Design Philosophy

1. **Segments (.ts)**: Cache aggressively - they're immutable once written
2. **Manifests (.m3u8)**: Cache with short validity - they update every 2s but we want caching benefits
3. **Stale-while-revalidate**: Always serve cached content while checking freshness in background

### Recommended Configuration

Since `open_file_cache_valid` can be set per-location, we can use different strategies:

```nginx
# Global: Aggressive caching for segments (default)
open_file_cache          max=10000 inactive=30s;
open_file_cache_valid    10s;   # Segments are immutable, 10s is fine
open_file_cache_min_uses 1;
open_file_cache_errors   on;

# Per-location: Frequent validation for manifests
location ~ \.m3u8$ {
    open_file_cache_valid 500ms;  # Check freshness every 500ms
    # With 2s segments, max staleness = 500ms (25% of segment duration)
    # Cache still serves during revalidation (stale-while-revalidate)
}
```

### Why 500ms for Manifests?

| Metric | Value |
|--------|-------|
| Segment duration | 2000ms |
| Manifest update interval | 2000ms |
| Cache validity | 500ms |
| Max staleness | 500ms (25% of segment) |
| Revalidation time | <1ms (tmpfs stat()) |

**Benefits**:
- 500ms validity = at most 1 revalidation per 4 client polls (if polling every 500ms)
- Serves cached content ~75% of the time
- Max 500ms behind live edge (acceptable for most players)
- Stale-while-revalidate ensures no blocking during revalidation

### Cache-Control Headers (Client/CDN Layer)

Keep the current stale-while-revalidate strategy:

```nix
# Manifests: short TTL with stale-while-revalidate
manifest = {
  maxAge = 1;                    # 1 second (half segment duration)
  staleWhileRevalidate = 2;      # Full segment duration
  public = true;
};

# Segments: immutable, cache forever (within reason)
segment = {
  maxAge = 60;
  immutable = true;
  public = true;
};
```

### Implementation Notes

1. **Nginx millisecond support**: Nginx 1.15.3+ supports millisecond precision for time values using `ms` suffix (e.g., `500ms`). The nixpkgs nginx is well above this version.
2. If `ms` not supported on older systems, use `1s` as fallback (still much better than 10s)
3. The per-location `open_file_cache_valid` overrides the global setting
4. **Verification**: After implementation, verify with `nginx -T` that the config is parsed correctly

---

## 9. Filesystem Optimization for HLS Directory

### Current Configuration

From `nix/test-origin/nixos-module.nix` (lines 79-88):
```nix
fileSystems."/var/hls" = {
  device = "tmpfs";
  fsType = "tmpfs";
  options = [
    "size=${toString d.recommendedTmpfsMB}M"
    "uid=hls"
    "gid=hls"
    "mode=0755"
  ];
};
```

**Status**: Using tmpfs (good!), but missing performance and security options.

### Issues Identified

| Issue | Impact | Severity |
|-------|--------|----------|
| No `noatime` | Every read updates access time metadata | **High** |
| No `nodiratime` | Directory reads update atime (implied by noatime) | Medium |
| No `nodev` | Could interpret special device files | Low (security) |
| No `nosuid` | Could allow setuid binaries | Low (security) |
| No `noexec` | Could execute binaries from tmpfs | Low (security) |

### Why `noatime` Matters for HLS

Without `noatime`, every file read triggers:
1. A metadata write to update `atime` (access time)
2. Even on tmpfs, this involves locking and memory operations

**Impact with 250 clients polling manifests every 500ms:**
- 500 reads/second × 1 manifest = 500 atime updates/second
- Each atime update requires: lock → update → unlock
- Adds latency and contention under high load

**With `noatime`:**
- Zero metadata writes on reads
- Pure read operation with no locking for atime
- Significantly better performance under high concurrency

### Recommended Configuration

```nix
fileSystems."/var/hls" = {
  device = "tmpfs";
  fsType = "tmpfs";
  options = [
    "size=${toString d.recommendedTmpfsMB}M"
    "uid=hls"
    "gid=hls"
    "mode=0755"
    # Performance optimizations
    "noatime"      # Don't update access time (huge win for read-heavy workloads)
    # Security hardening
    "nodev"        # No device files (HLS only needs regular files)
    "nosuid"       # No setuid/setgid (HLS files shouldn't be executable)
    "noexec"       # No execution (HLS files are data, not programs)
  ];
};
```

### Mount Options Explained

| Option | Purpose | Benefit |
|--------|---------|---------|
| `noatime` | Don't update access time on reads | Eliminates metadata writes, reduces contention |
| `nodiratime` | Don't update directory access time | Implied by `noatime` on modern kernels |
| `nodev` | Don't interpret char/block devices | Security: prevents device node attacks |
| `nosuid` | No setuid/setgid bits | Security: prevents privilege escalation |
| `noexec` | No binary execution | Security: HLS files shouldn't be executable |

### Performance Comparison

| Scenario | Without noatime | With noatime |
|----------|-----------------|--------------|
| 1 client, 1 req/s | No difference | No difference |
| 250 clients, 2 req/s | 500 atime writes/s | 0 atime writes/s |
| 1000 clients, 2 req/s | 2000 atime writes/s | 0 atime writes/s |

Even on tmpfs, eliminating unnecessary operations improves:
- Latency consistency (no lock contention spikes)
- CPU efficiency (less kernel time spent on metadata)
- Scalability (better under high concurrency)

### Related: Linux Kernel Default

Modern Linux kernels use `relatime` by default (update atime only if older than mtime), but this still causes unnecessary writes for frequently-read files like manifests. `noatime` is the optimal choice for HLS workloads.

---

## 10. Nginx Caching Architecture Analysis

### Can We Have Separate Caching Pools?

**Short answer**: No, `open_file_cache` is a single global cache.

**What nginx supports:**

| Feature | Global | Per-Location | Notes |
|---------|--------|--------------|-------|
| `open_file_cache max=N inactive=Ts` | ✅ | ❌ | Single global pool |
| `open_file_cache_valid Ts` | ✅ | ✅ | Can override per location |
| `open_file_cache_min_uses N` | ✅ | ❌ | Global only |
| `open_file_cache_errors on/off` | ✅ | ❌ | Global only |
| `open_file_cache off` | ✅ | ✅ | Can disable per location |

**Why no separate pools?**
- `open_file_cache` caches file descriptors at the process level
- File descriptors are a process-wide resource
- Nginx doesn't have a mechanism to partition FD cache by location

**Contrast with `proxy_cache`:**
- `proxy_cache` DOES support multiple zones (e.g., `proxy_cache_path ... keys_zone=segments:10m`)
- But `proxy_cache` is for proxied content, not local files
- We're serving from filesystem, so `open_file_cache` is what applies

### Would Separate tmpfs Help?

**Idea**: Separate tmpfs mounts for segments vs manifests:
```
/var/hls/segments/  → tmpfs optimized for large immutable files
/var/hls/manifests/ → tmpfs optimized for small frequently-updated files
```

**Problem**: FFmpeg writes both file types to the same directory. Options:

1. **Modify FFmpeg output paths** - FFmpeg's `-hls_segment_filename` only sets the pattern, not directory
2. **Use symlinks** - Could symlink manifest to a separate tmpfs
3. **Post-processing** - Move files after creation (adds latency, defeats purpose)

**Verdict**: Not practical without FFmpeg patches. Both file types must coexist.

**However**, we can optimize the single tmpfs for BOTH workloads:
- `noatime` helps both (no metadata writes on reads)
- tmpfs is already memory-backed (no disk I/O)
- The bottleneck is nginx's file cache, not tmpfs performance

### Alternative: Disable open_file_cache for Manifests Entirely

Instead of 500ms validity, we could disable caching entirely for manifests:

```nginx
location ~ \.m3u8$ {
    open_file_cache off;  # Always fresh
}
```

**Analysis:**

| Metric | 500ms validity | Cache disabled |
|--------|----------------|----------------|
| Max staleness | 500ms | 0ms |
| Syscalls per request | 0.25 stat() avg | 1 stat() + 1 open() |
| Memory usage | Uses global pool | No cache entry |

**With 250 clients × 2 req/s = 500 manifest requests/second:**

| Approach | Syscalls/second |
|----------|-----------------|
| 10s validity (current) | ~50 (1 check every 10s per file) |
| 500ms validity | ~1000 (1 check every 500ms per file) |
| Cache disabled | ~1000 stat() + 500 open() = 1500 |

**On tmpfs, 1500 syscalls/second is trivial.** Consider disabling cache for manifests if 0ms staleness is critical.

---

## 11. Comprehensive Performance Optimization Audit

### Already Implemented ✅

| Optimization | Location | Status |
|-------------|----------|--------|
| `sendfile on` | nginx.nix | ✅ Enabled |
| `tcp_nopush on` (segments) | nginx.nix | ✅ Enabled |
| `tcp_nodelay on` (manifests) | nginx.nix | ✅ Enabled |
| `aio threads` | nginx.nix | ✅ 32 threads |
| `directio 4m` | nginx.nix | ✅ Page cache for <4MB |
| `reuseport` | nixos-module.nix | ✅ Multiple accept threads |
| TCP buffer tuning | sysctl.nix | ✅ 16MB max |
| Connection limits | sysctl.nix | ✅ 65535 |
| TCP Fast Open | sysctl.nix | ✅ Enabled |
| `tcp_slow_start_after_idle=0` | sysctl.nix | ✅ Keep cwnd |
| `tcp_tw_reuse` | sysctl.nix | ✅ Enabled |
| Worker processes | nginx | ✅ auto (4 vCPU) |
| Worker connections | nginx | ✅ 16384 |
| Keepalive timeout | nginx | ✅ 30s |
| `reset_timedout_connection` | nginx | ✅ Free dirty connections |

### Recommended Changes 🔧

| Optimization | Priority | Impact | Notes |
|-------------|----------|--------|-------|
| `open_file_cache_valid 500ms` for manifests | **Critical** | Freshness | Currently 10s causes 10s staleness |
| tmpfs `noatime` | **High** | CPU/latency | Eliminates atime writes |
| tmpfs `nodev,nosuid,noexec` | Medium | Security | Defense in depth |
| BBR congestion control | Medium | Throughput | Better than cubic under loss |
| Busy polling | Low | Latency | Trade CPU for µs latency |
| CPU affinity | Low | Cache hits | Pin workers to CPUs |

### Not Recommended ❌

| Optimization | Reason |
|-------------|--------|
| Huge pages for tmpfs | Segments ~1.3MB, 2MB huge pages waste 35%+ |
| Separate tmpfs per file type | FFmpeg can't split output |
| io_uring | Nginx doesn't support it |
| Kernel bypass (DPDK/XDP) | Overkill, requires custom stack |
| gzip for manifests | CPU trade-off not worth 250 bytes saved |

### Optimizations to Add

#### 11.1 BBR Congestion Control - NOT RECOMMENDED

BBR (Bottleneck Bandwidth and RTT) is designed for:
- High-latency networks (WAN, internet)
- Networks with packet loss
- Long-distance paths where bandwidth estimation matters

**Our test environment:**
- Single machine or local network
- Very low latency (<1ms)
- Near-zero packet loss
- Short paths where congestion control barely matters

**Verdict**: **Keep CUBIC (default)**. BBR's benefits only manifest under conditions we don't have. The extra CPU overhead for BBR's bandwidth probing provides no benefit in low-latency, low-loss environments.

```nix
# Keep the default in sysctl.nix
"net.ipv4.tcp_congestion_control" = "cubic";  # Default, optimal for local testing
```

#### 11.2 Busy Polling (Ultra-Low Latency)

Trade CPU cycles for reduced latency:

```nix
# In sysctl.nix
"net.core.busy_poll" = 50;      # µs to busy-poll on poll()
"net.core.busy_read" = 50;      # µs to busy-poll on read()
```

**Benefits:**
- Reduces manifest latency by avoiding scheduler
- Can shave 10-50µs off response time

**Trade-off:**
- Burns CPU cycles polling
- Only beneficial for latency-critical workloads

**Recommendation**: Enable if sub-millisecond manifest latency is critical.

#### 11.3 Worker CPU Affinity

Pin nginx workers to specific CPUs:

```nginx
worker_processes 4;
worker_cpu_affinity 0001 0010 0100 1000;  # Pin to cores 0,1,2,3
```

**Benefits:**
- Better CPU cache utilization
- Reduced cache thrashing between cores

**Trade-off:**
- Less flexible load distribution
- Can cause imbalance if some cores are hotter

**Recommendation**: Test with and without; often marginal improvement.

#### 11.4 Accept Mutex and Multi-Accept Tuning

```nginx
events {
    accept_mutex off;  # With reuseport, mutex not needed
    multi_accept on;   # Accept all pending connections at once
}
```

**`multi_accept on`**: When a worker is notified of pending connections, accept ALL of them in one loop iteration instead of one at a time.

| Scenario | Without multi_accept | With multi_accept |
|----------|---------------------|-------------------|
| Burst of 100 connections | 100 wake-ups, 100 accept() calls | 1 wake-up, 100 accept() calls |
| Latency under burst | Higher (more context switches) | Lower (batch processing) |

**Recommendation**: Enable `multi_accept on` for better burst handling.

#### 11.5 Dynamic open_file_cache Size

**Current**: `open_file_cache max=10000` - way oversized!

**Actual file count calculation** (from `derived.nix`):
```
filesPerVariant = listSize + deleteThreshold + 1 = 10 + 5 + 1 = 16
totalFiles = (filesPerVariant × variantCount) + 1 master playlist

Single bitrate: (16 × 1) + 1 = 17 files
2 variants:     (16 × 2) + 1 = 33 files
```

**Recommended**: Calculate dynamically with 3x safety margin:
```nix
# In derived.nix
openFileCacheMax = (filesPerVariant * variantCount + 1) * 3;
# Single: 17 * 3 = 51
# Multi:  33 * 3 = 99
```

**Why this matters:**
- Smaller cache = faster hash table lookups
- Reduced memory overhead per entry
- More predictable `inactive` timeout behavior
- Avoids wasted memory for 9,950+ unused slots

#### 11.6 Sendfile Max Chunk

Already set to 512k. This prevents large segment transfers from starving manifest requests.

```nginx
sendfile_max_chunk 512k;  # Already configured
```

#### 11.7 Output Buffers (Fine-Tuning)

```nginx
# For manifests (small, latency-sensitive)
location ~ \.m3u8$ {
    output_buffers 1 4k;  # Single small buffer, send immediately
}

# For segments (large, throughput-sensitive)
location ~ \.ts$ {
    output_buffers 2 256k;  # Larger buffers for efficiency
}
```

**Recommendation**: Add to implementation.

---

## 12. Final Implementation Summary

### All Recommended Changes

This section consolidates ALL optimizations to implement for maximum origin performance.

#### 12.1 Critical Priority (Must Implement)

| Change | File | Description |
|--------|------|-------------|
| `open_file_cache_valid 500ms` for `.m3u8` | nixos-module.nix | Reduce manifest staleness from 10s to 500ms |
| Keep `open_file_cache_valid 10s` for `.ts` | nixos-module.nix | Segments are immutable, aggressive caching OK |

#### 12.2 High Priority (Strong Performance Impact)

| Change | File | Description |
|--------|------|-------------|
| `noatime` on tmpfs | nixos-module.nix | Eliminate atime metadata writes |
| `output_buffers 1 4k` for manifests | nixos-module.nix | Small buffer for immediate send |
| `output_buffers 2 256k` for segments | nixos-module.nix | Larger buffers for throughput |
| `multi_accept on` | nixos-module.nix | Accept all pending connections at once |
| Dynamic `open_file_cache max=N` | derived.nix + nixos-module.nix | Calculate from actual file count (not 10000) |

#### 12.3 Medium Priority (Security)

| Change | File | Description |
|--------|------|-------------|
| `nodev,nosuid,noexec` on tmpfs | nixos-module.nix | Security hardening |
| `accept_mutex off` | nginx config | Not needed with reuseport |

#### 12.4 Low Priority (Marginal Gains)

| Change | File | Description |
|--------|------|-------------|
| Worker CPU affinity | nginx config | Pin workers to cores (test if beneficial) |

#### 12.5 Not Recommended

| Optimization | Reason |
|--------------|--------|
| BBR congestion control | Test environment is low-latency, low-loss; BBR benefits only manifest on lossy WAN links |
| Huge pages for tmpfs | Segment size (1.3MB) doesn't align well with 2MB pages |
| Separate tmpfs per file type | FFmpeg can't split output to different directories |
| io_uring | Nginx doesn't support it |
| Kernel bypass (DPDK/XDP) | Overkill for this use case |
| Gzip for manifests | CPU trade-off not worth 250 bytes saved |
| Rate limiting | Test origin, not production |
| Busy polling | Burns CPU for µs gains; not worth it for local testing |

---

### Complete Implementation Diff

#### File: `nix/test-origin/config/derived.nix`

**Add open_file_cache calculation:**
```nix
# Add to the let block
totalHlsFiles = filesPerVariant * variantCount + 1;  # +1 for master playlist
openFileCacheMax = totalHlsFiles * 3;  # 3x safety margin

# Export in the attribute set
inherit totalHlsFiles openFileCacheMax;
```

**Calculated values:**
| Profile | filesPerVariant | variantCount | totalHlsFiles | openFileCacheMax |
|---------|-----------------|--------------|---------------|------------------|
| Single bitrate | 16 | 1 | 17 | 51 |
| 2 variants | 16 | 2 | 33 | 99 |

#### File: `nix/test-origin/nixos-module.nix`

**tmpfs mount (lines 79-88):**
```nix
fileSystems."/var/hls" = {
  device = "tmpfs";
  fsType = "tmpfs";
  options = [
    "size=${toString d.recommendedTmpfsMB}M"
    "uid=hls"
    "gid=hls"
    "mode=0755"
    # Performance optimizations
    "noatime"      # Don't update access time on reads
    # Security hardening
    "nodev"        # No device files
    "nosuid"       # No setuid/setgid
    "noexec"       # No binary execution
  ];
};
```

**Manifest location (add output_buffers and open_file_cache_valid):**
```nix
locations."~ \\.m3u8$" = {
  extraConfig = ''
    # Tiered caching - see docs/NGINX_HLS_CACHING_DESIGN.md
    open_file_cache_valid 500ms;  # Check freshness frequently for manifests
    output_buffers 1 4k;          # Small buffer for immediate send

    ${nginx.manifestAccessLog};
    tcp_nodelay    on;
    add_header Cache-Control "${nginx.manifestCacheControl}";
    add_header Access-Control-Allow-Origin "*";
    add_header Access-Control-Expose-Headers "Content-Length";
    types { application/vnd.apple.mpegurl m3u8; }
  '';
};
```

**Segment location (add output_buffers):**
```nix
locations."~ \\.ts$" = {
  extraConfig = ''
    output_buffers 2 256k;        # Larger buffers for throughput

    ${nginx.segmentAccessLog};
    sendfile       on;
    tcp_nopush     on;
    add_header Cache-Control "${nginx.segmentCacheControl}";
    add_header Access-Control-Allow-Origin "*";
    add_header Access-Control-Expose-Headers "Content-Length,Content-Range";
    add_header Accept-Ranges bytes;
    types { video/mp2t ts; }
  '';
};
```

**Update appendHttpConfig (dynamic open_file_cache max):**
```nix
appendHttpConfig = ''
  # Dynamic open_file_cache sizing - see docs/NGINX_HLS_CACHING_DESIGN.md
  # Calculated: ${toString d.openFileCacheMax} = (${toString d.filesPerVariant} files/variant × ${toString d.variantCount} variants + 1 master) × 3
  open_file_cache max=${toString d.openFileCacheMax} inactive=30s;
  open_file_cache_valid 10s;  # Default for segments (immutable)
  open_file_cache_errors on;

  # Accept all pending connections at once (better burst handling)
  # Note: This is in http{} context, but multi_accept goes in events{}

  reset_timedout_connection on;
  ${lib.optionalString log.enabled nginx.logFormats}
'';
```

**Add eventsConfig for multi_accept:**
```nix
# In services.nginx block
eventsConfig = ''
  multi_accept on;  # Accept all pending connections at once
'';
```

#### File: `nix/test-origin/sysctl.nix`

**No changes required.** Current settings are optimal for local/low-latency testing:
- CUBIC congestion control (default) - appropriate for low-latency, low-loss networks
- TCP tuning already comprehensive (see existing sysctl.nix)

---

### Expected Performance Improvements

| Metric | Before | After |
|--------|--------|-------|
| Manifest staleness (max) | 10,000ms | 500ms |
| Manifest staleness (avg) | 5,000ms | 250ms |
| atime writes/request | 1 | 0 |
| Segment buffer efficiency | Default | 256k optimized |
| Manifest buffer latency | Default | 4k immediate |

---

## 13. References

- [Nginx open_file_cache documentation](http://nginx.org/en/docs/http/ngx_http_core_module.html#open_file_cache)
- [Nginx Caching Guide](https://blog.nginx.org/blog/nginx-caching-guide) - focuses on proxy_cache, not open_file_cache
- FFmpeg HLS muxer: Uses `temp_file` flag for atomic writes
- [BBR Congestion Control](https://cloud.google.com/blog/products/networking/tcp-bbr-congestion-control-comes-to-gcp-your-internet-just-got-faster)
- [Linux tmpfs mount options](https://www.kernel.org/doc/html/latest/filesystems/tmpfs.html)

---

## 14. Test Plan

After applying the fix:

1. Rebuild MicroVM with new `open_file_cache_valid 1s`
2. Run `./scripts/curl_origin_manifests.sh`
3. Verify `#EXT-X-MEDIA-SEQUENCE` increments every 2 seconds (not every 10 seconds)
4. Run load test with 250 clients
5. Verify segment throughput metrics work (the original issue)
