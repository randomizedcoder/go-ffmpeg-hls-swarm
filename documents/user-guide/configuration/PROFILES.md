# Profiles Reference

> **Type**: User Documentation
> **Source**: Verified against `nix/apps.nix` and `nix/test-origin/config/profile-list.nix`

Profiles are pre-configured settings for common use cases.

---

## Test Origin Profiles

These profiles configure the HLS test origin server.

| Profile | Description | Use Case |
|---------|-------------|----------|
| `default` | Standard 2s segments, 720p | General testing |
| `low-latency` | 1s segments, optimized for speed | Low-latency streaming tests |
| `4k-abr` | Multi-bitrate 4K streaming | ABR testing, bandwidth tests |
| `stress` | Maximum throughput configuration | Finding origin limits |
| `logged` | With buffered segment logging (512k buffer) | Debugging, analysis |
| `debug` | Full logging with gzip compression | Detailed troubleshooting |

### Running Profiles

**Via Makefile:**

```bash
make test-origin                 # default profile
make test-origin-low-latency     # low-latency profile
make test-origin-4k-abr          # 4K ABR profile
make test-origin-stress          # stress profile
make test-origin-logged          # logged profile
make test-origin-debug           # debug profile
```

**Via Nix:**

```bash
nix run .#test-origin             # default
nix run .#test-origin-low-latency
nix run .#test-origin-4k-abr
nix run .#test-origin-stress
nix run .#test-origin-logged
nix run .#test-origin-debug
```

**Via unified CLI:**

```bash
nix run .#up default runner
nix run .#up low-latency runner
nix run .#up 4k-abr runner
nix run .#up stress runner
```

---

## Swarm Client Profiles

These profiles configure the load testing client.

| Profile | Clients | Ramp Rate | Use Case |
|---------|---------|-----------|----------|
| `default` | 50 | 5/sec | Quick validation |
| `gentle` | 20 | 1/sec | Careful testing |
| `burst` | 100 | 50/sec | Thundering herd simulation |
| `stress` | 200 | 20/sec | Heavy load testing |
| `extreme` | 500 | 50/sec | Finding breaking points |

### Running Swarm Profiles

**Via Makefile:**

```bash
make swarm-client             # default (50 clients)
make swarm-client-gentle      # gentle (20 clients)
make swarm-client-burst       # burst (100 clients, fast ramp)
make swarm-client-stress      # stress (200 clients)
make swarm-client-extreme     # extreme (500 clients)
```

**Via Nix:**

```bash
nix run .#swarm-client -- http://localhost:17080/stream.m3u8
nix run .#swarm-client-gentle -- http://localhost:17080/stream.m3u8
nix run .#swarm-client-burst -- http://localhost:17080/stream.m3u8
nix run .#swarm-client-stress -- http://localhost:17080/stream.m3u8
nix run .#swarm-client-extreme -- http://localhost:17080/stream.m3u8
```

---

## Deployment Types

Each profile can be deployed in different ways:

| Type | Description | Requirements |
|------|-------------|--------------|
| `runner` | Local shell script | All platforms |
| `container` | OCI container (Docker/Podman) | Linux to run |
| `vm` | MicroVM with KVM | Linux + KVM |

### Using the Unified CLI

The `nix run .#up` command provides interactive profile and deployment selection:

```bash
# Interactive mode (if TTY available)
nix run .#up

# Direct selection
nix run .#up default runner
nix run .#up low-latency container
nix run .#up stress vm
```

**Help:**

```bash
nix run .#up -- --help
```

---

## MicroVM Profiles

For MicroVM deployment, TAP networking profiles provide higher performance:

| Profile | Network | Performance |
|---------|---------|-------------|
| `test-origin-vm` | User-mode | Standard |
| `test-origin-vm-tap` | TAP/bridge | ~10 Gbps |
| `test-origin-vm-tap-logged` | TAP/bridge + logging | ~10 Gbps |

**Running MicroVMs:**

```bash
# Standard user-mode networking
make microvm-origin

# TAP networking (requires network-setup first)
make network-setup
make microvm-origin-tap

# With logging
make microvm-origin-logged
make microvm-origin-tap-logged
```

---

## Profile Selection Guide

| Scenario | Origin Profile | Client Profile |
|----------|----------------|----------------|
| Quick validation | `default` | `default` (50 clients) |
| Low-latency testing | `low-latency` | `default` |
| Bandwidth testing | `4k-abr` | `stress` (200 clients) |
| Find origin limits | `stress` | `extreme` (500 clients) |
| Debug issues | `debug` | `gentle` (20 clients) |
| Production simulation | `default` | `stress` (200 clients) |

---

## Custom Profiles

For custom configurations, use CLI flags directly:

```bash
# Custom client count and ramp rate
./bin/go-ffmpeg-hls-swarm -clients 75 -ramp-rate 15 -duration 5m \
  http://localhost:17080/stream.m3u8
```

Or create custom Nix configurations in `nix/swarm-client/config/`.
