#!/usr/bin/env bash
# Dry run evaluation tests - fast validation without builds
# Catches logic errors in lib.genAttrs, profile validation, etc.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

SYSTEM=$(get_system)
readonly SYSTEM

log_info "════════════════════════════════════════════════════════════"
log_info "Dry Run Evaluation Testing for $SYSTEM"
log_info "This should be fast (~10-30 seconds)..."
log_info "════════════════════════════════════════════════════════════"
echo ""

# Test 1: Evaluate all packages (no builds)
log_test "Evaluating all packages (no builds)..."
if nix eval ".#packages.$SYSTEM" --json >/dev/null 2>&1; then
    test_pass "All packages evaluate"
else
    test_fail "Package evaluation" "Failed to evaluate packages"
    exit 1
fi
echo ""  # Add spacing after test

# Test 2: Check for broken derivations
log_test "Checking for broken derivations..."
if nix flake show 2>&1 | grep -qiE "broken|error|failed"; then
    log_error "Found broken derivations:"
    nix flake show 2>&1 | grep -iE "broken|error|failed" || true
    test_fail "Broken derivations" "flake show reports errors"
    exit 1
else
    test_pass "No broken derivations"
fi

# Test 3: Verify profile validation works (using import, not builtins.getFlake to avoid socket file issues)
log_test "Testing profile validation..."
# Test that valid profiles work
# Use import directly instead of builtins.getFlake to avoid reading socket files
if nix eval --impure --expr '
  let
    pkgs = import <nixpkgs> {};
    lib = pkgs.lib;
    toConfig = import ./nix/test-origin/config/profile-list.nix { inherit lib; };
    scConfig = import ./nix/swarm-client/config/profile-list.nix { inherit lib; };
  in
  {
    test-origin-valid = toConfig.validateProfile "default";
    swarm-client-valid = scConfig.validateProfile "default";
  }
' --json >/dev/null 2>&1; then
    # Test that invalid profiles throw errors (as expected)
    if ! nix eval --impure --expr '
      let
        pkgs = import <nixpkgs> {};
        lib = pkgs.lib;
        toConfig = import ./nix/test-origin/config/profile-list.nix { inherit lib; };
      in
      toConfig.validateProfile "invalid-profile-that-does-not-exist"
    ' --json >/dev/null 2>&1; then
        test_pass "Profile validation works (valid profiles pass, invalid profiles fail)"
    else
        test_fail "Profile validation" "Invalid profile should have thrown an error"
        exit 1
    fi
else
    test_fail "Profile validation" "Failed to evaluate validation"
    exit 1
fi

# Test 4: Verify all profiles from single source are accessible (using import)
log_test "Verifying all profiles are accessible..."
PROFILES=$(nix eval --impure --expr '
  let
    pkgs = import <nixpkgs> {};
    lib = pkgs.lib;
    toConfig = import ./nix/test-origin/config/profile-list.nix { inherit lib; };
  in
  toConfig.profiles
' --json | jq -r '.[]')

for profile in $PROFILES; do
    # Map profile names to package names (some profiles have different package names)
    case "$profile" in
        "default")
            PKG_NAME="test-origin"
            ;;
        "stress-test")
            PKG_NAME="test-origin-stress"
            ;;
        "tap")
            PKG_NAME="test-origin-vm-tap"  # TAP profiles are VM-only
            ;;
        "tap-logged")
            PKG_NAME="test-origin-vm-tap-logged"  # TAP profiles are VM-only
            ;;
        *)
            PKG_NAME="test-origin-$profile"
            ;;
    esac

    if nix eval ".#packages.$SYSTEM.$PKG_NAME" --json >/dev/null 2>&1; then
        test_pass "test-origin-$profile → $PKG_NAME (accessible)"
    else
        # For VM-only packages, skip on non-Linux or if KVM not available
        if [[ "$profile" == "tap" ]] || [[ "$profile" == "tap-logged" ]]; then
            if ! is_linux; then
                test_skip "test-origin-$profile" "VM-only package, not on Linux"
            else
                test_skip "test-origin-$profile" "VM-only package, may require KVM"
            fi
        else
            test_fail "test-origin-$profile → $PKG_NAME" "Not accessible in packages"
        fi
    fi
done

# Test 5: Verify platform-specific packages are correctly gated
log_test "Verifying platform-specific package gating..."
if is_linux; then
    # Linux-only packages should exist
    if nix eval ".#packages.$SYSTEM.test-origin-container-enhanced" --json >/dev/null 2>&1; then
        test_pass "test-origin-container-enhanced (Linux, exists)"
    else
        # This is OK if Phase 3 not yet complete
        test_skip "test-origin-container-enhanced" "Phase 3 not yet complete"
    fi
else
    # Linux-only packages should not exist on non-Linux
    if nix eval ".#packages.$SYSTEM.test-origin-container-enhanced" --json >/dev/null 2>&1; then
        test_fail "test-origin-container-enhanced" "Should not exist on $(uname)"
    else
        test_pass "test-origin-container-enhanced (correctly omitted on $(uname))"
    fi
fi

# Test 6: Verify universal packages exist on all platforms
log_test "Verifying universal packages exist..."
UNIVERSAL_PACKAGES=(
    "go-ffmpeg-hls-swarm"
    "test-origin"
    "swarm-client"
)

for pkg in "${UNIVERSAL_PACKAGES[@]}"; do
    if nix eval ".#packages.$SYSTEM.$pkg" --json >/dev/null 2>&1; then
        test_pass "$pkg (universal, exists)"
    else
        test_fail "$pkg" "Universal package missing on $SYSTEM"
    fi
done

print_summary

# Exit with failure if any tests failed
if [[ $FAILED -gt 0 ]]; then
    log_error "Evaluation tests failed - do not proceed to Phase 2"
    exit 1
fi

log_info "✓ All evaluation tests passed - safe to proceed to Phase 2"
