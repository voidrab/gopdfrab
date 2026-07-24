#!/usr/bin/env bash
# Populate tests/realworld/ from a manifest of externally-hosted PDFs.
#
# The real-world corpus is not committed (licensing + size); this script
# downloads each file listed in a manifest and verifies its sha256, so a
# populated corpus is reproducible from hashes without redistributing the PDFs.
#
# Usage: scripts/fetch-realworld-corpus.sh [manifest.json]
# Manifest schema: see tests/realworld/manifest.example.json.
#
# Requires: jq, curl, sha256sum.
set -euo pipefail

manifest="${1:-tests/realworld/manifest.json}"
root="tests/realworld"

if [ ! -f "$manifest" ]; then
  echo "manifest not found: $manifest" >&2
  echo "copy tests/realworld/manifest.example.json to tests/realworld/manifest.json and fill it in" >&2
  exit 1
fi

count=$(jq length "$manifest")
for i in $(seq 0 $((count - 1))); do
  path=$(jq -r ".[$i].path" "$manifest")
  url=$(jq -r ".[$i].url" "$manifest")
  want=$(jq -r ".[$i].sha256" "$manifest")
  dest="$root/$path"

  mkdir -p "$(dirname "$dest")"
  if [ ! -f "$dest" ]; then
    echo "fetching $path"
    curl -fsSL "$url" -o "$dest"
  fi

  got=$(sha256sum "$dest" | cut -d' ' -f1)
  if [ "$got" != "$want" ]; then
    echo "sha256 mismatch for $path: got $got, want $want" >&2
    exit 1
  fi
done

echo "corpus ready under $root ($count files)"
