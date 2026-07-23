package convert

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// onePageTrailer builds a minimal in-heap one-page document graph.
func onePageTrailer() pdf.PDFDict {
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type":     pdf.PDFName{Value: "Page"},
		"Contents": pdf.PDFDict{Entries: map[string]pdf.PDFValue{}, HasStream: true, RawStream: []byte("1 0 0 rg 0 0 10 10 re f")},
		"MediaBox": pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(10), pdf.PDFInteger(10)},
	}}
	pages := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"},
		"Kids": pdf.PDFArray{page},
	}}
	root := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type":  pdf.PDFName{Value: "Catalog"},
		"Pages": pages,
	}}
	return pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Root": root}}
}

// openTrailer serializes trailer and reopens it as a Reader.
func openTrailer(t *testing.T, trailer pdf.PDFDict) *pdf.Reader {
	t.Helper()
	var buf bytes.Buffer
	if _, err := writer.WriteDocumentIndexed(&buf, trailer); err != nil {
		t.Fatalf("WriteDocumentIndexed: %v", err)
	}
	doc, err := pdf.OpenBytes(buf.Bytes())
	if err != nil {
		t.Fatalf("OpenBytes: %v", err)
	}
	t.Cleanup(func() { doc.Close() })
	return doc
}

// TestRasterBackstopFlattensAllPages drives the document-wide flatten branch:
// a residual fixable issue with no page attribution can't be targeted by the
// page-by-page raster pass, so the backstop flattens every page.
func TestRasterBackstopFlattensAllPages(t *testing.T) {
	trailer := onePageTrailer()
	doc := openTrailer(t, trailer)

	c := pdf.Checks.Colour.OutputIntentNotArray
	cr := &ConvertResult{Result: pdf.Result{
		Valid:  false,
		Issues: []pdf.PDFError{pdf.NewError(c, []error{errors.New("synthetic residual")}, 0, nil)},
	}}
	fixers := map[pdf.Check]Fixer{c: nil}
	var lastParts verify.Parts
	graphClean := false

	if err := rasterBackstop(doc, &trailer, cr, pdf.PDFA1B, fixers, &lastParts, &graphClean, defaultRasterDPI); err != nil {
		t.Fatalf("rasterBackstop: %v", err)
	}
	if cr.Iterations != 1 {
		t.Errorf("Iterations = %d, want 1 (one flatten-all verify)", cr.Iterations)
	}
	if !graphClean {
		t.Error("graphClean = false after the backstop's verify")
	}
	page := trailer.Entries["Root"].(pdf.PDFDict).Entries["Pages"].(pdf.PDFDict).Entries["Kids"].(pdf.PDFArray)[0].(pdf.PDFDict)
	res, ok := page.Entries["Resources"].(pdf.PDFDict)
	if !ok {
		t.Fatal("page was not flattened: no Resources dict")
	}
	if xo, ok := res.Entries["XObject"].(pdf.PDFDict); !ok || xo.Entries["Im0"] == nil {
		t.Error("page was not flattened: Resources/XObject/Im0 missing")
	}
}

// TestRasterBackstopVerifyErrors covers both raster blocks' verify-error
// returns, using an undefined-level profile that fails the in-heap verify:
// once via a page-attributed issue (page-by-page pass) and once via a
// document-wide one (flatten-all pass).
func TestRasterBackstopVerifyErrors(t *testing.T) {
	c := pdf.Checks.Colour.OutputIntentNotArray
	fixers := map[pdf.Check]Fixer{c: nil}
	for name, page := range map[string]int{"pageTargeted": 1, "docWide": 0} {
		t.Run(name, func(t *testing.T) {
			trailer := onePageTrailer()
			doc := openTrailer(t, trailer)
			cr := &ConvertResult{Result: pdf.Result{
				Valid:  false,
				Issues: []pdf.PDFError{pdf.NewError(c, []error{errors.New("synthetic")}, page, nil)},
			}}
			var lastParts verify.Parts
			graphClean := true
			err := rasterBackstop(doc, &trailer, cr, &pdf.Profile{Level: pdf.Undefined}, fixers, &lastParts, &graphClean, defaultRasterDPI)
			if err == nil {
				t.Fatal("rasterBackstop with an undefined-level profile did not propagate the verify error")
			}
			if graphClean {
				t.Error("graphClean = true after a failed post-flatten verify")
			}
		})
	}
}

