package pdf

import (
	"bytes"
	"compress/zlib"
	"encoding/ascii85"
	"errors"
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
		Entries:   map[string]PDFValue{"Filter": PDFName{Value: "Frobnicate"}},
		HasStream: true, RawStream: []byte("x"),
	}
	if _, err := DecodeStream(bad); !errors.Is(err, ErrUnsupportedFilter) {
		t.Errorf("unknown filter err = %v, want ErrUnsupportedFilter", err)
	}

	if _, err := DecodeStream(PDFDict{}); !errors.Is(err, ErrNotAStream) {
		t.Error("expected ErrNotAStream for a non-stream dict")
	}
}

// TestDecodeStreamNewFilters covers the filters the chain gained with the
// duplicate-decoder collapse: RunLengthDecode, LZW reachable from a stream
// dict, /EarlyChange, and a predictor riding on LZW rather than Flate.
func TestDecodeStreamNewFilters(t *testing.T) {
	t.Run("RunLengthDecode", func(t *testing.T) {
		dict := PDFDict{
			Entries:   map[string]PDFValue{"Filter": PDFName{Value: "RunLengthDecode"}},
			HasStream: true, RawStream: []byte{2, 'a', 'b', 'c', 255, 'z', 128},
		}
		if got, err := DecodeStream(dict); err != nil || string(got) != "abczz" {
			t.Errorf("RunLengthDecode = %q, %v; want \"abczz\"", got, err)
		}
	})

	t.Run("ASCII85 then RunLength", func(t *testing.T) {
		rle := []byte{2, 'a', 'b', 'c', 128}
		enc := make([]byte, ascii85.MaxEncodedLen(len(rle)))
		m := ascii85.Encode(enc, rle)
		dict := PDFDict{
			Entries: map[string]PDFValue{"Filter": PDFArray{
				PDFName{Value: "ASCII85Decode"}, PDFName{Value: "RunLengthDecode"},
			}},
			HasStream: true, RawStream: append(append([]byte{}, enc[:m]...), "~>"...),
		}
		if got, err := DecodeStream(dict); err != nil || string(got) != "abc" {
			t.Errorf("A85+RL = %q, %v; want \"abc\"", got, err)
		}
	})

	// /EarlyChange 0 must actually reach the decoder. Nothing in the codebase
	// read this parameter before the chain was unified.
	t.Run("EarlyChange reaches the decoder", func(t *testing.T) {
		codes := make([]int, 254)
		widths := make([]int, 254)
		for i := range codes {
			codes[i] = 65
			widths[i] = 9
		}
		codes = append(codes, 66, lzwEOD)
		widths = append(widths, 9, 9)
		raw := packLZWCodesVarWidth(codes, widths)

		mk := func(parms PDFValue) PDFDict {
			e := map[string]PDFValue{"Filter": PDFName{Value: "LZWDecode"}}
			if parms != nil {
				e["DecodeParms"] = parms
			}
			return PDFDict{Entries: e, HasStream: true, RawStream: raw}
		}
		zero, err := DecodeStream(mk(PDFDict{Entries: map[string]PDFValue{"EarlyChange": PDFInteger(0)}}))
		if err != nil {
			t.Fatalf("EarlyChange 0: %v", err)
		}
		want := append(bytes.Repeat([]byte("A"), 254), 'B')
		if !bytes.Equal(zero, want) {
			t.Errorf("EarlyChange 0 = %q (len %d), want 254 'A's then 'B'", zero, len(zero))
		}
		if dflt, err := DecodeStream(mk(nil)); err == nil && bytes.Equal(dflt, zero) {
			t.Error("EarlyChange 0 decoded like the default; the parameter is being ignored")
		}
	})

	// LZW plus a predictor in one chain -- a combination no decode path could
	// express before, since the LZW-capable copy lived in convert.
	t.Run("LZW with a predictor", func(t *testing.T) {
		plaintext := []byte{10, 20, 30, 40, 5, 5, 5, 5}
		var predicted []byte
		for row := 0; row < len(plaintext); row += 4 {
			predicted = append(predicted, 0) // PNG filter type 0: None
			predicted = append(predicted, plaintext[row:row+4]...)
		}
		dict := PDFDict{
			Entries: map[string]PDFValue{
				"Filter": PDFName{Value: "LZWDecode"},
				"DecodeParms": PDFDict{Entries: map[string]PDFValue{
					"Predictor": PDFInteger(12), "Columns": PDFInteger(4), "Colors": PDFInteger(1),
				}},
			},
			HasStream: true, RawStream: lzwEncodeLiterals(predicted),
		}
		got, err := DecodeStream(dict)
		if err != nil {
			t.Fatalf("LZW with predictor: %v", err)
		}
		if !bytes.Equal(got, plaintext) {
			t.Errorf("LZW with predictor = %v, want %v", got, plaintext)
		}
	})
}

