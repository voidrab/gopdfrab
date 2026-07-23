#!/usr/bin/env bash
# Installs the veraPDF greenfield CLI to benchmarks/tools/verapdf/verapdf, the
# path the differential cross-check (TestDifferentialVeraPDFCorpora,
# TestConvertNoResidualIssues) looks for. Idempotent. Requires java + unzip.
set -euo pipefail

cd "$(dirname "$0")/.."
DEST="benchmarks/tools/verapdf"
URL="https://software.verapdf.org/releases/verapdf-installer.zip"

if [ -x "$DEST/verapdf" ]; then
	echo "already installed: $("$DEST/verapdf" --version 2>&1 | head -1)"
	exit 0
fi

mkdir -p "$DEST"
workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

echo "downloading $URL"
curl -sL -o "$workdir/verapdf-installer.zip" "$URL"
unzip -q "$workdir/verapdf-installer.zip" -d "$workdir"
installer_jar="$(find "$workdir" -name 'verapdf-izpack-installer-*.jar' | head -1)"
if [ -z "$installer_jar" ]; then
	echo "could not find verapdf-izpack-installer-*.jar in the downloaded zip" >&2
	exit 1
fi
echo "INSTALL_PATH=$PWD/$DEST" > "$workdir/install.properties"
java -jar "$installer_jar" -options "$workdir/install.properties"
chmod +x "$DEST/verapdf"
echo "installed: $("$DEST/verapdf" --version 2>&1 | head -1)"
