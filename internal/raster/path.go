package raster

import (
	"cmp"
	"image"
	"math"
	"slices"
	"sync"

	"github.com/voidrab/gopdfrab/internal/content"
)

// FillPath rasterizes contours (each a closed polygon in device-space
// pixel coordinates) onto canvas using a classic active-edge-table scanline
// fill, with nonzero or even-odd winding per the f/f* operator, blending
// rgb at alpha over the existing pixels.
func FillPath(canvas *image.RGBA, contours [][]content.Point, rgb [3]float64, alpha float64, evenOdd bool) {
	fillSolid(canvas, contours, evenOdd, rgb, alpha, nil)
}

// fillSolid is FillPath within a clip mask, which may be nil. It works a run
// of equally covered pixels at a time: the interior of a shape is a few long
// runs, written as bytes when opaque, and only edge pixels and translucent
// ones are blended one by one.
func fillSolid(canvas *image.RGBA, contours [][]content.Point, evenOdd bool, rgb [3]float64, alpha float64, mask *clipMask) {
	if alpha <= 0 {
		return
	}
	var opaque [4]byte
	storeRGBA64(opaque[:], 0, rgb[0], rgb[1], rgb[2], 1)
	// Runs and mask spans are never empty, so the byte slice passed to
	// fillBytes is never empty.
	paint := func(y, x0, x1 int, a float64) {
		if a < 1 {
			for x := x0; x < x1; x++ {
				blendPixel(canvas, x, y, rgb, a)
			}
			return
		}
		fillBytes(canvas.Pix[canvas.PixOffset(x0, y):canvas.PixOffset(x1, y)], opaque)
	}
	scanRuns(canvas.Bounds(), contours, evenOdd, true, func(y, x0, x1 int, cov float32) {
		if mask == nil {
			paint(y, x0, x1, alpha*float64(cov))
			return
		}
		mask.eachSpan(y, x0, x1, func(sx0, sx1 int, mc float64) {
			if a := alpha * mc; a > 0 {
				paint(y, sx0, sx1, a*float64(cov))
			}
		})
	})
}

// fillBytes sets every pixel of row, a non-empty whole number of four-byte
// pixels, to px.
func fillBytes(row []byte, px [4]byte) {
	copy(row, px[:])
	// Doubling copies move the bytes written so far, rather than one pixel at
	// a time.
	for filled := 4; filled < len(row); filled *= 2 {
		copy(row[filled:], row[:filled])
	}
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
	scanCoverage(canvas.Bounds(), contours, evenOdd, aa, func(x, y int, cov float32) {
		rgb, alpha := colorAt(x, y)
		if alpha <= 0 {
			return
		}
		blendPixel(canvas, x, y, rgb, alpha*float64(cov))
	})
}

// scanCoverage rasterizes contours within bounds and calls emit for each pixel
// they cover, with the covered fraction in (0, 1]. Pixels whose coverage rounds
// to zero are skipped. Pixels are visited row by row, left to right. With aa
// false a pixel is covered whole or not at all, by whether its centre is
// inside.
func scanCoverage(bounds image.Rectangle, contours [][]content.Point, evenOdd, aa bool, emit func(x, y int, cov float32)) {
	scanRuns(bounds, contours, evenOdd, aa, func(y, x0, x1 int, cov float32) {
		for x := x0; x < x1; x++ {
			emit(x, y, cov)
		}
	})
}

// scanRuns is scanCoverage reporting runs: emit is called for each stretch
// [x0, x1) of row y over which the covered fraction is the constant cov, in
// (0, 1]. A shape's interior is a few long runs and its edges short ones, so
// the work done is proportional to the outline, not the area.
func scanRuns(bounds image.Rectangle, contours [][]content.Point, evenOdd, aa bool, emit func(y, x0, x1 int, cov float32)) {
	if len(contours) == 0 {
		return
	}
	f := scanScratch.Get().(*coverage)
	defer releaseScratch(f)
	edges := buildEdges(f.edges[:0], contours)
	f.edges = edges // kept, however this ends, for the next fill to reuse
	// An edge wholly above or below the rows scanned changes none of them. A
	// glyph far outside a tile or a clip then costs nothing but this pass.
	keep := edges[:0]
	for _, e := range edges {
		if e.y1 > float64(bounds.Min.Y) && e.y0 < float64(bounds.Max.Y) {
			keep = append(keep, e)
		}
	}
	edges = keep
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
	minY := max(bounds.Min.Y, int(math.Floor(edgeMinY)))
	maxY := min(bounds.Max.Y, int(math.Ceil(edgeMaxY)))
	// The pixel columns are limited to the edges' x-extent plus one pixel on
	// each side, so the per-row buffers are as wide as the shape, not the
	// canvas.
	edgeMinX, edgeMaxX := math.Inf(1), math.Inf(-1)
	for _, e := range edges {
		x1 := e.x0 + (e.y1-e.y0)*e.dxdy
		edgeMinX = math.Min(edgeMinX, math.Min(e.x0, x1))
		edgeMaxX = math.Max(edgeMaxX, math.Max(e.x0, x1))
	}
	minX := max(bounds.Min.X, int(math.Floor(edgeMinX))-1)
	maxX := min(bounds.Max.X, int(math.Ceil(edgeMaxX))+1)
	w := maxX - minX
	if minY >= maxY || w <= 0 {
		return
	}

	samples := 1
	if aa {
		samples = aaSamples
	}
	f.edges, f.evenOdd, f.aa, f.samples, f.w, f.minX = edges, evenOdd, aa, samples, w, float64(minX)
	f.next, f.active, f.cross, f.marks = 0, f.active[:0], f.cross[:0], f.marks[:0]
	f.partial, f.runs = zeroed(f.partial, w+1), zeroed(f.runs, w+2)
	for y := minY; y < maxY; y++ {
		f.row(y)
		f.flush(minX, y, emit)
	}
}

