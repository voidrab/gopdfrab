# gopdfrab benchmarks

Speed comparison of gopdfrab against other PDF/A-1b verifiers: [veraPDF](https://verapdf.org/)
(the reference validator), [Apache PDFBox Preflight](https://pdfbox.apache.org/),
and a JS-ecosystem reference point built on [mupdf](https://www.npmjs.com/package/mupdf).
Correctness is out of scope here — gopdfrab's conformance is already
established by the Isartor and veraPDF corpora tests at the repo root
(`isartor_test.go`, `veraPDF_test.go`). This benchmark only measures speed,
memory, and deployment footprint.

## Why two timing regimes

The vendored corpora used for the batch metrics (`test documents/Isartor
testsuite/PDFA-1b` + `test documents/veraPDF/PDF_A-1b`, 773 PDFs combined) are
dominated by tiny files — median ~3.6 KB, only a handful near the ~4 MB max.
A cold, one-file-per-process run therefore mostly measures **process/VM
startup**, not the verification engine: a JVM takes hundreds of ms to boot
regardless of how fast PDFBox's actual Preflight check is. So every report
gives you both:

- **cold** (`run_cold.sh`, metric 2): one process per file. Realistic for
  one-shot CLI use; includes startup.
- **batch** (`run_batch.sh`, metrics 3-5, 7): one process for the whole
  corpus. Isolates the engine from startup.

Read them together, not in isolation — a tool can look slow cold and fast in
batch purely because of its runtime's startup cost, which is a real, separate
result in its own right (metric 1, `run_startup.sh`).

## Prerequisites

Run `scripts/setup.sh` first. It installs/downloads everything below into
`tools/` (gitignored) and is idempotent — safe to re-run.

| Tool | Why | Notes |
|---|---|---|
| [hyperfine](https://github.com/sharkdp/hyperfine) | statistical CLI timing (metrics 1, 2) | via pacman, cargo, or a prebuilt release into `~/.local/bin` if neither is available |
| GNU `time` (the package, not the shell builtin) | max RSS + CPU% (metrics 3-5) | via pacman, or built from source into `~/.local/bin` (patches a 1996-era signal-handler typedef that no longer compiles under GCC 14+) |
| [benchstat](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat) | statistical summary of the Go microbenchmark (metric 6) | `go install golang.org/x/perf/cmd/benchstat@latest` |
| veraPDF greenfield CLI | competitor | downloaded + silently installed via its IzPack installer into `tools/verapdf/` |
| Apache PDFBox `preflight-app.jar` | competitor | downloaded into `tools/pdfbox/`; `PreflightBatch.java` (checked in) is compiled against it for the amortized batch metric |
| `mupdf` npm package | JS reference point | `npm install` in `js/` |

Already assumed present: Go (same version as the main module), a JDK, Node +
npm. If `~/.local/bin` isn't already on `PATH`, add it before running the
other scripts.

## Running

```sh
bash scripts/setup.sh        # one-time (or after upgrading a competitor version)
bash scripts/run_startup.sh  # metric 1
bash scripts/run_cold.sh     # metric 2 (small/median/large sample files)
bash scripts/run_batch.sh    # metrics 3, 4, 5 (+ raw per-file data for metric 7)
bash scripts/run_micro.sh    # metric 6 (Go microbenchmark) + metric 8 (footprint)
python3 scripts/report.py    # aggregates everything into results/report.md
```

Each `run_*.sh` is independent and writes its own `results/*.json|csv|txt`;
`report.py` reads whatever's present and notes what's missing, so a partial
run still produces a partial report. Re-running a script overwrites its own
files only.

## What's measured

1. **Startup overhead** — each tool's cheapest possible invocation
   (version/usage/module-load), no PDF processing. Isolates fixed JVM/Node/Go
   startup cost.
2. **Cold single-file latency** — one process per file, on a small (~2 KB),
   median (~3.6 KB), and large (~4 MB) sample. Includes startup.
3. **Batch throughput** — one process over the full combined corpus (773
   files). Wall-clock → files/s and MB/s.
4. **Peak memory** — max RSS during the batch run, via GNU `time -v`
   (gopdfrab's own runner also self-reports via `getrusage`, so it's
   redundant-but-confirmable for that one tool).
5. **CPU utilization** — user+sys vs. wall during the batch run; shows
   threading (veraPDF and PDFBox both parallelize internally; gopdfrab and
   the JS runner are single-threaded here).
6. **In-process Go microbenchmark** — `go test -bench`, gopdfrab only.
   Noise-free signal for the verification engine itself: ns/op, B/op,
   allocs/op via `benchstat`, on the same three sample files as metric 2.
7. **Throughput vs. file size** — per-file batch timings (metric 3-5's run),
   bucketed by size, to separate fixed per-file cost from cost that scales
   with content.
8. **Library / deployment footprint** — what you actually have to ship: a
   static gopdfrab binary (plain and stripped) vs. veraPDF's full install
   dir vs. PDFBox's single jar vs. the JS package's `node_modules` (mostly
   the WASM blob). veraPDF and PDFBox additionally require a separate JRE;
   the JS runner requires a separate Node runtime; gopdfrab requires nothing
   beyond the binary itself.

## Honest caveats baked into the design

- **The JS runner doesn't validate PDF/A at all.** No mature pure-JS PDF/A-1b
  validator exists; `mupdf`'s API (checked directly against its `.d.ts`) only
  exposes generic document load/parse, nothing PDF/A-aware. `js/runner.mjs`
  times `Document.openDocument()` + `countPages()` and tags every output row
  and summary `"valid":"load_only"` / `caveat` so its (much faster) numbers
  are never mistaken for a competing validator's verdict — it's a loose
  JS-runtime baseline, not a fourth PDF/A checker.
- **Verdict counts (pass/fail/valid/invalid) are a sanity check, not a
  correctness score.** The tools use different conformance interpretations
  (see the main README's veraPDF-vs-spec-literal discussion); this benchmark
  doesn't adjudicate between them, it only flags whether a tool is erroring
  on most files (which would make its timing meaningless).
- **gopdfrab is not uniformly fastest.** The microbenchmark (metric 6) found
  it allocates ~2 GB validating the corpus's one ~4 MB file, and the cold
  large-file latency (metric 2) shows it slower there than PDFBox. It wins
  decisively on small/median files and on batch throughput/memory across the
  whole corpus. Both are real findings — see `results/report.md` rather than
  any single number.

## Layout

```
cmd/gopdfrab-bench/   gopdfrab CLI runner (single/batch modes, CSV/JSON + self-reported RSS)
micro/                in-process Go microbenchmark (testing.B)
js/                   Node runner using mupdf (load/parse only — see caveats above)
tools/                downloaded/compiled competitors (gitignored; PreflightBatch.java is checked in)
scripts/              setup + one script per metric group + report.py
results/              generated output (gitignored)
```
