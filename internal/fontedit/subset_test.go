package fontedit

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"

	"github.com/voidrab/gopdfrab/internal/fontprog"
)

func cmap31(t *testing.T, tables map[string][]byte) map[uint16]uint16 {
	t.Helper()
	m := fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(tables))
	if m == nil {
		t.Fatal("no (3,1) format-4 cmap in output")
	}
	return m
}

func TestSubsetTrueType(t *testing.T) {
	src := subsetSource()
	out, err := SubsetTrueType(src.build(), []uint16{'A', 'B', ' ', 'Q', 'A'})
	if err != nil {
		t.Fatal(err)
	}
	tables := tt(t, out)
	srcTables := tt(t, src.build())

	// Closure: 0, 1, 2, 4 (component of B), 5 -> dense IDs 0, 1, 2, 3, 4.
	if n := fontprog.TTNumGlyphs(tables); n != 5 {
		t.Fatalf("glyph count = %d, want 5", n)
	}
	if got, want := cmap31(t, tables), (map[uint16]uint16{'A': 1, 'B': 2, ' ': 4}); !reflect.DeepEqual(got, want) {
		t.Errorf("cmap = %v, want %v", got, want)
	}
	if got := fontprog.GlyfRecord(tables, 1); !bytes.Equal(got, fontprog.GlyfRecord(srcTables, 1)) {
		t.Errorf("glyph 1 = %v, want the source's glyph 1", got)
	}
	// The composite points at the renumbered component (4 -> 3) and
	// the renumbered 'A' (1 -> 1).
	if got := fontprog.GlyfComponents(fontprog.GlyfRecord(tables, 2)); !reflect.DeepEqual(got, []int{3, 1}) {
		t.Errorf("composite components = %v, want [3 1]", got)
	}
	// The blank space becomes an explicit empty record, keeping its advance.
	if got := fontprog.GlyfRecord(tables, 4); !bytes.Equal(got, fontprog.EmptyGlyfRecord) {
		t.Errorf("space record = %v, want the explicit empty record", got)
	}
	if aw := fontprog.TTAdvanceWidth(tables, 4); aw != 250 {
		t.Errorf("space advance = %v, want 250", aw)
	}
	if aw := fontprog.TTAdvanceWidth(tables, 3); aw != 900 {
		t.Errorf("component advance = %v, want 900", aw)
	}
	if !bytes.Equal(tables["post"], make([]byte, 32)) {
		t.Errorf("post = %v, want the minimal format-3 table", tables["post"])
	}
	if binary.BigEndian.Uint16(tables["head"][50:]) != 1 {
		t.Error("head should declare the long loca format")
	}
	for _, tag := range []string{"OS/2", "name", "cvt "} {
		if !bytes.Equal(tables[tag], srcTables[tag]) {
			t.Errorf("table %q should be carried over unchanged", tag)
		}
	}
	for _, tag := range []string{"fpgm", "prep"} {
		if _, ok := tables[tag]; ok {
			t.Errorf("table %q was not in the source and should not appear", tag)
		}
	}
}

func TestSubsetTrueTypeErrors(t *testing.T) {
	if _, err := SubsetTrueType([]byte("nope"), nil); err == nil {
		t.Error("garbage input should be an error")
	}
	noCmap := subsetSource()
	noCmap.cmap = nil
	if _, err := SubsetTrueType(noCmap.build(), nil); err == nil {
		t.Error("a source without a (3,1) cmap should be an error")
	}
}

