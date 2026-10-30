package raster

import (
	"encoding/binary"
	"strconv"
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

func TestGlyphOutlineFromTrueTypeRejectsBadTables(t *testing.T) {
	good := glyfRecord(1, 0, 0) // one contour, one point: endPts=[0], insLen=0
	good = append(good, 0x31, 0x00)

	shortHead := glyfTables(1000, good)
	shortHead["head"] = shortHead["head"][:19]
	zeroUPM := glyfTables(0, good)
	badGlyph := glyfTables(1000, glyfRecord(1)) // truncated: no endPts

	for name, tc := range map[string]struct {
		tables map[string][]byte
		gid    int
	}{
		"short head":        {shortHead, 0},
		"zero unitsPerEm":   {zeroUPM, 0},
		"undecodable glyph": {badGlyph, 0},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := glyphOutlineFromTrueType(tc.tables, tc.gid); ok {
				t.Error("glyphOutlineFromTrueType accepted bad input")
			}
		})
	}
}

func TestTTGlyfContoursCompositeNestingLimit(t *testing.T) {
	// chain(n) is n composites, each wrapping the next, around one point glyph.
	chain := func(n int) map[string][]byte {
		var recs [][]byte
		for i := 1; i <= n; i++ {
			recs = append(recs, append(glyfRecord(-1, 0x0002, uint16(i)), 0, 0))
		}
		return glyfTables(1000, append(recs, append(glyfRecord(1, 0, 0), 0x31, 0x00))...)
	}
	if c, ok := ttGlyfContours(chain(8), 0, 0); !ok || len(c) != 1 {
		t.Errorf("8 levels of nesting: ok=%v contours=%d, want ok with 1", ok, len(c))
	}
	if _, ok := ttGlyfContours(chain(9), 0, 0); ok {
		t.Error("9 levels of nesting were accepted")
	}
	// A glyph that includes itself never terminates on its own.
	self := glyfTables(1000, append(glyfRecord(-1, 0x0002, 0), 0, 0))
	if _, ok := ttGlyfContours(self, 0, 0); ok {
		t.Error("a self-referencing composite was accepted")
	}
}

func TestTTSimpleGlyphContoursTruncation(t *testing.T) {
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
			if _, ok := ttSimpleGlyphContours(tc.rec); ok != tc.ok {
				t.Errorf("ok = %v, want %v", ok, tc.ok)
			}
		})
	}
}

func TestQuadContourToLines(t *testing.T) {
	if got := quadContourToLines(nil, nil); got != nil {
		t.Errorf("empty contour = %v, want nil", got)
	}
	// Off-curve only: the start is synthesized from the first/last midpoint
	// and the contour closes back to it.
	pts := []Point{{0, 0}, {100, 0}, {100, 100}, {0, 100}}
	got := quadContourToLines(pts, []bool{false, false, false, false})
	if len(got) < 4 {
		t.Fatalf("all-off-curve contour flattened to %d points", len(got))
	}
	if last := got[len(got)-1]; last != (Point{0, 50}) {
		t.Errorf("contour ends at %v, want the synthesized start {0 50}", last)
	}
	// On-curve start followed by a trailing off-curve control: the final
	// quadratic closes back to the start point.
	got = quadContourToLines([]Point{{0, 0}, {50, 100}}, []bool{true, false})
	if last := got[len(got)-1]; last != (Point{0, 0}) {
		t.Errorf("contour ends at %v, want the start {0 0}", last)
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

func TestGlyphOutlineFromCFFRejectsBadInput(t *testing.T) {
	cff := buildMinimalCIDCFF()
	if _, ok := glyphOutlineFromCFF(cff, 1); !ok {
		t.Fatal("valid CFF glyph rejected")
	}
	for name, tc := range map[string]struct {
		cff []byte
		gid int
	}{
		"garbage":             {[]byte{9, 9, 9}, 0},
		"negative glyph":      {cff, -1},
		"glyph past CharStrs": {cff, 99},
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := glyphOutlineFromCFF(tc.cff, tc.gid); ok {
				t.Error("glyphOutlineFromCFF accepted bad input")
			}
		})
	}
}

// type1Encrypt is the Type 1 cipher (eexec key 55665, charstring key 4330)
// with lead filler bytes prepended before encryption.
func type1Encrypt(plain []byte, key uint16, lead int) []byte {
	r := key
	out := make([]byte, 0, len(plain)+lead)
	for _, p := range append(make([]byte, lead), plain...) {
		c := p ^ byte(r>>8)
		r = (uint16(c)+r)*52845 + 22719
		out = append(out, c)
	}
	return out
}

// type1Font builds a minimal Type 1 program whose eexec section holds the
// given plaintext Private dict text.
func type1Font(private []byte) []byte {
	return append([]byte("%!PS-AdobeFont-1.0: Test\ncurrentfile eexec\n"), type1Encrypt(private, 55665, 4)...)
}

// type1SquareCharstring is the encrypted charstring of a 100-unit square whose
// lower-left corner is at (10, 10), in a font with lenIV 4.
func type1SquareCharstring() []byte {
	n := b139
	return type1Encrypt([]byte{n(0), n(500), 13, n(10), n(10), 21, n(100), 6, n(100), 7, n(-100), 6, 14}, 4330, 4)
}

func TestGlyphOutlineFromType1(t *testing.T) {
	square := type1SquareCharstring()
	priv := []byte("dup /Private 4 dict dup begin\n/lenIV 4 def\n2 index /CharStrings 4 dict dup begin\n")
	priv = append(priv, []byte("/other 1 RD x ND\n")...)
	priv = append(priv, []byte("/sq "+strconv.Itoa(len(square))+" RD ")...)
	priv = append(priv, square...)
	priv = append(priv, []byte(" ND\n/empty 0 RD  ND\n/huge 99999 RD  ND\nend\n")...)
	font := type1Font(priv)

	path, ok := glyphOutlineFromType1(font, "sq")
	if !ok || len(path.Contours) != 1 || len(path.Contours[0]) != 4 {
		t.Fatalf("sq = %v ok=%v, want one 4-point contour", path.Contours, ok)
	}
	if path.Contours[0][0] != (Point{10, 10}) {
		t.Errorf("sq starts at %v, want {10 10}", path.Contours[0][0])
	}

	for _, name := range []string{"absent", "empty", "huge"} {
		if _, ok := glyphOutlineFromType1(font, name); ok {
			t.Errorf("glyph %q was accepted", name)
		}
	}
	if _, ok := glyphOutlineFromType1([]byte("no eexec section here"), "sq"); ok {
		t.Error("a program without an eexec section yielded a glyph")
	}
}
