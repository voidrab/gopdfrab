package gopdfrab

import "github.com/voidrab/gopdfrab/internal/pdf"

// Limits holds gopdfrab's configurable resource caps. The zero value means the
// built-in defaults.
type Limits struct {
	// MaxDecodedStreamBytes caps a single stream's decoded output
	// (Flate/LZW/RunLength), guarding against decompression bombs. A value of
	// zero or less means the default (256 MB).
	MaxDecodedStreamBytes int64

	// MaxResidentBytes caps what one open document keeps in the caches it
	// could rebuild -- decoded stream bytes and tokenized page content. It is a
	// speed/memory dial, not a correctness limit: past the budget a document
	// recomputes instead of remembering, and the verdict is identical either
	// way. A value of zero or less means the default (64 MB); pass 1 to switch
	// memoization off, since no entry fits a one-byte budget.
	//
	// The default is far above what an ordinary document needs, so leaving it
	// alone changes nothing. Lower it when converting large documents in
	// parallel, where the footprint multiplies by the worker count; raise it
	// when converting one document with hundreds of megabytes of page content
	// and memory to spare.
	MaxResidentBytes int64
}

// DefaultLimits returns the built-in resource caps.
func DefaultLimits() Limits {
	return Limits{
		MaxDecodedStreamBytes: pdf.DefaultMaxDecodedStreamBytes,
		MaxResidentBytes:      pdf.DefaultMaxResidentBytes,
	}
}

// CurrentLimits returns the resource caps in effect.
func CurrentLimits() Limits {
	return Limits{
		MaxDecodedStreamBytes: pdf.MaxDecodedStreamBytes(),
		MaxResidentBytes:      pdf.MaxResidentBytes(),
	}
}

// SetLimits applies l process-wide. A non-positive field resets that cap to its
// default. The caps are global rather than per-call because they are enforced
// at decode chokepoints reached from many callers that hold no document handle;
// set them once at startup, before concurrent verify/convert.
//
// SetLimits and CurrentLimits may be called from any goroutine (the caps are
// atomics), but a change takes effect on the next stream decoded, so calling it
// mid-run leaves in-flight work split across the old and new cap.
func SetLimits(l Limits) {
	pdf.SetMaxDecodedStreamBytes(l.MaxDecodedStreamBytes)
	pdf.SetMaxResidentBytes(l.MaxResidentBytes)
}
