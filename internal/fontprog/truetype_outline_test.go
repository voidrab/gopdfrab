package fontprog

import (
	"encoding/binary"
	"testing"
)

// glyfTables wraps raw glyph records in a minimal short-loca sfnt table set
// (unitsPerEm 1000) so glyph ids index the records in order.
func glyfTables(upm uint16, recs ...[]byte) map[string][]byte {
	var glyf, loca []byte
	loca = binary.BigEndian.AppendUint16(loca, 0)
	for _, r := range recs {
		glyf = append(glyf, r...)
		if len(glyf)%2 != 0 { // short-format loca offsets are in 2-byte units
			glyf = append(glyf, 0)
		}
		loca = binary.BigEndian.AppendUint16(loca, uint16(len(glyf)/2))
	}
	head := make([]byte, 52)
	binary.BigEndian.PutUint16(head[18:20], upm)
	return map[string][]byte{"head": head, "loca": loca, "glyf": glyf}
}

// glyfRecord is a glyph record: the contour count, a zero bounding box, then
// whatever 16-bit words follow.
func glyfRecord(numContours int16, rest ...uint16) []byte {
	rec := binary.BigEndian.AppendUint16(nil, uint16(numContours))
	rec = append(rec, 0, 0, 0, 0, 0, 0, 0, 0) // bbox
	for _, v := range rest {
		rec = binary.BigEndian.AppendUint16(rec, v)
	}
	return rec
}

func TestTTGlyphContoursCompositeNestingLimit(t *testing.T) {
	// chain(n) is n composites, each wrapping the next, around one point glyph.
	chain := func(n int) map[string][]byte {
		var recs [][]byte
		for i := 1; i <= n; i++ {
			recs = append(recs, append(glyfRecord(-1, 0x0002, uint16(i)), 0, 0))
		}
		return glyfTables(1000, append(recs, append(glyfRecord(1, 0, 0), 0x31, 0x00))...)
	}
	if c, ok := ttGlyphContours(chain(8), 0, 0); !ok || len(c) != 1 {
		t.Errorf("8 levels of nesting: ok=%v contours=%d, want ok with 1", ok, len(c))
	}
	if _, ok := ttGlyphContours(chain(9), 0, 0); ok {
		t.Error("9 levels of nesting were accepted")
	}
	// A glyph that includes itself never terminates on its own.
	self := glyfTables(1000, append(glyfRecord(-1, 0x0002, 0), 0, 0))
	if _, ok := ttGlyphContours(self, 0, 0); ok {
		t.Error("a self-referencing composite was accepted")
	}
}

func TestTTSimpleContoursTruncation(t *testing.T) {
	// flagsOnly: one contour, endPts=[0], insLen=0, then body.
	rec := func(body ...byte) []byte { return append(glyfRecord(1, 0, 0), body...) }

	for _, tc := range []struct {
		name string
		rec  []byte
		ok   bool
	}{
		{"shorter than header", make([]byte, 5), false},
		{"no contours", glyfRecord(0), true},
		{"endpoints cut off", glyfRecord(2, 0), false},
		{"instruction length missing", glyfRecord(1, 0), false},
		{"flags missing", rec(), false},
		{"repeat count missing", rec(0x08), false},
		{"x short vector missing", rec(0x02), false},
		{"x word delta cut off", rec(0x00, 0x01), false},
		{"y short vector missing", rec(0x10 | 0x04), false},
		{"y word delta cut off", rec(0x10, 0x01), false},
		{"complete", rec(0x31), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := ttSimpleContours(tc.rec); ok != tc.ok {
				t.Errorf("ok = %v, want %v", ok, tc.ok)
			}
		})
	}
}

func TestTTCompositeContoursTruncation(t *testing.T) {
	point := append(glyfRecord(1, 0, 0), 0x31, 0x00) // gid 0: one on-curve point
	broken := glyfRecord(1)                          // gid 1: truncated simple glyph
	tables := glyfTables(1000, point, broken)
	comp := func(gid uint16, flags uint16, tail ...byte) []byte {
		return append(glyfRecord(-1, flags, gid), tail...)
	}
	const (
		words = 0x0001
		xy    = 0x0002
	)
	for _, tc := range []struct {
		name string
		rec  []byte
	}{
		{"word args cut off", comp(0, words|xy, 0, 1)},
		{"byte args cut off", comp(0, xy, 1)},
		{"scale cut off", comp(0, xy|0x0008, 0, 0, 0x40)},
		{"x/y scale cut off", comp(0, xy|0x0040, 0, 0, 0x40, 0, 0x20)},
		{"2x2 cut off", comp(0, xy|0x0080, 0, 0, 0x40, 0, 0, 0)},
		{"component undecodable", comp(1, xy, 0, 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := ttCompositeContours(tables, tc.rec, 0); ok {
				t.Error("malformed composite was accepted")
			}
		})
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

// TestTTSimpleContoursDescendingEndPoints covers a glyph whose contour
// endpoints do not climb. The point arrays are sized from the last endpoint
// but indexed by every one of them, so a font claiming a huge early contour
// and a tiny last one would read past the end of them. Such a read panics in a
// rasterizer worker goroutine, which nothing recovers, so the glyph is rejected
// instead.
func TestTTSimpleContoursDescendingEndPoints(t *testing.T) {
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
			if _, ok := ttSimpleContours(build(tc.endPts...)); ok {
				t.Error("a glyph with non-climbing contour endpoints was accepted")
			}
		})
	}

	// Equal consecutive endpoints are legal (an empty contour) and must still
	// be accepted, so the guard cannot just reject any non-increase.
	if _, ok := ttSimpleContours(build(0, 1)); !ok {
		t.Error("a glyph with climbing endpoints was rejected")
	}
}

