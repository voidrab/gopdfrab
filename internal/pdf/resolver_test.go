package pdf

import "testing"

// TestParseReferenceDanglingScan covers parseReference's fallback: an object
// number absent from every xref section triggers one brute-force scan, after
// which a physically-present-but-unlisted object resolves successfully, and a
// truly nonexistent one resolves to null (nil, nil) per ISO 32000-1 7.3.10.
func TestParseReferenceDanglingScan(t *testing.T) {
	t.Run("found by brute-force scan", func(t *testing.T) {
		data := []byte("5 0 obj\n<< /Foo 1 >>\nendobj\n")
		r := &Reader{data: data, xrefTable: map[int]int64{}}
		v, err := r.parseReference(PDFRef{ObjNum: 5})
		if err != nil {
			t.Fatalf("parseReference: %v", err)
		}
		dict, ok := v.(PDFDict)
		if !ok || !EqualPDFValue(dict.Entries["Foo"], PDFInteger(1)) {
			t.Errorf("parseReference result = %#v, want dict with Foo=1", v)
		}
	})

	t.Run("truly missing resolves to null", func(t *testing.T) {
		r := &Reader{data: []byte("no objects here"), xrefTable: map[int]int64{}}
		v, err := r.parseReference(PDFRef{ObjNum: 99})
		if err != nil || v != nil {
			t.Errorf("parseReference = %v, %v; want nil, nil", v, err)
		}
	})
}

// TestParseClassicReferenceFileBacked covers the d.data == nil path, which
// reads the object through d.file instead of aliasing an in-memory slice.
func TestParseClassicReferenceFileBacked(t *testing.T) {
	data := []byte("5 0 obj\n<< /Foo 1 >>\nendobj\n")
	r := newTestFileReader(data)
	v, err := r.parseClassicReference(PDFRef{ObjNum: 5}, 0)
	if err != nil {
		t.Fatalf("parseClassicReference: %v", err)
	}
	dict, ok := v.(PDFDict)
	if !ok || !EqualPDFValue(dict.Entries["Foo"], PDFInteger(1)) {
		t.Errorf("parseClassicReference result = %#v, want dict with Foo=1", v)
	}
}
