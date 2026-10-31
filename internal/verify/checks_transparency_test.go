package verify

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// transparentPage returns a page drawing content with resources.
func transparentPage(content string, resources pdf.PDFDict) pdf.PDFDict {
	return kv("Type", name("Page"), "Contents", streamDict(content), "Resources", resources)
}

func TestPageContainsTransparency(t *testing.T) {
	half := kv("ca", pdf.PDFReal(0.5))
	halfStroke := kv("CA", pdf.PDFReal(0.5))
	gsRes := func(gs pdf.PDFDict) pdf.PDFDict { return kv("ExtGState", kv("GS0", gs)) }
	group := kv("S", name("Transparency"))
	transparentForm := streamDict("0 0 1 1 re f")
	transparentForm.Entries.Set("Subtype", name("Form"))
	transparentForm.Entries.Set("Group", group)
	opaqueForm := streamDict("0 0 1 1 re f")
	opaqueForm.Entries.Set("Subtype", name("Form"))
	innerForm := streamDict("/GS0 gs 0 0 1 1 re f")
	innerForm.Entries.Set("Subtype", name("Form"))
	innerForm.Entries.Set("Resources", gsRes(half))
	softImage := kv("Subtype", name("Image"), "SMask", streamDict(""))
	inDataImage := kv("Subtype", name("Image"), "SMaskInData", pdf.PDFInteger(1))
	plainImage := kv("Subtype", name("Image"))
	pattern := streamDict("/GS0 gs 0 0 1 1 re f")
	pattern.Entries.Set("PatternType", pdf.PDFInteger(1))
	pattern.Entries.Set("Resources", gsRes(half))
	shadingPattern := kv("PatternType", pdf.PDFInteger(2))
	glyph := streamDict("/GS0 gs 0 0 1 1 re f")
	type3 := kv("Subtype", name("Type3"),
		"CharProcs", kv("a", glyph, "b", streamDict("0 0 1 1 re f")),
		"Encoding", kv("Differences", pdf.PDFArray{pdf.PDFInteger(97), name("a"), name("b"), pdf.PDFInteger(300), name("z"), pdf.PDFReal(1)}),
		"Resources", gsRes(half))
	fontRes := kv("Font", kv("F1", type3, "F2", kv("Subtype", name("Type1"))))

	for _, tc := range []struct {
		desc, content string
		resources     pdf.PDFDict
		want          bool
	}{
		{"opaque fill", "0 0 1 1 re f", kv(), false},
		{"fill alpha", "/GS0 gs 0 0 1 1 re f", gsRes(half), true},
		{"fill alpha, integer", "/GS0 gs 0 0 1 1 re f", gsRes(kv("ca", pdf.PDFInteger(0))), true},
		{"fill alpha, stroke only", "/GS0 gs 0 0 m 1 1 l S", gsRes(half), false},
		{"stroke alpha, stroke", "/GS0 gs 0 0 m 1 1 l s", gsRes(halfStroke), true},
		{"stroke alpha, fill and stroke", "/GS0 gs 0 0 1 1 re B", gsRes(halfStroke), true},
		{"alpha restored by Q", "q /GS0 gs Q 0 0 1 1 re f", gsRes(half), false},
		{"unbalanced Q", "Q 0 0 1 1 re f", kv(), false},
		{"unknown graphics state", "/GS9 gs 0 0 1 1 re f", gsRes(half), false},
		{"soft mask", "/GS0 gs 0 0 1 1 re f", gsRes(kv("SMask", kv("S", name("Luminosity")))), true},
		{"soft mask None", "/GS0 gs 0 0 1 1 re f", gsRes(kv("SMask", name("None"))), false},
		{"blend mode", "/GS0 gs 0 0 1 1 re f", gsRes(kv("BM", name("Darken"))), true},
		{"blend mode Normal", "/GS0 gs 0 0 1 1 re f", gsRes(kv("BM", name("Normal"))), false},
		{"blend mode array Normal", "/GS0 gs 0 0 1 1 re f", gsRes(kv("BM", pdf.PDFArray{name("Normal")})), false},
		{"blend mode array", "/GS0 gs 0 0 1 1 re f", gsRes(kv("BM", pdf.PDFArray{name("Normal"), name("Darken")})), true},
		{"blend mode not a name", "/GS0 gs 0 0 1 1 re f", gsRes(kv("BM", pdf.PDFInteger(1))), true},
		{"shading under alpha", "/GS0 gs /Sh0 sh", gsRes(half), true},
		{"inline image under alpha", "/GS0 gs BI /W 1 /H 1 /BPC 8 /CS /G ID \x00 EI", gsRes(half), true},
		{"XObject under alpha", "/GS0 gs /X0 Do", gsRes(half), true},
		{"image with soft mask", "/X0 Do", kv("XObject", kv("X0", softImage)), true},
		{"image with soft mask in data", "/X0 Do", kv("XObject", kv("X0", inDataImage)), true},
		{"plain image", "/X0 Do", kv("XObject", kv("X0", plainImage)), false},
		{"unknown XObject", "/X9 Do", kv(), false},
		{"transparency group form", "/X0 Do", kv("XObject", kv("X0", transparentForm)), true},
		{"opaque form", "/X0 Do", kv("XObject", kv("X0", opaqueForm)), false},
		{"form with transparent content", "/X0 Do", kv("XObject", kv("X0", innerForm)), true},
		{"form inheriting the alpha", "/GS0 gs /X0 Do", kv("ExtGState", kv("GS0", halfStroke), "XObject", kv("X0", streamDictWith("0 0 m 1 1 l S", "Subtype", name("Form")))), true},
		{"tiling pattern fill", "/Pattern cs /P0 scn 0 0 1 1 re f", kv("Pattern", kv("P0", pattern)), true},
		{"tiling pattern stroke", "/Pattern CS /P0 SCN 0 0 m 1 1 l S", kv("Pattern", kv("P0", pattern)), true},
		{"tiling pattern replaced", "/Pattern cs /P0 scn 0 g 0 0 1 1 re f", kv("Pattern", kv("P0", pattern)), false},
		{"shading pattern", "/Pattern cs /P0 scn 0 0 1 1 re f", kv("Pattern", kv("P0", shadingPattern)), false},
		{"tiling pattern unused", "/Pattern cs /P0 scn 0 0 m 1 1 l S", kv("Pattern", kv("P0", pattern)), false},
		{"Type 3 transparent glyph", "BT /F1 12 Tf (a) Tj ET", fontRes, true},
		{"Type 3 transparent glyph, hex", "BT /F1 12 Tf <61> Tj ET", fontRes, true},
		{"Type 3 glyph in TJ", "BT /F1 12 Tf [(b) -10 (a)] TJ ET", fontRes, true},
		{"Type 3 opaque glyph twice", "BT /F1 12 Tf (bb) ' ET", fontRes, false},
		{"Type 3 unknown glyph", "BT /F1 12 Tf (c) Tj ET", fontRes, false},
		{"Type 3 invisible text", "BT /F1 12 Tf 3 Tr (a) Tj ET", fontRes, false},
		{"Type 3 clip text", "BT /F1 12 Tf 7 Tr (a) Tj ET", fontRes, false},
		{"text alpha, stroked", "BT /GS0 gs 1 Tr (a) Tj ET", gsRes(halfStroke), true},
		{"text alpha, fill and stroke", "BT /GS0 gs 2 Tr 0 0 (a) \" ET", gsRes(half), true},
		{"text mode not a number", "BT /F2 12 Tf /x Tr (a) Tj ET", fontRes, false},
		{"Tf without operands", "BT Tf (a) Tj ET", fontRes, false},
		{"Tr without operands", "BT Tr (a) Tj ET", fontRes, false},
		{"other font", "BT /F2 12 Tf (a) Tj ET", fontRes, false},
		{"gs without operands", "gs 0 0 1 1 re f", kv(), false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			if got := PageContainsTransparency(transparentPage(tc.content, tc.resources), nil); got != tc.want {
				t.Errorf("PageContainsTransparency = %v, want %v", got, tc.want)
			}
		})
	}
}