func TestSubsetTrueTypeSymbolic(t *testing.T) {
	src := subsetSource().build()
	out, err := SubsetTrueTypeSymbolic(src, map[int]uint16{
		1:   'A',
		2:   'B',
		3:   'Q', // not in the source cmap
		300: 'A', // outside the single-byte code range
		-1:  'A',
	})
	if err != nil {
		t.Fatal(err)
	}
	tables := tt(t, out)
	if n := fontprog.TTNumGlyphs(tables); n != 4 { // 0, 1, 2, and B's component 4
		t.Errorf("glyph count = %d, want 4", n)
	}
	sym := fontprog.ParseCmapFormat4(fontprog.TTCmapSubtable(tables, 3, 0))
	want := map[uint16]uint16{1: 1, 0xF001: 1, 2: 2, 0xF002: 2}
	if !reflect.DeepEqual(sym, want) {
		t.Errorf("symbolic cmap = %v, want %v", sym, want)
	}
	if fontprog.TTWindowsBMPCmap(tables) != nil {
		t.Error("the output should carry no (3,1) subtable")
	}
	if got := fontprog.GlyfComponents(fontprog.GlyfRecord(tables, 2)); !reflect.DeepEqual(got, []int{3, 1}) {
		t.Errorf("composite components = %v, want [3 1]", got)
	}

	if _, err := SubsetTrueTypeSymbolic([]byte("nope"), nil); err == nil {
		t.Error("garbage input should be an error")
	}
	noCmap := subsetSource()
	noCmap.cmap = nil
	if _, err := SubsetTrueTypeSymbolic(noCmap.build(), nil); err == nil {
		t.Error("a source without a (3,1) cmap should be an error")
	}
}

