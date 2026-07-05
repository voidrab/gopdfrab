package convert

import (
	"encoding/binary"
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

func b139(v int) byte { return byte(v + 139) }

// TestType2CharstringOperators exercises the Type2 interpreter across the
// curve operators and every number encoding (single-byte, 247/251 ranges, the
// 28 int16 form, and the 255 16.16 fixed form).
func TestType2CharstringOperators(t *testing.T) {
	var p []byte
	add := func(bs ...byte) { p = append(p, bs...) }

	add(b139(100), b139(10), b139(20), 18)                                          // width=100, hstemhm 10 20
	add(b139(5), b139(5), 21)                                                       // rmoveto
	add(247, 0, 251, 0, 5)                                                          // rlineto with 247/251-range numbers
	add(28, 0x00, 0x0A, 28, 0x00, 0x0A, 5)                                          // rlineto with int16 numbers
	add(255, 0, 1, 0, 0, 255, 0, 1, 0, 0, 5)                                        // rlineto with 16.16 fixed numbers
	add(b139(10), 6)                                                                // hlineto
	add(b139(10), 7)                                                                // vlineto
	add(b139(5), b139(5), b139(5), b139(5), b139(5), b139(5), 8)                    // rrcurveto
	add(b139(5), b139(5), b139(5), b139(5), 26)                                     // vvcurveto
	add(b139(5), b139(5), b139(5), b139(5), 27)                                     // hhcurveto
	add(b139(5), b139(5), b139(5), b139(5), 30)                                     // vhcurveto
	add(b139(5), b139(5), b139(5), b139(5), 31)                                     // hvcurveto
	add(b139(5), b139(5), b139(5), b139(5), b139(5), b139(5), b139(5), b139(5), 24) // rcurveline
	add(b139(5), b139(5), b139(5), b139(5), b139(5), b139(5), b139(5), b139(5), 25) // rlinecurve
	add(14)                                                                         // endchar

	contours := interpretType2Charstring(p)
	if len(contours) == 0 {
		t.Fatal("interpretType2Charstring produced no contours")
	}
}

// TestType1CharstringOperators exercises the Type1 interpreter's moveto/curve
// operators and its number encodings (247/251 ranges and the 255 int32 form).
func TestType1CharstringOperators(t *testing.T) {
	var p []byte
	add := func(bs ...byte) { p = append(p, bs...) }

	add(b139(0), b139(100), 13)                                  // hsbw
	add(b139(20), 4)                                             // vmoveto
	add(b139(30), 22)                                            // hmoveto
	add(247, 0, 251, 0, 5)                                       // rlineto with 247/251-range numbers
	add(b139(5), b139(5), b139(5), b139(5), b139(5), b139(5), 8) // rrcurveto
	add(b139(5), b139(5), b139(5), b139(5), 30)                  // vhcurveto
	add(b139(5), b139(5), b139(5), b139(5), 31)                  // hvcurveto
	add(255, 0, 0, 0, 50, b139(0), 21)                           // rmoveto with int32 number
	add(9, 14)                                                   // closepath, endchar

	contours := interpretType1Charstring(p)
	if len(contours) == 0 {
		t.Fatal("interpretType1Charstring produced no contours")
	}
}

// TestReadType2NumberShortBuffer covers the short-buffer fallbacks in every
// multi-byte encoding (247/251/28/255 ranges) plus the unmatched-byte default.
func TestReadType2NumberShortBuffer(t *testing.T) {
	tests := []struct {
		name  string
		in    []byte
		wantV float64
		wantN int
	}{
		{"247-range truncated", []byte{247}, 0, 1},
		{"251-range truncated", []byte{251}, 0, 1},
		{"28-form fully truncated", []byte{28}, 0, 1},
		{"28-form partially truncated", []byte{28, 0x00}, 0, 2},
		{"255-form truncated", []byte{255, 0, 1}, 0, 3},
		{"unmatched byte default", []byte{0}, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, n := readType2Number(tt.in)
			if v != tt.wantV || n != tt.wantN {
				t.Errorf("readType2Number(%v) = (%v,%v), want (%v,%v)", tt.in, v, n, tt.wantV, tt.wantN)
			}
		})
	}
}

// TestReadType1NumberShortBuffer mirrors TestReadType2NumberShortBuffer for
// the Type1 number encoding (no 28-form; 255 is a plain int32, not fixed).
func TestReadType1NumberShortBuffer(t *testing.T) {
	tests := []struct {
		name  string
		in    []byte
		wantV float64
		wantN int
	}{
		{"247-range truncated", []byte{247}, 0, 1},
		{"251-range truncated", []byte{251}, 0, 1},
		{"255-form truncated", []byte{255, 0, 1}, 0, 3},
		{"unmatched byte default", []byte{0}, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, n := readType1Number(tt.in)
			if v != tt.wantV || n != tt.wantN {
				t.Errorf("readType1Number(%v) = (%v,%v), want (%v,%v)", tt.in, v, n, tt.wantV, tt.wantN)
			}
		})
	}
}

// TestType2CharstringEscapeAndHintmask covers the two-byte escape operator
// (12, a no-op on outline geometry) and the hintmask/cntrmask mask-byte skip.
func TestType2CharstringEscapeAndHintmask(t *testing.T) {
	var p []byte
	add := func(bs ...byte) { p = append(p, bs...) }

	add(b139(0), b139(4), b139(0), b139(4), 18) // hstemhm: 2 stems, no leading width
	add(19, 0xFF)                               // hintmask + 1 mask byte (ceil(2 stems/8))
	add(12, 0)                                  // two-byte escape, ignored
	add(b139(10), b139(10), 21)                 // rmoveto
	add(b139(20), b139(0), 5)                   // rlineto
	add(14)                                     // endchar

	contours := interpretType2Charstring(p)
	if len(contours) == 0 {
		t.Fatal("interpretType2Charstring produced no contours")
	}
}

// TestType1CharstringSubrEscapes covers callsubr/return and the div/seac/sbw
// two-byte escapes, none of which are exercised by the other Type1 tests.
func TestType1CharstringSubrEscapes(t *testing.T) {
	var p []byte
	add := func(bs ...byte) { p = append(p, bs...) }

	add(b139(0), b139(100), 13)                             // hsbw: sbx=0 wx=100
	add(b139(10), b139(10), 21)                             // rmoveto (10,10)
	add(b139(5), 10)                                        // push 5, callsubr (drops it)
	add(11)                                                 // return, no-op
	add(b139(10), b139(2), 12, 12)                          // div: 10 2 -> 5.0
	add(22)                                                 // hmoveto consumes the div result
	add(b139(1), b139(2), b139(3), b139(4), b139(5), 12, 6) // seac: unsupported, stack cleared
	add(b139(0), b139(0), b139(50), b139(0), 12, 7)         // sbw: sbx=0 sby=0 -> x,y=(0,0)
	add(b139(20), b139(20), 21)                             // rmoveto for a real contour
	add(b139(50), 6)                                        // hlineto
	add(9, 14)                                              // closepath, endchar

	contours := interpretType1Charstring(p)
	if len(contours) == 0 {
		t.Fatal("interpretType1Charstring produced no contours")
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

func TestAtoiSafe(t *testing.T) {
	if atoiSafe("123") != 123 {
		t.Error("atoiSafe(123)")
	}
	if atoiSafe("") != 0 {
		t.Error("atoiSafe(empty)")
	}
	if atoiSafe("12x") != -1 {
		t.Error("atoiSafe(non-digit) should be -1")
	}
}
