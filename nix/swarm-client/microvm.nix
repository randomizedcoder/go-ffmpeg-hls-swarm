# MicroVM configuration for go-ffmpeg-hls-swarm
# See: docs/CLIENT_DEPLOYMENT.md for detailed documentation
#
# Provides full kernel control for sysctl tuning and network namespacing.
# Ideal for integration testing and reproducing production conditions.
#
# Build: nix build .#swarm-client-microvm
# Run:   ./result/bin/microvm-run
#
{ pkgs, lib, config, swarmBinary, nixosModule }:

let
  cfg = config;
  d = cfg.derived;

in {
  # NixOS configuration for the MicroVM guest
  nixosConfiguration = { pkgs, lib, config, ... }: {
    imports = [
      nixosModule
      ./sysctl.nix
    ];

    # Basic system configuration
    system.stateVersion = "24.05";

    networking.hostName = "swarm-client";
    networking.useDHCP = true;

    # Install useful debugging tools
    environment.systemPackages = with pkgs; [
      swarmBinary
      ffmpeg-full
      curl
      htop
      iotop
      iproute2     # For tc qdisc (traffic shaping)
      procps       # For process monitoring
      netcat       # For network testing
    ];

    # Swarm client service (from nixosModule)
    # The service expects STREAM_URL to be set

    # Helper script to run the swarm client interactively
    environment.shellInit = ''
      alias swarm='go-ffmpeg-hls-swarm'

      # Quick start function
      run_swarm() {
        local url="''${1:-http://origin:8080/stream.m3u8}"
        echo "Starting swarm client against: $url"
        STREAM_URL="$url" systemctl start go-ffmpeg-hls-swarm
        echo "Swarm client started. View logs: journalctl -fu go-ffmpeg-hls-swarm"
      }

      # Status function
      swarm_status() {
        echo "=== Service Status ==="
        systemctl status go-ffmpeg-hls-swarm --no-pager || true
        echo ""
        echo "=== Recent Logs ==="
        journalctl -u go-ffmpeg-hls-swarm --no-pager -n 20 || true
        echo ""
        echo "=== Metrics ==="
        curl -sf http://localhost:${toString cfg.metricsPort}/metrics | head -50 || echo "Metrics not available"
      }
    '';

    # Welcome message
    services.getty.helpLine = ''

      ╔═══════════════════════════════════════════════════════════════╗
      ║           go-ffmpeg-hls-swarm MicroVM (${cfg._profile.name})              ║
      ╚═══════════════════════════════════════════════════════════════╝

      Quick start:
        run_swarm http://origin:8080/stream.m3u8
        swarm_status

      Manual control:
        STREAM_URL=http://... systemctl start go-ffmpeg-hls-swarm
        journalctl -fu go-ffmpeg-hls-swarm
        curl http://localhost:${toString cfg.metricsPort}/metrics

      Traffic shaping:
        tc qdisc add dev eth0 root netem delay 50ms
        tc qdisc del dev eth0 root
    '';
  };

  # MicroVM hypervisor configuration
  vmConfig = {
    microvm = {
      # Use QEMU for maximum compatibility
      hypervisor = "qemu";

      # Resource allocation based on client count
      mem = d.vmMemoryMB;
      vcpu = 2;

      # Networking with user-mode (NAT)
      interfaces = [{
        type = "user";
        id = "eth0";
        mac = "02:00:00:00:00:01";
      }];

      # Forward metrics port to host
      forwardPorts = [
        {
          from = "host";
          host.port = cfg.metricsPort;
          guest.port = cfg.metricsPort;
        }
      ];

      # Optional: shared directory for logs
      shares = [{
        tag = "logs";
        source = "/tmp/swarm-logs";
        mountPoint = "/var/log/swarm";
        proto = "9p";
      }];

      # Console access
      console = "tty";

      # Kernel parameters for performance
      kernelParams = [
        "console=ttyS0"
        "panic=1"
        "boot.panic_on_fail"
      ];
    };
  };

  # Convenience attributes
  profile = cfg._profile.name;
  clients = cfg.clients;
  metricsPort = cfg.metricsPort;
  estimatedMemory = d.estimatedMemoryMB;
}
