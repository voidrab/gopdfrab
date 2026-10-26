#!/usr/bin/env bash
# Metric 8: library/deployment footprint -- what you must ship to run each
# verifier. Sizes only, so unlike run_micro.sh this is cheap to rerun on its
# own after a change that moves them.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

echo "=== Footprint: what you must ship to run each verifier ==="

FOOTPRINT_CSV="$RESULTS_DIR/footprint.csv"
echo "tool,artifact,bytes,description" > "$FOOTPRINT_CSV"

add_row() {
    echo "$1,$2,$3,$4" >> "$FOOTPRINT_CSV"
}

# size builds pkg (relative to dir) with the given environment and ldflags
# and prints the artifact's size in bytes.
size() {
    local dir="$1" pkg="$2" ldflags="$3"
    shift 3
    local out
    out="$(mktemp -u)"
    ( cd "$dir" && env "$@" go build -trimpath -ldflags="$ldflags" -o "$out" "$pkg" )
    stat -c%s "$out"
    rm -f "$out"
}

# gopdfrab: static Go binaries, both as normally built and stripped. The module
# has zero external dependencies beyond klauspost/compress (see go.mod).
#
# The artifacts answer different questions and are not interchangeable:
#
#   cli_*         cmd/gopdfrab, the binary a user downloads: verify AND convert,
#                 so it carries the substitute fonts, ICC profiles, rasterizer
#                 and writer conversion needs.
#   verify_*      the benchmark runner, a complete verify-only program built on
#                 gopdfrab/verify, which links none of the converter. It is
#                 also what the timing metrics run, so the footprint and timing
#                 tables describe the same artifact.
#   wasm_*        the browser builds, stripped: ./wasm/verify (verify only) and
#                 ./wasm (verify and convert).
#
# Public footprint claims may cite cli_stripped for "the CLI", verify_stripped
# for "a program that only verifies", and the wasm rows for the browser.
add_row gopdfrab cli_plain "$(size "$REPO_DIR" ./cmd/gopdfrab "")" "cmd/gopdfrab (verify + convert); go build"
add_row gopdfrab cli_stripped "$(size "$REPO_DIR" ./cmd/gopdfrab "-s -w")" "cmd/gopdfrab (verify + convert); -ldflags '-s -w'"
add_row gopdfrab verify_plain "$(size "$BENCH_DIR" ./cmd/gopdfrab-bench "")" "verify-only program on gopdfrab/verify; go build"
add_row gopdfrab verify_stripped "$(size "$BENCH_DIR" ./cmd/gopdfrab-bench "-s -w")" "verify-only program on gopdfrab/verify; -ldflags '-s -w'"
add_row gopdfrab wasm_verify "$(size "$REPO_DIR" ./wasm/verify "-s -w" GOOS=js GOARCH=wasm)" "browser module; verify only; -ldflags '-s -w'"
add_row gopdfrab wasm_full "$(size "$REPO_DIR" ./wasm "-s -w" GOOS=js GOARCH=wasm)" "browser module; verify + convert; -ldflags '-s -w'"

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
echo "Done. $FOOTPRINT_CSV"
