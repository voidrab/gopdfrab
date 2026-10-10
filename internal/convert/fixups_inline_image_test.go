package convert

import (
	"bytes"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// TestInlineImageFixersClearViolations runs the inline-image fixers
// end-to-end (Convert) on real fixtures, confirming each targeted check is
// gone after the full write+reverify round trip.
func TestInlineImageFixersClearViolations(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		check pdf.Check
	}{
		{
			"ImageInterpolate",
			"../../tests/veraPDF/PDF_A-1b/6.2 Graphics/6.2.4 Images/veraPDF test suite 6-2-4-t03-fail-a.pdf",
			pdf.Checks.Image.ImageInterpolate,
		},
		{
			"InlineImageLZWFilter",
			"../../tests/Isartor/PDFA-1b/6.1 File structure/6.1.10 Filters/isartor-6-1-10-t01-fail-b.pdf",
			pdf.Checks.Structure.InlineImageLZWFilter,
		},
		{
			"InlineImageRenderingIntent",
			"../../tests/veraPDF/PDF_A-1b/6.2 Graphics/6.2.9 Rendering intents/veraPDF test suite 6-2-9-t01-fail-a.pdf",
			pdf.Checks.Colour.RenderingIntent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr, err := Convert(tt.path, pdf.PDFA1B, Options{})
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			for _, iss := range cr.Residual() {
				if iss.Check() == tt.check {
					t.Errorf("check %s still present after conversion: %v", tt.check.Name(), iss)
				}
			}
		})
	}
}

// buildTestInlineImageOp scans a single hand-built "BI...EI" content stream
// and returns its INLINEIMAGE op's operands, for unit-testing the per-op
// rewriters in isolation without a real fixture.
func buildTestInlineImageOp(t *testing.T, src string) []pdf.PDFValue {
	t.Helper()
	var operands []pdf.PDFValue
	pdf.NewContentScanner([]byte(src)).Scan(func(op string, ops []pdf.PDFValue) {
		if op == "INLINEIMAGE" {
			operands = ops
		}
	})
	if operands == nil {
		t.Fatalf("scanning %q produced no INLINEIMAGE op", src)
	}
	return operands
}

func TestFixInlineImageInterpolateFlipsTrueToFalse(t *testing.T) {
	operands := buildTestInlineImageOp(t, "BI /W 1 /H 1 /BPC 8 /CS /G /I true ID \x00 EI\n")

	var changed bool
	newOp, keep := fixInlineImageInterpolate("INLINEIMAGE", operands, &changed)
	if !keep {
		t.Fatalf("fixInlineImageInterpolate dropped the op")
	}
	if !changed {
		t.Fatalf("changed = false, want true (the op has /I true)")
	}

	params, _, ok := pdf.InlineImageRawOperand(newOp.Operands)
	if !ok {
		t.Fatalf("rewritten op carries no pdf.InlineImageRaw operand")
	}
	found := false
	for i := 0; i+1 < len(params); i += 2 {
		key, ok := params[i].(pdf.PDFName)
		if !ok || key.Value != "I" {
			continue
		}
		found = true
		if b, ok := params[i+1].(pdf.PDFBoolean); !ok || bool(b) {
			t.Errorf("/I = %#v, want false", params[i+1])
		}
	}
	if !found {
		t.Fatalf("rewritten params lost the /I key: %#v", params)
	}
}

func TestFixInlineImageInterpolateNoOpWhenAlreadyFalse(t *testing.T) {
	operands := buildTestInlineImageOp(t, "BI /W 1 /H 1 /BPC 8 /CS /G /I false ID \x00 EI\n")

	var changed bool
	_, keep := fixInlineImageInterpolate("INLINEIMAGE", operands, &changed)
	if !keep {
		t.Fatalf("fixInlineImageInterpolate dropped the op")
	}
	if changed {
		t.Errorf("changed = true, want false (the op already has /I false)")
	}
}

// TestInlineImageRawOperandEdgeCases covers the two ok=false paths: no
// operands at all, and operands whose last entry isn't a pdf.InlineImageRaw.
func TestInlineImageRawOperandEdgeCases(t *testing.T) {
	if _, _, ok := pdf.InlineImageRawOperand(nil); ok {
		t.Error("inlineImageRawOperand(nil) ok = true, want false")
	}
	notRaw := []pdf.PDFValue{pdf.PDFName{Value: "W"}, pdf.PDFInteger(1)}
	if _, _, ok := pdf.InlineImageRawOperand(notRaw); ok {
		t.Error("inlineImageRawOperand(no trailing raw) ok = true, want false")
	}
}

