package pdf

import (
	"bytes"
	"strings"
	"testing"
)

// newTestFileReader builds a bare Reader over in-memory bytes, bypassing
// Open/OpenBytes's structure parsing so ParseXRefSectionAt can be driven
// directly against hand-crafted xref sections at a known offset.
func newTestFileReader(data []byte) *Reader {
	return &Reader{file: bytesFileSource{bytes.NewReader(data)}, xrefTable: map[int]int64{}}
}

// TestParseXRefSectionAt covers a valid classic table+trailer, a bad (past
// EOF) offset, a missing "xref" keyword, and a missing "trailer" keyword.
func TestParseXRefSectionAt(t *testing.T) {
	t.Run("valid table and trailer", func(t *testing.T) {
		data := []byte("xref\n0 2\n0000000000 65535 f \n0000000010 00000 n \ntrailer\n<< /Size 2 /Root 1 0 R >>\n")
		r := newTestFileReader(data)
		dict, err := r.ParseXRefSectionAt(0, false)
		if err != nil {
			t.Fatalf("ParseXRefSectionAt: %v", err)
		}
		if !EqualPDFValue(dict.Entries["Root"], PDFRef{ObjNum: 1, GenNum: 0}) {
			t.Errorf("trailer Root = %v, want 1 0 R", dict.Entries["Root"])
		}
		if r.xrefTable[1] != 10 {
			t.Errorf("xrefTable[1] = %d, want 10", r.xrefTable[1])
		}
		if _, ok := r.xrefTable[0]; ok {
			t.Error("free entry (object 0) should not be recorded")
		}
	})

	t.Run("bad offset past EOF", func(t *testing.T) {
		data := []byte("xref\n0 1\n0000000000 65535 f \ntrailer\n<< /Size 1 >>\n")
		r := newTestFileReader(data)
		if _, err := r.ParseXRefSectionAt(int64(len(data)+100), false); err == nil {
			t.Error("expected error reading past EOF")
		}
	})

	t.Run("missing xref keyword", func(t *testing.T) {
		data := []byte("notxref\n0 1\n0000000000 65535 f \ntrailer\n<< /Size 1 >>\n")
		r := newTestFileReader(data)
		_, err := r.ParseXRefSectionAt(0, false)
		if err == nil || !strings.Contains(err.Error(), "expected 'xref'") {
			t.Errorf("err = %v, want mention of missing 'xref'", err)
		}
	})

	t.Run("missing trailer keyword", func(t *testing.T) {
		data := []byte("xref\n0 1\n0000000000 65535 f \nNOTRAILER\n")
		r := newTestFileReader(data)
		_, err := r.ParseXRefSectionAt(0, false)
		if err == nil || !strings.Contains(err.Error(), "expected 'trailer'") {
			t.Errorf("err = %v, want mention of missing 'trailer'", err)
		}
	})

	t.Run("fillIn true skips, false overwrites", func(t *testing.T) {
		data := []byte("xref\n0 2\n0000000000 65535 f \n0000000123 00000 n \ntrailer\n<< /Size 2 /Root 1 0 R >>\n")
		r := newTestFileReader(data)
		r.xrefTable = map[int]int64{1: 999} // pre-existing "newer revision" entry

		if _, err := r.ParseXRefSectionAt(0, true); err != nil {
			t.Fatalf("ParseXRefSectionAt (fillIn): %v", err)
		}
		if r.xrefTable[1] != 999 {
			t.Errorf("fillIn=true overwrote existing entry: xrefTable[1] = %d, want 999", r.xrefTable[1])
		}

		if _, err := r.ParseXRefSectionAt(0, false); err != nil {
			t.Fatalf("ParseXRefSectionAt (no fillIn): %v", err)
		}
		if r.xrefTable[1] != 123 {
			t.Errorf("fillIn=false did not overwrite: xrefTable[1] = %d, want 123", r.xrefTable[1])
		}
	})

	t.Run("negative offset seek error", func(t *testing.T) {
		r := newTestFileReader([]byte("xref\n"))
		if _, err := r.ParseXRefSectionAt(-1, false); err == nil {
			t.Error("expected a seek error for a negative offset")
		}
	})

	t.Run("EOF immediately after entries, no trailer reachable", func(t *testing.T) {
		data := []byte("xref\n0 1\n0000000000 65535 f \n")
		r := newTestFileReader(data)
		if _, err := r.ParseXRefSectionAt(0, false); err == nil {
			t.Error("expected an error: no 'trailer' keyword ever found")
		}
	})

	t.Run("truncated entry line: best-effort break, then no trailer", func(t *testing.T) {
		data := []byte("xref\n0 1\n123\n")
		r := newTestFileReader(data)
		if _, err := r.ParseXRefSectionAt(0, false); err == nil {
			t.Error("expected an error: truncated entry then no 'trailer' keyword")
		}
	})
}

