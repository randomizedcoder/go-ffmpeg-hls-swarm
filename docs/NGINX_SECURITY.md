# Nginx Security Hardening Design

This document outlines the security hardening plan for nginx in the HLS Origin MicroVM.

## Implementation Status

| Metric | Before | After | Notes |
|--------|--------|-------|-------|
| **Security Score** | 1.6 OK 🙂 | **1.1 OK 🙂** | ✅ Improved by 31% |
| **DynamicUser** | ❌ Static nginx user | ✅ Transient UID | User doesn't exist when stopped |
| **PrivateUsers** | ❌ | ✅ | User namespace isolation |
| **ProtectSystem** | partial | **strict** | Read-only filesystem |
| **SystemCallFilter** | basic | **@system-service ~@privileged ~@resources** | Enhanced filtering |

### What We Achieved

- **DynamicUser isolation** — Nginx runs as a transient user that doesn't exist when the service stops
- **Read-only filesystem** — `ProtectSystem = "strict"` with explicit write paths
- **Explicit HLS access** — `BindReadOnlyPaths = ["/var/hls"]` enforces read-only access
- **Capability reduction** — `CapabilityBoundingSet = ""` (no capabilities needed for port 17080)
- **Syscall filtering** — Blocks `@privileged`, `@resources`, `@mount`, `@debug`, etc.

### Caveats & Accepted Trade-offs

| Warning | Score Impact | Why We Accept It |
|---------|--------------|------------------|
| `RootDirectory=/RootImage=` | 0.1 | NixOS stores nginx in `/nix/store`; chroot would require complex bind mounts |
| `RestrictAddressFamilies=~AF_UNIX` | 0.1 | Required for nginx worker communication |
| `RestrictAddressFamilies=~AF_(INET|INET6)` | 0.3 | Required — it's a web server |
| `PrivateNetwork=` | 0.5 | Required — it's a web server |
| `DeviceAllow=` | 0.1 | Allows only `/dev/null`, `/dev/zero`, `/dev/urandom` |
| `IPAddressDeny=` | 0.2 | Optional; could break functionality |
| `UMask=` | 0.1 | Set to 0027 but systemd reports default |

### /var/hls World-Readable Requirement

With `DynamicUser = true`, nginx runs as a transient user that **cannot be added to groups**. To allow nginx to read HLS content written by FFmpeg:

```nix
# /var/hls tmpfs uses mode 0755 (world-readable) instead of 0750
fileSystems."/var/hls" = {
  device = "tmpfs";
  fsType = "tmpfs";
  options = [ "size=256M" "uid=hls" "gid=hls" "mode=0755" ];
};
```

This is acceptable because:
1. HLS content is intended to be publicly served anyway
2. The VM is isolated — no other users to hide content from
3. The alternative (static nginx user in hls group) provides weaker isolation

### Resource Limits

Nginx is the **main load testing target** — it needs generous resources while leaving room for FFmpeg and the base system.

**VM Configuration:** 4 CPUs, 4GB RAM (from `TEST_ORIGIN.md` design)

**Resource Budget:**

| Component | RAM | CPU | Notes |
|-----------|-----|-----|-------|
| **nginx** | 2 GB | 300% | Main load testing target — handles 10k+ connections |
| **hls-generator** | 256 MB | 50% | Measured: ~68MB/9.4% actual (see `HLS_GENERATOR_SECURITY.md`) |
| **System** | 300 MB | 50% | systemd, sshd, exporters, kernel |
| **Buffer** | 1.4 GB | 100% | Available for peaks |

**Why 2GB for nginx?**
- Serving HLS from tmpfs is memory-efficient (~2-5KB per connection)
- 10k connections × 5KB = 50MB for connection buffers
- `open_file_cache max=10000` caches file handles, not content
- 2GB allows nginx to scale well beyond 10k connections
- Nginx serving static files typically uses 100-500MB; 2GB provides 4-20× headroom

**Why 300% CPU for nginx?**
- HLS serving is I/O-bound, not CPU-bound
- Allows 3 cores for request handling under heavy load
- Leaves 50% (0.5 core) for FFmpeg encoding
- Leaves 50% (0.5 core) for system operations

### Systemd Slices for Monitoring

Both nginx and hls-generator run in isolated slices under a parent `hls-origin.slice`:

