package convert

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
)

// a2bSpecForTest returns the PDF/A-2b spec.
func a2bSpecForTest() *pdfa.Spec { s, _ := pdfa.For(pdf.A2B); return &s }

// cmapStream returns an embedded CMap stream holding program, with the given
// CMapName and UseCMap (none when nil).
func cmapStream(name string, useCMap pdf.PDFValue, program string) pdf.PDFDict {
	d := streamOf([]byte(program))
	d.Entries.Set("Type", pdf.PDFName{Value: "CMap"})
	d.Entries.Set("CMapName", pdf.PDFName{Value: name})
	if useCMap != nil {
		d.Entries.Set("UseCMap", useCMap)
	}
	return d
}

// type0Over returns a Type0 font with enc as its Encoding.
func type0Over(enc pdf.PDFValue) pdf.PDFDict {
	return dictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type0"}, "Encoding": enc,
	})
}

const (
	ownCMap  = "begincmap\n/Base usecmap\n1 begincidrange\n<0000> <00ff> 0\nendcidrange\nendcmap\n"
	baseCMap = "begincmap\n1 begincodespacerange\n<0000> <ffff>\nendcodespacerange\n1 begincidchar\n<0100> 7\nendcidchar\nendcmap\n"
)

func TestMergeCMapPrograms(t *testing.T) {
	got := string(mergeCMapPrograms([]byte(ownCMap), []byte(baseCMap), ""))
	want := "begincmap\n\n1 begincodespacerange\n<0000> <ffff>\nendcodespacerange\n1 begincidchar\n<0100> 7\nendcidchar\n" +
		"1 begincidrange\n<0000> <00ff> 0\nendcidrange\nendcmap\n"
	if got != want {
		t.Errorf("merged program = %q, want %q", got, want)
	}

	// A CMap without mapping blocks of its own gets the used ones before its
	// endcmap, or at the end when it has no endcmap. next replaces base in the
	// usecmap operator.
	got = string(mergeCMapPrograms([]byte("begincmap\nendcmap\n"), []byte(baseCMap), "H"))
	if !strings.HasPrefix(got, "begincmap\n/H usecmap\n1 begincodespacerange") || !strings.HasSuffix(got, "endcidchar\nendcmap\n") {
		t.Errorf("merged program = %q", got)
	}
	if got := string(mergeCMapPrograms([]byte("%junk"), []byte(baseCMap), "")); !strings.HasPrefix(got, "%junk1 begincodespacerange") {
		t.Errorf("merged program = %q", got)
	}
}

func TestUseCMapFixer(t *testing.T) {
	f := useCMapFixer{spec: a2bSpecForTest()}
	for _, c := range pdf.AllChecks() {
		if got, want := f.Applies(c), c == pdf.Checks.Font.UseCMapNotPredefined; got != want {
			t.Errorf("Applies(%s) = %v, want %v", c.Name(), got, want)
		}
	}

	own := cmapStream("Own", cmapStream("Base", nil, baseCMap), ownCMap)
	trailer := trailerWith("F", type0Over(own))
	if changed, _ := f.Fix(&trailer, nil); !changed {
		t.Fatal("Fix changed nothing")
	}
	if own.Entries.Get("UseCMap") != nil {
		t.Error("UseCMap kept after the merge")
	}
	data, _ := pdf.DecodeStream(own)
	if !strings.Contains(string(data), "<0100> 7") || strings.Contains(string(data), "usecmap") {
		t.Errorf("merged program = %q", data)
	}
	if changed, _ := f.Fix(&trailer, nil); changed {
		t.Error("second pass changed the merged CMap")
	}

	// A chain is flattened down to the predefined CMap at its end.
	mid := cmapStream("Mid", pdf.PDFName{Value: "H"}, baseCMap)
	top := cmapStream("Top", mid, ownCMap)
	trailer = trailerWith("F", type0Over(top))
	if changed, _ := f.Fix(&trailer, nil); !changed {
		t.Fatal("Fix changed nothing in a chain")
	}
	if got := top.Entries.Get("UseCMap"); got != (pdf.PDFName{Value: "H"}) {
		t.Errorf("UseCMap = %v, want /H", got)
	}
	if data, _ := pdf.DecodeStream(top); !strings.Contains(string(data), "/H usecmap") {
		t.Errorf("merged program = %q", data)
	}

	// A chain deeper than the fixer follows is left alone.
	deep := cmapStream("C0", nil, baseCMap)
	for i := range maxUseCMapDepth + 1 {
		deep = cmapStream("C"+string(rune('a'+i)), deep, ownCMap)
	}

	for name, enc := range map[string]pdf.PDFValue{
		"predefined used CMap": cmapStream("Own", cmapStream("H", nil, baseCMap), ownCMap),
		"used CMap by name":    cmapStream("Own", pdf.PDFName{Value: "Custom"}, ownCMap),
		"no UseCMap":           cmapStream("Own", nil, ownCMap),
		"undecodable own": func() pdf.PDFDict {
			d := undecodableStream()
			d.Entries.Set("UseCMap", cmapStream("B", nil, baseCMap))
			return d
		}(),
		"undecodable used": cmapStream("Own", undecodableStream(), ownCMap),
		"named Encoding":   pdf.PDFName{Value: "Identity-H"},
		"too deep":         deep,
	} {
		trailer := trailerWith("F", type0Over(enc))
		if changed, _ := f.Fix(&trailer, nil); changed {
			t.Errorf("%s: Fix changed the font", name)
		}
	}
	simple := trailerWith("F", dictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type1"}}))
	if changed, _ := f.Fix(&simple, nil); changed {
		t.Error("Fix changed a simple font")
	}
}

// notdefPage returns a page whose content stream is content and whose
// resources hold fonts, and a trailer over it.
func notdefPage(content string, fonts map[string]pdf.PDFValue) (page pdf.PDFDict, trailer pdf.PDFDict) {
	page = dictOf(map[string]pdf.PDFValue{
		"Type":      pdf.PDFName{Value: "Page"},
		"Contents":  streamOf([]byte(content)),
		"Resources": dictOf(map[string]pdf.PDFValue{"Font": dictOf(fonts)}),
	})
	trailer = trailerWith("Pages", dictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"}, "Kids": pdf.PDFArray{page},
	}))
	return page, trailer
}

