package raster

import (
	"image"
	"math"
	"testing"

	"github.com/voidrab/gopdfrab/internal/content"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

func name(s string) pdf.PDFName { return pdf.PDFName{Value: s} }

func dictOf(m map[string]pdf.PDFValue) pdf.PDFDict { return pdf.PDFDict{Entries: pdf.DictOf(m)} }

// renderEffects renders src on a 10x10 page at 72 dpi with the given ExtGState
// resources and returns the colour of the pixel at the page's centre.
func renderEffects(t *testing.T, src string, states map[string]pdf.PDFValue) [3]uint8 {
	t.Helper()
	res := dictOf(map[string]pdf.PDFValue{"ExtGState": dictOf(states)})
	canvas, _, err := renderContent([]byte(src), res, [4]float64{0, 0, 10, 10}, 72, InheritedPaint{}, paperBackdrop, nil)
	if err != nil {
		t.Fatal(err)
	}
	off := canvas.PixOffset(5, 5)
	return [3]uint8{canvas.Pix[off], canvas.Pix[off+1], canvas.Pix[off+2]}
}

func near(got [3]uint8, want [3]uint8) bool {
	for i := range got {
		if math.Abs(float64(got[i])-float64(want[i])) > 2 {
			return false
		}
	}
	return true
}

func TestBlendModeRendering(t *testing.T) {
	// A half-grey backdrop under a source of (0.8, 0.2, 0.4).
	const src = "0.5 g 0 0 10 10 re f /E gs 0.8 0.2 0.4 rg 0 0 10 10 re f"
	cases := []struct {
		bm   pdf.PDFValue
		want [3]uint8
	}{
		{name("Normal"), [3]uint8{204, 51, 102}},
		{name("Compatible"), [3]uint8{204, 51, 102}},
		{name("Multiply"), [3]uint8{102, 25, 51}},
		{name("Screen"), [3]uint8{230, 153, 179}},
		{name("Darken"), [3]uint8{128, 51, 102}},
		{name("Lighten"), [3]uint8{204, 128, 128}},
		{name("Difference"), [3]uint8{76, 76, 25}},
		{pdf.PDFArray{name("Unknown"), pdf.PDFInteger(1), name("Multiply")}, [3]uint8{102, 25, 51}},
		{name("Unknown"), [3]uint8{204, 51, 102}},
	}
	for _, tc := range cases {
		got := renderEffects(t, src, map[string]pdf.PDFValue{"E": dictOf(map[string]pdf.PDFValue{"BM": tc.bm})})
		if !near(got, tc.want) {
			t.Errorf("BM %v: got %v, want %v", tc.bm, got, tc.want)
		}
	}
}

func TestBlendFunctions(t *testing.T) {
	for mode, tc := range map[string]struct{ cb, cs, want float64 }{
		"Multiply":   {0.5, 0.5, 0.25},
		"Screen":     {0.5, 0.5, 0.75},
		"Overlay":    {0.25, 0.5, 0.25},
		"Darken":     {0.2, 0.7, 0.2},
		"Lighten":    {0.2, 0.7, 0.7},
		"HardLight":  {0.5, 0.75, 0.75},
		"Difference": {0.75, 0.25, 0.5},
		"Exclusion":  {0.5, 0.5, 0.5},
	} {
		if got := blendFunctions[mode](tc.cb, tc.cs); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%s(%v, %v) = %v, want %v", mode, tc.cb, tc.cs, got, tc.want)
		}
	}
	dodge, burn, soft := blendFunctions["ColorDodge"], blendFunctions["ColorBurn"], blendFunctions["SoftLight"]
	for _, tc := range []struct {
		fn           func(float64, float64) float64
		cb, cs, want float64
	}{
		{dodge, 0, 0.5, 0},
		{dodge, 0.6, 0.5, 1},
		{dodge, 0.25, 0.5, 0.5},
		{burn, 1, 0.5, 1},
		{burn, 0.25, 0.5, 0},
		{burn, 0.75, 0.5, 0.5},
		{soft, 0.5, 0.25, 0.375},
		{soft, 0.25, 0.75, 0.25 + 0.5*0.25},
		{soft, 0.16, 0.75, 0.16 + 0.5*(((16*0.16-12)*0.16+4)*0.16-0.16)},
	} {
		if got := tc.fn(tc.cb, tc.cs); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("(%v, %v) = %v, want %v", tc.cb, tc.cs, got, tc.want)
		}
	}
}

