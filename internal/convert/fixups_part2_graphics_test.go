package convert

import (
	"errors"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// treePage returns a trailer whose page tree holds one page under a Pages
// node with the given resources, and the page.
func treePage(content string, treeResources pdf.PDFDict) (trailer, page pdf.PDFDict) {
	pages := kv("Type", nm("Pages"), "Resources", treeResources)
	page = kv("Type", nm("Page"), "Parent", pages, "Contents", pdf.NewStreamDict(nil, []byte(content)))
	pages.Entries.Set("Kids", pdf.PDFArray{page})
	return kv("Root", kv("Type", nm("Catalog"), "Pages", pages)), page
}

// streamWith returns a stream with the entries of d and the given data.
func streamWith(d pdf.PDFDict, data []byte) pdf.PDFDict { return pdf.NewStreamDict(d.Entries, data) }

func form(content string, resources pdf.PDFValue) pdf.PDFDict {
	f := streamWith(kv("Subtype", nm("Form")), []byte(content))
	if resources != nil {
		f.Entries.Set("Resources", resources)
	}
	return f
}

func TestInheritedResourcesFixerSetsPageResources(t *testing.T) {
	tree := kv("Font", kv("F1", kv()))
	trailer, page := treePage("BT /F1 12 Tf ET", tree)
	changed, err := inheritedResourcesFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("Fix = %v, %v, want a change", changed, err)
	}
	if res, _ := page.Entries.Get("Resources").(pdf.PDFDict); res.Entries != tree.Entries {
		t.Errorf("page Resources = %v, want the page tree's", res)
	}
}

func TestInheritedResourcesFixerGivesAPageWithoutAnyResourcesSome(t *testing.T) {
	page := kv("Type", nm("Page"), "Contents", pdf.NewStreamDict(nil, []byte("0 0 m")))
	trailer := kv("Root", kv("Type", nm("Catalog"), "Pages", kv("Type", nm("Pages"), "Kids", pdf.PDFArray{page})))
	if changed, _ := (inheritedResourcesFixer{}).Fix(&trailer, nil); !changed {
		t.Fatal("Fix made no change")
	}
	if _, ok := page.Entries.Get("Resources").(pdf.PDFDict); !ok {
		t.Error("page has no Resources dictionary")
	}
}

// TestInheritedResourcesFixerCopiesIntoChildren verifies that a form, a
// tiling pattern and a Type 3 font each get the resources they use from the
// scope they are drawn in, and that the copy follows nested forms.
func TestInheritedResourcesFixerCopiesIntoChildren(t *testing.T) {
	gs := kv()
	inner := form("/GS0 gs", nil)
	outer := form("/X1 Do /GS0 gs /Missing gs", kv())
	pattern := streamWith(kv("PatternType", pdf.PDFInteger(1)), []byte("/GS0 gs"))
	glyph := pdf.NewStreamDict(nil, []byte("/GS0 gs"))
	type3 := kv("Subtype", nm("Type3"), "Resources", kv(), "CharProcs", kv("a", glyph, "b", pdf.PDFInteger(1)))
	resources := kv(
		"ExtGState", kv("GS0", gs),
		"XObject", kv("X0", outer, "X1", inner, "Img", streamWith(kv("Subtype", nm("Image")), nil), "Bad", kv()),
		"Pattern", kv("P0", pattern, "P2", streamWith(kv("PatternType", pdf.PDFInteger(2)), nil)),
		"Font", kv("T3", type3, "F1", kv("Subtype", nm("Type1"))),
	)
	trailer, page := hoistPage("/X0 Do /X0 Do /Img Do /Bad Do /P0 scn /P2 scn /T3 1 Tf /F1 1 Tf", resources)
	page.Entries.Set("Annots", pdf.PDFArray{
		kv("AP", kv("N", form("/X0 Do", resources))),
		kv("AP", kv("N", kv("On", form("", nil), "Off", pdf.PDFInteger(1)))),
		kv("AP", kv("N", pdf.PDFInteger(1))),
		pdf.PDFInteger(1),
	})
	changed, err := inheritedResourcesFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("Fix = %v, %v, want a change", changed, err)
	}
	for _, tc := range []struct {
		desc   string
		holder pdf.PDFDict
	}{
		{"outer form", outer}, {"nested form", inner}, {"pattern", pattern}, {"Type 3 font", type3},
	} {
		if got := pdf.LookupResource(resourcesOf(tc.holder, pdf.PDFDict{}), "ExtGState", "GS0"); got == nil {
			t.Errorf("%s did not get /GS0", tc.desc)
		}
	}
	if pdf.LookupResource(resourcesOf(outer, pdf.PDFDict{}), "ExtGState", "Missing") != nil {
		t.Error("a name no scope has was added")
	}
	if changed, _ := (inheritedResourcesFixer{}).Fix(&trailer, nil); changed {
		t.Error("a second Fix changed the document again")
	}
}

