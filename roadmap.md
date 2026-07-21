# Roadmap to 1.0

Goal: the best PDF/A-1b verifier and converter available in Go, good enough that
the API can be frozen. PDF/A-2/3/4 come after 1.0, not before.

Every claim below was checked against the code or reproduced with a test. Where
something was reproduced, the transcript is quoted.

## Where things stand

Genuinely solid:

- 158 checks across 10 groups. Isartor (204 fail files) and veraPDF (263 pass +
  306 fail) both fully green — so false positives *are* tested, on synthetic
  files.
- Convert pipeline: pre-emptive fixups, verify/fix loop (max 4 iterations),
  raster last resort. Corpus floor 510/510.
- Arlington object-model checks, verify and convert, off a generated model.
- Fuzzing at three levels, including semantic oracles (determinism, honesty,
  convergence) — better than most PDF libraries in any language.
- Resource hardening is thorough: ~15 depth/size caps across the parser, a
  256 MB inflate ceiling, CCITT column and byte caps, page-tree and resolve
  depth limits.
- Coverage: root 100%, arlington 100%, pdf 96.7%, writer 95.4%, verify 93.5%,
  convert 92.6%.
- 15–160x faster than veraPDF and PDFBox Preflight depending on metric.

The gaps below are what stands between that and a release.

---

## P0 — the verdict can't be trusted yet

### 1. An undecodable content stream turns a violation into a pass

Reproduced. Two PDFs, byte-identical page content (`1 0 0 rg` fill, no
OutputIntent — a 6.2.3.3 violation), differing only in how the content stream is
encoded:

```
plain.pdf   valid=false issues=1
     PDF/A violation (6.2.3.3/2), page 1, ref &{4 0}:
     "device colour (rgb) used in content stream without matching OutputIntent"
rle.pdf     valid=true  issues=0
```

`rle.pdf` uses `/RunLengthDecode`. `DecodeStream` doesn't support it, the decode
returns an error, and `collectUsageFromBytes` (`internal/verify/verifier.go:995`)
does `if err != nil { return }`. The violation silently disappears and the file
is reported clean.

This is the most serious defect in the project. It is not a missing feature; it
is the verifier answering "yes" when it means "I couldn't look."

Three parts, all required:

**1a. Never swallow a decode error.** Audit every `if err != nil { return }` in
`internal/verify` — roughly a dozen in `checks_font_program.go` alone, plus the
content walk. Add a `Structure.StreamUndecodable` check so a stream that can't be
read is a reported issue, not an absence of issues.

**1b. Support the remaining filters.** `internal/pdf/content.go` handles Flate,
ASCIIHex, ASCII85 and nothing else. Missing: **LZWDecode** (the decoder exists in
`internal/pdf/lzw.go` but is only reachable from convert and the rasterizer),
**RunLengthDecode** (no decoder at all), and **predictors** on content streams
(`decodeStreamPredicted` is unexported and used only for xref and object
streams, so a content stream with `/Predictor 12` decodes to garbage).

**1c. Collapse the duplicate decode chains.** There are four:
`DecodeStream` (`content.go`), `decodeStreamPredicted` (`predictor.go`),
`lzwStreamPlaintext` (`convert/fixups_stream.go`), and a partial one in
`raster_image.go`. They have already diverged — the convert copy handles LZW and
predictors, the verify path doesn't. That divergence *is* bug 1b. One chain in
`internal/pdf`, with image-only filters (DCT, JPX, JBIG2, CCITT) returning a
typed "encoded image data" result so callers can tell "not decodable to bytes"
from "broken."

### 2. A single bad xref offset suppresses unrelated checks

Reproduced. Same file, one xref entry pointed at a bogus offset:

```
plain.pdf   valid=false issues=3
     6.1.3/1  trailer does not contain the required ID keyword
     6.2.3.3/2 device colour (rgb) ... without matching OutputIntent
     6.7.2/1  document catalog lacks a Metadata entry
badoff.pdf  valid=false issues=2
     6.1.3/1  trailer does not contain the required ID keyword
     6.1.6    unexpected token 1 in object 4
```

