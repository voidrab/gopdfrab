package fontprog

import "math"

// maxSubrDepth is the subroutine nesting limit the Type 2 charstring format
// sets.
const maxSubrDepth = 10

// type2Interp interprets Type 2 charstrings. With a sink it draws the whole
// glyph into it; without one it stops at the first stack-clearing operator,
// which is where the advance width is decided.
type type2Interp struct {
	stack  []float64
	x, y   float64
	nStems int
	// gsubrs and lsubrs are the global and local subroutines callgsubr and
	// callsubr index into.
	gsubrs, lsubrs [][]byte
	sink           PathSink

	// widthDone is set once the first stack-clearing operator has run; the
	// width it decided is width when hasWidth.
	widthDone, hasWidth bool
	width               float64
	// bad records that the charstring broke the grammar before the width was
	// decided, so no width can be read from it.
	bad bool
}

// Type2CharstringWidth interprets a Type2 charstring only up to its first
// stack-clearing operator and reports the width operand, if present, as
// (width, true, true). ok is false when the prefix cannot be followed safely.
func Type2CharstringWidth(cs []byte, gsubrs, lsubrs [][]byte) (width float64, hasWidth, ok bool) {
	in := type2Interp{gsubrs: gsubrs, lsubrs: lsubrs}
	in.run(cs, 0)
	if in.bad || !in.widthDone {
		return 0, false, false
	}
	return in.width, in.hasWidth, true
}

// Type2Outline draws Type 2 charstring cs into sink, calling into gsubrs and
// lsubrs. A charstring that cannot be followed to its end draws what it drew
// up to that point.
func Type2Outline(cs []byte, gsubrs, lsubrs [][]byte, sink PathSink) {
	in := type2Interp{gsubrs: gsubrs, lsubrs: lsubrs, sink: sink}
	in.run(cs, 0)
}

// fail marks the charstring as unreadable for its width, if the width is not
// decided yet. It reports whether interpretation should stop, which it does
// without a sink, since then only the width is wanted.
func (in *type2Interp) fail() (stop bool) {
	if !in.widthDone {
		in.bad = true
	}
	return in.sink == nil
}

// takeWidth decides the width at the first stack-clearing operator op: the
// bottom operand is the width when the stack holds more than op takes. It
// reports whether interpretation should stop.
func (in *type2Interp) takeWidth(op byte) (stop bool) {
	if in.widthDone {
		return false
	}
	var extra bool
	switch n := len(in.stack); op {
	case 21: // rmoveto
		extra = n > 2
	case 4, 22: // vmoveto, hmoveto
		extra = n > 1
	case 14: // endchar, or the deprecated 4-operand seac form
		switch n {
		case 1, 5:
			extra = true
		case 0, 4:
		default:
			return in.fail()
		}
	default: // stem and hintmask operators take operand pairs
		extra = n%2 == 1
	}
	in.widthDone = true
	if extra {
		in.width, in.hasWidth = in.stack[0], true
		in.stack = in.stack[1:]
	}
	return in.sink == nil
}

