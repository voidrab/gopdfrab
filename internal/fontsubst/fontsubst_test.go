package fontsubst

import (
	"bytes"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestPick covers Pick's serif/fixed-pitch/
// italic/bold detection, both via FontDescriptor /Flags and /FontWeight and
// via BaseFont name heuristics, and checks the bundled face it returns.
func TestPick(t *testing.T) {
	flagsDict := func(flags int) pdf.PDFDict {
		return pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"Flags": pdf.PDFInteger(flags)})}
	}
	empty := pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{})}

	cases := []struct {
		name                            string
		desc                            pdf.PDFDict
		baseFont                        string
		serif, fixedPitch, italic, bold bool
		data                            []byte
	}{
		{"plain", empty, "Arial", false, false, false, false, LiberationSansRegular},
		{"serif flag", flagsDict(0x2), "Foo", true, false, false, false, libSerifRegular},
		{"serif name", empty, "Times New Roman", true, false, false, false, libSerifRegular},
		{"fixed flag", flagsDict(0x1), "Foo", false, true, false, false, libMonoRegular},
		{"fixed name", empty, "Courier", false, true, false, false, libMonoRegular},
		{"italic flag", flagsDict(0x40), "Foo", false, false, true, false, libSansItalic},
		{"italic name", empty, "Foo-Italic", false, false, true, false, libSansItalic},
		{"bold flag", flagsDict(0x40000), "Foo", false, false, false, true, libSansBold},
		{"bold name", empty, "Foo-Bold", false, false, false, true, libSansBold},
		{"bold weight", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"FontWeight": pdf.PDFInteger(700)})}, "Foo", false, false, false, true, libSansBold},
		{"sans bold italic", empty, "Arial-BoldItalic", false, false, true, true, libSansBoldItalic},
		{"serif bold italic", flagsDict(0x2 | 0x40 | 0x40000), "Foo", true, false, true, true, libSerifBoldItalic},
		{"serif italic", empty, "Georgia-Italic", true, false, true, false, libSerifItalic},
		{"serif bold", empty, "Garamond-Bold", true, false, false, true, libSerifBold},
		{"mono bold italic", empty, "Consolas-BoldOblique", false, true, true, true, libMonoBoldItalic},
		{"mono italic", empty, "Courier-Oblique", false, true, true, false, libMonoItalic},
		{"mono bold", empty, "Courier-Bold", false, true, false, true, libMonoBold},
		{"fixed beats serif", flagsDict(0x1 | 0x2), "Foo", true, true, false, false, libMonoRegular},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Pick(c.desc, c.baseFont)
			if got.Serif != c.serif || got.FixedPitch != c.fixedPitch || got.Italic != c.italic || got.Bold != c.bold {
				t.Errorf("Pick(%v, %q) = %+v, want serif=%v fixedPitch=%v italic=%v bold=%v",
					c.desc.Entries, c.baseFont, got, c.serif, c.fixedPitch, c.italic, c.bold)
			}
			if !bytes.Equal(got.Data, c.data) {
				t.Errorf("Pick(%v, %q) returned the wrong bundled face", c.desc.Entries, c.baseFont)
			}
		})
	}
}
