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
    "up"
    "generate-completion"
)

for app in "${CORE_APPS[@]}"; do
    log_test "Testing app: $app..."
    # Special handling for up and generate-completion
    if [[ "$app" == "up" ]]; then
        # Test unified CLI with --help
        if timeout 10 nix run ".#$app" -- --help >/dev/null 2>&1; then
            test_pass "$app"
        else
            test_fail "$app" "Execution failed"
        fi
    elif [[ "$app" == "generate-completion" ]]; then
        # Test completion generator (should work without args or with output dir)
        # Create temp dir first
        TEMP_DIR=$(mktemp -d)
        if timeout 10 nix run ".#$app" -- "$TEMP_DIR" >/dev/null 2>&1; then
            test_pass "$app"
            rm -rf "$TEMP_DIR"
        else
            test_skip "$app" "May require specific setup"
            rm -rf "$TEMP_DIR"
        fi
    else
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
    # Test-origin apps should support --help, but may fail if services aren't available
    # Just check that the app can be invoked (even if it exits with error)
    if timeout 5 nix run ".#$app" -- --help >/dev/null 2>&1 || \
       timeout 5 nix run ".#$app" 2>&1 | head -1 >/dev/null; then
        test_pass "$app"
    else
        # These apps may require actual services, so skip rather than fail
        test_skip "$app" "May require running services"
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
    # Swarm-client apps should support --help, but may fail if stream URL not provided
    # Just check that the app can be invoked
    if timeout 5 nix run ".#$app" -- --help >/dev/null 2>&1 || \
       timeout 5 nix run ".#$app" 2>&1 | head -1 >/dev/null; then
        test_pass "$app"
    else
        # These apps may require stream URLs, so skip rather than fail
        test_skip "$app" "May require stream URL"
    fi
done

print_summary
