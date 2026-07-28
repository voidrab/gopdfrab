#!/usr/bin/env bash
# Fetch the publicly-hosted PDFs listed in tests/realworld/manifest.json and
# verify their sha256, so those entries are reproducible from hashes without
# redistributing the bytes. Entries with an empty "url" are local-only (e.g.
# self-generated PDF/A) and are skipped -- those files must already be present.
#
# A URL that has rotated away is a warning, not a failure: one dead link must not
# abandon a fetch of a few thousand files, and the corpus tests only ever check
# the files that are actually present. A hash *mismatch* is still fatal -- that
# means the bytes are not the ones the inventory vouches for.
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

# Read the whole inventory once. Reading it per entry means three jq processes
# per file, each re-parsing the manifest -- unnoticeable for ten entries, minutes
# of pure overhead for a corpus of a few thousand.
fetched=0
verified=0
missing=0
# One field per line rather than TSV: tab is an IFS whitespace character, so
# `read` collapses consecutive tabs and an entry with no url would shift its
# hash into the wrong variable.
while IFS= read -r path && IFS= read -r url && IFS= read -r want; do
  dest="$root/$path"

  if [ -z "$url" ] || [ "$url" = "null" ]; then
    if [ ! -f "$dest" ]; then
      echo "warning: $path has no url and is not present locally; skipping" >&2
      missing=$((missing + 1))
    fi
    continue
  fi

  mkdir -p "$(dirname "$dest")"
  if [ ! -f "$dest" ]; then
    echo "fetching $path"
    curl -fsSL --retry 3 -A 'gopdfrab-corpus/0.1' "$url" -o "$dest" || {
      echo "warning: $path could not be fetched from $url" >&2
      rm -f "$dest"
      missing=$((missing + 1))
      continue
    }
    fetched=$((fetched + 1))
  fi

  got=$(sha256sum "$dest" | cut -d' ' -f1)
  if [ "$got" != "$want" ]; then
    echo "sha256 mismatch for $path: got $got, want $want" >&2
    exit 1
  fi
  verified=$((verified + 1))
done < <(jq -r '.[] | .path, (.url // ""), .sha256' "$manifest")

echo "fetch complete for $manifest: $fetched fetched, $verified verified, $missing unavailable"
