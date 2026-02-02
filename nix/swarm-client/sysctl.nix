# High-performance network tuning for HLS client
# See: docs/CLIENT_DEPLOYMENT.md for detailed documentation
#
# Mirrors test-origin sysctl.nix with client-specific optimizations:
# - Emphasis on outbound connections (ephemeral ports, TIME_WAIT reuse)
# - Large receive buffers for segment downloads
# - Fast connection recycling for high churn scenarios
#
{ config, pkgs, lib, ... }:

{
  boot.kernel.sysctl = {
    # ═══════════════════════════════════════════════════════════════════════
    # Connection Limits (Critical for many outbound connections)
    # ═══════════════════════════════════════════════════════════════════════

    # Maximum open files - each FFmpeg needs ~10 FDs
    "fs.file-max" = 2097152;

    # Network device backlog
    "net.core.netdev_max_backlog" = 65535;

    # SYN backlog (less critical for client, but good to have)
    "net.ipv4.tcp_max_syn_backlog" = 65535;

    # ═══════════════════════════════════════════════════════════════════════
    # Ephemeral Port Range (CRITICAL for load testing clients)
    # Default: 32768-60999 (~28k ports)
    # Tuned:   1026-65535 (~64k ports)
    #
    # With 200 clients × 4 connections each = 800 ports minimum
    # Need headroom for TIME_WAIT states (can persist 60-120s)
    # ═══════════════════════════════════════════════════════════════════════
    "net.ipv4.ip_local_port_range" = "1026 65535";

    # ═══════════════════════════════════════════════════════════════════════
    # TIME_WAIT Management (CRITICAL for high connection churn)
    #
    # HLS clients open/close connections frequently:
    # - New TCP connection per segment (without HTTP/2 multiplexing)
    # - Playlist refresh every 2-6 seconds
    #
    # TIME_WAIT sockets can exhaust ports if not managed
    # ═══════════════════════════════════════════════════════════════════════

    # Reuse TIME_WAIT sockets for new outbound connections
    # Safe with tcp_timestamps enabled
    "net.ipv4.tcp_tw_reuse" = 1;

    # Faster FIN_WAIT_2 timeout (default: 60s)
    # Frees ports faster when origin closes connection
    # Client-side: reduce to 15s for faster recycling
    "net.ipv4.tcp_fin_timeout" = 15;

    # ═══════════════════════════════════════════════════════════════════════
    # TCP Keepalive - Detect dead connections faster
    # Default: 7200s wait, 75s interval, 9 probes = 11.25 minutes
    # Tuned:  120s wait, 30s interval, 4 probes = 2 minutes
    # ═══════════════════════════════════════════════════════════════════════
    "net.ipv4.tcp_keepalive_time" = 120;
    "net.ipv4.tcp_keepalive_intvl" = 30;
    "net.ipv4.tcp_keepalive_probes" = 4;

    # ═══════════════════════════════════════════════════════════════════════
    # TCP Buffer Sizes - Large buffers for segment downloads
    # Format: "min default max" in bytes
    # Default: 4096 131072 6291456 (6MB max)
    # Tuned:   4096 1000000 16000000 (16MB max)
    #
    # For HLS: segments are 500KB-2MB, benefit from large buffers
    # ═══════════════════════════════════════════════════════════════════════
    "net.ipv4.tcp_rmem" = "4096 1000000 16000000";
    "net.ipv4.tcp_wmem" = "4096 1000000 16000000";
    "net.ipv6.tcp_rmem" = "4096 1000000 16000000";
    "net.ipv6.tcp_wmem" = "4096 1000000 16000000";

    # Core network buffers (~25MB default/max)
    "net.core.rmem_default" = 26214400;
    "net.core.rmem_max" = 26214400;
    "net.core.wmem_default" = 26214400;
    "net.core.wmem_max" = 26214400;

    # ═══════════════════════════════════════════════════════════════════════
    # TCP Performance Optimizations
    # ═══════════════════════════════════════════════════════════════════════

    # Don't reset congestion window after idle
    # CRITICAL: FFmpeg fetches segments every 2-6 seconds with gaps between
    # Without this, each fetch restarts slow-start
    "net.ipv4.tcp_slow_start_after_idle" = 0;

    # TCP Fast Open - client mode (1)
    # Reduces latency for playlist fetches to known servers
    # 1 = client only, 3 = client and server
    "net.ipv4.tcp_fastopen" = 1;

    # Enable timestamps (required for tcp_tw_reuse, PAWS protection)
    "net.ipv4.tcp_timestamps" = 1;

    # Selective ACK for faster recovery from packet loss
    "net.ipv4.tcp_sack" = 1;
    "net.ipv4.tcp_fack" = 1;

    # Window scaling (RFC 1323) - required for large windows
    "net.ipv4.tcp_window_scaling" = 1;

    # Explicit Congestion Notification
    "net.ipv4.tcp_ecn" = 1;

    # ═══════════════════════════════════════════════════════════════════════
    # Low Latency Settings
    # ═══════════════════════════════════════════════════════════════════════

    # Trigger socket writability earlier
    # Reduces latency for small writes
    "net.ipv4.tcp_notsent_lowat" = 131072;

    # Minimum RTO: 50ms (default: 200ms)
    # Faster retransmits on low-latency networks
    "net.ipv4.tcp_rto_min_us" = 50000;

    # Save slow-start threshold in route cache
    # Helps subsequent connections start faster
    "net.ipv4.tcp_no_ssthresh_metrics_save" = 0;

    # Reflect TOS in replies
    "net.ipv4.tcp_reflect_tos" = 1;

    # ═══════════════════════════════════════════════════════════════════════
    # Queue Discipline
    # ═══════════════════════════════════════════════════════════════════════

    # CAKE provides good fairness and latency under load
    "net.core.default_qdisc" = "cake";

    # Congestion control: cubic is reliable default
    # bbr is better for high-latency/lossy networks
    "net.ipv4.tcp_congestion_control" = "cubic";
  };

  # ═══════════════════════════════════════════════════════════════════════════
  # Process and File Limits
  # ═══════════════════════════════════════════════════════════════════════════
  security.pam.loginLimits = [
    { domain = "*"; type = "soft"; item = "nofile"; value = "1048576"; }
    { domain = "*"; type = "hard"; item = "nofile"; value = "1048576"; }
    { domain = "*"; type = "soft"; item = "nproc"; value = "unlimited"; }
    { domain = "*"; type = "hard"; item = "nproc"; value = "unlimited"; }
  ];
}
