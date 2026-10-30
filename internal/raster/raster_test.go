package raster

import (
	"encoding/binary"
	"image"
	"image/color"
	"os"
	"testing"

	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// buildMinimalCIDCFF assembles a tiny CID-keyed CFF font with 3 glyphs
// (.notdef, CID 1 width 600, CID 2 width 700), a format-0 CID charset, a
// single FDArray Font DICT (format-0 FDSelect), and a Private DICT with zero
// width defaults. Ported from verify.buildMinimalCIDCFF (internal/verify's
// test-only helper isn't importable across packages).
func buildMinimalCIDCFF() []byte {
	i32 := func(v int) []byte {
		var b [5]byte
		b[0] = 29
		binary.BigEndian.PutUint32(b[1:], uint32(v))
		return b[:]
	}

	header := []byte{0x01, 0x00, 0x04, 0x01}
	nameIndex := []byte{0x00, 0x01, 0x01, 0x01, 0x05, 'F', 'o', 'n', 't'}

	topDictLen := 6 + 6 + 2 + 7 + 7 // CharStrings, charset, ROS, FDArray, FDSelect
	topDictIndex := []byte{0x00, 0x01, 0x01, byte(1), byte(topDictLen + 1)}

	nameEnd := len(header) + len(nameIndex)
	topDictEnd := nameEnd + len(topDictIndex) + topDictLen
	stringIndex := []byte{0x00, 0x00}
	globalSubrIndex := []byte{0x00, 0x00}
	stringEnd := topDictEnd + len(stringIndex)
	subrEnd := stringEnd + len(globalSubrIndex)

	charsetOff := subrEnd
	charset := []byte{0x00, 0x00, 0x01, 0x00, 0x02} // format 0: gid1->CID1, gid2->CID2
	charsetEnd := charsetOff + len(charset)

	csOff := charsetEnd
	cs0 := []byte{0x0e}           // .notdef: endchar, no width
	cs1 := []byte{248, 236, 0x0e} // CID1: width 600, endchar
	cs2 := []byte{249, 80, 0x0e}  // CID2: width 700, endchar
	csIndex := []byte{0x00, 0x03, 0x01, 1, 2, 5, 8}
	csIndex = append(csIndex, cs0...)
	csIndex = append(csIndex, cs1...)
	csIndex = append(csIndex, cs2...)
	csEnd := csOff + len(csIndex)

	fdArrayOff := csEnd
	const fdDictLen = 11
	fdArrayIndexLen := 2 + 1 + 2 + fdDictLen
	fdArrayEnd := fdArrayOff + fdArrayIndexLen

	fdSelectOff := fdArrayEnd
	fdSelect := []byte{0x00, 0x00, 0x00, 0x00} // format 0, FD 0 for all 3 glyphs
	fdSelectEnd := fdSelectOff + len(fdSelect)

	privOff := fdSelectEnd
	fdDict := append(i32(4), i32(privOff)...)
	fdDict = append(fdDict, 18) // Private: [size offset]
	if len(fdDict) != fdDictLen {
		panic("buildMinimalCIDCFF: fd dict length mismatch")
	}
	fdArrayIndex := []byte{0x00, 0x01, 0x01, 1, byte(fdDictLen + 1)}
	fdArrayIndex = append(fdArrayIndex, fdDict...)

	privateDict := []byte{0x8b, 20, 0x8b, 21} // defaultWidthX=0, nominalWidthX=0

	topDict := append(i32(csOff), 17)
	topDict = append(topDict, i32(charsetOff)...)
	topDict = append(topDict, 15)
	topDict = append(topDict, 12, 30) // ROS
	topDict = append(topDict, i32(fdArrayOff)...)
	topDict = append(topDict, 12, 36)
	topDict = append(topDict, i32(fdSelectOff)...)
	topDict = append(topDict, 12, 37)
	if len(topDict) != topDictLen {
		panic("buildMinimalCIDCFF: top dict length mismatch")
	}

	var cff []byte
	cff = append(cff, header...)
	cff = append(cff, nameIndex...)
	cff = append(cff, topDictIndex...)
	cff = append(cff, topDict...)
	cff = append(cff, stringIndex...)
	cff = append(cff, globalSubrIndex...)
	cff = append(cff, charset...)
	cff = append(cff, csIndex...)
	cff = append(cff, fdArrayIndex...)
	cff = append(cff, fdSelect...)
	cff = append(cff, privateDict...)
	return cff
}

// type3Font builds a Type 3 font mapping code 65 ("A") to a CharProc, with
// the given /FontMatrix scale and glyph advance width.
func type3Font(proc string, matrixScale, width float64, procName string) pdf.PDFDict {
	charProcs := pdf.NewPDFDict()
	if procName != "" {
		charProcs.Entries.Set(procName, pdf.NewStreamDict(nil, []byte(proc)))
	}
	return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":       pdf.PDFName{Value: "Font"},
		"Subtype":    pdf.PDFName{Value: "Type3"},
		"FontMatrix": numArray(matrixScale, 0, 0, matrixScale, 0, 0),
		"FontBBox":   numArray(0, 0, 1000, 1000),
		"CharProcs":  charProcs,
		"Encoding": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Differences": pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "square"}},
		})},
		"FirstChar": pdf.PDFInteger(65),
		"LastChar":  pdf.PDFInteger(65),
		"Widths":    numArray(width),
	})}
}

