# HLS Generator Security Hardening Design

This document outlines the security hardening plan for the `hls-generator` service in the HLS Origin MicroVM.

## Overview

The `hls-generator` service runs FFmpeg to generate HLS test streams. Unlike nginx, it:
- **Does NOT listen on any network port** — purely a local process
- **Only needs to write** to `/var/hls`
- **Runs as a static user** (`hls`) so nginx can read its output
- Has a much **smaller attack surface** than a web server

**Initial Score:** 8.3 EXPOSED 🙁

**Target Score:** ~1.5-2.0 OK (excellent for a non-network service)

**Final Score:** 0.4 SAFE 😊 (exceeded target by 75%)

## Security Analysis

### Current Configuration

```nix
systemd.services.hls-generator.serviceConfig = {
  Type = "simple";
  User = "hls";
  Group = "hls";
  NoNewPrivileges = true;
  ProtectSystem = "strict";
  ProtectHome = true;
  PrivateTmp = true;
  ReadWritePaths = [ "/var/hls" ];
};
```

This provides basic hardening but misses most systemd security features.

### Why Such a Bad Score?

The 8.3 score comes from **missing restrictions**, not actual vulnerabilities. Key issues:

| Category | Missing | Impact |
|----------|---------|--------|
| **Capabilities** | All capabilities allowed | 0.1-0.3 each |
| **System Calls** | No filtering at all | 0.2 × 11 = 2.2 |
| **Namespaces** | All namespace creation allowed | 0.1-0.3 × 7 = 1.0 |
| **Network** | Full network access (not needed!) | 0.5 |
| **Process visibility** | Can see all processes | 0.3 |
| **Devices** | Can access all devices | 0.2 |

### What FFmpeg Actually Needs

FFmpeg for HLS generation needs:
- **File I/O** — Write to `/var/hls`
- **Memory** — Allocate buffers for encoding
- **Time** — Read system time for timestamps
- **Threads** — Multi-threaded encoding

FFmpeg does **NOT** need:
- ❌ Network access (generates local files only)
- ❌ Device access (no hardware encoding in this config)
- ❌ Kernel module loading
- ❌ Privilege escalation
- ❌ User/group changes
- ❌ Raw I/O or ptrace

## Systemd Slices for Resource Isolation

Using systemd slices provides **hierarchical resource control** and **visibility** into service performance. This is especially useful for load testing where we need to monitor nginx and FFmpeg separately.

### Slice Hierarchy

```
-.slice (root)
└── hls-origin.slice (parent for all HLS services)
    ├── hls-generator.slice (FFmpeg encoding)
    └── hls-nginx.slice (Nginx serving)
```

### Benefits

| Benefit | Description |
|---------|-------------|
| **Resource visibility** | `systemd-cgtop` shows per-slice CPU/memory |
| **Hierarchical limits** | Parent slice can cap total resource usage |
| **Isolation** | Services can't steal resources from each other |
| **Metrics** | Node exporter exposes cgroup metrics |
| **Debugging** | `systemctl status hls-origin.slice` shows all child services |

### Slice Configuration

```nix
# In nixos-module.nix
systemd.slices = {
  # Parent slice for all HLS origin services
  hls-origin = {
    description = "HLS Origin Services (FFmpeg + Nginx)";
    sliceConfig = {
      # Combined limit for all HLS services
      MemoryMax = "3G";          # Leave 1GB for system
      MemoryHigh = "2560M";      # Warn at 2.5GB
    };
  };

  # Child slice for FFmpeg encoding
  hls-generator = {
    description = "HLS Generator (FFmpeg encoding)";
    sliceConfig = {
      Slice = "hls-origin.slice";  # Inherit from parent
    };
  };

  # Child slice for Nginx serving
  hls-nginx = {
    description = "HLS Nginx (HTTP serving)";
    sliceConfig = {
      Slice = "hls-origin.slice";  # Inherit from parent
    };
  };
};

# Assign services to slices
systemd.services.hls-generator.serviceConfig.Slice = "hls-generator.slice";
systemd.services.nginx.serviceConfig.Slice = "hls-nginx.slice";
```

