# Roadmap: PDF/A-2b, then PDF/A-3b

Goal: verify and convert PDF/A-2b with the same standard as PDF/A-1b today —
the veraPDF corpus fully green and clause-mapped, a veraPDF-binary differential
check in CI, and conversion that keeps more of the input than the 1b converter
can, because Part 2 permits what Part 1 forced it to destroy. PDF/A-3b follows
as a small delta on 2b. PDF/A-2u is an inexpensive add-on along the way.

Out of scope: level A (2a/3a — 1a was never built either) and PDF/A-4
(different base: PDF 2.0, 111 veraPDF rules, its own work).

Baseline: `release/1.2.0` at 8ce72dd (2026-10-11). Every file:line below is
against that commit.

---

## Where things stand

### The verifier

- **One level.** `LevelType` has `A1B`, `ObjectModel` and `Undefined`
  (`internal/pdf/profile.go:9-19`). Dispatch is a single gate,
  `p.Level == pdf.A1B || p.Level == pdf.ObjectModel`, at
  `internal/verify/verifier.go:46`, `:104` and `:124`.
  - **Unknown levels pass silently.** Any other level returns `Valid: true`
    with no issues. This has to become an error before A2B exists.
- **A check is tied to its Part 1 clause.** Each of the 159 checks carries one
  Part 1 clause string (`internal/pdf/checks_catalog.go:15`). That string is
  identity as well as display:
  - `(clause, subclause)` is the registration key (`catalogByPair`);
  - it is the profile filter (`Profile.Allows`, `filterByProfile` at
    `verifier.go:228`);
  - it is the JSON identity (`internal/pdf/json.go`), the result sort key and
    the CLI grouping key.

  Part 2 renumbers most of the standard. Fonts move from 6.3 to 6.2.11,
  annotations from 6.5 to 6.3, metadata from 6.7 to 6.6, and forms from 6.9
  to 6.4.
- **`Check` is a comparable struct used as a map key.** It keys
  `Result.IssuesByCheck` in the public API and `fixerRegistry` in convert. It
  must stay comparable.
- **`NewFullProfile(level)` enables every registered check**
  (`profile.go:102`). Adding a Part 2-only check would silently switch it on in
  `PDFA1B` and `Legacy1B`.
- **The rules have no level to read.** `ValidationContext` has no level field,
  so every part-specific rule is a package global or a literal:
  - **Limits:** `verifier.go:1464-1500`, `checks_content.go:151-165`, DeviceN
    `checks_colour.go:158`.
  - **ICC:** `major >= 3` at `verifier.go:1706` and `:1757`.
  - **Exported tables:** `ForbiddenActions`, `AllowedAnnotationTypes`,
    `IsAllowedBlendMode`, `Post14ViewerPrefKeys` (`checks_dict.go`).
  - **pdfaid:** `part != "1"` and `conformance ∈ {A, B}` at
    `checks_xmp.go:982` and `:989`.
- **Several Part 1 bans live outside the catalogue.** The Arlington object
  model is compiled from the PDF 1.4 TSVs only (`internal/arlington/gen.go:21-28`,
  `modelVersion = "1.4"`; `model_gen.go`, ~18k lines). Because of that, these
  bans fall out of the model as `DisallowedValue`/`WrongValueType`:
  - the JPXDecode filter
  - the Crypt filter
  - the ObjStm and XRef types

  There is no explicit JPX check. Upstream Arlington ships a `tsv/1.7` set.
- **Xref and object streams already parse** (`xrefstream.go`, `objstm.go`,
  `document.go:631-650`). Today they are only *reported*: `XRefStream` is a
  parse diagnostic. Separately, `checkXRefSectionFormat` (`verifier.go:561`)
  expects the literal `xref` keyword at the xref offset. A clean xref-stream
  file therefore also reports `XRefKeyword`.

### The converter

- **The fix loop only repairs what the verifier reports**
  (`internal/convert/convert.go:436`, `inHeapVerify` at `:630`). No 2b
  conversion is possible until an A2B verifier exists.
- **Neither the fixers nor the pre-emptive fixups ever see the level.**
  - The profile reaches only `RunContext`, `inHeapVerify`, `rasterBackstop`,
    `serializeAndVerify` and one `p.Allows` call (`convert.go:544`).
  - Pre-emptive fixups have the signature `func(trailer, doc)` and always run
    (`convert_fixers.go:84`, called at `convert.go:405`).
- **Some fixers repair every dimension at once.** Five registered fixers are
  single visitors that fix everything in a dict, whichever check fired:
  - `extGStateFixer` (`fixups_dict.go:247-306`);
  - `annotationFlagsFixer`, `actionFixer` and `formFixer` (all in
    `fixups_dict.go`);
  - `fileSpecFixer` (`fixups_filespec.go:27`).

  Under a Part 2 target, a flagged non-standard `/BM` would also reset a legal
  `/SMask`, `/CA` and `/ca`.
- **The writer emits fixed Part 1 output.** It writes `%PDF-1.4` with a
  classic xref (`internal/writer/writer.go:54`). The XMP fixup writes
  `pdfaid:part=1` and regenerates XMP from scratch (`fixups_xmp.go:251`).
- **The raster fallback's own output is already valid PDF/A-2:** opaque
  DeviceRGB plus an optional 1-bit stencil. The renderer, though, ignores:
  - `/BM`, ExtGState `/SMask`, group isolation/knockout and optional-content
    visibility;
  - JPX and JBIG2 images, which it paints as flat gray (`raster/image.go:42`).

  `raster.go:617` documents its assumption that the SMask "is already
  neutralized by extGStateFixer". That holds for 1b only.

### Existing 1b defects the investigation turned up

All of these should be fixed in 1b first; see item 6.

1. **A JPX image is silently lost.**
   - The 1.4 Arlington enum flags `/Filter /JPXDecode` as `DisallowedValue`,
     and no fixer can repair it.
   - The page is rasterized and the image painted as a gray placeholder.
   - `RasterDrops` and `LostObjects` both stay empty.
   - Confirmed with a probe.
2. **`ConvertObjectModel` applies the PDF/A pre-emptive fixups.** It writes
   `pdfaid` XMP and injects an OutputIntent, because pre-emptives ignore the
   profile (`convert.go:405`; `gopdfrab.go:344`). Confirmed with a probe.
   Confirm whether that was ever intended.
3. **`fileSpecFixer` deletes attachments without reporting them.** It removes
   `/EF` and `/EmbeddedFiles`, and nothing reaches `LostObjects`
   (`fixups_filespec.go:27-46`).
4. **`optionalContentFixer` changes what the page shows.** It deletes
   `/OCProperties`, which un-hides layers that were off by default
   (`fixups_dict.go:597-610`).
5. **An unknown `LevelType` verifies as valid** (see above).

### Test oracles

- **veraPDF corpus** (`staging` branch, CC BY 4.0, same licence as the
  vendored 1b part). Every filename matches the existing `veraPDFNameRe`, and
  no file is shared between profiles.

  | Profile | Pass | Fail | Size | Notes |
  |---|---|---|---|---|
  | 1b (vendored today) | 263 | 306 | 8.9 MB | |
  | 2b | 377 | 609 | 23 MB | 578 of the 986 are 6.6.2 XMP tests; 6.2 Graphics is 70% of the bytes |
  | 2u | 12 | 10 | — | |
  | 3b | 7 | 5 | — | all clause 6.8 |
