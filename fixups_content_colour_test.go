package pdfrab

import (
	"os"
	"testing"
)

// TestDeviceColourFixerClearsContentStreamViolation exercises deviceColourFixer
// end-to-end (Convert, not just Fix) on a real fixture exhibiting
// DeviceColourContentStream, confirming the injected Default* colour space
// survives the full write+reverify round trip.
func TestDeviceColourFixerClearsContentStreamViolation(t *testing.T) {
	path := "test documents/veraPDF/PDF_A-1b/6.2 Graphics/6.2.3.3 Uncalibrated color space/veraPDF test suite 6-2-3-3-t01-fail-i.pdf"
	if _, err := os.Stat(path); err != nil {
		t.Skip("corpus fixture not present")
	}

	cr, err := Convert(path)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, iss := range cr.Residual() {
		if iss.check == Checks.Colour.DeviceColourContentStream {
			t.Errorf("DeviceColourContentStream still present after conversion: %v", iss)
		}
	}
}

// TestPageDeviceColourModelsFindsContentAndDictUsage checks
// pageDeviceColourModels against a synthetic page mixing a content-stream
// "k" operator (CMYK) with an Image XObject whose own /ColorSpace is
// DeviceRGB, confirming both detection paths (content scan and resource-dict
// scan) feed into the same result set.
func TestPageDeviceColourModelsFindsContentAndDictUsage(t *testing.T) {
	content, err := writeContentStream([]contentOp{
		{Op: "k", Operands: []PDFValue{PDFReal(0), PDFReal(0), PDFReal(0), PDFReal(1)}},
	})
	if err != nil {
		t.Fatalf("writeContentStream: %v", err)
	}

	page := NewPDFDict()
	page.Entries["Type"] = PDFName{Value: "Page"}
	contentsDict := NewPDFDict()
	contentsDict.HasStream = true
	contentsDict.RawStream = content
	page.Entries["Contents"] = contentsDict

	image := NewPDFDict()
	image.Entries["Subtype"] = PDFName{Value: "Image"}
	image.Entries["ColorSpace"] = PDFName{Value: "DeviceRGB"}

	xobjects := NewPDFDict()
	xobjects.Entries["Im0"] = image
	resources := NewPDFDict()
	resources.Entries["XObject"] = xobjects

	used := pageDeviceColourModels(page, resources)
	if !used["cmyk"] {
		t.Errorf("expected cmyk usage from content-stream k operator, got %v", used)
	}
	if !used["rgb"] {
		t.Errorf("expected rgb usage from Image XObject ColorSpace, got %v", used)
	}
}
