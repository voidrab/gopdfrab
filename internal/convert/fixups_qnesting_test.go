package convert

import (
	"bytes"
	"image"
	"slices"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/raster"
	"github.com/voidrab/gopdfrab/internal/verify"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// nestedQ wraps inner in n levels of q ... Q.
func nestedQ(n int, inner string) string {
	return strings.Repeat("q\n", n) + inner + strings.Repeat("Q\n", n)
}

// renderFirstPage renders a document's first page at 72 dpi.
func renderFirstPage(t *testing.T, data []byte) *image.RGBA {
	t.Helper()
	r := openReader(t, data)
	trailer, err := resolveTrailer(r)
	if err != nil {
		t.Fatalf("resolveTrailer: %v", err)
	}
	pages := orderedPages(trailer)
	if len(pages) == 0 {
		t.Fatal("document has no pages")
	}
	img, _, err := raster.RenderPage(pages[0].dict, pages[0].resources, pages[0].mediaBox, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	return img
}

// maxQDepth is the deepest q nesting in one decoded content stream.
func maxQDepth(t *testing.T, d pdf.PDFDict) int {
	t.Helper()
	data, err := pdf.DecodeStream(d)
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	depth, deepest := 0, 0
	pdf.NewContentScanner(data).Scan(func(op string, _ []pdf.PDFValue) {
		switch op {
		case "q":
			depth++
			deepest = max(deepest, depth)
		case "Q":
			depth = max(depth-1, 0)
		}
	})
	return deepest
}

// pageWithContent builds a one-page trailer graph drawing content, with an
// empty resource dict the repair can name its forms in.
func pageWithContent(content string) (trailer, page, resources pdf.PDFDict) {
	resources = pdf.NewPDFDict()
	page = pdf.NewPDFDict()
	page.Entries.Set("Type", pdf.PDFName{Value: "Page"})
	page.Entries.Set("MediaBox", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(200), pdf.PDFInteger(200)})
	page.Entries.Set("Resources", resources)
	page.Entries.Set("Contents", streamDict(content))
	return pagesTrailer(page), page, resources
}

// pagesTrailer builds a trailer whose page tree holds pages.
func pagesTrailer(pages ...pdf.PDFDict) pdf.PDFDict {
	kids := pdf.PDFArray{}
	for _, p := range pages {
		kids = append(kids, p)
	}
	tree := pdf.NewPDFDict()
	tree.Entries.Set("Type", pdf.PDFName{Value: "Pages"})
	tree.Entries.Set("Kids", kids)
	tree.Entries.Set("Count", pdf.PDFInteger(len(pages)))
	catalog := pdf.NewPDFDict()
	catalog.Entries.Set("Type", pdf.PDFName{Value: "Catalog"})
	catalog.Entries.Set("Pages", tree)
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", catalog)
	return trailer
}

// TestConvertUnnestsDeepGraphicsState converts a page nesting q/Q 31 deep,
// with balanced marked-content and BX/EX sections inside the moved block, and
// checks the violation is repaired in place and the page still renders
// exactly as before. The deep drawing sits under a quarter-scale CTM and
// reaches past the page's own coordinates in that space, so a form clipped
// to the unmapped page box would cut most of it off.
func TestConvertUnnestsDeepGraphicsState(t *testing.T) {
	content := "BT\nET\nq 0.25 0 0 0.25 0 0 cm\n" +
		nestedQ(30, "/Tag BMC\nBX\n0.5 g\n100 100 600 600 re\nf\nEX\nEMC\n0 g\n2 0 0 2 0 0 cm\n60 60 20 20 re\nf\n") +
		"Q\n0 g\n0 0 10 10 re\nf\n"
	data := onePageDoc(content)
	before, err := verify.VerifyBytes(data, pdf.PDFA1B, nil)
	if err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
	if !hasIssueForCheck(before.Issues, pdf.Checks.Structure.GraphicsStateNesting) {
		t.Fatal("input is not flagged with GraphicsStateNesting; the fixture proves nothing")
	}

	cr, err := ConvertBytes(data, pdf.PDFA1B, Options{})
	if err != nil {
		t.Fatalf("ConvertBytes: %v", err)
	}
	defer cr.Close()
	if !cr.Result.Valid {
		t.Fatalf("converted output not conformant: %v", issueClauses(cr.Residual()))
	}
	assertNotRasterized(t, cr)

	want, got := renderFirstPage(t, data), renderFirstPage(t, mustOutput(t, cr))
	if !bytes.Equal(want.Pix, got.Pix) {
		t.Error("the unnested page renders differently from the input")
	}

	// The renderer does not clip a form to its BBox, so check the clip a
	// viewer applies directly: the page, mapped into the quarter-scale space
	// the block draws in.
	trailer, err := resolveTrailer(openReader(t, mustOutput(t, cr)))
	if err != nil {
		t.Fatalf("resolveTrailer: %v", err)
	}
	xobjects, _ := orderedPages(trailer)[0].resources.Entries.Get("XObject").(pdf.PDFDict)
	form, ok := xobjects.Entries.Get("QNest0").(pdf.PDFDict)
	if !ok {
		t.Fatal("no QNest0 form in the output page's resources")
	}
	if bbox, _ := rectOf(form.Entries.Get("BBox")); bbox != (rect{0, 0, 800, 800}) {
		t.Errorf("form BBox = %v, want [0 0 800 800]", bbox)
	}
}

// TestQNestingFixerSplitsRepeatedly covers a block too deep to fit even after
// one move: the form holding it nests past the limit again, so it is split
// again inside the form. Every stream ends within the limit.
func TestQNestingFixerSplitsRepeatedly(t *testing.T) {
	trailer, page, resources := pageWithContent(nestedQ(70, "0 0 10 10 re f\n"))
	changed, err := qNestingFixer{spec: a1bSpec}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("Fix = (%v, %v), want (true, nil)", changed, err)
	}
	contents, _ := page.Entries.Get("Contents").(pdf.PDFDict)
	if d := maxQDepth(t, contents); d > maxQNesting {
		t.Errorf("page content nests %d deep", d)
	}
	xobjects, _ := resources.Entries.Get("XObject").(pdf.PDFDict)
	if n := xobjects.Entries.Len(); n != 2 {
		t.Errorf("%d forms created, want 2 (70 levels split at 28)", n)
	}
	for name, v := range xobjects.Entries.All() {
		form := v.(pdf.PDFDict)
		if d := maxQDepth(t, form); d > maxQNesting {
			t.Errorf("form %s nests %d deep", name, d)
		}
		if res, _ := form.Entries.Get("Resources").(pdf.PDFDict); pdf.ValuePointer(res.Entries) != pdf.ValuePointer(resources.Entries) {
			t.Errorf("form %s does not read the page's resources", name)
		}
	}

	// A second pass finds nothing left to do.
	if changed, _ := (qNestingFixer{spec: a1bSpec}).Fix(&trailer, nil); changed {
		t.Error("second Fix changed the graph; the repair is not idempotent")
	}
}

