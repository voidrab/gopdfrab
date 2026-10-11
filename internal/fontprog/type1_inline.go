package fontprog

import (
	"bytes"
	"strconv"
)

// InlineType1WidthSubrs rewrites a Type 1 (FontFile) program so that every
// glyph whose hsbw or sbw is reached only through callsubr carries that
// command itself: the called subr's body, less its return, replaces the
// "idx callsubr" that invoked it. Execution is unchanged -- a Type 1 subr runs
// on the caller's operand stack, in place -- but a reader that reads a glyph's
// width without following callsubr, as veraPDF does for 6.3.5/6.3.6, now finds
// it.
//
// length1 and length2 are the FontFile stream's /Length1 and /Length2: the
// clear-text part and the binary eexec part. The result keeps the clear text
// and anything after the eexec part byte for byte; newLength2 is the eexec
// part's new length. changed is false, and font is returned as is, when no
// glyph needed rewriting or the program is not one this can rewrite (hex
// eexec, inconsistent lengths, no CharStrings).
func InlineType1WidthSubrs(font []byte, length1, length2 int) (out []byte, newLength2 int, changed bool) {
	binStart, binEnd := length1, length1+length2
	if binStart <= 0 || length2 < 4 || binEnd > len(font) || isHexEexec(font[binStart:]) {
		return font, length2, false
	}
	plain := decryptType1(font[binStart:binEnd], 55665, 0)
	csIdx := bytes.Index(plain, []byte("/CharStrings"))
	if csIdx < 0 {
		return font, length2, false
	}
	lenIV := type1LenIV(plain[:csIdx])
	subrs := type1SubrArray(plain[:csIdx], lenIV)

	var rebuilt []byte
	copied := 0 // plain[:copied] is already in rebuilt
	for _, m := range Type1CharStringRe.FindAllSubmatchIndex(plain[csIdx:], -1) {
		for k := range m {
			m[k] += csIdx
		}
		n, err := strconv.Atoi(string(plain[m[4]:m[5]]))
		if err != nil || m[1]+n > len(plain) {
			continue
		}
		cs := decryptType1(plain[m[1]:m[1]+n], 4330, 0)
		if len(cs) < lenIV {
			continue
		}
		body, ok := inlineWidthSubr(cs[lenIV:], subrs)
		if !ok {
			continue
		}
		enc := encryptType1(append(cs[:lenIV:lenIV], body...), 4330)
		rebuilt = append(rebuilt, plain[copied:m[4]]...)
		rebuilt = strconv.AppendInt(rebuilt, int64(len(enc)), 10)
		rebuilt = append(rebuilt, plain[m[5]:m[1]]...)
		rebuilt = append(rebuilt, enc...)
		copied = m[1] + n
	}
	if rebuilt == nil {
		return font, length2, false
	}
	rebuilt = append(rebuilt, plain[copied:]...)

	eexec := encryptType1(rebuilt, 55665)
	out = make([]byte, 0, len(font)-length2+len(eexec))
	out = append(append(append(out, font[:binStart]...), eexec...), font[binEnd:]...)
	return out, len(eexec), true
}

// isHexEexec reports whether an eexec section is in the hexadecimal form,
// which the Type 1 spec signals by its first four bytes all being hex digits.
func isHexEexec(b []byte) bool {
	for _, c := range b[:4] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// inlineWidthSubr returns cs with its leading callsubrs inlined until hsbw or
// sbw is the first command, and false when that never happens: the first
// command is already a width command, the chain leads elsewhere, or a subr is
// missing or nested too deeply.
func inlineWidthSubr(cs []byte, subrs [][]byte) ([]byte, bool) {
	for depth := 0; depth <= type1SubrDepth; depth++ {
		i, last := 0, -1 // last: start of the final operand before the command
		for i < len(cs) && (cs[i] >= 32 || cs[i] == 28) {
			size := type1NumberSize(cs[i])
			if i+size > len(cs) {
				return nil, false
			}
			last = i
			i += size
		}
		if i == len(cs) {
			return nil, false
		}
		switch cs[i] {
		case 13, 12: // hsbw, or an escape (sbw is 12 7)
			return cs, depth > 0 && (cs[i] == 13 || i+1 < len(cs) && cs[i+1] == 7)
		case 10: // callsubr
		default:
			return nil, false
		}
		if last < 0 {
			return nil, false
		}
		idx := type1NumberValue(cs[last:i])
		if idx < 0 || idx >= len(subrs) || subrs[idx] == nil {
			return nil, false
		}
		sub := bytes.TrimSuffix(subrs[idx], []byte{11})
		cs = append(append(append(make([]byte, 0, len(cs)+len(sub)), cs[:last]...), sub...), cs[i+1:]...)
	}
	return nil, false
}

// type1NumberSize is the encoded length of the charstring number whose first
// byte is b (b >= 32, or 28).
func type1NumberSize(b byte) int {
	switch {
	case b == 28:
		return 3
	case b <= 246:
		return 1
	case b <= 254:
		return 2
	}
	return 5
}

// type1NumberValue decodes one charstring number, its bytes exactly.
func type1NumberValue(n []byte) int {
	switch b := n[0]; {
	case b == 28:
		return int(int16(uint16(n[1])<<8 | uint16(n[2])))
	case b <= 246:
		return int(b) - 139
	case b <= 250:
		return (int(b)-247)*256 + int(n[1]) + 108
	case b <= 254:
		return -((int(b)-251)*256 + int(n[1]) + 108)
	}
	return int(int32(uint32(n[1])<<24 | uint32(n[2])<<16 | uint32(n[3])<<8 | uint32(n[4])))
}

// encryptType1 is the eexec / charstring cipher's encryption direction; the
// plaintext's leading random bytes are encrypted like any other.
func encryptType1(plain []byte, key uint16) []byte {
	R := key
	out := make([]byte, len(plain))
	for i, p := range plain {
		c := p ^ byte(R>>8)
		R = (uint16(c)+R)*52845 + 22719
		out[i] = c
	}
	return out
}
