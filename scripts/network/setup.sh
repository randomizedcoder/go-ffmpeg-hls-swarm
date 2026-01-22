#!/usr/bin/env bash
# Setup high-performance TAP networking for HLS MicroVM
# See: docs/MICROVM_NETWORKING.md
#
# Creates:
#   - Bridge: hlsbr0 (10.177.0.1/24)
#   - TAP: hlstap0 (attached to bridge, with multi_queue for high performance)
#   - nftables: hls_nat, hls_filter tables
#
# Usage: ./scripts/network/setup.sh
#        make network-setup

set -euo pipefail

# ═══════════════════════════════════════════════════════════════════════════════
# Configuration - uses unique identifiers to avoid conflicts
# ═══════════════════════════════════════════════════════════════════════════════
BRIDGE="hlsbr0"
TAP="hlstap0"
SUBNET="10.177.0.0/24"
GATEWAY="10.177.0.1"
VM_IP="10.177.0.10"
CURRENT_USER="${USER:-$(whoami)}"

# Ports to forward to VM (matching docs/PORTS.md)
# Note: 17022 is NOT forwarded - it's QEMU's serial console on the HOST
HTTP_PORT="17080"
METRICS_PORT="17113"

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
# Check prerequisites
# ═══════════════════════════════════════════════════════════════════════════════
check_prerequisites() {
    log_info "Checking prerequisites..."

    # Check for root/sudo
    if [[ $EUID -ne 0 ]]; then
        if ! command -v sudo &>/dev/null; then
            log_error "This script requires root privileges. Please run with sudo."
            exit 1
        fi
    fi

    # Check for required commands
    local missing=()
    for cmd in ip nft modprobe; do
        if ! command -v "$cmd" &>/dev/null; then
            missing+=("$cmd")
        fi
    done

    if [[ ${#missing[@]} -gt 0 ]]; then
        log_error "Missing required commands: ${missing[*]}"
        exit 1
    fi

    log_success "Prerequisites met"
}

# ═══════════════════════════════════════════════════════════════════════════════
# Load kernel modules
# ═══════════════════════════════════════════════════════════════════════════════
load_modules() {
    log_info "Loading kernel modules..."

    local modules=("tun" "vhost_net" "bridge")
    for mod in "${modules[@]}"; do
        if ! lsmod | grep -q "^${mod}"; then
            if sudo modprobe "$mod" 2>/dev/null; then
                log_success "Loaded module: $mod"
            else
                log_warn "Could not load module: $mod (may already be built-in)"
            fi
        else
            log_success "Module already loaded: $mod"
        fi
    done
}

# ═══════════════════════════════════════════════════════════════════════════════
# Create bridge
# ═══════════════════════════════════════════════════════════════════════════════
create_bridge() {
    log_info "Setting up bridge $BRIDGE..."

    if ip link show "$BRIDGE" &>/dev/null; then
        log_success "Bridge $BRIDGE already exists"
    else
        sudo ip link add name "$BRIDGE" type bridge
        sudo ip addr add "${GATEWAY}/24" dev "$BRIDGE"
        sudo ip link set "$BRIDGE" up
        log_success "Created bridge $BRIDGE with gateway $GATEWAY"
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Create TAP device with multiqueue support
# ═══════════════════════════════════════════════════════════════════════════════
create_tap() {
    log_info "Setting up TAP device $TAP with multiqueue..."

    if ip link show "$TAP" &>/dev/null; then
        # Check if existing TAP has multiqueue
        log_warn "TAP device $TAP already exists"
        log_warn "If multiqueue issues occur, run: make network-teardown && make network-setup"
    else
        # Create TAP with multi_queue for parallel packet processing
        # This enables QEMU to use queues=N matching vCPU count
        sudo ip tuntap add dev "$TAP" mode tap multi_queue user "$CURRENT_USER"
        sudo ip link set "$TAP" master "$BRIDGE"
        sudo ip link set "$TAP" up
        log_success "Created TAP device $TAP with multi_queue (owner: $CURRENT_USER)"
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Enable IP forwarding
# ═══════════════════════════════════════════════════════════════════════════════
enable_forwarding() {
    log_info "Enabling IP forwarding..."

    local current
    current=$(cat /proc/sys/net/ipv4/ip_forward)
    if [[ "$current" != "1" ]]; then
        sudo sysctl -q -w net.ipv4.ip_forward=1
        log_success "IP forwarding enabled"
    else
        log_success "IP forwarding already enabled"
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Configure nftables
# ═══════════════════════════════════════════════════════════════════════════════
configure_nftables() {
    log_info "Configuring nftables..."

    # Remove existing tables if present (clean slate)
    sudo nft delete table ip hls_nat 2>/dev/null || true
    sudo nft delete table ip hls_filter 2>/dev/null || true

    # Create NAT table with port forwarding
    sudo nft -f - <<EOF
# HLS MicroVM NAT table
# Provides: masquerading for VM internet access, port forwarding for localhost access
table ip hls_nat {
    chain postrouting {
        type nat hook postrouting priority srcnat; policy accept;
        ip saddr ${SUBNET} oifname != "${BRIDGE}" masquerade
    }

    chain prerouting {
        type nat hook prerouting priority dstnat; policy accept;
        tcp dport ${HTTP_PORT} dnat to ${VM_IP}:${HTTP_PORT}
        tcp dport ${METRICS_PORT} dnat to ${VM_IP}:${METRICS_PORT}
    }

    chain output {
        type nat hook output priority dstnat; policy accept;
        ip daddr 127.0.0.1 tcp dport ${HTTP_PORT} dnat to ${VM_IP}:${HTTP_PORT}
        ip daddr 127.0.0.1 tcp dport ${METRICS_PORT} dnat to ${VM_IP}:${METRICS_PORT}
    }
}

# HLS MicroVM filter table
# Allows forwarding to/from our bridge
table ip hls_filter {
    chain forward {
        type filter hook forward priority filter - 1; policy accept;
        iifname "${BRIDGE}" accept
        oifname "${BRIDGE}" ct state related,established accept
        oifname "${BRIDGE}" ip daddr ${SUBNET} accept
    }
}
EOF

    log_success "nftables configured (tables: hls_nat, hls_filter)"
}

# ═══════════════════════════════════════════════════════════════════════════════
# Enable vhost-net for performance
# ═══════════════════════════════════════════════════════════════════════════════
enable_vhost() {
    log_info "Checking vhost-net..."

    if [[ -c /dev/vhost-net ]]; then
        # Make vhost-net accessible (needed for non-root QEMU)
        sudo chmod 666 /dev/vhost-net
        log_success "vhost-net enabled (/dev/vhost-net)"
    else
        log_warn "vhost-net not available - performance may be reduced"
        log_warn "Run: sudo modprobe vhost_net"
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Print summary
# ═══════════════════════════════════════════════════════════════════════════════
print_summary() {
    echo ""
    echo "╔════════════════════════════════════════════════════════════════════════╗"
    echo "║                    HLS Network Setup Complete                          ║"
    echo "╠════════════════════════════════════════════════════════════════════════╣"
    echo "║  Bridge:     ${BRIDGE} (${GATEWAY})"
    echo "║  TAP:        ${TAP}"
    echo "║  VM IP:      ${VM_IP}"
    echo "║  Subnet:     ${SUBNET}"
    echo "╠════════════════════════════════════════════════════════════════════════╣"
    echo "║  Port Forwarding (localhost -> VM):                                    ║"
    echo "║    :${HTTP_PORT}  -> ${VM_IP}:${HTTP_PORT}  (HLS Origin)"
    echo "║    :${METRICS_PORT}  -> ${VM_IP}:${METRICS_PORT}  (Prometheus)"
    echo "║  QEMU Console: localhost:17022 (HOST, not forwarded)                 ║"
    echo "╚════════════════════════════════════════════════════════════════════════╝"
    echo ""
    echo "Next steps:"
    echo "  make microvm-start-tap    # Start VM with TAP networking"
    echo "  make network-check        # Verify configuration"
    echo "  make network-teardown     # Remove when done"
    echo ""
}

# ═══════════════════════════════════════════════════════════════════════════════
# Main
# ═══════════════════════════════════════════════════════════════════════════════
main() {
    echo ""
    echo "╔════════════════════════════════════════════════════════════════════════╗"
    echo "║              HLS MicroVM Network Setup                                 ║"
    echo "╚════════════════════════════════════════════════════════════════════════╝"
    echo ""

    check_prerequisites
    load_modules
    create_bridge
    create_tap
    enable_forwarding
    configure_nftables
    enable_vhost
    print_summary
}

main "$@"