The colour violation vanishes, as expected. But so does the **6.7.2 catalog
Metadata** violation, which has nothing to do with the damaged object. One bad
offset takes down document-level checks elsewhere in the graph. The file is still
reported invalid, so this is not as dangerous as item 1 — but the issue list is
wrong, and a user fixing reported issues one at a time will never converge.

Fix alongside item 3: the graph walk must degrade per-object, not abandon whole
check families.

### 3. Convert can return no output and no error

Same run:

```
badoff.pdf  convert: outlen=0 valid=false err=<nil>
```

`convert.Run` (`internal/convert/convert.go:97`) falls back to verify-only when
`ResolveGraph` fails and returns `ConvertResult{Result: res}, nil` — success,
with nothing in it. The caller has to know to check `len(cr.Output)`;
`cr.Save(path)` then fails with a different error much later. A function that
promises a converted document must return one or return an error saying why not.

---

## P1 — resilience and unusual files

### 4. No xref recovery

Reproduced: corrupting `startxref` gives

```
verify: failed to parse structure: could not parse startxref offset
```

with zero issues and no result. Truncating the file gives `startxref not found`.
Both are hard errors — no verification, no conversion, nothing.

Files with damaged cross-reference data are exactly the files people reach for a
PDF/A converter to fix. veraPDF and PDFBox both scan for `N G obj` and rebuild.

- Full-file object scan when `startxref` is missing, unparseable, or doesn't
  point at an xref.
- Rebuild from the scan, last definition of each object number wins, recover the
  trailer by finding `/Type /Catalog`.
- Report the recovery as an issue — the file is not conformant — but keep going.
- Same fallback per-object when the table parses but an offset is wrong
  (item 2).

Seed `internal/pdfgen` with these shapes. Oracle: a file with a deliberately
broken xref must verify to the same issue set as the intact original, plus the
recovery issue. That oracle would have caught item 2.

### 5. Encrypted input can't be converted

No decryption anywhere. `Encrypt` in the trailer is correctly flagged (6.1.3) and
that's the end of it — the streams stay encrypted, so convert produces nothing.

A large share of real-world PDFs are encrypted with an empty user password purely
to set permission flags. Those are trivially decryptable and are a completely
reasonable conversion input. Implement the standard security handler (RC4 40/128,
AES-128 for R4, AES-256 for R6) for the empty-password case, plus an optional
password on the open path, and report clearly when a real password is needed.

### 6. The rasterizer silently drops content

`internal/convert/raster.go` handles a decent operator set but ignores:

- `sh` and shading patterns — gradient areas render blank.
- `BI`/`ID`/`EI` inline images.
- Type 3 fonts.
- `Tr` (text render mode) and `Ts` (rise) — invisible OCR text renders visible.

Raster is the fallback that *guarantees* conformance, so anything it drops is
data loss the user is never told about. Fill the gaps, and make the rasterizer
report what it couldn't render so `ConvertResult` carries a fidelity warning. A
loud residual beats a quiet blank page.

### 7. Limits are hardcoded and fail silently

The caps are good (see "already fine" below) but two things are wrong with how
they behave. `InflateZlib` uses `io.LimitReader(zr, maxInflateOutput)`, which
**truncates without error** — a legitimate stream over 256 MB silently decodes to
a prefix, and every check downstream then runs against partial data. Same class
of bug as item 1. And none of the caps are settable, so a caller who knows their
inputs can't raise or lower them.

Make hitting a limit an error or a reported issue, and expose the caps through
the options in item 10.

### 8. Convert holds everything in memory

`ConvertAll` (`internal/convert/convert.go:66`) keeps a full `ConvertResult` per
input, each with the complete output PDF as `[]byte`. 500 files means 500 output
documents resident at once.

Separately, `Run` resolves the entire object graph into Go structures. The read
path went to real trouble to mmap rather than heap-load the file; conversion
undoes that. Worth measuring peak RSS for a large convert before deciding how far
to take it, but at minimum `ConvertAll` needs a streaming/callback form and a
worker-count knob.

