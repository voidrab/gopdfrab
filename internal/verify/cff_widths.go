package verify

import (
	"encoding/binary"
)

// This file reads advance widths out of CFF (Type1C / CIDFontType0C) font
// programs for the 6.3.6 width-consistency checks: a Type2 charstring
// optionally carries its width as one extra leading operand before the first
// stack-clearing operator. Only that prefix is interpreted; on any parse
// uncertainty the glyph (or the whole font) is skipped rather than reported.

// cffDictNumbers parses a CFF DICT's operands/operators, invoking op for each
// operator with the operands collected before it (escape operators are passed
// as 1200+n). Returns false on malformed data.
func cffDictNumbers(dict []byte, op func(operator int, operands []float64)) bool {
	var stack []float64
	for i := 0; i < len(dict); {
		b := int(dict[i])
		switch {
		case b >= 32 && b <= 246:
			stack = append(stack, float64(b-139))
			i++
		case b >= 247 && b <= 250:
			if i+1 >= len(dict) {
				return false
			}
			stack = append(stack, float64((b-247)*256+int(dict[i+1])+108))
			i += 2
		case b >= 251 && b <= 254:
			if i+1 >= len(dict) {
				return false
			}
			stack = append(stack, float64(-(b-251)*256-int(dict[i+1])-108))
			i += 2
		case b == 28:
			if i+2 >= len(dict) {
				return false
			}
			stack = append(stack, float64(int16(binary.BigEndian.Uint16(dict[i+1:i+3]))))
			i += 3
		case b == 29:
			if i+4 >= len(dict) {
				return false
			}
			stack = append(stack, float64(int32(binary.BigEndian.Uint32(dict[i+1:i+5]))))
			i += 5
		case b == 30: // real number, nibble-encoded
			v, n, ok := cffRealNumber(dict[i+1:])
			if !ok {
				return false
			}
			stack = append(stack, v)
			i += 1 + n
		case b == 12:
			if i+1 >= len(dict) {
				return false
			}
			op(1200+int(dict[i+1]), stack)
			stack = nil
			i += 2
		default:
			op(b, stack)
			stack = nil
			i++
		}
	}
	return true
}

// cffRealNumber decodes a nibble-encoded real, returning its value and the
// number of bytes consumed after the 0x1E marker.
func cffRealNumber(data []byte) (float64, int, bool) {
	var buf []byte
	for i := 0; i < len(data); i++ {
		for _, nib := range [2]byte{data[i] >> 4, data[i] & 0x0F} {
			switch {
			case nib <= 9:
				buf = append(buf, '0'+nib)
			case nib == 0xA:
				buf = append(buf, '.')
			case nib == 0xB:
				buf = append(buf, 'E')
			case nib == 0xC:
				buf = append(buf, 'E', '-')
			case nib == 0xE:
				buf = append(buf, '-')
			case nib == 0xF:
				return parseSimpleFloat(string(buf)), i + 1, true
			}
		}
	}
	return 0, 0, false
}

// parseSimpleFloat is a strconv-free float parse for the tiny well-formed
// strings cffRealNumber produces; malformed input yields 0.
func parseSimpleFloat(s string) float64 {
	var mantissa, frac float64
	fracDiv := 1.0
	sign := 1.0
	exp := 0
	expSign := 1
	stage := 0 // 0 = int part, 1 = fraction, 2 = exponent
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '-' && i == 0:
			sign = -1
		case c == '-' && stage == 2:
			expSign = -1
		case c == '.':
			stage = 1
		case c == 'E':
			stage = 2
		case c >= '0' && c <= '9':
			switch stage {
			case 0:
				mantissa = mantissa*10 + float64(c-'0')
			case 1:
				fracDiv *= 10
				frac += float64(c-'0') / fracDiv
			case 2:
				exp = exp*10 + int(c-'0')
			}
		}
	}
	v := sign * (mantissa + frac)
	for range exp {
		if expSign > 0 {
			v *= 10
		} else {
			v /= 10
		}
	}
	return v
}

// cffPrivateInfo reads a Private DICT's width defaults and local Subrs INDEX.
func cffPrivateInfo(cff []byte, offset, size int) (defaultWidthX, nominalWidthX float64, localSubrs [][]byte, ok bool) {
	if offset < 0 || size <= 0 || offset+size > len(cff) {
		return 0, 0, nil, false
	}
	subrsOffset := -1
	parsed := cffDictNumbers(cff[offset:offset+size], func(operator int, operands []float64) {
		if len(operands) == 0 {
			return
		}
		switch operator {
		case 20:
			defaultWidthX = operands[0]
		case 21:
			nominalWidthX = operands[0]
		case 19:
			subrsOffset = int(operands[0])
		}
	})
	if !parsed {
		return 0, 0, nil, false
	}
	if subrsOffset >= 0 {
		localSubrs, _ = ParseCFFIndex(cff, offset+subrsOffset)
	}
	return defaultWidthX, nominalWidthX, localSubrs, true
}

