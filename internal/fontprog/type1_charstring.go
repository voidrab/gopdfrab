package fontprog

// Type1SubrDepth bounds Type 1 callsubr nesting, which the format leaves
// open, so subrs that call each other in a cycle cannot spin. It is the
// Type 2 limit.
const Type1SubrDepth = maxSubrDepth

// type1Interp interprets decrypted Type 1 charstrings. With a sink it draws
// the whole glyph into it; without one it stops at hsbw or sbw, which carry
// the advance width.
type type1Interp struct {
	stack []float64
	x, y  float64
	subrs [][]byte
	sink  PathSink

	// widthFound is set once hsbw or sbw has given the width.
	widthFound bool
	width      float64

	// glyphs supplies seac's base and accent glyphs; nil leaves seac
	// undrawn.
	glyphs map[string][]byte
	// ox, oy offset the glyph: seac places its accent this way.
	ox, oy float64
	// inSeac stops a seac component from composing another.
	inSeac bool
	// ps is the PostScript operand stack callothersubr leaves results on
	// for pop.
	ps []float64
	// flexing collects the flex points OtherSubrs 1 starts and 0 draws:
	// inside a flex, rmoveto records a point instead of moving.
	flexing      bool
	flexX, flexY float64
	flexPts      [][2]float64
}

// type1AdvanceWidth returns the advance width (wx) the hsbw or sbw command of
// decrypted Type 1 charstring cs gives. subrs are the program's decrypted
// Subrs: a subroutinized program pushes the hsbw operands and leaves the
// command itself in a subr, so a scan that stops at callsubr finds no width
// at all.
func type1AdvanceWidth(cs []byte, subrs [][]byte) (float64, bool) {
	in := type1Interp{subrs: subrs}
	in.run(cs, 0)
	return in.width, in.widthFound
}

// pop removes and returns the top n operands, oldest first, or nil when the
// stack holds fewer.
func (in *type1Interp) pop(n int) []float64 {
	if len(in.stack) < n {
		return nil
	}
	args := in.stack[len(in.stack)-n:]
	in.stack = in.stack[:len(in.stack)-n]
	return args
}

// run interprets cs, depth subroutine calls deep. It reports whether the
// glyph ended (endchar, seac, a call that could not be followed, or the width
// being found when only the width is wanted) as opposed to cs returning to
// its caller.
func (in *type1Interp) run(cs []byte, depth int) (ended bool) {
	i := 0
	for i < len(cs) {
		b := cs[i]
		if b >= 32 || b == 28 {
			v, n, ok := Type1Number(cs[i:])
			if !ok {
				return true
			}
			in.stack = append(in.stack, v)
			i += n
			continue
		}
		i++
		switch b {
		case 4: // vmoveto
			if len(in.stack) >= 1 {
				in.moveTo(in.x, in.y+in.stack[0])
			}
		case 5: // rlineto
			if len(in.stack) >= 2 {
				in.lineTo(in.x+in.stack[0], in.y+in.stack[1])
			}
		case 6: // hlineto
			if len(in.stack) >= 1 {
				in.lineTo(in.x+in.stack[0], in.y)
			}
		case 7: // vlineto
			if len(in.stack) >= 1 {
				in.lineTo(in.x, in.y+in.stack[0])
			}
		case 8: // rrcurveto
			if len(in.stack) >= 6 {
				a := in.stack
				in.curveTo(in.x+a[0], in.y+a[1], in.x+a[0]+a[2], in.y+a[1]+a[3], in.x+a[0]+a[2]+a[4], in.y+a[1]+a[3]+a[5])
			}
		case 10: // callsubr
			a := in.pop(1)
			if a == nil || depth >= Type1SubrDepth {
				return true
			}
			n := int(a[0])
			if n < 0 || n >= len(in.subrs) || in.subrs[n] == nil {
				return true
			}
			if in.run(in.subrs[n], depth+1) {
				return true
			}
			continue
		case 11: // return
			return false
		case 13: // hsbw: sbx wx
			if len(in.stack) < 2 {
				if in.sink == nil {
					return true
				}
				break
			}
			if in.setWidth(in.stack[1]) {
				return true
			}
			in.x, in.y = in.ox+in.stack[0], in.oy
		case 21: // rmoveto
			if len(in.stack) >= 2 {
				in.moveTo(in.x+in.stack[0], in.y+in.stack[1])
			}
		case 22: // hmoveto
			if len(in.stack) >= 1 {
				in.moveTo(in.x+in.stack[0], in.y)
			}
		case 30: // vhcurveto
			if len(in.stack) >= 4 {
				a := in.stack
				in.curveTo(in.x, in.y+a[0], in.x+a[1], in.y+a[0]+a[2], in.x+a[1]+a[3], in.y+a[0]+a[2])
			}
		case 31: // hvcurveto
			if len(in.stack) >= 4 {
				a := in.stack
				in.curveTo(in.x+a[0], in.y, in.x+a[0]+a[1], in.y+a[2], in.x+a[0]+a[1], in.y+a[2]+a[3])
			}
		case 12: // two-byte escape
			if i >= len(cs) {
				return true
			}
			esc := cs[i]
			i++
			if in.escape(esc) {
				return true
			}
			continue
		case 14: // endchar
			in.stack = in.stack[:0]
			return true
		}
		// Commands that draw nothing, such as hstem, vstem and closepath,
		// and undefined commands clear the operand stack, as do the
		// commands above once they have run.
		in.stack = in.stack[:0]
	}
	return false
}

