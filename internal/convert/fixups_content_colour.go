package convert

import (
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"

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

// decodeFunc supplies a stream's decoded bytes; a nil value falls back to the
// uncached pdf.DecodeStream. Convert wires the Reader's concurrent cache here
// so every colour scan in a run shares one decoded-stream cache.
type decodeFunc func(pdf.PDFDict) ([]byte, error)

// colourEmitter receives each device colour model a scan encounters, with the
// resources in scope so callers can apply their own Default* exemption.
type colourEmitter func(model string, resources pdf.PDFDict)

// scanContentColour tokenizes one content stream and reports every device
// colour model its operators use, recursing into the form XObjects it draws
// and into tiling patterns. claim dedups shared streams; decode supplies the
// bytes.
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
				emit(patternBaseModel(name, resources), resources)
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
			if xobj, ok := xobjects.Entries.Get(name.Value).(pdf.PDFDict); ok && xobj.IsStream() {
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
			if pat, ok := patterns.Entries.Get(name.Value).(pdf.PDFDict); ok && pat.IsStream() {
				scanContentColour(pat, resourcesOf(pat, resources), claim, decode, emit)
			}
		case "Tf":
			// A Type 3 font's glyph procedures are scanned with the font's
			// own Resources when it has them, and otherwise with the
			// resources the text is shown in.
			if len(operands) == 0 {
				return
			}
			font, ok := namedType3Font(operands[0], resources)
			if !ok || !claim(pdf.ValuePointer(font.Entries)) {
				return
			}
			pdf.ContentRoots(font, func(r pdf.ContentRoot) {
				scanContentColour(r.Stream, resourcesOf(font, resources), claim, decode, emit)
			})
		}
	})
}

// deviceColourFixer remediates DeviceColourContentStream (device colour in
// content stream operators and inline images), DeviceColourSpaceUsage (device
// colour spaces of Image and Shading dictionaries) and DeviceColourBase (the
// base space of an Indexed or uncoloured Pattern space). It mirrors
// reportContentColour and checkDeviceColour in the verify package, which both
// consult the page's Resources (or the page resources, for nested Form XObjects
// and patterns), so injecting the missing Default colour space entries there
// clears both.
// deviceColourFixer holds the run's decode function (the Reader's concurrent
// cache) so repeated content scans across fixer iterations hit warm bytes.
type deviceColourFixer struct {
	decode decodeFunc
	spec   *pdfa.Spec
}

func (deviceColourFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Colour.DeviceColourContentStream, pdf.Checks.Colour.DeviceColourSpaceUsage,
		pdf.Checks.Colour.DeviceColourBase:
		return true
	}
	return false
}

