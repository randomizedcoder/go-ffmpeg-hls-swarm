# go-ffmpeg-hls-swarm Nix Flake
#
# Provides a reproducible development environment and build
#
# Usage:
#   nix develop                              # Enter dev shell
#   nix build                                # Build the binary
#   nix run                                  # Show welcome banner
#   nix run .#run -- ...                     # Run with args
#   nix flake check                          # Run all checks
#   nix fmt                                  # Format nix files
#
# Integration Testing (Linux only):
#   nix build .#checks.x86_64-linux.integration-test
#   nix build .#checks.x86_64-linux.integration-test.driverInteractive
#   # ^ Then: ./result/bin/nixos-test-driver --interactive
#
# File Structure:
#   flake.nix       - Entry point (this file)
#   nix/lib.nix     - Shared metadata and helpers
#   nix/package.nix - buildGoModule derivation
#   nix/shell.nix   - Development shell
#   nix/checks.nix  - Go linting/testing checks
#   nix/apps.nix    - Runnable app definitions
#   nix/tests/      - NixOS integration tests
#
{
  description = "HLS load testing tool using FFmpeg process swarm";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        lib = pkgs.lib;

        # Shared project metadata and helpers
        meta = import ./nix/lib.nix { inherit pkgs lib; };

        # Clean source, excluding build artifacts
        src = lib.cleanSourceWith {
          src = lib.cleanSource ./.;
          filter =
            path: type:
            let
              baseName = builtins.baseNameOf path;
              ignoredPaths = [
                ".direnv"
                "result"
                ".go"
                ".git"
                ".vscode"
                ".cursor"
              ];
            in
            !(builtins.elem baseName ignoredPaths);
        };

        # Import modular components
        package = import ./nix/package.nix {
          inherit pkgs lib meta src;
        };

        shell = import ./nix/shell.nix {
          inherit pkgs lib meta;
        };

        apps = import ./nix/apps.nix {
          inherit pkgs lib meta package;
          welcome-app = shell.welcome-app;
        };

        goChecks = import ./nix/checks.nix {
          inherit pkgs lib meta src package;
        };

      in
      {
        formatter = pkgs.nixfmt;

        packages = {
          ${meta.pname} = package;
          default = package;
        };

        devShells = {
          inherit (shell) default;
        };

        inherit apps;

        checks =
          goChecks
          // lib.optionalAttrs pkgs.stdenv.isLinux {
            integration-test = import ./nix/tests/integration.nix {
              inherit pkgs self;
            };
          };
      }
    )
    // {
      overlays.default = final: prev: {
        go-ffmpeg-hls-swarm = self.packages.${final.system}.default;
      };
    };
}