// cffGlobalSubrs returns the Global Subr INDEX entries, which follow the
// String INDEX in every CFF program.
func cffGlobalSubrs(cff []byte) [][]byte {
	if len(cff) < 4 {
		return nil
	}
	_, off := ParseCFFIndex(cff, int(cff[2])) // Name INDEX
	_, off = ParseCFFIndex(cff, off)          // Top DICT INDEX
	_, off = ParseCFFIndex(cff, off)          // String INDEX
	subrs, _ := ParseCFFIndex(cff, off)
	return subrs
}

// cffSubrBias is the index bias Type2 charstrings apply to subr call operands.
func cffSubrBias(count int) int {
	switch {
	case count < 1240:
		return 107
	case count < 33900:
		return 1131
	default:
		return 32768
	}
}

// type2CharstringWidth interprets a Type2 charstring only up to its first
// stack-clearing operator and reports the width operand, if present, as
// (width, true, true). ok is false when the prefix cannot be followed safely.
func type2CharstringWidth(cs []byte, gsubrs, lsubrs [][]byte) (width float64, hasWidth, ok bool) {
	var stack []float64
	gBias, lBias := cffSubrBias(len(gsubrs)), cffSubrBias(len(lsubrs))

	// run walks the charstring, following subr calls, and returns the first
	// stack-clearing operator (-2 when the string ends or returns first).
	var run func(cs []byte, depth int) (term int, ok bool)
	run = func(cs []byte, depth int) (int, bool) {
		if depth > 10 {
			return -1, false
		}
		for i := 0; i < len(cs); {
			b := int(cs[i])
			switch {
			case b >= 32 && b <= 246:
				stack = append(stack, float64(b-139))
				i++
			case b >= 247 && b <= 250:
				if i+1 >= len(cs) {
					return -1, false
				}
				stack = append(stack, float64((b-247)*256+int(cs[i+1])+108))
				i += 2
			case b >= 251 && b <= 254:
				if i+1 >= len(cs) {
					return -1, false
				}
				stack = append(stack, float64(-(b-251)*256-int(cs[i+1])-108))
				i += 2
			case b == 28:
				if i+2 >= len(cs) {
					return -1, false
				}
				stack = append(stack, float64(int16(binary.BigEndian.Uint16(cs[i+1:i+3]))))
				i += 3
			case b == 255: // 16.16 fixed
				if i+4 >= len(cs) {
					return -1, false
				}
				stack = append(stack, float64(int32(binary.BigEndian.Uint32(cs[i+1:i+5])))/65536)
				i += 5
			case b == 1 || b == 3 || b == 18 || b == 23 || b == 19 || b == 20 ||
				b == 21 || b == 4 || b == 22 || b == 14:
				return b, true
			case b == 10 || b == 29: // callsubr / callgsubr
				if len(stack) == 0 {
					return -1, false
				}
				idx := int(stack[len(stack)-1])
				stack = stack[:len(stack)-1]
				subrs, bias := lsubrs, lBias
				if b == 29 {
					subrs, bias = gsubrs, gBias
				}
				idx += bias
				if idx < 0 || idx >= len(subrs) {
					return -1, false
				}
				term, ok := run(subrs[idx], depth+1)
				if !ok || term != -2 {
					return term, ok
				}
				i++
			case b == 11: // return from subr
				return -2, true
			default:
				// Any other operator before the first stack-clearing one is
				// out of the width prefix's grammar; give up.
				return -1, false
			}
		}
		return -2, true
	}

	term, runOK := run(cs, 0)
	if !runOK || term < 0 {
		return 0, false, false
	}

	// The width operand is stack[0] when the count exceeds the terminator's
	// arity: stem/hintmask operators take an even count, moveto forms 1-2,
	// endchar 0 (or the deprecated 4-argument seac form).
	widthFor := func(extra bool) (float64, bool, bool) {
		if !extra {
			return 0, false, true
		}
		if len(stack) == 0 {
			return 0, false, false
		}
		return stack[0], true, true
	}
	switch term {
	case 1, 3, 18, 23, 19, 20:
		return widthFor(len(stack)%2 == 1)
	case 21:
		return widthFor(len(stack) > 2)
	case 4, 22:
		return widthFor(len(stack) > 1)
	case 14:
		if len(stack) == 1 || len(stack) == 5 {
			return stack[0], true, true
		}
		if len(stack) == 0 || len(stack) == 4 {
			return 0, false, true
		}
		return 0, false, false
	}
	return 0, false, false
}

// cffCharstringWidth extracts one glyph's advance width given the width
// defaults and subrs in effect for it, or -1 when it cannot be followed.
func cffCharstringWidth(cs []byte, gsubrs, lsubrs [][]byte, defaultWidthX, nominalWidthX float64) int {
	w, hasWidth, ok := type2CharstringWidth(cs, gsubrs, lsubrs)
	if !ok {
		return -1
	}
	if hasWidth {
		return int(nominalWidthX + w + 0.5)
	}
	return int(defaultWidthX + 0.5)
}

