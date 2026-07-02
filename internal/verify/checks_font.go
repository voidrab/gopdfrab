package verify

import (
	"fmt"
	"regexp"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// SubsetTagRe matches a font subset prefix such as "ABCDEF+".
var SubsetTagRe = regexp.MustCompile(`^[A-Z]{6}\+`)

// predefinedCMaps are the CMap names that need not be embedded (6.3.3.3).
var predefinedCMaps = map[string]bool{
	"Identity-H": true, "Identity-V": true,
}

// hasEmbeddedProgram reports whether a font descriptor embeds a font program via
// any of the given FontFile keys.
func HasEmbeddedProgram(desc pdf.PDFDict, keys ...string) bool {
	if desc.Entries == nil {
		return false
	}
	for _, k := range keys {
		if desc.Entries[k] != nil {
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
		ff, ok := desc.Entries["FontFile3"].(pdf.PDFDict)
		if !ok {
			return ""
		}
		st, _ := ff.Entries["Subtype"].(pdf.PDFName)
		return st.Value
	}
	switch subtype {
	case "Type1", "MMType1":
		if desc.Entries["FontFile"] != nil {
			return true
		}
		st := fontFile3Subtype()
		return st == "Type1C" || st == "OpenType"
	case "TrueType", "CIDFontType2":
		return desc.Entries["FontFile2"] != nil || fontFile3Subtype() == "OpenType"
	case "CIDFontType0":
		st := fontFile3Subtype()
		return st == "CIDFontType0C" || st == "OpenType"
	}
	return HasEmbeddedProgram(desc, "FontFile", "FontFile2", "FontFile3")
}

// ValidateFontDict checks font dictionaries: embedding (6.3.4), composite fonts
// (6.3.3), subsets (6.3.5) and character encodings (6.3.7).
func ValidateFontDict(v pdf.PDFDict, ctx *ValidationContext) {
	if (v.Entries["Type"] != pdf.PDFName{Value: "Font"}) {
		return
	}

	subtype, _ := v.Entries["Subtype"].(pdf.PDFName)
	baseFont, _ := v.Entries["BaseFont"].(pdf.PDFName)
	desc, _ := v.Entries["FontDescriptor"].(pdf.PDFDict)

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

	subset := SubsetTagRe.MatchString(baseFont.Value)

	switch subtype.Value {
	case "Type1", "MMType1", "TrueType":
		if subtype.Value == "TrueType" {
			validateTrueTypeEncoding(v, desc, ctx)
			if ff, ok := desc.Entries["FontFile2"].(pdf.PDFDict); ok && !renderingMode3 {
				firstChar, fcOK := v.Entries["FirstChar"].(pdf.PDFInteger)
				lastChar, lcOK := v.Entries["LastChar"].(pdf.PDFInteger)
				widths, wOK := v.Entries["Widths"].(pdf.PDFArray)
				if fcOK && lcOK && wOK {
					if subset {
						ValidateSimpleTrueTypeSubset(v, ff, int(firstChar), int(lastChar), widths, ctx)
					}
					validateSimpleTrueTypeMetrics(v, ff, int(firstChar), widths, ctx)
				}
			}
		} else {
			firstChar, fcOK := v.Entries["FirstChar"].(pdf.PDFInteger)
			lastChar, lcOK := v.Entries["LastChar"].(pdf.PDFInteger)
			widths, wOK := v.Entries["Widths"].(pdf.PDFArray)
			haveWidths := fcOK && lcOK && wOK

			if subset {
				if desc.Entries != nil && desc.Entries["CharSet"] == nil {
					ctx.Report(pdf.Checks.Font.Type1SubsetCharSet, v, "Type 1 subset font descriptor lacks CharSet")
				} else if !renderingMode3 && haveWidths {
					ValidateType1SubsetCoverage(v, v, desc, int(firstChar), int(lastChar), widths, ctx)
				}
			}
			if !renderingMode3 && haveWidths {
				if ff, ok := desc.Entries["FontFile"].(pdf.PDFDict); ok {
					pdfEnc, _ := v.Entries["Encoding"].(pdf.PDFName)
					validateType1Metrics(v, ff, int(firstChar), widths, pdfEnc.Value, ctx)
				} else if ff, ok := desc.Entries["FontFile3"].(pdf.PDFDict); ok {
					validateType1CMetrics(v, v, ff, int(firstChar), widths, ctx)
				}
			}
		}

	case "CIDFontType0", "CIDFontType2":
		if subtype.Value == "CIDFontType2" && v.Entries["CIDToGIDMap"] == nil && !renderingMode3 {
			ctx.Report(pdf.Checks.Font.CIDToGIDMapMissing, v, "CIDFontType2 lacks CIDToGIDMap")
		}
		if subset && desc.Entries != nil && desc.Entries["CIDSet"] == nil {
			ctx.Report(pdf.Checks.Font.CIDSubsetCIDSet, v, "CID subset font descriptor lacks CIDSet")
		} else if subset && !renderingMode3 {
			switch subtype.Value {
			case "CIDFontType0":
				if ff, ok := desc.Entries["FontFile3"].(pdf.PDFDict); ok {
					validateCIDSetBitmap(v, desc, ff, ctx)
				}
			case "CIDFontType2":
				if ff, ok := desc.Entries["FontFile2"].(pdf.PDFDict); ok && IdentityCIDToGIDMap(v) {
					validateCIDSetTrueType(v, desc, ff, ctx)
				}
			}
		}
		if w, ok2 := v.Entries["W"].(pdf.PDFArray); ok2 && !renderingMode3 {
			switch subtype.Value {
			case "CIDFontType2":
				if ff, ok := desc.Entries["FontFile2"].(pdf.PDFDict); ok {
					if subset {
						ValidateCIDTrueTypeSubset(v, ff, w, ctx)
					}
					validateCIDTrueTypeMetrics(v, ff, w, ctx)
				}
			case "CIDFontType0":
				if ff, ok := desc.Entries["FontFile3"].(pdf.PDFDict); ok {
					if subset {
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
}

// validateType3Metrics checks that the Widths array of a Type3 font is
// consistent with the wx value declared in each glyph's d0 or d1 operator (6.3.6).
func validateType3Metrics(v pdf.PDFDict, ctx *ValidationContext) {
	firstChar, fcOK := v.Entries["FirstChar"].(pdf.PDFInteger)
	lastChar, lcOK := v.Entries["LastChar"].(pdf.PDFInteger)
	widths, wOK := v.Entries["Widths"].(pdf.PDFArray)
	charProcs, cpOK := v.Entries["CharProcs"].(pdf.PDFDict)
	enc, encOK := v.Entries["Encoding"].(pdf.PDFDict)
	if !fcOK || !lcOK || !wOK || !cpOK || !encOK {
		return
	}
	diffs, _ := enc.Entries["Differences"].(pdf.PDFArray)
	if diffs == nil {
		return
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
		proc, ok := charProcs.Entries[glyphName].(pdf.PDFDict)
		if !ok || !proc.HasStream {
			continue
		}
		data, err := pdf.DecodeStream(proc)
		if err != nil {
			continue
		}
		procWidth := Type3GlyphWidth(data)
		if procWidth < 0 {
			continue // no d0/d1 found
		}
		if Abs64(procWidth-pdfWidth) > 1.0 {
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

// validateTrueTypeEncoding checks symbolic/non-symbolic TrueType encodings (6.3.7).
func validateTrueTypeEncoding(v pdf.PDFDict, desc pdf.PDFDict, ctx *ValidationContext) {
	flags := 0
	if f, ok := desc.Entries["Flags"].(pdf.PDFInteger); ok {
		flags = int(f)
	}
	symbolic := flags&4 != 0

	enc := v.Entries["Encoding"]
	if symbolic {
		// 6.3.7: a symbolic TrueType font shall not define Encoding.
		if enc != nil {
			ctx.Report(pdf.Checks.Font.SymbolicTrueTypeEncoding, v, "symbolic TrueType font shall not specify Encoding")
		}
		// 6.3.7: a symbolic TrueType cmap shall contain exactly one subtable.
		if n, ok := trueTypeCmapSubtables(ctx, desc); ok && n != 1 {
			ctx.Report(pdf.Checks.Font.SymbolicTrueTypeCmap, v, fmt.Sprintf("symbolic TrueType cmap has %d subtables, expected 1", n))
		}
		return
	}

	// Non-symbolic TrueType: Encoding shall be MacRomanEncoding or WinAnsiEncoding.
	name, ok := enc.(pdf.PDFName)
	if !ok || (name.Value != "MacRomanEncoding" && name.Value != "WinAnsiEncoding") {
		ctx.Report(pdf.Checks.Font.TrueTypeEncoding, v, "non-symbolic TrueType font shall use MacRoman or WinAnsi encoding")
	}
}

// IdentityCIDToGIDMap reports whether a CIDFontType2's CIDToGIDMap maps each CID
// to the identically-numbered GID -- the spec default when the key is /Identity
// or absent -- so the CIDSet, indexed by CID, can be read against glyph IDs.
func IdentityCIDToGIDMap(v pdf.PDFDict) bool {
	c2g := v.Entries["CIDToGIDMap"]
	return c2g == nil || c2g == (pdf.PDFName{Value: "Identity"})
}

// DescendantCIDFont returns the descendant CIDFont dictionary of a Type0 font.
func DescendantCIDFont(v pdf.PDFDict) pdf.PDFDict {
	if arr, ok := v.Entries["DescendantFonts"].(pdf.PDFArray); ok && len(arr) > 0 {
		if d, ok := arr[0].(pdf.PDFDict); ok {
			return d
		}
	}
	return pdf.PDFDict{}
}

// validateType0Font checks composite font CMap embedding (6.3.3.3) and
// CIDSystemInfo consistency (6.3.3.1).
func validateType0Font(v pdf.PDFDict, ctx *ValidationContext) {
	enc := v.Entries["Encoding"]

	// 6.3.3.3: a CMap shall be one of the predefined CMaps or embedded.
	if name, ok := enc.(pdf.PDFName); ok && !predefinedCMaps[name.Value] {
		ctx.Report(pdf.Checks.Font.CMapNotEmbedded, v, fmt.Sprintf("CMap /%s is neither predefined nor embedded", name.Value))
	}
	// 6.3.3.3: an embedded CMap's WMode shall be consistent.
	if cmap, ok := enc.(pdf.PDFDict); ok {
		validateCMapWMode(v, cmap, ctx)
	}

	// 6.3.3.1: an embedded CMap's CIDSystemInfo shall match the CIDFont's.
	cmap, cmapOK := enc.(pdf.PDFDict)
	cid := DescendantCIDFont(v)
	if cmapOK && cid.Entries != nil {
		cmapCSI, _ := cmap.Entries["CIDSystemInfo"].(pdf.PDFDict)
		cidCSI, _ := cid.Entries["CIDSystemInfo"].(pdf.PDFDict)
		if cmapCSI.Entries != nil && cidCSI.Entries != nil {
			if !SameCIDSystemInfo(cmapCSI, cidCSI) {
				ctx.Report(pdf.Checks.Font.CIDSystemInfoMismatch, v, "CMap and CIDFont CIDSystemInfo are incompatible")
			}
		}
	}
}

// SameCIDSystemInfo reports whether two CIDSystemInfo dictionaries share Registry
// and Ordering.
func SameCIDSystemInfo(a, b pdf.PDFDict) bool {
	return cidInfoField(a, "Registry") == cidInfoField(b, "Registry") &&
		cidInfoField(a, "Ordering") == cidInfoField(b, "Ordering")
}

func cidInfoField(d pdf.PDFDict, key string) string {
	if s, ok := d.Entries[key].(pdf.PDFString); ok {
		return s.Value
	}
	return ""
}

// validateCMapStream checks an embedded CMap stream for CID values exceeding
// the architectural limit of 65535 (PDF/A-1, 6.1.12 / PDF Reference Table H.1).
func validateCMapStream(v pdf.PDFDict, ctx *ValidationContext) {
	if v.Entries["Type"] != (pdf.PDFName{Value: "CMap"}) || !v.HasStream {
		return
	}
	data, err := ctx.decodeStreamCached(v)
	if err != nil {
		return
	}
	checkCMapCIDLimits(v, data, ctx)
}

// checkCMapCIDLimits scans CMap PostScript content for CID values > 65535.
func checkCMapCIDLimits(obj pdf.PDFValue, data []byte, ctx *ValidationContext) {
	tokens := CmapTokenize(data)
	const maxCID = 65535

	inCIDRange := false
	inCIDChar := false
	pos := 0 // position within the current block entry (0-indexed token within triplet/pair)

	for _, tok := range tokens {
		switch tok.Text {
		case "begincidrange":
			inCIDRange = true
			inCIDChar = false
			pos = 0
		case "endcidrange":
			inCIDRange = false
			pos = 0
		case "begincidchar":
			inCIDChar = true
			inCIDRange = false
			pos = 0
		case "endcidchar":
			inCIDChar = false
			pos = 0
		default:
			if inCIDRange {
				// cidrange entries: <start-code> <end-code> start-CID (repeat).
				pos++
				if pos == 3 {
					if cid, ok := CmapParseInt(tok.Text); ok && cid > maxCID {
						ctx.Report(pdf.Checks.Structure.CMapCIDOutOfRange, obj, fmt.Sprintf("CMap CID value %d exceeds maximum of 65535", cid))
					}
					pos = 0
				}
			} else if inCIDChar {
				// cidchar entries: <code> CID (repeat).
				pos++
				if pos == 2 {
					if cid, ok := CmapParseInt(tok.Text); ok && cid > maxCID {
						ctx.Report(pdf.Checks.Structure.CMapCIDOutOfRange, obj, fmt.Sprintf("CMap CID value %d exceeds maximum of 65535", cid))
					}
					pos = 0
				}
			}
		}
	}
}

// CmapToken pairs a CmapTokenize token with its byte range in the source
// data, so a fixer can splice a replacement in place (fixups_limits.go)
// without disturbing anything else (comments, whitespace, formatting).
type CmapToken struct {
	Text       string
	Start, End int
}

// CmapTokenize splits CMap PostScript content into tokens, skipping comments.
func CmapTokenize(data []byte) []CmapToken {
	var tokens []CmapToken
	i := 0
	for i < len(data) {
		for i < len(data) && pdf.IsWhitespace(data[i]) {
			i++
		}
		if i >= len(data) {
			break
		}
		if data[i] == '%' {
			for i < len(data) && data[i] != '\n' {
				i++
			}
			continue
		}
		start := i
		if data[i] == '<' {
			// hex string token: <...> (stop at first >)
			j := i + 1
			for j < len(data) && data[j] != '>' {
				j++
			}
			if j < len(data) {
				j++
			}
			tokens = append(tokens, CmapToken{string(data[i:j]), start, j})
			i = j
		} else if data[i] == '(' {
			// literal string: skip to matching ')'
			j := i + 1
			depth := 1
			for j < len(data) && depth > 0 {
				if data[j] == '\\' {
					j += 2
					continue
				}
				switch data[j] {
				case '(':
					depth++
				case ')':
					depth--
				}
				j++
			}
			tokens = append(tokens, CmapToken{string(data[i:j]), start, j})
			i = j
		} else {
			// regular token: read until whitespace or delimiter
			j := i
			for j < len(data) && !pdf.IsWhitespace(data[j]) && data[j] != '<' && data[j] != '(' && data[j] != ')' {
				j++
			}
			if j > i {
				tokens = append(tokens, CmapToken{string(data[i:j]), start, j})
			}
			i = j
		}
	}
	return tokens
}

// CmapParseInt parses a decimal integer token from a CMap stream.
func CmapParseInt(tok string) (int64, bool) {
	if len(tok) == 0 {
		return 0, false
	}
	var n int64
	for _, c := range []byte(tok) {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
}
