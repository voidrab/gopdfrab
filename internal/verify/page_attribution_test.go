package verify

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// TestLinkDestDoesNotTakeOverPage pins that a page reached mid-walk sets the
// page for its own subtree only. Page 1 links to page 2, and /Annots sorts
// before /Resources, so the walk enters page 2 through the link before it
// reaches page 1's ExtGState. That /TR used to be reported on page 2.
func TestLinkDestDoesNotTakeOverPage(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R /StructTreeRoot << /Type /StructTreeRoot /G << /Type /ExtGState /TR /Identity >> >> >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Annots [5 0 R] /Resources << /ExtGState << /G1 << /Type /ExtGState /TR /Identity >> >> >> >>")
	b.Obj(4, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] /Resources << >> >>")
	b.Obj(5, "<< /Type /Annot /Subtype /Link /Rect [0 0 10 10] /F 4 /Dest [4 0 R /Fit] >>")
	data := b.FinishClassic("<< /Size 6 /Root 1 0 R /ID [<00112233445566778899aabbccddeeff><00112233445566778899aabbccddeeff>] >>")

	res, err := VerifyBytes(data, pdf.PDFA1B, nil)
	if err != nil {
		t.Fatal(err)
	}
	var pages []int
	for _, iss := range res.IssuesForCheck(pdf.Checks.Transparency.TransferFunction) {
		pages = append(pages, iss.Page())
	}
	// One /TR on page 1, and one under the catalog's /StructTreeRoot (sorted
	// after /Pages), which belongs to no page.
	if len(pages) != 2 || pages[0] == pages[1] {
		t.Fatalf("TR issues on pages %v, want one on page 1 and one document-level", pages)
	}
	for _, p := range pages {
		if p != 1 && p != 0 {
			t.Errorf("TR issue attributed to page %d, want page 1 or document-level (0)", p)
		}
	}
}
