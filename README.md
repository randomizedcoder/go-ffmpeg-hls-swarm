# go-ffmpeg-hls-swarm

<p align="center">
  <img src="go-ffmpeg-hls-swarm.png" alt="go-ffmpeg-hls-swarm logo" width="200">
</p>

<p align="center">
  <strong>Find where your streaming infrastructure breaks — before your viewers do.</strong>
</p>

<p align="center">
  <kbd>🚧 <b>STATUS: IMPLEMENTATION IN PROGRESS</b></kbd>
</p>

<p align="center">
  <em>✅ <b>Test Origin Server</b> — Fully implemented (runner, container, MicroVM)<br/>
  ✅ <b>Nix Infrastructure</b> — Complete with multiple profiles<br/>
  🚧 <b>Go Swarm Client</b> — Core packages written, CLI being integrated</em>
  <br/><br/>
  ⭐ <b>Star</b> to follow progress &nbsp;•&nbsp; 👁️ <b>Watch</b> for release &nbsp;•&nbsp; 💬 <b>Open an issue</b> to help shape it
</p>

---

A Go-based load testing tool that orchestrates a swarm of FFmpeg processes to stress-test HLS (HTTP Live Streaming) infrastructure. HLS is the dominant protocol for live and on-demand video streaming, powering services like Twitch, YouTube Live, and Apple's ecosystem.

---

## Why This Tool?

Most load testing tools (k6, Locust, Gatling) generate HTTP requests but **don't understand HLS**. They can't:

- Parse master playlists and follow variant playlist URLs
- Track segment sequencing in live streams
- Handle playlist refresh timing correctly
- Simulate realistic client reconnection behavior

**FFmpeg's HLS demuxer handles all of this natively**, making it ideal for realistic load testing. This tool orchestrates many FFmpeg processes to simulate concurrent viewers hitting your CDN or origin server.

### Comparison with Alternatives

| Feature | go-ffmpeg-hls-swarm | k6 / Locust | curl loops |
|---------|----------|-------------|------------|
| **HLS Protocol Understanding** | ✅ Native (via FFmpeg) | ❌ HTTP only | ❌ HTTP only |
| **Follows Variant Playlists** | ✅ Automatic | ❌ Manual scripting | ❌ Manual |
| **Handles Live Playlist Refresh** | ✅ Yes | ❌ Must implement | ❌ No |
| **Multi-variant Testing** | ✅ All/highest/lowest | ❌ Manual | ❌ No |
| **Reconnection on Failure** | ✅ Built-in | ⚠️ Must implement | ❌ No |
| **Prometheus Metrics** | ✅ Yes | ✅ Yes | ❌ No |
| **Setup Complexity** | Single binary + FFmpeg | Python/JS ecosystem | Shell scripts |

### Use Cases

| Scenario | How This Helps |
|----------|----------------|
| CDN capacity planning | Find the breaking point before a major event |
| Origin server stress testing | Bypass CDN cache to test origin directly |
| Edge node validation | Test specific servers by IP |
| Failover testing | See how infrastructure handles mass reconnection |

---

## What It Does

- 🎬 **Spawns 50–200+ concurrent HLS clients** using FFmpeg subprocesses
- 📊 **No video decoding** — exercises playlist fetching and segment downloads only
- 🎚️ **Variant selection** — test with all bitrates, highest only, or lowest only
- 🌐 **DNS override** — test specific servers by IP (bypass CDN routing)
- 🚫 **Cache bypass** — no-cache headers to stress origin servers directly
- 🚀 **Controlled ramp-up** — avoid thundering herd with configurable start rates
- 🔄 **Auto-restart with backoff** — handles transient failures gracefully
- 📈 **Prometheus metrics** — monitor active clients, restarts, and failure rates
- 🛑 **Graceful shutdown** — clean signal propagation to all child processes

---

## Try It Now (No Installation Needed)

**Can't wait for go-ffmpeg-hls-swarm?** Try the core concept immediately with just FFmpeg:

### Single Client Test

