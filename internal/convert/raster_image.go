package convert

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// DecodeImageRGBA decodes an Image XObject dictionary's samples into an RGBA
// buffer suitable for compositing during page rendering.
//
// FlateDecode/LZWDecode/ASCII-filtered raw samples (with PNG/TIFF predictor
// undo), DCTDecode (via the standard library's image/jpeg) and CCITTFaxDecode
// (Group 3/4, ccitt.go) are supported. JBIG2Decode/JPXDecode have no decoder
// in this codebase -- those JBIG2/JPEG2000 codecs are large standalone
// efforts out of scope here -- so an image using one of them, or a CCITT
// image that fails to decode, is painted as a flat mid-gray placeholder
// instead of failing the page.
func DecodeImageRGBA(dict pdf.PDFDict, resources pdf.PDFDict) (*image.RGBA, error) {
	width := pdf.DictInt(dict, "Width", 0)
	height := pdf.DictInt(dict, "Height", 0)
	if width <= 0 || height <= 0 {
		return nil, errInvalidImageDims
	}

	filters := pdf.FilterNames(dict.Entries["Filter"])
	last := ""
	if len(filters) > 0 {
		last = filters[len(filters)-1]
	}

	switch last {
	case "DCTDecode", "DCT":
		return decodeJPEGImage(dict)
	case "CCITTFaxDecode", "CCF":
		if img, err := decodeCCITTImage(dict, resources, width, height); err == nil {
			return img, nil
		}
		return placeholderImage(width, height), nil
	case "JBIG2Decode", "JPXDecode":
		return placeholderImage(width, height), nil
	}

	data, err := decodeImageRawSamples(dict)
	if err != nil {
		return nil, err
	}
	return unpackSamplesToRGBA(dict, resources, data, width, height)
}

type imageDecodeError string

func (e imageDecodeError) Error() string { return string(e) }

const errInvalidImageDims = imageDecodeError("raster_image: invalid /Width or /Height")

// decodeImageRawSamples decodes an image stream's filter chain (FlateDecode,
// LZWDecode, ASCIIHex/85Decode) and undoes any PNG/TIFF predictor, leaving
// raw packed component samples.
func decodeImageRawSamples(dict pdf.PDFDict) ([]byte, error) {
	filters := pdf.FilterNames(dict.Entries["Filter"])
	hasLZW := false
	for _, f := range filters {
		if f == "LZWDecode" || f == "LZW" {
			hasLZW = true
		}
	}

	var data []byte
	var err error
	if hasLZW {
		data, err = pdf.DecodeLZW(dict.RawStream)
	} else {
		data, err = pdf.DecodeStream(dict)
	}
	if err != nil {
		return nil, err
	}

	parms := pdf.StreamDecodeParms(dict)
	predictor := pdf.DictInt(parms, "Predictor", 1)
	if predictor == 1 {
		return data, nil
	}
	columns := pdf.DictInt(parms, "Columns", pdf.DictInt(dict, "Width", 1))
	colors := pdf.DictInt(parms, "Colors", 1)
	bpc := pdf.DictInt(parms, "BitsPerComponent", pdf.DictInt(dict, "BitsPerComponent", 8))
	if predictor == 2 {
		return pdf.UndoTIFFPredictor(data, columns, colors, bpc)
	}
	return pdf.UndoPNGPredictor(data, columns, colors, bpc)
}

// unpackSamplesToRGBA reads width*height pixels of packed component samples
// (bitsPerComponent-wide, row-padded to a byte boundary per the PDF spec)
// and resolves each pixel's colour via ResolveColor.
func unpackSamplesToRGBA(dict pdf.PDFDict, resources pdf.PDFDict, data []byte, width, height int) (*image.RGBA, error) {
	bpc := pdf.DictInt(dict, "BitsPerComponent", 8)
	cs := resolveImageColorSpace(dict, resources)
	ncomp := pdf.ColorSpaceComponents(cs)
	isMask := dict.Entries["ImageMask"] == pdf.PDFBoolean(true)
	if isMask {
		ncomp = 1
		bpc = 1
	}

	decode := imageDecodeArray(dict, cs, ncomp, bpc)

	if !isMask && bpc == 8 && isIdentityDecode(decode, ncomp) {
		if img, ok := unpack8Direct(fastColourModel(cs), ncomp, data, width, height); ok {
			return img, nil
		}
	}

	maxVal := float64((uint64(1) << bpc) - 1)
	rowBits := width * ncomp * bpc
	rowBytes := (rowBits + 7) / 8

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	comps := make([]float64, ncomp)
	for y := 0; y < height; y++ {
		rowOffset := y * rowBytes * 8
		off := img.PixOffset(0, y)
		for x := 0; x < width; x++ {
			for c := 0; c < ncomp; c++ {
				bitOffset := rowOffset + (x*ncomp+c)*bpc
				raw := float64(pdf.ReadBits(data, bitOffset, bpc))
				comps[c] = decode[2*c] + (raw/maxVal)*(decode[2*c+1]-decode[2*c])
			}

			if isMask {
				// A non-zero mask sample is fully transparent; zero is opaque black.
				if comps[0] != 0 {
					img.Pix[off], img.Pix[off+1], img.Pix[off+2], img.Pix[off+3] = 0, 0, 0, 0
				} else {
					img.Pix[off], img.Pix[off+1], img.Pix[off+2], img.Pix[off+3] = 0, 0, 0, 255
				}
			} else {
				r, g, b := pdf.ResolveColor(cs, comps, resources)
				storeRGBA64(img.Pix, off, r, g, b, 1)
			}
			off += 4
		}
	}
	return img, nil
}

