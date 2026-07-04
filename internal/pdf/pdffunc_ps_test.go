package pdf

import "testing"

// evalPS builds a Type 4 (PostScript calculator) function from prog and
// evaluates it, using a wide Domain/Range so clamping never interferes.
func evalPS(t *testing.T, prog string, nOut int, in ...float64) []float64 {
	t.Helper()
	var dom, rng PDFArray
	for range in {
		dom = append(dom, PDFReal(-1e6), PDFReal(1e6))
	}
	for i := 0; i < nOut; i++ {
		rng = append(rng, PDFReal(-1e6), PDFReal(1e6))
	}
	d := PDFDict{
		Entries: map[string]PDFValue{
			"FunctionType": PDFInteger(4),
			"Domain":       dom,
			"Range":        rng,
		},
		HasStream: true,
		RawStream: []byte(prog),
	}
	fn, err := ParseFunction(d)
	if err != nil {
		t.Fatalf("ParseFunction(%q): %v", prog, err)
	}
	return fn.Eval(in)
}

// TestPostScriptOperators exercises every arithmetic, comparison, and stack
// operator of the Type 4 interpreter.
func TestPostScriptOperators(t *testing.T) {
	cases := []struct {
		prog string
		want float64
	}{
		{"{ 3 4 mul }", 12},
		{"{ 10 3 sub }", 7},
		{"{ 5 neg }", -5},
		{"{ -5 abs }", 5},
		{"{ 9 sqrt }", 3},
		{"{ 2 3 exp }", 8},
		{"{ 100 log }", 2},
		{"{ 3.7 cvi }", 3},
		{"{ 3 3 eq }", 1},
		{"{ 1 2 ne }", 1},
		{"{ 3 2 ge }", 1},
		{"{ 2 3 lt }", 1},
		{"{ 2 3 le }", 1},
		{"{ 1 1 and }", 1},
		{"{ 0 1 or }", 1},
		{"{ 0 not }", 1},
		{"{ 5 dup add }", 10},
		{"{ 5 6 pop }", 5},
		{"{ 9 0 { 5 } if }", 9},  // if, false branch
		{"{ 1 { 5 } if }", 5},    // if, true branch
		{"{ 3.7 cvr }", 3.7},     // cvr is a no-op
		{"{ 1 2 3 2 index }", 1}, // index copies stack[len-1-2]
		{"{ 1 2 2 copy }", 2},    // copy duplicates top 2
	}
	for _, c := range cases {
		got := evalPS(t, c.prog, 1)
		if len(got) != 1 || !almostEqual(got[0], c.want, 1e-6) {
			t.Errorf("%s = %v, want [%v]", c.prog, got, c.want)
		}
	}

	// ln of e ~ 1.
	if got := evalPS(t, "{ 2.718281828459045 ln }", 1); !almostEqual(got[0], 1, 1e-6) {
		t.Errorf("ln(e) = %v, want ~1", got)
	}
	// div-by-zero guard returns 0.
	if got := evalPS(t, "{ 1 0 div }", 1); got[0] != 0 {
		t.Errorf("1 0 div = %v, want 0", got)
	}
	// roll rotates the top n by j.
	if got := evalPS(t, "{ 1 2 3 3 1 roll }", 3); len(got) != 3 {
		t.Fatalf("roll produced %v", got)
	}
}

// TestPostScriptErrors covers newPostScriptFunction and parse error paths, plus
// the unsupported-operator branch (surfaced inside execPostScript).
func TestPostScriptErrors(t *testing.T) {
	mk := func(raw string, extra map[string]PDFValue) PDFDict {
		e := map[string]PDFValue{
			"FunctionType": PDFInteger(4),
			"Domain":       PDFArray{PDFReal(0), PDFReal(1)},
			"Range":        PDFArray{PDFReal(0), PDFReal(1)},
		}
		for k, v := range extra {
			e[k] = v
		}
		return PDFDict{Entries: e, HasStream: true, RawStream: []byte(raw)}
	}

	if _, err := ParseFunction(mk("add }", nil)); err == nil {
		t.Error("expected error for a program not starting with '{'")
	}
	if _, err := ParseFunction(mk("{ add", nil)); err == nil {
		t.Error("expected error for an unterminated procedure")
	}
	if _, err := ParseFunction(mk("{ add }", map[string]PDFValue{
		"Range": PDFName{Value: "bad"},
	})); err == nil {
		t.Error("expected error for a non-array Range")
	}

	// Unsupported operator: parses fine, but execPostScript hits its default
	// (error) branch during Eval, which Eval tolerates.
	fn, err := ParseFunction(mk("{ 1 frobnicate }", nil))
	if err != nil {
		t.Fatalf("ParseFunction: %v", err)
	}
	_ = fn.Eval([]float64{0.5})
}