// setWidth records the advance width hsbw or sbw gives and reports whether
// interpretation should stop, which it does without a sink.
func (in *type1Interp) setWidth(wx float64) (stop bool) {
	if !in.widthFound {
		in.width, in.widthFound = wx, true
	}
	return in.sink == nil
}

// escape runs a two-byte escape operator and reports whether it ended the
// glyph.
func (in *type1Interp) escape(esc byte) (ended bool) {
	switch esc {
	case 12: // div
		if a := in.pop(2); a != nil {
			if a[1] != 0 {
				in.stack = append(in.stack, a[0]/a[1])
			}
		}
		return false
	case 6: // seac: asb adx ady bchar achar
		a := in.pop(5)
		in.stack = in.stack[:0]
		if a != nil {
			in.seac(a[0], a[1], a[2], int(a[3]), int(a[4]))
		}
		return true
	case 7: // sbw: sbx sby wx wy
		if len(in.stack) >= 4 {
			if in.setWidth(in.stack[2]) {
				return true
			}
			in.x, in.y = in.ox+in.stack[0], in.oy+in.stack[1]
		}
	case 16: // callothersubr: arg1 ... argn n othersubr#
		in.callOtherSubr()
		return false
	case 17: // pop: move a callothersubr result onto the stack
		v := 0.0
		if n := len(in.ps); n > 0 {
			v, in.ps = in.ps[n-1], in.ps[:n-1]
		}
		in.stack = append(in.stack, v)
		return false
	case 33: // setcurrentpoint: x y
		if a := in.pop(2); a != nil {
			in.x, in.y = a[0], a[1]
		}
	}
	in.stack = in.stack[:0]
	return false
}

func (in *type1Interp) moveTo(x, y float64) {
	in.x, in.y = x, y
	if in.flexing {
		in.flexPts = append(in.flexPts, [2]float64{x, y})
		return
	}
	if in.sink != nil {
		in.sink.MoveTo(x, y)
	}
}

func (in *type1Interp) lineTo(x, y float64) {
	in.x, in.y = x, y
	if in.sink != nil {
		in.sink.LineTo(x, y)
	}
}

func (in *type1Interp) curveTo(x1, y1, x2, y2, x3, y3 float64) {
	in.x, in.y = x3, y3
	if in.sink != nil {
		in.sink.CurveTo(x1, y1, x2, y2, x3, y3)
	}
}

// callOtherSubr runs the OtherSubrs that every Type 1 program defines: 1
// starts a flex, 2 marks one of its points, 0 draws the flex, and 3 (hint
// replacement) leaves its argument for pop. Any other OtherSubr leaves its
// arguments for pop.
func (in *type1Interp) callOtherSubr() {
	head := in.pop(2)
	if head == nil {
		in.stack = in.stack[:0]
		return
	}
	n, which := int(head[0]), int(head[1])
	args := in.pop(max(n, 0))
	in.stack = in.stack[:0]
	switch which {
	case 1:
		in.flexing, in.flexX, in.flexY, in.flexPts = true, in.x, in.y, nil
		return
	case 2:
		return
	case 0:
		// The first of the seven points is the flex's reference point, which
		// only matters for hinting; the other six are the two curves' control
		// and end points, drawn from where the flex started.
		if in.flexing && len(in.flexPts) == 7 {
			p := in.flexPts
			in.flexing = false
			in.x, in.y = in.flexX, in.flexY
			in.curveTo(p[1][0], p[1][1], p[2][0], p[2][1], p[3][0], p[3][1])
			in.curveTo(p[4][0], p[4][1], p[5][0], p[5][1], p[6][0], p[6][1])
		}
		in.flexing = false
		// pop pop setcurrentpoint follows, taking the end point x then y.
		if len(args) == 3 {
			in.ps = append(in.ps, args[2], args[1])
		}
		return
	}
	for j := len(args) - 1; j >= 0; j-- {
		in.ps = append(in.ps, args[j])
	}
}

// seac composes a standard accented character: the base glyph bchar at the
// origin and the accent achar offset by (adx-asb, ady), both named through
// StandardEncoding.
func (in *type1Interp) seac(asb, adx, ady float64, bchar, achar int) {
	if in.sink == nil || in.glyphs == nil || in.inSeac || bchar < 0 || bchar > 255 || achar < 0 || achar > 255 {
		return
	}
	for _, part := range []struct {
		code   int
		ox, oy float64
	}{{bchar, in.ox, in.oy}, {achar, in.ox + adx - asb, in.oy + ady}} {
		cs, ok := in.glyphs[StandardEncoding[part.code]]
		if !ok {
			continue
		}
		sub := &type1Interp{subrs: in.subrs, sink: in.sink, glyphs: in.glyphs, ox: part.ox, oy: part.oy, inSeac: true}
		sub.x, sub.y = part.ox, part.oy
		sub.run(cs, 0)
	}
}
