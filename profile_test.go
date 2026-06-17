package pdfrab

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestCatalog_AllChecksUnique(t *testing.T) {
	checks := AllChecks()
	if len(checks) == 0 {
		t.Fatal("AllChecks returned an empty catalog")
	}
	seenIDs := map[int]bool{}
	seenPairs := map[string]bool{}
	for _, c := range checks {
		if c.id == 0 {
			t.Errorf("check %q has zero (unregistered) ID", c.name)
		}
		if seenIDs[c.id] {
			t.Errorf("duplicate ID %d for check %q", c.id, c.name)
		}
		seenIDs[c.id] = true

		pair := c.clause + "/" + strconv.Itoa(c.subclause)
		if seenPairs[pair] {
			t.Errorf("duplicate (clause, subclause) pair %s for check %q", pair, c.name)
		}
		seenPairs[pair] = true

		if c.name == "" {
			t.Errorf("check with ID %d has empty name", c.id)
		}
		if c.description == "" {
			t.Errorf("check %q has empty description", c.name)
		}
		if c.clause == "" {
			t.Errorf("check %q has empty clause", c.name)
		}
	}
}

func TestCatalog_KnownChecks(t *testing.T) {
	for _, tc := range []struct {
		check  Check
		clause string
		sub    int
		name   string
	}{
		{Checks.Transparency.ImageWithSoftMask, "6.4", 6, "ImageWithSoftMask"},
		{Checks.Structure.FileHeaderSignature, "6.1.2", 1, "FileHeaderSignature"},
		{Checks.Structure.ObjectFraming, "6.1.8", 1, "ObjectFraming"},
		{Checks.Metadata.PDFAIdentifierMissing, "6.7.11", 1, "PDFAIdentifierMissing"},
		{Checks.Font.AdvanceWidthMismatch, "6.3.6", 1, "AdvanceWidthMismatch"},
		{Checks.Action.AdditionalActions, "6.6.2", 1, "AdditionalActions"},
		{Checks.Form.XFA, "6.9", 2, "XFA"},
		{Checks.Annotation.DisallowedSubtype, "6.5.2", 1, "DisallowedSubtype"},
	} {
		if tc.check.Clause() != tc.clause {
			t.Errorf("%s: Clause() = %q, want %q", tc.name, tc.check.Clause(), tc.clause)
		}
		if tc.check.Subclause() != tc.sub {
			t.Errorf("%s: Subclause() = %d, want %d", tc.name, tc.check.Subclause(), tc.sub)
		}
		if tc.check.Name() != tc.name {
			t.Errorf("Name() = %q, want %q", tc.check.Name(), tc.name)
		}
		if tc.check.id == 0 {
			t.Errorf("%s has zero ID (not registered)", tc.name)
		}
	}
}

func TestProfile_PDFA1BIsFullProfile(t *testing.T) {
	all := AllChecks()
	if len(PDFA_1B.Checks()) != len(all) {
		t.Errorf("PDFA_1B has %d checks, catalog has %d", len(PDFA_1B.Checks()), len(all))
	}
	for _, c := range all {
		if !PDFA_1B.Has(c) {
			t.Errorf("PDFA_1B missing check %q", c.name)
		}
	}
}

func TestProfile_NewProfileIsEmpty(t *testing.T) {
	p := NewProfile(A1_B)
	if len(p.Checks()) != 0 {
		t.Errorf("NewProfile should have 0 checks, got %d", len(p.Checks()))
	}
	if p.Has(Checks.Transparency.ImageWithSoftMask) {
		t.Error("empty profile should not contain any check")
	}
}

func TestProfile_CopyOnWrite(t *testing.T) {
	before := len(PDFA_1B.Checks())

	_ = PDFA_1B.Clear()
	if len(PDFA_1B.Checks()) != before {
		t.Errorf("Clear() modified PDFA_1B: had %d checks, now %d", before, len(PDFA_1B.Checks()))
	}

	_ = PDFA_1B.RemoveCheck(Checks.Transparency.ImageWithSoftMask)
	if !PDFA_1B.Has(Checks.Transparency.ImageWithSoftMask) {
		t.Error("RemoveCheck() modified PDFA_1B")
	}

	empty := NewProfile(A1_B)
	_ = empty.AddCheck(Checks.Structure.FileHeaderSignature)
	if empty.Has(Checks.Structure.FileHeaderSignature) {
		t.Error("AddCheck() modified the original empty profile")
	}
}

