package verify

import (
	"os"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestCFFAdvanceWidthsMatchDeclaredWidths cross-checks the Type2 charstring
// width reader against a real-world document whose /Widths veraPDF confirms
// consistent: every resolvable glyph's extracted width must agree with the
// declared one.
func TestCFFAdvanceWidthsMatchDeclaredWidths(t *testing.T) {
	path := "../../tests/regression/150911-LF-Energieeffizienz-in-RZ.pdf"
	if _, err := os.Stat(path); err != nil {
		t.Skip("regression fixture not present")
	}
	doc, err := pdf.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	graph, err := doc.ResolveGraph()
	if err != nil {
		t.Fatal(err)
	}
	trailer := graph.(pdf.PDFDict)

	fontsChecked, widthsChecked := 0, 0
	seen := map[uintptr]bool{}
	var walk func(v pdf.PDFValue)
	walk = func(v pdf.PDFValue) {
		switch val := v.(type) {
		case pdf.PDFDict:
			ptr := pdf.ValuePointer(val.Entries)
			if seen[ptr] {
				return
			}
			seen[ptr] = true
			if (val.Entries["Type"] == pdf.PDFName{Value: "Font"}) {
				if n := checkFontCFFWidths(t, val); n > 0 {
					fontsChecked++
					widthsChecked += n
				}
			}
			for k, c := range val.Entries {
				if k != "_ref" {
					walk(c)
				}
			}
		case pdf.PDFArray:
			for _, c := range val {
				walk(c)
			}
		}
	}
	walk(trailer)

	if fontsChecked < 5 || widthsChecked < 100 {
		t.Fatalf("checked %d fonts / %d widths, expected at least 5 / 100 -- fixture or parser regressed", fontsChecked, widthsChecked)
	}
}

func checkFontCFFWidths(t *testing.T, font pdf.PDFDict) (checked int) {
	t.Helper()
	desc, ok := font.Entries["FontDescriptor"].(pdf.PDFDict)
	if !ok {
		return 0
	}
	ff, ok := desc.Entries["FontFile3"].(pdf.PDFDict)
	if !ok {
		return 0
	}
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		return 0
	}
	glyphWidths := CFFAdvanceWidths(data)
	if len(glyphWidths) == 0 {
		return 0
	}
	glyphNames, ok := SimpleFontGlyphNameTable(font)
	if !ok {
		return 0
	}
	firstChar, _ := font.Entries["FirstChar"].(pdf.PDFInteger)
	widths, _ := font.Entries["Widths"].(pdf.PDFArray)
	baseFont, _ := font.Entries["BaseFont"].(pdf.PDFName)

	for i, w := range widths {
		pdfWidth, ok := pdf.PDFNumberToInt(w)
		if !ok || pdfWidth == 0 {
			continue
		}
		cc := int(firstChar) + i
		if cc < 0 || cc > 255 || glyphNames[cc] == "" {
			continue
		}
		csWidth, found := glyphWidths[glyphNames[cc]]
		if !found {
			continue
		}
		if pdf.AbsInt(pdfWidth-csWidth) > 1 {
			t.Errorf("%s code %d (/%s): declared width %d, charstring width %d", baseFont.Value, cc, glyphNames[cc], pdfWidth, csWidth)
		}
		checked++
	}
	return checked
}
