package convert

import (
	"bytes"
	"compress/zlib"
	"image"
	"image/color"
	"os"
	"slices"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/raster"
)

// The rasterizer's own tests live in internal/raster. These few stay here
// because they measure a render with the fidelity gate's inkFraction, drive
// flattenPageToImage, or build streams with this package's test encoders.

func nrgbaAt(t *testing.T, img *image.RGBA, x, y int) color.NRGBA {
	t.Helper()
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}

func loadEmbeddableTTF(t *testing.T) pdf.PDFDict {
	t.Helper()
	ttf, err := os.ReadFile("../fontsubst/fonts/LiberationSans-Regular.ttf")
	if err != nil {
		t.Skipf("font asset not available: %v", err)
	}
	return pdf.PDFDict{
		Entries:   pdf.DictOf(map[string]pdf.PDFValue{"Length1": pdf.PDFInteger(len(ttf))}),
		HasStream: true, RawStream: ttf,
	}
}

func hasDrop(drops []string, want string) bool {
	return slices.Contains(drops, want)
}

// TestRasterInvisibleTextRenderMode: text drawn under render mode 3 (invisible,
// the OCR-layer case) must paint nothing, while the same text at mode 0 does.
func TestRasterInvisibleTextRenderMode(t *testing.T) {
	ff := loadEmbeddableTTF(t)
	desc := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "FontDescriptor"}, "FontName": pdf.PDFName{Value: "LiberationSans"},
		"Flags": pdf.PDFInteger(32), "FontFile2": ff,
	})}
	widths := make(pdf.PDFArray, 95)
	for i := range widths {
		widths[i] = pdf.PDFInteger(500)
	}
	font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "TrueType"},
		"BaseFont": pdf.PDFName{Value: "LiberationSans"}, "Encoding": pdf.PDFName{Value: "WinAnsiEncoding"},
		"FirstChar": pdf.PDFInteger(32), "LastChar": pdf.PDFInteger(126),
		"Widths": widths, "FontDescriptor": desc,
	})}
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Font": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"F1": font})},
	})}
	render := func(tr string) *image.RGBA {
		page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Contents": pdf.PDFDict{HasStream: true,
				RawStream: []byte("BT /F1 20 Tf 0 0 0 rg " + tr + " 5 15 Td (ABC) Tj ET")},
		})}
		img, _, err := raster.RenderPage(page, resources, [4]float64{0, 0, 120, 40}, 72)
		if err != nil {
			t.Fatalf("RenderPage: %v", err)
		}
		return img
	}

	if inkFraction(render("0 Tr")) < inkThreshold {
		t.Fatal("mode 0 text painted no ink; the test cannot distinguish invisibility")
	}
	if got := inkFraction(render("3 Tr")); got >= inkThreshold {
		t.Errorf("mode 3 (invisible) text painted ink fraction %.4f, want ~0", got)
	}
}

// TestFlattenPageReportsDrops: flattening a page whose shading cannot be drawn
// records the dropped feature, which rasterBackstop surfaces as
// ConvertResult.RasterDrops.
func TestFlattenPageReportsDrops(t *testing.T) {
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte("q /Sh1 sh Q\n0 0 0 rg 2 2 5 5 re f")},
	})}
	drops, changed := flattenPageToImage(page, pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72)
	if !changed {
		t.Fatal("flattenPageToImage did not flatten the page")
	}
	if !hasDrop(drops, raster.DropShading) {
		t.Errorf("flatten drops = %v, want %q", drops, raster.DropShading)
	}
}

