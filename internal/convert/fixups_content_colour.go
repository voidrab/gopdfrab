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
type deviceColourFixer struct{}

func (deviceColourFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Colour.DeviceColourContentStream, pdf.Checks.Colour.DeviceColourSpaceUsage:
		return true
	}
	return false
}

func (deviceColourFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
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
		used := pageDeviceColourModels(d, resources)

		needRGB := used["rgb"] && !rgbCovered && !verify.DefaultColorSpaceDefined("rgb", resources)
		needCMYK := used["cmyk"] && !cmykCovered && !verify.DefaultColorSpaceDefined("cmyk", resources)
		if !needRGB && !needCMYK {
			return
		}

		csDict, ok := resources.Entries["ColorSpace"].(pdf.PDFDict)
		if !ok {
			csDict = pdf.NewPDFDict()
		}
		if needRGB {
			if sharedRGB == nil {
				sharedRGB = iccBasedColourSpace(3, srgbICCProfile)
			}
			csDict.Entries["DefaultRGB"] = sharedRGB
		}
		if needCMYK {
			if sharedCMYK == nil {
				sharedCMYK = iccBasedColourSpace(4, cmykICCProfile)
			}
			csDict.Entries["DefaultCMYK"] = sharedCMYK
		}
		resources.Entries["ColorSpace"] = csDict
		d.Entries["Resources"] = resources
		changed = true
	})
	return changed, nil
}

// pageDeviceColourModels returns which device colour models ("rgb"/"cmyk")
// are actually used by page's content, the Form XObjects/tiling patterns it
// invokes, and the Image/Shading colour spaces reachable from its
// resources -- mirroring reportContentColour's and checkDeviceColour's
// detection (DeviceGray is omitted: any OutputIntent already covers it, see
// deviceColourAllowed).
func pageDeviceColourModels(page pdf.PDFDict, resources pdf.PDFDict) map[string]bool {
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
		data, err := pdf.DecodeStream(dict)
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

	return used
}

// namedOrAbbrevColourModel resolves a cs/CS or inline-image /CS operand name
// to a device model, trying the inline-image abbreviations first.
func namedOrAbbrevColourModel(name string, resources pdf.PDFDict) string {
	if m, ok := verify.InlineCSAbbrev[name]; ok {
		return m
	}
	return verify.NamedColourModel(pdf.PDFName{Value: name}, resources)
}