// renderType3 draws content over a 20x20 page with font as /F1.
func renderType3(t *testing.T, content string, font pdf.PDFDict) (*image.RGBA, []string) {
	t.Helper()
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte(content)),
	})}
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Font": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"F1": font})},
	})}
	img, drops, err := RenderPage(page, resources, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	return img, drops
}

// TestRenderType3Glyph verifies that a Type 3 glyph's CharProc is executed,
// painting in the fill colour in force when the text was shown.
func TestRenderType3Glyph(t *testing.T) {
	font := type3Font("0 0 750 750 re f", 0.001, 750, "square")
	img, drops := renderType3(t, "0 0 1 rg BT /F1 20 Tf 2 2 Td (A) Tj ET", font)
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	// 750 glyph units * 0.001 * 20pt = a 15pt box at user (2,2)-(17,17),
	// which on the flipped device canvas is y 3..18.
	if got := nrgbaAt(t, img, 10, 10); got.B != 255 || got.R != 0 {
		t.Errorf("inside the glyph = %v, want the blue fill colour", got)
	}
	assertUnpainted(t, img, 19, 19, "outside the glyph")
}

// TestRenderType3Advance verifies that /Widths is in glyph space, so the
// second glyph is placed by the width scaled through the font matrix.
func TestRenderType3Advance(t *testing.T) {
	// An 8pt-wide box advancing 10pt leaves a 2pt gap between the two glyphs.
	font := type3Font("0 0 400 750 re f", 0.001, 500, "square")
	img, drops := renderType3(t, "BT /F1 20 Tf 0 2 Td (AA) Tj ET", font)
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	if got := nrgbaAt(t, img, 4, 10); got.R != 0 {
		t.Errorf("first glyph at x=4 = %v, want painted", got)
	}
	assertUnpainted(t, img, 9, 10, "the gap between glyphs")
	if got := nrgbaAt(t, img, 14, 10); got.R != 0 {
		t.Errorf("second glyph at x=14 = %v, want painted", got)
	}
}

// TestRenderType3MissingCharProcDrops verifies that a code with no glyph
// procedure is reported rather than silently leaving a hole in the text.
func TestRenderType3MissingCharProcDrops(t *testing.T) {
	tests := []struct {
		name string
		font pdf.PDFDict
	}{
		{"no CharProcs entry for the name", type3Font("0 0 750 750 re f", 0.001, 750, "")},
		{"name not in the encoding", type3Font("0 0 750 750 re f", 0.001, 750, "other")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, drops := renderType3(t, "BT /F1 20 Tf 2 2 Td (A) Tj ET", tc.font)
			if !hasDrop(drops, dropType3) {
				t.Errorf("drops = %v, want %q", drops, dropType3)
			}
		})
	}
}

// redShadingResources is a /Shading resource dict holding one flat-red axial
// shading named Sh1, used to prove which resource dict a CharProc resolved
// against: an unresolvable name would report a drop instead of painting.
func redShadingResources() pdf.PDFDict {
	return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Shading": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Sh1": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
				"ShadingType": pdf.PDFInteger(2),
				"ColorSpace":  pdf.PDFName{Value: "DeviceRGB"},
				"Coords":      numArray(0, 0, 1000, 0),
				"Function":    expFunction([]float64{1, 0, 0}, []float64{1, 0, 0}),
				"Extend":      pdf.PDFArray{pdf.PDFBoolean(true), pdf.PDFBoolean(true)},
			})},
		})},
	})}
}

