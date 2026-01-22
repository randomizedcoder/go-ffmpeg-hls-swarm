#!/usr/bin/env bash
# 300-client load test
# Usage: ./scripts/300-clients/run.sh [duration] [ramp-rate]
#
# Examples:
#   ./scripts/300-clients/run.sh          # Quick test (30s)
#   ./scripts/300-clients/run.sh 60s      # Standard test (60s)
#   ./scripts/300-clients/run.sh 5m 100   # Extended test (5 min, 100 clients/sec)
#
# This is a stress test suitable for:
# - High-load validation
# - Finding breaking points
# - Production capacity testing

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/../lib/common.sh"

CLIENTS=300
DURATION="${1:-30s}"
RAMP_RATE="${2:-50}"

echo "╔════════════════════════════════════════════════════════════════════════╗"
echo "║             300-Client Load Test (Stress)                              ║"
echo "╚════════════════════════════════════════════════════════════════════════╝"
echo ""

full_test "${CLIENTS}" "${DURATION}" "${RAMP_RATE}"
