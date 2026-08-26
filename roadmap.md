# Roadmap to 1.0

Goal: the best PDF/A-1b verifier and converter available in Go, good enough that
the API can be frozen. PDF/A-2/3/4 come after 1.0, not before.

Items 1–43 are done; each is one line under "Done", and the commit history has
the detail. What stands between here and a tagged 1.0 is item 44, the release
itself. The conformance work is finished.

## Where things stand

- 159 checks across 10 groups. Every check in scope for 1b is implemented; the
  ten that are not each say why in the catalogue — six are PDF/A-1a, four are
  subsumed by the object-model checks. Isartor (204 fail files) and veraPDF
  (263 pass + 306 fail) both fully green, cross-checked against the veraPDF
  binary itself in CI, not just against filename expectations.
- Convert pipeline: pre-emptive fixups, verify/fix loop, raster last resort.
  Committed-corpus floor 510/510. On the 1585-file real-world corpus, all 1580
  reach conformance with zero errors, panics or hangs; 1 of them still loses a
  page's visible content and 1 still draws over two (item 36). veraPDF accepts
  every output bar 3, each of those a defect of its own listed with the evidence
  against it in `crossCheckDeviations`.
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
  verify 94.3%, pdfgen 94.8%, convert 94.1%, writer 94.1%, root 100%.
- 15–160x faster than veraPDF and PDFBox Preflight depending on metric.

---

## Open work

Item 44 is the release.

### 44. Cut the release

Nothing declares the API frozen yet. `CHANGELOG.md` still says "Pre-1.0: the API
is not stable" and files its entries under `[Unreleased]`, and the README's
Status section says pre-1.0. So:

1. Roll `[Unreleased]` to `[1.0.0]` in `CHANGELOG.md` and drop the pre-1.0
   paragraph; the stability policy below it stays as written.
2. Edit the README's Status section off pre-1.0, and its Roadmap section, which
   points at this file.
3. Clear out what is lying around: this file goes — the git log and the
   changelog carry its content — and `benchmarks/`, `scripts/` and the ignore
   lists want a look for files that no longer earn their place.
4. Merge `feature/roadmap` into `main`. CI has been green on the pull request
   throughout (item 37), and the push event runs the same jobs.
5. Tag `v1.0.0`. The existing tags stop at `v0.7.0`, so this is the first one
   that makes the promise in `CHANGELOG.md` binding.

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
30. **The root package's untested wrappers.** Its whole shortfall was six public
    functions with no test at all — `VerifyContext`, `ConvertAllContext`,
    `OpenBytes`, `OpenBytesWithPassword`, `(*Document).VerifyContext` and
    `(*Document).ConvertContext`. Each is now pinned in both directions: a
    cancelled context returns `context.Canceled`, a live one agrees with the
    non-context sibling, and the two password forms reach the decryption step.
    **84.5% to 100%**, every function in the package covered.
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

    Its tail was two more, found the same way. A form is drawn onto whatever the
    page already has, but the flattener rendered it on paper and wrote back one
    flat image, so the part of the BBox the form never painted covered the page:
    a page-covering group drawn last emptied the page under it. A form now
    renders on nothing and keeps what it did not paint as a stencil. And a soft
    mask with nothing in it opaque enough to survive a threshold is not a shape
    but a faintness — a photograph behind text at a flat 20% — so thresholding it
    masked the whole picture out; it now goes into the samples the way item 35
    puts a partial opacity into a colour, `/Matte` undone first, keeping a
    stencil only where the mask is genuinely clear. Fading without that shape
    turned one page from blanked into drawn over, which is this item's own trap
    met head on: the blanked figure went *up* twice while the conversion was
    getting better, because a page covered in a black rectangle has plenty of
    ink and `Blanked()` never fires on it. Every increase was damage already
    there, uncovered by the repair before it. Measured over all 1580 files:
    **4 files blanking 6 pages before, 1 file blanking 1 page after**; drawn
    over unchanged at 1 file and 2 pages.

    What the item recorded as its last two cases was half wrong, and measuring
    beat reasoning about it. Taking an ExtGState's soft mask off was blamed for
    `zenodo-20632795`, but that file's damaged pages carry `/ca` and no soft
    mask at all: pages 33–37 come out covered only under the attribution
    harness,
    which deliberately runs the dictionary pass without `repairOpacity` and so
    measures pre-item-35 behaviour. Its page 5 is real, and is item 35's own
    white-paper assumption — a near-white full-page wash at `ca 0.8` over a
    photograph blends to 0.9968 and erases the page. That page, and the two
    `zenodo-21284637` still draws over, are what the ceilings in
    `surveyFidelity` now pin. Whether an ExtGState soft mask costs any fidelity
    is unmeasured: nothing in 1580 files loses content to it, and the renderer
    still does not apply one.

