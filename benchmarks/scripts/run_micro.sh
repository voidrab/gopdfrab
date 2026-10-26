#!/usr/bin/env bash
# Metric 6: in-process Go microbenchmark (noise-free engine signal, Go only).
# Metric 8: library/deployment footprint sizes for every tool (run_footprint.sh).
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# ---------------------------------------------------------------------------
echo "=== Microbenchmark: Open+Verify, small/median/large samples ==="
echo "(go test -bench, -count=10 so benchstat can report mean ± stddev)"

( cd "$BENCH_DIR" && go test -run=^$ -bench=. -benchmem -count=10 ./micro/... ) \
    | tee "$RESULTS_DIR/micro_raw.txt"

if command -v benchstat >/dev/null 2>&1; then
    benchstat "$RESULTS_DIR/micro_raw.txt" | tee "$RESULTS_DIR/micro_benchstat.txt"
else
    echo "benchstat not found (run setup.sh); skipping statistical summary" >&2
fi

# ---------------------------------------------------------------------------
echo
"$(dirname "${BASH_SOURCE[0]}")/run_footprint.sh"
