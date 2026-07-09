# Arlington PDF Model Integration — Implementation Review

Reviewed 2026-07-08 on branch `feature/arlington` (6 commits, ~2.5k lines of hand-written
code plus the vendored TSVs and the 17.8k-line generated table). This replaces the original
design/rollout document; the maintainer reference at the bottom preserves the traps recorded
there.

## Verdict

**The branch fulfills its goals.** It set out to add a generic ISO 32000 object-model
conformance layer — driven by the Arlington PDF Model as data instead of hand-written
per-rule Go — without regressing any existing gate, and it did exactly that. All claims
were re-verified for this review:

- `go test` green across `internal/arlington`, `internal/verify`, `internal/pdf`,
  `internal/convert`; Isartor and veraPDF corpus suites pass.
- 100% statement coverage on `internal/verify/checks_objectmodel.go`; `internal/verify`
  overall at 89.7% (93.5% as of stage 4, all `internal/` packages ≥90%).
- The predicate-classification floor is test-gated (`TestClassificationFloor`, floor 0.85,
  observed ~87.3%).
- The integration already paid for itself: type propagation surfaced two real conformance
  bugs in `internal/convert`'s font-substitution fixer (synthesized `FontDescriptor` dicts
  missing `/Type`; orphaned scratch descriptors), both fixed on this branch with tests.

## What the branch delivers

