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
			xobjects, _ := resources.Entries.Get("XObject").(pdf.PDFDict)
			if xobj, ok := xobjects.Entries.Get(name.Value).(pdf.PDFDict); ok && xobj.HasStream {
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
			patterns, _ := resources.Entries.Get("Pattern").(pdf.PDFDict)
			if pat, ok := patterns.Entries.Get(name.Value).(pdf.PDFDict); ok && pat.HasStream {
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
		if (d.Entries.Get("Type") != pdf.PDFName{Value: "Page"}) {
			return
		}
		resources, _ := d.Entries.Get("Resources").(pdf.PDFDict)
		used := pageDeviceColourModels(d, resources, f.decode)

		needRGB := used["rgb"] && !rgbCovered && !verify.DefaultColorSpaceDefined("rgb", resources)
		needCMYK := used["cmyk"] && !cmykCovered && !verify.DefaultColorSpaceDefined("cmyk", resources)
		// Anything with its own resource dict -- an appearance stream, a form
		// XObject -- is checked in that scope, so the page's Default* never
		// reaches it. Those need the entry regardless of what the page carries.
		nestedRGB := used["rgb"] && !rgbCovered
		nestedCMYK := used["cmyk"] && !cmykCovered
		if !needRGB && !needCMYK && !nestedRGB && !nestedCMYK {
			return
		}

		if (needRGB || nestedRGB) && sharedRGB == nil {
			sharedRGB = iccBasedColourSpace(3, srgbICCProfile)
		}
		if (needCMYK || nestedCMYK) && sharedCMYK == nil {
			sharedCMYK = iccBasedColourSpace(4, cmykICCProfile)
		}

		if needRGB || needCMYK {
			csDict, ok := resources.Entries.Get("ColorSpace").(pdf.PDFDict)
			if !ok {
				csDict = pdf.NewPDFDict()
			}
			if needRGB {
				csDict.Entries.Set("DefaultRGB", sharedRGB)
			}
			if needCMYK {
				csDict.Entries.Set("DefaultCMYK", sharedCMYK)
			}
			resources.Entries.Set("ColorSpace", csDict)
			d.Entries.Set("Resources", resources)
			changed = true
		}

		// Form XObjects the page draws carry their own resources, and the
		// page's Default* does not apply inside them.
		if nestedRGB || nestedCMYK {
			in := defaultCSInjection{
				needRGB: nestedRGB, needCMYK: nestedCMYK,
				rgb: sharedRGB, cmyk: sharedCMYK,
				decode: f.decode, visited: map[uintptr]bool{},
			}
			forEachContentStream(d, func(cs pdf.PDFDict) {
				if in.run(cs, resources) {
					changed = true
				}
			})
		}

		// Appearance streams have their own resource dicts and are checked
		// independently by strict verifiers, so also inject Default* there.
		if annots, ok := d.Entries.Get("Annots").(pdf.PDFArray); ok {
			for _, item := range annots {
				annot, ok := item.(pdf.PDFDict)
				if !ok {
					continue
				}
				ap, ok := annot.Entries.Get("AP").(pdf.PDFDict)
				if !ok {
					continue
				}
				if fixAPColour(ap.Entries.Get("N"), nestedRGB, nestedCMYK, sharedRGB, sharedCMYK, f.decode) {
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
	in := defaultCSInjection{
		needRGB: needRGB, needCMYK: needCMYK,
		rgb: sharedRGB, cmyk: sharedCMYK,
		decode: decode, visited: map[uintptr]bool{},
		// An appearance stream stands on its own resources, so it can be given
		// a resource dict it did not have.
		create: true,
	}
	changed := false
	switch v := n.(type) {
	case pdf.PDFDict:
		if v.HasStream {
			changed = in.run(v, resourcesOf(v, pdf.PDFDict{})) || changed
		} else {
			for k, sv := range v.Entries.All() {
				if k == "_ref" {
					continue
				}
				if sd, ok := sv.(pdf.PDFDict); ok && sd.HasStream {
					changed = in.run(sd, resourcesOf(sd, pdf.PDFDict{})) || changed
				}
			}
		}
	}
	return changed
}

// forEachContentStream calls fn for each of a page's content streams, which
// /Contents holds either as one stream or as an array of them.
func forEachContentStream(page pdf.PDFDict, fn func(pdf.PDFDict)) {
	switch contents := page.Entries.Get("Contents").(type) {
	case pdf.PDFDict:
		if contents.HasStream {
			fn(contents)
		}
	case pdf.PDFArray:
		for _, item := range contents {
			if cd, ok := item.(pdf.PDFDict); ok && cd.HasStream {
				fn(cd)
			}
		}
	}
}

// defaultCSInjection carries one Default* injection down a content stream and
// the form XObjects it invokes, so verifiers that read Default* from the
// resource dict in force -- rather than from the page above it -- see the
// entry where they look for it.
type defaultCSInjection struct {
	needRGB, needCMYK bool
	rgb, cmyk         pdf.PDFArray
	decode            decodeFunc
	visited           map[uintptr]bool
	// create allows a stream that has no /Resources of its own to be given
	// one. A form XObject inside a page must not be: it reads the page's
	// resources for every name it uses, so handing it a resource dict holding
	// nothing but a colour space would cut it off from all of them. Its
	// content is read in the page's scope anyway, which is where the page-level
	// injection put the same entry.
	create bool
}

// run injects into stream's own resources and follows every form XObject it
// draws, resolving names in scope -- the stream's own /Resources when it has
// them, the enclosing dict's otherwise.
func (in defaultCSInjection) run(stream, scope pdf.PDFDict) bool {
	ptr := pdf.ValuePointer(stream.Entries)
	if in.visited[ptr] {
		return false
	}
	in.visited[ptr] = true

	changed := false
	if _, own := stream.Entries.Get("Resources").(pdf.PDFDict); own || in.create {
		changed = injectDefaultCS(stream, in.needRGB, in.needCMYK, in.rgb, in.cmyk)
	}

	decode := in.decode
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
		xobjects, _ := scope.Entries.Get("XObject").(pdf.PDFDict)
		if xobj, ok := xobjects.Entries.Get(name.Value).(pdf.PDFDict); ok && xobj.HasStream {
			if in.run(xobj, resourcesOf(xobj, scope)) {
				changed = true
			}
		}
	})
	return changed
}

// injectDefaultCS injects missing Default* colour-space entries into the
// /Resources/ColorSpace dict of a stream dictionary.
func injectDefaultCS(stream pdf.PDFDict, needRGB, needCMYK bool, sharedRGB, sharedCMYK pdf.PDFArray) bool {
	res, _ := stream.Entries.Get("Resources").(pdf.PDFDict)
	if res.Entries == nil {
		res = pdf.NewPDFDict()
	}
	cs, _ := res.Entries.Get("ColorSpace").(pdf.PDFDict)
	if cs.Entries == nil {
		cs = pdf.NewPDFDict()
	}
	changed := false
	if needRGB && !verify.DefaultColorSpaceDefined("rgb", res) {
		cs.Entries.Set("DefaultRGB", sharedRGB)
		changed = true
	}
	if needCMYK && !verify.DefaultColorSpaceDefined("cmyk", res) {
		cs.Entries.Set("DefaultCMYK", sharedCMYK)
		changed = true
	}
	if changed {
		res.Entries.Set("ColorSpace", cs)
		stream.Entries.Set("Resources", res)
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

	switch contents := page.Entries.Get("Contents").(type) {
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

		if xobjects, ok := res.Entries.Get("XObject").(pdf.PDFDict); ok {
			for _, v := range xobjects.Entries.All() {
				xobj, ok := v.(pdf.PDFDict)
				if !ok {
					continue
				}
				switch xobj.Entries.Get("Subtype") {
				case pdf.PDFName{Value: "Image"}:
					addModel(verify.DeviceColourModel(xobj.Entries.Get("ColorSpace")))
				case pdf.PDFName{Value: "Form"}:
					scanResourceColour(resourcesOf(xobj, res))
				}
			}
		}
		if shadings, ok := res.Entries.Get("Shading").(pdf.PDFDict); ok {
			for _, v := range shadings.Entries.All() {
				if sh, ok := v.(pdf.PDFDict); ok {
					addModel(verify.DeviceColourModel(sh.Entries.Get("ColorSpace")))
				}
			}
		}
		if patterns, ok := res.Entries.Get("Pattern").(pdf.PDFDict); ok {
			for _, v := range patterns.Entries.All() {
				pat, ok := v.(pdf.PDFDict)
				if !ok {
					continue
				}
				if sh, ok := pat.Entries.Get("Shading").(pdf.PDFDict); ok {
					addModel(verify.DeviceColourModel(sh.Entries.Get("ColorSpace")))
				}
				scanResourceColour(resourcesOf(pat, res))
			}
		}
	}
	scanResourceColour(resources)

	// Appearance streams (reached via /AP/N, not via Do from page content)
	// are rendered as part of the page and must also be colour-clean.
	if annots, ok := page.Entries.Get("Annots").(pdf.PDFArray); ok {
		for _, item := range annots {
			annot, ok := item.(pdf.PDFDict)
			if !ok {
				continue
			}
			ap, ok := annot.Entries.Get("AP").(pdf.PDFDict)
			if !ok {
				continue
			}
			scanAPAppearance(ap.Entries.Get("N"), claim, decode, emit)
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
		apRes, _ := v.Entries.Get("Resources").(pdf.PDFDict)
		scanContentColour(v, apRes, claim, decode, emit)
		return
	}
	for k, sv := range v.Entries.All() {
		if k == "_ref" {
			continue
		}
		if sd, ok := sv.(pdf.PDFDict); ok && sd.HasStream {
			apRes, _ := sd.Entries.Get("Resources").(pdf.PDFDict)
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
