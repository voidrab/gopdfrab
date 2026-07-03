package convert

import (
	"github.com/voidrab/gopdfrab/internal/pdf"

	"github.com/voidrab/gopdfrab/internal/verify"
)

// This file fixes residual device-colour violations (6.2.3.3) that survive
// injectOutputIntent: a document with content in two device colour models
// (e.g. both RGB and CMYK) can only have one of them covered by the single
// OutputIntent injectOutputIntent installs. Rather than rewrite every
// colour operator in every content stream, deviceColourFixer injects a
// /DefaultRGB or /DefaultCMYK colour space into each page's /Resources --
// defaultColorSpaceDefined (checks_colour.go) treats either as sufficient
// to excuse that model, by design, without inspecting its value.

func init() {
	registerFixer(deviceColourFixer{})
}

// decodeFunc supplies a stream's decoded bytes; a nil value falls back to the
// uncached pdf.DecodeStream. Convert wires the Reader's concurrent cache here
// so every colour scan in a run shares one decoded-stream cache.
type decodeFunc func(pdf.PDFDict) ([]byte, error)

// colourEmitter receives each device colour model a scan encounters, with the
// resources in scope so callers can apply their own Default* exemption.
type colourEmitter func(model string, resources pdf.PDFDict)

// scanContentColour tokenizes one content stream and reports every device
// colour model its operators use, recursing into Do form XObjects and scn
// tiling patterns. claim dedups shared streams; decode supplies the bytes.
func scanContentColour(dict, resources pdf.PDFDict, claim func(uintptr) bool, decode decodeFunc, emit colourEmitter) {
	if !claim(pdf.ValuePointer(dict.Entries)) {
		return
	}
	if decode == nil {
		decode = pdf.DecodeStream
	}
	data, err := decode(dict)
	if err != nil {
		return
	}
	pdf.NewContentScanner(data).Scan(func(op string, operands []pdf.PDFValue) {
		switch op {
		case "rg", "RG":
			emit("rgb", resources)
		case "g", "G":
			emit("gray", resources)
		case "k", "K":
			emit("cmyk", resources)
		case "cs", "CS":
			if len(operands) == 0 {
				return
			}
			if name, ok := operands[len(operands)-1].(pdf.PDFName); ok {
				emit(namedOrAbbrevColourModel(name.Value, resources), resources)
			}
		case "INLINEIMAGE":
			for i := 0; i+1 < len(operands); i += 2 {
				key, ok := operands[i].(pdf.PDFName)
				if !ok || (key.Value != "CS" && key.Value != "ColorSpace") {
					continue
				}
				switch val := operands[i+1].(type) {
				case pdf.PDFName:
					emit(namedOrAbbrevColourModel(val.Value, resources), resources)
				case pdf.PDFArray:
					emit(verify.DeviceColourModel(val), resources)
				}
			}
		case "Do":
			if len(operands) != 1 {
				return
			}
			name, ok := operands[0].(pdf.PDFName)
			if !ok {
				return
			}
			xobjects, _ := resources.Entries["XObject"].(pdf.PDFDict)
			if xobj, ok := xobjects.Entries[name.Value].(pdf.PDFDict); ok && xobj.HasStream {
				scanContentColour(xobj, resourcesOf(xobj, resources), claim, decode, emit)
			}
		case "scn", "SCN":
			if len(operands) == 0 {
				return
			}
			name, ok := operands[len(operands)-1].(pdf.PDFName)
			if !ok {
				return
			}
			patterns, _ := resources.Entries["Pattern"].(pdf.PDFDict)
			if pat, ok := patterns.Entries[name.Value].(pdf.PDFDict); ok && pat.HasStream {
				scanContentColour(pat, resourcesOf(pat, resources), claim, decode, emit)
			}
		}
	})
}

