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

### 2. A single bad xref offset suppresses unrelated checks — **DONE**

Was: one bogus xref offset failed `ResolveGraph` wholesale, and
`verifyPdfA1bParts` bailed with a single document-level 6.1.6 — dropping the
6.7.2 catalog-Metadata finding (and every other graph-level check family) along
with the colour violation the damage legitimately hid. The issue list was wrong,
and a user fixing reported issues one at a time would never converge.

Fixed at the resolution boundary, not in the walk: a classic object that fails
to parse at its offset is retried once at its real `N G obj` header (whole-file
scan, last occurrence wins, memoized across objects) and degraded to a cached
null only when no intact copy exists. Both outcomes are recorded as per-object
6.1.6 diagnostics on the existing `parseDiagnostics`/`StructErrors` channel.
This pulled the per-object half of item 4's recovery forward, as this item's
cross-reference always implied, and item 4's oracle now holds:
`TestBrokenXrefOffsetOracle` verifies a broken-offset file to the same issue
set as the intact original plus exactly one recovery issue.

Findings from the work:

- **The wrong-value case was silent.** `validateObjectStart` parsed the
  header's object number and threw it away, so an offset landing on another
  object's well-formed header returned the wrong object with no diagnostic at
  all. It now returns the header number and a mismatch enters recovery. (The
  residual case — a garbage header whose body happens to parse as a scalar —
  still yields a 6.1.8 framing diagnostic only; full repair is item 4.)
