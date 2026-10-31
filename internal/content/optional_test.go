package content

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func ocDict(kv ...any) pdf.PDFDict {
	d := pdf.NewPDFDict()
	for i := 0; i < len(kv); i += 2 {
		d.Entries.Set(kv[i].(string), kv[i+1].(pdf.PDFValue))
	}
	return d
}

func ocName(s string) pdf.PDFName { return pdf.PDFName{Value: s} }

func ocg(name string) pdf.PDFDict {
	return ocDict("Type", ocName("OCG"), "Name", pdf.PDFString{Value: name})
}

// catalogWith returns a catalog whose default configuration is config.
func catalogWith(config pdf.PDFValue) pdf.PDFDict {
	return ocDict("OCProperties", ocDict("D", config))
}

// TestNewOptionalContentHidesNothing covers the documents that turn no group
// off, for which no tracking is set up.
func TestNewOptionalContentHidesNothing(t *testing.T) {
	g := ocg("A")
	for name, catalog := range map[string]pdf.PDFDict{
		"no OCProperties":  ocDict(),
		"no configuration": ocDict("OCProperties", ocDict()),
		"nothing off":      catalogWith(ocDict("ON", pdf.PDFArray{g})),
		"base state on":    catalogWith(ocDict("BaseState", ocName("ON"), "OFF", pdf.PDFArray{})),
	} {
		if oc := NewOptionalContent(catalog); oc != nil {
			t.Errorf("%s: NewOptionalContent = %+v, want nil", name, oc)
		}
	}
	var none *OptionalContent
	if none.Hides(g) {
		t.Error("a nil OptionalContent hides a group")
	}
}

func TestOptionalContentGroups(t *testing.T) {
	on, off, unlisted := ocg("on"), ocg("off"), ocg("unlisted")
	config := func(base string) *OptionalContent {
		c := ocDict("ON", pdf.PDFArray{on, pdf.PDFInteger(1)}, "OFF", pdf.PDFArray{off})
		if base != "" {
			c.Entries.Set("BaseState", ocName(base))
		}
		return NewOptionalContent(catalogWith(c))
	}
	for _, tc := range []struct {
		base string
		g    pdf.PDFDict
		want bool
	}{
		{"", on, false},
		{"", off, true},
		{"", unlisted, false},
		{"OFF", on, false},
		{"OFF", off, true},
		{"OFF", unlisted, true},
	} {
		if got := config(tc.base).Hides(tc.g); got != tc.want {
			t.Errorf("BaseState %q: Hides(%v) = %v, want %v", tc.base, tc.g.Entries.Get("Name"), got, tc.want)
		}
	}

	oc := config("")
	for name, v := range map[string]pdf.PDFValue{
		"not a dictionary": ocName("off"),
		"zero dictionary":  pdf.PDFDict{},
		"untyped":          ocDict("Name", pdf.PDFString{Value: "x"}),
	} {
		if oc.Hides(v) {
			t.Errorf("%s: hidden, want visible", name)
		}
	}
}

