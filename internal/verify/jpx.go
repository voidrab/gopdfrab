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

// JPXImageHeader reads the JP2 header box of the JPEG 2000 data of an image
// XObject that uses the JPXDecode filter. ok is false for any other
// dictionary and for data whose filters before the codec do not decode.
func JPXImageHeader(d pdf.PDFDict) (h JPXHeader, ok bool) {
	if (d.Entries.Get("Subtype") != pdf.PDFName{Value: "Image"}) || !pdf.HasFilter(d.Entries.Get("Filter"), pdf.FilterJPX) {
		return JPXHeader{}, false
	}
	data, _, err := pdf.ImageCodecInput(d)
	if err != nil {
		return JPXHeader{}, false
	}
	return readJPXHeader(data), true
}

// readJPXHeader reads the JP2 header box of JPEG 2000 data, the way veraPDF
// does: a box it cannot read ends the parse, keeping what was read so far.
// It reads r only up to the end of that box, skipping the boxes before it,
// and treats a read error as the end of the data.
func readJPXHeader(r io.Reader) JPXHeader {
	var sig [12]byte
	if _, err := io.ReadFull(r, sig[:]); err != nil || !bytes.Equal(sig[:], jp2Signature) {
		return JPXHeader{EnumCS: -1}
	}
	header, ok := readJP2HeaderBox(r)
	if !ok {
		return JPXHeader{EnumCS: -1}
	}
	return parseJP2Header(header)
}

// readJP2HeaderBox reads top-level boxes from r up to the first header box,
// and returns its contents, which may be cut short by the end of the data or
// by maxJP2HeaderBytes. found is false when no header box is read.
func readJP2HeaderBox(r io.Reader) (body []byte, found bool) {
	var head [8]byte
	for {
		if _, err := io.ReadFull(r, head[:]); err != nil {
			return nil, false
		}
		length, headerLen, kind := uint64(binary.BigEndian.Uint32(head[:])), uint64(8), string(head[4:])
		if length == 1 {
			if _, err := io.ReadFull(r, head[:]); err != nil {
				return nil, false
			}
			length, headerLen = binary.BigEndian.Uint64(head[:]), 16
		}
		toEnd := length == 0
		if !toEnd && length < headerLen {
			return nil, false
		}
		switch {
		case kind == "jp2h" && toEnd:
			body, _ = io.ReadAll(io.LimitReader(r, maxJP2HeaderBytes))
			return body, true
		case kind == "jp2h":
			body = make([]byte, min(length-headerLen, maxJP2HeaderBytes))
			n, _ := io.ReadFull(r, body)
			return body[:n], true
		case toEnd:
			return nil, false
		}
		if _, err := io.CopyN(io.Discard, r, int64(length-headerLen)); err != nil {
			return nil, false
		}
	}
}

// parseJP2Header reads the boxes of a JP2 header box's contents. The colour
// specification a reader uses is the first with APPROX 1, or else the only
// one.
func parseJP2Header(header []byte) JPXHeader {
	h := JPXHeader{EnumCS: -1}
	chosen := false
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
			if approx {
				h.ApproxSpecs++
			}
			use := !chosen && (approx || h.ColourSpecs == 1)
			if use {
				h.Method, h.EnumCS, chosen = method, -1, approx
			}
			if method == 1 {
				if len(body) < 7 {
					return false
				}
				if use {
					h.EnumCS = int(binary.BigEndian.Uint32(body[3:7]))
				}
			}
		}
		return true
	})
	if !chosen && h.ColourSpecs != 1 {
		h.Method, h.EnumCS = 0, -1
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
	if !ctx.part2() {
		return
	}
	h, ok := JPXImageHeader(v)
	if !ok {
		return
	}
	if h.Channels != 1 && h.Channels != 3 && h.Channels != 4 {
		ctx.Report(pdf.Checks.Image.JPXColourChannels, v, fmt.Sprintf("JPEG 2000 data has %d colour channels, not 1, 3 or 4", h.Channels))
	}
	if h.BPCC || h.BitDepth < 1 || h.BitDepth > 38 {
		ctx.Report(pdf.Checks.Image.JPXBitDepth, v, fmt.Sprintf("JPEG 2000 data has bit depth %d or a bpcc box, not one depth from 1 to 38", h.BitDepth))
	}
	if v.Entries.Get("ColorSpace") != nil {
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
