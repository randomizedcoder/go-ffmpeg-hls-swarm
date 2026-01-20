# go-ffmpeg-hls-swarm client deployment - Entry point
# See: docs/CLIENT_DEPLOYMENT.md for detailed documentation
#
# Usage:
#   # Default profile
#   swarmClient = import ./swarm-client { inherit pkgs lib swarmBinary; };
#
#   # Specific profile
#   swarmClient = import ./swarm-client { inherit pkgs lib swarmBinary; profile = "stress"; };
#
#   # With overrides
#   swarmClient = import ./swarm-client {
#     inherit pkgs lib swarmBinary;
#     profile = "default";
#     configOverrides = { clients = 100; };
#   };
#
{ pkgs, lib, swarmBinary, profile ? "default", configOverrides ? {} }:

let
  # Load configuration with profile and overrides
  config = import ./config.nix {
    inherit profile;
    overrides = configOverrides;
  };

  # Initialize components with config
  runner = import ./runner.nix { inherit pkgs lib config swarmBinary; };
  container = import ./container.nix { inherit pkgs lib config swarmBinary; };
  nixosModule = import ./nixos-module.nix { inherit config swarmBinary; };

  # MicroVM requires nixosModule
  microvm = import ./microvm.nix { inherit pkgs lib config swarmBinary nixosModule; };

  # Kernel tuning module (can be imported separately)
  sysctlModule = ./sysctl.nix;

in {
  # Export configuration for inspection
  inherit config;

  # Local development runner script
  inherit runner;

  # OCI container image
  inherit container;

  # NixOS module for systemd service
  inherit nixosModule;

  # MicroVM configuration
  inherit microvm;

  # Standalone sysctl module (for external use)
  inherit sysctlModule;

  # Convenience aliases
  runLocal = runner;
  ociImage = container;
  vm = microvm;

  # Export swarm binary for reference
  binary = swarmBinary;

  # Available profiles
  availableProfiles = config._profile.availableProfiles;
  currentProfile = config._profile.name;

  # Derived values for inspection
  derived = config.derived;

  # Quick access to key metrics
  summary = {
    profile = config._profile.name;
    clients = config.clients;
    rampRate = config.rampRate;
    metricsPort = config.metricsPort;
    estimatedMemoryMB = config.derived.estimatedMemoryMB;
    recommendedFdLimit = config.derived.recommendedFdLimit;
  };
}
