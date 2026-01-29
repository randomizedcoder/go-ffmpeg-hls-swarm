# Single source of truth for swarm-client profile names
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
