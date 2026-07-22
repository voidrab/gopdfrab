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
  raster last resort. Corpus floor 510/510 — the former encrypted hold-out now
  decrypts (item 5).
- One stream-decode chain in `internal/pdf`, covering every filter PDF/A-1
  permits, with a typed result separating "encoded image data" from "broken".
- Arlington object-model checks, verify and convert, off a generated model.
- Fuzzing at three levels, including semantic oracles (determinism, honesty,
  convergence) — better than most PDF libraries in any language.
- Resource hardening is thorough: ~15 depth/size caps across the parser, a
  256 MB inflate ceiling, LZW and RunLength output caps, CCITT column and byte
  caps, page-tree and resolve depth limits.
- Coverage: root 100%, arlington 100%, pdf 96.8%, writer 95.4%, verify 93.5%,
  convert 92.6%.
- 15–160x faster than veraPDF and PDFBox Preflight depending on metric.

The gaps below are what stands between that and a release.

---

## P0 — the verdict can't be trusted yet

### 1. An undecodable content stream turns a violation into a pass — **DONE**

Was: two PDFs with byte-identical page content (`1 0 0 rg` fill, no
OutputIntent — a 6.2.3.3 violation) differing only in stream encoding, where the
`/RunLengthDecode` one verified clean because `DecodeStream` failed and
`collectUsageFromBytes` did `if err != nil { return }`. Not a missing feature —
the verifier answering "yes" when it meant "I couldn't look."

Fixed across three commits, in the order 1c → 1b → 1a. That order is forced, not
stylistic: before 1c, `DecodeStream` errored on `DCTDecode`, so 1a's "report
every decode error" would have fired on every JPEG in the corpus. The typed
"encoded image data" result is what makes 1a safe to ship.
`TestEncodedContentStreamStillReportsViolations` now builds both documents and
asserts their issue sets are equal.

**1c. One decode chain.** New `internal/pdf/filters.go` holds the filter
registry (both spellings, image and predictor flags) and the typed
`DecodedStream`. `DecodeStream` keeps its signature and ~52 call sites; a chain
ending in an image codec returns `ErrEncodedImage` rather than "unsupported
filter". DecodeParms are resolved positionally, replacing `StreamDecodeParms`'
last-entry heuristic, and predictors are applied in-chain per filter.

**1b. The remaining filters.** RunLengthDecode (new), LZW reachable from stream
dicts with `/EarlyChange` finally honoured, predictors on content streams. Both
new decoders have output caps and fuzz clean.

**1a. Never swallow a decode error.** `Structure.StreamUndecodable` (6.1.7/8) is
reported from the two decode chokepoints, deduped by `StreamKey`.

Five findings from the work, none of which the plan anticipated:

- **There were six chains, not four.** `ccittEncodedBytes` and
  `undoInlineImagePredictor` were also copies. The former already *was* the
  typed image result, hand-rolled for one caller.
- **The bug had a second half.** `collectUsageFromBytes` is the sole producer of
  four `ValidationContext` fields, and each drives a check *suppression*. A
  swallowed decode error made those sets a subset of the truth, so an
  undecodable content stream suppressed 6.2.3.3 and 6.3.4 findings on objects
  unrelated to it. Usage collection now reports completeness and all four sets
  are discarded when incomplete. `SkipUnusedSimpleFonts` inverts — leaving it on
  with nil usage suppresses *every* 6.3.4 check — so it is explicitly ANDed.
- **Two latent bugs fell out.** `/Filter [/A85 /LZW]` fed ASCII85 text straight
  to the LZW decoder (a `hasLZW` flag short-circuited the chain), and
  `/Filter [/A85 /DCTDecode]` fed raw bytes to `jpeg.Decode`.
- **`ErrNotAStream` is exempt from the new check**, alongside `ErrEncodedImage`.
  A dict carrying no stream is not a stream object, so 6.1.7 does not apply; the
  callers that hit this are defensive guards against a mistyped entry, which the
  object-model checks report against the schema.
