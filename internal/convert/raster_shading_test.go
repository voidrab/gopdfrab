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
	return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"FunctionType": pdf.PDFInteger(2),
		"Domain":       numArray(0, 1),
		"C0":           numArray(c0...),
		"C1":           numArray(c1...),
		"N":            pdf.PDFInteger(1),
	})}
}

// renderShading paints a shading over a 20x20 page via the sh operator.
func renderShading(t *testing.T, sh pdf.PDFValue) (*image.RGBA, []string) {
	t.Helper()
	return renderShadingContent(t, sh, "q /Sh1 sh Q")
}

// renderShadingContent is renderShading with the page's content stream chosen
// by the caller, for the graphics states a plain sh cannot reach.
func renderShadingContent(t *testing.T, sh pdf.PDFValue, content string) (*image.RGBA, []string) {
	t.Helper()
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte(content)},
	})}
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Shading": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Sh1": sh})},
		"ExtGState": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"GSNone": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"ca": pdf.PDFReal(0)})},
		})},
	})}
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
	sh := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"ShadingType": pdf.PDFInteger(2),
		"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
		"Coords":      numArray(0, 0, 20, 0),
		"Function":    expFunction([]float64{1, 0, 0}, []float64{0, 0, 1}),
		"Extend":      pdf.PDFArray{pdf.PDFBoolean(true), pdf.PDFBoolean(true)},
	})}
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
	sh := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"ShadingType": pdf.PDFInteger(2),
		"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
		// The axis spans only the middle of the page.
		"Coords":   numArray(8, 0, 12, 0),
		"Function": expFunction([]float64{1, 0, 0}, []float64{1, 0, 0}),
	})}
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
	sh := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"ShadingType": pdf.PDFInteger(3),
		"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
		"Coords":      numArray(10, 10, 0, 10, 10, 8),
		"Function":    expFunction([]float64{0, 1, 0}, []float64{0, 0, 1}),
	})}
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
	sh := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"ShadingType": pdf.PDFInteger(1),
		"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
		"Domain":      numArray(0, 1, 0, 1),
		// Maps the unit domain onto the lower-left 10x10 of the page.
		"Matrix":   numArray(10, 0, 0, 10, 0, 0),
		"Function": expFunction([]float64{1, 0, 0}, []float64{0, 0, 1}),
	})}
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
	sh := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"ShadingType": pdf.PDFInteger(2),
		"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
		"Coords":      numArray(0, 0, 20, 0),
		"Function":    expFunction([]float64{1, 0, 0}, []float64{1, 0, 0}),
		"Extend":      pdf.PDFArray{pdf.PDFBoolean(true), pdf.PDFBoolean(true)},
		"BBox":        numArray(0, 0, 10, 20),
	})}
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
		{"unknown type", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"ShadingType": pdf.PDFInteger(9),
			"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
		})}},
		{"axial without Coords", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"ShadingType": pdf.PDFInteger(2),
			"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
			"Function":    expFunction([]float64{1, 0, 0}, []float64{0, 0, 1}),
		})}},
		{"axial without Function", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"ShadingType": pdf.PDFInteger(2),
			"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
			"Coords":      numArray(0, 0, 20, 0),
		})}},
		{"no colour space", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"ShadingType": pdf.PDFInteger(2),
			"Coords":      numArray(0, 0, 20, 0),
			"Function":    expFunction([]float64{1, 0, 0}, []float64{0, 0, 1}),
		})}},
		{"function-based without Function", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"ShadingType": pdf.PDFInteger(1),
			"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
			"Matrix":      numArray(10, 0, 0, 10, 0, 0),
		})}},
		{"function-based with a singular Matrix", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"ShadingType": pdf.PDFInteger(1),
			"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
			"Matrix":      numArray(0, 0, 0, 0, 0, 0),
			"Function":    expFunction([]float64{1, 0, 0}, []float64{0, 0, 1}),
		})}},
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

