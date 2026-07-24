package pdf

import "sync/atomic"

// DefaultMaxDecodedStreamBytes caps a single stream's decoded output
// (Flate / LZW / RunLength), guarding against decompression bombs.
const DefaultMaxDecodedStreamBytes int64 = 256 << 20

// decodedStreamCap holds the configured cap; 0 means DefaultMaxDecodedStreamBytes.
// Read by every decode chokepoint, so one value enforces the cap uniformly across
// all call sites, including the many that hold no Reader. Set at startup, before
// concurrent verify/convert; atomic access keeps even a mid-flight change
// race-free, though the value is not transactional across a single decode.
var decodedStreamCap atomic.Int64

func effectiveDecodedCap() int64 {
	if v := decodedStreamCap.Load(); v > 0 {
		return v
	}
	return DefaultMaxDecodedStreamBytes
}

// SetMaxDecodedStreamBytes sets the process-wide decoded-output cap. A value of
// zero or less restores the default.
func SetMaxDecodedStreamBytes(n int64) {
	if n < 0 {
		n = 0
	}
	decodedStreamCap.Store(n)
}

// MaxDecodedStreamBytes returns the decoded-output cap in effect.
func MaxDecodedStreamBytes() int64 { return effectiveDecodedCap() }
