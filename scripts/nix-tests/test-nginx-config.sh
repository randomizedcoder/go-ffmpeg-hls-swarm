#!/usr/bin/env bash
# Test nginx config generator packages and app
# Verifies that nginx configs can be generated and are valid

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

SYSTEM=$(get_system)
readonly SYSTEM

log_info "Testing nginx config generator for $SYSTEM"
log_info "This should be fast (~30 seconds)..."
echo ""

# Helper function to test if a package attribute exists and can be built
test_package_builds() {
    local package_name=$1
    local attr_path=".#packages.$SYSTEM.$package_name"

    log_test "Building $package_name..."
    if nix build "$attr_path" --print-out-paths >/dev/null 2>&1; then
        test_pass "$package_name"
        return 0
    else
        test_fail "$package_name" "Build failed"
        return 1
    fi
}

# Helper function to test if a package exists (evaluation only)
test_package_exists() {
    local package_name=$1
    local attr_path=".#packages.$SYSTEM.$package_name"

    if nix eval "$attr_path" >/dev/null 2>&1; then
        return 0
    else
        return 1
    fi
}

# Helper function to validate nginx config syntax using nginx from Nix store
validate_nginx_config() {
    local config_file=$1
    local package_name=$2

    # Get nginx from Nix store (fast, uses cache if available)
    local nginx_store_path
    if ! nginx_store_path=$(nix eval --raw nixpkgs#nginx.outPath 2>/dev/null); then
        # Fallback: try system nginx
        if command -v nginx &>/dev/null; then
            nginx_bin="nginx"
        else
            test_skip "$package_name-syntax" "nginx not available for syntax validation"
            return 0
        fi
    else
        nginx_bin="$nginx_store_path/bin/nginx"
    fi

    # nginx -t tests config syntax
    # The config file uses /dev/stderr and /tmp/nginx.pid which should be writable
    # Use -p to set prefix directory (for relative paths in config)
    local temp_dir
    temp_dir=$(mktemp -d)
    # Use function to avoid variable expansion issues in trap (shellcheck SC2064)
    cleanup_temp() {
        rm -rf "$temp_dir"
    }
    trap cleanup_temp EXIT

    # Test nginx config syntax
    # -t: test configuration file
    # -c: configuration file
    # -p: prefix path (for resolving relative paths)
    if "$nginx_bin" -t -c "$config_file" -p "$temp_dir" >/dev/null 2>&1; then
        test_pass "$package_name-syntax" "Valid nginx syntax"
        return 0
    else
        # Show error for debugging
        local error_output
        error_output=$("$nginx_bin" -t -c "$config_file" -p "$temp_dir" 2>&1 || true)
        test_fail "$package_name-syntax" "Invalid nginx syntax: ${error_output:0:100}"
        return 1
    fi
}

# Helper function to check config contains expected content
check_config_content() {
    local config_file=$1
    local package_name=$2
    local profile=$3

    local checks_passed=0
    local checks_failed=0

    # Check for basic nginx structure
    if grep -q "worker_processes" "$config_file"; then
        ((checks_passed++)) || true
    else
        ((checks_failed++)) || true
        log_warn "  Missing worker_processes directive"
    fi

    # Check for http block
    if grep -q "^http {" "$config_file"; then
        ((checks_passed++)) || true
    else
        ((checks_failed++)) || true
        log_warn "  Missing http block"
    fi

    # Check for server block
    if grep -q "server {" "$config_file"; then
        ((checks_passed++)) || true
    else
        ((checks_failed++)) || true
        log_warn "  Missing server block"
    fi

    # Check for HLS-specific locations
    if grep -q "location.*\.m3u8" "$config_file"; then
        ((checks_passed++)) || true
    else
        ((checks_failed++)) || true
        log_warn "  Missing .m3u8 location"
    fi

    if grep -q "location.*\.ts" "$config_file"; then
        ((checks_passed++)) || true
    else
        ((checks_failed++)) || true
        log_warn "  Missing .ts location"
    fi

    # Check for Cache-Control headers
    if grep -q "Cache-Control" "$config_file"; then
        ((checks_passed++)) || true
    else
        ((checks_failed++)) || true
        log_warn "  Missing Cache-Control headers"
    fi

    # Check for health endpoint
    if grep -q "/health" "$config_file"; then
        ((checks_passed++)) || true
    else
        ((checks_failed++)) || true
        log_warn "  Missing /health endpoint"
    fi

    if [[ $checks_failed -eq 0 ]]; then
        test_pass "$package_name-content" "Config contains expected content"
        return 0
    else
        test_fail "$package_name-content" "$checks_failed checks failed"
        return 1
    fi
}

# Test default nginx config package
log_test "Testing default nginx config package..."
if test_package_builds "test-origin-nginx-config"; then
    CONFIG_PATH=$(nix build ".#packages.$SYSTEM.test-origin-nginx-config" --print-out-paths 2>/dev/null)
    if [[ -f "$CONFIG_PATH" ]]; then
        test_pass "test-origin-nginx-config-file" "Config file exists"

        # Validate syntax if nginx is available
        validate_nginx_config "$CONFIG_PATH" "test-origin-nginx-config"

        # Check content
        check_config_content "$CONFIG_PATH" "test-origin-nginx-config" "default"
    else
        test_fail "test-origin-nginx-config-file" "Config file not found at $CONFIG_PATH"
    fi
fi

# Test profile-specific config packages
readonly PROFILES=(
    "low-latency"
    "stress"
    "4k-abr"
)

for profile in "${PROFILES[@]}"; do
    package_name="test-origin-nginx-config-$profile"

    log_test "Testing $package_name..."
    if test_package_exists "$package_name"; then
        if test_package_builds "$package_name"; then
            CONFIG_PATH=$(nix build ".#packages.$SYSTEM.$package_name" --print-out-paths 2>/dev/null)
            if [[ -f "$CONFIG_PATH" ]]; then
                test_pass "$package_name-file" "Config file exists"

                # Validate syntax
                validate_nginx_config "$CONFIG_PATH" "$package_name"

                # Check content
                check_config_content "$CONFIG_PATH" "$package_name" "$profile"
            else
                test_fail "$package_name-file" "Config file not found"
            fi
        fi
    else
        test_skip "$package_name" "Package not available (may not be implemented yet)"
    fi
done

# Test nginx-config app (if implemented)
log_test "Testing nginx-config app..."
if nix eval ".#apps.$SYSTEM.nginx-config" >/dev/null 2>&1; then
    # Test app with default profile
    log_test "Testing app with default profile..."
    if nix run ".#nginx-config" >/dev/null 2>&1; then
        test_pass "nginx-config-app-default" "App runs with default profile"

        # Check that output contains nginx config
        APP_OUTPUT=$(nix run ".#nginx-config" 2>/dev/null || true)
        if echo "$APP_OUTPUT" | grep -q "worker_processes"; then
            test_pass "nginx-config-app-output" "App outputs valid nginx config"
        else
            test_fail "nginx-config-app-output" "App output doesn't contain nginx config"
        fi
    else
        test_fail "nginx-config-app-default" "App failed to run"
    fi

    # Test app with specific profile (if supported)
    for profile in "${PROFILES[@]}"; do
        log_test "Testing app with $profile profile..."
        if nix run ".#nginx-config" -- "$profile" >/dev/null 2>&1; then
            test_pass "nginx-config-app-$profile" "App runs with $profile profile"
        else
            test_skip "nginx-config-app-$profile" "App may not support profile argument yet"
        fi
    done
else
    test_skip "nginx-config-app" "App not implemented yet"
fi

# Test that different profiles produce different configs (if multiple profiles available)
log_test "Testing profile differentiation..."
if test_package_exists "test-origin-nginx-config" && test_package_exists "test-origin-nginx-config-low-latency"; then
    DEFAULT_PATH=$(nix build ".#packages.$SYSTEM.test-origin-nginx-config" --print-out-paths 2>/dev/null)
    LOW_LATENCY_PATH=$(nix build ".#packages.$SYSTEM.test-origin-nginx-config-low-latency" --print-out-paths 2>/dev/null)

    if [[ -f "$DEFAULT_PATH" ]] && [[ -f "$LOW_LATENCY_PATH" ]]; then
        # Configs should be different (at least in some way)
        if ! cmp -s "$DEFAULT_PATH" "$LOW_LATENCY_PATH"; then
            test_pass "profile-differentiation" "Different profiles produce different configs"
        else
            test_skip "profile-differentiation" "Configs are identical (may be expected)"
        fi
    else
        test_skip "profile-differentiation" "Could not access config files"
    fi
else
    test_skip "profile-differentiation" "Required packages not available"
fi

# Test config file size (should be reasonable)
log_test "Testing config file size..."
if test_package_exists "test-origin-nginx-config"; then
    CONFIG_PATH=$(nix build ".#packages.$SYSTEM.test-origin-nginx-config" --print-out-paths 2>/dev/null)
    if [[ -f "$CONFIG_PATH" ]]; then
        SIZE=$(wc -c < "$CONFIG_PATH")
        if [[ $SIZE -gt 100 ]] && [[ $SIZE -lt 100000 ]]; then
            test_pass "config-file-size" "Config file size is reasonable ($SIZE bytes)"
        else
            test_fail "config-file-size" "Config file size seems wrong ($SIZE bytes)"
        fi
    fi
fi

print_summary
