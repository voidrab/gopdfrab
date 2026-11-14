package raster

import (
	"bytes"
	"errors"
	"image"
	"math/rand"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/content"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

func TestDecodeImageRGBARejectsInvalidDimensions(t *testing.T) {
	for _, tc := range []struct{ w, h int64 }{{0, 4}, {4, 0}, {-1, 4}} {
		dict := imageDict(map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(tc.w), "Height": pdf.PDFInteger(tc.h),
			"BitsPerComponent": pdf.PDFInteger(8), "ColorSpace": pdf.PDFName{Value: "DeviceGray"},
		}, []byte{0})
		if _, err := DecodeImageRGBA(dict, pdf.PDFDict{}); err != errInvalidImageDims {
			t.Errorf("%dx%d: err = %v, want errInvalidImageDims", tc.w, tc.h, err)
		}
	}
}

// TestDecodeImageRGBAUndecodableCodecsPlaceholder verifies that a codec with
// no decoder comes back as the placeholder and a PlaceholderError naming the
// codec, so the gray is paintable but never mistaken for the picture.
func TestDecodeImageRGBAUndecodableCodecsPlaceholder(t *testing.T) {
	for filter, codec := range map[string]string{"JBIG2Decode": "JBIG2", "JPXDecode": "JPEG 2000"} {
		t.Run(filter, func(t *testing.T) {
			dict := imageDict(map[string]pdf.PDFValue{
				"Width": pdf.PDFInteger(2), "Height": pdf.PDFInteger(2),
				"BitsPerComponent": pdf.PDFInteger(1), "ColorSpace": pdf.PDFName{Value: "DeviceGray"},
				"Filter": pdf.PDFName{Value: filter},
			}, []byte{0})
			img, err := DecodeImageRGBA(dict, pdf.PDFDict{})
			var ph *PlaceholderError
			if !errors.As(err, &ph) || ph.Codec != codec {
				t.Fatalf("DecodeImageRGBA err = %v, want a PlaceholderError for %s", err, codec)
			}
			if !strings.Contains(err.Error(), codec) {
				t.Errorf("error %q does not name the codec %s", err, codec)
			}
			if r, g, b := pixelAt(t, img, 0, 0); r != 127 || g != 127 || b != 127 {
				t.Errorf("pixel = (%d,%d,%d), want the mid-gray placeholder", r, g, b)
			}
		})
	}
}

func TestDecodeImageRGBACorruptJPEGFails(t *testing.T) {
	dict := imageDict(map[string]pdf.PDFValue{
		"Width": pdf.PDFInteger(4), "Height": pdf.PDFInteger(4),
		"BitsPerComponent": pdf.PDFInteger(8), "ColorSpace": pdf.PDFName{Value: "DeviceRGB"},
		"Filter": pdf.PDFName{Value: "DCTDecode"},
	}, []byte("not a jpeg"))
	if _, err := DecodeImageRGBA(dict, pdf.PDFDict{}); err == nil {
		t.Error("corrupt JPEG data decoded without error")
	}
}

// TestDecodeImageRGBAShortSamplesFallBack covers 8-bit gray data too short for
// the direct copy: the general loop reads the missing samples as zero.
func TestDecodeImageRGBAShortSamplesFallBack(t *testing.T) {
	dict := imageDict(map[string]pdf.PDFValue{
		"Width": pdf.PDFInteger(2), "Height": pdf.PDFInteger(2),
		"BitsPerComponent": pdf.PDFInteger(8), "ColorSpace": pdf.PDFName{Value: "DeviceGray"},
	}, []byte{0xFF, 0xFF})
	img, err := DecodeImageRGBA(dict, pdf.PDFDict{})
	if err != nil {
		t.Fatalf("DecodeImageRGBA: %v", err)
	}
	if r, _, _ := pixelAt(t, img, 1, 0); r != 255 {
		t.Errorf("present sample = %d, want 255", r)
	}
	if r, _, _ := pixelAt(t, img, 0, 1); r != 0 {
		t.Errorf("missing sample = %d, want 0", r)
	}
}

