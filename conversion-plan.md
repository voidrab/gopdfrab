# PDF/A-1b Conversion — Roadmap to Completeness

> **Purpose.** This document is the high-level roadmap for taking gopdfrab's PDF/A-1b
> *conversion* from its current state (435/510 corpus fixtures fully converted) to as close to
> *complete* as the format allows. Each phase below is scoped to be the input for a later,
> more specific implementation plan (`/plan`). It states the goal, the exact checks targeted,
> the approach, any external assets/tooling required, the test bar, and risk — but deliberately
> stops short of line-level design, which belongs in the per-phase plan.
>
> **How to read it.** Phases are ordered by value-per-unit-effort: cheap, asset-free,
> rendering-neutral wins first; font/content-stream/rasterization work (which needs new
> infrastructure and bundled assets) last. Phases 1–10 and Phase 11 (Stages A–D) are **done**;
> the rest is the roadmap.

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
| 7 | LZW stream re-encoding (family A') | `lzwStreamFixer` (`fixups_stream.go`) decodes `LZWDecode` streams (hand-rolled decoder, `lzw.go`) and marks them dirty for Flate re-encoding |
| 8 | Appearance-stream synthesis (family B) | `appearanceFixer` (`fixups_appearance.go`) rebuilds `/AP`; bundled Liberation Sans face for text widgets |
| 9 | Font metric & subset-metadata repair (family D, read) | `fontMetricFixer`/`fontSubsetMetaFixer` (`fixups_font_program.go`) recompute `/Widths`/`/W`/`/CharSet`/`/CIDSet` from the embedded program |
| 11 A | Real CMYK profile + `Default*` colour-space injection | `deviceColourFixer` (`fixups_content_colour.go`); embedded FOGRA39 v2 profile (`fixups_colour.go`) |
| 11 B | Content-stream rewriter + lossless inline-image round-trip | `contentLimitsFixer` (`fixups_content.go`); `inlineImageRaw` (`content.go`/`content_writer.go`) |
| 11 C | Inline-image fixes | `imageMetadataFixer`/`contentLimitsFixer` (`ImageInterpolate`, inline `/Intent`); `inlineImageLZWFixer` (`fixups_inline_image.go`) |
| 10 | TrueType subsetter + font substitution/re-embed (family D'') | `subsetTrueType`/`subsetTrueTypeForCID`/`trimTrueTypeCmapToSingleSubtable` (`fonttool_subset.go`); `fontSubstitutionFixer`/`trueTypeEncodingFixer` (`fixups_font_subst.go`) |
| 11 D | Remaining 6.1.12 limits (page-tree rebalance, resource pruning, name fixes, CMap CID clamp) | `pagesTreeArrayFixer`/`resourceDictPruneFixer`/`nameTooLongFixer`/`cmapCIDClampFixer` (`fixups_limits.go`) |

**Regression floor:** `minConvertedFully = 496` of 510 (`convert_test.go`). Latest corpus
sweep: **496 fully conformant · 10 known-hard residual · 4 other residual · 0 errored**.

Key reusable infrastructure already present:
- `walkDicts` (graph walk with cycle protection) — `fixups_dict.go`
- `walkStreamDicts` (graph walk that writes mutated streams back to the parent) — `fixups_stream.go`
- `walkScalars` (graph walk over dict/array scalar values, the leaf-level counterpart to `walkDicts`) — `fixups_content.go`
- `newContentScanner(...).scan(...)` (content-stream **reader**/tokenizer, now round-trips inline images via `inlineImageRaw`) — `content.go`
- `writeContentStream`/`contentOp` (content-stream **writer**, CW-3, landed Phase 8, inline-image support landed Phase 11B) — `content_writer.go`, sharing scalar serialization with `writer.go` via `writeScalar`/`writeOperand`
- `appearanceFont()` — bundled, embedded, conformant Liberation Sans simple TrueType font for synthesized appearance text — `fixups_appearance_font.go`
- Font-program parsers for TrueType/CFF/Type1 (glyph coverage, widths, cmap) — `checks_font_program.go` (~1300 lines, used for both validation and Phase 9's repair fixers)
- TrueType subsetter/sfnt repacker (CW-4 write side: glyf/loca/hmtx/cmap emission, checksums) — `fonttool_subset.go`, landed Phase 10
- `Fixer` registry + pre-emptive fixup registry — `convert_fixers.go`
- `ResidualCategory` — classifies a leftover check as font/content-stream/transparency-hard — `residual.go`
- `decodeStream` / `decodeStreamCached`, predictor & filter handling — `stream.go`, `predictor.go`
- Bundled ICC profiles: `assets/profiles/sRGB2014.icc` (v2 RGB), `assets/profiles/Small-footprint_FOGRA39v2.icc` (v2 CMYK) — `fixups_colour.go`. `fogra39.icc` is ICC v4 and unusable for PDF/A-1 (`validateICCProfileStream` rejects `major > 2`); left unembedded.

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
under their own (no longer family-**A**) root cause. (Phase 7 has since closed the
`StreamLZWFilter` row below — table otherwise left as the pre-Phase-7 snapshot; re-measure at
the start of Phase 8.)

| Check | Clause | FIX | HARD | Fix family |
|-------|--------|-----|------|-----------|
| SubsetGlyphCoverage | 6.3.5 | 12 | yes | **D** font subset |
| DeviceColourContentStream | 6.2.3.3 | 10 | no | **C** content-stream colour |
| AdvanceWidthMismatch | 6.3.6 | 9 | yes | **D** font metrics |
| IntegerOutOfRange | 6.1.12 | 7 | yes | **C** content-stream / **A** structure |
| ~~AppearanceNNotStream~~ | 6.5.3 | ~~6~~ | no | **✅ done, Phase 8** |
| ~~WidgetMissingAppearance~~ | 6.9 | ~~5~~ | no | **✅ done, Phase 8** |
| ~~StreamLZWFilter~~ | 6.1.10 | ~~5~~ | no | **✅ done, Phase 7** |
| StringTooLong | 6.1.12 | 5 | yes | **C** content-stream |
| CIDSubsetCIDSet | 6.3.5 | 4 | yes | **D** font subset meta |
| Type1SubsetCharSet | 6.3.5 | 3 | yes | **D** font subset meta |
| InvalidProgram | 6.3.2 | 3 | yes | **D** font re-embed |
| UndefinedOperator | 6.2.10 | 2 | yes | **C** content-stream |
| HexStringOddLength | 6.1.6 | 2 | no | **C** content-stream |
| RenderingIntent | 6.2.9 | 2 | no | **C** content-stream (`ri`) |
| ~~AppearanceMissingN / AppearanceExtraEntries~~ | 6.5.3 | ~~2/2~~ | no | **✅ done, Phase 8** |
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

### Phase 7 — Object-stream filter re-encoding (family A') — ✅ **done**
Landed: a hand-rolled PDF LZW decoder (`lzw.go` — stdlib `compress/lzw` targets GIF's
LSB packing and doesn't correctly decode PDF/TIFF's MSB + early-change variant, confirmed
against a real Isartor fixture before settling on a custom decoder) and `lzwStreamFixer`
(`fixups_stream.go`), which decodes any stream whose filter chain includes `LZWDecode`
(undoing TIFF/PNG predictors via the existing `predictor.go` helpers) and marks it dirty so
the writer re-emits it Flate-encoded. Needed a parent-aware graph walk (`walkStreamDicts`)
rather than `walkDicts`, since `RawStream` is a value field `walkDicts`' by-value callback
cannot persist back to the shared graph. 5 fixtures moved to fully conformant (419 → 424).
**Note:** inline-image LZW (`InlineImageLZWFilter`) lives in content bytes → Phase 11.

### Phase 8 — Appearance-stream synthesis (family B) — ✅ **done**
Landed (full-fidelity, not the minimal empty-AP version originally scoped here): a new
content-stream writer (`content_writer.go`, CW-3 — `writeContentStream`/`contentOp`, sharing
scalar serialization with `writer.go` via the extracted `writeScalar`/`writeOperand` helpers) and
`appearanceFixer` (`fixups_appearance.go`), which rebuilds `/AP` as `<< /N <value> >>` for any
annotation/widget violating `WidgetMissingAppearance` (6.9), `MissingAppearance`,
`AppearanceMissingN`, `AppearanceExtraEntries`, or `AppearanceNNotStream` (6.5.3) — preserving an
already-valid `/N` value where one exists (e.g. when the only fault was an extra `/D`/`/R` key)
rather than discarding it. Text/choice field widgets render their current `/V` as a single line
of text using a bundled, embedded Liberation Sans face (`fixups_appearance_font.go` —
`assets/fonts/LiberationSans-Regular.ttf`, SIL OFL, pulled forward from Phase 10's asset list);
button widgets get a state-name-to-stream subdictionary; everything else gets a structurally
valid empty Form XObject. The embedded font is deliberately **not** subset-tagged (so
`SubsetGlyphCoverage` 6.3.5 never applies to it) and its `/Widths` are built directly from the
font's own `hmtx` table (so `AdvanceWidthMismatch` 6.3.6 cannot fire — AP-only fonts get no
content-usage narrowing from the verifier, so every `Widths` entry is checked). 11 fixtures moved
to fully conformant (424 → 435).

### Phase 9 — Font metric & subset-metadata repair from the embedded program (family D, no new fonts) — ✅ **done**
Landed: `fontMetricFixer` and `fontSubsetMetaFixer` (`fixups_font_program.go`), recomputing
`/Widths`/CID `/W` from the embedded program's `hmtx`/Type1 charstrings (`AdvanceWidthMismatch`
6.3.6) and synthesizing `/CharSet`/`CIDSet` from the glyphs actually present (`Type1SubsetCharSet`,
`CIDSubsetCIDSet` 6.3.5). `SubsetGlyphCoverage` remains detection-only by design (a genuinely
missing glyph needs re-subsetting/substitution, Phase 10) — still the largest single residual
bucket (12 fixtures). No CFF/Type1C charstring advance-width reader exists, so CIDFontType0
`AdvanceWidthMismatch` is neither checked nor fixed; a small open gap, not yet hit by the corpus.
Floor raised 424 → 449.

### Phase 10 — Font embedding & substitution (family D'', **needs bundled fonts**) — ✅ **done**
Landed: a TrueType subsetter/sfnt repacker (`fonttool_subset.go` — `subsetTrueType`,
`subsetTrueTypeForCID`, `trimTrueTypeCmapToSingleSubtable`, the shared `buildSubsetTables`/
`packSfnt` table emission — CW-4's write side) plus `fontSubstitutionFixer` and
`trueTypeEncodingFixer` (`fixups_font_subst.go`), registered always-on (no opt-in flag, per
project decision this phase). `fontSubstitutionFixer` clears `SubsetGlyphCoverage`,
`SimpleNotEmbedded`, `CIDNotEmbedded` and `InvalidProgram` by substituting a subsetted bundled
Liberation face (Sans/Serif/Mono × regular/bold/italic/bold-italic, chosen from descriptor flags/
BaseFont) wherever a font's own program is missing, damaged, or doesn't cover a glyph it needs —
reusing the existing detection functions (`validateFontProgram`/`validateSimpleTrueTypeSubset`/
`validateType1SubsetCoverage`/`validateCIDTrueTypeSubset`/`validateCIDCFFSubset`) against a
throwaway `ValidationContext` so it only ever replaces what was genuinely flagged. Composite-font
substitution (`substituteCIDFont`) rebuilds the descendant as a `CIDFontType2` with
`/CIDToGIDMap /Identity` and the substitute glyph for each used CID placed at that *exact* glyph
ID (`subsetTrueTypeForCID`) — required because the CID TrueType checks
(`validateCIDTrueTypeSubset`/`Metrics`) look up a CID as a glyph ID directly with no
`/CIDToGIDMap` indirection, so only a CID==GID substitute passes them. CID->Unicode recovery uses
the font's own `/ToUnicode` CMap (`parseToUnicodeCMap`), so substitution only fires when
`/Encoding` is `Identity-H`/`Identity-V` and `/ToUnicode` is present and parseable — a CJK CID-keyed
font with neither (no bundled Adobe CMap resources to fall back on) is left residual, same as
`CMapNotEmbedded` (a non-Identity, non-embedded named CMap), which is deliberately not claimed at
all. `trueTypeEncodingFixer` clears the 6.3.7 trio: dropping a stray `/Encoding` from a symbolic
font, trimming a symbolic font's cmap to the single subtable the spec requires
(`trimTrueTypeCmapToSingleSubtable`, glyf/loca/hmtx untouched) and setting a non-symbolic font's
`/Encoding` to `WinAnsiEncoding` when neither permitted encoding is named. 13 fixtures moved to
fully conformant. Floor 478 → 491.
**Known limitation:** standard-14 `SimpleNotEmbedded` is claimed for completeness but inert under
the default `PDFA_1B` profile, which already excuses it (`profile.go`). No CFF subsetter exists —
every substitution target is a bundled TrueType face, so nothing needs one.

### Phase 11 — Content-stream rewriter (family C, **needs content-stream writer**) — **Stages A+B done**
**Goal:** fix violations that live inside content bytes, the largest "hard" cluster.

**Stage A — done.** A real CMYK ICC v2 profile (`assets/profiles/Small-footprint_FOGRA39v2.icc`,
FOGRA39, `prtr`/`CMYK`) is now embedded and used by `injectOutputIntent` for CMYK-dominant
documents, replacing the old `withICCColorSpace` sRGB-with-patched-signature placeholder
(`fixups_colour.go`). `deviceColourFixer` (`fixups_content_colour.go`) clears the multi-model
case `injectOutputIntent` can't cover with one profile: it scans each page's content (+ Form
XObjects/tiling patterns it invokes) and resource-level Image/Shading colour spaces for device
models not covered by the document's OutputIntent, and injects a `/DefaultRGB`/`DefaultCMYK`
ICCBased colour space into that page's `/Resources/ColorSpace` — `defaultColorSpaceDefined`
(checks_colour.go) excuses a covered model on presence alone, so no per-pixel colour conversion
is needed. Clears `DeviceColourContentStream` 6.2.3.3 (10) and `DeviceColourSpaceUsage` (present
in the check catalog, not hit by the corpus). Floor 449 → 459.

**Stage B — done.** `writeContentStream`/`newContentScanner` now round-trip inline images
losslessly: `scanInlineImage` captures the verbatim `BI...EI` byte span (via `inlineImageRaw`,
content.go) instead of discarding it, and `writeContentStream` re-emits it verbatim
(content_writer.go) — closing CW-3's one remaining gap. `contentLimitsFixer`
(`fixups_content.go`) rewrites content streams (Page/Form/tiling-Pattern/Type3 CharProcs) to
drop `UndefinedOperator` 6.2.10 (2) and replace a non-standard `ri` `RenderingIntent` 6.2.9 (2)
with `/RelativeColorimetric`, and clamps/repairs the 6.1.12/6.1.6 operand limits
(`IntegerOutOfRange`, `StringTooLong`, `HexStringOddLength`, `HexStringInvalidChar`) wherever they
occur — both inside content-stream operands and as plain dictionary/array values elsewhere in the
graph (one `Fixer` per `Check`, so both sources of the same check need the same fixer; see
`walkScalars`, the dict/array-element counterpart to `walkDicts`). Floor 459 → 475.
**Known limitation, by design:** the q/Q-nesting-depth flavour of `StringTooLong` is a structural
defect (too many nested `q`/`Q`), not a clampable operand — left for the rasterization backstop
(Phase 13); `TestConvertClearsRegisteredFixerChecks` excuses it explicitly, the same way it
already excuses inline-image-sourced residuals.

**Stage C — done.** Inline-image-specific fixes, in new `fixups_inline_image.go`. The scanner
now also captures an inline image's data bytes alone (`inlineImageRaw.Data`, distinct from the
verbatim `Bytes` span Stage B added), and `buildInlineImageBytes` (content_writer.go) rebuilds a
fresh `BI...EI` span from edited params + data — used only when something is actually fixed; an
untouched inline image still round-trips via its captured `Bytes` unchanged.
`fixInlineImageInterpolate` flips a true inline `/I`/`/Interpolate` to `false`, folded into
*`imageMetadataFixer`* (`fixups_dict.go`) since it already owns `ImageInterpolate` for the
dict-level Image-XObject case (one `Fixer` per `Check`). The inline `/Intent` flavour of
`RenderingIntent` is folded into *`contentLimitsFixer`* the same way (it already owns that
`Check` and already walks every `INLINEIMAGE` op). `inlineImageLZWFixer` is a new, separately
registered `Fixer` for `InlineImageLZWFilter` (unclaimed until now): it decodes the inline image's
LZW data (`decodeLZW`, `lzw.go`) and re-encodes as Flate (`deflateZlib`, `writer.go`), updating
`/F`/`/Filter`; it bails out (leaves the violation as residual) if a `/DP`/`/DecodeParms`
predictor is present, since no inline-image-aware predictor-undo exists — `ResidualCategory`
keeps `InlineImageLZWFilter` classified as content-stream-hard for that reason, mirroring
`StringTooLong`'s q/Q caveat. `walkContentStreams`/`contentOpRewriter` (`fixups_content.go`) were
extracted from Stage B's `contentLimitsFixer` into shared, reusable helpers for this purpose.
3 fixtures moved to fully conformant (`ImageInterpolate`, `InlineImageLZWFilter`, and the inline
`/Intent` `RenderingIntent` case). Floor 475 → 478.

**Stage D — done.** The remaining 6.1.12 limit checks Stage B/C left out of scope, in new
`fixups_limits.go`, each needing a different repair shape rather than a single scalar clamp:
- `ArrayTooLarge` (1) — the one corpus instance is a `/Pages` node's `/Kids` array with 10000
  flat leaf-page entries. `pagesTreeArrayFixer` splits it into a tree of new intermediate
  `/Pages` nodes (≤4096 kids each, re-pointing each kid's `/Parent`) — the standard technique
  real PDF writers use for huge page counts; `buildPageIndex` (document.go) and any other `/Kids`
  walker already recurse through arbitrary depth, so page count/order/content never changes.
  New intermediate nodes need a real indirect-object identity (so `/Parent` can reference them
  as required by PDF 32000-1 7.7.3.2) despite never having been read from disk; `nextAvailableObjNum`
  synthesizes a collision-free `_ref` by scanning the graph for the highest existing object number.
- `DictTooLarge` (1) — the one corpus instance is a `/Resources/ExtGState` sub-dictionary with
  4100 entries, none referenced by the page's content. `resourceDictPruneFixer` deletes entries a
  new resource-usage scanner (`computeResourceUsage`, walking `Do`/`Tf`/`gs`/`cs`/`CS`/`scn`/`SCN`/
  `sh`/`BDC`/`DP` across pages and invoked Form XObjects) confirms nothing references, down to the
  4096-entry limit — safe by construction; left residual if pruning every unused entry still isn't
  enough.
- `NameTooLong` (2) — `nameTooLongFixer` handles both flavours: an overlong name *value* is
  truncated in place via `walkScalars` (mirroring `contentLimitsFixer`'s other scalar clamps); an
  overlong dictionary *key* (the corpus case: a `/Resources/ColorSpace` key referenced by the
  page's content via `CS`/`cs`) is renamed to a short, collision-free replacement, with every
  content-stream operator selecting the old name rewritten to the new one via a new
  `walkResourceAwareContent` — a Resources-aware sibling of `walkContentStreams` that threads the
  in-effect `/Resources` dict through Page→Form `Do` recursion, since renaming without resyncing
  references would silently break the page's appearance.
- `CMapCIDOutOfRange` (2) — `cmapCIDClampFixer` clamps any cidrange/cidchar CID value over 65535
  down to 65535 directly within a CMap stream's PostScript bytes, splicing the replacement via
  token byte-offsets (`cmapTokenize` extended to record them) rather than re-serializing the
  whole stream — mirrors `checkCMapCIDLimits`' own token-position state machine exactly so it only
  ever touches what that check would flag.
- `DeviceNColorants` (1) — **not attempted, confirmed infeasible by inspection**: the one corpus
  fixture's `DeviceN` array lists 12 colorants, 3 of which are the spec's `/None` placeholder (no
  visual effect) — but the remaining 9 *real* colorants still exceed the 8-colorant maximum, and
  reducing them further would require rewriting the tint-transform function's input arity to
  match. `ResidualCategory` now classifies it explicitly rather than leaving it unclassified.

5 fixtures moved to fully conformant (the sixth, `CMapCIDOutOfRange`'s other instance, shares a
fixture with the unrelated, separately-residual CJK `SubsetGlyphCoverage` case — `CMapCIDOutOfRange`
itself is still cleared from it). Floor 491 → 496.

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
  substitution and rasterization. Font substitution changes glyph shapes but always runs (project
  decision, Phase 10) since a substituted-but-conformant font beats a non-conformant one;
  rasterization is more invasive (text becomes image) and should be **opt-in**. Either way, clear
  reporting via `ConvertResult.Residual()` / `ResidualCategory` covers what's left.
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
| ✅ done | 7 LZW re-encode | A' | — | LZW decoder | 5 landed |
| ✅ done | 8 Appearance synth | B | LiberationSans-Regular.ttf (pulled forward) | CW-3 (landed) | 11 landed / ~15 targeted |
| ✅ done | 9 Font metadata repair | D | — | CW-4 (read) | 14 landed (449 → 449; floor moved with Phase 11A/B below) |
| ✅ done | 11A CMYK + Default* colour | C | FOGRA39 v2 ICC | reuses CW-3 | 10 landed |
| ✅ done | 11B Content rewriter + inline-image round-trip | C | — | CW-3 inline-image support | 16 landed |
| ✅ done | 11C Inline-image fixes (ImageInterpolate, InlineImageLZWFilter, inline /Intent) | C | — | extracted `walkContentStreams`/`contentOpRewriter` | 3 landed |
| ✅ done | 10 Font embed/subset | D'' | fonts (already bundled, §3.2) | CW-4 (write): TrueType subsetter | 13 landed |
| ✅ done | 11D Remaining 6.1.12 limits | A/C | — | resource-usage scanner, Resources-aware content walk | 5 landed |
| next | 12 Transparency | E | — | CW-5 | ~3 |
| next | 13 Rasterization | E | ext. renderer | CW-5 | backstop |

Floor history: 424 (Phase 7) → 435 (Phase 8) → 449 (Phase 9) → 459 (Phase 11A) → 475 (Phase 11B)
→ 478 (Phase 11C) → 491 (Phase 10) → 496 (Phase 11D), of 510 total corpus fixtures.

Start each phase by generating a focused `/plan` using this section as the brief, the §2 table
to pick exact fixtures, and §3–4 to pull in the required assets/infra.
