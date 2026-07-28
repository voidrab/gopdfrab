package convert

import (
	"bytes"
	"context"
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
	if _, err := writer.WriteDocumentIndexed(&buf, trailer, 0); err != nil {
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

	if err := rasterBackstop(context.Background(), doc, &trailer, cr, pdf.PDFA1B, fixers, &lastParts, &graphClean, defaultRasterDPI, 0); err != nil {
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
			err := rasterBackstop(context.Background(), doc, &trailer, cr, &pdf.Profile{Level: pdf.Undefined}, fixers, &lastParts, &graphClean, defaultRasterDPI, 0)
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
	if err := rasterBackstop(context.Background(), nil, &trailer, cr, pdf.PDFA1B, map[pdf.Check]Fixer{}, &lastParts, &graphClean, defaultRasterDPI, 0); err != nil {
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
		err := serializeAndVerify(nil, onePageTrailer(), cr, nil, verify.Parts{}, clean, 0)
		if err == nil {
			t.Errorf("serializeAndVerify(nil profile, graphClean=%v) did not error", clean)
		}
	}
}

// TestRunRejectsUndefinedProfile covers the in-heap verify error path in Run.
func TestRunRejectsUndefinedProfile(t *testing.T) {
	doc := openTrailer(t, onePageTrailer())
	_, err := Run(doc, &pdf.Profile{Level: pdf.Undefined}, Options{})
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

	_, err = Run(doc, pdf.PDFA1B, Options{})
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

// TestFlattenPagesParallelDedupesAndReports pins three properties of the
// page-level raster backstop that only show up with a mixed batch: one page
// object reachable at two positions in the tree is rasterized once, a page the
// rasterizer cannot draw is excluded from RasterizedPages instead of being
// reported as flattened, and dropped features are attributed to the right page
// number.
func TestFlattenPagesParallelDedupesAndReports(t *testing.T) {
	renderable := func(content string) pdf.PDFDict {
		return pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Type": pdf.PDFName{Value: "Page"},
			"Contents": pdf.PDFDict{
				Entries: map[string]pdf.PDFValue{}, HasStream: true, RawStream: []byte(content),
			},
		}}
	}

	shared := renderable("q Q")
	// An sh operator with no shading resource is unusable content: the page
	// still rasterizes, but the feature is reported dropped.
	dropping := renderable("q /Missing sh Q")
	// A zero-area media box cannot be rendered at all.
	unrenderable := renderable("q Q")

	box := [4]float64{0, 0, 20, 20}
	pages := []pageTarget{
		{dict: shared, mediaBox: box},
		{dict: dropping, mediaBox: box},
		{dict: shared, mediaBox: box}, // the same object again
		{dict: unrenderable, mediaBox: [4]float64{0, 0, 0, 0}},
	}
	drops, rasterized, changed := flattenPagesParallel(pages, []int{1, 2, 3, 4}, 72)

	if !changed {
		t.Fatal("changed = false, want true")
	}
	if !slices.Equal(rasterized, []int{1, 2}) {
		t.Errorf("rasterized = %v, want [1 2]: page 3 is page 1's object and page 4 does not render", rasterized)
	}
	if len(drops) != 1 || drops[0].Page != 2 || len(drops[0].Features) == 0 {
		t.Errorf("drops = %+v, want one entry for page 2", drops)
	}

	// A batch with nothing in it reports no change rather than an empty rewrite.
	if _, _, changed := flattenPagesParallel(nil, nil, 72); changed {
		t.Error("an empty page batch reported changed = true")
	}
}

// TestSortedChecksOrdering pins the fixer application order -- clause, then
// subclause, then name -- over the whole catalog. The order has to be total:
// it decides which fixer runs first, and any pair left to map order would make
// the converted bytes differ between runs.
//
// It also records why sortedChecks's name comparison never actually decides
// anything today: (clause, subclause) is already unique across every
// registered check, so the tiebreak is there for a check added later that
// collides. If this stops holding, the tiebreak becomes live and the assertion
// below starts exercising it.
func TestSortedChecksOrdering(t *testing.T) {
	all := pdf.AllChecks()
	counts := make(map[pdf.Check]int, len(all))
	for _, c := range all {
		counts[c] = 1
	}
	got := sortedChecks(counts)
	if len(got) != len(counts) {
		t.Fatalf("sortedChecks returned %d checks, want %d", len(got), len(counts))
	}

	type key struct {
		clause    string
		subclause int
	}
	seen := make(map[key]string, len(got))
	for i, cur := range got {
		k := key{cur.Clause(), cur.Subclause()}
		if prevName, dup := seen[k]; dup {
			t.Logf("checks %q and %q share %s/%d: the name tiebreak is live",
				prevName, cur.Name(), k.clause, k.subclause)
		}
		seen[k] = cur.Name()
		if i == 0 {
			continue
		}
		prev := got[i-1]
		switch {
		case prev.Clause() != cur.Clause():
			if prev.Clause() > cur.Clause() {
				t.Fatalf("clause out of order at %d: %q then %q", i, prev.Clause(), cur.Clause())
			}
		case prev.Subclause() != cur.Subclause():
			if prev.Subclause() > cur.Subclause() {
				t.Fatalf("subclause out of order at %d: %d then %d", i, prev.Subclause(), cur.Subclause())
			}
		case prev.Name() >= cur.Name():
			t.Errorf("name out of order within %s/%d at %d: %q then %q",
				cur.Clause(), cur.Subclause(), i, prev.Name(), cur.Name())
		}
	}

	// The order must not depend on map iteration order.
	for range 5 {
		again := sortedChecks(counts)
		for i := range got {
			if again[i] != got[i] {
				t.Fatalf("sortedChecks is not deterministic: position %d was %q, now %q",
					i, got[i].Name(), again[i].Name())
			}
		}
	}
}
