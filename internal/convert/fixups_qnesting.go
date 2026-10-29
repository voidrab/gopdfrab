package convert

import (
	"fmt"
	"math"
	"slices"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
	"github.com/voidrab/gopdfrab/internal/raster"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// This file repairs GraphicsStateNesting (6.1.12: q/Q nested deeper than 28)
// in place. The limit is counted per content stream, and invoking a form
// XObject saves and restores the graphics state exactly as q ... Q does. So a
// q ... Q block that would reach past the limit is moved, contents and all,
// into a form XObject the stream draws, where its nesting starts again
// from zero. A block that is itself still too deep is split again inside the
// form.
//
// The form differs from the block in two ways, and both are made harmless:
//   - Drawing the form clips to its /BBox. The BBox is the area the stream
//     can show at all -- the page's MediaBox, or the BBox of the form or
//     pattern being repaired -- mapped back through the CTM the stream has
//     built up by the block, so nothing visible is cut away.
//   - The form reads names from its own /Resources. It is given the very dict
//     the stream reads them from, so every name means what it meant. A stream
//     whose resource scope is not known for certain (a form with no
//     /Resources of its own, a Type 3 glyph) is left to the raster fallback.
//
// A block that cannot become a form is also left to the fallback: one opening
// inside a text object (an XObject may not be drawn there), one cutting
// across a marked-content or BX/EX section, and one still open where the
// stream ends while later page content could close it.

func init() {
	registerFixer(qNestingFixer{})
}

// qNestingFixer remediates Checks.Structure.GraphicsStateNesting, to the
// level's q/Q nesting limit.
type qNestingFixer struct{ spec *pdfa.Spec }

func (f qNestingFixer) withSpec(s *pdfa.Spec) Fixer { f.spec = s; return f }

func (qNestingFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Structure.GraphicsStateNesting
}

func (f qNestingFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	limit := f.spec.Limits.MaxQNesting
	changed := false
	// One rewrite per stream, so a stream two pages share is replaced in both
	// by the same new dict.
	done := map[uintptr]pdf.PDFDict{}
	rewrite := func(d, resources pdf.PDFDict, visible rect, last bool) (pdf.PDFDict, bool) {
		ptr := pdf.ValuePointer(d.Entries)
		if fixed, ok := done[ptr]; ok {
			return fixed, true
		}
		fixed, ok := unnestStream(d, resources, visible, last, limit)
		if ok {
			done[ptr] = fixed
			changed = true
		}
		return fixed, ok
	}

	for _, page := range orderedPages(*trailer) {
		if page.resources.Entries == nil {
			continue
		}
		visible := rect{page.mediaBox[0], page.mediaBox[1], page.mediaBox[2], page.mediaBox[3]}
		switch contents := page.dict.Entries.Get("Contents").(type) {
		case pdf.PDFDict:
			if fixed, ok := rewrite(contents, page.resources, visible, true); ok {
				page.dict.Entries.Set("Contents", fixed)
			}
		case pdf.PDFArray:
			for i, item := range contents {
				// The array is one drawing cut into pieces, so a block left
				// open at the end of a piece may be closed by the next one.
				if cd, ok := item.(pdf.PDFDict); ok {
					if fixed, ok := rewrite(cd, page.resources, visible, i == len(contents)-1); ok {
						contents[i] = fixed
					}
				}
			}
		}
	}

	walkStreamDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) (pdf.PDFDict, bool) {
		isForm := (d.Entries.Get("Subtype") == pdf.PDFName{Value: "Form"})
		isTiling := d.Entries.Get("PatternType") == pdf.PDFInteger(1)
		if !d.HasStream || (!isForm && !isTiling) {
			return d, false
		}
		resources, ok := d.Entries.Get("Resources").(pdf.PDFDict)
		if !ok || resources.Entries == nil {
			return d, false
		}
		bbox, ok := rectOf(d.Entries.Get("BBox"))
		if !ok {
			return d, false
		}
		return rewrite(d, resources, bbox, true)
	})
	return changed, nil
}

// rect is an axis-aligned rectangle as [llx lly urx ury].
type rect [4]float64