- **veraPDF binary.** The installed 1.30.2 at `benchmarks/tools/verapdf/verapdf`
  accepts `--flavour 2b|2u|3b|3u`.
  - `runVeraPDF` hard-codes `--flavour 1b` (`convert_test.go:481`).
  - `differentialDeviations` is keyed by corpus-relative path, so 2b and 1b
    names collide.
  - `scripts/install-verapdf.sh` installs whatever is latest (unpinned).
- **BFO.** The jar in `../bfo-test` exposes `OutputProfile.PDFA2b`, `PDFA2u`
  and `PDFA3b`. `Main.java:26` hard-codes `PDFA1b_2005`. The BFO
  `pdfa-testsuite` has 34 files (33 PDF/A-2, CC BY 3.0). It is a second
  opinion, too small to be the primary oracle.
- **Isartor is Part 1 only.** No other broad public PDF/A-2/3 suite exists.
  ZUGFeRD/Factur-X invoices are natural 3b positives; their licences are
  unchecked.

---

## What Part 2 changes, at rule level

Source: veraPDF's aggregated profiles `PDF_A/PDFA-{1B,2B,2U,3B}.xml`
(veraPDF-validation-profiles, `integration`). Rules were matched by semantics,
not clause number.

| | 1b | 2b | 2u | 3b |
|---|---|---|---|---|
| veraPDF rules | 129 | 144 | 146 | 146 |

**2b against 1b:**

| Classification | Rules |
|---|---|
| Same rule, usually renumbered | 74 |
| Same object, changed test | 27 |
| New | 43 |
| 1b rules with no 2b counterpart | 26 |

**Relaxed: the 1b rules that disappear**

- Xref streams (1b 6.1.4-3) and the xref subsection-header spacing rule
  (6.1.4-1).
- Linearized first-page ID matching (6.1.3-4).
- The ban on embedded files (6.1.11-1 and -2). It is replaced by 6.8.
- The ban on optional content (6.1.13-1). It is replaced by 6.9.
- The array (8191) and dictionary (4095) capacity limits.
- The whole transparency ban (6.4-1 to -6): ExtGState SMask, XObject SMask,
  transparency groups, blend modes beyond Normal/Compatible, and CA/ca below 1.
- Annotation `/CA` must be 1.0, and the annotation C/IC colour rule.
- ICC `/N` must match the profile's colour space (6.2.3.2-2).
- The rule that the Catalog Metadata stream carries no `/Filter`.
- All eight Info↔XMP synchronisation rules (6.7.3-1 to -8).
- The form-field appearance rule (1b 6.9-2), which the broader 6.3.3-1 absorbs.

**Changed: the same rule with a different test**

- **Header:** strict `^%PDF-1\.[0-7]$`. 1b accepts any `%PDF-d.d`.
- **Trailer `/ID`:** always required and non-empty, with no linearization
  alternative.
- **Stream filters:** the LZW ban becomes a whitelist. JPXDecode is added,
  Crypt is allowed only with `/Name /Identity`, and unknown filters are
  banned.
- **Inline image filters:** become a whitelist that excludes Crypt, JBIG2 and
  JPX.
- **Limits:**
  - Reals now use the IEEE single range: ±3.403e38, where 1b capped them at
    ±32767.
  - Strings are tighter: at most 32767 bytes, where 1b allowed 65535.
  - DeviceN allows 32 colourants instead of 8.
- **ICC versions:** OutputIntent and ICCBased profiles may be up to v4.x
  (`< 5.0`); 1b requires `< 3.0`.
- **Images:** `BitsPerComponent` may also be 16. OPI is split between image
  and form.
- **Fonts:**
  - FontFile3 `/OpenType` is allowed.
  - Type 0 fonts get a CIDSystemInfo Supplement comparison.
  - `CIDToGIDMap` is required only when the font is embedded.
  - The predefined CMaps of ISO 32000-1 Table 118 are allowed without
    embedding.
  - Type 1 `CharSet` and CID `CIDSet` are checked only when present.
  - TrueType `Differences` are allowed when every name maps to Unicode.
  - A symbolic font's cmap may be exactly one subtable or include (3,0).
- **Annotations:**
  - The allowed set adds Polygon, PolyLine, Caret, FileAttachment, Watermark
    and Redact. 3D, Sound, Screen and Movie stay banned.
  - The `/F` flag test now also requires LockedContents (bit 256) to be clear.
- **Actions:** the forbidden set adds Hide, SetOCGState, Rendition, Trans and
  GoTo3DView. GoToE is allowed.
- **Additional actions:** `/AA` is banned only on the catalog, pages, widgets
  and fields. 1b bans it everywhere.
- **XMP:** properties are checked against the 2005 predefined schemas, not
  the 2004 ones. `pdfaid:part` must be 2, and `pdfaid:conformance` may be B,
  U or A.

**New: no 1b equivalent.** The full list is in Appendix C.

| Area | Rule IDs | What they require |
|---|---|---|
| File structure | 6.1.8-1, 6.1.12-1/2, 6.1.13-5, 6.1.13-11 | UTF-8 names. A permissions dict with only UR3/DocMDP, plus SigRef digest keys. No tiny reals. Page boxes 3–14400 units. |
| Content | 6.2.2-2 (deferred) | Explicit resources: no inheritance into a content stream. |
| Colour | 6.2.3-3, 6.2.4.2-2, 6.2.4.4-1/2 | No PDF/X `DestOutputProfileRef`. No OPM=1 with ICCBased CMYK when overprinting. A DeviceN Colorants dict. Separation consistency across the whole file. |
| ExtGState | 6.2.5-3 to -6 | Halftone rules and no HTP. |
| Images | 6.2.8.3-1 to -5 | JPEG 2000 box rules. |
| Transparency | 6.2.10-1/2 | Blend-mode whitelist. A page Group `/CS` when there is transparency and no OutputIntent. |
| Fonts | 6.2.11.3.3-3, 6.2.11.6-1, 6.2.11.8-1 | A CMap may reference only Table 118 CMaps. The non-symbolic TrueType cmap rule. No `.notdef` references. |
| Annotations | 6.3.2-1, 6.3.3-1 | `/F` required. An appearance on every annotation except Popup, Link and zero-size ones. |
| Forms, signatures, actions | 6.4.2-1/2, 6.4.3-1 to -3, 6.5.2-2 | No XFA and no NeedsRendering. Signature rules. No page `/AA`. |
| Metadata | 6.6.2.1-5, 6.6.4-7 | UTF-8 XMP. The `corr` prefix rule. |
| Embedded files | 6.8-2, 6.8-5 | F and UF keys. The embedded file must itself be PDF/A-1 or PDF/A-2. |
| Other | 6.9-1 to -4, 6.10-1/2, 6.11-1 | Optional content config rules. No alternate presentations, PresSteps or Requirements. |

**2u = 2b + 2 rules.**

- **6.2.11.7.2-1:** every used code must map to Unicode.
- **6.2.11.7.2-2:** no U+0000, U+FEFF or U+FFFE in ToUnicode.
- **Narrowed rule:** 6.6.4-3 accepts conformance `U` or `A` only.

**3b = 2b with a different embedded-file regime.**

- **Dropped:** 6.8-5, the recursive PDF/A check.
- **Added:**
  - 6.8-1: the EmbeddedFile `/Subtype` must be a MIME type.
  - 6.8-3: `AFRelationship` must be present. veraPDF's test applies to every
    file specification it instantiates.
  - 6.8-4 (deferred): the file spec must be reachable from an `/AF` array.
- **Changed:** `pdfaid:part` must be 3.
- Everything else is identical.

---

## Design decisions to settle before writing code

Each item below is a recommendation. The ones marked **(decide)** change
public behaviour or project policy and need an explicit call.