// lzwEncodeLiterals emits data as a sequence of 9-bit literal codes with no
// table reuse -- a valid, if maximally verbose, LZW stream.
func lzwEncodeLiterals(data []byte) []byte {
	codes := make([]int, 0, len(data)+2)
	codes = append(codes, lzwClearTable)
	for _, b := range data {
		codes = append(codes, int(b))
	}
	codes = append(codes, lzwEOD)
	return packLZWCodes(codes, 9)
}

// TestLookupFilter covers both spellings and the image/predictor flags.
func TestLookupFilter(t *testing.T) {
	for _, tc := range []struct {
		name      string
		kind      FilterKind
		image     bool
		predictor bool
	}{
		{"FlateDecode", FilterFlate, false, true},
		{"Fl", FilterFlate, false, true},
		{"LZWDecode", FilterLZW, false, true},
		{"LZW", FilterLZW, false, true},
		{"ASCIIHexDecode", FilterASCIIHex, false, false},
		{"AHx", FilterASCIIHex, false, false},
		{"ASCII85Decode", FilterASCII85, false, false},
		{"A85", FilterASCII85, false, false},
		{"RunLengthDecode", FilterRunLength, false, false},
		{"RL", FilterRunLength, false, false},
		{"CCITTFaxDecode", FilterCCITT, true, false},
		{"CCF", FilterCCITT, true, false},
		{"DCTDecode", FilterDCT, true, false},
		{"DCT", FilterDCT, true, false},
		{"JBIG2Decode", FilterJBIG2, true, false},
		{"JPXDecode", FilterJPX, true, false},
	} {
		info, ok := LookupFilter(tc.name)
		if !ok {
			t.Errorf("LookupFilter(%q) not found", tc.name)
			continue
		}
		if info.Kind != tc.kind || info.Image != tc.image || info.Predictor != tc.predictor {
			t.Errorf("LookupFilter(%q) = %+v; want kind %v image %v predictor %v",
				tc.name, info, tc.kind, tc.image, tc.predictor)
		}
	}
	if _, ok := LookupFilter("NotAFilter"); ok {
		t.Error("LookupFilter accepted an unknown name")
	}
}

// TestHasFilter covers the name, array and both-spelling forms.
func TestHasFilter(t *testing.T) {
	if !HasFilter(PDFName{Value: "LZWDecode"}, FilterLZW) {
		t.Error("HasFilter missed a bare LZWDecode name")
	}
	if !HasFilter(PDFName{Value: "LZW"}, FilterLZW) {
		t.Error("HasFilter missed the LZW abbreviation")
	}
	arr := PDFArray{PDFName{Value: "ASCII85Decode"}, PDFName{Value: "LZW"}}
	if !HasFilter(arr, FilterLZW) {
		t.Error("HasFilter missed LZW inside a filter array")
	}
	if HasFilter(arr, FilterFlate) {
		t.Error("HasFilter reported Flate for an array without it")
	}
	if HasFilter(nil, FilterLZW) {
		t.Error("HasFilter reported a filter for an absent /Filter")
	}
}