// rectOf reads a four-number PDF rectangle, normalising its corners.
func rectOf(v pdf.PDFValue) (rect, bool) {
	arr, ok := v.(pdf.PDFArray)
	if !ok || len(arr) != 4 {
		return rect{}, false
	}
	var r rect
	for i, item := range arr {
		f, ok := numericValue(item)
		if !ok {
			return rect{}, false
		}
		r[i] = f
	}
	return rect{math.Min(r[0], r[2]), math.Min(r[1], r[3]), math.Max(r[0], r[2]), math.Max(r[1], r[3])}, true
}

// unnestStream rewrites one content stream so no q/Q block in it nests past
// limit, or reports false and leaves it alone -- when it does not need
// it, or when a block cannot be moved without changing the drawing. resources
// is the dict the stream resolves names in, visible the area it can show in
// its own space, and last whether nothing drawn after it could close a block
// it leaves open. A zero limit sets none.
func unnestStream(d, resources pdf.PDFDict, visible rect, last bool, limit int) (pdf.PDFDict, bool) {
	if limit == 0 {
		return d, false
	}
	data, err := pdf.DecodeStream(d)
	if err != nil {
		return d, false
	}
	var ops []writer.ContentOp
	depth, deepest := 0, 0
	cs := pdf.NewContentScanner(data)
	cs.Scan(func(op string, operands []pdf.PDFValue) {
		// The scanner reuses its operand slice from one operator to the next.
		ops = append(ops, writer.ContentOp{Op: op, Operands: slices.Clone(operands)})
		switch op {
		case "q":
			depth++
			deepest = max(deepest, depth)
		case "Q":
			depth = max(depth-1, 0)
		}
	})
	if deepest <= limit || !cs.Complete() {
		return d, false
	}
	return unnestOps(d, ops, resources, visible, last, limit)
}

// unnestOps is unnestStream past the scan: it rewrites ops, the stream's
// operators, and stores the result in d.
func unnestOps(d pdf.PDFDict, ops []writer.ContentOp, resources pdf.PDFDict, visible rect, last bool, limit int) (pdf.PDFDict, bool) {
	u := unnester{resources: resources, limit: limit}
	out, ok := u.run(ops, visible, last)
	if !ok {
		return d, false
	}
	encoded, ok := encodeContentOps(out)
	if !ok {
		return d, false
	}
	writer.SetStreamFlate(&d, encoded)
	return d, true
}

// unnester moves too-deep blocks into form XObjects named in resources.
type unnester struct {
	resources pdf.PDFDict
	next      int
	// limit is the deepest nesting the output may reach.
	limit int
}

// run returns ops with every q block that would exceed u.limit replaced by
// an XObject invocation that draws a form holding it. ops is drawn in a space
// whose visible part is visible; closed says a block still open at the end
// may be closed there.
func (u *unnester) run(ops []writer.ContentOp, visible rect, closed bool) ([]writer.ContentOp, bool) {
	out := make([]writer.ContentOp, 0, len(ops))
	ctm := raster.IdentityMatrix
	var saved []raster.Matrix
	inText := false
	for i := 0; i < len(ops); i++ {
		op := ops[i]
		switch op.Op {
		case "q":
			if len(saved)+1 == u.limit {
				end, deepest := blockEnd(ops, i, u.limit)
				if deepest > u.limit {
					if inText || (end == len(ops) && !closed) || !selfContained(ops[i+1:min(end, len(ops))]) {
						return nil, false
					}
					do, ok := u.extract(ops[i+1:min(end, len(ops))], ctm, visible)
					if !ok {
						return nil, false
					}
					out = append(out, do)
					i = end // the matching Q goes with the block
					continue
				}
			}
			saved = append(saved, ctm)
		case "Q":
			if n := len(saved); n > 0 {
				ctm, saved = saved[n-1], saved[:n-1]
			}
		case "cm":
			if m, ok := cmMatrix(op.Operands); ok {
				ctm = m.Mul(ctm)
			}
		case "BT":
			inText = true
		case "ET":
			inText = false
		}
		out = append(out, op)
	}
	return out, true
}

