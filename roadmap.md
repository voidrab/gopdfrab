# Roadmap to 1.0

Goal: the best PDF/A-1b verifier and converter available in Go, good enough that
the API can be frozen. PDF/A-2/3/4 come after 1.0, not before.

Items 1–29 and 32–36 are done; each is one line under "Done", and the commit
history has the detail. What is still open is in "Open work".

## Where things stand

- 159 checks across 10 groups. Every check in scope for 1b is implemented; the
  ten that are not each say why in the catalogue — six are PDF/A-1a, four are
  subsumed by the object-model checks. Isartor (204 fail files) and veraPDF
  (263 pass + 306 fail) both fully green, cross-checked against the veraPDF
  binary itself in CI, not just against filename expectations.
- Convert pipeline: pre-emptive fixups, verify/fix loop, raster last resort.
  Committed-corpus floor 510/510. On the 1585-file real-world corpus, all 1580
  reach conformance with zero errors, panics or hangs; 4 of them still lose a
  page's visible content and 1 still draws over one (item 36).
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
- Coverage: arlington 100%, cmd 95.7%, pdf 95.5%, verify 94.3%, convert 94.1%,
  writer 94.1%, pdfgen 94.8%, root 92.5%.
- 15–160x faster than veraPDF and PDFBox Preflight depending on metric.

---

## Open work

### 30. Coverage to ~95%

verify 94.3%, convert 94.1%, writer 94.1%, root 92.5% — the root package is now
the widest gap. Per the standing decision, defensive parser guards are not
chased; the remainder is CFF/Type1 fixtures.

### 31. Scale Type1 widths instead of skipping them

`Type1WidthTable` skips the 6.3.6 comparison when the program declares a
non-default `/FontMatrix`, matching what the CFF path does. Scaling the
charstring widths by the declared matrix would keep the check live instead of
dropping it, and the real-world case that prompted the guard (a 1/2000 em font,
every width off by exactly 2x) says it would work. Speculative until a file
turns up where the skip actually hides something.

### 36. What the ink measurements still show

Over the 1580-file corpus, pinned in `surveyFidelity`: **4 files blank 6 pages,
and 1 file draws over 2.** It was 20 files and 47 pages blanked, 27 and 717
drawn over, when the item was written. Along the way the conversion also stopped
needing to rasterize 9 pages it used to (67 → 58) and stopped losing content in
8 files (17 → 9), because most of what it was rasterizing or dropping was work
it had made necessary itself.

Eight causes were found by measuring one repair at a time
(`GOPDFRAB_FIDELITY_ATTRIBUTE`, `fidelity_attribution_test.go`) rather than by
ranking candidates. All eight are fixed; each is one line under "Done" (36).

Two remain, both with a mechanism and a file against them:

- **Taking an ExtGState's soft mask off changes the page.** `zenodo-20632795`
  page 5 goes from 0.989 ink to 0.045 under the graphics-state dictionary pass
  alone, and its pages 33–37 come out covered. Setting `/SMask` to `/None` is
  the same "make it opaque" mistake item 35 fixed for `/ca`, one level up: a
  soft mask there applies to whatever is drawn under it, so there is no colour
  to fold it into and no image to hang a stencil on. What is left is to
  rasterize the content it masks, which is what the flattener does for a group.
- **One flattener case is still empty.** `oapen-26d73842` page 4 goes from
  0.064 ink to 0.000 under `transparencyFlattener.Fix` alone. Not investigated.

Recorded because it was a real trap: the blanked figure *went up* twice while
the conversion was getting better, because a page covered in a black rectangle
has plenty of ink, so `Blanked()` never fired on it. Every increase was damage
that was already there, uncovered by the repair before it. The association item
34 recorded — 11 of its 16 blanking files carrying a zero-alpha ExtGState — was
this, seen from the other side.

---

## Done

1. **Undecodable content streams no longer pass.** One decode chain
   (`internal/pdf/filters.go`), every PDF/A-1 filter, `Structure.StreamUndecodable`
   reported from both chokepoints, and the four usage-driven suppression sets
   discarded when usage is incomplete.
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
10. **Real-world corpus.** 1585 files, 3.9 GB, sourced from five public
    collections plus a self-generated producer matrix, classified by veraPDF's own
    verdict, inventoried by hash and licence (bytes gitignored). Found nine real
    defects no synthetic fixture could reach.
