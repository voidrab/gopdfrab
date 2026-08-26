#!/usr/bin/env bash
# Run every Fuzz* target in the module for a fixed duration each. A crash
# writes a reproducer under the owning package's testdata/fuzz and fails the
# run. Usage: scripts/fuzz.sh [duration]  (default 30s per target).
set -euo pipefail

duration="${1:-30s}"
cd "$(dirname "$0")/.."

fail=0
while IFS=$'\t' read -r pkg fn; do
	echo "=== fuzzing $fn ($pkg) for $duration ==="
	if ! go test "$pkg" -run '^$' -fuzz="^${fn}\$" -fuzztime="$duration"; then
		echo "!!! fuzz target $fn failed" >&2
		fail=1
	fi
done < <(
	grep -rhoE '^func (Fuzz[A-Za-z0-9_]+)\(' --include='*_test.go' . \
		| sed -E 's/^func (Fuzz[A-Za-z0-9_]+)\(/\1/' \
		| while read -r fn; do
			pkg=$(grep -rlE "^func ${fn}\(" --include='*_test.go' . | head -1)
			printf '%s\t%s\n' "./$(dirname "${pkg#./}")" "$fn"
		done | sort -u
)

exit "$fail"
