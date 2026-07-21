# Roadmap to 1.0

Goal: the best PDF/A-1b verifier and converter available in Go, good enough that
the API can be frozen. PDF/A-2/3/4 come after 1.0, not before.

## Where things stand

Working and tested:

- 158 checks across 10 groups, Isartor (205 files) and veraPDF (569 files)
  corpora both fully green.
- Convert pipeline: pre-emptive fixups, verify/fix loop (max 4 iterations),
  raster last resort. Corpus floor 510/510.
- Arlington object-model checks, verify and convert, wired to a generated model.
- Fuzzing at three levels including semantic oracles (determinism, honesty,
  convergence).
- Coverage: root 100%, arlington 100%, pdf 96.7%, writer 95.4%, verify 93.5%,
  convert 92.6%.
- Benchmarked 15–160x faster than veraPDF and PDFBox Preflight depending on the
  metric.

The gaps below are what stands between that and a release.

---

## P0 — a "valid" verdict currently can mean "we couldn't read it"

These are soundness holes. They matter more than any feature.

### 1. Stream decode failures silently skip checks

`internal/verify/verifier.go:993` and roughly a dozen sites in
`checks_font_program.go` do `if err != nil { return }` when a stream won't
decode. The checks that would have run against that stream just don't run, and
nothing is reported. A file whose content streams are undecodable can come back
`Valid: true`.

Fix: decode failure must produce an issue, not a silent skip. Options are a new
`Structure.StreamUndecodable` check, or attaching the failure to whichever check
was about to run. Prefer the former — one clear signal, easy to reason about.
Audit every `err != nil { return }` in `internal/verify` while doing it.

### 2. `DecodeStream` supports three filters

`internal/pdf/content.go` handles FlateDecode, ASCIIHexDecode, ASCII85Decode.
Everything else returns "unsupported filter", which combined with item 1 means
silent check skipping. Missing:

- **LZWDecode** — the decoder exists (`internal/pdf/lzw.go`) but is only reachable
  from convert and the rasterizer. Older PDFs from Acrobat 4-era tools use it for
  content streams.
- **RunLengthDecode** — no decoder at all.
- **Predictors** — `decodeStreamPredicted` is unexported and only used for xref
  and object streams. A content stream with `/Predictor 12` decodes to garbage
  through the public path.

### 3. Three near-identical decode chains

`internal/pdf/content.go` `DecodeStream`, `internal/pdf/predictor.go`
`decodeStreamPredicted`, `internal/convert/fixups_stream.go` `lzwStreamPlaintext`,
plus a fourth partial one in `raster_image.go`. They have already diverged — the
convert copy handles LZW and predictors, the verify path doesn't. This is how
item 2 happened.

Fix: one decode chain in `internal/pdf` handling every filter and predictor, with
the image-only filters (DCT, JPX, JBIG2, CCITT) returning a typed
"encoded image data, not decodable to bytes" result rather than a generic error,
so callers can distinguish "we can't" from "it's broken."

---

## P1 — resilience and unusual files

### 4. No xref recovery

Verified: corrupting the `startxref` offset in a working file gives

```
verify: failed to parse structure: could not parse startxref offset
```

with zero issues and no result. Truncating the file gives `startxref not found`.
Both are hard errors — no verification, no conversion, nothing.

This is the biggest practical gap. Files with damaged cross-reference data are
exactly the files people reach for a PDF/A converter to fix. veraPDF and PDFBox
both scan for `N G obj` patterns and rebuild. gopdfrab must too:

- Fall back to a full-file object scan when `startxref` is missing, unparseable,
  or points somewhere that isn't an xref.
- Rebuild the table from the scan, prefer the last definition of each object
  number, recover the trailer by finding the object with `/Type /Catalog`.
- Report the recovery as an issue (the file is not conformant), but keep going.
- Same fallback when the xref parses but its offsets are wrong — currently a
  wrong offset resolves to null and the file quietly loses objects.

Seed `internal/pdfgen` with these shapes and add oracle tests: a file with a
deliberately broken xref must verify to the same issue set as the intact one
plus the recovery issue.

