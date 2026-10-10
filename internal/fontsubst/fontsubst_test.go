package fontsubst

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// TestPick covers Pick's serif/fixed-pitch/
// italic/bold detection, both via FontDescriptor /Flags and /FontWeight and
// via BaseFont name heuristics.
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
	}{
		{"plain", empty, "Arial", false, false, false, false},
		{"serif flag", flagsDict(0x2), "Foo", true, false, false, false},
		{"serif name", empty, "Times New Roman", true, false, false, false},
		{"fixed flag", flagsDict(0x1), "Foo", false, true, false, false},
		{"fixed name", empty, "Courier", false, true, false, false},
		{"italic flag", flagsDict(0x40), "Foo", false, false, true, false},
		{"italic name", empty, "Foo-Italic", false, false, true, false},
		{"bold flag", flagsDict(0x40000), "Foo", false, false, false, true},
		{"bold name", empty, "Foo-Bold", false, false, false, true},
		{"bold weight", pdf.PDFDict{Entries: pdf.DictOf(map[string]pdf.PDFValue{"FontWeight": pdf.PDFInteger(700)})}, "Foo", false, false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Pick(c.desc, c.baseFont)
			if got.Serif != c.serif || got.FixedPitch != c.fixedPitch || got.Italic != c.italic || got.Bold != c.bold {
				t.Errorf("Pick(%v, %q) = %+v, want serif=%v fixedPitch=%v italic=%v bold=%v",
					c.desc.Entries, c.baseFont, got, c.serif, c.fixedPitch, c.italic, c.bold)
			}
		})
	}
}
