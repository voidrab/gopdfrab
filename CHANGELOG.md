# Changelog

All notable changes to gopdfrab are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html) from 1.0
onward.

## Versioning and stability

**From 1.0, the stability guarantee covers the public packages only** —
`github.com/voidrab/gopdfrab` and, from 1.3.0, its verification-only subset
`github.com/voidrab/gopdfrab/verify`. Everything under `internal/` is implementation
detail, is not importable by external code, and may change in any release without
notice.

- A **breaking change** is any change to a public package that requires a
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

### Added

- `CheckByClauseIn`, `ChecksForClauseIn`, `Check.ClauseIn` and
  `Check.AppliesTo` look checks up by clause within a conformance level.
- `Profile.Name`, `ProfileByName` and `Profiles` name the ready-made
  profiles (`pdfa1b`, `legacy1b`, `pdf`) and look them up by name.
- `github.com/voidrab/gopdfrab/verify`: the verification API alone, under
  the same names and over the same types, for programs that never convert.
  It does not compile or link the converter.
- WebAssembly: a verify-only module, `./wasm/verify` (about 6.2 MB against
  the full module's 15.1 MB), so a page can load it first and fetch the full
  module only to convert. Both modules now say which one is ready:
  `{type: "ready", module: "verify" | "convert"}`.

### Changed

- A profile of the `ObjectModel` level that enables a PDF/A check is now
  rejected with an error naming the check, not verified by PDF/A-1b's rules.
- The Arlington object model behind the `ObjectModel` checks ships as 17.5 KB
  of embedded data decoded on first use, instead of a generated Go map literal
  that compiled to about 680 KB of startup code. Every binary is about 1 MB
  smaller (1.6 MB in WebAssembly), and a program that never runs the
  object-model checks no longer pays to build the table at startup.
- Conversion allocates less: on an object-heavy file, about a third fewer
  allocations and 28% fewer bytes, a 12% lower peak heap and about 15% less
  time. Verification allocates 6–14% less.
- Verify skips a family of checks, such as the font or XMP checks, when the
  profile enables none of its checks, rather than running it and filtering
  its findings out.
- A document-level issue from the Info dictionary, optional content, output
  intent, interactive form or XMP checks names the object it was found in,
  such as the Info dictionary, the output intent or the metadata stream.

### Deprecated

- `CheckByClause`, `ChecksForClause` and `Profile.Allows`, which read PDF/A-1
  clauses only. Use the level-qualified lookups and `Profile.Has`.

### Fixed

- Convert removed embedded files and forbidden annotations, file attachments
  among them, without listing them in `LostObjects`.
- Removing `/OCProperties` made the optional content layers that a document
  turns off visible in the output. Their content is now removed and listed in
  `LostObjects`, and the fidelity gate renders the input with those layers
  hidden.
- Convert rasterized a figure whose transparency group draws nested forms,
  as chart libraries write labels, even when nothing in it is transparent.
  Such a group is now dropped and the figure stays vector content.
- `ConvertObjectModel` and the `PDF` profile stamped the output as PDF/A-1b
  (`pdfaid` XMP, `GTS_PDFA1` OutputIntent, font repairs) without a PDF/A check.
- A violation repeated throughout a content stream is reported once per check,
  with a count, not once per operator, which could run verify out of memory.
- Convert rewrote a stream shared by several objects through only one of them,
  writing a corrupt stream into output it reported conformant.
- A font repair on an embedded program that several fonts share reached only
  one of them; the others kept the original bytes under the repaired
  `/Length1` and `/Filter`. Every font sharing the program now gets the repair.
- The 6.3.6 width check and its repair skipped Type 1 glyphs that set their
  width with `sbw`, and misread widths computed with `div`.
- Raster fallback: a TrueType contour made only of off-curve points, as
  round dots often are, was drawn with each curve bent toward the wrong
  control point.
- Raster fallback: simple fonts map codes to glyphs as verify does
  (ISO 32000-1 9.6.6). A Type 1 font without /Encoding uses its built-in
  encoding rather than StandardEncoding, a symbolic TrueType font resolves
  through its (3,0) cmap, and a Type 1C font finds its glyphs by name
  instead of taking the code as the glyph ID.
- The DeviceN colorant repair kept the colour space a q block set after its
  Q, and rewrote a later colour in the restored space as black.
- Transparency flattening carried the previous colour into a form drawn
  after a colour space operator, which starts the colour at black.
- Raster fallback: a colour space operator started the colour at black in
  every space; it starts at the space's initial colour, such as full tint
  in a Separation space. A cm or colour operator short of operands is
  skipped instead of setting a zero matrix or black.
- Repairs that read a page's content used only the page's own /Resources,
  not one inherited from the page tree. The resource pruning repair could
  then delete resources such a page uses, and the colour, DeviceN and
  resource-renaming repairs missed what it names.
- An issue after a link annotation was attributed to the link's destination
  page, so the raster fallback could rasterize the wrong page.
- A cross-reference stream whose /Length is wrong is read up to its
  endstream keyword, as other streams are, instead of being discarded so
  the object table had to be rebuilt by scanning the file.
- When the fix loop hit `MaxIterations`, the raster fallback rasterized pages
  the last fix pass had already repaired.
- An image claiming a size its data does not back could make the raster
  fallback allocate gigabytes; it is now an `oversized image` drop.
- Raster fallback: CFF and Type 1 glyphs drawn through subroutines, flex or
  `seac` lost segments, transparency groups were drawn at the group's opacity
  instead of composited once, and strokes thinner than a pixel were dropped.
- Transparency flattening: fills under 50% opacity vanished and lighter ones
  turned grey, the opacity repair ran first so overlapping fills washed out,
  forms on a page with its own group were left to a later pass and came out
  opaque, and nested forms raced between workers.
- A transparency group with only opaque graphics states was rasterized; it
  now stays vector.
- Verify reported Info/XMP Title and Subject mismatches in a random order.

## [1.2.0] - 2026-10-11

### Added

- CLI: `--detail` on `verify` and `convert` lists every location of an issue.
- CLI: `convert` reports the pages it rasterized and what it could not draw
  on them (`rasterized:` and `dropped:` lines, `rasterizedPages` and
  `rasterDrops` in `--json`). A conversion that turned an image into a gray
  box used to print only `result: conformant`.

### Changed

- CLI: issues are grouped into one line per check and message, with a count,
  the pages as ranges and the number of objects.
- CLI: the profile is named once in a `profile:` header instead of
  `PDF/A violation` on every issue line.
- CLI: the text output of `verify` and `convert` has a fixed, parseable layout,
  described in the README. `--json` keeps its existing fields.
- Convert repairs more pages in place instead of rasterizing them: graphics
  state nested past the 28-level limit, tiling patterns painting in a device
  colour space the output intent does not cover, inline images with an
  array-form LZW filter, and CID fonts whose text draws an unmapped CID the
  font has no glyph for.

### Fixed

- `PDFError.String` printed an issue's object as `ref &{4 0}`; it is `ref 4 0 R` now.
- A profile whose level is not `A1B` or `ObjectModel` (for example
  `NewProfile("A-2b")`, or a zero-value `Profile`) ran no checks and verified
  as `Valid`; Verify and Convert now return an error for it.
- JPEG 2000, JBIG2 and undecodable CCITT images painted as a gray box by the
  raster fallback are now reported in `RasterDrops`.
- 6.3.5: text after `Q`, or in a form XObject using its caller's font, was
  checked against the wrong font, giving false missing-glyph reports and
  needlessly rasterized pages.
- 6.3.5 missed used glyphs absent from a Type 1 subset when the font had no
  `/Encoding` (built-in encoding) or a hexadecimal `/CharSet`.
- 6.3.5/6.3.6: simple TrueType codes resolve through `/Encoding` and the
  (3,1) and (1,0) cmaps as ISO 32000-1 9.6.6.4 says, not by raw code, which
  both missed and invented findings.
- XMP: the `exif:Flash` field `Function` was rejected as undefined.
- Converted files that veraPDF or BFO rejected: CFF fonts with fractional
  default widths (including CID-keyed), Type 1 glyphs that take their width
  from a subroutine, substitute fonts without a glyph for code 160, and
  TrueType programs with a truncated empty-glyph record or wrong checksums.
- Raster fallback: `MacRomanEncoding` fonts drew wrong glyphs for 0x27, 0x60
  and codes above 0x7F, WinAnsi 0x9F drew ÿ instead of Ÿ, form XObjects were
  not clipped to their `/BBox`, and text ignored the clip.
- Encrypted files with an inconsistent `/V` and `/R` (V5 with R2–R4, R5/R6
  without V5) or a `/UE`/`/OE` that is not 32 bytes failed with `ErrInternal`;
  they now return `ErrEncrypted`.

## [1.1.0] - 2026-10-10

### Added

- `ErrInternal`: a panic inside gopdfrab is recovered at the public entry points
  and returned as an error wrapping `ErrInternal`, instead of crashing the
  caller's process. Panics in internal worker goroutines are carried back to the
  calling goroutine first.
- `VerifyAllContext` honours `Options.Workers` (0 still means
  `runtime.NumCPU`). It used to ignore the field, which was documented as
  applying to the batch convert entry points only.
- CLI: `--password` falls back to `$GOPDFRAB_PASSWORD`.

### Changed

- `ConvertResult.Save` is atomic: it writes a temp file beside the target and
  renames it into place, so a failed save no longer truncates an existing file
  (including the input, when converting in place). An existing file keeps its
  permissions, a new one gets 0644, and a symlink is written through.
- `VerifyContext` now polls the context during the object-graph walk instead of
  only at entry, and Convert's raster passes stop dispatching page renders once
  the context is cancelled.
- A stream that fails to decode is remembered per document, so a decompression
  bomb is inflated up to the size cap once rather than once per check and per
  convert iteration. Inflate buffers grown past 16 MB are no longer pooled.
- Damaged-file recovery scans for object headers and `xref` keywords without
  regular expressions: 4–7× faster on the recovery benchmark, and on the
  non-mmap read path it reads the file in 1 MB chunks instead of loading it
  whole.
- CLI: `convert` rejects an output path given both as `-o` and as a second
  argument, and rejects extra arguments, instead of ignoring them.

### Fixed

- 6.1.7/6.1.8 object- and stream-framing issues reported the object number as
  the issue's `Page()`. They are now document-level, with the object in
  `ObjectRef()`, and no longer steer which pages Convert's raster fallback
  targets.
- 6.1.12 `IndirectObjectsExceeded` now fires when a file has more than 8,388,607
  indirect objects. It used to fire on an empty cross-reference table instead.
- Object-framing diagnostics for objects outside the page graph, and trailer
  recovery, now resolve objects in object-number order, so `Issues` is the same
  on every run.
- A stream whose `/Length` is wrong by more than a few bytes is now recovered
  from its `endstream` keyword and reported under 6.1.7, instead of being
  dropped to null and its content lost.
- One malformed object inside an object stream no longer loses every other
  object in that stream, and a wrong cross-reference index into an object stream
  no longer returns a different object than the one requested.
- Encrypted files: revision 5 (AES-256, Adobe Extension Level 3) now
  authenticates. It was hashed with the revision 6 algorithm, so every
  password, including the correct one, was rejected. A V4 handler without a
  key length now defaults to 128 bits instead of 40. A string that fails to
  decrypt is kept as stored and reported against its object instead of
  passing silently.
- A document's own `/_ref` or `/_dirty` dictionary keys are kept. `/_ref`
  collided with an internal bookkeeping key (overwritten on indirect objects,
  and on a direct dictionary it made the writer treat that dictionary as an
  indirect object); `/_dirty` was dropped on write.
- The writer no longer emits `NaN` or `Inf` for a non-finite real (it writes
  `0`), and escapes whitespace and delimiters in names built by fixers as `#XX`
  instead of letting them break the token stream.
- Convert no longer leaves spilled temp files behind when the document writer or
  the final verify fails, or for `ConvertEach` results that complete after the
  callback has returned an error.
- A file whose structure fails to parse is now unmapped, not just closed.
- `Document.Close` can be called more than once; later calls return nil.
- Convert could report a non-conformant output as valid. A stream whose
  `/Filter` used an inline-image abbreviation (`/Fl`) had the filter deleted by
  the object-model repair, leaving its compressed bytes to be read as content,
  and the decode cache still answered with the old decoded bytes. Abbreviations
  are now expanded to the full filter name, the repair never deletes a key that
  says how stream bytes are read, and the decode cache is keyed on the filter
  chain as well as the bytes.

### Security

- Hostile input can no longer crash the process or exhaust memory through:
  - a stream `/Length` past the end of the file, which sized a heap allocation
    on the non-mmap (Windows) read path;
  - a cross-reference stream offset too large for `int64`, which wrapped
    negative and panicked the lexer;
  - a long run of comment lines, which grew the lexer's stack one frame per
    line;
  - a page tree whose `/Kids` lists a node itself or an ancestor, which sent
    Convert's page walk into unbounded recursion (a fatal stack overflow).
- The CLI's `--password` flag is visible to other users in `ps`. Its help text
  now says so and points at `$GOPDFRAB_PASSWORD`.

## [1.0.0] - 2026-08-26

First changelog entry; earlier history lives in the git log.

### Added
- Generic ISO 32000 object-model checks, driven by the Arlington model, in both
  verification and conversion: the `ObjectModel` level, the `PDF` profile,
  `NewProfile`/`ObjectModelOnly`, and `VerifyObjectModel`/`ConvertObjectModel`
  with their `Bytes` forms.
- Four checks that were declared but never reported now do, each with a fixer:
  `FontFileSubtype`, `FontBaseFont`, `XMPNoCorrespondingType` and
  `ICCBasedComponentsMismatch`.
- A WebAssembly build under `wasm/`, registering `gopdfrabVerify` and
  `gopdfrabConvert` as JavaScript globals.
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
- The declared minimum Go version is 1.24. It was a 1.26.4 patch-level
  directive, which made that exact patch the floor.
- The bundled one-component ICC profile is the CC0 sGrey v2 profile. The
  previous one was byte-identical to Ghostscript's `sgray.icc`, which is GPL,
  and conversion embeds it in the output. `NOTICE` names every bundled asset.
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
- Advance widths are scaled by a font program's declared `FontMatrix` instead of
  skipping the 6.3.6 check, and the Type1 width fixer no longer writes
  glyph-space numbers into `/Widths`.
- A page's `/Contents` array is scanned as one stream, so a font selected at the
  end of one part still applies to the text at the start of the next.
- Type1 programs: the program's own `/lenIV` and `-|` charstring operator are
  honoured, widths follow `callsubr`, and a built-in `/Encoding` built as an
  array is read, not just a named one (6.3.6).
- An advance width is compared as a real number rather than rounded to an
  integer first, which widened the 6.3.6 tolerance to +-1.5 units.
- A character code no cmap maps is reported whatever the descriptor's symbolic
  flag says (6.3.5).
- An annotation dictionary without `/Type` is checked and repaired; `/Type` is
  optional there (6.5.3).
- A forbidden action is taken off the entry that names it, instead of leaving an
  action dictionary with no `/S` (6.6.1).
- An `/Info` text string that does not round trip is written back, so `/Info` and
  the XMP packet built from it describe the same string (6.7.3).
- A TrueType glyph whose contour endpoints do not increase read past its point
  arrays, which crashed a rasterizer worker goroutine.
- A flattened form keeps what it did not paint, instead of covering the page
  with the unpainted part of its bounding box, and a soft mask too faint to
  threshold fades into the samples instead of masking the image out.
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
