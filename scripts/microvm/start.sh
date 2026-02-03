#!/usr/bin/env bash
# Start the HLS Origin MicroVM with health polling
# Usage: ./scripts/microvm/start.sh [--tap] [--timeout SECONDS]
#
# This script:
# 1. Checks ports/network are available
# 2. Builds the MicroVM if needed
# 3. Starts the MicroVM in background
# 4. Polls health endpoint until ready (or timeout)
# 5. Reports status
#
# Networking modes:
#   Default (user-mode): Uses QEMU NAT, ~500 Mbps, zero config
#   --tap:               Uses TAP + vhost-net, ~10 Gbps, requires make network-setup

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# Configuration
TIMEOUT="${TIMEOUT:-120}"  # Max seconds to wait for VM to start
POLL_INTERVAL=2            # Seconds between health checks
USE_TAP=false              # Use TAP networking (high performance)

# Ports
HTTP_PORT="${MICROVM_HTTP_PORT:-17080}"
METRICS_PORT="${MICROVM_METRICS_PORT:-17113}"
NODE_EXPORTER_PORT="${MICROVM_NODE_EXPORTER_PORT:-17100}"
SSH_PORT="${MICROVM_SSH_PORT:-17122}"
CONSOLE_PORT="${MICROVM_CONSOLE_PORT:-17022}"

# URLs - will be set based on networking mode
HEALTH_URL=""
STREAM_URL=""
FILES_URL=""
VM_IP="10.177.0.10"  # Static IP for TAP mode

PID_FILE="/tmp/microvm-origin.pid"
LOG_FILE="/tmp/microvm-origin.log"
RESULT_DIR="result"  # Will be updated by build_if_needed

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info() { echo -e "${CYAN}[INFO]${NC} $*"; }
log_success() { echo -e "${GREEN}[OK]${NC} $*"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --tap)
            USE_TAP=true
            shift
            ;;
        --timeout)
            TIMEOUT="$2"
            shift 2
            ;;
        --help|-h)
            echo "Usage: $0 [--tap] [--timeout SECONDS]"
            echo ""
            echo "Options:"
            echo "  --tap                Use TAP + vhost-net (~10 Gbps, requires make network-setup)"
            echo "  --timeout SECONDS    Max wait time for VM to start (default: 120)"
            echo ""
            echo "Networking modes:"
            echo "  Default:  QEMU user-mode NAT (~500 Mbps, zero config)"
            echo "  --tap:    TAP + vhost-net (~10 Gbps multiqueue, high performance)"
            echo ""
            echo "Environment variables:"
            echo "  MICROVM_HTTP_PORT     HLS port (default: 17080)"
            echo "  MICROVM_METRICS_PORT  Prometheus port (default: 17113)"
            exit 0
            ;;
        *)
            log_error "Unknown option: $1"
            exit 1
            ;;
    esac
done

# Set URLs and ports based on networking mode
if [ "$USE_TAP" = true ]; then
    # TAP mode: direct access to VM IP, use standard ports
    URL_BASE="${VM_IP}"
    NGINX_EXPORTER_PORT="9113"   # Standard nginx exporter port
    NODE_EXPORTER_PORT="9100"    # Standard node exporter port
    SSH_PORT="22"                # Standard SSH port
else
    # User mode: localhost with QEMU port forwarding
    URL_BASE="localhost"
    NGINX_EXPORTER_PORT="${METRICS_PORT}"  # 17113 forwarded to 9113
    NODE_EXPORTER_PORT="17100"             # 17100 forwarded to 9100
    SSH_PORT="17122"                       # 17122 forwarded to 22
fi

HEALTH_URL="http://${URL_BASE}:${HTTP_PORT}/health"
STREAM_URL="http://${URL_BASE}:${HTTP_PORT}/stream.m3u8"
FILES_URL="http://${URL_BASE}:${HTTP_PORT}/files/json/"

cd "${PROJECT_ROOT}"

