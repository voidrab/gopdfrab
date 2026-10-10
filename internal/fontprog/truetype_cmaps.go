package fontprog

import (
	"encoding/binary"
	"sort"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// SimpleTrueTypeCmaps resolves a simple TrueType font's character codes to
// glyph IDs through the cmap subtables ISO 32000-1 9.6.6.4 consults.
type SimpleTrueTypeCmaps struct {
	CodeToUnicode [256]uint16
	// encoded is true when the font dictionary has /Encoding. Such a font is
	// read as non-symbolic: its codes resolve through glyph names, and a
	// (1,0) cmap is indexed by the name's Mac OS Roman code, not the raw code.
	encoded bool
	win     map[uint16]uint16 // (3,1), by Unicode
	ms      map[uint16]uint16 // (3,0), by code or 0xF000+code
	mac     map[uint16]uint16 // (1,0), by Mac OS Roman code
}

// NewSimpleTrueTypeCmaps reads the cmaps of a simple TrueType font whose
// font dictionary has the given /Encoding entry (nil when absent).
func NewSimpleTrueTypeCmaps(enc pdf.PDFValue, tables map[string][]byte) SimpleTrueTypeCmaps {
	return SimpleTrueTypeCmaps{
		CodeToUnicode: SimpleFontCodeToUnicode(enc),
		encoded:       enc != nil,
		win:           ParseCmapFormat4(TTWindowsBMPCmap(tables)),
		ms:            ParseCmapSubtable(TTCmapSubtable(tables, 3, 0)),
		mac:           ParseCmapSubtable(TTCmapSubtable(tables, 1, 0)),
	}
}

// Windows looks cc up in the (3,1) cmap by its glyph name's Unicode value.
// tried is false when the font has no (3,1) cmap or cc has no Unicode value.
func (r SimpleTrueTypeCmaps) Windows(cc int) (gid int, tried, found bool) {
	u := r.CodeToUnicode[cc]
	if u == 0 || r.win == nil {
		return 0, false, false
	}
	g, ok := r.win[u]
	return int(g), true, ok
}

// Fallback resolves cc once (3,1) has not. An encoded (non-symbolic) font
// has one step left in ISO 32000-1 9.6.6.4: (1,0) by the glyph name's Mac OS
// Roman code. Its raw code is never looked up -- after /Differences it can
// name a different glyph, hiding a missing one -- and neither is (3,0), which
// the procedure consults only for symbolic fonts. An unencoded font resolves
// by RawCode.
func (r SimpleTrueTypeCmaps) Fallback(cc int) (gid int, found bool) {
	if !r.encoded {
		return r.RawCode(cc)
	}
	if u := r.CodeToUnicode[cc]; u != 0 && r.mac != nil {
		if mc, ok := MacOSRomanCode(u); ok {
			if g, ok := r.mac[uint16(mc)]; ok {
				return int(g), true
			}
		}
	}
	return 0, false
}

// RawCode looks cc up in the symbolic cmap -- (3,0), else (1,0) -- by the raw
// code or its 0xF000 PUA alias. It is how a symbolic font resolves, and what
// lenient viewers fall back to for a non-symbolic font the strict lookup
// fails on.
func (r SimpleTrueTypeCmaps) RawCode(cc int) (gid int, found bool) {
	sym := r.ms
	if sym == nil {
		sym = r.mac
	}
	for _, candidate := range [2]uint16{uint16(cc) | 0xF000, uint16(cc)} {
		if g, ok := sym[candidate]; ok {
			return int(g), true
		}
	}
	return 0, false
}

// AddWindowsUnicodeCmap returns the TrueType program font with each Unicode
// -> glyph ID pair in add mapped by its (3,1) cmap subtable, creating that
// subtable if the font has none. Existing (3,1) mappings win, and every other
// subtable is kept byte for byte. changed is false when there was nothing to
// add or font is not a TrueType program with a cmap.
func AddWindowsUnicodeCmap(font []byte, add map[uint16]uint16) (out []byte, changed bool) {
	tables, ok := ParseSfnt(font)
	if !ok || len(tables["cmap"]) < 4 {
		return font, false
	}
	merged := ParseCmapFormat4(TTWindowsBMPCmap(tables))
	if merged == nil {
		merged = map[uint16]uint16{}
	}
	for u, gid := range add {
		if _, exists := merged[u]; !exists {
			merged[u] = gid
			changed = true
		}
	}
	if !changed {
		return font, false
	}

	// Keep the old cmap whole, appended after the new records and subtable,
	// so the other records only need their offsets shifted.
	old := tables["cmap"]
	type record struct {
		platform, encoding uint16
		offset             uint32
	}
	var records []record
	// A record count past the table is capped, and a record pointing outside
	// it dropped, so the rebuilt table holds no offset a reader would reject.
	for i := range min(int(binary.BigEndian.Uint16(old[2:4])), (len(old)-4)/8) {
		rec := old[4+i*8:]
		r := record{binary.BigEndian.Uint16(rec[0:2]), binary.BigEndian.Uint16(rec[2:4]), binary.BigEndian.Uint32(rec[4:8])}
		if (r.platform != 3 || r.encoding != 1) && int64(r.offset)+2 <= int64(len(old)) {
			records = append(records, r)
		}
	}
	sub := buildCmapFormat4Table(3, 1, merged)[12:]
	head := 4 + 8*(len(records)+1)
	for i := range records {
		records[i].offset += uint32(head + len(sub))
	}
	records = append(records, record{3, 1, uint32(head)})
	sort.Slice(records, func(i, j int) bool {
		if records[i].platform != records[j].platform {
			return records[i].platform < records[j].platform
		}
		return records[i].encoding < records[j].encoding
	})

	cmap := make([]byte, head, head+len(sub)+len(old))
	binary.BigEndian.PutUint16(cmap[2:4], uint16(len(records)))
	for i, r := range records {
		binary.BigEndian.PutUint16(cmap[4+i*8:], r.platform)
		binary.BigEndian.PutUint16(cmap[6+i*8:], r.encoding)
		binary.BigEndian.PutUint32(cmap[8+i*8:], r.offset)
	}
	cmap = append(append(cmap, sub...), old...)
	tables["cmap"] = cmap
	return PackSfnt(tables), true
}
