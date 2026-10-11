package raster

import (
	"bytes"
	"errors"
	"strings"
	"testing"

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

// TestDecodeImageRGBAUndecodableCodecsPlaceholder: a codec with no decoder
// comes back as the placeholder and a PlaceholderError naming the codec, so
// the gray is paintable but never mistaken for the picture.
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
			if r, g, b := pixelAt(t, img, 1, 1); r != 127 || g != 127 || b != 127 {
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