# ═══════════════════════════════════════════════════════════════════════════════
# Check TAP network setup (only for --tap mode)
# ═══════════════════════════════════════════════════════════════════════════════
check_tap_network() {
    if [ "$USE_TAP" != true ]; then
        return 0
    fi

    log_info "Checking TAP network setup..."

    # Check bridge exists
    if ! ip link show hlsbr0 &>/dev/null; then
        log_error "Bridge hlsbr0 not found!"
        echo ""
        echo -e "${YELLOW}Run: make network-setup${NC}"
        exit 1
    fi
    log_success "Bridge hlsbr0 exists"

    # Check TAP exists with multiqueue
    if ! ip link show hlstap0 &>/dev/null; then
        log_error "TAP device hlstap0 not found!"
        echo ""
        echo -e "${YELLOW}Run: make network-setup${NC}"
        exit 1
    fi
    log_success "TAP hlstap0 exists (multiqueue)"

    # Check TAP is attached to bridge
    if ! ip link show hlstap0 | grep -q "master hlsbr0"; then
        log_error "TAP hlstap0 not attached to bridge hlsbr0!"
        echo ""
        echo -e "${YELLOW}Run: make network-teardown && make network-setup${NC}"
        exit 1
    fi
    log_success "TAP attached to bridge"

    echo ""
}

# ═══════════════════════════════════════════════════════════════════════════════
# Check if port is in use (returns 0 if IN USE, 1 if FREE)
# ═══════════════════════════════════════════════════════════════════════════════
is_port_in_use() {
    local port="$1"
    bash -c "(echo >/dev/tcp/localhost/${port}) 2>/dev/null"
}

# ═══════════════════════════════════════════════════════════════════════════════
# Check ports are available
# ═══════════════════════════════════════════════════════════════════════════════
check_ports() {
    log_info "Checking port availability..."

    local blocked=0

    # Always check HTTP port (used by both modes on host/VM)
    if is_port_in_use "${HTTP_PORT}"; then
        log_error "Port ${HTTP_PORT} (HTTP) is already in use!"
        blocked=1
    else
        log_success "Port ${HTTP_PORT} (HTTP) is available"
    fi

    # Always check console port (QEMU listens on host)
    if is_port_in_use "${CONSOLE_PORT}"; then
        log_error "Port ${CONSOLE_PORT} (Console) is already in use!"
        blocked=1
    else
        log_success "Port ${CONSOLE_PORT} (Console) is available"
    fi

    # For user-mode networking, check forwarded ports on localhost
    # For TAP mode, these ports are VM-internal (9100, 9113, 22) - don't check on host
    if [ "$USE_TAP" = false ]; then
        if is_port_in_use "${METRICS_PORT}"; then
            log_error "Port ${METRICS_PORT} (Nginx Metrics) is already in use!"
            blocked=1
        else
            log_success "Port ${METRICS_PORT} (Nginx Metrics) is available"
        fi

        if is_port_in_use "${NODE_EXPORTER_PORT}"; then
            log_error "Port ${NODE_EXPORTER_PORT} (Node Exporter) is already in use!"
            blocked=1
        else
            log_success "Port ${NODE_EXPORTER_PORT} (Node Exporter) is available"
        fi

        if is_port_in_use "${SSH_PORT}"; then
            log_error "Port ${SSH_PORT} (SSH) is already in use!"
            blocked=1
        else
            log_success "Port ${SSH_PORT} (SSH) is available"
        fi
    else
        log_info "TAP mode: VM services use internal ports (9100, 9113, 22) - not checked on host"
    fi

    if [ $blocked -eq 1 ]; then
        echo ""
        log_error "Cannot start MicroVM - ports in use"
        echo ""
        echo -e "${YELLOW}To free ports:${NC}"
        if [ "$USE_TAP" = false ]; then
            echo "  sudo fuser -k ${HTTP_PORT}/tcp ${METRICS_PORT}/tcp ${NODE_EXPORTER_PORT}/tcp ${SSH_PORT}/tcp ${CONSOLE_PORT}/tcp"
        else
            echo "  sudo fuser -k ${HTTP_PORT}/tcp ${CONSOLE_PORT}/tcp"
        fi
        echo "  # Or kill previous MicroVM:"
        echo "  pkill -f 'qemu.*hls-origin'"
        echo ""
        exit 1
    fi

    echo ""
}