### D1. Check identity across parts

- **One catalogue.** A rule that exists in both parts stays one `Check` value.
  Fixers, `IssuesByCheck` and the ~600 `Checks.X.Y` test references keep
  working.
- **Applicability bitmask.** Give `Check` an unexported scalar `parts`
  bitmask; a scalar keeps the struct comparable.
- **Per-part clauses.** Keep each part's clause and subclause in an unexported
  side table keyed by `Check.id`.
- **Subclause numbering.** For Part 2 the subclause is the veraPDF test
  number, so corpus filenames map one to one. Part 1's subclauses stay the
  internal numbering they are today.
- **New methods, all additions:**
  - `Check.ClauseIn(level) (clause string, sub int, ok bool)`
  - `Check.AppliesTo(level) bool`
  - `CheckByClauseIn(level, clause, sub)`
  - `ChecksForClauseIn(level, clause)`
- **Existing methods keep their meaning.** `Clause()`, `Subclause()`,
  `CheckByClause`, `ChecksForClause` and `Profile.Allows` stay documented
  Part 1 lookups, because the CHANGELOG stability policy covers them.
- **The catalogue key gains the part.** `catalogByPair` becomes
  `part/clause/sub`.
- **Rejected alternatives:**
  - a second catalogue (`Checks2`): doubles maintenance and the fixer
    registry;
  - a contextual `Clause()`: process-global state;
  - `Check` as an interface: breaks map keys.

### D2. Level spec

- **One place for all level-dependent values.** Add an unexported `levelSpec`
  per `LevelType`, holding:
  - part, conformance and veraPDF flavour string;
  - maximum header version;
  - limits: real, string, DeviceN, and whether array/dict caps apply;
  - maximum ICC major version;
  - Arlington model version;
  - annotation, action, blend-mode and filter allow-lists;
  - the pdfaid values to expect and to write.
- **Both sides read it.** `ValidationContext` carries it, and so does the
  convert run. The verifier and the fixers read the same accessors, so they
  cannot drift.
- **Keep the exported globals as Part 1 values.** `ForbiddenActions`,
  `AllowedAnnotationTypes` and the rest keep their current names and values.
  Add `...For(level)` accessors beside them.

### D3. Profiles

- **Filter by level.** `NewFullProfile(level)` enables only the checks whose
  mask includes `level`. `PDFA1B` and `Legacy1B` must come out
  byte-identical, which a regression test should prove.
- **New profiles:** add `PDFA2B` and `PDFA3B`, later `PDFA2U`.
- **Leniency flags.** The two veraPDF leniency flags (`SkipUnreachableXObjects`,
  `SkipUnusedSimpleFonts`) carry over unchanged.
- **No `Legacy2B` unless needed.** There is no Isartor-style stricter suite
  for Part 2, so add one only if real disagreements with the standard text
  show up.
- **Filtering.** `filterByProfile` switches from `p.Allows(clause, sub)` to
  `p.Has(check)`.

### D4. Results and JSON (decide)

- **Clauses are level-aware at the result boundary.** `Result.Checks()`,
  `Summary()` and `Result.MarshalJSON` render `ClauseIn(r.Type)`. A-1b output
  is unchanged.
- **Bare `PDFError` JSON.** It carries no level, so it keeps the Part 1
  clause. Optionally add a `clauses` map to `Check` JSON; that is an additive
  field.
- **Decide** whether an additive `"part"` field in `Result` JSON is wanted.

### D5. Arlington model per version

- **Generate two models.** Parameterise `gen.go` (`tsvDir`, `modelVersion`)
  and generate `model_gen_14.go` and `model_gen_17.go`.
- **Choose by version.** Add `arlington.ModelFor(version)`. `Type`, `Types`
  and `Post14Keys` become `*Model` methods, with 1.4 shims so current callers
  keep working.
- **Vendor the data.** Copy `tsv/1.7` next to `1.4` and `latest`.
- **Repo size.** That adds roughly 500 KB of generated code. **Check** that
  the generator's per-version folding (`gen.go:757`, `:826`, `:1002`) works
  unchanged for 1.7.
- **Make the hidden 1b bans explicit first.** Before adding the 1.7 model,
  turn the JPX/Crypt filter and ObjStm/XRef bans that fall out of the 1.4
  enum into explicit Part 1 checks. Otherwise they silently disappear for
  anyone using the object model directly. This also gives JPX loss in 1b a
  named check (item 6).
- **Rename the post-version check.** `KeyIntroducedAfterPDF14` becomes "key
  introduced after the level's PDF version". The name stays; the docs change.

### D6. Embedded files under 2b (decide)

- **What the rule demands.** 6.8-5 requires each embedded file to be PDF/A-1
  or PDF/A-2. veraPDF decodes it and runs a nested validation.
- **Verify recursively.** Use `pdf.OpenBytes` and `VerifyContext` with the
  profile matching the embedded file's own claimed part.
- **Bounds:**
  - a depth limit (2 is enough);
  - a cap on total embedded bytes, inside the `SetLimits` budget.

  The decoded bytes live in the Go heap, which conflicts with the "never load
  the whole file" rule. Bound it explicitly and document the exception.
- **Report nested failures as one outer issue.** Prefix the nested messages.
  Add no nesting type to `PDFError`.
- **Convert policy (decide):**
  - **(a)** recursively convert embedded PDFs. Drop non-PDF attachments and
    record them in `LostObjects`.
  - **(b)** always drop attachments, reported.
  - **(c)** make it an `Options` field.

  Recommendation: (a) by default. Add an `Options` field later only if
  someone asks.

### D7. JPEG 2000 (decide)

- **Verification needs no codec.** A header-box reader (`jp2h`, `ihdr`,
  `colr`, `bpcc`, plus the raw-codestream SIZ fallback) is about 300–400
  lines, in a new `internal/jpx` package. The layering test table needs an
  entry for it.
- **Conversion, renderer and fidelity gate would need a decoder.** A decoder
  is thousands of lines. The pure-Go candidates are small, young and unvetted
  (Apache/MIT), and taking one would end the single-dependency posture
  (`klauspost/compress` only).
- **Recommendation:**
  - no decoder;
  - repair only what can be repaired at box level;
  - report the rest;
  - report gray placeholders as `RasterDrops`;
  - revisit only if the real-world corpus shows JPX pages actually being
    rasterized.

### D8. Corpus vendoring (decide)

- **Size.** The 2b tree is 23 MB, against the 9.2 MB vendored today.
- **Options:** vendor it into `tests/veraPDF/PDF_A-2b` like 1b, or fetch it
  in CI.
- **Recommendation:** vendor it. The 1b precedent and offline runs are worth
  23 MB. Do not vendor PDF_A-4 (82 MB).
- **Attribution.** `tests/veraPDF/attribution.md` covers the extra trees
  unchanged.

### D9. Output PDF version

- **Header.** For A2B/A3B the writer emits `%PDF-1.7`. 1.4 would pass the
  regex, but it misstates what the file contains once JPX, OC or 16-bit
  images are kept.
- **Writer interface.** `WriteDocumentIndexed` takes the version.
- **Keep the classic xref table.** Object streams are allowed, not required.
  Compressed output is a later, optional size win.

---

## Work items

Sizes:

| Size | Meaning |
|---|---|
| S | under a day |
| M | a few days |
| L | a week or more |
| XL | a new subsystem or codec |

Each phase ends green on everything before it. The 1b gates must stay green
throughout:
- Isartor 204/204
- veraPDF-1b 569/569
- the convert floor at 510/510
- the real-world corpus at 1580/1580
- coverage at 100% under `-short`