// TestParseXRefTable covers parseXRefTable's Seek error, a successful parse,
// and a truncated entry line (ReadFull error).
func TestParseXRefTable(t *testing.T) {
	t.Run("negative offset seek error", func(t *testing.T) {
		r := newTestFileReader([]byte("xref\n"))
		if err := r.parseXRefTable(-1); err == nil {
			t.Error("expected a seek error for a negative offset")
		}
	})

	t.Run("valid table", func(t *testing.T) {
		data := []byte("xref\n0 2\n0000000000 65535 f \n0000000010 00000 n \ntrailer\n")
		r := newTestFileReader(data)
		if err := r.parseXRefTable(0); err != nil {
			t.Fatalf("parseXRefTable: %v", err)
		}
		if r.xrefTable[1] != 10 {
			t.Errorf("xrefTable[1] = %d, want 10", r.xrefTable[1])
		}
	})

	t.Run("truncated entry line", func(t *testing.T) {
		data := []byte("xref\n0 1\n0000000")
		r := newTestFileReader(data)
		if err := r.parseXRefTable(0); err == nil {
			t.Error("expected an error reading a truncated 20-byte entry line")
		}
	})

	t.Run("EOF immediately after entries", func(t *testing.T) {
		data := []byte("xref\n0 1\n0000000000 65535 f \n")
		r := newTestFileReader(data)
		if err := r.parseXRefTable(0); err == nil {
			t.Error("expected an error: EOF while peeking for the next subsection or 't'")
		}
	})
}

// TestParseObjectValueTypes covers parseObject's dispatch for keyword
// (null and non-null), boolean, real, and hex-string tokens, via
// parseDictionary (which invokes parseObject for each value).
func TestParseObjectValueTypes(t *testing.T) {
	src := "/A null /B keywordX /C true /D 1.5 /E <48656C6C6F> >>"
	l := NewLexer(bytes.NewReader([]byte(src)))
	defer l.Release()

	dict, err := parseDictionary(l)
	if err != nil {
		t.Fatalf("parseDictionary: %v", err)
	}

	if dict.Entries["A"] != nil {
		t.Errorf("A (null) should be nil, got %#v", dict.Entries["A"])
	}
	if !EqualPDFValue(dict.Entries["B"], PDFName{Value: "keywordX"}) {
		t.Errorf("B = %#v, want PDFName(keywordX)", dict.Entries["B"])
	}
	if dict.Entries["C"] != PDFBoolean(true) {
		t.Errorf("C = %#v, want true", dict.Entries["C"])
	}
	if dict.Entries["D"] != PDFReal(1.5) {
		t.Errorf("D = %#v, want 1.5", dict.Entries["D"])
	}
	if !EqualPDFValue(dict.Entries["E"], PDFHexString{Value: "48656C6C6F"}) {
		t.Errorf("E = %#v, want hex string", dict.Entries["E"])
	}
}

// TestParseDictionaryUnexpectedEOF covers parseDictionary's EOF error branch.
func TestParseDictionaryUnexpectedEOF(t *testing.T) {
	l := NewLexer(bytes.NewReader([]byte("/A 1")))
	defer l.Release()
	if _, err := parseDictionary(l); err == nil {
		t.Error("expected error for a dictionary truncated before '>>'")
	}
}

// TestParseDictionaryKeyTypeMismatch covers the "expected dictionary key"
// error: a non-Name token where a key is expected.
func TestParseDictionaryKeyTypeMismatch(t *testing.T) {
	l := NewLexer(bytes.NewReader([]byte("1 >>")))
	defer l.Release()
	if _, err := parseDictionary(l); err == nil {
		t.Error("expected error for a non-name dictionary key")
	}
}

// TestParseDictionaryValueError covers parseDictionary's error-propagation
// branch: a malformed nested array value fails to parse and the failure
// bubbles up (also exercising parseArray's own error-propagation branch).
func TestParseDictionaryValueError(t *testing.T) {
	l := NewLexer(bytes.NewReader([]byte("/A [1 2 >>")))
	defer l.Release()
	if _, err := parseDictionary(l); err == nil {
		t.Error("expected error propagated from a malformed nested array value")
	}
}
