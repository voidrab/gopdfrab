package fidelity

import (
	"fmt"
	"image"
	"slices"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// onePageDoc builds a one-page document whose single content stream is the
// given operator string, so tests can control exactly what renders.
func onePageDoc(content string) []byte {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>")
	b.StreamObj(4, "<<", []byte(content))
	return b.FinishClassic("<< /Size 5 /Root 1 0 R >>")
}

// compareDocs renders the documents input and output at DPI and compares them.
func compareDocs(t *testing.T, input, output []byte) []PageFidelity {
	t.Helper()
	summarize := func(data []byte) []Summary {
		r, err := pdf.OpenBytes(data)
		if err != nil {
			t.Fatalf("OpenBytes: %v", err)
		}
		defer r.Close()
		graph, err := r.ResolveGraph()
		if err != nil {
			t.Fatalf("ResolveGraph: %v", err)
		}
		return Summarize(graph.(pdf.PDFDict), DPI)
	}
	return Compare(summarize(input), summarize(output))
}

// compareRenders is Compare over pages still in hand; a nil image is a page
// that did not render.
func compareRenders(in, out []*image.RGBA) []PageFidelity {
	measure := func(imgs []*image.RGBA) []Summary {
		s := make([]Summary, len(imgs))
		for i, img := range imgs {
			s[i] = summarize(img)
		}
		return s
	}
	return Compare(measure(in), measure(out))
}

// filledPage draws a large black rectangle, so the page carries real ink.
const filledPage = "0 0 0 rg\n20 20 160 160 re\nf\n"

// TestFidelityIdentical verifies that a document compared against itself
// scores near-perfect similarity and is never flagged as blanked.
func TestFidelityIdentical(t *testing.T) {
	data := onePageDoc(filledPage)
	report := compareDocs(t, data, data)
	if len(report) != 1 {
		t.Fatalf("got %d page reports, want 1", len(report))
	}
	pf := report[0]
	if pf.Similarity < 0.99 {
		t.Errorf("identical similarity = %.3f, want >= 0.99", pf.Similarity)
	}
	if pf.Blanked() {
		t.Errorf("identical page reported blanked: %+v", pf)
	}
	if pf.InputInk < inkThreshold {
		t.Errorf("filled page InputInk = %.4f, want >= %.4f", pf.InputInk, inkThreshold)
	}
}

// TestFidelityBlankedDetected verifies that an inked input against a blank
// output is flagged as blanked with low similarity. This is the destructive
// failure the gate exists to catch.
func TestFidelityBlankedDetected(t *testing.T) {
	input := onePageDoc(filledPage)
	output := onePageDoc("q\nQ\n") // renders nothing

	report := compareDocs(t, input, output)
	if len(report) != 1 {
		t.Fatalf("got %d page reports, want 1", len(report))
	}
	pf := report[0]
	if !pf.Blanked() {
		t.Errorf("blanked page not detected: %+v", pf)
	}
	if pf.Similarity > 0.5 {
		t.Errorf("blanked similarity = %.3f, want low", pf.Similarity)
	}
}

// TestFidelityChangedNotBlanked verifies that a page whose content moves or
// changes but still carries comparable ink is not flagged as blanked, even
// though its pixel similarity drops. Legitimate changes, such as font
// substitution, do not trip the destructive-loss gate.
func TestFidelityChangedNotBlanked(t *testing.T) {
	input := onePageDoc("0 0 0 rg\n20 20 70 70 re\nf\n")    // rectangle bottom-left
	output := onePageDoc("0 0 0 rg\n110 110 70 70 re\nf\n") // same-size rectangle top-right

	report := compareDocs(t, input, output)
	pf := report[0]
	if pf.Blanked() {
		t.Errorf("moved-but-present content wrongly flagged blanked: %+v", pf)
	}
	if pf.OutputInk < inkThreshold {
		t.Errorf("output should still carry ink: %+v", pf)
	}
}

// TestInkFractionExtremes verifies that a blank page has no ink and that two
// blank pages are not reported as blanked.
func TestInkFractionExtremes(t *testing.T) {
	white := compareDocs(t, onePageDoc("q\nQ\n"), onePageDoc("q\nQ\n"))
	if len(white) != 1 || white[0].InputInk >= inkThreshold {
		t.Errorf("blank page InputInk should be ~0, got %+v", white)
	}
	// Two blank pages are identical, so not blanked (no ink was lost).
	if white[0].Blanked() {
		t.Errorf("blank-to-blank should not be blanked: %+v", white[0])
	}
}

// TestCompareMissingRenders verifies that the comparison survives
// pages the rasterizer could not draw. A page missing on the input side has no
// baseline and is skipped entirely; one missing only on the output side is
// total loss, not a skipped page, or a conversion that stopped rendering would
// report clean.
func TestCompareMissingRenders(t *testing.T) {
	inked := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for i := range inked.Pix {
		inked.Pix[i] = 0
	}

	report := compareRenders(
		[]*image.RGBA{nil, inked, inked},
		[]*image.RGBA{inked, nil, inked},
	)
	if len(report) != 2 {
		t.Fatalf("got %d page reports, want 2 (page 1 has no baseline)", len(report))
	}
	if report[0].Page != 2 || report[0].OutputInk != 0 || report[0].Similarity != 0 {
		t.Errorf("page 2 = %+v, want a fully lost page", report[0])
	}
	if !report[0].Blanked() {
		t.Errorf("an unrenderable output page was not reported blanked: %+v", report[0])
	}
	if report[1].Page != 3 || report[1].Similarity < 0.99 {
		t.Errorf("page 3 = %+v, want an intact comparison", report[1])
	}

	// The report covers only pages present on both sides.
	if got := compareRenders([]*image.RGBA{inked, inked}, []*image.RGBA{inked}); len(got) != 1 {
		t.Errorf("got %d page reports for a shortened output, want 1", len(got))
	}
}

// TestFidelityMetricsOnEmptyRenders verifies that the ink and grid metrics,
// which are fed straight from the rasterizer, absorb a nil or zero-sized image
// rather than dividing by zero or indexing an empty pixel buffer.
func TestFidelityMetricsOnEmptyRenders(t *testing.T) {
	if got := inkFraction(nil); got != 0 {
		t.Errorf("inkFraction(nil) = %v, want 0", got)
	}
	empty := image.NewRGBA(image.Rect(0, 0, 0, 0))
	if got := inkFraction(empty); got != 0 {
		t.Errorf("inkFraction(empty) = %v, want 0", got)
	}
	if got := grayGrid(empty); got != [fidelityGrid * fidelityGrid]float64{} {
		t.Error("grayGrid(empty) returned non-zero samples")
	}
	if got := gridSimilarity(grayGrid(empty), grayGrid(empty)); got != 1 {
		t.Errorf("gridSimilarity of two empty renders = %v, want 1", got)
	}
}

// TestBlankedPages verifies that the page list is the Blanked() reports in
// page order, and that it stays nil when nothing was lost, so len is enough to
// test it.
func TestBlankedPages(t *testing.T) {
	lost := PageFidelity{Page: 2, InputInk: 0.5, OutputInk: 0}
	kept := PageFidelity{Page: 1, InputInk: 0.5, OutputInk: 0.5, Similarity: 1}
	if got := BlankedPages([]PageFidelity{kept, lost, {Page: 3, InputInk: 0.5, OutputInk: 0}}); !slices.Equal(got, []int{2, 3}) {
		t.Errorf("blankedPages = %v, want [2 3]", got)
	}
	if got := BlankedPages([]PageFidelity{kept}); got != nil {
		t.Errorf("blankedPages with nothing lost = %v, want nil", got)
	}
	if got := BlankedPages(nil); got != nil {
		t.Errorf("BlankedPages(nil) = %v, want nil", got)
	}
}

// TestOverpainted verifies that a page is judged overpainted by the area it
// covers, not by the ink it gains. Restoring text that the input's own fonts
// could not draw adds a great deal of ink and covers nothing. Conversions do
// this often, so counting that ink as damage would hide real overpainting.
func TestOverpainted(t *testing.T) {
	for _, tc := range []struct {
		name        string
		covered     float64
		overpainted bool
	}{
		{"page painted over", 0.9, true},
		{"a band across the page", 0.06, true},
		{"just under the bar", 0.049, false},
		{"nothing covered", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The ink numbers say nothing either way: these are the figures a
			// page of restored text carries, and it must not be flagged.
			pf := PageFidelity{Page: 1, InputInk: 0.01, OutputInk: 0.07, Covered: tc.covered}
			if got := pf.Overpainted(); got != tc.overpainted {
				t.Errorf("Overpainted() = %v for %+v, want %v", got, pf, tc.overpainted)
			}
		})
	}
}

