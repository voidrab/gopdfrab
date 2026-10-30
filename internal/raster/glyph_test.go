package raster

import (
	"encoding/binary"
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

// buildTriangleGlyfTables returns a tables map holding a single 3-point
// simple glyph at gid 0, enough to drive ttGlyfContours/ttCompositeContours
// directly without assembling a full sfnt binary.
func buildTriangleGlyfTables() map[string][]byte {
	var glyf []byte
	putU16 := func(v uint16) { glyf = binary.BigEndian.AppendUint16(glyf, v) }
	putI16 := func(v int16) { putU16(uint16(v)) }
	putI16(1) // numberOfContours
	putI16(0)
	putI16(0)
	putI16(0)
	putI16(0)                                 // bbox, unused
	putU16(2)                                 // endPtsOfContours[0]: 3 points
	putU16(0)                                 // instructionLength
	glyf = append(glyf, 0x01, 0x01, 0x01)     // 3 on-curve points, no repeat
	for _, d := range []int16{0, 100, -100} { // x deltas
		putI16(d)
	}
	for _, d := range []int16{0, 100, -100} { // y deltas
		putI16(d)
	}
	if len(glyf)%2 != 0 { // short-format loca offsets are in 2-byte units
		glyf = append(glyf, 0)
	}

	loca := make([]byte, 4) // short format: gid0 spans [0, len(glyf)/2)
	binary.BigEndian.PutUint16(loca[2:], uint16(len(glyf)/2))

	head := make([]byte, 52)
	binary.BigEndian.PutUint16(head[18:20], 1000) // unitsPerEm
	// head[50:52] left zero => indexToLocFormat = short

	return map[string][]byte{"head": head, "loca": loca, "glyf": glyf}
}

// TestTTCompositeContoursByteArgScaleVariants covers the byte-argument
// component form (as opposed to the word-argument form the other composite
// test exercises) together with each of the three scale encodings.
func TestTTCompositeContoursByteArgScaleVariants(t *testing.T) {
	tables := buildTriangleGlyfTables()

	buildRec := func(flags uint16, scale ...uint16) []byte {
		var rec []byte
		rec = binary.BigEndian.AppendUint16(rec, 0xFFFF) // numberOfContours = -1 (composite)
		rec = binary.BigEndian.AppendUint16(rec, 0)      // xMin
		rec = binary.BigEndian.AppendUint16(rec, 0)      // yMin
		rec = binary.BigEndian.AppendUint16(rec, 0)      // xMax
		rec = binary.BigEndian.AppendUint16(rec, 0)      // yMax
		rec = binary.BigEndian.AppendUint16(rec, flags)
		rec = binary.BigEndian.AppendUint16(rec, 0) // component gid = 0
		rec = append(rec, 10, 5)                    // byte args: dx=10 dy=5
		for _, s := range scale {
			rec = binary.BigEndian.AppendUint16(rec, s)
		}
		return rec
	}

	const argsAreXY = 0x0002
	tests := []struct {
		name  string
		flags uint16
		scale []uint16
	}{
		{"WE_HAVE_A_SCALE", argsAreXY | 0x0008, []uint16{16384}},                // 1.0
		{"WE_HAVE_AN_X_AND_Y_SCALE", argsAreXY | 0x0040, []uint16{16384, 8192}}, // 1.0, 0.5
		{"WE_HAVE_A_TWO_BY_TWO", argsAreXY | 0x0080, []uint16{16384, 0, 0, 16384}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := buildRec(tt.flags, tt.scale...)
			contours, ok := ttCompositeContours(tables, rec, 0)
			if !ok {
				t.Fatalf("ttCompositeContours failed")
			}
			if len(contours) != 1 || len(contours[0]) != 3 {
				t.Fatalf("got %d contours, want 1 with 3 points", len(contours))
			}
		})
	}
}

// TestTTSimpleGlyphContoursDescendingEndPoints covers a glyph whose contour
// endpoints do not climb. The point arrays are sized from the last endpoint
// but indexed by every one of them, so a font claiming a huge early contour
// and a tiny last one would read past the end of them. Such a read panics in a
// rasterizer worker goroutine, which nothing recovers, so the glyph is rejected
// instead.
func TestTTSimpleGlyphContoursDescendingEndPoints(t *testing.T) {
	build := func(endPts ...uint16) []byte {
		var rec []byte
		put := func(v uint16) { rec = binary.BigEndian.AppendUint16(rec, v) }
		put(uint16(len(endPts))) // numberOfContours
		put(0)
		put(0)
		put(0)
		put(0) // bbox, unused
		for _, e := range endPts {
			put(e)
		}
		put(0) // instructionLength
		// Point data for the two points the last endpoint claims, which is
		// all a well-formed reader would ever look at.
		rec = append(rec, 0x07, 0x07)
		return append(rec, 0, 0, 0, 0)
	}

	for _, tc := range []struct {
		name   string
		endPts []uint16
	}{
		{"first contour ends past the last", []uint16{60000, 1}},
		{"endpoints descend by one", []uint16{2, 1}},
		{"repeated then smaller", []uint16{5, 5, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The point is that this returns rather than panicking.
			if _, ok := ttSimpleGlyphContours(build(tc.endPts...)); ok {
				t.Error("a glyph with non-climbing contour endpoints was accepted")
			}
		})
	}

	// Equal consecutive endpoints are legal (an empty contour) and must still
	// be accepted, so the guard cannot just reject any non-increase.
	if _, ok := ttSimpleGlyphContours(build(0, 1)); !ok {
		t.Error("a glyph with climbing endpoints was rejected")
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
