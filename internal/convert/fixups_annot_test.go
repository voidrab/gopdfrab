package convert

import (
	"slices"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// trailerWith wraps v under Root so the dict visitors reach it.
func trailerWith(key string, v pdf.PDFValue) pdf.PDFDict {
	return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Type": pdf.PDFName{Value: "Catalog"}, key: v,
		})},
	})}
}

func TestDisallowedAnnotFixer(t *testing.T) {
	annot := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Annot"}, "Subtype": pdf.PDFName{Value: "Movie"},
		"Rect": pdf.PDFArray{},
	})}
	annot.SetRef(pdf.PDFRef{ObjNum: 5})
	trailer := trailerWith("Annot0", annot)
	var lost []pdf.PDFError
	changed, err := disallowedAnnotFixer{spec: a1bSpec, lost: &lost}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("disallowedAnnotFixer.Fix = %v, %v", changed, err)
	}
	if _, ok := annot.Entries.Lookup("Subtype"); ok {
		t.Error("disallowed annotation not cleared")
	}
	if len(lost) != 1 {
		t.Fatalf("lost = %v, want one record for the cleared annotation", lost)
	}
	if ref, ok := lost[0].ObjectRef(); !ok || ref.ObjNum != 5 ||
		lost[0].Check() != pdf.Checks.Annotation.DisallowedSubtype ||
		!slices.Equal(lost[0].Messages(), []string{"Movie annotation removed"}) {
		t.Errorf("lost[0] = %v, want object 5's Movie annotation under DisallowedSubtype", lost[0])
	}
}

func TestAnnotColourFixer(t *testing.T) {
	annot := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Annot"},
		"C":    pdf.PDFArray{pdf.PDFReal(1), pdf.PDFReal(0), pdf.PDFReal(0)}, // rgb
	})}
	// No output intent in the trailer, so an RGB annotation colour is
	// uncovered.
	trailer := trailerWith("Annot0", annot)
	changed, err := annotColourFixer{spec: a1bSpec}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("annotColourFixer.Fix = %v, %v", changed, err)
	}
	if _, ok := annot.Entries.Lookup("C"); ok {
		t.Error("uncovered annotation colour /C not removed")
	}
}

// TestAnnotColourFixerKeepsCoveredAndOtherShapes covers the branches
// TestAnnotColourFixer doesn't: a colour whose model IS covered by the
// document's OutputIntent (kept), /IC alongside /C, a gray (1-element) and
// an out-of-range-length array (skipped, not a colour model at all), and a
// non-Annot dict (ignored entirely).
func TestAnnotColourFixerKeepsCoveredAndOtherShapes(t *testing.T) {
	profile := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"N": pdf.PDFInteger(3)})}
	oi := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"S": pdf.PDFName{Value: "GTS_PDFA1"}, "DestOutputProfile": profile,
	})}
	annot := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Annot"},
		"C":    pdf.PDFArray{pdf.PDFReal(1), pdf.PDFReal(0), pdf.PDFReal(0)}, // rgb, covered
		"IC":   pdf.PDFArray{pdf.PDFReal(1), pdf.PDFReal(1)},                 // 2-element: not a model, skipped
	})}
	nonAnnot := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"C": pdf.PDFArray{pdf.PDFReal(1), pdf.PDFReal(0), pdf.PDFReal(0)},
	})}
	root := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Catalog"}, "OutputIntents": pdf.PDFArray{oi},
		"Annot0": annot, "NotAnAnnot": nonAnnot,
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Root": root})}

	changed, err := annotColourFixer{spec: a1bSpec}.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if changed {
		t.Error("changed = true, want false (rgb is covered, IC's length isn't a model)")
	}
	if _, ok := annot.Entries.Lookup("C"); !ok {
		t.Error("covered /C removed, want kept")
	}
	if _, ok := annot.Entries.Lookup("IC"); !ok {
		t.Error("out-of-range-length /IC removed, want kept (not a recognized model)")
	}
	if _, ok := nonAnnot.Entries.Lookup("C"); !ok {
		t.Error("non-Annot dict's /C was touched")
	}
}

