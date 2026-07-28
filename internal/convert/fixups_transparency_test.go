package convert

import (
	"bytes"
	"image"
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
		return pdf.PDFDict{Entries: map[string]pdf.PDFValue{}, HasStream: true, RawStream: []byte(content)}
	}

	// Flate with bytes that are not a zlib stream at all: a genuine decode
	// failure, so the guard must refuse to judge the content.
	if canDropGroupSafely(pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "FlateDecode"}}, HasStream: true, RawStream: []byte("not zlib")}) {
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
	group := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"S": pdf.PDFName{Value: "Transparency"}}}
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type":  pdf.PDFName{Value: "Page"},
		"Group": group,
	}}
	pages := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"},
		"Kids": pdf.PDFArray{page},
	}}
	root := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Pages": pages}}
	trailer := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Root": root}}

	changed, err := (transparencyFlattener{}).Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true (page had a /Group)")
	}
	if page.Entries["Group"] != nil {
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
		return pdf.PDFDict{Entries: map[string]pdf.PDFValue{"S": pdf.PDFName{Value: "Transparency"}}}
	}
	// A form whose content is provably transparency-free, so the fixer can drop
	// the group outright rather than rasterizing it.
	form := func(g pdf.PDFDict) pdf.PDFDict {
		return pdf.PDFDict{
			Entries: map[string]pdf.PDFValue{
				"Type":    pdf.PDFName{Value: "XObject"},
				"Subtype": pdf.PDFName{Value: "Form"},
				"BBox":    pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(10), pdf.PDFInteger(10)},
				"Group":   g,
			},
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
		Entries: map[string]pdf.PDFValue{
			"Type":    pdf.PDFName{Value: "XObject"},
			"Subtype": pdf.PDFName{Value: "Form"},
			"Resources": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
				"XObject": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Fm1": nested}},
			}},
		},
		HasStream: true,
		RawStream: []byte("/Fm1 Do"),
	}

	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Page"},
		"Annots": pdf.PDFArray{
			pdf.PDFDict{Entries: map[string]pdf.PDFValue{
				"Subtype": pdf.PDFName{Value: "Widget"},
				"AP":      pdf.PDFDict{Entries: map[string]pdf.PDFValue{"N": direct}},
			}},
			pdf.PDFDict{Entries: map[string]pdf.PDFValue{
				"Subtype": pdf.PDFName{Value: "Widget"},
				"AP": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
					"N": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"On": stated}},
				}},
			}},
			// The signature shape: an appearance form with no group of its own,
			// whose nested Form XObject carries one.
			pdf.PDFDict{Entries: map[string]pdf.PDFValue{
				"Subtype": pdf.PDFName{Value: "Widget"},
				"AP":      pdf.PDFDict{Entries: map[string]pdf.PDFValue{"N": outer}},
			}},
		},
	}}
	pages := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"},
		"Kids": pdf.PDFArray{page},
	}}
	trailer := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Pages": pages}},
	}}

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
		if f.Entries["Group"] != nil {
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
		Entries: map[string]pdf.PDFValue{
			"Type":    pdf.PDFName{Value: "XObject"},
			"Subtype": pdf.PDFName{Value: "Form"},
			"BBox":    pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(20), pdf.PDFInteger(20)},
			"Group": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
				"S": pdf.PDFName{Value: "Transparency"},
			}},
		},
		HasStream: true,
		// The gs keeps canDropGroupSafely from skipping the rasterization.
		RawStream: []byte("q /GS0 gs /Sh1 sh Q 0 0 0 rg 2 2 5 5 re f"),
	}
	xobjects := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Fm0": form}}
	resources := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"XObject": xobjects}}
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type":      pdf.PDFName{Value: "Page"},
		"Resources": resources,
	}}
	pages := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"},
		"Kids": pdf.PDFArray{page},
	}}
	root := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Pages": pages}}
	trailer := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Root": root}}

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
		Entries: map[string]pdf.PDFValue{
			"Type":    pdf.PDFName{Value: "XObject"},
			"Subtype": pdf.PDFName{Value: "Form"},
			"BBox":    pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(20), pdf.PDFInteger(20)},
			"Group": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
				"S": pdf.PDFName{Value: "Transparency"},
			}},
		},
		HasStream: true,
		RawStream: []byte("q /GS0 gs /Sh1 sh Q"),
	}
	xobjects := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Fm0": form}}
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Page"},
		"Resources": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"XObject": xobjects,
		}},
	}}
	pages := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"},
		"Kids": pdf.PDFArray{page},
	}}
	root := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Pages": pages}}
	trailer := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Root": root}}

	if _, err := (transparencyFlattener{}).Fix(&trailer, nil); err != nil {
		t.Fatalf("Fix without a drop sink: %v", err)
	}
}