// TestQNestingFixerRepairsSharedStreamOnce verifies that a content stream two
// pages share is rewritten once, and that both pages end up drawing the
// rewritten stream.
func TestQNestingFixerRepairsSharedStreamOnce(t *testing.T) {
	shared := streamDict(nestedQ(30, ""))
	resources := pdf.NewPDFDict()
	var pages []pdf.PDFDict
	for range 2 {
		p := pdf.NewPDFDict()
		p.Entries.Set("Type", pdf.PDFName{Value: "Page"})
		p.Entries.Set("Resources", resources)
		p.Entries.Set("Contents", shared)
		pages = append(pages, p)
	}
	trailer := pagesTrailer(pages...)
	if changed, _ := (qNestingFixer{spec: a1bSpec}).Fix(&trailer, nil); !changed {
		t.Fatal("Fix reported no change")
	}
	a, _ := pages[0].Entries.Get("Contents").(pdf.PDFDict)
	b, _ := pages[1].Entries.Get("Contents").(pdf.PDFDict)
	if !bytes.Equal(a.Stream(), b.Stream()) {
		t.Error("the pages sharing a stream ended up with different content")
	}
	if xobjects, _ := resources.Entries.Get("XObject").(pdf.PDFDict); xobjects.Entries.Len() != 1 {
		t.Errorf("XObject = %v, want one form", xobjects)
	}
}

