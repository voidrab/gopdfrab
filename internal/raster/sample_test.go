package raster

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func within(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestSamplerBilinearUpscale(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.SetRGBA(0, 0, color.RGBA{0, 0, 0, 255})
	img.SetRGBA(1, 0, color.RGBA{255, 255, 255, 255})
	s := newSampler(img)
	for _, tc := range []struct{ u, want float64 }{
		{0.25, 0}, {0.5, 0.5}, {0.75, 1}, // pixel centres sit at 1/4 and 3/4
		{0.375, 0.25}, {0, 0}, {1, 1}, // clamped outside the centres
	} {
		rgb, a := s.at(0, tc.u, 0.5, true)
		if !within(rgb[0], tc.want, 0.01) || !within(a, 1, 1e-9) {
			t.Errorf("at(u=%v) = %v alpha %v, want %v", tc.u, rgb[0], a, tc.want)
		}
	}
}

func TestSamplerAveragesWhenShrinking(t *testing.T) {
	// A 4x4 checkerboard viewed at a quarter size is mid-grey, not black or white.
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			c := uint8(0)
			if (x+y)%2 == 0 {
				c = 255
			}
			img.SetRGBA(x, y, color.RGBA{c, c, c, 255})
		}
	}
	s := newSampler(img)
	level := s.levelFor(4)
	if level != 2 {
		t.Fatalf("levelFor(4) = %d", level)
	}
	rgb, a := s.at(level, 0.5, 0.5, true)
	if !within(rgb[0], 0.5, 0.02) || !within(a, 1, 1e-9) {
		t.Errorf("shrunk checkerboard = %v alpha %v, want mid grey", rgb, a)
	}
	if got := s.levelFor(2); got != 1 || len(s.levels) != 3 {
		t.Errorf("levelFor(2) = %d with %d levels built", got, len(s.levels))
	}
}

func TestSamplerLevelSelection(t *testing.T) {
	s := newSampler(solid(8, 8, color.RGBA{9, 9, 9, 255}))
	for _, f := range []float64{0, 0.5, 1, math.NaN()} {
		if s.levelFor(f) != 0 {
			t.Errorf("levelFor(%v) != 0", f)
		}
	}
	if len(s.levels) != 1 {
		t.Error("a level was built without a shrink")
	}
	if got := s.levelFor(2.9); got != 1 {
		t.Errorf("levelFor(2.9) = %d", got)
	}
	// Past the 1x1 level the pyramid stops growing.
	if got := s.levelFor(1e6); got != 3 || s.levels[3].Rect.Dx() != 1 {
		t.Errorf("levelFor(1e6) = %d, top level %v", got, s.levels[len(s.levels)-1].Rect)
	}
}

func TestHalveHandlesOddSizesAndAlpha(t *testing.T) {
	// 3x1: the odd last column pairs with itself.
	img := image.NewRGBA(image.Rect(0, 0, 3, 1))
	img.SetRGBA(0, 0, color.RGBA{0, 0, 0, 255})
	img.SetRGBA(1, 0, color.RGBA{200, 200, 200, 255})
	img.SetRGBA(2, 0, color.RGBA{100, 100, 100, 255})
	h := halve(img)
	if h.Rect.Dx() != 2 || h.Rect.Dy() != 1 {
		t.Fatalf("halve = %v", h.Rect)
	}
	if got := h.RGBAAt(0, 0); got.R != 100 || got.A != 255 {
		t.Errorf("first block = %v", got)
	}
	if got := h.RGBAAt(1, 0); got.R != 100 || got.A != 255 {
		t.Errorf("odd block = %v", got)
	}

	// A red pixel beside a transparent one stays red at half the alpha; the
	// transparent pixel's colour must not tint it.
	img = image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.SetRGBA(0, 0, color.RGBA{255, 0, 0, 255})
	img.SetRGBA(1, 0, color.RGBA{0, 0, 255, 0})
	img.SetRGBA(0, 1, color.RGBA{0, 0, 255, 0})
	img.SetRGBA(1, 1, color.RGBA{0, 0, 255, 0})
	got := halve(img).RGBAAt(0, 0)
	if got.R != 255 || got.B != 0 || got.A != 64 {
		t.Errorf("alpha-weighted block = %v, want opaque-red at quarter alpha", got)
	}
	// An all-transparent block has no colour to speak of.
	if got := halve(solid(2, 2, color.RGBA{7, 7, 7, 0})).RGBAAt(0, 0); got != (color.RGBA{}) {
		t.Errorf("transparent block = %v", got)
	}
}

func TestSamplerAlphaIsWeightedNotTinted(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.SetRGBA(0, 0, color.RGBA{255, 0, 0, 255})
	img.SetRGBA(1, 0, color.RGBA{0, 255, 0, 0}) // transparent green
	rgb, a := newSampler(img).at(0, 0.5, 0.5, true)
	if !within(a, 0.5, 0.01) || !within(rgb[0], 1, 0.01) || rgb[1] > 0.01 {
		t.Errorf("midpoint = %v alpha %v, want red at half alpha", rgb, a)
	}
	// Fully transparent surroundings give no colour and no alpha.
	rgb, a = newSampler(solid(2, 2, color.RGBA{5, 5, 5, 0})).at(0, 0.5, 0.5, true)
	if a != 0 || rgb != ([3]float64{}) {
		t.Errorf("transparent sample = %v alpha %v", rgb, a)
	}
}

func TestSamplerOnASubImage(t *testing.T) {
	// Pixels are addressed relative to the image's own origin.
	base := solid(4, 4, color.RGBA{0, 0, 0, 255})
	base.SetRGBA(2, 2, color.RGBA{255, 255, 255, 255})
	sub := base.SubImage(image.Rect(2, 2, 4, 4)).(*image.RGBA)
	rgb, _ := newSampler(sub).at(0, 0.25, 0.25, true)
	if !within(rgb[0], 1, 0.01) {
		t.Errorf("sub-image sample = %v", rgb)
	}
	if got := halve(sub).RGBAAt(0, 0); got.R != 64 {
		t.Errorf("halved sub-image = %v", got)
	}
}

func TestSamplerNearestKeepsHardEdges(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.SetRGBA(0, 0, color.RGBA{255, 0, 0, 255})
	img.SetRGBA(1, 0, color.RGBA{0, 0, 255, 128})
	s := newSampler(img)
	for _, tc := range []struct {
		u    float64
		r, b float64
		a    float64
	}{{0.1, 1, 0, 1}, {0.49, 1, 0, 1}, {0.51, 0, 1, 128.0 / 255}, {0.99, 0, 1, 128.0 / 255}, {1, 0, 1, 128.0 / 255}, {-3, 1, 0, 1}} {
		rgb, a := s.at(0, tc.u, 0.5, false)
		if !within(rgb[0], tc.r, 1e-9) || !within(rgb[2], tc.b, 1e-9) || !within(a, tc.a, 1e-9) {
			t.Errorf("nearest at u=%v = %v alpha %v", tc.u, rgb, a)
		}
	}
}
