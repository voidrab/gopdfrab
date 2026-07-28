package convert

import (
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
