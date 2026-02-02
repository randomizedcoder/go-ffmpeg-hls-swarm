# Test origin profile definitions
# See: docs/TEST_ORIGIN.md for detailed documentation
{
  # Standard testing profile - balanced latency and safety
  default = {
    hls.segmentDuration = 2;
    hls.listSize = 10;
    video = {
      width = 1280;
      height = 720;
      bitrate = "2000k";
      maxrate = "2200k";
      bufsize = "4000k";
      audioBitrate = "128k";
    };
    encoder.framerate = 30;
    multibitrate = false;
  };

  # Low-latency profile - 1s segments for fast response
  low-latency = {
    hls.segmentDuration = 1;
    hls.listSize = 6;
    video = {
      width = 1280;
      height = 720;
      bitrate = "2000k";
      maxrate = "2500k";  # Higher maxrate for bursty encoding
      bufsize = "2000k";  # Smaller buffer for lower latency
      audioBitrate = "96k";
    };
    encoder = {
      framerate = 30;
      preset = "ultrafast";
      tune = "zerolatency";
    };
    multibitrate = false;
  };

  # 4K ABR profile - Multi-bitrate adaptive streaming
  "4k-abr" = {
    hls.segmentDuration = 2;
    hls.listSize = 10;
    multibitrate = true;
    variants = [
      {
        name = "2160p";
        width = 3840;
        height = 2160;
        bitrate = "15000k";
        maxrate = "16500k";
        bufsize = "30000k";
        audioBitrate = "192k";
      }
      {
        name = "1080p";
        width = 1920;
        height = 1080;
        bitrate = "5000k";
        maxrate = "5500k";
        bufsize = "10000k";
        audioBitrate = "128k";
      }
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
        name = "480p";
        width = 854;
        height = 480;
        bitrate = "1000k";
        maxrate = "1100k";
        bufsize = "2000k";
        audioBitrate = "96k";
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
    encoder.framerate = 30;
    # Use first variant for single bitrate settings
    video = {
      width = 1280;
      height = 720;
      bitrate = "2000k";
      maxrate = "2200k";
      bufsize = "4000k";
      audioBitrate = "128k";
    };
  };

  # High-load stress test profile - optimized for stability
  stress-test = {
    hls.segmentDuration = 2;
    hls.listSize = 15;  # Larger window for more stability
    video = {
      width = 1280;
      height = 720;
      bitrate = "1500k";  # Lower bitrate = faster encoding
      maxrate = "1650k";
      bufsize = "3000k";
      audioBitrate = "96k";
    };
    encoder = {
      framerate = 25;  # Lower framerate for less CPU
      preset = "ultrafast";
      tune = "zerolatency";
    };
    multibitrate = false;
  };

  # Logged profile - minimal logging for performance analysis
  # Logs segment requests only with 512k buffer
  logged = {
    logging = {
      enabled = true;
      buffer = "512k";
      flushInterval = "10s";
      gzip = 0;
      segmentsOnly = true;  # Only log .ts requests
    };
  };

  # Debug profile - full logging for debugging
  # Logs all requests with compression
  debug = {
    logging = {
      enabled = true;
      buffer = "256k";
      flushInterval = "5s";
      gzip = 4;
      segmentsOnly = false;  # Log all requests
    };
  };

  # TAP networking profile - high performance
  # Requires: make network-setup (creates hlsbr0 bridge + hlstap0 TAP)
  tap = {
    networking.mode = "tap";
  };

  # TAP + logging combo profile
  tap-logged = {
    networking.mode = "tap";
    logging = {
      enabled = true;
      buffer = "512k";
      flushInterval = "10s";
      gzip = 0;
      segmentsOnly = true;
    };
  };
}
