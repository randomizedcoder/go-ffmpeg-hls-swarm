# Shared project metadata and helpers
{ pkgs, lib }:
rec {
  # Project identity
  pname = "go-ffmpeg-hls-swarm";
  version = "0.1.0";
  description = "HLS load testing tool using FFmpeg";
  homepage = "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm";
  mainProgram = pname;
  subPackages = [ "cmd/${pname}" ];

  # Development packages
  goToolchain = with pkgs; [ go gopls gotools golangci-lint delve ];
  runtimeDeps = with pkgs; [ ffmpeg-full ];
  devUtils = with pkgs; [ curl jq nil ];

  # Deep merge two attribute sets, recursively merging nested sets
  # Used for merging base config, profile config, and overrides
  deepMerge = base: overlay:
    let
      mergeAttr = name:
        if builtins.isAttrs (base.${name} or null) && builtins.isAttrs (overlay.${name} or null)
        then deepMerge base.${name} overlay.${name}
        else overlay.${name} or base.${name} or null;
      allKeys = builtins.attrNames (base // overlay);
    in builtins.listToAttrs (map (name: { inherit name; value = mergeAttr name; }) allKeys);

  # Generic profile system builder
  # Creates a reusable profile framework for components
  #
  # Usage:
  #   profileSystem = lib.mkProfileSystem {
  #     base = { ... };  # Base configuration
  #     profiles = { default = { ... }; low-latency = { ... }; ... };
  #   };
  #   config = profileSystem.getConfig "default" {};
  #
  mkProfileSystem = { base, profiles }:
    rec {
      # Get config for a profile with optional overrides
      getConfig = profile: overrides:
        let
          available = builtins.attrNames profiles;
          profileConfig = profiles.${profile} or (
            throw ''
              Unknown profile: ${profile}

              Available profiles:
              ${lib.concatMapStringsSep "\n" (p: "  - ${p}") available}
            ''
          );
          merged = deepMerge (deepMerge base profileConfig) overrides;
        in merged // {
          _profile = {
            name = profile;
            availableProfiles = available;
          };
        };

      # List all available profiles
      listProfiles = builtins.attrNames profiles;

      # Validate profile exists
      validateProfile = profile:
        let
          available = builtins.attrNames profiles;
        in
        if builtins.hasAttr profile profiles
        then true
        else throw ''
          Unknown profile: ${profile}

          Available profiles:
          ${lib.concatMapStringsSep "\n" (p: "  - ${p}") available}
        '';
    };

  # Check derivation helper
  mkGoCheck =
    { src, name, script }:
    pkgs.stdenvNoCC.mkDerivation {
      name = "${pname}-${name}";
      inherit src;
      nativeBuildInputs = with pkgs; [ go golangci-lint ];
      buildPhase = ''
        runHook preBuild
        export HOME=$TMPDIR GOPATH=$TMPDIR/go GOCACHE=$TMPDIR/go-cache
        ${script}
        runHook postBuild
      '';
      installPhase = ''
        mkdir -p $out
        echo "${name} passed" > $out/result
      '';
    };
}
