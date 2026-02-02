#!/usr/bin/env bash
# Test all profile accessibility

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck source=scripts/nix-tests/lib.sh
source "$SCRIPT_DIR/lib.sh"

SYSTEM=$(get_system)
readonly SYSTEM

log_info "Testing profile accessibility for $SYSTEM"
log_info "This should be fast (~30 seconds)..."
echo ""

# Helper function to test if a package attribute exists and can be evaluated
test_package_exists() {
    local package_name=$1
    local attr_path=".#packages.$SYSTEM.$package_name"

    # Just check if the command succeeds
    # Allow stderr through (warnings are OK) but suppress stdout
    if nix eval "$attr_path" >/dev/null 2>&1; then
        return 0
    else
        return 1
    fi
}

# Core package
log_test "Checking go-ffmpeg-hls-swarm..."
if test_package_exists "go-ffmpeg-hls-swarm"; then
    test_pass "go-ffmpeg-hls-swarm"
else
    test_fail "go-ffmpeg-hls-swarm" "Package not found or evaluation failed"
fi

# Test-origin profiles
readonly TEST_ORIGIN_PROFILES=(
    "test-origin"
    "test-origin-low-latency"
    "test-origin-4k-abr"
    "test-origin-stress"
    "test-origin-logged"
    "test-origin-debug"
)

for profile in "${TEST_ORIGIN_PROFILES[@]}"; do
    log_test "Checking $profile..."
    if test_package_exists "$profile"; then
        test_pass "$profile"
    else
        test_fail "$profile" "Package not found or evaluation failed"
    fi
done

# Swarm-client profiles
readonly CLIENT_PROFILES=(
    "swarm-client"
    "swarm-client-stress"
    "swarm-client-gentle"
    "swarm-client-burst"
    "swarm-client-extreme"
)

for profile in "${CLIENT_PROFILES[@]}"; do
    log_test "Checking $profile..."
    if test_package_exists "$profile"; then
        test_pass "$profile"
    else
        test_fail "$profile" "Package not found or evaluation failed"
    fi
done

print_summary
