package convert

import (
	"image"
	"image/color"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func nrgbaAt(t *testing.T, img *image.RGBA, x, y int) color.NRGBA {
	t.Helper()
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}

func TestRenderPageRectangleFill(t *testing.T) {
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Contents": pdf.PDFDict{HasStream: true, RawStream: []byte("1 0 0 rg 5 5 10 10 re f")},
	}}
	canvas, err := RenderPage(page, pdf.PDFDict{}, [4]float64{0, 0, 20, 20}, 72)
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

	canvas, err := RenderPage(page, resources, [4]float64{0, 0, 20, 20}, 72)
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

	canvas, err := RenderPage(page, resources, [4]float64{0, 0, 20, 20}, 72)
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