37. **The changelog matches the branch, and the item's own premise did not.** It
    recorded 152 commits ahead of `main` and claimed none of them had been
    through CI, because the workflow triggers only on `main`. It is 178 commits,
    and the draft pull request from `feature/roadmap` fires the `pull_request`
    trigger, so the 3-OS race matrix, the wasm build, the differential job and
    the fuzz smoke test have run green on every push all along. The merge is not
    a first exposure. What was really behind was `CHANGELOG.md`: its
    `[Unreleased]` section stopped around item 33, leaving the object-model
    checks, item 29's four revived checks, the wasm build, the Go floor, the
    replaced gray profile and every font-program fix from items 31, 36 and 38
    unrecorded. All of it is in now, user-visible changes only — test, CI and
    doc-pinning work is not a changelog entry. The release itself is item 44.

38. **The five outputs veraPDF still rejected, at 1580 of 1580.** Four were one
    defect, and it was not in a font check: a page's `/Contents` array is a
    single stream split at token boundaries, so a `Tf` at the end of one part
    still selects the font for text shown at the start of the next. Usage
    attribution reset its text state per part, so those codes were attributed to
    no font — and a font with no usage entry falls back to iterating non-zero
    `/Widths`, where the drawn code's width is `0` and nothing is checked at
    all. The fifth is veraPDF's own: a subroutinized Type1 charstring pushes the
    `hsbw` operands and keeps the command in a subr, which its scan does not
    follow, so it reads a program width of 0 against a `/Widths` entry of 280.
    Making that case cost two hardcoded defaults of ours in the same place —
    the charstring decrypt always dropped 4 bytes though the Private dict's
    `/lenIV` says how many there are, and only `RD` was recognised as the
    charstring operator, not `-|` — which between them made a 37-glyph program
    read as having no glyphs and no widths. Widths now follow `callsubr`; the
    rasterizer's charstring interpreter still drops subr calls, which costs
    fidelity rather than conformance.

    The trap from the ten before it held again: the sample cannot validate the
    work, only the full gate can, and the dictionary veraPDF names is usually
    one gopdfrab created, so the output is what has to be verified, never the
    source.

39. **The public data model documents itself.** Every public type is an alias
    into `internal/`, and an alias carries none of the aliased type's fields or
    methods into the aliasing package's documentation, so `go doc . Check`
    printed no `Clause`, `Name` or `Description` and `ConvertResult` hid eleven
    members. Of the three ways out — declare the types in the root package,
    re-export a thin surface, or make the root package's own comments the
    reference — the last is the only one that costs no API change, and moving
    `ConvertResult` would have meant exporting the spill backing it holds. So
    each alias now carries its own field and method reference, which is exactly
    the text `go doc` and pkg.go.dev print. Two internal types were escaping
    through it as well: `PDFError.ObjectRef` and `PDFError.ObjModelDetail`
    returned `pdf.PDFRef` and `pdf.ObjModelDetail`, values a consumer could
    receive and not name; both are aliased now. A hand-written reference drifts,
    so it is pinned: `TestPublicTypesDocumented` fails when an exported field or
    method is not named in its type's doc comment, and
    `TestNoUnnameableInternalTypes` fails when a public signature reaches an
    internal type with no alias — the check that would have caught `PDFRef`.