// winAnsiType1 is a Type 1 font with WinAnsiEncoding whose codes from 0 on
// have the given widths.
func winAnsiType1(widths ...int) pdf.PDFDict {
	return dictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type1"},
		"Encoding": pdf.PDFName{Value: "WinAnsiEncoding"}, "FirstChar": pdf.PDFInteger(0), "Widths": ints(widths...),
	})
}

// contentOf returns the decoded content stream of page.
func contentOf(t *testing.T, page pdf.PDFDict) string {
	t.Helper()
	data, err := pdf.DecodeStream(page.Entries.Get("Contents").(pdf.PDFDict))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestNotdefFixer(t *testing.T) {
	f := notdefFixer{}
	for _, c := range pdf.AllChecks() {
		if got, want := f.Applies(c), c == pdf.Checks.Font.NotdefGlyphReference; got != want {
			t.Errorf("Applies(%s) = %v, want %v", c.Name(), got, want)
		}
	}

	font := winAnsiType1(500, 600)
	font.Entries.Set("FirstChar", pdf.PDFInteger(0))
	font.Entries.Set("Widths", pdf.PDFArray{pdf.PDFInteger(500)})
	font.Entries.Set("FontDescriptor", dictOf(map[string]pdf.PDFValue{"MissingWidth": pdf.PDFInteger(250)}))
	diffs := winAnsiType1(400)
	diffs.Entries.Set("Encoding", dictOf(map[string]pdf.PDFValue{
		"BaseEncoding": pdf.PDFName{Value: "WinAnsiEncoding"},
		"Differences":  pdf.PDFArray{pdf.PDFInteger(32), pdf.PDFName{Value: ".notdef"}},
	}))
	diffs.Entries.Set("FirstChar", pdf.PDFInteger(32))
	for _, tc := range []struct {
		name, in, want string
	}{
		{"Tj", "BT /F1 10 Tf 2 Tc (A\x00B) Tj ET", "[(A) -700 (B)] TJ"},
		{"TJ", "BT /F1 10 Tf [(\x00) -50 (C)] TJ ET", "[-500 -50 (C)] TJ"},
		{"hex string", "BT /F1 10 Tf <0041> Tj ET", "[-500 (A)] TJ"},
		{"missing width", "BT /F1 10 Tf (\x01) Tj ET", "[-250] TJ"},
		{"quote", "BT /F1 10 Tf (\x00) ' ET", "T*\n[-500] TJ"},
		{"double quote", "BT /F1 10 Tf 1 2 (\x00) \" ET", "1 Tw\n2 Tc\nT*\n[-700] TJ"},
		{"word spacing", "BT /F2 10 Tf 3 Tw (a b) Tj ET", "[(a) -700 (b)] TJ"},
		{"size zero", "BT /F2 0 Tf 3 Tw ( ) Tj ET", "[-400] TJ"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, trailer := notdefPage(tc.in, map[string]pdf.PDFValue{"F1": font, "F2": diffs})
			if changed, _ := f.Fix(&trailer, nil); !changed {
				t.Fatal("Fix changed nothing")
			}
			if got := contentOf(t, page); !strings.Contains(got, tc.want) {
				t.Errorf("content = %q, want it to contain %q", got, tc.want)
			}
		})
	}

	for _, tc := range []struct{ name, in string }{
		{"no .notdef", "BT /F1 10 Tf (AB) Tj [(C) 5] TJ ET"},
		{"font restored by Q", "q BT /F1 10 Tf ET Q BT (\x00) Tj ET"},
		{"no font", "BT (\x00) Tj ET"},
		{"unknown font", "BT /F9 10 Tf (\x00) Tj ET"},
		{"bad double quote", "BT /F1 10 Tf (\x00) \" ET"},
		{"no operands", "BT /F1 10 Tf Tj ET"},
		{"unbalanced Q", "Q BT /F1 10 Tf 0 Tc 0 Tw (A) Tj ET"},
		{"bad Tf", "BT /F1 Tf (\x00) Tj ET"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, trailer := notdefPage(tc.in, map[string]pdf.PDFValue{"F1": font})
			if changed, _ := f.Fix(&trailer, nil); changed {
				t.Error("Fix changed the stream")
			}
		})
	}

	// A broken stream is left as it is.
	page, trailer := notdefPage("", map[string]pdf.PDFValue{"F1": font})
	page.Entries.Set("Contents", undecodableStream())
	if changed, _ := f.Fix(&trailer, nil); changed {
		t.Error("Fix changed an undecodable stream")
	}
}