- **Predictors are scoped to Flate and LZW** per ISO 32000-1 Table 8. Verified
  against all three corpora: every predictor in every file sits on FlateDecode.

#### Two gaps this work exposed — both closed

- `fontProgramValid`'s FontFile3 arm accepted any program whose first byte was
  1, so a CFF with a valid header but an unparseable Top DICT passed 6.3.2 while
  `ValidateCIDCFFSubset` silently skipped it. Now runs `ParseCFFTopDict`, so the
  check and its dependents agree on what "valid" means. Measured rather than
  assumed, since it is stricter: the corpora hold 74 bare-CFF FontFile3
  programs and all 74 still pass.
- `ComputeContentUsage` runs before `verifyDocument`'s walk, so a content-stream
  decode failure was reported as document-level. The usage walk now maintains
  `CurrentPage` over each page subtree and restores it afterwards.

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

### 5. Encrypted input converts to a corrupt document — **DONE**

Was: no decryption anywhere. `/Encrypt` was correctly flagged (6.1.3), convert
**stripped that entry** to clear the violation, and the RC4-encrypted bytes
survived into the output unchanged — a document convert knew was broken. The one
encrypted fixture, `isartor-6-1-3-t02-fail-a.pdf`, was the sole hold-out keeping
the convert floor at 509.

Implemented the Standard security handler in `internal/pdf/crypt.go`: RC4 40/128
(V1/R2, V2/R3), AES-128 for R4 (AESV2 + Identity crypt filters), AES-256 for R6
(ISO 32000-2 Algorithm 2.A/2.B), covering user, owner and empty passwords.
Empty-password files decrypt automatically through `Open`/`OpenBytes` with no API
change; `OpenWithPassword`/`OpenBytesWithPassword` take an explicit password. The
handler is built once in `initializeStructure` (Encrypt dict and trailer `/ID`
read before it goes live), and applied per object at the single
`parseClassicReference` choke point — stream bytes into a fresh slice (never
mutating the mmap alias), strings via a recursive walk — with cross-reference
streams, the Encrypt object, and object-stream contents exempt by construction.
New sentinel errors `ErrEncrypted`/`ErrPasswordRequired`, re-exported from root.

Findings from the work:

- **The orphaned Encrypt object had to be dropped, not just its trailer entry.**
  The writer already omitted `/Encrypt` from the rebuilt trailer, but the in-heap
  verify still saw the resolved Encrypt dictionary, and the object-model checks
  reject an AESV3 dict (V5/R6) under PDF/A-1b's model. A pre-emptive fixup now
  deletes the trailer `/Encrypt` reference (the graph is already plaintext), which
  orphans the dict so verify and the writer agree.
- **Hex vs. literal string bytes.** `PDFHexString.Value` holds hex *text*, not
  decoded bytes, so `/O`, `/U`, `/ID` and every encrypted hex string had to be
  hex-decoded before use; the decrypted plaintext is raw bytes, so both spellings
  collapse to a decoded literal string.

Validated against real qpdf output for every revision (RC4-40/128, AESV2, AESV3,
plus cleartext-metadata, object-stream, and user/owner-password variants) and the
committed isartor fixture. Convert refuses a genuinely password-required file with
`ErrPasswordRequired` (surfaces at `pdf.Open`) rather than emitting broken output.
`minConvertedFully` raised to 510.

The optional `WithPassword` functional option on `Verify`/`Convert` is deferred to
item 15, which will call the same internal path.

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

The caps are good (see "already fine" below). The silent-truncation half is now
fixed; the settability half remains.

**Silent truncation — DONE.** `InflateZlib` used `io.LimitReader(zr,
maxInflateOutput)`, which **truncated without error** — a stream over 256 MB
silently decoded to a prefix every downstream check then ran against as if it
were whole (same class as item 1). It now reads one byte past the cap and returns
`ErrOutputTooLarge` when exceeded, matching `maxLZWOutput`/`maxRunLengthOutput`;
because that error flows through the decode chokepoint it surfaces as a reported
`StreamUndecodable` rather than vanishing. The size cap is kept distinct from
`InflateZlib`'s deliberate leniency toward truncated/CRC-broken streams (which
still return their inflated prefix) — `TestInflateZlibSizeCap` and
`TestInflateZlibTruncatedKeepsPrefix` pin both. That was the last
silent-truncation site.

