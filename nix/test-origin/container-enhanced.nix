# Enhanced OCI container with full NixOS systemd services
# Similar to MicroVM but runs in a container
# Uses the same nixos-module.nix for consistency
{ pkgs, lib, config, nixosModule, nixpkgs }:

let
  # Build minimal NixOS system with our module
  # The nixosModule is already created with config, ffmpeg, nginx in default.nix
  # So we just need to add container-specific settings
  nixos = nixpkgs.lib.nixosSystem {
    system = "x86_64-linux";
    modules = [
      # Minimal NixOS config for container
      ({ lib, ... }: {
        boot.isContainer = true;
        networking.hostName = "hls-origin";
        system.stateVersion = "24.11";
      })

      # Our shared NixOS module (already has config, ffmpeg, nginx)
      nixosModule
    ];
  };

  # Extract the system closure
  systemClosure = nixos.config.system.build.toplevel;

in
pkgs.dockerTools.buildLayeredImage {
  name = "go-ffmpeg-hls-swarm-test-origin-enhanced";
  tag = "latest";

  # Use the NixOS system closure in contents (not fromImage, which expects a tarball)
  contents = [ systemClosure ];

  # Layer optimization
  maxLayers = 100;

  config = {
    Cmd = [ "/init" ];  # NixOS init system
    ExposedPorts = {
      "${toString config.server.port}/tcp" = {};  # HLS origin server
      "9100/tcp" = {};  # Node exporter
      "9113/tcp" = {};  # Nginx exporter
    };

    # Healthcheck for container orchestration
    # Checks the /health endpoint on the origin server
    Healthcheck = {
      Test = [ "CMD" "curl" "-f" "http://localhost:${toString config.server.port}/health" "||" "exit" "1" ];
      Interval = 30000000000;  # 30 seconds (nanoseconds)
      Timeout = 5000000000;    # 5 seconds
      StartPeriod = 30000000000;  # 30 seconds grace period (systemd needs time to start)
      Retries = 3;
    };

    Labels = {
      "org.opencontainers.image.title" = "go-ffmpeg-hls-swarm-test-origin-enhanced";
      "org.opencontainers.image.description" = "Test HLS origin with full NixOS systemd services";
      "org.opencontainers.image.source" = "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm";
      "org.opencontainers.image.documentation" = "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm/blob/main/docs/TEST_ORIGIN.md";
      "hls.profile" = config._profile.name;
    };
  };
}
