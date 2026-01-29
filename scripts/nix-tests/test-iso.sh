#!/usr/bin/env bash
# Test ISO image builds (Linux only)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

SYSTEM=$(get_system)
readonly SYSTEM

log_info "Testing ISO image builds for $SYSTEM"
log_info "This may take a while (~10-15 minutes)..."
echo ""

if ! is_linux; then
    log_warn "ISO builds are Linux-only, skipping on $(uname)"
    test_skip "test-origin-iso" "Linux only"
    print_summary
    exit 0
fi

# Check KVM permissions (ISO builds may benefit from KVM but don't require it)
if has_kvm; then
    log_info "KVM is available, ISO builds will be faster"
else
    log_warn "KVM not available, ISO builds will be slower"
fi

# Test ISO build
log_test "Building test-origin-iso..."
if nix build ".#packages.$SYSTEM.test-origin-iso" --no-link 2>&1; then
    # Verify it's a valid ISO file
    # ISO outputs are directories containing iso/nixos-*.iso
    ISO_DIR=$(nix build ".#packages.$SYSTEM.test-origin-iso" --print-out-paths 2>/dev/null || echo "")
    if [[ -n "$ISO_DIR" ]] && [[ -d "$ISO_DIR" ]]; then
        # Find the actual ISO file inside the directory
        ISO_FILE=$(find "$ISO_DIR" -name "*.iso" -type f | head -1)
        if [[ -n "$ISO_FILE" ]] && [[ -f "$ISO_FILE" ]]; then
            test_pass "test-origin-iso"
            log_info "ISO file: $ISO_FILE"
            log_info "ISO directory: $ISO_DIR"
        else
            test_fail "test-origin-iso" "ISO file not found in directory"
        fi
    else
        test_fail "test-origin-iso" "ISO directory invalid"
    fi
else
    test_fail "test-origin-iso" "Build failed"
fi

print_summary
