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

// TestStreamDecodeParms covers the dict, array, and /DP-fallback forms.
func TestStreamDecodeParms(t *testing.T) {
	single := PDFDict{Entries: map[string]PDFValue{
		"DecodeParms": PDFDict{Entries: map[string]PDFValue{"Predictor": PDFInteger(12)}},
	}}
	if got := DictInt(StreamDecodeParms(single), "Predictor", 1); got != 12 {
		t.Errorf("single-dict Predictor = %d, want 12", got)
	}

	arr := PDFDict{Entries: map[string]PDFValue{
		"DecodeParms": PDFArray{nil, PDFDict{Entries: map[string]PDFValue{"Columns": PDFInteger(5)}}},
	}}
	if got := DictInt(StreamDecodeParms(arr), "Columns", 0); got != 5 {
		t.Errorf("array Columns = %d, want 5", got)
	}

	dp := PDFDict{Entries: map[string]PDFValue{
		"DP": PDFDict{Entries: map[string]PDFValue{"Colors": PDFInteger(3)}},
	}}
	if got := DictInt(StreamDecodeParms(dp), "Colors", 0); got != 3 {
		t.Errorf("/DP Colors = %d, want 3", got)
	}

	if got := StreamDecodeParms(PDFDict{Entries: map[string]PDFValue{}}); len(got.Entries) != 0 {
		t.Errorf("absent DecodeParms = %v, want empty dict", got)
	}
}

// TestDecodeStreamPredicted covers decodeStreamPredicted across the no-predictor,
// PNG, TIFF, and unsupported-predictor paths.
func TestDecodeStreamPredicted(t *testing.T) {
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
	got, err := decodeStreamPredicted(streamDict([]byte("hello"), nil))
	if err != nil || string(got) != "hello" {
		t.Fatalf("no-predictor = %q, %v; want \"hello\"", got, err)
	}

	// PNG Up predictor (12): two rows, filter byte 2 each.
	pngRaw := []byte{2, 1, 2, 2, 10, 10}
	got, err = decodeStreamPredicted(streamDict(pngRaw, map[string]PDFValue{
		"Predictor": PDFInteger(12), "Columns": PDFInteger(2),
	}))
	if err != nil {
		t.Fatalf("PNG predictor: %v", err)
	}
	if want := []byte{1, 2, 11, 12}; !bytes.Equal(got, want) {
		t.Errorf("PNG predictor = %v, want %v", got, want)
	}

	// TIFF predictor (2).
	got, err = decodeStreamPredicted(streamDict([]byte{1, 2, 3}, map[string]PDFValue{
		"Predictor": PDFInteger(2), "Columns": PDFInteger(3),
	}))
	if err != nil || !bytes.Equal(got, []byte{1, 3, 6}) {
		t.Errorf("TIFF predictor = %v, %v; want [1 3 6]", got, err)
	}

	// Unsupported predictor value.
	if _, err := decodeStreamPredicted(streamDict([]byte("x"), map[string]PDFValue{
		"Predictor": PDFInteger(5),
	})); err == nil {
		t.Error("expected error for an unsupported predictor")
	}
}
