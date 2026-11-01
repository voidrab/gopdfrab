package convert

import (
	"bytes"
	"encoding/binary"
	"io"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// jpxHeaderFixer remediates Checks.Image.JPXColourChannels and JPXBitDepth
// (6.2.8.3) for JPEG 2000 data whose JP2 header misstates the codestream it
// wraps. The rules read the number of components and their bit depth from the
// image header box, while a decoder takes them from the codestream's SIZ
// marker. When the codestream has 1, 3 or 4 components of one depth the rules
// allow, the image header box is rewritten to say so, and a bits-per-component
// box, which components of one depth do not need, is removed. The codestream
// itself is not touched. Data whose codestream breaks the rules too cannot be
// repaired without re-encoding it and stays reported.
type jpxHeaderFixer struct{}

func (jpxHeaderFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Image.JPXColourChannels || c == pdf.Checks.Image.JPXBitDepth
}

func (jpxHeaderFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) bool {
		h, ok := verify.JPXImageHeader(d)
		if !ok || validJPXChannels(h.Channels) && !h.BPCC && h.BitDepth >= 1 && h.BitDepth <= 38 {
			return false
		}
		r, _, err := pdf.ImageCodecInput(d)
		var data []byte
		if err == nil {
			data, err = io.ReadAll(r)
		}
		if err != nil {
			return false
		}
		fixed, ok := correctJP2Header(data)
		if !ok {
			return false
		}
		// The reader yields the data after the filters that precede the
		// codec, so JPXDecode is the only filter the stream keeps.
		d.SetStream(fixed)
		d.Entries.Set("Filter", pdf.PDFName{Value: "JPXDecode"})
		d.Entries.Del("DecodeParms")
		changed = true
		return true
	})
	return changed, nil
}

// validJPXChannels reports whether n components is a number 6.2.8.3 allows.
func validJPXChannels(n int) bool { return n == 1 || n == 3 || n == 4 }

// jp2Box is a box of a JP2 file: its type and the extent of its contents.
type jp2Box struct {
	kind             string
	start, body, end int // the box, its contents and its end, as offsets
}

// jp2Boxes returns the boxes in data, in order, with their offsets shifted by
// base. It returns false when a box is malformed or uses the extended length
// form (length field 1). A length field of 0 extends the box to the end of
// data.
func jp2Boxes(data []byte, base int) ([]jp2Box, bool) {
	var boxes []jp2Box
	for off := 0; off < len(data); {
		if len(data)-off < 8 {
			return nil, false
		}
		length := int(binary.BigEndian.Uint32(data[off:]))
		if length == 0 {
			length = len(data) - off
		}
		if length < 8 || length > len(data)-off {
			return nil, false
		}
		boxes = append(boxes, jp2Box{kind: string(data[off+4 : off+8]), start: base + off, body: base + off + 8, end: base + off + length})
		off += length
	}
	return boxes, true
}

// correctJP2Header returns the JP2 file data with its image header box made to
// agree with its codestream. It returns false when the file cannot be read or
// lacks a header or codestream box, when the codestream's components are
// outside the rules, and when the header already agrees.
func correctJP2Header(data []byte) ([]byte, bool) {
	top, ok := jp2Boxes(data, 0)
	if !ok {
		return nil, false
	}
	var header, codestream *jp2Box
	for i := range top {
		switch top[i].kind {
		case "jp2h":
			if header == nil {
				header = &top[i]
			}
		case "jp2c":
			if codestream == nil {
				codestream = &top[i]
			}
		}
	}
	if header == nil || codestream == nil {
		return nil, false
	}
	components, ssiz, ok := codestreamComponents(data[codestream.body:codestream.end])
	if !ok || !validJPXChannels(components) {
		return nil, false
	}
	children, ok := jp2Boxes(data[header.body:header.end], header.body)
	if !ok {
		return nil, false
	}

	var body bytes.Buffer
	changed := false
	for _, c := range children {
		switch c.kind {
		case "bpcc":
			changed = true
			continue
		case "ihdr":
			ihdr := append([]byte(nil), data[c.start:c.end]...)
			if len(ihdr) != 22 {
				return nil, false
			}
			binary.BigEndian.PutUint16(ihdr[16:], uint16(components))
			ihdr[18] = ssiz // the image header states the depth as SIZ does
			if !bytes.Equal(ihdr, data[c.start:c.end]) {
				changed = true
			}
			body.Write(ihdr)
			continue
		}
		body.Write(data[c.start:c.end])
	}
	if !changed {
		return nil, false
	}
	out := make([]byte, 0, len(data))
	out = append(out, data[:header.start]...)
	out = binary.BigEndian.AppendUint32(out, uint32(8+body.Len()))
	out = append(out, "jp2h"...)
	out = append(out, body.Bytes()...)
	return append(out, data[header.end:]...), true
}

// codestreamComponents returns the number of components and their shared
// Ssiz byte, which is the bit depth less one, from the SIZ marker of a JPEG
// 2000 codestream. ok is false when the marker cannot be read, when the
// components differ in depth, when the depth is outside 1 to 38 bits, or when
// the components are signed. veraPDF reads a signed depth as negative, which
// no image header can make conform.
func codestreamComponents(cs []byte) (components int, ssiz byte, ok bool) {
	// SOC, then SIZ: Lsiz, Rsiz, eight 32-bit sizes and offsets, Csiz, and
	// Ssiz, XRsiz and YRsiz for each component.
	if len(cs) < 4+38 || cs[0] != 0xFF || cs[1] != 0x4F || cs[2] != 0xFF || cs[3] != 0x51 {
		return 0, 0, false
	}
	siz := cs[2:]
	components = int(binary.BigEndian.Uint16(siz[38:]))
	if components == 0 || len(siz) < 40+3*components {
		return 0, 0, false
	}
	ssiz = siz[40]
	if ssiz&0x80 != 0 || ssiz > 37 {
		return 0, 0, false
	}
	for k := 1; k < components; k++ {
		if siz[40+3*k] != ssiz {
			return 0, 0, false
		}
	}
	return components, ssiz, true
}
