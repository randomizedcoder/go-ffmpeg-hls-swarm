// Package main provides the go-ffmpeg-hls-swarm CLI entry point.
//
// go-ffmpeg-hls-swarm is a load testing tool that orchestrates a swarm of FFmpeg
// processes to stress-test HLS (HTTP Live Streaming) infrastructure.
package main

import (
	"fmt"
	"os"
)

// version is set at build time via ldflags
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-v" || os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Printf("go-ffmpeg-hls-swarm %s\n", version)
		return
	}

	fmt.Print(`
╔═══════════════════════════════════════════════════════════════════╗
║                     go-ffmpeg-hls-swarm                           ║
║     HLS Load Testing with FFmpeg Process Orchestration            ║
╚═══════════════════════════════════════════════════════════════════╝

🚧 Implementation in progress!

This tool will orchestrate 50-200+ concurrent FFmpeg processes to
stress-test your HLS infrastructure.

┌─────────────────────────────────────────────────────────────────────┐
│ What's Coming:                                                      │
│                                                                     │
│   • Controlled ramp-up to avoid thundering herd                    │
│   • Process supervision with exponential backoff                   │
│   • Prometheus metrics at /metrics                                 │
│   • Graceful shutdown with signal propagation                      │
│   • DNS override for testing specific servers                      │
│   • Cache bypass for origin stress testing                         │
└─────────────────────────────────────────────────────────────────────┘

📖 Documentation:
   • README.md           - Overview and quick start
   • docs/QUICKSTART.md  - 5-minute tutorial
   • docs/DESIGN.md      - Architecture for contributors

🔧 Try the core concept now (with just FFmpeg):

   ffmpeg -hide_banner -loglevel info \
     -reconnect 1 -reconnect_streamed 1 \
     -i "https://test-streams.mux.dev/x36xhzz/x36xhzz.m3u8" \
     -map 0 -c copy -f null -

💬 Want to contribute? See CONTRIBUTING.md
`)
}