```bash
# This single FFmpeg command simulates one HLS viewer
ffmpeg -hide_banner -loglevel info \
  -reconnect 1 -reconnect_streamed 1 \
  -i "https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8" \
  -map 0 -c copy -f null -
```

Watch FFmpeg fetch the master playlist, select variants, and download segments. Press `Ctrl+C` to stop.

### Multi-Client Preview (Shell Script)

Want to see what go-ffmpeg-hls-swarm will automate? Run this shell script to spawn 5 concurrent FFmpeg clients:

```bash
#!/bin/bash
# preview-swarm.sh — Preview what go-ffmpeg-hls-swarm will do
URL="https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8"
CLIENTS=5

trap 'kill $(jobs -p) 2>/dev/null; echo "Stopped $CLIENTS clients"' EXIT

echo "Starting $CLIENTS FFmpeg clients..."
for i in $(seq 1 $CLIENTS); do
  ffmpeg -hide_banner -loglevel warning \
    -reconnect 1 -reconnect_streamed 1 \
    -i "$URL" -map 0 -c copy -f null - &
  echo "  Started client $i (PID: $!)"
  sleep 0.2  # Slight ramp-up
done

echo -e "\n$CLIENTS clients running. Press Ctrl+C to stop."
echo "Monitor with: ps aux | grep ffmpeg"
wait
```

Save as `preview-swarm.sh`, run with `bash preview-swarm.sh`, and you'll see exactly what this tool automates — minus the metrics, supervision, and graceful restart handling.

> ⚠️ **Be respectful of public test streams** — keep client count ≤10. For serious testing, use your own infrastructure.

---

## Quick Start

### Option 1: Test Origin Server (Fully Implemented ✅)

The test origin server is fully implemented and ready to use. It generates HLS streams locally using FFmpeg and serves them via Nginx:

```bash
# Using Nix (recommended)
nix run .#test-origin

# Or with Makefile
make test-origin

# Stream available at: http://localhost:8080/stream.m3u8
```

**Available profiles:**
```bash
make test-origin              # Default: 720p, 2s segments
make test-origin-low-latency  # 1s segments, optimized for speed
make test-origin-4k-abr       # Multi-bitrate 4K streaming
make test-origin-stress       # Maximum throughput
```

**MicroVM mode** (full VM isolation, requires KVM):
```bash
make microvm-check-kvm        # Verify KVM support
make microvm-origin           # Run as lightweight VM
```

**Container mode** (requires Podman/Docker):
```bash
nix build .#test-origin-container
podman load < result
podman run -d -p 8080:8080 go-ffmpeg-hls-swarm-test-origin:latest
```

### Option 2: Swarm Client (In Development 🚧)

The Go-based swarm client is being implemented. For now, use the preview script:

```bash
# Preview swarm behavior with FFmpeg directly
bash preview-swarm.sh

# Or when the CLI is ready:
./go-ffmpeg-hls-swarm -clients 50 http://localhost:8080/stream.m3u8
```

For the complete tutorial, see **[Quick Start Guide](docs/QUICKSTART.md)**.

---

## Usage

```bash
go-ffmpeg-hls-swarm [flags] <HLS_URL>

Orchestration Flags:
  -clients int        Number of concurrent clients (default 10)
  -ramp-rate int      Clients to start per second (default 5)
  -duration duration  Run duration, 0 = forever (default 0)

Variant Selection:
  -variant string     Bitrate selection: "all", "highest", "lowest", "first" (default "all")

Network / Testing:
  -resolve string     Connect to this IP (bypasses DNS, requires --dangerous)
  -no-cache           Add no-cache headers (bypass CDN cache)
  -header string      Add custom HTTP header (can repeat)

Safety & Diagnostics:
  --dangerous         Required for -resolve (disables TLS verification)
  --print-cmd         Print the FFmpeg command that would be run, then exit
  --check             Validate config and run 1 client for 10 seconds, then exit

Observability:
  -metrics string     Prometheus metrics address (default "0.0.0.0:9090")
  -v                  Verbose logging

FFmpeg:
  -ffmpeg string      Path to FFmpeg binary (default "ffmpeg")
```

