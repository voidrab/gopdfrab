# Contributing to gopdfrab

Thanks for looking. This file covers the practical things: what is most useful
to send, how to run the tests, where the test files come from, and how to add a
new PDF/A check.

## The most useful thing you can send is a PDF

gopdfrab is a verifier. Its worst failure mode is disagreeing with reality about
a real file, and real files are the one thing we cannot generate ourselves. Two
reports are worth more than anything else:

- **A file that fails and should not.** gopdfrab reports issues, but the
  document really is valid PDF/A-1b. This is a false positive, and it is the
  highest-value report we can receive.
- **A file that passes and should not.** gopdfrab says the document is
  conformant when it is not.

Report the file. You do not need to attach a stack trace, a
minimal reproducer, a patch, or any Go. If the file is confidential, say so and
we will work out what is possible — sometimes a single page of it is enough.

Every file that turns out to expose a real defect goes into the regression
corpus, so the bug stays fixed.

Use the [issue templates](https://github.com/voidrab/gopdfrab/issues/new/choose).
Suspected security problems go through [SECURITY.md](SECURITY.md) instead —
please do not open a public issue for one.

## Building and testing

Go 1.24 or newer. CI builds on 1.24 and 1.26.4, on Linux, macOS and
Windows.

```bash
go build ./...
go test -short ./...          # what CI runs on every push
go test -short -race ./...    # CI runs this too
```

`-short` skips the slow corpus sweeps. A full run without it takes a while and
needs the real-world corpus (below).

Linting uses [golangci-lint](https://golangci-lint.run) v2.12.2, configured in
`.golangci.yml` — `govet`, `staticcheck` (SA checks only), `ineffassign` and
`unused`:

```bash
GOTOOLCHAIN=go1.26.4 golangci-lint run
```

The `GOTOOLCHAIN` prefix is only needed if your local Go is older than the one
golangci-lint was built against.

Fuzzing:

```bash
scripts/fuzz.sh 30s     # every Fuzz* target in the module, 30s each
```

A crash writes a reproducer under the owning package's `testdata/fuzz` and fails
the run. Commit that reproducer with the fix.

## The test corpora

`tests/` is a separate Go
module (`tests/go.mod`) that contains no code — it exists so the vendored
PDFs are excluded from the published module zip.

| Directory | Size | What it is |
|---|---|---|
| `tests/Isartor` | 8 MB, 205 files | The Isartor test suite. Committed. |
| `tests/veraPDF` | 9 MB, 569 files | The veraPDF conformance corpus. Committed. |
| `tests/realworld` | 3.9 GB, 1585 files | Documents from real producers. **Not committed.** |

`tests/rules.md` is the PDF/A-1 rule text, clause by clause, taken from the
veraPDF validation profiles. It is the quickest way to find out what a clause
number actually requires.

### Getting the real-world corpus

The real-world PDFs are gitignored; what is committed is
`tests/realworld/manifest.json`, listing each file's path, source URL and
sha256. Fetch them with:

```bash
scripts/fetch-realworld-corpus.sh      # needs jq, curl and sha256sum
```

### You do not need any of this to start

Every test that reads a corpus skips when the corpus is not there. A fresh
clone runs `go test -short ./...` green without downloading anything. Fetch the
real-world corpus only when you are working on something the synthetic suites do
not cover — false positives, conversion fidelity, or performance.

## Adding a check

This is the easiest place to contribute code, and it is mostly a data change.
There are 159 checks in 11 groups. Adding one means touching two or three files.

Here is `Structure.OptionalContent` (PDF/A-1 clause 6.1.13, "no optional
content"), followed end to end.

**1. Declare it.** Add a field to the right group struct in
`internal/pdf/checks_catalog.go`. The groups are `structureChecks`,
`colourChecks`, `imageChecks`, `transparencyChecks`, `fontChecks`,
`logicalStructureChecks`, `annotationChecks`, `actionChecks`, `metadataChecks`,
`formChecks` and `objectModelChecks`:

```go
type structureChecks struct {
	// ...
	OptionalContent Check
}
```

**2. Register it** in the `init` function of the same file, with a name, a
one-line description of the rule, the clause and a sub-rule number:

```go
OptionalContent: newCheck(
	"OptionalContent",
	"The document catalog must not contain an OCProperties entry (optional content is not permitted in PDF/A-1)",
	"6.1.13", 1),
```

**3. Report it** from the matching file in `internal/verify`. The files follow
the check groups: `checks_dict.go`, `checks_colour.go`, `checks_content.go`,
`checks_font.go`, `checks_font_program.go`, `checks_xmp.go`,
`checks_objectmodel.go`, with the structural checks in `verifier.go`. Either
build a `pdf.PDFError` directly:

```go
func verifyOptionalContent(d *pdf.Reader) []pdf.PDFError {
	_, err := d.ResolveGraphByPath([]string{"Root", "OCProperties"})
	if err == nil {
		return []pdf.PDFError{pdf.NewError(
			pdf.Checks.Structure.OptionalContent,
			[]error{fmt.Errorf("OCProperties not allowed in document catalog")},
			0, nil,
		)}
	}
	return nil
}
```

or, inside a graph walk, call `ctx.Report(check, obj, message)` on the
`ValidationContext`.

**4. Optionally, fix it.** A check with a fixer is one the converter can repair.
Fixers live in `internal/convert/fixups_*.go` and implement two methods:

```go
type optionalContentFixer struct{}

func (optionalContentFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.OptionalContent
}

func (optionalContentFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	root, ok := trailer.Entries.Get("Root").(pdf.PDFDict)
	if !ok {
		return false, nil
	}
	if _, ok := root.Entries.Lookup("OCProperties"); !ok {
		return false, nil
	}
	root.Entries.Del("OCProperties")
	return true, nil
}
```

Register it with `registerFixer` in an `init`. A `Fix` must be idempotent —
the convert loop verifies, fixes, and verifies again until nothing changes. Each
check has at most one fixer; registering a second one panics.

**5. Test it.** Add a case to the `_test.go` file next to the code, with a
fixture. If the Isartor or veraPDF suite already covers the clause, point the
test at that file rather than building a new one, and skip when it is absent —
see the existing tests for the pattern.

If you are unsure whether a rule is in scope for PDF/A-1b, open an issue first.
The clause text in `tests/rules.md` is usually enough to settle it.

## Pull requests

- One concern per pull request.
- Ship the test with the change.
- Keep the existing style: the code explains *why*, not *what*.
- Update `CHANGELOG.md` if the change is visible to a user of the library or the
  CLI. `CHANGELOG.md` also states what counts as a breaking change.
- Green CI: build, tests, race, lint, the WASM build, and the differential run
  against veraPDF.

### The CLA

gopdfrab is dual-licensed under the AGPL 3.0 and a commercial licence. That only works if
one party can release the whole codebase under both, so code and documentation
contributions need a one-time signature on the [Contributor Licence
Agreement](CLA.md). You keep your copyright — you are granting permission, not
handing anything over. A bot asks you to confirm it in a comment the first time
you open a pull request.

Bug reports, questions and sample PDFs need nothing from you but the file.

## What to expect from us

- Every issue gets a human reply within 72 hours.
- Pull requests are merged or turned down within two weeks.