func (f deviceColourFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	_, rgbCovered, cmykCovered := outputIntentCoverage(*trailer, f.spec.OutputIntentSubtype)

	changed := false
	// Built lazily and reused across every page that needs it, so the
	// writer emits one shared ICC stream object rather than one per page.
	var sharedRGB, sharedCMYK pdf.PDFArray
	adopted := map[uintptr]map[uintptr]pdf.PDFDict{}

	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if (d.Entries.Get("Type") != pdf.PDFName{Value: "Page"}) {
			return
		}
		resources := pdf.PageResources(d)
		used := pageDeviceColourModels(d, resources, f.decode)

		needRGB := used["rgb"] && !rgbCovered && !verify.DefaultColorSpaceDefined("rgb", resources)
		needCMYK := used["cmyk"] && !cmykCovered && !verify.DefaultColorSpaceDefined("cmyk", resources)
		// Anything with its own resource dict -- an appearance stream,
		// a form XObject -- is checked in that scope, so the page's
		// Default* never reaches it. Those need the entry regardless of
		// what the page carries.
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
				decode: f.decode, visited: map[uintptr]bool{}, adopted: adopted,
			}
			forEachContentStream(d, func(cs pdf.PDFDict) {
				if in.follow(cs, resources) {
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
	}
	changed := false
	switch v := n.(type) {
	case pdf.PDFDict:
		if v.IsStream() {
			changed = in.run(v, resourcesOf(v, pdf.PDFDict{})) || changed
		} else {
			for _, sv := range v.Entries.All() {
				if sd, ok := sv.(pdf.PDFDict); ok && sd.IsStream() {
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
		if contents.IsStream() {
			fn(contents)
		}
	case pdf.PDFArray:
		for _, item := range contents {
			if cd, ok := item.(pdf.PDFDict); ok && cd.IsStream() {
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
	// adopted holds, per holder that had no /Resources, the copy of it that
	// serves each scope it is drawn in. It is shared across a document's pages;
	// nil leaves holders as they are found.
	adopted map[uintptr]map[uintptr]pdf.PDFDict
}

// run injects into the resources of stream, a form XObject or tiling pattern,
// and follows what it draws. scope holds the names the stream reads: its own
// /Resources when it has them, the enclosing dict's otherwise.
func (in defaultCSInjection) run(stream, scope pdf.PDFDict) bool {
	if in.visited[pdf.ValuePointer(stream.Entries)] {
		return false
	}
	changed := in.adoptResources(stream, scope)
	changed = injectDefaultCS(stream, in.needRGB, in.needCMYK, in.rgb, in.cmyk) || changed
	return in.follow(stream, scope) || changed
}

// holderIn returns the dictionary to work on for holder, which scope's category
// resources name. A holder without /Resources reads its names from where it is
// drawn, so adoption must not tie it to the first scope: each further scope gets
// a copy, which takes the holder's place in that scope.
func (in defaultCSInjection) holderIn(holder, scope pdf.PDFDict, category, name string) pdf.PDFDict {
	if in.adopted == nil {
		return holder
	}
	ptr, scopePtr := pdf.ValuePointer(holder.Entries), pdf.ValuePointer(scope.Entries)
	copies, adopted := in.adopted[ptr]
	if !adopted {
		if _, own := holder.Entries.Get("Resources").(pdf.PDFDict); !own {
			in.adopted[ptr] = map[uintptr]pdf.PDFDict{scopePtr: holder}
		}
		return holder
	}
	held, ok := copies[scopePtr]
	if !ok {
		held = pdf.PDFDict{Entries: holder.Entries.CloneAsNew()}
		held.Entries.Del("Resources")
		copies[scopePtr] = held
		in.adopted[pdf.ValuePointer(held.Entries)] = copies
	}
	if named, ok := scope.Entries.Get(category).(pdf.PDFDict); ok {
		named.Entries.Set(name, held)
	}
	return held
}

// adoptResources copies into a holder without /Resources the resources it reads
// from scope, reporting whether it changed anything. A Default colour space applies
// only in a holder's own resources, and a dictionary holding nothing else would
// cut it off from every name it uses.
func (in defaultCSInjection) adoptResources(holder, scope pdf.PDFDict) bool {
	if _, own := holder.Entries.Get("Resources").(pdf.PDFDict); own {
		return false
	}
	return inheritedResourcesFixer{decode: in.decode}.adopt(holder, scope, map[uintptr]bool{})
}

// follow injects into every form XObject and tiling pattern that stream draws
// and every Type 3 font it selects. It does not inject into stream itself,
// because a page's content and a glyph procedure read their holder's resources.
func (in defaultCSInjection) follow(stream, scope pdf.PDFDict) bool {
	ptr := pdf.ValuePointer(stream.Entries)
	if in.visited[ptr] {
		return false
	}
	in.visited[ptr] = true

	changed := false
	decode := in.decode
	if decode == nil {
		decode = pdf.DecodeStream
	}
	data, err := decode(stream)
	if err != nil {
		return changed
	}
	pdf.NewContentScanner(data).Scan(func(op string, operands []pdf.PDFValue) {
		// The same streams scanContentColour descends into: form
		// XObjects the stream draws and tiling patterns, each checked
		// in its own resources.
		var category string
		switch op {
		case "Do":
			category = "XObject"
		case "scn", "SCN":
			category = "Pattern"
		case "Tf":
			if len(operands) > 0 && in.runType3(operands[0], scope) {
				changed = true
			}
			return
		default:
			return
		}
		if len(operands) == 0 {
			return
		}
		name, ok := operands[len(operands)-1].(pdf.PDFName)
		if !ok {
			return
		}
		named, _ := scope.Entries.Get(category).(pdf.PDFDict)
		if sub, ok := named.Entries.Get(name.Value).(pdf.PDFDict); ok && sub.IsStream() {
			sub = in.holderIn(sub, scope, category, name.Value)
			if in.run(sub, resourcesOf(sub, scope)) {
				changed = true
			}
		}
	})
	return changed
}

// runType3 injects Default colour spaces into the resources of the Type 3 font
// that a Tf operand names and follows the form XObjects its glyph procedures
// draw. The glyph procedures are checked against the font's resources, which
// the page's Default colour spaces do not reach.
func (in defaultCSInjection) runType3(operand pdf.PDFValue, scope pdf.PDFDict) bool {
	font, ok := namedType3Font(operand, scope)
	if !ok {
		return false
	}
	name, _ := operand.(pdf.PDFName)
	font = in.holderIn(font, scope, "Font", name.Value)
	ptr := pdf.ValuePointer(font.Entries)
	if in.visited[ptr] {
		return false
	}
	in.visited[ptr] = true
	changed := in.adoptResources(font, scope)
	// injectDefaultCS edits the font's resources in place.
	changed = injectDefaultCS(font, in.needRGB, in.needCMYK, in.rgb, in.cmyk) || changed
	res, _ := font.Entries.Get("Resources").(pdf.PDFDict)
	pdf.ContentRoots(font, func(r pdf.ContentRoot) {
		if in.follow(r.Stream, res) {
			changed = true
		}
	})
	return changed
}

// namedType3Font returns the Type 3 font a Tf operand names in resources.
func namedType3Font(operand pdf.PDFValue, resources pdf.PDFDict) (pdf.PDFDict, bool) {
	name, _ := operand.(pdf.PDFName)
	font, ok := pdf.LookupResource(resources, "Font", name.Value).(pdf.PDFDict)
	return font, ok && font.Entries.Get("Subtype") == (pdf.PDFName{Value: "Type3"})
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
		if contents.IsStream() {
			scanContentColour(contents, resources, claim, decode, emit)
		}
	case pdf.PDFArray:
		for _, item := range contents {
			if cd, ok := item.(pdf.PDFDict); ok && cd.IsStream() {
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

	// Appearance streams (reached via /AP/N, not drawn from page content)
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
	if v.IsStream() {
		apRes, _ := v.Entries.Get("Resources").(pdf.PDFDict)
		scanContentColour(v, apRes, claim, decode, emit)
		return
	}
	for _, sv := range v.Entries.All() {
		if sd, ok := sv.(pdf.PDFDict); ok && sd.IsStream() {
			apRes, _ := sd.Entries.Get("Resources").(pdf.PDFDict)
			scanContentColour(sd, apRes, claim, decode, emit)
		}
	}
}

// patternBaseModel returns the device colour model of the base space of the
// uncoloured Pattern colour space a cs or CS operand names, or "".
func patternBaseModel(name pdf.PDFName, resources pdf.PDFDict) string {
	cs, _ := pdf.LookupNamedColorSpace(name.Value, resources)
	if arr, ok := cs.(pdf.PDFArray); !ok || len(arr) == 0 || arr[0] != (pdf.PDFName{Value: "Pattern"}) {
		return ""
	}
	return verify.BaseColourModel(cs)
}

// namedOrAbbrevColourModel resolves a cs/CS or inline-image /CS operand name
// to a device model, trying the inline-image abbreviations first.
func namedOrAbbrevColourModel(name string, resources pdf.PDFDict) string {
	if m, ok := verify.InlineCSAbbrev[name]; ok {
		return m
	}
	return verify.NamedColourModel(pdf.PDFName{Value: name}, resources)
}
