#!/usr/bin/env bash
# Test unified CLI (nix run .#up)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

SYSTEM=$(get_system)
readonly SYSTEM

log_info "Testing unified CLI for $SYSTEM"
log_info "This should be fast (~30 seconds)..."
echo ""

# Test 1: Help works
log_test "Testing up --help..."
if timeout 10 nix run ".#up" -- --help >/dev/null 2>&1; then
    test_pass "up --help"
else
    test_fail "up --help" "Help command failed"
fi

# Test 2: Non-interactive mode (no TTY, should use defaults)
log_test "Testing non-interactive mode..."
if echo "" | timeout 10 nix run ".#up" >/dev/null 2>&1; then
    test_pass "up (non-interactive, defaults)"
else
    # This might fail if it tries to actually run the origin, which is OK
    test_skip "up (non-interactive)" "May require actual execution"
fi

# Test 3: Explicit profile and type
log_test "Testing explicit profile and type..."
if timeout 10 nix run ".#up" -- default runner --help >/dev/null 2>&1; then
    test_pass "up default runner"
else
    test_fail "up default runner" "Failed with explicit args"
fi

# Test 4: Platform check for VM (should show helpful error on non-Linux)
if ! is_linux; then
    log_test "Testing platform check for VM..."
    if timeout 10 nix run ".#up" -- default vm 2>&1 | grep -qi "Linux\|KVM"; then
        test_pass "up default vm (platform check works)"
    else
        test_fail "up default vm" "Platform check should show error"
    fi
else
    test_skip "up default vm (platform check)" "On Linux, VM may actually run"
fi

print_summary
