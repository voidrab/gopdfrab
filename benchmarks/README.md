# gopdfrab benchmarks

Speed comparison of gopdfrab against other PDF/A-1b verifiers: [veraPDF](https://verapdf.org/)
(the reference validator) and [Apache PDFBox Preflight](https://pdfbox.apache.org/). 
Correctness is out of scope here — gopdfrab's conformance is established separately 
by the Isartor and veraPDF corpora tests at the repo root (`isartor_test.go`, `veraPDF_test.go`).
This measures speed, memory, and deployment footprint only.

## Why two timing regimes

The vendored corpora (`test documents/Isartor testsuite/PDFA-1b` + `test
documents/veraPDF/PDF_A-1b`, 773 PDFs) are dominated by tiny files — median
~3.6 KB. A cold, one-process-per-file run mostly measures **process/VM
startup**, not the verification engine itself — a JVM takes hundreds of ms to
boot regardless of how fast Preflight's actual check is. So every report
gives both:

- **cold** (`run_cold.sh`, metric 2): one process per file. Realistic for
  one-shot CLI use; includes startup.
- **batch** (`run_batch.sh`, metrics 3-5, 7): one process for the whole
  corpus. Isolates the engine from startup.

## Prerequisites

Run `scripts/setup.sh` first. It installs/downloads everything below into
`tools/` and is idempotent.

| Tool | Why | Notes |
|---|---|---|
| [hyperfine](https://github.com/sharkdp/hyperfine) | CLI timing (metrics 1, 2) | pacman/cargo, or a prebuilt release into `~/.local/bin` |
| GNU `time` (the package) | max RSS + CPU% (metrics 3-5) | pacman, or built from source into `~/.local/bin` |
| [benchstat](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat) | microbenchmark summary (metric 6) | `go install golang.org/x/perf/cmd/benchstat@latest` |
| veraPDF greenfield CLI | reference | installed via its IzPack installer into `tools/verapdf/` |
| PDFBox `preflight-app.jar` | reference | downloaded into `tools/pdfbox/`; batched via checked-in `PreflightBatch.java` |

Already assumed present: Go and a JDK. Make sure `~/.local/bin` is on `PATH`.

## Running

```sh
bash scripts/setup.sh        # one-time (or after upgrading versions)
bash scripts/run_startup.sh  # metric 1
bash scripts/run_cold.sh     # metric 2 (small/median/large sample files)
bash scripts/run_batch.sh    # metrics 3, 4, 5 (+ per-file data for metric 7)
bash scripts/run_micro.sh    # metric 6 (Go microbenchmark) + metric 8 (footprint)
python3 scripts/report.py    # aggregates everything into results/report.md
```

Each `run_*.sh` is independent and writes its own `results/*.json|csv|txt`;
`report.py` notes what's missing if run partially. Re-running a script
overwrites only its own files.

## What's measured

1. **Startup overhead** — cheapest possible invocation (version/usage/module
   load), no PDF processing. Isolates fixed JVM/Go startup cost.
2. **Cold single-file latency** — one process per file, on a small (~2 KB),
   median (~3.6 KB), and large (~4 MB) sample. Includes startup.
3. **Batch throughput** — one process over the full corpus (773 files).
   Wall-clock → files/s and MB/s.
4. **Peak memory** — max RSS during the batch run (GNU `time -v`).
5. **CPU utilization** — user+sys vs. wall during the batch run; shows
   threading (veraPDF/PDFBox parallelize internally; gopdfrab and the JS
   runner are single-threaded here).
6. **In-process Go microbenchmark** — `go test -bench`, gopdfrab only:
   ns/op, B/op, allocs/op via `benchstat`. `BenchmarkOpenVerify` covers the
   verification engine and `BenchmarkConvert` the full PDF/A-1b conversion
   pipeline, both over a set of representative sample files spanning the
   corpus size range and the main cost paths (fonts, colour, rasterization,
   large object-count). `TestConvertLargeAllocationsBounded` additionally
   guards conversion against regaining a verify pass.
7. **Throughput vs. file size** — metric 3-5's per-file timings, bucketed by
   size, to separate fixed per-file cost from cost that scales with content.
8. **Library / deployment footprint** — a static gopdfrab binary (plain and
   stripped) vs. veraPDF's full install dir vs. PDFBox's single jar. 
   veraPDF and PDFBox additionally require a separate JRE; gopdfrab nothing
   beyond the binary.

## Layout

```
cmd/gopdfrab-bench/   gopdfrab CLI runner (single/batch modes, CSV/JSON + self-reported RSS)
micro/                in-process Go microbenchmark (testing.B)
tools/                downloaded/compiled competitors (gitignored; PreflightBatch.java is checked in)
scripts/              setup + one script per metric group + report.py
results/              generated output (gitignored)
```
