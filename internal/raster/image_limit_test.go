package raster

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestDecodeImageRGBAPixelLimit pins that an image claiming more than
// maxImagePixels with samples that do not back the claim gets the one-pixel
// placeholder and an "oversized" PlaceholderError, not a buffer that size:
// four bytes used to ask for 60000x60000. Codecs that only ever get the
// placeholder allocate nothing and keep their own name.
func TestDecodeImageRGBAPixelLimit(t *testing.T) {
	huge := func(extra map[string]pdf.PDFValue) map[string]pdf.PDFValue {
		m := map[string]pdf.PDFValue{
			"Width": pdf.PDFInteger(60000), "Height": pdf.PDFInteger(60000),
			"BitsPerComponent": pdf.PDFInteger(8), "ColorSpace": pdf.PDFName{Value: "DeviceGray"},
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	for name, tc := range map[string]struct {
		dict  pdf.PDFDict
		codec string
	}{
		"raw samples": {imageDict(huge(nil), []byte{0, 0, 0, 0}), "oversized"},
		"image mask":  {imageDict(huge(map[string]pdf.PDFValue{"ImageMask": pdf.PDFBoolean(true)}), []byte{0}), "oversized"},
		"JPX":         {imageDict(huge(map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "JPXDecode"}}), []byte{0}), "JPEG 2000"},
	} {
		t.Run(name, func(t *testing.T) {
			img, err := DecodeImageRGBA(tc.dict, pdf.PDFDict{})
			var ph *PlaceholderError
			if !errors.As(err, &ph) || ph.Codec != tc.codec {
				t.Fatalf("err = %v, want the %s PlaceholderError", err, tc.codec)
			}
			if img.Bounds().Dx() != 1 || img.Bounds().Dy() != 1 {
				t.Errorf("image is %v, want the 1x1 placeholder", img.Bounds())
			}
		})
	}
}

// TestSamplesBackSize pins the rule: any claim up to maxImagePixels, and past
// it only a claim whose samples hold every row. Real figures reach 250M
// pixels, fully backed, and must keep decoding.
func TestSamplesBackSize(t *testing.T) {
	const w, h = 16000, 16000 // 256M pixels, past the limit
	cases := []struct {
		name                string
		n, w, h, ncomp, bpc int
		want                bool
	}{
		{"small and short", 0, 100, 100, 1, 8, true},
		{"big and backed", w * h, w, h, 1, 8, true},
		{"big, backed at 1 bpc", w * h / 8, w, h, 1, 1, true},
		{"big, one row short", w*h - w, w, h, 1, 8, false},
		{"big, not one row's worth", 10, w, h, 3, 8, false},
	}
	for _, tc := range cases {
		if got := samplesBackSize(tc.n, tc.w, tc.h, tc.ncomp, tc.bpc); got != tc.want {
			t.Errorf("%s: samplesBackSize = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestDecodeJPEGHeaderPixelLimit pins that a JPEG is checked against the
// size its own header declares, which image/jpeg allocates for regardless of
// the /Width and /Height around it.
func TestDecodeJPEGHeaderPixelLimit(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	sof := bytes.Index(data, []byte{0xFF, 0xC0})
	if sof < 0 {
		t.Fatal("no SOF0 marker")
	}
	// SOF0: marker, length (2), precision (1), height (2), width (2).
	copy(data[sof+5:], []byte{0xFF, 0xFF, 0xFF, 0xFF})

	dict := imageDict(map[string]pdf.PDFValue{
		"Width": pdf.PDFInteger(8), "Height": pdf.PDFInteger(8),
		"BitsPerComponent": pdf.PDFInteger(8), "ColorSpace": pdf.PDFName{Value: "DeviceGray"},
		"Filter": pdf.PDFName{Value: "DCTDecode"},
	}, data)
	img, err := DecodeImageRGBA(dict, pdf.PDFDict{})
	var ph *PlaceholderError
	if !errors.As(err, &ph) || ph.Codec != "oversized" || img.Bounds().Dx() != 1 {
		t.Errorf("err = %v, want the oversized placeholder for a 65535x65535 JPEG header", err)
	}
}

// TestRasterReportsOversizedImage pins that the renderer reports an
// oversized image as a drop, as it does an undecodable codec.
func TestRasterReportsOversizedImage(t *testing.T) {
	page, resources := placeholderImagePage(imageDict(map[string]pdf.PDFValue{
		"Width": pdf.PDFInteger(60000), "Height": pdf.PDFInteger(60000),
		"BitsPerComponent": pdf.PDFInteger(8), "ColorSpace": pdf.PDFName{Value: "DeviceGray"},
	}, []byte{0}))
	_, drops, err := RenderPage(page, resources, [4]float64{0, 0, 20, 20}, 72)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	if !hasDrop(drops, "oversized image") {
		t.Errorf("drops = %v, want %q", drops, "oversized image")
	}
}

// TestDecodeJPEGTruncatedBody covers a JPEG whose header reads but whose
// scan data is cut short: the size check passes, the decode itself fails.
func TestDecodeJPEGTruncatedBody(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, 64, 64)), nil); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	sos := bytes.Index(data, []byte{0xFF, 0xDA})
	if sos < 0 {
		t.Fatal("no SOS marker")
	}
	if _, err := decodeJPEGImage(data[:sos+4]); err == nil {
		t.Error("a JPEG cut off in its scan header decoded without error")
	}
}
