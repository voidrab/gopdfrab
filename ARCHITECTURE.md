# Architecture

A map of the codebase, so you can guess which file to open. If you want to know
how to *change* something, read [CONTRIBUTING.md](CONTRIBUTING.md) after this.

## The shape of it

```
open file → parse → resolve object graph → run checks → issues
                                             ↓
                                        pick fixers → apply → re-verify → write
```

Verification answers a question. Conversion is verification in a loop with a
repair step, and a writer at the end.

Everything is one Go module with no dependencies except
`klauspost/compress`. Six internal packages do the work; the root package is a
thin facade over them.

| Package | What it does |
|---|---|
| `internal/pdf` | Parser, object model, filters, decryption, and the check registry |
| `internal/verify` | The PDF/A-1b checks |
| `internal/convert` | Fixers, the fix loop, and the rasterizer fallback |
| `internal/writer` | Serializing a graph back to PDF bytes |
| `internal/arlington` | The Arlington PDF Model tables, for the object-model checks |
| `internal/pdfgen` | Generating PDFs, used by tests and fuzzing |

`gopdfrab.go` at the root is the public API: `Verify`, `Convert`, `Open` and
their `Bytes`, `All`, `Each` and `Context` variants, plus `Document` and
`Options`. It contains almost no logic — it re-exports types and calls into the
internals. `cmd/gopdfrab` is the CLI, `wasm/main.go` the browser build.

## internal/pdf — the parser and the object model

The bottom layer. Nothing above it touches bytes.

- `lexer.go`, `tokens.go`, `cursor.go` — tokenizing.
- `parser.go`, `document.go`, `resolver.go` — parsing objects and building the
  cross-reference. `xrefstream.go` and `objstm.go` handle the compressed forms.
- `types.go`, `values.go`, `dict.go` — the object model. `PDFDict`, `PDFName`,
  `PDFRef` and friends. A PDF null is Go `nil` throughout.
- `stream.go`, `filters.go` and one file per codec: `lzw.go`, `ccitt.go`,
  `runlength.go`, `predictor.go`.
- `crypt.go` — the standard security handler, RC4 and AES. Decryption happens in
  one place, so nothing further up has to know a file was encrypted.
- `mmap_unix.go`, `mmap_other.go` — files are memory-mapped, never read whole
  into the heap. Arbitrarily large PDFs have to work.
- `limits.go`, `footprint.go` — resource caps and how much memory a document is
  holding.
- `colorspace.go`, `content.go`, `pdffunc.go`, `pdffunc_ps.go`, `xmp.go` —
  reading the structures the checks ask about.
- `errors.go`, `results.go`, `json.go` — `PDFError`, `Result`, and their JSON
  form.

Reading is tolerant by design. A single unparseable object degrades to null and
is reported, rather than failing the whole document — a verifier that cannot
open a broken file cannot tell you what is wrong with it.

## internal/pdf/checks_catalog.go — the check registry

The one file worth knowing by name. Every rule gopdfrab enforces is registered
here as a `Check`:

- a `name` (`"OptionalContent"`),
- a `description`, which is what a user reads when their file fails,
- a `clause` and a `subclause` — the PDF/A-1 clause number and a sub-rule index
  within it,
- an `id`, assigned at registration, used as a compact key.

There are 159 checks in 11 groups: `Structure`, `Colour`, `Image`,
`Transparency`, `Font`, `LogicalStructure`, `Annotation`, `Action`, `Metadata`,
`Form` and `ObjectModel`. `pdf.Checks` is the registry value, so a check is
written `pdf.Checks.Structure.OptionalContent`.

`profile.go` turns the registry into a selection. A `Profile` carries a
conformance level and the set of enabled check IDs, so a caller can verify
against everything, against one clause, or against the object model alone.

## internal/verify — the checks

`verifier.go` holds the entry point and the order of play. `verifyPdfA1bParts`
resolves the object graph once, builds a page index, and then runs each family
of checks against that shared graph. Its result splits three ways:

- **PreStructural** — byte-level checks on the file header, trailer and
  cross-reference table.
- **Graph** — everything that is a function of the resolved objects.
- **PostStructural** — object framing and the parser's own diagnostics.

