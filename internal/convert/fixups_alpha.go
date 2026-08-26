package convert

import (
	"strings"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"

	"github.com/voidrab/gopdfrab/internal/verify"
)

// PDF/A-1 forbids transparency, and the repair for an opacity below 1 is to
// set it to 1. That is right for 0.9 and wrong for 0: what a file draws at zero
// opacity is not there, and making it opaque paints it over whatever it was
// drawn on top of -- black rectangles across a title, a black square where a
// photograph was.
//
// So before the opacity is put back to 1, this file takes the drawing away.
// While a zero opacity is in force a fill stops filling, a stroke stops
// stroking, an image or shading is dropped, and text is switched to the
// render mode that marks nothing, keeping its characters for anyone reading
// the file rather than looking at it. Clipping is untouched throughout: it is
// set before the operator that paints, and the operator that paints nothing
// still sets it.
//
// An opacity between the two is the same problem one step down: 0.4 becomes 1
// as well, and a shaded band over a chart turns into a solid block. What the
// file draws at 0.4 over white paper is its colour blended four parts in ten,
// so that is the colour the drawing is given, and the opacity can go to 1
// without changing what is seen. The white paper is an assumption -- the same
// one a soft mask is composited against -- and where the backdrop is not white
// the colour is a little wrong, which is much less wrong than opaque.
//
// Each content stream is read on its own, starting fully opaque, and only what
// the stream itself declares invisible is taken out. A stream shared between
// two places therefore comes out the same for both.

// invisibleAlpha is where an opacity counts as nothing at all, the tolerance
// validateExtGState compares against.
const invisibleAlpha = 1e-5

// textShowOps draw the text a stream carries.
var textShowOps = map[string]bool{"Tj": true, "TJ": true, "'": true, "\"": true}

// repairOpacity takes the drawing out of everything the document draws at zero
// opacity and gives everything it draws at a partial one the colour that looks
// like, and reports whether it found any. Nothing is read unless the document
// has an extended graphics state that sets an opacity below 1, so a file
// without one pays for one dictionary walk.
func repairOpacity(trailer *pdf.PDFDict) bool {
	if !hasTransparentExtGState(*trailer) {
		return false
	}

	changed := false
	for _, p := range orderedPages(*trailer) {
		rewritePageAlpha(p.dict, p.resources, &changed)
	}

	// Every other stream that draws: form XObjects (including the appearance
	// streams of annotations), tiling patterns and Type 3 glyphs, each read
	// with the resources it names itself. Page contents were done above and
	// are not reachable here, since a content stream is none of these things.
	// walkContentStreamDicts covers the same four shapes for the rewrites that
	// do not need to know which resource a name selects.
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) (pdf.PDFDict, bool) {
		switch {
		case d.HasStream && (d.Entries.Get("Subtype") == pdf.PDFName{Value: "Form"}),
			d.HasStream && d.Entries.Get("PatternType") == pdf.PDFInteger(1):
			fixed, ok := rewriteAlphaStream(d, resourcesOf(d, pdf.PDFDict{}), newAlphaState())
			if ok {
				changed = true
			}
			return fixed, ok

		case (d.Entries.Get("Subtype") == pdf.PDFName{Value: "Type3"}):
			procs, ok := d.Entries.Get("CharProcs").(pdf.PDFDict)
			if !ok {
				return d, false
			}
			resources := resourcesOf(d, pdf.PDFDict{})
			for _, name := range sortedKeys(procs.Entries) {
				proc, ok := procs.Entries.Get(name).(pdf.PDFDict)
				if !ok || !proc.HasStream {
					continue
				}
				if fixed, ok := rewriteAlphaStream(proc, resources, newAlphaState()); ok {
					procs.Entries.Set(name, fixed)
					changed = true
				}
			}
		}
		return d, false
	})
	return changed
}

// hasTransparentExtGState reports whether any extended graphics state in the
// document sets a stroking or non-stroking opacity below 1, testing the same
// dictionaries validateExtGState does.
func hasTransparentExtGState(trailer pdf.PDFDict) bool {
	found := false
	walkDicts(trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if t, ok := d.Entries.Get("Type").(pdf.PDFName); ok && t.Value != "ExtGState" {
			return
		}
		if isTransparent(d.Entries.Get("CA")) || isTransparent(d.Entries.Get("ca")) {
			found = true
		}
	})
	return found
}

// isTransparent reports whether v is an opacity of anything less than 1.
func isTransparent(v pdf.PDFValue) bool {
	f, num := verify.AsFloat(v)
	return num && f < 1-invisibleAlpha
}

