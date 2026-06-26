package verify

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/voidrab/gopdfrab/internal/pdf"
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

// TTNumGlyphs returns the number of glyphs in the font from the maxp table,
// or 0 if unavailable. A glyph ID is valid (present in the font) when it is
// in [0, numGlyphs-1]; empty outlines (space/whitespace) are still valid.
func TTNumGlyphs(tables map[string][]byte) int {
	maxp := tables["maxp"]
	if len(maxp) < 6 {
		return 0
	}
	return int(binary.BigEndian.Uint16(maxp[4:6]))
}

// TTGlyphInRange returns a function that reports whether a glyph ID is within
// the font's valid glyph range [0, numGlyphs-1]. Unlike ttLocaHasGlyph, this
// accepts glyphs with empty outlines (e.g. the space character).
func TTGlyphInRange(tables map[string][]byte) func(gid int) bool {
	n := TTNumGlyphs(tables)
	if n == 0 {
		return func(int) bool { return false }
	}
	return func(gid int) bool {
		return gid >= 0 && gid < n
	}
}

// TTGlyphPresent returns a function reporting whether a glyph ID counts as
// present for coverage purposes (6.3.5): non-empty outline, or in-range with
// zero advance width (whitespace glyphs are exempt from needing outline data).
func TTGlyphPresent(tables map[string][]byte) func(gid int) bool {
	hasData := ttLocaHasGlyph(tables)
	inRange := TTGlyphInRange(tables)
	return func(gid int) bool {
		if hasData(gid) {
			return true
		}
		if !inRange(gid) {
			return false
		}
		return TTAdvanceWidth(tables, gid) == 0
	}
}

// TTAdvanceWidth returns the advance width for glyph gid from the hmtx table,
// scaled to PDF units (1/1000 of em). Returns -1 if unavailable.
func TTAdvanceWidth(tables map[string][]byte, gid int) int {
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
	return (aw*1000 + upm/2) / upm
}

// TTWindowsBMPCmap finds the platform 3 encoding 1 cmap subtable, or nil.
func TTWindowsBMPCmap(tables map[string][]byte) []byte {
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

// ParseCmapFormat4 decodes a format-4 cmap subtable, returning Unicode→GID.
func ParseCmapFormat4(sub []byte) map[uint16]uint16 {
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

// TTSymbolicCmap finds a platform-3/encoding-0 (Windows symbol) or
// platform-1/encoding-0 (Mac) cmap subtable, preferring the Windows variant.
func TTSymbolicCmap(tables map[string][]byte) []byte {
	cmap := tables["cmap"]
	if len(cmap) < 4 {
		return nil
	}
	n := int(binary.BigEndian.Uint16(cmap[2:4]))
	var mac []byte
	for i := range n {
		rec := cmap[4+i*8:]
		if len(rec) < 8 {
			break
		}
		platform := binary.BigEndian.Uint16(rec[0:2])
		encoding := binary.BigEndian.Uint16(rec[2:4])
		offset := int(binary.BigEndian.Uint32(rec[4:8]))
		if offset+2 > len(cmap) {
			continue
		}
		if platform == 3 && encoding == 0 {
			return cmap[offset:]
		}
		if platform == 1 && encoding == 0 && mac == nil {
			mac = cmap[offset:]
		}
	}
	return mac
}

// ParseCmapFormat0 decodes a format-0 (byte array) cmap subtable into a code→GID map.
func ParseCmapFormat0(sub []byte) map[uint16]uint16 {
	if len(sub) < 262 || binary.BigEndian.Uint16(sub[0:2]) != 0 {
		return nil
	}
	result := map[uint16]uint16{}
	for i := 0; i < 256; i++ {
		if gid := uint16(sub[6+i]); gid != 0 {
			result[uint16(i)] = gid
		}
	}
	return result
}

// ParseCmapFormat6 decodes a format-6 (trimmed table) cmap subtable into a code→GID map.
func ParseCmapFormat6(sub []byte) map[uint16]uint16 {
	if len(sub) < 10 || binary.BigEndian.Uint16(sub[0:2]) != 6 {
		return nil
	}
	firstCode := int(binary.BigEndian.Uint16(sub[6:8]))
	count := int(binary.BigEndian.Uint16(sub[8:10]))
	if 10+count*2 > len(sub) {
		return nil
	}
	result := map[uint16]uint16{}
	for i := 0; i < count; i++ {
		if gid := binary.BigEndian.Uint16(sub[10+i*2:]); gid != 0 {
			result[uint16(firstCode+i)] = gid
		}
	}
	return result
}

// ParseCmapSubtable decodes a cmap subtable of format 0, 4, or 6.
func ParseCmapSubtable(sub []byte) map[uint16]uint16 {
	if len(sub) < 2 {
		return nil
	}
	switch binary.BigEndian.Uint16(sub[0:2]) {
	case 0:
		return ParseCmapFormat0(sub)
	case 4:
		return ParseCmapFormat4(sub)
	case 6:
		return ParseCmapFormat6(sub)
	}
	return nil
}

// WinAnsiGlyphName maps WinAnsiEncoding character codes 0–255 to Adobe glyph names.
// Empty string means the code is undefined (.notdef) in WinAnsiEncoding.
var WinAnsiGlyphName [256]string

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
		WinAnsiGlyphName[32+i] = n
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
		WinAnsiGlyphName[cc] = n
	}
}

