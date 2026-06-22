package pdfrab

// This file rewrites content-stream bytes to fix violations that live
// inside them rather than in a dictionary: undefined operators (6.2.10),
// non-standard rendering intents passed to ri (6.2.9), and the 6.1.12/6.1.6
// operand limits (out-of-range integers/reals, over-long strings, malformed
// hex strings) wherever they occur -- both inside content streams
// (checks_content.go's per-operand checks) and as plain dictionary/array
// values elsewhere in the graph (verifier.go's generic walk). One Fixer
// must cover both sources of the same Check (the registry is one Fixer per
// Check), so contentLimitsFixer runs a whole-graph scalar pass alongside
// the content-stream rewrite.
//
// Deliberately out of scope: the q/Q nesting-depth flavour of StringTooLong
// (checks_content.go) is a structural defect, not a clampable operand, and
// is left for the rasterization backstop. The inline-image /Intent flavour
// of RenderingIntent is fixed here (this file already owns that Check and
// already walks every INLINEIMAGE op); the other inline-image-specific
// fixes (ImageInterpolate, InlineImageLZWFilter) live in
// fixups_inline_image.go, since they belong to checks this file doesn't own.

func init() {
	registerFixer(contentLimitsFixer{})
}

// contentLimitsFixer remediates Checks.Colour.UndefinedOperator,
// Checks.Colour.RenderingIntent (the ri operator only), and the 6.1.12/6.1.6
// scalar-limit checks, mirroring scanContent/validateHexString/
// validateArchitecturalLimits in reverse.
type contentLimitsFixer struct{}

func (contentLimitsFixer) Applies(c Check) bool {
	switch c {
	case Checks.Colour.UndefinedOperator, Checks.Colour.RenderingIntent,
		Checks.Structure.HexStringOddLength, Checks.Structure.HexStringInvalidChar,
		Checks.Structure.IntegerOutOfRange, Checks.Structure.StringTooLong:
		return true
	}
	return false
}

func (contentLimitsFixer) Fix(trailer *PDFDict, _ []PDFError) (bool, error) {
	changed := false

	walkScalars(*trailer, map[uintptr]bool{}, func(v PDFValue) (PDFValue, bool) {
		fixed, ok := fixScalarLimitsValue(v)
		if ok {
			changed = true
		}
		return fixed, ok
	})

	if walkContentStreams(trailer, rewriteOperatorsAndLimits) {
		changed = true
	}

	return changed, nil
}

// rewriteOperatorsAndLimits is contentLimitsFixer's contentOpRewriter: it
// drops an undefined operator, replaces a non-standard ri/inline-/Intent
// rendering intent with /RelativeColorimetric, and clamps/repairs any
// operand violating the 6.1.12/6.1.6 limits.
func rewriteOperatorsAndLimits(op string, operands []PDFValue, changed *bool) (contentOp, bool) {
	fixed := make([]PDFValue, len(operands))
	opChanged := false
	for i, operand := range operands {
		v, ok := fixScalarLimitsValue(operand)
		fixed[i] = v
		if ok {
			opChanged = true
		}
	}
	switch {
	case op == "INLINEIMAGE":
		if withIntent, ok := fixInlineImageRenderingIntent(fixed); ok {
			fixed = withIntent
			opChanged = true
		}
	case !pdfOperators[op]:
		*changed = true
		return contentOp{}, false // drop the undefined operator and its operands entirely
	case op == "ri":
		if len(fixed) > 0 {
			if name, ok := fixed[len(fixed)-1].(PDFName); ok && !allowedIntents[name.Value] {
				fixed[len(fixed)-1] = PDFName{Value: "RelativeColorimetric"}
				opChanged = true
			}
		}
	}
	if opChanged {
		*changed = true
	}
	return contentOp{Op: op, Operands: fixed}, true
}

// contentOpRewriter transforms one scanned (op, operands) pair into a new
// contentOp, or signals it should be dropped from the stream (keep=false).
// It must set *changed to true whenever the emitted op differs from the
// scanned one (dropping an op always counts as a change).
type contentOpRewriter func(op string, operands []PDFValue, changed *bool) (newOp contentOp, keep bool)

// rewriteContentStreamDict decodes dict's content stream, applies rewrite to
// every scanned op, and re-encodes the stream only if rewrite actually
// changed something.
func rewriteContentStreamDict(dict PDFDict, rewrite contentOpRewriter) (PDFDict, bool) {
	data, err := decodeStream(dict)
	if err != nil {
		return dict, false
	}

	var ops []contentOp
	modified := false
	newContentScanner(data).scan(func(op string, operands []PDFValue) {
		newOp, keep := rewrite(op, operands, &modified)
		if !keep {
			return
		}
		ops = append(ops, newOp)
	})
	if !modified {
		return dict, false
	}

	out, err := writeContentStream(ops)
	if err != nil {
		return dict, false
	}
	delete(dict.Entries, "Filter")
	delete(dict.Entries, "DecodeParms")
	delete(dict.Entries, "DP")
	dict.RawStream = out
	MarkStreamDirty(&dict)
	return dict, true
}

