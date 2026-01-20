# MicroVM for test HLS origin
# See: docs/TEST_ORIGIN.md for detailed documentation
#
# Lightweight VM with full isolation, ~10s startup
# Uses the microvm.nix flake: https://github.com/astro/microvm.nix
#
# Usage (from flake):
#   nix run .#test-origin-vm
#   make microvm-origin
#
{ pkgs, lib, config, nixosModule, microvm, nixpkgs }:

let
  system = pkgs.stdenv.hostPlatform.system;

  # Build the NixOS system for the MicroVM
  # Use nixpkgs.lib.nixosSystem (not pkgs.lib which doesn't have it)
  nixos = nixpkgs.lib.nixosSystem {
    inherit system;
    modules = [
      # MicroVM module from the flake
      microvm.nixosModules.microvm

      # Our test-origin NixOS module (FFmpeg + Nginx services)
      nixosModule

      # MicroVM-specific configuration
      ({ lib, ... }: {
        networking.hostName = "hls-origin-vm";

        # Allow root login for debugging
        users.users.root.password = "";
        services.getty.autologinUser = "root";

        # System version
        system.stateVersion = "24.11";

        # MicroVM configuration
        microvm = {
          # Use qemu for broadest compatibility
          hypervisor = "qemu";

          # Memory allocation (1GB for tmpfs + services)
          mem = 1024;
          vcpu = 2;

          # Share host's /nix/store (faster startup, no squashfs build)
          shares = [{
            tag = "ro-store";
            source = "/nix/store";
            mountPoint = "/nix/.ro-store";
            proto = "9p";  # qemu has 9p built-in
          }];

          # User networking with port forwarding (no host setup required)
          interfaces = [{
            type = "user";
            id = "eth0";
            mac = "02:00:00:01:01:01";
          }];

          # Forward port 8080 -> VM's 8080
          forwardPorts = [{
            from = "host";
            host.port = config.server.port;
            guest.port = config.server.port;
          }];

          # Control socket for microvm command
          socket = "control.socket";
        };

        # Additional kernel tuning for the VM
        boot.kernel.sysctl = {
          # VM-specific: reduce memory pressure
          "vm.swappiness" = 10;
          "vm.dirty_ratio" = 40;
          "vm.dirty_background_ratio" = 10;
        };

        # Optimizations for minimal footprint
        documentation.enable = false;

        # NSS configuration - disable nscd but keep NSS working
        # (nscd is not needed in a minimal VM)
        system.nssModules = lib.mkForce [];
        services.nscd.enable = false;
      })
    ];
  };

in {
  # The MicroVM runner (executable) - this is the main output
  # Use: nix run .#test-origin-vm
  runner = nixos.config.microvm.declaredRunner;

  # Alternative: hypervisor-specific runner
  # runner = nixos.config.microvm.runner.qemu;

  # Full NixOS configuration (for inspection)
  inherit nixos;

  # The VM configuration
  vm = nixos.config.microvm.declaredRunner;

  # Helper script with nice output
  runScript = pkgs.writeShellScript "run-hls-origin-vm" ''
    echo "╔════════════════════════════════════════════════════════════════════════╗"
    echo "║                    HLS Origin MicroVM                                  ║"
    echo "║                    Profile: ${config._profile.name}                                         ║"
    echo "╠════════════════════════════════════════════════════════════════════════╣"
    echo "║ Stream:   http://localhost:${toString config.server.port}/${config.hls.playlistName}                               ║"
    echo "║ Health:   http://localhost:${toString config.server.port}/health                                      ║"
    echo "╚════════════════════════════════════════════════════════════════════════╝"
    echo ""
    echo "Starting MicroVM (press Ctrl+A X to exit QEMU)..."
    echo ""
    exec ${nixos.config.microvm.declaredRunner}/bin/microvm-run
  '';
}
