package convert

import (
	"image"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func numArray(v ...float64) pdf.PDFArray {
	arr := make(pdf.PDFArray, len(v))
	for i, f := range v {
		arr[i] = pdf.PDFReal(f)
	}
	return arr
}

// expFunction builds a Type 2 exponential function interpolating c0 -> c1.
func expFunction(c0, c1 []float64) pdf.PDFDict {
	return pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"FunctionType": pdf.PDFInteger(2),
		"Domain":       numArray(0, 1),
		"C0":           numArray(c0...),
		"C1":           numArray(c1...),
		"N":            pdf.PDFInteger(1),
	}}
}

// renderShading paints a shading over a 20x20 page via the sh operator.
func renderShading(t *testing.T, sh pdf.PDFValue) (*image.RGBA, []string) {
	t.Helper()
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte("q /Sh1 sh Q")},
	}}
	resources := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Shading": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Sh1": sh}},
	}}
	img, drops, err := RenderPage(page, resources, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	return img, drops
}

// assertUnpainted checks a pixel still holds the canvas's opaque white, i.e.
// the shading painted nothing there.
func assertUnpainted(t *testing.T, img *image.RGBA, x, y int, where string) {
	t.Helper()
	if got := nrgbaAt(t, img, x, y); got.R != 255 || got.G != 255 || got.B != 255 {
		t.Errorf("%s at (%d,%d) = %v, want untouched white", where, x, y, got)
	}
}

// TestRenderAxialShading: a red-to-blue axial gradient across the page paints
// red at the left edge, blue at the right, and a blend between.
func TestRenderAxialShading(t *testing.T) {
	sh := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"ShadingType": pdf.PDFInteger(2),
		"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
		"Coords":      numArray(0, 0, 20, 0),
		"Function":    expFunction([]float64{1, 0, 0}, []float64{0, 0, 1}),
		"Extend":      pdf.PDFArray{pdf.PDFBoolean(true), pdf.PDFBoolean(true)},
	}}
	img, drops := renderShading(t, sh)
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}

	left, mid, right := nrgbaAt(t, img, 1, 10), nrgbaAt(t, img, 10, 10), nrgbaAt(t, img, 18, 10)
	if left.R < 200 || left.B > 60 {
		t.Errorf("left edge = %v, want red-dominant", left)
	}
	if right.B < 200 || right.R > 60 {
		t.Errorf("right edge = %v, want blue-dominant", right)
	}
	if mid.R >= left.R || mid.R <= right.R {
		t.Errorf("midpoint red %d not between left %d and right %d", mid.R, left.R, right.R)
	}
}

// TestRenderAxialShadingWithoutExtend: with /Extend absent the shading paints
// only between its axis endpoints.
func TestRenderAxialShadingWithoutExtend(t *testing.T) {
	sh := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"ShadingType": pdf.PDFInteger(2),
		"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
		// The axis spans only the middle of the page.
		"Coords":   numArray(8, 0, 12, 0),
		"Function": expFunction([]float64{1, 0, 0}, []float64{1, 0, 0}),
	}}
	img, drops := renderShading(t, sh)
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	assertUnpainted(t, img, 2, 10, "outside the unextended axis")
	if got := nrgbaAt(t, img, 10, 10); got.R != 255 || got.G != 0 {
		t.Errorf("inside the axis = %v, want red", got)
	}
}

// TestRenderRadialShading: a radial shading centred on the page paints its
// inner colour at the centre and nothing beyond the outer circle.
func TestRenderRadialShading(t *testing.T) {
	sh := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"ShadingType": pdf.PDFInteger(3),
		"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
		"Coords":      numArray(10, 10, 0, 10, 10, 8),
		"Function":    expFunction([]float64{0, 1, 0}, []float64{0, 0, 1}),
	}}
	img, drops := renderShading(t, sh)
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	if got := nrgbaAt(t, img, 10, 10); got.G < 200 {
		t.Errorf("centre = %v, want the inner green", got)
	}
	if got := nrgbaAt(t, img, 10, 4); got.B < 150 {
		t.Errorf("near the outer circle = %v, want the outer blue", got)
	}
	assertUnpainted(t, img, 0, 0, "corner beyond the outer circle")
}