// TestCoveredFraction verifies that a cell counts as covered when it was paper
// and is now solid. Text darkens a cell it falls in without covering it, which
// is exactly what must not count.
func TestCoveredFraction(t *testing.T) {
	paper := onePageDoc("")
	// A rectangle over the top half of the page, and a page of text-like marks
	// spread across it: thin lines, one every few points.
	covered := onePageDoc("0 0 0 rg\n0 100 200 100 re\nf\n")
	var marks string
	for y := 4; y < 200; y += 6 {
		marks += fmt.Sprintf("0 0 0 rg\n10 %d 180 1 re\nf\n", y)
	}
	inked := onePageDoc(marks)

	report := compareDocs(t, paper, covered)
	if got := report[0].Covered; got < 0.4 || got > 0.6 {
		t.Errorf("half the page covered reported %.3f, want about 0.5", got)
	}
	if !report[0].Overpainted() {
		t.Errorf("a rectangle over half the page was not reported: %+v", report[0])
	}

	report = compareDocs(t, paper, inked)
	pf := report[0]
	if pf.OutputInk < inkThreshold {
		t.Fatalf("the marks should carry ink: %+v", pf)
	}
	if pf.Overpainted() {
		t.Errorf("marks spread over the page were reported as covering it: %+v", pf)
	}
}

