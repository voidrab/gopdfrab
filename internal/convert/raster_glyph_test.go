package convert

import (
	"os"
	"testing"

	"github.com/voidrab/gopdfrab/internal/verify"
)

func loadLiberationSansGID(t *testing.T, rune16 uint16) (map[string][]byte, int) {
	t.Helper()
	data, err := os.ReadFile("assets/fonts/LiberationSans-Regular.ttf")
	if err != nil {
		t.Fatalf("read font: %v", err)
	}
	tables, ok := verify.ParseSfnt(data)
	if !ok {
		t.Fatalf("parseSfnt failed")
	}
	gidMap := verify.ParseCmapFormat4(verify.TTWindowsBMPCmap(tables))
	if gidMap == nil {
		t.Fatalf("no cmap subtable")
	}
	gid, ok := gidMap[rune16]
	if !ok {
		t.Fatalf("no glyph mapped for rune %d", rune16)
	}
	return tables, int(gid)
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

func TestInterpretType2CharstringSquare(t *testing.T) {
	// A hand-built Type2 charstring tracing a 100x100 square via rmoveto +
	// rlineto, then endchar: moveto (10,10); lineto (110,10),(110,110),(10,110).
	enc := func(v int) byte { return byte(v + 139) }
	program := []byte{
		enc(10), enc(10), 21, // 10 10 rmoveto
		enc(100), enc(0), 5, // 100 0 rlineto
		enc(0), enc(100), 5, // 0 100 rlineto
		enc(-100), enc(0), 5, // -100 0 rlineto
		14, // endchar
	}
	contours := interpretType2Charstring(program)
	if len(contours) != 1 {
		t.Fatalf("got %d contours, want 1", len(contours))
	}
	pts := contours[0]
	if len(pts) != 4 {
		t.Fatalf("got %d points, want 4 (square corners)", len(pts))
	}
	want := []Point{{10, 10}, {110, 10}, {110, 110}, {10, 110}}
	for i, w := range want {
		if pts[i].X != w.X || pts[i].Y != w.Y {
			t.Errorf("point %d = %+v, want %+v", i, pts[i], w)
		}
	}
}

func TestInterpretType2CharstringHVCurve(t *testing.T) {
	enc := func(v int) byte { return byte(v + 139) }
	// 0 0 moveto, then a hvcurveto: dx1 dx2 dy2 dy3 (horizontal start) from (0,0).
	program := []byte{
		enc(0), enc(0), 21, // 0 0 rmoveto
		enc(50), enc(20), enc(20), enc(50), 31, // hvcurveto
		14,
	}
	contours := interpretType2Charstring(program)
	if len(contours) != 1 {
		t.Fatalf("got %d contours, want 1", len(contours))
	}
	pts := contours[0]
	last := pts[len(pts)-1]
	// Curve ends at x3=x2 (=50+20=70), y3=y2+50=20+50=70 for the horizontal-start 4-arg case.
	if last.X != 70 || last.Y != 70 {
		t.Errorf("curve endpoint = %+v, want (70,70)", last)
	}
}

func TestInterpretType1CharstringSquare(t *testing.T) {
	// hsbw sbx=0 wx=100, then rmoveto (10,10), rlineto x3 tracing a square,
	// closepath, endchar -- the Type1 counterpart of the Type2 square test.
	enc := func(v int) byte { return byte(v + 139) }
	program := []byte{
		enc(0), enc(100), 13, // 0 100 hsbw
		enc(10), enc(10), 21, // 10 10 rmoveto
		enc(100), 6, // 100 hlineto
		enc(100), 7, // 100 vlineto
		enc(-100), 6, // -100 hlineto
		9,  // closepath
		14, // endchar
	}
	contours := interpretType1Charstring(program)
	if len(contours) != 1 {
		t.Fatalf("got %d contours, want 1", len(contours))
	}
	pts := contours[0]
	want := []Point{{10, 10}, {110, 10}, {110, 110}, {10, 110}}
	if len(pts) != len(want) {
		t.Fatalf("got %d points, want %d", len(pts), len(want))
	}
	for i, w := range want {
		if pts[i].X != w.X || pts[i].Y != w.Y {
			t.Errorf("point %d = %+v, want %+v", i, pts[i], w)
		}
	}
}
