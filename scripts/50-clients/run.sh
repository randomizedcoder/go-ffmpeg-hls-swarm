#!/usr/bin/env bash
# 50-client load test
# Usage: ./scripts/50-clients/run.sh [duration] [ramp-rate]
#
# Examples:
#   ./scripts/50-clients/run.sh          # Quick test (30s)
#   ./scripts/50-clients/run.sh 60s      # Standard test (60s)
#   ./scripts/50-clients/run.sh 5m 20    # Extended test (5 min, 20 clients/sec)
#
# This is a gentle test suitable for:
# - Initial validation
# - Development testing
# - Low-resource systems

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/../lib/common.sh"

CLIENTS=50
DURATION="${1:-30s}"
RAMP_RATE="${2:-10}"

echo "╔════════════════════════════════════════════════════════════════════════╗"
echo "║             50-Client Load Test (Gentle)                               ║"
echo "╚════════════════════════════════════════════════════════════════════════╝"
echo ""

full_test "${CLIENTS}" "${DURATION}" "${RAMP_RATE}"
