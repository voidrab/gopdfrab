package convert

import (
	"bytes"
	"image"
	"strconv"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestSmaskFullyOpaque covers the empty-bounds guard and both the
// fully-opaque and not-fully-opaque results.
func TestSmaskFullyOpaque(t *testing.T) {
	if smaskFullyOpaque(image.NewRGBA(image.Rect(0, 0, 0, 0))) {
		t.Error("smaskFullyOpaque on a zero-size image = true, want false")
	}

	opaque := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for i := 0; i < len(opaque.Pix); i += 4 {
		opaque.Pix[i] = 255
	}
	if !smaskFullyOpaque(opaque) {
		t.Error("smaskFullyOpaque on an all-255 red channel = false, want true")
	}

	partial := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for i := 0; i < len(partial.Pix); i += 4 {
		partial.Pix[i] = 255
	}
	partial.Pix[4] = 128 // one pixel's alpha sample below 255
	if smaskFullyOpaque(partial) {
		t.Error("smaskFullyOpaque with one non-255 sample = true, want false")
	}
}

// TestCanDropGroupSafely covers the decode-error guard, the safe (no gs/Do/
// inline-image/pattern) content, and each of the disqualifying operators.
func TestCanDropGroupSafely(t *testing.T) {
	streamOf := func(content string) pdf.PDFDict {
		return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{}), HasStream: true, RawStream: []byte(content)}
	}

	// Flate with bytes that are not a zlib stream at all: a genuine decode
	// failure, so the guard must refuse to judge the content.
	if canDropGroupSafely(pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "FlateDecode"}}), HasStream: true, RawStream: []byte("not zlib")}) {
		t.Error("canDropGroupSafely on an undecodable stream = true, want false")
	}
	if !canDropGroupSafely(streamOf("1 0 0 rg 0 0 10 10 re f")) {
		t.Error("canDropGroupSafely on plain fill content = false, want true")
	}
	if canDropGroupSafely(streamOf("/GS1 gs")) {
		t.Error("canDropGroupSafely with a gs operator = true, want false")
	}
	if canDropGroupSafely(streamOf("/Fm1 Do")) {
		t.Error("canDropGroupSafely with a Do operator = true, want false")
	}
	if canDropGroupSafely(streamOf("BI /W 1 /H 1 /BPC 8 /CS /G ID \x00 EI\n")) {
		t.Error("canDropGroupSafely with an inline image = true, want false")
	}
	if canDropGroupSafely(streamOf("/P1 scn")) {
		t.Error("canDropGroupSafely with a pattern (named) scn fill = true, want false")
	}
	if !canDropGroupSafely(streamOf("1 0 0 scn")) {
		t.Error("canDropGroupSafely with a plain numeric scn fill = false, want true")
	}
}

// TestTransparencyFlattenerFixDropsPageGroup covers the "page" kind branch
// in Fix -- a transparency group set directly on the page dict itself,
// rather than on a Form/Image XObject its resources reach -- which none of
// the corpus-driven Convert tests happen to isolate.
func TestTransparencyFlattenerFixDropsPageGroup(t *testing.T) {
	group := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"S": pdf.PDFName{Value: "Transparency"}})}
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":  pdf.PDFName{Value: "Page"},
		"Group": group,
	})}
	pages := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"},
		"Kids": pdf.PDFArray{page},
	})}
	root := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Pages": pages})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Root": root})}

	changed, err := (transparencyFlattener{}).Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true (page had a /Group)")
	}
	if page.Entries.Get("Group") != nil {
		t.Error("page /Group still present after Fix, want removed")
	}
}

// TestTransparencyFlattenerReachesAnnotationAppearances pins that the fixer
// walks a page's annotation appearance streams, not only its /Resources.
//
// An appearance stream is not in the page's /Resources, but the verifier's
// graph walk reports violations inside it all the same. Acrobat writes a
// digital signature's appearance as nested Form XObjects whose innermost form
// carries /Group /S /Transparency, so before this every signed document
// reported a 6.4 violation the fixer could not reach: the verify/fix loop ran
// its full four iterations, rasterized a page trying to make progress, and
// still returned non-conformant output.
func TestTransparencyFlattenerReachesAnnotationAppearances(t *testing.T) {
	group := func() pdf.PDFDict {
		return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"S": pdf.PDFName{Value: "Transparency"}})}
	}
	// A form whose content is provably transparency-free, so the fixer can drop
	// the group outright rather than rasterizing it.
	form := func(g pdf.PDFDict) pdf.PDFDict {
		return pdf.PDFDict{
			Entries: pdf.DictOf(map[string]pdf.PDFValue{
				"Type":    pdf.PDFName{Value: "XObject"},
				"Subtype": pdf.PDFName{Value: "Form"},
				"BBox":    pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(10), pdf.PDFInteger(10)},
				"Group":   g,
			}),
			HasStream: true,
			RawStream: []byte("0 0 0 rg 0 0 10 10 re f"),
		}
	}

	// /AP /N as a Form XObject directly, and as a dictionary of appearance
	// states -- both shapes a real annotation uses.
	direct := form(group())
	stated := form(group())
	nested := form(group())
	outer := pdf.PDFDict{
		Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Type":    pdf.PDFName{Value: "XObject"},
			"Subtype": pdf.PDFName{Value: "Form"},
			"Resources": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
				"XObject": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Fm1": nested})},
			})},
		}),
		HasStream: true,
		RawStream: []byte("/Fm1 Do"),
	}

	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Page"},
		"Annots": pdf.PDFArray{
			pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
				"Subtype": pdf.PDFName{Value: "Widget"},
				"AP":      pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"N": direct})},
			})},
			pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
				"Subtype": pdf.PDFName{Value: "Widget"},
				"AP": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
					"N": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"On": stated})},
				})},
			})},
			// The signature shape: an appearance form with no group of its own,
			// whose nested Form XObject carries one.
			pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
				"Subtype": pdf.PDFName{Value: "Widget"},
				"AP":      pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"N": outer})},
			})},
		},
	})}
	pages := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"},
		"Kids": pdf.PDFArray{page},
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Pages": pages})},
	})}

	targets := collectTransparencyTargets(trailer)
	if len(targets) != 3 {
		t.Fatalf("collectTransparencyTargets found %d targets, want 3 (direct, stated, nested)", len(targets))
	}
	for _, tgt := range targets {
		if tgt.kind != "form" {
			t.Errorf("target kind = %q, want \"form\"", tgt.kind)
		}
		if tgt.page != 1 {
			t.Errorf("target page = %d, want 1", tgt.page)
		}
	}

	changed, err := (transparencyFlattener{}).Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	for name, f := range map[string]pdf.PDFDict{"direct": direct, "stated": stated, "nested": nested} {
		if f.Entries.Get("Group") != nil {
			t.Errorf("%s appearance form still carries /Group after Fix", name)
		}
	}
}

