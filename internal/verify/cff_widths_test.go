package verify

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

func TestCFFAdvanceWidths(t *testing.T) {
	widths := fontprog.CFFAdvanceWidths(buildMinimalCFF())
	if widths == nil {
		t.Fatal("CFFAdvanceWidths returned nil for a valid name-keyed CFF")
	}
	if _, ok := widths["A"]; !ok {
		t.Errorf("CFFAdvanceWidths missing glyph A: %v", widths)
	}
}

// TestCFFAdvanceWidthsFontMatrix covers a 1/2048 em program, whose charstring
// widths are 2.048 times what /Widths says.
func TestCFFAdvanceWidthsFontMatrix(t *testing.T) {
	// FontMatrix [0.00048828125 0 0 0.00048828125 0 0], the commonest
	// non-default matrix in the real-world corpus, and defaultWidthX 500.
	var matrix []byte
	for i, v := range []string{"0.00048828125", "0", "0", "0.00048828125", "0", "0"} {
		if i == 1 || i == 2 || i == 4 || i == 5 {
			matrix = append(matrix, 0x8b) // integer 0
			continue
		}
		matrix = append(matrix, cffReal(v)...)
	}
	matrix = append(matrix, 12, 7)
	private := []byte{248, 136, 20, 0x8b, 21} // defaultWidthX 500, nominalWidthX 0

	widths, stats := fontprog.CFFAdvanceWidthsStats(buildMinimalCFFWith(matrix, private))
	if stats.Skip != fontprog.WidthSkipNone {
		t.Fatalf("skip = %q, want none", stats.Skip)
	}
	if widths["A"] != 244.140625 { // 500 * 0.00048828125 * 1000, unrounded
		t.Errorf("widths[A] = %v, want 244.140625", widths["A"])
	}

	// The same program without the matrix keeps the raw glyph-space width.
	if widths, _ := fontprog.CFFAdvanceWidthsStats(buildMinimalCFFWith(nil, private)); widths["A"] != 500 {
		t.Errorf("unscaled widths[A] = %v, want 500", widths["A"])
	}
}

func TestCFFCIDAdvanceWidthsAndFDSelect(t *testing.T) {
	cff := buildMinimalCIDCFF()
	widths := fontprog.CFFCIDAdvanceWidths(cff)
	if widths[1] != 600 || widths[2] != 700 {
		t.Fatalf("CFFCIDAdvanceWidths = %v, want {1:600, 2:700, ...}", widths)
	}

	// Name-keyed (non-CID) CFF: CFFCIDAdvanceWidths must decline it.
	if fontprog.CFFCIDAdvanceWidths(buildMinimalCFF()) != nil {
		t.Error("CFFCIDAdvanceWidths should be nil for a name-keyed CFF")
	}
}

func TestCFFAdvanceWidthsRejections(t *testing.T) {
	if fontprog.CFFAdvanceWidths(buildMinimalCIDCFF()) != nil {
		t.Error("CFFAdvanceWidths should be nil for a CID-keyed CFF")
	}
	if fontprog.CFFAdvanceWidths(nil) != nil {
		t.Error("CFFAdvanceWidths should be nil for unparseable input")
	}
}

func TestCFFCIDAdvanceWidthsSingleFDFallback(t *testing.T) {
	// buildMinimalCIDCFF's FDSelect is format 0 explicit; CFFCIDAdvanceWidths
	// also has a fallback for a font with no FDSelect and exactly one FD --
	// exercise it by pointing FDSelect at an invalid offset (parseCFFFDSelect
	// returns nil) while FDArray still has exactly one entry.
	cff := buildMinimalCIDCFF()
	td, ok := fontprog.ParseCFFTopDict(cff)
	if !ok {
		t.Fatal("sanity: buildMinimalCIDCFF should parse")
	}
	broken := append([]byte{}, cff...)
	// Corrupt the FDSelect format byte to an unrecognized value.
	broken[td.FDSelect] = 0x09
	widths := fontprog.CFFCIDAdvanceWidths(broken)
	if widths == nil || widths[1] != 600 || widths[2] != 700 {
		t.Errorf("CFFCIDAdvanceWidths(single-FD fallback) = %v, want {1:600,2:700}", widths)
	}
}

