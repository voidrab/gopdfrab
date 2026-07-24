#!/usr/bin/env bash
# Fetch the publicly-hosted PDFs listed in tests/realworld/manifest.json and
# verify their sha256, so those entries are reproducible from hashes without
# redistributing the bytes. Entries with an empty "url" are local-only (e.g.
# self-generated PDF/A) and are skipped -- those files must already be present.
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
  exit 1
fi

count=$(jq length "$manifest")
for i in $(seq 0 $((count - 1))); do
  path=$(jq -r ".[$i].path" "$manifest")
  url=$(jq -r ".[$i].url" "$manifest")
  want=$(jq -r ".[$i].sha256" "$manifest")
  dest="$root/$path"

  if [ -z "$url" ] || [ "$url" = "null" ]; then
    if [ ! -f "$dest" ]; then
      echo "warning: $path has no url and is not present locally; skipping" >&2
    fi
    continue
  fi

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

echo "fetch complete for $manifest"