func TestPackRGBSamples(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Pix[0], img.Pix[1], img.Pix[2] = 10, 20, 30
	out := packRGBSamples(img)
	if len(out) != 2*2*3 {
		t.Fatalf("packRGBSamples len = %d, want 12", len(out))
	}
	if out[0] != 10 || out[1] != 20 || out[2] != 30 {
		t.Errorf("first pixel = %v, want [10 20 30]", out[:3])
	}
}

// TestFlattenFormReportsDrops: a Form XObject the flattener rasterizes carries
// its undrawable content out through the fixer's drop sink, tagged with the
// page it sits on -- the page-level fallback's report never sees these.
func TestFlattenFormReportsDrops(t *testing.T) {
	// An unresolvable shading is content the rasterizer must report losing.
	form := pdf.PDFDict{
		Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Type":    pdf.PDFName{Value: "XObject"},
			"Subtype": pdf.PDFName{Value: "Form"},
			"BBox":    pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(20), pdf.PDFInteger(20)},
			"Group": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
				"S": pdf.PDFName{Value: "Transparency"},
			})},
		}),
		HasStream: true,
		// The gs keeps canDropGroupSafely from skipping the rasterization.
		RawStream: []byte("q /GS0 gs /Sh1 sh Q 0 0 0 rg 2 2 5 5 re f"),
	}
	xobjects := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Fm0": form})}
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"XObject": xobjects})}
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":      pdf.PDFName{Value: "Page"},
		"Resources": resources,
	})}
	pages := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"},
		"Kids": pdf.PDFArray{page},
	})}
	root := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Pages": pages})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Root": root})}

	var drops []RasterDrop
	changed, err := (transparencyFlattener{drops: &drops}).Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true (the Form carried a /Group)")
	}
	if len(drops) != 1 {
		t.Fatalf("drops = %v, want exactly one entry", drops)
	}
	if drops[0].Page != 1 {
		t.Errorf("drop page = %d, want 1", drops[0].Page)
	}
	if !hasDrop(drops[0].Features, dropShading) {
		t.Errorf("drop features = %v, want %q", drops[0].Features, dropShading)
	}
}

// TestFlattenFormWithoutSinkIsSafe: the registry prototype carries no sink,
// so flattening must not depend on one being present.
func TestFlattenFormWithoutSinkIsSafe(t *testing.T) {
	form := pdf.PDFDict{
		Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Type":    pdf.PDFName{Value: "XObject"},
			"Subtype": pdf.PDFName{Value: "Form"},
			"BBox":    pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(20), pdf.PDFInteger(20)},
			"Group": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
				"S": pdf.PDFName{Value: "Transparency"},
			})},
		}),
		HasStream: true,
		RawStream: []byte("q /GS0 gs /Sh1 sh Q"),
	}
	xobjects := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Fm0": form})}
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Page"},
		"Resources": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"XObject": xobjects,
		})},
	})}
	pages := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"},
		"Kids": pdf.PDFArray{page},
	})}
	root := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Pages": pages})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Root": root})}

	if _, err := (transparencyFlattener{}).Fix(&trailer, nil); err != nil {
		t.Fatalf("Fix without a drop sink: %v", err)
	}
}

// grayImage builds a minimal DeviceGray image XObject from its samples.
func grayImage(w, h int, samples []byte) pdf.PDFDict {
	return pdf.PDFDict{
		Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Type":             pdf.PDFName{Value: "XObject"},
			"Subtype":          pdf.PDFName{Value: "Image"},
			"Width":            pdf.PDFInteger(w),
			"Height":           pdf.PDFInteger(h),
			"BitsPerComponent": pdf.PDFInteger(8),
			"ColorSpace":       pdf.PDFName{Value: "DeviceGray"},
		}),
		HasStream: true,
		RawStream: samples,
	}
}

func TestHasSoftMask(t *testing.T) {
	tests := []struct {
		name  string
		smask pdf.PDFValue
		want  bool
	}{
		{"absent", nil, false},
		{"the literal /None", pdf.PDFName{Value: "None"}, false},
		{"another name", pdf.PDFName{Value: "Im1"}, true},
		{"a stream dict", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			img := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}
			if tc.smask != nil {
				img.Entries.Set("SMask", tc.smask)
			}
			if got := hasSoftMask(img); got != tc.want {
				t.Errorf("hasSoftMask = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestBakeSoftMaskOutRefusals: baking needs both the base image and its mask
// to decode. When either does not, the image must be handed back untouched --
// a half-composited rewrite would corrupt it.
func TestBakeSoftMaskOutRefusals(t *testing.T) {
	mask := grayImage(2, 2, []byte{0, 128, 128, 255})

	tests := []struct {
		name string
		img  pdf.PDFDict
	}{
		{"undecodable base", func() pdf.PDFDict {
			img := grayImage(2, 2, []byte{1, 2, 3, 4})
			img.Entries.Set("Filter", pdf.PDFName{Value: "FlateDecode"})
			img.Entries.Set("SMask", mask)
			return img
		}()},
		{"SMask is not a stream dict", func() pdf.PDFDict {
			img := grayImage(2, 2, []byte{1, 2, 3, 4})
			img.Entries.Set("SMask", pdf.PDFName{Value: "Im1"})
			return img
		}()},
		{"undecodable SMask", func() pdf.PDFDict {
			bad := grayImage(2, 2, []byte{1, 2, 3, 4})
			bad.Entries.Set("Filter", pdf.PDFName{Value: "FlateDecode"})
			img := grayImage(2, 2, []byte{1, 2, 3, 4})
			img.Entries.Set("SMask", bad)
			return img
		}()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := bakeSoftMaskOut(tc.img, pdf.PDFDict{})
			if ok {
				t.Fatal("bakeSoftMaskOut reported success on an undecodable pair")
			}
			if _, still := got.Entries.Lookup("SMask"); !still && tc.name != "SMask is not a stream dict" {
				t.Error("a refused bake must leave /SMask in place")
			}
		})
	}
}

// TestBakeSoftMaskOutFullyOpaque: a mask that is opaque everywhere composites
// to the base unchanged, so the /SMask is simply dropped and the original
// encoding kept -- re-encoding it would be a needless quality loss.
func TestBakeSoftMaskOutFullyOpaque(t *testing.T) {
	samples := []byte{10, 20, 30, 40}
	img := grayImage(2, 2, samples)
	img.Entries.Set("SMask", grayImage(2, 2, []byte{255, 255, 255, 255}))

	got, ok := bakeSoftMaskOut(img, pdf.PDFDict{})
	if !ok {
		t.Fatal("bakeSoftMaskOut on a fully-opaque mask reported no change")
	}
	if _, still := got.Entries.Lookup("SMask"); still {
		t.Error("/SMask survived a fully-opaque bake")
	}
	if !bytes.Equal(got.RawStream, samples) {
		t.Errorf("stream was re-encoded: % x, want the original % x", got.RawStream, samples)
	}
	if cs, _ := got.Entries.Get("ColorSpace").(pdf.PDFName); cs.Value != "DeviceGray" {
		t.Errorf("ColorSpace = %v, want the original DeviceGray", got.Entries.Get("ColorSpace"))
	}
}

// TestBakeSoftMaskOutComposites: a partially transparent mask is composited
// against white and the image is rewritten flat and opaque.
func TestBakeSoftMaskOutComposites(t *testing.T) {
	img := grayImage(2, 1, []byte{0, 0})
	// two black pixels
	img.Entries.Set("SMask", grayImage(2, 1, []byte{255, 0}))

	got, ok := bakeSoftMaskOut(img, pdf.PDFDict{})
	if !ok {
		t.Fatal("bakeSoftMaskOut reported no change")
	}
	if _, still := got.Entries.Lookup("SMask"); still {
		t.Error("/SMask survived the bake")
	}
	if cs, _ := got.Entries.Get("ColorSpace").(pdf.PDFName); cs.Value != "DeviceRGB" {
		t.Errorf("ColorSpace = %v, want DeviceRGB", got.Entries.Get("ColorSpace"))
	}

	baked, err := DecodeImageRGBA(got, pdf.PDFDict{})
	if err != nil {
		t.Fatalf("baked image does not decode: %v", err)
	}
	// Alpha 255 keeps the black base; alpha 0 shows the white backdrop.
	if r := baked.Pix[0]; r > 20 {
		t.Errorf("opaque pixel = %d, want near black", r)
	}
	if r := baked.Pix[4]; r < 235 {
		t.Errorf("transparent pixel = %d, want the white backdrop", r)
	}
}

// TestCollectTargetsWithoutPageTree: both collectors walk from Root/Pages. A
// trailer missing either returns no targets rather than panicking -- a damaged
// document still has to get through the fix pass.
func TestCollectTargetsWithoutPageTree(t *testing.T) {
	noRoot := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}
	noPages := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})},
	})}
	for _, tc := range []struct {
		name    string
		trailer pdf.PDFDict
	}{{"no Root", noRoot}, {"no Pages", noPages}} {
		t.Run(tc.name, func(t *testing.T) {
			if got := collectTransparencyTargets(tc.trailer); got != nil {
				t.Errorf("collectTransparencyTargets = %v, want nil", got)
			}
			if got := orderedPages(tc.trailer); got != nil {
				t.Errorf("orderedPages = %v, want nil", got)
			}
		})
	}
}

