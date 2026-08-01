package convert

import (
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
// Each content stream is read on its own, starting fully opaque, and only what
// the stream itself declares invisible is taken out. A stream shared between
// two places therefore comes out the same for both.

// invisibleAlpha is where an opacity counts as nothing at all, the tolerance
// validateExtGState compares against.
const invisibleAlpha = 1e-5

// textShowOps draw the text a stream carries.
var textShowOps = map[string]bool{"Tj": true, "TJ": true, "'": true, "\"": true}

// dropInvisibleDrawing takes the drawing out of everything the document draws
// at zero opacity, and reports whether it found any. Nothing is read unless
// the document has an extended graphics state that sets an opacity to zero,
// so a file without one pays for one dictionary walk.
func dropInvisibleDrawing(trailer *pdf.PDFDict) bool {
	if !hasZeroAlphaExtGState(*trailer) {
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
	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) (pdf.PDFDict, bool) {
		switch {
		case d.HasStream && (d.Entries.Get("Subtype") == pdf.PDFName{Value: "Form"}),
			d.HasStream && d.Entries.Get("PatternType") == pdf.PDFInteger(1):
			fixed, ok := rewriteAlphaStream(d, resourcesOf(d, pdf.PDFDict{}), &alphaState{})
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
				if fixed, ok := rewriteAlphaStream(proc, resources, &alphaState{}); ok {
					procs.Entries.Set(name, fixed)
					changed = true
				}
			}
		}
		return d, false
	})
	return changed
}

// hasZeroAlphaExtGState reports whether any extended graphics state in the
// document sets a stroking or non-stroking opacity to zero, testing the same
// dictionaries validateExtGState does.
func hasZeroAlphaExtGState(trailer pdf.PDFDict) bool {
	found := false
	walkDicts(trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if t, ok := d.Entries.Get("Type").(pdf.PDFName); ok && t.Value != "ExtGState" {
			return
		}
		if isInvisibleAlpha(d.Entries.Get("CA")) || isInvisibleAlpha(d.Entries.Get("ca")) {
			found = true
		}
	})
	return found
}

// isInvisibleAlpha reports whether v is an opacity of zero.
func isInvisibleAlpha(v pdf.PDFValue) bool {
	f, num := verify.AsFloat(v)
	return num && f <= invisibleAlpha
}

// rewritePageAlpha rewrites a page's content. A page's content can be written
// as several streams, which are one stream split up: an opacity set in one
// part is still in force in the next, so the state carries across them.
func rewritePageAlpha(page, resources pdf.PDFDict, changed *bool) {
	state := &alphaState{}
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

// alphaState is the part of the graphics state that decides whether an
// operator marks the page. A stream starts fully opaque and filling text.
type alphaState struct {
	fillHidden   bool
	strokeHidden bool
	textMode     int
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
		r.emit(visible, nil)

	case op == "sh", op == "Do", op == "INLINEIMAGE":
		if r.marksNothing(op, operands) {
			r.changed = true
			return
		}
		r.emit(op, operands)

	case textShowOps[op]:
		mode, hidden := r.hiddenTextMode()
		if !hidden {
			r.emit(op, operands)
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
	}
}

// applyExtGState takes the opacities out of the named extended graphics state.
// A state that names only one of the two leaves the other as it was.
func (r *alphaRewriter) applyExtGState(operands []pdf.PDFValue) {
	egs, ok := r.namedResource("ExtGState", operands)
	if !ok {
		return
	}
	if f, num := verify.AsFloat(egs.Entries.Get("ca")); num {
		r.state.fillHidden = f <= invisibleAlpha
	}
	if f, num := verify.AsFloat(egs.Entries.Get("CA")); num {
		r.state.strokeHidden = f <= invisibleAlpha
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
	fill, stroke := r.state.fillHidden, r.state.strokeHidden
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
		return r.state.fillHidden
	}
	if xobj, ok := r.namedResource("XObject", operands); ok {
		if (xobj.Entries.Get("Subtype") == pdf.PDFName{Value: "Image"}) {
			return r.state.fillHidden
		}
	}
	return r.state.fillHidden && r.state.strokeHidden
}

// hiddenTextMode returns the render mode that draws nothing while keeping what
// the current one does besides drawing, and whether the current one draws
// anything at all. Modes 3 and 7 already mark nothing; the clipping modes go to
// 7 rather than 3, so text that clips still clips.
func (r *alphaRewriter) hiddenTextMode() (int, bool) {
	fill, stroke := r.state.fillHidden, r.state.strokeHidden
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
