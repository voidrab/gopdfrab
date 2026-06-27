package convert

import (
	"os"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"

	"github.com/voidrab/gopdfrab/internal/writer"
)

// TestDeviceColourFixerClearsContentStreamViolation exercises deviceColourFixer
// end-to-end (Convert, not just Fix) on a real fixture exhibiting
// DeviceColourContentStream, confirming the injected Default* colour space
// survives the full write+reverify round trip.
func TestDeviceColourFixerClearsContentStreamViolation(t *testing.T) {
	path := "../../test documents/veraPDF/PDF_A-1b/6.2 Graphics/6.2.3.3 Uncalibrated color space/veraPDF test suite 6-2-3-3-t01-fail-i.pdf"
	if _, err := os.Stat(path); err != nil {
		t.Skip("corpus fixture not present")
	}

	cr, err := Convert(path, pdf.PDFA_1B)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	for _, iss := range cr.Residual() {
		if iss.Check() == pdf.Checks.Colour.DeviceColourContentStream {
			t.Errorf("DeviceColourContentStream still present after conversion: %v", iss)
		}
	}
}

// buildNestedAPPage constructs a page with one widget annotation whose AP/N
// appearance stream invokes a nested Form XObject that contains an "rg" operator.
func buildNestedAPPage() (page, resources pdf.PDFDict) {
	// Nested Form XObject: uses DeviceRGB via "rg"
	rgbContent, _ := writer.WriteContentStream([]writer.ContentOp{
		{Op: "rg", Operands: []pdf.PDFValue{pdf.PDFReal(1), pdf.PDFReal(0), pdf.PDFReal(0)}},
	})
	xobj := pdf.NewPDFDict()
	xobj.HasStream = true
	xobj.RawStream = rgbContent
	xobj.Entries["Subtype"] = pdf.PDFName{Value: "Form"}

	// Appearance stream: Does the XObject
	doContent, _ := writer.WriteContentStream([]writer.ContentOp{
		{Op: "Do", Operands: []pdf.PDFValue{pdf.PDFName{Value: "Fm0"}}},
	})
	xobjects := pdf.NewPDFDict()
	xobjects.Entries["Fm0"] = xobj
	apRes := pdf.NewPDFDict()
	apRes.Entries["XObject"] = xobjects

	apStream := pdf.NewPDFDict()
	apStream.HasStream = true
	apStream.RawStream = doContent
	apStream.Entries["Resources"] = apRes

	ap := pdf.NewPDFDict()
	ap.Entries["N"] = apStream

	annot := pdf.NewPDFDict()
	annot.Entries["AP"] = ap

	page = pdf.NewPDFDict()
	page.Entries["Type"] = pdf.PDFName{Value: "Page"}
	page.Entries["Annots"] = pdf.PDFArray{annot}
	resources = pdf.NewPDFDict()
	return page, resources
}

// TestScanAPStreamDetectsNestedXObjectRGB confirms that scanAPStream now
// follows Do operators into nested Form XObjects when scanning for colours.
func TestScanAPStreamDetectsNestedXObjectRGB(t *testing.T) {
	page, _ := buildNestedAPPage()

	annots := page.Entries["Annots"].(pdf.PDFArray)
	annot := annots[0].(pdf.PDFDict)
	ap := annot.Entries["AP"].(pdf.PDFDict)
	apStream := ap.Entries["N"].(pdf.PDFDict)
	apRes := apStream.Entries["Resources"].(pdf.PDFDict)

	used := map[string]bool{}
	scanAPStream(apStream, apRes, map[uintptr]bool{}, func(m string) { used[m] = true }, nil)

	if !used["rgb"] {
		t.Error("scanAPStream did not detect DeviceRGB inside a Do-invoked nested Form XObject")
	}
}

// TestPageDeviceColourModelsFindsNestedAppearanceRGB checks that a widget
// annotation whose appearance stream Does a nested Form XObject with DeviceRGB
// is correctly identified — the bug that caused 208 veraPDF 6.2.3.3 failures.
func TestPageDeviceColourModelsFindsNestedAppearanceRGB(t *testing.T) {
	page, resources := buildNestedAPPage()
	used := pageDeviceColourModels(page, resources, nil)
	if !used["rgb"] {
		t.Error("pageDeviceColourModels did not detect DeviceRGB in nested widget appearance XObject")
	}
}

// TestFixAPColourInjectsIntoNestedXObject verifies that fixAPColour injects
// /DefaultRGB into the nested Form XObject's own /Resources/ColorSpace dict.
func TestFixAPColourInjectsIntoNestedXObject(t *testing.T) {
	page, _ := buildNestedAPPage()
	annots := page.Entries["Annots"].(pdf.PDFArray)
	annot := annots[0].(pdf.PDFDict)
	ap := annot.Entries["AP"].(pdf.PDFDict)
	apStream := ap.Entries["N"].(pdf.PDFDict)
	apRes := apStream.Entries["Resources"].(pdf.PDFDict)
	xobjects := apRes.Entries["XObject"].(pdf.PDFDict)
	xobj := xobjects.Entries["Fm0"].(pdf.PDFDict)

	sharedRGB := iccBasedColourSpace(3, []byte("fakeicc"))
	changed := fixAPColour(ap.Entries["N"], true, false, sharedRGB, nil, nil)

	if !changed {
		t.Fatal("fixAPColour returned false, expected an injection")
	}
	// DefaultRGB must be present in the nested XObject's own resources.
	xobjRes, _ := xobj.Entries["Resources"].(pdf.PDFDict)
	if !verify.DefaultColorSpaceDefined("rgb", xobjRes) {
		t.Error("DefaultRGB not injected into nested Form XObject resources")
	}
}

