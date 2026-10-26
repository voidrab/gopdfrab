# Architecture review — 2026-10-26

**Baseline:** `main` at 76a407b.

**Method:** five parallel read-only reviews covered `internal/pdf`, `verify`+`arlington`, `convert`, `raster`+`fontprog`+`fontsubst`, and the public API, CLI, wasm, writer and tooling. Each claim marked **[repro]** was reproduced, and each marked **[code]** was confirmed by reading the code. Unmarked claims are a reviewer's reasoning.

**Overall verdict:** the package layering is sound and guarded by `TestPackageLayering`. Nothing needs restructuring for its own sake. The real problems are three cross-cutting gaps:

1. There is **no conformance-level abstraction**. "PDF/A-1b" is spread through every layer, down to the parser.
2. **Object identity is split.** A stream's bytes are not part of the shared dict body.
3. **Interpreters and walkers are duplicated** (content streams, charstrings, page trees, xref sections), and the copies have already drifted apart.

Gaps 2 and 3 have already produced real bugs (section 1). Gap 1 is what PDF/A-2b would hit first.

---

## 1. Bugs found during the review (fix before any restructuring)

| # | Severity | Finding | Evidence |
|---|---|---|---|
| B1 | High | **A shared stream is edited through one reference only.** The output is corrupt but reported `Valid=true`. `PDFDict` is a value type: `Entries` is shared, while `HasStream`/`RawStream` are copied. `walkGraph` writes back only to the slot it came through, and the writer emits whichever copy it meets first. Repro: a 16-bit image referenced from both `/Thumb` and `/Resources`. After `imageSampleDepthFixer`, the output has `/FlateDecode /BPC 8` over the raw 16-bit bytes, so the stream fails with `zlib: invalid header`. Writer helpers have the same problem: `setFlateStream` sets `/Filter` on the shared body and the bytes on the local copy. | [repro] `internal/convert/fixups_dict.go:64-108`, `internal/writer/writer.go:181-196,318-326` |
| B2 | High | **Issues are attributed to the wrong page.** `verifyDocument` sets `ctx.CurrentPage` when it enters a Page and never restores it. Keys are visited in sorted order, so `/Annots` comes before `/Resources`, and a Link `/Dest` to a later page moves every remaining issue on the page to that later page. `applyRasterFallback` picks which pages to rasterize from `iss.Page()`. Repro: a `/TR` on page 1 is reported on page 2. | [repro] `internal/verify/verifier.go:862` (compare the save/restore at :1072-1083) |
| B3 | High | **The raster Type2 interpreter ignores `callsubr`/`callgsubr` and the flex operators. The Type1 interpreter ignores `callsubr` and `seac`.** Almost every subset CFF font uses subroutines, so its glyphs render incomplete, and no drop is reported. The fidelity gate uses the same renderer on input and output, so it cannot see this. fontprog's width interpreter does follow subroutines, so the second copy is the one that went wrong. | [code] `internal/raster/glyph.go:384-489,737,796` |
| B4 | High | **When the iteration cap is hit, the raster backstop uses issues from before the last fix pass.** That last pass is never re-verified. The reviewer measured 1→193, 2→30 and 4→0 rasterized pages over 40 corpus files. `MaxIterations` is public. | reviewer-measured; `internal/convert/convert.go:437-516,584-590` |
| B5 | Medium | **Image decode is unbounded.** `image.NewRGBA(/Width,/Height)` is called with no cap. The JPX/JBIG2 placeholder needs no data, so 1 KB of input can request 14 GB, which is a fatal OOM that recovery cannot catch. | [code] `internal/raster/image.go:25,104,387` |
| B6 | Medium | **Likely data race:** `flattenFormToImage` mutates form entries inside a `ParallelFor` worker while another worker may be rendering a parent form. The page case already defers its mutation to the serial phase for exactly this reason. | `internal/convert/fixups_transparency.go:104,827-829` |
| B7 | Low | `fixups_devicen.go` tracks the fill/stroke colour space with no q/Q restore. | `:121-152` |
| B7 | Low | `resourceDictPruneFixer` and `detectColourModelUsage` ignore inherited `/Resources`, patterns and appearance streams, so the claim that pruning is "safe by construction" is false. | |
| B7 | Low | `checkInfoXMPSync` ranges over a map, so issue order varies between runs. | `checks_xmp.go:1123` |
| B7 | Low | `fontsubst.Pick` matches on substrings: "Microsoft Sans Serif" gets the serif face and "Monotype Corsiva" gets mono. A `/FontWeight 700.0` is missed. | `fontsubst.go:79-85` |
| B7 | Low | The global profiles are mutable: `PDFA1B.SkipUnusedSimpleFonts = false` changes them for every caller. | |
| B7 | Low | wasm parses each input three times, and its comment claims one parse. | `wasm/main.go:114-172` |

