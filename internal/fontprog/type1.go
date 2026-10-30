package fontprog

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// DecryptType1Block decrypts a Type1 font binary block using the given seed key.
// It skips the first 4 bytes (random seed) and returns the plaintext.
func DecryptType1Block(data []byte, seedKey uint16) []byte {
	return DecryptType1(data, seedKey, 4)
}

// DecryptType1CharString decrypts one charstring, dropping the lenIV leading
// random bytes the program declares (Type 1 spec 7.1). The count is the
// program's own lenIV, including 0, so no bytes of the charstring are dropped.
func DecryptType1CharString(data []byte, lenIV int) []byte {
	return DecryptType1(data, 4330, lenIV)
}

// DecryptType1 runs the eexec cipher, discarding the first skip plaintext
// bytes.
func DecryptType1(data []byte, seedKey uint16, skip int) []byte {
	R := seedKey
	out := make([]byte, 0, len(data))
	for i, c := range data {
		p := byte(uint16(c) ^ (R >> 8))
		R = (uint16(c)+R)*52845 + 22719
		if i >= skip {
			out = append(out, p)
		}
	}
	return out
}

// type1LenIVRe captures a Private dict's /lenIV, the number of random bytes
// prepended to every charstring.
var type1LenIVRe = regexp.MustCompile(`/lenIV\s+(\d+)\s+def`)

// Type1LenIV reads /lenIV from a decrypted private section, defaulting to the
// 4 the spec prescribes when the program declares none.
func Type1LenIV(plain []byte) int {
	m := type1LenIVRe.FindSubmatch(plain)
	if m == nil {
		return 4
	}
	n, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return 4
	}
	return n
}

// Type1CharStringRe matches entries in a decrypted Type1 CharStrings dict.
// The operator that introduces the charstring bytes is whatever the Private
// dict named it, so both "RD" and "-|" are matched.
var Type1CharStringRe = regexp.MustCompile(`/(\S+) (\d+) (?:RD|-\|) `)

// type1SubrRe matches one entry of a decrypted Type1 Subrs array.
var type1SubrRe = regexp.MustCompile(`dup (\d+) (\d+) (?:RD|-\|) `)

// type1Sections decrypts a Type1 program's eexec section once and returns the
// bytes from "/CharStrings" on, the /lenIV its charstrings are padded with,
// and the decrypted Subrs indexed by subr number.
func type1Sections(fontData []byte, binStart int) (section []byte, lenIV int, subrs [][]byte) {
	if binStart <= 0 || binStart >= len(fontData) {
		return nil, 4, nil
	}
	plain := DecryptType1Block(fontData[binStart:], 55665)
	csIdx := bytes.Index(plain, []byte("/CharStrings"))
	if csIdx < 0 {
		return nil, 4, nil
	}
	// Both live in the Private dict, ahead of /CharStrings.
	private := plain[:csIdx]
	lenIV = Type1LenIV(private)
	return plain[csIdx:], lenIV, Type1SubrArray(private, lenIV)
}

// Type1SubrArray decrypts a Private dict's Subrs, indexed by subr number.
// Numbers are as the program writes them, so a sparse or out-of-order array
// leaves the gaps nil rather than shifting the rest.
func Type1SubrArray(private []byte, lenIV int) [][]byte {
	start := bytes.Index(private, []byte("/Subrs"))
	if start < 0 {
		return nil
	}
	var subrs [][]byte
	for _, m := range type1SubrRe.FindAllSubmatchIndex(private[start:], -1) {
		idx, err := strconv.Atoi(string(private[start+m[2] : start+m[3]]))
		n, err2 := strconv.Atoi(string(private[start+m[4] : start+m[5]]))
		if err != nil || err2 != nil || idx < 0 || idx > maxType1Subrs || n <= 0 || start+m[1]+n > len(private) {
			continue
		}
		for len(subrs) <= idx {
			subrs = append(subrs, nil)
		}
		subrs[idx] = DecryptType1CharString(private[start+m[1]:start+m[1]+n], lenIV)
	}
	return subrs
}

