# Roadmap to 1.0

Goal: the best PDF/A-1b verifier and converter available in Go, good enough that
the API can be frozen. PDF/A-2/3/4 come after 1.0, not before.

Items 1–36 are done except for the tail of 36; each is one line under "Done",
and the commit history has the detail. Four things stand between here and a
tagged 1.0: items 30, 36, 37 and 38.

## Where things stand

- 159 checks across 10 groups. Every check in scope for 1b is implemented; the
  ten that are not each say why in the catalogue — six are PDF/A-1a, four are
  subsumed by the object-model checks. Isartor (204 fail files) and veraPDF
  (263 pass + 306 fail) both fully green, cross-checked against the veraPDF
  binary itself in CI, not just against filename expectations.
- Convert pipeline: pre-emptive fixups, verify/fix loop, raster last resort.
  Committed-corpus floor 510/510. On the 1585-file real-world corpus, all 1580
  reach conformance with zero errors, panics or hangs; 4 of them still lose a
  page's visible content and 1 still draws over one (item 36), and veraPDF
  rejects 7 of the outputs gopdfrab passes (item 38).
- The rasterizer draws what a PDF/A conversion actually meets: images (inline and
  stencil masks), all seven shading types, shading patterns, Type 3 glyphs. What
  it cannot draw is reported per page, never dropped in silence.
- One stream-decode chain covering every filter PDF/A-1 permits, with a typed
  result separating "encoded image data" from "broken".
- Arlington object-model checks, verify and convert, off a generated model.
- Fuzzing at three levels, including semantic oracles (determinism, honesty,
  convergence).
- Resource hardening: ~15 depth/size caps across the parser, settable decode and
  resident-cache budgets, no silent truncation anywhere.
- Coverage (`go test ./... -cover`): arlington 100%, cmd 95.8%, pdf 95.6%,
  verify 94.4%, pdfgen 94.8%, convert 94.1%, writer 94.1%, root 84.5%.
- 15–160x faster than veraPDF and PDFBox Preflight depending on metric.

---

## Open work

### 30. Coverage to ~95%

The root package is the outlier at 84.5%, and its whole shortfall is six thin
wrappers with no test at all: `VerifyContext` and `ConvertAllContext`,
`(*Document).VerifyContext` and `(*Document).ConvertContext`, and the newly
added `OpenBytes`/`OpenBytesWithPassword` — public API shipped untested. That is
cheap and worth doing before the surface is frozen. Everything else sits at
94–100%; per the standing decision, defensive parser guards are not chased, and
what remains there is CFF/Type1 fixtures.

### 36. The last two fidelity cases

Pinned in `surveyFidelity` over the 1580-file corpus: **4 files blank 6 pages,
and 1 file draws over 2** (from 20 files/47 pages and 27 files/717 pages when
the item opened). Eight causes were found and fixed by measuring one repair at a
time (`GOPDFRAB_FIDELITY_ATTRIBUTE`, `fidelity_attribution_test.go`) rather than
by ranking candidates. Two remain, both with a mechanism and a file against
them:

- **Taking an ExtGState's soft mask off changes the page.** `zenodo-20632795`
  page 5 goes from 0.989 ink to 0.045 under the graphics-state dictionary pass
  alone, and its pages 33–37 come out covered. Setting `/SMask` to `/None`
  (`internal/convert/fixups_dict.go`) is the same "make it opaque" mistake item
  35 fixed for `/ca`, one level up: a soft mask there applies to whatever is
  drawn under it, so there is no colour to fold it into and no image to hang a
  stencil on. What is left is to rasterize the content it masks, which is what
  the flattener does for a group.
- **One flattener case is still empty.** `oapen-26d73842` page 4 goes from
  0.064 ink to 0.000 under `transparencyFlattener.Fix` alone. Not investigated.

A trap worth keeping: the blanked figure *went up* twice while the conversion
was getting better, because a page covered in a black rectangle has plenty of
ink, so `Blanked()` never fired on it. Every increase was damage already there,
uncovered by the repair before it.

### 37. Cut the release

The goal is a frozen API, and nothing declares it frozen yet: `CHANGELOG.md`
still says "Pre-1.0: the API is not stable", its entries are under
`[Unreleased]`, and the README's Status section says pre-1.0. Once 30, 36 and 38
close, that is a changelog roll to `[1.0.0]`, a README edit, and a tag.

### 38. The seven outputs veraPDF still rejects

`GOPDFRAB_REALWORLD_VERAPDF=all` converts all 1580 files to something gopdfrab
calls conformant, and veraPDF rejects **7 of those outputs**. This gate is not
part of `go test ./...`, so it was failing unnoticed: it stood at 17 when the
item opened, and every one of the 17 was a gopdfrab false negative, not a
matter of interpretation. Ten were fixed by finding one cause at a time:

