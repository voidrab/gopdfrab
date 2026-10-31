package verify

import (
	"fmt"

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
	ctx.collapsingStreamReports(func() {
		ctx.scanStream(dict, contentChecker(dict, resources, ctx))
	})
}

// NamedColourModel resolves a colour-space name to a device model, consulting
// the /ColorSpace resource dictionary for named spaces.
func NamedColourModel(name pdf.PDFName, resources pdf.PDFDict) string {
	if m := DeviceColourModel(name); m != "" {
		return m
	}
	if cs, ok := resources.Entries.Get("ColorSpace").(pdf.PDFDict); ok {
		return DeviceColourModel(cs.Entries.Get(name.Value))
	}
	return ""
}

// contentChecker returns the per-operator callback running the content-stream
// checks (6.2.3.3 colour, 6.2.9 rendering intent, 6.2.10 operators) against obj.
// It closes over the state a single stream carries from operator to operator, so
// one checker serves exactly one scan.
//
// The checks run fresh on every pass even when ctx.scanStream replays a cached
// token list, since a verdict can depend on state that changes between convert's
// fixer iterations (e.g. OutputIntent coverage, Resources).
func contentChecker(obj pdf.PDFValue, resources pdf.PDFDict, ctx *ValidationContext) func(string, []pdf.PDFValue) {
	colourSet := false
	qDepth := 0
	inherit := ctx.inherit
	part2 := ctx.part2()
	var over *overprintTracker
	if part2 {
		over = &overprintTracker{}
	}
	return func(op string, operands []pdf.PDFValue) {
		if part2 {
			inherit.checkOperands(op, operands, obj, ctx)
			over.step(op, operands, resources, obj, ctx)
		}
		// 6.1.12: integer/real/string operands are bounded (2^31-1, 32767, 65535 bytes).
		for _, operand := range operands {
			checkOperandLimits(operand, obj, ctx)
		}
		// 6.1.12: q/Q operators nest no deeper than the level's limit.
		switch op {
		case "q":
			qDepth++
			if limit := ctx.Spec.Limits.MaxQNesting; overLimit(qDepth, limit) {
				ctx.Report(pdf.Checks.Structure.GraphicsStateNesting, obj, fmt.Sprintf("q/Q nesting depth %d exceeds maximum of %d", qDepth, limit))
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
					checkBaseColour(namedColourSpace(name, resources), resources, obj, ctx)
				}
			}
		case "sc", "scn", "SC", "SCN":
			colourSet = true
		case "ri":
			if len(operands) > 0 {
				if name, ok := operands[len(operands)-1].(pdf.PDFName); ok && !ctx.Spec.RenderingIntents.Has(name.Value) {
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
	}
}

// checkOperandLimits reports any 6.1.12/6.1.6 limit violation in a content
// operand, recursing into array operands (e.g. a TJ text-positioning array)
// whose own scalars are subject to the same limits.
func checkOperandLimits(operand pdf.PDFValue, obj pdf.PDFValue, ctx *ValidationContext) {
	switch v := operand.(type) {
	case pdf.PDFInteger:
		if integerOutOfRange(v, ctx.Spec.Limits) {
			ctx.Report(pdf.Checks.Structure.IntegerOutOfRange, obj, fmt.Sprintf("integer in content stream exceeds limits: %d", v))
		}
	case pdf.PDFReal:
		if realOutOfRange(v, ctx.Spec.Limits) {
			ctx.Report(pdf.Checks.Structure.RealOutOfRange, obj, fmt.Sprintf("real number in content stream out of range: %g", float64(v)))
		}
		if realTooCloseToZero(v, ctx.Spec.Limits) {
			ctx.Report(pdf.Checks.Structure.RealTooCloseToZero, obj, fmt.Sprintf("real number in content stream too close to zero: %g", float64(v)))
		}
	case pdf.PDFString:
		if limit := ctx.Spec.Limits.MaxStringBytes; overLimit(len(v.Value), limit) {
			ctx.Report(pdf.Checks.Structure.StringTooLong, obj, fmt.Sprintf("string in content stream exceeds maximum length of %d bytes", limit))
		}
	case pdf.PDFHexString:
		// 6.1.6: hex string operands must be valid hex digits, even count.
		validateHexString(v, obj, ctx)
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
	if DefaultColorSpaceDefined(model, ctx.resourceScope) {
		return
	}
	ctx.reportUncovered(pdf.Checks.Colour.DeviceColourContentStream, obj, fmt.Sprintf("device colour (%s) used in content stream without matching OutputIntent", model))
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
			checkBaseColour(val, resources, obj, ctx)
		}
		if model != "" && !ctx.deviceColourAllowed(model) {
			reportContentColour(obj, model, resources, ctx)
		}
	}
}

// checkInlineImageFilter flags an inline image using the LZW filter, or a
// filter the level does not allow (6.1.10).
func checkInlineImageFilter(obj pdf.PDFValue, params []pdf.PDFValue, ctx *ValidationContext) {
	for i := 0; i+1 < len(params); i += 2 {
		key, ok := params[i].(pdf.PDFName)
		if !ok || (key.Value != "F" && key.Value != "Filter") {
			continue
		}
		for _, f := range pdf.FilterNames(params[i+1]) {
			switch allowed := ctx.Spec.InlineImageFilters; {
			case f == "LZW" || f == "LZWDecode":
				ctx.Report(pdf.Checks.Structure.InlineImageLZWFilter, obj, "inline image uses forbidden LZW filter")
			case allowed.Len() > 0 && !allowed.Has(f):
				ctx.Report(pdf.Checks.Structure.InlineImageFilterNotAllowed, obj, fmt.Sprintf("inline image uses filter /%s, which is not allowed", f))
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
			if name, ok := val.(pdf.PDFName); ok && !ctx.Spec.RenderingIntents.Has(name.Value) {
				// Part 1 numbers it with the content stream's intents
				// (6.2.9), Part 2 with every other rendering intent (6.2.6).
				c := pdf.Checks.Colour.RenderingIntent
				if ctx.part2() {
					c = pdf.Checks.Image.ImageRenderingIntent
				}
				ctx.Report(c, obj, fmt.Sprintf("inline image rendering intent /%s is not a standard rendering intent", name.Value))
			}
		}
	}
}

// validateContentStreams inspects the content streams v holds (see
// pdf.ContentRoots): a page's, with its annotation appearances, a tiling
// pattern, a form XObject that page content reaches, or a Type 3 font's
// glyph procedures.
func validateContentStreams(v pdf.PDFDict, ctx *ValidationContext) {
	kind := pdf.ContentRoots(v, func(r pdf.ContentRoot) {
		if r.Kind == pdf.ContentForm && !ctx.isReachableXObject(v) {
			return
		}
		saved := ctx.inherit
		ctx.inherit = inheritanceOf(r, v, ctx)
		scanContentDict(r.Stream, r.Resources, ctx)
		ctx.inherit = saved
	})
	if kind == pdf.ContentPage {
		scanAnnotAppearances(v, ctx)
	}
}

// scanAnnotAppearances scans the normal (N) appearance streams of every annotation
// on a page for content-stream violations (e.g. device-colour usage, 6.2.3.3).
// Appearance streams have their own resource scope; page Default* must not excuse
// their device-colour usage (PDF/A-1b clause 6.2.3.3, as enforced by veraPDF).
func scanAnnotAppearances(page pdf.PDFDict, ctx *ValidationContext) {
	annots, ok := page.Entries.Get("Annots").(pdf.PDFArray)
	if !ok {
		return
	}
	saved := ctx.resourceScope
	ctx.resourceScope = pdf.PDFDict{}
	defer func() { ctx.resourceScope = saved }()
	for _, item := range annots {
		annot, ok := item.(pdf.PDFDict)
		if !ok {
			continue
		}
		ap, ok := annot.Entries.Get("AP").(pdf.PDFDict)
		if !ok {
			continue
		}
		scanAPEntry(ap.Entries.Get("N"), ctx)
	}
}

// scanAPEntry scans one /AP entry: either a single stream or a subdictionary
// of appearance states (Btn widget).
func scanAPEntry(n pdf.PDFValue, ctx *ValidationContext) {
	switch v := n.(type) {
	case pdf.PDFDict:
		if v.IsStream() {
			apRes, _ := v.Entries.Get("Resources").(pdf.PDFDict)
			scanContentDict(v, apRes, ctx)
		} else {
			// Subdictionary of appearance states.
			for _, sv := range v.Entries.All() {
				if sd, ok := sv.(pdf.PDFDict); ok && sd.IsStream() {
					apRes, _ := sd.Entries.Get("Resources").(pdf.PDFDict)
					scanContentDict(sd, apRes, ctx)
				}
			}
		}
	}
}
