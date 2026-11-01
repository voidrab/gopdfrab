package fontprog

import (
	"encoding/binary"
	"reflect"
	"testing"
)

// format4Seg is one segment of a hand-built format-4 subtable.
type format4Seg struct {
	start, end  uint16
	delta       int16
	rangeOffset uint16 // 0, or an absolute value the helper rewrites for "glyphs"
	glyphs      bool   // point idRangeOffset at the glyph-id array
}

// format4Sub builds a format-4 subtable from explicit segments, so the decoder
// is tested against bytes the package's own writer never produces
// (idRangeOffset, negative deltas).
func format4Sub(glyphIDs []uint16, segs ...format4Seg) []byte {
	n := len(segs)
	x2 := n * 2
	endOff := 14
	startOff := endOff + x2 + 2
	deltaOff := startOff + x2
	rangeOff := deltaOff + x2
	glyphOff := rangeOff + x2
	sub := make([]byte, glyphOff+2*len(glyphIDs))
	binary.BigEndian.PutUint16(sub[0:], 4)
	binary.BigEndian.PutUint16(sub[2:], uint16(len(sub)))
	binary.BigEndian.PutUint16(sub[6:], uint16(x2))
	for i, s := range segs {
		binary.BigEndian.PutUint16(sub[endOff+i*2:], s.end)
		binary.BigEndian.PutUint16(sub[startOff+i*2:], s.start)
		binary.BigEndian.PutUint16(sub[deltaOff+i*2:], uint16(s.delta))
		ro := s.rangeOffset
		if s.glyphs {
			ro = uint16(glyphOff - (rangeOff + i*2))
		}
		binary.BigEndian.PutUint16(sub[rangeOff+i*2:], ro)
	}
	for i, g := range glyphIDs {
		binary.BigEndian.PutUint16(sub[glyphOff+i*2:], g)
	}
	return sub
}

func TestParseCmapFormat4(t *testing.T) {
	sub := format4Sub([]uint16{5, 0, 7},
		format4Seg{start: 65, end: 67, delta: -5},                // direct: gid = code - 5
		format4Seg{start: 100, end: 102, delta: 1, glyphs: true}, // via the glyph-id array
		format4Seg{start: 200, end: 201, rangeOffset: 2000},      // array index past the data
		format4Seg{start: 50, end: 50, delta: -50},               // maps to gid 0: absent
		format4Seg{start: 0xFFFF, end: 0xFFFF, delta: 1},
	)
	got := ParseCmapFormat4(sub)
	want := map[uint16]uint16{65: 60, 66: 61, 67: 62, 100: 6, 102: 8}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseCmapFormat4 = %v, want %v", got, want)
	}

	if ParseCmapFormat4(nil) != nil {
		t.Error("nil subtable should decode to nil")
	}
	wrong := append([]byte(nil), sub...)
	wrong[1] = 6
	if ParseCmapFormat4(wrong) != nil {
		t.Error("a non-format-4 subtable should decode to nil")
	}
	if ParseCmapFormat4(sub[:len(sub)-20]) != nil {
		t.Error("a subtable cut short of its range-offset array should decode to nil")
	}
}

func TestTTWindowsBMPCmapAndSymbolicCmap(t *testing.T) {
	win := format6Sub(1, 1)
	sym := format6Sub(2, 2)
	mac := format6Sub(3, 3)
	other := format6Sub(4, 4)

	tabs := func(recs ...cmapRecord) map[string][]byte {
		return map[string][]byte{"cmap": cmapTable(recs...)}
	}
	if got := TTWindowsBMPCmap(tabs(cmapRecord{1, 0, mac}, cmapRecord{3, 1, win})); !reflect.DeepEqual(got[:len(win)], win) {
		t.Errorf("TTWindowsBMPCmap should pick (3,1); got %v", got)
	}
	// A (3,1) record pointing outside the table is skipped.
	bad := tabs(cmapRecord{3, 1, win})
	binary.BigEndian.PutUint32(bad["cmap"][8:], 9999)
	if TTWindowsBMPCmap(bad) != nil {
		t.Error("an out-of-range (3,1) offset should be ignored")
	}
	// numTables larger than the records present stops at the table's end.
	short := tabs(cmapRecord{1, 0, mac})
	short["cmap"][3] = 5
	if TTWindowsBMPCmap(short) != nil {
		t.Error("a record count past the table should end the scan")
	}

	// TTCmapSubtable picks the first record matching platform and encoding.
	got := TTCmapSubtable(tabs(cmapRecord{1, 0, mac}, cmapRecord{3, 0, sym}), 3, 0)
	if !reflect.DeepEqual(got[:len(sym)], sym) {
		t.Errorf("TTCmapSubtable(3,0) = %v", got)
	}
	got = TTCmapSubtable(tabs(cmapRecord{3, 1, other}, cmapRecord{1, 0, mac}, cmapRecord{1, 0, other}), 1, 0)
	if !reflect.DeepEqual(got[:len(mac)], mac) {
		t.Errorf("TTCmapSubtable(1,0) should return the first (1,0); got %v", got)
	}
	if TTCmapSubtable(tabs(cmapRecord{3, 1, other}), 3, 0) != nil {
		t.Error("TTCmapSubtable should be nil with no matching record")
	}
	// Out-of-range offsets are skipped, and a short record array ends the scan.
	skip := tabs(cmapRecord{1, 0, other}, cmapRecord{1, 0, mac})
	binary.BigEndian.PutUint32(skip["cmap"][8:], 9999)
	got = TTCmapSubtable(skip, 1, 0)
	if !reflect.DeepEqual(got[:len(mac)], mac) {
		t.Errorf("an out-of-range offset should be skipped; got %v", got)
	}
	shortMac := tabs(cmapRecord{1, 0, mac})
	shortMac["cmap"][3] = 5
	if got := TTCmapSubtable(shortMac, 1, 0); !reflect.DeepEqual(got[:len(mac)], mac) {
		t.Errorf("a record count past the table should keep what was found; got %v", got)
	}
	if TTCmapSubtable(map[string][]byte{"cmap": {0, 0}}, 1, 0) != nil {
		t.Error("a truncated cmap should give nil")
	}
}

