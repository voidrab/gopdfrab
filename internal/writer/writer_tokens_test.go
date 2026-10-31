package writer

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func scalarString(t *testing.T, v pdf.PDFValue) string {
	t.Helper()
	var buf bytes.Buffer
	if ok, err := writeScalar(&buf, v); !ok || err != nil {
		t.Fatalf("writeScalar(%#v) = %v, %v", v, ok, err)
	}
	return buf.String()
}

// TestWriteRealNeverEmitsNaNOrInf pins that a non-finite real, which PDF has
// no token for, is written as 0 rather than as the text "NaN" or "+Inf".
func TestWriteRealNeverEmitsNaNOrInf(t *testing.T) {
	for _, f := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got := scalarString(t, pdf.PDFReal(f)); got != "0" {
			t.Errorf("PDFReal(%v) wrote %q, want 0", f, got)
		}
	}
	if got := scalarString(t, pdf.PDFReal(0.1)); got != "0.1" {
		t.Errorf("PDFReal(0.1) wrote %q, want the float32-shortest 0.1", got)
	}
}

// TestWriteNameEscapesTokenBreakers pins name escaping: bytes that would end
// the token are written as #XX, while a lexer-sourced name, which can hold none
// of them but may hold raw #XX escapes, passes through unchanged.
func TestWriteNameEscapesTokenBreakers(t *testing.T) {
	cases := map[string]string{
		"Times Roman":  "/Times#20Roman",
		"a/b(c)<d>[e]": "/a#2Fb#28c#29#3Cd#3E#5Be#5D",
		"x%y\n{z}":     "/x#25y#0A#7Bz#7D",
		"A#20B":        "/A#20B",
		"Helvetica":    "/Helvetica",
		"":             "/",
	}
	for in, want := range cases {
		if got := scalarString(t, pdf.PDFName{Value: in}); got != want {
			t.Errorf("name %q wrote %q, want %q", in, got, want)
		}
	}
}

// TestWrittenNamesReparse writes a dict whose key and value names hold spaces
// and delimiters, and checks the output still tokenizes into the same dict.
func TestWrittenNamesReparse(t *testing.T) {
	wr := newPDFWriter(0)
	var buf bytes.Buffer
	entries := pdf.DictOf(map[string]pdf.PDFValue{
		"My Key": pdf.PDFName{Value: "Times Roman"},
		"Next":   pdf.PDFInteger(1),
	})
	if err := wr.writeDictEntries(&countingWriter{w: &buf}, entries); err != nil {
		t.Fatal(err)
	}
	l := pdf.NewLexerBytes(buf.Bytes(), 0)
	var names []string
	for tok := l.NextToken(); tok.Type != pdf.TokenEOF; tok = l.NextToken() {
		if tok.Type == pdf.TokenName {
			names = append(names, tok.Value)
		}
	}
	if got := strings.Join(names, ","); got != "My#20Key,Times#20Roman,Next" {
		t.Fatalf("reparsed names %q from %q", got, buf.String())
	}
}

// TestStreamLengthOverride pins that a stream's /Length is written from the
// bytes actually emitted, replacing a wrong value or adding one when absent,
// without modifying the source dict.
func TestStreamLengthOverride(t *testing.T) {
	for name, entries := range map[string]pdf.Dict{
		"wrong":  pdf.DictOf(map[string]pdf.PDFValue{"Length": pdf.PDFInteger(999), "Filter": pdf.PDFName{Value: "X"}}),
		"absent": pdf.DictOf(map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "X"}, "Z": pdf.PDFInteger(1)}),
	} {
		t.Run(name, func(t *testing.T) {
			before, _ := entries.Lookup("Length")
			obj := pdf.NewStreamDict(entries, []byte("hello"))
			var buf bytes.Buffer
			if err := newPDFWriter(0).writeIndirectObject(&countingWriter{w: &buf}, 1, obj); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(buf.String(), "/Length 5 ") || strings.Count(buf.String(), "/Length") != 1 {
				t.Errorf("output %q, want exactly one /Length 5", buf.String())
			}
			if after, _ := entries.Lookup("Length"); after != before {
				t.Errorf("source /Length changed from %v to %v", before, after)
			}
		})
	}
}
