package gopdfrab

import pubverify "github.com/voidrab/gopdfrab/verify"

// Limits holds gopdfrab's configurable resource caps. It is the same type as
// verify.Limits, which documents each field. The zero value means the
// built-in defaults.
//
// Fields:
//
//	MaxDecodedStreamBytes caps a single stream's decoded output (default 256 MB)
//	MaxResidentBytes      caps what one open document keeps in its caches (default 64 MB)
type Limits = pubverify.Limits

// DefaultLimits returns the built-in resource caps.
func DefaultLimits() Limits { return pubverify.DefaultLimits() }

// CurrentLimits returns the resource caps in effect.
func CurrentLimits() Limits { return pubverify.CurrentLimits() }

// SetLimits applies l process-wide, for verification and conversion alike. A
// non-positive field resets that cap to its default. See verify.SetLimits.
func SetLimits(l Limits) { pubverify.SetLimits(l) }
