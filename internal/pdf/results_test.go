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
