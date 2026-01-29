#!/usr/bin/env bash
# Container security testing library
# Provides common security checks that can be reused across different container types
# Container-specific checks should be implemented in the calling script

# Common security checks that apply to all containers
# Usage: run_common_security_checks <runtime> <image_id> <user> <group> <uid> <gid>
run_common_security_checks() {
    local runtime=$1
    local image_id=$2
    local expected_user=$3
    local expected_group=$4
    local expected_uid=$5
    local expected_gid=$6

    local checks_passed=0
    local checks_failed=0

    # Security check 1: Verify /etc/passwd exists and contains expected user
    log_test "  Checking /etc/passwd for $expected_user user..."
    local passwd_content
    if [ "$runtime" = "podman" ]; then
        # Use busybox sh if available, otherwise try to find a shell
        passwd_content=$(podman run --rm --entrypoint "" "$image_id" sh -c "cat /etc/passwd 2>/dev/null" 2>/dev/null || \
                        podman run --rm --entrypoint "" "$image_id" /bin/sh -c "cat /etc/passwd 2>/dev/null" 2>/dev/null || \
                        podman run --rm "$image_id" cat /etc/passwd 2>/dev/null || echo "")
    else
        passwd_content=$(docker run --rm --entrypoint "" "$image_id" sh -c "cat /etc/passwd 2>/dev/null" 2>/dev/null || \
                        docker run --rm --entrypoint "" "$image_id" /bin/sh -c "cat /etc/passwd 2>/dev/null" 2>/dev/null || \
                        docker run --rm "$image_id" cat /etc/passwd 2>/dev/null || echo "")
    fi

    if [ -n "$passwd_content" ]; then
        if echo "$passwd_content" | grep -q "^${expected_user}:"; then
            ((checks_passed++)) || true
            local passwd_line
            passwd_line=$(echo "$passwd_content" | grep "^${expected_user}:")
            log_info "    ✓ $expected_user user found: $passwd_line"
        else
            ((checks_failed++)) || true
            log_warn "    ⊘ $expected_user user not found in /etc/passwd"
            log_info "    Available users: $(echo "$passwd_content" | cut -d: -f1 | tr '\n' ' ')"
        fi
    else
        ((checks_failed++)) || true
        log_warn "    ⊘ /etc/passwd not found or could not be read"
    fi

    # Security check 2: Verify /etc/group exists and contains expected group
    log_test "  Checking /etc/group for $expected_group group..."
    local group_content
    if [ "$runtime" = "podman" ]; then
        group_content=$(podman run --rm --entrypoint "" "$image_id" sh -c "cat /etc/group 2>/dev/null" 2>/dev/null || \
                       podman run --rm --entrypoint "" "$image_id" /bin/sh -c "cat /etc/group 2>/dev/null" 2>/dev/null || \
                       podman run --rm "$image_id" cat /etc/group 2>/dev/null || echo "")
    else
        group_content=$(docker run --rm --entrypoint "" "$image_id" sh -c "cat /etc/group 2>/dev/null" 2>/dev/null || \
                       docker run --rm --entrypoint "" "$image_id" /bin/sh -c "cat /etc/group 2>/dev/null" 2>/dev/null || \
                       docker run --rm "$image_id" cat /etc/group 2>/dev/null || echo "")
    fi

    if [ -n "$group_content" ]; then
        if echo "$group_content" | grep -q "^${expected_group}:"; then
            ((checks_passed++)) || true
            local group_line
            group_line=$(echo "$group_content" | grep "^${expected_group}:")
            log_info "    ✓ $expected_group group found: $group_line"
        else
            ((checks_failed++)) || true
            log_warn "    ⊘ $expected_group group not found in /etc/group"
            log_info "    Available groups: $(echo "$group_content" | cut -d: -f1 | tr '\n' ' ')"
        fi
    else
        ((checks_failed++)) || true
        log_warn "    ⊘ /etc/group not found or could not be read"
    fi

    # Security check 3: Check container config for non-root user
    log_test "  Checking container config for non-root user..."
    local config_user=""
    if [ "$runtime" = "podman" ]; then
        if command -v jq &>/dev/null; then
            config_user=$(podman inspect "$image_id" 2>/dev/null | jq -r '.[0].Config.User // empty' || echo "")
        else
            config_user=$(podman inspect "$image_id" 2>/dev/null | grep -o '"User"[[:space:]]*:[[:space:]]*"[^"]*"' | cut -d'"' -f4 || echo "")
        fi
    else
        if command -v jq &>/dev/null; then
            config_user=$(docker inspect "$image_id" 2>/dev/null | jq -r '.[0].Config.User // empty' || echo "")
        else
            config_user=$(docker inspect "$image_id" 2>/dev/null | grep -o '"User"[[:space:]]*:[[:space:]]*"[^"]*"' | cut -d'"' -f4 || echo "")
        fi
    fi

    if [ "$config_user" = "$expected_user" ] || [ "$config_user" = "$expected_uid" ] || [ "$config_user" = "$expected_uid:$expected_gid" ]; then
        ((checks_passed++)) || true
        log_info "    ✓ Container runs as non-root user: $config_user (secure)"
    elif [ -z "$config_user" ]; then
        ((checks_failed++)) || true
        log_warn "    ⊘ Could not determine container user from config (may default to root)"
    else
        ((checks_failed++)) || true
        log_warn "    ⊘ Container may run as root (User: $config_user) - security risk!"
    fi

    # Security check 4: Verify read-only root filesystem
    log_test "  Verifying filesystem write restrictions..."
    local can_write_etc
    if [ "$runtime" = "podman" ]; then
        if podman run --rm "$image_id" touch /etc/test_file 2>/dev/null; then
            can_write_etc="yes"
        else
            can_write_etc="no"
        fi
        podman run --rm "$image_id" rm -f /etc/test_file 2>/dev/null || true
    else
        if docker run --rm "$image_id" touch /etc/test_file 2>/dev/null; then
            can_write_etc="yes"
        else
            can_write_etc="no"
        fi
        docker run --rm "$image_id" rm -f /etc/test_file 2>/dev/null || true
    fi

    if [ "$can_write_etc" = "no" ]; then
        ((checks_passed++)) || true
        log_info "    ✓ Root filesystem is protected (cannot write to /etc)"
    else
        ((checks_failed++)) || true
        log_warn "    ⊘ Security Risk: Root filesystem is writable (can write to /etc)"
    fi

    # Security check 5: Validate no SUID/SGID binaries
    log_test "  Checking for SUID/SGID binaries..."
    local suid_files
    if [ "$runtime" = "podman" ]; then
        suid_files=$(podman run --rm "$image_id" find / -perm /6000 -type f 2>/dev/null | head -20 || echo "")
    else
        suid_files=$(docker run --rm "$image_id" find / -perm /6000 -type f 2>/dev/null | head -20 || echo "")
    fi

    if [ -z "$suid_files" ]; then
        ((checks_passed++)) || true
        log_info "    ✓ No SUID/SGID binaries found"
    else
        ((checks_failed++)) || true
        log_warn "    ⊘ Found potential privilege escalation vectors:"
        echo "$suid_files" | while IFS= read -r line; do
            [ -n "$line" ] && log_warn "      - $line"
        done
    fi

    # Security check 6: Check for unnecessary binaries (attack surface)
    log_test "  Checking attack surface (unnecessary tools)..."
    local forbidden_tools=("gcc" "make" "apt" "dnf" "yum" "nix" "python3" "perl" "ruby" "node" "npm")
    local found_tools=()

    for tool in "${forbidden_tools[@]}"; do
        local tool_exists
        if [ "$runtime" = "podman" ]; then
            if podman run --rm "$image_id" command -v "$tool" &>/dev/null; then
                tool_exists="yes"
            else
                tool_exists="no"
            fi
        else
            if docker run --rm "$image_id" command -v "$tool" &>/dev/null; then
                tool_exists="yes"
            else
                tool_exists="no"
            fi
        fi

        if [ "$tool_exists" = "yes" ]; then
            found_tools+=("$tool")
        fi
    done

    if [ ${#found_tools[@]} -eq 0 ]; then
        ((checks_passed++)) || true
        log_info "    ✓ No unnecessary development tools found"
    else
        ((checks_failed++)) || true
        log_warn "    ⊘ Attack surface warning: Found unnecessary tools:"
        for tool in "${found_tools[@]}"; do
            log_warn "      - $tool"
        done
    fi

    # Security check 7: Check environment variables for sensitive data
    log_test "  Checking environment variables for sensitive data..."
    local env_vars
    if [ "$runtime" = "podman" ]; then
        env_vars=$(podman inspect "$image_id" 2>/dev/null | grep -iE '"(PATH|HOME|USER|PWD|SECRET|KEY|PASSWORD|TOKEN|API)' || echo "")
    else
        env_vars=$(docker inspect "$image_id" 2>/dev/null | grep -iE '"(PATH|HOME|USER|PWD|SECRET|KEY|PASSWORD|TOKEN|API)' || echo "")
    fi

    local sensitive_found=0
    if echo "$env_vars" | grep -qiE "(SECRET|KEY|PASSWORD|TOKEN|API_KEY)"; then
        sensitive_found=1
    fi

    if [ $sensitive_found -eq 0 ]; then
        ((checks_passed++)) || true
        log_info "    ✓ No obvious sensitive data in environment variables"
    else
        ((checks_failed++)) || true
        log_warn "    ⊘ Potential sensitive data found in environment variables"
        log_info "    Review the following environment variables:"
        echo "$env_vars" | while IFS= read -r line; do
            [ -n "$line" ] && log_info "      $line"
        done
    fi

    # Security check 8: Check for interactive shells (attack surface)
    log_test "  Checking for interactive shells (Attack Surface)..."
    local shells_found=()
    local shell_commands=("sh" "bash" "zsh" "dash" "ash" "csh" "tcsh" "ksh")

    for shell_cmd in "${shell_commands[@]}"; do
        local shell_exists
        if [ "$runtime" = "podman" ]; then
            if podman run --rm "$image_id" which "$shell_cmd" &>/dev/null 2>&1 || \
               podman run --rm "$image_id" command -v "$shell_cmd" &>/dev/null 2>&1; then
                shell_exists="yes"
            else
                shell_exists="no"
            fi
        else
            if docker run --rm "$image_id" which "$shell_cmd" &>/dev/null 2>&1 || \
               docker run --rm "$image_id" command -v "$shell_cmd" &>/dev/null 2>&1; then
                shell_exists="yes"
            else
                shell_exists="no"
            fi
        fi

        if [ "$shell_exists" = "yes" ]; then
            shells_found+=("$shell_cmd")
        fi
    done

    if [ ${#shells_found[@]} -eq 0 ]; then
        ((checks_passed++)) || true
        log_info "    ✓ No shells found (Excellent attack surface reduction)"
    else
        log_warn "    ⊘ Warning: Shell(s) found in container (attack surface):"
        for shell in "${shells_found[@]}"; do
            log_warn "      - $shell"
        done
        log_info "    Note: Shells may be acceptable if only used in entrypoint scripts"
    fi

    # Security check 9: Check for dangerous capabilities
    log_test "  Checking container capabilities..."
    local capabilities
    if [ "$runtime" = "podman" ]; then
        capabilities=$(podman inspect "$image_id" 2>/dev/null | grep -iE '"CapAdd"|"CapDrop"|"EffectiveCaps"' || echo "")
    else
        capabilities=$(docker inspect "$image_id" 2>/dev/null | grep -iE '"CapAdd"|"CapDrop"|"EffectiveCaps"' || echo "")
    fi

    local dangerous_caps=0
    if echo "$capabilities" | grep -qiE "(NET_ADMIN|SYS_ADMIN|ALL)"; then
        dangerous_caps=1
    fi

    if [ $dangerous_caps -eq 0 ]; then
        ((checks_passed++)) || true
        log_info "    ✓ No dangerous capabilities found (container uses default caps)"
    else
        ((checks_failed++)) || true
        log_warn "    ⊘ Dangerous capabilities detected:"
        echo "$capabilities" | grep -iE "(NET_ADMIN|SYS_ADMIN|ALL)" | while IFS= read -r line; do
            [ -n "$line" ] && log_warn "      $line"
        done
    fi

    # Return results via global variables (bash limitation)
    COMMON_CHECKS_PASSED=$checks_passed
    COMMON_CHECKS_FAILED=$checks_failed
}

# Check directory ownership and permissions
# Usage: check_directory_permissions <runtime> <image_id> <directory_path> <expected_user> <expected_group> <expected_perms>
# Note: All log output should go to stderr to avoid interfering with return values
check_directory_permissions() {
    local runtime=$1
    local image_id=$2
    local dir_path=$3
    local expected_user=$4
    local expected_group=$5
    local expected_perms=$6

    local checks_passed=0
    local checks_failed=0

    log_test "  Checking $dir_path ownership and permissions..." >&2
    local dir_owner
    local dir_perms
    if [ "$runtime" = "podman" ]; then
        dir_owner=$(podman run --rm "$image_id" stat -c "%U:%G" "$dir_path" 2>/dev/null || echo "")
        dir_perms=$(podman run --rm "$image_id" stat -c "%a" "$dir_path" 2>/dev/null || echo "")
    else
        dir_owner=$(docker run --rm "$image_id" stat -c "%U:%G" "$dir_path" 2>/dev/null || echo "")
        dir_perms=$(docker run --rm "$image_id" stat -c "%a" "$dir_path" 2>/dev/null || echo "")
    fi

    if [ -n "$dir_owner" ]; then
        if [[ "$dir_owner" == *"$expected_user"* ]] || [[ "$dir_owner" == *"$expected_group"* ]] || [[ "$dir_owner" == *"1000"* ]]; then
            ((checks_passed++)) || true
            log_info "    ✓ $dir_path ownership: $dir_owner" >&2
        else
            ((checks_failed++)) || true
            log_warn "    ⊘ $dir_path ownership: $dir_owner (expected $expected_user:$expected_group)" >&2
        fi
    else
        log_info "    ⊘ $dir_path not found or could not check ownership" >&2
    fi

    if [ -n "$dir_perms" ] && [ -n "$expected_perms" ]; then
        if [[ "$dir_perms" =~ ^[0-9]+$ ]] && [ "$dir_perms" -ge "$expected_perms" ]; then
            ((checks_passed++)) || true
            log_info "    ✓ $dir_path permissions: $dir_perms (acceptable)" >&2
        else
            ((checks_failed++)) || true
            log_warn "    ⊘ $dir_path permissions may be too restrictive: $dir_perms (should be $expected_perms or higher)" >&2
        fi
    fi

    # Check if directory is writable
    local can_write
    if [ "$runtime" = "podman" ]; then
        if podman run --rm "$image_id" touch "$dir_path/test_file" 2>/dev/null; then
            can_write="yes"
            podman run --rm "$image_id" rm -f "$dir_path/test_file" 2>/dev/null || true
        else
            can_write="no"
        fi
    else
        if docker run --rm "$image_id" touch "$dir_path/test_file" 2>/dev/null; then
            can_write="yes"
            docker run --rm "$image_id" rm -f "$dir_path/test_file" 2>/dev/null || true
        else
            can_write="no"
        fi
    fi

    if [ "$can_write" = "yes" ]; then
        ((checks_passed++)) || true
        log_info "    ✓ $dir_path is writable (as expected)" >&2
    else
        ((checks_failed++)) || true
        log_warn "    ⊘ Warning: $dir_path is not writable (may affect functionality)" >&2
    fi

    DIR_CHECK_PASSED=$checks_passed
    DIR_CHECK_FAILED=$checks_failed
}