// TestDecodeImageRGBAIndexed1BitUsesPalette covers an Indexed image whose
// 1-bit /Decode is the identity but whose colour space is not a device one, so
// the direct bit-depth unpacker declines it.
func TestDecodeImageRGBAIndexed1BitUsesPalette(t *testing.T) {
	dict := imageDict(map[string]pdf.PDFValue{
		"Width": pdf.PDFInteger(2), "Height": pdf.PDFInteger(1),
		"BitsPerComponent": pdf.PDFInteger(1),
		"ColorSpace": pdf.PDFArray{
			pdf.PDFName{Value: "Indexed"}, pdf.PDFName{Value: "DeviceRGB"}, pdf.PDFInteger(1),
			pdf.PDFString{Value: "\xff\x00\x00\x00\x00\xff"},
		},
	}, []byte{0b01000000})
	img, err := DecodeImageRGBA(dict, pdf.PDFDict{})
	if err != nil {
		t.Fatalf("DecodeImageRGBA: %v", err)
	}
	if r, g, b := pixelAt(t, img, 0, 0); r != 255 || g != 0 || b != 0 {
		t.Errorf("pixel 0 = (%d,%d,%d), want palette entry 0 (red)", r, g, b)
	}
	if r, g, b := pixelAt(t, img, 1, 0); r != 0 || g != 0 || b != 255 {
		t.Errorf("pixel 1 = (%d,%d,%d), want palette entry 1 (blue)", r, g, b)
	}
}

func TestResolveImageColorSpaceNamedResource(t *testing.T) {
	rgb := pdf.PDFName{Value: "DeviceRGB"}
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"ColorSpace": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"CS0": rgb})},
	})}
	dict := imageDict(map[string]pdf.PDFValue{"ColorSpace": pdf.PDFName{Value: "CS0"}}, nil)
	if got := resolveImageColorSpace(dict, resources); got != rgb {
		t.Errorf("named colour space resolved to %v, want DeviceRGB", got)
	}
	// An unknown name is returned as written.
	dict = imageDict(map[string]pdf.PDFValue{"ColorSpace": pdf.PDFName{Value: "CS9"}}, nil)
	if got := resolveImageColorSpace(dict, resources); got != (pdf.PDFName{Value: "CS9"}) {
		t.Errorf("unknown name resolved to %v, want it unchanged", got)
	}
}

// TestDecodeCCITTImageDefaultsColumnsToWidth covers /Columns 0 in the decode
// parameters, which falls back to the image width.
func TestDecodeCCITTImageDefaultsColumnsToWidth(t *testing.T) {
	decode := func(columns int64) []byte {
		dict := imageDict(map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(8), "Height": pdf.PDFInteger(1),
			"BitsPerComponent": pdf.PDFInteger(1), "ColorSpace": pdf.PDFName{Value: "DeviceGray"},
			"Filter": pdf.PDFName{Value: "CCITTFaxDecode"},
			"DecodeParms": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
				"Columns": pdf.PDFInteger(columns), "K": pdf.PDFInteger(0),
			})},
		}, []byte{0x98})
		img, err := DecodeImageRGBA(dict, pdf.PDFDict{})
		if err != nil {
			t.Fatalf("DecodeImageRGBA: %v", err)
		}
		return img.Pix
	}
	if got, want := decode(0), decode(8); !bytes.Equal(got, want) {
		t.Errorf("Columns 0 decoded to %v, want the Columns 8 result %v", got, want)
	}
}

func TestInlineImageDictSkipsNonNameKeys(t *testing.T) {
	name := func(s string) pdf.PDFValue { return pdf.PDFName{Value: s} }
	dict, ok := inlineImageDict([]pdf.PDFValue{
		pdf.PDFInteger(5), pdf.PDFInteger(6), // not a key: skipped with its value
		name("W"), pdf.PDFInteger(2), name("H"), pdf.PDFInteger(3),
	}, pdf.InlineImageRaw{})
	if !ok {
		t.Fatal("inlineImageDict rejected usable params")
	}
	if w, h := pdf.DictInt(dict, "Width", 0), pdf.DictInt(dict, "Height", 0); w != 2 || h != 3 {
		t.Errorf("size = %dx%d, want 2x3", w, h)
	}
}

