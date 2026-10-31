package convert

import (
	"math"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// --- ISO 19005-2 6.2.2: resources a content stream inherits ---

// inheritedResourcesFixer gives every content stream the resources it uses
// as its own. A page that inherits its Resources from the page tree gets
// them set on itself. A form XObject, tiling pattern or Type 3 font gets each
// resource it uses but finds only in the resources it is drawn from copied
// into its own Resources dictionary, which is created when it has none.
type inheritedResourcesFixer struct{ decode decodeFunc }

func (inheritedResourcesFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Colour.InheritedResources
}

func (f inheritedResourcesFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false
	visited := map[uintptr]bool{}
	for _, p := range orderedPages(*trailer) {
		page := p.dict
		res, own := page.Entries.Get("Resources").(pdf.PDFDict)
		if !own {
			res = pdf.PageResources(page)
			if res.Entries == nil {
				res = pdf.NewPDFDict()
			}
			page.Entries.Set("Resources", res)
			changed = true
		}
		forEachContentStream(page, func(cs pdf.PDFDict) {
			if f.followChildren(cs, res, visited) {
				changed = true
			}
		})
		annots, _ := page.Entries.Get("Annots").(pdf.PDFArray)
		for _, item := range annots {
			annot, _ := item.(pdf.PDFDict)
			ap, _ := annot.Entries.Get("AP").(pdf.PDFDict)
			for _, s := range appearanceStreams(ap.Entries.Get("N")) {
				// An appearance stream is checked like a form drawn on
				// the page, so it inherits from the page's resources.
				if f.adopt(s, res, visited) {
					changed = true
				}
			}
		}
	}
	return changed, nil
}

// appearanceStreams returns the streams of an appearance (AP) entry: the entry
// itself, or each stream of its subdictionary of appearance states.
func appearanceStreams(n pdf.PDFValue) []pdf.PDFDict {
	d, ok := n.(pdf.PDFDict)
	if !ok {
		return nil
	}
	if d.IsStream() {
		return []pdf.PDFDict{d}
	}
	var out []pdf.PDFDict
	for _, v := range d.Entries.All() {
		if s, ok := v.(pdf.PDFDict); ok && s.IsStream() {
			out = append(out, s)
		}
	}
	return out
}

// followChildren adopts, for each form XObject, tiling pattern and Type 3
// font the stream draws with, the resources it inherits from scope.
func (f inheritedResourcesFixer) followChildren(stream, scope pdf.PDFDict, visited map[uintptr]bool) bool {
	var children []resourceName
	f.scan(stream, func(op string, operands []pdf.PDFValue) {
		verify.ResourceNames(op, operands, func(category, name string) {
			children = append(children, resourceName{category, name})
		})
	})
	return f.adoptChildren(children, scope, visited)
}

// resourceName is a resource a content stream names.
type resourceName struct{ category, name string }

// adoptChildren adopts, for each of names that is a form XObject, tiling
// pattern or Type 3 font in scope, the resources it inherits from scope.
func (f inheritedResourcesFixer) adoptChildren(names []resourceName, scope pdf.PDFDict, visited map[uintptr]bool) bool {
	changed := false
	for _, n := range names {
		child, ok := pdf.LookupResource(scope, n.category, n.name).(pdf.PDFDict)
		if !ok {
			continue
		}
		switch pdf.ContentRoots(child, func(pdf.ContentRoot) {}) {
		case pdf.ContentForm, pdf.ContentPattern, pdf.ContentType3:
			if f.adopt(child, scope, visited) {
				changed = true
			}
		}
	}
	return changed
}

// adopt copies into holder's own resources each resource its content uses
// that only scope has, then does the same for what it draws with in turn.
// holder is a form XObject, tiling pattern, Type 3 font or appearance stream.
func (f inheritedResourcesFixer) adopt(holder, scope pdf.PDFDict, visited map[uintptr]bool) bool {
	ptr := pdf.ValuePointer(holder.Entries)
	if visited[ptr] {
		return false
	}
	visited[ptr] = true

	var streams []pdf.PDFDict
	if pdf.ContentRoots(holder, func(r pdf.ContentRoot) { streams = append(streams, r.Stream) }) == pdf.ContentNone {
		streams = []pdf.PDFDict{holder}
	}
	own, _ := holder.Entries.Get("Resources").(pdf.PDFDict)
	changed := false
	var names []resourceName
	for _, s := range streams {
		f.scan(s, func(op string, operands []pdf.PDFValue) {
			verify.ResourceNames(op, operands, func(category, name string) {
				names = append(names, resourceName{category, name})
				if pdf.LookupResource(own, category, name) != nil {
					return
				}
				v := pdf.LookupResource(scope, category, name)
				if v == nil {
					return
				}
				if own.Entries == nil {
					own = pdf.NewPDFDict()
					holder.Entries.Set("Resources", own)
				}
				sub, ok := own.Entries.Get(category).(pdf.PDFDict)
				if !ok {
					sub = pdf.NewPDFDict()
					own.Entries.Set(category, sub)
				}
				sub.Entries.Set(name, v)
				changed = true
			})
		})
	}
	if f.adoptChildren(names, own, visited) {
		changed = true
	}
	return changed
}

// scan calls fn for each operator of a content stream that decodes.
func (f inheritedResourcesFixer) scan(stream pdf.PDFDict, fn func(string, []pdf.PDFValue)) {
	decode := f.decode
	if decode == nil {
		decode = pdf.DecodeStream
	}
	if data, err := decode(stream); err == nil {
		pdf.NewContentScanner(data).Scan(fn)
	}
}

// --- ISO 19005-2 6.2.4.2: overprint mode with ICCBased CMYK ---

// overprintModeFixer sets overprint mode 0 in every ExtGState that selects
// mode 1. Mode 1 only changes how a CMYK colour with zero components
// overprints, which ISO 19005-2 does not let a file rely on.
type overprintModeFixer struct{}

func (overprintModeFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Colour.ICCBasedCMYKOverprint
}

func (overprintModeFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if d.Entries.Get("OPM") == pdf.PDFInteger(1) {
			d.Entries.Set("OPM", pdf.PDFInteger(0))
			changed = true
		}
	})
	return changed, nil
}

