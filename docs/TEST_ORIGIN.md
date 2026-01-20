# Test HLS Origin Server — Design Document

> **Status**: Draft
> **Type**: Infrastructure Component
> **Related**: [DESIGN.md](DESIGN.md), [FFMPEG_HLS_REFERENCE.md](FFMPEG_HLS_REFERENCE.md), [NIX_FLAKE_DESIGN.md](NIX_FLAKE_DESIGN.md), [NIX_NGINX_REFERENCE.md](NIX_NGINX_REFERENCE.md)

---

## Overview

A reproducible, containerized HLS origin server for testing `go-ffmpeg-hls-swarm`. Uses FFmpeg to generate live HLS streams from test patterns and Nginx to serve them with high-performance configuration.

**Key requirements**:
- No external stream sources needed
- Reproducible via Nix
- High-performance serving for load testing
- Rolling segment window (live stream simulation)
- Optional tmpfs caching for maximum throughput

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         Test Origin Container                            │
│                                                                          │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │                        FFmpeg Process                              │   │
│  │   ┌─────────────┐    ┌──────────────┐    ┌─────────────────────┐ │   │
│  │   │ Test Source │ -> │  H.264 Enc   │ -> │   HLS Muxer         │ │   │
│  │   │ (testsrc2)  │    │  (libx264)   │    │   (segment files)   │ │   │
│  │   └─────────────┘    └──────────────┘    └─────────┬───────────┘ │   │
│  └────────────────────────────────────────────────────┼─────────────┘   │
│                                                        │                 │
│                                      Writes segments   │                 │
│                                      + playlist        ▼                 │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │                      /var/hls/ (tmpfs)                            │   │
│  │   stream.m3u8   seg000.ts   seg001.ts   seg002.ts   ...          │   │
│  │   (rolling)     (deleted after window)                            │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                          ▲                               │
│                                          │ Read-only                     │
│  ┌──────────────────────────────────────┼───────────────────────────┐   │
│  │                      Nginx Process   │                            │   │
│  │   ┌─────────────────────────────────┴────────────────────────┐   │   │
│  │   │                High-Performance Config                    │   │   │
│  │   │  - sendfile on                                            │   │   │
│  │   │  - tcp_nopush/nodelay                                     │   │   │
│  │   │  - open_file_cache                                        │   │   │
│  │   │  - No buffering for .ts files                             │   │   │
│  │   │  - Proper Cache-Control headers                           │   │   │
│  │   └───────────────────────────────────────────────────────────┘   │   │
│  └───────────────────────────────────────────────────────────────────┘   │
│                                          │                               │
│                                          │ :8080                         │
└──────────────────────────────────────────┼───────────────────────────────┘
                                           │
                                           ▼
                            ┌──────────────────────────────┐
                            │  go-ffmpeg-hls-swarm Clients │
                            │  GET /stream.m3u8            │
                            │  GET /segXXX.ts              │
                            └──────────────────────────────┘
```

### MicroVM Architecture

When deployed as a MicroVM, the same components run inside a lightweight VM:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              Host (NixOS/Linux)                              │
│                                                                              │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │                    MicroVM (qemu/firecracker/etc)                    │   │
│   │                                                                      │   │
│   │    ┌────────────────────────────────────────────────────────────┐   │   │
│   │    │                    NixOS Guest                              │   │   │
│   │    │                                                             │   │   │
│   │    │   systemd.services.hls-generator  ←── reuses ffmpeg.nix    │   │   │
│   │    │          ↓                                                  │   │   │
│   │    │   /var/hls (tmpfs)                ←── reuses config.nix    │   │   │
│   │    │          ↓                                                  │   │   │
│   │    │   services.nginx                  ←── reuses nginx.nix     │   │   │
│   │    │          ↓                                                  │   │   │
│   │    │   :8080 (virtio-net)                                        │   │   │
│   │    └────────────────────────────────────────────────────────────┘   │   │
│   │                           │                                          │   │
│   │              Port forward │ (user networking)                        │   │
│   └───────────────────────────┼──────────────────────────────────────────┘   │
│                               │                                              │
│                          localhost:8080                                      │
└───────────────────────────────┼──────────────────────────────────────────────┘
                                │
                                ▼
                 ┌──────────────────────────────┐
                 │  go-ffmpeg-hls-swarm Clients │
                 └──────────────────────────────┘
```

**Key benefit**: The same `config.nix`, `ffmpeg.nix`, and `nginx.nix` are reused across:
- Local runner script
- OCI container
- MicroVM

---

## FFmpeg Command

### Base Command (Live HLS Generation)

```bash
ffmpeg -re \
  -f lavfi -i "testsrc2=size=1280x720:rate=30:duration=0" \
  -f lavfi -i "sine=frequency=1000:sample_rate=48000:duration=0" \
  -c:v libx264 -preset ultrafast -tune zerolatency \
  -profile:v baseline -level 3.1 \
  -g 60 -keyint_min 60 -sc_threshold 0 \
  -b:v 2000k -maxrate 2000k -bufsize 4000k \
  -c:a aac -b:a 128k -ar 48000 \
  -f hls \
  -hls_time 2 \
  -hls_list_size 10 \
  -hls_flags delete_segments+omit_endlist \
  -hls_segment_filename "/var/hls/seg%05d.ts" \
  /var/hls/stream.m3u8
```

### Command Breakdown

| Option | Value | Purpose |
|--------|-------|---------|
| `-re` | - | Read input at native frame rate (real-time) |
| `-f lavfi -i "testsrc2=..."` | 1280x720@30fps | Synthetic test pattern (no external source) |
| `-f lavfi -i "sine=..."` | 1kHz @ 48kHz | Synthetic audio tone |
| `duration=0` | infinite | Run indefinitely for live simulation |
| `-c:v libx264` | H.264 | Universal codec support |
| `-preset ultrafast` | - | Minimize CPU for test source |
| `-tune zerolatency` | - | Reduce encoder buffering |
| `-g 60` | 2 seconds | GOP size (keyframe every 60 frames @ 30fps) |
| `-keyint_min 60` | 2 seconds | Force regular keyframes for segments |
| `-sc_threshold 0` | disabled | No scene-change keyframes (predictable segments) |
| `-hls_time 2` | 2 seconds | Segment duration |
| `-hls_list_size 10` | 10 segments | Rolling window (20 seconds of content) |
| `-hls_flags delete_segments` | - | Auto-delete old segments |
| `-hls_flags omit_endlist` | - | Live stream (no `#EXT-X-ENDLIST`) |

### Available Test Patterns

FFmpeg provides several test pattern sources via `lavfi`:

| Source | Description | Example |
|--------|-------------|---------|
| `testsrc` | Classic test pattern with scrolling numbers | `-f lavfi -i testsrc=size=1280x720:rate=30` |
| `testsrc2` | Modern pattern with timestamp overlay | `-f lavfi -i testsrc2=size=1280x720:rate=30` |
| `smptebars` | SMPTE color bars (broadcast standard) | `-f lavfi -i smptebars=size=1280x720:rate=30` |
| `pal75bars` | PAL 75% color bars | `-f lavfi -i pal75bars=size=1280x720:rate=30` |
| `color` | Solid color | `-f lavfi -i color=c=blue:size=1280x720:rate=30` |
| `rgbtestsrc` | RGB gradient test | `-f lavfi -i rgbtestsrc=size=1280x720:rate=30` |

### Source Code Reference

From FFmpeg source (`libavformat/hlsenc.c` lines 3106-3165):

```c
static const AVOption options[] = {
    {"hls_time",      "set segment length", ...},     // Default: 2s
    {"hls_list_size", "set maximum number of playlist entries", ...}, // Default: 5
    {"hls_flags",     "set flags affecting HLS playlist", ...},
    // Key flags:
    //   delete_segments - remove old .ts files
    //   omit_endlist    - live stream (no #EXT-X-ENDLIST)
    //   temp_file       - write to temp then rename (atomic)
    {"hls_segment_filename", "filename template for segment files", ...},
    // ...
};
```

---

## Nginx Configuration

> **Complete Nginx reference**: See [NIX_NGINX_REFERENCE.md](NIX_NGINX_REFERENCE.md) for all available NixOS module options, HTTP/3 setup, and performance tuning.

### Package Selection

Nixpkgs provides multiple Nginx variants. For HLS load testing, we recommend:

| Package | Version | Recommendation |
|---------|---------|----------------|
| `pkgs.nginxStable` | 1.28.0 | Conservative choice |
| `pkgs.nginxMainline` | **1.29.4** | **Recommended** — latest HTTP/3, performance fixes |
| `pkgs.angie` | - | Nginx fork with additional features |

**Built-in modules** (all enabled by default in nixpkgs):
- `--with-http_v2_module` — HTTP/2
- `--with-http_v3_module` — **HTTP/3 (QUIC)**
- `--with-threads` — Thread pool for async I/O
- `--with-file-aio` — Async file I/O
- `--with-http_stub_status_module` — Metrics endpoint

### High-Performance Config

```nginx
worker_processes auto;
worker_rlimit_nofile 65535;
thread_pool default threads=32 max_queue=65536;  # For async I/O

events {
    worker_connections 16384;
    use epoll;
    multi_accept on;
}

http {
    include       mime.types;
    default_type  application/octet-stream;

    # Performance tuning (global)
    sendfile           on;
    tcp_nopush         on;      # Fill packets before sending (segments)
    keepalive_timeout  65;
    keepalive_requests 10000;
    reset_timedout_connection on;  # Free memory from dirty client exits

    # Async I/O (prevents worker blocking even with tmpfs)
    aio            threads=default;
    directio       512;

    # File descriptor caching (critical for HLS)
    open_file_cache          max=10000 inactive=30s;
    open_file_cache_valid    10s;
    open_file_cache_min_uses 2;
    open_file_cache_errors   on;

    # Buffer tuning - avoid copying for static files
    sendfile_max_chunk 512k;

    # Logging (disable for max performance)
    access_log off;
    error_log /dev/stderr warn;

    # Gzip off for .ts files (already compressed)
    gzip off;

    server {
        listen 8080 reuseport;
        server_name _;

        root /var/hls;

        # HLS playlist - immediate delivery for freshness
        location ~ \.m3u8$ {
            tcp_nodelay    on;  # Send immediately (don't wait to fill packet)
            add_header Cache-Control "public, max-age=1, stale-while-revalidate=2, no-transform";
            add_header Access-Control-Allow-Origin "*";
            add_header Vary "Accept-Encoding";  # If gzip enabled for manifests
            types {
                application/vnd.apple.mpegurl m3u8;
            }
        }

        # HLS segments - throughput optimized with aggressive caching
        location ~ \.ts$ {
            sendfile       on;
            tcp_nopush     on;   # Fill packets for throughput
            aio            threads;
            add_header Cache-Control "public, max-age=60, immutable, no-transform";
            add_header Access-Control-Allow-Origin "*";
            add_header Accept-Ranges bytes;
            types {
                video/mp2t ts;
            }
        }

        # Health check
        location /health {
            return 200 "OK\n";
            add_header Content-Type text/plain;
            add_header Cache-Control "no-store";
        }

        # Metrics endpoint for Prometheus
        location /nginx_status {
            stub_status on;
            access_log off;
            allow 127.0.0.1;
            deny all;
        }
    }
}
```

### HTTP/3 (QUIC) Support

Nginx mainline 1.29.4 includes native HTTP/3 support. For HTTPS testing with HTTP/3:

```nix
# NixOS module configuration for HTTP/3
services.nginx = {
  enable = true;
  package = pkgs.nginxMainline;  # Required for HTTP/3

  virtualHosts."hls-origin" = {
    # SSL required for QUIC/HTTP/3
    onlySSL = true;
    sslCertificate = "/path/to/cert.pem";
    sslCertificateKey = "/path/to/key.pem";

    # Enable QUIC transport
    quic = true;

    # Enable HTTP/3 protocol
    http3 = true;

    # HTTP/2 alongside HTTP/3
    http2 = true;

    # Required once per port for QUIC
    reuseport = true;

    # Advertise HTTP/3 availability
    extraConfig = ''
      add_header Alt-Svc 'h3=":443"; ma=86400';
    '';

    locations = { /* ... */ };
  };
};

# Firewall: HTTP/3 uses UDP
networking.firewall = {
  allowedTCPPorts = [ 443 ];  # HTTPS
  allowedUDPPorts = [ 443 ];  # QUIC
};
```

**HTTP/3 benefits for HLS**:
- 0-RTT connection establishment
- Better performance over lossy networks
- Multiplexed streams without head-of-line blocking

> **Note**: HTTP/3 requires TLS. For local testing without TLS, HTTP/1.1 and HTTP/2 work fine.

### Configuration Rationale

| Setting | Value | Why |
|---------|-------|-----|
| `worker_processes auto` | CPU count | Maximize parallelism |
| `worker_connections 16384` | High | Support many concurrent clients |
| `thread_pool default` | 32 threads | Enable async I/O operations |
| `use epoll` | - | Linux-specific high-performance I/O |
| `sendfile on` | - | Zero-copy from disk to socket |
| `tcp_nopush` (global) | - | Send full packets (efficient for .ts files) |
| `tcp_nodelay` (.m3u8 only) | - | Immediate delivery for manifests |
| `aio threads` | - | Async I/O prevents worker blocking |
| `directio 512` | - | Direct I/O for large files |
| `open_file_cache` | 10000 files | Avoid repeated stat() calls |
| `reset_timedout_connection` | on | Free memory from stale connections |
| `reuseport` | - | Kernel load balancing across workers |
| `max-age=1, swr=2` for .m3u8 | - | Short cache with stale-while-revalidate |
| `max-age=60, immutable` for .ts | - | Segments are immutable, generous cache |
| `Vary: Accept-Encoding` | - | Proper caching with compression variants |

### NixOS Module Options

Instead of raw nginx.conf, use NixOS's structured configuration:

```nix
services.nginx = {
  enable = true;
  package = pkgs.nginxMainline;

  # Enable recommended settings (one-liners!)
  recommendedOptimisation = true;   # sendfile, tcp_nopush, tcp_nodelay
  recommendedTlsSettings = true;    # Modern TLS (if using HTTPS)
  recommendedProxySettings = true;  # Proxy headers (if reverse proxying)

  # Status page for monitoring
  statusPage = true;  # Enables /nginx_status on localhost

  # Custom additions
  appendHttpConfig = ''
    aio threads;
    directio 512;
    reset_timedout_connection on;
    open_file_cache max=10000 inactive=30s;
    open_file_cache_valid 10s;
    open_file_cache_errors on;
  '';

  virtualHosts."hls-origin" = {
    listen = [{ addr = "0.0.0.0"; port = 8080; }];
    root = "/var/hls";

    locations = {
      "~ \\.m3u8$".extraConfig = /* manifest config */;
      "~ \\.ts$".extraConfig = /* segment config */;
    };
  };
};
```