// streamDictWith returns a stream dictionary with data raw and one entry.
func streamDictWith(raw, key string, value pdf.PDFValue) pdf.PDFDict {
	d := streamDict(raw)
	d.Entries.Set(key, value)
	return d
}

func TestPageContainsTransparencyContentsAndAnnotations(t *testing.T) {
	gsRes := kv("ExtGState", kv("GS0", kv("ca", pdf.PDFReal(0.5))))
	transparent := streamDict("/GS0 gs 0 0 1 1 re f")
	opaque := streamDict("0 0 1 1 re f")
	appearance := streamDictWith("/GS0 gs 0 0 1 1 re f", "Resources", gsRes)
	for _, tc := range []struct {
		desc string
		page pdf.PDFDict
		want bool
	}{
		{"contents array", kv("Contents", pdf.PDFArray{opaque, pdf.PDFInteger(1), transparent}, "Resources", gsRes), true},
		{"contents not a stream", kv("Contents", kv()), false},
		{"no contents", kv(), false},
		{"normal appearance", kv("Annots", pdf.PDFArray{kv("AP", kv("N", appearance))}), true},
		{"rollover appearance state", kv("Annots", pdf.PDFArray{kv("AP", kv("R", kv("On", appearance, "Off", pdf.PDFInteger(1))))}), true},
		{"appearance not a dictionary", kv("Annots", pdf.PDFArray{kv("AP", kv("D", pdf.PDFInteger(1)))}), false},
		{"opaque appearance", kv("Annots", pdf.PDFArray{kv("AP", kv("N", opaque))}), false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			if got := PageContainsTransparency(tc.page, nil); got != tc.want {
				t.Errorf("PageContainsTransparency = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPageContainsTransparencyStops verifies that the analysis ends on a form
// that draws itself, on nesting too deep and on content that does not decode.
func TestPageContainsTransparencyStops(t *testing.T) {
	self := streamDictWith("/X0 Do", "Subtype", name("Form"))
	self.Entries.Set("Resources", kv("XObject", kv("X0", self)))
	if PageContainsTransparency(transparentPage("/X0 Do", kv("XObject", kv("X0", self))), nil) {
		t.Error("a form drawing itself contains transparency")
	}

	deepest := streamDictWith("/GS0 gs 0 0 1 1 re f", "Subtype", name("Form"))
	deepest.Entries.Set("Resources", kv("ExtGState", kv("GS0", kv("ca", pdf.PDFReal(0.5)))))
	form := deepest
	for range maxTransparencyDepth + 1 {
		form = streamDictWith("/X0 Do", "Subtype", name("Form"))
		form.Entries.Set("Resources", kv("XObject", kv("X0", deepest)))
		deepest = form
	}
	if PageContainsTransparency(transparentPage("/X0 Do", kv("XObject", kv("X0", form))), nil) {
		t.Error("transparency found past the depth limit")
	}

	page := kv("Type", name("Page"), "Contents", brokenStreamDict())
	if PageContainsTransparency(page, nil) {
		t.Error("undecodable content contains transparency")
	}
}

func TestPageContainsTransparencyThroughReader(t *testing.T) {
	path := t.TempDir() + "/t.pdf"
	if err := createValidPDF(path); err != nil {
		t.Fatal(err)
	}
	d, err := pdf.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	gsRes := kv("ExtGState", kv("GS0", kv("ca", pdf.PDFReal(0.5))))
	if !PageContainsTransparency(transparentPage("/GS0 gs 0 0 1 1 re f", gsRes), d) {
		t.Error("transparency not found through the reader")
	}
}

func TestValidatePageTransparency(t *testing.T) {
	gsRes := kv("ExtGState", kv("GS0", kv("ca", pdf.PDFReal(0.5))))
	page := func() pdf.PDFDict { return transparentPage("/GS0 gs 0 0 1 1 re f", gsRes) }
	withCS := page()
	withCS.Entries.Set("Group", kv("S", name("Transparency"), "CS", name("DeviceRGB")))
	withoutCS := page()
	withoutCS.Entries.Set("Group", kv("S", name("Transparency")))
	s := a1b()
	for _, tc := range []struct {
		desc   string
		ctx    *ValidationContext
		dict   pdf.PDFDict
		intent bool
		want   bool
	}{
		{"transparent page", a2bContext(), page(), false, true},
		{"group without CS", a2bContext(), withoutCS, false, true},
		{"group with CS", a2bContext(), withCS, false, false},
		{"PDF/A output intent", a2bContext(), page(), true, false},
		{"opaque page", a2bContext(), transparentPage("0 0 1 1 re f", kv()), false, false},
		{"not a page", a2bContext(), kv("Type", name("Pages")), false, false},
		{"Part 1", &ValidationContext{Spec: &s}, page(), false, false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			tc.ctx.hasOutputIntent = tc.intent
			validatePageTransparency(tc.dict, tc.ctx)
			if got := hasCheck(tc.ctx, pdf.Checks.Transparency.TransparencyWithoutBlendingSpace); got != tc.want {
				t.Errorf("TransparencyWithoutBlendingSpace reported = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidateBlendingColourSpace(t *testing.T) {
	group := func(cs pdf.PDFValue, isolated bool) pdf.PDFDict {
		g := kv("S", name("Transparency"), "I", pdf.PDFBoolean(isolated))
		if cs != nil {
			g.Entries.Set("CS", cs)
		}
		return g
	}
	page := func(cs pdf.PDFValue, resources pdf.PDFDict) pdf.PDFDict {
		return kv("Type", name("Page"), "Group", group(cs, false), "Resources", resources)
	}
	form := func(cs pdf.PDFValue, isolated bool) pdf.PDFDict {
		return kv("Subtype", name("Form"), "Group", group(cs, isolated), "Resources", kv())
	}
	defaultRGB := kv("ColorSpace", kv("DefaultRGB", pdf.PDFArray{name("CalRGB"), kv()}))
	s := a1b()
	for _, tc := range []struct {
		desc string
		ctx  *ValidationContext
		dict pdf.PDFDict
		want bool
	}{
		{"page DeviceRGB", a2bContext(), page(name("DeviceRGB"), kv()), true},
		{"page DeviceRGB under DefaultRGB", a2bContext(), page(name("DeviceRGB"), defaultRGB), false},
		{"page ICCBased", a2bContext(), page(pdf.PDFArray{name("ICCBased"), kv()}, kv()), false},
		{"page without CS", a2bContext(), page(nil, kv()), false},
		{"isolated form DeviceCMYK", a2bContext(), form(name("DeviceCMYK"), true), true},
		{"non-isolated form", a2bContext(), form(name("DeviceCMYK"), false), false},
		{"unreachable form", &ValidationContext{Spec: a2bContext().Spec, ReachableXObjectPtrs: map[uintptr]bool{}}, form(name("DeviceCMYK"), true), false},
		{"knockout group", a2bContext(), kv("Type", name("Page"), "Group", kv("S", name("Knockout"), "CS", name("DeviceRGB"))), false},
		{"group not a dictionary", a2bContext(), kv("Type", name("Page"), "Group", pdf.PDFInteger(1)), false},
		{"other holder", a2bContext(), kv("Type", name("Annot"), "Group", group(name("DeviceRGB"), true)), false},
		{"Part 1", &ValidationContext{Spec: &s}, page(name("DeviceRGB"), kv()), false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			validateBlendingColourSpace(tc.dict, tc.ctx)
			if got := hasCheck(tc.ctx, pdf.Checks.Transparency.DeviceBlendingColourSpace); got != tc.want {
				t.Errorf("DeviceBlendingColourSpace reported = %v, want %v", got, tc.want)
			}
		})
	}
	covered := a2bContext()
	covered.rgbCovered = true
	validateBlendingColourSpace(page(name("DeviceRGB"), kv()), covered)
	if hasCheck(covered, pdf.Checks.Transparency.DeviceBlendingColourSpace) {
		t.Error("DeviceRGB under an RGB output intent reported")
	}
}

func TestImageBitsPerComponentFollowsTheLevel(t *testing.T) {
	image := kv("Subtype", name("Image"), "BitsPerComponent", pdf.PDFInteger(16))
	s := a1b()
	part1 := &ValidationContext{Spec: &s}
	validateXObjectDict(image, part1)
	if !hasCheck(part1, pdf.Checks.Image.ImageBitsPerComponent) {
		t.Error("Part 1: 16 bits per component not reported")
	}
	part2 := a2bContext()
	validateXObjectDict(image, part2)
	if hasCheck(part2, pdf.Checks.Image.ImageBitsPerComponent) {
		t.Error("Part 2: 16 bits per component reported")
	}
	image.Entries.Set("BitsPerComponent", pdf.PDFInteger(12))
	validateXObjectDict(image, part2)
	if !hasCheck(part2, pdf.Checks.Image.ImageBitsPerComponent) {
		t.Error("Part 2: 12 bits per component not reported")
	}
}

func TestNormalBlendModeNull(t *testing.T) {
	if !normalBlendMode(nil) {
		t.Error("a null blend mode does not count as Normal")
	}
}
