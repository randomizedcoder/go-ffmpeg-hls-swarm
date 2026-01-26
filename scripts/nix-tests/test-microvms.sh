#!/usr/bin/env bash
# Test MicroVM builds (Linux only, requires KVM)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

SYSTEM=$(get_system)
readonly SYSTEM

log_info "Testing MicroVM builds for $SYSTEM"

# Check if we're on Linux
if ! is_linux; then
    log_warn "MicroVM tests require Linux"
    test_skip "all-microvms" "Not on Linux"
    print_summary
    exit 0
fi

# Check if KVM is available
if ! has_kvm; then
    log_warn "KVM not available (required for MicroVMs)"
    log_info "Enable KVM: sudo modprobe kvm_intel (or kvm_amd)"
    test_skip "all-microvms" "KVM not available"
    print_summary
    exit 0
fi

log_info "KVM is available, testing MicroVM builds..."
log_info "This may take a while (~10-15 minutes)..."
echo ""

# Test-origin MicroVM profiles
readonly MICROVM_PROFILES=(
    "test-origin-vm"
    "test-origin-vm-low-latency"
    "test-origin-vm-stress"
    "test-origin-vm-logged"
    "test-origin-vm-debug"
    "test-origin-vm-tap"
    "test-origin-vm-tap-logged"
)

for profile in "${MICROVM_PROFILES[@]}"; do
    log_test "Building $profile..."
    # Some MicroVM profiles may be null on certain systems, so we check for that
    if nix eval ".#packages.$SYSTEM.$profile" --apply 'x: if x == null then "null" else "ok"' 2>&1 | grep -q "null"; then
        test_skip "$profile" "Not available on this system"
    elif nix build ".#packages.$SYSTEM.$profile" --no-link 2>&1; then
        test_pass "$profile"
    else
        test_fail "$profile" "Build failed"
    fi
done

print_summary