1. **`internal/arlington`** — a compiled-in, dependency-free, allocation-free lookup table
   generated (`gen.go`, `go generate`) from two vendored Apache-2.0 TSV sets:
   `tsv/1.4/` (288 files, upstream's own PDF-1.4-only subset, the source of truth) and
   `tsv/latest/` (613 files, used only to diff out `Post14Keys`). No runtime TSV parsing.
2. **Generic discriminator resolution at codegen time** — ambiguous `Link` columns (19
   `Annot*` candidates, 13 `Action*`, ...) are resolved by searching each candidate's own
   schema for a `Required=TRUE` key with exactly one `PossibleValues` entry (`Subtype`,
   `S`, `FunctionType`). No hand-written name-mapping table; `/S = JavaScript` →
   `ActionECMAScript` falls out of the data. Every ambiguous step fails closed to `""`
   (untyped) — never a guessed type.
3. **Type propagation through the existing walk** — `verifyDocument`'s single recursion
   (`internal/verify/verifier.go`) threads `expectedType`, seeded `"FileTrailer"` at the
   trailer. No second graph walk.
4. **Five checks from one table-driven function** —
   `validateAgainstSchema` (`checks_objectmodel.go`) fires `MissingRequiredKey`,
   `WrongValueType`, `DisallowedValue`, `IndirectRequired`, `KeyIntroducedAfterPDF14`,
   registered under synthetic clause `"objmodel"` and reported through the same
   `ctx.Report` path every other check uses, so fixers resolve findings by ref as usual.
   `KeyIntroducedAfterPDF14` is `Legacy_1B`-only (`RemoveCheck` in `profile.go`) because
   real files carry harmless post-1.4 keys (`XRefStm`, `Extensions`) veraPDF doesn't flag.
5. **A standalone public API** — `pdf.ObjectModel` level, `ObjectModelOnly()` profile, and
   `VerifyObjectModel{,File,Bytes}` wrappers re-exported at the package root: "is this even
   valid PDF" without any PDF/A framing.

The two-layer split is architecturally right and clearly documented in code: Arlington says
what ISO 32000 *permits*, the existing hand-written checks say what PDF/A *further forbids*.
The overlapping hand-written tables (`AllowedAnnotationTypes`, `IsAllowedBlendMode`, ...)
were each investigated for deletion and correctly kept — they encode PDF/A judgment
Arlington has no data for.

## Review — strengths worth keeping

- **Fail-closed bias is applied consistently and correctly.** Predicated rows skipped,
  ambiguous links unresolved, unrecognized Go kinds treated as matching (in the *flagging*
  path) but non-matching (in the *type-propagation* path). The asymmetry between
  `valueTypeAllowed` (permissive default) and `linkGroupMatchesKind` (closed default) is
  deliberate and documented at both sites — the right call, since a wrong flag is noise but
  a wrong propagated type misvalidates a subtree.
- **Codegen over runtime parsing** matches the repo's existing pattern and the
  no-full-heap-load constraint; the generated table costs no startup work.
- **Reusing the existing walk and report path** means zero new infrastructure to maintain
  and fixers work on objmodel findings for free.
- **Each check landed one at a time, corpus-gated** — the false-positive traps below were
  found because of that discipline, not despite it.

## Room for improvement

Prioritized. Items 1–4, 6–7 and 9 are done, and item 5 is implemented through stages B1–B4
(what stays predicated: cross-object `::`/`parent::` paths, domain predicates like
`fn:NotStandard14Font`, `mod`/`fn:ArrayLength`/`fn:FileSize` operands, and the
unenforceable `fn:MustBeDirect` family) — see "Implementation progress" at the bottom.
Item 8 stays a documented false-negative class.

1. **`VerifyObjectModel` pays full PDF/A verification cost.** The `ObjectModel` level
   reuses `verifyPdfA1b` wholesale: content streams are decoded, font programs parsed, XMP
   validated — then `filterByProfile` throws away everything but the five objmodel
   findings. Correct, but wasteful for the advertised "just check the object model" entry
   point, and the cost gap grows with file size. Fix: gate the expensive check families
   (`validateContentStreams`, `ValidateFontDict`/font-program parsing, XMP) on the profile
   before running them, not after. This also benefits any future narrow profile.
2. **First-arrival-wins typing is nondeterministic.** `verifyDocument`'s `visited` map
   means a shared dict is schema-checked only under the `expectedType` of whichever path
   reaches it first — and Go's random map iteration order makes that path nondeterministic.
   A dict reachable via a typed path and an untyped (or differently-typed) path can gain or
   lose findings run-to-run. Today this only toggles false negatives (all gates are clean
   either way), but this project has already been bitten by nondeterministic ordering once
   (the convert-corpus 509 flake). Cheapest fix: iterate dict keys in sorted order during
   the walk; fuller fix: track `visited[ptr] → typeName` and re-visit (schema-check only,
   no recursion) when a node is later reached with a type it hasn't been checked under.
3. **Array-shaped types are never validated — 72 of 288 types (25% of the model).**
   `validateAgainstSchema` only fires on `PDFDict`; `ArrayOf*`, `Rectangle`-like, and
   fixed-index array schemas (required indices `0..3`, per-index types) are used solely to
   propagate element types into dict children. A `Rectangle` holding a string, or a
   required fixed index missing, is invisible to all five checks. A
   `validateArrayAgainstSchema` handling wildcard element types and fixed-index rows would
   close the single largest coverage gap — corpus-gate it like the others, since array
   shape is where malformed real-world files are common.
4. **Wildcard-row constraints on dict entries are unenforced.** `validateAgainstSchema`
   iterates `ot.Keys` only; a type's `*` row is used for `Post14Keys` suppression and child
   typing but its own `Types`/`IndirectReference` are never checked. Example: entries of a
   resource `XObject` map must be indirect streams — never flagged. Small, contained
   addition to the same function.
5. **Predicate evaluator for the ~13% skipped rows** — already flagged as next step in the
   original design, still the right long-term item. Do it per predicate family
   (`fn:IsRequired`, `fn:SinceVersion`, ...) with corpus gates per increment; it converts
   pure false negatives into catches across all five checks at once.
6. **Type re-anchoring when the descent loses track.** Once `expectedType` degrades to
   `""`, the entire subtree goes unchecked, even when a child self-identifies via
   `/Type /Page`, `/Type /Font`, etc. Sniffing a closed allowlist of unambiguous `/Type`
   (+`/Subtype`) values to re-anchor would recover coverage lost to the colour-space-array
   discrimination gap and to shared-object first-arrival ordering. Keep the allowlist
   small; a wrong re-anchor has the same subtree-misvalidation risk the discriminator
   design guards against.
7. **Array-first-element discrimination** (colour space arrays and similar) — structurally
   different from the dict-key discriminator; still unresolved (`""`), no regression but no
   win either. Reasonable to fold into item 6.
8. **Inheritable required keys are never checked.** `Required && Inheritable`
   (e.g. `Page.Resources`) is skipped outright rather than walked up the `Pages` ancestor
   chain. Existing hand-written checks cover the important cases, so this is low value —
   note it as a known false-negative class rather than build inheritance resolution.
9. **Minor cleanups.** `IndirectForbidden` is declared but never generated (zero
   occurrences in `model_gen.go` — `fn:MustBeDirect` rows are all predicated); either drop
   the constant or leave a comment that it's reserved for the predicate evaluator.
   `SinceVersion`/`DeprecatedIn` are carried per-key in the generated table but unread at
   runtime (the 1.4 TSV set is pre-filtered) — dropping them would shrink `model_gen.go`
   for free, keeping them only makes sense as predicate-evaluator groundwork; decide
   intentionally either way. `DisallowedValue` only enforces name/integer enums
   (string/real matching is format-fragile — documented, fine, but worth a line in the
   check's description).

## Reference for maintainers

Kept from the original document — traps that made each check corpus-clean, relevant to
anyone touching `validateAgainstSchema` or adding a sixth check:

1. **PDF null (Go `nil`) always matches any key's type**, regardless of the schema's
   `Type` list. ISO 32000 §7.3.9: null ≡ absent key. (Real veraPDF pass file: `/Title` as
   an indirect ref to a null object.)
2. **Never check `/Length` on stream dicts** — `internal/writer` recomputes it from
   `RawStream` at serialize time, so the in-memory value is meaningless.
   `validateAgainstSchema` special-cases this.
3. **Arlington's `bitmask` maps to `pdf.PDFInteger`** alongside `integer`/`number`.
4. **Arlington's `IndirectReference=FALSE` means "not required to be indirect"**, not
   "must be direct" — all-`FALSE` → `IndirectEither`; only all-`TRUE` → `IndirectRequired`.
   (`fn:MustBeDirect()` is the separate, always-predicated must-be-direct case.)
5. **`_ref` is the resolver's indirect-object marker key** and leaks into `Entries`; every
   key iteration in this layer must skip it, and `isIndirect` relies on it (arrays carry no
   marker, so array indirect-required rows — 3 in the model — always pass, a deliberate
   false negative).

Gate status at review time: Isartor 204/204 (`Legacy_1B`), veraPDF 569/569 (`PDFA_1B`),
convert corpus 510/510, all five checks enabled in both profiles except
`KeyIntroducedAfterPDF14` (`Legacy_1B` only).

## Implementation progress

The four improvement items are being implemented one stage per commit, every stage gated on
the full test suite plus all three corpora (Isartor, veraPDF, convert). Determinism (review
item 2) went first, because the profile-gating stage's equivalence test is only reliable
once schema typing no longer depends on map iteration order.

### Stage 1 — deterministic schema typing (review item 2) ✅ 2026-07-08

`verifyDocument`'s walk no longer lets the first arrival at a shared node decide its schema
coverage:

- **Sorted key iteration** (`sortedKeys`, `internal/verify/verifier.go`): dict entries are
  walked in sorted order, so walk order — and with it finding order and `CurrentPage`
  attribution for shared objects — is independent of Go map iteration order.
- **Type-aware re-descent** (`typedVisit`): `visited` still gates the per-node PDF/A checks
  (exactly once per node, as before), but schema validation is deduped per *(node, Arlington
  type)*. A node first reached untyped (or as type A) is re-descended when later reached as
  type B: schema checks and child-type propagation run under the new type, while scalar
  children — fully checked on the first visit — are skipped (`isContainer`) so no duplicate
  6.1.x findings can arise. Cycle-safe: each (node, type) pair is entered at most once.

Regression tests (`checks_objectmodel_test.go`): untyped-path-first still schema-checks the
typed re-descent; a dict shared between two differently-typed edges is checked under both
types; a doubly-referenced typed node yields exactly one finding. All gates green
(full suite, Isartor 204/204, veraPDF 569/569, convert corpus). `BenchmarkOpenVerify`
magnitudes unchanged (large ≈ 310ms).

### Stage 2 — profile-gated check families (review item 1) ✅ 2026-07-08

`VerifyObjectModel` no longer pays full PDF/A verification cost:

- `Profile.OnlyObjectModelChecks()` (`internal/pdf/profile.go`) reports whether a profile
  enables nothing outside the objmodel clause (now the exported `pdf.ObjectModelClause`
  constant instead of a string literal).
- When it does, `verifyPdfA1b` skips every PDF/A-specific family up front instead of
  filtering its findings away at the end: header/trailer/xref checks, the info dictionary,
  `ComputeContentUsage` (content-stream decoding), `computeColourCoverage`, and everything
  after the walk (optional content, output intents, forms, XMP, object framing). Inside the
  walk, a `ctx.schemaOnly` flag suppresses the per-dict PDF/A validators (font parsing,
  content streams, ...), the 6.1.x scalar/limit checks, and scalar descent entirely — only
  schema validation and container descent remain.
- The gate is profile-driven, not level-driven, but deliberately coarse: it only fires for
  objmodel-only profiles. Finer per-family gating (e.g. a structure-only profile skipping
  font parsing) would need a family→check matrix and wasn't worth the risk yet.

Measured (5 iterations, in-process): the ~203KB font-heavy corpus file drops 7.7ms → 0.37ms
(~20×); the 3.9MB 40k-object file drops 314ms → 195ms (the rest is graph resolution, which
schema checks genuinely need). Guarded by `TestVerifyObjectModelMatchesFilteredFullRun`
(`verifier_test.go`), which asserts the fast path's findings are identical to a full
PDF/A-1b run filtered to the objmodel clause across all 204 Isartor files — this equivalence
is exactly what stage 1's determinism made testable. All gates green.

### Stage 3 — array-shaped types validated (review item 3) ✅ 2026-07-08

`validateArrayAgainstSchema` (`checks_objectmodel.go`), called from the walk's array case
under the same per-(node, type) dedup as dicts, closes the 25%-of-the-model gap:

- **Fixed-index rows** (`"0"`, `"1"`, ...): a required index beyond the array's length is a
  `MissingRequiredKey`; a present element must match the row's types (`WrongValueType`),
  enumerated values (`DisallowedValue`), and indirect-reference requirement
  (`IndirectRequired`).
- **Wildcard rows** govern every element without a fixed row, with the same three
  element-level checks — e.g. `ArrayOfPageTreeNodeKids` requires indirect dictionary kids,
  `ArrayOfNumbersGeneral` (font `Widths`) requires numbers.
- Violations are reported against the *owner* (nearest enclosing dict), following the walk's
  existing convention for values that carry no `_ref` of their own, so fixers can resolve
  them. Predicated rows are skipped and PDF null matches everything, exactly as in the dict
  path — the shared `matchesValueType`/`scalarEnumString` helpers keep the two in lockstep.
- `KeyIntroducedAfterPDF14` is deliberately not applied to arrays: post-1.4 index additions
  are rare and `Post14Keys` is key-name-based; erring to false negatives.

Landed corpus-clean on the first run (Isartor, veraPDF, convert corpus, full suite) — no
false-positive traps surfaced beyond those the dict path had already codified. Known
remaining niche: `ValuePointer` of a zero-length array is not unique, so two distinct empty
arrays sharing a pointer dedupe as one node per type (pre-existing `visited` behavior, now
merely per-type); harmless for these checks since an empty array yields at most
`MissingRequiredKey`, which both would report identically.

### Stage 4 — wildcard dict-row constraints (review item 4) ✅ 2026-07-08

`validateAgainstSchema` now enforces the wildcard row on dictionary entries, not just uses
it for `Post14Keys` suppression and child typing:

- Every key without an explicit named row is checked against the `*` row's `Types`
  (`WrongValueType`), `PossibleValues` (`DisallowedValue`), and `IndirectReference`
  (`IndirectRequired`) — e.g. entries of a resource `XObject` map must be indirect streams,
  `ColorSpaceMap` name entries must be one of the device colour spaces. Same exemptions as
  named rows: `_ref` skipped, `Length` on streams skipped, predicated wildcard rows skipped.
- **Typing fix found while landing this**: a `LinkGroup` with nil `ValueTypes` (the key's
  only Type alternative) previously matched any value kind; `resolveLinkGroups` now falls
  back to the key's declared `Types`, so a mis-shaped value (a stream where a dictionary is
  declared) never inherits a wrong-shaped schema.
- **Convert-side trap**: `DocInfo`'s wildcard types every custom Info key as a text string,
  and real-world producers park integers/names there (e.g. `/SPDF`). A non-string custom
  value has no faithful coercion, so `normalizeInfoDict` (`internal/convert/fixups_xmp.go`)
  drops it.
- New public convenience `Document.IsPDF()` — `VerifyObjectModel` + `Valid`, the
  object-model counterpart to `IsPDFA()`.
- `KeyIntroducedAfterPDF14` is unaffected: wildcard types still skip it (arbitrary keys are
  never "introduced"), and custom keys on non-wildcard types are never in `Post14Keys`.

All gates green (full suite, Isartor 204/204, veraPDF 569/569, convert corpus 510/510).

### Housekeeping — review item 9 ✅ 2026-07-08

`IndirectForbidden` is commented as reserved for the predicate evaluator (all
`fn:MustBeDirect` rows are predicated today). `SinceVersion`/`DeprecatedIn` stay in the
generated table, now commented as deliberate groundwork for the evaluator's version-gate
families. `DisallowedValue`'s check description states the name/integer-enum limitation.
A wildcard-dict-enum test restores `checks_objectmodel.go` to 100% statement coverage.

### Stage B1 — version-gate predicate folding + per-column predication (review item 5, first increment) ✅ 2026-07-08

The predicate evaluator's foundation, entirely at codegen time — no runtime evaluator
exists yet, because every version-gate predicate constant-folds against the model's pinned
PDF 1.4 baseline (`modelVersion`, `gen.go`):

- **Expression folder** (`foldVersionExpr` and helpers in `gen.go`): recursive-descent
  folding of `fn:SinceVersion`/`fn:BeforeVersion`/`fn:IsPDFVersion` (with or without
  payloads), `fn:Not`, parentheses, and `||`/`&&` with decisive-operand short-circuiting
  (`fn:SinceVersion(2.0, <runtime payload>)` folds to false at 1.4 — the payload can no
  longer matter). Anything else stays unresolved. This parser is the compilation
  infrastructure the later runtime families (B2–B4) plug into.
- **Folds applied**: `fn:IsRequired(<pure version expr>)` → `Required` true/false
  (~27 rows, e.g. `XObjectFormType1.Matrix` required only before 1.3);
  `fn:MustBeIndirect(<version expr>)` → `IndirectRequired`/`IndirectEither`
  (9 rows: `Catalog` `Dests`/`Outlines`/`Threads`, four font `FontDescriptor`s,
  `FileTrailer.Info` all fold to must-be-indirect at 1.4); version-gated `PossibleValues`
  entries fold entry-wise (`fn:Deprecated`/`fn:Extension` values stay legal —
  false-negative direction; `fn:SinceVersion(1.5,...)` values drop out, e.g.
  `EncryptionStandard.V` now enforces `0..3`).
- **Per-column predication** (`arlington.Predication` replacing the row-level bool): each
  of Required/Types/Values/Indirect carries its own flag, and every check skips exactly
  its own column — a row whose Required condition is runtime-only still gets its type,
  enum, and indirection checks (e.g. `FileTrailer.Info`'s fold lands even though its
  Required column stays predicated; `GraphicsStateParameter.LW`'s type check now runs
  despite the `fn:Eval` range in its PossibleValues).
- **`*` enum entries fixed**: a literal `*` in PossibleValues means "any value", so such
  lists are no longer emitted — this removed a latent false positive on rows like
  `ActionNamed.N` whose list was already being enforced with `*` as a literal.
- **Third real convert bug caught**: the Isartor checkbox fixture converts with a *direct*
  `FontDescriptor`, which ISO 32000 requires to be indirect. Fixed generically by the new
  `indirectRequiredFixer` (`internal/convert/fixups_objectmodel.go`): any direct dict
  under a key the model requires indirect gets its own object number, so the writer hoists
  it — promotion is always conformance-neutral, since no enforced row demands directness.
- Classification: 87.3% → 89.3% simple rows; `TestClassificationFloor` raised 0.85 → 0.88.
  `TestVersionGateFolding` pins representative folds and still-predicated runtime rows.

All gates green (full suite, Isartor 204/204, veraPDF 569/569, convert corpus 510/510);
`BenchmarkOpenVerify` magnitudes unchanged (folding costs nothing at runtime).

### Stage B2 — compiled fn:IsRequired conditions (review item 5, second increment) ✅ 2026-07-08

The first *runtime* predicate family: `fn:IsRequired(cond)` rows whose condition only
touches the owning dict's own entries now compile to a `RequiredWhen *arlington.Cond` tree
(20 rows) instead of staying predicated:

- **Codegen** (`compileCond`, `gen.go`, superseding B1's `foldVersionExpr` as the general
  engine): sibling presence (`fn:IsPresent(Key)`), comparisons (`@Key==literal`,
  `@Key!=literal`), `fn:Not`, `||`/`&&`, and B1's version gates folding inline —
  `fn:IsPresent(EF) || fn:SinceVersion(2.0, ...) || fn:IsPresent(RF)` compiles to
  `Or(Present EF, Present RF)`. Cross-object paths (`::`, `parent::`) and domain
  predicates (`fn:NotStandard14Font`, ...) stay predicated. Decisive constants settle a
  boolean even when a sibling operand is uncompilable.
- **Runtime** (`evalCond`, `checks_objectmodel.go`): evaluates the tree against the dict,
  fail-closed — a present-but-non-scalar comparison operand makes the condition unknown
  and the requirement is skipped; an absent/null sibling is a *definite* state (`Present`
  false, `==` false, `!=` true). Only `MissingRequiredKey` consumes it; the array path
  ignores `RequiredWhen` (no compiled array rows exist).
- Newly live requirements include `PageObject.Parent` (when `@Type!=Template`),
  `FileTrailer.ID` (when `Encrypt` present), `EncryptionStandard.OE/UE/Perms` (when
  `@R==5||6`), `FileSpecification.F/EF/Type`, `Page/XObjectForm*.LastModified` (when
  `PieceInfo` present), `ActionLaunch.F`. Corpus-clean on the first run; one verify
  fixture needed a `Parent` it had genuinely omitted.
- Classification: 89.3% → 90.2% simple rows; floor raised 0.88 → 0.89.
  `TestCompiledConditions` pins representative trees, `TestEvalCond` the evaluator's
  tri-state semantics.

All gates green; `BenchmarkOpenVerify` magnitudes unchanged (the tree is a few pointer
hops per conditionally-required key, no allocations).

### Stage B3 — compiled fn:Eval value-range constraints (review item 5, third increment) ✅ 2026-07-08

Whole-column `fn:Eval` range constraints in PossibleValues — 62 named dict keys, e.g.
`GraphicsStateParameter.CA` ∈ [0,1], `FontDescriptor.Descent` ≤ 0, `DecodeParms.Columns`
≥ 0 — now compile to `ValueCond *arlington.Cond` and are enforced as `DisallowedValue`
("outside its legal range"):

- **Ordering operators**: `Cond` gains `CondLt/Le/Gt/Ge` (numeric comparison of the key's
  value against a literal); `compileCond` parses `>=`, `<=`, `>`, `<` alongside `==`/`!=`
  and unwraps nested `fn:Eval`. The condition references the key itself as a sibling
  (`@CA` inside the `CA` row), so `evalCond` needed no new operand concept.
- **Vacuous version gates**: a payload gate whose version test fails (e.g.
  `fn:BeforeVersion(1.3, fn:Eval(@Colors<=4))` at 1.4) is *neutral in its boolean
  context* — dropped from `&&`/`||` — not false; folding it to false would have made
  `(@Colors>=1) && fn:BeforeVersion(1.3,...)` unsatisfiable and flagged every legal value.
  B1/B2's existing folds were audited against this rule: all of them sat in contexts where
  false and neutral coincide (Required top level, `||` operands), so none change.
- **Fail-closed boundaries**: multi-group columns (per-type-alternative constraints,
  `[fn:Eval(@Page>=0)];[]`) are not compiled — the matching alternative is unknown at
  runtime; wildcard/fixed-index rows are not compiled (`@0` references an array element,
  not a sibling); `mod`, `fn:ArrayLength`, `fn:FileSize` operands stay predicated. At
  runtime an absent or non-numeric operand makes the condition unknown → no flag (the
  wrong-shape case is `WrongValueType`'s business).
- Classification: 90.2% → 93.2% simple rows; floor raised 0.89 → 0.92.

All gates green on the first corpus run; coverage held (verify 93.5%, arlington 100%).

### Stage B4 — fn:MustBeDirect: closed as unenforceable ✅ 2026-07-08

Surveying the 30 `fn:MustBeDirect` rows before building the check showed **every one has a
scalar or array value type** (trailer/linearization integers and names, signature strings
and arrays) — and indirection is only observable on dicts/streams, which carry the
resolver's `_ref` marker; a resolved indirect scalar is indistinguishable from a direct
one. The check would never fire. Decision: no `DirectRequired` check, no fixer; the rows
stay `Predicated.Indirect` (honest classification), and their *other* columns are already
enforced thanks to B1's per-column predication (e.g. `Signature.SubFilter`'s enum,
`LinearizationParameterDict`'s integer types). `IndirectForbidden` stays reserved with a
comment recording this analysis. This is a permanent, documented false-negative class —
same standing as the array-indirect-required niche in the maintainer reference — unless
the resolver ever learns to mark scalar indirection, which its perf constraints argue
against.

### Stage C — type re-anchoring + array-first-element discrimination (review items 6+7) ✅ 2026-07-09

Two recovery mechanisms for descents that lost their schema type, both data-driven and
fail-closed:

- **Self-identification table** (`writeSelfIdentified` in `gen.go` →
  `arlington.SelfIdentified`): each type claims every (`/Type`, `/Subtype`) value pair its
  enumerated PossibleValues allow — multi-value rows (`PageObject`'s `[Page,Template]`)
  claim each value, so overlapping claims collide and are dropped; a bare (`Type`, `""`)
  claim survives only when no other type constrains that `Type` value at all. 49
  unambiguous pairs are generated; the four `FontDescriptor*` types and the `XObject`
  subtypes collide away as designed. The walk's dict case re-anchors an untyped dict via
  `selfIdentifiedType` before schema validation.
- **Array-index discriminators** (`resolveLinkGroups`): `bestDiscriminator` already emitted
  fixed index `"0"` with a `ByValue` map for colour-space-shaped candidate groups; the
  runtime side now resolves a numeric discriminator against the array element (e.g.
  `[/ICCBased ...]` → `ICCBasedColorSpace`, 22 groups in the model). Out-of-range index or
  non-scalar element fails closed.

**The re-anchor was the first time page subtrees got typed at all**: `PageTreeNode.Kids`
links to `[PageTreeNode, PageObject]`, and `PageObject`'s multi-value `/Type` means no
discriminator, so every page arrived untyped until `/Type /Page` self-identified it. That
newly exposed three Arlington-vs-validator conflicts on the pass corpora, resolved as:

- **`requiredOverrides` in `gen.go`** — a documented codegen exception list for Required
  rows no PDF/A validator enforces: `XObjectFormType1.Resources` (spec itself says
  "optional but strongly recommended"), `PageObject.LastModified` (required-if-PieceInfo
  in ISO 32000, universally omitted), `StructElem.P` (same standing). Overriding at
  generation keeps the runtime clean and the decision greppable.
- **Rounding tolerance in `evalCond` ordering ops** — real files carry `/CA 1.0000001`;
  veraPDF accepts it as 1.0. Values within 1e-5 of the bound compare equal, matching the
  tolerance the hand-written transparency checks already use.
- **`descentSignFixer` in convert** (`fixups_objectmodel.go`) — a real catch, not a false
  positive: fonts that store `/Descent` as a magnitude (`205`) violate ISO 32000's
  non-positive requirement; the fixer negates it in place. Third real conformance defect
  this integration has surfaced in convert's output.

Gates: full suite green, Isartor 204/204, veraPDF 569/569 (0 false positives), convert
corpus 510/510; coverage arlington 100%, verify 93.5%, convert 91.7%. Benchstat: geomean
+1.4% (noise); the 10k-page `large` fixture is +14% (300ms → 343ms, p=0.002) — the honest
price of schema-validating ten thousand pages that previously went entirely unchecked.

### Stage D1 — array-element value conditions ✅ 2026-07-09

B3 deliberately refused to compile fixed-index `fn:Eval` constraints because `@0` references
an array element, not a sibling key. This stage lifts exactly that restriction — 27 rows,
e.g. `WhitepointArray` X/Z > 0, `GammaArray` ≥ 0, RGB components ∈ [0,1],
`IndexedColorSpace` hival ∈ [0,255] — classification 93.2% → 94.6% (floor 0.92 → 0.93):

- **Codegen** (`gen.go`): `splitComparison` accepts all-digit `@N` operands alongside sibling
  names; `condOperandsResolvable` then validates the compiled tree against its row's kind —
  a named dict row may only reference sibling keys, a fixed-index row only element indices,
  and wildcard/offset-wildcard rows (`*`, `1*`) resolve nothing. `RequiredWhen` stays
  dict-rows-only (the array path ignores it). Element-vs-element comparisons
  (`LabRangeArray`'s `@0<=@1` — RHS is not a literal) and multi-group columns
  (`Dest*Array`, `ArrayOfDuration.0`) stay predicated, fail closed.
- **Runtime** (`checks_objectmodel.go`): `evalCond` is now a thin wrapper over a generic
  `evalCondOn[S condOperands]` shared with the new `evalCondArray` — dict lookup by key,
  array lookup by index (out-of-range = definite absence), one copy of the tri-state
  semantics, no interface boxing, no allocations. The fixed-index loop in
  `validateArrayAgainstSchema` enforces a row's `ValueCond` as `DisallowedValue` against the
  owner dict; a null element is never range-checked (null matches everything, trap #1).

All gates green on the first corpus run (full suite, Isartor 204/204, veraPDF 569/569,
convert corpus 510/510); coverage arlington 100%, verify 93.6%, convert 91.7%;
`BenchmarkOpenVerify` magnitudes unchanged.