// TestCollectXObjectTargetsWalk covers the resource-walk arms the corpus does
// not isolate: a non-dict /XObject entry is skipped, a nested Form without its
// own /Resources inherits the parent's, and an /XObject dictionary reached
// twice is visited once.
func TestCollectXObjectTargetsWalk(t *testing.T) {
	group := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"S": pdf.PDFName{Value: "Transparency"}})}

	// The innermost form carries the group; the form above it has no
	// /Resources of its own, so the walk must fall back to the parent's.
	inner := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":    pdf.PDFName{Value: "XObject"},
		"Subtype": pdf.PDFName{Value: "Form"},
		"Group":   group,
	})}
	outer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":    pdf.PDFName{Value: "XObject"},
		"Subtype": pdf.PDFName{Value: "Form"},
	})}
	xobjects := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Fm0":     outer,
		"Fm1":     inner,
		"NotADic": pdf.PDFInteger(7),
	})}
	resources := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"XObject": xobjects})}

	var out []flaggedTarget
	visited := map[uintptr]bool{}
	collectXObjectTargets(resources, visited, 1, &out)
	if len(out) != 1 || out[0].name != "Fm1" {
		t.Fatalf("targets = %+v, want just Fm1", out)
	}

	// The same resources reached again must not re-flag: visited is what stops
	// a cyclic or shared XObject dictionary from looping.
	collectXObjectTargets(resources, visited, 1, &out)
	if len(out) != 1 {
		t.Errorf("revisiting the same /XObject dict produced %d targets, want 1", len(out))
	}
}

// groupFormDrawingIm0 builds a group Form XObject that paints /Im0 over its
// whole BBox. The gs is there to keep canDropGroupSafely from dropping the
// group without rasterizing, which is the path under test.
func groupFormDrawingIm0(resources pdf.PDFValue) pdf.PDFDict {
	entries := map[string]pdf.PDFValue{
		"Type":    pdf.PDFName{Value: "XObject"},
		"Subtype": pdf.PDFName{Value: "Form"},
		"BBox":    pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(20), pdf.PDFInteger(20)},
		"Group":   pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"S": pdf.PDFName{Value: "Transparency"}})},
	}
	if resources != nil {
		entries["Resources"] = resources
	}
	return pdf.PDFDict{
		Entries:   pdf.DictOf(entries),
		HasStream: true,
		RawStream: []byte("q /GS0 gs 20 0 0 20 0 0 cm /Im0 Do Q"),
	}
}

// flattenedInk rasterizes what a flattened form now paints and returns how
// much of it is ink, so a form that came out empty can be told from one that
// kept its picture.
func flattenedInk(t *testing.T, form pdf.PDFDict) float64 {
	t.Helper()
	resources, ok := form.Entries.Get("Resources").(pdf.PDFDict)
	if !ok {
		t.Fatal("flattened form has no /Resources")
	}
	xobjects, ok := resources.Entries.Get("XObject").(pdf.PDFDict)
	if !ok {
		t.Fatal("flattened form's resources name no XObject")
	}
	img, ok := xobjects.Entries.Get("Im0").(pdf.PDFDict)
	if !ok {
		t.Fatal("flattened form paints no image")
	}
	decoded, err := DecodeImageRGBA(img, pdf.PDFDict{})
	if err != nil {
		t.Fatalf("decoding the flattened image: %v", err)
	}
	return inkFraction(decoded)
}

