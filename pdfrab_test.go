package pdfrab

import (
	"os"
	"testing"
)

// TestRegistryPassthroughs exercises the check-registry facade functions,
// which need no PDF fixture.
func TestRegistryPassthroughs(t *testing.T) {
	if p := NewProfile(A_1B); p == nil {
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

	if _, err := Verify(path, PDFA_1B); err != nil {
		t.Errorf("Verify: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if _, err := VerifyBytes(data, PDFA_1B); err != nil {
		t.Errorf("VerifyBytes: %v", err)
	}

	results, err := VerifyAll([]string{path}, PDFA_1B)
	if err != nil {
		t.Errorf("VerifyAll: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("VerifyAll returned %d results, want 1", len(results))
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
	if n, err := doc.GetPageCount(); err != nil {
		t.Errorf("GetPageCount: %v", err)
	} else if n < 1 {
		t.Errorf("GetPageCount = %d, want >= 1", n)
	}
	if v, err := doc.GetVersion(); err != nil {
		t.Errorf("GetVersion: %v", err)
	} else if v == "" {
		t.Error("GetVersion returned empty string")
	}
	// Info is optional in PDF/A, so a missing dictionary is not a failure;
	// exercise the facade either way.
	doc.GetMetadata()

	// IsPDFA's error path only fires when the underlying verify fails, which
	// for a fixed profile means an undefined conformance level. Swap PDFA_1B
	// to drive that branch, then restore it.
	saved := PDFA_1B
	PDFA_1B = NewProfile(Undefined)
	if ok, err := doc.IsPDFA(); err == nil || ok {
		t.Errorf("IsPDFA with undefined profile = (%v, %v), want (false, error)", ok, err)
	}
	PDFA_1B = saved

	if _, err := Open("testdata/does-not-exist.pdf"); err == nil {
		t.Error("Open of a missing file returned nil error")
	}
}
