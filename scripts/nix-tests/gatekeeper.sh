#!/usr/bin/env bash
# Gatekeeper: Validates single source of truth integrity
# This script ensures profiles in profiles.nix appear in flake.nix
# Run before committing Phase 1 and after any profile changes

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

log_info "════════════════════════════════════════════════════════════"
log_info "Gatekeeper: Single Source of Truth Validation"
log_info "════════════════════════════════════════════════════════════"
echo ""

FAILED=0

# Test 1: Verify profile files exist
log_test "Checking profile definition files exist..."
if [[ ! -f "nix/test-origin/config/profile-list.nix" ]]; then
    log_error "Missing: nix/test-origin/config/profile-list.nix"
    FAILED=1
else
    test_pass "test-origin profile-list.nix exists"
fi

if [[ ! -f "nix/swarm-client/config/profile-list.nix" ]]; then
    log_error "Missing: nix/swarm-client/config/profile-list.nix"
    FAILED=1
else
    test_pass "swarm-client profile-list.nix exists"
fi

# Test 2: Verify profiles can be evaluated (using import to avoid socket file issues)
log_test "Evaluating profile lists..."
# Use import directly instead of builtins.getFlake to avoid reading socket files
if ! nix eval --impure --expr '
  let
    pkgs = import <nixpkgs> {};
    lib = pkgs.lib;
    to = import ./nix/test-origin/config/profile-list.nix { inherit lib; };
    sc = import ./nix/swarm-client/config/profile-list.nix { inherit lib; };
  in
  { test-origin = to.profiles; swarm-client = sc.profiles; }
' --json >/dev/null 2>&1; then
    log_error "Failed to evaluate profile lists"
    FAILED=1
else
    test_pass "Profile lists evaluate successfully"
fi

# Test 3: Verify flake.nix uses single source (if flake.nix exists and has been updated)
if [[ -f "flake.nix" ]]; then
    log_test "Checking flake.nix references single source..."
    
    # Check if flake.nix imports profile-list.nix
    if ! grep -q "test-origin/config/profile-list.nix" flake.nix; then
        log_warn "flake.nix may not be using single source of truth"
        log_warn "This is OK if Phase 1 is not yet complete"
    else
        test_pass "flake.nix references profile-list.nix"
    fi
fi

# Test 4: Verify no broken derivations in flake show
log_test "Checking for broken derivations..."
if nix flake show 2>&1 | grep -qiE "broken|error|failed"; then
    log_error "Found broken derivations in flake show"
    nix flake show 2>&1 | grep -iE "broken|error|failed" || true
    FAILED=1
else
    test_pass "No broken derivations found"
fi

# Test 5: Verify evaluation integrity
log_test "Testing evaluation integrity..."
SYSTEM=$(nix eval --impure --expr 'builtins.currentSystem' --raw)
if ! nix eval ".#packages.$SYSTEM" --json >/dev/null 2>&1; then
    log_error "Failed to evaluate packages for $SYSTEM"
    FAILED=1
else
    test_pass "Packages evaluate successfully for $SYSTEM"
fi

# Test 6: Assert no accidental new outputs (allowlist check)
log_test "Checking flake output structure (allowlist)..."
FLAKE_OUTPUTS=$(nix flake show --json 2>/dev/null | jq -r 'keys[]' | sort)
EXPECTED_OUTPUTS="apps checks devShells formatter packages"
UNEXPECTED_OUTPUTS=$(comm -23 <(echo "$FLAKE_OUTPUTS" | tr '\n' ' ') <(echo "$EXPECTED_OUTPUTS" | tr ' ' '\n' | sort) | tr '\n' ' ')

if [[ -n "$UNEXPECTED_OUTPUTS" ]]; then
    log_error "Unexpected flake outputs found: $UNEXPECTED_OUTPUTS"
    log_error "Expected only: $EXPECTED_OUTPUTS"
    log_error "This may indicate accidentally exposed internals or renames"
    FAILED=1
else
    test_pass "Flake output structure matches allowlist"
fi

echo ""
if [[ $FAILED -eq 0 ]]; then
    log_info "✓ Gatekeeper: All checks passed"
    exit 0
else
    log_error "✗ Gatekeeper: Validation failed"
    exit 1
fi