// deviceColourFixer remediates DeviceColourContentStream (content-stream
// operators/inline images) and DeviceColourSpaceUsage (Image/Shading
// /ColorSpace entries), mirroring reportContentColour/checkDeviceColour in
// checks_content.go/checks_colour.go -- both consult a page's /Resources
// (directly, or via ctx.pageResources as a fallback for nested Form
// XObjects/patterns), so injecting the missing Default* there clears both.
// deviceColourFixer holds the run's decode function (the Reader's concurrent
// cache) so repeated content scans across fixer iterations hit warm bytes.
type deviceColourFixer struct {
	decode decodeFunc
}

func (deviceColourFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Colour.DeviceColourContentStream, pdf.Checks.Colour.DeviceColourSpaceUsage:
		return true
	}
	return false
}

func (f deviceColourFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	_, rgbCovered, cmykCovered := outputIntentCoverage(*trailer)

	changed := false
	// Built lazily and reused across every page that needs it, so the
	// writer emits one shared ICC stream object rather than one per page.
	var sharedRGB, sharedCMYK pdf.PDFArray

	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if (d.Entries["Type"] != pdf.PDFName{Value: "Page"}) {
			return
		}
		resources, _ := d.Entries["Resources"].(pdf.PDFDict)
		used := pageDeviceColourModels(d, resources, f.decode)

		needRGB := used["rgb"] && !rgbCovered && !verify.DefaultColorSpaceDefined("rgb", resources)
		needCMYK := used["cmyk"] && !cmykCovered && !verify.DefaultColorSpaceDefined("cmyk", resources)
		// Appearance streams are checked in their own resource scope by strict
		// verifiers; inject Default* regardless of whether the page already has it.
		apNeedRGB := used["rgb"] && !rgbCovered
		apNeedCMYK := used["cmyk"] && !cmykCovered
		if !needRGB && !needCMYK && !apNeedRGB && !apNeedCMYK {
			return
		}

		if (needRGB || apNeedRGB) && sharedRGB == nil {
			sharedRGB = iccBasedColourSpace(3, srgbICCProfile)
		}
		if (needCMYK || apNeedCMYK) && sharedCMYK == nil {
			sharedCMYK = iccBasedColourSpace(4, cmykICCProfile)
		}

		if needRGB || needCMYK {
			csDict, ok := resources.Entries["ColorSpace"].(pdf.PDFDict)
			if !ok {
				csDict = pdf.NewPDFDict()
			}
			if needRGB {
				csDict.Entries["DefaultRGB"] = sharedRGB
			}
			if needCMYK {
				csDict.Entries["DefaultCMYK"] = sharedCMYK
			}
			resources.Entries["ColorSpace"] = csDict
			d.Entries["Resources"] = resources
			changed = true
		}

		// Appearance streams have their own resource dicts and are checked
		// independently by strict verifiers, so also inject Default* there.
		if annots, ok := d.Entries["Annots"].(pdf.PDFArray); ok {
			for _, item := range annots {
				annot, ok := item.(pdf.PDFDict)
				if !ok {
					continue
				}
				ap, ok := annot.Entries["AP"].(pdf.PDFDict)
				if !ok {
					continue
				}
				if fixAPColour(ap.Entries["N"], apNeedRGB, apNeedCMYK, sharedRGB, sharedCMYK, f.decode) {
					changed = true
				}
			}
		}
	})
	return changed, nil
}

// fixAPColour injects Default* colour spaces into the resource dict of each
// appearance stream under an /AP /N entry, including nested form XObjects.
func fixAPColour(n pdf.PDFValue, needRGB, needCMYK bool, sharedRGB, sharedCMYK pdf.PDFArray, decode decodeFunc) bool {
	visited := map[uintptr]bool{}
	changed := false
	switch v := n.(type) {
	case pdf.PDFDict:
		if v.HasStream {
			changed = injectDefaultCSRecursive(v, needRGB, needCMYK, sharedRGB, sharedCMYK, visited, decode) || changed
		} else {
			for k, sv := range v.Entries {
				if k == "_ref" {
					continue
				}
				if sd, ok := sv.(pdf.PDFDict); ok && sd.HasStream {
					changed = injectDefaultCSRecursive(sd, needRGB, needCMYK, sharedRGB, sharedCMYK, visited, decode) || changed
				}
			}
		}
	}
	return changed
}

