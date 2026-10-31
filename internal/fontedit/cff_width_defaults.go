package fontedit

import (
	"encoding/binary"
	"math"

	"github.com/voidrab/gopdfrab/internal/fontprog"
)

// NormalizeCFFWidthDefaults rewrites a CFF program whose Private DICT -- or,
// in a CID-keyed program, any Font DICT's Private DICT -- gives nominalWidthX
// or defaultWidthX as a fractional real, so every one is a whole number and
// every glyph still has exactly the advance width it had.
//
// A glyph's width is nominalWidthX plus the width operand its charstring
// starts with, or defaultWidthX when it has none, both read from the Private
// DICT of the glyph's Font DICT when the program is CID-keyed. Some readers --
// veraPDF among them -- truncate a real nominalWidthX to an integer, so every
// width they read is off by its fraction, and a correct /Widths or /W fails
// 6.3.6 against them. Here each charstring's width operand is re-encoded
// against the rounded nominalWidthX (a glyph that relied on a fractional
// defaultWidthX gets an explicit width), which every reader then decodes
// alike. Outlines, hints and subroutines are untouched.
//
// The rewritten CharStrings INDEX, FDArray when CID-keyed, and Private DICTs,
// each with a copy of its local Subrs, are appended to the program and the
// Top DICT is pointed at them. The old ones are dropped when they are the
// tail of the program, as they usually are, and otherwise stay behind
// unreferenced. changed is false, and cff is returned as is, when the
// defaults are already whole, or when the program is one this cannot rewrite
// with certainty: a Font DICT with its own FontMatrix, a width pushed from
// inside a subroutine, anything it cannot parse, or a result whose widths do
// not come out the same.
func NormalizeCFFWidthDefaults(cff []byte) (out []byte, changed bool) {
	td, ok := fontprog.ParseCFFTopDict(cff)
	if !ok || td.CSOffset < 0 {
		return cff, false
	}
	charStrings, _ := fontprog.ParseCFFIndex(cff, td.CSOffset)
	if len(charStrings) == 0 {
		return cff, false
	}

	// privates[fdIndex[gid]] holds the width defaults glyph gid is read
	// against; fds is the FDArray, nil for a name-keyed program.
	var privates []cffWidthPrivate
	var fdIndex []int
	var fds [][]cffDictEntry
	if td.IsCIDKeyed {
		privates, fdIndex, fds, ok = cidWidthPrivates(cff, td, len(charStrings))
	} else {
		var p cffWidthPrivate
		p, ok = readCFFWidthPrivate(cff, td.PrivateOffset, td.PrivateSize)
		privates, fdIndex = []cffWidthPrivate{p}, make([]int, len(charStrings))
	}
	if !ok || allWhole(privates) {
		return cff, false
	}

	gsubrs := fontprog.CFFGlobalSubrs(cff)
	rewritten := make([][]byte, len(charStrings))
	for gid, cs := range charStrings {
		if rewritten[gid], ok = privates[fdIndex[gid]].rewriteWidth(cs, gsubrs); !ok {
			return cff, false
		}
	}
	blocks := make([]privateBlock, len(privates))
	for i, p := range privates {
		if blocks[i], ok = p.rebuild(cff); !ok {
			return cff, false
		}
	}

	out, ok = relayoutCFF(cff, td, rewritten, fds, blocks)
	if !ok || !sameCFFWidths(cff, out) {
		return cff, false
	}
	return out, true
}

// cffWidthPrivate is a Private DICT as far as glyph widths go: where it lies,
// its width defaults and its local Subrs.
type cffWidthPrivate struct {
	offset, size       int
	defaultW, nominalW float64
	lsubrs             [][]byte
}

func readCFFWidthPrivate(cff []byte, offset, size int) (cffWidthPrivate, bool) {
	d, n, lsubrs, ok := fontprog.CFFPrivateInfo(cff, offset, size)
	return cffWidthPrivate{offset, size, d, n, lsubrs}, ok
}

