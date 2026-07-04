package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// TestFontSubstitutionFixerHandlesStandardType1 verifies that a standard
// Type1 font (like Helvetica in AcroForm/DR) without a FontDescriptor gets
// a Liberation substitute embedded -- previously the fixer returned false
// because FontDescriptor was absent.
func TestFontSubstitutionFixerHandlesStandardType1(t *testing.T) {
	font := pdf.NewPDFDict()
	font.Entries["Type"] = pdf.PDFName{Value: "Font"}
	font.Entries["Subtype"] = pdf.PDFName{Value: "Type1"}
	font.Entries["BaseFont"] = pdf.PDFName{Value: "Helvetica"}
	font.Entries["Encoding"] = pdf.PDFName{Value: "WinAnsiEncoding"}
	// No FontDescriptor, no FirstChar/Widths -- like a standard Type1 in AcroForm/DR.

	dr := pdf.NewPDFDict()
	dr.Entries["Font"] = pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Helv": font}}

	acroForm := pdf.NewPDFDict()
	acroForm.Entries["DR"] = dr

	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = pdf.PDFDict{Entries: map[string]pdf.PDFValue{"AcroForm": acroForm}}

	fixer := fontSubstitutionFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true (Helvetica has no embedded program)")
	}

	desc, ok := font.Entries["FontDescriptor"].(pdf.PDFDict)
	if !ok {
		t.Fatalf("FontDescriptor not set after substitution")
	}
	if !verify.HasEmbeddedProgram(desc, "FontFile", "FontFile2", "FontFile3") {
		t.Errorf("FontDescriptor still has no embedded program after substitution")
	}
}

// TestFontSubstitutionFixerIdempotentAfterStandardType1 confirms that a
// second pass is a no-op once the font is already substituted.
func TestFontSubstitutionFixerIdempotentAfterStandardType1(t *testing.T) {
	font := pdf.NewPDFDict()
	font.Entries["Type"] = pdf.PDFName{Value: "Font"}
	font.Entries["Subtype"] = pdf.PDFName{Value: "Type1"}
	font.Entries["BaseFont"] = pdf.PDFName{Value: "Helvetica"}
	font.Entries["Encoding"] = pdf.PDFName{Value: "WinAnsiEncoding"}

	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Font": font}}

	fixer := fontSubstitutionFixer{}
	if _, err := fixer.Fix(&trailer, nil); err != nil {
		t.Fatalf("first Fix: %v", err)
	}

	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("second Fix: %v", err)
	}
	if changed {
		t.Errorf("changed = true on second pass, want false (fixer must be idempotent)")
	}
}

func TestCloneFontDescriptor(t *testing.T) {
	desc := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "FontDescriptor"}, "Flags": pdf.PDFInteger(4),
		"_ref": pdf.PDFRef{ObjNum: 1}, "_dirty": pdf.PDFBoolean(true),
	}}
	next := 42
	clone := cloneFontDescriptor(desc, &next)
	if clone.Entries["Flags"] != pdf.PDFInteger(4) {
		t.Error("clone did not copy Flags")
	}
	if ref, _ := clone.Entries["_ref"].(pdf.PDFRef); ref.ObjNum != 42 {
		t.Errorf("clone _ref = %v, want ObjNum 42", clone.Entries["_ref"])
	}
	if _, ok := clone.Entries["_dirty"]; ok {
		t.Error("clone should not carry _dirty")
	}
	if next != 43 {
		t.Errorf("nextObjNum = %d, want 43", next)
	}
}