### Phase 0 — preparation inside 1b, no behaviour change

Everything here lands while only A1B exists and is provable by the existing
gates. It removes the structural risk before any Part 2 rule is written.

**1. Unknown level is an error (S).** `Verify`, `VerifyParts`,
`VerifyStructural` and `Convert` reject any level without a `levelSpec`. The
test is a fake level that errors instead of returning Valid.

**2. Check applicability and per-part clauses (M).** Implement D1 and D3:
- the parts mask;
- the side table;
- `ClauseIn` and friends;
- `NewFullProfile` filtering;
- `filterByProfile` via `p.Has`;
- level-aware rendering in results, JSON, CLI `report.go:67,193` and wasm
  `main.go:195`.

Every existing check gets mask = Part 1, which reproduces today's output. The
test is a golden JSON and summary of the full 1b corpora, byte-identical
before and after.

**3. `levelSpec` in both pipelines (M).** Implement D2. Move every literal and
table listed under "Where things stand" behind the spec, in verify and in
convert (`fixups_limits.go:53`, `:219`, `fixups_geometry.go:26`,
`fixups_content.go:264-270`, `fixups_devicen.go`). Still 1b-only values.

**4. Thread the target into convert (M).**
- **Run state.** Build `target := targetFor(p)` once in `RunContext`.
- **Pre-emptives.** Their signature becomes `func(env, trailer, doc)`.
- **Fixers.** Add a `levelAware` capability (`forTarget(t) Fixer`, where nil
  means "not for this level"). It is applied in `buildLocalFixers`, replacing
  today's type switch, in the same style as `batchDictFixer`/`targetedFixer`.
- **Object model.** `ObjectModel`/`Undefined` produce a part-0 target that
  turns off every PDF/A pre-emptive. That fixes defect 2, if the user confirms
  it is a defect.
- **Loss accounting.** `convert.go:544` uses the level-aware lookup.

**5. Split the non-selective fixers (M).**
- **The split.** `extGStateFixer`, `annotationFlagsFixer`, `actionFixer`,
  `formFixer` and `fileSpecFixer` become one repair per check: TR / TR2 / RI /
  SMask / BM / CA / ca, flags / CA, and so on.
- **Shared walks are kept.** Several pieces can still share one walk through
  the existing `batchDictFixer` capability, so 1b throughput does not regress
  (verify with `benchstat`).
- **Gate.** The 510 floor and the real-world sweep stay identical.

**6. Fix the 1b defects found (S–M each).**
- **(a) JPX.**
  - Add an explicit Part 1 JPX filter check (D5).
  - Report gray-placeholder images as `RasterDrops` (`raster/image.go:42` →
    the drop list).
  - Do the same for JBIG2, which is legal in 1b and painted gray today.
- **(b) Attachments.** Report deleted ones in `LostObjects`.
- **(c) Optional content.** Before deleting `/OCProperties`, remove the
  marked content of default-OFF OCGs, or report it, so hidden layers do not
  appear.
- **(d)** Covered by item 4.

Each is worth a 1.2.x/1.3 release on its own.

**7. Arlington 1.7 model (M).** Implement D5. The 1b object-model results stay
identical, because 1b keeps 1.4. Unit-test `ModelFor("1.7")` on a hand-built
JPX image dict and an ObjStm.

**8. Harness generalisation (M).**
- **Shared runners.** `runVeraPDF(flavour, ...)`, and `differentialDeviations`
  keyed by `flavour+path`.
- **Corpus table.** One table drives the corpus suites:
  `[]struct{dir; profile; level; flavour}`.
- **Clause matching.** `clauseMatches` uses `ClauseIn(level)`.
- **Convert floor.** `minConvertedFully` becomes a per-level table.
- **Real world.** The `should-pass` partition gets a flavour dimension, via
  `scripts/source-realworld-corpus.py classify`.
- **BFO.** `Main.java` takes the profile as an argument.
- **Fuzzing.** Fuzz targets get a profile selector byte.
- **Fixtures.** `internal/pdfgen` can emit `%PDF-1.7`, xref streams and
  object streams.
- **Corpus data.** Vendor the veraPDF 2b, 2u and 3b trees (D8).

### Phase 1 — PDF/A-2b verifier

Order follows corpus weight and dependency. Every new or changed check lands
with the corpus files of its clause turning green. Commit per clause section,
following the existing phase-by-phase workflow.

**9. Structural relaxations (M).**
- **Drop for Part 2:**
  - `XRefStream`, and the subsection-header spacing check;
  - `ArrayTooLarge` and `DictTooLarge`;
  - `PostPDF14ViewerPref`;
  - the linearized-ID alternative;
  - `EmbeddedFileSpec`, `EmbeddedFiles` and `OptionalContent` as bans (see
    items 17–18 for what replaces them).
- **Classic xref only.** `checkXRefSectionFormat` runs only when the xref at
  that offset is a classic table.
- **Parameterise:**
  - the header regex `^%PDF-1\.[0-7]$`;
  - reals ±3.403e38, plus the new tiny-real rule (6.1.13-5);
  - strings at 32767;
  - DeviceN at 32;
  - the new page-box rule 3–14400 (6.1.13-11).
- **Filters.** The stream-filter whitelist (6.1.7.2-1: Crypt only as
  Identity) and the inline-image whitelist (6.1.10-1).
- **New checks:**
  - UTF-8 names (6.1.8-1);
  - the `/Perms` keys and SigRef digest keys (6.1.12-1 and -2).

Oracle: the 2b `6.1` folder, 63 files.

**10. Metadata (L, the largest by test count).** The 2b `6.6` folder has 590
files.
- **Profile-driven identification:** `pdfaid:part` and `conformance` come from
  the spec, plus the `corr` prefix rule (6.6.4-7).
- **XMP 2005:** switch the predefined-schema table from 2004 to 2005, and
  allow properties defined in the main package (6.6.2.3.1-1).
- **UTF-8:** XMP must be UTF-8 (6.6.2.1-5).
- **Drop for Part 2:** Info↔XMP sync (`InfoDictXMPMismatch`, `InfoXMPSync`)
  and `MetadataFiltered`.
- **Keep:** the xpacket `bytes`/`encoding` bans. They are still rules
  (6.6.2.1-2 and -3); the verifier audit's guess that they drop was wrong.
- **Renumber:** extension schemas move from 6.7.8 to 6.6.2.3.x, a one-to-one
  mapping with the subclause offset by one.
- **Decide against the corpus:**
  - `InfoDictUnreadable`, `InfoDictEmptyValues` and `ObjectXMPNoXPacket`
    have no 2b rule;
  - keep them only if 2b corpus files expect them.

**11. Colour (M).**
- **ICC:** v4 allowed (`< 5.0`) in `ValidateICCProfileStream` and
  `ICCInputProfileDefect`.
- **OutputIntents:** add the PDF/X `DestOutputProfileRef` ban (6.2.3-3). The
  multi-OI identity rule already exists (`OutputIntentMultipleProfiles`);
  re-check its semantics against 6.2.3-2 `sameOutputProfileIndirect`.
- **Overprint (6.2.4.2-2):** no OPM=1 with ICCBased CMYK when overprinting.
  This needs overprint state from content scanning.
- **Spot colours:** the DeviceN Colorants rule (6.2.4.4-1), and Separation
  consistency across the file (6.2.4.4-2): a deep compare of tint transform
  and alternate, per colorant name.
- **Device colour (6.2.4.3):** the tests are identical in veraPDF. Confirm on
  the 47 corpus files whether the Part 2 prose about blending colour space and
  DefaultX changes anything.
