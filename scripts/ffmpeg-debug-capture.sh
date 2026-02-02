#!/usr/bin/env bash
#
# ffmpeg-debug-capture.sh - Capture FFmpeg debug output for parser development
#
# This script runs FFmpeg with the same flags as go-ffmpeg-hls-swarm to capture
# stderr output for analyzing log patterns and creating test data.
#
# Usage:
#   ./scripts/ffmpeg-debug-capture.sh [STREAM_URL] [DURATION] [OUTPUT_FILE]
#
# Examples:
#   ./scripts/ffmpeg-debug-capture.sh http://10.177.0.10:17080/stream.m3u8
#   ./scripts/ffmpeg-debug-capture.sh http://10.177.0.10:17080/stream.m3u8 30s
#   ./scripts/ffmpeg-debug-capture.sh http://10.177.0.10:17080/stream.m3u8 60s ffmpeg_capture.log
#
set -euo pipefail

# Defaults
DEFAULT_URL="http://10.177.0.10:17080/stream.m3u8"
DEFAULT_DURATION="30"
DEFAULT_OUTPUT="ffmpeg_debug_$(date +%Y%m%d_%H%M%S).log"

# Parse arguments
STREAM_URL="${1:-$DEFAULT_URL}"
DURATION="${2:-$DEFAULT_DURATION}"
OUTPUT_FILE="${3:-$DEFAULT_OUTPUT}"

# Strip 's' suffix from duration if present (e.g., "30s" -> "30")
DURATION="${DURATION%s}"

echo "=== FFmpeg Debug Capture ==="
echo "Stream URL:  $STREAM_URL"
echo "Duration:    ${DURATION}s"
echo "Output file: $OUTPUT_FILE"
echo ""

# Check if ffmpeg is available
if ! command -v ffmpeg &> /dev/null; then
    echo "ERROR: ffmpeg not found in PATH"
    exit 1
fi

echo "Starting FFmpeg capture (press Ctrl+C to stop early)..."
echo ""

# Run FFmpeg with the same flags as go-ffmpeg-hls-swarm
# See: internal/process/ffmpeg.go buildArgs()
timeout "${DURATION}" ffmpeg \
    -hide_banner \
    -nostdin \
    -loglevel repeat+level+datetime+debug \
    -reconnect 1 \
    -reconnect_streamed 1 \
    -reconnect_on_network_error 1 \
    -reconnect_delay_max 5 \
    -rw_timeout 15000000 \
    -user_agent "go-ffmpeg-hls-swarm/1.0/client-debug" \
    -seg_max_retry 3 \
    -i "$STREAM_URL" \
    -map 0:v:0? -map 0:a:0? \
    -c copy \
    -f null \
    - \
    2>&1 | tee "$OUTPUT_FILE" || true

echo ""
echo "=== Capture Complete ==="
echo "Output saved to: $OUTPUT_FILE"
echo ""

# Analyze the captured output
echo "=== Pattern Analysis ==="
echo ""

# Pattern 1: HLS Request (only during initial parsing)
HLS_REQUEST_COUNT=$(grep -c '\[hls @ 0x[0-9a-f]*\].*HLS request for url' "$OUTPUT_FILE" 2>/dev/null || echo "0")
echo "Pattern 1 - HLS Request:     $HLS_REQUEST_COUNT occurrences"
echo "  Regex: \\[hls @ 0x...\\] HLS request for url '...'"
echo "  Note:  Only fires during INITIAL playlist parsing"

# Pattern 2: HTTP Open (only for new connections)
HTTP_OPEN_COUNT=$(grep -c '\[http @ 0x[0-9a-f]*\].*Opening.*for reading' "$OUTPUT_FILE" 2>/dev/null || echo "0")
echo ""
echo "Pattern 2 - HTTP Open:       $HTTP_OPEN_COUNT occurrences"
echo "  Regex: \\[http @ 0x...\\] Opening '...' for reading"
echo "  Note:  Only fires for NEW connections, not keep-alive"

