package fontprog

import (
	"encoding/binary"
)

// GlyfRecord returns the raw glyf bytes for gid, or nil if gid is out of
// range or has an empty outline (e.g. space).
func GlyfRecord(tables map[string][]byte, gid int) []byte {
	loca := tables["loca"]
	glyf := tables["glyf"]
	head := tables["head"]
	if len(head) < 52 {
		return nil
	}
	locFmt := int(binary.BigEndian.Uint16(head[50:52]))
	var start, end int
	if locFmt == 0 {
		if (gid+1)*2+2 > len(loca) {
			return nil
		}
		start = int(binary.BigEndian.Uint16(loca[gid*2:])) * 2
		end = int(binary.BigEndian.Uint16(loca[(gid+1)*2:])) * 2
	} else {
		if (gid+1)*4+4 > len(loca) {
			return nil
		}
		start = int(binary.BigEndian.Uint32(loca[gid*4:]))
		end = int(binary.BigEndian.Uint32(loca[(gid+1)*4:]))
	}
	if start < 0 || end > len(glyf) || start > end {
		return nil
	}
	return glyf[start:end]
}

// GlyfIsComposite reports whether a glyf record's numberOfContours is
// negative, the TrueType signal for a composite (compound) glyph.
func GlyfIsComposite(rec []byte) bool {
	return len(rec) >= 2 && int16(binary.BigEndian.Uint16(rec)) < 0
}

// ComponentRecordSize returns the byte length of a single composite-glyph
// component record, given its flags word.
func ComponentRecordSize(flags uint16) int {
	size := 4 // flags + glyphIndex
	if flags&0x0001 != 0 {
		size += 4 // ARG_1_AND_2_ARE_WORDS
	} else {
		size += 2
	}
	switch {
	case flags&0x0008 != 0: // WE_HAVE_A_SCALE
		size += 2
	case flags&0x0040 != 0: // WE_HAVE_AN_X_AND_Y_SCALE
		size += 4
	case flags&0x0080 != 0: // WE_HAVE_A_TWO_BY_TWO
		size += 8
	}
	return size
}

// GlyfComponents returns the component glyph IDs referenced by a composite
// glyph record, or nil for a simple glyph (no outline data, or contours >= 0).
func GlyfComponents(rec []byte) []int {
	if !GlyfIsComposite(rec) {
		return nil
	}
	var gids []int
	off := 10
	for off+4 <= len(rec) {
		flags := binary.BigEndian.Uint16(rec[off:])
		gids = append(gids, int(binary.BigEndian.Uint16(rec[off+2:])))
		off += ComponentRecordSize(flags)
		if flags&0x0020 == 0 { // no MORE_COMPONENTS
			break
		}
	}
	return gids
}

// EmptyGlyfRecord is a minimal simple-glyph record: numberOfContours=0, a zero
// bbox, and an explicit instructionLength=0 -- a valid, explicit way to say
// "this glyph has no outline," used in place of a zero-length loca span.
var EmptyGlyfRecord = make([]byte, 12)
