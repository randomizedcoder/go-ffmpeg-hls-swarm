# Nginx HLS server - High-performance configuration with optimized caching
# See: docs/TEST_ORIGIN.md for detailed documentation
#
# Performance features:
# - aio threads: Async I/O prevents worker blocking under load
# - directio 4m: Only use direct I/O for files > 4MB (allows OS page cache for segments)
# - tcp_nopush for segments: Fill packets for throughput
# - tcp_nodelay for manifests: Immediate delivery for freshness
# - keepalive_timeout 30s: Optimized for HLS polling (frees connections faster)
# - reset_timedout_connection: Free memory from dirty client exits
# - client_body_buffer_size 128k: Minimal buffer (HLS origins don't accept POST)
# - Dynamic cache headers based on segment duration
# - Method filtering: Only GET/HEAD/OPTIONS allowed (security)
# - Optional buffered logging for performance analysis
#
# Note: nixpkgs nginx is built with --with-pcre-jit (PCRE2 with JIT) by default
# for fast regex matching on location blocks.
#
{ pkgs, lib, config }:

let
  cfg = config.server;
  c = config.cache;
  h = config.hls;
  log = config.logging;
  d = config.derived;

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

  # ═══════════════════════════════════════════════════════════════════════════
  # Logging configuration
  # ═══════════════════════════════════════════════════════════════════════════

  # Build access_log directive with buffering
  # Format: access_log /path format buffer=512k flush=10s [gzip=4];
  mkAccessLog = path: format:
    let
      gzipPart = if log.gzip > 0 then " gzip=${toString log.gzip}" else "";
    in "access_log ${path} ${format} buffer=${log.buffer} flush=${log.flushInterval}${gzipPart}";

  # Log format definitions for http block
  logFormats = ''
    # ═══════════════════════════════════════════════════════════════════════
    # Custom log formats for HLS performance analysis
    # ═══════════════════════════════════════════════════════════════════════

    # Timing format - includes request_time for latency analysis
    log_format timing '$remote_addr - [$time_local] '
                      '"$request" $status $body_bytes_sent '
                      'rt=$request_time';

    # HLS performance format - ISO timestamps, compact
    log_format hls_perf '$time_iso8601 $status $request_time $body_bytes_sent '
                        '$request_uri';
  '';

  # Access log directive for segments
  segmentAccessLog = if log.enabled
    then mkAccessLog "${log.directory}/${log.files.segments}" "hls_perf"
    else "access_log off";

  # Access log directive for manifests
  manifestAccessLog = if log.enabled && !log.segmentsOnly
    then mkAccessLog "${log.directory}/${log.files.manifests}" "timing"
    else "access_log off";

  # Default access log (for locations not specifically configured)
  defaultAccessLog = if log.enabled && !log.segmentsOnly
    then mkAccessLog "${log.directory}/${log.files.all}" "timing"
    else "access_log off";

in rec {
  # Export cache headers
  inherit segmentCacheControl manifestCacheControl masterCacheControl;

  # Export logging configuration for other modules
  inherit logFormats segmentAccessLog manifestAccessLog defaultAccessLog;
  loggingEnabled = log.enabled;
  loggingDirectory = log.directory;

  # ═══════════════════════════════════════════════════════════════════════════
  # High-performance nginx.conf - Optimized for 100k+ concurrent connections
  # ═══════════════════════════════════════════════════════════════════════════
  configFile = pkgs.writeText "nginx-hls.conf" ''
    user nginx;  # Run as dedicated nginx user (non-root for security)
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

        ${lib.optionalString log.enabled logFormats}

        # ═══════════════════════════════════════════════════════════════
        # Performance tuning (global)
        # ═══════════════════════════════════════════════════════════════
        sendfile           on;
        tcp_nopush         on;      # Fill packets before sending (throughput)
        keepalive_timeout  30;      # Reduced for HLS polling (frees connections faster)
        keepalive_requests 10000;

        # Free memory faster from dirty client exits
        reset_timedout_connection on;

        # File descriptor caching - see docs/NGINX_HLS_CACHING_DESIGN.md
        # Dynamic sizing: max=${toString d.openFileCacheMax} = (${toString d.filesPerVariant} files/variant × ${toString d.variantCount} variants + 1 master) × 3
        # Tiered strategy: aggressive for segments (10s), frequent for manifests (500ms per-location)
        open_file_cache          max=${toString d.openFileCacheMax} inactive=30s;
        open_file_cache_valid    10s;   # Default for segments (immutable)
        open_file_cache_min_uses 1;
        open_file_cache_errors   on;

        sendfile_max_chunk 512k;
        ${defaultAccessLog};
        gzip off;  # .ts files are already compressed

        # Client body buffer (HLS origins don't accept POST)
        client_body_buffer_size 128k;

        # ═══════════════════════════════════════════════════════════════
        # Async I/O for high-load scenarios
        # Optimized for small HLS segments (2-4MB) on fast storage
        # directio 4m: Only use direct I/O for files > 4MB
        # This allows OS page cache to serve hot .m3u8 and .ts files
        # ═══════════════════════════════════════════════════════════════
        aio            threads=default;
        directio       4m;  # Only use direct I/O for files > 4MB (allows page cache for segments)

        server {
            listen ${toString cfg.port} reuseport;
            server_name _;
            root ${cfg.hlsDir};

            # ═══════════════════════════════════════════════════════════
            # Security: Method filtering (HLS origins only need GET/HEAD/OPTIONS)
            # ═══════════════════════════════════════════════════════════
            if ($request_method !~ ^(GET|HEAD|OPTIONS)$ ) {
                return 405;
            }

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
                # Override global open_file_cache_valid for manifests (1s vs 10s)
                # Note: 500ms not supported by nginx - using 1s as fallback
                open_file_cache_valid 1s;
                # Small output buffer for immediate send (manifests are ~400 bytes)
                output_buffers 1 4k;
                ${manifestAccessLog};
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
                # Larger output buffers for throughput (segments are ~1.3MB)
                output_buffers 2 256k;
                ${segmentAccessLog};
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
    user nginx;  # Run as dedicated nginx user (non-root for security)
    worker_processes 1;
    error_log /dev/stderr warn;
    pid /tmp/nginx-test.pid;

    events { worker_connections 4096; }

    http {
        include ${pkgs.nginx}/conf/mime.types;
        default_type application/octet-stream;
        sendfile on;
        tcp_nopush on;
        keepalive_timeout 30;
        access_log off;
        reset_timedout_connection on;
        client_body_buffer_size 128k;

        # File caching
        open_file_cache max=1000 inactive=30s;
        open_file_cache_valid 10s;

        # Async I/O (optimized for small segments)
        aio threads=default;
        directio 4m;

        server {
            listen ${toString port};
            root ${hlsDir};

            # Security: Method filtering
            if ($request_method !~ ^(GET|HEAD|OPTIONS)$ ) {
                return 405;
            }

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

  # Export config file as a package (for nginx-config generator)
  configPackage = configFile;
}