// TestQNestingFixerRepairsFormsAndPatterns covers the streams with a scope of
// their own: a form XObject and a tiling pattern with /Resources and /BBox are
// repaired; ones missing either, whose names or visible area are not known
// for certain, are left alone.
func TestQNestingFixerRepairsFormsAndPatterns(t *testing.T) {
	deep := nestedQ(30, "")
	bbox := pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(10), pdf.PDFInteger(10)}
	build := func(kind string, withRes, withBBox pdf.PDFValue) pdf.PDFDict {
		d := streamDict(deep)
		if kind == "form" {
			d.Entries.Set("Subtype", pdf.PDFName{Value: "Form"})
		} else {
			d.Entries.Set("PatternType", pdf.PDFInteger(1))
		}
		if withRes != nil {
			d.Entries.Set("Resources", withRes)
		}
		if withBBox != nil {
			d.Entries.Set("BBox", withBBox)
		}
		return d
	}
	form := build("form", pdf.NewPDFDict(), bbox)
	pattern := build("pattern", pdf.NewPDFDict(), bbox)
	noRes := build("form", nil, bbox)
	noBBox := build("form", pdf.NewPDFDict(), nil)
	badBBox := build("form", pdf.NewPDFDict(), pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFName{Value: "x"}, pdf.PDFInteger(1), pdf.PDFInteger(1)})
	notStream := pdf.NewPDFDict()
	notStream.Entries.Set("Subtype", pdf.PDFName{Value: "Form"})
	other := streamDict(deep)

	holder := pdf.NewPDFDict()
	for name, d := range map[string]pdf.PDFDict{
		"form": form, "pattern": pattern, "noRes": noRes, "noBBox": noBBox,
		"badBBox": badBBox, "notStream": notStream, "other": other,
	} {
		holder.Entries.Set(name, d)
	}
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Holder", holder)

	if changed, _ := (qNestingFixer{spec: a1bSpec}).Fix(&trailer, nil); !changed {
		t.Fatal("Fix reported no change")
	}
	for _, name := range []string{"form", "pattern"} {
		if d := maxQDepth(t, holder.Entries.Get(name).(pdf.PDFDict)); d > maxQNesting {
			t.Errorf("%s still nests %d deep", name, d)
		}
	}
	for _, name := range []string{"noRes", "noBBox", "badBBox", "other"} {
		if d := maxQDepth(t, holder.Entries.Get(name).(pdf.PDFDict)); d != 30 {
			t.Errorf("%s nests %d deep, want it left at 30", name, d)
		}
	}
}