// TestOutputIntentCoverage covers outputIntentCoverage directly: no Root, no
// OutputIntents, a non-PDFA1 intent (ignored), and RGB+CMYK both covered via
// two separate GTS_PDFA1 intents.
func TestOutputIntentCoverage(t *testing.T) {
	if has, rgb, cmyk := outputIntentCoverage(pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}, "GTS_PDFA1"); has || rgb || cmyk {
		t.Errorf("outputIntentCoverage(no Root) = %v %v %v, want all false", has, rgb, cmyk)
	}

	noIntents := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})},
	})}
	if has, rgb, cmyk := outputIntentCoverage(noIntents, "GTS_PDFA1"); has || rgb || cmyk {
		t.Errorf("outputIntentCoverage(no OutputIntents) = %v %v %v, want all false", has, rgb, cmyk)
	}

	otherS := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"S": pdf.PDFName{Value: "GTS_PDFX"}})}
	rgbOI := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"S":                 pdf.PDFName{Value: "GTS_PDFA1"},
		"DestOutputProfile": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"N": pdf.PDFInteger(3)})},
	})}
	cmykOI := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"S":                 pdf.PDFName{Value: "GTS_PDFA1"},
		"DestOutputProfile": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"N": pdf.PDFInteger(4)})},
	})}
	both := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"OutputIntents": pdf.PDFArray{otherS, rgbOI, cmykOI},
		})},
	})}
	has, rgb, cmyk := outputIntentCoverage(both, "GTS_PDFA1")
	if !has || !rgb || !cmyk {
		t.Errorf("outputIntentCoverage(rgb+cmyk intents) = %v %v %v, want all true", has, rgb, cmyk)
	}
}

// TestAnnotColourFixerCmykAndGrayModels covers the 1- and 4-element colour
// arrays: gray is allowed by any OutputIntent, cmyk only by a 4-component
// profile. A non-dict OutputIntents entry is skipped.
func TestAnnotColourFixerCmykAndGrayModels(t *testing.T) {
	cmykOI := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"S":                 pdf.PDFName{Value: "GTS_PDFA1"},
		"DestOutputProfile": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"N": pdf.PDFInteger(4)})},
	})}
	mk := func(c pdf.PDFArray) pdf.PDFDict {
		return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Type": pdf.PDFName{Value: "Annot"}, "C": c,
		})}
	}
	cmyk := mk(pdf.PDFArray{pdf.PDFReal(0), pdf.PDFReal(0), pdf.PDFReal(0), pdf.PDFReal(1)})
	gray := mk(pdf.PDFArray{pdf.PDFReal(0.5)})
	rgb := mk(pdf.PDFArray{pdf.PDFReal(1), pdf.PDFReal(0), pdf.PDFReal(0)})
	root := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"OutputIntents": pdf.PDFArray{pdf.PDFInteger(1), cmykOI},
		"A":             cmyk, "B": gray, "R": rgb,
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Root": root})}
	changed, err := annotColourFixer{spec: a1bSpec}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("Fix = %v, %v; want true (rgb uncovered)", changed, err)
	}
	if _, ok := cmyk.Entries.Lookup("C"); !ok {
		t.Error("covered cmyk /C removed")
	}
	if _, ok := gray.Entries.Lookup("C"); !ok {
		t.Error("gray /C removed despite an OutputIntent")
	}
	if _, ok := rgb.Entries.Lookup("C"); ok {
		t.Error("uncovered rgb /C kept")
	}
}

// TestDisallowedAnnotFixerNamesAttachedFile checks that clearing a file
// attachment reports the embedded file that goes with it.
func TestDisallowedAnnotFixerNamesAttachedFile(t *testing.T) {
	fs := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"F":  pdf.PDFString{Value: "notes.txt"},
		"EF": pdf.PDFDict{Entries: pdf.NewDict()},
	})}
	annot := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Annot"}, "Subtype": pdf.PDFName{Value: "FileAttachment"}, "FS": fs,
	})}
	trailer := trailerWith("Annot0", annot)
	var lost []pdf.PDFError
	if _, err := (disallowedAnnotFixer{spec: a1bSpec, lost: &lost}).Fix(&trailer, nil); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	want := []string{`FileAttachment annotation removed with its embedded file "notes.txt"`}
	if len(lost) != 1 || !slices.Equal(lost[0].Messages(), want) {
		t.Errorf("lost = %v, want %q", lost, want)
	}
	if _, ok := lost[0].ObjectRef(); ok {
		t.Error("a direct annotation was reported with an object reference")
	}
}

// TestDisallowedAnnotFixerUntypedAnnotation checks the report for an
// annotation that has no /Subtype to name it by.
func TestDisallowedAnnotFixerUntypedAnnotation(t *testing.T) {
	annot := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Annot"}})}
	trailer := trailerWith("Annot0", annot)
	var lost []pdf.PDFError
	if _, err := (disallowedAnnotFixer{spec: a1bSpec, lost: &lost}).Fix(&trailer, nil); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if len(lost) != 1 || !slices.Equal(lost[0].Messages(), []string{"annotation removed"}) {
		t.Errorf("lost = %v, want one \"annotation removed\" record", lost)
	}
}
