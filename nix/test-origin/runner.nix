# Combined runner script for local testing
# See: docs/TEST_ORIGIN.md for detailed documentation
#
# Features:
# - Dynamic caching based on segment duration
# - ffprobe included for stream verification
# - Segment continuity checking
#
{ pkgs, lib, config, ffmpeg, nginx }:

let
  h = config.hls;
  c = config.cache;
  enc = config.encoder;
  v = config.video;
  a = config.audio;
  d = config.derived;

  # HLS flags string
  hlsFlags = lib.concatStringsSep "+" h.flags;
  gopSize = enc.framerate * h.segmentDuration;
in
pkgs.writeShellApplication {
  name = "test-hls-origin";

  # Include ffprobe for stream verification
  runtimeInputs = ffmpeg.runtimeInputs ++ nginx.runtimeInputs ++ [
    pkgs.coreutils
    pkgs.curl
    pkgs.jq  # For JSON parsing
  ];

  text = ''
    set -euo pipefail

    # Allow override via environment
    HLS_DIR="''${HLS_DIR:-/tmp/hls-test}"
    PORT="''${PORT:-${toString config.server.port}}"
    VERIFY_STREAM="''${VERIFY_STREAM:-false}"

    echo "╔════════════════════════════════════════════════════════════════════════╗"
    echo "║                    Test HLS Origin Server                              ║"
    echo "║                    Profile: ${config._profile.name}                                         ║"
    echo "╠════════════════════════════════════════════════════════════════════════╣"
    echo "║ HLS Settings:                                                          ║"
    echo "║   Segment duration:  ${toString h.segmentDuration}s                                              ║"
    echo "║   Rolling window:    ${toString h.listSize} segments (${toString (h.listSize * h.segmentDuration)}s of content)                       ║"
    echo "║   Delete threshold:  ${toString h.deleteThreshold} segments (safe buffer for SWR)                    ║"
    echo "║   Segment lifetime:  ${toString d.segmentLifetimeSec}s                                             ║"
    echo "║   Flags:             ${hlsFlags}                    ║"
    echo "╠════════════════════════════════════════════════════════════════════════╣"
    echo "║ Cache Settings (dynamic, based on segment duration):                   ║"
    echo "║   Segments:   max-age=${toString c.segment.maxAge}s, immutable (generous TTL)               ║"
    echo "║   Manifests:  max-age=${toString c.manifest.maxAge}s, swr=${toString c.manifest.staleWhileRevalidate}s (TTL=seg/2, SWR=seg)                ║"
    echo "║   Master:     max-age=${toString c.master.maxAge}s, swr=${toString c.master.staleWhileRevalidate}s                                     ║"
    echo "╠════════════════════════════════════════════════════════════════════════╣"
    echo "║ Storage Estimates:                                                     ║"
    echo "║   Segment size:      ~${toString d.segmentSizeKB} KB                                          ║"
    echo "║   Files per variant: ${toString d.filesPerVariant}                                              ║"
    echo "║   Recommended tmpfs: ${toString d.recommendedTmpfsMB} MB                                          ║"
    echo "╚════════════════════════════════════════════════════════════════════════╝"
    echo ""
    echo "HLS directory: $HLS_DIR"
    echo "Port:          $PORT"
    echo ""

    mkdir -p "$HLS_DIR"

    # Track child PIDs for cleanup
    CHILD_PIDS=""
    cleanup() {
      echo "Shutting down..."
      # shellcheck disable=SC2086 # Word splitting is intentional
      [ -n "$CHILD_PIDS" ] && kill $CHILD_PIDS 2>/dev/null || true
      rm -rf "$HLS_DIR"
    }
    trap cleanup EXIT INT TERM

    # Start FFmpeg HLS generator
    # Note: Don't use duration=0 - it breaks HLS segment generation with -re
    echo "▶ Starting FFmpeg HLS generator..."
    ffmpeg -re \
      -f lavfi -i "${config.testPattern}=size=${toString v.width}x${toString v.height}:rate=${toString enc.framerate}" \
      -f lavfi -i "sine=frequency=${toString a.frequency}:sample_rate=${toString a.sampleRate}" \
      -c:v libx264 -preset ${enc.preset} -tune ${enc.tune} \
      -profile:v ${enc.profile} -level ${enc.level} \
      -g ${toString gopSize} \
      -keyint_min ${toString gopSize} \
      -sc_threshold 0 \
      -b:v ${v.bitrate} -maxrate ${v.maxrate} -bufsize ${v.bufsize} \
      -c:a aac -b:a ${v.audioBitrate} -ar ${toString a.sampleRate} \
      -f hls \
      -hls_time ${toString h.segmentDuration} \
      -hls_list_size ${toString h.listSize} \
      -hls_delete_threshold ${toString h.deleteThreshold} \
      -hls_flags ${hlsFlags} \
      -hls_segment_filename "$HLS_DIR/${h.segmentPattern}" \
      "$HLS_DIR/${h.playlistName}" 2>&1 | grep -v "^frame=" &
    CHILD_PIDS="$CHILD_PIDS $!"

    # Wait for HLS stream
    echo "⏳ Waiting for HLS stream..."
    attempt=0
    while [ $attempt -lt 30 ]; do
      if [ -f "$HLS_DIR/${h.playlistName}" ]; then
        echo "✓ HLS stream ready"
        break
      fi
      attempt=$((attempt + 1))
      sleep 1
    done

    if [ ! -f "$HLS_DIR/${h.playlistName}" ]; then
      echo "✗ ERROR: Failed to generate HLS stream"
      exit 1
    fi

    # Verify stream with ffprobe if requested
    if [ "$VERIFY_STREAM" = "true" ]; then
      echo ""
      echo "▶ Verifying stream with ffprobe..."
      ffprobe -v quiet -print_format json -show_format -show_streams "file://$HLS_DIR/${h.playlistName}" | jq -r '.format.format_name, .streams[0].codec_name, .streams[0].width, .streams[0].height' 2>/dev/null || true
    fi

    # Generate nginx config with optimized caching
    # Note: Using unquoted heredoc to allow shell variable expansion ($PORT, $HLS_DIR)
    NGINX_CONF=$(mktemp --suffix=.conf)
    cat > "$NGINX_CONF" << NGINX_EOF
    worker_processes 1;
    error_log /dev/stderr warn;
    pid /tmp/nginx-hls-$$.pid;
    events { worker_connections 4096; }
    http {
        include ${pkgs.nginx}/conf/mime.types;
        sendfile on;
        tcp_nopush on;
        access_log off;
        reset_timedout_connection on;

        # File descriptor caching
        open_file_cache max=1000 inactive=30s;
        open_file_cache_valid 10s;

        server {
            listen $PORT;
            root $HLS_DIR;

            # Master playlist
            location = /${h.masterPlaylist} {
                tcp_nodelay    on;
                add_header Cache-Control "${nginx.masterCacheControl}";
                add_header Access-Control-Allow-Origin "*";
            }

            # Variant playlists - immediate delivery
            location ~ \.m3u8\$ {
                tcp_nodelay    on;
                add_header Cache-Control "${nginx.manifestCacheControl}";
                add_header Access-Control-Allow-Origin "*";
            }

            # Segments - throughput optimized
            location ~ \.ts\$ {
                sendfile       on;
                tcp_nopush     on;
                add_header Cache-Control "${nginx.segmentCacheControl}";
                add_header Access-Control-Allow-Origin "*";
                add_header Access-Control-Expose-Headers "Content-Length,Content-Range";
            }

            location /health { return 200 "OK\n"; }
            location /nginx_status { stub_status on; }
        }
    }
NGINX_EOF

    echo "▶ Starting Nginx on port $PORT..."
    nginx -c "$NGINX_CONF" -g "daemon off;" &
    CHILD_PIDS="$CHILD_PIDS $!"

    echo ""
    echo "╔════════════════════════════════════════════════════════════════════════╗"
    echo "║                         Origin Ready!                                  ║"
    echo "╠════════════════════════════════════════════════════════════════════════╣"
    echo "║ Stream:   http://localhost:$PORT/${h.playlistName}                               ║"
    echo "║ Health:   http://localhost:$PORT/health                                      ║"
    echo "║ Metrics:  http://localhost:$PORT/nginx_status                                ║"
    echo "╠════════════════════════════════════════════════════════════════════════╣"
    echo "║ Verification commands:                                                 ║"
    echo "║   # Check caching headers:                                             ║"
    echo "║   curl -I http://localhost:$PORT/${h.playlistName}                               ║"
    echo "║   curl -I http://localhost:$PORT/seg00001.ts                                 ║"
    echo "║                                                                        ║"
    echo "║   # Verify stream metadata with ffprobe:                               ║"
    echo "║   ffprobe http://localhost:$PORT/${h.playlistName}                               ║"
    echo "║                                                                        ║"
    echo "║   # Check segment continuity (run twice, 2s apart):                    ║"
    echo "║   curl -s http://localhost:$PORT/${h.playlistName} | grep MEDIA-SEQUENCE         ║"
    echo "╚════════════════════════════════════════════════════════════════════════╝"
    echo ""
    echo "Test playback:"
    echo "  ffplay http://localhost:$PORT/${h.playlistName}"
    echo "  mpv http://localhost:$PORT/${h.playlistName}"
    echo ""
    echo "Load test with go-ffmpeg-hls-swarm:"
    echo "  go-ffmpeg-hls-swarm -clients 50 http://localhost:$PORT/${h.playlistName}"
    echo ""
    echo "Press Ctrl+C to stop"

    wait
  '';
}
