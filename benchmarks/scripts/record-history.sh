#!/usr/bin/env bash
# Record one round of the in-process microbenchmarks into the committed
# performance history, and compare it against the previous round.
#
# Usage: benchmarks/scripts/record-history.sh [count]
#
# Unlike run_micro.sh, whose output is gitignored scratch, this writes a dated
# file under benchmarks/results/history/ that is meant to be committed, so
# regressions across releases stay visible. See that directory's README.md for
# how to read the numbers.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

COUNT="${1:-10}"
HISTORY_DIR="$RESULTS_DIR/history"
mkdir -p "$HISTORY_DIR"

sha="$(cd "$REPO_DIR" && git rev-parse --short HEAD)"
if ! (cd "$REPO_DIR" && git diff --quiet HEAD); then
    sha="$sha-dirty"
fi
out="$HISTORY_DIR/$(date +%Y-%m-%d)-$sha.txt"

# The file holds the raw `go test -bench` output and nothing else: benchstat
# reads it directly, and the goos/goarch/pkg/cpu lines it already carries are
# the provenance. Date and commit live in the filename.
echo "=== recording $COUNT runs into $(basename "$out") ==="
# benchmarks/ is its own module, so the run happens from BENCH_DIR.
( cd "$BENCH_DIR" && go test -run=^$ -bench=. -benchmem -count="$COUNT" ./micro/... ) \
    | tee "$out"

# The memory benchmarks live in internal/convert, which the benchmarks module
# cannot import, so they are a second run from the main module appended to the
# same file. benchstat reads the concatenation fine -- each run carries its own
# goos/goarch/pkg/cpu header.
echo
echo "=== recording $COUNT runs of the memory benchmarks ==="
( cd "$REPO_DIR" && go test -run=^$ -bench=Memory -benchmem -count="$COUNT" ./internal/convert/ ) \
    | tee -a "$out"

if ! command -v benchstat >/dev/null 2>&1; then
    echo >&2
    echo "benchstat not found (run setup.sh); skipping the comparison" >&2
    exit 0
fi

# Compare against the most recent earlier round, if there is one.
prev="$(ls -1 "$HISTORY_DIR"/*.txt 2>/dev/null | grep -v "^$out\$" | tail -1 || true)"
echo
if [ -z "$prev" ]; then
    echo "=== first recorded round; no baseline to compare against ==="
    benchstat "$out"
else
    echo "=== $(basename "$prev") -> $(basename "$out") ==="
    echo "(wall-clock on a dev machine is thermally noisy; trust allocs/op and"
    echo " treat a time change under ~15% as unproven)"
    benchstat "$prev" "$out"
fi
