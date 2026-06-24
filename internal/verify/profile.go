package verify

import (
	"fmt"
	"maps"

	"github.com/voidrab/gopdfrab/internal/check"
)

// Profile is a mutable set of enabled PDF/A checks for a conformance level,
// used by VerifyProfile. Mutators (Clear, AddCheck, RemoveCheck) return a new
// *Profile, leaving the receiver unchanged.
type Profile struct {
	Level   LevelType
	enabled map[int]bool // set of enabled check IDs

	// SkipUnreachableXObjects, when true, suppresses checks on Form XObjects
	// never invoked via Do from a reachable content stream. ISO 19005-1
	// (6.2.3.3, 6.2.10) applies to every Form XObject, so Legacy_1B keeps this
	// false; PDFA_1B sets it true to match veraPDF's lenient interpretation.
	SkipUnreachableXObjects bool
}

// PDFA_1B is the default PDF/A-1b profile, tuned to match veraPDF's
// interpretation of the spec. Used by Verify(A_1B).
var PDFA_1B *Profile

// Legacy_1B is the strict, fully spec-literal PDF/A-1b profile: every check
// enabled, every Form XObject checked regardless of reachability. Matches the
// Isartor suite's interpretation, which is stricter than veraPDF's in places.
var Legacy_1B *Profile

func init() {
	Legacy_1B = newFullProfile(A_1B)

	// PDFA_1B adjusts the full profile for veraPDF's divergences from the
	// stricter legacy/Isartor interpretation: unreachable Form XObjects are
	// out-of-scope (6.2.3.3, 6.2.10); 6.2.7 PostScript XObject checks are
	// disabled (veraPDF's own corpus intentionally includes one in a pass
	// file); and standard Type1 fonts referenced only in AcroForm DR/widget DA
	// strings aren't flagged as unembedded (6.3.4/1).
	PDFA_1B = newFullProfile(A_1B)
	PDFA_1B.SkipUnreachableXObjects = true
	PDFA_1B = PDFA_1B.RemoveCheck(
		check.Checks.Image.FormPostScript,
		check.Checks.Image.PostScriptXObject,
		check.Checks.Font.SimpleNotEmbedded,
	)
}

// NewProfile returns an empty profile for the given conformance level.
func NewProfile(level LevelType) *Profile {
	return &Profile{Level: level, enabled: make(map[int]bool)}
}

func newFullProfile(level LevelType) *Profile {
	all := check.AllChecks()
	p := &Profile{
		Level:   level,
		enabled: make(map[int]bool, len(all)),
	}
	for _, c := range all {
		p.enabled[c.ID()] = true
	}
	return p
}

func (p *Profile) clone() *Profile {
	out := &Profile{
		Level:                   p.Level,
		enabled:                 make(map[int]bool, len(p.enabled)),
		SkipUnreachableXObjects: p.SkipUnreachableXObjects,
	}
	maps.Copy(out.enabled, p.enabled)
	return out
}

// Clear returns a new profile with the same conformance level but no checks
// enabled. Behavioral flags (SkipUnreachableXObjects) are preserved.
func (p *Profile) Clear() *Profile {
	return &Profile{
		Level:                   p.Level,
		enabled:                 make(map[int]bool),
		SkipUnreachableXObjects: p.SkipUnreachableXObjects,
	}
}

// AddCheck returns a new profile with the given checks added to the enabled
// set.
func (p *Profile) AddCheck(checks ...check.Check) *Profile {
	out := p.clone()
	for _, c := range checks {
		out.enabled[c.ID()] = true
	}
	return out
}

// RemoveCheck returns a new profile with the given checks removed from the
// enabled set.
func (p *Profile) RemoveCheck(checks ...check.Check) *Profile {
	out := p.clone()
	for _, c := range checks {
		delete(out.enabled, c.ID())
	}
	return out
}

// Checks returns the list of currently enabled checks in catalog order.
func (p *Profile) Checks() []check.Check {
	var out []check.Check
	for _, c := range check.AllChecks() {
		if p.enabled[c.ID()] {
			out = append(out, c)
		}
	}
	return out
}

// Has reports whether check c is currently enabled in this profile.
func (p *Profile) Has(c check.Check) bool {
	return p.enabled[c.ID()]
}

func (p *Profile) allows(clause string, subclause int) bool {
	c, inCatalog := check.CheckByClause(clause, subclause)
	if !inCatalog {
		return true
	}
	return p.enabled[c.ID()]
}

// String returns a human-readable summary of the profile.
func (p *Profile) String() string {
	return fmt.Sprintf("Profile{Level:%s enabled:%d/%d}", p.Level, len(p.enabled), len(check.AllChecks()))
}