func TestOptionalContentMemberships(t *testing.T) {
	on, off := ocg("on"), ocg("off")
	oc := NewOptionalContent(catalogWith(ocDict("OFF", pdf.PDFArray{off})))
	md := func(kv ...any) pdf.PDFDict { return ocDict(append([]any{"Type", ocName("OCMD")}, kv...)...) }
	both := pdf.PDFArray{on, off, nil}

	for _, tc := range []struct {
		name string
		md   pdf.PDFDict
		want bool // hidden
	}{
		{"no groups", md(), false},
		{"one group dictionary", md("OCGs", off), true},
		{"AnyOn by default", md("OCGs", both), false},
		{"AnyOn, all off", md("OCGs", pdf.PDFArray{off}), true},
		{"AllOn", md("OCGs", both, "P", ocName("AllOn")), true},
		{"AnyOff", md("OCGs", both, "P", ocName("AnyOff")), false},
		{"AnyOff, all on", md("OCGs", pdf.PDFArray{on}, "P", ocName("AnyOff")), true},
		{"AllOff", md("OCGs", both, "P", ocName("AllOff")), true},
		{"AllOff, all off", md("OCGs", pdf.PDFArray{off}, "P", ocName("AllOff")), false},
		{"VE over OCGs", md("OCGs", pdf.PDFArray{on}, "VE", pdf.PDFArray{ocName("Not"), on}), true},
		{"VE And", md("VE", pdf.PDFArray{ocName("And"), on, off}), true},
		{"VE And, all on", md("VE", pdf.PDFArray{ocName("And"), on, on}), false},
		{"VE Or", md("VE", pdf.PDFArray{ocName("Or"), off, on}), false},
		{"VE Or, all off", md("VE", pdf.PDFArray{ocName("Or"), off}), true},
		{"VE nested", md("VE", pdf.PDFArray{ocName("Not"), pdf.PDFArray{ocName("And"), on, off}}), false},
		{"VE malformed falls back to OCGs", md("OCGs", off, "VE", pdf.PDFArray{ocName("Xor"), on}), true},
		{"VE empty", md("OCGs", off, "VE", pdf.PDFArray{}), true},
		{"VE Not arity", md("OCGs", off, "VE", pdf.PDFArray{ocName("Not"), on, on}), true},
		{"VE And without operands", md("OCGs", off, "VE", pdf.PDFArray{ocName("And")}), true},
		{"VE bad operand", md("OCGs", off, "VE", pdf.PDFArray{ocName("Or"), pdf.PDFInteger(1)}), true},
		{"VE bad nested", md("OCGs", off, "VE", pdf.PDFArray{ocName("Or"), pdf.PDFArray{}}), true},
	} {
		if got := oc.Hides(tc.md); got != tc.want {
			t.Errorf("%s: Hides = %v, want %v", tc.name, got, tc.want)
		}
	}

	// An expression nested past the depth cap is malformed.
	deep := pdf.PDFArray{ocName("Not"), on}
	for range maxVisibilityDepth + 1 {
		deep = pdf.PDFArray{ocName("Not"), deep}
	}
	if got := oc.Hides(md("OCGs", off, "VE", deep)); !got {
		t.Error("an over-deep expression was evaluated instead of falling back to /OCGs")
	}
}

func TestMarkedContent(t *testing.T) {
	off, on := ocg("off"), ocg("on")
	oc := NewOptionalContent(catalogWith(ocDict("OFF", pdf.PDFArray{off})))
	resources := ocDict("Properties", ocDict("Off", off, "On", on))
	m := NewMarkedContent(oc)
	step := func(op string, operands ...pdf.PDFValue) { m.Step(op, operands, resources) }
	check := func(at string, want bool) {
		t.Helper()
		if m.Hidden() != want {
			t.Errorf("%s: Hidden = %v, want %v", at, m.Hidden(), want)
		}
		by, ok := m.HiddenBy()
		if ok != want || (want && by.Entries != off.Entries) {
			t.Errorf("%s: HiddenBy = %v, %v", at, by, ok)
		}
	}

	step("EMC") // unbalanced: ignored
	check("start", false)
	step("BDC", ocName("OC"), ocName("On"))
	step("BMC", ocName("Span"))
	check("visible layer", false)
	step("BDC", ocName("OC"), ocName("Off"))
	check("hidden layer", true)
	step("BDC", ocName("OC"), off) // inline property list
	step("BDC", ocName("Span"), ocName("Off"))
	step("BDC", ocName("OC"), ocName("Missing"))
	check("nested", true)
	for range 4 {
		step("EMC")
	}
	check("closed", false)
	step("EMC")
	step("EMC")
	check("all closed", false)

	none := NewMarkedContent(nil)
	none.Step("BDC", []pdf.PDFValue{ocName("OC"), off}, resources)
	if none.Hidden() {
		t.Error("a tracker without optional content hid content")
	}
}
