package raster

import (
	"fmt"
	"image"
	"image/color"
	"math"
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
// device pixel, including zero, paints a line one pixel wide. The test lines
// run along pixel boundaries, so their single pixel of coverage is split
// between the two neighbouring rows or columns.
func TestStrokePathThinWidthDrawsHairline(t *testing.T) {
	for _, width := range []float64{0, 0.02} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			canvas := image.NewRGBA(image.Rect(0, 0, 20, 20))
			StrokePath(canvas, [][]content.Point{{{X: 2, Y: 5}, {X: 18, Y: 5}}, {{X: 10, Y: 8}, {X: 10, Y: 18}}}, width, [3]float64{0, 0, 1}, 1)
			for _, pair := range [][2]image.Point{{{6, 4}, {6, 5}}, {{9, 12}, {10, 12}}} {
				sum := int(canvas.RGBAAt(pair[0].X, pair[0].Y).A) + int(canvas.RGBAAt(pair[1].X, pair[1].Y).A)
				if sum < 253 || sum > 257 {
					t.Errorf("hairline across %v coverage = %d/255, want one pixel", pair, sum)
				}
			}
			if off := canvas.RGBAAt(6, 2); off.A != 0 {
				t.Errorf("pixel two rows away = %+v, want untouched", off)
			}
		})
	}
}

// alphaAt returns the alpha of the pixel at (x, y) as a fraction.
func alphaAt(c *image.RGBA, x, y int) float64 { return float64(c.RGBAAt(x, y).A) / 255 }

func rectContour(x0, y0, x1, y1 float64) []content.Point {
	return []content.Point{{X: x0, Y: y0}, {X: x1, Y: y0}, {X: x1, Y: y1}, {X: x0, Y: y1}}
}

// TestFillPathCoversPixelsFractionally verifies that edge pixels get alpha in
// proportion to the area the shape covers, horizontally to full precision
// and vertically to eighths.
func TestFillPathCoversPixelsFractionally(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 8, 8))
	FillPath(canvas, [][]content.Point{rectContour(1.5, 1.25, 5.25, 3)}, [3]float64{0, 0, 0}, 1, false)
	for _, tc := range []struct {
		x, y int
		want float64
	}{
		{1, 1, 0.5 * 0.75}, // corner: half a pixel wide, three quarters tall
		{3, 1, 0.75},       // top edge
		{1, 2, 0.5},        // left edge
		{5, 2, 0.25},       // right edge
		{3, 2, 1},          // interior
		{0, 2, 0}, {6, 2, 0}, {3, 3, 0}, {3, 0, 0},
	} {
		if got := alphaAt(canvas, tc.x, tc.y); got < tc.want-0.01 || got > tc.want+0.01 {
			t.Errorf("alpha(%d,%d) = %.3f, want %.3f", tc.x, tc.y, got, tc.want)
		}
	}
}

// TestFillPathAreaIsPreserved verifies that the summed coverage of a polygon
// matches its geometric area.
func TestFillPathAreaIsPreserved(t *testing.T) {
	// A regular 40-gon of radius 12.3 centred off-grid.
	const n, r, cx, cy = 40, 12.3, 16.37, 15.81
	var poly []content.Point
	for i := range n {
		a := 2 * math.Pi * float64(i) / n
		poly = append(poly, content.Point{X: cx + r*math.Cos(a), Y: cy + r*math.Sin(a)})
	}
	area := 0.5 * n * r * r * math.Sin(2*math.Pi/n)
	canvas := image.NewRGBA(image.Rect(0, 0, 32, 32))
	FillPath(canvas, [][]content.Point{poly}, [3]float64{0, 0, 0}, 1, false)
	var sum float64
	for y := range 32 {
		for x := range 32 {
			sum += alphaAt(canvas, x, y)
		}
	}
	if math.Abs(sum-area)/area > 0.01 {
		t.Errorf("summed coverage %.2f, polygon area %.2f", sum, area)
	}
}

// TestFillPathAliasedPaintsWholePixels verifies that the aliased mode paints a
// pixel fully when its centre is inside and not at all otherwise.
func TestFillPathAliasedPaintsWholePixels(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 8, 8))
	paint := func(int, int) ([3]float64, float64) { return [3]float64{1, 0, 0}, 1 }
	fillPathAliased(canvas, [][]content.Point{rectContour(1.4, 1.4, 4.6, 3.6)}, false, paint)
	for _, p := range []struct {
		x, y int
		on   bool
	}{
		{1, 1, true}, {4, 3, true}, {0, 1, false}, {5, 1, false}, {1, 0, false}, {1, 4, false},
	} {
		got := alphaAt(canvas, p.x, p.y)
		if (got == 1) != p.on || (got != 0 && got != 1) {
			t.Errorf("aliased alpha(%d,%d) = %.2f, want on=%v", p.x, p.y, got, p.on)
		}
	}
}

// TestFillPathOnAnOffsetCanvas verifies that coverage lands on the right
// pixels when the canvas bounds do not start at the origin, and that shapes
// hanging over the bounds are clipped.
func TestFillPathOnAnOffsetCanvas(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(10, 10, 14, 14))
	FillPath(canvas, [][]content.Point{rectContour(8, 8, 12.5, 20)}, [3]float64{0, 0, 0}, 1, false)
	for _, tc := range []struct {
		x, y int
		want float64
	}{{10, 10, 1}, {11, 13, 1}, {12, 12, 0.5}, {13, 12, 0}} {
		if got := alphaAt(canvas, tc.x, tc.y); math.Abs(got-tc.want) > 0.01 {
			t.Errorf("alpha(%d,%d) = %.2f, want %.2f", tc.x, tc.y, got, tc.want)
		}
	}
}

// TestStrokePathJoinsDoNotDarkenTranslucentStrokes verifies that two segments
// meeting at a corner paint the corner pixels once, not once per segment.
func TestStrokePathJoinsDoNotDarkenTranslucentStrokes(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 20, 20))
	StrokePath(canvas, [][]content.Point{{{X: 3, Y: 4}, {X: 15, Y: 4}, {X: 15, Y: 16}}}, 4, [3]float64{0, 0, 0}, 0.5)
	// (14,4) lies in both segments' quads near the corner.
	if got := alphaAt(canvas, 14, 4); math.Abs(got-0.5) > 0.01 {
		t.Errorf("corner alpha = %.3f, want 0.5 (painted once)", got)
	}
}
