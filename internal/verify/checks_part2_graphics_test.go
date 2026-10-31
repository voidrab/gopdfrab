package verify

import (
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func TestInheritanceOf(t *testing.T) {
	own, page, parent, scope := kv("Font", kv()), kv("Font", kv()), kv("XObject", kv()), kv("Shading", kv())
	ctx := a2bContext()
	ctx.parentResourceScope, ctx.resourceScope = parent, scope
	for _, tc := range []struct {
		desc      string
		kind      pdf.ContentKind
		holder    pdf.PDFDict
		own, from pdf.PDFDict
	}{
		{"page with its own resources", pdf.ContentPage, kv("Resources", own), own, pdf.PDFDict{}},
		{"page inheriting from the page tree", pdf.ContentPage, kv(), pdf.PDFDict{}, page},
		{"form with its own resources", pdf.ContentForm, kv("Resources", own), own, parent},
		{"form without resources", pdf.ContentForm, kv(), pdf.PDFDict{}, scope},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			got := inheritanceOf(pdf.ContentRoot{Kind: tc.kind, Resources: page}, tc.holder, ctx)
			if got.own.Entries != tc.own.Entries || got.from.Entries != tc.from.Entries {
				t.Errorf("inheritanceOf = %+v, want own %v from %v", got, tc.own, tc.from)
			}
		})
	}
}

