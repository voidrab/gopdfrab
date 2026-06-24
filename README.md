# gopdfrab

PDF/A processing for go

## Goals

- Verification of PDF/A compliance for files
- Conversion of files to reach PDF/A compliance

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

### Add pdfrab

```bash
go get github.com/voidrab/gopdfrab
```

### Import pdfrab

```go
import (
  pdfrab "github.com/voidrab/gopdfrab"
)
```

### Initialize a Document

```go
doc, err := pdfrab.Open(path)
if err != nil {
  log.Fatal(err)
}
```

### PDF/A Validation

```go
v, err := doc.Verify(pdfrab.PDFA_1B)
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

```
doc.close()
```

### Verify a File

```go

`Verify` opens, verifies, and closes a  file.

result := pdfrab.Verify(path, pdfrab.PDFA_1B)
```

### Verifying Multiple Files

`VerifyAll` opens, verifies, and closes a batch of files concurrently.

```go
results := pdfrab.VerifyAll(paths, pdfrab.PDFA_1B)
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
cr, err := pdfrab.Convert(path, pdfrab.PDFA_1B)
if err != nil {
    log.Fatal(err)
}

if err := os.WriteFile("out.pdf", cr.Output, 0o644); err != nil {
    log.Fatal(err)
}

fmt.Println(cr.Iterations)      // how many verify/fixup passes it took
fmt.Println(cr.Result.Valid)    // true if the output is fully PDF/A conformant
```

### Converting an Open Document

```go
cr, err := doc.Convert(pdfrab.PDFA_1B)
```

### Converting In-Memory Data

`ConvertBytes` is `Convert` for an in-memory PDF.

```go
cr, err := pdfrab.ConvertBytes(data, pdfrab.PDFA_1B)
```

### Converting Multiple Files

`ConvertAll` opens, converts, and closes a batch of files concurrently.

```go
results := pdfrab.ConvertAll(paths, pdfrab.PDFA_1B)
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
    fmt.Println(Clause(), Name())
    fmt.Println(iss.Page(), iss.Messages())
}
```

## Selective Check Profiles

Verification can be narrowed to a specific set of rules using `Verify`.

### Start from the full profile and remove checks

```go
p := pdfrab.PDFA_1B.
    RemoveCheck(pdfrab.Checks.Structure.FileHeaderSignature).
    RemoveCheck(pdfrab.Checks.Font.SimpleNotEmbedded)

res, err := doc.Verify(p)
```

### Start from an empty profile and add checks

```go
p := pdfrab.PDFA_1B.Clear().
    AddCheck(
        pdfrab.Checks.Transparency.ImageWithSoftMask,
        pdfrab.Checks.Metadata.PDFAIdentifierMissing,
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

Use `pdfrab.AllChecks()` to enumerate all registered checks with their names, descriptions, and clause numbers. `pdfrab.CheckByClause("6.3.4", 1)` and `pdfrab.ChecksForClause("6.3.4")` look up checks by clause directly.

## Performance

gopdfrab's PDF/A-1b verification performance is (unfairily) measured against the Java-based [veraPDF](https://verapdf.org/) and [PDFBox Preflight](https://pdfbox.apache.org/) on the combined Isartor + veraPDF corpora (773 files); see `benchmarks/README.md` for methodology.

| Benchmark | gopdfrab vs veraPDF | gopdfrab vs PDFBox Preflight |
|---|---|---|
| Startup time | 222x faster | 32x faster |
| Single file throughput | 261x faster | 80x faster |
| Batch throughput | 16x faster | 12x faster |
| Batch peak memory | 13x smaller | 14x smaller |
| Binary size | 5x smaller | 4x smaller |

Due to JVM startup overhead, the startup time and single file verification throughput are significantly slower for veraPFD and Preflight.

## Isartor Compatibility

The Isartor test suite is the old reference test suite for PDF/A-1b document compatibility before the veraPDF project was initiated.
If you require PDF/A-1b compatibility based on Isartor for your application, use the `Legacy_1B` profile.
