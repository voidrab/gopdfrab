package raster

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/content"
)

func TestFlattenCubicStraightLine(t *testing.T) {
	p0 := content.Point{X: 0, Y: 0}
	p1 := content.Point{X: 1, Y: 0}
	p2 := content.Point{X: 2, Y: 0}
	p3 := content.Point{X: 3, Y: 0}
	pts := flattenCubic(p0, p1, p2, p3, 0.01)
	if len(pts) != 1 {
		t.Fatalf("a degenerate straight cubic should flatten to a single segment, got %d points", len(pts))
	}
	if pts[0] != p3 {
		t.Errorf("last point = %v, want %v", pts[0], p3)
	}
}

func TestFlattenCubicCurved(t *testing.T) {
	p0 := content.Point{X: 0, Y: 0}
	p1 := content.Point{X: 0, Y: 50}
	p2 := content.Point{X: 100, Y: 50}
	p3 := content.Point{X: 100, Y: 0}

	loose := flattenCubic(p0, p1, p2, p3, 1.0)
	tight := flattenCubic(p0, p1, p2, p3, 0.001)

	if len(tight) <= len(loose) {
		t.Errorf("tighter tolerance should produce more points: loose=%d tight=%d", len(loose), len(tight))
	}
	if tight[len(tight)-1] != p3 {
		t.Errorf("last point = %v, want %v", tight[len(tight)-1], p3)
	}

	// Every flattened point should stay within the curve's convex hull bounding box.
	for _, p := range tight {
		if p.X < -1 || p.X > 101 || p.Y < -1 || p.Y > 51 {
			t.Errorf("flattened point %v outside expected bounds", p)
		}
	}
}
