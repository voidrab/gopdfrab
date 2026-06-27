package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestContentLimitsFixerClearsViolations runs contentLimitsFixer's targeted
// checks end-to-end (Convert) on real fixtures, confirming each one is gone
// after the full write+reverify round trip -- not just absent from the
// in-memory Fix() result.
func TestContentLimitsFixerClearsViolations(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		check pdf.Check
	}{
		{
			"UndefinedOperator",
			"../../tests/Isartor testsuite/PDFA-1b/6.2 Graphics/6.2.10 Content Streams/isartor-6-2-10-t01-fail-a.pdf",
			pdf.Checks.Colour.UndefinedOperator,
		},
		{
			"RenderingIntent",
			"../../tests/Isartor testsuite/PDFA-1b/6.2 Graphics/6.2.9 Rendering intents/isartor-6-2-9-t01-fail-a.pdf",
			pdf.Checks.Colour.RenderingIntent,
		},
		{
			"HexStringOddLength",
			"../../tests/veraPDF/PDF_A-1b/6.1 File structure/6.1.6 String objects/veraPDF test suite 6-1-6-t01-fail-a.pdf",
			pdf.Checks.Structure.HexStringOddLength,
		},
		{
			"HexStringInvalidChar",
			"../../tests/veraPDF/PDF_A-1b/6.1 File structure/6.1.6 String objects/veraPDF test suite 6-1-6-t01-fail-b.pdf",
			pdf.Checks.Structure.HexStringInvalidChar,
		},
		{
			"IntegerOutOfRange",
			"../../tests/Isartor testsuite/PDFA-1b/6.1 File structure/6.1.12 Implementation Limits/isartor-6-1-12-t01-fail-c.pdf",
			pdf.Checks.Structure.IntegerOutOfRange,
		},
		{
			"StringTooLong",
			"../../tests/veraPDF/PDF_A-1b/6.1 File structure/6.1.12 Implementation limits/veraPDF test suite 6-1-12-t03-fail-a.pdf",
			pdf.Checks.Structure.StringTooLong,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trailer, closeDoc := fixtureTrailer(t, tt.path)
			defer closeDoc()
			runFixerAndCheckIdempotent(t, contentLimitsFixer{}, &trailer)
			assertCheckClearedByWrite(t, trailer, tt.check)
		})
	}
}

// TestFixHexStringValue checks fixHexStringValue's two repairs in isolation:
// stripping non-hex characters and padding an odd digit count.
func TestFixHexStringValue(t *testing.T) {
	tests := []struct{ in, want string }{
		{"ABC", "ABC0"},      // odd length -> padded
		{"GHIJ", ""},         // all invalid chars dropped -> empty (valid: 0 is even)
		{"A1G2B3", "A12B30"}, // invalid char dropped, then odd -> padded
		{"AB", "AB"},         // already valid
	}
	for _, tt := range tests {
		if got := fixHexStringValue(tt.in); got != tt.want {
			t.Errorf("fixHexStringValue(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestRewriteContentStreamDictDropsUndefinedOperator confirms the rewriter
// drops an unrecognized operator (and its operands) while preserving the
// surrounding, valid operators untouched.
func TestRewriteContentStreamDictDropsUndefinedOperator(t *testing.T) {
	src := []byte("q 1 0 0 RG 0 0 100 100 re S XX 1 2 Q\n")
	dict := pdf.NewPDFDict()
	dict.HasStream = true
	dict.RawStream = src

	fixed, changed := rewriteContentStreamDict(dict, rewriteOperatorsAndLimits)
	if !changed {
		t.Fatalf("rewriteContentStreamDict reported no change for a stream with an undefined operator")
	}

	decoded, err := pdf.DecodeStream(fixed)
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	var ops []string
	pdf.NewContentScanner(decoded).Scan(func(op string, _ []pdf.PDFValue) {
		ops = append(ops, op)
	})
	for _, op := range ops {
		if op == "XX" {
			t.Errorf("undefined operator %q survived rewriting: %v", op, ops)
		}
	}
	want := []string{"q", "RG", "re", "S", "Q"}
	if len(ops) != len(want) {
		t.Fatalf("ops = %v, want %v", ops, want)
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Errorf("ops[%d] = %q, want %q (full: %v)", i, ops[i], want[i], ops)
		}
	}
}
