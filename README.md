# pdfrab for Go

PDF/A processing for go

## Goals

- Verification of PDF/A compliance for files
- Conversion of files to reach PDF/A compliance

## Current Progress

### PDF/A-1b Validation

PDF/A-1b (ISO 19005-1:2005) verification is implemented and passes the full [Isartor test suite](https://www.pdfa.org/resource/isartor-test-suite/) — **204/204** negative test files correctly detected by their intended clause.

| Clause | Area | Status |
|--------|------|--------|
| 6.1.2 | File header | ✓ |
| 6.1.3 | File trailer | ✓ |
| 6.1.4 | Cross-reference table | ✓ |
| 6.1.5 | Document information dictionary | ✓ |
| 6.1.6 | Metadata streams | ✓ |
| 6.1.7 | Stream objects | ✓ |
| 6.1.8 | Indirect objects | ✓ |
| 6.1.10 | Filters | ✓ |
| 6.1.11 | Character encoding | ✓ |
| 6.1.12 | Architectural limits | ✓ |
| 6.1.13 | Colours | ✓ |
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

## Getting Started

A full example can be found under `main/main.go`

### Import pdfrab

Import pdfrab for your go project.

```go
import (
  pdfrab "github.com/voidrab/gopdfrab"
)
```

### Initialize Document

```go
doc, err := pdfrab.Open(path)
if err != nil {
  log.Fatal(err)
}
```

### PDF/A Validation

```go
v, err := doc.Verify(pdfrab.A1_B)
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