// maxType1Subrs bounds the array a program can claim, so a bogus subr number
// cannot make it allocate without limit.
const maxType1Subrs = 65535

// Type1Program is the result of one combined CharStrings scan of a Type1
// font program: every defined glyph name (a glyph with an unparseable width
// is still a real glyph that belongs in CharSet, 6.3.5), and glyph name →
// advance width for each charstring whose width parses (6.3.6).
type Type1Program struct {
	Names  []string
	Widths map[string]float64
}

// ExtractType1Program decrypts the eexec binary section of a Type1 font
// program once and extracts names and widths in a single CharStrings scan.
// binStart is the byte offset of the first encrypted byte after "eexec".
func ExtractType1Program(fontData []byte, binStart int) Type1Program {
	cs, lenIV, subrs := type1Sections(fontData, binStart)
	if cs == nil {
		return Type1Program{}
	}
	// A matrix we cannot read leaves the widths out: the names still belong in
	// CharSet (6.3.5), but there is no width to compare against (6.3.6).
	scale, scaleOK := Type1WidthScale(fontData)
	var p Type1Program
	p.Widths = map[string]float64{}
	for _, m := range Type1CharStringRe.FindAllSubmatchIndex(cs, -1) {
		name := string(cs[m[2]:m[3]])
		p.Names = append(p.Names, name)
		n, _ := strconv.Atoi(string(cs[m[4]:m[5]]))
		if n <= 0 || m[1]+n > len(cs) {
			continue
		}
		dec := DecryptType1CharString(cs[m[1]:m[1]+n], lenIV)
		if w, ok := type1AdvanceWidth(dec, subrs); ok && scaleOK {
			p.Widths[name] = scaleWidth(w, scale)
		}
	}
	return p
}

// Type1GlyphNames returns the glyph names defined in a Type1 program's
// CharStrings dict (for CharSet synthesis, 6.3.5).
func Type1GlyphNames(fontData []byte) []string {
	return ExtractType1Program(fontData, Type1EexecBinStart(fontData)).Names
}

// type1EncodingRe finds the built-in Encoding name in a Type1 clear-text section.
var type1EncodingRe = regexp.MustCompile(`/Encoding\s+(\w+)\s+def`)

// type1EncodingArrayRe matches one entry of a Type1 program's own /Encoding
// array, the "dup <code> /<name> put" form a font with a custom encoding uses
// instead of naming a standard one.
var type1EncodingArrayRe = regexp.MustCompile(`dup\s+(\d+)\s*/([^\s/(\[{]+)\s+put`)

// type1BuiltinEncodingArray reads a Type1 program's own /Encoding array from
// the clear-text section. ok is false when the program declares no array,
// which is the case for one naming a standard encoding instead.
func type1BuiltinEncodingArray(textPart []byte) (enc [256]string, ok bool) {
	start := bytes.Index(textPart, []byte("/Encoding"))
	if start < 0 {
		return enc, false
	}
	for _, m := range type1EncodingArrayRe.FindAllSubmatch(textPart[start:], -1) {
		code, err := strconv.Atoi(string(m[1]))
		if err != nil || code < 0 || code > 255 {
			continue
		}
		enc[code] = string(m[2])
		ok = true
	}
	return enc, ok
}

// type1FontMatrixRe captures a Type1 /FontMatrix's first two elements, the
// x-scale and the term that would tilt an advance off the horizontal.
var type1FontMatrixRe = regexp.MustCompile(`/FontMatrix\s*\[\s*([0-9.eE+-]+)\s+([0-9.eE+-]+)`)

