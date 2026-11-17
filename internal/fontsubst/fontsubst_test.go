//go:build !gopdfrab_nofonts

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
		data                            func() []byte
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
			if !bytes.Equal(got.Data, c.data()) {
				t.Errorf("Pick(%v, %q) returned the wrong bundled face", c.desc.Entries, c.baseFont)
			}
		})
	}
}

// TestBundledFonts verifies that every bundled font inflates to a TrueType
// program with sfnt version 0x00010000, and that a second call returns the
// same backing array instead of inflating again.
func TestBundledFonts(t *testing.T) {
	fonts := map[string]func() []byte{
		"LiberationSans-Regular":            LiberationSansRegular,
		"LiberationSans-Bold":               libSansBold,
		"LiberationSans-Italic":             libSansItalic,
		"LiberationSans-BoldItalic":         libSansBoldItalic,
		"LiberationSerif-Regular":           libSerifRegular,
		"LiberationSerif-Bold":              libSerifBold,
		"LiberationSerif-Italic":            libSerifItalic,
		"LiberationSerif-BoldItalic":        libSerifBoldItalic,
		"LiberationMono-Regular":            libMonoRegular,
		"LiberationMono-Bold":               libMonoBold,
		"LiberationMono-Italic":             libMonoItalic,
		"LiberationMono-BoldItalic":         libMonoBoldItalic,
		"NotoSansSymbols2-Regular":          NotoSymbols2,
		"NotoSansSymbols-VariableFont_wght": NotoSymbols,
	}
	for name, font := range fonts {
		t.Run(name, func(t *testing.T) {
			data := font()
			if len(data) < 4 || !bytes.Equal(data[:4], []byte{0, 1, 0, 0}) {
				t.Fatalf("%s does not start with the sfnt version 0x00010000", name)
			}
			if again := font(); &again[0] != &data[0] {
				t.Errorf("%s was inflated again on its second call", name)
			}
		})
	}
}

// TestInflateCorrupt verifies that inflate fails on data that is not gzip
// and on a gzip stream cut short.
func TestInflateCorrupt(t *testing.T) {
	z, err := builtin("LiberationSans-Regular.ttf.gz")
	if err != nil {
		t.Fatal(err)
	}
	for name, z := range map[string][]byte{"not gzip": []byte("not gzip"), "truncated": z[:len(z)/2]} {
		t.Run(name, func(t *testing.T) {
			if _, err := inflate(z); err == nil {
				t.Error("inflate succeeded on corrupt data")
			}
		})
	}
}

// TestPreloadBuiltIn verifies that Preload loads nothing for the built-in
// fonts, which are loaded on first use.
func TestPreloadBuiltIn(t *testing.T) {
	SetSource(nil)
	if err := Preload(); err != nil {
		t.Fatalf("Preload() = %v, want nil", err)
	}
	_, gen := current()
	for name, f := range fonts {
		if f.gen == gen && (f.z != nil || f.data != nil) {
			t.Errorf("Preload loaded the built-in %s", name)
		}
	}
}