### Monitoring Slices

```bash
# Real-time resource usage by slice
systemd-cgtop

# Example output during load test:
# Control Group                       Tasks   %CPU   Memory
# /hls-origin.slice                      15   310%   1.2G
# /hls-origin.slice/hls-nginx.slice      4    300%   1.1G
# /hls-origin.slice/hls-generator.slice  3    10%    68M

# Check slice status
systemctl status hls-origin.slice

# List all units in a slice
systemctl list-units --type=service --state=running | grep hls

# View cgroup metrics (for Prometheus)
cat /sys/fs/cgroup/hls-origin.slice/memory.current
cat /sys/fs/cgroup/hls-origin.slice/cpu.stat
```

### Integration with Prometheus

The node exporter automatically collects cgroup metrics:

```promql
# CPU usage by slice
rate(node_cgroup_cpu_usage_usec{slice=~"hls.*"}[1m])

# Memory usage by slice
node_cgroup_memory_usage_bytes{slice=~"hls.*"}

# Compare nginx vs ffmpeg resource usage
sum by (slice) (rate(node_cgroup_cpu_usage_usec{slice=~"hls.*"}[1m]))
```

---

## Proposed Hardening

### Phase 1: Comprehensive Hardening

Apply all standard hardening options:

```nix
systemd.services.hls-generator.serviceConfig = {
  # ═══════════════════════════════════════════════════════════════════════
  # Identity (keep static user for nginx compatibility)
  # ═══════════════════════════════════════════════════════════════════════
  User = "hls";
  Group = "hls";

  # ═══════════════════════════════════════════════════════════════════════
  # Filesystem Isolation
  # ═══════════════════════════════════════════════════════════════════════
  ProtectSystem = "strict";     # Read-only filesystem
  ProtectHome = true;           # No access to /home, /root
  PrivateTmp = true;            # Private /tmp
  ReadWritePaths = [ "/var/hls" ];  # Only write to HLS output

  # ═══════════════════════════════════════════════════════════════════════
  # Network Isolation (FFmpeg doesn't need network!)
  # ═══════════════════════════════════════════════════════════════════════
  PrivateNetwork = true;        # Completely isolated network namespace
  RestrictAddressFamilies = "none";  # No sockets at all

  # Note: If FFmpeg needs to fetch remote sources in the future,
  # change to: RestrictAddressFamilies = [ "AF_INET" "AF_INET6" ];
  # and remove PrivateNetwork

  # ═══════════════════════════════════════════════════════════════════════
  # Device Access
  # ═══════════════════════════════════════════════════════════════════════
  PrivateDevices = true;        # No access to physical devices
  DeviceAllow = [
    "/dev/null rw"
    "/dev/zero rw"
    "/dev/urandom r"            # For random number generation
  ];

  # ═══════════════════════════════════════════════════════════════════════
  # Capability Restrictions
  # ═══════════════════════════════════════════════════════════════════════
  CapabilityBoundingSet = "";   # No capabilities needed
  AmbientCapabilities = "";     # No ambient capabilities
  NoNewPrivileges = true;       # Cannot gain privileges

  # ═══════════════════════════════════════════════════════════════════════
  # Kernel Protections
  # ═══════════════════════════════════════════════════════════════════════
  ProtectKernelTunables = true;
  ProtectKernelModules = true;
  ProtectKernelLogs = true;
  ProtectControlGroups = true;
  ProtectClock = true;
  ProtectHostname = true;

  # ═══════════════════════════════════════════════════════════════════════
  # Process Isolation
  # ═══════════════════════════════════════════════════════════════════════
  ProtectProc = "invisible";    # Hide other processes
  ProcSubset = "pid";           # Minimal /proc access

  # ═══════════════════════════════════════════════════════════════════════
  # Namespace Restrictions
  # ═══════════════════════════════════════════════════════════════════════
  RestrictNamespaces = true;    # Cannot create any namespaces
  PrivateUsers = true;          # User namespace isolation
  RestrictSUIDSGID = true;      # Cannot create SUID/SGID files

  # ═══════════════════════════════════════════════════════════════════════
  # Execution Restrictions
  # ═══════════════════════════════════════════════════════════════════════
  LockPersonality = true;
  RestrictRealtime = true;
  MemoryDenyWriteExecute = true;  # No JIT (FFmpeg doesn't need it)

  # ═══════════════════════════════════════════════════════════════════════
  # System Call Filtering
  # ═══════════════════════════════════════════════════════════════════════
  SystemCallArchitectures = "native";
  SystemCallFilter = [
    "@system-service"
    "~@privileged"
    "~@resources"
    "~@mount"
    "~@debug"
    "~@module"
    "~@reboot"
    "~@swap"
    "~@obsolete"
    "~@cpu-emulation"
    "~@clock"                   # FFmpeg uses gettimeofday, not settime
    "~@raw-io"
  ];
  SystemCallErrorNumber = "EPERM";

  # ═══════════════════════════════════════════════════════════════════════
  # IPC and Miscellaneous
  # ═══════════════════════════════════════════════════════════════════════
  RemoveIPC = true;             # Clean up IPC on service stop

  # ═══════════════════════════════════════════════════════════════════════
  # File Permissions
  # ═══════════════════════════════════════════════════════════════════════
  UMask = "0022";               # Files readable by nginx (world-readable)

    # ═══════════════════════════════════════════════════════════════════════
    # Resource Limits
    # Based on actual measurements: ~68MB RAM, ~9.4% CPU (ultrafast 720p)
    # ═══════════════════════════════════════════════════════════════════════
    MemoryMax = "256M";           # 3.7x actual usage (68MB) - generous headroom
    MemoryHigh = "200M";          # Warn before hard limit
    CPUQuota = "50%";             # 5x actual usage (9.4%) - allows encoding peaks
    LimitNOFILE = 256;            # Only needs HLS segment files
    LimitNPROC = 64;              # FFmpeg spawns threads, not processes
};
```