func TestInheritedResourcesFixerSkipsUndecodableStreams(t *testing.T) {
	broken := streamWith(kv("Filter", nm("FlateDecode")), []byte("not zlib"))
	trailer, _ := hoistPage("", kv())
	pages, _ := trailer.Entries.Get("Root").(pdf.PDFDict).Entries.Get("Pages").(pdf.PDFDict)
	page := pages.Entries.Get("Kids").(pdf.PDFArray)[0].(pdf.PDFDict)
	page.Entries.Set("Contents", broken)
	fail := func(pdf.PDFDict) ([]byte, error) { return nil, errors.New("no") }
	if changed, _ := (inheritedResourcesFixer{decode: fail}).Fix(&trailer, nil); changed {
		t.Error("Fix changed a page whose content does not decode")
	}
}

func TestOverprintModeFixer(t *testing.T) {
	gs1, gs0 := kv("OPM", pdf.PDFInteger(1)), kv("OPM", pdf.PDFInteger(0))
	trailer := kv("Root", kv("Things", pdf.PDFArray{gs1, gs0}))
	if !(overprintModeFixer{}).Applies(pdf.Checks.Colour.ICCBasedCMYKOverprint) {
		t.Error("does not apply to ICCBasedCMYKOverprint")
	}
	changed, err := overprintModeFixer{}.Fix(&trailer, nil)
	if err != nil || !changed || gs1.Entries.Get("OPM") != pdf.PDFInteger(0) {
		t.Errorf("Fix = %v, %v; OPM = %v", changed, err, gs1.Entries.Get("OPM"))
	}
}

func TestDeviceNColorantEntriesFixer(t *testing.T) {
	// Two inputs to three outputs: the first input drives red, the second
	// green.
	tint := streamWith(kv(
		"FunctionType", pdf.PDFInteger(4),
		"Domain", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1), pdf.PDFInteger(0), pdf.PDFInteger(1)},
		"Range", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1), pdf.PDFInteger(0), pdf.PDFInteger(1), pdf.PDFInteger(0), pdf.PDFInteger(1)},
	), []byte("{0}"))
	existing := pdf.PDFArray{nm("Separation"), nm("Green"), nm("DeviceRGB"), kv()}
	bare := pdf.PDFArray{nm("DeviceN"), pdf.PDFArray{nm("Red"), nm("Green")}, nm("DeviceRGB"), tint}
	partial := pdf.PDFArray{nm("DeviceN"), pdf.PDFArray{nm("Red"), nm("Green")}, nm("DeviceRGB"), tint, pdf.PDFInteger(1)}
	broken := pdf.PDFArray{nm("DeviceN"), pdf.PDFArray{nm("Blue")}, nm("DeviceRGB"), kv()}
	attrs := kv()
	withAttrs := pdf.PDFArray{nm("DeviceN"), pdf.PDFArray{nm("Red")}, nm("DeviceRGB"), tint, attrs}
	short := pdf.PDFArray{nm("DeviceN"), pdf.PDFArray{nm("Red")}}
	holder := kv("A", bare, "B", partial, "C", broken, "D", withAttrs, "E", short, "S", existing,
		"T", pdf.PDFArray{nm("Separation"), pdf.PDFInteger(1), nm("DeviceRGB"), kv()}, "U", pdf.PDFArray{nm("Separation")})
	trailer := kv("Root", holder)
	if !(deviceNColorantEntriesFixer{}).Applies(pdf.Checks.Colour.DeviceNColorantsMissing) {
		t.Error("does not apply to DeviceNColorantsMissing")
	}
	changed, err := deviceNColorantEntriesFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("Fix = %v, %v, want a change", changed, err)
	}
	fixed := holder.Entries.Get("A").(pdf.PDFArray)
	colorants := fixed[4].(pdf.PDFDict).Entries.Get("Colorants").(pdf.PDFDict)
	red, _ := colorants.Entries.Get("Red").(pdf.PDFArray)
	if len(red) != 4 || red[1] != nm("Red") {
		t.Fatalf("Red colorant = %v", red)
	}
	if fn, err := pdf.ParseFunction(red[3]); err != nil || len(fn.Eval([]float64{1})) != 3 {
		t.Errorf("Red tint transform does not evaluate: %v", err)
	}
	if green := colorants.Entries.Get("Green").(pdf.PDFArray); green[2] != existing[2] {
		t.Errorf("Green colorant = %v, want the document's Separation", green)
	}
	if partial := holder.Entries.Get("B").(pdf.PDFArray); partial[4].(pdf.PDFDict).Entries.Get("Colorants") == nil {
		t.Error("an attributes entry that is not a dictionary was not replaced")
	}
	if len(holder.Entries.Get("C").(pdf.PDFArray)) != 4 {
		t.Error("a DeviceN whose tint transform does not parse was given attributes")
	}
	if attrs.Entries.Get("Colorants") == nil {
		t.Error("the existing attributes dictionary got no Colorants")
	}
}

