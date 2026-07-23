package pdf

import "errors"

// FilterKind identifies a stream filter independently of which of its two
// spellings -- the long name or the inline-image abbreviation -- a file used.
type FilterKind int

const (
	FilterNone FilterKind = iota
	FilterFlate
	FilterLZW
	FilterASCIIHex
	FilterASCII85
	FilterRunLength
	FilterCCITT
	FilterDCT
	FilterJBIG2
	FilterJPX
	FilterCrypt
)

// FilterInfo describes one filter's decoding properties.
type FilterInfo struct {
	Kind FilterKind
	// Name is the canonical long form, e.g. "FlateDecode".
	Name string
	// Abbrev is the inline-image short form, e.g. "Fl". Empty when the filter
	// has no abbreviation.
	Abbrev string
	// Image marks a terminal image codec (CCITT/DCT/JBIG2/JPX). Its output is
	// a raster, not bytes, so the decode chain stops there and hands the
	// caller the still-encoded input; see DecodedStream.
	Image bool
	// Predictor marks a filter whose DecodeParms may carry /Predictor. Only
	// Flate and LZW may (ISO 32000-1 Table 8).
	Predictor bool
}

// filterTable is the single place filter name literals live. Every other
// filter test in the codebase goes through LookupFilter or HasFilter.
var filterTable = []FilterInfo{
	{Kind: FilterFlate, Name: "FlateDecode", Abbrev: "Fl", Predictor: true},
	{Kind: FilterLZW, Name: "LZWDecode", Abbrev: "LZW", Predictor: true},
	{Kind: FilterASCIIHex, Name: "ASCIIHexDecode", Abbrev: "AHx"},
	{Kind: FilterASCII85, Name: "ASCII85Decode", Abbrev: "A85"},
	{Kind: FilterRunLength, Name: "RunLengthDecode", Abbrev: "RL"},
	{Kind: FilterCCITT, Name: "CCITTFaxDecode", Abbrev: "CCF", Image: true},
	{Kind: FilterDCT, Name: "DCTDecode", Abbrev: "DCT", Image: true},
	{Kind: FilterJBIG2, Name: "JBIG2Decode", Image: true},
	{Kind: FilterJPX, Name: "JPXDecode", Image: true},
	{Kind: FilterCrypt, Name: "Crypt"},
}

var filtersByName = func() map[string]FilterInfo {
	m := make(map[string]FilterInfo, len(filterTable)*2)
	for _, f := range filterTable {
		m[f.Name] = f
		if f.Abbrev != "" {
			m[f.Abbrev] = f
		}
	}
	return m
}()

// LookupFilter resolves a /Filter name given in either spelling.
func LookupFilter(name string) (FilterInfo, bool) {
	f, ok := filtersByName[name]
	return f, ok
}

// HasFilter reports whether filter -- a stream's /Filter entry, name or array
// -- includes a filter of kind k, in either spelling.
func HasFilter(filter PDFValue, k FilterKind) bool {
	for _, name := range FilterNames(filter) {
		if info, ok := LookupFilter(name); ok && info.Kind == k {
			return true
		}
	}
	return false
}

// DecodedStream is the outcome of running a stream's filter chain.
//
// Data holds the fully decoded bytes when Image is nil. When Image is non-nil
// the chain stopped at a terminal image codec: Data then holds the bytes
// feeding that codec (every preceding ASCII/Flate/LZW filter already undone)
// and ImageParms its decode parameters. That state means "not decodable to
// bytes", which is a different answer from "broken" -- broken is an error
// return. Keeping the two apart is what lets a caller report a damaged stream
// as an issue without flagging every JPEG in the document.
type DecodedStream struct {
	Data       []byte
	Image      *FilterInfo
	ImageParms PDFDict
}

// IsImage reports whether the chain ended in an image codec.
func (s DecodedStream) IsImage() bool { return s.Image != nil }

var (
	// ErrEncodedImage is returned by DecodeStream when a stream's filter chain
	// ends in an image codec. The stream is well-formed; it simply has no byte
	// representation. Callers that can handle raster payloads call
	// DecodeStreamFull and inspect DecodedStream.Image instead.
	ErrEncodedImage = errors.New("pdf: stream ends in an image filter")

	ErrUnsupportedFilter    = errors.New("pdf: unsupported filter")
	ErrUnsupportedPredictor = errors.New("pdf: unsupported predictor")
	ErrNotAStream           = errors.New("pdf: object is not a stream")
	ErrOutputTooLarge       = errors.New("pdf: decoded output exceeds size limit")

	// ErrEncrypted reports an encryption the standard security handler does not
	// implement; ErrPasswordRequired reports a correct password is needed.
	ErrEncrypted        = errors.New("pdf: document is encrypted")
	ErrPasswordRequired = errors.New("pdf: correct password required to decrypt")

	// ErrNotPDF reports that the input is not a PDF (no %PDF- header); ErrDamaged
	// reports a PDF whose cross-reference or trailer structure could not be
	// parsed. Both wrap the specific cause and are matchable with errors.Is.
	ErrNotPDF  = errors.New("pdf: not a PDF document")
	ErrDamaged = errors.New("pdf: damaged document structure")

	// ErrUnresolvableGraph reports that the object graph could not be resolved
	// even with per-object degradation, so no converted output can be produced.
	ErrUnresolvableGraph = errors.New("pdf: object graph could not be resolved")
)

// DecodeOptions supplies context a stream dictionary alone does not carry.
// The zero value is the spec-literal chain, which is what DecodeStream uses.
type DecodeOptions struct {
	// Columns, Colors and BitsPerComponent override the predictor defaults
	// used when DecodeParms omits them. Zero means the spec default (1, 1, 8).
	Columns          int
	Colors           int
	BitsPerComponent int

	// LenientPredictor treats an unrecognised /Predictor value as PNG rather
	// than an error -- the rasterizer's historical behaviour, kept so a weird
	// image renders as pixels instead of failing the page.
	LenientPredictor bool
}

// ImageDecodeOptions returns the DecodeOptions an Image XObject dictionary
// implies: for image streams /DecodeParms /Columns defaults to the image's
// /Width and /BitsPerComponent to the image's own (ISO 32000-1 Table 10),
// rather than to the 1/8 the spec uses elsewhere.
func ImageDecodeOptions(dict PDFDict) DecodeOptions {
	return DecodeOptions{
		Columns:          DictInt(dict, "Width", 1),
		BitsPerComponent: DictInt(dict, "BitsPerComponent", 8),
		LenientPredictor: true,
	}
}
