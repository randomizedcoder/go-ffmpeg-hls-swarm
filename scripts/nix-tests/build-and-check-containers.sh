#!/usr/bin/env bash
# Build all containers and run security checks on them
# This is a convenience script that combines building and testing

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

SYSTEM=$(get_system)
readonly SYSTEM

log_info "Building and security-checking all containers for $SYSTEM..."
echo ""

# Step 1: Build all containers
log_info "════════════════════════════════════════════════════════════"
log_info "Step 1: Building containers"
log_info "════════════════════════════════════════════════════════════"
echo ""

if [ -f "$SCRIPT_DIR/build-all-containers.sh" ]; then
    "$SCRIPT_DIR/build-all-containers.sh"
    BUILD_EXIT_CODE=$?
    
    if [ $BUILD_EXIT_CODE -ne 0 ]; then
        log_error "Container build failed. Skipping security checks."
        exit $BUILD_EXIT_CODE
    fi
else
    log_warn "build-all-containers.sh not found, building containers individually..."
    
    # Build test-origin-container
    if nix eval ".#packages.$SYSTEM.test-origin-container" >/dev/null 2>&1; then
        log_test "Building test-origin-container..."
        if nix build ".#packages.$SYSTEM.test-origin-container" --out-link "./result-test-origin" 2>&1; then
            test_pass "test-origin-container-build" "Built successfully"
        else
            test_fail "test-origin-container-build" "Build failed"
        fi
    fi
    
    # Build swarm-client-container
    if nix eval ".#packages.$SYSTEM.swarm-client-container" >/dev/null 2>&1; then
        log_test "Building swarm-client-container..."
        if nix build ".#packages.$SYSTEM.swarm-client-container" --out-link "./result-swarm-client" 2>&1; then
            test_pass "swarm-client-container-build" "Built successfully"
        else
            test_fail "swarm-client-container-build" "Build failed"
        fi
    fi
fi

echo ""
log_info "════════════════════════════════════════════════════════════"
log_info "Step 2: Running security checks"
log_info "════════════════════════════════════════════════════════════"
echo ""

# Step 2: Run security checks
if [ -f "$SCRIPT_DIR/test-container-security.sh" ]; then
    "$SCRIPT_DIR/test-container-security.sh"
    SECURITY_EXIT_CODE=$?
    
    if [ $SECURITY_EXIT_CODE -ne 0 ]; then
        log_warn "Some security checks failed. Review the output above."
        exit $SECURITY_EXIT_CODE
    fi
else
    log_error "test-container-security.sh not found!"
    exit 1
fi

echo ""
log_info "════════════════════════════════════════════════════════════"
log_info "Summary"
log_info "════════════════════════════════════════════════════════════"
log_info "✓ All containers built successfully"
log_info "✓ Security checks completed"
log_info ""
log_info "Containers available at:"
log_info "  ./result-test-origin  -> test-origin-container"
log_info "  ./result-swarm-client -> swarm-client-container"
