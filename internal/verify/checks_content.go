package verify

import (
	"fmt"
	"math"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// PDFOperators is the set of content-stream operators defined by the PDF
// Reference (PDF 32000 Annex A). Any other operator violates 6.2.10.
var PDFOperators = map[string]bool{
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

// InlineCSAbbrev maps inline-image colour space abbreviations to device models.
var InlineCSAbbrev = map[string]string{
	"RGB": "rgb", "G": "gray", "CMYK": "cmyk",
	"DeviceRGB": "rgb", "DeviceGray": "gray", "DeviceCMYK": "cmyk",
}

// scanContentDict decodes and inspects a single content stream dictionary.
func scanContentDict(dict pdf.PDFDict, resources pdf.PDFDict, ctx *ValidationContext) {
	ops, err := ctx.scanStreamCached(dict)
	if err != nil {
		return
	}
	scanContent(ops, dict, resources, ctx)
}

// scanContentValue inspects a /Contents value that may be a single stream or an
// array of streams.
func scanContentValue(contents pdf.PDFValue, resources pdf.PDFDict, ctx *ValidationContext) {
	switch v := contents.(type) {
	case pdf.PDFDict:
		scanContentDict(v, resources, ctx)
	case pdf.PDFArray:
		for _, item := range v {
			if d, ok := item.(pdf.PDFDict); ok {
				scanContentDict(d, resources, ctx)
			}
		}
	}
}

// NamedColourModel resolves a colour-space name to a device model, consulting
// the /ColorSpace resource dictionary for named spaces.
func NamedColourModel(name pdf.PDFName, resources pdf.PDFDict) string {
	if m := DeviceColourModel(name); m != "" {
		return m
	}
	if cs, ok := resources.Entries["ColorSpace"].(pdf.PDFDict); ok {
		return DeviceColourModel(cs.Entries[name.Value])
	}
	return ""
}

// scanContent runs the content-stream checks (6.2.3.3 colour, 6.2.9 rendering
// intent, 6.2.10 operators) over a stream's tokenized operators. Replaying a
// cached token list (see ctx.scanStreamCached) rather than re-lexing means an
// unchanged stream is tokenized once across all of convert's fixer
// iterations; the checks themselves still run fresh every call, since a
// verdict can depend on state that changes between iterations (e.g.
// OutputIntent coverage, Resources).
func scanContent(ops []pdf.ScannedOp, obj pdf.PDFValue, resources pdf.PDFDict, ctx *ValidationContext) {
	colourSet := false
	qDepth := 0
	pdf.ReplayOps(ops, func(op string, operands []pdf.PDFValue) {
		// 6.1.12: integer/real/string operands are bounded (2^31-1, 32767, 65535 bytes).
		for _, operand := range operands {
			checkOperandLimits(operand, obj, ctx)
		}
		// 6.1.12: maximum nesting depth of q/Q operators is 28.
		switch op {
		case "q":
			qDepth++
			if qDepth > 28 {
				ctx.Report(pdf.Checks.Structure.GraphicsStateNesting, obj, fmt.Sprintf("q/Q nesting depth %d exceeds maximum of 28", qDepth))
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
				if name, ok := operands[len(operands)-1].(pdf.PDFName); ok {
					if model := NamedColourModel(name, resources); model != "" {
						reportContentColour(obj, model, resources, ctx)
					}
				}
			}
		case "sc", "scn", "SC", "SCN":
			colourSet = true
		case "ri":
			if len(operands) > 0 {
				if name, ok := operands[len(operands)-1].(pdf.PDFName); ok && !AllowedIntents[name.Value] {
					ctx.Report(pdf.Checks.Colour.RenderingIntent, obj, fmt.Sprintf("undefined rendering intent /%s", name.Value))
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
			if !PDFOperators[op] {
				ctx.Report(pdf.Checks.Colour.UndefinedOperator, obj, fmt.Sprintf("undefined content operator %q", op))
			}
		}
	})
}

// checkOperandLimits reports any 6.1.12/6.1.6 limit violation in a content
// operand, recursing into array operands (e.g. a TJ text-positioning array)
// whose own scalars are subject to the same limits.
func checkOperandLimits(operand pdf.PDFValue, obj pdf.PDFValue, ctx *ValidationContext) {
	switch v := operand.(type) {
	case pdf.PDFInteger:
		if v > 2147483647 || v < -2147483648 {
			ctx.Report(pdf.Checks.Structure.IntegerOutOfRange, obj, fmt.Sprintf("integer in content stream exceeds limits: %d", v))
		}
	case pdf.PDFReal:
		if math.Abs(float64(v)) > 32767 {
			ctx.Report(pdf.Checks.Structure.RealOutOfRange, obj, fmt.Sprintf("real number in content stream out of range: %g", float64(v)))
		}
	case pdf.PDFString:
		if len(v.Value) > 65535 {
			ctx.Report(pdf.Checks.Structure.StringTooLong, obj, "string in content stream exceeds maximum length of 65535 bytes")
		}
	case pdf.PDFHexString:
		// 6.1.6: hex string operands must be valid hex digits, even count.
		validateHexString(v, ctx)
	case pdf.PDFArray:
		for _, e := range v {
			checkOperandLimits(e, obj, ctx)
		}
	}
}

// reportContentColour flags a device colour model not covered by an output
// intent (6.2.3.3), unless a Default* colour space override applies.
func reportContentColour(obj pdf.PDFValue, model string, resources pdf.PDFDict, ctx *ValidationContext) {
	if ctx.deviceColourAllowed(model) {
		return
	}
	if DefaultColorSpaceDefined(model, resources) {
		return
	}
	if DefaultColorSpaceDefined(model, ctx.pageResources) {
		return
	}
	ctx.Report(pdf.Checks.Colour.DeviceColourContentStream, obj, fmt.Sprintf("device colour (%s) used in content stream without matching OutputIntent", model))
}

// checkInlineImageColour inspects inline image parameters for a device colour
// space (6.2.3.3).
func checkInlineImageColour(obj pdf.PDFValue, params []pdf.PDFValue, resources pdf.PDFDict, ctx *ValidationContext) {
	for i := 0; i+1 < len(params); i += 2 {
		key, ok := params[i].(pdf.PDFName)
		if !ok {
			continue
		}
		if key.Value != "CS" && key.Value != "ColorSpace" {
			continue
		}
		var model string
		switch val := params[i+1].(type) {
		case pdf.PDFName:
			if m, ok := InlineCSAbbrev[val.Value]; ok {
				model = m
			} else {
				model = NamedColourModel(val, resources)
			}
		case pdf.PDFArray:
			model = DeviceColourModel(val)
		}
		if model != "" && !ctx.deviceColourAllowed(model) {
			reportContentColour(obj, model, resources, ctx)
		}
	}
}

// checkInlineImageFilter flags an inline image using the LZW filter (6.1.10).
func checkInlineImageFilter(obj pdf.PDFValue, params []pdf.PDFValue, ctx *ValidationContext) {
	for i := 0; i+1 < len(params); i += 2 {
		key, ok := params[i].(pdf.PDFName)
		if !ok || (key.Value != "F" && key.Value != "Filter") {
			continue
		}
		for _, f := range pdf.FilterNames(params[i+1]) {
			if f == "LZW" || f == "LZWDecode" {
				ctx.Report(pdf.Checks.Structure.InlineImageLZWFilter, obj, "inline image uses forbidden LZW filter")
			}
		}
	}
}

// checkInlineImageOther checks inline image parameters for 6.2.4 (Interpolate)
// and 6.2.9 (rendering intent).
func checkInlineImageOther(obj pdf.PDFValue, params []pdf.PDFValue, ctx *ValidationContext) {
	for i := 0; i+1 < len(params); i += 2 {
		key, ok := params[i].(pdf.PDFName)
		if !ok {
			continue
		}
		val := params[i+1]
		switch key.Value {
		case "I", "Interpolate":
			if b, ok := val.(pdf.PDFBoolean); ok && bool(b) {
				ctx.Report(pdf.Checks.Image.ImageInterpolate, obj, "inline image Interpolate shall not be true")
			}
		case "Intent":
			if name, ok := val.(pdf.PDFName); ok && !AllowedIntents[name.Value] {
				ctx.Report(pdf.Checks.Colour.RenderingIntent, obj, fmt.Sprintf("inline image rendering intent /%s is not a standard rendering intent", name.Value))
			}
		}
	}
}

// validateContentStreams dispatches content-stream inspection for pages, form
// XObjects, Type3 glyph procedures and tiling patterns.
func validateContentStreams(v pdf.PDFDict, ctx *ValidationContext) {
	resources, _ := v.Entries["Resources"].(pdf.PDFDict)
	switch {
	case v.Entries["Type"] == pdf.PDFName{Value: "Page"}:
		ctx.pageResources = resources
		scanContentValue(v.Entries["Contents"], resources, ctx)
		scanAnnotAppearances(v, ctx)
	case v.Entries["PatternType"] == pdf.PDFInteger(1) && v.HasStream:
		// Tiling patterns are always rendered (invoked via scn/SCN, not Do).
		scanContentDict(v, resources, ctx)
	case v.Entries["Subtype"] == pdf.PDFName{Value: "Form"} && v.HasStream:
		if !ctx.isReachableXObject(v) {
			return
		}
		scanContentDict(v, resources, ctx)
	case v.Entries["Subtype"] == pdf.PDFName{Value: "Type3"}:
		if cp, ok := v.Entries["CharProcs"].(pdf.PDFDict); ok {
			for _, proc := range cp.Entries {
				if pd, ok := proc.(pdf.PDFDict); ok && pd.HasStream {
					scanContentDict(pd, resources, ctx)
				}
			}
		}
	}
}

// scanAnnotAppearances scans the normal (N) appearance streams of every annotation
// on a page for content-stream violations (e.g. device-colour usage, 6.2.3.3).
// Appearance streams have their own resource scope; page Default* must not excuse
// their device-colour usage (PDF/A-1b clause 6.2.3.3, as enforced by veraPDF).
func scanAnnotAppearances(page pdf.PDFDict, ctx *ValidationContext) {
	annots, ok := page.Entries["Annots"].(pdf.PDFArray)
	if !ok {
		return
	}
	saved := ctx.pageResources
	ctx.pageResources = pdf.PDFDict{}
	defer func() { ctx.pageResources = saved }()
	for _, item := range annots {
		annot, ok := item.(pdf.PDFDict)
		if !ok {
			continue
		}
		ap, ok := annot.Entries["AP"].(pdf.PDFDict)
		if !ok {
			continue
		}
		scanAPEntry(ap.Entries["N"], ctx)
	}
}

// scanAPEntry scans one /AP entry: either a single stream or a subdictionary
// of appearance states (Btn widget).
func scanAPEntry(n pdf.PDFValue, ctx *ValidationContext) {
	switch v := n.(type) {
	case pdf.PDFDict:
		if v.HasStream {
			apRes, _ := v.Entries["Resources"].(pdf.PDFDict)
			scanContentDict(v, apRes, ctx)
		} else {
			// Subdictionary of appearance states.
			for k, sv := range v.Entries {
				if k == "_ref" {
					continue
				}
				if sd, ok := sv.(pdf.PDFDict); ok && sd.HasStream {
					apRes, _ := sd.Entries["Resources"].(pdf.PDFDict)
					scanContentDict(sd, apRes, ctx)
				}
			}
		}
	}
}
