# PDF/A-1b Conversion — Roadmap to Completeness

> **Purpose.** This document is the high-level roadmap for taking gopdfrab's PDF/A-1b
> *conversion* from its current state (419/510 corpus fixtures fully converted) to as close to
> *complete* as the format allows. Each phase below is scoped to be the input for a later,
> more specific implementation plan (`/plan`). It states the goal, the exact checks targeted,
> the approach, any external assets/tooling required, the test bar, and risk — but deliberately
> stops short of line-level design, which belongs in the per-phase plan.
>
> **How to read it.** Phases are ordered by value-per-unit-effort: cheap, asset-free,
> rendering-neutral wins first; font/content-stream/rasterization work (which needs new
> infrastructure and bundled assets) last. Phases 1–6 are **done**; 7+ are the roadmap.

---

## 1. Current state (done)

The pipeline lives in `convert.go`:

```
PDF -> pre-emptive fixups -> [ WriteDocument -> verify -> targeted Fixers ]* (<=4 passes) -> output
```

| Phase | Landed | Mechanism |
|-------|--------|-----------|
| 2 | Bare loop + writer | `WriteDocument` re-emits a clean classic xref + framed objects, fixing all of 6.1.x structural by construction |
| 3 | Dictionary key-edit `Fixer`s (`fixups_dict.go`) | actions 6.6, ExtGState transparency 6.2.8/6.4, annotation flags 6.5.3, forms 6.9, image/form XObject metadata 6.2.4–6.2.7 |
| 4 | Pre-emptive fixups | `regenerateXMP` (6.7), `injectOutputIntent` + embedded ICC (6.2.2, device colour 6.2.3) |
| 5 | ICC embed + font dict fixer | embed `assets/sRGB2014.icc`; `fontDictFixer` adds `/CIDToGIDMap /Identity` (6.3.3.2) |
| 6 | Dictionary-level fixer expansion (family A) | see §1.1 below — annotation subtype/colour, file specs/embedded files, PostScript form XObjects, optional content, Type0 CIDSystemInfo/WMode, Info-dict normalization, writer-synthesized `/ID` |

**Regression floor:** `minConvertedFully = 419` of 510 (`convert_test.go`). Latest corpus
sweep: **419 fully conformant · 49 known-hard residual · 42 other residual · 0 errored**.

Key reusable infrastructure already present:
- `walkDicts` (graph walk with cycle protection) — `fixups_dict.go`
- `newContentScanner(...).scan(...)` (content-stream **reader**/tokenizer) — `content.go`
- Font-program parsers for TrueType/CFF/Type1 (glyph coverage, widths, cmap) — `checks_font_program.go` (~1300 lines, used today only for *validation*)
- `Fixer` registry + pre-emptive fixup registry — `convert_fixers.go`
- `ResidualCategory` — classifies a leftover check as font/content-stream/transparency-hard — `residual.go`
- `decodeStream` / `decodeStreamCached`, predictor & filter handling — `stream.go`, `predictor.go`

### 1.1 Phase 6 detail (landed, asset-free per project decision)

No CMYK ICC profile or substitute fonts were pulled in for this phase — both remain
deferred to Phases 10/11 as originally scoped. Landed fixers, each mirroring its check's
detection logic in reverse via `walkDicts`:

| File | Fixer(s) | Checks fully cleared |
|------|----------|----------------------|
| `fixups_annot.go` (new) | `disallowedAnnotFixer`, `annotColourFixer` | `DisallowedSubtype` 6.5.2, `ColourWithoutIntent` 6.5.3 |
| `fixups_filespec.go` (new) | `fileSpecFixer` | `EmbeddedFileSpec`/`EmbeddedFiles` 6.1.11, `StreamFileSpec`/`StreamFileFilter`/`StreamFileDecodeParams` 6.1.7 |
| `fixups_dict.go` (extended) | `postScriptXObjectFixer`, `optionalContentFixer` | `FormPSEntry`/`FormPostScript`/`FormSubtype2PS` 6.2.5/6.2.7, `OptionalContent` 6.1.13 |
| `fixups_font.go` (extended) | `type0FontFixer` | `CIDSystemInfoMismatch` 6.3.3.1, `CMapWModeInconsistent` 6.3.3.3 |
| `writer.go` (modified) | `WriteDocument` synthesizes a deterministic `/ID` when absent | `TrailerID` 6.1.3 |
| `fixups_xmp.go` (extended) | `normalizeInfoDict` (called from `regenerateXMP`) + control-character escaping | `InfoDictXMPMismatch`/`InfoXMPSync`/`XMPNotWellFormed` — **mostly** cleared, see below |