// injectDefaultCSRecursive injects Default* into a stream and any form XObjects
// it invokes via Do, so verifiers that don't use parent-resource inheritance
// (e.g. veraPDF) see the Default* in each XObject's own resource dict.
func injectDefaultCSRecursive(stream pdf.PDFDict, needRGB, needCMYK bool, sharedRGB, sharedCMYK pdf.PDFArray, visited map[uintptr]bool, decode decodeFunc) bool {
	ptr := pdf.ValuePointer(stream.Entries)
	if visited[ptr] {
		return false
	}
	visited[ptr] = true

	changed := injectDefaultCS(stream, needRGB, needCMYK, sharedRGB, sharedCMYK)

	res, _ := stream.Entries["Resources"].(pdf.PDFDict)
	if decode == nil {
		decode = pdf.DecodeStream
	}
	data, err := decode(stream)
	if err != nil {
		return changed
	}
	pdf.NewContentScanner(data).Scan(func(op string, operands []pdf.PDFValue) {
		if op != "Do" || len(operands) != 1 {
			return
		}
		name, ok := operands[0].(pdf.PDFName)
		if !ok {
			return
		}
		xobjects, _ := res.Entries["XObject"].(pdf.PDFDict)
		if xobj, ok := xobjects.Entries[name.Value].(pdf.PDFDict); ok && xobj.HasStream {
			if injectDefaultCSRecursive(xobj, needRGB, needCMYK, sharedRGB, sharedCMYK, visited, decode) {
				changed = true
			}
		}
	})
	return changed
}

// injectDefaultCS injects missing Default* colour-space entries into the
// /Resources/ColorSpace dict of a stream dictionary.
func injectDefaultCS(stream pdf.PDFDict, needRGB, needCMYK bool, sharedRGB, sharedCMYK pdf.PDFArray) bool {
	res, _ := stream.Entries["Resources"].(pdf.PDFDict)
	if res.Entries == nil {
		res = pdf.NewPDFDict()
	}
	cs, _ := res.Entries["ColorSpace"].(pdf.PDFDict)
	if cs.Entries == nil {
		cs = pdf.NewPDFDict()
	}
	changed := false
	if needRGB && !verify.DefaultColorSpaceDefined("rgb", res) {
		cs.Entries["DefaultRGB"] = sharedRGB
		changed = true
	}
	if needCMYK && !verify.DefaultColorSpaceDefined("cmyk", res) {
		cs.Entries["DefaultCMYK"] = sharedCMYK
		changed = true
	}
	if changed {
		res.Entries["ColorSpace"] = cs
		stream.Entries["Resources"] = res
	}
	return changed
}