// --- ISO 19005-2 6.2.4.4: DeviceN colorants and Separation consistency ---

// colourSpaceArrays calls fn for each colour space array a dict or array of
// the graph holds, at any depth.
func colourSpaceArrays(trailer pdf.PDFDict, head string, fn func(pdf.PDFArray) (pdf.PDFArray, bool)) {
	walkGraph(trailer, map[uintptr]bool{}, nil, func(v pdf.PDFValue) (pdf.PDFValue, bool) {
		arr, ok := v.(pdf.PDFArray)
		if !ok || len(arr) == 0 || arr[0] != (pdf.PDFName{Value: head}) {
			return nil, false
		}
		return fn(arr)
	})
}

// deviceNColorantEntriesFixer describes each spot colorant of a DeviceN
// colour space in its Colorants dictionary, adding an attributes dictionary
// when the array has none. A colorant gets the document's Separation colour
// space of the same name when there is one, so the two stay consistent, and
// otherwise a new one whose tint transform samples the DeviceN's own with
// only that colorant inked.
type deviceNColorantEntriesFixer struct{}

func (deviceNColorantEntriesFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Colour.DeviceNColorantsMissing
}

func (deviceNColorantEntriesFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	separations := map[string]pdf.PDFArray{}
	colourSpaceArrays(*trailer, "Separation", func(arr pdf.PDFArray) (pdf.PDFArray, bool) {
		if len(arr) < 4 {
			return nil, false
		}
		if name, ok := arr[1].(pdf.PDFName); ok && separations[name.Value] == nil {
			separations[name.Value] = arr
		}
		return nil, false
	})

	changed := false
	colourSpaceArrays(*trailer, "DeviceN", func(arr pdf.PDFArray) (pdf.PDFArray, bool) {
		missing := verify.MissingColorants(arr)
		if len(missing) == 0 || len(arr) < 4 {
			return nil, false
		}
		names, _ := arr[1].(pdf.PDFArray)
		var colorants pdf.PDFDict
		for _, name := range missing {
			sep, ok := separations[name]
			if !ok {
				tint, ok := colorantTint(arr[3], names, name)
				if !ok {
					continue
				}
				sep = pdf.PDFArray{pdf.PDFName{Value: "Separation"}, pdf.PDFName{Value: name}, arr[2], tint}
				separations[name] = sep
			}
			if colorants.Entries == nil {
				colorants = deviceNColorants(&arr)
			}
			colorants.Entries.Set(name, sep)
			changed = true
		}
		return arr, true
	})
	return changed, nil
}