---

## 2. Architectural findings

### A1. There is no level abstraction, which blocks PDF/A-2b

PDF/A-1b assumptions are found in every layer:

- **The parser reports PDF/A checks.** There are 11 `Checks.Structure.*` sites in `document.go`, `stream.go` and `crypt.go`. One of them is `XRefStream` ("not permitted"), which is wrong under 2b.
- **A check's identity is a single Part 1 clause.** `newCheck` panics on a duplicate clause. Profile filtering goes from Check to clause and back to Check (`p.Allows(c.Clause(), c.Subclause())` in `verifier.go:231` and `convert.go:545`). The public `CheckByClause`, `ChecksForClause` and `Check.Clause()` take no level, and neither does the JSON wire identity. The CLI (`report.go`) and wasm sort and print by `Clause()`.
- **verify has about 20 hard-wired 1b sites:**
  - the xref-stream ban;
  - the EmbeddedFiles ban;
  - limits written twice in different forms;
  - DeviceN ≤ 8;
  - the ICC major-version check, written twice;
  - transparency, Optional Content and FontFile subtypes;
  - the XMP `part != "1"` check.
- **"Per-level clauses" are faked by reporting twice.** `FormPostScript`/`FormPSEntry` and `InfoXMPSync`/`InfoDictXMPMismatch` each report the same finding under two checks, and the filter keeps one.
- **convert: about 18 of 45 fixers and 5 of 10 pre-emptive steps hard-code 1b.** Fixers have no level input at all. `extGStateFixer` resets SMask, BM and CA/ca whenever any of its 7 checks fires, so in 2b it would destroy legal transparency. The XMP is stamped `part=1/B`, the injected ICC is v2, and Arlington is pinned to 1.4.
- **The policy tables are exported globals in `verify` and read by `convert`:** `AllowedIntents`, `ForbiddenActions`, `AllowedAnnotationTypes`, `Post14ViewerPrefKeys`, `IsAllowedBlendMode` and others. The limit 32767 is defined in 4 places; 65535, 127, 28, 4095 and 8191 are defined again in convert.

**Change:**

1. **The reader emits neutral diagnostics.** `Diagnostic{Kind: ObjectFraming | StreamKeywordEOL | LengthMismatch | UsedXRefStream | XRefRebuilt | …}`. verify maps a Kind to a Check through the level spec.
2. **A new `internal/pdfa` package holds `levelSpec`:**
   - the limits;
   - the allowed and forbidden sets;
   - the switches for transparency, Optional Content, xref streams and embedded files;
   - the ICC maximum version;
   - the XMP identity;
   - the Arlington model.

   verify and convert both read it, and the exported policy globals in verify are deleted.
3. **Make the change in the catalogue and lookups.**
   - Use `p.Has(c)` everywhere and delete `Profile.Allows`. This step is trivial.
   - Give each check stable explicit IDs and a parts bitmask.
   - Add `ClauseIn(level)`.
   - Add level-qualified public lookups and deprecate the clause-only ones.
   - Collapse each double-reported check into one Check whose clause varies by level.
4. **Fixers become issue-driven.** 34 of 41 `Fix` methods ignore their `issues` argument and repair everything their checks cover, and 53 comments say a fixer "mirrors" a check. Fixers should repair what was reported, through the spec. Split the bundled fixers (`extGStateFixer`, `contentLimitsFixer`).

The byte-exact 1b corpora (Isartor 204, veraPDF 569, real-world 1585) act as the regression oracle for this whole refactor.

### A2. Stream payload and ref belong in the shared dict body

Move `HasStream`, `RawStream` and the indirect ref into `DictBody`, with `Stream()`/`SetStream()`/`Ref()` accessors.

- **What it fixes:** B1 at its root.
- **What it deletes:**
  - the per-fixer workarounds: `parentSlot`/`replaceObject`/`collectParentSlots` (`convert_fixpass.go`), `uniqueByDict`, and the qNesting `done` map;
  - the `RefKey " ref"` hidden entry, which has 53 cross-package uses and 18 skip sites. The "subtract the ref key" count is also written twice.
- **Cost:** mechanical but wide: about 119 `.HasStream` sites and 22 `.RawStream` sites in production code. A short-term patch for B1 is to make `walkGraph` memoize its replacements by Entries pointer, which is about 10 lines.

### A3. Registration is ad hoc in both verify and convert

**verify:**