// TestCellMeansOnEmptyRender verifies that the grid, which is fed straight from
// the rasterizer, absorbs a zero-sized image.
func TestCellMeansOnEmptyRender(t *testing.T) {
	if got := cellMeans(image.NewRGBA(image.Rect(0, 0, 0, 0))); got != [fidelityGrid * fidelityGrid]float64{} {
		t.Error("cellMeans of an empty render returned non-zero cells")
	}
}

// TestOverpaintedPages verifies that the page list mirrors BlankedPages: page
// order, and nil when nothing was drawn over.
func TestOverpaintedPages(t *testing.T) {
	over := PageFidelity{Page: 2, Covered: 0.8}
	kept := PageFidelity{Page: 1, InputInk: 0.5, OutputInk: 0.5, Similarity: 1}
	if got := OverpaintedPages([]PageFidelity{kept, over, {Page: 3, Covered: 0.9}}); !slices.Equal(got, []int{2, 3}) {
		t.Errorf("overpaintedPages = %v, want [2 3]", got)
	}
	if got := OverpaintedPages([]PageFidelity{kept}); got != nil {
		t.Errorf("overpaintedPages with nothing added = %v, want nil", got)
	}
	if got := OverpaintedPages(nil); got != nil {
		t.Errorf("OverpaintedPages(nil) = %v, want nil", got)
	}
}

// TestSummarizeWithoutPages checks that a trailer with no page tree measures
// no pages, and that a page whose content cannot be read is kept, unrendered,
// so the pages that follow keep their numbers.
func TestSummarizeWithoutPages(t *testing.T) {
	if got := Summarize(pdf.NewPDFDict(), DPI); got != nil {
		t.Errorf("Summarize(no Root) = %v, want nil", got)
	}

	page := pdf.NewPDFDict()
	page.Entries.Set("Type", pdf.PDFName{Value: "Page"})
	page.Entries.Set("MediaBox", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(10), pdf.PDFInteger(10)})
	bad := pdf.NewStreamDict(pdf.NewDict(), []byte("not deflate"))
	bad.Entries.Set("Filter", pdf.PDFName{Value: "FlateDecode"})
	page.Entries.Set("Contents", bad)
	pages := pdf.NewPDFDict()
	pages.Entries.Set("Type", pdf.PDFName{Value: "Pages"})
	pages.Entries.Set("Kids", pdf.PDFArray{page})
	root := pdf.NewPDFDict()
	root.Entries.Set("Pages", pages)
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", root)
	if got := Summarize(trailer, DPI); len(got) != 1 || got[0].rendered {
		t.Errorf("Summarize(unreadable page) = %d summaries, want one unrendered", len(got))
	}
}