# ═══════════════════════════════════════════════════════════════════════════════
# Build MicroVM if needed
# ═══════════════════════════════════════════════════════════════════════════════
build_if_needed() {
    local vm_package
    local result_dir

    if [ "$USE_TAP" = true ]; then
        vm_package=".#test-origin-vm-tap"
        result_dir="result-tap"
    else
        vm_package=".#test-origin-vm"
        result_dir="result"
    fi

    log_info "VM package: ${vm_package}"
    log_info "Result directory: ${result_dir}"

    if [ ! -x "./${result_dir}/bin/microvm-run" ]; then
        log_info "Building MicroVM (${vm_package})..."
        if nix build "${vm_package}" -o "${result_dir}" 2>&1; then
            log_success "MicroVM built successfully"
        else
            log_error "Failed to build MicroVM!"
            exit 1
        fi
    else
        log_success "MicroVM already built (${result_dir})"
        ls -la "./${result_dir}/bin/" | head -5
    fi

    # Verify the result
    if [ ! -x "./${result_dir}/bin/microvm-run" ]; then
        log_error "Build succeeded but microvm-run not found!"
        ls -la "./${result_dir}/" 2>/dev/null || echo "Result dir doesn't exist"
        exit 1
    fi

    # Export for start_vm
    RESULT_DIR="${result_dir}"
}

