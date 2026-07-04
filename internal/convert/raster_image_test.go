package convert

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func pixelAt(t *testing.T, img *image.RGBA, x, y int) (r, g, b uint8) {
	t.Helper()
	c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
	return c.R, c.G, c.B
}

func TestDecodeImageRGBA8BitGray(t *testing.T) {
	dict := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(2), "Height": pdf.PDFInteger(1),
			"BitsPerComponent": pdf.PDFInteger(8),
			"ColorSpace":       pdf.PDFName{Value: "DeviceGray"},
		},
		HasStream: true,
		RawStream: []byte{0x00, 0xFF},
	}
	img, err := DecodeImageRGBA(dict, pdf.PDFDict{})
	if err != nil {
		t.Fatalf("DecodeImageRGBA: %v", err)
	}
	if r, _, _ := pixelAt(t, img, 0, 0); r != 0 {
		t.Errorf("pixel(0,0) R = %d, want 0", r)
	}
	if r, _, _ := pixelAt(t, img, 1, 0); r != 255 {
		t.Errorf("pixel(1,0) R = %d, want 255", r)
	}
}

func TestDecodeImageRGBA1BitMonochrome(t *testing.T) {
	// 1-bit DeviceGray, width=8 packed into a single byte: 10110010.
	dict := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(8), "Height": pdf.PDFInteger(1),
			"BitsPerComponent": pdf.PDFInteger(1),
			"ColorSpace":       pdf.PDFName{Value: "DeviceGray"},
		},
		HasStream: true,
		RawStream: []byte{0b10110010},
	}
	img, err := DecodeImageRGBA(dict, pdf.PDFDict{})
	if err != nil {
		t.Fatalf("DecodeImageRGBA: %v", err)
	}
	want := []bool{true, false, true, true, false, false, true, false}
	for x, bit := range want {
		r, _, _ := pixelAt(t, img, x, 0)
		got := r != 0
		if got != bit {
			t.Errorf("pixel(%d,0) white=%v, want %v", x, got, bit)
		}
	}
}

func TestDecodeImageRGBA2And4Bit(t *testing.T) {
	// 4-bit DeviceGray, 2 pixels packed into 1 byte: 0x F (15) and 0x0 (0).
	dict4 := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(2), "Height": pdf.PDFInteger(1),
			"BitsPerComponent": pdf.PDFInteger(4),
			"ColorSpace":       pdf.PDFName{Value: "DeviceGray"},
		},
		HasStream: true,
		RawStream: []byte{0xF0},
	}
	img, err := DecodeImageRGBA(dict4, pdf.PDFDict{})
	if err != nil {
		t.Fatalf("DecodeImageRGBA: %v", err)
	}
	if r, _, _ := pixelAt(t, img, 0, 0); r != 255 {
		t.Errorf("4-bit pixel(0,0) R = %d, want 255", r)
	}
	if r, _, _ := pixelAt(t, img, 1, 0); r != 0 {
		t.Errorf("4-bit pixel(1,0) R = %d, want 0", r)
	}

	// 2-bit DeviceGray, 4 pixels packed into 1 byte: 11 10 01 00 -> 255,170,85,0.
	dict2 := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(4), "Height": pdf.PDFInteger(1),
			"BitsPerComponent": pdf.PDFInteger(2),
			"ColorSpace":       pdf.PDFName{Value: "DeviceGray"},
		},
		HasStream: true,
		RawStream: []byte{0b11100100},
	}
	img2, err := DecodeImageRGBA(dict2, pdf.PDFDict{})
	if err != nil {
		t.Fatalf("DecodeImageRGBA: %v", err)
	}
	wantVals := []uint8{255, 170, 85, 0}
	for x, want := range wantVals {
		r, _, _ := pixelAt(t, img2, x, 0)
		if r != want {
			t.Errorf("2-bit pixel(%d,0) R = %d, want %d", x, r, want)
		}
	}
}