// isIdentityDecode reports whether decode is the natural [0,1] range for every
// one of ncomp components, i.e. the sample maps to its colour value unchanged.
func isIdentityDecode(decode []float64, ncomp int) bool {
	if len(decode) != 2*ncomp {
		return false
	}
	for c := range ncomp {
		if decode[2*c] != 0 || decode[2*c+1] != 1 {
			return false
		}
	}
	return true
}

// fastColourModel classifies cs as the RGB- or Gray-identity family the 8-bpc
// fast path can copy verbatim (DeviceRGB/CalRGB/ICCBased N=3 and the Gray
// equivalents), or "" for any space needing real ResolveColor evaluation.
func fastColourModel(cs pdf.PDFValue) string {
	switch v := cs.(type) {
	case pdf.PDFName:
		switch v.Value {
		case "DeviceRGB", "RGB", "CalRGB":
			return "rgb"
		case "DeviceGray", "G", "CalGray":
			return "gray"
		}
	case pdf.PDFArray:
		if len(v) == 0 {
			return ""
		}
		head, ok := v[0].(pdf.PDFName)
		if !ok {
			return ""
		}
		switch head.Value {
		case "DeviceRGB", "CalRGB":
			return "rgb"
		case "DeviceGray", "CalGray":
			return "gray"
		case "ICCBased":
			if len(v) >= 2 {
				if stream, ok := v[1].(pdf.PDFDict); ok {
					switch pdf.DictInt(stream, "N", 0) {
					case 3:
						return "rgb"
					case 1:
						return "gray"
					}
				}
			}
		}
	}
	return ""
}

// unpack8Direct copies 8-bpc DeviceRGB (3 source bytes per pixel) or DeviceGray
// (1 byte broadcast to R=G=B) samples straight into an opaque RGBA buffer. ok is
// false when the colour model isn't fast-pathable or data is short, so the
// caller falls back to the general sample loop.
func unpack8Direct(model string, ncomp int, data []byte, width, height int) (*image.RGBA, bool) {
	rowBytes := width * ncomp
	if len(data) < height*rowBytes {
		return nil, false
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	switch {
	case model == "rgb" && ncomp == 3:
		for y := range height {
			src := data[y*rowBytes:]
			off := img.PixOffset(0, y)
			for x := range width {
				s := x * 3
				img.Pix[off], img.Pix[off+1], img.Pix[off+2], img.Pix[off+3] = src[s], src[s+1], src[s+2], 255
				off += 4
			}
		}
		return img, true
	case model == "gray" && ncomp == 1:
		for y := range height {
			src := data[y*rowBytes:]
			off := img.PixOffset(0, y)
			for x := range width {
				v := src[x]
				img.Pix[off], img.Pix[off+1], img.Pix[off+2], img.Pix[off+3] = v, v, v, 255
				off += 4
			}
		}
		return img, true
	}
	return nil, false
}

// storeRGBA64 writes the float colour {r,g,b,a} into a Pix slice.
func storeRGBA64(pix []byte, off int, r, g, b, a float64) {
	ca := convU16(a)
	pix[off] = uint8((convU16(r) * ca / 0xFFFF) >> 8)
	pix[off+1] = uint8((convU16(g) * ca / 0xFFFF) >> 8)
	pix[off+2] = uint8((convU16(b) * ca / 0xFFFF) >> 8)
	pix[off+3] = uint8(ca >> 8)
}

// convU16 clamps v to [0,1] and scales to the 16-bit range, matching
// colorRGBA64.RGBA's conv closure.
func convU16(v float64) uint32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 65535
	}
	return uint32(v * 65535)
}

// colorRGBA64 implements color.Color over float64 [0,1] components.
type colorRGBA64 struct{ r, g, b, a float64 }

