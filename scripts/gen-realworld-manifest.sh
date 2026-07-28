#!/usr/bin/env bash
# Generate tests/realworld/manifest.json from the PDFs currently under
# tests/realworld/{should-pass,should-convert}/.
#
# Drop PDFs into those folders, then run this script: it hashes each file and
# merges it into the committed manifest (the shared inventory of the corpus).
# Existing url/license/producer/note fields are preserved, so re-running never
# clobbers provenance you have already filled in; a new file gets a stub with
# "license": "TODO", which you then edit. Entries whose file is not present
# locally are left untouched (they may exist on another machine).
#
# The manifest records hashes, licences, and provenance -- never the PDF bytes,
# which stay gitignored. After running, fill in each new entry's licence (and a
# url if the file is publicly hosted), then commit manifest.json.
#
# For bulk sourcing from public collections, use
# scripts/source-realworld-corpus.py instead: it records url/license/producer
# automatically from each source's API. This script is the manual path, for
# files you drop in by hand.
#
# Requires: jq, sha256sum.
set -euo pipefail

root="tests/realworld"
manifest="$root/manifest.json"
[ -f "$manifest" ] || echo "[]" >"$manifest"

# Hash every corpus file first, in parallel, then merge in a single jq pass.
# Hashing and merging per file would spawn a jq per file over a manifest that
# grows as it goes -- fine for ten files, quadratic for a few thousand.
hashes=$(mktemp)
trap 'rm -f "$hashes"' EXIT
for sub in should-pass should-convert; do
  [ -d "$root/$sub" ] || continue
  find "$root/$sub" -type f -name '*.pdf' -print0 |
    xargs -0 -r -P "$(nproc 2>/dev/null || echo 4)" -n 32 sha256sum
done | sort -k2 >"$hashes"

jq -n --rawfile hashes "$hashes" --slurpfile manifest "$manifest" '
  # sha256sum lines are "<64 hex><2 spaces><path>", path relative to the repo root.
  ($hashes | split("\n") | map(select(length > 66))
           | map({sha256: .[0:64], path: (.[66:] | sub("^tests/realworld/"; ""))})) as $files
  | ($manifest[0] // []) as $entries
  | ($entries | map({key: .path, value: .}) | from_entries) as $byPath
  | ($files | map(
      ($byPath[.path] // {path: .path, url: "", sha256: "", license: "TODO", producer: "", note: ""})
      + {sha256: .sha256}
    )) as $updated
  | ($updated | map({key: .path, value: true}) | from_entries) as $present
  # Entries whose file is absent here may exist on another machine: keep them.
  | ($updated + ($entries | map(select($present[.path] | not)))) | sort_by(.path)
' >"$manifest.new"
mv "$manifest.new" "$manifest"

todo=$(jq '[.[] | select(.license == "TODO")] | length' "$manifest")
echo "manifest updated: $manifest ($(jq length "$manifest") entries, $todo need a licence)"