// TestQNestingFixerLeavesUnsafeBlocks covers every block a form cannot hold
// without changing the drawing. Each is left untouched for the raster
// fallback, with the rest of the page's content.
func TestQNestingFixerLeavesUnsafeBlocks(t *testing.T) {
	for name, content := range map[string]string{
		"opens in a text object":     "BT\n" + nestedQ(30, "") + "ET\n",
		"ends a text object":         nestedQ(27, "q\nq\nET\nQ\nQ\n"),
		"opens a text object twice":  nestedQ(27, "q\nq\nBT\nBT\nQ\nQ\n"),
		"leaves a text object open":  nestedQ(27, "q\nq\nBT\nQ\nQ\n"),
		"closes marked content":      nestedQ(27, "q\nq\nEMC\nQ\nQ\n"),
		"leaves marked content open": nestedQ(27, "q\nq\n/Tag BMC\nQ\nQ\n"),
		"closes a BX section":        nestedQ(27, "q\nq\nEX\nQ\nQ\n"),
		"singular CTM":               nestedQ(27, "0 0 0 0 0 0 cm\nq\nq\nQ\nQ\n"),
		// Balanced as a whole, but the split needed inside the form would
		// open within its text object.
		"unsafe split inside a form": nestedQ(27, "q\nBT\n"+nestedQ(30, "")+"ET\nQ\n"),
	} {
		t.Run(name, func(t *testing.T) {
			trailer, page, _ := pageWithContent(content)
			before := page.Entries.Get("Contents").(pdf.PDFDict).Stream()
			if changed, _ := (qNestingFixer{spec: a1bSpec}).Fix(&trailer, nil); changed {
				t.Error("Fix changed the page")
			}
			if after := page.Entries.Get("Contents").(pdf.PDFDict).Stream(); !bytes.Equal(before, after) {
				t.Error("the page's content was rewritten")
			}
		})
	}
}

// TestQNestingFixerContentsArray verifies that a page's /Contents array is
// treated as one drawing cut into pieces. A block still open where a piece
// ends may be closed by the next piece, so it moves only in the last one.
func TestQNestingFixerContentsArray(t *testing.T) {
	open := strings.Repeat("q\n", 30)
	first, last := streamDict(open), streamDict(open)
	resources := pdf.NewPDFDict()
	page := pdf.NewPDFDict()
	page.Entries.Set("Type", pdf.PDFName{Value: "Page"})
	page.Entries.Set("Resources", resources)
	page.Entries.Set("Contents", pdf.PDFArray{first, pdf.PDFInteger(7), last})
	trailer := pagesTrailer(page)

	if changed, _ := (qNestingFixer{spec: a1bSpec}).Fix(&trailer, nil); !changed {
		t.Fatal("Fix reported no change")
	}
	contents := page.Entries.Get("Contents").(pdf.PDFArray)
	if d := maxQDepth(t, contents[0].(pdf.PDFDict)); d != 30 {
		t.Errorf("first piece nests %d deep, want it left at 30", d)
	}
	if d := maxQDepth(t, contents[2].(pdf.PDFDict)); d > maxQNesting {
		t.Errorf("last piece still nests %d deep", d)
	}
}

// TestQNestingFixerSkipsWhatItCannotRead covers the inputs the repair passes
// over: a page with no resources to name a form in, a stream that does not
// decode, one the scanner cannot finish, and one within the limit.
func TestQNestingFixerSkipsWhatItCannotRead(t *testing.T) {
	noResources := pdf.NewPDFDict()
	noResources.Entries.Set("Type", pdf.PDFName{Value: "Page"})
	noResources.Entries.Set("Contents", streamDict(nestedQ(30, "")))

	broken := streamDict(nestedQ(30, ""))
	broken.Entries.Set("Filter", pdf.PDFName{Value: "FlateDecode"})
	truncated := streamDict(nestedQ(30, "") + "(unterminated")
	shallow := streamDict(nestedQ(3, ""))

	var pages []pdf.PDFDict
	for _, contents := range []pdf.PDFDict{broken, truncated, shallow} {
		_, p, _ := pageWithContent("")
		p.Entries.Set("Contents", contents)
		pages = append(pages, p)
	}
	trailer := pagesTrailer(append(pages, noResources)...)
	if changed, _ := (qNestingFixer{spec: a1bSpec}).Fix(&trailer, nil); changed {
		t.Error("Fix changed a graph it has nothing it can repair in")
	}
}

