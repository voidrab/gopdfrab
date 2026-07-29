package convert

import (
	"strconv"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"

	"github.com/voidrab/gopdfrab/internal/verify"
)

// TestFontDictFixerAddsIdentityCIDToGIDMap builds a minimal trailer with a
// Type0 font whose CIDFontType2 descendant lacks /CIDToGIDMap, runs
// fontDictFixer.Fix, and checks that /CIDToGIDMap /Identity is added -- and
// that a second pass is a no-op, since the fixer must be idempotent for the
// bounded convert loop's progress detection (sameMultiset, convert.go) to
// terminate.
func TestFontDictFixerAddsIdentityCIDToGIDMap(t *testing.T) {
	cidFont := pdf.NewPDFDict()
	cidFont.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	cidFont.Entries.Set("Subtype", pdf.PDFName{Value: "CIDFontType2"})
	cidFont.Entries.Set("BaseFont", pdf.PDFName{Value: "Test"})

	type0 := pdf.NewPDFDict()
	type0.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	type0.Entries.Set("Subtype", pdf.PDFName{Value: "Type0"})
	type0.Entries.Set("DescendantFonts", pdf.PDFArray{cidFont})

	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Font": type0})})

	fixer := fontDictFixer{}

	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true (CIDToGIDMap was missing)")
	}
	got, ok := cidFont.Entries.Get("CIDToGIDMap").(pdf.PDFName)
	if !ok || got.Value != "Identity" {
		t.Fatalf("CIDToGIDMap = %#v, want pdf.PDFName{Identity}", cidFont.Entries.Get("CIDToGIDMap"))
	}

	changed, err = fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix (second pass): %v", err)
	}
	if changed {
		t.Errorf("changed = true on second pass, want false (fixer must be idempotent)")
	}
}

// TestFontDictFixerAppliesOnlyToCIDToGIDMapMissing checks the Fixer/Applies
// contract (mirroring the registration pattern in fixups_dict.go): the
// fixer must claim exactly Checks.Font.CIDToGIDMapMissing and nothing else,
// since registerFixer panics on a Check claimed by more than one Fixer.
func TestFontDictFixerAppliesOnlyToCIDToGIDMapMissing(t *testing.T) {
	fixer := fontDictFixer{}
	for _, c := range pdf.AllChecks() {
		want := c == pdf.Checks.Font.CIDToGIDMapMissing
		if got := fixer.Applies(c); got != want {
			t.Errorf("Applies(%s) = %v, want %v", c.Name(), got, want)
		}
	}
}

func TestType0FontFixerCIDSystemInfo(t *testing.T) {
	cid := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "CIDFontType2"},
		"CIDSystemInfo": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Registry": pdf.PDFString{Value: "Adobe"}, "Ordering": pdf.PDFString{Value: "Japan1"},
			"Supplement": pdf.PDFInteger(0),
		})},
	})}
	cmap := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "CMap"},
		"CIDSystemInfo": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Registry": pdf.PDFString{Value: "Adobe"}, "Ordering": pdf.PDFString{Value: "Identity"},
			"Supplement": pdf.PDFInteger(0),
		})},
	})}
	font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type0"},
		"Encoding": cmap, "DescendantFonts": pdf.PDFArray{cid},
	})}
	trailer := trailerWith("F1", font)
	changed, err := type0FontFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("type0FontFixer.Fix = %v, %v", changed, err)
	}
	got := cmap.Entries.Get("CIDSystemInfo").(pdf.PDFDict).Entries.Get("Ordering")
	if got != (pdf.PDFString{Value: "Japan1"}) {
		t.Errorf("CMap CIDSystemInfo Ordering = %v, want it copied from the CIDFont (Japan1)", got)
	}
}

func TestBaseFontFixerAppliesOnlyToFontBaseFont(t *testing.T) {
	fixer := baseFontFixer{}
	for _, c := range pdf.AllChecks() {
		want := c == pdf.Checks.Font.FontBaseFont
		if got := fixer.Applies(c); got != want {
			t.Errorf("Applies(%s) = %v, want %v", c.Name(), got, want)
		}
	}
}

// fontMissingBaseFont builds a trailer holding one font of the given subtype
// with no BaseFont, whose descriptor is desc (nil for no descriptor at all).
func fontMissingBaseFont(subtype string, desc *pdf.PDFDict) pdf.PDFDict {
	font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":    pdf.PDFName{Value: "Font"},
		"Subtype": pdf.PDFName{Value: subtype},
	})}
	if desc != nil {
		font.Entries.Set("FontDescriptor", *desc)
	}
	return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Font": font})}
}

// baseFontOf returns the BaseFont of the single font in a trailer built by
// fontMissingBaseFont.
func baseFontOf(t *testing.T, trailer pdf.PDFDict) pdf.PDFValue {
	t.Helper()
	font, ok := trailer.Entries.Get("Font").(pdf.PDFDict)
	if !ok {
		t.Fatalf("trailer has no font")
	}
	return font.Entries.Get("BaseFont")
}

