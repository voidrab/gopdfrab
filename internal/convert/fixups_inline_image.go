package convert

import (
	"fmt"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// This file fixes violations specific to an inline image's own parameters
// (the BI...ID dict), reusing the walkContentStreams/contentOpRewriter
// plumbing from fixups_content.go: a non-standard inline /Intent (folded
// into contentLimitsFixer, which already owns RenderingIntent), a true
// /Interpolate (folded into imageMetadataFixer, which already owns
// ImageInterpolate for the dict-level Image XObject case -- a Check can
// only have one registered Fixer), and the LZW inline-image filter
// (registered here, since no other Fixer claims it).
//
// Each rewriter bails out (returns the op unchanged) rather than guessing
// if anything looks unexpected -- a missing pdf.InlineImageRaw operand, or (for
// LZW) a /DP-/DecodeParms predictor this package doesn't replicate for
// inline images -- leaving the violation as a residual rather than risking
// a corrupted image.

// removeInlineImageKeys drops every (key, value) pair whose key matches one
// of keys from an inline image's params.
func removeInlineImageKeys(params []pdf.PDFValue, keys ...string) []pdf.PDFValue {
	drop := make(map[string]bool, len(keys))
	for _, k := range keys {
		drop[k] = true
	}
	out := make([]pdf.PDFValue, 0, len(params))
	for i := 0; i+1 < len(params); i += 2 {
		if name, ok := params[i].(pdf.PDFName); ok && drop[name.Value] {
			continue
		}
		out = append(out, params[i], params[i+1])
	}
	return out
}

// fixInlineImageRenderingIntent flips a non-standard inline-image /Intent
// to /RelativeColorimetric, mirroring checkInlineImageOther's Intent case
// (checks_content.go) in reverse. ok is false if operands isn't an
// INLINEIMAGE op's operands or nothing needed fixing.
func fixInlineImageRenderingIntent(operands []pdf.PDFValue, spec *pdfa.Spec) (fixed []pdf.PDFValue, ok bool) {
	params, raw, ok := pdf.InlineImageRawOperand(operands)
	if !ok {
		return operands, false
	}
	fixedParams := append([]pdf.PDFValue{}, params...)
	changed := false
	for i := 0; i+1 < len(fixedParams); i += 2 {
		key, ok := fixedParams[i].(pdf.PDFName)
		if !ok || key.Value != "Intent" {
			continue
		}
		if name, ok := fixedParams[i+1].(pdf.PDFName); ok && !spec.RenderingIntents.Has(name.Value) {
			fixedParams[i+1] = pdf.PDFName{Value: "RelativeColorimetric"}
			changed = true
		}
	}
	if !changed {
		return operands, false
	}
	newBytes, err := writer.BuildInlineImageBytes(fixedParams, raw.Data)
	if err != nil {
		return operands, false
	}
	return append(fixedParams, pdf.InlineImageRaw{Bytes: newBytes, Data: raw.Data}), true
}

// fixInlineImageInterpolate flips a true inline-image /I or /Interpolate to
// false, mirroring checkInlineImageOther's Interpolate case
// (checks_content.go) in reverse. Used by imageMetadataFixer, which already
// owns Checks.Image.ImageInterpolate for the dict-level Image XObject case.
func fixInlineImageInterpolate(op string, operands []pdf.PDFValue, changed *bool) (writer.ContentOp, bool) {
	if op != "INLINEIMAGE" {
		return writer.ContentOp{Op: op, Operands: operands}, true
	}
	params, raw, ok := pdf.InlineImageRawOperand(operands)
	if !ok {
		return writer.ContentOp{Op: op, Operands: operands}, true
	}
	fixedParams := append([]pdf.PDFValue{}, params...)
	flipped := false
	for i := 0; i+1 < len(fixedParams); i += 2 {
		key, ok := fixedParams[i].(pdf.PDFName)
		if !ok || (key.Value != "I" && key.Value != "Interpolate") {
			continue
		}
		if b, ok := fixedParams[i+1].(pdf.PDFBoolean); ok && bool(b) {
			fixedParams[i+1] = pdf.PDFBoolean(false)
			flipped = true
		}
	}
	if !flipped {
		return writer.ContentOp{Op: op, Operands: operands}, true
	}
	newBytes, err := writer.BuildInlineImageBytes(fixedParams, raw.Data)
	if err != nil {
		return writer.ContentOp{Op: op, Operands: operands}, true
	}
	*changed = true
	return writer.ContentOp{Op: op, Operands: append(fixedParams, pdf.InlineImageRaw{Bytes: newBytes, Data: raw.Data})}, true
}

// inlineImageLZWFixer remediates Checks.Structure.InlineImageLZWFilter by
// decoding an inline image's LZW-filtered data and re-encoding it as Flate,
// mirroring checkInlineImageFilter (checks_content.go) in reverse.
type inlineImageLZWFixer struct{}

func (inlineImageLZWFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.InlineImageLZWFilter
}

func (inlineImageLZWFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	return walkContentStreams(trailer, fixInlineImageLZW), nil
}

// fixInlineImageLZW re-encodes an inline image's data from LZW to Flate.
// /F or /Filter may be a single name or an array, the way the verifier
// (checkInlineImageFilter) reads it. Every filter up to and including the last
// LZW is decoded, each with its positionally matched /DP or /DecodeParms entry
// and its predictor undone, and the result is re-encoded as one Flate stage
// that carries no predictor.
//
// Filters after the LZW stay as they were, with their own parameters: a chain
// such as [/LZW /DCT] becomes [/Fl /DCT]. Decoding past an image codec (DCT,
// CCITT) would mean rendering the picture, and re-encoding it would be lossy
// for DCT; the codec's input, the bytes the LZW stage produced, is exactly
// what the Flate stage has to reproduce, so nothing beyond the LZW is touched.
//
// It bails out (leaving the op unchanged) on any unexpected shape -- more than
// one filter key, a non-name inside the filter array, a filter it cannot decode
// -- rather than risking a corrupted image.
func fixInlineImageLZW(op string, operands []pdf.PDFValue, changed *bool) (writer.ContentOp, bool) {
	unchanged := writer.ContentOp{Op: op, Operands: operands}
	if op != "INLINEIMAGE" {
		return unchanged, true
	}
	params, raw, ok := pdf.InlineImageRawOperand(operands)
	if !ok {
		return unchanged, true
	}

	filterKey := -1
	for i := 0; i+1 < len(params); i += 2 {
		if key, ok := params[i].(pdf.PDFName); ok && (key.Value == "F" || key.Value == "Filter") {
			if filterKey >= 0 {
				return unchanged, true
			}
			filterKey = i
		}
	}
	if filterKey < 0 {
		return unchanged, true
	}
	names, ok := inlineImageFilterChain(params[filterKey+1])
	if !ok {
		return unchanged, true
	}
	lastLZW := -1
	for i, n := range names {
		if info, known := pdf.LookupFilter(n.Value); known && info.Kind == pdf.FilterLZW {
			lastLZW = i
		}
	}
	if lastLZW < 0 {
		return unchanged, true
	}

	// Rebuild the filter chain as a stream dictionary so the shared decoder
	// aligns each filter with its parameters; the head of it is decoded, the
	// tail merely carried over.
	chain := pdf.NewPDFDict()
	chain.Entries.Set("Filter", params[filterKey+1])
	for i := 0; i+1 < len(params); i += 2 {
		if key, ok := params[i].(pdf.PDFName); ok && (key.Value == "DP" || key.Value == "DecodeParms") {
			chain.Entries.Set(key.Value, params[i+1])
		}
	}
	head := pdf.NewPDFDict()
	head.SetStream(raw.Data)
	headFilter := make(pdf.PDFArray, 0, lastLZW+1)
	headParms := make(pdf.PDFArray, 0, lastLZW+1)
	for i := 0; i <= lastLZW; i++ {
		headFilter = append(headFilter, names[i])
		headParms = append(headParms, pdf.FilterDecodeParms(chain, i, len(names)))
	}
	head.Entries.Set("Filter", headFilter)
	head.Entries.Set("DecodeParms", headParms)
	decoded, err := pdf.DecodeStreamFull(head, pdf.DecodeOptions{})
	if err != nil {
		return unchanged, true
	}

	newFilter := pdf.PDFValue(pdf.PDFName{Value: "Fl"})
	var tailParms pdf.PDFArray
	hasTailParms := false
	if tail := names[lastLZW+1:]; len(tail) > 0 {
		filters := pdf.PDFArray{pdf.PDFName{Value: "Fl"}}
		tailParms = pdf.PDFArray{pdf.NewPDFDict()}
		for i, n := range tail {
			filters = append(filters, n)
			p := pdf.FilterDecodeParms(chain, lastLZW+1+i, len(names))
			hasTailParms = hasTailParms || p.Entries.Len() > 0
			tailParms = append(tailParms, p)
		}
		newFilter = filters
	}

	fixedParams := removeInlineImageKeys(params, "DP", "DecodeParms")
	for i := 0; i+1 < len(fixedParams); i += 2 {
		if key, ok := fixedParams[i].(pdf.PDFName); ok && (key.Value == "F" || key.Value == "Filter") {
			fixedParams[i+1] = newFilter
		}
	}
	if hasTailParms {
		fixedParams = append(fixedParams, pdf.PDFName{Value: "DP"}, tailParms)
	}
	compressed := writer.DeflateZlib(decoded.Data)
	newBytes, err := writer.BuildInlineImageBytes(fixedParams, compressed)
	if err != nil {
		return unchanged, true
	}

	*changed = true
	return writer.ContentOp{Op: op, Operands: append(fixedParams, pdf.InlineImageRaw{Bytes: newBytes, Data: compressed})}, true
}

// inlineImageFilterChain returns the names in an inline image's /F or /Filter
// value, which is a name or an array of names. ok is false for any other
// shape, including an array holding a non-name: pdf.FilterNames would skip it
// and shift every later filter off its /DP entry.
func inlineImageFilterChain(v pdf.PDFValue) (names []pdf.PDFName, ok bool) {
	switch f := v.(type) {
	case pdf.PDFName:
		return []pdf.PDFName{f}, true
	case pdf.PDFArray:
		for _, item := range f {
			n, isName := item.(pdf.PDFName)
			if !isName {
				return nil, false
			}
			names = append(names, n)
		}
		return names, true
	}
	return nil, false
}

// inlineImageFilterFixer remediates Checks.Structure.InlineImageFilterNotAllowed
// by moving each inline image whose filters a stream may use, though an
// inline image may not (JBIG2Decode, JPXDecode, an Identity Crypt filter),
// into an image XObject that the content draws in its place with the Do
// operator. An image XObject paints the same unit square of user space as an
// inline image, so the page looks the same. An inline image with a filter no
// stream may use either is left as it is.
type inlineImageFilterFixer struct{ spec *pdfa.Spec }

func (inlineImageFilterFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.InlineImageFilterNotAllowed
}

func (f inlineImageFilterFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	if f.spec.InlineImageFilters.Len() == 0 {
		return false, nil
	}
	changed := false
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) bool {
		fixedSelf := false
		pdf.ContentRoots(d, func(r pdf.ContentRoot) {
			h := inlineImageHoist{spec: f.spec, holder: d, resources: r.Resources}
			fixed, ok := rewriteContentStreamDict(r.Stream, h.rewrite)
			if !ok {
				return
			}
			r.Replace(fixed)
			changed = true
			if r.Kind == pdf.ContentPattern || r.Kind == pdf.ContentForm {
				fixedSelf = true
			}
		})
		return fixedSelf
	})
	return changed, nil
}

