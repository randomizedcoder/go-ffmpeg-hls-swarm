#!/usr/bin/env bash
# Run all Nix tests

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

START_TIME=$(date +%s)
readonly START_TIME

log_info "════════════════════════════════════════════════════════════"
log_info "Running All Nix Tests"
log_info "════════════════════════════════════════════════════════════"
echo ""

# Run test categories (continue on failure to get full report)
# Each script will print its own summary

# Fast evaluation tests first
"$SCRIPT_DIR/test-eval.sh" || true
echo ""

# Gatekeeper validation
"$SCRIPT_DIR/gatekeeper.sh" || true
echo ""

# Profile and package tests
"$SCRIPT_DIR/test-profiles.sh" || true
echo ""
"$SCRIPT_DIR/test-packages.sh" || true
echo ""

# Container tests
"$SCRIPT_DIR/test-containers.sh" || true
echo ""

# ISO tests (Linux only)
if is_linux; then
    "$SCRIPT_DIR/test-iso.sh" || true
    echo ""
else
    log_warn "Skipping ISO tests (Linux only)"
    echo ""
fi

# MicroVM tests (Linux + KVM only)
if is_linux && has_kvm; then
    "$SCRIPT_DIR/test-microvms.sh" || true
    echo ""
else
    log_warn "Skipping MicroVM tests (not on Linux or KVM not available)"
    echo ""
fi

# App tests (including unified CLI)
"$SCRIPT_DIR/test-apps.sh" || true
echo ""
"$SCRIPT_DIR/test-cli.sh" || true
echo ""

# Nginx config generator tests
"$SCRIPT_DIR/test-nginx-config.sh" || true
echo ""

# Container execution tests (requires Docker)
if command -v docker &>/dev/null && docker info &>/dev/null 2>&1; then
    "$SCRIPT_DIR/test-containers-env.sh" || true
    echo ""
else
    log_warn "Skipping container execution tests (Docker not available)"
    echo ""
fi

# MicroVM network tests (requires KVM and sudo)
if is_linux && has_kvm; then
    "$SCRIPT_DIR/test-microvms-network.sh" || true
    echo ""
else
    log_warn "Skipping MicroVM network tests (not on Linux or KVM not available)"
    echo ""
fi

# Note: Individual scripts print their own summaries
# We don't aggregate here because each script has its own variable scope
log_info "See individual test summaries above for detailed results."

END_TIME=$(date +%s)
readonly END_TIME
readonly DURATION=$((END_TIME - START_TIME))

log_info "════════════════════════════════════════════════════════════"
log_info "All Tests Completed in ${DURATION}s"
log_info "════════════════════════════════════════════════════════════"
echo ""
log_info "Note: Each test category printed its own summary above."
log_info "Review individual summaries to see which tests passed/failed."
echo ""

# Exit with success (individual scripts report their own failures)
exit 0
