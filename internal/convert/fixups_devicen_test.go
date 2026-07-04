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
