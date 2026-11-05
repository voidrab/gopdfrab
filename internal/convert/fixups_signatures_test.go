package convert

import (
	"bytes"
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

// TestConvertDropsSignatures verifies that every level removes the signatures
// and reports each one once, because the field's V and Perms hold the same
// signature. The record carries a clause only at a level that has the check.
func TestConvertDropsSignatures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		p      *pdf.Profile
		clause string
	}{
		{"pdfa1b", pdf.PDFA1B, ""},
		{"legacy1b", pdf.Legacy1B, ""},
		{"pdf", pdf.PDF, ""},
		{"pdfa2b", pdf.PDFA2B, "6.4.3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cr, err := ConvertBytes(signedFieldDoc(), tc.p, Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer cr.Close()
			var lost []pdf.PDFError
			for _, e := range cr.LostObjects {
				if e.Check() == pdf.Checks.Form.SignatureByteRange {
					lost = append(lost, e)
				}
			}
			if len(lost) != 1 {
				t.Fatalf("lost %d signature records, want 1: %v", len(lost), cr.LostObjects)
			}
			if clause, _ := lost[0].Clause(); clause != tc.clause {
				t.Errorf("record clause = %q, want %q", clause, tc.clause)
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
			if _, hasPerms := root.(pdf.PDFDict).Entries.Lookup("Perms"); hasPerms {
				t.Error("output keeps Perms")
			}
			if bytes.Contains(out, []byte("/ByteRange")) {
				t.Error("output keeps a signature dictionary")
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
	dropSignatures(trailer, &lost)
	if len(lost) != 1 || perms.Entries.Get("UR3") != nil || root.Entries.Get("Perms") == nil {
		t.Errorf("lost %v, Perms %v: want UR3 dropped and Perms kept", lost, root.Entries.Get("Perms"))
	}
}
