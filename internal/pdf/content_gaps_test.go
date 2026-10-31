package pdf

import (
	"reflect"
	"testing"
)

func TestDecodeASCIIHexWhitespaceEdges(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"  \n", ""},  // only whitespace: input ends before any digit
		{"4 1>", "A"}, // whitespace between the two nibbles
		{"4 ", "@"},   // input ends after the high nibble
		{"41 \t 42>", "AB"},
	} {
		got, err := DecodeASCIIHex([]byte(tc.in))
		if err != nil || string(got) != tc.want {
			t.Errorf("DecodeASCIIHex(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestDecodeASCII85TrailingWhitespace(t *testing.T) {
	got, err := DecodeASCII85([]byte("87cURDZ \n"))
	if err != nil || string(got) != "Hello" {
		t.Errorf("DecodeASCII85 = %q, %v; want Hello", got, err)
	}
	if got, err = DecodeASCII85([]byte(" \n")); err != nil || len(got) != 0 {
		t.Errorf("whitespace only = %q, %v; want empty", got, err)
	}
}

func TestStreamKeyOfArrayDecodeParms(t *testing.T) {
	parms := PDFArray{PDFInteger(1)}
	d := NewStreamDict(DictOf(map[string]PDFValue{
		"DecodeParms": parms,
	}), []byte("x"))
	k, ok := StreamKeyOf(d)
	if !ok || k.parms != ArrayPointer(parms) {
		t.Errorf("StreamKeyOf = %+v, %v; want parms keyed on the array", k, ok)
	}
}

func TestContentScannerStrayClosers(t *testing.T) {
	// ']' and '>>' outside any array/dict are discarded along with the operands
	// collected so far.
	ops := TokenizeContent([]byte("1 2 ] 3 >> m"))
	if len(ops) != 1 || ops[0].Op != "m" || len(ops[0].Operands) != 0 {
		t.Errorf("ops = %+v, want one m with no operands", ops)
	}
}

func TestContentScannerInlineImageDetails(t *testing.T) {
	ops := TokenizeContent([]byte("BI /IM true ID\x00\x01 EI Q"))
	if len(ops) != 2 || ops[0].Op != "INLINEIMAGE" {
		t.Fatalf("ops = %+v", ops)
	}
	want := []PDFValue{PDFName{Value: "IM"}, PDFBoolean(true)}
	params, raw, ok := InlineImageRawOperand(ops[0].Operands)
	if !ok || !reflect.DeepEqual(params, want) {
		t.Errorf("params = %#v, %v; want %#v", params, ok, want)
	}
	if string(raw.Data) != "\x01" {
		t.Errorf("Data = %q", raw.Data)
	}

	// Data starting immediately after "ID" with no separator: the byte is data.
	ops = TokenizeContent([]byte("BI ID(a EI"))
	_, raw, ok = InlineImageRawOperand(ops[0].Operands)
	if !ok || string(raw.Data) != "(a" {
		t.Errorf("no-separator Data = %q, %v", raw.Data, ok)
	}

	// No terminating EI: the data runs to the end of the stream.
	ops = TokenizeContent([]byte("BI ID abc"))
	_, raw, ok = InlineImageRawOperand(ops[0].Operands)
	if !ok || string(raw.Data) != "abc" {
		t.Errorf("unterminated Data = %q, %v", raw.Data, ok)
	}
}

func TestInlineImageRawOperand(t *testing.T) {
	if _, _, ok := InlineImageRawOperand(nil); ok {
		t.Error("nil operands: ok")
	}
	if _, _, ok := InlineImageRawOperand([]PDFValue{PDFInteger(1)}); ok {
		t.Error("no raw marker: ok")
	}
}

func TestNumericOperands(t *testing.T) {
	got := NumericOperands([]PDFValue{PDFInteger(1), PDFName{Value: "N"}, PDFReal(2.5)})
	if !reflect.DeepEqual(got, []float64{1, 2.5}) {
		t.Errorf("NumericOperands = %v, want [1 2.5]", got)
	}
	if got := NumericOperands(nil); got != nil {
		t.Errorf("NumericOperands(nil) = %v, want nil", got)
	}
}
