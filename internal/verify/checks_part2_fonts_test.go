package verify

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
)

// a2bSpec is PDF/A-2b's spec for test contexts that only read it.
var a2bSpec = func() *pdfa.Spec { s, _ := pdfa.For(pdf.A2B); return &s }()

// sfntWithCmaps returns a TrueType program whose cmap table has one record per
// (platform, encoding) pair, all pointing at one format-0 subtable, and whose
// maxp table declares numGlyphs glyphs.
func sfntWithCmaps(numGlyphs int, records ...[2]uint16) []byte {
	cmap := binary.BigEndian.AppendUint16(nil, 0)
	cmap = binary.BigEndian.AppendUint16(cmap, uint16(len(records)))
	sub := uint32(4 + 8*len(records))
	for _, r := range records {
		cmap = binary.BigEndian.AppendUint16(cmap, r[0])
		cmap = binary.BigEndian.AppendUint16(cmap, r[1])
		cmap = binary.BigEndian.AppendUint32(cmap, sub)
	}
	cmap = binary.BigEndian.AppendUint16(cmap, 0)   // format 0
	cmap = binary.BigEndian.AppendUint16(cmap, 262) // length
	cmap = binary.BigEndian.AppendUint16(cmap, 0)   // language
	cmap = append(cmap, make([]byte, 256)...)
	maxp := binary.BigEndian.AppendUint32(nil, 0x00005000)
	maxp = binary.BigEndian.AppendUint16(maxp, uint16(numGlyphs))

	tables := []struct {
		tag  string
		data []byte
	}{{"cmap", cmap}, {"maxp", maxp}}
	out := binary.BigEndian.AppendUint32(nil, 0x00010000)
	out = binary.BigEndian.AppendUint16(out, uint16(len(tables)))
	out = append(out, make([]byte, 6)...)
	off := 12 + 16*len(tables)
	var body []byte
	for _, tb := range tables {
		out = append(out, tb.tag...)
		out = binary.BigEndian.AppendUint32(out, 0)
		out = binary.BigEndian.AppendUint32(out, uint32(off+len(body)))
		out = binary.BigEndian.AppendUint32(out, uint32(len(tb.data)))
		body = append(body, tb.data...)
	}
	return append(out, body...)
}

// trueTypeFontWith returns a TrueType font dictionary and its descriptor,
// with the given Flags and, when program is not nil, a FontFile2.
func trueTypeFontWith(flags int, program []byte) (font, desc pdf.PDFDict) {
	desc = pdf.NewPDFDict()
	desc.Entries.Set("Flags", pdf.PDFInteger(flags))
	if program != nil {
		desc.Entries.Set("FontFile2", streamDict(string(program)))
	}
	font = pdf.NewPDFDict()
	font.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	font.Entries.Set("Subtype", pdf.PDFName{Value: "TrueType"})
	font.Entries.Set("FontDescriptor", desc)
	return font, desc
}

// encodingDict returns an Encoding dictionary with base as its BaseEncoding
// (none when empty) and the given Differences glyph names from code 96 on.
func encodingDict(base string, names ...string) pdf.PDFDict {
	enc := pdf.NewPDFDict()
	if base != "" {
		enc.Entries.Set("BaseEncoding", pdf.PDFName{Value: base})
	}
	if len(names) > 0 {
		diffs := pdf.PDFArray{pdf.PDFInteger(96)}
		for _, n := range names {
			diffs = append(diffs, pdf.PDFName{Value: n})
		}
		enc.Entries.Set("Differences", diffs)
	}
	return enc
}

func TestPart2TrueTypeEncoding(t *testing.T) {
	unicode := sfntWithCmaps(4, [2]uint16{3, 1})
	symbolOnly := sfntWithCmaps(4, [2]uint16{3, 0})
	for _, tc := range []struct {
		name    string
		enc     pdf.PDFValue
		program []byte
		want    bool
	}{
		{"WinAnsi name", pdf.PDFName{Value: "WinAnsiEncoding"}, unicode, false},
		{"Standard name", pdf.PDFName{Value: "StandardEncoding"}, unicode, true},
		{"no Encoding", nil, unicode, true},
		{"MacRoman base, no Differences", encodingDict("MacRomanEncoding"), unicode, false},
		{"no BaseEncoding", encodingDict(""), unicode, true},
		{"glyph list Differences", encodingDict("WinAnsiEncoding", "grave"), unicode, false},
		{"Differences off the glyph list", encodingDict("WinAnsiEncoding", "grav"), unicode, true},
		{"Differences without a (3,1) subtable", encodingDict("WinAnsiEncoding", "grave"), symbolOnly, true},
		{"Differences, unreadable program", encodingDict("WinAnsiEncoding", "grave"), []byte("junk"), true},
		{"Differences, no program", encodingDict("WinAnsiEncoding", "grav"), nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			font, desc := trueTypeFontWith(32, tc.program)
			if tc.enc != nil {
				font.Entries.Set("Encoding", tc.enc)
			}
			ctx := &ValidationContext{Spec: a2bSpec}
			validateTrueTypeEncoding(font, desc, ctx)
			if got := hasCheck(ctx, pdf.Checks.Font.TrueTypeEncoding); got != tc.want {
				t.Errorf("TrueTypeEncoding reported = %v, want %v (%v)", got, tc.want, ctx.errs)
			}
		})
	}
}

