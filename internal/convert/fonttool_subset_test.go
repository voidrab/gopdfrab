package convert

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/voidrab/gopdfrab/internal/verify"
)

func loadLiberationSansForTest(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("assets/fonts/LiberationSans-Regular.ttf")
	if err != nil {
		t.Skipf("bundled font not present: %v", err)
	}
	return data
}

// TestSubsetTrueTypeRoundTrips subsets a bundled Liberation Sans face down to
// a handful of Latin letters and checks the output re-parses, keeps exactly
// the requested glyphs (plus .notdef), and preserves their advance widths.
func TestSubsetTrueTypeRoundTrips(t *testing.T) {
	src := loadLiberationSansForTest(t)
	srcTables, ok := verify.ParseSfnt(src)
	if !ok {
		t.Fatalf("verify.ParseSfnt(src) failed")
	}
	srcCmap := verify.ParseCmapFormat4(verify.TTWindowsBMPCmap(srcTables))

	unicodes := []uint16{'A', 'B', 'C', 'a', 'b', 'c', ' '}
	out, err := subsetTrueType(src, unicodes)
	if err != nil {
		t.Fatalf("subsetTrueType: %v", err)
	}

	tables, ok := verify.ParseSfnt(out)
	if !ok {
		t.Fatalf("verify.ParseSfnt(subsetted output) failed -- not a valid sfnt")
	}

	cmap := verify.ParseCmapFormat4(verify.TTWindowsBMPCmap(tables))
	if cmap == nil {
		t.Fatalf("subsetted output has no usable (3,1) cmap")
	}
	glyphPresent := verify.TTGlyphPresent(tables)
	numGlyphs := verify.TTNumGlyphs(tables)
	if numGlyphs <= 0 || numGlyphs > len(unicodes)+1 {
		t.Errorf("ttNumGlyphs = %d, want a small subset (<= %d)", numGlyphs, len(unicodes)+1)
	}

	for _, u := range unicodes {
		gid, ok := cmap[u]
		if !ok {
			t.Errorf("unicode %q missing from subsetted cmap", rune(u))
			continue
		}
		if !glyphPresent(int(gid)) && u != ' ' {
			t.Errorf("unicode %q (gid %d) has no glyph data in subsetted output", rune(u), gid)
		}

		srcGID, srcOK := srcCmap[u]
		if !srcOK {
			continue
		}
		wantWidth := verify.TTAdvanceWidth(srcTables, int(srcGID))
		gotWidth := verify.TTAdvanceWidth(tables, int(gid))
		if wantWidth != gotWidth {
			t.Errorf("unicode %q: advance width = %d, want %d (source)", rune(u), gotWidth, wantWidth)
		}
	}

	// .notdef (gid 0) must always survive.
	if !verify.TTGlyphInRange(tables)(0) {
		t.Errorf("gid 0 (.notdef) missing from subsetted output")
	}
}

// TestSubsetTrueTypeSkipsUnmappedUnicodes checks that requesting a codepoint
// absent from the source cmap doesn't fail the whole subset.
func TestSubsetTrueTypeSkipsUnmappedUnicodes(t *testing.T) {
	src := loadLiberationSansForTest(t)
	out, err := subsetTrueType(src, []uint16{'A', 0xFFFE})
	if err != nil {
		t.Fatalf("subsetTrueType: %v", err)
	}
	tables, ok := verify.ParseSfnt(out)
	if !ok {
		t.Fatalf("verify.ParseSfnt(output) failed")
	}
	cmap := verify.ParseCmapFormat4(verify.TTWindowsBMPCmap(tables))
	if _, ok := cmap['A']; !ok {
		t.Errorf("'A' missing from output cmap")
	}
	if _, ok := cmap[0xFFFE]; ok {
		t.Errorf("unmapped codepoint 0xFFFE unexpectedly present in output cmap")
	}
}

