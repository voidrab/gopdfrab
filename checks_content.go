package pdfrab

import "fmt"

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

// scanContent runs the content-stream checks (6.2.3.3 colour, 6.2.9 rendering
// intent, 6.2.10 operators) over decoded content bytes.
func scanContent(data []byte, obj PDFValue, resources PDFDict, ctx *ValidationContext) {
	cs := newContentScanner(data)
	colourSet := false
	cs.scan(func(op string, operands []PDFValue) {
		// 6.1.12: integer operands are limited to 2^31 - 1.
		for _, operand := range operands {
			if n, ok := operand.(PDFInteger); ok && (n > 2147483647 || n < -2147483648) {
				ctx.ReportError(obj, "6.1.12", 2, fmt.Sprintf("integer in content stream exceeds limits: %d", n))
			}
		}
		switch op {
		case "rg", "RG":
			colourSet = true
			reportContentColour(obj, "rgb", ctx)
		case "g", "G":
			colourSet = true
			reportContentColour(obj, "gray", ctx)
		case "k", "K":
			colourSet = true
			reportContentColour(obj, "cmyk", ctx)
		case "cs", "CS":
			colourSet = true
			if len(operands) > 0 {
				if name, ok := operands[len(operands)-1].(PDFName); ok {
					if model := namedColourModel(name, resources); model != "" {
						reportContentColour(obj, model, ctx)
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
		default:
			if paintingOps[op] && !colourSet {
				// Default fill/stroke colour is DeviceGray.
				reportContentColour(obj, "gray", ctx)
			}
			if !pdfOperators[op] {
				ctx.ReportError(obj, "6.2.10", 1, fmt.Sprintf("undefined content operator %q", op))
			}
		}
	})
}

// reportContentColour flags use of a device colour model not covered by an
// output intent (6.2.3.3).
func reportContentColour(obj PDFValue, model string, ctx *ValidationContext) {
	if ctx.deviceColourAllowed(model) {
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
			reportContentColour(obj, model, ctx)
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

// validateContentStreams dispatches content-stream inspection for pages, form
// XObjects, Type3 glyph procedures and tiling patterns.
func validateContentStreams(v PDFDict, ctx *ValidationContext) {
	resources, _ := v.Entries["Resources"].(PDFDict)
	switch {
	case v.Entries["Type"] == PDFName{Value: "Page"}:
		scanContentValue(v.Entries["Contents"], resources, ctx)
	case v.Entries["Subtype"] == PDFName{Value: "Form"} && v.HasStream:
		scanContentDict(v, resources, ctx)
	case v.Entries["Subtype"] == PDFName{Value: "Type3"}:
		if cp, ok := v.Entries["CharProcs"].(PDFDict); ok {
			for _, proc := range cp.Entries {
				if pd, ok := proc.(PDFDict); ok && pd.HasStream {
					scanContentDict(pd, resources, ctx)
				}
			}
		}
	case v.Entries["PatternType"] == PDFInteger(1) && v.HasStream:
		scanContentDict(v, resources, ctx)
	}
}