func TestNonSeparableBlends(t *testing.T) {
	cb, cs := [3]float64{0.5, 0.5, 0.5}, [3]float64{0.9, 0.1, 0.1}
	if got := blend("Color", cb, cs); math.Abs(lum(got)-lum(cb)) > 1e-9 {
		t.Errorf("Color did not keep the backdrop's luminosity: %v", got)
	}
	if got := blend("Luminosity", cs, cb); math.Abs(lum(got)-lum(cb)) > 1e-9 {
		t.Errorf("Luminosity did not take the source's luminosity: %v", got)
	}
	if got := blend("Saturation", cb, cs); math.Abs(got[0]-0.5)+math.Abs(got[1]-0.5)+math.Abs(got[2]-0.5) > 1e-9 {
		t.Errorf("Saturation of a grey backdrop = %v, want it unchanged", got)
	}
	if got := blend("Hue", cs, cb); sat(got) > 1e-9 {
		t.Errorf("Hue of a grey source keeps saturation: %v", got)
	}
	if got := blend("Hue", cs, [3]float64{0.1, 0.9, 0.1}); got == cs {
		t.Errorf("Hue left the colour unchanged: %v", got)
	}
	// setLum brings components outside [0, 1] back into it.
	for _, c := range []([3]float64){setLum([3]float64{1, 1, 0}, 0.95), setLum([3]float64{0, 0, 1}, 0.02)} {
		for _, v := range c {
			if v < -1e-9 || v > 1+1e-9 {
				t.Errorf("setLum left the range: %v", c)
			}
		}
	}
}

// maskForm returns a soft mask dictionary whose group paints src.
func maskForm(subtype string, src string, extra map[string]pdf.PDFValue) pdf.PDFDict {
	form := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{
		"Subtype": name("Form"),
		"BBox":    pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(10), pdf.PDFInteger(10)},
	}), []byte(src))
	m := map[string]pdf.PDFValue{"S": name(subtype), "G": form}
	for k, v := range extra {
		m[k] = v
	}
	return dictOf(m)
}

