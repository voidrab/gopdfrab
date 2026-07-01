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

// deviceColourFixer remediates DeviceColourContentStream (content-stream
// operators/inline images) and DeviceColourSpaceUsage (Image/Shading
// /ColorSpace entries), mirroring reportContentColour/checkDeviceColour in
// checks_content.go/checks_colour.go -- both consult a page's /Resources
// (directly, or via ctx.pageResources as a fallback for nested Form
// XObjects/patterns), so injecting the missing Default* there clears both.
// deviceColourFixer holds an optional per-run decode cache to avoid inflating
// the same content stream more than once across fixer iterations.
type deviceColourFixer struct {
	cache map[pdf.StreamKey][]byte
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
		used := pageDeviceColourModels(d, resources, f.cache)

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
				if fixAPColour(ap.Entries["N"], apNeedRGB, apNeedCMYK, sharedRGB, sharedCMYK, f.cache) {
					changed = true
				}
			}
		}
	})
	return changed, nil
}

// fixAPColour injects Default* colour spaces into the resource dict of each
// appearance stream under an /AP /N entry, including nested form XObjects.
func fixAPColour(n pdf.PDFValue, needRGB, needCMYK bool, sharedRGB, sharedCMYK pdf.PDFArray, cache map[pdf.StreamKey][]byte) bool {
	visited := map[uintptr]bool{}
	changed := false
	switch v := n.(type) {
	case pdf.PDFDict:
		if v.HasStream {
			changed = injectDefaultCSRecursive(v, needRGB, needCMYK, sharedRGB, sharedCMYK, visited, cache) || changed
		} else {
			for k, sv := range v.Entries {
				if k == "_ref" {
					continue
				}
				if sd, ok := sv.(pdf.PDFDict); ok && sd.HasStream {
					changed = injectDefaultCSRecursive(sd, needRGB, needCMYK, sharedRGB, sharedCMYK, visited, cache) || changed
				}
			}
		}
	}
	return changed
}

