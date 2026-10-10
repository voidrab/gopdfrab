package convert

import (
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/fontsubst"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// unembeddedHelvetica is a Type1 font with an empty descriptor and no embedded
// program, so it always needs substitution.
func unembeddedHelvetica() pdf.PDFDict {
	return dictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type1"},
		"BaseFont": pdf.PDFName{Value: "Helvetica"}, "Encoding": pdf.PDFName{Value: "WinAnsiEncoding"},
		"FontDescriptor": dictOf(nil),
	})
}

func usageOf(d pdf.PDFDict, codes ...int) map[uintptr]map[int]bool {
	m := map[int]bool{}
	for _, c := range codes {
		m[c] = true
	}
	return map[uintptr]map[int]bool{pdf.ValuePointer(d.Entries): m}
}

func TestSimpleFontUsedUnicodes(t *testing.T) {
	var table [256]uint16
	table['A'], table['B'], table['C'] = 'A', 'B', 'C'
	d := dictOf(nil)

	// Tracked usage: codes outside 0..255 are ignored.
	got := simpleFontUsedUnicodes(d, usageOf(d, 'A', 300, -1), table)
	if len(got) != 1 || got[0] != 'A' {
		t.Errorf("tracked usage = %v, want [A]", got)
	}

	// Without tracked usage the non-zero /Widths decide; B has width 0 and the
	// last code runs past 255.
	d = dictOf(map[string]pdf.PDFValue{"FirstChar": pdf.PDFInteger(65), "Widths": ints(600, 0, 600)})
	got = simpleFontUsedUnicodes(d, nil, table)
	if len(got) != 2 || got[0] != 'A' || got[1] != 'C' {
		t.Errorf("Widths fallback = %v, want [A C]", got)
	}
	d = dictOf(map[string]pdf.PDFValue{"FirstChar": pdf.PDFInteger(255), "Widths": ints(600, 600)})
	if got = simpleFontUsedUnicodes(d, nil, table); len(got) != 0 {
		t.Errorf("Widths beyond code 255 = %v, want none", got)
	}
}

func TestSimpleFontNeedsSubstitutionKeepsConformingProgram(t *testing.T) {
	ttf := streamOf(fontsubst.LiberationSansRegular)
	font := func(baseFont string, extra map[string]pdf.PDFValue) (pdf.PDFDict, pdf.PDFDict) {
		desc := dictOf(map[string]pdf.PDFValue{"FontFile2": ttf})
		d := dictOf(map[string]pdf.PDFValue{
			"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "TrueType"},
			"BaseFont": pdf.PDFName{Value: baseFont}, "FontDescriptor": desc,
		})
		for k, v := range extra {
			d.Entries.Set(k, v)
		}
		return d, desc
	}
	widths := map[string]pdf.PDFValue{"FirstChar": pdf.PDFInteger(65), "LastChar": pdf.PDFInteger(65), "Widths": ints(667)}

	d, desc := font("Foo", nil) // no FirstChar/LastChar/Widths to inspect further
	if simpleFontNeedsSubstitution(d, desc, nil) {
		t.Error("program-bearing font without widths needs substitution, want it kept")
	}
	d, desc = font("Foo", widths) // not subset-tagged: coverage is not checked
	if simpleFontNeedsSubstitution(d, desc, nil) {
		t.Error("full-embedded font needs substitution, want it kept")
	}
}

func TestSubstituteCoversUsageEdges(t *testing.T) {
	tables, _ := fontprog.ParseSfnt(fontsubst.LiberationSansRegular)
	cmap := fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(tables))
	var table [256]uint16
	table['A'] = 'A'
	table[0x80] = 0x0378 // an unassigned code point: no glyph in the face
	d := dictOf(nil)

	// Codes outside 0..255 cannot be rendered by a simple font and never block.
	if !substituteCoversUsage(d, usageOf(d, 300, -1), table, cmap, tables) {
		t.Error("out-of-range codes blocked substitution")
	}
	if substituteCoversUsage(d, usageOf(d, 0x80), table, cmap, tables) {
		t.Error("code whose unicode the face lacks was reported covered")
	}
}

