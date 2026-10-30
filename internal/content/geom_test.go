package content

import (
	"math"
	"testing"
)

func TestMatrixMulComposition(t *testing.T) {
	translate := Matrix{A: 1, D: 1, E: 10, F: 20}
	scale := Matrix{A: 2, D: 3}

	// Applying translate then scale should match applying the composed matrix.
	composed := translate.Mul(scale)
	p := Point{X: 1, Y: 1}

	got := composed.Apply(p)
	want := scale.Apply(translate.Apply(p))
	if got != want {
		t.Errorf("composed.Apply(p) = %v, want %v", got, want)
	}
}

func TestMatrixApplyIdentity(t *testing.T) {
	p := Point{X: 3.5, Y: -2.25}
	if got := IdentityMatrix.Apply(p); got != p {
		t.Errorf("IdentityMatrix.Apply(%v) = %v, want %v", p, got, p)
	}
}

func TestMatrixApplyTranslateScale(t *testing.T) {
	m := Matrix{A: 2, D: 3, E: 5, F: 7}
	got := m.Apply(Point{X: 1, Y: 1})
	want := Point{X: 7, Y: 10}
	if got != want {
		t.Errorf("m.Apply = %v, want %v", got, want)
	}
}

func TestMatrixInvertSingular(t *testing.T) {
	m := Matrix{A: 1, B: 2, C: 2, D: 4}
	if _, ok := m.Invert(); ok {
		t.Error("Invert() should report false for a singular matrix")
	}
}

func TestMatrixInvert(t *testing.T) {
	m := Matrix{A: 2, B: 0, C: 0, D: 4, E: 1, F: 2}
	inv, ok := m.Invert()
	if !ok {
		t.Fatal("Invert() reported singular for a non-singular matrix")
	}
	p := Point{X: 10, Y: -3}
	roundTrip := inv.Apply(m.Apply(p))
	if math.Abs(roundTrip.X-p.X) > 1e-9 || math.Abs(roundTrip.Y-p.Y) > 1e-9 {
		t.Errorf("round trip = %v, want %v", roundTrip, p)
	}
}
