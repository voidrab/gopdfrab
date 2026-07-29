package convert

import (
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// assertSymbolicSubstitute checks that font is a conformant symbolic TrueType
// substitute whose (3,0) cmap still maps code to a real glyph meaning unicode.
func assertSymbolicSubstitute(t *testing.T, font pdf.PDFDict, code int, unicode uint16) {
	t.Helper()
	if font.Entries.Get("Subtype") != (pdf.PDFName{Value: "TrueType"}) {
		t.Fatalf("Subtype = %v, want TrueType", font.Entries.Get("Subtype"))
	}
	if font.Entries.Get("Encoding") != nil {
		t.Errorf("symbolic substitute still has an Encoding entry: %v", font.Entries.Get("Encoding"))
	}
	desc, ok := font.Entries.Get("FontDescriptor").(pdf.PDFDict)
	if !ok {
		t.Fatalf("no FontDescriptor")
	}
	if flags, _ := desc.Entries.Get("Flags").(pdf.PDFInteger); flags&4 == 0 || flags&32 != 0 {
		t.Errorf("Flags = %d, want symbolic set and non-symbolic clear", flags)
	}
	ff, ok := desc.Entries.Get("FontFile2").(pdf.PDFDict)
	if !ok || !ff.HasStream {
		t.Fatalf("no embedded FontFile2")
	}
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		t.Fatalf("decoding FontFile2: %v", err)
	}
	tables, ok := verify.ParseSfnt(data)
	if !ok {
		t.Fatalf("FontFile2 is not a valid sfnt")
	}
	if n, ok := trueTypeCmapSubtableCount(data); !ok || n != 1 {
		t.Errorf("cmap subtable count = %d, want exactly 1", n)
	}
	platform, encID, gidMap, ok := firstFormat4CmapSubtable(tables)
	if !ok {
		t.Fatalf("no format-4 cmap subtable")
	}
	if platform != 3 || encID != 0 {
		t.Errorf("cmap subtable is (%d,%d), want (3,0)", platform, encID)
	}
	gid, ok := gidMap[0xF000|uint16(code)]
	if !ok || gid == 0 {
		t.Fatalf("code %d has no glyph via 0xF0xx alias", code)
	}
	if aw := verify.TTAdvanceWidth(tables, int(gid)); aw <= 0 {
		t.Errorf("glyph for code %d has advance %d, want > 0", code, aw)
	}

	toUni, ok := font.Entries.Get("ToUnicode").(pdf.PDFDict)
	if !ok || !toUni.HasStream {
		t.Fatalf("no ToUnicode CMap on symbolic substitute")
	}
	tuData, err := pdf.DecodeStream(toUni)
	if err != nil {
		t.Fatalf("decoding ToUnicode: %v", err)
	}
	if got := parseToUnicodeCMap(tuData)[code]; got != unicode {
		t.Errorf("ToUnicode[%d] = %04X, want %04X", code, got, unicode)
	}
}

// TestFontSubstitutionSymbolicPreservesDingbats verifies that substituting a
// non-embedded ZapfDingbats font keeps the content-stream codes' meanings: the
// substitute must be a symbolic TrueType whose cmap maps the original code to
// the dingbat glyph, never a WinAnsi reinterpretation (code 52 rendering "4").
func TestFontSubstitutionSymbolicPreservesDingbats(t *testing.T) {
	desc := pdf.NewPDFDict()
	desc.Entries.Set("Flags", pdf.PDFInteger(4))

	font := pdf.NewPDFDict()
	font.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	font.Entries.Set("Subtype", pdf.PDFName{Value: "Type1"})
	font.Entries.Set("BaseFont", pdf.PDFName{Value: "ZapfDingbats"})
	font.Entries.Set("FontDescriptor", desc)
	font.Entries.Set("FirstChar", pdf.PDFInteger(52))
	font.Entries.Set("LastChar", pdf.PDFInteger(52))
	font.Entries.Set("Widths", pdf.PDFArray{pdf.PDFInteger(791)})

	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Font": font})})

	changed, err := fontSubstitutionFixer{}.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want substitution")
	}

	baseFont, _ := font.Entries.Get("BaseFont").(pdf.PDFName)
	if !strings.Contains(baseFont.Value, "NotoSansSymbols") {
		t.Errorf("BaseFont = %q, want a Noto symbol face", baseFont.Value)
	}
	assertSymbolicSubstitute(t, font, 52, 0x2714)

	// A second pass must be a no-op.
	changed, err = fontSubstitutionFixer{}.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("second Fix: %v", err)
	}
	if changed {
		t.Errorf("second Fix changed the graph; symbolic substitution is not idempotent")
	}
}

// TestConvertPreservesDingbatCheckmark converts the Isartor checkbox fixture
// (ZapfDingbats code 52, the checkmark) end-to-end and asserts the output is
// conformant while the checkmark's meaning survives in vector text.
func TestConvertPreservesDingbatCheckmark(t *testing.T) {
	path := "../../tests/Isartor/PDFA-1b/6.3 Fonts/6.3.4 Embedded font programs/isartor-6-3-4-t01-fail-f.pdf"
	cr, err := Convert(path, pdf.PDFA1B, Options{})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if !cr.Result.Valid {
		t.Fatalf("converted fixture is not conformant: %v", cr.Residual())
	}

	out, err := pdf.OpenBytes(mustOutput(t, cr))
	if err != nil {
		t.Fatalf("reopening output: %v", err)
	}
	graph, err := out.ResolveGraph()
	if err != nil {
		t.Fatalf("resolving output graph: %v", err)
	}
	var subst pdf.PDFDict
	walkDicts(graph, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if (d.Entries.Get("Type") == pdf.PDFName{Value: "Font"}) {
			if bf, _ := d.Entries.Get("BaseFont").(pdf.PDFName); strings.Contains(bf.Value, "NotoSansSymbols") {
				subst = d
			}
		}
	})
	if subst.Entries == nil {
		t.Fatalf("no Noto symbol substitute found in output; dingbats font was reinterpreted or page rasterized")
	}
	assertSymbolicSubstitute(t, subst, 52, 0x2714)
}