func TestInheritedResources(t *testing.T) {
	from := kv(
		"Font", kv("F1", kv()), "ColorSpace", kv("CS0", kv()), "Pattern", kv("P0", kv()),
		"Shading", kv("Sh0", kv()), "XObject", kv("X0", kv()), "ExtGState", kv("GS0", kv()),
		"Properties", kv("MC0", kv()),
	)
	own := kv("Font", kv("F2", kv()))
	in := inheritance{own: own, from: from}
	for _, tc := range []struct {
		desc     string
		in       inheritance
		op       string
		operands []pdf.PDFValue
		want     bool
	}{
		{"inherited font", in, "Tf", []pdf.PDFValue{name("F1"), pdf.PDFInteger(12)}, true},
		{"own font", in, "Tf", []pdf.PDFValue{name("F2"), pdf.PDFInteger(12)}, false},
		{"font named nowhere", in, "Tf", []pdf.PDFValue{name("F9"), pdf.PDFInteger(12)}, false},
		{"font operand not a name", in, "Tf", []pdf.PDFValue{pdf.PDFInteger(1), pdf.PDFInteger(12)}, false},
		{"inherited colour space", in, "cs", []pdf.PDFValue{name("CS0")}, true},
		{"device colour space", in, "CS", []pdf.PDFValue{name("DeviceRGB")}, false},
		{"colour space operand not a name", in, "cs", []pdf.PDFValue{pdf.PDFInteger(1)}, false},
		{"inherited pattern", in, "scn", []pdf.PDFValue{name("P0")}, true},
		{"inherited shading", in, "sh", []pdf.PDFValue{name("Sh0")}, true},
		{"inherited XObject", in, "Do", []pdf.PDFValue{name("X0")}, true},
		{"inherited graphics state", in, "gs", []pdf.PDFValue{name("GS0")}, true},
		{"inherited properties", in, "BDC", []pdf.PDFValue{name("OC"), name("MC0")}, true},
		{"inline properties", in, "DP", []pdf.PDFValue{name("OC")}, false},
		{"inline image colour space", in, "INLINEIMAGE", []pdf.PDFValue{name("CS"), name("CS0")}, true},
		{"inline image device colour space", in, "INLINEIMAGE", []pdf.PDFValue{name("ColorSpace"), name("RGB")}, false},
		{"inline image colour space array", in, "INLINEIMAGE", []pdf.PDFValue{name("CS"), pdf.PDFArray{}}, false},
		{"inline image other key", in, "INLINEIMAGE", []pdf.PDFValue{name("W"), name("CS0")}, false},
		{"operator naming no resource", in, "m", []pdf.PDFValue{pdf.PDFInteger(0), pdf.PDFInteger(0)}, false},
		{"no operands", in, "Do", nil, false},
		{"nothing to inherit", inheritance{own: own}, "Do", []pdf.PDFValue{name("X0")}, false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := a2bContext()
			tc.in.checkOperands(tc.op, tc.operands, nil, ctx)
			if got := hasCheck(ctx, pdf.Checks.Colour.InheritedResources); got != tc.want {
				t.Errorf("InheritedResources reported = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestContentCheckerInheritsOnlyUnderPart2 verifies that the content checker
// runs the Part 2 graphics checks only on a Part 2 level.
func TestContentCheckerInheritsOnlyUnderPart2(t *testing.T) {
	for _, tc := range []struct {
		desc string
		ctx  *ValidationContext
		want bool
	}{
		{"PDF/A-2b", a2bContext(), true},
		{"PDF/A-1b", func() *ValidationContext { s := a1b(); return &ValidationContext{Spec: &s} }(), false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			tc.ctx.inherit = inheritance{from: kv("XObject", kv("X0", kv()))}
			contentChecker(nil, pdf.PDFDict{}, tc.ctx)("Do", []pdf.PDFValue{name("X0")})
			if got := hasCheck(tc.ctx, pdf.Checks.Colour.InheritedResources); got != tc.want {
				t.Errorf("InheritedResources reported = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestValidateContentStreamsSetsInheritance verifies that a page inheriting
// its resources from the page tree is reported for the names it uses, and
// that the inheritance is restored after the scan.
func TestValidateContentStreamsSetsInheritance(t *testing.T) {
	pages := kv("Type", name("Pages"), "Resources", kv("XObject", kv("X0", kv())))
	page := kv("Type", name("Page"), "Parent", pages, "Contents", streamDict("/X0 Do"))
	ctx := a2bContext()
	saved := inheritance{own: kv()}
	ctx.inherit = saved
	validateContentStreams(page, ctx)
	if !hasCheck(ctx, pdf.Checks.Colour.InheritedResources) {
		t.Error("InheritedResources not reported for a page using the page tree's resources")
	}
	if ctx.inherit.own.Entries != saved.own.Entries {
		t.Error("the inheritance was not restored after the scan")
	}
}

func TestDeviceColourWithoutPDFAIntentRule(t *testing.T) {
	reader := func(intents pdf.PDFValue) *pdf.Reader {
		root := kv("Type", name("Catalog"))
		if intents != nil {
			root.Entries.Set("OutputIntents", intents)
		}
		return rawReader("", kv("Root", root))
	}
	pdfx := pdf.PDFArray{kv("S", name("GTS_PDFX"))}
	for _, tc := range []struct {
		desc      string
		intents   pdf.PDFValue
		hasIntent bool
		uncovered bool
		want      bool
	}{
		{"device colour with only a PDF/X intent", pdfx, false, true, true},
		{"a PDF/A intent", pdfx, true, true, false},
		{"no device colour reported", pdfx, false, false, false},
		{"no output intents", nil, false, true, false},
		{"empty output intents", pdf.PDFArray{}, false, true, false},
		{"output intents not an array", kv(), false, true, false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := a2bContext()
			ctx.hasOutputIntent, ctx.uncoveredDeviceColour = tc.hasIntent, tc.uncovered
			got := deviceColourWithoutPDFAIntentRule(reader(tc.intents), nil, ctx)
			if (len(got) == 1 && got[0].Check() == pdf.Checks.Colour.DeviceColourWithoutPDFAIntent) != tc.want {
				t.Errorf("rule returned %v, want reported = %v", got, tc.want)
			}
		})
	}
}

func TestOverprintTracker(t *testing.T) {
	icc := func(n int) pdf.PDFArray {
		s := streamDict("")
		s.Entries.Set("N", pdf.PDFInteger(n))
		return pdf.PDFArray{name("ICCBased"), s}
	}
	resources := kv(
		"ColorSpace", kv("CMYK", icc(4), "RGB", icc(3), "Cal", pdf.PDFArray{name("CalRGB"), kv()}, "Bad", pdf.PDFArray{name("ICCBased")}),
		"ExtGState", kv(
			"OP", kv("OP", pdf.PDFBoolean(true), "OPM", pdf.PDFInteger(1)),
			"OPop", kv("OP", pdf.PDFBoolean(true), "op", pdf.PDFBoolean(false), "OPM", pdf.PDFInteger(1)),
			"op", kv("op", pdf.PDFBoolean(true), "OPM", pdf.PDFInteger(1)),
			"Off", kv("OPM", pdf.PDFInteger(0)),
		),
	)
	type op struct {
		op       string
		operands []pdf.PDFValue
	}
	gs := func(n string) op { return op{"gs", []pdf.PDFValue{name(n)}} }
	cs := func(o, n string) op { return op{o, []pdf.PDFValue{name(n)}} }
	for _, tc := range []struct {
		desc string
		ops  []op
		want bool
	}{
		{"fill overprinted with OPM 1", []op{gs("OP"), cs("cs", "CMYK"), {"f", nil}}, true},
		{"stroke overprinted with OPM 1", []op{gs("OPop"), cs("CS", "CMYK"), {"S", nil}}, true},
		{"fill not overprinted", []op{gs("OPop"), cs("cs", "CMYK"), {"f", nil}}, false},
		{"op alone", []op{gs("op"), cs("cs", "CMYK"), {"Tj", nil}}, true},
		{"overprint mode 0", []op{gs("OP"), gs("Off"), cs("cs", "CMYK"), {"f", nil}}, false},
		{"RGB ICCBased space", []op{gs("OP"), cs("cs", "RGB"), {"f", nil}}, false},
		{"not ICCBased", []op{gs("OP"), cs("cs", "Cal"), {"f", nil}}, false},
		{"short ICCBased array", []op{gs("OP"), cs("cs", "Bad"), {"f", nil}}, false},
		{"unknown colour space", []op{gs("OP"), cs("cs", "None"), {"f", nil}}, false},
		{"colour space operand not a name", []op{gs("OP"), {"cs", []pdf.PDFValue{pdf.PDFInteger(1)}}, {"f", nil}}, false},
		{"state restored by Q", []op{{"q", nil}, gs("OP"), cs("cs", "CMYK"), {"Q", nil}, {"f", nil}}, false},
		{"unbalanced Q", []op{{"Q", nil}, gs("OP"), cs("cs", "CMYK"), {"f", nil}}, true},
		{"gs without operands", []op{{"gs", nil}, cs("cs", "CMYK"), {"f", nil}}, false},
		{"stroke colour not ICCBased CMYK", []op{gs("OP"), cs("cs", "CMYK"), {"S", nil}}, false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := a2bContext()
			var tr overprintTracker
			for _, o := range tc.ops {
				tr.step(o.op, o.operands, resources, nil, ctx)
			}
			if got := hasCheck(ctx, pdf.Checks.Colour.ICCBasedCMYKOverprint); got != tc.want {
				t.Errorf("ICCBasedCMYKOverprint reported = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDeviceNColorants(t *testing.T) {
	alt, fn := name("DeviceRGB"), kv()
	deviceN := func(names pdf.PDFArray, attrs ...pdf.PDFValue) pdf.PDFArray {
		return append(pdf.PDFArray{name("DeviceN"), names, alt, fn}, attrs...)
	}
	spots := pdf.PDFArray{name("Spot1"), name("Spot2")}
	for _, tc := range []struct {
		desc string
		cs   pdf.PDFArray
		want []string
	}{
		{"no attributes", deviceN(spots), []string{"Spot1", "Spot2"}},
		{"process colorants and None", deviceN(pdf.PDFArray{name("Cyan"), name("Black"), name("None"), pdf.PDFInteger(1)}), nil},
		{"colorants listed", deviceN(spots, kv("Colorants", kv("Spot1", kv(), "Spot2", kv()))), nil},
		{"one listed", deviceN(spots, kv("Colorants", kv("Spot2", kv()))), []string{"Spot1"}},
		{"process components", deviceN(spots, kv("Process", kv("Components", pdf.PDFArray{name("Spot1")}))), []string{"Spot2"}},
		{"attributes not a dictionary", deviceN(spots, pdf.PDFInteger(1)), []string{"Spot1", "Spot2"}},
		{"names not an array", pdf.PDFArray{name("DeviceN"), pdf.PDFInteger(1), alt, fn}, nil},
		{"short array", pdf.PDFArray{name("DeviceN")}, nil},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			if got := MissingColorants(tc.cs); strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("MissingColorants = %v, want %v", got, tc.want)
			}
			if len(tc.cs) < 4 {
				return
			}
			ctx := a2bContext()
			validateColourSpaceArray(tc.cs, ctx)
			if got := hasCheck(ctx, pdf.Checks.Colour.DeviceNColorantsMissing); got != (len(tc.want) > 0) {
				t.Errorf("DeviceNColorantsMissing reported = %v, want %v", got, len(tc.want) > 0)
			}
		})
	}
}

// TestColourSpaceArrayPart2ChecksOnlyUnderPart2 verifies that a Part 1 level
// does not run the Part 2 colorant and Separation checks.
func TestColourSpaceArrayPart2ChecksOnlyUnderPart2(t *testing.T) {
	s := a1b()
	ctx := &ValidationContext{Spec: &s, hasOutputIntent: true}
	validateColourSpaceArray(pdf.PDFArray{name("DeviceN"), pdf.PDFArray{name("Spot")}, name("DeviceGray"), kv()}, ctx)
	validateColourSpaceArray(pdf.PDFArray{name("Separation"), name("Spot"), name("DeviceGray"), kv()}, ctx)
	if ctx.separations != nil || len(ctx.errs) != 0 {
		t.Errorf("Part 1 context ran the Part 2 checks: %v", ctx.errs)
	}
}

func TestSeparationConsistency(t *testing.T) {
	tint := func(code string) pdf.PDFDict {
		s := streamDict(code)
		s.Entries.Set("FunctionType", pdf.PDFInteger(4))
		s.Entries.Set("Length", pdf.PDFInteger(len(code)))
		return s
	}
	shared := tint("{}")
	sep := func(n pdf.PDFValue, alt pdf.PDFValue, fn pdf.PDFValue) pdf.PDFArray {
		return pdf.PDFArray{name("Separation"), n, alt, fn}
	}
	red := name("Red")
	for _, tc := range []struct {
		desc string
		cs   []pdf.PDFArray
		want int
	}{
		{"same objects", []pdf.PDFArray{sep(red, name("DeviceRGB"), shared), sep(red, name("DeviceRGB"), shared)}, 0},
		{"equivalent objects", []pdf.PDFArray{sep(red, name("DeviceRGB"), tint("{}")), sep(red, name("DeviceRGB"), tint("{}"))}, 0},
		{"different tint transforms", []pdf.PDFArray{sep(red, name("DeviceRGB"), tint("{}")), sep(red, name("DeviceRGB"), tint("{pop}"))}, 1},
		{"different alternate spaces", []pdf.PDFArray{sep(red, name("DeviceRGB"), shared), sep(red, name("DeviceGray"), shared)}, 1},
		{"reported once per name", []pdf.PDFArray{sep(red, name("DeviceRGB"), shared), sep(red, name("DeviceGray"), shared), sep(red, name("DeviceCMYK"), shared)}, 1},
		{"different names", []pdf.PDFArray{sep(red, name("DeviceRGB"), shared), sep(name("Blue"), name("DeviceGray"), shared)}, 0},
		{"name not a name", []pdf.PDFArray{sep(pdf.PDFInteger(1), name("DeviceRGB"), shared), sep(pdf.PDFInteger(1), name("DeviceGray"), shared)}, 0},
		{"short array", []pdf.PDFArray{{name("Separation"), red, name("DeviceRGB")}, sep(red, name("DeviceGray"), shared)}, 0},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := a2bContext()
			for _, cs := range tc.cs {
				checkSeparationConsistency(cs, ctx)
			}
			got := 0
			for _, e := range ctx.errs {
				if e.Check() == pdf.Checks.Colour.SeparationInconsistent {
					got++
				}
			}
			if got != tc.want {
				t.Errorf("SeparationInconsistent reported %d times, want %d", got, tc.want)
			}
		})
	}
}

func TestEquivalent(t *testing.T) {
	withRef := func(d pdf.PDFDict, n int) pdf.PDFDict {
		d.SetRef(pdf.PDFRef{ObjNum: n})
		return d
	}
	stream := func(data string, length int) pdf.PDFDict {
		s := streamDict(data)
		s.Entries.Set("Length", pdf.PDFInteger(length))
		return s
	}
	deep := pdf.PDFValue(pdf.PDFInteger(1))
	other := pdf.PDFValue(pdf.PDFInteger(2))
	for range maxEquivalenceDepth + 2 {
		deep, other = pdf.PDFArray{deep}, pdf.PDFArray{other}
	}
	for _, tc := range []struct {
		desc string
		a, b pdf.PDFValue
		want bool
	}{
		{"equal names", name("A"), name("A"), true},
		{"different names", name("A"), name("B"), false},
		{"equal arrays", pdf.PDFArray{name("A")}, pdf.PDFArray{name("A")}, true},
		{"arrays of different lengths", pdf.PDFArray{name("A")}, pdf.PDFArray{}, false},
		{"arrays with different elements", pdf.PDFArray{name("A")}, pdf.PDFArray{name("B")}, false},
		{"array and name", pdf.PDFArray{}, name("A"), false},
		{"dict and name", kv(), name("A"), false},
		{"same reference", withRef(kv("A", name("A")), 5), withRef(kv("A", name("B")), 5), true},
		{"different references, equal entries", withRef(kv("A", name("A")), 5), withRef(kv("A", name("A")), 6), true},
		{"one referenced", withRef(kv("A", name("A")), 5), kv("A", name("A")), true},
		{"different entry counts", kv("A", name("A")), kv(), false},
		{"different keys", kv("A", name("A")), kv("B", name("A")), false},
		{"different values", kv("A", name("A")), kv("A", name("B")), false},
		{"stream and dict", streamDict(""), kv(), false},
		{"equal streams, different lengths", stream("abc", 3), stream("abc", 9), true},
		{"streams with different data", stream("abc", 3), stream("abd", 3), false},
		{"undecodable stream", brokenStreamDict(), brokenStreamDict(), false},
		{"nesting past the cap", deep, other, true},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			if got := a2bContext().equivalent(tc.a, tc.b, 0); got != tc.want {
				t.Errorf("equivalent = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckBaseColour(t *testing.T) {
	for _, tc := range []struct {
		desc      string
		cs        pdf.PDFValue
		resources pdf.PDFDict
		covered   bool
		want      bool
	}{
		{"Indexed on DeviceRGB", pdf.PDFArray{name("Indexed"), name("DeviceRGB"), pdf.PDFInteger(1), pdf.PDFString{}}, kv(), false, true},
		{"abbreviated Indexed", pdf.PDFArray{name("I"), name("DeviceGray"), pdf.PDFInteger(1), pdf.PDFString{}}, kv(), false, true},
		{"Pattern on DeviceCMYK", pdf.PDFArray{name("Pattern"), name("DeviceCMYK")}, kv(), false, true},
		{"covered by an output intent", pdf.PDFArray{name("Pattern"), name("DeviceRGB")}, kv(), true, false},
		{"covered by a Default colour space", pdf.PDFArray{name("Pattern"), name("DeviceRGB")}, kv("ColorSpace", kv("DefaultRGB", kv())), false, false},
		{"Indexed on a calibrated space", pdf.PDFArray{name("Indexed"), pdf.PDFArray{name("CalRGB"), kv()}}, kv(), false, false},
		{"other colour space array", pdf.PDFArray{name("ICCBased"), kv()}, kv(), false, false},
		{"short array", pdf.PDFArray{name("Pattern")}, kv(), false, false},
		{"name", name("DeviceRGB"), kv(), false, false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := a2bContext()
			ctx.hasOutputIntent, ctx.rgbCovered = tc.covered, tc.covered
			checkBaseColour(tc.cs, tc.resources, nil, ctx)
			if got := hasCheck(ctx, pdf.Checks.Colour.DeviceColourBase); got != tc.want {
				t.Errorf("DeviceColourBase reported = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDeviceColourBaseInContent verifies that an Indexed space selected in
// content, given in an inline image or set on an image is reported.
func TestDeviceColourBaseInContent(t *testing.T) {
	indexed := pdf.PDFArray{name("Indexed"), name("DeviceRGB"), pdf.PDFInteger(1), pdf.PDFString{}}
	resources := kv("ColorSpace", kv("CS0", indexed))
	for _, tc := range []struct {
		desc string
		run  func(ctx *ValidationContext)
	}{
		{"cs", func(ctx *ValidationContext) {
			contentChecker(nil, resources, ctx)("cs", []pdf.PDFValue{name("CS0")})
		}},
		{"inline image", func(ctx *ValidationContext) {
			contentChecker(nil, resources, ctx)("INLINEIMAGE", []pdf.PDFValue{name("CS"), indexed})
		}},
		{"image", func(ctx *ValidationContext) {
			validateColourSpaceUsage(kv("Subtype", name("Image"), "ColorSpace", indexed), ctx)
		}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := a2bContext()
			tc.run(ctx)
			if !hasCheck(ctx, pdf.Checks.Colour.DeviceColourBase) {
				t.Error("DeviceColourBase not reported")
			}
		})
	}
	if namedColourSpace(pdf.PDFInteger(1), resources) != nil {
		t.Error("namedColourSpace resolved an operand that is not a name")
	}
}

func TestValidateHalftones(t *testing.T) {
	type1 := func(pairs ...any) pdf.PDFDict {
		return kv(append([]any{"HalftoneType", pdf.PDFInteger(1)}, pairs...)...)
	}
	type5 := func(pairs ...any) pdf.PDFDict {
		return kv(append([]any{"HalftoneType", pdf.PDFInteger(5)}, pairs...)...)
	}
	tr := pdf.Checks.Transparency
	for _, tc := range []struct {
		desc string
		gs   pdf.PDFDict
		want []pdf.Check
	}{
		{"HTP", kv("HTP", pdf.PDFArray{}), []pdf.Check{tr.HalftoneExtGState}},
		{"default halftone", kv("HT", name("Default")), nil},
		{"type 1", kv("HT", type1()), nil},
		{"type 6", kv("HT", kv("HalftoneType", pdf.PDFInteger(6))), []pdf.Check{tr.HalftoneType}},
		{"no type", kv("HT", kv()), []pdf.Check{tr.HalftoneType}},
		{"halftone name", kv("HT", type1("HalftoneName", pdf.PDFString{Value: "x"})), []pdf.Check{tr.HalftoneName}},
		{"transfer function on the top halftone", kv("HT", type1("TransferFunction", name("Identity"))), []pdf.Check{tr.HalftoneTransferFunction}},
		{"type 5 components", kv("HT", type5(
			"Type", name("Halftone"),
			"Default", type1("TransferFunction", name("Identity")),
			"Cyan", type1(),
			"Spot", type1("TransferFunction", name("Identity")),
			"Note", pdf.PDFInteger(1),
		)), nil},
		{"type 5 primary with a transfer function", kv("HT", type5("Default", type1(), "Black", type1("TransferFunction", name("Identity")))), []pdf.Check{tr.HalftoneTransferFunction}},
		{"type 5 spot without a transfer function", kv("HT", type5("Default", type1(), "Spot", type1())), []pdf.Check{tr.HalftoneTransferFunction}},
		{"type 5 component of type 6", kv("HT", type5("Default", kv("HalftoneType", pdf.PDFInteger(6)))), []pdf.Check{tr.HalftoneType}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := a2bContext()
			validateExtGState(tc.gs, ctx)
			var got []pdf.Check
			for _, e := range ctx.errs {
				got = append(got, e.Check())
			}
			if len(got) != len(tc.want) {
				t.Fatalf("reported %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("reported %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestVerifyOutputIntentOtherIntents(t *testing.T) {
	reader := func(intents ...pdf.PDFValue) *pdf.Reader {
		return rawReader("", kv("Root", kv("Type", name("Catalog"), "OutputIntents", pdf.PDFArray(intents))))
	}
	ident := pdf.PDFString{Value: "Custom"}
	pdfx := kv("S", name("GTS_PDFX"), "OutputConditionIdentifier", ident)
	withRef := kv("S", name("GTS_PDFX"), "OutputConditionIdentifier", ident, "DestOutputProfileRef", kv())
	for _, tc := range []struct {
		desc    string
		a1b     bool
		intents []pdf.PDFValue
		want    []pdf.Check
	}{
		{"PDF/X intent without a profile, Part 2", false, []pdf.PDFValue{pdfx}, nil},
		{"PDF/X intent with DestOutputProfileRef", false, []pdf.PDFValue{withRef}, []pdf.Check{pdf.Checks.Colour.OutputIntentDestOutputProfileRef}},
		{"PDF/X intent, Part 1", true, []pdf.PDFValue{pdfx}, []pdf.Check{pdf.Checks.Colour.OutputIntentWrongS, pdf.Checks.Colour.OutputIntentUnresolvedProfile}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			spec := a2b()
			if tc.a1b {
				spec = a1b()
			}
			errs := verifyOutputIntent(reader(tc.intents...), spec)
			if len(errs) != len(tc.want) {
				t.Fatalf("verifyOutputIntent = %v, want %v", errs, tc.want)
			}
			for i, e := range errs {
				if e.Check() != tc.want[i] {
					t.Errorf("verifyOutputIntent = %v, want %v", errs, tc.want)
				}
			}
		})
	}
}

// TestInlineImageIntentFollowsTheLevel verifies that an inline image's
// rendering intent is reported as RenderingIntent under Part 1 and as
// ImageRenderingIntent under Part 2.
func TestInlineImageIntentFollowsTheLevel(t *testing.T) {
	params := []pdf.PDFValue{name("Intent"), name("Custom")}
	s := a1b()
	part1 := &ValidationContext{Spec: &s}
	checkInlineImageOther(nil, params, part1)
	if !hasCheck(part1, pdf.Checks.Colour.RenderingIntent) {
		t.Error("Part 1: RenderingIntent not reported")
	}
	part2 := a2bContext()
	checkInlineImageOther(nil, params, part2)
	if !hasCheck(part2, pdf.Checks.Image.ImageRenderingIntent) {
		t.Error("Part 2: ImageRenderingIntent not reported")
	}
}

func TestProcessColorant(t *testing.T) {
	if !ProcessColorant("Magenta") || ProcessColorant("Spot") {
		t.Error("ProcessColorant does not tell process colorants from spot ones")
	}
}