// identityType0 returns an Identity-H Type0 font over a CIDFontType0 that
// embeds a CID-keyed CFF program with glyphs for CIDs 1 and 2, CID 1 being
// 600 wide.
func identityType0(t *testing.T) pdf.PDFDict {
	t.Helper()
	cid := dictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "CIDFontType0"},
		"FontDescriptor": dictOf(map[string]pdf.PDFValue{"FontFile3": streamOf(buildMinimalCIDCFF())}),
		"W":              pdf.PDFArray{pdf.PDFInteger(1), ints(600)},
		"DW":             pdf.PDFInteger(900),
	})
	return dictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type0"},
		"Encoding": pdf.PDFName{Value: "Identity-H"}, "DescendantFonts": pdf.PDFArray{cid},
	})
}

func TestNotdefFixerCompositeFont(t *testing.T) {
	page, trailer := notdefPage("BT /F1 10 Tf <00010000000201> Tj ET", map[string]pdf.PDFValue{"F1": identityType0(t)})
	if changed, _ := (notdefFixer{}).Fix(&trailer, nil); !changed {
		t.Fatal("Fix changed nothing")
	}
	// CID 0 has no /W entry, so its advance is DW, 900. The trailing partial
	// code is dropped without an adjustment.
	if got := contentOf(t, page); !strings.Contains(got, "[(\x00\x01) -900 (\x00\x02)] TJ") {
		t.Errorf("content = %q", got)
	}
}

func TestNotdefFixerForm(t *testing.T) {
	form := streamOf([]byte("BT /F1 10 Tf (\x00) Tj ET"))
	form.Entries.Set("Type", pdf.PDFName{Value: "XObject"})
	form.Entries.Set("Subtype", pdf.PDFName{Value: "Form"})
	form.Entries.Set("Resources", dictOf(map[string]pdf.PDFValue{"Font": dictOf(map[string]pdf.PDFValue{"F1": winAnsiType1(500)})}))
	trailer := trailerWith("Form", form)
	if changed, _ := (notdefFixer{}).Fix(&trailer, nil); !changed {
		t.Fatal("Fix changed nothing")
	}
	if data, _ := pdf.DecodeStream(form); !strings.Contains(string(data), "[-500] TJ") {
		t.Errorf("form content = %q", data)
	}
}