// TestCIDFontSubstitutionEligible covers every rejection branch (wrong
// encoding, missing/empty ToUnicode, undecodable ToUnicode, an empty parsed
// CMap) plus the eligible case, direct-unit-testing a helper otherwise only
// exercised indirectly via corpus fixtures (see cidSubstitutionPossible,
// convert_test.go).
func TestCIDFontSubstitutionEligible(t *testing.T) {
	toUnicodeStream := func(cmap string) pdf.PDFDict {
		return pdf.PDFDict{Entries: map[string]pdf.PDFValue{}, HasStream: true, RawStream: []byte(cmap)}
	}
	validCMap := "1 beginbfchar\n<0001> <0041>\nendbfchar\n"

	cases := []struct {
		name string
		d    pdf.PDFDict
		ok   bool
	}{
		{"not Identity-H/V", pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Encoding": pdf.PDFName{Value: "WinAnsiEncoding"}, "ToUnicode": toUnicodeStream(validCMap),
		}}, false},
		{"no ToUnicode", pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Encoding": pdf.PDFName{Value: "Identity-H"},
		}}, false},
		{"ToUnicode not a stream", pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Encoding": pdf.PDFName{Value: "Identity-H"}, "ToUnicode": pdf.PDFDict{Entries: map[string]pdf.PDFValue{}},
		}}, false},
		{"ToUnicode parses to nothing", pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Encoding": pdf.PDFName{Value: "Identity-H"}, "ToUnicode": toUnicodeStream("no bfchar or bfrange here"),
		}}, false},
		{"eligible (Identity-V)", pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Encoding": pdf.PDFName{Value: "Identity-V"}, "ToUnicode": toUnicodeStream(validCMap),
		}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := cidFontSubstitutionEligible(c.d)
			if ok != c.ok {
				t.Fatalf("cidFontSubstitutionEligible ok = %v, want %v", ok, c.ok)
			}
			if ok && got[1] != 0x0041 {
				t.Errorf("cidFontSubstitutionEligible map = %v, want [1]=0x0041", got)
			}
		})
	}
}

// TestHexToUnicode covers the direct-value and too-short-to-decode paths.
func TestHexToUnicode(t *testing.T) {
	if got, ok := hexToUnicode("0041"); !ok || got != 0x0041 {
		t.Errorf("hexToUnicode(0041) = (%04X, %v), want (0041, true)", got, ok)
	}
	if _, ok := hexToUnicode("41"); ok {
		t.Error("hexToUnicode(41) (single byte) ok = true, want false")
	}
}

// TestParseToUnicodeCMap covers all three bfchar/bfrange forms the parser
// supports (single bfchar entries are already exercised via
// assertSymbolicSubstitute): a bfrange with a single hex base (incrementing
// per code), a bfrange with a destination array (one hex value per code),
// and the malformed entries that must be skipped rather than panic.
func TestParseToUnicodeCMap(t *testing.T) {
	cmap := []byte(`
1 beginbfchar
<41> <0041>
endbfchar
2 beginbfrange
<42> <44> <0042>
<61> <63> [<0061> <0062> <0063>]
<FF> <10> <2000>
endbfrange
`)
	got := parseToUnicodeCMap(cmap)

	want := map[int]uint16{
		0x41: 0x0041, // bfchar
		0x42: 0x0042, // bfrange, base hex
		0x43: 0x0043,
		0x44: 0x0044,
		0x61: 0x0061, // bfrange, destination array
		0x62: 0x0062,
		0x63: 0x0063,
	}
	for code, u := range want {
		if got[code] != u {
			t.Errorf("parseToUnicodeCMap[%02X] = %04X, want %04X", code, got[code], u)
		}
	}
	// <FF> <10> has hi < lo and must be skipped, not produce entries.
	if _, ok := got[0xFF]; ok {
		t.Errorf("parseToUnicodeCMap produced an entry for a hi<lo range: %v", got)
	}
}

func TestStemVFor(t *testing.T) {
	if got := stemVFor(false); got != 80 {
		t.Errorf("stemVFor(false) = %d, want 80", got)
	}
	if got := stemVFor(true); got != 120 {
		t.Errorf("stemVFor(true) = %d, want 120", got)
	}
}

func TestSubstituteFlags(t *testing.T) {
	cases := []struct {
		face liberationFace
		want pdf.PDFInteger
	}{
		{liberationFace{}, 32},
		{liberationFace{serif: true}, 32 | 0x2},
		{liberationFace{fixedPitch: true}, 32 | 0x1},
		{liberationFace{italic: true}, 32 | 0x40},
		{liberationFace{serif: true, fixedPitch: true, italic: true}, 32 | 0x2 | 0x1 | 0x40},
	}
	for _, c := range cases {
		if got := substituteFlags(c.face); got != c.want {
			t.Errorf("substituteFlags(%+v) = %d, want %d", c.face, got, c.want)
		}
	}
}

