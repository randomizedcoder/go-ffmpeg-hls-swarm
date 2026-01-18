# Runnable apps
{ pkgs, lib, meta, package, welcome-app }:
let
  mkApp = drv: {
    type = "app";
    program = lib.getExe drv;
  };
in
{
  default = mkApp welcome-app;
  welcome = mkApp welcome-app;

  build = mkApp (pkgs.writeShellApplication {
    name = "${meta.pname}-build";
    text = ''
      echo "Building ${meta.pname}..."
      nix build --print-out-paths
    '';
  });

  run = mkApp (pkgs.writeShellApplication {
    name = "${meta.pname}-run";
    runtimeInputs = meta.runtimeDeps;
    text = ''exec ${lib.getExe package} "$@"'';
  });
}