// WinAnsiToUnicode maps WinAnsiEncoding character codes 0–255 to Unicode.
// For codes 0x20-0x7E and 0xA0-0xFF, the mapping equals the code point.
// Codes 0x80-0x9F use the Windows-1252 / PDF WinAnsiEncoding table.
var WinAnsiToUnicode [256]uint16

func init() {
	for i := 0x20; i <= 0x7E; i++ {
		WinAnsiToUnicode[i] = uint16(i)
	}
	for i := 0xA0; i <= 0xFF; i++ {
		WinAnsiToUnicode[i] = uint16(i)
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
		WinAnsiToUnicode[cc] = u
	}
}

// GlyphNameUnicode maps Adobe glyph names to Unicode codepoints, covering
// WinAnsiEncoding's repertoire plus supplemental names (fi/fl ligatures, etc.).
var GlyphNameUnicode map[string]uint16

var uniGlyphNameRe = regexp.MustCompile(`^uni([0-9A-Fa-f]{4})$`)

func init() {
	GlyphNameUnicode = make(map[string]uint16, 256)
	for cc, name := range WinAnsiGlyphName {
		if name != "" {
			if _, ok := GlyphNameUnicode[name]; !ok {
				GlyphNameUnicode[name] = WinAnsiToUnicode[cc]
			}
		}
	}
	for name, u := range map[string]uint16{
		"quoteright": 0x2019, "quoteleft": 0x2018, "fraction": 0x2044,
		"fi": 0xFB01, "fl": 0xFB02, "dotaccent": 0x02D9, "ring": 0x02DA,
		"hungarumlaut": 0x02DD, "ogonek": 0x02DB, "caron": 0x02C7,
		"Lslash": 0x0141, "lslash": 0x0142, "dotlessi": 0x0131,
		"florin": 0x0192, "breve": 0x02D8, "acute": 0x00B4, "macron": 0x00AF,
	} {
		if _, ok := GlyphNameUnicode[name]; !ok {
			GlyphNameUnicode[name] = u
		}
	}
}

// GlyphNameToUnicode resolves an Adobe glyph name to a Unicode codepoint using
// GlyphNameUnicode and the "uniXXXX" naming convention.
func GlyphNameToUnicode(name string) (uint16, bool) {
	if u, ok := GlyphNameUnicode[name]; ok {
		return u, true
	}
	if m := uniGlyphNameRe.FindStringSubmatch(name); m != nil {
		if v, err := strconv.ParseUint(m[1], 16, 32); err == nil {
			return uint16(v), true
		}
	}
	return 0, false
}

