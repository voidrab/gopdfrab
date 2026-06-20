#!/usr/bin/env bash
# Shared paths and helpers for the benchmark scripts. Source this, don't run
# it directly:
#   source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCH_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_DIR="$(cd "$BENCH_DIR/.." && pwd)"
TOOLS_DIR="$BENCH_DIR/tools"
RESULTS_DIR="$BENCH_DIR/results"
mkdir -p "$RESULTS_DIR"

ISARTOR_DIR="$REPO_DIR/test documents/Isartor testsuite/PDFA-1b"
VERA_DIR="$REPO_DIR/test documents/veraPDF/PDF_A-1b"

GOPDFRAB_BIN="$TOOLS_DIR/gopdfrab-bench"
VERAPDF_BIN="$TOOLS_DIR/verapdf/verapdf"
PDFBOX_JAR="$(ls "$TOOLS_DIR"/pdfbox/preflight-app-*.jar 2>/dev/null | head -1)"
PREFLIGHT_BATCH_CLASS="$TOOLS_DIR/pdfbox/PreflightBatch.class"
JS_RUNNER="$BENCH_DIR/js/runner.mjs"

# Sample files spanning the corpus size range, matching benchmarks/micro's
# small/median/large picks so the in-process and process-level numbers line up.
SAMPLE_SMALL="$ISARTOR_DIR/6.7 Metadata/6.7.2 Properties/isartor-6-7-2-t01-fail-a.pdf"
SAMPLE_MEDIAN="$VERA_DIR/6.7 Metadata/6.7.2 Properties/veraPDF test suite 6-7-2-t13-fail-g.pdf"
SAMPLE_LARGE="$ISARTOR_DIR/6.1 File structure/6.1.12 Implementation Limits/isartor-6-1-12-t01-fail-a.pdf"

# build_gopdfrab_bench compiles cmd/gopdfrab-bench into tools/ if not already
# built (it's a gitignored build artifact, same as the downloaded jars).
build_gopdfrab_bench() {
    if [ ! -x "$GOPDFRAB_BIN" ]; then
        echo "building gopdfrab-bench..." >&2
        ( cd "$REPO_DIR" && go build -o "$GOPDFRAB_BIN" ./benchmarks/cmd/gopdfrab-bench )
    fi
}

# require_tool exits with a clear message if a prerequisite from setup.sh is
# missing, instead of failing deep inside hyperfine/java with a confusing error.
require_tool() {
    local path="$1" name="$2"
    if [ -z "$path" ] || [ ! -e "$path" ]; then
        echo "missing $name (expected at: $path) — run benchmarks/scripts/setup.sh first" >&2
        exit 1
    fi
}

require_all_tools() {
    build_gopdfrab_bench
    require_tool "$GOPDFRAB_BIN" "gopdfrab-bench binary"
    require_tool "$VERAPDF_BIN" "veraPDF CLI"
    require_tool "$PDFBOX_JAR" "PDFBox preflight-app.jar"
    require_tool "$PREFLIGHT_BATCH_CLASS" "compiled PreflightBatch.class"
    require_tool "$BENCH_DIR/js/node_modules/mupdf" "mupdf npm package"
    command -v hyperfine >/dev/null 2>&1 || { echo "missing hyperfine — run setup.sh first" >&2; exit 1; }
}
