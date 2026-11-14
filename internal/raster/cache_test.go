package raster

import (
	"context"
	"image"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func grayImage(w, h int, v byte) pdf.PDFDict {
	raw := make([]byte, w*h)
	for i := range raw {
		raw[i] = v
	}
	return imageDict(map[string]pdf.PDFValue{
		"Width": pdf.PDFInteger(w), "Height": pdf.PDFInteger(h),
		"BitsPerComponent": pdf.PDFInteger(8), "ColorSpace": pdf.PDFName{Value: "DeviceGray"},
	}, raw)
}

func TestCacheKeepsDecodedImagesWithinItsBudget(t *testing.T) {
	a, b, c := grayImage(10, 10, 10), grayImage(10, 10, 20), grayImage(10, 10, 30)
	cache := NewCache(2 * 534) // room for two 10x10 images and their pyramids, 533 bytes each
	first, err := cache.decode(a, dictOf(nil))
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := cache.decode(a, dictOf(nil)); again != first {
		t.Error("a decoded image was decoded again")
	}
	cache.decode(b, dictOf(nil))
	cache.decode(a, dictOf(nil)) // a is now the most recently used
	cache.decode(c, dictOf(nil)) // over budget: b, the least recently used, goes
	if _, ok := cache.images[pdf.ValuePointer(b.Entries)]; ok {
		t.Error("the least recently used image was kept")
	}
	if _, ok := cache.images[pdf.ValuePointer(a.Entries)]; !ok {
		t.Error("a recently used image was dropped")
	}
	if cache.bytes != 2*533 {
		t.Errorf("cache holds %d bytes, want %d", cache.bytes, 2*533)
	}
}

func TestCacheSkipsOversizedImagesAndErrors(t *testing.T) {
	cache := NewCache(100)
	big := grayImage(10, 10, 1) // 533 bytes with its pyramid
	img, err := cache.decode(big, dictOf(nil))
	if err != nil || img == nil || len(cache.images) != 0 || cache.bytes != 0 {
		t.Errorf("oversized image: %v, %v, %d cached", img, err, len(cache.images))
	}
	bad := imageDict(map[string]pdf.PDFValue{"Width": pdf.PDFInteger(0), "Height": pdf.PDFInteger(1)}, nil)
	if _, err := cache.decode(bad, dictOf(nil)); err == nil {
		t.Error("an undecodable image gave no error")
	}
	if len(cache.images) != 0 {
		t.Error("an undecodable image was cached")
	}
}

// TestRendersThroughACacheMatchPlainRenders draws a page with an image and
// text-free content twice through one cache and once without.
func TestRendersThroughACacheMatchPlainRenders(t *testing.T) {
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(pdf.DictOf(nil), []byte("q 40 0 0 30 5 5 cm /Im Do Q")),
	})}
	res := dictOf(map[string]pdf.PDFValue{"XObject": dictOf(map[string]pdf.PDFValue{"Im": grayImage(4, 3, 90)})})
	box := [4]float64{0, 0, 60, 50}
	want, _, err := RenderPageContext(context.Background(), page, res, box, 72, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cache := NewCache(1 << 20)
	for i := 0; i < 2; i++ {
		got, _, err := RenderPageRegion(context.Background(), page, res, box, 72, image.Rect(0, 0, 60, 50), nil, cache)
		if err != nil {
			t.Fatal(err)
		}
		if string(got.Pix) != string(want.Pix) {
			t.Fatalf("render %d through the cache differs from a plain render", i)
		}
	}
	if len(cache.images) != 1 {
		t.Errorf("%d images cached, want 1", len(cache.images))
	}
}

func TestCacheKeepsOnePyramidPerImage(t *testing.T) {
	cache := NewCache(1 << 20)
	img, _ := cache.decode(grayImage(8, 8, 5), dictOf(nil))
	s := cache.sampler(img)
	if s.levelFor(4); cache.sampler(img) != s || len(cache.sampler(img).levels) < 2 {
		t.Error("the pyramid of a cached image was not kept")
	}
	// An image the cache did not decode gets a sampler of its own each time.
	other := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if cache.sampler(other) == cache.sampler(other) {
		t.Error("a foreign image shares a sampler")
	}
}