```
hls-origin.slice (3GB limit)
├── hls-nginx.slice      ← nginx.service
└── hls-generator.slice  ← hls-generator.service
```

**Monitor resource usage:**
```bash
# Real-time per-slice usage
systemd-cgtop

# Example during load test:
# /hls-origin.slice                      15   310%   1.2G
# /hls-origin.slice/hls-nginx.slice      4    300%   1.1G
# /hls-origin.slice/hls-generator.slice  3    10%    68M
```

See [HLS_GENERATOR_SECURITY.md](HLS_GENERATOR_SECURITY.md#systemd-slices-for-resource-isolation) for complete slice configuration.

---

## Design Principles

1. **Use NixOS module options** - Prefer high-level abstractions over raw `serviceConfig`
2. **Leverage systemd isolation** - Use `DynamicUser`, `ProtectSystem`, etc.
3. **Environment-aware** - Use `lib.mkIf` to toggle aggressive hardening
4. **Defense in depth** - Multiple layers of protection
5. **Fail predictably** - Use `SystemCallErrorNumber = "EPERM"`

## Original Security Posture

**Original Score:** 1.6 OK 🙂 (from `systemd-analyze security nginx`)

The NixOS nginx module already provides good baseline security. This document planned additional hardening.

**Target Score:** 0.5-0.7 (excellent for a network service)

**Achieved Score:** 1.1 OK 🙂 — The remaining 0.6 points come from required web server functionality (network access, Unix sockets) and NixOS-specific architecture (Nix store instead of chroot).

## Security Analysis

### Current Issues (from systemd-analyze)

| Issue | Exposure | Required? | Action |
|-------|----------|-----------|--------|
| `SystemCallFilter=~@resources` | 0.2 | No | **Add to filter** |
| `RootDirectory=/RootImage=` | 0.1 | Complex | Skip (requires chroot setup) |
| `AmbientCapabilities=` | 0.1 | No | **Clear ambient caps** |
| `RestrictAddressFamilies=~AF_UNIX` | 0.1 | Yes | Keep (needed for internal sockets) |
| `RestrictAddressFamilies=~AF_(INET|INET6)` | 0.3 | Yes | Keep (web server needs network) |
| `CapabilityBoundingSet=~CAP_SYS_(NICE|RESOURCE)` | 0.1 | No | **Remove capability** |
| `CapabilityBoundingSet=~CAP_NET_(BIND_SERVICE|...)` | 0.1 | Partial | **Remove BROADCAST/RAW** |
| `PrivateNetwork=` | 0.5 | Yes | Keep (web server needs network) |
| `PrivateUsers=` | 0.2 | No | **Enable** |
| `DeviceAllow=` | 0.1 | Partial | **Restrict to null/zero/urandom** |
| `IPAddressDeny=` | 0.2 | Optional | Consider (may break functionality) |
| `UMask=` | 0.1 | No | **Set to 0027** |

### What We Can Harden

These restrictions are safe to add without breaking nginx functionality:

1. **System Call Filtering** - Add `@resources` to deny list
2. **Capability Reduction** - Remove unnecessary capabilities
3. **Device Access** - Restrict to essential devices only
4. **File Permissions** - Stricter UMask
5. **User Isolation** - Enable PrivateUsers
6. **Resource Limits** - Add memory/CPU limits

### What We Must Keep

These are required for nginx to function as a web server:

1. **Network Access** - `AF_INET`, `AF_INET6` (serve HTTP)
2. **Unix Sockets** - `AF_UNIX` (nginx worker communication)
3. **CAP_NET_BIND_SERVICE** - Only if binding to port < 1024 (we use 17080, so **not needed**)

## Proposed Configuration

### Architecture: Modular Hardening

The configuration is split into layers using NixOS idioms:

```
nixos-module.nix
├── services.nginx.*           # Use NixOS module options
├── systemd.services.nginx     # Merged hardening config
│   ├── lib.mkMerge [
│   │   ├── baseHardening      # Always applied (Phase 1)
│   │   ├── lib.mkIf aggressive # Production hardening (Phase 2)
│   │   └── lib.mkIf ipRestrict # IP restrictions (Phase 3)
│   ]
```

### NixOS Module Options (Use First)

Before custom `serviceConfig`, leverage built-in NixOS options:

```nix
services.nginx = {
  enable = true;

  # ═══════════════════════════════════════════════════════════════════════
  # NixOS Nginx Module Options (preferred over raw serviceConfig)
  # ═══════════════════════════════════════════════════════════════════════

  # Enable graceful reload without restart
  enableReload = true;

  # Recommended optimizations (already using these)
  recommendedOptimisation = true;
  recommendedProxySettings = true;

  # For future TLS support
  # recommendedTlsSettings = true;

  # Global HTTP config (cleaner than injecting into systemd)
  commonHttpConfig = ''
    # Hide nginx version in error pages and headers
    server_tokens off;

    # Security headers
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Frame-Options "SAMEORIGIN" always;
  '';
};
```

### Phase 1: Base Hardening (Always Applied)

```nix
# In nixos-module.nix
let
  # Hardening configuration for nginx
  nginxHardening = {
    # ═══════════════════════════════════════════════════════════════════════
    # Resource Limits
    # Nginx is the main load testing target - give it most VM resources
    # VM has 4 CPUs and 4GB RAM; FFmpeg uses ~68MB/10%, system uses ~300MB
    # ═══════════════════════════════════════════════════════════════════════
    MemoryMax = "2G";         # Plenty for 10k+ connections and file cache
    MemoryHigh = "1536M";     # Warn at 1.5GB
    CPUQuota = "300%";        # Allow 3 cores for HLS serving
    LimitNOFILE = 65536;      # High FD limit for many connections
    LimitNPROC = 1024;

    # ═══════════════════════════════════════════════════════════════════════
    # File Permissions
    # ═══════════════════════════════════════════════════════════════════════
    UMask = "0027";           # Stricter file creation mask

    # ═══════════════════════════════════════════════════════════════════════
    # Filesystem Isolation (systemd abstractions)
    # ═══════════════════════════════════════════════════════════════════════
    ProtectSystem = "strict";  # Read-only filesystem except explicit paths
    ProtectHome = true;        # No access to /home, /root, /run/user
    PrivateTmp = true;         # Private /tmp (already set by NixOS)

    # ⚠️  CRITICAL: ReadWritePaths required with ProtectSystem=strict
    # Without these, nginx WILL fail to start!
    ReadWritePaths = [
      "/var/log/nginx"         # Log files (nginx will fail without this)
      "/var/cache/nginx"       # Proxy/fastcgi cache
      "/run/nginx"             # PID file and Unix sockets
      # NOTE: /var/hls is NOT here - nginx only READS from it
    ];

    # /var/hls is readable via:
    # 1. ProtectSystem=strict allows reading by default
    # 2. nginx user is in 'hls' group (configured in users.users.nginx.extraGroups)
    # 3. /var/hls has mode 0750 (owner rwx, group rx)

    # Note: /nix/store is readable by default with ProtectSystem=strict
    # No need for BindReadOnlyPaths unless using RootDirectory

    # ═══════════════════════════════════════════════════════════════════════
    # System Call Filtering
    # ═══════════════════════════════════════════════════════════════════════
    SystemCallFilter = [
      "@system-service"
      "~@privileged"          # Block privilege escalation
      "~@resources"           # Block resource priority changes
      "~@debug"
      "~@mount"
      "~@module"
      "~@reboot"
      "~@swap"
      "~@obsolete"
      "~@cpu-emulation"
    ];
    SystemCallErrorNumber = "EPERM";  # Fail predictably (not ENOSYS)
    SystemCallArchitectures = "native";

    # ═══════════════════════════════════════════════════════════════════════
    # Capability Restrictions
    # ═══════════════════════════════════════════════════════════════════════
    # Nginx on port 17080 doesn't need any special capabilities
    CapabilityBoundingSet = "";       # Empty = no capabilities
    AmbientCapabilities = "";         # No ambient capabilities
    NoNewPrivileges = true;           # Prevent privilege escalation

    # ═══════════════════════════════════════════════════════════════════════
    # Device Access
    # ═══════════════════════════════════════════════════════════════════════
    PrivateDevices = true;            # No access to physical devices
    DeviceAllow = [
      "/dev/null rw"
      "/dev/zero rw"
      "/dev/urandom r"
    ];

    # ═══════════════════════════════════════════════════════════════════════
    # Additional Kernel Protections
    # ═══════════════════════════════════════════════════════════════════════
    ProtectKernelTunables = true;
    ProtectKernelModules = true;
    ProtectKernelLogs = true;
    ProtectControlGroups = true;
    ProtectClock = true;
    ProtectHostname = true;

    # ═══════════════════════════════════════════════════════════════════════
    # Namespace Restrictions
    # ═══════════════════════════════════════════════════════════════════════
    RestrictNamespaces = true;
    RestrictRealtime = true;
    RestrictSUIDSGID = true;
    LockPersonality = true;
    MemoryDenyWriteExecute = true;

    # ═══════════════════════════════════════════════════════════════════════
    # Process Isolation
    # ═══════════════════════════════════════════════════════════════════════
    ProtectProc = "invisible";        # Hide other processes
    ProcSubset = "pid";               # Minimal /proc access
  };
in
{
  # Apply base hardening
  systemd.services.nginx.serviceConfig = nginxHardening;
}
```

### Phase 2: Aggressive Hardening (Production)

Add these for production environments:

```nix
# Toggle with a config option
{ config, lib, ... }:
let
  cfg = config.hlsOrigin;  # Custom module option
in
{
  options.hlsOrigin.aggressiveHardening = lib.mkEnableOption "aggressive nginx hardening";

  config = lib.mkIf cfg.aggressiveHardening {
    systemd.services.nginx.serviceConfig = {
      # ═══════════════════════════════════════════════════════════════════════
      # DynamicUser - Most powerful isolation feature
      # Allocates temporary UID/GID, user doesn't exist when service is off
      # ═══════════════════════════════════════════════════════════════════════
      DynamicUser = true;

      # With DynamicUser, systemd manages these directories automatically
      StateDirectory = "nginx";       # → /var/lib/nginx
      CacheDirectory = "nginx";       # → /var/cache/nginx
      LogsDirectory = "nginx";        # → /var/log/nginx
      RuntimeDirectory = "nginx";     # → /run/nginx

      # ═══════════════════════════════════════════════════════════════════════
      # HLS Content Access - READ-ONLY (enforced by systemd)
      # FFmpeg writes as 'hls' user, nginx reads via bind mount
      # ═══════════════════════════════════════════════════════════════════════
      BindReadOnlyPaths = [ "/var/hls" ];

      # ═══════════════════════════════════════════════════════════════════════
      # User Isolation
      # ═══════════════════════════════════════════════════════════════════════
      PrivateUsers = true;            # Isolated user namespace

      # ═══════════════════════════════════════════════════════════════════════
      # Network Restrictions
      # ═══════════════════════════════════════════════════════════════════════
      RestrictAddressFamilies = [
        "AF_INET"                     # IPv4 (required)
        "AF_INET6"                    # IPv6 (required)
        "AF_UNIX"                     # Unix sockets (worker communication)
      ];
    };
  };
}
```

### Phase 3: IP Address Restrictions (Optional)

For controlled environments:

```nix
{ config, lib, ... }:
let
  cfg = config.hlsOrigin;
in
{
  options.hlsOrigin.restrictIPs = lib.mkEnableOption "restrict nginx to specific IP ranges";

  config = lib.mkIf cfg.restrictIPs {
    systemd.services.nginx.serviceConfig = {
      # Only allow connections from specific networks
      IPAddressAllow = [
        "10.177.0.0/24"               # VM subnet (TAP networking)
        "127.0.0.0/8"                 # Localhost
        "0.0.0.0/0"                   # Allow all (adjust for production)
      ];
      IPAddressDeny = "any";
    };
  };
}
```

**Warning:** IP restrictions may break functionality. Test thoroughly.

### DynamicUser Considerations

`DynamicUser=true` is one of systemd's most powerful isolation features:

**Benefits:**
- Allocates temporary UID/GID when service starts
- User doesn't exist when service is stopped
- Each restart gets a fresh, unpredictable UID
- Prevents persistence attacks
- Attacker can't target a known UID

**Implications for Nginx:**
```nix
# With DynamicUser, you MUST use these directory options:
DynamicUser = true;
StateDirectory = "nginx";      # → /var/lib/nginx (owned by dynamic user)
CacheDirectory = "nginx";      # → /var/cache/nginx
LogsDirectory = "nginx";       # → /var/log/nginx
RuntimeDirectory = "nginx";    # → /run/nginx

# Cannot use:
# - User = "nginx";            (conflicts with DynamicUser)
# - ReadWritePaths with absolute paths (use *Directory instead)
```

**HLS Content Access with DynamicUser:**

Since FFmpeg runs as fixed `hls` user and writes to `/var/hls`, how does DynamicUser nginx read it?

```nix
# Option 1: BindReadOnlyPaths (RECOMMENDED - most explicit)
DynamicUser = true;
BindReadOnlyPaths = [ "/var/hls" ];
# Bind-mounts /var/hls as READ-ONLY into nginx's namespace
# Enforced at systemd level - nginx CANNOT write even if tmpfs allows it

# Option 2: SupplementaryGroups (also works)
DynamicUser = true;
SupplementaryGroups = [ "hls" ];
# Dynamic user is added to 'hls' group at runtime
# Can read /var/hls via group permissions (mode 0750)

# Option 3: World-readable tmpfs (NOT recommended)
# Would require changing /var/hls to mode 0755
# Any process could read HLS content
```

**Recommended Configuration (Maximum Security):**

```nix
# FFmpeg: Fixed user (needs to write)
systemd.services.hls-generator.serviceConfig = {
  User = "hls";
  Group = "hls";
  # ... other hardening ...
};

# Nginx: DynamicUser (only needs to read)
systemd.services.nginx.serviceConfig = {
  DynamicUser = true;

  # Systemd-managed directories (owned by dynamic user)
  StateDirectory = "nginx";
  CacheDirectory = "nginx";
  LogsDirectory = "nginx";
  RuntimeDirectory = "nginx";

  # READ-ONLY access to HLS content (explicit, enforced by systemd)
  BindReadOnlyPaths = [ "/var/hls" ];

  # ... other hardening ...
};
```

**Why This Is More Secure:**

| Aspect | Static User | DynamicUser + BindReadOnlyPaths |
|--------|-------------|--------------------------------|
| UID predictability | Known (nginx) | Random each start |
| Persistence | User exists always | User gone when stopped |
| /var/hls access | Read via group | Read-only enforced by systemd |
| Write to /var/hls | Prevented by mode 0750 | Prevented by BindReadOnlyPaths |
| Attack surface | Standard | Minimal |

**Trade-off:** DynamicUser adds complexity but significantly improves security. For a production HLS origin, this is worth it.

### Complete Integration Example

```nix
# nix/test-origin/nixos-module.nix
{ config, lib, pkgs, ... }:

let
  # Base hardening (always applied)
  baseHardening = {
    # Resource limits (generous for load testing)
    MemoryMax = "2G";
    MemoryHigh = "1536M";
    CPUQuota = "300%";

    # File permissions
    UMask = "0027";

    # Filesystem isolation
    ProtectSystem = "strict";
    ProtectHome = true;
    PrivateTmp = true;

    # ⚠️ CRITICAL: These paths MUST be writable or nginx fails!
    ReadWritePaths = [
      "/var/log/nginx"       # Logs
      "/var/cache/nginx"     # Cache
      "/run/nginx"           # PID/sockets
      # NOTE: /var/hls NOT needed - nginx only READS (via hls group membership)
    ];

    # Capabilities (none needed for port 17080)
    CapabilityBoundingSet = "";
    AmbientCapabilities = "";
    NoNewPrivileges = true;

    # Device access
    PrivateDevices = true;
    DeviceAllow = [ "/dev/null rw" "/dev/zero rw" "/dev/urandom r" ];

    # Kernel protections
    ProtectKernelTunables = true;
    ProtectKernelModules = true;
    ProtectKernelLogs = true;
    ProtectControlGroups = true;
    ProtectClock = true;
    ProtectHostname = true;

    # Namespace/personality restrictions
    RestrictNamespaces = true;
    RestrictRealtime = true;
    RestrictSUIDSGID = true;
    LockPersonality = true;
    MemoryDenyWriteExecute = true;

    # Process isolation
    ProtectProc = "invisible";
    ProcSubset = "pid";

    # System call filtering
    SystemCallFilter = [ "@system-service" "~@privileged" "~@resources" ];
    SystemCallErrorNumber = "EPERM";
    SystemCallArchitectures = "native";
  };

  # Aggressive hardening (production only)
  aggressiveHardening = {
    DynamicUser = true;
    PrivateUsers = true;
    RestrictAddressFamilies = [ "AF_INET" "AF_INET6" "AF_UNIX" ];

    # Systemd-managed directories (required with DynamicUser)
    StateDirectory = "nginx";
    CacheDirectory = "nginx";
    LogsDirectory = "nginx";
    RuntimeDirectory = "nginx";

    # READ-ONLY access to HLS content (enforced by systemd)
    BindReadOnlyPaths = [ "/var/hls" ];
  };
in
{
  services.nginx = {
    enable = true;
    enableReload = true;
    recommendedOptimisation = true;
    commonHttpConfig = ''
      server_tokens off;
    '';
    # ... virtualHosts config ...
  };

  # Merge hardening configurations
  systemd.services.nginx.serviceConfig = lib.mkMerge [
    baseHardening
    # Uncomment for production:
    # aggressiveHardening
  ];
}
```

## Implementation Strategy

### Step 1: Baseline Measurement

```bash
# Before changes
ssh das@10.177.0.10 'systemd-analyze security nginx'
# Record score (currently 1.6)

# Save full output for comparison
ssh das@10.177.0.10 'systemd-analyze security nginx' > nginx-security-before.txt
```

### Step 2: Apply NixOS Module Options

Update `nixos-module.nix` with high-level nginx options:

```nix
services.nginx = {
  enableReload = true;
  commonHttpConfig = ''
    server_tokens off;
  '';
};
```

### Step 3: Apply Phase 1 Hardening

1. Add `baseHardening` configuration to `nixos-module.nix`
2. Rebuild VM:
   ```bash
   make microvm-reset-full && make network-setup && make microvm-start-tap
   ```
3. Verify nginx still works:
   ```bash
   curl http://10.177.0.10:17080/health
   curl http://10.177.0.10:17080/stream.m3u8
   curl http://10.177.0.10:9113/metrics
   ```
4. Check new score:
   ```bash
   ssh das@10.177.0.10 'systemd-analyze security nginx'
   ```
5. If failures occur, check logs:
   ```bash
   ssh das@10.177.0.10 'journalctl -u nginx -xe --no-pager | tail -50'
   ```

### Step 4: (Optional) Apply Phase 2 Hardening

Only if Phase 1 is stable and you want maximum security:

1. Enable aggressive hardening options
2. Test thoroughly (DynamicUser may affect file permissions)
3. Consider trade-offs vs. complexity

### Step 5: Document Final Configuration

```bash
# After changes
ssh das@10.177.0.10 'systemd-analyze security nginx' > nginx-security-after.txt

# Compare
diff nginx-security-before.txt nginx-security-after.txt
```

### Rollback Strategy

If hardening breaks nginx:

1. Comment out problematic settings in `nixos-module.nix`
2. Rebuild: `make microvm-reset-full && make network-setup && make microvm-start-tap`
3. Binary search: enable half the settings, test, repeat

## Expected Improvements

| Setting | Current | Phase 1 | Phase 2 |
|---------|---------|---------|---------|
| SystemCallFilter=~@resources | ✗ 0.2 | ✓ 0.0 | ✓ 0.0 |
| SystemCallFilter=~@privileged | ✓ | ✓ 0.0 | ✓ 0.0 |
| AmbientCapabilities= | ✗ 0.1 | ✓ 0.0 | ✓ 0.0 |
| CapabilityBoundingSet (all) | ✗ 0.2 | ✓ 0.0 | ✓ 0.0 |
| DeviceAllow= | ✗ 0.1 | ✓ 0.0 | ✓ 0.0 |
| UMask= | ✗ 0.1 | ✓ 0.0 | ✓ 0.0 |
| ProtectSystem=strict | partial | ✓ 0.0 | ✓ 0.0 |
| ProtectHome= | ✓ | ✓ 0.0 | ✓ 0.0 |
| ProtectProc=invisible | partial | ✓ 0.0 | ✓ 0.0 |
| ProcSubset=pid | partial | ✓ 0.0 | ✓ 0.0 |
| PrivateUsers= | ✗ 0.2 | ✗ 0.2 | ✓ 0.0 |
| DynamicUser= | ✗ | ✗ | ✓ 0.0 |
| RestrictAddressFamilies= | ✗ 0.4 | ✗ 0.4 | ✓ 0.0 |
| **Estimated Score** | **1.6** | **~0.8** | **~0.5** |

### Score Breakdown

- **Current (1.6):** Good baseline from NixOS nginx module
- **Phase 1 (~0.8):** Comprehensive hardening without breaking functionality
- **Phase 2 (~0.5):** Production-grade with DynamicUser and full isolation

## Risks and Mitigations

### Risk: Nginx Fails to Start

**Symptoms:** Service fails, workers crash
**Cause:** System call blocked by filter
**Mitigation:** Check `journalctl -u nginx -xe` for denied syscalls
**Resolution:**
- Look for `syscall X denied` messages
- Add specific syscall to allow list or remove from deny list
- `SystemCallErrorNumber = "EPERM"` ensures predictable failures

### Risk: File Access Denied

**Symptoms:** 403 errors, can't read HLS files
**Cause:** ProtectSystem=strict blocking writes
**Mitigation:** Ensure ReadWritePaths includes all necessary directories
**Resolution:**
- Add `/var/hls` to ReadWritePaths
- Check nginx error log: `journalctl -u nginx -f`

### Risk: DynamicUser Breaks File Ownership

**Symptoms:** Permission denied on startup, can't write logs
**Cause:** DynamicUser changes UID/GID each restart
**Mitigation:** Use StateDirectory/CacheDirectory/LogsDirectory
**Resolution:**
```nix
DynamicUser = true;
StateDirectory = "nginx";
CacheDirectory = "nginx";
LogsDirectory = "nginx";
```
- Systemd manages ownership automatically
- Paths become `/var/lib/nginx`, `/var/cache/nginx`, `/var/log/nginx`

### Risk: Network Communication Fails

**Symptoms:** Can't bind to port, can't serve requests
**Cause:** RestrictAddressFamilies too restrictive
**Mitigation:** Don't enable PrivateNetwork (we're not doing this)
**Resolution:** Ensure RestrictAddressFamilies includes:
- `AF_INET` (IPv4)
- `AF_INET6` (IPv6)
- `AF_UNIX` (worker communication)