func TestDecodeImageRGBADecodeArrayInversion(t *testing.T) {
	dict := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(2), "Height": pdf.PDFInteger(1),
			"BitsPerComponent": pdf.PDFInteger(8),
			"ColorSpace":       pdf.PDFName{Value: "DeviceGray"},
			"Decode":           pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFInteger(0)},
		},
		HasStream: true,
		RawStream: []byte{0x00, 0xFF},
	}
	img, err := DecodeImageRGBA(dict, pdf.PDFDict{})
	if err != nil {
		t.Fatalf("DecodeImageRGBA: %v", err)
	}
	if r, _, _ := pixelAt(t, img, 0, 0); r != 255 {
		t.Errorf("inverted pixel(0,0) R = %d, want 255", r)
	}
	if r, _, _ := pixelAt(t, img, 1, 0); r != 0 {
		t.Errorf("inverted pixel(1,0) R = %d, want 0", r)
	}
}

func TestDecodeImageRGBAJPEGRoundTrip(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			src.Set(x, y, color.RGBA{R: 200, G: 50, B: 50, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}

	dict := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(4), "Height": pdf.PDFInteger(4),
			"BitsPerComponent": pdf.PDFInteger(8),
			"ColorSpace":       pdf.PDFName{Value: "DeviceRGB"},
			"Filter":           pdf.PDFName{Value: "DCTDecode"},
		},
		HasStream: true,
		RawStream: buf.Bytes(),
	}
	img, err := DecodeImageRGBA(dict, pdf.PDFDict{})
	if err != nil {
		t.Fatalf("DecodeImageRGBA: %v", err)
	}
	r, g, b := pixelAt(t, img, 0, 0)
	if r < 180 || g > 80 || b > 80 {
		t.Errorf("decoded JPEG pixel (%d,%d,%d) doesn't resemble the encoded reddish colour", r, g, b)
	}
}

func TestDecodeImageRGBAUnsupportedCodecPlaceholder(t *testing.T) {
	dict := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(3), "Height": pdf.PDFInteger(3),
			"BitsPerComponent": pdf.PDFInteger(1),
			"ColorSpace":       pdf.PDFName{Value: "DeviceGray"},
			"Filter":           pdf.PDFName{Value: "CCITTFaxDecode"},
		},
		HasStream: true,
		RawStream: []byte{0x00},
	}
	img, err := DecodeImageRGBA(dict, pdf.PDFDict{})
	if err != nil {
		t.Fatalf("DecodeImageRGBA: %v", err)
	}
	r, g, b := pixelAt(t, img, 1, 1)
	if r != 127 || g != 127 || b != 127 {
		t.Errorf("unsupported codec placeholder pixel = (%d,%d,%d), want mid-gray (127,127,127)", r, g, b)
	}
}

// renderImageXObject draws img (an Image XObject) scaled over a 20x20 canvas
// via a Do operator, returning an error only if rendering fails.
func renderImageXObject(t *testing.T, img pdf.PDFDict) {
	t.Helper()
	resources := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"XObject": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Im1": img}},
	}}
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte("0 0 0 rg q 20 0 0 20 0 0 cm /Im1 Do Q")},
	}}
	if _, err := RenderPage(page, resources, [4]float64{0, 0, 20, 20}, 72); err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
}

func imageDict(entries map[string]pdf.PDFValue, raw []byte) pdf.PDFDict {
	entries["Subtype"] = pdf.PDFName{Value: "Image"}
	return pdf.PDFDict{Entries: entries, HasStream: true, RawStream: raw}
}

