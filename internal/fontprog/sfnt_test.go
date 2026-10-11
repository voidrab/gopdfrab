package fontprog

import (
	"encoding/binary"
	"testing"
)

// TestCmapFormat0And6 covers the format-0 and format-6 cmap decoders with
// hand-built subtables, including the dispatch in ParseCmapSubtable.
func TestCmapFormat0And6(t *testing.T) {
	sub0 := make([]byte, 262)
	binary.BigEndian.PutUint16(sub0[0:], 0)
	sub0[6+65] = 5 // code 'A' -> gid 5
	if m := ParseCmapFormat0(sub0); m[65] != 5 {
		t.Errorf("ParseCmapFormat0[65] = %d, want 5", m[65])
	}
	if ParseCmapFormat0([]byte{0, 0}) != nil {
		t.Error("ParseCmapFormat0 on short input should be nil")
	}

	sub6 := make([]byte, 12)
	binary.BigEndian.PutUint16(sub6[0:], 6)
	binary.BigEndian.PutUint16(sub6[6:], 65) // firstCode
	binary.BigEndian.PutUint16(sub6[8:], 1)  // count
	binary.BigEndian.PutUint16(sub6[10:], 5) // gid
	if m := ParseCmapFormat6(sub6); m[65] != 5 {
		t.Errorf("ParseCmapFormat6[65] = %d, want 5", m[65])
	}
	if m := ParseCmapSubtable(sub6); m[65] != 5 {
		t.Error("ParseCmapSubtable(format6) failed")
	}
	if ParseCmapSubtable([]byte{0x00, 0x63}) != nil { // format 99
		t.Error("ParseCmapSubtable of unknown format should be nil")
	}
}

// TestTrueTypeTableGuards exercises the short-buffer / out-of-range defensive
// branches of the low-level sfnt table accessors using synthetic tables (the
// real LiberationSans TTF only ever hits their "happy path").
func TestTrueTypeTableGuards(t *testing.T) {
	if TTNumGlyphs(map[string][]byte{}) != 0 {
		t.Error("TTNumGlyphs should be 0 with no maxp table")
	}
	if TTNumGlyphs(map[string][]byte{"maxp": {0, 0}}) != 0 {
		t.Error("TTNumGlyphs should be 0 with a truncated maxp table")
	}

	if TTLocaHasGlyph(map[string][]byte{})(0) {
		t.Error("TTLocaHasGlyph should be false with no loca/head tables")
	}
	// A format-0 (short) loca table: 2-byte entries.
	head0 := make([]byte, 52) // locFormat (offset 50) = 0
	shortLoca := map[string][]byte{"head": head0, "loca": {0, 0, 0, 2}}
	hasGlyph := TTLocaHasGlyph(shortLoca)
	if !hasGlyph(0) {
		t.Error("TTLocaHasGlyph(format0) should detect a non-empty glyph 0")
	}
	if hasGlyph(5) {
		t.Error("TTLocaHasGlyph(format0) should be false past the end of the table")
	}

	if TTGlyphInRange(map[string][]byte{})(0) {
		t.Error("TTGlyphInRange should be false with no maxp table")
	}

	if TTAdvanceWidth(map[string][]byte{}, 0) != -1 {
		t.Error("TTAdvanceWidth should be -1 with no hmtx/hhea/head tables")
	}
	head := make([]byte, 20)
	head[18], head[19] = 0x03, 0xE8 // unitsPerEm = 1000
	hhea := make([]byte, 36)
	hhea[34], hhea[35] = 0x00, 0x01                                                     // numberOfHMetrics = 1
	tables := map[string][]byte{"head": head, "hhea": hhea, "hmtx": {0x01, 0xF4, 0, 0}} // aw=500
	if w := TTAdvanceWidth(tables, 0); w != 500 {
		t.Errorf("TTAdvanceWidth = %v, want 500", w)
	}
	// gid beyond numberOfHMetrics falls back to the last entry.
	if w := TTAdvanceWidth(tables, 5); w != 500 {
		t.Errorf("TTAdvanceWidth(beyond nHM) = %v, want 500 (fallback)", w)
	}
	headZeroUPM := make([]byte, 20) // unitsPerEm = 0
	if TTAdvanceWidth(map[string][]byte{"head": headZeroUPM, "hhea": hhea, "hmtx": {0, 0}}, 0) != -1 {
		t.Error("TTAdvanceWidth should be -1 when unitsPerEm is 0")
	}

	if TTWindowsBMPCmap(map[string][]byte{}) != nil {
		t.Error("TTWindowsBMPCmap should be nil with no cmap table")
	}
	if TTWindowsBMPCmap(map[string][]byte{"cmap": {0, 0, 0, 1}}) != nil {
		t.Error("TTWindowsBMPCmap should be nil when no (3,1) subtable exists")
	}
	if TTCmapSubtable(map[string][]byte{}, 3, 0) != nil {
		t.Error("TTCmapSubtable should be nil with no cmap table")
	}

	if ParseCmapFormat4([]byte{0, 4}) != nil {
		t.Error("ParseCmapFormat4 should be nil for a too-short subtable")
	}
	if ParseCmapFormat6([]byte{0, 6}) != nil {
		t.Error("ParseCmapFormat6 should be nil for a too-short subtable")
	}
}

