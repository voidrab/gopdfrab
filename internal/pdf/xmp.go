package pdf

import (
	"errors"
	"fmt"
	"regexp"
	"unicode/utf16"
	"unicode/utf8"
)

// PDFAPartRe and PDFAConfRe extract the PDF/A part and conformance level a
// document's XMP metadata claims (6.7.11 pdfaid namespace), shared by
// ClaimedConformance and the 6.7.11 verifier.
var (
	PDFAPartRe = regexp.MustCompile(`pdfaid:part\s*=\s*"([^"]*)"|<pdfaid:part>\s*([^<\s]+)\s*</pdfaid:part>`)
	PDFAConfRe = regexp.MustCompile(`pdfaid:conformance\s*=\s*"([^"]*)"|<pdfaid:conformance>\s*([^<\s]+)\s*</pdfaid:conformance>`)
)

// FirstRegexpGroup returns the first non-empty capture group re matches in s.
func FirstRegexpGroup(re *regexp.Regexp, s string) (string, bool) {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	for _, g := range m[1:] {
		if g != "" {
			return g, true
		}
	}
	return "", true
}

// ErrNoXMPMetadata and ErrXMPMetadataNotStream are returned by RawXMP when the
// document catalog has no usable Metadata stream at all (as opposed to one
// that exists but fails to decode).
var (
	ErrNoXMPMetadata        = errors.New("document catalog lacks a Metadata entry")
	ErrXMPMetadataNotStream = errors.New("document Metadata is not a metadata stream")
)

// RawXMP resolves the document's Metadata stream (Root/Metadata) and returns
// its decoded XMP packet bytes, normalised to UTF-8, along with the metadata
// stream's own dictionary (so callers can inspect entries like Filter). meta
// is returned even on a decode error, but is zero-valued if no Metadata
// stream exists at all (ErrNoXMPMetadata / ErrXMPMetadataNotStream).
func (d *Reader) RawXMP() (data []byte, meta PDFDict, err error) {
	value, err := d.ResolveGraphByPath([]string{"Root", "Metadata"})
	if err != nil || value == nil {
		return nil, PDFDict{}, ErrNoXMPMetadata
	}
	meta, ok := value.(PDFDict)
	if !ok || !meta.HasStream {
		return nil, PDFDict{}, ErrXMPMetadataNotStream
	}

	data, err = DecodeStream(meta)
	if err != nil {
		return nil, meta, fmt.Errorf("unable to read XMP metadata stream: %w", err)
	}
	// Normalise UTF-16/UTF-32 XMP streams to UTF-8 before any further processing.
	return decodeXMPEncoding(data), meta, nil
}

// decodeXMPEncoding converts an XMP stream to UTF-8 if it is UTF-16 or
// UTF-32 encoded, detected by BOM or the leading '<' byte pattern.
func decodeXMPEncoding(data []byte) []byte {
	if len(data) < 4 {
		if len(data) >= 2 {
			return decodeXMPEncoding16(data, 0)
		}
		return data
	}

	// UTF-32 BOM detection (must come before UTF-16 BOM check because
	// UTF-32 LE BOM starts with the same two bytes as UTF-16 LE BOM).
	if data[0] == 0xFF && data[1] == 0xFE && data[2] == 0x00 && data[3] == 0x00 {
		return decodeUTF32(data[4:], true) // UTF-32 LE BOM
	}
	if data[0] == 0x00 && data[1] == 0x00 && data[2] == 0xFE && data[3] == 0xFF {
		return decodeUTF32(data[4:], false) // UTF-32 BE BOM
	}
	// UTF-32 without BOM: '<' followed by three NUL bytes.
	if data[0] == 0x3C && data[1] == 0x00 && data[2] == 0x00 && data[3] == 0x00 {
		return decodeUTF32(data, true) // UTF-32 LE
	}
	if data[0] == 0x00 && data[1] == 0x00 && data[2] == 0x00 && data[3] == 0x3C {
		return decodeUTF32(data, false) // UTF-32 BE
	}
	// UTF-16 BOM or bare '<' + 0x00 pattern.
	return decodeXMPEncoding16(data, 0)
}

// decodeUTF32 converts a UTF-32 stream (offset bytes already stripped) to UTF-8.
func decodeUTF32(raw []byte, le bool) []byte {
	n := len(raw) / 4
	buf := make([]byte, 0, n*3)
	var tmp [4]byte
	for i := range n {
		b := raw[i*4 : i*4+4]
		var cp uint32
		if le {
			cp = uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
		} else {
			cp = uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
		}
		r := rune(cp)
		if r > 0x10FFFF {
			r = 0xFFFD
		}
		sz := utf8.EncodeRune(tmp[:], r)
		buf = append(buf, tmp[:sz]...)
	}
	return buf
}

// decodeXMPEncoding16 handles UTF-16 detection and conversion.
func decodeXMPEncoding16(data []byte, _ int) []byte {
	if len(data) < 2 {
		return data
	}
	var le bool
	offset := 0
	if data[0] == 0xFF && data[1] == 0xFE {
		le = true
		offset = 2
	} else if data[0] == 0xFE && data[1] == 0xFF {
		le = false
		offset = 2
	} else if data[0] == 0x3C && data[1] == 0x00 {
		le = true
	} else if data[0] == 0x00 && data[1] == 0x3C {
		le = false
	} else {
		return data // Already UTF-8
	}
	raw := data[offset:]
	if len(raw)%2 != 0 {
		raw = raw[:len(raw)-1]
	}
	u16 := make([]uint16, len(raw)/2)
	for i := range u16 {
		b0, b1 := raw[i*2], raw[i*2+1]
		if le {
			u16[i] = uint16(b0) | uint16(b1)<<8
		} else {
			u16[i] = uint16(b0)<<8 | uint16(b1)
		}
	}
	runes := utf16.Decode(u16)
	buf := make([]byte, 0, len(runes)*3)
	var tmp [4]byte
	for _, r := range runes {
		n := utf8.EncodeRune(tmp[:], r)
		buf = append(buf, tmp[:n]...)
	}
	return buf
}