### Risk: Worker Communication Fails

**Symptoms:** Workers crash, signals not received
**Cause:** AF_UNIX blocked
**Mitigation:** Keep AF_UNIX in RestrictAddressFamilies
**Resolution:** Don't restrict Unix sockets

### Risk: PrivateUsers Breaks User Lookup

**Symptoms:** "No such file or directory" for user operations
**Cause:** Can't access /etc/passwd, /etc/group
**Mitigation:** Nginx doesn't need user lookups after startup
**Resolution:** Should work, but if not, set `PrivateUsers = false`

### Risk: ProtectSystem=strict Blocks Log Writes

**Symptoms:** Nginx fails to start, "Permission denied" for log files
**Cause:** `ProtectSystem = "strict"` makes filesystem read-only
**Mitigation:** Must explicitly allow write paths
**Resolution:**
```nix
ProtectSystem = "strict";
ReadWritePaths = [
  "/var/log/nginx"    # REQUIRED for logging
  "/var/cache/nginx"  # REQUIRED for proxy cache
  "/run/nginx"        # REQUIRED for PID/socket files
];
# NOTE: /var/hls is NOT needed in ReadWritePaths
# - Nginx only READS HLS files (FFmpeg writes them)
# - ProtectSystem=strict allows reading by default
# - Nginx is in 'hls' group for directory access
```