// deviceNColorants returns the Colorants dictionary of a DeviceN colour
// space, creating it and the attributes dictionary as needed.
func deviceNColorants(arr *pdf.PDFArray) pdf.PDFDict {
	if len(*arr) < 5 {
		*arr = append((*arr)[:4:4], pdf.NewPDFDict())
	}
	attrs, ok := (*arr)[4].(pdf.PDFDict)
	if !ok {
		attrs = pdf.NewPDFDict()
		(*arr)[4] = attrs
	}
	colorants, ok := attrs.Entries.Get("Colorants").(pdf.PDFDict)
	if !ok {
		colorants = pdf.NewPDFDict()
		attrs.Entries.Set("Colorants", colorants)
	}
	return colorants
}

// colorantSamples is how many tints a sampled colorant tint transform holds.
const colorantSamples = 256

// colorantTint returns a sampled function that maps one colorant's tint to
// what the DeviceN tint transform gives for that tint with every other
// colorant at zero.
func colorantTint(tintTransform pdf.PDFValue, names pdf.PDFArray, colorant string) (pdf.PDFDict, bool) {
	fn, err := pdf.ParseFunction(tintTransform)
	if err != nil {
		return pdf.PDFDict{}, false
	}
	index := -1
	for i, v := range names {
		if v == (pdf.PDFName{Value: colorant}) {
			index = i
		}
	}
	if index < 0 {
		return pdf.PDFDict{}, false
	}
	in := make([]float64, len(names))
	outputs := make([][]float64, colorantSamples)
	for i := range outputs {
		in[index] = float64(i) / (colorantSamples - 1)
		outputs[i] = fn.Eval(in)
		if len(outputs[i]) == 0 || len(outputs[i]) != len(outputs[0]) {
			return pdf.PDFDict{}, false
		}
	}
	n := len(outputs[0])
	lo, hi := make([]float64, n), make([]float64, n)
	for j := range n {
		lo[j], hi[j] = math.Inf(1), math.Inf(-1)
		for _, out := range outputs {
			lo[j], hi[j] = math.Min(lo[j], out[j]), math.Max(hi[j], out[j])
		}
		if hi[j] <= lo[j] {
			hi[j] = lo[j] + 1
		}
	}
	samples := make([]byte, 0, colorantSamples*n)
	decode := make(pdf.PDFArray, 0, 2*n)
	for j := range n {
		decode = append(decode, pdf.PDFReal(lo[j]), pdf.PDFReal(hi[j]))
	}
	for _, out := range outputs {
		for j, v := range out {
			samples = append(samples, byte(math.Round((v-lo[j])/(hi[j]-lo[j])*255)))
		}
	}
	d := pdf.NewPDFDict()
	d.Entries.Set("FunctionType", pdf.PDFInteger(0))
	d.Entries.Set("Domain", pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(1)})
	d.Entries.Set("Range", decode)
	d.Entries.Set("Size", pdf.PDFArray{pdf.PDFInteger(colorantSamples)})
	d.Entries.Set("BitsPerSample", pdf.PDFInteger(8))
	d.Entries.Set("Decode", decode)
	d.SetStream(samples)
	return d, true
}

// separationConsistencyFixer gives every Separation colour space the
// alternate space and tint transform of the first one of its colorant name,
// so that all of them render the colorant alike.
type separationConsistencyFixer struct{}

func (separationConsistencyFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Colour.SeparationInconsistent
}

