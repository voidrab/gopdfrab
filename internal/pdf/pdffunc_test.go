package pdf

import (
	"bytes"
	"compress/zlib"
	"math"
	"testing"
)

func almostEqual(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

func TestExponentialFunctionLinear(t *testing.T) {
	d := PDFDict{Entries: map[string]PDFValue{
		"FunctionType": PDFInteger(2),
		"Domain":       PDFArray{PDFInteger(0), PDFInteger(1)},
		"C0":           PDFArray{PDFReal(0)},
		"C1":           PDFArray{PDFReal(1)},
		"N":            PDFInteger(1),
	}}
	fn, err := ParseFunction(d)
	if err != nil {
		t.Fatalf("ParseFunction: %v", err)
	}
	for _, x := range []float64{0, 0.25, 0.5, 1} {
		got := fn.Eval([]float64{x})
		if len(got) != 1 || !almostEqual(got[0], x, 1e-9) {
			t.Errorf("Eval(%v) = %v, want [%v]", x, got, x)
		}
	}
}

func TestExponentialFunctionDomainClamp(t *testing.T) {
	d := PDFDict{Entries: map[string]PDFValue{
		"FunctionType": PDFInteger(2),
		"Domain":       PDFArray{PDFReal(0), PDFReal(1)},
		"C0":           PDFArray{PDFReal(10)},
		"C1":           PDFArray{PDFReal(20)},
		"N":            PDFInteger(1),
	}}
	fn, err := ParseFunction(d)
	if err != nil {
		t.Fatalf("ParseFunction: %v", err)
	}
	got := fn.Eval([]float64{5})
	if !almostEqual(got[0], 20, 1e-9) {
		t.Errorf("Eval(5) clamped = %v, want 20 (clamped to Domain max)", got[0])
	}
}

func TestStitchingFunctionDispatch(t *testing.T) {
	// Two sub-functions: f1 maps [0,1] -> 0 constant, f2 maps [0,1] -> 1 constant.
	f1 := PDFDict{Entries: map[string]PDFValue{
		"FunctionType": PDFInteger(2),
		"Domain":       PDFArray{PDFReal(0), PDFReal(1)},
		"C0":           PDFArray{PDFReal(0)},
		"C1":           PDFArray{PDFReal(0)},
		"N":            PDFInteger(1),
	}}
	f2 := PDFDict{Entries: map[string]PDFValue{
		"FunctionType": PDFInteger(2),
		"Domain":       PDFArray{PDFReal(0), PDFReal(1)},
		"C0":           PDFArray{PDFReal(1)},
		"C1":           PDFArray{PDFReal(1)},
		"N":            PDFInteger(1),
	}}
	stitch := PDFDict{Entries: map[string]PDFValue{
		"FunctionType": PDFInteger(3),
		"Domain":       PDFArray{PDFReal(0), PDFReal(1)},
		"Functions":    PDFArray{f1, f2},
		"Bounds":       PDFArray{PDFReal(0.5)},
		"Encode":       PDFArray{PDFReal(0), PDFReal(1), PDFReal(0), PDFReal(1)},
	}}
	fn, err := ParseFunction(stitch)
	if err != nil {
		t.Fatalf("ParseFunction: %v", err)
	}

	if got := fn.Eval([]float64{0.25}); !almostEqual(got[0], 0, 1e-9) {
		t.Errorf("Eval(0.25) = %v, want [0] (dispatched to f1)", got)
	}
	if got := fn.Eval([]float64{0.75}); !almostEqual(got[0], 1, 1e-9) {
		t.Errorf("Eval(0.75) = %v, want [1] (dispatched to f2)", got)
	}
}

func TestSampledFunctionInterpolation(t *testing.T) {
	// 1-D, 1-output, 2 samples (at x=0 -> 0, x=1 -> 255), 8 bits per sample.
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	zw.Write([]byte{0, 255})
	zw.Close()

	d := PDFDict{
		Entries: map[string]PDFValue{
			"FunctionType":  PDFInteger(0),
			"Domain":        PDFArray{PDFReal(0), PDFReal(1)},
			"Range":         PDFArray{PDFReal(0), PDFReal(1)},
			"Size":          PDFArray{PDFInteger(2)},
			"BitsPerSample": PDFInteger(8),
			"Filter":        PDFName{Value: "FlateDecode"},
		},
		HasStream: true,
		RawStream: buf.Bytes(),
	}
	fn, err := ParseFunction(d)
	if err != nil {
		t.Fatalf("ParseFunction: %v", err)
	}

	if got := fn.Eval([]float64{0}); !almostEqual(got[0], 0, 1e-6) {
		t.Errorf("Eval(0) = %v, want [0]", got)
	}
	if got := fn.Eval([]float64{1}); !almostEqual(got[0], 1, 1e-6) {
		t.Errorf("Eval(1) = %v, want [1]", got)
	}
	if got := fn.Eval([]float64{0.5}); !almostEqual(got[0], 0.5, 0.01) {
		t.Errorf("Eval(0.5) = %v, want ~[0.5] (midpoint interpolation)", got)
	}
}

func TestPostScriptFunctionArithmetic(t *testing.T) {
	d := PDFDict{
		Entries: map[string]PDFValue{
			"FunctionType": PDFInteger(4),
			"Domain":       PDFArray{PDFReal(0), PDFReal(1), PDFReal(0), PDFReal(1)},
			"Range":        PDFArray{PDFReal(0), PDFReal(1)},
		},
		HasStream: true,
		RawStream: []byte("{ add 2 div }"),
	}
	fn, err := ParseFunction(d)
	if err != nil {
		t.Fatalf("ParseFunction: %v", err)
	}
	got := fn.Eval([]float64{0.2, 0.6})
	if len(got) != 1 || !almostEqual(got[0], 0.4, 1e-9) {
		t.Errorf("Eval(0.2, 0.6) = %v, want [0.4]", got)
	}
}

func TestPostScriptFunctionIfElse(t *testing.T) {
	d := PDFDict{
		Entries: map[string]PDFValue{
			"FunctionType": PDFInteger(4),
			"Domain":       PDFArray{PDFReal(0), PDFReal(1)},
			"Range":        PDFArray{PDFReal(0), PDFReal(1)},
		},
		HasStream: true,
		RawStream: []byte("{ dup 0.5 gt { pop 1 } { pop 0 } ifelse }"),
	}
	fn, err := ParseFunction(d)
	if err != nil {
		t.Fatalf("ParseFunction: %v", err)
	}
	if got := fn.Eval([]float64{0.9}); !almostEqual(got[0], 1, 1e-9) {
		t.Errorf("Eval(0.9) = %v, want [1]", got)
	}
	if got := fn.Eval([]float64{0.1}); !almostEqual(got[0], 0, 1e-9) {
		t.Errorf("Eval(0.1) = %v, want [0]", got)
	}
}

func TestPostScriptFunctionStackOps(t *testing.T) {
	d := PDFDict{
		Entries: map[string]PDFValue{
			"FunctionType": PDFInteger(4),
			"Domain":       PDFArray{PDFReal(0), PDFReal(1), PDFReal(0), PDFReal(1)},
			"Range":        PDFArray{PDFReal(0), PDFReal(1), PDFReal(0), PDFReal(1)},
		},
		HasStream: true,
		RawStream: []byte("{ exch }"),
	}
	fn, err := ParseFunction(d)
	if err != nil {
		t.Fatalf("ParseFunction: %v", err)
	}
	got := fn.Eval([]float64{0.2, 0.8})
	if len(got) != 2 || !almostEqual(got[0], 0.8, 1e-9) || !almostEqual(got[1], 0.2, 1e-9) {
		t.Errorf("Eval(0.2, 0.8) with exch = %v, want [0.8, 0.2]", got)
	}
}

func TestRollStack(t *testing.T) {
	stack := []psValue{{number: 1}, {number: 2}, {number: 3}, {number: 4}}
	rollStack(stack, 3, 1)
	want := []float64{1, 4, 2, 3}
	for i, w := range want {
		if stack[i].number != w {
			t.Errorf("rollStack result[%d] = %v, want %v (full: %v)", i, stack[i].number, w, stack)
		}
	}
}
