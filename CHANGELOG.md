# Changelog

All notable changes to gopdfrab are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html) from 1.0.

## Versioning and stability

The stability guarantee covers the public packages only:
`github.com/voidrab/gopdfrab` and, from 1.3.0, `github.com/voidrab/gopdfrab/verify`.
Everything under `internal/` may change in any release.

- A **breaking change** needs a consumer to edit their code to keep compiling,
  or alters documented behavior of a public symbol. It happens only in a major
  version.
- **Additions** are minor releases; **fixes** that leave the public surface
  alone are patch releases. A more correct verdict is a fix.
- A symbol slated for removal is marked `// Deprecated:`, kept for at least one
  minor release, and removed no earlier than the next major version.

## [Unreleased]

## [1.6.0] - 2026-11-17

### Added

- The `gopdfrab_nofonts` build tag leaves the bundled substitute fonts (about
  3.1 MB) out of the binary. `SetFontSource` supplies them instead,
  `FontFiles` lists the files a source serves, and `PreloadFonts` fetches them
  all. A render leaves out glyphs whose font cannot be loaded; a conversion
  fails.
- `Page.Render` draws annotations through their normal appearance streams,
  skipping hidden, NoView, popup and switched-off optional content ones.

### Fixed

- Verify no longer rejects a value of a multi-type key, such as an integer
  /LineHeight, against the enumerated values of its other types.
- Convert removes a /Lang that is not a language tag and, from PDF/A-2, wraps
  kept object XMP in xpacket processing instructions.

## [1.5.2] - 2026-11-15

### Changed

- Opening a large document for viewing is much faster: `Document.Pages`,
  `Document.Page`, `Document.Metadata` and the other document helpers read
  only what they return, and objects in object streams are parsed on first
  use. On a 560-page tagged book, `Document.Pages` went from 0.71 s to 0.06 s.

### Fixed

- A damaged object outside the parts a call reads no longer makes it fail.

## [1.5.1] - 2026-11-15

### Changed

- The bundled substitute fonts are stored gzip-compressed and inflated on
  first use, making every binary about 2.8 MB smaller.

## [1.5.0] - 2026-11-15

### Added

- A page API for viewers: `Document.Pages` and `Document.Page`, and a `Page`
  with `Size`, `Rotation` and `Render` (`RenderOptions.Scale` and `Region`).
  `ErrPageRange` reports an index outside the document.
- `Page.Glyphs` and `Page.Links` give characters and links with their `Rect`s
  on the displayed page.

### Changed

- The renderer anti-aliases, strokes with caps, joins, miters and dashes,
  filters scaled images, and draws all text render modes and tiling and
  uncoloured patterns. Conversion's raster fallback and fidelity checks use it.
- Rendering is faster and leaner: images, pyramids and glyph outlines are
  cached between renders, and a render stops soon after its context is done.

### Fixed

- CFF and Type 1 outlines honour the FontMatrix, and CID-keyed CFF glyphs are
  found through the charset.
- On Windows, a file open in a `Reader` can be renamed or deleted, and
  `ConvertResult.Save` can write over it.

## [1.4.0] - 2026-11-09

### Added

- PDF/A-2b: the `A2B` level and `PDFA2B` profile (also in `gopdfrab/verify`,
  and `--profile pdfa2b`). A converted file declares PDF 1.7.
- `PDFError.Level`, `PDFError.Clause` and `Check.DescriptionIn`; issues and
  summaries are numbered by the clause of their level.
- Forty-five checks for rules only PDF/A-2 sets, across every check group,
  with metadata checked on an RDF model of the XMP and embedded files required
  to conform themselves.

### Changed

- The raster fallback draws blend modes and soft masks, and reports work-limit
  stops in `RasterDrops`.
- PDF/A-2b conversion converts embedded PDFs and removes non-conforming
  embedded files. Signatures are removed at every level. Both are reported in
  `LostObjects`.

### Fixed

- A font no fixer could embed rasterized every page, not only those showing it.
- Font, overprint, annotation and action checks and repairs, the newest
  revision of an incremental update, and several smaller parsing and XMP
  defects.

## [1.3.0] - 2026-10-31

### Added

- `github.com/voidrab/gopdfrab/verify`: the verification API alone, without
  the converter, and a matching verify-only WebAssembly module (6.2 MB
  against 15.1 MB).
- `CheckByClauseIn`, `ChecksForClauseIn`, `Check.ClauseIn`, `Check.AppliesTo`,
  `Profile.Name`, `ProfileByName` and `Profiles`.

### Changed

- Binaries are about 1 MB smaller: the Arlington model ships as embedded data.
- Conversion makes a third fewer allocations and runs about 15% faster on
  object-heavy files.
- Document-level issues name their object, and an `ObjectModel` profile that
  enables a PDF/A check is rejected.

### Deprecated

- `CheckByClause`, `ChecksForClause` and `Profile.Allows`; use the
  level-qualified lookups and `Profile.Has`.