func (p cffWidthPrivate) whole() bool {
	return p.defaultW == math.Trunc(p.defaultW) && p.nominalW == math.Trunc(p.nominalW)
}

func allWhole(privates []cffWidthPrivate) bool {
	for _, p := range privates {
		if !p.whole() {
			return false
		}
	}
	return true
}

// rewriteWidth re-encodes charstring cs so it decodes to the same width
// against p's rounded defaults; cs itself when it already does.
func (p cffWidthPrivate) rewriteWidth(cs []byte, gsubrs [][]byte) ([]byte, bool) {
	w, hasWidth, ok := fontprog.Type2CharstringWidth(cs, gsubrs, p.lsubrs)
	if !ok {
		return nil, false
	}
	newNominal, newDefault := math.Round(p.nominalW), math.Round(p.defaultW)
	width, body := p.defaultW, cs
	if hasWidth {
		// The width is the bottom of the stack, so it must be the
		// charstring's own first token to be replaced in place.
		v, n, ok := fontprog.Type2Number(cs)
		if !ok || v != w {
			return nil, false
		}
		width, body = p.nominalW+w, cs[n:]
	}
	if hasWidth && p.nominalW == newNominal || !hasWidth && width == newDefault {
		return cs, true
	}
	return append(encodeType2Number(width-newNominal), body...), true
}

// privateBlock is a rebuilt Private DICT and the copy of its local Subrs
// that follows it, with the offset of the DICT it replaces.
type privateBlock struct {
	dict, subrs []byte
	oldOffset   int
}

// rebuild returns p's Private DICT with its width defaults rounded and its
// Subrs offset pointing just past the DICT, where the copy of the local Subrs
// goes.
func (p cffWidthPrivate) rebuild(cff []byte) (privateBlock, bool) {
	// readCFFWidthPrivate has parsed these very bytes; anything amiss in them
	// still has to get past sameCFFWidths.
	entries := cffDictEntries(cff[p.offset : p.offset+p.size])
	b := privateBlock{oldOffset: p.offset}
	for _, e := range entries {
		if e.op == 19 && len(e.operands) == 1 {
			start := p.offset + int(e.operands[0])
			_, end := fontprog.ParseCFFIndex(cff, start)
			if end <= start {
				return b, false
			}
			b.subrs = cff[start:end]
		}
	}
	subrsAt := 0
	for _, e := range entries {
		switch e.op {
		case 19:
			subrsAt = len(b.dict)
			b.dict = appendCFFDictEntry(b.dict, e.op, 0)
		case 20:
			b.dict = appendCFFDictEntry(b.dict, e.op, int(math.Round(p.defaultW)))
		case 21:
			b.dict = appendCFFDictEntry(b.dict, e.op, int(math.Round(p.nominalW)))
		default:
			b.dict = append(b.dict, e.raw...)
		}
	}
	if b.subrs != nil {
		// Subrs is relative to the Private DICT, and the copy follows it.
		binary.BigEndian.PutUint32(b.dict[subrsAt+1:], uint32(len(b.dict)))
	}
	return b, true
}

