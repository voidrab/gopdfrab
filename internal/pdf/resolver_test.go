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

// TestParseClassicReferenceTokenTypes covers parseClassicReference's
// top-level token-type dispatch for every non-dictionary object body.
func TestParseClassicReferenceTokenTypes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want PDFValue
	}{
		{"array", "[1 2]", PDFArray{PDFInteger(1), PDFInteger(2)}},
		{"integer", "42", PDFInteger(42)},
		{"real", "3.5", PDFReal(3.5)},
		{"boolean true", "true", PDFBoolean(true)},
		{"boolean false", "false", PDFBoolean(false)},
		{"name", "/Foo", PDFName{Value: "Foo"}},
		{"null keyword", "null", nil},
		{"non-null keyword", "someKeyword", PDFName{Value: "someKeyword"}},
		{"string", "(Hello)", PDFString{Value: "Hello"}},
		{"hex string", "<48656C6C6F>", PDFHexString{Value: "48656C6C6F"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte("1 0 obj\n" + tc.body + "\nendobj\n")
			r := &Reader{data: data, xrefTable: map[int]int64{1: 0}}
			got, err := r.ResolveReference(PDFRef{ObjNum: 1})
			if err != nil {
				t.Fatalf("ResolveReference: %v", err)
			}
			if !EqualPDFValue(got, tc.want) {
				t.Errorf("ResolveReference(%q) = %#v, want %#v", tc.body, got, tc.want)
			}
		})
	}

	t.Run("malformed real number error", func(t *testing.T) {
		// "1.2.3" scans as one numeric token containing a '.', so it's typed
		// TokenReal, but strconv.ParseFloat rejects it, exercising RealValue's
		// error branch.
		data := []byte("1 0 obj\n1.2.3\nendobj\n")
		r := &Reader{data: data, xrefTable: map[int]int64{1: 0}}
		if _, err := r.ResolveReference(PDFRef{ObjNum: 1}); err == nil {
			t.Error("expected error for a malformed real number")
		}
	})

	t.Run("unterminated array error", func(t *testing.T) {
		data := []byte("1 0 obj\n[1 2\nendobj\n")
		r := &Reader{data: data, xrefTable: map[int]int64{1: 0}}
		if _, err := r.ResolveReference(PDFRef{ObjNum: 1}); err == nil {
			t.Error("expected error for an unterminated array")
		}
	})
}

// TestResolveShallow covers both of resolveShallow's branches directly: a
// PDFRef is dereferenced via ResolveReference, anything else passes through
// unchanged. (resolvePath's own eager top-level ResolveObject call fully
// resolves the graph before this ever runs, so a raw PDFRef never reaches
// resolveShallow through that path in practice -- this is a direct unit test.)
func TestResolveShallow(t *testing.T) {
	d := &Reader{objCache: map[int]PDFValue{5: PDFInteger(9)}}
	got, err := d.resolveShallow(PDFRef{ObjNum: 5})
	if err != nil || got != PDFInteger(9) {
		t.Errorf("resolveShallow(ref) = %v, %v; want 9", got, err)
	}

	got2, err2 := d.resolveShallow(PDFInteger(3))
	if err2 != nil || got2 != PDFInteger(3) {
		t.Errorf("resolveShallow(non-ref) = %v, %v; want 3", got2, err2)
	}
}

// TestParseClassicReferenceDictBranches covers the file-backed Seek error,
// the parseDictionary error, the validateStream error, the leadingWS/preEOL
// framing diagnostics before "endobj", and the "neither stream nor endobj
// follows" default branch.
func TestParseClassicReferenceDictBranches(t *testing.T) {
	t.Run("file-backed seek error", func(t *testing.T) {
		r := newTestFileReader([]byte("data"))
		if _, err := r.parseClassicReference(PDFRef{ObjNum: 1}, -1); err == nil {
			t.Error("expected a seek error for a negative offset")
		}
	})

	t.Run("parseDictionary error", func(t *testing.T) {
		data := []byte("1 0 obj\n<< /A 1\nendobj\n") // unterminated dictionary
		d := &Reader{data: data, xrefTable: map[int]int64{1: 0}}
		if _, err := d.ResolveReference(PDFRef{ObjNum: 1}); err == nil {
			t.Error("expected error for an unterminated dictionary")
		}
	})

	t.Run("validateStream error", func(t *testing.T) {
		data := []byte("1 0 obj\n<< /Length /Bad >>\nstream\ndata\nendstream\nendobj\n")
		d := &Reader{data: data, xrefTable: map[int]int64{1: 0}}
		if _, err := d.ResolveReference(PDFRef{ObjNum: 1}); err == nil {
			t.Error("expected error from a non-integer stream /Length")
		}
	})

	t.Run("leadingWS and preEOL diagnostics before endobj", func(t *testing.T) {
		// The dict's last value must not be a plain integer: parseObject's
		// ref-lookahead for a bare integer pushes tokens back onto l.pushed,
		// which would skip the preEOL/leadingWS check entirely (see the
		// comment on that check in parseClassicReference).
		data := []byte("1 0 obj\n<< /A true >> \nendobj\n") // stray space before the EOL
		d := &Reader{data: data, xrefTable: map[int]int64{1: 0}}
		v, err := d.ResolveReference(PDFRef{ObjNum: 1})
		if err != nil {
			t.Fatalf("ResolveReference: %v", err)
		}
		if _, ok := v.(PDFDict); !ok {
			t.Fatalf("result = %#v, want PDFDict", v)
		}
		if len(d.parseDiagnostics) == 0 {
			t.Error("expected recorded framing diagnostics for the stray space before endobj")
		}
	})

	t.Run("neither stream nor endobj follows", func(t *testing.T) {
		data := []byte("1 0 obj\n<< /A 1 >>\nfoo\n")
		d := &Reader{data: data, xrefTable: map[int]int64{1: 0}}
		v, err := d.ResolveReference(PDFRef{ObjNum: 1})
		if err != nil {
			t.Fatalf("ResolveReference: %v", err)
		}
		dict, ok := v.(PDFDict)
		if !ok || !EqualPDFValue(dict.Entries["A"], PDFInteger(1)) {
			t.Errorf("result = %#v, want dict with A=1", v)
		}
	})
}