### Resource Analysis

The resource limits are based on **actual measurements** of FFmpeg running in the VM with the `ultrafast` preset at 720p:

```bash
# Measure FFmpeg resource usage
ssh das@10.177.0.10 'ps -p $(pgrep ffmpeg) -o pid,%cpu,%mem,rss,vsz --no-headers'
#     508  9.4  1.7 68588 1176060
#          ^^^  ^^^  ^^^^^
#          CPU  MEM  RSS (KB)
```

| Metric | Actual | Limit Set | Headroom |
|--------|--------|-----------|----------|
| **RAM (RSS)** | ~68 MB | 256 MB | 3.7× |
| **CPU** | ~9.4% | 50% | 5× |
| **Virtual Memory** | 1.1 GB | (not limited) | N/A |

**Why these specific limits?**

1. **256MB RAM**: FFmpeg uses ~68MB for libx264 encoding buffers. The 3.7× headroom accounts for:
   - Encoding spikes during keyframe generation
   - GOP buffer (60 frames at 720p ≈ 50MB raw)
   - Audio encoding buffers

2. **50% CPU**: The `ultrafast` preset minimizes CPU usage (~9.4%). The 5× headroom:
   - Allows encoding to burst briefly if needed
   - Leaves 350% CPU (3.5 cores) for nginx and system

3. **256 file descriptors**: FFmpeg only needs:
   - 2 input file descriptors (lavfi sources)
   - ~15 output files (10 segments + playlist + temp files)
   - Standard fds (stdin/stdout/stderr, libs)

**VM Resource Budget (4 cores, 4GB RAM):**

| Component | RAM | CPU | Notes |
|-----------|-----|-----|-------|
| **hls-generator** | 256 MB | 50% | Measured + headroom |
| **nginx** | 2 GB | 200% | Main load testing target |
| **System** | 300 MB | 50% | systemd, sshd, exporters |
| **Buffer** | 1.4 GB | 100% | Available for peaks |

