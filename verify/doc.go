// Package verify checks PDF files for PDF/A-1b conformance, without the
// conversion half of gopdfrab.
//
// It is the verification API of the root gopdfrab package and nothing else:
// the same functions under the same names, over the same types. A program that
// only verifies imports this package instead of gopdfrab and never compiles or
// links the converter -- no fixers, writer, rasterizer, substitute fonts or ICC
// profiles. The linker already drops most of what a verify-only caller of
// gopdfrab leaves unused, so the saving there is moderate (about a sixth of
// the binary); against a build that also converts, such as the browser
// module, it is more than half. Switching is an import swap:
//
//	res, err := verify.Verify("in.pdf", verify.PDFA1B)
//	if err != nil {
//		// the file could not be opened or parsed
//	}
//	fmt.Println(res.Valid, len(res.Issues))
//
// [Result], [Profile], [Check], [PDFError] and the other data types are
// aliases of the very types gopdfrab exposes, so values pass freely between a
// program's verify-only and converting parts. The profile variables [PDF],
// [PDFA1B] and [Legacy1B] start out as the same profiles as gopdfrab's, but
// they are separate variables: reassigning one here does not change the
// default gopdfrab uses, or the other way round. [SetLimits] is process-wide
// and shared with gopdfrab.
//
// # Concurrency
//
// A [Document] holds parser caches and is not safe for concurrent use; open one
// per goroutine. The package-level [Verify] and its variants each open their
// own document, so any number of goroutines may call them at once. A [Profile]
// and an [Options] value are only read, and a [Result] is immutable once
// returned. VerifyAll is internally concurrent and meant to be called once
// from a single goroutine.
//
// # Platform support
//
// On unix Open memory-maps the file, so verification stays bounded in resident
// memory and handles files larger than RAM. Windows falls back to a seek/ReadAt
// read path with byte-for-byte identical results but no larger-than-RAM
// guarantee. On js/wasm only the *Bytes entry points apply.
package verify