// Type1WidthScale is cffWidthScale for a Type1 program, reading the FontMatrix
// out of the clear-text section before "eexec". A program declaring none is in
// the default 1/1000 em glyph space.
func Type1WidthScale(fontData []byte) (scale float64, ok bool) {
	textPart := fontData
	if i := bytes.Index(fontData, []byte("eexec")); i > 0 {
		textPart = fontData[:i]
	}
	m := type1FontMatrixRe.FindSubmatch(textPart)
	if m == nil {
		return 1, true
	}
	a, aErr := strconv.ParseFloat(string(m[1]), 64)
	b, bErr := strconv.ParseFloat(string(m[2]), 64)
	if aErr != nil || bErr != nil {
		return 0, false
	}
	return fontMatrixWidthScale(a, b)
}

// Type1GlyphNameTable resolves the code->glyph-name table for a Type1
// (FontFile) program, composing the font dict's /Encoding over the program's
// built-in encoding per ISO 32000-1 9.6.6.1/9.6.6.2: a name selects that base,
// a dictionary takes its /BaseEncoding when modelled and the built-in
// otherwise, with /Differences overlaid. ok is false when no base resolved.
//
// An /Encoding dictionary is handled as well as a name. Reading a dictionary as
// absent would ignore its /Differences and compare /Widths against the wrong
// glyphs.
func Type1GlyphNameTable(fontData []byte, encoding pdf.PDFValue) (glyphNames [256]string, ok bool) {
	builtin := func() ([256]string, bool) { return Type1EncodingTable(fontData, "") }

	switch enc := encoding.(type) {
	case pdf.PDFName:
		return Type1EncodingTable(fontData, enc.Value)
	case pdf.PDFDict:
		base, _ := enc.Entries.Get("BaseEncoding").(pdf.PDFName)
		if base.Value != "" {
			glyphNames, ok = namedEncodingTable(base.Value)
		} else {
			glyphNames, ok = builtin()
		}
		// Differences may name every code the font actually shows, so they
		// are applied even when no base could be resolved; ok stays false in
		// that case only if the base was required and missing.
		applyDifferences(&glyphNames, enc)
		if !ok {
			_, hasDiffs := enc.Entries.Get("Differences").(pdf.PDFArray)
			ok = hasDiffs
		}
		return glyphNames, ok
	default:
		return builtin()
	}
}

// Type1WidthTable is the Type1 (FontFile) counterpart to
// CFFAdvanceWidthsStats: it resolves the code->glyph-name table the 6.3.6 width
// comparison needs and reports why it gave up when it did. widths is the
// program's glyph-name -> advance width map (Type1GlyphWidths); encoding is the
// font dict's raw /Encoding entry.
func Type1WidthTable(fontData []byte, encoding pdf.PDFValue, widths map[string]float64) ([256]string, WidthStats) {
	enc, ok := Type1GlyphNameTable(fontData, encoding)
	if !ok {
		return enc, WidthStats{Skip: WidthSkipEncoding}
	}
	if _, ok := Type1WidthScale(fontData); !ok {
		return enc, WidthStats{Skip: WidthSkipFontMatrix}
	}
	if len(widths) == 0 {
		return enc, WidthStats{Skip: WidthSkipNoWidths}
	}
	return enc, WidthStats{GlyphsTotal: len(widths)}
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
		} else if enc, ok := type1BuiltinEncodingArray(textPart); ok {
			// A program with a custom encoding builds the array itself rather
			// than naming a standard one.
			return enc, true
		}
	}
	switch encName {
	case "StandardEncoding":
		return StandardEncoding, true
	case "WinAnsiEncoding":
		return WinAnsiGlyphName, true
	case "MacRomanEncoding":
		return MacRomanGlyphName, true
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
// program and returns its glyph name -> advance width map (in 1/1000 em; a
// declared FontMatrix is folded in, and an unreadable one yields no widths).
func Type1GlyphWidths(fontData []byte) map[string]float64 {
	return ExtractType1Program(fontData, Type1EexecBinStart(fontData)).Widths
}

// SplitCharSetNames splits a CharSet string like "/a/b/c" into ["a", "b", "c"],
// tolerating whitespace between entries.
func SplitCharSetNames(cs string) []string {
	var result []string
	for part := range strings.SplitSeq(cs, "/") {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}
