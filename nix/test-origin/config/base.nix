# Base configuration for test origin (defaults for all profiles)
{
  # Mode selection
  multibitrate = false;

  # HLS settings
  hls = {
    segmentDuration = 2;
    listSize = 10;
    deleteThreshold = 5;  # INCREASED: Safe buffer for SWR/CDN lag
    # Note: Use single % here. Systemd escaping (if needed) should be handled
    # in systemd-specific code, not in the base config.
    segmentPattern = "seg%05d.ts";
    playlistName = "stream.m3u8";
    masterPlaylist = "master.m3u8";

    # FFmpeg HLS flags
    # - delete_segments: Remove old .ts files automatically
    # - omit_endlist: Live stream (no #EXT-X-ENDLIST)
    # - temp_file: Atomic writes (write to .tmp then rename)
    # Note: strftime and second_level_segment_index removed - not supported in FFmpeg 8.0
    flags = [
      "delete_segments"
      "omit_endlist"
      "temp_file"
    ];
  };

  # Server settings
  # See docs/PORTS.md for port documentation
  server = {
    port = 17080;
    hlsDir = "/var/hls";
  };

  # Audio settings
  audio = {
    frequency = 1000;
    sampleRate = 48000;
  };

  # Use testsrc2 for more complex video that's harder to compress
  testPattern = "testsrc2";

  # Video settings (single bitrate)
  # 1080p60 generates more data and is harder to compress
  video = {
    width = 1920;
    height = 1080;
    bitrate = "5000k";
    minrate = "5000k";   # Force minimum bitrate (prevents underrun on simple content)
    maxrate = "5500k";
    bufsize = "10000k";
    audioBitrate = "128k";
  };

  # Default variants (ABR)
  variants = [
    {
      name = "720p";
      width = 1280;
      height = 720;
      bitrate = "2000k";
      maxrate = "2200k";
      bufsize = "4000k";
      audioBitrate = "128k";
    }
    {
      name = "360p";
      width = 640;
      height = 360;
      bitrate = "500k";
      maxrate = "550k";
      bufsize = "1000k";
      audioBitrate = "64k";
    }
  ];

  # Encoder settings
  # 30fps is much more manageable for CPU - 60fps requires ~2-3 cores
  encoder = {
    framerate = 30;       # Changed from 60 - halves encoding load
    preset = "veryfast";  # Better quality than ultrafast, still fast
    tune = "film";        # Changed from zerolatency - allows proper rate control
    profile = "high";     # Better compression efficiency
    level = "4.1";        # 4.1 is sufficient for 1080p30 (4.2 was for 1080p60)
  };

  # ═══════════════════════════════════════════════════════════════════════════
  # MicroVM networking configuration
  # See: docs/MICROVM_NETWORKING.md
  # ═══════════════════════════════════════════════════════════════════════════
  networking = {
    # Networking mode: "user" (default, zero config) or "tap" (high performance)
    # "user" - QEMU user-mode NAT (~500 Mbps, no host setup)
    # "tap"  - TAP + vhost-net (~10 Gbps, requires make network-setup)
    mode = "user";

    # TAP device configuration (only used when mode = "tap")
    tap = {
      device = "hlstap0";         # TAP device name (created by make network-setup with multi_queue)
      mac = "02:00:00:01:77:01";  # VM MAC address (unique per VM)
    };

    # Static IP for TAP mode (VM needs fixed IP for port forwarding)
    staticIp = "10.177.0.10";
    gateway = "10.177.0.1";
    subnet = "10.177.0.0/24";
  };

  # ═══════════════════════════════════════════════════════════════════════════
  # Logging configuration for performance analysis
  # ═══════════════════════════════════════════════════════════════════════════
  logging = {
    # Enable/disable logging (disabled by default for max performance)
    enabled = false;

    # Log directory
    directory = "/var/log/nginx";

    # Buffer size for reduced I/O (512k recommended for high-load tests)
    buffer = "512k";

    # Flush interval (10s recommended for buffered logging)
    flushInterval = "10s";

    # Gzip compression level (0 = disabled, 1-9 = compression level)
    gzip = 0;

    # Log only segment requests (reduces volume significantly)
    segmentsOnly = false;

    # Log file names
    files = {
      segments = "segments.log";
      manifests = "manifests.log";
      all = "access.log";
    };
  };
}
