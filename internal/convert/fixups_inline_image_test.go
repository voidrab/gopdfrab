package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
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

	params, _, ok := inlineImageRawOperand(newOp.Operands)
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
	if _, _, ok := inlineImageRawOperand(nil); ok {
		t.Error("inlineImageRawOperand(nil) ok = true, want false")
	}
	notRaw := []pdf.PDFValue{pdf.PDFName{Value: "W"}, pdf.PDFInteger(1)}
	if _, _, ok := inlineImageRawOperand(notRaw); ok {
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
		pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Ignored": pdf.PDFInteger(1)}},
		pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Predictor": pdf.PDFInteger(12)}},
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
	parms := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Predictor": pdf.PDFInteger(15), "Columns": pdf.PDFInteger(4), "Colors": pdf.PDFInteger(1),
	}}
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
	parms := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Predictor": pdf.PDFInteger(3)}}
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
	params, _, rawOk := inlineImageRawOperand(fixed)
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

	params, _, ok := inlineImageRawOperand(newOp.Operands)
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
