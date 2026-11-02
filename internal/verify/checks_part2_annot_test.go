package verify

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
)

// annotWith returns an annotation dictionary of the given subtype with a
// printable F, a 10 by 10 Rect and an appearance stream. Each pair in pairs
// sets a key to its value, or deletes the key when the value is nil.
func annotWith(subtype string, pairs ...any) pdf.PDFDict {
	n := pdf.NewPDFDict()
	n.SetStream([]byte("q Q"))
	d := kv("Type", name("Annot"), "Subtype", name(subtype), "F", pdf.PDFInteger(AnnotFlagPrint),
		"Rect", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(10), pdf.PDFInteger(10)},
		"AP", kv("N", n))
	for i := 0; i < len(pairs); i += 2 {
		if pairs[i+1] == nil {
			d.Entries.Del(pairs[i].(string))
			continue
		}
		d.Entries.Set(pairs[i].(string), pairs[i+1].(pdf.PDFValue))
	}
	return d
}

// reportedChecksOf validates the annotation under spec and returns the
// checks reported.
func reportedChecksOf(d pdf.PDFDict, spec *pdfa.Spec) map[pdf.Check]bool {
	ctx := &ValidationContext{Spec: spec}
	validateAnnotation(d, ctx)
	got := map[pdf.Check]bool{}
	for _, e := range ctx.errs {
		got[e.Check()] = true
	}
	return got
}

func TestPart2AnnotationFlags(t *testing.T) {
	an := pdf.Checks.Annotation
	zeroRect := pdf.PDFArray{pdf.PDFInteger(5), pdf.PDFInteger(5), pdf.PDFInteger(5), pdf.PDFInteger(5)}
	for _, tc := range []struct {
		name  string
		annot pdf.PDFDict
		spec  *pdfa.Spec
		want  []pdf.Check
	}{
		{"conforming", annotWith("Text"), a2bSpec, nil},
		{"no F", annotWith("Text", "F", nil), a2bSpec, []pdf.Check{an.AnnotationFlagsMissing}},
		{"no F on a popup", annotWith("Popup", "F", nil), a2bSpec, nil},
		{"no F under Part 1", annotWith("Text", "F", nil), a1bSpec, []pdf.Check{an.PrintFlagNotSet}},
		{"ToggleNoView", annotWith("Text", "F", pdf.PDFInteger(AnnotFlagPrint|AnnotFlagToggleNoView)), a2bSpec,
			[]pdf.Check{an.ToggleNoViewFlagSet}},
		{"ToggleNoView under Part 1", annotWith("Text", "F", pdf.PDFInteger(AnnotFlagPrint|AnnotFlagToggleNoView)), a1bSpec, nil},
		{"disallowed subtype checked on", annotWith("Sound", "F", nil), a2bSpec,
			[]pdf.Check{an.DisallowedSubtype, an.AnnotationFlagsMissing}},
		{"disallowed subtype under Part 1", annotWith("Sound", "F", nil), a1bSpec, []pdf.Check{an.DisallowedSubtype}},
		{"no AP, zero-size Rect", annotWith("Text", "AP", nil, "Rect", zeroRect), a2bSpec, nil},
		{"no AP, zero-size Rect under Part 1", annotWith("Text", "AP", nil, "Rect", zeroRect), a1bSpec,
			[]pdf.Check{an.MissingAppearance}},
		{"no AP on a widget", annotWith("Widget", "AP", nil, "FT", name("Tx")), a2bSpec, []pdf.Check{an.MissingAppearance}},
		{"no AP on a widget under Part 1", annotWith("Widget", "AP", nil, "FT", name("Tx")), a1bSpec,
			[]pdf.Check{pdf.Checks.Form.WidgetMissingAppearance}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := reportedChecksOf(tc.annot, tc.spec)
			for _, c := range tc.want {
				if !got[c] {
					t.Errorf("%s not reported, got %v", c.Name(), got)
				}
			}
			if len(got) != len(tc.want) {
				t.Errorf("reported %v, want exactly %v", got, tc.want)
			}
		})
	}
}

func TestZeroSizeRect(t *testing.T) {
	n := func(v ...pdf.PDFValue) pdf.PDFArray { return v }
	for _, tc := range []struct {
		name string
		rect pdf.PDFValue
		want bool
	}{
		{"equal corners", n(pdf.PDFInteger(3), pdf.PDFReal(4), pdf.PDFInteger(3), pdf.PDFReal(4)), true},
		{"no height only", n(pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(3), pdf.PDFInteger(0)), false},
		{"short", n(pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(0)), false},
		{"not numbers", n(pdf.PDFInteger(0), name("x"), pdf.PDFInteger(0), pdf.PDFInteger(0)), false},
		{"no Rect", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := pdf.NewPDFDict()
			if tc.rect != nil {
				d.Entries.Set("Rect", tc.rect)
			}
			if got := ZeroSizeRect(d); got != tc.want {
				t.Errorf("ZeroSizeRect = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestButtonAppearances covers veraPDF's containsAppearances: a Btn widget's
// normal appearance must be a subdictionary naming at least one appearance
// stream, at every level.
func TestButtonAppearances(t *testing.T) {
	stream := pdf.NewPDFDict()
	stream.SetStream([]byte("q Q"))
	for _, tc := range []struct {
		name string
		n    pdf.PDFDict
		want bool
	}{
		{"state streams", kv("Off", pdf.NewPDFDict(), "On", stream), true},
		{"empty subdictionary", pdf.NewPDFDict(), false},
		{"states that are no streams", kv("On", pdf.NewPDFDict()), false},
		{"a stream", stream, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ButtonAppearances(tc.n); got != tc.want {
				t.Errorf("ButtonAppearances = %v, want %v", got, tc.want)
			}
			for _, spec := range []*pdfa.Spec{a1bSpec, a2bSpec} {
				got := reportedChecksOf(annotWith("Widget", "FT", name("Btn"), "AP", kv("N", tc.n)), spec)
				if reported := got[pdf.Checks.Annotation.AppearanceNNotStream]; reported == tc.want {
					t.Errorf("%s: AppearanceNNotStream reported = %v, want %v", spec.Level, reported, !tc.want)
				}
			}
		})
	}
}
