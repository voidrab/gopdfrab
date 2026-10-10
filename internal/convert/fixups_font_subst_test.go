package convert

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/fontsubst"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// TestFontSubstitutionFixerHandlesStandardType1 verifies that a standard
// Type1 font (like Helvetica in AcroForm/DR) without a FontDescriptor gets
// a Liberation substitute embedded -- previously the fixer returned false
// because FontDescriptor was absent.
func TestFontSubstitutionFixerHandlesStandardType1(t *testing.T) {
	font := pdf.NewPDFDict()
	font.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	font.Entries.Set("Subtype", pdf.PDFName{Value: "Type1"})
	font.Entries.Set("BaseFont", pdf.PDFName{Value: "Helvetica"})
	font.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})
	// No FontDescriptor, no FirstChar/Widths -- like a standard Type1 in AcroForm/DR.

	dr := pdf.NewPDFDict()
	dr.Entries.Set("Font", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Helv": font})})

	acroForm := pdf.NewPDFDict()
	acroForm.Entries.Set("DR", dr)

	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"AcroForm": acroForm})})

	fixer := fontSubstitutionFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true (Helvetica has no embedded program)")
	}

	desc, ok := font.Entries.Get("FontDescriptor").(pdf.PDFDict)
	if !ok {
		t.Fatalf("FontDescriptor not set after substitution")
	}
	if !verify.HasEmbeddedProgram(desc, "FontFile", "FontFile2", "FontFile3") {
		t.Errorf("FontDescriptor still has no embedded program after substitution")
	}
	if (desc.Entries.Get("Type") != pdf.PDFName{Value: "FontDescriptor"}) {
		t.Errorf("FontDescriptor.Type = %v, want /FontDescriptor", desc.Entries.Get("Type"))
	}
	if _, ok := desc.Entries.Get("FontName").(pdf.PDFName); !ok {
		t.Errorf("FontDescriptor.FontName not set after substitution")
	}
}

// TestFontSubstitutionFixerLeavesNoDescriptorWhenSubstitutionSkipped covers a
// standard Type1 font with no FontDescriptor whose declared Widths are all
// zero, so substituteSimpleFont bails (no characters actually need glyphs)
// before writing anything. The fixer must not leave behind the scratch
// FontDescriptor it created to attempt substitution -- doing so previously
// planted an all-fields-missing descriptor where the PDF legitimately had
// none.
func TestFontSubstitutionFixerLeavesNoDescriptorWhenSubstitutionSkipped(t *testing.T) {
	font := pdf.NewPDFDict()
	font.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	font.Entries.Set("Subtype", pdf.PDFName{Value: "Type1"})
	font.Entries.Set("BaseFont", pdf.PDFName{Value: "Helvetica"})
	font.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})
	font.Entries.Set("FirstChar", pdf.PDFInteger(32))
	font.Entries.Set("LastChar", pdf.PDFInteger(33))
	font.Entries.Set("Widths", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0)})
	// No FontDescriptor: the fixer must create one only as scratch space.

	dr := pdf.NewPDFDict()
	dr.Entries.Set("Font", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Helv": font})})
	acroForm := pdf.NewPDFDict()
	acroForm.Entries.Set("DR", dr)
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"AcroForm": acroForm})})

	fixer := fontSubstitutionFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if changed {
		t.Errorf("changed = true, want false (all-zero Widths means no glyphs are actually used)")
	}
	if font.Entries.Get("FontDescriptor") != nil {
		t.Errorf("FontDescriptor = %v, want absent (scratch descriptor must not be left behind)", font.Entries.Get("FontDescriptor"))
	}
}

