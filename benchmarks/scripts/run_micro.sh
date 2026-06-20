#!/usr/bin/env bash
# Metric 6: in-process Go microbenchmark (noise-free engine signal, Go only).
# Metric 8: library/deployment footprint sizes for every tool.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# ---------------------------------------------------------------------------
echo "=== Microbenchmark: Open+Verify, small/median/large samples ==="
echo "(go test -bench, -count=10 so benchstat can report mean ± stddev)"

( cd "$REPO_DIR" && go test -run=^$ -bench=. -benchmem -count=10 ./benchmarks/micro/... ) \
    | tee "$RESULTS_DIR/micro_raw.txt"

if command -v benchstat >/dev/null 2>&1; then
    benchstat "$RESULTS_DIR/micro_raw.txt" | tee "$RESULTS_DIR/micro_benchstat.txt"
else
    echo "benchstat not found (run setup.sh); skipping statistical summary" >&2
fi

# ---------------------------------------------------------------------------
echo
echo "=== Footprint: what you must ship to run each verifier ==="

FOOTPRINT_CSV="$RESULTS_DIR/footprint.csv"
echo "tool,artifact,bytes,description" > "$FOOTPRINT_CSV"

add_row() {
    echo "$1,$2,$3,$4" >> "$FOOTPRINT_CSV"
}

# gopdfrab: a static Go binary, both as normally built and stripped. The
# module has zero external dependencies (see go.mod).
go_plain="$(mktemp -u)"
go_stripped="$(mktemp -u)"
( cd "$REPO_DIR" && go build -o "$go_plain" ./benchmarks/cmd/gopdfrab-bench )
( cd "$REPO_DIR" && go build -ldflags="-s -w" -o "$go_stripped" ./benchmarks/cmd/gopdfrab-bench )
add_row gopdfrab binary_plain "$(stat -c%s "$go_plain")" "go build; static; zero deps"
add_row gopdfrab binary_stripped "$(stat -c%s "$go_stripped")" "go build -ldflags '-s -w'"
rm -f "$go_plain" "$go_stripped"

# veraPDF: full greenfield install (GUI + CLI + docs + bundled libs).
if [ -d "$TOOLS_DIR/verapdf" ]; then
    vera_bytes="$(du -sb "$TOOLS_DIR/verapdf" | cut -f1)"
    add_row veraPDF install_dir "$vera_bytes" "full greenfield install (GUI+CLI+docs); requires a separate JRE"
    if [ -f "$TOOLS_DIR/verapdf/bin/cli-1.30.2.jar" ]; then
        add_row veraPDF cli_jar "$(stat -c%s "$TOOLS_DIR/verapdf/bin/cli-1.30.2.jar")" "CLI jar only; excl. GUI/docs"
    fi
fi

# PDFBox: the single shaded preflight-app jar (includes its dependencies).
if [ -n "$PDFBOX_JAR" ]; then
    add_row pdfbox-preflight jar "$(stat -c%s "$PDFBOX_JAR")" "shaded preflight-app jar incl. deps; requires a separate JRE"
fi

column -s, -t "$FOOTPRINT_CSV"
echo
echo "Done. $FOOTPRINT_CSV, $RESULTS_DIR/micro_*.txt"
