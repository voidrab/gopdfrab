package pdf

import (
	"reflect"
	"strings"
	"unicode/utf16"
)

// ValuePointer returns the identity pointer of a PDFDict's Entries map or a
// PDFArray's backing slice, used to detect cycles and dedupe visits when
// walking the object graph.
func ValuePointer(v PDFValue) uintptr {
	return reflect.ValueOf(v).Pointer()
}

// AbsInt returns the absolute value of x.
func AbsInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ClampInt clamps v to the inclusive range [lo, hi].
func ClampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// PDFNumberToInt extracts an int from a PDFInteger/PDFReal value, the
// repeated PDF-number-operand pattern used throughout font and image checks.
func PDFNumberToInt(v PDFValue) (int, bool) {
	switch x := v.(type) {
	case PDFInteger:
		return int(x), true
	case PDFReal:
		return int(x), true
	}
	return 0, false
}

// DecodePDFLiteralStringBytes decodes a PDF literal string's backslash escape
// sequences into the bytes it represents.
func DecodePDFLiteralStringBytes(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c != '\\' {
			out = append(out, c)
			i++
			continue
		}
		i++
		if i >= len(s) {
			break
		}
		switch s[i] {
		case 'n':
			out = append(out, '\n')
			i++
		case 'r':
			out = append(out, '\r')
			i++
		case 't':
			out = append(out, '\t')
			i++
		case 'b':
			out = append(out, '\b')
			i++
		case 'f':
			out = append(out, '\f')
			i++
		case '(', ')', '\\':
			out = append(out, s[i])
			i++
		case '\r':
			i++
			if i < len(s) && s[i] == '\n' {
				i++
			}
		case '\n':
			i++
		default:
			if s[i] >= '0' && s[i] <= '7' {
				v, j := 0, 0
				for j < 3 && i < len(s) && s[i] >= '0' && s[i] <= '7' {
					v = v*8 + int(s[i]-'0')
					i++
					j++
				}
				out = append(out, byte(v))
			} else {
				out = append(out, s[i])
				i++
			}
		}
	}
	return out
}

// DecodePDFTextString decodes a PDF text string's bytes per 7.9.2.2: a leading
// 0xFE 0xFF byte-order mark means the rest is UTF-16BE, otherwise the bytes are
// returned as-is (PDFDocEncoding, treated as raw single-byte text by this
// package), with any invalid UTF-8 byte replaced so the result is always
// well-formed -- both call sites must apply this identically, so the
// replacement happens here rather than where the value is later embedded in XML.
func DecodePDFTextString(raw []byte) string {
	if len(raw) < 2 || raw[0] != 0xFE || raw[1] != 0xFF {
		return strings.ToValidUTF8(string(raw), "�")
	}
	raw = raw[2:]
	if len(raw)%2 != 0 {
		raw = raw[:len(raw)-1]
	}
	u16 := make([]uint16, len(raw)/2)
	for i := range u16 {
		u16[i] = uint16(raw[i*2])<<8 | uint16(raw[i*2+1])
	}
	return string(utf16.Decode(u16))
}

// DecodePDFHexStringBytes decodes a hex string's digit characters into bytes,
// ignoring whitespace and padding a trailing odd nibble with 0.
func DecodePDFHexStringBytes(s string) []byte {
	digits := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if hexDigit(s[i]) >= 0 {
			digits = append(digits, s[i])
		}
	}
	if len(digits)%2 != 0 {
		digits = append(digits, '0')
	}
	out := make([]byte, 0, len(digits)/2)
	for i := 0; i < len(digits); i += 2 {
		out = append(out, byte(hexDigit(digits[i])<<4|hexDigit(digits[i+1])))
	}
	return out
}

// DecodePDFName decodes PDF name #XX escape sequences and returns the
// resulting byte slice. Unescaped bytes are returned as-is.
func DecodePDFName(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		if s[i] == '#' && i+2 < len(s) {
			hi := hexDigit(s[i+1])
			lo := hexDigit(s[i+2])
			if hi >= 0 && lo >= 0 {
				out = append(out, byte(hi<<4|lo))
				i += 3
				continue
			}
		}
		out = append(out, s[i])
		i++
	}
	return out
}
