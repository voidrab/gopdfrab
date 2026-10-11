package fontprog

import (
	"encoding/binary"
	"math"
)

// NormalizeCFFWidthDefaults rewrites a name-keyed CFF program whose Private
// DICT gives nominalWidthX or defaultWidthX as a fractional real, so both are
// whole numbers and every glyph still has exactly the advance width it had.
//
// A glyph's width is nominalWidthX plus the width operand its charstring
// starts with, or defaultWidthX when it has none. Some readers -- veraPDF
// among them -- truncate a real nominalWidthX to an integer, so every width
// they read is off by its fraction, and a correct /Widths fails 6.3.6 against
// them. Here each charstring's width operand is re-encoded against the
// rounded nominalWidthX (a glyph that relied on a fractional defaultWidthX
// gets an explicit width), which every reader then decodes alike. Outlines,
// hints and subroutines are untouched.
//
// The rewritten CharStrings INDEX and Private DICT, with a copy of its local
// Subrs, are appended to the program and the Top DICT is pointed at them. The
// old ones are dropped when they are the tail of the program, as they usually
// are, and otherwise stay behind unreferenced. changed is false, and cff is returned as
// is, when the defaults are already whole, or when the program is one this
// cannot rewrite with certainty: CID-keyed, a width pushed from inside a
// subroutine, anything it cannot parse, or a result whose widths do not come
// out the same.
func NormalizeCFFWidthDefaults(cff []byte) (out []byte, changed bool) {
	td, ok := ParseCFFTopDict(cff)
	if !ok || td.IsCIDKeyed || td.CSOffset < 0 || td.PrivateOffset < 0 || td.PrivateSize < 0 {
		return cff, false
	}
	defaultW, nominalW, lsubrs, ok := cffPrivateInfo(cff, td.PrivateOffset, td.PrivateSize)
	if !ok || (defaultW == math.Trunc(defaultW) && nominalW == math.Trunc(nominalW)) {
		return cff, false
	}
	charStrings, _ := ParseCFFIndex(cff, td.CSOffset)
	if len(charStrings) == 0 {
		return cff, false
	}
	newNominal, newDefault := math.Round(nominalW), math.Round(defaultW)

	gsubrs := cffGlobalSubrs(cff)
	rewritten := make([][]byte, len(charStrings))
	for gid, cs := range charStrings {
		w, hasWidth, ok := type2CharstringWidth(cs, gsubrs, lsubrs)
		if !ok {
			return cff, false
		}
		width, body := defaultW, cs
		if hasWidth {
			// The width is the bottom of the stack, so it must be the
			// charstring's own first token to be replaced in place.
			v, n, ok := type2Number(cs)
			if !ok || v != w {
				return cff, false
			}
			width, body = nominalW+w, cs[n:]
		}
		if !hasWidth && width == newDefault {
			rewritten[gid] = cs
			continue
		}
		rewritten[gid] = append(encodeType2Number(width-newNominal), body...)
	}

	// cffPrivateInfo has parsed these very bytes, and ParseCFFTopDict the Top
	// DICT's; anything amiss in either still has to get past sameCFFWidths.
	privEntries := cffDictEntries(cff[td.PrivateOffset : td.PrivateOffset+td.PrivateSize])
	var localSubrs []byte
	for _, e := range privEntries {
		if e.op == 19 && len(e.operands) == 1 {
			start := td.PrivateOffset + int(e.operands[0])
			_, end := ParseCFFIndex(cff, start)
			if end <= start {
				return cff, false
			}
			localSubrs = cff[start:end]
		}
	}
	private := func() []byte {
		var d []byte
		var subrsAt int
		for _, e := range privEntries {
			switch e.op {
			case 19:
				subrsAt = len(d)
				d = appendCFFDictEntry(d, e.op, 0)
			case 20:
				d = appendCFFDictEntry(d, e.op, int(newDefault))
			case 21:
				d = appendCFFDictEntry(d, e.op, int(newNominal))
			default:
				d = append(d, e.raw...)
			}
		}
		if localSubrs != nil {
			// Subrs is relative to the Private DICT, and the copy follows it.
			binary.BigEndian.PutUint32(d[subrsAt+1:], uint32(len(d)))
		}
		return d
	}()

	hdrSize := int(cff[2])
	_, nameEnd := ParseCFFIndex(cff, hdrSize)
	tops, topEnd := ParseCFFIndex(cff, nameEnd)
	if len(tops) != 1 {
		return cff, false
	}
	topEntries := cffDictEntries(tops[0])
	// Keep everything up to the old CharStrings and Private DICT; when the
	// charset or Encoding lies past them, keep the whole program.
	keep := min(td.CSOffset, td.PrivateOffset)
	for _, e := range topEntries {
		if (e.op == 15 || e.op == 16) && len(e.operands) == 1 && e.operands[0] > 2 && int(e.operands[0]) >= keep {
			keep = len(cff)
		}
	}
	newCS := encodeCFFIndex(rewritten)
	// Every offset the Top DICT holds is written as a 5-byte integer, so its
	// size does not depend on the values; measure it, then fill them in.
	topIndex := func(shift, csAt, privAt int) []byte {
		var d []byte
		for _, e := range topEntries {
			switch {
			case e.op == 15 && len(e.operands) == 1 && e.operands[0] > 2, // charset
				e.op == 16 && len(e.operands) == 1 && e.operands[0] > 1: // Encoding
				d = appendCFFDictEntry(d, e.op, int(e.operands[0])+shift)
			case e.op == 17:
				d = appendCFFDictEntry(d, e.op, csAt)
			case e.op == 18:
				d = appendCFFDictEntry(d, e.op, len(private), privAt)
			default:
				d = append(d, e.raw...)
			}
		}
		return encodeCFFIndex([][]byte{d})
	}
	shift := len(topIndex(0, 0, 0)) - (topEnd - nameEnd)
	csAt := keep + shift
	privAt := csAt + len(newCS)

	out = append([]byte{}, cff[:nameEnd]...)
	out = append(out, topIndex(shift, csAt, privAt)...)
	out = append(out, cff[topEnd:keep]...)
	out = append(out, newCS...)
	out = append(out, private...)
	out = append(out, localSubrs...)

	if !sameCFFWidths(cff, out) {
		return cff, false
	}
	return out, true
}

