package convert

import (
	"fmt"

	"github.com/voidrab/gopdfrab/internal/check"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"

	"github.com/voidrab/gopdfrab/internal/verify"
)

// This file fixes violations specific to an inline image's own parameters
// (the BI...ID dict), reusing the walkContentStreams/contentOpRewriter
// plumbing from fixups_content.go: a non-standard inline /Intent (folded
// into contentLimitsFixer, which already owns RenderingIntent), a true
// /Interpolate (folded into imageMetadataFixer, which already owns
// ImageInterpolate for the dict-level Image XObject case -- a check.Check can
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

// inlineImageRawOperand splits an INLINEIMAGE op's operands into its
// (key, value) params and its trailing pdf.InlineImageRaw, or ok=false if the
// last operand isn't the expected raw marker.
func inlineImageRawOperand(operands []pdf.PDFValue) (params []pdf.PDFValue, raw pdf.InlineImageRaw, ok bool) {
	if len(operands) == 0 {
		return nil, pdf.InlineImageRaw{}, false
	}
	raw, ok = operands[len(operands)-1].(pdf.InlineImageRaw)
	if !ok {
		return nil, pdf.InlineImageRaw{}, false
	}
	return operands[:len(operands)-1], raw, true
}

// hasInlineImageKey reports whether key appears among params' (key, value)
// pairs, regardless of its value.
func hasInlineImageKey(params []pdf.PDFValue, key string) bool {
	for i := 0; i+1 < len(params); i += 2 {
		if name, ok := params[i].(pdf.PDFName); ok && name.Value == key {
			return true
		}
	}
	return false
}

// inlineImageDecodeParms returns the predictor parameters from an inline
// image's /DP or /DecodeParms entry, mirroring streamDecodeParms for the
// operand-pair form. Returns a zero-value dict when none is present.
func inlineImageDecodeParms(params []pdf.PDFValue) pdf.PDFDict {
	var parms pdf.PDFValue
	for i := 0; i+1 < len(params); i += 2 {
		if name, ok := params[i].(pdf.PDFName); ok && (name.Value == "DP" || name.Value == "DecodeParms") {
			parms = params[i+1]
		}
	}
	switch p := parms.(type) {
	case pdf.PDFDict:
		return p
	case pdf.PDFArray:
		for i := len(p) - 1; i >= 0; i-- {
			if d, ok := p[i].(pdf.PDFDict); ok {
				return d
			}
		}
	}
	return pdf.PDFDict{}
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

// undoInlineImagePredictor reverses any PNG/TIFF predictor recorded in an
// inline image's decode params, mirroring decodeStreamPredicted's tail. Data
// is returned unchanged when no predictor applies.
func undoInlineImagePredictor(data []byte, params []pdf.PDFValue) ([]byte, error) {
	parms := inlineImageDecodeParms(params)
	predictor := pdf.DictInt(parms, "Predictor", 1)
	if predictor == 1 {
		return data, nil
	}
	columns := pdf.DictInt(parms, "Columns", 1)
	colors := pdf.DictInt(parms, "Colors", 1)
	bpc := pdf.DictInt(parms, "BitsPerComponent", 8)
	switch {
	case predictor == 2:
		return pdf.UndoTIFFPredictor(data, columns, colors, bpc)
	case predictor >= 10:
		return pdf.UndoPNGPredictor(data, columns, colors, bpc)
	default:
		return nil, fmt.Errorf("unsupported predictor %d", predictor)
	}
}

// fixInlineImageRenderingIntent flips a non-standard inline-image /Intent
// to /RelativeColorimetric, mirroring checkInlineImageOther's Intent case
// (checks_content.go) in reverse. ok is false if operands isn't an
// INLINEIMAGE op's operands or nothing needed fixing.
func fixInlineImageRenderingIntent(operands []pdf.PDFValue) (fixed []pdf.PDFValue, ok bool) {
	params, raw, ok := inlineImageRawOperand(operands)
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
		if name, ok := fixedParams[i+1].(pdf.PDFName); ok && !verify.AllowedIntents[name.Value] {
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
// owns check.Checks.Image.ImageInterpolate for the dict-level Image XObject case.
func fixInlineImageInterpolate(op string, operands []pdf.PDFValue, changed *bool) (writer.ContentOp, bool) {
	if op != "INLINEIMAGE" {
		return writer.ContentOp{Op: op, Operands: operands}, true
	}
	params, raw, ok := inlineImageRawOperand(operands)
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

// inlineImageLZWFixer remediates check.Checks.Structure.InlineImageLZWFilter by
// decoding an inline image's LZW-filtered data and re-encoding it as Flate,
// mirroring checkInlineImageFilter (checks_content.go) in reverse.
type inlineImageLZWFixer struct{}

func (inlineImageLZWFixer) Applies(c check.Check) bool {
	return c == check.Checks.Structure.InlineImageLZWFilter
}

func (inlineImageLZWFixer) Fix(trailer *pdf.PDFDict, _ []check.PDFError) (bool, error) {
	return walkContentStreams(trailer, fixInlineImageLZW), nil
}

// fixInlineImageLZW re-encodes an inline image's data from LZW to Flate,
// updating its /F or /Filter param accordingly. A /DP or /DecodeParms
// predictor is undone on the decoded samples (reusing the shared predictor
// helpers, the way lzwStreamPlaintext does for regular streams) and then
// dropped from the params, since the re-emitted Flate data carries no
// predictor. It bails out (leaving the op unchanged) on any unexpected
// shape rather than risking a corrupted image.
func fixInlineImageLZW(op string, operands []pdf.PDFValue, changed *bool) (writer.ContentOp, bool) {
	if op != "INLINEIMAGE" {
		return writer.ContentOp{Op: op, Operands: operands}, true
	}
	params, raw, ok := inlineImageRawOperand(operands)
	if !ok {
		return writer.ContentOp{Op: op, Operands: operands}, true
	}

	fixedParams := append([]pdf.PDFValue{}, params...)
	lzwFound := false
	for i := 0; i+1 < len(fixedParams); i += 2 {
		key, ok := fixedParams[i].(pdf.PDFName)
		if !ok || (key.Value != "F" && key.Value != "Filter") {
			continue
		}
		name, ok := fixedParams[i+1].(pdf.PDFName)
		if !ok || (name.Value != "LZW" && name.Value != "LZWDecode") {
			continue
		}
		fixedParams[i+1] = pdf.PDFName{Value: "Fl"}
		lzwFound = true
	}
	if !lzwFound {
		return writer.ContentOp{Op: op, Operands: operands}, true
	}

	plain, err := pdf.DecodeLZW(raw.Data)
	if err != nil {
		return writer.ContentOp{Op: op, Operands: operands}, true
	}
	plain, err = undoInlineImagePredictor(plain, fixedParams)
	if err != nil {
		return writer.ContentOp{Op: op, Operands: operands}, true
	}
	fixedParams = removeInlineImageKeys(fixedParams, "DP", "DecodeParms")
	compressed, err := writer.DeflateZlib(plain)
	if err != nil {
		return writer.ContentOp{Op: op, Operands: operands}, true
	}
	newBytes, err := writer.BuildInlineImageBytes(fixedParams, compressed)
	if err != nil {
		return writer.ContentOp{Op: op, Operands: operands}, true
	}

	*changed = true
	return writer.ContentOp{Op: op, Operands: append(fixedParams, pdf.InlineImageRaw{Bytes: newBytes, Data: compressed})}, true
}