- **Drop for Part 2:** the ICC `/N` mismatch rule. Map which of
  `ICCBasedComponentsMismatch` and `ICCBasedProfileInvalid` corresponds to
  1b 6.2.3.2-2.

**12. Transparency and ExtGState (M).**
- **Drop for Part 2:** `StrokingAlpha`, `NonStrokingAlpha`,
  `ImageWithSoftMask`, `SoftMaskExtGState` and `TransparencyGroup`.
- **Rework `BlendMode`** to the 16-mode whitelist plus Compatible (6.2.10-1).
- **New: page group `/CS` (6.2.10-2).** When there is no OutputIntent, a page
  containing transparency must carry a group `/CS`. Implement the
  "contains transparency" predicate from:
  - ExtGState ca/CA below 1, SMask or a non-Normal BM;
  - images with an SMask;
  - forms with a transparency group;
  - annotations.

  Reuse `hasTransparentExtGState` (`fixups_alpha.go:99`) logic. The same
  predicate is needed by the converter.
- **Halftones (6.2.5-3 to -6):** no HTP; HalftoneType 1 or 5; no
  HalftoneName; TransferFunction only where required.
- **Keep:** TR, TR2 and RI unchanged.

Oracle: 6.2.10 has 56 files, 6.2.5 has 10.

**13. Images and JPEG 2000 (M).**
- **Image rules:** `ImageBitsPerComponent` allows 16. OPI applies to images,
  and to forms through 6.2.9-1. `Interpolate` is unchanged.
- **JPX:** the box reader (D7) plus 6.2.8.3-1 to -5:
  - channels 1, 3 or 4;
  - colour specifications;
  - METH 1, 2 or 3;
  - no CIEJab;
  - bit depth 1–38 with no `bpcc` box.

  Fixtures come from the 15 files in 6.2.8. Fuzz the box reader.

**14. Fonts (L).**
- **Parameterise:**
  - `FontFileSubtype` adds `/OpenType`;
  - `ValidateFontProgram` (`checks_font_program.go:699`) must accept it and
    validate OTTO/sfnt via the existing `ParseSfnt` and `ExtractCFFBytes`.
- **Rework:**
  - `Type1SubsetCharSet` and `CIDSubsetCIDSet` apply only when present;
  - `CIDToGIDMapMissing` applies only when the font is embedded;
  - `CMapNotEmbedded` accepts the Table 118 list;
  - `CIDSystemInfoMismatch` adds the Supplement comparison;
  - `TrueTypeEncoding` allows Unicode-compliant `Differences` via the AGL,
    `uniXXXX` and `uXXXX` names;
  - `SymbolicTrueTypeCmap` passes with one subtable or with (3,0).
- **New:**
  - CMap may reference only Table 118 CMaps (6.2.11.3.3-3);
  - the non-symbolic TrueType cmap rule (6.2.11.6-1);
  - **no `.notdef` references (6.2.11.8-1).**
- **`.notdef` is the hard one.** Per text-showing operator, in every
  rendering mode, resolve each code to a glyph name through encoding,
  `Differences` and the program's glyph lookup.
  - The usage machinery already exists: `ComputeContentUsage`, the used codes
    and CIDs.
  - So do the glyph lookups: `TTGlyphPresent`, `ParseCmapFormat*`, the CFF
    charsets.
  - It shares a path with glyph presence (6.2.11.4.1-2) and widths
    (6.2.11.5-1).
  - BFO already flags this in 1b output; see the memory note on PR #85.

Oracle: 6.2.11 has 53 files.

**15. Annotations, actions, forms, signatures (M).**
- **Annotations:**
  - `DisallowedSubtype` takes its list from the spec;
  - `/F` required except on Popup (6.3.2-1);
  - LockedContents must be clear;
  - `MissingAppearance` applies to every annotation except Popup, Link and
    zero-size ones (6.3.3-1);
  - drop `OpacityNotOne` and `ColourWithoutIntent`.
- **Actions:**
  - the forbidden-action set comes from the spec;
  - `AdditionalActions` is narrowed to the catalog (6.5.2-1), pages (6.5.2-2),
    widgets (6.4.1-1) and fields (6.4.1-2);
  - for `FieldAction`, veraPDF's 6.4.1-2 tests only `AA`, so drop `FieldAction`
    for parity.
- **Forms:** add catalog `NeedsRendering` (6.4.2-2). NeedAppearances and XFA
  are unchanged.
- **Signatures (6.4.3-1 to -3):**
  - ByteRange must cover the whole file;
  - the DER PKCS#7 must contain a signing certificate and exactly one
    SignerInfo.

  This needs a minimal DER walker using `encoding/asn1` from the stdlib.
  There are zero signature fixtures in the 2b corpus outside 6.4; check how
  many 6.4 has.

**16. Optional content, alternate presentations, requirements (S).**
- `OptionalContent` the ban becomes the 6.9-1 to -4 config rules:
  - `/Name` present and unique across `/D` and `/Configs`;
  - `/Order` lists every OCG;
  - no `/AS`.
- New: no AlternatePresentations (6.10-1), no PresSteps (6.10-2), no
  Requirements (6.11-1).

**17. Embedded files (L).** Implement D6, with 6.8-2 (F and UF) and 6.8-5
(the nested PDF/A-1/2 check).
- **Policy for nested files.** The nested verification picks the profile from
  the embedded file's `ClaimedConformance`. Only `1b` exists among levels A
  and B of Part 1; a nested 1a, 2a or 2u needs a policy: treat it as 1b or 2b.
- **Fixtures.** No 6.8 files exist in the 2b corpus (6.8 lives in 3b and 2a
  there). Build fixtures, and use the BFO suite's 6.8 file.

**18. Explicit resources, 6.2.2-2 (M).** A content stream must not rely on
inherited resources. veraPDF defers this rule. It applies to form XObjects,
tiling patterns and Type 3 glyph streams. Reuse `resourceScope`
(`verifier.go:857`) to detect inheritance.

**19. Phase gate.**
- The veraPDF 2b corpus is 986/986 and correctly clause-mapped.
- `TestDifferentialVeraPDFCorpora` runs with `--flavour 2b`, with an empty
  deviation list or every entry justified.
- The BFO pdfa-testsuite agrees, or each disagreement is documented.
- `PDFA2B` and `A2B` are exported, with a CHANGELOG entry.
- This is releasable as "PDF/A-2b verification" before any converter work.

### Phase 2 — PDF/A-2b converter

**20. Gate off Part 1 destruction (M).** Every row marked GATE in Appendix B.
- **Explicit code for pre-emptives.** `pagesKidsRebalanceVisitor` and
  `dropOversizedStructure` need it, because pre-emptives run regardless of
  what the verifier reports.
- **Automatic for loop fixers** once A2B stops emitting their checks, now that
  item 5 has split the multi-dimension visitors.
- **New gate: pass-file idempotence.** Converting each of the 377 veraPDF 2b
  pass files must:
  - keep it valid;
  - avoid rasterizing it;
  - leave fidelity free of blanked or overpainted pages;
  - leave the structural feature counts unchanged (SMask, BM, Group, OCG,
    JPX, attachments).

  This is the safety net for the whole phase.

**21. Rework shared fixers (M).** Every REWORK row in Appendix B. Make each
one read the spec:
- **Annotations:** annotation set, flags, `/F`, LockedContents, appearance
  synthesis for all annotation types.
- **Actions:** the action set and the narrower AA scope.
- **ExtGState:** only BM outside the whitelist becomes `/Normal`; add HTP and
  halftone removal.
