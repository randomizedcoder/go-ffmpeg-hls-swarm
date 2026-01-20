# FFmpeg HLS stream generator with modular argument builder
# See: docs/TEST_ORIGIN.md for detailed documentation
#
# Key export: mkFfmpegArgs - a function to build FFmpeg arguments
# This enables easy overriding in integration tests without rewriting strings.
#
{ pkgs, lib, config }:

let
  cfg = config;
  enc = cfg.encoder;
  a = cfg.audio;
  h = cfg.hls;
  v = cfg.video;

  # ═══════════════════════════════════════════════════════════════════════════
  # mkFfmpegArgs: Modular argument builder for FFmpeg HLS generation
  # ═══════════════════════════════════════════════════════════════════════════
  #
  # Usage:
  #   # Default args:
  #   args = mkFfmpegArgs {}
  #
  #   # Override output directory:
  #   args = mkFfmpegArgs { hlsDir = "/tmp/my-test"; }
  #
  #   # Override segment duration for testing:
  #   args = mkFfmpegArgs { segmentDuration = 1; }
  #
  mkFfmpegArgs = {
    # Allow overriding any setting
    hlsDir ? cfg.server.hlsDir,
    segmentDuration ? h.segmentDuration,
    listSize ? h.listSize,
    deleteThreshold ? h.deleteThreshold,
    flags ? h.flags,
    segmentPattern ? h.segmentPattern,
    playlistName ? h.playlistName,
    masterPlaylist ? h.masterPlaylist,
    testPattern ? cfg.testPattern,
    width ? v.width,
    height ? v.height,
    bitrate ? v.bitrate,
    maxrate ? v.maxrate,
    bufsize ? v.bufsize,
    audioBitrate ? v.audioBitrate,
    framerate ? enc.framerate,
    preset ? enc.preset,
    tune ? enc.tune,
    profile ? enc.profile,
    level ? enc.level,
    audioFrequency ? a.frequency,
    audioSampleRate ? a.sampleRate,
    # Extra flags to append
    extraArgs ? [],
  }:
  let
    gopSize = framerate * segmentDuration;
    hlsFlags = lib.concatStringsSep "+" flags;
  in [
    "-re"
    "-f" "lavfi"
    "-i" "${testPattern}=size=${toString width}x${toString height}:rate=${toString framerate}:duration=0"
    "-f" "lavfi"
    "-i" "sine=frequency=${toString audioFrequency}:sample_rate=${toString audioSampleRate}:duration=0"
    "-c:v" "libx264"
    "-preset" preset
    "-tune" tune
    "-profile:v" profile
    "-level" level
    "-g" (toString gopSize)
    "-keyint_min" (toString gopSize)
    "-sc_threshold" "0"
    "-b:v" bitrate
    "-maxrate" maxrate
    "-bufsize" bufsize
    "-c:a" "aac"
    "-b:a" audioBitrate
    "-ar" (toString audioSampleRate)
    "-f" "hls"
    "-hls_time" (toString segmentDuration)
    "-hls_list_size" (toString listSize)
    "-hls_delete_threshold" (toString deleteThreshold)
    "-hls_flags" hlsFlags
    "-hls_segment_filename" "${hlsDir}/${segmentPattern}"
    "${hlsDir}/${playlistName}"
  ] ++ extraArgs;

  # ═══════════════════════════════════════════════════════════════════════════
  # mkMultiBitrateArgs: Build args for ABR ladder
  # ═══════════════════════════════════════════════════════════════════════════
  mkMultiBitrateArgs = {
    hlsDir ? cfg.server.hlsDir,
    segmentDuration ? h.segmentDuration,
    listSize ? h.listSize,
    deleteThreshold ? h.deleteThreshold,
    flags ? h.flags,
    segmentPattern ? h.segmentPattern,
    playlistName ? h.playlistName,
    masterPlaylist ? h.masterPlaylist,
    testPattern ? cfg.testPattern,
    variants ? cfg.variants,
    framerate ? enc.framerate,
    preset ? enc.preset,
    tune ? enc.tune,
    profile ? enc.profile,
    audioFrequency ? a.frequency,
    audioSampleRate ? a.sampleRate,
    extraArgs ? [],
  }:
  let
    gopSize = framerate * segmentDuration;
    hlsFlags = lib.concatStringsSep "+" flags;
    numVariants = builtins.length variants;
    maxRes = builtins.head variants;

    # Build filter_complex for scaling
    filterComplex = let
      splits = lib.concatMapStringsSep "" (i: "[v${toString i}]") (lib.range 0 (numVariants - 1));
      scales = lib.concatMapStringsSep "; " (i:
        let vr = builtins.elemAt variants i;
        in "[v${toString i}]scale=${toString vr.width}:${toString vr.height}[out${toString i}]"
      ) (lib.range 0 (numVariants - 1));
    in "[0:v]split=${toString numVariants}${splits}; ${scales}";

    # Build -map and encoding options for each variant
    variantEncoderArgs = lib.concatMap (i:
      let vr = builtins.elemAt variants i;
      in [
        "-map" "[out${toString i}]"
        "-map" "1:a"
        "-c:v:${toString i}" "libx264"
        "-preset" preset
        "-tune" tune
        "-profile:v:${toString i}" profile
        "-b:v:${toString i}" vr.bitrate
        "-maxrate:v:${toString i}" vr.maxrate
        "-bufsize:v:${toString i}" vr.bufsize
        "-g:v:${toString i}" (toString gopSize)
        "-keyint_min:v:${toString i}" (toString gopSize)
        "-sc_threshold:v:${toString i}" "0"
        "-c:a:${toString i}" "aac"
        "-b:a:${toString i}" vr.audioBitrate
        "-ar:a:${toString i}" (toString audioSampleRate)
      ]
    ) (lib.range 0 (numVariants - 1));

    # Build var_stream_map
    varStreamMap = lib.concatMapStringsSep " " (i:
      let vr = builtins.elemAt variants i;
      in "v:${toString i},a:${toString i},name:${vr.name}"
    ) (lib.range 0 (numVariants - 1));

  in [
    "-re"
    "-f" "lavfi"
    "-i" "${testPattern}=size=${toString maxRes.width}x${toString maxRes.height}:rate=${toString framerate}:duration=0"
    "-f" "lavfi"
    "-i" "sine=frequency=${toString audioFrequency}:sample_rate=${toString audioSampleRate}:duration=0"
    "-filter_complex" filterComplex
  ] ++ variantEncoderArgs ++ [
    "-f" "hls"
    "-hls_time" (toString segmentDuration)
    "-hls_list_size" (toString listSize)
    "-hls_delete_threshold" (toString deleteThreshold)
    "-hls_flags" hlsFlags
    "-var_stream_map" varStreamMap
    "-master_pl_name" masterPlaylist
    "-hls_segment_filename" "${hlsDir}/%v/${segmentPattern}"
    "${hlsDir}/%v/${playlistName}"
  ] ++ extraArgs;

  # ═══════════════════════════════════════════════════════════════════════════
  # Pre-built argument lists (using defaults from config)
  # ═══════════════════════════════════════════════════════════════════════════
  singleBitrateArgs = mkFfmpegArgs {};
  multiBitrateArgs = mkMultiBitrateArgs {};

  # Select based on config
  ffmpegArgs = if cfg.multibitrate then multiBitrateArgs else singleBitrateArgs;

  variants = cfg.variants;
  numVariants = builtins.length variants;