// rewritePageAlpha rewrites a page's content. A page's content can be written
// as several streams, which are one stream split up: an opacity set in one
// part is still in force in the next, so the state carries across them.
func rewritePageAlpha(page, resources pdf.PDFDict, changed *bool) {
	state := newAlphaState()
	switch contents := page.Entries.Get("Contents").(type) {
	case pdf.PDFDict:
		if !contents.HasStream {
			return
		}
		if fixed, ok := rewriteAlphaStream(contents, resources, state); ok {
			page.Entries.Set("Contents", fixed)
			*changed = true
		}

	case pdf.PDFArray:
		for i, item := range contents {
			part, ok := item.(pdf.PDFDict)
			if !ok || !part.HasStream {
				continue
			}
			if fixed, ok := rewriteAlphaStream(part, resources, state); ok {
				contents[i] = fixed
				*changed = true
			}
		}
	}
}

// rewriteAlphaStream rewrites one content stream, carrying on from the state it
// is handed and leaving that state where the stream ends.
func rewriteAlphaStream(dict, resources pdf.PDFDict, state *alphaState) (pdf.PDFDict, bool) {
	data, err := pdf.DecodeStream(dict)
	if err != nil {
		return dict, false
	}

	var cw writer.ContentStreamWriter
	r := &alphaRewriter{w: &cw, resources: resources, state: *state}
	cs := pdf.NewContentScanner(data)
	cs.Scan(r.op)
	*state = r.state
	// A stream only part of which could be read: see rewriteContentStreamDict.
	if !r.changed || !cs.Complete() || cw.Err() != nil {
		return dict, false
	}
	if err := writer.SetStreamFlate(&dict, cw.Bytes()); err != nil {
		return dict, false
	}
	return dict, true
}

// alphaState is the part of the graphics state that decides what an operator
// marks the page with. A stream starts fully opaque and filling text.
type alphaState struct {
	fillAlpha    float64
	strokeAlpha  float64
	textMode     int
	fill, stroke colourState
}

// newAlphaState is where a content stream starts: fully opaque.
func newAlphaState() *alphaState {
	return &alphaState{fillAlpha: 1, strokeAlpha: 1}
}

func (s alphaState) fillHidden() bool   { return s.fillAlpha <= invisibleAlpha }
func (s alphaState) strokeHidden() bool { return s.strokeAlpha <= invisibleAlpha }

// faded reports an opacity that hides nothing but is not opaque either.
func faded(alpha float64) bool { return alpha > invisibleAlpha && alpha < 1-invisibleAlpha }

// colourState is the colour a stream paints in: the operator that set it, the
// numbers it set, and the space those numbers are in. It stays empty while the
// colour is one this cannot work out, which is how those are left alone.
type colourState struct {
	op    string
	ops   []pdf.PDFValue
	space pdf.PDFValue
}

// alphaRewriter walks one content stream, writing it back with the drawing
// removed wherever the opacity in force says there is nothing to see.
type alphaRewriter struct {
	w         *writer.ContentStreamWriter
	resources pdf.PDFDict
	state     alphaState
	stack     []alphaState
	changed   bool
}

func (r *alphaRewriter) op(op string, operands []pdf.PDFValue) {
	r.track(op, operands)

	switch {
	case paintOps[op]:
		visible := r.visiblePaint(op)
		if visible != op {
			r.changed = true
		}
		r.emitPainted(visible, nil, paintFills(visible), paintStrokes(visible))

	case op == "sh", op == "Do", op == "INLINEIMAGE":
		if r.marksNothing(op, operands) {
			r.changed = true
			return
		}
		r.emit(op, operands)

	case textShowOps[op]:
		mode, hidden := r.hiddenTextMode()
		if !hidden {
			fills, strokes := textPaints(r.state.textMode)
			r.emitPainted(op, operands, fills, strokes)
			return
		}
		// Around the operator rather than once for the stream: the text keeps
		// its own render mode everywhere else, and the stream may set it again.
		r.emitTextMode(mode)
		r.emit(op, operands)
		r.emitTextMode(r.state.textMode)
		r.changed = true

	default:
		r.emit(op, operands)
	}
}

