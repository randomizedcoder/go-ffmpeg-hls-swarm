# Single source of truth for test-origin profile names
# All packages, apps, and scripts derive from this list
# This file contains ONLY the list of profile names, not their definitions
{ lib }:

let
  profiles = [
    "default"
    "low-latency"
    "4k-abr"
    "stress-test"
    "logged"
    "debug"
    "tap"
    "tap-logged"
  ];
  
  # Validation function: ensures profile exists
  validateProfile = profile:
    if lib.elem profile profiles then
      profile
    else
      throw "Unknown test-origin profile '${profile}'. Available: ${lib.concatStringsSep ", " profiles}";
in
{
  inherit profiles validateProfile;
}
