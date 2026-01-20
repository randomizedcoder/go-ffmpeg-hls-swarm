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
#   flake.nix           - Entry point (this file)
#   nix/lib.nix         - Shared metadata and helpers
#   nix/package.nix     - buildGoModule derivation
#   nix/shell.nix       - Development shell
#   nix/checks.nix      - Go linting/testing checks
#   nix/apps.nix        - Runnable app definitions
#   nix/tests/          - NixOS integration tests
#   nix/test-origin/    - Test HLS origin server components
#   nix/swarm-client/   - Client deployment (container, MicroVM)
#
# Test Origin Server:
#   nix run .#test-origin                 # Run local test origin (default profile)
#   nix run .#test-origin-low-latency     # Run with low-latency profile
#   nix run .#test-origin-4k-abr          # Run with 4K ABR profile
#   nix build .#test-origin-container     # Build OCI container
#
# Swarm Client (Load Tester):
#   nix run .#swarm-client                # Run with default profile (50 clients)
#   nix run .#swarm-client-stress         # Run with stress profile (200 clients)
#   nix build .#swarm-client-container    # Build OCI container
#
# Available profiles:
#   test-origin: default, low-latency, 4k-abr, stress-test
#   swarm-client: default, stress, gentle, burst, extreme
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

        appsBase = import ./nix/apps.nix {
          inherit pkgs lib meta package;
          welcome-app = shell.welcome-app;
        };

        goChecks = import ./nix/checks.nix {
          inherit pkgs lib meta src package;
        };

        # Test origin server components (with profile support)
        testOrigin = import ./nix/test-origin { inherit pkgs lib; };
        testOriginLowLatency = import ./nix/test-origin { inherit pkgs lib; profile = "low-latency"; };
        testOrigin4kAbr = import ./nix/test-origin { inherit pkgs lib; profile = "4k-abr"; };
        testOriginStress = import ./nix/test-origin { inherit pkgs lib; profile = "stress-test"; };

        # Swarm client components (with profile support)
        swarmClient = import ./nix/swarm-client { inherit pkgs lib; swarmBinary = package; };
        swarmClientStress = import ./nix/swarm-client { inherit pkgs lib; swarmBinary = package; profile = "stress"; };
        swarmClientGentle = import ./nix/swarm-client { inherit pkgs lib; swarmBinary = package; profile = "gentle"; };
        swarmClientBurst = import ./nix/swarm-client { inherit pkgs lib; swarmBinary = package; profile = "burst"; };
        swarmClientExtreme = import ./nix/swarm-client { inherit pkgs lib; swarmBinary = package; profile = "extreme"; };

      in
      {
        formatter = pkgs.nixfmt;

        packages = {
          ${meta.pname} = package;
          default = package;

          # Test origin server packages (default profile)
          test-origin = testOrigin.runner;
          test-origin-container = testOrigin.container;

          # Profile-specific test origins
          test-origin-low-latency = testOriginLowLatency.runner;
          test-origin-4k-abr = testOrigin4kAbr.runner;
          test-origin-stress = testOriginStress.runner;

          # Swarm client packages (default profile)
          swarm-client = swarmClient.runner;
          swarm-client-container = swarmClient.container;

          # Profile-specific swarm clients
          swarm-client-stress = swarmClientStress.runner;
          swarm-client-gentle = swarmClientGentle.runner;
          swarm-client-burst = swarmClientBurst.runner;
          swarm-client-extreme = swarmClientExtreme.runner;
        };

        devShells = {
          inherit (shell) default;
        };

        apps = appsBase // {
          # Test origin server apps (different profiles)
          test-origin = {
            type = "app";
            program = "${testOrigin.runner}/bin/test-hls-origin";
          };
          test-origin-low-latency = {
            type = "app";
            program = "${testOriginLowLatency.runner}/bin/test-hls-origin";
          };
          test-origin-4k-abr = {
            type = "app";
            program = "${testOrigin4kAbr.runner}/bin/test-hls-origin";
          };
          test-origin-stress = {
            type = "app";
            program = "${testOriginStress.runner}/bin/test-hls-origin";
          };

          # Swarm client apps (different profiles)
          swarm-client = {
            type = "app";
            program = "${swarmClient.runner}/bin/swarm-client";
          };
          swarm-client-stress = {
            type = "app";
            program = "${swarmClientStress.runner}/bin/swarm-client";
          };
          swarm-client-gentle = {
            type = "app";
            program = "${swarmClientGentle.runner}/bin/swarm-client";
          };
          swarm-client-burst = {
            type = "app";
            program = "${swarmClientBurst.runner}/bin/swarm-client";
          };
          swarm-client-extreme = {
            type = "app";
            program = "${swarmClientExtreme.runner}/bin/swarm-client";
          };
        };

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