func TestPart2TrueTypeCmaps(t *testing.T) {
	for _, tc := range []struct {
		name      string
		flags     int
		records   [][2]uint16
		spec      *pdfa.Spec
		check     pdf.Check
		wantFound bool
	}{
		{"non-symbolic, (3,0) only", 32, [][2]uint16{{3, 0}}, a2bSpec, pdf.Checks.Font.NonSymbolicTrueTypeCmap, true},
		{"non-symbolic, no subtable", 32, nil, a2bSpec, pdf.Checks.Font.NonSymbolicTrueTypeCmap, true},
		{"non-symbolic, (3,0) and (1,0)", 32, [][2]uint16{{1, 0}, {3, 0}}, a2bSpec, pdf.Checks.Font.NonSymbolicTrueTypeCmap, false},
		{"non-symbolic, PDF/A-1", 32, [][2]uint16{{3, 0}}, a1bSpec, pdf.Checks.Font.NonSymbolicTrueTypeCmap, false},
		{"symbolic, (3,0) among two", 4, [][2]uint16{{1, 0}, {3, 0}}, a2bSpec, pdf.Checks.Font.SymbolicTrueTypeCmap, false},
		{"symbolic, two without (3,0)", 4, [][2]uint16{{1, 0}, {3, 1}}, a2bSpec, pdf.Checks.Font.SymbolicTrueTypeCmap, true},
		{"symbolic, (3,0) among two, PDF/A-1", 4, [][2]uint16{{1, 0}, {3, 0}}, a1bSpec, pdf.Checks.Font.SymbolicTrueTypeCmap, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			font, desc := trueTypeFontWith(tc.flags, sfntWithCmaps(4, tc.records...))
			font.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})
			if tc.flags&4 != 0 {
				font.Entries.Del("Encoding")
			}
			ctx := &ValidationContext{Spec: tc.spec}
			validateTrueTypeEncoding(font, desc, ctx)
			if got := hasCheck(ctx, tc.check); got != tc.wantFound {
				t.Errorf("%s reported = %v, want %v (%v)", tc.check.Name(), got, tc.wantFound, ctx.errs)
			}
		})
	}
}

// type0FontWith returns a Type0 font with the given Encoding and a CIDFont
// descendant with the given CIDSystemInfo Supplement.
func type0FontWith(enc pdf.PDFValue, cidSupplement pdf.PDFValue) pdf.PDFDict {
	csi := pdf.NewPDFDict()
	csi.Entries.Set("Registry", pdf.PDFString{Value: "Adobe"})
	csi.Entries.Set("Ordering", pdf.PDFString{Value: "Japan1"})
	if cidSupplement != nil {
		csi.Entries.Set("Supplement", cidSupplement)
	}
	cid := pdf.NewPDFDict()
	cid.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	cid.Entries.Set("Subtype", pdf.PDFName{Value: "CIDFontType0"})
	cid.Entries.Set("CIDSystemInfo", csi)
	font := pdf.NewPDFDict()
	font.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	font.Entries.Set("Subtype", pdf.PDFName{Value: "Type0"})
	font.Entries.Set("Encoding", enc)
	font.Entries.Set("DescendantFonts", pdf.PDFArray{cid})
	return font
}

// embeddedCMap returns a CMap stream with the given Supplement and UseCMap
// entries (none when nil).
func embeddedCMap(supplement, useCMap pdf.PDFValue) pdf.PDFDict {
	csi := pdf.NewPDFDict()
	csi.Entries.Set("Registry", pdf.PDFString{Value: "Adobe"})
	csi.Entries.Set("Ordering", pdf.PDFString{Value: "Japan1"})
	if supplement != nil {
		csi.Entries.Set("Supplement", supplement)
	}
	cmap := streamDict("begincmap endcmap")
	cmap.Entries.Set("Type", pdf.PDFName{Value: "CMap"})
	cmap.Entries.Set("CIDSystemInfo", csi)
	if useCMap != nil {
		cmap.Entries.Set("UseCMap", useCMap)
	}
	return cmap
}

