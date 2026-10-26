package pdfa

import (
	"slices"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func mustFor(t *testing.T, level pdf.LevelType) Spec {
	t.Helper()
	s, ok := For(level)
	if !ok {
		t.Fatalf("For(%q) found no spec", level)
	}
	return s
}

func TestForKnownLevels(t *testing.T) {
	if s := mustFor(t, pdf.A1B); s.Level != pdf.A1B || !s.PDFA {
		t.Errorf("A1B spec: Level %q, PDFA %v", s.Level, s.PDFA)
	}
	if s := mustFor(t, pdf.ObjectModel); s.Level != pdf.ObjectModel || s.PDFA {
		t.Errorf("ObjectModel spec: Level %q, PDFA %v", s.Level, s.PDFA)
	}
}

// TestForUnknownLevels: a level without a spec is one nothing can verify
// against, so For must say so rather than hand back a permissive zero Spec.
func TestForUnknownLevels(t *testing.T) {
	for _, level := range []pdf.LevelType{pdf.Undefined, "", "A-2b"} {
		if _, ok := For(level); ok {
			t.Errorf("For(%q) found a spec", level)
		}
	}
}

// TestA1BLimits pins ISO 19005-1 Table 1 (6.1.12).
func TestA1BLimits(t *testing.T) {
	want := Limits{
		MaxInteger:          2147483647,
		MinInteger:          -2147483648,
		MaxReal:             32767,
		MaxStringBytes:      65535,
		MaxNameBytes:        127,
		MaxArrayElements:    8191,
		MaxDictEntries:      4095,
		MaxCID:              65535,
		MaxDeviceNColorants: 8,
		MaxIndirectObjects:  8388607,
		MaxQNesting:         28,
	}
	if got := mustFor(t, pdf.A1B).Limits; got != want {
		t.Errorf("A1B limits = %+v, want %+v", got, want)
	}
}

func TestA1BSwitchesAndIdentity(t *testing.T) {
	s := mustFor(t, pdf.A1B)
	for name, allowed := range map[string]bool{
		"Transparency":    s.Transparency,
		"OptionalContent": s.OptionalContent,
		"XRefStreams":     s.XRefStreams,
		"EmbeddedFiles":   s.EmbeddedFiles,
	} {
		if allowed {
			t.Errorf("PDF/A-1 permits %s", name)
		}
	}
	if s.ICCMaxMajorVersion != 2 {
		t.Errorf("ICCMaxMajorVersion = %d, want 2 (ICC.1:2003-09)", s.ICCMaxMajorVersion)
	}
	if s.OutputIntentSubtype != "GTS_PDFA1" {
		t.Errorf("OutputIntentSubtype = %q", s.OutputIntentSubtype)
	}
	if s.XMPPart != "1" || s.XMPConformance != "B" {
		t.Errorf("XMP identity = part %q conformance %q, want 1/B", s.XMPPart, s.XMPConformance)
	}
	if got := s.XMPConformances.All(); !slices.Equal(got, []string{"A", "B"}) {
		t.Errorf("XMPConformances = %v, want [A B]: a 1a identifier is a Part 1 identifier", got)
	}
	if s.ModelVersion != "1.4" {
		t.Errorf("ModelVersion = %q, want 1.4", s.ModelVersion)
	}
}

func TestA1BPolicySets(t *testing.T) {
	s := mustFor(t, pdf.A1B)
	for _, tc := range []struct {
		set     string
		names   Names
		in, out []string
	}{
		{"RenderingIntents", s.RenderingIntents,
			[]string{"AbsoluteColorimetric", "RelativeColorimetric", "Saturation", "Perceptual"},
			[]string{"Default", ""}},
		{"ForbiddenActions", s.ForbiddenActions,
			[]string{"Launch", "Sound", "Movie", "ResetForm", "ImportData", "JavaScript", "Hide",
				"SetOCGState", "Rendition", "Trans", "GoTo3DView", "SetState", "NOP"},
			[]string{"GoTo", "GoToR", "URI", "Named", "SubmitForm", "Thread"}},
		{"NamedActions", s.NamedActions,
			[]string{"NextPage", "PrevPage", "FirstPage", "LastPage"},
			[]string{"Print", "GoBack"}},
		{"AnnotationSubtypes", s.AnnotationSubtypes,
			[]string{"Text", "Link", "FreeText", "Line", "Square", "Circle", "Highlight", "Underline",
				"Squiggly", "StrikeOut", "Stamp", "Ink", "Popup", "Widget", "PrinterMark", "TrapNet"},
			[]string{"FileAttachment", "Sound", "Movie", "Screen", "3D", "Polygon", "Caret"}},
		{"BlendModes", s.BlendModes,
			[]string{"Normal", "Compatible"},
			[]string{"Multiply", "Screen"}},
		{"FontFileSubtypes", s.FontFileSubtypes,
			[]string{"Type1C", "CIDFontType0C"},
			[]string{"OpenType"}},
	} {
		for _, n := range tc.in {
			if !tc.names.Has(n) {
				t.Errorf("%s lacks %q", tc.set, n)
			}
		}
		for _, n := range tc.out {
			if tc.names.Has(n) {
				t.Errorf("%s has %q", tc.set, n)
			}
		}
	}
	if got := s.ForbiddenViewerPrefKeys.All(); !slices.Equal(got,
		[]string{"PrintScaling", "PickTrayByPDFSize", "PrintPageRange", "NumCopies"}) {
		t.Errorf("ForbiddenViewerPrefKeys = %v", got)
	}
}

// TestBlendModeAllowed: /BM is a name or an array of names to choose from,
// and an array is allowed only when every entry is.
func TestBlendModeAllowed(t *testing.T) {
	s := mustFor(t, pdf.A1B)
	name := func(n string) pdf.PDFName { return pdf.PDFName{Value: n} }
	for _, tc := range []struct {
		bm   pdf.PDFValue
		want bool
	}{
		{name("Normal"), true},
		{name("Compatible"), true},
		{name("Multiply"), false},
		{pdf.PDFArray{name("Normal"), name("Compatible")}, true},
		{pdf.PDFArray{name("Normal"), name("Multiply")}, false},
		{pdf.PDFArray{name("Normal"), pdf.PDFInteger(1)}, false},
		{pdf.PDFArray{}, true},
		{pdf.PDFInteger(0), false},
		{nil, false},
	} {
		if got := s.BlendModeAllowed(tc.bm); got != tc.want {
			t.Errorf("BlendModeAllowed(%v) = %v, want %v", tc.bm, got, tc.want)
		}
	}
}

// TestObjectModelSpec: the object-model level is plain PDF. It carries the
// Arlington model and no PDF/A policy.
func TestObjectModelSpec(t *testing.T) {
	s := mustFor(t, pdf.ObjectModel)
	if s.ModelVersion != "1.4" {
		t.Errorf("ModelVersion = %q, want 1.4", s.ModelVersion)
	}
	if s.XMPPart != "" || s.OutputIntentSubtype != "" {
		t.Errorf("ObjectModel spec carries a PDF/A identity: part %q, output intent %q", s.XMPPart, s.OutputIntentSubtype)
	}
	if s.Limits != (Limits{}) {
		t.Errorf("ObjectModel spec has PDF/A limits %+v", s.Limits)
	}
}

// TestSpecCopiesAreIndependent: For hands out copies, so a caller editing
// its copy cannot change what the next caller gets.
func TestSpecCopiesAreIndependent(t *testing.T) {
	s := mustFor(t, pdf.A1B)
	s.Transparency = true
	s.Limits.MaxNameBytes = 1
	all := s.RenderingIntents.All()
	all[0] = "Bogus"
	again := mustFor(t, pdf.A1B)
	if again.Transparency || again.Limits.MaxNameBytes != 127 || again.RenderingIntents.Has("Bogus") {
		t.Error("editing one Spec copy changed the level's spec")
	}
}

func TestNames(t *testing.T) {
	n := NewNames("b", "a", "b")
	if got := n.All(); !slices.Equal(got, []string{"b", "a"}) {
		t.Errorf("All() = %v, want [b a]: first-seen order, no duplicates", got)
	}
	if !n.Has("a") || n.Has("c") {
		t.Error("Has disagrees with the members")
	}
	var zero Names
	if zero.Has("a") || len(zero.All()) != 0 {
		t.Error("the zero Names is not empty")
	}
}