// TestRenderType3GlyphUsesFontResources verifies that a CharProc draws
// through the font's own /Resources.
func TestRenderType3GlyphUsesFontResources(t *testing.T) {
	font := type3Font("/Sh1 sh", 0.001, 750, "square")
	font.Entries.Set("Resources", redShadingResources())

	img, drops := renderType3(t, "BT /F1 20 Tf 2 2 Td (A) Tj ET", font)
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	if got := nrgbaAt(t, img, 10, 10); got.R != 255 || got.G != 0 {
		t.Errorf("glyph drawn from font resources = %v, want the shading's red", got)
	}
}

// TestRenderType3GlyphFallsBackToPageResources verifies that a font declaring
// no /Resources resolves its CharProc's names against the page's (ISO 32000-1
// 9.6.5), rather than finding nothing.
func TestRenderType3GlyphFallsBackToPageResources(t *testing.T) {
	font := type3Font("/Sh1 sh", 0.001, 750, "square")
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte("BT /F1 20 Tf 2 2 Td (A) Tj ET")),
	})}
	resources := redShadingResources()
	resources.Entries.Set("Font", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"F1": font})})

	img, drops, err := RenderPage(page, resources, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if len(drops) != 0 {
		t.Errorf("drops = %v, want none", drops)
	}
	if got := nrgbaAt(t, img, 10, 10); got.R != 255 || got.G != 0 {
		t.Errorf("glyph drawn from page resources = %v, want the shading's red", got)
	}
}

func nrgbaAt(t *testing.T, img *image.RGBA, x, y int) color.NRGBA {
	t.Helper()
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}

func TestRenderPageRectangleFill(t *testing.T) {
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte("1 0 0 rg 5 5 10 10 re f")),
	})}
	canvas, _, err := RenderPage(page, pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}

	inside := nrgbaAt(t, canvas, 10, 10)
	if inside.R != 255 || inside.G != 0 || inside.B != 0 || inside.A != 255 {
		t.Errorf("inside rect pixel = %+v, want opaque red", inside)
	}

	outside := nrgbaAt(t, canvas, 1, 1)
	if outside.R != 255 || outside.G != 255 || outside.B != 255 {
		t.Errorf("outside rect pixel = %+v, want opaque white backdrop", outside)
	}
}

// TestRenderStartsInDeviceGray verifies that a drawing starts in DeviceGray,
// as specified in ISO 32000-1 table 52, so "1 sc" fills white without naming a
// colour space first.
func TestRenderStartsInDeviceGray(t *testing.T) {
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte("1 sc 5 5 10 10 re f")),
	})}
	canvas, _, err := RenderPage(page, pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if inside := nrgbaAt(t, canvas, 10, 10); inside.R != 255 || inside.G != 255 || inside.B != 255 {
		t.Errorf("inside rect pixel = %+v, want white", inside)
	}

	// The stroking half of the same state.
	page = pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte("1 SC 4 w 0 10 m 20 10 l S")),
	})}
	canvas, _, err = RenderPage(page, pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if on := nrgbaAt(t, canvas, 10, 10); on.R != 255 || on.G != 255 || on.B != 255 {
		t.Errorf("stroked pixel = %+v, want white", on)
	}
}

func TestRenderPageImageXObject(t *testing.T) {
	img := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{
		"Subtype": pdf.PDFName{Value: "Image"},
		"Width":   pdf.PDFInteger(2), "Height": pdf.PDFInteger(2), "BitsPerComponent": pdf.PDFInteger(8),
		"ColorSpace": pdf.PDFName{Value: "DeviceRGB"},
	}), []byte{0, 0, 255, 0, 0, 255, 0, 0, 255, 0, 0, 255})
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"XObject": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Im1": img})},
	})}
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte("q 20 0 0 20 0 0 cm /Im1 Do Q")),
	})}

	canvas, _, err := RenderPage(page, resources, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	center := nrgbaAt(t, canvas, 10, 10)
	if center.R != 0 || center.G != 0 || center.B != 255 || center.A != 255 {
		t.Errorf("image pixel = %+v, want opaque blue", center)
	}
}

