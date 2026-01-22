#!/usr/bin/env bash
# Verify HLS MicroVM network configuration
# See: docs/MICROVM_NETWORKING.md
#
# Checks:
#   - Kernel modules loaded
#   - Bridge exists and has correct IP
#   - TAP device exists and attached
#   - nftables rules configured
#   - IP forwarding enabled
#   - vhost-net available
#   - VM connectivity (if running)
#
# Usage: ./scripts/network/check.sh
#        make network-check

set -euo pipefail

# ═══════════════════════════════════════════════════════════════════════════════
# Configuration - must match setup.sh
# ═══════════════════════════════════════════════════════════════════════════════
BRIDGE="hlsbr0"
TAP="hlstap0"
GATEWAY="10.177.0.1"
VM_IP="10.177.0.10"
HTTP_PORT="17080"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

# Counters
PASS=0
FAIL=0
WARN=0

check_pass() { echo -e "${GREEN}✓${NC} $*"; ((PASS++)) || true; }
check_fail() { echo -e "${RED}✗${NC} $*"; ((FAIL++)) || true; }
check_warn() { echo -e "${YELLOW}⚠${NC} $*"; ((WARN++)) || true; }
check_info() { echo -e "${CYAN}•${NC} $*"; }

# ═══════════════════════════════════════════════════════════════════════════════
# Check kernel modules
# ═══════════════════════════════════════════════════════════════════════════════
check_modules() {
    echo ""
    echo "Kernel Modules:"

    local modules=("tun" "vhost_net" "bridge")
    for mod in "${modules[@]}"; do
        if lsmod | grep -q "^${mod}"; then
            check_pass "$mod loaded"
        elif [[ -d "/sys/module/$mod" ]]; then
            check_pass "$mod built-in"
        else
            check_warn "$mod not loaded (run: sudo modprobe $mod)"
        fi
    done
}

