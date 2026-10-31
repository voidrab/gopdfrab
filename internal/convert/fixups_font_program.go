package convert

import (
	"encoding/binary"
	"math"
	"sort"
	"strings"

	"github.com/voidrab/gopdfrab/internal/fontedit"
	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
	"github.com/voidrab/gopdfrab/internal/writer"

	"github.com/voidrab/gopdfrab/internal/verify"
)

// This file registers Fixers that repair font metadata using data already
// inside the embedded font program (6.3.5/6.3.6): advance widths, and
// Type1/CID subset glyph-name listings. The program is authoritative, so
// making PDF metadata agree with it never changes a glyph's shape -- unlike
// SubsetGlyphCoverage (a genuinely missing glyph), which needs re-subsetting
// and stays out of scope here.

// normalizeCFFWidthDefaultsInFont rewrites an embedded bare CFF program,
// name-keyed or CID-keyed, whose nominalWidthX or defaultWidthX is fractional
// so every one is whole and every glyph keeps its width
// (fontedit.NormalizeCFFWidthDefaults). veraPDF truncates a real
// nominalWidthX, so it otherwise reads every width off by the fraction and
// reports 6.3.6 against a correct /Widths or /W.
// d is any dict: only a font descriptor carries /FontFile3.
func normalizeCFFWidthDefaultsInFont(d pdf.PDFDict) {
	ff, ok := d.Entries.Get("FontFile3").(pdf.PDFDict)
	if !ok || !ff.IsStream() {
		return
	}
	switch ff.Entries.Get("Subtype") {
	case pdf.PDFName{Value: "Type1C"}, pdf.PDFName{Value: "CIDFontType0C"}:
	default:
		return
	}
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		return
	}
	rewritten, changed := fontedit.NormalizeCFFWidthDefaults(data)
	if !changed {
		return
	}
	writer.SetStreamFlate(&ff, rewritten)
	d.Entries.Set("FontFile3", ff)
}

// inlineType1WidthSubrsInFont rewrites a Type 1 font's embedded FontFile so
// every glyph carries its hsbw/sbw itself rather than in a called subr
// (fontedit.InlineType1WidthSubrs). Rendering is unchanged; veraPDF, which
// does not follow callsubr when reading a width, otherwise reads 0 and reports
// 6.3.5/6.3.6 against a correct font.
func inlineType1WidthSubrsInFont(d pdf.PDFDict) {
	switch d.Entries.Get("Subtype") {
	case pdf.PDFName{Value: "Type1"}, pdf.PDFName{Value: "MMType1"}:
	default:
		return
	}
	desc, _ := d.Entries.Get("FontDescriptor").(pdf.PDFDict)
	ff, ok := desc.Entries.Get("FontFile").(pdf.PDFDict)
	if !ok || !ff.IsStream() {
		return
	}
	length1, ok1 := ff.Entries.Get("Length1").(pdf.PDFInteger)
	length2, ok2 := ff.Entries.Get("Length2").(pdf.PDFInteger)
	if !ok1 || !ok2 {
		return
	}
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		return
	}
	rewritten, newLength2, changed := fontedit.InlineType1WidthSubrs(data, int(length1), int(length2))
	if !changed {
		return
	}
	ff.Entries.Set("Length2", pdf.PDFInteger(newLength2))
	writer.SetStreamFlate(&ff, rewritten)
	desc.Entries.Set("FontFile", ff)
}