func TestRenderPageImageWithSoftMask(t *testing.T) {
	smask := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{
		"Width": pdf.PDFInteger(2), "Height": pdf.PDFInteger(2), "BitsPerComponent": pdf.PDFInteger(8),
		"ColorSpace": pdf.PDFName{Value: "DeviceGray"},
	}), []byte{128, 128, 128, 128})
	img := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{
		"Subtype": pdf.PDFName{Value: "Image"},
		"Width":   pdf.PDFInteger(2), "Height": pdf.PDFInteger(2), "BitsPerComponent": pdf.PDFInteger(8),
		"ColorSpace": pdf.PDFName{Value: "DeviceRGB"}, "SMask": smask,
	}), []byte{255, 0, 0, 255, 0, 0, 255, 0, 0, 255, 0, 0})
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"XObject": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Im1": img})},
	})}
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte("q 20 0 0 20 0 0 cm /Im1 Do Q")),
	})}

	canvas, _, err := RenderPage(page, resources, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	center := nrgbaAt(t, canvas, 10, 10)
	if center.R != 255 {
		t.Errorf("soft-masked pixel red = %d, want 255 (unblended channel)", center.R)
	}
	if center.G < 120 || center.G > 135 {
		t.Errorf("soft-masked pixel green = %d, want ~127 (50%% blend toward white backdrop)", center.G)
	}
}

// TestRenderPageImageWithStencilMask verifies that the renderer honours a
// stencil /Mask, the form a soft mask takes after conversion to PDF/A-1, so
// that converted output can be rendered for comparison with its input.
func TestRenderPageImageWithStencilMask(t *testing.T) {
	// One bit per pixel, one byte per row: paint the left pixel, mask out the
	// right one.
	mask := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{
		"Subtype": pdf.PDFName{Value: "Image"},
		"Width":   pdf.PDFInteger(2), "Height": pdf.PDFInteger(1),
		"BitsPerComponent": pdf.PDFInteger(1), "ImageMask": pdf.PDFBoolean(true),
	}), []byte{0x40})
	img := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{
		"Subtype": pdf.PDFName{Value: "Image"},
		"Width":   pdf.PDFInteger(2), "Height": pdf.PDFInteger(1),
		"BitsPerComponent": pdf.PDFInteger(8),
		"ColorSpace":       pdf.PDFName{Value: "DeviceRGB"}, "Mask": mask,
	}), []byte{0, 0, 0, 0, 0, 0})
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"XObject": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Im1": img})},
	})}
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte("q 20 0 0 20 0 0 cm /Im1 Do Q")),
	})}

	canvas, _, err := RenderPage(page, resources, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if painted := nrgbaAt(t, canvas, 5, 10); painted.R > 20 {
		t.Errorf("the painted half = %d, want the black image", painted.R)
	}
	if through := nrgbaAt(t, canvas, 15, 10); through.R < 235 {
		t.Errorf("the masked-out half = %d, want the page showing through", through.R)
	}
}

func TestGlyphNameToWinAnsiCode(t *testing.T) {
	if c, ok := glyphNameToWinAnsiCode("A"); !ok || c != 65 {
		t.Errorf("glyphNameToWinAnsiCode(A) = %d, %v", c, ok)
	}
	if _, ok := glyphNameToWinAnsiCode("no_such_glyph"); ok {
		t.Error("glyphNameToWinAnsiCode(unknown) should be false")
	}
}

func TestSimpleCodeToGID(t *testing.T) {
	gidMap := map[uint16]uint16{'A': 10, 66: 20}
	var names [256]string
	names[65] = "A"
	if gid := simpleCodeToGID(gidMap, 65, names); gid != 10 {
		t.Errorf("simpleCodeToGID via name = %d, want 10", gid)
	}
	if gid := simpleCodeToGID(gidMap, 66, [256]string{}); gid != 20 {
		t.Errorf("simpleCodeToGID direct = %d, want 20", gid)
	}
	if gid := simpleCodeToGID(nil, 99, [256]string{}); gid != 99 {
		t.Errorf("simpleCodeToGID nil map = %d, want passthrough 99", gid)
	}
	_ = fontprog.WinAnsiToUnicode // ensure the encoding table is linked
}

