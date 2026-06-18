package pdfrab

import (
	"fmt"
	"math"
)

// pdfOperators is the set of content-stream operators defined by the PDF
// Reference (PDF 32000 Annex A). Any other operator violates 6.2.10.
var pdfOperators = map[string]bool{
	"b": true, "B": true, "b*": true, "B*": true, "BDC": true, "BI": true,
	"BMC": true, "BT": true, "BX": true, "c": true, "cm": true, "CS": true,
	"cs": true, "d": true, "d0": true, "d1": true, "Do": true, "DP": true,
	"EI": true, "EMC": true, "ET": true, "EX": true, "f": true, "F": true,
	"f*": true, "G": true, "g": true, "gs": true, "h": true, "i": true,
	"ID": true, "j": true, "J": true, "K": true, "k": true, "l": true,
	"m": true, "M": true, "MP": true, "n": true, "q": true, "Q": true,
	"re": true, "RG": true, "rg": true, "ri": true, "s": true, "S": true,
	"SC": true, "sc": true, "SCN": true, "scn": true, "sh": true, "T*": true,
	"Tc": true, "Td": true, "TD": true, "Tf": true, "Tj": true, "TJ": true,
	"TL": true, "Tm": true, "Tr": true, "Ts": true, "Tw": true, "Tz": true,
	"v": true, "w": true, "W": true, "W*": true, "y": true, "'": true, "\"": true,
}

// paintingOps are the operators that mark the page; using them with no colour
// set applies the default DeviceGray fill/stroke colour.
var paintingOps = map[string]bool{
	"f": true, "F": true, "f*": true, "S": true, "s": true,
	"B": true, "B*": true, "b": true, "b*": true, "sh": true,
	"Tj": true, "TJ": true, "'": true, "\"": true,
}

// inlineCSAbbrev maps inline-image colour space abbreviations to device models.
var inlineCSAbbrev = map[string]string{
	"RGB": "rgb", "G": "gray", "CMYK": "cmyk",
	"DeviceRGB": "rgb", "DeviceGray": "gray", "DeviceCMYK": "cmyk",
}

// scanContentDict decodes and inspects a single content stream dictionary.
func scanContentDict(dict PDFDict, resources PDFDict, ctx *ValidationContext) {
	data, err := decodeStream(dict)
	if err != nil {
		return
	}
	scanContent(data, dict, resources, ctx)
}

// scanContentValue inspects a /Contents value that may be a single stream or an
// array of streams.
func scanContentValue(contents PDFValue, resources PDFDict, ctx *ValidationContext) {
	switch v := contents.(type) {
	case PDFDict:
		scanContentDict(v, resources, ctx)
	case PDFArray:
		for _, item := range v {
			if d, ok := item.(PDFDict); ok {
				scanContentDict(d, resources, ctx)
			}
		}
	}
}

// namedColourModel resolves a colour-space name to a device model, consulting
// the /ColorSpace resource dictionary for named spaces.
func namedColourModel(name PDFName, resources PDFDict) string {
	if m := deviceColourModel(name); m != "" {
		return m
	}
	if cs, ok := resources.Entries["ColorSpace"].(PDFDict); ok {
		return deviceColourModel(cs.Entries[name.Value])
	}
	return ""
}

// validateContentHexStrings scans decoded content-stream bytes for invalid hex
// strings (6.1.6): odd number of non-whitespace chars, or non-hex characters.
func validateContentHexStrings(data []byte, obj PDFValue, ctx *ValidationContext) {
	i := 0
	for i < len(data) {
		b := data[i]
		// Skip comments (% to end of line).
		if b == '%' {
			for i < len(data) && data[i] != '\n' && data[i] != '\r' {
				i++
			}
			continue
		}
		// Skip literal strings (balanced parens, handling backslash escapes).
		if b == '(' {
			i++
			depth := 1
			for i < len(data) && depth > 0 {
				c := data[i]
				if c == '\\' {
					i += 2
				} else if c == '(' {
					depth++
					i++
				} else if c == ')' {
					depth--
					i++
				} else {
					i++
				}
			}
			continue
		}
		// << is dictionary start, not a hex string.
		if b == '<' && i+1 < len(data) && data[i+1] == '<' {
			i += 2
			continue
		}
		// Single < starts a hex string.
		if b == '<' {
			i++
			nonws := 0
			hasInvalid := false
			for i < len(data) && data[i] != '>' {
				c := data[i]
				if !isWhitespaceByte(c) {
					if hexDigit(c) == -1 {
						hasInvalid = true
					}
					nonws++
				}
				i++
			}
			if i < len(data) {
				i++ // consume >
			}
			if hasInvalid {
				ctx.ReportError(obj, "6.1.6", 2, "hex string contains non-hexadecimal character")
			} else if nonws%2 != 0 {
				ctx.ReportError(obj, "6.1.6", 1, "hex string has odd number of non-whitespace characters")
			}
			continue
		}
		i++
	}
}

