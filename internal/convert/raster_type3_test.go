package convert

import (
	"image"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// type3Font builds a Type 3 font mapping code 65 ("A") to a CharProc, with
// the given /FontMatrix scale and glyph advance width.
func type3Font(proc string, matrixScale, width float64, procName string) pdf.PDFDict {
	charProcs := pdf.NewPDFDict()
	if procName != "" {
		charProcs.Entries[procName] = pdf.PDFDict{HasStream: true, RawStream: []byte(proc)}
	}
	return pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type":       pdf.PDFName{Value: "Font"},
		"Subtype":    pdf.PDFName{Value: "Type3"},
		"FontMatrix": numArray(matrixScale, 0, 0, matrixScale, 0, 0),
		"FontBBox":   numArray(0, 0, 1000, 1000),
		"CharProcs":  charProcs,
		"Encoding": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Differences": pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "square"}},
		}},
		"FirstChar": pdf.PDFInteger(65),
		"LastChar":  pdf.PDFInteger(65),
		"Widths":    numArray(width),
	}}
}

// renderType3 draws content over a 20x20 page with font as /F1.
func renderType3(t *testing.T, content string, font pdf.PDFDict) (*image.RGBA, []string) {
	t.Helper()
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte(content)},
	}}
	resources := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Font": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"F1": font}},
	}}
	img, drops, err := RenderPage(page, resources, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	return img, drops
}

// TestRenderType3Glyph: a Type 3 glyph's CharProc is executed, painting in the
// fill colour in force when the text was shown.
func TestRenderType3Glyph(t *testing.T) {
	font := type3Font("0 0 750 750 re f", 0.001, 750, "square")
	img, drops := renderType3(t, "0 0 1 rg BT /F1 20 Tf 2 2 Td (A) Tj ET", font)
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	// 750 glyph units * 0.001 * 20pt = a 15pt box at user (2,2)-(17,17),
	// which on the flipped device canvas is y 3..18.
	if got := nrgbaAt(t, img, 10, 10); got.B != 255 || got.R != 0 {
		t.Errorf("inside the glyph = %v, want the blue fill colour", got)
	}
	assertUnpainted(t, img, 19, 19, "outside the glyph")
}

// TestRenderType3FontMatrix: the glyph is scaled by /FontMatrix, not by the
// 1000-unit em every other font type uses.
func TestRenderType3FontMatrix(t *testing.T) {
	// A tenth of the coordinates with ten times the matrix scale must paint
	// the same 15pt box.
	coarse := type3Font("0 0 75 75 re f", 0.01, 75, "square")
	fine := type3Font("0 0 750 750 re f", 0.001, 750, "square")

	content := "BT /F1 20 Tf 2 2 Td (A) Tj ET"
	a, _ := renderType3(t, content, coarse)
	b, _ := renderType3(t, content, fine)
	for _, p := range [][2]int{{10, 10}, {4, 16}, {16, 4}, {19, 19}} {
		if nrgbaAt(t, a, p[0], p[1]) != nrgbaAt(t, b, p[0], p[1]) {
			t.Errorf("at (%d,%d) the FontMatrix-scaled glyph differs: %v vs %v",
				p[0], p[1], nrgbaAt(t, a, p[0], p[1]), nrgbaAt(t, b, p[0], p[1]))
		}
	}
	// Guard against both renders being blank, which would pass vacuously.
	if inkFraction(a) < inkThreshold {
		t.Fatal("the glyph painted no ink; the comparison proves nothing")
	}
}

// TestRenderType3Advance: /Widths is in glyph space, so the second glyph is
// placed by the width scaled through the font matrix.
func TestRenderType3Advance(t *testing.T) {
	// An 8pt-wide box advancing 10pt leaves a 2pt gap between the two glyphs.
	font := type3Font("0 0 400 750 re f", 0.001, 500, "square")
	img, drops := renderType3(t, "BT /F1 20 Tf 0 2 Td (AA) Tj ET", font)
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	if got := nrgbaAt(t, img, 4, 10); got.R != 0 {
		t.Errorf("first glyph at x=4 = %v, want painted", got)
	}
	assertUnpainted(t, img, 9, 10, "the gap between glyphs")
	if got := nrgbaAt(t, img, 14, 10); got.R != 0 {
		t.Errorf("second glyph at x=14 = %v, want painted", got)
	}
}

