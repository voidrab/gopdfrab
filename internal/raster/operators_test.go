package raster

import (
	"bytes"
	"image"
	"image/jpeg"
	"math"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// renderSized runs content over a w x h page at 72 dpi (so one user unit is
// one pixel, y flipped) with the given resources.
func renderSized(t *testing.T, w, h float64, content string, resources pdf.PDFDict) (*image.RGBA, []string) {
	t.Helper()
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte(content)),
	})}
	canvas, drops, err := RenderPage(page, resources, [4]float64{0, 0, w, h}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	return canvas, drops
}

// render20 is renderSized over the 20x20 page most operator tests use.
func render20(t *testing.T, content string, resources pdf.PDFDict) *image.RGBA {
	t.Helper()
	canvas, _ := renderSized(t, 20, 20, content, resources)
	return canvas
}

func isWhite(t *testing.T, img *image.RGBA, x, y int) bool {
	t.Helper()
	px := nrgbaAt(t, img, x, y)
	return px.R == 255 && px.G == 255 && px.B == 255 && px.A == 255
}

func isBlack(t *testing.T, img *image.RGBA, x, y int) bool {
	t.Helper()
	px := nrgbaAt(t, img, x, y)
	return px.R == 0 && px.G == 0 && px.B == 0 && px.A == 255
}

// ink counts the pixels that are not paper white and returns their bounding
// box (all zero when there are none).
func ink(img *image.RGBA) (n, minX, minY, maxX, maxY int) {
	first := true
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			o := img.PixOffset(x, y)
			if p := img.Pix[o : o+4]; p[0] == 255 && p[1] == 255 && p[2] == 255 && p[3] == 255 {
				continue
			}
			n++
			if first {
				minX, minY, maxX, maxY, first = x, y, x, y, false
				continue
			}
			minX, maxX = min(minX, x), max(maxX, x)
			minY, maxY = min(minY, y), max(maxY, y)
		}
	}
	return
}

func resourcesWith(kind string, entries map[string]pdf.PDFValue) pdf.PDFDict {
	return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		kind: pdf.PDFDict{Entries: pdf.DictOf(entries)},
	})}
}

func TestPathOperatorsStartAtOrigin(t *testing.T) {
	// l and c with no current point start from (0,0) rather than being lost.
	line := render20(t, "0 0 0 rg 20 0 l 20 20 l f", pdf.PDFDict{})
	if !isBlack(t, line, 15, 15) || !isWhite(t, line, 5, 5) {
		t.Error("l from an implicit start did not fill the lower-right triangle")
	}
	curve := render20(t, "0 0 0 rg 20 0 20 20 20 20 c f", pdf.PDFDict{})
	if !isBlack(t, curve, 10, 15) || !isWhite(t, curve, 5, 5) {
		t.Error("c from an implicit start did not fill the region under the curve")
	}
}

func TestClipOperators(t *testing.T) {
	// An empty path leaves the clip as it was.
	open := render20(t, "W n 0 0 0 rg 0 0 20 20 re f", pdf.PDFDict{})
	if !isBlack(t, open, 1, 1) || !isBlack(t, open, 18, 18) {
		t.Error("W over an empty path restricted the clip")
	}
	// A real path restricts everything after it.
	clipped := render20(t, "5 5 10 10 re W n 0 0 0 rg 0 0 20 20 re f", pdf.PDFDict{})
	if !isBlack(t, clipped, 10, 10) || !isWhite(t, clipped, 2, 2) || !isWhite(t, clipped, 18, 18) {
		t.Error("W did not confine painting to the clip rectangle")
	}
}

func TestPaintOperatorsWithoutPathDoNothing(t *testing.T) {
	for _, op := range []string{"f", "S", "B", "n"} {
		img := render20(t, "0 0 0 rg 0 0 0 RG "+op, pdf.PDFDict{})
		if n, _, _, _, _ := ink(img); n != 0 {
			t.Errorf("%s with no path painted %d pixels", op, n)
		}
	}
}

