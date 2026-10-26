package fontprog

import (
	"encoding/binary"
	"sort"
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
// space; the glyph is defined, it just draws nothing. This used to additionally
// require a zero advance width before accepting an empty outline, which is not
// a rule TrueType has: a space in a monospaced subset is empty *and* 600/1000
// wide, so every such font was reported as missing a glyph it plainly defines.
// A genuinely absent glyph is out of range instead, which is still caught.
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
	head := tables["head"]
	if len(hmtx) == 0 || len(hhea) < 36 || len(head) < 20 {
		return -1
	}
	upm := int(binary.BigEndian.Uint16(head[18:20]))
	if upm == 0 {
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

// maxPow2LE returns the largest power of two <= n, and its base-2 exponent,
// the "searchRange/entrySelector" pair every sfnt binary-search table
// (the table directory, and a format-4 cmap subtable) declares.
func maxPow2LE(n int) (pow, exp int) {
	pow, exp = 1, 0
	for pow*2 <= n {
		pow *= 2
		exp++
	}
	return
}

// BuildCmapFormat4Table builds a complete 'cmap' table with a single (platform,
// encoding) format-4 subtable, mapping each unicode in uniToGID to its glyph
// ID via one segment per code point -- simpler and more robust than merging
// contiguous runs, at the cost of a slightly larger table.
func BuildCmapFormat4Table(platform, encoding uint16, uniToGID map[uint16]uint16) []byte {
	unicodes := make([]uint16, 0, len(uniToGID))
	for u := range uniToGID {
		unicodes = append(unicodes, u)
	}
	sort.Slice(unicodes, func(i, j int) bool { return unicodes[i] < unicodes[j] })

	segCount := len(unicodes) + 1 // +1 terminator segment
	segCountX2 := segCount * 2
	pow, exp := maxPow2LE(segCount)
	searchRange := pow * 2

	endOff := 14
	startOff := endOff + segCountX2 + 2 // +2 reservedPad
	deltaOff := startOff + segCountX2
	rangeOff := deltaOff + segCountX2
	glyphArrayOff := rangeOff + segCountX2
	subLen := glyphArrayOff + len(unicodes)*2

	sub := make([]byte, subLen)
	binary.BigEndian.PutUint16(sub[0:], 4)
	binary.BigEndian.PutUint16(sub[2:], uint16(subLen))
	binary.BigEndian.PutUint16(sub[6:], uint16(segCountX2))
	binary.BigEndian.PutUint16(sub[8:], uint16(searchRange))
	binary.BigEndian.PutUint16(sub[10:], uint16(exp))
	binary.BigEndian.PutUint16(sub[12:], uint16(segCountX2-searchRange))

	for i, u := range unicodes {
		binary.BigEndian.PutUint16(sub[endOff+i*2:], u)
		binary.BigEndian.PutUint16(sub[startOff+i*2:], u)
		glyphSlot, rangeSlot := glyphArrayOff+i*2, rangeOff+i*2
		binary.BigEndian.PutUint16(sub[rangeSlot:], uint16(glyphSlot-rangeSlot))
		binary.BigEndian.PutUint16(sub[glyphSlot:], uniToGID[u])
	}
	term := len(unicodes)
	binary.BigEndian.PutUint16(sub[endOff+term*2:], 0xFFFF)
	binary.BigEndian.PutUint16(sub[startOff+term*2:], 0xFFFF)
	binary.BigEndian.PutUint16(sub[deltaOff+term*2:], 1)

	cmap := make([]byte, 4+8+len(sub))
	binary.BigEndian.PutUint16(cmap[2:], 1) // numTables
	binary.BigEndian.PutUint16(cmap[4:], platform)
	binary.BigEndian.PutUint16(cmap[6:], encoding)
	binary.BigEndian.PutUint32(cmap[8:], 12) // subtable offset
	copy(cmap[12:], sub)
	return cmap
}

// tableChecksum sums b (which must be a multiple of 4 bytes) as a sequence of
// big-endian uint32 words, the checksum algorithm every sfnt table directory
// entry -- and the whole-file checkSumAdjustment -- uses.
func tableChecksum(b []byte) uint32 {
	var sum uint32
	for i := 0; i+4 <= len(b); i += 4 {
		sum += binary.BigEndian.Uint32(b[i:])
	}
	return sum
}

// PackSfnt assembles tables into a complete sfnt binary: a sorted table
// directory with correct offsets/lengths/per-table checksums, 4-byte-aligned
// table data, and (if a 'head' table is present) a correctly patched
// checkSumAdjustment.
func PackSfnt(tables map[string][]byte) []byte {
	tags := make([]string, 0, len(tables))
	for t := range tables {
		tags = append(tags, t)
	}
	sort.Strings(tags)

	numTables := len(tags)
	type packedEntry struct {
		tag            string
		padded         []byte
		rawLen, offset int
	}
	entries := make([]packedEntry, 0, numTables)
	offset := 12 + 16*numTables
	for _, tag := range tags {
		raw := tables[tag]
		padded := raw
		if r := len(raw) % 4; r != 0 {
			padded = append(append([]byte(nil), raw...), make([]byte, 4-r)...)
		}
		entries = append(entries, packedEntry{tag, padded, len(raw), offset})
		offset += len(padded)
	}

	buf := make([]byte, offset)
	binary.BigEndian.PutUint32(buf[0:4], 0x00010000)
	binary.BigEndian.PutUint16(buf[4:6], uint16(numTables))
	pow, exp := maxPow2LE(numTables)
	searchRange := pow * 16
	binary.BigEndian.PutUint16(buf[6:8], uint16(searchRange))
	binary.BigEndian.PutUint16(buf[8:10], uint16(exp))
	binary.BigEndian.PutUint16(buf[10:12], uint16(numTables*16-searchRange))

	headOffset := -1
	for i, e := range entries {
		rec := buf[12+i*16 : 12+i*16+16]
		copy(rec[0:4], e.tag)
		binary.BigEndian.PutUint32(rec[8:12], uint32(e.offset))
		binary.BigEndian.PutUint32(rec[12:16], uint32(e.rawLen))
		copy(buf[e.offset:e.offset+len(e.padded)], e.padded)
		if e.tag == "head" {
			headOffset = e.offset
		}
	}
	// Every checksum, head's included, is taken with checkSumAdjustment
	// zeroed; the adjustment is then patched in last so the whole file sums
	// to the magic. Re-checksumming head afterwards would break both.
	hasHead := headOffset >= 0 && headOffset+12 <= len(buf)
	if hasHead {
		binary.BigEndian.PutUint32(buf[headOffset+8:headOffset+12], 0)
	}
	for i, e := range entries {
		cs := tableChecksum(buf[e.offset : e.offset+len(e.padded)])
		binary.BigEndian.PutUint32(buf[12+i*16+4:12+i*16+8], cs)
	}
	if hasHead {
		adjustment := SfntChecksumAdjustmentMagic - tableChecksum(buf)
		binary.BigEndian.PutUint32(buf[headOffset+8:headOffset+12], adjustment)
	}
	return buf
}
