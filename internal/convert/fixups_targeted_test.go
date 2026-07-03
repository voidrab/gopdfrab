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
