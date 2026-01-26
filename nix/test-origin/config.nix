# Test origin configuration - Function-based with profile support
# See: docs/TEST_ORIGIN.md for detailed documentation
#
# Usage:
#   config = import ./config.nix { profile = "default"; lib = lib; }
#   config = import ./config.nix { profile = "low-latency"; lib = lib; }
#   config = import ./config.nix { profile = "4k-abr"; overrides = { hls.listSize = 15; }; lib = lib; }
#
{ profile ? "default", overrides ? {}, lib, meta }:

let
  # Import split modules
  profiles = import ./config/profiles.nix;
  baseConfig = import ./config/base.nix;

  # Use generic profile system
  profileSystem = meta.mkProfileSystem {
    base = baseConfig;
    inherit profiles;
  };
  
  # Get merged config
  mergedConfig = profileSystem.getConfig profile overrides;

  # Import derived and cache calculations
  derived = import ./config/derived.nix { config = mergedConfig; };
  cache = import ./config/cache.nix { config = mergedConfig; };

  # Shortcuts for convenience
  h = mergedConfig.hls;
  v = mergedConfig.video;
  enc = mergedConfig.encoder;

in mergedConfig // {
  # Export derived calculations
  inherit derived cache;

  # Computed encoder values
  encoder = mergedConfig.encoder // {
    gopSize = derived.gopSize;
  };

  # Export profile info (already included by getConfig, but explicit for clarity)
  _profile = mergedConfig._profile;

  # HLS flags string (convenience)
  hlsFlags = lib.concatStringsSep "+" h.flags;
}
