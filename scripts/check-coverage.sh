#!/usr/bin/env bash
# Fail unless every statement in a Go coverage profile ran at least once.
# The percentage `go tool cover` prints is rounded, so 100.0% can hide a few
# uncovered statements; this checks the blocks themselves and lists any that
# never ran. A profile built with -coverpkg holds each block once per test
# package, so a block counts as covered if any of its lines has a hit.
# Usage: scripts/check-coverage.sh [coverage.txt]
set -euo pipefail

profile="${1:-coverage.txt}"

awk '
	NR == 1 { next } # mode: line
	{
		stmts[$1] = $2
		if ($3 > 0) hit[$1] = 1
	}
	END {
		missed = 0
		for (b in stmts) {
			if (stmts[b] > 0 && !(b in hit)) {
				print "uncovered: " b
				missed += stmts[b]
			}
		}
		if (missed > 0) {
			printf "%d statement(s) not covered; coverage must stay at 100%%\n", missed
			exit 1
		}
		print "coverage: every statement covered"
	}
' "$profile"
