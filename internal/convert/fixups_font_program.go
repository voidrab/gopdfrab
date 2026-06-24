package convert

import (
	"encoding/binary"
	"sort"
	"strings"

	"github.com/voidrab/gopdfrab/internal/check"
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
}

// fontMetricFixer remediates check.Checks.Font.AdvanceWidthMismatch by recomputing
// PDF /Widths (simple TrueType, Type1, Type3) or /W (CIDFontType2) entries
// from the embedded font program, mirroring the detection in
// validateSimpleTrueTypeMetrics/validateType1Metrics/validateType3Metrics
// (checks_font.go/checks_font_program.go) and validateCIDTrueTypeMetrics.
// CIDFontType0 (CFF) has no width check today -- no CFF charstring width
// reader exists -- so it needs no handling here.
type fontMetricFixer struct{}

func (fontMetricFixer) Applies(c check.Check) bool {
	return c == check.Checks.Font.AdvanceWidthMismatch
}

func (fontMetricFixer) Fix(trailer *pdf.PDFDict, issues []check.PDFError) (bool, error) {
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if (d.Entries["Type"] != pdf.PDFName{Value: "Font"}) {
			return
		}
		subtype, _ := d.Entries["Subtype"].(pdf.PDFName)
		baseFont, _ := d.Entries["BaseFont"].(pdf.PDFName)
		desc, _ := d.Entries["FontDescriptor"].(pdf.PDFDict)

		switch subtype.Value {
		case "TrueType":
			if ff, ok := desc.Entries["FontFile2"].(pdf.PDFDict); ok {
				if fixSimpleTrueTypeWidths(d, ff) {
					changed = true
				}
			}
		case "Type1", "MMType1":
			// validateType1Metrics only runs for non-subset Type1 fonts
			// (subset Type1 fonts are checked for CharSet coverage instead).
			if !verify.SubsetTagRe.MatchString(baseFont.Value) {
				if ff, ok := desc.Entries["FontFile"].(pdf.PDFDict); ok {
					pdfEnc, _ := d.Entries["Encoding"].(pdf.PDFName)
					if fixType1Widths(d, ff, pdfEnc.Value) {
						changed = true
					}
				}
			}
		case "CIDFontType2":
			if ff, ok := desc.Entries["FontFile2"].(pdf.PDFDict); ok {
				if fixCIDTrueTypeWidths(d, ff) {
					changed = true
				}
			}
		case "Type3":
			if fixType3Widths(d) {
				changed = true
			}
		}
	})
	return changed, nil
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
	gidMap := verify.ParseCmapFormat4(verify.TTWindowsBMPCmap(tables))
	if gidMap == nil {
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
		gid, exists := gidMap[verify.WinAnsiToUnicode[cc]]
		if !exists {
			continue
		}
		fontWidth := verify.TTAdvanceWidth(tables, int(gid))
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

// fontSubsetMetaFixer remediates check.Checks.Font.Type1SubsetCharSet and
// check.Checks.Font.CIDSubsetCIDSet by synthesizing the missing/incomplete
// /CharSet or /CIDSet from the glyphs actually present in the embedded
// program, mirroring validateType1SubsetCoverage's CharSet-presence check
// and validateCIDSetBitmap (checks_font.go/checks_font_program.go).
type fontSubsetMetaFixer struct{}

func (fontSubsetMetaFixer) Applies(c check.Check) bool {
	switch c {
	case check.Checks.Font.Type1SubsetCharSet, check.Checks.Font.CIDSubsetCIDSet:
		return true
	}
	return false
}

func (fontSubsetMetaFixer) Fix(trailer *pdf.PDFDict, issues []check.PDFError) (bool, error) {
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if (d.Entries["Type"] != pdf.PDFName{Value: "Font"}) {
			return
		}
		baseFont, _ := d.Entries["BaseFont"].(pdf.PDFName)
		if !verify.SubsetTagRe.MatchString(baseFont.Value) {
			return
		}
		desc, ok := d.Entries["FontDescriptor"].(pdf.PDFDict)
		if !ok || desc.Entries == nil {
			return
		}

		subtype, _ := d.Entries["Subtype"].(pdf.PDFName)
		switch subtype.Value {
		case "Type1", "MMType1":
			if fixType1CharSet(desc) {
				changed = true
			}
		case "CIDFontType0":
			if ff, ok := desc.Entries["FontFile3"].(pdf.PDFDict); ok {
				if fixCFFCIDSet(desc, ff) {
					changed = true
				}
			}
		case "CIDFontType2":
			if ff, ok := desc.Entries["FontFile2"].(pdf.PDFDict); ok {
				if fixTrueTypeCIDSet(d, desc, ff) {
					changed = true
				}
			}
		}
	})
	return changed, nil
}

// fixType1CharSet synthesizes /CharSet from the glyph names defined in the
// embedded program -- a raw Type1 program's CharStrings dict (FontFile) or a
// name-keyed CFF program's charset (FontFile3, "Type1C") -- mirroring the
// CharSet-presence/emptiness check in
// validateFontDict/validateType1SubsetCoverage.
func fixType1CharSet(desc pdf.PDFDict) bool {
	current, hasCurrent := desc.Entries["CharSet"].(pdf.PDFString)
	if hasCurrent && current.Value != "" {
		return false
	}
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
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteByte('/')
		b.WriteString(n)
	}
	desc.Entries["CharSet"] = pdf.PDFString{Value: b.String()}
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
// mirroring validateCIDSetBitmap's own completeness check.
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
	stream.HasStream = true
	stream.RawStream = buildCIDSetBitmap(cids)
	writer.MarkStreamDirty(&stream)
	desc.Entries["CIDSet"] = stream
	return true
}

// fixTrueTypeCIDSet synthesizes /CIDSet from the glyphs present in a
// CIDFontType2 program, mirroring the CIDSet-presence check in
// validateFontDict (checks_font.go). Unlike CIDFontType0/CFF, no checker
// validates an existing CIDSet's completeness for CIDFontType2, so this only
// handles the missing case.
func fixTrueTypeCIDSet(d, desc pdf.PDFDict, ff pdf.PDFDict) bool {
	if desc.Entries["CIDSet"] != nil {
		return false
	}
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

	stream := pdf.NewPDFDict()
	stream.HasStream = true
	stream.RawStream = buildCIDSetBitmap(cids)
	writer.MarkStreamDirty(&stream)
	desc.Entries["CIDSet"] = stream
	return true
}
