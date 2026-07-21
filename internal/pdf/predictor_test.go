package pdf

import (
	"bytes"
	"compress/zlib"
	"testing"
)

// TestUndoPNGPredictorFilters exercises every PNG filter type (None/Sub/Up/
// Average/Paeth) plus the unsupported-filter and invalid-parameter errors.
func TestUndoPNGPredictorFilters(t *testing.T) {
	// columns=2 colors=1 bpc=8 => bpp=1, rowBytes=2. One row per filter type.
	data := []byte{
		0, 10, 20, // None  -> {10,20}
		1, 5, 3, // Sub   -> {5,8}
		2, 1, 1, // Up    -> {6,9}
		3, 0, 0, // Average
		4, 0, 0, // Paeth
	}
	out, err := UndoPNGPredictor(data, 2, 1, 8)
	if err != nil {
		t.Fatalf("UndoPNGPredictor: %v", err)
	}
	want := []byte{10, 20, 5, 8, 6, 9, 3, 6, 3, 6}
	if !bytes.Equal(out, want) {
		t.Errorf("UndoPNGPredictor = %v, want %v", out, want)
	}

	if _, err := UndoPNGPredictor([]byte{9, 0, 0}, 2, 1, 8); err == nil {
		t.Error("expected error for an unsupported PNG filter type")
	}
	if _, err := UndoPNGPredictor([]byte{0}, 0, 1, 8); err == nil {
		t.Error("expected error for invalid predictor parameters")
	}
}

// TestUndoTIFFPredictor covers TIFF horizontal differencing and its two errors.
func TestUndoTIFFPredictor(t *testing.T) {
	out, err := UndoTIFFPredictor([]byte{1, 2, 3}, 3, 1, 8)
	if err != nil {
		t.Fatalf("UndoTIFFPredictor: %v", err)
	}
	if want := []byte{1, 3, 6}; !bytes.Equal(out, want) {
		t.Errorf("UndoTIFFPredictor = %v, want %v", out, want)
	}
	if _, err := UndoTIFFPredictor(nil, 3, 1, 16); err == nil {
		t.Error("expected error for non-8-bit TIFF predictor")
	}
	if _, err := UndoTIFFPredictor(nil, 0, 1, 8); err == nil {
		t.Error("expected error for invalid TIFF predictor parameters")
	}
}

