package fontprog

import "strconv"

// Type1Charstrings decrypts a Type1 font program once and returns every
// glyph's decrypted charstring by name, together with the decrypted Subrs the
// charstrings call into. ok is false when the program has no readable
// CharStrings.
func Type1Charstrings(fontData []byte) (glyphs map[string][]byte, subrs [][]byte, ok bool) {
	cs, lenIV, subrs := type1Sections(fontData, Type1EexecBinStart(fontData))
	if cs == nil {
		return nil, nil, false
	}
	glyphs = map[string][]byte{}
	for _, m := range Type1CharStringRe.FindAllSubmatchIndex(cs, -1) {
		n, _ := strconv.Atoi(string(cs[m[4]:m[5]]))
		if n <= 0 || m[1]+n > len(cs) {
			continue
		}
		glyphs[string(cs[m[2]:m[3]])] = DecryptType1CharString(cs[m[1]:m[1]+n], lenIV)
	}
	return glyphs, subrs, true
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
