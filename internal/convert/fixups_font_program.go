package convert

import (
	"encoding/binary"
	"sort"
	"strings"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"

	"github.com/voidrab/gopdfrab/internal/verify"
)

// This file registers Fixers that repair font metadata using data already
// inside the embedded font program (6.3.5/6.3.6): advance widths, and
// Type1/CID subset glyph-name listings. The program is authoritative, so
// making PDF metadata agree with it never changes a glyph's shape -- unlike
// SubsetGlyphCoverage (a genuinely missing glyph), which needs re-subsetting
// and stays out of scope here.

func init() {
	registerFixer(fontMetricFixer{})
	registerFixer(fontSubsetMetaFixer{})
	registerPreemptiveFixup(promoteEmptyGlyphsInFonts)
}

// promoteEmptyGlyphsInFonts rewrites a CIDFontType2's embedded TrueType program
// so its blank glyphs are explicit zero-contour records.
func promoteEmptyGlyphsInFonts(trailer *pdf.PDFDict, _ *pdf.Reader) error {
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if (d.Entries["Subtype"] != pdf.PDFName{Value: "CIDFontType2"}) {
			return
		}
		desc, ok := d.Entries["FontDescriptor"].(pdf.PDFDict)
		if !ok {
			return
		}
		ff, ok := desc.Entries["FontFile2"].(pdf.PDFDict)
		if !ok || !ff.HasStream {
			return
		}
		data, err := pdf.DecodeStream(ff)
		if err != nil {
			return
		}
		repaired, changed := promoteEmptyGlyphs(data)
		if !changed {
			return
		}
		ff.Entries["Length1"] = pdf.PDFInteger(len(repaired))
		if err := writer.SetStreamFlate(&ff, repaired); err != nil {
			return
		}
		desc.Entries["FontFile2"] = ff
	})
	return nil
}

// fontMetricFixer remediates Checks.Font.AdvanceWidthMismatch by recomputing
// PDF /Widths (simple TrueType, Type1, Type1C, Type3) or /W (CIDFontType2,
// CIDFontType0) entries from the embedded font program, mirroring the
// detection in checks_font.go/checks_font_program.go.
type fontMetricFixer struct{}

func (fontMetricFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Font.AdvanceWidthMismatch
}

func (fontMetricFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if fixFontMetricsDict(d) {
			changed = true
		}
	})
	return changed, nil
}

// fixTargeted repairs only the font dicts the issues reference; the verifier
// reports every mismatching font per pass, so this covers all violations.
func (fontMetricFixer) fixTargeted(p *fixPass, issues []pdf.PDFError) (changed, handled bool, err error) {
	targets, ok := p.dictsForIssues(issues)
	if !ok {
		return false, false, nil
	}
	for _, d := range targets {
		if fixFontMetricsDict(d) {
			changed = true
		}
	}
	return changed, true, nil
}

// fixFontMetricsDict recomputes d's width metadata from its embedded font
// program if d is a font dict; it re-checks the predicate so a stale or
// already-fixed target is a no-op.
func fixFontMetricsDict(d pdf.PDFDict) bool {
	if (d.Entries["Type"] != pdf.PDFName{Value: "Font"}) {
		return false
	}
	subtype, _ := d.Entries["Subtype"].(pdf.PDFName)
	desc, _ := d.Entries["FontDescriptor"].(pdf.PDFDict)

	switch subtype.Value {
	case "TrueType":
		if ff, ok := desc.Entries["FontFile2"].(pdf.PDFDict); ok {
			return fixSimpleTrueTypeWidths(d, ff)
		}
	case "Type1", "MMType1":
		if ff, ok := desc.Entries["FontFile"].(pdf.PDFDict); ok {
			pdfEnc, _ := d.Entries["Encoding"].(pdf.PDFName)
			return fixType1Widths(d, ff, pdfEnc.Value)
		} else if ff, ok := desc.Entries["FontFile3"].(pdf.PDFDict); ok {
			return fixType1CWidths(d, ff)
		}
	case "CIDFontType2":
		if ff, ok := desc.Entries["FontFile2"].(pdf.PDFDict); ok {
			return fixCIDTrueTypeWidths(d, ff)
		}
	case "CIDFontType0":
		if ff, ok := desc.Entries["FontFile3"].(pdf.PDFDict); ok {
			return fixCIDCFFWidths(d, ff)
		}
	case "Type3":
		return fixType3Widths(d)
	}
	return false
}

