# Integrating the Arlington PDF Model

This document tracks how the [Arlington PDF Model](https://github.com/pdf-association/arlington-pdf-model)
was integrated into gopdfrab. The integration is **done**: `internal/arlington` is a compiled-in
PDF 1.4 object-model table, `internal/verify` drives five generic checks from it over the resolved
graph, and all five are enabled in both `PDFA_1B` and `Legacy_1B`. What follows is the original
design rationale (still accurate) plus a record of what was actually built and the false positives
found along the way, so a future change to this area doesn't have to rediscover the same traps.

## What Arlington is

Arlington is a machine-readable definition of the PDF object model (ISO 32000-2, with
version annotations reaching back to PDF 1.0), maintained by the PDF Association under
Apache-2.0. For every dictionary/array/stream type in the spec ("Catalog", "Page",
"ExtGState", "ArrayOf...", ...) it provides a TSV row per key/index with:

- `Key` — the entry name (or `*` for wildcard/array element)
- `Type` — one or more allowed PDF types (`dictionary`, `array`, `name`, `integer`, ...),
  sometimes gated by a predicate (`fn:IsPDFVersion(1.5)`)
- `Required` — whether the key must be present (itself sometimes predicated)
- `IndirectReference` — whether the value must/must not/may be an indirect reference
- `Inheritable` — whether the key inherits from an ancestor (relevant for e.g. `Page`)
- `DefaultValue`, `PossibleValues` — enumerated legal values, when constrained
- `SinceVersion`, `DeprecatedIn` — the PDF version a key/value was introduced/withdrawn
- `Link` — the Arlington type(s) that a dict/array value should itself conform to

## Why this matters for gopdfrab

`internal/verify` encodes PDF/A-1b's clause-by-clause requirements as hand-written Go, one
function per rule area: `checks_dict.go`, `checks_colour.go`, `checks_font.go`, etc. A recurring
pattern there — `AllowedAnnotationTypes`, `ActionTypes`, `AllowedIntents`, `Post14ViewerPrefKeys`,
`IsAllowedBlendMode` (`checks_dict.go`) — is a hand-maintained Go map or switch standing in for
"the set of legal values/keys/types the PDF spec defines for this object." These tables are
correct because the corpora (Isartor, veraPDF) are green, but each is a second, independent
transcription of the spec that can silently drift. Before this integration there was no generic
check for "does this dictionary have keys/types/values the base PDF object model doesn't allow at
all," only for the specific PDF/A-narrowed subset someone already wrote a check for.

Arlington fills that gap: a generic **object-model conformance layer**, orthogonal to the
existing PDF/A-specific clause checks. All five checks below are now implemented:

- **`MissingRequiredKey`** — a dictionary lacking a key its Arlington type requires.
- **`WrongValueType`** — a key's value isn't one of its schema-allowed types.
- **`DisallowedValue`** — a key's value isn't one of its enumerated `PossibleValues`.
- **`IndirectRequired`** — a key whose value must be an indirect reference is inlined directly.
- **`KeyIntroducedAfterPDF14`** — a key the model says was introduced after PDF 1.4 is present
  (generalizes the old `Post14ViewerPrefKeys` one-off, from the full Arlington key set rather
  than one hand-picked dictionary).

None of this replaces the PDF/A-specific restriction logic (Arlington describes what ISO
32000 *permits*; PDF/A narrows that further, e.g. forbidding `JavaScript` actions that are
otherwise perfectly legal PDF). The two layers are complementary: Arlington catches "this
isn't even valid PDF," the existing checks catch "this is valid PDF but not valid PDF/A."

## Architecture, as built

### 1. Vendor + codegen, not a runtime TSV parser

Following the same pattern as `internal/pdf/checks_catalog.go`, TSVs are not parsed at runtime.
`internal/arlington` is populated by `go generate ./internal/arlington/...`, which runs
`gen.go` (`//go:build ignore`) and writes the checked-in `model_gen.go`.

Two TSV sets are vendored under `internal/arlington/testdata/tsv/` (Apache-2.0, upstream
`LICENSE` + provenance `README.md`):

- **`1.4/`** (288 files) — upstream's own pre-filtered, PDF-1.4-only subset. This is the source
  of truth for the generated `Types` table; PDF/A-1b is built on PDF 1.4, so no extra
  `SinceVersion`/`DeprecatedIn` filtering is needed beyond consuming this set as given.
- **`latest/`** (613 files) — the unfiltered current set, used *only* to compute
  `ObjectType.Post14Keys` (see `KeyIntroducedAfterPDF14` below) by diffing its key set against
  `1.4/`'s per type. It does not otherwise feed the generated table.

The generated API (`internal/arlington/arlington.go`):

```go
type ObjectType struct {
    Name       string
    Keys       []KeyDef
    Wildcard   *KeyDef  // the "*" row, if this type has one
    Post14Keys []string // keys present in tsv/latest but absent from tsv/1.4
}

type KeyDef struct {
    Name              string
    Types             []ValueType
    Required          bool
    IndirectReference IndirectRule // Either | Required | Forbidden
    SinceVersion      string
    DeprecatedIn      string
    PossibleValues    []string
    LinkGroups        []LinkGroup  // see "Resolving ambiguous Link edges" below
    Inheritable       bool
    Predicated        bool
}

var Types = map[string]ObjectType{ /* generated */ }
func Type(name string) (ObjectType, bool)
```

Hot-path allocation-free and dependency-free, consistent with [[large-file-no-full-load]].

### 2. Resolving "which Arlington type is this dict" at a graph position

`verifyDocument`'s existing `walk` (`internal/verify/verifier.go`) threads a third parameter,
`expectedType string`, alongside the pre-existing `owner`, seeded at the trailer as
`"FileTrailer"`. `FileTrailer.Root` links to `Catalog`, so the whole graph types itself from the
root down through the *same* single recursion — no second graph walk, matching the project's own
`ComputeContentUsage` guidance against duplicate walks.

**Resolving ambiguous `Link` edges (the biggest single piece of work here).** Many keys don't
link to a single Arlington type: `PageObject.Annots` → `ArrayOfAnnots`'s wildcard has 19
candidate `Annot*` types; any action dispatch site has 13 `Action*` candidates;
`Resources.XObject`'s wildcard has 4. The generator resolves these **generically, at `go
generate` time**, not via a hand-written name-mapping table:

- `Type` and `Link` columns are split in lockstep into one `LinkGroup` per type alternative
  (`ValueTypes`, `Candidates`).
- For any group with more than one candidate, every candidate's *own* schema is searched for a
  key it declares `Required=TRUE` with exactly one `PossibleValues` entry (e.g.
  `AnnotText.Subtype`→`[Text]`, `ActionGoTo.S`→`[GoTo]`, `FunctionType2.FunctionType`→`[2]`).
  The key name covering the most candidates (ties broken alphabetically) becomes the group's
  `Discriminator`, and its `ByValue` map is built from the candidates that cleanly qualify —
  candidates that collide on the same value are dropped from the map rather than guessed.
  This is not a `"Action"+S` naming convention: `/S == "JavaScript"` correctly resolves to
  `ActionECMAScript`, discovered from `ActionECMAScript`'s own schema row, not string-built.
- At runtime, `arlingtonChildType`/`arlingtonElementType`
  (`internal/verify/checks_objectmodel.go`) pick the `LinkGroup` matching the concrete value's
  Go-level kind, then resolve via the single candidate or the discriminator lookup. Every
  ambiguous step — no group matches, no discriminator was found, the discriminator key is
  absent, its value is unrecognized — fails closed (`""`, untyped): propagating a wrong guessed
  type would misvalidate an entire subtree, a worse outcome than leaving it unchecked.

### 3. Checks, wired as a distinct group

`internal/pdf/checks_catalog.go`'s `objectModelChecks` group holds all five checks, registered
under a synthetic non-numeric clause `"objmodel"` (subclauses 1–5; `ClauseLess` already sorts
non-numeric clauses safely via string fallback). All five fire from one generic function,
`validateAgainstSchema(v pdf.PDFDict, typeName string, ctx *ValidationContext)`
(`internal/verify/checks_objectmodel.go`), driven by table lookup rather than per-rule Go code —
deliberately the opposite of the existing one-function-per-clause style, appropriate here because
the "rule" is genuinely data (the Arlington row), not spec prose requiring interpretation.
Reports go through the same `ctx.Report(...)` every other check uses, so fixers resolve
violations by ref exactly like any other finding.

Registering a check puts it in `NewFullProfile`, so both `PDFA_1B` and `Legacy_1B` enable it
automatically — **with one exception**: `KeyIntroducedAfterPDF14` is disabled in `PDFA_1B` (see
Known limitation below), so `PDFA_1B` explicitly `RemoveCheck`s it in `internal/pdf/profile.go`,
alongside the pre-existing `FormPostScript`/`PostScriptXObject` veraPDF divergences.

### 4. PDF 1.4 gating for PDF/A-1b

Handled entirely by consuming `tsv/1.4/` as the source of truth (see §1) — no extra gating logic
needed, since upstream's own per-version filtering already does this correctly (confirmed:
`Catalog.tsv` shrinks from 33 rows in `latest` to 24 in `1.4`).

## What stays hand-written

Arlington does not model, and these remain exactly as before:

- PDF/A's own restrictions on top of otherwise-legal PDF: forbidden action types
  (`ForbiddenActions`), forbidden blend modes (`IsAllowedBlendMode`'s "must be Normal/Compatible"
  restriction), mandatory `Print` flag, XMP/Info synchronization, ICC profile internals
  (`ValidateICCProfileStream`). `AllowedAnnotationTypes`/`ActionTypes`/`AllowedIntents` also stay:
  their *value lists* overlap Arlington's `PossibleValues`, but the restriction logic wrapping
  them (which subset PDF/A forbids) is PDF/A-specific judgment Arlington has no opinion on.
- Content-stream operator semantics (`checks_content.go`) — Arlington covers objects, not the
  operator grammar.
- Font program internals (`checks_font_program.go`) — glyph tables, CFF/Type1 parsing.

**`Post14ViewerPrefKeys`/`PostPDF14ViewerPref` specifically was investigated for deletion** (the
one genuine full overlap with `KeyIntroducedAfterPDF14`) and kept: `KeyIntroducedAfterPDF14` is
`Legacy_1B`-only (see below), so the hand-written check still carries real weight under
`PDFA_1B`, where nothing else covers this today.

## Known limitations

**Predicate handling.** Arlington rows are sometimes conditional (`fn:Eval`, `fn:IsRequired(...)`,
`fn:BeforeVersion(...)`, cross-key predicates). The codegen step classifies each row as **simple**
(plain type list, plain boolean-or-absent `Required`, plain `SinceVersion`/`DeprecatedIn`) or
**predicated**, and only simple rows feed the checks. Predicated rows are skipped (never flagged),
erring toward false negatives — consistent with [[coverage-target-95]]. Currently classified as
simple: **87.3%** of rows (`TestClassificationFloor` in `arlington_test.go` gates against a 0.85
floor). A full predicate evaluator remains unbuilt — see Next steps.

**`KeyIntroducedAfterPDF14` is enabled in `Legacy_1B` only, not `PDFA_1B`.** Found via real
(non-corpus) files: `FileTrailer.XRefStm` (a hybrid-xref compatibility pointer, by design
ignorable by a PDF 1.4 reader) and `Catalog.Extensions` (a purely informational
extension-declaration dict) are both keys Arlington introduced after 1.4, but real veraPDF
(external tool) does not flag either — Arlington's post-1.4 key list has no way to distinguish
"changes required interpretation" (genuinely forbidden in PDF/A-1b) from "purely
additive/ignorable" (harmless), and that distinction is PDF/A-specific judgment outside
Arlington's data. Handled via the same `RemoveCheck` precedent already used for
`FormPostScript`/`PostScriptXObject`.

**Array-first-element-style discrimination is not covered.** Colour space arrays (and a few
other special-cased arrays) are disambiguated by their first element being a name, not by a
dict key — a structurally different shape than the `Subtype`/`S`/`FunctionType` discriminator
mechanism handles. These stay unresolved (`""`), same as before this work — no regression, just
not a win yet.

## False-positive traps found while making each check corpus-clean

Relevant to anyone touching `validateAgainstSchema` or adding a sixth check:

1. **PDF null (Go `nil`) must always match any key's type**, unconditionally, regardless of
   whether the schema's `Type` list includes `null`. ISO 32000 §7.3.9: null is structurally
   equivalent to the key being absent. (Real case: a veraPDF pass file with `/Title` as an
   indirect ref to a null object.) See [[pdf-null-and-parse-paths]].
2. **`/Length` must never be checked on stream dicts.** `internal/writer/writer.go` unconditionally
   recomputes `Length` from `RawStream` at serialize time, so its in-memory presence/value before
   that point is never a meaningful signal — `validateAgainstSchema` special-cases
   `kd.Name == "Length" && v.HasStream` to always skip.
3. **Arlington's `bitmask` type token maps to `pdf.PDFInteger`** alongside `integer`/`number` —
   flags fields (e.g. `OutlineItem.F`) are typed `bitmask` in Arlington but represented as a plain
   Go `pdf.PDFInteger` here.
4. **`gen.go`'s `IndirectReference` parsing had a real bug**: Arlington's `FALSE` means "not
   *required* to be indirect" (either legal), not "must be direct" (that's the separate,
   always-predicated `fn:MustBeDirect()` case). Fixed to map all-`FALSE` → `IndirectEither`; only
   all-`TRUE` rows → `IndirectRequired`.
5. **Widening type propagation (discriminators) surfaced two real conformance bugs in
   `internal/convert`**, not false positives: the font-substitution fixer
   (`fixups_font_subst.go`) built synthesized `FontDescriptor` dicts that never set `/Type`, and
   separately left an all-fields-missing scratch `FontDescriptor` behind whenever substitution
   was skipped (e.g. an AcroForm/DR font with all-zero `Widths`). Both were genuine ISO 32000
   object-model violations `Convert` was silently producing — fixed at the source, confirming the
   integration is pulling its weight rather than just adding noise.

## Rollout history

1. ~~Vendor Arlington TSVs~~ — done, `tsv/1.4/` + `tsv/latest/`.
2. ~~Codegen: `internal/arlington` package, unit-tested against `Catalog`/`Page`/`ExtGState`~~ —
   done, no verifier wiring in this step.
3. ~~Wire `MissingRequiredKey`/`WrongValueType`, enable in both profiles~~ — done, corpus-clean
   (Isartor 204/204, veraPDF 569/569).
4. ~~Add `DisallowedValue`, `IndirectRequired`, `KeyIntroducedAfterPDF14`~~ — done, one at a time,
   each corpus-clean before the next landed. `KeyIntroducedAfterPDF14` ended up `Legacy_1B`-only
   (see Known limitations).
5. ~~Revisit deleting overlapping hand-written tables~~ — investigated, nothing deleted:
   `AllowedAnnotationTypes`/`IsAllowedBlendMode`/`AllowedIntents`/`ActionTypes` all encode PDF/A
   restrictions Arlington doesn't model; `Post14ViewerPrefKeys` still carries real weight under
   `PDFA_1B` (see above).
6. ~~Discriminator-based type propagation~~ — done. Before this, `arlingtonChildType`/
   `arlingtonElementType` only propagated through single-candidate `Link` columns, so every
   annotation and action in every real PDF got zero schema checking. Now resolved generically via
   each candidate's own discriminating key, corpus-clean, and found/fixed two real `Convert`
   defects as a direct result (see above).

All five checks are corpus-clean on all three gates as of the latest work: Isartor 204/204
(`Legacy_1B`), veraPDF 569/569 (`PDFA_1B`), convert corpus 510/510 fully conformant. 100% test
coverage on `checks_objectmodel.go`.

## Next steps

### A standalone "object-model conformance" API, independent of PDF/A

Today the only way to run these five checks is through `verify.Verify(d, profile)` with a
PDF/A-1b profile (`PDFA_1B`/`Legacy_1B`) — there is no entry point for "just tell me what's wrong
with this PDF's base object model," decoupled from any PDF/A conformance level. This is a real
gap: the object-model layer is explicitly designed to catch "this isn't even valid PDF"
independent of PDF/A (see "Why this matters," above), but nothing currently exposes that
independently.

**This turns out to need no changes to the verification engine itself.** `verifyDocument`'s walk
already computes *every* registered check's findings unconditionally (`ctx.Report` never
consults the profile); `Verify` only narrows the result set at the very end via
`filterByProfile(issues, p)` based on `p.Allows(clause, subclause)`. So an object-model-only
report is purely a matter of profile construction, not new traversal or validation logic:

```go
// internal/pdf/profile.go
// ObjectModelOnly returns a profile enabling only the generic ISO 32000 object-model checks
// (MissingRequiredKey, WrongValueType, DisallowedValue, IndirectRequired,
// KeyIntroducedAfterPDF14), with every PDF/A-specific check disabled -- useful for asking
// "is this even valid PDF" independent of any PDF/A conformance level.
func ObjectModelOnly() *Profile {
    return NewProfile(A_1B).AddCheck(
        Checks.ObjectModel.MissingRequiredKey,
        Checks.ObjectModel.WrongValueType,
        Checks.ObjectModel.DisallowedValue,
        Checks.ObjectModel.IndirectRequired,
        Checks.ObjectModel.KeyIntroducedAfterPDF14,
    )
}
```

```go
// internal/verify/verifier.go
// VerifyObjectModel checks d against the generic ISO 32000 object-model checks only,
// independent of any PDF/A conformance level.
func VerifyObjectModel(d *pdf.Reader) (pdf.Result, error) {
    return Verify(d, pdf.ObjectModelOnly())
}
// + VerifyObjectModelFile(path string), VerifyObjectModelBytes(data []byte), mirroring the
// existing VerifyFile/VerifyBytes convenience wrappers.
```

Add a genuinely new `LevelType` (e.g. `pdf.ObjectModel`) purely for reporting, and generalize
  `Verify`'s internal gate (currently `if p.Level == pdf.A_1B { issues = verifyPdfA1b(d, p) }`)
  to run the same computation for this level too — a small, low-risk change since profile-based
  filtering already does all the real work.

### Other candidates, roughly in order of value/risk

- **Predicate evaluator** for the ~13% of rows currently skipped as predicated (`fn:IsRequired`,
  `fn:SinceVersion`, `fn:IsPresent`, ...). Flagged in the original design as "a project of its
  own" — real value (turns false negatives into real catches across all five checks) but open-
  ended scope and real corpus risk per row-shape newly enforced. Do this in small, corpus-gated
  increments per predicate family, not as one pass.
- **Array-first-element discrimination** (colour space arrays and similar), to close the one
  known gap in the discriminator mechanism.
