# Integrating the Arlington PDF Model

This document sketches how the [Arlington PDF Model](https://github.com/pdf-association/arlington-pdf-model)
could be integrated into gopdfrab, scoped to **verification** only (conversion/fixups are
out of scope here).

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

`internal/verify` currently encodes PDF/A-1b's clause-by-clause requirements as hand-written
Go, one function per rule area: `checks_dict.go`, `checks_colour.go`, `checks_font.go`, etc.
Reading through them (e.g. `AllowedAnnotationTypes`, `ActionTypes`, `AllowedIntents`,
`Post14ViewerPrefKeys`, `IsAllowedBlendMode` in `checks_dict.go:20-93,163-178`) shows a
recurring pattern: a hand-maintained Go map or switch standing in for "the set of legal
values/keys/types the PDF spec defines for this object." These tables are correct today
because the corpora (Isartor, veraPDF) are green, but they are a second, independent
transcription of the spec that can silently drift as new edge cases surface — there is no
generic check today for "does this dictionary have keys/types/values the base PDF object
model doesn't allow at all," only for the specific PDF/A-narrowed subset someone already
wrote a check for.

Arlington fills that gap: a generic **object-model conformance layer**, orthogonal to the
existing PDF/A-specific clause checks:

- **Missing required keys** (e.g. a `Page` missing `Type`, an `ExtGState`'s `Font` array
  missing its second element) — not currently checked anywhere generically.
- **Wrong value types** for a key (e.g. `/BitsPerComponent` as a name instead of an
  integer) — today only checked where a specific PDF/A rule already inspects that key.
- **Disallowed enum values** for constrained keys, sourced from `PossibleValues` instead of
  a bespoke Go map per key.
- **Indirect-reference requirements** — PDF/A already cares about a few of these
  (`Structure.ObjectFraming`), but Arlington has the full list.
- **Version-gated keys/values** — `SinceVersion`/`DeprecatedIn` generalizes the one-off
  `Post14ViewerPrefKeys` pattern (`checks_dict.go:91-110`) to every key in the spec, which is
  useful because PDF/A-1b is anchored to PDF 1.4 and forbids anything introduced later.

None of this replaces the PDF/A-specific restriction logic (Arlington describes what ISO
32000 *permits*; PDF/A narrows that further, e.g. forbidding `JavaScript` actions that are
otherwise perfectly legal PDF). The two layers are complementary: Arlington catches "this
isn't even valid PDF," the existing checks catch "this is valid PDF but not valid PDF/A."

## Proposed architecture

### 1. Vendor + codegen, not a runtime TSV parser

Follow the same pattern already used for `internal/pdf/checks_catalog.go`: don't parse TSV
at runtime. Add a `internal/arlington` package populated by `go generate` from the
Arlington TSV set.

**Done:** vendored under `internal/arlington/testdata/tsv/1.4/` (288 files), with upstream
`LICENSE` and a short provenance `README.md`. Notably, upstream ships its own
**pre-filtered, per-PDF-version TSV directories** (`tsv/1.0` through `tsv/2.0`, plus
`tsv/latest`) — `tsv/1.4/` already excludes any key/object/enum value introduced after PDF
1.4, so this is the exact subset PDF/A-1b (built on PDF 1.4) needs. This removes the need to
reimplement `SinceVersion`/`DeprecatedIn` filtering ourselves (see the updated §4 below); we
only need to consume the row set as given. `Catalog.tsv` shrinks from 33 rows in `latest` to
24 in `1.4`, confirming the filtering is real and meaningful, not a no-op.

Not yet done: the `go generate` step itself. The generator would emit a Go file with one
struct literal per Arlington type:

```go
type ObjectType struct {
    Name string
    Keys []KeyDef
}

type KeyDef struct {
    Name              string
    Types             []ValueType   // parsed from "array;dictionary;name" etc.
    Required          bool          // predicated rows fall back to false, see Limitations
    IndirectReference IndirectRule  // Forbidden | Required | Either
    SinceVersion      PDFVersion
    DeprecatedIn      PDFVersion    // zero value = never
    PossibleValues    []string      // per-type, when constrained
    Link              []string      // names of ObjectTypes the value should conform to
}

var Types = map[string]ObjectType{ /* generated */ }
```

This keeps the hot verify path allocation-free and dependency-free at runtime, consistent
with [[large-file-no-full-load]] and the project's general aversion to runtime parsing of
static data.

### 2. Resolving "which Arlington type is this dict" at a graph position

Arlington types aren't always self-announcing via `/Type`/`/Subtype` — plenty of dicts
(`ExtGState`, most array element types) have no type key at all, and the correct schema is
determined by *how you got there* (the `Link` column on the key that pointed at this value).
Concretely: extend `verifyDocument`'s existing `walk` (`verifier.go:530-624`), which already
threads an `owner` and already special-cases `Type == Page`
(`verifier.go:551-555`), to also thread the *expected Arlington type name* down from the
parent key's `Link`. Reuse the same recursion rather than a second graph walk — the
project's own `ComputeContentUsage` comment on `verifier.go:626-632` calls out avoiding
exactly this kind of duplicate walk.

Root the descent at `Catalog` (from the trailer's `/Root`), which is where Arlington itself
roots its own conformance ("TestGrammar") tooling.

### 3. New checks, wired as a distinct group

Add a `Checks.ObjectModel` group (or similar) to `checks_catalog.go`, analogous to
`Checks.Structure`/`Checks.Colour`, with a handful of checks rather than one per Arlington
type:

```go
type objectModelChecks struct {
    MissingRequiredKey    Check
    WrongValueType        Check
    DisallowedValue       Check
    IndirectRequired      Check
    KeyIntroducedAfterPDF14 Check
}
```

Each check fires from one generic validation function (`validateAgainstSchema(v pdf.PDFDict, typeName string, ctx *ValidationContext)`) driven by table lookup, not from per-rule
Go code — the opposite of today's one-function-per-clause style, which is appropriate here
because the "rule" is genuinely data (the Arlington row), not spec prose requiring
interpretation.

Report against `owner`/`v` the same way existing checks do (`ctx.Report(...)`), so fixers
can resolve violations by ref exactly like every other check today.

### 4. PDF 1.4 gating for PDF/A-1b

PDF/A-1b's base is PDF 1.4. Originally this section proposed filtering
`PossibleValues`/`Required`/`Type` ourselves against `SinceVersion`/`DeprecatedIn` — but
since vendoring confirmed upstream already ships a pre-filtered `tsv/1.4/` set (see §1),
that filtering is upstream's job, not ours: the generated table for PDF/A-1b purposes is
built straight from `tsv/1.4/` with no extra gating logic. A key that is present in a file
but absent from its dict's `tsv/1.4/` row is exactly the `KeyIntroducedAfterPDF14` case —
this generalizes `Post14ViewerPrefKeys` and should let that table be deleted once the
generic check covers it (confirm via corpus run before deleting).

One caveat worth flagging: `tsv/1.4/` still contains a few rows with `SinceVersion` *equal
to* 1.4 exactly (e.g. `BM`/`SMask`/`CA`/`ca` in `GraphicsStateParameter.tsv`, i.e.
`ExtGState`), which is correct — those keys are legal in PDF 1.4 — but also still contains
`fn:Extension(...)`-gated vendor extension rows (e.g. `AAPL:AA`, `AAPL:ST`) that are
technically outside base ISO 32000 PDF 1.4. Those should land in the "predicated, skip for
now" bucket from the Limitations section below, not be treated as ordinary 1.4 keys.

## What stays hand-written

Arlington does not model:

- PDF/A's own restrictions on top of otherwise-legal PDF (forbidden action types, forbidden
  blend modes, mandatory `Print` flag, XMP/Info synchronization, ICC profile internals). All
  of `checks_dict.go`, `checks_colour.go`'s ICC byte-level validation
  (`ValidateICCProfileStream`, `verifier.go:1211-1278`), and friends remain exactly as-is —
  they check things Arlington has no opinion on.
- Content-stream operator semantics (`checks_content.go`) — Arlington covers objects, not
  the operator grammar.
- Font program internals (`checks_font_program.go`) — glyph tables, CFF/Type1 parsing are
  out of Arlington's scope entirely.

Some existing tables *do* overlap and are candidates for deletion once the generic layer is
proven on the corpora: `AllowedAnnotationTypes`/`ActionTypes` overlap with Arlington's
`PossibleValues` for `Subtype`/`S`, though the *forbidden subset* logic
(`ForbiddenActions`) is PDF/A-specific and stays. `IsAllowedBlendMode`'s `PossibleValues`
list could come from Arlington; the "must be Normal/Compatible" restriction is PDF/A-specific
and stays.

## Known limitations / predicate handling

Arlington rows are sometimes conditional (`fn:Eval`, `fn:IsRequired(...)`,
`fn:BeforeVersion(...)`, cross-key predicates like "Required if `Filter` is absent"). A
full predicate evaluator is a project of its own. Recommended approach: the codegen step
classifies each row as either a **simple** row (plain type list, plain boolean-or-absent
`Required`, plain `SinceVersion`/`DeprecatedIn`) or a **predicated** row, and only simple
rows feed the generic checks initially. Predicated rows are skipped (never flagged),
erring toward false negatives over false positives — consistent with how this project
already treats defensive-parser edge cases per [[coverage-target-95]]. The generator should
report what fraction of rows it can classify as simple, so predicate coverage is a visible,
trackable number rather than silent gaps.

## Rollout plan

Following the project's usual [[pdfa-phase-by-phase]] approach:

1. ~~Vendor Arlington TSVs~~ **done** (`internal/arlington/testdata/tsv/1.4/`). Remaining:
   codegen, no runtime wiring yet. Unit-test the generated table against a handful of known
   types (`Catalog`, `Page`, `GraphicsStateParameter`/`ExtGState`).
2. Wire `MissingRequiredKey`/`WrongValueType` only, run against Isartor + veraPDF corpora
   in report-only mode (collect issues, don't fail the profile) to find false positives
   before they become regressions.
3. Once clean, add `Checks.ObjectModel` to `PDFA_1B` and `Legacy_1B` profiles and confirm
   204/204 + 569/569 still hold per [[pdfa-isartor-status]].
4. Add `DisallowedValue`, `IndirectRequired`, `KeyIntroducedAfterPDF14` the same way, one at
   a time.
5. Only after all four are corpus-clean, revisit deleting the overlapping hand-written
   tables named above.

## Licensing

Arlington PDF Model is Apache-2.0. Vendoring its TSV data under gopdfrab's dual
AGPL/commercial license is compatible (Apache-2.0 is permissive and AGPL-compatible for
inbound use); the upstream `LICENSE` is vendored alongside the TSVs at
`internal/arlington/testdata/LICENSE`, and the generated Go file's header comment should
credit the PDF Association.
