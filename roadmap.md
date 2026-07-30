# Roadmap to 1.0

Goal: the best PDF/A-1b verifier and converter available in Go, good enough that
the API can be frozen. PDF/A-2/3/4 come after 1.0, not before.

Items 1–29 and 32 are done; each is one line under "Done", and the commit
history has the detail. What is still open is in "Open work".

## Where things stand

- 159 checks across 10 groups. Every check in scope for 1b is implemented; the
  ten that are not each say why in the catalogue — six are PDF/A-1a, four are
  subsumed by the object-model checks. Isartor (204 fail files) and veraPDF
  (263 pass + 306 fail) both fully green, cross-checked against the veraPDF
  binary itself in CI, not just against filename expectations.
- Convert pipeline: pre-emptive fixups, verify/fix loop, raster last resort.
  Committed-corpus floor 510/510. On the 1585-file real-world corpus, 1564 of
  1580 reach conformance with zero errors, panics or hangs.
- The rasterizer draws what a PDF/A conversion actually meets: images (inline and
  stencil masks), all seven shading types, shading patterns, Type 3 glyphs. What
  it cannot draw is reported per page, never dropped in silence.
- One stream-decode chain covering every filter PDF/A-1 permits, with a typed
  result separating "encoded image data" from "broken".
- Arlington object-model checks, verify and convert, off a generated model.
- Fuzzing at three levels, including semantic oracles (determinism, honesty,
  convergence).
- Resource hardening: ~15 depth/size caps across the parser, settable decode and
  resident-cache budgets, no silent truncation anywhere.
- Coverage: arlington 100%, cmd 95.7%, pdf 95.5%, verify 94.3%, convert 94.3%,
  writer 94.1%, pdfgen 94.8%, root 92.5%.
- 15–160x faster than veraPDF and PDFBox Preflight depending on metric.

---

## Open work

### 30. Coverage to ~95%

verify 94.3%, convert 94.3%, writer 94.1%, root 92.5% — the root package is now
the widest gap. Per the standing decision, defensive parser guards are not
chased; the remainder is CFF/Type1 fixtures.

### 31. Scale Type1 widths instead of skipping them

`Type1WidthTable` skips the 6.3.6 comparison when the program declares a
non-default `/FontMatrix`, matching what the CFF path does. Scaling the
charstring widths by the declared matrix would keep the check live instead of
dropping it, and the real-world case that prompted the guard (a 1/2000 em font,
every width off by exactly 2x) says it would work. Speculative until a file
turns up where the skip actually hides something.

### 33. The last 16 files: 1564 of 1580 → 1580 of 1580

Measured 2026-07-30, with the harness's own per-file report
(`GOPDFRAB_REALWORLD_REPORT`). What the 16 non-conformant files report:

| residual | files |
| --- | --- |
| objmodel: `OutlineItem` missing `/Title` (119, 170 and 21 issues) | 3 |
| `StreamUndecodable` 6.1.7 (72 issues on one) | 3 |
| `XRefSubsectionHeaderFormat` 6.1.4 | 2 |
| objmodel: a page tree whose `Kids`/`Count` disagree, and a trailer `Info` of the wrong type | 2 |
| `GraphResolutionFailure` 6.1.6 | 1 |
| `GraphResolutionFailure` 6.1.6 together with outline items missing `/Parent` | 1 |
| one each: `SeparationAlternateColour` 6.2.3.4, `ImageWithSoftMask` 6.4, `ImageBitsPerComponent` 16, `TrueTypeEncoding` 6.3.7 | 4 |

Two separate problems sit behind that list, and the second is the smaller one.

**The verdict disagrees with the file it just wrote.** Re-reading all 16 written
outputs with the same profile, every one of them verifies **clean**. Only one of
the 16 is explained by the deliberate carry-over at
`internal/convert/convert.go:458`, where an object the reader degraded to null is
re-reported so a lossy conversion cannot claim success — that path emits nothing
but `GraphResolutionFailure`. The other 15 report checks it never emits, so they
come from the final verify, which the code says runs on the output bytes. Bytes
that then verify clean on a fresh read. That contradiction is the thing to chase
first; it is most of the 16, and until it is understood the conformance fraction
is measuring something other than what it claims.