This approach:
- Reduces boilerplate
- Ensures consistent defaults
- Automatically handles MIME types (via `mailcap`)
- Validates config at build time

---

## HLS Rolling Window — Deep Dive

### How Rolling Window Works

FFmpeg's HLS muxer maintains a sliding window of segments for live streams:

```
Time →  (hls_list_size=10, hls_delete_threshold=3)
─────────────────────────────────────────────────────────────────────────────

t=0    Segment 1 created, added to playlist
       [seg00001.ts]
       playlist: seg00001.ts

t=2s   Segment 2 created
       [seg00001.ts] [seg00002.ts]
       playlist: seg00001.ts, seg00002.ts

...

t=18s  Segment 10 created (hls_list_size=10 reached)
       [seg00001.ts] ... [seg00010.ts]
       playlist: seg00001.ts ... seg00010.ts

t=20s  Segment 11 created → Segment 1 REMOVED from playlist
       [seg00001.ts] [seg00002.ts] ... [seg00011.ts]  (seg01 now "old")
       playlist: seg00002.ts ... seg00011.ts
       old_segments: seg00001.ts (kept due to delete_threshold)

t=22s  Segment 12 created → Segment 2 removed
       old_segments: seg00001.ts, seg00002.ts

t=24s  Segment 13 created → Segment 3 removed
       old_segments: seg00001.ts, seg00002.ts, seg00003.ts (threshold=3 reached)

t=26s  Segment 14 created → Segment 4 removed
       old_segments: seg00002.ts, seg00003.ts, seg00004.ts
       ↓
       seg00001.ts DELETED (exceeded threshold of 3)

       ...and so on (rolling window)
```

### Key FFmpeg Options

| Option | Default | Our Setting | Effect |
|--------|---------|-------------|--------|
| `hls_list_size` | 5 | 10 | Segments in playlist (rolling window size) |
| `hls_time` | 2s | 2s | Target segment duration |
| `hls_delete_threshold` | 1 | **3** | Segments to keep after removal from playlist |
| `delete_segments` flag | off | **on** | Actually delete old .ts files |
| `omit_endlist` flag | off | **on** | No `#EXT-X-ENDLIST` (live stream) |
| `temp_file` flag | off | **on** | Atomic writes via .tmp rename |

### File Count Calculation

**Single bitrate stream:**

```
Files on disk at any time = hls_list_size + hls_delete_threshold + 1
                          = 10 + 3 + 1
                          = 14 segment files maximum

Plus:
- 1 playlist file (stream.m3u8)
- 1 temp playlist during write (stream.m3u8.tmp)

Total: ~16 files
```

**Multi-bitrate stream (2 variants):**

```
Per variant: 14 segment files
2 variants: 28 segment files
Plus: 2 variant playlists + 1 master playlist + temp files

Total: ~32 files
```

### Storage Space Calculation

**Segment size formula:**

```
Segment size = (video_bitrate + audio_bitrate) × segment_duration / 8

For our default config:
  = (2000 kbps + 128 kbps) × 2 seconds / 8
  = 2128 kbps × 2s / 8
  = 532 KB per segment
```

**Total storage (single bitrate):**

```
Storage = segment_size × (hls_list_size + hls_delete_threshold + 1)
        = 532 KB × 14
        ≈ 7.4 MB
```

**Total storage (2 bitrates):**

| Variant | Bitrate | Segment Size | Files | Storage |
|---------|---------|--------------|-------|---------|
| 720p | 2000+128 kbps | 532 KB | 14 | 7.4 MB |
| 360p | 500+64 kbps | 141 KB | 14 | 2.0 MB |
| **Total** | | | **28** | **9.4 MB** |

Plus ~100KB for playlists → **~10 MB total**

### Delete Process Internals

From FFmpeg source (`libavformat/hlsenc.c`):

```c
// When a new segment is created and list is full:
if (hls->max_nb_segments && vs->nb_entries >= hls->max_nb_segments) {
    // Move oldest segment to old_segments list
    en->next = vs->old_segments;
    vs->old_segments = en;

    // Delete old segments beyond threshold
    hls_delete_old_segments(s, hls, vs);
}

// hls_delete_old_segments() walks old_segments list
// and calls unlink() on files beyond hls_delete_threshold
```

**Timeline of a single segment (with hls_delete_threshold=3):**

```
t=0    Created: seg00001.ts written to disk
t=0    Listed: Added to playlist (stream.m3u8)
t=20s  Unlisted: Removed from playlist (10 new segments pushed in)
t=20s  Queued: Moved to old_segments list
t=26s  Deleted: unlink() called when threshold (3) exceeded
```

**Segment lifetime = 26 seconds** — cached content remains valid for this duration.

### Playlist Content Example

After 20 seconds of streaming with `hls_list_size=10`:

```m3u8
#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:2
#EXT-X-MEDIA-SEQUENCE:2
#EXTINF:2.000000,
seg00002.ts
#EXTINF:2.000000,
seg00003.ts
#EXTINF:2.000000,
seg00004.ts
#EXTINF:2.000000,
seg00005.ts
#EXTINF:2.000000,
seg00006.ts
#EXTINF:2.000000,
seg00007.ts
#EXTINF:2.000000,
seg00008.ts
#EXTINF:2.000000,
seg00009.ts
#EXTINF:2.000000,
seg00010.ts
#EXTINF:2.000000,
seg00011.ts
```

Note: `#EXT-X-MEDIA-SEQUENCE:2` indicates segment 1 was removed.

---

## Multi-Bitrate (ABR) Stream

### Why Multi-Bitrate?

Real HLS streams offer multiple quality levels for Adaptive Bitrate (ABR) switching:

| Benefit | Description |
|---------|-------------|
| **Realistic testing** | Simulate real CDN traffic patterns |
| **Higher load** | Multiple variants = more segment requests |
| **ABR behavior** | Test client switching between qualities |

### Bitrate Ladder Configuration

```nix
# nix/test-origin/config.nix (extended for multi-bitrate)
{
  # ... existing config ...

  # ABR variants (2 quality levels)
  variants = [
    {
      name = "1080p";
      width = 1920;
      height = 1080;
      videoBitrate = "4000k";
      audioBitrate = "192k";
    }
    {
      name = "480p";
      width = 854;
      height = 480;
      videoBitrate = "800k";
      audioBitrate = "96k";
    }
  ];

  # Simplified 2-variant ladder
  variants2 = [
    { name = "high"; width = 1280; height = 720; videoBitrate = "2000k"; audioBitrate = "128k"; }
    { name = "low";  width = 640;  height = 360; videoBitrate = "500k";  audioBitrate = "64k"; }
  ];
}
```

### Multi-Bitrate FFmpeg Command

```bash
ffmpeg -re \
  -f lavfi -i "testsrc2=size=1920x1080:rate=30:duration=0" \
  -f lavfi -i "sine=frequency=1000:sample_rate=48000:duration=0" \
  \
  # Split video into 2 scaled outputs
  -filter_complex "[0:v]split=2[v1][v2]; \
    [v1]scale=1280:720[v720p]; \
    [v2]scale=640:360[v360p]" \
  \
  # Encode variant 1 (720p)
  -map "[v720p]" -map 1:a \
  -c:v:0 libx264 -preset ultrafast -tune zerolatency \
  -b:v:0 2000k -maxrate:v:0 2200k -bufsize:v:0 4000k \
  -g 60 -keyint_min 60 -sc_threshold 0 \
  -c:a:0 aac -b:a:0 128k \
  \
  # Encode variant 2 (360p)
  -map "[v360p]" -map 1:a \
  -c:v:1 libx264 -preset ultrafast -tune zerolatency \
  -b:v:1 500k -maxrate:v:1 550k -bufsize:v:1 1000k \
  -g 60 -keyint_min 60 -sc_threshold 0 \
  -c:a:1 aac -b:a:1 64k \
  \
  # HLS output with variant stream map
  -f hls \
  -hls_time 2 \
  -hls_list_size 10 \
  -hls_flags delete_segments+omit_endlist \
  -var_stream_map "v:0,a:0,name:720p v:1,a:1,name:360p" \
  -master_pl_name master.m3u8 \
  -hls_segment_filename "/var/hls/%v/seg%05d.ts" \
  /var/hls/%v/stream.m3u8
```

### Directory Structure (Multi-Bitrate)

```
/var/hls/
├── master.m3u8              # Master playlist (ABR entry point)
├── 720p/
│   ├── stream.m3u8          # 720p variant playlist
│   ├── seg00001.ts
│   ├── seg00002.ts
│   └── ...
└── 360p/
    ├── stream.m3u8          # 360p variant playlist
    ├── seg00001.ts
    ├── seg00002.ts
    └── ...
```

### Master Playlist Content

```m3u8
#EXTM3U
#EXT-X-VERSION:3

#EXT-X-STREAM-INF:BANDWIDTH=2128000,RESOLUTION=1280x720,NAME="720p"
720p/stream.m3u8

#EXT-X-STREAM-INF:BANDWIDTH=564000,RESOLUTION=640x360,NAME="360p"
360p/stream.m3u8
```

### Multi-Bitrate File Count & Storage

| Variant | Segment Size | Segments | Storage |
|---------|--------------|----------|---------|
| 720p (2000k+128k) | 532 KB | 12 | 6.4 MB |
| 360p (500k+64k) | 141 KB | 12 | 1.7 MB |
| Playlists | ~1 KB each | 3 | ~3 KB |
| **Total** | | **~27 files** | **~8.1 MB** |

