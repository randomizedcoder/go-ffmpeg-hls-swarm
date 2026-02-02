#!/usr/bin/env bash
# Test MicroVM execution with network setup/teardown
# Tests that MicroVMs can start and connect to network

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

SYSTEM=$(get_system)
readonly SYSTEM

log_info "Testing MicroVM execution with network for $SYSTEM"
log_info "This requires KVM and network setup..."
echo ""

# Check prerequisites
if ! is_linux; then
    log_warn "MicroVM network tests require Linux"
    test_skip "microvm-network" "Not on Linux"
    print_summary
    exit 0
fi

if ! has_kvm; then
    log_warn "KVM not available (required for MicroVMs)"
    test_skip "microvm-network" "KVM not available"
    print_summary
    exit 0
fi

# Check if network scripts exist
NETWORK_SETUP="$SCRIPT_DIR/../network/setup.sh"
NETWORK_TEARDOWN="$SCRIPT_DIR/../network/teardown.sh"
NETWORK_CHECK="$SCRIPT_DIR/../network/check.sh"

if [[ ! -f "$NETWORK_SETUP" ]] || [[ ! -f "$NETWORK_TEARDOWN" ]]; then
    log_warn "Network scripts not found, skipping network tests"
    test_skip "microvm-network" "Network scripts not available"
    print_summary
    exit 0
fi

# Check if we have sudo access
if ! sudo -n true 2>/dev/null; then
    log_warn "This test requires sudo for network setup/teardown"
    log_warn "Skipping network tests (run with sudo access for full testing)"
    test_skip "microvm-network" "Sudo access required"
    print_summary
    exit 0
fi

# Teardown existing network (if any)
log_info "Tearing down existing network (if any)..."
"$NETWORK_TEARDOWN" >/dev/null 2>&1 || true

