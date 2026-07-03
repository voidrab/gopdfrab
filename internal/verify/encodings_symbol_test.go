package verify

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

func TestMacRomanToUnicode(t *testing.T) {
	cases := []struct {
		code int
		want uint16
	}{
		{65, 0x0041},   // A
		{0x8E, 0x00E9}, // eacute
		{0xA0, 0x2020}, // dagger
		{0xD5, 0x2019}, // quoteright
		{0xDB, 0x00A4}, // currency
		{0xF5, 0x0131}, // dotlessi
		{0xFF, 0x02C7}, // caron
		{0x00, 0},
	}
	for _, c := range cases {
		if got := MacRomanToUnicode[c.code]; got != c.want {
			t.Errorf("MacRomanToUnicode[%d] = %04X, want %04X", c.code, got, c.want)
		}
	}
}

func TestStandardToUnicode(t *testing.T) {
	cases := []struct {
		code int
		want uint16
	}{
		{65, 0x0041},   // A
		{0x27, 0x2019}, // quoteright, not apostrophe
		{0x60, 0x2018}, // quoteleft, not grave
		{0xA4, 0x2044}, // fraction
		{0x80, 0},
	}
	for _, c := range cases {
		if got := StandardToUnicode[c.code]; got != c.want {
			t.Errorf("StandardToUnicode[%d] = %04X, want %04X", c.code, got, c.want)
		}
	}
}

func TestSymbolToUnicode(t *testing.T) {
	cases := []struct {
		code int
		want uint16
	}{
		{32, 0x0020},  // space
		{34, 0x2200},  // universal
		{67, 0x03A7},  // Chi
		{97, 0x03B1},  // alpha
		{165, 0x221E}, // infinity
		{229, 0x2211}, // summation
		{240, 0xF8FF}, // apple (PUA)
		{254, 0xF8FE}, // bracerightbt (PUA)
		{127, 0},      // undefined
	}
	for _, c := range cases {
		if got := SymbolToUnicode[c.code]; got != c.want {
			t.Errorf("SymbolToUnicode[%d] = %04X, want %04X", c.code, got, c.want)
		}
	}
	if SymbolGlyphNameUnicode["universal"] != 0x2200 || SymbolGlyphNameUnicode["alpha"] != 0x03B1 {
		t.Errorf("SymbolGlyphNameUnicode missing universal/alpha")
	}
}

func TestZapfDingbatsToUnicode(t *testing.T) {
	cases := []struct {
		code int
		want uint16
	}{
		{32, 0x0020},  // space
		{33, 0x2701},  // a1 upper blade scissors
		{37, 0x260E},  // a4 black telephone
		{51, 0x2713},  // a19 check mark
		{52, 0x2714},  // a20 heavy check mark
		{72, 0x2605},  // a35 black star
		{126, 0x275E}, // a100
		{128, 0x2768}, // a89 medium left parenthesis ornament
		{141, 0x2775}, // a96 medium right curly bracket ornament
		{168, 0x2663}, // a112 club suit
		{172, 0x2460}, // a120 circled digit one
		{181, 0x2469}, // a129 circled number ten
		{182, 0x2776}, // a130 negative circled digit one
		{213, 0x2192}, // a161 rightwards arrow
		{254, 0x27BE}, // a191
		{240, 0},      // undefined
		{160, 0},      // undefined
	}
	for _, c := range cases {
		if got := ZapfDingbatsToUnicode[c.code]; got != c.want {
			t.Errorf("ZapfDingbatsToUnicode[%d] = %04X, want %04X", c.code, got, c.want)
		}
	}
	nameCases := map[string]uint16{
		"a19": 0x2713, "a20": 0x2714, "a89": 0x2768, "a205": 0x276E, "a191": 0x27BE,
	}
	for name, want := range nameCases {
		if got := ZapfDingbatsGlyphNameUnicode[name]; got != want {
			t.Errorf("ZapfDingbatsGlyphNameUnicode[%q] = %04X, want %04X", name, got, want)
		}
	}
}

func TestSimpleFontCodeToUnicodeMacRomanExact(t *testing.T) {
	table := SimpleFontCodeToUnicode(pdf.PDFName{Value: "MacRomanEncoding"})
	if table[0x8E] != 0x00E9 {
		t.Errorf("MacRoman code 0x8E = %04X, want 00E9 (eacute)", table[0x8E])
	}
	if table[0xD5] != 0x2019 {
		t.Errorf("MacRoman code 0xD5 = %04X, want 2019 (quoteright)", table[0xD5])
	}
}
