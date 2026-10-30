package fontprog

import (
	"encoding/binary"
	"strconv"
)

// PathSink receives the outline a charstring draws, in glyph space. CurveTo
// draws a cubic Bezier curve from the current point, which is the end of the
// previous segment or the last MoveTo.
type PathSink interface {
	MoveTo(x, y float64)
	LineTo(x, y float64)
	CurveTo(x1, y1, x2, y2, x3, y3 float64)
}

// Type2Number decodes the Type 2 charstring operand b starts with, returning
// its value and encoded length. ok is false when b does not start with a
// complete operand.
func Type2Number(b []byte) (v float64, n int, ok bool) {
	return charstringNumber(b, 65536)
}

// Type1Number decodes the Type 1 charstring operand b starts with, as
// Type2Number does. The two encodings differ only in byte 255, a 32-bit
// integer here and 16.16 fixed point in Type 2.
func Type1Number(b []byte) (v float64, n int, ok bool) {
	return charstringNumber(b, 1)
}

// charstringNumber decodes one charstring operand; div255 scales the 32-bit
// form byte 255 introduces.
func charstringNumber(b []byte, div255 float64) (float64, int, bool) {
	if len(b) == 0 {
		return 0, 0, false
	}
	switch v := int(b[0]); {
	case v >= 32 && v <= 246:
		return float64(v - 139), 1, true
	case v >= 247 && v <= 250 && len(b) >= 2:
		return float64((v-247)*256 + int(b[1]) + 108), 2, true
	case v >= 251 && v <= 254 && len(b) >= 2:
		return float64(-(v-251)*256 - int(b[1]) - 108), 2, true
	case v == 28 && len(b) >= 3:
		return float64(int16(binary.BigEndian.Uint16(b[1:3]))), 3, true
	case v == 255 && len(b) >= 5:
		return float64(int32(binary.BigEndian.Uint32(b[1:5]))) / div255, 5, true
	}
	return 0, 0, false
}

// Type1Charstrings is a Type 1 font program decrypted once: its glyphs'
// charstrings by name and the Subrs they call.
type Type1Charstrings struct {
	glyphs map[string][]byte
	subrs  [][]byte
}

// ParseType1Charstrings decrypts a Type 1 font program's charstrings. ok is
// false when the program has no readable CharStrings.
func ParseType1Charstrings(fontData []byte) (*Type1Charstrings, bool) {
	cs, lenIV, subrs := type1Sections(fontData, Type1EexecBinStart(fontData))
	if cs == nil {
		return nil, false
	}
	glyphs := map[string][]byte{}
	for _, m := range Type1CharStringRe.FindAllSubmatchIndex(cs, -1) {
		n, _ := strconv.Atoi(string(cs[m[4]:m[5]]))
		if n <= 0 || m[1]+n > len(cs) {
			continue
		}
		glyphs[string(cs[m[2]:m[3]])] = DecryptType1CharString(cs[m[1]:m[1]+n], lenIV)
	}
	return &Type1Charstrings{glyphs: glyphs, subrs: subrs}, true
}

// Outline draws the named glyph into sink and reports whether the program
// defines it.
func (p *Type1Charstrings) Outline(name string, sink PathSink) bool {
	cs, ok := p.glyphs[name]
	if !ok {
		return false
	}
	in := type1Interp{subrs: p.subrs, sink: sink, glyphs: p.glyphs}
	in.run(cs, 0)
	return true
}

// CFFCharstrings is a CFF program's charstrings and the subroutines they
// call, read once.
type CFFCharstrings struct {
	charStrings [][]byte
	subrs       CFFSubrs
}

// ParseCFFCharstrings reads a CFF program's charstrings. ok is false when it
// has no readable Top DICT or CharStrings.
func ParseCFFCharstrings(cff []byte) (*CFFCharstrings, bool) {
	td, ok := ParseCFFTopDict(cff)
	if !ok || td.CSOffset < 0 {
		return nil, false
	}
	entries, _ := ParseCFFIndex(cff, td.CSOffset)
	return &CFFCharstrings{charStrings: entries, subrs: ParseCFFSubrs(cff, td, len(entries))}, true
}