// inlineImageHoist moves the inline images of one content stream into image
// XObjects of the stream's resources. holder is the dictionary that holds
// the stream and, when the stream has no resources yet, receives them.
type inlineImageHoist struct {
	spec      *pdfa.Spec
	holder    pdf.PDFDict
	resources pdf.PDFDict
}

// inlineImageKeys are the full names of the inline image keys ISO 32000-1
// Table 93 abbreviates, and of the L (Length) some writers add.
var inlineImageKeys = map[string]string{
	"BPC": "BitsPerComponent", "CS": "ColorSpace", "D": "Decode", "DP": "DecodeParms",
	"F": "Filter", "H": "Height", "IM": "ImageMask", "I": "Interpolate", "W": "Width",
	"L": "Length",
}

// inlineColourSpaces are the full names of the colour space names ISO
// 32000-1 Table 94 abbreviates.
var inlineColourSpaces = map[string]string{
	"G": "DeviceGray", "RGB": "DeviceRGB", "CMYK": "DeviceCMYK", "I": "Indexed",
}

func (h *inlineImageHoist) rewrite(op string, operands []pdf.PDFValue, changed *bool) (writer.ContentOp, bool) {
	unchanged := writer.ContentOp{Op: op, Operands: operands}
	if op != "INLINEIMAGE" {
		return unchanged, true
	}
	params, raw, ok := pdf.InlineImageRawOperand(operands)
	if !ok {
		return unchanged, true
	}
	image := pdf.NewStreamDict(nil, raw.Data)
	image.Entries.Set("Type", pdf.PDFName{Value: "XObject"})
	image.Entries.Set("Subtype", pdf.PDFName{Value: "Image"})
	for i := 0; i+1 < len(params); i += 2 {
		key, ok := params[i].(pdf.PDFName)
		if !ok {
			return unchanged, true
		}
		name := key.Value
		if full, ok := inlineImageKeys[name]; ok {
			name = full
		}
		value := params[i+1]
		switch name {
		case "Filter":
			value = fullFilterNames(value)
		case "ColorSpace":
			if value, ok = h.colourSpace(value); !ok {
				return unchanged, true
			}
		case "Length":
			// The writer sets the Length of the image XObject itself, so the
			// inline image's is dropped.
			continue
		}
		image.Entries.Set(name, value)
	}
	if !h.needsHoisting(image) {
		return unchanged, true
	}
	*changed = true
	return writer.ContentOp{Op: "Do", Operands: []pdf.PDFValue{h.addXObject(image)}}, true
}

