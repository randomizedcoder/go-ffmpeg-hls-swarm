#!/usr/bin/env bash
# Reset MicroVM environment to clean state
#
# This script:
#   1. Stops any running MicroVM instances
#   2. Tears down TAP/bridge networking
#   3. Cleans up build artifacts (optional)
#
# Usage:
#   ./scripts/microvm/reset.sh           # Reset VM and networking
#   ./scripts/microvm/reset.sh --full    # Also remove build artifacts
#   make microvm-reset                   # Via Makefile
#   make microvm-reset-full              # Full reset via Makefile

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info() { echo -e "${CYAN}[INFO]${NC} $*"; }
log_success() { echo -e "${GREEN}[OK]${NC} $*"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

# Parse arguments
FULL_RESET=false
for arg in "$@"; do
    case "$arg" in
        --full|-f)
            FULL_RESET=true
            ;;
    esac
done

# ═══════════════════════════════════════════════════════════════════════════════
# Stop VM
# ═══════════════════════════════════════════════════════════════════════════════
stop_vm() {
    log_info "Stopping MicroVM..."

    # Check for running QEMU/MicroVM processes (matches both naming conventions)
    local qemu_pids
    qemu_pids=$(pgrep -f 'qemu.*hls-origin|microvm@hls-origin' 2>/dev/null || true)

    if [ -n "$qemu_pids" ]; then
        log_info "Found running MicroVM process(es): $qemu_pids"

        # Graceful shutdown
        echo "$qemu_pids" | xargs -r kill 2>/dev/null || true
        sleep 2

        # Force kill if still running
        qemu_pids=$(pgrep -f 'qemu.*hls-origin|microvm@hls-origin' 2>/dev/null || true)
        if [ -n "$qemu_pids" ]; then
            log_warn "Processes still running, sending SIGKILL..."
            echo "$qemu_pids" | xargs -r kill -9 2>/dev/null || true
            sleep 1
        fi

        log_success "MicroVM stopped"
    else
        log_info "No MicroVM processes found"
    fi

    # Clean up PID file
    rm -f /tmp/microvm-origin.pid 2>/dev/null || true
}

# ═══════════════════════════════════════════════════════════════════════════════
# Teardown networking
# ═══════════════════════════════════════════════════════════════════════════════
teardown_network() {
    log_info "Tearing down network configuration..."

    local BRIDGE="hlsbr0"
    local TAP="hlstap0"

    # Remove nftables rules
    if sudo nft list table ip hls_nat &>/dev/null; then
        sudo nft delete table ip hls_nat
        log_success "Removed nftables table: hls_nat"
    fi

    if sudo nft list table ip hls_filter &>/dev/null; then
        sudo nft delete table ip hls_filter
        log_success "Removed nftables table: hls_filter"
    fi

    # Remove TAP device
    if ip link show "$TAP" &>/dev/null; then
        sudo ip link set "$TAP" down 2>/dev/null || true
        sudo ip link delete "$TAP"
        log_success "Removed TAP device: $TAP"
    fi

    # Remove bridge (only if empty)
    if ip link show "$BRIDGE" &>/dev/null; then
        local attached
        attached=$(ip link show master "$BRIDGE" 2>/dev/null | wc -l)

        if [[ "$attached" -eq 0 ]]; then
            sudo ip link set "$BRIDGE" down 2>/dev/null || true
            sudo ip link delete "$BRIDGE"
            log_success "Removed bridge: $BRIDGE"
        else
            log_warn "Bridge $BRIDGE has attached interfaces, keeping"
        fi
    fi

    # Clear connection tracking
    if command -v conntrack &>/dev/null; then
        sudo conntrack -D -s 10.177.0.0/24 2>/dev/null || true
        sudo conntrack -D -d 10.177.0.10 2>/dev/null || true
    fi

    log_success "Network teardown complete"
}

# ═══════════════════════════════════════════════════════════════════════════════
# Clean build artifacts
# ═══════════════════════════════════════════════════════════════════════════════
clean_artifacts() {
    log_info "Cleaning build artifacts..."

    cd "${PROJECT_ROOT}"

    # Remove Nix build results
    for link in result result-vm result-tap; do
        if [ -L "$link" ] || [ -e "$link" ]; then
            rm -f "$link"
            log_success "Removed: $link"
        fi
    done

    # Remove any lingering log files
    rm -f /tmp/microvm-origin.log 2>/dev/null || true

    log_success "Build artifacts cleaned"
}

# ═══════════════════════════════════════════════════════════════════════════════
# Main
# ═══════════════════════════════════════════════════════════════════════════════
main() {
    echo ""
    echo "╔════════════════════════════════════════════════════════════════════════╗"
    echo "║              MicroVM Environment Reset                                 ║"
    if [ "$FULL_RESET" = true ]; then
    echo "║              Mode: FULL (includes build artifacts)                     ║"
    else
    echo "║              Mode: Standard (VM + networking)                          ║"
    fi
    echo "╚════════════════════════════════════════════════════════════════════════╝"
    echo ""

    stop_vm
    echo ""
    teardown_network

    if [ "$FULL_RESET" = true ]; then
        echo ""
        clean_artifacts
    fi

    echo ""
    echo "╔════════════════════════════════════════════════════════════════════════╗"
    echo "║                    Reset Complete!                                     ║"
    echo "╠════════════════════════════════════════════════════════════════════════╣"
    echo "║  To recreate the environment:                                          ║"
    echo "║                                                                        ║"
    echo "║    make network-setup         # Setup bridge/TAP (requires sudo)       ║"
    echo "║    make microvm-start-tap     # Start VM with TAP networking           ║"
    echo "║                                                                        ║"
    echo "║  Or for user-mode networking:                                          ║"
    echo "║                                                                        ║"
    echo "║    make microvm-start         # Start VM with QEMU port forwarding     ║"
    echo "╚════════════════════════════════════════════════════════════════════════╝"
    echo ""
}

main "$@"