// sameCFFWidths reports whether b decodes to the same glyphs with the same
// advance widths as a, and to whole width defaults.
func sameCFFWidths(a, b []byte) bool {
	td, ok := ParseCFFTopDict(b)
	if !ok {
		return false
	}
	d, n, _, ok := cffPrivateInfo(b, td.PrivateOffset, td.PrivateSize)
	if !ok || d != math.Trunc(d) || n != math.Trunc(n) {
		return false
	}
	before, after := CFFAdvanceWidths(a), CFFAdvanceWidths(b)
	if len(before) == 0 || len(before) != len(after) {
		return false
	}
	for name, w := range before {
		if v, ok := after[name]; !ok || math.Abs(v-w) > 1e-3 {
			return false
		}
	}
	return true
}

// cffDictEntry is one operator of a CFF DICT with its operands, and the bytes
// it was read from.
type cffDictEntry struct {
	op       int
	operands []float64
	raw      []byte
}

// cffDictEntries splits a DICT into its entries, as far as it parses.
func cffDictEntries(dict []byte) []cffDictEntry {
	var entries []cffDictEntry
	start := 0
	cffDictNumbersAt(dict, func(op int, operands []float64, end int) {
		entries = append(entries, cffDictEntry{op: op, operands: append([]float64(nil), operands...), raw: dict[start:end]})
		start = end
	})
	return entries
}

// appendCFFDictEntry appends a one-byte operator with whole-number operands,
// each as a 5-byte integer.
func appendCFFDictEntry(d []byte, op int, operands ...int) []byte {
	for _, v := range operands {
		d = append(d, 29, 0, 0, 0, 0)
		binary.BigEndian.PutUint32(d[len(d)-4:], uint32(int32(v)))
	}
	return append(d, byte(op))
}

// type2Number decodes the number token cs starts with, returning its value
// and length.
func type2Number(cs []byte) (v float64, n int, ok bool) {
	if len(cs) == 0 {
		return 0, 0, false
	}
	switch b := int(cs[0]); {
	case b >= 32 && b <= 246:
		return float64(b - 139), 1, true
	case b >= 247 && b <= 250 && len(cs) >= 2:
		return float64((b-247)*256 + int(cs[1]) + 108), 2, true
	case b >= 251 && b <= 254 && len(cs) >= 2:
		return float64(-(b-251)*256 - int(cs[1]) - 108), 2, true
	case b == 28 && len(cs) >= 3:
		return float64(int16(binary.BigEndian.Uint16(cs[1:3]))), 3, true
	case b == 255 && len(cs) >= 5:
		return float64(int32(binary.BigEndian.Uint32(cs[1:5]))) / 65536, 5, true
	}
	return 0, 0, false
}

// encodeType2Number encodes v as a Type 2 charstring operand: the shortest
// integer form when it is whole, 16.16 fixed otherwise.
func encodeType2Number(v float64) []byte {
	if v != math.Trunc(v) || v < -32768 || v > 32767 {
		b := []byte{255, 0, 0, 0, 0}
		binary.BigEndian.PutUint32(b[1:], uint32(int32(math.Round(v*65536))))
		return b
	}
	switch i := int(v); {
	case i >= -107 && i <= 107:
		return []byte{byte(i + 139)}
	case i >= 108 && i <= 1131:
		i -= 108
		return []byte{byte(i/256 + 247), byte(i % 256)}
	case i >= -1131 && i <= -108:
		i = -i - 108
		return []byte{byte(i/256 + 251), byte(i % 256)}
	default:
		return []byte{28, byte(uint16(int16(i)) >> 8), byte(int16(i))}
	}
}

// encodeCFFIndex encodes entries, at least one, as a CFF INDEX with the
// smallest offset size.
func encodeCFFIndex(entries [][]byte) []byte {
	total := 1
	for _, e := range entries {
		total += len(e)
	}
	offSize := 1
	for limit := 1 << 8; total >= limit && offSize < 4; limit <<= 8 {
		offSize++
	}
	out := []byte{byte(len(entries) >> 8), byte(len(entries)), byte(offSize)}
	put := func(v int) {
		for i := offSize - 1; i >= 0; i-- {
			out = append(out, byte(v>>(8*i)))
		}
	}
	off := 1
	put(off)
	for _, e := range entries {
		off += len(e)
		put(off)
	}
	for _, e := range entries {
		out = append(out, e...)
	}
	return out
}