func TestSubsetTrueTypeForCID(t *testing.T) {
	srcFont := subsetSource()
	src := srcFont.build()
	srcTables := tt(t, src)
	out, err := SubsetTrueTypeForCID(src, map[uint16][]int{
		'A': {3},
		'B': {6},
		'Q': {8}, // absent from the source cmap, but still sizes the output
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tables := tt(t, out)
	// Targets 3 and 6; B's component (old 4) lands after the largest target 8.
	if n := fontprog.TTNumGlyphs(tables); n != 10 {
		t.Fatalf("glyph count = %d, want 10", n)
	}
	if got := fontprog.GlyfRecord(tables, 3); !bytes.Equal(got, fontprog.GlyfRecord(srcTables, 1)) {
		t.Errorf("glyph 3 = %v, want the source 'A'", got)
	}
	if got := fontprog.GlyfComponents(fontprog.GlyfRecord(tables, 6)); !reflect.DeepEqual(got, []int{9, 3}) {
		t.Errorf("composite components = %v, want [9 3]", got)
	}
	if got := fontprog.GlyfRecord(tables, 9); !bytes.Equal(got, fontprog.GlyfRecord(srcTables, 4)) {
		t.Errorf("glyph 9 = %v, want the source component glyph", got)
	}
	for _, gid := range []int{0, 1, 2, 4, 5, 7, 8} {
		if rec := fontprog.GlyfRecord(tables, gid); len(rec) != 0 {
			t.Errorf("unassigned glyph %d should be an empty placeholder, got %v", gid, rec)
		}
	}
	if aw := fontprog.TTAdvanceWidth(tables, 3); aw != 600 {
		t.Errorf("glyph 3 advance = %v, want 600", aw)
	}
	if m := cmap31(t, tables); len(m) != 0 {
		t.Errorf("cmap = %v, want an empty (3,1) table", m)
	}

	if _, err := SubsetTrueTypeForCID([]byte("nope"), nil, nil); err == nil {
		t.Error("garbage input should be an error")
	}
	srcFont.cmap = nil
	if _, err := SubsetTrueTypeForCID(srcFont.build(), nil, nil); err == nil {
		t.Error("a source without a (3,1) cmap should be an error")
	}
}

// TestSubsetTrueTypeForCIDNotdef: a CID given in notdef gets the source's
// .notdef outline at exactly that glyph ID, advancing by the width asked for
// rather than the .notdef's own -- the CID drew the missing-glyph shape in the
// font being replaced, and the text after it must not move.
func TestSubsetTrueTypeForCIDNotdef(t *testing.T) {
	src := subsetSource().build()
	srcTables := tt(t, src)
	out, err := SubsetTrueTypeForCID(src, map[uint16][]int{'A': {3}}, map[int]float64{12: 1000, 2: 333.4})
	if err != nil {
		t.Fatal(err)
	}
	tables := tt(t, out)
	if n := fontprog.TTNumGlyphs(tables); n != 13 {
		t.Fatalf("glyph count = %d, want 13 (the notdef CID 12 sizes the output)", n)
	}
	for cid, want := range map[int]float64{12: 1000, 2: 333} {
		if got := fontprog.GlyfRecord(tables, cid); !bytes.Equal(got, fontprog.GlyfRecord(srcTables, 0)) {
			t.Errorf("glyph %d = %v, want the source .notdef", cid, got)
		}
		if aw := fontprog.TTAdvanceWidth(tables, cid); aw != want {
			t.Errorf("glyph %d advance = %v, want %v", cid, aw, want)
		}
	}
	if aw := fontprog.TTAdvanceWidth(tables, 3); aw != 600 {
		t.Errorf("glyph 3 advance = %v, want the source 'A' width 600", aw)
	}
}

func TestPromoteEmptyGlyphs(t *testing.T) {
	for _, short := range []bool{false, true} {
		src := subsetSource()
		src.shortLoca = short
		data := src.build()
		out, changed := PromoteEmptyGlyphs(data)
		if !changed {
			t.Fatalf("short=%v: a font with a blank glyph should change", short)
		}
		before, after := tt(t, data), tt(t, out)
		for gid := range 7 {
			want := fontprog.GlyfRecord(before, gid)
			if gid == 5 {
				want = fontprog.EmptyGlyfRecord
			}
			got := fontprog.GlyfRecord(after, gid)
			// Odd-length records gain a pad byte; the record itself is a prefix.
			if !bytes.HasPrefix(got, want) || len(got)-len(want) > 1 {
				t.Errorf("short=%v: glyph %d = %v, want %v", short, gid, got, want)
			}
		}
		if binary.BigEndian.Uint16(after["head"][50:]) != 1 {
			t.Errorf("short=%v: head should declare the long loca format", short)
		}
		if !bytes.Equal(after["hmtx"], before["hmtx"]) || !bytes.Equal(after["cmap"], before["cmap"]) {
			t.Errorf("short=%v: untouched tables must be byte-for-byte identical", short)
		}
	}

	// Nothing blank: the input comes back untouched.
	full := ttFont{glyphs: []ttGlyph{{rec: simpleGlyph(12, 1)}, {rec: simpleGlyph(12, 2)}}}.build()
	if out, changed := PromoteEmptyGlyphs(full); changed || !bytes.Equal(out, full) {
		t.Error("a font with no blank glyphs should be returned unchanged")
	}
	// Not a font, and a font with no glyph count.
	if out, changed := PromoteEmptyGlyphs([]byte("nope")); changed || string(out) != "nope" {
		t.Error("garbage should be returned unchanged")
	}
	noMaxp := PackSfnt(map[string][]byte{"head": make([]byte, 54)})
	if out, changed := PromoteEmptyGlyphs(noMaxp); changed || !bytes.Equal(out, noMaxp) {
		t.Error("a font without maxp should be returned unchanged")
	}
}

// TestPromoteEmptyGlyphsPadsTruncatedRecords: a zero-contour record cut short
// of its instructionLength -- 10 bytes, as some producers write .notdef -- is
// padded to the full 12, keeping its bbox. BFO's glyf parser otherwise reads
// past it and reports the font's glyphs missing and its widths inconsistent.
func TestPromoteEmptyGlyphsPadsTruncatedRecords(t *testing.T) {
	truncated := make([]byte, 10)
	binary.BigEndian.PutUint16(truncated[2:], 5) // xMin, kept
	binary.BigEndian.PutUint16(truncated[6:], 7) // xMax, kept
	shortOutline := simpleGlyph(10, 3)           // has a contour: not ours to touch
	full := make([]byte, 12)                     // already complete
	data := ttFont{glyphs: []ttGlyph{
		{rec: truncated, aw: 1000}, {rec: simpleGlyph(14, 1), aw: 500},
		{rec: shortOutline}, {rec: full}, {rec: []byte{0}},
	}}.build()

	out, changed := PromoteEmptyGlyphs(data)
	if !changed {
		t.Fatal("a truncated zero-contour record should be padded")
	}
	before, after := tt(t, data), tt(t, out)
	want := map[int][]byte{
		0: append(append([]byte(nil), truncated...), 0, 0),
		1: fontprog.GlyfRecord(before, 1),
		2: shortOutline,
		3: full,
		4: fontprog.EmptyGlyfRecord, // too short to hold numberOfContours at all
	}
	for gid, w := range want {
		if got := fontprog.GlyfRecord(after, gid); !bytes.Equal(got, w) {
			t.Errorf("glyph %d = %v, want %v", gid, got, w)
		}
	}
	if !bytes.Equal(after["hmtx"], before["hmtx"]) {
		t.Error("hmtx must be byte-for-byte identical")
	}
}

func TestPromoteEmptyGlyphsShortHead(t *testing.T) {
	// A head table too short to carry indexToLocFormat is left as it is.
	tables := map[string][]byte{
		"head": make([]byte, 8),
		"maxp": {0, 1, 0, 0, 0, 1},
		"loca": {0, 0, 0, 0, 0, 0, 0, 0},
		"glyf": nil,
	}
	// fontprog.GlyfRecord needs a 52-byte head to read the loca format, so every glyph
	// reads as empty and is promoted.
	out, changed := PromoteEmptyGlyphs(PackSfnt(tables))
	if !changed {
		t.Fatal("expected the empty glyph to be promoted")
	}
	if got := tt(t, out)["head"]; len(got) != 8 {
		t.Errorf("head length = %d, want it left at 8", len(got))
	}
}

func TestBuildSubsetTablesShortSourceTables(t *testing.T) {
	// Source tables too short to patch (hhea, maxp, head) are copied as they
	// are, and glyphs without metrics get zeroed hmtx entries.
	tables := map[string][]byte{"hhea": {1}, "maxp": {2}, "head": {3}}
	out := buildSubsetTables(tables, 2, map[int]int{1: 0}, map[int]int{0: 1})
	if !bytes.Equal(out["hhea"], []byte{1}) || !bytes.Equal(out["maxp"], []byte{2}) || !bytes.Equal(out["head"], []byte{3}) {
		t.Errorf("short tables were modified: %v %v %v", out["hhea"], out["maxp"], out["head"])
	}
	if !bytes.Equal(out["hmtx"], make([]byte, 8)) {
		t.Errorf("hmtx = %v, want zeroes", out["hmtx"])
	}
}

func TestTrimTrueTypeCmapToSingleSubtable(t *testing.T) {
	font := subsetSource()
	data := font.build()
	// Add a second, format-0 subtable ahead of the (3,1) one: it must go.
	tables := tt(t, data)
	tables["cmap"] = cmapTable(
		cmapRecord{1, 0, make([]byte, 262)},
		cmapRecord{3, 1, BuildCmapFormat4Table(3, 1, map[uint16]uint16{'A': 1})[12:]},
	)
	out, err := TrimTrueTypeCmapToSingleSubtable(PackSfnt(tables))
	if err != nil {
		t.Fatal(err)
	}
	got := tt(t, out)
	if n := binary.BigEndian.Uint16(got["cmap"][2:]); n != 1 {
		t.Errorf("cmap subtables = %d, want 1", n)
	}
	if m := cmap31(t, got); !reflect.DeepEqual(m, map[uint16]uint16{'A': 1}) {
		t.Errorf("cmap = %v", m)
	}
	if !bytes.Equal(got["glyf"], tables["glyf"]) || !bytes.Equal(got["loca"], tables["loca"]) {
		t.Error("glyph data must be carried over unchanged")
	}

	// A symbolic font whose only usable subtable is (3,0) keeps its platform.
	tables["cmap"] = cmapTable(cmapRecord{3, 0, BuildCmapFormat4Table(3, 0, map[uint16]uint16{0xF041: 1})[12:]})
	out, err = TrimTrueTypeCmapToSingleSubtable(PackSfnt(tables))
	if err != nil {
		t.Fatal(err)
	}
	cm := tt(t, out)["cmap"]
	if p, e := binary.BigEndian.Uint16(cm[4:]), binary.BigEndian.Uint16(cm[6:]); p != 3 || e != 0 {
		t.Errorf("platform/encoding = (%d,%d), want (3,0)", p, e)
	}

	if _, err := TrimTrueTypeCmapToSingleSubtable([]byte("nope")); err == nil {
		t.Error("garbage input should be an error")
	}
	tables["cmap"] = cmapTable(cmapRecord{1, 0, make([]byte, 262)})
	if _, err := TrimTrueTypeCmapToSingleSubtable(PackSfnt(tables)); err == nil {
		t.Error("a cmap with no format-4 subtable should be an error")
	}
}

func TestFirstFormat4CmapSubtable(t *testing.T) {
	f4 := BuildCmapFormat4Table(1, 0, map[uint16]uint16{65: 2})[12:]
	tabs := func(cmap []byte) map[string][]byte { return map[string][]byte{"cmap": cmap} }

	if p, e, m, ok := FirstFormat4CmapSubtable(tabs(cmapTable(cmapRecord{3, 1, f4}))); !ok || p != 3 || e != 1 || m[65] != 2 {
		t.Errorf("(3,1) preferred: got %d %d %v %v", p, e, m, ok)
	}
	// An unreadable record, an out-of-range one, then the first format 4.
	cm := cmapTable(cmapRecord{1, 0, make([]byte, 262)}, cmapRecord{2, 0, f4}, cmapRecord{0, 3, f4})
	binary.BigEndian.PutUint32(cm[4+8+4:], 9999) // second record: offset past the table
	if p, e, m, ok := FirstFormat4CmapSubtable(tabs(cm)); !ok || p != 0 || e != 3 || m[65] != 2 {
		t.Errorf("fallback scan: got %d %d %v %v", p, e, m, ok)
	}
	if _, _, _, ok := FirstFormat4CmapSubtable(tabs(nil)); ok {
		t.Error("no cmap should report not-ok")
	}
	if _, _, _, ok := FirstFormat4CmapSubtable(tabs(cmapTable(cmapRecord{1, 0, make([]byte, 262)}))); ok {
		t.Error("no format-4 subtable should report not-ok")
	}
	// A record count past the table's end stops the scan.
	trunc := []byte{0, 0, 0, 2, 0, 1, 0, 0, 0, 0, 0, 4}
	if _, _, _, ok := FirstFormat4CmapSubtable(tabs(trunc)); ok {
		t.Error("a record count past the table should end the scan")
	}
}

func TestRemapGlyfComponents(t *testing.T) {
	simple := simpleGlyph(12, 3)
	if got := remapGlyfComponents(simple, map[int]int{1: 2}); !bytes.Equal(got, simple) {
		t.Error("a simple glyph is returned unchanged")
	}
	rec := compositeGlyph(1, 2, 3)
	orig := append([]byte(nil), rec...)
	got := remapGlyfComponents(rec, map[int]int{1: 10, 3: 30}) // 2 has no mapping
	if comps := fontprog.GlyfComponents(got); !reflect.DeepEqual(comps, []int{10, 2, 30}) {
		t.Errorf("remapped components = %v, want [10 2 30]", comps)
	}
	if !bytes.Equal(rec, orig) {
		t.Error("the input record must not be modified")
	}
	cut := compositeGlyph(1, 2)[:10+6+2]
	if comps := fontprog.GlyfComponents(remapGlyfComponents(cut, map[int]int{1: 5})); !reflect.DeepEqual(comps, []int{5}) {
		t.Errorf("truncated remap = %v, want [5]", comps)
	}
}

// oddSpanFont is a two-glyph font whose first glyph is 13 bytes with no
// padding after it, as some real programs store it, and whose second is empty.
func oddSpanFont() []byte {
	tables := map[string][]byte{
		"head": append(make([]byte, 50), 0, 1, 0, 0), // long loca format
		"maxp": {0, 1, 0, 0, 0, 2},
		"loca": {0, 0, 0, 0, 0, 0, 0, 13, 0, 0, 0, 13},
		"glyf": simpleGlyph(13, 7),
		"cmap": BuildCmapFormat4Table(3, 1, map[uint16]uint16{'A': 0, 'B': 1}),
	}
	return PackSfnt(tables)
}

func TestOddLengthGlyphsArePadded(t *testing.T) {
	// Promoting the empty glyph rebuilds glyf, padding the odd record.
	out, changed := PromoteEmptyGlyphs(oddSpanFont())
	if !changed {
		t.Fatal("expected the empty glyph to be promoted")
	}
	tables := tt(t, out)
	if got := fontprog.GlyfRecord(tables, 0); len(got) != 14 || !bytes.HasPrefix(got, simpleGlyph(13, 7)) {
		t.Errorf("glyph 0 = %v, want the 13-byte record plus one pad byte", got)
	}
	if got := fontprog.GlyfRecord(tables, 1); !bytes.Equal(got, fontprog.EmptyGlyfRecord) {
		t.Errorf("glyph 1 = %v, want the explicit empty record", got)
	}

	// Subsetting pads it the same way.
	sub, err := SubsetTrueType(oddSpanFont(), []uint16{'A'})
	if err != nil {
		t.Fatal(err)
	}
	// 'A' maps to gid 0, which cmap decoding drops, so only .notdef remains.
	if rec := fontprog.GlyfRecord(tt(t, sub), 0); len(rec) != 14 {
		t.Errorf("subset .notdef = %v, want 14 padded bytes", rec)
	}
}

// TestTTRawHMetric covers every branch: the too-short-table guards, an
// explicit hmtx entry, the beyond-numberOfHMetrics fallback that reuses the
// last advance with its own lsb, and that same fallback when even the lsb
// column is missing (advance only).
func TestTTRawHMetric(t *testing.T) {
	hhea := hheaWithNumHMetrics(2)
	// gid0: advance 500 lsb 10; gid1: advance 600 lsb 20; gid2: lsb-only 30.
	hmtx := []byte{0x01, 0xF4, 0x00, 0x0A, 0x02, 0x58, 0x00, 0x14, 0x00, 0x1E}
	tables := map[string][]byte{"hhea": hhea, "hmtx": hmtx}

	if _, _, ok := ttRawHMetric(map[string][]byte{"hhea": hhea}, 0); ok {
		t.Error("ttRawHMetric with no hmtx table: ok = true, want false")
	}
	if _, _, ok := ttRawHMetric(map[string][]byte{"hmtx": hmtx, "hhea": hheaWithNumHMetrics(0)[:10]}, 0); ok {
		t.Error("ttRawHMetric with a truncated hhea: ok = true, want false")
	}
	if _, _, ok := ttRawHMetric(map[string][]byte{"hmtx": hmtx, "hhea": hheaWithNumHMetrics(0)}, 0); ok {
		t.Error("ttRawHMetric with numberOfHMetrics=0: ok = true, want false")
	}

	if aw, lsb, ok := ttRawHMetric(tables, 0); !ok || aw != 500 || lsb != 10 {
		t.Errorf("ttRawHMetric(gid 0) = (%d, %d, %v), want (500, 10, true)", aw, lsb, ok)
	}
	if aw, lsb, ok := ttRawHMetric(tables, 2); !ok || aw != 600 || lsb != 30 {
		t.Errorf("ttRawHMetric(gid 2, beyond numberOfHMetrics) = (%d, %d, %v), want (600, 30, true)", aw, lsb, ok)
	}
	if aw, lsb, ok := ttRawHMetric(tables, 3); !ok || aw != 600 || lsb != 0 {
		t.Errorf("ttRawHMetric(gid 3, past the lsb column too) = (%d, %d, %v), want (600, 0, true)", aw, lsb, ok)
	}

	// gid < numberOfHMetrics but hmtx is truncated before that entry.
	truncated := map[string][]byte{"hhea": hheaWithNumHMetrics(5), "hmtx": hmtx[:8]}
	if _, _, ok := ttRawHMetric(truncated, 3); ok {
		t.Error("ttRawHMetric(gid within numberOfHMetrics but past truncated hmtx) ok = true, want false")
	}
}
