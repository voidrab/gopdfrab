package convert

import (
	"image"
	"math"
	"sort"
)

// FillPath rasterizes contours (each a closed polygon in device-space
// pixel coordinates) onto canvas using a classic active-edge-table scanline
// fill, with nonzero or even-odd winding per the f/f* operator, blending
// rgb at alpha over the existing pixels.
func FillPath(canvas *image.RGBA, contours [][]Point, rgb [3]float64, alpha float64, evenOdd bool) {
	if alpha <= 0 || len(contours) == 0 {
		return
	}
	edges := buildEdges(contours)
	if len(edges) == 0 {
		return
	}

	bounds := canvas.Bounds()
	minY, maxY := bounds.Min.Y, bounds.Max.Y
	for y := minY; y < maxY; y++ {
		scanY := float64(y) + 0.5
		spans := scanlineSpans(edges, scanY, evenOdd)
		for _, sp := range spans {
			x0 := int(math.Floor(sp[0] + 0.5))
			x1 := int(math.Ceil(sp[1] - 0.5))
			if x0 < bounds.Min.X {
				x0 = bounds.Min.X
			}
			if x1 > bounds.Max.X-1 {
				x1 = bounds.Max.X - 1
			}
			for x := x0; x <= x1; x++ {
				blendPixel(canvas, x, y, rgb, alpha)
			}
		}
	}
}

// pathEdge is one non-horizontal edge of a polygon, used by the active-edge
// scanline algorithm.
type pathEdge struct {
	y0, y1 float64 // y0 < y1
	x0     float64 // x at y0
	dxdy   float64
	dir    int // +1 if the original segment went upward in y, -1 otherwise
}

func buildEdges(contours [][]Point) []pathEdge {
	var edges []pathEdge
	for _, contour := range contours {
		n := len(contour)
		for i := 0; i < n; i++ {
			p0 := contour[i]
			p1 := contour[(i+1)%n]
			if p0.Y == p1.Y {
				continue
			}
			dir := 1
			if p0.Y > p1.Y {
				p0, p1 = p1, p0
				dir = -1
			}
			edges = append(edges, pathEdge{
				y0: p0.Y, y1: p1.Y, x0: p0.X,
				dxdy: (p1.X - p0.X) / (p1.Y - p0.Y),
				dir:  dir,
			})
		}
	}
	return edges
}

// scanlineSpans returns the filled [xStart,xEnd] intervals at height y.
func scanlineSpans(edges []pathEdge, y float64, evenOdd bool) [][2]float64 {
	type crossing struct {
		x   float64
		dir int
	}
	var crossings []crossing
	for _, e := range edges {
		if y < e.y0 || y >= e.y1 {
			continue
		}
		x := e.x0 + (y-e.y0)*e.dxdy
		crossings = append(crossings, crossing{x: x, dir: e.dir})
	}
	if len(crossings) == 0 {
		return nil
	}
	sort.Slice(crossings, func(i, j int) bool { return crossings[i].x < crossings[j].x })

	var spans [][2]float64
	winding := 0
	var spanStart float64
	inSpan := false
	for _, c := range crossings {
		wasInside := isInside(winding, evenOdd)
		winding += c.dir
		isInsideNow := isInside(winding, evenOdd)
		if !wasInside && isInsideNow {
			spanStart = c.x
			inSpan = true
		} else if wasInside && !isInsideNow && inSpan {
			spans = append(spans, [2]float64{spanStart, c.x})
			inSpan = false
		}
	}
	return spans
}

func isInside(winding int, evenOdd bool) bool {
	if evenOdd {
		return winding%2 != 0
	}
	return winding != 0
}

func blendPixel(canvas *image.RGBA, x, y int, rgb [3]float64, alpha float64) {
	off := canvas.PixOffset(x, y)
	pix := canvas.Pix
	if alpha >= 1 {
		storeRGBA64(pix, off, rgb[0], rgb[1], rgb[2], 1)
		return
	}
	er, eg, eb, ea := float64(pix[off])/255, float64(pix[off+1])/255, float64(pix[off+2])/255, float64(pix[off+3])/255
	outA := alpha + ea*(1-alpha)
	if outA <= 0 {
		storeRGBA64(pix, off, 0, 0, 0, 0)
		return
	}
	blend := func(c, ec float64) float64 { return (c*alpha + ec*ea*(1-alpha)) / outA }
	storeRGBA64(pix, off, blend(rgb[0], er), blend(rgb[1], eg), blend(rgb[2], eb), outA)
}

// StrokePath flattens contours as open polylines and fills the quad swept
// by each segment at lineWidth/2 half-width (device space), with bevelled
// joins rather than mitered/rounded -- a documented approximation since the
// rasterizer's only purpose is producing a flattened, no-longer-vector page.
func StrokePath(canvas *image.RGBA, contours [][]Point, lineWidth float64, rgb [3]float64, alpha float64) {
	half := lineWidth / 2
	if half <= 0 {
		half = 0.5
	}
	for _, contour := range contours {
		for i := 0; i+1 < len(contour); i++ {
			quad := segmentQuad(contour[i], contour[i+1], half)
			FillPath(canvas, [][]Point{quad}, rgb, alpha, false)
		}
	}
}

// segmentQuad returns the four corners of the rectangle covering segment
// p0-p1 at the given half-width, perpendicular to the segment direction.
func segmentQuad(p0, p1 Point, half float64) []Point {
	dx, dy := p1.X-p0.X, p1.Y-p0.Y
	length := math.Hypot(dx, dy)
	if length == 0 {
		return []Point{
			{p0.X - half, p0.Y - half}, {p0.X + half, p0.Y - half},
			{p0.X + half, p0.Y + half}, {p0.X - half, p0.Y + half},
		}
	}
	nx, ny := -dy/length*half, dx/length*half
	return []Point{
		{p0.X + nx, p0.Y + ny}, {p1.X + nx, p1.Y + ny},
		{p1.X - nx, p1.Y - ny}, {p0.X - nx, p0.Y - ny},
	}
}
