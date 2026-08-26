package verify

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
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

// buildPSXObjectDoc builds a one-page document carrying a PostScript XObject
// in the page's XObject resources; drawn controls whether the content stream
// invokes it.
func buildPSXObjectDoc(drawn bool) []byte {
	content := "q\nQ\n"
	if drawn {
		content = "q\n/X0 Do\nQ\n"
	}
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << /XObject << /X0 4 0 R >> >> /Contents 5 0 R >>")
	b.StreamObj(4, "<< /Type /XObject /Subtype /PS /FormType 1 /BBox [0 0 10 10]", []byte("%!PS"))
	b.StreamObj(5, "<<", []byte(content))
	return b.FinishClassic("<< /Size 6 /Root 1 0 R >>")
}

// TestPostScriptXObjectReachabilityGate pins the veraPDF-differential finding
// on isartor-6-2-7-t01-fail-a: a PostScript XObject invoked via Do must be
// flagged under PDFA1B, while an unreferenced one is out of scope (the
// veraPDF corpus passes 6-2-5-t03-pass-a, which carries one unreferenced).
// Legacy1B flags both.
func TestPostScriptXObjectReachabilityGate(t *testing.T) {
	hasPS := func(data []byte, p *pdf.Profile) bool {
		res, err := VerifyBytes(data, p, nil)
		if err != nil {
			t.Fatalf("VerifyBytes: %v", err)
		}
		for _, e := range res.Issues {
			if e.Check() == pdf.Checks.Image.PostScriptXObject {
				return true
			}
		}
		return false
	}
	if !hasPS(buildPSXObjectDoc(true), pdf.PDFA1B) {
		t.Error("drawn PostScript XObject not flagged under PDFA1B")
	}
	if hasPS(buildPSXObjectDoc(false), pdf.PDFA1B) {
		t.Error("unreferenced PostScript XObject flagged under PDFA1B")
	}
	if !hasPS(buildPSXObjectDoc(false), pdf.Legacy1B) {
		t.Error("unreferenced PostScript XObject not flagged under Legacy1B")
	}
}

// buildPatternFontDoc builds a one-page document whose only font use is
// inside a tiling pattern's content stream; used controls whether the page
// content sets the pattern at all.
func buildPatternFontDoc(used bool) []byte {
	content := "q\nQ\n"
	if used {
		content = "/Pattern cs /P0 scn\n0 0 50 50 re f\n"
	}
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << /Pattern << /P0 5 0 R >> >> /Contents 4 0 R >>")
	b.StreamObj(4, "<<", []byte(content))
	b.StreamObj(5, "<< /PatternType 1 /PaintType 1 /TilingType 1 /BBox [0 0 10 10] /XStep 10 /YStep 10 /Resources << /Font << /F0 6 0 R >> >>",
		[]byte("BT\n/F0 12 Tf\n(A) Tj\nET\n"))
	b.Obj(6, "<< /Type /Font /Subtype /TrueType /BaseFont /ArialMT /FontDescriptor 7 0 R >>")
	b.Obj(7, "<< /Type /FontDescriptor /FontName /ArialMT /Flags 32 >>")
	return b.FinishClassic("<< /Size 8 /Root 1 0 R >>")
}

// TestPatternFontUsageCollected pins the veraPDF-differential finding on
// isartor-6-3-4-t01-fail-h: a non-embedded font shown only inside a tiling
// pattern is used, so SkipUnusedSimpleFonts must not suppress 6.3.4. A
// pattern the content never sets keeps the suppression.
func TestPatternFontUsageCollected(t *testing.T) {
	hasNotEmbedded := func(data []byte) bool {
		res, err := VerifyBytes(data, pdf.PDFA1B, nil)
		if err != nil {
			t.Fatalf("VerifyBytes: %v", err)
		}
		for _, e := range res.Issues {
			if e.Check() == pdf.Checks.Font.SimpleNotEmbedded {
				return true
			}
		}
		return false
	}
	if !hasNotEmbedded(buildPatternFontDoc(true)) {
		t.Error("font used only inside a set tiling pattern not flagged as unembedded")
	}
	if hasNotEmbedded(buildPatternFontDoc(false)) {
		t.Error("font inside a never-set pattern flagged despite SkipUnusedSimpleFonts")
	}
}
