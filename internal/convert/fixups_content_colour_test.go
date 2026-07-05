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
	path := "../../tests/veraPDF/PDF_A-1b/6.2 Graphics/6.2.3.3 Uncalibrated color space/veraPDF test suite 6-2-3-3-t01-fail-i.pdf"
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

// TestScanContentColourDetectsNestedXObjectRGB confirms scanContentColour
// follows Do operators into nested Form XObjects when scanning for colours.
func TestScanContentColourDetectsNestedXObjectRGB(t *testing.T) {
	page, _ := buildNestedAPPage()

	annots := page.Entries["Annots"].(pdf.PDFArray)
	annot := annots[0].(pdf.PDFDict)
	ap := annot.Entries["AP"].(pdf.PDFDict)
	apStream := ap.Entries["N"].(pdf.PDFDict)
	apRes := apStream.Entries["Resources"].(pdf.PDFDict)

	visited := map[uintptr]bool{}
	claim := func(ptr uintptr) bool {
		if visited[ptr] {
			return false
		}
		visited[ptr] = true
		return true
	}
	used := map[string]bool{}
	scanContentColour(apStream, apRes, claim, nil, func(m string, _ pdf.PDFDict) { used[m] = true })

	if !used["rgb"] {
		t.Error("scanContentColour did not detect DeviceRGB inside a Do-invoked nested Form XObject")
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
// carry /DefaultRGB.
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

// buildStatefulAPPage constructs a page with one widget annotation whose
// AP/N is a state sub-dictionary (checkbox On/Off), rather than a direct
// stream -- the shape scanAPAppearance/fixAPColour dispatch to when /N isn't
// itself a stream, exercised nowhere else in this file's fixtures.
func buildStatefulAPPage() (page pdf.PDFDict, onStream pdf.PDFDict) {
	onContent, _ := writer.WriteContentStream([]writer.ContentOp{
		{Op: "rg", Operands: []pdf.PDFValue{pdf.PDFReal(1), pdf.PDFReal(0), pdf.PDFReal(0)}},
	})
	onStream = pdf.NewPDFDict()
	onStream.HasStream = true
	onStream.RawStream = onContent

	offContent, _ := writer.WriteContentStream(nil)
	offStream := pdf.NewPDFDict()
	offStream.HasStream = true
	offStream.RawStream = offContent

	n := pdf.NewPDFDict()
	n.Entries["On"] = onStream
	n.Entries["Off"] = offStream

	ap := pdf.NewPDFDict()
	ap.Entries["N"] = n

	annot := pdf.NewPDFDict()
	annot.Entries["AP"] = ap

	page = pdf.NewPDFDict()
	page.Entries["Type"] = pdf.PDFName{Value: "Page"}
	page.Entries["Annots"] = pdf.PDFArray{annot}
	return page, onStream
}

// TestPageDeviceColourModelsFindsStatefulAppearanceRGB covers
// scanAPAppearance's state-sub-dictionary branch (AP/N with no stream of
// its own, e.g. a checkbox's On/Off appearances).
func TestPageDeviceColourModelsFindsStatefulAppearanceRGB(t *testing.T) {
	page, _ := buildStatefulAPPage()
	used := pageDeviceColourModels(page, pdf.NewPDFDict(), nil)
	if !used["rgb"] {
		t.Error("pageDeviceColourModels did not detect DeviceRGB in a stateful (On/Off) AP/N appearance")
	}
}

// TestFixAPColourInjectsIntoEachState covers fixAPColour's matching
// state-sub-dictionary branch: each state stream must get its own injection.
func TestFixAPColourInjectsIntoEachState(t *testing.T) {
	page, onStream := buildStatefulAPPage()
	annot := page.Entries["Annots"].(pdf.PDFArray)[0].(pdf.PDFDict)
	n := annot.Entries["AP"].(pdf.PDFDict).Entries["N"]

	sharedRGB := iccBasedColourSpace(3, []byte("fakeicc"))
	if !fixAPColour(n, true, false, sharedRGB, nil, nil) {
		t.Fatal("fixAPColour returned false, expected an injection into the On state")
	}
	onRes, _ := onStream.Entries["Resources"].(pdf.PDFDict)
	if !verify.DefaultColorSpaceDefined("rgb", onRes) {
		t.Error("DefaultRGB not injected into the On state's own resources")
	}
}

// TestPageDeviceColourModelsFindsShadingPatternAndArrayContents covers the
// remaining pageDeviceColourModels branches untouched by the other tests in
// this file: an array-form /Contents, a /Shading resource dict entry, and a
// tiling Pattern's own /Shading entry.
func TestPageDeviceColourModelsFindsShadingPatternAndArrayContents(t *testing.T) {
	contentA, _ := writer.WriteContentStream(nil)
	dictA := pdf.NewPDFDict()
	dictA.HasStream = true
	dictA.RawStream = contentA
	contentB, _ := writer.WriteContentStream([]writer.ContentOp{
		{Op: "k", Operands: []pdf.PDFValue{pdf.PDFReal(0), pdf.PDFReal(0), pdf.PDFReal(0), pdf.PDFReal(1)}},
	})
	dictB := pdf.NewPDFDict()
	dictB.HasStream = true
	dictB.RawStream = contentB

	shading := pdf.NewPDFDict()
	shading.Entries["ColorSpace"] = pdf.PDFName{Value: "DeviceRGB"}
	shadings := pdf.NewPDFDict()
	shadings.Entries["Sh1"] = shading

	patternShading := pdf.NewPDFDict()
	patternShading.Entries["ColorSpace"] = pdf.PDFName{Value: "DeviceRGB"}
	pattern := pdf.NewPDFDict()
	pattern.Entries["Shading"] = patternShading
	patterns := pdf.NewPDFDict()
	patterns.Entries["P1"] = pattern

	resources := pdf.NewPDFDict()
	resources.Entries["Shading"] = shadings
	resources.Entries["Pattern"] = patterns

	page := pdf.NewPDFDict()
	page.Entries["Type"] = pdf.PDFName{Value: "Page"}
	page.Entries["Contents"] = pdf.PDFArray{dictA, dictB}

	used := pageDeviceColourModels(page, resources, nil)
	if !used["cmyk"] {
		t.Errorf("expected cmyk from the array-form Contents' second stream, got %v", used)
	}
	if !used["rgb"] {
		t.Errorf("expected rgb from the /Shading resource and the Pattern's own /Shading, got %v", used)
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

// TestScanContentColourDetectsInlineImageColourSpace covers the INLINEIMAGE
// operand branch for both an inline image /CS given as a bare name and as
// an array (e.g. an Indexed base), neither exercised by the other tests in
// this file, which only use rg/g/k and cs/CS.
func TestScanContentColourDetectsInlineImageColourSpace(t *testing.T) {
	visited := map[uintptr]bool{}
	claim := func(ptr uintptr) bool {
		if visited[ptr] {
			return false
		}
		visited[ptr] = true
		return true
	}

	t.Run("name form", func(t *testing.T) {
		dict := pdf.NewPDFDict()
		dict.HasStream = true
		dict.RawStream = []byte("BI /CS /DeviceRGB ID X EI")
		used := map[string]bool{}
		scanContentColour(dict, pdf.NewPDFDict(), claim, nil, func(m string, _ pdf.PDFDict) { used[m] = true })
		if !used["rgb"] {
			t.Error("did not detect rgb from an inline image's name-form /CS")
		}
	})

	t.Run("array form", func(t *testing.T) {
		dict := pdf.NewPDFDict()
		dict.HasStream = true
		dict.RawStream = []byte("BI /CS [/DeviceCMYK] ID X EI")
		used := map[string]bool{}
		scanContentColour(dict, pdf.NewPDFDict(), claim, nil, func(m string, _ pdf.PDFDict) { used[m] = true })
		if !used["cmyk"] {
			t.Error("did not detect cmyk from an inline image's array-form /CS")
		}
	})
}

// TestScanContentColourDetectsPatternCMYK covers scanContentColour's scn/SCN
// tiling-pattern recursion -- a distinct code path from
// pageDeviceColourModels' own resource-dict-only Pattern/Shading scan
// (TestPageDeviceColourModelsFindsShadingPatternAndArrayContents).
func TestScanContentColourDetectsPatternCMYK(t *testing.T) {
	patternContent, err := writer.WriteContentStream([]writer.ContentOp{
		{Op: "k", Operands: []pdf.PDFValue{pdf.PDFReal(0), pdf.PDFReal(0), pdf.PDFReal(0), pdf.PDFReal(1)}},
	})
	if err != nil {
		t.Fatalf("WriteContentStream: %v", err)
	}
	pattern := pdf.NewPDFDict()
	pattern.HasStream = true
	pattern.RawStream = patternContent

	patterns := pdf.NewPDFDict()
	patterns.Entries["P0"] = pattern
	resources := pdf.NewPDFDict()
	resources.Entries["Pattern"] = patterns

	content, err := writer.WriteContentStream([]writer.ContentOp{
		{Op: "scn", Operands: []pdf.PDFValue{pdf.PDFName{Value: "P0"}}},
	})
	if err != nil {
		t.Fatalf("WriteContentStream: %v", err)
	}
	dict := pdf.NewPDFDict()
	dict.HasStream = true
	dict.RawStream = content

	visited := map[uintptr]bool{}
	claim := func(ptr uintptr) bool {
		if visited[ptr] {
			return false
		}
		visited[ptr] = true
		return true
	}
	used := map[string]bool{}
	scanContentColour(dict, resources, claim, nil, func(m string, _ pdf.PDFDict) { used[m] = true })

	if !used["cmyk"] {
		t.Error("scanContentColour did not detect cmyk usage inside an scn-invoked tiling pattern")
	}
}

// TestDeviceColourFixerInjectsCMYKWithoutOutputIntent mirrors the RGB-side
// tests in this file on the CMYK branches of Fix (needCMYK/apNeedCMYK/
// sharedCMYK/DefaultCMYK), driven by a document with no OutputIntent at all.
func TestDeviceColourFixerInjectsCMYKWithoutOutputIntent(t *testing.T) {
	kOp := []writer.ContentOp{
		{Op: "k", Operands: []pdf.PDFValue{pdf.PDFReal(0), pdf.PDFReal(0), pdf.PDFReal(0), pdf.PDFReal(1)}},
	}
	content, err := writer.WriteContentStream(kOp)
	if err != nil {
		t.Fatalf("WriteContentStream: %v", err)
	}
	contentsDict := pdf.NewPDFDict()
	contentsDict.HasStream = true
	contentsDict.RawStream = content

	apContent, err := writer.WriteContentStream(kOp)
	if err != nil {
		t.Fatalf("WriteContentStream: %v", err)
	}
	apStream := pdf.NewPDFDict()
	apStream.HasStream = true
	apStream.RawStream = apContent

	ap := pdf.NewPDFDict()
	ap.Entries["N"] = apStream
	annot := pdf.NewPDFDict()
	annot.Entries["AP"] = ap

	page := pdf.NewPDFDict()
	page.Entries["Type"] = pdf.PDFName{Value: "Page"}
	page.Entries["Contents"] = contentsDict
	page.Entries["Resources"] = pdf.NewPDFDict()
	page.Entries["Annots"] = pdf.PDFArray{annot}

	catalog := pdf.NewPDFDict()
	catalog.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
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

	pageRes, _ := page.Entries["Resources"].(pdf.PDFDict)
	if !verify.DefaultColorSpaceDefined("cmyk", pageRes) {
		t.Error("DefaultCMYK not injected into page resources")
	}
	apRes, _ := apStream.Entries["Resources"].(pdf.PDFDict)
	if !verify.DefaultColorSpaceDefined("cmyk", apRes) {
		t.Error("DefaultCMYK not injected into AP stream resources")
	}
}
