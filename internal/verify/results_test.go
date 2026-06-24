package verify

import (
	"os"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/check"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

const sampleIsartorFailFile = "../../test documents/Isartor testsuite/PDFA-1b/6.4 Transparency/isartor-6-4-t01-fail-a.pdf"
const sampleVeraPassFile = "../../test documents/veraPDF/PDF_A-1b/6.4 Transparency/veraPDF test suite 6-4-t01-pass-a.pdf"

func TestClauseLess(t *testing.T) {
	cases := []struct{ a, b string }{
		{"6.2.9", "6.2.10"},
		{"6.1.2", "6.1.3"},
		{"6.1", "6.1.2"},
		{"6.2.10", "6.3.1"},
	}
	for _, c := range cases {
		if !clauseLess(c.a, c.b) {
			t.Errorf("clauseLess(%q, %q) = false, want true", c.a, c.b)
		}
		if clauseLess(c.b, c.a) {
			t.Errorf("clauseLess(%q, %q) = true, want false", c.b, c.a)
		}
	}
}

func TestPDFErrorAccessorsAndCheck(t *testing.T) {
	if _, err := os.Stat(sampleIsartorFailFile); err != nil {
		t.Skip("Isartor sample file not present")
	}

	doc, err := pdf.Open(sampleIsartorFailFile)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	res, err := VerifyProfile(doc, Legacy_1B)
	if err != nil {
		t.Fatalf("VerifyProfile: %v", err)
	}
	if res.Valid || len(res.Issues) == 0 {
		t.Fatalf("expected non-conformant result with issues, got Valid=%v Issues=%d", res.Valid, len(res.Issues))
	}

	issue := res.Issues[0]
	c := issue.Check()
	if c.Clause() == "" {
		t.Error("check.Check().Clause() returned empty string")
	}
	if c.Subclause() < 0 {
		t.Error("check.Check().Subclause() returned negative value")
	}
	if c.Name() == "" {
		t.Error("check.Check() has empty Name()")
	}
	if got, want := issue.IsDocumentLevel(), issue.Page() == 0; got != want {
		t.Errorf("IsDocumentLevel() = %v, want %v", got, want)
	}
	if msgs := issue.Messages(); len(msgs) == 0 {
		t.Error("Messages() returned no messages")
	}

	got, ok := check.CheckByClause(c.Clause(), c.Subclause())
	if !ok || got != c {
		t.Errorf("check.CheckByClause(%s, %d) = %v, %v, want %v, true", c.Clause(), c.Subclause(), got, ok, c)
	}
}

func TestResultAggregation(t *testing.T) {
	if _, err := os.Stat(sampleIsartorFailFile); err != nil {
		t.Skip("Isartor sample file not present")
	}

	doc, err := pdf.Open(sampleIsartorFailFile)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	res, err := VerifyProfile(doc, Legacy_1B)
	if err != nil {
		t.Fatalf("VerifyProfile: %v", err)
	}
	if res.Count() != len(res.Issues) {
		t.Errorf("Count() = %d, want %d", res.Count(), len(res.Issues))
	}

	checks := res.Checks()
	if len(checks) == 0 {
		t.Fatal("check.Checks() returned none")
	}
	byCheck := res.IssuesByCheck()
	var total int
	for _, c := range checks {
		issues, ok := byCheck[c]
		if !ok || len(issues) == 0 {
			t.Errorf("IssuesByCheck()[%v] missing or empty", c.Name())
		}
		total += len(issues)
	}
	if total != len(res.Issues) {
		t.Errorf("IssuesByCheck() total = %d, want %d", total, len(res.Issues))
	}

	first := res.Issues[0]
	onPage := res.IssuesOnPage(first.Page())
	found := false
	for _, iss := range onPage {
		if iss.Check() == first.Check() {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("IssuesOnPage(%d) did not contain the issue it was sourced from", first.Page())
	}

	c := first.Check()
	forCheck := res.IssuesForCheck(c)
	if len(forCheck) == 0 {
		t.Errorf("IssuesForCheck(%v) returned none", c.Name())
	}

	summary := res.Summary()
	if !strings.Contains(summary, "invalid") {
		t.Errorf("Summary() = %q, want it to mention invalid", summary)
	}
	for _, c := range checks {
		if !strings.Contains(summary, c.Clause()) {
			t.Errorf("Summary() missing clause %q:\n%s", c.Clause(), summary)
		}
	}
}

func TestDocumentPDFAInspection(t *testing.T) {
	if _, err := os.Stat(sampleVeraPassFile); err != nil {
		t.Skip("veraPDF sample file not present")
	}

	doc, err := pdf.Open(sampleVeraPassFile)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	res, err := Verify(doc, A_1B)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !res.Valid {
		t.Error("Verify().Valid = false, want true for a veraPDF pass file")
	}

	part, conformance, err := doc.ClaimedConformance()
	if err != nil {
		t.Fatalf("ClaimedConformance: %v", err)
	}
	if part != "1" || conformance != "B" {
		t.Errorf("ClaimedConformance() = (%q, %q), want (\"1\", \"B\")", part, conformance)
	}

	xmp, err := doc.XMPMetadata()
	if err != nil {
		t.Fatalf("XMPMetadata: %v", err)
	}
	if len(xmp) == 0 {
		t.Error("XMPMetadata() returned empty bytes")
	}
	if !strings.Contains(string(xmp), "pdfaid") {
		t.Error("XMPMetadata() does not contain the pdfaid namespace")
	}
}

func TestCheckRegistryLookups(t *testing.T) {
	c, ok := check.CheckByClause(check.Checks.Font.SimpleNotEmbedded.Clause(), check.Checks.Font.SimpleNotEmbedded.Subclause())
	if !ok {
		t.Fatal("CheckByClause did not find check.Checks.Font.SimpleNotEmbedded")
	}
	if c.Name() != check.Checks.Font.SimpleNotEmbedded.Name() {
		t.Errorf("check.CheckByClause() = %q, want %q", c.Name(), check.Checks.Font.SimpleNotEmbedded.Name())
	}

	if _, ok := check.CheckByClause("9.9.9", 99); ok {
		t.Error("check.CheckByClause() found a check for a clause that shouldn't exist")
	}

	clause := check.Checks.Font.SimpleNotEmbedded.Clause()
	all := check.ChecksForClause(clause)
	if len(all) == 0 {
		t.Fatalf("check.ChecksForClause(%q) returned none", clause)
	}
	for _, c := range all {
		if c.Clause() != clause {
			t.Errorf("check.ChecksForClause(%q) returned check with clause %q", clause, c.Clause())
		}
	}
}
