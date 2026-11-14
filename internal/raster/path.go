package raster

import (
	"cmp"
	"image"
	"math"
	"slices"

	"github.com/voidrab/gopdfrab/internal/content"
)

// FillPath rasterizes contours (each a closed polygon in device-space
// pixel coordinates) onto canvas using a classic active-edge-table scanline
// fill, with nonzero or even-odd winding per the f/f* operator, blending
// rgb at alpha over the existing pixels.
func FillPath(canvas *image.RGBA, contours [][]content.Point, rgb [3]float64, alpha float64, evenOdd bool) {
	if alpha <= 0 {
		return
	}
	fillPathFunc(canvas, contours, evenOdd, func(x, y int) ([3]float64, float64) {
		return rgb, alpha
	})
}

// FillPathShaded fills contours the same way as FillPath, but asks colorAt
// for each covered pixel's colour and alpha, so a shading pattern paints the
// path's true shape rather than its bounding box. An alpha of 0 leaves the
// pixel untouched.
func FillPathShaded(canvas *image.RGBA, contours [][]content.Point, evenOdd bool, colorAt func(x, y int) ([3]float64, float64)) {
	fillPathFunc(canvas, contours, evenOdd, colorAt)
}

// aaSamples is the number of sub-scanlines each pixel row is sampled at; the
// coverage of a pixel is exact across x and quantized to 1/aaSamples in y.
const aaSamples = 8

// fillPathFunc rasterizes contours with anti-aliasing. A pixel's coverage
// scales the alpha that colorAt returns for it, so a fully covered pixel keeps
// colorAt's alpha and only edge pixels are blended at reduced alpha.
func fillPathFunc(canvas *image.RGBA, contours [][]content.Point, evenOdd bool, colorAt func(x, y int) ([3]float64, float64)) {
	fillPathMode(canvas, contours, evenOdd, true, colorAt)
}

// fillPathAliased is fillPathFunc without anti-aliasing: a pixel is painted
// whole when its centre is inside. Shapes that must tile without seams, such
// as the triangles of a mesh shading, use it, because two anti-aliased
// neighbours each cover only part of the pixels along their shared edge and
// let the backdrop show through.
func fillPathAliased(canvas *image.RGBA, contours [][]content.Point, evenOdd bool, colorAt func(x, y int) ([3]float64, float64)) {
	fillPathMode(canvas, contours, evenOdd, false, colorAt)
}

func fillPathMode(canvas *image.RGBA, contours [][]content.Point, evenOdd, aa bool, colorAt func(x, y int) ([3]float64, float64)) {
	if len(contours) == 0 {
		return
	}
	edges := buildEdges(contours)
	if len(edges) == 0 {
		return
	}
	slices.SortFunc(edges, func(a, b pathEdge) int { return cmp.Compare(a.y0, b.y0) })

	// The scan loop is limited to the edges' y-extent. showText issues one fill
	// per glyph, so without this limit every glyph would scan the full page
	// height.
	edgeMinY, edgeMaxY := edges[0].y0, edges[0].y1
	for _, e := range edges[1:] {
		edgeMaxY = math.Max(edgeMaxY, e.y1)
	}
	bounds := canvas.Bounds()
	minY := max(bounds.Min.Y, int(math.Floor(edgeMinY)))
	maxY := min(bounds.Max.Y, int(math.Ceil(edgeMaxY)))
	w := bounds.Dx()
	if minY >= maxY || w <= 0 {
		return
	}

	samples := 1
	if aa {
		samples = aaSamples
	}
	f := coverage{
		edges: edges, evenOdd: evenOdd, aa: aa, samples: samples, w: w, minX: float64(bounds.Min.X),
		partial: make([]float32, w+1), runs: make([]float32, w+2),
	}
	for y := minY; y < maxY; y++ {
		f.row(y)
		f.flush(canvas, y, colorAt)
	}
}

// coverage accumulates the coverage of one pixel row. A span adds its
// partially covered end pixels to partial and its fully covered interior as a
// +1/-1 pair in runs, so the cost of a span does not grow with its width.
type coverage struct {
	edges   []pathEdge // sorted by y0
	evenOdd bool
	aa      bool // whether to sample aaSamples sub-scanlines and keep fractional x
	samples int  // sub-scanlines per row: aaSamples, or 1 when aliased
	w       int
	minX    float64

	next   int   // first edge not yet activated
	active []int // indices of the edges crossing the current sub-scanline
	cross  []crossing

	partial []float32 // coverage in units of sub-scanlines, per pixel
	runs    []float32 // difference array of fully covered sub-scanlines
	lo, hi  int       // touched pixel range of the current row
}

// row samples the sub-scanlines of pixel row y.
func (f *coverage) row(y int) {
	f.lo, f.hi = f.w, -1
	for k := range f.samples {
		sy := float64(y) + (float64(k)+0.5)/float64(f.samples)
		for f.next < len(f.edges) && f.edges[f.next].y0 <= sy {
			f.active = append(f.active, f.next)
			f.next++
		}
		f.cross = f.cross[:0]
		kept := f.active[:0]
		for _, ei := range f.active {
			e := &f.edges[ei]
			if e.y1 <= sy {
				continue
			}
			kept = append(kept, ei)
			f.cross = append(f.cross, crossing{x: e.x0 + (sy-e.y0)*e.dxdy - f.minX, dir: e.dir})
		}
		f.active = kept
		sortCrossings(f.cross)

		winding := 0
		var start float64
		for _, c := range f.cross {
			was := isInside(winding, f.evenOdd)
			winding += c.dir
			now := isInside(winding, f.evenOdd)
			if !was && now {
				start = c.x
			} else if was && !now {
				f.addSpan(start, c.x)
			}
		}
	}
}