// injectDefaultCSRecursive injects Default* into a stream and any form XObjects
// it invokes via Do, so verifiers that don't use parent-resource inheritance
// (e.g. veraPDF) see the Default* in each XObject's own resource dict.
func injectDefaultCSRecursive(stream pdf.PDFDict, needRGB, needCMYK bool, sharedRGB, sharedCMYK pdf.PDFArray, visited map[uintptr]bool, cache map[pdf.StreamKey][]byte) bool {
	ptr := pdf.ValuePointer(stream.Entries)
	if visited[ptr] {
		return false
	}
	visited[ptr] = true

	changed := injectDefaultCS(stream, needRGB, needCMYK, sharedRGB, sharedCMYK)

	res, _ := stream.Entries["Resources"].(pdf.PDFDict)
	var data []byte
	var err error
	if cache != nil {
		data, err = pdf.DecodeCached(stream, cache)
	} else {
		data, err = pdf.DecodeStream(stream)
	}
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
			if injectDefaultCSRecursive(xobj, needRGB, needCMYK, sharedRGB, sharedCMYK, visited, cache) {
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
func pageDeviceColourModels(page pdf.PDFDict, resources pdf.PDFDict, cache map[pdf.StreamKey][]byte) map[string]bool {
	used := map[string]bool{}
	addModel := func(m string) {
		if m == "rgb" || m == "cmyk" {
			used[m] = true
		}
	}

	contentVisited := map[uintptr]bool{}
	var scanContentFor func(dict, res pdf.PDFDict)
	scanContentFor = func(dict, res pdf.PDFDict) {
		ptr := pdf.ValuePointer(dict.Entries)
		if contentVisited[ptr] {
			return
		}
		contentVisited[ptr] = true
		var data []byte
		var err error
		if cache != nil {
			data, err = pdf.DecodeCached(dict, cache)
		} else {
			data, err = pdf.DecodeStream(dict)
		}
		if err != nil {
			return
		}
		pdf.NewContentScanner(data).Scan(func(op string, operands []pdf.PDFValue) {
			switch op {
			case "rg", "RG":
				addModel("rgb")
			case "k", "K":
				addModel("cmyk")
			case "cs", "CS":
				if len(operands) == 0 {
					return
				}
				if name, ok := operands[len(operands)-1].(pdf.PDFName); ok {
					addModel(namedOrAbbrevColourModel(name.Value, res))
				}
			case "INLINEIMAGE":
				for i := 0; i+1 < len(operands); i += 2 {
					key, ok := operands[i].(pdf.PDFName)
					if !ok || (key.Value != "CS" && key.Value != "ColorSpace") {
						continue
					}
					switch val := operands[i+1].(type) {
					case pdf.PDFName:
						addModel(namedOrAbbrevColourModel(val.Value, res))
					case pdf.PDFArray:
						addModel(verify.DeviceColourModel(val))
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
				xobjects, _ := res.Entries["XObject"].(pdf.PDFDict)
				if xobj, ok := xobjects.Entries[name.Value].(pdf.PDFDict); ok && xobj.HasStream {
					scanContentFor(xobj, resourcesOf(xobj, res))
				}
			case "scn", "SCN":
				if len(operands) == 0 {
					return
				}
				name, ok := operands[len(operands)-1].(pdf.PDFName)
				if !ok {
					return
				}
				patterns, _ := res.Entries["Pattern"].(pdf.PDFDict)
				pat, ok := patterns.Entries[name.Value].(pdf.PDFDict)
				if !ok {
					return
				}
				if pat.HasStream {
					scanContentFor(pat, resourcesOf(pat, res))
				}
				if sh, ok := pat.Entries["Shading"].(pdf.PDFDict); ok {
					addModel(verify.DeviceColourModel(sh.Entries["ColorSpace"]))
				}
			}
		})
	}
	switch contents := page.Entries["Contents"].(type) {
	case pdf.PDFDict:
		if contents.HasStream {
			scanContentFor(contents, resources)
		}
	case pdf.PDFArray:
		for _, item := range contents {
			if cd, ok := item.(pdf.PDFDict); ok && cd.HasStream {
				scanContentFor(cd, resources)
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
			scanAPAppearance(ap.Entries["N"], contentVisited, addModel, cache)
		}
	}

	return used
}

// scanAPAppearance scans one /AP /N entry (a single stream or a subdictionary
// of appearance states) for device colour operators.
func scanAPAppearance(n pdf.PDFValue, visited map[uintptr]bool, addModel func(string), cache map[pdf.StreamKey][]byte) {
	switch v := n.(type) {
	case pdf.PDFDict:
		if v.HasStream {
			apRes, _ := v.Entries["Resources"].(pdf.PDFDict)
			scanAPStream(v, apRes, visited, addModel, cache)
		} else {
			for k, sv := range v.Entries {
				if k == "_ref" {
					continue
				}
				if sd, ok := sv.(pdf.PDFDict); ok && sd.HasStream {
					apRes, _ := sd.Entries["Resources"].(pdf.PDFDict)
					scanAPStream(sd, apRes, visited, addModel, cache)
				}
			}
		}
	}
}

// scanAPStream scans one appearance-stream dict (and any form XObjects it
// invokes via Do) for device colour operators.
func scanAPStream(dict, res pdf.PDFDict, visited map[uintptr]bool, addModel func(string), cache map[pdf.StreamKey][]byte) {
	ptr := pdf.ValuePointer(dict.Entries)
	if visited[ptr] {
		return
	}
	visited[ptr] = true
	var data []byte
	var err error
	if cache != nil {
		data, err = pdf.DecodeCached(dict, cache)
	} else {
		data, err = pdf.DecodeStream(dict)
	}
	if err != nil {
		return
	}
	pdf.NewContentScanner(data).Scan(func(op string, operands []pdf.PDFValue) {
		switch op {
		case "rg", "RG":
			addModel("rgb")
		case "k", "K":
			addModel("cmyk")
		case "cs", "CS":
			if len(operands) == 0 {
				return
			}
			if name, ok := operands[len(operands)-1].(pdf.PDFName); ok {
				addModel(namedOrAbbrevColourModel(name.Value, res))
			}
		case "Do":
			if len(operands) != 1 {
				return
			}
			name, ok := operands[0].(pdf.PDFName)
			if !ok {
				return
			}
			xobjects, _ := res.Entries["XObject"].(pdf.PDFDict)
			if xobj, ok := xobjects.Entries[name.Value].(pdf.PDFDict); ok && xobj.HasStream {
				scanAPStream(xobj, resourcesOf(xobj, res), visited, addModel, cache)
			}
		}
	})
}

// namedOrAbbrevColourModel resolves a cs/CS or inline-image /CS operand name
// to a device model, trying the inline-image abbreviations first.
func namedOrAbbrevColourModel(name string, resources pdf.PDFDict) string {
	if m, ok := verify.InlineCSAbbrev[name]; ok {
		return m
	}
	return verify.NamedColourModel(pdf.PDFName{Value: name}, resources)
}