// TestShadingNamedColorSpace: a /ColorSpace given as a bare name resolves
// through the resource dictionary, the same way image colour spaces do.
func TestShadingNamedColorSpace(t *testing.T) {
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"ColorSpace": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"CS1": pdf.PDFArray{pdf.PDFName{Value: "CalRGB"}, pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
				"WhitePoint": numArray(0.9505, 1, 1.089),
			})}},
		})},
	})}
	dict := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"ShadingType": pdf.PDFInteger(2),
		"ColorSpace":  pdf.PDFName{Value: "CS1"},
		"Coords":      numArray(0, 0, 20, 0),
		"Function":    expFunction([]float64{1, 0, 0}, []float64{0, 0, 1}),
	})}

	sh, ok := parseShading(dict, resources)
	if !ok {
		t.Fatal("parseShading rejected a shading with a named colour space")
	}
	if _, isName := sh.cs.(pdf.PDFName); isName {
		t.Errorf("cs = %v, want the resolved array, not the name", sh.cs)
	}

	// An unresolvable name stays as written, so ResolveColor can still apply
	// its device-space fallback rather than the shading being dropped.
	if got := resolveShadingColorSpace(dict, pdf.PDFDict{}); got != dict.Entries.Get("ColorSpace") {
		t.Errorf("unresolvable name = %v, want it passed through", got)
	}
}

// TestAxialShadingDomain: a /Domain other than the default remaps the
// parametric range the colour table is sampled over.
func TestAxialShadingDomain(t *testing.T) {
	mk := func(domain pdf.PDFValue) *shading {
		entries := pdf.DictOf(map[string]pdf.PDFValue{
			"ShadingType": pdf.PDFInteger(2),
			"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
			"Coords":      numArray(0, 0, 20, 0),
			"Function":    expFunction([]float64{1, 0, 0}, []float64{0, 0, 1}),
		})
		if domain != nil {
			entries.Set("Domain", domain)
		}
		sh, ok := parseShading(pdf.PDFDict{Entries: entries}, pdf.PDFDict{})
		if !ok {
			t.Fatal("parseShading rejected an axial shading")
		}
		return sh
	}

	full, half := mk(nil), mk(numArray(0, 0.5))
	if full.domain != [2]float64{0, 1} {
		t.Errorf("default domain = %v, want [0 1]", full.domain)
	}
	if half.domain != [2]float64{0, 0.5} {
		t.Errorf("domain = %v, want [0 0.5]", half.domain)
	}
	// Half the domain only ever reaches the midpoint colour, so its end is
	// nowhere near as blue as the full domain's.
	if half.lutAt(1)[2] >= full.lutAt(1)[2] {
		t.Errorf("half-domain end blue %v not below full-domain end %v",
			half.lutAt(1)[2], full.lutAt(1)[2])
	}

	// A malformed /Domain is ignored rather than rejected.
	if got := mk(numArray(0, 0.5, 1)).domain; got != [2]float64{0, 1} {
		t.Errorf("three-element domain = %v, want the default [0 1]", got)
	}
}

