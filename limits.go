package gopdfrab

import "github.com/voidrab/gopdfrab/internal/pdf"

// Limits holds gopdfrab's configurable resource caps. The zero value means the
// built-in defaults.
type Limits struct {
	// MaxDecodedStreamBytes caps a single stream's decoded output
	// (Flate/LZW/RunLength), guarding against decompression bombs. A value of
	// zero or less means the default (256 MB).
	MaxDecodedStreamBytes int64
}

// DefaultLimits returns the built-in resource caps.
func DefaultLimits() Limits {
	return Limits{MaxDecodedStreamBytes: pdf.DefaultMaxDecodedStreamBytes}
}

// CurrentLimits returns the resource caps in effect.
func CurrentLimits() Limits {
	return Limits{MaxDecodedStreamBytes: pdf.MaxDecodedStreamBytes()}
}

// SetLimits applies l process-wide. A non-positive field resets that cap to its
// default. The caps are global rather than per-call because they are enforced
// at decode chokepoints reached from many callers that hold no document handle;
// set them once at startup, before concurrent verify/convert.
func SetLimits(l Limits) {
	pdf.SetMaxDecodedStreamBytes(l.MaxDecodedStreamBytes)
}
