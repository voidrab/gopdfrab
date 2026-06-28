package convert

import (
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"

	"github.com/voidrab/gopdfrab/internal/verify"
)

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
// GraphicsStateNesting (q/Q nesting depth, checks_content.go) is claimed here
// only so it counts as fixer-addressable and triggers the rasterization
// backstop; it is a structural defect, not a clampable operand, so the
// in-place pass leaves it untouched and rasterization repairs it. The inline-image /Intent flavour
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

func (contentLimitsFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Colour.UndefinedOperator, pdf.Checks.Colour.RenderingIntent,
		pdf.Checks.Structure.HexStringOddLength, pdf.Checks.Structure.HexStringInvalidChar,
		pdf.Checks.Structure.IntegerOutOfRange, pdf.Checks.Structure.RealOutOfRange,
		pdf.Checks.Structure.StringTooLong, pdf.Checks.Structure.GraphicsStateNesting:
		return true
	}
	return false
}

func (contentLimitsFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false

	walkScalars(*trailer, map[uintptr]bool{}, func(v pdf.PDFValue) (pdf.PDFValue, bool) {
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
func rewriteOperatorsAndLimits(op string, operands []pdf.PDFValue, changed *bool) (writer.ContentOp, bool) {
	fixed := make([]pdf.PDFValue, len(operands))
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
	case !verify.PDFOperators[op]:
		*changed = true
		return writer.ContentOp{}, false // drop the undefined operator and its operands entirely
	case op == "ri":
		if len(fixed) > 0 {
			if name, ok := fixed[len(fixed)-1].(pdf.PDFName); ok && !verify.AllowedIntents[name.Value] {
				fixed[len(fixed)-1] = pdf.PDFName{Value: "RelativeColorimetric"}
				opChanged = true
			}
		}
	}
	if opChanged {
		*changed = true
	}
	return writer.ContentOp{Op: op, Operands: fixed}, true
}

// contentOpRewriter transforms one scanned (op, operands) pair into a new
// writer.ContentOp, or signals it should be dropped from the stream (keep=false).
// It must set *changed to true whenever the emitted op differs from the
// scanned one (dropping an op always counts as a change).
type contentOpRewriter func(op string, operands []pdf.PDFValue, changed *bool) (newOp writer.ContentOp, keep bool)

// rewriteContentStreamDict decodes dict's content stream, applies rewrite to
// every scanned op, and re-encodes the stream only if rewrite actually
// changed something.
func rewriteContentStreamDict(dict pdf.PDFDict, rewrite contentOpRewriter) (pdf.PDFDict, bool) {
	data, err := pdf.DecodeStream(dict)
	if err != nil {
		return dict, false
	}

	var ops []writer.ContentOp
	modified := false
	pdf.NewContentScanner(data).Scan(func(op string, operands []pdf.PDFValue) {
		newOp, keep := rewrite(op, operands, &modified)
		if !keep {
			return
		}
		ops = append(ops, newOp)
	})
	if !modified {
		return dict, false
	}

	out, err := writer.WriteContentStream(ops)
	if err != nil {
		return dict, false
	}
	if err := writer.SetStreamFlate(&dict, out); err != nil {
		return dict, false
	}
	return dict, true
}

// walkContentStreams calls rewrite (via rewriteContentStreamDict) for every
// content-bearing stream reachable from trailer -- Page /Contents, tiling
// Pattern, Form XObject, Type3 CharProcs -- the same dispatch
// validateContentStreams (checks_content.go) uses, and reports whether
// anything changed.
func walkContentStreams(trailer *pdf.PDFDict, rewrite contentOpRewriter) bool {
	changed := false
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) (pdf.PDFDict, bool) {
		switch {
		case (d.Entries["Type"] == pdf.PDFName{Value: "Page"}):
			switch contents := d.Entries["Contents"].(type) {
			case pdf.PDFDict:
				if contents.HasStream {
					if fixed, ok := rewriteContentStreamDict(contents, rewrite); ok {
						d.Entries["Contents"] = fixed
						changed = true
					}
				}
			case pdf.PDFArray:
				for i, item := range contents {
					cd, ok := item.(pdf.PDFDict)
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

		case d.Entries["PatternType"] == pdf.PDFInteger(1) && d.HasStream,
			(d.Entries["Subtype"] == pdf.PDFName{Value: "Form"}) && d.HasStream:
			if fixed, ok := rewriteContentStreamDict(d, rewrite); ok {
				changed = true
				return fixed, true
			}
			return d, false

		case (d.Entries["Subtype"] == pdf.PDFName{Value: "Type3"}):
			if procs, ok := d.Entries["CharProcs"].(pdf.PDFDict); ok {
				for k, v := range procs.Entries {
					pd, ok := v.(pdf.PDFDict)
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
func fixScalarLimitsValue(v pdf.PDFValue) (fixed pdf.PDFValue, ok bool) {
	switch val := v.(type) {
	case pdf.PDFInteger:
		switch {
		case val > 2147483647:
			return pdf.PDFInteger(2147483647), true
		case val < -2147483648:
			return pdf.PDFInteger(-2147483648), true
		}
	case pdf.PDFReal:
		switch {
		case val > 32767:
			return pdf.PDFReal(32767), true
		case val < -32767:
			return pdf.PDFReal(-32767), true
		}
	case pdf.PDFString:
		if verify.PDFStringDecodedLen(val.Value) > 65535 {
			return pdf.PDFString{Value: truncatePDFStringRaw(val.Value, 65535)}, true
		}
	case pdf.PDFHexString:
		if repaired := fixHexStringValue(val.Value); repaired != val.Value {
			return pdf.PDFHexString{Value: repaired}, true
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
		if pdf.IsHexDigit(s[i]) {
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

// walkScalars visits every pdf.PDFInteger/pdf.PDFReal/pdf.PDFString/pdf.PDFHexString value
// reachable from v as a dict entry or array element, replacing it in place
// via fix when fix reports a change. The dict/array-element counterpart to
// walkDicts (which only ever invokes its callback on pdf.PDFDict nodes).
func walkScalars(v pdf.PDFValue, visited map[uintptr]bool, fix func(pdf.PDFValue) (pdf.PDFValue, bool)) {
	switch val := v.(type) {
	case pdf.PDFDict:
		ptr := pdf.ValuePointer(val.Entries)
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

	case pdf.PDFArray:
		ptr := pdf.ValuePointer(val)
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