func TestProfile_Clear(t *testing.T) {
	empty := PDFA_1B.Clear()
	if len(empty.Checks()) != 0 {
		t.Errorf("Clear() left %d checks, want 0", len(empty.Checks()))
	}
	if empty.Level != PDFA_1B.Level {
		t.Errorf("Clear() changed Level: got %v, want %v", empty.Level, PDFA_1B.Level)
	}
}

func TestProfile_AddCheck(t *testing.T) {
	p := PDFA_1B.Clear().
		AddCheck(Checks.Transparency.ImageWithSoftMask, Checks.Structure.ObjectFraming)
	if len(p.Checks()) != 2 {
		t.Errorf("AddCheck: got %d checks, want 2", len(p.Checks()))
	}
	if !p.Has(Checks.Transparency.ImageWithSoftMask) {
		t.Error("added ImageWithSoftMask not found")
	}
	if !p.Has(Checks.Structure.ObjectFraming) {
		t.Error("added ObjectFraming not found")
	}
	if p.Has(Checks.Metadata.PDFAIdentifierMissing) {
		t.Error("non-added check should not be present")
	}
}

func TestProfile_RemoveCheck(t *testing.T) {
	p := PDFA_1B.RemoveCheck(Checks.Transparency.ImageWithSoftMask)
	if p.Has(Checks.Transparency.ImageWithSoftMask) {
		t.Error("removed check still present")
	}
	if !p.Has(Checks.Structure.FileHeaderSignature) {
		t.Error("unrelated check should still be present after RemoveCheck")
	}
	empty := PDFA_1B.Clear()
	_ = empty.RemoveCheck(Checks.Transparency.ImageWithSoftMask)
}

func TestProfile_ChecksOrder(t *testing.T) {
	all := AllChecks()
	got := PDFA_1B.Checks()
	if len(got) != len(all) {
		t.Fatalf("Checks() length mismatch: %d vs %d", len(got), len(all))
	}
	for i := range all {
		if got[i].id != all[i].id {
			t.Errorf("Checks()[%d].id = %d, want %d (%s)", i, got[i].id, all[i].id, all[i].name)
		}
	}
}

func TestProfile_AllowsUnknownPairs(t *testing.T) {
	empty := NewProfile(A1_B)
	if !empty.allows("99.99.99", 999) {
		t.Error("unknown pair should be allowed in an empty profile")
	}
	full := newFullProfile(A1_B)
	if !full.allows("99.99.99", 999) {
		t.Error("unknown pair should be allowed in a full profile")
	}
}

func TestProfile_AllowsCatalogPairs(t *testing.T) {
	check := Checks.Transparency.ImageWithSoftMask

	full := newFullProfile(A1_B)
	if !full.allows(check.clause, check.subclause) {
		t.Error("enabled check pair should be allowed")
	}

	p := full.RemoveCheck(check)
	if p.allows(check.clause, check.subclause) {
		t.Error("removed check pair should not be allowed")
	}

	p2 := p.AddCheck(check)
	if !p2.allows(check.clause, check.subclause) {
		t.Error("re-added check pair should be allowed")
	}
}

const header61dir = isartorDir + "/6.1 File structure/6.1.2 File header"

// header62checks is the group of all four 6.1.2 checks used in behavioural tests.
func header61checks() []Check {
	s := Checks.Structure
	return []Check{
		s.FileHeaderSignature,
		s.FileHeaderComment,
		s.FileHeaderCommentLength,
		s.FileHeaderCommentBytes,
	}
}

func findFirstIsartor612File(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat(isartorDir); os.IsNotExist(err) {
		t.Skip("Isartor test suite not present")
	}
	// Walk the 6.1.2 subdirectory for any fail file.
	var found string
	_ = filepath.WalkDir(header61dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || found != "" {
			return err
		}
		if filepath.Ext(path) == ".pdf" {
			found = path
		}
		return nil
	})
	if found == "" {
		t.Skip("no 6.1.2 Isartor test file found")
	}
	return found
}