func TestResolveOperandColorSpaceUnknownNameIsKept(t *testing.T) {
	unknown := pdf.PDFName{Value: "NoSuchSpace"}
	got := ResolveOperandColorSpace([]pdf.PDFValue{unknown}, pdf.PDFDict{})
	if !pdf.EqualPDFValue(got, unknown) {
		t.Errorf("unresolvable name = %v, want it returned as written", got)
	}
}

func TestPatternNameOperand(t *testing.T) {
	for _, tc := range []struct {
		name     string
		operands []pdf.PDFValue
		want     string
		ok       bool
	}{
		{"none", nil, "", false},
		{"numbers only", []pdf.PDFValue{pdf.PDFInteger(1), pdf.PDFReal(0.5)}, "", false},
		{"name last", []pdf.PDFValue{pdf.PDFReal(0.5), pdf.PDFName{Value: "P1"}}, "P1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := patternNameOperand(tc.operands)
			if got != tc.want || ok != tc.ok {
				t.Errorf("patternNameOperand = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestExtGStateOperator(t *testing.T) {
	resources := resourcesWith("ExtGState", map[string]pdf.PDFValue{
		"Half":  pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"ca": pdf.PDFReal(0.5)})},
		"Empty": pdf.PDFDict{Entries: pdf.DictOf(nil)},
	})
	fill := "0 0 0 rg 0 0 20 20 re f"

	half := render20(t, "/Half gs "+fill, resources)
	if px := nrgbaAt(t, half, 10, 10); px.R < 120 || px.R > 135 || px.A != 255 {
		t.Errorf("ca 0.5 black over paper = %+v, want mid-gray", px)
	}
	// Everything that cannot name an ExtGState leaves painting opaque.
	for name, content := range map[string]string{
		"no operand":      "gs " + fill,
		"not a name":      "5 gs " + fill,
		"unknown name":    "/Nope gs " + fill,
		"no ca/CA inside": "/Empty gs " + fill,
	} {
		img := render20(t, content, resources)
		if !isBlack(t, img, 10, 10) {
			t.Errorf("%s: fill was not opaque black", name)
		}
	}
}

func formXObject(content string, extra map[string]pdf.PDFValue) pdf.PDFDict {
	entries := map[string]pdf.PDFValue{
		"Subtype": pdf.PDFName{Value: "Form"},
		"BBox":    numArray(0, 0, 20, 20),
	}
	for k, v := range extra {
		entries[k] = v
	}
	return pdf.NewStreamDict(pdf.DictOf(entries), []byte(content))
}

func TestDoOperatorIgnoresUnusableOperands(t *testing.T) {
	resources := resourcesWith("XObject", map[string]pdf.PDFValue{
		"NotAStream": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Form"}})},
		"Untyped":    pdf.NewStreamDict(pdf.DictOf(nil), []byte("0 0 20 20 re f")),
	})
	for name, op := range map[string]string{
		"no operand":      "Do",
		"not a name":      "5 Do",
		"unknown name":    "/Nope Do",
		"not a stream":    "/NotAStream Do",
		"no Subtype":      "/Untyped Do",
		"unresolved name": "/Gone Do",
	} {
		if n, _, _, _, _ := ink(render20(t, "0 0 0 rg "+op, resources)); n != 0 {
			t.Errorf("%s: Do painted %d pixels", name, n)
		}
	}
}

func TestFormXObjectBehaviour(t *testing.T) {
	t.Run("inherits page resources and paint, applies /content.Matrix", func(t *testing.T) {
		form := formXObject("0 0 5 5 re f", map[string]pdf.PDFValue{"content.Matrix": numArray(1, 0, 0, 1, 10, 0)})
		resources := resourcesWith("XObject", map[string]pdf.PDFValue{"Fm": form})
		img := render20(t, "1 0 0 rg /Fm Do", resources)
		// The form's 5x5 square at its origin lands at x 10..15 along the bottom.
		if px := nrgbaAt(t, img, 12, 17); px.R != 255 || px.G != 0 || px.A != 255 {
			t.Errorf("translated form pixel = %+v, want red", px)
		}
		if !isWhite(t, img, 2, 17) {
			t.Error("form painted at its untranslated position")
		}
	})

	t.Run("a /content.Matrix that is not six numbers is ignored", func(t *testing.T) {
		form := formXObject("0 0 5 5 re f", map[string]pdf.PDFValue{"content.Matrix": numArray(1, 0, 0)})
		resources := resourcesWith("XObject", map[string]pdf.PDFValue{"Fm": form})
		if img := render20(t, "0 0 0 rg /Fm Do", resources); !isBlack(t, img, 2, 17) {
			t.Error("form with a malformed /content.Matrix did not paint at the identity position")
		}
	})

	t.Run("paints only inside its /BBox", func(t *testing.T) {
		form := formXObject("0 0 20 20 re f", map[string]pdf.PDFValue{"BBox": numArray(5, 5, 10, 10)})
		resources := resourcesWith("XObject", map[string]pdf.PDFValue{"Fm": form})
		img := render20(t, "0 0 0 rg /Fm Do", resources)
		// User y 5..10 is device y 10..15.
		if !isBlack(t, img, 7, 12) {
			t.Error("form did not paint inside its /BBox")
		}
		for _, p := range [][2]int{{2, 12}, {17, 12}, {7, 5}, {7, 18}} {
			if !isWhite(t, img, p[0], p[1]) {
				t.Errorf("form painted outside its /BBox at %v", p)
			}
		}
	})

	t.Run("/BBox is in form space, mapped through /content.Matrix", func(t *testing.T) {
		form := formXObject("0 0 20 20 re f", map[string]pdf.PDFValue{
			"BBox": numArray(0, 0, 5, 5), "content.Matrix": numArray(1, 0, 0, 1, 10, 0),
		})
		resources := resourcesWith("XObject", map[string]pdf.PDFValue{"Fm": form})
		img := render20(t, "0 0 0 rg /Fm Do", resources)
		if !isBlack(t, img, 12, 17) || !isWhite(t, img, 2, 17) || !isWhite(t, img, 17, 17) || !isWhite(t, img, 12, 10) {
			t.Error("/BBox was not clipped at the form's translated position")
		}
	})

	t.Run("a /BBox disjoint from the clip in force paints nothing", func(t *testing.T) {
		form := formXObject("0 0 20 20 re f", map[string]pdf.PDFValue{"BBox": numArray(10, 10, 20, 20)})
		resources := resourcesWith("XObject", map[string]pdf.PDFValue{"Fm": form})
		if n, _, _, _, _ := ink(render20(t, "0 0 5 5 re W n 0 0 0 rg /Fm Do", resources)); n != 0 {
			t.Errorf("form painted %d pixels through an empty clip", n)
		}
	})

	t.Run("undecodable content paints nothing", func(t *testing.T) {
		form := formXObject("", map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "LZWDecode"}})
		form.SetStream([]byte{0xFF, 0xFF, 0xFF})
		resources := resourcesWith("XObject", map[string]pdf.PDFValue{"Fm": form})
		if n, _, _, _, _ := ink(render20(t, "0 0 0 rg /Fm Do", resources)); n != 0 {
			t.Errorf("undecodable form painted %d pixels", n)
		}
	})

	t.Run("self-invocation stops at the nesting limit", func(t *testing.T) {
		// Each level shifts one unit right and paints a unit square, then
		// invokes itself. 13 levels run (nesting depth 0 through 12).
		form := formXObject("1 0 0 1 1 0 cm 0 0 1 1 re f /Fm Do", nil)
		resources := resourcesWith("XObject", map[string]pdf.PDFValue{"Fm": form})
		img := render20(t, "0 0 0 rg /Fm Do", resources)
		// The unit square of level k covers pixels k and k+1 (the fill takes
		// both pixels its edges touch), so level 13 ends at pixel 14.
		if !isBlack(t, img, 14, 19) {
			t.Error("the 13th nested level did not paint")
		}
		if !isWhite(t, img, 15, 19) {
			t.Error("a 14th nested level painted past the limit")
		}
	})
}

