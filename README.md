# pdfrab for Go

PDF/A processing for go

## Goals

- Verification of PDF/A compliance for files
- Conversion of files to reach PDF/A compliance

## Current Progress

- Working on PDF/A-1b validation

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
