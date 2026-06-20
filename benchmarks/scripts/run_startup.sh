#!/usr/bin/env bash
# Metric 1: startup overhead. Times each tool's cheapest possible invocation
# (version/usage/module-load) to isolate fixed process/VM startup cost from
# actual PDF processing — this is what explains most of the gap in the cold
# single-file latency numbers (run_cold.sh).
#
# Each hyperfine positional argument is one whole command string (tokenized
# by hyperfine itself, not a real shell, since --shell=none); embed quotes
# around any path that may contain spaces.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
require_all_tools

echo "Measuring startup overhead (version/usage/module-load, no PDF processing)..."

hyperfine \
    --shell=none \
    --warmup 5 --min-runs 20 \
    --ignore-failure \
    --export-json "$RESULTS_DIR/startup_gopdfrab.json" \
    --command-name gopdfrab \
    "'$GOPDFRAB_BIN' -h"

hyperfine \
    --shell=none \
    --warmup 5 --min-runs 20 \
    --ignore-failure \
    --export-json "$RESULTS_DIR/startup_verapdf.json" \
    --command-name veraPDF \
    "'$VERAPDF_BIN' --version"

hyperfine \
    --shell=none \
    --warmup 5 --min-runs 20 \
    --ignore-failure \
    --export-json "$RESULTS_DIR/startup_pdfbox.json" \
    --command-name pdfbox-preflight \
    "java -jar '$PDFBOX_JAR'"

echo "Done. Raw results: $RESULTS_DIR/startup_*.json"
