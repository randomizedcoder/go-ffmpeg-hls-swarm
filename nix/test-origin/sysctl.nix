# High-performance network tuning for HLS streaming
# Reference: https://www.kernel.org/doc/html/latest/networking/ip-sysctl.html
#
# This module applies kernel sysctl settings optimized for:
# - High concurrent connections (10k+)
# - Large file transfers (HLS segments)
# - Low latency manifest delivery
# - Fast dead connection detection
#
{ config, pkgs, lib, ... }:

{
  boot.kernel.sysctl = {
    # ═══════════════════════════════════════════════════════════════════════
    # Connection Limits
    # ═══════════════════════════════════════════════════════════════════════
    "net.core.somaxconn" = 65535;                  # Max socket listen backlog
    "net.ipv4.tcp_max_syn_backlog" = 65535;        # SYN queue size
    "net.core.netdev_max_backlog" = 65535;         # Network device backlog
    "fs.file-max" = 2097152;                       # Max open files system-wide

    # ═══════════════════════════════════════════════════════════════════════
    # TCP Keepalive - Detect dead connections faster
    # Default: 7200s wait, 75s interval, 9 probes = 11.25 minutes
    # Tuned:  120s wait, 30s interval, 4 probes = 2 minutes
    # ═══════════════════════════════════════════════════════════════════════
    "net.ipv4.tcp_keepalive_time" = 120;           # First probe after 120s
    "net.ipv4.tcp_keepalive_intvl" = 30;           # Probe interval: 30s
    "net.ipv4.tcp_keepalive_probes" = 4;           # 4 probes before drop

    # ═══════════════════════════════════════════════════════════════════════
    # TCP Buffer Sizes - Large buffers for high throughput
    # Format: "min default max" in bytes
    # Default: 4096 131072 6291456 (6MB max)
    # Tuned:   4096 1000000 16000000 (16MB max)
    #
    # For HLS: large segments (500KB-2MB) benefit from big buffers
    # ═══════════════════════════════════════════════════════════════════════
    "net.ipv4.tcp_rmem" = "4096 1000000 16000000"; # Read buffer
    "net.ipv4.tcp_wmem" = "4096 1000000 16000000"; # Write buffer
    "net.ipv6.tcp_rmem" = "4096 1000000 16000000"; # IPv6 read buffer
    "net.ipv6.tcp_wmem" = "4096 1000000 16000000"; # IPv6 write buffer

    # Core network buffers (~25MB default/max)
    "net.core.rmem_default" = 26214400;
    "net.core.rmem_max" = 26214400;
    "net.core.wmem_default" = 26214400;
    "net.core.wmem_max" = 26214400;

    # ═══════════════════════════════════════════════════════════════════════
    # TCP Optimizations for High Performance
    # ═══════════════════════════════════════════════════════════════════════

    # tcp_notsent_lowat: Trigger socket writability earlier
    # Reduces latency for small writes (like manifest files)
    # Reference: https://lwn.net/Articles/560082/
    "net.ipv4.tcp_notsent_lowat" = 131072;

    # TIME-WAIT socket reuse (safe with timestamps enabled)
    "net.ipv4.tcp_tw_reuse" = 1;

    # Enable TCP timestamps (required for tcp_tw_reuse, PAWS)
    "net.ipv4.tcp_timestamps" = 1;

    # Explicit Congestion Notification
    "net.ipv4.tcp_ecn" = 1;

    # Window scaling (RFC 1323) - required for large windows
    "net.ipv4.tcp_window_scaling" = 1;

    # Selective ACK - faster recovery from packet loss
    "net.ipv4.tcp_sack" = 1;

    # Forward ACK - works with SACK for better recovery
    "net.ipv4.tcp_fack" = 1;

    # FIN-WAIT-2 timeout (default: 60s)
    "net.ipv4.tcp_fin_timeout" = 30;

    # ═══════════════════════════════════════════════════════════════════════
    # TCP Fast Start / Low Latency
    # ═══════════════════════════════════════════════════════════════════════

    # Don't reset congestion window after idle
    # Important for keepalive connections fetching segments periodically
    "net.ipv4.tcp_slow_start_after_idle" = 0;

    # TCP Fast Open (TFO) - 0-RTT for repeat connections
    # 3 = enabled for both client and server
    "net.ipv4.tcp_fastopen" = 3;

    # Save slow-start threshold in route cache
    # Helps subsequent connections start faster
    "net.ipv4.tcp_no_ssthresh_metrics_save" = 0;

    # Reflect TOS field in replies
    "net.ipv4.tcp_reflect_tos" = 1;

    # Minimum RTO: 50ms (default: 200ms)
    # Faster retransmits on low-latency networks
    "net.ipv4.tcp_rto_min_us" = 50000;

    # ═══════════════════════════════════════════════════════════════════════
    # Port Range and Queueing
    # ═══════════════════════════════════════════════════════════════════════

    # Expanded ephemeral port range
    # Default: 32768-60999 (~28k ports)
    # Tuned:  1026-65535 (~64k ports)
    "net.ipv4.ip_local_port_range" = "1026 65535";

    # Queue discipline: CAKE provides good fairness and latency
    # Alternative: fq_codel, fq
    "net.core.default_qdisc" = "cake";

    # Congestion control algorithm
    # cubic: good for stable networks (default)
    # bbr: better for high-latency/lossy networks
    "net.ipv4.tcp_congestion_control" = "cubic";
  };
}
