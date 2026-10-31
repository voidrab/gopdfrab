package raster

import (
	"image"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// axialPattern wraps a red-to-blue axial shading across x = 0..20 as a
// PatternType 2 pattern.
func axialPattern() pdf.PDFDict {
	return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"PatternType": pdf.PDFInteger(2),
		"Shading": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"ShadingType": pdf.PDFInteger(2),
			"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
			"Coords":      numArray(0, 0, 20, 0),
			"Function":    expFunction([]float64{1, 0, 0}, []float64{0, 0, 1}),
			"Extend":      pdf.PDFArray{pdf.PDFBoolean(true), pdf.PDFBoolean(true)},
		})},
	})}
}

// renderPattern paints content over a 20x20 page with pattern as /P1.
func renderPattern(t *testing.T, content string, pattern pdf.PDFValue) (*image.RGBA, []string) {
	t.Helper()
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte(content)),
	})}
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Pattern": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"P1": pattern})},
	})}
	img, drops, err := RenderPage(page, resources, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	return img, drops
}

// TestFillWithShadingPattern verifies that a path filled through a Pattern
// colour space takes the shading's colours, and only inside the path.
func TestFillWithShadingPattern(t *testing.T) {
	img, drops := renderPattern(t, "/Pattern cs /P1 scn 5 5 10 10 re f", axialPattern())
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	left, right := nrgbaAt(t, img, 6, 10), nrgbaAt(t, img, 14, 10)
	if left.R <= right.R || left.B >= right.B {
		t.Errorf("no gradient across the fill: left %v, right %v", left, right)
	}
	// The fill must not paint the flat black that a Pattern colour space leaves
	// behind.
	if left.R < 100 {
		t.Errorf("fill at (6,10) = %v, want the shading's red end", left)
	}
	assertUnpainted(t, img, 2, 2, "outside the filled path")
	assertUnpainted(t, img, 17, 17, "outside the filled path")
}

// TestStrokeWithShadingPattern verifies that the stroke colour operator (SCN)
// selects a stroke pattern, which paints along the stroked path rather than
// falling back to a flat colour.
func TestStrokeWithShadingPattern(t *testing.T) {
	img, drops := renderPattern(t, "/Pattern CS /P1 SCN 4 w 0 10 m 20 10 l S", axialPattern())
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	left, right := nrgbaAt(t, img, 2, 10), nrgbaAt(t, img, 18, 10)
	if left.R <= right.R || left.B >= right.B {
		t.Errorf("no gradient along the stroke: left %v, right %v", left, right)
	}
	assertUnpainted(t, img, 10, 2, "away from the stroked line")
}

// TestFillWithMeshShadingPattern verifies that a mesh pattern has no colour
// field to sample, so it paints the mesh over the path's bounds instead.
func TestFillWithMeshShadingPattern(t *testing.T) {
	var data []byte
	data = append(data, vertex(0, 255, 0, 0, 255)...)
	data = append(data, vertex(255, 255, 0, 0, 255)...)
	data = append(data, vertex(0, 0, 0, 0, 255)...)
	data = append(data, vertex(255, 0, 0, 0, 255)...)
	pattern := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"PatternType": pdf.PDFInteger(2),
		"Shading":     meshShading(5, data, map[string]pdf.PDFValue{"VerticesPerRow": pdf.PDFInteger(2)}),
	})}

	img, drops := renderPattern(t, "/Pattern cs /P1 scn 5 5 10 10 re f", pattern)
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	if got := nrgbaAt(t, img, 10, 10); got.B < 200 {
		t.Errorf("mesh pattern fill = %v, want blue", got)
	}
}

// TestTilingPatternReportsDrop verifies that a tiling pattern is not rendered,
// and must be reported rather than painting the flat black that a Pattern
// colour space leaves behind.
func TestTilingPatternReportsDrop(t *testing.T) {
	pattern := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{
		"PatternType": pdf.PDFInteger(1),
		"PaintType":   pdf.PDFInteger(1),
		"TilingType":  pdf.PDFInteger(1),
		"BBox":        numArray(0, 0, 4, 4),
		"XStep":       pdf.PDFInteger(4),
		"YStep":       pdf.PDFInteger(4),
	}), []byte("1 0 0 rg 0 0 4 4 re f"))

	img, drops := renderPattern(t, "/Pattern cs /P1 scn 5 5 10 10 re f", pattern)
	if !hasDrop(drops, dropTilingPattern) {
		t.Errorf("drops = %v, want %q", drops, dropTilingPattern)
	}
	assertUnpainted(t, img, 10, 10, "tiling pattern fill")
}

// TestUnusablePatternReportsDrop verifies that a pattern name that resolves to
// nothing, or to a shading the rasterizer cannot use, paints nothing and says
// so.
func TestUnusablePatternReportsDrop(t *testing.T) {
	tests := []struct {
		name    string
		pattern pdf.PDFValue
	}{
		{"no such pattern", nil},
		{"shading pattern with an unusable shading", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"PatternType": pdf.PDFInteger(2),
			"Shading": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
				"ShadingType": pdf.PDFInteger(2),
				"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
			})},
		})}},
		{"unknown pattern type", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"PatternType": pdf.PDFInteger(7),
		})}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			img, drops := renderPattern(t, "/Pattern cs /P1 scn 5 5 10 10 re f", tc.pattern)
			if !hasDrop(drops, DropShading) {
				t.Errorf("drops = %v, want %q", drops, DropShading)
			}
			assertUnpainted(t, img, 10, 10, "unusable pattern fill")
		})
	}
}