11. **The differential harness runs.** veraPDF binary cross-check over both
    committed suites, in CI. Found two verifier false-negatives immediately.
12. **Fidelity gate.** Symmetric-renderer input-vs-output comparison;
    `Options.CheckFidelity`. The "0 blanked pages" figure recorded here was from
    a sample; two files that do blank have since turned up — see item 34.
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
24. **Recovery benchmarked** (`BenchmarkXRefRecovery`): linear, not a DoS vector.
    No allocation guard on it yet.
25. **Stability policy** (`CHANGELOG.md`).
26. **Security policy** (`SECURITY.md`).
27. **Documentation.** `doc.go` plus runnable examples that double as tests.
28. **Repo hygiene.** `TODO.md` folded in and deleted, wasm `-o` in CI, tree clean.
29. **The catalogue tells the truth.** No registered check is silently dead any
    more, and the ones deliberately left to something else say so in place.
    `FontFileSubtype` and `XMPNoCorrespondingType` were declared but never
    reported; `ICCBasedComponentsMismatch` fired only from the output-intent
    path, never the ICCBased colour spaces its clause names; `FontBaseFont` had
    no fixer. All four now do what the catalogue says, with fixers that keep the
    document's own content: an OpenType font file is unwrapped to the CFF or
    TrueType program inside it rather than substituted away, and an ICCBased
    profile is replaced to match `/N` rather than the reverse, since `/N` is the
    operand count every `sc` in the file depends on.

32. **An ICCBased colour space's own profile is checked.** `ICCBasedProfileInvalid`
    (6.2.3.2/2) rejects a profile whose version, device class or colour space
    PDF/A-1 does not allow, and the ICCBased fixer replaces it. Not the
    speculative gap it was recorded as: 38 real-world files carry an ICC v4
    input profile, and one of them converted to output gopdfrab called
    conformant that veraPDF rejected on exactly this rule. Output-intent
    profiles were narrowed to the same clause's stricter set in passing, and
    `sgray.icc` — committed but unused — was embedded so a one-component space
    is repaired in place instead of being dropped for `DeviceGray`.

33. **The last 16 files: 1580 of 1580.** The item as written blamed most of the
    16 on a contradiction — that the written outputs verify clean on a fresh
    read. Re-measured file by file, that was false: 15 of the 16 reproduce their
    residual exactly on the written bytes. Every residual was a true finding,
    over ten separate causes, all fixed: the xref format check read a fixed 8 KB
    window and reported the leftover `"t"` of `trailer` as a malformed header
    (and left everything past 8 KB unchecked); the `Default*` colour-space
    exemption was taken from the page's resources instead of the resource
    dictionary actually in force, so it excused content inside a form XObject
    with its own — the one veraPDF disagreement that was ours; zero-length
    Flate bodies and a junk Type 3 `CharProc` had no fixer; outline items with
    no `/Title` or no `/Parent`; an empty page-tree node; a trailer `/Info`
    pointing at the metadata stream; an `/SMask` image reachable only through
    `/PieceInfo`; a 16-bpc image; a non-symbolic TrueType `/Encoding` dict with
    no `/BaseEncoding`. Lost content was split from non-conformance
    (`ConvertResult.LostObjects`), so a clean output is no longer called invalid
    because the reader degraded an object. `GOPDFRAB_REALWORLD_VERAPDF` closes
    the gap that hid two of these: the corpus is not in CI, so the differential
    harness never saw converted real-world output. Final run: 1580 of 1580
    conformant, 67 needing a rasterized page, 17 losing undrawable content;
    veraPDF rejected none of the sampled outputs.

