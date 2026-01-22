#!/usr/bin/env bash
# Stop the HLS Origin MicroVM
# Usage: ./scripts/microvm/stop.sh

set -euo pipefail

PID_FILE="/tmp/microvm-origin.pid"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info() { echo -e "${CYAN}[INFO]${NC} $*"; }
log_success() { echo -e "${GREEN}[OK]${NC} $*"; }

log_info "Stopping MicroVM..."

# Stop by PID file
if [ -f "${PID_FILE}" ]; then
    pid=$(cat "${PID_FILE}")
    if kill -0 "${pid}" 2>/dev/null; then
        log_info "Killing MicroVM process (PID: ${pid})"
        kill "${pid}" 2>/dev/null || true
        sleep 2
        # Force kill if still running
        if kill -0 "${pid}" 2>/dev/null; then
            log_info "Force killing..."
            kill -9 "${pid}" 2>/dev/null || true
        fi
    fi
    rm -f "${PID_FILE}"
fi

# Also kill any orphaned qemu processes
pkill -f "qemu.*hls-origin" 2>/dev/null || true

log_success "MicroVM stopped"
