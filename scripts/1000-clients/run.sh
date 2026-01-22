#!/usr/bin/env bash
# 1000-client load test
# Usage: ./scripts/1000-clients/run.sh [duration] [ramp-rate]
#
# Examples:
#   ./scripts/1000-clients/run.sh          # Quick test (30s)
#   ./scripts/1000-clients/run.sh 60s      # Standard test (60s)
#   ./scripts/1000-clients/run.sh 5m 200   # Extended test (5 min, 200 clients/sec)
#
# This is an extreme stress test suitable for:
# - Maximum capacity testing
# - Breaking point discovery
# - Production-scale validation
#
# WARNING: This requires significant system resources!
# - Ensure ulimit -n is at least 4096
# - Monitor system memory and CPU
# - May need tuned kernel parameters

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/../lib/common.sh"

CLIENTS=1000
DURATION="${1:-30s}"
RAMP_RATE="${2:-100}"

echo "╔════════════════════════════════════════════════════════════════════════╗"
echo "║             1000-Client Load Test (Extreme)                            ║"
echo "╠════════════════════════════════════════════════════════════════════════╣"
echo "║ WARNING: This test requires significant system resources!              ║"
echo "╚════════════════════════════════════════════════════════════════════════╝"
echo ""

# Check file descriptor limit
FD_LIMIT=$(ulimit -n)
if [ "${FD_LIMIT}" -lt 4096 ]; then
    log_warn "File descriptor limit (${FD_LIMIT}) may be too low for 1000 clients"
    log_warn "Consider: ulimit -n 8192"
fi

full_test "${CLIENTS}" "${DURATION}" "${RAMP_RATE}"