// TestFlattenFormUsesItsOwnResources is roadmap item 36's blanking cause: a
// form names what it draws in its own /Resources, so rasterizing it against
// the page's instead leaves every image, font and colour inside it
// unresolvable and replaces the form with a blank picture. The page it stood
// on then comes out empty, and conformance says nothing about it. Both files
// the item named are this shape -- a whole page inside one group form.
func TestFlattenFormUsesItsOwnResources(t *testing.T) {
	black := grayImage(2, 2, []byte{0, 0, 0, 0})
	own := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"XObject": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Im0": black})},
	})}
	form := groupFormDrawingIm0(own)
	// The page names the form and nothing else: /Im0 exists only inside it.
	xobjects := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Fm0": form})}
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Page"},
		"Resources": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"XObject": xobjects,
		})},
	})}
	pages := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"},
		"Kids": pdf.PDFArray{page},
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Pages": pages})},
	})}

	changed, err := (transparencyFlattener{}).Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true (the form carried a /Group)")
	}
	flattened, ok := xobjects.Entries.Get("Fm0").(pdf.PDFDict)
	if !ok {
		t.Fatal("the form was not written back into the page's resources")
	}
	if ink := flattenedInk(t, flattened); ink < 0.9 {
		t.Errorf("the flattened form carries %.3f ink, want the black image it drew", ink)
	}
}

// TestFlattenFormWithoutOwnResourcesInherits is the other half: a form with no
// /Resources of its own draws from the ones around it, as the renderer does.
func TestFlattenFormWithoutOwnResourcesInherits(t *testing.T) {
	black := grayImage(2, 2, []byte{0, 0, 0, 0})
	form := groupFormDrawingIm0(nil)
	xobjects := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Fm0": form, "Im0": black})}
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Page"},
		"Resources": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"XObject": xobjects,
		})},
	})}
	pages := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"},
		"Kids": pdf.PDFArray{page},
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Pages": pages})},
	})}

	if _, err := (transparencyFlattener{}).Fix(&trailer, nil); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	flattened, ok := xobjects.Entries.Get("Fm0").(pdf.PDFDict)
	if !ok {
		t.Fatal("the form was not written back into the page's resources")
	}
	if ink := flattenedInk(t, flattened); ink < 0.9 {
		t.Errorf("the flattened form carries %.3f ink, want the page's image it drew", ink)
	}
}

// TestFlattenAppearanceFormUsesItsOwnResources: the same, for a form reached
// through an annotation's appearance rather than the page's resources.
func TestFlattenAppearanceFormUsesItsOwnResources(t *testing.T) {
	black := grayImage(2, 2, []byte{0, 0, 0, 0})
	own := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"XObject": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Im0": black})},
	})}
	form := groupFormDrawingIm0(own)
	ap := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"N": form})}
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Page"},
		"Annots": pdf.PDFArray{pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Subtype": pdf.PDFName{Value: "Widget"},
			"AP":      ap,
		})}},
	})}
	pages := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"},
		"Kids": pdf.PDFArray{page},
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Pages": pages})},
	})}

	if _, err := (transparencyFlattener{}).Fix(&trailer, nil); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	flattened, ok := ap.Entries.Get("N").(pdf.PDFDict)
	if !ok {
		t.Fatal("the appearance was not written back")
	}
	if ink := flattenedInk(t, flattened); ink < 0.9 {
		t.Errorf("the flattened appearance carries %.3f ink, want the image it drew", ink)
	}
}

// TestCollectAnnotationTargetsSkipsMalformed: annotation entries that are not
// dictionaries, or that carry no /AP, are stepped over rather than aborting
// the walk of the rest of the array.
func TestCollectAnnotationTargetsSkipsMalformed(t *testing.T) {
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Page"},
		"Annots": pdf.PDFArray{
			pdf.PDFInteger(1), // not a dictionary
			pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Link"}})}, // no /AP
		},
	})}
	var out []flaggedTarget
	collectAnnotationTargets(page, pdf.PDFDict{}, map[uintptr]bool{}, 1, &out)
	if len(out) != 0 {
		t.Errorf("targets = %+v, want none", out)
	}

	// A page with no /Annots at all is equally fine.
	collectAnnotationTargets(pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}, pdf.PDFDict{}, map[uintptr]bool{}, 1, &out)
	if len(out) != 0 {
		t.Errorf("targets = %+v, want none", out)
	}
}

// TestFlattenUnrenderableLeavesObjectUntouched: a Form or Page the rasterizer
// cannot draw must be left exactly as it was. Reporting "fixed" on an object
// that was never rewritten would let the verify/fix loop believe it made
// progress and converge on a document still carrying its /Group.
func TestFlattenUnrenderableLeavesObjectUntouched(t *testing.T) {
	group := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"S": pdf.PDFName{Value: "Transparency"}})}

	// A Form with no /BBox cannot be rendered in isolation.
	form := pdf.PDFDict{
		Entries:   pdf.DictOf(map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Form"}, "Group": group}),
		HasStream: true,
		RawStream: []byte("q Q"),
	}
	if _, _, ok := flattenFormToImage(form, pdf.PDFDict{}, 150); ok {
		t.Error("flattenFormToImage on a Form with no /BBox reported success")
	}
	if form.Entries.Get("Group") == nil {
		t.Error("a failed form flatten dropped /Group anyway")
	}

	// A page whose media box has no area cannot be rendered either.
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":  pdf.PDFName{Value: "Page"},
		"Group": group,
	})}
	if _, ok := flattenPageToImage(page, pdf.PDFDict{}, [4]float64{0, 0, 0, 0}, 150); ok {
		t.Error("flattenPageToImage on a zero-area media box reported success")
	}
	if page.Entries.Get("Group") == nil {
		t.Error("a failed page flatten dropped /Group anyway")
	}
}

// TestTransparencyFlattenerSkipsUnfixableTargets: Fix reports changed=false
// when every flagged target failed to flatten, so the fix pass does not record
// a no-op iteration as progress.
func TestTransparencyFlattenerSkipsUnfixableTargets(t *testing.T) {
	group := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"S": pdf.PDFName{Value: "Transparency"}})}
	// No /BBox, and content the safety check refuses to call transparency-free,
	// so neither dropping the group nor flattening is available.
	form := pdf.PDFDict{
		Entries:   pdf.DictOf(map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Form"}, "Group": group}),
		HasStream: true,
		RawStream: []byte("/GS0 gs"),
	}
	xobjects := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Fm0": form})}
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":      pdf.PDFName{Value: "Page"},
		"MediaBox":  pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(20), pdf.PDFInteger(20)},
		"Resources": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"XObject": xobjects})},
	})}
	pages := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"},
		"Kids": pdf.PDFArray{page},
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Pages": pages})},
	})}

	changed, err := (transparencyFlattener{}).Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if changed {
		t.Error("changed = true although no target could be flattened")
	}
	if form.Entries.Get("Group") == nil {
		t.Error("/Group was dropped from a form that was never flattened")
	}
}

