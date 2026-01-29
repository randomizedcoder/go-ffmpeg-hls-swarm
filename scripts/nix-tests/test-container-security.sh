#!/usr/bin/env bash
# Test container structure and security properties
# Verifies that containers meet security objectives:
# - Non-root user execution
# - Proper file ownership
# - Correct directory permissions
# - User/group files present
#
# Supports multiple container types with common and container-specific checks

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/container-security-lib.sh"

SYSTEM=$(get_system)
readonly SYSTEM

log_info "Testing container structure and security for $SYSTEM"
log_info "This should be fast (~30 seconds)..."
echo ""

# Detect container runtime (docker or podman)
detect_runtime() {
    if command -v podman &>/dev/null && podman info &>/dev/null; then
        echo "podman"
    elif command -v docker &>/dev/null && docker info &>/dev/null; then
        echo "docker"
    else
        echo ""
    fi
}

# Container-specific checks for test-origin-container
# Uses global variables SPECIFIC_CHECKS_PASSED and SPECIFIC_CHECKS_FAILED to return results
test_origin_specific_checks() {
    local runtime=$1
    local image_id=$2
    local base_passed=$3
    local base_failed=$4

    local checks_passed=$base_passed
    local checks_failed=$base_failed

    # Check nginx-specific directories
    check_directory_permissions "$runtime" "$image_id" "/var/hls" "nginx" "nginx" "775" >&2
    checks_passed=$((checks_passed + DIR_CHECK_PASSED))
    checks_failed=$((checks_failed + DIR_CHECK_FAILED))

    check_directory_permissions "$runtime" "$image_id" "/var/log/nginx" "nginx" "nginx" "755" >&2
    checks_passed=$((checks_passed + DIR_CHECK_PASSED))
    checks_failed=$((checks_failed + DIR_CHECK_FAILED))

    # Verify nginx can run as non-root user
    # Note: Config is provided at runtime, so we just verify nginx binary works
    log_test "  Verifying nginx can run as non-root user..." >&2
    local nginx_test_result
    local nginx_version_result
    if [ "$runtime" = "podman" ]; then
        # Check if nginx binary exists and can show version (doesn't require config)
        if podman run --rm --user 1000:1000 "$image_id" nginx -v &>/dev/null; then
            nginx_version_result="pass"
        else
            nginx_version_result="fail"
        fi
        # Try config test if a default config exists (may fail, that's okay)
        if podman run --rm --user 1000:1000 "$image_id" nginx -t &>/dev/null 2>&1; then
            nginx_test_result="pass"
        else
            nginx_test_result="skip"  # Config provided at runtime, not a failure
        fi
    else
        if docker run --rm --user 1000:1000 "$image_id" nginx -v &>/dev/null; then
            nginx_version_result="pass"
        else
            nginx_version_result="fail"
        fi
        if docker run --rm --user 1000:1000 "$image_id" nginx -t &>/dev/null 2>&1; then
            nginx_test_result="pass"
        else
            nginx_test_result="skip"
        fi
    fi

    if [ "$nginx_version_result" = "pass" ]; then
        ((checks_passed++)) || true
        if [ "$nginx_test_result" = "pass" ]; then
            log_info "    ✓ Nginx can run as non-root user and config is valid" >&2
        else
            log_info "    ✓ Nginx can run as non-root user (config provided at runtime)" >&2
        fi
    else
        ((checks_failed++)) || true
        log_warn "    ⊘ Nginx binary not accessible or cannot run as non-root user" >&2
    fi

    # List nginx-specific key files
    log_test "  Listing key files in container..." >&2
    local key_files=(
        "/etc/passwd"
        "/etc/group"
        "/etc/shadow"
        "/etc/nsswitch.conf"
        "/var/hls"
        "/var/log/nginx"
    )

    log_info "    Files found in container:" >&2
    for file in "${key_files[@]}"; do
        local file_exists
        if [ "$runtime" = "podman" ]; then
            file_exists=$(podman run --rm "$image_id" test -e "$file" 2>/dev/null && echo "yes" || echo "no")
        else
            file_exists=$(docker run --rm "$image_id" test -e "$file" 2>/dev/null && echo "yes" || echo "no")
        fi

        if [ "$file_exists" = "yes" ]; then
            log_info "      ✓ $file (exists)" >&2
        else
            log_info "      ⊘ $file (not found)" >&2
        fi
    done

    SPECIFIC_CHECKS_PASSED=$checks_passed
    SPECIFIC_CHECKS_FAILED=$checks_failed
}

