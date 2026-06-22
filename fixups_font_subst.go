package pdfrab

import (
	_ "embed"
	"encoding/binary"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// This file registers fixers that re-embed or substitute a bundled
// Liberation face (assets/fonts/, already pulled in for Phase 8's appearance
// streams) for fonts the converter cannot otherwise make conformant:
// SubsetGlyphCoverage/InvalidProgram (a genuinely missing or damaged glyph
// program -- the only fix is a different, complete program) and
// CIDNotEmbedded (the program is simply absent). This changes glyph shapes,
// but only for documents already non-conformant for that font, and always
// runs (per project decision -- see conversion-plan.md Phase 10) since a
// substituted-but-conformant font beats a non-conformant one. The embedded
// substitute is subset to the document's actual glyph usage via
// subsetTrueType (fonttool_subset.go) but deliberately not subset-tagged
// (BaseFont keeps no "ABCDEF+" prefix), the same trick buildAppearanceFont
// (fixups_appearance_font.go) uses to keep SubsetGlyphCoverage/
// AdvanceWidthMismatch from ever applying to the substitute itself.
//
// CMapNotEmbedded is deliberately NOT claimed here: it only fires for a
// non-Identity, non-embedded named CMap (e.g. "UniJIS-UCS2-H"), and
// recovering what that CMap actually mapped would need Adobe's CMap
// resource files, which gopdfrab does not bundle -- there is no tractable
// fix, so it stays residual (see ResidualCategory).

func init() {
	registerFixer(fontSubstitutionFixer{})
	registerFixer(trueTypeEncodingFixer{})
}

// --- Bundled substitute faces ---

//go:embed assets/fonts/LiberationSans-Regular.ttf
var libSansRegular []byte

//go:embed assets/fonts/LiberationSans-Bold.ttf
var libSansBold []byte

//go:embed assets/fonts/LiberationSans-Italic.ttf
var libSansItalic []byte

//go:embed assets/fonts/LiberationSans-BoldItalic.ttf
var libSansBoldItalic []byte

//go:embed assets/fonts/LiberationSerif-Regular.ttf
var libSerifRegular []byte

//go:embed assets/fonts/LiberationSerif-Bold.ttf
var libSerifBold []byte

//go:embed assets/fonts/LiberationSerif-Italic.ttf
var libSerifItalic []byte

//go:embed assets/fonts/LiberationSerif-BoldItalic.ttf
var libSerifBoldItalic []byte

//go:embed assets/fonts/LiberationMono-Regular.ttf
var libMonoRegular []byte

//go:embed assets/fonts/LiberationMono-Bold.ttf
var libMonoBold []byte

//go:embed assets/fonts/LiberationMono-Italic.ttf
var libMonoItalic []byte

//go:embed assets/fonts/LiberationMono-BoldItalic.ttf
var libMonoBoldItalic []byte

// liberationFace describes a chosen substitute face: its raw program bytes
// plus the style facts needed to rebuild a FontDescriptor that matches it.
type liberationFace struct {
	data                            []byte
	serif, fixedPitch, italic, bold bool
}

// pickLiberationFace chooses a bundled Liberation style to substitute for a
// non-conformant font, from its FontDescriptor flags/weight and BaseFont
// name (descriptors are sometimes incomplete, so the name is a fallback).
func pickLiberationFace(desc PDFDict, baseFont string) liberationFace {
	flags := 0
	if f, ok := desc.Entries["Flags"].(PDFInteger); ok {
		flags = int(f)
	}
	lower := strings.ToLower(baseFont)
	serif := flags&0x2 != 0 || strings.Contains(lower, "times") || strings.Contains(lower, "serif") ||
		strings.Contains(lower, "georgia") || strings.Contains(lower, "garamond") || strings.Contains(lower, "minion")
	fixedPitch := flags&0x1 != 0 || strings.Contains(lower, "courier") || strings.Contains(lower, "mono") ||
		strings.Contains(lower, "consol")
	italic := flags&0x40 != 0 || strings.Contains(lower, "italic") || strings.Contains(lower, "oblique")
	bold := flags&0x40000 != 0 || strings.Contains(lower, "bold")
	if fw, ok := desc.Entries["FontWeight"].(PDFInteger); ok && fw >= 600 {
		bold = true
	}

	pick := func(regular, b, i, bi []byte) []byte {
		switch {
		case bold && italic:
			return bi
		case bold:
			return b
		case italic:
			return i
		default:
			return regular
		}
	}
	var data []byte
	switch {
	case fixedPitch:
		data = pick(libMonoRegular, libMonoBold, libMonoItalic, libMonoBoldItalic)
	case serif:
		data = pick(libSerifRegular, libSerifBold, libSerifItalic, libSerifBoldItalic)
	default:
		data = pick(libSansRegular, libSansBold, libSansItalic, libSansBoldItalic)
	}
	return liberationFace{data: data, serif: serif, fixedPitch: fixedPitch, italic: italic, bold: bold}
}

// substituteFlags builds a FontDescriptor /Flags value for a substituted
// font: always non-symbolic, since substitution always resolves glyphs via
// WinAnsi/Unicode rather than a symbolic cmap.
func substituteFlags(face liberationFace) PDFInteger {
	flags := 32
	if face.serif {
		flags |= 0x2
	}
	if face.fixedPitch {
		flags |= 0x1
	}
	if face.italic {
		flags |= 0x40
	}
	return PDFInteger(flags)
}

func stemVFor(bold bool) PDFInteger {
	if bold {
		return 120
	}
	return 80
}

func italicAngleFor(italic bool) PDFInteger {
	if italic {
		return -12
	}
	return 0
}

// ttScaledAscentDescent reads hhea's ascender/descender, scaled to PDF's
// 1000-unit em like ttAdvanceWidth, for a FontDescriptor's Ascent/Descent.
func ttScaledAscentDescent(tables map[string][]byte) (ascent, descent int) {
	hhea := tables["hhea"]
	head := tables["head"]
	if len(hhea) < 8 || len(head) < 20 {
		return 0, 0
	}
	upm := int(binary.BigEndian.Uint16(head[18:20]))
	if upm == 0 {
		return 0, 0
	}
	a := int(int16(binary.BigEndian.Uint16(hhea[4:6])))
	d := int(int16(binary.BigEndian.Uint16(hhea[6:8])))
	return a * 1000 / upm, d * 1000 / upm
}

// ttScaledCapHeight reads OS/2's sCapHeight scaled to PDF's 1000-unit em,
// falling back when the field isn't present (OS/2 version < 2).
func ttScaledCapHeight(tables map[string][]byte, fallback int) int {
	os2 := tables["OS/2"]
	head := tables["head"]
	if len(os2) < 90 || len(head) < 20 {
		return fallback
	}
	upm := int(binary.BigEndian.Uint16(head[18:20]))
	if upm == 0 {
		return fallback
	}
	return int(int16(binary.BigEndian.Uint16(os2[88:90]))) * 1000 / upm
}

// --- Simple font (Type1/MMType1/TrueType) substitution ---

// glyphNameUnicode maps common Adobe glyph names to Unicode, covering
// WinAnsiEncoding's repertoire plus the handful of StandardEncoding names it
// doesn't share -- enough for the overwhelming majority of real-world
// /Differences entries. Built from the project's existing winAnsiGlyphName/
// winAnsiToUnicode tables (checks_font_program.go) rather than duplicating
// them by hand.
var glyphNameUnicode map[string]uint16

func init() {
	glyphNameUnicode = make(map[string]uint16, 256)
	for cc, name := range winAnsiGlyphName {
		if name != "" {
			glyphNameUnicode[name] = winAnsiToUnicode[cc]
		}
	}
	for name, u := range map[string]uint16{
		"quoteright": 0x2019, "quoteleft": 0x2018, "fraction": 0x2044,
		"fi": 0xFB01, "fl": 0xFB02, "dotaccent": 0x02D9, "ring": 0x02DA,
		"hungarumlaut": 0x02DD, "ogonek": 0x02DB, "caron": 0x02C7,
		"Lslash": 0x0141, "lslash": 0x0142, "dotlessi": 0x0131,
		"florin": 0x0192, "breve": 0x02D8, "acute": 0x00B4, "macron": 0x00AF,
	} {
		if _, ok := glyphNameUnicode[name]; !ok {
			glyphNameUnicode[name] = u
		}
	}
}

// uniGlyphNameRe matches the Adobe "uniXXXX" glyph-name convention for an
// otherwise-unlisted name's Unicode value.
var uniGlyphNameRe = regexp.MustCompile(`^uni([0-9A-Fa-f]{4})$`)

func glyphNameToUnicode(name string) (uint16, bool) {
	if u, ok := glyphNameUnicode[name]; ok {
		return u, true
	}
	if m := uniGlyphNameRe.FindStringSubmatch(name); m != nil {
		if v, err := strconv.ParseUint(m[1], 16, 32); err == nil {
			return uint16(v), true
		}
	}
	return 0, false
}

// simpleFontCodeToUnicode resolves a simple font's effective encoding (a
// base encoding name, or a /Differences dict layered over one) to a
// code->Unicode table, mirroring the resolution validateType1SubsetCoverage
// (checks_font_program.go) already does for CharSet checking.
func simpleFontCodeToUnicode(enc PDFValue) [256]uint16 {
	var table [256]uint16
	applyBase := func(name string) {
		switch name {
		case "WinAnsiEncoding":
			table = winAnsiToUnicode
		default: // MacRomanEncoding, StandardEncoding, or unspecified
			for cc, n := range standardEncoding {
				if n != "" {
					if u, ok := glyphNameToUnicode(n); ok {
						table[cc] = u
					}
				}
			}
		}
	}
	switch e := enc.(type) {
	case PDFName:
		applyBase(e.Value)
	case PDFDict:
		base, _ := e.Entries["BaseEncoding"].(PDFName)
		applyBase(base.Value)
		if diffs, ok := e.Entries["Differences"].(PDFArray); ok {
			code := 0
			for _, item := range diffs {
				switch d := item.(type) {
				case PDFInteger:
					code = int(d)
				case PDFName:
					if code >= 0 && code < 256 {
						u, ok := glyphNameToUnicode(d.Value)
						if ok {
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

// simpleFontUsedUnicodes resolves the Unicode values a simple font dict
// actually needs to render: the codes ctx recorded as shown, or every
// nonzero-width code in [FirstChar,LastChar] if usage wasn't tracked.
func simpleFontUsedUnicodes(d PDFDict, usedCodes map[uintptr]map[int]bool, codeToUnicode [256]uint16) []uint16 {
	var codes map[int]bool
	if usedCodes != nil {
		codes = usedCodes[pdfValuePointer(d.Entries)]
	}
	seen := map[uint16]bool{}
	var result []uint16
	add := func(cc int) {
		if cc < 0 || cc > 255 {
			return
		}
		if u := codeToUnicode[cc]; u != 0 && !seen[u] {
			seen[u] = true
			result = append(result, u)
		}
	}
	if codes != nil {
		for cc := range codes {
			add(cc)
		}
		return result
	}
	firstChar, _ := d.Entries["FirstChar"].(PDFInteger)
	widths, _ := d.Entries["Widths"].(PDFArray)
	for i, w := range widths {
		if n, ok := pdfNumberToInt(w); ok && n > 0 {
			add(int(firstChar) + i)
		}
	}
	return result
}

// simpleFontNeedsSubstitution reports whether d currently violates one of
// SimpleNotEmbedded/InvalidProgram/SubsetGlyphCoverage, by calling the exact
// same detection the corresponding check uses (checks_font.go/
// checks_font_program.go) against a throwaway ValidationContext -- so
// substitution only ever replaces a font that was genuinely flagged, never
// one that's already fine.
func simpleFontNeedsSubstitution(d, desc PDFDict, usedCodes map[uintptr]map[int]bool) bool {
	if !hasEmbeddedProgram(desc, "FontFile", "FontFile2", "FontFile3") {
		return true
	}
	baseFont, _ := d.Entries["BaseFont"].(PDFName)
	check := &ValidationContext{UsedCharCodes: usedCodes}
	validateFontProgram(d, desc, baseFont.Value, check)
	if len(check.errs) > 0 {
		return true
	}

	firstChar, fcOK := d.Entries["FirstChar"].(PDFInteger)
	lastChar, lcOK := d.Entries["LastChar"].(PDFInteger)
	widths, wOK := d.Entries["Widths"].(PDFArray)
	if !fcOK || !lcOK || !wOK {
		return false
	}
	subtype, _ := d.Entries["Subtype"].(PDFName)
	subset := subsetTagRe.MatchString(baseFont.Value)
	if !subset {
		return false
	}
	switch subtype.Value {
	case "TrueType":
		if ff, ok := desc.Entries["FontFile2"].(PDFDict); ok {
			validateSimpleTrueTypeSubset(d, ff, int(firstChar), int(lastChar), widths, check)
		}
	case "Type1", "MMType1":
		if desc.Entries["CharSet"] != nil {
			validateType1SubsetCoverage(d, d, desc, int(firstChar), int(lastChar), widths, check)
		}
	}
	return len(check.errs) > 0
}

// substituteSimpleFont rebuilds d in place as a non-symbolic TrueType font
// embedding a subsetted bundled Liberation face, preserving FirstChar/
// LastChar/Encoding so existing content-stream codes keep working --
// rendering now resolves through the substitute's own cmap instead of the
// original (missing or damaged) program.
func substituteSimpleFont(d PDFDict, usedCodes map[uintptr]map[int]bool) bool {
	desc, ok := d.Entries["FontDescriptor"].(PDFDict)
	if !ok || desc.Entries == nil {
		return false
	}
	if !simpleFontNeedsSubstitution(d, desc, usedCodes) {
		return false
	}

	codeToUnicode := simpleFontCodeToUnicode(d.Entries["Encoding"])
	unicodes := simpleFontUsedUnicodes(d, usedCodes, codeToUnicode)
	if len(unicodes) == 0 {
		return false
	}

	baseFont, _ := d.Entries["BaseFont"].(PDFName)
	face := pickLiberationFace(desc, baseFont.Value)
	subset, err := subsetTrueType(face.data, unicodes)
	if err != nil {
		return false
	}
	tables, ok := parseSfnt(subset)
	if !ok {
		return false
	}
	cmap := parseCmapFormat4(ttWindowsBMPCmap(tables))

	firstChar, _ := d.Entries["FirstChar"].(PDFInteger)
	lastChar, _ := d.Entries["LastChar"].(PDFInteger)
	if lastChar < firstChar {
		firstChar, lastChar = 0, 255
	}
	widths := make(PDFArray, int(lastChar-firstChar)+1)
	for i := range widths {
		w := 0
		if cc := int(firstChar) + i; cc >= 0 && cc < 256 {
			if u := codeToUnicode[cc]; u != 0 {
				if gid, ok := cmap[u]; ok {
					if aw := ttAdvanceWidth(tables, int(gid)); aw >= 0 {
						w = aw
					}
				}
			}
		}
		widths[i] = PDFInteger(w)
	}

	applySubstituteDescriptor(desc, tables, subset, face)
	d.Entries["Subtype"] = PDFName{Value: "TrueType"}
	d.Entries["FirstChar"] = PDFInteger(firstChar)
	d.Entries["LastChar"] = PDFInteger(lastChar)
	d.Entries["Widths"] = widths
	switch d.Entries["Encoding"].(type) {
	case PDFName, PDFDict:
		// keep the existing encoding/Differences -- codes are unchanged
	default:
		d.Entries["Encoding"] = PDFName{Value: "WinAnsiEncoding"}
	}
	return true
}

// applySubstituteDescriptor rewrites desc's program and metrics to describe
// the freshly-built substitute program, shared by the simple- and CID-font
// substitution paths.
func applySubstituteDescriptor(desc PDFDict, tables map[string][]byte, program []byte, face liberationFace) {
	fontFile := NewPDFDict()
	fontFile.Entries["Length1"] = PDFInteger(len(program))
	fontFile.HasStream = true
	fontFile.RawStream = program
	MarkStreamDirty(&fontFile)

	for _, k := range []string{"FontFile", "FontFile2", "FontFile3"} {
		delete(desc.Entries, k)
	}
	desc.Entries["FontFile2"] = fontFile
	desc.Entries["FontBBox"] = ttScaledBBox(tables)
	desc.Entries["Flags"] = substituteFlags(face)
	ascent, descent := ttScaledAscentDescent(tables)
	desc.Entries["Ascent"] = PDFInteger(ascent)
	desc.Entries["Descent"] = PDFInteger(descent)
	desc.Entries["CapHeight"] = PDFInteger(ttScaledCapHeight(tables, ascent))
	desc.Entries["StemV"] = stemVFor(face.bold)
	desc.Entries["ItalicAngle"] = italicAngleFor(face.italic)
	desc.Entries["MissingWidth"] = PDFInteger(0)
}

// --- CID font (CIDFontType0/CIDFontType2) substitution ---

// cidFontSubstitutionEligible reports whether a Type0 font carries a
// directly-recoverable code/CID->Unicode mapping: Identity-H/V encoding
// (content code == CID) plus a parseable /ToUnicode CMap. Any other
// encoding -- a predefined non-Identity CMap or a custom embedded one --
// has no way to recover intended glyphs without resources gopdfrab doesn't
// bundle, so substitution is skipped rather than guessed at.
func cidFontSubstitutionEligible(type0 PDFDict) (map[int]uint16, bool) {
	enc, _ := type0.Entries["Encoding"].(PDFName)
	if enc.Value != "Identity-H" && enc.Value != "Identity-V" {
		return nil, false
	}
	toUni, ok := type0.Entries["ToUnicode"].(PDFDict)
	if !ok || !toUni.HasStream {
		return nil, false
	}
	data, err := decodeStream(toUni)
	if err != nil {
		return nil, false
	}
	cidToUnicode := parseToUnicodeCMap(data)
	if len(cidToUnicode) == 0 {
		return nil, false
	}
	return cidToUnicode, true
}

// cidFontNeedsSubstitution mirrors simpleFontNeedsSubstitution for composite
// fonts, reusing validateFontProgram/validateCIDTrueTypeSubset/
// validateCIDCFFSubset (checks_font.go/checks_font_program.go) against a
// throwaway ValidationContext.
func cidFontNeedsSubstitution(cid, desc PDFDict, usedCIDs map[uintptr]map[int]bool) bool {
	if !hasEmbeddedProgram(desc, "FontFile2", "FontFile3") {
		return true
	}
	baseFont, _ := cid.Entries["BaseFont"].(PDFName)
	check := &ValidationContext{UsedCIDs: usedCIDs}
	validateFontProgram(cid, desc, baseFont.Value, check)
	if len(check.errs) > 0 {
		return true
	}

	if !subsetTagRe.MatchString(baseFont.Value) {
		return false
	}
	w, _ := cid.Entries["W"].(PDFArray)
	subtype, _ := cid.Entries["Subtype"].(PDFName)
	switch subtype.Value {
	case "CIDFontType2":
		if ff, ok := desc.Entries["FontFile2"].(PDFDict); ok {
			validateCIDTrueTypeSubset(cid, ff, w, check)
		}
	case "CIDFontType0":
		if ff, ok := desc.Entries["FontFile3"].(PDFDict); ok {
			validateCIDCFFSubset(cid, ff, w, check)
		}
	}
	return len(check.errs) > 0
}

// substituteCIDFont rebuilds a Type0 font's descendant in place as a
// CIDFontType2 embedding a subsetted bundled Liberation face. The substitute
// is built so each used CID lands at the identical glyph ID
// (subsetTrueTypeForCID, fonttool_subset.go) and /CIDToGIDMap is set to
// /Identity, rather than indirecting through a CIDToGIDMap stream: the
// verifier's CID TrueType checks (validateCIDTrueTypeSubset/Metrics,
// checks_font_program.go) look up a CID as a glyph ID directly with no
// indirection, so only a CID==GID substitute can pass them.
func substituteCIDFont(type0, cid PDFDict, usedCIDs map[uintptr]map[int]bool) bool {
	desc, ok := cid.Entries["FontDescriptor"].(PDFDict)
	if !ok || desc.Entries == nil {
		return false
	}
	if !cidFontNeedsSubstitution(cid, desc, usedCIDs) {
		return false
	}
	cidToUnicode, ok := cidFontSubstitutionEligible(type0)
	if !ok {
		return false
	}

	var used map[int]bool
	if usedCIDs != nil {
		used = usedCIDs[pdfValuePointer(cid.Entries)]
	}
	cids := map[int]bool{}
	if used != nil {
		for c := range used {
			cids[c] = true
		}
	} else {
		w, _ := cid.Entries["W"].(PDFArray)
		for _, pair := range parseCIDWidths(w) {
			cids[pair[0]] = true
		}
	}

	baseFont, _ := cid.Entries["BaseFont"].(PDFName)
	face := pickLiberationFace(desc, baseFont.Value)
	faceTables, ok := parseSfnt(face.data)
	if !ok {
		return false
	}
	faceCmap := parseCmapFormat4(ttWindowsBMPCmap(faceTables))
	if faceCmap == nil {
		return false
	}

	// Only CIDs whose Unicode resolves to a real glyph in the substitute
	// face get a target GID -- the rest become empty placeholder glyphs
	// that subsetTrueTypeForCID never assigns, so they must be excluded here
	// too rather than left for it to silently skip.
	targetGID := map[uint16]int{}
	cidForUnicode := map[uint16]int{}
	for c := range cids {
		u, ok := cidToUnicode[c]
		if !ok {
			continue
		}
		if _, ok := faceCmap[u]; !ok {
			continue
		}
		targetGID[u] = c
		cidForUnicode[u] = c
	}
	if len(targetGID) == 0 {
		return false
	}

	subset, err := subsetTrueTypeForCID(face.data, targetGID)
	if err != nil {
		return false
	}
	tables, ok := parseSfnt(subset)
	if !ok {
		return false
	}

	var widthPairs [][2]int
	for _, c := range cidForUnicode {
		if aw := ttAdvanceWidth(tables, c); aw >= 0 {
			widthPairs = append(widthPairs, [2]int{c, aw})
		}
	}
	if len(widthPairs) == 0 {
		return false
	}
	sort.Slice(widthPairs, func(i, j int) bool { return widthPairs[i][0] < widthPairs[j][0] })

	applySubstituteDescriptor(desc, tables, subset, face)
	cid.Entries["Subtype"] = PDFName{Value: "CIDFontType2"}
	cid.Entries["CIDToGIDMap"] = PDFName{Value: "Identity"}
	cid.Entries["W"] = buildCIDWidthsArray(widthPairs)
	return true
}

// --- /ToUnicode CMap parsing ---

var (
	toUnicodeBfCharBlockRe  = regexp.MustCompile(`(?s)beginbfchar(.*?)endbfchar`)
	toUnicodeBfRangeBlockRe = regexp.MustCompile(`(?s)beginbfrange(.*?)endbfrange`)
	toUnicodeBfCharEntryRe  = regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`)
	toUnicodeBfRangeEntryRe = regexp.MustCompile(`(?s)<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>\s*(?:<([0-9A-Fa-f]+)>|\[([^\]]*)\])`)
	toUnicodeHexTokenRe     = regexp.MustCompile(`<([0-9A-Fa-f]+)>`)
)

// hexToUnicode takes the first UTF-16 code unit of a ToUnicode destination
// hex string as its Unicode value, ignoring surrogate pairs/ligatures --
// the bundled Liberation substitute only needs BMP coverage.
func hexToUnicode(hex string) (uint16, bool) {
	b := decodePDFHexStringBytes(hex)
	if len(b) < 2 {
		return 0, false
	}
	return binary.BigEndian.Uint16(b[:2]), true
}

// parseToUnicodeCMap extracts a code->Unicode mapping from a /ToUnicode
// CMap stream's bfchar/bfrange blocks (PDF 32000-1, 9.10.3). For Identity-H/V
// fonts the "code" here is the CID directly (content text-showing codes are
// 2-byte and equal the CID), which is what makes substitution's CID->Unicode
// recovery possible.
func parseToUnicodeCMap(data []byte) map[int]uint16 {
	result := map[int]uint16{}
	s := string(data)
	for _, block := range toUnicodeBfCharBlockRe.FindAllStringSubmatch(s, -1) {
		for _, m := range toUnicodeBfCharEntryRe.FindAllStringSubmatch(block[1], -1) {
			code, err := strconv.ParseInt(m[1], 16, 32)
			if err != nil {
				continue
			}
			if u, ok := hexToUnicode(m[2]); ok {
				result[int(code)] = u
			}
		}
	}
	for _, block := range toUnicodeBfRangeBlockRe.FindAllStringSubmatch(s, -1) {
		for _, m := range toUnicodeBfRangeEntryRe.FindAllStringSubmatch(block[1], -1) {
			lo, errLo := strconv.ParseInt(m[1], 16, 32)
			hi, errHi := strconv.ParseInt(m[2], 16, 32)
			if errLo != nil || errHi != nil || hi < lo {
				continue
			}
			if m[3] != "" {
				base, ok := hexToUnicode(m[3])
				if !ok {
					continue
				}
				for c := lo; c <= hi; c++ {
					result[int(c)] = base + uint16(c-lo)
				}
			} else if m[4] != "" {
				dsts := toUnicodeHexTokenRe.FindAllStringSubmatch(m[4], -1)
				for i, c := 0, lo; i < len(dsts) && c <= hi; i, c = i+1, c+1 {
					if u, ok := hexToUnicode(dsts[i][1]); ok {
						result[int(c)] = u
					}
				}
			}
		}
	}
	return result
}

// --- Fixer ---

// fontSubstitutionFixer remediates SubsetGlyphCoverage, SimpleNotEmbedded,
// CIDNotEmbedded and InvalidProgram by substituting a bundled Liberation
// face wherever a font's own program is missing, damaged, or doesn't cover
// a glyph it needs (see this file's header comment). SimpleNotEmbedded is
// claimed for completeness against stricter profiles, but the default
// PDFA_1B profile excuses it (profile.go), so it never actually reaches
// Convert's loop today.
type fontSubstitutionFixer struct{}

func (fontSubstitutionFixer) Applies(c Check) bool {
	switch c {
	case Checks.Font.SubsetGlyphCoverage, Checks.Font.SimpleNotEmbedded,
		Checks.Font.CIDNotEmbedded, Checks.Font.InvalidProgram:
		return true
	}
	return false
}

func (fontSubstitutionFixer) Fix(trailer *PDFDict, issues []PDFError) (bool, error) {
	usageCtx := &ValidationContext{}
	_, _, usedCodes, usedCIDs := computeContentUsage(*trailer, usageCtx)

	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d PDFDict) {
		if (d.Entries["Type"] != PDFName{Value: "Font"}) {
			return
		}
		switch subtype, _ := d.Entries["Subtype"].(PDFName); subtype.Value {
		case "Type1", "MMType1", "TrueType":
			if substituteSimpleFont(d, usedCodes) {
				changed = true
			}
		case "Type0":
			if cid := descendantCIDFont(d); cid.Entries != nil {
				if substituteCIDFont(d, cid, usedCIDs) {
					changed = true
				}
			}
		}
	})
	return changed, nil
}

// trueTypeEncodingFixer remediates the 6.3.7 TrueType encoding checks on
// fonts that are otherwise embedded and fine: removing a stray /Encoding
// from a symbolic font (the spec default the viewer already falls back to,
// so this never changes glyph mapping), trimming a symbolic font's cmap to
// the single subtable 6.3.7 requires (via trimTrueTypeCmapToSingleSubtable,
// fonttool_subset.go -- glyf/loca/hmtx are left untouched, so glyph shapes
// are unaffected), and setting a non-symbolic font's /Encoding to
// WinAnsiEncoding when it names neither permitted encoding.
type trueTypeEncodingFixer struct{}

func (trueTypeEncodingFixer) Applies(c Check) bool {
	switch c {
	case Checks.Font.TrueTypeEncoding, Checks.Font.SymbolicTrueTypeEncoding, Checks.Font.SymbolicTrueTypeCmap:
		return true
	}
	return false
}

func (trueTypeEncodingFixer) Fix(trailer *PDFDict, issues []PDFError) (bool, error) {
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d PDFDict) {
		if (d.Entries["Type"] != PDFName{Value: "Font"}) {
			return
		}
		if (d.Entries["Subtype"] != PDFName{Value: "TrueType"}) {
			return
		}
		desc, ok := d.Entries["FontDescriptor"].(PDFDict)
		if !ok {
			return
		}
		flags := 0
		if f, ok := desc.Entries["Flags"].(PDFInteger); ok {
			flags = int(f)
		}
		if flags&4 != 0 { // symbolic
			if d.Entries["Encoding"] != nil {
				delete(d.Entries, "Encoding")
				changed = true
			}
			if trimSymbolicCmap(desc) {
				changed = true
			}
			return
		}
		if name, ok := d.Entries["Encoding"].(PDFName); !ok || (name.Value != "MacRomanEncoding" && name.Value != "WinAnsiEncoding") {
			d.Entries["Encoding"] = PDFName{Value: "WinAnsiEncoding"}
			changed = true
		}
	})
	return changed, nil
}

// trimSymbolicCmap reduces desc's embedded FontFile2's cmap to a single
// subtable in place, leaving glyph data untouched. Returns false (no-op) if
// the font isn't embedded, already has one subtable, or its surviving
// subtable isn't format 4 -- left as residual rather than risk a wrong
// rebuild (trimTrueTypeCmapToSingleSubtable's own constraint).
func trimSymbolicCmap(desc PDFDict) bool {
	ff, ok := desc.Entries["FontFile2"].(PDFDict)
	if !ok || !ff.HasStream {
		return false
	}
	data, err := decodeStream(ff)
	if err != nil {
		return false
	}
	if n, ok := trueTypeCmapSubtableCount(data); !ok || n == 1 {
		return false
	}
	trimmed, err := trimTrueTypeCmapToSingleSubtable(data)
	if err != nil {
		return false
	}
	ff.RawStream = trimmed
	MarkStreamDirty(&ff)
	desc.Entries["FontFile2"] = ff
	return true
}

func trueTypeCmapSubtableCount(data []byte) (int, bool) {
	tables, ok := parseSfnt(data)
	if !ok {
		return 0, false
	}
	cmap := tables["cmap"]
	if len(cmap) < 4 {
		return 0, false
	}
	return int(binary.BigEndian.Uint16(cmap[2:4])), true
}
