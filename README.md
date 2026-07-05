<img width="1586" height="634" alt="gopdfrab-gopher" src="https://github.com/user-attachments/assets/837c019b-8c52-4d0f-bfab-9181911cff68" />

# gopdfrab

[![codecov](https://codecov.io/gh/voidrab/gopdfrab/branch/main/graph/badge.svg)](https://codecov.io/gh/voidrab/gopdfrab)
[![Go Reference](https://pkg.go.dev/badge/github.com/voidrab/gopdfrab.svg)](https://pkg.go.dev/github.com/voidrab/gopdfrab)
[![Mentioned in Awesome Go](https://awesome.re/mentioned-badge.svg)](https://github.com/avelino/awesome-go)

PDF/A processing for go!

Verify and convert PDF documents with a small, predictable open source library.

## Disclaimer

This project is at an early stage and under active development. The API is not final and will change heavily until the first proper release.

## Features

- PDF/A verification
- PDF/A conversion

## Current Progress

### PDF/A-1b Validation

PDF/A-1b (ISO 19005-1:2005) verification is implemented and passes both reference test corpora in full:

- [Isartor test suite](https://www.pdfa.org/resource/isartor-test-suite/) — **204/204** negative test files correctly detected by their intended clause.
- [veraPDF test suite](https://github.com/veraPDF/veraPDF-corpus) (PDF_A-1b) — **569/569** files verified correctly.

| Clause | Area | Status |
|--------|------|--------|
| 6.1.2 | File header | ✓ |
| 6.1.3 | File trailer | ✓ |
| 6.1.4 | Cross-reference table | ✓ |
| 6.1.5 | Document information dictionary | ✓ |
| 6.1.6 | String objects | ✓ |
| 6.1.7 | Stream objects | ✓ |
| 6.1.8 | Indirect objects | ✓ |
| 6.1.10 | Filters | ✓ |
| 6.1.11 | Embedded files | ✓ |
| 6.1.12 | Architectural limits | ✓ |
| 6.1.13 | Optional content | ✓ |
| 6.2.2 | Output intent | ✓ |
| 6.2.3 | Device colour spaces | ✓ |
| 6.2.4 | Image dictionaries | ✓ |
| 6.2.5–6.2.7 | XObjects | ✓ |
| 6.2.8 | ExtGState | ✓ |
| 6.2.9 | Rendering intent | ✓ |
| 6.2.10 | Operators | ✓ |
| 6.3.2 | Font programs | ✓ |
| 6.3.3 | Composite fonts | ✓ |
| 6.3.4 | Font embedding | ✓ |
| 6.3.5 | Font subsets | ✓ |
| 6.3.6 | Font metrics | ✓ |
| 6.3.7 | Font encoding | ✓ |
| 6.4 | Transparency | ✓ |
| 6.5 | Annotations | ✓ |
| 6.6 | Actions | ✓ |
| 6.7.2 | XMP metadata structure | ✓ |
| 6.7.3 | Info/XMP synchronisation | ✓ |
| 6.7.5 | XMP packet header | ✓ |
| 6.7.8 | Extension schemas | ✓ |
| 6.7.9 | XMP well-formedness | ✓ |
| 6.7.11 | PDF/A identifier | ✓ |
| 6.9 | Interactive forms | ✓ |

### PDF/A-1b Conversion

PDF/A-1b conversion is fully implemented. See [Converting to PDF/A](#converting-to-pdfa) below.

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

### PDF/A Validation

```go
v, err := doc.Verify(gopdfrab.PDFA_1B)
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
result, err := gopdfrab.Verify(path, gopdfrab.PDFA_1B)
if err != nil {
    log.Fatal(err)
}
fmt.Println(result.Valid)
```

### Verifying In-Memory Data

`VerifyBytes` is `Verify` for an in-memory PDF.

```go
result, err := gopdfrab.VerifyBytes(data, gopdfrab.PDFA_1B)
```

### Verifying Multiple Files

`VerifyAll` opens, verifies, and closes a batch of files concurrently.

```go
results, err := gopdfrab.VerifyAll(paths, gopdfrab.PDFA_1B)
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

### Document Helpers

```go
ok, err := doc.IsPDFA()           // shorthand for Verify(A_1B).Valid

part, level, err := doc.ClaimedConformance() // e.g. "1", "B" — what the file claims, not whether it's valid

xmp, err := doc.XMPMetadata()     // raw XMP packet bytes, decoded to UTF-8
```

### Converting to PDF/A

`Convert` produces a PDF/A conformant rewrite. It runs pre-emptive fixups, then a verify/fix loop, and rasterizes pages as a last resort when no in-place fixer can repair them.

```go
cr, err := gopdfrab.Convert(path, gopdfrab.PDFA_1B)
if err != nil {
    log.Fatal(err)
}

if err := cr.Save("out.pdf"); err != nil {
    log.Fatal(err)
}

fmt.Println(cr.Iterations)      // how many verify/fixup passes it took
fmt.Println(cr.Result.Valid)    // true if the output is fully PDF/A conformant
```

### Converting an Open Document

```go
cr, err := doc.Convert(gopdfrab.PDFA_1B)
```

### Converting In-Memory Data

`ConvertBytes` is `Convert` for an in-memory PDF.

```go
cr, err := gopdfrab.ConvertBytes(data, gopdfrab.PDFA_1B)
```

### Converting Multiple Files

`ConvertAll` opens, converts, and closes a batch of files concurrently.

```go
results, err := gopdfrab.ConvertAll(paths, gopdfrab.PDFA_1B)
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
p := gopdfrab.PDFA_1B.
    RemoveCheck(gopdfrab.Checks.Structure.FileHeaderSignature).
    RemoveCheck(gopdfrab.Checks.Font.SimpleNotEmbedded)

res, err := doc.Verify(p)
```

### Start from an empty profile and add checks

```go
p := gopdfrab.PDFA_1B.Clear().
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

Use `gopdfrab.AllChecks()` to enumerate all registered checks with their names, descriptions, and clause numbers. `gopdfrab.CheckByClause("6.3.4", 1)` and `gopdfrab.ChecksForClause("6.3.4")` look up checks by clause directly.

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
If you require PDF/A-1b compatibility based on Isartor for your application, use the `Legacy_1B` profile.

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
