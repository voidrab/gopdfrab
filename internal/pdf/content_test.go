package pdf

import (
	"bytes"
	"compress/zlib"
	"encoding/ascii85"
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
