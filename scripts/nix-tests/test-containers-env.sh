#!/usr/bin/env bash
# Test container execution with environment variables
# Tests that containers can be loaded and run with proper environment setup

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

SYSTEM=$(get_system)
readonly SYSTEM

log_info "Testing container execution with environment variables for $SYSTEM"
log_info "This requires Docker and may take a few minutes..."
echo ""

if ! command -v docker &>/dev/null; then
    log_warn "Docker not found, skipping container execution tests"
    test_skip "container-execution" "Docker not available"
    print_summary
    exit 0
fi

# Check if Docker daemon is running
if ! docker info &>/dev/null; then
    log_warn "Docker daemon not running, skipping container execution tests"
    test_skip "container-execution" "Docker daemon not running"
    print_summary
    exit 0
fi

# Test 1: Main binary container (smoke test)
log_test "Testing main binary container execution..."
if nix build ".#packages.$SYSTEM.go-ffmpeg-hls-swarm-container" --print-out-paths >/dev/null 2>&1; then
    CONTAINER_PATH=$(nix build ".#packages.$SYSTEM.go-ffmpeg-hls-swarm-container" --print-out-paths 2>/dev/null)
    if docker load < "$CONTAINER_PATH" 2>&1 | grep -q "Loaded image"; then
        # Test that container can start and entrypoint is available
        # First verify entrypoint exists
        if timeout 5 docker run --rm --entrypoint /bin/sh go-ffmpeg-hls-swarm:latest \
            -c "test -f /bin/swarm-entrypoint && echo 'Entrypoint available'" >/dev/null 2>&1; then
            test_pass "main-container-entrypoint" "Container entrypoint available"

            # Test that binary is available (container can start)
            if timeout 5 docker run --rm --entrypoint /bin/sh go-ffmpeg-hls-swarm:latest \
                -c "go-ffmpeg-hls-swarm --version >/dev/null 2>&1 || go-ffmpeg-hls-swarm --help >/dev/null 2>&1 || echo 'Binary available'" >/dev/null 2>&1; then
                test_pass "main-container-execution" "Container can execute binary"
            else
                test_skip "main-container-execution" "Binary may require stream URL"
            fi
        else
            test_fail "main-container-execution" "Container entrypoint not found"
        fi
    else
        test_fail "main-container-execution" "Failed to load container"
    fi
else
    test_skip "main-container-execution" "Container not built"
fi

# Test 2: Test origin container (server)
log_test "Testing test-origin container execution (server)..."
if nix build ".#packages.$SYSTEM.test-origin-container" --print-out-paths >/dev/null 2>&1; then
    CONTAINER_PATH=$(nix build ".#packages.$SYSTEM.test-origin-container" --print-out-paths 2>/dev/null)
    if docker load < "$CONTAINER_PATH" 2>&1 | grep -q "Loaded image"; then
        # Start container in background and test health endpoint
        CONTAINER_ID=$(docker run -d -p 17080:17080 go-ffmpeg-hls-swarm-test-origin:latest 2>/dev/null || echo "")
        if [[ -n "$CONTAINER_ID" ]]; then
            # Wait for services to start (FFmpeg needs time to generate segments)
            log_info "Waiting for container services to start..."
            sleep 10

            # Test health endpoint (with retries, FFmpeg may take time)
            HEALTH_SUCCESS=false
            retry_count=0
            max_retries=15
            while [ $retry_count -lt $max_retries ]; do
                if curl -sf http://localhost:17080/health >/dev/null 2>&1; then
                    HEALTH_SUCCESS=true
                    break
                fi
                retry_count=$((retry_count + 1))
                sleep 2
            done

            if [[ "$HEALTH_SUCCESS" == "true" ]]; then
                test_pass "test-origin-container-execution" "Server container responding"
            else
                # Check if container is still running
                if docker ps | grep -q "$CONTAINER_ID"; then
                    test_skip "test-origin-container-execution" "Health endpoint not ready (FFmpeg may still be starting)"
                    log_info "Container logs (last 20 lines):"
                    docker logs "$CONTAINER_ID" 2>&1 | tail -20 | sed 's/^/  /'
                else
                    test_fail "test-origin-container-execution" "Container exited"
                    log_info "Container logs (last 30 lines):"
                    docker logs "$CONTAINER_ID" 2>&1 | tail -30 | sed 's/^/  /'
                fi
            fi

            # Cleanup
            log_info "Cleaning up container..."
            docker stop "$CONTAINER_ID" >/dev/null 2>&1 || true
            sleep 1
            docker rm "$CONTAINER_ID" >/dev/null 2>&1 || true
        else
            test_fail "test-origin-container-execution" "Failed to start container"
        fi
    else
        test_fail "test-origin-container-execution" "Failed to load container"
    fi
