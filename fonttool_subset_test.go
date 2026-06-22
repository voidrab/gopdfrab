package pdfrab

import (
	"os"
	"testing"
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
	srcTables, ok := parseSfnt(src)
	if !ok {
		t.Fatalf("parseSfnt(src) failed")
	}
	srcCmap := parseCmapFormat4(ttWindowsBMPCmap(srcTables))

	unicodes := []uint16{'A', 'B', 'C', 'a', 'b', 'c', ' '}
	out, err := subsetTrueType(src, unicodes)
	if err != nil {
		t.Fatalf("subsetTrueType: %v", err)
	}

	tables, ok := parseSfnt(out)
	if !ok {
		t.Fatalf("parseSfnt(subsetted output) failed -- not a valid sfnt")
	}

	cmap := parseCmapFormat4(ttWindowsBMPCmap(tables))
	if cmap == nil {
		t.Fatalf("subsetted output has no usable (3,1) cmap")
	}
	glyphPresent := ttGlyphPresent(tables)
	numGlyphs := ttNumGlyphs(tables)
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
		wantWidth := ttAdvanceWidth(srcTables, int(srcGID))
		gotWidth := ttAdvanceWidth(tables, int(gid))
		if wantWidth != gotWidth {
			t.Errorf("unicode %q: advance width = %d, want %d (source)", rune(u), gotWidth, wantWidth)
		}
	}

	// .notdef (gid 0) must always survive.
	if !ttGlyphInRange(tables)(0) {
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
	tables, ok := parseSfnt(out)
	if !ok {
		t.Fatalf("parseSfnt(output) failed")
	}
	cmap := parseCmapFormat4(ttWindowsBMPCmap(tables))
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
	tables, _ := parseSfnt(src)
	cmap := parseCmapFormat4(ttWindowsBMPCmap(tables))

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
	outTables, ok := parseSfnt(out)
	if !ok {
		t.Fatalf("parseSfnt(output) failed")
	}
	outCmap := parseCmapFormat4(ttWindowsBMPCmap(outTables))
	gid, ok := outCmap[compositeUnicode]
	if !ok {
		t.Fatalf("composite glyph's unicode missing from subsetted cmap")
	}
	rec := glyfRecord(outTables, int(gid))
	if !glyfIsComposite(rec) {
		t.Fatalf("subsetted glyph is no longer composite")
	}
	numGlyphs := ttNumGlyphs(outTables)
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
	srcTables, _ := parseSfnt(src)
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
	outTables, ok := parseSfnt(out)
	if !ok {
		t.Fatalf("parseSfnt(output) failed")
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
	srcCmap := parseCmapFormat4(ttWindowsBMPCmap(srcTables))
	outCmap := parseCmapFormat4(ttWindowsBMPCmap(outTables))
	for u, gid := range srcCmap {
		if outCmap[u] != gid {
			t.Errorf("unicode %q: gid changed from %d to %d after trim", rune(u), gid, outCmap[u])
		}
	}
}

func trueTypeCmapSubtablesForTest(tables map[string][]byte) (int, bool) {
	cmap, ok := tables["cmap"]
	if !ok || len(cmap) < 4 {
		return 0, false
	}
	return int(cmap[2])<<8 | int(cmap[3]), true
}
