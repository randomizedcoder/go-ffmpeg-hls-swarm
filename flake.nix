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
      # Testing vhost-net fix: https://github.com/randomizedcoder/microvm.nix/tree/tap-performance
      url = "github:randomizedcoder/microvm.nix/tap-performance";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  # Enable microvm binary cache for faster builds
  # Note: Non-trusted users will see warnings about ignoring these settings.
  # This is harmless - it just means they won't use the cache. Trusted users
  # can add themselves to trusted-users in nix.conf to use the cache.
  # These warnings cannot be suppressed without removing the cache entirely,
  # which would hurt trusted users who can benefit from it.
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
                "control.socket"  # Socket file (unsupported by Nix)
              ];
            in
            !(builtins.elem baseName ignoredPaths);
        };

        # Import modular components
        #
        # Go Application Build:
        # - Uses buildGoModule which requires a vendor directory in the source
        # - The vendor/ directory must be committed to git and included in the source
        # - vendorHash = null allows buildGoModule to compute the hash automatically
        # - The vendor directory is created via: go mod vendor
        # - This ensures reproducible builds by locking all dependency versions
        # - Both Nix (via vendorHash) and Go (via go.sum) lock dependency versions
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
        # Import single source of truth for profiles
        testOriginProfileConfig = import ./nix/test-origin/config/profile-list.nix { inherit (pkgs) lib; };
        testOriginProfileNames = testOriginProfileConfig.profiles;

        # Generate all profile variants (derives from single list)
        testOriginProfiles = lib.genAttrs testOriginProfileNames (name:
          import ./nix/test-origin {
            inherit pkgs lib meta microvm nixpkgs;
            profile = testOriginProfileConfig.validateProfile name;
          }
        );

        # Swarm client components (with profile support)
        # Import single source of truth for profiles
        swarmClientProfileConfig = import ./nix/swarm-client/config/profile-list.nix { inherit (pkgs) lib; };
        swarmClientProfileNames = swarmClientProfileConfig.profiles;

        # Generate all profile variants (derives from single list)
        swarmClientProfiles = lib.genAttrs swarmClientProfileNames (name:
          import ./nix/swarm-client {
            inherit pkgs lib meta;
            swarmBinary = package;
            profile = swarmClientProfileConfig.validateProfile name;
          }
        );

      in
      {
        formatter = pkgs.nixfmt;

        packages = {
          ${meta.pname} = package;
          default = package;

          # OCI container for main binary (all platforms can build)
          go-ffmpeg-hls-swarm-container = import ./nix/container.nix {
            inherit pkgs lib;
            package = package;
          };

          # Test origin server packages (default profile)
          test-origin = testOriginProfiles.default.runner;
          test-origin-container = testOriginProfiles.default.container;

          # Profile-specific test origins
          test-origin-low-latency = testOriginProfiles.low-latency.runner;
          test-origin-4k-abr = testOriginProfiles."4k-abr".runner;
          test-origin-stress = testOriginProfiles.stress-test.runner;

          # Logging-enabled profiles for performance analysis
          test-origin-logged = testOriginProfiles.logged.runner;
          test-origin-debug = testOriginProfiles.debug.runner;

          # MicroVM packages (Linux only, requires KVM)
          test-origin-vm = testOriginProfiles.default.microvm.vm or (throw "MicroVM not available - requires microvm input");
          test-origin-vm-low-latency = testOriginProfiles.low-latency.microvm.vm or null;
          test-origin-vm-stress = testOriginProfiles.stress-test.microvm.vm or null;
          test-origin-vm-logged = testOriginProfiles.logged.microvm.vm or null;
          test-origin-vm-debug = testOriginProfiles.debug.microvm.vm or null;

          # TAP networking MicroVMs (high performance, requires make network-setup)
          test-origin-vm-tap = testOriginProfiles.tap.microvm.vm or null;
          test-origin-vm-tap-logged = testOriginProfiles.tap-logged.microvm.vm or null;

          # Swarm client packages (default profile)
          swarm-client = swarmClientProfiles.default.runner;
          swarm-client-container = swarmClientProfiles.default.container;

          # Profile-specific swarm clients
          swarm-client-stress = swarmClientProfiles.stress.runner;
          swarm-client-gentle = swarmClientProfiles.gentle.runner;
          swarm-client-burst = swarmClientProfiles.burst.runner;
          swarm-client-extreme = swarmClientProfiles.extreme.runner;

          # Nginx config generator packages (for viewing generated configs)
          test-origin-nginx-config = testOriginProfiles.default.nginxConfig;
          test-origin-nginx-config-low-latency = testOriginProfiles.low-latency.nginxConfig;
          test-origin-nginx-config-4k-abr = testOriginProfiles."4k-abr".nginxConfig;
          test-origin-nginx-config-stress = testOriginProfiles.stress-test.nginxConfig;
          test-origin-nginx-config-logged = testOriginProfiles.logged.nginxConfig;
          test-origin-nginx-config-debug = testOriginProfiles.debug.nginxConfig;
        } // lib.optionalAttrs pkgs.stdenv.isLinux {
          # Enhanced test origin container (requires systemd)
          test-origin-container-enhanced = testOriginProfiles.default.containerEnhanced or null;

          # ISO image (requires NixOS)
          test-origin-iso = testOriginProfiles.default.iso or null;
        };

        devShells = {
          inherit (shell) default;
        };

        apps = appsBase // {
          # Test origin server apps (different profiles)
          test-origin = {
            type = "app";
            program = "${testOriginProfiles.default.runner}/bin/test-hls-origin";
          };
          test-origin-low-latency = {
            type = "app";
            program = "${testOriginProfiles.low-latency.runner}/bin/test-hls-origin";
          };
          test-origin-4k-abr = {
            type = "app";
            program = "${testOriginProfiles."4k-abr".runner}/bin/test-hls-origin";
          };
          test-origin-stress = {
            type = "app";
            program = "${testOriginProfiles.stress-test.runner}/bin/test-hls-origin";
          };

          # Logging-enabled profiles for performance analysis
          test-origin-logged = {
            type = "app";
            program = "${testOriginProfiles.logged.runner}/bin/test-hls-origin";
          };
          test-origin-debug = {
            type = "app";
            program = "${testOriginProfiles.debug.runner}/bin/test-hls-origin";
          };

          # MicroVM apps (Linux only, requires KVM)
          test-origin-vm = {
            type = "app";
            program = "${testOriginProfiles.default.microvm.runScript}";
          };
          test-origin-vm-logged = {
            type = "app";
            program = "${testOriginProfiles.logged.microvm.runScript}";
          };
          test-origin-vm-debug = {
            type = "app";
            program = "${testOriginProfiles.debug.microvm.runScript}";
          };

          # TAP networking MicroVM apps (high performance)
          test-origin-vm-tap = {
            type = "app";
            program = "${testOriginProfiles.tap.microvm.runScript}";
          };
          test-origin-vm-tap-logged = {
            type = "app";
            program = "${testOriginProfiles.tap-logged.microvm.runScript}";
          };

          # Swarm client apps (different profiles)
          swarm-client = {
            type = "app";
            program = "${swarmClientProfiles.default.runner}/bin/swarm-client";
          };
          swarm-client-stress = {
            type = "app";
            program = "${swarmClientProfiles.stress.runner}/bin/swarm-client";
          };
          swarm-client-gentle = {
            type = "app";
            program = "${swarmClientProfiles.gentle.runner}/bin/swarm-client";
          };
          swarm-client-burst = {
            type = "app";
            program = "${swarmClientProfiles.burst.runner}/bin/swarm-client";
          };
          swarm-client-extreme = {
            type = "app";
            program = "${swarmClientProfiles.extreme.runner}/bin/swarm-client";
          };

          # Nginx config generator app
          nginx-config = {
            type = "app";
            program = "${pkgs.writeShellScript "nginx-config" ''
              set -euo pipefail
              
              PROFILE="''${1:-default}"
              
              # Map profile names to package names
              case "$PROFILE" in
                default)
                  PACKAGE="test-origin-nginx-config"
                  ;;
                low-latency)
                  PACKAGE="test-origin-nginx-config-low-latency"
                  ;;
                4k-abr)
                  PACKAGE="test-origin-nginx-config-4k-abr"
                  ;;
                stress)
                  PACKAGE="test-origin-nginx-config-stress"
                  ;;
                logged)
                  PACKAGE="test-origin-nginx-config-logged"
                  ;;
                debug)
                  PACKAGE="test-origin-nginx-config-debug"
                  ;;
                *)
                  echo "Error: Unknown profile '$PROFILE'" >&2
                  echo "Available profiles: default, low-latency, 4k-abr, stress, logged, debug" >&2
                  exit 1
                  ;;
              esac
              
              # Get current system
              SYSTEM=$(nix eval --impure --expr 'builtins.currentSystem' --raw 2>/dev/null)
              
              # Build and output the config
              CONFIG_PATH=$(nix build ".#packages.$SYSTEM.$PACKAGE" --print-out-paths 2>/dev/null)
              if [[ -n "$CONFIG_PATH" ]] && [[ -f "$CONFIG_PATH" ]]; then
                cat "$CONFIG_PATH"
              else
                echo "Error: Failed to build nginx config for profile '$PROFILE'" >&2
                echo "Package: $PACKAGE" >&2
                echo "System: $SYSTEM" >&2
                exit 1
              fi
            ''}";
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