func (c colorRGBA64) RGBA() (r, g, b, a uint32) {
	conv := func(v float64) uint32 {
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		x := uint32(v * 65535)
		return x
	}
	return conv(c.r) * conv(c.a) / 0xFFFF, conv(c.g) * conv(c.a) / 0xFFFF, conv(c.b) * conv(c.a) / 0xFFFF, conv(c.a)
}

// resolveImageColorSpace reads an image dict's /ColorSpace, resolving a
// named reference against resources if needed. ImageMask images have an
// implicit DeviceGray space (the sample is opacity, not colour).
func resolveImageColorSpace(dict pdf.PDFDict, resources pdf.PDFDict) pdf.PDFValue {
	if dict.Entries["ImageMask"] == pdf.PDFBoolean(true) {
		return pdf.PDFName{Value: "DeviceGray"}
	}
	cs := dict.Entries["ColorSpace"]
	if name, ok := cs.(pdf.PDFName); ok {
		if named, ok := pdf.LookupNamedColorSpace(name.Value, resources); ok {
			return named
		}
	}
	return cs
}

// imageDecodeArray returns the effective per-component Decode range,
// defaulting to the colour space's natural range when /Decode is absent.
func imageDecodeArray(dict pdf.PDFDict, cs pdf.PDFValue, ncomp, bpc int) []float64 {
	if arr, err := pdf.FloatArray(dict.Entries["Decode"]); err == nil && len(arr) == 2*ncomp {
		return arr
	}
	if _, isIndexed := indexedHead(cs); isIndexed {
		return []float64{0, float64((uint64(1) << bpc) - 1)}
	}
	out := make([]float64, 2*ncomp)
	for i := 0; i < ncomp; i++ {
		out[2*i], out[2*i+1] = 0, 1
	}
	return out
}

func indexedHead(cs pdf.PDFValue) (pdf.PDFArray, bool) {
	arr, ok := cs.(pdf.PDFArray)
	if !ok || len(arr) == 0 {
		return nil, false
	}
	name, ok := arr[0].(pdf.PDFName)
	return arr, ok && (name.Value == "Indexed" || name.Value == "I")
}

// decodeCCITTImage decodes a CCITTFaxDecode image into RGBA, running any
// preceding ASCII filters, decoding the fax bitstream (ccitt.go) into packed
// 1-bpc samples and resolving them through the normal sample path.
func decodeCCITTImage(dict pdf.PDFDict, resources pdf.PDFDict, width, height int) (*image.RGBA, error) {
	data, err := ccittEncodedBytes(dict)
	if err != nil {
		return nil, err
	}
	parms := pdf.StreamDecodeParms(dict)
	p := pdf.CCITTParams{
		Columns:   pdf.DictInt(parms, "Columns", 1728),
		Rows:      pdf.DictInt(parms, "Rows", 0),
		K:         pdf.DictInt(parms, "K", 0),
		ByteAlign: parms.Entries["EncodedByteAlign"] == pdf.PDFBoolean(true),
		BlackIs1:  parms.Entries["BlackIs1"] == pdf.PDFBoolean(true),
	}
	if p.Columns <= 0 {
		p.Columns = width
	}
	if p.Rows <= 0 {
		p.Rows = height
	}
	raw, err := pdf.DecodeCCITT(data, p)
	if err != nil {
		return nil, err
	}
	return unpackSamplesToRGBA(dict, resources, raw, width, height)
}

// ccittEncodedBytes returns the bytes feeding the CCITTFaxDecode filter,
// undoing any ASCII filters applied before it in the chain.
func ccittEncodedBytes(dict pdf.PDFDict) ([]byte, error) {
	data := dict.RawStream
	for _, f := range pdf.FilterNames(dict.Entries["Filter"]) {
		switch f {
		case "ASCIIHexDecode", "AHx":
			out, err := pdf.DecodeASCIIHex(data)
			if err != nil {
				return nil, err
			}
			data = out
		case "ASCII85Decode", "A85":
			out, err := pdf.DecodeASCII85(data)
			if err != nil {
				return nil, err
			}
			data = out
		case "CCITTFaxDecode", "CCF":
			return data, nil
		default:
			return nil, fmt.Errorf("ccitt: unexpected filter %q before CCITTFaxDecode", f)
		}
	}
	return data, nil
}

// decodeJPEGImage decodes a DCTDecode image stream using the standard
// library's JPEG decoder.
func decodeJPEGImage(dict pdf.PDFDict) (*image.RGBA, error) {
	img, err := jpeg.Decode(bytes.NewReader(dict.RawStream))
	if err != nil {
		return nil, err
	}
	bounds := img.Bounds()
	out := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			out.Set(x, y, img.At(x, y))
		}
	}
	return out, nil
}

// placeholderImage paints a flat mid-gray rectangle, the fallback used for
// image codecs this package cannot decode.
func placeholderImage(width, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, colorRGBA64{0.5, 0.5, 0.5, 1})
		}
	}
	return img
}
