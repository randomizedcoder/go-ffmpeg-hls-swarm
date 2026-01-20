# Nginx HLS server - High-performance configuration with optimized caching
# See: docs/TEST_ORIGIN.md for detailed documentation
#
# Performance features:
# - aio threads: Async I/O prevents worker blocking under load
# - tcp_nopush for segments: Fill packets for throughput
# - tcp_nodelay for manifests: Immediate delivery for freshness
# - reset_timedout_connection: Free memory from dirty client exits
# - Dynamic cache headers based on segment duration
#
{ pkgs, lib, config }:

let
  cfg = config.server;
  c = config.cache;
  h = config.hls;

  # ═══════════════════════════════════════════════════════════════════════════
  # Build Cache-Control header values (dynamically from config)
  # ═══════════════════════════════════════════════════════════════════════════
  mkCacheControl = opts:
    lib.concatStringsSep ", " (
      lib.optional opts.public "public"
      ++ lib.optional (opts ? maxAge) "max-age=${toString opts.maxAge}"
      ++ lib.optional (opts ? staleWhileRevalidate) "stale-while-revalidate=${toString opts.staleWhileRevalidate}"
      ++ lib.optional (opts.immutable or false) "immutable"
      ++ [ "no-transform" ]
    );

  # Cache control headers (exported for use by other modules)
  segmentCacheControl = mkCacheControl c.segment;
  manifestCacheControl = mkCacheControl c.manifest;
  masterCacheControl = mkCacheControl c.master;

  # Segment lifetime for comments
  segmentLifetime = toString ((h.listSize + h.deleteThreshold) * h.segmentDuration);

