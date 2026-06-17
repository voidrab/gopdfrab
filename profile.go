package pdfrab

import (
	"fmt"
	"maps"
	"strconv"
)

// Profile is a mutable set of enabled PDF/A checks associated with a
// conformance level. It determines which rules are enforced when
// VerifyProfile is called.
//
// Mutators (Clear, AddCheck, RemoveCheck) return a new *Profile, leaving the
// receiver unchanged:
//
//	// Start from the full profile and remove checks:
//	p := gopdfrab.PDFA_1B.RemoveCheck(
//	    gopdfrab.Checks.Structure.FileHeaderSignature,
//	    gopdfrab.Checks.Font.SimpleNotEmbedded,
//	)
//
//	// Start from an empty profile and add checks:
//	p := gopdfrab.PDFA_1B.Clear().
//	    AddCheck(gopdfrab.Checks.Transparency.ImageWithSoftMask)
type Profile struct {
	Level   LevelType
	enabled map[int]bool // set of enabled check IDs
}

var PDFA_1B *Profile

func init() {
	PDFA_1B = newFullProfile(A_1B)
}

// NewProfile returns an empty profile for the given conformance level.
func NewProfile(level LevelType) *Profile {
	return &Profile{Level: level, enabled: make(map[int]bool)}
}

func newFullProfile(level LevelType) *Profile {
	p := &Profile{
		Level:   level,
		enabled: make(map[int]bool, len(allChecksCatalog)),
	}
	for _, c := range allChecksCatalog {
		p.enabled[c.id] = true
	}
	return p
}

func (p *Profile) clone() *Profile {
	out := &Profile{
		Level:   p.Level,
		enabled: make(map[int]bool, len(p.enabled)),
	}
	maps.Copy(out.enabled, p.enabled)
	return out
}

// Clear returns a new profile with the same conformance level but no checks
// enabled.
func (p *Profile) Clear() *Profile {
	return &Profile{Level: p.Level, enabled: make(map[int]bool)}
}

// AddCheck returns a new profile with the given checks added to the enabled
// set.
func (p *Profile) AddCheck(checks ...Check) *Profile {
	out := p.clone()
	for _, c := range checks {
		out.enabled[c.id] = true
	}
	return out
}

// RemoveCheck returns a new profile with the given checks removed from the
// enabled set.
func (p *Profile) RemoveCheck(checks ...Check) *Profile {
	out := p.clone()
	for _, c := range checks {
		delete(out.enabled, c.id)
	}
	return out
}

// Checks returns the list of currently enabled checks in catalog order.
func (p *Profile) Checks() []Check {
	var out []Check
	for _, c := range allChecksCatalog {
		if p.enabled[c.id] {
			out = append(out, c)
		}
	}
	return out
}

// Has reports whether check c is currently enabled in this profile.
func (p *Profile) Has(c Check) bool {
	return p.enabled[c.id]
}

func (p *Profile) allows(clause string, subclause int) bool {
	key := clause + "/" + strconv.Itoa(subclause)
	c, inCatalog := catalogByPair[key]
	if !inCatalog {
		return true
	}
	return p.enabled[c.id]
}

// String returns a human-readable summary of the profile.
func (p *Profile) String() string {
	return fmt.Sprintf("Profile{Level:%s enabled:%d/%d}", p.Level, len(p.enabled), len(allChecksCatalog))
}