34. **A large coordinate is folded into the CTM, not truncated.** Clamping a
    real to ±32767 is right for a dictionary value and wrong for a coordinate:
    the limit constrains the number, but what the file means is a position.
    `zenodo-21258097` opens every page with a full-page clip drawn at 1/508
    scale, `0 0 m 365760.0 0 l … h W n`; clamped, that clip shrinks from
    720x405 pt to 64.5x64.5 and takes 98% of the page with it. The output was
    conformant, so nothing said a word.

    The repair scales the path down by a power of two and folds the same factor
    into the CTM, so the drawing lands where it did and every number written
    stays in range. Powers of two because dividing a coordinate by one only
    shifts its exponent — nothing is rounded away, and the inverse matrix
    restores the CTM exactly. A `cm` pair rather than `q`/`Q`, since `Q` would
    discard the clip that is the whole point. Line width and dash are user-space
    lengths, so they scale with the path and are put back after it; a stroke
    whose width an ExtGState may have set out of view, a magnitude past what a
    matrix can carry, and a path too long to buffer are all left to the clamp.
    None of those three fires anywhere in the corpus.

    Measured with `Options{CheckFidelity: true}` over all 1580 files, which the
    item asked for and nobody had done: **48 files blanking 485 pages before,
    16 files blanking 38 pages after**, 32 files fixed outright, no regressions,
    no output turned invalid. `zenodo-21258097` went from 35 blanked pages of 36
    to none, and its output passes veraPDF and the BFO validator. What remains
    is item 36.

    Two of the item's own claims were false, in the way item 33's premise was:
    `zenodo-20632795` is not a coordinate case — it holds no out-of-range real
    anywhere — and the "862 of 1580 files carry a real outside the range"
    exposure figure never mapped to damage. `ConvertResult.BlankedPages` and an
    opt-in corpus survey (`GOPDFRAB_REALWORLD_FIDELITY`) exist now so the next
    such claim can be checked instead of assumed.

35. **Content drawn at zero opacity is taken out, not painted over.** PDF/A-1
    forbids transparency and the repair for an opacity below 1 was to set it to
    1, which is right for `0.9` and destructive for `0`: what a file draws at
    zero opacity is not there, and making it opaque paints it over what it was
    drawn on top of.

    The item named two files, and as in items 33 and 34 one of its claims was
    false: `zenodo-21249927` carries no zero-alpha graphics state anywhere, so
    whatever blanks 11 of its pages is not this (it blanks the same 11 before
    and after, and belongs to item 36). `zenodo-21258097` does carry them. The
    damage is real either way, and much wider than two files — see the numbers
    below.

    So the drawing goes first, in the same pass, while the opacity still says
    it is invisible: a fill stops filling, a stroke stops stroking, an image or
    shading is dropped, and text switches to the render mode that marks nothing
    — keeping its characters for anyone reading the file rather than looking at
    it. Clipping survives all of it, since a clip is set by the path and not by
    the operator that paints it. Every content stream is read on its own,
    starting fully opaque, so only what that stream declares invisible is taken
    out and a stream shared between two places comes out the same for both.
    Nothing is read at all unless the document has a graphics state that sets
    an opacity to zero.

    The measurement the item asked for came first, and needed a metric that did
    not exist: the fidelity gate reported ink *lost* and said nothing about ink
    *added*. `PageFidelity.Overpainted` and `ConvertResult.OverpaintedPages` are
    that other direction, and they found the problem to be far larger than the
    two files the item named — **58 files drawing over 1095 pages, down to 27
    files and 717 after**, with conformance unchanged at 1580 of 1580 and no
    page newly rasterized. Both named files' outputs pass veraPDF and the BFO
    validator. What remains is item 36.

    Two defects turned up in the measuring, both fixed here:

    - **The content scanner stopped at `'` and `"`.** Both show text, neither is
      made of letters, and the lexer reads a keyword as letters only — so they
      arrived as errors and the scan returned. Everything past the first one in
      a stream went unread by the verifier and by every fixer that rewrites
      content. Worse, the rewriters emit as they read, so a stream they stopped
      part way through was written back with the rest of its drawing missing.
      `ContentScanner.Complete` reports whether the scan reached the end, and
      the five rewrite paths now keep the original bytes when it did not.
    - **An oversized `cm` was still clamped.** Item 34 folds an out-of-range
      path coordinate into the CTM, but a `cm` is the placement itself, and it
      is what places an image: in a presentation drawn at 1/508 scale, clamping
      it collapsed every picture from 610x291 pt into the same 64.5 pt square.
      A matrix that does not fit is now written as two that do — a power-of-two
      scale, then the same matrix divided by it, which composes to exactly what
      was written.