func TestParseCmapSubtableDispatch(t *testing.T) {
	sub0 := make([]byte, 262)
	sub0[6+7] = 3
	if m := ParseCmapSubtable(sub0); m[7] != 3 {
		t.Errorf("format 0 dispatch = %v", m)
	}
	sub4 := cmapFormat4Table(3, 1, map[uint16]uint16{65: 1})[12:]
	if m := ParseCmapSubtable(sub4); m[65] != 1 {
		t.Errorf("format 4 dispatch = %v", m)
	}
	if ParseCmapSubtable([]byte{0}) != nil {
		t.Error("a one-byte subtable should give nil")
	}
}

func TestTTAdvanceWidthTruncatedHmtx(t *testing.T) {
	head := make([]byte, 20)
	binary.BigEndian.PutUint16(head[18:], 2000)
	tables := func(nHM int, hmtx []byte) map[string][]byte {
		return map[string][]byte{"head": head, "hhea": hheaWithNumHMetrics(nHM), "hmtx": hmtx}
	}
	// 3 metrics promised, 1 present: gid 1 is past the data.
	if w := TTAdvanceWidth(tables(3, []byte{0x03, 0xE8, 0, 0}), 1); w != -1 {
		t.Errorf("gid past a truncated hmtx = %v, want -1", w)
	}
	// 5 metrics promised, 1 present: the fallback to the last one is past it.
	if w := TTAdvanceWidth(tables(5, []byte{0x03, 0xE8, 0, 0}), 9); w != -1 {
		t.Errorf("fallback past a truncated hmtx = %v, want -1", w)
	}
	// No metrics at all: the width is 0.
	if w := TTAdvanceWidth(tables(0, []byte{1, 2, 3, 4}), 0); w != 0 {
		t.Errorf("numberOfHMetrics = 0 gives %v, want 0", w)
	}
	// Scaled by unitsPerEm: 1000 units at 2000/em is 500.
	if w := TTAdvanceWidth(tables(1, []byte{0x03, 0xE8, 0, 0}), 0); w != 500 {
		t.Errorf("1000 units at upm 2000 = %v, want 500", w)
	}
}

func TestTTLocaHasGlyphLongFormat(t *testing.T) {
	head := make([]byte, 52)
	binary.BigEndian.PutUint16(head[50:], 1)
	loca := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 12} // gid 0 empty, gid 1 has 12 bytes
	has := TTLocaHasGlyph(map[string][]byte{"head": head, "loca": loca})
	if has(0) {
		t.Error("gid 0 has an empty span")
	}
	if !has(1) {
		t.Error("gid 1 has a 12-byte span")
	}
	if has(2) {
		t.Error("gid 2 is past the loca table")
	}
}

func TestParseSfntTableBounds(t *testing.T) {
	// Directory with three tables: one in range, one whose offset is past the
	// data (skipped), one whose length overruns the data (clipped).
	data := make([]byte, 12+3*16+4)
	binary.BigEndian.PutUint32(data, 0x00010000)
	binary.BigEndian.PutUint16(data[4:], 3)
	put := func(i int, tag string, off, ln uint32) {
		rec := data[12+i*16:]
		copy(rec, tag)
		binary.BigEndian.PutUint32(rec[8:], off)
		binary.BigEndian.PutUint32(rec[12:], ln)
	}
	put(0, "aaaa", uint32(12+3*16), 4)
	put(1, "bbbb", 9999, 4)
	put(2, "cccc", uint32(12+3*16), 100)
	data[12+3*16] = 7
	tables, ok := ParseSfnt(data)
	if !ok {
		t.Fatal("ParseSfnt failed")
	}
	if _, present := tables["bbbb"]; present {
		t.Error("a table with an out-of-range offset should be skipped")
	}
	if len(tables["aaaa"]) != 4 || tables["aaaa"][0] != 7 {
		t.Errorf("table aaaa = %v", tables["aaaa"])
	}
	if len(tables["cccc"]) != 4 {
		t.Errorf("table cccc should be clipped to the data: %v", tables["cccc"])
	}
	// A directory that does not fit in the data.
	if _, ok := ParseSfnt(data[:20]); ok {
		t.Error("a truncated directory should fail")
	}
}

func TestParseCmapFormat6Truncated(t *testing.T) {
	sub := format6Sub(65, 1, 2, 3)
	if m := ParseCmapFormat6(sub); !reflect.DeepEqual(m, map[uint16]uint16{65: 1, 66: 2, 67: 3}) {
		t.Errorf("ParseCmapFormat6 = %v", m)
	}
	if ParseCmapFormat6(sub[:len(sub)-2]) != nil {
		t.Error("a subtable shorter than its declared count should decode to nil")
	}
}

func TestSummarizeCmap(t *testing.T) {
	if _, ok := SummarizeCmap(map[string][]byte{}); ok {
		t.Error("SummarizeCmap without a cmap table = ok")
	}
	tables := map[string][]byte{"cmap": cmapFormat4Table(3, 0, map[uint16]uint16{0xF041: 1})}
	if got, ok := SummarizeCmap(tables); !ok || got != (CmapSummary{Count: 1, Symbol: true}) {
		t.Errorf("SummarizeCmap = %+v, %v", got, ok)
	}
}