- `verifyDocument` calls 13 `validate*` functions on every dict without condition, and each one re-checks `/Type` itself.
- Five document-level families return `[]PDFError` with no page, no ref and no cancellation, instead of calling `ctx.Report`.
- Disabled checks still run.
- Adding a check means four hand-wired edits.

**convert:**

- Fixers register through `init()` over 4 channels, so their order is the order Go runs the files' init functions.
- `buildLocalFixers` type-switches over 6 concrete types to inject per-run state.
- `Applies` is only used once, at init.
- `fontFileSubtypeFixer.fixTargeted` is unreachable, because the batch capability is checked first.
- Three fixers have a no-op `Fix`.
- A test swaps the global registry, which rules out `t.Parallel`.

**Change:**

- **verify:** a rule registry: `dictRule{checks, applies(spec), fn}`, plus `docRule` and `contentOpRule`. The walk dispatches through it, every rule reports through `ctx.Report`, and a rule whose checks are all disabled is skipped. Do not build a DSL.
- **convert:** one explicit table, `[]fixerSpec{checks, phase, mode, new func(*run) Fixer}`. The pre-emptive fixups become phase 0, gated by the spec. Replace `init()` registration.
- **Phases:** give each check a declared `Phase` (PreStructural, Graph or PostStructural), plus a test. convert's replay currently relies on byte-reading checks happening to sit in the structural parts, and nothing enforces that.

### A4. Interpreters and walkers are duplicated, and the copies already disagree

| What | Copies | Drift already found |
|---|---|---|
| Content-stream graphics/colour state | raster, `paintAtDo`, devicen, the qnesting unnester, verify's usage scan | devicen has no q/Q; `paintAtDo` doesn't reset colour on `cs` and has no Pattern handling |
| Content-root and page-tree walk | 4 in convert, plus `walkPageTree`, plus `BuildPageIndex` | inherited resources ignored (B7) |
| CFF Type2 / Type1 charstrings | 2 Type2 interpreters, 3 number decoders, 3 Type1 walkers | B3 |
| Simple-font code→glyph mapping | raster's own copy, plus fontprog | raster defaults to StandardEncoding, fontprog to WinAnsi; raster uses (3,1)+0xF000 where the spec says (3,0) |
| `/Differences` loop | 7 | |
| `unitsPerEm` reads | 7 | |
| CMap CID-range loop | 2 | |
| xref-section readers | 5 (4 in pdf, 1 in verify) | different tolerances |
| Indirect-object readers | 4 | `parseIndirectObjectAt` always seeks and copies, even when the file is mmapped |
| `ComputeContentUsage` | recomputed per font fixer | same result the iteration's verify just produced |

**Change:**

- A new `internal/content` package (imports only `pdf`) holding `Matrix`/`Point` (moved out of raster), `GState`, `Tracker.Step(op, operands, res)`, resource lookup, and a guarded Form/Type3 walker. Consumers keep their own switch statements; this is not a full visitor. Migrate devicen, then `paintAtDo`, then the unnester, then raster. Move verify last, or never, because it is the hot path.
- `pdf.WalkPages`, which yields inherited Resources and MediaBox, and `pdf.ContentRoots`.
- In fontprog, one Type2 and one Type1 interpreter with width and outline sinks. The TrueType outline code moves there from raster. Also `SimpleFontGIDs`, `SfntMetrics` and `EachCMapCID`.
- One xref-section reader with a strictness flag. One byte-window abstraction under the lexer, which deletes the 15 `data != nil` forks and the bufio twins.
- Expose the content-usage result through `verify.Parts` so convert can reuse it.

### A5. `internal/pdf` does five jobs

The jobs are the object model, the reader, the codecs, the check registry with its diagnostics, and drawing helpers. It has 258 exported identifiers; 76 are unused by other packages' production code and 55 are unused outside the package at all.

`Reader` mixes the byte source, a lazy cache, and a renumbered in-memory graph (`SeedResolvedGraph`). After seeding, `XRefTable()` and `objCache` disagree, and `verifyAllObjectFraming` is correct only because of how it is called.

**Target (import cycles checked against current imports):**

```
internal/pdf/object  PDFValue, PDFDict{*body: entries+stream+ref}, scalars, Equal     → —
internal/pdf/filter  codecs, DecodeStream, decode cap, StreamKey                      → object
internal/pdf         lexer/parser/ContentScanner; Source (xref, crypt, mmap, neutral
                     Diagnostics); Graph (resolved objects, caches, footprint, pages) → object, filter
internal/checks      Check catalogue + parts mask, clause tables, Profile, PDFError,
                     Result, JSON                                                     → object
internal/gfx         colorspace, PDF functions (raster/convert only today)            → object, filter
internal/safe        ParallelFor, RecoverAsError, ErrInternal                         → —
```

