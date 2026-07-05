package pdf

import "testing"

// buildObjStmDict returns a minimal valid object-stream dict containing two
// objects: object 1 = true (boolean), object 2 = 42 (integer).
func buildObjStmDict() PDFDict {
	header := "1 0 2 5"
	region := "true 42"
	full := header + " " + region
	return PDFDict{
		HasStream: true,
		RawStream: []byte(full),
		Entries: map[string]PDFValue{
			"Type":  PDFName{Value: "ObjStm"},
			"N":     PDFInteger(2),
			"First": PDFInteger(len(header) + 1),
		},
	}
}

// TestDecodeObjStm covers the cache-hit path, every ResolveReference/format
// error branch, and a full successful decode with the synthetic _ref stamp.
func TestDecodeObjStm(t *testing.T) {
	t.Run("cache hit", func(t *testing.T) {
		d := &Reader{objStmCache: map[int][]objStmEntry{5: {{objNum: 1, value: PDFInteger(9)}}}}
		entries, err := d.decodeObjStm(5)
		if err != nil || len(entries) != 1 || entries[0].value != PDFInteger(9) {
			t.Errorf("decodeObjStm (cached) = %v, %v", entries, err)
		}
	})

	t.Run("ResolveReference error", func(t *testing.T) {
		d := &Reader{
			xrefTable: map[int]int64{5: 0},
			data:      []byte("5 0 obj\n[1 2\nendobj\n"), // malformed: unterminated array
		}
		if _, err := d.decodeObjStm(5); err == nil {
			t.Error("expected error when the container object fails to resolve")
		}
	})

	t.Run("not an object stream", func(t *testing.T) {
		d := &Reader{objCache: map[int]PDFValue{5: PDFInteger(1)}}
		if _, err := d.decodeObjStm(5); err == nil {
			t.Error("expected error when the container is not a dict")
		}

		d2 := &Reader{objCache: map[int]PDFValue{5: PDFDict{}}}
		if _, err := d2.decodeObjStm(5); err == nil {
			t.Error("expected error when the container dict has no stream")
		}
	})

	t.Run("decode failure", func(t *testing.T) {
		dict := PDFDict{
			HasStream: true, RawStream: []byte("x"),
			Entries: map[string]PDFValue{"Filter": PDFName{Value: "JPXDecode"}},
		}
		d := &Reader{objCache: map[int]PDFValue{5: dict}}
		if _, err := d.decodeObjStm(5); err == nil {
			t.Error("expected error decoding an unsupported filter")
		}
	})

	t.Run("missing N or First", func(t *testing.T) {
		dict := PDFDict{HasStream: true, RawStream: []byte("1 0 true")}
		d := &Reader{objCache: map[int]PDFValue{5: dict}}
		if _, err := d.decodeObjStm(5); err == nil {
			t.Error("expected error when /N or /First is missing")
		}
	})

	t.Run("malformed header", func(t *testing.T) {
		dict := PDFDict{
			HasStream: true, RawStream: []byte("X Y true"),
			Entries: map[string]PDFValue{"N": PDFInteger(1), "First": PDFInteger(4)},
		}
		d := &Reader{objCache: map[int]PDFValue{5: dict}}
		if _, err := d.decodeObjStm(5); err == nil {
			t.Error("expected error for a non-integer header pair")
		}
	})

	t.Run("out-of-range offset", func(t *testing.T) {
		dict := PDFDict{
			HasStream: true, RawStream: []byte("1 1000 true"),
			Entries: map[string]PDFValue{"N": PDFInteger(1), "First": PDFInteger(4)},
		}
		d := &Reader{objCache: map[int]PDFValue{5: dict}}
		if _, err := d.decodeObjStm(5); err == nil {
			t.Error("expected error for an out-of-range object offset")
		}
	})

	t.Run("malformed nested object", func(t *testing.T) {
		header := "1 0"
		region := "[1 2" // unterminated array
		dict := PDFDict{
			HasStream: true, RawStream: []byte(header + " " + region),
			Entries: map[string]PDFValue{"N": PDFInteger(1), "First": PDFInteger(len(header) + 1)},
		}
		d := &Reader{objCache: map[int]PDFValue{5: dict}}
		if _, err := d.decodeObjStm(5); err == nil {
			t.Error("expected error for a malformed nested object")
		}
	})

	t.Run("success", func(t *testing.T) {
		d := &Reader{objCache: map[int]PDFValue{5: buildObjStmDict()}}
		entries, err := d.decodeObjStm(5)
		if err != nil {
			t.Fatalf("decodeObjStm: %v", err)
		}
		if len(entries) != 2 || entries[0].objNum != 1 || entries[0].value != PDFBoolean(true) {
			t.Errorf("entries[0] = %+v, want {1 true}", entries[0])
		}
		if entries[1].objNum != 2 || entries[1].value != PDFInteger(42) {
			t.Errorf("entries[1] = %+v, want {2 42}", entries[1])
		}
		if len(d.objStmCache[5]) != 2 {
			t.Error("expected the result to be cached")
		}
	})

	t.Run("success with nested dict stamps _ref", func(t *testing.T) {
		header := "7 0"
		region := "<< /K 1 >>"
		dict := PDFDict{
			HasStream: true, RawStream: []byte(header + " " + region),
			Entries: map[string]PDFValue{"N": PDFInteger(1), "First": PDFInteger(len(header) + 1)},
		}
		d := &Reader{objCache: map[int]PDFValue{9: dict}}
		entries, err := d.decodeObjStm(9)
		if err != nil {
			t.Fatalf("decodeObjStm: %v", err)
		}
		got, ok := entries[0].value.(PDFDict)
		if !ok || !EqualPDFValue(got.Entries["_ref"], PDFRef{ObjNum: 7}) {
			t.Errorf("nested dict = %#v, want _ref stamped to 7", entries[0].value)
		}
	})
}

// TestResolveCompressedObject covers the decodeObjStm-error passthrough, the
// index-out-of-range error, and a successful lookup.
func TestResolveCompressedObject(t *testing.T) {
	t.Run("decodeObjStm error", func(t *testing.T) {
		d := &Reader{objCache: map[int]PDFValue{5: PDFInteger(1)}}
		if _, err := d.resolveCompressedObject(PDFRef{ObjNum: 1}, compressedXrefEntry{streamObjNum: 5}); err == nil {
			t.Error("expected propagated decodeObjStm error")
		}
	})

	t.Run("index out of range", func(t *testing.T) {
		d := &Reader{objCache: map[int]PDFValue{5: buildObjStmDict()}}
		if _, err := d.resolveCompressedObject(PDFRef{ObjNum: 1}, compressedXrefEntry{streamObjNum: 5, index: 99}); err == nil {
			t.Error("expected error for an out-of-range index")
		}
	})

	t.Run("success", func(t *testing.T) {
		d := &Reader{objCache: map[int]PDFValue{5: buildObjStmDict()}}
		got, err := d.resolveCompressedObject(PDFRef{ObjNum: 2}, compressedXrefEntry{streamObjNum: 5, index: 1})
		if err != nil || got != PDFInteger(42) {
			t.Errorf("resolveCompressedObject = %v, %v; want 42", got, err)
		}
	})
}
