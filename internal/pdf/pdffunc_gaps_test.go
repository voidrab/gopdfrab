package pdf

import (
	"strings"
	"testing"
)

func fnDict(kv map[string]PDFValue) PDFDict {
	return PDFDict{Entries: DictOf(kv)}
}

var unit = PDFArray{PDFInteger(0), PDFInteger(1)}

// TestParseFunctionErrors drives every malformed-input rejection in
// ParseFunction and the per-type constructors.
func TestParseFunctionErrors(t *testing.T) {
	badArr := PDFArray{PDFName{Value: "x"}}
	expOK := func(extra map[string]PDFValue) PDFDict {
		m := map[string]PDFValue{"FunctionType": PDFInteger(2), "Domain": unit, "N": PDFInteger(1)}
		for k, v := range extra {
			m[k] = v
		}
		return fnDict(m)
	}
	stitch := func(extra map[string]PDFValue) PDFDict {
		m := map[string]PDFValue{
			"FunctionType": PDFInteger(3), "Domain": unit,
			"Functions": PDFArray{expOK(nil)}, "Bounds": PDFArray{}, "Encode": unit,
		}
		for k, v := range extra {
			m[k] = v
		}
		return fnDict(m)
	}
	sampled := func(extra map[string]PDFValue) PDFDict {
		m := map[string]PDFValue{
			"FunctionType": PDFInteger(0), "Domain": unit, "Size": PDFArray{PDFInteger(2)},
			"BitsPerSample": PDFInteger(8), "Range": unit,
		}
		for k, v := range extra {
			m[k] = v
		}
		d := fnDict(m)
		d.SetStream([]byte{0, 255})
		return d
	}

	for name, tc := range map[string]struct {
		v    PDFValue
		want string
	}{
		"not a dict":          {PDFInteger(1), "not a dictionary"},
		"unknown type":        {fnDict(map[string]PDFValue{"FunctionType": PDFInteger(9), "Domain": unit}), "unsupported FunctionType 9"},
		"domain not array":    {fnDict(map[string]PDFValue{"FunctionType": PDFInteger(2)}), "Domain"},
		"domain element":      {fnDict(map[string]PDFValue{"FunctionType": PDFInteger(2), "Domain": badArr}), "element 0 is not a number"},
		"C0 bad":              {expOK(map[string]PDFValue{"C0": badArr}), "C0"},
		"C1 bad":              {expOK(map[string]PDFValue{"C1": badArr}), "C1"},
		"functions not array": {stitch(map[string]PDFValue{"Functions": PDFInteger(1)}), "Functions: expected an array"},
		"bounds bad":          {stitch(map[string]PDFValue{"Bounds": badArr}), "Bounds"},
		"encode bad":          {stitch(map[string]PDFValue{"Encode": badArr}), "Encode"},
		"size bad":            {sampled(map[string]PDFValue{"Size": badArr}), "Size"},
		"range bad":           {sampled(map[string]PDFValue{"Range": badArr}), "Range"},
		"sample encode bad":   {sampled(map[string]PDFValue{"Encode": badArr}), "Encode"},
		"sample decode bad":   {sampled(map[string]PDFValue{"Decode": badArr}), "Decode"},
		"sample data bad": {func() PDFDict {
			d := sampled(nil)
			d.Entries.isStream = false
			return d
		}(), "sample data"},
		"postscript stream bad": {fnDict(map[string]PDFValue{"FunctionType": PDFInteger(4), "Domain": unit}), "PostScript calculator stream"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseFunction(tc.v)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestStitchingDegenerateDomain verifies that a zero-width Domain maps the
// input to the start of the Encode range.
func TestStitchingDegenerateDomain(t *testing.T) {
	sub := fnDict(map[string]PDFValue{"FunctionType": PDFInteger(2), "Domain": unit,
		"C0": PDFArray{PDFInteger(10)}, "C1": PDFArray{PDFInteger(20)}, "N": PDFInteger(1)})
	fn, err := ParseFunction(fnDict(map[string]PDFValue{
		"FunctionType": PDFInteger(3), "Domain": PDFArray{PDFInteger(0), PDFInteger(0)},
		"Functions": PDFArray{sub}, "Bounds": PDFArray{}, "Encode": PDFArray{PDFReal(0.5), PDFInteger(1)},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := fn.Eval([]float64{0}); len(got) != 1 || !almostEqual(got[0], 15, 1e-9) {
		t.Errorf("Eval = %v, want [15]", got)
	}
}

// TestPostScriptFailureInsideProcedure verifies that an unsupported operator
// inside an if or ifelse body aborts the program, and that Eval still returns
// without panicking.
func TestPostScriptFailureInsideProcedure(t *testing.T) {
	for _, prog := range []string{
		"{ 1 { frobnicate } if }",
		"{ 1 { frobnicate } { 2 } ifelse }",
	} {
		if got := evalPS(t, prog, 1, 0.5); len(got) != 1 {
			t.Errorf("%s: Eval = %v, want one output", prog, got)
		}
	}
}

func TestRollStackOutOfRangeIsNoop(t *testing.T) {
	for _, n := range []int{0, -1, 4} {
		stack := []psValue{{number: 1}, {number: 2}, {number: 3}}
		rollStack(stack, n, 1)
		for i, w := range []float64{1, 2, 3} {
			if stack[i].number != w {
				t.Errorf("n=%d: stack changed: %v", n, stack)
			}
		}
	}
}