// needsHoisting reports whether an inline image, as an image XObject, names
// a filter an inline image may not use while every one of its filters is one
// a stream may use.
func (h *inlineImageHoist) needsHoisting(image pdf.PDFDict) bool {
	names := pdf.FilterNames(image.Entries.Get("Filter"))
	inlineOnly := false
	for i, name := range names {
		if !h.spec.StreamFilters.Has(name) {
			return false
		}
		if n := pdf.FilterDecodeParms(image, i, len(names)).Entries.Get("Name"); name == "Crypt" && n != nil && n != (pdf.PDFName{Value: "Identity"}) {
			return false
		}
		if !h.spec.InlineImageFilters.Has(name) {
			inlineOnly = true
		}
	}
	return inlineOnly
}

// fullFilterNames returns an inline image's filter, a name or an array of
// names, with each abbreviation replaced by the filter's full name.
func fullFilterNames(filter pdf.PDFValue) pdf.PDFValue {
	full := func(v pdf.PDFValue) pdf.PDFValue {
		if n, ok := v.(pdf.PDFName); ok {
			if info, ok := pdf.LookupFilter(n.Value); ok {
				return pdf.PDFName{Value: info.Name}
			}
		}
		return v
	}
	if arr, ok := filter.(pdf.PDFArray); ok {
		out := make(pdf.PDFArray, len(arr))
		for i, v := range arr {
			out[i] = full(v)
		}
		return out
	}
	return full(filter)
}

