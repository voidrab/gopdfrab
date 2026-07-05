package pdf

import "testing"

func TestFirstRegexpGroup(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		want   string
		wantOK bool
	}{
		{"no match", "nothing here", "", false},
		{"attribute form", `pdfaid:part = "1"`, "1", true},
		{"element form empty group", "pdfaid:part=\"\"", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := FirstRegexpGroup(PDFAPartRe, tc.s)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("FirstRegexpGroup() = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestDecodeXMPEncoding(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want []byte
	}{
		{"empty", []byte{}, []byte{}},
		{"single byte", []byte{0x41}, []byte{0x41}},
		{"2-3 bytes delegates to decodeXMPEncoding16", []byte{0x3C, 0x00, 0x41}, []byte("<")},
		{"utf-32 LE BOM", append([]byte{0xFF, 0xFE, 0x00, 0x00}, encodeUTF32(t, 'A', true)...), []byte("A")},
		{"utf-32 BE BOM", append([]byte{0x00, 0x00, 0xFE, 0xFF}, encodeUTF32(t, 'A', false)...), []byte("A")},
		{"bare utf-32 LE", append(encodeUTF32(t, '<', true), encodeUTF32(t, 'B', true)...), []byte("<B")},
		{"bare utf-32 BE", append(encodeUTF32(t, '<', false), encodeUTF32(t, 'B', false)...), []byte("<B")},
		{"utf-8 passthrough", []byte("<xml/>"), []byte("<xml/>")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeXMPEncoding(tc.data)
			if string(got) != string(tc.want) {
				t.Errorf("decodeXMPEncoding(%v) = %q, want %q", tc.data, got, tc.want)
			}
		})
	}
}

// encodeUTF32 packs a single rune as 4 raw UTF-32 bytes (no BOM), used to build
// UTF-32 test fixtures by hand rather than importing an encoding package.
func encodeUTF32(t *testing.T, r rune, le bool) []byte {
	t.Helper()
	b := make([]byte, 4)
	v := uint32(r)
	if le {
		b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
	} else {
		b[0], b[1], b[2], b[3] = byte(v>>24), byte(v>>16), byte(v>>8), byte(v)
	}
	return b
}

func TestDecodeUTF32(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
		le   bool
		want string
	}{
		{"LE ascii", encodeUTF32(t, 'Z', true), true, "Z"},
		{"BE ascii", encodeUTF32(t, 'Z', false), false, "Z"},
		{"LE out of range replaced", encodeUTF32(t, 0, true), true, "\x00"}, // placeholder, overwritten below
	}
	// Out-of-range codepoint (> 0x10FFFF) must become U+FFFD.
	outOfRange := []byte{0x00, 0x00, 0x11, 0x00} // LE: 0x00110000
	tests[2] = struct {
		name string
		raw  []byte
		le   bool
		want string
	}{"LE out of range replaced", outOfRange, true, "�"}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeUTF32(tc.raw, tc.le)
			if string(got) != tc.want {
				t.Errorf("decodeUTF32(%v, %v) = %q, want %q", tc.raw, tc.le, got, tc.want)
			}
		})
	}
}

func TestDecodeXMPEncoding16(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"too short", []byte{0x41}, "A"},
		{"LE BOM", []byte{0xFF, 0xFE, 0x41, 0x00}, "A"},
		{"BE BOM", []byte{0xFE, 0xFF, 0x00, 0x41}, "A"},
		{"bare LE", []byte{0x3C, 0x00, 0x41, 0x00}, "<A"},
		{"bare BE", []byte{0x00, 0x3C, 0x00, 0x41}, "<A"},
		{"utf-8 passthrough", []byte("<a"), "<a"},
		{"odd length LE trimmed", []byte{0xFF, 0xFE, 0x41, 0x00, 0x42}, "A"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeXMPEncoding16(tc.data, 0)
			if string(got) != tc.want {
				t.Errorf("decodeXMPEncoding16(%v) = %q, want %q", tc.data, got, tc.want)
			}
		})
	}
}