func TestHasInlineImageKey(t *testing.T) {
	params := []pdf.PDFValue{pdf.PDFName{Value: "W"}, pdf.PDFInteger(1), pdf.PDFName{Value: "H"}, pdf.PDFInteger(2)}
	if !hasInlineImageKey(params, "H") {
		t.Error("hasInlineImageKey(H) = false, want true")
	}
	if hasInlineImageKey(params, "BPC") {
		t.Error("hasInlineImageKey(BPC) = true, want false (key absent)")
	}
}

// TestInlineImageDecodeParms covers the absent, single-dict (already exercised
// elsewhere), and array-of-dicts forms, mirroring streamDecodeParms.
func TestInlineImageDecodeParms(t *testing.T) {
	if got := inlineImageDecodeParms(nil); got.Entries != nil {
		t.Errorf("inlineImageDecodeParms(nil) = %#v, want zero-value dict", got)
	}

	dp := pdf.PDFArray{
		pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Ignored": pdf.PDFInteger(1)})},
		pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Predictor": pdf.PDFInteger(12)})},
	}
	params := []pdf.PDFValue{pdf.PDFName{Value: "DecodeParms"}, dp}
	got := inlineImageDecodeParms(params)
	if pdf.DictInt(got, "Predictor", 1) != 12 {
		t.Errorf("inlineImageDecodeParms(array form) = %#v, want the last dict entry (Predictor=12)", got)
	}
}

// TestInlineImagePredictorNoPredictorAndPNG covers the predictor==1
// (unchanged data) and predictor>=10 (PNG) branches of the shared predictor
// helper as the inline-image path drives it; TIFF (predictor==2) is already
// exercised by TestFixInlineImageLZWUndoesPredictor.
func TestInlineImagePredictorNoPredictorAndPNG(t *testing.T) {
	data := []byte{1, 2, 3, 4}
	got, err := pdf.UndoStreamPredictor(data, inlineImageDecodeParms(nil), pdf.DecodeOptions{})
	if err != nil || string(got) != string(data) {
		t.Errorf("UndoStreamPredictor(no params) = (%v, %v), want (%v, nil)", got, err, data)
	}

	plaintext := []byte{10, 20, 30, 40}
	predicted := append([]byte{0}, plaintext...) // one row, PNG filter type 0 (None)
	parms := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Predictor": pdf.PDFInteger(15), "Columns": pdf.PDFInteger(4), "Colors": pdf.PDFInteger(1),
	})}
	params := []pdf.PDFValue{pdf.PDFName{Value: "DP"}, parms}
	got, err = pdf.UndoStreamPredictor(predicted, inlineImageDecodeParms(params), pdf.DecodeOptions{})
	if err != nil {
		t.Fatalf("UndoStreamPredictor (PNG): %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("UndoStreamPredictor (PNG) = %v, want %v", got, plaintext)
	}
}

// TestInlineImagePredictorUnsupported covers the default (neither 1, 2, nor
// >= 10) error branch.
func TestInlineImagePredictorUnsupported(t *testing.T) {
	parms := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Predictor": pdf.PDFInteger(3)})}
	params := []pdf.PDFValue{pdf.PDFName{Value: "DP"}, parms}
	if _, err := pdf.UndoStreamPredictor([]byte{1, 2, 3, 4}, inlineImageDecodeParms(params), pdf.DecodeOptions{}); err == nil {
		t.Error("UndoStreamPredictor with predictor=3 = nil error, want an error")
	}
}