// Outline draws glyph gid into sink and reports whether the program has that
// glyph.
func (p *CFFCharstrings) Outline(gid int, sink PathSink) bool {
	if gid < 0 || gid >= len(p.charStrings) {
		return false
	}
	Type2Outline(p.charStrings[gid], p.subrs.Global, p.subrs.Local(gid), sink)
	return true
}

// CFFSubrs holds the subroutines a CFF program's Type2 charstrings call: the
// Global Subr INDEX, and the local Subr INDEX of each Private DICT -- the top
// one for a name-keyed program, one per Font DICT for a CID-keyed one.
type CFFSubrs struct {
	Global  [][]byte
	local   [][][]byte
	fdIndex []int // glyph ID -> Font DICT; nil when every glyph uses local[0]
}

// ParseCFFSubrs reads the subroutines of a CFF program whose Top DICT is td
// and which has numGlyphs charstrings. Whatever cannot be read is left empty,
// so a charstring calling into it fails its call rather than the whole font.
func ParseCFFSubrs(cff []byte, td CFFTopDict, numGlyphs int) CFFSubrs {
	s := CFFSubrs{Global: CFFGlobalSubrs(cff)}
	if !td.IsCIDKeyed {
		_, _, ls, _ := CFFPrivateInfo(cff, td.PrivateOffset, td.PrivateSize)
		s.local = [][][]byte{ls}
		return s
	}
	fds, _ := ParseCFFIndex(cff, td.FDArrayOffset)
	for _, p := range cffFontDictPrivates(cff, fds) {
		s.local = append(s.local, p.localSubrs)
	}
	s.fdIndex = ParseCFFFDSelect(cff, td.FDSelect, numGlyphs)
	return s
}

// Local returns the local Subr INDEX glyph gid's charstring calls into.
func (s CFFSubrs) Local(gid int) [][]byte {
	fd := 0
	if s.fdIndex != nil {
		if gid < 0 || gid >= len(s.fdIndex) {
			return nil
		}
		fd = s.fdIndex[gid]
	}
	if fd < 0 || fd >= len(s.local) {
		return nil
	}
	return s.local[fd]
}

// CFFSubr resolves a Type2 callsubr/callgsubr operand against subrs, applying
// the bias the INDEX's size calls for.
func CFFSubr(subrs [][]byte, operand int) ([]byte, bool) {
	idx := operand + cffSubrBias(len(subrs))
	if idx < 0 || idx >= len(subrs) {
		return nil, false
	}
	return subrs[idx], true
}

// cffPrivate is one Font DICT's Private DICT, as cffPrivateInfo reads it.
type cffPrivate struct {
	defaultWidthX, nominalWidthX float64
	localSubrs                   [][]byte
	ok                           bool
	// hasMatrix is set when the Font DICT declares its own FontMatrix.
	hasMatrix bool
}

// cffFontDictPrivates reads the Private DICT each Font DICT of a CID-keyed
// program points to.
func cffFontDictPrivates(cff []byte, fds [][]byte) []cffPrivate {
	out := make([]cffPrivate, len(fds))
	for i, fd := range fds {
		privOff, privSize := -1, -1
		hasMatrix := false
		if !CFFDictNumbers(fd, func(operator int, operands []float64) {
			switch {
			case operator == 18 && len(operands) > 1:
				privSize = int(operands[0])
				privOff = int(operands[1])
			case operator == 1207:
				hasMatrix = true
			}
		}) {
			continue
		}
		d, n, ls, ok := CFFPrivateInfo(cff, privOff, privSize)
		out[i] = cffPrivate{d, n, ls, ok, hasMatrix}
	}
	return out
}
