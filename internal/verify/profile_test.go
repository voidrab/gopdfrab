package verify

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func TestProfile_Legacy1BIsFullProfile(t *testing.T) {
	all := pdf.AllChecks()
	if len(pdf.Legacy1B.Checks()) != len(all) {
		t.Errorf("Legacy1B has %d checks, catalog has %d", len(pdf.Legacy1B.Checks()), len(all))
	}
	for _, c := range all {
		if !pdf.Legacy1B.Has(c) {
			t.Errorf("Legacy1B missing check %q", c.Name())
		}
	}
}

func TestProfile_NewProfileIsEmpty(t *testing.T) {
	p := pdf.NewProfile(pdf.A1B)
	if len(p.Checks()) != 0 {
		t.Errorf("NewProfile should have 0 checks, got %d", len(p.Checks()))
	}
	if p.Has(pdf.Checks.Transparency.ImageWithSoftMask) {
		t.Error("empty profile should not contain any check")
	}
}

func TestProfile_CopyOnWrite(t *testing.T) {
	before := len(pdf.PDFA1B.Checks())

	_ = pdf.PDFA1B.Clear()
	if len(pdf.PDFA1B.Checks()) != before {
		t.Errorf("Clear() modified PDFA1B: had %d checks, now %d", before, len(pdf.PDFA1B.Checks()))
	}

	_ = pdf.PDFA1B.RemoveCheck(pdf.Checks.Transparency.ImageWithSoftMask)
	if !pdf.PDFA1B.Has(pdf.Checks.Transparency.ImageWithSoftMask) {
		t.Error("RemoveCheck() modified PDFA1B")
	}

	empty := pdf.NewProfile(pdf.A1B)
	_ = empty.AddCheck(pdf.Checks.Structure.FileHeaderSignature)
	if empty.Has(pdf.Checks.Structure.FileHeaderSignature) {
		t.Error("AddCheck() modified the original empty profile")
	}
}

func TestProfile_Clear(t *testing.T) {
	empty := pdf.PDFA1B.Clear()
	if len(empty.Checks()) != 0 {
		t.Errorf("Clear() left %d checks, want 0", len(empty.Checks()))
	}
	if empty.Level != pdf.PDFA1B.Level {
		t.Errorf("Clear() changed Level: got %v, want %v", empty.Level, pdf.PDFA1B.Level)
	}
}

func TestProfile_AddCheck(t *testing.T) {
	p := pdf.PDFA1B.Clear().
		AddCheck(pdf.Checks.Transparency.ImageWithSoftMask, pdf.Checks.Structure.ObjectFraming)
	if len(p.Checks()) != 2 {
		t.Errorf("AddCheck: got %d checks, want 2", len(p.Checks()))
	}
	if !p.Has(pdf.Checks.Transparency.ImageWithSoftMask) {
		t.Error("added ImageWithSoftMask not found")
	}
	if !p.Has(pdf.Checks.Structure.ObjectFraming) {
		t.Error("added ObjectFraming not found")
	}
	if p.Has(pdf.Checks.Metadata.PDFAIdentifierMissing) {
		t.Error("non-added check should not be present")
	}
}

func TestProfile_RemoveCheck(t *testing.T) {
	p := pdf.PDFA1B.RemoveCheck(pdf.Checks.Transparency.ImageWithSoftMask)
	if p.Has(pdf.Checks.Transparency.ImageWithSoftMask) {
		t.Error("removed check still present")
	}
	if !p.Has(pdf.Checks.Structure.FileHeaderSignature) {
		t.Error("unrelated check should still be present after RemoveCheck")
	}
	empty := pdf.PDFA1B.Clear()
	_ = empty.RemoveCheck(pdf.Checks.Transparency.ImageWithSoftMask)
}

func TestProfile_ChecksOrder(t *testing.T) {
	all := pdf.AllChecks()
	got := pdf.Legacy1B.Checks()
	if len(got) != len(all) {
		t.Fatalf("Checks() length mismatch: %d vs %d", len(got), len(all))
	}
	for i := range all {
		if got[i].ID() != all[i].ID() {
			t.Errorf("Checks()[%d].id = %d, want %d (%s)", i, got[i].ID(), all[i].ID(), all[i].Name())
		}
	}
}