### 9. Windows and macOS are untested

CI is ubuntu-only. `mmap_other.go` returns nil on non-unix, so Windows takes an
entirely different, never-exercised seek-based read path — and loses the
large-file guarantee. Add a CI matrix, and either implement Windows file mapping
or document the limitation honestly.

---

## P2 — proving it works on files nobody wrote a test for

This section is the one the first draft of this roadmap missed, and it may matter
more than anything except P0. Everything currently green is green against
*synthetic conformance-suite files*. Both suites are hand-built to exercise one
clause each. Real PDFs are not like that.

### 10. There is no real-world corpus

Isartor and veraPDF are 777 files averaging 3.6 KB, each deliberately
constructed. Nothing in the test suite is a 200-page scanned report from a
real scanner, a LaTeX paper, an InDesign export, a Word document, or a
Ghostscript-converted invoice.

Two corpora needed, and they answer different questions:

- **Should-pass**: real PDF/A output from Acrobat, Ghostscript, LibreOffice,
  Word, and the common PDF/A converters. Any file these tools claim is PDF/A and
  veraPDF agrees is PDF/A must verify clean. A verifier that flags real Acrobat
  output is unusable regardless of what the test suites say.
- **Should-convert**: ordinary non-PDF/A documents across producers, page counts,
  and font technologies. The metric is what fraction convert to conformant output
  *without* falling back to raster.

Licensing makes this awkward — most real PDFs can't be redistributed. Options:
generate a corpus from permissively-licensed sources (arXiv, government
publications, Wikimedia), or keep the corpus in a separate repository referenced
by hash. Pick one and commit to it; the current situation is that no real
document is tested at all.

### 11. The differential harness exists but never runs

`convert_regression_test.go` already cross-checks gopdfrab against the bundled
veraPDF binary — genuinely the right idea. It is dark in three ways at once:

- it skips under `-short`, which is what CI runs;
- it skips when the veraPDF binary is absent, which it is in CI;
- its corpus is `tests/regression/`, which is **gitignored**, so the 16 documents
  that caught real bugs exist only on one machine.

So the single highest-value correctness tool in the repo has never run in CI and
its inputs aren't shared. Fix all three, then expand it: run both verifiers over
the real-world corpus from item 10 and diff at clause level. Every disagreement is
either a gopdfrab bug or a documented, justified deviation. That list *is* the
conformance argument for 1.0, and it's far stronger than "both suites pass."

Note the benchmarks README explicitly puts correctness out of scope and defers to
the corpora. That was fine while the corpora were the whole story. It isn't now.

### 12. Nothing checks that converted output still looks like the input

There is no fidelity test anywhere — no rendering of input versus output, no
pixel comparison, nothing. The only guard is "does it verify clean."

That is a dangerous metric to optimise, because blanking a page always improves
it. The known failure mode is already on record: conformant output that has been
destructively rasterized or blanked, with page `/Group` deleted rather than
rendered. Convert is currently free to destroy a document and score it as a win.

Build a fidelity gate: render each page of input and output, compare
perceptually, fail when a page changes beyond a threshold or goes blank. Report
per-page fidelity in `ConvertResult` alongside residual issues. This pairs with
item 6 — the rasterizer needs to know and say what it dropped.

### 13. CI runs a fraction of the test suite

`.github/workflows/go.yml` runs `go test -short` on ubuntu, and that's all.
Consequences:

- **No `-race`.** `TestGeneratedCorpusRace` and `TestConcurrentDecodeIsSafe` exist
  specifically to be run under `-race` and never are. The Reader has caches with
  one mutex covering one path; this is exactly where a race would live.
- **No fuzzing.** Every target seeds its corpus in code so seeds replay, but no
  new inputs are ever explored. A nightly `-fuzz` job per target, or OSS-Fuzz,
  turns a large existing investment into something that keeps paying.
