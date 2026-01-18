# Package derivation
{ pkgs, lib, meta, src }:
pkgs.buildGoModule {
  inherit (meta) pname version subPackages;
  inherit src;

  # Update after adding dependencies: nix build 2>&1 | grep "got:" | awk '{print $2}'
  vendorHash = null;

  ldflags = [ "-s" "-w" "-X main.version=${meta.version}" ];

  meta = with lib; {
    inherit (meta) description homepage mainProgram;
    license = licenses.mit;
    platforms = platforms.unix;
  };
}