During migration, keep `type PDFDict = object.PDFDict` aliases in `pdf` so the 2,246 call sites don't all move in one pass. Unexport the unused identifiers, using `export_test.go` where tests need them.

### A6. verify↔convert coupling is wide and implicit

- convert uses **45 distinct `verify.*` symbols**.
- It builds `&verify.ValidationContext{UsedCharCodes: …}` as a struct literal (`fixups_font_subst.go:172,639`) and reads `Issues()` back.
- `ValidationContext` has about 22 fields covering five roles: the issue sink, cancellation, walk cursor, document facts and caches.

**Change:**

- Split it into an immutable `DocFacts{Usage, Coverage, Spec}`, a private walk cursor, a sink and caches.
- Give convert a narrow `verify.Probe(facts)` API in place of the struct literals and the exported policy tables. The tables move to `pdfa` under A1.
- Delete the 10 `Verify*` file/bytes wrappers in verify, and the 10 entry-point wrappers in convert. Both duplicate the root facade, so the internal packages should take a `*Reader`.

### A7. convert does more than fixing

- `fidelity.go` imports only pdf and raster, so it becomes `internal/fidelity`. Its exported `CompareFidelity` has only test callers.
- The output plumbing (spill, `Save`, `ConvertResult`, the batch engine, about 350 lines) moves to `result.go`.
- The raster-fallback orchestration is split across `convert.go` and `fixups_transparency.go`; it gets its own file.
- `ttScaledAscentDescent`, `ttScaledCapHeight` and `trueTypeCmapSubtableCount` move to fontprog.
- `RunContext` (183 lines) and `rasterBackstop` (10 parameters, 2 out-pointers) become a `run` struct. The B4 fix belongs there.
- Keep the fixers in `convert`. Splitting them into their own package would mean exporting `fixPass` and the walkers.

### A8. Arlington

- It is single-version and exposed as a mutable global `Types` map, which convert iterates directly.
- The 125-line condition evaluator lives in verify and is re-exported to convert.
- `SinceVersion` is generated but never read.

**Change:**

- Give `gen.go` a `-version` flag that emits `model_14_gen.go` and `model_17_gen.go` (the latest table has 613 types against 288 for 1.4), with `ModelFor(v)`.
- Move the evaluator and link resolution to `internal/objmodel`, which verify and convert both import.

### A9. Public API (v1 constraints: additive or deprecation only)

**Freeze the function surface.**

- There are 26 verify/convert entry points. New levels become `Profile` values and new settings go into `Options`; neither gets new functions.
- Deprecate the 7 ObjectModel shorthands. They also have no Context, Options or password form.
- Fix the doc errors:
  - `ConvertAll`'s doc mentions Workers, but `ConvertAll` takes no Options.
  - "Verify uses only Password" is wrong: `VerifyAllContext` uses Workers.
  - "PDF/A-1b rewrite" also appears for the `PDF` profile.
- Do not add functional options in v1. That would be a second configuration style beside `Options`.

**Make the API level-aware.**

- Add `Profile.Name()`, `ProfileByName` and `Profiles()`, so the CLI and wasm stop hard-coding profile names.
- `Document.IsPDFA()` is tied to 1b. Document that, and add `Conforms(*Profile)`.
- Switch the CLI and wasm to `ClauseIn(result.Type)`.

**Fix shared state.**

- `Profile` fields are exported on shared pointers. Have the convenience functions use the internal pointers. In v2, unexport the fields.
- Add `Options.Limits` for per-call limits. `MaxResidentBytes` is already per-Reader; the decode cap is read at 3 sites. Keep `SetLimits` as the process-wide default.

**Use one JSON schema.** Add `Check.Group()` (wasm currently rebuilds group names by reflection) and `ConvertResult.MarshalJSON`. The CLI and wasm each define their own JSON today.

**Methods to deprecate.** These came through the type aliases: `Check.ID`, `PDFError.WithObjModelDetail`, and `Profile.Allows` (after A1).

**v2, only if another break forces it:** one family `Verify`/`Convert(ctx, Source, *Profile, Options)`, about 6 entry points in total.

**CLI:** add `--check-fidelity`. Without it the CLI can never report blanked pages.

### A10. Tests, tooling and assets