The carry-over itself is worth revisiting alongside it: it makes "content was
lost" and "the file is not conformant" the same signal, when they are different
facts a caller would want separately.

**Four outputs really are non-conformant, and we pass them.** veraPDF fails 4 of
the 16 outputs: 6.2.4-4, 6.2.3.3-2, and 6.3.6-1 twice. Those are verifier
false-negatives, found the same way item 32's was, and the 6.3.6-1 pair is worth
reading next to item 31 — that check is already skipped for some font programs.

The real-world corpus is not in CI and so is not covered by the differential
harness (item 11), which is why both of these went unseen. A differential pass
over converted real-world output, even a sampled one, would have caught them.

---

## Done

1. **Undecodable content streams no longer pass.** One decode chain
   (`internal/pdf/filters.go`), every PDF/A-1 filter, `Structure.StreamUndecodable`
   reported from both chokepoints, and the four usage-driven suppression sets
   discarded when usage is incomplete.
2. **A bad xref offset no longer suppresses unrelated checks.** Per-object
   recovery at the resolution boundary: retry at the real `N G obj` header, else
   degrade to a cached null, both reported as 6.1.6.
3. **Convert never returns empty output with a nil error.** `ErrUnresolvableGraph`;
   reader-degraded objects are carried into the final result and force `Valid=false`.
4. **Whole-table xref recovery.** A missing or unparseable `startxref` rebuilds by
   full-file scan and synthesizes a trailer, reported as 6.1.4; linear time.
5. **Encryption.** Standard security handler (RC4 40/128, AES-128, AES-256), user,
   owner and empty passwords, applied at one choke point.
6. **The rasterizer no longer drops content silently.** `Tr`/`Ts` correctness fix,
   then inline images, all seven shadings, mesh shadings, Type 3 glyphs and
   shading patterns actually drawn; tiling patterns reported.
7. **Limits are settable and never truncate silently.** `Limits`/`SetLimits`,
   `--max-decoded-mb`, `--max-resident-mb`.
8. **Convert's memory.** `ConvertEach` + `Options.Workers`; output spills to a temp
   file; streaming content tokenization under a 64 MB budget; bounded, releasable
   caches; a slice-backed `PDFDict` (graph 23.5 → 16.7 MB, convert peak 63 →
   52.6 MB). The remaining piece is recorded under "Not in 1.0".
9. **Windows seek path.** Exercised on Linux via `pdf.OpenBytesSeek` parity tests;
   the absence of mmap on Windows is documented, not implemented away.
10. **Real-world corpus.** 1585 files, 3.9 GB, sourced from five public
    collections plus a self-generated producer matrix, classified by veraPDF's own
    verdict, inventoried by hash and licence (bytes gitignored). Found nine real
    defects no synthetic fixture could reach.
11. **The differential harness runs.** veraPDF binary cross-check over both
    committed suites, in CI. Found two verifier false-negatives immediately.
12. **Fidelity gate.** Symmetric-renderer input-vs-output comparison;
    `Blanked()` finds 0 blanked pages across the corpus; `Options.CheckFidelity`.
13. **CI.** `-race` on a 3-OS matrix, per-target fuzzing plus a nightly cron, wasm
    build, differential job.
14. **Thread-safety is documented and enforced**, with five `-race` tests.
15. **Options.** Two-argument entry points plus `…Context` counterparts taking a
    `context.Context` and an `Options` struct.
16. **Cancellation.** Per file in a batch, per verify/fix iteration, per raster pass.
17. **Results serialize.** `MarshalJSON` on `Check`, `PDFError`, `Result`.
18. **Streaming output.** `WriteTo`, lazy spill-backed `Output()`, `Close()`.
19. **Typed errors.** `ErrNotPDF`, `ErrDamaged`, `ErrEncrypted`, `ErrPasswordRequired`.
20. **A real CLI.** `cmd/gopdfrab`, public API only, meaningful exit codes.
21. **Naming.** Underscores dropped per staticcheck ST1003; `Get` prefix removed.
22. **Committed performance history**, one benchstat-readable file per round.
23. **Allocation guards** extended to every distinct cost path.
24. **Recovery benchmarked** (`BenchmarkXRefRecovery`): linear, not a DoS vector.
    No allocation guard on it yet.