// track follows the operators that decide what is visible: the saved and
// restored state, the opacity an extended graphics state sets, and the text
// render mode.
func (r *alphaRewriter) track(op string, operands []pdf.PDFValue) {
	switch op {
	case "q":
		r.stack = append(r.stack, r.state)
	case "Q":
		if n := len(r.stack); n > 0 {
			r.state = r.stack[n-1]
			r.stack = r.stack[:n-1]
		}
	case "gs":
		r.applyExtGState(operands)
	case "Tr":
		if len(operands) == 1 {
			if mode, ok := numericValue(operands[0]); ok {
				r.state.textMode = int(mode)
			}
		}
	case "cs":
		r.state.fill = colourState{space: r.operandSpace(operands)}
	case "CS":
		r.state.stroke = colourState{space: r.operandSpace(operands)}
	case "g", "rg", "k", "sc", "scn":
		r.state.fill = r.setColour(op, operands, r.state.fill.space)
	case "G", "RG", "K", "SC", "SCN":
		r.state.stroke = r.setColour(op, operands, r.state.stroke.space)
	}
}

// deviceSpaces are the spaces the colour operators name by themselves.
var deviceSpaces = map[string]string{
	"g": "DeviceGray", "rg": "DeviceRGB", "k": "DeviceCMYK",
}

// setColour records what a colour operator set. An operand that is not a
// number -- the name of a pattern -- leaves the colour unrecorded, and so
// untouched.
func (r *alphaRewriter) setColour(op string, operands []pdf.PDFValue, space pdf.PDFValue) colourState {
	// The stroking operators are the same letters in capitals, and the colour
	// has to go back out as the operator it came in as -- writing a stroke's
	// colour with a fill's operator would paint the wrong half of the page.
	if name, ok := deviceSpaces[strings.ToLower(op)]; ok {
		space = pdf.PDFName{Value: name}
	}
	// Copied, not kept: the scanner hands out the same operand slice every
	// operator.
	ops := make([]pdf.PDFValue, 0, len(operands))
	for _, v := range operands {
		if _, num := verify.AsFloat(v); !num {
			return colourState{space: space}
		}
		ops = append(ops, v)
	}
	if len(ops) == 0 {
		return colourState{space: space}
	}
	return colourState{op: op, ops: ops, space: space}
}

// operandSpace resolves the colour space a cs or CS operator names.
func (r *alphaRewriter) operandSpace(operands []pdf.PDFValue) pdf.PDFValue {
	if len(operands) == 0 {
		return nil
	}
	name, ok := operands[len(operands)-1].(pdf.PDFName)
	if !ok {
		return nil
	}
	if space, ok := pdf.LookupNamedColorSpace(name.Value, r.resources); ok {
		return space
	}
	return name
}

// emitPainted writes an operator that marks the page, in the colour it looks
// like at the opacity in force -- and puts the colour back afterwards, since
// the rest of the stream still paints in the one the file set.
func (r *alphaRewriter) emitPainted(op string, operands []pdf.PDFValue, fills, strokes bool) {
	fill := fills && faded(r.state.fillAlpha)
	stroke := strokes && faded(r.state.strokeAlpha)
	if fill {
		fill = r.emitColour(r.state.fill, r.state.fillAlpha)
	}
	if stroke {
		stroke = r.emitColour(r.state.stroke, r.state.strokeAlpha)
	}
	r.emit(op, operands)
	if fill {
		r.emit(r.state.fill.op, r.state.fill.ops)
	}
	if stroke {
		r.emit(r.state.stroke.op, r.state.stroke.ops)
	}
	if fill || stroke {
		r.changed = true
	}
}

// emitColour writes c as it looks at opacity alpha over white paper, and
// reports whether it could.
func (r *alphaRewriter) emitColour(c colourState, alpha float64) bool {
	white, ok := whiteComponent(c.space)
	if !ok || c.op == "" {
		return false
	}
	ops := make([]pdf.PDFValue, len(c.ops))
	same := true
	for i, v := range c.ops {
		f, _ := verify.AsFloat(v)
		blended := alpha*f + (1-alpha)*white
		if blended != f {
			same = false
		}
		ops[i] = pdf.PDFReal(blended)
	}
	if same {
		return false // the colour already looks like itself at this opacity
	}
	r.emit(c.op, ops)
	return true
}

// whiteComponent is what every component of a colour space's white is: 1 where
// a larger number is lighter, 0 where it is more ink. A space this cannot say
// for -- indexed, Lab, a pattern -- is left alone.
func whiteComponent(space pdf.PDFValue) (float64, bool) {
	var family string
	switch v := space.(type) {
	case pdf.PDFName:
		family = v.Value
	case pdf.PDFArray:
		if len(v) == 0 {
			return 0, false
		}
		head, _ := v[0].(pdf.PDFName)
		family = head.Value
	default:
		return 0, false
	}
	switch family {
	case "DeviceGray", "G", "CalGray", "DeviceRGB", "RGB", "CalRGB":
		return 1, true
	case "DeviceCMYK", "CMYK", "Separation", "DeviceN":
		return 0, true
	case "ICCBased":
		switch pdf.ColorSpaceComponents(space) {
		case 1, 3:
			return 1, true
		case 4:
			return 0, true
		}
	}
	return 0, false
}