// TestRenderImageFormats drives DecodeImageRGBA across colour spaces, bit
// depths, image masks, Decode arrays, and a CCITT-encoded image.
func TestRenderImageFormats(t *testing.T) {
	t.Run("16bit-rgb", func(t *testing.T) {
		renderImageXObject(t, imageDict(map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(1), "Height": pdf.PDFInteger(1),
			"BitsPerComponent": pdf.PDFInteger(16), "ColorSpace": pdf.PDFName{Value: "DeviceRGB"},
		}, []byte{0xFF, 0xFF, 0x00, 0x00, 0x80, 0x00}))
	})

	t.Run("cmyk", func(t *testing.T) {
		renderImageXObject(t, imageDict(map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(1), "Height": pdf.PDFInteger(1),
			"BitsPerComponent": pdf.PDFInteger(8), "ColorSpace": pdf.PDFName{Value: "DeviceCMYK"},
		}, []byte{0, 0, 0, 0}))
	})

	t.Run("indexed", func(t *testing.T) {
		renderImageXObject(t, imageDict(map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(2), "Height": pdf.PDFInteger(1),
			"BitsPerComponent": pdf.PDFInteger(8),
			"ColorSpace": pdf.PDFArray{
				pdf.PDFName{Value: "Indexed"}, pdf.PDFName{Value: "DeviceRGB"}, pdf.PDFInteger(1),
				pdf.PDFString{Value: "\x00\x00\x00\xff\xff\xff"},
			},
		}, []byte{0, 1}))
	})

	t.Run("1bit-gray", func(t *testing.T) {
		renderImageXObject(t, imageDict(map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(8), "Height": pdf.PDFInteger(1),
			"BitsPerComponent": pdf.PDFInteger(1), "ColorSpace": pdf.PDFName{Value: "DeviceGray"},
		}, []byte{0b10101010}))
	})

	t.Run("image-mask", func(t *testing.T) {
		renderImageXObject(t, imageDict(map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(8), "Height": pdf.PDFInteger(1),
			"ImageMask": pdf.PDFBoolean(true),
		}, []byte{0b11110000}))
	})

	t.Run("decode-array-inverted", func(t *testing.T) {
		renderImageXObject(t, imageDict(map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(2), "Height": pdf.PDFInteger(1),
			"BitsPerComponent": pdf.PDFInteger(8), "ColorSpace": pdf.PDFName{Value: "DeviceGray"},
			"Decode": pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFInteger(0)},
		}, []byte{0x00, 0xFF}))
	})

	t.Run("16bit-gray", func(t *testing.T) {
		renderImageXObject(t, imageDict(map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(1), "Height": pdf.PDFInteger(1),
			"BitsPerComponent": pdf.PDFInteger(16), "ColorSpace": pdf.PDFName{Value: "DeviceGray"},
		}, []byte{0x80, 0x00}))
	})

	t.Run("4bit-gray", func(t *testing.T) {
		renderImageXObject(t, imageDict(map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(2), "Height": pdf.PDFInteger(1),
			"BitsPerComponent": pdf.PDFInteger(4), "ColorSpace": pdf.PDFName{Value: "DeviceGray"},
		}, []byte{0x0F}))
	})

	t.Run("jpeg-dct", func(t *testing.T) {
		src := image.NewRGBA(image.Rect(0, 0, 4, 4))
		for i := range src.Pix {
			src.Pix[i] = 0xC0
		}
		src.Set(0, 0, color.RGBA{200, 50, 50, 255})
		var jbuf bytes.Buffer
		if err := jpeg.Encode(&jbuf, src, &jpeg.Options{Quality: 80}); err != nil {
			t.Fatalf("jpeg encode: %v", err)
		}
		renderImageXObject(t, imageDict(map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(4), "Height": pdf.PDFInteger(4),
			"BitsPerComponent": pdf.PDFInteger(8), "ColorSpace": pdf.PDFName{Value: "DeviceRGB"},
			"Filter": pdf.PDFName{Value: "DCTDecode"},
		}, jbuf.Bytes()))
	})

	t.Run("ccitt", func(t *testing.T) {
		renderImageXObject(t, imageDict(map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(8), "Height": pdf.PDFInteger(1),
			"BitsPerComponent": pdf.PDFInteger(1), "ColorSpace": pdf.PDFName{Value: "DeviceGray"},
			"Filter": pdf.PDFName{Value: "CCITTFaxDecode"},
			"DecodeParms": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
				"Columns": pdf.PDFInteger(8), "Rows": pdf.PDFInteger(1), "K": pdf.PDFInteger(0),
			}},
		}, []byte{0x98}))
	})
}

func TestImageDecodeError(t *testing.T) {
	if imageDecodeError("boom").Error() != "boom" {
		t.Error("imageDecodeError.Error()")
	}
}
