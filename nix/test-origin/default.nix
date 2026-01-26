# Test HLS origin server - Entry point
# See: docs/TEST_ORIGIN.md for detailed documentation
#
# Usage:
#   # Default profile
#   testOrigin = import ./test-origin { inherit pkgs lib; };
#
#   # Specific profile
#   testOrigin = import ./test-origin { inherit pkgs lib; profile = "low-latency"; };
#
#   # With overrides
#   testOrigin = import ./test-origin {
#     inherit pkgs lib;
#     profile = "default";
#     configOverrides = { hls.listSize = 15; };
#   };
#
#   # With MicroVM support (requires microvm flake input)
#   testOrigin = import ./test-origin {
#     inherit pkgs lib;
#     microvm = inputs.microvm;  # Pass the microvm flake
#   };
#
{ pkgs, lib, meta, profile ? "default", configOverrides ? {}, microvm ? null }:

let
  # Load configuration with profile and overrides
  config = import ./config.nix {
    inherit profile;
    overrides = configOverrides;
    lib = lib;  # nixpkgs lib for standard functions
    meta = meta;  # custom lib for mkProfileSystem, deepMerge, etc.
  };

  # Initialize components with config
  ffmpeg = import ./ffmpeg.nix { inherit pkgs lib config; };
  nginx = import ./nginx.nix { inherit pkgs lib config; };
  runner = import ./runner.nix { inherit pkgs lib config ffmpeg nginx; };
  container = import ./container.nix { inherit pkgs lib config ffmpeg nginx; };
  nixosModule = import ./nixos-module.nix { inherit config ffmpeg nginx; };

  # MicroVM (only available if microvm input is provided)
  # Note: We need to pass nixpkgs for lib.nixosSystem
  microvmModule = if microvm != null then
    import ./microvm.nix {
      inherit pkgs lib config nixosModule microvm;
      nixpkgs = microvm.inputs.nixpkgs;  # Get nixpkgs from microvm's inputs
    }
  else
    null;

  # Kernel tuning module (can be imported separately)
  sysctlModule = ./sysctl.nix;

in {
  # Export configuration for inspection
  inherit config;

  # Export individual components
  inherit ffmpeg nginx;

  # Combined runner script for local development
  inherit runner;

  # OCI container image
  inherit container;

  # NixOS module for services
  inherit nixosModule;

  # MicroVM support (null if microvm input not provided)
  microvm = microvmModule;

  # Kernel tuning module (standalone, can be imported separately)
  inherit sysctlModule;

  # Convenience aliases
  runLocal = runner;
  ociImage = container;

  # Quick access to scripts
  scripts = {
    hlsGenerator = ffmpeg.script;
    nginxServer = nginx.script;
    combined = runner;
  };

  # Export cache control headers for external use
  cacheHeaders = {
    segment = nginx.segmentCacheControl;
    manifest = nginx.manifestCacheControl;
    master = nginx.masterCacheControl;
  };

  # Export mkFfmpegArgs for integration tests
  inherit (ffmpeg) mkFfmpegArgs mkMultiBitrateArgs;

  # Available profiles
  availableProfiles = config._profile.availableProfiles;
  currentProfile = config._profile.name;

  # Derived values for inspection
  derived = config.derived;

  # Check if MicroVM support is available
  hasMicrovm = microvm != null;
}