in rec {
  # ═══════════════════════════════════════════════════════════════════════════
  # Exports
  # ═══════════════════════════════════════════════════════════════════════════

  # Primary export: argument builder functions
  inherit mkFfmpegArgs mkMultiBitrateArgs;

  # Pre-built args for convenience
  inherit ffmpegArgs singleBitrateArgs multiBitrateArgs;

  # HLS flags string
  hlsFlags = lib.concatStringsSep "+" h.flags;

  # Shell script for standalone use
  script = pkgs.writeShellScript "hls-generator" ''
    set -euo pipefail
    mkdir -p ${cfg.server.hlsDir}
    ${lib.optionalString cfg.multibitrate ''
      # Create variant directories for multi-bitrate
      ${lib.concatMapStringsSep "\n" (vr: "mkdir -p ${cfg.server.hlsDir}/${vr.name}") variants}
    ''}
    exec ${pkgs.ffmpeg-full}/bin/ffmpeg \
      ${lib.concatStringsSep " \\\n      " (map lib.escapeShellArg ffmpegArgs)}
  '';

  # Systemd service configuration
  systemdService = {
    description = "FFmpeg HLS Test Stream Generator (${if cfg.multibitrate then "${toString numVariants} variants" else "single bitrate"})";
    after = [ "network.target" ];
    wantedBy = [ "multi-user.target" ];
    serviceConfig = {
      Type = "simple";
      ExecStartPre = pkgs.writeShellScript "hls-generator-pre" ''
        mkdir -p ${cfg.server.hlsDir}
        ${lib.optionalString cfg.multibitrate ''
          ${lib.concatMapStringsSep "\n" (vr: "mkdir -p ${cfg.server.hlsDir}/${vr.name}") variants}
        ''}
      '';
      ExecStart = "${pkgs.ffmpeg-full}/bin/ffmpeg ${lib.concatStringsSep " " (map lib.escapeShellArg ffmpegArgs)}";
      Restart = "always";
      RestartSec = 2;
    };
  };

  # Runtime inputs - include ffprobe for stream verification
  runtimeInputs = [ pkgs.ffmpeg-full ];

  # Export for inspection
  mode = if cfg.multibitrate then "multibitrate" else "single";
  variantCount = if cfg.multibitrate then numVariants else 1;
}