func TestProfile_AllowsUnknownPairs(t *testing.T) {
	empty := pdf.NewProfile(pdf.A1B)
	if !empty.Allows("99.99.99", 999) {
		t.Error("unknown pair should be allowed in an empty profile")
	}
	full := pdf.NewFullProfile(pdf.A1B)
	if !full.Allows("99.99.99", 999) {
		t.Error("unknown pair should be allowed in a full profile")
	}
}

func TestProfile_AllowsCatalogPairs(t *testing.T) {
	chk := pdf.Checks.Transparency.ImageWithSoftMask

	full := pdf.NewFullProfile(pdf.A1B)
	if !full.Allows(chk.Clause(), chk.Subclause()) {
		t.Error("enabled check pair should be allowed")
	}

	p := full.RemoveCheck(chk)
	if p.Allows(chk.Clause(), chk.Subclause()) {
		t.Error("removed check pair should not be allowed")
	}

	p2 := p.AddCheck(chk)
	if !p2.Allows(chk.Clause(), chk.Subclause()) {
		t.Error("re-added check pair should be allowed")
	}
}

const header61dir = isartorDir + "/6.1 File structure/6.1.2 File header"

// header61checks returns the four 6.1.2 file-header checks.
func header61checks() []pdf.Check {
	s := pdf.Checks.Structure
	return []pdf.Check{
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
	doc, err := pdf.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	resVerify, err := Verify(doc, pdf.PDFA1B)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	resProfile, err := Verify(doc, pdf.PDFA1B)
	if err != nil {
		t.Fatalf("Verify(PDFA1B): %v", err)
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
	doc, err := pdf.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	// Baseline: full profile must catch a 6.1.2 violation.
	full, _ := Verify(doc, pdf.PDFA1B)
	if full.Valid {
		t.Fatal("full profile: expected non-conformant file but got Valid=true")
	}
	caught := false
	for _, iss := range full.Issues {
		if clauseMatches(iss.Check().Clause(), "6.1.2") {
			caught = true
			break
		}
	}
	if !caught {
		t.Fatal("full profile: expected 6.1.2 violation not found in issues")
	}

	// Remove all 6.1.2 checks: none should appear.
	p := pdf.PDFA1B.RemoveCheck(header61checks()...)
	res, _ := Verify(doc, p)
	for _, iss := range res.Issues {
		if clauseMatches(iss.Check().Clause(), "6.1.2") {
			t.Errorf("disabled 6.1.2 check still reported: %s", iss)
		}
	}
}

func TestVerifyProfile_ClearThenAddOnlyFiresEnabledCheck(t *testing.T) {
	path := findFirstIsartor612File(t)
	doc, err := pdf.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	// Empty profile + all 6.1.2 checks: only 6.1.2 violations may appear.
	p := pdf.PDFA1B.Clear().AddCheck(header61checks()...)
	res, _ := Verify(doc, p)
	for _, iss := range res.Issues {
		if !clauseMatches(iss.Check().Clause(), "6.1.2") {
			t.Errorf("unexpected clause %q reported with only 6.1.2 checks enabled", iss.Check().Clause())
		}
	}
	caught := false
	for _, iss := range res.Issues {
		if clauseMatches(iss.Check().Clause(), "6.1.2") {
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
	doc, err := pdf.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	// An empty profile enables no checks, so no violations can be reported.
	p := pdf.NewProfile(pdf.A1B)
	res, err := Verify(doc, p)
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
	doc, err := pdf.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	_, err = Verify(doc, nil)
	if err == nil {
		t.Error("Verify(nil) should return an error")
	}
}

func TestVerifyProfile_UndefinedLevelReturnsError(t *testing.T) {
	path := findFirstIsartor612File(t)
	doc, err := pdf.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()

	_, err = Verify(doc, pdf.NewProfile(pdf.Undefined))
	if err == nil {
		t.Error("Verify(Undefined level) should return an error")
	}
}