// descriptorWithProgram builds a font descriptor embedding program under key.
func descriptorWithProgram(key string, program []byte) pdf.PDFDict {
	ff := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{}), HasStream: true, RawStream: program}
	return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{key: ff})}
}

// TestBaseFontFixerTakesDescriptorName covers the first source: the descriptor
// already records what the font is called.
func TestBaseFontFixerTakesDescriptorName(t *testing.T) {
	desc := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"FontName": pdf.PDFName{Value: "ABCDEF+MyFace"},
	})}
	trailer := fontMissingBaseFont("Type1", &desc)
	runFixerAndCheckIdempotent(t, baseFontFixer{}, &trailer)

	if got := baseFontOf(t, trailer); (got != pdf.PDFName{Value: "ABCDEF+MyFace"}) {
		t.Errorf("BaseFont = %v, want /ABCDEF+MyFace", got)
	}
}

// TestBaseFontFixerReadsEmbeddedProgram covers the second source: the program
// itself names the font, in each of the three embedding formats.
func TestBaseFontFixerReadsEmbeddedProgram(t *testing.T) {
	otf := wrapInSfnt(0x4F54544F, map[string][]byte{"CFF ": buildMinimalCFF()})

	for _, tc := range []struct {
		name    string
		key     string
		program []byte
		want    string
	}{
		{"bare CFF name index", "FontFile3", buildMinimalCFF(), "Font"},
		{"OpenType-wrapped CFF", "FontFile3", otf, "Font"},
		{"Type 1 clear-text header", "FontFile",
			[]byte("%!PS-AdobeFont-1.0\n/FontName /MyType1Face def\n"), "MyType1Face"},
		{"TrueType name table", "FontFile2", loadLiberationSansForTest(t), "LiberationSans"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			desc := descriptorWithProgram(tc.key, tc.program)
			trailer := fontMissingBaseFont("Type1", &desc)
			runFixerAndCheckIdempotent(t, baseFontFixer{}, &trailer)

			if got := baseFontOf(t, trailer); (got != pdf.PDFName{Value: tc.want}) {
				t.Errorf("BaseFont = %v, want /%s", got, tc.want)
			}
		})
	}
}

// TestBaseFontFixerFallsBack covers the last resort: nothing in the file says
// what the font is called, so a fixed placeholder goes in and the repair still
// converges.
func TestBaseFontFixerFallsBack(t *testing.T) {
	undecodable := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Filter": pdf.PDFName{Value: "FlateDecode"},
	}), HasStream: true, RawStream: []byte("not a zlib stream")}

	for _, tc := range []struct {
		name string
		desc *pdf.PDFDict
	}{
		{"no descriptor", nil},
		{"empty descriptor", &pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}},
		{"descriptor FontName is not a name", &pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"FontName": pdf.PDFString{Value: "MyFace"},
		})}},
		{"descriptor FontName has no usable bytes", &pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"FontName": pdf.PDFName{Value: "  "},
		})}},
		{"program will not decode", &pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"FontFile3": undecodable,
		})}},
		{"program is not a font", func() *pdf.PDFDict {
			d := descriptorWithProgram("FontFile2", []byte("nothing font-shaped here"))
			return &d
		}()},
		{"font file is not a stream", &pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"FontFile": pdf.NewPDFDict(),
		})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trailer := fontMissingBaseFont("TrueType", tc.desc)
			runFixerAndCheckIdempotent(t, baseFontFixer{}, &trailer)

			if got := baseFontOf(t, trailer); (got != pdf.PDFName{Value: fallbackBaseFontName}) {
				t.Errorf("BaseFont = %v, want /%s", got, fallbackBaseFontName)
			}
		})
	}
}

// TestBaseFontFixerReplacesWrongType covers a BaseFont that is present but not
// a name, which the check reports for the same reason as an absent one.
func TestBaseFontFixerReplacesWrongType(t *testing.T) {
	desc := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"FontName": pdf.PDFName{Value: "MyFace"},
	})}
	trailer := fontMissingBaseFont("Type1", &desc)
	trailer.Entries.Get("Font").(pdf.PDFDict).Entries.Set("BaseFont", pdf.PDFString{Value: "MyFace"})

	runFixerAndCheckIdempotent(t, baseFontFixer{}, &trailer)
	if got := baseFontOf(t, trailer); (got != pdf.PDFName{Value: "MyFace"}) {
		t.Errorf("BaseFont = %v, want /MyFace", got)
	}
}

