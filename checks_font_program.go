package pdfrab

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// --------------------------------------------------------------------------
// TrueType helper functions for 6.3.5 (subset completeness) and
// 6.3.6 (width consistency) checks.
// --------------------------------------------------------------------------

// ttLocaHasGlyph returns a function that reports whether a glyph ID has
// non-empty glyph data in the loca/glyf tables.
func ttLocaHasGlyph(tables map[string][]byte) func(gid int) bool {
	loca := tables["loca"]
	head := tables["head"]
	if len(loca) < 4 || len(head) < 52 {
		return func(int) bool { return false }
	}
	locFmt := int(binary.BigEndian.Uint16(head[50:52]))
	return func(gid int) bool {
		if locFmt == 0 {
			if (gid+1)*2+2 > len(loca) {
				return false
			}
			return binary.BigEndian.Uint16(loca[gid*2:]) != binary.BigEndian.Uint16(loca[(gid+1)*2:])
		}
		if (gid+1)*4+4 > len(loca) {
			return false
		}
		return binary.BigEndian.Uint32(loca[gid*4:]) != binary.BigEndian.Uint32(loca[(gid+1)*4:])
	}
}

// ttNumGlyphs returns the number of glyphs in the font from the maxp table,
// or 0 if unavailable. A glyph ID is valid (present in the font) when it is
// in [0, numGlyphs-1]; empty outlines (space/whitespace) are still valid.
func ttNumGlyphs(tables map[string][]byte) int {
	maxp := tables["maxp"]
	if len(maxp) < 6 {
		return 0
	}
	return int(binary.BigEndian.Uint16(maxp[4:6]))
}

// ttGlyphInRange returns a function that reports whether a glyph ID is within
// the font's valid glyph range [0, numGlyphs-1]. Unlike ttLocaHasGlyph, this
// accepts glyphs with empty outlines (e.g. the space character).
func ttGlyphInRange(tables map[string][]byte) func(gid int) bool {
	n := ttNumGlyphs(tables)
	if n == 0 {
		return func(int) bool { return false }
	}
	return func(gid int) bool {
		return gid >= 0 && gid < n
	}
}

// ttGlyphPresent returns a function that reports whether a glyph ID is
// "present" in the font for coverage purposes (6.3.5). A glyph is present if:
//   - it has non-empty outline data (loca entries differ), OR
//   - it exists in the valid range AND has zero advance width in hmtx
//     (pure whitespace like space — valid to have no outline).
func ttGlyphPresent(tables map[string][]byte) func(gid int) bool {
	hasData := ttLocaHasGlyph(tables)
	inRange := ttGlyphInRange(tables)
	return func(gid int) bool {
		if hasData(gid) {
			return true
		}
		// Glyph has no outline — acceptable only if it has zero advance width
		// (whitespace glyph). This avoids flagging space while still catching
		// subset fonts that omit outline data for non-whitespace glyphs.
		if !inRange(gid) {
			return false
		}
		return ttAdvanceWidth(tables, gid) == 0
	}
}

// ttAdvanceWidth returns the advance width for glyph gid from the hmtx table,
// scaled to PDF units (1/1000 of em). Returns -1 if unavailable.
func ttAdvanceWidth(tables map[string][]byte, gid int) int {
	hmtx := tables["hmtx"]
	hhea := tables["hhea"]
	head := tables["head"]
	if len(hmtx) == 0 || len(hhea) < 36 || len(head) < 20 {
		return -1
	}
	upm := int(binary.BigEndian.Uint16(head[18:20]))
	if upm == 0 {
		return -1
	}
	nHM := int(binary.BigEndian.Uint16(hhea[34:36]))
	var aw int
	if gid < nHM {
		if gid*4+2 > len(hmtx) {
			return -1
		}
		aw = int(binary.BigEndian.Uint16(hmtx[gid*4:]))
	} else if nHM > 0 {
		if (nHM-1)*4+2 > len(hmtx) {
			return -1
		}
		aw = int(binary.BigEndian.Uint16(hmtx[(nHM-1)*4:]))
	}
	return aw * 1000 / upm
}

// ttWindowsBMPCmap finds the platform 3 encoding 1 cmap subtable, or nil.
func ttWindowsBMPCmap(tables map[string][]byte) []byte {
	cmap := tables["cmap"]
	if len(cmap) < 4 {
		return nil
	}
	n := int(binary.BigEndian.Uint16(cmap[2:4]))
	for i := range n {
		rec := cmap[4+i*8:]
		if len(rec) < 8 {
			break
		}
		platform := binary.BigEndian.Uint16(rec[0:2])
		encoding := binary.BigEndian.Uint16(rec[2:4])
		offset := int(binary.BigEndian.Uint32(rec[4:8]))
		if platform == 3 && encoding == 1 && offset+2 <= len(cmap) {
			return cmap[offset:]
		}
	}
	return nil
}

