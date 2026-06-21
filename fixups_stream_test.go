package pdfrab

import (
	"bytes"
	"compress/lzw"
	"testing"
)

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
	got, err := decodeLZW(encodeLZW(t, want))
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
	for _, c := range AllChecks() {
		want := c == Checks.Structure.StreamLZWFilter
		if got := fixer.Applies(c); got != want {
			t.Errorf("Applies(%s/%d) = %v, want %v", c.Clause(), c.Subclause(), got, want)
		}
	}
}

// lzwStreamTrailer builds a minimal Catalog/Pages/Page graph whose page
// content stream is LZW-encoded, with optional DecodeParms (for predictor
// coverage), for exercising lzwStreamFixer.
func lzwStreamTrailer(encoded []byte, decodeParms PDFDict) PDFDict {
	entries := map[string]PDFValue{"_ref": PDFRef{ObjNum: 4}, "Filter": PDFName{Value: "LZWDecode"}}
	if decodeParms.Entries != nil {
		entries["DecodeParms"] = decodeParms
	}
	streamDict := PDFDict{Entries: entries, HasStream: true, RawStream: encoded}

	page := PDFDict{Entries: map[string]PDFValue{
		"_ref":     PDFRef{ObjNum: 3},
		"Type":     PDFName{Value: "Page"},
		"MediaBox": PDFArray{PDFInteger(0), PDFInteger(0), PDFInteger(100), PDFInteger(100)},
		"Contents": streamDict,
	}}
	pages := PDFDict{Entries: map[string]PDFValue{"_ref": PDFRef{ObjNum: 2}, "Type": PDFName{Value: "Pages"}, "Kids": PDFArray{page}, "Count": PDFInteger(1)}}
	catalog := PDFDict{Entries: map[string]PDFValue{"_ref": PDFRef{ObjNum: 1}, "Type": PDFName{Value: "Catalog"}, "Pages": pages}}
	return PDFDict{Entries: map[string]PDFValue{"Root": catalog}}
}

func TestLZWStreamFixerDecodesAndMarksDirty(t *testing.T) {
	plaintext := []byte("0 0 0 rg 0 0 100 100 re f")
	trailer := lzwStreamTrailer(encodeLZW(t, plaintext), PDFDict{})

	fixer := lzwStreamFixer{}
	changed, err := fixer.Fix(&trailer, nil)
	if err != nil {
		t.Fatalf("Fix: %v", err)
	}
	if !changed {
		t.Fatalf("changed = false, want true (stream used LZWDecode)")
	}

	page := trailer.Entries["Root"].(PDFDict).Entries["Pages"].(PDFDict).Entries["Kids"].(PDFArray)[0].(PDFDict)
	contents := page.Entries["Contents"].(PDFDict)
	if contents.Entries["Filter"] != nil {
		t.Errorf("Filter = %v, want removed", contents.Entries["Filter"])
	}
	if dirty, _ := contents.Entries["_dirty"].(PDFBoolean); !bool(dirty) {
		t.Errorf("_dirty not set, want true")
	}
	if string(contents.RawStream) != string(plaintext) {
		t.Errorf("RawStream = %q, want %q", contents.RawStream, plaintext)
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
	parms := PDFDict{Entries: map[string]PDFValue{
		"Predictor": PDFInteger(2),
		"Columns":   PDFInteger(4),
		"Colors":    PDFInteger(1),
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

	page := trailer.Entries["Root"].(PDFDict).Entries["Pages"].(PDFDict).Entries["Kids"].(PDFArray)[0].(PDFDict)
	contents := page.Entries["Contents"].(PDFDict)
	if string(contents.RawStream) != string(plaintext) {
		t.Errorf("RawStream = %v, want %v (predictor not undone)", contents.RawStream, plaintext)
	}
	if contents.Entries["DecodeParms"] != nil {
		t.Errorf("DecodeParms = %v, want removed", contents.Entries["DecodeParms"])
	}
}

// TestLZWStreamFixerRoundTripsThroughWriter checks the fixer's output
// survives a real WriteDocument -> Open -> decode round trip as a plain
// Flate-encoded stream, with no LZWDecode filter remaining.
func TestLZWStreamFixerRoundTripsThroughWriter(t *testing.T) {
	plaintext := []byte("0 0 0 rg 0 0 100 100 re f")
	trailer := lzwStreamTrailer(encodeLZW(t, plaintext), PDFDict{})

	fixer := lzwStreamFixer{}
	if _, err := fixer.Fix(&trailer, nil); err != nil {
		t.Fatalf("Fix: %v", err)
	}

	var buf bytes.Buffer
	if err := WriteDocument(&buf, trailer); err != nil {
		t.Fatalf("WriteDocument: %v", err)
	}
	if bytes.Contains(buf.Bytes(), []byte("LZWDecode")) {
		t.Errorf("output still references LZWDecode")
	}

	doc, err := Open(writeTempPDF(t, "lzw_fixed.pdf", buf.Bytes()))
	if err != nil {
		t.Fatalf("Open(written output): %v", err)
	}
	defer doc.Close()

	graph, err := doc.ResolveGraph()
	if err != nil {
		t.Fatalf("ResolveGraph: %v", err)
	}
	gotPage := assertOnePageGraph(t, graph)
	assertContentStream(t, doc, gotPage, string(plaintext))
}