40. **The bundled assets say where they come from, and one of them had to be
    replaced first.** `internal/convert/assets/` ships in the module zip and
    `go:embed` writes it into converted output, so a substituted font and an ICC
    profile are redistributed twice over. `assets/fonts/OFL.txt` was the unfilled
    SIL template, down to `Copyright (c) <dates>, <Copyright Holder>`, when the
    OFL's own condition is that the notice travel with the font; the two real
    notices were in the fonts' own name tables all along. `assets/profiles/` had
    no licence file at all, and tracing its three files turned up more than
    missing paperwork: `sgray.icc` was **byte-identical to Ghostscript's
    `iccprofiles/sgray.icc`**, which that project's LICENSE puts inside GPL
    Ghostscript, so every repaired one-component ICCBased space carried a
    copyleft asset into the reader's file and out through the commercial
    licence. It is now the CC0 sGrey v2 profile — same shape, 336 bytes instead
    of 416, and it costs nothing: all 1580 real-world files still convert to
    conformance and veraPDF still rejects none of the outputs, the same three
    known deviations aside.

    The CMYK half has no such exit, which is worth recording so nobody repeats
    the search. The one compact CC0 CMYK profile published is device class
    `scnr`, an input profile its own documentation says cannot be used for
    conversion *to* CMYK, so PDF/A-1 and `ValidateICCProfileStream` both refuse
    it as an OutputIntent; every clearly-licensed CMYK output profile is 2–8.6 MB
    against the 49 KB reduction bundled here, which would land in every
    CMYK-dominant output. So the profile stays, under the ICC's own terms, with
    what it declares recorded next to it.

    `NOTICE` names every bundled file — the 14 faces, the 3 profiles, and the
    vendored Arlington model, which ships in the zip too. A hand-written list
    drifts, so `TestNoticeCoversBundledAssets` fails when a file under `assets/`
    is not in it, and `TestBundledProfilesAreUsable` runs each embedded profile
    through the rules that pick it, since a licence problem is a reason to swap
    one out and a swap must not ship something PDF/A-1 rejects.

41. **The lint that is configured now runs.** `.golangci.yml` had been selecting
    staticcheck (SA\* only), ineffassign and unused with no CI job invoking it,
    so only `govet` was enforced — implicitly, through `go test`. The whole tree
    held exactly one violation, an ineffectual `obj = nil` in
    `heapRetainedBy` after the `runtime.KeepAlive` that already pinned the
    value. The new `lint` job runs the linter at a pinned version, and builds,
    vets and lints `benchmarks/` too: it is a separate module behind a `replace`
    directive against the root, so no other job would have noticed it rotting
    against an API change. `tests/` stays out — it is a marker module with no Go
    code.

42. **The minimum Go version is a decision now, not the newest patch.** All four
    `go.mod` files said `go 1.26.4`, which made that exact patch the floor:
    1.26.0–1.26.3 gets a toolchain download and `GOTOOLCHAIN=local` fails
    outright. The floor is `go 1.24`, the oldest that can work — the one
    dependency, `klauspost/compress`, declares `go 1.24` itself. A `floor` CI
    job builds and tests at 1.24.x so the real minimum cannot drift up
    unnoticed, and `TestGoDirectivesAgree` holds the four files to one another
    and rejects a patch-level directive.

43. **The README documents the surface that exists.** The godoc has been the
    reference since item 39, but the README is where a reader starts, and five
    public functions appeared nowhere in it — `VerifyBytesContext`,
    `ConvertBytesContext`, `ConvertEachContext`, `OpenBytesWithPassword` and
    `NewProfile`, plus `OpenBytes`, found by the guard rather than by reading.
    The CLI section listed four flags for both subcommands when there are five,
    and none of `convert`'s own three, nor the `version` and `help`
    subcommands. `wasm/` builds a real `syscall/js` wrapper with its own CI job
    and was not mentioned once; it now has a section naming the two globals it
    registers and the shapes they resolve to.

    Three things the freeze had to settle rather than inherit. The object-model
    profile had two spellings, `PDF` in `doc.go` and `ObjectModelOnly()` in the
    README: `PDF` is the one spelling now, with `ObjectModelOnly()` documented
    as the way to get a profile nothing else shares. `PDF`, `PDFA1B` and
    `Legacy1B` stay exported **variables** — that is how a Go package carries a
    default, and the profiles themselves are immutable — so what reassigning
    one costs is written next to them instead of engineered away.
    `Checks.Colour` keeps the ISO spelling, which ISO 19005 and ISO 32000 both
    use, recorded so the question is closed rather than reopened after the
    freeze.

    A hand-written reference drifts, which is how it got here, so it is pinned
    the way items 39 and 40 pinned theirs: `TestREADMENamesPublicAPI` fails
    when a package-level exported function is not named in the README, and
    `TestREADMEDocumentsCLIFlags` reads the flag names out of `cmd/gopdfrab`
    itself rather than trusting a second list. Both were checked by breaking
    them.

Also closed along the way: the Type1 `FontFile` width path honours
`/Differences`; an unresolved `PDFRef` in the verify walk is reported rather
than silently unverified; the DeviceN and resource-rename content rewriters no
longer retain the scanner's reused operand stack; and the corpus harness
self-check no longer fails when `GOPDFRAB_REALWORLD_VERAPDF=all` is set, which
made it sweep the whole deviation list against one generated file.

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
