package raster

import (
	"math"

	"github.com/voidrab/gopdfrab/internal/content"
)

// flattenCubic subdivides a cubic Bezier into line segments such that the
// deviation from the true curve is within tol, returning points from p1 to
// p3 inclusive (p0 itself is not included, since callers already have it as
// the current point).
func flattenCubic(p0, p1, p2, p3 content.Point, tol float64) []content.Point {
	return flattenCubicDepth(p0, p1, p2, p3, tol, 0)
}

// flattenCubicDepth bounds subdivision depth so a pathological curve cannot
// recurse without end (e.g. an all-zero control polygon, where the flatness
// bound is identically zero).
func flattenCubicDepth(p0, p1, p2, p3 content.Point, tol float64, depth int) []content.Point {
	if depth >= 18 || cubicFlatEnough(p0, p1, p2, p3, tol) {
		return []content.Point{p3}
	}
	l0, l1, l2, l3, r0, r1, r2, r3 := splitCubic(p0, p1, p2, p3)
	left := flattenCubicDepth(l0, l1, l2, l3, tol, depth+1)
	right := flattenCubicDepth(r0, r1, r2, r3, tol, depth+1)
	return append(left, right...)
}

// cubicFlatEnough reports whether the control points p1, p2 lie within tol
// of the chord p0-p3, using the standard distance-to-line bound.
func cubicFlatEnough(p0, p1, p2, p3 content.Point, tol float64) bool {
	dx, dy := p3.X-p0.X, p3.Y-p0.Y
	d1 := math.Abs((p1.X-p3.X)*dy - (p1.Y-p3.Y)*dx)
	d2 := math.Abs((p2.X-p3.X)*dy - (p2.Y-p3.Y)*dx)
	if (d1+d2)*(d1+d2) < tol*(dx*dx+dy*dy) {
		return true
	}
	// Degenerate chord (p0≈p3): the area-based bound above is ~zero and never
	// converges, so fall back to bounding the control points' extent.
	if dx*dx+dy*dy < tol {
		ext := math.Max(math.Hypot(p1.X-p0.X, p1.Y-p0.Y), math.Hypot(p2.X-p0.X, p2.Y-p0.Y))
		return ext*ext < tol
	}
	return false
}

// splitCubic applies de Casteljau's algorithm to split a cubic Bezier at
// t=0.5 into two cubics covering the first and second half.
func splitCubic(p0, p1, p2, p3 content.Point) (l0, l1, l2, l3, r0, r1, r2, r3 content.Point) {
	mid := func(a, b content.Point) content.Point { return content.Point{X: (a.X + b.X) / 2, Y: (a.Y + b.Y) / 2} }
	p01 := mid(p0, p1)
	p12 := mid(p1, p2)
	p23 := mid(p2, p3)
	p012 := mid(p01, p12)
	p123 := mid(p12, p23)
	p0123 := mid(p012, p123)
	return p0, p01, p012, p0123, p0123, p123, p23, p3
}