// TestUniqueByDictCollapsesAliases: one Form reachable under two resource
// names is a single object, so it must be flattened once and the result
// written back to both slots.
func TestUniqueByDictCollapsesAliases(t *testing.T) {
	shared := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Form"}})}
	other := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Form"}})}

	targets := []flaggedTarget{
		{kind: "form", dict: shared, name: "Fm0"},
		{kind: "form", dict: shared, name: "Fm1"},
		{kind: "form", dict: other, name: "Fm2"},
	}
	got := uniqueByDict(targets)
	if len(got) != 2 {
		t.Fatalf("uniqueByDict returned %d targets, want 2", len(got))
	}
	if got[0].name != "Fm0" || got[1].name != "Fm2" {
		t.Errorf("uniqueByDict kept %q and %q, want the first of each object", got[0].name, got[1].name)
	}
}

// TestBakeStraySoftMasksReachesOutsideThePageTree covers the image no page
// walk can see: a Photoshop /PieceInfo keeps a composite copy with its soft
// mask, and 6.4 is reported against it like any other image dictionary. No
// amount of rasterizing the pages it is not on would remove it.
func TestBakeStraySoftMasksReachesOutsideThePageTree(t *testing.T) {
	img := grayImage(2, 2, []byte{10, 20, 30, 40})
	img.Entries.Set("SMask", grayImage(2, 2, []byte{0, 128, 255, 64}))

	private := pdf.NewPDFDict()
	private.Entries.Set("CompositeImage", img)
	photoshop := pdf.NewPDFDict()
	photoshop.Entries.Set("Private", private)
	pieceInfo := pdf.NewPDFDict()
	pieceInfo.Entries.Set("AdobePhotoshop", photoshop)

	page := pdf.NewPDFDict()
	page.Entries.Set("Type", pdf.PDFName{Value: "Page"})
	page.Entries.Set("PieceInfo", pieceInfo)
	pages := pdf.NewPDFDict()
	pages.Entries.Set("Type", pdf.PDFName{Value: "Pages"})
	pages.Entries.Set("Kids", pdf.PDFArray{page})
	catalog := pdf.NewPDFDict()
	catalog.Entries.Set("Pages", pages)
	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", catalog)

	changed, err := (transparencyFlattener{}).Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatal("Fix reported no change for a soft mask outside the page resources")
	}
	got, _ := private.Entries.Get("CompositeImage").(pdf.PDFDict)
	if _, still := got.Entries.Lookup("SMask"); still {
		t.Error("/SMask survived on an image reached through /PieceInfo")
	}
	if changed, _ := (transparencyFlattener{}).Fix(&trailer, nil); changed {
		t.Error("second pass reported a change, want idempotent")
	}
}

// --- zero opacity (roadmap item 35) ---

// alphaStates is the resource dictionary the tests below draw with: /F0 puts
// the fill opacity at zero, /S0 the stroke opacity, /B0 both, /Half puts the
// fill opacity half way and /SHalf the stroke opacity.
func alphaStates() pdf.PDFDict {
	state := func(entries map[string]pdf.PDFValue) pdf.PDFDict {
		return pdf.PDFDict{Entries: pdf.DictOf(entries)}
	}
	return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"ExtGState": state(map[string]pdf.PDFValue{
			"F0":    state(map[string]pdf.PDFValue{"ca": pdf.PDFReal(0)}),
			"S0":    state(map[string]pdf.PDFValue{"CA": pdf.PDFReal(0)}),
			"B0":    state(map[string]pdf.PDFValue{"ca": pdf.PDFReal(0), "CA": pdf.PDFReal(0)}),
			"Half":  state(map[string]pdf.PDFValue{"ca": pdf.PDFReal(0.5)}),
			"SHalf": state(map[string]pdf.PDFValue{"CA": pdf.PDFReal(0.5)}),
			"Back":  state(map[string]pdf.PDFValue{"ca": pdf.PDFReal(1), "CA": pdf.PDFReal(1)}),
		}),
	})}
}

// contentStream is an unfiltered stream dictionary holding content.
func contentStream(content string) pdf.PDFDict {
	return pdf.PDFDict{Entries: pdf.NewPDFDict().Entries, HasStream: true, RawStream: []byte(content)}
}

// streamText decodes a stream dictionary back to its operators.
func streamText(t *testing.T, d pdf.PDFDict) string {
	t.Helper()
	data, err := pdf.DecodeStream(d)
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	return string(data)
}

// onePageAlphaTrailer wraps one page's content and resources in the smallest
// graph orderedPages will walk.
func onePageAlphaTrailer(contents pdf.PDFValue, resources pdf.PDFDict) (trailer, page pdf.PDFDict) {
	page = pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":      pdf.PDFName{Value: "Page"},
		"Resources": resources,
		"Contents":  contents,
	})}
	pages := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"},
		"Kids": pdf.PDFArray{page},
	})}
	root := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Pages": pages})}
	trailer = pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Root": root})}
	return trailer, page
}

// dropFromPage runs the repair over a one-page document and returns the page's
// content afterwards.
func dropFromPage(t *testing.T, content string, resources pdf.PDFDict) (string, bool) {
	t.Helper()
	trailer, page := onePageAlphaTrailer(contentStream(content), resources)
	changed := repairOpacity(&trailer)
	contents, ok := page.Entries.Get("Contents").(pdf.PDFDict)
	if !ok {
		t.Fatalf("page /Contents is no longer a stream dict")
	}
	return streamText(t, contents), changed
}

// TestDropInvisiblePaintOperators walks every path-painting operator under
// each combination of hidden opacities: what is still visible keeps being
// painted, and what is not falls back to the operator that paints nothing.
func TestDropInvisiblePaintOperators(t *testing.T) {
	for _, tc := range []struct{ gs, op, want string }{
		{"F0", "f", "n"},
		{"F0", "F", "n"},
		{"F0", "f*", "n"},
		{"F0", "S", "S"}, // the stroke is still opaque
		{"F0", "B", "S"},
		{"F0", "B*", "S"},
		{"F0", "b", "s"},
		{"F0", "b*", "s"},
		{"S0", "S", "n"},
		{"S0", "s", "n"},
		{"S0", "f", "f"},
		{"S0", "B", "f"},
		{"S0", "B*", "f*"},
		{"S0", "b", "f"},
		{"S0", "b*", "f*"},
		{"B0", "f", "n"},
		{"B0", "S", "n"},
		{"B0", "B", "n"},
		{"B0", "b*", "n"},
		{"B0", "n", "n"},
		{"Half", "f", "f"}, // nothing set a colour here, so there is none to blend
		{"Back", "f", "f"},
	} {
		t.Run(tc.gs+"-"+tc.op, func(t *testing.T) {
			got, changed := dropFromPage(t, "/"+tc.gs+" gs\n0 0 10 10 re\n"+tc.op+"\n", alphaStates())
			want := "/" + tc.gs + " gs\n0 0 10 10 re\n" + tc.want + "\n"
			if tc.want == tc.op {
				// Nothing to take out, so the stream is left exactly as written.
				if changed {
					t.Errorf("changed = true for a visible %s under /%s", tc.op, tc.gs)
				}
				return
			}
			if !changed {
				t.Errorf("changed = false, want the %s taken out", tc.op)
			}
			if got != want {
				t.Errorf("content = %q, want %q", got, want)
			}
		})
	}
}