- **`-short` skips** the regression cross-check and the time-bound scan.
- **One OS** (item 9).

### 14. Thread-safety is undocumented

Nothing in `gopdfrab.go` or `document.go` says whether a `*Document` may be used
from multiple goroutines. `Reader` carries several caches and one mutex that
guards only `DecodeStreamCachedConcurrent`, which strongly suggests the answer is
no — but callers can't know that, and `VerifyAll`/`ConvertAll` being concurrent
implies otherwise. Decide the contract, document it at the type, and add a `-race`
test that enforces it.

---

## P3 — API, before it gets frozen

The disclaimer says the API will change heavily before release. This is the list.
Do it in one pass.

### 15. Options

Every entry point takes `(path, profile)` and nothing else. No way to set raster
DPI, cap iterations (hardcoded `maxConvertIterations = 4`), adjust the resource
limits from item 7, or supply a password.

```go
gopdfrab.Convert(path, gopdfrab.PDFA_1B,
    gopdfrab.WithRasterDPI(200),
    gopdfrab.WithMaxIterations(8),
    gopdfrab.WithPassword(pw))
```

Keep the two-argument form working — it's the common case.

### 16. No `context.Context` anywhere

Nothing is cancellable. Add `VerifyContext`/`ConvertContext` and check
cancellation at loop boundaries: per fixer pass, per page walk, per file in a
batch. Anyone putting this behind an HTTP handler needs it.

### 17. Results don't serialize

Every `PDFError` field is unexported, so `json.Marshal(result)` yields
`[{},{},{}]`. Any CLI, service, or CI integration needs JSON. Add `MarshalJSON`
on `PDFError`, `Check`, and `Result` with a documented, stable shape.

### 18. Streaming output

`ConvertResult.Output []byte` plus `Save(path)`. Add `WriteTo(io.Writer)` and
consider making `Output` lazy — see item 8.

### 19. Typed errors

Everything is `fmt.Errorf`/`errors.New` strings, so callers can't tell "not a
PDF" from "encrypted" from "truncated" from "I/O error" without matching on text.
Define `ErrNotPDF`, `ErrEncrypted`, `ErrPasswordRequired`, `ErrDamaged`, wrapped
for `errors.Is`. Item 3 depends on this.

### 20. A real CLI

`cmd/` is empty. `main/main.go` is an example that imports `internal/pdf`, which
external users cannot do. Ship `cmd/gopdfrab` with verify/convert subcommands,
`--json`, meaningful exit codes (0 valid, 1 invalid, 2 error), and recursive
input. It's how most people will first try the library and what makes the
benchmark numbers reproducible by anyone else.

### 21. Naming consistency

`PDFA_1B` / `A_1B` / `Legacy_1B` / `PDF` / `ObjectModel` mix conventions across
level constants and profile variables. `Document.GetPageCount`/`GetVersion`/
`GetMetadata` carry `Get` prefixes nothing else uses. Fix both while breaking
changes are still free.

---

## P4 — performance

Numbers are strong; the work is keeping them.

### 22. Commit performance history

`benchmarks/results/` is gitignored, so every round is local-only and regressions
across releases are invisible. Commit a per-release benchstat summary.

### 23. Extend the allocation guards

Wall-clock on a dev machine is ±15% noisy, so the `allocs/op` assertions in
`benchmarks/micro/bench_test.go` are the only stable gate, and they cover a
fraction of the samples. Extend to `Convert/fonts` and the other cost paths.

### 24. Benchmark the recovery path

Once item 4 lands, a full-file object scan is a new worst case. Benchmark it so
recovery on a large damaged file doesn't become a denial-of-service vector.

---

## P5 — release engineering

### 25. Stability policy

No `CHANGELOG.md`, no stated semver policy, no deprecation process. For a library
whose headline promise is a frozen API, say explicitly what 1.0 covers: the root
package only, `internal/*` exempt, what a breaking change means, how long
deprecated symbols live.

### 26. Security policy

