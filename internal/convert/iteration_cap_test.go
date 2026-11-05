package convert

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// TestIterationCapDoesNotRasterizeFixedPages pins that the raster backstop
// works from a verify of the graph as it stands. With MaxIterations 1 the
// loop ends on the fix pass that removes page 1's /TR, unverified. The
// backstop works from a fresh verify, so page 1 is not rasterized from the
// stale issue list left by that pass.
func TestIterationCapDoesNotRasterizeFixedPages(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Resources << /ExtGState << /G1 << /Type /ExtGState /TR /Identity >> >> >> /Contents 4 0 R >>")
	b.StreamObj(4, "<<", []byte("/G1 gs 0 0 10 10 re f"))
	in := b.FinishClassic("<< /Size 5 /Root 1 0 R /ID [<00112233445566778899aabbccddeeff><00112233445566778899aabbccddeeff>] >>")

	cr, err := ConvertBytes(in, pdf.PDFA1B, Options{MaxIterations: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()
	if !cr.Result.Valid {
		t.Fatalf("output not valid: %v", cr.Result.Issues)
	}
	if len(cr.RasterizedPages) != 0 {
		t.Errorf("rasterized pages %v, want none: the one fix pass already repaired page 1", cr.RasterizedPages)
	}
}

// TestRasterBackstopKeepsPagesThatNeverUsedTheFont verifies that a font no fixer
// can embed flattens exactly the pages that hold it, although the verifier reports
// the font on the first of them only.
func TestRasterBackstopKeepsPagesThatNeverUsedTheFont(t *testing.T) {
	const page = "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>"
	b := pdfgen.NewBuilder("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [4 0 R 5 0 R 6 0 R] /Count 3 >>")
	b.Obj(3, "<< /Type /Font /Subtype /Type0 /BaseFont /HeiseiKakuGo-W5 /Encoding /UniJIS-UCS2-H /DescendantFonts [<< /Type /Font /Subtype /CIDFontType0 /BaseFont /HeiseiKakuGo-W5 /CIDSystemInfo << /Registry (Adobe) /Ordering (Japan1) /Supplement 2 >> >>] >>")
	b.Obj(4, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Contents 7 0 R >>")
	b.Obj(5, fmt.Sprintf(page, 8))
	b.Obj(6, fmt.Sprintf(page, 9))
	b.StreamObj(7, "<<", []byte("0 0 10 10 re f"))
	b.StreamObj(8, "<<", []byte("BT /F1 12 Tf 10 10 Td <3042> Tj ET"))
	b.StreamObj(9, "<<", []byte("BT /F1 12 Tf 10 10 Td <3044> Tj ET"))
	in := b.FinishClassic("<< /Size 10 /Root 1 0 R /ID [<00112233445566778899aabbccddeeff><00112233445566778899aabbccddeeff>] >>")

	cr, err := ConvertBytes(in, pdf.PDFA1B, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer cr.Close()
	if !cr.Result.Valid {
		t.Fatalf("output not valid: %v", cr.Result.Issues)
	}
	if !slices.Equal(cr.RasterizedPages, []int{2, 3}) {
		t.Errorf("rasterized pages %v, want [2 3]", cr.RasterizedPages)
	}
}

// TestRasterBackstopFlattensEveryPageAfterTheLastPagePass verifies that a residual
// outlasting the page passes ends in the flatten-all pass, and that no page pass
// rebuilds a page twice.
func TestRasterBackstopFlattensEveryPageAfterTheLastPagePass(t *testing.T) {
	trailer := onePageTrailer()
	flattened := map[int]bool{1: true}
	issue := pdf.NewError(pdf.Checks.Colour.OutputIntentNotArray, []error{errors.New("synthetic")}, 1, nil)
	if _, _, changed := applyRasterFallback(context.Background(), &trailer, []pdf.PDFError{issue}, flattened, 72); changed {
		t.Error("a page already flattened was rebuilt")
	}

	doc := openTrailer(t, trailer)
	c := pdf.Checks.Colour.OutputIntentNotArray
	r := backstopRun(context.Background(), doc, trailer, map[pdf.Check]Fixer{c: nil}, pdf.NewError(c, []error{errors.New("synthetic")}, 1, nil))
	if err := r.backstop(); err != nil {
		t.Fatalf("backstop: %v", err)
	}
	if !slices.Equal(r.cr.RasterizedPages, []int{1}) {
		t.Errorf("rasterized pages %v, want [1]", r.cr.RasterizedPages)
	}
}