// TestQNestingNameAvoidsCollision verifies that the form's name skips one the
// resource dict already uses.
func TestQNestingNameAvoidsCollision(t *testing.T) {
	trailer, _, resources := pageWithContent(nestedQ(30, ""))
	xobjects := pdf.NewPDFDict()
	xobjects.Entries.Set("QNest0", pdf.PDFInteger(1))
	resources.Entries.Set("XObject", xobjects)
	if changed, _ := (qNestingFixer{spec: a1bSpec}).Fix(&trailer, nil); !changed {
		t.Fatal("Fix reported no change")
	}
	if xobjects.Entries.Get("QNest0") != pdf.PDFInteger(1) {
		t.Error("existing QNest0 was overwritten")
	}
	if _, ok := xobjects.Entries.Get("QNest1").(pdf.PDFDict); !ok {
		t.Error("form not named QNest1")
	}
}

// TestUnnestOpsDeclinesUnwritableContent verifies that an operator the content
// writer cannot serialize -- an inline image without its raw bytes, which a
// scanned stream always has -- stops the rewrite instead of truncating the
// stream, both in the page and in a form being built.
func TestUnnestOpsDeclinesUnwritableContent(t *testing.T) {
	bad := writer.ContentOp{Op: "INLINEIMAGE"}
	q, Q := writer.ContentOp{Op: "q"}, writer.ContentOp{Op: "Q"}
	repeat := func(op writer.ContentOp, n int) []writer.ContentOp {
		out := make([]writer.ContentOp, n)
		for i := range out {
			out[i] = op
		}
		return out
	}
	visible := rect{0, 0, 10, 10}
	for name, ops := range map[string][]writer.ContentOp{
		"in the form": slices.Concat(repeat(q, 30), []writer.ContentOp{bad}, repeat(Q, 30)),
		"in the page": slices.Concat([]writer.ContentOp{bad}, repeat(q, 30), repeat(Q, 30)),
	} {
		if _, ok := unnestOps(streamDict(""), ops, pdf.NewPDFDict(), visible, true, maxQNesting); ok {
			t.Errorf("%s: unwritable content was rewritten", name)
		}
	}
}

// TestCmMatrixRejectsMalformedOperands verifies that a cm with the wrong
// operand count or a non-number operand is not read as a matrix, and that the
// repair steps over it.
func TestCmMatrixRejectsMalformedOperands(t *testing.T) {
	if _, ok := cmMatrix([]pdf.PDFValue{pdf.PDFInteger(1)}); ok {
		t.Error("a one-operand cm was read as a matrix")
	}
	six := []pdf.PDFValue{pdf.PDFInteger(1), pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(1), pdf.PDFInteger(0), pdf.PDFName{Value: "x"}}
	if _, ok := cmMatrix(six); ok {
		t.Error("a cm with a name operand was read as a matrix")
	}
	// The repair steps over both, and over a Q with nothing to restore.
	trailer, page, _ := pageWithContent("Q\n1 cm\n1 0 0 1 0 /x cm\n" + nestedQ(30, ""))
	if changed, _ := (qNestingFixer{spec: a1bSpec}).Fix(&trailer, nil); !changed {
		t.Fatal("Fix reported no change")
	}
	if d := maxQDepth(t, page.Entries.Get("Contents").(pdf.PDFDict)); d > maxQNesting {
		t.Errorf("page still nests %d deep", d)
	}
}

// TestRectOfNormalisesCorners verifies that a rectangle given upper-right first
// reads as the same area; anything but four numbers is refused.
func TestRectOfNormalisesCorners(t *testing.T) {
	got, ok := rectOf(pdf.PDFArray{pdf.PDFInteger(10), pdf.PDFInteger(20), pdf.PDFInteger(0), pdf.PDFInteger(5)})
	if !ok || got != (rect{0, 5, 10, 20}) {
		t.Errorf("rectOf = (%v, %v), want ([0 5 10 20], true)", got, ok)
	}
	if _, ok := rectOf(pdf.PDFInteger(1)); ok {
		t.Error("a non-array was read as a rectangle")
	}
}