// scanScratch holds the buffers of finished fills for the next ones: showText
// fills every glyph on its own, and allocating them afresh for each was most
// of what rendering a page of text allocated.
var scanScratch = sync.Pool{New: func() any { return new(coverage) }}

// maxScratchEdges and maxScratchWidth bound the buffers kept in scanScratch, so
// that one huge path does not hold its memory for the fills after it.
const (
	maxScratchEdges = 1 << 16
	maxScratchWidth = 1 << 14
)

// releaseScratch returns f to scanScratch unless its buffers grew too large.
func releaseScratch(f *coverage) {
	if cap(f.edges) > maxScratchEdges || cap(f.partial) > maxScratchWidth+2 || cap(f.runs) > maxScratchWidth+2 {
		return
	}
	scanScratch.Put(f)
}

// zeroed returns s resized to n with every element zero, reusing its memory
// when it is large enough.
func zeroed(s []float32, n int) []float32 {
	if cap(s) < n {
		return make([]float32, n)
	}
	s = s[:n]
	clear(s)
	return s
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
	marks   []int     // the pixels of the current row where partial or runs is set
}

// row samples the sub-scanlines of pixel row y.
func (f *coverage) row(y int) {
	f.marks = f.marks[:0]
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
	f.marks = append(f.marks, ia)
	if ib > ia {
		f.marks = append(f.marks, ib)
		if ib > ia+1 {
			f.marks = append(f.marks, ia+1)
		}
	}
}

// flush reports the accumulated row as runs and clears it for the next one.
// Coverage only changes at the marked pixels, so between two of them it is
// the constant that the difference array has summed to.
func (f *coverage) flush(x0, y int, emit func(y, x0, x1 int, cov float32)) {
	if len(f.marks) == 0 {
		return
	}
	slices.Sort(f.marks)
	marks := slices.Compact(f.marks)
	thr := 0.5 / float32(f.samples)
	var run float32
	for k, m := range marks {
		run += f.runs[m]
		cov := (f.partial[m] + run) / float32(f.samples)
		f.partial[m], f.runs[m] = 0, 0
		if m < f.w && cov >= thr {
			emit(y, x0+m, x0+m+1, min(cov, 1))
		}
		end := f.w
		if k+1 < len(marks) {
			end = marks[k+1]
		}
		if between := run / float32(f.samples); end > m+1 && between >= thr {
			emit(y, x0+m+1, x0+end, min(between, 1))
		}
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

// buildEdges appends the non-horizontal edges of contours to edges, growing it
// once for all of them, and returns it.
func buildEdges(edges []pathEdge, contours [][]content.Point) []pathEdge {
	n := 0
	for _, c := range contours {
		n += len(c)
	}
	edges = slices.Grow(edges, n)
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

// StrokePath strokes contours, which are flattened subpaths in device space,
// with a solid line lineWidth pixels wide (at least one pixel), butt caps and
// mitered joins, the PDF defaults. See StrokePathStyled for the other styles.
func StrokePath(canvas *image.RGBA, contours [][]content.Point, lineWidth float64, rgb [3]float64, alpha float64) {
	StrokePathStyled(canvas, contours, StrokeStyle{Width: lineWidth}, rgb, alpha)
}

// StrokePathStyled strokes contours as st describes, blending rgb at alpha.
func StrokePathStyled(canvas *image.RGBA, contours [][]content.Point, st StrokeStyle, rgb [3]float64, alpha float64) {
	if alpha <= 0 {
		return
	}
	StrokePathStyledShaded(canvas, contours, st, func(x, y int) ([3]float64, float64) {
		return rgb, alpha
	})
}

// StrokePathStyledShaded strokes as StrokePathStyled does, taking each
// pixel's colour from colorAt.
func StrokePathStyledShaded(canvas *image.RGBA, contours [][]content.Point, st StrokeStyle, colorAt func(x, y int) ([3]float64, float64)) {
	// The pieces of the stroke are filled in one call, so each pixel is
	// blended once and a translucent stroke does not darken where they
	// overlap.
	fillPathFunc(canvas, strokeOutline(contours, st), false, colorAt)
}