// TestWidthSkipReasons drives each documented bail in the three width paths and
// checks the reason it reports, so the tally in TestWidthSkipCorpusBudget can be
// read as meaning what it says.
func TestWidthSkipReasons(t *testing.T) {
	nameKeyed, cidKeyed := buildMinimalCFF(), buildMinimalCIDCFF()

	// Corrupting a Top DICT offset operand in place: the dict still parses, but
	// the structure it points at does not.
	brokenCharset := append([]byte{}, cidKeyed...)
	td, ok := fontprog.ParseCFFTopDict(cidKeyed)
	if !ok {
		t.Fatal("sanity: buildMinimalCIDCFF should parse")
	}
	brokenCharset[td.CharsetOffset] = 0x09 // unknown charset format

	cffCases := []struct {
		name string
		cff  []byte
		want fontprog.WidthSkip
	}{
		{"name-keyed ok", nameKeyed, fontprog.WidthSkipNone},
		{"top dict unparseable", nil, fontprog.WidthSkipTopDict},
		{"cid-keyed program", cidKeyed, fontprog.WidthSkipCIDKeyed},
	}
	for _, c := range cffCases {
		t.Run(c.name, func(t *testing.T) {
			if _, stats := fontprog.CFFAdvanceWidthsStats(c.cff); stats.Skip != c.want {
				t.Errorf("CFFAdvanceWidthsStats skip = %q, want %q", stats.Skip, c.want)
			}
		})
	}

	cidCases := []struct {
		name string
		cff  []byte
		want fontprog.WidthSkip
	}{
		{"cid-keyed ok", cidKeyed, fontprog.WidthSkipNone},
		{"top dict unparseable", nil, fontprog.WidthSkipTopDict},
		{"name-keyed program", nameKeyed, fontprog.WidthSkipNotCIDKeyed},
		{"charset unparseable", brokenCharset, fontprog.WidthSkipCharset},
	}
	for _, c := range cidCases {
		t.Run("cid/"+c.name, func(t *testing.T) {
			if _, stats := fontprog.CFFCIDAdvanceWidthsStats(c.cff); stats.Skip != c.want {
				t.Errorf("CFFCIDAdvanceWidthsStats skip = %q, want %q", stats.Skip, c.want)
			}
		})
	}

	// Top DICTs that parse but omit or misplace a section the width walk needs.
	// Operands use the single-byte encoding (value = byte - 139); operator 17 is
	// CharStrings, 18 Private, 15 charset, 12 7 FontMatrix, 12 36 FDArray.
	byTopDict := []struct {
		name string
		dict []byte
		want fontprog.WidthSkip
		cid  bool
	}{
		{"font matrix", []byte{12, 7, 139, 18}, fontprog.WidthSkipFontMatrix, false},
		{"no charstrings", []byte{139, 15}, fontprog.WidthSkipCharStrings, false},
		{"no private dict", []byte{139 + 48, 17}, fontprog.WidthSkipPrivate, false},
		{"charstrings unparseable", []byte{255, 0x7f, 0, 0, 0, 17, 139, 139, 18}, fontprog.WidthSkipCharStrings, false},
		{"cid: no charstrings", cidTopDict(nil), fontprog.WidthSkipCharStrings, true},
		{"cid: no fd array", cidTopDict([]byte{139 + 48, 17}), fontprog.WidthSkipFDArray, true},
		{"cid: font matrix", cidTopDict([]byte{12, 7}), fontprog.WidthSkipFontMatrix, true},
	}
	for _, c := range byTopDict {
		t.Run(c.name, func(t *testing.T) {
			cff := buildCFFWithTopDict(c.dict)
			var got fontprog.WidthSkip
			if c.cid {
				_, stats := fontprog.CFFCIDAdvanceWidthsStats(cff)
				got = stats.Skip
			} else {
				_, stats := fontprog.CFFAdvanceWidthsStats(cff)
				got = stats.Skip
			}
			if got != c.want {
				t.Errorf("skip = %q, want %q", got, c.want)
			}
		})
	}

	// A name-keyed program whose charset yields no glyph names: point charset at
	// an unknown format so CFFGlyphNames declines it.
	noNames := append([]byte{}, nameKeyed...)
	ntd, ok := fontprog.ParseCFFTopDict(nameKeyed)
	if !ok {
		t.Fatal("sanity: buildMinimalCFF should parse")
	}
	noNames[ntd.CharsetOffset] = 0x09
	if _, stats := fontprog.CFFAdvanceWidthsStats(noNames); stats.Skip != fontprog.WidthSkipGlyphNames {
		t.Errorf("unparseable charset skip = %q, want %q", stats.Skip, fontprog.WidthSkipGlyphNames)
	}

	type1 := []struct {
		name     string
		encoding string
		widths   map[string]float64
		want     fontprog.WidthSkip
	}{
		{"ok", "WinAnsiEncoding", map[string]float64{"A": 600}, fontprog.WidthSkipNone},
		{"unmodelled encoding", "MacExpertEncoding", map[string]float64{"A": 600}, fontprog.WidthSkipEncoding},
		{"no widths", "StandardEncoding", nil, fontprog.WidthSkipNoWidths},
	}
	for _, c := range type1 {
		t.Run("type1/"+c.name, func(t *testing.T) {
			if _, stats := fontprog.Type1WidthTable(nil, pdf.PDFName{Value: c.encoding}, c.widths); stats.Skip != c.want {
				t.Errorf("Type1WidthTable skip = %q, want %q", stats.Skip, c.want)
			}
		})
	}
}

