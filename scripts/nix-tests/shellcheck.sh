#!/usr/bin/env bash
# Run shellcheck on all Nix test scripts

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR

# Colors for output
readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $*"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $*"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $*"
}

# Check if shellcheck is available
if ! command -v shellcheck >/dev/null 2>&1; then
    log_error "shellcheck is not installed"
    log_info "Install with: nix-shell -p shellcheck"
    log_info "Or: brew install shellcheck (macOS)"
    exit 1
fi

log_info "Running shellcheck on Nix test scripts..."
echo ""

FAILED=0
PASSED=0

# Find all shell scripts in nix-tests directory
readonly SCRIPTS=(
    "$SCRIPT_DIR/lib.sh"
    "$SCRIPT_DIR/test-packages.sh"
    "$SCRIPT_DIR/test-profiles.sh"
    "$SCRIPT_DIR/test-containers.sh"
    "$SCRIPT_DIR/test-containers-env.sh"
    "$SCRIPT_DIR/test-container-security.sh"
    "$SCRIPT_DIR/test-microvms.sh"
    "$SCRIPT_DIR/test-microvms-network.sh"
    "$SCRIPT_DIR/test-apps.sh"
    "$SCRIPT_DIR/test-cli.sh"
    "$SCRIPT_DIR/test-eval.sh"
    "$SCRIPT_DIR/test-iso.sh"
    "$SCRIPT_DIR/test-nginx-config.sh"
    "$SCRIPT_DIR/test-all.sh"
    "$SCRIPT_DIR/shellcheck.sh"
)

FAILED_FILES=()

for script in "${SCRIPTS[@]}"; do
    if [[ ! -f "$script" ]]; then
        log_warn "Skipping $(basename "$script") (not found)"
        continue
    fi

    # Run shellcheck and capture exit code explicitly
    # Temporarily disable set -e to continue checking all scripts even if one fails
    set +e
    shellcheck -x "$script" >/dev/null 2>&1
    exit_code=$?
    set -e

    if [[ $exit_code -eq 0 ]]; then
        log_info "✓ $(basename "$script")"
        ((PASSED++))
    else
        log_error "✗ $(basename "$script")"
        FAILED_FILES+=("$script")
        ((FAILED++))
    fi
done

echo ""
echo "════════════════════════════════════════════════════════════"
echo "Shellcheck Summary"
echo "════════════════════════════════════════════════════════════"
echo "Passed:  $PASSED"
echo "Failed:  $FAILED"
echo ""

if [[ $FAILED -gt 0 ]]; then
    log_error "Some scripts failed shellcheck validation:"
    for file in "${FAILED_FILES[@]}"; do
        echo "  - $file"
    done
    echo ""
    log_info "Run 'shellcheck -x <file>' for details"
    exit 1
fi

log_info "All scripts passed shellcheck validation!"
exit 0
