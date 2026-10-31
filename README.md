# gopdfrab

[![codecov](https://codecov.io/gh/voidrab/gopdfrab/branch/main/graph/badge.svg)](https://codecov.io/gh/voidrab/gopdfrab)
[![Go Reference](https://pkg.go.dev/badge/github.com/voidrab/gopdfrab.svg)](https://pkg.go.dev/github.com/voidrab/gopdfrab)
[![Mentioned in Awesome Go](https://awesome.re/mentioned-badge.svg)](https://github.com/avelino/awesome-go)

PDF/A processing for go!

Verify and convert PDF documents with a small, predictable open source library.

- PDF/A-1b verification and conversion, passing the full Isartor and veraPDF
  conformance suites
- PDF structural verification and repair against the Arlington object model
- Decryption of encrypted PDFs (RC4 40/128, AES-128, AES-256)
- A CLI and two WebAssembly builds

The API is **stable as of 1.0**; [CHANGELOG.md](CHANGELOG.md) states the
versioning policy. PDF/A-2, -3 and -4 come next.

## Command-line tool

```bash
go install github.com/voidrab/gopdfrab/cmd/gopdfrab@latest

gopdfrab verify docs/                     # verify every PDF under a directory
gopdfrab verify --json report.pdf         # machine-readable output
gopdfrab convert in.pdf out.pdf           # rewrite towards PDF/A-1b
gopdfrab convert --dpi 300 in.pdf         # tune the raster fallback
```

Subcommands are `verify`, `convert`, `version` and `help`. Exit codes are `0`
conformant, `1` non-conformant, `2` error. `verify` walks directories
recursively and prints a fixed layout that scripts can parse:

```text
profile: pdfa1b

FAIL  issues=974 distinct=36  docs/L34a.pdf
  6.1.2/5     count=1    pages=doc        objs=1    "ViewerPreferences entry /PrintScaling requires PDF 1.5 or later"
  6.2.3.3/2   count=149  pages=1-2        objs=127  "device colour (gray) used in content stream without matching OutputIntent"
  6.3.4/1     count=3    pages=doc,1      objs=3    "font Courier-Bold is not embedded"
  ...
  objmodel/1  count=3    pages=1-2        objs=0    "Appearance is missing required key \"N\""
PASS  docs/ok.pdf
ERROR docs/broken.pdf
  error "failed to parse structure: pdf: not a PDF document: no %PDF- header"

summary: pass=1 fail=1 error=1 files=3
```

- A file line starts with `PASS`, `FAIL` or `ERROR` and ends with the path.
  `FAIL` gives the number of issues and of issue lines.
- An indented issue line covers every report of one check with one message:
  clause[/subclause], `count=` reports, `pages=` as ranges (`doc` for
  document-level), `objs=` distinct objects, and the Go-quoted messages. Lines
  are sorted by clause, then message.
- `--detail` lists one line per page and object instead (`page=<n|doc>`,
  `obj=<num>.<gen>` or `obj=-`, `count=`). `--json` reports every occurrence,
  ungrouped.
- `convert` prints `profile:`, `input:`, `output:` and `iterations:`, then
  optionally `rasterized: pages=<ranges>`,
  `dropped: pages=<ranges> features=<n>` with one line per feature the
  rasterizer could not draw, and `lost: objects=<n> distinct=<n>`, and last
  `result: conformant` or `result: residual issues=<n> distinct=<n>` with
  issue lines beneath.

| Flag                | Meaning                                                                                 |
| ------------------- | --------------------------------------------------------------------------------------- |
| `--profile`         | `pdfa1b` (default), `legacy1b`, or `pdf`                                                |
| `--password`        | password for an encrypted input; visible in `ps`, so prefer `$GOPDFRAB_PASSWORD`        |
| `--max-decoded-mb`  | cap a single stream's decoded output in MB (0 = default 256)                            |
| `--max-resident-mb` | cap a document's rebuildable caches in MB (0 = default 64)                              |
| `--json`            | emit machine-readable JSON                                                              |
| `--detail`          | list every page and object an issue was found on                                        |
| `--dpi`             | `convert` only: raster fallback resolution (0 = default 150)                            |
| `--max-iterations`  | `convert` only: verify/fix loop bound (0 = default 4)                                   |
| `-o`                | `convert` only: output path (default: the input with a `.pdfa.pdf`/`.fixed.pdf` suffix) |

## Library

```bash
go get github.com/voidrab/gopdfrab
```

### Verifying

```go
res, err := gopdfrab.Verify(path, gopdfrab.PDFA1B)      // open, verify, close
res, err := gopdfrab.VerifyBytes(data, gopdfrab.PDFA1B) // in-memory PDF
if err != nil {
    log.Fatal(err)
}
if !res.Valid {
    for i, issue := range res.Issues {
        fmt.Printf("#%d: %v\n", i+1, issue)
    }
}
```

`VerifyAll(paths, profile)` verifies a batch concurrently and returns one
`FileResult` per path, each with `Path`, `Result` and `Err`.

To work with one file repeatedly, open it as a `Document`:

```go
doc, err := gopdfrab.Open(path) // or OpenBytes(data)
if err != nil {
    log.Fatal(err)
}
defer doc.Close()

res, err := doc.Verify(gopdfrab.PDFA1B)
ok, err := doc.IsPDFA()                      // Verify(PDFA1B).Valid
ok, err := doc.IsPDF()                       // VerifyObjectModel().Valid
part, level, err := doc.ClaimedConformance() // what the file claims, e.g. "1", "B"
n, err := doc.PageCount()
version, err := doc.Version()                // header version, e.g. "1.7"
info, err := doc.Metadata()                  // Info dictionary entries
xmp, err := doc.XMPMetadata()                // raw XMP packet, as UTF-8
```

A program that never converts can import `github.com/voidrab/gopdfrab/verify`
instead. It has the same verification API under the same names, over the same
types, so switching is an import swap, and it never links the converter. Go's
linker already drops most unused converter code, so natively this saves about
19% of a small verifying binary; in WebAssembly the verify-only module is less
than half the size of the full one.

### Inspecting issues

Each `PDFError` exposes the `Check` that flagged it, its page and its messages,
and `Result` groups them:

```go
for _, issue := range res.Issues {
    c := issue.Check()
    fmt.Println(c.Clause(), c.Subclause(), c.Name(), c.Description())
    fmt.Println(issue.Page(), issue.Messages())
}

fmt.Println(res.Summary()) // one line per Check
res.Checks()               // distinct Checks violated, sorted by clause
res.IssuesByCheck()        // map[Check][]PDFError
res.IssuesOnPage(1)        // 0 = document-level
```

`Result`, `PDFError` and `Check` marshal to a stable JSON shape:

```json
{"type":"A-1b","valid":false,"issueCount":2,"issues":[{"check":{"name":"...","clause":"6.1.3",...},"page":0,"documentLevel":true,"messages":["..."],"text":"..."}]}
```

### Converting

`Convert` produces a PDF/A conformant rewrite. It runs pre-emptive fixups, then
a verify/fix loop, and rasterizes a page only when no in-place repair works.

```go
cr, err := gopdfrab.Convert(path, gopdfrab.PDFA1B) // or ConvertBytes, doc.Convert
if err != nil {
    log.Fatal(err)
}
defer cr.Close() // a large output spills to a temp file

if err := cr.Save("out.pdf"); err != nil { // atomic; WriteTo(w) streams, Output() returns bytes
    log.Fatal(err)
}
fmt.Println(cr.Iterations, cr.Result.Valid)
for _, iss := range cr.Residual() { // issues nothing could repair
    fmt.Println(iss.Check().Clause(), iss.Page(), iss.Messages())
}
```

`ConvertAll(paths, profile)` converts a batch concurrently. For a batch too
large to hold every output at once, `ConvertEach` calls a callback on each
result as it completes and closes it afterwards; returning an error stops the
batch.

```go
err := gopdfrab.ConvertEach(paths, gopdfrab.PDFA1B, gopdfrab.Options{Workers: 4},
    func(r gopdfrab.FileResult[gopdfrab.ConvertResult]) error {
        if r.Err != nil {
            return nil // skip this file, keep going
        }
        return r.Result.Save(filepath.Join(outDir, filepath.Base(r.Path)))
    })
```

### What a conversion lost

Conforming to PDF/A is not the same as looking like the input, so a result
reports loss separately from `cr.Result.Valid`:

- `cr.RasterizedPages` lists pages rebuilt as an image, and `cr.RasterDrops`
  what the rasterizer could not draw on them.
- `cr.LostObjects` lists content that could not be carried over at all: an
  unresolvable object, an undecodable stream, an embedded file or annotation
  the level forbids, a layer the document hides.
- `Options.CheckFidelity` renders input and output with the same rasterizer
  and fills `cr.Fidelity` with a per-page comparison; `Blanked()` flags a page
  that lost its content.

```go
cr, _ := gopdfrab.ConvertContext(ctx, path, gopdfrab.PDFA1B,
    gopdfrab.Options{CheckFidelity: true})
for _, pf := range cr.Fidelity {
    if pf.Blanked() {
        log.Printf("page %d lost its content during conversion", pf.Page)
    }
}
```

### Options, cancellation and limits

Every entry point has a `…Context` counterpart taking a `context.Context` and
an `Options` value (`VerifyContext`, `VerifyBytesContext`, `VerifyAllContext`,
`ConvertContext`, `ConvertBytesContext`, `ConvertAllContext`,
`ConvertEachContext`); the zero `Options` is the default.

```go
cr, err := gopdfrab.ConvertContext(ctx, path, gopdfrab.PDFA1B, gopdfrab.Options{
    Password:      []byte("secret"), // decrypt an encrypted input
    RasterDPI:     300,              // raster fallback resolution (default 150)
    MaxIterations: 8,                // verify/fix loop bound (default 4)
    Workers:       4,                // batch concurrency (default runtime.NumCPU)
})
```

`Password` applies when the file is opened; for a `Document`, use
`OpenWithPassword` or `OpenBytesWithPassword`. Files with an empty user
password are decrypted without one. Cancellation is checked between fix
iterations and raster passes, and batch forms stop dispatching new files.

A single stream decodes to at most 256 MB by default, a guard against
decompression bombs. The caps are process-wide, because they are enforced deep
in the decode path; set them once at startup:

```go
gopdfrab.SetLimits(gopdfrab.Limits{MaxDecodedStreamBytes: 512 << 20})
```

A zero field resets that cap to its default; `CurrentLimits` and
`DefaultLimits` report the effective and built-in values.

### Errors and damaged files

Failures can be matched with `errors.Is`:

| Error                           | Meaning                                                                      |
| ------------------------------- | ---------------------------------------------------------------------------- |
| `gopdfrab.ErrNotPDF`            | the input is not a PDF (no `%PDF-` header)                                   |
| `gopdfrab.ErrDamaged`           | the cross-reference or trailer structure could not be parsed                 |
| `gopdfrab.ErrEncrypted`         | an encryption scheme gopdfrab does not implement                             |
| `gopdfrab.ErrPasswordRequired`  | a correct password is required to open the file                              |
| `gopdfrab.ErrUnresolvableGraph` | `Convert` could not resolve the object graph, so no output was produced      |
| `gopdfrab.ErrInternal`          | a defect in gopdfrab, recovered from a panic; please report it with the file |

Damage short of that does not fail the document. An object that fails to parse
is re-located by scanning for its `N G obj` header, or resolved to null, and a
missing or unusable `startxref` rebuilds the cross-reference table from a
full-file scan. Either way the damage is reported as an issue and every other
check still runs; a conversion that had to null an object never reports itself
valid.

## Profiles and checks

`PDFA1B`, `Legacy1B` and `PDF` are the ready-made profiles, named `pdfa1b`,
`legacy1b` and `pdf` as the CLI's `--profile` takes them (`ProfileByName`,
`Profiles`, `Profile.Name`). Use `Legacy1B` if you need the verdicts of the
older Isartor suite. A profile is immutable: `AddCheck`, `RemoveCheck` and
`Clear` return a clone. The package variables are not, so reassigning one
changes the default for the whole process.

```go
p := gopdfrab.PDFA1B.RemoveCheck(gopdfrab.Checks.Font.SimpleNotEmbedded)

p := gopdfrab.PDFA1B.Clear().AddCheck(
    gopdfrab.Checks.Transparency.ImageWithSoftMask,
    gopdfrab.Checks.Metadata.PDFAIdentifierMissing,
)

p := gopdfrab.NewProfile(gopdfrab.A1B).AddCheck(gopdfrab.Checks.Font.SimpleNotEmbedded)

res, err := doc.Verify(p)
```

| Registry field        | Spec area                                                    |
| --------------------- | ------------------------------------------------------------ |
| `Checks.Structure`    | 6.1.x file header, trailer, xref, object framing, limits     |
| `Checks.Colour`       | 6.2.2 OutputIntent, 6.2.3.x device colours, 6.2.9–10         |
| `Checks.Image`        | 6.2.4–6.2.7 image, form and PostScript XObjects              |
| `Checks.Transparency` | 6.2.8 transfer functions, 6.4 soft masks, blend modes, alpha |
| `Checks.Font`         | 6.3.x embedding, subsets, metrics, encoding                  |
| `Checks.Annotation`   | 6.5.x annotation types and dictionaries                      |
| `Checks.Action`       | 6.6.x action types and additional actions                    |
| `Checks.Metadata`     | 6.7.x XMP metadata, extension schemas, PDF/A identifier      |
| `Checks.Form`         | 6.9 interactive forms                                        |
| `Checks.ObjectModel`  | ISO 32000 object-model conformance, independent of PDF/A     |

An eleventh group, `Checks.LogicalStructure` (6.8.x), is PDF/A-1a only, so
`AllChecks()` returns 184 checks over all eleven groups, 179 over the ten above.
A clause names a check only within a conformance level, so lookups take one:
`CheckByClauseIn(gopdfrab.A1B, "6.3.4", 1)`, `ChecksForClauseIn` and
`check.ClauseIn(level)`. The older `CheckByClause` and `ChecksForClause` read
PDF/A-1 clauses and are deprecated.

### PDF object-model conformance

`Checks.ObjectModel` holds six checks derived from the
[Arlington PDF Model](https://github.com/pdf-association/arlington-pdf-model),
the machine-readable ISO 32000 object model. They answer "is this even valid
PDF", independent of PDF/A. The `PDF` profile enables only them, and
`ObjectModelOnly()` builds an unshared copy of it.

```go
res, err := gopdfrab.VerifyObjectModel(path) // or VerifyObjectModelBytes, doc.VerifyObjectModel
cr, err := gopdfrab.ConvertObjectModel(path) // or ConvertObjectModelBytes, doc.ConvertObjectModel
```

`ConvertObjectModel` applies every repair that is safe and preserves meaning,
and reports anything else as a residual.

## WebAssembly

`wasm/` holds two `syscall/js` builds that run in the browser:

```bash
# verify only: about 6.2 MB
GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o gopdfrab-verify.wasm ./wasm/verify
# verify and convert: about 15.1 MB
GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o gopdfrab.wasm ./wasm
```

A page can load the verify-only module up front and fetch the full one only to
convert. Each registers its globals, which take a `Uint8Array` and return a
Promise, then posts `{type: "ready", module}` with `module` set to `"verify"`
or `"convert"`:

- `gopdfrabVerify(bytes)` resolves to `{valid, summary, profile, issueCount, doc, issues}`.
- `gopdfrabConvert(bytes)` (full module only) resolves to `{valid, iterations, output, doc, before, resolved, residual, rasterizedPages, rasterDrops, lostObjects}`, with `output` a `Uint8Array`.

The doc comments in `wasm/main.go` and `wasm/internal/jsapi` are the full
reference.

## Performance

PDF/A-1b verification measured against the Java-based
[veraPDF](https://verapdf.org/) and [PDFBox Preflight](https://pdfbox.apache.org/)
on the combined Isartor and veraPDF corpora (773 files); `benchmarks/README.md`
has the methodology. Startup and cold single-file numbers are dominated by JVM
startup.

| Benchmark                       | vs veraPDF  | vs PDFBox Preflight |
| ------------------------------- | ----------- | ------------------- |
| Startup time                    | 149x faster | 22x faster          |
| Single file (cold, median file) | 160x faster | 50x faster          |
| Batch throughput                | 20x faster  | 15x faster          |
| Batch peak memory               | 11x smaller | 15x smaller         |
| Deployment footprint            | 9x smaller  | 3x smaller          |

The 773-file batch verifies in 0.29 s (2749 files/s) at 67 MB peak RSS, and a
cold verification of the median file takes about 5 ms including process
startup.

## Fuzzing

gopdfrab reads untrusted, often malformed input, so `internal/pdfgen`
generates broken PDFs in memory from a seed: valid skeletons corrupted with
truncation, bad offsets, negative lengths, dangling and circular references and
deep nesting, plus random object graphs from a small PDF grammar. They seed
native Go fuzz targets for the whole pipeline, for each decoder, parser and
writer, and for semantic oracles: repeated runs must match byte for byte, and a
conversion reported valid must re-verify as valid. Every seed replays on each
`go test` run, alongside race and time-bound tests and a reproducer for each
fixed crash.

```sh
go test -run '^$' -fuzz=FuzzOpenBytes        -fuzztime=60s ./internal/pdf/
go test -run '^$' -fuzz=FuzzConvertRoundTrip -fuzztime=60s .
```

## Security

To report a suspected vulnerability, follow [SECURITY.md](SECURITY.md); please
do not open a public issue for one.

## Contributing

[CONTRIBUTING.md](CONTRIBUTING.md) covers the test suites, the test corpora and
walkthroughs; [ARCHITECTURE.md](ARCHITECTURE.md) maps the codebase.

- **A PDF that fails and should not, or passes and should not**, is the most
  useful thing to send. Please attach it to an
  [issue](https://github.com/voidrab/gopdfrab/issues/new/choose).
- **Code changes**: one concern per pull request, with a test.
- **New checks**: mostly a data change in the check registry; see
  CONTRIBUTING.md.

Pull requests need a one-time signature on the [Contributor Licence
Agreement](CLA.md), so that the whole codebase can be released under both
licences. You keep your copyright. A bot asks you to confirm it the first time
you open a pull request.

## Licensing

gopdfrab is dual-licensed under GNU AGPL 3.0 and a commercial licence.
[Get in touch](mailto:contact@voidrab.com) about commercial licensing.

gopdfrab bundles third-party fonts and ICC profiles and embeds them into
converted files; [NOTICE](NOTICE) gives each one's source and licence.