**Important:** If using `LogsDirectory = "nginx"` with `DynamicUser`, systemd manages this automatically. But with a static user, you must use `ReadWritePaths`.

### Risk: Nix Store Access Blocked

**Symptoms:** Nginx binary not found, library loading fails
**Cause:** Custom `RootDirectory` or `MountFlags` blocking `/nix/store`
**Mitigation:**
- `ProtectSystem = "strict"` allows `/nix/store` access by default ✓
- Don't use `RootDirectory` or `RootImage` (we're not)
- Don't use `MountFlags = private` without careful testing
**Resolution:**
```nix
# If you must use RootDirectory, bind-mount the nix store:
BindReadOnlyPaths = [ "/nix/store" ];

# Or simply avoid RootDirectory for NixOS services
# (it's complex and rarely needed)
```

**Note:** The NixOS nginx module does NOT use `RootDirectory`, so this risk is low for our configuration. The `systemd-analyze security` warning about `RootDirectory` (0.1 exposure) is acceptable.

## Testing Checklist

After applying hardening, verify:

- [ ] `curl http://10.177.0.10:17080/health` returns OK
- [ ] `curl http://10.177.0.10:17080/stream.m3u8` returns playlist
- [ ] `curl http://10.177.0.10:17080/files/` returns JSON listing
- [ ] `curl http://10.177.0.10:9113/metrics` returns nginx metrics
- [ ] `systemctl status nginx` shows active (running)
- [ ] `journalctl -u nginx` shows no errors
- [ ] `systemd-analyze security nginx` shows improved score

