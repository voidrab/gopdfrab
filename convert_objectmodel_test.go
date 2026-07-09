package gopdfrab

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// objModelFixture serializes a minimal one-page PDF whose only object-model
// defect is a direct FontDescriptor, which ISO 32000 requires to be indirect.
func objModelFixture(t *testing.T) []byte {
	t.Helper()

	desc := pdf.NewPDFDict() // deliberately no _ref: inlined in the font dict
	desc.Entries["Type"] = pdf.PDFName{Value: "FontDescriptor"}
	desc.Entries["FontName"] = pdf.PDFName{Value: "Helvetica"}
	desc.Entries["Flags"] = pdf.PDFInteger(32)
	desc.Entries["FontBBox"] = pdf.PDFArray{pdf.PDFInteger(-166), pdf.PDFInteger(-225), pdf.PDFInteger(1000), pdf.PDFInteger(931)}
	desc.Entries["ItalicAngle"] = pdf.PDFInteger(0)
	desc.Entries["Ascent"] = pdf.PDFInteger(718)
	desc.Entries["Descent"] = pdf.PDFInteger(-207)
	desc.Entries["CapHeight"] = pdf.PDFInteger(718)
	desc.Entries["StemV"] = pdf.PDFInteger(88)

	font := pdf.NewPDFDict()
	font.Entries["Type"] = pdf.PDFName{Value: "Font"}
	font.Entries["Subtype"] = pdf.PDFName{Value: "Type1"}
	font.Entries["BaseFont"] = pdf.PDFName{Value: "Helvetica"}
	font.Entries["FontDescriptor"] = desc
	font.Entries["_ref"] = pdf.PDFRef{ObjNum: 5}

	fontMap := pdf.NewPDFDict()
	fontMap.Entries["F1"] = font
	resources := pdf.NewPDFDict()
	resources.Entries["Font"] = fontMap

	contents := pdf.NewPDFDict()
	contents.HasStream = true
	contents.RawStream = []byte("BT /F1 12 Tf 72 720 Td (x) Tj ET")
	contents.Entries["_ref"] = pdf.PDFRef{ObjNum: 4}

	pages := pdf.NewPDFDict()
	pages.Entries["Type"] = pdf.PDFName{Value: "Pages"}
	pages.Entries["Count"] = pdf.PDFInteger(1)
	pages.Entries["_ref"] = pdf.PDFRef{ObjNum: 2}

	page := pdf.NewPDFDict()
	page.Entries["Type"] = pdf.PDFName{Value: "Page"}
	page.Entries["Parent"] = pages
	page.Entries["MediaBox"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(612), pdf.PDFInteger(792)}
	page.Entries["Resources"] = resources
	page.Entries["Contents"] = contents
	page.Entries["_ref"] = pdf.PDFRef{ObjNum: 3}
	pages.Entries["Kids"] = pdf.PDFArray{page}

	catalog := pdf.NewPDFDict()
	catalog.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
	catalog.Entries["Pages"] = pages
	catalog.Entries["_ref"] = pdf.PDFRef{ObjNum: 1}

	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = catalog

	var buf bytes.Buffer
	if err := writer.WriteDocument(&buf, trailer); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	return buf.Bytes()
}

// TestConvertObjectModelAPI exercises the full ConvertObjectModel surface:
// bytes, path, and Document forms all repair an object-model-invalid input
// into a fully conformant rewrite.
func TestConvertObjectModelAPI(t *testing.T) {
	data := objModelFixture(t)

	res, err := VerifyObjectModelBytes(data)
	if err != nil {
		t.Fatalf("VerifyObjectModelBytes: %v", err)
	}
	if res.Valid {
		t.Fatal("fixture must be object-model invalid (direct FontDescriptor)")
	}

	cr, err := ConvertObjectModelBytes(data)
	if err != nil {
		t.Fatalf("ConvertObjectModelBytes: %v", err)
	}
	if !cr.Result.Valid || len(cr.Residual()) != 0 {
		t.Fatalf("ConvertObjectModelBytes: Valid=%v, residual %v", cr.Result.Valid, cr.Residual())
	}

	out, err := VerifyObjectModelBytes(cr.Output)
	if err != nil {
		t.Fatalf("VerifyObjectModelBytes(output): %v", err)
	}
	if !out.Valid {
		t.Errorf("output independently re-verifies as invalid: %v", out.Issues)
	}

	path := filepath.Join(t.TempDir(), "objmodel.pdf")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cr, err = ConvertObjectModel(path)
	if err != nil {
		t.Fatalf("ConvertObjectModel: %v", err)
	}
	if !cr.Result.Valid {
		t.Errorf("ConvertObjectModel: residual %v", cr.Residual())
	}

	doc, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer doc.Close()
	cr, err = doc.ConvertObjectModel()
	if err != nil {
		t.Fatalf("Document.ConvertObjectModel: %v", err)
	}
	if !cr.Result.Valid {
		t.Errorf("Document.ConvertObjectModel: residual %v", cr.Residual())
	}
}
