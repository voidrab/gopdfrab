package convert

import (
	"image"
	"image/color"
	"os"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

func nrgbaAt(t *testing.T, img *image.RGBA, x, y int) color.NRGBA {
	t.Helper()
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}

func TestRenderPageRectangleFill(t *testing.T) {
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte("1 0 0 rg 5 5 10 10 re f")},
	}}
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

func TestRenderPageImageXObject(t *testing.T) {
	img := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Subtype": pdf.PDFName{Value: "Image"},
			"Width":   pdf.PDFInteger(2), "Height": pdf.PDFInteger(2), "BitsPerComponent": pdf.PDFInteger(8),
			"ColorSpace": pdf.PDFName{Value: "DeviceRGB"},
		},
		HasStream: true,
		RawStream: []byte{0, 0, 255, 0, 0, 255, 0, 0, 255, 0, 0, 255},
	}
	resources := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"XObject": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Im1": img}},
	}}
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte("q 20 0 0 20 0 0 cm /Im1 Do Q")},
	}}

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
	smask := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(2), "Height": pdf.PDFInteger(2), "BitsPerComponent": pdf.PDFInteger(8),
			"ColorSpace": pdf.PDFName{Value: "DeviceGray"},
		},
		HasStream: true,
		RawStream: []byte{128, 128, 128, 128},
	}
	img := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Subtype": pdf.PDFName{Value: "Image"},
			"Width":   pdf.PDFInteger(2), "Height": pdf.PDFInteger(2), "BitsPerComponent": pdf.PDFInteger(8),
			"ColorSpace": pdf.PDFName{Value: "DeviceRGB"}, "SMask": smask,
		},
		HasStream: true,
		RawStream: []byte{255, 0, 0, 255, 0, 0, 255, 0, 0, 255, 0, 0},
	}
	resources := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"XObject": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Im1": img}},
	}}
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte("q 20 0 0 20 0 0 cm /Im1 Do Q")},
	}}

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
	_ = verify.WinAnsiToUnicode // ensure the encoding table is linked
}

// TestRenderPageOperatorCoverage drives RenderPage with a content stream that
// exercises the full operator dispatch: path construction, every fill/stroke
// paint variant, clipping, colour (gray/rgb/cmyk/named), graphics-state save/
// restore, an ExtGState, a Form XObject, and the text-positioning operators.
func TestRenderPageOperatorCoverage(t *testing.T) {
	form := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Subtype":   pdf.PDFName{Value: "Form"},
			"BBox":      pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(10), pdf.PDFInteger(10)},
			"Resources": pdf.PDFDict{Entries: map[string]pdf.PDFValue{}},
		},
		HasStream: true,
		RawStream: []byte("0 1 0 rg 0 0 5 5 re f"),
	}
	font := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type1"},
		"BaseFont": pdf.PDFName{Value: "Helvetica"},
	}}
	gs := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"ca": pdf.PDFReal(0.5), "CA": pdf.PDFReal(0.5),
	}}
	resources := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"XObject":    pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Fm1": form}},
		"Font":       pdf.PDFDict{Entries: map[string]pdf.PDFValue{"F1": font}},
		"ExtGState":  pdf.PDFDict{Entries: map[string]pdf.PDFValue{"GS1": gs}},
		"ColorSpace": pdf.PDFDict{Entries: map[string]pdf.PDFValue{}},
	}}

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
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte(content)},
	}}

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
	ttf, err := os.ReadFile("assets/fonts/LiberationSans-Regular.ttf")
	if err != nil {
		t.Skipf("font asset not available: %v", err)
	}
	return pdf.PDFDict{
		Entries:   map[string]pdf.PDFValue{"Length1": pdf.PDFInteger(len(ttf))},
		HasStream: true, RawStream: ttf,
	}
}

