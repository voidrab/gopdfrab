package pdfrab

import (
	"image"
	"image/color"
	"testing"
)

func nrgbaAt(t *testing.T, img *image.RGBA, x, y int) color.NRGBA {
	t.Helper()
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}

func TestRenderPageRectangleFill(t *testing.T) {
	page := PDFDict{Entries: map[string]PDFValue{
		"Contents": PDFDict{HasStream: true, RawStream: []byte("1 0 0 rg 5 5 10 10 re f")},
	}}
	canvas, err := RenderPage(page, PDFDict{}, [4]float64{0, 0, 20, 20}, 72)
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
	img := PDFDict{
		Entries: map[string]PDFValue{
			"Subtype": PDFName{Value: "Image"},
			"Width":   PDFInteger(2), "Height": PDFInteger(2), "BitsPerComponent": PDFInteger(8),
			"ColorSpace": PDFName{Value: "DeviceRGB"},
		},
		HasStream: true,
		RawStream: []byte{0, 0, 255, 0, 0, 255, 0, 0, 255, 0, 0, 255},
	}
	resources := PDFDict{Entries: map[string]PDFValue{
		"XObject": PDFDict{Entries: map[string]PDFValue{"Im1": img}},
	}}
	page := PDFDict{Entries: map[string]PDFValue{
		"Contents": PDFDict{HasStream: true, RawStream: []byte("q 20 0 0 20 0 0 cm /Im1 Do Q")},
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
	smask := PDFDict{
		Entries: map[string]PDFValue{
			"Width": PDFInteger(2), "Height": PDFInteger(2), "BitsPerComponent": PDFInteger(8),
			"ColorSpace": PDFName{Value: "DeviceGray"},
		},
		HasStream: true,
		RawStream: []byte{128, 128, 128, 128},
	}
	img := PDFDict{
		Entries: map[string]PDFValue{
			"Subtype": PDFName{Value: "Image"},
			"Width":   PDFInteger(2), "Height": PDFInteger(2), "BitsPerComponent": PDFInteger(8),
			"ColorSpace": PDFName{Value: "DeviceRGB"}, "SMask": smask,
		},
		HasStream: true,
		RawStream: []byte{255, 0, 0, 255, 0, 0, 255, 0, 0, 255, 0, 0},
	}
	resources := PDFDict{Entries: map[string]PDFValue{
		"XObject": PDFDict{Entries: map[string]PDFValue{"Im1": img}},
	}}
	page := PDFDict{Entries: map[string]PDFValue{
		"Contents": PDFDict{HasStream: true, RawStream: []byte("q 20 0 0 20 0 0 cm /Im1 Do Q")},
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
