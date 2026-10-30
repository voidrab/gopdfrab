package raster

import (
	"fmt"
	"image"
	"image/color"
	"testing"

	"github.com/voidrab/gopdfrab/internal/content"
)

func TestFillPathAlphaBlending(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 4, 4))
	rect := []content.Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 4, Y: 4}, {X: 0, Y: 4}}
	FillPath(canvas, [][]content.Point{rect}, [3]float64{1, 0, 0}, 0.5, false)

	px := canvas.RGBAAt(2, 2)
	if px.A < 120 || px.A > 135 {
		t.Errorf("alpha=0.5 fill over empty canvas alpha = %d, want ~127", px.A)
	}
}

func TestFillPathRectangle(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 10, 10))
	rect := []content.Point{{X: 2, Y: 2}, {X: 8, Y: 2}, {X: 8, Y: 8}, {X: 2, Y: 8}}
	FillPath(canvas, [][]content.Point{rect}, [3]float64{1, 0, 0}, 1, false)

	// Inside the rectangle should be opaque red.
	inside := color.NRGBAModel.Convert(canvas.At(5, 5)).(color.NRGBA)
	if inside.R != 255 || inside.G != 0 || inside.B != 0 || inside.A != 255 {
		t.Errorf("inside pixel = %+v, want opaque red", inside)
	}

	// Outside should remain untouched (transparent black).
	outside := color.NRGBAModel.Convert(canvas.At(0, 0)).(color.NRGBA)
	if outside.A != 0 {
		t.Errorf("outside pixel = %+v, want untouched (alpha 0)", outside)
	}
}

func TestFillPathTriangle(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 10, 10))
	triangle := []content.Point{{X: 5, Y: 1}, {X: 9, Y: 9}, {X: 1, Y: 9}}
	FillPath(canvas, [][]content.Point{triangle}, [3]float64{0, 1, 0}, 1, false)

	// The centroid should be filled.
	centroid := color.NRGBAModel.Convert(canvas.At(5, 7)).(color.NRGBA)
	if centroid.G != 255 || centroid.A != 255 {
		t.Errorf("centroid pixel = %+v, want opaque green", centroid)
	}

	// A far corner outside the triangle should be untouched.
	corner := color.NRGBAModel.Convert(canvas.At(0, 0)).(color.NRGBA)
	if corner.A != 0 {
		t.Errorf("corner pixel = %+v, want untouched", corner)
	}
}

func TestFillPathEvenOddVsNonzero(t *testing.T) {
	// Two overlapping squares wound the same direction: nonzero fills the
	// union including the overlap, even-odd leaves the overlap unfilled.
	outer := []content.Point{{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}, {X: 0, Y: 10}}
	inner := []content.Point{{X: 3, Y: 3}, {X: 7, Y: 3}, {X: 7, Y: 7}, {X: 3, Y: 7}}

	canvasNonzero := image.NewRGBA(image.Rect(0, 0, 10, 10))
	FillPath(canvasNonzero, [][]content.Point{outer, inner}, [3]float64{1, 1, 1}, 1, false)
	centerNonzero := color.NRGBAModel.Convert(canvasNonzero.At(5, 5)).(color.NRGBA)
	if centerNonzero.A != 255 {
		t.Errorf("nonzero winding: overlap pixel = %+v, want filled", centerNonzero)
	}

	canvasEvenOdd := image.NewRGBA(image.Rect(0, 0, 10, 10))
	FillPath(canvasEvenOdd, [][]content.Point{outer, inner}, [3]float64{1, 1, 1}, 1, true)
	centerEvenOdd := color.NRGBAModel.Convert(canvasEvenOdd.At(5, 5)).(color.NRGBA)
	if centerEvenOdd.A != 0 {
		t.Errorf("even-odd winding: overlap pixel = %+v, want unfilled (hole)", centerEvenOdd)
	}
}

func TestStrokePathWidth(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 20, 10))
	line := []content.Point{{X: 2, Y: 5}, {X: 18, Y: 5}}
	StrokePath(canvas, [][]content.Point{line}, 4, [3]float64{0, 0, 1}, 1)

	// Directly on the line should be filled.
	on := color.NRGBAModel.Convert(canvas.At(10, 5)).(color.NRGBA)
	if on.A != 255 {
		t.Errorf("on-line pixel = %+v, want opaque", on)
	}

	// Far above/below the 4px-wide stroke should be untouched.
	above := color.NRGBAModel.Convert(canvas.At(10, 0)).(color.NRGBA)
	if above.A != 0 {
		t.Errorf("far-above pixel = %+v, want untouched", above)
	}
}

func canvasIsBlank(c *image.RGBA) bool {
	for _, b := range c.Pix {
		if b != 0 {
			return false
		}
	}
	return true
}

func TestFillPathPaintsNothingForDegenerateInput(t *testing.T) {
	square := []content.Point{{X: 1, Y: 1}, {X: 5, Y: 1}, {X: 5, Y: 5}, {X: 1, Y: 5}}
	// A horizontal-only contour has no edges to scan.
	flat := []content.Point{{X: 1, Y: 2}, {X: 5, Y: 2}, {X: 9, Y: 2}}

	for _, tc := range []struct {
		name string
		fill func(*image.RGBA)
	}{
		{"zero alpha", func(c *image.RGBA) { FillPath(c, [][]content.Point{square}, [3]float64{1, 0, 0}, 0, false) }},
		{"negative alpha", func(c *image.RGBA) { FillPath(c, [][]content.Point{square}, [3]float64{1, 0, 0}, -1, false) }},
		{"no contours", func(c *image.RGBA) { FillPath(c, nil, [3]float64{1, 0, 0}, 1, false) }},
		{"only horizontal edges", func(c *image.RGBA) { FillPath(c, [][]content.Point{flat}, [3]float64{1, 0, 0}, 1, false) }},
		{"shaded, zero alpha everywhere", func(c *image.RGBA) {
			FillPathShaded(c, [][]content.Point{square}, false, func(x, y int) ([3]float64, float64) {
				return [3]float64{1, 0, 0}, 0
			})
		}},
		{"stroke, zero alpha", func(c *image.RGBA) {
			StrokePath(c, [][]content.Point{{{X: 1, Y: 5}, {X: 9, Y: 5}}}, 2, [3]float64{1, 0, 0}, 0)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canvas := image.NewRGBA(image.Rect(0, 0, 10, 10))
			tc.fill(canvas)
			if !canvasIsBlank(canvas) {
				t.Error("canvas was painted")
			}
		})
	}
}

// TestStrokePathThinWidthDrawsHairline verifies that a line width below one
// device pixel, including zero, paints a line one pixel wide. Pixels are
// sampled at their centres, so a thinner stroke could otherwise paint nothing.
func TestStrokePathThinWidthDrawsHairline(t *testing.T) {
	for _, width := range []float64{0, 0.02} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			canvas := image.NewRGBA(image.Rect(0, 0, 20, 20))
			StrokePath(canvas, [][]content.Point{{{X: 2, Y: 5}, {X: 18, Y: 5}}, {{X: 10, Y: 8}, {X: 10, Y: 18}}}, width, [3]float64{0, 0, 1}, 1)
			for _, on := range []image.Point{{6, 4}, {10, 12}} {
				if c := canvas.RGBAAt(on.X, on.Y); c.A != 255 {
					t.Errorf("hairline pixel %v = %+v, want opaque", on, c)
				}
			}
			if off := canvas.RGBAAt(6, 2); off.A != 0 {
				t.Errorf("pixel two rows away = %+v, want untouched", off)
			}
		})
	}
}
