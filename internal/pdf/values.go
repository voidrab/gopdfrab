package pdf

import (
	"reflect"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

// ValuePointer returns the identity pointer of a PDFDict's Entries map or a
// PDFArray's backing slice, used to detect cycles and dedupe visits when
// walking the object graph.
func ValuePointer(v PDFValue) uintptr {
	switch x := v.(type) {
	case map[string]PDFValue:
		// A map variable is a pointer to the runtime map header; dereference it.
		return *(*uintptr)(unsafe.Pointer(&x))
	case PDFArray:
		// A slice header starts with the data pointer.
		return *(*uintptr)(unsafe.Pointer(&x))
	default:
		return reflect.ValueOf(v).Pointer()
	}
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

// EncodePDFLiteralString escapes a decoded string's bytes for serialization
// between "(" and ")": backslash, parentheses, and EOL bytes are escaped so a
// conforming reader recovers the exact original bytes.
func EncodePDFLiteralString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\', '(', ')':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\r':
			b.WriteString(`\r`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// pdfDocEncoding80s maps PDFDocEncoding bytes 0x80–0x9F to Unicode codepoints
// (per ISO 32000-1 Annex D). 0x9F is undefined; all others have explicit mappings.
var pdfDocEncoding80s = [32]rune{
	0x2022, 0x2020, 0x2021, 0x2026, 0x2014, 0x2013, 0x0192, 0x2044,
	0x2039, 0x203A, 0x2212, 0x2030, 0x201E, 0x201C, 0x201D, 0x2018,
	0x2019, 0x201A, 0x2122, 0xFB01, 0xFB02, 0x0141, 0x0152, 0x0160,
	0x0178, 0x017D, 0x0131, 0x0142, 0x0153, 0x0161, 0x017E, 0xFFFD,
}

// decodePDFDocEncoding converts raw PDFDocEncoding bytes to a UTF-8 string.
// 0x00–0x7F are ASCII-identical; 0x80–0x9F use pdfDocEncoding80s; 0xA0–0xFF
// map directly to the same Unicode codepoints (Latin-1 range).
func decodePDFDocEncoding(raw []byte) string {
	var b strings.Builder
	b.Grow(len(raw))
	var tmp [4]byte
	for _, c := range raw {
		var r rune
		switch {
		case c <= 0x7F:
			r = rune(c)
		case c >= 0xA0:
			r = rune(c) // Latin-1 = Unicode
		default:
			r = pdfDocEncoding80s[c-0x80]
		}
		n := utf8.EncodeRune(tmp[:], r)
		b.Write(tmp[:n])
	}
	return b.String()
}

// DecodePDFTextString decodes a PDF text string's bytes per 7.9.2.2: a leading
// 0xFE 0xFF BOM means the rest is UTF-16BE; otherwise bytes are PDFDocEncoding.
func DecodePDFTextString(raw []byte) string {
	if len(raw) < 2 || raw[0] != 0xFE || raw[1] != 0xFF {
		return decodePDFDocEncoding(raw)
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

// DecodeInfoTextString decodes a PDFString or PDFHexString Info-dictionary value
// to Unicode: the bytes are interpreted as a PDF text string (UTF-16BE with
// BOM, otherwise PDFDocEncoding). Returns "" for any other value type.
func DecodeInfoTextString(v PDFValue) string {
	switch s := v.(type) {
	case PDFString:
		return DecodePDFTextString([]byte(s.Value))
	case PDFHexString:
		return DecodePDFTextString(DecodePDFHexStringBytes(s.Value))
	}
	return ""
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
