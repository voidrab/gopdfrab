package convert

import (
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

func init() {
	registerFixer(inlineImageLZWFixer{})
}

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
	head.HasStream = true
	head.RawStream = raw.Data
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