// TestFixInlineImageRenderingIntentFlipsDisallowedIntent drives
// fixInlineImageRenderingIntent directly, otherwise only exercised
// indirectly via corpus fixtures in TestInlineImageFixersClearViolations.
func TestFixInlineImageRenderingIntentFlipsDisallowedIntent(t *testing.T) {
	operands := buildTestInlineImageOp(t, "BI /W 1 /H 1 /BPC 8 /CS /G /Intent /Bogus ID \x00 EI\n")

	fixed, ok := fixInlineImageRenderingIntent(operands)
	if !ok {
		t.Fatalf("fixInlineImageRenderingIntent ok = false, want true (/Intent /Bogus is disallowed)")
	}
	params, _, rawOk := pdf.InlineImageRawOperand(fixed)
	if !rawOk {
		t.Fatalf("rewritten op carries no pdf.InlineImageRaw operand")
	}
	found := false
	for i := 0; i+1 < len(params); i += 2 {
		key, ok := params[i].(pdf.PDFName)
		if !ok || key.Value != "Intent" {
			continue
		}
		found = true
		if name, ok := params[i+1].(pdf.PDFName); !ok || name.Value != "RelativeColorimetric" {
			t.Errorf("/Intent = %#v, want /RelativeColorimetric", params[i+1])
		}
	}
	if !found {
		t.Fatalf("rewritten params lost the /Intent key: %#v", params)
	}
}

// TestFixInlineImageRenderingIntentNoOpWhenAllowed checks an already-allowed
// intent is left untouched.
func TestFixInlineImageRenderingIntentNoOpWhenAllowed(t *testing.T) {
	operands := buildTestInlineImageOp(t, "BI /W 1 /H 1 /BPC 8 /CS /G /Intent /Saturation ID \x00 EI\n")
	if _, ok := fixInlineImageRenderingIntent(operands); ok {
		t.Error("fixInlineImageRenderingIntent ok = true, want false (/Saturation is already allowed)")
	}
}

func TestFixInlineImageLZWUndoesPredictor(t *testing.T) {
	operands := buildTestInlineImageOp(t, "BI /W 1 /H 1 /BPC 8 /CS /G /F /LZW /DP << /Predictor 2 >> ID \x00 EI\n")

	var changed bool
	newOp, keep := fixInlineImageLZW("INLINEIMAGE", operands, &changed)
	if !keep {
		t.Fatalf("fixInlineImageLZW dropped the op")
	}
	if !changed {
		t.Fatalf("changed = false, want true (the predictor should be undone, not bailed on)")
	}

	params, _, ok := pdf.InlineImageRawOperand(newOp.Operands)
	if !ok {
		t.Fatalf("rewritten op carries no pdf.InlineImageRaw operand")
	}
	if hasInlineImageKey(params, "DP") || hasInlineImageKey(params, "DecodeParms") {
		t.Errorf("predictor params survived re-encoding: %#v", params)
	}
	for i := 0; i+1 < len(params); i += 2 {
		if key, ok := params[i].(pdf.PDFName); ok && (key.Value == "F" || key.Value == "Filter") {
			if name, ok := params[i+1].(pdf.PDFName); !ok || name.Value != "Fl" {
				t.Errorf("filter = %#v, want /Fl", params[i+1])
			}
		}
	}
}

// inlineImageOperands builds an INLINEIMAGE op's operands directly from
// params, bypassing the scanner so params can carry values it never emits.
func inlineImageOperands(data []byte, params ...pdf.PDFValue) []pdf.PDFValue {
	return append(append([]pdf.PDFValue{}, params...), pdf.InlineImageRaw{Bytes: []byte("BI ID x EI"), Data: data})
}

func name(s string) pdf.PDFName { return pdf.PDFName{Value: s} }