// cidWidthPrivates reads a CID-keyed program's FDArray: the Private DICT of
// each Font DICT, which one each glyph uses, and the Font DICTs' entries.
// It declines a Font DICT with its own FontMatrix, whose widths
// fontprog.CFFCIDAdvanceWidths does not read and so could not be checked.
func cidWidthPrivates(cff []byte, td fontprog.CFFTopDict, numGlyphs int) (privates []cffWidthPrivate, fdIndex []int, fds [][]cffDictEntry, ok bool) {
	if td.FDArrayOffset < 0 || td.PrivateOffset >= 0 {
		return nil, nil, nil, false
	}
	dicts, _ := fontprog.ParseCFFIndex(cff, td.FDArrayOffset)
	if len(dicts) == 0 {
		return nil, nil, nil, false
	}
	fdIndex = fontprog.ParseCFFFDSelect(cff, td.FDSelect, numGlyphs)
	if fdIndex == nil {
		if len(dicts) != 1 {
			return nil, nil, nil, false
		}
		fdIndex = make([]int, numGlyphs)
	}
	for _, fd := range fdIndex {
		if fd >= len(dicts) {
			return nil, nil, nil, false
		}
	}
	privates = make([]cffWidthPrivate, len(dicts))
	fds = make([][]cffDictEntry, len(dicts))
	for i, d := range dicts {
		off, size, ok := fontDictPrivate(d)
		if !ok {
			return nil, nil, nil, false
		}
		if privates[i], ok = readCFFWidthPrivate(cff, off, size); !ok {
			return nil, nil, nil, false
		}
		fds[i] = cffDictEntries(d)
	}
	return privates, fdIndex, fds, true
}

// fontDictPrivate returns the Private DICT a Font DICT points at; ok is false
// when the Font DICT does not parse or declares its own FontMatrix.
func fontDictPrivate(fd []byte) (offset, size int, ok bool) {
	offset, size = -1, -1
	matrix := false
	parsed := fontprog.CFFDictNumbers(fd, func(operator int, operands []float64) {
		switch {
		case operator == 18 && len(operands) == 2:
			size, offset = int(operands[0]), int(operands[1])
		case operator == 1207:
			matrix = true
		}
	})
	return offset, size, parsed && !matrix
}

// relayoutCFF assembles the rewritten program: everything up to the first
// block being replaced, then the new CharStrings INDEX, the new FDArray when
// fds is not nil, and each Private DICT block, with the Top DICT -- and each
// Font DICT -- pointed at them.
func relayoutCFF(cff []byte, td fontprog.CFFTopDict, charStrings [][]byte, fds [][]cffDictEntry, privates []privateBlock) ([]byte, bool) {
	hdrSize := int(cff[2])
	_, nameEnd := fontprog.ParseCFFIndex(cff, hdrSize)
	tops, topEnd := fontprog.ParseCFFIndex(cff, nameEnd)
	if len(tops) != 1 {
		return nil, false
	}
	topEntries := cffDictEntries(tops[0])

	// A charset, Encoding or FDSelect the Top DICT points at is kept where it
	// is, so it moves only with the Top DICT's change in size.
	kept := func(e cffDictEntry) bool {
		if len(e.operands) != 1 {
			return false
		}
		switch e.op {
		case 15: // charset; 0-2 name a predefined one
			return e.operands[0] > 2
		case 16: // Encoding; 0-1 name a predefined one
			return e.operands[0] > 1
		case 1237: // FDSelect
			return true
		}
		return false
	}
	// Keep everything up to the old CharStrings, FDArray and Private DICTs;
	// when a kept table lies past them, keep the whole program.
	keep := td.CSOffset
	if fds != nil {
		keep = min(keep, td.FDArrayOffset)
	}
	for _, p := range privates {
		keep = min(keep, p.oldOffset)
	}
	for _, e := range topEntries {
		if kept(e) && int(e.operands[0]) >= keep {
			keep = len(cff)
		}
	}

	// Every offset is written as a 5-byte integer, so no DICT's size depends
	// on the values; measure with zeros, then fill them in.
	fdArray := func(privAt []int) []byte {
		dicts := make([][]byte, len(fds))
		for i, entries := range fds {
			for _, e := range entries {
				if e.op == 18 {
					dicts[i] = appendCFFDictEntry(dicts[i], e.op, len(privates[i].dict), privAt[i])
				} else {
					dicts[i] = append(dicts[i], e.raw...)
				}
			}
		}
		return encodeCFFIndex(dicts)
	}
	topIndex := func(shift, csAt, fdArrayAt int, privAt []int) []byte {
		var d []byte
		for _, e := range topEntries {
			switch {
			case kept(e):
				d = appendCFFDictEntry(d, e.op, int(e.operands[0])+shift)
			case e.op == 17:
				d = appendCFFDictEntry(d, e.op, csAt)
			case e.op == 18 && fds == nil:
				d = appendCFFDictEntry(d, e.op, len(privates[0].dict), privAt[0])
			case e.op == 1236 && fds != nil:
				d = appendCFFDictEntry(d, e.op, fdArrayAt)
			default:
				d = append(d, e.raw...)
			}
		}
		return encodeCFFIndex([][]byte{d})
	}
	zeros := make([]int, len(privates))
	newCS := encodeCFFIndex(charStrings)
	shift := len(topIndex(0, 0, 0, zeros)) - (topEnd - nameEnd)
	csAt := keep + shift
	fdArrayAt := csAt + len(newCS)
	at := fdArrayAt
	if fds != nil {
		at += len(fdArray(zeros))
	}
	privAt := make([]int, len(privates))
	for i, p := range privates {
		privAt[i] = at
		at += len(p.dict) + len(p.subrs)
	}

	out := append([]byte{}, cff[:nameEnd]...)
	out = append(out, topIndex(shift, csAt, fdArrayAt, privAt)...)
	out = append(out, cff[topEnd:keep]...)
	out = append(out, newCS...)
	if fds != nil {
		out = append(out, fdArray(privAt)...)
	}
	for _, p := range privates {
		out = append(out, p.dict...)
		out = append(out, p.subrs...)
	}
	return out, true
}

