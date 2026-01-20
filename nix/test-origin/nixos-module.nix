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
#
{ config, ffmpeg, nginx }:

{ pkgs, lib, ... }:

let
  h = config.hls;
  c = config.cache;
  d = config.derived;

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
  # tmpfs for HLS segments
  # Size: (Bitrate * Window * 2) + 64MB
  fileSystems."/var/hls" = {
    device = "tmpfs";
    fsType = "tmpfs";
    options = [ "size=${toString d.recommendedTmpfsMB}M" "mode=1777" ];
  };

  # FFmpeg HLS generator systemd service
  systemd.services.hls-generator = {
    description = "FFmpeg HLS Test Stream Generator (${config._profile.name} profile)";
    after = [ "network.target" ];
    wantedBy = [ "multi-user.target" ];

    serviceConfig = {
      Type = "simple";
      ExecStartPre = "${pkgs.coreutils}/bin/mkdir -p /var/hls";
      ExecStart = "${pkgs.ffmpeg-full}/bin/ffmpeg ${lib.concatStringsSep " " (map lib.escapeShellArg ffmpegArgs)}";
      Restart = "always";
      RestartSec = 2;
    };
  };

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
    '';

    virtualHosts."hls-origin" = {
      listen = [{ addr = "0.0.0.0"; port = config.server.port; }];
      root = "/var/hls";

      # Master playlist (ABR entry point)
      locations."= /${h.masterPlaylist}" = {
        extraConfig = ''
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
    };
  };

  # Open firewall
  networking.firewall.allowedTCPPorts = [ config.server.port ];
}
