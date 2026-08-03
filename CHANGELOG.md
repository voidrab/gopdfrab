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

First changelog entry; earlier history lives in the git log.

### Added
- `OpenBytes`/`OpenBytesWithPassword` open a `*Document` from an in-memory PDF,
  so the document helpers (`PageCount`, `Version`, `ClaimedConformance`,
  `Metadata`, `XMPMetadata`) are reachable without a file on disk.
- Content a file draws at zero opacity is taken out rather than repainted
  opaque, and `ConvertResult.OverpaintedPages` reports any page drawn over.
- `ConvertResult.LostObjects` reports content a conversion could not carry over
  as its own fact, so a valid output is no longer reported as invalid for it.
- `ICCBasedProfileInvalid` rejects an ICCBased colour space whose embedded ICC
  profile is a version or kind PDF/A-1 does not allow, and conversion replaces
  it; output-intent profiles are now held to the same clause's narrower set.
- **Breaking:** `ConvertResult.Output` is now `Output() ([]byte, error)`; large
  output spills to a temp file, and a new `Close()` releases it.
- `Limits.MaxResidentBytes` and `--max-resident-mb` cap the caches one open
  document keeps; a conversion releases them when it finishes.
- `Limits`/`SetLimits`/`CurrentLimits`/`DefaultLimits` and `--max-decoded-mb`
  make the decompression-bomb cap configurable.
- `ConvertEach`/`ConvertEachContext` pass each batch result to a callback as it
  completes; `Options.Workers` bounds the concurrency of both batch forms.
- `…Context` counterparts of every `Verify`/`Convert` entry point, taking a
  `context.Context` and an `Options` value.
- `Options.CheckFidelity` populates `ConvertResult.Fidelity` with per-page
  similarity and ink coverage.
- `ConvertResult.RasterizedPages` lists the pages a conversion rebuilt as a flat
  image; `RasterDrops` lists content the rasterizer could not draw.
- `ConvertResult.WriteTo(io.Writer)` streams the converted PDF to any sink.
- `ErrNotPDF`, `ErrDamaged`, `ErrEncrypted`, `ErrPasswordRequired` and
  `ErrUnresolvableGraph` sentinels, matchable with `errors.Is`.
- `Check`, `PDFError` and `Result` implement `MarshalJSON`.
- Standard security handler decryption (RC4 40/128, AES-128, AES-256), with
  `OpenWithPassword`/`OpenBytesWithPassword` for non-empty passwords.
- Cross-reference recovery: a missing or unusable `startxref` rebuilds the table
  by a full-file scan instead of failing the open.
- Per-object recovery: an object that fails to parse at its recorded offset is
  re-located or resolved to null, and reported.
- Command-line tool `cmd/gopdfrab`: `verify`/`convert`, `--json`, recursive
  input, exit codes 0/1/2, SIGINT cancellation.
- Package documentation (`doc.go`) and runnable examples.
- A real-world corpus of test documents.

### Changed
- **Breaking:** renamed for Go convention — `A_1B` → `A1B`, `PDFA_1B` →
  `PDFA1B`, `Legacy_1B` → `Legacy1B`, `GetPageCount`/`GetVersion`/`GetMetadata`
  → `PageCount`/`Version`/`Metadata`.
- Content-stream tokenization streams past `Limits.MaxResidentBytes` rather than
  materializing first, and the default budget drops from 256 MB to 64 MB.
- The raster fallback draws inline images, all seven shading types, shading
  patterns and Type 3 glyphs, and honours text render mode and rise.
- Convert refuses a file that requires a password with `ErrPasswordRequired`
  instead of emitting undecryptable streams.
- The concurrency contract of each public type is documented and race-tested.

### Fixed
- Eight repairs that emptied a page or drew over one: a group form rasterized
  against the wrong resources, in the wrong colour, or at a size nothing could
  decode; a soft mask composited over white rather than turned into a stencil
  `/Mask`; a partial opacity made opaque; and a drawing begun in no colour
  space (6.4).
- An out-of-range coordinate (6.1.12) is rescaled into the CTM rather than
  clamped, which emptied pages drawn at a small scale while still verifying
  clean; `ConvertResult.BlankedPages` reports any page that comes out empty.
- A cross-reference table with bare CR line endings was unparseable; CR, LF and
  CRLF are all accepted now, on both read paths.
- An object listed in the xref but defined nowhere in the file is resolved to
  null instead of reported as lost content.
- The Info/XMP `Author` sync check (6.1.5, 6.7.3) rejected values matching its
  own, over undecoded XML entities and one-sided whitespace trimming.
- The TrueType CIDSet check (6.3.5) demanded glyphs the document never renders,
  rejecting real PDF/A; it is scoped to rendered CIDs now.
- The PDF/A identifier was recognised only in double-quoted XMP attributes, so
  every Ghostscript-produced PDF/A was reported as missing it (6.7.11).
- A TrueType glyph with an empty outline and a non-zero advance — the space in
  any monospaced subset — was reported as missing (6.3.5).
- A transparency group in an annotation appearance stream was unreachable by the
  fixer, so no digitally signed document could be converted (6.4).
- `/EncryptMetadata false` was not honoured, degrading the metadata stream to
  null.
- A character code absent from a non-symbolic TrueType's (3,1) cmap skipped the
  (1,0) lookup ISO 32000-1 9.6.6.4 prescribes next.
- The simple-font coverage check and the `/XObject` resource walk both took map
  order, naming a different character code and `RasterDrops` order per run.
- A `/Resources` dictionary pruned to the 4095-entry limit dropped entries in map
  order, so a conversion was not byte-reproducible.
- Two verifier false-negatives caught against the veraPDF binary: a referenced
  PostScript XObject (6.2.7), and a non-embedded font used only by a tiling
  pattern (6.3.4).
- Two verifier messages named an arbitrary map element (6.3.5 CharSet, 6.5.3
  appearance entry) instead of a sorted one.
- A fill under a `/Pattern` colour space painted flat black, and an `/ImageMask`
  ignored the fill colour, both without any report.
- Undecodable content streams are reported (`StreamUndecodable`) rather than
  turning a violation into a pass.
- A single bad cross-reference offset suppressed unrelated checks; verification
  now degrades per object.
- `Convert` could return empty output with a nil error.
- `InflateZlib` truncated silently past the 256 MB cap instead of returning
  `ErrOutputTooLarge`.
- The Windows seek/ReadAt read path is now exercised on any OS and asserted to
  match the mmap path over both corpora.
