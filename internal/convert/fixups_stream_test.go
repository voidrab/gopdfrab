package convert

import (
	"bytes"
	"compress/lzw"
	"encoding/ascii85"
	"encoding/hex"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// encodeASCII85/encodeASCIIHex build fixtures in the encodings
// pdf.DecodeASCII85/pdf.DecodeASCIIHex expect to consume.
func encodeASCII85(data []byte) []byte {
	buf := make([]byte, ascii85.MaxEncodedLen(len(data)))
	n := ascii85.Encode(buf, data)
	return append(buf[:n], '~', '>')
}

func encodeASCIIHex(data []byte) []byte {
	return append([]byte(hex.EncodeToString(data)), '>')
}

// encodeLZW LZW-encodes data the way a PDF producer would (MSB order, 8-bit
// initial width), for building test fixtures.
func encodeLZW(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := lzw.NewWriter(&buf, lzw.MSB, 8)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("encodeLZW: Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("encodeLZW: Close: %v", err)
	}
	return buf.Bytes()
}

func TestDecodeLZWRoundTrips(t *testing.T) {
	want := []byte("the quick brown fox jumps over the lazy dog, the quick brown fox jumps again")
	got, err := pdf.DecodeLZW(encodeLZW(t, want))
	if err != nil {
		t.Fatalf("decodeLZW: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("decodeLZW round-trip = %q, want %q", got, want)
	}
}

// TestLZWStreamFixerAppliesOnlyToStreamLZWFilter mirrors
// TestFontDictFixerAppliesOnlyToCIDToGIDMapMissing: a Fixer must claim
// exactly its one Check, since registerFixer panics on overlap.
func TestLZWStreamFixerAppliesOnlyToStreamLZWFilter(t *testing.T) {
	fixer := lzwStreamFixer{}
	for _, c := range pdf.AllChecks() {
		want := c == pdf.Checks.Structure.StreamLZWFilter
		if got := fixer.Applies(c); got != want {
			t.Errorf("Applies(%s/%d) = %v, want %v", c.Clause(), c.Subclause(), got, want)
		}
	}
}

// lzwStreamTrailer builds a minimal Catalog/Pages/Page graph whose page
// content stream is LZW-encoded, with optional DecodeParms (for predictor
// coverage), for exercising lzwStreamFixer.
func lzwStreamTrailer(encoded []byte, decodeParms pdf.PDFDict) pdf.PDFDict {
	entries := map[string]pdf.PDFValue{"_ref": pdf.PDFRef{ObjNum: 4}, "Filter": pdf.PDFName{Value: "LZWDecode"}}
	if decodeParms.Entries != nil {
		entries["DecodeParms"] = decodeParms
	}
	streamDict := pdf.PDFDict{Entries: entries, HasStream: true, RawStream: encoded}

	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"_ref":     pdf.PDFRef{ObjNum: 3},
		"Type":     pdf.PDFName{Value: "Page"},
		"MediaBox": pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(100), pdf.PDFInteger(100)},
		"Contents": streamDict,
	}}
	pages := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"_ref": pdf.PDFRef{ObjNum: 2}, "Type": pdf.PDFName{Value: "Pages"}, "Kids": pdf.PDFArray{page}, "Count": pdf.PDFInteger(1)}}
	catalog := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"_ref": pdf.PDFRef{ObjNum: 1}, "Type": pdf.PDFName{Value: "Catalog"}, "Pages": pages}}
	return pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Root": catalog}}
}

func TestLZWStreamFixerDecodesAndSetsFlate(t *testing.T) {
	plaintext := []byte("0 0 0 rg 0 0 100 100 re f")
	trailer := lzwStreamTrailer(encodeLZW(t, plaintext), pdf.PDFDict{})

	fixer := lzwStreamFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true (stream used LZWDecode)")
	}

	page := trailer.Entries["Root"].(pdf.PDFDict).Entries["Pages"].(pdf.PDFDict).Entries["Kids"].(pdf.PDFArray)[0].(pdf.PDFDict)
	contents := page.Entries["Contents"].(pdf.PDFDict)
	if (contents.Entries["Filter"] != pdf.PDFName{Value: "FlateDecode"}) {
		t.Errorf("Filter = %v, want /FlateDecode", contents.Entries["Filter"])
	}
	decoded, err := pdf.DecodeStream(contents)
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	if string(decoded) != string(plaintext) {
		t.Errorf("decoded RawStream = %q, want %q", decoded, plaintext)
	}

	// Idempotent: a second pass over the already-fixed graph is a no-op.
	changed, err = fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix (second pass): %v", err)
	}
	if changed {
		t.Errorf("changed = true on second pass, want false (fixer must be idempotent)")
	}
}

func TestLZWStreamFixerUndoesPredictor(t *testing.T) {
	// Two 4-byte "rows" with TIFF predictor 2 (horizontal differencing).
	plaintext := []byte{10, 20, 30, 40, 5, 5, 5, 5}
	predicted := make([]byte, len(plaintext))
	copy(predicted, plaintext)
	for rowStart := 0; rowStart < len(predicted); rowStart += 4 {
		for i := rowStart + 3; i > rowStart; i-- {
			predicted[i] -= predicted[i-1]
		}
	}
	parms := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Predictor": pdf.PDFInteger(2),
		"Columns":   pdf.PDFInteger(4),
		"Colors":    pdf.PDFInteger(1),
	}}
	trailer := lzwStreamTrailer(encodeLZW(t, predicted), parms)

	fixer := lzwStreamFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true")
	}

	page := trailer.Entries["Root"].(pdf.PDFDict).Entries["Pages"].(pdf.PDFDict).Entries["Kids"].(pdf.PDFArray)[0].(pdf.PDFDict)
	contents := page.Entries["Contents"].(pdf.PDFDict)
	decoded, err := pdf.DecodeStream(contents)
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	if string(decoded) != string(plaintext) {
		t.Errorf("decoded = %v, want %v (predictor not undone)", decoded, plaintext)
	}
	if contents.Entries["DecodeParms"] != nil {
		t.Errorf("DecodeParms = %v, want removed", contents.Entries["DecodeParms"])
	}
}