// TestTTGlyphPresentEmptyOutlines pins what "the font defines this glyph"
// means for 6.3.5: an empty loca entry is how TrueType encodes a glyph with no
// contours -- a space -- and such a glyph is defined, not missing.
//
// The check used to accept an empty outline only when its advance width was
// also zero. A space in a monospaced subset is empty and 600/1000 wide, so
// every one of those fonts was reported as missing a glyph it plainly defines
// (Ghostscript's PDF/A output, which veraPDF passes). Only an out-of-range
// glyph id is genuinely absent.
func TestTTGlyphPresentEmptyOutlines(t *testing.T) {
	head := make([]byte, 52) // indexToLocFormat (offset 50) = 0, unitsPerEm at 18
	head[18], head[19] = 0x03, 0xE8
	hhea := make([]byte, 36)
	hhea[34], hhea[35] = 0x00, 0x03 // numberOfHMetrics = 3
	tables := map[string][]byte{
		"head": head,
		"hhea": hhea,
		// gid 0: outline, zero advance. gid 1: empty outline, 600 advance
		// (a space). gid 2: outline, 600 advance.
		"loca": {0, 0, 0, 10, 0, 10, 0, 20},
		"hmtx": {0x00, 0x00, 0, 0, 0x02, 0x58, 0, 0, 0x02, 0x58, 0, 0},
		"maxp": {0, 0, 0, 0, 0, 3}, // numGlyphs = 3
	}

	present := TTGlyphPresent(tables)
	for gid := range 3 {
		if !present(gid) {
			t.Errorf("gid %d reported absent; every glyph the font defines is present", gid)
		}
	}
	if present(3) {
		t.Error("gid 3 is past numGlyphs and must be reported absent")
	}
}

func TestParseSfntAndCFFIndexMalformed(t *testing.T) {
	if _, ok := ParseSfnt(nil); ok {
		t.Error("ParseSfnt should fail on empty input")
	}
	if _, ok := ParseSfnt([]byte("BAD!garbagegarbagegarbage")); ok {
		t.Error("ParseSfnt should fail on an unrecognized version tag")
	}
	// Valid tag but zero tables.
	zeroTables := []byte{0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0, 0, 0, 0, 0, 0}
	if _, ok := ParseSfnt(zeroTables); ok {
		t.Error("ParseSfnt should fail with numTables=0")
	}

	if entries, end := ParseCFFIndex(nil, 0); entries != nil || end != 0 {
		t.Errorf("ParseCFFIndex(empty) = %v, %d, want nil, 0", entries, end)
	}
	if entries, _ := ParseCFFIndex([]byte{0, 0}, 0); entries != nil {
		t.Error("ParseCFFIndex with count=0 should return nil entries")
	}

	if cffStringIndexEntries(nil) != nil {
		t.Error("cffStringIndexEntries should be nil for too-short input")
	}
}
