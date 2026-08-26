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

	cr, err := Convert(path, pdf.PDFA1B, Options{})
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
	xobj.Entries.Set("Subtype", pdf.PDFName{Value: "Form"})

	// Appearance stream: Does the XObject
	doContent, _ := writer.WriteContentStream([]writer.ContentOp{
		{Op: "Do", Operands: []pdf.PDFValue{pdf.PDFName{Value: "Fm0"}}},
	})
	xobjects := pdf.NewPDFDict()
	xobjects.Entries.Set("Fm0", xobj)
	apRes := pdf.NewPDFDict()
	apRes.Entries.Set("XObject", xobjects)

	apStream := pdf.NewPDFDict()
	apStream.HasStream = true
	apStream.RawStream = doContent
	apStream.Entries.Set("Resources", apRes)

	ap := pdf.NewPDFDict()
	ap.Entries.Set("N", apStream)

	annot := pdf.NewPDFDict()
	annot.Entries.Set("AP", ap)

	page = pdf.NewPDFDict()
	page.Entries.Set("Type", pdf.PDFName{Value: "Page"})
	page.Entries.Set("Annots", pdf.PDFArray{annot})
	resources = pdf.NewPDFDict()
	return page, resources
}

// TestScanContentColourDetectsNestedXObjectRGB confirms scanContentColour
// follows Do operators into nested Form XObjects when scanning for colours.
func TestScanContentColourDetectsNestedXObjectRGB(t *testing.T) {
	page, _ := buildNestedAPPage()

	annots := page.Entries.Get("Annots").(pdf.PDFArray)
	annot := annots[0].(pdf.PDFDict)
	ap := annot.Entries.Get("AP").(pdf.PDFDict)
	apStream := ap.Entries.Get("N").(pdf.PDFDict)
	apRes := apStream.Entries.Get("Resources").(pdf.PDFDict)

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
	annots := page.Entries.Get("Annots").(pdf.PDFArray)
	annot := annots[0].(pdf.PDFDict)
	ap := annot.Entries.Get("AP").(pdf.PDFDict)
	apStream := ap.Entries.Get("N").(pdf.PDFDict)
	apRes := apStream.Entries.Get("Resources").(pdf.PDFDict)
	xobjects := apRes.Entries.Get("XObject").(pdf.PDFDict)
	xobj := xobjects.Entries.Get("Fm0").(pdf.PDFDict)

	sharedRGB := iccBasedColourSpace(3, []byte("fakeicc"))
	changed := fixAPColour(ap.Entries.Get("N"), true, false, sharedRGB, nil, nil)

	if !changed {
		t.Fatal("fixAPColour returned false, expected an injection")
	}
	// DefaultRGB must be present in the nested XObject's own resources.
	xobjRes, _ := xobj.Entries.Get("Resources").(pdf.PDFDict)
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
	apStream.Entries.Set("Subtype", pdf.PDFName{Value: "Form"})

	ap := pdf.NewPDFDict()
	ap.Entries.Set("N", apStream)

	annot := pdf.NewPDFDict()
	annot.Entries.Set("AP", ap)

	// Inject DefaultRGB into page resources to simulate a prior converter pass.
	sharedRGB := iccBasedColourSpace(3, srgbICCProfile)
	pageCS := pdf.NewPDFDict()
	pageCS.Entries.Set("DefaultRGB", sharedRGB)
	pageRes := pdf.NewPDFDict()
	pageRes.Entries.Set("ColorSpace", pageCS)

	page := pdf.NewPDFDict()
	page.Entries.Set("Type", pdf.PDFName{Value: "Page"})
	page.Entries.Set("Resources", pageRes)
	page.Entries.Set("Annots", pdf.PDFArray{annot})

	// Minimal trailer with a CMYK OutputIntent (OutputConditionIdentifier only).
	oi := pdf.NewPDFDict()
	oi.Entries.Set("S", pdf.PDFName{Value: "GTS_PDFA1"})
	oi.Entries.Set("DestOutputProfile", pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}, pdf.PDFInteger(4)})
	oiArr := pdf.PDFArray{oi}
	catalog := pdf.NewPDFDict()
	catalog.Entries.Set("Type", pdf.PDFName{Value: "Catalog"})
	catalog.Entries.Set("OutputIntents", oiArr)
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", catalog)
	trailer.Entries.Set("Pages", pdf.PDFArray{page})

	fixer := deviceColourFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatal("expected Fix to return changed=true")
	}

	// The appearance stream's own resources must now carry DefaultRGB.
	apStream2 := annot.Entries.Get("AP").(pdf.PDFDict).Entries.Get("N").(pdf.PDFDict)
	apRes2, _ := apStream2.Entries.Get("Resources").(pdf.PDFDict)
	if !verify.DefaultColorSpaceDefined("rgb", apRes2) {
		t.Error("DefaultRGB not injected into AP stream resources despite page already having it")
	}
}

