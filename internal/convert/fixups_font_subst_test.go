package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// TestFontSubstitutionFixerHandlesStandardType1 verifies that a standard
// Type1 font (like Helvetica in AcroForm/DR) without a FontDescriptor gets
// a Liberation substitute embedded -- previously the fixer returned false
// because FontDescriptor was absent.
func TestFontSubstitutionFixerHandlesStandardType1(t *testing.T) {
	font := pdf.NewPDFDict()
	font.Entries["Type"] = pdf.PDFName{Value: "Font"}
	font.Entries["Subtype"] = pdf.PDFName{Value: "Type1"}
	font.Entries["BaseFont"] = pdf.PDFName{Value: "Helvetica"}
	font.Entries["Encoding"] = pdf.PDFName{Value: "WinAnsiEncoding"}
	// No FontDescriptor, no FirstChar/Widths -- like a standard Type1 in AcroForm/DR.

	dr := pdf.NewPDFDict()
	dr.Entries["Font"] = pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Helv": font}}

	acroForm := pdf.NewPDFDict()
	acroForm.Entries["DR"] = dr

	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = pdf.PDFDict{Entries: map[string]pdf.PDFValue{"AcroForm": acroForm}}

	fixer := fontSubstitutionFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true (Helvetica has no embedded program)")
	}

	desc, ok := font.Entries["FontDescriptor"].(pdf.PDFDict)
	if !ok {
		t.Fatalf("FontDescriptor not set after substitution")
	}
	if !verify.HasEmbeddedProgram(desc, "FontFile", "FontFile2", "FontFile3") {
		t.Errorf("FontDescriptor still has no embedded program after substitution")
	}
}

// TestFontSubstitutionFixerIdempotentAfterStandardType1 confirms that a
// second pass is a no-op once the font is already substituted.
func TestFontSubstitutionFixerIdempotentAfterStandardType1(t *testing.T) {
	font := pdf.NewPDFDict()
	font.Entries["Type"] = pdf.PDFName{Value: "Font"}
	font.Entries["Subtype"] = pdf.PDFName{Value: "Type1"}
	font.Entries["BaseFont"] = pdf.PDFName{Value: "Helvetica"}
	font.Entries["Encoding"] = pdf.PDFName{Value: "WinAnsiEncoding"}

	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Font": font}}

	fixer := fontSubstitutionFixer{}
	if _, err := fixer.Fix(&trailer, nil); err != nil {
		t.Fatalf("first Fix: %v", err)
	}

	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("second Fix: %v", err)
	}
	if changed {
		t.Errorf("changed = true on second pass, want false (fixer must be idempotent)")
	}
}