func TestGlyphWidthFunc(t *testing.T) {
	simple := winAnsiType1(500)
	simple.Entries.Set("Widths", pdf.PDFArray{pdf.PDFName{Value: "x"}})
	if got := glyphWidthFunc(simple)(0); got != 0 {
		t.Errorf("width of a non-number entry = %v, want the missing width 0", got)
	}
	composite := identityType0(t)
	if w := glyphWidthFunc(composite); w(1) != 600 || w(5) != 900 {
		t.Errorf("composite widths = %v, %v; want 600 and the DW 900", w(1), w(5))
	}
	cid := composite.Entries.Get("DescendantFonts").(pdf.PDFArray)[0].(pdf.PDFDict)
	cid.Entries.Del("DW")
	if w := glyphWidthFunc(composite); w(5) != 1000 {
		t.Errorf("width without DW = %v, want 1000", w(5))
	}
}

func TestFixPanose(t *testing.T) {
	descWith := func(panose pdf.PDFValue) pdf.PDFDict {
		return dictOf(map[string]pdf.PDFValue{
			"Type":  pdf.PDFName{Value: "FontDescriptor"},
			"Style": dictOf(map[string]pdf.PDFValue{"Panose": panose}),
		})
	}
	ten := "\x02\x00\x00\x00\x00\x00\x00\x00\x00\x00"
	for _, tc := range []struct {
		name      string
		desc      pdf.PDFDict
		changed   bool
		wantStyle bool
	}{
		{"ten bytes", descWith(pdf.PDFString{Value: ten}), true, true},
		{"ten hex bytes", descWith(pdf.PDFHexString{Value: "02000000000000000000"}), true, true},
		{"twelve bytes", descWith(pdf.PDFString{Value: "\x00\x00" + ten}), false, true},
		{"seven bytes", descWith(pdf.PDFString{Value: "\x00\x00\x00\x00\x00\x00\x00"}), true, false},
		{"not a string", descWith(pdf.PDFInteger(1)), true, false},
		{"no Style", dictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "FontDescriptor"}}), false, false},
		{"not a descriptor", dictOf(map[string]pdf.PDFValue{"Style": pdf.NewPDFDict()}), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fixPanose(tc.desc); got != tc.changed {
				t.Errorf("fixPanose = %v, want %v", got, tc.changed)
			}
			style, hasStyle := tc.desc.Entries.Get("Style").(pdf.PDFDict)
			if hasStyle != tc.wantStyle {
				t.Fatalf("Style kept = %v, want %v", hasStyle, tc.wantStyle)
			}
			if p, ok := style.Entries.Get("Panose").(pdf.PDFString); ok && tc.changed && len(p.Value) != 12 {
				t.Errorf("Panose has %d bytes, want 12", len(p.Value))
			}
		})
	}
	trailer := trailerWith("D", descWith(pdf.PDFString{Value: ten}))
	if changed, _ := (constraintFixer{}).Fix(&trailer, nil); !changed {
		t.Error("constraintFixer left a 10-byte Panose")
	}
}

// sfntCmapOnly returns a TrueType program with one cmap record per
// (platform, encoding) pair.
func sfntCmapOnly(records ...[2]uint16) []byte {
	cmap := binary.BigEndian.AppendUint16(nil, 0)
	cmap = binary.BigEndian.AppendUint16(cmap, uint16(len(records)))
	for _, r := range records {
		cmap = binary.BigEndian.AppendUint16(cmap, r[0])
		cmap = binary.BigEndian.AppendUint16(cmap, r[1])
		cmap = binary.BigEndian.AppendUint32(cmap, uint32(4+8*len(records)))
	}
	cmap = append(cmap, 0, 0, 1, 6, 0, 0)
	cmap = append(cmap, make([]byte, 256)...)
	out := binary.BigEndian.AppendUint32(nil, 0x00010000)
	out = binary.BigEndian.AppendUint16(out, 1)
	out = append(out, make([]byte, 6)...)
	out = append(out, "cmap"...)
	out = binary.BigEndian.AppendUint32(out, 0)
	out = binary.BigEndian.AppendUint32(out, 28)
	out = binary.BigEndian.AppendUint32(out, uint32(len(cmap)))
	return append(out, cmap...)
}