// TestRenderPageEmbeddedSimpleText renders text with an embedded simple
// TrueType font, exercising showText/showTextArray and the glyph-outline path.
func TestRenderPageEmbeddedSimpleText(t *testing.T) {
	ff := loadEmbeddableTTF(t)
	desc := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "FontDescriptor"}, "FontName": pdf.PDFName{Value: "LiberationSans"},
		"Flags": pdf.PDFInteger(32), "FontFile2": ff,
	}}
	widths := make(pdf.PDFArray, 95) // FirstChar 32 .. LastChar 126
	for i := range widths {
		widths[i] = pdf.PDFInteger(500)
	}
	font := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "TrueType"},
		"BaseFont": pdf.PDFName{Value: "LiberationSans"}, "Encoding": pdf.PDFName{Value: "WinAnsiEncoding"},
		"FirstChar": pdf.PDFInteger(32), "LastChar": pdf.PDFInteger(126),
		"Widths": widths, "FontDescriptor": desc,
	}}
	resources := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Font": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"F1": font}},
	}}
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{Entries: map[string]pdf.PDFValue{}, HasStream: true,
			RawStream: []byte("BT /F1 20 Tf 0 0 0 rg 5 20 Td (ABC) Tj [(D) -100 (E)] TJ ET")},
	}}
	if _, _, err := RenderPage(page, resources, [4]float64{0, 0, 120, 40}, 72); err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
}

// TestRenderPageEmbeddedCIDText renders text with an embedded Type0/CIDFontType2
// font (Identity-H), exercising the DescendantFonts / CID glyph path.
func TestRenderPageEmbeddedCIDText(t *testing.T) {
	ff := loadEmbeddableTTF(t)
	desc := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "FontDescriptor"}, "FontName": pdf.PDFName{Value: "LiberationSans"},
		"Flags": pdf.PDFInteger(32), "FontFile2": ff,
	}}
	cid := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "CIDFontType2"},
		"BaseFont": pdf.PDFName{Value: "LiberationSans"}, "FontDescriptor": desc,
		"CIDToGIDMap": pdf.PDFName{Value: "Identity"},
		"W":           pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFArray{pdf.PDFInteger(600)}},
	}}
	font := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type0"},
		"BaseFont": pdf.PDFName{Value: "LiberationSans"}, "Encoding": pdf.PDFName{Value: "Identity-H"},
		"DescendantFonts": pdf.PDFArray{cid},
	}}
	resources := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Font": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"F1": font}},
	}}
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{Entries: map[string]pdf.PDFValue{}, HasStream: true,
			RawStream: []byte("BT /F1 20 Tf 5 20 Td <00410042> Tj ET")},
	}}
	if _, _, err := RenderPage(page, resources, [4]float64{0, 0, 80, 40}, 72); err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
}

func TestResolveSimpleEncoding(t *testing.T) {
	// WinAnsi base with a Differences override.
	enc := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"BaseEncoding": pdf.PDFName{Value: "WinAnsiEncoding"},
		"Differences":  pdf.PDFArray{pdf.PDFInteger(65), pdf.PDFName{Value: "Alpha"}, pdf.PDFName{Value: "Beta"}},
	}}
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
		page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Contents": pdf.PDFArray{
				pdf.PDFDict{HasStream: true, RawStream: []byte("1 0 0 rg")},
				pdf.PDFDict{HasStream: true, RawStream: []byte("5 5 10 10 re f")},
			},
		}}
		got, err := pageContentBytes(page)
		if err != nil {
			t.Fatalf("pageContentBytes: %v", err)
		}
		want := "1 0 0 rg\n5 5 10 10 re f\n"
		if string(got) != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("single stream decode error", func(t *testing.T) {
		page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Contents": pdf.PDFDict{
				Entries:   map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "LZWDecode"}},
				HasStream: true, RawStream: []byte{0xFF, 0xFF, 0xFF},
			},
		}}
		if _, err := pageContentBytes(page); err == nil {
			t.Error("pageContentBytes: want error for undecodable stream, got nil")
		}
	})

	t.Run("stream within array decode error", func(t *testing.T) {
		page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Contents": pdf.PDFArray{
				pdf.PDFDict{
					Entries:   map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "LZWDecode"}},
					HasStream: true, RawStream: []byte{0xFF, 0xFF, 0xFF},
				},
			},
		}}
		if _, err := pageContentBytes(page); err == nil {
			t.Error("pageContentBytes: want error for undecodable stream in array, got nil")
		}
	})
}

