package pdfrab

import "math"

// Matrix is a PDF-style 2D affine transform: [A B 0; C D 0; E F 1],
// applied to a point as x' = A*x + C*y + E, y' = B*x + D*y + F.
type Matrix struct {
	A, B, C, D, E, F float64
}

// IdentityMatrix is the no-op transform.
var IdentityMatrix = Matrix{A: 1, D: 1}

// Point is a 2D coordinate.
type Point struct {
	X, Y float64
}

// Mul composes two matrices so that applying the result equals applying m
// then n (PDF's cm semantics: the new CTM is m concatenated with n).
func (m Matrix) Mul(n Matrix) Matrix {
	return Matrix{
		A: m.A*n.A + m.B*n.C,
		B: m.A*n.B + m.B*n.D,
		C: m.C*n.A + m.D*n.C,
		D: m.C*n.B + m.D*n.D,
		E: m.E*n.A + m.F*n.C + n.E,
		F: m.E*n.B + m.F*n.D + n.F,
	}
}

// Apply transforms a point by m.
func (m Matrix) Apply(p Point) Point {
	return Point{
		X: m.A*p.X + m.C*p.Y + m.E,
		Y: m.B*p.X + m.D*p.Y + m.F,
	}
}

// Invert returns m's inverse, or false if m is singular.
func (m Matrix) Invert() (Matrix, bool) {
	det := m.A*m.D - m.B*m.C
	if det == 0 {
		return Matrix{}, false
	}
	inv := 1 / det
	return Matrix{
		A: m.D * inv,
		B: -m.B * inv,
		C: -m.C * inv,
		D: m.A * inv,
		E: (m.C*m.F - m.D*m.E) * inv,
		F: (m.B*m.E - m.A*m.F) * inv,
	}, true
}

// flattenCubic subdivides a cubic Bezier into line segments such that the
// deviation from the true curve is within tol, returning points from p1 to
// p3 inclusive (p0 itself is not included, since callers already have it as
// the current point).
func flattenCubic(p0, p1, p2, p3 Point, tol float64) []Point {
	if cubicFlatEnough(p0, p1, p2, p3, tol) {
		return []Point{p3}
	}
	l0, l1, l2, l3, r0, r1, r2, r3 := splitCubic(p0, p1, p2, p3)
	left := flattenCubic(l0, l1, l2, l3, tol)
	right := flattenCubic(r0, r1, r2, r3, tol)
	return append(left, right...)
}

// cubicFlatEnough reports whether the control points p1, p2 lie within tol
// of the chord p0-p3, using the standard distance-to-line bound.
func cubicFlatEnough(p0, p1, p2, p3 Point, tol float64) bool {
	dx, dy := p3.X-p0.X, p3.Y-p0.Y
	d1 := math.Abs((p1.X-p3.X)*dy - (p1.Y-p3.Y)*dx)
	d2 := math.Abs((p2.X-p3.X)*dy - (p2.Y-p3.Y)*dx)
	if (d1+d2)*(d1+d2) < tol*(dx*dx+dy*dy) {
		return true
	}
	return false
}

// splitCubic applies de Casteljau's algorithm to split a cubic Bezier at
// t=0.5 into two cubics covering the first and second half.
func splitCubic(p0, p1, p2, p3 Point) (l0, l1, l2, l3, r0, r1, r2, r3 Point) {
	mid := func(a, b Point) Point { return Point{(a.X + b.X) / 2, (a.Y + b.Y) / 2} }
	p01 := mid(p0, p1)
	p12 := mid(p1, p2)
	p23 := mid(p2, p3)
	p012 := mid(p01, p12)
	p123 := mid(p12, p23)
	p0123 := mid(p012, p123)
	return p0, p01, p012, p0123, p0123, p123, p23, p3
}
