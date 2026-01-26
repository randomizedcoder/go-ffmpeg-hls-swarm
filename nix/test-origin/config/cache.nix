# Cache timing configuration (dynamically calculated from segment duration)
{ config }:

let
  h = config.hls;
in {
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
}
