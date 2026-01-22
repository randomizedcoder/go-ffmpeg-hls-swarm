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
  # - FFmpeg runs as 'hls' user, writes to /var/hls
  # - Nginx runs as 'nginx' user, reads from /var/hls via 'hls' group
  # ═══════════════════════════════════════════════════════════════════════════
  users.groups.hls = {};
  users.users.hls = {
    isSystemUser = true;
    group = "hls";
    description = "HLS stream generator";
  };

  # Add nginx to hls group so it can read the files
  users.users.nginx.extraGroups = [ "hls" ];

  # tmpfs for HLS segments with restricted permissions
  # - Owner: hls:hls
  # - Mode: 0750 (owner rwx, group rx, others none)
  fileSystems."/var/hls" = {
    device = "tmpfs";
    fsType = "tmpfs";
    options = [
      "size=${toString d.recommendedTmpfsMB}M"
      "uid=hls"
      "gid=hls"
      "mode=0750"
    ];
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
      # Security hardening
      NoNewPrivileges = true;
      ProtectSystem = "strict";
      ProtectHome = true;
      PrivateTmp = true;
      # Allow write to /var/hls
      ReadWritePaths = [ "/var/hls" ];
      ExecStart = "${pkgs.ffmpeg-full}/bin/ffmpeg ${lib.concatStringsSep " " (map lib.escapeShellArg ffmpegArgs)}";
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
    recommendedOptimisation = true;
    recommendedProxySettings = true;

    # Append to global http config
    appendHttpConfig = ''
      # File descriptor caching
      open_file_cache max=10000 inactive=30s;
      open_file_cache_valid 10s;
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
      locations."~ \\.m3u8$" = {
        extraConfig = ''
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
      locations."/files/" = {
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
          
          # Accept Prometheus exporter
          tcp dport 9113 accept
          
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