// TestRenderPageOperatorCoverage drives RenderPage with a content stream that
// exercises the full operator dispatch: path construction, every fill/stroke
// paint variant, clipping, colour (gray/rgb/cmyk/named), graphics-state save/
// restore, an ExtGState, a Form XObject, and the text-positioning operators.
func TestRenderPageOperatorCoverage(t *testing.T) {
	form := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{
		"Subtype":   pdf.PDFName{Value: "Form"},
		"BBox":      pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(10), pdf.PDFInteger(10)},
		"Resources": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})},
	}), []byte("0 1 0 rg 0 0 5 5 re f"))
	font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type1"},
		"BaseFont": pdf.PDFName{Value: "Helvetica"},
	})}
	gs := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"ca": pdf.PDFReal(0.5), "CA": pdf.PDFReal(0.5),
	})}
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"XObject":    pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Fm1": form})},
		"Font":       pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"F1": font})},
		"ExtGState":  pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"GS1": gs})},
		"ColorSpace": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})},
	})}

	content := `q
3 w
0 0 1 rg 0.5 0.5 0.5 RG
1 0 0 1 2 2 cm
10 10 m 20 20 l 15 25 20 30 30 20 c 40 40 v 50 50 y h
5 5 30 30 re f
20 20 m 30 30 l S
22 22 m 32 32 l s
10 10 m 20 20 l 25 25 re B
10 10 m 20 20 l b
12 12 m 22 22 l B*
14 14 m 24 24 l b*
2 2 40 40 re W n
2 2 40 40 re W* n
0.5 g 0.6 G
0.1 0.2 0.3 0.4 k 0.4 0.3 0.2 0.1 K
/DeviceRGB cs /DeviceGray CS
0.2 0.3 0.4 sc 0.5 scn
0.3 SC 0.2 0.3 0.4 SCN
Q
q /GS1 gs 0 0 10 10 re f Q
q 1 0 0 1 5 5 cm /Fm1 Do Q
BT
/F1 12 Tf 1 Tc 2 Tw 100 Tz 14 TL
5 5 Td 2 2 TD 1 0 0 1 8 8 Tm
(Hello) Tj
[(A) -120 (B)] TJ
T*
(line) '
3 4 (quoted) "
ET
`
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte(content)),
	})}

	canvas, _, err := RenderPage(page, resources, [4]float64{0, 0, 50, 50}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if canvas == nil || canvas.Bounds().Empty() {
		t.Fatal("RenderPage returned an empty canvas")
	}
}

func loadEmbeddableTTF(t *testing.T) pdf.PDFDict {
	t.Helper()
	ttf, err := os.ReadFile("../fontsubst/fonts/LiberationSans-Regular.ttf")
	if err != nil {
		t.Skipf("font asset not available: %v", err)
	}
	return pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{"Length1": pdf.PDFInteger(len(ttf))}), ttf)
}

// TestRenderPageEmbeddedSimpleText renders text with an embedded simple
// TrueType font, exercising showText/showTextArray and the glyph-outline path.
func TestRenderPageEmbeddedSimpleText(t *testing.T) {
	resources := embeddedSimpleFontResources(t)
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{}), []byte("BT /F1 20 Tf 0 0 0 rg 5 20 Td (ABC) Tj [(D) -100 (E)] TJ ET")),
	})}
	if _, _, err := RenderPage(page, resources, [4]float64{0, 0, 120, 40}, 72); err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
}

// TestRenderTextHonoursClip verifies that glyphs paint only inside the clip in
// force, like paths do.
func TestRenderTextHonoursClip(t *testing.T) {
	resources := embeddedSimpleFontResources(t)
	const text = "BT /F1 20 Tf 0 0 0 rg 5 15 Td (ABC) Tj ET"
	free, _ := renderSized(t, 120, 40, text, resources)
	if _, _, _, maxX, _ := ink(free); maxX <= 20 {
		t.Fatalf("unclipped text ends at x %d, want past 20 for the clip to bite", maxX)
	}
	clipped, _ := renderSized(t, 120, 40, "0 0 20 40 re W n "+text, resources)
	if n, _, _, maxX, _ := ink(clipped); n == 0 || maxX >= 20 {
		t.Errorf("clipped text: %d ink pixels ending at x %d, want some, all left of 20", n, maxX)
	}
}

