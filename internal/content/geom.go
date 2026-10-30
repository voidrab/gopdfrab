package content

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
