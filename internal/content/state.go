package content

import "github.com/voidrab/gopdfrab/internal/pdf"

// Colour is the colour one half of the graphics state, fill or stroke, paints
// with.
type Colour struct {
	// Space is the current colour space; nil when a cs or CS operator named
	// none.
	Space pdf.PDFValue
	// RGB is the colour as red, green and blue in [0, 1].
	RGB [3]float64
	// Pattern names the pattern resource an sc or scn operator selected, or is
	// empty.
	Pattern string
}

// IsPattern reports whether the colour paints with a pattern: one was
// selected, or the colour space is a Pattern space.
func (c Colour) IsPattern() bool {
	return c.Pattern != "" || IsPatternSpace(c.Space)
}

// GState is the part of the graphics state every reader of a content stream
// follows: the current transformation matrix and the two colours.
type GState struct {
	CTM          Matrix
	Fill, Stroke Colour
}

// NewGState returns the state a content stream starts in: the identity
// matrix, and black in DeviceGray for both colours (ISO 32000-1 table 52).
func NewGState() GState {
	black := Colour{Space: pdf.PDFName{Value: "DeviceGray"}}
	return GState{CTM: IdentityMatrix, Fill: black, Stroke: black}
}

// deviceOps maps each operator that sets a colour in a device colour space to
// that space and its number of components.
var deviceOps = map[string]struct {
	space string
	n     int
}{
	"g": {"DeviceGray", 1}, "rg": {"DeviceRGB", 3}, "k": {"DeviceCMYK", 4},
	"G": {"DeviceGray", 1}, "RG": {"DeviceRGB", 3}, "K": {"DeviceCMYK", 4},
}

// Apply applies op to g if op is cm or a colour operator, and reports whether
// it is one. An operator without the operands it needs changes nothing.
// resources resolves the colour spaces operators name.
func (g *GState) Apply(op string, operands []pdf.PDFValue, resources pdf.PDFDict) bool {
	switch op {
	case "cm":
		if a, ok := numbers(operands, 6); ok {
			g.CTM = Matrix{A: a[0], B: a[1], C: a[2], D: a[3], E: a[4], F: a[5]}.Mul(g.CTM)
		}
	case "g", "rg", "k", "G", "RG", "K":
		d := deviceOps[op]
		a, ok := numbers(operands, d.n)
		if !ok {
			break
		}
		c := Colour{Space: pdf.PDFName{Value: d.space}}
		switch d.n {
		case 1:
			c.RGB = [3]float64{a[0], a[0], a[0]}
		case 3:
			c.RGB = [3]float64{a[0], a[1], a[2]}
		default:
			c.RGB[0], c.RGB[1], c.RGB[2] = pdf.CMYKToRGB(a)
		}
		*g.colour(op) = c
	case "cs", "CS":
		// The colour starts as black, whatever the space.
		*g.colour(op) = Colour{Space: ColorSpaceOperand(operands, resources)}
	case "sc", "scn", "SC", "SCN":
		c := g.colour(op)
		if len(operands) > 0 {
			if name, ok := operands[len(operands)-1].(pdf.PDFName); ok {
				c.Pattern = name.Value
				return true
			}
		}
		if comps := pdf.NumericOperands(operands); len(comps) > 0 && c.Space != nil {
			c.RGB[0], c.RGB[1], c.RGB[2] = pdf.ResolveColor(c.Space, comps, resources)
		}
	default:
		return false
	}
	return true
}

// colour returns the half of g a colour operator sets: the stroke for the
// capitalized operators, the fill for the rest.
func (g *GState) colour(op string) *Colour {
	if op[0] >= 'A' && op[0] <= 'Z' {
		return &g.Stroke
	}
	return &g.Fill
}

// numbers returns the last n operands as numbers. ok is false when there are
// fewer than n or one of them is not a number, and the operator is then
// skipped, as viewers skip it.
func numbers(operands []pdf.PDFValue, n int) (out []float64, ok bool) {
	if len(operands) < n {
		return nil, false
	}
	out = make([]float64, n)
	for i, v := range operands[len(operands)-n:] {
		if out[i], ok = pdf.PDFNumberToFloat(v); !ok {
			return nil, false
		}
	}
	return out, true
}

// ColorSpaceOperand resolves the colour space a cs or CS operator names
// against resources. The device spaces and Pattern stand for themselves; a
// name resources lacks is returned as it is, and an operand that is not a
// name is returned unresolved.
func ColorSpaceOperand(operands []pdf.PDFValue, resources pdf.PDFDict) pdf.PDFValue {
	if len(operands) == 0 {
		return nil
	}
	name, ok := operands[len(operands)-1].(pdf.PDFName)
	if !ok {
		return operands[len(operands)-1]
	}
	switch name.Value {
	case "DeviceGray", "DeviceRGB", "DeviceCMYK", "Pattern":
		return name
	}
	if cs, ok := pdf.LookupNamedColorSpace(name.Value, resources); ok {
		return cs
	}
	return name
}

// IsPatternSpace reports whether cs is a Pattern colour space, uncoloured
// ([/Pattern base]) or not.
func IsPatternSpace(cs pdf.PDFValue) bool {
	switch v := cs.(type) {
	case pdf.PDFName:
		return v.Value == "Pattern"
	case pdf.PDFArray:
		if len(v) == 0 {
			return false
		}
		name, ok := v[0].(pdf.PDFName)
		return ok && name.Value == "Pattern"
	}
	return false
}

// Tracker follows a GState through a content stream, saving it at q and
// restoring it at Q.
type Tracker struct {
	GState
	saved []GState
}

// NewTracker returns a Tracker in the state a content stream starts in.
func NewTracker() *Tracker {
	return &Tracker{GState: NewGState()}
}

// Step applies op to the tracked state if op is q, Q, cm or a colour
// operator, and reports whether it was. A Q with nothing saved is ignored.
func (t *Tracker) Step(op string, operands []pdf.PDFValue, resources pdf.PDFDict) bool {
	switch op {
	case "q":
		t.saved = append(t.saved, t.GState)
	case "Q":
		if n := len(t.saved); n > 0 {
			t.GState, t.saved = t.saved[n-1], t.saved[:n-1]
		}
	default:
		return t.Apply(op, operands, resources)
	}
	return true
}

// Depth is the number of states saved and not yet restored.
func (t *Tracker) Depth() int { return len(t.saved) }
