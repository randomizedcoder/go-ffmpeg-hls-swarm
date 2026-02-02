# MicroVM for test HLS origin
# See: docs/TEST_ORIGIN.md for detailed documentation
#
# Lightweight VM with full isolation, ~10s startup
# Uses the microvm.nix flake: https://github.com/astro/microvm.nix
#
# Networking modes:
#   - "user" (default): QEMU user-mode NAT, ~500 Mbps, zero config
#   - "tap": TAP + vhost-net, ~10 Gbps, requires `make network-setup`
#
# Usage (from flake):
#   nix run .#test-origin-vm           # User-mode networking (default)
#   nix run .#test-origin-vm-tap       # TAP networking (high performance)
#   make microvm-origin                # Via Makefile
#
# Logging:
#   When logging is enabled, logs are stored in /var/log/nginx/
#   Use a persistent volume to preserve logs across VM restarts
#
{ pkgs, lib, config, nixosModule, microvm, nixpkgs }:

let
  system = pkgs.stdenv.hostPlatform.system;
  log = config.logging;
  net = config.networking;

  # Is TAP networking enabled?
  useTap = net.mode == "tap";

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
        # ═══════════════════════════════════════════════════════════════════════
        # Network configuration
        # - User-mode: DHCP from QEMU's built-in server
        # - TAP mode: Static IP for predictable port forwarding
        # ═══════════════════════════════════════════════════════════════════════
        networking = {
          hostName = "hls-origin-vm";
          useDHCP = lib.mkDefault (!useTap);
        };

        # For TAP mode, use systemd-networkd with MAC address matching
        # (The interface inside the VM is NOT "eth0" - it's "enp*" due to predictable naming)
        systemd.network = lib.mkIf useTap {
          enable = true;
          networks."10-vm" = {
            # Match by MAC address (reliable across interface naming schemes)
            matchConfig.MACAddress = net.tap.mac;
            networkConfig = {
              DHCP = "no";
              Address = "${net.staticIp}/24";
              Gateway = net.gateway;
              DNS = [ "1.1.1.1" "8.8.8.8" ];
            };
          };
        };

        # Allow root login for debugging
        users.users.root.password = "";
        services.getty.autologinUser = "root";

        # System version
        system.stateVersion = "24.11";

        # MicroVM configuration
        microvm = {
          # Use qemu for broadest compatibility
          # Note: For TAP, could use cloud-hypervisor for even better perf
          hypervisor = "qemu";

          # Memory allocation (4GB for high-load testing)
          # Increase if testing with 500+ clients
          mem = 4096;
          vcpu = 4;

          # Share host's /nix/store (faster startup, no squashfs build)
          shares = [{
            tag = "ro-store";
            source = "/nix/store";
            mountPoint = "/nix/.ro-store";
            proto = "9p";  # qemu has 9p built-in
          }];

          # ═══════════════════════════════════════════════════════════════════
          # Network interface configuration
          # ═══════════════════════════════════════════════════════════════════
          interfaces = if useTap then [
            # TAP networking with multiqueue + vhost-net (high performance)
            # Requires: make network-setup (creates hlsbr0 bridge + hlstap0 TAP with multi_queue)
            {
              type = "tap";
              id = net.tap.device;
              mac = net.tap.mac;
              tap.vhost = true;  # Enable vhost-net for ~10 Gbps throughput
            }
          ] else [
            # User-mode networking (default, zero config)
            {
              type = "user";
              id = "eth0";
              mac = "02:00:00:01:01:01";
            }
          ];

          # Port forwarding (only for user-mode networking)
          # TAP mode uses nftables port forwarding on host instead
          forwardPorts = lib.mkIf (!useTap) [
            # HLS origin server
            {
              from = "host";
              host.port = config.server.port;
              guest.port = config.server.port;
            }
            # SSH (see docs/PORTS.md)
            {
              from = "host";
              host.port = 17122;  # Host-side port (17xxx + 122 suggests SSH)
              guest.port = 22;    # Standard SSH port
            }
            # Prometheus nginx exporter (see docs/PORTS.md)
            {
              from = "host";
              host.port = 17113;  # Host-side port
              guest.port = 9113;  # Internal exporter port
            }
            # Prometheus node exporter (see docs/PORTS.md)
            {
              from = "host";
              host.port = 17100;  # Host-side port (17xxx + 100 suggests node-exporter)
              guest.port = 9100;  # Internal exporter port
            }
          ];

          # Control socket for microvm command
          socket = "control.socket";

          # ═══════════════════════════════════════════════════════════════════
          # QEMU console configuration
          # ═══════════════════════════════════════════════════════════════════
          # Disable default stdio serial so we can use TCP for ttyS0
          qemu.serialConsole = false;

          # Add TCP serial as ttyS0 (matches kernel console=ttyS0)
          qemu.extraArgs = [
            "-serial" "tcp:0.0.0.0:17022,server=on,wait=off"
          ];

          # Persistent volume for nginx logs (only if logging enabled)
          # This preserves logs across VM restarts for post-test analysis
          volumes = lib.mkIf log.enabled [{
            image = "nginx-logs.img";
            mountPoint = log.directory;
            size = 256;  # 256MB for logs
          }];
        };

        # Kernel console for TCP serial (since we disabled qemu.serialConsole)
        boot.kernelParams = [ "console=ttyS0" "earlyprintk=ttyS0" ];

        # Additional kernel tuning for the VM
        boot.kernel.sysctl = {
          # VM-specific: reduce memory pressure
          "vm.swappiness" = 10;
          "vm.dirty_ratio" = 40;
          "vm.dirty_background_ratio" = 10;
        };

        # Optimizations for minimal footprint
        documentation.enable = false;

        # Debug tools for performance analysis
        environment.systemPackages = with pkgs; [
          btop       # Interactive process viewer
          htop       # Alternative process viewer
          below      # Facebook's system monitoring tool (cgroup-aware)
          iotop      # I/O monitoring
          iftop      # Network bandwidth monitoring
          tcpdump    # Packet capture
          strace     # System call tracing
          lsof       # List open files
          perf-tools # Linux perf tools wrapper
          ethtool    # Network interface diagnostics
          iproute2   # ip/ss commands for network debugging
        ];

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
    echo "║                    Network: ${if useTap then "TAP + vhost-net (high perf)" else "User-mode NAT (default)"}             ║"
    echo "╠════════════════════════════════════════════════════════════════════════╣"
    ${if useTap then ''
    echo "║ Stream:   http://${net.staticIp}:${toString config.server.port}/${config.hls.playlistName}                      ║"
    echo "║ Health:   http://${net.staticIp}:${toString config.server.port}/health                             ║"
    echo "║ Files:    http://${net.staticIp}:${toString config.server.port}/files/                             ║"
    echo "╠════════════════════════════════════════════════════════════════════════╣"
    echo "║ Nginx:    http://${net.staticIp}:9113/metrics                           ║"
    echo "║ Node:     http://${net.staticIp}:9100/metrics                           ║"
    echo "║ SSH:      ssh root@${net.staticIp}                                      ║"
    echo "║ SSH:      ssh -o StrictHostKeyChecking=no root@${net.staticIp}                                      ║"
    '' else ''
    echo "║ Stream:   http://localhost:${toString config.server.port}/${config.hls.playlistName}                               ║"
    echo "║ Health:   http://localhost:${toString config.server.port}/health                                      ║"
    echo "║ Files:    http://localhost:${toString config.server.port}/files/                                      ║"
    echo "║ Status:   http://localhost:${toString config.server.port}/nginx_status                                ║"
    echo "╠════════════════════════════════════════════════════════════════════════╣"
    echo "║ Nginx:    http://localhost:17113/metrics  (nginx exporter)             ║"
    echo "║ Node:     http://localhost:17100/metrics  (node exporter)              ║"
    echo "║ SSH:      ssh -p 17122 root@localhost                                  ║"
    ''}
    echo "║ Console:  nc localhost 17022 (or socat - TCP:localhost:17022)        ║"
    ${lib.optionalString log.enabled ''
    echo "╠════════════════════════════════════════════════════════════════════════╣"
    echo "║ Logging:  ENABLED (${log.buffer} buffer, ${log.flushInterval} flush)                       ║"
    echo "║ Logs:     ${log.directory}/                                            ║"
    ''}
    echo "╚════════════════════════════════════════════════════════════════════════╝"
    echo ""
    ${if useTap then ''
    echo "🚀 TAP networking enabled - ~10 Gbps throughput"
    echo "   Ensure 'make network-setup' was run first!"
    echo ""
    '' else ''
    echo "📊 Prometheus metrics:"
    echo "   curl -s http://localhost:17113/metrics | grep nginx_"
    echo "   curl -s http://localhost:17100/metrics | grep node_"
    echo ""
    echo "🔑 SSH access (root, empty password):"
    echo "   ssh -o StrictHostKeyChecking=no -p 17122 root@localhost"
    echo ""
    ''}
    ${lib.optionalString log.enabled ''
    echo "📝 After testing, view logs in VM console:"
    echo "   tail -100 ${log.directory}/${log.files.segments}"
    echo "   awk '{sum+=\$3; count++} END {print \"Avg latency:\", sum/count \"s\"}' ${log.directory}/${log.files.segments}"
    echo ""
    ''}
    echo "Starting MicroVM (press Ctrl+A X to exit QEMU)..."
    echo ""
    exec ${nixos.config.microvm.declaredRunner}/bin/microvm-run
  '';
}