# ═══════════════════════════════════════════════════════════════════════════════
# Check bridge
# ═══════════════════════════════════════════════════════════════════════════════
check_bridge() {
    echo ""
    echo "Bridge Configuration:"

    if ip link show "$BRIDGE" &>/dev/null; then
        check_pass "Bridge $BRIDGE exists"

        # Check if administratively UP (flags contain UP)
        # Note: "state DOWN" is normal when no carrier (no VM connected)
        if ip link show "$BRIDGE" | grep -q ",UP"; then
            check_pass "Bridge $BRIDGE is UP"
            # Check for carrier
            if ip link show "$BRIDGE" | grep -q "NO-CARRIER"; then
                check_info "Bridge has no carrier (VM not connected yet)"
            fi
        else
            check_fail "Bridge $BRIDGE is administratively DOWN"
        fi

        # Check IP address
        if ip addr show "$BRIDGE" | grep -q "$GATEWAY"; then
            check_pass "Bridge has IP $GATEWAY"
        else
            check_fail "Bridge missing IP $GATEWAY"
        fi
    else
        check_fail "Bridge $BRIDGE does not exist"
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Check TAP device
# ═══════════════════════════════════════════════════════════════════════════════
check_tap() {
    echo ""
    echo "TAP Device:"

    if ip link show "$TAP" &>/dev/null; then
        check_pass "TAP device $TAP exists"

        # Check if attached to bridge
        if ip link show "$TAP" | grep -q "master $BRIDGE"; then
            check_pass "TAP attached to $BRIDGE"
        else
            check_fail "TAP not attached to bridge"
        fi

        # Check if UP
        if ip link show "$TAP" | grep -q "state UP"; then
            check_pass "TAP is UP"
        else
            check_warn "TAP is DOWN (will come up when VM starts)"
        fi
    else
        check_fail "TAP device $TAP does not exist"
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Check nftables
# ═══════════════════════════════════════════════════════════════════════════════
check_nftables() {
    echo ""
    echo "nftables Rules:"

    if sudo nft list table ip hls_nat &>/dev/null; then
        check_pass "Table hls_nat exists"

        # Check for key rules
        local nat_rules
        nat_rules=$(sudo nft list table ip hls_nat 2>/dev/null)

        if echo "$nat_rules" | grep -q "masquerade"; then
            check_pass "NAT masquerade rule present"
        else
            check_fail "NAT masquerade rule missing"
        fi

        if echo "$nat_rules" | grep -q "dnat to $VM_IP"; then
            check_pass "Port forwarding rules present"
        else
            check_fail "Port forwarding rules missing"
        fi
    else
        check_fail "Table hls_nat does not exist"
    fi

    if sudo nft list table ip hls_filter &>/dev/null; then
        check_pass "Table hls_filter exists"
    else
        check_fail "Table hls_filter does not exist"
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Check IP forwarding
# ═══════════════════════════════════════════════════════════════════════════════
check_forwarding() {
    echo ""
    echo "System Configuration:"

    local forwarding
    forwarding=$(cat /proc/sys/net/ipv4/ip_forward)
    if [[ "$forwarding" == "1" ]]; then
        check_pass "IP forwarding enabled"
    else
        check_fail "IP forwarding disabled (run: sudo sysctl -w net.ipv4.ip_forward=1)"
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Check vhost-net
# ═══════════════════════════════════════════════════════════════════════════════
check_vhost() {
    echo ""
    echo "Performance Features:"

    if [[ -c /dev/vhost-net ]]; then
        check_pass "vhost-net device exists"

        # Check permissions
        if [[ -r /dev/vhost-net ]] && [[ -w /dev/vhost-net ]]; then
            check_pass "vhost-net is accessible"
        else
            check_warn "vhost-net not accessible (run: sudo chmod 666 /dev/vhost-net)"
        fi
    else
        check_warn "vhost-net not available (performance may be reduced)"
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Check VM connectivity
# ═══════════════════════════════════════════════════════════════════════════════
check_vm() {
    echo ""
    echo "VM Connectivity:"

    # Try to ping VM
    if ping -c 1 -W 1 "$VM_IP" &>/dev/null; then
        check_pass "VM reachable at $VM_IP"

        # Try health endpoint
        if curl -sf "http://${VM_IP}:${HTTP_PORT}/health" &>/dev/null; then
            check_pass "HLS origin responding at ${VM_IP}:${HTTP_PORT}"
        else
            check_info "HLS origin not responding (VM may not be running)"
        fi

        # Try localhost port forward
        if curl -sf "http://localhost:${HTTP_PORT}/health" &>/dev/null; then
            check_pass "Port forwarding working (localhost:${HTTP_PORT})"
        else
            check_warn "Port forwarding not working (check nftables)"
        fi
    else
        check_info "VM not reachable at $VM_IP (VM may not be running)"
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Print summary
# ═══════════════════════════════════════════════════════════════════════════════
print_summary() {
    echo ""
    echo "════════════════════════════════════════════════════════════════════════"
    echo -e "Summary: ${GREEN}${PASS} passed${NC}, ${RED}${FAIL} failed${NC}, ${YELLOW}${WARN} warnings${NC}"
    echo "════════════════════════════════════════════════════════════════════════"

    if [[ $FAIL -gt 0 ]]; then
        echo ""
        echo "To fix issues, run: make network-setup"
        exit 1
    elif [[ $WARN -gt 0 ]]; then
        echo ""
        echo "Network is functional but has warnings."
        exit 0
    else
        echo ""
        echo "Network configuration is complete and ready."
        exit 0
    fi
}

# ═══════════════════════════════════════════════════════════════════════════════
# Main
# ═══════════════════════════════════════════════════════════════════════════════
main() {
    echo ""
    echo "╔════════════════════════════════════════════════════════════════════════╗"
    echo "║              HLS MicroVM Network Check                                 ║"
    echo "╚════════════════════════════════════════════════════════════════════════╝"

    check_modules
    check_bridge
    check_tap
    check_nftables
    check_forwarding
    check_vhost
    check_vm
    print_summary
}

main "$@"
