# Local development runner for go-ffmpeg-hls-swarm
# See: docs/CLIENT_DEPLOYMENT.md for detailed documentation
#
# Build: nix build .#swarm-client
# Run:   ./result/bin/swarm-client http://localhost:8080/stream.m3u8
#
{ pkgs, lib, config, swarmBinary }:

let
  cfg = config;
  d = cfg.derived;

in pkgs.writeShellApplication {
  name = "swarm-client";

  runtimeInputs = [
    swarmBinary
    pkgs.ffmpeg-full
    pkgs.curl
  ];

  text = ''
    set -euo pipefail

    # Default stream URL (can be overridden as first argument)
    STREAM_URL="''${1:-http://localhost:8080/stream.m3u8}"

    # Configuration from profile (can be overridden via environment)
    CLIENTS="''${CLIENTS:-${toString cfg.clients}}"
    RAMP_RATE="''${RAMP_RATE:-${toString cfg.rampRate}}"
    RAMP_JITTER="''${RAMP_JITTER:-${toString cfg.rampJitter}}"
    METRICS_PORT="''${METRICS_PORT:-${toString cfg.metricsPort}}"
    LOG_LEVEL="''${LOG_LEVEL:-${cfg.logLevel}}"
    VARIANT="''${VARIANT:-${cfg.variant}}"
    TIMEOUT="''${TIMEOUT:-${toString cfg.timeout}}"

    echo "╔═══════════════════════════════════════════════════════════════╗"
    echo "║              go-ffmpeg-hls-swarm (local runner)               ║"
    echo "╚═══════════════════════════════════════════════════════════════╝"
    echo ""
    echo "Profile:      ${cfg._profile.name}"
    echo "Stream URL:   $STREAM_URL"
    echo "Clients:      $CLIENTS"
    echo "Ramp Rate:    $RAMP_RATE/sec (jitter: ''${RAMP_JITTER}ms)"
    echo "Variant:      $VARIANT"
    echo "Timeout:      ''${TIMEOUT}s"
    echo "Metrics:      http://localhost:$METRICS_PORT/metrics"
    echo ""
    echo "Estimated Memory: ~${toString d.estimatedMemoryMB}MB"
    echo "Ramp Duration:    ~${toString d.rampDuration}s"
    echo ""

    # Verify dependencies
    echo "Verifying FFmpeg..."
    ffmpeg -version | head -1
    ffprobe -version | head -1
    echo ""

    # Check if stream is reachable (optional, don't fail)
    echo "Checking stream availability..."
    if curl -sf --connect-timeout 5 "$STREAM_URL" > /dev/null 2>&1; then
      echo "✓ Stream is reachable"
    else
      echo "⚠ Warning: Stream may not be reachable (continuing anyway)"
    fi
    echo ""

    # Check system limits
    echo "System limits:"
    echo "  Open files (soft): $(ulimit -Sn)"
    echo "  Open files (hard): $(ulimit -Hn)"
    echo ""

    # Warn if limits might be too low
    CURRENT_LIMIT=$(ulimit -Sn)
    RECOMMENDED=${toString d.recommendedFdLimit}
    if [ "$CURRENT_LIMIT" -lt "$RECOMMENDED" ]; then
      echo "⚠ Warning: Open file limit ($CURRENT_LIMIT) is below recommended ($RECOMMENDED)"
      echo "   Consider: ulimit -n $RECOMMENDED"
      echo ""
    fi

    echo "Starting load test..."
    echo "Press Ctrl+C to stop gracefully"
    echo ""

    # Build command
    CMD=(
      go-ffmpeg-hls-swarm
      --clients "$CLIENTS"
      --ramp-rate "$RAMP_RATE"
      --ramp-jitter "$RAMP_JITTER"
      --metrics-port "$METRICS_PORT"
      --log-level "$LOG_LEVEL"
      --variant "$VARIANT"
      --timeout "$TIMEOUT"
    )

    # Add optional reconnect flags
    # Use variables to avoid shellcheck constant expression warnings
    RECONNECT_FLAG="${toString cfg.reconnect}"
    RECONNECT_DELAY="${toString cfg.reconnectDelayMax}"
    if [ "$RECONNECT_FLAG" = "true" ] || [ "''${RECONNECT:-}" = "1" ]; then
      CMD+=(--reconnect)
      if [ "$RECONNECT_DELAY" -gt 0 ]; then
        CMD+=(--reconnect-delay-max "$RECONNECT_DELAY")
      fi
    fi

    CMD+=(--seg-max-retry "${toString cfg.segMaxRetry}")

    # Add extra args if provided
    if [ -n "''${EXTRA_ARGS:-}" ]; then
      # shellcheck disable=SC2206
      CMD+=($EXTRA_ARGS)
    fi

    # Add stream URL
    CMD+=("$STREAM_URL")

    # Execute
    exec "''${CMD[@]}"
  '';
}