func TestColorantTint(t *testing.T) {
	identity := streamWith(kv(
		"FunctionType", pdf.PDFInteger(4),
		"Domain", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1)},
		"Range", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1)},
	), []byte("{}"))
	names := pdf.PDFArray{nm("Spot")}
	if _, ok := colorantTint(identity, names, "Other"); ok {
		t.Error("a colorant the DeviceN does not name got a tint transform")
	}
	if _, ok := colorantTint(kv(), names, "Spot"); ok {
		t.Error("a tint transform that does not parse was sampled")
	}
	constant := streamWith(kv(
		"FunctionType", pdf.PDFInteger(4),
		"Domain", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1)},
		"Range", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1)},
	), []byte("{pop 0.5}"))
	fn, ok := colorantTint(constant, names, "Spot")
	if !ok {
		t.Fatal("constant tint transform not sampled")
	}
	got, err := pdf.ParseFunction(fn)
	if err != nil {
		t.Fatal(err)
	}
	if v := got.Eval([]float64{1})[0]; v != 0.5 {
		t.Errorf("sampled constant = %v, want 0.5", v)
	}
	empty := streamWith(kv(
		"FunctionType", pdf.PDFInteger(4),
		"Domain", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1)},
		"Range", pdf.PDFArray{},
	), []byte("{pop}"))
	if _, ok := colorantTint(empty, names, "Spot"); ok {
		t.Error("a tint transform with no outputs was sampled")
	}
}

func TestSeparationConsistencyFixer(t *testing.T) {
	fn1, fn2 := kv(), kv()
	first := pdf.PDFArray{nm("Separation"), nm("Red"), nm("DeviceRGB"), fn1}
	second := pdf.PDFArray{nm("Separation"), nm("Red"), nm("DeviceGray"), fn2}
	same := pdf.PDFArray{nm("Separation"), nm("Red"), nm("DeviceRGB"), fn1}
	odd := pdf.PDFArray{nm("Separation"), pdf.PDFInteger(1), nm("DeviceGray"), fn2}
	trailer := kv("Root", kv("A", first, "B", second, "C", same, "D", odd, "E", pdf.PDFArray{nm("Separation")}))
	if !(separationConsistencyFixer{}).Applies(pdf.Checks.Colour.SeparationInconsistent) {
		t.Error("does not apply to SeparationInconsistent")
	}
	changed, err := separationConsistencyFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("Fix = %v, %v, want a change", changed, err)
	}
	if second[2] != nm("DeviceRGB") || second[3].(pdf.PDFDict).Entries != fn1.Entries {
		t.Errorf("second Separation = %v, want the first's alternate and tint", second)
	}
	if changed, _ := (separationConsistencyFixer{}).Fix(&trailer, nil); changed {
		t.Error("a second Fix changed the document again")
	}
}