func TestPart2Type0Font(t *testing.T) {
	namedCMap := func(name string) pdf.PDFValue {
		cmap := streamDict("")
		cmap.Entries.Set("CMapName", pdf.PDFName{Value: name})
		return cmap
	}
	for _, tc := range []struct {
		name  string
		font  pdf.PDFDict
		spec  *pdfa.Spec
		check pdf.Check
		want  bool
	}{
		{"Table 118 CMap name", type0FontWith(pdf.PDFName{Value: "90ms-RKSJ-H"}, nil), a2bSpec, pdf.Checks.Font.CMapNotEmbedded, false},
		{"Table 118 CMap name, PDF/A-1", type0FontWith(pdf.PDFName{Value: "90ms-RKSJ-H"}, nil), a1bSpec, pdf.Checks.Font.CMapNotEmbedded, true},
		{"uses a predefined name", type0FontWith(embeddedCMap(pdf.PDFInteger(2), pdf.PDFName{Value: "H"}), pdf.PDFInteger(2)), a2bSpec, pdf.Checks.Font.UseCMapNotPredefined, false},
		{"uses another name", type0FontWith(embeddedCMap(pdf.PDFInteger(2), pdf.PDFName{Value: "Custom"}), pdf.PDFInteger(2)), a2bSpec, pdf.Checks.Font.UseCMapNotPredefined, true},
		{"uses an embedded CMap", type0FontWith(embeddedCMap(pdf.PDFInteger(2), namedCMap("Adobe-Japan1-4")), pdf.PDFInteger(2)), a2bSpec, pdf.Checks.Font.UseCMapNotPredefined, true},
		{"uses an embedded predefined CMap", type0FontWith(embeddedCMap(pdf.PDFInteger(2), namedCMap("H")), pdf.PDFInteger(2)), a2bSpec, pdf.Checks.Font.UseCMapNotPredefined, false},
		{"uses another name, PDF/A-1", type0FontWith(embeddedCMap(pdf.PDFInteger(2), pdf.PDFName{Value: "Custom"}), pdf.PDFInteger(2)), a1bSpec, pdf.Checks.Font.UseCMapNotPredefined, false},
		{"CIDFont Supplement higher", type0FontWith(embeddedCMap(pdf.PDFInteger(2), nil), pdf.PDFInteger(3)), a2bSpec, pdf.Checks.Font.CIDSystemInfoMismatch, true},
		{"CIDFont Supplement lower", type0FontWith(embeddedCMap(pdf.PDFInteger(3), nil), pdf.PDFInteger(2)), a2bSpec, pdf.Checks.Font.CIDSystemInfoMismatch, false},
		{"CMap without Supplement", type0FontWith(embeddedCMap(nil, nil), pdf.PDFInteger(2)), a2bSpec, pdf.Checks.Font.CIDSystemInfoMismatch, true},
		{"CIDFont Supplement higher, PDF/A-1", type0FontWith(embeddedCMap(pdf.PDFInteger(2), nil), pdf.PDFInteger(3)), a1bSpec, pdf.Checks.Font.CIDSystemInfoMismatch, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &ValidationContext{Spec: tc.spec}
			validateType0Font(tc.font, ctx)
			if got := hasCheck(ctx, tc.check); got != tc.want {
				t.Errorf("%s reported = %v, want %v (%v)", tc.check.Name(), got, tc.want, ctx.errs)
			}
		})
	}
}

func TestCMapName(t *testing.T) {
	stream := streamDict("")
	stream.Entries.Set("CMapName", pdf.PDFName{Value: "Adobe-Japan1-4"})
	for _, tc := range []struct {
		cmap pdf.PDFValue
		want string
	}{
		{pdf.PDFName{Value: "H"}, "H"},
		{stream, "Adobe-Japan1-4"},
		{streamDict(""), ""},
		{pdf.PDFInteger(1), ""},
	} {
		if got := CMapName(tc.cmap); got != tc.want {
			t.Errorf("CMapName(%v) = %q, want %q", tc.cmap, got, tc.want)
		}
	}
}

// cidTrueTypeFont returns a CIDFontType2 font dictionary embedding the
// bundled TrueType face, with the given CIDToGIDMap (none when nil).
func cidTrueTypeFont(t *testing.T, c2g pdf.PDFValue) pdf.PDFDict {
	t.Helper()
	desc := pdf.NewPDFDict()
	desc.Entries.Set("FontFile2", streamDict(string(loadTTF(t))))
	font := pdf.NewPDFDict()
	font.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	font.Entries.Set("Subtype", pdf.PDFName{Value: "CIDFontType2"})
	font.Entries.Set("BaseFont", pdf.PDFName{Value: "Liberation"})
	font.Entries.Set("FontDescriptor", desc)
	if c2g != nil {
		font.Entries.Set("CIDToGIDMap", c2g)
	}
	return font
}

func TestCIDToGIDMapValidity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		c2g      pdf.PDFValue
		embedded bool
		spec     *pdfa.Spec
		want     bool
	}{
		{"Identity", pdf.PDFName{Value: "Identity"}, true, a2bSpec, false},
		{"stream", streamDict("\x00\x01"), true, a2bSpec, false},
		{"other name", pdf.PDFName{Value: "Custom"}, true, a2bSpec, true},
		{"dictionary", pdf.NewPDFDict(), true, a2bSpec, true},
		{"missing", nil, true, a2bSpec, true},
		{"missing, not embedded", nil, false, a2bSpec, false},
		{"other name, PDF/A-1", pdf.PDFName{Value: "Custom"}, true, a1bSpec, true},
		{"missing, not embedded, PDF/A-1", nil, false, a1bSpec, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			font := cidTrueTypeFont(t, tc.c2g)
			if !tc.embedded {
				font.Entries.Set("FontDescriptor", pdf.NewPDFDict())
			}
			ctx := &ValidationContext{Spec: tc.spec}
			ValidateFontDict(font, ctx)
			if got := hasCheck(ctx, pdf.Checks.Font.CIDToGIDMapMissing); got != tc.want {
				t.Errorf("CIDToGIDMapMissing reported = %v, want %v (%v)", got, tc.want, ctx.errs)
			}
		})
	}
}

