package pdfrab

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
// if anything looks unexpected -- a missing inlineImageRaw operand, or (for
// LZW) a /DP-/DecodeParms predictor this package doesn't replicate for
// inline images -- leaving the violation as a residual rather than risking
// a corrupted image.

func init() {
	registerFixer(inlineImageLZWFixer{})
}

// inlineImageRawOperand splits an INLINEIMAGE op's operands into its
// (key, value) params and its trailing inlineImageRaw, or ok=false if the
// last operand isn't the expected raw marker.
func inlineImageRawOperand(operands []PDFValue) (params []PDFValue, raw inlineImageRaw, ok bool) {
	if len(operands) == 0 {
		return nil, inlineImageRaw{}, false
	}
	raw, ok = operands[len(operands)-1].(inlineImageRaw)
	if !ok {
		return nil, inlineImageRaw{}, false
	}
	return operands[:len(operands)-1], raw, true
}

// hasInlineImageKey reports whether key appears among params' (key, value)
// pairs, regardless of its value.
func hasInlineImageKey(params []PDFValue, key string) bool {
	for i := 0; i+1 < len(params); i += 2 {
		if name, ok := params[i].(PDFName); ok && name.Value == key {
			return true
		}
	}
	return false
}

// fixInlineImageRenderingIntent flips a non-standard inline-image /Intent
// to /RelativeColorimetric, mirroring checkInlineImageOther's Intent case
// (checks_content.go) in reverse. ok is false if operands isn't an
// INLINEIMAGE op's operands or nothing needed fixing.
func fixInlineImageRenderingIntent(operands []PDFValue) (fixed []PDFValue, ok bool) {
	params, raw, ok := inlineImageRawOperand(operands)
	if !ok {
		return operands, false
	}
	fixedParams := append([]PDFValue{}, params...)
	changed := false
	for i := 0; i+1 < len(fixedParams); i += 2 {
		key, ok := fixedParams[i].(PDFName)
		if !ok || key.Value != "Intent" {
			continue
		}
		if name, ok := fixedParams[i+1].(PDFName); ok && !allowedIntents[name.Value] {
			fixedParams[i+1] = PDFName{Value: "RelativeColorimetric"}
			changed = true
		}
	}
	if !changed {
		return operands, false
	}
	newBytes, err := buildInlineImageBytes(fixedParams, raw.Data)
	if err != nil {
		return operands, false
	}
	return append(fixedParams, inlineImageRaw{Bytes: newBytes, Data: raw.Data}), true
}

// fixInlineImageInterpolate flips a true inline-image /I or /Interpolate to
// false, mirroring checkInlineImageOther's Interpolate case
// (checks_content.go) in reverse. Used by imageMetadataFixer, which already
// owns Checks.Image.ImageInterpolate for the dict-level Image XObject case.
func fixInlineImageInterpolate(op string, operands []PDFValue, changed *bool) (contentOp, bool) {
	if op != "INLINEIMAGE" {
		return contentOp{Op: op, Operands: operands}, true
	}
	params, raw, ok := inlineImageRawOperand(operands)
	if !ok {
		return contentOp{Op: op, Operands: operands}, true
	}
	fixedParams := append([]PDFValue{}, params...)
	flipped := false
	for i := 0; i+1 < len(fixedParams); i += 2 {
		key, ok := fixedParams[i].(PDFName)
		if !ok || (key.Value != "I" && key.Value != "Interpolate") {
			continue
		}
		if b, ok := fixedParams[i+1].(PDFBoolean); ok && bool(b) {
			fixedParams[i+1] = PDFBoolean(false)
			flipped = true
		}
	}
	if !flipped {
		return contentOp{Op: op, Operands: operands}, true
	}
	newBytes, err := buildInlineImageBytes(fixedParams, raw.Data)
	if err != nil {
		return contentOp{Op: op, Operands: operands}, true
	}
	*changed = true
	return contentOp{Op: op, Operands: append(fixedParams, inlineImageRaw{Bytes: newBytes, Data: raw.Data})}, true
}

// inlineImageLZWFixer remediates Checks.Structure.InlineImageLZWFilter by
// decoding an inline image's LZW-filtered data and re-encoding it as Flate,
// mirroring checkInlineImageFilter (checks_content.go) in reverse.
type inlineImageLZWFixer struct{}

func (inlineImageLZWFixer) Applies(c Check) bool {
	return c == Checks.Structure.InlineImageLZWFilter
}

func (inlineImageLZWFixer) Fix(trailer *PDFDict, _ []PDFError) (bool, error) {
	return walkContentStreams(trailer, fixInlineImageLZW), nil
}

// fixInlineImageLZW re-encodes an inline image's data from LZW to Flate,
// updating its /F or /Filter param accordingly. It bails out (leaving the
// op unchanged) if a /DP or /DecodeParms predictor is present, since this
// package's LZW decoder doesn't replicate the predictor undo step for
// inline images the way lzwStreamPlaintext (fixups_stream.go) does for
// regular streams.
func fixInlineImageLZW(op string, operands []PDFValue, changed *bool) (contentOp, bool) {
	if op != "INLINEIMAGE" {
		return contentOp{Op: op, Operands: operands}, true
	}
	params, raw, ok := inlineImageRawOperand(operands)
	if !ok || hasInlineImageKey(params, "DP") || hasInlineImageKey(params, "DecodeParms") {
		return contentOp{Op: op, Operands: operands}, true
	}

	fixedParams := append([]PDFValue{}, params...)
	lzwFound := false
	for i := 0; i+1 < len(fixedParams); i += 2 {
		key, ok := fixedParams[i].(PDFName)
		if !ok || (key.Value != "F" && key.Value != "Filter") {
			continue
		}
		name, ok := fixedParams[i+1].(PDFName)
		if !ok || (name.Value != "LZW" && name.Value != "LZWDecode") {
			continue
		}
		fixedParams[i+1] = PDFName{Value: "Fl"}
		lzwFound = true
	}
	if !lzwFound {
		return contentOp{Op: op, Operands: operands}, true
	}

	plain, err := decodeLZW(raw.Data)
	if err != nil {
		return contentOp{Op: op, Operands: operands}, true
	}
	compressed, err := deflateZlib(plain)
	if err != nil {
		return contentOp{Op: op, Operands: operands}, true
	}
	newBytes, err := buildInlineImageBytes(fixedParams, compressed)
	if err != nil {
		return contentOp{Op: op, Operands: operands}, true
	}

	*changed = true
	return contentOp{Op: op, Operands: append(fixedParams, inlineImageRaw{Bytes: newBytes, Data: compressed})}, true
}