// cidTopDict returns a Top DICT marked CID-keyed (ROS, escape operator 12 30)
// followed by extra.
func cidTopDict(extra []byte) []byte {
	return append([]byte{139, 139, 139, 12, 30}, extra...)
}

// TestWidthSkipGlyphCounts checks the per-glyph half of WidthStats: a program
// that parses but holds one unfollowable charstring reports it rather than
// quietly dropping the glyph.
func TestWidthSkipGlyphCounts(t *testing.T) {
	widths, stats := fontprog.CFFAdvanceWidthsStats(buildMinimalCFF())
	if stats.Skip != fontprog.WidthSkipNone || stats.GlyphsTotal == 0 {
		t.Fatalf("stats = %+v, want no skip and a non-zero glyph count", stats)
	}
	if stats.GlyphsTotal-stats.GlyphsSkipped != len(widths) {
		t.Errorf("stats = %+v, but %d widths extracted", stats, len(widths))
	}

	// Replace glyph 1's charstring (a lone endchar at offset 55, see
	// buildMinimalCFF's layout) with an operator outside the width-prefix
	// grammar: the program is still usable, but that one glyph is given up on.
	broken := buildMinimalCFF()
	broken[55] = 0x09
	widths, stats = fontprog.CFFAdvanceWidthsStats(broken)
	if stats.Skip != fontprog.WidthSkipNone {
		t.Fatalf("one bad charstring should not skip the program: %+v", stats)
	}
	if stats.GlyphsSkipped != 1 || len(widths) != 0 {
		t.Errorf("stats = %+v, widths = %v; want 1 glyph skipped and none extracted", stats, widths)
	}

	// CID path: point one glyph's FDSelect entry at a Font DICT that does not
	// exist, so its width is given up on while the rest of the program is fine.
	cid := buildMinimalCIDCFF()
	td, ok := fontprog.ParseCFFTopDict(cid)
	if !ok {
		t.Fatal("sanity: buildMinimalCIDCFF should parse")
	}
	cid[td.FDSelect+2] = 5 // format-0 FDSelect: one byte per glyph, glyph 1 here
	cidWidths, cidStats := fontprog.CFFCIDAdvanceWidthsStats(cid)
	if cidStats.Skip != fontprog.WidthSkipNone || cidStats.GlyphsSkipped != 1 {
		t.Errorf("stats = %+v, want no skip and 1 glyph skipped", cidStats)
	}
	if _, found := cidWidths[1]; found {
		t.Errorf("CID 1 should have no width: %v", cidWidths)
	}
}

// widthSkipBudget pins how much 6.3.6 advance-width coverage the conservative
// bails give up across the committed corpora. These are measured numbers, not
// targets: a diff here means a width path started or stopped following a class
// of font program, which is worth a deliberate look before re-pinning.
// Measured 2026-08-22 over the 773 committed corpus files: of the 30 embedded
// programs that reach a width path, 1 is given up on, a Type1 program whose
// eexec section yields no charstring widths. The charstring decrypt honours
// /lenIV, so a program declaring /lenIV 0 keeps the hsbw that carries its
// width. Bare-CFF programs that declare a FontMatrix are scaled by it and
// checked.
// No corpus file hits the Type1 encoding bail, so that asymmetry with the
// Type1C path costs nothing measurable here.
var widthSkipBudget = map[string]int{
	"cidcff/none":     14,
	"type1/no-widths": 1,
	"type1/none":      6,
	"type1c/none":     9,
}