// TestCCITTEncodedBytes covers the decode chain's handling of ASCII filters
// preceding CCITTFaxDecode: the chain stops at the image codec and hands back
// its input with the wrappers already undone.
func TestCCITTEncodedBytes(t *testing.T) {
	payload := []byte{0x98, 0x01}

	for _, tc := range []struct {
		name    string
		filter  string
		encoded []byte
	}{
		{"ASCIIHexDecode prefilter", "ASCIIHexDecode", encodeASCIIHex(payload)},
		{"ASCII85Decode prefilter", "ASCII85Decode", encodeASCII85(payload)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dict := pdf.PDFDict{
				Entries:   pdf.DictOf(map[string]pdf.PDFValue{"Filter": pdf.PDFArray{pdf.PDFName{Value: tc.filter}, pdf.PDFName{Value: "CCITTFaxDecode"}}}),
				HasStream: true,
				RawStream: tc.encoded,
			}
			s, err := pdf.DecodeStreamFull(dict, pdf.ImageDecodeOptions(dict))
			if err != nil {
				t.Fatalf("DecodeStreamFull: %v", err)
			}
			if !s.IsImage() || s.Image.Kind != pdf.FilterCCITT {
				t.Fatalf("image = %+v, want CCITTFaxDecode", s.Image)
			}
			if !bytes.Equal(s.Data, payload) {
				t.Errorf("got %x, want %x", s.Data, payload)
			}
		})
	}

	t.Run("broken filter before CCITT", func(t *testing.T) {
		dict := pdf.PDFDict{
			Entries:   pdf.DictOf(map[string]pdf.PDFValue{"Filter": pdf.PDFArray{pdf.PDFName{Value: "FlateDecode"}, pdf.PDFName{Value: "CCITTFaxDecode"}}}),
			HasStream: true,
			RawStream: payload, // not valid zlib
		}
		if _, err := pdf.DecodeStreamFull(dict, pdf.ImageDecodeOptions(dict)); err == nil {
			t.Error("want error for an undecodable pre-filter, got nil")
		}
	})
}

// TestImageDecodeChainLZWAndPredictors covers the LZW-filter branch and the
// TIFF (predictor 2) / PNG (predictor >=10) predictor-undo paths, none of
// which the colour-space-focused DecodeImageRGBA tests exercise. Image
// streams take their predictor Columns/BitsPerComponent defaults from the
// image dict, which is what ImageDecodeOptions supplies.
func TestImageDecodeChainLZWAndPredictors(t *testing.T) {
	decode := func(t *testing.T, dict pdf.PDFDict) []byte {
		t.Helper()
		s, err := pdf.DecodeStreamFull(dict, pdf.ImageDecodeOptions(dict))
		if err != nil {
			t.Fatalf("DecodeStreamFull: %v", err)
		}
		return s.Data
	}
	// Only Flate and LZW take a /Predictor (ISO 32000-1 Table 8), so the
	// predictor cases ride on a real Flate stage rather than bare bytes.
	flated := func(t *testing.T, raw []byte) []byte {
		t.Helper()
		var buf bytes.Buffer
		zw := zlib.NewWriter(&buf)
		if _, err := zw.Write(raw); err != nil {
			t.Fatalf("zlib Write: %v", err)
		}
		if err := zw.Close(); err != nil {
			t.Fatalf("zlib Close: %v", err)
		}
		return buf.Bytes()
	}

	t.Run("LZWDecode filter", func(t *testing.T) {
		want := []byte{1, 2, 3, 4, 5, 6}
		dict := pdf.PDFDict{
			Entries:   pdf.DictOf(map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "LZWDecode"}}),
			HasStream: true,
			RawStream: encodeLZW(t, want),
		}
		if got := decode(t, dict); !bytes.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	// Regression: the old image path had a hasLZW flag that short-circuited
	// the whole chain, feeding still-ASCII85-encoded text straight to the LZW
	// decoder. The unified chain applies filters in order.
	t.Run("ASCII85 then LZW", func(t *testing.T) {
		want := []byte{1, 2, 3, 4, 5, 6}
		dict := pdf.PDFDict{
			Entries: pdf.DictOf(map[string]pdf.PDFValue{"Filter": pdf.PDFArray{
				pdf.PDFName{Value: "ASCII85Decode"}, pdf.PDFName{Value: "LZWDecode"},
			}}),
			HasStream: true,
			RawStream: encodeASCII85(encodeLZW(t, want)),
		}
		if got := decode(t, dict); !bytes.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("TIFF predictor", func(t *testing.T) {
		// 2 columns, 1 colour, 8bpc: row [10, 5] TIFF-delta-encoded is [10, 5]
		// itself (each sample is a delta from the previous, first is raw).
		dict := pdf.PDFDict{
			Entries: pdf.DictOf(map[string]pdf.PDFValue{
				"Width":  pdf.PDFInteger(2),
				"Filter": pdf.PDFName{Value: "FlateDecode"},
				"DecodeParms": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
					"Predictor": pdf.PDFInteger(2), "Columns": pdf.PDFInteger(2), "Colors": pdf.PDFInteger(1), "BitsPerComponent": pdf.PDFInteger(8),
				})},
			}),
			HasStream: true,
			RawStream: flated(t, []byte{10, 5}),
		}
		if got, want := decode(t, dict), []byte{10, 15}; !bytes.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("PNG predictor", func(t *testing.T) {
		// PNG predictor 12 (up): a 1-byte-per-pixel filter tag (0=None)
		// followed by the raw row, decodes to the row unchanged.
		dict := pdf.PDFDict{
			Entries: pdf.DictOf(map[string]pdf.PDFValue{
				"Width":  pdf.PDFInteger(2),
				"Filter": pdf.PDFName{Value: "FlateDecode"},
				"DecodeParms": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
					"Predictor": pdf.PDFInteger(12), "Columns": pdf.PDFInteger(2), "Colors": pdf.PDFInteger(1), "BitsPerComponent": pdf.PDFInteger(8),
				})},
			}),
			HasStream: true,
			RawStream: flated(t, []byte{0x00, 7, 9}),
		}
		if got, want := decode(t, dict), []byte{7, 9}; !bytes.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

func numArray(v ...float64) pdf.PDFArray {
	arr := make(pdf.PDFArray, len(v))
	for i, f := range v {
		arr[i] = pdf.PDFReal(f)
	}
	return arr
}

// type3Font builds a Type 3 font mapping code 65 ("A") to a CharProc, with
// the given /FontMatrix scale and glyph advance width.
func type3Font(proc string, matrixScale, width float64, procName string) pdf.PDFDict {
	charProcs := pdf.NewPDFDict()
	if procName != "" {
		charProcs.Entries.Set(procName, pdf.PDFDict{HasStream: true, RawStream: []byte(proc)})
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
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte(content)},
	})}
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Font": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"F1": font})},
	})}
	img, drops, err := raster.RenderPage(page, resources, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	return img, drops
}