# Container-specific checks for swarm-client-container
# Uses global variables SPECIFIC_CHECKS_PASSED and SPECIFIC_CHECKS_FAILED to return results
swarm_client_specific_checks() {
    local runtime=$1
    local image_id=$2
    local base_passed=$3
    local base_failed=$4

    local checks_passed=$base_passed
    local checks_failed=$base_failed

    # List swarm-client-specific key files
    log_test "  Listing key files in container..." >&2
    local key_files=(
        "/etc/passwd"
        "/etc/group"
        "/etc/shadow"
        "/etc/nsswitch.conf"
        "/tmp"
    )

    log_info "    Files found in container:" >&2
    for file in "${key_files[@]}"; do
        local file_exists
        if [ "$runtime" = "podman" ]; then
            # Override entrypoint to avoid STREAM_URL requirement
            # Use ls to check existence (works with symlinks) or test -e as fallback
            file_exists=$(podman run --rm --entrypoint "" "$image_id" sh -c "ls $file >/dev/null 2>&1 && echo yes || echo no" 2>/dev/null || \
                         podman run --rm --entrypoint "" "$image_id" /bin/sh -c "ls $file >/dev/null 2>&1 && echo yes || echo no" 2>/dev/null || \
                         podman run --rm --entrypoint "" "$image_id" sh -c "test -e $file 2>/dev/null && echo yes || echo no" 2>/dev/null || echo "no")
        else
            file_exists=$(docker run --rm --entrypoint "" "$image_id" sh -c "ls $file >/dev/null 2>&1 && echo yes || echo no" 2>/dev/null || \
                         docker run --rm --entrypoint "" "$image_id" /bin/sh -c "ls $file >/dev/null 2>&1 && echo yes || echo no" 2>/dev/null || \
                         docker run --rm --entrypoint "" "$image_id" sh -c "test -e $file 2>/dev/null && echo yes || echo no" 2>/dev/null || echo "no")
        fi

        if [ "$file_exists" = "yes" ]; then
            log_info "      ✓ $file (exists)" >&2
        else
            log_info "      ⊘ $file (not found)" >&2
        fi
    done

    # Check /tmp permissions (should be world-writable with sticky bit)
    # Note: check_directory_permissions also needs entrypoint override, but it's in the library
    # We'll handle /tmp check separately here
    log_test "  Checking /tmp ownership and permissions..." >&2
    local tmp_owner
    local tmp_uid_gid
    local tmp_perms
    if [ "$runtime" = "podman" ]; then
        tmp_owner=$(podman run --rm --entrypoint "" "$image_id" sh -c "stat -c '%U:%G' /tmp 2>/dev/null" 2>/dev/null || echo "")
        tmp_uid_gid=$(podman run --rm --entrypoint "" "$image_id" sh -c "stat -c '%u:%g' /tmp 2>/dev/null" 2>/dev/null || echo "")
        tmp_perms=$(podman run --rm --entrypoint "" "$image_id" sh -c "stat -c '%a' /tmp 2>/dev/null" 2>/dev/null || echo "")
    else
        tmp_owner=$(docker run --rm --entrypoint "" "$image_id" sh -c "stat -c '%U:%G' /tmp 2>/dev/null" 2>/dev/null || echo "")
        tmp_uid_gid=$(docker run --rm --entrypoint "" "$image_id" sh -c "stat -c '%u:%g' /tmp 2>/dev/null" 2>/dev/null || echo "")
        tmp_perms=$(docker run --rm --entrypoint "" "$image_id" sh -c "stat -c '%a' /tmp 2>/dev/null" 2>/dev/null || echo "")
    fi

    if [ -n "$tmp_perms" ]; then
        if [[ "$tmp_perms" =~ ^[0-9]+$ ]] && [ "$tmp_perms" -ge 1777 ]; then
            ((checks_passed++)) || true
            log_info "    ✓ /tmp permissions: $tmp_perms (world-writable with sticky bit)" >&2
        else
            ((checks_failed++)) || true
            log_warn "    ⊘ /tmp permissions: $tmp_perms (should be 1777)" >&2
        fi
    else
        ((checks_failed++)) || true
        log_warn "    ⊘ Could not check /tmp permissions" >&2
    fi

    # Check ownership - use numeric UID:GID if names aren't resolvable
    if [ -n "$tmp_uid_gid" ]; then
        if [ "$tmp_uid_gid" = "0:0" ]; then
            ((checks_passed++)) || true
            log_info "    ✓ /tmp ownership: $tmp_owner (UID:GID=0:0, root:root)" >&2
        else
            ((checks_failed++)) || true
            log_warn "    ⊘ /tmp ownership: $tmp_owner (UID:GID=$tmp_uid_gid, expected 0:0)" >&2
        fi
    elif [ -n "$tmp_owner" ] && [[ "$tmp_owner" == *"root"* ]]; then
        ((checks_passed++)) || true
        log_info "    ✓ /tmp ownership: $tmp_owner" >&2
    elif [ -n "$tmp_owner" ]; then
        ((checks_failed++)) || true
        log_warn "    ⊘ /tmp ownership: $tmp_owner (expected root:root)" >&2
    else
        ((checks_failed++)) || true
        log_warn "    ⊘ Could not check /tmp ownership" >&2
    fi
    checks_passed=$((checks_passed + DIR_CHECK_PASSED))
    checks_failed=$((checks_failed + DIR_CHECK_FAILED))

    SPECIFIC_CHECKS_PASSED=$checks_passed
    SPECIFIC_CHECKS_FAILED=$checks_failed
}