// TestTransparencyGroupForm verifies ISO 32000-1 11.6.6: a group's content
// starts at alpha 1, and the alpha in effect where the group is drawn applies
// once, to the composited group.
func TestTransparencyGroupForm(t *testing.T) {
	group := map[string]pdf.PDFValue{"S": pdf.PDFName{Value: "Transparency"}}
	gsWith := func(ca float64) pdf.PDFDict {
		return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"ca": pdf.PDFReal(ca)})}
	}
	extGStates := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Half": gsWith(0.5), "Full": gsWith(1),
	})}
	formWith := func(content string, xobjects map[string]pdf.PDFValue) pdf.PDFDict {
		res := map[string]pdf.PDFValue{"ExtGState": extGStates}
		if xobjects != nil {
			res["XObject"] = pdf.PDFDict{Entries: pdf.DictOf(xobjects)}
		}
		return formXObject(content, map[string]pdf.PDFValue{
			"Group":     pdf.PDFDict{Entries: pdf.DictOf(group)},
			"Resources": pdf.PDFDict{Entries: pdf.DictOf(res)},
		})
	}
	page := func(form pdf.PDFDict) pdf.PDFDict {
		return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"ExtGState": extGStates,
			"XObject":   pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Fm": form})},
		})}
	}
	// Red at alpha a over white paper: (255, 255(1-a), 255(1-a)).
	wantRed := func(t *testing.T, img *image.RGBA, a float64) {
		t.Helper()
		px := nrgbaAt(t, img, 5, 15)
		want := 255 * (1 - a)
		if px.R != 255 || math.Abs(float64(px.G)-want) > 2 || math.Abs(float64(px.B)-want) > 2 {
			t.Errorf("pixel = %+v, want red at alpha %.2f (G, B about %.0f)", px, a, want)
		}
	}

	t.Run("a /ca 1 inside does not undo the alpha it is drawn at", func(t *testing.T) {
		form := formWith("/Full gs 1 0 0 rg 0 0 20 20 re f", nil)
		wantRed(t, render20(t, "/Half gs /Fm Do", page(form)), 0.5)
	})

	t.Run("nested groups multiply their alphas", func(t *testing.T) {
		inner := formWith("1 0 0 rg 0 0 20 20 re f", nil)
		outer := formWith("/Half gs /In Do", map[string]pdf.PDFValue{"In": inner})
		wantRed(t, render20(t, "/Half gs /Fm Do", page(outer)), 0.25)
	})

	t.Run("overlapping fills inside composite before the group alpha", func(t *testing.T) {
		// Two opaque reds over each other are one red, faded once.
		form := formWith("1 0 0 rg 0 0 20 20 re f 0 0 20 20 re f", nil)
		wantRed(t, render20(t, "/Half gs /Fm Do", page(form)), 0.5)
	})

	t.Run("a form that is not a group still inherits the alpha", func(t *testing.T) {
		form := formXObject("1 0 0 rg 0 0 20 20 re f", nil)
		wantRed(t, render20(t, "/Half gs /Fm Do", page(form)), 0.5)
	})

	t.Run("what a group leaves unpainted keeps the backdrop", func(t *testing.T) {
		form := formWith("1 0 0 rg 0 0 10 20 re f", nil)
		img := render20(t, "0 0 1 rg 0 0 20 20 re f /Half gs /Fm Do", page(form))
		if px := nrgbaAt(t, img, 15, 15); px.R != 0 || px.G != 0 || px.B != 255 {
			t.Errorf("unpainted part of the group = %+v, want the blue under it", px)
		}
	})

	t.Run("a group outside the clip in force paints nothing", func(t *testing.T) {
		form := formWith("1 0 0 rg 0 0 20 20 re f", nil)
		form.Entries.Set("BBox", numArray(10, 10, 20, 20))
		if n, _, _, _, _ := ink(render20(t, "0 0 5 5 re W n /Fm Do", page(form))); n != 0 {
			t.Errorf("group painted %d pixels through an empty clip", n)
		}
	})
}

