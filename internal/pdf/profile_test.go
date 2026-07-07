package pdf

import (
	"strings"
	"testing"
)

func TestProfileString(t *testing.T) {
	p := NewProfile(A_1B)
	s := p.String()
	if !strings.Contains(s, "Profile{") || !strings.Contains(s, "enabled:") {
		t.Errorf("Profile.String() = %q", s)
	}
}

// TestProfileClearAddCheckHas covers Clear (empties enabled, keeps flags),
// AddCheck (enables new checks without mutating the receiver), and Has.
func TestProfileClearAddCheckHas(t *testing.T) {
	c := Checks.Structure.ObjectFraming

	full := NewFullProfile(A_1B)
	full.SkipUnreachableXObjects = true
	if !full.Has(c) {
		t.Fatal("full profile should have every check enabled")
	}

	cleared := full.Clear()
	if cleared.Has(c) {
		t.Error("Clear() should disable all checks")
	}
	if !cleared.SkipUnreachableXObjects {
		t.Error("Clear() should preserve behavioral flags")
	}
	if len(cleared.Checks()) != 0 {
		t.Errorf("Clear() profile Checks() = %v, want empty", cleared.Checks())
	}

	added := cleared.AddCheck(c)
	if !added.Has(c) {
		t.Error("AddCheck() should enable the given check")
	}
	if cleared.Has(c) {
		t.Error("AddCheck() must not mutate the receiver")
	}
}

// TestProfileChecksNonEmpty covers Checks()'s enabled-check append branch.
func TestProfileChecksNonEmpty(t *testing.T) {
	full := NewFullProfile(A_1B)
	if len(full.Checks()) == 0 {
		t.Error("expected a full profile's Checks() to be non-empty")
	}
}

// TestProfileAllows covers both branches: a clause absent from the catalog
// is always allowed, and a cataloged clause follows the profile's enabled state.
func TestProfileAllows(t *testing.T) {
	full := NewFullProfile(A_1B)
	if !full.Allows("not.a.real.clause", 0) {
		t.Error("Allows() for an unknown clause should default to true")
	}
	if !full.Allows("6.1.7", 3) {
		t.Error("Allows() should be true for an enabled cataloged clause")
	}
}

// TestPDFA1BDisablesKeyIntroducedAfterPDF14 documents the veraPDF divergence:
// PDFA_1B drops this check (structural/informational post-1.4 keys like
// FileTrailer.XRefStm are ignorable by a 1.4 reader and veraPDF does not flag
// them), while Legacy_1B keeps the full, spec-literal catalog.
func TestPDFA1BDisablesKeyIntroducedAfterPDF14(t *testing.T) {
	c := Checks.ObjectModel.KeyIntroducedAfterPDF14
	if PDFA_1B.Has(c) {
		t.Error("PDFA_1B should not enforce KeyIntroducedAfterPDF14")
	}
	if !Legacy_1B.Has(c) {
		t.Error("Legacy_1B should enforce KeyIntroducedAfterPDF14")
	}
}
