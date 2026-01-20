# MicroVM for test HLS origin
# See: docs/TEST_ORIGIN.md for detailed documentation
#
# Lightweight VM with full isolation, ~10s startup
#
{ pkgs, lib, config, nixosModule, microvm }:

let
  # MicroVM NixOS configuration
  microvmConfig = {
    imports = [
      microvm.nixosModules.microvm
      nixosModule
    ];

    networking.hostName = "hls-origin-vm";

    # Allow root login for debugging
    users.users.root.password = "";
    services.getty.autologinUser = "root";

    # MicroVM-specific configuration
    microvm = {
      # Use qemu for broadest compatibility
      hypervisor = "qemu";

      # Memory allocation based on tmpfs needs
      mem = 1024;  # 1GB RAM (generous for tmpfs)
      vcpu = 2;

      # Share host's /nix/store
      shares = [{
        tag = "ro-store";
        source = "/nix/store";
        mountPoint = "/nix/.ro-store";
        proto = "virtiofs";
      }];

      # Forward port to host
      forwardPorts = [{
        from = "host";
        host.port = config.server.port;
        guest.port = config.server.port;
      }];

      # Network interface
      interfaces = [{
        type = "user";
        id = "eth0";
      }];

      volumes = [];
    };

    system.stateVersion = "24.05";
  };

in {
  # Build the MicroVM
  vm = microvm.lib.buildMicrovm {
    inherit pkgs;
    config = microvmConfig;
  };

  inherit microvmConfig;

  # Helper script to run the VM
  runScript = pkgs.writeShellScript "run-hls-origin-vm" ''
    echo "Starting HLS Origin MicroVM (${config._profile.name} profile)..."
    echo "Stream: http://localhost:${toString config.server.port}/${config.hls.playlistName}"
    exec ${microvm.lib.buildMicrovm { inherit pkgs; config = microvmConfig; }}/bin/microvm-run
  '';
}