Every check above is **fully** eliminated from the corpus residual except the last row,
which has two small known leftovers (1 fixture each) traced to root cause:
- **`InfoXMPSync`/`InfoDictXMPMismatch`, 1 fixture** (`veraPDF test suite 6-1-5-t01-fail-b.pdf`):
  `checkInfoXMPSync`'s Author/`dc:creator` comparison (`checks_xmp.go`) compares the **raw**
  Info `/Author` string against the **trimmed** extracted XMP value — an asymmetric trim. When
  Info's `/Author` has leading/trailing whitespace (as this fixture's does, `" veraPDF
  Consortium "`), no XMP packet can satisfy both a byte-faithful round-trip and that
  comparison simultaneously. Closeable in a follow-up by trimming `/Author` itself in
  `normalizeInfoDict` (a one-line addition) — deferred here since it's a verifier/fixer
  interaction subtlety discovered only by empirical sweep, not part of the original brief.
- **`XMPNotWellFormed`, 1 fixture** (`veraPDF test suite 6-1-5-t01-fail-d.pdf`): an Info string
  (`/Keywords`) contains a byte that isn't valid UTF-8 (non-UTF-8 source encoding). `xmlEscapeText`
  deliberately does not decode/re-encode Info strings as UTF-8 (see its doc comment) to keep
  XMP/Info byte-for-byte in sync for `checkInfoXMPSync`'s other comparisons — but an invalid
  UTF-8 byte is also invalid raw XML 1.0 text, regardless of entity-escaping. This is a genuine
  tension between "byte-exact sync" and "well-formed XML"; the C0-control-character hardening
  landed in Phase 6 closes the control-character class of this check but not invalid-UTF-8 bytes.
  A full fix needs either a lossy re-encode (breaks Info/XMP sync for non-UTF-8 fields) or
  XML 1.0's numeric-character-reference escape for the invalid byte (preserves sync, slightly
  more code) — left as a follow-up, not attempted here.

---

## 2. Grounding: what actually remains (residual inventory)

Re-measured after Phase 6 landed, by converting every corpus "fail" fixture and tallying the
checks still present. `FIX` = distinct fixtures affected; `HARD` = currently classified as
needing re-encoding/rasterization by `ResidualCategory`. Every check Phase 6 targeted (§1.1) is
gone from this table except the two single-fixture XMP edge cases noted above, which now show
under their own (no longer family-**A**) root cause.