// TestInlineImageFixersLeaveUnrecognisedShapesAlone covers each way the three
// inline-image rewriters decline: operands without the raw-image operand, and
// parameter lists that cannot be re-serialised (an indirect reference has no
// place in a content stream, so the rebuilt BI...EI cannot be written).
func TestInlineImageFixersLeaveUnrecognisedShapesAlone(t *testing.T) {
	unwritable := pdf.PDFArray{pdf.PDFRef{ObjNum: 1}}
	noRaw := []pdf.PDFValue{pdf.PDFInteger(1)}

	if _, ok := fixInlineImageRenderingIntent(noRaw); ok {
		t.Error("RenderingIntent: ok = true for operands without raw image data")
	}
	if _, ok := fixInlineImageRenderingIntent(inlineImageOperands([]byte{0},
		name("Intent"), name("Bogus"), name("X"), unwritable)); ok {
		t.Error("RenderingIntent: ok = true although the rebuilt image cannot be written")
	}

	fixers := map[string]func(string, []pdf.PDFValue, *bool) (writer.ContentOp, bool){
		"Interpolate": fixInlineImageInterpolate,
		"LZW":         fixInlineImageLZW,
	}
	for label, fix := range fixers {
		var changed bool
		op, keep := fix("INLINEIMAGE", noRaw, &changed)
		if !keep || changed || len(op.Operands) != 1 {
			t.Errorf("%s: operands without raw image data: keep=%v changed=%v op=%v", label, keep, changed, op)
		}
	}

	// Interpolate true, plus an unwritable parameter.
	var changed bool
	if op, keep := fixInlineImageInterpolate("INLINEIMAGE", inlineImageOperands([]byte{0},
		name("I"), pdf.PDFBoolean(true), name("X"), unwritable), &changed); !keep || changed || len(op.Operands) != 5 {
		t.Errorf("Interpolate with unwritable params: keep=%v changed=%v op=%v", keep, changed, op)
	}

	// LZW: unwritable params, bad LZW data, unsupported predictor, and a
	// filter value that is not a name.
	lzw := func(extra ...pdf.PDFValue) []pdf.PDFValue {
		return inlineImageOperands([]byte{0x80, 0x0B, 0x60, 0x50, 0x22, 0x0C, 0x0C, 0x85, 0x01},
			append([]pdf.PDFValue{name("F"), name("LZW")}, extra...)...)
	}
	cases := map[string][]pdf.PDFValue{
		"unwritable params":    lzw(name("X"), unwritable),
		"unsupported DP":       lzw(name("DP"), pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Predictor": pdf.PDFInteger(3)})}),
		"filter not a name":    inlineImageOperands([]byte{0}, name("F"), pdf.PDFInteger(5)),
		"undecodable LZW data": inlineImageOperands([]byte{0xFF, 0xFF, 0xFF, 0xFF}, name("F"), name("LZW")),
	}
	for label, operands := range cases {
		changed = false
		op, keep := fixInlineImageLZW("INLINEIMAGE", operands, &changed)
		if !keep || changed || len(op.Operands) != len(operands) {
			t.Errorf("LZW %s: keep=%v changed=%v len=%d want unchanged len %d", label, keep, changed, len(op.Operands), len(operands))
		}
	}
}

// inlineImageDecoded reads an inline-image op's rewritten params back as a
// stream: the filter names it now declares and the result of running that
// chain over its data. For a chain ending in an image codec, data is the
// codec's still-encoded input and image names the codec.
func inlineImageDecoded(t *testing.T, operands []pdf.PDFValue) (filters []string, data []byte, image string) {
	t.Helper()
	params, raw, ok := pdf.InlineImageRawOperand(operands)
	if !ok {
		t.Fatalf("rewritten op carries no pdf.InlineImageRaw operand")
	}
	d := pdf.PDFDict{Entries: pdf.DictOf(nil), HasStream: true, RawStream: raw.Data}
	for i := 0; i+1 < len(params); i += 2 {
		if key, ok := params[i].(pdf.PDFName); ok {
			switch key.Value {
			case "F", "Filter":
				d.Entries.Set("Filter", params[i+1])
				filters = pdf.FilterNames(params[i+1])
			case "DP", "DecodeParms":
				d.Entries.Set("DecodeParms", params[i+1])
			}
		}
	}
	s, err := pdf.DecodeStreamFull(d, pdf.DecodeOptions{})
	if err != nil {
		t.Fatalf("rewritten chain %v does not decode: %v", filters, err)
	}
	if s.IsImage() {
		image = s.Image.Name
	}
	return filters, s.Data, image
}

// TestFixInlineImageLZWArrayForms covers the /F and /Filter array spellings
// the verifier also flags: LZW alone, ahead of or behind other decoders, and
// ahead of an image codec, whose input is left encoded.
func TestFixInlineImageLZWArrayForms(t *testing.T) {
	plain := []byte("-----A---B-----A---B")
	jpeg := []byte("\xff\xd8not really a jpeg\xff\xd9")
	lzw := func(b []byte) []byte { return encodeLZW(t, b) }

	tests := []struct {
		name        string
		key         string
		chain       pdf.PDFArray
		data        []byte
		wantFilters []string
		wantData    []byte
		wantImage   string
	}{
		{"single-element array", "F", pdf.PDFArray{name("LZW")}, lzw(plain), []string{"Fl"}, plain, ""},
		{"long key and name", "Filter", pdf.PDFArray{name("LZWDecode")}, lzw(plain), []string{"Fl"}, plain, ""},
		{"LZW then AHx", "F", pdf.PDFArray{name("LZW"), name("AHx")}, lzw(encodeASCIIHex(plain)), []string{"Fl", "AHx"}, plain, ""},
		{"A85 then LZW", "F", pdf.PDFArray{name("A85"), name("LZW")}, encodeASCII85(lzw(plain)), []string{"Fl"}, plain, ""},
		{"LZW then DCT", "F", pdf.PDFArray{name("LZW"), name("DCT")}, lzw(jpeg), []string{"Fl", "DCT"}, jpeg, "DCTDecode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			operands := inlineImageOperands(tt.data, name(tt.key), tt.chain)
			var changed bool
			op, keep := fixInlineImageLZW("INLINEIMAGE", operands, &changed)
			if !keep || !changed {
				t.Fatalf("keep=%v changed=%v, want the chain repaired", keep, changed)
			}
			filters, data, image := inlineImageDecoded(t, op.Operands)
			if strings.Join(filters, ",") != strings.Join(tt.wantFilters, ",") {
				t.Errorf("filters = %v, want %v", filters, tt.wantFilters)
			}
			if string(data) != string(tt.wantData) {
				t.Errorf("decoded data = %q, want %q", data, tt.wantData)
			}
			if image != tt.wantImage {
				t.Errorf("image codec = %q, want %q", image, tt.wantImage)
			}
			params, _, _ := pdf.InlineImageRawOperand(op.Operands)
			if hasInlineImageKey(params, "DP") || hasInlineImageKey(params, "DecodeParms") {
				t.Errorf("no tail filter takes parameters, yet DP survived: %#v", params)
			}
		})
	}
}