func TestTrueTypeEncodingFixerSymbolOnlyCmap(t *testing.T) {
	fontWith := func(program pdf.PDFDict) (pdf.PDFDict, pdf.PDFDict) {
		desc := dictOf(map[string]pdf.PDFValue{"Flags": pdf.PDFInteger(32), "FontFile2": program})
		return dictOf(map[string]pdf.PDFValue{
			"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "TrueType"},
			"Encoding": pdf.PDFName{Value: "WinAnsiEncoding"}, "FontDescriptor": desc,
		}), desc
	}
	reported := []pdf.PDFError{pdf.NewError(pdf.Checks.Font.NonSymbolicTrueTypeCmap, nil, 0, nil)}

	font, desc := fontWith(streamOf(sfntCmapOnly([2]uint16{3, 0})))
	trailer := trailerWith("F", font)
	if changed, _ := (trueTypeEncodingFixer{}).Fix(&trailer, reported); !changed {
		t.Fatal("Fix changed nothing")
	}
	if flags := desc.Entries.Get("Flags"); flags != pdf.PDFInteger(4) {
		t.Errorf("Flags = %v, want 4 (symbolic)", flags)
	}
	if font.Entries.Get("Encoding") != nil {
		t.Error("Encoding kept on the now symbolic font")
	}

	// Without the report the font is left alone. onlySymbolCmap is false for
	// a program with another subtable, one without a (3,0) subtable, or one
	// that cannot be read.
	font, _ = fontWith(streamOf(sfntCmapOnly([2]uint16{3, 0})))
	trailer = trailerWith("F", font)
	if changed, _ := (trueTypeEncodingFixer{}).Fix(&trailer, nil); changed {
		t.Error("Fix changed a font without the report")
	}
	for name, program := range map[string]pdf.PDFDict{
		"two subtables": streamOf(sfntCmapOnly([2]uint16{1, 0}, [2]uint16{3, 0})),
		"(3,1) only":    streamOf(sfntCmapOnly([2]uint16{3, 1})),
		"undecodable":   undecodableStream(),
		"not sfnt":      streamOf([]byte("junk")),
		"not a stream":  pdf.NewPDFDict(),
	} {
		if onlySymbolCmap(dictOf(map[string]pdf.PDFValue{"FontFile2": program})) {
			t.Errorf("%s: onlySymbolCmap = true", name)
		}
	}
}

func TestType0FontFixerSupplement(t *testing.T) {
	csi := func(supplement pdf.PDFValue) pdf.PDFDict {
		d := dictOf(map[string]pdf.PDFValue{
			"Registry": pdf.PDFString{Value: "Adobe"}, "Ordering": pdf.PDFString{Value: "Japan1"},
		})
		if supplement != nil {
			d.Entries.Set("Supplement", supplement)
		}
		return d
	}
	fontWith := func(cmapSupp, cidSupp pdf.PDFValue) (pdf.PDFDict, pdf.PDFDict) {
		cmap := dictOf(map[string]pdf.PDFValue{"Type": pdf.PDFName{Value: "CMap"}, "CIDSystemInfo": csi(cmapSupp)})
		cid := dictOf(map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "CIDFontType0"}, "CIDSystemInfo": csi(cidSupp)})
		font := type0Over(cmap)
		font.Entries.Set("DescendantFonts", pdf.PDFArray{cid})
		return font, cmap
	}

	font, cmap := fontWith(pdf.PDFInteger(2), pdf.PDFInteger(3))
	trailer := trailerWith("F", font)
	if changed, _ := (type0FontFixer{}).Fix(&trailer, nil); !changed {
		t.Fatal("Fix changed nothing")
	}
	if got := cmap.Entries.Get("CIDSystemInfo").(pdf.PDFDict).Entries.Get("Supplement"); got != pdf.PDFInteger(3) {
		t.Errorf("CMap Supplement = %v, want 3", got)
	}

	// A CIDFont without an integer Supplement gives the CMap no Supplement
	// to copy, so the CMap is left alone.
	font, _ = fontWith(nil, nil)
	trailer = trailerWith("F", font)
	if changed, _ := (type0FontFixer{}).Fix(&trailer, nil); changed {
		t.Error("Fix changed a CMap the CIDFont has no Supplement for")
	}
}

func TestFontDictFixerReplacesInvalidCIDToGIDMap(t *testing.T) {
	cid := dictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "CIDFontType2"},
		"CIDToGIDMap": pdf.PDFName{Value: "Custom"},
	})
	trailer := trailerWith("F", cid)
	if changed, _ := (fontDictFixer{}).Fix(&trailer, nil); !changed {
		t.Fatal("Fix changed nothing")
	}
	if got := cid.Entries.Get("CIDToGIDMap"); got != (pdf.PDFName{Value: "Identity"}) {
		t.Errorf("CIDToGIDMap = %v, want /Identity", got)
	}
}
