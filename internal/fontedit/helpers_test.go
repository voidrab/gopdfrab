package fontedit

import "encoding/binary"

// hheaWithNumHMetrics builds a minimal 36-byte hhea table with
// numberOfHMetrics (offset 34) set to n; the other fields are unused by
// ttRawHMetric.
func hheaWithNumHMetrics(n int) []byte {
	hhea := make([]byte, 36)
	binary.BigEndian.PutUint16(hhea[34:36], uint16(n))
	return hhea
}

// sub is one cmap subtable with its (platform, encoding) IDs.
type sub struct {
	plat, enc uint16
	data      []byte
}

// format0 builds a format-0 cmap subtable mapping each code to its GID.
func format0(m map[byte]byte) []byte {
	sub := make([]byte, 262)
	binary.BigEndian.PutUint16(sub[2:], 262)
	for code, gid := range m {
		sub[6+int(code)] = gid
	}
	return sub
}

// cmapWith builds a cmap table holding the given subtables, in order.
func cmapWith(subs ...sub) map[string][]byte {
	cmap := make([]byte, 4+8*len(subs))
	binary.BigEndian.PutUint16(cmap[2:], uint16(len(subs)))
	for i, s := range subs {
		rec := cmap[4+8*i:]
		binary.BigEndian.PutUint16(rec[0:], s.plat)
		binary.BigEndian.PutUint16(rec[2:], s.enc)
		binary.BigEndian.PutUint32(rec[4:], uint32(len(cmap)))
		cmap = append(cmap, s.data...)
	}
	return map[string][]byte{"cmap": cmap}
}