// TestDeviceColourFixerInjectsIntoNestedFormResources covers the form XObject
// a page draws: it is checked in its own /Resources, so the page's DefaultCMYK
// does not excuse the CMYK inside it and it needs the entry too. A form with no
// resources of its own reads the page's and must be left as it is -- giving it
// one would cut it off from every other name the page defines.
func TestDeviceColourFixerInjectsIntoNestedFormResources(t *testing.T) {
	formContent, err := writer.WriteContentStream([]writer.ContentOp{
		{Op: "k", Operands: []pdf.PDFValue{pdf.PDFReal(0), pdf.PDFReal(0), pdf.PDFReal(0), pdf.PDFReal(1)}},
	})
	if err != nil {
		t.Fatalf("WriteContentStream: %v", err)
	}
	owned := pdf.NewPDFDict()
	owned.HasStream = true
	owned.RawStream = formContent
	owned.Entries.Set("Subtype", pdf.PDFName{Value: "Form"})
	owned.Entries.Set("Resources", pdf.NewPDFDict())

	borrowed := pdf.NewPDFDict()
	borrowed.HasStream = true
	borrowed.RawStream = formContent
	borrowed.Entries.Set("Subtype", pdf.PDFName{Value: "Form"})

	pageContent, err := writer.WriteContentStream([]writer.ContentOp{
		{Op: "Do", Operands: []pdf.PDFValue{pdf.PDFName{Value: "Fown"}}},
		{Op: "Do", Operands: []pdf.PDFValue{pdf.PDFName{Value: "Fborrowed"}}},
	})
	if err != nil {
		t.Fatalf("WriteContentStream: %v", err)
	}
	contents := pdf.NewPDFDict()
	contents.HasStream = true
	contents.RawStream = pageContent

	xobjects := pdf.NewPDFDict()
	xobjects.Entries.Set("Fown", owned)
	xobjects.Entries.Set("Fborrowed", borrowed)
	// The page already carries DefaultCMYK, which is what used to stop the
	// injection reaching anything below it.
	pageCS := pdf.NewPDFDict()
	pageCS.Entries.Set("DefaultCMYK", iccBasedColourSpace(4, cmykICCProfile))
	pageRes := pdf.NewPDFDict()
	pageRes.Entries.Set("ColorSpace", pageCS)
	pageRes.Entries.Set("XObject", xobjects)

	page := pdf.NewPDFDict()
	page.Entries.Set("Type", pdf.PDFName{Value: "Page"})
	page.Entries.Set("Resources", pageRes)
	page.Entries.Set("Contents", contents)

	oi := pdf.NewPDFDict()
	oi.Entries.Set("S", pdf.PDFName{Value: "GTS_PDFA1"})
	profile := pdf.NewPDFDict()
	profile.Entries.Set("N", pdf.PDFInteger(3))
	oi.Entries.Set("DestOutputProfile", profile)
	catalog := pdf.NewPDFDict()
	catalog.Entries.Set("Type", pdf.PDFName{Value: "Catalog"})
	catalog.Entries.Set("OutputIntents", pdf.PDFArray{oi})
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", catalog)
	trailer.Entries.Set("Pages", pdf.PDFArray{page})

	changed, err := deviceColourFixer{}.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatal("expected Fix to return changed=true")
	}

	ownRes, _ := owned.Entries.Get("Resources").(pdf.PDFDict)
	if !verify.DefaultColorSpaceDefined("cmyk", ownRes) {
		t.Error("DefaultCMYK not injected into the form's own resources")
	}
	if _, has := borrowed.Entries.Lookup("Resources"); has {
		t.Error("a form without its own resources was given one")
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
	n.Entries.Set("On", onStream)
	n.Entries.Set("Off", offStream)

	ap := pdf.NewPDFDict()
	ap.Entries.Set("N", n)

	annot := pdf.NewPDFDict()
	annot.Entries.Set("AP", ap)

	page = pdf.NewPDFDict()
	page.Entries.Set("Type", pdf.PDFName{Value: "Page"})
	page.Entries.Set("Annots", pdf.PDFArray{annot})
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
	annot := page.Entries.Get("Annots").(pdf.PDFArray)[0].(pdf.PDFDict)
	n := annot.Entries.Get("AP").(pdf.PDFDict).Entries.Get("N")

	sharedRGB := iccBasedColourSpace(3, []byte("fakeicc"))
	if !fixAPColour(n, true, false, sharedRGB, nil, nil) {
		t.Fatal("fixAPColour returned false, expected an injection into the On state")
	}
	onRes, _ := onStream.Entries.Get("Resources").(pdf.PDFDict)
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
	shading.Entries.Set("ColorSpace", pdf.PDFName{Value: "DeviceRGB"})
	shadings := pdf.NewPDFDict()
	shadings.Entries.Set("Sh1", shading)

	patternShading := pdf.NewPDFDict()
	patternShading.Entries.Set("ColorSpace", pdf.PDFName{Value: "DeviceRGB"})
	pattern := pdf.NewPDFDict()
	pattern.Entries.Set("Shading", patternShading)
	patterns := pdf.NewPDFDict()
	patterns.Entries.Set("P1", pattern)

	resources := pdf.NewPDFDict()
	resources.Entries.Set("Shading", shadings)
	resources.Entries.Set("Pattern", patterns)

	page := pdf.NewPDFDict()
	page.Entries.Set("Type", pdf.PDFName{Value: "Page"})
	page.Entries.Set("Contents", pdf.PDFArray{dictA, dictB})

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
	page.Entries.Set("Type", pdf.PDFName{Value: "Page"})
	contentsDict := pdf.NewPDFDict()
	contentsDict.HasStream = true
	contentsDict.RawStream = content
	page.Entries.Set("Contents", contentsDict)

	image := pdf.NewPDFDict()
	image.Entries.Set("Subtype", pdf.PDFName{Value: "Image"})
	image.Entries.Set("ColorSpace", pdf.PDFName{Value: "DeviceRGB"})

	xobjects := pdf.NewPDFDict()
	xobjects.Entries.Set("Im0", image)
	resources := pdf.NewPDFDict()
	resources.Entries.Set("XObject", xobjects)

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
	patterns.Entries.Set("P0", pattern)
	resources := pdf.NewPDFDict()
	resources.Entries.Set("Pattern", patterns)

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
	ap.Entries.Set("N", apStream)
	annot := pdf.NewPDFDict()
	annot.Entries.Set("AP", ap)

	page := pdf.NewPDFDict()
	page.Entries.Set("Type", pdf.PDFName{Value: "Page"})
	page.Entries.Set("Contents", contentsDict)
	page.Entries.Set("Resources", pdf.NewPDFDict())
	page.Entries.Set("Annots", pdf.PDFArray{annot})

	catalog := pdf.NewPDFDict()
	catalog.Entries.Set("Type", pdf.PDFName{Value: "Catalog"})
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", catalog)
	trailer.Entries.Set("Pages", pdf.PDFArray{page})

	fixer := deviceColourFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatal("expected Fix to return changed=true")
	}

	pageRes, _ := page.Entries.Get("Resources").(pdf.PDFDict)
	if !verify.DefaultColorSpaceDefined("cmyk", pageRes) {
		t.Error("DefaultCMYK not injected into page resources")
	}
	apRes, _ := apStream.Entries.Get("Resources").(pdf.PDFDict)
	if !verify.DefaultColorSpaceDefined("cmyk", apRes) {
		t.Error("DefaultCMYK not injected into AP stream resources")
	}
}
