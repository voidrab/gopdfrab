package verify

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// ValidateType1SubsetCoverage verifies every used character code against the
// embedded font program: a used glyph the program does not define violates
// 6.3.5-1, and a defined-but-used glyph missing from the descriptor's CharSet
// violates 6.3.5-2 (unused program glyphs need not be listed). The CharSet
// string stands in as the glyph set when the program is unparseable. A level
// whose subsets may omit the CharSet checks the program alone when the CharSet
// is absent.
func ValidateType1SubsetCoverage(obj pdf.PDFValue, v pdf.PDFDict, desc pdf.PDFDict, firstChar, lastChar int, widths pdf.PDFArray, ctx *ValidationContext) {
	var charSet map[string]bool
	if charSetVal, ok := desc.Entries.Lookup("CharSet"); ok {
		var charSetStr string
		switch cs := charSetVal.(type) {
		case pdf.PDFString:
			charSetStr = cs.Value
		case pdf.PDFHexString:
			charSetStr = string(pdf.DecodePDFHexStringBytes(cs.Value))
		default:
			return
		}
		if charSetStr == "" {
			ctx.Report(pdf.Checks.Font.Type1SubsetCharSet, obj, "Type 1 subset font descriptor has an empty CharSet")
			return
		}
		charSet = map[string]bool{".notdef": true}
		for _, part := range fontprog.SplitCharSetNames(charSetStr) {
			charSet[part] = true
		}
	} else if !ctx.subsetSetsOptional() {
		return // CharSet absence is caught by a separate check
	}

	// The program's own glyph set is the coverage ground truth; CharSet is
	// metadata that must stay consistent with it for the glyphs actually used.
	// When the program is absent or unreadable, glyphSet falls back to CharSet
	// and the 6.3.5-2 consistency branch below becomes unreachable -- there is
	// nothing to compare CharSet against. An unreadable program is separately
	// reported as StreamUndecodable by the decode chokepoint, so the fallback
	// is a narrowed check rather than a silent pass.
	glyphSet := charSet
	haveProgram := false
	if programNames := embeddedType1GlyphNames(desc, ctx); len(programNames) > 0 {
		haveProgram = true
		glyphSet = map[string]bool{".notdef": true}
		for _, n := range programNames {
			glyphSet[n] = true
		}
	}
	if glyphSet == nil {
		return // Without a CharSet or a readable program, nothing can be checked.
	}

	glyphNames, ok := type1SubsetGlyphNames(v, desc, ctx)
	if !ok {
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
		if !glyphSet[glyph] {
			ctx.Report(pdf.Checks.Font.SubsetGlyphCoverage, obj, fmt.Sprintf("character code %d maps to glyph /%s which is not defined in the embedded font subset", cc, glyph))
			return false
		}
		if haveProgram && charSet != nil && !charSet[glyph] {
			ctx.Report(pdf.Checks.Font.Type1SubsetCharSet, obj, fmt.Sprintf("CharSet does not list used glyph /%s defined in the font subset", glyph))
			return false
		}
		return true
	}

	// Prefer codes actually shown in content streams (a missing glyph is
	// sometimes given width 0 as a placeholder, hiding the violation);
	// fall back to non-zero-width codes if usage info is unavailable.
	if usedCodes, knownUsage := ctx.usedCodesFor(v); knownUsage {
		// Iterate in code order: checkCode reports the first offending glyph and
		// stops, so a stable order makes the reported glyph deterministic rather
		// than dependent on map iteration.
		codes := make([]int, 0, len(usedCodes))
		for cc := range usedCodes {
			codes = append(codes, cc)
		}
		sort.Ints(codes)
		for _, cc := range codes {
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

// validateType1CMetrics checks that PDF Widths entries match the advance
// widths in an embedded Type1C (CFF) program (6.3.6). Only codes actually
// shown are checked when usage info is available.
func validateType1CMetrics(obj pdf.PDFValue, v pdf.PDFDict, ff pdf.PDFDict, firstChar int, widths pdf.PDFArray, ctx *ValidationContext) {
	data, err := ctx.decodeStreamCached(ff)
	if err != nil || len(data) == 0 {
		return
	}
	glyphWidths := fontprog.CFFAdvanceWidths(data)
	if len(glyphWidths) == 0 {
		return
	}
	glyphNames, ok := fontprog.SimpleFontGlyphNameTable(v)
	if !ok {
		return
	}

	usedCodes, knownUsage := ctx.usedCodesFor(v)
	for i, w := range widths {
		pdfWidth, ok := pdf.PDFNumberToInt(w)
		if !ok || pdfWidth == 0 {
			continue
		}
		cc := firstChar + i
		if cc < 0 || cc > 255 {
			continue
		}
		if knownUsage && !usedCodes[cc] {
			continue
		}
		glyph := glyphNames[cc]
		if glyph == "" {
			continue
		}
		csWidth, found := glyphWidths[glyph]
		if !found {
			continue
		}
		if math.Abs(float64(pdfWidth)-csWidth) > 1 {
			ctx.Report(pdf.Checks.Font.AdvanceWidthMismatch, obj, fmt.Sprintf("character code %d (/%s): PDF width %d ≠ CFF advance width %s",
				cc, glyph, pdfWidth, formatWidth(csWidth)))
			return
		}
	}
}

// validateCIDCFFMetrics checks /W entries against the advance widths in an
// embedded CID-keyed CFF program (6.3.6). CIDs never shown are exempt when
// usage info is available.
func validateCIDCFFMetrics(obj pdf.PDFValue, v pdf.PDFDict, ff pdf.PDFDict, w pdf.PDFArray, ctx *ValidationContext) {
	data, err := ctx.decodeStreamCached(ff)
	if err != nil || len(data) == 0 {
		return
	}
	cidWidths := fontprog.CFFCIDAdvanceWidths(data)
	if len(cidWidths) == 0 {
		return
	}
	usedCIDs, knownUsage := ctx.usedCIDsFor(v)
	for _, pair := range fontprog.ParseCIDWidths(w) {
		if knownUsage && !usedCIDs[pair[0]] {
			continue
		}
		csWidth, found := cidWidths[pair[0]]
		if !found {
			continue
		}
		if math.Abs(float64(pair[1])-csWidth) > 1 {
			ctx.Report(pdf.Checks.Font.AdvanceWidthMismatch, obj, fmt.Sprintf("CID %d: PDF width %d ≠ CFF advance width %s",
				pair[0], pair[1], formatWidth(csWidth)))
			return
		}
	}
}

// type1SubsetGlyphNames resolves the code->glyph-name table for the 6.3.5
// Type 1 coverage check. With an embedded FontFile program, an absent
// /Encoding or /BaseEncoding means the program's built-in encoding (ISO
// 32000-1 9.6.6.1); the font dictionary alone cannot say what that is.
func type1SubsetGlyphNames(v, desc pdf.PDFDict, ctx *ValidationContext) ([256]string, bool) {
	if ff, ok := desc.Entries.Get("FontFile").(pdf.PDFDict); ok {
		if data, err := ctx.decodeStreamCached(ff); err == nil {
			return fontprog.Type1GlyphNameTable(data, v.Entries.Get("Encoding"))
		}
	}
	return fontprog.SimpleFontGlyphNameTable(v)
}

// embeddedType1GlyphNames returns the glyph names defined by a descriptor's
// embedded Type1 (FontFile) or Type1C (FontFile3) program, or nil when no
// program is present or it cannot be parsed.
func embeddedType1GlyphNames(desc pdf.PDFDict, ctx *ValidationContext) []string {
	if ff, ok := desc.Entries.Get("FontFile").(pdf.PDFDict); ok {
		if data, err := ctx.decodeStreamCached(ff); err == nil {
			return ctx.type1ProgramFor(data).Names
		}
		return nil
	}
	if ff, ok := desc.Entries.Get("FontFile3").(pdf.PDFDict); ok {
		if data, err := ctx.decodeStreamCached(ff); err == nil {
			return fontprog.CFFGlyphNames(data)
		}
	}
	return nil
}

// ValidateCIDCFFSubset checks that all CIDs referenced in the W array are
// defined in the embedded CFF program (6.3.5). CIDs only used to declare a
// width, never shown, are exempt when usage info is available.
func ValidateCIDCFFSubset(obj pdf.PDFValue, ff pdf.PDFDict, w pdf.PDFArray, ctx *ValidationContext) {
	data, err := ctx.decodeStreamCached(ff)
	if err != nil {
		return
	}
	// unparseable program: reported as Font.InvalidProgram (6.3.2) by ValidateFontProgram
	td, ok := fontprog.ParseCFFTopDict(data)
	if !ok || td.CSOffset < 0 || td.CSOffset+2 > len(data) {
		return
	}
	csCount := int(binary.BigEndian.Uint16(data[td.CSOffset : td.CSOffset+2]))

	// With known usage, the CIDs checked are those shown, including those
	// that rely on the default width DW. Otherwise they are the CIDs the W
	// array lists.
	cidList, _ := cidsToCheck(obj, w, ctx)

	// CID-keyed CFFs remap glyph IDs to CIDs via the Charset table (GID and
	// CID differ); non-CID-keyed CFFs use the GID directly as the CID.
	if td.IsCIDKeyed {
		cids := fontprog.ParseCFFCharsetCIDs(data, td.CharsetOffset, csCount)
		if cids == nil {
			return
		}
		gidOfCID := make(map[int]int, len(cids))
		for gid, c := range cids {
			gidOfCID[c] = gid
		}
		// A bare/near-empty CharString (e.g. a single "endchar" byte, no
		// hsbw/width) is functionally undefined despite the charset entry.
		lens := fontprog.ParseCFFCharStringLengths(data, td.CSOffset)
		for _, cid := range cidList {
			gid, ok := gidOfCID[cid]
			if !ok || (lens != nil && gid < len(lens) && lens[gid] <= 1) {
				ctx.Report(pdf.Checks.Font.SubsetGlyphCoverage, obj, fmt.Sprintf("CID %d referenced for rendering is not defined in CFF charset", cid))
				return
			}
		}
		return
	}

	for _, cid := range cidList {
		if cid >= csCount {
			ctx.Report(pdf.Checks.Font.SubsetGlyphCoverage, obj, fmt.Sprintf("CID %d referenced for rendering is not defined in CFF CharStrings (count=%d)", cid, csCount))
			return
		}
	}
}

// validateCIDSetBitmap checks that the FontDescriptor's CIDSet bitmap marks
// every CID that has a glyph in the embedded CID-keyed CFF program (6.3.5/3).
// Each byte covers 8 CIDs, MSB first: bit j of byte i is CID i*8+j.
func validateCIDSetBitmap(obj pdf.PDFValue, desc pdf.PDFDict, ff pdf.PDFDict, ctx *ValidationContext) {
	cidSet, ok := desc.Entries.Get("CIDSet").(pdf.PDFDict)
	if !ok || !cidSet.IsStream() {
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
	// unparseable program: reported as Font.InvalidProgram (6.3.2) by ValidateFontProgram
	td, ok := fontprog.ParseCFFTopDict(data)
	if !ok || !td.IsCIDKeyed || td.CSOffset < 0 || td.CSOffset+2 > len(data) {
		return
	}
	csCount := int(binary.BigEndian.Uint16(data[td.CSOffset : td.CSOffset+2]))
	cids := fontprog.ParseCFFCharsetCIDs(data, td.CharsetOffset, csCount)
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

// validateCIDSetTrueType checks that the FontDescriptor's CIDSet bitmap marks
// the CIDs the document renders from the embedded CIDFontType2 subset program
// (6.3.5/3), assuming an Identity CIDToGIDMap. CID 0 (.notdef) is exempt.
//
// Scoped to rendered CIDs, unlike the CFF path, and the asymmetry is in how the
// two formats subset rather than in the rule. A CID-keyed CFF subset's charset
// lists exactly the glyphs the subset carries, so "in the charset" is the same
// question as "present in the font program". A TrueType subsetter typically
// keeps `loca` and `numGlyphs` at their original size and simply empties the
// entries it dropped, so glyphs with outline data include composite components
// and whatever else the subsetter chose to retain -- none of which the document
// renders, and none of which a producer puts in CIDSet. Requiring those would
// reject conformant files. One such font had 3065 glyph slots, 64 outlines and
// 26 CIDs in its CIDSet, all consistent.
//
// When usage is unknown (an undecodable content stream, a degraded object) the
// check does not run at all rather than falling back to the glyph table, on the
// rule that an incomplete usage set must not drive a report.
func validateCIDSetTrueType(obj pdf.PDFDict, desc pdf.PDFDict, ff pdf.PDFDict, ctx *ValidationContext) {
	cidSet, ok := desc.Entries.Get("CIDSet").(pdf.PDFDict)
	if !ok || !cidSet.IsStream() {
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
	// unparseable program: reported as Font.InvalidProgram (6.3.2) by ValidateFontProgram
	tables, ok := fontprog.ParseSfnt(data)
	if !ok {
		return
	}
	used, known := cidsRenderedBy(obj, ctx)
	if !known {
		return
	}
	hasGlyph := fontprog.TTLocaHasGlyph(tables)
	numGlyphs := fontprog.TTNumGlyphs(tables)
	for _, cid := range used {
		if cid < 1 || cid >= numGlyphs || !hasGlyph(cid) {
			continue // absent glyphs are the coverage check's business, not this one
		}
		byteIdx, bitIdx := cid/8, 7-cid%8
		if byteIdx >= len(bitmap) || bitmap[byteIdx]&(1<<bitIdx) == 0 {
			ctx.Report(pdf.Checks.Font.CIDSubsetCIDSet, obj, fmt.Sprintf("CIDSet does not list CID %d, which the document renders from the embedded font program", cid))
			return
		}
	}
}

// cidsRenderedBy returns the CIDs the document shows from the CIDFont obj, in
// ascending order, and whether usage is known at all.
func cidsRenderedBy(obj pdf.PDFDict, ctx *ValidationContext) (cids []int, known bool) {
	used, known := ctx.usedCIDsFor(obj)
	if !known {
		return nil, false
	}
	cids = make([]int, 0, len(used))
	for cid := range used {
		cids = append(cids, cid)
	}
	sort.Ints(cids) // the first mismatch is reported, so the order must be stable
	return cids, true
}

// cidsToCheck returns the CIDs a coverage/metrics check should examine for the
// CIDFont obj and whether the list reflects actual rendering usage. When usage
// is known it is the glyphs shown for rendering (so CIDs relying on the default
// width DW, absent from W, are still covered); otherwise it is the W entries.
func cidsToCheck(obj pdf.PDFValue, w pdf.PDFArray, ctx *ValidationContext) (cids []int, knownUsage bool) {
	if desc, ok := obj.(pdf.PDFDict); ok {
		if used, known := ctx.usedCIDsFor(desc); known {
			cids = make([]int, 0, len(used))
			for cid := range used {
				cids = append(cids, cid)
			}
			sort.Ints(cids)
			return cids, true
		}
	}
	for _, pair := range fontprog.ParseCIDWidths(w) {
		cids = append(cids, pair[0])
	}
	return cids, false
}

// cidDefaultWidth returns the CIDFont's DW entry, defaulting to 1000.
func cidDefaultWidth(obj pdf.PDFValue) int {
	if d, ok := obj.(pdf.PDFDict); ok {
		switch dw := d.Entries.Get("DW").(type) {
		case pdf.PDFInteger:
			return int(dw)
		case pdf.PDFReal:
			return int(dw)
		}
	}
	return 1000
}

// ValidateCIDTrueTypeSubset checks that every CID referenced for rendering has
// a glyph in the embedded TrueType program (6.3.5). CIDs that are only declared
// (in W) but never shown are exempt when usage info is available.
func ValidateCIDTrueTypeSubset(obj pdf.PDFValue, ff pdf.PDFDict, w pdf.PDFArray, ctx *ValidationContext) {
	data, err := ctx.decodeStreamCached(ff)
	if err != nil {
		return
	}
	// unparseable program: reported as Font.InvalidProgram (6.3.2) by ValidateFontProgram
	tables, ok := fontprog.ParseSfnt(data)
	if !ok {
		return
	}
	cids, _ := cidsToCheck(obj, w, ctx)
	glyphPresent := fontprog.TTGlyphPresent(tables)
	for _, cid := range cids {
		if !glyphPresent(cid) {
			ctx.Report(pdf.Checks.Font.SubsetGlyphCoverage, obj, fmt.Sprintf("CID %d referenced for rendering has no glyph in embedded program", cid))
			return
		}
	}
}

// validateCIDTrueTypeMetrics checks that CID advance widths match the embedded
// TrueType hmtx table (6.3.6): every W entry, plus rendered CIDs that fall back
// to the default width DW because they are absent from W.
func validateCIDTrueTypeMetrics(obj pdf.PDFDict, ff pdf.PDFDict, w pdf.PDFArray, ctx *ValidationContext) {
	data, err := ctx.decodeStreamCached(ff)
	if err != nil {
		return
	}
	// unparseable program: reported as Font.InvalidProgram (6.3.2) by ValidateFontProgram
	tables, ok := fontprog.ParseSfnt(data)
	if !ok {
		return
	}
	glyphPresent := fontprog.TTGlyphPresent(tables)
	mismatched := func(cid, pdfWidth int) bool {
		if !glyphPresent(cid) {
			return false // absent glyph: 6.3.5 coverage handles it
		}
		fontWidth := fontprog.TTAdvanceWidth(tables, cid)
		// Allow ±1 rounding tolerance.
		return fontWidth >= 0 && math.Abs(fontWidth-float64(pdfWidth)) > 1
	}
	report := func(cid, pdfWidth int, fontWidth float64) {
		ctx.Report(pdf.Checks.Font.AdvanceWidthMismatch, obj, fmt.Sprintf("CID %d: PDF width %d ≠ font hmtx width %s",
			cid, pdfWidth, formatWidth(fontWidth)))
	}

	widthOf := map[int]int{}
	for _, pair := range fontprog.ParseCIDWidths(w) {
		widthOf[pair[0]] = pair[1]
		if mismatched(pair[0], pair[1]) {
			report(pair[0], pair[1], fontprog.TTAdvanceWidth(tables, pair[0]))
			return
		}
	}

	// Rendered CIDs absent from W take the default width DW.
	used, known := ctx.usedCIDsFor(obj)
	if !known {
		return
	}
	dw := cidDefaultWidth(obj)
	cids := make([]int, 0, len(used))
	for cid := range used {
		if _, inW := widthOf[cid]; !inW {
			cids = append(cids, cid)
		}
	}
	sort.Ints(cids)
	for _, cid := range cids {
		if mismatched(cid, dw) {
			report(cid, dw, fontprog.TTAdvanceWidth(tables, cid))
			return
		}
	}
}

// fontEncoding returns a font dictionary's /Encoding entry, or nil (also for
// a non-dictionary, whose zero Entries is nil-safe).
func fontEncoding(obj pdf.PDFValue) pdf.PDFValue {
	fontDict, _ := obj.(pdf.PDFDict)
	return fontDict.Entries.Get("Encoding")
}

// ValidateSimpleTrueTypeSubset checks that all referenced character codes have
// a corresponding glyph in the embedded TrueType program (6.3.5).
func ValidateSimpleTrueTypeSubset(obj pdf.PDFValue, ff pdf.PDFDict, firstChar, lastChar int, widths pdf.PDFArray, ctx *ValidationContext) {
	data, err := ctx.decodeStreamCached(ff)
	if err != nil {
		return
	}
	// unparseable program: reported as Font.InvalidProgram (6.3.2) by ValidateFontProgram
	tables, ok := fontprog.ParseSfnt(data)
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

	r := fontprog.NewSimpleTrueTypeCmaps(fontEncoding(obj), tables)
	numGlyphs := fontprog.TTNumGlyphs(tables)

	// codeToGID resolves a char code to a GID; 0 (.notdef) when no cmap maps it.
	codeToGID := func(cc int) int {
		if gid, _, found := r.Windows(cc); found {
			return gid
		}
		// A (3,1) miss is not the end of the lookup. ISO 32000-1 9.6.6.4
		// has a viewer try the (1,0) Macintosh cmap next, and subsetters
		// prune (3,1) entries for characters the document does not use
		// while leaving (1,0) intact -- so treating a miss as definitive
		// rejects conformant files whose glyphs resolve through (1,0).
		//
		// If no cmap maps the code, whatever the font's flags say, the
		// viewer has nothing left to resolve it with and renders .notdef.
		// The symbolic case must therefore not be skipped here, or a code
		// shown from a symbolic subset whose cmaps do not carry it goes
		// unreported.
		gid, _ := r.Fallback(cc)
		return gid
	}

	checkCode := func(cc int) bool {
		gid := codeToGID(cc)
		if gid == 0 || (numGlyphs > 0 && gid >= numGlyphs) {
			u := r.CodeToUnicode[cc]
			ctx.Report(pdf.Checks.Font.SubsetGlyphCoverage, obj, fmt.Sprintf("character code %d (U+%04X) has no glyph in embedded font program", cc, u))
			return false
		}
		return true
	}

	// Prefer codes actually shown in content streams; fall back to
	// non-zero-width codes if usage info is unavailable.
	if fontDict, ok := obj.(pdf.PDFDict); ok {
		if usedCodes, knownUsage := ctx.usedCodesFor(fontDict); knownUsage {
			// Sorted: checkCode reports the first failure and stops, so
			// iterating the map directly would name a different code per run.
			codes := make([]int, 0, len(usedCodes))
			for cc := range usedCodes {
				codes = append(codes, cc)
			}
			sort.Ints(codes)
			for _, cc := range codes {
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
	// unparseable program: reported as Font.InvalidProgram (6.3.2) by ValidateFontProgram
	tables, ok := fontprog.ParseSfnt(data)
	if !ok {
		return
	}

	r := fontprog.NewSimpleTrueTypeCmaps(fontEncoding(obj), tables)

	codeToGID := func(cc int) (int, bool) {
		if gid, tried, found := r.Windows(cc); tried {
			return gid, found // a (3,1) miss: the width cannot be verified
		}
		return r.Fallback(cc)
	}

	// With known usage, every shown code is checked, including those with a
	// zero width. Otherwise only codes with a nonzero width are checked, as a
	// zero width often marks a code the font leaves unused.
	var shown map[int]bool
	knownUsage := false
	if fontDict, ok := obj.(pdf.PDFDict); ok {
		shown, knownUsage = ctx.usedCodesFor(fontDict)
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
		cc := firstChar + i
		if knownUsage && !shown[cc] || !knownUsage && pdfWidth == 0 {
			continue
		}
		gid, known := codeToGID(cc)
		if !known {
			continue
		}
		fontWidth := fontprog.TTAdvanceWidth(tables, gid)
		if fontWidth < 0 {
			continue
		}
		if math.Abs(fontWidth-float64(pdfWidth)) > 1 {
			ctx.Report(pdf.Checks.Font.AdvanceWidthMismatch, obj, fmt.Sprintf("character code %d: PDF width %d ≠ font hmtx width %s",
				cc, pdfWidth, formatWidth(fontWidth)))
			return
		}
	}
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
		_, ok := fontprog.ParseSfnt(data)
		return ok
	case "FontFile3":
		if len(data) >= 4 && data[0] == 1 { // CFF header, major version 1
			_, ok := fontprog.ParseCFFTopDict(data)
			return ok
		}
		_, ok := fontprog.ParseSfnt(data) // OpenType-wrapped CFF
		return ok
	case "FontFile":
		// Type 1: the clear-text portion begins with a PostScript marker.
		return bytes.HasPrefix(data, []byte("%!"))
	}
	return true
}

// ValidateFontProgram flags a damaged embedded font program (6.3.2) and an
// embedded font file of a /Subtype the level does not allow (6.3.2).
func ValidateFontProgram(obj pdf.PDFValue, desc pdf.PDFDict, name string, ctx *ValidationContext) {
	for _, key := range []string{"FontFile", "FontFile2", "FontFile3"} {
		ff, ok := desc.Entries.Get(key).(pdf.PDFDict)
		if !ok {
			continue
		}
		if !fontProgramValid(ctx, ff, key) {
			ctx.Report(pdf.Checks.Font.InvalidProgram, obj, fmt.Sprintf("embedded font program for %s is damaged", name))
		}
		// No subtype at all is fine.
		if st, ok := ff.Entries.Get("Subtype").(pdf.PDFName); ok && !ctx.Spec.FontFileSubtypes.Has(st.Value) {
			ctx.Report(pdf.Checks.Font.FontFileSubtype, obj,
				fmt.Sprintf("embedded font file for %s has unsupported Subtype /%s", name, st.Value))
		}
	}
}

// sfntCmaps describes the cmap table of an embedded TrueType program: how
// many subtables it has, and whether a Microsoft Symbol (3,0) or Unicode
// (3,1) subtable is among them.
type sfntCmaps struct {
	count           int
	symbol, unicode bool
}

// trueTypeCmaps returns the cmap table summary of an embedded TrueType font,
// and whether it could be read.
func trueTypeCmaps(ctx *ValidationContext, desc pdf.PDFDict) (sfntCmaps, bool) {
	ff, ok := desc.Entries.Get("FontFile2").(pdf.PDFDict)
	if !ok {
		return sfntCmaps{}, false
	}
	data, err := ctx.decodeStreamCached(ff)
	if err != nil {
		return sfntCmaps{}, false
	}
	// unparseable program: reported as Font.InvalidProgram (6.3.2) by ValidateFontProgram
	tables, ok := fontprog.ParseSfnt(data)
	if !ok {
		return sfntCmaps{}, false
	}
	cmap, ok := tables["cmap"]
	if !ok || len(cmap) < 4 {
		return sfntCmaps{}, false
	}
	return sfntCmaps{
		count:   int(binary.BigEndian.Uint16(cmap[2:4])),
		symbol:  fontprog.TTCmapSubtable(tables, 3, 0) != nil,
		unicode: fontprog.TTCmapSubtable(tables, 3, 1) != nil,
	}, true
}

// type1ProgramFor memoizes ExtractType1Program per decoded font program for
// the lifetime of the validation, so the CharSet (6.3.5) and metrics (6.3.6)
// checks share one eexec decrypt and CharStrings scan instead of each
// running their own. Keyed by the decoded stream's backing array, which
// ctx.decodeStreamCached keeps alive and stable for the context's lifetime.
func (ctx *ValidationContext) type1ProgramFor(fontData []byte) fontprog.Type1Program {
	if len(fontData) == 0 {
		return fontprog.Type1Program{}
	}
	key := &fontData[0]
	if p, ok := ctx.type1Cache[key]; ok {
		return p
	}
	p := fontprog.ExtractType1Program(fontData, fontprog.Type1EexecBinStart(fontData))
	if ctx.type1Cache == nil {
		ctx.type1Cache = map[*byte]fontprog.Type1Program{}
	}
	ctx.type1Cache[key] = p
	return p
}

// validateType1Metrics checks that PDF Widths entries match advance widths in
// the embedded Type1 font program (6.3.6).
func validateType1Metrics(obj pdf.PDFValue, desc pdf.PDFDict, ff pdf.PDFDict, firstChar int, widths pdf.PDFArray, encoding pdf.PDFValue, ctx *ValidationContext) {
	fontData, err := ctx.decodeStreamCached(ff)
	if err != nil || len(fontData) == 0 {
		return
	}

	glyphWidths := ctx.type1ProgramFor(fontData).Widths
	enc, stats := fontprog.Type1WidthTable(fontData, encoding, glyphWidths)
	if stats.Skip != fontprog.WidthSkipNone {
		return
	}

	// Only codes actually shown, as on the Type1C path: a /Widths entry for a
	// code the document never draws says nothing about what gets rendered, and
	// the base encoding can put a real glyph name on one.
	var usedCodes map[int]bool
	knownUsage := false
	if fontDict, ok := obj.(pdf.PDFDict); ok {
		usedCodes, knownUsage = ctx.usedCodesFor(fontDict)
	}

	// A code outside /Widths takes its width from /MissingWidth, so a font
	// whose FirstChar starts past a code it shows is checked there rather than
	// not at all -- which is where the Isartor 6.3.6 case lives.
	if knownUsage {
		missing, _ := pdf.PDFNumberToInt(desc.Entries.Get("MissingWidth"))
		codes := make([]int, 0, len(usedCodes))
		for cc := range usedCodes {
			codes = append(codes, cc)
		}
		sort.Ints(codes)
		for _, cc := range codes {
			if cc >= firstChar && cc < firstChar+len(widths) {
				continue
			}
			if !reportType1WidthMismatch(obj, cc, missing, enc, glyphWidths, ctx) {
				return
			}
		}
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
		if knownUsage && !usedCodes[cc] {
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
		if math.Abs(float64(pdfWidth)-csWidth) > 1 {
			ctx.Report(pdf.Checks.Font.AdvanceWidthMismatch, obj, fmt.Sprintf("character code %d (/%s): PDF width %d ≠ Type1 advance width %s",
				cc, glyph, pdfWidth, formatWidth(csWidth)))
			return
		}
	}
}

// reportType1WidthMismatch compares one code's width against the program and
// reports the first disagreement. It returns false once it has reported, so
// the caller stops at one finding per font.
func reportType1WidthMismatch(obj pdf.PDFValue, cc, pdfWidth int, enc [256]string, glyphWidths map[string]float64, ctx *ValidationContext) bool {
	if cc < 0 || cc > 255 || pdfWidth == 0 {
		return true
	}
	glyph := enc[cc]
	if glyph == "" {
		return true
	}
	csWidth, found := glyphWidths[glyph]
	if !found || math.Abs(float64(pdfWidth)-csWidth) <= 1 {
		return true
	}
	ctx.Report(pdf.Checks.Font.AdvanceWidthMismatch, obj, fmt.Sprintf("character code %d (/%s): PDF width %d ≠ Type1 advance width %s",
		cc, glyph, pdfWidth, formatWidth(csWidth)))
	return false
}

// formatWidth renders an advance width for a message: a program's width need
// not be a whole number, and printing it rounded would make a real mismatch
// look like an off-by-one.
func formatWidth(w float64) string {
	return strconv.FormatFloat(w, 'g', -1, 64)
}

// validateCMapWMode flags an embedded CMap whose dictionary WMode disagrees with
// the WMode declared in its stream (6.3.3.3).
func validateCMapWMode(obj pdf.PDFValue, cmap pdf.PDFDict, ctx *ValidationContext) {
	if !cmap.IsStream() {
		return
	}
	dictWMode, ok := cmap.Entries.Get("WMode").(pdf.PDFInteger)
	if !ok {
		return
	}
	data, err := ctx.decodeStreamCached(cmap)
	if err != nil {
		return
	}
	m := fontprog.WmodeRe.FindSubmatch(data)
	if m == nil {
		return
	}
	streamWMode, _ := strconv.Atoi(string(m[1]))
	if int(dictWMode) != streamWMode {
		ctx.Report(pdf.Checks.Font.CMapWModeInconsistent, obj, "WMode in CMap dictionary and stream are inconsistent")
	}
}