// fixSimpleTrueTypeWidths rewrites mismatched /Widths entries to the
// embedded TrueType program's hmtx advance width, mirroring
// validateSimpleTrueTypeMetrics (checks_font_program.go).
func fixSimpleTrueTypeWidths(v pdf.PDFDict, ff pdf.PDFDict) bool {
	firstChar, fcOK := v.Entries["FirstChar"].(pdf.PDFInteger)
	widths, wOK := v.Entries["Widths"].(pdf.PDFArray)
	if !fcOK || !wOK {
		return false
	}
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		return false
	}
	tables, ok := verify.ParseSfnt(data)
	if !ok {
		return false
	}

	codeToUnicode := verify.SimpleFontCodeToUnicode(v.Entries["Encoding"])
	winGIDMap := verify.ParseCmapFormat4(verify.TTWindowsBMPCmap(tables))
	symGIDMap := verify.ParseCmapSubtable(verify.TTSymbolicCmap(tables))

	codeToGID := func(cc int) (int, bool) {
		if u := codeToUnicode[cc]; u != 0 && winGIDMap != nil {
			if gid, ok := winGIDMap[u]; ok {
				return int(gid), true
			}
			return 0, false
		}
		if symGIDMap != nil {
			for _, candidate := range [2]uint16{uint16(cc) | 0xF000, uint16(cc)} {
				if gid, ok := symGIDMap[candidate]; ok {
					return int(gid), true
				}
			}
		}
		return 0, false
	}

	changed := false
	for i, w := range widths {
		pdfWidth, ok := pdf.PDFNumberToInt(w)
		if !ok || pdfWidth == 0 {
			continue
		}
		cc := int(firstChar) + i
		if cc < 0 || cc > 255 {
			continue
		}
		gid, known := codeToGID(cc)
		if !known {
			continue
		}
		fontWidth := verify.TTAdvanceWidth(tables, gid)
		if fontWidth < 0 || pdf.AbsInt(fontWidth-pdfWidth) <= 1 {
			continue
		}
		widths[i] = pdf.PDFInteger(fontWidth)
		changed = true
	}
	return changed
}

// fixType1Widths rewrites mismatched /Widths entries to the embedded Type1
// program's advance width, mirroring validateType1Metrics
// (checks_font_program.go).
func fixType1Widths(v pdf.PDFDict, ff pdf.PDFDict, pdfEncoding string) bool {
	firstChar, fcOK := v.Entries["FirstChar"].(pdf.PDFInteger)
	widths, wOK := v.Entries["Widths"].(pdf.PDFArray)
	if !fcOK || !wOK {
		return false
	}
	fontData, err := pdf.DecodeStream(ff)
	if err != nil || len(fontData) == 0 {
		return false
	}
	enc, ok := verify.Type1EncodingTable(fontData, pdfEncoding)
	if !ok {
		return false
	}
	glyphWidths := verify.Type1GlyphWidths(fontData)
	if len(glyphWidths) == 0 {
		return false
	}

	changed := false
	for i, w := range widths {
		pdfWidth, ok := pdf.PDFNumberToInt(w)
		if !ok || pdfWidth == 0 {
			continue
		}
		cc := int(firstChar) + i
		if cc < 0 || cc > 255 {
			continue
		}
		glyph := enc[cc]
		if glyph == "" {
			continue
		}
		csWidth, found := glyphWidths[glyph]
		if !found || pdf.AbsInt(pdfWidth-csWidth) <= 1 {
			continue
		}
		widths[i] = pdf.PDFInteger(csWidth)
		changed = true
	}
	return changed
}

// fixType1CWidths rewrites mismatched /Widths entries to the embedded CFF
// program's charstring advance width, mirroring validateType1CMetrics
// (checks_font_program.go).
func fixType1CWidths(v pdf.PDFDict, ff pdf.PDFDict) bool {
	firstChar, fcOK := v.Entries["FirstChar"].(pdf.PDFInteger)
	widths, wOK := v.Entries["Widths"].(pdf.PDFArray)
	if !fcOK || !wOK {
		return false
	}
	data, err := pdf.DecodeStream(ff)
	if err != nil || len(data) == 0 {
		return false
	}
	glyphWidths := verify.CFFAdvanceWidths(data)
	if len(glyphWidths) == 0 {
		return false
	}
	glyphNames, ok := verify.SimpleFontGlyphNameTable(v)
	if !ok {
		return false
	}

	changed := false
	for i, w := range widths {
		pdfWidth, ok := pdf.PDFNumberToInt(w)
		if !ok || pdfWidth == 0 {
			continue
		}
		cc := int(firstChar) + i
		if cc < 0 || cc > 255 {
			continue
		}
		glyph := glyphNames[cc]
		if glyph == "" {
			continue
		}
		csWidth, found := glyphWidths[glyph]
		if !found || pdf.AbsInt(pdfWidth-csWidth) <= 1 {
			continue
		}
		widths[i] = pdf.PDFInteger(csWidth)
		changed = true
	}
	return changed
}