// curveSink records the control points of every curve drawn.
type curveSink struct {
	recSink
	curves [][6]float64
}

func (s *curveSink) CurveTo(x1, y1, x2, y2, x3, y3 float64) {
	s.curves = append(s.curves, [6]float64{x1, y1, x2, y2, x3, y3})
	s.recSink.CurveTo(x1, y1, x2, y2, x3, y3)
}

// drawn draws one contour of points, on-curve where on says so.
func drawn(pts [][2]float64, on ...bool) *curveSink {
	c := make([]ttPoint, len(pts))
	for i, p := range pts {
		c[i] = ttPoint{p[0], p[1], on[i]}
	}
	var s curveSink
	drawTTContour(c, &s)
	return &s
}

func TestDrawTTContour(t *testing.T) {
	if got := drawn(nil).result(); got != nil {
		t.Errorf("empty contour drew %v", got)
	}

	// A quadratic is drawn as the cubic equal to it: control points two
	// thirds of the way from each end to the quadratic's control point.
	s := drawn([][2]float64{{0, 0}, {30, 60}, {90, 0}}, true, false, true)
	if len(s.curves) != 1 || s.curves[0] != [6]float64{20, 40, 50, 40, 90, 0} {
		t.Errorf("curves = %v, want the elevated quadratic {20 40 50 40 90 0}", s.curves)
	}
	if got := s.result(); len(got) != 1 || got[0][0] != (Point{0, 0}) || got[0][2] != (Point{0, 0}) {
		t.Errorf("contour = %v, want it to start at {0 0} and close back with a line", got)
	}

	// Off-curve only: the start is the midpoint of the last and first
	// points, each pair of off-curve points implies an on-curve one, and the
	// contour closes back to the start.
	got := drawn([][2]float64{{0, 0}, {100, 0}, {100, 100}, {0, 100}}, false, false, false, false).result()
	want := []Point{{0, 50}, {50, 0}, {100, 50}, {50, 100}, {0, 50}}
	if len(got) != 1 || len(got[0]) != len(want) {
		t.Fatalf("contour = %v, want %v", got, want)
	}
	for i, p := range want {
		if got[0][i] != p {
			t.Errorf("point %d = %v, want %v", i, got[0][i], p)
		}
	}

	// A trailing off-curve point closes the contour with a quadratic.
	s = drawn([][2]float64{{0, 0}, {50, 100}}, true, false)
	if len(s.curves) != 1 || lastPoint(t, s.result()) != (Point{0, 0}) {
		t.Errorf("curves = %v, want one closing back at {0 0}", s.curves)
	}
}

// TestTrueTypeOutline pins that a glyph is drawn whole or not at all.
func TestTrueTypeOutline(t *testing.T) {
	tables := buildTriangleGlyfTables()
	var s recSink
	if !TrueTypeOutline(tables, 0, &s) {
		t.Fatal("triangle rejected")
	}
	want := []Point{{0, 0}, {100, 100}, {0, 0}, {0, 0}}
	if got := s.result(); len(got) != 1 || len(got[0]) != len(want) || got[0][1] != want[1] {
		t.Errorf("contours = %v, want %v", got, want)
	}
	// A glyph without a record draws nothing and is not an error.
	s = recSink{}
	if !TrueTypeOutline(tables, 5, &s) || s.result() != nil {
		t.Errorf("a glyph without a record drew %v", s.contours)
	}
	// An undecodable glyph draws nothing.
	bad := glyfTables(1000, append(glyfRecord(-1, 0x0002, 1), 0, 0), glyfRecord(1))
	s = recSink{}
	if TrueTypeOutline(bad, 0, &s) || s.result() != nil {
		t.Errorf("an undecodable glyph was accepted or drew %v", s.contours)
	}
}

func TestUnitsPerEm(t *testing.T) {
	if upm, ok := UnitsPerEm(glyfTables(2048)); !ok || upm != 2048 {
		t.Errorf("UnitsPerEm = %d, %v, want 2048, true", upm, ok)
	}
	short := glyfTables(1000)
	short["head"] = short["head"][:19]
	for name, tables := range map[string]map[string][]byte{
		"no head":    {},
		"short head": short,
		"zero":       glyfTables(0),
	} {
		if _, ok := UnitsPerEm(tables); ok {
			t.Errorf("%s: UnitsPerEm accepted it", name)
		}
	}
}
