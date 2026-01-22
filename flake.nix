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
#   test-origin: default, low-latency, 4k-abr, stress-test, logged, debug
#   swarm-client: default, stress, gentle, burst, extreme
#
# Logging profiles (for performance analysis):
#   nix run .#test-origin-logged           # With segment logging (512k buffer)
#   nix run .#test-origin-debug            # Full logging with compression
#   nix run .#test-origin-vm-logged        # MicroVM with logging enabled
#
{
  description = "HLS load testing tool using FFmpeg process swarm";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";

    # MicroVM support for lightweight VM testing
    microvm = {
      url = "github:astro/microvm.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  # Enable microvm binary cache for faster builds
  nixConfig = {
    extra-substituters = [ "https://microvm.cachix.org" ];
    extra-trusted-public-keys = [
      "microvm.cachix.org-1:oXnBc6hRE3eX5rSYdRyMYXnfzcCxC7yKPTbZXALsqys="
    ];
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      microvm,
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

        # Test origin server components (with profile support and MicroVM)
        testOrigin = import ./nix/test-origin { inherit pkgs lib microvm; };
        testOriginLowLatency = import ./nix/test-origin { inherit pkgs lib microvm; profile = "low-latency"; };
        testOrigin4kAbr = import ./nix/test-origin { inherit pkgs lib microvm; profile = "4k-abr"; };
        testOriginStress = import ./nix/test-origin { inherit pkgs lib microvm; profile = "stress-test"; };

        # Logging-enabled profiles for performance analysis
        testOriginLogged = import ./nix/test-origin { inherit pkgs lib microvm; profile = "logged"; };
        testOriginDebug = import ./nix/test-origin { inherit pkgs lib microvm; profile = "debug"; };

        # TAP networking profiles (high performance, requires make network-setup)
        testOriginTap = import ./nix/test-origin { inherit pkgs lib microvm; profile = "tap"; };
        testOriginTapLogged = import ./nix/test-origin { inherit pkgs lib microvm; profile = "tap-logged"; };

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

          # Logging-enabled profiles for performance analysis
          test-origin-logged = testOriginLogged.runner;
          test-origin-debug = testOriginDebug.runner;

          # MicroVM packages (Linux only, requires KVM)
          test-origin-vm = testOrigin.microvm.vm or (throw "MicroVM not available - requires microvm input");
          test-origin-vm-low-latency = testOriginLowLatency.microvm.vm or null;
          test-origin-vm-stress = testOriginStress.microvm.vm or null;
          test-origin-vm-logged = testOriginLogged.microvm.vm or null;
          test-origin-vm-debug = testOriginDebug.microvm.vm or null;

          # TAP networking MicroVMs (high performance, requires make network-setup)
          test-origin-vm-tap = testOriginTap.microvm.vm or null;
          test-origin-vm-tap-logged = testOriginTapLogged.microvm.vm or null;

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

          # Logging-enabled profiles for performance analysis
          test-origin-logged = {
            type = "app";
            program = "${testOriginLogged.runner}/bin/test-hls-origin";
          };
          test-origin-debug = {
            type = "app";
            program = "${testOriginDebug.runner}/bin/test-hls-origin";
          };

          # MicroVM apps (Linux only, requires KVM)
          test-origin-vm = {
            type = "app";
            program = "${testOrigin.microvm.runScript}";
          };
          test-origin-vm-logged = {
            type = "app";
            program = "${testOriginLogged.microvm.runScript}";
          };
          test-origin-vm-debug = {
            type = "app";
            program = "${testOriginDebug.microvm.runScript}";
          };

          # TAP networking MicroVM apps (high performance)
          test-origin-vm-tap = {
            type = "app";
            program = "${testOriginTap.microvm.runScript}";
          };
          test-origin-vm-tap-logged = {
            type = "app";
            program = "${testOriginTapLogged.microvm.runScript}";
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
