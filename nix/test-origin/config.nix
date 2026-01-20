# Test origin configuration - Function-based with profile support
# See: docs/TEST_ORIGIN.md for detailed documentation
#
# Usage:
#   config = import ./config.nix { profile = "default"; }
#   config = import ./config.nix { profile = "low-latency"; }
#   config = import ./config.nix { profile = "4k-abr"; overrides = { hls.listSize = 15; }; }
#
{ profile ? "default", overrides ? {} }:

let
  # ═══════════════════════════════════════════════════════════════════════════
  # Profile definitions
  # ═══════════════════════════════════════════════════════════════════════════
  profiles = {
    # Standard testing profile - balanced latency and safety
    default = {
      hls.segmentDuration = 2;
      hls.listSize = 10;
      video = {
        width = 1280;
        height = 720;
        bitrate = "2000k";
        maxrate = "2200k";
        bufsize = "4000k";
        audioBitrate = "128k";
      };
      encoder.framerate = 30;
      multibitrate = false;
    };

    # Low-latency profile - 1s segments for fast response
    low-latency = {
      hls.segmentDuration = 1;
      hls.listSize = 6;
      video = {
        width = 1280;
        height = 720;
        bitrate = "2000k";
        maxrate = "2500k";  # Higher maxrate for bursty encoding
        bufsize = "2000k";  # Smaller buffer for lower latency
        audioBitrate = "96k";
      };
      encoder = {
        framerate = 30;
        preset = "ultrafast";
        tune = "zerolatency";
      };
      multibitrate = false;
    };

    # 4K ABR profile - Multi-bitrate adaptive streaming
    "4k-abr" = {
      hls.segmentDuration = 2;
      hls.listSize = 10;
      multibitrate = true;
      variants = [
        {
          name = "2160p";
          width = 3840;
          height = 2160;
          bitrate = "15000k";
          maxrate = "16500k";
          bufsize = "30000k";
          audioBitrate = "192k";
        }
        {
          name = "1080p";
          width = 1920;
          height = 1080;
          bitrate = "5000k";
          maxrate = "5500k";
          bufsize = "10000k";
          audioBitrate = "128k";
        }
        {
          name = "720p";
          width = 1280;
          height = 720;
          bitrate = "2000k";
          maxrate = "2200k";
          bufsize = "4000k";
          audioBitrate = "128k";
        }
        {
          name = "480p";
          width = 854;
          height = 480;
          bitrate = "1000k";
          maxrate = "1100k";
          bufsize = "2000k";
          audioBitrate = "96k";
        }
        {
          name = "360p";
          width = 640;
          height = 360;
          bitrate = "500k";
          maxrate = "550k";
          bufsize = "1000k";
          audioBitrate = "64k";
        }
      ];
      encoder.framerate = 30;
      # Use first variant for single bitrate settings
      video = {
        width = 1280;
        height = 720;
        bitrate = "2000k";
        maxrate = "2200k";
        bufsize = "4000k";
        audioBitrate = "128k";
      };
    };

    # High-load stress test profile - optimized for stability
    stress-test = {
      hls.segmentDuration = 2;
      hls.listSize = 15;  # Larger window for more stability
      video = {
        width = 1280;
        height = 720;
        bitrate = "1500k";  # Lower bitrate = faster encoding
        maxrate = "1650k";
        bufsize = "3000k";
        audioBitrate = "96k";
      };
      encoder = {
        framerate = 25;  # Lower framerate for less CPU
        preset = "ultrafast";
        tune = "zerolatency";
      };
      multibitrate = false;
    };
  };

  # ═══════════════════════════════════════════════════════════════════════════
  # Base configuration (defaults for all profiles)
  # ═══════════════════════════════════════════════════════════════════════════
  baseConfig = {
    # Mode selection
    multibitrate = false;

    # HLS settings
    hls = {
      segmentDuration = 2;
      listSize = 10;
      deleteThreshold = 5;  # INCREASED: Safe buffer for SWR/CDN lag
      segmentPattern = "seg%05d.ts";
      playlistName = "stream.m3u8";
      masterPlaylist = "master.m3u8";

      # FFmpeg HLS flags
      flags = [
        "delete_segments"
        "omit_endlist"
        "temp_file"
      ];
    };

    # Server settings
    server = {
      port = 8080;
      hlsDir = "/var/hls";
    };

    # Audio settings
    audio = {
      frequency = 1000;
      sampleRate = 48000;
    };

    testPattern = "testsrc2";

    # Video settings (single bitrate)
    video = {
      width = 1280;
      height = 720;
      bitrate = "2000k";
      maxrate = "2200k";
      bufsize = "4000k";
      audioBitrate = "128k";
    };

    # Default variants (ABR)
    variants = [
      {
        name = "720p";
        width = 1280;
        height = 720;
        bitrate = "2000k";
        maxrate = "2200k";
        bufsize = "4000k";
        audioBitrate = "128k";
      }
      {
        name = "360p";
        width = 640;
        height = 360;
        bitrate = "500k";
        maxrate = "550k";
        bufsize = "1000k";
        audioBitrate = "64k";
      }
    ];

    # Encoder settings
    encoder = {
      framerate = 30;
      preset = "ultrafast";
      tune = "zerolatency";
      profile = "baseline";
      level = "3.1";
    };
  };

  # ═══════════════════════════════════════════════════════════════════════════
  # Deep merge function for nested attribute sets
  # ═══════════════════════════════════════════════════════════════════════════
  deepMerge = base: overlay:
    let
      mergeAttr = name:
        if builtins.isAttrs (base.${name} or null) && builtins.isAttrs (overlay.${name} or null)
        then deepMerge base.${name} overlay.${name}
        else overlay.${name} or base.${name} or null;
      allKeys = builtins.attrNames (base // overlay);
    in builtins.listToAttrs (map (name: { inherit name; value = mergeAttr name; }) allKeys);

  # Merge: base <- profile <- overrides
  profileConfig = profiles.${profile} or {};
  mergedConfig = deepMerge (deepMerge baseConfig profileConfig) overrides;

  # ═══════════════════════════════════════════════════════════════════════════
  # Derived values (computed from merged config)
  # ═══════════════════════════════════════════════════════════════════════════
  h = mergedConfig.hls;
  v = mergedConfig.video;
  enc = mergedConfig.encoder;

  derived = {
    # GOP size = framerate × segment duration
    gopSize = enc.framerate * h.segmentDuration;

    # Segment lifetime = (listSize + deleteThreshold) × segmentDuration
    segmentLifetimeSec = (h.listSize + h.deleteThreshold) * h.segmentDuration;

    # Playlist window duration
    playlistWindowSec = h.listSize * h.segmentDuration;

    # Parse bitrate string to integer (kbps)
    parseBitrate = str:
      let
        stripped = builtins.replaceStrings ["k" "K" "m" "M"] ["" "" "" ""] str;
        num = builtins.fromJSON stripped;
        multiplier = if builtins.match ".*[mM].*" str != null then 1000 else 1;
      in num * multiplier;

    # Total bitrate per variant (video + audio) in kbps
    totalBitrateKbps = derived.parseBitrate v.bitrate + derived.parseBitrate v.audioBitrate;

    # Segment size estimate
    segmentSizeKB = (derived.totalBitrateKbps * h.segmentDuration) / 8;

    # Files per variant = listSize + deleteThreshold + 1 (being written)
    filesPerVariant = h.listSize + h.deleteThreshold + 1;

    # Storage per variant
    storagePerVariantMB = (derived.segmentSizeKB * derived.filesPerVariant) / 1024;

    # Number of variants
    variantCount = if mergedConfig.multibitrate
                   then builtins.length mergedConfig.variants
                   else 1;

    # Total storage estimate
    totalStorageMB = derived.storagePerVariantMB * derived.variantCount;

    # Recommended tmpfs size: (Bitrate * Window * 2) + 64MB
    # Formula: (total_bitrate_kbps / 8 * playlist_window_sec * 2 * variant_count / 1024) + 64
    recommendedTmpfsMB = let
      bitrateBytes = derived.totalBitrateKbps / 8;  # KB/s
      windowBytes = bitrateBytes * derived.playlistWindowSec;  # KB
      safetyBuffer = windowBytes * 2;  # Double buffer
      perVariant = safetyBuffer * derived.variantCount;
      inMB = perVariant / 1024;
    in builtins.ceil (inMB + 64);
  };

  # ═══════════════════════════════════════════════════════════════════════════
  # Cache timing (dynamically calculated from segment duration)
  # ═══════════════════════════════════════════════════════════════════════════
  cache = {
    # Segments: immutable, cache for full lifetime + safety margin
    segment = {
      maxAge = 60;  # Segments are immutable; generous TTL is safe
      immutable = true;
      public = true;
    };

    # Manifests: TTL = segmentDuration / 2, SWR = segmentDuration
    manifest = {
      maxAge = h.segmentDuration / 2;  # Half segment duration
      staleWhileRevalidate = h.segmentDuration;  # Full segment duration
      public = true;
    };

    # Master playlist: rarely changes
    master = {
      maxAge = 5;
      staleWhileRevalidate = 10;
      public = true;
    };
  };

in mergedConfig // {
  # Export derived calculations
  inherit derived cache;

  # Computed encoder values
  encoder = mergedConfig.encoder // {
    gopSize = derived.gopSize;
  };

  # Export profile info
  _profile = {
    name = profile;
    availableProfiles = builtins.attrNames profiles;
  };

  # HLS flags string (convenience)
  hlsFlags = builtins.concatStringsSep "+" h.flags;
}