// repairNonSymbolicTrueTypeCmap gives a non-symbolic (encoded) simple
// TrueType font a (3,1) cmap mapping for every code in FirstChar..LastChar
// that ISO 32000-1 9.6.6.4 cannot resolve but the raw-code lookup lenient
// viewers fall back to can. Strict readers -- verify, veraPDF -- otherwise
// render .notdef and report 6.3.5 for a glyph the font has; the mapping points
// at the very glyph lenient viewers already show, so no page changes.
func repairNonSymbolicTrueTypeCmap(d pdf.PDFDict) {
	if (d.Entries.Get("Subtype") != pdf.PDFName{Value: "TrueType"}) {
		return
	}
	enc := d.Entries.Get("Encoding")
	desc, _ := d.Entries.Get("FontDescriptor").(pdf.PDFDict)
	ff, ok := desc.Entries.Get("FontFile2").(pdf.PDFDict)
	if enc == nil || !ok || !ff.IsStream() {
		return
	}
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		return
	}
	tables, ok := fontprog.ParseSfnt(data)
	if !ok {
		return
	}
	r := fontprog.NewSimpleTrueTypeCmaps(enc, tables)
	first, lo := d.Entries.Get("FirstChar").(pdf.PDFInteger)
	last, hi := d.Entries.Get("LastChar").(pdf.PDFInteger)
	if !lo || !hi {
		first, last = 0, 255
	}
	add := map[uint16]uint16{}
	for cc := max(int(first), 0); cc <= min(int(last), 255); cc++ {
		u := r.CodeToUnicode[cc]
		if u == 0 {
			continue
		}
		if _, _, found := r.Windows(cc); found {
			continue
		}
		if _, found := r.Fallback(cc); found {
			continue
		}
		if gid, found := r.RawCode(cc); found && gid != 0 {
			add[u] = uint16(gid)
		}
	}
	repaired, changed := fontedit.AddWindowsUnicodeCmap(data, add)
	if !changed {
		return
	}
	ff.Entries.Set("Length1", pdf.PDFInteger(len(repaired)))
	writer.SetStreamFlate(&ff, repaired)
	desc.Entries.Set("FontFile2", ff)
}

// promoteEmptyGlyphsInFonts rewrites every CIDFontType2's embedded TrueType
// program so its blank glyphs are explicit zero-contour records. The shared
// pre-emptive walk drives promoteEmptyGlyphsInFont per dict; this standalone
// form remains for direct use.
func promoteEmptyGlyphsInFonts(trailer *pdf.PDFDict, _ *pdf.Reader) error {
	walkDicts(*trailer, map[uintptr]bool{}, promoteEmptyGlyphsInFont)
	return nil
}

func promoteEmptyGlyphsInFont(d pdf.PDFDict) {
	if (d.Entries.Get("Subtype") != pdf.PDFName{Value: "CIDFontType2"}) {
		return
	}
	desc, ok := d.Entries.Get("FontDescriptor").(pdf.PDFDict)
	if !ok {
		return
	}
	ff, ok := desc.Entries.Get("FontFile2").(pdf.PDFDict)
	if !ok || !ff.IsStream() {
		return
	}
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		return
	}
	repaired, changed := fontedit.PromoteEmptyGlyphs(data)
	if !changed {
		return
	}
	ff.Entries.Set("Length1", pdf.PDFInteger(len(repaired)))
	writer.SetStreamFlate(&ff, repaired)
	desc.Entries.Set("FontFile2", ff)
}

// fontFileSubtypeFixer remediates Checks.Font.FontFileSubtype by unwrapping an
// embedded font file whose declared format PDF 1.4 does not define -- almost
// always an OpenType wrapper. The glyphs inside are kept: an OpenType file is
// a container, and the CFF or TrueType program it carries is exactly what a
// PDF 1.4 font file holds directly. Substituting a bundled face instead would
// throw away the document's own glyphs, so anything this cannot unwrap is left
// alone for the substitution fixer to pick up as an unusable program.
type fontFileSubtypeFixer struct{ spec *pdfa.Spec }

func (fontFileSubtypeFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Font.FontFileSubtype
}

func (f fontFileSubtypeFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	return runDictVisitor(trailer, issues, f.prepare)
}

func (f fontFileSubtypeFixer) prepare(_ *pdf.PDFDict, _ []pdf.PDFError, changed *bool) (func(pdf.PDFDict), bool) {
	return func(d pdf.PDFDict) {
		if fixFontFileSubtypeDict(d, f.spec) {
			*changed = true
		}
	}, true
}