### 5. Encrypted input can't be converted

There is no decryption anywhere. `Encrypt` in the trailer is correctly flagged
(6.1.3), but that's the end of it — the streams stay encrypted and unreadable, so
convert produces nothing useful.

A large share of real-world PDFs are encrypted with an empty user password purely
to set permission flags. Those are trivially decryptable and are a completely
reasonable conversion input. Implement standard security handler decryption
(RC4 40/128, AES-128 for R4, AES-256 for R6) for the empty-password case, plus an
optional password on the open path. Refuse and report clearly when a real
password is needed.

### 6. Rasterizer drops content it doesn't understand

The raster last resort in `internal/convert/raster.go` handles a good set of
operators but silently ignores:

- `sh` (shading operator) and shading patterns — pages with gradients render blank
  in those areas.
- `BI`/`ID`/`EI` inline images inside content streams.
- Type 3 fonts.
- `Tr` (text render mode) and `Ts` (rise) — invisible OCR text renders as visible.

Since raster is the fallback that guarantees conformance, anything it drops is
data loss the user is never told about. Two things needed: fill the gaps above,
and make the rasterizer report what it couldn't render so `ConvertResult` can
carry a fidelity warning. Silent data loss is worse than a residual issue.

### 7. Batch operations hold everything in memory

`ConvertAll` (`internal/convert/convert.go:66`) stores a full `ConvertResult` per
input, each with the complete output PDF as `[]byte`. Converting 500 files means
500 output documents resident at once. This directly contradicts the mmap-based
design of the read path.

Fix: a callback or iterator form that hands each result over as it completes and
lets it be freed, plus a worker-count knob. Keep `ConvertAll` for small batches.

### 8. Windows and macOS are untested

CI runs ubuntu-latest only. `mmap_other.go` returns nil on non-unix, so Windows
takes an entirely different, unexercised seek-based read path — and loses the
large-file guarantee. Add a CI matrix (linux/macos/windows), and either implement
Windows file mapping or document the limitation honestly.

---

## P2 — API before it gets frozen

The disclaimer says the API will change heavily before release. This is the list.

### 9. Options

Every entry point takes `(path, profile)` and nothing else. There is no way to
set raster DPI, cap iterations (hardcoded `maxConvertIterations = 4`), bound
memory or time, or supply a password. Add a functional-options or config-struct
form:

```go
gopdfrab.Convert(path, gopdfrab.PDFA_1B,
    gopdfrab.WithRasterDPI(200),
    gopdfrab.WithMaxIterations(8),
    gopdfrab.WithPassword(pw))
```

Keep the current two-argument signatures working — they're the common case.

### 10. No `context.Context` anywhere

Nothing is cancellable. A malformed file that sends the fix loop down a slow path
runs to completion no matter what. Anyone putting this behind an HTTP handler
needs cancellation. Add `ConvertContext`/`VerifyContext` variants and check
cancellation at loop boundaries (per fixer pass, per page walk, per file in a
batch).

### 11. Results don't serialize

`Result.Issues` is `[]PDFError` and every `PDFError` field is unexported with
accessor methods. `json.Marshal` on a result yields `[{},{},{}]`. Any CLI, HTTP
service, or CI integration needs JSON. Add `MarshalJSON` on `PDFError`, `Check`,
and `Result` with a stable documented shape (clause, subclause, name,
description, page, object ref, messages).

### 12. Streaming output

`ConvertResult.Output []byte` plus `Save(path)`. For a large document this holds
the whole output in the heap after the read path went to trouble to avoid exactly
that. Add `WriteTo(io.Writer)` and consider making `Output` lazy.

### 13. Typed errors

Failures are `fmt.Errorf` and `errors.New` strings. Callers can't distinguish
"not a PDF" from "encrypted" from "truncated" from "I/O error" without string
matching. Define sentinels: `ErrNotPDF`, `ErrEncrypted`, `ErrDamaged`,
`ErrPasswordRequired`, wrapped so `errors.Is` works.

