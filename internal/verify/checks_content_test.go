package verify

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// buildAPWithRGB returns a page dict whose widget annotation's AP/N stream
// contains an "rg" operator, plus a separate page-level resources dict that
// already holds /ColorSpace/DefaultRGB.
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

// scanRaw tokenizes raw content-stream bytes and runs scanContent over them.
func scanRaw(data []byte, resources pdf.PDFDict, ctx *ValidationContext) {
	ops := pdf.TokenizeContent(data)
	scanContent(ops, pdf.PDFDict{}, resources, ctx)
}

func TestScanContentValue(t *testing.T) {
	single := pdf.NewPDFDict()
	single.HasStream = true
	single.RawStream = []byte("ZzBadOp\n")
	ctx := &ValidationContext{}
	scanContentValue(single, pdf.NewPDFDict(), ctx)
	if !hasCheck(ctx, pdf.Checks.Colour.UndefinedOperator) {
		t.Error("expected the single content stream dict to be scanned")
	}

	arr := pdf.PDFArray{single, pdf.PDFInteger(1) /* non-dict: skipped */}
	ctx2 := &ValidationContext{}
	scanContentValue(arr, pdf.NewPDFDict(), ctx2)
	if !hasCheck(ctx2, pdf.Checks.Colour.UndefinedOperator) {
		t.Error("expected an array of content streams to be scanned")
	}

	ctx3 := &ValidationContext{}
	scanContentValue(pdf.PDFName{Value: "x"}, pdf.NewPDFDict(), ctx3)
	if len(ctx3.errs) != 0 {
		t.Error("unexpected violation for a non-dict/array Contents value")
	}
}

func TestScanContentInlineImage(t *testing.T) {
	data := []byte("q\nBI /CS /RGB /F /LZW /I true /Intent /BadIntent ID \x00\x00\x00 EI\nQ\n")
	ctx := &ValidationContext{hasOutputIntent: true, cmykCovered: true, grayCovered: true}
	scanRaw(data, pdf.NewPDFDict(), ctx)

	want := map[pdf.Check]bool{
		pdf.Checks.Colour.DeviceColourContentStream: false,
		pdf.Checks.Structure.InlineImageLZWFilter:   false,
		pdf.Checks.Image.ImageInterpolate:           false,
		pdf.Checks.Colour.RenderingIntent:           false,
	}
	for _, e := range ctx.errs {
		if _, ok := want[e.Check()]; ok {
			want[e.Check()] = true
		}
	}
	for chk, got := range want {
		if !got {
			t.Errorf("expected check %v from inline image scan, not reported", chk)
		}
	}
}

func TestCheckInlineImageColourNamedResource(t *testing.T) {
	resources := pdf.NewPDFDict()
	cs := pdf.NewPDFDict()
	cs.Entries["MyCS"] = pdf.PDFName{Value: "DeviceCMYK"}
	resources.Entries["ColorSpace"] = cs

	params := []pdf.PDFValue{pdf.PDFName{Value: "CS"}, pdf.PDFName{Value: "MyCS"}}
	ctx := &ValidationContext{hasOutputIntent: true, rgbCovered: true}
	checkInlineImageColour(pdf.PDFDict{}, params, resources, ctx)
	if !hasCheck(ctx, pdf.Checks.Colour.DeviceColourContentStream) {
		t.Error("expected DeviceColourContentStream for an uncovered named resource colour space")
	}

	// Array-form ColorSpace parameter.
	arrParams := []pdf.PDFValue{pdf.PDFName{Value: "ColorSpace"}, pdf.PDFArray{pdf.PDFName{Value: "DeviceGray"}}}
	ctx2 := &ValidationContext{}
	checkInlineImageColour(pdf.PDFDict{}, arrParams, resources, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Colour.DeviceColourContentStream) {
		t.Error("expected DeviceColourContentStream for an uncovered array-form colour space")
	}
}

func TestCheckOperandLimits(t *testing.T) {
	ctx := &ValidationContext{}
	checkOperandLimits(pdf.PDFInteger(3000000000), pdf.PDFDict{}, ctx)
	if !hasCheck(ctx, pdf.Checks.Structure.IntegerOutOfRange) {
		t.Error("expected IntegerOutOfRange for an out-of-range integer")
	}

	ctx2 := &ValidationContext{}
	checkOperandLimits(pdf.PDFReal(40000), pdf.PDFDict{}, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Structure.RealOutOfRange) {
		t.Error("expected RealOutOfRange for an out-of-range real")
	}

	ctx3 := &ValidationContext{}
	longStr := pdf.PDFString{Value: string(make([]byte, 70000))}
	checkOperandLimits(longStr, pdf.PDFDict{}, ctx3)
	if !hasCheck(ctx3, pdf.Checks.Structure.StringTooLong) {
		t.Error("expected StringTooLong for an over-long string operand")
	}

	// Recurses into arrays (e.g. TJ's positioning array).
	ctx4 := &ValidationContext{}
	checkOperandLimits(pdf.PDFArray{pdf.PDFInteger(3000000000)}, pdf.PDFDict{}, ctx4)
	if !hasCheck(ctx4, pdf.Checks.Structure.IntegerOutOfRange) {
		t.Error("expected IntegerOutOfRange recursing into an array operand")
	}
}

