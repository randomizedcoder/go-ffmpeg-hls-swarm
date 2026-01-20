# OCI container for test origin
# See: docs/TEST_ORIGIN.md for detailed documentation
{ pkgs, lib, config, ffmpeg, nginx }:

let
  # Entrypoint script that runs both services
  entrypoint = pkgs.writeShellScript "entrypoint" ''
    set -euo pipefail

    echo "HLS Origin Container - Profile: ${config._profile.name}"
    echo "Segment duration: ${toString config.hls.segmentDuration}s"
    echo "Rolling window: ${toString config.hls.listSize} segments"
    echo "Delete threshold: ${toString config.hls.deleteThreshold}"
    echo ""

    mkdir -p ${config.server.hlsDir}

    echo "Starting FFmpeg HLS generator..."
    ${ffmpeg.script} &

    # Wait for stream
    for i in $(seq 1 30); do
      [ -f "${config.server.hlsDir}/${config.hls.playlistName}" ] && break
      sleep 1
    done

    echo "Starting Nginx on port ${toString config.server.port}..."
    exec ${nginx.script}
  '';
in
pkgs.dockerTools.buildLayeredImage {
  name = "go-ffmpeg-hls-swarm-test-origin";
  tag = "latest";

  contents = [
    pkgs.ffmpeg-full
    pkgs.nginx
    pkgs.coreutils
    pkgs.bash
  ];

  config = {
    Cmd = [ "${entrypoint}" ];
    ExposedPorts = { "${toString config.server.port}/tcp" = {}; };
    Volumes = { "${config.server.hlsDir}" = {}; };
    Env = [
      "HLS_DIR=${config.server.hlsDir}"
      "PORT=${toString config.server.port}"
    ];
    Labels = {
      "org.opencontainers.image.title" = "go-ffmpeg-hls-swarm-test-origin";
      "org.opencontainers.image.description" = "Test HLS origin server for load testing";
      "hls.profile" = config._profile.name;
      "hls.segment-duration" = toString config.hls.segmentDuration;
      "hls.list-size" = toString config.hls.listSize;
    };
  };

  extraCommands = ''
    mkdir -p var/hls tmp
  '';
}
