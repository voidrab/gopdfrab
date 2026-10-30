package raster

import (
	"os"
	"testing"

	"github.com/voidrab/gopdfrab/internal/fontprog"
)

func loadLiberationSansGID(t *testing.T, rune16 uint16) (map[string][]byte, int) {
	t.Helper()
	data, err := os.ReadFile("../fontsubst/fonts/LiberationSans-Regular.ttf")
	if err != nil {
		t.Fatalf("read font: %v", err)
	}
	tables, ok := fontprog.ParseSfnt(data)
	if !ok {
		t.Fatalf("parseSfnt failed")
	}
	gidMap := fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(tables))
	if gidMap == nil {
		t.Fatalf("no cmap subtable")
	}
	gid, ok := gidMap[rune16]
	if !ok {
		t.Fatalf("no glyph mapped for rune %d", rune16)
	}
	return tables, int(gid)
}

func TestGlyphOutlineFromTrueTypeSimpleGlyph(t *testing.T) {
	tables, gid := loadLiberationSansGID(t, 'A')
	path, ok := glyphOutlineFromTrueType(tables, gid)
	if !ok {
		t.Fatalf("glyphOutlineFromTrueType failed")
	}
	if len(path.Contours) == 0 {
		t.Fatalf("'A' should have at least one contour")
	}
	minX, minY, maxX, maxY := boundsOf(path.Contours)
	// A 1000-unit-em glyph's bounding box should sit within a generous
	// margin of the em square, and have a sane non-degenerate extent.
	if minX < -200 || maxX > 1200 || minY < -200 || maxY > 1200 {
		t.Errorf("'A' bbox = (%v,%v)-(%v,%v), want roughly within the em square", minX, minY, maxX, maxY)
	}
	if maxX-minX < 100 || maxY-minY < 100 {
		t.Errorf("'A' bbox = (%v,%v)-(%v,%v), too small to be a real glyph", minX, minY, maxX, maxY)
	}
}

func TestGlyphOutlineFromTrueTypeCompositeGlyph(t *testing.T) {
	// A diacritic-bearing glyph (e.g. Acircumflex, U+00C2) is commonly built
	// as a TrueType composite of base 'A' + a circumflex component.
	tables, gid := loadLiberationSansGID(t, 0x00C2)
	path, ok := glyphOutlineFromTrueType(tables, gid)
	if !ok {
		t.Fatalf("glyphOutlineFromTrueType failed")
	}
	if len(path.Contours) < 2 {
		t.Errorf("composite glyph should have multiple contours (base + accent), got %d", len(path.Contours))
	}
}

func TestGlyphOutlineFromTrueTypeSpaceIsEmpty(t *testing.T) {
	tables, gid := loadLiberationSansGID(t, ' ')
	path, ok := glyphOutlineFromTrueType(tables, gid)
	if !ok {
		t.Fatalf("glyphOutlineFromTrueType failed")
	}
	if len(path.Contours) != 0 {
		t.Errorf("space glyph should have no contours, got %d", len(path.Contours))
	}
}

func boundsOf(contours [][]Point) (minX, minY, maxX, maxY float64) {
	first := true
	for _, c := range contours {
		for _, p := range c {
			if first {
				minX, maxX, minY, maxY = p.X, p.X, p.Y, p.Y
				first = false
				continue
			}
			if p.X < minX {
				minX = p.X
			}
			if p.X > maxX {
				maxX = p.X
			}
			if p.Y < minY {
				minY = p.Y
			}
			if p.Y > maxY {
				maxY = p.Y
			}
		}
	}
	return
}

// TestContourSink pins how a charstring outline becomes polygon contours:
// every MoveTo starts a contour, a contour that draws nothing is dropped, and
// curves are flattened from the current point.
func TestContourSink(t *testing.T) {
	var s contourSink
	s.MoveTo(0, 0) // drawn nothing: dropped
	s.MoveTo(10, 0)
	s.LineTo(20, 0)
	s.CurveTo(30, 0, 40, 10, 40, 20)
	s.MoveTo(5, 5)
	s.LineTo(5, 6)
	got := s.glyphPath().Contours
	if len(got) != 2 {
		t.Fatalf("contours = %v, want two", got)
	}
	c := got[0]
	if c[0] != (Point{10, 0}) || c[1] != (Point{20, 0}) || c[len(c)-1] != (Point{40, 20}) {
		t.Errorf("first contour = %v, want {10 0} {20 0} ... {40 20}", c)
	}
	if len(c) < 4 {
		t.Errorf("curve was not flattened: %v", c)
	}
	if got[1][0] != (Point{5, 5}) || got[1][1] != (Point{5, 6}) {
		t.Errorf("second contour = %v, want {5 5} {5 6}", got[1])
	}
}