// TestSubsetTrueTypeRetainsCompositeComponents checks that subsetting a
// composite glyph (e.g. an accented Latin letter, commonly built from a base
// letter + diacritic component) also retains and correctly remaps its
// component glyphs, rather than producing a dangling reference.
func TestSubsetTrueTypeRetainsCompositeComponents(t *testing.T) {
	src := loadLiberationSansForTest(t)
	tables, _ := verify.ParseSfnt(src)
	cmap := verify.ParseCmapFormat4(verify.TTWindowsBMPCmap(tables))

	// Find a composite glyph among common accented Latin-1 letters.
	var compositeUnicode uint16
	for _, u := range []uint16{0xC0, 0xC1, 0xC2, 0xC3, 0xC4, 0xC9, 0xD1} { // A-grave..N-tilde
		gid, ok := cmap[u]
		if !ok {
			continue
		}
		if glyfIsComposite(glyfRecord(tables, int(gid))) {
			compositeUnicode = u
			break
		}
	}
	if compositeUnicode == 0 {
		t.Skip("no composite glyph found among tested codepoints in bundled face")
	}

	out, err := subsetTrueType(src, []uint16{compositeUnicode})
	if err != nil {
		t.Fatalf("subsetTrueType: %v", err)
	}
	outTables, ok := verify.ParseSfnt(out)
	if !ok {
		t.Fatalf("verify.ParseSfnt(output) failed")
	}
	outCmap := verify.ParseCmapFormat4(verify.TTWindowsBMPCmap(outTables))
	gid, ok := outCmap[compositeUnicode]
	if !ok {
		t.Fatalf("composite glyph's unicode missing from subsetted cmap")
	}
	rec := glyfRecord(outTables, int(gid))
	if !glyfIsComposite(rec) {
		t.Fatalf("subsetted glyph is no longer composite")
	}
	numGlyphs := verify.TTNumGlyphs(outTables)
	for _, c := range glyfComponents(rec) {
		if c < 0 || c >= numGlyphs {
			t.Errorf("composite component gid %d out of range [0,%d) after remap", c, numGlyphs)
		}
	}
}

// TestTrimTrueTypeCmapToSingleSubtable checks that trimming preserves glyph
// data (glyf/loca/hmtx untouched) while reducing the cmap to one subtable.
func TestTrimTrueTypeCmapToSingleSubtable(t *testing.T) {
	src := loadLiberationSansForTest(t)
	srcTables, _ := verify.ParseSfnt(src)
	n, ok := trueTypeCmapSubtablesForTest(srcTables)
	if !ok {
		t.Fatalf("could not read source cmap subtable count")
	}
	if n < 2 {
		t.Skip("bundled face already has a single cmap subtable; nothing to trim")
	}

	out, err := trimTrueTypeCmapToSingleSubtable(src)
	if err != nil {
		t.Fatalf("trimTrueTypeCmapToSingleSubtable: %v", err)
	}
	outTables, ok := verify.ParseSfnt(out)
	if !ok {
		t.Fatalf("verify.ParseSfnt(output) failed")
	}
	got, ok := trueTypeCmapSubtablesForTest(outTables)
	if !ok || got != 1 {
		t.Errorf("output cmap subtable count = %d (ok=%v), want 1", got, ok)
	}

	// glyf/loca/hmtx must be byte-for-byte unchanged.
	for _, tag := range []string{"glyf", "loca", "hmtx"} {
		if string(outTables[tag]) != string(srcTables[tag]) {
			t.Errorf("table %q changed by cmap trim, want byte-for-byte unchanged", tag)
		}
	}

	// The surviving (3,1) mapping must still resolve the same glyphs.
	srcCmap := verify.ParseCmapFormat4(verify.TTWindowsBMPCmap(srcTables))
	outCmap := verify.ParseCmapFormat4(verify.TTWindowsBMPCmap(outTables))
	for u, gid := range srcCmap {
		if outCmap[u] != gid {
			t.Errorf("unicode %q: gid changed from %d to %d after trim", rune(u), gid, outCmap[u])
		}
	}
}

// TestComponentRecordSize covers every argument/scale flag combination:
// word vs. byte args, and no-scale/scale/x-and-y-scale/two-by-two.
func TestComponentRecordSize(t *testing.T) {
	tests := []struct {
		name  string
		flags uint16
		want  int
	}{
		{"byte args, no scale", 0x0000, 6},
		{"word args, no scale", 0x0001, 8},
		{"byte args, WE_HAVE_A_SCALE", 0x0008, 8},
		{"byte args, WE_HAVE_AN_X_AND_Y_SCALE", 0x0040, 10},
		{"byte args, WE_HAVE_A_TWO_BY_TWO", 0x0080, 14},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := componentRecordSize(tt.flags); got != tt.want {
				t.Errorf("componentRecordSize(0x%04x) = %d, want %d", tt.flags, got, tt.want)
			}
		})
	}
}

func trueTypeCmapSubtablesForTest(tables map[string][]byte) (int, bool) {
	cmap, ok := tables["cmap"]
	if !ok || len(cmap) < 4 {
		return 0, false
	}
	return int(cmap[2])<<8 | int(cmap[3]), true
}

// hheaWithNumHMetrics builds a minimal 36-byte hhea table with
// numberOfHMetrics (offset 34) set to n; the other fields are unused by
// ttRawHMetric.
func hheaWithNumHMetrics(n int) []byte {
	hhea := make([]byte, 36)
	binary.BigEndian.PutUint16(hhea[34:36], uint16(n))
	return hhea
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