- **Limits:** level-driven, tiny reals zeroed, geometry rescale skipped.
- **ICC:** accept v4; keep valid existing v4 OutputIntents rather than
  replacing them.
- **DeviceN:** flatten only above 32, and add Colorants entries.
- **Font subset metadata:** regenerate when present and incomplete.
- **TrueType encoding:** the Part 2 rules.
- **Object-model fixers:** use the 1.7 model.
- **`post14KeyFixer`:** off for 2b.

**22. New small fixers (S each).**

| Rule(s) | Fix |
|---|---|
| 6.4.2-2, 6.10, 6.11 | Delete NeedsRendering, Requirements, AlternatePresentations and PresSteps. |
| 6.1.12-1/2 | Clean `/Perms` and the SigRef digest keys. |
| 6.9 | Repair OC configs: names, `/AS`, `/Order`. This replaces `optionalContentFixer`. |
| 6.2.5 | Strip halftones and HTP. |
| 6.1.13-11 | Fix page box sizes: shrink by folding into the CTM, as `fixups_geometry.go` does; grow tiny boxes. |
| 6.1.8-1 | Make names valid UTF-8. Transcode, and rename consistently across tint arrays and Colorants. |
| 6.2.4.2-2 | Set OPM 0 where it collides with ICCBased CMYK overprint. |
| 6.1.10-1 | Inline-image filter whitelist. |
| 6.1.7.2-1 | Crypt filter must be Identity. |

**23. Separation consistency (M).** Group Separation arrays by colorant name
and make the tint transform and alternate identical. The first occurrence
wins; the result is reported, because spot appearance may change.

**24. Explicit resources (M).** Give forms, patterns and Type 3 glyph streams
their own `/Resources`, copying what they inherit; `computeResourceUsage`
(`fixups_limits.go:310`) already finds the used names. Interaction:
`qNestingFixer` declines streams without their own resources, so this may
improve it in 1b too.

**25. Embedded files (L).** D6 policy (a):
- recursive `ConvertBytes` of embedded PDFs at the same level, under the
  depth and size budget;
- drop non-PDFs and record them in `LostObjects`;
- add `/F` and `/UF`.

Memory and stress tests cover the recursion.

**26. `.notdef` repair (L).** Two strategies:
- substitute the font, extending `fontSubstitutionFixer`
  (`drawsNotdef`, `fixups_font_subst.go:578`);
- or drop the code from the string and compensate the advance with a `TJ`
  adjustment, through `walkResourceAwareContent`.

Choose per font kind. Verify against BFO, which is the stricter reader here.

**27. JPX (M).** With the box reader:
- drop extra `colr` boxes, keeping one with METH 1–3 or the approximation
  field;
- fix `bpcc`.

Anything needing a codec stays as a residual, or the image is dropped and
reported (D7).

**28. Output (S–M).**
- **Header:** the writer emits `%PDF-1.7` for A2B (D9).
- **XMP:** writes part 2 and conformance B.
- **Preserve existing XMP:** patch it rather than regenerate (M). This is
  worth doing for 1b too.
- **Non-catalog metadata:** `stripEmbeddedMetadataVisitor` becomes
  validate-or-delete.
- **Info dictionary:** `normalizeInfoDict` is harmless and stays.