// TestRenderType3Invisible: render mode 3 must not execute the CharProc, the
// same OCR-layer guarantee outline fonts already have.
func TestRenderType3Invisible(t *testing.T) {
	font := type3Font("0 0 750 750 re f", 0.001, 750, "square")
	visible, _ := renderType3(t, "BT /F1 20 Tf 0 Tr 2 2 Td (A) Tj ET", font)
	if inkFraction(visible) < inkThreshold {
		t.Fatal("mode 0 painted no ink; the test cannot distinguish invisibility")
	}
	hidden, _ := renderType3(t, "BT /F1 20 Tf 3 Tr 2 2 Td (A) Tj ET", font)
	if got := inkFraction(hidden); got >= inkThreshold {
		t.Errorf("mode 3 (invisible) painted ink fraction %.4f, want ~0", got)
	}
}

// TestRenderType3MissingCharProcDrops: a code with no glyph procedure is
// reported rather than silently leaving a hole in the text.
func TestRenderType3MissingCharProcDrops(t *testing.T) {
	tests := []struct {
		name string
		font pdf.PDFDict
	}{
		{"no CharProcs entry for the name", type3Font("0 0 750 750 re f", 0.001, 750, "")},
		{"name not in the encoding", type3Font("0 0 750 750 re f", 0.001, 750, "other")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, drops := renderType3(t, "BT /F1 20 Tf 2 2 Td (A) Tj ET", tc.font)
			if !hasDrop(drops, dropType3) {
				t.Errorf("drops = %v, want %q", drops, dropType3)
			}
		})
	}
}

// redShadingResources is a /Shading resource dict holding one flat-red axial
// shading named Sh1, used to prove which resource dict a CharProc resolved
// against: an unresolvable name would report a drop instead of painting.
func redShadingResources() pdf.PDFDict {
	return pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Shading": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Sh1": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
				"ShadingType": pdf.PDFInteger(2),
				"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
				"Coords":      numArray(0, 0, 1000, 0),
				"Function":    expFunction([]float64{1, 0, 0}, []float64{1, 0, 0}),
				"Extend":      pdf.PDFArray{pdf.PDFBoolean(true), pdf.PDFBoolean(true)},
			}},
		}},
	}}
}

// TestRenderType3GlyphUsesFontResources: a CharProc draws through the font's
// own /Resources.
func TestRenderType3GlyphUsesFontResources(t *testing.T) {
	font := type3Font("/Sh1 sh", 0.001, 750, "square")
	font.Entries["Resources"] = redShadingResources()

	img, drops := renderType3(t, "BT /F1 20 Tf 2 2 Td (A) Tj ET", font)
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	if got := nrgbaAt(t, img, 10, 10); got.R != 255 || got.G != 0 {
		t.Errorf("glyph drawn from font resources = %v, want the shading's red", got)
	}
}

// TestRenderType3GlyphFallsBackToPageResources: a font declaring no
// /Resources resolves its CharProc's names against the page's (ISO 32000-1
// 9.6.5), rather than finding nothing.
func TestRenderType3GlyphFallsBackToPageResources(t *testing.T) {
	font := type3Font("/Sh1 sh", 0.001, 750, "square")
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{HasStream: true,
			RawStream: []byte("BT /F1 20 Tf 2 2 Td (A) Tj ET")},
	}}
	resources := redShadingResources()
	resources.Entries["Font"] = pdf.PDFDict{Entries: map[string]pdf.PDFValue{"F1": font}}

	img, drops, err := RenderPage(page, resources, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	if got := nrgbaAt(t, img, 10, 10); got.R != 255 || got.G != 0 {
		t.Errorf("glyph drawn from page resources = %v, want the shading's red", got)
	}
}
