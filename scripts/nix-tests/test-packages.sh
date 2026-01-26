#!/usr/bin/env bash
# Test all package builds

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

SYSTEM=$(get_system)
readonly SYSTEM

log_info "Testing package builds for $SYSTEM"
log_info "This may take several minutes..."
echo ""

# Core package
log_test "Building go-ffmpeg-hls-swarm..."
if nix build ".#packages.$SYSTEM.go-ffmpeg-hls-swarm" --no-link 2>&1; then
    test_pass "go-ffmpeg-hls-swarm"
else
    test_fail "go-ffmpeg-hls-swarm" "Build failed"
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
    log_test "Building $profile..."
    if nix build ".#packages.$SYSTEM.$profile" --no-link 2>&1; then
        test_pass "$profile"
    else
        test_fail "$profile" "Build failed"
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
    log_test "Building $profile..."
    if nix build ".#packages.$SYSTEM.$profile" --no-link 2>&1; then
        test_pass "$profile"
    else
        test_fail "$profile" "Build failed"
    fi
done

print_summary
