#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IRONCLAW="${IRONCLAW:-ironclaw}"
WORKDIR=$(mktemp -d)
trap 'rm -rf "$WORKDIR"' EXIT

echo "=== IronClaw Self-Evolution Demo ==="
echo ""
echo "Working directory: $WORKDIR"
echo ""

# Step 1: Run baseline
echo "--- Step 1: Running baseline evaluation ---"
$IRONCLAW eval run --suite builtin --output "$WORKDIR/baseline.json" --run-id "baseline"
echo ""

# Step 2: Show insights (from any existing trajectory data)
echo "--- Step 2: Checking cognitive health ---"
$IRONCLAW insights health --days 30 2>/dev/null || echo "(No trajectory data yet — this is expected for a fresh install)"
echo ""

# Step 3: Run comparison
echo "--- Step 3: Running comparison evaluation ---"
$IRONCLAW eval run --suite builtin --output "$WORKDIR/after.json" --run-id "after-evolution"
echo ""

# Step 4: Compare
echo "--- Step 4: Comparison Report ---"
echo ""
$IRONCLAW eval compare --before "$WORKDIR/baseline.json" --after "$WORKDIR/after.json"

echo ""
echo "=== Demo Complete ==="
echo ""
echo "For real self-evolution, enable evolution in your config:"
echo "  evolution:"
echo "    enabled: true"
echo "    optimizer:"
echo "      hard_control_enabled: true"
echo ""
echo "Then use IronClaw in cognitive mode for several sessions."
echo "Run this demo again to see measured improvement."