// TestFontSubstitutionFixerIdempotentAfterStandardType1 confirms that a
// second pass is a no-op once the font is already substituted.
func TestFontSubstitutionFixerIdempotentAfterStandardType1(t *testing.T) {
	font := pdf.NewPDFDict()
	font.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	font.Entries.Set("Subtype", pdf.PDFName{Value: "Type1"})
	font.Entries.Set("BaseFont", pdf.PDFName{Value: "Helvetica"})
	font.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})

	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Font": font})})

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
	desc := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "FontDescriptor"}, "Flags": pdf.PDFInteger(4),
		pdf.RefKey: pdf.PDFRef{ObjNum: 1},
	})}
	next := 42
	clone := cloneFontDescriptor(desc, &next)
	if clone.Entries.Get("Flags") != pdf.PDFInteger(4) {
		t.Error("clone did not copy Flags")
	}
	if ref, _ := clone.Entries.Get(pdf.RefKey).(pdf.PDFRef); ref.ObjNum != 42 {
		t.Errorf("clone _ref = %v, want ObjNum 42", clone.Entries.Get(pdf.RefKey))
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
		return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{}), HasStream: true, RawStream: []byte(cmap)}
	}
	validCMap := "1 beginbfchar\n<0001> <0041>\nendbfchar\n"

	cases := []struct {
		name string
		d    pdf.PDFDict
		ok   bool
	}{
		{"not Identity-H/V", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Encoding": pdf.PDFName{Value: "WinAnsiEncoding"}, "ToUnicode": toUnicodeStream(validCMap),
		})}, false},
		{"no ToUnicode", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Encoding": pdf.PDFName{Value: "Identity-H"},
		})}, false},
		{"ToUnicode not a stream", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Encoding": pdf.PDFName{Value: "Identity-H"}, "ToUnicode": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})},
		})}, false},
		{"ToUnicode parses to nothing", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Encoding": pdf.PDFName{Value: "Identity-H"}, "ToUnicode": toUnicodeStream("no bfchar or bfrange here"),
		})}, false},
		{"eligible (Identity-V)", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Encoding": pdf.PDFName{Value: "Identity-V"}, "ToUnicode": toUnicodeStream(validCMap),
		})}, true},
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
		face fontsubst.Face
		want pdf.PDFInteger
	}{
		{fontsubst.Face{}, 32},
		{fontsubst.Face{Serif: true}, 32 | 0x2},
		{fontsubst.Face{FixedPitch: true}, 32 | 0x1},
		{fontsubst.Face{Italic: true}, 32 | 0x40},
		{fontsubst.Face{Serif: true, FixedPitch: true, Italic: true}, 32 | 0x2 | 0x1 | 0x40},
	}
	for _, c := range cases {
		if got := substituteFlags(c.face); got != c.want {
			t.Errorf("substituteFlags(%+v) = %d, want %d", c.face, got, c.want)
		}
	}
}

// TestSubstituteCoversUsage covers both dispatch paths -- an explicit
// usedCodes set (from a page content scan) and the FirstChar/Widths
// fallback when no usage was recorded -- each with a covered and an
// uncovered code, using the bundled Liberation Sans face as a real cmap.
func TestSubstituteCoversUsage(t *testing.T) {
	ttf := loadLiberationSansForTest(t)
	tables, ok := fontprog.ParseSfnt(ttf)
	if !ok {
		t.Fatal("fontprog.ParseSfnt failed")
	}
	cmap := fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(tables))

	var codeToUnicode [256]uint16
	codeToUnicode['A'] = 'A'
	// code 200 deliberately left unmapped (codeToUnicode[200] == 0).

	d := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}

	usedCovered := map[uintptr]map[int]bool{pdf.ValuePointer(d.Entries): {int('A'): true}}
	if !substituteCoversUsage(d, usedCovered, codeToUnicode, cmap, tables) {
		t.Error("substituteCoversUsage(usedCodes={'A'}) = false, want true")
	}

	usedUncovered := map[uintptr]map[int]bool{pdf.ValuePointer(d.Entries): {200: true}}
	if substituteCoversUsage(d, usedUncovered, codeToUnicode, cmap, tables) {
		t.Error("substituteCoversUsage(usedCodes={200, unmapped}) = true, want false")
	}

	fallback := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"FirstChar": pdf.PDFInteger('A'), "Widths": pdf.PDFArray{pdf.PDFInteger(600)},
	})}
	if !substituteCoversUsage(fallback, nil, codeToUnicode, cmap, tables) {
		t.Error("substituteCoversUsage(Widths fallback, 'A' covered) = false, want true")
	}

	fallbackUncovered := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"FirstChar": pdf.PDFInteger(200), "Widths": pdf.PDFArray{pdf.PDFInteger(600)},
	})}
	if substituteCoversUsage(fallbackUncovered, nil, codeToUnicode, cmap, tables) {
		t.Error("substituteCoversUsage(Widths fallback, unmapped code) = true, want false")
	}
}