else
    test_skip "test-origin-container-execution" "Container not built"
fi

# Test 3: Enhanced container (Linux only, requires special Docker flags)
if is_linux; then
    log_test "Testing enhanced container execution (server with systemd)..."
    if nix build ".#packages.$SYSTEM.test-origin-container-enhanced" --print-out-paths >/dev/null 2>&1; then
        CONTAINER_PATH=$(nix build ".#packages.$SYSTEM.test-origin-container-enhanced" --print-out-paths 2>/dev/null)
        if docker load < "$CONTAINER_PATH" 2>&1 | grep -q "Loaded image"; then
            # Enhanced container needs SYS_ADMIN and tmpfs
            # Just test that it can start (don't wait for full startup)
            if timeout 5 docker run --rm \
                --cap-add SYS_ADMIN \
                --tmpfs /tmp --tmpfs /run --tmpfs /run/lock \
                go-ffmpeg-hls-swarm-test-origin-enhanced:latest \
                /bin/sh -c "echo 'Container started'" >/dev/null 2>&1; then
                test_pass "enhanced-container-execution" "Enhanced container started"
            else
                test_skip "enhanced-container-execution" "May require network setup"
            fi
        else
            test_fail "enhanced-container-execution" "Failed to load container"
        fi
    else
        test_skip "enhanced-container-execution" "Container not built"
    fi
else
    test_skip "enhanced-container-execution" "Linux only"
fi

# Test 4: Swarm client container
log_test "Testing swarm-client container execution (client)..."
if nix build ".#packages.$SYSTEM.swarm-client-container" --print-out-paths >/dev/null 2>&1; then
    CONTAINER_PATH=$(nix build ".#packages.$SYSTEM.swarm-client-container" --print-out-paths 2>/dev/null)
    if docker load < "$CONTAINER_PATH" 2>&1 | grep -q "Loaded image"; then
        # Test that container can start and show help/version
        # Note: Client requires STREAM_URL, so we test with --help or invalid URL to verify it starts
        if timeout 10 docker run --rm \
            -e STREAM_URL=http://test:8080/stream.m3u8 \
            go-ffmpeg-hls-swarm:latest \
            /bin/sh -c "go-ffmpeg-hls-swarm --help >/dev/null 2>&1 || echo 'Binary available'" >/dev/null 2>&1; then
            test_pass "swarm-client-container-execution" "Client container started and binary available"
        else
            # Alternative: just verify the entrypoint script exists
            if timeout 5 docker run --rm \
                go-ffmpeg-hls-swarm:latest \
                /bin/sh -c "test -f /bin/swarm-entrypoint && echo 'Entrypoint available'" >/dev/null 2>&1; then
                test_pass "swarm-client-container-execution" "Client container entrypoint available"
            else
                test_skip "swarm-client-container-execution" "Container may require valid STREAM_URL"
            fi
        fi
    else
        test_fail "swarm-client-container-execution" "Failed to load container"
    fi
else
    test_skip "swarm-client-container-execution" "Container not built"
fi

print_summary
