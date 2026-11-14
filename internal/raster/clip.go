package raster

import (
	"image"
	"math"

	"github.com/voidrab/gopdfrab/internal/content"
)

// clipSpan is a stretch of pixels [x0, x1) in one row of a clip mask that
// the clip lets through at the fraction cov/255.
type clipSpan struct {
	x0, x1 int32
	cov    uint8
}

// clipMask is the coverage of a non-rectangular clip path, as the spans of
// each row within rect; pixels in no span, and outside rect, are clipped away.
// A nil *clipMask clips nothing beyond the rectangle clip. Masks are
// immutable, so the graphics states saved by the q operator can share one.
//
// A row holds the runs of a shape's interior and the single pixels of its
// edges, so a mask costs in proportion to the clip's outline, not its area.
type clipMask struct {
	rect image.Rectangle
	rows [][]clipSpan // rect.Dy() rows, each sorted by x and without overlaps
}

// at returns the fraction of the pixel at (x, y) the clip lets through.
func (m *clipMask) at(x, y int) float64 {
	if m == nil {
		return 1
	}
	if !image.Pt(x, y).In(m.rect) {
		return 0
	}
	row := m.rows[y-m.rect.Min.Y]
	// Rows are short; a binary search only pays off in the odd long one. It
	// narrows to the first span ending after x, which lies in [lo, hi].
	lo, hi := 0, len(row)
	for hi-lo > 8 {
		mid := (lo + hi) / 2
		if int(row[mid].x1) <= x {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	for _, sp := range row[lo:min(hi+1, len(row))] {
		if int(sp.x1) > x {
			if int(sp.x0) <= x {
				return float64(sp.cov) / 255
			}
			break
		}
	}
	return 0
}

// wrap returns colorAt with its alpha scaled by the mask coverage at each
// pixel. A nil mask returns colorAt unchanged.
func (m *clipMask) wrap(colorAt func(x, y int) ([3]float64, float64)) func(x, y int) ([3]float64, float64) {
	if m == nil {
		return colorAt
	}
	return func(x, y int) ([3]float64, float64) {
		rgb, alpha := colorAt(x, y)
		if alpha <= 0 {
			return rgb, 0
		}
		return rgb, alpha * m.at(x, y)
	}
}

// constant returns a colour function that gives rgb at alpha everywhere.
func constant(rgb [3]float64, alpha float64) func(x, y int) ([3]float64, float64) {
	return func(int, int) ([3]float64, float64) { return rgb, alpha }
}

// newClipMask rasterizes contours into the mask of a clip within bounds,
// narrowed by parent, the clip already in force. The result's rect is empty
// when nothing can show through.
func newClipMask(bounds image.Rectangle, contours [][]content.Point, evenOdd bool, parent *clipMask) *clipMask {
	rect := bounds
	if parent != nil {
		rect = rect.Intersect(parent.rect)
	}
	minX, minY, maxX, maxY := boundsOfContours(contours)
	rect = rect.Intersect(image.Rect(int(math.Floor(minX)), int(math.Floor(minY)), int(math.Ceil(maxX)), int(math.Ceil(maxY))))
	m := &clipMask{rect: rect}
	if rect.Empty() {
		return m
	}
	m.rows = make([][]clipSpan, rect.Dy())
	// Runs arrive row by row, left to right, so the parent's spans in the
	// current row can be walked once with a moving index.
	var parentRow []clipSpan
	pi, parentY := 0, math.MinInt
	scanRuns(rect, contours, evenOdd, true, func(y, x0, x1 int, cov float32) {
		// The scanner reports no coverage under 1/16, so u is never zero.
		u := uint8(float64(cov)*255 + 0.5)
		row := &m.rows[y-rect.Min.Y]
		if parent == nil {
			*row = appendSpan(*row, x0, x1, u)
			return
		}
		if y != parentY {
			parentY, pi = y, 0
			parentRow = parent.rows[y-parent.rect.Min.Y]
		}
		for pi < len(parentRow) && int(parentRow[pi].x1) <= x0 {
			pi++
		}
		for j := pi; j < len(parentRow) && int(parentRow[j].x0) < x1; j++ {
			p := parentRow[j]
			if v := uint8((uint32(u)*uint32(p.cov) + 127) / 255); v > 0 {
				*row = appendSpan(*row, max(x0, int(p.x0)), min(x1, int(p.x1)), v)
			}
		}
	})
	return m
}

// appendSpan adds the pixels [x0, x1) at coverage cov to a row, merging with
// the span before it when they touch and agree.
func appendSpan(row []clipSpan, x0, x1 int, cov uint8) []clipSpan {
	if n := len(row); n > 0 && int(row[n-1].x1) == x0 && row[n-1].cov == cov {
		row[n-1].x1 = int32(x1)
		return row
	}
	return append(row, clipSpan{int32(x0), int32(x1), cov})
}

// axisAlignedRect reports whether contours are a single axis-aligned
// rectangle, which a bounding-box clip represents exactly, and returns it.
func axisAlignedRect(contours [][]content.Point) ([4]float64, bool) {
	if len(contours) != 1 {
		return [4]float64{}, false
	}
	pts := contours[0]
	if n := len(pts); n == 5 && pts[0] == pts[4] {
		pts = pts[:4]
	}
	if len(pts) != 4 {
		return [4]float64{}, false
	}
	const eps = 1e-6
	for i, p := range pts {
		q := pts[(i+1)%4]
		if math.Abs(p.X-q.X) > eps && math.Abs(p.Y-q.Y) > eps {
			return [4]float64{}, false
		}
	}
	minX, minY, maxX, maxY := boundsOfContours(contours)
	return [4]float64{minX, minY, maxX, maxY}, true
}