func TestVerifyProfile_FullProfileMatchesVerify(t *testing.T) {
	if _, err := os.Stat(isartorDir); os.IsNotExist(err) {
		t.Skip("Isartor test suite not present")
	}
	path := findFirstIsartor612File(t)
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	resVerify, err := doc.Verify(A1_B)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	resProfile, err := doc.VerifyProfile(PDFA_1B)
	if err != nil {
		t.Fatalf("VerifyProfile(PDFA_1B): %v", err)
	}

	if resVerify.Valid != resProfile.Valid {
		t.Errorf("Valid mismatch: Verify=%v VerifyProfile=%v", resVerify.Valid, resProfile.Valid)
	}
	if len(resVerify.Issues) != len(resProfile.Issues) {
		t.Errorf("Issues count mismatch: Verify=%d VerifyProfile=%d",
			len(resVerify.Issues), len(resProfile.Issues))
	}
}

func TestVerifyProfile_RemoveCheckSuppressesViolation(t *testing.T) {
	path := findFirstIsartor612File(t)
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	// Baseline: full profile must catch a 6.1.2 violation.
	full, _ := doc.VerifyProfile(PDFA_1B)
	if full.Valid {
		t.Fatal("full profile: expected non-conformant file but got Valid=true")
	}
	caught := false
	for _, iss := range full.Issues {
		if clauseMatches(iss.clause, "6.1.2") {
			caught = true
			break
		}
	}
	if !caught {
		t.Fatal("full profile: expected 6.1.2 violation not found in issues")
	}

	// Remove all 6.1.2 checks: none should appear.
	p := PDFA_1B.RemoveCheck(header61checks()...)
	res, _ := doc.VerifyProfile(p)
	for _, iss := range res.Issues {
		if clauseMatches(iss.clause, "6.1.2") {
			t.Errorf("disabled 6.1.2 check still reported: %s", iss)
		}
	}
}

func TestVerifyProfile_ClearThenAddOnlyFiresEnabledCheck(t *testing.T) {
	path := findFirstIsartor612File(t)
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	// Empty profile + all 6.1.2 checks: only 6.1.2 violations may appear.
	p := PDFA_1B.Clear().AddCheck(header61checks()...)
	res, _ := doc.VerifyProfile(p)
	for _, iss := range res.Issues {
		if !clauseMatches(iss.clause, "6.1.2") {
			t.Errorf("unexpected clause %q reported with only 6.1.2 checks enabled", iss.clause)
		}
	}
	caught := false
	for _, iss := range res.Issues {
		if clauseMatches(iss.clause, "6.1.2") {
			caught = true
			break
		}
	}
	if !caught {
		t.Error("clear+add 6.1.2 checks: expected 6.1.2 violation not found")
	}
}

func TestVerifyProfile_EmptyProfileReturnsValid(t *testing.T) {
	path := findFirstIsartor612File(t)
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	// An empty profile enables no checks, so no violations can be reported.
	p := NewProfile(A1_B)
	res, err := doc.VerifyProfile(p)
	if err != nil {
		t.Fatalf("VerifyProfile: %v", err)
	}
	if !res.Valid {
		t.Errorf("empty profile: expected Valid=true, got issues: %v", res.Issues)
	}
	if len(res.Issues) != 0 {
		t.Errorf("empty profile: expected 0 issues, got %d", len(res.Issues))
	}
}

func TestVerifyProfile_NilProfileReturnsError(t *testing.T) {
	path := findFirstIsartor612File(t)
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	_, err = doc.VerifyProfile(nil)
	if err == nil {
		t.Error("VerifyProfile(nil) should return an error")
	}
}

func TestVerifyProfile_UndefinedLevelReturnsError(t *testing.T) {
	path := findFirstIsartor612File(t)
	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	_, err = doc.VerifyProfile(NewProfile(Undefined))
	if err == nil {
		t.Error("VerifyProfile(Undefined level) should return an error")
	}
}
