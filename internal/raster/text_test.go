package raster

import (
	"encoding/binary"
	"reflect"
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

	// The Ts operator raises the baseline, which is toward smaller device y.
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
	// "H H" ends on the second H, so it shifts right by the word spacing (Tw);
	// "HH" has no space.
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

// type1Glyphs builds a Type 1 program, with no encoding of its own, holding
// one glyph called name: a 100-unit square whose lower-left corner is at
// (10, 10).
func type1Glyphs(name string) []byte {
	square := type1SquareCharstring()
	priv := []byte("dup /Private 4 dict dup begin\n/lenIV 4 def\n2 index /CharStrings 4 dict dup begin\n")
	priv = append(priv, []byte("/"+name+" "+strconv.Itoa(len(square))+" RD ")...)
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
			"FontFile": pdf.NewStreamDict(nil, type1Glyphs("sq")),
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

// TestEmbeddedType1WithoutEncoding verifies that a Type 1 font with no
// encoding in the font dictionary or the program names its codes through
// StandardEncoding.
func TestEmbeddedType1WithoutEncoding(t *testing.T) {
	font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"FontDescriptor": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"FontFile": pdf.NewStreamDict(nil, type1Glyphs("A")),
		})},
	})}
	fi := buildSimpleFontInfo(font)
	if _, ok := fi.glyphFor('A'); !ok {
		t.Error("code 65, StandardEncoding's A, has no glyph")
	}
	if _, ok := fi.glyphFor('B'); ok {
		t.Error("code 66 names a glyph the program lacks, yet produced one")
	}
}

// TestEmbeddedTrueTypeSimpleFontCodes verifies that an embedded simple
// TrueType font draws no glyph for a code outside 0..255 and draws a code no
// cmap maps as the glyph ID it equals.
func TestEmbeddedTrueTypeSimpleFontCodes(t *testing.T) {
	fonts := embeddedSimpleFontResources(t).Entries.Get("Font").(pdf.PDFDict)
	fi := buildSimpleFontInfo(fonts.Entries.Get("F1").(pdf.PDFDict))
	for _, code := range []int{-1, 256} {
		if _, ok := fi.glyphFor(code); ok {
			t.Errorf("code %d produced a glyph", code)
		}
	}

	data, err := pdf.DecodeStream(loadEmbeddableTTF(t))
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	tables, _ := fontprog.ParseSfnt(data)
	// WinAnsiEncoding gives code 5 no name, and LiberationSans has no
	// symbolic cmap to look the raw code up in.
	want, ok := glyphOutlineFromTrueType(tables, 5)
	if !ok {
		t.Fatal("LiberationSans has no glyph 5")
	}
	got, ok := fi.glyphFor(5)
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("code 5 drew %v, %v, want glyph 5 (%v)", got, ok, want)
	}
}

// TestEmbeddedType1CSimpleFont verifies that a name-keyed Type 1C font finds
// a code's glyph by the name its encoding gives it, and falls back to the
// code as glyph ID when the charset lacks that name.
func TestEmbeddedType1CSimpleFont(t *testing.T) {
	font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Encoding": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Differences": pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "B"}, pdf.PDFName{Value: "A"}},
		})},
		"FontDescriptor": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"FontFile3": pdf.NewStreamDict(nil, buildNameKeyedCFF()),
		})},
	})}
	fi := buildSimpleFontInfo(font)
	width := func(code int) float64 {
		t.Helper()
		g, ok := fi.glyphFor(code)
		if !ok {
			t.Fatalf("code %d has no glyph", code)
		}
		minX, _, maxX, _ := boundsOf(g.Contours)
		return maxX - minX
	}
	// Glyph 1 is A, 100 units wide; glyph 2 is B, 50 units wide.
	if w := width(65); w != 50 {
		t.Errorf("code 65, encoded as B, is %v units wide, want B's 50", w)
	}
	if w := width(66); w != 100 {
		t.Errorf("code 66, encoded as A, is %v units wide, want A's 100", w)
	}
	// StandardEncoding gives code 1 no name, so it is drawn as glyph 1.
	if w := width(1); w != 100 {
		t.Errorf("code 1 is %v units wide, want glyph 1's 100", w)
	}
	for _, code := range []int{-1, 256} {
		if _, ok := fi.glyphFor(code); ok {
			t.Errorf("code %d produced a glyph", code)
		}
	}
}

// buildNameKeyedCFF assembles a name-keyed CFF program with three glyphs:
// .notdef, A (a 100-unit square at (10, 10)) and B (a 50-unit one).
func buildNameKeyedCFF() []byte {
	i32 := func(v int) []byte {
		b := []byte{29, 0, 0, 0, 0}
		binary.BigEndian.PutUint32(b[1:], uint32(v))
		return b
	}
	square := func(side int) []byte {
		n := b139
		return []byte{n(10), n(10), 21, n(side), 6, n(side), 7, n(-side), 6, 14}
	}
	glyphs := [][]byte{{14}, square(100), square(50)}

	header := []byte{1, 0, 4, 1}
	nameIndex := []byte{0, 1, 1, 1, 2, 'F'}
	const topDictLen = 5 + 1 + 5 + 1 + 5 + 5 + 1 // CharStrings, charset, Private
	stringAndGsubrs := []byte{0, 0, 0, 0}
	charsetOff := len(header) + len(nameIndex) + 5 + topDictLen + len(stringAndGsubrs)
	charset := []byte{0, 0, 34, 0, 35} // format 0: SIDs of A and B
	csOff := charsetOff + len(charset)
	csIndex := []byte{0, byte(len(glyphs)), 1, 1} // count, offSize 1, offsets
	for _, g := range glyphs {
		csIndex = append(csIndex, csIndex[len(csIndex)-1]+byte(len(g)))
	}
	for _, g := range glyphs {
		csIndex = append(csIndex, g...)
	}
	privOff := csOff + len(csIndex)
	private := []byte{139, 20, 139, 21} // defaultWidthX 0, nominalWidthX 0

	topDict := append(i32(csOff), 17)
	topDict = append(topDict, i32(charsetOff)...)
	topDict = append(topDict, 15)
	topDict = append(topDict, i32(len(private))...)
	topDict = append(topDict, i32(privOff)...)
	topDict = append(topDict, 18)

	var cff []byte
	cff = append(cff, header...)
	cff = append(cff, nameIndex...)
	cff = append(cff, 0, 1, 1, 1, byte(topDictLen+1))
	cff = append(cff, topDict...)
	cff = append(cff, stringAndGsubrs...)
	cff = append(cff, charset...)
	cff = append(cff, csIndex...)
	cff = append(cff, private...)
	return cff
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
	return pdf.NewStreamDict(pdf.DictOf(nil), data)
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
		"FontFile3 that cannot decode": {"FontFile3": pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "LZWDecode"}}), []byte{0xFF, 0xFF, 0xFF})},
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
