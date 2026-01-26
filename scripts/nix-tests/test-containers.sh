#!/usr/bin/env bash
# Test container builds

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

SYSTEM=$(get_system)
readonly SYSTEM

log_info "Testing container builds for $SYSTEM"
log_info "This may take several minutes..."
echo ""

# Test-origin container
log_test "Building test-origin-container..."
if nix build ".#packages.$SYSTEM.test-origin-container" --no-link 2>&1; then
    # Verify it's a valid container image
    if nix build ".#packages.$SYSTEM.test-origin-container" --print-out-paths >/dev/null 2>&1; then
        test_pass "test-origin-container"
    else
        test_fail "test-origin-container" "Container path invalid"
    fi
else
    test_fail "test-origin-container" "Build failed"
fi

# Swarm-client container
log_test "Building swarm-client-container..."
if nix build ".#packages.$SYSTEM.swarm-client-container" --no-link 2>&1; then
    # Verify it's a valid container image
    if nix build ".#packages.$SYSTEM.swarm-client-container" --print-out-paths >/dev/null 2>&1; then
        test_pass "swarm-client-container"
    else
        test_fail "swarm-client-container" "Container path invalid"
    fi
else
    test_fail "swarm-client-container" "Build failed"
fi

print_summary