// embeddedSimpleFontResources is a page /Resources with an embedded TrueType
// LiberationSans, WinAnsi-encoded, as /F1.
func embeddedSimpleFontResources(t *testing.T) pdf.PDFDict {
	t.Helper()
	ff := loadEmbeddableTTF(t)
	desc := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "FontDescriptor"}, "FontName": pdf.PDFName{Value: "LiberationSans"},
		"Flags": pdf.PDFInteger(32), "FontFile2": ff,
	})}
	widths := make(pdf.PDFArray, 95) // FirstChar 32 .. LastChar 126
	for i := range widths {
		widths[i] = pdf.PDFInteger(500)
	}
	font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "TrueType"},
		"BaseFont": pdf.PDFName{Value: "LiberationSans"}, "Encoding": pdf.PDFName{Value: "WinAnsiEncoding"},
		"FirstChar": pdf.PDFInteger(32), "LastChar": pdf.PDFInteger(126),
		"Widths": widths, "FontDescriptor": desc,
	})}
	return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Font": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"F1": font})},
	})}
}

// TestRenderPageEmbeddedCIDText renders text with an embedded Type0/CIDFontType2
// font (Identity-H), exercising the DescendantFonts / CID glyph path.
func TestRenderPageEmbeddedCIDText(t *testing.T) {
	ff := loadEmbeddableTTF(t)
	desc := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "FontDescriptor"}, "FontName": pdf.PDFName{Value: "LiberationSans"},
		"Flags": pdf.PDFInteger(32), "FontFile2": ff,
	})}
	cid := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "CIDFontType2"},
		"BaseFont": pdf.PDFName{Value: "LiberationSans"}, "FontDescriptor": desc,
		"CIDToGIDMap": pdf.PDFName{Value: "Identity"},
		"W":           pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFArray{pdf.PDFInteger(600)}},
	})}
	font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type0"},
		"BaseFont": pdf.PDFName{Value: "LiberationSans"}, "Encoding": pdf.PDFName{Value: "Identity-H"},
		"DescendantFonts": pdf.PDFArray{cid},
	})}
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Font": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"F1": font})},
	})}
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{}), []byte("BT /F1 20 Tf 5 20 Td <00410042> Tj ET")),
	})}
	if _, _, err := RenderPage(page, resources, [4]float64{0, 0, 80, 40}, 72); err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
}

func TestResolveSimpleEncoding(t *testing.T) {
	// WinAnsi base with a Differences override.
	enc := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"BaseEncoding": pdf.PDFName{Value: "WinAnsiEncoding"},
		"Differences":  pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "Alpha"}, pdf.PDFName{Value: "Beta"}},
	})}
	names := resolveSimpleEncoding(enc)
	if names[65] != "Alpha" || names[66] != "Beta" {
		t.Errorf("Differences override = %q,%q; want Alpha,Beta", names[65], names[66])
	}

	// Named encoding.
	if n := resolveSimpleEncoding(pdf.PDFName{Value: "WinAnsiEncoding"}); n[65] != "A" {
		t.Errorf("WinAnsi code 65 = %q, want A", n[65])
	}
	// Default / unknown falls back to StandardEncoding.
	if n := resolveSimpleEncoding(pdf.PDFInteger(0)); n[65] != "A" {
		t.Errorf("default encoding code 65 = %q, want A", n[65])
	}
}

// TestPageContentBytesArrayAndErrors covers the /Contents-is-an-array join
// path and both DecodeStream error returns (single stream and within an
// array), which the single-stream-only RenderPage tests never exercise.
func TestPageContentBytesArrayAndErrors(t *testing.T) {
	t.Run("array of streams joined", func(t *testing.T) {
		page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Contents": pdf.PDFArray{
				pdf.NewStreamDict(nil, []byte("1 0 0 rg")),
				pdf.NewStreamDict(nil, []byte("5 5 10 10 re f")),
			},
		})}
		got, err := PageContentBytes(page)
		if err != nil {
			t.Fatalf("pageContentBytes: %v", err)
		}
		want := "1 0 0 rg\n5 5 10 10 re f\n"
		if string(got) != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("single stream decode error", func(t *testing.T) {
		page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Contents": pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "LZWDecode"}}), []byte{0xFF, 0xFF, 0xFF}),
		})}
		if _, err := PageContentBytes(page); err == nil {
			t.Error("pageContentBytes: want error for undecodable stream, got nil")
		}
	})

	t.Run("stream within array decode error", func(t *testing.T) {
		page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Contents": pdf.PDFArray{
				pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "LZWDecode"}}), []byte{0xFF, 0xFF, 0xFF}),
			},
		})}
		if _, err := PageContentBytes(page); err == nil {
			t.Error("pageContentBytes: want error for undecodable stream in array, got nil")
		}
	})
}