- **An annotation with no `/Type` never reached 6.5.3.** `/Type` is optional on
  an annotation dictionary (ISO 32000-1 12.5.2) and a viewer reaches one through
  a page's `/Annots`. Check and fixer both demanded `/Type /Annot` (2 files).
- **A forbidden action was emptied in place**, leaving the `/A` pointing at a
  dictionary with no `/S` — an action of no known type. The entry has to go, not
  just the contents (1 file).
- **An Info text string that does not survive a decode/re-encode round trip.**
  The XMP packet is built from the decoded text, so a `/Author` in UTF-16BE with
  an odd byte count leaves Info and XMP describing different strings (2 files).
- **A Type1 program's own `/Encoding` array was unreadable.** Only a *named*
  encoding was recognised; subset TeX fonts build the array themselves. Over the
  real-world corpus the encoding bail drops **4322 → 354** font dictionaries, and
  the programs reaching a live width comparison go **2877 → 6780** (3 files).
- **An advance width was rounded before it was compared**, quietly widening the
  ±1 tolerance to ±1.5 (1 file).
- **A code no cmap maps was skipped when the descriptor said "symbolic"**, though
  a viewer renders .notdef either way (1 file).

Two traps worth keeping. The sample of 17 could not validate the work: fixing
the Info strings **introduced** an eighth rejection on a file that was never in
the 17 (`arxiv-1911.06742`), because the pre-existing `TrimSpace` ran on the raw
bytes and took the low half off a trailing space — reproducing the exact
malformed value the repair exists to remove. Only the full 1580-file gate caught
it. And chasing the width cases meant verifying the *output*, not the source:
the converter substitutes fonts, so the dictionary veraPDF complains about is
often one gopdfrab created.

What is left is one clause pair, 6.3.5 with 6.3.6 following from it, in two
shapes:

- **Usage unknown, width zero (3 usgov scans).** These carry three font
  dictionaries sharing `KVHOAI+LiberationSans`; two have no entry in
  `UsedCharCodes`, so `ValidateSimpleTrueTypeSubset` falls back to iterating
  non-zero `/Widths` and skips the drawn code because its width is `0`. Not
  investigated past that point. Checking all 256 codes instead would report every
  unmapped code in every subset font, so the fix is to work out why usage
  attribution misses those dictionaries.
