# OCI container image for go-ffmpeg-hls-swarm binary
# Supports environment variables for Kubernetes/Nomad orchestration
# Includes healthcheck for container orchestration
#
# Security: Test container security with ./scripts/nix-tests/test-container-security.sh
#           This script verifies non-root execution, file permissions, attack surface,
#           and other security best practices.
#
# Build: nix build .#go-ffmpeg-hls-swarm-container
# Load:  docker load < ./result
# Run:   docker run --rm go-ffmpeg-hls-swarm:latest -clients 10 http://origin:8080/stream.m3u8
#
{ pkgs, lib, package }:

let
  # Wrapper script with transparent environment variable support
  # Canonical mapping: CLI args override env vars (CLI takes precedence)
  entrypoint = pkgs.writeShellApplication {
    name = "swarm-entrypoint";
    runtimeInputs = [ package pkgs.ffmpeg-full ];
    text = ''
      set -euo pipefail

      # Canonical env var → CLI flag mapping
      # Rule: CLI args override env vars (CLI takes precedence)

      # Build args from env vars (if not overridden by CLI)
      ARGS=()

      # Only use env vars if corresponding CLI flag not present
      if ! echo "$*" | grep -qE '\s--clients\s'; then
        [ -n "''${CLIENTS:-}" ] && ARGS+=(--clients "$CLIENTS")
      fi

      if ! echo "$*" | grep -qE '\s--duration\s'; then
        [ -n "''${DURATION:-}" ] && ARGS+=(--duration "$DURATION")
      fi

      if ! echo "$*" | grep -qE '\s--ramp-rate\s'; then
        [ -n "''${RAMP_RATE:-}" ] && ARGS+=(--ramp-rate "$RAMP_RATE")
      fi

      if ! echo "$*" | grep -qE '\s--metrics-port\s'; then
        METRICS_PORT="''${METRICS_PORT:-9100}"
        ARGS+=(--metrics-port "$METRICS_PORT")
      fi

      if ! echo "$*" | grep -qE '\s--log-level\s'; then
        LOG_LEVEL="''${LOG_LEVEL:-info}"
        ARGS+=(--log-level "$LOG_LEVEL")
      fi

      # Debug mode: print resolved command
      if [[ "''${LOG_LEVEL:-}" == "debug" ]] || [[ "''${PRINT_CMD:-}" == "1" ]]; then
        echo "Resolved command: ${lib.getExe package} ''${ARGS[*]} $*" >&2
      fi

      # Execute (CLI args come after env-var-derived args, so CLI overrides)
      # Quote array expansion to prevent word splitting
      exec ${lib.getExe package} "''${ARGS[@]}" "$@"
    '';
  };
in
pkgs.dockerTools.buildLayeredImage {
  name = "go-ffmpeg-hls-swarm";
  tag = "latest";

  contents = [
    # Core binary
    package

    # Runtime dependencies
    pkgs.ffmpeg-full
    entrypoint

    # Minimal utilities for debugging
    pkgs.busybox
    pkgs.curl

    # TLS certificates (required for HTTPS streams)
    pkgs.cacert
  ];

  config = {
    Entrypoint = [ "${lib.getExe entrypoint}" ];

    ExposedPorts = {
      "9100/tcp" = {};  # Metrics port (default)
    };

    Env = [
      # TLS certificates
      "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
      "METRICS_PORT=9100"
      "LOG_LEVEL=info"
    ];

    # Healthcheck for container orchestration (Kubernetes, Docker Compose, etc.)
    Healthcheck = {
      Test = [ "CMD" "curl" "-f" "http://localhost:9100/metrics" "||" "exit" "1" ];
      Interval = 30000000000;  # 30 seconds (nanoseconds)
      Timeout = 5000000000;    # 5 seconds
      StartPeriod = 10000000000;  # 10 seconds grace period
      Retries = 3;
    };

    Labels = {
      "org.opencontainers.image.title" = "go-ffmpeg-hls-swarm";
      "org.opencontainers.image.description" = "HLS load testing with FFmpeg process orchestration";
      "org.opencontainers.image.source" = "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm";
      "org.opencontainers.image.documentation" = "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm/blob/main/README.md";
      "org.opencontainers.image.version" = "0.1.0";
      "org.opencontainers.image.vendor" = "randomizedcoder";
      "org.opencontainers.image.licenses" = "MIT";
    };
  };

  fakeRootCommands = ''
    mkdir -p /tmp
    chmod 1777 /tmp
  '';

  maxLayers = 100;
}