// fixCIDCFFWidths rewrites mismatched /W entries to the embedded CID-keyed
// CFF program's charstring advance width, mirroring validateCIDCFFMetrics
// (checks_font_program.go).
func fixCIDCFFWidths(v pdf.PDFDict, ff pdf.PDFDict) bool {
	w, ok := v.Entries["W"].(pdf.PDFArray)
	if !ok {
		return false
	}
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		return false
	}
	cidWidths := verify.CFFCIDAdvanceWidths(data)
	if len(cidWidths) == 0 {
		return false
	}

	pairs := verify.ParseCIDWidths(w)
	changed := false
	for i, pair := range pairs {
		csWidth, found := cidWidths[pair[0]]
		if !found || pdf.AbsInt(csWidth-pair[1]) <= 1 {
			continue
		}
		pairs[i][1] = csWidth
		changed = true
	}
	if !changed {
		return false
	}
	v.Entries["W"] = buildCIDWidthsArray(pairs)
	return true
}

// fixCIDTrueTypeWidths rewrites mismatched /W entries to the embedded
// TrueType program's hmtx advance width, mirroring validateCIDTrueTypeMetrics
// (checks_font_program.go).
func fixCIDTrueTypeWidths(v pdf.PDFDict, ff pdf.PDFDict) bool {
	w, ok := v.Entries["W"].(pdf.PDFArray)
	if !ok {
		return false
	}
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		return false
	}
	tables, ok := verify.ParseSfnt(data)
	if !ok {
		return false
	}

	pairs := verify.ParseCIDWidths(w)
	changed := false
	for i, pair := range pairs {
		fontWidth := verify.TTAdvanceWidth(tables, pair[0])
		if fontWidth < 0 || pdf.AbsInt(fontWidth-pair[1]) <= 1 {
			continue
		}
		pairs[i][1] = fontWidth
		changed = true
	}
	if !changed {
		return false
	}
	v.Entries["W"] = buildCIDWidthsArray(pairs)
	return true
}

// buildCIDWidthsArray serializes (cid, width) pairs as a /W array, grouping
// consecutive CIDs into a single "c1 [w1 w2 ...]" entry so
// parseCIDWidths (checks_font_program.go) can re-parse it unchanged.
func buildCIDWidthsArray(pairs [][2]int) pdf.PDFArray {
	var out pdf.PDFArray
	i := 0
	for i < len(pairs) {
		start := pairs[i][0]
		var widths pdf.PDFArray
		j := i
		for j < len(pairs) && pairs[j][0] == start+(j-i) {
			widths = append(widths, pdf.PDFInteger(pairs[j][1]))
			j++
		}
		out = append(out, pdf.PDFInteger(start), widths)
		i = j
	}
	return out
}

// fixType3Widths rewrites mismatched /Widths entries to each glyph
// procedure's own d0/d1 width, mirroring validateType3Metrics
// (checks_font.go).
func fixType3Widths(v pdf.PDFDict) bool {
	firstChar, fcOK := v.Entries["FirstChar"].(pdf.PDFInteger)
	widths, wOK := v.Entries["Widths"].(pdf.PDFArray)
	charProcs, cpOK := v.Entries["CharProcs"].(pdf.PDFDict)
	enc, encOK := v.Entries["Encoding"].(pdf.PDFDict)
	if !fcOK || !wOK || !cpOK || !encOK {
		return false
	}
	diffs, _ := enc.Entries["Differences"].(pdf.PDFArray)
	if diffs == nil {
		return false
	}

	codeToGlyph := map[int]string{}
	code := 0
	for _, item := range diffs {
		switch d := item.(type) {
		case pdf.PDFInteger:
			code = int(d)
		case pdf.PDFName:
			codeToGlyph[code] = d.Value
			code++
		}
	}

	changed := false
	for i, w := range widths {
		var pdfWidth float64
		switch wv := w.(type) {
		case pdf.PDFInteger:
			pdfWidth = float64(wv)
		case pdf.PDFReal:
			pdfWidth = float64(wv)
		default:
			continue
		}
		glyphName := codeToGlyph[int(firstChar)+i]
		if glyphName == "" {
			continue
		}
		proc, ok := charProcs.Entries[glyphName].(pdf.PDFDict)
		if !ok || !proc.HasStream {
			continue
		}
		data, err := pdf.DecodeStream(proc)
		if err != nil {
			continue
		}
		procWidth := verify.Type3GlyphWidth(data)
		if procWidth < 0 || verify.Abs64(procWidth-pdfWidth) <= 1.0 {
			continue
		}
		if _, isInt := w.(pdf.PDFInteger); isInt {
			widths[i] = pdf.PDFInteger(int(procWidth))
		} else {
			widths[i] = pdf.PDFReal(procWidth)
		}
		changed = true
	}
	return changed
}