func TestLiberationFamilyName(t *testing.T) {
	cases := []struct {
		face fontsubst.Face
		want string
	}{
		{fontsubst.Face{}, "LiberationSans"},
		{fontsubst.Face{Bold: true}, "LiberationSans-Bold"},
		{fontsubst.Face{Italic: true}, "LiberationSans-Italic"},
		{fontsubst.Face{Bold: true, Italic: true}, "LiberationSans-BoldItalic"},
		{fontsubst.Face{Serif: true}, "LiberationSerif"},
		{fontsubst.Face{FixedPitch: true, Bold: true}, "LiberationMono-Bold"},
	}
	for _, c := range cases {
		if got := liberationFamilyName(c.face); got != c.want {
			t.Errorf("liberationFamilyName(%+v) = %q, want %q", c.face, got, c.want)
		}
	}
}

// TestTTScaledCapHeight covers the too-short-table and upm==0 fallbacks
// alongside the normal scaled-reading path.
func TestTTScaledCapHeight(t *testing.T) {
	validHead := make([]byte, 20)
	binary.BigEndian.PutUint16(validHead[18:20], 1000) // unitsPerEm

	zeroUPMHead := make([]byte, 20) // unitsPerEm left at 0

	validOS2 := make([]byte, 90)
	binary.BigEndian.PutUint16(validOS2[88:90], 700) // sCapHeight

	tests := []struct {
		name   string
		tables map[string][]byte
		want   int
	}{
		{"short OS/2", map[string][]byte{"OS/2": make([]byte, 10), "head": validHead}, -1},
		{"short head", map[string][]byte{"OS/2": validOS2, "head": make([]byte, 5)}, -1},
		{"upm zero", map[string][]byte{"OS/2": validOS2, "head": zeroUPMHead}, -1},
		{"valid", map[string][]byte{"OS/2": validOS2, "head": validHead}, 700},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const fallback = -1
			if got := ttScaledCapHeight(tt.tables, fallback); got != tt.want {
				t.Errorf("ttScaledCapHeight() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestTrueTypeCmapSubtableCount covers the ParseSfnt-failure path (garbage
// input) and the success path (a real embedded face).
func TestTrueTypeCmapSubtableCount(t *testing.T) {
	if _, ok := trueTypeCmapSubtableCount([]byte("not a font")); ok {
		t.Error("trueTypeCmapSubtableCount(garbage) ok = true, want false")
	}

	data, err := os.ReadFile("../fontsubst/fonts/LiberationSans-Regular.ttf")
	if err != nil {
		t.Skipf("bundled font not present: %v", err)
	}
	n, ok := trueTypeCmapSubtableCount(data)
	if !ok || n <= 0 {
		t.Errorf("trueTypeCmapSubtableCount(Liberation Sans) = (%d,%v), want a positive count", n, ok)
	}
}

// TestTrimSymbolicCmap covers both branches: a multi-subtable face gets
// trimmed to one, and re-running against that already-trimmed output (now
// n==1) is a no-op.
func TestTrimSymbolicCmap(t *testing.T) {
	data, err := os.ReadFile("../fontsubst/fonts/LiberationSans-Regular.ttf")
	if err != nil {
		t.Skipf("bundled font not present: %v", err)
	}
	n, ok := trueTypeCmapSubtableCount(data)
	if !ok || n < 2 {
		t.Skip("bundled face already has a single cmap subtable; nothing to trim")
	}

	desc := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"FontFile2": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{}), HasStream: true, RawStream: data},
	})}
	if !trimSymbolicCmap(desc) {
		t.Fatal("trimSymbolicCmap on a multi-subtable face = false, want true")
	}
	ff := desc.Entries.Get("FontFile2").(pdf.PDFDict)
	trimmed, err := pdf.DecodeStream(ff)
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	if got, ok := trueTypeCmapSubtableCount(trimmed); !ok || got != 1 {
		t.Errorf("trimmed subtable count = (%d,%v), want (1,true)", got, ok)
	}

	// Re-running against the already-trimmed (single-subtable) program is a no-op.
	desc2 := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"FontFile2": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{}), HasStream: true, RawStream: trimmed},
	})}
	if trimSymbolicCmap(desc2) {
		t.Error("trimSymbolicCmap on an already-single-subtable face = true, want false")
	}
}