> **Flag convention**: Single-dash flags (`-clients`, `-resolve`) are normal options. Double-dash flags (`--dangerous`, `--check`, `--print-cmd`) are safety gates or diagnostic modes that change the tool's behavior significantly.

See [CONFIGURATION.md](docs/CONFIGURATION.md) for complete flag reference and examples.

---

## Variant Selection

| Mode | Description | Best For |
|------|-------------|----------|
| `all` | Download ALL quality levels simultaneously | Maximum CDN stress |
| `highest` | Highest bitrate only (via ffprobe) | Simulating premium viewers |
| `lowest` | Lowest bitrate only (via ffprobe) | Simulating mobile users |
| `first` | First variant in playlist (no probe) | Fast startup, unpredictable quality |

> ⚠️ **Note on `-variant all`**: This downloads every quality level simultaneously, which is NOT how real viewers behave. With 4 variants and 50 clients, you generate **200 concurrent streams** (4× bandwidth). Use for maximum stress testing; use `highest` or `lowest` for more realistic simulation.

> ℹ️ **Note on `-variant first`**: "First" means first listed in the master playlist. Playlist ordering varies by encoder—it might be highest, lowest, or something else. Use this mode for quick tests when you don't care about specific quality.

---

## Examples

```bash
# Quick smoke test with a public stream
./go-ffmpeg-hls-swarm -clients 5 https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8

# Simulate premium viewers: highest bitrate only
./go-ffmpeg-hls-swarm -clients 100 -variant highest https://cdn.example.com/live/master.m3u8

# Bypass CDN cache (stress origin directly)
./go-ffmpeg-hls-swarm -clients 50 -no-cache https://cdn.example.com/live/master.m3u8

# Test specific server by IP (⚠️ disables TLS verification!)
./go-ffmpeg-hls-swarm -clients 50 -resolve 192.168.1.100 --dangerous \
  https://cdn.example.com/live/master.m3u8

# Full stress test: specific origin + cache bypass + all variants
./go-ffmpeg-hls-swarm -clients 100 -variant all \
  -resolve 10.0.0.50 --dangerous -no-cache \
  https://cdn.example.com/live/master.m3u8

# 30-minute timed load test
./go-ffmpeg-hls-swarm -clients 200 -ramp-rate 5 -duration 30m \
  https://cdn.example.com/live/master.m3u8
```

---

## What This Tool Is (and Isn't)

> 💡 **This is a stress testing tool, not a viewer simulator.**

| ✅ Great For | ❌ Not Designed For |
|-------------|---------------------|
| Finding CDN/origin breaking points | Simulating realistic viewer behavior |
| Validating infrastructure before events | Quality of Experience (QoE) testing |
| Testing specific servers by IP | ABR algorithm testing |
| Bypassing cache to stress origins | Network impairment simulation |

### Key Limitations

