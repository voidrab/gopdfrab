package fontprog

import (
	"encoding/binary"
	"sort"
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
		tables["cmap"] = cmapFormat4Table(3, 1, f.cmap)
	}
	for tag, data := range f.extra {
		tables[tag] = data
	}
	return packSfnt(tables)
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

// format6Sub is a format-6 subtable mapping firstCode.. to gids.
func format6Sub(firstCode uint16, gids ...uint16) []byte {
	sub := []byte{0, 6, 0, 0, 0, 0}
	sub = binary.BigEndian.AppendUint16(sub, firstCode)
	sub = binary.BigEndian.AppendUint16(sub, uint16(len(gids)))
	for _, g := range gids {
		sub = binary.BigEndian.AppendUint16(sub, g)
	}
	return sub
}

// packSfnt assembles tables into an sfnt with a valid table directory. The
// parsers read only tags, offsets and lengths, so checksums and the binary
// search fields are left zero; fontedit.PackSfnt writes the real thing.
func packSfnt(tables map[string][]byte) []byte {
	tags := make([]string, 0, len(tables))
	for tag := range tables {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	out := binary.BigEndian.AppendUint32(nil, 0x00010000)
	out = binary.BigEndian.AppendUint16(out, uint16(len(tags)))
	out = append(out, make([]byte, 6)...)
	off := 12 + 16*len(tags)
	var body []byte
	for _, tag := range tags {
		data := tables[tag]
		out = append(out, tag...)
		out = binary.BigEndian.AppendUint32(out, 0)
		out = binary.BigEndian.AppendUint32(out, uint32(off+len(body)))
		out = binary.BigEndian.AppendUint32(out, uint32(len(data)))
		body = append(body, data...)
		body = append(body, make([]byte, (4-len(data)%4)%4)...)
	}
	return append(out, body...)
}

// cmapFormat4Table is a 'cmap' table with one (platform, encoding) format-4
// subtable at offset 12, one segment per mapped code plus the closing 0xFFFF
// segment. The binary search fields are left zero, as the parser ignores them.
func cmapFormat4Table(platform, encoding uint16, uniToGID map[uint16]uint16) []byte {
	codes := make([]int, 0, len(uniToGID))
	for u := range uniToGID {
		codes = append(codes, int(u))
	}
	sort.Ints(codes)
	segs := len(codes) + 1
	ends := make([]byte, 0, 2*segs)
	starts := make([]byte, 0, 2*segs)
	deltas := make([]byte, 0, 2*segs)
	for _, c := range codes {
		ends = binary.BigEndian.AppendUint16(ends, uint16(c))
		starts = binary.BigEndian.AppendUint16(starts, uint16(c))
		deltas = binary.BigEndian.AppendUint16(deltas, uniToGID[uint16(c)]-uint16(c))
	}
	ends = binary.BigEndian.AppendUint16(ends, 0xFFFF)
	starts = binary.BigEndian.AppendUint16(starts, 0xFFFF)
	deltas = binary.BigEndian.AppendUint16(deltas, 1)

	sub := []byte{0, 4, 0, 0, 0, 0}
	sub = binary.BigEndian.AppendUint16(sub, uint16(2*segs))
	sub = append(sub, make([]byte, 6)...)
	sub = append(sub, ends...)
	sub = append(sub, 0, 0)
	sub = append(sub, starts...)
	sub = append(sub, deltas...)
	sub = append(sub, make([]byte, 2*segs)...)
	binary.BigEndian.PutUint16(sub[2:], uint16(len(sub)))

	out := []byte{0, 0, 0, 1}
	out = binary.BigEndian.AppendUint16(out, platform)
	out = binary.BigEndian.AppendUint16(out, encoding)
	out = binary.BigEndian.AppendUint32(out, 12)
	return append(out, sub...)
}