in rec {
  # Export cache headers
  inherit segmentCacheControl manifestCacheControl masterCacheControl;

  # ═══════════════════════════════════════════════════════════════════════════
  # High-performance nginx.conf - Optimized for 100k+ concurrent connections
  # ═══════════════════════════════════════════════════════════════════════════
  configFile = pkgs.writeText "nginx-hls.conf" ''
    worker_processes auto;
    worker_rlimit_nofile 65535;
    error_log /dev/stderr warn;
    pid /tmp/nginx.pid;

    # Thread pool for async I/O
    thread_pool default threads=32 max_queue=65536;

    events {
        worker_connections 16384;
        use epoll;
        multi_accept on;
    }

    http {
        include       ${pkgs.nginx}/conf/mime.types;
        default_type  application/octet-stream;

        # ═══════════════════════════════════════════════════════════════
        # Performance tuning (global)
        # ═══════════════════════════════════════════════════════════════
        sendfile           on;
        tcp_nopush         on;      # Fill packets before sending (throughput)
        keepalive_timeout  65;
        keepalive_requests 10000;

        # Free memory faster from dirty client exits
        reset_timedout_connection on;

        # File descriptor caching (reduces stat() syscalls)
        open_file_cache          max=10000 inactive=30s;
        open_file_cache_valid    10s;
        open_file_cache_min_uses 1;
        open_file_cache_errors   on;

        sendfile_max_chunk 512k;
        access_log off;
        gzip off;  # .ts files are already compressed

        # ═══════════════════════════════════════════════════════════════
        # Async I/O for high-load scenarios
        # Even with tmpfs, prevents worker blocking on metadata ops
        # ═══════════════════════════════════════════════════════════════
        aio            threads=default;
        directio       512;

        server {
            listen ${toString cfg.port} reuseport;
            server_name _;
            root ${cfg.hlsDir};

            # ═══════════════════════════════════════════════════════════
            # Master playlist (ABR entry point)
            # - Rarely changes unless variants added/removed
            # - tcp_nodelay for immediate delivery
            # ═══════════════════════════════════════════════════════════
            location = /${h.masterPlaylist} {
                tcp_nodelay    on;  # Immediate delivery
                add_header Cache-Control "${masterCacheControl}";
                add_header Access-Control-Allow-Origin "*";
                add_header Access-Control-Expose-Headers "Content-Length";
                types { application/vnd.apple.mpegurl m3u8; }
            }

            # ═══════════════════════════════════════════════════════════
            # Variant playlists (.m3u8)
            # - Updates every ${toString h.segmentDuration}s
            # - TTL = ${toString c.manifest.maxAge}s, SWR = ${toString c.manifest.staleWhileRevalidate}s
            # - tcp_nodelay for freshness over throughput
            # ═══════════════════════════════════════════════════════════
            location ~ \.m3u8$ {
                tcp_nodelay    on;  # Immediate delivery for freshness
                add_header Cache-Control "${manifestCacheControl}";
                add_header Access-Control-Allow-Origin "*";
                add_header Access-Control-Expose-Headers "Content-Length";
                types { application/vnd.apple.mpegurl m3u8; }
            }

            # ═══════════════════════════════════════════════════════════
            # Segments (.ts)
            # - Immutable once written
            # - Segment lifetime: ${segmentLifetime}s
            # - Cache TTL: ${toString c.segment.maxAge}s (generous, safe)
            # - tcp_nopush for throughput (fill packets)
            # ═══════════════════════════════════════════════════════════
            location ~ \.ts$ {
                sendfile       on;
                tcp_nopush     on;   # Fill packets for throughput
                aio            threads;
                add_header Cache-Control "${segmentCacheControl}";
                add_header Access-Control-Allow-Origin "*";
                add_header Access-Control-Expose-Headers "Content-Length,Content-Range";
                add_header Accept-Ranges bytes;
                types { video/mp2t ts; }
            }

            # ═══════════════════════════════════════════════════════════
            # Health check
            # ═══════════════════════════════════════════════════════════
            location /health {
                return 200 "OK\n";
                add_header Content-Type text/plain;
                add_header Cache-Control "no-store";
            }

            # ═══════════════════════════════════════════════════════════
            # Metrics (for monitoring/observability)
            # ═══════════════════════════════════════════════════════════
            location /nginx_status {
                stub_status on;
                access_log off;
                add_header Cache-Control "no-store";
            }

            # ═══════════════════════════════════════════════════════════
            # CORS preflight
            # ═══════════════════════════════════════════════════════════
            location / {
                if ($request_method = 'OPTIONS') {
                    add_header Access-Control-Allow-Origin "*";
                    add_header Access-Control-Allow-Methods "GET, HEAD, OPTIONS";
                    add_header Access-Control-Allow-Headers "Range";
                    add_header Access-Control-Max-Age 86400;
                    add_header Content-Length 0;
                    return 204;
                }
            }
        }
    }
  '';

  # ═══════════════════════════════════════════════════════════════════════════
  # Minimal config for runner script (dynamic port/dir)
  # ═══════════════════════════════════════════════════════════════════════════
  minimalConfigTemplate = port: hlsDir: pkgs.writeText "nginx-minimal.conf" ''
    worker_processes 1;
    error_log /dev/stderr warn;
    pid /tmp/nginx-test.pid;

    events { worker_connections 4096; }

    http {
        include ${pkgs.nginx}/conf/mime.types;
        default_type application/octet-stream;
        sendfile on;
        tcp_nopush on;
        access_log off;
        reset_timedout_connection on;

        # File caching
        open_file_cache max=1000 inactive=30s;
        open_file_cache_valid 10s;

        server {
            listen ${toString port};
            root ${hlsDir};

            # Master playlist
            location = /${h.masterPlaylist} {
                tcp_nodelay    on;
                add_header Cache-Control "${masterCacheControl}";
                add_header Access-Control-Allow-Origin "*";
            }

            # Variant playlists - immediate delivery
            location ~ \.m3u8$ {
                tcp_nodelay    on;
                add_header Cache-Control "${manifestCacheControl}";
                add_header Access-Control-Allow-Origin "*";
            }

            # Segments - throughput optimized
            location ~ \.ts$ {
                sendfile       on;
                tcp_nopush     on;
                add_header Cache-Control "${segmentCacheControl}";
                add_header Access-Control-Allow-Origin "*";
            }

            location /health { return 200 "OK\n"; }
            location /nginx_status { stub_status on; }
        }
    }
  '';

  # Shell script for standalone use
  script = pkgs.writeShellScript "nginx-hls-server" ''
    exec ${pkgs.nginx}/bin/nginx -c ${configFile} -g "daemon off;"
  '';

  # Systemd service configuration
  systemdService = {
    description = "Nginx HLS Server";
    after = [ "network.target" ];
    wantedBy = [ "multi-user.target" ];
    serviceConfig = {
      ExecStart = "${pkgs.nginx}/bin/nginx -c ${configFile} -g 'daemon off;'";
      Restart = "always";
    };
  };

  runtimeInputs = [ pkgs.nginx ];
}