func TestImageXObjectEdgeCases(t *testing.T) {
	gray := func(entries map[string]pdf.PDFValue, data ...byte) pdf.PDFDict {
		e := map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(1), "Height": pdf.PDFInteger(1),
			"BitsPerComponent": pdf.PDFInteger(8), "ColorSpace": pdf.PDFName{Value: "DeviceGray"},
		}
		for k, v := range entries {
			e[k] = v
		}
		return imageDict(e, data)
	}
	draw := func(im pdf.PDFDict, ctm string) *image.RGBA {
		resources := resourcesWith("XObject", map[string]pdf.PDFValue{"Im": im})
		return render20(t, "q "+ctm+" /Im Do Q", resources)
	}
	const full = "20 0 0 20 0 0 cm"

	if n, _, _, _, _ := ink(draw(gray(map[string]pdf.PDFValue{"Width": pdf.PDFInteger(0)}, 0), full)); n != 0 {
		t.Errorf("an image with no width painted %d pixels", n)
	}
	if n, _, _, _, _ := ink(draw(gray(nil, 0), "0 0 0 0 0 0 cm")); n != 0 {
		t.Errorf("an image under a singular CTM painted %d pixels", n)
	}

	// A stencil /Mask that cannot be decoded is ignored: the image still paints.
	badMask := imageDict(map[string]pdf.PDFValue{"Width": pdf.PDFInteger(0), "ImageMask": pdf.PDFBoolean(true)}, nil)
	if img := draw(gray(map[string]pdf.PDFValue{"Mask": badMask}, 0), full); !isBlack(t, img, 10, 10) {
		t.Error("an undecodable /Mask stopped the image painting")
	}

	// Rotated 45 degrees, the image's bounding box has corners outside the
	// image itself, which stay unpainted.
	rotated := draw(gray(nil, 0), "10 10 -10 10 10 0 cm")
	if !isBlack(t, rotated, 10, 10) {
		t.Error("the rotated image missed its own centre")
	}
	if !isWhite(t, rotated, 1, 1) {
		t.Error("the rotated image painted its bounding box corner")
	}
}

// TestZeroSizeJPEGDoesNotPanic verifies that a JPEG header claiming 0x0 decodes
// to an empty image, which has nothing to paint.
func TestZeroSizeJPEGDoesNotPanic(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	for i := 0; i+9 < len(data); i++ {
		if data[i] == 0xFF && data[i+1] == 0xC0 { // SOF0: zero the height and width
			data[i+5], data[i+6], data[i+7], data[i+8] = 0, 0, 0, 0
		}
	}
	im := imageDict(map[string]pdf.PDFValue{
		"Width": pdf.PDFInteger(8), "Height": pdf.PDFInteger(8),
		"BitsPerComponent": pdf.PDFInteger(8), "ColorSpace": pdf.PDFName{Value: "DeviceRGB"},
		"Filter": pdf.PDFName{Value: "DCTDecode"},
	}, data)
	resources := resourcesWith("XObject", map[string]pdf.PDFValue{"Im": im})
	if n, _, _, _, _ := ink(render20(t, "q 20 0 0 20 0 0 cm /Im Do Q", resources)); n != 0 {
		t.Errorf("an empty JPEG painted %d pixels", n)
	}
}
