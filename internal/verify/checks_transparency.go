package verify

import (
	"fmt"

	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// 6.2.10 Transparency (ISO 19005-2)

// maxTransparencyDepth bounds how deeply the transparency analysis follows
// forms, patterns and Type 3 glyphs into one another.
const maxTransparencyDepth = 16

// validatePageTransparency reports a page that contains transparency but has
// no blending colour space, in a file with no PDF/A output intent.
func validatePageTransparency(v pdf.PDFDict, ctx *ValidationContext) {
	if !ctx.part2() || ctx.hasOutputIntent || (v.Entries.Get("Type") != pdf.PDFName{Value: "Page"}) {
		return
	}
	if group, ok := v.Entries.Get("Group").(pdf.PDFDict); ok && group.Entries.Get("CS") != nil {
		return
	}
	if ctx.transparency == nil {
		ctx.transparency = newTransparencyScan(ctx.reader)
	}
	if !ctx.transparency.page(v) {
		return
	}
	ctx.Report(pdf.Checks.Transparency.TransparencyWithoutBlendingSpace, v,
		"page contains transparency but its Group has no CS entry, and the file has no PDF/A output intent")
}

// validateBlendingColourSpace reports the blending colour space of a page's
// transparency group, or of an isolated transparency group a form XObject
// draws, that is a device colour space no output intent covers.
func validateBlendingColourSpace(v pdf.PDFDict, ctx *ValidationContext) {
	if !ctx.part2() {
		return
	}
	cs, resources, ok := BlendingColourSpace(v)
	if !ok || (v.Entries.Get("Subtype") == pdf.PDFName{Value: "Form"}) && !ctx.isReachableXObject(v) {
		return
	}
	model := DeviceColourModel(cs)
	if model == "" || ctx.deviceColourAllowed(model) || DefaultColorSpaceDefined(model, resources) {
		return
	}
	ctx.reportUncovered(pdf.Checks.Transparency.DeviceBlendingColourSpace, v,
		fmt.Sprintf("transparency group uses device colour (%s) as its blending colour space without matching OutputIntent", model))
}

// BlendingColourSpace returns the blending colour space of v's transparency
// group, with the resources a Default colour space for it would come from,
// when v is a page or a form XObject whose group sets one. A form's group
// counts only when it is isolated, since a non-isolated group blends in its
// parent's space.
func BlendingColourSpace(v pdf.PDFDict) (cs pdf.PDFValue, resources pdf.PDFDict, ok bool) {
	group, isDict := v.Entries.Get("Group").(pdf.PDFDict)
	if !isDict || (group.Entries.Get("S") != pdf.PDFName{Value: "Transparency"}) {
		return nil, pdf.PDFDict{}, false
	}
	cs = group.Entries.Get("CS")
	if cs == nil {
		return nil, pdf.PDFDict{}, false
	}
	switch {
	case v.Entries.Get("Type") == pdf.PDFName{Value: "Page"}:
		return cs, pdf.PageResources(v), true
	case v.Entries.Get("Subtype") == pdf.PDFName{Value: "Form"}:
		if isolated, _ := group.Entries.Get("I").(pdf.PDFBoolean); !bool(isolated) {
			return nil, pdf.PDFDict{}, false
		}
		resources, _ = v.Entries.Get("Resources").(pdf.PDFDict)
		return cs, resources, true
	}
	return nil, pdf.PDFDict{}, false
}

// page reports whether painting the page, or one of its annotations'
// appearances, involves transparency: a painting operator under a soft mask,
// a blend mode other than Normal or a constant alpha below 1 for the colour
// it paints, an image with a soft mask, a transparency group, or a tiling
// pattern or Type 3 glyph whose own content involves it.
func (t *transparencyScan) page(page pdf.PDFDict) bool {
	found := false
	pdf.ContentRoots(page, func(r pdf.ContentRoot) {
		found = found || t.stream(r.Stream, r.Resources, opaque, 0)
	})
	if found {
		return true
	}
	resources := pdf.PageResources(page)
	annots, _ := page.Entries.Get("Annots").(pdf.PDFArray)
	for _, item := range annots {
		annot, _ := item.(pdf.PDFDict)
		ap, _ := annot.Entries.Get("AP").(pdf.PDFDict)
		for _, key := range []string{"N", "R", "D"} {
			for _, appearance := range pdf.AppearanceStreams(ap.Entries.Get(key)) {
				if t.form(appearance, resources, opaque, 0) {
					return true
				}
			}
		}
	}
	return false
}

// alphaState is the part of the graphics state that decides whether painting
// involves transparency.
type alphaState struct {
	// softMask and blendMode are true under a soft mask and under a blend
	// mode other than Normal.
	softMask, blendMode bool
	// fill and stroke are the nonstroking and stroking constant alpha.
	fill, stroke float64
	// fillPattern and strokePattern are the tiling patterns selected as the
	// nonstroking and stroking colour, if any.
	fillPattern, strokePattern pdf.PDFDict
	// font is the Type 3 font selected, if any, and textMode the text
	// rendering mode.
	font     pdf.PDFDict
	textMode int
}

// opaque is the initial graphics state.
var opaque = alphaState{fill: 1, stroke: 1}

// transparencyScan follows a document's page content into the forms,
// patterns and glyphs it paints.
type transparencyScan struct {
	reader *pdf.Reader
	// visiting holds the streams being scanned, so a stream that draws
	// itself ends the descent.
	visiting map[uintptr]bool
	// scanned holds the outcome of each stream already scanned from a given
	// state with given resources, so content drawn many times is scanned
	// once.
	scanned map[scanKey]bool
	// cutShort is set when a scan has stopped at maxTransparencyDepth. The
	// outcome of such a scan depends on the depth it started at, so it is not
	// remembered in scanned.
	cutShort bool
	// encodings holds the Differences of each Type 3 font met.
	encodings map[uintptr]map[byte]string
}

// newTransparencyScan returns a scan that decodes content through d's cache
// when d is not nil.
func newTransparencyScan(d *pdf.Reader) *transparencyScan {
	return &transparencyScan{reader: d, visiting: map[uintptr]bool{}, scanned: map[scanKey]bool{}, encodings: map[uintptr]map[byte]string{}}
}

// scanKey identifies one scan of a stream: the stream, the resources its
// names resolve in and the state it starts in.
type scanKey struct {
	stream, resources uintptr
	state             alphaState
}

// paintCall is a form, pattern or glyph a stream paints, to be scanned once
// the stream itself has been.
type paintCall struct {
	stream    pdf.PDFDict
	resources pdf.PDFDict
	state     alphaState
}

// stream reports whether painting one content stream, started in state with
// resources, involves transparency.
func (t *transparencyScan) stream(s, resources pdf.PDFDict, state alphaState, depth int) bool {
	key := pdf.ValuePointer(s.Entries)
	if depth > maxTransparencyDepth {
		t.cutShort = true
		return false
	}
	if t.visiting[key] {
		return false
	}
	memo := scanKey{key, pdf.ValuePointer(resources.Entries), state}
	if found, ok := t.scanned[memo]; ok {
		return found
	}
	outer := t.cutShort
	t.cutShort = false
	t.visiting[key] = true
	found := t.walkContent(s, resources, state, depth)
	delete(t.visiting, key)
	if !t.cutShort {
		t.scanned[memo] = found
	}
	t.cutShort = t.cutShort || outer
	return found
}

// walkContent scans the content of one stream. stream calls it, and handles
// cycles and repeated scans.
func (t *transparencyScan) walkContent(s, resources pdf.PDFDict, state alphaState, depth int) bool {
	found := false
	var calls []paintCall
	var saved []alphaState
	err := scanContent(t.reader, s, func(op string, operands []pdf.PDFValue) {
		if found {
			return
		}
		switch op {
		case "q":
			saved = append(saved, state)
		case "Q":
			if n := len(saved); n > 0 {
				state, saved = saved[n-1], saved[:n-1]
			}
		case "gs":
			if gs, ok := lastResource(operands, resources, "ExtGState"); ok {
				state.applyExtGState(gs)
			}
		case "cs", "g", "rg", "k":
			state.fillPattern = pdf.PDFDict{}
		case "CS", "G", "RG", "K":
			state.strokePattern = pdf.PDFDict{}
		case "scn":
			state.fillPattern = tilingPattern(operands, resources)
		case "SCN":
			state.strokePattern = tilingPattern(operands, resources)
		case "Tf":
			state.font = pdf.PDFDict{}
			if len(operands) > 0 {
				if font, ok := pdf.LookupResource(resources, "Font", nameValue(operands[0])).(pdf.PDFDict); ok &&
					(font.Entries.Get("Subtype") == pdf.PDFName{Value: "Type3"}) {
					state.font = font
				}
			}
		case "Tr":
			if len(operands) > 0 {
				if mode, ok := pdf.PDFNumberToInt(operands[0]); ok {
					state.textMode = mode
				}
			}
		case "S", "s":
			found = state.paints(false, true, &calls, resources)
		case "f", "F", "f*":
			found = state.paints(true, false, &calls, resources)
		case "B", "B*", "b", "b*":
			found = state.paints(true, true, &calls, resources)
		case "sh", "INLINEIMAGE":
			found = state.fillTransparent()
		case "Do":
			if found = state.fillTransparent(); found {
				return
			}
			xobject, ok := lastResource(operands, resources, "XObject")
			if !ok {
				return
			}
			switch xobject.Entries.Get("Subtype") {
			case pdf.PDFName{Value: "Image"}:
				found = imageHasSoftMask(xobject)
			case pdf.PDFName{Value: "Form"}:
				if found = transparencyGroup(xobject); !found {
					calls = append(calls, paintCall{stream: xobject, resources: resourcesOr(xobject, resources), state: state})
				}
			}
		case "Tj", "TJ", "'", "\"":
			fill, stroke := pdf.TextRenderPaints(state.textMode)
			if !fill && !stroke {
				return
			}
			if found = state.paints(fill, stroke, &calls, resources); found {
				return
			}
			for _, glyph := range t.type3Glyphs(state.font, op, operands) {
				calls = append(calls, paintCall{stream: glyph, resources: resourcesOr(state.font, resources), state: state})
			}
		}
	})
	if err != nil || found {
		return found
	}
	for _, c := range calls {
		if t.stream(c.stream, c.resources, c.state, depth+1) {
			return true
		}
	}
	return false
}

// form reports whether painting a form XObject, started in state from a
// stream with resources, involves transparency.
func (t *transparencyScan) form(f, resources pdf.PDFDict, state alphaState, depth int) bool {
	return transparencyGroup(f) || t.stream(f, resourcesOr(f, resources), state, depth)
}

// transparencyGroup reports whether a form XObject is a transparency group.
func transparencyGroup(f pdf.PDFDict) bool {
	group, ok := f.Entries.Get("Group").(pdf.PDFDict)
	return ok && (group.Entries.Get("S") == pdf.PDFName{Value: "Transparency"})
}

// blends reports whether painting in this state blends, whatever the alpha.
func (s alphaState) blends() bool { return s.softMask || s.blendMode }

// fillTransparent reports whether painting with the nonstroking colour, or an
// image or shading, involves transparency in this state.
func (s alphaState) fillTransparent() bool { return s.blends() || s.fill < 1 }

// paints reports whether painting with the nonstroking colour (fill) or the
// stroking colour (stroke) involves transparency in this state. The tiling
// patterns it paints with are added to calls, to be scanned in turn.
func (s alphaState) paints(fill, stroke bool, calls *[]paintCall, resources pdf.PDFDict) bool {
	if s.blends() || fill && s.fill < 1 || stroke && s.stroke < 1 {
		return true
	}
	if fill && s.fillPattern.Entries != nil {
		*calls = append(*calls, paintCall{stream: s.fillPattern, resources: resourcesOr(s.fillPattern, resources), state: opaque})
	}
	if stroke && s.strokePattern.Entries != nil {
		*calls = append(*calls, paintCall{stream: s.strokePattern, resources: resourcesOr(s.strokePattern, resources), state: opaque})
	}
	return false
}

// applyExtGState sets the soft mask, blend mode and constant alpha that an
// ExtGState holds. A blend mode counts as blending unless it is Normal, or
// an array holding Normal alone.
func (s *alphaState) applyExtGState(gs pdf.PDFDict) {
	if smask, ok := gs.Entries.Lookup("SMask"); ok {
		_, s.softMask = smask.(pdf.PDFDict)
	}
	if bm, ok := gs.Entries.Lookup("BM"); ok {
		s.blendMode = !normalBlendMode(bm)
	}
	if ca, ok := pdf.PDFNumberToFloat(gs.Entries.Get("ca")); ok {
		s.fill = ca
	}
	if ca, ok := pdf.PDFNumberToFloat(gs.Entries.Get("CA")); ok {
		s.stroke = ca
	}
}

// normalBlendMode reports whether a /BM value selects the Normal blend mode.
func normalBlendMode(bm pdf.PDFValue) bool {
	switch v := bm.(type) {
	case nil:
		return true
	case pdf.PDFName:
		return v.Value == "Normal"
	case pdf.PDFArray:
		return len(v) == 1 && v[0] == pdf.PDFName{Value: "Normal"}
	}
	return false
}

// imageHasSoftMask reports whether an image XObject carries a soft mask, as a
// stream or in its JPEG 2000 data.
func imageHasSoftMask(image pdf.PDFDict) bool {
	if smask, ok := image.Entries.Get("SMask").(pdf.PDFDict); ok && smask.IsStream() {
		return true
	}
	n, _ := pdf.PDFNumberToInt(image.Entries.Get("SMaskInData"))
	return n > 0
}

// tilingPattern returns the tiling pattern an scn or SCN operator selects,
// or an empty dictionary for any other colour.
func tilingPattern(operands []pdf.PDFValue, resources pdf.PDFDict) pdf.PDFDict {
	pattern, ok := lastResource(operands, resources, "Pattern")
	if !ok || pattern.Entries.Get("PatternType") != pdf.PDFInteger(1) {
		return pdf.PDFDict{}
	}
	return pattern
}

// type3Glyphs returns the glyph procedures of a Type 3 font that a text
// showing operator's strings select, through the font's Encoding
// Differences. A glyph shown twice is listed twice; its second scan is
// remembered.
func (t *transparencyScan) type3Glyphs(font pdf.PDFDict, op string, operands []pdf.PDFValue) []pdf.PDFDict {
	if font.Entries == nil {
		return nil
	}
	procs, _ := font.Entries.Get("CharProcs").(pdf.PDFDict)
	key := pdf.ValuePointer(font.Entries)
	names, ok := t.encodings[key]
	if !ok {
		names = map[byte]string{}
		enc, _ := font.Entries.Get("Encoding").(pdf.PDFDict)
		fontprog.EachDifference(enc, func(code int, name string) {
			if code >= 0 && code < 256 {
				names[byte(code)] = name
			}
		})
		t.encodings[key] = names
	}
	var out []pdf.PDFDict
	for _, code := range pdf.ShownStringBytes(op, operands) {
		if glyph, ok := procs.Entries.Get(names[code]).(pdf.PDFDict); ok && glyph.IsStream() {
			out = append(out, glyph)
		}
	}
	return out
}

// lastResource returns the dictionary that an operator's last operand names
// in a resource category.
func lastResource(operands []pdf.PDFValue, resources pdf.PDFDict, category string) (pdf.PDFDict, bool) {
	if len(operands) == 0 {
		return pdf.PDFDict{}, false
	}
	d, ok := pdf.LookupResource(resources, category, nameValue(operands[len(operands)-1])).(pdf.PDFDict)
	return d, ok
}

// nameValue returns a name operand's value, or "" for another operand.
func nameValue(v pdf.PDFValue) string {
	name, _ := v.(pdf.PDFName)
	return name.Value
}

// resourcesOr returns the Resources dictionary of d, or parent when d has
// none.
func resourcesOr(d, parent pdf.PDFDict) pdf.PDFDict {
	if own, ok := d.Entries.Get("Resources").(pdf.PDFDict); ok {
		return own
	}
	return parent
}