// TestDropInvisibleKeepsClipping: the clip is set by the path, not by the
// operator that paints it, so an invisible fill must still leave the clip --
// dropping the whole path object would take the rest of the page with it.
func TestDropInvisibleKeepsClipping(t *testing.T) {
	got, changed := dropFromPage(t, "/F0 gs\n0 0 10 10 re\nW\nf\n1 0 0 rg\n0 0 5 5 re\nf\n", alphaStates())
	if !changed {
		t.Fatal("changed = false, want the invisible fill taken out")
	}
	want := "/F0 gs\n0 0 10 10 re\nW\nn\n1 0 0 rg\n0 0 5 5 re\nn\n"
	if got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

// TestDropInvisibleRestoresState: a hidden opacity lasts until the state that
// set it is restored, and no longer -- Q puts back what q saved, and a later
// graphics state can set the opacity back to 1 itself.
func TestDropInvisibleRestoresState(t *testing.T) {
	got, changed := dropFromPage(t,
		"q\n/F0 gs\n0 0 1 1 re\nf\nQ\n0 0 2 2 re\nf\n/F0 gs\n0 0 3 3 re\nf\n/Back gs\n0 0 4 4 re\nf\n",
		alphaStates())
	if !changed {
		t.Fatal("changed = false, want the two hidden fills taken out")
	}
	want := "q\n/F0 gs\n0 0 1 1 re\nn\nQ\n0 0 2 2 re\nf\n/F0 gs\n0 0 3 3 re\nn\n/Back gs\n0 0 4 4 re\nf\n"
	if got != want {
		t.Errorf("content = %q, want %q", got, want)
	}

	// An unbalanced Q is malformed content, and must not take the rewriter's
	// state stack down with it.
	if _, changed := dropFromPage(t, "Q\n/F0 gs\n0 0 1 1 re\nf\n", alphaStates()); !changed {
		t.Error("changed = false after a stray Q, want the hidden fill still taken out")
	}
}

// TestDropInvisibleText: text drawn at zero opacity keeps its characters and
// its position and stops marking the page, so it is still there to be read,
// copied and searched. The clipping render modes go to 7 rather than 3, since
// text that clips is still clipping.
func TestDropInvisibleText(t *testing.T) {
	for _, tc := range []struct {
		gs     string
		mode   string
		hidden bool
		want   int
	}{
		{"F0", "0", true, 3},
		{"F0", "2", false, 0}, // mode 2 also strokes, and the stroke is visible
		{"F0", "4", true, 7},
		{"F0", "3", false, 0}, // already marks nothing
		{"F0", "7", false, 0},
		{"S0", "1", true, 3},
		{"S0", "0", false, 0},
		{"S0", "5", true, 7},
		{"B0", "2", true, 3},
		{"B0", "6", true, 7},
		{"Half", "0", false, 0},
	} {
		t.Run(tc.gs+"-Tr"+tc.mode, func(t *testing.T) {
			content := "/" + tc.gs + " gs\nBT\n" + tc.mode + " Tr\n(hi) Tj\nET\n"
			got, changed := dropFromPage(t, content, alphaStates())
			if changed != tc.hidden {
				t.Fatalf("changed = %v, want %v (content %q)", changed, tc.hidden, got)
			}
			if !tc.hidden {
				return
			}
			want := "/" + tc.gs + " gs\nBT\n" + tc.mode + " Tr\n" +
				strconv.Itoa(tc.want) + " Tr\n(hi) Tj\n" + tc.mode + " Tr\nET\n"
			if got != want {
				t.Errorf("content = %q, want %q", got, want)
			}
		})
	}

	// Every showing operator is covered, including the two that also move to
	// the next line.
	for _, op := range []string{"(hi) Tj", "[(hi)] TJ", "(hi) '", "1 2 (hi) \""} {
		got, changed := dropFromPage(t, "/F0 gs\nBT\n"+op+"\nET\n", alphaStates())
		if !changed {
			t.Errorf("%s at zero opacity was left marking the page: %q", op, got)
		}
	}
}

// TestDropInvisibleImagesAndShadings: an image, a shading and an inline image
// are painted with the fill opacity, so at zero they are dropped outright. A
// form can fill and stroke, so it only goes when neither is visible.
func TestDropInvisibleImagesAndShadings(t *testing.T) {
	resources := alphaStates()
	xobjects := pdf.DictOf(map[string]pdf.PDFValue{
		"Im0": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Image"}})},
		"Fm0": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Form"}})},
	})
	resources.Entries.Set("XObject", pdf.PDFDict{Entries: xobjects})

	inline := "BI /W 1 /H 1 /BPC 8 /CS /G ID \x00 EI\n"
	for _, tc := range []struct {
		gs, draw string
		dropped  bool
	}{
		{"F0", "/Sh0 sh\n", true},
		{"F0", "/Im0 Do\n", true},
		{"F0", inline, true},
		{"F0", "/Fm0 Do\n", false}, // its strokes are still visible
		{"B0", "/Fm0 Do\n", true},  // nothing it draws can be seen
		{"B0", "/Miss Do\n", true}, // an unresolvable name is treated as a form
		{"S0", "/Im0 Do\n", false}, // images do not stroke
		{"S0", "/Sh0 sh\n", false},
		{"Half", "/Im0 Do\n", false},
	} {
		t.Run(tc.gs+"-"+strings.TrimSpace(tc.draw), func(t *testing.T) {
			got, changed := dropFromPage(t, "/"+tc.gs+" gs\n"+tc.draw, resources)
			if changed != tc.dropped {
				t.Fatalf("changed = %v, want %v (content %q)", changed, tc.dropped, got)
			}
			if tc.dropped && got != "/"+tc.gs+" gs\n" {
				t.Errorf("content = %q, want only the gs left", got)
			}
		})
	}
}

// TestDropInvisibleGate: a document that draws nothing at zero opacity is not
// read at all, and one that does is left alone the second time round -- the
// repair runs on every pass of the fix loop.
func TestDropInvisibleGate(t *testing.T) {
	trailer, page := onePageAlphaTrailer(contentStream("/Half gs\n0 0 10 10 re\nf\n"), alphaStates())
	if repairOpacity(&trailer) {
		t.Error("changed = true for a fill in no colour at all")
	}

	trailer, page = onePageAlphaTrailer(contentStream("/F0 gs\n0 0 10 10 re\nf\n"), alphaStates())
	if !repairOpacity(&trailer) {
		t.Fatal("changed = false, want the invisible fill taken out")
	}
	if repairOpacity(&trailer) {
		t.Error("second run reported a change, want nothing left to take out")
	}
	contents, _ := page.Entries.Get("Contents").(pdf.PDFDict)
	if got := streamText(t, contents); got != "/F0 gs\n0 0 10 10 re\nn\n" {
		t.Errorf("content = %q after two runs, want one rewrite", got)
	}

	// A stream nothing can decode is left as it is rather than emptied.
	broken := pdf.PDFDict{
		Entries:   pdf.DictOf(map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "FlateDecode"}}),
		HasStream: true,
		RawStream: []byte("not zlib"),
	}
	trailer, page = onePageAlphaTrailer(broken, alphaStates())
	// The graphics state has to be reachable for the gate to open at all.
	page.Entries.Set("Resources", alphaStates())
	if repairOpacity(&trailer) {
		t.Error("changed = true for an undecodable content stream")
	}
}