| Check | Clause | FIX | HARD | Fix family |
|-------|--------|-----|------|-----------|
| SubsetGlyphCoverage | 6.3.5 | 12 | yes | **D** font subset |
| DeviceColourContentStream | 6.2.3.3 | 10 | no | **C** content-stream colour |
| AdvanceWidthMismatch | 6.3.6 | 9 | yes | **D** font metrics |
| IntegerOutOfRange | 6.1.12 | 7 | yes | **C** content-stream / **A** structure |
| AppearanceNNotStream | 6.5.3 | 6 | no | **B** appearance synth |
| WidgetMissingAppearance | 6.9 | 5 | no | **B** appearance synth |
| StreamLZWFilter | 6.1.10 | 5 | no | **A'** stream re-encode |
| StringTooLong | 6.1.12 | 5 | yes | **C** content-stream |
| CIDSubsetCIDSet | 6.3.5 | 4 | yes | **D** font subset meta |
| Type1SubsetCharSet | 6.3.5 | 3 | yes | **D** font subset meta |
| InvalidProgram | 6.3.2 | 3 | yes | **D** font re-embed |
| UndefinedOperator | 6.2.10 | 2 | yes | **C** content-stream |
| HexStringOddLength | 6.1.6 | 2 | no | **C** content-stream |
| RenderingIntent | 6.2.9 | 2 | no | **C** content-stream (`ri`) |
| AppearanceMissingN / AppearanceExtraEntries | 6.5.3 | 2/2 | no | **B** appearance synth |
| TransparencyGroup | 6.4 | 2 | yes | **E** flatten/raster |
| NameTooLong / CMapCIDOutOfRange | 6.1.12 | 2/2 | yes | **C** limits |
| TrueTypeEncoding / SymbolicTrueTypeEncoding / SymbolicTrueTypeCmap | 6.3.7 | 1/1/1 | no | **D'** font encoding (rendering-affecting) |
| CIDNotEmbedded / CMapNotEmbedded | 6.3.4 / 6.3.3.3 | 1/1 | yes | **D''** font substitution (needs bundled fonts) |
| **InfoXMPSync / InfoDictXMPMismatch** | 6.7.3 / 6.1.5 | 1/1 | no | **A** Info-dict normalize — *one fixture remains, root-caused in §1.1: asymmetric trim in `checkInfoXMPSync`'s Author comparison* |
| **XMPNotWellFormed** | 6.7.9 | 1 | no | **A** XMP regen edge case — *one fixture remains, root-caused in §1.1: non-UTF-8 byte in an Info string* |
| ImageInterpolate (inline) | 6.2.4 | 1 | no | **C** content-stream |
| InlineImageLZWFilter | 6.1.10 | 1 | yes | **C** content-stream |
| HexStringInvalidChar | 6.1.6 | 1 | no | **C** content-stream |
| DictTooLarge / ArrayTooLarge / DeviceNColorants | 6.1.12 | 1/1/1 | mixed | **C**/**A** limits |
| ImageWithSoftMask | 6.4 | 1 | yes | **E** flatten/raster |
| XRefSubsectionHeader / GraphResolutionFailure | 6.1.4 / 6.1.6 | 1/1 | no | parser recovery (unfixable if graph won't resolve) |

**Fix families:**
- **A** — dictionary-level edit/delete (no new assets, reuse `walkDicts`). *Biggest cheap win.*
- **A'** — stream re-encode at the object level (decode forbidden filter, re-Flate).
- **B** — appearance-stream synthesis (small content-stream **generation**).
- **C** — content-stream **rewriting** (needs a content-stream writer/serializer).
- **D** — font program work (read/subset/re-embed; **D''** needs bundled replacement fonts).
- **E** — transparency flattening / rasterization (needs a renderer).

---

## 3. External assets & tooling required

A "complete" converter cannot be pure-Go-logic-only; some phases need bundled binary assets
and new internal toolkits. Plan the repo layout up front:

```
assets/
  icc/
    sRGB2014.icc        # present — RGB output intent (v2)         [Phase 4/5]
    <CMYK>.icc          # NEEDED — real CMYK v2 profile            [Phase 6/11]
    <Gray>.icc          # OPTIONAL — sGray v2 (gray is covered by any intent)
  fonts/
    LiberationSans-*.ttf   # NEEDED — Helvetica/Arial metric match [Phase 10]
    LiberationSerif-*.ttf  # NEEDED — Times metric match
    LiberationMono-*.ttf   # NEEDED — Courier metric match
    <Symbol/Dingbats>      # NEEDED — Symbol & ZapfDingbats subs
```

### 3.1 ICC colour profiles
- **RGB:** `sRGB2014.icc` (present, ICC v2 — satisfies the `major ≤ 2` rule).
- **CMYK:** a *real* CMYK ICC **v2** profile is **required** for correct CMYK documents.
  Today `injectOutputIntent` fakes CMYK by overwriting the sRGB profile's colour-space
  signature bytes (`withICCColorSpace`) — this *passes verification* but is colorimetrically
  wrong. Candidates: a permissively-licensed coated-FOGRA/SWOP v2 profile. License must allow
  redistribution (verify each profile's terms).
- **Gray:** optional; any output intent covers DeviceGray, so a dedicated sGray profile is
  only needed if we ever want a gray-only intent.
- **Hard constraint:** every embedded profile must be ICC **≤ v2.x** (`validateICCProfileStream`,
  `verifier.go`) — ICC v4 profiles (e.g. `sRGB_v4_ICC_preference.icc`) are rejected by PDF/A-1.

### 3.2 Replacement fonts (Phase 10)
Embedding is mandatory in PDF/A-1b even for the "standard 14" fonts. To embed a font that the
input only *references*, we must ship metric-compatible, embeddable, freely-licensed faces:
- **Liberation** family (SIL OFL) — Sans↔Helvetica/Arial, Serif↔Times, Mono↔Courier.
- **Symbol / ZapfDingbats** — need an OFL/permissive substitute (e.g. a URW++ base-35 face
  under AGPL/permissive, or a purpose-built symbol font). Confirm license before bundling.
- Licensing note: bundle only OFL/permissive fonts; record each license under `assets/fonts/`.

### 3.3 New internal toolkits (no binary assets, but significant code)
- **Font toolkit** (Phase 9/10): promote `checks_font_program.go`'s parsers into a read/write
  toolkit — read glyph set, advance widths, cmap; **subset** TrueType/CFF; build a minimal
  embeddable program. Reuse existing parsing; add emission.
- **Content-stream writer** (Phase 11): the inverse of `newContentScanner` — serialize a
  token stream back to bytes, so a fixer can rewrite operands/operators (colour conversion,
  drop `ri`, remove unknown operators, clamp limits).
- **Renderer / rasterizer hook** (Phase 13): pluggable last-resort that renders a page to an
  image and rebuilds it. Likely an *external* dependency (Ghostscript/pdfium via an interface)
  rather than a built-in renderer.

---

## 4. Cross-cutting infrastructure (do early, benefits many phases)

These are not user-visible features but unblock and de-risk later phases. Schedule them as
their dependents arrive (noted per phase), not all up front.

- **CW-1 · In-memory verify.** `convert.go`'s loop currently writes a temp file and re-`Open`s
  it every pass (`verifyBytes` → `writeTempFile` → `Open`). Add an in-memory verify path so the
  round-trip avoids disk I/O and a full re-parse. *Big perf win, see §6.*
- **CW-2 · Single batched graph walk per pass.** Today each `Fixer` does its own `walkDicts`.
  As family **A** grows to a dozen fixers, dispatch them through **one** walk per pass (visit
  each dict once, offer it to every applicable fixer). Keeps per-pass cost O(graph), not
  O(graph × fixers).
- **CW-3 · Content-stream writer** (prereq for family **B**, **C**).
- **CW-4 · Font read/write toolkit** (prereq for family **D**).
- **CW-5 · Pluggable rasterizer interface** (prereq for family **E** / Phase 13).

---

## 5. Phased roadmap

### Phase 6 — Dictionary-level fixer expansion (family A) — ✅ **done, see §1.1**
Landed: annotation subtype/colour, file specs/embedded files, PostScript form XObjects,
optional content, Type0 CIDSystemInfo/WMode, Info-dict normalization, writer `/ID` synthesis.
33 fixtures moved to fully conformant (386 → 419); two single-fixture XMP edge cases remain,
root-caused in §1.1. CW-2 (single batched walk) was **not** done — left as a pure-perf
follow-up now that the family-A fixer count has grown to ~13; see §4.

### Phase 7 — Object-stream filter re-encoding (family A')
**Goal:** `StreamLZWFilter` 6.1.10 (5). LZW is forbidden; decode the LZW stream and re-emit it
Flate-encoded. The writer already Flate-encodes streams marked dirty, so the work is: add an
**LZW decoder**, decode on detection, drop `/LZWDecode`, mark the stream dirty.
**Assets:** none. **Infra:** small (LZW decoder). **Risk:** low — lossless filter swap.
**Note:** inline-image LZW (`InlineImageLZWFilter`) lives in content bytes → Phase 11.

### Phase 8 — Appearance-stream synthesis (family B)
**Goal:** `WidgetMissingAppearance` 6.9 (5), `AppearanceNNotStream`/`AppearanceMissingN`/
`AppearanceExtraEntries` 6.5.3 (10). Generate a minimal valid normal-appearance (`/AP /N`)
form XObject for annotations/widgets lacking one, and normalize malformed `/AP`.
**Assets:** none (uses a standard-14-substitute font only if text APs are needed → may pull a
small dependency on Phase 10 for field text; start with empty/box APs).
**Infra:** CW-3 (content-stream **writer**) — first consumer.
**Risk:** medium — synthesized appearance may not match the original intent visually, but a
conformant empty/neutral AP is acceptable for 1b and better than non-conformance.

### Phase 9 — Font metric & subset-metadata repair from the embedded program (family D, no new fonts)
**Goal:** fix font issues using data already inside the file, by reading the embedded program.
**Targets:**
- `AdvanceWidthMismatch` 6.3.6 (9) — recompute `/Widths` (and CID `/W`) from the embedded
  program's `hmtx`/charstrings so PDF metrics match glyph metrics.
- `Type1SubsetCharSet` 6.3.5 (3), `CIDSubsetCIDSet` 6.3.5 (4) — synthesize the `/CharSet` /
  `CIDSet` from the glyphs actually present in the embedded subset program.
- `SubsetGlyphCoverage` 6.3.5 (12) — where the program already contains every referenced
  glyph, this is a metadata/consistency fix; where glyphs are genuinely missing, defer to
  Phase 10 (re-subset/substitute).
**Assets:** none. **Infra:** CW-4 (font toolkit, **read** side) — extend `checks_font_program.go`.
**Risk:** medium — must parse multiple font formats correctly; widths are rendering-relevant
but we only make PDF metadata match the actual program (no glyph change).

### Phase 10 — Font embedding & substitution (family D'', **needs bundled fonts**)
**Goal:** `CIDNotEmbedded` 6.3.4, `CMapNotEmbedded` 6.3.3.3, and standard-14 `SimpleNotEmbedded`
(currently excused in `PDFA_1B`) — embed a metric-compatible face for fonts the input only
references; re-subset where glyphs are missing.
**Targets:** non-embedded simple/CID fonts; the standard-14 referenced without embedding.
**Assets:** `assets/fonts/*` (Liberation + Symbol/Dingbats substitutes, §3.2).
**Infra:** CW-4 (font toolkit, **write/subset** side) — TrueType/CFF subsetter, encoding→glyph
mapping, `/FontFile2`/`/FontFile3` emission, descriptor synthesis.
**Risk:** high — substitution changes exact glyph shapes/metrics; needs careful encoding
mapping. This is the largest single-feature effort. Also a `D'` sub-task: the rendering-affecting
TrueType encoding normalizations (6.3.7, 3 fixtures) — gate behind an explicit opt-in flag
because they can change glyph mapping (deferred from Phase 5 by project precedent).

### Phase 11 — Content-stream rewriter (family C, **needs content-stream writer**)
**Goal:** fix violations that live inside content bytes, the largest "hard" cluster.
**Targets:**
- `DeviceColourContentStream` 6.2.3.3 (10) — convert device colours not covered by the output
  intent (and the multi-model case `injectOutputIntent` can't cover with one profile): rewrite
  `rg/g/k`-family operators and inline-image colour, or inject `Default*` colour spaces. Needs
  the real CMYK profile from §3.1 for correct conversion.
- `UndefinedOperator` 6.2.10 (2), `RenderingIntent` `ri` 6.2.9 (2), `ImageInterpolate` inline
  (1), `InlineImageLZWFilter` (1) — drop/replace offending tokens.
- 6.1.12 limits inside content (`IntegerOutOfRange`, `StringTooLong`, `NameTooLong`,
  `CMapCIDOutOfRange`, `ArrayTooLarge`, `DictTooLarge`), and 6.1.6 `HexString*` — clamp/repair
  during re-tokenize+re-emit.
**Assets:** real CMYK ICC v2 (§3.1). **Infra:** CW-3 (content-stream writer), reuse the scanner.
**Risk:** high — re-emitting content must be byte-faithful except for the targeted change;
colour conversion is appearance-relevant.

### Phase 12 — Transparency flattening (family E)
**Goal:** `TransparencyGroup` 6.4 (2), `ImageWithSoftMask` 6.4 (1). Removing `/Group`/`/SMask`
is trivial but changes appearance; a faithful fix flattens the transparency.
**Assets:** none directly. **Infra:** CW-5 (rasterizer) for true flattening, or a limited
analytic flattener for simple cases.
**Risk:** high; small fixture count → low priority despite difficulty.

### Phase 13 — Rasterization escape hatch (completeness backstop)
**Goal:** guarantee *some* conformant output for any input by rendering the offending page(s)
to an image and rebuilding the page as an image XObject with a correct colour space — catching
whatever Phases 6–12 leave behind (exotic content, unflattenable transparency, unsubsettable
fonts, `InvalidProgram`).
**Assets:** none bundled, but an **external renderer** dependency (Ghostscript/pdfium) behind
the CW-5 interface; opt-in, since it's lossy (text becomes image, file grows).
**Risk:** high effort / external dep, but it is the only route to *literal* completeness.
**Excluded by nature:** `GraphResolutionFailure`/`XRefSubsectionHeader` where the graph can't
be resolved at all — no rewrite is possible without a parseable document (best handled by
improving parser recovery, not conversion).

---

## 6. Performance considerations

The pipeline today optimizes for correctness; before/with the heavier phases, address cost:

- **Verify round-trip is the dominant cost.** Each of up to 4 passes calls `verifyBytes`,
  which writes a temp file and re-`Open`s/re-parses the whole document. → **CW-1 in-memory
  verify**; ideally verify operates on the in-memory graph the loop already holds, eliminating
  serialize+reparse per pass. Expected: large constant-factor win on every conversion.
- **Per-pass graph walks scale with fixer count.** Each `Fixer.Fix` runs its own `walkDicts`.
  → **CW-2 single batched walk** per pass. With ~15+ family-A fixers this is the difference
  between O(graph) and O(graph × fixers) per pass.
- **Redundant colour scan.** `injectOutputIntent` runs `detectColourModelUsage`, a full
  content scan, on every `Convert` even when an adequate output intent already exists. Cache
  the decode (`decodeStreamCached` exists) and short-circuit when coverage is already valid.
- **Convergence / pass count.** Most docs converge in 1–2 passes; keep `maxConvertIterations`
  small and rely on `sameMultiset` early-exit. Batched dispatch (CW-2) also reduces passes by
  applying all applicable fixers before the next verify.
- **Font subsetting & content re-encoding are CPU-heavy (Phases 9–11).** Subset/rewrite each
  font/stream **once**, lazily, and cache by object; never re-subset across passes. Parse font
  programs once and memoize the parsed table.
- **Memory.** `ResolveGraph` holds the whole document in memory, plus the serialized buffer,
  plus (today) a re-parsed copy during verify. CW-1 removes the third copy. For very large
  files, consider streaming object emission in `WriteDocument` and not retaining decoded
  stream bytes longer than needed.
- **Parallelism.** Page-local operations (appearance synthesis, content rewriting, per-page
  rasterization) are independent and can be parallelized across pages once the fixers are
  page-scoped.
- **Asset cost.** Bundled fonts/ICC add to binary size; they are `//go:embed`-ed. Keep the
  embedded set minimal (subset/strip unneeded tables from bundled fonts where licensing
  permits) and load lazily.
- **Benchmarks.** The repo already has `benchmarks/`; add conversion benchmarks (small/large,
  font-heavy, colour-heavy) and guard regressions as the heavy phases land.

---

## 7. Definition of "complete" & the irreducible residual

"Complete PDF/A-1b conversion" is bounded:
- **Fully achievable by logic/metadata (Phases 6–9, 11-partial):** the structural, dictionary,
  metadata, appearance, font-metadata, and most content-stream violations — the bulk of the
  remaining 91 non-conformant fixtures (Phase 6 closed 33 of the original 124).
- **Achievable only with bundled assets (Phase 10) or external tools (Phase 13):** font
  substitution and rasterization. These change appearance and/or pull dependencies, so they
  should be **opt-in** with clear reporting via `ConvertResult.Residual()` / `ResidualCategory`.
- **Fundamentally unconvertible:** inputs whose object graph cannot be resolved
  (`GraphResolutionFailure`) — there is nothing to rewrite. These belong to parser-recovery
  work, not conversion, and should continue to degrade gracefully (return best-effort `Result`,
  never error), as `TestConvertDegradesGracefullyOnUnresolvableGraph` already asserts.

**Success metric per phase:** `minConvertedFully` rises and the `TestConvertCorpusEndToEnd`
"other residual" bucket shrinks toward only the opt-in (font-substitution/raster) and
unresolvable cases. Maintain the invariant that **no conformant input is ever made
non-conformant** (`TestConvertNeverBreaksConformantInput`) at every step.

---

## 8. Suggested sequencing summary

| Order | Phase | Family | New assets | New infra | Approx. fixtures |
|------:|-------|--------|-----------|-----------|-----------------:|
| ✅ done | 6 Dict expansion | A | — | (CW-2 still open) | 33 landed / ~40 targeted |
| 1 | 7 LZW re-encode | A' | — | LZW decoder | ~6 |
| 2 | 8 Appearance synth | B | — | CW-3 | ~15 |
| 3 | 9 Font metadata repair | D | — | CW-4 (read) | ~16+ |
| 4 | 11 Content rewriter | C | CMYK ICC | CW-3, CMYK | ~20 |
| 5 | 10 Font embed/subset | D'' | fonts | CW-4 (write) | ~5+ (opt-in) |
| 6 | 12 Transparency | E | — | CW-5 | ~3 |
| 7 | 13 Rasterization | E | ext. renderer | CW-5 | backstop |

Start each phase by generating a focused `/plan` using this section as the brief, the §2 table
to pick exact fixtures, and §3–4 to pull in the required assets/infra.
