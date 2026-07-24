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
// a [ConvertResult] carrying the output bytes, the final verify [Result], and any
// residual issues. ConvertEach streams a large batch through a callback so every
// output need not stay resident at once.
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
// per goroutine. The batch helpers VerifyAll, ConvertAll and ConvertEach are
// internally concurrent and meant to be called once from a single goroutine.
package gopdfrab
