package verify

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// buildUnembeddedTrueTypeFont returns a minimal Type/TrueType font dict with
// no FontFile2 in its descriptor (simulating a non-embedded font).
func buildUnembeddedTrueTypeFont(name string) (pdf.PDFDict, uintptr) {
	desc := pdf.NewPDFDict()
	desc.Entries["Type"] = pdf.PDFName{Value: "FontDescriptor"}
	desc.Entries["FontName"] = pdf.PDFName{Value: name}
	desc.Entries["Flags"] = pdf.PDFInteger(32)

	font := pdf.NewPDFDict()
	font.Entries["Type"] = pdf.PDFName{Value: "Font"}
	font.Entries["Subtype"] = pdf.PDFName{Value: "TrueType"}
	font.Entries["BaseFont"] = pdf.PDFName{Value: name}
	font.Entries["Encoding"] = pdf.PDFName{Value: "WinAnsiEncoding"}
	font.Entries["FirstChar"] = pdf.PDFInteger(32)
	font.Entries["LastChar"] = pdf.PDFInteger(32)
	font.Entries["Widths"] = pdf.PDFArray{pdf.PDFInteger(278)}
	font.Entries["FontDescriptor"] = desc

	ptr := pdf.ValuePointer(font.Entries)
	return font, ptr
}

// TestSimpleNotEmbedded_DrawnFontFlagged verifies that a non-embedded simple
// font present in UsedCharCodes (drawn in content) is flagged even when
// SkipUnusedSimpleFonts is true.
func TestSimpleNotEmbedded_DrawnFontFlagged(t *testing.T) {
	font, ptr := buildUnembeddedTrueTypeFont("ArialMT")

	ctx := &ValidationContext{
		SkipUnusedSimpleFonts: true,
		UsedCharCodes:         map[uintptr]map[int]bool{ptr: {32: true}},
	}
	ValidateFontDict(font, ctx)

	var got []pdf.PDFError
	for _, e := range ctx.errs {
		if e.Check() == pdf.Checks.Font.SimpleNotEmbedded {
			got = append(got, e)
		}
	}
	if len(got) == 0 {
		t.Error("expected SimpleNotEmbedded for drawn non-embedded font, got none")
	}
}

// TestSimpleNotEmbedded_UndrawnFontSkipped verifies that a non-embedded
// simple font absent from UsedCharCodes is not flagged when
// SkipUnusedSimpleFonts is true (veraPDF / PDFA_1B behaviour).
func TestSimpleNotEmbedded_UndrawnFontSkipped(t *testing.T) {
	font, _ := buildUnembeddedTrueTypeFont("ArialMT")

	ctx := &ValidationContext{
		SkipUnusedSimpleFonts: true,
		UsedCharCodes:         map[uintptr]map[int]bool{},
	}
	ValidateFontDict(font, ctx)

	for _, e := range ctx.errs {
		if e.Check() == pdf.Checks.Font.SimpleNotEmbedded {
			t.Errorf("unexpected SimpleNotEmbedded for undrawn font: %v", e)
		}
	}
}

// TestSimpleNotEmbedded_LegacyStrictness verifies that with
// SkipUnusedSimpleFonts=false (Legacy_1B) a non-embedded font is always flagged.
func TestSimpleNotEmbedded_LegacyStrictness(t *testing.T) {
	font, _ := buildUnembeddedTrueTypeFont("ArialMT")

	ctx := &ValidationContext{
		SkipUnusedSimpleFonts: false,
		UsedCharCodes:         map[uintptr]map[int]bool{},
	}
	ValidateFontDict(font, ctx)

	var got []pdf.PDFError
	for _, e := range ctx.errs {
		if e.Check() == pdf.Checks.Font.SimpleNotEmbedded {
			got = append(got, e)
		}
	}
	if len(got) == 0 {
		t.Error("expected SimpleNotEmbedded in strict mode for non-embedded font, got none")
	}
}