func (separationConsistencyFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	first := map[string]pdf.PDFArray{}
	changed := false
	colourSpaceArrays(*trailer, "Separation", func(arr pdf.PDFArray) (pdf.PDFArray, bool) {
		if len(arr) < 4 {
			return nil, false
		}
		name, ok := arr[1].(pdf.PDFName)
		if !ok {
			return nil, false
		}
		f, seen := first[name.Value]
		if !seen {
			first[name.Value] = arr
			return nil, false
		}
		if !pdf.EqualPDFValue(f[2], arr[2]) || !pdf.EqualPDFValue(f[3], arr[3]) {
			arr[2], arr[3] = f[2], f[3]
			changed = true
		}
		return nil, false
	})
	return changed, nil
}

// --- ISO 19005-2 6.2.5: halftones ---

// halftoneFixer removes the halftone (HTP) entry of an ExtGState and repairs
// the halftone (HT) it holds. A halftone of a type other than 1 or 5 is
// replaced by the Default halftone, the halftone name (HalftoneName) is
// removed, and the transfer function (TransferFunction) is removed from a
// primary colorant's halftone and set to Identity on a spot colorant's. Each
// repair runs only for its reported check.
type halftoneFixer struct{}

func (halftoneFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Transparency.HalftoneExtGState, pdf.Checks.Transparency.HalftoneType,
		pdf.Checks.Transparency.HalftoneName, pdf.Checks.Transparency.HalftoneTransferFunction:
		return true
	}
	return false
}

func (halftoneFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	reported := reportedChecks(issues)
	tr := pdf.Checks.Transparency
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if t, ok := d.Entries.Get("Type").(pdf.PDFName); ok && t.Value != "ExtGState" {
			return
		}
		if _, ok := d.Entries.Lookup("HTP"); ok && reported[tr.HalftoneExtGState] {
			d.Entries.Del("HTP")
			changed = true
		}
		ht, ok := d.Entries.Get("HT").(pdf.PDFDict)
		if !ok {
			return
		}
		if reported[tr.HalftoneType] && (!verify.HalftoneTypeAllowed(ht) || !verify.HalftoneTypeAllowed(defaultHalftone(ht))) {
			d.Entries.Set("HT", pdf.PDFName{Value: "Default"})
			changed = true
			return
		}
		if repairHalftone(ht, "", reported) {
			changed = true
		}
	})
	return changed, nil
}

// defaultHalftone returns the default halftone (Default) of a type 5 halftone,
// or ht itself for any other.
func defaultHalftone(ht pdf.PDFDict) pdf.PDFDict {
	if ht.Entries.Get("HalftoneType") != pdf.PDFInteger(5) {
		return ht
	}
	d, ok := ht.Entries.Get("Default").(pdf.PDFDict)
	if !ok {
		return ht
	}
	return d
}

// repairHalftone repairs one halftone, held under colorant by a type 5
// halftone or directly by an ExtGState when colorant is "".
func repairHalftone(ht pdf.PDFDict, colorant string, reported map[pdf.Check]bool) bool {
	tr := pdf.Checks.Transparency
	changed := false
	if _, ok := ht.Entries.Lookup("HalftoneName"); ok && reported[tr.HalftoneName] {
		ht.Entries.Del("HalftoneName")
		changed = true
	}
	if colorant != "Default" && reported[tr.HalftoneTransferFunction] {
		_, hasTF := ht.Entries.Lookup("TransferFunction")
		switch primary := colorant == "" || verify.ProcessColorant(colorant); {
		case primary && hasTF:
			ht.Entries.Del("TransferFunction")
			changed = true
		case !primary && !hasTF:
			ht.Entries.Set("TransferFunction", pdf.PDFName{Value: "Identity"})
			changed = true
		}
	}
	if ht.Entries.Get("HalftoneType") != pdf.PDFInteger(5) {
		return changed
	}
	var drop []string
	for k, sub := range ht.Entries.All() {
		switch k {
		case "Type", "HalftoneType", "HalftoneName":
			continue
		}
		d, ok := sub.(pdf.PDFDict)
		if !ok {
			continue
		}
		if !verify.HalftoneTypeAllowed(d) {
			if reported[tr.HalftoneType] {
				drop = append(drop, k)
			}
			continue
		}
		if repairHalftone(d, k, reported) {
			changed = true
		}
	}
	for _, k := range drop {
		ht.Entries.Del(k)
		changed = true
	}
	return changed
}