// SimpleFontCodeToUnicode resolves a simple font's /Encoding (a name or dict
// with optional /Differences) to a 256-entry code→Unicode table.
func SimpleFontCodeToUnicode(enc pdf.PDFValue) [256]uint16 {
	var table [256]uint16
	applyBase := func(name string) {
		switch name {
		case "WinAnsiEncoding":
			table = WinAnsiToUnicode
		default: // MacRomanEncoding, StandardEncoding, or unspecified
			for cc, n := range StandardEncoding {
				if n != "" {
					if u, ok := GlyphNameToUnicode(n); ok {
						table[cc] = u
					}
				}
			}
		}
	}
	switch e := enc.(type) {
	case pdf.PDFName:
		applyBase(e.Value)
	case pdf.PDFDict:
		base, _ := e.Entries["BaseEncoding"].(pdf.PDFName)
		applyBase(base.Value)
		if diffs, ok := e.Entries["Differences"].(pdf.PDFArray); ok {
			code := 0
			for _, item := range diffs {
				switch d := item.(type) {
				case pdf.PDFInteger:
					code = int(d)
				case pdf.PDFName:
					if code >= 0 && code < 256 {
						if u, ok := GlyphNameToUnicode(d.Value); ok {
							table[code] = u
						} else {
							table[code] = 0
						}
					}
					code++
				}
			}
		}
	default:
		applyBase("WinAnsiEncoding")
	}
	return table
}

