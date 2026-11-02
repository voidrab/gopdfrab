package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// TestDisallowedAnnotFixerRemovesFromAnnots verifies that a disallowed
// annotation leaves its page's /Annots, as does an emptied dictionary, which
// a reader would take for an annotation with no subtype.
func TestDisallowedAnnotFixerRemovesFromAnnots(t *testing.T) {
	for _, tc := range []struct {
		name      string
		spec      string
		subtype   string
		wantKept  int
		wantLost  int
		wantClear bool
	}{
		{"Movie under 1b", "1b", "Movie", 1, 1, true},
		{"Sound under 2b", "2b", "Sound", 1, 1, true},
		{"Polygon under 2b", "2b", "Polygon", 2, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := a1bSpec
			if tc.spec == "2b" {
				spec = a2bSpec
			}
			text := kv("Type", nm("Annot"), "Subtype", nm("Text"))
			other := kv("Type", nm("Annot"), "Subtype", nm(tc.subtype))
			page := kv("Type", nm("Page"), "Annots", pdf.PDFArray{text, other, pdf.NewPDFDict()})
			trailer := trailerWith("Page", page)
			var lost []pdf.PDFError
			changed, err := disallowedAnnotFixer{spec: spec, lost: &lost}.Fix(&trailer, nil)
			if err != nil || !changed {
				t.Fatalf("Fix = %v, %v; want changed", changed, err)
			}
			if got := page.Entries.Get("Annots").(pdf.PDFArray); len(got) != tc.wantKept {
				t.Errorf("Annots has %d entries, want %d", len(got), tc.wantKept)
			}
			if len(lost) != tc.wantLost {
				t.Errorf("lost = %v, want %d records", lost, tc.wantLost)
			}
			if cleared := other.Entries.Len() == 0; cleared != tc.wantClear {
				t.Errorf("annotation cleared = %v, want %v", cleared, tc.wantClear)
			}
		})
	}
}

// TestDisallowedAnnotFixerClearsAnnotationReachedFirst verifies that an
// annotation reached through another dictionary before its page is cleared
// there, then dropped from /Annots as an empty dictionary and reported once.
func TestDisallowedAnnotFixerClearsAnnotationReachedFirst(t *testing.T) {
	movie := kv("Type", nm("Annot"), "Subtype", nm("Movie"))
	page := kv("Type", nm("Page"), "Annots", pdf.PDFArray{movie})
	trailer := kv("A", kv("Movie", movie), "Z", kv("Page", page))
	var lost []pdf.PDFError
	if _, err := (disallowedAnnotFixer{spec: a1bSpec, lost: &lost}).Fix(&trailer, nil); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if got := page.Entries.Get("Annots").(pdf.PDFArray); len(got) != 0 {
		t.Errorf("Annots = %v, want empty", got)
	}
	if len(lost) != 1 {
		t.Errorf("lost = %v, want one record", lost)
	}
}

func TestAnnotationFlagsFixerPart2(t *testing.T) {
	an := pdf.Checks.Annotation
	text := kv("Type", nm("Annot"), "Subtype", nm("Text"))
	popup := kv("Type", nm("Annot"), "Subtype", nm("Popup"))
	toggled := kv("Type", nm("Annot"), "Subtype", nm("Text"),
		"F", pdf.PDFInteger(verify.AnnotFlagPrint|verify.AnnotFlagToggleNoView))
	trailer := kv("Root", kv("Annots", pdf.PDFArray{text, popup, toggled}))

	changed, err := annotationFlagsFixer{}.Fix(&trailer, issuesOf(an.AnnotationFlagsMissing))
	if err != nil || !changed {
		t.Fatalf("Fix = %v, %v; want changed", changed, err)
	}
	if got := text.Entries.Get("F"); got != pdf.PDFInteger(verify.AnnotFlagPrint) {
		t.Errorf("text F = %v, want Print", got)
	}
	if got := popup.Entries.Get("F"); got != nil {
		t.Errorf("popup F = %v, want none", got)
	}
	if got := toggled.Entries.Get("F"); got != pdf.PDFInteger(verify.AnnotFlagPrint|verify.AnnotFlagToggleNoView) {
		t.Errorf("ToggleNoView cleared with only AnnotationFlagsMissing reported: F = %v", got)
	}

	if _, err := (annotationFlagsFixer{}).Fix(&trailer, issuesOf(an.ToggleNoViewFlagSet)); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if got := toggled.Entries.Get("F"); got != pdf.PDFInteger(verify.AnnotFlagPrint) {
		t.Errorf("toggled F = %v, want Print", got)
	}
}

