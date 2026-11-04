package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
)

// signedFieldDoc is a document with a signed signature field, a DocMDP
// signature in the catalog's Perms, and an unsigned signature field.
func signedFieldDoc() []byte {
	b := pdfgen.NewBuilder("%PDF-1.7\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [4 0 R 6 0 R] /SigFlags 3 >> /Perms << /DocMDP 5 0 R >> >>")
	b.Obj(2, "<< /Type /Pages /Count 1 /Kids [3 0 R] >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] >>")
	b.Obj(4, "<< /FT /Sig /T (Signed) /V 5 0 R >>")
	b.Obj(5, "<< /Type /Sig /Filter /Adobe.PPKLite /ByteRange [0 1 2 3] /Contents <00> >>")
	b.Obj(6, "<< /FT /Sig /T (Unsigned) >>")
	return b.FinishClassic("<< /Size 7 /Root 1 0 R >>")
}

func TestConvertDropsSignatures(t *testing.T) {
	for _, tc := range []struct {
		p    *pdf.Profile
		lost int
	}{
		{pdf.PDFA1B, 0},
		{pdf.PDFA2B, 2},
	} {
		t.Run(string(tc.p.Level), func(t *testing.T) {
			cr, err := ConvertBytes(signedFieldDoc(), tc.p, Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer cr.Close()
			var lost int
			for _, e := range cr.LostObjects {
				if e.Check() == pdf.Checks.Form.SignatureByteRange {
					lost++
				}
			}
			if lost != tc.lost {
				t.Errorf("lost %d signatures, want %d: %v", lost, tc.lost, cr.LostObjects)
			}
			out, err := cr.Output()
			if err != nil {
				t.Fatal(err)
			}
			doc, err := pdf.OpenBytes(out)
			if err != nil {
				t.Fatal(err)
			}
			defer doc.Close()
			root, _ := doc.ResolveGraphByPath([]string{"Root"})
			_, hasPerms := root.(pdf.PDFDict).Entries.Lookup("Perms")
			if signed := tc.lost == 0; hasPerms != signed {
				t.Errorf("output has Perms = %v, want %v", hasPerms, signed)
			}
		})
	}
}

func TestDropSignaturesKeepsOtherPerms(t *testing.T) {
	sig := pdf.NewPDFDict()
	perms := pdf.NewPDFDict()
	perms.Entries.Set("UR3", sig)
	perms.Entries.Set("Other", pdf.PDFInteger(1))
	root := pdf.NewPDFDict()
	root.Entries.Set("Perms", perms)
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", root)

	var lost []pdf.PDFError
	dropSignatures(trailer, pdf.PDFA2B, &lost)
	if len(lost) != 1 || perms.Entries.Get("UR3") != nil || root.Entries.Get("Perms") == nil {
		t.Errorf("lost %v, Perms %v: want UR3 dropped and Perms kept", lost, root.Entries.Get("Perms"))
	}
}
