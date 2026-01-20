# OCI container image for go-ffmpeg-hls-swarm
# See: docs/CLIENT_DEPLOYMENT.md for detailed documentation
#
# Build: nix build .#swarm-client-container
# Load:  docker load < ./result
# Run:   docker run --rm -e STREAM_URL=http://origin:8080/stream.m3u8 swarm-client
#
{ pkgs, lib, config, swarmBinary }:

let
  # Wrapper script with environment variable support
  entrypoint = pkgs.writeShellApplication {
    name = "swarm-entrypoint";
    runtimeInputs = [ swarmBinary pkgs.ffmpeg-full ];
    text = ''
      set -euo pipefail

      # Required environment variable
      : "''${STREAM_URL:?STREAM_URL environment variable is required}"

      # Optional with defaults from config profile
      CLIENTS="''${CLIENTS:-${toString config.clients}}"
      RAMP_RATE="''${RAMP_RATE:-${toString config.rampRate}}"
      RAMP_JITTER="''${RAMP_JITTER:-${toString config.rampJitter}}"
      METRICS_PORT="''${METRICS_PORT:-${toString config.metricsPort}}"
      LOG_LEVEL="''${LOG_LEVEL:-${config.logLevel}}"
      VARIANT="''${VARIANT:-${config.variant}}"
      TIMEOUT="''${TIMEOUT:-${toString config.timeout}}"

      echo "╔═══════════════════════════════════════════════════════════════╗"
      echo "║              go-ffmpeg-hls-swarm (container)                  ║"
      echo "╚═══════════════════════════════════════════════════════════════╝"
      echo ""
      echo "Profile:      ${config._profile.name}"
      echo "Stream URL:   $STREAM_URL"
      echo "Clients:      $CLIENTS"
      echo "Ramp Rate:    $RAMP_RATE/sec (jitter: ''${RAMP_JITTER}ms)"
      echo "Variant:      $VARIANT"
      echo "Timeout:      ''${TIMEOUT}s"
      echo "Metrics:      :$METRICS_PORT/metrics"
      echo ""

      # Verify FFmpeg is available
      echo "Verifying FFmpeg..."
      ffmpeg -version | head -1
      ffprobe -version | head -1
      echo ""

      # Build and execute command
      # Note: actual CLI flags will depend on go-ffmpeg-hls-swarm implementation
      exec go-ffmpeg-hls-swarm \
        --clients "$CLIENTS" \
        --ramp-rate "$RAMP_RATE" \
        --ramp-jitter "$RAMP_JITTER" \
        --metrics-port "$METRICS_PORT" \
        --log-level "$LOG_LEVEL" \
        --variant "$VARIANT" \
        --timeout "$TIMEOUT" \
        ''${RECONNECT:+--reconnect} \
        ''${NO_CACHE:+--no-cache} \
        ''${RESOLVE_IP:+--resolve "$RESOLVE_IP"} \
        ''${DANGEROUS:+--dangerous} \
        ''${EXTRA_ARGS:-} \
        "$STREAM_URL"
    '';
  };

in pkgs.dockerTools.buildLayeredImage {
  name = "go-ffmpeg-hls-swarm";
  tag = "latest";

  contents = [
    # Core binaries
    pkgs.ffmpeg-full
    swarmBinary
    entrypoint

    # Minimal utilities for debugging
    pkgs.busybox
    pkgs.curl

    # TLS certificates
    pkgs.cacert
  ];

  config = {
    Entrypoint = [ "${entrypoint}/bin/swarm-entrypoint" ];

    ExposedPorts = {
      "${toString config.metricsPort}/tcp" = {};
    };

    Env = [
      # TLS certificates
      "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"

      # Default configuration (can be overridden at runtime)
      "CLIENTS=${toString config.clients}"
      "RAMP_RATE=${toString config.rampRate}"
      "RAMP_JITTER=${toString config.rampJitter}"
      "METRICS_PORT=${toString config.metricsPort}"
      "LOG_LEVEL=${config.logLevel}"
      "VARIANT=${config.variant}"
      "TIMEOUT=${toString config.timeout}"

      # Optional flags (set to enable)
      # "RECONNECT=1"
      # "NO_CACHE=1"
      # "RESOLVE_IP=192.168.1.100"
      # "DANGEROUS=1"
    ];

    Labels = {
      "org.opencontainers.image.title" = "go-ffmpeg-hls-swarm";
      "org.opencontainers.image.description" = "HLS load testing with FFmpeg process orchestration";
      "org.opencontainers.image.source" = "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm";
      "org.opencontainers.image.documentation" = "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm/blob/main/docs/CLIENT_DEPLOYMENT.md";
      "org.opencontainers.image.version" = "0.1.0";
      "org.opencontainers.image.vendor" = "randomizedcoder";
      "swarm.profile" = config._profile.name;
      "swarm.clients.default" = toString config.clients;
    };
  };

  # Set up temp directory with correct permissions
  fakeRootCommands = ''
    mkdir -p /tmp
    chmod 1777 /tmp
  '';

  # Layer optimization
  maxLayers = 100;
}
