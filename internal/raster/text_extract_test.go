package raster

import (
	"context"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// extract returns the glyphs of a 100x50 page whose content is content, with
// font as /F1.
func extract(t *testing.T, content string, font pdf.PDFDict) []TextGlyph {
	t.Helper()
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte(content)),
	})}
	glyphs, err := ExtractText(context.Background(), page, fontResources(font), [4]float64{0, 0, 100, 50}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return glyphs
}

func TestExtractTextGivesRunesAndBoxes(t *testing.T) {
	// 20 pt text with a baseline 30 up from the bottom of a 50 pt page: 20 down
	// from the top. Every glyph is 500 units, 10 pt, wide.
	got := extract(t, "BT /F1 20 Tf 10 30 Td (Hi) Tj ET", helvetica(nil))
	want := []TextGlyph{
		{R: 'H', Box: [4]float64{10, 4, 20, 24}},
		{R: 'i', Box: [4]float64{20, 4, 30, 24}},
	}
	if len(got) != len(want) {
		t.Fatalf("%d glyphs, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].R != want[i].R {
			t.Errorf("glyph %d is %q, want %q", i, got[i].R, want[i].R)
		}
		for j := range 4 {
			if d := got[i].Box[j] - want[i].Box[j]; d < -0.01 || d > 0.01 {
				t.Errorf("glyph %d box %v, want %v", i, got[i].Box, want[i].Box)
				break
			}
		}
	}
}

func TestExtractTextFollowsPositioningAndState(t *testing.T) {
	// A TJ number moves the next glyph, character spacing and a rise shift it,
	// and invisible (render mode 3) text is text too.
	got := extract(t, "BT /F1 10 Tf 0 0 Td [(a) -1000 (b)] TJ 2 Tc 5 Ts (c) Tj 3 Tr (d) Tj ET", helvetica(nil))
	if len(got) != 4 {
		t.Fatalf("%d glyphs: %+v", len(got), got)
	}
	// a is 5 wide; the TJ number 1000 thousandths of 10 pt moves b 10 on.
	if a, b := got[0].Box[0], got[1].Box[0]; b-a != 15 {
		t.Errorf("b starts %v after a, want 15", b-a)
	}
	// Character spacing is added after each glyph shown once it is set, so d
	// follows c by c's width and 2 pt more, and c follows b by b's width alone.
	if c, b := got[2].Box[0], got[1].Box[0]; c-b != 5 {
		t.Errorf("c starts %v after b, want 5", c-b)
	}
	if d, c := got[3].Box[0], got[2].Box[0]; d-c != 5+2 {
		t.Errorf("d starts %v after c, want 7", d-c)
	}
	// The rise lifts c's box by 5.
	if got[2].Box[1] != got[1].Box[1]-5 {
		t.Errorf("c's box top is %v, b's %v: want 5 higher", got[2].Box[1], got[1].Box[1])
	}
	if got[3].R != 'd' {
		t.Errorf("invisible text came out as %q", got[3].R)
	}
}

func TestExtractTextSkipsImagesAndReadsFormText(t *testing.T) {
	// Text inside a Form XObject is extracted. An image is not decoded, so a
	// broken image does not cause an error.
	form := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{
		"Subtype": pdf.PDFName{Value: "Form"}, "BBox": numArray(0, 0, 100, 50),
	}), []byte("BT /F1 10 Tf 0 0 Td (x) Tj ET"))
	broken := imageDict(map[string]pdf.PDFValue{"Width": pdf.PDFInteger(0), "Height": pdf.PDFInteger(0)}, nil)
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte("/Im Do /Fm Do")),
	})}
	res := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Font":    pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"F1": helvetica(nil)})},
		"XObject": pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Im": broken, "Fm": form})},
	})}
	got, err := ExtractText(context.Background(), page, res, [4]float64{0, 0, 100, 50}, nil, nil)
	if err != nil || len(got) != 1 || got[0].R != 'x' {
		t.Errorf("glyphs %+v, err %v", got, err)
	}
}

func TestExtractTextMapsThroughToUnicodeAndEncodings(t *testing.T) {
	cmap := pdf.NewStreamDict(nil, []byte("1 beginbfchar <48> <0058> endbfchar 1 beginbfrange <69> <6A> <0041> endbfrange"))
	got := extract(t, "BT /F1 10 Tf (Hij) Tj ET", helvetica(map[string]pdf.PDFValue{"ToUnicode": cmap}))
	if len(got) != 3 || got[0].R != 'X' || got[1].R != 'A' || got[2].R != 'B' {
		t.Errorf("through /ToUnicode: %+v", got)
	}
	// Without a CMap the encoding decides: WinAnsi 0x80 is the euro sign.
	got = extract(t, "BT /F1 10 Tf <80> Tj ET", helvetica(nil))
	if len(got) != 1 || got[0].R != '€' {
		t.Errorf("through the encoding: %+v", got)
	}
	// A composite font without a CMap does not say what its codes mean.
	composite := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Type": pdf.PDFName{Value: "Font"}, "Subtype": pdf.PDFName{Value: "Type0"},
		"BaseFont": pdf.PDFName{Value: "X"}, "Encoding": pdf.PDFName{Value: "Identity-H"},
		"DescendantFonts": pdf.PDFArray{pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
			"Subtype": pdf.PDFName{Value: "CIDFontType2"}, "DW": pdf.PDFInteger(1000),
		})}},
	})}
	got = extract(t, "BT /F1 10 Tf <00480049> Tj ET", composite)
	if len(got) != 2 || got[0].R != 0 || got[1].R != 0 || got[1].Box[0]-got[0].Box[0] != 10 {
		t.Errorf("composite font without /ToUnicode: %+v", got)
	}
	// An unusable CMap falls back to the encoding.
	bad := pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "LZWDecode"}}), []byte{0xFF, 0xFF, 0xFF})
	got = extract(t, "BT /F1 10 Tf (H) Tj ET", helvetica(map[string]pdf.PDFValue{"ToUnicode": bad}))
	if len(got) != 1 || got[0].R != 'H' {
		t.Errorf("with an unreadable /ToUnicode: %+v", got)
	}
}

func TestExtractTextRecordsType3Glyphs(t *testing.T) {
	// A Type 3 glyph 1000 glyph-space units wide under a 0.001 FontMatrix is 1 em.
	got := extract(t, "BT /F1 10 Tf 0 0 Td (AA) Tj ET", type3Font("1000 0 d0 0 0 1000 1000 re f", 0.001, 1000, "square"))
	near := func(a, b float64) bool { return a-b < 1e-3 && b-a < 1e-3 }
	if len(got) != 2 || !near(got[0].Box[2]-got[0].Box[0], 10) || !near(got[1].Box[0], 10) {
		t.Errorf("Type 3 glyphs: %+v", got)
	}
}

func TestExtractTextReportsErrors(t *testing.T) {
	bad := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(pdf.DictOf(map[string]pdf.PDFValue{"Filter": pdf.PDFName{Value: "LZWDecode"}}), []byte{0xFF, 0xFF, 0xFF}),
	})}
	if _, err := ExtractText(context.Background(), bad, pdf.PDFDict{}, [4]float64{0, 0, 10, 10}, nil, nil); err == nil {
		t.Error("undecodable contents were accepted")
	}
	page := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{
		"Contents": pdf.NewStreamDict(nil, []byte("BT /F1 10 Tf (a) Tj ET")),
	})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ExtractText(ctx, page, fontResources(helvetica(nil)), [4]float64{0, 0, 10, 10}, nil, nil); err != context.Canceled {
		t.Errorf("a cancelled extraction returned %v", err)
	}
}
