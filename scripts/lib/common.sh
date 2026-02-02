#!/usr/bin/env bash
# Common functions for load test scripts
# Source this file in test scripts: source "$(dirname "$0")/../lib/common.sh"

set -euo pipefail

# ═══════════════════════════════════════════════════════════════════════════════
# Configuration
# See docs/PORTS.md for full port documentation
# ═══════════════════════════════════════════════════════════════════════════════
export HLS_DIR="${HLS_DIR:-/tmp/hls-test}"

# Local test origin (Python HTTP server)
export ORIGIN_PORT="${ORIGIN_PORT:-17088}"
export STREAM_URL="${STREAM_URL:-http://localhost:${ORIGIN_PORT}/stream.m3u8}"

# Swarm client Prometheus metrics
export METRICS_PORT="${METRICS_PORT:-17091}"

# MicroVM/Container origin (Nginx)
export MICROVM_HTTP_PORT="${MICROVM_HTTP_PORT:-17080}"
export MICROVM_METRICS_PORT="${MICROVM_METRICS_PORT:-17113}"
export MICROVM_CONSOLE_PORT="${MICROVM_CONSOLE_PORT:-17022}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# ═══════════════════════════════════════════════════════════════════════════════
# Logging functions
# ═══════════════════════════════════════════════════════════════════════════════
log_info() { echo -e "${CYAN}[INFO]${NC} $*"; }
log_success() { echo -e "${GREEN}[OK]${NC} $*"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }

# ═══════════════════════════════════════════════════════════════════════════════
# Port checking functions
# ═══════════════════════════════════════════════════════════════════════════════

# Check if a port is in use by trying to connect to it
# Returns 0 if port is FREE, 1 if port is IN USE
is_port_free() {
    local port="$1"
    # Try to connect - if it succeeds, something is listening (port in use)
    if (echo >/dev/tcp/localhost/"${port}") 2>/dev/null; then
        return 1  # Port is in use
    fi
    return 0  # Port is free
}