# ═══════════════════════════════════════════════════════════════════════════════
# Stop any existing MicroVM
# ═══════════════════════════════════════════════════════════════════════════════
stop_existing() {
    local found_running=false

    # Check for running QEMU/MicroVM processes (matches both naming conventions)
    local qemu_pids
    qemu_pids=$(pgrep -f 'qemu.*hls-origin|microvm@hls-origin' 2>/dev/null || true)

    if [ -n "$qemu_pids" ]; then
        found_running=true
        log_warn "Found existing MicroVM process(es):"
        echo "$qemu_pids" | while read -r pid; do
            echo "  PID $pid"
        done

        log_info "Stopping existing MicroVM(s)..."

        # First try graceful shutdown
        echo "$qemu_pids" | xargs -r kill 2>/dev/null || true
        sleep 2

        # Check if any are still running
        qemu_pids=$(pgrep -f 'qemu.*hls-origin|microvm@hls-origin' 2>/dev/null || true)
        if [ -n "$qemu_pids" ]; then
            log_warn "Processes still running, sending SIGKILL..."
            echo "$qemu_pids" | xargs -r kill -9 2>/dev/null || true
            sleep 1
        fi

        # Final check
        qemu_pids=$(pgrep -f 'qemu.*hls-origin|microvm@hls-origin' 2>/dev/null || true)
        if [ -n "$qemu_pids" ]; then
            log_error "Failed to stop existing MicroVM(s)!"
            echo "Still running PIDs: $qemu_pids"
            echo ""
            echo -e "${YELLOW}Try manually:${NC}"
            echo "  sudo kill -9 $qemu_pids"
            exit 1
        fi

        log_success "Existing MicroVM(s) stopped"
    fi

    # Clean up PID file
    if [ -f "${PID_FILE}" ]; then
        rm -f "${PID_FILE}"
    fi

    # If we stopped something, wait a moment for ports to be released
    if [ "$found_running" = true ]; then
        log_info "Waiting for ports to be released..."
        sleep 2
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Start MicroVM
# ═══════════════════════════════════════════════════════════════════════════════
start_vm() {
    local mode_str
    if [ "$USE_TAP" = true ]; then
        mode_str="TAP + vhost-net (multiqueue, ~10 Gbps)"
    else
        mode_str="user-mode NAT (~500 Mbps)"
    fi

    log_info "Starting MicroVM with ${mode_str}..."

    local vm_cmd="./${RESULT_DIR}/bin/microvm-run"

    # Verify the command exists and is executable
    if [ ! -x "${vm_cmd}" ]; then
        log_error "VM command not found or not executable: ${vm_cmd}"
        exit 1
    fi

    log_info "VM command: ${vm_cmd}"
    log_info "Log file: ${LOG_FILE}"

    # Show the actual QEMU command being run
    log_info "QEMU command (from runner script):"
    grep -E "^exec|qemu" "${vm_cmd}" | head -3 | while read -r line; do
        echo "    ${line:0:100}..."
    done

    # Start with setsid to create new session (prevents SIGHUP when parent exits)
    # stdin from /dev/null, output to log file
    log_info "Executing: setsid ${vm_cmd} < /dev/null > ${LOG_FILE} 2>&1 &"
    setsid "${vm_cmd}" < /dev/null > "${LOG_FILE}" 2>&1 &
    local pid=$!
    echo "${pid}" > "${PID_FILE}"

    # Small delay to let QEMU start
    sleep 1

    # Check if process is still running
    if kill -0 "${pid}" 2>/dev/null; then
        log_success "MicroVM process started (PID: ${pid})"
    else
        log_error "MicroVM process failed to start or exited immediately!"
        echo ""
        echo -e "${YELLOW}Log output:${NC}"
        cat "${LOG_FILE}" 2>/dev/null | head -30 || echo "(no log)"
        exit 1
    fi

    # Show first few lines of log
    log_info "Initial log output:"
    sleep 1
    head -10 "${LOG_FILE}" 2>/dev/null | while read -r line; do
        echo "    ${line}"
    done
}

# ═══════════════════════════════════════════════════════════════════════════════
# Poll for health endpoint
# ═══════════════════════════════════════════════════════════════════════════════
wait_for_ready() {
    log_info "Waiting for MicroVM to be ready (timeout: ${TIMEOUT}s)..."

    local elapsed=0
    local spinner=('⠋' '⠙' '⠹' '⠸' '⠼' '⠴' '⠦' '⠧' '⠇' '⠏')
    local spin_idx=0

    while [ $elapsed -lt "$TIMEOUT" ]; do
        # Check if process is still running
        if [ -f "${PID_FILE}" ]; then
            local pid
            pid=$(cat "${PID_FILE}")
            if ! kill -0 "${pid}" 2>/dev/null; then
                echo ""
                log_error "MicroVM process died!"
                echo ""
                echo -e "${YELLOW}Last 20 lines of log:${NC}"
                tail -20 "${LOG_FILE}" 2>/dev/null || echo "(no log)"
                exit 1
            fi
        fi

        # Try health endpoint
        if curl -sf "${HEALTH_URL}" > /dev/null 2>&1; then
            echo ""
            log_success "MicroVM is ready! (took ${elapsed}s)"
            return 0
        fi

        # Show spinner
        printf "\r  ${spinner[$spin_idx]} Waiting... (%ds/%ds)" "$elapsed" "$TIMEOUT"
        spin_idx=$(( (spin_idx + 1) % ${#spinner[@]} ))

        sleep "${POLL_INTERVAL}"
        elapsed=$((elapsed + POLL_INTERVAL))
    done

    echo ""
    log_error "Timeout waiting for MicroVM (${TIMEOUT}s)"
    echo ""
    echo -e "${YELLOW}Last 30 lines of log:${NC}"
    tail -30 "${LOG_FILE}" 2>/dev/null || echo "(no log)"
    exit 1
}

# ═══════════════════════════════════════════════════════════════════════════════
# Verify stream is working
# ═══════════════════════════════════════════════════════════════════════════════
verify_stream() {
    log_info "Verifying HLS stream..."

    if curl -sf "${STREAM_URL}" > /dev/null 2>&1; then
        log_success "HLS stream is available"
    else
        log_warn "Health OK but stream not yet available (FFmpeg may still be starting)"
        log_info "Waiting a few more seconds..."
        sleep 5
        if curl -sf "${STREAM_URL}" > /dev/null 2>&1; then
            log_success "HLS stream is now available"
        else
            log_warn "Stream still not ready - check ${LOG_FILE}"
        fi
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Verify FFmpeg is writing files via directory listing
# ═══════════════════════════════════════════════════════════════════════════════
verify_ffmpeg_writing() {
    log_info "Verifying FFmpeg is writing files..."

    local max_attempts=15
    local attempt=0

    while [ $attempt -lt $max_attempts ]; do
        # Get directory listing (JSON format)
        local files_json
        files_json=$(curl -sf "${FILES_URL}" 2>/dev/null || echo "")

        if [ -n "$files_json" ]; then
            # Count .ts and .m3u8 files (robust counting)
            local ts_count m3u8_count
            ts_count=$(echo "$files_json" | grep -o '\.ts"' | wc -l | tr -d ' ')
            m3u8_count=$(echo "$files_json" | grep -o '\.m3u8"' | wc -l | tr -d ' ')

            # Ensure we have valid integers
            ts_count=${ts_count:-0}
            m3u8_count=${m3u8_count:-0}

            if [ "$ts_count" -gt 0 ] && [ "$m3u8_count" -gt 0 ]; then
                log_success "FFmpeg is writing files: ${ts_count} segments, ${m3u8_count} playlists"

                # Show file listing
                echo ""
                echo -e "${CYAN}HLS Directory Contents:${NC}"
                echo "$files_json" | grep -oP '"name"\s*:\s*"\K[^"]+' | sort | head -20
                if [ "$ts_count" -gt 20 ]; then
                    echo "  ... and $((ts_count - 20)) more segments"
                fi
                echo ""
                return 0
            fi
        fi

        attempt=$((attempt + 1))
        printf "\r  Waiting for FFmpeg to generate segments... (%d/%d)" "$attempt" "$max_attempts"
        sleep 2
    done

    echo ""
    log_warn "FFmpeg may still be starting - directory listing:"
    curl -sf "${FILES_URL}" 2>/dev/null | head -5 || echo "  (empty or unavailable)"
    echo ""
}

# ═══════════════════════════════════════════════════════════════════════════════
# Verify SSH is working
# ═══════════════════════════════════════════════════════════════════════════════
verify_ssh() {
    log_info "Verifying SSH access..."

    # Check if SSH port is responding (URL_BASE and SSH_PORT set based on networking mode)
    if timeout 5 bash -c "(echo >/dev/tcp/${URL_BASE}/${SSH_PORT}) 2>/dev/null"; then
        log_success "SSH responding at ${URL_BASE}:${SSH_PORT}"
    else
        log_warn "SSH at ${URL_BASE}:${SSH_PORT} not yet responding (may still be starting)"
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Verify Prometheus exporters are working
# ═══════════════════════════════════════════════════════════════════════════════
verify_exporters() {
    log_info "Verifying Prometheus exporters..."

    # Check nginx exporter (ports set based on networking mode)
    if curl -sf "http://${URL_BASE}:${NGINX_EXPORTER_PORT}/metrics" > /dev/null 2>&1; then
        log_success "Nginx exporter responding at ${URL_BASE}:${NGINX_EXPORTER_PORT}"
    else
        log_warn "Nginx exporter at ${URL_BASE}:${NGINX_EXPORTER_PORT} not yet responding"
    fi

    # Check node exporter
    if curl -sf "http://${URL_BASE}:${NODE_EXPORTER_PORT}/metrics" > /dev/null 2>&1; then
        log_success "Node exporter responding at ${URL_BASE}:${NODE_EXPORTER_PORT}"
    else
        log_warn "Node exporter at ${URL_BASE}:${NODE_EXPORTER_PORT} not yet responding"
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Main
# ═══════════════════════════════════════════════════════════════════════════════
main() {
    local mode_str net_mode
    if [ "$USE_TAP" = true ]; then
        mode_str="TAP + vhost-net (multiqueue)"
        net_mode="tap"
    else
        mode_str="user-mode NAT"
        net_mode="user"
    fi

    echo ""
    echo "╔════════════════════════════════════════════════════════════════════════╗"
    echo "║                    Starting HLS Origin MicroVM                         ║"
    echo "║                    Network: ${mode_str}"
    echo "╚════════════════════════════════════════════════════════════════════════╝"
    echo ""

    check_tap_network  # Only runs checks if --tap
    check_ports
    stop_existing
    build_if_needed
    start_vm
    wait_for_ready
    verify_stream
    verify_ffmpeg_writing
    verify_ssh
    verify_exporters

    echo ""
    echo "╔════════════════════════════════════════════════════════════════════════╗"
    echo "║                    MicroVM Ready!                                      ║"
    echo "║                    Network: ${mode_str}"
    echo "╠════════════════════════════════════════════════════════════════════════╣"
    echo "║ Stream:   http://${URL_BASE}:${HTTP_PORT}/stream.m3u8"
    echo "║ Files:    http://${URL_BASE}:${HTTP_PORT}/files/"
    echo "║ Health:   http://${URL_BASE}:${HTTP_PORT}/health"
    echo "║ Status:   http://${URL_BASE}:${HTTP_PORT}/nginx_status"
    echo "╠════════════════════════════════════════════════════════════════════════╣"
    echo "║ Nginx:    http://${URL_BASE}:${NGINX_EXPORTER_PORT}/metrics"
    echo "║ Node:     http://${URL_BASE}:${NODE_EXPORTER_PORT}/metrics"
    if [ "$USE_TAP" = true ]; then
    echo "║ SSH:      ssh root@${URL_BASE}"
    else
    echo "║ SSH:      ssh -p ${SSH_PORT} root@${URL_BASE}"
    fi
    echo "║ Console:  nc localhost ${CONSOLE_PORT}"
    echo "╠════════════════════════════════════════════════════════════════════════╣"
    echo "║ PID:      $(cat "${PID_FILE}")"
    echo "║ Log:      ${LOG_FILE}"
    echo "╚════════════════════════════════════════════════════════════════════════╝"
    echo ""
    if [ "$USE_TAP" = true ]; then
        echo "🚀 High-performance TAP networking enabled"
        echo "   Throughput: ~10 Gbps with ${net_mode} multiqueue"
        echo "   Direct access to VM at ${VM_IP} (no port forwarding)"
        echo ""
    fi
    echo "📊 Prometheus metrics:"
    echo "  curl -s http://${URL_BASE}:${NGINX_EXPORTER_PORT}/metrics | grep nginx_"
    echo "  curl -s http://${URL_BASE}:${NODE_EXPORTER_PORT}/metrics | grep node_"
    echo ""
    echo "🔑 SSH access (root, empty password):"
    if [ "$USE_TAP" = true ]; then
    echo "  ssh root@${URL_BASE}"
    else
    echo "  ssh -o StrictHostKeyChecking=no -p ${SSH_PORT} root@${URL_BASE}"
    fi
    echo ""
    echo "Debug commands (via SSH or console):"
    echo "  journalctl -u hls-generator -f   # FFmpeg logs"
    echo "  journalctl -u nginx -f           # Nginx logs"
    echo "  ls -la /var/hls/                 # Check HLS files"
    echo ""
    echo "To stop: ./scripts/microvm/stop.sh"
    echo "To test: make load-test-300-microvm"
    echo ""
    echo "make microvm-stop"
    echo "rm -rf result-tap"
    echo "make microvm-start-tap"
    echo ""
}

main "$@"
