package pdf

import "testing"

func TestDecodePDFTextStringPDFDocEncoding(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
		want string
	}{
		{"latin-1 ä", []byte{0xE4}, "ä"},
		{"latin-1 ö", []byte{0xF6}, "ö"},
		{"latin-1 ü", []byte{0xFC}, "ü"},
		{"80s bullet", []byte{0x80}, "•"},
		{"80s em-dash", []byte{0x84}, "—"},
		{"80s trademark", []byte{0x92}, "™"},
		{"9F undefined", []byte{0x9F}, "�"},
		{"ASCII", []byte("Hello"), "Hello"},
		{"UTF-16BE BOM", []byte{0xFE, 0xFF, 0x00, 0x48, 0x00, 0x69}, "Hi"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DecodePDFTextString(tc.raw); got != tc.want {
				t.Errorf("DecodePDFTextString(%x) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestDecodeInfoTextString(t *testing.T) {
	tests := []struct {
		name string
		val  PDFValue
		want string
	}{
		{
			"plain text passthrough",
			PDFString{Value: "Hello (World)"},
			"Hello (World)",
		},
		{
			"PDFDocEncoding ä via PDFString",
			PDFString{Value: string([]byte{0xE4})},
			"ä",
		},
		{
			"hex string ä",
			PDFHexString{Value: "E4"},
			"ä",
		},
		{
			"UTF-16BE via PDFString",
			PDFString{Value: string([]byte{0xFE, 0xFF, 0x00, 0x41})},
			"A",
		},
		{
			"non-string returns empty",
			PDFName{Value: "Test"},
			"",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DecodeInfoTextString(tc.val); got != tc.want {
				t.Errorf("DecodeInfoTextString(%T) = %q, want %q", tc.val, got, tc.want)
			}
		})
	}
}

func TestClampInt(t *testing.T) {
	if ClampInt(5, 0, 10) != 5 {
		t.Error("in-range")
	}
	if ClampInt(-3, 0, 10) != 0 {
		t.Error("below lo")
	}
	if ClampInt(99, 0, 10) != 10 {
		t.Error("above hi")
	}
}

func TestPDFNumberToInt(t *testing.T) {
	if v, ok := PDFNumberToInt(PDFInteger(7)); !ok || v != 7 {
		t.Errorf("integer = %d, %v", v, ok)
	}
	if v, ok := PDFNumberToInt(PDFReal(3.9)); !ok || v != 3 {
		t.Errorf("real = %d, %v", v, ok)
	}
	if _, ok := PDFNumberToInt(PDFName{Value: "x"}); ok {
		t.Error("non-number should be false")
	}
}

func TestEncodePDFLiteralString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"backslash", `a\b`, `a\\b`},
		{"parens", "a(b)c", `a\(b\)c`},
		{"CR", "a\rb", `a\rb`},
		{"LF", "a\nb", `a\nb`},
		{"plain", "abc", "abc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := EncodePDFLiteralString(tc.in); got != tc.want {
				t.Errorf("EncodePDFLiteralString(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestValuePointerDefaultBranch covers the default branch (any Go value
// whose reflect.Kind supports Pointer(), not just PDFArray/map[string]PDFValue).
func TestValuePointerDefaultBranch(t *testing.T) {
	s := []byte("hello")
	if ValuePointer(s) == 0 {
		t.Error("ValuePointer(default branch) = 0, want a non-zero pointer")
	}
}

func TestAbsInt(t *testing.T) {
	if AbsInt(-5) != 5 {
		t.Error("AbsInt(-5) should be 5")
	}
	if AbsInt(5) != 5 {
		t.Error("AbsInt(5) should be 5")
	}
}

// TestDecodePDFTextStringOddUTF16 covers the odd-trailing-byte truncation
// branch of the UTF-16BE path.
func TestDecodePDFTextStringOddUTF16(t *testing.T) {
	raw := []byte{0xFE, 0xFF, 0x00, 0x41, 0x00}
	if got := DecodePDFTextString(raw); got != "A" {
		t.Errorf("DecodePDFTextString(odd UTF-16) = %q, want %q", got, "A")
	}
}

// TestDecodePDFHexStringBytesOddDigits covers the odd-digit-count padding branch.
func TestDecodePDFHexStringBytesOddDigits(t *testing.T) {
	if got := DecodePDFHexStringBytes("41 4"); string(got) != "A@" {
		t.Errorf("DecodePDFHexStringBytes(odd) = %q, want %q", got, "A@")
	}
}

func TestDecodePDFName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"valid escape", "A#41B", "AAB"},
		{"truncated escape", "A#4", "A#4"},
		{"invalid hex", "A#ZZB", "A#ZZB"},
		{"plain text", "Plain", "Plain"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(DecodePDFName(tc.in)); got != tc.want {
				t.Errorf("DecodePDFName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