func TestSubstituteSimpleFontDeclines(t *testing.T) {
	next := 100
	if substituteSimpleFont(dictOf(map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Type1"}}), nil, nil, &next) {
		t.Error("substituted a font with no descriptor")
	}

	// 0x81 is undefined in WinAnsi, so the substitute could never draw it.
	d := unembeddedHelvetica()
	if substituteSimpleFont(d, usageOf(d, 'A', 0x81), nil, &next) {
		t.Error("substituted although a used code has no glyph in the substitute")
	}
	if d.Entries.Get("Subtype") != (pdf.PDFName{Value: "Type1"}) {
		t.Error("declined substitution still modified the font")
	}
}

func TestSubstituteSimpleFontClonesSharedDescriptor(t *testing.T) {
	d := unembeddedHelvetica()
	shared := d.Entries.Get("FontDescriptor").(pdf.PDFDict)
	next := 100
	sharedDescs := map[uintptr]bool{pdf.ValuePointer(shared.Entries): true}
	if !substituteSimpleFont(d, usageOf(d, 'A'), sharedDescs, &next) {
		t.Fatal("substituteSimpleFont = false, want true")
	}
	own := d.Entries.Get("FontDescriptor").(pdf.PDFDict)
	if pdf.ValuePointer(own.Entries) == pdf.ValuePointer(shared.Entries) {
		t.Error("shared descriptor was rewritten in place")
	}
	if shared.Entries.Get("FontFile2") != nil {
		t.Error("the other fonts' descriptor gained a program")
	}
	if own.Entries.Get("FontFile2") == nil {
		t.Error("clone has no substitute program")
	}
	if ref, _ := own.Entries.Get(pdf.RefKey).(pdf.PDFRef); ref.ObjNum != 100 || next != 101 {
		t.Errorf("clone ref = %v, next = %d; want object 100, next 101", own.Entries.Get(pdf.RefKey), next)
	}
}

func TestSubstituteSimpleFontSymbolicDeclines(t *testing.T) {
	var a, nowhere [256]uint16
	a['A'] = 'A'
	nowhere['A'] = 0x0378 // unassigned: no bundled face has it
	next := 100
	font := func() pdf.PDFDict {
		d := unembeddedHelvetica()
		d.Entries.Del("Encoding")
		return d
	}

	d := dictOf(nil)
	if substituteSimpleFontSymbolic(d, nil, a, true, nil, &next) {
		t.Error("substituted a font with no descriptor")
	}
	d = font()
	if substituteSimpleFontSymbolic(d, usageOf(d, 'A', 'B'), a, false, nil, &next) {
		t.Error("substituted although code B means something unknown")
	}
	d = font()
	if substituteSimpleFontSymbolic(d, usageOf(d, 'B'), a, true, nil, &next) {
		t.Error("substituted with no code that has a meaning")
	}
	d = font()
	if substituteSimpleFontSymbolic(d, usageOf(d, 'A'), nowhere, true, nil, &next) {
		t.Error("substituted although no bundled face has the glyph")
	}
	if d.Entries.Get("Subtype") != (pdf.PDFName{Value: "Type1"}) {
		t.Error("declined substitution still modified the font")
	}

	// A shared descriptor is cloned rather than rewritten under its other users.
	d = font()
	shared := d.Entries.Get("FontDescriptor").(pdf.PDFDict)
	sharedDescs := map[uintptr]bool{pdf.ValuePointer(shared.Entries): true}
	if !substituteSimpleFontSymbolic(d, usageOf(d, 'A'), a, true, sharedDescs, &next) {
		t.Fatal("substituteSimpleFontSymbolic = false, want true")
	}
	own := d.Entries.Get("FontDescriptor").(pdf.PDFDict)
	if pdf.ValuePointer(own.Entries) == pdf.ValuePointer(shared.Entries) || shared.Entries.Get("FontFile2") != nil {
		t.Error("shared descriptor was rewritten in place")
	}
	assertSymbolicSubstitute(t, d, 'A', 'A')
}

// type0WithToUnicode builds a Type0 font and its (unembedded) CIDFontType2
// descendant whose Identity-H CIDs map to the given Unicode values.
func type0WithToUnicode(w pdf.PDFValue, cidToUnicode map[int]uint16) (type0, cid pdf.PDFDict) {
	var cmap strings.Builder
	cmap.WriteString("beginbfchar\n")
	for c, u := range cidToUnicode {
		cmap.WriteString("<" + hex4(c) + "> <" + hex4(int(u)) + ">\n")
	}
	cmap.WriteString("endbfchar\n")
	cid = dictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "CIDFontType2"},
		"BaseFont": pdf.PDFName{Value: "Foo"}, "FontDescriptor": dictOf(nil),
	})
	if w != nil {
		cid.Entries.Set("W", w)
	}
	type0 = dictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type0"},
		"Encoding": pdf.PDFName{Value: "Identity-H"}, "ToUnicode": streamOf([]byte(cmap.String())),
		"DescendantFonts": pdf.PDFArray{cid},
	})
	return type0, cid
}