### Why Static User Instead of DynamicUser?

Unlike nginx, we **cannot** use `DynamicUser` for hls-generator because:

1. **Nginx needs to read `/var/hls`** — nginx runs as a DynamicUser and reads via `BindReadOnlyPaths`
2. **File ownership matters** — If hls-generator used DynamicUser, the UID would change each restart
3. **Mode 0755 requirement** — We already set `/var/hls` world-readable for nginx; static `hls` user maintains consistent ownership

The trade-off:
- DynamicUser (nginx) = UID changes each start = unpredictable to attackers
- Static user (hls) = Consistent UID = necessary for file sharing

This is acceptable because:
- hls-generator has **no network exposure**
- It's isolated by `PrivateNetwork = true`
- The user only has access to `/var/hls`

### PrivateNetwork = true — Key Security Win

Unlike nginx, FFmpeg in this configuration generates test patterns locally (using `lavfi` filters). It does **not** need network access.

```nix
# FFmpeg command uses local sources only:
# -f lavfi -i smptebars=size=1280x720:rate=30
# -f lavfi -i sine=frequency=1000:sample_rate=48000

PrivateNetwork = true;           # Complete network isolation
RestrictAddressFamilies = "none";  # No sockets allowed
```

This **eliminates the entire network attack surface** (0.5 + 0.3 + 0.2 + 0.1 = 1.1 points).

> **Note:** If you modify the service to fetch remote streams, you'll need to change these settings.

## Expected Improvements

| Setting | Before | After | Savings |
|---------|--------|-------|---------|
| PrivateNetwork= | ✗ 0.5 | ✓ 0.0 | 0.5 |
| RestrictAddressFamilies | ✗ 1.0 | ✓ 0.0 | 1.0 |
| SystemCallFilter (all) | ✗ 2.2 | ✓ 0.0 | 2.2 |
| CapabilityBoundingSet | ✗ ~3.0 | ✓ 0.0 | 3.0 |
| RestrictNamespaces | ✗ 1.0 | ✓ 0.0 | 1.0 |
| PrivateDevices | ✗ 0.2 | ✓ 0.0 | 0.2 |
| ProtectProc/ProcSubset | ✗ 0.3 | ✓ 0.0 | 0.3 |
| RemoveIPC | ✗ 0.1 | ✓ 0.0 | 0.1 |
| UMask | ✗ 0.1 | ✓ 0.0 | 0.1 |
| **Remaining** | | | ~0.3 |
| **Total** | **8.3** | **~1.5** | **~6.8** |

The remaining ~0.3 comes from:
- `RootDirectory=` (0.1) — NixOS uses Nix store
- `User=/DynamicUser=` shows as static user (acceptable)

## Complete NixOS Configuration

