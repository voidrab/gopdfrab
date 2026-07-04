package pdf

import (
	"bytes"
	"compress/zlib"
	"encoding/ascii85"
	"reflect"
	"testing"
)

// TestDecodeASCIIHex covers the whitespace, odd-length, EOD, and invalid-digit
// paths of DecodeASCIIHex.
func TestDecodeASCIIHex(t *testing.T) {
	if got, err := DecodeASCIIHex([]byte("48 65 6C 6C 6F>")); err != nil || string(got) != "Hello" {
		t.Errorf("DecodeASCIIHex = %q, %v; want \"Hello\"", got, err)
	}
	if got, err := DecodeASCIIHex([]byte("414>")); err != nil || string(got) != "A@" {
		t.Errorf("odd-length DecodeASCIIHex = %q, %v; want \"A@\"", got, err)
	}
	if _, err := DecodeASCIIHex([]byte("G>")); err == nil {
		t.Error("expected error for an invalid high nibble")
	}
	if _, err := DecodeASCIIHex([]byte("4G>")); err == nil {
		t.Error("expected error for an invalid low nibble")
	}
}

// TestDecodeASCII85 round-trips stdlib-encoded data and covers the 'z', EOD,
// and invalid-byte paths.
func TestDecodeASCII85(t *testing.T) {
	orig := []byte("Hello, ASCII85 world!")
	enc := make([]byte, ascii85.MaxEncodedLen(len(orig)))
	n := ascii85.Encode(enc, orig)
	in := append(append([]byte{}, enc[:n]...), "~>"...)
	if got, err := DecodeASCII85(in); err != nil || !bytes.Equal(got, orig) {
		t.Errorf("DecodeASCII85 round-trip = %q, %v; want %q", got, err, orig)
	}

	if got, err := DecodeASCII85([]byte("z~>")); err != nil || !bytes.Equal(got, []byte{0, 0, 0, 0}) {
		t.Errorf("'z' = %v, %v; want four zero bytes", got, err)
	}
	if _, err := DecodeASCII85([]byte("v~>")); err == nil {
		t.Error("expected error for an out-of-range ASCII85 byte")
	}
}

// TestDecodeStreamFilters covers DecodeStream's filter dispatch, a filter
// cascade, and the non-stream and unsupported-filter errors.
func TestDecodeStreamFilters(t *testing.T) {
	flate := func(raw []byte) []byte {
		var buf bytes.Buffer
		zw := zlib.NewWriter(&buf)
		zw.Write(raw)
		zw.Close()
		return buf.Bytes()
	}

	if _, err := DecodeStream(PDFDict{}); err == nil {
		t.Error("expected error decoding a non-stream dict")
	}

	flateDict := PDFDict{
		Entries:   map[string]PDFValue{"Filter": PDFName{Value: "FlateDecode"}},
		HasStream: true, RawStream: flate([]byte("flated")),
	}
	if got, err := DecodeStream(flateDict); err != nil || string(got) != "flated" {
		t.Errorf("FlateDecode = %q, %v", got, err)
	}

	hexDict := PDFDict{
		Entries:   map[string]PDFValue{"Filter": PDFName{Value: "ASCIIHexDecode"}},
		HasStream: true, RawStream: []byte("48656C6C6F>"),
	}
	if got, err := DecodeStream(hexDict); err != nil || string(got) != "Hello" {
		t.Errorf("ASCIIHexDecode = %q, %v", got, err)
	}

	// Cascade: ASCII85Decode then FlateDecode (applied in array order).
	raw := []byte("cascaded content")
	flated := flate(raw)
	enc := make([]byte, ascii85.MaxEncodedLen(len(flated)))
	m := ascii85.Encode(enc, flated)
	a85 := append(append([]byte{}, enc[:m]...), "~>"...)
	cascade := PDFDict{
		Entries: map[string]PDFValue{"Filter": PDFArray{
			PDFName{Value: "ASCII85Decode"}, PDFName{Value: "FlateDecode"},
		}},
		HasStream: true, RawStream: a85,
	}
	if got, err := DecodeStream(cascade); err != nil || !bytes.Equal(got, raw) {
		t.Errorf("cascade decode = %q, %v; want %q", got, err, raw)
	}

	bad := PDFDict{
		Entries:   map[string]PDFValue{"Filter": PDFName{Value: "JPXDecode"}},
		HasStream: true, RawStream: []byte("x"),
	}
	if _, err := DecodeStream(bad); err == nil {
		t.Error("expected error for an unsupported filter")
	}
}

