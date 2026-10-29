package convert

import (
	_ "embed"
	"encoding/binary"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/voidrab/gopdfrab/internal/fontedit"
	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/fontsubst"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
	"github.com/voidrab/gopdfrab/internal/verify"

	"github.com/voidrab/gopdfrab/internal/writer"
)

// init registers the fixers that re-embed or substitute a bundled Liberation
// face for fonts the converter cannot otherwise make conformant.
func init() {
	registerFixer(fontSubstitutionFixer{})
	registerFixer(trueTypeEncodingFixer{})
}

// substituteFlags builds a FontDescriptor /Flags value for a substituted
// font: always non-symbolic, since substitution always resolves glyphs via
// WinAnsi/Unicode rather than a symbolic cmap.
func substituteFlags(face fontsubst.Face) pdf.PDFInteger {
	flags := 32
	if face.Serif {
		flags |= 0x2
	}
	if face.FixedPitch {
		flags |= 0x1
	}
	if face.Italic {
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
	return fontprog.SimpleFontCodeToUnicode(enc)
}

// noBreakSpaceAlias returns U+0020 for U+00A0 and 0 otherwise. ISO 32000-1
// Annex D reads WinAnsi 0240 and MacRoman 0312 as the glyph "space" while
// other tables read them as "nbspace", so a substitute is given both.
func noBreakSpaceAlias(u uint16) uint16 {
	if u == 0x00A0 {
		return 0x0020
	}
	return 0
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
		u := codeToUnicode[cc]
		if u == 0 {
			return
		}
		for _, v := range []uint16{u, noBreakSpaceAlias(u)} {
			if v != 0 && !seen[v] {
				seen[v] = true
				result = append(result, v)
			}
		}
	}
	if codes != nil {
		for cc := range codes {
			add(cc)
		}
		return result
	}
	firstChar, _ := d.Entries.Get("FirstChar").(pdf.PDFInteger)
	widths, _ := d.Entries.Get("Widths").(pdf.PDFArray)
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
func simpleFontNeedsSubstitution(d, desc pdf.PDFDict, usedCodes map[uintptr]map[int]bool, spec *pdfa.Spec) bool {
	subtype, _ := d.Entries.Get("Subtype").(pdf.PDFName)
	if !verify.EmbeddedProgramMatchesSubtype(subtype.Value, desc) {
		return true
	}
	baseFont, _ := d.Entries.Get("BaseFont").(pdf.PDFName)
	vctx := &verify.ValidationContext{UsedCharCodes: usedCodes, Spec: spec}
	verify.ValidateFontProgram(d, desc, baseFont.Value, vctx)
	if hasSubstitutionIssue(vctx) {
		return true
	}

	firstChar, fcOK := d.Entries.Get("FirstChar").(pdf.PDFInteger)
	lastChar, lcOK := d.Entries.Get("LastChar").(pdf.PDFInteger)
	widths, wOK := d.Entries.Get("Widths").(pdf.PDFArray)
	if !fcOK || !lcOK || !wOK {
		return false
	}
	subset := fontprog.SubsetTagRe.MatchString(baseFont.Value)
	if !subset {
		return false
	}
	switch subtype.Value {
	case "TrueType":
		if ff, ok := desc.Entries.Get("FontFile2").(pdf.PDFDict); ok {
			verify.ValidateSimpleTrueTypeSubset(d, ff, int(firstChar), int(lastChar), widths, vctx)
		}
	case "Type1", "MMType1":
		if desc.Entries.Get("CharSet") != nil {
			verify.ValidateType1SubsetCoverage(d, d, desc, int(firstChar), int(lastChar), widths, vctx)
		}
	}
	return hasSubstitutionIssue(vctx)
}

// substituteCoversUsage reports whether every character code the font renders
// resolves to a real glyph in the substitute subset. The resolution is the
// Encoding/Differences-based one that external validators apply. A code the
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
		return fontprog.TTAdvanceWidth(tables, int(gid)) >= 0
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
	firstChar, _ := d.Entries.Get("FirstChar").(pdf.PDFInteger)
	widths, _ := d.Entries.Get("Widths").(pdf.PDFArray)
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
func substituteSimpleFont(d pdf.PDFDict, usedCodes map[uintptr]map[int]bool, sharedDescs map[uintptr]bool, nextObjNum *int, spec *pdfa.Spec) bool {
	desc, ok := d.Entries.Get("FontDescriptor").(pdf.PDFDict)
	if !ok || desc.Entries == nil {
		return false
	}
	if !simpleFontNeedsSubstitution(d, desc, usedCodes, spec) {
		return false
	}

	// The result is always a non-symbolic TrueType font, which 6.3.7 limits
	// to the MacRoman/WinAnsi encoding names. Deciding the final encoding
	// up front keeps subset, Widths, and coverage consistent with what the
	// font dictionary will actually declare.
	finalEnc := pdf.PDFValue(pdf.PDFName{Value: "WinAnsiEncoding"})
	if name, ok := d.Entries.Get("Encoding").(pdf.PDFName); ok &&
		(name.Value == "MacRomanEncoding" || name.Value == "WinAnsiEncoding") {
		finalEnc = name
	}
	codeToUnicode := simpleFontCodeToUnicode(finalEnc)
	// The substitute keeps the content-stream bytes, so every used code must
	// mean the same thing under the declared encoding as it originally did;
	// otherwise a symbolic substitute preserves the codes' meanings directly.
	origTable, baseKnown := fontprog.OriginalSimpleFontCodeToUnicode(d)
	if !encodingRewritePreservesMeaning(d, usedCodes, origTable, codeToUnicode) {
		return substituteSimpleFontSymbolic(d, usedCodes, origTable, baseKnown, sharedDescs, nextObjNum)
	}
	unicodes := simpleFontUsedUnicodes(d, usedCodes, codeToUnicode)
	if len(unicodes) == 0 {
		return false
	}

	baseFont, _ := d.Entries.Get("BaseFont").(pdf.PDFName)
	face := fontsubst.Pick(desc, baseFont.Value)
	// face is a bundled Liberation face, which
	// TestBundledFacesSupportSubsetting pins to parse and carry a (3,1)
	// cmap: that is all SubsetTrueType can fail on, and its output is a
	// packed sfnt that always parses.
	subset, _ := fontedit.SubsetTrueType(face.Data, unicodes)
	tables, _ := fontprog.ParseSfnt(subset)
	cmap := fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(tables))

	if !substituteCoversUsage(d, usedCodes, codeToUnicode, cmap, tables) {
		return false
	}

	firstChar, fcOK := d.Entries.Get("FirstChar").(pdf.PDFInteger)
	lastChar, lcOK := d.Entries.Get("LastChar").(pdf.PDFInteger)
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
					if aw := fontprog.TTAdvanceWidth(tables, int(gid)); aw >= 0 {
						w = int(math.Round(aw))
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
		d.Entries.Set("FontDescriptor", desc)
	}

	newName := substituteBaseFontName(face, baseFont.Value)
	applySubstituteDescriptor(desc, tables, subset, face)
	desc.Entries.Set("FontName", pdf.PDFName{Value: newName})
	d.Entries.Set("BaseFont", pdf.PDFName{Value: newName})
	d.Entries.Set("Subtype", pdf.PDFName{Value: "TrueType"})
	d.Entries.Set("FirstChar", pdf.PDFInteger(firstChar))
	d.Entries.Set("LastChar", pdf.PDFInteger(lastChar))
	d.Entries.Set("Widths", widths)
	d.Entries.Set("Encoding", finalEnc)
	return true
}

