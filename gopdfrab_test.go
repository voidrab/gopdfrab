package gopdfrab

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestRegistryPassthroughs exercises the check-registry facade functions,
// which need no PDF fixture.
func TestRegistryPassthroughs(t *testing.T) {
	if p := NewProfile(A1B); p == nil {
		t.Fatal("NewProfile returned nil")
	}

	all := AllChecks()
	if len(all) == 0 {
		t.Fatal("AllChecks returned no checks")
	}

	c := all[0]
	if got, ok := CheckByClause(c.Clause(), c.Subclause()); !ok {
		t.Errorf("CheckByClause(%q, %d) not found", c.Clause(), c.Subclause())
	} else if got.Clause() != c.Clause() {
		t.Errorf("CheckByClause returned clause %q, want %q", got.Clause(), c.Clause())
	}

	if len(ChecksForClause(c.Clause())) == 0 {
		t.Errorf("ChecksForClause(%q) returned nothing", c.Clause())
	}
}

// TestVerifyWrappers exercises the file, in-memory, and batch verify facades.
func TestVerifyWrappers(t *testing.T) {
	paths := passFixtures(t)
	if len(paths) == 0 {
		t.Skip("veraPDF suite not present")
	}
	path := paths[0]

	if _, err := Verify(path, PDFA1B); err != nil {
		t.Errorf("Verify: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if _, err := VerifyBytes(data, PDFA1B); err != nil {
		t.Errorf("VerifyBytes: %v", err)
	}

	results, err := VerifyAll([]string{path}, PDFA1B)
	if err != nil {
		t.Errorf("VerifyAll: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("VerifyAll returned %d results, want 1", len(results))
	}
}

// plainPDF is a minimal one-page PDF with no PDF/A structure but a
// well-formed base object model, so object-model-only checks pass on it.
const plainPDF = "%PDF-1.4\n" +
	"1 0 obj\n<</Type/Catalog/Pages 2 0 R>>\nendobj\n" +
	"2 0 obj\n<</Type/Pages/Kids[3 0 R]/Count 1>>\nendobj\n" +
	"3 0 obj\n<</Type/Page/Parent 2 0 R/MediaBox[0 0 595 842]>>\nendobj\n" +
	"xref\n0 4\n" +
	"0000000000 65535 f \n" +
	"0000000009 00000 n \n" +
	"0000000054 00000 n \n" +
	"0000000105 00000 n \n" +
	"trailer\n<</Size 4/Root 1 0 R>>\n" +
	"startxref\n170\n%%EOF"

// TestObjectModelWrappers exercises every ObjectModel-related facade
// function, independent of any PDF/A profile or corpus fixture.
func TestObjectModelWrappers(t *testing.T) {
	if p := ObjectModelOnly(); p == nil {
		t.Fatal("ObjectModelOnly returned nil")
	} else if p.Level != ObjectModel {
		t.Errorf("ObjectModelOnly Level = %v, want %v", p.Level, ObjectModel)
	}

	data := []byte(plainPDF)

	res, err := VerifyObjectModelBytes(data)
	if err != nil {
		t.Fatalf("VerifyObjectModelBytes: %v", err)
	}
	if res.Type != ObjectModel {
		t.Errorf("VerifyObjectModelBytes Type = %v, want %v", res.Type, ObjectModel)
	}
	if !res.Valid {
		t.Errorf("VerifyObjectModelBytes Valid = false, issues: %v", res.Issues)
	}

	path := filepath.Join(t.TempDir(), "plain.pdf")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	res, err = VerifyObjectModel(path)
	if err != nil {
		t.Fatalf("VerifyObjectModel: %v", err)
	}
	if !res.Valid {
		t.Errorf("VerifyObjectModel Valid = false, issues: %v", res.Issues)
	}

	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	res, err = doc.VerifyObjectModel()
	if err != nil {
		t.Fatalf("Document.VerifyObjectModel: %v", err)
	}
	if !res.Valid {
		t.Errorf("Document.VerifyObjectModel Valid = false, issues: %v", res.Issues)
	}
}

// TestDocumentAccessors exercises Open and every Document accessor facade,
// including Open's error path.
func TestDocumentAccessors(t *testing.T) {
	paths := passFixtures(t)
	if len(paths) == 0 {
		t.Skip("veraPDF suite not present")
	}

	doc, err := Open(paths[0])
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	if _, err := doc.IsPDFA(); err != nil {
		t.Errorf("IsPDFA: %v", err)
	}
	if _, err := doc.IsPDF(); err != nil {
		t.Errorf("IsPDF: %v", err)
	}
	if xmp, err := doc.XMPMetadata(); err != nil {
		t.Errorf("XMPMetadata: %v", err)
	} else if len(xmp) == 0 {
		t.Error("XMPMetadata returned empty packet")
	}
	if part, _, err := doc.ClaimedConformance(); err != nil {
		t.Errorf("ClaimedConformance: %v", err)
	} else if part != "1" {
		t.Errorf("ClaimedConformance part = %q, want \"1\"", part)
	}
	if n, err := doc.PageCount(); err != nil {
		t.Errorf("PageCount: %v", err)
	} else if n < 1 {
		t.Errorf("PageCount = %d, want >= 1", n)
	}
	if v, err := doc.Version(); err != nil {
		t.Errorf("Version: %v", err)
	} else if v == "" {
		t.Error("Version returned empty string")
	}
	// Info is optional in PDF/A, so a missing dictionary is not a failure;
	// exercise the facade either way.
	doc.Metadata()

	// The IsPDFA/IsPDF error paths only fire when the underlying verify
	// fails, which for a fixed profile means an undefined conformance level.
	// Swap the profile variables to drive those branches, then restore them.
	savedPDFA := PDFA1B
	savedPDF := PDF
	PDFA1B = NewProfile(Undefined)
	PDF = NewProfile(Undefined)
	if ok, err := doc.IsPDFA(); err == nil || ok {
		t.Errorf("IsPDFA with undefined profile = (%v, %v), want (false, error)", ok, err)
	}
	if ok, err := doc.IsPDF(); err == nil || ok {
		t.Errorf("IsPDF with undefined profile = (%v, %v), want (false, error)", ok, err)
	}
	PDFA1B = savedPDFA
	PDF = savedPDF

	if _, err := Open("testdata/does-not-exist.pdf"); err == nil {
		t.Error("Open of a missing file returned nil error")
	}
}

// TestResultMarshalsToJSON confirms the public Result/PDFError/Check aliases
// serialize to a populated, stable JSON shape rather than the empty objects
// their unexported fields used to produce (roadmap item 17).
func TestResultMarshalsToJSON(t *testing.T) {
	data, err := os.ReadFile("internal/pdf/testdata/crypt/enc_aesv3.pdf")
	if err != nil {
		t.Skipf("fixture absent: %v", err)
	}
	res, err := VerifyBytes(data, PDFA1B)
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
	if res.Valid || len(res.Issues) == 0 {
		t.Fatalf("expected an invalid result with issues (6.1.3 Encrypt), got valid=%v issues=%d", res.Valid, len(res.Issues))
	}

	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal(Result): %v", err)
	}
	if bytes.Contains(b, []byte("{}")) {
		t.Fatalf("result JSON still contains empty objects: %s", b)
	}

	var decoded struct {
		Valid      bool `json:"valid"`
		IssueCount int  `json:"issueCount"`
		Issues     []struct {
			Check struct {
				Name   string `json:"name"`
				Clause string `json:"clause"`
			} `json:"check"`
			Text string `json:"text"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.IssueCount != len(res.Issues) || len(decoded.Issues) == 0 {
		t.Fatalf("issueCount/issues mismatch: %s", b)
	}
	if decoded.Issues[0].Check.Clause == "" || decoded.Issues[0].Text == "" {
		t.Errorf("issue check/text not populated: %s", b)
	}
}
