# Derived values computed from merged config
{ config }:

let
  h = config.hls;
  v = config.video;
  enc = config.encoder;

  parseBitrate = str:
    let
      stripped = builtins.replaceStrings ["k" "K" "m" "M"] ["" "" "" ""] str;
      num = builtins.fromJSON stripped;
      multiplier = if builtins.match ".*[mM].*" str != null then 1000 else 1;
    in num * multiplier;

  totalBitrateKbps = parseBitrate v.bitrate + parseBitrate v.audioBitrate;
  segmentSizeKB = (totalBitrateKbps * h.segmentDuration) / 8;
  filesPerVariant = h.listSize + h.deleteThreshold + 1;
  storagePerVariantMB = (segmentSizeKB * filesPerVariant) / 1024;
  variantCount = if config.multibitrate
                 then builtins.length config.variants
                 else 1;
  totalStorageMB = storagePerVariantMB * variantCount;
  playlistWindowSec = h.listSize * h.segmentDuration;
  recommendedTmpfsMB = let
    bitrateBytes = totalBitrateKbps / 8;  # KB/s
    windowBytes = bitrateBytes * playlistWindowSec;  # KB
    safetyBuffer = windowBytes * 2;  # Double buffer
    perVariant = safetyBuffer * variantCount;
    inMB = perVariant / 1024;
  in builtins.ceil (inMB + 64);
in {
  gopSize = enc.framerate * h.segmentDuration;
  segmentLifetimeSec = (h.listSize + h.deleteThreshold) * h.segmentDuration;
  playlistWindowSec = playlistWindowSec;
  parseBitrate = parseBitrate;
  totalBitrateKbps = totalBitrateKbps;
  segmentSizeKB = segmentSizeKB;
  filesPerVariant = filesPerVariant;
  storagePerVariantMB = storagePerVariantMB;
  variantCount = variantCount;
  totalStorageMB = totalStorageMB;
  recommendedTmpfsMB = recommendedTmpfsMB;
}
