package raster

import (
	"strconv"
	"testing"

	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// helvetica is a non-embedded Helvetica, drawn from the bundled faces, with
// every code 32..126 500 units wide.
func helvetica(extra map[string]pdf.PDFValue) pdf.PDFDict {
	widths := make(pdf.PDFArray, 95)
	for i := range widths {
		widths[i] = pdf.PDFInteger(500)
	}
	entries := map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type1"},
		"BaseFont": pdf.PDFName{Value: "Helvetica"}, "Encoding": pdf.PDFName{Value: "WinAnsiEncoding"},
		"FirstChar": pdf.PDFInteger(32), "LastChar": pdf.PDFInteger(126), "Widths": widths,
	}
	for k, v := range extra {
		entries[k] = v
	}
	return pdf.PDFDict{Entries: pdf.DictOf(entries)}
}

func fontResources(font pdf.PDFDict) pdf.PDFDict {
	return resourcesWith("Font", map[string]pdf.PDFValue{"F1": font})
}

// text renders one text object on a 60x30 page.
func text(t *testing.T, body string) (n, minX, minY, maxX, maxY int) {
	t.Helper()
	img, _ := renderSized(t, 60, 30, "0 0 0 rg BT "+body+" ET", fontResources(helvetica(nil)))
	return ink(img)
}

func TestTextPaintsAndPositions(t *testing.T) {
	n, _, _, _, baseMaxY := text(t, "/F1 12 Tf 5 5 Td (H) Tj")
	if n == 0 {
		t.Fatal("a shown glyph left no ink")
	}

	// Ts raises the baseline, which is toward smaller device y.
	_, _, _, _, raisedMaxY := text(t, "/F1 12 Tf 5 5 Td 10 Ts (H) Tj")
	if got := baseMaxY - raisedMaxY; got < 9 || got > 11 {
		t.Errorf("10 Ts moved the glyph up by %d pixels, want about 10", got)
	}

	// Modes 3 (invisible) and 7 (clip) paint no marks; the others do.
	for mode, wantInk := range map[string]bool{"0": true, "1": true, "3": false, "7": false} {
		if got, _, _, _, _ := text(t, mode+" Tr /F1 12 Tf 5 5 Td (H) Tj"); (got > 0) != wantInk {
			t.Errorf("Tr %s: %d pixels of ink, want ink=%v", mode, got, wantInk)
		}
	}
}

func TestTextWordSpacingAppliesToSpaceOnly(t *testing.T) {
	// "H H" ends on the second H, so it shifts right by Tw; "HH" has no space.
	_, _, _, plain, _ := text(t, "/F1 10 Tf 5 5 Td (H H) Tj")
	_, _, _, spaced, _ := text(t, "/F1 10 Tf 5 5 Td 6 Tw (H H) Tj")
	if got := spaced - plain; got < 5 || got > 7 {
		t.Errorf("6 Tw moved the second word by %d pixels, want about 6", got)
	}
	_, _, _, plainNoSpace, _ := text(t, "/F1 10 Tf 5 5 Td (HH) Tj")
	_, _, _, spacedNoSpace, _ := text(t, "/F1 10 Tf 5 5 Td 6 Tw (HH) Tj")
	if plainNoSpace != spacedNoSpace {
		t.Errorf("Tw moved text that has no space: %d vs %d", plainNoSpace, spacedNoSpace)
	}
}

func TestTextOperatorsWithUnusableOperandsPaintNothing(t *testing.T) {
	for name, body := range map[string]string{
		"Tf without a size":      "/F1 Tf 5 5 Td (H) Tj",
		"Tf with a numeric font": "5 12 Tf 5 5 Td (H) Tj",
		"Tf of an unknown font":  "/Nope 12 Tf 5 5 Td (H) Tj",
		"no Tf at all":           "5 5 Td (H) Tj",
		"empty string":           "/F1 12 Tf 5 5 Td () Tj",
		"TJ without operands":    "/F1 12 Tf 5 5 Td TJ",
		"TJ of a non-array":      "/F1 12 Tf 5 5 Td (H) TJ",
	} {
		if n, _, _, _, _ := text(t, body); n != 0 {
			t.Errorf("%s: %d pixels of ink", name, n)
		}
	}
}

func TestTJShowsHexAndLiteralStringsAlike(t *testing.T) {
	_, aMinX, _, aMaxX, _ := text(t, "/F1 12 Tf 5 5 Td [(H) -500 (H)] TJ")
	n, bMinX, _, bMaxX, _ := text(t, "/F1 12 Tf 5 5 Td [<48> -500 <48>] TJ")
	if n == 0 || aMinX != bMinX || aMaxX != bMaxX {
		t.Errorf("hex TJ spans %d..%d, literal spans %d..%d; want identical", bMinX, bMaxX, aMinX, aMaxX)
	}
}

