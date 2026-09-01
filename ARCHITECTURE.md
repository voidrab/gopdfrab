# Architecture

This document provides a map of the codebase to help you navigate it more easily. If you want to know how to _change_ something, see [CONTRIBUTING.md](CONTRIBUTING.md).

## Overview

```text
open file → parse → resolve object graph → run checks → issues
                                             ↓
                                        pick fixers → apply → re-verify → write
```

Verification involves checking whether a document meets certain PDF/A criteria. Conversion involves extending the verification process to include a repair step, followed by a writer that produces the final output.

The entire project is a single Go module with no dependencies other than `klauspost/compress`. Six internal packages contain the implementation, while the root package provides a thin facade over them.

| Package              | What it does                                                                |
| -------------------- | --------------------------------------------------------------------------- |
| `internal/pdf`       | Provides the parser, object model, filters, decryption, and check registry. |
| `internal/verify`    | Contains the PDF/A-1b checks.                                               |
| `internal/convert`   | Contains the fixers, fix loop, and rasterizer fallback.                     |
| `internal/writer`    | Serializes the object graph back into PDF bytes.                            |
| `internal/arlington` | Contains the Arlington PDF Model tables used by the object-model checks.    |
| `internal/pdfgen`    | Generates PDFs for tests and fuzzing.                                       |

`gopdfrab.go` at the root defines the public API: `Verify`, `Convert`, and `Open`, together with their `Bytes`, `All`, `Each`, and `Context` variants, as well as `Document` and `Options`. It contains almost no implementation logic; instead, it re-exports types and delegates to the internal packages. `cmd/gopdfrab` provides the CLI, while `wasm/main.go` provides the browser build.

## internal/pdf — the parser and object model

This is the bottom layer of the stack. Nothing above it interacts directly with raw bytes.

- `lexer.go`, `tokens.go`, and `cursor.go` handle tokenization.
- `parser.go`, `document.go`, and `resolver.go` parse objects and build the cross-reference. `xrefstream.go` and `objstm.go` handle the compressed forms.
- `types.go`, `values.go`, and `dict.go` define the object model, including `PDFDict`, `PDFName`, `PDFRef`, and related types. A PDF null is represented as Go `nil` throughout.
- `stream.go`, `filters.go`, and the individual codec files—`lzw.go`, `ccitt.go`, `runlength.go`, and `predictor.go`—handle stream processing and decoding.
- `crypt.go` implements the standard security handler, including RC4 and AES. Decryption takes place in one location, so code further up the stack does not need to know whether a file was encrypted.
- `mmap_unix.go` and `mmap_other.go` provide memory mapping. Files are never read entirely into the heap, allowing the system to handle arbitrarily large PDFs.
- `limits.go` and `footprint.go` manage resource limits and track how much memory a document is using.
- `colorspace.go`, `content.go`, `pdffunc.go`, `pdffunc_ps.go`, and `xmp.go` read the structures required by the checks.
- `errors.go`, `results.go`, and `json.go` define `PDFError`, `Result`, and their JSON representations.

## internal/pdf/checks_catalog.go — the check registry

Every rule enforced by gopdfrab is registered in this registry as a `Check` containing:

- a `name`, such as `"OptionalContent"`;
- a `description`, which is shown to the user when the corresponding check fails;
- a `clause` and `subclause`, identifying the relevant PDF/A-1 clause and the sub-rule within it; and
- an `id`, assigned during registration and used as a compact key.

There are 159 checks, organized into 11 groups: `Structure`, `Colour`, `Image`, `Transparency`, `Font`, `LogicalStructure`, `Annotation`, `Action`, `Metadata`, `Form`, and `ObjectModel`. `pdf.Checks` is the registry value, so a check can be referenced as `pdf.Checks.Structure.OptionalContent`.

A `Profile` as found in `profile.go` contains a conformance level and a set of enabled check IDs, allowing a caller to verify a document against all checks, some checks, or only the object model.

## internal/verify — the checks

`verifier.go` contains the entry point and defines the order in which verification proceeds. `verifyPdfA1bParts` resolves the object graph once, builds a page index, and then runs each family of checks against that shared graph. Its results are divided into three categories:

- **PreStructural** — byte-level checks on the file header, trailer, and cross-reference table.
- **Graph** — checks that depend on the resolved objects.
- **PostStructural** — checks concerning object framing and the parser's own diagnostics.

This separation supports conversion: after a fix pass, the graph portion can be replayed without rereading the file.

`ValidationContext` (`context.go`) is the context to which checks report their findings. `ctx.Report(check, object, message)` records an issue. The context also contains the results of a content-usage scan, including which XObjects are reachable and which glyphs are actually drawn. This allows checks to ignore content that the document never displays.

The check files generally follow these groups. To find where a particular check is reported, search for its name—for example, `pdf.Checks.Colour.DeviceColourSpaceUsage`. The overall structure is as follows:

| Group                                  | Reported from                                        |
| -------------------------------------- | ---------------------------------------------------- |
| Structure                              | `verifier.go`, with a few checks in most other files |
| Colour                                 | `checks_colour.go`                                   |
| Image                                  | `checks_content.go`                                  |
| Font                                   | `checks_font.go`, `checks_font_program.go`           |
| Metadata                               | `checks_xmp.go`                                      |
| Annotation, Action, Form, Transparency | `checks_dict.go`                                     |
| ObjectModel                            | `checks_objectmodel.go`                              |

`LogicalStructure` is the exception. All five of its checks apply only to PDF/A-1**a**, so none of them are reported under the 1b profile. They are nevertheless registered and selectable, which is why `AllChecks()` returns 159 checks, while the README's table of ten groups contains 154.

The supporting files `cff_widths.go` and `encodings_symbol.go` parse embedded font programs. This is where some of the more intricate checks are implemented.

## internal/convert — fixers and the fix loop

`convert.go`'s `RunContext` implements the conversion loop:

1. Apply pre-emptive fixups—repairs that must be made before the first verification.
2. Verify the in-memory object graph.
3. If the document is valid, stop.
4. If the set of violations is identical to that of the previous iteration, stop because no further progress is being made.
5. For each violation, find the registered fixer and apply it.
6. Return to step 2, subject to the iteration limit.
7. Serialize the result and verify the output bytes.

`convert_fixers.go` defines the fixer contract. A `Fixer` provides `Applies(check) bool` and `Fix(trailer, issues) (changed, error)`, and registers itself through `registerFixer`. Each check can have at most one fixer.

Two optional capabilities improve performance. A `batchDictFixer` performs edits on a per-dictionary basis, with all such fixers sharing a single graph traversal per pass. A `targetedFixer` can jump directly to the objects named by its issues instead of traversing the graph. `convert_fixpass.go` coordinates both approaches.

The fixers themselves are defined in `fixups_*.go` files, named according to what they repair—for example, `fixups_colour.go`, `fixups_font.go`, `fixups_xmp.go`, and `fixups_annot.go`. Each fixer must be idempotent because the loop may invoke it more than once.

### The rasterizer

`raster*.go` provides the last-resort conversion mechanism. When content cannot be made conformant by any other means, it is rendered to an image and that image is embedded in the PDF.

The rasterizer is a full renderer. It handles paths, shadings, patterns, Type 3 fonts, glyph outlines, and other features because it must reproduce the visual appearance of the original page. Rasterization is lossy, so a conversion reports how much of the output was produced through rasterization.

`fidelity.go` guards against the opposite failure mode: producing a perfectly conformant but blank page. It renders both the input and output and compares them to ensure that the conversion has not lost the page's visual content.

`spill.go` keeps large output in a temporary file rather than the heap.

## internal/writer

`writer.go` serializes the object graph back into PDF bytes. It assigns object numbers, writes the cross-reference table, and keeps the output deterministic. `content_writer.go` handles the serialization of content streams.

## internal/arlington

`model_gen.go` contains the generated Arlington PDF Model. For every object type defined by ISO 32000, it specifies which keys are required, which types those keys may contain, and the PDF version in which they were introduced.

This model drives the `ObjectModel` check group, which asks whether the document is structurally valid PDF at all. Do not edit `model_gen.go` by hand as it is generated by `gen.go`.

## Tests

Tests are located alongside the code. There are three corpora under `tests/`, ranging from the Isartor and veraPDF suites to a collection of 1,585 real-world documents. [CONTRIBUTING.md](CONTRIBUTING.md) describes these corpora and explains how to work with them.