// TestRenderType3FontMatrix: the glyph is scaled by /FontMatrix, not by the
// 1000-unit em every other font type uses.
func TestRenderType3FontMatrix(t *testing.T) {
	// A tenth of the coordinates with ten times the matrix scale must paint
	// the same 15pt box.
	coarse := type3Font("0 0 75 75 re f", 0.01, 75, "square")
	fine := type3Font("0 0 750 750 re f", 0.001, 750, "square")

	content := "BT /F1 20 Tf 2 2 Td (A) Tj ET"
	a, _ := renderType3(t, content, coarse)
	b, _ := renderType3(t, content, fine)
	for _, p := range [][2]int{{10, 10}, {4, 16}, {16, 4}, {19, 19}} {
		if nrgbaAt(t, a, p[0], p[1]) != nrgbaAt(t, b, p[0], p[1]) {
			t.Errorf("at (%d,%d) the FontMatrix-scaled glyph differs: %v vs %v",
				p[0], p[1], nrgbaAt(t, a, p[0], p[1]), nrgbaAt(t, b, p[0], p[1]))
		}
	}
	// Guard against both renders being blank, which would pass vacuously.
	if inkFraction(a) < inkThreshold {
		t.Fatal("the glyph painted no ink; the comparison proves nothing")
	}
}

// TestRenderType3Invisible: render mode 3 must not execute the CharProc, the
// same OCR-layer guarantee outline fonts already have.
func TestRenderType3Invisible(t *testing.T) {
	font := type3Font("0 0 750 750 re f", 0.001, 750, "square")
	visible, _ := renderType3(t, "BT /F1 20 Tf 0 Tr 2 2 Td (A) Tj ET", font)
	if inkFraction(visible) < inkThreshold {
		t.Fatal("mode 0 painted no ink; the test cannot distinguish invisibility")
	}
	hidden, _ := renderType3(t, "BT /F1 20 Tf 3 Tr 2 2 Td (A) Tj ET", font)
	if got := inkFraction(hidden); got >= inkThreshold {
		t.Errorf("mode 3 (invisible) painted ink fraction %.4f, want ~0", got)
	}
}