// TestConvertClearsArrayFormInlineImageLZW converts documents whose inline
// image names LZW inside a /F array -- alone, or chained with other decoders --
// and checks the finding is repaired in place. Conversion has a last resort
// that rasterizes a page it cannot otherwise clear, which would also remove the
// finding while destroying the vector content, so the page must still carry its
// inline image.
func TestConvertClearsArrayFormInlineImageLZW(t *testing.T) {
	pixels := []byte{0x10, 0x40, 0x80, 0xF0}
	tests := []struct {
		name   string
		filter string
		data   []byte
	}{
		{"single-element array", "[/LZW]", encodeLZW(t, pixels)},
		{"LZW then AHx", "[/LZW /AHx]", encodeLZW(t, encodeASCIIHex(pixels))},
		{"A85 then LZW", "[/A85 /LZW]", encodeASCII85(encodeLZW(t, pixels))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var content bytes.Buffer
			content.WriteString("q 20 0 0 20 10 10 cm\nBI /W 2 /H 2 /BPC 8 /CS /G /F " + tt.filter + "\nID ")
			content.Write(tt.data)
			content.WriteString("\nEI Q\n")

			doc := onePageDoc(content.String())
			before, err := verify.VerifyBytes(doc, pdf.PDFA1B, nil)
			if err != nil {
				t.Fatalf("VerifyBytes: %v", err)
			}
			flagged := false
			for _, iss := range before.Issues {
				flagged = flagged || iss.Check() == pdf.Checks.Structure.InlineImageLZWFilter
			}
			if !flagged {
				t.Fatalf("input is not flagged with InlineImageLZWFilter; the fixture proves nothing")
			}

			cr, err := ConvertBytes(doc, pdf.PDFA1B, Options{})
			if err != nil {
				t.Fatalf("ConvertBytes: %v", err)
			}
			defer cr.Close()
			for _, iss := range cr.Residual() {
				if iss.Check() == pdf.Checks.Structure.InlineImageLZWFilter {
					t.Errorf("array-form LZW inline image survived conversion: %v", iss)
				}
			}

			r, err := pdf.OpenBytes(mustOutput(t, cr))
			if err != nil {
				t.Fatalf("reopen output: %v", err)
			}
			defer r.Close()
			graph, err := r.ResolveGraph()
			if err != nil {
				t.Fatalf("ResolveGraph: %v", err)
			}
			page := assertOnePageGraph(t, graph)
			stream, ok := page.Entries.Get("Contents").(pdf.PDFDict)
			if !ok {
				t.Fatalf("page /Contents = %v, want a single stream", page.Entries.Get("Contents"))
			}
			if got := contentBytesOf(t, stream); !bytes.Contains(got, []byte("BI")) {
				t.Errorf("converted page content lost its inline image (rasterized?): %q", got)
			}
		})
	}
}
