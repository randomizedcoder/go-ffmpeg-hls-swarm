# OCI container for test origin
# See: docs/TEST_ORIGIN.md for detailed documentation
#
# Uses Nix-idiomatic user/group management with buildLayeredImage.
# Reference: https://nixos.org/manual/nixpkgs/stable/#sec-pkgs-dockerTools
{ pkgs, lib, config, ffmpeg, nginx }:

let
  # Nix-idiomatic user/group creation (no imperative useradd/groupadd)
  # Creates /etc/passwd, /etc/group, /etc/shadow directly using writeTextDir
  # This avoids permission issues with shadowSetup and fakeRootCommands
  # See: https://nixos.org/manual/nixpkgs/stable/#sec-pkgs-dockerTools
  user = "nginx";
  group = "nginx";
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
    "${user}:x:${uid}:${gid}:Nginx user:/var/cache/nginx:/bin/false\n";

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

  # Entrypoint script that runs both services
  entrypoint = pkgs.writeShellScript "entrypoint" ''
    set -euo pipefail

    echo "HLS Origin Container - Profile: ${config._profile.name}"
    echo "Segment duration: ${toString config.hls.segmentDuration}s"
    echo "Rolling window: ${toString config.hls.listSize} segments"
    echo "Delete threshold: ${toString config.hls.deleteThreshold}"
    echo ""

    mkdir -p ${config.server.hlsDir}

    # Set umask for group-readable files (FFmpeg writes, nginx reads)
    # 0022 = rwxr-xr-x (owner/group can read, owner can write)
    umask 0022

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
    etcContents   # Nix-idiomatic user/group files (no shadowSetup needed)
    pkgs.ffmpeg-full
    pkgs.nginx
    pkgs.coreutils
    pkgs.bash
  ];

  config = {
    Cmd = [ "${entrypoint}" ];
    # Run as non-root user for security (port 17080 > 1024, no privileges needed)
    User = "${user}";
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

  # Context: extraCommands runs as build user (non-root) in build sandbox
  # - Creates directory structure relative to build sandbox
  # - Can set permissions but cannot chown (no root privileges)
  # - Directories created here are visible to fakeRootCommands via relative paths
  extraCommands = ''
    mkdir -p var/hls tmp var/log/nginx var/cache/nginx
    # Set permissions (ownership will be set in fakeRootCommands)
    chmod 755 var/hls var/log/nginx var/cache/nginx
  '';

  # Context: fakeRootCommands runs as root in build sandbox
  # - Can use chown/chgrp to set ownership
  # - Must use relative paths (no leading /) to reference extraCommands directories
  # - These paths become /var/* in the final container image
  fakeRootCommands = ''
    #!${pkgs.runtimeShell}
    # Set ownership so nginx user can write logs and cache
    # Use relative paths (no leading /) to reference directories from extraCommands
    chown -R ${uid}:${gid} var/log/nginx var/cache/nginx
    # HLS directory owned by root:nginx (group-writable for FFmpeg to write segments)
    # FFmpeg runs as nginx user and needs to write segments, nginx reads them
    chown root:${gid} var/hls
    chmod 775 var/hls
  '';
}
