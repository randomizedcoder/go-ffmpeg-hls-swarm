# ISO image for test origin server
# Bootable image for Proxmox, VMware, VirtualBox, etc.
# Supports optional Cloud-Init for SSH keys and network configuration
{ pkgs, lib, config, nixosModule, nixpkgs, cloudInit ? null }:

let
  system = "x86_64-linux";  # TODO: Support aarch64-linux

  # Build NixOS ISO
  iso = nixpkgs.lib.nixosSystem {
    inherit system;
    modules = [
      # ISO image base configuration
      "${nixpkgs}/nixos/modules/installer/cd-dvd/iso-image.nix"

      # Our shared NixOS module
      nixosModule

      # ISO-specific configuration
      ({ lib, ... }: {
        # Enable HLS origin service (via nixosModule)
        # The nixosModule already has everything enabled

        # Boot configuration
        boot.loader.grub = {
          enable = true;
          device = "/dev/sda";
        };

        # Networking (DHCP by default, can be configured)
        networking = {
          hostName = "hls-origin";
          useDHCP = true;
          firewall.enable = false;  # Allow all traffic for testing
        };

        # Allow root login for initial setup
        users.users.root.password = "";
        services.getty.autologinUser = "root";

        # SSH for remote access
        services.openssh = {
          enable = true;
          settings = {
            PermitRootLogin = "yes";
            PasswordAuthentication = true;
          };
        };

        # System version
        system.stateVersion = "24.11";
      } // lib.optionalAttrs (cloudInit != null && cloudInit.enable) {
        # Cloud-Init configuration (optional)
        systemd.services.cloud-init = {
          enable = true;
          wantedBy = [ "multi-user.target" ];
        };

        # Cloud-Init user data
        cloud-init = {
          enable = true;
          userData = cloudInit.userData or "";
        };

        # Allow Cloud-Init to configure networking
        networking.useNetworkd = true;
      })
    ];
  };

in
iso.config.system.build.isoImage
