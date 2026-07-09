package pdf

import (
	"fmt"
	"maps"
)

type LevelType string

const (
	Undefined LevelType = "undefined"
	A_1B      LevelType = "A-1b"
	// ObjectModel is a reporting-only level for the generic ISO 32000
	// object-model checks (see ObjectModelOnly), independent of any PDF/A level.
	ObjectModel LevelType = "ObjectModel"
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

	// SkipUnusedSimpleFonts, when true, only reports 6.3.4 (SimpleNotEmbedded)
	// for simple fonts actually shown in content. Fonts in AcroForm /DR that
	// are never drawn are silently ignored, matching veraPDF's interpretation.
	// Legacy_1B keeps this false so every referenced non-embedded font is flagged.
	SkipUnusedSimpleFonts bool
}

// PDF is the default profile for generic ISO 32000 object-model checks.
var PDF *Profile

// PDFA_1B is the default PDF/A-1b profile, tuned to match veraPDF's
// interpretation of the spec. Used by Verify(A_1B).
var PDFA_1B *Profile

// Legacy_1B is the strict, fully spec-literal PDF/A-1b profile: every check
// enabled, every Form XObject checked regardless of reachability. Matches the
// Isartor suite's interpretation, which is stricter than veraPDF's in places.
var Legacy_1B *Profile

func init() {
	PDF = ObjectModelOnly()
	Legacy_1B = NewFullProfile(A_1B)

	// PDFA_1B adjusts the full profile for veraPDF's divergences from the
	// stricter legacy/Isartor interpretation: unreachable Form XObjects are
	// out-of-scope (6.2.3.3, 6.2.10); 6.2.7 PostScript XObject checks are
	// disabled (veraPDF's own corpus intentionally includes one in a pass
	// file); 6.3.4 simple-font embedding is only required for fonts actually
	// shown in content (SkipUnusedSimpleFonts), not for fonts in AcroForm /DR.
	// KeyIntroducedAfterPDF14 is disabled: real-world files carry post-1.4
	// keys that are purely structural/informational (e.g. FileTrailer's
	// hybrid-reference XRefStm, Catalog's Extensions) and are ignorable by a
	// PDF 1.4 reader, but Arlington has no data distinguishing those from
	// keys that actually change required interpretation -- veraPDF does not
	// flag them, so this stays Legacy_1B-only (spec-literal) for now.
	PDFA_1B = NewFullProfile(A_1B)
	PDFA_1B.SkipUnreachableXObjects = true
	PDFA_1B.SkipUnusedSimpleFonts = true
	PDFA_1B = PDFA_1B.RemoveCheck(
		Checks.Image.FormPostScript,
		Checks.Image.PostScriptXObject,
		Checks.ObjectModel.KeyIntroducedAfterPDF14,
	)
}

// NewProfile returns an empty profile for the given conformance level.
func NewProfile(level LevelType) *Profile {
	return &Profile{Level: level, enabled: make(map[int]bool)}
}

// ObjectModelOnly returns a profile enabling only the generic ISO 32000
// object-model checks (MissingRequiredKey, WrongValueType, DisallowedValue,
// IndirectRequired, KeyIntroducedAfterPDF14, ConstraintViolated), with every
// PDF/A-specific check disabled -- useful for asking "is this even valid PDF"
// independent of any PDF/A conformance level.
func ObjectModelOnly() *Profile {
	return NewProfile(ObjectModel).AddCheck(
		Checks.ObjectModel.MissingRequiredKey,
		Checks.ObjectModel.WrongValueType,
		Checks.ObjectModel.DisallowedValue,
		Checks.ObjectModel.IndirectRequired,
		Checks.ObjectModel.KeyIntroducedAfterPDF14,
		Checks.ObjectModel.ConstraintViolated,
	)
}

func NewFullProfile(level LevelType) *Profile {
	all := AllChecks()
	p := &Profile{
		Level:   level,
		enabled: make(map[int]bool, len(all)),
	}
	for _, c := range all {
		p.enabled[c.ID()] = true
	}
	return p
}

func (p *Profile) Clone() *Profile {
	out := &Profile{
		Level:                   p.Level,
		enabled:                 make(map[int]bool, len(p.enabled)),
		SkipUnreachableXObjects: p.SkipUnreachableXObjects,
		SkipUnusedSimpleFonts:   p.SkipUnusedSimpleFonts,
	}
	maps.Copy(out.enabled, p.enabled)
	return out
}

// Clear returns a new profile with the same conformance level but no checks
// enabled. Behavioral flags (SkipUnreachableXObjects, SkipUnusedSimpleFonts)
// are preserved.
func (p *Profile) Clear() *Profile {
	return &Profile{
		Level:                   p.Level,
		enabled:                 make(map[int]bool),
		SkipUnreachableXObjects: p.SkipUnreachableXObjects,
		SkipUnusedSimpleFonts:   p.SkipUnusedSimpleFonts,
	}
}

// AddCheck returns a new profile with the given checks added to the enabled
// set.
func (p *Profile) AddCheck(checks ...Check) *Profile {
	out := p.Clone()
	for _, c := range checks {
		out.enabled[c.ID()] = true
	}
	return out
}

// RemoveCheck returns a new profile with the given checks removed from the
// enabled set.
func (p *Profile) RemoveCheck(checks ...Check) *Profile {
	out := p.Clone()
	for _, c := range checks {
		delete(out.enabled, c.ID())
	}
	return out
}

// Checks returns the list of currently enabled checks in catalog order.
func (p *Profile) Checks() []Check {
	var out []Check
	for _, c := range AllChecks() {
		if p.enabled[c.ID()] {
			out = append(out, c)
		}
	}
	return out
}

// Has reports whether check c is currently enabled in this profile.
func (p *Profile) Has(c Check) bool {
	return p.enabled[c.ID()]
}

// OnlyObjectModelChecks reports whether p enables no check outside the generic
// object-model group, so a verifier may skip every PDF/A-specific check family
// whose findings would be filtered out anyway.
func (p *Profile) OnlyObjectModelChecks() bool {
	for _, c := range p.Checks() {
		if c.Clause() != ObjectModelClause {
			return false
		}
	}
	return true
}

func (p *Profile) Allows(clause string, subclause int) bool {
	c, inCatalog := CheckByClause(clause, subclause)
	if !inCatalog {
		return true
	}
	return p.enabled[c.ID()]
}

// String returns a human-readable summary of the profile.
func (p *Profile) String() string {
	return fmt.Sprintf("Profile{Level:%s enabled:%d/%d}", p.Level, len(p.enabled), len(AllChecks()))
}