// TestLZWStreamPlaintextChainedFilters drives the ASCII85Decode branch and
// the multi-filter loop: /Filter [ASCII85Decode LZWDecode] undoes ASCII85
// first (outermost, applied last when encoding), then LZW.
func TestLZWStreamPlaintextChainedFilters(t *testing.T) {
	plaintext := []byte("0 0 0 rg 0 0 100 100 re f")
	lzwed := encodeLZW(t, plaintext)
	encoded := encodeASCII85(lzwed)

	dict := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Filter": pdf.PDFArray{pdf.PDFName{Value: "ASCII85Decode"}, pdf.PDFName{Value: "LZWDecode"}},
		},
		HasStream: true, RawStream: encoded,
	}
	got, err := lzwStreamPlaintext(dict)
	if err != nil {
		t.Fatalf("lzwStreamPlaintext: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("lzwStreamPlaintext = %q, want %q", got, plaintext)
	}
}

// TestLZWStreamPlaintextASCIIHexAndPNGPredictor drives the ASCIIHexDecode
// branch and the PNG (predictor >= 10) reconstruction path, using the
// trivial "None" (filter type 0) row so no per-row math is needed.
func TestLZWStreamPlaintextASCIIHexAndPNGPredictor(t *testing.T) {
	plaintext := []byte{10, 20, 30, 40, 5, 5, 5, 5}
	var predicted []byte
	for rowStart := 0; rowStart < len(plaintext); rowStart += 4 {
		predicted = append(predicted, 0) // filter type 0: None
		predicted = append(predicted, plaintext[rowStart:rowStart+4]...)
	}
	hexed := encodeASCIIHex(predicted)

	dict := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Filter": pdf.PDFName{Value: "ASCIIHexDecode"},
			"DecodeParms": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
				"Predictor": pdf.PDFInteger(15), "Columns": pdf.PDFInteger(4), "Colors": pdf.PDFInteger(1),
			}},
		},
		HasStream: true, RawStream: hexed,
	}
	got, err := lzwStreamPlaintext(dict)
	if err != nil {
		t.Fatalf("lzwStreamPlaintext: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Errorf("lzwStreamPlaintext = %v, want %v", got, plaintext)
	}
}

// TestLZWStreamPlaintextUnsupportedFilterOrPredictor covers the two error
// branches: a filter name outside the decoder chain's switch, and a
// predictor value that is neither 1 (none), 2 (TIFF), nor >= 10 (PNG).
func TestLZWStreamPlaintextUnsupportedFilterOrPredictor(t *testing.T) {
	unsupportedFilter := pdf.PDFDict{
		Entries:   map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "CCITTFaxDecode"}},
		HasStream: true, RawStream: []byte("whatever"),
	}
	if _, err := lzwStreamPlaintext(unsupportedFilter); err == nil {
		t.Error("lzwStreamPlaintext with an unsupported filter = nil error, want an error")
	}

	unsupportedPredictor := pdf.PDFDict{
		Entries: map[string]pdf.PDFValue{
			"Filter":      pdf.PDFName{Value: "LZWDecode"},
			"DecodeParms": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Predictor": pdf.PDFInteger(3)}},
		},
		HasStream: true, RawStream: encodeLZW(t, []byte("abc")),
	}
	if _, err := lzwStreamPlaintext(unsupportedPredictor); err == nil {
		t.Error("lzwStreamPlaintext with an unsupported predictor = nil error, want an error")
	}
}

// TestLZWStreamFixerRoundTripsThroughWriter checks the fixer's output
// survives a real WriteDocument -> Open -> decode round trip as a plain
// Flate-encoded stream, with no LZWDecode filter remaining.
func TestLZWStreamFixerRoundTripsThroughWriter(t *testing.T) {
	plaintext := []byte("0 0 0 rg 0 0 100 100 re f")
	trailer := lzwStreamTrailer(encodeLZW(t, plaintext), pdf.PDFDict{})

	fixer := lzwStreamFixer{}
	if _, err := fixer.Fix(&trailer, nil); err != nil {
		t.Fatalf("Fix: %v", err)
	}

	var buf bytes.Buffer
	if err := writer.WriteDocument(&buf, trailer); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if bytes.Contains(buf.Bytes(), []byte("LZWDecode")) {
		t.Errorf("output still references LZWDecode")
	}

	doc, err := pdf.Open(writeTempPDF(t, "lzw_fixed.pdf", buf.Bytes()))
	if err != nil {
		t.Fatalf("pdf.Open(written output): %v", err)
	}
	defer doc.Close()

	graph, err := doc.ResolveGraph()
	if err != nil {
		t.Fatalf("ResolveGraph: %v", err)
	}
	gotPage := assertOnePageGraph(t, graph)
	assertContentStream(t, gotPage, string(plaintext))
}
