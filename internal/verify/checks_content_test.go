package verify

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"
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
	apStream.SetStream(apContent)
	apStream.Entries.Set("Resources", pdf.NewPDFDict()) // no DefaultRGB here

	ap := pdf.NewPDFDict()
	ap.Entries.Set("N", apStream)

	annot := pdf.NewPDFDict()
	annot.Entries.Set("AP", ap)

	page = pdf.NewPDFDict()
	page.Entries.Set("Type", pdf.PDFName{Value: "Page"})
	page.Entries.Set("Annots", pdf.PDFArray{annot})

	// The page resources carry DefaultRGB, which must not excuse the appearance
	// stream, since that stream has its own resource scope.
	cs := pdf.NewPDFDict()
	cs.Entries.Set("DefaultRGB", pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}})
	pageRes = pdf.NewPDFDict()
	pageRes.Entries.Set("ColorSpace", cs)
	return page, pageRes
}

// TestScanAnnotAppearancesRejectsPageDefaultRGB confirms that a widget
// appearance stream's DeviceRGB is reported even when the page resources carry
// /DefaultRGB, because appearance streams have their own resource scope.
func TestScanAnnotAppearancesRejectsPageDefaultRGB(t *testing.T) {
	page, pageRes := buildAPWithRGB(t)

	ctx := &ValidationContext{Spec: a1bSpec, hasOutputIntent: true, cmykCovered: true}
	ctx.resourceScope = pageRes

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
	annot := page.Entries.Get("Annots").(pdf.PDFArray)[0].(pdf.PDFDict)
	apStream := annot.Entries.Get("AP").(pdf.PDFDict).Entries.Get("N").(pdf.PDFDict)
	cs := pdf.NewPDFDict()
	cs.Entries.Set("DefaultRGB", pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}})
	apRes := pdf.NewPDFDict()
	apRes.Entries.Set("ColorSpace", cs)
	apStream.Entries.Set("Resources", apRes)

	ctx := &ValidationContext{Spec: a1bSpec, hasOutputIntent: true, cmykCovered: true}
	scanAnnotAppearances(page, ctx)

	for _, iss := range ctx.Issues() {
		if iss.Check() == pdf.Checks.Colour.DeviceColourContentStream {
			t.Errorf("unexpected DeviceColourContentStream when AP has own DefaultRGB: %v", iss.Error())
		}
	}
}

// scanRaw runs the content checks over raw content-stream bytes.
func scanRaw(data []byte, resources pdf.PDFDict, ctx *ValidationContext) {
	ctx.collapsingStreamReports(func() {
		pdf.NewContentScanner(data).Scan(contentChecker(pdf.PDFDict{}, resources, ctx))
	})
}

