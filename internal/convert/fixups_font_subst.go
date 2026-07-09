package convert

import (
	_ "embed"
	"encoding/binary"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"

	"github.com/voidrab/gopdfrab/internal/writer"
)

// This file registers fixers that re-embed or substitute a bundled
// Liberation face for fonts the converter cannot otherwise make conformant.
func init() {
	registerFixer(fontSubstitutionFixer{})
	registerFixer(trueTypeEncodingFixer{})
}

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

//go:embed assets/fonts/NotoSansSymbols2-Regular.ttf
var notoSymbols2 []byte

//go:embed assets/fonts/NotoSansSymbols-VariableFont_wght.ttf
var notoSymbols []byte

type liberationFace struct {
	data                            []byte
	serif, fixedPitch, italic, bold bool
}

// pickLiberationFace chooses a bundled Liberation style to substitute for a
// non-conformant font, from its FontDescriptor flags/weight and BaseFont
// name (descriptors are sometimes incomplete, so the name is a fallback).
func pickLiberationFace(desc pdf.PDFDict, baseFont string) liberationFace {
	flags := 0
	if f, ok := desc.Entries["Flags"].(pdf.PDFInteger); ok {
		flags = int(f)
	}
	lower := strings.ToLower(baseFont)
	serif := flags&0x2 != 0 || strings.Contains(lower, "times") || strings.Contains(lower, "serif") ||
		strings.Contains(lower, "georgia") || strings.Contains(lower, "garamond") || strings.Contains(lower, "minion")
	fixedPitch := flags&0x1 != 0 || strings.Contains(lower, "courier") || strings.Contains(lower, "mono") ||
		strings.Contains(lower, "consol")
	italic := flags&0x40 != 0 || strings.Contains(lower, "italic") || strings.Contains(lower, "oblique")
	bold := flags&0x40000 != 0 || strings.Contains(lower, "bold")
	if fw, ok := desc.Entries["FontWeight"].(pdf.PDFInteger); ok && fw >= 600 {
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
func substituteFlags(face liberationFace) pdf.PDFInteger {
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
	return pdf.PDFInteger(flags)
}

func stemVFor(bold bool) pdf.PDFInteger {
	if bold {
		return 120
	}
	return 80
}

func italicAngleFor(italic bool) pdf.PDFInteger {
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

func simpleFontCodeToUnicode(enc pdf.PDFValue) [256]uint16 {
	return verify.SimpleFontCodeToUnicode(enc)
}

// simpleFontUsedUnicodes resolves the Unicode values a simple font dict
// actually needs to render.
func simpleFontUsedUnicodes(d pdf.PDFDict, usedCodes map[uintptr]map[int]bool, codeToUnicode [256]uint16) []uint16 {
	var codes map[int]bool
	if usedCodes != nil {
		codes = usedCodes[pdf.ValuePointer(d.Entries)]
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
	firstChar, _ := d.Entries["FirstChar"].(pdf.PDFInteger)
	widths, _ := d.Entries["Widths"].(pdf.PDFArray)
	if len(widths) > 0 {
		for i, w := range widths {
			if n, ok := pdf.PDFNumberToInt(w); ok && n > 0 {
				add(int(firstChar) + i)
			}
		}
		return result
	}
	// No tracked usage and no Widths (e.g. a standard Type1 in AcroForm/DR):
	// assume every character in the encoding may be needed.
	for cc := range codeToUnicode {
		add(cc)
	}
	return result
}

// hasSubstitutionIssue reports whether vctx recorded a violation that only a
// font substitution can repair; metadata defects (e.g. a stale CharSet) have
// cheaper fixers and must never destroy an embedded program.
func hasSubstitutionIssue(vctx *verify.ValidationContext) bool {
	for _, iss := range vctx.Issues() {
		if (fontSubstitutionFixer{}).Applies(iss.Check()) {
			return true
		}
	}
	return false
}

// simpleFontNeedsSubstitution reports whether d currently violates one of
// SimpleNotEmbedded/InvalidProgram/SubsetGlyphCoverage.
func simpleFontNeedsSubstitution(d, desc pdf.PDFDict, usedCodes map[uintptr]map[int]bool) bool {
	subtype, _ := d.Entries["Subtype"].(pdf.PDFName)
	if !verify.EmbeddedProgramMatchesSubtype(subtype.Value, desc) {
		return true
	}
	baseFont, _ := d.Entries["BaseFont"].(pdf.PDFName)
	vctx := &verify.ValidationContext{UsedCharCodes: usedCodes}
	verify.ValidateFontProgram(d, desc, baseFont.Value, vctx)
	if hasSubstitutionIssue(vctx) {
		return true
	}

	firstChar, fcOK := d.Entries["FirstChar"].(pdf.PDFInteger)
	lastChar, lcOK := d.Entries["LastChar"].(pdf.PDFInteger)
	widths, wOK := d.Entries["Widths"].(pdf.PDFArray)
	if !fcOK || !lcOK || !wOK {
		return false
	}
	subset := verify.SubsetTagRe.MatchString(baseFont.Value)
	if !subset {
		return false
	}
	switch subtype.Value {
	case "TrueType":
		if ff, ok := desc.Entries["FontFile2"].(pdf.PDFDict); ok {
			verify.ValidateSimpleTrueTypeSubset(d, ff, int(firstChar), int(lastChar), widths, vctx)
		}
	case "Type1", "MMType1":
		if desc.Entries["CharSet"] != nil {
			verify.ValidateType1SubsetCoverage(d, d, desc, int(firstChar), int(lastChar), widths, vctx)
		}
	}
	return hasSubstitutionIssue(vctx)
}

// substituteCoversUsage confirms every character code the font renders
// resolves to a real glyph in the substitute subset -- the same
// Encoding/Differences-based resolution external validators apply. A code the
// substitute cannot cover makes substitution destructive, so the caller must
// leave the font alone and let raster fallback handle its pages.
func substituteCoversUsage(d pdf.PDFDict, usedCodes map[uintptr]map[int]bool, codeToUnicode [256]uint16, cmap map[uint16]uint16, tables map[string][]byte) bool {
	covered := func(cc int) bool {
		if cc < 0 || cc > 255 {
			return true
		}
		u := codeToUnicode[cc]
		if u == 0 {
			return false
		}
		gid, ok := cmap[u]
		if !ok || gid == 0 {
			return false
		}
		return verify.TTAdvanceWidth(tables, int(gid)) >= 0
	}

	var codes map[int]bool
	if usedCodes != nil {
		codes = usedCodes[pdf.ValuePointer(d.Entries)]
	}
	if codes != nil {
		for cc := range codes {
			if !covered(cc) {
				return false
			}
		}
		return true
	}
	firstChar, _ := d.Entries["FirstChar"].(pdf.PDFInteger)
	widths, _ := d.Entries["Widths"].(pdf.PDFArray)
	for i, w := range widths {
		if n, ok := pdf.PDFNumberToInt(w); ok && n > 0 {
			if !covered(int(firstChar) + i) {
				return false
			}
		}
	}
	return true
}

// substituteSimpleFont rebuilds d in place as a non-symbolic TrueType font
// embedding a subsetted bundled Liberation face, preserving FirstChar/
// LastChar/Encoding so existing content-stream codes keep working.
func substituteSimpleFont(d pdf.PDFDict, usedCodes map[uintptr]map[int]bool, sharedDescs map[uintptr]bool, nextObjNum *int) bool {
	desc, ok := d.Entries["FontDescriptor"].(pdf.PDFDict)
	if !ok || desc.Entries == nil {
		return false
	}
	if !simpleFontNeedsSubstitution(d, desc, usedCodes) {
		return false
	}

	// The result is always a non-symbolic TrueType font, which 6.3.7 limits
	// to the MacRoman/WinAnsi encoding names. Deciding the final encoding
	// up front keeps subset, Widths, and coverage consistent with what the
	// font dictionary will actually declare.
	finalEnc := pdf.PDFValue(pdf.PDFName{Value: "WinAnsiEncoding"})
	if name, ok := d.Entries["Encoding"].(pdf.PDFName); ok &&
		(name.Value == "MacRomanEncoding" || name.Value == "WinAnsiEncoding") {
		finalEnc = name
	}
	codeToUnicode := simpleFontCodeToUnicode(finalEnc)
	// The substitute keeps the content-stream bytes, so every used code must
	// mean the same thing under the declared encoding as it originally did;
	// otherwise a symbolic substitute preserves the codes' meanings directly.
	origTable, baseKnown := originalSimpleFontCodeToUnicode(d)
	if !encodingRewritePreservesMeaning(d, usedCodes, origTable, codeToUnicode) {
		return substituteSimpleFontSymbolic(d, usedCodes, origTable, baseKnown, sharedDescs, nextObjNum)
	}
	unicodes := simpleFontUsedUnicodes(d, usedCodes, codeToUnicode)
	if len(unicodes) == 0 {
		return false
	}

	baseFont, _ := d.Entries["BaseFont"].(pdf.PDFName)
	face := pickLiberationFace(desc, baseFont.Value)
	subset, err := subsetTrueType(face.data, unicodes)
	if err != nil {
		return false
	}
	tables, ok := verify.ParseSfnt(subset)
	if !ok {
		return false
	}
	cmap := verify.ParseCmapFormat4(verify.TTWindowsBMPCmap(tables))

	if !substituteCoversUsage(d, usedCodes, codeToUnicode, cmap, tables) {
		return false
	}

	firstChar, fcOK := d.Entries["FirstChar"].(pdf.PDFInteger)
	lastChar, lcOK := d.Entries["LastChar"].(pdf.PDFInteger)
	if !fcOK || !lcOK || lastChar < firstChar {
		// Standard Type1 fonts in AcroForm/DR have no FirstChar/LastChar.
		firstChar, lastChar = 0, 255
	}
	widths := make(pdf.PDFArray, int(lastChar-firstChar)+1)
	for i := range widths {
		w := 0
		if cc := int(firstChar) + i; cc >= 0 && cc < 256 {
			if u := codeToUnicode[cc]; u != 0 {
				if gid, ok := cmap[u]; ok {
					if aw := verify.TTAdvanceWidth(tables, int(gid)); aw >= 0 {
						w = aw
					}
				}
			}
		}
		widths[i] = pdf.PDFInteger(w)
	}

	// A descriptor other font dicts still reference must not be rewritten
	// under them; give the substituted font its own copy.
	if sharedDescs[pdf.ValuePointer(desc.Entries)] {
		desc = cloneFontDescriptor(desc, nextObjNum)
		d.Entries["FontDescriptor"] = desc
	}

	newName := substituteBaseFontName(face, baseFont.Value)
	applySubstituteDescriptor(desc, tables, subset, face)
	desc.Entries["FontName"] = pdf.PDFName{Value: newName}
	d.Entries["BaseFont"] = pdf.PDFName{Value: newName}
	d.Entries["Subtype"] = pdf.PDFName{Value: "TrueType"}
	d.Entries["FirstChar"] = pdf.PDFInteger(firstChar)
	d.Entries["LastChar"] = pdf.PDFInteger(lastChar)
	d.Entries["Widths"] = widths
	d.Entries["Encoding"] = finalEnc
	return true
}

// symbolicSubsetCoversCodes confirms every code resolves to a real glyph with
// a usable advance width through the subset's own (3,0) cmap.
func symbolicSubsetCoversCodes(tables map[string][]byte, gidMap map[uint16]uint16, codeUnicode map[int]uint16) bool {
	for cc := range codeUnicode {
		gid, ok := gidMap[0xF000|uint16(cc)]
		if !ok || gid == 0 {
			return false
		}
		if verify.TTAdvanceWidth(tables, int(gid)) < 0 {
			return false
		}
	}
	return true
}

// substituteSimpleFontSymbolic rebuilds d as a symbolic TrueType font whose
// single (3,0) cmap maps the original character codes directly to the glyphs
// they meant, preserving untouched content-stream bytes when no
// MacRoman/WinAnsi name encoding can (6.3.7 forbids everything else).
func substituteSimpleFontSymbolic(d pdf.PDFDict, usedCodes map[uintptr]map[int]bool, origTable [256]uint16, baseKnown bool, sharedDescs map[uintptr]bool, nextObjNum *int) bool {
	desc, ok := d.Entries["FontDescriptor"].(pdf.PDFDict)
	if !ok || desc.Entries == nil {
		return false
	}
	codeUnicode := map[int]uint16{}
	known := forEachAssumedUsedCode(d, usedCodes, func(cc int) bool {
		u := origTable[cc]
		if u == 0 {
			// A zero entry under a known encoding rendered .notdef before and
			// keeps doing so; under an unknown one it could mean anything.
			return baseKnown
		}
		codeUnicode[cc] = u
		return true
	})
	if !known || len(codeUnicode) == 0 {
		return false
	}

	baseFont, _ := d.Entries["BaseFont"].(pdf.PDFName)
	libFace := pickLiberationFace(desc, baseFont.Value)
	var subset []byte
	var tables map[string][]byte
	var gidOf map[uint16]uint16
	family := ""
	for _, cand := range []struct {
		data   []byte
		family string
	}{
		{libFace.data, liberationFamilyName(libFace)},
		{notoSymbols2, "NotoSansSymbols2"},
		{notoSymbols, "NotoSansSymbols"},
	} {
		s, err := subsetTrueTypeSymbolic(cand.data, codeUnicode)
		if err != nil {
			continue
		}
		t, ok := verify.ParseSfnt(s)
		if !ok {
			continue
		}
		_, _, gm, ok := firstFormat4CmapSubtable(t)
		if !ok {
			continue
		}
		if !symbolicSubsetCoversCodes(t, gm, codeUnicode) {
			continue
		}
		subset, tables, gidOf, family = s, t, gm, cand.family
		break
	}
	if subset == nil {
		return false
	}

	minCode, maxCode := 255, 0
	for cc := range codeUnicode {
		if cc < minCode {
			minCode = cc
		}
		if cc > maxCode {
			maxCode = cc
		}
	}
	widths := make(pdf.PDFArray, maxCode-minCode+1)
	for i := range widths {
		w := 0
		if cc := minCode + i; codeUnicode[cc] != 0 {
			if gid, ok := gidOf[0xF000|uint16(cc)]; ok {
				if aw := verify.TTAdvanceWidth(tables, int(gid)); aw >= 0 {
					w = aw
				}
			}
		}
		widths[i] = pdf.PDFInteger(w)
	}

	if sharedDescs[pdf.ValuePointer(desc.Entries)] {
		desc = cloneFontDescriptor(desc, nextObjNum)
		d.Entries["FontDescriptor"] = desc
	}

	newName := substituteTaggedName(family, baseFont.Value)
	applySubstituteDescriptor(desc, tables, subset, libFace)
	// Symbolic flag set (and non-symbolic clear); no Encoding entry allowed.
	desc.Entries["Flags"] = pdf.PDFInteger(4)
	desc.Entries["FontName"] = pdf.PDFName{Value: newName}
	d.Entries["BaseFont"] = pdf.PDFName{Value: newName}
	d.Entries["Subtype"] = pdf.PDFName{Value: "TrueType"}
	d.Entries["FirstChar"] = pdf.PDFInteger(minCode)
	d.Entries["LastChar"] = pdf.PDFInteger(maxCode)
	d.Entries["Widths"] = widths
	delete(d.Entries, "Encoding")
	if toUni, ok := buildToUnicodeStream(codeUnicode); ok {
		d.Entries["ToUnicode"] = toUni
	}
	return true
}

// cloneFontDescriptor copies desc into a fresh indirect dict so it can be
// mutated without affecting other fonts referencing the original.
func cloneFontDescriptor(desc pdf.PDFDict, nextObjNum *int) pdf.PDFDict {
	clone := pdf.NewPDFDict()
	for k, v := range desc.Entries {
		if k == "_ref" || k == "_dirty" {
			continue
		}
		clone.Entries[k] = v
	}
	clone.Entries["_ref"] = pdf.PDFRef{ObjNum: *nextObjNum}
	*nextObjNum++
	return clone
}

// liberationFamilyName returns the PostScript family name of the bundled
// Liberation face matching face's style bits.
func liberationFamilyName(face liberationFace) string {
	family := "LiberationSans"
	switch {
	case face.fixedPitch:
		family = "LiberationMono"
	case face.serif:
		family = "LiberationSerif"
	}
	switch {
	case face.bold && face.italic:
		family += "-BoldItalic"
	case face.bold:
		family += "-Bold"
	case face.italic:
		family += "-Italic"
	}
	return family
}

// substituteTaggedName builds a substituted font's name: a deterministic
// subset tag derived from the original name plus the substitute family, so
// the misleading original family name is dropped.
func substituteTaggedName(family, original string) string {
	h := uint32(2166136261)
	for i := 0; i < len(original); i++ {
		h = (h ^ uint32(original[i])) * 16777619
	}
	tag := make([]byte, 6)
	for i := range tag {
		tag[i] = byte('A' + h%26)
		h /= 26
	}
	return string(tag) + "+" + family
}

func substituteBaseFontName(face liberationFace, original string) string {
	return substituteTaggedName(liberationFamilyName(face), original)
}

// buildToUnicodeStream builds a minimal bfchar-based /ToUnicode CMap stream
// for a simple font, so text extraction keeps working after a symbolic
// substitution removes the name encoding.
func buildToUnicodeStream(codeUnicode map[int]uint16) (pdf.PDFDict, bool) {
	codes := make([]int, 0, len(codeUnicode))
	for cc := range codeUnicode {
		codes = append(codes, cc)
	}
	sort.Ints(codes)

	var b strings.Builder
	b.WriteString("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n" +
		"/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def\n" +
		"/CMapName /Adobe-Identity-UCS def\n/CMapType 2 def\n" +
		"1 begincodespacerange\n<00> <FF>\nendcodespacerange\n")
	// bfchar blocks are limited to 100 entries each.
	for start := 0; start < len(codes); start += 100 {
		end := start + 100
		if end > len(codes) {
			end = len(codes)
		}
		fmt.Fprintf(&b, "%d beginbfchar\n", end-start)
		for _, cc := range codes[start:end] {
			fmt.Fprintf(&b, "<%02X> <%04X>\n", cc, codeUnicode[cc])
		}
		b.WriteString("endbfchar\n")
	}
	b.WriteString("endcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n")

	toUni := pdf.NewPDFDict()
	if err := writer.SetStreamFlate(&toUni, []byte(b.String())); err != nil {
		return pdf.PDFDict{}, false
	}
	return toUni, true
}

func applySubstituteDescriptor(desc pdf.PDFDict, tables map[string][]byte, program []byte, face liberationFace) {
	fontFile := pdf.NewPDFDict()
	fontFile.Entries["Length1"] = pdf.PDFInteger(len(program))
	if err := writer.SetStreamFlate(&fontFile, program); err != nil {
		return
	}

	for _, k := range []string{"FontFile", "FontFile2", "FontFile3", "CharSet", "FontFamily"} {
		delete(desc.Entries, k)
	}
	desc.Entries["Type"] = pdf.PDFName{Value: "FontDescriptor"}
	desc.Entries["FontFile2"] = fontFile
	desc.Entries["FontBBox"] = ttScaledBBox(tables)
	desc.Entries["Flags"] = substituteFlags(face)
	ascent, descent := ttScaledAscentDescent(tables)
	desc.Entries["Ascent"] = pdf.PDFInteger(ascent)
	desc.Entries["Descent"] = pdf.PDFInteger(descent)
	desc.Entries["CapHeight"] = pdf.PDFInteger(ttScaledCapHeight(tables, ascent))
	desc.Entries["StemV"] = stemVFor(face.bold)
	desc.Entries["ItalicAngle"] = italicAngleFor(face.italic)
	desc.Entries["MissingWidth"] = pdf.PDFInteger(0)
}

// cidFontSubstitutionEligible reports whether a Type0 font carries a
// directly-recoverable code/CID->Unicode mapping.
func cidFontSubstitutionEligible(type0 pdf.PDFDict) (map[int]uint16, bool) {
	enc, _ := type0.Entries["Encoding"].(pdf.PDFName)
	if enc.Value != "Identity-H" && enc.Value != "Identity-V" {
		return nil, false
	}
	toUni, ok := type0.Entries["ToUnicode"].(pdf.PDFDict)
	if !ok || !toUni.HasStream {
		return nil, false
	}
	data, err := pdf.DecodeStream(toUni)
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
// fonts.
func cidFontNeedsSubstitution(cid, desc pdf.PDFDict, usedCIDs map[uintptr]map[int]bool) bool {
	subtype, _ := cid.Entries["Subtype"].(pdf.PDFName)
	if !verify.EmbeddedProgramMatchesSubtype(subtype.Value, desc) {
		return true
	}
	baseFont, _ := cid.Entries["BaseFont"].(pdf.PDFName)
	vctx := &verify.ValidationContext{UsedCIDs: usedCIDs}
	verify.ValidateFontProgram(cid, desc, baseFont.Value, vctx)
	if hasSubstitutionIssue(vctx) {
		return true
	}

	if !verify.SubsetTagRe.MatchString(baseFont.Value) {
		return false
	}
	w, _ := cid.Entries["W"].(pdf.PDFArray)
	switch subtype.Value {
	case "CIDFontType2":
		if ff, ok := desc.Entries["FontFile2"].(pdf.PDFDict); ok {
			verify.ValidateCIDTrueTypeSubset(cid, ff, w, vctx)
		}
	case "CIDFontType0":
		if ff, ok := desc.Entries["FontFile3"].(pdf.PDFDict); ok {
			verify.ValidateCIDCFFSubset(cid, ff, w, vctx)
		}
	}
	return hasSubstitutionIssue(vctx)
}

// substituteCIDFont rebuilds a Type0 font's descendant in place as a
// CIDFontType2 embedding a subsetted bundled Liberation face.
func substituteCIDFont(type0, cid pdf.PDFDict, usedCIDs map[uintptr]map[int]bool, sharedDescs map[uintptr]bool, nextObjNum *int) bool {
	desc, ok := cid.Entries["FontDescriptor"].(pdf.PDFDict)
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
		used = usedCIDs[pdf.ValuePointer(cid.Entries)]
	}
	cids := map[int]bool{}
	if used != nil {
		for c := range used {
			cids[c] = true
		}
	} else {
		w, _ := cid.Entries["W"].(pdf.PDFArray)
		for _, pair := range verify.ParseCIDWidths(w) {
			cids[pair[0]] = true
		}
	}

	// Every rendered CID must survive the substitution; CID 0 (.notdef) needs
	// no mapping. A CID with no recoverable meaning would silently lose its
	// glyph, so refuse and leave the page to raster fallback.
	targetCIDs := map[uint16][]int{}
	for c := range cids {
		if c == 0 {
			continue
		}
		u, ok := cidToUnicode[c]
		if !ok {
			return false
		}
		targetCIDs[u] = append(targetCIDs[u], c)
	}
	if len(targetCIDs) == 0 {
		return false
	}

	// Prefer the style-matched Liberation face, falling back to the bundled
	// Noto symbol repertoires before giving the page to raster fallback.
	baseFont, _ := cid.Entries["BaseFont"].(pdf.PDFName)
	face := pickLiberationFace(desc, baseFont.Value)
	var faceData []byte
	family := ""
	for _, cand := range []struct {
		data   []byte
		family string
	}{
		{face.data, liberationFamilyName(face)},
		{notoSymbols2, "NotoSansSymbols2"},
		{notoSymbols, "NotoSansSymbols"},
	} {
		faceTables, ok := verify.ParseSfnt(cand.data)
		if !ok {
			continue
		}
		faceCmap := verify.ParseCmapFormat4(verify.TTWindowsBMPCmap(faceTables))
		if faceCmap == nil {
			continue
		}
		covered := true
		for u := range targetCIDs {
			if _, ok := faceCmap[u]; !ok {
				covered = false
				break
			}
		}
		if covered {
			faceData, family = cand.data, cand.family
			break
		}
	}
	if faceData == nil {
		return false
	}

	subset, err := subsetTrueTypeForCID(faceData, targetCIDs)
	if err != nil {
		return false
	}
	tables, ok := verify.ParseSfnt(subset)
	if !ok {
		return false
	}

	var widthPairs [][2]int
	for _, cidList := range targetCIDs {
		for _, c := range cidList {
			if aw := verify.TTAdvanceWidth(tables, c); aw >= 0 {
				widthPairs = append(widthPairs, [2]int{c, aw})
			}
		}
	}
	if len(widthPairs) == 0 {
		return false
	}
	sort.Slice(widthPairs, func(i, j int) bool { return widthPairs[i][0] < widthPairs[j][0] })

	if sharedDescs[pdf.ValuePointer(desc.Entries)] {
		desc = cloneFontDescriptor(desc, nextObjNum)
		cid.Entries["FontDescriptor"] = desc
	}

	newName := substituteTaggedName(family, baseFont.Value)
	applySubstituteDescriptor(desc, tables, subset, face)
	desc.Entries["FontName"] = pdf.PDFName{Value: newName}
	cid.Entries["BaseFont"] = pdf.PDFName{Value: newName}
	type0.Entries["BaseFont"] = pdf.PDFName{Value: newName}
	cid.Entries["Subtype"] = pdf.PDFName{Value: "CIDFontType2"}
	cid.Entries["CIDToGIDMap"] = pdf.PDFName{Value: "Identity"}
	cid.Entries["W"] = buildCIDWidthsArray(widthPairs)
	if ff, ok := desc.Entries["FontFile2"].(pdf.PDFDict); ok {
		delete(desc.Entries, "CIDSet")
		fixTrueTypeCIDSet(cid, desc, ff)
	}
	cid.Entries["DW"] = pdf.PDFInteger(0)
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
// hex string as its Unicode value, ignoring surrogate pairs/ligatures.
func hexToUnicode(hex string) (uint16, bool) {
	b := pdf.DecodePDFHexStringBytes(hex)
	if len(b) < 2 {
		return 0, false
	}
	return binary.BigEndian.Uint16(b[:2]), true
}

// parseToUnicodeCMap extracts a code->Unicode mapping from a /ToUnicode
// CMap stream's bfchar/bfrange blocks (PDF 32000-1, 9.10.3).
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

// fontSubstitutionFixer remediates SubsetGlyphCoverage, SimpleNotEmbedded,
// CIDNotEmbedded and InvalidProgram by substituting a bundled Liberation
// face wherever a font's own program is missing, damaged, or doesn't cover
// a glyph it needs.
type fontSubstitutionFixer struct{ doc *pdf.Reader }

func (fontSubstitutionFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Font.SubsetGlyphCoverage, pdf.Checks.Font.SimpleNotEmbedded,
		pdf.Checks.Font.CIDNotEmbedded, pdf.Checks.Font.InvalidProgram:
		return true
	}
	return false
}

func (f fontSubstitutionFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	// One prepass gathers everything the substitutions need: candidate font
	// dicts, descriptor sharing counts, and the highest object number.
	var simple, composite []pdf.PDFDict
	descCounts := map[uintptr]int{}
	maxRef := 0
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if ref, ok := d.Entries["_ref"].(pdf.PDFRef); ok && ref.ObjNum > maxRef {
			maxRef = ref.ObjNum
		}
		if (d.Entries["Type"] != pdf.PDFName{Value: "Font"}) {
			return
		}
		if desc, ok := d.Entries["FontDescriptor"].(pdf.PDFDict); ok && desc.Entries != nil {
			descCounts[pdf.ValuePointer(desc.Entries)]++
		}
		switch subtype, _ := d.Entries["Subtype"].(pdf.PDFName); subtype.Value {
		case "Type1", "MMType1", "TrueType":
			simple = append(simple, d)
		case "Type0":
			composite = append(composite, d)
		}
	})
	if len(simple) == 0 && len(composite) == 0 {
		return false, nil
	}

	usageCtx := verify.NewContext(f.doc)
	_, _, usedCodes, usedCIDs := verify.ComputeContentUsage(*trailer, usageCtx)
	sharedDescs := map[uintptr]bool{}
	for ptr, n := range descCounts {
		if n > 1 {
			sharedDescs[ptr] = true
		}
	}
	nextObjNum := maxRef + 1

	changed := false
	for _, d := range simple {
		// Standard Type1 fonts (e.g. in AcroForm/DR) have no FontDescriptor;
		// create a scratch one so substituteSimpleFont can proceed. Remove it
		// again if substitution turns out not to be needed -- leaving it
		// behind would plant an all-fields-missing descriptor where the PDF
		// legitimately had none.
		hadDescriptor := d.Entries["FontDescriptor"] != nil
		if !hadDescriptor {
			d.Entries["FontDescriptor"] = pdf.NewPDFDict()
		}
		if substituteSimpleFont(d, usedCodes, sharedDescs, &nextObjNum) {
			changed = true
		} else if !hadDescriptor {
			delete(d.Entries, "FontDescriptor")
		}
	}
	for _, d := range composite {
		if cid := verify.DescendantCIDFont(d); cid.Entries != nil {
			if substituteCIDFont(d, cid, usedCIDs, sharedDescs, &nextObjNum) {
				changed = true
			}
		}
	}
	return changed, nil
}

// trueTypeEncodingFixer remediates the 6.3.7 TrueType encoding checks.
type trueTypeEncodingFixer struct{ doc *pdf.Reader }

func (trueTypeEncodingFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Font.TrueTypeEncoding, pdf.Checks.Font.SymbolicTrueTypeEncoding, pdf.Checks.Font.SymbolicTrueTypeCmap:
		return true
	}
	return false
}

func (f trueTypeEncodingFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	changed := false
	var usedCodes map[uintptr]map[int]bool
	usageComputed := false
	usage := func() map[uintptr]map[int]bool {
		if !usageComputed {
			usageComputed = true
			_, _, usedCodes, _ = verify.ComputeContentUsage(*trailer, verify.NewContext(f.doc))
		}
		return usedCodes
	}
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if (d.Entries["Type"] != pdf.PDFName{Value: "Font"}) {
			return
		}
		if (d.Entries["Subtype"] != pdf.PDFName{Value: "TrueType"}) {
			return
		}
		desc, ok := d.Entries["FontDescriptor"].(pdf.PDFDict)
		if !ok {
			return
		}
		flags := 0
		if f, ok := desc.Entries["Flags"].(pdf.PDFInteger); ok {
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
		if name, ok := d.Entries["Encoding"].(pdf.PDFName); !ok || (name.Value != "MacRomanEncoding" && name.Value != "WinAnsiEncoding") {
			// The font keeps its program and content bytes, so the replacement
			// name encoding must preserve what every used code meant; when
			// neither does, leave the violation for the raster fallback.
			orig, _ := originalSimpleFontCodeToUnicode(d)
			for _, cand := range [...]struct {
				name  string
				table [256]uint16
			}{
				{"WinAnsiEncoding", verify.WinAnsiToUnicode},
				{"MacRomanEncoding", verify.MacRomanToUnicode},
			} {
				if encodingRewritePreservesMeaning(d, usage(), orig, cand.table) {
					d.Entries["Encoding"] = pdf.PDFName{Value: cand.name}
					changed = true
					break
				}
			}
		}
	})
	return changed, nil
}

// trimSymbolicCmap reduces desc's embedded FontFile2's cmap to a single
// subtable in place, leaving glyph data untouched.
func trimSymbolicCmap(desc pdf.PDFDict) bool {
	ff, ok := desc.Entries["FontFile2"].(pdf.PDFDict)
	if !ok || !ff.HasStream {
		return false
	}
	data, err := pdf.DecodeStream(ff)
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
	if err := writer.SetStreamFlate(&ff, trimmed); err != nil {
		return false
	}
	desc.Entries["FontFile2"] = ff
	return true
}

func trueTypeCmapSubtableCount(data []byte) (int, bool) {
	tables, ok := verify.ParseSfnt(data)
	if !ok {
		return 0, false
	}
	cmap := tables["cmap"]
	if len(cmap) < 4 {
		return 0, false
	}
	return int(binary.BigEndian.Uint16(cmap[2:4])), true
}