// TestConstraintFixerSetsTrapNetFlags verifies that an annotation's F gets
// the bits the model requires set, and that no other flag word does.
func TestConstraintFixerSetsTrapNetFlags(t *testing.T) {
	for _, tc := range []struct {
		typeName, key string
		value, want   int
	}{
		// Print and ReadOnly, with Invisible cleared.
		{"AnnotTrapNetwork", "F", 1 | 4, 4 | 64},
		// Bit 17 makes a button a push button; it is never set.
		{"FieldBtnPush", "Ff", 0, 0},
	} {
		t.Run(tc.typeName, func(t *testing.T) {
			d := kv(tc.key, pdf.PDFInteger(tc.value))
			d.SetRef(pdf.PDFRef{ObjNum: 4})
			trailer := kv("D", d)
			ref := pdf.PDFRef{ObjNum: 4}
			pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{4: d}}
			issues := []pdf.PDFError{objModelIssue(pdf.Checks.ObjectModel.ConstraintViolated, &ref, tc.typeName, tc.key)}
			if _, _, err := (constraintFixer{}).fixTargeted(pass, issues); err != nil {
				t.Fatalf("fixTargeted: %v", err)
			}
			if got := d.Entries.Get(tc.key); got != pdf.PDFInteger(tc.want) {
				t.Errorf("%s = %v, want %d", tc.key, got, tc.want)
			}
		})
	}
}

