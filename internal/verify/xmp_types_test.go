package verify

import "testing"

func TestXMPParseDate(t *testing.T) {
	for s, want := range map[string]bool{
		"":                               true,
		"2016":                           true,
		"-0044":                          true,
		"2016-02":                        true,
		"2016-02-01":                     true,
		"2016-02-01T13":                  true,
		"2016-02-01T13Z":                 true,
		"2016-02-01T13:19":               true,
		"2016-02-01T13:19Z":              true,
		"2016-02-01T13:19:21":            true,
		"2016-02-01T13:19:21+01:00":      true,
		"2016-02-01T13:19:21.5-01":       true,
		"2016-02-01T13:19:21.123456789Z": true,
		"2016-99-99T99:99:99":            true, // components are clamped, not rejected
		"x":                              false,
		"2016/02":                        false,
		"2016-x":                         false,
		"2016-02/01":                     false,
		"2016-02-x":                      false,
		"2016-02-01 13":                  false,
		"2016-02-01T":                    false,
		"2016-02-01T13:x":                false,
		"2016-02-01T13:19x":              false,
		"2016-02-01T13x":                 false,
		"2016-02-01T13:19:x":             false,
		"2016-02-01T13:19:21x":           false,
		"2016-02-01T13:19:21.x":          false,
		"2016-02-01T13:19:21.5x":         false,
		"2016-02-01T13:19:21+x":          false,
		"2016-02-01T13:19:21+01x":        false,
		"2016-02-01T13:19:21+01:x":       false,
		"2016-02-01T13:19:21Zx":          false,
	} {
		if got := xmpParseDate(s); got != want {
			t.Errorf("xmpParseDate(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestXMPSimplifyType(t *testing.T) {
	for in, want := range map[string]string{
		"":                         "text",
		"Choice":                   "text",
		"Closed Choice of Integer": "integer",
		"open choice of Text":      "text",
		"Lang Alt":                 "lang alt",
		"Bag":                      "bag text",
		"seq":                      "seq text",
		"Alt":                      "alt text",
		"Seq ProperName":           "seq propername",
	} {
		if got := xmpSimplifyType(in); got != want {
			t.Errorf("xmpSimplifyType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestXMPTypeSetKnown(t *testing.T) {
	for typ, want := range map[string]bool{
		"Text": true, "Bag Seq Integer": true, "Date": true, "Lang Alt": true,
		"ResourceRef": true, "Colorant": true, "GPSCoordinate": true, "Nope": false, "bag nope": false,
	} {
		if got := xmpPart2Types.known(typ); got != want {
			t.Errorf("known(%q) = %v, want %v", typ, got, want)
		}
	}
	if xmpEmptyTypes.known("Locale") || xmpEmptyTypes.known("ResourceRef") || !xmpEmptyTypes.known("Integer") {
		t.Error("xmpEmptyTypes should hold only the basic simple types")
	}
}

func TestXMPTypeSetValid(t *testing.T) {
	simple := func(v string) *xmpNode { return &xmpNode{value: v} }
	field := func(ns, name, v string) *xmpNode { return &xmpNode{ns: ns, name: name, value: v} }
	array := func(f xmpForm, kids ...*xmpNode) *xmpNode { return &xmpNode{form: f, children: kids} }
	strct := func(kids ...*xmpNode) *xmpNode { return &xmpNode{form: xmpStructure, children: kids} }
	langAlt := array(xmpAlt, &xmpNode{hasLang: true, value: "t"})
	langAlt.altText = true
	for _, tc := range []struct {
		name string
		n    *xmpNode
		typ  string
		want bool
	}{
		{"any", strct(), "any", true},
		{"integer", simple("+12"), "Integer", true},
		{"not an integer", simple("1.5"), "Integer", false},
		{"real", simple(".5"), "Real", true},
		{"boolean", simple("true"), "Boolean", false},
		{"text", simple("x"), "Text", true},
		{"text is simple", array(xmpBag), "Text", false},
		{"date", simple("2016-02-01"), "Date", true},
		{"bad date", simple("yesterday"), "Date", false},
		{"date is simple", strct(), "Date", false},
		{"lang alt", langAlt, "Lang Alt", true},
		{"empty alt", array(xmpAlt), "Lang Alt", true},
		{"plain alt", array(xmpAlt, simple("t")), "Lang Alt", false},
		{"lang alt is an alt", array(xmpSeq), "Lang Alt", false},
		{"bag", array(xmpBag, simple("a")), "Bag Text", true},
		{"seq is not a bag", array(xmpSeq, simple("a")), "Bag Text", false},
		{"bag item", array(xmpBag, simple("x")), "Bag Integer", false},
		{"gps", simple("10,20.5N"), "GPSCoordinate", true},
		{"bad gps", simple("north"), "GPSCoordinate", false},
		{"unknown", simple("x"), "Nope", false},
		{"struct", strct(field(nsDimensions, "w", "1"), field(nsDimensions, "unit", "mm")), "Dimensions", true},
		{"struct is a structure", simple("1x1"), "Dimensions", false},
		{"struct field namespace", strct(field(nsXMP, "w", "1")), "Dimensions", false},
		{"struct unknown field", strct(field(nsDimensions, "d", "1")), "Dimensions", false},
		{"struct field type", strct(field(nsDimensions, "w", "wide")), "Dimensions", false},
	} {
		if got := xmpPart2Types.valid(tc.n, tc.typ); got != tc.want {
			t.Errorf("%s: valid = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestXMPTypeSetCloneIsIndependent(t *testing.T) {
	c := xmpPart2Types.clone()
	c.simple["custom"] = nil
	c.structs["customstruct"] = xmpStructType{}
	if xmpPart2Types.known("custom") || xmpPart2Types.known("customstruct") {
		t.Error("extending a clone changed the original")
	}
	if !c.known("custom") || !c.known("customstruct") {
		t.Error("clone lost its additions")
	}
}
