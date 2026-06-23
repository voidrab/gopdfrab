package pdfrab

import "testing"

// TestInlineImageFixersClearViolations runs the inline-image fixers
// end-to-end (Convert) on real fixtures, confirming each targeted check is
// gone after the full write+reverify round trip.
func TestInlineImageFixersClearViolations(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		check Check
	}{
		{
			"ImageInterpolate",
			"test documents/veraPDF/PDF_A-1b/6.2 Graphics/6.2.4 Images/veraPDF test suite 6-2-4-t03-fail-a.pdf",
			Checks.Image.ImageInterpolate,
		},
		{
			"InlineImageLZWFilter",
			"test documents/Isartor testsuite/PDFA-1b/6.1 File structure/6.1.10 Filters/isartor-6-1-10-t01-fail-b.pdf",
			Checks.Structure.InlineImageLZWFilter,
		},
		{
			"InlineImageRenderingIntent",
			"test documents/veraPDF/PDF_A-1b/6.2 Graphics/6.2.9 Rendering intents/veraPDF test suite 6-2-9-t01-fail-a.pdf",
			Checks.Colour.RenderingIntent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr, err := Convert(tt.path)
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			for _, iss := range cr.Residual() {
				if iss.check == tt.check {
					t.Errorf("check %s still present after conversion: %v", tt.check.Name(), iss)
				}
			}
		})
	}
}

// buildTestInlineImageOp scans a single hand-built "BI...EI" content stream
// and returns its INLINEIMAGE op's operands, for unit-testing the per-op
// rewriters in isolation without a real fixture.
func buildTestInlineImageOp(t *testing.T, src string) []PDFValue {
	t.Helper()
	var operands []PDFValue
	newContentScanner([]byte(src)).scan(func(op string, ops []PDFValue) {
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
		t.Fatalf("rewritten op carries no inlineImageRaw operand")
	}
	found := false
	for i := 0; i+1 < len(params); i += 2 {
		key, ok := params[i].(PDFName)
		if !ok || key.Value != "I" {
			continue
		}
		found = true
		if b, ok := params[i+1].(PDFBoolean); !ok || bool(b) {
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
		t.Fatalf("rewritten op carries no inlineImageRaw operand")
	}
	if hasInlineImageKey(params, "DP") || hasInlineImageKey(params, "DecodeParms") {
		t.Errorf("predictor params survived re-encoding: %#v", params)
	}
	for i := 0; i+1 < len(params); i += 2 {
		if key, ok := params[i].(PDFName); ok && (key.Value == "F" || key.Value == "Filter") {
			if name, ok := params[i+1].(PDFName); !ok || name.Value != "Fl" {
				t.Errorf("filter = %#v, want /Fl", params[i+1])
			}
		}
	}
}