// fixFontFileSubtypeDict repairs the embedded font files of one font dict; it
// re-checks the predicate so a stale or already-fixed target is a no-op.
func fixFontFileSubtypeDict(d pdf.PDFDict, spec *pdfa.Spec) bool {
	if (d.Entries.Get("Type") != pdf.PDFName{Value: "Font"}) {
		return false
	}
	desc, ok := d.Entries.Get("FontDescriptor").(pdf.PDFDict)
	if !ok || desc.Entries == nil {
		return false
	}
	subtype, _ := d.Entries.Get("Subtype").(pdf.PDFName)

	changed := false
	// FontFile and FontFile2 have no Subtype key at all, so one there is
	// spurious and simply goes away.
	for _, key := range []string{"FontFile", "FontFile2"} {
		ff, ok := desc.Entries.Get(key).(pdf.PDFDict)
		if !ok || !badFontFileSubtype(ff, spec) {
			continue
		}
		ff.Entries.Del("Subtype")
		changed = true
	}
	if ff, ok := desc.Entries.Get("FontFile3").(pdf.PDFDict); ok && badFontFileSubtype(ff, spec) {
		if unwrapFontFile3(desc, ff, subtype.Value) {
			changed = true
		}
	}
	return changed
}

// badFontFileSubtype reports whether ff declares a font file format the level
// does not allow, mirroring the detection in ValidateFontProgram.
func badFontFileSubtype(ff pdf.PDFDict, spec *pdfa.Spec) bool {
	st, ok := ff.Entries.Get("Subtype").(pdf.PDFName)
	return ok && !spec.FontFileSubtypes.Has(st.Value)
}

// unwrapFontFile3 replaces a wrongly-typed FontFile3 with the program it
// actually holds: the CFF table becomes the stream itself, and a TrueType
// program moves to FontFile2 where it belongs. fontSubtype is the owning font
// dictionary's Subtype.
func unwrapFontFile3(desc, ff pdf.PDFDict, fontSubtype string) bool {
	data, err := pdf.DecodeStream(ff)
	if err != nil || len(data) == 0 {
		return false
	}

	if cff := fontprog.ExtractCFFBytes(data); cff != nil {
		want := "Type1C"
		td, ok := fontprog.ParseCFFTopDict(cff)
		if fontSubtype == "CIDFontType0" || (ok && td.IsCIDKeyed) {
			want = "CIDFontType0C"
		}
		// A bare CFF that only misnames itself needs no re-encoding.
		if len(cff) != len(data) {
			writer.SetStreamFlate(&ff, cff)
		}
		ff.Entries.Set("Subtype", pdf.PDFName{Value: want})
		desc.Entries.Set("FontFile3", ff)
		return true
	}

	// A TrueType-flavoured wrapper is only worth moving when the font is one
	// that reads TrueType glyphs; under any other font it would stop counting
	// as embedded, which the substitution fixer handles better.
	if fontSubtype != "TrueType" && fontSubtype != "CIDFontType2" {
		return false
	}
	tables, ok := fontprog.ParseSfnt(data)
	if !ok || tables["glyf"] == nil {
		return false
	}
	ff.Entries.Del("Subtype")
	ff.Entries.Set("Length1", pdf.PDFInteger(len(data)))
	writer.SetStreamFlate(&ff, data)
	desc.Entries.Set("FontFile2", ff)
	desc.Entries.Del("FontFile3")
	return true
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
	if (d.Entries.Get("Type") != pdf.PDFName{Value: "Font"}) {
		return false
	}
	subtype, _ := d.Entries.Get("Subtype").(pdf.PDFName)
	desc, _ := d.Entries.Get("FontDescriptor").(pdf.PDFDict)

	switch subtype.Value {
	case "TrueType":
		if ff, ok := desc.Entries.Get("FontFile2").(pdf.PDFDict); ok {
			return fixSimpleTrueTypeWidths(d, ff)
		}
	case "Type1", "MMType1":
		if ff, ok := desc.Entries.Get("FontFile").(pdf.PDFDict); ok {
			return fixType1Widths(d, desc, ff, d.Entries.Get("Encoding"))
		} else if ff, ok := desc.Entries.Get("FontFile3").(pdf.PDFDict); ok {
			return fixType1CWidths(d, ff)
		}
	case "CIDFontType2":
		if ff, ok := desc.Entries.Get("FontFile2").(pdf.PDFDict); ok {
			return fixCIDTrueTypeWidths(d, ff)
		}
	case "CIDFontType0":
		if ff, ok := desc.Entries.Get("FontFile3").(pdf.PDFDict); ok {
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
	firstChar, fcOK := v.Entries.Get("FirstChar").(pdf.PDFInteger)
	widths, wOK := v.Entries.Get("Widths").(pdf.PDFArray)
	if !fcOK || !wOK {
		return false
	}
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		return false
	}
	tables, ok := fontprog.ParseSfnt(data)
	if !ok {
		return false
	}

	r := fontprog.NewSimpleTrueTypeCmaps(v.Entries.Get("Encoding"), tables)

	codeToGID := func(cc int) (int, bool) {
		if gid, tried, found := r.Windows(cc); tried {
			return gid, found
		}
		return r.Fallback(cc)
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
		fontWidth := fontprog.TTAdvanceWidth(tables, gid)
		if fontWidth < 0 || math.Abs(fontWidth-float64(pdfWidth)) <= 1 {
			continue
		}
		widths[i] = pdf.PDFInteger(math.Round(fontWidth))
		changed = true
	}
	return changed
}

// fixType1Widths rewrites mismatched /Widths entries to the embedded Type1
// program's advance width, mirroring validateType1Metrics
// (checks_font_program.go).
func fixType1Widths(v pdf.PDFDict, desc pdf.PDFDict, ff pdf.PDFDict, encoding pdf.PDFValue) bool {
	firstChar, fcOK := v.Entries.Get("FirstChar").(pdf.PDFInteger)
	widths, wOK := v.Entries.Get("Widths").(pdf.PDFArray)
	if !fcOK || !wOK {
		return false
	}
	fontData, err := pdf.DecodeStream(ff)
	if err != nil || len(fontData) == 0 {
		return false
	}
	enc, ok := fontprog.Type1GlyphNameTable(fontData, encoding)
	if !ok {
		return false
	}
	glyphWidths := fontprog.Type1GlyphWidths(fontData)
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
		if !found || math.Abs(float64(pdfWidth)-csWidth) <= 1 {
			continue
		}
		widths[i] = pdf.PDFInteger(math.Round(csWidth))
		changed = true
	}
	return growType1Widths(v, desc, enc, glyphWidths, int(firstChar), widths) || changed
}