### 14. A real CLI

`cmd/` is empty. `main/main.go` is an example that imports `internal/pdf`, which
external users can't do. Ship `cmd/gopdfrab` with verify/convert subcommands,
`--json` output, exit codes that mean something (0 valid, 1 invalid, 2 error),
and glob/recursive input. This is how most people will first try the library, and
it's also what makes the benchmark numbers reachable by anyone else.

### 15. Naming consistency

`PDFA_1B` vs `A_1B` vs `Legacy_1B` vs `PDF` and `ObjectModel` — the level
constants and profile variables use overlapping names with different conventions.
`Document.GetPageCount`/`GetVersion`/`GetMetadata` carry Go-unidiomatic `Get`
prefixes while everything else doesn't. Fix both while breaking changes are still
free.

---

## P3 — performance

Current numbers are strong; the work here is keeping them.

### 16. Commit performance history

`benchmarks/results/` is gitignored, so every round's numbers are local-only and
regressions across releases are invisible. Commit a small per-round summary
(benchstat table or JSON) per release tag.

### 17. Extend the allocation guards

Wall-clock on a dev machine is thermally noisy (±15% on the 47 MB convert), so
the `allocs/op` assertions in `benchmarks/micro/bench_test.go` are the only
stable gate. They currently cover a fraction of the samples. Extend to
`Convert/fonts` and the other cost paths to lock in the 2026-07 wins.

### 18. Benchmark the recovery path

Once item 4 lands, a full-file object scan is a new worst case. Benchmark it
explicitly so recovery on a large damaged file doesn't become a denial-of-service
vector.

---

## P4 — infrastructure

### 19. Coverage to target

verify at 93.5% and convert at 92.6% against a ~95% target. Known remaining gaps
are listed in the per-package notes; the CFF/Type1 fixture work is the bulk of it.

### 20. Instrument the conservative skips

`CFFAdvanceWidths`/`CFFCIDAdvanceWidths` deliberately bail on FontMatrix, unusual
charstring prefixes, and per-glyph parse uncertainty. The bails are invisible —
there's no way to know how much 6.3.6 coverage is silently skipped across the
corpus. Add a debug counter or test-only stat. Same for the Type1 `FontFile`
width path, which bails on Differences while the Type1C path handles them.

### 21. Document the two dangerous invariants

- `pdf.StreamKey` keys caches by `uintptr(&RawStream[0])`, valid only while the
  graph pins the slices. `AdoptStreamCaches` extends this across two Readers.
  Document at the type before a future cache consumer violates it.
- PDF null is Go `nil` across the parser, resolver, and writer. A present-but-null
  dict entry is indistinguishable from an absent one, which is correct PDF
  semantics and very easy to break with `if _, ok := Entries[k]; ok`. Document at
  the type.

### 22. Deduplicate `parseClassicReference`

It's a copy of `parseObject` that has already silently diverged on scalars and
null once. The only legitimate difference is the `N G R` reference lookahead.
Extract the shared scalar dispatch so the fork point is explicit.

### 23. Build nits

`GOOS=js GOARCH=wasm go build ./wasm/` fails without `-o` because the default
output name collides with the `wasm/` directory. Add an explicit `-o` wherever
that gets built, and add the wasm build to CI so it can't rot.

---

## Order of work

1. Items 1–3. Correctness first — everything else is built on trusting the
   verdict.
2. Item 4. Biggest real-world gap, and it changes what "resilient" means.
3. Items 9–15. Do the API break in one pass while the disclaimer still covers it.
4. Items 5–8. Encryption and rasterizer fidelity are large but well-bounded.
5. Items 16–23. Continuous.

## Not in 1.0

- PDF/A-2, -3, -4. Adding parts before -1b is airtight would spread the same
  soundness holes across four conformance levels.
- PDF/A-1a (accessibility, tagged PDF). Different problem, much larger.
- Digital signature validation.
- Rendering as a general-purpose feature. The rasterizer stays a conversion
  fallback.