# Check required ports and exit with helpful message if any are in use
# Usage: check_ports_available 8080 9091 9113
check_ports_available() {
    local ports=("$@")
    local blocked=()
    
    log_info "Checking port availability..."
    
    for port in "${ports[@]}"; do
        if ! is_port_free "${port}"; then
            blocked+=("${port}")
            log_error "Port ${port} is already in use!"
        else
            log_success "Port ${port} is available"
        fi
    done
    
    if [ ${#blocked[@]} -gt 0 ]; then
        echo ""
        echo -e "${RED}╔════════════════════════════════════════════════════════════════╗${NC}"
        echo -e "${RED}║  ERROR: ${#blocked[@]} port(s) already in use!                            ║${NC}"
        echo -e "${RED}╚════════════════════════════════════════════════════════════════╝${NC}"
        echo ""
        echo -e "${YELLOW}Blocked ports: ${blocked[*]}${NC}"
        echo ""
        echo -e "${CYAN}To see what's using them:${NC}"
        for port in "${blocked[@]}"; do
            echo "  lsof -i :${port}"
        done
        echo ""
        echo -e "${CYAN}To free them:${NC}"
        for port in "${blocked[@]}"; do
            echo "  sudo fuser -k ${port}/tcp"
        done
        echo ""
        echo -e "${CYAN}Or use different ports:${NC}"
        echo "  ORIGIN_PORT=9000 METRICS_PORT=9092 make load-test-100"
        echo ""
        exit 1
    fi
    
    echo ""
}

# Check ports for local origin tests
check_local_origin_ports() {
    check_ports_available "${ORIGIN_PORT}" "${METRICS_PORT}"
}

# Check ports for MicroVM
check_microvm_ports() {
    check_ports_available "${MICROVM_HTTP_PORT}" "${MICROVM_METRICS_PORT}" "${MICROVM_CONSOLE_PORT}"
}

# ═══════════════════════════════════════════════════════════════════════════════
# Cleanup function - call in trap
# ═══════════════════════════════════════════════════════════════════════════════
cleanup() {
    log_info "Cleaning up..."
    pkill -f "http.server ${ORIGIN_PORT}" 2>/dev/null || true
    pkill -f "testsrc2.*hls-test" 2>/dev/null || true
    sleep 1
}

# ═══════════════════════════════════════════════════════════════════════════════
# Start the HLS origin server
# ═══════════════════════════════════════════════════════════════════════════════
start_origin() {
    log_info "Starting HLS origin on port ${ORIGIN_PORT}..."
    
    # Check ports first
    check_local_origin_ports
    
    # Clean up any existing
    cleanup
    
    # Create HLS directory
    rm -rf "${HLS_DIR}"
    mkdir -p "${HLS_DIR}"
    
    # Start FFmpeg HLS generator
    nohup ffmpeg -re \
        -f lavfi -i "testsrc2=size=640x480:rate=30" \
        -f lavfi -i "sine=frequency=1000:sample_rate=48000" \
        -c:v libx264 -preset ultrafast -tune zerolatency \
        -c:a aac -b:a 128k -ar 48000 \
        -f hls -hls_time 2 -hls_list_size 10 \
        -hls_flags delete_segments+omit_endlist \
        "${HLS_DIR}/stream.m3u8" > /tmp/ffmpeg-test.log 2>&1 &
    
    # Wait for stream to be ready
    log_info "Waiting for HLS stream..."
    for _ in $(seq 1 20); do
        if [ -f "${HLS_DIR}/stream.m3u8" ]; then
            log_success "HLS stream ready"
            break
        fi
        sleep 1
    done
    
    if [ ! -f "${HLS_DIR}/stream.m3u8" ]; then
        log_error "Failed to start HLS stream"
        exit 1
    fi
    
    # Start HTTP server
    cd "${HLS_DIR}"
    nohup python3 -m http.server "${ORIGIN_PORT}" > /tmp/http-test.log 2>&1 &
    cd - > /dev/null
    sleep 2
    
    # Verify server is running
    if curl -sf "http://localhost:${ORIGIN_PORT}/stream.m3u8" > /dev/null; then
        log_success "Origin server running at http://localhost:${ORIGIN_PORT}"
    else
        log_error "Origin server failed to start"
        exit 1
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Run load test
# Args: $1 = number of clients, $2 = duration, $3 = ramp rate
# ═══════════════════════════════════════════════════════════════════════════════
run_load_test() {
    local clients="${1:-50}"
    local duration="${2:-60s}"
    local ramp_rate="${3:-10}"
    
    log_info "Running load test: ${clients} clients, ${duration} duration, ${ramp_rate}/sec ramp"
    echo ""
    echo "╔════════════════════════════════════════════════════════════════════════╗"
    echo "║                    HLS Load Test - ${clients} Clients                          ║"
    echo "╠════════════════════════════════════════════════════════════════════════╣"
    echo "║ Stream URL:  ${STREAM_URL}"
    echo "║ Metrics:     http://localhost:${METRICS_PORT}/metrics"
    echo "║ Duration:    ${duration}"
    echo "║ Ramp Rate:   ${ramp_rate} clients/sec"
    echo "╚════════════════════════════════════════════════════════════════════════╝"
    echo ""
    
    # Find the swarm binary
    local swarm_binary
    if [ -x "./bin/go-ffmpeg-hls-swarm" ]; then
        swarm_binary="./bin/go-ffmpeg-hls-swarm"
    elif [ -x "./go-ffmpeg-hls-swarm" ]; then
        swarm_binary="./go-ffmpeg-hls-swarm"
    elif [ -x "./result/bin/go-ffmpeg-hls-swarm" ]; then
        swarm_binary="./result/bin/go-ffmpeg-hls-swarm"
    else
        log_error "go-ffmpeg-hls-swarm binary not found. Run 'make build' first."
        exit 1
    fi
    
    "${swarm_binary}" \
        -clients "${clients}" \
        -duration "${duration}" \
        -ramp-rate "${ramp_rate}" \
        -metrics "0.0.0.0:${METRICS_PORT}" \
        "${STREAM_URL}"
}

# ═══════════════════════════════════════════════════════════════════════════════
# Full test: start origin + run load test + cleanup
# Args: $1 = number of clients, $2 = duration, $3 = ramp rate
# ═══════════════════════════════════════════════════════════════════════════════
full_test() {
    local clients="${1:-50}"
    local duration="${2:-60s}"
    local ramp_rate="${3:-10}"
    
    trap cleanup EXIT INT TERM
    
    start_origin
    echo ""
    run_load_test "${clients}" "${duration}" "${ramp_rate}"
}

# ═══════════════════════════════════════════════════════════════════════════════
# MicroVM helpers
# ═══════════════════════════════════════════════════════════════════════════════

# Start MicroVM origin (checks ports first)
start_microvm_origin() {
    log_info "Preparing to start MicroVM origin..."
    
    # Check ports before attempting to start
    check_microvm_ports
    
    log_info "Starting MicroVM (this takes ~30 seconds)..."
    
    # Check if result/bin/microvm-run exists
    if [ ! -x "./result/bin/microvm-run" ]; then
        log_info "Building MicroVM (first time only)..."
        nix build .#test-origin-vm -o result 2>&1 | tail -5
    fi
    
    # Start MicroVM with stdin from /dev/null to prevent SIGTSTP
    ./result/bin/microvm-run < /dev/null > /tmp/microvm.log 2>&1 &
    local qemu_pid=$!
    echo "${qemu_pid}" > /tmp/microvm.pid
    
    log_info "MicroVM started (PID: ${qemu_pid})"
    log_info "Waiting for VM to boot..."
    
    # Wait for health endpoint
    local attempts=0
    local max_attempts=60
    while [ $attempts -lt $max_attempts ]; do
        if curl -sf "http://localhost:${MICROVM_HTTP_PORT}/health" > /dev/null 2>&1; then
            log_success "MicroVM is ready!"
            echo ""
            echo "  Stream:  http://localhost:${MICROVM_HTTP_PORT}/stream.m3u8"
            echo "  Health:  http://localhost:${MICROVM_HTTP_PORT}/health"
            echo "  Metrics: http://localhost:${MICROVM_METRICS_PORT}/metrics"
            echo ""
            return 0
        fi
        sleep 1
        attempts=$((attempts + 1))
        if [ $((attempts % 10)) -eq 0 ]; then
            log_info "Still waiting... (${attempts}s)"
        fi
    done
    
    log_error "MicroVM failed to start within ${max_attempts} seconds"
    log_error "Check /tmp/microvm.log for details"
    return 1
}

# Stop MicroVM origin
stop_microvm_origin() {
    log_info "Stopping MicroVM..."
    if [ -f /tmp/microvm.pid ]; then
        local pid
        pid=$(cat /tmp/microvm.pid)
        kill "${pid}" 2>/dev/null || true
        rm -f /tmp/microvm.pid
    fi
    pkill -f "qemu.*hls-origin" 2>/dev/null || true
    log_success "MicroVM stopped"
}