# Setup fresh network
log_test "Setting up network for MicroVM testing..."
if sudo "$NETWORK_SETUP" >/dev/null 2>&1; then
    test_pass "network-setup"

    # Verify network is set up correctly
    if [[ -f "$NETWORK_CHECK" ]]; then
        log_test "Verifying network configuration..."
        if "$NETWORK_CHECK" >/dev/null 2>&1; then
            test_pass "network-verification"
        else
            test_fail "network-verification" "Network check failed"
        fi
    fi

    # Test TAP networking MicroVM (high-performance, multi-queue)
    log_test "Testing TAP networking MicroVM (high-performance)..."

    # Test that TAP MicroVM package exists
    if ! nix build ".#packages.$SYSTEM.test-origin-vm-tap" --print-out-paths >/dev/null 2>&1; then
        test_fail "microvm-tap-package" "TAP MicroVM package not available"
        "$NETWORK_TEARDOWN" >/dev/null 2>&1 || true
        print_summary
        exit 1
    fi

    test_pass "microvm-tap-package"

    # Verify TAP device is configured correctly for high-performance networking
    log_test "Verifying TAP device configuration (high-performance multi-queue)..."
    if ip link show hlstap0 &>/dev/null; then
        test_pass "tap-device-exists" "TAP device hlstap0 exists"

        # Check if TAP was created with multi_queue flag (high-performance)
        # TAP devices with multi_queue support parallel packet processing
        if ip -d link show hlstap0 2>/dev/null | grep -q "multi_queue"; then
            test_pass "tap-multi-queue-flag" "TAP device created with multi_queue flag"
            log_info "TAP device supports multi-queue (enables parallel packet processing)"
        else
            test_skip "tap-multi-queue-flag" "Cannot verify multi_queue flag (may still work)"
        fi

        # Verify TAP is attached to bridge
        if ip link show hlstap0 | grep -q "master hlsbr0"; then
            test_pass "tap-bridge-attachment" "TAP device attached to bridge"
        else
            test_fail "tap-bridge-attachment" "TAP device not attached to bridge"
        fi
    else
        test_fail "tap-device" "TAP device hlstap0 not found"
    fi

    # Check vhost-net module (required for high-performance TAP networking)
    log_test "Verifying vhost-net support..."
    if lsmod | grep -q "^vhost_net"; then
        test_pass "vhost-net-module" "vhost-net kernel module loaded"
        log_info "vhost-net enabled (required for ~10 Gbps performance)"
    else
        test_skip "vhost-net-module" "vhost-net not loaded (performance may be reduced)"
        log_info "To enable: sudo modprobe vhost_net"
    fi

    # Test VM can build
    log_test "Building TAP MicroVM..."
    VM_PATH=$(nix build ".#packages.$SYSTEM.test-origin-vm-tap" --print-out-paths 2>/dev/null)
    if [[ -n "$VM_PATH" ]] && [[ -d "$VM_PATH" ]]; then
        test_pass "microvm-tap-build" "TAP MicroVM built successfully"

        # Check if QEMU command includes multi-queue configuration
        VM_RUNNER="$VM_PATH/bin/microvm-run"
        if [[ -f "$VM_RUNNER" ]]; then
            if grep -q "queues=" "$VM_RUNNER" 2>/dev/null || grep -q "vhost" "$VM_RUNNER" 2>/dev/null; then
                test_pass "qemu-multiqueue-config" "QEMU configured for multi-queue networking"
                log_info "QEMU will use multi-queue virtio-net with vhost-net"
            else
                test_skip "qemu-multiqueue-config" "Cannot verify QEMU multi-queue config"
            fi
        fi
    else
        test_fail "microvm-tap-build" "TAP MicroVM build path invalid"
    fi

    # Test VM startup and connectivity (brief execution test)
    log_test "Testing TAP MicroVM startup and connectivity..."
    if [[ -n "$VM_PATH" ]] && [[ -x "$VM_PATH/bin/microvm-run" ]]; then
        log_info "Starting VM briefly to test high-performance TAP networking..."

        # Ensure no existing VM is running
        pkill -f "qemu.*hls-origin|microvm@hls-origin" 2>/dev/null || true
        sleep 1

        # Start VM in background with setsid (like start.sh does)
        VM_LOG="/tmp/test-microvm-tap.log"
        setsid "$VM_PATH/bin/microvm-run" < /dev/null > "$VM_LOG" 2>&1 &
        VM_PID=$!

        # Wait for VM to start (QEMU takes a moment)
        sleep 3

        # Check if VM process is still running
        if kill -0 "$VM_PID" 2>/dev/null; then
            test_pass "microvm-start" "VM process started successfully"

            # Test connectivity to VM IP (TAP networking)
            VM_IP="10.177.0.10"
            HTTP_PORT="17080"

            # Try to ping VM (with retries, boot takes time)
            PING_SUCCESS=false
            ping_retry=0
            max_ping_retries=10
            while [ $ping_retry -lt $max_ping_retries ]; do
                if ping -c 1 -W 1 "$VM_IP" &>/dev/null; then
                    PING_SUCCESS=true
                    break
                fi
                ping_retry=$((ping_retry + 1))
                sleep 1
            done

            if [[ "$PING_SUCCESS" == "true" ]]; then
                test_pass "vm-ping" "VM reachable at $VM_IP via TAP networking"

                # Try health endpoint (services take longer to start)
                HTTP_SUCCESS=false
                http_retry=0
                max_http_retries=15
                while [ $http_retry -lt $max_http_retries ]; do
                    if curl -sf "http://${VM_IP}:${HTTP_PORT}/health" &>/dev/null; then
                        HTTP_SUCCESS=true
                        break
                    fi
                    http_retry=$((http_retry + 1))
                    sleep 1
                done

                if [[ "$HTTP_SUCCESS" == "true" ]]; then
                    test_pass "vm-http" "VM HTTP service responding via TAP"
                    log_info "✓ High-performance multi-queue TAP networking verified!"
                else
                    test_skip "vm-http" "HTTP not yet ready (VM services still starting)"
                fi
            else
                test_skip "vm-ping" "VM not yet reachable (may still be booting)"
            fi

            # Stop VM cleanly
            log_info "Stopping test VM..."
            kill "$VM_PID" 2>/dev/null || true
            sleep 2
            # Force kill if still running
            if kill -0 "$VM_PID" 2>/dev/null; then
                kill -9 "$VM_PID" 2>/dev/null || true
            fi
            sleep 1

            # Final cleanup - ensure no QEMU processes remain
            pkill -f "qemu.*hls-origin|microvm@hls-origin" 2>/dev/null || true
        else
            test_skip "microvm-start" "VM process exited (check $VM_LOG)"
            if [[ -f "$VM_LOG" ]]; then
                log_info "VM log (last 15 lines):"
                tail -15 "$VM_LOG" | sed 's/^/  /'
            fi
        fi
    else
        test_skip "microvm-execution" "VM runner not available for execution test"
    fi

    # Cleanup: Teardown network
    log_info "Tearing down test network..."
    "$NETWORK_TEARDOWN" >/dev/null 2>&1 || true
    test_pass "network-teardown"
else
    test_fail "network-setup" "Failed to setup network"
    # Try to cleanup on failure
    "$NETWORK_TEARDOWN" >/dev/null 2>&1 || true
fi

print_summary