> **Note**: See [nix/test-origin/config.nix](#nixtestoriginconfignix) in the Nix Implementation section for the complete configuration.

---

## tmpfs Consideration

### When to Use tmpfs

| Scenario | Recommendation |
|----------|---------------|
| Max throughput testing | **Use tmpfs** - eliminates disk I/O |
| Realistic origin simulation | **Use disk** - includes I/O latency |
| Memory-constrained host | **Use disk** - segments consume RAM |
| SSD/NVMe storage | **Either** - disk I/O is fast anyway |

### tmpfs Setup

```bash
# Mount tmpfs for HLS directory
mount -t tmpfs -o size=256M tmpfs /var/hls

# In Nix (NixOS module)
fileSystems."/var/hls" = {
  device = "tmpfs";
  fsType = "tmpfs";
  options = [ "size=256M" "mode=1777" ];
};
```

### Memory Calculation

See [HLS Rolling Window — Deep Dive](#hls-rolling-window--deep-dive) for detailed calculations.

**Quick reference (with hls_delete_threshold=3):**

| Mode | Files | Storage | Recommended tmpfs |
|------|-------|---------|-------------------|
| Single bitrate (720p@2Mbps) | ~16 | ~8 MB | 64 MB |
| Multi-bitrate (2 variants) | ~32 | ~12 MB | 128 MB |
| Multi-bitrate (3+ variants) | ~48+ | ~20 MB | 256 MB |

**Formula:**
```
tmpfs size = (storage × 3) + 32MB overhead
           = (~12 MB × 3) + 32 MB
           = ~68 MB minimum for 2 variants
```

We use 256 MB for safety margin (temp files, multiple streams, growth).

---

## HTTP Caching Strategy for Live HLS

### Overview

Live HLS streaming has two fundamentally different file types with opposing caching requirements:

| File Type | Behavior | Caching Need |
|-----------|----------|--------------|
| **Segments (.ts)** | Immutable once written | Cache aggressively |
| **Playlists (.m3u8)** | Constantly rewritten | Serve fresh, but optimize |

**The challenge**: Maximize performance while ensuring clients always get valid content.

### Understanding the Live Edge

```
Timeline of a live HLS stream (hls_time=2s, hls_list_size=10)
═══════════════════════════════════════════════════════════════════════════════

                    ◄─── 20 seconds of content in playlist ───►

Segments:    [seg05] [seg06] [seg07] [seg08] [seg09] [seg10] [seg11] [seg12] [seg13] [seg14]
                                                                              ▲
                                                                              │
                                                                         Live Edge
                                                                    (newest segment)

Player position:              ▲
                              │
                        Typical player
                     (3 segments behind live edge
                      for buffer safety)
```

**Key insight**: Players don't need old segments — they track near the live edge. Segments only need to be cached for:

```
Cache duration = (hls_list_size × hls_time) + safety_buffer
               = (10 × 2s) + 10s
               = 30 seconds
```

### Cache Timing Calculations

#### Segment Cache Duration

```
Variables:
  hls_list_size = 10         (segments in playlist)
  hls_time = 2s              (segment duration)
  hls_delete_threshold = 3   (segments kept after removal from playlist)

Segment lifetime on disk:
  = (hls_list_size + hls_delete_threshold) × hls_time
  = (10 + 3) × 2s
  = 26 seconds

Safe cache duration (segment lifetime + small buffer):
  = segment_lifetime + (2 × hls_time)
  = 26s + 4s
  = 30 seconds

Recommended: max-age=30
```

> **Why hls_delete_threshold=3?** Gives 6 extra seconds (3 × 2s) of buffer for slow
> clients before segments are deleted. Default of 1 was too aggressive for cached edge
> cases where a client's playlist is slightly stale.

#### Playlist (Manifest) Cache Duration

**Critical constraint**: Playlist updates every `hls_time` seconds (2s). Clients MUST get fresh playlists to discover new segments.

```
Worst case with caching:
  1. Playlist cached at t=0 (contains seg01-seg10)
  2. Client requests at t=10s, gets cached version
  3. Reality: seg01-seg05 deleted, seg11-seg15 exist
  4. Client tries to fetch seg01 → 404 ERROR

Maximum safe cache time:
  = hls_time - (network_latency + processing_time)
  = 2s - 0.5s
  = 1.5 seconds (theoretical max)

Practical recommendation: 0-1 second, or use stale-while-revalidate
```

### Stale-While-Revalidate Strategy

`stale-while-revalidate` is ideal for live HLS manifests:

```
Cache-Control: max-age=1, stale-while-revalidate=2
```

**How it works:**

```
t=0.0s  Client A requests /stream.m3u8
        → Cache MISS, fetch from origin, cache response
        → Client A gets fresh manifest

t=0.5s  Client B requests /stream.m3u8
        → Cache HIT (age=0.5s < max-age=1s)
        → Client B gets cached manifest immediately

t=1.5s  Client C requests /stream.m3u8
        → Cache STALE (age=1.5s > max-age=1s, but < max-age + swr=3s)
        → Client C gets stale manifest IMMEDIATELY
        → Cache triggers background revalidation
        → Fresh manifest cached for next request

t=4.0s  Client D requests /stream.m3u8
        → Cache EXPIRED (age=4s > max-age + swr=3s)
        → Client D waits for fresh fetch
```

**Benefits:**
- Clients never wait for origin during the stale window
- Origin load reduced by coalescing requests
- Graceful degradation if origin is slow

### Recommended Cache Headers

#### For Segments (.ts files)

```nginx
location ~ \.ts$ {
    # Segments are immutable - cache for their full lifetime
    add_header Cache-Control "public, max-age=30, immutable, no-transform";

    # Allow CDN/proxy caching
    add_header Surrogate-Control "max-age=30";

    # CORS for cross-origin players
    add_header Access-Control-Allow-Origin "*";
    add_header Access-Control-Expose-Headers "Content-Length,Content-Range";

    # Correct MIME type
    types { video/mp2t ts; }
}
```

**Header explanation:**

| Header | Value | Purpose |
|--------|-------|---------|
| `max-age=30` | 30 seconds | Cache for segment lifetime (26s + 4s buffer) |
| `immutable` | - | Segment content never changes (skip revalidation) |
| `public` | - | Allow shared caches (CDN, proxy) |
| `no-transform` | - | Prevent proxies from transcoding |

#### For Variant Playlists (.m3u8 files)

```nginx
location ~ \.m3u8$ {
    # Fresh for 1s, serve stale for 2s while revalidating
    # stale-while-revalidate reduces origin load while keeping content fresh
    add_header Cache-Control "public, max-age=1, stale-while-revalidate=2, no-transform";

    # Help CDNs understand the content is dynamic
    add_header Surrogate-Control "max-age=1, stale-while-revalidate=2";

    # CORS
    add_header Access-Control-Allow-Origin "*";
    add_header Access-Control-Expose-Headers "Content-Length";

    # Correct MIME type
    types { application/vnd.apple.mpegurl m3u8; }
}
```

**Why `max-age=1` instead of `no-cache`?**

| Approach | Origin RPS (1000 clients) | Latency P50 | Safety |
|----------|---------------------------|-------------|--------|
| `no-cache` | 500 RPS (per playlist update) | Higher (wait for origin) | Safest |
| `max-age=1, swr=2` | ~50 RPS (coalesced) | Lower (serve stale) | Safe with delete_threshold=3 |

With `hls_delete_threshold=3`, segments live 26 seconds — plenty of safety margin for 1-3s manifest staleness.

#### For Master Playlist (if using ABR)

```nginx
location = /master.m3u8 {
    # Master playlist rarely changes (only if variants change)
    # Can cache longer than variant playlists
    add_header Cache-Control "public, max-age=5, stale-while-revalidate=10, no-transform";

    add_header Access-Control-Allow-Origin "*";
    add_header Access-Control-Expose-Headers "Content-Length";
    types { application/vnd.apple.mpegurl m3u8; }
}
```

### What Can Go Wrong

#### Problem 1: Stale Manifest → 404 on Segments

**Scenario:**
```
1. Client gets cached manifest listing seg001-seg010
2. Client processes manifest slowly (or has high latency)
3. By the time client requests seg001, it's been deleted
4. Result: 404 Not Found → playback stutter/failure
```

**Our mitigations:**

| Mitigation | Our Setting | Effect |
|------------|-------------|--------|
| **Increased delete_threshold** | `-hls_delete_threshold 3` | 6 extra seconds of segment life |
| **Short manifest cache** | `max-age=1, swr=2` | Clients get fresh playlists |
| **temp_file flag** | `-hls_flags temp_file` | Atomic writes prevent partial reads |

**Our safe window:**

```
Segment lifetime = (hls_list_size + delete_threshold) × hls_time
                 = (10 + 3) × 2s
                 = 26 seconds

With max-age=1 for manifests, worst case staleness = 3s
Safety margin = 26s - 3s = 23 seconds ✓
```

#### Problem 2: Race Condition During Segment Write

**Scenario (without mitigation):**
```
1. FFmpeg starts writing seg015.ts
2. Client requests seg015.ts (listed in manifest)
3. Nginx serves partial file
4. Result: Corrupt segment → decoder error
```

**Our solution**: The `temp_file` flag in our configuration:

```bash
-hls_flags delete_segments+omit_endlist+temp_file
```

**How temp_file works:**
```
1. FFmpeg creates seg015.ts.tmp
2. FFmpeg writes full segment to .tmp file
3. FFmpeg renames seg015.ts.tmp → seg015.ts (atomic)
4. Nginx only sees complete files
```

This is already implemented in our `config.nix` → `hls.flags` list.

#### Problem 3: CDN Cache Inconsistency

**Scenario:**
```
1. CDN edge A caches manifest at t=0 (seg001-seg010)
2. CDN edge B caches manifest at t=5 (seg003-seg012)
3. Client load-balanced between edges gets inconsistent view
4. Result: Missed segments, discontinuity
```

**Mitigations:**

| Approach | Implementation | Trade-off |
|----------|----------------|-----------|
| **Sticky sessions** | Route client to same edge | Reduces CDN effectiveness |
| **Lower TTL** | Very short manifest cache | Higher origin load |
| **Surrogate-Key purge** | Invalidate on new segment | CDN-specific |
| **Edge-side includes** | Assemble at edge | Complex |

**Recommended**: For test origin, use short TTL. For production CDN, implement cache invalidation.

#### Problem 4: Thundering Herd on Origin

**Scenario:**
```
1. Manifest cache expires at t=10
2. 1000 clients simultaneously request manifest
3. All requests hit origin (cache miss)
4. Origin overwhelmed
```

**Mitigations:**

| Approach | Implementation | Trade-off |
|----------|----------------|-----------|
| **stale-while-revalidate** | Single revalidation | Best option |
| **Request coalescing** | Nginx proxy_cache_lock | Adds latency |
| **Cache warming** | Pre-fetch manifests | Complexity |

**Recommended**: Combine stale-while-revalidate with proxy_cache_lock:

```nginx
proxy_cache_lock on;
proxy_cache_lock_timeout 5s;
proxy_cache_use_stale updating;
```

### Cache Configuration Summary

Our implementation uses these exact headers (defined in `config.nix`):

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        HLS Caching Decision Tree                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Request for .ts segment?                                                    │
│       │                                                                      │
│       ├─ YES → Cache-Control: public, max-age=30, immutable, no-transform   │
│       │        ✓ Cache for segment lifetime (26s) + buffer                  │
│       │                                                                      │
│       └─ NO → Is it master.m3u8?                                            │
│               │                                                              │
│               ├─ YES → Cache-Control: public, max-age=5, swr=10, no-transform│
│               │        ✓ Rarely changes, moderate caching                   │
│               │                                                              │
│               └─ NO → Variant playlist (.m3u8)                              │
│                       │                                                      │
│                       └─ Cache-Control: public, max-age=1, swr=2, no-transform│
│                          ✓ Updates every 2s, minimal caching + swr          │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Timing Relationships

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ Parameter Relationships for Safe Caching                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  hls_time (segment duration)           = 2 seconds                          │
│  hls_list_size                         = 10 segments                        │
│  hls_delete_threshold                  = 3 segments (increased for safety)  │
│                                                                              │
│  ─────────────────────────────────────────────────────────────────────────  │
│                                                                              │
│  Playlist window                       = list_size × hls_time               │
│                                        = 10 × 2s = 20 seconds               │
│                                                                              │
│  Segment lifetime on disk              = (list_size + threshold) × hls_time │
│                                        = (10 + 3) × 2s = 26 seconds         │
│                                                                              │
│  ─────────────────────────────────────────────────────────────────────────  │
│                                                                              │
│  Manifest max-age                      ≤ hls_time / 2                       │
│                                        = 2s / 2 = 1 second                  │
│                                                                              │
│  Manifest stale-while-revalidate       ≤ hls_time                           │
│                                        = 2 seconds                          │
│                                                                              │
│  Segment max-age                       ≤ segment_lifetime                   │
│                                        = 26 seconds (use 30 for margin)     │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Updated Configuration Values

Add to `config.nix`:

```nix
{
  # ... existing config ...

  hls = {
    segmentDuration = 2;
    listSize = 10;
    deleteThreshold = 3;  # Increased from 1 for cache safety
    # ...
  };

  # Cache timing (derived from HLS settings)
  cache = {
    # Segment cache: slightly longer than segment lifetime
    segmentMaxAge = 30;  # seconds
    segmentImmutable = true;

    # Manifest cache: very short, with stale-while-revalidate
    manifestMaxAge = 1;  # seconds
    manifestStaleWhileRevalidate = 2;  # seconds

    # Master playlist (ABR only): moderate caching
    masterMaxAge = 5;
    masterStaleWhileRevalidate = 10;
  };
}
```

### Testing Cache Behavior

```bash
# Test segment caching
curl -I http://localhost:8080/seg00001.ts
# Expect: Cache-Control: public, max-age=30, immutable

# Test manifest caching
curl -I http://localhost:8080/stream.m3u8
# Expect: Cache-Control: public, max-age=1, stale-while-revalidate=2

# Test with timing
for i in {1..5}; do
  echo "=== Request $i ==="
  curl -w "Time: %{time_total}s\n" -o /dev/null -s http://localhost:8080/stream.m3u8
  sleep 0.5
done
# Should see consistent fast times with stale-while-revalidate

# Verify segment availability after manifest update
curl -s http://localhost:8080/stream.m3u8 | grep -o 'seg[0-9]*.ts' | while read seg; do
  curl -sf -o /dev/null "http://localhost:8080/$seg" && echo "$seg OK" || echo "$seg MISSING"
done
```

---

## Nix Implementation

### Modular File Structure

Each component is isolated for maintainability:

```
nix/
├── lib.nix                    # Shared metadata (existing)
├── package.nix                # go-ffmpeg-hls-swarm package (existing)
├── shell.nix                  # Dev shell (existing)
├── checks.nix                 # Go checks (existing)
├── apps.nix                   # App definitions (existing)
├── test-origin/               # Test origin components
│   ├── default.nix            # Entry point, combines components
│   ├── config.nix             # Shared configuration values
│   ├── sysctl.nix             # Kernel network tuning
│   ├── ffmpeg.nix             # FFmpeg HLS generator
│   ├── nginx.nix              # Nginx web server
│   ├── runner.nix             # Combined runner script
│   ├── container.nix          # OCI container definition
│   ├── microvm.nix            # MicroVM definition (lightweight VM)
│   └── nixos-module.nix       # NixOS module for services
└── tests/
    └── integration.nix        # Integration tests (existing)
```

### Deployment Options

| Method | Use Case | Isolation | Startup Time | Overhead |
|--------|----------|-----------|--------------|----------|
| **Runner script** | Local dev, quick tests | Process | ~3s | Minimal |
| **OCI Container** | Docker/Podman, CI/CD | Container | ~5s | Low |
| **MicroVM** | Full isolation, prod-like | VM | ~10s | Medium |

---

## Container Runtime Requirements

### Overview

OCI containers require a container runtime on the host system. On NixOS, we **recommend Podman** over Docker for several reasons:

| Feature | Podman | Docker |
|---------|--------|--------|
| **Rootless by default** | ✅ Yes | ❌ Requires config |
| **Daemonless** | ✅ No background service | ❌ Requires dockerd |
| **Systemd integration** | ✅ Native | ⚠️ Limited |
| **OCI compliance** | ✅ Full | ✅ Full |
| **NixOS support** | ✅ Excellent | ✅ Good |
| **Resource usage** | Lower (no daemon) | Higher (daemon always running) |
| **Security model** | User namespaces | Root daemon |

### NixOS Podman Configuration (Recommended)

Add to your NixOS `configuration.nix`:

```nix
{ config, pkgs, ... }:

{
  # ═══════════════════════════════════════════════════════════════════════════
  # Podman - Recommended container runtime
  # ═══════════════════════════════════════════════════════════════════════════
  virtualisation.podman = {
    enable = true;

    # Provide 'docker' command alias for compatibility
    dockerCompat = true;

    # Enable DNS for containers (required for network access)
    defaultNetwork.settings.dns_enabled = true;

    # Auto-prune unused images/containers weekly
    autoPrune = {
      enable = true;
      dates = "weekly";
      flags = [ "--all" ];
    };
  };

  # Optional: Allow rootless containers to bind to privileged ports
  # (Needed if you want to run the origin on port 80 instead of 8080)
  boot.kernel.sysctl."net.ipv4.ip_unprivileged_port_start" = 80;

  # Storage driver (overlay is recommended, btrfs if using btrfs filesystem)
  virtualisation.containers.storage.settings = {
    storage = {
      driver = "overlay";
      # For better performance with many layers:
      options.overlay.mountopt = "nodev,metacopy=on";
    };
  };
}
```

### Running the OCI Container

```bash
# Build the container image
nix build .#test-origin-container

# Load into Podman
podman load < result

# Run the test origin container
podman run -d \
  --name hls-origin \
  -p 8080:8080 \
  go-ffmpeg-hls-swarm-test-origin:latest

# Verify it's running
curl http://localhost:8080/health
curl http://localhost:8080/stream.m3u8

# View logs
podman logs -f hls-origin

# Stop and remove
podman stop hls-origin && podman rm hls-origin
```

### Running with Docker (Alternative)

If you prefer Docker, add to `configuration.nix`:

```nix
{
  virtualisation.docker = {
    enable = true;
    # Rootless mode (recommended for security)
    rootless = {
      enable = true;
      setSocketVariable = true;
    };
  };

  # Add your user to the docker group (if not using rootless)
  # users.users.youruser.extraGroups = [ "docker" ];
}
```

```bash
# Load and run with Docker
docker load < result
docker run -d -p 8080:8080 --name hls-origin go-ffmpeg-hls-swarm-test-origin:latest
```

---

## MicroVM Requirements

### Overview

MicroVMs provide **full VM isolation** with near-container startup times (~10 seconds). They're ideal for:

- Production-like testing with real kernel isolation
- Testing kernel sysctl tuning (containers share host kernel)
- Security-sensitive environments
- Environments where containers aren't available

### Host Requirements

MicroVMs require:

1. **Linux host** with KVM support (`/dev/kvm` must exist)
2. **microvm.nix flake** input in your project
3. **Sufficient RAM** (1GB+ recommended for the origin VM)

Check KVM availability:

```bash
# Verify KVM is available
ls -la /dev/kvm
# Should show: crw-rw-rw- 1 root kvm ...

# If missing, load the module
sudo modprobe kvm_intel  # Intel CPUs
sudo modprobe kvm_amd    # AMD CPUs

# Verify virtualization support
grep -E 'vmx|svm' /proc/cpuinfo
```

### NixOS KVM Configuration

```nix
{ config, pkgs, ... }:

{
  # Enable KVM for MicroVMs
  boot.kernelModules = [ "kvm-intel" ];  # or "kvm-amd" for AMD CPUs

  # Allow user access to /dev/kvm
  users.groups.kvm = {};
  users.users.youruser.extraGroups = [ "kvm" ];

  # Alternatively, use libvirtd for more features
  virtualisation.libvirtd = {
    enable = true;
    qemu = {
      package = pkgs.qemu_kvm;
      runAsRoot = false;
    };
  };
}
```

### Flake Configuration for MicroVM

Add the microvm input to your `flake.nix`:

```nix
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

    # MicroVM support
    microvm = {
      url = "github:astro/microvm.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  # Enable microvm binary cache for faster builds
  nixConfig = {
    extra-substituters = [ "https://microvm.cachix.org" ];
    extra-trusted-public-keys = [
      "microvm.cachix.org-1:oXnBc6hRE3eX5rSYdRyMYXnfzcCxC7yKPTbZXALsqys="
    ];
  };

  outputs = { self, nixpkgs, microvm }: {
    # ... expose microvm packages ...
  };
}
```

### Running a MicroVM

```bash
# Build and run the MicroVM
nix run .#test-origin-vm

# The VM will:
# 1. Boot a minimal NixOS (~10 seconds)
# 2. Start FFmpeg HLS generator
# 3. Start Nginx on port 8080 (forwarded to host)
# 4. Apply all sysctl tuning from sysctl.nix

# Test from host
curl http://localhost:8080/health
curl http://localhost:8080/stream.m3u8
```

### Available Hypervisors

MicroVM supports multiple hypervisors:

| Hypervisor | Startup | Features | Recommendation |
|------------|---------|----------|----------------|
| **qemu** | ~10s | Full-featured, broad compatibility | ✅ Default, works everywhere |
| **cloud-hypervisor** | ~3s | Fast, modern, fewer features | Good for speed |
| **firecracker** | ~1s | Fastest, minimal features | AWS Lambda-style workloads |
| **crosvm** | ~5s | Chrome OS hypervisor | Experimental |
| **kvmtool** | ~2s | Minimal, fast | Lightweight testing |

Configure in `microvm.nix`:

```nix
microvm = {
  hypervisor = "qemu";  # or "cloud-hypervisor", "firecracker", etc.
  # ...
};
```

---

## Container vs MicroVM: When to Use Which

### Decision Matrix

| Scenario | Container | MicroVM | Why |
|----------|-----------|---------|-----|
| **Local development** | ✅ | ⚠️ | Faster iteration, simpler setup |
| **CI/CD pipelines** | ✅ | ⚠️ | Containers work in most CI systems |
| **Production load testing** | ⚠️ | ✅ | MicroVM has real kernel isolation |
| **Testing sysctl tuning** | ❌ | ✅ | Containers share host kernel |
| **Air-gapped/security-sensitive** | ⚠️ | ✅ | Full VM isolation |
| **Resource-constrained host** | ✅ | ❌ | Containers have lower overhead |
| **Testing at scale (100+ instances)** | ✅ | ⚠️ | Container orchestration is mature |

### Key Tradeoffs

#### Containers (Podman/Docker)

**Advantages:**
- ✅ Fast startup (~5 seconds)
- ✅ Low resource overhead (shares host kernel)
- ✅ Easy orchestration (Kubernetes, Docker Compose, Podman pods)
- ✅ Works in CI/CD (GitHub Actions, GitLab CI)
- ✅ Simple networking (host port mapping)
- ✅ Widely understood by teams

**Limitations:**
- ❌ Shares host kernel (sysctl changes affect host)
- ❌ Container escape vulnerabilities (rare but possible)
- ❌ Some kernel features unavailable
- ❌ cgroups v2 compatibility issues on older hosts

#### MicroVMs

**Advantages:**
- ✅ Full kernel isolation (real VM)
- ✅ Test sysctl tuning safely
- ✅ Production-identical environment
- ✅ Stronger security boundary
- ✅ Can run different kernel versions

**Limitations:**
- ❌ Slower startup (~10 seconds with qemu)
- ❌ Higher memory overhead (dedicated kernel + userspace)
- ❌ Requires KVM support on host
- ❌ More complex networking setup
- ❌ Doesn't work in most CI environments (no nested virtualization)

### Host Infrastructure Considerations

| Aspect | Containers | MicroVMs |
|--------|------------|----------|
| **Host OS** | Linux, macOS*, Windows* | Linux only (KVM) |
| **CPU requirements** | Any | VT-x/AMD-V required |
| **Memory per instance** | ~50-100MB | ~512MB-1GB |
| **Disk per instance** | Shared layers | Dedicated image or shared store |
| **Network setup** | Simple (NAT/bridge) | User networking or TAP devices |
| **Scaling to 100+ instances** | Easy | Complex (memory pressure) |

\* macOS and Windows require a Linux VM for containers (e.g., Podman Machine, Docker Desktop)

### Hybrid Approach

For comprehensive testing, use both:

```bash
# Development: Quick iteration with runner script
nix run .#test-origin

# CI/CD: Container for reproducibility
nix build .#test-origin-container
podman run -d -p 8080:8080 go-ffmpeg-hls-swarm-test-origin:latest

# Pre-production: MicroVM for sysctl tuning validation
nix run .#test-origin-vm
# Verify: sysctl net.ipv4.tcp_rmem  # Check tuning applied
```

---

### nix/test-origin/config.nix

**Function-based configuration with profile support**. Instead of a static config, use a function
that returns derived values. This enables different "profiles" (e.g., "Low Latency" vs "4K ABR").

**Usage:**

```bash
# Default profile
nix run .#test-origin

# Low-latency profile (1s segments)
nix run .#test-origin-low-latency

# 4K ABR profile (5 quality variants)
nix run .#test-origin-4k-abr

# Stress test profile (optimized for stability)
nix run .#test-origin-stress
```

```nix
# Function-based config with profile support
# Usage:
#   config = import ./config.nix { profile = "default"; }
#   config = import ./config.nix { profile = "low-latency"; }
#   config = import ./config.nix { profile = "4k-abr"; overrides = { hls.listSize = 15; }; }
#
{ profile ? "default", overrides ? {} }:

let
  # ═══════════════════════════════════════════════════════════════
  # Profile definitions
  # ═══════════════════════════════════════════════════════════════
  profiles = {
    default = { hls.segmentDuration = 2; hls.listSize = 10; };
    low-latency = { hls.segmentDuration = 1; hls.listSize = 6; };
    "4k-abr" = { multibitrate = true; /* 5 variants */ };
    stress-test = { hls.listSize = 15; encoder.framerate = 25; };
  };

  # ═══════════════════════════════════════════════════════════════
  # Base configuration (merged with profile and overrides)
  # ═══════════════════════════════════════════════════════════════
  baseConfig = {
    multibitrate = false;

    hls = {
      segmentDuration = 2;
      listSize = 10;
      deleteThreshold = 5;  # Safe buffer for SWR/CDN lag (increased from 3)
      segmentPattern = "seg%05d.ts";
      playlistName = "stream.m3u8";
      masterPlaylist = "master.m3u8";
      flags = [ "delete_segments" "omit_endlist" "temp_file" ];
    };

    server = { port = 8080; hlsDir = "/var/hls"; };
    audio = { frequency = 1000; sampleRate = 48000; };
    testPattern = "testsrc2";
    video = { width = 1280; height = 720; bitrate = "2000k"; /* ... */ };
    encoder = { framerate = 30; preset = "ultrafast"; /* ... */ };
  };

  # Deep merge: base <- profile <- overrides
  mergedConfig = deepMerge (deepMerge baseConfig profiles.${profile}) overrides;

  # ═══════════════════════════════════════════════════════════════
  # Derived values (computed automatically from config)
  # ═══════════════════════════════════════════════════════════════
  derived = {
    gopSize = enc.framerate * h.segmentDuration;
    segmentLifetimeSec = (h.listSize + h.deleteThreshold) * h.segmentDuration;
    playlistWindowSec = h.listSize * h.segmentDuration;
    filesPerVariant = h.listSize + h.deleteThreshold + 1;

    # tmpfs size: (Bitrate * Window * 2) + 64MB
    recommendedTmpfsMB = /* calculated */;
  };

  # ═══════════════════════════════════════════════════════════════
  # Cache timing (dynamically calculated from segment duration)
  # Manifest TTL = segmentDuration / 2
  # Manifest SWR = segmentDuration
  # ═══════════════════════════════════════════════════════════════
  cache = {
    segment = {
      maxAge = 60;        # Segments are immutable; generous TTL
      immutable = true;
      public = true;
    };

    manifest = {
      maxAge = h.segmentDuration / 2;        # Dynamic: TTL = seg/2
      staleWhileRevalidate = h.segmentDuration;  # Dynamic: SWR = seg
      public = true;
    };

    master = { maxAge = 5; staleWhileRevalidate = 10; public = true; };
  };

in mergedConfig // { inherit derived cache; }
```

### Recommended Constants

Based on the design review, these are the optimal "safe" ratios:

| Metric | Value | Rationale |
|--------|-------|-----------|
| `hls_delete_threshold` | **5** | Safe buffer for SWR/CDN lag |
| Segment Cache TTL | **60s** | Segments are immutable; generous TTL |
| Manifest Cache TTL | `segmentDuration / 2` | Dynamic based on segment timing |
| Manifest SWR | `segmentDuration` | Match segment update frequency |
| tmpfs Size | `(Bitrate × Window × 2) + 64MB` | Double buffer + overhead |

### Storage Summary

With `deleteThreshold = 5`, segments remain on disk for 30 seconds (15 segment cycles):

| Configuration | Variants | Files | Peak Storage | tmpfs |
|--------------|----------|-------|--------------|-------|
| `multibitrate = false` | 1 | ~16 | ~8 MB | 64M |
| `multibitrate = true` (default) | 2 | ~32 | ~12 MB | 128M |
| Custom 3-variant | 3 | ~48 | ~18 MB | 256M |

---

### nix/test-origin/sysctl.nix

**High-performance kernel network tuning** for HLS streaming. This module is imported by both `nixos-module.nix` and `microvm.nix`.

```nix
# High-performance network tuning for HLS streaming
# See full implementation in nix/test-origin/sysctl.nix
{ config, pkgs, lib, ... }:

{
  boot.kernel.sysctl = {
    # Connection limits (10k+ concurrent)
    "net.core.somaxconn" = 65535;
    "net.ipv4.tcp_max_syn_backlog" = 65535;

    # Fast dead connection detection (2 min vs 11 min default)
    "net.ipv4.tcp_keepalive_time" = 120;
    "net.ipv4.tcp_keepalive_intvl" = 30;
    "net.ipv4.tcp_keepalive_probes" = 4;

    # Large TCP buffers for HLS segments (16MB max)
    "net.ipv4.tcp_rmem" = "4096 1000000 16000000";
    "net.ipv4.tcp_wmem" = "4096 1000000 16000000";

    # Low latency optimizations
    "net.ipv4.tcp_notsent_lowat" = 131072;
    "net.ipv4.tcp_slow_start_after_idle" = 0;
    "net.ipv4.tcp_fastopen" = 3;
    "net.ipv4.tcp_rto_min_us" = 50000;

    # Queueing and congestion
    "net.core.default_qdisc" = "cake";
    "net.ipv4.tcp_congestion_control" = "cubic";

    # ... (see full file for complete settings)
  };
}
```

**Key tuning categories:**

| Category | Settings | Impact |
|----------|----------|--------|
| **Connection limits** | somaxconn, tcp_max_syn_backlog | 10k+ concurrent clients |
| **Dead connection detection** | tcp_keepalive_* | 2 min (vs 11 min default) |
| **Buffer sizes** | tcp_rmem/wmem, core buffers | 16MB max for HLS throughput |
| **Low latency** | tcp_notsent_lowat, tcp_fastopen | Fast manifest delivery |
| **Congestion control** | default_qdisc, tcp_congestion_control | CAKE + CUBIC/BBR |

---

### nix/test-origin/ffmpeg.nix

**Modular FFmpeg argument builder with `mkFfmpegArgs` helper**. This makes integration tests
much cleaner as you can override specific flags without rewriting the whole string.

```nix
# FFmpeg HLS stream generator with modular argument builder
{ pkgs, lib, config }:

let
  # ═══════════════════════════════════════════════════════════════
  # mkFfmpegArgs: Modular argument builder for FFmpeg HLS generation
  # ═══════════════════════════════════════════════════════════════
  # Usage in tests:
  #   args = mkFfmpegArgs {}                          # Default args
  #   args = mkFfmpegArgs { hlsDir = "/tmp/test"; }   # Override output dir
  #   args = mkFfmpegArgs { segmentDuration = 1; }    # Override segment time
  #
  mkFfmpegArgs = {
    hlsDir ? cfg.server.hlsDir,
    segmentDuration ? h.segmentDuration,
    listSize ? h.listSize,
    deleteThreshold ? h.deleteThreshold,
    flags ? h.flags,
    extraArgs ? [],
    # ... all other settings overridable
  }:
  let
    gopSize = framerate * segmentDuration;
    hlsFlags = lib.concatStringsSep "+" flags;
  in [
    "-re"
    "-f" "lavfi"
    "-i" "${testPattern}=size=${width}x${height}:rate=${framerate}:duration=0"
    "-f" "lavfi"
    "-i" "sine=frequency=${audioFrequency}:sample_rate=${audioSampleRate}:duration=0"
    "-c:v" "libx264"
    "-preset" enc.preset
    "-tune" enc.tune
    "-profile:v" enc.profile
    "-level" enc.level
    "-g" (toString gopSize)
    "-keyint_min" (toString gopSize)
    "-sc_threshold" "0"
    "-b:v" v.bitrate
    "-maxrate" v.maxrate
    "-bufsize" v.bufsize
    "-c:a" "aac"
    "-b:a" v.audioBitrate
    "-ar" (toString a.sampleRate)
    "-f" "hls"
    "-hls_time" (toString h.segmentDuration)
    "-hls_list_size" (toString h.listSize)
    "-hls_delete_threshold" (toString h.deleteThreshold)  # Keep N segments after removal
    "-hls_flags" hlsFlags                                  # delete_segments+omit_endlist+temp_file
    "-hls_segment_filename" "${cfg.server.hlsDir}/${h.segmentPattern}"
    "${cfg.server.hlsDir}/${h.playlistName}"
  ];

  # ═══════════════════════════════════════════════════════════════
  # Multi-bitrate mode (ABR ladder)
  # ═══════════════════════════════════════════════════════════════
  variants = cfg.variants;
  numVariants = builtins.length variants;

  # Build filter_complex for scaling
  # [0:v]split=2[v0][v1]; [v0]scale=1280:720[out0]; [v1]scale=640:360[out1]
  filterComplex = let
    splits = lib.concatMapStringsSep "" (i: "[v${toString i}]") (lib.range 0 (numVariants - 1));
    scales = lib.concatMapStringsSep "; " (i:
      let v = builtins.elemAt variants i;
      in "[v${toString i}]scale=${toString v.width}:${toString v.height}[out${toString i}]"
    ) (lib.range 0 (numVariants - 1));
  in "[0:v]split=${toString numVariants}${splits}; ${scales}";

  # Build -map and encoding options for each variant
  variantEncoderArgs = lib.concatMap (i:
    let v = builtins.elemAt variants i;
    in [
      "-map" "[out${toString i}]"
      "-map" "1:a"
      "-c:v:${toString i}" "libx264"
      "-preset" enc.preset
      "-tune" enc.tune
      "-profile:v:${toString i}" enc.profile
      "-b:v:${toString i}" v.bitrate
      "-maxrate:v:${toString i}" v.maxrate
      "-bufsize:v:${toString i}" v.bufsize
      "-g:v:${toString i}" (toString gopSize)
      "-keyint_min:v:${toString i}" (toString gopSize)
      "-sc_threshold:v:${toString i}" "0"
      "-c:a:${toString i}" "aac"
      "-b:a:${toString i}" v.audioBitrate
      "-ar:a:${toString i}" (toString a.sampleRate)
    ]
  ) (lib.range 0 (numVariants - 1));

  # Build var_stream_map: "v:0,a:0,name:720p v:1,a:1,name:360p"
  varStreamMap = lib.concatMapStringsSep " " (i:
    let v = builtins.elemAt variants i;
    in "v:${toString i},a:${toString i},name:${v.name}"
  ) (lib.range 0 (numVariants - 1));

  multiBitrateArgs = let
    maxRes = builtins.head variants;  # First variant is highest resolution
  in [
    "-re"
    "-f" "lavfi"
    "-i" "${cfg.testPattern}=size=${toString maxRes.width}x${toString maxRes.height}:rate=${toString enc.framerate}:duration=0"
    "-f" "lavfi"
    "-i" "sine=frequency=${toString a.frequency}:sample_rate=${toString a.sampleRate}:duration=0"
    "-filter_complex" filterComplex
  ] ++ variantEncoderArgs ++ [
    "-f" "hls"
    "-hls_time" (toString h.segmentDuration)
    "-hls_list_size" (toString h.listSize)
    "-hls_delete_threshold" (toString h.deleteThreshold)  # Keep N segments after removal
    "-hls_flags" hlsFlags                                  # delete_segments+omit_endlist+temp_file
    "-var_stream_map" varStreamMap
    "-master_pl_name" h.masterPlaylist
    "-hls_segment_filename" "${cfg.server.hlsDir}/%v/${h.segmentPattern}"
    "${cfg.server.hlsDir}/%v/${h.playlistName}"
  ];

  # ═══════════════════════════════════════════════════════════════
  # Select mode based on config
  # ═══════════════════════════════════════════════════════════════
  ffmpegArgs = if cfg.multibitrate then multiBitrateArgs else singleBitrateArgs;

in rec {
  inherit ffmpegArgs singleBitrateArgs multiBitrateArgs;

  # Shell script for standalone use
  script = pkgs.writeShellScript "hls-generator" ''
    set -euo pipefail
    mkdir -p ${cfg.server.hlsDir}
    ${lib.optionalString cfg.multibitrate ''
      # Create variant directories for multi-bitrate
      ${lib.concatMapStringsSep "\n" (v: "mkdir -p ${cfg.server.hlsDir}/${v.name}") variants}
    ''}
    exec ${pkgs.ffmpeg-full}/bin/ffmpeg \
      ${lib.concatStringsSep " \\\n      " (map lib.escapeShellArg ffmpegArgs)}
  '';

  # Systemd service configuration
  systemdService = {
    description = "FFmpeg HLS Test Stream Generator (${if cfg.multibitrate then "${toString numVariants} variants" else "single bitrate"})";
    after = [ "network.target" ];
    wantedBy = [ "multi-user.target" ];
    serviceConfig = {
      Type = "simple";
      ExecStartPre = pkgs.writeShellScript "hls-generator-pre" ''
        mkdir -p ${cfg.server.hlsDir}
        ${lib.optionalString cfg.multibitrate ''
          ${lib.concatMapStringsSep "\n" (v: "mkdir -p ${cfg.server.hlsDir}/${v.name}") variants}
        ''}
      '';
      ExecStart = "${pkgs.ffmpeg-full}/bin/ffmpeg ${lib.concatStringsSep " " (map lib.escapeShellArg ffmpegArgs)}";
      Restart = "always";
      RestartSec = 2;
    };
  };

  # Runtime inputs
  runtimeInputs = [ pkgs.ffmpeg-full ];

  # Export for inspection
  mode = if cfg.multibitrate then "multibitrate" else "single";
  variantCount = if cfg.multibitrate then numVariants else 1;
}
```

---

### nix/test-origin/nginx.nix

**High-performance Nginx configuration** optimized for 100k+ concurrent connections.

> **Reference**: See [NIX_NGINX_REFERENCE.md](NIX_NGINX_REFERENCE.md) for all available NixOS options.

Key performance features:
- **`pkgs.nginxMainline`**: Version 1.29.4 with HTTP/3 support
- **`aio threads`**: Async I/O prevents worker blocking under load
- **`tcp_nopush` for segments**: Fill packets for throughput
- **`tcp_nodelay` for manifests**: Immediate delivery for freshness
- **`reset_timedout_connection`**: Free memory from dirty client exits
- **`Vary: Accept-Encoding`**: Proper cache key for compressed variants

```nix
# Nginx HLS server - High-performance configuration
# Uses NixOS module options where possible for cleaner config
{ pkgs, lib, config }:

let
  cfg = config.server;
  h = config.hls;
  c = config.cache;

  # Dynamic cache header builder
  mkCacheControl = { maxAge, staleWhileRevalidate ? null, immutable ? false, public ? true }:
    lib.concatStringsSep ", " (
      lib.optional public "public"
      ++ [ "max-age=${toString maxAge}" ]
      ++ lib.optional (staleWhileRevalidate != null) "stale-while-revalidate=${toString staleWhileRevalidate}"
      ++ lib.optional immutable "immutable"
      ++ [ "no-transform" ]
    );

  segmentCacheControl = mkCacheControl c.segment;   # "public, max-age=60, immutable, no-transform"
  manifestCacheControl = mkCacheControl c.manifest; # "public, max-age=1, stale-while-revalidate=2, no-transform"
  masterCacheControl = mkCacheControl c.master;     # "public, max-age=5, stale-while-revalidate=10, no-transform"

in rec {
  # ═══════════════════════════════════════════════════════════════════════
  # Package selection: use mainline for HTTP/3 and latest fixes
  # ═══════════════════════════════════════════════════════════════════════
  package = pkgs.nginxMainline;  # Version 1.29.4 with HTTP/3

  # ═══════════════════════════════════════════════════════════════════════
  # Raw config file (for standalone use or containers)
  # ═══════════════════════════════════════════════════════════════════════
  configFile = pkgs.writeText "nginx-hls.conf" ''
    worker_processes auto;
    worker_rlimit_nofile 65535;
    thread_pool default threads=32 max_queue=65536;  # For async I/O

    events {
        worker_connections 16384;
        use epoll;
        multi_accept on;
    }

    http {
        include ${package}/conf/mime.types;

        # Performance tuning (global)
        sendfile           on;
        tcp_nopush         on;      # Fill packets before sending
        keepalive_timeout  65;
        keepalive_requests 10000;
        reset_timedout_connection on;  # Free memory from dirty exits

        # File descriptor caching
        open_file_cache max=10000 inactive=30s;
        open_file_cache_valid 10s;
        open_file_cache_errors on;

        # Async I/O (even with tmpfs, prevents blocking)
        aio            threads=default;
        directio       512;

        access_log off;
        gzip off;

        server {
            listen ${toString cfg.port} reuseport;
            root ${cfg.hlsDir};

            # Playlists - immediate delivery for freshness
            location ~ \.m3u8$ {
                tcp_nodelay    on;  # Send immediately (don't wait to fill packet)
                add_header Cache-Control "${manifestCacheControl}";
                add_header Access-Control-Allow-Origin "*";
                add_header Vary "Accept-Encoding";
            }

            # Segments - throughput optimized with aggressive caching
            location ~ \.ts$ {
                sendfile       on;
                tcp_nopush     on;   # Fill packets for throughput
                aio            threads;
                add_header Cache-Control "${segmentCacheControl}";
                add_header Access-Control-Allow-Origin "*";
                add_header Accept-Ranges bytes;
            }

            location /health { return 200 "OK\n"; }
            location /nginx_status { stub_status on; access_log off; }
        }
    }
  '';

  # ═══════════════════════════════════════════════════════════════════════
  # NixOS module configuration (preferred for VMs and integration tests)
  # ═══════════════════════════════════════════════════════════════════════
  nixosModuleConfig = {
    services.nginx = {
      enable = true;
      package = package;

      # Use NixOS recommended settings
      recommendedOptimisation = true;   # sendfile, tcp_nopush, tcp_nodelay, keepalive

      # Status page on localhost
      statusPage = true;

      # Performance additions
      eventsConfig = ''
        worker_connections 16384;
        use epoll;
        multi_accept on;
      '';

      appendHttpConfig = ''
        # Thread pool for async I/O
        aio threads;
        directio 512;

        # Connection cleanup
        reset_timedout_connection on;

        # File descriptor caching
        open_file_cache max=10000 inactive=30s;
        open_file_cache_valid 10s;
        open_file_cache_errors on;

        # Disable compression for HLS (already compressed)
        gzip off;
      '';

      virtualHosts."hls-origin" = {
        listen = [{ addr = "0.0.0.0"; port = cfg.port; }];
        root = cfg.hlsDir;

        locations = {
          # Master playlist (ABR entry point)
          "= /${h.masterPlaylist}" = {
            extraConfig = ''
              tcp_nodelay on;
              add_header Cache-Control "${masterCacheControl}";
              add_header Access-Control-Allow-Origin "*";
              add_header Vary "Accept-Encoding";
            '';
          };

          # Variant playlists - stale-while-revalidate
          "~ \\.m3u8$" = {
            extraConfig = ''
              tcp_nodelay on;
              add_header Cache-Control "${manifestCacheControl}";
              add_header Access-Control-Allow-Origin "*";
              add_header Vary "Accept-Encoding";
            '';
          };

          # Segments - throughput optimized
          "~ \\.ts$" = {
            extraConfig = ''
              sendfile on;
              tcp_nopush on;
              aio threads;
              add_header Cache-Control "${segmentCacheControl}";
              add_header Access-Control-Allow-Origin "*";
              add_header Accept-Ranges bytes;
            '';
          };

          "/health" = {
            return = "200 'OK\\n'";
            extraConfig = ''
              add_header Content-Type text/plain;
              add_header Cache-Control "no-store";
            '';
          };
        };
      };
    };
  };

  # ═══════════════════════════════════════════════════════════════════════
  # HTTP/3 configuration (requires TLS certificates)
  # ═══════════════════════════════════════════════════════════════════════
  http3Config = { certPath, keyPath }: {
    services.nginx.virtualHosts."hls-origin" = {
      onlySSL = true;
      sslCertificate = certPath;
      sslCertificateKey = keyPath;

      quic = true;        # Enable QUIC transport
      http3 = true;       # Enable HTTP/3 protocol
      http2 = true;       # HTTP/2 alongside HTTP/3
      reuseport = true;   # Required for QUIC (once per port)

      extraConfig = ''
        add_header Alt-Svc 'h3=":${toString cfg.port}"; ma=86400';
      '';
    };

    networking.firewall.allowedUDPPorts = [ cfg.port ];  # QUIC uses UDP
  };

  # ═══════════════════════════════════════════════════════════════════════
  # Minimal config for runner script (supports dynamic port/dir)
  # ═══════════════════════════════════════════════════════════════════════
  minimalConfigTemplate = port: hlsDir: pkgs.writeText "nginx-minimal.conf" ''
    worker_processes 1;
    error_log /dev/stderr warn;
    pid /tmp/nginx-test.pid;

    events { worker_connections 4096; }

    http {
        include ${package}/conf/mime.types;
        default_type application/octet-stream;
        sendfile on;
        tcp_nopush on;
        access_log off;

        # File caching
        open_file_cache max=1000 inactive=30s;
        open_file_cache_valid 10s;

        server {
            listen ${toString port};
            root ${hlsDir};

            # Master playlist
            location = /${h.masterPlaylist} {
                tcp_nodelay on;
                add_header Cache-Control "${masterCacheControl}";
                add_header Access-Control-Allow-Origin "*";
            }

            # Variant playlists - stale-while-revalidate for performance
            location ~ \.m3u8$ {
                tcp_nodelay on;
                add_header Cache-Control "${manifestCacheControl}";
                add_header Access-Control-Allow-Origin "*";
            }

            # Segments - cache aggressively
            location ~ \.ts$ {
                sendfile on;
                tcp_nopush on;
                add_header Cache-Control "${segmentCacheControl}";
                add_header Access-Control-Allow-Origin "*";
            }

            location /health { return 200 "OK\n"; }
        }
    }
  '';

  # Shell script for standalone use
  script = pkgs.writeShellScript "nginx-hls-server" ''
    exec ${package}/bin/nginx -c ${configFile} -g "daemon off;"
  '';

  # Systemd service configuration (for non-NixOS systems)
  systemdService = {
    description = "Nginx HLS Server (mainline)";
    after = [ "network.target" ];
    wantedBy = [ "multi-user.target" ];
    serviceConfig = {
      ExecStart = "${package}/bin/nginx -c ${configFile} -g 'daemon off;'";
      Restart = "always";
    };
  };

  runtimeInputs = [ package ];
}
```

---

### nix/test-origin/runner.nix

Combined runner for local development with optimized caching:

```nix
# Combined runner script for local testing
# Implements the HTTP caching strategy from config
{ pkgs, lib, config, ffmpeg, nginx }:

let
  h = config.hls;
  c = config.cache;

  # Build Cache-Control header strings
  segmentCache = "public, max-age=${toString c.segment.maxAge}, immutable, no-transform";
  manifestCache = "public, max-age=${toString c.manifest.maxAge}, stale-while-revalidate=${toString c.manifest.staleWhileRevalidate}, no-transform";
  masterCache = "public, max-age=${toString c.master.maxAge}, stale-while-revalidate=${toString c.master.staleWhileRevalidate}, no-transform";

  # HLS flags string
  hlsFlags = lib.concatStringsSep "+" h.flags;
in
pkgs.writeShellApplication {
  name = "test-hls-origin";
  runtimeInputs = ffmpeg.runtimeInputs ++ nginx.runtimeInputs;

  text = ''
    set -euo pipefail

    # Allow override via environment
    HLS_DIR="''${HLS_DIR:-/tmp/hls-test}"
    PORT="''${PORT:-${toString config.server.port}}"

    echo "╔══════════════════════════════════════════════════════════════════╗"
    echo "║              Test HLS Origin Server                              ║"
    echo "╠══════════════════════════════════════════════════════════════════╣"
    echo "║ HLS Settings:                                                    ║"
    echo "║   Segment duration: ${toString h.segmentDuration}s                                           ║"
    echo "║   Rolling window:   ${toString h.listSize} segments (${toString (h.listSize * h.segmentDuration)}s)                                ║"
    echo "║   Delete threshold: ${toString h.deleteThreshold} segments                                        ║"
    echo "║   Flags:            ${hlsFlags}             ║"
    echo "╠══════════════════════════════════════════════════════════════════╣"
    echo "║ Cache Settings:                                                  ║"
    echo "║   Segments:  max-age=${toString c.segment.maxAge}s, immutable                             ║"
    echo "║   Manifests: max-age=${toString c.manifest.maxAge}s, swr=${toString c.manifest.staleWhileRevalidate}s                                   ║"
    echo "║   Master:    max-age=${toString c.master.maxAge}s, swr=${toString c.master.staleWhileRevalidate}s                                   ║"
    echo "╚══════════════════════════════════════════════════════════════════╝"
    echo ""
    echo "HLS directory: $HLS_DIR"
    echo "Port:          $PORT"
    echo ""

    mkdir -p "$HLS_DIR"
    trap 'echo "Shutting down..."; kill $(jobs -p) 2>/dev/null; rm -rf "$HLS_DIR"' EXIT INT TERM

    # Start FFmpeg HLS generator
    echo "▶ Starting FFmpeg HLS generator..."
    ffmpeg -re \
      -f lavfi -i "${config.testPattern}=size=${toString config.video.width}x${toString config.video.height}:rate=${toString config.encoder.framerate}:duration=0" \
      -f lavfi -i "sine=frequency=${toString config.audio.frequency}:sample_rate=${toString config.audio.sampleRate}:duration=0" \
      -c:v libx264 -preset ${config.encoder.preset} -tune ${config.encoder.tune} \
      -profile:v ${config.encoder.profile} -level ${config.encoder.level} \
      -g ${toString config.encoder.gopSize} \
      -keyint_min ${toString config.encoder.gopSize} \
      -sc_threshold 0 \
      -b:v ${config.video.bitrate} -maxrate ${config.video.maxrate} -bufsize ${config.video.bufsize} \
      -c:a aac -b:a ${config.video.audioBitrate} -ar ${toString config.audio.sampleRate} \
      -f hls \
      -hls_time ${toString h.segmentDuration} \
      -hls_list_size ${toString h.listSize} \
      -hls_delete_threshold ${toString h.deleteThreshold} \
      -hls_flags ${hlsFlags} \
      -hls_segment_filename "$HLS_DIR/${h.segmentPattern}" \
      "$HLS_DIR/${h.playlistName}" 2>&1 | grep -v "^frame=" &

    FFMPEG_PID=$!

    # Wait for HLS stream
    echo "⏳ Waiting for HLS stream..."
    for i in $(seq 1 30); do
      if [ -f "$HLS_DIR/${h.playlistName}" ]; then
        echo "✓ HLS stream ready"
        break
      fi
      sleep 1
    done

    if [ ! -f "$HLS_DIR/${h.playlistName}" ]; then
      echo "✗ ERROR: Failed to generate HLS stream"
      exit 1
    fi

    # Generate nginx config with optimized caching
    NGINX_CONF=$(mktemp --suffix=.conf)
    cat > "$NGINX_CONF" << 'NGINX_EOF'
    worker_processes 1;
    error_log /dev/stderr warn;
    pid /tmp/nginx-hls-$$.pid;
    events { worker_connections 4096; }
    http {
        include ${pkgs.nginx}/conf/mime.types;
        sendfile on;
        tcp_nopush on;
        tcp_nodelay on;
        access_log off;

        # File descriptor caching
        open_file_cache max=1000 inactive=30s;
        open_file_cache_valid 10s;

        server {
            listen $PORT;
            root $HLS_DIR;

            # Master playlist (if using ABR)
            location = /${h.masterPlaylist} {
                add_header Cache-Control "${masterCache}";
                add_header Access-Control-Allow-Origin "*";
            }

            # Variant playlists - stale-while-revalidate
            location ~ \.m3u8$ {
                add_header Cache-Control "${manifestCache}";
                add_header Access-Control-Allow-Origin "*";
            }

            # Segments - cache aggressively (immutable)
            location ~ \.ts$ {
                add_header Cache-Control "${segmentCache}";
                add_header Access-Control-Allow-Origin "*";
                add_header Access-Control-Expose-Headers "Content-Length,Content-Range";
            }

            location /health { return 200 "OK\n"; }
        }
    }
    NGINX_EOF

    echo "▶ Starting Nginx on port $PORT..."
    nginx -c "$NGINX_CONF" -g "daemon off;" &

    echo ""
    echo "╔══════════════════════════════════════════════════════════════════╗"
    echo "║                         Origin Ready!                            ║"
    echo "╠══════════════════════════════════════════════════════════════════╣"
    echo "║ Stream:  http://localhost:$PORT/${h.playlistName}                            ║"
    echo "║ Health:  http://localhost:$PORT/health                                   ║"
    echo "╠══════════════════════════════════════════════════════════════════╣"
    echo "║ Verify caching headers:                                          ║"
    echo "║   curl -I http://localhost:$PORT/${h.playlistName}                           ║"
    echo "║   curl -I http://localhost:$PORT/seg00001.ts                             ║"
    echo "╚══════════════════════════════════════════════════════════════════╝"
    echo ""
    echo "Test with:"
    echo "  ffplay http://localhost:$PORT/${h.playlistName}"
    echo "  curl -s http://localhost:$PORT/${h.playlistName} | head -15"
    echo ""
    echo "Load test with go-ffmpeg-hls-swarm:"
    echo "  ./go-ffmpeg-hls-swarm -clients 50 http://localhost:$PORT/${h.playlistName}"
    echo ""
    echo "Press Ctrl+C to stop"

    wait
  '';
}
```

---

### nix/test-origin/container.nix

OCI container image with optimized Nginx:

```nix
# OCI container for test origin
{ pkgs, config, ffmpeg, nginx }:

let
  # Use mainline Nginx for HTTP/3 support and latest fixes
  nginxPackage = pkgs.nginxMainline;

  # Entrypoint script that runs both services
  entrypoint = pkgs.writeShellScript "entrypoint" ''
    set -euo pipefail

    mkdir -p ${config.server.hlsDir}

    echo "╔══════════════════════════════════════════════════════════════════╗"
    echo "║              Test HLS Origin Server (Container)                  ║"
    echo "║ Nginx:  ${nginxPackage.version} (mainline)                                       ║"
    echo "║ FFmpeg: ${pkgs.ffmpeg-full.version}                                                   ║"
    echo "╚══════════════════════════════════════════════════════════════════╝"

    echo "Starting FFmpeg HLS generator..."
    ${ffmpeg.script} &

    # Wait for stream
    for i in $(seq 1 30); do
      [ -f "${config.server.hlsDir}/${config.hls.playlistName}" ] && break
      sleep 1
    done

    echo "Starting Nginx (mainline)..."
    exec ${nginx.script}
  '';
in
pkgs.dockerTools.buildLayeredImage {
  name = "go-ffmpeg-hls-swarm-test-origin";
  tag = "latest";

  contents = [
    pkgs.ffmpeg-full
    pkgs.ffprobe         # For stream verification
    nginxPackage         # Mainline with HTTP/3
    pkgs.coreutils
    pkgs.bash
    pkgs.curl            # Health check
  ];

  config = {
    Cmd = [ "${entrypoint}" ];
    ExposedPorts = {
      "${toString config.server.port}/tcp" = {};
      "${toString config.server.port}/udp" = {};  # For HTTP/3 (QUIC)
    };
    Volumes = { "${config.server.hlsDir}" = {}; };
    Env = [
      "HLS_DIR=${config.server.hlsDir}"
      "PORT=${toString config.server.port}"
    ];
    Labels = {
      "org.opencontainers.image.title" = "go-ffmpeg-hls-swarm Test Origin";
      "org.opencontainers.image.description" = "Self-contained HLS origin for load testing";
      "nginx.version" = nginxPackage.version;
    };
  };

  extraCommands = ''
    mkdir -p var/hls tmp var/log/nginx
  '';
}
```

---

### nix/test-origin/nixos-module.nix

Shared NixOS module used by both MicroVM and integration tests.

> **Reference**: See [NIX_NGINX_REFERENCE.md](NIX_NGINX_REFERENCE.md) for all available Nginx options.

```nix
# NixOS module for HLS origin services
# Reusable across MicroVMs, containers, and NixOS tests
# Implements HTTP caching strategy from config
{ config, ffmpeg, nginx }:

{ pkgs, lib, ... }:

let
  cfg = config;
  h = cfg.hls;
  c = cfg.cache;
  enc = cfg.encoder;

  # Build Cache-Control headers from config (dynamically calculated)
  segmentCache = "public, max-age=${toString c.segment.maxAge}, immutable, no-transform";
  manifestCache = "public, max-age=${toString c.manifest.maxAge}, stale-while-revalidate=${toString c.manifest.staleWhileRevalidate}, no-transform";
  masterCache = "public, max-age=${toString c.master.maxAge}, stale-while-revalidate=${toString c.master.staleWhileRevalidate}, no-transform";

  # HLS flags string
  hlsFlags = lib.concatStringsSep "+" h.flags;
in
{
  # ═══════════════════════════════════════════════════════════════════════
  # tmpfs for HLS segments (high performance, auto-cleanup)
  # Size calculated: (Bitrate * Window * 2) + 64MB
  # ═══════════════════════════════════════════════════════════════════════
  fileSystems."/var/hls" = {
    device = "tmpfs";
    fsType = "tmpfs";
    options = [ "size=${toString cfg.derived.recommendedTmpfsMB}M" "mode=1777" ];
  };

  # ═══════════════════════════════════════════════════════════════════════
  # FFmpeg HLS generator systemd service
  # ═══════════════════════════════════════════════════════════════════════
  systemd.services.hls-generator = {
    description = "FFmpeg HLS Test Stream Generator";
    after = [ "network.target" ];
    wantedBy = [ "multi-user.target" ];

    serviceConfig = {
      Type = "simple";
      ExecStartPre = "${pkgs.coreutils}/bin/mkdir -p /var/hls";
      ExecStart = lib.concatStringsSep " " ([
        "${pkgs.ffmpeg-full}/bin/ffmpeg"
        "-re"
        "-f" "lavfi" "-i" "\"${cfg.testPattern}=size=${toString cfg.video.width}x${toString cfg.video.height}:rate=${toString enc.framerate}:duration=0\""
        "-f" "lavfi" "-i" "\"sine=frequency=${toString cfg.audio.frequency}:sample_rate=${toString cfg.audio.sampleRate}:duration=0\""
        "-c:v" "libx264" "-preset" enc.preset "-tune" enc.tune
        "-profile:v" enc.profile "-level" enc.level
        "-g" (toString cfg.derived.gopSize)
        "-keyint_min" (toString cfg.derived.gopSize)
        "-sc_threshold" "0"
        "-b:v" cfg.video.bitrate "-maxrate" cfg.video.maxrate "-bufsize" cfg.video.bufsize
        "-c:a" "aac" "-b:a" cfg.video.audioBitrate "-ar" (toString cfg.audio.sampleRate)
        "-f" "hls"
        "-hls_time" (toString h.segmentDuration)
        "-hls_list_size" (toString h.listSize)
        "-hls_delete_threshold" (toString h.deleteThreshold)
        "-hls_flags" hlsFlags
        "-hls_segment_filename" "/var/hls/${h.segmentPattern}"
        "/var/hls/${h.playlistName}"
      ]);
      Restart = "always";
      RestartSec = 2;
    };
  };

  # ═══════════════════════════════════════════════════════════════════════
  # Nginx HLS server - using NixOS module for cleaner config
  # ═══════════════════════════════════════════════════════════════════════
  services.nginx = {
    enable = true;
    package = pkgs.nginxMainline;  # Version 1.29.4 with HTTP/3 support

    # One-liner recommended settings
    recommendedOptimisation = true;   # sendfile, tcp_nopush, tcp_nodelay

    # Enable status page for monitoring
    statusPage = true;  # /nginx_status on localhost

    # Performance additions
    eventsConfig = ''
      worker_connections 16384;
      use epoll;
      multi_accept on;
    '';

    appendHttpConfig = ''
      # Thread pool for async I/O
      aio threads;
      directio 512;

      # Connection cleanup
      reset_timedout_connection on;

      # File descriptor caching
      open_file_cache max=10000 inactive=30s;
      open_file_cache_valid 10s;
      open_file_cache_errors on;

      # Disable compression for HLS (segments already compressed)
      gzip off;
    '';

    virtualHosts."hls-origin" = {
      listen = [{ addr = "0.0.0.0"; port = cfg.server.port; }];
      root = "/var/hls";

      # Master playlist (ABR entry point) - moderate caching
      locations."= /${h.masterPlaylist}" = {
        extraConfig = ''
          tcp_nodelay on;
          add_header Cache-Control "${masterCache}";
          add_header Access-Control-Allow-Origin "*";
          add_header Access-Control-Expose-Headers "Content-Length";
          add_header Vary "Accept-Encoding";
          types { application/vnd.apple.mpegurl m3u8; }
        '';
      };

      # Variant playlists - stale-while-revalidate
      locations."~ \\.m3u8$" = {
        extraConfig = ''
          tcp_nodelay on;
          add_header Cache-Control "${manifestCache}";
          add_header Access-Control-Allow-Origin "*";
          add_header Access-Control-Expose-Headers "Content-Length";
          add_header Vary "Accept-Encoding";
          types { application/vnd.apple.mpegurl m3u8; }
        '';
      };

      # Segments - aggressive caching (immutable)
      locations."~ \\.ts$" = {
        extraConfig = ''
          sendfile on;
          tcp_nopush on;
          aio threads;
          add_header Cache-Control "${segmentCache}";
          add_header Access-Control-Allow-Origin "*";
          add_header Access-Control-Expose-Headers "Content-Length,Content-Range";
          add_header Accept-Ranges bytes;
          types { video/mp2t ts; }
        '';
      };

      locations."/health" = {
        return = "200 'OK\\n'";
        extraConfig = ''
          add_header Content-Type text/plain;
          add_header Cache-Control "no-store";
        '';
      };
    };
  };

  # ═══════════════════════════════════════════════════════════════════════
  # Prometheus metrics exporter (for Grafana dashboards)
  # ═══════════════════════════════════════════════════════════════════════
  services.prometheus.exporters.nginx = {
    enable = true;
    port = 9113;
    scrapeUri = "http://localhost/nginx_status";
  };

  # ═══════════════════════════════════════════════════════════════════════
  # Firewall
  # ═══════════════════════════════════════════════════════════════════════
  networking.firewall.allowedTCPPorts = [
    cfg.server.port
    9113  # Prometheus exporter
  ];

  # ═══════════════════════════════════════════════════════════════════════
  # Kernel Tuning for High-Performance Networking
  # See: https://www.kernel.org/doc/html/latest/networking/ip-sysctl.html
  # ═══════════════════════════════════════════════════════════════════════
  boot.kernel.sysctl = {
    # Connection limits
    "net.core.somaxconn" = 65535;
    "net.ipv4.tcp_max_syn_backlog" = 65535;
    "net.core.netdev_max_backlog" = 65535;

    # Detect dead connections faster (2 min vs 11 min default)
    "net.ipv4.tcp_keepalive_time" = 120;
    "net.ipv4.tcp_keepalive_intvl" = 30;
    "net.ipv4.tcp_keepalive_probes" = 4;

    # Large TCP buffers for high throughput (16MB max)
    "net.ipv4.tcp_rmem" = "4096 1000000 16000000";
    "net.ipv4.tcp_wmem" = "4096 1000000 16000000";
    "net.ipv6.tcp_rmem" = "4096 1000000 16000000";
    "net.ipv6.tcp_wmem" = "4096 1000000 16000000";

    # Core buffers (~25MB)
    "net.core.rmem_default" = 26214400;
    "net.core.rmem_max" = 26214400;
    "net.core.wmem_default" = 26214400;
    "net.core.wmem_max" = 26214400;

    # TCP optimizations
    "net.ipv4.tcp_notsent_lowat" = 131072;   # Reduce latency
    "net.ipv4.tcp_tw_reuse" = 1;             # Reuse TIME-WAIT
    "net.ipv4.tcp_timestamps" = 1;
    "net.ipv4.tcp_ecn" = 1;
    "net.ipv4.tcp_window_scaling" = 1;
    "net.ipv4.tcp_sack" = 1;
    "net.ipv4.tcp_fack" = 1;
    "net.ipv4.tcp_fin_timeout" = 30;

    # Fast start / low latency
    "net.ipv4.tcp_slow_start_after_idle" = 0;
    "net.ipv4.tcp_fastopen" = 3;
    "net.ipv4.tcp_no_ssthresh_metrics_save" = 0;
    "net.ipv4.tcp_reflect_tos" = 1;
    "net.ipv4.tcp_rto_min_us" = 50000;  # 50ms min RTO

    # Port range and queueing
    "net.ipv4.ip_local_port_range" = "1026 65535";
    "net.core.default_qdisc" = "cake";
    "net.ipv4.tcp_congestion_control" = "cubic";
  };
}
```

### Prometheus Nginx Exporter

The NixOS module includes `prometheus-nginx-exporter` for Grafana integration:

```bash
# Verify metrics are being exported
curl http://localhost:9113/metrics | grep nginx

# Example Prometheus scrape config
scrape_configs:
  - job_name: 'hls-origin-nginx'
    static_configs:
      - targets: ['hls-origin:9113']
```

**Available metrics:**
- `nginx_connections_active` — Current active connections
- `nginx_connections_reading` — Connections reading request
- `nginx_connections_writing` — Connections writing response
- `nginx_connections_waiting` — Idle keepalive connections
- `nginx_http_requests_total` — Total request count

---

### nix/test-origin/microvm.nix

MicroVM definition using [microvm.nix](https://github.com/microvm-nix/microvm.nix):

```nix
# MicroVM for test HLS origin
# Lightweight VM with full isolation, ~10s startup
{ pkgs, lib, config, nixosModule, microvm }:

let
  # Shared sysctl tuning for high-performance networking
  sysctlConfig = import ./sysctl.nix { inherit config pkgs; };

  # MicroVM NixOS configuration
  microvmConfig = {
    imports = [
      microvm.nixosModules.microvm
      nixosModule
      sysctlConfig  # Apply network tuning
    ];

    networking.hostName = "hls-origin-vm";

    # Allow root login for debugging
    users.users.root.password = "";
    services.getty.autologinUser = "root";

    # MicroVM-specific configuration
    microvm = {
      # Use qemu for broadest compatibility (supports 9p, user networking)
      hypervisor = "qemu";

      # Memory allocation (increased for large TCP buffers)
      mem = 1536;  # 1.5GB RAM (accounts for sysctl buffer sizes)
      vcpu = 2;    # 2 vCPUs

      # Share host's /nix/store (faster startup, smaller image)
      shares = [{
        tag = "ro-store";
        source = "/nix/store";
        mountPoint = "/nix/.ro-store";
        proto = "9p";
      }];

      # User networking with port forwarding (no host setup required)
      interfaces = [{
        type = "user";
        id = "eth0";
        mac = "02:00:00:01:01:01";
      }];

      # Forward port 8080 -> VM's 8080
      forwardPorts = [{
        from = "host";
        host.port = config.server.port;
        guest.port = config.server.port;
      }];

      # Optional: persistent volume for logs
      # volumes = [{
      #   image = "hls-origin-var.img";
      #   mountPoint = "/var/log";
      #   size = 128;
      # }];
    };

    # ═══════════════════════════════════════════════════════════════════════
    # Additional MicroVM-specific kernel tuning
    # ═══════════════════════════════════════════════════════════════════════
    boot.kernel.sysctl = {
      # VM-specific: reduce memory pressure
      "vm.swappiness" = 10;
      "vm.dirty_ratio" = 40;
      "vm.dirty_background_ratio" = 10;
    };

    # Optimizations for minimal footprint
    documentation.enable = false;
    nix.enable = false;  # No need to build in VM

    # Disable unnecessary services
    services.nscd.enable = false;
    systemd.services.systemd-journal-flush.enable = false;
  };

  # Build the NixOS system
  nixos = lib.nixosSystem {
    system = pkgs.system;
    modules = [ microvmConfig ];
  };

in {
  # The MicroVM runner (executable)
  runner = nixos.config.microvm.declaredRunner;

  # Full NixOS configuration (for inspection)
  inherit nixos;

  # Hypervisor-specific runners
  runners = nixos.config.microvm.runner;
}
```

### Verifying sysctl Settings in MicroVM

```bash
# Start the MicroVM
nix run .#test-origin-vm

# In another terminal, check applied settings
ssh root@localhost -p 2222  # (if SSH enabled)

# Or via console, verify key settings:
sysctl net.ipv4.tcp_rmem
# Expected: net.ipv4.tcp_rmem = 4096	1000000	16000000

sysctl net.ipv4.tcp_fastopen
# Expected: net.ipv4.tcp_fastopen = 3

sysctl net.core.default_qdisc
# Expected: net.core.default_qdisc = cake
```

---

### nix/test-origin/default.nix

Entry point that combines all components:

```nix
# Test origin server - main entry point
# Provides: runner (local), container (OCI), microvm (lightweight VM)
{ pkgs, lib, microvm ? null }:

let
  # Load configuration
  config = import ./config.nix;

  # Load components with config
  ffmpeg = import ./ffmpeg.nix { inherit pkgs config; };
  nginx = import ./nginx.nix { inherit pkgs config; };

  # NixOS module (shared by microvm and integration tests)
  nixosModule = import ./nixos-module.nix { inherit config ffmpeg nginx; };

  # Build composed components
  runner = import ./runner.nix { inherit pkgs config ffmpeg nginx; };
  container = import ./container.nix { inherit pkgs config ffmpeg nginx; };

  # MicroVM (only if microvm input is provided)
  microvm' = if microvm != null
    then import ./microvm.nix { inherit pkgs lib config nixosModule microvm; }
    else null;

in
{
  # Configuration (for inspection/override)
  inherit config;

  # Individual components
  inherit ffmpeg nginx nixosModule;

  # Composed outputs
  inherit runner container;

  # MicroVM outputs (null if microvm input not provided)
  microvm = microvm';

  # Convenience aliases
  runOrigin = runner;

  # Helper to create custom config variants
  withConfig = newConfig: import ./default.nix {
    inherit pkgs lib microvm;
    config = config // newConfig;
  };
}
```

---

### Integration with flake.nix

> **Nginx options**: See [NIX_NGINX_REFERENCE.md](NIX_NGINX_REFERENCE.md) for available Nginx packages and modules.

```nix
{
  description = "go-ffmpeg-hls-swarm - HLS load testing tool";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

    # MicroVM support (optional but recommended)
    microvm = {
      url = "github:microvm-nix/microvm.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  nixConfig = {
    extra-substituters = [ "https://microvm.cachix.org" ];
    extra-trusted-public-keys = [
      "microvm.cachix.org-1:oXnBc6hRE3eX5rSYdRyMYXnfzcCxC7yKPTbZXALsqys="
    ];
  };

  outputs = { self, nixpkgs, microvm }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      lib = nixpkgs.lib;

      # Import test origin with microvm support
      # Uses pkgs.nginxMainline (1.29.4) by default for HTTP/3
      testOrigin = import ./nix/test-origin {
        inherit pkgs lib;
        microvm = microvm;  # Pass microvm input
      };

    in {
      packages.${system} = {
        default = self.packages.${system}.go-ffmpeg-hls-swarm;
        go-ffmpeg-hls-swarm = /* existing package */;

        # Test origin: multiple deployment options
        test-origin = testOrigin.runOrigin;
        test-origin-container = testOrigin.container;
        test-origin-microvm = testOrigin.microvm.runner;

        # Profile variants (low-latency, 4K ABR, stress test)
        test-origin-low-latency = testOrigin.profiles.low-latency.runOrigin;
        test-origin-4k-abr = testOrigin.profiles."4k-abr".runOrigin;
        test-origin-stress = testOrigin.profiles.stress-test.runOrigin;
      };

      apps.${system} = {
        default = /* existing */;

        # Run test origin locally (process)
        test-origin = {
          type = "app";
          program = "${testOrigin.runOrigin}/bin/test-hls-origin";
        };

        # Run test origin as MicroVM
        test-origin-vm = {
          type = "app";
          program = "${testOrigin.microvm.runner}/bin/microvm-run";
        };

        # Profile variants
        test-origin-low-latency = {
          type = "app";
          program = "${testOrigin.profiles.low-latency.runOrigin}/bin/test-hls-origin";
        };
      };

      # NixOS configurations for MicroVM
      nixosConfigurations = {
        hls-origin-vm = testOrigin.microvm.nixos;
      };

      # Export for use in integration tests
      lib.${system}.testOrigin = testOrigin;
    };
}
```

### Nginx Version in Builds

The test origin uses `pkgs.nginxMainline` (version 1.29.4) by default:

```bash
# Verify Nginx version in container
nix build .#test-origin-container
docker load < result
docker run --rm go-ffmpeg-hls-swarm-test-origin nginx -v
# nginx version: nginx/1.29.4

# Check HTTP/3 support
docker run --rm go-ffmpeg-hls-swarm-test-origin nginx -V 2>&1 | grep http_v3
# --with-http_v3_module
```

---

### Usage Examples

```bash
# ═══════════════════════════════════════════════════════════════════
# Option 1: Local runner (fastest startup, minimal isolation)
# ═══════════════════════════════════════════════════════════════════
nix run .#test-origin

# With custom port
PORT=9000 nix run .#test-origin

# ═══════════════════════════════════════════════════════════════════
# Option 2: OCI Container (Docker/Podman)
# ═══════════════════════════════════════════════════════════════════
# Build container image
nix build .#test-origin-container
docker load < result

# Run container
docker run -p 8080:8080 go-ffmpeg-hls-swarm-test-origin

# Or with Podman
podman run -p 8080:8080 go-ffmpeg-hls-swarm-test-origin

# ═══════════════════════════════════════════════════════════════════
# Option 3: MicroVM (full VM isolation, ~10s startup)
# ═══════════════════════════════════════════════════════════════════
# Run MicroVM directly
nix run .#test-origin-vm

# Build and inspect the MicroVM runner
nix build .#test-origin-microvm
ls -la result/bin/

# Run with specific hypervisor
# (qemu is default, also: cloud-hypervisor, firecracker, kvmtool)
nix build .#nixosConfigurations.hls-origin-vm.config.microvm.runner.firecracker
./result/bin/microvm-run

# ═══════════════════════════════════════════════════════════════════
# Inspect components (nix repl)
# ═══════════════════════════════════════════════════════════════════
nix repl
:lf .
testOrigin.config.video.bitrate        # "2000k"
testOrigin.ffmpeg.ffmpegArgs           # Full argument list
testOrigin.microvm.runners             # All hypervisor runners

# ═══════════════════════════════════════════════════════════════════
# Test the stream (any deployment method)
# ═══════════════════════════════════════════════════════════════════
curl http://localhost:8080/stream.m3u8
ffplay http://localhost:8080/stream.m3u8

# Run load test against it
./go-ffmpeg-hls-swarm -clients 50 http://localhost:8080/stream.m3u8
```

### Comparison: Container vs MicroVM

| Aspect | OCI Container | MicroVM |
|--------|---------------|---------|
| **Isolation** | Namespace/cgroup | Full VM (KVM) |
| **Startup** | ~5 seconds | ~10 seconds |
| **Memory overhead** | ~50 MB | ~100 MB |
| **Kernel** | Host kernel | Guest kernel |
| **Networking** | Host/bridge | virtio-net |
| **Use case** | CI/CD, quick tests | Security-sensitive, prod-like |
| **Host requirements** | Docker/Podman | KVM-enabled Linux |

---

## Testing Strategy

### 1. Unit Test: FFmpeg Command Validity

```bash
# Verify FFmpeg can parse the command (dry run)
ffmpeg -f lavfi -i "testsrc2=size=1280x720:rate=30:duration=5" \
  -c:v libx264 -preset ultrafast -f null - 2>&1 | grep -q "video:.*kB"
```

### 2. Integration Test: Stream Generation

```python
# tests/test_origin.py (conceptual)
def test_hls_stream_generated():
    """Verify HLS playlist and segments are created."""
    # Start origin
    # Wait for stream.m3u8
    # Parse playlist, verify segment references
    # Verify .ts files exist
    # Verify segment count <= hls_list_size
```

### 3. NixOS Integration Test

Update `nix/tests/integration.nix`:

```nix
{ pkgs, self, ... }:

pkgs.testers.nixosTest {
  name = "go-ffmpeg-hls-swarm-with-origin";

  nodes = {
    origin = { config, pkgs, ... }: {
      # Use tmpfs for HLS
      fileSystems."/var/hls" = {
        device = "tmpfs";
        fsType = "tmpfs";
        options = [ "size=256M" ];
      };

      # FFmpeg HLS generator service
      systemd.services.hls-generator = {
        description = "FFmpeg HLS Test Stream Generator";
        after = [ "network.target" ];
        wantedBy = [ "multi-user.target" ];
        serviceConfig = {
          ExecStart = ''
            ${pkgs.ffmpeg-full}/bin/ffmpeg -re \
              -f lavfi -i "testsrc2=size=1280x720:rate=30:duration=0" \
              -f lavfi -i "sine=frequency=1000:sample_rate=48000:duration=0" \
              -c:v libx264 -preset ultrafast -tune zerolatency \
              -g 60 -keyint_min 60 -sc_threshold 0 \
              -b:v 2000k -maxrate 2000k -bufsize 4000k \
              -c:a aac -b:a 128k \
              -f hls -hls_time 2 -hls_list_size 10 \
              -hls_flags delete_segments+omit_endlist \
              -hls_segment_filename "/var/hls/seg%05d.ts" \
              /var/hls/stream.m3u8
          '';
          Restart = "always";
          RestartSec = 2;
        };
      };

      # Nginx HLS server
      services.nginx = {
        enable = true;
        config = ''
          events { worker_connections 1024; }
          http {
            include ${pkgs.nginx}/conf/mime.types;
            sendfile on;
            server {
              listen 80;
              root /var/hls;
              location ~ \.m3u8$ {
                add_header Cache-Control "no-cache";
                add_header Access-Control-Allow-Origin "*";
              }
              location ~ \.ts$ {
                add_header Cache-Control "max-age=86400";
              }
              location /health { return 200 "OK"; }
            }
          }
        '';
      };
    };

    client = { config, pkgs, ... }: {
      environment.systemPackages = [
        self.packages.${pkgs.system}.go-ffmpeg-hls-swarm
        pkgs.ffmpeg-full
        pkgs.curl
      ];
    };
  };

  testScript = ''
    import time

    # Start origin services
    origin.wait_for_unit("hls-generator.service")
    origin.wait_for_unit("nginx.service")
    origin.wait_for_open_port(80)

    # Wait for HLS stream to be ready
    origin.wait_until_succeeds("test -f /var/hls/stream.m3u8", timeout=30)
    origin.wait_until_succeeds("grep -q EXTINF /var/hls/stream.m3u8", timeout=30)

    with subtest("Health endpoint responds"):
        origin.succeed("curl -sf http://localhost/health")

    with subtest("HLS playlist is valid"):
        playlist = origin.succeed("curl -sf http://localhost/stream.m3u8")
        assert "#EXTM3U" in playlist, "Missing EXTM3U header"
        assert "#EXTINF" in playlist, "No segments in playlist"
        assert "#EXT-X-ENDLIST" not in playlist, "Should be live stream"

    with subtest("Segments are downloadable"):
        # Get first segment name from playlist
        origin.succeed("curl -sf http://localhost/stream.m3u8 | grep -m1 '.ts' | xargs -I{} curl -sf -o /dev/null http://localhost/{}")

    # Client tests
    client.wait_for_unit("multi-user.target")

    with subtest("Client can fetch playlist"):
        client.succeed("curl -sf http://origin/stream.m3u8 | head -5")

    with subtest("FFmpeg can consume stream"):
        # Download 5 seconds of stream
        client.succeed("timeout 10 ffmpeg -hide_banner -loglevel error -i http://origin/stream.m3u8 -t 5 -c copy -f null - || test $? -eq 124")

    with subtest("go-ffmpeg-hls-swarm can start"):
        client.succeed("${self.packages.${pkgs.system}.go-ffmpeg-hls-swarm}/bin/go-ffmpeg-hls-swarm --help")

    # Optional: Load test with multiple clients
    # with subtest("Load test with 10 clients"):
    #     client.succeed("go-ffmpeg-hls-swarm -clients 10 -duration 30s http://origin/stream.m3u8")
  '';
}
```

### 4. Manual Testing Commands

```bash
# Start test origin
nix run .#test-origin

# In another terminal:
# Verify playlist
curl http://localhost:8080/stream.m3u8

# Play stream (requires ffplay)
ffplay http://localhost:8080/stream.m3u8

# Run load test
./go-ffmpeg-hls-swarm -clients 10 http://localhost:8080/stream.m3u8
```

---

## Performance Tuning

### Nginx Tuning for High Concurrency

| Tunable | Value | Notes |
|---------|-------|-------|
| `worker_rlimit_nofile` | 65535 | File descriptor limit per worker |
| `worker_connections` | 16384 | Max connections per worker |
| `keepalive_requests` | 10000 | Requests per connection |
| `open_file_cache` | max=10000 | Cache file handles |
| `reset_timedout_connection` | on | Free memory from stale connections |
| `aio threads` | default (32) | Async I/O prevents worker blocking |
| `directio` | 512 | Direct I/O for large files |

### Additional Nginx Modules

> **Full module list**: See [NIX_NGINX_REFERENCE.md](NIX_NGINX_REFERENCE.md#additional-modules)

For advanced use cases, add modules via `services.nginx.additionalModules`:

```nix
services.nginx = {
  additionalModules = with pkgs.nginxModules; [
    # Advanced monitoring (VTS - Virtual Traffic Status)
    vts    # Detailed per-vhost metrics: bytes, requests, latency

    # Compression (not for .ts, but for API responses)
    brotli # Better than gzip

    # Streaming (for testing RTMP ingest)
    rtmp   # RTMP/HLS streaming server

    # Caching control
    cache-purge  # Purge content from cache
  ];
};
```

**VTS (Virtual Traffic Status) for detailed metrics:**

```nix
services.nginx = {
  additionalModules = [ pkgs.nginxModules.vts ];

  appendHttpConfig = ''
    vhost_traffic_status_zone;
  '';

  virtualHosts."hls-origin".locations."/status" = {
    extraConfig = ''
      vhost_traffic_status_display;
      vhost_traffic_status_display_format html;
    '';
  };
};
```

This provides:
- Per-vhost request/byte counts
- Response time histograms
- Upstream health metrics
- JSON export for Prometheus

### Kernel Tuning (NixOS sysctl)

For maximum network performance in the test origin (especially MicroVMs), apply comprehensive sysctl tuning:

```nix
# nix/test-origin/sysctl.nix
# High-performance network tuning for HLS streaming
# Reference: https://www.kernel.org/doc/html/latest/networking/ip-sysctl.html
{ config, pkgs, ... }:

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
    "net.ipv4.tcp_notsent_lowat" = 131072;         # Optimize latency (LWN 560082)
    "net.ipv4.tcp_tw_reuse" = 1;                   # Reuse TIME-WAIT sockets
    "net.ipv4.tcp_timestamps" = 1;                 # Enable timestamps
    "net.ipv4.tcp_ecn" = 1;                        # Explicit Congestion Notification
    "net.ipv4.tcp_window_scaling" = 1;             # Window scaling (RFC 1323)
    "net.ipv4.tcp_sack" = 1;                       # Selective ACK
    "net.ipv4.tcp_fack" = 1;                       # Forward ACK
    "net.ipv4.tcp_fin_timeout" = 30;               # FIN-WAIT-2 timeout (default: 60)

    # ═══════════════════════════════════════════════════════════════════════
    # TCP Fast Start / Low Latency
    # ═══════════════════════════════════════════════════════════════════════
    "net.ipv4.tcp_slow_start_after_idle" = 0;      # No slow start after idle
    "net.ipv4.tcp_fastopen" = 3;                   # TCP Fast Open (client+server)
    "net.ipv4.tcp_no_ssthresh_metrics_save" = 0;   # Save slow start threshold
    "net.ipv4.tcp_reflect_tos" = 1;                # Reflect TOS on reply
    "net.ipv4.tcp_rto_min_us" = 50000;             # Min RTO: 50ms (default: 200ms)

    # ═══════════════════════════════════════════════════════════════════════
    # Port Range and Queueing
    # ═══════════════════════════════════════════════════════════════════════
    "net.ipv4.ip_local_port_range" = "1026 65535"; # More ephemeral ports
    "net.core.default_qdisc" = "cake";             # CAKE qdisc (better than fq_codel)
    "net.ipv4.tcp_congestion_control" = "cubic";   # Congestion control (or "bbr")
  };
}
```

### Sysctl Configuration Rationale

| Setting | Default | Tuned | Why |
|---------|---------|-------|-----|
| `tcp_keepalive_time` | 7200s | 120s | Detect dead clients faster |
| `tcp_rmem/wmem` | 6MB max | 16MB max | Higher throughput for HLS segments |
| `tcp_notsent_lowat` | 4GB | 128KB | Reduce latency for manifests |
| `tcp_tw_reuse` | 0 | 1 | Handle many short connections |
| `tcp_slow_start_after_idle` | 1 | 0 | Maintain throughput on idle connections |
| `tcp_fastopen` | 0 | 3 | 0-RTT for repeat clients |
| `tcp_rto_min_us` | 200ms | 50ms | Faster retransmits on fast networks |
| `default_qdisc` | pfifo_fast | cake | Better fairness and latency |

### BBR vs CUBIC

For HLS streaming:
- **CUBIC** (default): Better for stable, low-loss networks (recommended for test origin)
- **BBR**: Better for high-latency or lossy networks (consider for production CDN)

```nix
# To use BBR instead:
"net.ipv4.tcp_congestion_control" = "bbr";
# Note: BBR requires kernel 4.9+ and CONFIG_TCP_CONG_BBR
```

### Expected Performance

| Metric | Expected | Notes |
|--------|----------|-------|
| Concurrent connections | 10,000+ | Nginx limit |
| Requests/second | 50,000+ | Depends on segment size |
| Latency (p99) | <10ms | With tmpfs |
| Memory usage | ~300MB | FFmpeg + Nginx + tmpfs |

---

## Variants / Bitrate Ladder (Future)

For more realistic testing, generate multiple bitrate variants:

```bash
# master.m3u8 with multiple renditions
ffmpeg -re \
  -f lavfi -i "testsrc2=size=1920x1080:rate=30:duration=0" \
  -f lavfi -i "sine=frequency=1000:sample_rate=48000:duration=0" \
  -filter_complex "[0:v]split=3[v1][v2][v3]; \
    [v1]scale=1920:1080[v1out]; \
    [v2]scale=1280:720[v2out]; \
    [v3]scale=854:480[v3out]" \
  -map "[v1out]" -map "[v2out]" -map "[v3out]" -map 0:a \
  -c:v libx264 -preset ultrafast \
  -c:a aac -b:a 128k \
  -var_stream_map "v:0,a:0 v:1,a:0 v:2,a:0" \
  -master_pl_name master.m3u8 \
  -f hls -hls_time 2 -hls_list_size 10 \
  -hls_flags delete_segments+omit_endlist \
  -hls_segment_filename "/var/hls/v%v/seg%05d.ts" \
  /var/hls/v%v/stream.m3u8
```

---

## Summary

| Component | Technology | Purpose |
|-----------|------------|---------|
| Stream Generator | FFmpeg + lavfi | Synthetic live HLS |
| Web Server | Nginx | High-performance serving |
| Storage | tmpfs (optional) | Maximum I/O throughput |
| Packaging | Nix | Reproducible builds |
| Testing | NixOS VM tests | Full integration verification |

This design provides a self-contained, reproducible test environment that can be used for:
- Local development testing
- CI/CD integration tests
- Performance benchmarking
- Debugging client behavior

---

## Related Documentation

- [DESIGN.md](DESIGN.md) — Main architecture
- [FFMPEG_HLS_REFERENCE.md](FFMPEG_HLS_REFERENCE.md) — FFmpeg HLS demuxer details
- [NIX_FLAKE_DESIGN.md](NIX_FLAKE_DESIGN.md) — Nix flake structure
- [NIX_NGINX_REFERENCE.md](NIX_NGINX_REFERENCE.md) — Complete Nginx configuration reference (HTTP/3, modules, NixOS options)
- [MEMORY.md](MEMORY.md) — Memory efficiency with multiple FFmpeg processes
