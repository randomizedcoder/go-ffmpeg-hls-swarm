#!/usr/bin/env bash
# Teardown HLS MicroVM networking
# See: docs/MICROVM_NETWORKING.md
#
# Removes:
#   - nftables tables: hls_nat, hls_filter
#   - TAP device: hlstap0
#   - Bridge: hlsbr0 (if no other devices attached)
#
# Usage: ./scripts/network/teardown.sh
#        make network-teardown

set -euo pipefail

# ═══════════════════════════════════════════════════════════════════════════════
# Configuration - must match setup.sh
# ═══════════════════════════════════════════════════════════════════════════════
BRIDGE="hlsbr0"
TAP="hlstap0"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info() { echo -e "${CYAN}[INFO]${NC} $*"; }
log_success() { echo -e "${GREEN}[OK]${NC} $*"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

# ═══════════════════════════════════════════════════════════════════════════════
# Remove nftables rules
# ═══════════════════════════════════════════════════════════════════════════════
remove_nftables() {
    log_info "Removing nftables rules..."

    if sudo nft list table ip hls_nat &>/dev/null; then
        sudo nft delete table ip hls_nat
        log_success "Removed table: hls_nat"
    else
        log_info "Table hls_nat not found (already removed)"
    fi

    if sudo nft list table ip hls_filter &>/dev/null; then
        sudo nft delete table ip hls_filter
        log_success "Removed table: hls_filter"
    else
        log_info "Table hls_filter not found (already removed)"
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Remove TAP device
# ═══════════════════════════════════════════════════════════════════════════════
remove_tap() {
    log_info "Removing TAP device..."

    if ip link show "$TAP" &>/dev/null; then
        sudo ip link set "$TAP" down 2>/dev/null || true
        sudo ip link delete "$TAP"
        log_success "Removed TAP device: $TAP"
    else
        log_info "TAP device $TAP not found (already removed)"
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Remove bridge (only if empty)
# ═══════════════════════════════════════════════════════════════════════════════
remove_bridge() {
    log_info "Checking bridge..."

    if ip link show "$BRIDGE" &>/dev/null; then
        # Count interfaces attached to bridge
        local attached
        attached=$(ip link show master "$BRIDGE" 2>/dev/null | wc -l)

        if [[ "$attached" -eq 0 ]]; then
            sudo ip link set "$BRIDGE" down 2>/dev/null || true
            sudo ip link delete "$BRIDGE"
            log_success "Removed bridge: $BRIDGE"
        else
            log_warn "Bridge $BRIDGE has $attached attached interface(s), keeping"
            log_warn "Attached devices:"
            ip link show master "$BRIDGE" 2>/dev/null | grep -oP '^\d+: \K[^:@]+' || true
        fi
    else
        log_info "Bridge $BRIDGE not found (already removed)"
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Clear connection tracking (optional, helps with stale connections)
# ═══════════════════════════════════════════════════════════════════════════════
clear_conntrack() {
    if command -v conntrack &>/dev/null; then
        log_info "Clearing connection tracking entries..."
        sudo conntrack -D -s 10.177.0.0/24 2>/dev/null || true
        sudo conntrack -D -d 10.177.0.10 2>/dev/null || true
        log_success "Connection tracking cleared"
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Main
# ═══════════════════════════════════════════════════════════════════════════════
main() {
    echo ""
    echo "╔════════════════════════════════════════════════════════════════════════╗"
    echo "║              HLS MicroVM Network Teardown                              ║"
    echo "╚════════════════════════════════════════════════════════════════════════╝"
    echo ""

    remove_nftables
    remove_tap
    remove_bridge
    clear_conntrack

    echo ""
    log_success "Network teardown complete"
    echo ""
}

main "$@"