25. **Stability policy** (`CHANGELOG.md`).
26. **Security policy** (`SECURITY.md`).
27. **Documentation.** `doc.go` plus runnable examples that double as tests.
28. **Repo hygiene.** `TODO.md` folded in and deleted, wasm `-o` in CI, tree clean.
29. **The catalogue tells the truth.** No registered check is silently dead any
    more, and the ones deliberately left to something else say so in place.
    `FontFileSubtype` and `XMPNoCorrespondingType` were declared but never
    reported; `ICCBasedComponentsMismatch` fired only from the output-intent
    path, never the ICCBased colour spaces its clause names; `FontBaseFont` had
    no fixer. All four now do what the catalogue says, with fixers that keep the
    document's own content: an OpenType font file is unwrapped to the CFF or
    TrueType program inside it rather than substituted away, and an ICCBased
    profile is replaced to match `/N` rather than the reverse, since `/N` is the
    operand count every `sc` in the file depends on.

32. **An ICCBased colour space's own profile is checked.** `ICCBasedProfileInvalid`
    (6.2.3.2/2) rejects a profile whose version, device class or colour space
    PDF/A-1 does not allow, and the ICCBased fixer replaces it. Not the
    speculative gap it was recorded as: 38 real-world files carry an ICC v4
    input profile, and one of them converted to output gopdfrab called
    conformant that veraPDF rejected on exactly this rule. Output-intent
    profiles were narrowed to the same clause's stricter set in passing, and
    `sgray.icc` — committed but unused — was embedded so a one-component space
    is repaired in place instead of being dropped for `DeviceGray`.

Also closed along the way: the Type1 `FontFile` width path honours `/Differences`
(it was silently comparing against the wrong glyph, a false positive on
conformant files) and skips on a non-default `/FontMatrix`; an unresolved
`PDFRef` in the verify walk is reported rather than silently unverified; the
DeviceN and resource-rename content rewriters no longer retain the content
scanner's reused operand stack, which corrupted their output on any
multi-operator stream.

---

## Checked and already fine

Recorded so nobody re-investigates:

- **Decompression bombs**: capped, with CCITT column/byte caps and ~15 depth and
  size limits across the parser.
- **Profile immutability**: `AddCheck`/`RemoveCheck`/`Clear` all clone.
- **False positives are tested**: 263 pass files in the veraPDF corpus, plus the
  real-world should-pass half.
- **Predictors belong to Flate and LZW only** (ISO 32000-1 Table 8). Confirmed by
  scanning all three corpora.
- **CCITT stays an image-only filter.** Its output is packed 1-bpc samples,
  meaningless without `/Columns`, `/Rows` and `/BlackIs1`.
- **The real-world corpus stays local.** It is multi-gigabyte and gitignored;
  `manifest.json` keeps it reproducible on any machine, and re-fetching it per CI
  run is not a job worth having.

---

## Not in 1.0

- **Item 8's subtree-streaming resolution.** The original design was to resolve a
  page subtree, walk it, re-reference it and drop the object numbers. It is a
  **verify-side** optimisation that cannot apply to convert, which holds one
  trailer across up to four fix iterations, walks the whole graph per fixer, and
  must enumerate every object before the first output byte because numbers are
  DFS position — so it would not move the peak that matters. It also rests on
  blockers that are permanent: arrays have no object number and cannot get one
  without changing `PDFArray` from `[]PDFValue`, and inline dicts deliberately
  carry no `_ref`. The footprint was attacked by representation instead (item 8),
  which needed none of that.
- PDF/A-2, -3, -4. Adding parts before -1b is airtight spreads item 1's class of
  bug across four conformance levels.
- PDF/A-1a (accessibility, tagged PDF). Different problem, much larger.
- Digital signature validation.
- Rendering as a general-purpose feature. The rasterizer stays a conversion
  fallback.