# Pattern 3: HTTP GET (should fire for ALL requests)
HTTP_GET_COUNT=$(grep -c '\[http @ 0x[0-9a-f]*\].*request: GET' "$OUTPUT_FILE" 2>/dev/null || echo "0")
echo ""
echo "Pattern 3 - HTTP GET:        $HTTP_GET_COUNT occurrences"
echo "  Regex: \\[http @ 0x...\\] request: GET /... HTTP/..."
echo "  Note:  Should fire for ALL requests including keep-alive"

# Segment-specific counts
echo ""
echo "=== Segment-Specific Counts ==="
SEGMENT_HLS=$(grep -c '\.ts.*HLS request' "$OUTPUT_FILE" 2>/dev/null || echo "0")
SEGMENT_OPEN=$(grep -c '\.ts.*for reading' "$OUTPUT_FILE" 2>/dev/null || echo "0")
SEGMENT_GET=$(grep -c 'request: GET.*\.ts' "$OUTPUT_FILE" 2>/dev/null || echo "0")
echo "Segments via HLS Request:  $SEGMENT_HLS"
echo "Segments via HTTP Open:    $SEGMENT_OPEN"
echo "Segments via HTTP GET:     $SEGMENT_GET"

# Manifest counts
echo ""
echo "=== Manifest Counts ==="
MANIFEST_OPEN=$(grep -c '\.m3u8.*for reading' "$OUTPUT_FILE" 2>/dev/null || echo "0")
MANIFEST_GET=$(grep -c 'request: GET.*\.m3u8' "$OUTPUT_FILE" 2>/dev/null || echo "0")
echo "Manifests via HTTP Open:   $MANIFEST_OPEN"
echo "Manifests via HTTP GET:    $MANIFEST_GET"

# TCP connection events
echo ""
echo "=== TCP Connection Events ==="
TCP_START=$(grep -c '\[tcp @ 0x[0-9a-f]*\].*Starting connection' "$OUTPUT_FILE" 2>/dev/null || echo "0")
TCP_CONNECTED=$(grep -c '\[tcp @ 0x[0-9a-f]*\].*Successfully connected' "$OUTPUT_FILE" 2>/dev/null || echo "0")
echo "TCP Start:      $TCP_START"
echo "TCP Connected:  $TCP_CONNECTED"

# Show sample lines for key patterns
echo ""
echo "=== Sample Lines ==="

echo ""
echo "--- First 3 HLS Request lines ---"
grep '\[hls @ 0x[0-9a-f]*\].*HLS request' "$OUTPUT_FILE" 2>/dev/null | head -3 || echo "(none found)"

echo ""
echo "--- First 3 HTTP Open lines (segments) ---"
grep '\[http @ 0x[0-9a-f]*\].*Opening.*\.ts.*for reading' "$OUTPUT_FILE" 2>/dev/null | head -3 || echo "(none found)"

echo ""
echo "--- First 3 HTTP GET lines (segments) ---"
grep '\[http @ 0x[0-9a-f]*\].*request: GET.*\.ts' "$OUTPUT_FILE" 2>/dev/null | head -3 || echo "(none found)"

echo ""
echo "--- Last 3 HTTP GET lines (segments) - should show steady-state ---"
grep '\[http @ 0x[0-9a-f]*\].*request: GET.*\.ts' "$OUTPUT_FILE" 2>/dev/null | tail -3 || echo "(none found)"

echo ""
echo "=== Investigation Notes ==="
echo ""
echo "If HTTP GET count is 0 but HTTP Open count > 0:"
echo "  -> FFmpeg may not be logging 'request: GET' at this log level"
echo "  -> Try running with -loglevel trace (very verbose)"
echo ""
echo "If HTTP GET count stops increasing after initial burst:"
echo "  -> Keep-alive connections may not be logging requests"
echo "  -> This would explain why throughput stops after ramp-up"
echo ""
echo "Full log file: $OUTPUT_FILE"