- **Failed attempts had to be rolled back.** A parse attempt at a bad offset
  records 6.1.7/6.1.8 framing diagnostics before it knows it will fail; those
  are discarded per-object on failure (nested resolutions' records survive) so
  recovery leaves no noise and the oracle's issue-set equality holds.
- **Item 1's completeness lesson applies here too.** A nulled object makes the
  content-usage sets a subset of the truth exactly like an undecodable stream,
  so usage completeness is ANDed with `!HasDegradedObjects()` and every
  usage-driven suppression is discarded when anything degraded.
- **Both verifier bails dropped diagnostics.** The early returns for an
  unresolvable graph and an unbuildable page index skipped
  `structuralPostIssues`, so a degraded *catalog* would have swallowed its own
  diagnostic. Both bails now emit PostStructural.
- **Decryption failures degrade too** — the handler was authenticated at
  `Open`, so a mid-graph decrypt error is object-level corruption. Degradation
  is gated off during `initializeStructure` so `Open`'s error classification
  (`ErrDamaged`, `ErrEncrypted`, ...) is unchanged.

### 3. Convert can return no output and no error — **DONE**

Was: `convert.Run` fell back to verify-only when `ResolveGraph` failed and
returned `ConvertResult{Result: res}, nil` — success with nothing in it, which
`cr.Save(path)` turned into a different error much later.

With item 2, resolution failure is reduced to pathological cases (the resolve
depth cap); those now return the new `ErrUnresolvableGraph` sentinel (wrapped
per the item-19 pattern, re-exported from root) with the best-effort verify
`Result` still attached. The empty-output-with-nil-error path is gone.

One finding: degradation made convert's other half of the bug visible. The
final verify runs on the freshly-written output bytes, where a nulled object is
indistinguishable from an intentional null — so a conversion that lost content
would have verified *clean*. `Run` now carries the reader's degraded-object
diagnostics into the final `Result` and forces `Valid=false`; recovered objects
are deliberately not carried, since the rewrite emits a correct xref and
genuinely fixes them.

---

## P1 — resilience and unusual files

### 4. No xref recovery — **DONE**

Was: corrupting `startxref` gave `could not parse startxref offset`; truncating
gave `startxref not found`. Both were hard `ErrDamaged` failures — no
verification, no conversion, nothing. Files with damaged cross-reference data
are exactly the files people reach for a PDF/A converter to fix.

`initializeStructure` now treats a missing `startxref` keyword, a missing offset
token, and a non-numeric offset the same way it already treated an unparseable
xref *table*: a full-file `N G obj` scan (`recoverXRefByBruteForceScan`, last
definition wins) rebuilds the object table, and a new `recoverTrailer`
synthesizes the trailer — preferring a literal `trailer` dict if the tail still
has one, then a scanned cross-reference stream (`/Type /XRef`, which carries
`/Root`/`/Info`/`/ID`), then the document catalog (`/Type /Catalog`), returning
`<< /Root <ref> >>`. Catalog and xref-stream selection pick the latest by file
offset, so recovery is deterministic across map-iteration order. The rebuild is
reported as a 6.1.4 diagnostic and everything keeps going; a synthesized trailer
has no `/ID`, so the file is correctly reported non-conformant (6.1.3).

Findings:

- **The per-object half was already done in item 2** (recovery scan for a wrong
  offset, `TestBrokenXrefOffsetOracle`). This item is the whole-table case.
- **Trailer recovery with a *valid* xref stays a hard error.** When the xref
  parses but the trailer keyword/dict is missing or malformed, Open still fails
  rather than silently synthesizing — recovering there without the 6.1.4
  umbrella would risk the item-1 honesty gap (a rebuilt file verifying clean).
  Recovery is scoped to the case where the xref itself is unlocatable/unparseable.
- **Two 6.1.4 issues fire on a rebuilt file**, not one: the parse-time recovery
  diagnostic plus the pre-existing verify-time `checkXRefSectionFormat`. Both
  are legitimately about the broken xref; deduping across the parse/verify layer
  boundary was judged not worth the coupling.
- **The full-file scan is linear** (`BenchmarkXRefRecovery`, item 24): 100→5000
  objects scales ~linearly, a 5000-object damaged file recovering in ~30 ms, so
  recovery is not a DoS vector.

Oracle: `pdfgen.BreakStartxref` destroys the offset; `TestBrokenStartxrefOracle`
asserts the rebuilt file verifies to the intact issue set plus the 6.1.4
recovery issue, and `TestConvertRecoversBrokenStartxref` asserts the rewrite
emits a fresh xref with no 6.1.4 residual.

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

Passing a password to `Verify`/`Convert` (rather than pre-opening with
`OpenWithPassword`) landed with item 15 as `Options.Password` on the `…Context`
entry points, reaching the same internal path.

### 6. The rasterizer silently drops content — **mostly DONE**

Was: `internal/convert/raster.go` handled a decent operator set but silently
ignored `sh`/shadings, `BI`/`ID`/`EI` inline images, Type 3 fonts, and — worse
than dropping — `Tr`/`Ts`, so an invisible OCR text layer rendered *visible*
over the scanned image.

Two halves, per the roadmap's "a loud residual beats a quiet blank page":

- **The `Tr`/`Ts` correctness bug is fixed.** `showText` now honours the text
  rise (`Ts`) and paints no marks under render modes 3 and 7 (invisible/clip),
  so rasterizing an OCR PDF no longer prints its hidden text layer.
  `TestRasterInvisibleTextRenderMode` pins it (mode 3 → ~0 ink, mode 0 → ink).
- **The remaining drops are now reported, not silent.** The renderer records
  every feature it could not draw — `sh`, inline images (`INLINEIMAGE`),
  Type 3 fonts — accumulated across Form recursion (`r.execContent` shares the
  renderer). `RenderPage`/`renderContent` return the drop list; the page raster
  fallback carries it into `ConvertResult.RasterDrops []RasterDrop{Page,
  Features}` (re-exported from root). This closes item 12's documented blind
  spot: content the rasterizer drops symmetrically — invisible to the fidelity
  gate — is now a loud per-page residual.

Still open: actually *rendering* shadings, inline images, and Type 3 glyphs
(rather than reporting their loss) is deferred — a large rendering effort, and
the loud report already prevents silent data loss. Drops during the
transparency-flattener path (`flattenFormToImage`, a Fixer with no `ConvertResult`
handle) are not yet carried; only the page-level raster fallback reports.

### 7. Limits are hardcoded and fail silently — **DONE**

The caps are good (see "already fine" below). The silent-truncation half was
fixed earlier; the settability half is now done too.

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

**Settability — DONE.** The three per-stream output caps (Flate/LZW/RunLength,
all 256 MB) were consolidated into one configurable value and exposed as the
root-package `Limits` type with `SetLimits`/`CurrentLimits`/`DefaultLimits`, plus
a `--max-decoded-mb` CLI flag. Deliberately a **process-global** setter, not an
`Options` field: the caps are read by leaf decoders reached from ~50
free-function `DecodeStream` call sites that hold no `Reader`, so one global read
by every decoder enforces the cap uniformly (correct for both raising and
lowering — a per-call `Options` value would leave the Reader-less sites at
defaults, i.e. holes when lowering for safety) and, backed by `atomic.Int64`, is
race-clean. Resolve depth and the structural depth consts stay internal. The
default is unchanged, so no corpus behavior moved.

### 8. Convert holds everything in memory — **output half done; graph deferred**

Was: `ConvertAll` kept a full `ConvertResult` per input, each with the complete
output PDF as `[]byte`, so 500 files meant 500 output documents resident at once,
and the worker count was hardcoded to `NumCPU`.

**The batch half is done.** `ConvertEach`/`ConvertEachContext` (re-exported from
root) invoke a callback on each result as it completes rather than retaining
them, so a caller can write each output and drop it — peak memory is bounded by
the worker count, not the batch size. Both batch forms share one worker engine
and honour a new `Options.Workers` knob (0 = `NumCPU`). The callback is
serialized, delivered in completion order, and a non-nil return aborts the batch.

**The per-conversion output footprint is done too (with item 18).** Measurement
first, as this item asked: on the corpus-maximum sample (~3.9 MB) the returned
`ConvertResult` retained ~8.2 MB, of which the output buffer was ~4.4 MB — the
resolved graph, not the output, is the larger cost, but the output is the
tractable slice. So a conversion's output now spills to a temp file above an 8 MB
threshold and the final verify mmaps it, rather than holding the whole PDF in the
heap and re-opening it with `OpenBytes`; `ConvertResult.Output()` reads it back on
demand and `Close()` releases it (see item 18). `BenchmarkConvertMemory` and
`heapRetainedBy` guard it.

Still open (deferred): `Run` resolves the entire object graph into Go structures,
so a single large conversion still heap-loads what the read path went to trouble
to mmap — the ~4 MB graph above. Reducing it means partial/streaming resolution,
which the whole verify/convert model (a fully-resolved in-heap trailer) is built
against, so it is the larger rearchitecture, now backed by the numbers above.

### 9. Windows and macOS are untested — **DONE** (fallback tested + documented)

Was: CI ubuntu-only, and `mmap_other.go` returns nil on non-unix so Windows takes
an entirely different, never-exercised seek-based read path — and loses the
large-file guarantee.

Two corrections and a resolution:

- **macOS is not a separate path.** The `//go:build unix` tag covers darwin and
  the BSDs, so macOS uses `mmap_unix.go` exactly like Linux; the CI `macos` job
  (item 13) runs the same code. Only **Windows** hits `mmap_other.go`.
- **The Windows seek path is now exercised on Linux.** `pdf.OpenBytesSeek` parses
  in-memory data through the same `d.data == nil` → `NewLexerAt` (ReadAt/seek)
  path Windows takes without mmap. `TestSeekPathParsesLikeBytes` (internal/pdf)
  and `TestSeekPathVerifyMatchesBytes` (internal/verify, both committed corpora)
  assert it produces byte-for-byte the same parse and issue-for-issue the same
  verify result as the byte-slice path. That parity run immediately caught two
  latent map-iteration message nondeterminisms (6.3.5 CharSet glyph, 6.5.3
  appearance entry), now fixed — the fuzz determinism oracle missed them because
  it compares only `Valid` and `Count`.
- **The limitation is documented, not implemented away.** Windows still gets no
  mmap: parsing streams via `ReadAt` (bounded), but the whole-file recovery scans
  (`fullBytes`) heap-load on a damaged file, so the "larger than RAM" guarantee is
  unix-only. Implementing `CreateFileMapping`/`MapViewOfFile` was declined for now
  — it can only be tested on the Windows CI runner, not locally — and the seek
  fallback is proven correct instead. See `doc.go` and `mmap_other.go`.

---

## P2 — proving it works on files nobody wrote a test for

This section is the one the first draft of this roadmap missed, and it may matter
more than anything except P0. Everything currently green is green against
*synthetic conformance-suite files*. Both suites are hand-built to exercise one
clause each. Real PDFs are not like that.

### 10. There is no real-world corpus — **harness done; population pending**

Isartor and veraPDF are 777 files averaging 3.6 KB, each deliberately
constructed. Nothing in the test suite is a 200-page scanned report from a
real scanner, a LaTeX paper, an InDesign export, a Word document, or a
Ghostscript-converted invoice.

Two corpora, answering different questions, are now wired as `TestRealWorldCorpus`
(`convert_test.go`) over `tests/realworld/{should-pass,should-convert}/`:

- **Should-pass**: real files a tool and veraPDF both call PDF/A-1b. gopdfrab must
  verify every one clean; a rejection is a false positive and fails the test.
- **Should-convert**: ordinary non-PDF/A documents across producers. The test
  reports the fraction reaching conformance and how many did so only with dropped
  raster content (`ConvertResult.RasterDrops`). A precise "without raster" metric
  would want a convert-time rasterized signal — a possible follow-up.

The **PDF bytes are gitignored; the inventory is committed.** `manifest.json`
records each file's hash, licence, provenance and an optional source URL — never
the bytes. You drop PDFs into the corpus dirs and run
`scripts/gen-realworld-manifest.sh`, which hashes them and merges them into the
manifest (preserving annotations, stubbing new licences as `TODO`); this handles
self-generated PDF/A files, which have no URL. Entries that do carry a URL stay
reproducible via `scripts/fetch-realworld-corpus.sh`. `TestRealWorldCorpus`
checks every present file against the inventory (an unlisted file, hash mismatch,
or `TODO` licence fails), and skips when the corpus is absent;
`TestRealWorldHarnessSelfCheck`/`TestRealWorldManifestCheck` cover the metric and
inventory logic against generated fixtures so the harness is green regardless.
**Still pending: sourcing and populating** with real, permissively-licensed
documents (arXiv CC-BY, US-gov public domain, Wikimedia, and self-generated
producer output) — the remaining work is curation, not code.

### 11. The differential harness exists but never runs — **DONE**

Was: `convert_regression_test.go` cross-checked gopdfrab against the bundled
veraPDF binary — the right idea, dark in three ways: it skipped under `-short`
(what CI ran), skipped when the binary was absent (as in CI), and its corpus
`tests/regression/` was gitignored, so the documents that caught bugs lived on
one machine.

Turned on and expanded. `TestDifferentialVeraPDFCorpora` (conformance_test.go)
runs the veraPDF binary in one batch per corpus over both **committed** suites
(777 files) and diffs its verdict against gopdfrab's file-by-file — this
compares against veraPDF the implementation, not the filename expectations the
suite tests already assert. A verdict disagreement fails unless listed in
`differentialDeviations` with a justification (empty today). Clause-set
differences are logged, not failed, since check granularity legitimately
differs. A new CI `differential` job installs veraPDF via
`scripts/install-verapdf.sh` and runs this plus the regression cross-check, so
the harness is no longer dark. The `tests/regression/` real-world corpus stays
item 10 (licensing); `TestConvertNoResidualIssues` still covers it when present.

Running it immediately paid for itself — **two real verifier bugs**, both
false-negatives veraPDF caught and no synthetic test did:

- **6.2.7 PostScript XObject was profile-disabled, not reachability-gated.**
  PDFA1B dropped the `PostScriptXObject` check entirely because veraPDF's
  corpus passes a file with an *unreferenced* PS XObject — but veraPDF *fails*
  a referenced one (isartor-6-2-7-t01-fail-a). Now the check is gated on
  `isReachableXObject` exactly like Form XObjects, matching veraPDF on both. A
  new convert fixer neuters a referenced PS XObject into an empty Form XObject
  (PS passthrough renders nothing in a viewer), verified conformant by the
  veraPDF binary on the converted output.
- **Content usage never entered tiling-pattern streams.** A non-embedded font
  shown only inside a tiling pattern's content (isartor-6-3-4-t01-fail-h) was
  invisible to `ComputeContentUsage`, so `SkipUnusedSimpleFonts` wrongly
  suppressed the 6.3.4 finding. `scn`/`SCN` now scan the selected pattern's
  stream (deduped) for font and XObject usage, gated on the pattern actually
  being set.

Note the benchmarks README explicitly puts correctness out of scope and defers
to the corpora. That was fine while the corpora were the whole story. It isn't
now.

### 12. Nothing checks that converted output still looks like the input — **DONE**

Was: no fidelity test anywhere — the only guard was "does it verify clean,"
which blanking a page always improves. The known failure mode is on record:
conformant output that was destructively rasterized or blanked.

`internal/convert/fidelity.go` renders the input and the converted output with
gopdfrab's **own** rasterizer on both sides, so the renderer's operator-coverage
gaps are symmetric and cancel — the comparison isolates what the *conversion*
changed, not what the renderer cannot draw. `CompareFidelity` returns a
`PageFidelity` per page (`Similarity`, `InputInk`, `OutputInk`); `Blanked()`
flags the unambiguous data loss (input had ink, output is near-empty), which
unlike a raw similarity threshold does not fire on legitimate changes such as
font substitution.

Two deliverables:

- **The gate** (`TestConvertFidelityNoBlankedPages`): converts each corpus
  fixture and asserts no page was blanked. Running it found **0 blanked pages**
  across the full corpus. Rendering both sides is dominated by a heavy tail of
  megabyte fixtures, so by default it skips inputs over 50 KB (still ~95% of the
  corpus, 0.4 s) and `GOPDFRAB_FIDELITY_FULL=1` renders everything (~3 min); a
  blanking regression is systematic and shows up across the small fixtures too.
- **The report**: opt-in `Options.CheckFidelity` renders input (captured before
  any fixup mutates the graph) against output and populates
  `ConvertResult.Fidelity []PageFidelity`, re-exported from the root package.

The symmetric-renderer design has one blind spot — content the rasterizer
itself cannot draw (shadings, inline images, Type 3) is dropped on *both* sides,
so a raster fallback that loses it is not caught here. **Item 6 closes it**: the
rasterizer now reports those drops in `ConvertResult.RasterDrops`, so the loss
is loud even though the pixel comparison can't see it. The two together are the
complete fidelity story.

### 13. CI runs a fraction of the test suite — **mostly DONE**

Was: `.github/workflows/go.yml` ran `go test -short` on ubuntu only. Now the
workflow has:

- **`-race`.** Every matrix OS runs `go test -short -race` over all packages,
  exercising `TestGeneratedCorpusRace`/`TestConcurrentDecodeIsSafe` (green,
  including on the shared-Reader convert path).
- **Fuzzing.** `scripts/fuzz.sh` runs every one of the 26 `Fuzz*` targets; a
  `fuzz` job smoke-fuzzes each for 20 s on every push, and a `nightly-fuzz`
  cron job runs 5 min per target and uploads any crasher. Seeds replay from the
  committed corpus as before.
- **OS matrix** (item 9): ubuntu + windows + macos build and race-test.
- **wasm** (item 28): a `wasm` job builds `GOOS=js` with `-o /dev/null`.
- **Differential** (item 11): a `differential` job installs veraPDF and runs
  the cross-check.

Still open: the differential and regression jobs cover the committed corpora
but not a real-world one (item 10). Windows's seek-based read path is now
exercised on Linux via `pdf.OpenBytesSeek` parity tests (item 9).

### 14. Thread-safety is undocumented — **DONE**

Was: nothing in `gopdfrab.go` or `document.go` said whether a `*Document` may be
used from multiple goroutines. `Reader` carries several caches and one mutex that
guards only `DecodeStreamCachedConcurrent`, which strongly suggested the answer
was no — but callers couldn't know that, and `VerifyAll`/`ConvertAll` being
concurrent implied otherwise.

The contract is now what the code already did, stated at each type rather than
only in the package overview:

| Surface | Contract |
|---|---|
| `*Document` / `pdf.Reader` | **Not** safe for concurrent use — unsynchronized parse and decode caches. One per goroutine. |
| Package-level `Verify*`/`Convert*` | Safe to call concurrently; each opens its own document. |
| `*Profile`, `Options` | Read-only after construction (the profile mutators clone), so one value may be shared. |
| `Result` | Immutable once returned. |
| `ConvertResult` | `Output`/`WriteTo`/`Save` are safe concurrently — each opens the spill backing separately, sharing no read offset — but not with `Close`, which releases what they read. |
| `VerifyAll`/`ConvertAll`/`ConvertEach` | Internally concurrent, call from one goroutine; the `ConvertEach` callback is serialized. |
| `SetLimits`/`CurrentLimits` | Safe from any goroutine (atomics); a change applies to the next stream decoded, not to work in flight. |

Five `-race` tests enforce it, all cheap enough for CI's `-short` race job:
`TestDocumentPerGoroutineIsSafe` (a Document per goroutine over a shared
`*Profile`, every goroutine's issue set compared),
`TestConvertResultConcurrentReads`, `TestBatchHelpersConcurrentCalls` (a
`VerifyAll` and a `ConvertEach` running against each other),
`TestSetLimitsConcurrentWithVerify`, `TestVerifyContextConcurrentCancellation`,
plus `TestSeparateReadersAreIndependent` in `internal/pdf`.

Findings:

- **The negative half was verified, not assumed.** A throwaway test sharing one
  `Document` across four goroutines makes the race detector fire immediately, so
  "not safe for concurrent use" is a measured statement and the test suite would
  catch a regression that quietly made it safe-looking.
- **`EqualPDFValue` cannot compare resolved graphs.** A resolved graph is cyclic
  (a page's `/Parent` points back at its `Pages` node), so the obvious
  cross-goroutine comparison stack-overflows. `TestSeparateReadersAreIndependent`
  renders a cycle-safe signature instead (dict keys sorted, each `Entries` map
  visited once) — worth knowing before anyone else reaches for `EqualPDFValue` on
  a whole graph.
- **The `ConvertResult` read/Close split fell out of the spill design.** `bytes`
  and `writeTo` each `os.Open` the temp file, so concurrent reads were already
  safe; only `Close` (which clears `path`/`mem` under a `sync.Once`) races with
  them. Documented rather than locked — a mutex would buy nothing a caller
  can't get by owning the lifetime.

---

## P3 — API, before it gets frozen

The disclaimer says the API will change heavily before release. This is the list.
Do it in one pass.

### 15. Options — **mostly DONE** (merged with item 16)

Was: every entry point took `(path, profile)` and nothing else. Now the
two-argument forms cover the common case and each has a `…Context` counterpart
that adds a `context.Context` and an explicit `Options` struct:

```go
cr, err := gopdfrab.ConvertContext(ctx, path, gopdfrab.PDFA1B, gopdfrab.Options{
    Password:      pw,
    RasterDPI:     300, // replaces the former flattenDPI const
    MaxIterations: 8,   // replaces the former maxConvertIterations const
})
```

A plain struct value, not functional options: an early functional-options pass
(`WithRasterDPI(…)`) was rejected as too much machinery for a frozen API, and a
variadic config struct as too loose. `Options.Password` threads to the open
step (so it applies to `Verify`/`Convert` but not the `*Document` methods, whose
file is already open); `RasterDPI`/`MaxIterations` are convert-only. To set
options without a deadline, pass `context.Background()`. Internally
`convert.Options` and a `verify` password parameter are plain explicit params
(no variadic); the two-argument public forms pass the zero value.

The **resource-limit** caps from item 7 landed as a process-global
`SetLimits`/`Limits` surface rather than `Options` fields (item 7 explains why:
the caps are enforced at decode chokepoints reached from callers with no
document handle, so one global enforces them uniformly across verify and
convert). That closes the remaining half of both item 7 and this item.

### 16. No `context.Context` anywhere — **DONE**

Was: nothing was cancellable. Added `VerifyContext`/`VerifyBytesContext`/
`VerifyAllContext` and `ConvertContext`/`ConvertBytesContext`/
`ConvertAllContext` (plus `(*Document).VerifyContext`/`ConvertContext`), each
also carrying the `Options` from item 15 — one `…Context` variant per operation
rather than separate context and options overloads.

Cancellation is checked at the loop boundaries the roadmap named: **per file in
a batch** (`ConvertAllContext`/`VerifyAllContext` stop dispatching and record
`ctx.Err()` for files not started), **per verify/fix iteration**, and **per
raster pass** (`RunContext` checks before each). A single `Verify` is bounded by
the parser's resource caps, so `VerifyContext` checks once at entry rather than
threading ctx through the internal graph walk — a deliberately coarser
granularity documented at the function. The non-context forms delegate with
`context.Background()`, so no internal call site or the two-argument public form
changed.

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

### 18. Streaming output — **DONE**

`ConvertResult` has `WriteTo(io.Writer) (int64, error)` (implements
`io.WriterTo`), so callers can stream the rewrite to any sink without copying it
again. `Output` is now lazy: it changed from a `[]byte` field to a method
`Output() ([]byte, error)`, and a conversion's output spills to a temp file above
an 8 MB threshold (`spillWriter`) instead of staying resident, so the whole PDF
need not stay in the heap. `WriteTo`/`Save` stream straight from the backing
(in-memory bytes or the temp file); the final verify mmaps the temp file via
`pdf.Open` rather than heap-holding it via `OpenBytes`. A new `Close()` releases
the backing (removing the temp file); the backing is shared by pointer so value
copies of a `ConvertResult` Close idempotently. `ConvertEach` closes each result
after its callback; `Convert`/`ConvertAll` leave `Close` to the caller. On
js/wasm (no filesystem) output stays in memory unchanged. This is the memory
work item 8 named; the resolved-graph footprint there is the remaining, deferred
half.

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

### 20. A real CLI — **DONE**

Was: `cmd/` empty and `main/main.go` an example importing `internal/pdf` (which
external users cannot). Shipped `cmd/gopdfrab` built on the public API only,
with `verify` and `convert` subcommands, `--json`, meaningful exit codes
(0 conformant, 1 non-conformant, 2 error), recursive directory input on
`verify`, and `--profile`/`--password`/`--dpi`/`--max-iterations` flags. It uses
the item-15/16 `Options` and `ConvertContext`/`VerifyAllContext` entry points,
so a SIGINT cancels an in-flight run through the same context path. The
`run(ctx, args, stdout, stderr) int` core is table-tested without spawning a
process (exit codes, JSON shape, pass/fail/error, cancellation), and the
obsolete `main/` example was removed. `go install
github.com/voidrab/gopdfrab/cmd/gopdfrab@latest`.

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

### 23. Extend the allocation guards — **DONE**

Was: wall-clock on a dev machine is ±15% noisy, so the `allocs/op` assertions in
`benchmarks/micro/bench_test.go` are the only stable gate, and they covered only
the "large" torture-test sample (Open+Verify and Convert). Added
`TestCostPathAllocationsBounded`, a table-driven guard extending the ceilings to
the other distinct cost paths: `verify`/`convert` of the embedded-font sample
(`Convert/fonts` and its verify), Separation/DeviceN colour verification
(`large_color`), and the raster-fallback conversion (`raster`). Each ceiling is a
regression ceiling set ~10% over the measured, deterministic `allocs/op`, so a
reintroduced per-object re-parse (which multiplies allocations) trips it without
false positives.

### 24. Benchmark the recovery path — **DONE**

`BenchmarkXRefRecovery` (internal/pdf) opens the same document intact and with
its startxref destroyed at 100/1000/5000 objects. The full-file scan scales
linearly (10× objects → ~11× time; a 5000-object damaged file recovers in
~30 ms), so item 4's recovery is not a denial-of-service vector. Still local-only
until the benchstat history lands (item 22); no allocation guard yet.

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

### 27. Documentation — **DONE**

Was: one `Example()` in the entire repo, no `doc.go`, no package-level overview
on pkg.go.dev beyond a one-line comment. Added `doc.go` with a package overview
covering the verify/convert model, profiles, options/limits/cancellation and the
concurrency contract, plus runnable examples for the main entry points (`Verify`,
`VerifyBytes`, `ConvertBytes`, `ConvertEach`, `SetLimits`, `Profile.RemoveCheck`)
that double as tests via their `// Output:` blocks.

The README Roadmap/Status pass this item also called for was already done in an
earlier refresh — the "early stage"/"must be created" wording is gone.

### 28. Repo hygiene

`tests/regression/` is gitignored (item 11). Three stale multi-megabyte `.test`
binaries and two coverage files sit in the working tree — ignored, but they
suggest the working directory is doing double duty as a scratch space.
`GOOS=js GOARCH=wasm go build ./wasm/` fails without `-o` because the output
name collides with the directory; fix that and put the wasm build in CI so it
can't rot. `TODO.md` is gitignored, so items 20–22 of the old list live nowhere
shared — fold what survives into this file.

### 29. Carry-over from TODO.md

- ~~Document the `pdf.StreamKey` invariant at the type.~~ **DONE.** Stated at
  `StreamKey`: a key is meaningful only while something pins the bytes it was
  taken from (a key outliving its slice can be matched by an unrelated
  allocation at the same address), the resolved graph is that pin, and that is
  exactly why `AdoptStreamCaches` is sound between two Readers seeded with the
  same graph.
- ~~Document the null ≡ Go `nil` invariant.~~ **DONE.** Stated at `PDFValue`,
  with the three sites that depend on it and the trap it creates: a
  present-but-null entry is `Entries[k] == nil`, indistinguishable from absent,
  so `if _, ok := Entries[k]; ok` does not mean "has a value".
- ~~Deduplicate `parseClassicReference` against `parseObject`.~~ **DONE.** The
  scalar cases now live in one `parseScalarToken`, which both dispatchers call
  first; each keeps only what genuinely differs. `TestScalarDispatchAgreesAcrossParsers`
  compares the two over every scalar literal, and
  `TestObjectBodyIntegerIsNotAReference` pins the one legitimate divergence
  (`1 0 R` is a reference inside a containing object, but just the integer 1 as
  an object's whole body — a body is a value, not a reference chain).

  The gate was checked against the historical bug, not just written: making the
  object-body path return a `PDFName` for `null` again fails the test on the
  `null` case specifically.

  One sub-item **kept, not removed**: the old note called `checks_xmp.go`'s
  `val == "null"` comparisons dead now that null is nil. They are not — a
  producer can legitimately write the literal *string* `(null)` into an Info
  entry, which is what those comparisons still catch. Left in place.
- ~~Instrument the conservative skips in `CFFAdvanceWidths`/`CFFCIDAdvanceWidths`
  and the Type1 `FontFile` width path.~~ **DONE.** They bail on FontMatrix,
  unusual charstring prefixes, and Differences, and the bails were invisible —
  the same class as item 1a, one layer up. The three paths now return a typed
  `WidthSkip` reason and per-glyph counts alongside the widths
  (`CFFAdvanceWidthsStats`, `CFFCIDAdvanceWidthsStats`, `Type1WidthTable`); the
  plain functions stay as wrappers, so no call site or verdict moved. Nothing is
  reported to the user — this measures the coverage hole, it does not close it.

  `TestWidthSkipCorpusBudget` tallies every skip across all 773 committed corpus
  files against pinned numbers, so a path that starts or stops following a class
  of font program has to be re-pinned deliberately. The measurement: **of the 30
  embedded programs that reach a width path, 8 are given up on** — 5 bare-CFF
  programs declaring a FontMatrix (glyph space is not 1/1000 em, so the widths
  are not comparable) and 3 Type1 programs whose eexec section yields no
  charstring widths at all.

  One finding: **the Differences asymmetry costs nothing measurable.** The Type1
  `FontFile` path models only StandardEncoding and WinAnsiEncoding while the
  Type1C path handles `/Differences` — but no corpus file hits that bail, so the
  gap the old TODO flagged is real in principle and empty in practice. Worth
  knowing before spending effort closing it.
- ~~Document the `DecodeOptions` cache rule at `Reader`.~~ **DONE.** Stated at
  `DecodeStreamCached`: only the default-options, non-image decode is cached,
  because `StreamKey` identifies raw bytes and nothing else, so a cache shared
  across `DecodeOptions` would answer one caller with another's parameters.
  Anything needing options goes through `DecodeStreamFull` uncached.
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

1. ~~**Items 1–3.**~~ Done. P0 is clear: the verdict degrades per-object and
   convert never returns empty output without an error.
2. ~~**Items 11 and 13.**~~ Done. Differential harness runs in CI against both
   committed corpora (found two real verifier false-negatives immediately);
   `-race`, per-target fuzzing, an OS matrix and the wasm build are all wired.
   The remaining CI gap is the real-world corpus (item 10); the Windows read
   path is now covered by the item-9 parity tests.
3. ~~**Item 4.**~~ Done. Whole-table xref recovery: full-file object scan,
   catalog/xref-stream trailer synthesis, reported as 6.1.4, linear-time
   (item 24 benchmark confirms it is not a DoS vector).
4. ~~**Items 15–21.**~~ Done. The API break, in one pass, while the disclaimer
   covers it: items 15 (options), 16 (context), 17, 18 (lazy `Output`), 19,
   20 (CLI), 21.
5. **Items 10 and 12.** Item 12 (fidelity gate) done — the gate found 0 blanked
   pages across the corpus, and `Options.CheckFidelity` reports per-page fidelity
   in `ConvertResult`. Item 10's harness is done (hash-referenced external corpus,
   `TestRealWorldCorpus` + fetch script + manifest schema); populating it with
   real permissively-licensed files is the remaining curation work.
6. **Items 5–9.** Item 5 (encryption), item 6 (rasterizer `Tr`/`Ts` fix + drop
   reporting) and item 7 (settable limits, via a process-global `SetLimits`)
   done; item 8's batch half and its per-conversion output footprint done
   (`ConvertEach` + `Options.Workers`; lazy `Output` spilling to a temp file,
   item 18); item 9 done (Windows seek path exercised on Linux via
   `OpenBytesSeek` parity + documented). Remaining: item 8's resolved-graph
   footprint (the larger rearchitecture, now backed by measurement). Rendering
   the dropped features (shadings, Type 3) is the large deferred half of item 6.
7. **Items 22–29.** Continuous.

## Not in 1.0

- PDF/A-2, -3, -4. Adding parts before -1b is airtight spreads item 1's class of
  bug across four conformance levels.
- PDF/A-1a (accessibility, tagged PDF). Different problem, much larger.
- Digital signature validation.
- Rendering as a general-purpose feature. The rasterizer stays a conversion
  fallback — though items 6 and 12 will improve it considerably as a side effect.
