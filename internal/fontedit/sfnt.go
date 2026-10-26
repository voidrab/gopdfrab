package fontedit

import (
	"encoding/binary"
	"sort"
)

// This file writes the sfnt container: the table directory with its
// checksums, and format-4 cmap tables.

// SfntChecksumAdjustmentMagic is the constant every TrueType font's
// head.checkSumAdjustment is defined relative to: adjustment = magic -
// (checksum of the whole file with checkSumAdjustment itself zeroed).
const SfntChecksumAdjustmentMagic = 0xB1B0AFBA

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