// TestValidateContentStreamsPage verifies that a page's /Contents is scanned
// when it is one stream or an array of streams with a non-stream element, and
// that a value of neither kind reports nothing.
func TestValidateContentStreamsPage(t *testing.T) {
	single := pdf.NewPDFDict()
	single.SetStream([]byte("ZzBadOp\n"))
	page := func(contents pdf.PDFValue) pdf.PDFDict {
		p := pdf.NewPDFDict()
		p.Entries.Set("Type", pdf.PDFName{Value: "Page"})
		p.Entries.Set("Contents", contents)
		return p
	}
	for _, tc := range []struct {
		name     string
		contents pdf.PDFValue
		want     bool
	}{
		{"single stream", single, true},
		{"array of streams", pdf.PDFArray{single, pdf.PDFInteger(1)}, true},
		{"neither", pdf.PDFName{Value: "x"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &ValidationContext{Spec: a1bSpec}
			validateContentStreams(page(tc.contents), ctx)
			if got := hasCheck(ctx, pdf.Checks.Colour.UndefinedOperator); got != tc.want {
				t.Errorf("undefined operator reported = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestScanContentInlineImage(t *testing.T) {
	data := []byte("q\nBI /CS /RGB /F /LZW /I true /Intent /BadIntent ID \x00\x00\x00 EI\nQ\n")
	ctx := &ValidationContext{Spec: a1bSpec, hasOutputIntent: true, cmykCovered: true, grayCovered: true}
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
	cs.Entries.Set("MyCS", pdf.PDFName{Value: "DeviceCMYK"})
	resources.Entries.Set("ColorSpace", cs)

	params := []pdf.PDFValue{pdf.PDFName{Value: "CS"}, pdf.PDFName{Value: "MyCS"}}
	ctx := &ValidationContext{Spec: a1bSpec, hasOutputIntent: true, rgbCovered: true}
	checkInlineImageColour(pdf.PDFDict{}, params, resources, ctx)
	if !hasCheck(ctx, pdf.Checks.Colour.DeviceColourContentStream) {
		t.Error("expected DeviceColourContentStream for an uncovered named resource colour space")
	}

	// Array-form ColorSpace parameter.
	arrParams := []pdf.PDFValue{pdf.PDFName{Value: "ColorSpace"}, pdf.PDFArray{pdf.PDFName{Value: "DeviceGray"}}}
	ctx2 := &ValidationContext{Spec: a1bSpec}
	checkInlineImageColour(pdf.PDFDict{}, arrParams, resources, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Colour.DeviceColourContentStream) {
		t.Error("expected DeviceColourContentStream for an uncovered array-form colour space")
	}
}

func TestCheckOperandLimits(t *testing.T) {
	ctx := &ValidationContext{Spec: a1bSpec}
	checkOperandLimits(pdf.PDFInteger(3000000000), pdf.PDFDict{}, ctx)
	if !hasCheck(ctx, pdf.Checks.Structure.IntegerOutOfRange) {
		t.Error("expected IntegerOutOfRange for an out-of-range integer")
	}

	ctx2 := &ValidationContext{Spec: a1bSpec}
	checkOperandLimits(pdf.PDFReal(40000), pdf.PDFDict{}, ctx2)
	if !hasCheck(ctx2, pdf.Checks.Structure.RealOutOfRange) {
		t.Error("expected RealOutOfRange for an out-of-range real")
	}

	ctx3 := &ValidationContext{Spec: a1bSpec}
	longStr := pdf.PDFString{Value: string(make([]byte, 70000))}
	checkOperandLimits(longStr, pdf.PDFDict{}, ctx3)
	if !hasCheck(ctx3, pdf.Checks.Structure.StringTooLong) {
		t.Error("expected StringTooLong for an over-long string operand")
	}

	// Recurses into arrays (e.g. TJ's positioning array).
	ctx4 := &ValidationContext{Spec: a1bSpec}
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
	ctx := &ValidationContext{Spec: a1bSpec}
	scanRaw(data, pdf.NewPDFDict(), ctx)
	if !hasCheck(ctx, pdf.Checks.Structure.GraphicsStateNesting) {
		t.Error("expected GraphicsStateNesting for q depth > 28")
	}

	ctx2 := &ValidationContext{Spec: a1bSpec}
	scanRaw([]byte("ZzNotAnOperator\n"), pdf.NewPDFDict(), ctx2)
	if !hasCheck(ctx2, pdf.Checks.Colour.UndefinedOperator) {
		t.Error("expected UndefinedOperator for an unrecognized keyword")
	}
}

func TestScanContentDefaultGrayAndRenderingIntent(t *testing.T) {
	ctx := &ValidationContext{Spec: a1bSpec, cmykCovered: true, rgbCovered: true}
	scanRaw([]byte("0 0 100 100 re f\n"), pdf.NewPDFDict(), ctx)
	if !hasCheck(ctx, pdf.Checks.Colour.DeviceColourContentStream) {
		t.Error("expected DeviceColourContentStream for a fill with no colour set (default DeviceGray)")
	}

	ctx2 := &ValidationContext{Spec: a1bSpec}
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
	cs.Entries.Set("Custom", pdf.PDFName{Value: "DeviceCMYK"})
	resources.Entries.Set("ColorSpace", cs)
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
	pattern.Entries.Set("PatternType", pdf.PDFInteger(1))
	pattern.SetStream([]byte("ZzBadOp\n"))
	ctx := &ValidationContext{Spec: a1bSpec}
	validateContentStreams(pattern, ctx)
	if !hasCheck(ctx, pdf.Checks.Colour.UndefinedOperator) {
		t.Error("expected the tiling pattern's content to be scanned")
	}

	// Unreachable Form XObject: skipped.
	form := pdf.NewPDFDict()
	form.Entries.Set("Subtype", pdf.PDFName{Value: "Form"})
	form.SetStream([]byte("ZzBadOp\n"))
	ctx2 := &ValidationContext{Spec: a1bSpec, ReachableXObjectPtrs: map[uintptr]bool{}} // non-nil, form's ptr absent
	validateContentStreams(form, ctx2)
	if len(ctx2.errs) != 0 {
		t.Error("an unreachable Form XObject should not be scanned")
	}

	// Type3 CharProcs: every glyph stream scanned.
	proc := pdf.NewPDFDict()
	proc.SetStream([]byte("ZzBadOp\n"))
	charProcs := pdf.NewPDFDict()
	charProcs.Entries.Set("g1", proc)
	t3 := pdf.NewPDFDict()
	t3.Entries.Set("Subtype", pdf.PDFName{Value: "Type3"})
	t3.Entries.Set("CharProcs", charProcs)
	ctx3 := &ValidationContext{Spec: a1bSpec}
	validateContentStreams(t3, ctx3)
	if !hasCheck(ctx3, pdf.Checks.Colour.UndefinedOperator) {
		t.Error("expected the Type3 glyph procedure's content to be scanned")
	}
}

func TestScanAPEntryStateSubdictionary(t *testing.T) {
	onState := pdf.NewPDFDict()
	onState.SetStream([]byte("ZzBadOp\n"))
	states := pdf.NewPDFDict()
	states.Entries.Set("On", onState)
	states.Entries.Set("Off", pdf.NewPDFDict()) // no stream: skipped
	// An indirect state dictionary carries the reader's synthetic ref entry,
	// which is not an appearance state.
	states.SetRef(pdf.PDFRef{ObjNum: 9})

	ctx := &ValidationContext{Spec: a1bSpec}
	scanAPEntry(states, ctx)
	if !hasCheck(ctx, pdf.Checks.Colour.UndefinedOperator) {
		t.Error("expected the 'On' appearance-state stream to be scanned")
	}
}

// TestFormXObjectWithOwnResourcesLosesPageDefaultRGB verifies that a form
// XObject with its own /Resources is checked against that dictionary, so the
// page's /DefaultRGB does not excuse DeviceRGB used inside the form.
func TestFormXObjectWithOwnResourcesLosesPageDefaultRGB(t *testing.T) {
	content, err := writer.WriteContentStream([]writer.ContentOp{
		{Op: "rg", Operands: []pdf.PDFValue{pdf.PDFReal(1), pdf.PDFReal(0), pdf.PDFReal(0)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	form := pdf.NewPDFDict()
	form.Entries.Set("Subtype", pdf.PDFName{Value: "Form"})
	form.Entries.Set("Resources", pdf.NewPDFDict()) // its own, and empty
	form.SetStream(content)

	xobjects := pdf.NewPDFDict()
	xobjects.Entries.Set("Fx0", form)
	cs := pdf.NewPDFDict()
	cs.Entries.Set("DefaultRGB", pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}})
	pageRes := pdf.NewPDFDict()
	pageRes.Entries.Set("ColorSpace", cs)
	pageRes.Entries.Set("XObject", xobjects)

	page := pdf.NewPDFDict()
	page.Entries.Set("Type", pdf.PDFName{Value: "Page"})
	page.Entries.Set("Resources", pageRes)

	pages := pdf.NewPDFDict()
	pages.Entries.Set("Type", pdf.PDFName{Value: "Pages"})
	pages.Entries.Set("Kids", pdf.PDFArray{page})
	pages.Entries.Set("Count", pdf.PDFInteger(1))
	catalog := pdf.NewPDFDict()
	catalog.Entries.Set("Type", pdf.PDFName{Value: "Catalog"})
	catalog.Entries.Set("Pages", pages)
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", catalog)
	trailer.Entries.Set("Size", pdf.PDFInteger(5))

	ctx := &ValidationContext{Spec: a1bSpec, hasOutputIntent: true, cmykCovered: true}
	verifyDocument(trailer, ctx)

	if !hasCheck(ctx, pdf.Checks.Colour.DeviceColourContentStream) {
		t.Error("expected DeviceColourContentStream for rg inside a form with its own resources")
	}
}

// TestPageContentFormXObjectStillInheritsPageDefaultRGB confirms the existing
// behaviour: a Form XObject invoked from page content by the Do operator is
// excused by the page's /DefaultRGB via the ctx.resourceScope fallback.
func TestPageContentFormXObjectStillInheritsPageDefaultRGB(t *testing.T) {
	// Form XObject with DeviceRGB usage, no own resources.
	fContent, err := writer.WriteContentStream([]writer.ContentOp{
		{Op: "rg", Operands: []pdf.PDFValue{pdf.PDFReal(1), pdf.PDFReal(0), pdf.PDFReal(0)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// scanContentDict is called with the XObject's own (empty) resources and the
	// page resources as ctx.resourceScope — reportContentColour should not fire.
	xobj := pdf.NewPDFDict()
	xobj.SetStream(fContent)

	cs := pdf.NewPDFDict()
	cs.Entries.Set("DefaultRGB", pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}})
	pageRes := pdf.NewPDFDict()
	pageRes.Entries.Set("ColorSpace", cs)

	ctx := &ValidationContext{Spec: a1bSpec, hasOutputIntent: true, cmykCovered: true}
	ctx.resourceScope = pageRes

	scanContentDict(xobj, pdf.NewPDFDict(), ctx)

	for _, iss := range ctx.Issues() {
		if iss.Check() == pdf.Checks.Colour.DeviceColourContentStream {
			t.Errorf("page-content Form XObject should be excused by page DefaultRGB: %v", iss.Error())
		}
	}
}

// TestScanContentDictPredictorEncoded covers a content stream carrying a
// PNG predictor. The predictor must be undone for content streams as well as
// for xref and object streams, so that the page's violations are found.
func TestScanContentDictPredictorEncoded(t *testing.T) {
	content, err := writer.WriteContentStream([]writer.ContentOp{
		{Op: "rg", Operands: []pdf.PDFValue{pdf.PDFReal(1), pdf.PDFReal(0), pdf.PDFReal(0)}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wrap each row in a PNG "None" filter tag, then Flate it, exactly as a
	// producer emitting /Predictor 12 would.
	const columns = 8
	var predicted []byte
	for i := 0; i < len(content); i += columns {
		predicted = append(predicted, 0)
		predicted = append(predicted, content[i:min(i+columns, len(content))]...)
	}
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(predicted); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	stream := pdf.NewPDFDict()
	stream.SetStream(buf.Bytes())
	stream.Entries.Set("Filter", pdf.PDFName{Value: "FlateDecode"})
	stream.Entries.Set("DecodeParms", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Predictor": pdf.PDFInteger(12), "Columns": pdf.PDFInteger(columns), "Colors": pdf.PDFInteger(1),
	})})

	ctx := &ValidationContext{Spec: a1bSpec}
	scanContentDict(stream, pdf.NewPDFDict(), ctx)
	if !hasCheck(ctx, pdf.Checks.Colour.DeviceColourContentStream) {
		t.Error("predictor-encoded content stream: expected the rg device-colour violation to be reported")
	}
}

// TestInlineImageParamsIgnoreNonNameKeys checks that a malformed BI parameter
// list whose key slot is not a name is skipped instead of misread.
func TestInlineImageParamsIgnoreNonNameKeys(t *testing.T) {
	params := []pdf.PDFValue{pdf.PDFInteger(1), pdf.PDFName{Value: "DeviceRGB"}, pdf.PDFInteger(2), pdf.PDFBoolean(true)}
	ctx := &ValidationContext{Spec: a1bSpec, hasOutputIntent: true}
	checkInlineImageColour(pdf.PDFDict{}, params, pdf.NewPDFDict(), ctx)
	checkInlineImageOther(pdf.PDFDict{}, params, ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("non-name keys produced issues: %v", ctx.Issues())
	}
}

// TestScanAnnotAppearancesSkipsNonDictAnnotations checks that a non-dictionary
// /Annots entry is skipped while the annotations after it are still scanned.
func TestScanAnnotAppearancesSkipsNonDictAnnotations(t *testing.T) {
	page, _ := buildAPWithRGB(t)
	annots := page.Entries.Get("Annots").(pdf.PDFArray)
	page.Entries.Set("Annots", append(pdf.PDFArray{pdf.PDFInteger(7)}, annots...))

	ctx := &ValidationContext{Spec: a1bSpec, hasOutputIntent: true, cmykCovered: true}
	scanAnnotAppearances(page, ctx)
	if !hasCheck(ctx, pdf.Checks.Colour.DeviceColourContentStream) {
		t.Error("the annotation after a non-dictionary entry was not scanned")
	}
}

// TestScanContentColourSpaceOperatorUsesNamedModel pins that cs/CS with a name
// that resolves to a device model is held to the same OutputIntent rule as the
// direct colour operators, and that an unresolvable name is not reported.
func TestScanContentColourSpaceOperatorUsesNamedModel(t *testing.T) {
	ctx := &ValidationContext{Spec: a1bSpec}
	scanRaw([]byte("/DeviceRGB cs\n"), pdf.NewPDFDict(), ctx)
	if !hasCheck(ctx, pdf.Checks.Colour.DeviceColourContentStream) {
		t.Error("/DeviceRGB cs without an OutputIntent was not reported")
	}

	covered := &ValidationContext{Spec: a1bSpec, hasOutputIntent: true, rgbCovered: true}
	scanRaw([]byte("/DeviceRGB cs\n"), pdf.NewPDFDict(), covered)
	if hasCheck(covered, pdf.Checks.Colour.DeviceColourContentStream) {
		t.Error("/DeviceRGB cs covered by an RGB OutputIntent was reported")
	}

	unknown := &ValidationContext{Spec: a1bSpec}
	scanRaw([]byte("/Unresolvable CS\n"), pdf.NewPDFDict(), unknown)
	if hasCheck(unknown, pdf.Checks.Colour.DeviceColourContentStream) {
		t.Error("an unresolvable colour space name was reported")
	}
}

// TestContentStreamReportsEachCheckOnce verifies that a violation repeated
// throughout a content stream is reported once, at its first occurrence, with
// a count of the rest, and that distinct checks are reported separately in the
// order they first occur.
func TestContentStreamReportsEachCheckOnce(t *testing.T) {
	var content strings.Builder
	for i := range 1000 {
		fmt.Fprintf(&content, "%d.5 0 m\n", 40000+i) // RealOutOfRange
	}
	content.WriteString("ZzA\nZzB\n") // UndefinedOperator, twice
	ctx := &ValidationContext{Spec: a1bSpec, cmykCovered: true, rgbCovered: true, grayCovered: true, hasOutputIntent: true}
	scanRaw([]byte(content.String()), pdf.NewPDFDict(), ctx)

	type finding struct {
		check pdf.Check
		msg   string
	}
	var got []finding
	for _, e := range ctx.errs {
		got = append(got, finding{e.Check(), strings.Join(e.Messages(), "; ")})
	}
	want := []finding{
		{pdf.Checks.Structure.RealOutOfRange, "real number in content stream out of range: 40000.5 (and 999 more in this content stream)"},
		{pdf.Checks.Colour.UndefinedOperator, `undefined content operator "ZzA" (and 1 more in this content stream)`},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d issues %v, want %v", len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("issue %d = %v, want %v", i, got[i], want[i])
		}
	}

	// A violation that occurs once keeps its message as it was.
	once := &ValidationContext{Spec: a1bSpec}
	scanRaw([]byte("ZzA\n"), pdf.NewPDFDict(), once)
	if len(once.errs) != 1 || once.errs[0].Messages()[0] != `undefined content operator "ZzA"` {
		t.Errorf("single violation = %v", once.errs)
	}
}
