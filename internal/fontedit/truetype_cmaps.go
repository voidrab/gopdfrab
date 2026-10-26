package fontedit

import (
	"encoding/binary"
	"sort"

	"github.com/voidrab/gopdfrab/internal/fontprog"
)

// AddWindowsUnicodeCmap returns the TrueType program font with each Unicode
// -> glyph ID pair in add mapped by its (3,1) cmap subtable, creating that
// subtable if the font has none. Existing (3,1) mappings win, and every other
// subtable is kept byte for byte. changed is false when there was nothing to
// add or font is not a TrueType program with a cmap.
func AddWindowsUnicodeCmap(font []byte, add map[uint16]uint16) (out []byte, changed bool) {
	tables, ok := fontprog.ParseSfnt(font)
	if !ok || len(tables["cmap"]) < 4 {
		return font, false
	}
	merged := fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(tables))
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
	sub := BuildCmapFormat4Table(3, 1, merged)[12:]
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