// TestWidthSkipCorpusBudget tallies every width-path skip across both
// conformance corpora. The bails are individually reasonable, but without a
// tally their cost is invisible, so this measures them instead of leaving them
// to be guessed at.
func TestWidthSkipCorpusBudget(t *testing.T) {
	got := map[string]int{}
	files := 0
	for _, p := range a1bCorpusFiles() {
		files++
		tallyWidthSkips(p, got)
	}
	if files == 0 {
		t.Skip("conformance corpora not present")
	}

	if len(widthSkipBudget) == 0 {
		// First run, or a deliberate re-pin: report the measurement.
		t.Errorf("widthSkipBudget is empty; measured tally over %d files:\n%s", files, formatTally(got))
		return
	}
	for _, key := range sortedTallyKeys(got, widthSkipBudget) {
		if got[key] != widthSkipBudget[key] {
			t.Errorf("width skip %q: %d, want %d (re-pin widthSkipBudget deliberately)\nfull tally:\n%s",
				key, got[key], widthSkipBudget[key], formatTally(got))
			return
		}
	}
}

// tallyWidthSkips opens one PDF and records, per font dictionary, which width
// path applies and what it reported. It mirrors the dispatch in
// validateFontDict, so the tally reflects what verification actually does.
func tallyWidthSkips(path string, tally map[string]int) {
	r, err := pdf.Open(path)
	if err != nil {
		return
	}
	defer r.Close()
	graph, err := r.ResolveGraph()
	if err != nil {
		return
	}

	forEachDict(graph, func(v pdf.PDFDict) {
		if v.Entries.Get("Type") != (pdf.PDFName{Value: "Font"}) {
			return
		}
		subtype, _ := v.Entries.Get("Subtype").(pdf.PDFName)
		desc, _ := v.Entries.Get("FontDescriptor").(pdf.PDFDict)
		if desc.Entries == nil {
			return
		}

		record := func(kind string, stats fontprog.WidthStats) {
			skip := string(stats.Skip)
			if skip == "" {
				skip = "none"
			}
			tally[kind+"/"+skip]++
		}

		switch subtype.Value {
		case "Type1", "MMType1":
			if ff, ok := desc.Entries.Get("FontFile").(pdf.PDFDict); ok {
				data, err := pdf.DecodeStream(ff)
				if err != nil {
					return
				}
				_, stats := fontprog.Type1WidthTable(data, v.Entries.Get("Encoding"), fontprog.Type1GlyphWidths(data))
				record("type1", stats)
			} else if ff, ok := desc.Entries.Get("FontFile3").(pdf.PDFDict); ok {
				data, err := pdf.DecodeStream(ff)
				if err != nil {
					return
				}
				_, stats := fontprog.CFFAdvanceWidthsStats(data)
				record("type1c", stats)
			}
		case "CIDFontType0":
			if ff, ok := desc.Entries.Get("FontFile3").(pdf.PDFDict); ok {
				data, err := pdf.DecodeStream(ff)
				if err != nil {
					return
				}
				_, stats := fontprog.CFFCIDAdvanceWidthsStats(data)
				record("cidcff", stats)
			}
		}
	})
}

// forEachDict visits every dictionary in a resolved graph once. The graph is
// cyclic (/Parent), so visited nodes are tracked by their Entries map identity.
func forEachDict(v pdf.PDFValue, fn func(pdf.PDFDict)) {
	visited := map[uintptr]bool{}
	var walk func(pdf.PDFValue, int)
	walk = func(node pdf.PDFValue, depth int) {
		if depth > 64 {
			return
		}
		switch t := node.(type) {
		case pdf.PDFDict:
			ptr := pdf.ValuePointer(t.Entries)
			if visited[ptr] {
				return
			}
			visited[ptr] = true
			fn(t)
			for _, e := range t.Entries.All() {
				walk(e, depth+1)
			}
		case pdf.PDFArray:
			for _, e := range t {
				walk(e, depth+1)
			}
		}
	}
	walk(v, 0)
}

// sortedTallyKeys returns the union of both maps' keys, sorted.
func sortedTallyKeys(a, b map[string]int) []string {
	seen := map[string]bool{}
	var keys []string
	for _, m := range []map[string]int{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// formatTally renders a tally as pinnable Go map literal lines.
func formatTally(t map[string]int) string {
	var b strings.Builder
	for _, k := range sortedTallyKeys(t, nil) {
		fmt.Fprintf(&b, "\t%q: %d,\n", k, t[k])
	}
	return b.String()
}
