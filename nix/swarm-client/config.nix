# Configuration for go-ffmpeg-hls-swarm client deployment
# See: docs/CLIENT_DEPLOYMENT.md for detailed documentation
#
# Usage:
#   config = import ./config.nix { profile = "default"; };
#   config = import ./config.nix { profile = "stress"; overrides = { clients = 300; }; };
#
{ profile ? "default", overrides ? {}, lib, meta }:

let
  # ═══════════════════════════════════════════════════════════════════════════
  # Profile Definitions
  # ═══════════════════════════════════════════════════════════════════════════
  profiles = {
    # Standard testing profile
    default = {
      clients = 50;
      rampRate = 5;             # clients per second
      rampJitter = 100;         # milliseconds
      metricsPort = 9090;
      logLevel = "info";
      variant = "all";
      reconnect = true;
      reconnectDelayMax = 5;
      segMaxRetry = 3;
      timeout = 15;             # seconds
    };

    # High-load stress testing
    stress = {
      clients = 200;
      rampRate = 20;
      rampJitter = 50;
      metricsPort = 9090;
      logLevel = "warning";
      variant = "all";
      reconnect = true;
      reconnectDelayMax = 3;
      segMaxRetry = 2;
      timeout = 10;
    };

    # Gentle warm-up / baseline
    gentle = {
      clients = 20;
      rampRate = 1;
      rampJitter = 500;
      metricsPort = 9090;
      logLevel = "info";
      variant = "first";
      reconnect = true;
      reconnectDelayMax = 10;
      segMaxRetry = 5;
      timeout = 30;
    };

    # Thundering herd simulation
    burst = {
      clients = 100;
      rampRate = 50;
      rampJitter = 10;
      metricsPort = 9090;
      logLevel = "warning";
      variant = "all";
      reconnect = false;        # No reconnect for burst testing
      reconnectDelayMax = 0;
      segMaxRetry = 1;
      timeout = 5;
    };

    # Maximum load testing
    extreme = {
      clients = 500;
      rampRate = 50;
      rampJitter = 20;
      metricsPort = 9090;
      logLevel = "error";
      variant = "all";
      reconnect = true;
      reconnectDelayMax = 2;
      segMaxRetry = 1;
      timeout = 5;
    };
  };

  # Use generic profile system
  profileSystem = meta.mkProfileSystem {
    base = {};  # swarm-client has no base config, only profiles
    inherit profiles;
  };

  # Get merged config
  cfg = profileSystem.getConfig profile overrides;

in cfg // {
  # ═══════════════════════════════════════════════════════════════════════════
  # Derived Values
  # ═══════════════════════════════════════════════════════════════════════════
  derived = {
    # Estimated ramp-up duration (seconds)
    rampDuration = cfg.clients / cfg.rampRate;

    # Memory estimate (see docs/MEMORY.md)
    # ~19MB private per process + ~64MB shared/overhead
    estimatedMemoryMB = (cfg.clients * 19) + 64;

    # Recommended file descriptor limit
    # Each FFmpeg needs ~10-15 FDs, plus orchestrator overhead
    recommendedFdLimit = (cfg.clients * 15) + 1000;

    # Recommended ephemeral ports
    # Each FFmpeg client uses 1-4 concurrent connections
    recommendedPorts = cfg.clients * 5;

    # Container memory limit (with 20% headroom)
    containerMemoryMB = builtins.ceil (((cfg.clients * 19) + 64) * 1.2);

    # VM memory (with kernel and service overhead)
    vmMemoryMB = builtins.ceil (((cfg.clients * 19) + 64) * 1.3) + 256;
  };

  # Profile metadata (already included by getConfig, but explicit for clarity)
  _profile = cfg._profile;
}
