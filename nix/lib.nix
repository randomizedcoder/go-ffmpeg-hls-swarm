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
