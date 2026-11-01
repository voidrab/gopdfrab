package verify

import (
	"fmt"
	"math"

	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// HasEmbeddedProgram reports whether a font descriptor embeds a font program via
// any of the given FontFile keys.
func HasEmbeddedProgram(desc pdf.PDFDict, keys ...string) bool {
	if desc.Entries == nil {
		return false
	}
	for _, k := range keys {
		if desc.Entries.Get(k) != nil {
			return true
		}
	}
	return false
}

// EmbeddedProgramMatchesSubtype reports whether desc embeds a font program of
// the kind the font's Subtype requires (6.3.4): a FontFile2 under a Type1
// font, say, is not an embedded Type1 program.
func EmbeddedProgramMatchesSubtype(subtype string, desc pdf.PDFDict) bool {
	if desc.Entries == nil {
		return false
	}
	fontFile3Subtype := func() string {
		ff, ok := desc.Entries.Get("FontFile3").(pdf.PDFDict)
		if !ok {
			return ""
		}
		st, _ := ff.Entries.Get("Subtype").(pdf.PDFName)
		return st.Value
	}
	switch subtype {
	case "Type1", "MMType1":
		if desc.Entries.Get("FontFile") != nil {
			return true
		}
		st := fontFile3Subtype()
		return st == "Type1C" || st == "OpenType"
	case "TrueType", "CIDFontType2":
		return desc.Entries.Get("FontFile2") != nil || fontFile3Subtype() == "OpenType"
	case "CIDFontType0":
		st := fontFile3Subtype()
		return st == "CIDFontType0C" || st == "OpenType"
	}
	return HasEmbeddedProgram(desc, "FontFile", "FontFile2", "FontFile3")
}

// ValidateFontDict checks font dictionaries: embedding (6.3.4), composite fonts
// (6.3.3), subsets (6.3.5) and character encodings (6.3.7).
func ValidateFontDict(v pdf.PDFDict, ctx *ValidationContext) {
	if (v.Entries.Get("Type") != pdf.PDFName{Value: "Font"}) {
		return
	}

	subtype, _ := v.Entries.Get("Subtype").(pdf.PDFName)
	baseFont, _ := v.Entries.Get("BaseFont").(pdf.PDFName)
	desc, _ := v.Entries.Get("FontDescriptor").(pdf.PDFDict)

	ValidateFontProgram(v, desc, baseFont.Value, ctx)

	renderingMode3 := ctx.isInvisibleOnlyFont(v)
	containsFontFile := EmbeddedProgramMatchesSubtype(subtype.Value, desc)

	// 6.3.4
	if !(subtype.Value == "Type3" || subtype.Value == "Type0" || renderingMode3 || containsFontFile) {
		if subtype.Value == "CIDFontType0" || subtype.Value == "CIDFontType2" {
			ctx.Report(pdf.Checks.Font.CIDNotEmbedded, v, fmt.Sprintf("CID font %s is not embedded", baseFont.Value))
		} else if !ctx.SkipUnusedSimpleFonts || ctx.simpleFontShown(v) {
			ctx.Report(pdf.Checks.Font.SimpleNotEmbedded, v, fmt.Sprintf("font %s is not embedded", baseFont.Value))
		}
	}

	if subtype.Value == "" ||
		(subtype.Value != "MMType1" && subtype.Value != "TrueType" &&
			subtype.Value != "CIDFontType0" && subtype.Value != "CIDFontType2" &&
			subtype.Value != "Type0" && subtype.Value != "Type1" && subtype.Value != "Type3") {
		ctx.Report(pdf.Checks.Font.InvalidSubtype, v, fmt.Sprintf("font %s has an invalid or missing Subtype: '%s'",
			baseFont.Value, subtype.Value))
		return
	}

	if subtype.Value != "Type3" && baseFont.Value == "" {
		ctx.Report(pdf.Checks.Font.FontBaseFont, v, "font dictionary lacks BaseFont")
	}

	subset := fontprog.SubsetTagRe.MatchString(baseFont.Value)

	switch subtype.Value {
	case "Type1", "MMType1", "TrueType":
		if subtype.Value == "TrueType" {
			validateTrueTypeEncoding(v, desc, ctx)
			if ff, ok := desc.Entries.Get("FontFile2").(pdf.PDFDict); ok && !renderingMode3 {
				firstChar, fcOK := v.Entries.Get("FirstChar").(pdf.PDFInteger)
				lastChar, lcOK := v.Entries.Get("LastChar").(pdf.PDFInteger)
				widths, wOK := v.Entries.Get("Widths").(pdf.PDFArray)
				if fcOK && lcOK && wOK {
					if subset {
						ValidateSimpleTrueTypeSubset(v, ff, int(firstChar), int(lastChar), widths, ctx)
					}
					validateSimpleTrueTypeMetrics(v, ff, int(firstChar), widths, ctx)
				}
			}
		} else {
			firstChar, fcOK := v.Entries.Get("FirstChar").(pdf.PDFInteger)
			lastChar, lcOK := v.Entries.Get("LastChar").(pdf.PDFInteger)
			widths, wOK := v.Entries.Get("Widths").(pdf.PDFArray)
			haveWidths := fcOK && lcOK && wOK

			if subset {
				if desc.Entries != nil && desc.Entries.Get("CharSet") == nil && !ctx.Spec.SubsetSetsOptional {
					ctx.Report(pdf.Checks.Font.Type1SubsetCharSet, v, "Type 1 subset font descriptor lacks CharSet")
				} else if !renderingMode3 && haveWidths {
					ValidateType1SubsetCoverage(v, v, desc, int(firstChar), int(lastChar), widths, ctx)
				}
			}
			if !renderingMode3 && haveWidths {
				if ff, ok := desc.Entries.Get("FontFile").(pdf.PDFDict); ok {
					validateType1Metrics(v, desc, ff, int(firstChar), widths, v.Entries.Get("Encoding"), ctx)
				} else if ff, ok := desc.Entries.Get("FontFile3").(pdf.PDFDict); ok {
					validateType1CMetrics(v, v, ff, int(firstChar), widths, ctx)
				}
			}
		}

	case "CIDFontType0", "CIDFontType2":
		// PDF/A-1 exempts a font used only in rendering mode 3, PDF/A-2 one
		// with no embedded program.
		cidToGIDExempt := renderingMode3
		if ctx.part2() {
			cidToGIDExempt = !containsFontFile
		}
		if subtype.Value == "CIDFontType2" && !cidToGIDExempt {
			switch c2g := v.Entries.Get("CIDToGIDMap"); {
			case c2g == nil:
				ctx.Report(pdf.Checks.Font.CIDToGIDMapMissing, v, "CIDFontType2 lacks CIDToGIDMap")
			case !ValidCIDToGIDMap(c2g):
				ctx.Report(pdf.Checks.Font.CIDToGIDMapMissing, v, "CIDFontType2 CIDToGIDMap is neither a stream nor Identity")
			}
		}
		hasCIDSet := desc.Entries.Get("CIDSet") != nil
		if subset && desc.Entries != nil && !hasCIDSet && !ctx.Spec.SubsetSetsOptional {
			ctx.Report(pdf.Checks.Font.CIDSubsetCIDSet, v, "CID subset font descriptor lacks CIDSet")
		} else if subset && !renderingMode3 && (hasCIDSet || desc.Entries == nil) {
			switch subtype.Value {
			case "CIDFontType0":
				if ff, ok := desc.Entries.Get("FontFile3").(pdf.PDFDict); ok {
					validateCIDSetBitmap(v, desc, ff, ctx)
				}
			case "CIDFontType2":
				if ff, ok := desc.Entries.Get("FontFile2").(pdf.PDFDict); ok && IdentityCIDToGIDMap(v) {
					validateCIDSetTrueType(v, desc, ff, ctx)
				}
			}
		}
		// Without a W array every CID has the default width, and the CIDs
		// shown still need glyphs.
		if w, _ := v.Entries.Get("W").(pdf.PDFArray); !renderingMode3 {
			// A code cut short selects no glyph at all.
			partial := subset && containsFontFile && ctx.showsPartialCode(v)
			if partial {
				ctx.Report(pdf.Checks.Font.SubsetGlyphCoverage, v, "a shown string ends in the middle of a two-byte character code")
			}
			switch subtype.Value {
			case "CIDFontType2":
				if ff, ok := desc.Entries.Get("FontFile2").(pdf.PDFDict); ok {
					if subset && !partial {
						ValidateCIDTrueTypeSubset(v, ff, w, ctx)
					}
					validateCIDTrueTypeMetrics(v, ff, w, ctx)
				}
			case "CIDFontType0":
				if ff, ok := desc.Entries.Get("FontFile3").(pdf.PDFDict); ok {
					if subset && !partial {
						ValidateCIDCFFSubset(v, ff, w, ctx)
					}
					validateCIDCFFMetrics(v, v, ff, w, ctx)
				}
			}
		}

	case "Type3":
		validateType3Metrics(v, ctx)

	case "Type0":
		validateType0Font(v, ctx)
	}

	if ctx.part2() {
		validateNotdefReferences(v, subtype.Value, desc, ctx)
	}
}

// validateType3Metrics checks that the Widths array of a Type3 font is
// consistent with the wx value declared in each glyph's d0 or d1 operator (6.3.6).
func validateType3Metrics(v pdf.PDFDict, ctx *ValidationContext) {
	firstChar, fcOK := v.Entries.Get("FirstChar").(pdf.PDFInteger)
	lastChar, lcOK := v.Entries.Get("LastChar").(pdf.PDFInteger)
	widths, wOK := v.Entries.Get("Widths").(pdf.PDFArray)
	charProcs, cpOK := v.Entries.Get("CharProcs").(pdf.PDFDict)
	enc, encOK := v.Entries.Get("Encoding").(pdf.PDFDict)
	if !fcOK || !lcOK || !wOK || !cpOK || !encOK {
		return
	}
	codeToGlyph := map[int]string{}
	if !fontprog.EachDifference(enc, func(code int, name string) { codeToGlyph[code] = name }) {
		return
	}

	for i, w := range widths {
		var pdfWidth float64
		switch wv := w.(type) {
		case pdf.PDFInteger:
			pdfWidth = float64(wv)
		case pdf.PDFReal:
			pdfWidth = float64(wv)
		}
		cc := int(firstChar) + i
		if cc > int(lastChar) {
			break
		}
		glyphName := codeToGlyph[cc]
		if glyphName == "" {
			continue
		}
		proc, ok := charProcs.Entries.Get(glyphName).(pdf.PDFDict)
		if !ok || !proc.IsStream() {
			continue
		}
		data, err := ctx.decodeStreamCached(proc)
		if err != nil {
			continue // reported as StreamUndecodable by the decode chokepoint
		}
		procWidth := Type3GlyphWidth(data)
		if procWidth < 0 {
			continue // no d0/d1 found
		}
		if math.Abs(procWidth-pdfWidth) > 1.0 {
			ctx.Report(pdf.Checks.Font.AdvanceWidthMismatch, v, fmt.Sprintf("Type3 glyph /%s: Widths entry %g does not match d0/d1 width %g", glyphName, pdfWidth, procWidth))
			return
		}
	}
}

// Type3GlyphWidth extracts the wx (horizontal advance) from the first d0 or d1
// operator in a Type3 glyph procedure. Returns -1 if not found.
func Type3GlyphWidth(data []byte) float64 {
	cs := pdf.NewContentScanner(data)
	result := -1.0
	cs.Scan(func(op string, operands []pdf.PDFValue) {
		if result >= 0 {
			return
		}
		if (op == "d0" || op == "d1") && len(operands) >= 1 {
			switch wv := operands[0].(type) {
			case pdf.PDFInteger:
				result = float64(wv)
			case pdf.PDFReal:
				result = float64(wv)
			}
		}
	})
	return result
}

// validateTrueTypeEncoding checks symbolic/non-symbolic TrueType encodings
// and cmap tables (6.3.7, 6.2.11.6).
func validateTrueTypeEncoding(v pdf.PDFDict, desc pdf.PDFDict, ctx *ValidationContext) {
	flags := 0
	if f, ok := desc.Entries.Get("Flags").(pdf.PDFInteger); ok {
		flags = int(f)
	}
	symbolic := flags&4 != 0
	cmaps, haveCmaps := trueTypeCmaps(ctx, desc)

	enc := v.Entries.Get("Encoding")
	if symbolic {
		// 6.3.7: a symbolic TrueType font shall not define Encoding.
		if enc != nil {
			ctx.Report(pdf.Checks.Font.SymbolicTrueTypeEncoding, v, "symbolic TrueType font shall not specify Encoding")
		}
		// 6.3.7: a symbolic TrueType cmap shall contain exactly one subtable.
		// PDF/A-2 also allows several when one is the (3,0) subtable.
		if haveCmaps && cmaps.count != 1 && !(ctx.part2() && cmaps.symbol) {
			ctx.Report(pdf.Checks.Font.SymbolicTrueTypeCmap, v, fmt.Sprintf("symbolic TrueType cmap has %d subtables, expected 1", cmaps.count))
		}
		return
	}

	// 6.2.11.6: a non-symbolic program needs a cmap subtable other than (3,0).
	if ctx.part2() && haveCmaps && (cmaps.count == 0 || cmaps.symbol && cmaps.count == 1) {
		ctx.Report(pdf.Checks.Font.NonSymbolicTrueTypeCmap, v, "non-symbolic TrueType cmap has no subtable other than (3,0)")
	}

	// Non-symbolic TrueType: Encoding shall be MacRomanEncoding or
	// WinAnsiEncoding. The clause qualifies this one with "used for rendering"
	// -- unlike the symbolic sentences above -- so a font a resource dictionary
	// names and no content stream ever selects is outside it, decided by the
	// same profile flag that scopes 6.3.4 embedding.
	if ctx.SkipUnusedSimpleFonts && !ctx.simpleFontShown(v) {
		return
	}
	if ctx.part2() {
		if msg := part2TrueTypeEncodingProblem(enc, cmaps, haveCmaps, desc); msg != "" {
			ctx.Report(pdf.Checks.Font.TrueTypeEncoding, v, msg)
		}
		return
	}
	name, ok := enc.(pdf.PDFName)
	if !ok || (name.Value != "MacRomanEncoding" && name.Value != "WinAnsiEncoding") {
		ctx.Report(pdf.Checks.Font.TrueTypeEncoding, v, "non-symbolic TrueType font shall use MacRoman or WinAnsi encoding")
	}
}

// part2TrueTypeEncodingProblem describes how a non-symbolic TrueType font's
// Encoding breaks 6.2.11.6, or returns "". The Encoding, or its BaseEncoding,
// must be MacRomanEncoding or WinAnsiEncoding, and Differences may name only
// Adobe Glyph List glyphs of a program with a (3,1) cmap subtable, as veraPDF
// reads the rule.
func part2TrueTypeEncodingProblem(enc pdf.PDFValue, cmaps sfntCmaps, haveCmaps bool, desc pdf.PDFDict) string {
	base := enc
	dict, isDict := enc.(pdf.PDFDict)
	if isDict {
		base = dict.Entries.Get("BaseEncoding")
	}
	if name, ok := base.(pdf.PDFName); !ok || (name.Value != "MacRomanEncoding" && name.Value != "WinAnsiEncoding") {
		return "non-symbolic TrueType font shall use MacRoman or WinAnsi encoding"
	}
	if !isDict || dict.Entries.Get("Differences") == nil || !HasEmbeddedProgram(desc, "FontFile2") {
		return ""
	}
	if !haveCmaps || !cmaps.unicode {
		return "non-symbolic TrueType font with Differences has no (3,1) cmap subtable"
	}
	bad := ""
	fontprog.EachDifference(dict, func(_ int, name string) {
		if bad == "" && !fontprog.InAdobeGlyphList(name) {
			bad = name
		}
	})
	if bad != "" {
		return fmt.Sprintf("Differences glyph name /%s is not in the Adobe Glyph List", bad)
	}
	return ""
}

// IdentityCIDToGIDMap reports whether a CIDFontType2's CIDToGIDMap maps each CID
// to the identically-numbered GID -- the spec default when the key is /Identity
// or absent -- so the CIDSet, indexed by CID, can be read against glyph IDs.
func IdentityCIDToGIDMap(v pdf.PDFDict) bool {
	c2g := v.Entries.Get("CIDToGIDMap")
	return c2g == nil || c2g == (pdf.PDFName{Value: "Identity"})
}

// DescendantCIDFont returns the descendant CIDFont dictionary of a Type0 font.
func DescendantCIDFont(v pdf.PDFDict) pdf.PDFDict {
	if arr, ok := v.Entries.Get("DescendantFonts").(pdf.PDFArray); ok && len(arr) > 0 {
		if d, ok := arr[0].(pdf.PDFDict); ok {
			return d
		}
	}
	return pdf.PDFDict{}
}

// ValidCIDToGIDMap reports whether a CIDToGIDMap value is one ISO 32000-1
// allows: a stream or the name Identity.
func ValidCIDToGIDMap(c2g pdf.PDFValue) bool {
	if d, ok := c2g.(pdf.PDFDict); ok {
		return d.IsStream()
	}
	return c2g == pdf.PDFName{Value: "Identity"}
}

// validateType0Font checks composite font CMap embedding (6.3.3.3,
// 6.2.11.3.3) and CIDSystemInfo consistency (6.3.3.1, 6.2.11.3.1).
func validateType0Font(v pdf.PDFDict, ctx *ValidationContext) {
	enc := v.Entries.Get("Encoding")

	// 6.3.3.3: a CMap shall be one of the predefined CMaps or embedded.
	if name, ok := enc.(pdf.PDFName); ok && !ctx.Spec.PredefinedCMaps.Has(name.Value) {
		ctx.Report(pdf.Checks.Font.CMapNotEmbedded, v, fmt.Sprintf("CMap /%s is neither predefined nor embedded", name.Value))
	}
	cmap, cmapOK := enc.(pdf.PDFDict)
	if cmapOK {
		// 6.3.3.3: an embedded CMap's WMode shall be consistent.
		validateCMapWMode(v, cmap, ctx)
		// 6.2.11.3.3: it may use only a predefined CMap.
		if ctx.Spec.UseCMapPredefined {
			if used := cmap.Entries.Get("UseCMap"); used != nil && !ctx.Spec.PredefinedCMaps.Has(cmapName(used)) {
				ctx.Report(pdf.Checks.Font.UseCMapNotPredefined, v, fmt.Sprintf("CMap uses CMap /%s, which is not predefined", cmapName(used)))
			}
		}
	}

	// 6.3.3.1: an embedded CMap's CIDSystemInfo shall match the CIDFont's.
	cid := DescendantCIDFont(v)
	if cmapOK && cid.Entries != nil {
		cmapCSI, _ := cmap.Entries.Get("CIDSystemInfo").(pdf.PDFDict)
		cidCSI, _ := cid.Entries.Get("CIDSystemInfo").(pdf.PDFDict)
		if cmapCSI.Entries != nil && cidCSI.Entries != nil {
			if !SameCIDSystemInfo(cmapCSI, cidCSI) {
				ctx.Report(pdf.Checks.Font.CIDSystemInfoMismatch, v, "CMap and CIDFont CIDSystemInfo are incompatible")
			} else if ctx.part2() && !SupplementCovered(cmapCSI, cidCSI) {
				ctx.Report(pdf.Checks.Font.CIDSystemInfoMismatch, v, "CIDFont CIDSystemInfo Supplement exceeds the CMap's")
			}
		}
	}
}

// cmapName returns the name of a CMap given by name or as a stream with a
// CMapName entry, or "".
func cmapName(cmap pdf.PDFValue) string {
	if d, ok := cmap.(pdf.PDFDict); ok {
		cmap = d.Entries.Get("CMapName")
	}
	name, _ := cmap.(pdf.PDFName)
	return name.Value
}

// SameCIDSystemInfo reports whether two CIDSystemInfo dictionaries share Registry
// and Ordering.
func SameCIDSystemInfo(a, b pdf.PDFDict) bool {
	return cidInfoField(a, "Registry") == cidInfoField(b, "Registry") &&
		cidInfoField(a, "Ordering") == cidInfoField(b, "Ordering")
}

// SupplementCovered reports whether a CMap's CIDSystemInfo has a Supplement at
// least the CIDFont's, as PDF/A-2 requires; both must be integers.
func SupplementCovered(cmapCSI, cidCSI pdf.PDFDict) bool {
	cmapSupp, ok1 := cmapCSI.Entries.Get("Supplement").(pdf.PDFInteger)
	cidSupp, ok2 := cidCSI.Entries.Get("Supplement").(pdf.PDFInteger)
	return ok1 && ok2 && cidSupp <= cmapSupp
}

func cidInfoField(d pdf.PDFDict, key string) string {
	if s, ok := d.Entries.Get(key).(pdf.PDFString); ok {
		return s.Value
	}
	return ""
}

// validateCMapStream checks an embedded CMap stream for CID values exceeding
// the architectural limit of 65535 (PDF/A-1, 6.1.12 / PDF Reference Table H.1).
func validateCMapStream(v pdf.PDFDict, ctx *ValidationContext) {
	if v.Entries.Get("Type") != (pdf.PDFName{Value: "CMap"}) || !v.IsStream() {
		return
	}
	data, err := ctx.decodeStreamCached(v)
	if err != nil {
		return
	}
	checkCMapCIDLimits(v, data, ctx)
}

// checkCMapCIDLimits scans CMap PostScript content for CID values over the
// level's limit.
func checkCMapCIDLimits(obj pdf.PDFValue, data []byte, ctx *ValidationContext) {
	maxCID := ctx.Spec.Limits.MaxCID
	fontprog.EachCMapCID(data, func(tok fontprog.CmapToken) {
		if cid, ok := fontprog.CmapParseInt(tok.Text); ok && overLimit(cid, maxCID) {
			ctx.Report(pdf.Checks.Structure.CMapCIDOutOfRange, obj, fmt.Sprintf("CMap CID value %d exceeds maximum of %d", cid, maxCID))
		}
	})
}