func TestMissingRequiredKeyFixerTrapNetDefaults(t *testing.T) {
	for _, tc := range []struct {
		typeName, key string
		want          pdf.PDFValue
	}{
		{"AnnotTrapNetwork", "AS", nm("Off")},
		{"XObjectFormTrapNet", "PCM", nm("DeviceCMYK")},
	} {
		t.Run(tc.typeName, func(t *testing.T) {
			d := pdf.NewPDFDict()
			d.SetRef(pdf.PDFRef{ObjNum: 2})
			trailer := kv("D", d)
			ref := pdf.PDFRef{ObjNum: 2}
			pass := &fixPass{trailer: &trailer, objs: map[int]pdf.PDFValue{2: d}}
			issues := []pdf.PDFError{objModelIssue(pdf.Checks.ObjectModel.MissingRequiredKey, &ref, tc.typeName, tc.key)}
			changed, _, err := missingRequiredKeyFixer{}.fixTargeted(pass, issues)
			if err != nil || !changed {
				t.Fatalf("fixTargeted = %v, %v; want changed", changed, err)
			}
			if got := d.Entries.Get(tc.key); !pdf.EqualPDFValue(got, tc.want) {
				t.Errorf("%s = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

// TestAppearanceFixerPart2 verifies that an annotation with a zero-size Rect
// gets no appearance under PDF/A-2, and that a Btn widget whose normal
// appearance names no stream gets a new one at every level.
func TestAppearanceFixerPart2(t *testing.T) {
	zero := pdf.PDFArray{pdf.PDFInteger(5), pdf.PDFInteger(5), pdf.PDFInteger(5), pdf.PDFInteger(5)}
	for _, tc := range []struct {
		name  string
		annot func() pdf.PDFDict
		a1b   bool
		a2b   bool
	}{
		{"zero-size Rect", func() pdf.PDFDict {
			return kv("Type", nm("Annot"), "Subtype", nm("Square"), "Rect", zero)
		}, true, false},
		{"Btn states without streams", func() pdf.PDFDict {
			w := widgetWith("Btn", nil)
			w.Entries.Set("AP", kv("N", kv("On", pdf.NewPDFDict())))
			return w
		}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, level := range []struct {
				name string
				fix  appearanceFixer
				want bool
			}{
				{"1b", appearanceFixer{spec: a1bSpec}, tc.a1b},
				{"2b", appearanceFixer{spec: a2bSpec}, tc.a2b},
			} {
				annot := tc.annot()
				trailer := trailerWith("Annot", annot)
				changed, err := level.fix.Fix(&trailer, nil)
				if err != nil || changed != level.want {
					t.Fatalf("%s: Fix = %v, %v; want %v", level.name, changed, err, level.want)
				}
				if !level.want {
					continue
				}
				n := annot.Entries.Get("AP").(pdf.PDFDict).Entries.Get("N")
				if nd, ok := n.(pdf.PDFDict); tc.name != "zero-size Rect" && (!ok || !verify.ButtonAppearances(nd)) {
					t.Errorf("%s: AP N = %v, want a state subdictionary of streams", level.name, n)
				}
			}
		})
	}
}

// TestAnnotFixersTakeUntypedAnnotations verifies that the disallowed-subtype
// and appearance fixers repair an annotation with no Type entry, which the
// verifier treats as an annotation when it has Subtype and Rect.
func TestAnnotFixersTakeUntypedAnnotations(t *testing.T) {
	rect := pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(10), pdf.PDFInteger(10)}
	sound := kv("Subtype", nm("Sound"), "Rect", rect)
	page := kv("Type", nm("Page"), "Annots", pdf.PDFArray{sound})
	trailer := trailerWith("Page", page)
	if changed, err := (disallowedAnnotFixer{spec: a2bSpec}).Fix(&trailer, nil); err != nil || !changed {
		t.Fatalf("disallowedAnnotFixer.Fix = %v, %v; want changed", changed, err)
	}
	if got := page.Entries.Get("Annots").(pdf.PDFArray); len(got) != 0 {
		t.Errorf("Annots = %v, want the untyped Sound annotation removed", got)
	}

	widget := kv("Subtype", nm("Widget"), "FT", nm("Tx"), "Rect", rect)
	trailer = trailerWith("Widget", widget)
	if changed, err := (appearanceFixer{spec: a2bSpec}).Fix(&trailer, nil); err != nil || !changed {
		t.Fatalf("appearanceFixer.Fix = %v, %v; want changed", changed, err)
	}
	if _, ok := widget.Entries.Get("AP").(pdf.PDFDict); !ok {
		t.Error("untyped widget got no appearance")
	}
}

// TestDisallowedAnnotFixerKeepsCleanAnnots verifies that the Annots array is
// left as it is when nothing in it is removed.
func TestDisallowedAnnotFixerKeepsCleanAnnots(t *testing.T) {
	annots := pdf.PDFArray{kv("Type", nm("Annot"), "Subtype", nm("Text"))}
	page := kv("Type", nm("Page"), "Annots", annots)
	trailer := trailerWith("Page", page)
	if changed, err := (disallowedAnnotFixer{spec: a2bSpec}).Fix(&trailer, nil); err != nil || changed {
		t.Fatalf("Fix = %v, %v; want no change", changed, err)
	}
	if got := page.Entries.Get("Annots").(pdf.PDFArray); len(got) != 1 || &got[0] != &annots[0] {
		t.Errorf("Annots replaced: %v", got)
	}
}

// TestAnnotationFlagsFixerRealF verifies that a real F is read as its
// integer, so its other bits survive a repair.
func TestAnnotationFlagsFixerRealF(t *testing.T) {
	annot := kv("Type", nm("Annot"), "Subtype", nm("Text"),
		"F", pdf.PDFReal(verify.AnnotFlagPrint|verify.AnnotFlagToggleNoView|128))
	trailer := trailerWith("Annot", annot)
	an := pdf.Checks.Annotation
	if _, err := (annotationFlagsFixer{}).Fix(&trailer, issuesOf(an.AnnotationFlagsMissing, an.ToggleNoViewFlagSet)); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if got := annot.Entries.Get("F"); got != pdf.PDFInteger(verify.AnnotFlagPrint|128) {
		t.Errorf("F = %v, want Print and Locked", got)
	}
}

// TestConstraintFixerSetsDirectTrapNetFlags verifies that the whole-graph
// pass repairs a trap network annotation no finding can target.
func TestConstraintFixerSetsDirectTrapNetFlags(t *testing.T) {
	annot := kv("Type", nm("Annot"), "Subtype", nm("TrapNet"), "F", pdf.PDFInteger(1|4))
	trailer := trailerWith("Annot", annot)
	if changed, err := (constraintFixer{}).Fix(&trailer, nil); err != nil || !changed {
		t.Fatalf("Fix = %v, %v; want changed", changed, err)
	}
	if got := annot.Entries.Get("F"); got != pdf.PDFInteger(4|64) {
		t.Errorf("F = %v, want Print and ReadOnly", got)
	}
}
