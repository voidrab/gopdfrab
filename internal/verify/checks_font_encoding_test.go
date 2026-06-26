package verify

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func TestGlyphNameToUnicode(t *testing.T) {
	cases := []struct {
		name string
		want uint16
		ok   bool
	}{
		{"space", 0x0020, true},
		{"A", 0x0041, true},
		{"fi", 0xFB01, true},
		{"fl", 0xFB02, true},
		{"Euro", 0x20AC, true},
		{"uni0041", 0x0041, true},
		{"uni20AC", 0x20AC, true},
		{"notexist", 0, false},
	}
	for _, c := range cases {
		got, ok := GlyphNameToUnicode(c.name)
		if ok != c.ok || got != c.want {
			t.Errorf("GlyphNameToUnicode(%q) = (%04X, %v), want (%04X, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestSimpleFontCodeToUnicodeWinAnsi(t *testing.T) {
	table := SimpleFontCodeToUnicode(pdf.PDFName{Value: "WinAnsiEncoding"})
	// code 65 = 'A' in WinAnsi
	if table[65] != 0x0041 {
		t.Errorf("WinAnsi code 65 = %04X, want 0041", table[65])
	}
	// code 0x80 = Euro sign in Windows-1252
	if table[0x80] != 0x20AC {
		t.Errorf("WinAnsi code 0x80 = %04X, want 20AC", table[0x80])
	}
}

// TestSimpleFontCodeToUnicodeDifferences verifies that /Differences entries
// override the base encoding, which is the path the verifier missed before.
func TestSimpleFontCodeToUnicodeDifferences(t *testing.T) {
	// Encoding: WinAnsiEncoding with code 32 remapped to "fi" (U+FB01)
	diffs := pdf.PDFArray{
		pdf.PDFInteger(32),
		pdf.PDFName{Value: "fi"},
	}
	enc := pdf.NewPDFDict()
	enc.Entries["Type"] = pdf.PDFName{Value: "Encoding"}
	enc.Entries["BaseEncoding"] = pdf.PDFName{Value: "WinAnsiEncoding"}
	enc.Entries["Differences"] = diffs

	table := SimpleFontCodeToUnicode(enc)

	// code 32 was remapped from U+0020 (space) to U+FB01 (fi)
	if table[32] != 0xFB01 {
		t.Errorf("code 32 with /Differences fi = %04X, want FB01", table[32])
	}
	// code 33 should still be WinAnsi 0x0021 (!)
	if table[33] != 0x0021 {
		t.Errorf("code 33 unchanged = %04X, want 0021", table[33])
	}
}

// TestSimpleFontCodeToUnicodeUnknownGlyphName verifies that an unrecognised
// glyph name in /Differences maps the code to 0 (conservatively skipped).
func TestSimpleFontCodeToUnicodeUnknownGlyphName(t *testing.T) {
	diffs := pdf.PDFArray{
		pdf.PDFInteger(65),
		pdf.PDFName{Value: ".unimappedglyph"},
	}
	enc := pdf.NewPDFDict()
	enc.Entries["BaseEncoding"] = pdf.PDFName{Value: "WinAnsiEncoding"}
	enc.Entries["Differences"] = diffs

	table := SimpleFontCodeToUnicode(enc)
	// Unknown glyph name must map to 0 so the check skips conservatively.
	if table[65] != 0 {
		t.Errorf("unknown glyph name code 65 = %04X, want 0000", table[65])
	}
}
