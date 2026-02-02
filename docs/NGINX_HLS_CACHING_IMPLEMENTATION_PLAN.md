# Nginx HLS Caching Implementation Plan

> **Status**: Ready for Review
> **Created**: 2025-01-30
> **Design Doc**: [NGINX_HLS_CACHING_DESIGN.md](./NGINX_HLS_CACHING_DESIGN.md)
> **Issue**: Manifest staleness (10s) and suboptimal origin performance

---

## Overview

This plan implements all optimizations identified in the design document to maximize HLS origin performance. Changes are ordered by dependency (derived.nix first, then config files).

### Important: Two Nginx Config Paths

There are **two separate nginx configurations** that must be kept in sync:

| File | Used By | Config Method |
|------|---------|---------------|
| `nginx.nix` | Standalone runner, `test-origin-nginx-config` package | Direct config template |
| `nixos-module.nix` | MicroVM, NixOS containers | `services.nginx` module |

The `nix build .#test-origin-nginx-config` package builds from `nginx.nix`, which we'll use for before/after diffing. The MicroVM uses `nixos-module.nix`.

---

## Pre-Implementation Checklist

- [ ] Design document reviewed and approved
- [ ] MicroVM currently stopped (`make microvm-stop`)
- [ ] No uncommitted changes in working directory

---

## Phase 0: Capture Baseline Configuration

Before making any changes, capture the current nginx configuration for comparison.

### 0.1: Save Current Nginx Config

```bash
# Create directory for config snapshots
mkdir -p /tmp/nginx-config-diff

# Capture current config (before changes)
nix build .#test-origin-nginx-config -o /tmp/nginx-config-diff/before
cat /tmp/nginx-config-diff/before > /tmp/nginx-config-diff/nginx-before.conf

# Verify the file was created
wc -l /tmp/nginx-config-diff/nginx-before.conf
# Expected: ~250 lines
```

### 0.2: Document Current Values

```bash
# Record current open_file_cache settings
grep -E "open_file_cache|output_buffers|multi_accept" /tmp/nginx-config-diff/nginx-before.conf

# Expected (before):
# open_file_cache max=10000 inactive=30s;
# open_file_cache_valid 10s;
# (no output_buffers lines)
# (no multi_accept line)
```

---

## Phase 1: Add Derived Configuration Values

**File**: `nix/test-origin/config/derived.nix`

### Change 1.1: Add open_file_cache calculation

**Location**: Inside the `let` block (after line 24)

**Add**:
```nix
# Calculate optimal open_file_cache size
# Total files = (segments + manifest per variant) × variants + master playlist
totalHlsFiles = filesPerVariant * variantCount + 1;
openFileCacheMax = totalHlsFiles * 3;  # 3x safety margin for rotation
```

**Location**: Inside the attribute set (after line 43)

**Add**:
```nix
inherit totalHlsFiles openFileCacheMax;
```

**Verification**:
```bash
# After change, verify values are computed correctly
nix eval .#test-origin-vm.config.derived.openFileCacheMax
# Expected: 51 (single bitrate) or 99 (2 variants)
```

---

## Phase 2: Update tmpfs Mount Options

**File**: `nix/test-origin/nixos-module.nix`

### Change 2.1: Add performance and security options to tmpfs

**Location**: Lines 79-88 (fileSystems."/var/hls")

**Before**:
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

**After**:
```nix
fileSystems."/var/hls" = {
  device = "tmpfs";
  fsType = "tmpfs";
  options = [
    "size=${toString d.recommendedTmpfsMB}M"
    "uid=hls"
    "gid=hls"
    "mode=0755"
    # Performance: Don't update access time on reads
    "noatime"
    # Security: HLS files are data, not executables or devices
    "nodev"
    "nosuid"
    "noexec"
  ];
};
```

**Verification**:
```bash
# After VM starts, verify mount options
ssh -p 17122 root@localhost 'mount | grep /var/hls'
# Expected: tmpfs on /var/hls type tmpfs (rw,nosuid,nodev,noexec,noatime,...)
```

---

## Phase 3: Update Nginx Configuration

**File**: `nix/test-origin/nixos-module.nix`

### Change 3.1: Add eventsConfig for multi_accept

**Location**: Inside `services.nginx = {` block (after line 263)

