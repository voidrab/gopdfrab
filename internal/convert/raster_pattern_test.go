package convert

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
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte(content)},
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

// TestFillWithShadingPattern: a path filled through a Pattern colour space
// takes the shading's colours, and only inside the path.
func TestFillWithShadingPattern(t *testing.T) {
	img, drops := renderPattern(t, "/Pattern cs /P1 scn 5 5 10 10 re f", axialPattern())
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	left, right := nrgbaAt(t, img, 6, 10), nrgbaAt(t, img, 14, 10)
	if left.R <= right.R || left.B >= right.B {
		t.Errorf("no gradient across the fill: left %v, right %v", left, right)
	}
	// The fill must not paint the flat black a Pattern space used to leave.
	if left.R < 100 {
		t.Errorf("fill at (6,10) = %v, want the shading's red end", left)
	}
	assertUnpainted(t, img, 2, 2, "outside the filled path")
	assertUnpainted(t, img, 17, 17, "outside the filled path")
}

// TestStrokeWithShadingPattern: SCN selects a stroke pattern, which paints
// along the stroked path rather than falling back to a flat colour.
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

// TestFillWithMeshShadingPattern: a mesh pattern has no colour field to
// sample, so it paints the mesh over the path's bounds instead.
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

// TestTilingPatternReportsDrop: a tiling pattern is not rendered, and must be
// reported rather than painting the flat black a Pattern cs leaves behind.
func TestTilingPatternReportsDrop(t *testing.T) {
	pattern := pdf.PDFDict{
		Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"PatternType": pdf.PDFInteger(1),
			"PaintType":   pdf.PDFInteger(1),
			"TilingType":  pdf.PDFInteger(1),
			"BBox":        numArray(0, 0, 4, 4),
			"XStep":       pdf.PDFInteger(4),
			"YStep":       pdf.PDFInteger(4),
		}),
		HasStream: true,
		RawStream: []byte("1 0 0 rg 0 0 4 4 re f"),
	}

	img, drops := renderPattern(t, "/Pattern cs /P1 scn 5 5 10 10 re f", pattern)
	if !hasDrop(drops, dropTilingPattern) {
		t.Errorf("drops = %v, want %q", drops, dropTilingPattern)
	}
	assertUnpainted(t, img, 10, 10, "tiling pattern fill")
}

// TestUnusablePatternReportsDrop: a pattern name that resolves to nothing, or
// to a shading the rasterizer cannot use, paints nothing and says so.
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
			if !hasDrop(drops, dropShading) {
				t.Errorf("drops = %v, want %q", drops, dropShading)
			}
			assertUnpainted(t, img, 10, 10, "unusable pattern fill")
		})
	}
}

// TestPatternClearedByPlainColour: selecting an ordinary colour after a
// pattern must drop the pattern, not keep shading later fills.
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

// TestPatternRestoredByGraphicsState: q/Q saves and restores the selected
// pattern along with the rest of the colour state.
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