func TestSimpleFontMissingWidth(t *testing.T) {
	noWidths := func(missing int64) *fontInfo {
		font := helvetica(map[string]pdf.PDFValue{
			"Widths": pdf.PDFArray{},
			"FontDescriptor": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
				"MissingWidth": pdf.PDFInteger(missing),
			})},
		})
		return buildSimpleFontInfo(font)
	}
	if got := noWidths(600).defaultWidth; got != 600 {
		t.Errorf("defaultWidth = %v, want the descriptor's MissingWidth 600", got)
	}
}

func TestResolveSimpleEncodingDictWithoutBase(t *testing.T) {
	enc := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Differences": pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFName{Value: "Alpha"}},
	})}
	names := resolveSimpleEncoding(enc)
	if names[1] != "Alpha" || names[65] != fontprog.StandardEncoding[65] {
		t.Errorf("names[1]=%q names[65]=%q, want Alpha over StandardEncoding", names[1], names[65])
	}
}

func TestSimpleCodeToGIDSymbolicCmap(t *testing.T) {
	var names [256]string
	// A (3,0) symbol cmap maps codes into the F000 private-use block.
	if got := simpleCodeToGID(map[uint16]uint16{0xF041: 7}, 0x41, names); got != 7 {
		t.Errorf("gid = %d, want 7 via the 0xF000 offset", got)
	}
}

// type1Glyphs builds a Type 1 program with one glyph, "sq", a 100-unit square
// whose lower-left corner is at (10, 10).
func type1Glyphs() []byte {
	square := type1SquareCharstring()
	priv := []byte("dup /Private 4 dict dup begin\n/lenIV 4 def\n2 index /CharStrings 4 dict dup begin\n")
	priv = append(priv, []byte("/sq "+strconv.Itoa(len(square))+" RD ")...)
	priv = append(priv, square...)
	priv = append(priv, []byte(" ND\nend\n")...)
	return type1Font(priv)
}

func TestEmbeddedType1SimpleFont(t *testing.T) {
	font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type1"},
		"Encoding": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Differences": pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "sq"}},
		})},
		"FirstChar": pdf.PDFInteger(65), "Widths": numArray(500),
		"FontDescriptor": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"FontFile": pdf.PDFDict{HasStream: true, RawStream: type1Glyphs()},
		})},
	})}
	draw := func(codes string) int {
		img, _ := renderSized(t, 60, 30, "0 0 0 rg BT /F1 100 Tf 5 5 Td "+codes+" Tj ET", fontResources(font))
		n, _, _, _, _ := ink(img)
		return n
	}
	if n := draw("(A)"); n < 50 {
		t.Errorf("the mapped glyph painted %d pixels, want a filled 10x10 square", n)
	}
	// Code 0 has no glyph name and code B names a glyph the program lacks.
	if n := draw("<00>"); n != 0 {
		t.Errorf("a code with no glyph name painted %d pixels", n)
	}
	if n := draw("(B)"); n != 0 {
		t.Errorf("a code naming an absent glyph painted %d pixels", n)
	}
}

func TestBundledFaceFallbackGaps(t *testing.T) {
	font := helvetica(map[string]pdf.PDFValue{
		"Encoding": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"BaseEncoding": pdf.PDFName{Value: "WinAnsiEncoding"},
			"Differences":  pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "uniE000"}},
		})},
	})
	fi := buildSimpleFontInfo(font)
	if _, ok := fi.glyphFor('B'); !ok {
		t.Fatal("an ordinary code has no glyph")
	}
	if _, ok := fi.glyphFor(0); ok {
		t.Error("code 0, which has no meaning, produced a glyph")
	}
	if _, ok := fi.glyphFor(65); ok {
		t.Error("a private-use code point absent from every bundled face produced a glyph")
	}
}

func TestResolveSimpleEncodingUnlistedNameIsStandard(t *testing.T) {
	// Only WinAnsiEncoding is told apart; any other name, MacRomanEncoding
	// included, resolves to StandardEncoding.
	for _, name := range []string{"MacRomanEncoding", "StandardEncoding", "Whatever"} {
		names := resolveSimpleEncoding(pdf.PDFName{Value: name})
		if names != fontprog.StandardEncoding {
			t.Errorf("%s did not resolve to StandardEncoding", name)
		}
	}
}