```nix
# nix/test-origin/nixos-module.nix

# ═══════════════════════════════════════════════════════════════════════════════
# Systemd Slices for Resource Isolation and Monitoring
# ═══════════════════════════════════════════════════════════════════════════════
systemd.slices = {
  # Parent slice for all HLS origin services
  hls-origin = {
    description = "HLS Origin Services (FFmpeg + Nginx)";
    sliceConfig = {
      MemoryMax = "3G";          # Leave 1GB for system
      MemoryHigh = "2560M";      # Warn at 2.5GB
    };
  };

  # Child slice for FFmpeg encoding
  hls-generator = {
    description = "HLS Generator (FFmpeg encoding)";
    sliceConfig = {
      Slice = "hls-origin.slice";
    };
  };

  # Child slice for Nginx serving
  hls-nginx = {
    description = "HLS Nginx (HTTP serving)";
    sliceConfig = {
      Slice = "hls-origin.slice";
    };
  };
};

# ═══════════════════════════════════════════════════════════════════════════════
# HLS Generator Service
# ═══════════════════════════════════════════════════════════════════════════════
systemd.services.hls-generator = {
  description = "FFmpeg HLS Test Stream Generator (${config._profile.name} profile)";
  after = [ "network.target" "var-hls.mount" ];
  requires = [ "var-hls.mount" ];
  wantedBy = [ "multi-user.target" ];

  serviceConfig = {
    Type = "simple";
    ExecStart = "${pkgs.ffmpeg-full}/bin/ffmpeg ${lib.concatStringsSep " " (map lib.escapeShellArg ffmpegArgs)}";
    Restart = "always";
    RestartSec = 2;

    # ─────────────────────────────────────────────────────────────────────
    # Slice Assignment (for resource isolation and monitoring)
    # ─────────────────────────────────────────────────────────────────────
    Slice = "hls-generator.slice";

    # ─────────────────────────────────────────────────────────────────────
    # Identity
    # ─────────────────────────────────────────────────────────────────────
    User = "hls";
    Group = "hls";

    # ─────────────────────────────────────────────────────────────────────
    # Filesystem Isolation
    # ─────────────────────────────────────────────────────────────────────
    ProtectSystem = "strict";
    ProtectHome = true;
    PrivateTmp = true;
    ReadWritePaths = [ "/var/hls" ];

    # ─────────────────────────────────────────────────────────────────────
    # Network Isolation (FFmpeg doesn't need network for test patterns)
    # ─────────────────────────────────────────────────────────────────────
    PrivateNetwork = true;
    RestrictAddressFamilies = "none";

    # ─────────────────────────────────────────────────────────────────────
    # Device Access
    # ─────────────────────────────────────────────────────────────────────
    PrivateDevices = true;
    DeviceAllow = [
      "/dev/null rw"
      "/dev/zero rw"
      "/dev/urandom r"
    ];

    # ─────────────────────────────────────────────────────────────────────
    # Capabilities
    # ─────────────────────────────────────────────────────────────────────
    CapabilityBoundingSet = "";
    AmbientCapabilities = "";
    NoNewPrivileges = true;

    # ─────────────────────────────────────────────────────────────────────
    # Kernel Protections
    # ─────────────────────────────────────────────────────────────────────
    ProtectKernelTunables = true;
    ProtectKernelModules = true;
    ProtectKernelLogs = true;
    ProtectControlGroups = true;
    ProtectClock = true;
    ProtectHostname = true;

    # ─────────────────────────────────────────────────────────────────────
    # Process Isolation
    # ─────────────────────────────────────────────────────────────────────
    ProtectProc = "invisible";
    ProcSubset = "pid";
    PrivateUsers = true;

    # ─────────────────────────────────────────────────────────────────────
    # Namespace Restrictions
    # ─────────────────────────────────────────────────────────────────────
    RestrictNamespaces = true;
    RestrictSUIDSGID = true;
    RemoveIPC = true;

    # ─────────────────────────────────────────────────────────────────────
    # Execution Restrictions
    # ─────────────────────────────────────────────────────────────────────
    LockPersonality = true;
    RestrictRealtime = true;
    MemoryDenyWriteExecute = true;

    # ─────────────────────────────────────────────────────────────────────
    # System Call Filtering
    # ─────────────────────────────────────────────────────────────────────
    SystemCallArchitectures = "native";
    SystemCallFilter = [
      "@system-service"
      "~@privileged"
      "~@resources"
      "~@mount"
      "~@debug"
      "~@module"
      "~@reboot"
      "~@swap"
      "~@obsolete"
      "~@cpu-emulation"
      "~@clock"
      "~@raw-io"
    ];
    SystemCallErrorNumber = "EPERM";

    # ─────────────────────────────────────────────────────────────────────
    # File Permissions
    # ─────────────────────────────────────────────────────────────────────
    UMask = "0022";

    # ─────────────────────────────────────────────────────────────────────
    # Resource Limits (based on actual measurements)
    # ─────────────────────────────────────────────────────────────────────
    MemoryMax = "256M";
    MemoryHigh = "200M";
    CPUQuota = "50%";
    LimitNOFILE = 256;
    LimitNPROC = 64;
  };
};
```

