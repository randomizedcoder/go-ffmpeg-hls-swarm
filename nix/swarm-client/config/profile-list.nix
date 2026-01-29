# Single source of truth for swarm-client profile names
# All packages, apps, and scripts derive from this list
# This file contains ONLY the list of profile names, not their definitions
{ lib }:

let
  profiles = [
    "default"
    "stress"
    "gentle"
    "burst"
    "extreme"
  ];
  
  validateProfile = profile:
    if lib.elem profile profiles then
      profile
    else
      throw "Unknown swarm-client profile '${profile}'. Available: ${lib.concatStringsSep ", " profiles}";
in
{
  inherit profiles validateProfile;
}
