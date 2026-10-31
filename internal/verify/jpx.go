package verify

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// 6.2.8.3 JPEG2000 (ISO 19005-2)

// JPXHeader is what the JP2 header box of JPEG 2000 data says about its
// colour: the fields ISO 19005-2 6.2.8.3 restricts. The zero value is the
// header of data that is no JP2 file, such as a bare codestream.
type JPXHeader struct {
	// Channels is the number of components (NC) and BitDepth their bit
	// depth, both from the image header box.
	Channels, BitDepth int
	// BPCC is true when a bits-per-component box is present, which only
	// components of differing depths need.
	BPCC bool
	// ColourSpecs counts the colour specification boxes and ApproxSpecs
	// those whose APPROX field is 1.
	ColourSpecs, ApproxSpecs int
	// Method and EnumCS are the METH and EnumCS fields of the colour
	// specification a reader uses: the first with APPROX 1, or else the
	// only one. EnumCS is -1 when that box enumerates no colour space.
	Method, EnumCS int
}

// ColourSpecificationUsable reports whether the data has exactly one colour
// specification a reader uses, of a method and colour space the rules allow,
// so that an image needs no ColorSpace entry to override it.
func (h JPXHeader) ColourSpecificationUsable() bool {
	return (h.ColourSpecs == 1 || h.ApproxSpecs == 1) && h.Method >= 1 && h.Method <= 3 && h.EnumCS != 19
}

// jp2Signature is the JPEG 2000 signature box that starts a JP2 file.
var jp2Signature = []byte{0, 0, 0, 0x0C, 'j', 'P', ' ', ' ', 0x0D, 0x0A, 0x87, 0x0A}

// maxJP2HeaderBytes bounds how much of a JP2 header box ReadJPXHeader reads.
// The box holds a few small boxes and at most an ICC profile.
const maxJP2HeaderBytes = 16 << 20

// ReadJPXHeader reads the JP2 header box of JPEG 2000 data, the way veraPDF
// does: a box it cannot read ends the parse, keeping what was read so far.
// It reads r only up to the end of that box, skipping the boxes before it,
// and treats a read error as the end of the data.
func ReadJPXHeader(r io.Reader) JPXHeader {
	sig := make([]byte, len(jp2Signature))
	if _, err := io.ReadFull(r, sig); err != nil || !bytes.Equal(sig, jp2Signature) {
		return JPXHeader{EnumCS: -1}
	}
	header, ok := readJP2Box(r, "jp2h")
	if !ok {
		return JPXHeader{EnumCS: -1}
	}
	return parseJP2Header(header)
}

// readJP2Box reads top-level boxes from r up to the first of a kind, and
// returns its contents, which may be cut short by the end of the data or by
// maxJP2HeaderBytes. found is false when no box of the kind is read.
func readJP2Box(r io.Reader, kind string) (body []byte, found bool) {
	for {
		head := make([]byte, 8)
		if _, err := io.ReadFull(r, head); err != nil {
			return nil, false
		}
		length, headerLen, boxKind := uint64(binary.BigEndian.Uint32(head)), uint64(8), string(head[4:])
		if length == 1 {
			if _, err := io.ReadFull(r, head); err != nil {
				return nil, false
			}
			length, headerLen = binary.BigEndian.Uint64(head), 16
		}
		toEnd := length == 0
		if !toEnd && length < headerLen {
			return nil, false
		}
		if boxKind == kind {
			limit := uint64(maxJP2HeaderBytes)
			if !toEnd {
				limit = min(limit, length-headerLen)
			}
			body, _ = io.ReadAll(io.LimitReader(r, int64(limit)))
			return body, true
		}
		if toEnd {
			return nil, false
		}
		if _, err := io.CopyN(io.Discard, r, int64(length-headerLen)); err != nil {
			return nil, false
		}
	}
}