// TestRasterBackstopSkipsUnfixableIssues pins the no-op guard.
func TestRasterBackstopSkipsUnfixableIssues(t *testing.T) {
	trailer := onePageTrailer()
	cr := &ConvertResult{Result: pdf.Result{
		Valid:  false,
		Issues: []pdf.PDFError{pdf.NewError(pdf.Checks.Colour.OutputIntentNotArray, []error{errors.New("x")}, 0, nil)},
	}}
	var lastParts verify.Parts
	graphClean := true
	// No fixer registered for the issue's check: nothing to do.
	if err := rasterBackstop(nil, &trailer, cr, pdf.PDFA1B, map[pdf.Check]Fixer{}, &lastParts, &graphClean, defaultRasterDPI); err != nil {
		t.Fatalf("rasterBackstop: %v", err)
	}
	if cr.Iterations != 0 || !graphClean {
		t.Errorf("backstop acted on an unfixable issue: iterations=%d graphClean=%v", cr.Iterations, graphClean)
	}
}

// TestSerializeAndVerifyRejectsBadProfile covers both final-verify paths'
// error returns (merged/clean and full/dirty) via the nil-profile guard.
func TestSerializeAndVerifyRejectsBadProfile(t *testing.T) {
	for _, clean := range []bool{true, false} {
		cr := &ConvertResult{}
		err := serializeAndVerify(nil, onePageTrailer(), cr, nil, verify.Parts{}, clean)
		if err == nil {
			t.Errorf("serializeAndVerify(nil profile, graphClean=%v) did not error", clean)
		}
	}
}

// TestRunRejectsUndefinedProfile covers the in-heap verify error path in Run.
func TestRunRejectsUndefinedProfile(t *testing.T) {
	doc := openTrailer(t, onePageTrailer())
	_, err := Run(doc, &pdf.Profile{Level: pdf.Undefined})
	if err == nil || !strings.Contains(err.Error(), "convert:") {
		t.Errorf("Run(Undefined profile) err = %v, want a wrapped convert error", err)
	}
}

// TestRunWrapsSerializeError covers Run's final-serialize error path: a
// graph value the writer cannot serialize survives verification and the fix
// loop untouched, then fails WriteDocumentIndexed.
func TestRunWrapsSerializeError(t *testing.T) {
	doc := openTrailer(t, onePageTrailer())
	g, err := doc.ResolveGraph()
	if err != nil {
		t.Fatalf("ResolveGraph: %v", err)
	}
	trailer := g.(pdf.PDFDict)
	root := trailer.Entries["Root"].(pdf.PDFDict)
	root.Entries["Bogus"] = struct{ X int }{1}

	_, err = Run(doc, pdf.PDFA1B)
	if err == nil || !strings.Contains(err.Error(), "unsupported value type") {
		t.Errorf("Run over an unserializable graph err = %v, want an unsupported-value-type error", err)
	}
}

// TestApplyPreemptiveFixupsAfterFixupError covers the after-walk phase's
// error propagation.
func TestApplyPreemptiveFixupsAfterFixupError(t *testing.T) {
	old := preemptiveAfterFixups
	t.Cleanup(func() { preemptiveAfterFixups = old })
	preemptiveAfterFixups = append(slices.Clone(old), func(*pdf.PDFDict, *pdf.Reader) error {
		return errors.New("after fixup failed")
	})

	doc := openTrailer(t, onePageTrailer())
	g, err := doc.ResolveGraph()
	if err != nil {
		t.Fatalf("ResolveGraph: %v", err)
	}
	trailer := g.(pdf.PDFDict)
	if err := applyPreemptiveFixups(&trailer, doc); err == nil || !strings.Contains(err.Error(), "after fixup failed") {
		t.Errorf("applyPreemptiveFixups err = %v, want the after-fixup failure", err)
	}
}