// grayImage builds a minimal DeviceGray image XObject from its samples.
func grayImage(w, h int, samples []byte) pdf.PDFDict {
	return pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Type":             pdf.PDFName{Value: "XObject"},
			"Subtype":          pdf.PDFName{Value: "Image"},
			"Width":            pdf.PDFInteger(w),
			"Height":           pdf.PDFInteger(h),
			"BitsPerComponent": pdf.PDFInteger(8),
			"ColorSpace":       pdf.PDFName{Value: "DeviceGray"},
		},
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
		{"a stream dict", pdf.PDFDict{Entries: map[string]pdf.PDFValue{}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			img := pdf.PDFDict{Entries: map[string]pdf.PDFValue{}}
			if tc.smask != nil {
				img.Entries["SMask"] = tc.smask
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
			img.Entries["Filter"] = pdf.PDFName{Value: "FlateDecode"}
			img.Entries["SMask"] = mask
			return img
		}()},
		{"SMask is not a stream dict", func() pdf.PDFDict {
			img := grayImage(2, 2, []byte{1, 2, 3, 4})
			img.Entries["SMask"] = pdf.PDFName{Value: "Im1"}
			return img
		}()},
		{"undecodable SMask", func() pdf.PDFDict {
			bad := grayImage(2, 2, []byte{1, 2, 3, 4})
			bad.Entries["Filter"] = pdf.PDFName{Value: "FlateDecode"}
			img := grayImage(2, 2, []byte{1, 2, 3, 4})
			img.Entries["SMask"] = bad
			return img
		}()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := bakeSoftMaskOut(tc.img, pdf.PDFDict{})
			if ok {
				t.Fatal("bakeSoftMaskOut reported success on an undecodable pair")
			}
			if _, still := got.Entries["SMask"]; !still && tc.name != "SMask is not a stream dict" {
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
	img.Entries["SMask"] = grayImage(2, 2, []byte{255, 255, 255, 255})

	got, ok := bakeSoftMaskOut(img, pdf.PDFDict{})
	if !ok {
		t.Fatal("bakeSoftMaskOut on a fully-opaque mask reported no change")
	}
	if _, still := got.Entries["SMask"]; still {
		t.Error("/SMask survived a fully-opaque bake")
	}
	if !bytes.Equal(got.RawStream, samples) {
		t.Errorf("stream was re-encoded: % x, want the original % x", got.RawStream, samples)
	}
	if cs, _ := got.Entries["ColorSpace"].(pdf.PDFName); cs.Value != "DeviceGray" {
		t.Errorf("ColorSpace = %v, want the original DeviceGray", got.Entries["ColorSpace"])
	}
}

// TestBakeSoftMaskOutComposites: a partially transparent mask is composited
// against white and the image is rewritten flat and opaque.
func TestBakeSoftMaskOutComposites(t *testing.T) {
	img := grayImage(2, 1, []byte{0, 0}) // two black pixels
	img.Entries["SMask"] = grayImage(2, 1, []byte{255, 0})

	got, ok := bakeSoftMaskOut(img, pdf.PDFDict{})
	if !ok {
		t.Fatal("bakeSoftMaskOut reported no change")
	}
	if _, still := got.Entries["SMask"]; still {
		t.Error("/SMask survived the bake")
	}
	if cs, _ := got.Entries["ColorSpace"].(pdf.PDFName); cs.Value != "DeviceRGB" {
		t.Errorf("ColorSpace = %v, want DeviceRGB", got.Entries["ColorSpace"])
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
	noRoot := pdf.PDFDict{Entries: map[string]pdf.PDFValue{}}
	noPages := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: map[string]pdf.PDFValue{}},
	}}
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
	group := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"S": pdf.PDFName{Value: "Transparency"}}}

	// The innermost form carries the group; the form above it has no
	// /Resources of its own, so the walk must fall back to the parent's.
	inner := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type":    pdf.PDFName{Value: "XObject"},
		"Subtype": pdf.PDFName{Value: "Form"},
		"Group":   group,
	}}
	outer := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type":    pdf.PDFName{Value: "XObject"},
		"Subtype": pdf.PDFName{Value: "Form"},
	}}
	xobjects := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Fm0":     outer,
		"Fm1":     inner,
		"NotADic": pdf.PDFInteger(7),
	}}
	resources := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"XObject": xobjects}}

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