// TestDropInvisibleAcrossContentParts: a page's content can be written as
// several streams, which are one stream split up -- an opacity set in the
// first part is still in force in the second.
func TestDropInvisibleAcrossContentParts(t *testing.T) {
	first, second := contentStream("/F0 gs\n"), contentStream("0 0 10 10 re\nf\n")
	trailer, page := onePageAlphaTrailer(pdf.PDFArray{first, second}, alphaStates())
	if !repairOpacity(&trailer) {
		t.Fatal("changed = false, want the fill in the second part taken out")
	}
	parts, _ := page.Entries.Get("Contents").(pdf.PDFArray)
	if len(parts) != 2 {
		t.Fatalf("Contents = %v, want two parts", parts)
	}
	if got := streamText(t, parts[1].(pdf.PDFDict)); got != "0 0 10 10 re\nn\n" {
		t.Errorf("second part = %q, want the fill taken out", got)
	}
}

// --- partial opacity (roadmap item 36) ---

// TestFadePartialOpacityColours: a drawing at a partial opacity keeps what it
// looks like over white paper, in the space the file named its colour in --
// blended towards white where a larger number is lighter, and towards nothing
// where it is more ink. The colour goes back afterwards, since the rest of the
// stream still paints in the one the file set.
func TestFadePartialOpacityColours(t *testing.T) {
	icc := func(n int) pdf.PDFValue {
		return pdf.PDFArray{pdf.PDFName{Value: "ICCBased"},
			pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"N": pdf.PDFInteger(n)})}}
	}
	resources := alphaStates()
	resources.Entries.Set("ColorSpace", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Rgb":     icc(3),
		"Cmyk":    icc(4),
		"Indexed": pdf.PDFArray{pdf.PDFName{Value: "Indexed"}, pdf.PDFName{Value: "DeviceRGB"}, pdf.PDFInteger(1)},
	})})

	for _, tc := range []struct{ name, colour, faded string }{
		{"grey", "0 g", "0.5 g"},
		{"rgb", "0 0 0 rg", "0.5 0.5 0.5 rg"},
		{"cmyk", "0 0 0 1 k", "0 0 0 0.5 k"},
		{"icc rgb", "/Rgb cs\n1 0 0 sc", "1 0.5 0.5 sc"},
		{"icc cmyk", "/Cmyk cs\n0 0 0 1 scn", "0 0 0 0.5 scn"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := dropFromPage(t, "/Half gs\n"+tc.colour+"\n0 0 10 10 re\nf\n", resources)
			if !changed {
				t.Fatalf("changed = false, want the colour blended (content %q)", got)
			}
			last := tc.colour[strings.LastIndex(tc.colour, "\n")+1:]
			want := "/Half gs\n" + tc.colour + "\n0 0 10 10 re\n" + tc.faded + "\nf\n" + last + "\n"
			if got != want {
				t.Errorf("content = %q, want %q", got, want)
			}
		})
	}

	// A colour this cannot work out is left as it is: an indexed space (the
	// number is a position in a table, not an amount of anything) and a
	// pattern (there is no number at all).
	for _, colour := range []string{"/Indexed cs\n3 sc", "/P0 scn"} {
		if got, changed := dropFromPage(t, "/Half gs\n"+colour+"\n0 0 10 10 re\nf\n", resources); changed {
			t.Errorf("colour %q was blended: %q", colour, got)
		}
	}
}

// TestFadePartialOpacityStrokeAndText: the stroking opacity blends the
// stroking colour, and text blends whichever of the two its render mode uses.
func TestFadePartialOpacityStrokeAndText(t *testing.T) {
	got, changed := dropFromPage(t, "/SHalf gs\n0 0 0 RG\n0 0 m 10 10 l\nS\n", alphaStates())
	if !changed {
		t.Fatalf("changed = false, want the stroke blended (content %q)", got)
	}
	if want := "/SHalf gs\n0 0 0 RG\n0 0 m\n10 10 l\n0.5 0.5 0.5 RG\nS\n0 0 0 RG\n"; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}

	// Mode 1 strokes its text and does not fill it, so the fill opacity leaves
	// it alone; mode 0 fills it.
	if got, changed := dropFromPage(t, "/Half gs\n0 0 0 rg\nBT\n1 Tr\n(hi) Tj\nET\n", alphaStates()); changed {
		t.Errorf("stroked text was blended with the fill opacity: %q", got)
	}
	got, changed = dropFromPage(t, "/Half gs\n0 0 0 rg\nBT\n(hi) Tj\nET\n", alphaStates())
	if !changed {
		t.Fatalf("changed = false, want the filled text blended (content %q)", got)
	}
	if want := "/Half gs\n0 0 0 rg\nBT\n0.5 0.5 0.5 rg\n(hi) Tj\n0 0 0 rg\nET\n"; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

