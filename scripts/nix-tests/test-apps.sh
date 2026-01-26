#!/usr/bin/env bash
# Test app execution

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

SYSTEM=$(get_system)
readonly SYSTEM

log_info "Testing app execution for $SYSTEM"
log_info "This should be fast (~1-2 minutes)..."
echo ""

# Core apps
readonly CORE_APPS=(
    "welcome"
    "build"
    "run"
)

for app in "${CORE_APPS[@]}"; do
    log_test "Testing app: $app..."
    # Try to run with --help or just run it briefly
    if timeout 10 nix run ".#$app" -- --help >/dev/null 2>&1 || \
       timeout 10 nix run ".#$app" >/dev/null 2>&1; then
        test_pass "$app"
    else
        # Some apps might not support --help, check if they at least start
        if timeout 5 nix run ".#$app" 2>&1 | head -1 >/dev/null; then
            test_pass "$app"
        else
            test_fail "$app" "Execution failed"
        fi
    fi
done

# Test-origin apps
readonly TEST_ORIGIN_APPS=(
    "test-origin"
    "test-origin-low-latency"
    "test-origin-4k-abr"
    "test-origin-stress"
    "test-origin-logged"
    "test-origin-debug"
)

for app in "${TEST_ORIGIN_APPS[@]}"; do
    log_test "Testing app: $app..."
    # Test-origin apps should support --help
    if timeout 10 nix run ".#$app" -- --help >/dev/null 2>&1; then
        test_pass "$app"
    else
        test_fail "$app" "Execution failed"
    fi
done

# Swarm-client apps
readonly CLIENT_APPS=(
    "swarm-client"
    "swarm-client-stress"
    "swarm-client-gentle"
    "swarm-client-burst"
    "swarm-client-extreme"
)

for app in "${CLIENT_APPS[@]}"; do
    log_test "Testing app: $app..."
    # Swarm-client apps should support --help
    if timeout 10 nix run ".#$app" -- --help >/dev/null 2>&1; then
        test_pass "$app"
    else
        test_fail "$app" "Execution failed"
    fi
done

print_summary