The split exists for conversion: after a fix pass, the graph half can be
replayed without re-reading the file.

`ValidationContext` (`context.go`) is what a check writes to. `ctx.Report(check,
object, message)` records an issue. The context also carries the results of a
content-usage scan — which XObjects are reachable, which glyphs are actually
drawn — so checks can skip things the document never shows. Every such
suppression is switched off when the scan could not complete, so an
undecodable content stream makes the verifier check more, not less.

The check files roughly follow the groups. To find where a check is reported,
grep for its name — `pdf.Checks.Colour.DeviceColourSpaceUsage`, say — but this
is the shape:

| Group | Reported from |
|---|---|
| Structure | `verifier.go`, and a few from most other files |
| Colour | `checks_colour.go` |
| Image | `checks_content.go` |
| Font | `checks_font.go`, `checks_font_program.go` |
| Metadata | `checks_xmp.go` |
| Annotation, Action, Form, Transparency | `checks_dict.go` |
| ObjectModel | `checks_objectmodel.go` |

`LogicalStructure` is the exception: all five of its checks are PDF/A-1**a**
only, so nothing reports them under the 1b profile. They are registered and
selectable, which is why `AllChecks()` returns 159 and the README's table of ten
groups covers 154.

Supporting files: `cff_widths.go` and `encodings_symbol.go` parse embedded font
programs, which is where the fiddliest checks live.

## internal/convert — fixers and the fix loop

`convert.go`'s `RunContext` is the loop:

1. Apply pre-emptive fixups — repairs that must happen before the first
   verification.
2. Verify the in-heap graph.
3. If it is valid, stop.
4. If the set of violations is identical to the previous iteration, stop; no
   progress is being made.
5. For each violation, find the registered fixer and apply it.
6. Go back to 2, up to the iteration cap.
7. Serialize and verify the output bytes.

`convert_fixers.go` defines the contract. A `Fixer` has `Applies(check) bool`
and `Fix(trailer, issues) (changed, error)`, and registers itself with
`registerFixer`. One check gets at most one fixer; a second registration panics
at startup. Two optional capabilities exist for speed: a `batchDictFixer` is a
per-dictionary edit, and all of them share a single graph walk per pass; a
`targetedFixer` can jump straight to the objects its issues name instead of
walking at all. `convert_fixpass.go` drives both.

The fixers themselves are `fixups_*.go`, named after what they repair —
`fixups_colour.go`, `fixups_font.go`, `fixups_xmp.go`, `fixups_annot.go` and so
on. A fixer must be idempotent, because the loop will run it again.

### The rasterizer

`raster*.go` is the last resort: content that cannot be made conformant any
other way is drawn to an image and the image is embedded. It is a full renderer
— paths, shadings, patterns, Type 3 fonts, glyph outlines — because it has to
reproduce what the page looked like. It is lossy, so a conversion reports how
much of the output got there by rastering.

`fidelity.go` guards against the opposite failure: a conversion that produces a
perfectly conformant blank page. It renders input and output and compares them.

`spill.go` keeps large output in a temp file instead of the heap, which is why
`ConvertResult.Output()` is a method that returns an error and why the result
has a `Close`.

## internal/writer

`writer.go` serializes a graph back to PDF bytes: numbering objects, writing the
cross-reference table, and keeping output deterministic — the same input must
produce the same bytes every time, or the corpus gates cannot tell a real change
from noise. `content_writer.go` writes content streams.

## internal/arlington

`model_gen.go` is the generated Arlington PDF Model: for every object type in
ISO 32000, which keys are required, what types they may hold, and from which PDF
version they exist. It drives the `ObjectModel` check group, which asks "is this
even valid PDF?" — a question orthogonal to the PDF/A restrictions. Do not edit
`model_gen.go` by hand; `gen.go` produces it.

## Tests

Tests sit next to the code. What is unusual is the volume of real input: four
corpora under `tests/`, from the Isartor and veraPDF suites up to 1585
real-world documents. [CONTRIBUTING.md](CONTRIBUTING.md) covers what they are
and how to get them. The corpus sweeps run as ordinary Go tests, skip when the
files are absent, and are what actually keeps the verifier honest.