| Limitation | Why It Matters | Workaround |
|------------|----------------|------------|
| **Downloads at full speed** | FFmpeg fetches segments ASAP, not at playback rate | This maximizes stress — use external rate limiting if needed |
| **Linux recommended** | High concurrency needs OS tuning (FDs, processes) | See [OS Tuning](#os-tuning-for-high-concurrency) |
| **Single stream URL** | All clients target the same URL per run | Run multiple instances for multiple streams |
| **No ABR simulation** | Clients don't switch bitrates dynamically | Use `-variant` flag to select quality level |

See [OPERATIONS.md](docs/OPERATIONS.md) for detailed discussion of limitations and failure modes.

---

## Requirements

- **Go 1.25+**
- **FFmpeg** with HLS demuxer support (most builds include this)
- **Linux** recommended (for high process/FD limits)

### OS Tuning for High Concurrency

```bash
# Increase file descriptor limit (required for 100+ clients)
ulimit -n 8192

# Or permanently via /etc/security/limits.conf
# your-user soft nofile 8192
# your-user hard nofile 16384
```

See [OPERATIONS.md](docs/OPERATIONS.md) for complete tuning guide.

---

## How It Works

```
┌─────────────────────────────────────────────────────────────────┐
│                         Orchestrator                            │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────┐  │
│  │ CLI / Config │  │ Ramp Scheduler│  │ Metrics Server       │  │
│  │   Parser     │  │              │  │ (Prometheus /metrics)│  │
│  └──────────────┘  └──────────────┘  └──────────────────────┘  │
│                           │                                     │
│                           ▼                                     │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │                   Client Manager                          │  │
│  │  ┌─────────┐ ┌─────────┐ ┌─────────┐       ┌─────────┐   │  │
│  │  │Client 0 │ │Client 1 │ │Client 2 │  ...  │Client N │   │  │
│  │  │Supervisor│ │Supervisor│ │Supervisor│     │Supervisor│   │  │
│  │  └────┬────┘ └────┬────┘ └────┬────┘       └────┬────┘   │  │
│  └───────┼───────────┼───────────┼─────────────────┼────────┘  │
└──────────┼───────────┼───────────┼─────────────────┼───────────┘
           │           │           │                 │
           ▼           ▼           ▼                 ▼
       ┌───────┐   ┌───────┐   ┌───────┐         ┌───────┐
       │FFmpeg │   │FFmpeg │   │FFmpeg │   ...   │FFmpeg │
       │Process│   │Process│   │Process│         │Process│
       └───────┘   └───────┘   └───────┘         └───────┘
```

1. **Orchestrator** parses config and starts the metrics server
2. **Ramp scheduler** starts clients at the configured rate with jitter
3. **Each client supervisor** spawns an FFmpeg process:
   ```bash
   ffmpeg -hide_banner -nostdin -loglevel info \
     -reconnect 1 -reconnect_streamed 1 \
     -user_agent "go-ffmpeg-hls-swarm/1.0" \
     -i "<HLS_URL>" -map 0 -c copy -f null -
   ```
4. **On failure**, clients restart with exponential backoff
5. **On SIGTERM/SIGINT**, signals propagate to all FFmpeg processes

---

## Why Trust This Design?

This tool is built on careful research, not guesswork:

- **[FFmpeg HLS Reference](docs/FFMPEG_HLS_REFERENCE.md)** — Deep source code analysis of FFmpeg's HLS implementation
- **Every CLI flag** maps to documented, tested FFmpeg options
- **Process supervision** follows proven patterns from production orchestrators
- **Failure modes** are explicitly documented with mitigations

The design phase lets us get the architecture right before writing code that's hard to change.

---

## Interpreting Your Results

After running a load test, look for these patterns:

| Scenario | What You'll See | What It Means |
|----------|-----------------|---------------|
| 🟢 **Healthy** | Active clients ≈ target, low restart rate | Infrastructure handling load well |
| 🟡 **At Limit** | Active < target (e.g., 150/200), steady restarts | Found the breaking point — this is useful data! |
| 🔴 **Failing** | All clients restarting rapidly, high error rate | Origin/CDN severely overloaded or misconfigured |

**Key insight**: "Failure" in load testing is often success — you're finding where things break before your users do.

---

## Metrics

Available at `/metrics` (default port 9090):

| Metric | Description |
|--------|-------------|
| `hlsswarm_clients_active` | Currently running FFmpeg processes |
| `hlsswarm_clients_target` | Configured target client count |
| `hlsswarm_clients_started_total` | Total clients ever started |
| `hlsswarm_clients_restarted_total` | Total restart events |
| `hlsswarm_process_exits_total{code}` | Exits by exit code |

---

## Documentation

| Your Goal | Read This |
|-----------|-----------|
| **Start a test HLS origin** | [Quick Start](#quick-start) (above) or `make test-origin` |
| **Run origin as MicroVM/Container** | [Test Origin Guide](docs/TEST_ORIGIN.md) |
| **Understand the swarm client CLI** | [Configuration Reference](docs/CONFIGURATION.md) |
| **Run at scale (OS tuning)** | [Operations Guide](docs/OPERATIONS.md) |
| **Contribute to development** | [Contributing](CONTRIBUTING.md) → [Design](docs/DESIGN.md) |

<details>
<summary><b>📚 All Documentation</b></summary>

**User Documentation:**
- [Quick Start Guide](docs/QUICKSTART.md) — 5-minute tutorial
- [Configuration Reference](docs/CONFIGURATION.md) — All CLI flags
- [Operations Guide](docs/OPERATIONS.md) — OS tuning, troubleshooting
- [Observability](docs/OBSERVABILITY.md) — Metrics, logging

**Contributor/Advanced:**
- [Design Document](docs/DESIGN.md) — Architecture for contributors
- [FFmpeg HLS Reference](docs/FFMPEG_HLS_REFERENCE.md) — FFmpeg source analysis
- [Supervision](docs/SUPERVISION.md) — Process lifecycle details
- [Test Origin Server](docs/TEST_ORIGIN.md) — Local HLS origin for testing
- [Client Deployment](docs/CLIENT_DEPLOYMENT.md) — Containers/VMs
- [Nix Flake Design](docs/NIX_FLAKE_DESIGN.md) — For Nix users

</details>

---

## Project Status & Roadmap

| Component | Status | Notes |
|-----------|--------|-------|
| ✅ **Design & Documentation** | Complete | All docs written |
| ✅ **FFmpeg HLS Research** | Complete | See [FFMPEG_HLS_REFERENCE.md](docs/FFMPEG_HLS_REFERENCE.md) |
| ✅ **Nix Infrastructure** | Complete | Flake, shell, checks, apps |
| ✅ **Test Origin Server** | **Implemented** | Runner, container, MicroVM |
| ✅ **Makefile** | Complete | All targets functional |
| 🚧 **Go Swarm Client** | In Progress | Core packages written |
| ⏳ **Integration Tests** | Waiting | NixOS VM tests defined |

### What's Working Now

```bash
# Test origin (FFmpeg + Nginx)
make test-origin                    # Run locally
make microvm-origin                 # Run in MicroVM (KVM)
nix build .#test-origin-container   # Build OCI container

# Development
make dev                            # Enter Nix shell
make build                          # Build Go binary
make check                          # Run all checks
```

**Want to help?** This is the perfect time to contribute! See [CONTRIBUTING.md](CONTRIBUTING.md).

---

## FAQ

<details>
<summary><b>Can I use this tool today?</b></summary>

**Yes, partially!**
- ✅ **Test Origin Server** — Fully working. Run `make test-origin` to start a local HLS stream.
- ✅ **MicroVM deployment** — Run `make microvm-origin` for full VM isolation.
- ✅ **Container deployment** — Build with `nix build .#test-origin-container`.
- 🚧 **Swarm Client** — Core Go packages exist, CLI integration in progress. Use the [shell script preview](#try-it-now-no-installation-needed) for now.
</details>

<details>
<summary><b>Why FFmpeg instead of a custom HLS client?</b></summary>

FFmpeg's HLS demuxer handles all protocol edge cases: master playlist parsing, variant selection, live playlist refresh, segment sequencing, and reconnection. We'd spend months reimplementing what FFmpeg already does perfectly.
</details>

<details>
<summary><b>How many clients can I run?</b></summary>

Depends on your system. Typically 50-200+ on a well-tuned Linux box. Each FFmpeg process uses ~20-50MB RAM. See [Operations Guide](docs/OPERATIONS.md) for OS tuning.
</details>

<details>
<summary><b>Will this work on macOS/Windows?</b></summary>

Linux is recommended for high concurrency (easier FD/process tuning). macOS should work for smaller tests. Windows is untested.
</details>

<details>
<summary><b>Is this a DDoS tool?</b></summary>

No. This is for testing **your own** infrastructure. Please don't use it against services you don't own or have permission to test.
</details>

---

## License

[MIT](LICENSE)

---

<p align="center">
  <em>Built for finding where your streaming infrastructure breaks, so you can fix it before your viewers do.</em>
</p>