// extract builds the form XObject holding inner, a block's content without
// its q and Q, and returns the operator that draws it. ctm maps the block's
// space into the space visible is given in.
func (u *unnester) extract(inner []writer.ContentOp, ctm raster.Matrix, visible rect) (writer.ContentOp, bool) {
	inv, ok := ctm.Invert()
	if !ok {
		return writer.ContentOp{}, false
	}
	bbox := transformRect(visible, inv)
	body, ok := u.run(inner, bbox, true)
	if !ok {
		return writer.ContentOp{}, false
	}
	encoded, ok := encodeContentOps(body)
	if !ok {
		return writer.ContentOp{}, false
	}

	form := pdf.NewPDFDict()
	form.Entries.Set("Type", pdf.PDFName{Value: "XObject"})
	form.Entries.Set("Subtype", pdf.PDFName{Value: "Form"})
	// Integers, rounded outward: a real is capped at 32767 in PDF/A-1, which a
	// steep scale down would push the bounds past.
	form.Entries.Set("BBox", pdf.PDFArray{
		pdf.PDFInteger(math.Floor(bbox[0])), pdf.PDFInteger(math.Floor(bbox[1])),
		pdf.PDFInteger(math.Ceil(bbox[2])), pdf.PDFInteger(math.Ceil(bbox[3])),
	})
	form.Entries.Set("Resources", u.resources)
	writer.SetStreamFlate(&form, encoded)

	xobjects, ok := u.resources.Entries.Get("XObject").(pdf.PDFDict)
	if !ok {
		xobjects = pdf.NewPDFDict()
		u.resources.Entries.Set("XObject", xobjects)
	}
	name := u.freeName(xobjects)
	xobjects.Entries.Set(name, form)
	return writer.ContentOp{Op: "Do", Operands: []pdf.PDFValue{pdf.PDFName{Value: name}}}, true
}

// freeName returns the first QNest<n> name xobjects does not hold yet.
func (u *unnester) freeName(xobjects pdf.PDFDict) string {
	for {
		name := fmt.Sprintf("QNest%d", u.next)
		u.next++
		if xobjects.Entries.Get(name) == nil {
			return name
		}
	}
}

// blockEnd finds the Q closing the q at ops[start], returning its index (or
// len(ops) when the stream ends first) and the deepest nesting reached
// inside, counting the block's own q from the depth start sits at, limit.
func blockEnd(ops []writer.ContentOp, start, limit int) (end, deepest int) {
	depth := limit
	deepest = depth
	for i := start + 1; i < len(ops); i++ {
		switch ops[i].Op {
		case "q":
			depth++
			deepest = max(deepest, depth)
		case "Q":
			depth--
			if depth < limit {
				return i, deepest
			}
		}
	}
	return len(ops), deepest
}

// selfContained reports whether ops opens and closes its own text objects,
// marked-content sequences and compatibility sections, so it can stand alone
// as a form's content.
func selfContained(ops []writer.ContentOp) bool {
	inText := false
	marked, compat := 0, 0
	for _, op := range ops {
		switch op.Op {
		case "BT":
			if inText {
				return false
			}
			inText = true
		case "ET":
			if !inText {
				return false
			}
			inText = false
		case "BMC", "BDC":
			marked++
		case "EMC":
			if marked--; marked < 0 {
				return false
			}
		case "BX":
			compat++
		case "EX":
			if compat--; compat < 0 {
				return false
			}
		}
	}
	return !inText && marked == 0 && compat == 0
}

// cmMatrix reads a cm operator's six operands.
func cmMatrix(operands []pdf.PDFValue) (raster.Matrix, bool) {
	if len(operands) != 6 {
		return raster.Matrix{}, false
	}
	var v [6]float64
	for i, o := range operands {
		f, ok := numericValue(o)
		if !ok {
			return raster.Matrix{}, false
		}
		v[i] = f
	}
	return raster.Matrix{A: v[0], B: v[1], C: v[2], D: v[3], E: v[4], F: v[5]}, true
}

// transformRect maps r through m and returns the bounds of the result.
func transformRect(r rect, m raster.Matrix) rect {
	corners := [4]raster.Point{{X: r[0], Y: r[1]}, {X: r[2], Y: r[1]}, {X: r[0], Y: r[3]}, {X: r[2], Y: r[3]}}
	out := rect{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, c := range corners {
		p := m.Apply(c)
		out = rect{math.Min(out[0], p.X), math.Min(out[1], p.Y), math.Max(out[2], p.X), math.Max(out[3], p.Y)}
	}
	return out
}

// encodeContentOps serializes ops as a content stream.
func encodeContentOps(ops []writer.ContentOp) ([]byte, bool) {
	var cw writer.ContentStreamWriter
	for _, op := range ops {
		_ = cw.WriteOp(op.Op, op.Operands)
	}
	return cw.Bytes(), cw.Err() == nil
}