// sameCFFWidths reports whether b decodes to the same glyphs with the same
// advance widths as a, and to whole width defaults in every Private DICT.
func sameCFFWidths(a, b []byte) bool {
	td, ok := fontprog.ParseCFFTopDict(b)
	if !ok {
		return false
	}
	if !td.IsCIDKeyed {
		p, ok := readCFFWidthPrivate(b, td.PrivateOffset, td.PrivateSize)
		return ok && p.whole() && sameWidths(fontprog.CFFAdvanceWidths(a), fontprog.CFFAdvanceWidths(b))
	}
	if td.FDArrayOffset < 0 {
		return false
	}
	fds, _ := fontprog.ParseCFFIndex(b, td.FDArrayOffset)
	for _, d := range fds {
		off, size, ok := fontDictPrivate(d)
		if !ok {
			return false
		}
		if p, ok := readCFFWidthPrivate(b, off, size); !ok || !p.whole() {
			return false
		}
	}
	return len(fds) > 0 && sameWidths(fontprog.CFFCIDAdvanceWidths(a), fontprog.CFFCIDAdvanceWidths(b))
}

// sameWidths reports whether before and after give the same glyphs the same
// widths, and give any at all.
func sameWidths[K comparable](before, after map[K]float64) bool {
	if len(before) == 0 || len(before) != len(after) {
		return false
	}
	for k, w := range before {
		if v, ok := after[k]; !ok || math.Abs(v-w) > 1e-3 {
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
	fontprog.CFFDictNumbersAt(dict, func(op int, operands []float64, end int) {
		entries = append(entries, cffDictEntry{op: op, operands: append([]float64(nil), operands...), raw: dict[start:end]})
		start = end
	})
	return entries
}

// appendCFFDictEntry appends an operator with whole-number operands, each as
// a 5-byte integer. An op of 1200 and up is the two-byte escape 12, op-1200,
// as fontprog.CFFDictNumbers numbers them.
func appendCFFDictEntry(d []byte, op int, operands ...int) []byte {
	for _, v := range operands {
		d = append(d, 29, 0, 0, 0, 0)
		binary.BigEndian.PutUint32(d[len(d)-4:], uint32(int32(v)))
	}
	if op >= 1200 {
		return append(d, 12, byte(op-1200))
	}
	return append(d, byte(op))
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