### Fixed

- Convert removed embedded files, forbidden annotations and hidden layers
  without listing them in `LostObjects`.
- Convert could write a corrupt shared stream or font program into output it
  reported conformant, and `ConvertObjectModel` stamped output as PDF/A-1b.
- Fewer needless rasterizations, correct transparency flattening, inherited
  page resources honoured, and many raster fallback drawing fixes.
- A violation repeated throughout a content stream is reported once with a
  count, instead of once per operator.

## [1.2.0] - 2026-10-11

### Added

- CLI: `--detail` lists every location of an issue, and `convert` reports
  rasterized pages and what could not be drawn on them.

### Changed

- CLI: issues are grouped per check with counts and page ranges, under a
  `profile:` header, in a fixed, parseable text layout.
- Convert repairs more pages in place instead of rasterizing them.

### Fixed

- A profile with an unsupported level ran no checks and verified as `Valid`;
  it is now an error.
- `PDFError.String` printed the object as `ref &{4 0}`.
- Font checks (6.3.5, 6.3.6) read the wrong font after `Q` and in forms, and
  mapped TrueType codes incorrectly.
- Converted fonts that veraPDF or BFO rejected, raster fallback encoding and
  clipping errors, and inconsistent encryption dictionaries failing with
  `ErrInternal`.

## [1.1.0] - 2026-10-10

### Added

- `ErrInternal`: a panic inside gopdfrab is returned as an error instead of
  crashing the caller.
- `VerifyAllContext` honours `Options.Workers`, and `--password` falls back to
  `$GOPDFRAB_PASSWORD`.

### Changed

- `ConvertResult.Save` is atomic and keeps the target's permissions.
- `VerifyContext` and Convert's raster passes stop promptly on cancellation.
- Damaged-file recovery is 4–7× faster, and a failed stream decode is
  remembered per document.
- CLI: `convert` rejects conflicting or extra arguments.

### Fixed

- Object-framing issues reported object numbers as pages, 6.1.12 fired on the
  wrong condition, and some diagnostics came out in a random order.
- Streams with a wrong `/Length` and malformed objects in object streams lost
  content.
- Revision 5 encryption rejected every password.
- Convert could report non-conformant output as valid when a filter name was
  abbreviated, and left temp files behind on failure.
- The writer emitted `NaN` reals and unescaped names, and dropped a
  document's own `/_ref` and `/_dirty` keys.

### Security

- Hostile input could crash the process or exhaust memory through an oversized
  stream `/Length`, a huge xref offset, long comment runs or a cyclic page
  tree.
- The `--password` help text warns that the flag is visible in `ps`.

## [1.0.0] - 2026-08-26

First changelog entry; earlier history lives in the git log.

### Added

- Arlington-driven ISO 32000 object-model checks and repairs: the
  `ObjectModel` level, the `PDF` profile, `NewProfile`, `ObjectModelOnly`,
  `VerifyObjectModel` and `ConvertObjectModel`.
- `OpenBytes` and `OpenBytesWithPassword`, standard security handler
  decryption, and cross-reference and per-object recovery of damaged files.
- `…Context` forms of every entry point with `Options`, plus `ConvertEach`,
  `Options.Workers` and `Options.CheckFidelity`.
- `ConvertResult.LostObjects`, `RasterizedPages`, `RasterDrops`,
  `OverpaintedPages`, `BlankedPages`, `Fidelity` and `WriteTo`.
- `Limits`, `SetLimits`, `CurrentLimits` and `DefaultLimits`, with
  `--max-decoded-mb` and `--max-resident-mb`.
- `ErrNotPDF`, `ErrDamaged`, `ErrEncrypted`, `ErrPasswordRequired` and
  `ErrUnresolvableGraph`, and JSON marshalling for `Check`, `PDFError` and
  `Result`.
- A WebAssembly build, the `cmd/gopdfrab` CLI, package documentation and
  examples.

### Changed

- **Breaking:** `ConvertResult.Output` is now `Output() ([]byte, error)`,
  spilling large output to a temp file released by `Close()`.
- **Breaking:** renamed for Go convention: `A_1B` → `A1B`, `PDFA_1B` →
  `PDFA1B`, `Legacy_1B` → `Legacy1B`, and `GetPageCount`, `GetVersion` and
  `GetMetadata` → `PageCount`, `Version` and `Metadata`.
- The minimum Go version is 1.24, and the bundled gray ICC profile is the CC0
  sGrey profile.
- The raster fallback draws inline images, shadings and Type 3 glyphs, and
  Convert refuses password-protected files with `ErrPasswordRequired`.

### Fixed

- Many font, colour, annotation, action and metadata verdicts, several of
  them caught against veraPDF, and repairs that emptied or covered pages.
- Coordinates out of range are rescaled into the CTM instead of clamped.
- Output is byte-reproducible, and verifier messages are deterministic.
- `Convert` could return empty output with a nil error, and `InflateZlib`
  truncated silently past its cap.