// TestFilterDecodeParms covers the dict, array, /DP-fallback and positional
// forms, including the lone-dict shorthand real writers emit.
func TestFilterDecodeParms(t *testing.T) {
	// A lone dict on a single-filter chain belongs to filter 0.
	single := PDFDict{Entries: map[string]PDFValue{
		"Filter":      PDFName{Value: "FlateDecode"},
		"DecodeParms": PDFDict{Entries: map[string]PDFValue{"Predictor": PDFInteger(12)}},
	}}
	if got := DictInt(FilterDecodeParms(single, 0, 1), "Predictor", 1); got != 12 {
		t.Errorf("single-dict Predictor = %d, want 12", got)
	}

	// An array is matched positionally; a null element means no parameters.
	arr := PDFDict{Entries: map[string]PDFValue{
		"Filter":      PDFArray{PDFName{Value: "ASCII85Decode"}, PDFName{Value: "FlateDecode"}},
		"DecodeParms": PDFArray{nil, PDFDict{Entries: map[string]PDFValue{"Columns": PDFInteger(5)}}},
	}}
	if got := DictInt(FilterDecodeParms(arr, 1, 2), "Columns", 0); got != 5 {
		t.Errorf("array filter 1 Columns = %d, want 5", got)
	}
	if got := FilterDecodeParms(arr, 0, 2); len(got.Entries) != 0 {
		t.Errorf("array filter 0 = %v, want empty dict", got)
	}

	// A lone dict on a multi-filter chain attaches to the sole predictor-taking
	// filter, not to filter 0.
	lone := PDFDict{Entries: map[string]PDFValue{
		"Filter":      PDFArray{PDFName{Value: "ASCII85Decode"}, PDFName{Value: "FlateDecode"}},
		"DecodeParms": PDFDict{Entries: map[string]PDFValue{"Predictor": PDFInteger(12)}},
	}}
	if got := DictInt(FilterDecodeParms(lone, 1, 2), "Predictor", 1); got != 12 {
		t.Errorf("lone-dict filter 1 Predictor = %d, want 12", got)
	}
	if got := DictInt(FilterDecodeParms(lone, 0, 2), "Predictor", 1); got != 1 {
		t.Errorf("lone-dict filter 0 Predictor = %d, want 1 (unattached)", got)
	}

	// With no predictor-taking filter, a lone dict falls back to filter 0.
	noPred := PDFDict{Entries: map[string]PDFValue{
		"Filter":      PDFArray{PDFName{Value: "ASCIIHexDecode"}, PDFName{Value: "ASCII85Decode"}},
		"DecodeParms": PDFDict{Entries: map[string]PDFValue{"Columns": PDFInteger(7)}},
	}}
	if got := DictInt(FilterDecodeParms(noPred, 0, 2), "Columns", 0); got != 7 {
		t.Errorf("no-predictor lone dict filter 0 Columns = %d, want 7", got)
	}

	dp := PDFDict{Entries: map[string]PDFValue{
		"Filter": PDFName{Value: "FlateDecode"},
		"DP":     PDFDict{Entries: map[string]PDFValue{"Colors": PDFInteger(3)}},
	}}
	if got := DictInt(FilterDecodeParms(dp, 0, 1), "Colors", 0); got != 3 {
		t.Errorf("/DP Colors = %d, want 3", got)
	}

	if got := FilterDecodeParms(PDFDict{Entries: map[string]PDFValue{}}, 0, 1); len(got.Entries) != 0 {
		t.Errorf("absent DecodeParms = %v, want empty dict", got)
	}
}

// TestDecodeStreamPredictorInChain covers predictors applied inside the decode
// chain across the no-predictor, PNG, TIFF, and unsupported-predictor paths.
func TestDecodeStreamPredictorInChain(t *testing.T) {
	flate := func(raw []byte) []byte {
		var buf bytes.Buffer
		zw := zlib.NewWriter(&buf)
		zw.Write(raw)
		zw.Close()
		return buf.Bytes()
	}
	streamDict := func(raw []byte, parms map[string]PDFValue) PDFDict {
		e := map[string]PDFValue{"Filter": PDFName{Value: "FlateDecode"}}
		if parms != nil {
			e["DecodeParms"] = PDFDict{Entries: parms}
		}
		return PDFDict{Entries: e, HasStream: true, RawStream: flate(raw)}
	}

	// Predictor 1 (none): decodes verbatim.
	got, err := DecodeStream(streamDict([]byte("hello"), nil))
	if err != nil || string(got) != "hello" {
		t.Fatalf("no-predictor = %q, %v; want \"hello\"", got, err)
	}

	// PNG Up predictor (12): two rows, filter byte 2 each.
	pngRaw := []byte{2, 1, 2, 2, 10, 10}
	got, err = DecodeStream(streamDict(pngRaw, map[string]PDFValue{
		"Predictor": PDFInteger(12), "Columns": PDFInteger(2),
	}))
	if err != nil {
		t.Fatalf("PNG predictor: %v", err)
	}
	if want := []byte{1, 2, 11, 12}; !bytes.Equal(got, want) {
		t.Errorf("PNG predictor = %v, want %v", got, want)
	}

	// TIFF predictor (2).
	got, err = DecodeStream(streamDict([]byte{1, 2, 3}, map[string]PDFValue{
		"Predictor": PDFInteger(2), "Columns": PDFInteger(3),
	}))
	if err != nil || !bytes.Equal(got, []byte{1, 3, 6}) {
		t.Errorf("TIFF predictor = %v, %v; want [1 3 6]", got, err)
	}

	// Unsupported predictor value.
	if _, err := DecodeStream(streamDict([]byte("x"), map[string]PDFValue{
		"Predictor": PDFInteger(5),
	})); err == nil {
		t.Error("expected error for an unsupported predictor")
	}
}