func TestHalftoneFixer(t *testing.T) {
	tr := pdf.Checks.Transparency
	all := []pdf.PDFError{
		pdf.NewError(tr.HalftoneExtGState, nil, 0, nil), pdf.NewError(tr.HalftoneType, nil, 0, nil),
		pdf.NewError(tr.HalftoneName, nil, 0, nil), pdf.NewError(tr.HalftoneTransferFunction, nil, 0, nil),
	}
	type1 := func(pairs ...any) pdf.PDFDict {
		return kv(append([]any{"HalftoneType", pdf.PDFInteger(1)}, pairs...)...)
	}
	type5 := func(pairs ...any) pdf.PDFDict {
		return kv(append([]any{"HalftoneType", pdf.PDFInteger(5)}, pairs...)...)
	}
	identity := nm("Identity")

	htp := kv("HTP", pdf.PDFArray{})
	type6 := kv("Type", nm("ExtGState"), "HT", kv("HalftoneType", pdf.PDFInteger(6)))
	badDefault := kv("HT", type5("Default", kv("HalftoneType", pdf.PDFInteger(6))))
	named := type1("HalftoneName", pdf.PDFString{Value: "x"}, "TransferFunction", identity)
	spot, cyan, bad := type1(), type1("TransferFunction", identity), kv("HalftoneType", pdf.PDFInteger(6))
	dflt := type1("TransferFunction", identity)
	composite := type5("Type", nm("Halftone"), "Default", dflt, "Spot", spot, "Cyan", cyan, "Magenta", bad, "Note", pdf.PDFInteger(1))
	annot := kv("Type", nm("Annot"), "HTP", pdf.PDFArray{})
	trailer := kv("Root", kv("Things", pdf.PDFArray{
		htp, type6, badDefault, kv("HT", named), kv("HT", composite), kv("HT", nm("Default")), annot,
	}))
	for _, c := range []pdf.Check{tr.HalftoneExtGState, tr.HalftoneType, tr.HalftoneName, tr.HalftoneTransferFunction} {
		if !(halftoneFixer{}).Applies(c) {
			t.Errorf("does not apply to %s", c.Name())
		}
	}
	if (halftoneFixer{}).Applies(tr.TransferFunction) {
		t.Error("applies to TransferFunction")
	}
	changed, err := halftoneFixer{}.Fix(&trailer, all)
	if err != nil || !changed {
		t.Fatalf("Fix = %v, %v, want a change", changed, err)
	}
	for _, tc := range []struct {
		desc string
		ok   bool
	}{
		{"HTP removed", htp.Entries.Get("HTP") == nil},
		{"type 6 replaced by Default", type6.Entries.Get("HT") == nm("Default")},
		{"type 5 with a bad Default replaced", badDefault.Entries.Get("HT") == nm("Default")},
		{"HalftoneName removed", named.Entries.Get("HalftoneName") == nil},
		{"primary transfer function removed", named.Entries.Get("TransferFunction") == nil && cyan.Entries.Get("TransferFunction") == nil},
		{"spot transfer function added", spot.Entries.Get("TransferFunction") == identity},
		{"Default transfer function kept", dflt.Entries.Get("TransferFunction") == identity},
		{"bad component dropped", composite.Entries.Get("Magenta") == nil},
		{"other Type left alone", annot.Entries.Get("HTP") != nil},
	} {
		if !tc.ok {
			t.Error(tc.desc)
		}
	}
	if changed, _ := (halftoneFixer{}).Fix(&trailer, all); changed {
		t.Error("a second Fix changed the document again")
	}
}

// TestHalftoneFixerRepairsOnlyReportedChecks verifies that nothing changes
// when none of the fixer's checks is reported.
func TestHalftoneFixerRepairsOnlyReportedChecks(t *testing.T) {
	gs := kv("HTP", pdf.PDFArray{}, "HT", kv("HalftoneType", pdf.PDFInteger(5), "Default",
		kv("HalftoneType", pdf.PDFInteger(6)), "Spot", kv("HalftoneType", pdf.PDFInteger(6))))
	trailer := kv("Root", gs)
	if changed, _ := (halftoneFixer{}).Fix(&trailer, nil); changed {
		t.Error("Fix changed the document with no check reported")
	}
}