// growType1Widths extends /Widths over the codes outside it whose width would
// come from a declared /MissingWidth that the program disagrees with. Only a
// non-zero /MissingWidth is repaired: an absent one is 0, which the width check
// reads as "no width declared" and never compares.
func growType1Widths(v pdf.PDFDict, desc pdf.PDFDict, enc [256]string, glyphWidths map[string]float64, firstChar int, widths pdf.PDFArray) bool {
	missing, _ := pdf.PDFNumberToInt(desc.Entries.Get("MissingWidth"))
	if missing == 0 || len(widths) == 0 {
		return false
	}
	programWidth := func(cc int) (float64, bool) {
		if enc[cc] == "" {
			return 0, false
		}
		w, ok := glyphWidths[enc[cc]]
		return w, ok
	}

	lo, hi := firstChar, firstChar+len(widths)-1
	newLo, newHi := lo, hi
	for cc := 0; cc < 256; cc++ {
		if cc >= lo && cc <= hi {
			continue
		}
		w, ok := programWidth(cc)
		if !ok || math.Abs(float64(missing)-w) <= 1 {
			continue
		}
		newLo = min(newLo, cc)
		newHi = max(newHi, cc)
	}
	if newLo == lo && newHi == hi {
		return false
	}

	grown := make(pdf.PDFArray, newHi-newLo+1)
	for cc := newLo; cc <= newHi; cc++ {
		switch w, ok := programWidth(cc); {
		case cc >= lo && cc <= hi:
			grown[cc-newLo] = widths[cc-lo]
		case ok:
			grown[cc-newLo] = pdf.PDFInteger(math.Round(w))
		default:
			grown[cc-newLo] = pdf.PDFInteger(missing)
		}
	}
	v.Entries.Set("FirstChar", pdf.PDFInteger(newLo))
	v.Entries.Set("LastChar", pdf.PDFInteger(newHi))
	v.Entries.Set("Widths", grown)
	return true
}