// run interprets cs, depth subroutine calls deep. It reports whether the
// glyph ended (endchar, a call that could not be followed, or the width being
// decided when only the width is wanted) as opposed to cs returning to its
// caller.
func (in *type2Interp) run(cs []byte, depth int) (ended bool) {
	i := 0
	for i < len(cs) {
		b := cs[i]
		if b >= 32 || b == 28 {
			v, n, ok := Type2Number(cs[i:])
			if !ok {
				in.fail()
				return true
			}
			in.stack = append(in.stack, v)
			i += n
			continue
		}
		i++
		switch b {
		case 10, 29: // callsubr, callgsubr
			if len(in.stack) == 0 || depth >= maxSubrDepth {
				in.fail()
				return true
			}
			subrs := in.lsubrs
			if b == 29 {
				subrs = in.gsubrs
			}
			sub, ok := CFFSubr(subrs, int(in.stack[len(in.stack)-1]))
			in.stack = in.stack[:len(in.stack)-1]
			if !ok {
				in.fail()
				return true
			}
			if in.run(sub, depth+1) {
				return true
			}
			continue
		case 11: // return
			return false
		case 1, 3, 18, 23, 19, 20, 21, 22, 4, 14: // the stack-clearing operators
			if in.takeWidth(b) {
				return true
			}
		default: // a drawing or escape operator ahead of the width is out of order
			if !in.widthDone && in.fail() {
				return true
			}
		}

		switch b {
		case 12: // two-byte escape: the flex family draws; the rest
			// (arithmetic, storage) do not touch the outline
			if i < len(cs) {
				in.flex(cs[i])
			}
			i++
		case 1, 3, 18, 23: // hstem/vstem/hstemhm/vstemhm
			in.nStems += len(in.stack) / 2
		case 19, 20: // hintmask/cntrmask
			in.nStems += len(in.stack) / 2
			i += (in.nStems + 7) / 8
		case 21: // rmoveto
			if len(in.stack) >= 2 {
				in.moveTo(in.x+in.stack[0], in.y+in.stack[1])
			}
		case 22: // hmoveto
			if len(in.stack) >= 1 {
				in.moveTo(in.x+in.stack[0], in.y)
			}
		case 4: // vmoveto
			if len(in.stack) >= 1 {
				in.moveTo(in.x, in.y+in.stack[0])
			}
		case 5: // rlineto
			for j := 0; j+1 < len(in.stack); j += 2 {
				in.lineTo(in.x+in.stack[j], in.y+in.stack[j+1])
			}
		case 6: // hlineto
			in.alternatingLineTo(true)
		case 7: // vlineto
			in.alternatingLineTo(false)
		case 8: // rrcurveto
			for j := 0; j+5 < len(in.stack); j += 6 {
				in.rcurveTo(in.stack[j:])
			}
		case 24: // rcurveline
			j := 0
			for ; j+5 < len(in.stack)-2; j += 6 {
				in.rcurveTo(in.stack[j:])
			}
			if j+1 < len(in.stack) {
				in.lineTo(in.x+in.stack[j], in.y+in.stack[j+1])
			}
		case 25: // rlinecurve
			j := 0
			for ; j+1 < len(in.stack)-6; j += 2 {
				in.lineTo(in.x+in.stack[j], in.y+in.stack[j+1])
			}
			if j+5 < len(in.stack) {
				in.rcurveTo(in.stack[j:])
			}
		case 26: // vvcurveto
			in.vvCurveTo()
		case 27: // hhcurveto
			in.hhCurveTo()
		case 30: // vhcurveto
			in.vhhvCurveTo(false)
		case 31: // hvcurveto
			in.vhhvCurveTo(true)
		case 14: // endchar
			in.stack = in.stack[:0]
			return true
		}
		in.stack = in.stack[:0]
	}
	return false
}

func (in *type2Interp) moveTo(x, y float64) {
	in.x, in.y = x, y
	in.sink.MoveTo(x, y)
}

func (in *type2Interp) lineTo(x, y float64) {
	in.x, in.y = x, y
	in.sink.LineTo(x, y)
}

func (in *type2Interp) curveTo(x1, y1, x2, y2, x3, y3 float64) {
	in.x, in.y = x3, y3
	in.sink.CurveTo(x1, y1, x2, y2, x3, y3)
}

// rcurveTo draws the curve whose six operands, relative like rrcurveto's,
// start a.
func (in *type2Interp) rcurveTo(a []float64) {
	in.curveTo(in.x+a[0], in.y+a[1], in.x+a[0]+a[2], in.y+a[1]+a[3], in.x+a[0]+a[2]+a[4], in.y+a[1]+a[3]+a[5])
}