# Helper function to inspect container using Docker/podman
inspect_container() {
    local package_name=$1
    local container_type=$2  # "test-origin" or "swarm-client"
    local expected_user=$3
    local expected_group=$4
    local expected_uid=$5
    local expected_gid=$6
    local attr_path=".#packages.$SYSTEM.$package_name"

    log_test "Inspecting $package_name..."

    # Build the container (use --print-out-paths to avoid symlink conflicts)
    # This doesn't create ./result, so multiple containers can be built in parallel
    local container_path
    if ! container_path=$(nix build "$attr_path" --print-out-paths --no-link 2>/dev/null); then
        test_fail "$package_name-build" "Failed to build container"
        return 1
    fi

    test_pass "$package_name-build" "Container built successfully"
    log_info "  Container path: $container_path"

    # Detect container runtime
    local runtime
    runtime=$(detect_runtime)
    if [ -z "$runtime" ]; then
        test_skip "$package_name-inspect" "No container runtime (docker/podman) available"
        return 0
    fi
    log_info "  Using container runtime: $runtime"

    # Load the container image
    log_test "  Loading container image..."
    local image_id
    image_id=""

    if [ "$runtime" = "podman" ]; then
        if image_id=$(podman load -i "$container_path" 2>&1 | grep -oP 'Loaded image: \K[^ ]+' | head -1); then
            log_info "  ✓ Image loaded: $image_id"
        else
            # Try alternative output format
            image_id=$(podman load -i "$container_path" 2>&1 | tail -1 | awk '{print $NF}' || echo "")
            if [ -n "$image_id" ]; then
                log_info "  ✓ Image loaded: $image_id"
            else
                test_fail "$package_name-load" "Failed to load container image"
                return 1
            fi
        fi
    else
        # docker
        if image_id=$(docker load -i "$container_path" 2>&1 | grep -oP 'Loaded image: \K[^ ]+' | head -1); then
            log_info "  ✓ Image loaded: $image_id"
        else
            # Try alternative output format
            image_id=$(docker load -i "$container_path" 2>&1 | tail -1 | awk '{print $NF}' || echo "")
            if [ -n "$image_id" ]; then
                log_info "  ✓ Image loaded: $image_id"
            else
                test_fail "$package_name-load" "Failed to load container image"
                return 1
            fi
        fi
    fi

    # Cleanup function to remove loaded image
    cleanup_image() {
        local img_id="${image_id:-}"
        local rt="${runtime:-}"
        if [ -n "$img_id" ] && [ -n "$rt" ]; then
            if [ "$rt" = "podman" ]; then
                podman rmi "$img_id" &>/dev/null || true
            else
                docker rmi "$img_id" &>/dev/null || true
            fi
        fi
    }
    trap cleanup_image EXIT

    # Run common security checks
    run_common_security_checks "$runtime" "$image_id" "$expected_user" "$expected_group" "$expected_uid" "$expected_gid"
    local checks_passed=$COMMON_CHECKS_PASSED
    local checks_failed=$COMMON_CHECKS_FAILED

    # Run container-specific checks (output goes to stderr to avoid mixing with return values)
    if [ "$container_type" = "test-origin" ]; then
        test_origin_specific_checks "$runtime" "$image_id" "$checks_passed" "$checks_failed"
        checks_passed=$SPECIFIC_CHECKS_PASSED
        checks_failed=$SPECIFIC_CHECKS_FAILED
    elif [ "$container_type" = "swarm-client" ]; then
        swarm_client_specific_checks "$runtime" "$image_id" "$checks_passed" "$checks_failed"
        checks_passed=$SPECIFIC_CHECKS_PASSED
        checks_failed=$SPECIFIC_CHECKS_FAILED
    else
        log_warn "    ⊘ Unknown container type: $container_type"
    fi

    # Summary
    echo ""
    log_info "  Security check summary:"
    log_info "    Passed: $checks_passed"
    log_info "    Failed: $checks_failed"

    if [[ $checks_failed -eq 0 ]]; then
        test_pass "$package_name-security" "All security checks passed ($checks_passed checks)"
        log_info "  ✓ Container meets all security objectives"
        return 0
    else
        test_fail "$package_name-security" "$checks_failed security checks failed ($checks_passed passed)"
        log_warn "  ⊘ Container has security issues that need attention"
        return 1
    fi
}

# Test test-origin-container
if nix eval ".#packages.$SYSTEM.test-origin-container" >/dev/null 2>&1; then
    inspect_container "test-origin-container" "test-origin" "nginx" "nginx" "1000" "1000"
else
    test_skip "test-origin-container" "Container not available"
fi

# Test swarm-client-container
if nix eval ".#packages.$SYSTEM.swarm-client-container" >/dev/null 2>&1; then
    inspect_container "swarm-client-container" "swarm-client" "swarm" "swarm" "1000" "1000"
else
    test_skip "swarm-client-container" "Container not available"
fi

print_summary
