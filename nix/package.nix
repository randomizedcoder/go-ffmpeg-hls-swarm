# Package derivation
#
# Build Configuration:
# - Uses buildGoModule which requires a vendor/ directory in the source
# - The vendor/ directory must be committed to git (created via: go mod vendor)
# - vendorHash = null allows buildGoModule to compute the hash automatically
# - This ensures reproducible builds by locking all dependency versions
# - Both Nix (via vendorHash) and Go (via go.sum) provide dependency locking
{ pkgs, lib, meta, src }:
pkgs.buildGoModule {
  inherit (meta) pname version subPackages;
  inherit src;

  # vendorHash = null: buildGoModule will compute the hash automatically
  # If you need to update after adding dependencies, run:
  #   nix build 2>&1 | grep "got:" | awk '{print $2}'
  vendorHash = null;

  ldflags = [ "-s" "-w" "-X main.version=${meta.version}" ];

  meta = with lib; {
    inherit (meta) description homepage mainProgram;
    license = licenses.mit;
    platforms = platforms.unix;
  };
}