// flex draws the two curves of a flex escape operator (12 34-37), whose
// operands are relative to the current point like rrcurveto's. The flex depth
// argument only matters for hinting, so the curves are always drawn.
func (in *type2Interp) flex(op byte) {
	a := in.stack
	var d [12]float64 // dx1 dy1 ... dx6 dy6
	switch op {
	case 35: // flex: dx1 dy1 ... dx6 dy6 fd
		if len(a) < 12 {
			return
		}
		copy(d[:], a[:12])
	case 34: // hflex: dx1 dx2 dy2 dx3 dx4 dx5 dx6
		if len(a) < 7 {
			return
		}
		d = [12]float64{a[0], 0, a[1], a[2], a[3], 0, a[4], 0, a[5], -a[2], a[6], 0}
	case 36: // hflex1: dx1 dy1 dx2 dy2 dx3 dx4 dx5 dy5 dx6
		if len(a) < 9 {
			return
		}
		d = [12]float64{a[0], a[1], a[2], a[3], a[4], 0, a[5], 0, a[6], a[7], a[8], -(a[1] + a[3] + a[7])}
	case 37: // flex1: dx1 dy1 ... dx5 dy5 d6
		if len(a) < 11 {
			return
		}
		copy(d[:10], a[:10])
		var dx, dy float64
		for j := 0; j < 10; j += 2 {
			dx += a[j]
			dy += a[j+1]
		}
		if math.Abs(dx) > math.Abs(dy) {
			d[10], d[11] = a[10], -dy
		} else {
			d[10], d[11] = -dx, a[10]
		}
	default:
		return
	}
	in.rcurveTo(d[:6])
	in.rcurveTo(d[6:])
}

// alternatingLineTo implements hlineto/vlineto, which alternate the axis of
// each successive line segment, starting horizontal (hlineto) or vertical.
func (in *type2Interp) alternatingLineTo(startHorizontal bool) {
	horiz := startHorizontal
	for _, v := range in.stack {
		if horiz {
			in.lineTo(in.x+v, in.y)
		} else {
			in.lineTo(in.x, in.y+v)
		}
		horiz = !horiz
	}
}

// vvCurveTo implements vvcurveto: a series of curves mostly-vertical, with
// an optional leading dx1 on the first curve only.
func (in *type2Interp) vvCurveTo() {
	a := in.stack
	dx1 := 0.0
	j := 0
	if len(a)%4 == 1 {
		dx1 = a[0]
		j = 1
	}
	for ; j+3 < len(a); j += 4 {
		x1, y1 := in.x+dx1, in.y+a[j]
		x2, y2 := x1+a[j+1], y1+a[j+2]
		in.curveTo(x1, y1, x2, y2, x2, y2+a[j+3])
		dx1 = 0
	}
}

// hhCurveTo implements hhcurveto: the horizontal counterpart of vvcurveto.
func (in *type2Interp) hhCurveTo() {
	a := in.stack
	dy1 := 0.0
	j := 0
	if len(a)%4 == 1 {
		dy1 = a[0]
		j = 1
	}
	for ; j+3 < len(a); j += 4 {
		x1, y1 := in.x+a[j], in.y+dy1
		x2, y2 := x1+a[j+1], y1+a[j+2]
		in.curveTo(x1, y1, x2, y2, x2+a[j+3], y2)
		dy1 = 0
	}
}

// vhhvCurveTo implements vhcurveto/hvcurveto, which alternate the tangent
// axis between successive curve segments.
func (in *type2Interp) vhhvCurveTo(startHorizontal bool) {
	a := in.stack
	horiz := startHorizontal
	for j := 0; j+3 < len(a); j += 4 {
		last := j+4 >= len(a)-1
		if horiz {
			x1, y1 := in.x+a[j], in.y
			x2, y2 := x1+a[j+1], y1+a[j+2]
			x3 := x2
			if last && j+4 < len(a) {
				x3 = x2 + a[j+4]
			}
			in.curveTo(x1, y1, x2, y2, x3, y2+a[j+3])
		} else {
			x1, y1 := in.x, in.y+a[j]
			x2, y2 := x1+a[j+1], y1+a[j+2]
			y3 := y2
			if last && j+4 < len(a) {
				y3 = y2 + a[j+4]
			}
			in.curveTo(x1, y1, x2, y2, x2+a[j+3], y3)
		}
		horiz = !horiz
	}
}
