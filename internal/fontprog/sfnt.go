package fontprog

import (
	"encoding/binary"
)

// This file reads TrueType (sfnt) tables for the 6.3.5 subset-completeness
// and 6.3.6 width-consistency checks and for the converter's font repairs.

// TTLocaHasGlyph returns a function that reports whether a glyph ID has
// non-empty glyph data in the loca/glyf tables.
func TTLocaHasGlyph(tables map[string][]byte) func(gid int) bool {
	loca := tables["loca"]
	head := tables["head"]
	if len(loca) < 4 || len(head) < 52 {
		return func(int) bool { return false }
	}
	locFmt := int(binary.BigEndian.Uint16(head[50:52]))
	return func(gid int) bool {
		if locFmt == 0 {
			if (gid+1)*2+2 > len(loca) {
				return false
			}
			return binary.BigEndian.Uint16(loca[gid*2:]) != binary.BigEndian.Uint16(loca[(gid+1)*2:])
		}
		if (gid+1)*4+4 > len(loca) {
			return false
		}
		return binary.BigEndian.Uint32(loca[gid*4:]) != binary.BigEndian.Uint32(loca[(gid+1)*4:])
	}
}

// TTNumGlyphs returns the number of glyphs in the font from the maxp table,
// or 0 if unavailable. A glyph ID is valid (present in the font) when it is
// in [0, numGlyphs-1]; empty outlines (space/whitespace) are still valid.
func TTNumGlyphs(tables map[string][]byte) int {
	maxp := tables["maxp"]
	if len(maxp) < 6 {
		return 0
	}
	return int(binary.BigEndian.Uint16(maxp[4:6]))
}

// TTGlyphInRange returns a function that reports whether a glyph ID is within
// the font's valid glyph range [0, numGlyphs-1]. Unlike TTLocaHasGlyph, this
// accepts glyphs with empty outlines (e.g. the space character).
func TTGlyphInRange(tables map[string][]byte) func(gid int) bool {
	n := TTNumGlyphs(tables)
	if n == 0 {
		return func(int) bool { return false }
	}
	return func(gid int) bool {
		return gid >= 0 && gid < n
	}
}

// TTGlyphPresent returns a function reporting whether a glyph ID counts as
// present for coverage purposes (6.3.5): a non-empty outline, or any glyph the
// font defines at all -- an in-range glyph ID with a loca entry.
//
// An empty loca entry means "no contours", which is how TrueType encodes a
// space; the glyph is defined, it just draws nothing. The advance width is not
// consulted: an empty outline is present whatever its width, since a space in a
// monospaced subset is empty and has a nonzero advance. A glyph absent from the
// font is one outside the glyph range.
func TTGlyphPresent(tables map[string][]byte) func(gid int) bool {
	hasData := TTLocaHasGlyph(tables)
	inRange := TTGlyphInRange(tables)
	return func(gid int) bool {
		// hasData covers the case of a readable loca with no maxp to bound it.
		return hasData(gid) || inRange(gid)
	}
}

// TTAdvanceWidth returns the advance width for glyph gid from the hmtx table,
// scaled to PDF units (1/1000 of em). Returns -1 if unavailable. The result
// stays a real number: at a unitsPerEm that is not 1000 the scaled width is
// rarely a whole one, and rounding it here would widen the 6.3.6 comparison's
// tolerance by half a unit -- enough to miss a mismatch a stricter reader
// catches.
func TTAdvanceWidth(tables map[string][]byte, gid int) float64 {
	hmtx := tables["hmtx"]
	hhea := tables["hhea"]
	upm, ok := UnitsPerEm(tables)
	if len(hmtx) == 0 || len(hhea) < 36 || !ok {
		return -1
	}
	nHM := int(binary.BigEndian.Uint16(hhea[34:36]))
	var aw int
	if gid < nHM {
		if gid*4+2 > len(hmtx) {
			return -1
		}
		aw = int(binary.BigEndian.Uint16(hmtx[gid*4:]))
	} else if nHM > 0 {
		if (nHM-1)*4+2 > len(hmtx) {
			return -1
		}
		aw = int(binary.BigEndian.Uint16(hmtx[(nHM-1)*4:]))
	}
	return float64(aw) * 1000 / float64(upm)
}

// TTWindowsBMPCmap finds the platform 3 encoding 1 cmap subtable, or nil.
func TTWindowsBMPCmap(tables map[string][]byte) []byte {
	return TTCmapSubtable(tables, 3, 1)
}

// TTCmapSubtable returns the first cmap subtable for the given platform and
// encoding IDs, sliced from its offset to the end of the cmap table, or nil.
func TTCmapSubtable(tables map[string][]byte, platform, encoding uint16) []byte {
	cmap := tables["cmap"]
	if len(cmap) < 4 {
		return nil
	}
	n := int(binary.BigEndian.Uint16(cmap[2:4]))
	for i := range n {
		rec := cmap[4+i*8:]
		if len(rec) < 8 {
			break
		}
		offset := int(binary.BigEndian.Uint32(rec[4:8]))
		if binary.BigEndian.Uint16(rec[0:2]) == platform && binary.BigEndian.Uint16(rec[2:4]) == encoding && offset+2 <= len(cmap) {
			return cmap[offset:]
		}
	}
	return nil
}

