package pdfrab

import "fmt"

// computeColourCoverage inspects the document's OutputIntents and records which
// device colour models are covered (6.2.2 / 6.2.3.3).
func (d *Document) computeColourCoverage(ctx *ValidationContext) {
	value, err := d.ResolveGraphByPath([]string{"Root", "OutputIntents"})
	if err != nil || value == nil {
		return
	}
	intents, ok := value.(PDFArray)
	if !ok {
		return
	}
	for _, it := range intents {
		itResolved, err := d.resolveObject(it)
		if err != nil {
			continue
		}
		intent, ok := itResolved.(PDFDict)
		if !ok {
			continue
		}
		if (intent.Entries["S"] != PDFName{Value: "GTS_PDFA1"}) {
			continue
		}
		ctx.hasOutputIntent = true

		destRef := intent.Entries["DestOutputProfile"]
		if destRef == nil {
			continue
		}
		profileObj, err := d.resolveObject(destRef)
		if err != nil {
			continue
		}
		profile, ok := profileObj.(PDFDict)
		if !ok {
			continue
		}
		n, ok := profile.Entries["N"].(PDFInteger)
		if !ok {
			continue
		}
		switch int(n) {
		case 1:
			ctx.grayCovered = true
		case 3:
			ctx.rgbCovered = true
		case 4:
			ctx.cmykCovered = true
		}
	}
}

// deviceColourModel returns "rgb", "gray" or "cmyk" if cs is (or reduces to) an
// uncalibrated device colour space, else "". Indexed spaces reduce to their base.
func deviceColourModel(cs PDFValue) string {
	switch v := cs.(type) {
	case PDFName:
		switch v.Value {
		case "DeviceRGB", "RGB":
			return "rgb"
		case "DeviceGray", "G":
			return "gray"
		case "DeviceCMYK", "CMYK":
			return "cmyk"
		}
	case PDFArray:
		if len(v) == 0 {
			return ""
		}
		head, ok := v[0].(PDFName)
		if !ok {
			return ""
		}
		switch head.Value {
		case "Indexed", "I":
			if len(v) > 1 {
				return deviceColourModel(v[1])
			}
		case "DeviceRGB":
			return "rgb"
		case "DeviceGray":
			return "gray"
		case "DeviceCMYK":
			return "cmyk"
		}
	}
	return ""
}

// defaultColorSpaceDefined reports whether a Default* colour space is present in
// resources/ColorSpace, substituting the device space and avoiding a 6.2.3.3 violation.
func defaultColorSpaceDefined(model string, resources PDFDict) bool {
	cs, ok := resources.Entries["ColorSpace"].(PDFDict)
	if !ok {
		return false
	}
	switch model {
	case "rgb":
		return cs.Entries["DefaultRGB"] != nil
	case "cmyk":
		return cs.Entries["DefaultCMYK"] != nil
	case "gray":
		return cs.Entries["DefaultGray"] != nil
	}
	return false
}

// checkDeviceColour reports a 6.2.3.3 violation if the colour space reduces to a
// device colour model not covered by an output intent and not overridden by a
// Default* colour space in the current resources.
func checkDeviceColour(obj PDFValue, cs PDFValue, ctx *ValidationContext, context string) {
	model := deviceColourModel(cs)
	if model == "" || ctx.deviceColourAllowed(model) {
		return
	}
	if defaultColorSpaceDefined(model, ctx.pageResources) {
		return
	}
	ctx.ReportError(obj, "6.2.3.3", 1,
		fmt.Sprintf("device colour space (%s) used in %s without matching OutputIntent", model, context))
}

// validateColourSpaceUsage checks dictionary-level colour-space usage: image and
// shading colour spaces (6.2.3.3) and Separation/DeviceN alternate spaces (6.2.3.4).
func validateColourSpaceUsage(v PDFDict, ctx *ValidationContext) {
	if (v.Entries["Subtype"] == PDFName{Value: "Image"}) {
		if cs := v.Entries["ColorSpace"]; cs != nil {
			checkDeviceColour(v, cs, ctx, "image")
		}
	}

	if v.Entries["ShadingType"] != nil {
		if cs := v.Entries["ColorSpace"]; cs != nil {
			checkDeviceColour(v, cs, ctx, "shading")
		}
	}
}

// validateColourSpaceArray checks a colour-space array for Separation/DeviceN
// alternate spaces that reduce to an uncovered device space (6.2.3.4).
func validateColourSpaceArray(arr PDFArray, ctx *ValidationContext) {
	if len(arr) < 3 {
		return
	}
	head, ok := arr[0].(PDFName)
	if !ok || (head.Value != "Separation" && head.Value != "DeviceN") {
		return
	}
	// [/Separation name alternateSpace tintTransform]
	// [/DeviceN names alternateSpace tintTransform]
	if head.Value == "DeviceN" {
		// 6.1.12: DeviceN colour space shall not have more than 8 colorants.
		if names, ok := arr[1].(PDFArray); ok && len(names) > 8 {
			ctx.ReportError(arr, "6.1.12", 7, fmt.Sprintf("DeviceN colour space has %d colorants, maximum is 8", len(names)))
		}
	}
	alt := arr[2]
	model := deviceColourModel(alt)
	if model == "" || ctx.deviceColourAllowed(model) {
		return
	}
	ctx.ReportError(arr, "6.2.3.4", 1,
		fmt.Sprintf("%s alternate colour space (%s) used without matching OutputIntent", head.Value, model))
}
