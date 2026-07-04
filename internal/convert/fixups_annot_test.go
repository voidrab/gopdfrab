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

// TestAnnotColourFixerKeepsCoveredAndOtherShapes covers the branches
// TestAnnotColourFixer doesn't: a colour whose model IS covered by the
// document's OutputIntent (kept), /IC alongside /C, a gray (1-element) and
// an out-of-range-length array (skipped, not a colour model at all), and a
// non-Annot dict (ignored entirely).
func TestAnnotColourFixerKeepsCoveredAndOtherShapes(t *testing.T) {
	profile := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"N": pdf.PDFInteger(3)}}
	oi := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"S": pdf.PDFName{Value: "GTS_PDFA1"}, "DestOutputProfile": profile,
	}}
	annot := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Annot"},
		"C":    pdf.PDFArray{pdf.PDFReal(1), pdf.PDFReal(0), pdf.PDFReal(0)}, // rgb, covered
		"IC":   pdf.PDFArray{pdf.PDFReal(1), pdf.PDFReal(1)},                 // 2-element: not a model, skipped
	}}
	nonAnnot := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"C": pdf.PDFArray{pdf.PDFReal(1), pdf.PDFReal(0), pdf.PDFReal(0)},
	}}
	root := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Catalog"}, "OutputIntents": pdf.PDFArray{oi},
		"Annot0": annot, "NotAnAnnot": nonAnnot,
	}}
	trailer := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Root": root}}

	changed, err := annotColourFixer{}.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if changed {
		t.Error("changed = true, want false (rgb is covered, IC's length isn't a model)")
	}
	if _, ok := annot.Entries["C"]; !ok {
		t.Error("covered /C removed, want kept")
	}
	if _, ok := annot.Entries["IC"]; !ok {
		t.Error("out-of-range-length /IC removed, want kept (not a recognized model)")
	}
	if _, ok := nonAnnot.Entries["C"]; !ok {
		t.Error("non-Annot dict's /C was touched")
	}
}

// TestOutputIntentCoverage covers outputIntentCoverage directly: no Root, no
// OutputIntents, a non-PDFA1 intent (ignored), and RGB+CMYK both covered via
// two separate GTS_PDFA1 intents.
func TestOutputIntentCoverage(t *testing.T) {
	if has, rgb, cmyk := outputIntentCoverage(pdf.PDFDict{Entries: map[string]pdf.PDFValue{}}); has || rgb || cmyk {
		t.Errorf("outputIntentCoverage(no Root) = %v %v %v, want all false", has, rgb, cmyk)
	}

	noIntents := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: map[string]pdf.PDFValue{}},
	}}
	if has, rgb, cmyk := outputIntentCoverage(noIntents); has || rgb || cmyk {
		t.Errorf("outputIntentCoverage(no OutputIntents) = %v %v %v, want all false", has, rgb, cmyk)
	}

	otherS := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"S": pdf.PDFName{Value: "GTS_PDFX"}}}
	rgbOI := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"S":                 pdf.PDFName{Value: "GTS_PDFA1"},
		"DestOutputProfile": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"N": pdf.PDFInteger(3)}},
	}}
	cmykOI := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"S":                 pdf.PDFName{Value: "GTS_PDFA1"},
		"DestOutputProfile": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"N": pdf.PDFInteger(4)}},
	}}
	both := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"OutputIntents": pdf.PDFArray{otherS, rgbOI, cmykOI},
		}},
	}}
	has, rgb, cmyk := outputIntentCoverage(both)
	if !has || !rgb || !cmyk {
		t.Errorf("outputIntentCoverage(rgb+cmyk intents) = %v %v %v, want all true", has, rgb, cmyk)
	}
}