// TestRenderFunctionBasedShading: a type 1 shading maps device space through
// the inverse of /Matrix and paints only inside /Domain.
func TestRenderFunctionBasedShading(t *testing.T) {
	sh := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"ShadingType": pdf.PDFInteger(1),
		"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
		"Domain":      numArray(0, 1, 0, 1),
		// Maps the unit domain onto the lower-left 10x10 of the page.
		"Matrix":   numArray(10, 0, 0, 10, 0, 0),
		"Function": expFunction([]float64{1, 0, 0}, []float64{0, 0, 1}),
	}}
	img, drops := renderShading(t, sh)
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	// Device y is flipped, so the domain occupies the bottom-left on screen.
	if got := nrgbaAt(t, img, 2, 18); got.R < 180 {
		t.Errorf("domain x near 0 = %v, want red-dominant", got)
	}
	if got := nrgbaAt(t, img, 8, 18); got.B < 180 {
		t.Errorf("domain x near 1 = %v, want blue-dominant", got)
	}
	assertUnpainted(t, img, 15, 5, "outside the mapped domain")
}

// TestRenderShadingBBoxClips: a /BBox restricts where the shading paints.
func TestRenderShadingBBoxClips(t *testing.T) {
	sh := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"ShadingType": pdf.PDFInteger(2),
		"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
		"Coords":      numArray(0, 0, 20, 0),
		"Function":    expFunction([]float64{1, 0, 0}, []float64{1, 0, 0}),
		"Extend":      pdf.PDFArray{pdf.PDFBoolean(true), pdf.PDFBoolean(true)},
		"BBox":        numArray(0, 0, 10, 20),
	}}
	img, _ := renderShading(t, sh)
	if got := nrgbaAt(t, img, 5, 10); got.R != 255 || got.G != 0 {
		t.Errorf("inside BBox = %v, want red", got)
	}
	assertUnpainted(t, img, 15, 10, "outside BBox")
}

// TestRenderShadingMalformedDrops: a shading the rasterizer cannot use stays
// loud rather than silently painting nothing.
func TestRenderShadingMalformedDrops(t *testing.T) {
	tests := []struct {
		name string
		sh   pdf.PDFValue
	}{
		{"missing entry", nil},
		{"unknown type", pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"ShadingType": pdf.PDFInteger(9),
			"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
		}}},
		{"axial without Coords", pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"ShadingType": pdf.PDFInteger(2),
			"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
			"Function":    expFunction([]float64{1, 0, 0}, []float64{0, 0, 1}),
		}}},
		{"axial without Function", pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"ShadingType": pdf.PDFInteger(2),
			"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
			"Coords":      numArray(0, 0, 20, 0),
		}}},
		{"no colour space", pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"ShadingType": pdf.PDFInteger(2),
			"Coords":      numArray(0, 0, 20, 0),
			"Function":    expFunction([]float64{1, 0, 0}, []float64{0, 0, 1}),
		}}},
		{"function-based with a singular Matrix", pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"ShadingType": pdf.PDFInteger(1),
			"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
			"Matrix":      numArray(0, 0, 0, 0, 0, 0),
			"Function":    expFunction([]float64{1, 0, 0}, []float64{0, 0, 1}),
		}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, drops := renderShading(t, tc.sh)
			if !hasDrop(drops, dropShading) {
				t.Errorf("drops = %v, want %q", drops, dropShading)
			}
		})
	}
}

// TestShadingFuncArrayFanOut: a /Function array of n single-output functions
// is equivalent to one function with n outputs.
func TestShadingFuncArrayFanOut(t *testing.T) {
	one := func(c0, c1 float64) pdf.PDFDict { return expFunction([]float64{c0}, []float64{c1}) }

	sf := parseShadingFunc(pdf.PDFArray{one(1, 0), one(0, 1), one(0, 0)})
	if !sf.valid() {
		t.Fatal("parseShadingFunc rejected a valid function array")
	}
	got := sf.eval([]float64{0})
	want := []float64{1, 0, 0}
	if len(got) != len(want) {
		t.Fatalf("eval returned %d components, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("component %d = %v, want %v", i, got[i], want[i])
		}
	}

	if parseShadingFunc(pdf.PDFArray{one(0, 1), pdf.PDFInteger(3)}).valid() {
		t.Error("an array holding a non-function should not parse")
	}
	if parseShadingFunc(nil).valid() {
		t.Error("a missing /Function should not parse")
	}
}
