package pdfrab

import (
	_ "embed"
	"fmt"
)

// srgbICCProfile is the ICC's official sRGB v2 profile (color.org), used as
// the base ICC stream for any OutputIntent this package injects.
// withICCColorSpace below overwrites its colour-space signature (bytes
// 16:20) per document, so only its header needs to satisfy
// validateICCProfileStream: version <= 2.x and a valid deviceClass/acsp
// signature. It must stay v2 -- PDF/A-1 (ISO 19005-1) requires ICC.1:2003-09
// and validateICCProfileStream rejects any major version above 2, which
// rules out v4 profiles such as sRGB_v4_ICC_preference.icc.
//
//go:embed assets/sRGB2014.icc
var srgbICCProfile []byte

func init() {
	registerPreemptiveFixup(injectOutputIntent)
}

// colourModelN maps dominantColourModel's "rgb"/"cmyk" result to the /N an
// OutputIntent's ICC profile must declare to cover it. "gray" deliberately
// has no entry: it never needs a specific /N (see dominantColourModel).
var colourModelN = map[string]int{"rgb": 3, "cmyk": 4}

// injectOutputIntent ensures the document's catalog has a PDF/A OutputIntent
// backed by an embedded ICC profile.
func injectOutputIntent(trailer *PDFDict) error {
	root, ok := trailer.Entries["Root"].(PDFDict)
	if !ok {
		return fmt.Errorf("injectOutputIntent: Root is not a dictionary")
	}

	dominant := dominantColourModel(detectColourModelUsage(*trailer))
	if existingN, ok := validPDFAOutputIntentN(root); ok {
		if dominant == "" || colourModelN[dominant] == existingN {
			return nil
		}
	}

	wantN, colorSpaceSig, alternate, identifier := colourModelN["rgb"], "RGB ", "DeviceRGB", "sRGB"
	if dominant == "cmyk" {
		wantN, colorSpaceSig, alternate, identifier = colourModelN["cmyk"], "CMYK", "DeviceCMYK", "CMYK"
	}

	profile := NewPDFDict()
	profile.Entries["N"] = PDFInteger(wantN)
	profile.Entries["Alternate"] = PDFName{Value: alternate}
	profile.HasStream = true
	profile.RawStream = withICCColorSpace(colorSpaceSig)

	intent := NewPDFDict()
	intent.Entries["Type"] = PDFName{Value: "OutputIntent"}
	intent.Entries["S"] = PDFName{Value: "GTS_PDFA1"}
	intent.Entries["OutputConditionIdentifier"] = PDFString{Value: identifier}
	intent.Entries["Info"] = PDFString{Value: identifier + " (placeholder ICC profile injected by gopdfrab)"}
	intent.Entries["DestOutputProfile"] = profile

	root.Entries["OutputIntents"] = PDFArray{intent}
	trailer.Entries["Root"] = root
	return nil
}

func withICCColorSpace(sig string) []byte {
	out := make([]byte, len(srgbICCProfile))
	copy(out, srgbICCProfile)
	copy(out[16:20], sig)
	return out
}

// dominantColourModel returns "rgb" or "cmyk" based on which has the higher usage count,
// returning "" if neither is used. It ignores "gray" since any OutputIntent covers it,
// and checks keys in a fixed order to ensure tie-breakers are deterministic.
func dominantColourModel(usage map[string]int) string {
	best := ""
	for _, model := range [...]string{"rgb", "cmyk"} {
		if usage[model] > 0 && (best == "" || usage[model] > usage[best]) {
			best = model
		}
	}
	return best
}

func resourcesOf(dict, fallback PDFDict) PDFDict {
	if res, ok := dict.Entries["Resources"].(PDFDict); ok {
		return res
	}
	return fallback
}