// symbolicSubsetCoversCodes reports whether every code resolves to a real glyph
// through the subset's own (3,0) cmap. Every gid that cmap names is below the
// subset's glyph count and so has an hmtx entry, so no advance-width check is
// needed.
func symbolicSubsetCoversCodes(gidMap map[uint16]uint16, codeUnicode map[int]uint16) bool {
	for cc := range codeUnicode {
		gid, ok := gidMap[0xF000|uint16(cc)]
		if !ok || gid == 0 {
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
	desc, ok := d.Entries.Get("FontDescriptor").(pdf.PDFDict)
	if !ok || desc.Entries == nil {
		return false
	}
	codeUnicode := map[int]uint16{}
	known := forEachAssumedUsedCode(d, usedCodes, func(cc int) bool {
		u := origTable[cc]
		if u == 0 {
			// A zero entry under a known encoding renders .notdef,
			// and the substitute must keep it that way; under an
			// unknown one it could mean anything.
			return baseKnown
		}
		codeUnicode[cc] = u
		return true
	})
	if !known || len(codeUnicode) == 0 {
		return false
	}

	baseFont, _ := d.Entries.Get("BaseFont").(pdf.PDFName)
	libFace := fontsubst.Pick(desc, baseFont.Value)
	var subset []byte
	var tables map[string][]byte
	var gidOf map[uint16]uint16
	family := ""
	for _, cand := range []struct {
		data   []byte
		family string
	}{
		{libFace.Data, liberationFamilyName(libFace)},
		{fontsubst.NotoSymbols2, "NotoSansSymbols2"},
		{fontsubst.NotoSymbols, "NotoSansSymbols"},
	} {
		// Every candidate is a bundled face (see
		// TestBundledFacesSupportSubsetting), so the subsetter cannot
		// fail; its packed output parses and carries the format-4 (3,0)
		// cmap it wrote.
		s, _ := fontedit.SubsetTrueTypeSymbolic(cand.data, codeUnicode)
		t, _ := fontprog.ParseSfnt(s)
		_, _, gm, _ := fontedit.FirstFormat4CmapSubtable(t)
		if !symbolicSubsetCoversCodes(gm, codeUnicode) {
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
				if aw := fontprog.TTAdvanceWidth(tables, int(gid)); aw >= 0 {
					w = int(math.Round(aw))
				}
			}
		}
		widths[i] = pdf.PDFInteger(w)
	}

	if sharedDescs[pdf.ValuePointer(desc.Entries)] {
		desc = cloneFontDescriptor(desc, nextObjNum)
		d.Entries.Set("FontDescriptor", desc)
	}

	newName := substituteTaggedName(family, baseFont.Value)
	applySubstituteDescriptor(desc, tables, subset, libFace)
	// Symbolic flag set (and non-symbolic clear); no Encoding entry allowed.
	desc.Entries.Set("Flags", pdf.PDFInteger(4))
	desc.Entries.Set("FontName", pdf.PDFName{Value: newName})
	d.Entries.Set("BaseFont", pdf.PDFName{Value: newName})
	d.Entries.Set("Subtype", pdf.PDFName{Value: "TrueType"})
	d.Entries.Set("FirstChar", pdf.PDFInteger(minCode))
	d.Entries.Set("LastChar", pdf.PDFInteger(maxCode))
	d.Entries.Set("Widths", widths)
	d.Entries.Del("Encoding")
	if toUni, ok := buildToUnicodeStream(codeUnicode); ok {
		d.Entries.Set("ToUnicode", toUni)
	}
	return true
}

// cloneFontDescriptor copies desc into a fresh indirect dict so it can be
// mutated without affecting other fonts referencing the original.
func cloneFontDescriptor(desc pdf.PDFDict, nextObjNum *int) pdf.PDFDict {
	clone := pdf.NewPDFDict()
	for k, v := range desc.Entries.All() {
		if k == pdf.RefKey {
			continue
		}
		clone.Entries.Set(k, v)
	}
	clone.Entries.Set(pdf.RefKey, pdf.PDFRef{ObjNum: *nextObjNum})
	*nextObjNum++
	return clone
}

// liberationFamilyName returns the PostScript family name of the bundled
// Liberation face matching face's style bits.
func liberationFamilyName(face fontsubst.Face) string {
	family := "LiberationSans"
	switch {
	case face.FixedPitch:
		family = "LiberationMono"
	case face.Serif:
		family = "LiberationSerif"
	}
	switch {
	case face.Bold && face.Italic:
		family += "-BoldItalic"
	case face.Bold:
		family += "-Bold"
	case face.Italic:
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

func substituteBaseFontName(face fontsubst.Face, original string) string {
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
	writer.SetStreamFlate(&toUni, []byte(b.String()))
	return toUni, true
}

func applySubstituteDescriptor(desc pdf.PDFDict, tables map[string][]byte, program []byte, face fontsubst.Face) {
	fontFile := pdf.NewPDFDict()
	fontFile.Entries.Set("Length1", pdf.PDFInteger(len(program)))
	writer.SetStreamFlate(&fontFile, program)

	for _, k := range []string{"FontFile", "FontFile2", "FontFile3", "CharSet", "FontFamily"} {
		desc.Entries.Del(k)
	}
	desc.Entries.Set("Type", pdf.PDFName{Value: "FontDescriptor"})
	desc.Entries.Set("FontFile2", fontFile)
	desc.Entries.Set("FontBBox", ttScaledBBox(tables))
	desc.Entries.Set("Flags", substituteFlags(face))
	ascent, descent := ttScaledAscentDescent(tables)
	desc.Entries.Set("Ascent", pdf.PDFInteger(ascent))
	desc.Entries.Set("Descent", pdf.PDFInteger(descent))
	desc.Entries.Set("CapHeight", pdf.PDFInteger(ttScaledCapHeight(tables, ascent)))
	desc.Entries.Set("StemV", stemVFor(face.Bold))
	desc.Entries.Set("ItalicAngle", italicAngleFor(face.Italic))
	desc.Entries.Set("MissingWidth", pdf.PDFInteger(0))
}

// cidFontSubstitutionEligible reports whether a Type0 font carries a
// directly-recoverable code/CID->Unicode mapping.
func cidFontSubstitutionEligible(type0 pdf.PDFDict) (map[int]uint16, bool) {
	enc, _ := type0.Entries.Get("Encoding").(pdf.PDFName)
	if enc.Value != "Identity-H" && enc.Value != "Identity-V" {
		return nil, false
	}
	toUni, ok := type0.Entries.Get("ToUnicode").(pdf.PDFDict)
	if !ok || !toUni.HasStream {
		return nil, false
	}
	data, err := pdf.DecodeStream(toUni)
	if err != nil {
		return nil, false
	}
	cidToUnicode := fontprog.ParseToUnicodeCMap(data)
	if len(cidToUnicode) == 0 {
		return nil, false
	}
	return cidToUnicode, true
}

// drawsNotdef reports whether the embedded program of a CIDFont certainly has
// no glyph for cid, so a viewer draws the font's .notdef in its place. Only a
// program that can be read and an Identity CID-to-glyph mapping are trusted;
// anything less certain is false.
func drawsNotdef(cid, desc pdf.PDFDict, c int) bool {
	if ff, ok := desc.Entries.Get("FontFile2").(pdf.PDFDict); ok {
		if !verify.IdentityCIDToGIDMap(cid) {
			return false
		}
		data, err := pdf.DecodeStream(ff)
		if err != nil {
			return false
		}
		tables, ok := fontprog.ParseSfnt(data)
		return ok && !fontprog.TTGlyphPresent(tables)(c)
	}
	ff, ok := desc.Entries.Get("FontFile3").(pdf.PDFDict)
	if !ok {
		return false
	}
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		return false
	}
	td, ok := fontprog.ParseCFFTopDict(data)
	if !ok {
		return false
	}
	lens := fontprog.ParseCFFCharStringLengths(data, td.CSOffset)
	if lens == nil {
		return false
	}
	if !td.IsCIDKeyed {
		return c >= len(lens)
	}
	cids := fontprog.ParseCFFCharsetCIDs(data, td.CharsetOffset, len(lens))
	return cids != nil && !slices.Contains(cids, c)
}

// cidWidth is the width a CIDFont gives cid: its /W entry, else /DW, else
// the spec default 1000.
func cidWidth(cid pdf.PDFDict, c int) float64 {
	w, _ := cid.Entries.Get("W").(pdf.PDFArray)
	for _, pair := range fontprog.ParseCIDWidths(w) {
		if pair[0] == c {
			return float64(pair[1])
		}
	}
	if dw, ok := numericValue(cid.Entries.Get("DW")); ok {
		return dw
	}
	return 1000
}

// cidFontNeedsSubstitution reports whether a composite font needs substitution.
// It mirrors simpleFontNeedsSubstitution for composite fonts.
func cidFontNeedsSubstitution(cid, desc pdf.PDFDict, usedCIDs map[uintptr]map[int]bool, spec *pdfa.Spec) bool {
	subtype, _ := cid.Entries.Get("Subtype").(pdf.PDFName)
	if !verify.EmbeddedProgramMatchesSubtype(subtype.Value, desc) {
		return true
	}
	baseFont, _ := cid.Entries.Get("BaseFont").(pdf.PDFName)
	vctx := &verify.ValidationContext{UsedCIDs: usedCIDs, Spec: spec}
	verify.ValidateFontProgram(cid, desc, baseFont.Value, vctx)
	if hasSubstitutionIssue(vctx) {
		return true
	}

	if !fontprog.SubsetTagRe.MatchString(baseFont.Value) {
		return false
	}
	w, _ := cid.Entries.Get("W").(pdf.PDFArray)
	switch subtype.Value {
	case "CIDFontType2":
		if ff, ok := desc.Entries.Get("FontFile2").(pdf.PDFDict); ok {
			verify.ValidateCIDTrueTypeSubset(cid, ff, w, vctx)
		}
	case "CIDFontType0":
		if ff, ok := desc.Entries.Get("FontFile3").(pdf.PDFDict); ok {
			verify.ValidateCIDCFFSubset(cid, ff, w, vctx)
		}
	}
	return hasSubstitutionIssue(vctx)
}

// substituteCIDFont rebuilds a Type0 font's descendant in place as a
// CIDFontType2 embedding a subsetted bundled Liberation face.
func substituteCIDFont(type0, cid pdf.PDFDict, usedCIDs map[uintptr]map[int]bool, sharedDescs map[uintptr]bool, nextObjNum *int, spec *pdfa.Spec) bool {
	desc, ok := cid.Entries.Get("FontDescriptor").(pdf.PDFDict)
	if !ok || desc.Entries == nil {
		return false
	}
	if !cidFontNeedsSubstitution(cid, desc, usedCIDs, spec) {
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
		w, _ := cid.Entries.Get("W").(pdf.PDFArray)
		for _, pair := range fontprog.ParseCIDWidths(w) {
			cids[pair[0]] = true
		}
	}

	// Every rendered CID must survive the substitution; CID 0 (.notdef) needs
	// no mapping. A CID with no recoverable meaning would silently lose its
	// glyph, so refuse and leave the page to raster fallback -- unless the
	// program being replaced has no glyph for it either. Then what it drew was
	// the missing-glyph shape, which the substitute's own .notdef stands in
	// for, advancing by the same width so the text after it stays put.
	targetCIDs := map[uint16][]int{}
	notdef := map[int]float64{}
	for c := range cids {
		if c == 0 {
			continue
		}
		u, ok := cidToUnicode[c]
		switch {
		case ok:
			targetCIDs[u] = append(targetCIDs[u], c)
		case drawsNotdef(cid, desc, c):
			notdef[c] = cidWidth(cid, c)
		default:
			return false
		}
	}
	if len(targetCIDs) == 0 {
		return false
	}

	// Prefer the style-matched Liberation face, falling back to the bundled
	// Noto symbol repertoires before giving the page to raster fallback.
	baseFont, _ := cid.Entries.Get("BaseFont").(pdf.PDFName)
	face := fontsubst.Pick(desc, baseFont.Value)
	var faceData []byte
	family := ""
	for _, cand := range []struct {
		data   []byte
		family string
	}{
		{face.Data, liberationFamilyName(face)},
		{fontsubst.NotoSymbols2, "NotoSansSymbols2"},
		{fontsubst.NotoSymbols, "NotoSansSymbols"},
	} {
		// Every candidate is a bundled face with a format-4 (3,1) cmap (see
		// TestBundledFacesSupportSubsetting).
		faceTables, _ := fontprog.ParseSfnt(cand.data)
		faceCmap := fontprog.ParseCmapFormat4(fontprog.TTWindowsBMPCmap(faceTables))
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

	// faceData passed the cmap check above, the only thing the subsetter can
	// fail on, and its packed output always parses.
	subset, _ := fontedit.SubsetTrueTypeForCID(faceData, targetCIDs, notdef)
	tables, _ := fontprog.ParseSfnt(subset)

	var widthPairs [][2]int
	addWidth := func(c int) {
		if aw := fontprog.TTAdvanceWidth(tables, c); aw >= 0 {
			widthPairs = append(widthPairs, [2]int{c, int(math.Round(aw))})
		}
	}
	for _, cidList := range targetCIDs {
		for _, c := range cidList {
			addWidth(c)
		}
	}
	for c := range notdef {
		addWidth(c)
	}
	// targetCIDs is non-empty and every CID in it is below the subset's glyph
	// count, with an hmtx entry of its own, so widthPairs is never empty.
	sort.Slice(widthPairs, func(i, j int) bool { return widthPairs[i][0] < widthPairs[j][0] })

	if sharedDescs[pdf.ValuePointer(desc.Entries)] {
		desc = cloneFontDescriptor(desc, nextObjNum)
		cid.Entries.Set("FontDescriptor", desc)
	}

	newName := substituteTaggedName(family, baseFont.Value)
	applySubstituteDescriptor(desc, tables, subset, face)
	desc.Entries.Set("FontName", pdf.PDFName{Value: newName})
	cid.Entries.Set("BaseFont", pdf.PDFName{Value: newName})
	type0.Entries.Set("BaseFont", pdf.PDFName{Value: newName})
	cid.Entries.Set("Subtype", pdf.PDFName{Value: "CIDFontType2"})
	cid.Entries.Set("CIDToGIDMap", pdf.PDFName{Value: "Identity"})
	cid.Entries.Set("W", buildCIDWidthsArray(widthPairs))
	if ff, ok := desc.Entries.Get("FontFile2").(pdf.PDFDict); ok {
		desc.Entries.Del("CIDSet")
		fixTrueTypeCIDSet(cid, desc, ff)
	}
	cid.Entries.Set("DW", pdf.PDFInteger(0))
	return true
}

// --- /ToUnicode CMap parsing ---

// fontSubstitutionFixer remediates SubsetGlyphCoverage, SimpleNotEmbedded,
// CIDNotEmbedded and InvalidProgram by substituting a bundled Liberation
// face wherever a font's own program is missing, damaged, or doesn't cover
// a glyph it needs.
type fontSubstitutionFixer struct {
	doc  *pdf.Reader
	spec *pdfa.Spec
}

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
		if ref, ok := d.Entries.Get(pdf.RefKey).(pdf.PDFRef); ok && ref.ObjNum > maxRef {
			maxRef = ref.ObjNum
		}
		if (d.Entries.Get("Type") != pdf.PDFName{Value: "Font"}) {
			return
		}
		if desc, ok := d.Entries.Get("FontDescriptor").(pdf.PDFDict); ok && desc.Entries != nil {
			descCounts[pdf.ValuePointer(desc.Entries)]++
		}
		switch subtype, _ := d.Entries.Get("Subtype").(pdf.PDFName); subtype.Value {
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
	_, _, usedCodes, usedCIDs, _ := verify.ComputeContentUsage(*trailer, usageCtx)
	sharedDescs := map[uintptr]bool{}
	for ptr, n := range descCounts {
		if n > 1 {
			sharedDescs[ptr] = true
		}
	}
	nextObjNum := maxRef + 1

	changed := false
	for _, d := range simple {
		// Standard Type1 fonts (e.g. in AcroForm/DR) have no
		// FontDescriptor; create a scratch one so substituteSimpleFont
		// can proceed. Remove it again if substitution turns out not to
		// be needed -- leaving it behind would plant an
		// all-fields-missing descriptor where the PDF legitimately had
		// none.
		hadDescriptor := d.Entries.Get("FontDescriptor") != nil
		if !hadDescriptor {
			d.Entries.Set("FontDescriptor", pdf.NewPDFDict())
		}
		if substituteSimpleFont(d, usedCodes, sharedDescs, &nextObjNum, f.spec) {
			changed = true
		} else if !hadDescriptor {
			d.Entries.Del("FontDescriptor")
		}
	}
	for _, d := range composite {
		if cid := verify.DescendantCIDFont(d); cid.Entries != nil {
			if substituteCIDFont(d, cid, usedCIDs, sharedDescs, &nextObjNum, f.spec) {
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
			_, _, usedCodes, _, _ = verify.ComputeContentUsage(*trailer, verify.NewContext(f.doc))
		}
		return usedCodes
	}
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if (d.Entries.Get("Type") != pdf.PDFName{Value: "Font"}) {
			return
		}
		if (d.Entries.Get("Subtype") != pdf.PDFName{Value: "TrueType"}) {
			return
		}
		desc, ok := d.Entries.Get("FontDescriptor").(pdf.PDFDict)
		if !ok {
			return
		}
		flags := 0
		if f, ok := desc.Entries.Get("Flags").(pdf.PDFInteger); ok {
			flags = int(f)
		}
		if flags&4 != 0 { // symbolic
			if d.Entries.Get("Encoding") != nil {
				d.Entries.Del("Encoding")
				changed = true
			}
			if trimSymbolicCmap(desc) {
				changed = true
			}
			return
		}
		if name, ok := d.Entries.Get("Encoding").(pdf.PDFName); !ok || (name.Value != "MacRomanEncoding" && name.Value != "WinAnsiEncoding") {
			// The font keeps its program and content bytes, so the
			// replacement name encoding must preserve what every
			// used code meant; when neither does, leave the
			// violation for the raster fallback.
			orig, _ := fontprog.OriginalSimpleFontCodeToUnicode(d)
			for _, cand := range [...]struct {
				name  string
				table [256]uint16
			}{
				{"WinAnsiEncoding", fontprog.WinAnsiToUnicode},
				{"MacRomanEncoding", fontprog.MacRomanToUnicode},
			} {
				if encodingRewritePreservesMeaning(d, usage(), orig, cand.table) {
					d.Entries.Set("Encoding", pdf.PDFName{Value: cand.name})
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
	ff, ok := desc.Entries.Get("FontFile2").(pdf.PDFDict)
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
	trimmed, err := fontedit.TrimTrueTypeCmapToSingleSubtable(data)
	if err != nil {
		return false
	}
	writer.SetStreamFlate(&ff, trimmed)
	desc.Entries.Set("FontFile2", ff)
	return true
}

func trueTypeCmapSubtableCount(data []byte) (int, bool) {
	tables, ok := fontprog.ParseSfnt(data)
	if !ok {
		return 0, false
	}
	cmap := tables["cmap"]
	if len(cmap) < 4 {
		return 0, false
	}
	return int(binary.BigEndian.Uint16(cmap[2:4])), true
}
