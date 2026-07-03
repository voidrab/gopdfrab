package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"

	"github.com/voidrab/gopdfrab/internal/verify"
)

// targetedFixture opens path, numbers its graph, and verifies it in-heap the
// way the convert loop does, returning the pass and the issues for check c.
func targetedFixture(t *testing.T, path string, c pdf.Check) (*fixPass, []pdf.PDFError, func()) {
	t.Helper()
	trailerHolder := new(pdf.PDFDict)
	trailer, closeDoc := fixtureTrailer(t, path)
	*trailerHolder = trailer

	doc, err := pdf.Open(path)
	if err != nil {
		closeDoc()
		t.Fatalf("pdf.Open(%s): %v", path, err)
	}
	objs := writer.NumberObjects(*trailerHolder)
	doc.SeedResolvedGraph(*trailerHolder, objs)
	res, err := verify.Verify(doc, pdf.PDFA_1B)
	if err != nil {
		doc.Close()
		closeDoc()
		t.Fatalf("Verify: %v", err)
	}
	issues := res.IssuesForCheck(c)
	if len(issues) == 0 {
		doc.Close()
		closeDoc()
		t.Fatalf("fixture reports no %s issues", c.Name())
	}
	pass := &fixPass{trailer: trailerHolder, objs: objs}
	return pass, issues, func() { doc.Close(); closeDoc() }
}

// runTargetedAndCheckIdempotent asserts fixTargeted handles the batch, changes
// the graph on the first call, and is a no-op on the second.
func runTargetedAndCheckIdempotent(t *testing.T, tf targetedFixer, pass *fixPass, issues []pdf.PDFError) {
	t.Helper()
	changed, handled, err := tf.fixTargeted(pass, issues)
	if err != nil {
		t.Fatalf("fixTargeted: %v", err)
	}
	if !handled {
		t.Fatalf("handled = false, want targeted handling (all issues carry refs)")
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	changed, handled, err = tf.fixTargeted(pass, issues)
	if err != nil {
		t.Fatalf("fixTargeted (second pass): %v", err)
	}
	if !handled {
		t.Fatalf("handled = false on second pass, want true")
	}
	if changed {
		t.Errorf("changed = true on second pass, want false (targeted fix must be idempotent)")
	}
}

func TestFontMetricFixerTargetsIssueRefs(t *testing.T) {
	path := "../../tests/Isartor/PDFA-1b/6.3 Fonts/6.3.6 Font metrics/isartor-6-3-6-t01-fail-b.pdf"
	pass, issues, done := targetedFixture(t, path, pdf.Checks.Font.AdvanceWidthMismatch)
	defer done()

	runTargetedAndCheckIdempotent(t, fontMetricFixer{}, pass, issues)
	assertCheckClearedByWrite(t, *pass.trailer, pdf.Checks.Font.AdvanceWidthMismatch)
}

func TestFontSubsetMetaFixerTargetsIssueRefs(t *testing.T) {
	path := "../../tests/Isartor/PDFA-1b/6.3 Fonts/6.3.5 Font subsets/isartor-6-3-5-t02-fail-a.pdf"
	pass, issues, done := targetedFixture(t, path, pdf.Checks.Font.Type1SubsetCharSet)
	defer done()

	runTargetedAndCheckIdempotent(t, fontSubsetMetaFixer{}, pass, issues)
	assertCheckClearedByWrite(t, *pass.trailer, pdf.Checks.Font.Type1SubsetCharSet)
}

// appearanceTargetWidget builds a minimal widget annotation with no /AP.
func appearanceTargetWidget() pdf.PDFDict {
	w := pdf.NewPDFDict()
	w.Entries["Type"] = pdf.PDFName{Value: "Annot"}
	w.Entries["Subtype"] = pdf.PDFName{Value: "Widget"}
	w.Entries["Rect"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(100), pdf.PDFInteger(20)}
	w.Entries["_ref"] = pdf.PDFRef{ObjNum: 90}
	return w
}

// TestAppearanceFixerTargetsOnlyFlaggedAnnots documents the targeted
// contract: the verifier reports every violating annotation per pass, so
// fixTargeted may leave an unflagged (but equally violating) one untouched.
func TestAppearanceFixerTargetsOnlyFlaggedAnnots(t *testing.T) {
	flagged, other := appearanceTargetWidget(), appearanceTargetWidget()
	root := pdf.NewPDFDict()
	root.Entries["_ref"] = pdf.PDFRef{ObjNum: 91}
	root.Entries["Annots"] = pdf.PDFArray{flagged, other}
	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = root

	objs := writer.NumberObjects(trailer)
	pass := &fixPass{trailer: &trailer, objs: objs}
	ref := flagged.Entries["_ref"].(pdf.PDFRef)
	issue := pdf.NewError(pdf.Checks.Annotation.MissingAppearance, nil, 1, &ref)

	changed, handled, err := appearanceFixer{}.fixTargeted(pass, []pdf.PDFError{issue})
	if err != nil {
		t.Fatalf("fixTargeted: %v", err)
	}
	if !handled || !changed {
		t.Fatalf("handled=%v changed=%v, want true/true", handled, changed)
	}
	if _, ok := flagged.Entries["AP"].(pdf.PDFDict); !ok {
		t.Error("flagged widget got no /AP")
	}
	if _, ok := other.Entries["AP"]; ok {
		t.Error("unflagged widget was touched by the targeted fix")
	}

	// A ref-less issue in the batch must force the full-walk fallback, which
	// then fixes the remaining widget too.
	noRef := pdf.NewError(pdf.Checks.Annotation.MissingAppearance, nil, 1, nil)
	_, handled, err = appearanceFixer{}.fixTargeted(pass, []pdf.PDFError{noRef})
	if err != nil {
		t.Fatalf("fixTargeted: %v", err)
	}
	if handled {
		t.Fatal("handled = true with a ref-less issue, want fallback")
	}
	if _, err := (appearanceFixer{}).Fix(&trailer, nil); err != nil {
		t.Fatalf("Fix fallback: %v", err)
	}
	if _, ok := other.Entries["AP"].(pdf.PDFDict); !ok {
		t.Error("full-walk fallback did not fix the remaining widget")
	}
}

func TestFontMetricFixerTargetedFallsBackWithoutRefs(t *testing.T) {
	path := "../../tests/Isartor/PDFA-1b/6.3 Fonts/6.3.6 Font metrics/isartor-6-3-6-t01-fail-b.pdf"
	pass, issues, done := targetedFixture(t, path, pdf.Checks.Font.AdvanceWidthMismatch)
	defer done()

	noRef := pdf.NewError(pdf.Checks.Font.AdvanceWidthMismatch, nil, 0, nil)
	_, handled, err := fontMetricFixer{}.fixTargeted(pass, append(issues, noRef))
	if err != nil {
		t.Fatalf("fixTargeted: %v", err)
	}
	if handled {
		t.Fatal("handled = true with a ref-less issue in the batch, want full-walk fallback")
	}
}