## Risks and Mitigations

### Risk: FFmpeg Fails to Start

**Symptoms:** Service crashes immediately, no HLS output
**Cause:** System call blocked by filter
**Diagnosis:**
```bash
journalctl -u hls-generator -xe --no-pager | tail -50
```
**Resolution:** Look for "syscall X denied" and either:
- Add specific syscall to allow list
- Remove problematic filter temporarily

### Risk: MemoryDenyWriteExecute Breaks FFmpeg

**Symptoms:** FFmpeg crashes during encoding
**Cause:** Some codecs use JIT compilation
**Note:** Our configuration uses `libx264` which does NOT require JIT
**Resolution:** If using other codecs, set `MemoryDenyWriteExecute = false`

### Risk: PrivateNetwork Breaks Future Features

**Symptoms:** Cannot add network stream sources
**Cause:** Network completely isolated
**Resolution:** If you need to fetch remote streams:
```nix
# Replace these settings:
PrivateNetwork = false;  # or remove entirely
RestrictAddressFamilies = [ "AF_INET" "AF_INET6" ];
```

### Risk: Resource Limits Too Restrictive

**Symptoms:** FFmpeg killed for memory, encoding too slow
**Cause:** Limits based on 720p `ultrafast` encoding measurements
**Resolution:** Adjust for your use case:
```nix
# Current limits (720p ultrafast):
MemoryMax = "256M";   # 3.7x measured ~68MB
CPUQuota = "50%";     # 5x measured ~9.4%

# For 1080p medium preset:
MemoryMax = "512M";
CPUQuota = "100%";

# For 4K encoding:
MemoryMax = "1G";
CPUQuota = "200%";
```

**Note:** If you hit memory limits, check `journalctl -u hls-generator` for OOM messages.

## Implementation Steps

### Step 1: Baseline Measurement

```bash
ssh das@10.177.0.10 'systemd-analyze security hls-generator'
# Record score (currently 8.3)
```

### Step 2: Apply Hardening

Update `nix/test-origin/nixos-module.nix` with the complete configuration above.

### Step 3: Rebuild and Test

```bash
make microvm-reset-full
make network-setup
make microvm-start-tap
```

### Step 4: Verify Service Works

```bash
# Check service status
ssh das@10.177.0.10 'systemctl status hls-generator'

# Verify HLS output
curl http://10.177.0.10:17080/stream.m3u8

# Check for errors
ssh das@10.177.0.10 'journalctl -u hls-generator -n 20 --no-pager'
```

### Step 5: Measure New Score

```bash
ssh das@10.177.0.10 'systemd-analyze security hls-generator'
# Expected: ~1.5-2.0 OK
```

## Comparison: nginx vs hls-generator

| Aspect | nginx | hls-generator |
|--------|-------|---------------|
| **Network** | Required (web server) | Not needed |
| **PrivateNetwork** | ❌ Cannot use | ✅ Can use |
| **DynamicUser** | ✅ Used | ❌ Static user |
| **File Access** | Read `/var/hls` | Write `/var/hls` |
| **Attack Surface** | HTTP requests | None (local only) |
| **Starting Score** | 1.6 | 8.3 |
| **Final Score** | **1.1 OK** | **0.4 SAFE** |

## References

