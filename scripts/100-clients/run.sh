#!/usr/bin/env bash
# 100-client load test
# Usage: ./scripts/100-clients/run.sh [duration] [ramp-rate]
#
# Examples:
#   ./scripts/100-clients/run.sh          # Quick test (30s)
#   ./scripts/100-clients/run.sh 60s      # Standard test (60s)
#   ./scripts/100-clients/run.sh 5m 50    # Extended test (5 min, 50 clients/sec)
#
# This is a moderate test suitable for:
# - Standard load testing
# - Verifying stability
# - Baseline performance measurement

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "${SCRIPT_DIR}/../lib/common.sh"

CLIENTS=100
DURATION="${1:-30s}"
RAMP_RATE="${2:-20}"

echo "╔════════════════════════════════════════════════════════════════════════╗"
echo "║             100-Client Load Test (Standard)                            ║"
echo "╚════════════════════════════════════════════════════════════════════════╝"
echo ""

full_test "${CLIENTS}" "${DURATION}" "${RAMP_RATE}"
