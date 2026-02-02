# NixOS module for go-ffmpeg-hls-swarm
# See: docs/CLIENT_DEPLOYMENT.md for detailed documentation
#
# Reusable across MicroVMs, containers, and NixOS tests.
# Provides systemd service with resource limits and kernel tuning.
#
{ config, swarmBinary }:

{ pkgs, lib, ... }:

let
  cfg = config;
  d = cfg.derived;

  # Build command line arguments
  mkSwarmArgs = streamUrl: lib.concatStringsSep " " (lib.filter (x: x != "") [
    "${swarmBinary}/bin/go-ffmpeg-hls-swarm"
    "--clients ${toString cfg.clients}"
    "--ramp-rate ${toString cfg.rampRate}"
    "--ramp-jitter ${toString cfg.rampJitter}"
    "--metrics-port ${toString cfg.metricsPort}"
    "--log-level ${cfg.logLevel}"
    "--variant ${cfg.variant}"
    "--timeout ${toString cfg.timeout}"
    (lib.optionalString cfg.reconnect "--reconnect")
    (lib.optionalString (cfg.reconnectDelayMax > 0) "--reconnect-delay-max ${toString cfg.reconnectDelayMax}")
    "--seg-max-retry ${toString cfg.segMaxRetry}"
    streamUrl
  ]);

in {
  # Import kernel tuning
  imports = [ ./sysctl.nix ];

  # Required packages
  environment.systemPackages = [
    swarmBinary
    pkgs.ffmpeg-full
    pkgs.curl      # For health checks
    pkgs.htop      # For debugging
  ];

  # Systemd service for the swarm client
  systemd.services.go-ffmpeg-hls-swarm = {
    description = "HLS Load Testing Client (${cfg._profile.name} profile)";
    documentation = [ "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm" ];

    after = [ "network-online.target" ];
    wants = [ "network-online.target" ];
    # Don't start automatically - user should set STREAM_URL first
    # wantedBy = [ "multi-user.target" ];

    environment = {
      # Stream URL must be set before starting
      # STREAM_URL = "http://origin:8080/stream.m3u8";

      # FFmpeg will need SSL certificates
      SSL_CERT_FILE = "${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt";
    };

    serviceConfig = {
      Type = "simple";

      # Command with stream URL from environment
      ExecStart = mkSwarmArgs "\${STREAM_URL}";

      # Restart on failure (not on clean exit)
      Restart = "on-failure";
      RestartSec = 5;

      # Resource limits based on config
      LimitNOFILE = d.recommendedFdLimit;
      LimitNPROC = "infinity";

      # Memory limit with headroom
      MemoryMax = "${toString d.containerMemoryMB}M";

      # Security hardening
      NoNewPrivileges = true;
      ProtectSystem = "strict";
      ProtectHome = true;
      PrivateTmp = true;

      # Allow network access
      PrivateNetwork = false;

      # Working directory
      WorkingDirectory = "/tmp";

      # Logging
      StandardOutput = "journal";
      StandardError = "journal";
      SyslogIdentifier = "go-ffmpeg-hls-swarm";
    };
  };

  # Helper service to verify FFmpeg before starting
  systemd.services.go-ffmpeg-hls-swarm-preflight = {
    description = "Verify FFmpeg installation for HLS swarm client";
    before = [ "go-ffmpeg-hls-swarm.service" ];
    requiredBy = [ "go-ffmpeg-hls-swarm.service" ];

    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      ExecStart = pkgs.writeShellScript "swarm-preflight" ''
        set -e
        echo "Checking FFmpeg..."
        ${pkgs.ffmpeg-full}/bin/ffmpeg -version | head -1
        ${pkgs.ffmpeg-full}/bin/ffprobe -version | head -1
        echo "FFmpeg OK"

        echo "Checking go-ffmpeg-hls-swarm..."
        ${swarmBinary}/bin/go-ffmpeg-hls-swarm --version 2>/dev/null || echo "Binary exists"
        echo "Preflight checks passed"
      '';
    };
  };

  # Open metrics port
  networking.firewall.allowedTCPPorts = [ cfg.metricsPort ];
}