// TestBundledFacesSupportSubsetting pins what the substitution code relies on
// instead of re-checking at run time: every face it can embed (the twelve
// Liberation styles Pick returns and the two Noto symbol faces) parses, has a
// format-4 (3,1) cmap and full head/hhea tables, so the subsetters cannot fail
// on them and their packed output parses back with a format-4 cmap.
func TestBundledFacesSupportSubsetting(t *testing.T) {
	faces := map[string][]byte{"NotoSymbols2": fontsubst.NotoSymbols2, "NotoSymbols": fontsubst.NotoSymbols}
	for _, flags := range []int{0, 0x2, 0x1} {
		for _, style := range []int{0, 0x40, 0x40000, 0x40 | 0x40000} {
			face := fontsubst.Pick(dictOf(map[string]pdf.PDFValue{"Flags": pdf.PDFInteger(flags | style)}), "")
			faces[liberationFamilyName(face)] = face.Data
		}
	}
	if len(faces) != 14 {
		t.Fatalf("collected %d distinct faces, want 12 Liberation + 2 Noto", len(faces))
	}

	for name, data := range faces {
		t.Run(name, func(t *testing.T) {
			tables, ok := fontprog.ParseSfnt(data)
			if !ok {
				t.Fatal("face does not parse")
			}
			if len(tables["head"]) < 54 || len(tables["hhea"]) < 36 || len(tables["OS/2"]) < 90 {
				t.Errorf("short head/hhea/OS-2 tables: %d/%d/%d", len(tables["head"]), len(tables["hhea"]), len(tables["OS/2"]))
			}
			if fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(tables)) == nil {
				t.Fatal("face has no format-4 (3,1) cmap")
			}

			outputs := map[string]func() ([]byte, error){
				"SubsetTrueType":         func() ([]byte, error) { return fontprog.SubsetTrueType(data, []uint16{'A'}) },
				"SubsetTrueTypeSymbolic": func() ([]byte, error) { return fontprog.SubsetTrueTypeSymbolic(data, map[int]uint16{65: 'A'}) },
				"SubsetTrueTypeForCID":   func() ([]byte, error) { return fontprog.SubsetTrueTypeForCID(data, map[uint16][]int{'A': {1}}) },
			}
			for fn, run := range outputs {
				out, err := run()
				if err != nil {
					t.Fatalf("%s: %v", fn, err)
				}
				outTables, ok := fontprog.ParseSfnt(out)
				if !ok {
					t.Fatalf("%s output does not parse", fn)
				}
				if _, _, gm, ok := fontprog.FirstFormat4CmapSubtable(outTables); !ok || gm == nil {
					t.Errorf("%s output has no format-4 cmap", fn)
				}
			}
		})
	}
}

// TestTTScaledAscentDescent covers the short-table and zero-unitsPerEm
// fallbacks alongside the scaled reading.
func TestTTScaledAscentDescent(t *testing.T) {
	head := make([]byte, 20)
	binary.BigEndian.PutUint16(head[18:20], 2000) // unitsPerEm
	hhea := make([]byte, 8)
	binary.BigEndian.PutUint16(hhea[4:6], 1600)           // ascender
	binary.BigEndian.PutUint16(hhea[6:8], uint16(0xFC18)) // descender -1000

	for name, tc := range map[string]struct {
		tables          map[string][]byte
		ascent, descent int
	}{
		"short hhea": {map[string][]byte{"hhea": make([]byte, 4), "head": head}, 0, 0},
		"short head": {map[string][]byte{"hhea": hhea, "head": make([]byte, 4)}, 0, 0},
		"zero upm":   {map[string][]byte{"hhea": hhea, "head": make([]byte, 20)}, 0, 0},
		"scaled":     {map[string][]byte{"hhea": hhea, "head": head}, 800, -500},
	} {
		if a, d := ttScaledAscentDescent(tc.tables); a != tc.ascent || d != tc.descent {
			t.Errorf("%s: ttScaledAscentDescent = %d, %d; want %d, %d", name, a, d, tc.ascent, tc.descent)
		}
	}
}
