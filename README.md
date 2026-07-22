# gopdfrab

[![codecov](https://codecov.io/gh/voidrab/gopdfrab/branch/main/graph/badge.svg)](https://codecov.io/gh/voidrab/gopdfrab)
[![Go Reference](https://pkg.go.dev/badge/github.com/voidrab/gopdfrab.svg)](https://pkg.go.dev/github.com/voidrab/gopdfrab)
[![Mentioned in Awesome Go](https://awesome.re/mentioned-badge.svg)](https://github.com/avelino/awesome-go)

PDF/A processing for go!

Verify and convert PDF documents with a small, predictable open source library.

## Status

PDF/A-1b verification and conversion are implemented and tested against the full
Isartor and veraPDF conformance suites (see [Performance](#performance) and
[Fuzzing & Stress Testing](#fuzzing--stress-testing)). The project is **pre-1.0**:
the API is still being refined and may change until the first tagged release —
[CHANGELOG.md](CHANGELOG.md) states the versioning and stability policy. To report
a vulnerability, see [SECURITY.md](SECURITY.md).

## Features

- PDF structural integrity verification (Arlington model)
- PDF/A-1b verification
- PDF/A-1b conversion
- Decryption of encrypted PDFs (RC4 40/128, AES-128, AES-256), with the empty or
  a supplied user/owner password

## Roadmap

PDF/A-1b is the current focus and the target for the 1.0 release. The detailed,
up-to-date roadmap — including what remains before the API is frozen — lives in
[roadmap.md](roadmap.md). PDF/A-2, -3 and -4 come after 1.0.

## Getting Started

A full example can be found under `main/main.go`

### Add gopdfrab

```bash
go get github.com/voidrab/gopdfrab
```

### Import gopdfrab

```go
import (
  "github.com/voidrab/gopdfrab"
)
```

### Initialize a Document

```go
doc, err := gopdfrab.Open(path)
if err != nil {
  log.Fatal(err)
}
```

### Encrypted PDFs

Encrypted documents are decrypted transparently on open when they use the empty
user password — the common permission-only case. Supply a user or owner password
explicitly with `OpenWithPassword`:

```go
doc, err := gopdfrab.OpenWithPassword(path, []byte("secret"))
if errors.Is(err, gopdfrab.ErrPasswordRequired) {
  log.Fatal("a correct password is required to open this file")
}
```

`Verify` and `Convert` decrypt the same way. A file that genuinely needs a
password they don't have is reported with `ErrPasswordRequired` rather than
producing a broken result.

### PDF/A Validation

```go
v, err := doc.Verify(gopdfrab.PDFA1B)
if err != nil {
  log.Println(err)
}

if v.Valid {
  fmt.Println("Document is PDF/A-1b compliant")
} else {
  fmt.Println("Document is not PDF/A-1b compliant")
  fmt.Println("Issues:")
  for i, v := range v.Issues {
    fmt.Printf("#%v: %v\n", i+1, v)
  }
}
```

Finally, close doc.

```go
doc.Close()
```

### Verify a File

`Verify` opens, verifies, and closes a file.

```go
result, err := gopdfrab.Verify(path, gopdfrab.PDFA1B)
if err != nil {
    log.Fatal(err)
}
fmt.Println(result.Valid)
```

### Verifying In-Memory Data

`VerifyBytes` is `Verify` for an in-memory PDF.

```go
result, err := gopdfrab.VerifyBytes(data, gopdfrab.PDFA1B)
```

### Verifying Multiple Files

`VerifyAll` opens, verifies, and closes a batch of files concurrently.

```go
results, err := gopdfrab.VerifyAll(paths, gopdfrab.PDFA1B)
if err != nil {
    log.Fatal(err)
}
for _, r := range results {
    if r.Err != nil {
        log.Println(r.Path, r.Err)
        continue
    }
    fmt.Println(r.Path, r.Result.Valid)
}
```

### Typed Errors

Open, verify and convert failures can be matched with `errors.Is` instead of
inspecting message text:

| Error | Meaning |
|---|---|
| `gopdfrab.ErrNotPDF` | the input is not a PDF (no `%PDF-` header) |
| `gopdfrab.ErrDamaged` | a PDF whose cross-reference or trailer structure could not be parsed |
| `gopdfrab.ErrEncrypted` | an encryption scheme gopdfrab does not implement |
| `gopdfrab.ErrPasswordRequired` | a correct password is required to open the file |

### Inspecting Issues

Each `PDFError` in `v.Issues` exposes the `Check` that flagged it, along with its page and underlying messages.

```go
for _, issue := range v.Issues {
    c := issue.Check()
    fmt.Println(c.Clause(), c.Subclause(), c.Name(), c.Description())
    fmt.Println(issue.Page(), issue.Messages())
}
```

`Result` has helpers for grouping and summarizing issues:

```go
fmt.Println(v.Summary())          // human-readable report, one line per Check
v.Checks()                        // distinct Checks violated, sorted by clause
v.IssuesByCheck()                 // map[Check][]PDFError
v.IssuesOnPage(1)                 // issues found on page 1 (0 = document-level)
```

`Result`, `PDFError` and `Check` marshal to a stable JSON shape, for CLI, service, or CI integration:

```go
b, _ := json.Marshal(v)
// {"type":"A-1b","valid":false,"issueCount":2,"issues":[{"check":{"name":"...","clause":"6.1.3",...},"page":0,"documentLevel":true,"messages":["..."],"text":"..."}]}
```

### Document Helpers

```go
ok, err := doc.IsPDFA()                      // shorthand for Verify(PDFA1B).Valid

ok, err := doc.IsPDF()                       // shorthand for VerifyObjectModel().Valid

part, level, err := doc.ClaimedConformance() // e.g. "1", "B" — what the file claims, not whether it's valid

n, err := doc.PageCount()                    // number of pages

version, err := doc.Version()                // PDF version from the header, e.g. "1.7"

info, err := doc.Metadata()                  // Info dictionary entries (Title, Author, ...)

xmp, err := doc.XMPMetadata()                // raw XMP packet bytes, decoded to UTF-8
```

### Converting to PDF/A

`Convert` produces a PDF/A conformant rewrite. It runs pre-emptive fixups, then a verify/fix loop, and rasterizes pages as a last resort when no in-place fixer can repair them.

```go
cr, err := gopdfrab.Convert(path, gopdfrab.PDFA1B)
if err != nil {
    log.Fatal(err)
}

if err := cr.Save("out.pdf"); err != nil {
    log.Fatal(err)
}

fmt.Println(cr.Iterations)      // how many verify/fixup passes it took
fmt.Println(cr.Result.Valid)    // true if the output is fully PDF/A conformant
```

`cr.Save(path)` writes the output to a file; `cr.WriteTo(w)` streams it to any `io.Writer` (it implements `io.WriterTo`). Both error when there is no output.

```go
_, err := cr.WriteTo(w) // e.g. an http.ResponseWriter or a bytes.Buffer
```

### Converting an Open Document

```go
cr, err := doc.Convert(gopdfrab.PDFA1B)
```

### Converting In-Memory Data

`ConvertBytes` is `Convert` for an in-memory PDF.

```go
cr, err := gopdfrab.ConvertBytes(data, gopdfrab.PDFA1B)
```

### Converting Multiple Files

`ConvertAll` opens, converts, and closes a batch of files concurrently.

```go
results, err := gopdfrab.ConvertAll(paths, gopdfrab.PDFA1B)
if err != nil {
    log.Fatal(err)
}
for _, r := range results {
    if r.Err != nil {
        log.Println(r.Path, r.Err)
        continue
    }
    fmt.Println(r.Path, r.Result.Result.Valid) // r.Result is a ConvertResult
}
```

### Inspecting Residuals

Even though `Convert` always returns its best attempt, the result may still carry residual issues if no automatic remediation — including the raster last resort — fully resolved them.

```go
residual := cr.Residual()
for _, iss := range residual {
    check := iss.Check()
    fmt.Println(check.Clause(), check.Name())
    fmt.Println(iss.Page(), iss.Messages())
}
```

## Selective Check Profiles

Verification can be narrowed to a specific set of rules using `Verify`.

### Start from the full profile and remove checks

```go
p := gopdfrab.PDFA1B.
    RemoveCheck(gopdfrab.Checks.Structure.FileHeaderSignature).
    RemoveCheck(gopdfrab.Checks.Font.SimpleNotEmbedded)

res, err := doc.Verify(p)
```

### Start from an empty profile and add checks

```go
p := gopdfrab.PDFA1B.Clear().
    AddCheck(
        gopdfrab.Checks.Transparency.ImageWithSoftMask,
        gopdfrab.Checks.Metadata.PDFAIdentifierMissing,
    )

res, err := doc.Verify(p)
```

### Available check groups

| Registry field | Spec area |
|---|---|
| `Checks.Structure` | 6.1.x — file header, trailer, xref, object framing, limits |
| `Checks.Colour` | 6.2.2 OutputIntent, 6.2.3.x device colours, 6.2.9–10 |
| `Checks.Image` | 6.2.4–6.2.7 image/form/PostScript XObjects |
| `Checks.Transparency` | 6.2.8 transfer functions, 6.4 soft masks/blend modes/alpha |
| `Checks.Font` | 6.3.x embedding, subsets, metrics, encoding |
| `Checks.Annotation` | 6.5.x annotation types and dictionaries |
| `Checks.Action` | 6.6.x action types and additional actions |
| `Checks.Metadata` | 6.7.x XMP metadata, extension schemas, PDF/A identifier |
| `Checks.Form` | 6.9 interactive forms |
| `Checks.ObjectModel` | Generic ISO 32000 object-model conformance, independent of PDF/A — see below |

Use `gopdfrab.AllChecks()` to enumerate all registered checks with their names, descriptions, and clause numbers. `gopdfrab.CheckByClause("6.3.4", 1)` and `gopdfrab.ChecksForClause("6.3.4")` look up checks by clause directly.

## PDF Object-Model Conformance

`Checks.ObjectModel` holds six checks — `MissingRequiredKey`, `WrongValueType`, `DisallowedValue`, `IndirectRequired`, `KeyIntroducedAfterPDF14`, `ConstraintViolated` — derived from the [Arlington PDF Model](https://github.com/pdf-association/arlington-pdf-model), the machine-readable ISO 32000 object model. They answer "is this even valid PDF," independent of any PDF/A conformance level.

```go
res, err := gopdfrab.VerifyObjectModel(path)
```

`VerifyObjectModelBytes` is the in-memory equivalent, and `doc.VerifyObjectModel()` runs it on an already-open `Document`:

```go
res, err := gopdfrab.VerifyObjectModelBytes(data)
res, err := doc.VerifyObjectModel()
```

These are shorthand for `Verify`/`VerifyBytes`/`doc.Verify` with `gopdfrab.ObjectModelOnly()`, a profile enabling only the six checks above:

```go
res, err := doc.Verify(gopdfrab.ObjectModelOnly())
```

`ConvertObjectModel` is the conversion counterpart: it produces a rewrite repaired against the object-model checks only, applying every fix that is safe and semantics-preserving and reporting anything else as a residual.

```go
cr, err := gopdfrab.ConvertObjectModel(path)
cr, err := gopdfrab.ConvertObjectModelBytes(data)
cr, err := doc.ConvertObjectModel()
```

## Performance

gopdfrab's PDF/A-1b verification performance is (unfairly) measured against the Java-based [veraPDF](https://verapdf.org/) and [PDFBox Preflight](https://pdfbox.apache.org/) on the combined Isartor + veraPDF corpora (773 files); see `benchmarks/README.md` for methodology.

| Benchmark | gopdfrab vs veraPDF | gopdfrab vs PDFBox Preflight |
|---|---|---|
| Startup time | 149x faster | 22x faster |
| Single file (cold, median file) | 160x faster | 50x faster |
| Batch throughput | 20x faster | 15x faster |
| Batch peak memory | 11x smaller | 15x smaller |
| Deployment footprint | 9x smaller | 3x smaller |

Absolute numbers from the same run: the full 773-file batch verifies in 0.29 s (2749 files/s) at 67 MB peak RSS, and a cold single-file verification of the median corpus file takes ~5 ms including process startup.

Due to JVM startup overhead, startup time and cold single-file verification are significantly slower for veraPDF and Preflight.

## Isartor Compatibility

The Isartor test suite is the old reference test suite for PDF/A-1b document compatibility before the veraPDF project was initiated.
If you require PDF/A-1b compatibility based on Isartor for your application, use the `Legacy1B` profile.

## Fuzzing & Stress Testing

Because gopdfrab's whole job is to read untrusted, frequently-malformed PDFs, the
`internal/pdfgen` package programmatically generates "crazy, broken" PDF documents
— structurally-valid skeletons deliberately corrupted with truncation, bad
cross-reference offsets, negative stream lengths, dangling and circular
references, deep nesting, and more. Everything is generated in memory from a seed
(no external document files), so any crash is reproducible from its seed alone via
`pdfgen.Generate(seed)`.

The generator also builds fresh random object graphs from a small PDF grammar
(`pdfgen.GenerateGrammar`) to reach shapes that corrupting a fixed seed never
produces.

These inputs drive native Go fuzz targets at three levels:

- **Whole pipeline** — `FuzzOpenBytes`/`FuzzLexer` (parser), `FuzzVerifyBytes`,
  `FuzzConvertBytes`, `FuzzConvertRoundTrip`, and `FuzzGeneratedSeed` (which lets
  the fuzzer explore the generator's own seed space under coverage guidance).
- **Isolated subsystems** — the decoders and parsers that whole-file fuzzing only
  reaches shallowly: `FuzzDecodeStream`, `FuzzInflateZlib`, `FuzzDecodeASCIIHex`,
  `FuzzDecodeASCII85`, `FuzzDecodeLZW`, `FuzzDecodeCCITT`, `FuzzUndoPredictor`,
  `FuzzTokenizeContent`, `FuzzParseFunction`, `FuzzResolveColor`, and the writer
  targets (`FuzzWritePDF`, `FuzzWriteContentStream`, `FuzzBuildInlineImageBytes`).
- **Semantic oracles** — beyond "does not panic": `FuzzVerifyDeterministic` and
  `FuzzConvertDeterministic` (repeat runs must match byte-for-byte),
  `FuzzConvertHonest` (a conversion reported valid must independently re-verify as
  valid), and `FuzzConvertConverges`.

Every target seeds its corpus in code, so the generated broken PDFs replay on
every `go test` run; `TestGeneratedCorpusDoesNotPanic` additionally drives a
deterministic batch through the public API on every build, and named
`TestCrasher_*` reproducers guard each previously-fixed crash. Concurrency and
resource bounds are covered by `TestGeneratedCorpusRace` /
`TestConcurrentDecodeIsSafe` (run under `-race`) and `TestGeneratedCorpusTimeBounded`.

To actively hunt for new crashes locally:

```sh
go test -run '^$' -fuzz=FuzzOpenBytes        -fuzztime=60s ./internal/pdf/
go test -run '^$' -fuzz=FuzzParseFunction    -fuzztime=60s ./internal/pdf/
go test -run '^$' -fuzz=FuzzConvertRoundTrip -fuzztime=60s .
go test -race -run 'TestGeneratedCorpusRace|TestConcurrentDecodeIsSafe' ./... 
```

## Security

gopdfrab parses untrusted, frequently-hostile input by design. To report a
suspected vulnerability, follow [SECURITY.md](SECURITY.md) — please do not open a
public issue for one.

## Licensing

This work is dual-licensed under GNU AGPL 3.0 and our commercial license.
[Get in touch](mailto:contact@voidrab.com) for more information about our commercial licensing options.

## Contributing

Contributions are welcome! Whether it's a bug report, a failing test file, a new check, or a performance improvement — all of it helps.

- **Bug reports & questions** — open an [issue](https://github.com/voidrab/gopdfrab/issues).
- **Code changes** — fork the repo, make your change on a feature branch, and open a pull request. Please keep pull requests focused: one concern per PR.
- **New checks** — if you have a PDF document that contains properties which aren't covered yet, open an issue first so we can agree on the approach before you write the code.
- **Test files** — if you have PDFs that expose edge cases or regressions, attach them to an issue.

All contributions are made under the [AGPL](LICENSE).
