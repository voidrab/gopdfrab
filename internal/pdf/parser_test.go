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
		if !EqualPDFValue(dict.Entries.Get("Root"), PDFRef{ObjNum: 1, GenNum: 0}) {
			t.Errorf("trailer Root = %v, want 1 0 R", dict.Entries.Get("Root"))
		}
		if r.xrefTable[1] != 10 {
			t.Errorf("xrefTable[1] = %d, want 10", r.xrefTable[1])
		}
		if _, ok := r.xrefTable[0]; ok {
			t.Error("free entry (object 0) should not be recorded")
		}
	})

	// CR, LF and CRLF are all end-of-line markers (ISO 32000-1 7.2.3). A
	// CR-terminated xref used to be wholly unparseable -- the reader swallowed
	// every following line into the first one -- which sent real documents
	// (a dvips-produced arXiv paper) down the brute-force recovery path and,
	// when their trailer carried no /Root, left the graph unresolvable.
	for _, eol := range []struct{ name, sep string }{
		{"LF", "\n"}, {"CRLF", "\r\n"}, {"CR", "\r"},
	} {
		t.Run("line endings "+eol.name, func(t *testing.T) {
			data := []byte("xref" + eol.sep + "0 2" + eol.sep +
				"0000000000 65535 f \n0000000010 00000 n \n" +
				"trailer" + eol.sep + "<< /Size 2 /Root 1 0 R >>" + eol.sep)
			for _, tc := range []struct {
				name string
				r    *Reader
			}{
				{"bytes", &Reader{data: data, xrefTable: map[int]int64{}}},
				{"seek", newTestFileReader(data)},
			} {
				dict, err := tc.r.ParseXRefSectionAt(0, false)
				if err != nil {
					t.Fatalf("%s path: ParseXRefSectionAt: %v", tc.name, err)
				}
				if !EqualPDFValue(dict.Entries.Get("Root"), PDFRef{ObjNum: 1, GenNum: 0}) {
					t.Errorf("%s path: trailer Root = %v, want 1 0 R", tc.name, dict.Entries.Get("Root"))
				}
				if tc.r.xrefTable[1] != 10 {
					t.Errorf("%s path: xrefTable[1] = %d, want 10", tc.name, tc.r.xrefTable[1])
				}
			}
		})
	}

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

	if dict.Entries.Get("A") != nil {
		t.Errorf("A (null) should be nil, got %#v", dict.Entries.Get("A"))
	}
	if !EqualPDFValue(dict.Entries.Get("B"), PDFName{Value: "keywordX"}) {
		t.Errorf("B = %#v, want PDFName(keywordX)", dict.Entries.Get("B"))
	}
	if dict.Entries.Get("C") != PDFBoolean(true) {
		t.Errorf("C = %#v, want true", dict.Entries.Get("C"))
	}
	if dict.Entries.Get("D") != PDFReal(1.5) {
		t.Errorf("D = %#v, want 1.5", dict.Entries.Get("D"))
	}
	if !EqualPDFValue(dict.Entries.Get("E"), PDFHexString{Value: "48656C6C6F"}) {
		t.Errorf("E = %#v, want hex string", dict.Entries.Get("E"))
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

// TestScalarDispatchAgreesAcrossParsers is the regression gate for the shared
// parseScalarToken: the two token dispatchers -- parseObject, for a value inside
// a containing object, and parseClassicAt, for an indirect object's body -- must
// agree on every scalar. They were separate copies once and silently diverged on
// scalars and null.
func TestScalarDispatchAgreesAcrossParsers(t *testing.T) {
	cases := []struct {
		name    string
		literal string
	}{
		{"null", "null"},
		{"true", "true"},
		{"false", "false"},
		{"name", "/Foo"},
		{"literal string", "(hi there)"},
		{"hex string", "<48656C6C6F>"},
		{"real", "3.5"},
		{"negative real", "-0.25"},
		{"integer", "42"},
		{"bare keyword", "wibble"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := NewLexerBytes([]byte(c.literal), 0)
			want, wantErr := parseObject(l, l.NextToken())

			d := &Reader{data: []byte("1 0 obj " + c.literal + " endobj")}
			got, gotErr := d.parseClassicAt(PDFRef{ObjNum: 1}, 0)

			if (wantErr == nil) != (gotErr == nil) {
				t.Fatalf("errors differ: parseObject=%v, object body=%v", wantErr, gotErr)
			}
			if !EqualPDFValue(got, want) {
				t.Errorf("object body = %#v, parseObject = %#v", got, want)
			}
		})
	}
}

// TestObjectBodyIntegerIsNotAReference pins the one legitimate difference
// between the two dispatchers: "1 0 R" inside a containing object is a
// reference, but as an object's whole body it is just the integer 1 -- an
// object body is a value, not a reference chain.
func TestObjectBodyIntegerIsNotAReference(t *testing.T) {
	l := NewLexerBytes([]byte("1 0 R"), 0)
	inside, err := parseObject(l, l.NextToken())
	if err != nil {
		t.Fatalf("parseObject: %v", err)
	}
	if ref, ok := inside.(PDFRef); !ok || ref.ObjNum != 1 {
		t.Fatalf("inside an object, 1 0 R = %#v, want PDFRef{1,0}", inside)
	}

	d := &Reader{data: []byte("1 0 obj 1 0 R endobj")}
	body, err := d.parseClassicAt(PDFRef{ObjNum: 1}, 0)
	if err != nil {
		t.Fatalf("parseClassicAt: %v", err)
	}
	if body != PDFInteger(1) {
		t.Errorf("as an object body, 1 0 R = %#v, want PDFInteger(1)", body)
	}
}