// validateType1SubsetCoverage verifies that every used character code maps to
// a glyph name present in the font's CharSet (6.3.5).
func ValidateType1SubsetCoverage(obj pdf.PDFValue, v pdf.PDFDict, desc pdf.PDFDict, firstChar, lastChar int, widths pdf.PDFArray, ctx *ValidationContext) {
	charSetVal, ok := desc.Entries["CharSet"]
	if !ok {
		return // CharSet absence is caught by a separate check
	}
	var charSetStr string
	switch cs := charSetVal.(type) {
	case pdf.PDFString:
		charSetStr = cs.Value
	default:
		return
	}

	if charSetStr == "" {
		ctx.Report(pdf.Checks.Font.Type1SubsetCharSet, obj, "Type 1 subset font descriptor has an empty CharSet")
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
	case pdf.PDFName:
		switch enc.Value {
		case "WinAnsiEncoding":
			glyphNames = WinAnsiGlyphName
		default:
			return
		}
	case pdf.PDFDict:
		// Custom encoding: start from BaseEncoding, then apply Differences.
		base, _ := enc.Entries["BaseEncoding"].(pdf.PDFName)
		switch base.Value {
		case "WinAnsiEncoding":
			glyphNames = WinAnsiGlyphName
		}
		if diffs, ok := enc.Entries["Differences"].(pdf.PDFArray); ok {
			code := 0
			for _, item := range diffs {
				switch d := item.(type) {
				case pdf.PDFInteger:
					code = int(d)
				case pdf.PDFName:
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
			ctx.Report(pdf.Checks.Font.SubsetGlyphCoverage, obj, fmt.Sprintf("character code %d maps to glyph /%s which is not defined in the embedded font subset (CharSet)", cc, glyph))
			return false
		}
		return true
	}

	// Prefer codes actually shown in content streams (a missing glyph is
	// sometimes given width 0 as a placeholder, hiding the violation);
	// fall back to non-zero-width codes if usage info is unavailable.
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
		case pdf.PDFInteger:
			width = int(wv)
		case pdf.PDFReal:
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

// ParseCIDWidths decodes a CID font W array into (CID, pdfWidth-in-1/1000-em) pairs.
// W format: [c1 [w1 w2 ...] c2 c3 w ...]
func ParseCIDWidths(w pdf.PDFArray) [][2]int {
	var pairs [][2]int
	i := 0
	intV := func(v pdf.PDFValue) (int, bool) {
		switch x := v.(type) {
		case pdf.PDFInteger:
			return int(x), true
		case pdf.PDFReal:
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
		if arr, ok2 := w[i].(pdf.PDFArray); ok2 {
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

// CFFTopDict holds the Top DICT operands relevant to CID-keyed subset
// validation (6.3.5).
type CFFTopDict struct {
	CSOffset      int // CharStrings INDEX offset, -1 if not found
	CharsetOffset int // Charset table offset, -1 if not found / predefined
	IsCIDKeyed    bool
}

// ParseCFFTopDict parses a CFF binary stream's Top DICT and returns the
// operands needed to validate CID coverage.
func ParseCFFTopDict(cff []byte) (td CFFTopDict, ok bool) {
	td.CSOffset = -1
	td.CharsetOffset = -1
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
				td.IsCIDKeyed = true
			}
			stack = nil
			i += 2
		default: // single-byte operator
			switch {
			case b == 17 && len(stack) > 0: // CharStrings
				td.CSOffset = stack[0]
			case b == 15 && len(stack) > 0: // charset
				td.CharsetOffset = stack[0]
			}
			stack = nil
			i++
		}
	}
	return td, true
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

// parseCFFCharStringLengths returns the byte length of each entry in the
// CharStrings INDEX at the given offset, or nil on parse failure.
func parseCFFCharStringLengths(cff []byte, CSOffset int) []int {
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

// cffStandardStrings is the CFF specification's fixed table of predefined
// SIDs (Appendix A); SIDs >= len(cffStandardStrings) index into a font's own
// String INDEX instead.
var cffStandardStrings = [...]string{
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
	if sid >= 0 && sid < len(cffStandardStrings) {
		return cffStandardStrings[sid]
	}
	if idx := sid - len(cffStandardStrings); idx >= 0 && idx < len(customStrings) {
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

// validateCIDCFFSubset checks that all CIDs referenced in the W array are
// defined in the embedded CFF program (6.3.5). CIDs only used to declare a
// width, never shown, are exempt when usage info is available.
func ValidateCIDCFFSubset(obj pdf.PDFValue, ff pdf.PDFDict, w pdf.PDFArray, ctx *ValidationContext) {
	data, err := ctx.decodeStreamCached(ff)
	if err != nil {
		return
	}
	td, ok := ParseCFFTopDict(data)
	if !ok || td.CSOffset < 0 || td.CSOffset+2 > len(data) {
		return
	}
	csCount := int(binary.BigEndian.Uint16(data[td.CSOffset : td.CSOffset+2]))

	var used map[int]bool
	var knownUsage bool
	if desc, ok := obj.(pdf.PDFDict); ok {
		used, knownUsage = ctx.usedCIDsFor(desc)
	}

	// CID-keyed CFFs remap glyph IDs to CIDs via the Charset table (GID and
	// CID differ); non-CID-keyed CFFs use the GID directly as the CID.
	if td.IsCIDKeyed {
		cids := ParseCFFCharsetCIDs(data, td.CharsetOffset, csCount)
		if cids == nil {
			return
		}
		gidOfCID := make(map[int]int, len(cids))
		for gid, c := range cids {
			gidOfCID[c] = gid
		}
		// A bare/near-empty CharString (e.g. a single "endchar" byte, no
		// hsbw/width) is functionally undefined despite the charset entry.
		lens := parseCFFCharStringLengths(data, td.CSOffset)
		for _, pair := range ParseCIDWidths(w) {
			cid := pair[0]
			if knownUsage && !used[cid] {
				continue
			}
			gid, ok := gidOfCID[cid]
			if !ok || (lens != nil && gid < len(lens) && lens[gid] <= 1) {
				ctx.Report(pdf.Checks.Font.SubsetGlyphCoverage, obj, fmt.Sprintf("CID %d referenced in font W array is not defined in CFF charset", cid))
				return
			}
		}
		return
	}

	for _, pair := range ParseCIDWidths(w) {
		cid := pair[0]
		if knownUsage && !used[cid] {
			continue
		}
		if cid >= csCount {
			ctx.Report(pdf.Checks.Font.SubsetGlyphCoverage, obj, fmt.Sprintf("CID %d referenced in font W array is not defined in CFF CharStrings (count=%d)", cid, csCount))
			return
		}
	}
}

// validateCIDSetBitmap checks that the FontDescriptor's CIDSet bitmap marks
// every CID that has a glyph in the embedded CID-keyed CFF program (6.3.5/3).
// Each byte covers 8 CIDs, MSB first: bit j of byte i is CID i*8+j.
func validateCIDSetBitmap(obj pdf.PDFValue, desc pdf.PDFDict, ff pdf.PDFDict, ctx *ValidationContext) {
	cidSet, ok := desc.Entries["CIDSet"].(pdf.PDFDict)
	if !ok || !cidSet.HasStream {
		return
	}
	bitmap, err := ctx.decodeStreamCached(cidSet)
	if err != nil {
		return
	}
	data, err := ctx.decodeStreamCached(ff)
	if err != nil {
		return
	}
	td, ok := ParseCFFTopDict(data)
	if !ok || !td.IsCIDKeyed || td.CSOffset < 0 || td.CSOffset+2 > len(data) {
		return
	}
	csCount := int(binary.BigEndian.Uint16(data[td.CSOffset : td.CSOffset+2]))
	cids := ParseCFFCharsetCIDs(data, td.CharsetOffset, csCount)
	if cids == nil {
		return
	}
	for _, cid := range cids {
		byteIdx, bitIdx := cid/8, 7-cid%8
		if byteIdx >= len(bitmap) || bitmap[byteIdx]&(1<<bitIdx) == 0 {
			ctx.Report(pdf.Checks.Font.CIDSubsetCIDSet, obj, fmt.Sprintf("CIDSet does not list CID %d, which has a glyph in the embedded font program", cid))
			return
		}
	}
}

// validateCIDTrueTypeSubset checks that all CIDs referenced in the W array
// are present in the embedded TrueType program (6.3.5). Width-only CIDs that
// are never shown are exempt when usage info is available.
func ValidateCIDTrueTypeSubset(obj pdf.PDFValue, ff pdf.PDFDict, w pdf.PDFArray, ctx *ValidationContext) {
	data, err := ctx.decodeStreamCached(ff)
	if err != nil {
		return
	}
	tables, ok := ParseSfnt(data)
	if !ok {
		return
	}
	var used map[int]bool
	var knownUsage bool
	if desc, ok := obj.(pdf.PDFDict); ok {
		used, knownUsage = ctx.usedCIDsFor(desc)
	}
	glyphPresent := TTGlyphPresent(tables)
	for _, pair := range ParseCIDWidths(w) {
		cid := pair[0]
		if knownUsage && !used[cid] {
			continue
		}
		if !glyphPresent(cid) {
			ctx.Report(pdf.Checks.Font.SubsetGlyphCoverage, obj, fmt.Sprintf("CID %d referenced in font W array has no glyph in embedded program", cid))
			return
		}
	}
}

// validateCIDTrueTypeMetrics checks that CID advance widths in W match the
// embedded TrueType hmtx table (6.3.6).
func validateCIDTrueTypeMetrics(obj pdf.PDFValue, ff pdf.PDFDict, w pdf.PDFArray, ctx *ValidationContext) {
	data, err := ctx.decodeStreamCached(ff)
	if err != nil {
		return
	}
	tables, ok := ParseSfnt(data)
	if !ok {
		return
	}
	for _, pair := range ParseCIDWidths(w) {
		cid, pdfWidth := pair[0], pair[1]
		fontWidth := TTAdvanceWidth(tables, cid)
		if fontWidth < 0 {
			continue
		}
		// Allow ±1 rounding tolerance.
		if pdf.AbsInt(fontWidth-pdfWidth) > 1 {
			ctx.Report(pdf.Checks.Font.AdvanceWidthMismatch, obj, fmt.Sprintf("CID %d: PDF width %d ≠ font hmtx width %d",
				cid, pdfWidth, fontWidth))
			return
		}
	}
}

// validateSimpleTrueTypeSubset checks that all referenced character codes have
// a corresponding glyph in the embedded TrueType program (6.3.5).
func ValidateSimpleTrueTypeSubset(obj pdf.PDFValue, ff pdf.PDFDict, firstChar, lastChar int, widths pdf.PDFArray, ctx *ValidationContext) {
	data, err := ctx.decodeStreamCached(ff)
	if err != nil {
		return
	}
	tables, ok := ParseSfnt(data)
	if !ok {
		return
	}

	// Count non-zero widths entries (character codes the PDF actually uses).
	required := 0
	for _, w := range widths {
		if wi, ok2 := w.(pdf.PDFInteger); ok2 && wi > 0 {
			required++
		} else if wr, ok2 := w.(pdf.PDFReal); ok2 && wr > 0 {
			required++
		}
	}
	if required == 0 {
		return
	}

	// Build encoding-aware code→unicode table from the font's /Encoding.
	var enc pdf.PDFValue
	if fontDict, ok2 := obj.(pdf.PDFDict); ok2 {
		enc = fontDict.Entries["Encoding"]
	}
	codeToUnicode := SimpleFontCodeToUnicode(enc)

	winCmap := TTWindowsBMPCmap(tables)
	var winGIDMap map[uint16]uint16
	if winCmap != nil {
		winGIDMap = ParseCmapFormat4(winCmap)
	}
	symGIDMap := ParseCmapSubtable(TTSymbolicCmap(tables))
	numGlyphs := TTNumGlyphs(tables)

	// codeToGID resolves a char code to a GID; returns (gid, true) when known.
	codeToGID := func(cc int) (int, bool) {
		if u := codeToUnicode[cc]; u != 0 && winGIDMap != nil {
			gid, exists := winGIDMap[u]
			if exists {
				return int(gid), true
			}
			// Unicode is known but not in (3,1) cmap: glyph definitively absent.
			return 0, true
		}
		// Fall back to symbolic (3,0)/(1,0) cmap with raw code or PUA offset.
		if symGIDMap != nil {
			for _, candidate := range [2]uint16{uint16(cc) | 0xF000, uint16(cc)} {
				if gid, exists := symGIDMap[candidate]; exists {
					return int(gid), true
				}
			}
		}
		return 0, false // Cannot determine GID — skip.
	}

	checkCode := func(cc int) bool {
		gid, known := codeToGID(cc)
		if !known {
			return true
		}
		if gid == 0 || (numGlyphs > 0 && gid >= numGlyphs) {
			u := codeToUnicode[cc]
			ctx.Report(pdf.Checks.Font.SubsetGlyphCoverage, obj, fmt.Sprintf("character code %d (U+%04X) has no glyph in embedded font program", cc, u))
			return false
		}
		return true
	}

	// Prefer codes actually shown in content streams; fall back to
	// non-zero-width codes if usage info is unavailable.
	if fontDict, ok := obj.(pdf.PDFDict); ok {
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
		case pdf.PDFInteger:
			isNonZero = wv > 0
		case pdf.PDFReal:
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
func validateSimpleTrueTypeMetrics(obj pdf.PDFValue, ff pdf.PDFDict, firstChar int, widths pdf.PDFArray, ctx *ValidationContext) {
	data, err := ctx.decodeStreamCached(ff)
	if err != nil {
		return
	}
	tables, ok := ParseSfnt(data)
	if !ok {
		return
	}

	var enc pdf.PDFValue
	if fontDict, ok2 := obj.(pdf.PDFDict); ok2 {
		enc = fontDict.Entries["Encoding"]
	}
	codeToUnicode := SimpleFontCodeToUnicode(enc)

	winCmap := TTWindowsBMPCmap(tables)
	var winGIDMap map[uint16]uint16
	if winCmap != nil {
		winGIDMap = ParseCmapFormat4(winCmap)
	}
	symGIDMap := ParseCmapSubtable(TTSymbolicCmap(tables))

	codeToGID := func(cc int) (int, bool) {
		if u := codeToUnicode[cc]; u != 0 && winGIDMap != nil {
			gid, exists := winGIDMap[u]
			if exists {
				return int(gid), true
			}
			return 0, false // Missing from cmap — width cannot be verified.
		}
		if symGIDMap != nil {
			for _, candidate := range [2]uint16{uint16(cc) | 0xF000, uint16(cc)} {
				if gid, exists := symGIDMap[candidate]; exists {
					return int(gid), true
				}
			}
		}
		return 0, false
	}

	for i, w := range widths {
		var pdfWidth int
		switch wv := w.(type) {
		case pdf.PDFInteger:
			pdfWidth = int(wv)
		case pdf.PDFReal:
			pdfWidth = int(wv)
		default:
			continue
		}
		if pdfWidth == 0 {
			continue
		}
		gid, known := codeToGID(firstChar + i)
		if !known {
			continue
		}
		fontWidth := TTAdvanceWidth(tables, gid)
		if fontWidth < 0 {
			continue
		}
		if pdf.AbsInt(fontWidth-pdfWidth) > 1 {
			ctx.Report(pdf.Checks.Font.AdvanceWidthMismatch, obj, fmt.Sprintf("character code %d: PDF width %d ≠ font hmtx width %d",
				firstChar+i, pdfWidth, fontWidth))
			return
		}
	}
}

// ParseSfnt parses an sfnt (TrueType/OpenType) table directory, returning each
// table's bytes. The second result is false if the data is not a valid sfnt.
func ParseSfnt(data []byte) (map[string][]byte, bool) {
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
func fontProgramValid(ctx *ValidationContext, stream pdf.PDFDict, key string) bool {
	data, err := ctx.decodeStreamCached(stream)
	if err != nil || len(data) == 0 {
		return false
	}
	switch key {
	case "FontFile2":
		_, ok := ParseSfnt(data)
		return ok
	case "FontFile3":
		if len(data) >= 4 && data[0] == 1 { // CFF header, major version 1
			return true
		}
		_, ok := ParseSfnt(data) // OpenType-wrapped CFF
		return ok
	case "FontFile":
		// Type 1: the clear-text portion begins with a PostScript marker.
		return bytes.HasPrefix(data, []byte("%!"))
	}
	return true
}

// validateFontProgram flags a damaged embedded font program (6.3.2).
func ValidateFontProgram(obj pdf.PDFValue, desc pdf.PDFDict, name string, ctx *ValidationContext) {
	for _, key := range []string{"FontFile", "FontFile2", "FontFile3"} {
		ff, ok := desc.Entries[key].(pdf.PDFDict)
		if !ok {
			continue
		}
		if !fontProgramValid(ctx, ff, key) {
			ctx.Report(pdf.Checks.Font.InvalidProgram, obj, fmt.Sprintf("embedded font program for %s is damaged", name))
		}
	}
}

// trueTypeCmapSubtables returns the number of cmap subtables in an embedded
// TrueType font, and whether it could be determined.
func trueTypeCmapSubtables(ctx *ValidationContext, desc pdf.PDFDict) (int, bool) {
	ff, ok := desc.Entries["FontFile2"].(pdf.PDFDict)
	if !ok {
		return 0, false
	}
	data, err := ctx.decodeStreamCached(ff)
	if err != nil {
		return 0, false
	}
	tables, ok := ParseSfnt(data)
	if !ok {
		return 0, false
	}
	cmap, ok := tables["cmap"]
	if !ok || len(cmap) < 4 {
		return 0, false
	}
	return int(binary.BigEndian.Uint16(cmap[2:4])), true
}

// StandardEncoding maps character codes 0–255 to PostScript Standard Encoding
// glyph names. Empty string means the code is undefined (.notdef).
var StandardEncoding = [256]string{
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

// DecryptType1Block decrypts a Type1 font binary block using the given seed key.
// It skips the first 4 bytes (random seed) and returns the plaintext.
func DecryptType1Block(data []byte, seedKey uint16) []byte {
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

// Type1CharStringRe matches entries in a decrypted Type1 CharStrings dict.
var Type1CharStringRe = regexp.MustCompile(`/(\S+) (\d+) RD `)

// Type1CharStringsSection decrypts a Type1 program's eexec section and
// returns the decrypted bytes starting at "/CharStrings", or nil if absent.
// binStart is the byte offset of the first encrypted byte after "eexec".
func Type1CharStringsSection(fontData []byte, binStart int) []byte {
	if binStart <= 0 || binStart >= len(fontData) {
		return nil
	}
	plain := DecryptType1Block(fontData[binStart:], 55665)
	csIdx := bytes.Index(plain, []byte("/CharStrings"))
	if csIdx < 0 {
		return nil
	}
	return plain[csIdx:]
}

// extractType1GlyphWidths decrypts the eexec binary section of a Type1 font
// program and returns glyph name → advance width.
// binStart is the byte offset of the first encrypted byte after the "eexec" keyword.
func extractType1GlyphWidths(fontData []byte, binStart int) map[string]int {
	cs := Type1CharStringsSection(fontData, binStart)
	if cs == nil {
		return nil
	}
	result := map[string]int{}
	matches := Type1CharStringRe.FindAllSubmatchIndex(cs, -1)
	for _, m := range matches {
		name := string(cs[m[2]:m[3]])
		n, _ := strconv.Atoi(string(cs[m[4]:m[5]]))
		if n <= 0 || m[1]+n > len(cs) {
			continue
		}
		dec := DecryptType1Block(cs[m[1]:m[1]+n], 4330)
		if w, ok := parseType1AdvanceWidth(dec); ok {
			result[name] = w
		}
	}
	return result
}

// Type1GlyphNames returns the glyph names defined in a Type1 program's
// CharStrings dict (for CharSet synthesis, 6.3.5), regardless of whether
// each charstring's advance width can be parsed -- unlike
// extractType1GlyphWidths, a glyph with an unparseable width is still a
// real glyph that belongs in CharSet.
func Type1GlyphNames(fontData []byte) []string {
	cs := Type1CharStringsSection(fontData, Type1EexecBinStart(fontData))
	if cs == nil {
		return nil
	}
	var names []string
	for _, m := range Type1CharStringRe.FindAllSubmatchIndex(cs, -1) {
		names = append(names, string(cs[m[2]:m[3]]))
	}
	return names
}

// type1EncodingRe finds the built-in Encoding name in a Type1 clear-text section.
var type1EncodingRe = regexp.MustCompile(`/Encoding\s+(\w+)\s+def`)

// validateType1Metrics checks that PDF Widths entries match advance widths in
// the embedded Type1 font program (6.3.6).
func validateType1Metrics(obj pdf.PDFValue, ff pdf.PDFDict, firstChar int, widths pdf.PDFArray, pdfEncoding string, ctx *ValidationContext) {
	fontData, err := ctx.decodeStreamCached(ff)
	if err != nil || len(fontData) == 0 {
		return
	}

	enc, ok := Type1EncodingTable(fontData, pdfEncoding)
	if !ok {
		return
	}

	glyphWidths := Type1GlyphWidths(fontData)
	if len(glyphWidths) == 0 {
		return
	}

	for i, w := range widths {
		var pdfWidth int
		switch wv := w.(type) {
		case pdf.PDFInteger:
			pdfWidth = int(wv)
		case pdf.PDFReal:
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
		if pdf.AbsInt(pdfWidth-csWidth) > 1 {
			ctx.Report(pdf.Checks.Font.AdvanceWidthMismatch, obj, fmt.Sprintf("character code %d (/%s): PDF width %d ≠ Type1 advance width %d",
				cc, glyph, pdfWidth, csWidth))
			return
		}
	}
}

// Type1EncodingTable resolves the code->glyph-name table for a Type1 program,
// preferring the PDF-declared encoding and falling back to the font's own
// /Encoding declaration in the clear-text section before "eexec". ok is false
// for an encoding this package doesn't model.
func Type1EncodingTable(fontData []byte, pdfEncoding string) (enc [256]string, ok bool) {
	encName := pdfEncoding
	if encName == "" {
		eexecIdx := bytes.Index(fontData, []byte("eexec"))
		textPart := fontData
		if eexecIdx > 0 {
			textPart = fontData[:eexecIdx]
		}
		if m := type1EncodingRe.FindSubmatch(textPart); m != nil {
			encName = string(m[1])
		}
	}
	switch encName {
	case "StandardEncoding":
		return StandardEncoding, true
	case "WinAnsiEncoding":
		return WinAnsiGlyphName, true
	default:
		return enc, false
	}
}

// Type1EexecBinStart returns the byte offset of the first encrypted byte
// after a Type1 program's "eexec" keyword and its trailing whitespace, or -1
// if "eexec" is absent.
func Type1EexecBinStart(fontData []byte) int {
	eexecIdx := bytes.Index(fontData, []byte("eexec"))
	if eexecIdx < 0 {
		return -1
	}
	binStart := eexecIdx + 5
	for binStart < len(fontData) && (fontData[binStart] == '\n' || fontData[binStart] == '\r' || fontData[binStart] == ' ') {
		binStart++
	}
	return binStart
}

// Type1GlyphWidths locates the eexec-encrypted section of a Type1 font
// program and returns its glyph name -> advance width map (in 1/1000 em).
func Type1GlyphWidths(fontData []byte) map[string]int {
	return extractType1GlyphWidths(fontData, Type1EexecBinStart(fontData))
}

var WmodeRe = regexp.MustCompile(`/WMode\s+(\d+)\s+def`)

// validateCMapWMode flags an embedded CMap whose dictionary WMode disagrees with
// the WMode declared in its stream (6.3.3.3).
func validateCMapWMode(obj pdf.PDFValue, cmap pdf.PDFDict, ctx *ValidationContext) {
	if !cmap.HasStream {
		return
	}
	dictWMode, ok := cmap.Entries["WMode"].(pdf.PDFInteger)
	if !ok {
		return
	}
	data, err := ctx.decodeStreamCached(cmap)
	if err != nil {
		return
	}
	m := WmodeRe.FindSubmatch(data)
	if m == nil {
		return
	}
	streamWMode, _ := strconv.Atoi(string(m[1]))
	if int(dictWMode) != streamWMode {
		ctx.Report(pdf.Checks.Font.CMapWModeInconsistent, obj, "WMode in CMap dictionary and stream are inconsistent")
	}
}
