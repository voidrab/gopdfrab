// Package gopdfrab verifies and converts PDF files for PDF/A-1b conformance.
//
// PDF/A-1b (ISO 19005-1 Level B) is the archival PDF profile that keeps a
// document reproducible over time: fonts are embedded, colour is unambiguous,
// encryption and external dependencies are forbidden, and XMP metadata describes
// the file. gopdfrab both checks a file against that standard and rewrites a
// non-conformant file to meet it.
//
// # Verifying
//
// Verify reports whether a file conforms and, if not, why. Each violation is a
// [PDFError] naming the [Check] that failed and the ISO clause behind it; a
// [Result] collects them alongside an overall Valid verdict.
//
//	res, err := gopdfrab.Verify("in.pdf", gopdfrab.PDFA1B)
//	if err != nil {
//		// the file could not be opened or parsed
//	}
//	fmt.Println(res.Valid, len(res.Issues))
//
// VerifyBytes verifies in-memory data and VerifyAll a batch of files; each has a
// ...Context variant taking a context.Context and an [Options] value.
//
// # Converting
//
// Convert produces a best-effort PDF/A-1b rewrite: it applies pre-emptive
// fixups, then loops verify-and-fix, and rasterizes a page only as a last
// resort. It always returns its best attempt, even if some violations remain, as
// a [ConvertResult] carrying the output, the final verify [Result], and any
// residual issues. ConvertEach streams a large batch through a callback so every
// output need not stay resident at once.
//
// A large output spills to a temp file rather than staying resident in the heap,
// so read it with [ConvertResult.Output], stream it with WriteTo or Save, and
// call [ConvertResult.Close] when done to release the backing. Close is your
// responsibility for results from Convert and ConvertAll; ConvertEach closes each
// result after its callback returns.
//
// # Profiles
//
// A [Profile] is the set of checks a Verify or Convert applies. [PDFA1B] is the
// full PDF/A-1b profile; [PDF] runs only the generic ISO 32000 object-model
// checks. Profiles are immutable — AddCheck, RemoveCheck and Clear each return a
// clone — so a caller can narrow the rule set without disturbing the shared
// defaults. The [Checks] registry names every selectable check.
//
// # Options, limits, and cancellation
//
// The two-argument entry points cover the common case; the ...Context forms add
// cancellation and an [Options] value (password, raster DPI, iteration bound,
// batch worker count, and an optional fidelity check). [SetLimits] configures
// process-wide resource caps, such as the maximum decoded stream size.
//
// # Concurrency
//
// A [Document] holds parser caches and is not safe for concurrent use; open one
// per goroutine. Everything else is:
//
//   - The package-level [Verify], [Convert] and their variants each open their
//     own document, so any number of goroutines may call them at once.
//   - A [Profile] is immutable (AddCheck, RemoveCheck and Clear clone), so one
//     profile may be shared by concurrent calls. An [Options] value is likewise
//     only read.
//   - A [Result] is immutable once returned. A [ConvertResult] may be read
//     (Output, WriteTo, Save) from several goroutines, but its Close must not
//     run concurrently with those reads.
//   - [SetLimits] and [CurrentLimits] are safe from any goroutine; a change
//     applies to the next stream decoded, not to work already in flight.
//
// The batch helpers VerifyAll, ConvertAll and ConvertEach are internally
// concurrent and meant to be called once from a single goroutine; ConvertEach
// invokes its callback serially.
//
// # Platform support
//
// On unix (Linux, macOS, the BSDs) Open memory-maps the file, so verification
// and conversion stay bounded in resident memory and handle files larger than
// RAM. Windows has no memory mapping here and falls back to an incremental
// seek/ReadAt read path: it produces byte-for-byte identical results (verified
// against the mmap path over both conformance corpora), but does not offer the
// larger-than-RAM guarantee. On js/wasm only the *Bytes entry points apply.
package gopdfrab
