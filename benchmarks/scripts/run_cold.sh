#!/usr/bin/env bash
# Metric 2: cold single-file latency. Times one full Open+Verify (or each
# competitor's equivalent) per process invocation, across small/median/large
# sample files. This includes process/VM startup (see run_startup.sh) — it's
# the number that matters for one-shot CLI use, but should always be read
# alongside run_batch.sh's amortized throughput, which isolates the engine.
#
# Each hyperfine positional argument is one whole command string (tokenized
# by hyperfine itself, not a real shell, since --shell=none); embed quotes
# around any path that may contain spaces.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
require_all_tools

run_for_size() {
    local size="$1" file="$2"
    echo "--- $size ($(stat -c%s "$file") bytes) ---"

    hyperfine \
        --shell=none \
        --warmup 3 --min-runs 15 \
        --ignore-failure \
        --export-json "$RESULTS_DIR/cold_gopdfrab_${size}.json" \
        --command-name gopdfrab \
        "'$GOPDFRAB_BIN' -mode=single '$file'"

    hyperfine \
        --shell=none \
        --warmup 3 --min-runs 15 \
        --ignore-failure \
        --export-json "$RESULTS_DIR/cold_verapdf_${size}.json" \
        --command-name veraPDF \
        "'$VERAPDF_BIN' -f 1b --format text '$file'"

    hyperfine \
        --shell=none \
        --warmup 3 --min-runs 15 \
        --ignore-failure \
        --export-json "$RESULTS_DIR/cold_pdfbox_${size}.json" \
        --command-name pdfbox-preflight \
        "java -jar '$PDFBOX_JAR' '$file'"

    hyperfine \
        --shell=none \
        --warmup 3 --min-runs 15 \
        --ignore-failure \
        --export-json "$RESULTS_DIR/cold_js_${size}.json" \
        --command-name js-mupdf \
        "node '$JS_RUNNER' --mode=single '$file'"
}

run_for_size small  "$SAMPLE_SMALL"
run_for_size median "$SAMPLE_MEDIAN"
run_for_size large  "$SAMPLE_LARGE"

echo "Done. Raw results: $RESULTS_DIR/cold_*.json"
