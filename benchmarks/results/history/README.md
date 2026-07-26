# Performance history

Committed rounds of the in-process microbenchmarks (`benchmarks/micro`), so
performance regressions across releases are visible rather than living on one
developer's machine. Everything else under `benchmarks/results/` is gitignored
scratch; this directory is not.

## Recording a round

```sh
benchmarks/scripts/record-history.sh [count]   # default count=10
```

It writes `YYYY-MM-DD-<short-sha>.txt` — the raw `go test -bench` output, which
is what `benchstat` reads — and prints a comparison against the previous round.
Record one per release, and after any change meant to move performance.

The commit must be clean: a dirty tree gets a `-dirty` suffix, which means the
numbers cannot be tied to anything and the file should not be committed.

## Reading the numbers

**Trust `allocs/op`.** It is deterministic (the rounds below run at ± 0%), so a
change there is real and attributable. The allocation ceilings in
`benchmarks/micro/bench_test.go` (`TestCostPathAllocationsBounded`) are the
enforced gate; this history is the record of how the numbers got there.

**Distrust wall-clock.** `sec/op` on a developer machine is thermally noisy —
roughly ±15% run to run on the larger samples — so a time difference under that
is unproven, not an improvement. Compare across rounds only when `benchstat`
reports a small p-value *and* the direction is corroborated by `B/op` or
`allocs/op`.

Rounds are not comparable across machines: `goos`, `goarch` and `cpu` are
recorded in each file's header for exactly that reason.
