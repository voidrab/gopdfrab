package convert

import (
	"bytes"
	"strings"
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
			"RealOutOfRange",
			"../../tests/veraPDF/PDF_A-1b/6.1 File structure/6.1.12 Implementation limits/veraPDF test suite 6-1-12-t02-fail-c.pdf",
			pdf.Checks.Structure.RealOutOfRange,
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

// rescaled runs the path rescale over a content stream and returns what it
// wrote, or "" if it left the stream alone.
func rescaled(t *testing.T, src string) string {
	t.Helper()
	out, ok := rescalePathsAndRewrite([]byte(src), rewriteOperatorsAndLimits)
	if !ok {
		return ""
	}
	return string(out)
}

// TestRescalePathFoldsMagnitudeIntoCTM is the shape that prompted this repair,
// taken from a real presentation: a full-page clip drawn at 1/508 scale, whose
// corner is at 365760 units. Clamping that to 32767 kept the file conformant
// and threw away 98% of every page. Folding 16 into the CTM instead leaves
// every number inside the limit and the clip exactly where it was.
func TestRescalePathFoldsMagnitudeIntoCTM(t *testing.T) {
	got := rescaled(t, "q 0.001968504 0 0 0.001968504 0 0 cm\n"+
		"0 0 m 365760.0 0 l 365760.0 205740.0 l 0 205740.0 l 0 0 l h W n\n")
	want := "q\n" +
		"0.001968504 0 0 0.001968504 0 0 cm\n" +
		"16 0 0 16 0 0 cm\n" +
		"0 0 m\n22860 0 l\n22860 12858.75 l\n0 12858.75 l\n0 0 l\nh\nW\nn\n" +
		"0.0625 0 0 0.0625 0 0 cm\n"
	if got != want {
		t.Errorf("rescaled stream:\n%s\nwant:\n%s", got, want)
	}
}

// TestRescaleFactorIsAPowerOfTwo pins scaleFor at its boundaries. Powers of two
// are the point: dividing a coordinate by one only shifts its exponent, so
// nothing is rounded away and the matrix that undoes the scale undoes it
// exactly.
func TestRescaleFactorIsAPowerOfTwo(t *testing.T) {
	tests := []struct {
		largest float64
		want    float64
	}{
		{32767, 1},   // already inside the limit
		{32768, 2},   // one past it
		{365760, 16}, // the real-world case
		{maxScale * realLimit, maxScale},
		{maxScale*realLimit + 1, 0}, // past what a matrix can carry
	}
	for _, tt := range tests {
		if got := scaleFor(tt.largest); got != tt.want {
			t.Errorf("scaleFor(%g) = %g, want %g", tt.largest, got, tt.want)
		}
	}
}

// TestRescaleLeavesInRangePathsAlone: a path that breaks no limit must come
// out byte for byte the same, so the pass costs nothing on the vast majority
// of documents.
func TestRescaleLeavesInRangePathsAlone(t *testing.T) {
	if got := rescaled(t, "0 0 m 100 0 l 100 100 l h f\n"); got != "" {
		t.Errorf("an in-range path was rewritten:\n%s", got)
	}
}

// TestRescaleTooLargeForAMatrix: a coordinate no allowed matrix can carry is
// left for the clamp, which is still better than a wrong answer.
func TestRescaleTooLargeForAMatrix(t *testing.T) {
	got := rescaled(t, "0 0 m 999999999.0 0 l f\n")
	if strings.Contains(got, "cm") {
		t.Errorf("a coordinate past the matrix range was rescaled:\n%s", got)
	}
	if !strings.Contains(got, "32767") {
		t.Errorf("clamp did not take over:\n%s", got)
	}
}

// TestRescaleStrokeKeepsLineWidth: line width is a length in user space, so a
// stroke drawn under a scaled CTM would come out that many times thicker. The
// width goes down with the path and is put back after it.
func TestRescaleStrokeKeepsLineWidth(t *testing.T) {
	got := rescaled(t, "3 w 0 0 m 65536.0 0 l S\n")
	want := "3 w\n" +
		"4 0 0 4 0 0 cm\n" +
		"0.75 w\n" +
		"0 0 m\n16384 0 l\nS\n" +
		"0.25 0 0 0.25 0 0 cm\n" +
		"3 w\n"
	if got != want {
		t.Errorf("rescaled stroke:\n%s\nwant:\n%s", got, want)
	}
}

// TestRescaleStrokeKeepsDash: dash lengths are user-space too, and scale with
// the path exactly as the line width does. With no w in the stream the width
// restored is 1, the value a stream starts with -- restoring 0 would turn every
// unset stroke into a hairline.
func TestRescaleStrokeKeepsDash(t *testing.T) {
	got := rescaled(t, "[8 4] 0 d 0 0 m 65536.0 0 l S\n")
	want := "[8 4] 0 d\n" +
		"4 0 0 4 0 0 cm\n" +
		"0.25 w\n[2 1] 0 d\n" +
		"0 0 m\n16384 0 l\nS\n" +
		"0.25 0 0 0.25 0 0 cm\n" +
		"1 w\n[8 4] 0 d\n"
	if got != want {
		t.Errorf("rescaled dashed stroke:\n%s\nwant:\n%s", got, want)
	}
}

// TestRescaleStrokeAfterExtGState: an ExtGState can set the line width and the
// dash to values the stream never names, so there is nothing to put back. The
// path is left to the clamp rather than redrawn at a guessed thickness.
func TestRescaleStrokeAfterExtGState(t *testing.T) {
	got := rescaled(t, "/GS1 gs 0 0 m 65536.0 0 l S\n")
	if strings.Contains(got, "cm") {
		t.Errorf("a stroke was rescaled with the line width out of view:\n%s", got)
	}
	// A fill has no width to get wrong, so the same stream still rescales.
	filled := rescaled(t, "/GS1 gs 0 0 m 65536.0 0 l f\n")
	if !strings.Contains(filled, "4 0 0 4 0 0 cm") {
		t.Errorf("a fill after gs should still rescale:\n%s", filled)
	}
}

// TestRescaleTracksLineWidthThroughSave: q and Q save and restore the line
// width, so a width set inside a q is not the one put back outside it.
func TestRescaleTracksLineWidthThroughSave(t *testing.T) {
	got := rescaled(t, "3 w q 8 w Q 0 0 m 65536.0 0 l S\n")
	if !strings.Contains(got, "0.75 w") || !strings.HasSuffix(got, "3 w\n") {
		t.Errorf("width restored by Q was not the one used:\n%s", got)
	}
	// An ExtGState inside the q is undone by the Q as well, so the stroke can
	// still be rescaled afterwards.
	cleared := rescaled(t, "3 w q /GS1 gs Q 0 0 m 65536.0 0 l S\n")
	if !strings.Contains(cleared, "4 0 0 4 0 0 cm") {
		t.Errorf("Q should have restored a known line width:\n%s", cleared)
	}
}

// TestRescaleUnterminatedPath: a stream that ends mid-path, or puts an
// operator where one cannot go, still has to come out as valid content. The
// path is written back as it was scanned and the clamp handles it.
func TestRescaleUnterminatedPath(t *testing.T) {
	for _, src := range []string{
		"0 0 m 65536.0 0 l\n",         // ends without painting
		"0 0 m 65536.0 0 l BT ET f\n", // text object opened inside the path
	} {
		got := rescaled(t, src)
		if strings.Contains(got, "16384") {
			t.Errorf("a path that could not be closed was rescaled: %q ->\n%s", src, got)
		}
		if !strings.Contains(got, "32767") {
			t.Errorf("clamp did not take over: %q ->\n%s", src, got)
		}
	}
}

// TestRescaleLongPathSpills: a path too long to hold is written out as it is
// scanned, so a pathological stream cannot grow the heap without limit. It
// gives up the rescale to do it.
func TestRescaleLongPathSpills(t *testing.T) {
	var src strings.Builder
	src.WriteString("0 0 m\n")
	for range maxBufferedPathOps + 2 {
		src.WriteString("65536.0 0 l\n")
	}
	src.WriteString("f\n")
	got := rescaled(t, src.String())
	if strings.Contains(got, "cm") {
		t.Errorf("an oversized path was rescaled instead of spilled")
	}
	if !strings.Contains(got, "32767") {
		t.Errorf("clamp did not take over on a spilled path")
	}
}

// TestRescaleIgnoresNonNumericOperands: a malformed stream can put anything
// where a number belongs. Nothing that is not a number is scaled, and an
// operator that sets state with the wrong operand leaves that state alone.
func TestRescaleIgnoresNonNumericOperands(t *testing.T) {
	// A w whose operand is a name leaves the width at its default of 1.
	got := rescaled(t, "/bogus w 0 0 m 65536.0 0 l S\n")
	if !strings.Contains(got, "0.25 w") || !strings.HasSuffix(got, "1 w\n") {
		t.Errorf("a w with a non-numeric operand should not change the width:\n%s", got)
	}
	// A dash array holding a name keeps the name and scales the rest.
	dashed := rescaled(t, "[8 /x] 0 d 0 0 m 65536.0 0 l S\n")
	if !strings.Contains(dashed, "[2 /x] 0 d") {
		t.Errorf("a non-numeric dash entry should be left as it is:\n%s", dashed)
	}
}

// TestRescaleContentStreamDictUndecodable: a stream nothing can decode is left
// exactly as it is, rather than replaced with an empty one.
func TestRescaleContentStreamDictUndecodable(t *testing.T) {
	dict := pdf.NewPDFDict()
	dict.HasStream = true
	dict.RawStream = []byte("not flate data")
	dict.Entries.Set("Filter", pdf.PDFName{Value: "FlateDecode"})

	if _, changed := rescaleContentStreamDict(dict, rewriteOperatorsAndLimits); changed {
		t.Error("an undecodable stream was reported as rewritten")
	}
}

// undefinedOpStream builds a minimal content stream carrying one undefined
// operator ("XX"), for exercising walkContentStreams' various dispatch
// branches: rewriteOperatorsAndLimits drops it, so a cleared stream proves
// that branch was actually reached and rewritten.
func undefinedOpStream() pdf.PDFDict {
	return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{}), HasStream: true, RawStream: []byte("q 1 0 0 RG XX 1 2 Q\n")}
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
	pattern.Entries.Set("PatternType", pdf.PDFInteger(1))

	form := undefinedOpStream()
	form.Entries.Set("Subtype", pdf.PDFName{Value: "Form"})

	glyph := undefinedOpStream()
	charProcs := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"g1": glyph})}
	type3Font := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Subtype":   pdf.PDFName{Value: "Type3"},
		"CharProcs": charProcs,
	})}

	pageStreamA := undefinedOpStream()
	pageStreamB := undefinedOpStream()
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type":     pdf.PDFName{Value: "Page"},
		"Contents": pdf.PDFArray{pageStreamA, pageStreamB},
		"Resources": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Pattern": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"P1": pattern})},
			"XObject": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Fm1": form})},
			"Font":    pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"T3": type3Font})},
		})},
	})}
	trailer := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Root": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Pages": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Kids": pdf.PDFArray{page}})},
		})},
	})}

	if !walkContentStreams(&trailer, rewriteOperatorsAndLimits) {
		t.Fatalf("walkContentStreams reported no change across an array-Contents page, a Pattern, a Form, and a Type3 glyph")
	}

	gotPage := trailer.Entries.Get("Root").(pdf.PDFDict).Entries.Get("Pages").(pdf.PDFDict).Entries.Get("Kids").(pdf.PDFArray)[0].(pdf.PDFDict)
	contents := gotPage.Entries.Get("Contents").(pdf.PDFArray)
	assertOperatorDropped(t, "Page/Contents[0]", contents[0].(pdf.PDFDict))
	assertOperatorDropped(t, "Page/Contents[1]", contents[1].(pdf.PDFDict))

	resources := gotPage.Entries.Get("Resources").(pdf.PDFDict)
	gotPattern := resources.Entries.Get("Pattern").(pdf.PDFDict).Entries.Get("P1").(pdf.PDFDict)
	assertOperatorDropped(t, "Pattern", gotPattern)
	gotForm := resources.Entries.Get("XObject").(pdf.PDFDict).Entries.Get("Fm1").(pdf.PDFDict)
	assertOperatorDropped(t, "Form", gotForm)
	gotGlyph := resources.Entries.Get("Font").(pdf.PDFDict).Entries.Get("T3").(pdf.PDFDict).Entries.Get("CharProcs").(pdf.PDFDict).Entries.Get("g1").(pdf.PDFDict)
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

// TestRewriteKeepsAStreamItCouldNotReadWhole: a rewriter emits as it reads, so
// a stream it stops part way through has only been part written. Writing that
// back would throw away the rest of the drawing, so the original bytes stay.
func TestRewriteKeepsAStreamItCouldNotReadWhole(t *testing.T) {
	// An undefined operator to rewrite, then a brace, which is not a
	// content-stream token at all and stops the read.
	src := []byte("q XX 1 2\n{ }\n0 0 100 100 re f Q\n")
	dict := pdf.NewPDFDict()
	dict.HasStream = true
	dict.RawStream = src

	if _, changed := rewriteContentStreamDict(dict, rewriteOperatorsAndLimits); changed {
		t.Error("a half-read stream was rewritten, want the original kept")
	}
	if _, changed := repairContentStream(dict); changed {
		t.Error("a half-read stream was rescaled, want the original kept")
	}
	if !bytes.Equal(dict.RawStream, src) {
		t.Errorf("stream = %q, want it untouched", dict.RawStream)
	}
}