// paintFills and paintStrokes report which halves of the page a painting
// operator marks.
func paintFills(op string) bool {
	switch op {
	case "f", "F", "f*", "B", "B*", "b", "b*":
		return true
	}
	return false
}

func paintStrokes(op string) bool {
	switch op {
	case "S", "s", "B", "B*", "b", "b*":
		return true
	}
	return false
}

// textPaints reports the same for a text render mode.
func textPaints(mode int) (fills, strokes bool) {
	switch mode {
	case 0, 4:
		return true, false
	case 1, 5:
		return false, true
	case 2, 6:
		return true, true
	}
	return false, false
}

// applyExtGState takes the opacities out of the named extended graphics state.
// A state that names only one of the two leaves the other as it was.
func (r *alphaRewriter) applyExtGState(operands []pdf.PDFValue) {
	egs, ok := r.namedResource("ExtGState", operands)
	if !ok {
		return
	}
	if f, num := verify.AsFloat(egs.Entries.Get("ca")); num {
		r.state.fillAlpha = f
	}
	if f, num := verify.AsFloat(egs.Entries.Get("CA")); num {
		r.state.strokeAlpha = f
	}
}

// namedResource looks up the resource an operator's name operand selects.
func (r *alphaRewriter) namedResource(category string, operands []pdf.PDFValue) (pdf.PDFDict, bool) {
	if len(operands) == 0 {
		return pdf.PDFDict{}, false
	}
	name, ok := operands[len(operands)-1].(pdf.PDFName)
	if !ok {
		return pdf.PDFDict{}, false
	}
	entries, ok := r.resources.Entries.Get(category).(pdf.PDFDict)
	if !ok {
		return pdf.PDFDict{}, false
	}
	d, ok := entries.Entries.Get(name.Value).(pdf.PDFDict)
	return d, ok
}

// visiblePaint returns the operator that paints only the half of op still
// visible: n when neither half is, and op itself when both are. A path that is
// no longer painted is still built and still clipped with.
func (r *alphaRewriter) visiblePaint(op string) string {
	fill, stroke := r.state.fillHidden(), r.state.strokeHidden()
	switch op {
	case "f", "F", "f*":
		if fill {
			return "n"
		}
	case "S", "s":
		if stroke {
			return "n"
		}
	case "B", "B*", "b", "b*":
		closes := op == "b" || op == "b*"
		evenOdd := op == "B*" || op == "b*"
		switch {
		case fill && stroke:
			return "n"
		case fill:
			if closes {
				return "s"
			}
			return "S"
		case stroke:
			if evenOdd {
				return "f*"
			}
			return "f"
		}
	}
	return op
}

// marksNothing reports whether an image, a shading or an XObject would draw
// nothing at the opacity in force. A shading and an image are painted with the
// non-stroking opacity; a form can do either, so it only goes when both are
// out -- and an unresolvable name is treated as a form.
func (r *alphaRewriter) marksNothing(op string, operands []pdf.PDFValue) bool {
	if op == "sh" || op == "INLINEIMAGE" {
		return r.state.fillHidden()
	}
	if xobj, ok := r.namedResource("XObject", operands); ok {
		if (xobj.Entries.Get("Subtype") == pdf.PDFName{Value: "Image"}) {
			return r.state.fillHidden()
		}
	}
	return r.state.fillHidden() && r.state.strokeHidden()
}

// hiddenTextMode returns the render mode that draws nothing while keeping what
// the current one does besides drawing, and whether the current one draws
// anything at all. Modes 3 and 7 already mark nothing; the clipping modes go to
// 7 rather than 3, so text that clips still clips.
func (r *alphaRewriter) hiddenTextMode() (int, bool) {
	fill, stroke := r.state.fillHidden(), r.state.strokeHidden()
	switch r.state.textMode {
	case 0:
		return 3, fill
	case 1:
		return 3, stroke
	case 2:
		return 3, fill && stroke
	case 4:
		return 7, fill
	case 5:
		return 7, stroke
	case 6:
		return 7, fill && stroke
	}
	return 0, false
}

func (r *alphaRewriter) emitTextMode(mode int) {
	r.emit("Tr", []pdf.PDFValue{pdf.PDFInteger(mode)})
}

func (r *alphaRewriter) emit(op string, operands []pdf.PDFValue) {
	_ = r.w.WriteOp(op, operands) // the writer keeps the first error
}