// TestResolveOperandColorSpace covers the empty-operands, non-name-operand,
// device-name, and named-resource-lookup branches.
func TestResolveOperandColorSpace(t *testing.T) {
	resources := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"ColorSpace": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"CS0": pdf.PDFName{Value: "DeviceGray"},
		}},
	}}

	if got := resolveOperandColorSpace(nil, resources); got != nil {
		t.Errorf("empty operands = %v, want nil", got)
	}

	arrayOperand := pdf.PDFArray{pdf.PDFName{Value: "DeviceRGB"}}
	if got := resolveOperandColorSpace([]pdf.PDFValue{arrayOperand}, resources); !pdf.EqualPDFValue(got, arrayOperand) {
		t.Errorf("non-name operand = %v, want passthrough %v", got, arrayOperand)
	}

	if got := resolveOperandColorSpace([]pdf.PDFValue{pdf.PDFName{Value: "DeviceRGB"}}, resources); !pdf.EqualPDFValue(got, pdf.PDFName{Value: "DeviceRGB"}) {
		t.Errorf("device name = %v, want /DeviceRGB passthrough", got)
	}

	got := resolveOperandColorSpace([]pdf.PDFValue{pdf.PDFName{Value: "CS0"}}, resources)
	if !pdf.EqualPDFValue(got, pdf.PDFName{Value: "DeviceGray"}) {
		t.Errorf("named resource lookup = %v, want /DeviceGray", got)
	}
}

// TestExtractCFFBytes covers all three shapes: a bare CFF program passed
// through as-is, a CFF wrapped in an OpenType ("CFF " table) container, and
// data that is neither.
func TestExtractCFFBytes(t *testing.T) {
	cff := buildMinimalCIDCFF()

	if got := extractCFFBytes(cff); string(got) != string(cff) {
		t.Errorf("bare CFF passthrough = %x, want %x", got, cff)
	}

	otf := packSfnt(map[string][]byte{"CFF ": cff})
	if got := extractCFFBytes(otf); string(got) != string(cff) {
		t.Errorf("OpenType-wrapped CFF extraction = %x, want %x", got, cff)
	}

	if got := extractCFFBytes([]byte("not a font")); got != nil {
		t.Errorf("extractCFFBytes(garbage) = %x, want nil", got)
	}
}

// TestBuildSimpleFontInfoFontFile3AndFallback covers the FontFile3 (CFF)
// branch, the bundled-face fallback for a standard-encoded font with no
// embedded program, and the final "no glyph" closure when neither applies --
// the FontFile2 branch is already exercised via TestRenderPageEmbeddedSimpleText.
func TestBuildSimpleFontInfoFontFile3AndFallback(t *testing.T) {
	t.Run("FontFile3 CFF", func(t *testing.T) {
		cff := buildMinimalCIDCFF()
		desc := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"FontFile3": pdf.PDFDict{Entries: map[string]pdf.PDFValue{}, HasStream: true, RawStream: cff},
		}}
		font := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"FontDescriptor": desc}}
		fi := buildSimpleFontInfo(font)
		if _, ok := fi.glyphFor(1); !ok {
			t.Error("glyphFor(1) via FontFile3 CFF = false, want true (code-as-GID approximation)")
		}
	})

	t.Run("bundled-face fallback", func(t *testing.T) {
		font := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"BaseFont": pdf.PDFName{Value: "Helvetica"},
			"Encoding": pdf.PDFName{Value: "WinAnsiEncoding"},
		}}
		fi := buildSimpleFontInfo(font)
		if _, ok := fi.glyphFor('A'); !ok {
			t.Error("glyphFor('A') via bundled-face fallback = false, want true")
		}
	})

	t.Run("no usable program or encoding", func(t *testing.T) {
		desc := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Flags": pdf.PDFInteger(4)}} // symbolic
		font := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"FontDescriptor": desc}}
		fi := buildSimpleFontInfo(font)
		if _, ok := fi.glyphFor('A'); ok {
			t.Error("glyphFor('A') with no program/encoding = true, want false")
		}
	})
}
