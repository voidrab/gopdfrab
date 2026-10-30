package content

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func name(s string) pdf.PDFName { return pdf.PDFName{Value: s} }

func nums(vs ...float64) []pdf.PDFValue {
	out := make([]pdf.PDFValue, len(vs))
	for i, v := range vs {
		out[i] = pdf.PDFReal(v)
	}
	return out
}

// resources holds one named colour space, /CS0, which is DeviceRGB.
var resources = pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
	"ColorSpace": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"CS0": name("DeviceRGB")})},
})}

func TestNewGState(t *testing.T) {
	g := NewGState()
	if g.CTM != IdentityMatrix || g.Fill.Space != name("DeviceGray") || g.Fill.RGB != [3]float64{} || g.Stroke != g.Fill {
		t.Errorf("NewGState() = %+v, want identity and black DeviceGray", g)
	}
}

func TestGStateDeviceColours(t *testing.T) {
	for _, tc := range []struct {
		op       string
		operands []pdf.PDFValue
		space    string
		rgb      [3]float64
		stroke   bool
	}{
		{"g", nums(0.5), "DeviceGray", [3]float64{0.5, 0.5, 0.5}, false},
		{"RG", nums(1, 0, 0), "DeviceRGB", [3]float64{1, 0, 0}, true},
		{"k", nums(0, 0, 0, 1), "DeviceCMYK", [3]float64{0, 0, 0}, false},
		{"k", append(nums(9), nums(0, 0, 0, 0)...), "DeviceCMYK", [3]float64{1, 1, 1}, false}, // the last four
	} {
		g := NewGState()
		g.Fill.Pattern = "P0"
		if !g.Apply(tc.op, tc.operands, resources) {
			t.Fatalf("%s was not applied", tc.op)
		}
		got := g.Fill
		if tc.stroke {
			got = g.Stroke
		}
		if got != (Colour{Space: name(tc.space), RGB: tc.rgb}) {
			t.Errorf("%s %v: colour = %+v, want %s %v and no pattern", tc.op, tc.operands, got, tc.space, tc.rgb)
		}
	}
}

func TestGStateColourSpaces(t *testing.T) {
	g := NewGState()
	g.Apply("g", nums(1), resources)
	g.Apply("cs", []pdf.PDFValue{name("CS0")}, resources)
	if g.Fill != (Colour{Space: name("DeviceRGB")}) {
		t.Errorf("after cs: fill = %+v, want black in the resolved space", g.Fill)
	}
	g.Apply("sc", nums(0, 1, 0), resources)
	if g.Fill.RGB != [3]float64{0, 1, 0} {
		t.Errorf("after sc: fill = %v, want green", g.Fill.RGB)
	}

	// A pattern is selected by name, and the colour is left as it was.
	g.Apply("CS", []pdf.PDFValue{name("Pattern")}, resources)
	if !g.Stroke.IsPattern() || g.Stroke.Pattern != "" {
		t.Errorf("after CS /Pattern: stroke = %+v, want a pattern space with none selected", g.Stroke)
	}
	g.Apply("SCN", []pdf.PDFValue{name("P1")}, resources)
	if g.Stroke.Pattern != "P1" {
		t.Errorf("after SCN /P1: pattern = %q", g.Stroke.Pattern)
	}

	// Without a space, components cannot be read.
	g.Fill.Space = nil
	g.Apply("scn", nums(1), resources)
	if g.Fill.RGB != [3]float64{0, 1, 0} {
		t.Errorf("scn without a space changed the colour to %v", g.Fill.RGB)
	}
	if g.Apply("w", nums(1), resources) {
		t.Error("w was applied")
	}
}

// TestGStateMalformedOperands verifies that an operator short of numeric
// operands changes nothing.
func TestGStateMalformedOperands(t *testing.T) {
	for _, tc := range []struct {
		op       string
		operands []pdf.PDFValue
	}{
		{"rg", nums(1)},
		{"g", []pdf.PDFValue{name("x")}},
		{"cm", nums(1, 0, 0, 1)},
		{"cm", append(nums(1, 0, 0, 1, 0), name("x"))},
	} {
		g := NewGState()
		g.Fill.Pattern = "P0"
		want := g
		if !g.Apply(tc.op, tc.operands, resources) || g != want {
			t.Errorf("%s %v changed the state to %+v", tc.op, tc.operands, g)
		}
	}
}

func TestGStateCM(t *testing.T) {
	g := NewGState()
	g.Apply("cm", nums(2, 0, 0, 2, 0, 0), resources)
	g.Apply("cm", nums(1, 0, 0, 1, 5, 0), resources)
	if p := g.CTM.Apply(Point{X: 1, Y: 1}); p != (Point{X: 12, Y: 2}) {
		t.Errorf("CTM maps {1 1} to %v, want {12 2}: the later cm applies first", p)
	}
}

func TestColorSpaceOperand(t *testing.T) {
	for _, tc := range []struct {
		operands []pdf.PDFValue
		want     pdf.PDFValue
	}{
		{nil, nil},
		{[]pdf.PDFValue{name("DeviceCMYK")}, name("DeviceCMYK")},
		{[]pdf.PDFValue{name("Pattern")}, name("Pattern")},
		{[]pdf.PDFValue{name("CS0")}, name("DeviceRGB")},
		{[]pdf.PDFValue{name("Missing")}, name("Missing")},
		{[]pdf.PDFValue{pdf.PDFInteger(3)}, pdf.PDFInteger(3)},
	} {
		if got := ColorSpaceOperand(tc.operands, resources); got != tc.want {
			t.Errorf("ColorSpaceOperand(%v) = %v, want %v", tc.operands, got, tc.want)
		}
	}
}

func TestIsPatternSpace(t *testing.T) {
	for cs, want := range map[string]bool{"Pattern": true, "DeviceRGB": false} {
		if IsPatternSpace(name(cs)) != want {
			t.Errorf("IsPatternSpace(%s) != %v", cs, want)
		}
	}
	if !IsPatternSpace(pdf.PDFArray{name("Pattern"), name("DeviceRGB")}) {
		t.Error("an uncoloured Pattern space was not recognized")
	}
	for _, cs := range []pdf.PDFValue{pdf.PDFArray{}, pdf.PDFArray{pdf.PDFInteger(1)}, nil} {
		if IsPatternSpace(cs) {
			t.Errorf("IsPatternSpace(%v) = true", cs)
		}
	}
}

func TestTracker(t *testing.T) {
	tr := NewTracker()
	tr.Step("Q", nil, resources) // nothing saved: ignored
	tr.Step("q", nil, resources)
	tr.Step("rg", nums(1, 0, 0), resources)
	tr.Step("cm", nums(2, 0, 0, 2, 0, 0), resources)
	if tr.Depth() != 1 || tr.Fill.RGB != [3]float64{1, 0, 0} {
		t.Errorf("inside q: depth %d fill %v", tr.Depth(), tr.Fill.RGB)
	}
	tr.Step("Q", nil, resources)
	if tr.Depth() != 0 || tr.GState != NewGState() {
		t.Errorf("after Q: depth %d state %+v, want the starting state", tr.Depth(), tr.GState)
	}
	if tr.Step("re", nums(0, 0, 1, 1), resources) {
		t.Error("re was tracked")
	}
}
