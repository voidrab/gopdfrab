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
	if dict.Entries["ImageMask"] == pdf.PDFBoolean(true) {
		ncomp = 1
		bpc = 1
	}

	decode := imageDecodeArray(dict, cs, ncomp, bpc)
	maxVal := float64((uint64(1) << bpc) - 1)
	rowBits := width * ncomp * bpc
	rowBytes := (rowBits + 7) / 8

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	comps := make([]float64, ncomp)
	for y := 0; y < height; y++ {
		rowOffset := y * rowBytes * 8
		for x := 0; x < width; x++ {
			for c := 0; c < ncomp; c++ {
				bitOffset := rowOffset + (x*ncomp+c)*bpc
				raw := float64(pdf.ReadBits(data, bitOffset, bpc))
				comps[c] = decode[2*c] + (raw/maxVal)*(decode[2*c+1]-decode[2*c])
			}

			var r, g, b, a float64 = 0, 0, 0, 1
			if dict.Entries["ImageMask"] == pdf.PDFBoolean(true) {
				if comps[0] != 0 {
					a = 0
				}
			} else {
				r, g, b = pdf.ResolveColor(cs, comps, resources)
			}
			img.Set(x, y, colorRGBA64{r, g, b, a})
		}
	}
	return img, nil
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
