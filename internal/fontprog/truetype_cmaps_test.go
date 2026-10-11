package fontprog

import (
	"encoding/binary"
	"reflect"
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
	win := buildCmapFormat4Table(3, 1, map[uint16]uint16{'A': 1})
	tables, _ := ParseSfnt(PackSfnt(map[string][]byte{"cmap": win}))
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
	pua := sub{3, 0, buildCmapFormat4Table(3, 0, map[uint16]uint16{0xF041: 4})[12:]}
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

func TestAddWindowsUnicodeCmap(t *testing.T) {
	mac := sub{1, 0, format0(map[byte]byte{0xB0: 1})}
	ms := sub{3, 0, format0(map[byte]byte{0xB0: 1})}
	font := PackSfnt(map[string][]byte{"cmap": cmapWith(mac, ms)["cmap"], "maxp": {0, 0, 0x50, 0, 0, 3}})

	out, changed := AddWindowsUnicodeCmap(font, map[uint16]uint16{0x00B0: 1})
	if !changed {
		t.Fatal("expected a (3,1) subtable to be added")
	}
	tables, ok := ParseSfnt(out)
	if !ok {
		t.Fatal("output does not parse")
	}
	if got := ParseCmapFormat4(TTWindowsBMPCmap(tables)); got[0x00B0] != 1 {
		t.Errorf("(3,1) = %v, want U+00B0 -> 1", got)
	}
	// The existing subtables survive, and the records stay sorted.
	if got := ParseCmapSubtable(TTCmapSubtable(tables, 1, 0)); got[0xB0] != 1 {
		t.Errorf("(1,0) lost: %v", got)
	}
	if got := ParseCmapSubtable(TTCmapSubtable(tables, 3, 0)); got[0xB0] != 1 {
		t.Errorf("(3,0) lost: %v", got)
	}
	cmap := tables["cmap"]
	var ids []uint32
	for i := range int(binary.BigEndian.Uint16(cmap[2:])) {
		ids = append(ids, binary.BigEndian.Uint32(cmap[4+8*i:]))
	}
	want := []uint32{0x00010000, 0x00030000, 0x00030001} // (1,0), (3,0), (3,1)
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("records = %x, want %x", ids, want)
	}

	// Already mapped: nothing to do.
	if _, changed := AddWindowsUnicodeCmap(out, map[uint16]uint16{0x00B0: 1}); changed {
		t.Error("re-adding an existing mapping reported a change")
	}
	// An existing (3,1) mapping wins over a new one for the same Unicode
	// value, and its record is replaced rather than duplicated.
	out2, changed := AddWindowsUnicodeCmap(out, map[uint16]uint16{0x00B0: 2, 'A': 2})
	tables, _ = ParseSfnt(out2)
	got := ParseCmapFormat4(TTWindowsBMPCmap(tables))
	if !changed || got[0x00B0] != 1 || got['A'] != 2 {
		t.Errorf("merge: changed=%v (3,1)=%v; want U+00B0 -> 1 kept, 'A' -> 2 added", changed, got)
	}
	if n := binary.BigEndian.Uint16(tables["cmap"][2:]); n != 3 {
		t.Errorf("%d cmap records after merge, want 3", n)
	}

	// A record pointing outside the table is dropped.
	bad := cmapWith(mac, ms)["cmap"]
	binary.BigEndian.PutUint32(bad[8:], 9999) // the (1,0) record
	out3, changed := AddWindowsUnicodeCmap(PackSfnt(map[string][]byte{"cmap": bad}), map[uint16]uint16{0x00B0: 1})
	tables, _ = ParseSfnt(out3)
	if !changed || TTCmapSubtable(tables, 1, 0) != nil || ParseCmapSubtable(TTCmapSubtable(tables, 3, 0))[0xB0] != 1 {
		t.Error("out-of-range (1,0) record not dropped, or (3,0) lost")
	}
	if n := binary.BigEndian.Uint16(tables["cmap"][2:]); n != 2 {
		t.Errorf("%d records, want 2", n)
	}

	// Not a font, or no cmap table: unchanged.
	if _, changed := AddWindowsUnicodeCmap([]byte("junk"), map[uint16]uint16{1: 1}); changed {
		t.Error("junk input changed")
	}
	if _, changed := AddWindowsUnicodeCmap(PackSfnt(map[string][]byte{"maxp": {0, 0, 0x50, 0, 0, 3}}), map[uint16]uint16{1: 1}); changed {
		t.Error("font without cmap changed")
	}
}
