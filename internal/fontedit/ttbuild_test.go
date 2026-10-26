package fontedit

import (
	"encoding/binary"
	"testing"

	"github.com/voidrab/gopdfrab/internal/fontprog"
)

// This file builds small synthetic TrueType programs for the sfnt and subset
// tests. Glyph outlines are opaque filler: the code under test copies glyph
// records and rewrites composite component references, it never rasterizes.

// ttGlyph is one glyph of a synthetic font.
type ttGlyph struct {
	rec []byte // glyf record; empty is a blank glyph such as a space
	aw  uint16 // advance width
	lsb int16  // left side bearing
}

// simpleGlyph returns a simple-glyph record (numberOfContours = 1) of n bytes
// whose body is filled with fill, so subsets can be told apart.
func simpleGlyph(n int, fill byte) []byte {
	rec := make([]byte, n)
	binary.BigEndian.PutUint16(rec, 1)
	for i := 2; i < n; i++ {
		rec[i] = fill
	}
	return rec
}

// compositeGlyph returns a composite record referencing comps in order, each
// with byte-sized arguments and no transform; all but the last carry
// MORE_COMPONENTS.
func compositeGlyph(comps ...int) []byte {
	rec := make([]byte, 10) // header: numberOfContours = -1, zero bbox
	binary.BigEndian.PutUint16(rec, 0xFFFF)
	for i, c := range comps {
		flags := uint16(0)
		if i < len(comps)-1 {
			flags = 0x0020
		}
		rec = binary.BigEndian.AppendUint16(rec, flags)
		rec = binary.BigEndian.AppendUint16(rec, uint16(c))
		rec = append(rec, 0, 0)
	}
	return rec
}

// ttFont describes a synthetic TrueType program.
type ttFont struct {
	glyphs    []ttGlyph
	cmap      map[uint16]uint16 // (3,1) format-4 cmap; nil omits the table
	shortLoca bool              // 'head' declares the short (2-byte) loca format
	extra     map[string][]byte // further tables, e.g. "OS/2"
}

// build returns the packed sfnt.
func (f ttFont) build() []byte {
	n := len(f.glyphs)
	var glyf []byte
	loca := make([]byte, 0, 4*(n+1))
	putLoca := func() {
		if f.shortLoca {
			loca = binary.BigEndian.AppendUint16(loca, uint16(len(glyf)/2))
		} else {
			loca = binary.BigEndian.AppendUint32(loca, uint32(len(glyf)))
		}
	}
	hmtx := make([]byte, 0, 4*n)
	for _, g := range f.glyphs {
		putLoca()
		glyf = append(glyf, g.rec...)
		if len(glyf)%2 != 0 {
			glyf = append(glyf, 0)
		}
		hmtx = binary.BigEndian.AppendUint16(hmtx, g.aw)
		hmtx = binary.BigEndian.AppendUint16(hmtx, uint16(g.lsb))
	}
	putLoca()

	head := make([]byte, 54)
	binary.BigEndian.PutUint16(head[18:], 1000) // unitsPerEm
	if !f.shortLoca {
		binary.BigEndian.PutUint16(head[50:], 1)
	}
	hhea := make([]byte, 36)
	binary.BigEndian.PutUint16(hhea[34:], uint16(n))
	maxp := make([]byte, 32)
	binary.BigEndian.PutUint32(maxp, 0x00010000)
	binary.BigEndian.PutUint16(maxp[4:], uint16(n))

	tables := map[string][]byte{
		"head": head, "hhea": hhea, "maxp": maxp, "hmtx": hmtx, "loca": loca, "glyf": glyf,
	}
	if f.cmap != nil {
		tables["cmap"] = fontprog.BuildCmapFormat4Table(3, 1, f.cmap)
	}
	for tag, data := range f.extra {
		tables[tag] = data
	}
	return fontprog.PackSfnt(tables)
}

// cmapRecord is one encoding record of a hand-built cmap table.
type cmapRecord struct {
	platform, encoding uint16
	sub                []byte
}

// cmapTable assembles a cmap table out of raw subtables.
func cmapTable(recs ...cmapRecord) []byte {
	out := []byte{0, 0, 0, byte(len(recs))}
	off := 4 + 8*len(recs)
	var body []byte
	for _, r := range recs {
		out = binary.BigEndian.AppendUint16(out, r.platform)
		out = binary.BigEndian.AppendUint16(out, r.encoding)
		out = binary.BigEndian.AppendUint32(out, uint32(off+len(body)))
		body = append(body, r.sub...)
	}
	return append(out, body...)
}

// subsetSource is a seven-glyph font: 0 .notdef, 1 'A', 2 'B' (a composite of
// glyphs 4 and 1), 3 unused, 4 a component-only glyph, 5 a blank space with an
// advance, 6 'C' with an odd-length record.
func subsetSource() ttFont {
	return ttFont{
		glyphs: []ttGlyph{
			{rec: simpleGlyph(12, 0xA0), aw: 500, lsb: 1},
			{rec: simpleGlyph(14, 0xA1), aw: 600, lsb: 2},
			{rec: compositeGlyph(4, 1), aw: 700, lsb: 3},
			{rec: simpleGlyph(12, 0xA3), aw: 800, lsb: 4},
			{rec: simpleGlyph(16, 0xA4), aw: 900, lsb: 5},
			{rec: nil, aw: 250, lsb: 0},
			{rec: simpleGlyph(13, 0xA6), aw: 650, lsb: 6},
		},
		cmap:  map[uint16]uint16{'A': 1, 'B': 2, ' ': 5, 'C': 6, 'Z': 3},
		extra: map[string][]byte{"OS/2": {1, 2, 3, 4}, "name": {5, 6, 7, 8}, "cvt ": {9, 9, 9, 9}},
	}
}

// tt parses a built font and fails the test if it is not a valid sfnt.
func tt(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	tables, ok := fontprog.ParseSfnt(data)
	if !ok {
		t.Fatal("output is not a valid sfnt")
	}
	return tables
}
