package raster

import (
	"fmt"
	"image"
	"image/color"
	"testing"
)

func TestFillPathAlphaBlending(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 4, 4))
	rect := []Point{{0, 0}, {4, 0}, {4, 4}, {0, 4}}
	FillPath(canvas, [][]Point{rect}, [3]float64{1, 0, 0}, 0.5, false)

	px := canvas.RGBAAt(2, 2)
	if px.A < 120 || px.A > 135 {
		t.Errorf("alpha=0.5 fill over empty canvas alpha = %d, want ~127", px.A)
	}
}

func TestFillPathRectangle(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 10, 10))
	rect := []Point{{2, 2}, {8, 2}, {8, 8}, {2, 8}}
	FillPath(canvas, [][]Point{rect}, [3]float64{1, 0, 0}, 1, false)

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
	triangle := []Point{{5, 1}, {9, 9}, {1, 9}}
	FillPath(canvas, [][]Point{triangle}, [3]float64{0, 1, 0}, 1, false)

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
	outer := []Point{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
	inner := []Point{{3, 3}, {7, 3}, {7, 7}, {3, 7}}

	canvasNonzero := image.NewRGBA(image.Rect(0, 0, 10, 10))
	FillPath(canvasNonzero, [][]Point{outer, inner}, [3]float64{1, 1, 1}, 1, false)
	centerNonzero := color.NRGBAModel.Convert(canvasNonzero.At(5, 5)).(color.NRGBA)
	if centerNonzero.A != 255 {
		t.Errorf("nonzero winding: overlap pixel = %+v, want filled", centerNonzero)
	}

	canvasEvenOdd := image.NewRGBA(image.Rect(0, 0, 10, 10))
	FillPath(canvasEvenOdd, [][]Point{outer, inner}, [3]float64{1, 1, 1}, 1, true)
	centerEvenOdd := color.NRGBAModel.Convert(canvasEvenOdd.At(5, 5)).(color.NRGBA)
	if centerEvenOdd.A != 0 {
		t.Errorf("even-odd winding: overlap pixel = %+v, want unfilled (hole)", centerEvenOdd)
	}
}

func TestStrokePathWidth(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 20, 10))
	line := []Point{{2, 5}, {18, 5}}
	StrokePath(canvas, [][]Point{line}, 4, [3]float64{0, 0, 1}, 1)

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
	square := []Point{{1, 1}, {5, 1}, {5, 5}, {1, 5}}
	// A horizontal-only contour has no edges to scan.
	flat := []Point{{1, 2}, {5, 2}, {9, 2}}

	for _, tc := range []struct {
		name string
		fill func(*image.RGBA)
	}{
		{"zero alpha", func(c *image.RGBA) { FillPath(c, [][]Point{square}, [3]float64{1, 0, 0}, 0, false) }},
		{"negative alpha", func(c *image.RGBA) { FillPath(c, [][]Point{square}, [3]float64{1, 0, 0}, -1, false) }},
		{"no contours", func(c *image.RGBA) { FillPath(c, nil, [3]float64{1, 0, 0}, 1, false) }},
		{"only horizontal edges", func(c *image.RGBA) { FillPath(c, [][]Point{flat}, [3]float64{1, 0, 0}, 1, false) }},
		{"shaded, zero alpha everywhere", func(c *image.RGBA) {
			FillPathShaded(c, [][]Point{square}, false, func(x, y int) ([3]float64, float64) {
				return [3]float64{1, 0, 0}, 0
			})
		}},
		{"stroke, zero alpha", func(c *image.RGBA) {
			StrokePath(c, [][]Point{{{1, 5}, {9, 5}}}, 2, [3]float64{1, 0, 0}, 0)
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

// TestStrokePathThinWidthDrawsHairline pins that a line width under one
// device pixel, zero included, still paints a one-pixel line. Pixels are
// sampled at their centres, so a thinner stroke that falls between two
// centres would paint nothing.
func TestStrokePathThinWidthDrawsHairline(t *testing.T) {
	for _, width := range []float64{0, 0.02} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			canvas := image.NewRGBA(image.Rect(0, 0, 20, 20))
			StrokePath(canvas, [][]Point{{{2, 5}, {18, 5}}, {{10, 8}, {10, 18}}}, width, [3]float64{0, 0, 1}, 1)
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