// TestBaseFontFixerSkipsFontsThatNeedNoName covers the no-ops: Type3 fonts
// have no BaseFont, a font that already has one keeps it, and a dict that is
// not a font is not touched.
func TestBaseFontFixerSkipsFontsThatNeedNoName(t *testing.T) {
	type3 := fontMissingBaseFont("Type3", nil)
	if changed, _ := (baseFontFixer{}).Fix(&type3, nil); changed {
		t.Error("changed = true for a Type3 font, which has no BaseFont")
	}

	named := fontMissingBaseFont("Type1", nil)
	named.Entries.Get("Font").(pdf.PDFDict).Entries.Set("BaseFont", pdf.PDFName{Value: "Helvetica"})
	if changed, _ := (baseFontFixer{}).Fix(&named, nil); changed {
		t.Error("changed = true for a font that already has a BaseFont")
	}

	notAFont := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Subtype": pdf.PDFName{Value: "Type1"},
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"X": notAFont})}
	if changed, _ := (baseFontFixer{}).Fix(&trailer, nil); changed {
		t.Error("changed = true for a dict that is not a font")
	}
}

// TestSanitizeBaseFontName covers the byte filter on its own: a name recovered
// from a font program has to be writable as a PDF name unescaped.
func TestSanitizeBaseFontName(t *testing.T) {
	for in, want := range map[string]string{
		"Helvetica":      "Helvetica",
		"ABCDEF+Face":    "ABCDEF+Face",
		"My Face":        "MyFace",
		"a/b[c]d(e)f%g#": "abcdefg",
		"\x00\x7fé":      "",
		"":               "",
	} {
		if got := sanitizeBaseFontName(in); got != want {
			t.Errorf("sanitizeBaseFontName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSfntPostScriptNameMalformed covers the name-table guards: a table too
// short to hold a header, a record pointing outside it, and one with no
// PostScript name at all.
func TestSfntPostScriptNameMalformed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		table []byte
	}{
		{"too short", []byte{0, 0}},
		{"count overruns the table", []byte{0, 0, 0, 4, 0, 6, 0, 0}},
		{"no name ID 6", append([]byte{0, 0, 0, 1, 0, 18},
			0, 3, 0, 1, 0, 0, 0, 1, 0, 2, 0, 0, 'h', 'i')},
		{"offset outside the table", append([]byte{0, 0, 0, 1, 0, 18},
			0, 3, 0, 1, 0, 0, 0, 6, 0, 99, 0, 99)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sfntPostScriptName(tc.table); got != "" {
				t.Errorf("sfntPostScriptName = %q, want empty", got)
			}
		})
	}

	// A Macintosh-platform record is plain bytes, not UTF-16BE.
	mac := append([]byte{0, 0, 0, 1, 0, 18},
		0, 1, 0, 0, 0, 0, 0, 6, 0, 3, 0, 0)
	mac = append(mac, 'A', 'B', 'C')
	if got := sfntPostScriptName(mac); got != "ABC" {
		t.Errorf("sfntPostScriptName(mac) = %q, want ABC", got)
	}
}

// TestConvertClearsMissingBaseFont is the end-to-end proof: a font with no
// BaseFont gets the name its descriptor already recorded, and the document
// converts without the page being rasterized.
func TestConvertClearsMissingBaseFont(t *testing.T) {
	ttf := loadLiberationSansForTest(t)

	b := pdfgen.NewBuilder("%PDF-1.4\n")
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] "+
		"/Resources << /Font << /F1 4 0 R >> >> /Contents 6 0 R >>")
	b.Obj(4, "<< /Type /Font /Subtype /TrueType /Encoding /WinAnsiEncoding "+
		"/FirstChar 65 /LastChar 65 /Widths [1479] /FontDescriptor 5 0 R >>")
	b.Obj(5, "<< /Type /FontDescriptor /FontName /RecordedFace /Flags 32 /ItalicAngle 0 "+
		"/Ascent 700 /Descent -200 /CapHeight 700 /StemV 80 /FontBBox [0 0 100 100] "+
		"/FontFile2 7 0 R >>")
	content := []byte("BT /F1 12 Tf 10 100 Td (A) Tj ET")
	b.StreamObj(6, "<<", content)
	b.StreamObj(7, "<< /Length1 "+strconv.Itoa(len(ttf)), ttf)
	data := b.FinishClassic("<< /Size 8 /Root 1 0 R >>")

	res, err := verify.VerifyBytes(data, pdf.PDFA1B, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	found := false
	for _, iss := range res.Issues {
		if iss.Check() == pdf.Checks.Font.FontBaseFont {
			found = true
		}
	}
	if !found {
		t.Fatal("sanity: FontBaseFont not reported for a font dictionary with no BaseFont")
	}

	cr, err := ConvertBytes(data, pdf.PDFA1B, Options{})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	defer cr.Close()
	for _, iss := range cr.Result.Issues {
		if iss.Check() == pdf.Checks.Font.FontBaseFont {
			t.Errorf("FontBaseFont survived conversion: %v", iss)
		}
	}
	if len(cr.RasterizedPages) != 0 {
		t.Errorf("page %v was rasterized; a BaseFont should just have been added", cr.RasterizedPages)
	}
}