**Add**:
```nix
# Accept all pending connections at once (better burst handling)
eventsConfig = ''
  multi_accept on;
'';
```

### Change 3.2: Update appendHttpConfig with dynamic open_file_cache

**Location**: Lines 273-289 (appendHttpConfig)

**Before**:
```nix
appendHttpConfig = ''
  # File descriptor caching - see docs/NGINX_HLS_CACHING_DESIGN.md
  # open_file_cache caches file descriptors AND stat() results (mtime, size)
  # open_file_cache_valid controls how often nginx re-stats files to detect changes
  #
  # Tiered caching strategy:
  # - Segments (.ts): Immutable, use aggressive 10s validity (global default)
  # - Manifests (.m3u8): Update every 2s, use 500ms validity (per-location override)
  open_file_cache max=10000 inactive=30s;
  open_file_cache_valid 10s;  # Default for segments (immutable)
  open_file_cache_errors on;

  # Free memory faster from dirty client exits
  reset_timedout_connection on;

  ${lib.optionalString log.enabled nginx.logFormats}
'';
```

**After**:
```nix
appendHttpConfig = ''
  # File descriptor caching - see docs/NGINX_HLS_CACHING_DESIGN.md
  # Dynamic sizing: ${toString d.openFileCacheMax} = (${toString d.filesPerVariant} files/variant * ${toString d.variantCount} variants + 1 master) * 3
  #
  # Tiered caching strategy:
  # - Segments (.ts): Immutable, use aggressive 10s validity (global default)
  # - Manifests (.m3u8): Update every 2s, use 500ms validity (per-location override)
  open_file_cache max=${toString d.openFileCacheMax} inactive=30s;
  open_file_cache_valid 10s;  # Default for segments (immutable)
  open_file_cache_errors on;

  # Free memory faster from dirty client exits
  reset_timedout_connection on;

  ${lib.optionalString log.enabled nginx.logFormats}
'';
```

### Change 3.3: Update manifest location with output_buffers

**Location**: Lines 309-323 (locations."~ \\.m3u8$")

**Before**:
```nix
locations."~ \\.m3u8$" = {
  extraConfig = ''
    # Override global open_file_cache_valid for manifests
    # Manifests update every 2s; 500ms validity = max 25% staleness
    # Still cache (serves ~75% of requests), but check freshness frequently
    open_file_cache_valid 500ms;

    ${nginx.manifestAccessLog};
    tcp_nodelay    on;
    add_header Cache-Control "${nginx.manifestCacheControl}";
    add_header Access-Control-Allow-Origin "*";
    add_header Access-Control-Expose-Headers "Content-Length";
    types { application/vnd.apple.mpegurl m3u8; }
  '';
};
```

**After**:
```nix
locations."~ \\.m3u8$" = {
  extraConfig = ''
    # Override global open_file_cache_valid for manifests
    # Manifests update every 2s; 500ms validity = max 25% staleness
    # Still cache (serves ~75% of requests), but check freshness frequently
    open_file_cache_valid 500ms;

    # Small output buffer for immediate send (manifests are ~400 bytes)
    output_buffers 1 4k;

    ${nginx.manifestAccessLog};
    tcp_nodelay    on;
    add_header Cache-Control "${nginx.manifestCacheControl}";
    add_header Access-Control-Allow-Origin "*";
    add_header Access-Control-Expose-Headers "Content-Length";
    types { application/vnd.apple.mpegurl m3u8; }
  '';
};
```

### Change 3.4: Update segment location with output_buffers

**Location**: Lines 326-338 (locations."~ \\.ts$")

