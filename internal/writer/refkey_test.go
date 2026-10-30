package writer

import (
	"bytes"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// TestLiteralUnderscoreKeysRoundTrip pins that a document's own /_ref and
// /_dirty keys are ordinary data: kept on an indirect object, kept on a direct
// dict without making it look indirect, and written back out. The reader keeps
// an object's own reference outside its entries, so no key collides with it.
func TestLiteralUnderscoreKeysRoundTrip(t *testing.T) {
	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R /_ref (mine) /D << /_ref 7 /_dirty true >> >>")
	b.Obj(2, "<< /Type /Pages /Kids [] /Count 0 >>")
	in, err := pdf.OpenBytes(b.FinishClassic("<< /Size 3 /Root 1 0 R >>"))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	graph, err := in.ResolveGraph()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := WriteDocument(&out, graph.(pdf.PDFDict), 0); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}

	doc, err := pdf.OpenBytes(out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	g, err := doc.ResolveGraph()
	if err != nil {
		t.Fatal(err)
	}
	root := g.(pdf.PDFDict).Entries.Get("Root").(pdf.PDFDict)
	if got := root.Entries.Get("_ref"); got != (pdf.PDFString{Value: "mine"}) {
		t.Errorf("catalog /_ref = %#v, want the document's own string", got)
	}
	d := root.Entries.Get("D").(pdf.PDFDict)
	if d.Entries.Get("_ref") != pdf.PDFInteger(7) || d.Entries.Get("_dirty") != pdf.PDFBoolean(true) {
		t.Errorf("direct dict = %v, want /_ref 7 and /_dirty true kept", d.Entries)
	}
}
