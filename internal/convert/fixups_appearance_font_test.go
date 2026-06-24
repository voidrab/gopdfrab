package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// TestAppearanceFontIsConformant runs the bundled appearance font through
// validateFontDict (checks_font.go) the same way the generic graph walk
// does for any font reachable from an AP stream's /Resources, and asserts
// none of the simple-TrueType checks fire. This guards the conversion
// plan's core claim for this font: a non-subset BaseFont skips
// SubsetGlyphCoverage, and Widths built from the embedded hmtx table can
// never disagree with AdvanceWidthMismatch's own hmtx read.
func TestAppearanceFontIsConformant(t *testing.T) {
	font := appearanceFont()
	ctx := &verify.ValidationContext{}
	verify.ValidateFontDict(font, ctx)

	disallowed := map[pdf.Check]bool{
		pdf.Checks.Font.SimpleNotEmbedded:        true,
		pdf.Checks.Font.InvalidProgram:           true,
		pdf.Checks.Font.TrueTypeEncoding:         true,
		pdf.Checks.Font.SymbolicTrueTypeEncoding: true,
		pdf.Checks.Font.SymbolicTrueTypeCmap:     true,
		pdf.Checks.Font.SubsetGlyphCoverage:      true,
		pdf.Checks.Font.AdvanceWidthMismatch:     true,
	}
	for _, err := range ctx.Issues() {
		if disallowed[err.Check()] {
			t.Errorf("appearanceFont() failed %s: %v", err.Check().Name(), err)
		}
	}
}

// TestAppearanceFontIsCachedAndShared checks that appearanceFont() returns
// the same underlying Entries map on every call, which is what lets the
// writer's identity-based dedup (writer.go) coalesce every reference into a
// single embedded font object instead of duplicating the font program once
// per widget.
func TestAppearanceFontIsCachedAndShared(t *testing.T) {
	a := appearanceFont()
	b := appearanceFont()
	if pdf.ValuePointer(a.Entries) != pdf.ValuePointer(b.Entries) {
		t.Errorf("appearanceFont() returned distinct Entries maps across calls, want the same shared instance")
	}
}

// TestAppearanceFontWidthsMatchHmtx spot-checks a few WinAnsi codes' Widths
// entries against the embedded font's own hmtx table, the same comparison
// validateSimpleTrueTypeMetrics performs, to catch a regression in the
// Widths-building loop directly rather than only via the absence of a
// reported error.
func TestAppearanceFontWidthsMatchHmtx(t *testing.T) {
	font := appearanceFont()
	desc := font.Entries["FontDescriptor"].(pdf.PDFDict)
	ff := desc.Entries["FontFile2"].(pdf.PDFDict)
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		t.Fatalf("pdf.DecodeStream(FontFile2): %v", err)
	}
	tables, ok := verify.ParseSfnt(data)
	if !ok {
		t.Fatalf("embedded FontFile2 did not parse as sfnt")
	}
	gidMap := verify.ParseCmapFormat4(verify.TTWindowsBMPCmap(tables))

	widths := font.Entries["Widths"].(pdf.PDFArray)
	firstChar := int(font.Entries["FirstChar"].(pdf.PDFInteger))
	for _, cc := range []int{'A', 'a', ' ', '0', 'W'} {
		gid, ok := gidMap[verify.WinAnsiToUnicode[cc]]
		if !ok {
			t.Fatalf("code %d has no glyph in the embedded font's cmap", cc)
		}
		want := verify.TTAdvanceWidth(tables, int(gid))
		got := int(widths[cc-firstChar].(pdf.PDFInteger))
		if got != want {
			t.Errorf("Widths[%d] (code %d) = %d, want %d (hmtx)", cc-firstChar, cc, got, want)
		}
	}
}