// TestCollectAnnotationTargetsSkipsMalformed: annotation entries that are not
// dictionaries, or that carry no /AP, are stepped over rather than aborting
// the walk of the rest of the array.
func TestCollectAnnotationTargetsSkipsMalformed(t *testing.T) {
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Page"},
		"Annots": pdf.PDFArray{
			pdf.PDFInteger(1), // not a dictionary
			pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Link"}}}, // no /AP
		},
	}}
	var out []flaggedTarget
	collectAnnotationTargets(page, map[uintptr]bool{}, 1, &out)
	if len(out) != 0 {
		t.Errorf("targets = %+v, want none", out)
	}

	// A page with no /Annots at all is equally fine.
	collectAnnotationTargets(pdf.PDFDict{Entries: map[string]pdf.PDFValue{}}, map[uintptr]bool{}, 1, &out)
	if len(out) != 0 {
		t.Errorf("targets = %+v, want none", out)
	}
}

// TestFlattenUnrenderableLeavesObjectUntouched: a Form or Page the rasterizer
// cannot draw must be left exactly as it was. Reporting "fixed" on an object
// that was never rewritten would let the verify/fix loop believe it made
// progress and converge on a document still carrying its /Group.
func TestFlattenUnrenderableLeavesObjectUntouched(t *testing.T) {
	group := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"S": pdf.PDFName{Value: "Transparency"}}}

	// A Form with no /BBox cannot be rendered in isolation.
	form := pdf.PDFDict{
		Entries:   map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Form"}, "Group": group},
		HasStream: true,
		RawStream: []byte("q Q"),
	}
	if _, _, ok := flattenFormToImage(form, pdf.PDFDict{}, 150); ok {
		t.Error("flattenFormToImage on a Form with no /BBox reported success")
	}
	if form.Entries["Group"] == nil {
		t.Error("a failed form flatten dropped /Group anyway")
	}

	// A page whose media box has no area cannot be rendered either.
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type":  pdf.PDFName{Value: "Page"},
		"Group": group,
	}}
	if _, ok := flattenPageToImage(page, pdf.PDFDict{}, [4]float64{0, 0, 0, 0}, 150); ok {
		t.Error("flattenPageToImage on a zero-area media box reported success")
	}
	if page.Entries["Group"] == nil {
		t.Error("a failed page flatten dropped /Group anyway")
	}
}

// TestTransparencyFlattenerSkipsUnfixableTargets: Fix reports changed=false
// when every flagged target failed to flatten, so the fix pass does not record
// a no-op iteration as progress.
func TestTransparencyFlattenerSkipsUnfixableTargets(t *testing.T) {
	group := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"S": pdf.PDFName{Value: "Transparency"}}}
	// No /BBox, and content the safety check refuses to call transparency-free,
	// so neither dropping the group nor flattening is available.
	form := pdf.PDFDict{
		Entries:   map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Form"}, "Group": group},
		HasStream: true,
		RawStream: []byte("/GS0 gs"),
	}
	xobjects := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Fm0": form}}
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type":      pdf.PDFName{Value: "Page"},
		"MediaBox":  pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(20), pdf.PDFInteger(20)},
		"Resources": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"XObject": xobjects}},
	}}
	pages := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Pages"},
		"Kids": pdf.PDFArray{page},
	}}
	trailer := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Pages": pages}},
	}}

	changed, err := (transparencyFlattener{}).Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if changed {
		t.Error("changed = true although no target could be flattened")
	}
	if form.Entries["Group"] == nil {
		t.Error("/Group was dropped from a form that was never flattened")
	}
}

// TestUniqueByDictCollapsesAliases: one Form reachable under two resource
// names is a single object, so it must be flattened once and the result
// written back to both slots.
func TestUniqueByDictCollapsesAliases(t *testing.T) {
	shared := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Form"}}}
	other := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Subtype": pdf.PDFName{Value: "Form"}}}

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