// TestResolveOperandColorSpace covers the empty-operands, non-name-operand,
// device-name, and named-resource-lookup branches.
func TestResolveOperandColorSpace(t *testing.T) {
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"ColorSpace": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"CS0": pdf.PDFName{Value: "DeviceGray"},
		})},
	})}

	if got := ResolveOperandColorSpace(nil, resources); got != nil {
		t.Errorf("empty operands = %v, want nil", got)
	}

	arrayOperand := pdf.PDFArray{pdf.PDFName{Value: "DeviceRGB"}}
	if got := ResolveOperandColorSpace([]pdf.PDFValue{arrayOperand}, resources); !pdf.EqualPDFValue(got, arrayOperand) {
		t.Errorf("non-name operand = %v, want passthrough %v", got, arrayOperand)
	}

	if got := ResolveOperandColorSpace([]pdf.PDFValue{pdf.PDFName{Value: "DeviceRGB"}}, resources); !pdf.EqualPDFValue(got, pdf.PDFName{Value: "DeviceRGB"}) {
		t.Errorf("device name = %v, want /DeviceRGB passthrough", got)
	}

	got := ResolveOperandColorSpace([]pdf.PDFValue{pdf.PDFName{Value: "CS0"}}, resources)
	if !pdf.EqualPDFValue(got, pdf.PDFName{Value: "DeviceGray"}) {
		t.Errorf("named resource lookup = %v, want /DeviceGray", got)
	}
}

// TestBuildSimpleFontInfoFontFile3AndFallback covers the FontFile3 (CFF)
// branch, the bundled-face fallback for a standard-encoded font with no
// embedded program, and the final "no glyph" closure when neither applies --
// the FontFile2 branch is already exercised via TestRenderPageEmbeddedSimpleText.
func TestBuildSimpleFontInfoFontFile3AndFallback(t *testing.T) {
	t.Run("FontFile3 CFF", func(t *testing.T) {
		cff := buildMinimalCIDCFF()
		desc := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"FontFile3": pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{}), cff),
		})}
		font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"FontDescriptor": desc})}
		fi := buildSimpleFontInfo(font)
		if _, ok := fi.glyphFor(1); !ok {
			t.Error("glyphFor(1) via FontFile3 CFF = false, want true (code-as-GID approximation)")
		}
	})

	t.Run("bundled-face fallback", func(t *testing.T) {
		font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"BaseFont": pdf.PDFName{Value: "Helvetica"},
			"Encoding": pdf.PDFName{Value: "WinAnsiEncoding"},
		})}
		fi := buildSimpleFontInfo(font)
		if _, ok := fi.glyphFor('A'); !ok {
			t.Error("glyphFor('A') via bundled-face fallback = false, want true")
		}
	})

	t.Run("no usable program or encoding", func(t *testing.T) {
		desc := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Flags": pdf.PDFInteger(4)})} // symbolic
		font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"FontDescriptor": desc})}
		fi := buildSimpleFontInfo(font)
		if _, ok := fi.glyphFor('A'); ok {
			t.Error("glyphFor('A') with no program/encoding = true, want false")
		}
	})
}

// TestRasterPixelBudget verifies that dpiWithin lowers the resolution for a
// page that would exceed maxRasterPixels at the requested one, such as an
// 8424 by 5955 point poster at 150 dpi, and leaves ordinary pages and
// degenerate bounds at the requested resolution.
func TestRasterPixelBudget(t *testing.T) {
	if got := dpiWithin([4]float64{0, 0, 612, 792}, 150); got != 150 {
		t.Errorf("an ordinary page dropped to %d dpi, want the 150 it asked for", got)
	}
	poster := [4]float64{0, 0, 8424, 5955}
	lowered := dpiWithin(poster, 150)
	if lowered >= 150 || lowered < 1 {
		t.Fatalf("a poster rendered at %d dpi, want something lower and usable", lowered)
	}
	w := (poster[2] - poster[0]) * float64(lowered) / 72
	h := (poster[3] - poster[1]) * float64(lowered) / 72
	if w*h > maxRasterPixels {
		t.Errorf("the lowered resolution still comes to %.0f pixels, over the %d budget", w*h, maxRasterPixels)
	}
	// Degenerate bounds are left to the caller's own guard.
	if got := dpiWithin([4]float64{0, 0, 0, 0}, 150); got != 150 {
		t.Errorf("empty bounds changed the resolution to %d", got)
	}

	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte("0 0 0 rg 0 0 8424 5955 re f")),
	})}
	canvas, _, err := RenderPage(page, pdf.PDFDict{}, poster, 150)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if px := canvas.Bounds().Dx() * canvas.Bounds().Dy(); px > maxRasterPixels {
		t.Errorf("rendered %d pixels, over the %d budget", px, maxRasterPixels)
	}
}