// TestContentScannerOperandTypes covers Scan's per-token-type stack pushes:
// integer, real, string, hex string, name, boolean, array, and dict operands.
func TestContentScannerOperandTypes(t *testing.T) {
	content := `1 2.5 (str) <48656C> /Name true [1 2] << /K 1 >> Tj`
	ops := TokenizeContent([]byte(content))
	if len(ops) != 1 || ops[0].Op != "Tj" {
		t.Fatalf("ops = %+v, want single Tj op", ops)
	}
	want := []PDFValue{
		PDFInteger(1),
		PDFReal(2.5),
		PDFString{Value: "str"},
		PDFHexString{Value: "48656C"},
		PDFName{Value: "Name"},
		PDFBoolean(true),
		PDFArray{PDFInteger(1), PDFInteger(2)},
		PDFDict{Entries: map[string]PDFValue{"K": PDFInteger(1)}},
	}
	if !reflect.DeepEqual(ops[0].Operands, want) {
		t.Errorf("operands = %#v, want %#v", ops[0].Operands, want)
	}
}

// TestContentScannerMalformedOperands covers the parse-error branches for
// arrays and dicts: an unterminated composite is dropped, not pushed.
func TestContentScannerMalformedOperands(t *testing.T) {
	if ops := TokenizeContent([]byte(`[1 2`)); len(ops) != 0 {
		t.Errorf("unterminated array: ops = %+v, want none", ops)
	}
	if ops := TokenizeContent([]byte(`<< /K 1`)); len(ops) != 0 {
		t.Errorf("unterminated dict: ops = %+v, want none", ops)
	}
}

// TestContentScannerInlineImage covers scanInlineImage's parameter type
// switch and the full BI...ID...EI span reported via InlineImageRaw.
func TestContentScannerInlineImage(t *testing.T) {
	content := `BI /W 1 /H 1 /Decode [0 1] /Gamma 2.2 ID X EI Q`
	ops := TokenizeContent([]byte(content))
	if len(ops) != 2 || ops[0].Op != "INLINEIMAGE" || ops[1].Op != "Q" {
		t.Fatalf("ops = %+v, want [INLINEIMAGE, Q]", ops)
	}
	operands := ops[0].Operands
	wantParams := []PDFValue{
		PDFName{Value: "W"}, PDFInteger(1),
		PDFName{Value: "H"}, PDFInteger(1),
		PDFName{Value: "Decode"}, PDFArray{PDFInteger(0), PDFInteger(1)},
		PDFName{Value: "Gamma"}, PDFReal(2.2),
	}
	if len(operands) != len(wantParams)+1 { // params + trailing InlineImageRaw
		t.Fatalf("operands = %#v, want %d params + raw", operands, len(wantParams))
	}
	if !reflect.DeepEqual(operands[:len(wantParams)], wantParams) {
		t.Errorf("params = %#v, want %#v", operands[:len(wantParams)], wantParams)
	}

	raw, ok := operands[len(operands)-1].(InlineImageRaw)
	if !ok {
		t.Fatalf("last operand = %#v, want InlineImageRaw", operands[len(operands)-1])
	}
	if string(raw.Data) != "X" {
		t.Errorf("InlineImageRaw.Data = %q, want %q", raw.Data, "X")
	}
	if string(raw.Bytes) != content[:len(content)-len(" Q")] {
		t.Errorf("InlineImageRaw.Bytes = %q, want the full BI...EI span", raw.Bytes)
	}
}

// TestContentScannerInlineImageEdgeCases covers scanInlineImage/skipToEI edge
// branches: missing ID (early EOF return), empty data (dataEnd/dataStart
// clamp), and an EI-like byte pair inside the image data that must not be
// mistaken for the terminator.
func TestContentScannerInlineImageEdgeCases(t *testing.T) {
	if ops := TokenizeContent([]byte(`BI /W 1`)); len(ops) != 0 {
		t.Errorf("missing ID: ops = %+v, want none", ops)
	}

	ops := TokenizeContent([]byte(`BI ID EI Q`))
	if len(ops) != 2 || ops[0].Op != "INLINEIMAGE" {
		t.Fatalf("empty data: ops = %+v", ops)
	}
	raw := ops[0].Operands[len(ops[0].Operands)-1].(InlineImageRaw)
	if len(raw.Data) != 0 {
		t.Errorf("empty data: InlineImageRaw.Data = %q, want empty", raw.Data)
	}

	ops = TokenizeContent([]byte(`BI ID XEIY EI Q`))
	if len(ops) != 2 || ops[0].Op != "INLINEIMAGE" {
		t.Fatalf("false EI: ops = %+v", ops)
	}
	raw = ops[0].Operands[len(ops[0].Operands)-1].(InlineImageRaw)
	if string(raw.Data) != "XEIY" {
		t.Errorf("false EI: InlineImageRaw.Data = %q, want %q", raw.Data, "XEIY")
	}
}
