#!/usr/bin/env bash
# Metrics 3, 4, 5: amortized batch throughput, peak memory (max RSS), and CPU
# utilization. Each tool processes the full vendored corpus (both Isartor and
# veraPDF test suites, 774 PDFs) in a single process invocation, wrapped in
# GNU `time -v` — this isolates the verification engine from per-file process
# startup (contrast with run_cold.sh) and captures wall-clock, max RSS, and
# CPU% in one pass.
#
# Each runner's own "#summary {...}" JSON line and GNU time's "-v" report
# both land on stderr, captured together in results/batch_<tool>_time.txt;
# per-file output goes to results/batch_<tool>.{csv,json} — CSV for
# gopdfrab/pdfbox/js, JSON for veraPDF (its own --format json, which is also
# how report.py gets per-file size+duration for the throughput-vs-size
# metric and pass/fail counts, since veraPDF has no #summary line).
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
require_all_tools

# Resolve GNU time by explicit path, not `command -v time`: in bash, `time`
# is a shell reserved word, so `command -v` reports that instead of the
# actual /usr/bin/time (or ~/.local/bin/time, if built locally by setup.sh)
# executable on PATH.
GNU_TIME="${GNU_TIME:-}"
if [ -z "$GNU_TIME" ]; then
    for candidate in /usr/bin/time "$HOME/.local/bin/time" /usr/local/bin/time; do
        if [ -x "$candidate" ]; then
            GNU_TIME="$candidate"
            break
        fi
    done
fi
if [ -z "$GNU_TIME" ] || [ ! -x "$GNU_TIME" ]; then
    echo "missing GNU time (the 'time' package) — run benchmarks/scripts/setup.sh first" >&2
    exit 1
fi

# Both corpora's PDFA-1b directories specifically (204 Isartor + 569 veraPDF
# = 773 PDFs) — not their shared "tests" parent, which also holds a
# few non-test files (e.g. the Isartor suite's own PDF manual) that would
# otherwise get counted alongside the actual test corpus.
CORPUS_ROOTS=("$ISARTOR_DIR" "$VERA_DIR")

echo "Batch run over: ${CORPUS_ROOTS[*]}"
echo "(this walks both vendored corpora, combined, in one pass per tool)"

run_batch() {
    local tool="$1" ext="$2"; shift 2
    echo "--- $tool ---"
    "$GNU_TIME" -v "$@" \
        > "$RESULTS_DIR/batch_${tool}.${ext}" \
        2> "$RESULTS_DIR/batch_${tool}_time.txt" \
        || echo "(non-zero exit for $tool; see $RESULTS_DIR/batch_${tool}_time.txt)"
    tail -n 25 "$RESULTS_DIR/batch_${tool}_time.txt"
}

run_batch gopdfrab csv \
    "$GOPDFRAB_BIN" -mode=batch "${CORPUS_ROOTS[@]}"

# --format json (rather than text) so report.py can also extract per-file
# size + processingTime for the throughput-vs-file-size metric, and derive
# pass/fail counts from the "compliant" field — one run covers everything
# text format alone wouldn't.
run_batch verapdf json \
    "$VERAPDF_BIN" -f 1b --format json -r "${CORPUS_ROOTS[@]}"

run_batch pdfbox csv \
    java -classpath "$PDFBOX_JAR:$(dirname "$PREFLIGHT_BATCH_CLASS")" PreflightBatch "${CORPUS_ROOTS[@]}"

echo "Done. Per-file CSVs + time reports: $RESULTS_DIR/batch_*"