// cidFont builds a Type0 font over a CIDFontType2/0 descendant with the given
// descendant entries added to a FontDescriptor holding descriptor entries.
func cidFont(desc, descriptor map[string]pdf.PDFValue) pdf.PDFDict {
	descEntries := map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "CIDFontType2"},
		"BaseFont": pdf.PDFName{Value: "X"},
	}
	for k, v := range desc {
		descEntries[k] = v
	}
	if descriptor != nil {
		descEntries["FontDescriptor"] = pdf.PDFDict{Entries: pdf.DictOf(descriptor)}
	}
	return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type0"},
		"Encoding":        pdf.PDFName{Value: "Identity-H"},
		"DescendantFonts": pdf.PDFArray{pdf.PDFDict{Entries: pdf.DictOf(descEntries)}},
	})}
}

func streamOf(data []byte) pdf.PDFDict {
	return pdf.PDFDict{Entries: pdf.DictOf(nil), HasStream: true, RawStream: data}
}

func TestCompositeFontDefaultWidthAndCIDToGIDStream(t *testing.T) {
	ttf := loadEmbeddableTTF(t)
	tables, gidA := loadLiberationSansGID(t, 'A')
	// Map CID 0 to the glyph for 'A'; every CID past the table's end falls back
	// to glyph 0.
	cidToGID := []byte{byte(gidA >> 8), byte(gidA)}
	font := cidFont(
		map[string]pdf.PDFValue{"DW": pdf.PDFInteger(750), "CIDToGIDMap": streamOf(cidToGID)},
		map[string]pdf.PDFValue{"FontFile2": ttf},
	)
	fi := buildCompositeFontInfo(font.Entries.Get("DescendantFonts").(pdf.PDFArray)[0].(pdf.PDFDict))
	if fi.defaultWidth != 750 {
		t.Errorf("defaultWidth = %v, want the DW 750", fi.defaultWidth)
	}
	want, _ := glyphOutlineFromTrueType(tables, gidA)
	got, ok := fi.glyphFor(0)
	if !ok || len(got.Contours) == 0 || len(got.Contours) != len(want.Contours) {
		t.Errorf("CID 0 drew %d contours (ok=%v), want the %d of glyph %d", len(got.Contours), ok, len(want.Contours), gidA)
	}
	notdef, _ := glyphOutlineFromTrueType(tables, 0)
	got, _ = fi.glyphFor(500)
	if len(got.Contours) != len(notdef.Contours) {
		t.Errorf("a CID past the map drew %d contours, want glyph 0's %d", len(got.Contours), len(notdef.Contours))
	}
}

func TestCompositeFontProgramSources(t *testing.T) {
	descendant := func(font pdf.PDFDict) pdf.PDFDict {
		return font.Entries.Get("DescendantFonts").(pdf.PDFArray)[0].(pdf.PDFDict)
	}
	cff := buildMinimalCIDCFF()

	t.Run("CFF in FontFile3", func(t *testing.T) {
		fi := buildCompositeFontInfo(descendant(cidFont(nil, map[string]pdf.PDFValue{"FontFile3": streamOf(cff)})))
		if _, ok := fi.glyphFor(1); !ok {
			t.Error("CID 1 has no outline from the CFF program")
		}
		if _, ok := fi.glyphFor(99); ok {
			t.Error("a CID past the CFF's glyphs produced an outline")
		}
	})

	for name, descriptor := range map[string]map[string]pdf.PDFValue{
		"no descriptor":                nil,
		"descriptor without a program": {},
		"FontFile3 that is not a font": {"FontFile3": streamOf([]byte("not a font"))},
		"FontFile3 that cannot decode": {"FontFile3": pdf.PDFDict{
			Entries:   pdf.DictOf(map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "LZWDecode"}}),
			HasStream: true, RawStream: []byte{0xFF, 0xFF, 0xFF},
		}},
		"FontFile2 that is not a font": {"FontFile2": streamOf([]byte("not a font"))},
	} {
		t.Run(name, func(t *testing.T) {
			fi := buildCompositeFontInfo(descendant(cidFont(nil, descriptor)))
			if _, ok := fi.glyphFor(1); ok {
				t.Error("a font with no usable program produced an outline")
			}
		})
	}
}

func TestCompositeFontIgnoresTrailingHalfCode(t *testing.T) {
	ttf := loadEmbeddableTTF(t)
	font := cidFont(nil, map[string]pdf.PDFValue{"FontFile2": ttf})
	draw := func(codes string) int {
		img, _ := renderSized(t, 60, 30, "0 0 0 rg BT /F1 20 Tf 5 5 Td "+codes+" Tj ET", fontResources(font))
		n, _, _, _, _ := ink(img)
		return n
	}
	// CID 0x24 is a letter in Liberation Sans's glyph order, and the stray
	// third byte cannot form a second code.
	whole := draw("<0024>")
	if whole == 0 {
		t.Fatal("the two-byte code painted nothing")
	}
	if got := draw("<002400>"); got != whole {
		t.Errorf("a trailing half code changed the output: %d vs %d pixels", got, whole)
	}
}