func hex4(v int) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{digits[v>>12&15], digits[v>>8&15], digits[v>>4&15], digits[v&15]})
}

func TestSubstituteCIDFont(t *testing.T) {
	next := 100
	w := pdf.PDFArray{pdf.PDFInteger(0), ints(500, 600)}

	// Without tracked usage the CIDs come from /W; CID 0 (.notdef) needs no
	// mapping.
	type0, cid := type0WithToUnicode(w, map[int]uint16{1: 'A'})
	if !substituteCIDFont(type0, cid, nil, nil, &next) {
		t.Fatal("substituteCIDFont = false, want true")
	}
	if name := cid.Entries.Get("BaseFont").(pdf.PDFName).Value; !strings.HasSuffix(name, "+LiberationSans") {
		t.Errorf("BaseFont = %q, want a tagged LiberationSans substitute", name)
	}
	if cid.Entries.Get("Subtype") != (pdf.PDFName{Value: "CIDFontType2"}) || cid.Entries.Get("CIDToGIDMap") != (pdf.PDFName{Value: "Identity"}) {
		t.Error("descendant is not an Identity-mapped CIDFontType2")
	}

	// A glyph Liberation lacks falls back to the Noto symbol repertoire.
	type0, cid = type0WithToUnicode(w, map[int]uint16{1: 0x2701})
	if !substituteCIDFont(type0, cid, nil, nil, &next) {
		t.Fatal("substituteCIDFont (symbol) = false, want true")
	}
	if name := cid.Entries.Get("BaseFont").(pdf.PDFName).Value; !strings.Contains(name, "+NotoSansSymbols") {
		t.Errorf("BaseFont = %q, want a Noto symbol substitute", name)
	}

	// A shared descriptor is cloned rather than rewritten under its other users.
	type0, cid = type0WithToUnicode(w, map[int]uint16{1: 'A'})
	shared := cid.Entries.Get("FontDescriptor").(pdf.PDFDict)
	sharedDescs := map[uintptr]bool{pdf.ValuePointer(shared.Entries): true}
	if !substituteCIDFont(type0, cid, usageOf(cid, 1), sharedDescs, &next) {
		t.Fatal("substituteCIDFont (shared) = false, want true")
	}
	own := cid.Entries.Get("FontDescriptor").(pdf.PDFDict)
	if pdf.ValuePointer(own.Entries) == pdf.ValuePointer(shared.Entries) || shared.Entries.Get("FontFile2") != nil {
		t.Error("shared descriptor was rewritten in place")
	}
}

func TestSubstituteCIDFontDeclines(t *testing.T) {
	next := 100
	w := pdf.PDFArray{pdf.PDFInteger(0), ints(500, 600)}

	type0, cid := type0WithToUnicode(w, map[int]uint16{1: 'A'})
	cid.Entries.Del("FontDescriptor")
	if substituteCIDFont(type0, cid, nil, nil, &next) {
		t.Error("substituted a descendant with no descriptor")
	}

	// A full (not subset-tagged) program that conforms is kept.
	type0, cid = type0WithToUnicode(w, map[int]uint16{1: 'A'})
	cid.Entries.Set("FontDescriptor", dictOf(map[string]pdf.PDFValue{"FontFile2": streamOf(fontsubst.LiberationSansRegular)}))
	if substituteCIDFont(type0, cid, nil, nil, &next) {
		t.Error("substituted a conforming full program")
	}

	// A CID with no recoverable Unicode meaning cannot be substituted.
	type0, cid = type0WithToUnicode(w, map[int]uint16{2: 'A'})
	if substituteCIDFont(type0, cid, nil, nil, &next) {
		t.Error("substituted although CID 1 has no Unicode value")
	}

	// Only .notdef is rendered: nothing to substitute.
	type0, cid = type0WithToUnicode(pdf.PDFArray{pdf.PDFInteger(0), ints(500)}, map[int]uint16{1: 'A'})
	if substituteCIDFont(type0, cid, nil, nil, &next) {
		t.Error("substituted a font that renders only .notdef")
	}

	// No bundled face has the glyph.
	type0, cid = type0WithToUnicode(w, map[int]uint16{1: 0x0378})
	if substituteCIDFont(type0, cid, nil, nil, &next) {
		t.Error("substituted although no bundled face has the glyph")
	}
}