No `SECURITY.md`. This library parses untrusted, hostile input by design — that
is its whole job. It needs a disclosure address and a stated response
expectation before 1.0, not after the first report arrives.

### 27. Documentation

One `Example()` in the entire repo, no `doc.go`, no package-level overview on
pkg.go.dev beyond a one-line comment. Add runnable examples for each main entry
point (they double as tests) and a package overview explaining the verify/convert
model and the profile system.

The README also needs a pass: its Roadmap section still says PDF/A-1b is "at an
early stage" and that testing infrastructure "must be created," which undersells
where things actually are.

### 28. Repo hygiene

`tests/regression/` is gitignored (item 11). Three stale multi-megabyte `.test`
binaries and two coverage files sit in the working tree — ignored, but they
suggest the working directory is doing double duty as a scratch space.
`GOOS=js GOARCH=wasm go build ./wasm/` fails without `-o` because the output
name collides with the directory; fix that and put the wasm build in CI so it
can't rot. `TODO.md` is gitignored, so items 20–22 of the old list live nowhere
shared — fold what survives into this file.

### 29. Carry-over from TODO.md

- Document the `pdf.StreamKey` invariant at the type: it keys caches by
  `uintptr(&RawStream[0])`, valid only while the graph pins the slices, and
  `AdoptStreamCaches` extends that across two Readers.
- Document the null ≡ Go `nil` invariant. A present-but-null dict entry is
  indistinguishable from an absent one — correct PDF semantics, very easy to
  break with `if _, ok := Entries[k]; ok`.
- Deduplicate `parseClassicReference` against `parseObject`. It's a copy that has
  already silently diverged on scalars and null once; the only legitimate
  difference is the `N G R` lookahead.
- Instrument the conservative skips in `CFFAdvanceWidths`/`CFFCIDAdvanceWidths`
  and the Type1 `FontFile` width path. They bail on FontMatrix, unusual
  charstring prefixes, and Differences, and the bails are invisible — there's no
  way to know how much 6.3.6 coverage is silently skipped across the corpus.
- Coverage to ~95%: verify 93.5%, convert 92.6%. CFF/Type1 fixtures are the bulk.

---

## Checked and already fine

Recorded so nobody re-investigates:

- **Decompression bombs**: capped at 256 MB, plus CCITT column/byte caps and
  ~15 depth and size limits across the parser. Only the silent-truncation
  behaviour is a problem (item 7).
- **Profile immutability**: `AddCheck`/`RemoveCheck`/`Clear` all clone. The
  `PDFA_1B.RemoveCheck(...)` pattern in the README cannot mutate the global.
- **Corpus is committed**: 777 files tracked. Conformance genuinely does run in
  CI. It's `tests/regression/` that's missing (item 11).
- **False positives are tested**: 263 pass files in the veraPDF corpus. The gap
  is real-world producers (item 10), not pass-file coverage per se.

---

## Order of work

1. **Items 1–3.** Nothing else matters if the verdict is wrong. Item 1 is the
   whole release blocker in one bug.
2. **Items 11 and 13.** Turn the dark differential harness on and put `-race`
   plus fuzzing in CI. Do this early — it's cheap and it changes what every
   later change is measured against.
3. **Item 4.** Biggest real-world gap, and the oracle it needs would have caught
   item 2.
4. **Items 15–21.** The API break, in one pass, while the disclaimer covers it.
5. **Items 10 and 12.** Real corpus and fidelity gate. Slower, and they need
   items 2 and 4 done first to be meaningful.
6. **Items 5–9.** Encryption and rasterizer fidelity: large but well-bounded.
7. **Items 22–29.** Continuous.

## Not in 1.0

- PDF/A-2, -3, -4. Adding parts before -1b is airtight spreads item 1's class of
  bug across four conformance levels.
- PDF/A-1a (accessibility, tagged PDF). Different problem, much larger.
- Digital signature validation.
- Rendering as a general-purpose feature. The rasterizer stays a conversion
  fallback — though items 6 and 12 will improve it considerably as a side effect.