// fontSubsetMetaFixer remediates Checks.Font.Type1SubsetCharSet and
// Checks.Font.CIDSubsetCIDSet by synthesizing the missing/incomplete
// /CharSet or /CIDSet from the glyphs actually present in the embedded
// program, mirroring validateType1SubsetCoverage's CharSet-presence check
// and validateCIDSetBitmap (checks_font.go/checks_font_program.go).
type fontSubsetMetaFixer struct{}

func (fontSubsetMetaFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Font.Type1SubsetCharSet, pdf.Checks.Font.CIDSubsetCIDSet:
		return true
	}
	return false
}

func (fontSubsetMetaFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if fixFontSubsetMetaDict(d) {
			changed = true
		}
	})
	return changed, nil
}

// fixTargeted regenerates subset metadata only for the font dicts the issues
// reference, falling back to the full walk when any issue lacks a ref.
func (fontSubsetMetaFixer) fixTargeted(p *fixPass, issues []pdf.PDFError) (changed, handled bool, err error) {
	targets, ok := p.dictsForIssues(issues)
	if !ok {
		return false, false, nil
	}
	for _, d := range targets {
		if fixFontSubsetMetaDict(d) {
			changed = true
		}
	}
	return changed, true, nil
}

// fixFontSubsetMetaDict synthesizes /CharSet or /CIDSet for a subset font
// dict; it re-checks the predicate so a stale or already-fixed target is a
// no-op.
func fixFontSubsetMetaDict(d pdf.PDFDict) bool {
	if (d.Entries["Type"] != pdf.PDFName{Value: "Font"}) {
		return false
	}
	baseFont, _ := d.Entries["BaseFont"].(pdf.PDFName)
	if !verify.SubsetTagRe.MatchString(baseFont.Value) {
		return false
	}
	desc, ok := d.Entries["FontDescriptor"].(pdf.PDFDict)
	if !ok || desc.Entries == nil {
		return false
	}

	subtype, _ := d.Entries["Subtype"].(pdf.PDFName)
	switch subtype.Value {
	case "Type1", "MMType1":
		return fixType1CharSet(desc)
	case "CIDFontType0":
		if ff, ok := desc.Entries["FontFile3"].(pdf.PDFDict); ok {
			return fixCFFCIDSet(desc, ff)
		}
	case "CIDFontType2":
		if ff, ok := desc.Entries["FontFile2"].(pdf.PDFDict); ok {
			return fixTrueTypeCIDSet(d, desc, ff)
		}
	}
	return false
}

// fixType1CharSet synthesizes /CharSet from the glyph names defined in the
// embedded program -- a raw Type1 program's CharStrings dict (FontFile) or a
// name-keyed CFF program's charset (FontFile3, "Type1C") -- when CharSet is
// missing, empty, or lists a glyph the program does not define, mirroring the
// checks in validateFontDict/ValidateType1SubsetCoverage.
func fixType1CharSet(desc pdf.PDFDict) bool {
	var names []string
	switch {
	case desc.Entries["FontFile"] != nil:
		ff := desc.Entries["FontFile"].(pdf.PDFDict)
		fontData, err := pdf.DecodeStream(ff)
		if err != nil || len(fontData) == 0 {
			return false
		}
		names = verify.Type1GlyphNames(fontData)
	case desc.Entries["FontFile3"] != nil:
		ff := desc.Entries["FontFile3"].(pdf.PDFDict)
		data, err := pdf.DecodeStream(ff)
		if err != nil {
			return false
		}
		names = verify.CFFGlyphNames(data)
	default:
		return false
	}
	if len(names) == 0 {
		return false
	}
	if current, ok := desc.Entries["CharSet"].(pdf.PDFString); ok && current.Value != "" && charSetConsistent(current.Value, names) {
		return false
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteByte('/')
		b.WriteString(n)
	}
	desc.Entries["CharSet"] = pdf.PDFString{Value: b.String()}
	return true
}