- [NGINX_SECURITY.md](NGINX_SECURITY.md) — Companion document for nginx hardening
- [systemd.exec(5)](https://www.freedesktop.org/software/systemd/man/systemd.exec.html) — Security options
- [systemd-analyze security](https://www.freedesktop.org/software/systemd/man/systemd-analyze.html)

## Appendix: Full systemd-analyze Output (Before)

<details>
<summary>Current hls-generator security analysis (score 8.3)</summary>

```
✗ RemoveIPC= (0.1)
✗ RootDirectory=/RootImage= (0.1)
✓ User=/DynamicUser= - Static non-root user
✗ CapabilityBoundingSet=~CAP_SYS_TIME (0.2)
✓ NoNewPrivileges=
✓ AmbientCapabilities=
✗ PrivateDevices= (0.2)
✗ ProtectClock= (0.2)
✗ CapabilityBoundingSet=~CAP_SYS_PACCT (0.1)
✗ CapabilityBoundingSet=~CAP_KILL (0.1)
✗ ProtectKernelLogs= (0.2)
✗ CapabilityBoundingSet=~CAP_WAKE_ALARM (0.1)
✗ CapabilityBoundingSet=~CAP_(DAC_*|FOWNER|IPC_OWNER) (0.2)
✗ ProtectControlGroups= (0.2)
✗ CapabilityBoundingSet=~CAP_LINUX_IMMUTABLE (0.1)
✗ CapabilityBoundingSet=~CAP_IPC_LOCK (0.1)
✗ ProtectKernelModules= (0.2)
✗ CapabilityBoundingSet=~CAP_SYS_MODULE (0.2)
✗ CapabilityBoundingSet=~CAP_BPF (0.1)
✗ CapabilityBoundingSet=~CAP_SYS_TTY_CONFIG (0.1)
✗ CapabilityBoundingSet=~CAP_SYS_BOOT (0.1)
✗ CapabilityBoundingSet=~CAP_SYS_CHROOT (0.1)
✗ SystemCallArchitectures= (0.2)
✗ CapabilityBoundingSet=~CAP_BLOCK_SUSPEND (0.1)
✗ MemoryDenyWriteExecute= (0.1)
✗ RestrictNamespaces=~user (0.3)
✗ RestrictNamespaces=~pid (0.1)
✗ RestrictNamespaces=~net (0.1)
✗ RestrictNamespaces=~uts (0.1)
✗ RestrictNamespaces=~mnt (0.1)
✗ CapabilityBoundingSet=~CAP_LEASE (0.1)
✗ CapabilityBoundingSet=~CAP_MKNOD (0.1)
✗ RestrictNamespaces=~cgroup (0.1)
✗ RestrictSUIDSGID= (0.2)
✗ RestrictNamespaces=~ipc (0.1)
✗ ProtectHostname= (0.1)
✗ CapabilityBoundingSet=~CAP_(CHOWN|FSETID|SETFCAP) (0.2)
✗ CapabilityBoundingSet=~CAP_SET(UID|GID|PCAP) (0.3)
✗ LockPersonality= (0.1)
✗ ProtectKernelTunables= (0.2)
✗ RestrictAddressFamilies=~AF_PACKET (0.2)
✗ RestrictAddressFamilies=~AF_NETLINK (0.1)
✗ RestrictAddressFamilies=~AF_UNIX (0.1)
✗ RestrictAddressFamilies=~… (0.3)
✗ RestrictAddressFamilies=~AF_(INET|INET6) (0.3)
✗ CapabilityBoundingSet=~CAP_MAC_* (0.1)
✗ RestrictRealtime= (0.1)
✓ ProtectSystem= - strict read-only
✗ CapabilityBoundingSet=~CAP_SYS_RAWIO (0.2)
✗ CapabilityBoundingSet=~CAP_SYS_PTRACE (0.3)
✗ CapabilityBoundingSet=~CAP_SYS_(NICE|RESOURCE) (0.1)
✓ SupplementaryGroups=
✗ DeviceAllow= (0.2)
✓ PrivateTmp=
✓ ProtectHome=
✗ CapabilityBoundingSet=~CAP_NET_ADMIN (0.2)
✗ ProtectProc= (0.2)
✗ ProcSubset= (0.1)
✗ CapabilityBoundingSet=~CAP_NET_(BIND_SERVICE|BROADCAST|RAW) (0.1)
✗ CapabilityBoundingSet=~CAP_AUDIT_* (0.1)
✗ CapabilityBoundingSet=~CAP_SYS_ADMIN (0.3)
✗ PrivateNetwork= (0.5)
✗ PrivateUsers= (0.2)
✗ CapabilityBoundingSet=~CAP_SYSLOG (0.1)
✓ KeyringMode=
✓ Delegate=
✗ SystemCallFilter=~@clock (0.2)
✗ SystemCallFilter=~@cpu-emulation (0.1)
✗ SystemCallFilter=~@debug (0.2)
✗ SystemCallFilter=~@module (0.2)
✗ SystemCallFilter=~@mount (0.2)
✗ SystemCallFilter=~@obsolete (0.1)
✗ SystemCallFilter=~@privileged (0.2)
✗ SystemCallFilter=~@raw-io (0.2)
✗ SystemCallFilter=~@reboot (0.2)
✗ SystemCallFilter=~@resources (0.2)
✗ SystemCallFilter=~@swap (0.2)
✗ IPAddressDeny= (0.2)
✓ NotifyAccess=
✓ PrivateMounts=
✗ UMask= (0.1)

→ Overall exposure level: 8.3 EXPOSED 🙁
```

</details>

---

## Implementation Summary

### ✅ Hardening Complete

The full security hardening was implemented on **January 22, 2026**.

### Security Score Results

| Metric | Before | After | Change |
|--------|--------|-------|--------|
| **Score** | 8.3 EXPOSED 🙁 | **0.4 SAFE 😊** | **95% improvement** |
| **Target** | — | ~1.5-2.0 | **Exceeded** |

### Key Achievements

| Security Feature | Status | Impact |
|------------------|--------|--------|
| **PrivateNetwork = true** | ✅ | Eliminates entire network attack surface |
| **RestrictAddressFamilies = "none"** | ✅ | Cannot allocate any sockets |
| **All syscall filters** | ✅ | 11 categories blocked |
| **CapabilityBoundingSet = ""** | ✅ | Zero capabilities |
| **All kernel protections** | ✅ | Clock, modules, tunables, logs, etc. |
| **PrivateUsers = true** | ✅ | User namespace isolation |
| **PrivateDevices = true** | ✅ | No hardware access |
| **ProtectProc = "invisible"** | ✅ | Hidden from other processes |
| **RestrictNamespaces = true** | ✅ | Cannot create namespaces |
| **MemoryDenyWriteExecute = true** | ✅ | No JIT/writable+executable memory |

### Remaining Warnings (Acceptable)

Only 0.4 points remain, all acceptable trade-offs:

| Warning | Score | Reason |
|---------|-------|--------|
| `RootDirectory=` | 0.1 | NixOS architecture uses `/nix/store` |
| `DeviceAllow` | 0.1 | Only allows `/dev/null`, `/dev/zero`, `/dev/urandom` |
| `IPAddressDeny=` | 0.2 | Not needed — `PrivateNetwork=true` already isolates |
| `UMask=` | 0.1 | Set to 0022, systemd reports differently |

### Resource Usage (Verified)

```bash
$ systemd-cgtop -b -n 1 --depth=3 | grep hls
hls.slice                                    79.5M
hls.slice/hls-generator.slice                74.9M (limit: 256M)
hls.slice/hls-nginx.slice                     4.5M (limit: 2G)
```

### Service Status (Verified)

```bash
$ systemctl status hls-generator
● hls-generator.service - FFmpeg HLS Test Stream Generator (tap profile)
     Active: active (running)
     Memory: 74.9M (high: 200M, max: 256M)
     CGroup: /hls.slice/hls-generator.slice/hls-generator.service
```

### HLS Stream (Verified)

```bash
$ curl -s http://10.177.0.10:17080/stream.m3u8 | head -4
#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:46
```

### Comparison with nginx

| Service | Before | After | Key Difference |
|---------|--------|-------|----------------|
| **hls-generator** | 8.3 EXPOSED | **0.4 SAFE** | `PrivateNetwork=true` (no network needed) |
| **nginx** | 1.6 OK | **1.1 OK** | Must have network access |

The hls-generator achieves a **better score** than nginx because it can use `PrivateNetwork=true` — FFmpeg generates test patterns locally and has no need for network access whatsoever.