func TestDefaultHalftone(t *testing.T) {
	type5 := kv("HalftoneType", pdf.PDFInteger(5))
	if got := defaultHalftone(type5); got.Entries != type5.Entries {
		t.Error("type 5 without a Default dictionary did not return itself")
	}
}

// TestInheritedResourcesFixerFollowsAppearances verifies that a form an
// appearance stream draws gets the resources it inherits from the stream.
func TestInheritedResourcesFixerFollowsAppearances(t *testing.T) {
	x := form("/GS0 gs", nil)
	ap := form("/X0 Do", kv("XObject", kv("X0", x), "ExtGState", kv("GS0", kv())))
	trailer, page := hoistPage("", kv())
	page.Entries.Set("Annots", pdf.PDFArray{kv("AP", kv("N", ap))})
	if changed, _ := (inheritedResourcesFixer{}).Fix(&trailer, nil); !changed {
		t.Fatal("Fix made no change")
	}
	if pdf.LookupResource(resourcesOf(x, pdf.PDFDict{}), "ExtGState", "GS0") == nil {
		t.Error("the form did not get /GS0")
	}
}

// TestDeviceColourFixerCoversPatternBase verifies that the device base of an
// uncoloured Pattern space a page selects gets a Default colour space.
func TestDeviceColourFixerCoversPatternBase(t *testing.T) {
	resources := kv("ColorSpace", kv(
		"P0", pdf.PDFArray{nm("Pattern"), nm("DeviceCMYK")},
		"P1", pdf.PDFArray{nm("Pattern")},
		"I0", pdf.PDFArray{nm("Indexed"), nm("DeviceGray")},
	))
	trailer, _ := hoistPage("/P0 cs /P1 cs /I0 cs /None cs", resources)
	if !(deviceColourFixer{}).Applies(pdf.Checks.Colour.DeviceColourBase) {
		t.Error("does not apply to DeviceColourBase")
	}
	if changed, err := (deviceColourFixer{spec: a2bSpec}).Fix(&trailer, nil); err != nil || !changed {
		t.Fatalf("Fix = %v, %v, want a change", changed, err)
	}
	if !verify.DefaultColorSpaceDefined("cmyk", resources) {
		t.Error("no DefaultCMYK for the Pattern space's DeviceCMYK base")
	}
}

// TestInheritedResourcesFixerAdoptsIntoAppearances verifies that an appearance
// stream without resources of its own gets the page resources it uses.
func TestInheritedResourcesFixerAdoptsIntoAppearances(t *testing.T) {
	ap := form("/Helv 9 Tf", nil)
	trailer, page := hoistPage("", kv("Font", kv("Helv", kv())))
	page.Entries.Set("Annots", pdf.PDFArray{kv("AP", kv("N", ap))})
	if changed, _ := (inheritedResourcesFixer{}).Fix(&trailer, nil); !changed {
		t.Fatal("Fix made no change")
	}
	if pdf.LookupResource(resourcesOf(ap, pdf.PDFDict{}), "Font", "Helv") == nil {
		t.Error("the appearance stream did not get /Helv")
	}
}

// TestInheritedResourcesFixerAdoptsIntoUntypedAppearances verifies that an
// appearance stream that does not declare itself a form is still given the
// page resources it uses.
func TestInheritedResourcesFixerAdoptsIntoUntypedAppearances(t *testing.T) {
	ap := pdf.NewStreamDict(nil, []byte("/Helv 9 Tf"))
	trailer, page := hoistPage("", kv("Font", kv("Helv", kv())))
	page.Entries.Set("Annots", pdf.PDFArray{kv("AP", kv("N", ap))})
	if changed, _ := (inheritedResourcesFixer{}).Fix(&trailer, nil); !changed {
		t.Fatal("Fix made no change")
	}
	if pdf.LookupResource(resourcesOf(ap, pdf.PDFDict{}), "Font", "Helv") == nil {
		t.Error("the appearance stream did not get /Helv")
	}
}