// charSetConsistent reports whether a CharSet string lists every glyph name
// the embedded program defines (extra names are tolerated, per 6.3.5-2).
func charSetConsistent(charSet string, programNames []string) bool {
	listed := map[string]bool{".notdef": true}
	for _, part := range verify.SplitCharSetNames(charSet) {
		listed[part] = true
	}
	for _, n := range programNames {
		if !listed[n] {
			return false
		}
	}
	return true
}

// buildCIDSetBitmap encodes cids as a CIDSet bitmap: bit 7-cid%8 of byte
// cid/8 is set for every CID present, matching the layout validateCIDSetBitmap
// (checks_font_program.go) reads.
func buildCIDSetBitmap(cids []int) []byte {
	maxCID := 0
	for _, cid := range cids {
		if cid > maxCID {
			maxCID = cid
		}
	}
	bitmap := make([]byte, maxCID/8+1)
	for _, cid := range cids {
		bitmap[cid/8] |= 1 << (7 - cid%8)
	}
	return bitmap
}

// cidSetComplete reports whether bitmap already marks every CID in cids,
// mirroring validateCIDSetBitmap's own completeness
func cidSetComplete(bitmap []byte, cids []int) bool {
	for _, cid := range cids {
		byteIdx, bitIdx := cid/8, 7-cid%8
		if byteIdx >= len(bitmap) || bitmap[byteIdx]&(1<<bitIdx) == 0 {
			return false
		}
	}
	return true
}

// fixCFFCIDSet synthesizes or completes /CIDSet from the CID-keyed CFF
// program's charset, mirroring validateCIDSetBitmap (checks_font_program.go).
func fixCFFCIDSet(desc pdf.PDFDict, ff pdf.PDFDict) bool {
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		return false
	}
	td, ok := verify.ParseCFFTopDict(data)
	if !ok || !td.IsCIDKeyed || td.CSOffset < 0 || td.CSOffset+2 > len(data) {
		return false
	}
	csCount := int(binary.BigEndian.Uint16(data[td.CSOffset : td.CSOffset+2]))
	cids := verify.ParseCFFCharsetCIDs(data, td.CharsetOffset, csCount)
	if cids == nil {
		return false
	}

	if current, ok := desc.Entries["CIDSet"].(pdf.PDFDict); ok && current.HasStream {
		if existing, err := pdf.DecodeStream(current); err == nil && cidSetComplete(existing, cids) {
			return false
		}
	}

	stream := pdf.NewPDFDict()
	if err := writer.SetStreamFlate(&stream, buildCIDSetBitmap(cids)); err != nil {
		return false
	}
	desc.Entries["CIDSet"] = stream
	return true
}

// fixTrueTypeCIDSet synthesizes or completes /CIDSet from the glyphs present
// in a CIDFontType2 program. it handles both a missing CIDSet and an existing
// one that omits a present glyph.
func fixTrueTypeCIDSet(d, desc pdf.PDFDict, ff pdf.PDFDict) bool {
	// Only safe when CID==GID (the spec default); a stream CIDToGIDMap means
	// CIDs don't correspond to GIDs directly.
	if c2g := d.Entries["CIDToGIDMap"]; c2g != nil && c2g != (pdf.PDFName{Value: "Identity"}) {
		return false
	}
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		return false
	}
	tables, ok := verify.ParseSfnt(data)
	if !ok {
		return false
	}
	numGlyphs := verify.TTNumGlyphs(tables)
	if numGlyphs <= 0 {
		return false
	}
	glyphPresent := verify.TTGlyphPresent(tables)
	var cids []int
	for gid := range numGlyphs {
		if glyphPresent(gid) {
			cids = append(cids, gid)
		}
	}
	if len(cids) == 0 {
		return false
	}

	if current, ok := desc.Entries["CIDSet"].(pdf.PDFDict); ok && current.HasStream {
		if existing, err := pdf.DecodeStream(current); err == nil && cidSetComplete(existing, cids) {
			return false
		}
	}

	stream := pdf.NewPDFDict()
	if err := writer.SetStreamFlate(&stream, buildCIDSetBitmap(cids)); err != nil {
		return false
	}
	desc.Entries["CIDSet"] = stream
	return true
}