// ParseCmapFormat4 decodes a format-4 cmap subtable, returning Unicode→GID.
func ParseCmapFormat4(sub []byte) map[uint16]uint16 {
	if len(sub) < 14 || binary.BigEndian.Uint16(sub[0:2]) != 4 {
		return nil
	}
	segCountX2 := int(binary.BigEndian.Uint16(sub[6:8]))
	segCount := segCountX2 / 2
	endOff := 14
	startOff := endOff + segCountX2 + 2 // +2 for reservedPad
	deltaOff := startOff + segCountX2
	rangeOff := deltaOff + segCountX2

	if rangeOff+segCountX2 > len(sub) {
		return nil
	}

	result := map[uint16]uint16{}
	for i := range segCount {
		endCount := binary.BigEndian.Uint16(sub[endOff+i*2:])
		startCount := binary.BigEndian.Uint16(sub[startOff+i*2:])
		delta := int(int16(binary.BigEndian.Uint16(sub[deltaOff+i*2:])))
		rangeOffset := int(binary.BigEndian.Uint16(sub[rangeOff+i*2:]))
		if startCount == 0xFFFF {
			break
		}
		for cc := int(startCount); cc <= int(endCount); cc++ {
			var gid int
			if rangeOffset == 0 {
				gid = (cc + delta) & 0xFFFF
			} else {
				idx := rangeOff + i*2 + rangeOffset + (cc-int(startCount))*2
				if idx+2 > len(sub) {
					continue
				}
				raw := int(binary.BigEndian.Uint16(sub[idx:]))
				if raw == 0 {
					continue
				}
				gid = (raw + delta) & 0xFFFF
			}
			if gid != 0 {
				result[uint16(cc)] = uint16(gid)
			}
		}
	}
	return result
}

// ParseCmapFormat0 decodes a format-0 (byte array) cmap subtable into a code→GID map.
func ParseCmapFormat0(sub []byte) map[uint16]uint16 {
	if len(sub) < 262 || binary.BigEndian.Uint16(sub[0:2]) != 0 {
		return nil
	}
	result := map[uint16]uint16{}
	for i := 0; i < 256; i++ {
		if gid := uint16(sub[6+i]); gid != 0 {
			result[uint16(i)] = gid
		}
	}
	return result
}

// ParseCmapFormat6 decodes a format-6 (trimmed table) cmap subtable into a code→GID map.
func ParseCmapFormat6(sub []byte) map[uint16]uint16 {
	if len(sub) < 10 || binary.BigEndian.Uint16(sub[0:2]) != 6 {
		return nil
	}
	firstCode := int(binary.BigEndian.Uint16(sub[6:8]))
	count := int(binary.BigEndian.Uint16(sub[8:10]))
	if 10+count*2 > len(sub) {
		return nil
	}
	result := map[uint16]uint16{}
	for i := 0; i < count; i++ {
		if gid := binary.BigEndian.Uint16(sub[10+i*2:]); gid != 0 {
			result[uint16(firstCode+i)] = gid
		}
	}
	return result
}

// ParseCmapSubtable decodes a cmap subtable of format 0, 4, or 6.
func ParseCmapSubtable(sub []byte) map[uint16]uint16 {
	if len(sub) < 2 {
		return nil
	}
	switch binary.BigEndian.Uint16(sub[0:2]) {
	case 0:
		return ParseCmapFormat0(sub)
	case 4:
		return ParseCmapFormat4(sub)
	case 6:
		return ParseCmapFormat6(sub)
	}
	return nil
}

// ParseSfnt parses an sfnt (TrueType/OpenType) table directory, returning each
// table's bytes. The second result is false if the data is not a valid sfnt.
func ParseSfnt(data []byte) (map[string][]byte, bool) {
	if len(data) < 12 {
		return nil, false
	}
	switch binary.BigEndian.Uint32(data[:4]) {
	case 0x00010000, 0x74727565, 0x4F54544F: // 1.0, 'true', 'OTTO'
	default:
		return nil, false
	}
	num := int(binary.BigEndian.Uint16(data[4:6]))
	if num == 0 || 12+num*16 > len(data) {
		return nil, false
	}
	tables := map[string][]byte{}
	for i := range num {
		rec := data[12+i*16:]
		off := binary.BigEndian.Uint32(rec[8:12])
		ln := binary.BigEndian.Uint32(rec[12:16])
		if int(off) > len(data) {
			continue
		}
		end := min(int(off)+int(ln), len(data))
		tables[string(rec[:4])] = data[off:end]
	}
	return tables, true
}

// ScaledAscentDescent reads hhea's ascender/descender, scaled to PDF's
// 1000-unit em for a FontDescriptor's Ascent/Descent.
func ScaledAscentDescent(tables map[string][]byte) (ascent, descent int) {
	hhea := tables["hhea"]
	upm, ok := UnitsPerEm(tables)
	if len(hhea) < 8 || !ok {
		return 0, 0
	}
	a := int(int16(binary.BigEndian.Uint16(hhea[4:6])))
	d := int(int16(binary.BigEndian.Uint16(hhea[6:8])))
	return a * 1000 / upm, d * 1000 / upm
}

// ScaledCapHeight reads OS/2's sCapHeight scaled to PDF's 1000-unit em. It
// returns fallback when the table is too short to hold the field or the units
// per em are unknown.
func ScaledCapHeight(tables map[string][]byte, fallback int) int {
	os2 := tables["OS/2"]
	upm, ok := UnitsPerEm(tables)
	if len(os2) < 90 || !ok {
		return fallback
	}
	return int(int16(binary.BigEndian.Uint16(os2[88:90]))) * 1000 / upm
}

// CmapSubtableCount returns the number of subtables in the cmap table of the
// sfnt font program data, and false when data does not parse or has no
// usable cmap table.
func CmapSubtableCount(data []byte) (int, bool) {
	tables, ok := ParseSfnt(data)
	if !ok {
		return 0, false
	}
	cmap := tables["cmap"]
	if len(cmap) < 4 {
		return 0, false
	}
	return int(binary.BigEndian.Uint16(cmap[2:4])), true
}