// parseJP2Header reads the boxes of a JP2 header box's contents.
func parseJP2Header(header []byte) JPXHeader {
	h := JPXHeader{EnumCS: -1}
	firstMethod, firstEnum := -1, -1
	approxMethod, approxEnum := -1, -1
	forEachJP2Box(header, func(kind string, body []byte) bool {
		switch kind {
		case "ihdr":
			if len(body) != 14 {
				return false
			}
			h.Channels = int(binary.BigEndian.Uint16(body[8:10]))
			h.BitDepth = int(int8(body[10])) + 1
		case "bpcc":
			h.BPCC = true
		case "colr":
			if len(body) < 3 {
				return false
			}
			h.ColourSpecs++
			method, approx := int(body[0]), body[2] == 1
			first := h.ColourSpecs == 1
			usedApprox := approx && approxMethod < 0
			if first {
				firstMethod = method
			}
			if approx {
				h.ApproxSpecs++
			}
			if usedApprox {
				approxMethod = method
			}
			if method == 1 {
				if len(body) < 7 {
					return false
				}
				enum := int(binary.BigEndian.Uint32(body[3:7]))
				if first {
					firstEnum = enum
				}
				if usedApprox {
					approxEnum = enum
				}
			}
		}
		return true
	})
	switch {
	case h.ApproxSpecs > 0:
		h.Method, h.EnumCS = approxMethod, approxEnum
	case h.ColourSpecs == 1:
		h.Method, h.EnumCS = firstMethod, firstEnum
	}
	return h
}

// forEachJP2Box calls fn with the type and contents of each box in data, in
// order, until fn returns false or a box is malformed. A box of length 0
// extends to the end of data.
func forEachJP2Box(data []byte, fn func(kind string, body []byte) bool) {
	for len(data) >= 8 {
		length := uint64(binary.BigEndian.Uint32(data))
		kind := string(data[4:8])
		headerLen := uint64(8)
		if length == 1 {
			if len(data) < 16 {
				return
			}
			length, headerLen = binary.BigEndian.Uint64(data[8:16]), 16
		}
		if length == 0 {
			length = uint64(len(data))
		}
		if length < headerLen || length > uint64(len(data)) {
			return
		}
		if !fn(kind, data[headerLen:length]) {
			return
		}
		data = data[length:]
	}
}

// validateJPXImage checks the JPEG 2000 data of an image XObject that uses
// the JPXDecode filter. The colour specification rules do not apply to an
// image whose ColorSpace entry overrides the data's own colour space.
func validateJPXImage(v pdf.PDFDict, ctx *ValidationContext) {
	if !ctx.part2() || (v.Entries.Get("Subtype") != pdf.PDFName{Value: "Image"}) ||
		!pdf.HasFilter(v.Entries.Get("Filter"), pdf.FilterJPX) {
		return
	}
	data, codec, err := pdf.ImageCodecInput(v)
	if err != nil || codec == nil || codec.Kind != pdf.FilterJPX {
		return
	}
	h := ReadJPXHeader(data)
	if h.Channels != 1 && h.Channels != 3 && h.Channels != 4 {
		ctx.Report(pdf.Checks.Image.JPXColourChannels, v, fmt.Sprintf("JPEG 2000 data has %d colour channels, not 1, 3 or 4", h.Channels))
	}
	if h.BPCC || h.BitDepth < 1 || h.BitDepth > 38 {
		ctx.Report(pdf.Checks.Image.JPXBitDepth, v, fmt.Sprintf("JPEG 2000 data has bit depth %d or a bpcc box, not one depth from 1 to 38", h.BitDepth))
	}
	if cs := v.Entries.Get("ColorSpace"); cs != nil {
		return
	}
	if h.ColourSpecs != 1 && h.ApproxSpecs != 1 {
		ctx.Report(pdf.Checks.Image.JPXColourSpecs, v, fmt.Sprintf("JPEG 2000 data has %d colour specifications, %d of them with APPROX 1", h.ColourSpecs, h.ApproxSpecs))
	}
	if h.Method < 1 || h.Method > 3 {
		ctx.Report(pdf.Checks.Image.JPXColourMethod, v, fmt.Sprintf("JPEG 2000 colour specification method is %d, not 1, 2 or 3", h.Method))
	}
	if h.EnumCS == 19 {
		ctx.Report(pdf.Checks.Image.JPXCIEJab, v, "JPEG 2000 data uses the enumerated colour space CIEJab")
	}
}
