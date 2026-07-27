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

// DefaultMaxResidentBytes caps what one Reader keeps in its derived caches --
// decoded stream bytes and tokenized content. It is a working-set budget, not a
// correctness limit: past it a Reader stops memoizing and starts recomputing,
// which costs time and changes nothing about the result.
//
// 64 MB leaves the largest committed corpus file (~4 MB of caches) sixteen times
// the room it needs, so the default binds only on documents whose content is
// genuinely huge -- exactly the ones a default should bound. It was 256 MB while
// the budget under-counted what it was measuring; a 100 MB vector document spent
// all of that on decoded page content and peaked near a gigabyte.
const DefaultMaxResidentBytes int64 = 64 << 20

// residentCap holds the configured budget; 0 means DefaultMaxResidentBytes.
// Global for the same reason decodedStreamCap is: the caches are filled from
// call sites throughout verify and convert, and one value bounds them all.
var residentCap atomic.Int64

func effectiveResidentCap() int64 {
	if v := residentCap.Load(); v > 0 {
		return v
	}
	return DefaultMaxResidentBytes
}

// SetMaxResidentBytes sets the process-wide per-Reader cache budget. A value of
// zero or less restores the default, matching SetMaxDecodedStreamBytes -- so a
// partially-filled Limits value never silently disables anything. To switch
// memoization off, pass 1: no entry fits a one-byte budget.
func SetMaxResidentBytes(n int64) {
	if n < 0 {
		n = 0
	}
	residentCap.Store(n)
}

// MaxResidentBytes returns the cache budget in effect.
func MaxResidentBytes() int64 { return effectiveResidentCap() }

// cacheHasRoom reports whether d may memoize another n bytes. A single entry
// larger than the whole budget is never cached.
func (d *Reader) cacheHasRoom(n int64) bool {
	return d.decodedBytes+d.scanBytes+n <= effectiveResidentCap()
}
