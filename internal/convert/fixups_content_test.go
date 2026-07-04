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
			"../../tests/Isartor/PDFA-1b/6.2 Graphics/6.2.10 Content Streams/isartor-6-2-10-t01-fail-a.pdf",
			pdf.Checks.Colour.UndefinedOperator,
		},
		{
			"RenderingIntent",
			"../../tests/Isartor/PDFA-1b/6.2 Graphics/6.2.9 Rendering intents/isartor-6-2-9-t01-fail-a.pdf",
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
			"../../tests/Isartor/PDFA-1b/6.1 File structure/6.1.12 Implementation Limits/isartor-6-1-12-t01-fail-c.pdf",
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

// undefinedOpStream builds a minimal content stream carrying one undefined
// operator ("XX"), for exercising walkContentStreams' various dispatch
// branches: rewriteOperatorsAndLimits drops it, so a cleared stream proves
// that branch was actually reached and rewritten.
func undefinedOpStream() pdf.PDFDict {
	return pdf.PDFDict{Entries: map[string]pdf.PDFValue{}, HasStream: true, RawStream: []byte("q 1 0 0 RG XX 1 2 Q\n")}
}

// assertOperatorDropped decodes dict's content stream and fails the test if
// the undefined "XX" operator survived.
func assertOperatorDropped(t *testing.T, label string, dict pdf.PDFDict) {
	t.Helper()
	decoded, err := pdf.DecodeStream(dict)
	if err != nil {
		t.Fatalf("%s: DecodeStream: %v", label, err)
	}
	var sawXX bool
	pdf.NewContentScanner(decoded).Scan(func(op string, _ []pdf.PDFValue) {
		if op == "XX" {
			sawXX = true
		}
	})
	if sawXX {
		t.Errorf("%s: undefined operator survived: %q", label, decoded)
	}
}

// TestWalkContentStreamsDispatch exercises every content-bearing stream
// shape walkContentStreams dispatches to: an array-form Page /Contents, a
// tiling Pattern, a Form XObject, and a Type3 glyph's CharProcs stream --
// contentLimitsFixer's corpus-fixture tests only ever hit the single-dict
// Page /Contents case, leaving the rest unexercised.
func TestWalkContentStreamsDispatch(t *testing.T) {
	pattern := undefinedOpStream()
	pattern.Entries["PatternType"] = pdf.PDFInteger(1)

	form := undefinedOpStream()
	form.Entries["Subtype"] = pdf.PDFName{Value: "Form"}

	glyph := undefinedOpStream()
	charProcs := pdf.PDFDict{Entries: map[string]pdf.PDFValue{"g1": glyph}}
	type3Font := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Subtype":   pdf.PDFName{Value: "Type3"},
		"CharProcs": charProcs,
	}}

	pageStreamA := undefinedOpStream()
	pageStreamB := undefinedOpStream()
	page := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Type":     pdf.PDFName{Value: "Page"},
		"Contents": pdf.PDFArray{pageStreamA, pageStreamB},
		"Resources": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Pattern": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"P1": pattern}},
			"XObject": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Fm1": form}},
			"Font":    pdf.PDFDict{Entries: map[string]pdf.PDFValue{"T3": type3Font}},
		}},
	}}
	trailer := pdf.PDFDict{Entries: map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: map[string]pdf.PDFValue{
			"Pages": pdf.PDFDict{Entries: map[string]pdf.PDFValue{"Kids": pdf.PDFArray{page}}},
		}},
	}}

	if !walkContentStreams(&trailer, rewriteOperatorsAndLimits) {
		t.Fatalf("walkContentStreams reported no change across an array-Contents page, a Pattern, a Form, and a Type3 glyph")
	}

	gotPage := trailer.Entries["Root"].(pdf.PDFDict).Entries["Pages"].(pdf.PDFDict).Entries["Kids"].(pdf.PDFArray)[0].(pdf.PDFDict)
	contents := gotPage.Entries["Contents"].(pdf.PDFArray)
	assertOperatorDropped(t, "Page/Contents[0]", contents[0].(pdf.PDFDict))
	assertOperatorDropped(t, "Page/Contents[1]", contents[1].(pdf.PDFDict))

	resources := gotPage.Entries["Resources"].(pdf.PDFDict)
	gotPattern := resources.Entries["Pattern"].(pdf.PDFDict).Entries["P1"].(pdf.PDFDict)
	assertOperatorDropped(t, "Pattern", gotPattern)
	gotForm := resources.Entries["XObject"].(pdf.PDFDict).Entries["Fm1"].(pdf.PDFDict)
	assertOperatorDropped(t, "Form", gotForm)
	gotGlyph := resources.Entries["Font"].(pdf.PDFDict).Entries["T3"].(pdf.PDFDict).Entries["CharProcs"].(pdf.PDFDict).Entries["g1"].(pdf.PDFDict)
	assertOperatorDropped(t, "Type3 glyph", gotGlyph)
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