// pageDeviceColourModels returns which device colour models ("rgb"/"cmyk")
// are actually used by page's content, the Form XObjects/tiling patterns it
// invokes, and the Image/Shading colour spaces reachable from its
// resources -- mirroring reportContentColour's and checkDeviceColour's
// detection (DeviceGray is omitted: any OutputIntent already covers it, see
// deviceColourAllowed).
func pageDeviceColourModels(page pdf.PDFDict, resources pdf.PDFDict, decode decodeFunc) map[string]bool {
	used := map[string]bool{}
	addModel := func(m string) {
		if m == "rgb" || m == "cmyk" {
			used[m] = true
		}
	}

	contentVisited := map[uintptr]bool{}
	claim := func(ptr uintptr) bool {
		if contentVisited[ptr] {
			return false
		}
		contentVisited[ptr] = true
		return true
	}
	emit := func(model string, _ pdf.PDFDict) { addModel(model) }

	switch contents := page.Entries["Contents"].(type) {
	case pdf.PDFDict:
		if contents.HasStream {
			scanContentColour(contents, resources, claim, decode, emit)
		}
	case pdf.PDFArray:
		for _, item := range contents {
			if cd, ok := item.(pdf.PDFDict); ok && cd.HasStream {
				scanContentColour(cd, resources, claim, decode, emit)
			}
		}
	}

	dictVisited := map[uintptr]bool{}
	var scanResourceColour func(res pdf.PDFDict)
	scanResourceColour = func(res pdf.PDFDict) {
		ptr := pdf.ValuePointer(res.Entries)
		if dictVisited[ptr] {
			return
		}
		dictVisited[ptr] = true

		if xobjects, ok := res.Entries["XObject"].(pdf.PDFDict); ok {
			for _, v := range xobjects.Entries {
				xobj, ok := v.(pdf.PDFDict)
				if !ok {
					continue
				}
				switch xobj.Entries["Subtype"] {
				case pdf.PDFName{Value: "Image"}:
					addModel(verify.DeviceColourModel(xobj.Entries["ColorSpace"]))
				case pdf.PDFName{Value: "Form"}:
					scanResourceColour(resourcesOf(xobj, res))
				}
			}
		}
		if shadings, ok := res.Entries["Shading"].(pdf.PDFDict); ok {
			for _, v := range shadings.Entries {
				if sh, ok := v.(pdf.PDFDict); ok {
					addModel(verify.DeviceColourModel(sh.Entries["ColorSpace"]))
				}
			}
		}
		if patterns, ok := res.Entries["Pattern"].(pdf.PDFDict); ok {
			for _, v := range patterns.Entries {
				pat, ok := v.(pdf.PDFDict)
				if !ok {
					continue
				}
				if sh, ok := pat.Entries["Shading"].(pdf.PDFDict); ok {
					addModel(verify.DeviceColourModel(sh.Entries["ColorSpace"]))
				}
				scanResourceColour(resourcesOf(pat, res))
			}
		}
	}
	scanResourceColour(resources)

	// Appearance streams (reached via /AP/N, not via Do from page content)
	// are rendered as part of the page and must also be colour-clean.
	if annots, ok := page.Entries["Annots"].(pdf.PDFArray); ok {
		for _, item := range annots {
			annot, ok := item.(pdf.PDFDict)
			if !ok {
				continue
			}
			ap, ok := annot.Entries["AP"].(pdf.PDFDict)
			if !ok {
				continue
			}
			scanAPAppearance(ap.Entries["N"], claim, decode, emit)
		}
	}

	return used
}

// scanAPAppearance scans one /AP /N entry (a single stream or a subdictionary
// of appearance states) for device colour operators via scanContentColour.
func scanAPAppearance(n pdf.PDFValue, claim func(uintptr) bool, decode decodeFunc, emit colourEmitter) {
	v, ok := n.(pdf.PDFDict)
	if !ok {
		return
	}
	if v.HasStream {
		apRes, _ := v.Entries["Resources"].(pdf.PDFDict)
		scanContentColour(v, apRes, claim, decode, emit)
		return
	}
	for k, sv := range v.Entries {
		if k == "_ref" {
			continue
		}
		if sd, ok := sv.(pdf.PDFDict); ok && sd.HasStream {
			apRes, _ := sd.Entries["Resources"].(pdf.PDFDict)
			scanContentColour(sd, apRes, claim, decode, emit)
		}
	}
}

// namedOrAbbrevColourModel resolves a cs/CS or inline-image /CS operand name
// to a device model, trying the inline-image abbreviations first.
func namedOrAbbrevColourModel(name string, resources pdf.PDFDict) string {
	if m, ok := verify.InlineCSAbbrev[name]; ok {
		return m
	}
	return verify.NamedColourModel(pdf.PDFName{Value: name}, resources)
}