// parseCmapFormat4 decodes a format-4 cmap subtable, returning Unicode→GID.
func parseCmapFormat4(sub []byte) map[uint16]uint16 {
	if len(sub) < 14 || binary.BigEndian.Uint16(sub[0:2]) != 4 {
		return nil
	}
	segCountX2 := int(binary.BigEndian.Uint16(sub[6:8]))
	segCount := segCountX2 / 2
	endOff := 14
	startOff := endOff + segCountX2 + 2 // +2 for reservedPad
	deltaOff := startOff + segCountX2
	rangeOff := deltaOff + segCountX2

	if rangeOff+segCountX2 > len(sub) {
		return nil
	}

	result := map[uint16]uint16{}
	for i := range segCount {
		endCount := binary.BigEndian.Uint16(sub[endOff+i*2:])
		startCount := binary.BigEndian.Uint16(sub[startOff+i*2:])
		delta := int(int16(binary.BigEndian.Uint16(sub[deltaOff+i*2:])))
		rangeOffset := int(binary.BigEndian.Uint16(sub[rangeOff+i*2:]))
		if startCount == 0xFFFF {
			break
		}
		for cc := int(startCount); cc <= int(endCount); cc++ {
			var gid int
			if rangeOffset == 0 {
				gid = (cc + delta) & 0xFFFF
			} else {
				idx := rangeOff + i*2 + rangeOffset + (cc-int(startCount))*2
				if idx+2 > len(sub) {
					continue
				}
				raw := int(binary.BigEndian.Uint16(sub[idx:]))
				if raw == 0 {
					continue
				}
				gid = (raw + delta) & 0xFFFF
			}
			if gid != 0 {
				result[uint16(cc)] = uint16(gid)
			}
		}
	}
	return result
}

// winAnsiGlyphName maps WinAnsiEncoding character codes 0–255 to Adobe glyph names.
// Empty string means the code is undefined (.notdef) in WinAnsiEncoding.
var winAnsiGlyphName [256]string

func init() {
	names32 := []string{
		"space", "exclam", "quotedbl", "numbersign", "dollar", "percent",
		"ampersand", "quotesingle", "parenleft", "parenright", "asterisk", "plus",
		"comma", "hyphen", "period", "slash",
		"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine",
		"colon", "semicolon", "less", "equal", "greater", "question",
		"at",
		"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M",
		"N", "O", "P", "Q", "R", "S", "T", "U", "V", "W", "X", "Y", "Z",
		"bracketleft", "backslash", "bracketright", "asciicircum", "underscore",
		"grave",
		"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m",
		"n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z",
		"braceleft", "bar", "braceright", "asciitilde",
	}
	for i, n := range names32 {
		winAnsiGlyphName[32+i] = n
	}
	extra := map[int]string{
		128: "Euro", 130: "quotesinglbase", 131: "florin", 132: "quotedblbase",
		133: "ellipsis", 134: "dagger", 135: "daggerdbl", 136: "circumflex",
		137: "perthousand", 138: "Scaron", 139: "guilsinglleft", 140: "OE",
		142: "Zcaron", 145: "quoteleft", 146: "quoteright", 147: "quotedblleft",
		148: "quotedblright", 149: "bullet", 150: "endash", 151: "emdash",
		152: "tilde", 153: "trademark", 154: "scaron", 155: "guilsinglright",
		156: "oe", 158: "zcaron", 159: "ydieresis",
		160: "nbspace", 161: "exclamdown", 162: "cent", 163: "sterling",
		164: "currency", 165: "yen", 166: "brokenbar", 167: "section",
		168: "dieresis", 169: "copyright", 170: "ordfeminine", 171: "guillemotleft",
		172: "logicalnot", 173: "hyphen", 174: "registered", 175: "macron",
		176: "degree", 177: "plusminus", 178: "twosuperior", 179: "threesuperior",
		180: "acute", 181: "mu", 182: "paragraph", 183: "periodcentered",
		184: "cedilla", 185: "onesuperior", 186: "ordmasculine", 187: "guillemotright",
		188: "onequarter", 189: "onehalf", 190: "threequarters", 191: "questiondown",
		192: "Agrave", 193: "Aacute", 194: "Acircumflex", 195: "Atilde",
		196: "Adieresis", 197: "Aring", 198: "AE", 199: "Ccedilla",
		200: "Egrave", 201: "Eacute", 202: "Ecircumflex", 203: "Edieresis",
		204: "Igrave", 205: "Iacute", 206: "Icircumflex", 207: "Idieresis",
		208: "Eth", 209: "Ntilde", 210: "Ograve", 211: "Oacute",
		212: "Ocircumflex", 213: "Otilde", 214: "Odieresis", 215: "multiply",
		216: "Oslash", 217: "Ugrave", 218: "Uacute", 219: "Ucircumflex",
		220: "Udieresis", 221: "Yacute", 222: "Thorn", 223: "germandbls",
		224: "agrave", 225: "aacute", 226: "acircumflex", 227: "atilde",
		228: "adieresis", 229: "aring", 230: "ae", 231: "ccedilla",
		232: "egrave", 233: "eacute", 234: "ecircumflex", 235: "edieresis",
		236: "igrave", 237: "iacute", 238: "icircumflex", 239: "idieresis",
		240: "eth", 241: "ntilde", 242: "ograve", 243: "oacute",
		244: "ocircumflex", 245: "otilde", 246: "odieresis", 247: "divide",
		248: "oslash", 249: "ugrave", 250: "uacute", 251: "ucircumflex",
		252: "udieresis", 253: "yacute", 254: "thorn", 255: "ydieresis",
	}
	for cc, n := range extra {
		winAnsiGlyphName[cc] = n
	}
}

// winAnsiToUnicode maps WinAnsiEncoding character codes 0–255 to Unicode.
// For codes 0x20-0x7E and 0xA0-0xFF, the mapping equals the code point.
// Codes 0x80-0x9F use the Windows-1252 / PDF WinAnsiEncoding table.
var winAnsiToUnicode [256]uint16