**Before**:
```nix
locations."~ \\.ts$" = {
  extraConfig = ''
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

**After**:
```nix
locations."~ \\.ts$" = {
  extraConfig = ''
    # Larger output buffers for throughput (segments are ~1.3MB)
    output_buffers 2 256k;

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

---

## Phase 3B: Update Standalone Nginx Config (nginx.nix)

**File**: `nix/test-origin/nginx.nix`

This file generates the standalone nginx config used by the runner script and the `test-origin-nginx-config` package. It must be updated to match `nixos-module.nix`.

### Change 3B.1: Update open_file_cache to use derived value

**Location**: Line 136 (inside configFile)

**Before**:
```nix
        # File descriptor caching - see docs/NGINX_HLS_CACHING_DESIGN.md
        # Tiered strategy: aggressive for segments (10s), frequent for manifests (500ms per-location)
        open_file_cache          max=10000 inactive=30s;
```

**After**:
```nix
        # File descriptor caching - see docs/NGINX_HLS_CACHING_DESIGN.md
        # Dynamic sizing: max=${toString config.derived.openFileCacheMax} files
        # Tiered strategy: aggressive for segments (10s), frequent for manifests (500ms per-location)
        open_file_cache          max=${toString config.derived.openFileCacheMax} inactive=30s;
```

### Change 3B.2: Add output_buffers to manifest location

**Location**: ~Line 188 (inside `location ~ \.m3u8$`)

**Before**:
```nix
            location ~ \.m3u8$ {
                ${manifestAccessLog};
                tcp_nodelay    on;  # Immediate delivery for freshness
```

**After**:
```nix
            location ~ \.m3u8$ {
                open_file_cache_valid 500ms;  # Check freshness frequently for manifests
                output_buffers 1 4k;          # Small buffer for immediate send
                ${manifestAccessLog};
                tcp_nodelay    on;  # Immediate delivery for freshness
```

### Change 3B.3: Add output_buffers to segment location

**Location**: ~Line 204 (inside `location ~ \.ts$`)

**Before**:
```nix
            location ~ \.ts$ {
                ${segmentAccessLog};
                sendfile       on;
```

**After**:
```nix
            location ~ \.ts$ {
                output_buffers 2 256k;        # Larger buffers for throughput
                ${segmentAccessLog};
                sendfile       on;
```

**Note**: `multi_accept on` is already present in `nginx.nix` line 114.

---

## Phase 4: Generate and Diff Nginx Config

After making all changes, generate the new config and verify the diff matches expectations.

### 4.1: Build New Nginx Config

```bash
# Build the new config (after changes)
nix build .#test-origin-nginx-config -o /tmp/nginx-config-diff/after
cat /tmp/nginx-config-diff/after > /tmp/nginx-config-diff/nginx-after.conf
```

### 4.2: Generate Diff

```bash
# Show the diff
diff -u /tmp/nginx-config-diff/nginx-before.conf /tmp/nginx-config-diff/nginx-after.conf

# Or use a side-by-side diff
diff -y --width=120 /tmp/nginx-config-diff/nginx-before.conf /tmp/nginx-config-diff/nginx-after.conf | head -100
```

### 4.3: Verify Expected Changes

The diff should show ONLY these changes:

```diff
# 1. Comment update for dynamic sizing
-        # Tiered strategy: aggressive for segments (10s), frequent for manifests (500ms per-location)
+        # Dynamic sizing: max=51 files
+        # Tiered strategy: aggressive for segments (10s), frequent for manifests (500ms per-location)

# 2. open_file_cache max changed from 10000 to 51
-        open_file_cache          max=10000 inactive=30s;
+        open_file_cache          max=51 inactive=30s;

# 3. open_file_cache_valid and output_buffers added to m3u8 location
             location ~ \.m3u8$ {
+                open_file_cache_valid 500ms;  # Check freshness frequently for manifests
+                output_buffers 1 4k;          # Small buffer for immediate send

# 4. output_buffers added to ts location
             location ~ \.ts$ {
+                output_buffers 2 256k;        # Larger buffers for throughput
```

**Note**: `multi_accept on` should NOT appear in the diff - it's already in `nginx.nix`.

**If the diff shows unexpected changes, STOP and investigate before proceeding.**

### 4.4: Changes NOT Visible in Nginx Config Diff

The following changes are in NixOS configuration, not nginx.conf:

| Change | Where It's Applied | How to Verify |
|--------|-------------------|---------------|
| tmpfs `noatime,nodev,nosuid,noexec` | `/etc/fstab` in VM | `mount \| grep /var/hls` at runtime |
| `open_file_cache_valid 500ms` | Already in nixos-module.nix | Already in nginx config (verify present) |

### 4.5: Save Diff for Records

```bash
# Save the diff to a file for reference
diff -u /tmp/nginx-config-diff/nginx-before.conf /tmp/nginx-config-diff/nginx-after.conf \
  > /tmp/nginx-config-diff/implementation.diff

# Review the saved diff
cat /tmp/nginx-config-diff/implementation.diff
```

---

## Phase 5: Runtime Verification

### 5.1: Build Verification

```bash
# Verify nix evaluation succeeds
nix flake check --no-build

# Verify derived values
nix eval .#test-origin-vm.config.derived.openFileCacheMax
# Expected: 51 (single bitrate)

nix eval .#test-origin-vm.config.derived.totalHlsFiles
# Expected: 17 (single bitrate)
```

### 5.2: Start MicroVM

```bash
# Clean build and start
rm -rf result-tap
make microvm-start-tap
# Or for user-mode networking:
# make microvm-start
```

### 5.3: Verify tmpfs Mount Options

```bash
ssh -p 17122 root@localhost 'mount | grep /var/hls'
# Expected output should include: noatime,nodev,nosuid,noexec
```

### 5.4: Verify Nginx Configuration (Runtime)

```bash
ssh -p 17122 root@localhost 'nginx -T 2>/dev/null | grep -A2 "open_file_cache"'
# Expected: open_file_cache max=51 inactive=30s (or max=99 for multibitrate)

ssh -p 17122 root@localhost 'nginx -T 2>/dev/null | grep "multi_accept"'
# Expected: multi_accept on;

ssh -p 17122 root@localhost 'nginx -T 2>/dev/null | grep "output_buffers"'
# Expected: output_buffers 1 4k; and output_buffers 2 256k;

ssh -p 17122 root@localhost 'nginx -T 2>/dev/null | grep "open_file_cache_valid"'
# Expected: 10s (global) and 500ms (in m3u8 location)
```

### 5.5: Verify Manifest Freshness

```bash
# Run manifest polling script
./scripts/curl_origin_manifests.sh

# Verify sequence increments every 2 seconds (not every 10 seconds)
# Watch for: #EXT-X-MEDIA-SEQUENCE increasing by 1 every ~2s
```

### 5.6: Load Test Verification

```bash
# Run load test with 100 clients
make run CLIENTS=100 DURATION=60s

# Verify:
# 1. Segment Throughput column shows data (not "(no data)")
# 2. No manifest staleness errors
# 3. Stable performance throughout test
```

---

## Rollback Plan

If issues occur, revert to previous configuration:

```bash
# Stop MicroVM
make microvm-stop

# Revert changes
git checkout -- nix/test-origin/config/derived.nix
git checkout -- nix/test-origin/nixos-module.nix

# Rebuild and restart
rm -rf result-tap
make microvm-start-tap
```

---

## Summary of Changes

| File | Change | Lines |
|------|--------|-------|
| `config/derived.nix` | Add `totalHlsFiles`, `openFileCacheMax` | +4 |
| `nixos-module.nix` | Add tmpfs options (`noatime`, `nodev`, `nosuid`, `noexec`) | +5 |
| `nixos-module.nix` | Add `eventsConfig` with `multi_accept on` | +4 |
| `nixos-module.nix` | Update `open_file_cache max=` to use derived value | ~1 |
| `nixos-module.nix` | Add `output_buffers 1 4k` to manifest location | +3 |
| `nixos-module.nix` | Add `output_buffers 2 256k` to segment location | +3 |
| `nginx.nix` | Update `open_file_cache max=` to use derived value | ~2 |
| `nginx.nix` | Add `open_file_cache_valid 500ms` to manifest location | +1 |
| `nginx.nix` | Add `output_buffers 1 4k` to manifest location | +1 |
| `nginx.nix` | Add `output_buffers 2 256k` to segment location | +1 |

**Total**: ~25 lines added/modified across 3 files

---

## Expected Outcomes

| Metric | Before | After |
|--------|--------|-------|
| Manifest staleness (max) | 10,000ms | 500ms |
| Manifest staleness (avg) | 5,000ms | 250ms |
| atime writes per read | 1 | 0 |
| open_file_cache size | 10,000 (wasteful) | ~51 (right-sized) |
| Connection burst handling | Sequential | Batch (multi_accept) |
| Manifest send latency | Default buffering | Immediate (4k buffer) |
| Segment throughput | Default buffering | Optimized (256k buffer) |