// TestPromoteEmptyGlyphsInFontGuards drives the visitor's guard cascade with
// synthetic dicts: wrong subtype, missing descriptor, missing/streamless
// FontFile2, and an undecodable program all leave the dict untouched.
func TestPromoteEmptyGlyphsInFontGuards(t *testing.T) {
	notCID := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "TrueType"}}}
	promoteEmptyGlyphsInFont(notCID)

	noDesc := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "CIDFontType2"}}}
	promoteEmptyGlyphsInFont(noDesc)

	noFF := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Subtype":        pdf.PDFName{Value: "CIDFontType2"},
		"FontDescriptor": pdf.PDFDict{Entries: map[string]pdf.PDFValue{}},
	}}
	promoteEmptyGlyphsInFont(noFF)

	streamless := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Subtype": pdf.PDFName{Value: "CIDFontType2"},
		"FontDescriptor": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"FontFile2": pdf.PDFDict{Entries: map[string]pdf.PDFValue{}},
		}},
	}}
	promoteEmptyGlyphsInFont(streamless)

	badFF := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Filter": pdf.PDFName{Value: "NoSuchFilter"},
	}, HasStream: true, RawStream: []byte("junk")}
	undecodable := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Subtype": pdf.PDFName{Value: "CIDFontType2"},
		"FontDescriptor": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"FontFile2": badFF,
		}},
	}}
	promoteEmptyGlyphsInFont(undecodable)
	if string(badFF.RawStream) != "junk" {
		t.Error("undecodable FontFile2 was rewritten")
	}
}

// TestPagesTreeArrayFixerRebalances covers the Fix wrapper and the lazily
// computed replacement object numbers: an oversized Kids array is split into
// a tree of intermediate Pages nodes.
func TestPagesTreeArrayFixerRebalances(t *testing.T) {
	kids := make(pdf.PDFArray, maxPDFArrayElements+1)
	for i := range kids {
		kids[i] = pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Page"}}}
	}
	pages := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"},
		"Kids": kids,
	}}
	root := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type":  pdf.PDFName{Value: "Catalog"},
		"Pages": pages,
	}}
	trailer := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Root": root}}

	changed, err := pagesTreeArrayFixer{}.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatal("Fix reported no change for an oversized Kids array")
	}
	got, ok := pages.Entries["Kids"].(pdf.PDFArray)
	if !ok || len(got) > maxPDFArrayElements {
		t.Errorf("Kids not rebalanced: len = %d, want <= %d", len(got), maxPDFArrayElements)
	}

	// A conforming tree is left alone.
	changed, err = pagesTreeArrayFixer{}.Fix(&trailer, nil)
	if err != nil || changed {
		t.Errorf("second Fix = changed %v, err %v; want no-op", changed, err)
	}
}

// TestPagesTreeArrayFixerDropsOversizedStructure covers Fix's structure-drop
// branch: a struct tree holding an unsplittable oversized array is removed.
func TestPagesTreeArrayFixerDropsOversizedStructure(t *testing.T) {
	parents := make(pdf.PDFArray, maxPDFArrayElements+1)
	for i := range parents {
		parents[i] = pdf.PDFInteger(i)
	}
	st := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "StructTreeRoot"},
		"K":    parents,
	}}
	root := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type":           pdf.PDFName{Value: "Catalog"},
		"StructTreeRoot": st,
	}}
	trailer := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Root": root}}

	changed, err := pagesTreeArrayFixer{}.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatal("Fix reported no change for an oversized struct tree")
	}
	if _, still := root.Entries["StructTreeRoot"]; still {
		t.Error("StructTreeRoot was not dropped")
	}
}

// TestPlaceholderImageDegenerate covers the zero-dimension early return and
// the doubling-copy fill.
func TestPlaceholderImageDegenerate(t *testing.T) {
	if img := placeholderImage(0, 3); img.Bounds().Dx() != 0 {
		t.Errorf("placeholderImage(0,3) width = %d, want 0", img.Bounds().Dx())
	}
	img := placeholderImage(3, 2)
	r, g, b, _ := img.At(2, 1).RGBA()
	if r != g || g != b || r == 0 {
		t.Errorf("placeholderImage pixel = %d,%d,%d, want uniform gray", r, g, b)
	}
}
