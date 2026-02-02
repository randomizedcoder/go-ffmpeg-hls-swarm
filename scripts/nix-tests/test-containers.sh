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

# Main binary container (all platforms)
log_test "Building go-ffmpeg-hls-swarm-container..."
if nix build ".#packages.$SYSTEM.go-ffmpeg-hls-swarm-container" --no-link 2>&1; then
    if nix build ".#packages.$SYSTEM.go-ffmpeg-hls-swarm-container" --print-out-paths >/dev/null 2>&1; then
        test_pass "go-ffmpeg-hls-swarm-container"
    else
        test_fail "go-ffmpeg-hls-swarm-container" "Container path invalid"
    fi
else
    test_fail "go-ffmpeg-hls-swarm-container" "Build failed"
fi

# Test-origin container
log_test "Building test-origin-container..."
if nix build ".#packages.$SYSTEM.test-origin-container" --no-link 2>&1; then
    if nix build ".#packages.$SYSTEM.test-origin-container" --print-out-paths >/dev/null 2>&1; then
        test_pass "test-origin-container"
    else
        test_fail "test-origin-container" "Container path invalid"
    fi
else
    test_fail "test-origin-container" "Build failed"
fi

# Enhanced container (Linux only)
if is_linux; then
    log_test "Building test-origin-container-enhanced..."
    if nix build ".#packages.$SYSTEM.test-origin-container-enhanced" --no-link 2>&1; then
        if nix build ".#packages.$SYSTEM.test-origin-container-enhanced" --print-out-paths >/dev/null 2>&1; then
            test_pass "test-origin-container-enhanced"
        else
            test_fail "test-origin-container-enhanced" "Container path invalid"
        fi
    else
        test_fail "test-origin-container-enhanced" "Build failed"
    fi
else
    test_skip "test-origin-container-enhanced" "Linux only"
fi

# Swarm-client container
log_test "Building swarm-client-container..."
if nix build ".#packages.$SYSTEM.swarm-client-container" --no-link 2>&1; then
    if nix build ".#packages.$SYSTEM.swarm-client-container" --print-out-paths >/dev/null 2>&1; then
        test_pass "swarm-client-container"
    else
        test_fail "swarm-client-container" "Container path invalid"
    fi
else
    test_fail "swarm-client-container" "Build failed"
fi

print_summary
