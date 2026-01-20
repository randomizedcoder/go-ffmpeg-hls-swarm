# Nix Nginx Reference

A comprehensive guide to Nginx configuration in NixOS and nixpkgs for the `go-ffmpeg-hls-swarm` project.

**Purpose**: Maximize usage of Nix's Nginx module capabilities, including HTTP/3 support, for high-performance HLS delivery.

**Source**: Based on analysis of the local nixpkgs repository at `/home/das/Downloads/nixpkgs`.

---

## Table of Contents

1. [Package Variants](#package-variants)
2. [Built-in Modules](#built-in-modules)
3. [Additional Modules](#additional-modules)
4. [NixOS Service Configuration](#nixos-service-configuration)
5. [HTTP/3 and QUIC Support](#http3-and-quic-support)
6. [Performance Settings](#performance-settings)
7. [Prometheus Monitoring](#prometheus-monitoring)
8. [HLS-Specific Configuration](#hls-specific-configuration)
9. [File Reference](#file-reference)

---

## Package Variants

**Source**: `pkgs/servers/http/nginx/stable.nix`, `pkgs/servers/http/nginx/mainline.nix`

| Package | Version | Recommendation |
|---------|---------|----------------|
| `pkgs.nginxStable` | 1.28.0 | Default, conservative |
| `pkgs.nginxMainline` | **1.29.4** | **Recommended** - latest features |
| `pkgs.angie` | - | Nginx fork with additional features |
| `pkgs.openresty` | - | Lua scripting support |
| `pkgs.tengine` | - | Alibaba's fork |

**For HLS load testing**: Use `pkgs.nginxMainline` for:
- Latest HTTP/3 implementation
- Best performance optimizations
- Active security patches

```nix
services.nginx.package = pkgs.nginxMainline;
```

---

## Built-in Modules

**Source**: `pkgs/servers/http/nginx/generic.nix` (lines 116-177)

The Nix Nginx build includes these modules by default:

| Module | Configure Flag | Purpose |
|--------|---------------|---------|
| HTTP/2 | `--with-http_v2_module` | HTTP/2 protocol |
| **HTTP/3** | `--with-http_v3_module` | **QUIC/HTTP/3 protocol** |
| SSL | `--with-http_ssl_module` | TLS encryption |
| Real IP | `--with-http_realip_module` | Client IP from proxies |
| Sub | `--with-http_sub_module` | Response body substitutions |
| DAV | `--with-http_dav_module` | WebDAV |
| FLV | `--with-http_flv_module` | FLV streaming |
| MP4 | `--with-http_mp4_module` | MP4 streaming |
| Gunzip | `--with-http_gunzip_module` | Decompress gzipped responses |
| Gzip Static | `--with-http_gzip_static_module` | Pre-compressed files |
| Auth Request | `--with-http_auth_request_module` | Subrequest authentication |
| Stub Status | `--with-http_stub_status_module` | Basic status info |
| Threads | `--with-threads` | Thread pool support |
| File AIO | `--with-file-aio` | Async file I/O (Linux/FreeBSD) |
| PCRE JIT | `--with-pcre-jit` | Fast regex matching |

### Build Options

```nix
# In a flake or overlay:
pkgs.nginxMainline.override {
  withStream = true;      # TCP/UDP proxy (default: true)
  withMail = false;       # Mail proxy (default: false)
  withDebug = false;      # Debug logging (default: false)
  withKTLS = true;        # Kernel TLS (default: true)
  withSlice = false;      # Byte-range slicing (default: false)
  withImageFilter = false; # Image processing (default: false)
}
```

---

## Additional Modules

**Source**: `pkgs/servers/http/nginx/modules.nix`

Over 50 third-party modules are packaged. Key ones for HLS:

### Streaming Modules

```nix
services.nginx.additionalModules = with pkgs.nginxModules; [
  # RTMP streaming (not for HLS consumption, but useful for testing)
  rtmp      # Media streaming server

  # MPEG-TS (for testing)
  mpeg-ts   # MPEG-TS live module

  # Live streaming
  live      # HTTP live module
];
```

### Performance Modules

```nix
services.nginx.additionalModules = with pkgs.nginxModules; [
  # Compression
  brotli    # Brotli compression (better than gzip)
  zstd      # Zstandard compression

  # Caching
  cache-purge  # Purge content from caches

  # Monitoring
  vts       # Virtual host traffic status
];
```

### Module Usage Example

```nix
{ pkgs, ... }:
{
  services.nginx = {
    package = pkgs.nginxMainline;
    additionalModules = [ pkgs.nginxModules.brotli ];

    recommendedBrotliSettings = true;  # Auto-configures brotli
  };
}
```

---

## NixOS Service Configuration

**Source**: `nixos/modules/services/web-servers/nginx/default.nix`

### Recommended Settings (Boolean Toggles)

```nix
services.nginx = {
  enable = true;

  # Performance
  recommendedOptimisation = true;     # sendfile, tcp_nopush, tcp_nodelay

  # Compression
  recommendedGzipSettings = true;     # gzip on, gzip_static, etc.
  recommendedBrotliSettings = true;   # brotli on (adds module automatically)

  # TLS
  recommendedTlsSettings = true;      # Modern TLS config

  # Proxy
  recommendedProxySettings = true;    # Proxy headers
};
```

### What `recommendedOptimisation` Does

**Source**: Lines 195-201 of `default.nix`

```nginx
sendfile on;
tcp_nopush on;
tcp_nodelay on;
keepalive_timeout 65;
```

### What `recommendedTlsSettings` Does

**Source**: Lines 207-218 of `default.nix`

```nginx
ssl_conf_command Groups "X25519MLKEM768:X25519:P-256:P-384";
ssl_session_timeout 1d;
ssl_session_cache shared:SSL:10m;
ssl_session_tickets off;
ssl_prefer_server_ciphers off;
```

### Global Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `package` | package | `pkgs.nginxStable` | Nginx package to use |
| `additionalModules` | list | `[]` | Extra modules to compile in |
| `user` | string | `"nginx"` | User to run as |
| `group` | string | `"nginx"` | Group to run as |
| `clientMaxBodySize` | string | `"10m"` | Max request body size |
| `serverTokens` | bool | `false` | Show version in headers |
| `logError` | string | `"stderr"` | Error log destination |
| `sslProtocols` | string | `"TLSv1.2 TLSv1.3"` | Allowed TLS versions |
| `statusPage` | bool | `false` | Enable `/nginx_status` on localhost |
| `enableReload` | bool | `false` | Reload instead of restart on config change |
| `enableQuicBPF` | bool | `false` | eBPF for QUIC connection migration |

---

## HTTP/3 and QUIC Support

**Source**: `nixos/tests/nginx-http3.nix`, `vhost-options.nix`

### Enabling HTTP/3

```nix
services.nginx.virtualHosts."hls.example.com" = {
  # Enable QUIC transport (required for HTTP/3)
  quic = true;

  # Enable HTTP/3 protocol
  http3 = true;

  # HTTP/2 (recommended alongside HTTP/3)
  http2 = true;

  # Required for QUIC (call once per port)
  reuseport = true;

  # SSL is required for QUIC/HTTP/3
  onlySSL = true;
  sslCertificate = "/path/to/cert.pem";
  sslCertificateKey = "/path/to/key.pem";

  # Advertise HTTP/3 availability in responses
  extraConfig = ''
    add_header Alt-Svc 'h3=":443"; ma=86400';
  '';
};
```

### QUIC BPF for Connection Migration

For advanced QUIC features (connection migration):

```nix
services.nginx.enableQuicBPF = true;
```

**Note**: Requires Linux 5.7+ and adds `CAP_SYS_ADMIN` + `CAP_NET_ADMIN` capabilities.

### Firewall Configuration

HTTP/3 uses UDP, so you need:

```nix
networking.firewall = {
  allowedTCPPorts = [ 443 ];  # HTTPS (HTTP/1.1, HTTP/2)
  allowedUDPPorts = [ 443 ];  # QUIC (HTTP/3)
};
```

---

## Performance Settings

### Events Block

```nix
services.nginx.eventsConfig = ''
  worker_connections 16384;
  use epoll;
  multi_accept on;
'';
```

### HTTP Block Additions

```nix
services.nginx.appendHttpConfig = ''
  # Thread pool for async I/O
  aio threads;
  directio 512;

  # File descriptor caching
  open_file_cache max=10000 inactive=30s;
  open_file_cache_valid 10s;
  open_file_cache_errors on;

  # Connection management
  reset_timedout_connection on;
  keepalive_requests 10000;

  # Buffer tuning
  sendfile_max_chunk 512k;
'';
```

### Per-Worker Settings

```nix
services.nginx.prependConfig = ''
  worker_processes auto;
  worker_rlimit_nofile 65535;
  thread_pool default threads=32 max_queue=65536;
'';
```

---

## Prometheus Monitoring

**Source**: `pkgs/servers/monitoring/prometheus/nginx-exporter.nix`

### Enable Stub Status

```nix
services.nginx = {
  statusPage = true;  # Enables /nginx_status on localhost

  # Or manually configure:
  virtualHosts."localhost" = {
    locations."/nginx_status" = {
      extraConfig = ''
        stub_status on;
        access_log off;
        allow 127.0.0.1;
        deny all;
      '';
    };
  };
};
```

### Prometheus Nginx Exporter

```nix
services.prometheus.exporters.nginx = {
  enable = true;
  port = 9113;
  scrapeUri = "http://localhost/nginx_status";
};
```

### Virtual Host Traffic Status (VTS) Module

For more detailed metrics:

```nix
services.nginx = {
  additionalModules = [ pkgs.nginxModules.vts ];

  appendHttpConfig = ''
    vhost_traffic_status_zone;
  '';

  virtualHosts."localhost".locations."/status" = {
    extraConfig = ''
      vhost_traffic_status_display;
      vhost_traffic_status_display_format html;
    '';
  };
};
```

---

## HLS-Specific Configuration

### Complete HLS Origin Configuration

```nix
{ config, pkgs, lib, ... }:

let
  hlsDir = "/var/hls";
  hlsPort = 8080;
in
{
  services.nginx = {
    enable = true;
    package = pkgs.nginxMainline;  # Latest features

    recommendedOptimisation = true;
    recommendedTlsSettings = true;

    # Global performance settings
    eventsConfig = ''
      worker_connections 16384;
      use epoll;
      multi_accept on;
    '';

    appendHttpConfig = ''
      # Async I/O
      aio threads;
      directio 512;

      # File caching (excellent for HLS segments)
      open_file_cache max=10000 inactive=30s;
      open_file_cache_valid 10s;
      open_file_cache_errors on;

      # Performance
      reset_timedout_connection on;
      sendfile_max_chunk 512k;
    '';

    virtualHosts."hls-origin" = {
      listen = [{ addr = "0.0.0.0"; port = hlsPort; }];
      root = hlsDir;

      locations = {
        # Manifest files - fresh delivery
        "~ \\.m3u8$" = {
          extraConfig = ''
            tcp_nodelay on;
            add_header Cache-Control "public, max-age=1, stale-while-revalidate=2, no-transform";
            add_header Access-Control-Allow-Origin "*";
            types { application/vnd.apple.mpegurl m3u8; }
          '';
        };

        # Segment files - aggressive caching
        "~ \\.ts$" = {
          extraConfig = ''
            sendfile on;
            tcp_nopush on;
            aio threads;
            add_header Cache-Control "public, max-age=60, immutable, no-transform";
            add_header Access-Control-Allow-Origin "*";
            add_header Accept-Ranges bytes;
            types { video/mp2t ts; }
          '';
        };

        "/health" = {
          return = "200 'OK\\n'";
          extraConfig = ''
            add_header Content-Type text/plain;
            add_header Cache-Control "no-store";
          '';
        };

        "/nginx_status" = {
          extraConfig = ''
            stub_status on;
            access_log off;
          '';
        };
      };
    };
  };

  # Firewall
  networking.firewall.allowedTCPPorts = [ hlsPort ];

  # tmpfs for HLS segments
  fileSystems.${hlsDir} = {
    device = "tmpfs";
    fsType = "tmpfs";
    options = [ "size=256M" "mode=1777" ];
  };
}
```

### MIME Types for HLS

The default Nginx MIME types are incomplete. NixOS uses `mailcap` for better coverage:

```nix
services.nginx.defaultMimeTypes = "${pkgs.mailcap}/etc/nginx/mime.types";
```

For HLS, ensure these types are defined:

| Extension | MIME Type |
|-----------|-----------|
| `.m3u8` | `application/vnd.apple.mpegurl` |
| `.ts` | `video/mp2t` |

---

## File Reference

### Package Files

| Path | Purpose |
|------|---------|
| `pkgs/servers/http/nginx/generic.nix` | Main Nginx build derivation |
| `pkgs/servers/http/nginx/stable.nix` | Stable version definition (1.28.0) |
| `pkgs/servers/http/nginx/mainline.nix` | Mainline version definition (1.29.4) |
| `pkgs/servers/http/nginx/modules.nix` | 50+ third-party module definitions |

### NixOS Module Files

| Path | Purpose |
|------|---------|
| `nixos/modules/services/web-servers/nginx/default.nix` | Main service module |
| `nixos/modules/services/web-servers/nginx/vhost-options.nix` | Virtual host options |
| `nixos/modules/services/web-servers/nginx/location-options.nix` | Location block options |

### Test Files (Example Configurations)

| Path | Purpose |
|------|---------|
| `nixos/tests/nginx-http3.nix` | HTTP/3 configuration example |
| `nixos/tests/nginx-etag.nix` | ETag/caching test |
| `nixos/tests/nginx-status-page.nix` | Status page test |
| `nixos/tests/nginx.nix` | General Nginx test |

### Monitoring

| Path | Purpose |
|------|---------|
| `pkgs/servers/monitoring/prometheus/nginx-exporter.nix` | Prometheus exporter |
| `nixos/modules/services/monitoring/prometheus/exporters/nginx.nix` | NixOS exporter module |

---

## Quick Reference Syntax

### Virtual Host Options

**Source**: `vhost-options.nix`

```nix
services.nginx.virtualHosts."example.com" = {
  # SSL
  forceSSL = true;           # Redirect HTTP to HTTPS
  onlySSL = true;            # HTTPS only
  addSSL = true;             # HTTP + HTTPS
  enableACME = true;         # Let's Encrypt
  useACMEHost = "shared";    # Use another cert
  sslCertificate = "/path";
  sslCertificateKey = "/path";

  # HTTP/2 and HTTP/3
  http2 = true;              # Enable HTTP/2
  http3 = true;              # Enable HTTP/3
  quic = true;               # Enable QUIC transport
  reuseport = true;          # Required for QUIC
  kTLS = true;               # Kernel TLS offload

  # General
  root = "/var/www";
  default = true;            # Default server
  serverAliases = [ "www.example.com" ];

  # Redirect
  globalRedirect = "other.com";
  redirectCode = 301;

  # Auth
  basicAuth = { user = "pass"; };
  basicAuthFile = "/path/to/htpasswd";

  # Custom config
  extraConfig = "";

  # Locations
  locations = { /* ... */ };
};
```

### Location Options

**Source**: `location-options.nix`

```nix
locations."/" = {
  root = "/var/www";
  alias = "/var/alias";
  index = "index.html";
  tryFiles = "$uri =404";
  return = "200 'OK'";

  # Proxy
  proxyPass = "http://localhost:3000";
  proxyWebsockets = true;
  recommendedProxySettings = true;

  # FastCGI
  fastcgiParams = { SCRIPT_FILENAME = "$document_root$fastcgi_script_name"; };

  # uWSGI
  uwsgiPass = "unix:/run/uwsgi.sock";
  recommendedUwsgiSettings = true;

  # Auth
  basicAuth = { user = "pass"; };

  # Custom
  extraConfig = "";
  priority = 1000;  # Order in config
};
```

---

## Usage in go-ffmpeg-hls-swarm

For the test origin server, the recommended configuration:

```nix
# nix/test-origin/nginx.nix
services.nginx = {
  enable = true;
  package = pkgs.nginxMainline;  # Version 1.29.4

  recommendedOptimisation = true;

  # Use NixOS module for structured config where possible
  virtualHosts."hls-origin" = {
    listen = [{ addr = "0.0.0.0"; port = cfg.port; }];
    root = cfg.hlsDir;

    locations = {
      "~ \\.m3u8$".extraConfig = /* manifest config */;
      "~ \\.ts$".extraConfig = /* segment config */;
    };
  };
};
```

---

## See Also

- [Official Nginx Documentation](https://nginx.org/en/docs/)
- [NixOS Nginx Manual](https://nixos.org/manual/nixos/stable/#module-services-nginx)
- [Mozilla SSL Configuration Generator](https://ssl-config.mozilla.org/#server=nginx)
- [TEST_ORIGIN.md](./TEST_ORIGIN.md) - Test origin server design
