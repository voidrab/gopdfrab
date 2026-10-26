package fontedit

import (
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/voidrab/gopdfrab/internal/fontprog"
)

func TestAddWindowsUnicodeCmap(t *testing.T) {
	mac := sub{1, 0, format0(map[byte]byte{0xB0: 1})}
	ms := sub{3, 0, format0(map[byte]byte{0xB0: 1})}
	font := PackSfnt(map[string][]byte{"cmap": cmapWith(mac, ms)["cmap"], "maxp": {0, 0, 0x50, 0, 0, 3}})

	out, changed := AddWindowsUnicodeCmap(font, map[uint16]uint16{0x00B0: 1})
	if !changed {
		t.Fatal("expected a (3,1) subtable to be added")
	}
	tables, ok := fontprog.ParseSfnt(out)
	if !ok {
		t.Fatal("output does not parse")
	}
	if got := fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(tables)); got[0x00B0] != 1 {
		t.Errorf("(3,1) = %v, want U+00B0 -> 1", got)
	}
	// The existing subtables survive, and the records stay sorted.
	if got := fontprog.ParseCmapSubtable(fontprog.TTCmapSubtable(tables, 1, 0)); got[0xB0] != 1 {
		t.Errorf("(1,0) lost: %v", got)
	}
	if got := fontprog.ParseCmapSubtable(fontprog.TTCmapSubtable(tables, 3, 0)); got[0xB0] != 1 {
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
	tables, _ = fontprog.ParseSfnt(out2)
	got := fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(tables))
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
	tables, _ = fontprog.ParseSfnt(out3)
	if !changed || fontprog.TTCmapSubtable(tables, 1, 0) != nil || fontprog.ParseCmapSubtable(fontprog.TTCmapSubtable(tables, 3, 0))[0xB0] != 1 {
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
