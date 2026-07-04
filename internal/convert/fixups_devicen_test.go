package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// oversizedDeviceN builds a [/DeviceN [9 names] /DeviceRGB tint] colour space
// array, which exceeds the 8-colorant limit the fixer targets.
func oversizedDeviceN() pdf.PDFArray {
	names := make(pdf.PDFArray, 9)
	for i := range names {
		names[i] = pdf.PDFName{Value: string(rune('a' + i))}
	}
	var domain pdf.PDFArray
	for range 9 {
		domain = append(domain, pdf.PDFReal(0), pdf.PDFReal(1))
	}
	tint := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"FunctionType": pdf.PDFInteger(4),
			"Domain":       domain,
			"Range":        pdf.PDFArray{pdf.PDFReal(0), pdf.PDFReal(1), pdf.PDFReal(0), pdf.PDFReal(1), pdf.PDFReal(0), pdf.PDFReal(1)},
		},
		HasStream: true,
		RawStream: []byte("{ pop pop pop pop pop pop pop pop pop 0 0 0 }"),
	}
	return pdf.PDFArray{pdf.PDFName{Value: "DeviceN"}, names, pdf.PDFName{Value: "DeviceRGB"}, tint}
}

// TestDeviceNColorantsFixer drives the oversized-DeviceN remediation across
// page content (cs/scn), a recursed Form XObject, and an image dict.
func TestDeviceNColorantsFixer(t *testing.T) {
	dn := oversizedDeviceN()

	form := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Subtype":   pdf.PDFName{Value: "Form"},
			"Resources": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"ColorSpace": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"DN": dn}}}},
		},
		HasStream: true,
		RawStream: []byte("/DN cs 0.1 0.2 0.3 0.4 0.5 0.6 0.7 0.8 0.9 scn"),
	}
	image := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Subtype": pdf.PDFName{Value: "Image"},
			"Width":   pdf.PDFInteger(1), "Height": pdf.PDFInteger(1),
			"BitsPerComponent": pdf.PDFInteger(8), "ColorSpace": dn,
		},
		HasStream: true,
		RawStream: []byte{10, 20, 30, 40, 50, 60, 70, 80, 90},
	}
	resources := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"ColorSpace": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"DN": dn}},
		"XObject":    pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Fm1": form, "Im1": image}},
	}}
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type":      pdf.PDFName{Value: "Page"},
		"Resources": resources,
		"Contents": pdf.PDFDict{Entries: map[string]pdf.PDFValue{}, HasStream: true, RawStream: []byte(
			"/DN cs 0.1 0.2 0.3 0.4 0.5 0.6 0.7 0.8 0.9 scn /DN CS 0.1 0.2 0.3 0.4 0.5 0.6 0.7 0.8 0.9 SCN /Fm1 Do")},
	}}
	trailer := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Pages": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Kids": pdf.PDFArray{page}}},
		}},
	}}

	changed, err := deviceNColorantsFixer{}.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("deviceNColorantsFixer.Fix: %v", err)
	}
	if !changed {
		t.Error("expected the oversized-DeviceN fixer to change the graph")
	}

	// The page's ColorSpace entry for the dead DeviceN space should be pruned.
	if cs, ok := resources.Entries["ColorSpace"].(pdf.PDFDict); ok {
		if _, still := cs.Entries["DN"]; still {
			t.Error("dead DeviceN ColorSpace entry not pruned")
		}
	}
}

// TestDeviceNColorantsFixerArrayContents drives the PDFArray branch of
// rewriteDeviceNPageContents -- a page whose /Contents is an array of
// multiple content streams, only one of which uses the oversized space.
func TestDeviceNColorantsFixerArrayContents(t *testing.T) {
	dn := oversizedDeviceN()
	resources := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"ColorSpace": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"DN": dn}},
	}}
	unrelated := pdf.PDFDict{Entries: map[string]pdf.PDFValue{}, HasStream: true, RawStream: []byte("1 0 0 rg 0 0 10 10 re f")}
	offending := pdf.PDFDict{Entries: map[string]pdf.PDFValue{}, HasStream: true, RawStream: []byte(
		"/DN cs 0.1 0.2 0.3 0.4 0.5 0.6 0.7 0.8 0.9 scn")}
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type":      pdf.PDFName{Value: "Page"},
		"Resources": resources,
		"Contents":  pdf.PDFArray{unrelated, offending},
	}}
	trailer := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Pages": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Kids": pdf.PDFArray{page}}},
		}},
	}}

	changed, err := deviceNColorantsFixer{}.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("deviceNColorantsFixer.Fix: %v", err)
	}
	if !changed {
		t.Fatalf("expected the array-Contents page to be rewritten")
	}

	contents := page.Entries["Contents"].(pdf.PDFArray)
	decodedOffending, err := pdf.DecodeStream(contents[1].(pdf.PDFDict))
	if err != nil {
		t.Fatalf("DecodeStream(offending): %v", err)
	}
	if string(decodedOffending) == string(offending.RawStream) {
		t.Errorf("offending stream (array[1]) was not rewritten: %q", decodedOffending)
	}
	decodedUnrelated, err := pdf.DecodeStream(contents[0].(pdf.PDFDict))
	if err != nil {
		t.Fatalf("DecodeStream(unrelated): %v", err)
	}
	if string(decodedUnrelated) != string(unrelated.RawStream) {
		t.Errorf("unrelated stream (array[0]) was rewritten unexpectedly: %q", decodedUnrelated)
	}
}

