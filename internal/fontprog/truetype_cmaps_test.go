package fontprog

import (
	"encoding/binary"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// format0 builds a format-0 cmap subtable mapping each code to its GID.
func format0(m map[byte]byte) []byte {
	sub := make([]byte, 262)
	binary.BigEndian.PutUint16(sub[2:], 262)
	for code, gid := range m {
		sub[6+int(code)] = gid
	}
	return sub
}

// sub is one cmap subtable with its (platform, encoding) IDs.
type sub struct {
	plat, enc uint16
	data      []byte
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

func TestSimpleTrueTypeCmapsWindows(t *testing.T) {
	win := cmapFormat4Table(3, 1, map[uint16]uint16{'A': 1})
	tables, _ := ParseSfnt(packSfnt(map[string][]byte{"cmap": win}))
	r := NewSimpleTrueTypeCmaps(pdf.PDFName{Value: "WinAnsiEncoding"}, tables)

	if gid, tried, found := r.Windows('A'); gid != 1 || !tried || !found {
		t.Errorf("Windows('A') = %d, %v, %v; want 1, true, true", gid, tried, found)
	}
	if _, tried, found := r.Windows('B'); !tried || found {
		t.Errorf("Windows('B') tried=%v found=%v; want a tried miss", tried, found)
	}
	if _, tried, _ := r.Windows(1); tried { // WinAnsi code 1 has no glyph name
		t.Error("a code with no Unicode value should not be tried")
	}
	if _, tried, _ := NewSimpleTrueTypeCmaps(nil, map[string][]byte{}).Windows('A'); tried {
		t.Error("a font without (3,1) should not be tried")
	}
}

func TestSimpleTrueTypeCmapsFallback(t *testing.T) {
	mac := sub{1, 0, format0(map[byte]byte{0x41: 1, 0x8E: 2})}
	ms := sub{3, 0, format0(map[byte]byte{0x42: 3})}
	winAnsi := pdf.PDFName{Value: "WinAnsiEncoding"}
	renamed := pdf.NewPDFDict()
	renamed.Entries.Set("Differences", pdf.PDFArray{pdf.PDFInteger(0x41), pdf.PDFName{Value: "Lslash"}})

	for _, tc := range []struct {
		name   string
		enc    pdf.PDFValue
		tables map[string][]byte
		cc     int
		gid    int
		found  bool
	}{
		// Encoded: (1,0) by the glyph name's Mac OS Roman code.
		{"encoded eacute via Mac code 0x8E", winAnsi, cmapWith(mac), 0xE9, 2, true},
		{"encoded raw code is not looked up in (1,0)", winAnsi, cmapWith(mac), 0x8E, 0, false},
		{"encoded name without a Mac code", renamed, cmapWith(mac), 0x41, 0, false},
		// 9.6.6.4 gives an encoded (non-symbolic) font no (3,0) step.
		{"encoded does not fall through to (3,0)", winAnsi, cmapWith(mac, ms), 0x42, 0, false},
		// Unencoded: the symbolic cmap by raw code -- (3,0), else (1,0).
		{"unencoded raw code in (1,0)", nil, cmapWith(mac), 0x8E, 2, true},
		{"unencoded prefers (3,0)", nil, cmapWith(mac, ms), 0x41, 0, false},
		{"unencoded (3,0) hit", nil, cmapWith(mac, ms), 0x42, 3, true},
		{"no cmaps", nil, map[string][]byte{}, 0x41, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gid, found := NewSimpleTrueTypeCmaps(tc.enc, tc.tables).Fallback(tc.cc)
			if gid != tc.gid || found != tc.found {
				t.Errorf("Fallback(%#x) = %d, %v; want %d, %v", tc.cc, gid, found, tc.gid, tc.found)
			}
		})
	}

	// The 0xF000 PUA alias is tried in a (3,0) format-4 subtable.
	pua := sub{3, 0, cmapFormat4Table(3, 0, map[uint16]uint16{0xF041: 4})[12:]}
	if gid, found := NewSimpleTrueTypeCmaps(nil, cmapWith(pua)).Fallback(0x41); gid != 4 || !found {
		t.Errorf("PUA alias: %d, %v; want 4, true", gid, found)
	}
}

// TestSimpleTrueTypeCmapsRawCode covers the lookup a lenient viewer applies
// whatever the encoding: the symbolic cmap -- (3,0), else (1,0) -- by the raw
// code or its 0xF000 alias.
func TestSimpleTrueTypeCmapsRawCode(t *testing.T) {
	mac := sub{1, 0, format0(map[byte]byte{0x41: 1})}
	ms := sub{3, 0, format0(map[byte]byte{0x42: 3})}
	winAnsi := pdf.PDFName{Value: "WinAnsiEncoding"}
	for _, tc := range []struct {
		name   string
		tables map[string][]byte
		cc     int
		gid    int
		found  bool
	}{
		{"(1,0) when there is no (3,0)", cmapWith(mac), 0x41, 1, true},
		{"(3,0) preferred", cmapWith(mac, ms), 0x42, 3, true},
		{"(3,0) present, code only in (1,0)", cmapWith(mac, ms), 0x41, 0, false},
		{"no cmaps", map[string][]byte{}, 0x41, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gid, found := NewSimpleTrueTypeCmaps(winAnsi, tc.tables).RawCode(tc.cc)
			if gid != tc.gid || found != tc.found {
				t.Errorf("RawCode(%#x) = %d, %v; want %d, %v", tc.cc, gid, found, tc.gid, tc.found)
			}
		})
	}
}