// TestDecodeStreamImageFilters covers the typed image result: a chain ending
// in an image codec is well-formed but has no byte representation, which must
// be distinguishable from a damaged stream.
func TestDecodeStreamImageFilters(t *testing.T) {
	jpeg := PDFDict{
		Entries:   map[string]PDFValue{"Filter": PDFName{Value: "DCTDecode"}},
		HasStream: true, RawStream: []byte("jpegbytes"),
	}

	_, err := DecodeStream(jpeg)
	if !errors.Is(err, ErrEncodedImage) {
		t.Errorf("DecodeStream on DCTDecode = %v, want ErrEncodedImage", err)
	}
	if errors.Is(err, ErrUnsupportedFilter) {
		t.Error("an image filter must not read as unsupported -- callers rely on the distinction")
	}

	s, err := DecodeStreamFull(jpeg, DecodeOptions{})
	if err != nil {
		t.Fatalf("DecodeStreamFull: %v", err)
	}
	if !s.IsImage() || s.Image.Kind != FilterDCT {
		t.Fatalf("DecodeStreamFull image = %+v, want DCTDecode", s.Image)
	}
	if string(s.Data) != "jpegbytes" {
		t.Errorf("image payload = %q, want the raw stream", s.Data)
	}

	// Preceding filters are undone before the codec sees the bytes.
	enc := make([]byte, ascii85.MaxEncodedLen(len("jpegbytes")))
	m := ascii85.Encode(enc, []byte("jpegbytes"))
	wrapped := PDFDict{
		Entries: map[string]PDFValue{"Filter": PDFArray{
			PDFName{Value: "ASCII85Decode"}, PDFName{Value: "DCTDecode"},
		}},
		HasStream: true, RawStream: append(append([]byte{}, enc[:m]...), "~>"...),
	}
	s, err = DecodeStreamFull(wrapped, DecodeOptions{})
	if err != nil {
		t.Fatalf("DecodeStreamFull on wrapped image: %v", err)
	}
	if !s.IsImage() || string(s.Data) != "jpegbytes" {
		t.Errorf("wrapped image payload = %q, want the ASCII85-decoded bytes", s.Data)
	}

	// An image codec consumes the stream, so nothing may follow it.
	notLast := PDFDict{
		Entries: map[string]PDFValue{"Filter": PDFArray{
			PDFName{Value: "DCTDecode"}, PDFName{Value: "FlateDecode"},
		}},
		HasStream: true, RawStream: []byte("x"),
	}
	if _, err := DecodeStreamFull(notLast, DecodeOptions{}); !errors.Is(err, ErrUnsupportedFilter) {
		t.Errorf("image filter not last = %v, want ErrUnsupportedFilter", err)
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

// TestImageDecodeOptions covers the image-specific predictor defaults: an
// image stream takes Columns from /Width and BitsPerComponent from its own,
// rather than the spec's 1 and 8.
func TestImageDecodeOptions(t *testing.T) {
	dict := PDFDict{Entries: map[string]PDFValue{
		"Width":            PDFInteger(64),
		"BitsPerComponent": PDFInteger(4),
	}}
	opts := ImageDecodeOptions(dict)
	if opts.Columns != 64 || opts.BitsPerComponent != 4 || !opts.LenientPredictor {
		t.Errorf("ImageDecodeOptions = %+v, want Columns 64, BPC 4, lenient", opts)
	}

	// Absent entries fall back to the spec defaults.
	bare := ImageDecodeOptions(PDFDict{Entries: map[string]PDFValue{}})
	if bare.Columns != 1 || bare.BitsPerComponent != 8 {
		t.Errorf("bare ImageDecodeOptions = %+v, want Columns 1, BPC 8", bare)
	}

	// LenientPredictor accepts a predictor value the strict chain rejects.
	if _, err := UndoStreamPredictor([]byte{0, 1, 2},
		PDFDict{Entries: map[string]PDFValue{"Predictor": PDFInteger(5)}},
		DecodeOptions{Columns: 2, LenientPredictor: true}); err != nil {
		t.Errorf("lenient predictor still errored: %v", err)
	}
}

// TestDecodeStreamCryptFilter covers the Crypt filter: a known name with no
// decoder, which must read as unsupported rather than unknown.
func TestDecodeStreamCryptFilter(t *testing.T) {
	dict := PDFDict{
		Entries:   map[string]PDFValue{"Filter": PDFName{Value: "Crypt"}},
		HasStream: true, RawStream: []byte("x"),
	}
	if _, err := DecodeStream(dict); !errors.Is(err, ErrUnsupportedFilter) {
		t.Errorf("Crypt filter = %v, want ErrUnsupportedFilter", err)
	}
}
