#!/usr/bin/env bash
# Common functions for Nix test scripts

set -euo pipefail

# Colors for output
readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly NC='\033[0m' # No Color

# Test results tracking
PASSED=0
FAILED=0
SKIPPED=0
RESULTS=()

# Logging functions
log_info() {
    echo -e "${GREEN}[INFO]${NC} $*"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $*"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $*"
}

log_test() {
    echo -e "[TEST] $*"
}

# Test result tracking
test_pass() {
    ((PASSED++)) || true  # || true prevents exit on error if PASSED is readonly (shouldn't happen, but safe)
    RESULTS+=("PASS: $1")
    log_info "✓ $1"
}

test_fail() {
    ((FAILED++)) || true  # || true prevents exit on error
    RESULTS+=("FAIL: $1 - $2")
    log_error "✗ $1: $2"
}

test_skip() {
    ((SKIPPED++)) || true  # || true prevents exit on error
    RESULTS+=("SKIP: $1 - $2")
    log_warn "⊘ $1: $2"
}

# Print summary
print_summary() {
    echo ""
    echo "════════════════════════════════════════════════════════════"
    echo "Test Summary"
    echo "════════════════════════════════════════════════════════════"
    echo "Passed:  $PASSED"
    echo "Failed:  $FAILED"
    echo "Skipped: $SKIPPED"
    echo ""
    
    if [[ $FAILED -gt 0 ]]; then
        echo "Failed tests:"
        for result in "${RESULTS[@]}"; do
            if [[ "$result" == FAIL:* ]]; then
                echo "  $result"
            fi
        done
        return 1
    fi
    
    return 0
}

# Get system from nix
get_system() {
    nix eval --impure --expr 'builtins.currentSystem' --raw
}

# Check if running on Linux
is_linux() {
    [[ "$(uname)" == "Linux" ]]
}

# Check if KVM is available (for MicroVM tests)
has_kvm() {
    if is_linux && [[ -e /dev/kvm ]] && [[ -r /dev/kvm ]] && [[ -w /dev/kvm ]]; then
        return 0
    else
        return 1
    fi
}