// TestPatternClearedByPlainColour verifies that selecting an ordinary colour
// after a pattern must drop the pattern, not keep shading later fills.
func TestPatternClearedByPlainColour(t *testing.T) {
	content := "/Pattern cs /P1 scn 0 1 0 rg 5 5 10 10 re f"
	img, drops := renderPattern(t, content, axialPattern())
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	if got := nrgbaAt(t, img, 10, 10); got.R != 0 || got.G != 255 || got.B != 0 {
		t.Errorf("fill after rg = %v, want the plain green", got)
	}
}

// TestPatternKeptByOtherColourOperator verifies that setting the stroking
// colour leaves the fill pattern in place.
func TestPatternKeptByOtherColourOperator(t *testing.T) {
	content := "/Pattern cs /P1 scn 0 1 0 RG 5 5 10 10 re f"
	img, drops := renderPattern(t, content, axialPattern())
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	if got := nrgbaAt(t, img, 6, 10); got.R < 100 || got.G != 0 {
		t.Errorf("fill after RG = %v, want the pattern's red end", got)
	}
}

// TestPatternRestoredByGraphicsState verifies that q/Q saves and restores the
// selected pattern along with the rest of the colour state.
func TestPatternRestoredByGraphicsState(t *testing.T) {
	content := "/Pattern cs /P1 scn q 0 1 0 rg 0 0 4 4 re f Q 5 5 10 10 re f"
	img, drops := renderPattern(t, content, axialPattern())
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	if got := nrgbaAt(t, img, 2, 18); got.G != 255 || got.R != 0 {
		t.Errorf("fill inside q/Q = %v, want the plain green", got)
	}
	if got := nrgbaAt(t, img, 6, 10); got.R < 100 {
		t.Errorf("fill after Q = %v, want the restored pattern's red end", got)
	}
}

// shiftedAxial is axialPattern with the given /Matrix and, unless extend, no
// extension past the gradient's ends (so pixels outside are unpainted).
func shiftedAxial(matrix pdf.PDFValue, extend bool) pdf.PDFDict {
	p := axialPattern()
	if matrix != nil {
		p.Entries.Set("content.Matrix", matrix)
	}
	if !extend {
		sh := p.Entries.Get("Shading").(pdf.PDFDict)
		sh.Entries.Del("Extend")
		sh.Entries.Set("Coords", numArray(8, 0, 12, 0))
	}
	return p
}

// TestShadingPatternMatrix verifies that a pattern's /Matrix places its
// gradient relative to the page, so shifting it 10 units right slides the red
// end with it.
func TestShadingPatternMatrix(t *testing.T) {
	const content = "/Pattern cs /P1 scn 0 0 20 20 re f"
	plain, _ := renderPattern(t, content, axialPattern())
	shifted, _ := renderPattern(t, content, shiftedAxial(numArray(1, 0, 0, 1, 10, 0), true))
	if p, s := nrgbaAt(t, plain, 4, 10), nrgbaAt(t, shifted, 4, 10); s.R <= p.R {
		t.Errorf("at x=4 the shifted gradient (%v) is not redder than the unshifted one (%v)", s, p)
	}
	if s := nrgbaAt(t, shifted, 4, 10); s.R != 255 || s.B != 0 {
		t.Errorf("at x=4, left of the shifted gradient, got %v, want its extended red end", s)
	}
}

// TestShadingPatternPaintsOnlyWhereTheShadingDoes verifies that without Extend
// a gradient leaves the pixels beyond its ends alone.
func TestShadingPatternPaintsOnlyWhereTheShadingDoes(t *testing.T) {
	img, _ := renderPattern(t, "/Pattern cs /P1 scn 0 0 20 20 re f", shiftedAxial(nil, false))
	if got := nrgbaAt(t, img, 10, 10); got.R == 255 && got.G == 255 && got.B == 255 {
		t.Error("inside the gradient's span nothing was painted")
	}
	assertUnpainted(t, img, 3, 10, "before the gradient starts")
	assertUnpainted(t, img, 17, 10, "after the gradient ends")
}

// TestShadingPatternWithSingularMatrixPaintsNothing verifies that a /Matrix
// that cannot be inverted gives no way to look up a pixel's colour, for fills
// and strokes.
func TestShadingPatternWithSingularMatrixPaintsNothing(t *testing.T) {
	pattern := shiftedAxial(numArray(0, 0, 0, 0, 0, 0), true)
	for name, content := range map[string]string{
		"fill":   "/Pattern cs /P1 scn 0 0 20 20 re f",
		"stroke": "/Pattern CS /P1 SCN 4 w 0 10 m 20 10 l S",
	} {
		img, _ := renderPattern(t, content, pattern)
		if n, _, _, _, _ := ink(img); n != 0 {
			t.Errorf("%s: %d pixels painted through a singular pattern matrix", name, n)
		}
	}
}