## Related Documents

- [HLS_GENERATOR_SECURITY.md](HLS_GENERATOR_SECURITY.md) — Security hardening for the FFmpeg hls-generator service
- [SECURITY.md](SECURITY.md) — General security considerations for go-ffmpeg-hls-swarm

## References

### systemd Documentation
- [systemd.exec(5) - Security options](https://www.freedesktop.org/software/systemd/man/systemd.exec.html)
- [systemd-analyze security](https://www.freedesktop.org/software/systemd/man/systemd-analyze.html)
- [DynamicUser documentation](https://www.freedesktop.org/software/systemd/man/systemd.exec.html#DynamicUser=)

### NixOS Documentation
- [NixOS nginx module source](https://github.com/NixOS/nixpkgs/blob/master/nixos/modules/services/web-servers/nginx/default.nix)
- [NixOS nginx options](https://search.nixos.org/options?query=services.nginx)
- [lib.mkMerge documentation](https://nixos.org/manual/nixpkgs/stable/#function-library-lib.trivial.mkMerge)

### Security Guides
- [systemd service hardening](https://gist.github.com/ageis/f5595e59b1cddb1513d1b425a323db04)
- [ANSSI systemd hardening guide](https://www.ssi.gouv.fr/uploads/2019/03/linux_configuration-en-v1.2.pdf)

### Project Examples
- `atftpd.nix` - Network service hardening example (score 2.0)
- This project's `hls-generator` service can also be hardened similarly

## Appendix: Full systemd-analyze Output

<details>
<summary>Current nginx security analysis (score 1.6)</summary>

```
✓ SystemCallFilter=~@swap
✗ SystemCallFilter=~@resources (0.2)
✓ SystemCallFilter=~@reboot
✓ SystemCallFilter=~@raw-io
✓ SystemCallFilter=~@privileged
✓ SystemCallFilter=~@obsolete
✓ SystemCallFilter=~@mount
✓ SystemCallFilter=~@module
✓ SystemCallFilter=~@debug
✓ SystemCallFilter=~@cpu-emulation
✓ SystemCallFilter=~@clock
✓ RemoveIPC=
✗ RootDirectory=/RootImage= (0.1)
✓ User=/DynamicUser=
✓ RestrictRealtime=
✓ NoNewPrivileges=
✗ AmbientCapabilities= (0.1)
✓ SystemCallArchitectures=
✗ RestrictAddressFamilies=~AF_UNIX (0.1)
✗ RestrictAddressFamilies=~AF_(INET|INET6) (0.3)
✓ ProtectSystem=
✓ ProtectProc=
✗ CapabilityBoundingSet=~CAP_SYS_(NICE|RESOURCE) (0.1)
✓ CapabilityBoundingSet=~CAP_SYS_RAWIO
✓ CapabilityBoundingSet=~CAP_SYS_PTRACE
✓ CapabilityBoundingSet=~CAP_NET_ADMIN
✓ CapabilityBoundingSet=~CAP_AUDIT_*
✓ CapabilityBoundingSet=~CAP_SYS_ADMIN
✓ PrivateTmp=
✓ ProcSubset=
✓ ProtectHome=
✓ PrivateDevices=
✗ CapabilityBoundingSet=~CAP_NET_(BIND_SERVICE|BROADCAST|RAW) (0.1)
✗ PrivateNetwork= (0.5)
✗ PrivateUsers= (0.2)
✗ DeviceAllow= (0.1)
✓ KeyringMode=
✗ IPAddressDeny= (0.2)
✓ ProtectClock=
✓ ProtectKernelLogs=
✓ ProtectControlGroups=
✓ ProtectKernelModules=
✓ MemoryDenyWriteExecute=
✓ RestrictNamespaces=~*
✓ ProtectHostname=
✓ LockPersonality=
✓ ProtectKernelTunables=
✓ RestrictAddressFamilies=~AF_PACKET
✓ RestrictAddressFamilies=~AF_NETLINK
✓ RestrictSUIDSGID=
✗ UMask= (0.1)

→ Overall exposure level: 1.6 OK
```

</details>
