package pdf

import (
	"strings"
	"testing"
)

// TestClauseLess covers segment-by-segment numeric comparison, including the
// "6.2.9" vs "6.2.10" case lexicographic comparison would get wrong, and the
// non-numeric-segment and differing-length fallbacks.
func TestClauseLess(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"6.2.9", "6.2.10", true},
		{"6.2.10", "6.2.9", false},
		{"6.1", "6.1.2", true},
		{"6.1.2", "6.1.2", false},
		{"a.1", "b.1", true},  // non-numeric segment falls back to string compare
		{"a.1", "a.2", true},  // equal non-numeric segment: continue to next segment
		{"a.2", "a.1", false}, // equal non-numeric segment: continue to next segment
	}
	for _, tc := range tests {
		if got := ClauseLess(tc.a, tc.b); got != tc.want {
			t.Errorf("ClauseLess(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestResultAccessors covers Count, Checks (dedup + clause order), IssuesByCheck,
// IssuesOnPage, IssuesForCheck, and Summary across a multi-issue Result.
func TestResultAccessors(t *testing.T) {
	c1 := Checks.Structure.FileHeaderSignature // clause 6.1.2
	c2 := Checks.Structure.ObjectFraming       // clause 6.1.8

	r := Result{
		Type:  A1B,
		Valid: false,
		Issues: []PDFError{
			NewError(c2, []error{}, 1, nil),
			NewError(c1, []error{}, 0, nil),
			NewError(c2, []error{}, 2, nil),
		},
	}

	if r.Count() != 3 {
		t.Errorf("Count() = %d, want 3", r.Count())
	}

	checks := r.Checks()
	if len(checks) != 2 || checks[0] != c1 || checks[1] != c2 {
		t.Errorf("Checks() = %v, want [c1, c2] in clause order (6.1.2 before 6.1.8)", checks)
	}

	byCheck := r.IssuesByCheck()
	if len(byCheck[c2]) != 2 || len(byCheck[c1]) != 1 {
		t.Errorf("IssuesByCheck() = %v, want 2 issues for c2 and 1 for c1", byCheck)
	}

	if onPage1 := r.IssuesOnPage(1); len(onPage1) != 1 {
		t.Errorf("IssuesOnPage(1) = %v, want 1 issue", onPage1)
	}
	if onPage0 := r.IssuesOnPage(0); len(onPage0) != 1 {
		t.Errorf("IssuesOnPage(0) = %v, want 1 document-level issue", onPage0)
	}

	if forC2 := r.IssuesForCheck(c2); len(forC2) != 2 {
		t.Errorf("IssuesForCheck(c2) = %v, want 2 issues", forC2)
	}

	summary := r.Summary()
	if !strings.Contains(summary, "invalid (3 issues)") {
		t.Errorf("Summary() = %q, missing issue count", summary)
	}
	if !strings.Contains(summary, c1.Clause()) || !strings.Contains(summary, c2.Clause()) {
		t.Errorf("Summary() = %q, missing clause lines", summary)
	}

	valid := Result{Type: A1B, Valid: true}
	if got := valid.Summary(); !strings.Contains(got, "valid (no issues)") {
		t.Errorf("Summary() for valid result = %q", got)
	}
}

// TestResultChecksSameClauseSubclauseOrder covers Checks()'s subclause-level
// tiebreaker: two checks sharing a clause sort by Subclause().
func TestResultChecksSameClauseSubclauseOrder(t *testing.T) {
	c1 := Checks.Structure.StreamFileFilter // 6.1.7, subclause 2
	c2 := Checks.Structure.StreamFileSpec   // 6.1.7, subclause 1

	r := Result{Issues: []PDFError{
		NewError(c1, []error{}, 0, nil),
		NewError(c2, []error{}, 0, nil),
	}}
	checks := r.Checks()
	if len(checks) != 2 || checks[0] != c2 || checks[1] != c1 {
		t.Errorf("Checks() = %v, want [c2 (subclause 1), c1 (subclause 2)]", checks)
	}
}

// TestIssuesNumberedByTheirLevel verifies that an issue stamped with a level
// renders, sorts and summarizes by the clause that level gives its check: a
// Part 1 font clause (6.3.4) sorts after a Part 1 transparency one (6.4), but
// under PDF/A-2b the font rule (6.2.11.4.1) comes after transparency (6.2.10).
func TestIssuesNumberedByTheirLevel(t *testing.T) {
	font := Checks.Font.SimpleNotEmbedded
	blend := Checks.Transparency.BlendMode
	utf8 := Checks.Structure.NameNotUTF8 // Part 2 only

	issues := []PDFError{
		NewError(blend, nil, 1, nil),
		NewError(font, nil, 1, nil),
		NewError(utf8, nil, 0, nil),
	}
	if got := issues[0].Level(); got != Undefined {
		t.Errorf("unstamped Level() = %q, want %q", got, Undefined)
	}
	if got := issues[2].String(); !strings.HasPrefix(got, "PDF/A violation, document-level") {
		t.Errorf("unstamped Part 2 only issue renders %q, want no clause", got)
	}

	StampLevel(issues, A2B)
	if got := issues[0].Level(); got != A2B {
		t.Errorf("stamped Level() = %q, want %q", got, A2B)
	}
	if clause, sub := issues[0].Clause(); clause != "6.2.10" || sub != 1 {
		t.Errorf("stamped Clause() = %s/%d, want 6.2.10/1", clause, sub)
	}
	if got := issues[1].String(); !strings.HasPrefix(got, "PDF/A violation (6.2.11.4.1/1)") {
		t.Errorf("String() = %q, want the Part 2 clause", got)
	}

	r := Result{Type: A2B, Issues: issues}
	checks := r.Checks()
	if len(checks) != 3 || checks[0] != utf8 || checks[1] != blend || checks[2] != font {
		t.Errorf("Checks() = %v, want [NameNotUTF8 6.1.8, BlendMode 6.2.10, SimpleNotEmbedded 6.2.11.4.1]", checks)
	}
	want := "A-2b: invalid (3 issues)\n  6.1.8/1 NameNotUTF8: 1\n  6.2.10/1 BlendMode: 1\n  6.2.11.4.1/1 SimpleNotEmbedded: 1"
	if got := r.Summary(); got != want {
		t.Errorf("Summary() =\n%s\nwant\n%s", got, want)
	}

	r1 := Result{Type: A1B, Issues: issues[:2]}
	if checks := r1.Checks(); len(checks) != 2 || checks[0] != font || checks[1] != blend {
		t.Errorf("A1B Checks() = %v, want [SimpleNotEmbedded 6.3.4, BlendMode 6.4]", checks)
	}
}

// TestSummaryOmitsZeroSubclause verifies that a check numbered by its clause
// alone is summarized without a "/0".
func TestSummaryOmitsZeroSubclause(t *testing.T) {
	gray := Checks.Colour.DeviceColourContentStream // 6.2.4.3 under Part 2
	r := Result{Type: A2B, Issues: []PDFError{NewError(gray, nil, 1, nil)}}
	if got, want := r.Summary(), "A-2b: invalid (1 issue)\n  6.2.4.3 DeviceColourContentStream: 1"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
}

// TestLevelOfInvertsPartOf verifies that every level with a part maps back to
// itself, and that a PDFError stamped with a level that has none reports
// Undefined.
func TestLevelOfInvertsPartOf(t *testing.T) {
	for _, level := range []LevelType{A1B, A2B, ObjectModel, Undefined} {
		if got := levelOf(partOf(level)); got != level {
			t.Errorf("levelOf(partOf(%q)) = %q", level, got)
		}
	}
	issues := []PDFError{NewError(Checks.Structure.TrailerEOF, nil, 0, nil)}
	StampLevel(issues, LevelType("A-9z"))
	if got := issues[0].Level(); got != Undefined {
		t.Errorf("Level() after stamping an unknown level = %q, want %q", got, Undefined)
	}
}

// TestResultChecksBreaksSharedClauseTiesByName verifies that checks sharing a
// clause and subclause under a level sort by name, whatever order their
// issues were reported in.
func TestResultChecksBreaksSharedClauseTiesByName(t *testing.T) {
	a := Checks.Structure.StreamLengthIncludesEOL
	b := Checks.Structure.StreamLengthMismatch
	ca, sa, _ := a.ClauseIn(A2B)
	cb, sb, _ := b.ClauseIn(A2B)
	if ca != cb || sa != sb {
		t.Fatalf("%s is %s/%d and %s is %s/%d under A2B, want one shared pair", a.Name(), ca, sa, b.Name(), cb, sb)
	}
	for _, issues := range [][]PDFError{
		{NewError(a, nil, 0, nil), NewError(b, nil, 0, nil)},
		{NewError(b, nil, 0, nil), NewError(a, nil, 0, nil)},
	} {
		r := Result{Type: A2B, Issues: issues}
		if checks := r.Checks(); len(checks) != 2 || checks[0] != a || checks[1] != b {
			t.Errorf("Checks() = %v, want [%s, %s]", checks, a.Name(), b.Name())
		}
	}
}