func TestSubsetSetsOptional(t *testing.T) {
	// A CID subset without a CIDSet: required by PDF/A-1, optional in PDF/A-2.
	for _, tc := range []struct {
		spec *pdfa.Spec
		want bool
	}{{a1bSpec, true}, {a2bSpec, false}} {
		font := cidTrueTypeFont(t, pdf.PDFName{Value: "Identity"})
		font.Entries.Set("BaseFont", pdf.PDFName{Value: "ABCDEF+Liberation"})
		ctx := &ValidationContext{Spec: tc.spec}
		ValidateFontDict(font, ctx)
		if got := hasCheck(ctx, pdf.Checks.Font.CIDSubsetCIDSet); got != tc.want {
			t.Errorf("Part %d: CIDSubsetCIDSet reported = %v, want %v", tc.spec.Part, got, tc.want)
		}
	}

	// A Type 1 subset without a CharSet is checked against its program alone
	// in PDF/A-2, and not at all without one.
	font := pdf.NewPDFDict()
	desc := pdf.NewPDFDict()
	ctx := &ValidationContext{Spec: a2bSpec}
	ValidateType1SubsetCoverage(font, font, desc, 32, 32, pdf.PDFArray{pdf.PDFInteger(500)}, ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("reported %v for a subset with neither a CharSet nor a program", ctx.errs)
	}
}

func TestPartialCodeIsMissingGlyph(t *testing.T) {
	font := cidTrueTypeFont(t, pdf.PDFName{Value: "Identity"})
	font.Entries.Set("BaseFont", pdf.PDFName{Value: "ABCDEF+Liberation"})
	ctx := &ValidationContext{Spec: a2bSpec, partialCodeFonts: map[uintptr]bool{pdf.ValuePointer(font.Entries): true}}
	ValidateFontDict(font, ctx)
	if !hasCheck(ctx, pdf.Checks.Font.SubsetGlyphCoverage) {
		t.Errorf("no SubsetGlyphCoverage for a string ending mid-code (%v)", ctx.errs)
	}
}

func TestEncodingGlyphNames(t *testing.T) {
	for _, tc := range []struct {
		name      string
		enc       pdf.PDFValue
		code      int
		want      string
		wantNamed bool
	}{
		{"WinAnsi letter", pdf.PDFName{Value: "WinAnsiEncoding"}, 'A', "A", true},
		{"WinAnsi control code", pdf.PDFName{Value: "WinAnsiEncoding"}, 0, ".notdef", true},
		{"WinAnsi undefined code", pdf.PDFName{Value: "WinAnsiEncoding"}, 129, "bullet", true},
		{"MacRoman control code", pdf.PDFName{Value: "MacRomanEncoding"}, 1, ".notdef", true},
		{"Standard", pdf.PDFName{Value: "StandardEncoding"}, 'A', "", false},
		{"none", nil, 'A', "", false},
		{"Differences entry", encodingDict("", "Aring"), 96, "Aring", true},
		{"Differences .notdef", encodingDict("WinAnsiEncoding", ".notdef"), 96, ".notdef", true},
		{"BaseEncoding fallback", encodingDict("WinAnsiEncoding", "Aring"), 'A', "A", true},
		{"no BaseEncoding fallback", encodingDict("", "Aring"), 'A', "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			names, named := encodingGlyphNames(tc.enc)
			if got := names[tc.code]; got != tc.want || named[tc.code] != tc.wantNamed {
				t.Errorf("encodingGlyphNames[%d] = %q, %v; want %q, %v", tc.code, got, named[tc.code], tc.want, tc.wantNamed)
			}
		})
	}
}