func TestSoftMaskRendering(t *testing.T) {
	const paint = "/E gs 0 0 0 rg 0 0 10 10 re f"
	white, grey, black := [3]uint8{255, 255, 255}, [3]uint8{128, 128, 128}, [3]uint8{0, 0, 0}
	invert := dictOf(map[string]pdf.PDFValue{
		"FunctionType": pdf.PDFInteger(2), "Domain": pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1)},
		"N": pdf.PDFInteger(1), "C0": pdf.PDFArray{pdf.PDFInteger(1)}, "C1": pdf.PDFArray{pdf.PDFInteger(0)},
	})
	unreadable := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{"Filter": name("FlateDecode")}), []byte("x"))
	cases := []struct {
		name string
		mask pdf.PDFValue
		want [3]uint8
	}{
		{"luminosity, white group shows", maskForm("Luminosity", "1 g 0 0 10 10 re f", nil), black},
		{"luminosity, black group hides", maskForm("Luminosity", "0 g 0 0 10 10 re f", nil), white},
		{"luminosity, half", maskForm("Luminosity", "0.5 g 0 0 10 10 re f", nil), grey},
		{"luminosity, empty group on black hides", maskForm("Luminosity", "", nil), white},
		{"luminosity, white backdrop shows", maskForm("Luminosity", "", map[string]pdf.PDFValue{"BC": pdf.PDFArray{pdf.PDFInteger(1)}}), black},
		{"luminosity, RGB backdrop", maskForm("Luminosity", "", map[string]pdf.PDFValue{"BC": pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFInteger(1), pdf.PDFInteger(1)}}), black},
		{"luminosity, CMYK backdrop", maskForm("Luminosity", "", map[string]pdf.PDFValue{"BC": pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(0)}}), black},
		{"luminosity, backdrop of no known space", maskForm("Luminosity", "", map[string]pdf.PDFValue{"BC": pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFInteger(1)}}), white},
		{"alpha, painted shows", maskForm("Alpha", "0 g 0 0 10 10 re f", nil), black},
		{"alpha, unpainted hides", maskForm("Alpha", "", nil), white},
		{"transfer function inverts", maskForm("Luminosity", "0 g 0 0 10 10 re f", map[string]pdf.PDFValue{"TR": invert}), black},
		{"Identity transfer", maskForm("Luminosity", "1 g 0 0 10 10 re f", map[string]pdf.PDFValue{"TR": name("Identity")}), black},
		{"unusable transfer", maskForm("Luminosity", "1 g 0 0 10 10 re f", map[string]pdf.PDFValue{"TR": pdf.PDFInteger(1)}), black},
		{"None masks nothing", name("None"), black},
		{"no group masks nothing", dictOf(map[string]pdf.PDFValue{"S": name("Luminosity")}), black},
		{"unreadable group masks nothing", dictOf(map[string]pdf.PDFValue{"S": name("Luminosity"), "G": unreadable}), black},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderEffects(t, paint, map[string]pdf.PDFValue{"E": dictOf(map[string]pdf.PDFValue{"SMask": tc.mask})})
			if !near(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSoftMaskGroupColourSpace(t *testing.T) {
	m := maskForm("Luminosity", "", map[string]pdf.PDFValue{"BC": pdf.PDFArray{pdf.PDFInteger(1)}})
	form := m.Entries.Get("G").(pdf.PDFDict)
	form.Entries.Set("Group", dictOf(map[string]pdf.PDFValue{"CS": name("DeviceGray")}))
	got := renderEffects(t, "/E gs 0 g 0 0 10 10 re f", map[string]pdf.PDFValue{"E": dictOf(map[string]pdf.PDFValue{"SMask": m})})
	if !near(got, [3]uint8{0, 0, 0}) {
		t.Errorf("got %v, want black", got)
	}
}

// TestSoftMaskIsReusedAndScopedToTheCTM verifies that a soft mask follows the
// CTM in force at the gs operator, and that drawing with it twice is still
// masked correctly.
func TestSoftMaskIsReusedAndScopedToTheCTM(t *testing.T) {
	half := maskForm("Luminosity", "1 g 0 0 5 10 re f", nil)
	states := map[string]pdf.PDFValue{"E": dictOf(map[string]pdf.PDFValue{"SMask": half})}
	if got := renderEffects(t, "/E gs 0 g 6 0 4 10 re f", states); !near(got, [3]uint8{255, 255, 255}) {
		t.Errorf("masked side: got %v", got)
	}
	if got := renderEffects(t, "q 1 0 0 1 5 0 cm /E gs Q q 1 0 0 1 5 0 cm /E gs 0 g 0 0 5 10 re f /E gs 0 0 5 10 re f Q", states); !near(got, [3]uint8{0, 0, 0}) {
		t.Errorf("moved mask: got %v", got)
	}
}

// TestEffectsApplyToEachPaintingOperator exercises the operators that go through
// withEffects other than paths, under a mask that hides everything.
func TestEffectsApplyToEachPaintingOperator(t *testing.T) {
	hide := map[string]pdf.PDFValue{"E": dictOf(map[string]pdf.PDFValue{"SMask": maskForm("Luminosity", "", nil)})}
	for _, op := range []string{
		"(a) Tj", "(a) '", "1 1 (a) \"", "[(a)] TJ", "/S sh", "/X Do",
		"BI /W 1 /H 1 /BPC 8 /CS /G ID \x00 EI",
	} {
		if got := renderEffects(t, "/E gs "+op, hide); !near(got, [3]uint8{255, 255, 255}) {
			t.Errorf("%s under a hiding mask: got %v", op, got)
		}
	}
}

func TestMaskOutsideItsBounds(t *testing.T) {
	m := &softMask{bounds: image.Rect(0, 0, 1, 1), alpha: []uint8{255}, outside: 51}
	if got := m.at(0, 0); got != 1 {
		t.Errorf("inside = %v", got)
	}
	if got := m.at(5, 5); got != 0.2 {
		t.Errorf("outside = %v, want 0.2", got)
	}
}

func TestCompositeLayerOntoClearBackdrop(t *testing.T) {
	dst, layer := image.NewRGBA(image.Rect(0, 0, 1, 1)), image.NewRGBA(image.Rect(0, 0, 1, 1))
	storeRGBA64(layer.Pix, 0, 1, 0, 0, 0.5)
	compositeLayer(dst, layer, nil, "Multiply")
	if dst.Pix[3] < 126 || dst.Pix[3] > 129 || dst.Pix[0] != dst.Pix[3] {
		t.Errorf("a clear backdrop should take the source unblended: %v", dst.Pix[:4])
	}
}

func TestEffectBudgetDropsEffects(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 4, 4))
	r := &renderer{canvas: canvas, effectPixels: effectPixelBudget}
	gs := renderState{blendMode: "Multiply", clip: [4]float64{0, 0, 4, 4}}
	painted := false
	r.withEffects(&gs, noHint, func() {
		painted = r.canvas == canvas && gs.blendMode == ""
	})
	if !painted {
		t.Error("over budget, paint should run on the canvas without effects")
	}
	if gs.blendMode != "Multiply" {
		t.Error("the blend mode was not restored")
	}
	if !r.dropped[dropEffects] {
		t.Error("the drop was not recorded")
	}
}

func TestPathReach(t *testing.T) {
	var p pathBuilder
	gs := &renderState{GState: content.NewGState(), lineWidth: 1}
	if got := pathReach(&p, gs); !got.Empty() {
		t.Errorf("an empty path reaches %v", got)
	}
	p.rect(10, 10, 5, 5)
	got := pathReach(&p, gs)
	if !(image.Pt(10, 10).In(got) && image.Pt(14, 14).In(got)) {
		t.Errorf("reach %v misses the path", got)
	}
}

// TestFormClipWithoutBBox verifies that a form with no usable bounding box
// (/BBox) does not narrow the clip.
func TestFormClipWithoutBBox(t *testing.T) {
	clip := [4]float64{1, 2, 3, 4}
	if got := formClip(pdf.NewPDFDict(), content.IdentityMatrix, clip); got != clip {
		t.Errorf("formClip = %v, want %v", got, clip)
	}
}

func TestDeepSoftMaskIsNotRendered(t *testing.T) {
	r := &renderer{canvas: image.NewRGBA(image.Rect(0, 0, 1, 1)), depth: 13}
	gs := &renderState{GState: content.NewGState()}
	if m := r.renderSoftMask(maskForm("Alpha", "", nil), pdf.PDFDict{}, gs); m != nil {
		t.Error("rendered a mask past the depth limit")
	}
}