**29. Renderer and fidelity (M–L).**
- **Why it is needed now.** Before any 2b page can safely reach the raster
  fallback, the renderer needs:
  - OC visibility (BDC /OC, OCMD and the default config's OFF list);
  - ExtGState SMask (luminosity needs a sub-render);
  - blend modes in `blendPixel`.

  Otherwise a rasterized 2b page bakes in hidden layers and hard shadows.
  Group isolation and knockout come after.
- **Make the fidelity gate see retained features.** Add a renderer-independent
  structural diff to `ConvertResult`: per-feature input-vs-output counts. The
  symmetric-renderer argument (`fidelity.go:10-17`) no longer covers features
  the converter keeps.
- **Fallback output.** Optionally emit an 8-bit `/SMask` instead of the 1-bit
  stencil; that is legal in Part 2.

**30. Phase gate.**
- The convert floor covers the 609 veraPDF 2b fail files. It is raised
  ratchet-style and starts wherever the fixers land.
- The `expectedRasterized` list for 2b is strictly shorter than 1b's. No
  transparency, OC or attachment fixture may rasterize.
- veraPDF `--flavour 2b` accepts every converted output.
- On the real-world corpus with a 2b target, everything 1b converts also
  converts. Report the rasterized-page count against 1b; it should drop
  sharply.
- BFO PDFA2b over a sample.
- CLI `--profile pdfa2b`, wasm level argument, README, doc.go,
  ARCHITECTURE.md and `TestREADMEStatesCheckCounts` all updated.

### Phase 3 — PDF/A-2u (small, optional)

**31. Unicode mapping (M).**
- `ToUnicodeMissing` becomes a real check for 2u: every used code must map to
  Unicode (6.2.11.7.2-1), and the values must exclude U+0000, U+FEFF and
  U+FFFE (6.2.11.7.2-2).
- **Fallbacks in order:**
  1. ToUnicode;
  2. standard encodings and AGL names for simple fonts;
  3. Adobe CID collections and predefined CMaps for CID fonts.
- pdfaid conformance must be U or A.
- **Converter:** synthesise ToUnicode where derivable; the font-substitution
  code already builds encodings.
- **Oracle:** 22 corpus files.

### Phase 4 — PDF/A-3b

**32. Verifier (S–M).**
- `pdfaid:part=3`.
- Drop 6.8-5, the recursion.
- **New checks:**
  - 6.8-1: EmbeddedFile `/Subtype` matches the MIME regex
    `^[-\w+\.]+/[-\w+\.]+$`;
  - 6.8-3: `AFRelationship` on file specs. **Check** veraPDF's model to see
    whether it is enforced on file specs without `/EF`;
  - 6.8-4 (deferred): every file spec reachable from some `/AF` array, by
    collecting `/AF` targets during the walk.
- **Oracle:** the 12 corpus files, plus ZUGFeRD/Factur-X positives if their
  licences permit.

**33. Converter (M).**
- Keep attachments, with no recursion.
- **Fill in each attachment's metadata:**
  - `AFRelationship` (default `/Unspecified`);
  - MIME `/Subtype` by sniffing magic bytes and the extension, falling back
    to `application/octet-stream`;
  - `/Params /ModDate` and `/Size`;
  - `/F` and `/UF`.
- Make every attachment reachable from catalog `/AF`.
- **Optional API:** caller-supplied attachments, which ZUGFeRD-style use needs.
  That is an `Options` addition; **decide** whether it is wanted.

**34. Phase gate.**
- Corpus 12/12.
- The differential check runs with `--flavour 3b`.
- veraPDF 3b accepts the converted outputs.
- CLI, docs and README updated.

---

## Effort, roughly

| Phase | Size | Notes |
|---|---|---|
| 0 — preparation | L | Mostly mechanical, wide; every gate must stay identical |
| 1 — 2b verifier | L–XL | Metadata (590 files) and fonts/`.notdef` dominate; JPX, signatures and embedded recursion are the new subsystems |
| 2 — 2b converter | XL | Gating is cheap. `.notdef`, attachments, explicit resources and renderer work are not |
| 3 — 2u | M | |
| 4 — 3b | M | Mostly embedded-file metadata |

Verification alone is roughly half the effort 1b verification took, because
the parser, font programs, ICC, XMP, content scanning, Arlington, the
differential harness and the corpus walkers all carry over. The converter is
the bigger and riskier half. Most of its cost is making ~40 fixers
level-aware without moving any 1b output by a byte.

## Risks

- **1b regressions from plumbing.** This is the biggest risk. Mitigation:
  - Phase 0 is behaviour-neutral;
  - golden 1b JSON;
  - the 510 floor and the real-world sweep before and after every item.
- **veraPDF semantics hidden in model properties.** Some rules are only
  expressions over veraPDF model properties: `gOutputCS`,
  `containsTransparency`, `isValidPDFA12`, `differencesAreUnicodeCompliant`,
  glyph names. Their exact meaning lives in veraPDF-validation and
  veraPDF-model.
  - Read the source with `gh api` rather than guessing, per the existing
    memory note.
  - Two points are still open: whether the 3b `AFRelationship` test applies
    to file specs without `/EF`, and which properties the halftone and Glyph
    rules use.
- **Recursion and memory.** Nested embedded PDFs break the
  "never hold the whole file" rule. They need explicit caps and stress tests.
- **The renderer is blind to retained transparency.** Until item 29, any 2b
  page that falls back to raster can lose appearance without the fidelity gate
  noticing.
- **Unpinned veraPDF install.** A new upstream release can move the
  differential baseline. Pin it at the start of Phase 1.
- **Arlington 1.7 model size and generator assumptions.** Check that the
  folding passes behave for 1.5–1.7 predicates before relying on them.

---

## Appendix A — the 159 catalogue checks under Part 2

Verdicts merge the verifier audit with the veraPDF rule diff. Where they
disagreed, veraPDF wins; those cases are marked with †.

| Verdict | Meaning |
|---|---|
| KEEP | Same rule. Needs only a Part 2 clause entry. |
| PARAM | Same rule with different values. |
| REWORK | Changed semantics. |
| DROP | Not a Part 2 rule. |
| OPEN | Decide against the 2b corpus. |
| 1A | Level A only. Not implemented today. |

**KEEP**
- Structure: FileHeaderComment, FileHeaderCommentLength,
  FileHeaderCommentBytes, TrailerEncrypt, TrailerEOF, GraphResolutionFailure,
  HexStringInvalidChar, HexStringOddLength, StreamFileSpec, StreamFileFilter,
  StreamFileDecodeParams, StreamKeywordEOL, EndstreamEOL,
  StreamLengthIncludesEOL, StreamLengthMismatch, StreamUndecodable,
  ObjectFraming, StreamLZWFilter, InlineImageLZWFilter (folded into the
  6.1.10 whitelist), NameTooLong, IntegerOutOfRange, CMapCIDOutOfRange,
  IndirectObjectsExceeded, GraphicsStateNesting.
- Colour: OutputIntentNotArray, NotDict, InvalidS, WrongS, MissingIdentifier,
  MultipleProfiles, UnresolvedProfile, InvalidProfile, MissingN, InvalidN;
  RenderingIntent; UndefinedOperator.
- Image: ImageInterpolate†, ImageAlternates, ImageOPI, ImageRenderingIntent,
  ImageMaskBitsPerComponent, FormOPI, FormSubtype2PS, FormPSEntry,
  ReferenceXObject, FormPostScript, PostScriptXObject.
- ExtGState: TransferFunction, DefaultTransferFunction,
  ExtGStateRenderingIntent.
- Font: FontType, InvalidSubtype, FontBaseFont, SimpleFontFirstChar,
  SimpleFontLastChar, SimpleFontWidths, CMapWModeInconsistent,
  SimpleNotEmbedded, CIDNotEmbedded, SubsetGlyphCoverage,
  AdvanceWidthMismatch, SymbolicTrueTypeEncoding.
- Annotation: PrintFlagNotSet, HiddenFlagSet, InvisibleFlagSet, NoViewFlagSet,
  AppearanceMissingN, AppearanceExtraEntries†, AppearanceNNotStream.
- Action: DisallowedNamedAction.
- Metadata: MetadataMissing, MetadataPropertyType, MetadataUndeclaredProperty,
  XPacketBytesAttribute†, XPacketEncodingAttribute†, all ten Ext* checks
  (renumbered 6.7.8 → 6.6.2.3.x), XMPStreamUnreadable, XMPNotWellFormed,
  XMPNoCorrespondingType, PDFAIdentifierMissing, PDFAIdentifierNamespace.
- Form: NeedAppearances, XFA, FieldAdditionalActions.

**PARAM**
- Structure:
  - FileHeaderSignature: header regex `1.[0-7]`;
  - XRefKeyword: classic xref only;
  - RealOutOfRange: ±3.403e38;
  - StringTooLong: 32767;
  - DeviceNColorants: 32;
  - TrailerID: no linearized alternative.
- Colour: OutputIntentICCVersion (< 5.0).
- Image: ImageBitsPerComponent (adds 16).
- Font:
  - FontFileSubtype: adds OpenType;
  - InvalidProgram: validates OpenType programs;
  - CIDSystemInfoMismatch: adds Supplement;
  - CMapNotEmbedded: Table 118.
- Action: ForbiddenActionType (set).
- Metadata: PDFAConformanceLevel (B/U/A), PDFAPartNumber,
  PDFAIdentifierUndefinedProperty (`corr`).
- ObjectModel, all six against the 1.7 model: MissingRequiredKey,
  WrongValueType, DisallowedValue, IndirectRequired, KeyIntroducedAfterPDF14,
  ConstraintViolated.

**REWORK**
- Structure:
  - EmbeddedFileSpec and EmbeddedFiles: bans become 6.8;
  - OptionalContent: ban becomes 6.9 config rules.
- Colour:
  - DeviceColourSpaceUsage and DeviceColourContentStream: confirm Part 2
    blending-CS prose on 47 files;
  - SeparationAlternateColour: folds into 6.2.4.3, plus new 6.2.4.4.
- Transparency: BlendMode (whitelist).
- Font:
  - Type1SubsetCharSet, CIDSubsetCIDSet: only if present;
  - CIDToGIDMapMissing: only if embedded;
  - TrueTypeEncoding: AGL Differences;
  - SymbolicTrueTypeCmap: one subtable or (3,0).
- Annotation:
  - DisallowedSubtype: new set;
  - MissingAppearance and WidgetMissingAppearance: all annotations except
    Popup, Link and zero-size.
- Action: AdditionalActions (catalog, page, widget and field only).

**DROP for Part 2**
- Structure: XRefStream, XRefSubsectionHeader (the spacing rule; confirm
  which of the two subsection checks maps to 1b 6.1.4-1), ArrayTooLarge†,
  DictTooLarge†, PostPDF14ViewerPref.
- Colour: the ICC `/N` mismatch check (1b 6.2.3.2-2). Confirm which of
  ICCBasedComponentsMismatch and ICCBasedProfileInvalid it is; the other is
  PARAM (< 5.0).
- Transparency: SoftMaskExtGState, StrokingAlpha, NonStrokingAlpha,
  TransparencyGroup, ImageWithSoftMask.
- Annotation: OpacityNotOne, ColourWithoutIntent.
- Metadata: MetadataFiltered, InfoXMPSync†.
- Structure: InfoDictXMPMismatch†.
- Form: FieldAction (veraPDF 6.4.1-2 tests only `AA`).

**OPEN**
- InfoDictUnreadable and InfoDictEmptyValues: gopdfrab-specific, with no
  veraPDF rule in either part.
- ObjectXMPNoXPacket.

**1A**
- ToUnicodeMissing, which becomes the 2u check in item 31.
- The five LogicalStructure checks.

## Appendix B — converter fixers under a Part 2 target

**Pre-emptive fixups.** These always run, so every change needs explicit
gating.

| Fixup | 2b |
|---|---|
| `injectOutputIntent` | REWORK: ICC < 5.0; keep valid OutputIntents, including v4 and multiple sharing one profile |
| Encrypt strip | KEEP |
| `regenerateXMP` | REWORK: part and conformance from the spec; preserve existing XMP |
| `stripEmbeddedMetadataVisitor` | REWORK: validate or delete |
| `promoteEmptyGlyphsInFont` | KEEP |
| `repairNonSymbolicTrueTypeCmap` | KEEP |
| `inlineType1WidthSubrsInFont` | KEEP |
| `normalizeCFFWidthDefaultsInFont` | KEEP |
| `pagesKidsRebalanceVisitor` | GATE |
| `dropOversizedStructure` | GATE (it deletes tagging) |

**Loop fixers.**

| Verdict | Fixers |
|---|---|
| KEEP | `deviceColourFixer`, `fontDictFixer`, `type0FontFixer` (add Supplement), `baseFontFixer`, `formFixer` (add NeedsRendering), `imageMetadataFixer`, `postScriptXObjectFixer`, `fontMetricFixer`, `fontSubstitutionFixer` (later extended for `.notdef`), `inlineImageLZWFixer`, `qNestingFixer`, `nameTooLongFixer`, `cmapCIDClampFixer`, `lzwStreamFixer`, `undecodableStreamFixer` |
| REWORK | `disallowedAnnotFixer`, `appearanceFixer`, `iccBasedProfileFixer`, `separationAlternateFixer`, `deviceNColorantsFixer`, `contentLimitsFixer`, `fileSpecFixer` (split: stream F keys stay banned, EF becomes policy), `actionFixer`, `extGStateFixer` (BM whitelist only; `repairOpacity` GATE), `annotationFlagsFixer` (no CA; add LockedContents and `/F`), `fontSubsetMetaFixer`, `trueTypeEncodingFixer`, the five Arlington object-model fixers (1.7 model) |
| GATE | `annotColourFixer`, `imageSampleDepthFixer` (16-bit → 8-bit RGB loses the colour space), `optionalContentFixer` (replaced by OC config repair), `viewerPrefFixer`, `fontFileSubtypeFixer` (OpenType is legal), `pagesTreeArrayFixer`, `resourceDictPruneFixer`, `post14KeyFixer`, `transparencyFlattener` (the core Part 1 destruction) |

## Appendix C — new 2b rules, for reference

Rule IDs are veraPDF `clause-test` under ISO 19005-2.

| Rule | veraPDF object | Requirement |
|---|---|---|
| 6.1.8-1 | CosUnicodeName | Font, colorant and structure type names are valid UTF-8 |
| 6.1.12-1 | PDPerms | Permissions dict has only UR3 and DocMDP |
| 6.1.12-2 | PDSigRef | With DocMDP, SigRef has no DigestLocation, DigestMethod or DigestValue |
| 6.1.13-5 | CosReal | No reals nearer zero than ±1.175e-38 |
| 6.1.13-11 | CosBBox | Page boxes are 3–14400 units on each side |
| 6.2.2-2 | PDContentStream, deferred | Resources are explicitly associated, not inherited |
| 6.2.3-3 | PDOutputIntent | No `DestOutputProfileRef` in a GTS_PDFX intent |
| 6.2.4.2-2 | PDICCBasedCMYK | OPM is not 1 when overprinting with ICCBased CMYK |
| 6.2.4.4-1 | PDDeviceN | Spot colorants are present in the Colorants dict |
| 6.2.4.4-2 | PDSeparation | Same-name Separations share tint transform and alternate |
| 6.2.5-3 | ExtGState | No HTP |
| 6.2.5-4 | Halftone | HalftoneType is 1 or 5 |
| 6.2.5-5 | Halftone | No HalftoneName |
| 6.2.5-6 | Halftone | TransferFunction only where required |
| 6.2.8.3-1 | JPEG2000 | 1, 3 or 4 channels |
| 6.2.8.3-2 | JPEG2000 | Colour specs: exactly one, or exactly one with APPROX = 1 |
| 6.2.8.3-3 | JPEG2000 | METH is 1, 2 or 3 |
| 6.2.8.3-4 | JPEG2000 | Not EnumCS 19 (CIEJab) |
| 6.2.8.3-5 | JPEG2000 | Bit depth 1–38 and no `bpcc` box |
| 6.2.10-1 | CosBM | Standard blend modes only |
| 6.2.10-2 | PDPage | Group `/CS` when there is transparency and no OutputIntent |
| 6.2.11.3.3-3 | PDReferencedCMap | Only Table 118 CMaps are referenced |
| 6.2.11.6-1 | TrueTypeFontProgram | Non-symbolic TrueType has a usable non-symbolic cmap |
| 6.2.11.8-1 | Glyph | No `.notdef` references |
| 6.3.2-1 | PDAnnot | `/F` present, except on Popup |
| 6.3.3-1 | PDAnnot | AP present, except on Popup, Link and zero-size |
| 6.4.2-1 | PDAcroForm | No XFA |
| 6.4.2-2 | CosDocument | No NeedsRendering |
| 6.4.3-1 | PDSignature | ByteRange covers the whole file |
| 6.4.3-2 | PKCS#7 | Signing certificate present |
| 6.4.3-3 | PKCS#7 | Exactly one SignerInfo |
| 6.5.2-2 | PDPage | No `/AA` |
| 6.6.2.1-5 | XMPPackage | UTF-8 |
| 6.6.4-7 | PDFAIdentification | `corr` uses the `pdfaid` prefix |
| 6.8-2 | CosFileSpecification | An embedded file spec has F and UF |
| 6.8-5 | EmbeddedFile | The embedded file is PDF/A-1 or PDF/A-2 (2b only) |
| 6.9-1 | OC config | `/Name` present |
| 6.9-2 | OC config | `/Name` unique |
| 6.9-3 | OC config | `/Order` lists every OCG |
| 6.9-4 | OC config | No `/AS` |
| 6.10-1 | Name dictionary | No AlternatePresentations |
| 6.10-2 | PDPage | No PresSteps |
| 6.11-1 | Catalog | No Requirements |

Clause crosswalk, 1b → 2b:

| 1b | 2b | Topic |
|---|---|---|
| 6.1.7 | 6.1.7.1 | Streams |
| 6.1.10 | 6.1.7.2 (streams), 6.1.10 (inline images) | Filters |
| 6.1.8 | 6.1.9 | Object framing |
| 6.1.12 | 6.1.13 | Limits |
| 6.2.10 | 6.2.2 | Content operators |
| 6.2.2 | 6.2.3 | Output intent |
| 6.2.3.2 | 6.2.4.2 | ICCBased |
| 6.2.3.3 | 6.2.4.3 | Device colour |
| 6.2.4 | 6.2.8 | Images |
| 6.2.5–6.2.7 | 6.2.9 | Forms, PostScript, reference XObjects |
| 6.2.8 | 6.2.5 | ExtGState |
| 6.2.9 | 6.2.6 | Rendering intent |
| 6.4 | 6.2.10 | Transparency |
| 6.3.x | 6.2.11.x | Fonts |
| 6.5 | 6.3 | Annotations |
| 6.6.1 | 6.5.1 | Actions |
| 6.6.2 | 6.4.1 / 6.5.2 | Additional actions |
| 6.7 | 6.6 | Metadata |
| 6.7.8 | 6.6.2.3 | Extension schemas |
| 6.7.11 | 6.6.4 | PDF/A identification |
| 6.9 | 6.4 | Interactive forms |
