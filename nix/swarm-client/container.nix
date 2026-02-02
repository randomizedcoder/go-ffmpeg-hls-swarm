# OCI container image for go-ffmpeg-hls-swarm
# See: docs/CLIENT_DEPLOYMENT.md for detailed documentation
#
# Security: Test container security with ./scripts/nix-tests/test-container-security.sh
#           This script verifies non-root execution, file permissions, attack surface,
#           and other security best practices.
#
# Uses Nix-idiomatic user/group management with buildLayeredImage.
# Reference: https://nixos.org/manual/nixpkgs/stable/#sec-pkgs-dockerTools
#
# Build: nix build .#swarm-client-container
# Load:  docker load < ./result
# Run:   docker run --rm -e STREAM_URL=http://origin:8080/stream.m3u8 swarm-client
#
{ pkgs, lib, config, swarmBinary }:

let
  # Nix-idiomatic user/group creation (no imperative useradd/groupadd)
  # Creates /etc/passwd, /etc/group, /etc/shadow directly using writeTextDir
  # This avoids permission issues with shadowSetup and fakeRootCommands
  # See: https://nixos.org/manual/nixpkgs/stable/#sec-pkgs-dockerTools
  user = "swarm";
  group = "swarm";
  uid = "1000";
  gid = "1000";

  # Define file contents as clean, modular variables
  nsswitchContent = ''
    passwd: files
    group: files
    shadow: files
    hosts: files dns
  '';

  # Create individual file derivations using writeTextDir
  # /etc/passwd: username:password:UID:GID:GECOS:home:shell
  passwdFile = pkgs.writeTextDir "etc/passwd"
    "${user}:x:${uid}:${gid}:Swarm client user:/tmp:/bin/false\n";

  # /etc/group: groupname:password:GID:members
  groupFile = pkgs.writeTextDir "etc/group"
    "${group}:x:${gid}:\n";

  # /etc/shadow: username:password:lastchanged:min:max:warn:inactive:expire:reserved
  # Password field "!" means locked/disabled (no password login)
  shadowFile = pkgs.writeTextDir "etc/shadow"
    "${user}:!:1:0:99999:7:::\n";

  nsswitchFile = pkgs.writeTextDir "etc/nsswitch.conf" nsswitchContent;

  # Merge them into a single etc output using symlinkJoin
  etcContents = pkgs.symlinkJoin {
    name = "etc-contents";
    paths = [ passwdFile groupFile shadowFile nsswitchFile ];
  };

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
      # RAMP_JITTER is in milliseconds, but Go flag expects duration (e.g., "100ms")
      # Convert number to duration format if it's just a number
      # This conversion ensures the Go flag parser receives a valid duration string
      RAMP_JITTER_RAW="''${RAMP_JITTER:-${toString config.rampJitter}}"
      if echo "''$RAMP_JITTER_RAW" | grep -qE '^[0-9]+$'; then
        # It's just a number, assume milliseconds
        RAMP_JITTER="''${RAMP_JITTER_RAW}ms"
      else
        # Already has a unit (ms, s, etc.)
        RAMP_JITTER="''$RAMP_JITTER_RAW"
      fi
      METRICS_PORT="''${METRICS_PORT:-${toString config.metricsPort}}"
      # Metrics flag expects full address like "0.0.0.0:17091"
      METRICS_ADDR="0.0.0.0:''$METRICS_PORT"
      # LOG_LEVEL in config is "info", but flag is --log-format which expects "json" or "text"
      # For now, map "info" to "text" format (more readable in containers)
      LOG_FORMAT="''${LOG_FORMAT:-text}"
      VARIANT="''${VARIANT:-${config.variant}}"
      # TIMEOUT is in seconds, but Go flag expects duration (e.g., "15s")
      TIMEOUT_RAW="''${TIMEOUT:-${toString config.timeout}}"
      if echo "''$TIMEOUT_RAW" | grep -qE '^[0-9]+$'; then
        # It's just a number, assume seconds
        TIMEOUT="''${TIMEOUT_RAW}s"
      else
        # Already has a unit
        TIMEOUT="''$TIMEOUT_RAW"
      fi

      echo "╔═══════════════════════════════════════════════════════════════╗"
      echo "║              go-ffmpeg-hls-swarm (container)                  ║"
      echo "╚═══════════════════════════════════════════════════════════════╝"
      echo ""
      echo "Profile:      ${config._profile.name}"
      echo "Stream URL:   ''$STREAM_URL"
      echo "Clients:      ''$CLIENTS"
      echo "Ramp Rate:    ''$RAMP_RATE/sec (jitter: ''$RAMP_JITTER)"
      echo "Variant:      ''$VARIANT"
      echo "Timeout:      ''$TIMEOUT"
      echo "Metrics:      http://localhost:''$METRICS_PORT/metrics"
      echo ""

      # Verify FFmpeg is available
      echo "Verifying FFmpeg..."
      ffmpeg -version | head -1
      ffprobe -version | head -1
      echo ""

      # Build and execute command
      exec go-ffmpeg-hls-swarm \
        -clients "''$CLIENTS" \
        -ramp-rate "''$RAMP_RATE" \
        -ramp-jitter "''$RAMP_JITTER" \
        -metrics "''$METRICS_ADDR" \
        -log-format "''$LOG_FORMAT" \
        -variant "''$VARIANT" \
        -timeout "''$TIMEOUT" \
        ''${TUI:+-tui} \
        ''${RECONNECT:+--reconnect} \
        ''${NO_CACHE:+-no-cache} \
        ''${RESOLVE_IP:+-resolve "''$RESOLVE_IP"} \
        ''${DANGEROUS:+--dangerous} \
        ''${EXTRA_ARGS:+"''$EXTRA_ARGS"} \
        "''$STREAM_URL"
    '';
  };

in pkgs.dockerTools.buildLayeredImage {
  name = "go-ffmpeg-hls-swarm";
  tag = "latest";

  contents = [
    etcContents   # Nix-idiomatic user/group files (no shadowSetup needed)
    # Core binaries
    pkgs.ffmpeg-full
    swarmBinary
    entrypoint

    # Minimal utilities (consider removing if not needed for production)
    # busybox provides basic shell utilities, curl for healthchecks
    pkgs.busybox
    pkgs.curl

    # TLS certificates
    pkgs.cacert
  ];

  config = {
    Entrypoint = [ "${entrypoint}/bin/swarm-entrypoint" ];
    # Run as non-root user for security (metrics port > 1024, no privileges needed)
    User = "${user}";

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

  # Context: extraCommands runs as build user (non-root) in build sandbox
  # - Creates directory structure relative to build sandbox
  # - Can set permissions but cannot chown (no root privileges)
  # - Directories created here are visible to fakeRootCommands via relative paths
  extraCommands = ''
    mkdir -p tmp
    # Set permissions (ownership will be set in fakeRootCommands)
    chmod 1777 tmp
  '';

  # Context: fakeRootCommands runs as root in build sandbox
  # - Can use chown/chgrp to set ownership
  # - Must use relative paths (no leading /) to reference extraCommands directories
  # - These paths become /tmp in the final container image
  fakeRootCommands = ''
    #!${pkgs.runtimeShell}
    # Set ownership for temp directory (world-writable with sticky bit)
    # Use relative paths (no leading /) to reference directories from extraCommands
    chown root:root tmp
    chmod 1777 tmp
  '';

  # Layer optimization
  maxLayers = 100;
}