func TestSimpleNotdefCodes(t *testing.T) {
	winAnsi := pdf.PDFName{Value: "WinAnsiEncoding"}
	for _, tc := range []struct {
		name    string
		subtype string
		flags   int
		enc     pdf.PDFValue
		program bool
		code    int
		want    bool
	}{
		{"Type1 control code", "Type1", 32, winAnsi, true, 0, true},
		{"Type1 letter", "Type1", 32, winAnsi, true, 'A', false},
		{"Type1 without Encoding", "Type1", 32, nil, true, 0, false},
		{"symbolic TrueType ignores Encoding", "TrueType", 4, winAnsi, true, 0, false},
		{"TrueType without a program, code 0", "TrueType", 4, nil, false, 0, true},
		{"TrueType without a program, code 1", "TrueType", 4, nil, false, 1, false},
		{"TrueType with a program, code 0", "TrueType", 4, nil, true, 0, false},
		{"Type3 Differences .notdef", "Type3", 0, encodingDict("", ".notdef"), false, 96, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var program []byte
			if tc.program {
				program = sfntWithCmaps(4, [2]uint16{3, 0})
			}
			font, desc := trueTypeFontWith(tc.flags, program)
			font.Entries.Set("Subtype", pdf.PDFName{Value: tc.subtype})
			if tc.enc != nil {
				font.Entries.Set("Encoding", tc.enc)
			}
			if got := simpleNotdefCodes(font, tc.subtype, desc)[tc.code]; got != tc.want {
				t.Errorf("simpleNotdefCodes[%d] = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
	// A font without a descriptor still has an Encoding to go by.
	font := pdf.NewPDFDict()
	font.Entries.Set("Encoding", winAnsi)
	if !simpleNotdefCodes(font, "TrueType", pdf.PDFDict{})[0] {
		t.Error("WinAnsi code 0 of a TrueType font without a descriptor is .notdef")
	}
}

// type0WithDescendant returns an Identity-H Type0 font over cid.
func type0WithDescendant(cid pdf.PDFDict) pdf.PDFDict {
	font := pdf.NewPDFDict()
	font.Entries.Set("Type", pdf.PDFName{Value: "Font"})
	font.Entries.Set("Subtype", pdf.PDFName{Value: "Type0"})
	font.Entries.Set("Encoding", pdf.PDFName{Value: "Identity-H"})
	font.Entries.Set("DescendantFonts", pdf.PDFArray{cid})
	return font
}

// cidCFFFont returns a CIDFontType0 font dictionary embedding program.
func cidCFFFont(program []byte) pdf.PDFDict {
	desc := pdf.NewPDFDict()
	desc.Entries.Set("FontFile3", streamDict(string(program)))
	cid := pdf.NewPDFDict()
	cid.Entries.Set("Subtype", pdf.PDFName{Value: "CIDFontType0"})
	cid.Entries.Set("FontDescriptor", desc)
	return cid
}

func TestCIDGlyphPresence(t *testing.T) {
	ttf := cidTrueTypeFont(t, nil)
	mapped := cidTrueTypeFont(t, streamDict("\x00\x00\x00\x05\xff\xff"))
	for _, tc := range []struct {
		name string
		cid  pdf.PDFDict
		ok   bool
		yes  []int
		no   []int
	}{
		{"TrueType, Identity", ttf, true, []int{1, 36}, []int{0, 1 << 20}},
		{"TrueType, stream map", mapped, true, []int{1}, []int{0, 2, 3}},
		{"CID-keyed CFF", cidCFFFont(buildMinimalCIDCFF()), true, []int{1, 2}, []int{0, 3}},
		{"name-keyed CFF", cidCFFFont(buildMinimalCFF()), true, []int{1}, []int{0, 2}},
		{"TrueType, bad map", cidTrueTypeFont(t, pdf.PDFName{Value: "Custom"}), false, nil, nil},
		{"TrueType, undecodable map", cidTrueTypeFont(t, brokenStreamDict()), false, nil, nil},
		{"TrueType, no program", func() pdf.PDFDict {
			f := cidTrueTypeFont(t, nil)
			f.Entries.Set("FontDescriptor", pdf.NewPDFDict())
			return f
		}(), false, nil, nil},
		{"CFF, no program", func() pdf.PDFDict { f := cidCFFFont(nil); f.Entries.Set("FontDescriptor", pdf.NewPDFDict()); return f }(), false, nil, nil},
		{"CFF, junk program", cidCFFFont([]byte("junk")), false, nil, nil},
		{"CFF, undecodable program", func() pdf.PDFDict {
			f := cidCFFFont(nil)
			desc, _ := f.Entries.Get("FontDescriptor").(pdf.PDFDict)
			desc.Entries.Set("FontFile3", brokenStreamDict())
			return f
		}(), false, nil, nil},
		{"CFF, bad charset", cidCFFFont(corruptCFFCharset(t, buildMinimalCIDCFF())), false, nil, nil},
		{"TrueType, junk program", func() pdf.PDFDict {
			f := cidTrueTypeFont(t, nil)
			desc, _ := f.Entries.Get("FontDescriptor").(pdf.PDFDict)
			desc.Entries.Set("FontFile2", streamDict("junk"))
			return f
		}(), false, nil, nil},
		{"TrueType, undecodable program", func() pdf.PDFDict {
			f := cidTrueTypeFont(t, nil)
			desc, _ := f.Entries.Get("FontDescriptor").(pdf.PDFDict)
			desc.Entries.Set("FontFile2", brokenStreamDict())
			return f
		}(), false, nil, nil},
		{"other subtype", pdf.NewPDFDict(), false, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			present, ok := cidGlyphPresence(tc.cid, &ValidationContext{Spec: a2bSpec})
			if ok != tc.ok {
				t.Fatalf("cidGlyphPresence ok = %v, want %v", ok, tc.ok)
			}
			for _, c := range tc.yes {
				if !present(c) {
					t.Errorf("CID %d has no glyph, want one", c)
				}
			}
			for _, c := range tc.no {
				if present(c) {
					t.Errorf("CID %d has a glyph, want none", c)
				}
			}
		})
	}
}

func TestValidateNotdefReferences(t *testing.T) {
	winAnsi := pdf.PDFName{Value: "WinAnsiEncoding"}
	simple, desc := trueTypeFontWith(32, nil)
	simple.Entries.Set("Subtype", pdf.PDFName{Value: "Type1"})
	simple.Entries.Set("Encoding", winAnsi)
	ptr := pdf.ValuePointer(simple.Entries)

	cid := cidCFFFont(buildMinimalCIDCFF())
	composite := type0WithDescendant(cid)
	cidPtr := pdf.ValuePointer(cid.Entries)

	for _, tc := range []struct {
		name  string
		font  pdf.PDFDict
		sub   string
		ctx   *ValidationContext
		want  bool
		check pdf.Check
	}{
		{"simple, code 0 shown", simple, "Type1", &ValidationContext{UsedCharCodes: map[uintptr]map[int]bool{ptr: {'A': true, 0: true}}}, true, pdf.Checks.Font.NotdefGlyphReference},
		{"simple, letters shown", simple, "Type1", &ValidationContext{UsedCharCodes: map[uintptr]map[int]bool{ptr: {'A': true}}}, false, pdf.Checks.Font.NotdefGlyphReference},
		{"simple, usage unknown", simple, "Type1", &ValidationContext{}, false, pdf.Checks.Font.NotdefGlyphReference},
		{"composite, CID 0 shown", composite, "Type0", &ValidationContext{UsedCIDs: map[uintptr]map[int]bool{cidPtr: {1: true, 0: true}}}, true, pdf.Checks.Font.NotdefGlyphReference},
		{"composite, glyphs shown", composite, "Type0", &ValidationContext{UsedCIDs: map[uintptr]map[int]bool{cidPtr: {1: true, 2: true}}}, false, pdf.Checks.Font.NotdefGlyphReference},
		{"composite, code cut short", composite, "Type0", &ValidationContext{UsedCIDs: map[uintptr]map[int]bool{cidPtr: {1: true}}, partialCodeFonts: map[uintptr]bool{cidPtr: true}}, true, pdf.Checks.Font.NotdefGlyphReference},
		{"composite, usage unknown", composite, "Type0", &ValidationContext{}, false, pdf.Checks.Font.NotdefGlyphReference},
		{"composite, no program", type0WithDescendant(pdf.NewPDFDict()), "Type0", &ValidationContext{UsedCIDs: map[uintptr]map[int]bool{}}, false, pdf.Checks.Font.NotdefGlyphReference},
		{"other subtype", simple, "CIDFontType0", &ValidationContext{}, false, pdf.Checks.Font.NotdefGlyphReference},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.ctx.Spec = a2bSpec
			if tc.name == "composite, no program" {
				d := DescendantCIDFont(tc.font)
				tc.ctx.UsedCIDs[pdf.ValuePointer(d.Entries)] = map[int]bool{0: true}
			}
			validateNotdefReferences(tc.font, tc.sub, desc, tc.ctx)
			if got := hasCheck(tc.ctx, tc.check); got != tc.want {
				t.Errorf("NotdefGlyphReference reported = %v, want %v (%v)", got, tc.want, tc.ctx.errs)
			}
		})
	}
}

func TestNotdefSelector(t *testing.T) {
	simple, _ := trueTypeFontWith(32, nil)
	simple.Entries.Set("Encoding", pdf.PDFName{Value: "WinAnsiEncoding"})
	codeLen, notdef, ok := NotdefSelector(simple, &ValidationContext{})
	if !ok || codeLen != 1 || !notdef(0) || notdef('A') {
		t.Errorf("simple font: codeLen=%d ok=%v", codeLen, ok)
	}

	composite := type0WithDescendant(cidCFFFont(buildMinimalCIDCFF()))
	codeLen, notdef, ok = NotdefSelector(composite, &ValidationContext{})
	if !ok || codeLen != 2 || !notdef(0) || notdef(1) {
		t.Errorf("Identity-H font: codeLen=%d ok=%v", codeLen, ok)
	}

	vertical := type0WithDescendant(cidCFFFont(buildMinimalCIDCFF()))
	vertical.Entries.Set("Encoding", pdf.PDFName{Value: "Identity-V"})
	if _, _, ok := NotdefSelector(vertical, &ValidationContext{}); ok {
		t.Error("an Identity-V font is not judged code by code")
	}
	if _, _, ok := NotdefSelector(type0WithDescendant(pdf.NewPDFDict()), &ValidationContext{}); ok {
		t.Error("a composite font without a program is not judged code by code")
	}
	if _, _, ok := NotdefSelector(pdf.NewPDFDict(), &ValidationContext{}); ok {
		t.Error("a dictionary with no font subtype is not judged")
	}
}

// TestComputeContentUsageTJStringsApart verifies that each string of a TJ
// array is decoded on its own: a two-byte code never spans two strings, and a
// string with a byte left over is a code cut short.
func TestComputeContentUsageTJStringsApart(t *testing.T) {
	descendant := pdf.NewPDFDict()
	composite := type0WithDescendant(descendant)
	fonts := pdf.NewPDFDict()
	fonts.Entries.Set("F1", composite)
	resources := pdf.NewPDFDict()
	resources.Entries.Set("Font", fonts)
	page := pdf.NewPDFDict()
	page.Entries.Set("Type", pdf.PDFName{Value: "Page"})
	page.Entries.Set("Resources", resources)
	page.Entries.Set("Contents", streamDict("BT /F1 12 Tf [<00> -100 <4100>] TJ ET"))

	ctx := &ValidationContext{Spec: a2bSpec}
	_, _, _, usedCIDs, partialCodes, _ := ComputeContentUsage(page, ctx)
	cids := usedCIDs[pdf.ValuePointer(descendant.Entries)]
	if len(cids) != 1 || !cids[0x4100] {
		t.Errorf("CIDs = %v, want only 0x4100", cids)
	}
	if !partialCodes[pdf.ValuePointer(descendant.Entries)] {
		t.Error("the one-byte string is not recorded as a code cut short")
	}
}

// TestFontRulesWithoutSpec verifies that the font rules a level parameterizes
// fall back to the PDF/A-1 values on a context with no spec.
func TestFontRulesWithoutSpec(t *testing.T) {
	ctx := &ValidationContext{}
	if ctx.subsetSetsOptional() {
		t.Error("subset sets optional without a spec")
	}
	if !ctx.predefinedCMap("Identity-H") || ctx.predefinedCMap("90ms-RKSJ-H") {
		t.Error("predefined CMaps without a spec are not the Identity ones")
	}
	validateType0Font(type0FontWith(embeddedCMap(pdf.PDFInteger(2), pdf.PDFName{Value: "Custom"}), pdf.PDFInteger(2)), ctx)
	if hasCheck(ctx, pdf.Checks.Font.UseCMapNotPredefined) {
		t.Error("UseCMapNotPredefined reported without a spec")
	}
}

func TestExactSubsetSets(t *testing.T) {
	// The test program defines /A only, and the font shows code 65 (/A).
	for _, tc := range []struct {
		charSet  string
		a1b, a2b string // the reported message, or "" for none
	}{
		{"/A", "", ""},
		{"/.notdef/A", "", ""},
		{"/A/B", "", "CharSet lists glyph /B, which the embedded font subset does not define"},
		{"/.notdef", "CharSet does not list used glyph /A defined in the font subset",
			"CharSet does not list glyph /A defined in the font subset"},
	} {
		for _, lc := range []struct {
			spec *pdfa.Spec
			want string
		}{{a1bSpec, tc.a1b}, {a2bSpec, tc.a2b}} {
			v := buildType1FontDict("ABCDEF+Test")
			v.Entries.Get("FontDescriptor").(pdf.PDFDict).Entries.Set("CharSet", pdf.PDFString{Value: tc.charSet})
			ctx := &ValidationContext{Spec: lc.spec}
			ValidateFontDict(v, ctx)
			var got string
			for _, e := range ctx.errs {
				if e.Check() == pdf.Checks.Font.Type1SubsetCharSet {
					got = e.Messages()[0]
				}
			}
			if got != lc.want {
				t.Errorf("CharSet %s, Part %d: reported %q, want %q", tc.charSet, lc.spec.Part, got, lc.want)
			}
		}
	}
}

// cidSetIssue runs the exact CIDSet check on a CIDFont and returns its message.
func cidSetIssue(font, desc pdf.PDFDict) string {
	ctx := &ValidationContext{Spec: a2bSpec}
	validateCIDSetExact(font, desc, ctx)
	for _, e := range ctx.errs {
		if e.Check() == pdf.Checks.Font.CIDSubsetCIDSet {
			return e.Messages()[0]
		}
	}
	return ""
}

func cidSetStream(bits ...byte) pdf.PDFDict {
	return pdf.NewStreamDict(pdf.NewDict(), bits)
}

func TestValidateCIDSetExactCFF(t *testing.T) {
	program := pdf.NewStreamDict(pdf.NewDict(), buildMinimalCIDCFF())
	td, ok := fontprog.ParseCFFTopDict(buildMinimalCIDCFF())
	if !ok {
		t.Fatal("test program does not parse")
	}
	count := int(binary.BigEndian.Uint16(buildMinimalCIDCFF()[td.CSOffset:]))
	cids := fontprog.ParseCFFCharsetCIDs(buildMinimalCIDCFF(), td.CharsetOffset, count)
	exact := make([]byte, 16)
	for _, cid := range cids {
		exact[cid/8] |= 0x80 >> (cid % 8)
	}
	font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "CIDFontType0"}})}
	desc := func(set pdf.PDFValue, ff pdf.PDFValue) pdf.PDFDict {
		return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"CIDSet": set, "FontFile3": ff})}
	}

	if got := cidSetIssue(font, desc(cidSetStream(exact...), program)); got != "" {
		t.Errorf("exact set reported: %s", got)
	}
	extra := append([]byte(nil), exact...)
	extra[15] |= 1
	if got := cidSetIssue(font, desc(cidSetStream(extra...), program)); !strings.Contains(got, "lists CID 127") {
		t.Errorf("extra bit: %q", got)
	}
	if len(cids) > 1 {
		if got := cidSetIssue(font, desc(cidSetStream(0), program)); !strings.Contains(got, "does not list") {
			t.Errorf("missing bit: %q", got)
		}
	}
	for name, d := range map[string]pdf.PDFDict{
		"no CIDSet":           {Entries: pdf.DictOf(map[string]pdf.PDFValue{"FontFile3": program})},
		"undecodable CIDSet":  desc(pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "FlateDecode"}}), []byte("x")), program),
		"no program":          {Entries: pdf.DictOf(map[string]pdf.PDFValue{"CIDSet": cidSetStream(0xFF)})},
		"undecodable program": desc(cidSetStream(0xFF), pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "FlateDecode"}}), []byte("x"))),
		"unparsable program":  desc(cidSetStream(0xFF), pdf.NewStreamDict(pdf.NewDict(), []byte("x"))),
	} {
		if got := cidSetIssue(font, d); got != "" {
			t.Errorf("%s: reported %q", name, got)
		}
	}
	// A name-keyed program has no CIDs at all.
	nameKeyed := pdf.NewStreamDict(pdf.NewDict(), buildMinimalCFF())
	if got := cidSetIssue(font, desc(cidSetStream(0x40), nameKeyed)); !strings.Contains(got, "lists CID 1") {
		t.Errorf("name-keyed program: %q", got)
	}
}