// CFFAdvanceWidths returns glyph-name -> advance width (in 1/1000 em) for a
// name-keyed CFF program, or nil when the program is CID-keyed, declares a
// non-default FontMatrix, or cannot be parsed. Unparseable glyphs are omitted.
func CFFAdvanceWidths(cff []byte) map[string]int {
	td, ok := ParseCFFTopDict(cff)
	if !ok || td.IsCIDKeyed || td.HasFontMatrix || td.CSOffset < 0 || td.PrivateOffset < 0 {
		return nil
	}
	names := CFFGlyphNames(cff)
	if names == nil {
		return nil
	}
	charStrings, _ := ParseCFFIndex(cff, td.CSOffset)
	// names is indexed by gid, including .notdef at gid 0.
	if len(charStrings) == 0 || len(names) != len(charStrings) {
		return nil
	}
	defaultWidthX, nominalWidthX, lsubrs, ok := cffPrivateInfo(cff, td.PrivateOffset, td.PrivateSize)
	if !ok {
		return nil
	}
	gsubrs := cffGlobalSubrs(cff)

	widths := make(map[string]int, len(names))
	for gid := 1; gid < len(charStrings); gid++ {
		if w := cffCharstringWidth(charStrings[gid], gsubrs, lsubrs, defaultWidthX, nominalWidthX); w >= 0 {
			widths[names[gid]] = w
		}
	}
	return widths
}

// CFFCIDAdvanceWidths returns CID -> advance width (in 1/1000 em) for a
// CID-keyed CFF program, or nil when it cannot be parsed safely.
func CFFCIDAdvanceWidths(cff []byte) map[int]int {
	td, ok := ParseCFFTopDict(cff)
	if !ok || !td.IsCIDKeyed || td.HasFontMatrix || td.CSOffset < 0 || td.FDArrayOffset < 0 {
		return nil
	}
	charStrings, _ := ParseCFFIndex(cff, td.CSOffset)
	if len(charStrings) == 0 {
		return nil
	}
	cids := ParseCFFCharsetCIDs(cff, td.CharsetOffset, len(charStrings))
	if cids == nil {
		return nil
	}
	fdIndex := parseCFFFDSelect(cff, td.FDSelect, len(charStrings))
	fds, _ := ParseCFFIndex(cff, td.FDArrayOffset)
	if len(fds) == 0 {
		return nil
	}
	if fdIndex == nil {
		if len(fds) != 1 {
			return nil
		}
		fdIndex = make([]int, len(charStrings))
	}

	type fdInfo struct {
		defaultWidthX, nominalWidthX float64
		lsubrs                       [][]byte
		ok                           bool
	}
	infos := make([]fdInfo, len(fds))
	for i, fd := range fds {
		var privOff, privSize int
		privOff, privSize = -1, -1
		hasMatrix := false
		if !cffDictNumbers(fd, func(operator int, operands []float64) {
			switch {
			case operator == 18 && len(operands) > 1:
				privSize = int(operands[0])
				privOff = int(operands[1])
			case operator == 1207:
				hasMatrix = true
			}
		}) || hasMatrix {
			continue
		}
		d, n, ls, ok := cffPrivateInfo(cff, privOff, privSize)
		infos[i] = fdInfo{d, n, ls, ok}
	}
	gsubrs := cffGlobalSubrs(cff)

	widths := make(map[int]int, len(charStrings))
	for gid := 0; gid < len(charStrings); gid++ {
		fd := fdIndex[gid]
		if fd < 0 || fd >= len(infos) || !infos[fd].ok {
			continue
		}
		info := infos[fd]
		if w := cffCharstringWidth(charStrings[gid], gsubrs, info.lsubrs, info.defaultWidthX, info.nominalWidthX); w >= 0 {
			widths[cids[gid]] = w
		}
	}
	return widths
}

// parseCFFFDSelect maps each glyph ID to its Font DICT index (formats 0 and
// 3); nil on absence or parse failure.
func parseCFFFDSelect(cff []byte, offset, numGlyphs int) []int {
	if offset < 0 || offset >= len(cff) || numGlyphs <= 0 {
		return nil
	}
	out := make([]int, numGlyphs)
	switch cff[offset] {
	case 0:
		if offset+1+numGlyphs > len(cff) {
			return nil
		}
		for i := range numGlyphs {
			out[i] = int(cff[offset+1+i])
		}
	case 3:
		if offset+5 > len(cff) {
			return nil
		}
		nRanges := int(binary.BigEndian.Uint16(cff[offset+1 : offset+3]))
		pos := offset + 3
		if pos+nRanges*3+2 > len(cff) {
			return nil
		}
		sentinel := int(binary.BigEndian.Uint16(cff[pos+nRanges*3 : pos+nRanges*3+2]))
		if sentinel != numGlyphs {
			return nil
		}
		for r := range nRanges {
			first := int(binary.BigEndian.Uint16(cff[pos+r*3 : pos+r*3+2]))
			fd := int(cff[pos+r*3+2])
			next := sentinel
			if r+1 < nRanges {
				next = int(binary.BigEndian.Uint16(cff[pos+(r+1)*3 : pos+(r+1)*3+2]))
			}
			if first < 0 || next > numGlyphs || first > next {
				return nil
			}
			for g := first; g < next; g++ {
				out[g] = fd
			}
		}
	default:
		return nil
	}
	return out
}
