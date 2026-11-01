package fontprog

import "testing"

func TestInAdobeGlyphList(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"A", true},
		{"grave", true},
		{"zuhiragana", true},
		{".notdef", true},
		{"uni0041", false},
		{"g123", false},
		{"", false},
		{"#", false},
	} {
		if got := InAdobeGlyphList(tc.name); got != tc.want {
			t.Errorf("InAdobeGlyphList(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
	if n := len(glyphListNames()); n != 4281 {
		t.Errorf("glyph list has %d names, want 4281", n)
	}
}