func TestValidateCIDSetExactTrueType(t *testing.T) {
	_, ff, _, _ := buildCIDTrueTypeFont(t)
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		t.Fatal(err)
	}
	tables, ok := fontprog.ParseSfnt(data)
	if !ok {
		t.Fatal("test program does not parse")
	}
	numGlyphs := fontprog.TTNumGlyphs(tables)
	nHM := fontprog.TTNumHMetrics(tables)
	font := func(entries map[string]pdf.PDFValue) pdf.PDFDict {
		entries["Subtype"] = pdf.PDFName{Value: "CIDFontType2"}
		return pdf.PDFDict{Entries: pdf.DictOf(entries)}
	}
	desc := func(set pdf.PDFValue, file pdf.PDFValue) pdf.PDFDict {
		return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"CIDSet": set, "FontFile2": file})}
	}
	bitsFor := func(from, to int) []byte {
		bits := make([]byte, to/8+1)
		for cid := from; cid < to; cid++ {
			bits[cid/8] |= 0x80 >> (cid % 8)
		}
		return bits
	}

	// Identity map: every metric-bearing CID must be listed, none past the
	// glyphs.
	identity := font(map[string]pdf.PDFValue{})
	if got := cidSetIssue(identity, desc(cidSetStream(bitsFor(1, nHM)...), ff)); got != "" {
		t.Errorf("identity, exact set: %s", got)
	}
	if nHM > 1 {
		if got := cidSetIssue(identity, desc(cidSetStream(0), ff)); !strings.Contains(got, "does not list") {
			t.Errorf("identity, empty set: %q", got)
		}
	}
	if got := cidSetIssue(identity, desc(cidSetStream(bitsFor(1, numGlyphs+8)...), ff)); !strings.Contains(got, "lists CID") {
		t.Errorf("identity, past the glyphs: %q", got)
	}

	// Stream map: CID 1 maps to glyph 0, CID 2 past the last glyph.
	gidMap := []byte{0, 0, 0, 0, 0xFF, 0xFF}
	mapped := font(map[string]pdf.PDFValue{"CIDToGIDMap": pdf.NewStreamDict(pdf.NewDict(), gidMap)})
	if got := cidSetIssue(mapped, desc(cidSetStream(0x40), ff)); got != "" {
		t.Errorf("stream map, exact set: %s", got)
	}
	if got := cidSetIssue(mapped, desc(cidSetStream(0x20), ff)); got == "" {
		t.Error("stream map, a CID past the glyphs was not reported")
	}

	bad := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "FlateDecode"}}), []byte("x"))
	for name, tc := range map[string]struct{ font, desc pdf.PDFDict }{
		"no program":          {identity, pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"CIDSet": cidSetStream(0xFF)})}},
		"undecodable program": {identity, desc(cidSetStream(0xFF), bad)},
		"unparsable program":  {identity, desc(cidSetStream(0xFF), pdf.NewStreamDict(pdf.NewDict(), []byte("x")))},
		"map not a stream":    {font(map[string]pdf.PDFValue{"CIDToGIDMap": pdf.PDFName{Value: "Other"}}), desc(cidSetStream(0xFF), ff)},
		"undecodable map":     {font(map[string]pdf.PDFValue{"CIDToGIDMap": bad}), desc(cidSetStream(0xFF), ff)},
		"other font subtype":  {pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Type1"}})}, desc(cidSetStream(0xFF), ff)},
	} {
		if got := cidSetIssue(tc.font, tc.desc); got != "" {
			t.Errorf("%s: reported %q", name, got)
		}
	}
}