// scanContent runs the content-stream checks (6.2.3.3 colour, 6.2.9 rendering
// intent, 6.2.10 operators) over decoded content bytes.
func scanContent(data []byte, obj PDFValue, resources PDFDict, ctx *ValidationContext) {
	validateContentHexStrings(data, obj, ctx)
	cs := newContentScanner(data)
	colourSet := false
	qDepth := 0
	cs.scan(func(op string, operands []PDFValue) {
		// 6.1.12: integer operands are limited to 2^31 - 1.
		// 6.1.12: real number operands must have magnitude ≤ 32767.
		// 6.1.12: string operands must not exceed 65535 bytes.
		for _, operand := range operands {
			if n, ok := operand.(PDFInteger); ok && (n > 2147483647 || n < -2147483648) {
				ctx.ReportError(obj, "6.1.12", 2, fmt.Sprintf("integer in content stream exceeds limits: %d", n))
			}
			if r, ok := operand.(PDFReal); ok && math.Abs(float64(r)) > 32767 {
				ctx.ReportError(obj, "6.1.12", 2, fmt.Sprintf("real number in content stream out of range: %g", float64(r)))
			}
			if s, ok := operand.(PDFString); ok && pdfStringDecodedLen(s.Value) > 65535 {
				ctx.ReportError(obj, "6.1.12", 6, "string in content stream exceeds maximum length of 65535 bytes")
			}
		}
		// 6.1.12: maximum nesting depth of q/Q operators is 28.
		switch op {
		case "q":
			qDepth++
			if qDepth > 28 {
				ctx.ReportError(obj, "6.1.12", 6, fmt.Sprintf("q/Q nesting depth %d exceeds maximum of 28", qDepth))
			}
		case "Q":
			if qDepth > 0 {
				qDepth--
			}
		}
		switch op {
		case "rg", "RG":
			colourSet = true
			reportContentColour(obj, "rgb", resources, ctx)
		case "g", "G":
			colourSet = true
			reportContentColour(obj, "gray", resources, ctx)
		case "k", "K":
			colourSet = true
			reportContentColour(obj, "cmyk", resources, ctx)
		case "cs", "CS":
			colourSet = true
			if len(operands) > 0 {
				if name, ok := operands[len(operands)-1].(PDFName); ok {
					if model := namedColourModel(name, resources); model != "" {
						reportContentColour(obj, model, resources, ctx)
					}
				}
			}
		case "sc", "scn", "SC", "SCN":
			colourSet = true
		case "ri":
			if len(operands) > 0 {
				if name, ok := operands[len(operands)-1].(PDFName); ok && !allowedIntents[name.Value] {
					ctx.ReportError(obj, "6.2.9", 1, fmt.Sprintf("undefined rendering intent /%s", name.Value))
				}
			}
		case "INLINEIMAGE":
			checkInlineImageColour(obj, operands, resources, ctx)
			checkInlineImageFilter(obj, operands, ctx)
			checkInlineImageOther(obj, operands, ctx)
		default:
			if paintingOps[op] && !colourSet {
				// Default fill/stroke colour is DeviceGray.
				reportContentColour(obj, "gray", resources, ctx)
			}
			if !pdfOperators[op] {
				ctx.ReportError(obj, "6.2.10", 1, fmt.Sprintf("undefined content operator %q", op))
			}
		}
	})
}

