package convert

import (
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
