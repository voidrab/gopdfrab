package raster

import (
	"image"
	"math"

	"github.com/voidrab/gopdfrab/internal/content"
)

// clipMask is the soft edge of a non-rectangular clipping path: how much of
// each pixel the path covers, within rect. Outside rect nothing shows. A nil
// *clipMask clips nothing beyond the rectangle clip, and masks are immutable,
// so the graphics states q saves can share one.
type clipMask struct {
	rect image.Rectangle
	cov  []uint8 // rect.Dx() x rect.Dy() coverage, row by row
}

// at returns the fraction of the pixel at (x, y) the clip lets through.
func (m *clipMask) at(x, y int) float64 {
	if m == nil {
		return 1
	}
	if !image.Pt(x, y).In(m.rect) {
		return 0
	}
	return float64(m.cov[(y-m.rect.Min.Y)*m.rect.Dx()+(x-m.rect.Min.X)]) / 255
}

// wrap returns colorAt with the pixels the mask hides removed or faded.
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
	m.cov = make([]uint8, rect.Dx()*rect.Dy())
	scanCoverage(rect, contours, evenOdd, true, func(x, y int, cov float32) {
		v := float64(cov) * parent.at(x, y)
		m.cov[(y-rect.Min.Y)*rect.Dx()+(x-rect.Min.X)] = uint8(v*255 + 0.5)
	})
	return m
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