func TestCIDFontSubstitutionEligibleUndecodableToUnicode(t *testing.T) {
	d := dictOf(map[string]pdf.PDFValue{"Encoding": pdf.PDFName{Value: "Identity-H"}, "ToUnicode": undecodableStream()})
	if _, ok := cidFontSubstitutionEligible(d); ok {
		t.Error("undecodable ToUnicode reported eligible")
	}
}

func TestFontSubstitutionFixerSharedDescriptorAndNoFonts(t *testing.T) {
	// A document with no fonts has nothing to do.
	trailer := trailerWith("Pages", dictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Pages"}}))
	if changed, err := (fontSubstitutionFixer{}).Fix(&trailer, nil); changed || err != nil {
		t.Errorf("Fix without fonts = %v, %v; want false, nil", changed, err)
	}

	// Two fonts reference one descriptor dict: each gets its own copy.
	a, b := unembeddedHelvetica(), unembeddedHelvetica()
	shared := dictOf(nil)
	shared.Entries.Set(pdf.RefKey, pdf.PDFRef{ObjNum: 7})
	a.Entries.Set("FontDescriptor", shared)
	b.Entries.Set("FontDescriptor", shared)
	trailer = trailerWith("Fonts", pdf.PDFArray{a, b})
	changed, err := (fontSubstitutionFixer{}).Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("Fix = %v, %v; want changed", changed, err)
	}
	da, db := a.Entries.Get("FontDescriptor").(pdf.PDFDict), b.Entries.Get("FontDescriptor").(pdf.PDFDict)
	if pdf.ValuePointer(da.Entries) == pdf.ValuePointer(db.Entries) {
		t.Error("both fonts still share one descriptor")
	}
}

// symbolicTrueType is a TrueType font flagged symbolic whose descriptor embeds
// program (nil for none).
func symbolicTrueType(program *pdf.PDFDict) pdf.PDFDict {
	desc := dictOf(map[string]pdf.PDFValue{"Flags": pdf.PDFInteger(4)})
	if program != nil {
		desc.Entries.Set("FontFile2", *program)
	}
	return dictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "TrueType"},
		"FontDescriptor": desc,
	})
}

func TestTrueTypeEncodingFixerSkipsAndLeavesUnrepairable(t *testing.T) {
	// A cmap holding two subtables, neither of them format 4.
	cmap := []byte{0, 0, 0, 2, 0, 3, 0, 1, 0, 0, 0, 20, 0, 1, 0, 0, 0, 0, 0, 20, 0, 6, 0, 0, 0, 0, 0, 0}
	twoNonFormat4 := streamOf(wrapInSfnt(0x00010000, map[string][]byte{"cmap": cmap}))
	tinyCmap := streamOf(wrapInSfnt(0x00010000, map[string][]byte{"cmap": {0, 0}}))
	undecodable := undecodableStream()

	fonts := pdf.PDFArray{
		dictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Page"}}),
		dictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type1"}}),
		dictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "TrueType"}}),
		symbolicTrueType(nil),
		symbolicTrueType(&undecodable),
		symbolicTrueType(&twoNonFormat4),
		symbolicTrueType(&tinyCmap),
	}
	trailer := trailerWith("Fonts", fonts)
	changed, err := (trueTypeEncodingFixer{}).Fix(&trailer, nil)
	if err != nil || changed {
		t.Errorf("Fix = %v, %v; want unchanged", changed, err)
	}
}

func TestTrueTypeCmapSubtableCountTinyCmap(t *testing.T) {
	if _, ok := trueTypeCmapSubtableCount(wrapInSfnt(0x00010000, map[string][]byte{"cmap": {0, 0}})); ok {
		t.Error("cmap shorter than its header reported a count")
	}
}