// fixType1CWidths rewrites mismatched /Widths entries to the embedded CFF
// program's charstring advance width, mirroring validateType1CMetrics
// (checks_font_program.go).
func fixType1CWidths(v pdf.PDFDict, ff pdf.PDFDict) bool {
	firstChar, fcOK := v.Entries.Get("FirstChar").(pdf.PDFInteger)
	widths, wOK := v.Entries.Get("Widths").(pdf.PDFArray)
	if !fcOK || !wOK {
		return false
	}
	data, err := pdf.DecodeStream(ff)
	if err != nil || len(data) == 0 {
		return false
	}
	glyphWidths := fontprog.CFFAdvanceWidths(data)
	if len(glyphWidths) == 0 {
		return false
	}
	glyphNames, ok := fontprog.SimpleFontGlyphNameTable(v)
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
		if !found || math.Abs(float64(pdfWidth)-csWidth) <= 1 {
			continue
		}
		widths[i] = pdf.PDFInteger(math.Round(csWidth))
		changed = true
	}
	return changed
}

// fixCIDCFFWidths rewrites mismatched /W entries to the embedded CID-keyed
// CFF program's charstring advance width, mirroring validateCIDCFFMetrics
// (checks_font_program.go).
func fixCIDCFFWidths(v pdf.PDFDict, ff pdf.PDFDict) bool {
	w, ok := v.Entries.Get("W").(pdf.PDFArray)
	if !ok {
		return false
	}
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		return false
	}
	cidWidths := fontprog.CFFCIDAdvanceWidths(data)
	if len(cidWidths) == 0 {
		return false
	}

	pairs := fontprog.ParseCIDWidths(w)
	changed := false
	for i, pair := range pairs {
		csWidth, found := cidWidths[pair[0]]
		if !found || math.Abs(csWidth-float64(pair[1])) <= 1 {
			continue
		}
		pairs[i][1] = int(math.Round(csWidth))
		changed = true
	}
	if !changed {
		return false
	}
	v.Entries.Set("W", buildCIDWidthsArray(pairs))
	return true
}