- **Fixture duplication.** `buildMinimalCIDCFF` is byte-identical in convert, fontprog and raster, with a variant in verify. 9 files load Liberation Sans themselves. convert has about 12 one-page trailer builders, even though `pdfgen` exists. Fix: an `internal/fonttest` package of pure byte builders with no internal imports; add `"fonttest": nil` to the layering test.
- **Tests in the wrong package:**
  - `verify/cff_widths_test.go` and `convert/fonttool_subset_test.go` test fontprog functions.
  - Parts of `convert/raster_test.go` test raster and pdf.
  - `verify/profile_test.go` and `results_test.go` test `pdf` types.
- **Root `convert_test.go`** is 1,538 lines and holds the corpus, differential and fidelity harnesses. Split it into `realworld_test.go`, `differential_test.go` and `conformance_test.go`, parameterised by level, ready for 2b.
- **No fuzz targets in fontprog or raster,** although their contract is "never panic". One bad font currently turns the whole document's result into `ErrInternal`.
- **CI:**
  - The `grep -vE '/benchmarks/|/main$'` filter is copied into 3 jobs and matches nothing.
  - `codecov.yml` ignores a `main` package that doesn't exist, and duplicates `check-coverage.sh`.
  - `setup-go@v4` is outdated.
  - Fix: one `scripts/test.sh` used by both CI and CONTRIBUTING.
- **The raster drop vocabulary** is untyped strings in the public `RasterDrops.Features`. Several approximations are not reported at all: clipping is reduced to a bounding box; dash, cap and join are ignored; ExtGState SMask and blend modes are ignored. `RenderPage` takes no context and has no operator budget.
- **fontsubst embeds 5.96 MB in every binary, wasm included.** Gzip at build time and inflate lazily: 3.1 MB with gzip, 2.1 MB with xz. Subset Noto Symbols 2 (1.23 MB). Make `Pick(flags, weight, name)` so fontsubst stops importing pdf. raster re-parses the substitute faces, including the 1.2 MB Noto face, on every page render.
- **Type 1 glyphs** are re-decrypted for each glyph drawn, and no outlines are cached for any font type.
- **README.** `TestREADMENamesPublicAPI` makes the 648-line README track every exported function. Point it at godoc or `docs/api.md`, and move the fuzzing section to CONTRIBUTING.
- **writer:**
  - `%PDF-1.4` is hard-coded, which is already wrong for `ConvertObjectModel` on 1.5+ input.
  - `WritePDF` and `SetStreamFlateFast` are dead exports that the 100% coverage gate still forces tests for.

---

## 3. Combined target structure

```
gopdfrab (root)      frozen facade; profiles.go (named profiles, lookup); Options.Limits
internal/pdf/object  object model with stream+ref in the shared body
internal/pdf/filter  codecs
internal/pdf         reader: Source (+ neutral Diagnostics) / Graph; WalkPages, ContentRoots
internal/checks      catalogue (parts mask, ClauseIn), Profile, PDFError, Result, JSON
internal/pdfa        levelSpec per level: limits, policy sets, switches, XMP identity, model version
internal/objmodel    Arlington ModelFor + condition evaluator
internal/arlington   generated tables per PDF version
internal/content     Matrix, GState, Tracker, resource lookup, Form/Type3 walker
internal/gfx         colour spaces, PDF functions
internal/safe        ParallelFor, RecoverAsError
internal/fontprog    readers; one Type2 + one Type1 interpreter; outlines; simple-font mapping; edit_*.go writers
internal/fontsubst   compressed assets, Pick(flags, weight, name) → fontprog
internal/verify      rule registry + single walk + DocFacts + Probe; xmp/ subpackage
internal/raster      renderer over content+fontprog; typed drops; ctx + budgets
internal/fidelity    out of convert
internal/convert     run struct, explicit fixerSpec table, issue-driven level-aware fixers, result.go
internal/writer      header version from spec; dead exports removed
internal/fonttest, internal/pdfgen   test-only builders
```

## 4. Suggested order

1. **Bugs B1–B6.** Each is small, and B1 has an interim 10-line fix.
2. **Cheap prerequisites for 2b:** `p.Has(c)` instead of `Allows`; restore the page after the walk (this is B2); declared check Phase plus a test; profile names and lookup; CLI/wasm `ClauseIn`.
3. **A2:** stream and ref into the shared body. Do this before the 2b fixers, which rewrite streams.
4. **A1 + A3 together:** neutral diagnostics, `internal/pdfa` levelSpec, the catalogue parts mask, the verify rule registry, the convert fixerSpec table, issue-driven fixers.
5. **A4:** single charstring interpreters (which also fixes B3), then `internal/content`, `WalkPages`/`ContentRoots`, and the xref reader.
6. **A5–A8 package moves.** These are opportunistic, done as each area is touched for 2b.
7. **A10 hygiene,** in parallel with the rest.
