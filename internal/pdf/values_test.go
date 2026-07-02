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