// detectColourModelUsage counts how often RGB, Gray, and CMYK color models appear in the document graph.
// It checks dictionary-level color spaces everywhere, but only counts content-stream operators and inline
// images where they are actually used.
func detectColourModelUsage(trailer PDFDict) map[string]int {
	counts := map[string]int{}

	countModelExempt := func(model string, resources PDFDict) {
		if model == "" || defaultColorSpaceDefined(model, resources) {
			return
		}
		counts[model]++
	}

	countModel := func(name string, resources PDFDict) {
		if m, ok := inlineCSAbbrev[name]; ok {
			countModelExempt(m, resources)
			return
		}
		countModelExempt(namedColourModel(PDFName{Value: name}, resources), resources)
	}

	scanVisited := map[uintptr]bool{}

	var scan func(dict, resources PDFDict)
	scan = func(dict, resources PDFDict) {
		ptr := pdfValuePointer(dict.Entries)
		if scanVisited[ptr] {
			return
		}
		scanVisited[ptr] = true

		data, err := decodeStream(dict)
		if err != nil {
			return
		}
		newContentScanner(data).scan(func(op string, operands []PDFValue) {
			switch op {
			case "rg", "RG":
				countModelExempt("rgb", resources)
			case "g", "G":
				countModelExempt("gray", resources)
			case "k", "K":
				countModelExempt("cmyk", resources)
			case "cs", "CS":
				if len(operands) != 1 {
					return
				}
				if name, ok := operands[0].(PDFName); ok {
					countModel(name.Value, resources)
				}
			case "INLINEIMAGE":
				for i := 0; i+1 < len(operands); i += 2 {
					key, ok := operands[i].(PDFName)
					if !ok || (key.Value != "CS" && key.Value != "ColorSpace") {
						continue
					}
					if name, ok := operands[i+1].(PDFName); ok {
						countModel(name.Value, resources)
					}
				}
			case "Do":
				if len(operands) != 1 {
					return
				}
				name, ok := operands[0].(PDFName)
				if !ok {
					return
				}
				xobjects, _ := resources.Entries["XObject"].(PDFDict)
				if form, ok := xobjects.Entries[name.Value].(PDFDict); ok && form.HasStream {
					scan(form, resourcesOf(form, resources))
				}
			case "scn", "SCN":
				if len(operands) == 0 {
					return
				}
				name, ok := operands[len(operands)-1].(PDFName)
				if !ok {
					return
				}
				patterns, _ := resources.Entries["Pattern"].(PDFDict)
				if pat, ok := patterns.Entries[name.Value].(PDFDict); ok && pat.HasStream {
					scan(pat, resourcesOf(pat, resources))
				}
			}
		})
	}

	walkDicts(trailer, map[uintptr]bool{}, func(d PDFDict) {
		if model := deviceColourModel(d.Entries["ColorSpace"]); model != "" {
			counts[model]++
		}

		for _, v := range d.Entries {
			arr, ok := v.(PDFArray)
			if !ok || len(arr) < 3 {
				continue
			}
			head, ok := arr[0].(PDFName)
			if !ok || (head.Value != "Separation" && head.Value != "DeviceN") {
				continue
			}
			if model := deviceColourModel(arr[2]); model != "" {
				counts[model]++
			}
		}

		resources, _ := d.Entries["Resources"].(PDFDict)

		if EqualPDFValue(d.Entries["Type"], PDFName{Value: "Page"}) {
			switch contents := d.Entries["Contents"].(type) {
			case PDFDict:
				if contents.HasStream {
					scan(contents, resources)
				}
			case PDFArray:
				for _, item := range contents {
					if cd, ok := item.(PDFDict); ok && cd.HasStream {
						scan(cd, resources)
					}
				}
			}
			return
		}
		if EqualPDFValue(d.Entries["Type"], PDFName{Value: "Font"}) &&
			EqualPDFValue(d.Entries["Subtype"], PDFName{Value: "Type3"}) {
			if procs, ok := d.Entries["CharProcs"].(PDFDict); ok {
				for _, proc := range procs.Entries {
					if pd, ok := proc.(PDFDict); ok && pd.HasStream {
						scan(pd, resources)
					}
				}
			}
		}
	})

	return counts
}

// validPDFAOutputIntentN returns the /N value of the first OutputIntent that meets all PDF/A-1 and ICC profile checks.
// If multiple intents exist, they must use the same profile object, or the entire array is treated as invalid.
func validPDFAOutputIntentN(root PDFDict) (n int, ok bool) {
	intents, ok := root.Entries["OutputIntents"].(PDFArray)
	if !ok {
		return 0, false
	}

	var firstProfile PDFValue
	for _, v := range intents {
		intent, ok := v.(PDFDict)
		if !ok {
			continue
		}
		profile := intent.Entries["DestOutputProfile"]
		if profile == nil {
			continue
		}
		if firstProfile == nil {
			firstProfile = profile
		} else if !EqualPDFValue(firstProfile, profile) {
			return 0, false
		}
	}

	for _, v := range intents {
		intent, ok := v.(PDFDict)
		if !ok {
			continue
		}
		if !EqualPDFValue(intent.Entries["S"], PDFName{Value: "GTS_PDFA1"}) {
			continue
		}
		if intent.Entries["OutputConditionIdentifier"] == nil {
			continue
		}
		profile, ok := intent.Entries["DestOutputProfile"].(PDFDict)
		if !ok || !profile.HasStream {
			continue
		}
		nVal, ok := profile.Entries["N"].(PDFInteger)
		if !ok {
			continue
		}
		switch int(nVal) {
		case 1, 3, 4:
		default:
			continue
		}
		if validateICCProfileStream(profile) != nil {
			continue
		}
		return int(nVal), true
	}
	return 0, false
}