// TestPickLiberationFace covers pickLiberationFace's serif/fixed-pitch/
// italic/bold detection, both via FontDescriptor /Flags and /FontWeight and
// via BaseFont name heuristics.
func TestPickLiberationFace(t *testing.T) {
	flagsDict := func(flags int) pdf.PDFDict {
		return pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Flags": pdf.PDFInteger(flags)}}
	}
	empty := pdf.PDFDict{Entries: map[string]pdf.PDFValue{}}

	cases := []struct {
		name                            string
		desc                            pdf.PDFDict
		baseFont                        string
		serif, fixedPitch, italic, bold bool
	}{
		{"plain", empty, "Arial", false, false, false, false},
		{"serif flag", flagsDict(0x2), "Foo", true, false, false, false},
		{"serif name", empty, "Times New Roman", true, false, false, false},
		{"fixed flag", flagsDict(0x1), "Foo", false, true, false, false},
		{"fixed name", empty, "Courier", false, true, false, false},
		{"italic flag", flagsDict(0x40), "Foo", false, false, true, false},
		{"italic name", empty, "Foo-Italic", false, false, true, false},
		{"bold flag", flagsDict(0x40000), "Foo", false, false, false, true},
		{"bold name", empty, "Foo-Bold", false, false, false, true},
		{"bold weight", pdf.PDFDict{Entries: map[string]pdf.PDFValue{"FontWeight": pdf.PDFInteger(700)}}, "Foo", false, false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pickLiberationFace(c.desc, c.baseFont)
			if got.serif != c.serif || got.fixedPitch != c.fixedPitch || got.italic != c.italic || got.bold != c.bold {
				t.Errorf("pickLiberationFace(%v, %q) = %+v, want serif=%v fixedPitch=%v italic=%v bold=%v",
					c.desc.Entries, c.baseFont, got, c.serif, c.fixedPitch, c.italic, c.bold)
			}
		})
	}
}

// TestSubstituteCoversUsage covers both dispatch paths -- an explicit
// usedCodes set (from a page content scan) and the FirstChar/Widths
// fallback when no usage was recorded -- each with a covered and an
// uncovered code, using the bundled Liberation Sans face as a real cmap.
func TestSubstituteCoversUsage(t *testing.T) {
	ttf := loadLiberationSansForTest(t)
	tables, ok := verify.ParseSfnt(ttf)
	if !ok {
		t.Fatal("verify.ParseSfnt failed")
	}
	cmap := verify.ParseCmapFormat4(verify.TTWindowsBMPCmap(tables))

	var codeToUnicode [256]uint16
	codeToUnicode['A'] = 'A'
	// code 200 deliberately left unmapped (codeToUnicode[200] == 0).

	d := pdf.PDFDict{Entries: map[string]pdf.PDFValue{}}

	usedCovered := map[uintptr]map[int]bool{pdf.ValuePointer(d.Entries): {int('A'): true}}
	if !substituteCoversUsage(d, usedCovered, codeToUnicode, cmap, tables) {
		t.Error("substituteCoversUsage(usedCodes={'A'}) = false, want true")
	}

	usedUncovered := map[uintptr]map[int]bool{pdf.ValuePointer(d.Entries): {200: true}}
	if substituteCoversUsage(d, usedUncovered, codeToUnicode, cmap, tables) {
		t.Error("substituteCoversUsage(usedCodes={200, unmapped}) = true, want false")
	}

	fallback := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"FirstChar": pdf.PDFInteger('A'), "Widths": pdf.PDFArray{pdf.PDFInteger(600)},
	}}
	if !substituteCoversUsage(fallback, nil, codeToUnicode, cmap, tables) {
		t.Error("substituteCoversUsage(Widths fallback, 'A' covered) = false, want true")
	}

	fallbackUncovered := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"FirstChar": pdf.PDFInteger(200), "Widths": pdf.PDFArray{pdf.PDFInteger(600)},
	}}
	if substituteCoversUsage(fallbackUncovered, nil, codeToUnicode, cmap, tables) {
		t.Error("substituteCoversUsage(Widths fallback, unmapped code) = true, want false")
	}
}

func TestLiberationFamilyName(t *testing.T) {
	cases := []struct {
		face liberationFace
		want string
	}{
		{liberationFace{}, "LiberationSans"},
		{liberationFace{bold: true}, "LiberationSans-Bold"},
		{liberationFace{italic: true}, "LiberationSans-Italic"},
		{liberationFace{bold: true, italic: true}, "LiberationSans-BoldItalic"},
		{liberationFace{serif: true}, "LiberationSerif"},
		{liberationFace{fixedPitch: true, bold: true}, "LiberationMono-Bold"},
	}
	for _, c := range cases {
		if got := liberationFamilyName(c.face); got != c.want {
			t.Errorf("liberationFamilyName(%+v) = %q, want %q", c.face, got, c.want)
		}
	}
}