// fixCIDTrueTypeWidths rewrites mismatched /W entries to the embedded
// TrueType program's hmtx advance width, mirroring validateCIDTrueTypeMetrics
// (checks_font_program.go).
func fixCIDTrueTypeWidths(v pdf.PDFDict, ff pdf.PDFDict) bool {
	w, ok := v.Entries.Get("W").(pdf.PDFArray)
	if !ok {
		return false
	}
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		return false
	}
	tables, ok := fontprog.ParseSfnt(data)
	if !ok {
		return false
	}

	pairs := fontprog.ParseCIDWidths(w)
	changed := false
	for i, pair := range pairs {
		fontWidth := fontprog.TTAdvanceWidth(tables, pair[0])
		if fontWidth < 0 || math.Abs(fontWidth-float64(pair[1])) <= 1 {
			continue
		}
		pairs[i][1] = int(math.Round(fontWidth))
		changed = true
	}
	if !changed {
		return false
	}
	v.Entries.Set("W", buildCIDWidthsArray(pairs))
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
	firstChar, fcOK := v.Entries.Get("FirstChar").(pdf.PDFInteger)
	widths, wOK := v.Entries.Get("Widths").(pdf.PDFArray)
	charProcs, cpOK := v.Entries.Get("CharProcs").(pdf.PDFDict)
	enc, encOK := v.Entries.Get("Encoding").(pdf.PDFDict)
	if !fcOK || !wOK || !cpOK || !encOK {
		return false
	}
	codeToGlyph := map[int]string{}
	if !fontprog.EachDifference(enc, func(code int, name string) { codeToGlyph[code] = name }) {
		return false
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
		proc, ok := charProcs.Entries.Get(glyphName).(pdf.PDFDict)
		if !ok || !proc.IsStream() {
			continue
		}
		data, err := pdf.DecodeStream(proc)
		if err != nil {
			continue
		}
		procWidth := verify.Type3GlyphWidth(data)
		if procWidth < 0 || math.Abs(procWidth-pdfWidth) <= 1.0 {
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
	if (d.Entries.Get("Type") != pdf.PDFName{Value: "Font"}) {
		return false
	}
	baseFont, _ := d.Entries.Get("BaseFont").(pdf.PDFName)
	if !fontprog.SubsetTagRe.MatchString(baseFont.Value) {
		return false
	}
	desc, ok := d.Entries.Get("FontDescriptor").(pdf.PDFDict)
	if !ok || desc.Entries == nil {
		return false
	}

	subtype, _ := d.Entries.Get("Subtype").(pdf.PDFName)
	switch subtype.Value {
	case "Type1", "MMType1":
		return fixType1CharSet(desc)
	case "CIDFontType0":
		if ff, ok := desc.Entries.Get("FontFile3").(pdf.PDFDict); ok {
			return fixCFFCIDSet(desc, ff)
		}
	case "CIDFontType2":
		if ff, ok := desc.Entries.Get("FontFile2").(pdf.PDFDict); ok {
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
	case desc.Entries.Get("FontFile") != nil:
		ff := desc.Entries.Get("FontFile").(pdf.PDFDict)
		fontData, err := pdf.DecodeStream(ff)
		if err != nil || len(fontData) == 0 {
			return false
		}
		names = fontprog.Type1GlyphNames(fontData)
	case desc.Entries.Get("FontFile3") != nil:
		ff := desc.Entries.Get("FontFile3").(pdf.PDFDict)
		data, err := pdf.DecodeStream(ff)
		if err != nil {
			return false
		}
		names = fontprog.CFFGlyphNames(data)
	default:
		return false
	}
	if len(names) == 0 {
		return false
	}
	if current, ok := desc.Entries.Get("CharSet").(pdf.PDFString); ok && current.Value != "" && charSetConsistent(current.Value, names) {
		return false
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteByte('/')
		b.WriteString(n)
	}
	desc.Entries.Set("CharSet", pdf.PDFString{Value: b.String()})
	return true
}

// charSetConsistent reports whether a CharSet string lists every glyph name
// the embedded program defines (extra names are tolerated, per 6.3.5-2).
func charSetConsistent(charSet string, programNames []string) bool {
	listed := map[string]bool{".notdef": true}
	for _, part := range fontprog.SplitCharSetNames(charSet) {
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
	td, ok := fontprog.ParseCFFTopDict(data)
	if !ok || !td.IsCIDKeyed || td.CSOffset < 0 || td.CSOffset+2 > len(data) {
		return false
	}
	csCount := int(binary.BigEndian.Uint16(data[td.CSOffset : td.CSOffset+2]))
	cids := fontprog.ParseCFFCharsetCIDs(data, td.CharsetOffset, csCount)
	if cids == nil {
		return false
	}

	if current, ok := desc.Entries.Get("CIDSet").(pdf.PDFDict); ok && current.IsStream() {
		if existing, err := pdf.DecodeStream(current); err == nil && cidSetComplete(existing, cids) {
			return false
		}
	}

	stream := pdf.NewPDFDict()
	writer.SetStreamFlate(&stream, buildCIDSetBitmap(cids))
	desc.Entries.Set("CIDSet", stream)
	return true
}

// fixTrueTypeCIDSet synthesizes or completes /CIDSet from the glyphs present
// in a CIDFontType2 program. It handles both a missing CIDSet and an existing
// one that omits a present glyph.
func fixTrueTypeCIDSet(d, desc pdf.PDFDict, ff pdf.PDFDict) bool {
	// Only safe when CID==GID (the spec default); a stream CIDToGIDMap means
	// CIDs don't correspond to GIDs directly.
	if c2g := d.Entries.Get("CIDToGIDMap"); c2g != nil && c2g != (pdf.PDFName{Value: "Identity"}) {
		return false
	}
	data, err := pdf.DecodeStream(ff)
	if err != nil {
		return false
	}
	tables, ok := fontprog.ParseSfnt(data)
	if !ok {
		return false
	}
	numGlyphs := fontprog.TTNumGlyphs(tables)
	if numGlyphs <= 0 {
		return false
	}
	// numGlyphs > 0, so gid 0 is in range and TTGlyphPresent accepts every
	// gid in [0, numGlyphs): cids is never empty.
	glyphPresent := fontprog.TTGlyphPresent(tables)
	var cids []int
	for gid := range numGlyphs {
		if glyphPresent(gid) {
			cids = append(cids, gid)
		}
	}

	if current, ok := desc.Entries.Get("CIDSet").(pdf.PDFDict); ok && current.IsStream() {
		if existing, err := pdf.DecodeStream(current); err == nil && cidSetComplete(existing, cids) {
			return false
		}
	}

	stream := pdf.NewPDFDict()
	writer.SetStreamFlate(&stream, buildCIDSetBitmap(cids))
	desc.Entries.Set("CIDSet", stream)
	return true
}