// colourSpace returns an inline image's colour space as an image XObject
// names it: an abbreviation is expanded, and a name the resources define is
// replaced by the colour space it stands for. ok is false for a name that is
// neither, which an image XObject could not resolve.
func (h *inlineImageHoist) colourSpace(cs pdf.PDFValue) (_ pdf.PDFValue, ok bool) {
	switch v := cs.(type) {
	case pdf.PDFName:
		if full, ok := inlineColourSpaces[v.Value]; ok {
			return pdf.PDFName{Value: full}, true
		}
		switch v.Value {
		case "DeviceGray", "DeviceRGB", "DeviceCMYK":
			return v, true
		}
		spaces, _ := h.resources.Entries.Get("ColorSpace").(pdf.PDFDict)
		if named := spaces.Entries.Get(v.Value); named != nil {
			return named, true
		}
		return nil, false
	case pdf.PDFArray:
		if len(v) < 2 {
			return nil, false
		}
		out := append(pdf.PDFArray{}, v...)
		if head, ok := v[0].(pdf.PDFName); ok && head.Value == "I" {
			out[0] = pdf.PDFName{Value: "Indexed"}
		}
		base, ok := h.colourSpace(v[1])
		if !ok {
			return nil, false
		}
		out[1] = base
		return out, true
	}
	return nil, false
}

// addXObject adds image to the stream's XObject resources under a name not
// yet in use, and returns that name. A stream without resources gets them.
func (h *inlineImageHoist) addXObject(image pdf.PDFDict) pdf.PDFName {
	if h.resources.Entries == nil {
		if own, ok := h.holder.Entries.Get("Resources").(pdf.PDFDict); ok {
			h.resources = own
		} else {
			h.resources = pdf.NewPDFDict()
			h.holder.Entries.Set("Resources", h.resources)
		}
	}
	xobjects, ok := h.resources.Entries.Get("XObject").(pdf.PDFDict)
	if !ok {
		xobjects = pdf.NewPDFDict()
		h.resources.Entries.Set("XObject", xobjects)
	}
	for i := 1; ; i++ {
		name := fmt.Sprintf("InlineIm%d", i)
		if xobjects.Entries.Get(name) == nil {
			xobjects.Entries.Set(name, image)
			return pdf.PDFName{Value: name}
		}
	}
}