func init() {
	for i := 0x20; i <= 0x7E; i++ {
		winAnsiToUnicode[i] = uint16(i)
	}
	for i := 0xA0; i <= 0xFF; i++ {
		winAnsiToUnicode[i] = uint16(i)
	}
	// Windows-1252 0x80-0x9F block
	special := map[int]uint16{
		0x80: 0x20AC, 0x82: 0x201A, 0x83: 0x0192, 0x84: 0x201E,
		0x85: 0x2026, 0x86: 0x2020, 0x87: 0x2021, 0x88: 0x02C6,
		0x89: 0x2030, 0x8A: 0x0160, 0x8B: 0x2039, 0x8C: 0x0152,
		0x8E: 0x017D, 0x91: 0x2018, 0x92: 0x2019, 0x93: 0x201C,
		0x94: 0x201D, 0x95: 0x2022, 0x96: 0x2013, 0x97: 0x2014,
		0x98: 0x02DC, 0x99: 0x2122, 0x9A: 0x0161, 0x9B: 0x203A,
		0x9C: 0x0153, 0x9E: 0x017E, 0x9F: 0x0178,
	}
	for cc, u := range special {
		winAnsiToUnicode[cc] = u
	}
}

// validateType1SubsetCoverage verifies that every character code with a non-zero
// width in the Widths array maps to a glyph name that is present in the font's
// CharSet (6.3.5). Handles WinAnsiEncoding by name and custom encoding dicts.
func validateType1SubsetCoverage(obj PDFValue, v PDFDict, desc PDFDict, firstChar, lastChar int, widths PDFArray, ctx *ValidationContext) {
	charSetVal, ok := desc.Entries["CharSet"]
	if !ok {
		return // CharSet absence is caught by a separate check
	}
	var charSetStr string
	switch cs := charSetVal.(type) {
	case PDFString:
		charSetStr = cs.Value
	default:
		return
	}

	// 6.3.5: an empty CharSet is a violation — a subset must list the glyphs it contains.
	if charSetStr == "" {
		ctx.ReportError(obj, "6.3.5", 2, "Type 1 subset font descriptor has an empty CharSet")
		return
	}

	// Parse CharSet: "/glyph1/glyph2/..."
	charSet := map[string]bool{}
	charSet[".notdef"] = true
	for _, part := range splitGlyphNames(charSetStr) {
		charSet[part] = true
	}

	// Build a code→glyphName table from the font's encoding.
	var glyphNames [256]string
	switch enc := v.Entries["Encoding"].(type) {
	case PDFName:
		switch enc.Value {
		case "WinAnsiEncoding":
			glyphNames = winAnsiGlyphName
		default:
			return // unsupported named encoding
		}
	case PDFDict:
		// Custom encoding: start from base (StandardEncoding or as named by BaseEncoding)
		// then apply Differences.
		base, _ := enc.Entries["BaseEncoding"].(PDFName)
		switch base.Value {
		case "WinAnsiEncoding":
			glyphNames = winAnsiGlyphName
		}
		if diffs, ok := enc.Entries["Differences"].(PDFArray); ok {
			code := 0
			for _, item := range diffs {
				switch d := item.(type) {
				case PDFInteger:
					code = int(d)
				case PDFName:
					if code >= 0 && code < 256 {
						glyphNames[code] = d.Value
					}
					code++
				}
			}
		}
	default:
		return
	}

	checkCode := func(cc int) bool {
		if cc < 0 || cc > 255 {
			return true
		}
		glyph := glyphNames[cc]
		if glyph == "" || glyph == ".notdef" {
			return true
		}
		if !charSet[glyph] {
			ctx.ReportError(obj, "6.3.5", 1,
				fmt.Sprintf("character code %d maps to glyph /%s which is not defined in the embedded font subset (CharSet)", cc, glyph))
			return false
		}
		return true
	}

	// CharSet must list every glyph actually "used for rendering" — check
	// codes actually shown in content streams regardless of their Widths
	// entry (a missing glyph is sometimes given width 0 as a placeholder,
	// which would otherwise hide the violation). Fall back to checking every
	// non-zero-width code in the declared range when usage info could not be
	// collected for this font.
	if usedCodes, knownUsage := ctx.usedCodesFor(v); knownUsage {
		for cc := range usedCodes {
			if !checkCode(cc) {
				return
			}
		}
		return
	}

	for i, w := range widths {
		var width int
		switch wv := w.(type) {
		case PDFInteger:
			width = int(wv)
		case PDFReal:
			width = int(wv)
		}
		if width == 0 {
			continue
		}
		if !checkCode(firstChar + i) {
			return
		}
	}
}

