# Changelog

All notable changes to gopdfrab are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html) from 1.0
onward.

## Versioning and stability

**Pre-1.0 (now): the API is not stable.** Anything may change between releases
while PDF/A-1b verification and conversion is hardened toward 1.0. Pin a version
if you depend on current behavior.

**From 1.0, the stability guarantee covers the root package only** —
`github.com/voidrab/gopdfrab`. Everything under `internal/` is implementation
detail, is not importable by external code, and may change in any release without
notice.

- A **breaking change** is any change to the root package that requires a
  consumer to edit their code to keep compiling, or that alters documented
  behavior of a public symbol. These happen only in a major version after 1.0.
- **Additions** (new functions, types, options) are minor releases.
- **Fixes** that do not change the public surface are patch releases. Note that a
  verifier or converter producing a *more correct* result for a given input is
  treated as a fix, not a breaking change, even if a caller was relying on the
  previous verdict.
- **Deprecation:** a symbol slated for removal is marked with a `// Deprecated:`
  comment naming its replacement, kept for at least one subsequent minor release,
  and removed no earlier than the next major version.

## [Unreleased]

This is the first changelog entry; earlier history lives in the git log. Recent
notable work:

- **Fidelity checking** for conversions. `Options.CheckFidelity` renders the
  input and the converted output with gopdfrab's own rasterizer and populates
  `ConvertResult.Fidelity` with a per-page `PageFidelity` (similarity, input/
  output ink coverage, and a `Blanked()` helper that flags destroyed content).
  A corpus gate asserts no conversion blanks a page.
- **Command-line tool** `cmd/gopdfrab` (built on the public API only): `verify`
  and `convert` subcommands, `--json` output, recursive directory input, exit
  codes 0/1/2 (conformant / non-conformant / error), `--profile`/`--password`/
  `--dpi`/`--max-iterations` flags, and SIGINT cancellation. Install with
  `go install github.com/voidrab/gopdfrab/cmd/gopdfrab@latest`. The old
  `internal`-importing `main/` example was removed.
- **Context-aware entry points with an `Options` struct.** Each `Verify`/
  `Convert` (and `Bytes`/`All`) function has a `…Context` counterpart taking a
  `context.Context` and an `Options{Password, RasterDPI, MaxIterations}` value
  (zero value = defaults). `ConvertContext` checks the context before each
  verify/fix iteration and each raster pass; `ConvertAllContext`/
  `VerifyAllContext` stop dispatching new files on cancellation. The
  two-argument forms are unchanged.
- Whole-table cross-reference **recovery**: a missing, non-numeric, or
  unlocatable `startxref` no longer fails the open. The object table is rebuilt
  by a full-file `N G obj` scan and the trailer is synthesized from a
  cross-reference stream or the document catalog, reported as a 6.1.4 issue. The
  scan is linear-time (`BenchmarkXRefRecovery`).
- Standard security handler **decryption**: RC4 40/128, AES-128 (R4) and AES-256
  (R6). Empty-password files decrypt automatically through `Open`/`OpenBytes`;
  `OpenWithPassword` / `OpenBytesWithPassword` take an explicit user or owner
  password.
- Typed, `errors.Is`-matchable sentinels on the root package: `ErrNotPDF`,
  `ErrDamaged`, `ErrEncrypted` and `ErrPasswordRequired`. `Open`/`Verify`/
  `Convert` classify open failures with these instead of message text.
- Stable JSON encoding of results: `Check`, `PDFError` and `Result` now implement
  `MarshalJSON`, so `json.Marshal` of a verify/convert result produces a
  documented shape instead of empty objects.
- `ConvertResult.WriteTo(io.Writer)` (implements `io.WriterTo`) to stream the
  converted PDF to any sink alongside the existing `Save(path)`.
- Per-object recovery of broken cross-reference offsets: an object that fails to
  parse at its recorded offset is re-located by scanning for its real
  `N G obj` header, or resolved to null when no intact copy exists. Both
  outcomes are reported as issues while every unrelated check keeps running.
- `ErrUnresolvableGraph` sentinel: `Convert` returns it (with the best-effort
  verify `Result` attached) when no object graph — and therefore no output —
  could be produced.

### Changed
- **Breaking:** renamed for Go convention (no underscores, no `Get` prefix).
  Constant `A_1B` → `A1B`; profile variables `PDFA_1B` → `PDFA1B`, `Legacy_1B` →
  `Legacy1B`; `Document`/`Reader` accessors `GetPageCount`/`GetVersion`/
  `GetMetadata` → `PageCount`/`Version`/`Metadata`. No aliases (pre-1.0).
- Convert refuses a file that genuinely requires a password with
  `ErrPasswordRequired` instead of emitting a document with undecryptable
  streams.

### Fixed
- Two verifier false-negatives found by cross-checking against the veraPDF
  binary over both conformance corpora: a referenced PostScript XObject (6.2.7)
  is now flagged under PDFA1B (reachability-gated like Form XObjects, rather
  than the whole check being disabled), and a non-embedded font shown only
  inside a tiling pattern (6.3.4) is no longer suppressed by
  `SkipUnusedSimpleFonts` — pattern content streams are now walked for usage.
  Convert neuters a referenced PostScript XObject into an empty Form XObject.
- Undecodable content streams are now reported (`StreamUndecodable`) rather than
  silently turning a violation into a pass.
- A single bad cross-reference offset no longer suppresses unrelated checks:
  verification degrades per object instead of abandoning whole check families,
  so a file with one broken offset reports the same issues as the intact file
  plus the recovery issue. Content-usage suppressions are discarded when any
  object degraded, and a verifier bail-out no longer drops the per-object parse
  diagnostics gathered before it.
- `Convert` can no longer return an empty output with a nil error: an
  unresolvable graph is `ErrUnresolvableGraph`, and a conversion that nulled an
  unrecoverable object reports the loss in `Residual()` with `Valid=false`.
- `InflateZlib` returns `ErrOutputTooLarge` when a stream would inflate past the
  256 MB cap, instead of silently truncating to a prefix that downstream checks
  then trusted as complete. The deliberate leniency toward truncated/CRC-broken
  streams (which still return their inflated prefix) is unchanged.