// TestRenderFormRendersOnNothing verifies that a form is rendered onto a
// transparent canvas, so that the pixels it does not paint remain transparent
// rather than white.
func TestRenderFormRendersOnNothing(t *testing.T) {
	formWith := func(content string) pdf.PDFDict {
		return pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{
			"BBox": pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(20), pdf.PDFInteger(20)},
		}), []byte(content))
	}

	empty, _, _, err := RenderFormContent(formWith(""), pdf.PDFDict{}, 72, InheritedPaint{})
	if err != nil {
		t.Fatalf("renderFormContent: %v", err)
	}
	if a := nrgbaAt(t, empty, 10, 10).A; a != 0 {
		t.Errorf("a form that painted nothing has alpha %d at its centre, want 0", a)
	}

	filled, _, _, err := RenderFormContent(formWith("1 0 0 rg 5 5 10 10 re f"), pdf.PDFDict{}, 72, InheritedPaint{})
	if err != nil {
		t.Fatalf("renderFormContent: %v", err)
	}
	if inside := nrgbaAt(t, filled, 10, 10); inside.A != 255 || inside.R != 255 || inside.G != 0 {
		t.Errorf("inside the fill = %+v, want opaque red", inside)
	}
	if a := nrgbaAt(t, filled, 1, 1).A; a != 0 {
		t.Errorf("beside the fill has alpha %d, want 0", a)
	}

	// A page still renders on paper: only a form composites onto something.
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte("")),
	})}
	blank, _, err := RenderPage(page, pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if px := nrgbaAt(t, blank, 10, 10); px.A != 255 || px.R != 255 {
		t.Errorf("an empty page renders %+v, want opaque white", px)
	}
}

func TestType3WordSpaceAppliesOnlyToCode32(t *testing.T) {
	gs := &renderState{wordSpace: 7}
	if got := type3WordSpace(32, gs); got != 7 {
		t.Errorf("code 32 word space = %v, want 7", got)
	}
	if got := type3WordSpace(65, gs); got != 0 {
		t.Errorf("code 65 word space = %v, want 0", got)
	}
}

func TestBuildType3FontInfoWidthsDefaultFirstChar(t *testing.T) {
	font := type3Font("0 0 m", 0.001, 500, "a")
	font.Entries.Del("FirstChar")
	fi := buildType3FontInfo(font)
	if got := fi.widths[0]; got != 500 {
		t.Errorf("width of code 0 = %v, want 500 (FirstChar defaults to 0)", got)
	}
}

// TestRenderType3GlyphThatShowsItselfStops verifies that a CharProc that shows
// its own font does not recurse forever: the shared nesting guard ends it,
// leaving the outer glyph painted.
func TestRenderType3GlyphThatShowsItselfStops(t *testing.T) {
	font := type3Font("0 0 750 750 re f BT /F1 1000 Tf (A) Tj ET", 0.001, 750, "square")
	img, _ := renderType3(t, "0 0 1 rg BT /F1 20 Tf 2 2 Td (A) Tj ET", font)
	if got := nrgbaAt(t, img, 10, 10); got.B != 255 || got.R != 0 {
		t.Errorf("inside the outer glyph = %v, want the blue fill colour", got)
	}
}

// TestRenderType3UndecodableCharProcDrops verifies that a glyph procedure
// whose stream cannot be decoded is reported like a missing one.
func TestRenderType3UndecodableCharProcDrops(t *testing.T) {
	font := type3Font("", 0.001, 750, "square")
	proc := undecodableStream()
	font.Entries.Get("CharProcs").(pdf.PDFDict).Entries.Set("square", proc)
	_, drops := renderType3(t, "BT /F1 20 Tf 2 2 Td (A) Tj ET", font)
	if !hasDrop(drops, dropType3) {
		t.Errorf("drops = %v, want %q", drops, dropType3)
	}
}