// splitGlyphNames splits a CharSet string like "/a/b/c" into ["a", "b", "c"].
func splitGlyphNames(cs string) []string {
	var result []string
	for part := range strings.SplitSeq(cs, "/") {
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

// parseCIDWidths decodes a CID font W array into (CID, pdfWidth-in-1/1000-em) pairs.
// W format: [c1 [w1 w2 ...] c2 c3 w ...]
func parseCIDWidths(w PDFArray) [][2]int {
	var pairs [][2]int
	i := 0
	intV := func(v PDFValue) (int, bool) {
		switch x := v.(type) {
		case PDFInteger:
			return int(x), true
		case PDFReal:
			return int(x), true
		}
		return 0, false
	}
	for i < len(w) {
		c1, ok := intV(w[i])
		if !ok {
			i++
			continue
		}
		i++
		if i >= len(w) {
			break
		}
		if arr, ok2 := w[i].(PDFArray); ok2 {
			for j, wv := range arr {
				if width, ok3 := intV(wv); ok3 {
					pairs = append(pairs, [2]int{c1 + j, width})
				}
			}
			i++
		} else {
			c2, ok2 := intV(w[i])
			if !ok2 {
				i++
				continue
			}
			i++
			if i >= len(w) {
				break
			}
			width, ok3 := intV(w[i])
			if !ok3 {
				i++
				continue
			}
			i++
			for c := c1; c <= c2; c++ {
				pairs = append(pairs, [2]int{c, width})
			}
		}
	}
	return pairs
}

// cffTopDict holds the Top DICT operands relevant to CID-keyed subset
// validation (6.3.5): the CharStrings INDEX location, whether the font is
// CID-keyed (has a ROS operator), and the Charset table offset.
type cffTopDict struct {
	csOffset      int // CharStrings INDEX offset, -1 if not found
	charsetOffset int // Charset table offset, -1 if not found / predefined
	isCIDKeyed    bool
}

// parseCFFTopDict parses a CFF binary stream's Top DICT and returns the
// operands needed to validate CID coverage. ok is false on parse failure.
func parseCFFTopDict(cff []byte) (td cffTopDict, ok bool) {
	td.csOffset = -1
	td.charsetOffset = -1
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

	// Parse Top DICT DICT encoding to find CharStrings (17), Charset (15),
	// and ROS (escape 12 30, present only on CID-keyed fonts).
	var stack []int
	for i := 0; i < len(topDict); {
		b := int(topDict[i])
		switch {
		case b >= 32 && b <= 246:
			stack = append(stack, b-139)
			i++
		case b >= 247 && b <= 250:
			if i+1 >= len(topDict) {
				return td, false
			}
			stack = append(stack, (b-247)*256+int(topDict[i+1])+108)
			i += 2
		case b >= 251 && b <= 254:
			if i+1 >= len(topDict) {
				return td, false
			}
			stack = append(stack, -(b-251)*256-int(topDict[i+1])-108)
			i += 2
		case b == 28:
			if i+2 >= len(topDict) {
				return td, false
			}
			v := int(int16(binary.BigEndian.Uint16(topDict[i+1 : i+3])))
			stack = append(stack, v)
			i += 3
		case b == 29:
			if i+4 >= len(topDict) {
				return td, false
			}
			v := int(int32(binary.BigEndian.Uint32(topDict[i+1 : i+5])))
			stack = append(stack, v)
			i += 5
		case b == 30: // real number — skip
			i++
			for i < len(topDict) {
				nb := topDict[i]
				i++
				if nb&0x0F == 0x0F {
					break
				}
			}
		case b == 12: // two-byte escape operator
			if i+1 >= len(topDict) {
				return td, false
			}
			if topDict[i+1] == 30 { // ROS: marks a CID-keyed font
				td.isCIDKeyed = true
			}
			stack = nil
			i += 2
		default: // single-byte operator
			switch {
			case b == 17 && len(stack) > 0: // CharStrings
				td.csOffset = stack[0]
			case b == 15 && len(stack) > 0: // charset
				td.charsetOffset = stack[0]
			}
			stack = nil
			i++
		}
	}
	return td, true
}

// parseCFFCharStringsCount parses a CFF binary stream and returns the number
// of entries in the CharStrings INDEX. Returns -1 on parse failure.
func parseCFFCharStringsCount(cff []byte) int {
	td, ok := parseCFFTopDict(cff)
	if !ok || td.csOffset < 0 || td.csOffset+2 > len(cff) {
		return -1
	}
	return int(binary.BigEndian.Uint16(cff[td.csOffset : td.csOffset+2]))
}

// parseCFFCharsetCIDs parses a CFF Charset table (CID-keyed fonts store CIDs
// here instead of SIDs) and returns the CID for each glyph ID. GID 0 is
// always .notdef (CID 0). Returns nil if the charset is one of the three
// predefined tables (offsets 0, 1, 2) or otherwise unparsable — predefined
// charsets are not used by CID-keyed fonts in practice.
func parseCFFCharsetCIDs(cff []byte, charsetOffset, numGlyphs int) []int {
	if charsetOffset <= 2 || charsetOffset >= len(cff) || numGlyphs <= 0 {
		return nil
	}
	format := cff[charsetOffset]
	cids := make([]int, numGlyphs)
	off := charsetOffset + 1
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

// parseCFFCharStringLengths returns the byte length of each entry in the
// CharStrings INDEX at the given offset, or nil on parse failure.
func parseCFFCharStringLengths(cff []byte, csOffset int) []int {
	if csOffset < 0 || csOffset+3 > len(cff) {
		return nil
	}
	n := int(binary.BigEndian.Uint16(cff[csOffset : csOffset+2]))
	if n == 0 {
		return nil
	}
	osz := int(cff[csOffset+2])
	if osz < 1 || osz > 4 || csOffset+3+(n+1)*osz > len(cff) {
		return nil
	}
	base := csOffset + 3
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

// validateCIDCFFSubset checks that all CIDs referenced in the W array are
// defined in the embedded CFF program (CharStrings count ≥ max CID + 1) (6.3.5).
func validateCIDCFFSubset(obj PDFValue, ff PDFDict, w PDFArray, ctx *ValidationContext) {
	data, err := decodeStream(ff)
	if err != nil {
		return
	}
	td, ok := parseCFFTopDict(data)
	if !ok || td.csOffset < 0 || td.csOffset+2 > len(data) {
		return
	}
	csCount := int(binary.BigEndian.Uint16(data[td.csOffset : td.csOffset+2]))

	// CID-keyed CFFs (ROS present) remap glyph IDs to CIDs via the Charset
	// table — GID index and CID are not the same number, so a referenced CID
	// is valid as long as some glyph in the subset maps to it. Non-CID-keyed
	// CFFs (Identity ordering) use the GID directly as the CID.
	if td.isCIDKeyed {
		cids := parseCFFCharsetCIDs(data, td.charsetOffset, csCount)
		if cids == nil {
			return
		}
		gidOfCID := make(map[int]int, len(cids))
		for gid, c := range cids {
			gidOfCID[c] = gid
		}
		// A glyph mapped by the charset can still be functionally undefined if
		// its CharString is a bare/near-empty stub (no drawing operators) —
		// e.g. a single-byte "endchar" with no preceding hsbw/width. Treat
		// such stubs as not present, the same way a missing charset entry is.
		lens := parseCFFCharStringLengths(data, td.csOffset)
		for _, pair := range parseCIDWidths(w) {
			cid := pair[0]
			gid, ok := gidOfCID[cid]
			if !ok || (lens != nil && gid < len(lens) && lens[gid] <= 1) {
				ctx.ReportError(obj, "6.3.5", 1,
					fmt.Sprintf("CID %d referenced in font W array is not defined in CFF charset", cid))
				return
			}
		}
		return
	}

	for _, pair := range parseCIDWidths(w) {
		cid := pair[0]
		if cid >= csCount {
			ctx.ReportError(obj, "6.3.5", 1,
				fmt.Sprintf("CID %d referenced in font W array is not defined in CFF CharStrings (count=%d)", cid, csCount))
			return
		}
	}
}

// validateCIDSetBitmap checks that the FontDescriptor's CIDSet bitmap marks
// every CID that actually has a glyph in the embedded CID-keyed CFF program
// (6.3.5/3). Each byte covers 8 CIDs, most-significant bit first: bit j of
// byte i corresponds to CID i*8+j.
func validateCIDSetBitmap(obj PDFValue, desc PDFDict, ff PDFDict, ctx *ValidationContext) {
	cidSet, ok := desc.Entries["CIDSet"].(PDFDict)
	if !ok || !cidSet.HasStream {
		return
	}
	bitmap, err := decodeStream(cidSet)
	if err != nil {
		return
	}
	data, err := decodeStream(ff)
	if err != nil {
		return
	}
	td, ok := parseCFFTopDict(data)
	if !ok || !td.isCIDKeyed || td.csOffset < 0 || td.csOffset+2 > len(data) {
		return
	}
	csCount := int(binary.BigEndian.Uint16(data[td.csOffset : td.csOffset+2]))
	cids := parseCFFCharsetCIDs(data, td.charsetOffset, csCount)
	if cids == nil {
		return
	}
	for _, cid := range cids {
		byteIdx, bitIdx := cid/8, 7-cid%8
		if byteIdx >= len(bitmap) || bitmap[byteIdx]&(1<<bitIdx) == 0 {
			ctx.ReportError(obj, "6.3.5", 3,
				fmt.Sprintf("CIDSet does not list CID %d, which has a glyph in the embedded font program", cid))
			return
		}
	}
}

// validateCIDTrueTypeSubset checks that all CIDs referenced in the W array
// are present in the embedded TrueType program (6.3.5). A glyph with an empty
// outline but zero advance width (e.g. space) is still considered present.
func validateCIDTrueTypeSubset(obj PDFValue, ff PDFDict, w PDFArray, ctx *ValidationContext) {
	data, err := decodeStream(ff)
	if err != nil {
		return
	}
	tables, ok := parseSfnt(data)
	if !ok {
		return
	}
	glyphPresent := ttGlyphPresent(tables)
	for _, pair := range parseCIDWidths(w) {
		cid := pair[0]
		if !glyphPresent(cid) {
			ctx.ReportError(obj, "6.3.5", 1,
				fmt.Sprintf("CID %d referenced in font W array has no glyph in embedded program", cid))
			return
		}
	}
}

// validateCIDTrueTypeMetrics checks that CID advance widths in W match the
// embedded TrueType hmtx table (6.3.6).
func validateCIDTrueTypeMetrics(obj PDFValue, ff PDFDict, w PDFArray, ctx *ValidationContext) {
	data, err := decodeStream(ff)
	if err != nil {
		return
	}
	tables, ok := parseSfnt(data)
	if !ok {
		return
	}
	for _, pair := range parseCIDWidths(w) {
		cid, pdfWidth := pair[0], pair[1]
		fontWidth := ttAdvanceWidth(tables, cid)
		if fontWidth < 0 {
			continue
		}
		// Allow ±1 rounding tolerance.
		if abs(fontWidth-pdfWidth) > 1 {
			ctx.ReportError(obj, "6.3.6", 1,
				fmt.Sprintf("CID %d: PDF width %d ≠ font hmtx width %d",
					cid, pdfWidth, fontWidth))
			return
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// validateSimpleTrueTypeSubset checks that all referenced character codes have
// a corresponding glyph in the embedded TrueType program (6.3.5).
// For fonts without a cmap (common in subsets), uses a heuristic: the number
// of non-empty glyphs must be at least the number of referenced character codes.
func validateSimpleTrueTypeSubset(obj PDFValue, ff PDFDict, firstChar, lastChar int, widths PDFArray, ctx *ValidationContext) {
	data, err := decodeStream(ff)
	if err != nil {
		return
	}
	tables, ok := parseSfnt(data)
	if !ok {
		return
	}

	// Count non-zero widths entries (character codes the PDF actually uses).
	required := 0
	for _, w := range widths {
		if wi, ok2 := w.(PDFInteger); ok2 && wi > 0 {
			required++
		} else if wr, ok2 := w.(PDFReal); ok2 && wr > 0 {
			required++
		}
	}
	if required == 0 {
		return
	}

	// Try cmap-based lookup first.
	cmapSub := ttWindowsBMPCmap(tables)
	if cmapSub == nil {
		return
	}
	gidMap := parseCmapFormat4(cmapSub)
	if gidMap == nil {
		return
	}
	numGlyphs := ttNumGlyphs(tables)

	checkCode := func(cc int) bool {
		unicode := winAnsiToUnicode[cc]
		if unicode == 0 {
			return true
		}
		gid, exists := gidMap[unicode]
		// A glyph is present in the subset if it maps to a non-.notdef GID
		// within the font's valid range. GID 0 is .notdef, which means the
		// character was not included in the subset. Outline data may be absent
		// for whitespace glyphs (e.g. space) — that is still conformant.
		if !exists || gid == 0 || (numGlyphs > 0 && int(gid) >= numGlyphs) {
			ctx.ReportError(obj, "6.3.5", 1,
				fmt.Sprintf("character code %d (U+%04X) has no glyph in embedded font program", cc, unicode))
			return false
		}
		return true
	}

	// CharSet coverage only needs to hold for codes actually "used for
	// rendering" — check codes actually shown in content streams regardless
	// of their Widths entry (a missing glyph is sometimes given width 0 as a
	// placeholder). Fall back to checking every non-zero-width code in the
	// declared range when usage info could not be collected for this font.
	if fontDict, ok := obj.(PDFDict); ok {
		if usedCodes, knownUsage := ctx.usedCodesFor(fontDict); knownUsage {
			for cc := range usedCodes {
				if !checkCode(cc) {
					return
				}
			}
			return
		}
	}

	for i, w := range widths {
		var isNonZero bool
		switch wv := w.(type) {
		case PDFInteger:
			isNonZero = wv > 0
		case PDFReal:
			isNonZero = wv > 0
		}
		if !isNonZero {
			continue
		}
		if !checkCode(firstChar + i) {
			return
		}
	}
}

// validateSimpleTrueTypeMetrics checks that advance widths in the PDF Widths
// array match the embedded TrueType hmtx table (6.3.6).
func validateSimpleTrueTypeMetrics(obj PDFValue, ff PDFDict, firstChar, lastChar int, widths PDFArray, ctx *ValidationContext) {
	data, err := decodeStream(ff)
	if err != nil {
		return
	}
	tables, ok := parseSfnt(data)
	if !ok {
		return
	}
	cmapSub := ttWindowsBMPCmap(tables)
	if cmapSub == nil {
		return
	}
	gidMap := parseCmapFormat4(cmapSub)
	if gidMap == nil {
		return
	}
	for i, w := range widths {
		var pdfWidth int
		switch wv := w.(type) {
		case PDFInteger:
			pdfWidth = int(wv)
		case PDFReal:
			pdfWidth = int(wv)
		default:
			continue
		}
		if pdfWidth == 0 {
			continue
		}
		cc := firstChar + i
		unicode := winAnsiToUnicode[cc]
		if unicode == 0 {
			continue
		}
		gid, exists := gidMap[unicode]
		if !exists {
			continue
		}
		fontWidth := ttAdvanceWidth(tables, int(gid))
		if fontWidth < 0 {
			continue
		}
		if abs(fontWidth-pdfWidth) > 1 {
			ctx.ReportError(obj, "6.3.6", 1,
				fmt.Sprintf("character code %d: PDF width %d ≠ font hmtx width %d",
					cc, pdfWidth, fontWidth))
			return
		}
	}
}

// parseSfnt parses an sfnt (TrueType/OpenType) table directory, returning each
// table's bytes. The second result is false if the data is not a valid sfnt.
func parseSfnt(data []byte) (map[string][]byte, bool) {
	if len(data) < 12 {
		return nil, false
	}
	switch binary.BigEndian.Uint32(data[:4]) {
	case 0x00010000, 0x74727565, 0x4F54544F: // 1.0, 'true', 'OTTO'
	default:
		return nil, false
	}
	num := int(binary.BigEndian.Uint16(data[4:6]))
	if num == 0 || 12+num*16 > len(data) {
		return nil, false
	}
	tables := map[string][]byte{}
	for i := range num {
		rec := data[12+i*16:]
		off := binary.BigEndian.Uint32(rec[8:12])
		ln := binary.BigEndian.Uint32(rec[12:16])
		if int(off) > len(data) {
			continue
		}
		end := min(int(off)+int(ln), len(data))
		tables[string(rec[:4])] = data[off:end]
	}
	return tables, true
}

// fontProgramValid reports whether the embedded font program in a FontFile is a
// structurally valid font of its expected type (6.3.2).
func fontProgramValid(stream PDFDict, key string) bool {
	data, err := decodeStream(stream)
	if err != nil || len(data) == 0 {
		return false
	}
	switch key {
	case "FontFile2":
		_, ok := parseSfnt(data)
		return ok
	case "FontFile3":
		if len(data) >= 4 && data[0] == 1 { // CFF header, major version 1
			return true
		}
		_, ok := parseSfnt(data) // OpenType-wrapped CFF
		return ok
	case "FontFile":
		// Type 1: the clear-text portion begins with a PostScript marker.
		return bytes.HasPrefix(data, []byte("%!"))
	}
	return true
}

// validateFontProgram flags a damaged embedded font program (6.3.2).
func validateFontProgram(obj PDFValue, desc PDFDict, name string, ctx *ValidationContext) {
	for _, key := range []string{"FontFile", "FontFile2", "FontFile3"} {
		ff, ok := desc.Entries[key].(PDFDict)
		if !ok {
			continue
		}
		if !fontProgramValid(ff, key) {
			ctx.ReportError(obj, "6.3.2", 1, fmt.Sprintf("embedded font program for %s is damaged", name))
		}
	}
}

// trueTypeCmapSubtables returns the number of cmap subtables in an embedded
// TrueType font, and whether it could be determined.
func trueTypeCmapSubtables(desc PDFDict) (int, bool) {
	ff, ok := desc.Entries["FontFile2"].(PDFDict)
	if !ok {
		return 0, false
	}
	data, err := decodeStream(ff)
	if err != nil {
		return 0, false
	}
	tables, ok := parseSfnt(data)
	if !ok {
		return 0, false
	}
	cmap, ok := tables["cmap"]
	if !ok || len(cmap) < 4 {
		return 0, false
	}
	return int(binary.BigEndian.Uint16(cmap[2:4])), true
}

// standardEncoding maps character codes 0–255 to PostScript Standard Encoding
// glyph names. Empty string means the code is undefined (.notdef).
var standardEncoding = [256]string{
	32: "space", 33: "exclam", 34: "quotedbl", 35: "numbersign", 36: "dollar",
	37: "percent", 38: "ampersand", 39: "quoteright", 40: "parenleft", 41: "parenright",
	42: "asterisk", 43: "plus", 44: "comma", 45: "hyphen", 46: "period", 47: "slash",
	48: "zero", 49: "one", 50: "two", 51: "three", 52: "four", 53: "five",
	54: "six", 55: "seven", 56: "eight", 57: "nine", 58: "colon", 59: "semicolon",
	60: "less", 61: "equal", 62: "greater", 63: "question", 64: "at",
	65: "A", 66: "B", 67: "C", 68: "D", 69: "E", 70: "F", 71: "G", 72: "H",
	73: "I", 74: "J", 75: "K", 76: "L", 77: "M", 78: "N", 79: "O", 80: "P",
	81: "Q", 82: "R", 83: "S", 84: "T", 85: "U", 86: "V", 87: "W", 88: "X",
	89: "Y", 90: "Z",
	91: "bracketleft", 92: "backslash", 93: "bracketright", 94: "asciicircum",
	95: "underscore", 96: "quoteleft",
	97: "a", 98: "b", 99: "c", 100: "d", 101: "e", 102: "f", 103: "g", 104: "h",
	105: "i", 106: "j", 107: "k", 108: "l", 109: "m", 110: "n", 111: "o", 112: "p",
	113: "q", 114: "r", 115: "s", 116: "t", 117: "u", 118: "v", 119: "w", 120: "x",
	121: "y", 122: "z",
	123: "braceleft", 124: "bar", 125: "braceright", 126: "asciitilde",
	161: "exclamdown", 162: "cent", 163: "sterling", 164: "fraction", 165: "yen",
	166: "florin", 167: "section", 168: "currency", 169: "quotesingle", 170: "quotedblleft",
	171: "guillemotleft", 172: "guilsinglleft", 173: "guilsinglright", 174: "fi", 175: "fl",
	177: "endash", 178: "dagger", 179: "daggerdbl", 180: "periodcentered",
	182: "paragraph", 183: "bullet", 184: "quotesinglbase", 185: "quotedblbase",
	186: "quotedblright", 187: "guillemotright", 188: "ellipsis", 189: "perthousand",
	191: "questiondown", 193: "grave", 194: "acute", 195: "circumflex", 196: "tilde",
	197: "macron", 198: "breve", 199: "dotaccent", 200: "dieresis",
	202: "ring", 203: "cedilla", 205: "hungarumlaut", 206: "ogonek", 207: "caron",
	208: "emdash", 225: "AE", 227: "ordfeminine", 232: "Lslash", 233: "Oslash",
	234: "OE", 235: "ordmasculine", 241: "ae", 245: "dotlessi", 248: "lslash",
	249: "oslash", 250: "oe", 251: "germandbls",
}

// decryptType1Block decrypts a Type1 font binary block using the given seed key.
// It skips the first 4 bytes (random seed) and returns the plaintext.
func decryptType1Block(data []byte, seedKey uint16) []byte {
	R := seedKey
	out := make([]byte, 0, len(data))
	for i, c := range data {
		p := byte(uint16(c) ^ (R >> 8))
		R = (uint16(c)+R)*52845 + 22719
		if i >= 4 {
			out = append(out, p)
		}
	}
	return out
}

// parseType1AdvanceWidth parses a decrypted Type1 CharString and returns the
// advance width (wx) from the hsbw (op 13) or sbw (escape op 8) command.
func parseType1AdvanceWidth(cs []byte) (int, bool) {
	stack := make([]int, 0, 8)
	i := 0
	for i < len(cs) {
		b := cs[i]
		switch {
		case b >= 32 && b <= 246:
			stack = append(stack, int(b)-139)
			i++
		case b >= 247 && b <= 250:
			if i+1 >= len(cs) {
				return 0, false
			}
			stack = append(stack, (int(b)-247)*256+int(cs[i+1])+108)
			i += 2
		case b >= 251 && b <= 254:
			if i+1 >= len(cs) {
				return 0, false
			}
			stack = append(stack, -((int(b)-251)*256 + int(cs[i+1]) + 108))
			i += 2
		case b == 28:
			if i+2 >= len(cs) {
				return 0, false
			}
			v := int(int16(uint16(cs[i+1])<<8 | uint16(cs[i+2])))
			stack = append(stack, v)
			i += 3
		case b == 29:
			if i+4 >= len(cs) {
				return 0, false
			}
			v := int(int32(uint32(cs[i+1])<<24 | uint32(cs[i+2])<<16 | uint32(cs[i+3])<<8 | uint32(cs[i+4])))
			stack = append(stack, v)
			i += 5
		case b == 12: // 2-byte escape
			i++
			if i >= len(cs) {
				return 0, false
			}
			if cs[i] == 8 && len(stack) >= 4 { // sbw: sbx sby wx wy
				return stack[2], true
			}
			stack = stack[:0]
			i++
		case b == 13: // hsbw: sbx wx → wx is advance width
			if len(stack) >= 2 {
				return stack[1], true
			}
			return 0, false
		case b == 14: // endchar
			return 0, false
		default:
			stack = stack[:0]
			i++
		}
	}
	return 0, false
}

// type1CharStringRe matches entries in a decrypted Type1 CharStrings dict.
var type1CharStringRe = regexp.MustCompile(`/(\S+) (\d+) RD `)

// extractType1GlyphWidths decrypts the eexec binary section of a Type1 font
// program and returns glyph name → advance width.
// binStart is the byte offset of the first encrypted byte after the "eexec" keyword.
func extractType1GlyphWidths(fontData []byte, binStart int) map[string]int {
	if binStart <= 0 || binStart >= len(fontData) {
		return nil
	}
	plain := decryptType1Block(fontData[binStart:], 55665)

	csIdx := bytes.Index(plain, []byte("/CharStrings"))
	if csIdx < 0 {
		return nil
	}
	result := map[string]int{}
	cs := plain[csIdx:]
	matches := type1CharStringRe.FindAllSubmatchIndex(cs, -1)
	for _, m := range matches {
		name := string(cs[m[2]:m[3]])
		n, _ := strconv.Atoi(string(cs[m[4]:m[5]]))
		if n <= 0 || m[1]+n > len(cs) {
			continue
		}
		dec := decryptType1Block(cs[m[1]:m[1]+n], 4330)
		if w, ok := parseType1AdvanceWidth(dec); ok {
			result[name] = w
		}
	}
	return result
}

// type1EncodingRe finds the built-in Encoding name in a Type1 clear-text section.
var type1EncodingRe = regexp.MustCompile(`/Encoding\s+(\w+)\s+def`)

// validateType1Metrics checks that PDF Widths entries match advance widths in
// the embedded Type1 font program (6.3.6).
func validateType1Metrics(obj PDFValue, ff PDFDict, firstChar, lastChar int, widths PDFArray, pdfEncoding string, ctx *ValidationContext) {
	fontData, err := decodeStream(ff)
	if err != nil || len(fontData) == 0 {
		return
	}

	// Determine the character-code → glyph-name mapping.
	// Prefer the encoding declared in the PDF font dict; fall back to the
	// font's own /Encoding declaration in the clear-text section.
	encName := pdfEncoding
	if encName == "" {
		// Parse the text portion of the Type1 program for /Encoding <name> def.
		// The text portion is everything before the eexec keyword.
		eexecIdx := bytes.Index(fontData, []byte("eexec"))
		textPart := fontData
		if eexecIdx > 0 {
			textPart = fontData[:eexecIdx]
		}
		if m := type1EncodingRe.FindSubmatch(textPart); m != nil {
			encName = string(m[1])
		}
	}
	var enc [256]string
	switch encName {
	case "StandardEncoding":
		enc = standardEncoding
	case "WinAnsiEncoding":
		enc = winAnsiGlyphName
	default:
		return
	}

	// Locate the boundary between the ASCII and binary sections by finding
	// the "eexec" keyword; the binary data starts on the next byte.
	eexecIdx := bytes.Index(fontData, []byte("eexec"))
	if eexecIdx < 0 {
		return
	}
	binStart := eexecIdx + 5
	for binStart < len(fontData) && (fontData[binStart] == '\n' || fontData[binStart] == '\r' || fontData[binStart] == ' ') {
		binStart++
	}

	glyphWidths := extractType1GlyphWidths(fontData, binStart)
	if len(glyphWidths) == 0 {
		return
	}

	for i, w := range widths {
		var pdfWidth int
		switch wv := w.(type) {
		case PDFInteger:
			pdfWidth = int(wv)
		case PDFReal:
			pdfWidth = int(wv)
		default:
			continue
		}
		if pdfWidth == 0 {
			continue
		}
		cc := firstChar + i
		if cc < 0 || cc > 255 {
			continue
		}
		glyph := enc[cc]
		if glyph == "" {
			continue
		}
		csWidth, found := glyphWidths[glyph]
		if !found {
			continue
		}
		if abs(pdfWidth-csWidth) > 1 {
			ctx.ReportError(obj, "6.3.6", 1,
				fmt.Sprintf("character code %d (/%s): PDF width %d ≠ Type1 advance width %d",
					cc, glyph, pdfWidth, csWidth))
			return
		}
	}
}

var wmodeRe = regexp.MustCompile(`/WMode\s+(\d+)\s+def`)

// validateCMapWMode flags an embedded CMap whose dictionary WMode disagrees with
// the WMode declared in its stream (6.3.3.3).
func validateCMapWMode(obj PDFValue, cmap PDFDict, ctx *ValidationContext) {
	if !cmap.HasStream {
		return
	}
	dictWMode, ok := cmap.Entries["WMode"].(PDFInteger)
	if !ok {
		return
	}
	data, err := decodeStream(cmap)
	if err != nil {
		return
	}
	m := wmodeRe.FindSubmatch(data)
	if m == nil {
		return
	}
	streamWMode, _ := strconv.Atoi(string(m[1]))
	if int(dictWMode) != streamWMode {
		ctx.ReportError(obj, "6.3.3.3", 2, "WMode in CMap dictionary and stream are inconsistent")
	}
}
