package convert

import (
	"math"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// PDF/A-1 caps a real at 32767, and the only repair for a violation used to be
// to clamp the number. That is right for a dictionary value and wrong for a
// coordinate: what the limit constrains is the number, but what the file means
// is a position, and truncating one moves the other. A page drawn at 1/500
// scale routinely puts a full-page rectangle at 365760 units, so clamping it
// shrinks that rectangle to a corner -- and when the rectangle is a clip, the
// whole page goes with it.
//
// This file repairs the position instead. The magnitude that does not fit in
// the operand is folded into the CTM, so the drawing keeps its size and place
// and every number written stays inside the limit. Only paths are rescaled;
// anything else out of range is still clamped by fixups_content.go, which runs
// after this.

const (
	// realLimit is the largest real PDF/A-1 allows (6.1.12).
	realLimit = 32767
	// maxScale bounds the factor folded into the CTM, since the factor is
	// itself a real that has to stay inside the limit. Together with realLimit
	// it puts the largest repairable coordinate around 5.4e8; beyond that the
	// clamp takes over.
	maxScale = 16384
	// defaultLineWidth is the width a stream starts with, before any w operator
	// (ISO 32000-1 table 52). Restoring 0 instead would turn every unset stroke
	// into the thinnest line the device can draw.
	defaultLineWidth = 1
	// maxBufferedPathOps bounds how much of a path is held in memory before
	// being written out. A path longer than this is passed through untouched
	// rather than allowed to grow the heap without limit.
	maxBufferedPathOps = 1 << 16
)

// pathBuildOps build a path. W and W* belong to the same path object: a clip is
// set between the last segment and the operator that paints it.
var pathBuildOps = map[string]bool{
	"m": true, "l": true, "c": true, "v": true, "y": true, "re": true,
	"h": true, "W": true, "W*": true,
}

// paintOps end a path object; strokingPaintOps are the ones that draw its
// outline, whose width follows the CTM and so needs putting back.
var (
	paintOps = map[string]bool{
		"S": true, "s": true, "f": true, "F": true, "f*": true,
		"B": true, "B*": true, "b": true, "b*": true, "n": true,
	}
	strokingPaintOps = map[string]bool{
		"S": true, "s": true, "B": true, "B*": true, "b": true, "b*": true,
	}
)

// rescaleContentStreamDict is rewriteContentStreamDict with the path rescale
// in front of it: an out-of-range path is moved into the CTM, and everything
// rewrite still finds out of range afterwards is clamped as before. The two
// run in one pass so a stream is decoded, scanned and re-encoded once.
func rescaleContentStreamDict(dict pdf.PDFDict, rewrite contentOpRewriter) (pdf.PDFDict, bool) {
	data, err := pdf.DecodeStream(dict)
	if err != nil {
		return dict, false
	}
	rewritten, ok := rescalePathsAndRewrite(data, rewrite)
	if !ok {
		return dict, false
	}
	if err := writer.SetStreamFlate(&dict, rewritten); err != nil {
		return dict, false
	}
	return dict, true
}

// rescalePathsAndRewrite returns data with every rescalable path folded into
// the CTM and every operator passed through rewrite, or ok=false if neither
// changed anything.
func rescalePathsAndRewrite(data []byte, rewrite contentOpRewriter) ([]byte, bool) {
	var cw writer.ContentStreamWriter
	r := &pathRescaler{w: &cw, rewrite: rewrite, state: graphicsState{lineWidth: defaultLineWidth}}
	cs := pdf.NewContentScanner(data)
	cs.Scan(r.op)
	r.emitBuffered() // a path the stream ended without painting
	// Only part of the stream was read, so only part of it was written back:
	// see rewriteContentStreamDict.
	if !r.changed || !cs.Complete() || cw.Err() != nil {
		return nil, false
	}
	return cw.Bytes(), true
}

// graphicsState is the part of the state a rescaled path has to restore. Line
// width and dash lengths are measured in user space, so drawing them under a
// scaled CTM would change how thick and how broken the line comes out.
type graphicsState struct {
	lineWidth float64
	// dash is the last d operator's two operands, or nil for the solid default.
	dash []pdf.PDFValue
	// Set when an ExtGState was applied, since it may have set either of the
	// two above to something the stream itself does not say.
	widthUnknown bool
	dashUnknown  bool
}

// pathRescaler walks one content stream, holding each path until the operator
// that paints it says whether the path can be rescaled.
type pathRescaler struct {
	w *writer.ContentStreamWriter
	// rewrite is applied to every operator on its way out, so the clamp still
	// sees whatever the rescale left out of range.
	rewrite contentOpRewriter
	state   graphicsState
	stack   []graphicsState

	path    []writer.ContentOp // the path being collected
	largest float64            // its largest out-of-range coordinate
	spilled bool               // the path outgrew the buffer and is going out as it comes
	changed bool
}

func (r *pathRescaler) op(op string, operands []pdf.PDFValue) {
	switch {
	case pathBuildOps[op]:
		r.collect(op, operands)
	case paintOps[op]:
		r.paint(op)
	default:
		// Anything else cannot appear inside a path, so a path still open here
		// is malformed and goes out as it was written.
		r.emitBuffered()
		r.track(op, operands)
		if op == "cm" {
			if scale, divided, ok := splitOversizedMatrix(operands); ok {
				r.emit("cm", scale)
				r.emit("cm", divided)
				r.changed = true
				return
			}
		}
		r.emit(op, operands)
	}
}

// splitOversizedMatrix writes a matrix that does not fit as two that do. A
// matrix cannot be folded into the drawing the way a coordinate can, since it
// is the drawing's placement itself -- but scaling up and then applying the
// same matrix divided by that scale composes to exactly what was written, and
// both halves are in range. It is what places an image, so clamping it instead
// shrinks the picture to a corner of where it belongs.
func splitOversizedMatrix(operands []pdf.PDFValue) (scale, divided []pdf.PDFValue, ok bool) {
	if len(operands) != 6 {
		return nil, nil, false
	}
	nums := make([]float64, 6)
	largest := 0.0
	for i, v := range operands {
		f, isNum := numericValue(v)
		if !isNum {
			return nil, nil, false
		}
		nums[i] = f
		if abs := math.Abs(f); abs > realLimit && abs > largest {
			largest = abs
		}
	}
	if largest == 0 {
		return nil, nil, false
	}
	s := scaleFor(largest)
	if s == 0 {
		return nil, nil, false // too far out of range to write as two; the clamp takes it
	}
	divided = make([]pdf.PDFValue, 6)
	for i, f := range nums {
		divided[i] = pdf.PDFReal(f / s)
	}
	return scaleMatrix(s), divided, true
}

// collect holds one path-building operator, or writes it straight out once the
// path has grown past what is worth buffering.
func (r *pathRescaler) collect(op string, operands []pdf.PDFValue) {
	if !r.spilled && len(r.path) >= maxBufferedPathOps {
		r.emitBuffered()
		r.spilled = true
	}
	if r.spilled {
		r.emit(op, operands)
		return
	}
	r.path = append(r.path, writer.ContentOp{Op: op, Operands: copyOperands(operands)})
	for _, v := range operands {
		if f, ok := v.(pdf.PDFReal); ok {
			if abs := math.Abs(float64(f)); abs > realLimit && abs > r.largest {
				r.largest = abs
			}
		}
	}
}

// paint closes the current path, rescaling it if it needs rescaling and can be
// rescaled, and writing it out unchanged otherwise.
func (r *pathRescaler) paint(op string) {
	defer r.reset()

	stroking := strokingPaintOps[op]
	scale := 0.0
	if !r.spilled && r.largest > 0 {
		scale = scaleFor(r.largest)
	}
	// Nothing out of range, too far out of range to fold into a matrix, or a
	// stroke whose width and dash an ExtGState may have set out of view: leave
	// the path alone and let the clamp deal with it.
	if scale == 0 || (stroking && r.stateHidden()) {
		r.emitBuffered()
		r.emit(op, nil)
		return
	}

	shrink := 1 / scale
	r.emit("cm", scaleMatrix(scale))
	if stroking {
		r.emitLineWidth(r.state.lineWidth * shrink)
		r.emitDash(shrink)
	}
	for _, p := range r.path {
		r.emit(p.Op, scaleOperands(p.Operands, shrink))
	}
	r.emit(op, nil)
	r.emit("cm", scaleMatrix(shrink))
	if stroking {
		r.emitLineWidth(r.state.lineWidth)
		r.emitDash(1)
	}
	r.changed = true
}

// stateHidden reports whether an ExtGState may have changed the line width or
// the dash pattern, leaving no way to write back what was in force.
func (r *pathRescaler) stateHidden() bool {
	return r.state.widthUnknown || r.state.dashUnknown
}

// track follows the few state operators a rescaled stroke depends on.
func (r *pathRescaler) track(op string, operands []pdf.PDFValue) {
	switch op {
	case "q":
		r.stack = append(r.stack, r.state)
	case "Q":
		if n := len(r.stack); n > 0 {
			r.state = r.stack[n-1]
			r.stack = r.stack[:n-1]
		}
	case "w":
		if len(operands) == 1 {
			if f, ok := numericValue(operands[0]); ok {
				r.state.lineWidth, r.state.widthUnknown = f, false
			}
		}
	case "d":
		if len(operands) == 2 {
			r.state.dash, r.state.dashUnknown = copyOperands(operands), false
		}
	case "gs":
		r.state.widthUnknown, r.state.dashUnknown = true, true
	}
}

// emitBuffered writes out the collected path as it was scanned.
func (r *pathRescaler) emitBuffered() {
	for _, p := range r.path {
		r.emit(p.Op, p.Operands)
	}
	r.path = nil
}

// emitLineWidth writes a w operator, so a stroke drawn under a scaled CTM comes
// out the thickness it was meant to be.
func (r *pathRescaler) emitLineWidth(width float64) {
	r.emit("w", []pdf.PDFValue{pdf.PDFReal(width)})
}

// emitDash writes back the dash pattern with its lengths scaled, or nothing if
// the line is solid, which no scaling changes. A scale of 1 puts the pattern
// back exactly as the stream wrote it.
func (r *pathRescaler) emitDash(scale float64) {
	if r.state.dash == nil {
		return
	}
	if scale == 1 {
		r.emit("d", r.state.dash)
		return
	}
	r.emit("d", scaleOperands(r.state.dash, scale))
}

func (r *pathRescaler) emit(op string, operands []pdf.PDFValue) {
	rewritten, keep := r.rewrite(op, operands, &r.changed)
	if !keep {
		return
	}
	_ = r.w.WriteOp(rewritten.Op, rewritten.Operands) // the writer keeps the first error
}

func (r *pathRescaler) reset() {
	r.path, r.largest, r.spilled = nil, 0, false
}

// scaleFor returns the factor to fold into the CTM to bring largest inside the
// limit, or 0 if no allowed factor is enough. Factors are powers of two, so
// dividing a coordinate by one only shifts its exponent: nothing is rounded
// away, and the matrix that undoes the scale undoes it exactly.
func scaleFor(largest float64) float64 {
	for scale := 1.0; scale <= maxScale; scale *= 2 {
		if largest/scale <= realLimit {
			return scale
		}
	}
	return 0
}

// scaleMatrix is the operand list of a cm that scales both axes by scale.
func scaleMatrix(scale float64) []pdf.PDFValue {
	s := pdf.PDFReal(scale)
	return []pdf.PDFValue{s, pdf.PDFReal(0), pdf.PDFReal(0), s, pdf.PDFReal(0), pdf.PDFReal(0)}
}

// scaleOperands multiplies every number in operands by scale, descending one
// level into an array so a dash pattern scales with the path it dashes.
func scaleOperands(operands []pdf.PDFValue, scale float64) []pdf.PDFValue {
	out := make([]pdf.PDFValue, len(operands))
	for i, v := range operands {
		if a, ok := v.(pdf.PDFArray); ok {
			out[i] = pdf.PDFArray(scaleOperands(a, scale))
			continue
		}
		if f, ok := numericValue(v); ok {
			out[i] = pdf.PDFReal(f * scale)
			continue
		}
		out[i] = v
	}
	return out
}

// copyOperands takes a copy of a scanned operator's operands, which the
// scanner reuses as soon as its callback returns.
func copyOperands(operands []pdf.PDFValue) []pdf.PDFValue {
	out := make([]pdf.PDFValue, len(operands))
	for i, v := range operands {
		if a, ok := v.(pdf.PDFArray); ok {
			out[i] = pdf.PDFArray(copyOperands(a))
			continue
		}
		out[i] = v
	}
	return out
}

// numericValue reads a value that is a number either way it was written.
func numericValue(v pdf.PDFValue) (float64, bool) {
	switch n := v.(type) {
	case pdf.PDFReal:
		return float64(n), true
	case pdf.PDFInteger:
		return float64(n), true
	}
	return 0, false
}