// addSpan adds the filled interval [xa, xb) of one sub-scanline, in pixels
// from the canvas's left edge.
func (f *coverage) addSpan(xa, xb float64) {
	if !f.aa {
		// A pixel counts when its centre lies in the half-open [xa, xb), so
		// neighbouring spans tile with neither gap nor overlap.
		xa, xb = math.Ceil(xa-0.5), math.Ceil(xb-0.5)
	}
	xa = math.Max(xa, 0)
	xb = math.Min(xb, float64(f.w))
	if xb <= xa {
		return
	}
	ia, ib := int(xa), int(xb)
	if ia == ib {
		f.partial[ia] += float32(xb - xa)
	} else {
		f.partial[ia] += float32(float64(ia+1) - xa)
		if ib > ia+1 {
			f.runs[ia+1]++
			f.runs[ib]--
		}
		f.partial[ib] += float32(xb - float64(ib))
	}
	f.lo = min(f.lo, ia)
	f.hi = max(f.hi, min(ib, f.w-1))
}

// flush paints the accumulated row and clears it for the next one.
func (f *coverage) flush(canvas *image.RGBA, y int, colorAt func(x, y int) ([3]float64, float64)) {
	var run float32
	x0 := canvas.Bounds().Min.X
	for i := f.lo; i <= f.hi; i++ {
		run += f.runs[i]
		cov := (f.partial[i] + run) / float32(f.samples)
		f.partial[i], f.runs[i] = 0, 0
		if cov < 0.5/float32(f.samples) {
			continue
		}
		rgb, alpha := colorAt(x0+i, y)
		if alpha <= 0 {
			continue
		}
		blendPixel(canvas, x0+i, y, rgb, alpha*math.Min(float64(cov), 1))
	}
	if f.hi >= 0 {
		f.runs[f.hi+1] = 0
		f.runs[f.w] = 0
		f.partial[f.w] = 0
	}
}

// sortCrossings sorts c by x in place. A sub-scanline has few crossings, so an
// insertion sort is cheaper than the general-purpose sort.
func sortCrossings(c []crossing) {
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j].x < c[j-1].x; j-- {
			c[j], c[j-1] = c[j-1], c[j]
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

func buildEdges(contours [][]content.Point) []pathEdge {
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

type crossing struct {
	x   float64
	dir int
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
	// Every caller skips alpha <= 0, so outA is always positive.
	outA := alpha + ea*(1-alpha)
	blend := func(c, ec float64) float64 { return (c*alpha + ec*ea*(1-alpha)) / outA }
	storeRGBA64(pix, off, blend(rgb[0], er), blend(rgb[1], eg), blend(rgb[2], eb), outA)
}

// StrokePath flattens contours as open polylines and fills the quad swept
// by each segment at lineWidth/2 half-width (device space), with bevelled
// joins rather than mitered or rounded joins. This is an approximation: the
// rasterizer only produces flattened pages.
func StrokePath(canvas *image.RGBA, contours [][]content.Point, lineWidth float64, rgb [3]float64, alpha float64) {
	if alpha <= 0 {
		return
	}
	StrokePathShaded(canvas, contours, lineWidth, func(x, y int) ([3]float64, float64) {
		return rgb, alpha
	})
}

// StrokePathShaded strokes as StrokePath does, taking each pixel's colour
// from colorAt so a shading pattern can stroke as well as fill.
func StrokePathShaded(canvas *image.RGBA, contours [][]content.Point, lineWidth float64, colorAt func(x, y int) ([3]float64, float64)) {
	half := max(lineWidth/2, 0.5)
	// All segment quads are filled in one call, so each pixel is blended once
	// for the whole stroke and a translucent stroke does not darken where two
	// segments overlap.
	var quads [][]content.Point
	for _, contour := range contours {
		for i := 0; i+1 < len(contour); i++ {
			quads = append(quads, segmentQuad(contour[i], contour[i+1], half))
		}
	}
	fillPathFunc(canvas, quads, false, colorAt)
}

// segmentQuad returns the four corners of the rectangle covering segment
// p0-p1 at the given half-width, perpendicular to the segment direction.
func segmentQuad(p0, p1 content.Point, half float64) []content.Point {
	dx, dy := p1.X-p0.X, p1.Y-p0.Y
	length := math.Hypot(dx, dy)
	if length == 0 {
		return []content.Point{
			{X: p0.X - half, Y: p0.Y - half}, {X: p0.X + half, Y: p0.Y - half},
			{X: p0.X + half, Y: p0.Y + half}, {X: p0.X - half, Y: p0.Y + half},
		}
	}
	nx, ny := -dy/length*half, dx/length*half
	return []content.Point{
		{X: p0.X + nx, Y: p0.Y + ny}, {X: p1.X + nx, Y: p1.Y + ny},
		{X: p1.X - nx, Y: p1.Y - ny}, {X: p0.X - nx, Y: p0.Y - ny},
	}
}
