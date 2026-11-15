package raster

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestCompositeCFFMapsCIDsThroughTheCharset verifies that in a CID-keyed CFF
// program, as PDF producers embed for CIDFontType0 subsets, a CID selects the
// glyph the charset lists for it rather than the glyph with that index.
func TestCompositeCFFMapsCIDsThroughTheCharset(t *testing.T) {
	cff := buildMinimalCIDCFF()
	td, ok := fontprog.ParseCFFTopDict(cff)
	if !ok || !td.IsCIDKeyed {
		t.Fatal("the test program is not CID-keyed")
	}
	// The format-0 charset lists one CID per glyph from glyph 1: make them 7
	// and 3, so that no CID equals its glyph's index.
	cff[td.CharsetOffset+2] = 7
	cff[td.CharsetOffset+4] = 3

	desc := cidFont(nil, map[string]pdf.PDFValue{"FontFile3": streamOf(cff)}).Entries.Get("DescendantFonts").(pdf.PDFArray)[0].(pdf.PDFDict)
	fi := buildCompositeFontInfo(desc)
	for _, cid := range []int{0, 7, 3} {
		if _, ok := fi.glyphFor(cid); !ok {
			t.Errorf("CID %d is in the charset but has no glyph", cid)
		}
	}
	for _, cid := range []int{1, 2, 99} {
		if _, ok := fi.glyphFor(cid); ok {
			t.Errorf("CID %d is not in the charset but drew a glyph", cid)
		}
	}
}