36. **Eight repairs that emptied a page or drew over one.** Each was found by
    measuring one repair at a time over a file, not by ranking candidates:
    `GOPDFRAB_FIDELITY_ATTRIBUTE` runs the conversion's repairs singly and
    reports the ink each page had before and after, with the survey's own
    renderer and thresholds.

    - **A group form was rasterized against the page's resources**, so every
      image, font and colour inside it resolved to nothing and it came back
      blank — and where a whole page is one group form, which is how a
      presentation is exported, the page came out empty and conformant.
    - **A group form was rasterized in the colour a page starts in.** A form
      inherits the state of the stream that draws it and need never set a
      colour of its own; `zenodo-21193482` fills its background under the
      page's `1 g`, and from a page's black default that fill came out solid.
      The colour in force at each `Do` is read off the invoking stream now.
    - **A form is measured in points**, so an 8424-point one at 150 dpi came to
      217 megapixels — 871 MB to hold, and past what any reader decodes once it
      is written back, so the picture was there and nothing could draw it. A
      pixel budget lowers the resolution instead, which also bounds what the
      fidelity renderer holds.
    - **A soft mask was composited over white**, which is right only where the
      page behind the image is white. `zenodo-21227479` draws a photograph over
      a dark background: five of its pages came out blank, where poppler
      renders them at 98.6% ink. PDF/A-1 does allow two values of opacity —
      a stencil `/Mask` is PDF 1.3 — so the mask is thresholded and the image
      left alone, and what is behind it still shows. A soft edge becomes a hard
      one, which is the price. The renderer reads a stencil `/Mask` back now.
    - **A drawing began in no colour space at all**, so `sc` and `SC` did
      nothing until the content named one and every fill relying on the state
      it inherited was painted black. Twelve charts in one group came back as
      twelve black rectangles.
    - **A partial opacity was made opaque**, turning a shaded band over a chart
      into a solid block. What a file draws at 0.4 over white paper is its
      colour blended four parts in ten, so that is the colour the drawing is
      given and the opacity can go to 1 unseen. A stroking colour has to go
      back out as the operator it came in as, and a colour that blends to
      itself is not written at all.
    - **"Drew over" counted ink gained**, so it counted every page whose text a
      conversion made drawable that the file's own fonts could not draw — 651
      of the 717 pages, five books' worth of subset fonts missing the glyphs
      they show. Painting over a page covers it, so `PageFidelity.Covered`
      measures the share of it that was paper on the way in and is solid on the
      way out.
    - **The fidelity comparison held every rendered page of both sides**, which
      is over a gigabyte a side for a long book and what ran a 30 GB machine
      out of memory mid-sweep. It keeps 37 KB of measurements a page now and
      lets each render go.

Also closed along the way: the Type1 `FontFile` width path honours `/Differences`
(it was silently comparing against the wrong glyph, a false positive on
conformant files) and skips on a non-default `/FontMatrix`; an unresolved
`PDFRef` in the verify walk is reported rather than silently unverified; the
DeviceN and resource-rename content rewriters no longer retain the content
scanner's reused operand stack, which corrupted their output on any
multi-operator stream.

---

## Checked and already fine

Recorded so nobody re-investigates:

- **Decompression bombs**: capped, with CCITT column/byte caps and ~15 depth and
  size limits across the parser.
- **Profile immutability**: `AddCheck`/`RemoveCheck`/`Clear` all clone.
- **False positives are tested**: 263 pass files in the veraPDF corpus, plus the
  real-world should-pass half.
- **Predictors belong to Flate and LZW only** (ISO 32000-1 Table 8). Confirmed by
  scanning all three corpora.
- **CCITT stays an image-only filter.** Its output is packed 1-bpc samples,
  meaningless without `/Columns`, `/Rows` and `/BlackIs1`.
- **The real-world corpus stays local.** It is multi-gigabyte and gitignored;
  `manifest.json` keeps it reproducible on any machine, and re-fetching it per CI
  run is not a job worth having.

---

## Not in 1.0

- **Item 8's subtree-streaming resolution.** The original design was to resolve a
  page subtree, walk it, re-reference it and drop the object numbers. It is a
  **verify-side** optimisation that cannot apply to convert, which holds one
  trailer across up to four fix iterations, walks the whole graph per fixer, and
  must enumerate every object before the first output byte because numbers are
  DFS position — so it would not move the peak that matters. It also rests on
  blockers that are permanent: arrays have no object number and cannot get one
  without changing `PDFArray` from `[]PDFValue`, and inline dicts deliberately
  carry no `_ref`. The footprint was attacked by representation instead (item 8),
  which needed none of that.
- PDF/A-2, -3, -4. Adding parts before -1b is airtight spreads item 1's class of
  bug across four conformance levels.
- PDF/A-1a (accessibility, tagged PDF). Different problem, much larger.
- Digital signature validation.
- Rendering as a general-purpose feature. The rasterizer stays a conversion
  fallback.