- **cmap precedence (`arxiv-1102.5670`, 2 zenodo, `oapen-0806b1b0`).** A
  non-symbolic font whose (3,1) cmap misses a code: gopdfrab falls back to the
  (3,0)/(1,0) cmap and finds the glyph, veraPDF renders .notdef and reports it
  missing. **Do not change this to match.** The fallback is deliberate — the
  comment in `codeToGID` records that treating a (3,1) miss as definitive
  rejected real PDF/A — and ISO 32000-1 9.6.6.4 describes it. veraPDF's own
  issue [#1575][] is the same defect in its `u`-level .notdef check: closed
  2026-04-22, milestone 1.30, root-caused to `GFGlyph.java` assigning .notdef
  "without consulting the font program's cmap, violating ISO 32000-1:2008
  section 9.6.6.4" — our reading, accepted upstream. The fix is already in the
  bundled 1.30.2 (its own test file passes there), but it was applied only to
  6.2.11.8, not to the PDF/A-1 6.3.5 path. Checked against the newest dev build,
  1.31.158 of 2026-08-20: **identical verdicts on all 17 files**, so waiting for
  a release does not close these four.

[#1575]: https://github.com/veraPDF/veraPDF-library/issues/1575

---

## Done

1. **Undecodable content streams no longer pass.** One decode chain
   (`internal/pdf/filters.go`), every PDF/A-1 filter, `Structure.StreamUndecodable`
   reported from both chokepoints.
2. **A bad xref offset no longer suppresses unrelated checks.** Per-object
   recovery at the resolution boundary: retry at the real `N G obj` header, else
   degrade to a cached null, both reported as 6.1.6.
3. **Convert never returns empty output with a nil error.** `ErrUnresolvableGraph`;
   reader-degraded objects are carried into the final result, as lost content
   rather than as a conformance failure (item 33).
4. **Whole-table xref recovery.** A missing or unparseable `startxref` rebuilds by
   full-file scan and synthesizes a trailer, reported as 6.1.4; linear time.
5. **Encryption.** Standard security handler (RC4 40/128, AES-128, AES-256), user,
   owner and empty passwords, applied at one choke point.
6. **The rasterizer no longer drops content silently.** `Tr`/`Ts` correctness fix,
   then inline images, all seven shadings, mesh shadings, Type 3 glyphs and
   shading patterns actually drawn; tiling patterns reported.
7. **Limits are settable and never truncate silently.** `Limits`/`SetLimits`,
   `--max-decoded-mb`, `--max-resident-mb`.
8. **Convert's memory.** `ConvertEach` + `Options.Workers`; output spills to a temp
   file; streaming content tokenization under a 64 MB budget; bounded, releasable
   caches; a slice-backed `PDFDict` (graph 23.5 → 16.7 MB, convert peak 63 →
   52.6 MB). The remaining piece is recorded under "Not in 1.0".
9. **Windows seek path.** Exercised on Linux via `pdf.OpenBytesSeek` parity tests;
   the absence of mmap on Windows is documented, not implemented away.
10. **Real-world corpus.** 1585 files, 3.9 GB, from five public collections plus a
    self-generated producer matrix, classified by veraPDF's own verdict,
    inventoried by hash and licence (bytes gitignored). Found nine real defects no
    synthetic fixture could reach.
11. **The differential harness runs.** veraPDF binary cross-check over both
    committed suites, in CI. Found two verifier false-negatives immediately.
12. **Fidelity gate.** Symmetric-renderer input-vs-output comparison;
    `Options.CheckFidelity`.
13. **CI.** `-race` on a 3-OS matrix, per-target fuzzing plus a nightly cron, wasm
    build, differential job.
14. **Thread-safety is documented and enforced**, with five `-race` tests.
15. **Options.** Two-argument entry points plus `…Context` counterparts taking a
    `context.Context` and an `Options` struct.
16. **Cancellation.** Per file in a batch, per verify/fix iteration, per raster pass.
17. **Results serialize.** `MarshalJSON` on `Check`, `PDFError`, `Result`.
18. **Streaming output.** `WriteTo`, lazy spill-backed `Output()`, `Close()`.
19. **Typed errors.** `ErrNotPDF`, `ErrDamaged`, `ErrEncrypted`, `ErrPasswordRequired`.
20. **A real CLI.** `cmd/gopdfrab`, public API only, meaningful exit codes.
21. **Naming.** Underscores dropped per staticcheck ST1003; `Get` prefix removed.
22. **Committed performance history**, one benchstat-readable file per round.
23. **Allocation guards** extended to every distinct cost path.
24. **Recovery benchmarked** (`BenchmarkXRefRecovery`): linear, not a DoS vector;
    no allocation guard on it yet.
25. **Stability policy** (`CHANGELOG.md`).
26. **Security policy** (`SECURITY.md`).
27. **Documentation.** `doc.go` plus runnable examples that double as tests.
28. **Repo hygiene.** `TODO.md` folded in and deleted, wasm `-o` in CI, tree clean.
29. **The catalogue tells the truth.** No registered check is silently dead, and
    the ones deliberately left to something else say so in place. `FontFileSubtype`,
    `XMPNoCorrespondingType`, `ICCBasedComponentsMismatch` and `FontBaseFont` were
    declared but never reported or had no fixer; all four now do what the catalogue
    says, with fixers that keep the document's own content.
31. **A declared FontMatrix is scaled into the widths, not skipped over.** Both
    width paths gave up on 6.3.6 when a program declared one, because charstring
    widths are in glyph space and `/Widths` is in 1/1000 em text space. Only the
    matrix's `a` term is needed — an advance is a pure x displacement, so shear
    and translation leave it alone and a nonzero `b` is the one form with no
    single width left to compare. Folding `a` into the widths where they are
    extracted fixes verify and convert at once, including a latent defect in
    `fixType1Widths`, which had no matrix guard at all and would have written raw
    glyph-space numbers into `/Widths`. Measured over the 1585 real-world files:
    of 2378 CFF programs declaring a FontMatrix, **2248 declare the default
    `[0.001 0 0 0.001 0 0]` and were being dropped for nothing**, 103 are 1/2048
    em, 24 are the default scale with a shear, 3 are other scales. On the
    committed corpora `type1c/font-matrix: 5` became `type1c/none: 9`. The Type1
    half stays speculative on measurement: `type1/font-matrix` does not fire once
    in 1585 files. `ParseCFFTopDict` now runs through `cffDictNumbers` to get
    there, which is where the matrix operands were being thrown away.

    What the newly-live checks found: nothing. 6.3.6 reports 687 findings across
    176 real-world files both before and after, so the skip was hiding no
    verdict — a wrong scale would have flooded that number instead. The check is
    live rather than silently dropped, which is item 29's principle, and the
    convert-side defect it uncovered is the part that was actually writing
    wrong output.

32. **An ICCBased colour space's own profile is checked.** `ICCBasedProfileInvalid`
    (6.2.3.2/2) rejects a profile whose version, device class or colour space
    PDF/A-1 does not allow, and the ICCBased fixer replaces it. Not speculative:
    38 real-world files carry an ICC v4 input profile, one of which converted to
    output gopdfrab called conformant that veraPDF rejected on exactly this rule.
33. **The last 16 files: 1580 of 1580.** Ten separate causes, all fixed — an 8 KB
    xref read window, `Default*` colour spaces taken from the wrong resource dict,
    zero-length Flate bodies, a junk Type 3 `CharProc`, outline items with no
    `/Title` or `/Parent`, an empty page-tree node, a trailer `/Info` pointing at
    the metadata stream, an `/SMask` reachable only through `/PieceInfo`, a 16-bpc
    image, a non-symbolic TrueType `/Encoding` with no `/BaseEncoding`. Lost
    content was split from non-conformance (`ConvertResult.LostObjects`), and
    `GOPDFRAB_REALWORLD_VERAPDF` now cross-checks converted real-world output.
34. **A large coordinate is folded into the CTM, not truncated.** Clamping a real
    to ±32767 is right for a dictionary value and wrong for a coordinate. The path
    is scaled down by a power of two and the same factor folded into the CTM (a
    `cm` pair, not `q`/`Q`, which would discard the clip), so nothing is rounded
    away and the drawing lands where it did. Measured over all 1580 files:
    **48 files blanking 485 pages before, 16 files blanking 38 pages after.**
35. **Content drawn at zero opacity is taken out, not painted over.** Raising an
    opacity of 0 to 1 paints invisible content over what it sat on, so the drawing
    goes first, in the same pass: a fill stops filling, a stroke stops stroking, an
    image or shading is dropped, text switches to the render mode that marks
    nothing. Clipping survives. Measuring it needed the other direction of the
    fidelity metric (`PageFidelity.Overpainted`): **58 files drawing over 1095
    pages, down to 27 files and 717.** Two defects surfaced and were fixed with it:
    the content scanner stopped at `'` and `"` (so rewriters wrote streams back
    with the rest of their drawing missing — `ContentScanner.Complete` now guards
    the five rewrite paths), and an oversized `cm` was still clamped (now written
    as two matrices that compose to it).
36. **Eight repairs that emptied a page or drew over one**, each found by measuring
    one repair at a time: a group form rasterized against the page's resources
    instead of its own; a group form rasterized in the page's default colour rather
    than the invoking stream's; a form measured in points (8424 pt at 150 dpi is
    217 megapixels — a pixel budget lowers the resolution instead); a soft mask
    composited over white rather than thresholded to a stencil; a drawing begun in
    no colour space, so every inherited fill came out black; a partial opacity made
    opaque instead of blended into the colour; "drew over" counting ink gained
    rather than paper covered (`PageFidelity.Covered`); and the fidelity comparison
    holding every rendered page of both sides, which cost gigabytes.

Also closed along the way: the Type1 `FontFile` width path honours
`/Differences`; an unresolved `PDFRef` in the verify walk is reported rather
than silently unverified; the DeviceN and resource-rename content rewriters no
longer retain the scanner's reused operand stack.

---

## Checked and already fine

Recorded so nobody re-investigates:

- **Decompression bombs**: capped, with CCITT column/byte caps and ~15 depth and
  size limits across the parser.
- **Profile immutability**: `AddCheck`/`RemoveCheck`/`Clear` all clone.
- **False positives are tested**: 263 pass files in the veraPDF corpus, plus the
  real-world should-pass half.
- **Predictors belong to Flate and LZW only** (ISO 32000-1 Table 8), and **CCITT
  stays an image-only filter**. Confirmed across all three corpora.
- **The real-world corpus stays local.** Multi-gigabyte and gitignored;
  `manifest.json` keeps it reproducible on any machine.

---

## Not in 1.0

- **Item 8's subtree-streaming resolution.** A verify-side optimisation that
  cannot apply to convert, which holds one trailer across up to four fix
  iterations and must enumerate every object before the first output byte. It
  also rests on permanent blockers: arrays have no object number and cannot get
  one without changing `PDFArray` from `[]PDFValue`, and inline dicts
  deliberately carry no `_ref`. The footprint was attacked by representation
  instead (item 8), which needed none of that.
- PDF/A-2, -3, -4. Adding parts before -1b is airtight spreads item 1's class of
  bug across four conformance levels.
- PDF/A-1a (accessibility, tagged PDF). Different problem, much larger.
- Digital signature validation.
- Rendering as a general-purpose feature. The rasterizer stays a conversion
  fallback.
