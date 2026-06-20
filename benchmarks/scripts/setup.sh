#!/usr/bin/env bash
# Installs/downloads everything the benchmark suite needs:
#   - hyperfine, GNU time, benchstat (measurement tooling)
#   - veraPDF greenfield CLI, Apache PDFBox preflight-app.jar (competitors)
#
# Idempotent: re-running skips anything already present. Everything
# downloaded/installed lands under benchmarks/tools/ (gitignored).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCH_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
TOOLS_DIR="$BENCH_DIR/tools"
mkdir -p "$TOOLS_DIR/verapdf" "$TOOLS_DIR/pdfbox"

PDFBOX_VERSION="${PDFBOX_VERSION:-3.0.7}"
VERAPDF_URL="https://software.verapdf.org/releases/verapdf-installer.zip"
PDFBOX_URL="https://repo1.maven.org/maven2/org/apache/pdfbox/preflight-app/${PDFBOX_VERSION}/preflight-app-${PDFBOX_VERSION}.jar"

log() { printf '\n=== %s ===\n' "$*"; }

# build_gnu_time_from_source builds GNU time 1.9 into ~/.local/bin without
# requiring sudo. Upstream's 1996-era signal-handler typedef doesn't match
# modern glibc's __sighandler_t and fails to compile under GCC 14+
# (-Wincompatible-pointer-types is now a hard error by default); patch it.
build_gnu_time_from_source() {
    local workdir
    workdir="$(mktemp -d)"
    ( cd "$workdir" \
      && curl -sL -o time.tar.gz "https://ftp.gnu.org/gnu/time/time-1.9.tar.gz" \
      && tar xzf time.tar.gz \
      && cd time-1.9 \
      && sed -i 's/typedef RETSIGTYPE (\*sighandler) ();/typedef RETSIGTYPE (*sighandler) (int);/' src/time.c \
      && ./configure --prefix="$HOME/.local" \
      && make \
      && make install )
    rm -rf "$workdir"
    echo "built GNU time from source: $("$HOME/.local/bin/time" --version 2>&1 | head -1)"
    echo "make sure $HOME/.local/bin is on PATH"
}

# ---------------------------------------------------------------------------
log "Measurement tooling: hyperfine, GNU time, benchstat"

if command -v hyperfine >/dev/null 2>&1; then
    echo "hyperfine: $(hyperfine --version)"
elif command -v pacman >/dev/null 2>&1 && sudo -n true 2>/dev/null; then
    sudo pacman -S --needed --noconfirm hyperfine
elif command -v cargo >/dev/null 2>&1; then
    cargo install hyperfine
else
    echo "hyperfine not found; no passwordless sudo for pacman and no cargo."
    echo "Downloading a prebuilt release into ~/.local/bin instead (no sudo needed)."
    hf_workdir="$(mktemp -d)"
    curl -sL -o "$hf_workdir/hyperfine.tar.gz" \
        "https://github.com/sharkdp/hyperfine/releases/download/v1.20.0/hyperfine-v1.20.0-x86_64-unknown-linux-gnu.tar.gz"
    tar xzf "$hf_workdir/hyperfine.tar.gz" -C "$hf_workdir"
    mkdir -p "$HOME/.local/bin"
    cp "$hf_workdir"/hyperfine-*/hyperfine "$HOME/.local/bin/hyperfine"
    chmod +x "$HOME/.local/bin/hyperfine"
    rm -rf "$hf_workdir"
    echo "installed: $("$HOME/.local/bin/hyperfine" --version), make sure $HOME/.local/bin is on PATH"
fi

# `time` is a bash reserved word, so check explicit paths rather than
# `command -v time` (which would just report the shell keyword).
gnu_time=""
for candidate in /usr/bin/time "$HOME/.local/bin/time" /usr/local/bin/time; do
    [ -x "$candidate" ] && gnu_time="$candidate" && break
done

if [ -n "$gnu_time" ]; then
    echo "GNU time: $("$gnu_time" --version 2>&1 | head -1)"
elif command -v pacman >/dev/null 2>&1 && sudo -n true 2>/dev/null; then
    sudo pacman -S --needed --noconfirm time
elif command -v pacman >/dev/null 2>&1; then
    echo "GNU time not found; install it with: sudo pacman -S time" >&2
    echo "(falling back to building from source into ~/.local/bin, no sudo needed)"
    build_gnu_time_from_source
else
    build_gnu_time_from_source
fi

if command -v benchstat >/dev/null 2>&1; then
    echo "benchstat: already installed"
else
    go install golang.org/x/perf/cmd/benchstat@latest
fi

# ---------------------------------------------------------------------------
log "veraPDF greenfield CLI"

if [ -x "$TOOLS_DIR/verapdf/verapdf" ]; then
    echo "already installed: $("$TOOLS_DIR/verapdf/verapdf" --version 2>&1 | head -1)"
else
    workdir="$(mktemp -d)"
    trap 'rm -rf "$workdir"' EXIT
    echo "downloading $VERAPDF_URL"
    curl -sL -o "$workdir/verapdf-installer.zip" "$VERAPDF_URL"
    unzip -q "$workdir/verapdf-installer.zip" -d "$workdir"
    installer_jar="$(find "$workdir" -name 'verapdf-izpack-installer-*.jar' | head -1)"
    if [ -z "$installer_jar" ]; then
        echo "could not find verapdf-izpack-installer-*.jar in the downloaded zip" >&2
        exit 1
    fi
    echo "INSTALL_PATH=$TOOLS_DIR/verapdf" > "$workdir/install.properties"
    java -jar "$installer_jar" -options "$workdir/install.properties"
    chmod +x "$TOOLS_DIR/verapdf/verapdf"
    echo "installed: $("$TOOLS_DIR/verapdf/verapdf" --version 2>&1 | head -1)"
fi

# ---------------------------------------------------------------------------
log "Apache PDFBox preflight-app.jar"

PREFLIGHT_JAR="$TOOLS_DIR/pdfbox/preflight-app-${PDFBOX_VERSION}.jar"
if [ -f "$PREFLIGHT_JAR" ]; then
    echo "already downloaded: $PREFLIGHT_JAR"
else
    echo "downloading $PDFBOX_URL"
    curl -sL -o "$PREFLIGHT_JAR" "$PDFBOX_URL"
fi

log "Compiling PreflightBatch.java (amortized in-JVM batch driver)"
javac -classpath "$PREFLIGHT_JAR" -d "$TOOLS_DIR/pdfbox" "$TOOLS_DIR/pdfbox/PreflightBatch.java"
echo "compiled: $TOOLS_DIR/pdfbox/PreflightBatch.class"

# ---------------------------------------------------------------------------
log "Setup complete — installed/downloaded sizes"
du -sh "$TOOLS_DIR/verapdf" 2>/dev/null || true
du -sh "$PREFLIGHT_JAR" 2>/dev/null || true
echo "All set. See benchmarks/README.md to run the benchmarks."
