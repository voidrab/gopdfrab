package verify

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// buildAPWithRGB returns a page dict whose widget annotation's AP/N stream
// contains an "rg" operator, plus a separate page-level resources dict that
// already holds /ColorSpace/DefaultRGB. The appearance stream's own resources
// intentionally lack DefaultRGB, mimicking the failing L34a conversion output.
func buildAPWithRGB(t *testing.T) (page pdf.PDFDict, pageRes pdf.PDFDict) {
	t.Helper()
	apContent, err := writer.WriteContentStream([]writer.ContentOp{
		{Op: "rg", Operands: []pdf.PDFValue{pdf.PDFReal(1), pdf.PDFReal(0), pdf.PDFReal(0)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	apStream := pdf.NewPDFDict()
	apStream.HasStream = true
	apStream.RawStream = apContent
	apStream.Entries["Resources"] = pdf.NewPDFDict() // no DefaultRGB here

	ap := pdf.NewPDFDict()
	ap.Entries["N"] = apStream

	annot := pdf.NewPDFDict()
	annot.Entries["AP"] = ap

	page = pdf.NewPDFDict()
	page.Entries["Type"] = pdf.PDFName{Value: "Page"}
	page.Entries["Annots"] = pdf.PDFArray{annot}

	// Page resources carry DefaultRGB — the soundness bug made this excuse the AP.
	cs := pdf.NewPDFDict()
	cs.Entries["DefaultRGB"] = pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}}
	pageRes = pdf.NewPDFDict()
	pageRes.Entries["ColorSpace"] = cs
	return page, pageRes
}

// TestScanAnnotAppearancesRejectsPageDefaultRGB confirms that a widget
// appearance stream's DeviceRGB is reported even when the page resources carry
// /DefaultRGB, because appearance streams have their own resource scope.
func TestScanAnnotAppearancesRejectsPageDefaultRGB(t *testing.T) {
	page, pageRes := buildAPWithRGB(t)

	ctx := &ValidationContext{hasOutputIntent: true, cmykCovered: true}
	ctx.pageResources = pageRes

	scanAnnotAppearances(page, ctx)

	found := false
	for _, iss := range ctx.Issues() {
		if iss.Check() == pdf.Checks.Colour.DeviceColourContentStream {
			found = true
		}
	}
	if !found {
		t.Error("expected DeviceColourContentStream from appearance-stream rg with page-only DefaultRGB; got none")
	}
}

// TestScanAnnotAppearancesHonoursOwnDefaultRGB confirms that a widget
// appearance stream with /DefaultRGB in its OWN resources is not flagged.
func TestScanAnnotAppearancesHonoursOwnDefaultRGB(t *testing.T) {
	page, _ := buildAPWithRGB(t)

	// Inject DefaultRGB into the appearance stream's own resources.
	annot := page.Entries["Annots"].(pdf.PDFArray)[0].(pdf.PDFDict)
	apStream := annot.Entries["AP"].(pdf.PDFDict).Entries["N"].(pdf.PDFDict)
	cs := pdf.NewPDFDict()
	cs.Entries["DefaultRGB"] = pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}}
	apRes := pdf.NewPDFDict()
	apRes.Entries["ColorSpace"] = cs
	apStream.Entries["Resources"] = apRes

	ctx := &ValidationContext{hasOutputIntent: true, cmykCovered: true}
	scanAnnotAppearances(page, ctx)

	for _, iss := range ctx.Issues() {
		if iss.Check() == pdf.Checks.Colour.DeviceColourContentStream {
			t.Errorf("unexpected DeviceColourContentStream when AP has own DefaultRGB: %v", iss.Error())
		}
	}
}

// TestPageContentFormXObjectStillInheritsPageDefaultRGB confirms the existing
// behaviour: a Form XObject invoked from page content (via Do) is excused by the
// page's /DefaultRGB via the ctx.pageResources fallback — unaffected by Fix A.
func TestPageContentFormXObjectStillInheritsPageDefaultRGB(t *testing.T) {
	// Form XObject with DeviceRGB usage, no own resources.
	fContent, err := writer.WriteContentStream([]writer.ContentOp{
		{Op: "rg", Operands: []pdf.PDFValue{pdf.PDFReal(1), pdf.PDFReal(0), pdf.PDFReal(0)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// scanContentDict is called with the XObject's own (empty) resources and the
	// page resources as ctx.pageResources — reportContentColour should not fire.
	xobj := pdf.NewPDFDict()
	xobj.HasStream = true
	xobj.RawStream = fContent

	cs := pdf.NewPDFDict()
	cs.Entries["DefaultRGB"] = pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}}
	pageRes := pdf.NewPDFDict()
	pageRes.Entries["ColorSpace"] = cs

	ctx := &ValidationContext{hasOutputIntent: true, cmykCovered: true}
	ctx.pageResources = pageRes

	scanContentDict(xobj, pdf.NewPDFDict(), ctx)

	for _, iss := range ctx.Issues() {
		if iss.Check() == pdf.Checks.Colour.DeviceColourContentStream {
			t.Errorf("page-content Form XObject should be excused by page DefaultRGB: %v", iss.Error())
		}
	}
}