**Settability — still open.** None of the caps are settable from outside the
package (only the test-only `SetMaxInflateOutput`), so a caller who knows their
inputs can't raise or lower them. Expose the caps through the options in item 15.

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
gopdfrab.Convert(path, gopdfrab.PDFA1B,
    gopdfrab.WithRasterDPI(200),
    gopdfrab.WithMaxIterations(8),
    gopdfrab.WithPassword(pw))
```

Keep the two-argument form working — it's the common case.

### 16. No `context.Context` anywhere

Nothing is cancellable. Add `VerifyContext`/`ConvertContext` and check
cancellation at loop boundaries: per fixer pass, per page walk, per file in a
batch. Anyone putting this behind an HTTP handler needs it.

### 17. Results don't serialize — **DONE**

Every `PDFError` field is unexported, so `json.Marshal(result)` yielded
`[{},{},{}]`. `internal/pdf/json.go` now defines `MarshalJSON` on `Check`,
`PDFError` and `Result` with a documented, stable, output-only shape (the root
aliases inherit it): `Check` → `{name, clause, subclause, description}` (internal
id omitted); `PDFError` → `{check, page, documentLevel, object?, objModel?,
messages, text}`; `Result` → `{type, valid, issueCount, issues}` with `issues`
always an array. Shape is pinned by tests in both `internal/pdf` and the root
package. No `UnmarshalJSON`: a `Check`'s identity is its registry entry, not free
text.

### 18. Streaming output — partly done

`ConvertResult` now has `WriteTo(io.Writer) (int64, error)` (implements
`io.WriterTo`; `Save` unchanged), so callers can stream the rewrite to any sink
without copying `Output` again. Both error when there is no output.

Still open: making `Output` itself lazy so the whole PDF need not stay resident —
that is the memory-footprint work in item 8, not a standalone API change.

### 19. Typed errors — **DONE**

Callers can now tell the open-failure categories apart with `errors.Is` instead
of matching message text. `ErrNotPDF`, `ErrDamaged`, `ErrEncrypted` and
`ErrPasswordRequired` are defined in `internal/pdf/filters.go` and re-exported
from the root package. The open path wraps them: `newDocument` and
`initializeStructure` return `ErrNotPDF` when there is no `%PDF-` header (a
tolerated garbage prefix is not misclassified) and `ErrDamaged` for every
unparseable startxref/xref/trailer failure; decryption returns
`ErrEncrypted`/`ErrPasswordRequired`. All wrap the specific cause, so the message
is unchanged and the chain survives the `failed to parse structure: %w` wrapper.
`TestOpenBytesErrorClassification` pins the mapping.

The decode chain's `ErrEncodedImage`, `ErrUnsupportedFilter`,
`ErrUnsupportedPredictor`, `ErrNotAStream` and `ErrOutputTooLarge` remain as
before. A distinct `ErrIO` was considered and skipped: a genuine mid-read I/O
error is rare and already surfaces wrapped; splitting it from `ErrNotPDF` would
add a category with almost no reachable call site.

### 20. A real CLI

`cmd/` is empty. `main/main.go` is an example that imports `internal/pdf`, which
external users cannot do. Ship `cmd/gopdfrab` with verify/convert subcommands,
`--json`, meaningful exit codes (0 valid, 1 invalid, 2 error), and recursive
input. It's how most people will first try the library and what makes the
benchmark numbers reproducible by anyone else.

### 21. Naming consistency — **DONE**

The level constants and profile variables mixed snake_case with MixedCaps.
Dropped the underscores per Go convention (staticcheck ST1003): the `LevelType`
constant `A_1B` is now `A1B` (`Undefined`/`ObjectModel` unchanged), and the
profile variables `PDFA1B`/`Legacy1B` are now `PDFA1B`/`Legacy1B` (`PDF`
unchanged). Initialisms stay all-caps (`PDFA1B`, not `PdfA1b`). The profile's
`PDF` prefix over the level's bare name is intentional — they are different types
in different roles (`Verify(path, PDFA1B)` vs `res.Type == A1B`).

Per Effective Go's getter rule, the `Get` prefix is gone from the `Document` and
`Reader` accessors: `GetPageCount`/`GetVersion`/`GetMetadata` are now
`PageCount`/`Version`/`Metadata`. A hard rename with no deprecated aliases, since
the API is still pre-1.0. README updated to match.

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

### 25. Stability policy — **DONE**

`CHANGELOG.md` added (Keep a Changelog format) with a "Versioning and stability"
section stating it explicitly: pre-1.0 nothing is stable; from 1.0 the guarantee
covers the root package only, `internal/*` is exempt, a breaking change is one
that stops a root-package consumer compiling or alters documented behavior (major
only), a more-correct verifier/converter verdict is a fix not a break, and
deprecated symbols carry a `// Deprecated:` comment for at least one minor release
before removal in a later major. The `[Unreleased]` section is seeded with recent
work.

### 26. Security policy — **DONE**

`SECURITY.md` added: private disclosure via GitHub security advisory or
`contact@voidrab.com`, what counts as a vulnerability (crash/DoS/limit-bypass, a
false pass, silent content loss on convert) versus an ordinary bug, best-effort
response targets (3 / 10 business days), and pre-1.0 supported-version scope.

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
  Same class as item 1a, one layer up: the `ParseSfnt`/`ParseCFFTopDict` bails
  turned out to be covered by `ValidateFontProgram` reporting the same parse
  failure, and are now annotated with a test pinning that; the width-path bails
  have no such sibling.
- Document the `DecodeOptions` cache rule at `Reader`: only the default-options,
  non-image decode is cached, because `StreamKey` identifies raw bytes and would
  otherwise hand back a variant decoded under different parameters.
- Coverage to ~95%: verify 93.5%, convert 92.6%. CFF/Type1 fixtures are the bulk.

---

## Checked and already fine

Recorded so nobody re-investigates:

- **Decompression bombs**: capped at 256 MB, plus CCITT column/byte caps and
  ~15 depth and size limits across the parser. Only the silent-truncation
  behaviour is a problem (item 7).
- **Profile immutability**: `AddCheck`/`RemoveCheck`/`Clear` all clone. The
  `PDFA1B.RemoveCheck(...)` pattern in the README cannot mutate the global.
- **Corpus is committed**: 777 files tracked. Conformance genuinely does run in
  CI. It's `tests/regression/` that's missing (item 11).
- **False positives are tested**: 263 pass files in the veraPDF corpus. The gap
  is real-world producers (item 10), not pass-file coverage per se.
- **Predictors belong to Flate and LZW only** (ISO 32000-1 Table 8). Confirmed
  by scanning all three corpora: every `/Predictor` in every file sits on
  FlateDecode. The old chains applied the predictor after *any* filter, which is
  why three test fixtures had predictors on filters that cannot carry them.
- **CCITT stays an image-only filter.** Its output is packed 1-bpc samples that
  are meaningless without `/Columns`, `/Rows` and `/BlackIs1`, it is only legal
  as the terminal filter of an image, and no byte-consuming caller could use the
  result. `pdf.DecodeCCITT` remains the explicit second step for the rasterizer.

---

## Order of work

1. ~~**Item 1.**~~ Done. **Items 2–3** remain: nothing else matters if the
   verdict is wrong.
2. **Items 11 and 13.** Turn the dark differential harness on and put `-race`
   plus fuzzing in CI. Do this early — it's cheap and it changes what every
   later change is measured against. Now also the cheapest way to find whether
   item 1a's new check fires on any real-world file, which no synthetic corpus
   can answer.
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
