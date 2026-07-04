package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// trailerWith wraps v under Root so the dict visitors reach it.
func trailerWith(key string, v pdf.PDFValue) pdf.PDFDict {
	return pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Type": pdf.PDFName{Value: "Catalog"}, key: v,
		}},
	}}
}

func TestDisallowedAnnotFixer(t *testing.T) {
	annot := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Annot"}, "Subtype": pdf.PDFName{Value: "Movie"},
		"Rect": pdf.PDFArray{},
	}}
	trailer := trailerWith("Annot0", annot)
	changed, err := disallowedAnnotFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("disallowedAnnotFixer.Fix = %v, %v", changed, err)
	}
	if _, ok := annot.Entries["Subtype"]; ok {
		t.Error("disallowed annotation not cleared")
	}
}

func TestAnnotColourFixer(t *testing.T) {
	annot := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Annot"},
		"C":    pdf.PDFArray{pdf.PDFReal(1), pdf.PDFReal(0), pdf.PDFReal(0)}, // rgb
	}}
	// No output intent in the trailer, so an RGB annotation colour is uncovered.
	trailer := trailerWith("Annot0", annot)
	changed, err := annotColourFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("annotColourFixer.Fix = %v, %v", changed, err)
	}
	if _, ok := annot.Entries["C"]; ok {
		t.Error("uncovered annotation colour /C not removed")
	}
}