// TestFadePartialOpacityKeepsTheZeroCase: zero opacity is not a colour to
// blend -- white paint over the page is as wrong as black -- so it stays the
// drawing that is taken out.
func TestFadePartialOpacityKeepsTheZeroCase(t *testing.T) {
	got, changed := dropFromPage(t, "/F0 gs\n0 0 0 rg\n0 0 10 10 re\nf\n", alphaStates())
	if !changed {
		t.Fatal("changed = false, want the invisible fill taken out")
	}
	if want := "/F0 gs\n0 0 0 rg\n0 0 10 10 re\nn\n"; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

// TestDropInvisibleBeyondPageContents: everything else that draws is read the
// same way, with the resources it names itself -- a form XObject (which is
// also how an annotation's appearance is drawn), a tiling pattern and a Type 3
// glyph.
func TestDropInvisibleBeyondPageContents(t *testing.T) {
	form := pdf.PDFDict{
		Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Subtype":   pdf.PDFName{Value: "Form"},
			"Resources": alphaStates(),
		}),
		HasStream: true,
		RawStream: []byte("/F0 gs\n0 0 10 10 re\nf\n"),
	}
	pattern := pdf.PDFDict{
		Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"PatternType": pdf.PDFInteger(1),
			"Resources":   alphaStates(),
		}),
		HasStream: true,
		RawStream: []byte("/B0 gs\n0 0 10 10 re\nB\n"),
	}
	glyph := pdf.PDFDict{HasStream: true, RawStream: []byte("/S0 gs\n0 0 10 10 re\nS\n"), Entries: pdf.NewPDFDict().Entries}
	font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Subtype":   pdf.PDFName{Value: "Type3"},
		"Resources": alphaStates(),
		"CharProcs": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"a": glyph})},
	})}

	annot := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Annot"},
		"AP":   pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"N": form})},
	})}
	trailer, page := onePageAlphaTrailer(contentStream("0 0 1 1 re\nf\n"), alphaStates())
	page.Entries.Set("Annots", pdf.PDFArray{annot})
	page.Entries.Set("Pattern", pattern)
	page.Entries.Set("Font", font)

	if !repairOpacity(&trailer) {
		t.Fatal("changed = false, want all three streams rewritten")
	}
	ap, _ := annot.Entries.Get("AP").(pdf.PDFDict)
	if got := streamText(t, ap.Entries.Get("N").(pdf.PDFDict)); got != "/F0 gs\n0 0 10 10 re\nn\n" {
		t.Errorf("appearance stream = %q, want the fill taken out", got)
	}
	if got := streamText(t, page.Entries.Get("Pattern").(pdf.PDFDict)); got != "/B0 gs\n0 0 10 10 re\nn\n" {
		t.Errorf("pattern = %q, want the fill and stroke taken out", got)
	}
	procs, _ := page.Entries.Get("Font").(pdf.PDFDict).Entries.Get("CharProcs").(pdf.PDFDict)
	if got := streamText(t, procs.Entries.Get("a").(pdf.PDFDict)); got != "/S0 gs\n0 0 10 10 re\nn\n" {
		t.Errorf("Type 3 glyph = %q, want the stroke taken out", got)
	}
	// The page's own content had nothing invisible in it and is untouched.
	if got := streamText(t, page.Entries.Get("Contents").(pdf.PDFDict)); got != "0 0 1 1 re\nf\n" {
		t.Errorf("page content = %q, want it unchanged", got)
	}
}

// TestDropInvisibleMalformedOperands: the operands a real file carries are not
// always the ones the operator takes, and none of them may crash the walk or
// make it drop something visible.
func TestDropInvisibleMalformedOperands(t *testing.T) {
	resources := alphaStates()
	// A graphics state with no name, a name that resolves to nothing, an
	// opacity that is not a number, and a render mode that is not a number.
	for _, content := range []string{
		"gs\n0 0 1 1 re\nf\n",
		"/Missing gs\n0 0 1 1 re\nf\n",
		"/Text gs\nBT\n(hi) Tj\nET\n",
		"/F0 gs\nBT\n/x Tr\n(hi) Tj\nET\n",
	} {
		if got, changed := dropFromPage(t, content, resources); changed && got == "" {
			t.Errorf("content %q was emptied", content)
		}
	}

	// A graphics state whose opacity is a string leaves the opacity as it was.
	resources.Entries.Get("ExtGState").(pdf.PDFDict).Entries.Set("Text",
		pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"ca": pdf.PDFString{Value: "0"}})})
	if got, changed := dropFromPage(t, "/Text gs\n0 0 1 1 re\nf\n", resources); changed {
		t.Errorf("a non-numeric opacity took the fill out: %q", got)
	}
	// ... and an opacity of zero on something that is not a graphics state --
	// an annotation carries its own /CA -- is not this repair's business.
	annot := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Annot"},
		"CA":   pdf.PDFReal(0),
	})}
	trailer, _ := onePageAlphaTrailer(contentStream("0 0 1 1 re\nf\n"), pdf.NewPDFDict())
	trailer.Entries.Set("Stray", annot)
	if hasTransparentExtGState(trailer) {
		t.Error("an annotation opacity of zero opened the gate, want only graphics states")
	}
}

// TestDropInvisibleOddGraphShapes: the walk meets whatever a real file holds,
// so every shape it can be handed has to leave it unmoved rather than in a
// panic or with the drawing gone.
func TestDropInvisibleOddGraphShapes(t *testing.T) {
	// A page whose content is a dictionary with no stream, and an array whose
	// entries are not streams either.
	for _, contents := range []pdf.PDFValue{
		pdf.PDFDict{Entries: pdf.NewPDFDict().Entries},
		pdf.PDFArray{pdf.PDFInteger(1), pdf.PDFDict{Entries: pdf.NewPDFDict().Entries}},
		pdf.PDFName{Value: "not-content"},
	} {
		trailer, _ := onePageAlphaTrailer(contents, alphaStates())
		if repairOpacity(&trailer) {
			t.Errorf("changed = true for contents %T with nothing to read", contents)
		}
	}

	// A Type 3 font whose /CharProcs is missing, is not a dictionary, or holds
	// something that is not a glyph stream.
	for _, procs := range []pdf.PDFValue{
		nil,
		pdf.PDFInteger(1),
		pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"a": pdf.PDFInteger(1),
			"b": pdf.PDFDict{Entries: pdf.NewPDFDict().Entries},
		})},
	} {
		font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Subtype":   pdf.PDFName{Value: "Type3"},
			"Resources": alphaStates(),
		})}
		if procs != nil {
			font.Entries.Set("CharProcs", procs)
		}
		trailer, page := onePageAlphaTrailer(contentStream("0 0 1 1 re\nf\n"), alphaStates())
		page.Entries.Set("Font", font)
		if repairOpacity(&trailer) {
			t.Errorf("changed = true for /CharProcs %T with no glyph to read", procs)
		}
	}
}

// TestDropInvisibleUnreadableNames: an operator that selects a resource can
// carry the wrong operands, or none, and must then select nothing at all.
func TestDropInvisibleUnreadableNames(t *testing.T) {
	r := &alphaRewriter{resources: alphaStates()}
	for _, operands := range [][]pdf.PDFValue{
		nil,
		{pdf.PDFInteger(1)},
	} {
		if _, ok := r.namedResource("ExtGState", operands); ok {
			t.Errorf("operands %v selected a graphics state", operands)
		}
	}
	// A category the resources do not have at all.
	if _, ok := r.namedResource("XObject", []pdf.PDFValue{pdf.PDFName{Value: "Im0"}}); ok {
		t.Error("an absent resource category still selected something")
	}
}
