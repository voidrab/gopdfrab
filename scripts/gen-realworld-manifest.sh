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
# Requires: jq, sha256sum.
set -euo pipefail

root="tests/realworld"
manifest="$root/manifest.json"
[ -f "$manifest" ] || echo "[]" >"$manifest"

tmp=$(mktemp)
cp "$manifest" "$tmp"

for sub in should-pass should-convert; do
  find "$root/$sub" -type f -name '*.pdf' 2>/dev/null | sort | while read -r file; do
    rel=${file#"$root/"}
    sum=$(sha256sum "$file" | cut -d' ' -f1)
    jq --arg path "$rel" --arg sum "$sum" '
      if any(.[]; .path == $path)
      then map(if .path == $path then .sha256 = $sum else . end)
      else . + [{path: $path, url: "", sha256: $sum, license: "TODO", producer: "", note: ""}]
      end
    ' "$tmp" >"$tmp.new" && mv "$tmp.new" "$tmp"
  done
done

jq 'sort_by(.path)' "$tmp" >"$manifest"
rm -f "$tmp"

todo=$(jq '[.[] | select(.license == "TODO")] | length' "$manifest")
echo "manifest updated: $manifest ($(jq length "$manifest") entries, $todo need a licence)"