// walkContentStreams calls rewrite (via rewriteContentStreamDict) for every
// content-bearing stream reachable from trailer -- Page /Contents, tiling
// Pattern, Form XObject, Type3 CharProcs -- the same dispatch
// validateContentStreams (checks_content.go) uses, and reports whether
// anything changed.
func walkContentStreams(trailer *PDFDict, rewrite contentOpRewriter) bool {
	changed := false
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d PDFDict) (PDFDict, bool) {
		switch {
		case (d.Entries["Type"] == PDFName{Value: "Page"}):
			switch contents := d.Entries["Contents"].(type) {
			case PDFDict:
				if contents.HasStream {
					if fixed, ok := rewriteContentStreamDict(contents, rewrite); ok {
						d.Entries["Contents"] = fixed
						changed = true
					}
				}
			case PDFArray:
				for i, item := range contents {
					cd, ok := item.(PDFDict)
					if !ok || !cd.HasStream {
						continue
					}
					if fixed, ok := rewriteContentStreamDict(cd, rewrite); ok {
						contents[i] = fixed
						changed = true
					}
				}
			}
			return d, false

		case d.Entries["PatternType"] == PDFInteger(1) && d.HasStream,
			(d.Entries["Subtype"] == PDFName{Value: "Form"}) && d.HasStream:
			if fixed, ok := rewriteContentStreamDict(d, rewrite); ok {
				changed = true
				return fixed, true
			}
			return d, false

		case (d.Entries["Subtype"] == PDFName{Value: "Type3"}):
			if procs, ok := d.Entries["CharProcs"].(PDFDict); ok {
				for k, v := range procs.Entries {
					pd, ok := v.(PDFDict)
					if !ok || !pd.HasStream {
						continue
					}
					if fixed, ok := rewriteContentStreamDict(pd, rewrite); ok {
						procs.Entries[k] = fixed
						changed = true
					}
				}
			}
			return d, false
		}
		return d, false
	})
	return changed
}

// fixScalarLimitsValue clamps/repairs v if it violates the 6.1.12 integer/
// real/string limits or the 6.1.6 hex-string rules, mirroring
// validateArchitecturalLimits/validateHexString (verifier.go) in reverse.
// ok is false (v returned unchanged) for any other type or an in-range value.
func fixScalarLimitsValue(v PDFValue) (fixed PDFValue, ok bool) {
	switch val := v.(type) {
	case PDFInteger:
		switch {
		case val > 2147483647:
			return PDFInteger(2147483647), true
		case val < -2147483648:
			return PDFInteger(-2147483648), true
		}
	case PDFReal:
		switch {
		case val > 32767:
			return PDFReal(32767), true
		case val < -32767:
			return PDFReal(-32767), true
		}
	case PDFString:
		if pdfStringDecodedLen(val.Value) > 65535 {
			return PDFString{Value: truncatePDFStringRaw(val.Value, 65535)}, true
		}
	case PDFHexString:
		if repaired := fixHexStringValue(val.Value); repaired != val.Value {
			return PDFHexString{Value: repaired}, true
		}
	}
	return v, false
}

// fixHexStringValue strips non-hex characters and, if the remaining digit
// count is odd, pads with a trailing "0" -- the PDF spec's own rule for an
// odd-length hex string (the final nibble defaults to 0).
func fixHexStringValue(s string) string {
	buf := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if isHexDigit(s[i]) {
			buf = append(buf, s[i])
		}
	}
	if len(buf)%2 != 0 {
		buf = append(buf, '0')
	}
	return string(buf)
}

// truncatePDFStringRaw truncates a literal string's raw (escape sequences
// intact) bytes to at most max bytes, trimming one further byte if that
// would leave a trailing unescaped backslash that would otherwise escape
// whatever follows once re-serialized.
func truncatePDFStringRaw(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	backslashes := 0
	for i := len(s) - 1; i >= 0 && s[i] == '\\'; i-- {
		backslashes++
	}
	if backslashes%2 == 1 {
		s = s[:len(s)-1]
	}
	return s
}

// walkScalars visits every PDFInteger/PDFReal/PDFString/PDFHexString value
// reachable from v as a dict entry or array element, replacing it in place
// via fix when fix reports a change. The dict/array-element counterpart to
// walkDicts (which only ever invokes its callback on PDFDict nodes).
func walkScalars(v PDFValue, visited map[uintptr]bool, fix func(PDFValue) (PDFValue, bool)) {
	switch val := v.(type) {
	case PDFDict:
		ptr := pdfValuePointer(val.Entries)
		if visited[ptr] {
			return
		}
		visited[ptr] = true
		for k, child := range val.Entries {
			if k == "_ref" || k == "_dirty" {
				continue
			}
			if updated, ok := fix(child); ok {
				val.Entries[k] = updated
				child = updated
			}
			walkScalars(child, visited, fix)
		}

	case PDFArray:
		ptr := pdfValuePointer(val)
		if visited[ptr] {
			return
		}
		visited[ptr] = true
		for i, child := range val {
			if updated, ok := fix(child); ok {
				val[i] = updated
				child = updated
			}
			walkScalars(child, visited, fix)
		}
	}
}