// TestDecodeImageRGBAIndexed8BitRepeatsEntries checks that pixels sharing a
// palette index get that entry's colour however often the index recurs.
func TestDecodeImageRGBAIndexed8BitRepeatsEntries(t *testing.T) {
	dict := imageDict(map[string]pdf.PDFValue{
		"Width": pdf.PDFInteger(4), "Height": pdf.PDFInteger(1),
		"BitsPerComponent": pdf.PDFInteger(8),
		"ColorSpace": pdf.PDFArray{
			pdf.PDFName{Value: "Indexed"}, pdf.PDFName{Value: "DeviceRGB"}, pdf.PDFInteger(2),
			pdf.PDFString{Value: "\xff\x00\x00\x00\xff\x00\x00\x00\xff"},
		},
	}, []byte{1, 2, 1, 0})
	img, err := DecodeImageRGBA(dict, pdf.PDFDict{})
	if err != nil {
		t.Fatalf("DecodeImageRGBA: %v", err)
	}
	want := [][3]uint8{{0, 255, 0}, {0, 0, 255}, {0, 255, 0}, {255, 0, 0}}
	for x, w := range want {
		if r, g, b := pixelAt(t, img, x, 0); [3]uint8{uint8(r), uint8(g), uint8(b)} != w {
			t.Errorf("pixel %d = (%d,%d,%d), want %v", x, r, g, b, w)
		}
	}
}

// TestAlignedImageMatchesGeneralLoop draws random images, scales and offsets
// through the aligned fast path and through the general loop (forced by a
// clip mask that lets everything through) and compares the canvases.
func TestAlignedImageMatchesGeneralLoop(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for iter := 0; iter < 150; iter++ {
		w, h := 1+rng.Intn(12), 1+rng.Intn(12)
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256))
			img.Pix[i+3] = []uint8{255, 255, 255, 0, uint8(rng.Intn(256))}[rng.Intn(5)]
		}
		sx, sy := 3+rng.Float64()*50, 3+rng.Float64()*50
		if rng.Intn(3) == 0 {
			sy = -sy // upside down
		}
		ctm := content.Matrix{A: sx, D: sy, E: rng.Float64()*20 - 10, F: rng.Float64()*20 - 10}
		draw := func(mask *clipMask) *image.RGBA {
			canvas := image.NewRGBA(image.Rect(0, 0, 40, 40))
			for i := range canvas.Pix {
				canvas.Pix[i] = 200
			}
			gs := newRenderState(ctm, [4]float64{0, 0, 40, 40})
			gs.mask = mask
			r := &renderer{canvas: canvas}
			r.compositeImage(img, nil, false, false, &gs)
			return canvas
		}
		all := newClipMask(image.Rect(0, 0, 40, 40), [][]content.Point{{{X: -1, Y: -1}, {X: 41, Y: -1}, {X: 41, Y: 41}, {X: -1, Y: 41}}}, false, nil)
		if got, want := draw(nil), draw(all); !bytes.Equal(got.Pix, want.Pix) {
			t.Fatalf("iteration %d: aligned image differs from the general loop (ctm %+v, %dx%d)", iter, ctm, w, h)
		}
	}
}

// TestSmoothAlignedImageMatchesGeneralLoop verifies that smoothed images,
// enlarged with /Interpolate or shrunk, drawn on the aligned path differ from
// the general loop by at most one level per channel. Translucent smoothed
// images take the general loop, because the smooth aligned path needs opaque
// pixels.
func TestSmoothAlignedImageMatchesGeneralLoop(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	for iter := 0; iter < 150; iter++ {
		w, h := 1+rng.Intn(40), 1+rng.Intn(40)
		translucent := iter%5 == 0
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255
			if translucent {
				img.Pix[i+3] = uint8(rng.Intn(256))
			}
		}
		sx, sy := 2+rng.Float64()*45, 2+rng.Float64()*45
		if rng.Intn(3) == 0 {
			sy = -sy
		}
		ctm := content.Matrix{A: sx, D: sy, E: rng.Float64()*20 - 10, F: rng.Float64()*20 - 10}
		draw := func(mask *clipMask) *image.RGBA {
			canvas := image.NewRGBA(image.Rect(0, 0, 40, 40))
			for i := range canvas.Pix {
				canvas.Pix[i] = 200
			}
			gs := newRenderState(ctm, [4]float64{0, 0, 40, 40})
			gs.mask = mask
			r := &renderer{canvas: canvas}
			r.compositeImage(img, nil, false, true, &gs)
			return canvas
		}
		all := newClipMask(image.Rect(0, 0, 40, 40), [][]content.Point{{{X: -1, Y: -1}, {X: 41, Y: -1}, {X: 41, Y: 41}, {X: -1, Y: 41}}}, false, nil)
		got, want := draw(nil), draw(all)
		for i := range got.Pix {
			if d := int(got.Pix[i]) - int(want.Pix[i]); d < -1 || d > 1 {
				t.Fatalf("iteration %d: byte %d is %d, the general loop has %d (ctm %+v, %dx%d)", iter, i, got.Pix[i], want.Pix[i], ctm, w, h)
			}
		}
	}
}