// reportContentColour flags use of a device colour model not covered by an
// output intent (6.2.3.3). resources is the current content stream's resource
// dict; Default* colour space overrides defined there (or in the page resources)
// make the device colour space conformant.
func reportContentColour(obj PDFValue, model string, resources PDFDict, ctx *ValidationContext) {
	if ctx.deviceColourAllowed(model) {
		return
	}
	if defaultColorSpaceDefined(model, resources) {
		return
	}
	if defaultColorSpaceDefined(model, ctx.pageResources) {
		return
	}
	ctx.ReportError(obj, "6.2.3.3", 2,
		fmt.Sprintf("device colour (%s) used in content stream without matching OutputIntent", model))
}

// checkInlineImageColour inspects inline image parameters for a device colour
// space (6.2.3.3).
func checkInlineImageColour(obj PDFValue, params []PDFValue, resources PDFDict, ctx *ValidationContext) {
	for i := 0; i+1 < len(params); i += 2 {
		key, ok := params[i].(PDFName)
		if !ok {
			continue
		}
		if key.Value != "CS" && key.Value != "ColorSpace" {
			continue
		}
		var model string
		switch val := params[i+1].(type) {
		case PDFName:
			if m, ok := inlineCSAbbrev[val.Value]; ok {
				model = m
			} else {
				model = namedColourModel(val, resources)
			}
		case PDFArray:
			model = deviceColourModel(val)
		}
		if model != "" && !ctx.deviceColourAllowed(model) {
			reportContentColour(obj, model, resources, ctx)
		}
	}
}

// checkInlineImageFilter flags an inline image using the LZW filter (6.1.10).
func checkInlineImageFilter(obj PDFValue, params []PDFValue, ctx *ValidationContext) {
	for i := 0; i+1 < len(params); i += 2 {
		key, ok := params[i].(PDFName)
		if !ok || (key.Value != "F" && key.Value != "Filter") {
			continue
		}
		for _, f := range filterNames(params[i+1]) {
			if f == "LZW" || f == "LZWDecode" {
				ctx.ReportError(obj, "6.1.10", 2, "inline image uses forbidden LZW filter")
			}
		}
	}
}

// checkInlineImageOther checks inline image parameters for 6.2.4 (Interpolate)
// and 6.2.9 (rendering intent).
func checkInlineImageOther(obj PDFValue, params []PDFValue, ctx *ValidationContext) {
	for i := 0; i+1 < len(params); i += 2 {
		key, ok := params[i].(PDFName)
		if !ok {
			continue
		}
		val := params[i+1]
		switch key.Value {
		case "I", "Interpolate":
			if b, ok := val.(PDFBoolean); ok && bool(b) {
				ctx.ReportError(obj, "6.2.4", 1, "inline image Interpolate shall not be true")
			}
		case "Intent":
			if name, ok := val.(PDFName); ok && !allowedIntents[name.Value] {
				ctx.ReportError(obj, "6.2.9", 1, fmt.Sprintf("inline image rendering intent /%s is not a standard rendering intent", name.Value))
			}
		}
	}
}

// validateContentStreams dispatches content-stream inspection for pages, form
// XObjects, Type3 glyph procedures and tiling patterns.
func validateContentStreams(v PDFDict, ctx *ValidationContext) {
	resources, _ := v.Entries["Resources"].(PDFDict)
	switch {
	case v.Entries["Type"] == PDFName{Value: "Page"}:
		ctx.pageResources = resources
		scanContentValue(v.Entries["Contents"], resources, ctx)
	case v.Entries["PatternType"] == PDFInteger(1) && v.HasStream:
		// Tiling patterns are always rendered (invoked via scn/SCN, not Do).
		scanContentDict(v, resources, ctx)
	case v.Entries["Subtype"] == PDFName{Value: "Form"} && v.HasStream:
		if !ctx.isReachableXObject(v) {
			return
		}
		scanContentDict(v, resources, ctx)
	case v.Entries["Subtype"] == PDFName{Value: "Type3"}:
		if cp, ok := v.Entries["CharProcs"].(PDFDict); ok {
			for _, proc := range cp.Entries {
				if pd, ok := proc.(PDFDict); ok && pd.HasStream {
					scanContentDict(pd, resources, ctx)
				}
			}
		}
	}
}