func TestScanContentGraphicsStateNestingAndUndefinedOp(t *testing.T) {
	var data []byte
	for range 30 {
		data = append(data, []byte("q\n")...)
	}
	ctx := &ValidationContext{}
	scanRaw(data, pdf.NewPDFDict(), ctx)
	if !hasCheck(ctx, pdf.Checks.Structure.GraphicsStateNesting) {
		t.Error("expected GraphicsStateNesting for q depth > 28")
	}

	ctx2 := &ValidationContext{}
	scanRaw([]byte("ZzNotAnOperator\n"), pdf.NewPDFDict(), ctx2)
	if !hasCheck(ctx2, pdf.Checks.Colour.UndefinedOperator) {
		t.Error("expected UndefinedOperator for an unrecognized keyword")
	}
}

func TestScanContentDefaultGrayAndRenderingIntent(t *testing.T) {
	ctx := &ValidationContext{cmykCovered: true, rgbCovered: true}
	scanRaw([]byte("0 0 100 100 re f\n"), pdf.NewPDFDict(), ctx)
	if !hasCheck(ctx, pdf.Checks.Colour.DeviceColourContentStream) {
		t.Error("expected DeviceColourContentStream for a fill with no colour set (default DeviceGray)")
	}

	ctx2 := &ValidationContext{}
	scanRaw([]byte("/UnknownIntent ri\n"), pdf.NewPDFDict(), ctx2)
	if !hasCheck(ctx2, pdf.Checks.Colour.RenderingIntent) {
		t.Error("expected RenderingIntent for an undefined rendering intent")
	}
}

func TestNamedColourModel(t *testing.T) {
	if got := NamedColourModel(pdf.PDFName{Value: "DeviceRGB"}, pdf.NewPDFDict()); got != "rgb" {
		t.Errorf("NamedColourModel(DeviceRGB) = %q, want rgb", got)
	}
	resources := pdf.NewPDFDict()
	cs := pdf.NewPDFDict()
	cs.Entries["Custom"] = pdf.PDFName{Value: "DeviceCMYK"}
	resources.Entries["ColorSpace"] = cs
	if got := NamedColourModel(pdf.PDFName{Value: "Custom"}, resources); got != "cmyk" {
		t.Errorf("NamedColourModel(Custom) = %q, want cmyk", got)
	}
	if got := NamedColourModel(pdf.PDFName{Value: "Unresolvable"}, pdf.NewPDFDict()); got != "" {
		t.Errorf("NamedColourModel(unresolvable) = %q, want empty", got)
	}
}

func TestValidateContentStreamsDispatch(t *testing.T) {
	// Tiling pattern: always scanned.
	pattern := pdf.NewPDFDict()
	pattern.Entries["PatternType"] = pdf.PDFInteger(1)
	pattern.HasStream = true
	pattern.RawStream = []byte("ZzBadOp\n")
	ctx := &ValidationContext{}
	validateContentStreams(pattern, ctx)
	if !hasCheck(ctx, pdf.Checks.Colour.UndefinedOperator) {
		t.Error("expected the tiling pattern's content to be scanned")
	}

	// Unreachable Form XObject: skipped.
	form := pdf.NewPDFDict()
	form.Entries["Subtype"] = pdf.PDFName{Value: "Form"}
	form.HasStream = true
	form.RawStream = []byte("ZzBadOp\n")
	ctx2 := &ValidationContext{ReachableXObjectPtrs: map[uintptr]bool{}} // non-nil, form's ptr absent
	validateContentStreams(form, ctx2)
	if len(ctx2.errs) != 0 {
		t.Error("an unreachable Form XObject should not be scanned")
	}

	// Type3 CharProcs: every glyph stream scanned.
	proc := pdf.NewPDFDict()
	proc.HasStream = true
	proc.RawStream = []byte("ZzBadOp\n")
	charProcs := pdf.NewPDFDict()
	charProcs.Entries["g1"] = proc
	t3 := pdf.NewPDFDict()
	t3.Entries["Subtype"] = pdf.PDFName{Value: "Type3"}
	t3.Entries["CharProcs"] = charProcs
	ctx3 := &ValidationContext{}
	validateContentStreams(t3, ctx3)
	if !hasCheck(ctx3, pdf.Checks.Colour.UndefinedOperator) {
		t.Error("expected the Type3 glyph procedure's content to be scanned")
	}
}

func TestScanAPEntryStateSubdictionary(t *testing.T) {
	onState := pdf.NewPDFDict()
	onState.HasStream = true
	onState.RawStream = []byte("ZzBadOp\n")
	states := pdf.NewPDFDict()
	states.Entries["On"] = onState
	states.Entries["Off"] = pdf.NewPDFDict() // no stream: skipped

	ctx := &ValidationContext{}
	scanAPEntry(states, ctx)
	if !hasCheck(ctx, pdf.Checks.Colour.UndefinedOperator) {
		t.Error("expected the 'On' appearance-state stream to be scanned")
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
