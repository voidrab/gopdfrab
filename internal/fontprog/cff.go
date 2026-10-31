package fontprog

import (
	"encoding/binary"
	"math"
)

// This file reads advance widths out of CFF (Type1C / CIDFontType0C) font
// programs for the 6.3.6 width-consistency checks: a Type2 charstring
// optionally carries its width as one extra leading operand before the first
// stack-clearing operator. Only that prefix is interpreted; on any parse
// uncertainty the glyph (or the whole font) is skipped rather than reported.

// CFFDictNumbers parses a CFF DICT's operands/operators, invoking op for each
// operator with the operands collected before it (escape operators are passed
// as 1200+n). Returns false on malformed data.
func CFFDictNumbers(dict []byte, op func(operator int, operands []float64)) bool {
	return CFFDictNumbersAt(dict, func(operator int, operands []float64, _ int) { op(operator, operands) })
}

// CFFDictNumbersAt is CFFDictNumbers that also passes, with each operator,
// the offset just past it -- where the next entry starts.
func CFFDictNumbersAt(dict []byte, op func(operator int, operands []float64, end int)) bool {
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
			op(1200+int(dict[i+1]), stack, i+2)
			stack = nil
			i += 2
		default:
			op(b, stack, i+1)
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

// CFFPrivateInfo reads a Private DICT's width defaults and local Subrs INDEX.
func CFFPrivateInfo(cff []byte, offset, size int) (defaultWidthX, nominalWidthX float64, localSubrs [][]byte, ok bool) {
	if offset < 0 || size <= 0 || offset+size > len(cff) {
		return 0, 0, nil, false
	}
	subrsOffset := -1
	parsed := CFFDictNumbers(cff[offset:offset+size], func(operator int, operands []float64) {
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

// CFFGlobalSubrs returns the Global Subr INDEX entries, which follow the
// String INDEX in every CFF program.
func CFFGlobalSubrs(cff []byte) [][]byte {
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

// cffCharstringWidth extracts one glyph's advance width given the width
// defaults and subrs in effect for it, or -1 when it cannot be followed.
func cffCharstringWidth(cs []byte, gsubrs, lsubrs [][]byte, defaultWidthX, nominalWidthX float64) float64 {
	w, hasWidth, ok := Type2CharstringWidth(cs, gsubrs, lsubrs)
	if !ok {
		return -1
	}
	if hasWidth {
		return nominalWidthX + w
	}
	return defaultWidthX
}

// WidthSkip names why an advance-width extraction gave up. The width paths bail
// conservatively on anything they cannot follow exactly, which silently gives up
// 6.3.6 coverage; naming the reason lets a test measure how much (see
// TestWidthSkipCorpusBudget) instead of guessing.
type WidthSkip string

const (
	WidthSkipNone        WidthSkip = ""              // widths were extracted
	WidthSkipTopDict     WidthSkip = "top-dict"      // the CFF Top DICT would not parse
	WidthSkipCIDKeyed    WidthSkip = "cid-keyed"     // name-keyed path, CID-keyed program
	WidthSkipNotCIDKeyed WidthSkip = "not-cid-keyed" // CID path, name-keyed program
	WidthSkipFontMatrix  WidthSkip = "font-matrix"   // glyph space is not the default 1/1000 em
	WidthSkipCharStrings WidthSkip = "charstrings"   // CharStrings INDEX missing or unusable
	WidthSkipGlyphNames  WidthSkip = "glyph-names"   // charset gives no usable glyph names
	WidthSkipPrivate     WidthSkip = "private-dict"  // Private DICT missing or unreadable
	WidthSkipCharset     WidthSkip = "charset"       // CID charset would not parse
	WidthSkipFDArray     WidthSkip = "fd-array"      // FDArray missing or empty
	WidthSkipFDSelect    WidthSkip = "fd-select"     // no FDSelect and more than one Font DICT
	WidthSkipEncoding    WidthSkip = "encoding"      // Type1 encoding this package does not model
	WidthSkipNoWidths    WidthSkip = "no-widths"     // program parsed but yielded no widths
)

// WidthStats reports what an advance-width extraction covered. Skip is set when
// the whole program was given up on, in which case the width map is empty;
// GlyphsSkipped counts glyphs inside an otherwise-usable program whose
// charstring could not be followed.
type WidthStats struct {
	Skip          WidthSkip
	GlyphsTotal   int
	GlyphsSkipped int
}

// fontMatrixWidthScale returns the factor that takes an advance width out of
// the glyph space a FontMatrix declares and into the 1/1000 em text space
// /Widths uses. Only a and b matter: an advance is a pure x displacement, so
// shear and translation leave it where it is, but a nonzero b would tilt it off
// the horizontal and there would be no single width left to compare. ok is
// false for a matrix that is not a plain horizontal scale.
func fontMatrixWidthScale(a, b float64) (scale float64, ok bool) {
	if b != 0 || !(a > 0) || math.IsInf(a, 0) { // !(a > 0) also rejects NaN
		return 0, false
	}
	return a * 1000, true
}

// cffWidthScale is fontMatrixWidthScale for a parsed Top DICT, defaulting to
// the 1/1000 em glyph space a program that declares no FontMatrix is in.
func cffWidthScale(td CFFTopDict) (scale float64, ok bool) {
	if !td.HasFontMatrix {
		return 1, true
	}
	return fontMatrixWidthScale(td.FontMatrix[0], td.FontMatrix[1])
}

// scaleWidth applies a fontMatrixWidthScale factor. The result stays a real
// number: /Widths holds integers, but a program's advance width need not be
// one, and rounding here would widen the comparison's tolerance by half a unit.
func scaleWidth(w, scale float64) float64 {
	if scale == 1 {
		return w
	}
	return w * scale
}

// CFFAdvanceWidths returns glyph-name -> advance width (in 1/1000 em) for a
// name-keyed CFF program, or nil when the program is CID-keyed or cannot be
// parsed. A declared FontMatrix is folded into the widths. Unparseable glyphs
// are omitted.
func CFFAdvanceWidths(cff []byte) map[string]float64 {
	w, _ := CFFAdvanceWidthsStats(cff)
	return w
}

// CFFAdvanceWidthsStats is CFFAdvanceWidths reporting why it gave up.
func CFFAdvanceWidthsStats(cff []byte) (map[string]float64, WidthStats) {
	td, tdOK := ParseCFFTopDict(cff)
	scale, scaleOK := cffWidthScale(td)
	switch {
	case !tdOK:
		return nil, WidthStats{Skip: WidthSkipTopDict}
	case td.IsCIDKeyed:
		return nil, WidthStats{Skip: WidthSkipCIDKeyed}
	case !scaleOK:
		return nil, WidthStats{Skip: WidthSkipFontMatrix}
	case td.CSOffset < 0:
		return nil, WidthStats{Skip: WidthSkipCharStrings}
	case td.PrivateOffset < 0:
		return nil, WidthStats{Skip: WidthSkipPrivate}
	}
	names := CFFGlyphNames(cff)
	if names == nil {
		return nil, WidthStats{Skip: WidthSkipGlyphNames}
	}
	charStrings, _ := ParseCFFIndex(cff, td.CSOffset)
	// names is indexed by gid, including .notdef at gid 0.
	if len(charStrings) == 0 {
		return nil, WidthStats{Skip: WidthSkipCharStrings}
	}
	if len(names) != len(charStrings) {
		return nil, WidthStats{Skip: WidthSkipGlyphNames}
	}
	defaultWidthX, nominalWidthX, lsubrs, ok := CFFPrivateInfo(cff, td.PrivateOffset, td.PrivateSize)
	if !ok {
		return nil, WidthStats{Skip: WidthSkipPrivate}
	}
	gsubrs := CFFGlobalSubrs(cff)

	stats := WidthStats{GlyphsTotal: len(charStrings) - 1}
	widths := make(map[string]float64, len(names))
	for gid := 1; gid < len(charStrings); gid++ {
		if w := cffCharstringWidth(charStrings[gid], gsubrs, lsubrs, defaultWidthX, nominalWidthX); w >= 0 {
			widths[names[gid]] = scaleWidth(w, scale)
		} else {
			stats.GlyphsSkipped++
		}
	}
	return widths, stats
}

// CFFCIDAdvanceWidths returns CID -> advance width (in 1/1000 em) for a
// CID-keyed CFF program, or nil when it cannot be parsed safely. A Top DICT
// FontMatrix is folded into the widths; a Font DICT that declares one of its
// own still has its glyphs skipped.
func CFFCIDAdvanceWidths(cff []byte) map[int]float64 {
	w, _ := CFFCIDAdvanceWidthsStats(cff)
	return w
}

// CFFCIDAdvanceWidthsStats is CFFCIDAdvanceWidths reporting why it gave up.
func CFFCIDAdvanceWidthsStats(cff []byte) (map[int]float64, WidthStats) {
	td, tdOK := ParseCFFTopDict(cff)
	scale, scaleOK := cffWidthScale(td)
	switch {
	case !tdOK:
		return nil, WidthStats{Skip: WidthSkipTopDict}
	case !td.IsCIDKeyed:
		return nil, WidthStats{Skip: WidthSkipNotCIDKeyed}
	case !scaleOK:
		return nil, WidthStats{Skip: WidthSkipFontMatrix}
	case td.CSOffset < 0:
		return nil, WidthStats{Skip: WidthSkipCharStrings}
	case td.FDArrayOffset < 0:
		return nil, WidthStats{Skip: WidthSkipFDArray}
	}
	charStrings, _ := ParseCFFIndex(cff, td.CSOffset)
	if len(charStrings) == 0 {
		return nil, WidthStats{Skip: WidthSkipCharStrings}
	}
	cids := ParseCFFCharsetCIDs(cff, td.CharsetOffset, len(charStrings))
	if cids == nil {
		return nil, WidthStats{Skip: WidthSkipCharset}
	}
	fdIndex := ParseCFFFDSelect(cff, td.FDSelect, len(charStrings))
	fds, _ := ParseCFFIndex(cff, td.FDArrayOffset)
	if len(fds) == 0 {
		return nil, WidthStats{Skip: WidthSkipFDArray}
	}
	if fdIndex == nil {
		if len(fds) != 1 {
			return nil, WidthStats{Skip: WidthSkipFDSelect}
		}
		fdIndex = make([]int, len(charStrings))
	}

	// A Font DICT with its own FontMatrix scales its glyphs' widths by a
	// matrix this path does not apply, so its glyphs are skipped.
	infos := cffFontDictPrivates(cff, fds)
	for i := range infos {
		infos[i].ok = infos[i].ok && !infos[i].hasMatrix
	}
	gsubrs := CFFGlobalSubrs(cff)

	stats := WidthStats{GlyphsTotal: len(charStrings)}
	widths := make(map[int]float64, len(charStrings))
	for gid := 0; gid < len(charStrings); gid++ {
		fd := fdIndex[gid]
		if fd < 0 || fd >= len(infos) || !infos[fd].ok {
			stats.GlyphsSkipped++ // this glyph's Font DICT was unusable
			continue
		}
		info := infos[fd]
		if w := cffCharstringWidth(charStrings[gid], gsubrs, info.localSubrs, info.defaultWidthX, info.nominalWidthX); w >= 0 {
			widths[cids[gid]] = scaleWidth(w, scale)
		} else {
			stats.GlyphsSkipped++
		}
	}
	return widths, stats
}

// ParseCFFFDSelect maps each glyph ID to its Font DICT index (formats 0 and
// 3); nil on absence or parse failure.
func ParseCFFFDSelect(cff []byte, offset, numGlyphs int) []int {
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

// CFFTopDict holds the Top DICT operands relevant to CID-keyed subset
// validation (6.3.5).
type CFFTopDict struct {
	CSOffset      int // CharStrings INDEX offset, -1 if not found
	CharsetOffset int // Charset table offset, -1 if not found / predefined
	PrivateOffset int // Private DICT offset, -1 if not found
	PrivateSize   int
	FDArrayOffset int        // CID-keyed only, -1 if not found
	FDSelect      int        // CID-keyed only, -1 if not found
	FontMatrix    [6]float64 // as declared; only meaningful when HasFontMatrix
	HasFontMatrix bool
	IsCIDKeyed    bool
}

// ParseCFFTopDict parses a CFF binary stream's Top DICT and returns the
// operands needed to validate CID coverage.
func ParseCFFTopDict(cff []byte) (td CFFTopDict, ok bool) {
	td.CSOffset = -1
	td.CharsetOffset = -1
	td.PrivateOffset = -1
	td.PrivateSize = -1
	td.FDArrayOffset = -1
	td.FDSelect = -1
	if len(cff) < 4 {
		return td, false
	}
	hdrSize := int(cff[2])

	// Parse INDEX: returns byte length of the INDEX block and entry count.
	parseIndex := func(off int) (end int, count int) {
		if off+2 > len(cff) {
			return off, 0
		}
		n := int(binary.BigEndian.Uint16(cff[off : off+2]))
		if n == 0 {
			return off + 2, 0
		}
		if off+3 > len(cff) {
			return off, 0
		}
		os := int(cff[off+2]) // offset size: 1, 2, 3, or 4
		if os < 1 || os > 4 || off+3+(n+1)*os > len(cff) {
			return off, 0
		}
		lastOffBytes := cff[off+3+n*os : off+3+(n+1)*os]
		lastOff := 0
		for _, b := range lastOffBytes {
			lastOff = lastOff<<8 | int(b)
		}
		return off + 3 + (n+1)*os + lastOff - 1, n
	}

	// Skip Name INDEX.
	off, _ := parseIndex(hdrSize)
	if off == hdrSize {
		return td, false
	}

	// Scan Top DICT INDEX data for the operators we need.
	if off+2 > len(cff) {
		return td, false
	}
	tdCount := int(binary.BigEndian.Uint16(cff[off : off+2]))
	if tdCount == 0 {
		return td, false
	}
	os := int(cff[off+2])
	if os < 1 || os > 4 || off+3+(tdCount+1)*os > len(cff) {
		return td, false
	}
	tdDataStart := off + 3 + (tdCount+1)*os
	endOffBytes := cff[off+3+tdCount*os : off+3+(tdCount+1)*os]
	tdDataLen := 0
	for _, b := range endOffBytes {
		tdDataLen = tdDataLen<<8 | int(b)
	}
	tdDataLen-- // offsets are 1-based
	if tdDataStart+tdDataLen > len(cff) {
		return td, false
	}
	topDict := cff[tdDataStart : tdDataStart+tdDataLen]

	// Read the operators we need. CFFDictNumbers decodes nibble reals, which
	// is how FontMatrix is always written.
	return td, CFFDictNumbers(topDict, func(operator int, operands []float64) {
		first := func() (int, bool) {
			if len(operands) == 0 {
				return 0, false
			}
			return int(operands[0]), true
		}
		switch operator {
		case 17: // CharStrings
			if v, o := first(); o {
				td.CSOffset = v
			}
		case 15: // charset
			if v, o := first(); o {
				td.CharsetOffset = v
			}
		case 18: // Private: [size offset]
			if len(operands) > 1 {
				td.PrivateSize = int(operands[0])
				td.PrivateOffset = int(operands[1])
			}
		case 1207: // FontMatrix: glyph space is not the default 1/1000 em
			td.HasFontMatrix = true
			copy(td.FontMatrix[:], operands)
		case 1230: // ROS: marks a CID-keyed font
			td.IsCIDKeyed = true
		case 1236: // FDArray
			if v, o := first(); o {
				td.FDArrayOffset = v
			}
		case 1237: // FDSelect
			if v, o := first(); o {
				td.FDSelect = v
			}
		}
	})
}

// ParseCFFCharsetCIDs parses a CFF Charset table (CID-keyed fonts store CIDs
// here instead of SIDs) and returns the CID for each glyph ID. Returns nil
// for predefined charsets (offsets 0-2) or on parse failure.
func ParseCFFCharsetCIDs(cff []byte, CharsetOffset, numGlyphs int) []int {
	if CharsetOffset <= 2 || CharsetOffset >= len(cff) || numGlyphs <= 0 {
		return nil
	}
	format := cff[CharsetOffset]
	cids := make([]int, numGlyphs)
	off := CharsetOffset + 1
	gid := 1
	switch format {
	case 0:
		for gid < numGlyphs {
			if off+2 > len(cff) {
				return nil
			}
			cids[gid] = int(binary.BigEndian.Uint16(cff[off : off+2]))
			off += 2
			gid++
		}
	case 1, 2:
		for gid < numGlyphs {
			if off+2 > len(cff) {
				return nil
			}
			first := int(binary.BigEndian.Uint16(cff[off : off+2]))
			off += 2
			var nLeft int
			if format == 1 {
				if off+1 > len(cff) {
					return nil
				}
				nLeft = int(cff[off])
				off++
			} else {
				if off+2 > len(cff) {
					return nil
				}
				nLeft = int(binary.BigEndian.Uint16(cff[off : off+2]))
				off += 2
			}
			for j := 0; j <= nLeft && gid < numGlyphs; j++ {
				cids[gid] = first + j
				gid++
			}
		}
	default:
		return nil
	}
	return cids
}

// ParseCFFCharStringLengths returns the byte length of each entry in the
// CharStrings INDEX at the given offset, or nil on parse failure.
func ParseCFFCharStringLengths(cff []byte, CSOffset int) []int {
	if CSOffset < 0 || CSOffset+3 > len(cff) {
		return nil
	}
	n := int(binary.BigEndian.Uint16(cff[CSOffset : CSOffset+2]))
	if n == 0 {
		return nil
	}
	osz := int(cff[CSOffset+2])
	if osz < 1 || osz > 4 || CSOffset+3+(n+1)*osz > len(cff) {
		return nil
	}
	base := CSOffset + 3
	offsets := make([]int, n+1)
	for i := 0; i <= n; i++ {
		v := 0
		for b := range osz {
			v = v<<8 | int(cff[base+i*osz+b])
		}
		offsets[i] = v
	}
	lens := make([]int, n)
	for i := range n {
		lens[i] = offsets[i+1] - offsets[i]
	}
	return lens
}

// CffStandardStrings is the CFF specification's fixed table of predefined
// SIDs (Appendix A); SIDs >= len(CffStandardStrings) index into a font's own
// String INDEX instead.
var CffStandardStrings = [...]string{
	".notdef", "space", "exclam", "quotedbl", "numbersign", "dollar", "percent", "ampersand",
	"quoteright", "parenleft", "parenright", "asterisk", "plus", "comma", "hyphen", "period",
	"slash", "zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine",
	"colon", "semicolon", "less", "equal", "greater", "question", "at", "A", "B", "C", "D", "E",
	"F", "G", "H", "I", "J", "K", "L", "M", "N", "O", "P", "Q", "R", "S", "T", "U", "V", "W",
	"X", "Y", "Z", "bracketleft", "backslash", "bracketright", "asciicircum", "underscore",
	"quoteleft", "a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p",
	"q", "r", "s", "t", "u", "v", "w", "x", "y", "z", "braceleft", "bar", "braceright",
	"asciitilde", "exclamdown", "cent", "sterling", "fraction", "yen", "florin", "section",
	"currency", "quotesingle", "quotedblleft", "guillemotleft", "guilsinglleft",
	"guilsinglright", "fi", "fl", "endash", "dagger", "daggerdbl", "periodcentered", "paragraph",
	"bullet", "quotesinglbase", "quotedblbase", "quotedblright", "guillemotright", "ellipsis",
	"perthousand", "questiondown", "grave", "acute", "circumflex", "tilde", "macron", "breve",
	"dotaccent", "dieresis", "ring", "cedilla", "hungarumlaut", "ogonek", "caron", "emdash",
	"AE", "ordfeminine", "Lslash", "Oslash", "OE", "ordmasculine", "ae", "dotlessi", "lslash",
	"oslash", "oe", "germandbls", "onesuperior", "logicalnot", "mu", "trademark", "Eth",
	"onehalf", "plusminus", "Thorn", "onequarter", "divide", "brokenbar", "degree", "thorn",
	"threequarters", "twosuperior", "registered", "minus", "eth", "multiply", "threesuperior",
	"copyright", "Aacute", "Acircumflex", "Adieresis", "Agrave", "Aring", "Atilde", "Ccedilla",
	"Eacute", "Ecircumflex", "Edieresis", "Egrave", "Iacute", "Icircumflex", "Idieresis",
	"Igrave", "Ntilde", "Oacute", "Ocircumflex", "Odieresis", "Ograve", "Otilde", "Scaron",
	"Uacute", "Ucircumflex", "Udieresis", "Ugrave", "Yacute", "Ydieresis", "Zcaron", "aacute",
	"acircumflex", "adieresis", "agrave", "aring", "atilde", "ccedilla", "eacute", "ecircumflex",
	"edieresis", "egrave", "iacute", "icircumflex", "idieresis", "igrave", "ntilde", "oacute",
	"ocircumflex", "odieresis", "ograve", "otilde", "scaron", "uacute", "ucircumflex",
	"udieresis", "ugrave", "yacute", "ydieresis", "zcaron", "exclamsmall", "Hungarumlautsmall",
	"dollaroldstyle", "dollarsuperior", "ampersandsmall", "Acutesmall", "parenleftsuperior",
	"parenrightsuperior", "twodotenleader", "onedotenleader", "zerooldstyle", "oneoldstyle",
	"twooldstyle", "threeoldstyle", "fouroldstyle", "fiveoldstyle", "sixoldstyle",
	"sevenoldstyle", "eightoldstyle", "nineoldstyle", "commasuperior", "threequartersemdash",
	"periodsuperior", "questionsmall", "asuperior", "bsuperior", "centsuperior", "dsuperior",
	"esuperior", "isuperior", "lsuperior", "msuperior", "nsuperior", "osuperior", "rsuperior",
	"ssuperior", "tsuperior", "ff", "ffi", "ffl", "parenleftinferior", "parenrightinferior",
	"Circumflexsmall", "hyphensuperior", "Gravesmall", "Asmall", "Bsmall", "Csmall", "Dsmall",
	"Esmall", "Fsmall", "Gsmall", "Hsmall", "Ismall", "Jsmall", "Ksmall", "Lsmall", "Msmall",
	"Nsmall", "Osmall", "Psmall", "Qsmall", "Rsmall", "Ssmall", "Tsmall", "Usmall", "Vsmall",
	"Wsmall", "Xsmall", "Ysmall", "Zsmall", "colonmonetary", "onefitted", "rupiah", "Tildesmall",
	"exclamdownsmall", "centoldstyle", "Lslashsmall", "Scaronsmall", "Zcaronsmall",
	"Dieresissmall", "Brevesmall", "Caronsmall", "Dotaccentsmall", "Macronsmall", "figuredash",
	"hypheninferior", "Ogoneksmall", "Ringsmall", "Cedillasmall", "questiondownsmall",
	"oneeighth", "threeeighths", "fiveeighths", "seveneighths", "onethird", "twothirds",
	"zerosuperior", "foursuperior", "fivesuperior", "sixsuperior", "sevensuperior",
	"eightsuperior", "ninesuperior", "zeroinferior", "oneinferior", "twoinferior",
	"threeinferior", "fourinferior", "fiveinferior", "sixinferior", "seveninferior",
	"eightinferior", "nineinferior", "centinferior", "dollarinferior", "periodinferior",
	"commainferior", "Agravesmall", "Aacutesmall", "Acircumflexsmall", "Atildesmall",
	"Adieresissmall", "Aringsmall", "AEsmall", "Ccedillasmall", "Egravesmall", "Eacutesmall",
	"Ecircumflexsmall", "Edieresissmall", "Igravesmall", "Iacutesmall", "Icircumflexsmall",
	"Idieresissmall", "Ethsmall", "Ntildesmall", "Ogravesmall", "Oacutesmall",
	"Ocircumflexsmall", "Otildesmall", "Odieresissmall", "OEsmall", "Oslashsmall", "Ugravesmall",
	"Uacutesmall", "Ucircumflexsmall", "Udieresissmall", "Yacutesmall", "Thornsmall",
	"Ydieresissmall", "001.000", "001.001", "001.002", "001.003", "Black", "Bold", "Book",
	"Light", "Medium", "Regular", "Roman", "Semibold",
}

// ParseCFFIndex parses a CFF INDEX structure at offset off, returning each
// entry's bytes and the byte offset immediately following the INDEX.
func ParseCFFIndex(cff []byte, off int) (entries [][]byte, end int) {
	if off+2 > len(cff) {
		return nil, off
	}
	n := int(binary.BigEndian.Uint16(cff[off : off+2]))
	if n == 0 {
		return nil, off + 2
	}
	if off+3 > len(cff) {
		return nil, off
	}
	osz := int(cff[off+2])
	if osz < 1 || osz > 4 || off+3+(n+1)*osz > len(cff) {
		return nil, off
	}
	readOff := func(i int) int {
		v := 0
		for _, b := range cff[off+3+i*osz : off+3+(i+1)*osz] {
			v = v<<8 | int(b)
		}
		return v
	}
	dataStart := off + 3 + (n+1)*osz
	last := readOff(n)
	if dataStart+last-1 > len(cff) || last < 1 {
		return nil, off
	}
	entries = make([][]byte, n)
	for i := range n {
		s, e := readOff(i)-1, readOff(i+1)-1
		if s < 0 || e > last-1 || s > e {
			return nil, off
		}
		entries[i] = cff[dataStart+s : dataStart+e]
	}
	return entries, dataStart + last - 1
}

// cffStringIndexEntries returns a CFF program's String INDEX entries (custom
// strings, addressed by SID-len(cffStandardStrings) and up), walking past the
// Name INDEX and Top DICT INDEX to reach it. Returns nil on parse failure.
func cffStringIndexEntries(cff []byte) [][]byte {
	if len(cff) < 4 {
		return nil
	}
	_, off := ParseCFFIndex(cff, int(cff[2])) // skip Name INDEX
	_, off = ParseCFFIndex(cff, off)          // skip Top DICT INDEX
	entries, _ := ParseCFFIndex(cff, off)     // String INDEX
	return entries
}

// cffSIDName resolves a CFF string ID to its glyph name, via the standard
// strings table or the font's own String INDEX. Returns "" if unresolvable.
func cffSIDName(sid int, customStrings [][]byte) string {
	if sid >= 0 && sid < len(CffStandardStrings) {
		return CffStandardStrings[sid]
	}
	if idx := sid - len(CffStandardStrings); idx >= 0 && idx < len(customStrings) {
		return string(customStrings[idx])
	}
	return ""
}

// CFFGlyphNames returns the glyph names defined in a name-keyed (non-CID)
// CFF program's charset, resolving each glyph's SID via cffSIDName. Returns
// nil for CID-keyed fonts (use ParseCFFCharsetCIDs instead) or on parse
// failure, including the rare predefined-charset case (ISOAdobe/Expert/
// ExpertSubset, CharsetOffset 0-2), which this package doesn't decode.
func CFFGlyphNames(cff []byte) []string {
	td, ok := ParseCFFTopDict(cff)
	if !ok || td.IsCIDKeyed || td.CSOffset < 0 || td.CSOffset+2 > len(cff) {
		return nil
	}
	csCount := int(binary.BigEndian.Uint16(cff[td.CSOffset : td.CSOffset+2]))
	// The charset table layout is identical whether it stores CIDs (CID-keyed
	// fonts) or SIDs (name-keyed fonts); only the interpretation differs.
	sids := ParseCFFCharsetCIDs(cff, td.CharsetOffset, csCount)
	if sids == nil {
		return nil
	}
	customStrings := cffStringIndexEntries(cff)
	var names []string
	for _, sid := range sids {
		if name := cffSIDName(sid, customStrings); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// ExtractCFFBytes returns a FontFile3 stream's raw CFF table bytes, whether
// it's a bare CFF program or an OpenType-wrapped one (Subtype /OpenType).
func ExtractCFFBytes(data []byte) []byte {
	if len(data) >= 4 && data[0] == 1 {
		return data
	}
	if tables, ok := ParseSfnt(data); ok {
		if cff, ok := tables["CFF "]; ok {
			return cff
		}
	}
	return nil
}