// TestDeviceColourFixerInjectsAPDefaultRGBWhenPageAlreadyHasIt verifies that
// the appearance-stream injection happens even when the page resources already
// carry /DefaultRGB (the intermittent L34a bug: page-gated early-return was
// wrongly suppressing the AP injection loop).
func TestDeviceColourFixerInjectsAPDefaultRGBWhenPageAlreadyHasIt(t *testing.T) {
	// Build a minimal CMYK-OutputIntent document whose page resources already
	// have /DefaultRGB but whose AP stream uses "rg" without its own DefaultRGB.
	apContent, err := writer.WriteContentStream([]writer.ContentOp{
		{Op: "rg", Operands: []pdf.PDFValue{pdf.PDFReal(1), pdf.PDFReal(0), pdf.PDFReal(0)}},
	})
	if err != nil {
		t.Fatalf("WriteContentStream: %v", err)
	}
	apStream := pdf.NewPDFDict()
	apStream.HasStream = true
	apStream.RawStream = apContent
	apStream.Entries["Subtype"] = pdf.PDFName{Value: "Form"}

	ap := pdf.NewPDFDict()
	ap.Entries["N"] = apStream

	annot := pdf.NewPDFDict()
	annot.Entries["AP"] = ap

	// Inject DefaultRGB into page resources to simulate a prior converter pass.
	sharedRGB := iccBasedColourSpace(3, srgbICCProfile)
	pageCS := pdf.NewPDFDict()
	pageCS.Entries["DefaultRGB"] = sharedRGB
	pageRes := pdf.NewPDFDict()
	pageRes.Entries["ColorSpace"] = pageCS

	page := pdf.NewPDFDict()
	page.Entries["Type"] = pdf.PDFName{Value: "Page"}
	page.Entries["Resources"] = pageRes
	page.Entries["Annots"] = pdf.PDFArray{annot}

	// Minimal trailer with a CMYK OutputIntent (OutputConditionIdentifier only).
	oi := pdf.NewPDFDict()
	oi.Entries["S"] = pdf.PDFName{Value: "GTS_PDFA1"}
	oi.Entries["DestOutputProfile"] = pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}, pdf.PDFInteger(4)}
	oiArr := pdf.PDFArray{oi}
	catalog := pdf.NewPDFDict()
	catalog.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
	catalog.Entries["OutputIntents"] = oiArr
	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = catalog
	trailer.Entries["Pages"] = pdf.PDFArray{page}

	fixer := deviceColourFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatal("expected Fix to return changed=true")
	}

	// The appearance stream's own resources must now carry DefaultRGB.
	apStream2 := annot.Entries["AP"].(pdf.PDFDict).Entries["N"].(pdf.PDFDict)
	apRes2, _ := apStream2.Entries["Resources"].(pdf.PDFDict)
	if !verify.DefaultColorSpaceDefined("rgb", apRes2) {
		t.Error("DefaultRGB not injected into AP stream resources despite page already having it")
	}
}

// TestPageDeviceColourModelsFindsContentAndDictUsage checks
// pageDeviceColourModels against a synthetic page mixing a content-stream
// "k" operator (CMYK) with an Image XObject whose own /ColorSpace is
// DeviceRGB, confirming both detection paths (content scan and resource-dict
// scan) feed into the same result set.
func TestPageDeviceColourModelsFindsContentAndDictUsage(t *testing.T) {
	content, err := writer.WriteContentStream([]writer.ContentOp{
		{Op: "k", Operands: []pdf.PDFValue{pdf.PDFReal(0), pdf.PDFReal(0), pdf.PDFReal(0), pdf.PDFReal(1)}},
	})
	if err != nil {
		t.Fatalf("writeContentStream: %v", err)
	}

	page := pdf.NewPDFDict()
	page.Entries["Type"] = pdf.PDFName{Value: "Page"}
	contentsDict := pdf.NewPDFDict()
	contentsDict.HasStream = true
	contentsDict.RawStream = content
	page.Entries["Contents"] = contentsDict

	image := pdf.NewPDFDict()
	image.Entries["Subtype"] = pdf.PDFName{Value: "Image"}
	image.Entries["ColorSpace"] = pdf.PDFName{Value: "DeviceRGB"}

	xobjects := pdf.NewPDFDict()
	xobjects.Entries["Im0"] = image
	resources := pdf.NewPDFDict()
	resources.Entries["XObject"] = xobjects

	used := pageDeviceColourModels(page, resources, nil)
	if !used["cmyk"] {
		t.Errorf("expected cmyk usage from content-stream k operator, got %v", used)
	}
	if !used["rgb"] {
		t.Errorf("expected rgb usage from Image XObject ColorSpace, got %v", used)
	}
}