// TestClampExtend pins the /Extend contract at both ends of the domain: a
// parametric value outside [0,1] paints the end colour only where the
// corresponding /Extend flag is set.
func TestClampExtend(t *testing.T) {
	tests := []struct {
		name   string
		extend [2]bool
		s      float64
		want   float64
		wantOK bool
	}{
		{"inside", [2]bool{false, false}, 0.4, 0.4, true},
		{"below, not extended", [2]bool{false, false}, -0.5, 0, false},
		{"below, extended", [2]bool{true, false}, -0.5, 0, true},
		{"above, not extended", [2]bool{false, false}, 1.5, 0, false},
		{"above, extended", [2]bool{false, true}, 1.5, 1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sh := &shading{extend: tc.extend}
			got, ok := sh.clampExtend(tc.s)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("clampExtend(%v) = %v, %v; want %v, %v", tc.s, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestColorAtUnsupportedKind: colorAt is the sampling path for types 1-3 only.
// Mesh types never reach it, and an unset kind must paint nothing rather than
// index into an absent colour table.
func TestColorAtUnsupportedKind(t *testing.T) {
	for _, kind := range []int{0, 4, 9} {
		sh := &shading{kind: kind}
		if _, ok := sh.colorAt(1, 1); ok {
			t.Errorf("colorAt on a type %d shading returned a colour", kind)
		}
	}
}

// TestRenderAxialDegenerateAxis: an axis whose endpoints coincide has no
// gradient direction. It floods the plane with the start colour when either
// /Extend is set, and paints nothing at all when neither is.
func TestRenderAxialDegenerateAxis(t *testing.T) {
	mk := func(extend pdf.PDFValue) pdf.PDFDict {
		entries := pdf.DictOf(map[string]pdf.PDFValue{
			"ShadingType": pdf.PDFInteger(2),
			"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
			"Coords":      numArray(10, 10, 10, 10),
			"Function":    expFunction([]float64{0, 1, 0}, []float64{0, 0, 1}),
		})
		if extend != nil {
			entries.Set("Extend", extend)
		}
		return pdf.PDFDict{Entries: entries}
	}

	img, drops := renderShading(t, mk(pdf.PDFArray{pdf.PDFBoolean(true), pdf.PDFBoolean(true)}))
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	if got := nrgbaAt(t, img, 3, 3); got.G < 200 {
		t.Errorf("extended degenerate axis at (3,3) = %v, want the start green", got)
	}

	img, drops = renderShading(t, mk(nil))
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	assertUnpainted(t, img, 3, 3, "an unextended degenerate axis")
}

// TestRenderRadialLinearCase: when the two circles' radius difference matches
// their centre distance the quadratic degenerates to a linear equation, and
// the point where even that has no solution paints nothing.
func TestRenderRadialLinearCase(t *testing.T) {
	sh := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"ShadingType": pdf.PDFInteger(3),
		// dx=10, dy=0, dr=10: a = dx^2+dy^2-dr^2 = 0. x0 sits on the centre of
		// device column 0 so that column's linear coefficient vanishes too.
		"Coords":     numArray(0.5, 10, 0, 10.5, 10, 10),
		"ColorSpace": pdf.PDFName{Value: "DeviceRGB"},
		"Function":   expFunction([]float64{1, 0, 0}, []float64{0, 0, 1}),
		"Extend":     pdf.PDFArray{pdf.PDFBoolean(true), pdf.PDFBoolean(true)},
	})}
	img, drops := renderShading(t, sh)
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	// Inside the cone the linear root is solvable and paints.
	assertPainted(t, img, 12, 10, "the degenerate-quadratic radial")
	// On the x = x0 column the linear coefficient vanishes too, leaving no
	// solution: those pixels stay white.
	assertUnpainted(t, img, 0, 3, "the column where the linear term vanishes")
}

// TestRenderRadialNoIntersection: pixels no circle in the family passes
// through are left unpainted (a negative discriminant).
func TestRenderRadialNoIntersection(t *testing.T) {
	sh := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"ShadingType": pdf.PDFInteger(3),
		// Two unit circles 10 apart: the family sweeps a thin tube around y=10.
		"Coords":     numArray(5, 10, 1, 15, 10, 1),
		"ColorSpace": pdf.PDFName{Value: "DeviceRGB"},
		"Function":   expFunction([]float64{1, 0, 0}, []float64{0, 0, 1}),
	})}
	img, drops := renderShading(t, sh)
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	assertPainted(t, img, 10, 10, "the swept tube")
	assertUnpainted(t, img, 10, 1, "well outside the swept tube")
}

// TestPaintShadingDegenerateState: a shading painted under a singular CTM, a
// zero alpha, or an empty clip paints nothing and reports no drop -- there is
// no content to lose.
func TestPaintShadingDegenerateState(t *testing.T) {
	sh := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"ShadingType": pdf.PDFInteger(2),
		"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
		"Coords":      numArray(0, 0, 20, 0),
		"Function":    expFunction([]float64{1, 0, 0}, []float64{0, 0, 1}),
		"Extend":      pdf.PDFArray{pdf.PDFBoolean(true), pdf.PDFBoolean(true)},
	})}
	tests := []struct {
		name    string
		content string
	}{
		{"singular CTM", "q 0 0 0 0 0 0 cm /Sh1 sh Q"},
		{"zero alpha", "q /GSNone gs /Sh1 sh Q"},
		{"empty clip", "q 5 5 0 0 re W n /Sh1 sh Q"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			img, drops := renderShadingContent(t, sh, tc.content)
			if len(drops) != 0 {
				t.Errorf("drops = %v, want none", drops)
			}
			assertUnpainted(t, img, 10, 10, tc.name)
		})
	}
}

// TestShadingOperandDrops: an sh operator whose operand is missing or is not a
// name has nothing to paint, and the loss is reported.
func TestShadingOperandDrops(t *testing.T) {
	sh := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"ShadingType": pdf.PDFInteger(2),
		"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
		"Coords":      numArray(0, 0, 20, 0),
		"Function":    expFunction([]float64{1, 0, 0}, []float64{0, 0, 1}),
	})}
	for _, content := range []string{"q sh Q", "q 42 sh Q"} {
		t.Run(content, func(t *testing.T) {
			_, drops := renderShadingContent(t, sh, content)
			if !hasDrop(drops, dropShading) {
				t.Errorf("drops = %v, want %q", drops, dropShading)
			}
		})
	}
}
