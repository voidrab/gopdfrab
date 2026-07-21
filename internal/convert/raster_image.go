package convert

import (
	"bytes"
	"image"
	"image/draw"
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

	s, err := pdf.DecodeStreamFull(dict, pdf.ImageDecodeOptions(dict))
	if err != nil {
		return nil, err
	}
	if s.IsImage() {
		switch s.Image.Kind {
		case pdf.FilterDCT:
			return decodeJPEGImage(s.Data)
		case pdf.FilterCCITT:
			if img, err := decodeCCITTImage(dict, resources, s, width, height); err == nil {
				return img, nil
			}
			return placeholderImage(width, height), nil
		default: // JBIG2, JPX
			return placeholderImage(width, height), nil
		}
	}
	return unpackSamplesToRGBA(dict, resources, s.Data, width, height)
}

type imageDecodeError string

func (e imageDecodeError) Error() string { return string(e) }

const errInvalidImageDims = imageDecodeError("raster_image: invalid /Width or /Height")

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

	if !isMask && isIdentityDecode(decode, ncomp) {
		model := fastColourModel(cs)
		if bpc == 8 {
			if img, ok := unpack8Direct(model, ncomp, data, width, height); ok {
				return img, nil
			}
		} else if img, ok := unpackDeviceDirect(model, ncomp, bpc, data, width, height); ok {
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

// unpackDeviceDirect reads 1/2/4/16-bpc DeviceRGB or DeviceGray samples (any
// bit depth other than the byte-aligned 8-bpc unpack8Direct handles) and
// scales them straight to 8-bit RGBA, skipping the general per-pixel
// ResolveColor path. ok is false when the colour model isn't fast-pathable.
func unpackDeviceDirect(model string, ncomp, bpc int, data []byte, width, height int) (*image.RGBA, bool) {
	maxVal := uint64(1)<<bpc - 1
	rowBytes := (width*ncomp*bpc + 7) / 8
	scale8 := func(raw uint64) uint8 { return uint8(raw * 255 / maxVal) }

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	switch {
	case model == "rgb" && ncomp == 3:
		for y := 0; y < height; y++ {
			rowOffset := y * rowBytes * 8
			off := img.PixOffset(0, y)
			for x := 0; x < width; x++ {
				base := rowOffset + x*3*bpc
				img.Pix[off] = scale8(pdf.ReadBits(data, base, bpc))
				img.Pix[off+1] = scale8(pdf.ReadBits(data, base+bpc, bpc))
				img.Pix[off+2] = scale8(pdf.ReadBits(data, base+2*bpc, bpc))
				img.Pix[off+3] = 255
				off += 4
			}
		}
		return img, true
	case model == "gray" && ncomp == 1:
		for y := 0; y < height; y++ {
			rowOffset := y * rowBytes * 8
			off := img.PixOffset(0, y)
			for x := 0; x < width; x++ {
				v := scale8(pdf.ReadBits(data, rowOffset+x*bpc, bpc))
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

// decodeCCITTImage decodes a CCITTFaxDecode image into RGBA. s carries the
// bytes feeding the fax codec (preceding ASCII filters already undone by the
// decode chain) and its decode parameters; the bitstream is decoded (ccitt.go)
// into packed 1-bpc samples and resolved through the normal sample path.
func decodeCCITTImage(dict pdf.PDFDict, resources pdf.PDFDict, s pdf.DecodedStream, width, height int) (*image.RGBA, error) {
	parms := s.ImageParms
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
	raw, err := pdf.DecodeCCITT(s.Data, p)
	if err != nil {
		return nil, err
	}
	return unpackSamplesToRGBA(dict, resources, raw, width, height)
}

// decodeJPEGImage decodes DCTDecode image data using the standard library's
// JPEG decoder. data is the codec's input, so any ASCII filters wrapping it
// have already been undone by the decode chain.
func decodeJPEGImage(data []byte) (*image.RGBA, error) {
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	bounds := img.Bounds()
	out := image.NewRGBA(bounds)
	// draw.Src is defined as dst.Set(src.At(...)) per pixel, so this is the
	// former nested Set/At loop minus the per-pixel color.Color boxing (the
	// stdlib has typed fast paths for the JPEG decoder's YCbCr/Gray images).
	draw.Draw(out, bounds, img, bounds.Min, draw.Src)
	return out, nil
}

// placeholderImage paints a flat mid-gray rectangle, the fallback used for
// image codecs this package cannot decode.
func placeholderImage(width, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	if width <= 0 || height <= 0 {
		return img
	}
	// Every pixel is the same gray: set the first, then fill the rest with
	// doubling copies instead of a per-pixel Set.
	img.Set(0, 0, colorRGBA64{0.5, 0.5, 0.5, 1})
	pix := img.Pix
	for filled := 4; filled < len(pix); filled *= 2 {
		copy(pix[filled:], pix[:filled])
	}
	return img
}
