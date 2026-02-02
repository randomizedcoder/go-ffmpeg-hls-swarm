#!/usr/bin/env bash
# 500-client load test
# Usage: ./scripts/500-clients/run.sh [duration] [ramp-rate]
#
# Examples:
#   ./scripts/500-clients/run.sh          # Quick test (30s)
#   ./scripts/500-clients/run.sh 60s      # Standard test (60s)
#   ./scripts/500-clients/run.sh 5m 200   # Extended test (5 min, 200 clients/sec)
#
# This is a heavy stress test suitable for:
# - High-capacity validation
# - CDN/cache testing
# - Infrastructure limits testing

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/../lib/common.sh"

CLIENTS=500
DURATION="${1:-30s}"
RAMP_RATE="${2:-100}"

echo "╔════════════════════════════════════════════════════════════════════════╗"
echo "║             500-Client Load Test (Heavy)                               ║"
echo "╚════════════════════════════════════════════════════════════════════════╝"
echo ""

full_test "${CLIENTS}" "${DURATION}" "${RAMP_RATE}"
