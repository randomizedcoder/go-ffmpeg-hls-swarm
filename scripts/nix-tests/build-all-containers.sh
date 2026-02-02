#!/usr/bin/env bash
# Build all containers to separate output paths
# This allows building multiple containers without overwriting ./result

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

SYSTEM=$(get_system)
readonly SYSTEM

log_info "Building all containers for $SYSTEM..."
echo ""

# Build containers to specific output paths
build_container() {
    local package_name=$1
    local output_name=$2
    local attr_path=".#packages.$SYSTEM.$package_name"

    log_test "Building $package_name..."
    
    if nix build "$attr_path" --out-link "./result-$output_name" 2>&1; then
        local container_path
        container_path=$(nix build "$attr_path" --print-out-paths 2>/dev/null)
        test_pass "$package_name" "Built successfully"
        log_info "  Output: ./result-$output_name -> $container_path"
        return 0
    else
        test_fail "$package_name" "Build failed"
        return 1
    fi
}

# Build test-origin-container
if nix eval ".#packages.$SYSTEM.test-origin-container" >/dev/null 2>&1; then
    build_container "test-origin-container" "test-origin"
else
    test_skip "test-origin-container" "Container not available for $SYSTEM"
fi

# Build swarm-client-container
if nix eval ".#packages.$SYSTEM.swarm-client-container" >/dev/null 2>&1; then
    build_container "swarm-client-container" "swarm-client"
else
    test_skip "swarm-client-container" "Container not available for $SYSTEM"
fi

echo ""
log_info "Build summary:"
log_info "  Containers built to:"
log_info "    ./result-test-origin  -> test-origin-container"
log_info "    ./result-swarm-client -> swarm-client-container"
log_info ""
log_info "To load into Docker/podman:"
log_info "  docker load < ./result-test-origin"
log_info "  docker load < ./result-swarm-client"

print_summary