// TestRecurseDeviceNFormEdgeCases covers every early-return branch: no
// operands, a non-name operand, missing /XObject resources, an unknown/non-
// Form/streamless target, an already-visited Form, the /Resources
// inheritance fallback, and a Form whose content needs no rewriting at all.
func TestRecurseDeviceNFormEdgeCases(t *testing.T) {
	resourcesNoXObject := pdf.PDFDict{Entries: map[string]pdf.PDFValue{}}
	notForm := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Image"}}, HasStream: true}
	noStream := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Form"}}}
	plainForm := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Form"}}, HasStream: true, RawStream: []byte("1 0 0 rg 0 0 1 1 re f")}
	resources := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"XObject": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"NotForm": notForm, "NoStream": noStream, "Fm1": plainForm,
		}},
	}}

	if _, ok := recurseDeviceNForm(nil, resources, map[uintptr]bool{}); ok {
		t.Error("recurseDeviceNForm(no operands) ok = true, want false")
	}
	if _, ok := recurseDeviceNForm([]pdf.PDFValue{pdf.PDFInteger(1)}, resources, map[uintptr]bool{}); ok {
		t.Error("recurseDeviceNForm(non-name operand) ok = true, want false")
	}
	if _, ok := recurseDeviceNForm([]pdf.PDFValue{pdf.PDFName{Value: "Fm1"}}, resourcesNoXObject, map[uintptr]bool{}); ok {
		t.Error("recurseDeviceNForm(no XObject resources) ok = true, want false")
	}
	if _, ok := recurseDeviceNForm([]pdf.PDFValue{pdf.PDFName{Value: "Missing"}}, resources, map[uintptr]bool{}); ok {
		t.Error("recurseDeviceNForm(unknown target) ok = true, want false")
	}
	if _, ok := recurseDeviceNForm([]pdf.PDFValue{pdf.PDFName{Value: "NotForm"}}, resources, map[uintptr]bool{}); ok {
		t.Error("recurseDeviceNForm(non-Form target) ok = true, want false")
	}
	if _, ok := recurseDeviceNForm([]pdf.PDFValue{pdf.PDFName{Value: "NoStream"}}, resources, map[uintptr]bool{}); ok {
		t.Error("recurseDeviceNForm(streamless Form) ok = true, want false")
	}
	visited := map[uintptr]bool{pdf.ValuePointer(plainForm.Entries): true}
	if _, ok := recurseDeviceNForm([]pdf.PDFValue{pdf.PDFName{Value: "Fm1"}}, resources, visited); ok {
		t.Error("recurseDeviceNForm(already-visited Form) ok = true, want false")
	}
	// plainForm has no own /Resources and no oversized-DeviceN usage: the
	// inheritance fallback runs, but rewriteDeviceNStream reports no change.
	if _, ok := recurseDeviceNForm([]pdf.PDFValue{pdf.PDFName{Value: "Fm1"}}, resources, map[uintptr]bool{}); ok {
		t.Error("recurseDeviceNForm(Form needing no rewrite) ok = true, want false")
	}
}

func TestIsOversizedDeviceN(t *testing.T) {
	if !isOversizedDeviceN(oversizedDeviceN()) {
		t.Error("9-colorant DeviceN should be oversized")
	}
	small := pdf.PDFArray{pdf.PDFName{Value: "DeviceN"}, pdf.PDFArray{pdf.PDFName{Value: "a"}}, pdf.PDFName{Value: "DeviceRGB"}}
	if isOversizedDeviceN(small) {
		t.Error("1-colorant DeviceN should not be oversized")
	}
	if isOversizedDeviceN(pdf.PDFName{Value: "DeviceRGB"}) {
		t.Error("non-array should not be oversized")
	}
}
