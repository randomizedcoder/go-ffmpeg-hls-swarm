# NixOS module for HLS origin services
# See: docs/TEST_ORIGIN.md for detailed documentation
#
# Uses mkFfmpegArgs helper for clean argument building.
# Reusable across MicroVMs, containers, and NixOS tests.
#
# Includes:
# - Kernel sysctl tuning for high-performance networking
# - FFmpeg HLS generator service
# - Nginx with optimized caching
# - Optional buffered logging for performance analysis
#
{ config, ffmpeg, nginx }:

{ pkgs, lib, ... }:

let
  h = config.hls;
  c = config.cache;
  d = config.derived;
  log = config.logging;

  # Escape % as %% for systemd specifier handling
  # See: https://www.freedesktop.org/software/systemd/man/systemd.unit.html#Specifiers
  escapeSystemdPercent = s: builtins.replaceStrings ["%"] ["%%"] s;

  # Use the mkFfmpegArgs helper for clean argument building
  # This makes it easy to override in tests without rewriting strings
  ffmpegArgs = ffmpeg.mkFfmpegArgs {
    hlsDir = "/var/hls";
  };

  # HLS flags string
  hlsFlags = lib.concatStringsSep "+" h.flags;
in
{
  # Import kernel network tuning
  imports = [ ./sysctl.nix ];
  # ═══════════════════════════════════════════════════════════════════════════
  # Security: Dedicated user/group for HLS file access
  # - FFmpeg runs as fixed 'hls' user, writes to /var/hls
  # - Nginx runs as DynamicUser, reads from /var/hls via BindReadOnlyPaths
  # See: docs/NGINX_SECURITY.md for security design
  # ═══════════════════════════════════════════════════════════════════════════
  users.groups.hls = {};
  users.users.hls = {
    isSystemUser = true;
    group = "hls";
    description = "HLS stream generator";
  };

  # NOTE: No longer adding nginx to hls group - using DynamicUser + BindReadOnlyPaths instead

  # ═══════════════════════════════════════════════════════════════════════════
  # User accounts for SSH access
  # ═══════════════════════════════════════════════════════════════════════════

  # User 'das' with SSH key authentication
  users.users.das = {
    isNormalUser = true;
    description = "das";
    extraGroups = [ "wheel" ];
    openssh.authorizedKeys.keys = [
      "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGMCFUMSCFJX95eLfm7P9r72NBp9I1FiXwNwJ+x/HGPV das@t"
    ];
  };

  # User 'user' with password authentication (for testing)
  # Password: user123
  users.users.user = {
    isNormalUser = true;
    description = "Test user";
    extraGroups = [ "wheel" ];
    password = "user123";
  };

  # tmpfs for HLS segments
  # - Owner: hls:hls (FFmpeg writes as hls user)
  # - Mode: 0755 (world-readable for DynamicUser nginx to read)
  # Note: We use 0755 instead of 0750 because DynamicUser nginx
  # cannot be added to the hls group. This is acceptable since
  # HLS content is intended to be publicly served anyway.
  fileSystems."/var/hls" = {
    device = "tmpfs";
    fsType = "tmpfs";
    options = [
      "size=${toString d.recommendedTmpfsMB}M"
      "uid=hls"
      "gid=hls"
      "mode=0755"
      # Performance: Don't update access time on reads
      # See: docs/NGINX_HLS_CACHING_DESIGN.md section 9
      "noatime"
      # Security: HLS files are data, not executables or devices
      "nodev"
      "nosuid"
      "noexec"
    ];
  };

  # ═══════════════════════════════════════════════════════════════════════════════
  # Systemd Slices for Resource Isolation and Monitoring
  # Provides hierarchical resource control and visibility via systemd-cgtop
  # See: docs/HLS_GENERATOR_SECURITY.md for details
  # ═══════════════════════════════════════════════════════════════════════════════
  systemd.slices = {
    # Parent slice for all HLS origin services
    hls-origin = {
      description = "HLS Origin Services (FFmpeg + Nginx)";
      sliceConfig = {
        # Combined limit for all HLS services (leave 1GB for system)
        MemoryMax = "3G";
        MemoryHigh = "2560M";
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

  # FFmpeg HLS generator systemd service
  systemd.services.hls-generator = {
    description = "FFmpeg HLS Test Stream Generator (${config._profile.name} profile)";
    # Wait for tmpfs mount to be ready before starting
    after = [ "network.target" "var-hls.mount" ];
    requires = [ "var-hls.mount" ];
    wantedBy = [ "multi-user.target" ];

    serviceConfig = {
      Type = "simple";
      # Run as dedicated hls user (not root!)
      User = "hls";
      Group = "hls";

      # ─────────────────────────────────────────────────────────────────────────
      # Slice Assignment (for resource isolation and monitoring)
      # Monitor with: systemd-cgtop or systemctl status hls-generator.slice
      # ─────────────────────────────────────────────────────────────────────────
      Slice = "hls-generator.slice";

      # ─────────────────────────────────────────────────────────────────────────
      # Filesystem Isolation
      # ─────────────────────────────────────────────────────────────────────────
      ProtectSystem = "strict";     # Read-only filesystem
      ProtectHome = true;           # No access to /home, /root
      PrivateTmp = true;            # Private /tmp
      ReadWritePaths = [ "/var/hls" ];  # Only write to HLS output

      # ─────────────────────────────────────────────────────────────────────────
      # Network Isolation (FFmpeg doesn't need network for test patterns!)
      # This is our biggest security win - eliminates entire attack surface
      # ─────────────────────────────────────────────────────────────────────────
      PrivateNetwork = true;        # Complete network isolation
      RestrictAddressFamilies = "none";  # No sockets at all

      # ─────────────────────────────────────────────────────────────────────────
      # Device Access
      # ─────────────────────────────────────────────────────────────────────────
      PrivateDevices = true;        # No access to physical devices
      DeviceAllow = [
        "/dev/null rw"
        "/dev/zero rw"
        "/dev/urandom r"            # For random number generation
      ];

      # ─────────────────────────────────────────────────────────────────────────
      # Capability Restrictions
      # ─────────────────────────────────────────────────────────────────────────
      CapabilityBoundingSet = "";   # No capabilities needed
      AmbientCapabilities = "";     # No ambient capabilities
      NoNewPrivileges = true;       # Cannot gain privileges

      # ─────────────────────────────────────────────────────────────────────────
      # Kernel Protections
      # ─────────────────────────────────────────────────────────────────────────
      ProtectKernelTunables = true;
      ProtectKernelModules = true;
      ProtectKernelLogs = true;
      ProtectControlGroups = true;
      ProtectClock = true;
      ProtectHostname = true;

      # ─────────────────────────────────────────────────────────────────────────
      # Process Isolation
      # ─────────────────────────────────────────────────────────────────────────
      ProtectProc = "invisible";    # Hide other processes
      ProcSubset = "pid";           # Minimal /proc access
      PrivateUsers = true;          # User namespace isolation

      # ─────────────────────────────────────────────────────────────────────────
      # Namespace Restrictions
      # ─────────────────────────────────────────────────────────────────────────
      RestrictNamespaces = true;    # Cannot create any namespaces
      RestrictSUIDSGID = true;      # Cannot create SUID/SGID files
      RemoveIPC = true;             # Clean up IPC on service stop

      # ─────────────────────────────────────────────────────────────────────────
      # Execution Restrictions
      # ─────────────────────────────────────────────────────────────────────────
      LockPersonality = true;
      RestrictRealtime = true;
      MemoryDenyWriteExecute = true;  # No JIT (FFmpeg doesn't need it)

      # ─────────────────────────────────────────────────────────────────────────
      # System Call Filtering
      # ─────────────────────────────────────────────────────────────────────────
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

      # ─────────────────────────────────────────────────────────────────────────
      # File Permissions
      # ─────────────────────────────────────────────────────────────────────────
      UMask = "0022";               # Files readable by nginx (world-readable)

      # ─────────────────────────────────────────────────────────────────────────
      # Resource Limits (1080p60 encoding needs more resources than 720p30)
      # See: docs/HLS_GENERATOR_SECURITY.md for analysis
      # Note: Previous limits (512M/400M) caused severe throttling under load.
      # 1080p60 veryfast with HLS buffering needs ~500-600MB in practice.
      # Note: 1080p60 libx264 veryfast can need 2-3 cores for realtime encoding
      # depending on content complexity. Using 300% for headroom.
      # ─────────────────────────────────────────────────────────────────────────
      MemoryMax = "1G";             # Hard limit - 1080p60 veryfast + HLS buffers
      MemoryHigh = "768M";          # Warn before hard limit
      CPUQuota = "300%";            # 1080p60 veryfast - give 3 cores for headroom
      Nice = "-10";                 # Higher priority than normal processes (-20 to 19)
      IOSchedulingClass = "realtime"; # Prioritize disk I/O for segment writes
      IOSchedulingPriority = 2;     # High priority within realtime class (0-7, lower=higher)
      LimitNOFILE = 256;            # Only needs HLS segment files
      LimitNPROC = 64;              # FFmpeg spawns threads, not processes

      # Escape % as %% for systemd, then escape for shell
      ExecStart = "${pkgs.ffmpeg-full}/bin/ffmpeg ${lib.concatStringsSep " " (map (a: lib.escapeShellArg (escapeSystemdPercent a)) ffmpegArgs)}";
      Restart = "always";
      RestartSec = 2;
    };
  };

  # Create log directory if logging is enabled
  systemd.tmpfiles.rules = lib.mkIf log.enabled [
    "d ${log.directory} 0755 nginx nginx -"
  ];

  # Nginx HLS server with optimized caching and performance
  services.nginx = {
    enable = true;
    enableReload = true;  # Graceful reload without restart
    recommendedOptimisation = true;
    recommendedProxySettings = true;

    # Use all available CPU cores for nginx workers
    # Critical for high-throughput HLS serving under load
    appendConfig = ''
      worker_processes auto;
      worker_rlimit_nofile 65535;
    '';

    # Accept all pending connections at once (better burst handling)
    # See: docs/NGINX_HLS_CACHING_DESIGN.md section 11.4
    eventsConfig = ''
      worker_connections 16384;
      multi_accept on;
    '';

    # Global HTTP config
    # Note: server_tokens is already set by recommendedOptimisation
    commonHttpConfig = ''
      # Additional security headers (server_tokens already off via recommendedOptimisation)
    '';

    # Append to global http config
    appendHttpConfig = ''
      # File descriptor caching - see docs/NGINX_HLS_CACHING_DESIGN.md
      # Dynamic sizing: max=${toString d.openFileCacheMax} = (${toString d.filesPerVariant} files/variant × ${toString d.variantCount} variants + 1 master) × 3
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

    virtualHosts."hls-origin" = {
      listen = [{ addr = "0.0.0.0"; port = config.server.port; }];
      root = "/var/hls";

      # Master playlist (ABR entry point)
      locations."= /${h.masterPlaylist}" = {
        extraConfig = ''
          ${nginx.manifestAccessLog};
          tcp_nodelay    on;
          add_header Cache-Control "${nginx.masterCacheControl}";
          add_header Access-Control-Allow-Origin "*";
          add_header Access-Control-Expose-Headers "Content-Length";
          types { application/vnd.apple.mpegurl m3u8; }
        '';
      };

      # Variant playlists - immediate delivery for freshness
      # See docs/NGINX_HLS_CACHING_DESIGN.md for caching strategy
      locations."~ \\.m3u8$" = {
        extraConfig = ''
          # Override global open_file_cache_valid for manifests
          # Manifests update every 2s; 1s validity = max 50% staleness
          # Still cache (serves ~50% of requests), but check freshness frequently
          # Note: 500ms not supported by nginx - using 1s as fallback
          open_file_cache_valid 1s;

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

      # Segments - throughput optimized with aggressive caching
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

      locations."/health" = {
        return = "200 'OK\\n'";
        extraConfig = ''
          access_log off;
          add_header Content-Type text/plain;
          add_header Cache-Control "no-store";
        '';
      };

      locations."/nginx_status" = {
        extraConfig = ''
          stub_status on;
          access_log off;
          add_header Cache-Control "no-store";
        '';
      };

      # Directory listing for debugging - verify FFmpeg is writing files
      # HTML format at /files/ for human browsing
      # JSON format at /files/json/ for programmatic access
      locations."/files/" = {
        alias = "/var/hls/";
        extraConfig = ''
          autoindex on;
          autoindex_format html;
          access_log off;
          add_header Cache-Control "no-store";
        '';
      };

      locations."/files/json/" = {
        alias = "/var/hls/";
        extraConfig = ''
          autoindex on;
          autoindex_format json;
          access_log off;
          add_header Cache-Control "no-store";
          add_header Content-Type "application/json";
        '';
      };
    };
  };

  # ═══════════════════════════════════════════════════════════════════════════
  # Nginx Systemd Ordering
  # Ensure nginx starts AFTER:
  # 1. tmpfs is mounted (var-hls.mount)
  # 2. FFmpeg has started producing files (hls-generator.service)
  # This prevents 404 errors during startup when clients request files
  # that don't exist yet.
  # ═══════════════════════════════════════════════════════════════════════════
  systemd.services.nginx = {
    after = [ "var-hls.mount" "hls-generator.service" ];
    wants = [ "hls-generator.service" ];
  };

  # ═══════════════════════════════════════════════════════════════════════════
  # Nginx Security Hardening (Phase 2 - Production Grade)
  # See: docs/NGINX_SECURITY.md for detailed documentation
  # Target security score: ~0.5 (from systemd-analyze security nginx)
  # ═══════════════════════════════════════════════════════════════════════════
  systemd.services.nginx.serviceConfig = {
    # ─────────────────────────────────────────────────────────────────────────
    # Slice Assignment (for resource isolation and monitoring)
    # Monitor with: systemd-cgtop or systemctl status hls-nginx.slice
    # ─────────────────────────────────────────────────────────────────────────
    Slice = "hls-nginx.slice";

    # ─────────────────────────────────────────────────────────────────────────
    # DynamicUser - Most powerful isolation feature
    # Allocates temporary UID/GID, user doesn't exist when service is off
    # ─────────────────────────────────────────────────────────────────────────
    DynamicUser = true;

    # Systemd-managed directories (owned by dynamic user)
    StateDirectory = "nginx";       # → /var/lib/nginx
    CacheDirectory = "nginx";       # → /var/cache/nginx
    LogsDirectory = "nginx";        # → /var/log/nginx
    RuntimeDirectory = "nginx";     # → /run/nginx

    # ─────────────────────────────────────────────────────────────────────────
    # HLS Content Access - READ-ONLY (enforced by systemd)
    # FFmpeg writes as 'hls' user, nginx reads via bind mount
    # ─────────────────────────────────────────────────────────────────────────
    BindReadOnlyPaths = [ "/var/hls" ];

    # ─────────────────────────────────────────────────────────────────────────
    # Resource Limits
    # Nginx is the main load testing target - give it most VM resources
    # VM has 4 CPUs and 4GB RAM; FFmpeg uses ~68MB/10%, system uses ~300MB
    # ─────────────────────────────────────────────────────────────────────────
    MemoryMax = "2G";               # Plenty for 10k+ connections and file cache
    MemoryHigh = "1536M";           # Warn at 1.5GB
    CPUQuota = "300%";              # Allow 3 cores for request handling
    LimitNOFILE = 65536;            # High FD limit for many connections
    LimitNPROC = 1024;

    # ─────────────────────────────────────────────────────────────────────────
    # File Permissions
    # ─────────────────────────────────────────────────────────────────────────
    UMask = "0027";                 # Stricter file creation mask

    # ─────────────────────────────────────────────────────────────────────────
    # Filesystem Isolation
    # ─────────────────────────────────────────────────────────────────────────
    ProtectSystem = "strict";       # Read-only filesystem except explicit paths
    ProtectHome = true;             # No access to /home, /root, /run/user
    PrivateTmp = true;              # Private /tmp

    # ─────────────────────────────────────────────────────────────────────────
    # Capabilities (none needed for port 17080)
    # ─────────────────────────────────────────────────────────────────────────
    CapabilityBoundingSet = "";     # No capabilities needed
    AmbientCapabilities = "";       # No ambient capabilities
    NoNewPrivileges = true;         # Prevent privilege escalation

    # ─────────────────────────────────────────────────────────────────────────
    # Device Access
    # ─────────────────────────────────────────────────────────────────────────
    PrivateDevices = true;          # No access to physical devices
    DeviceAllow = [
      "/dev/null rw"
      "/dev/zero rw"
      "/dev/urandom r"
    ];

    # ─────────────────────────────────────────────────────────────────────────
    # Kernel Protections
    # ─────────────────────────────────────────────────────────────────────────
    ProtectKernelTunables = true;
    ProtectKernelModules = true;
    ProtectKernelLogs = true;
    ProtectControlGroups = true;
    ProtectClock = true;
    ProtectHostname = true;

    # ─────────────────────────────────────────────────────────────────────────
    # Namespace/Personality Restrictions
    # ─────────────────────────────────────────────────────────────────────────
    RestrictNamespaces = true;
    RestrictRealtime = true;
    RestrictSUIDSGID = true;
    LockPersonality = true;
    MemoryDenyWriteExecute = true;

    # ─────────────────────────────────────────────────────────────────────────
    # User/Process Isolation
    # ─────────────────────────────────────────────────────────────────────────
    PrivateUsers = true;            # Isolated user namespace
    ProtectProc = "invisible";      # Hide other processes
    ProcSubset = "pid";             # Minimal /proc access

    # ─────────────────────────────────────────────────────────────────────────
    # Network Restrictions
    # ─────────────────────────────────────────────────────────────────────────
    RestrictAddressFamilies = [
      "AF_INET"                     # IPv4 (required)
      "AF_INET6"                    # IPv6 (required)
      "AF_UNIX"                     # Unix sockets (worker communication)
    ];

    # ─────────────────────────────────────────────────────────────────────────
    # System Call Filtering
    # ─────────────────────────────────────────────────────────────────────────
    SystemCallFilter = [
      "@system-service"
      "~@privileged"                # Block privilege escalation
      "~@resources"                 # Block resource priority changes
    ];
    SystemCallErrorNumber = "EPERM";  # Fail predictably
    SystemCallArchitectures = "native";
  };

  # ═══════════════════════════════════════════════════════════════════════════
  # SSH Server for remote access and debugging
  # Host port 17122 → VM port 22
  # See: docs/PORTS.md for port documentation
  # ═══════════════════════════════════════════════════════════════════════════
  services.openssh = {
    enable = true;
    settings = {
      # Allow root login with empty password (for testing only!)
      PermitRootLogin = "yes";
      PermitEmptyPasswords = "yes";
    };
  };

  # ═══════════════════════════════════════════════════════════════════════════
  # Prometheus Nginx Exporter (v1.5.1)
  # Exposes nginx metrics for Grafana dashboards and load test analysis
  # See: docs/NIX_NGINX_REFERENCE.md for detailed documentation
  # ═══════════════════════════════════════════════════════════════════════════
  services.prometheus.exporters.nginx = {
    enable = true;
    port = 9113;
    scrapeUri = "http://localhost:${toString config.server.port}/nginx_status";

    # Add constant labels for multi-instance identification
    constLabels = [
      "instance=hls-origin"
      "profile=${config._profile.name}"
    ];

    # Metrics endpoint path
    telemetryPath = "/metrics";
  };

  # ═══════════════════════════════════════════════════════════════════════════
  # Prometheus Node Exporter
  # Exposes system metrics (CPU, memory, disk, network)
  # Host port 17100 → VM port 9100
  # See: docs/PORTS.md for port documentation
  # ═══════════════════════════════════════════════════════════════════════════
  services.prometheus.exporters.node = {
    enable = true;
    port = 9100;

    # Enable useful collectors for load testing
    enabledCollectors = [
      "cpu"
      "diskstats"
      "filesystem"
      "loadavg"
      "meminfo"
      "netdev"
      "netstat"
      "stat"
      "time"
      "vmstat"
    ];

    # Disable collectors that add noise
    disabledCollectors = [
      "textfile"  # No textfile metrics
    ];
  };

  # Use nftables firewall (modern, cleaner than iptables)
  networking.nftables = {
    enable = true;
    tables.filter = {
      family = "inet";
      content = ''
        chain input {
          type filter hook input priority 0; policy accept;

          # Accept loopback
          iifname "lo" accept

          # Accept established/related
          ct state {established, related} accept

          # Accept ICMP (ping)
          ip protocol icmp accept
          ip6 nexthdr icmpv6 accept

          # Accept HLS origin port
          tcp dport ${toString config.server.port} accept

          # Accept SSH
          tcp dport 22 accept

          # Accept Prometheus nginx exporter
          tcp dport 9113 accept

          # Accept Prometheus node exporter
          tcp dport 9100 accept

          # Accept all (permissive for testing)
          accept
        }

        chain output {
          type filter hook output priority 0; policy accept;
        }

        chain forward {
          type filter hook forward priority 0; policy accept;
        }
      '';
    };
  };

  # Disable the legacy iptables-based firewall
  networking.firewall.enable = false;
}
