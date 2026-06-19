package pdfrab

import (
	"fmt"
	"regexp"
)

// subsetTagRe matches a font subset prefix such as "ABCDEF+".
var subsetTagRe = regexp.MustCompile(`^[A-Z]{6}\+`)

// predefinedCMaps are the CMap names that need not be embedded (6.3.3.3).
var predefinedCMaps = map[string]bool{
	"Identity-H": true, "Identity-V": true,
}

// hasEmbeddedProgram reports whether a font descriptor embeds a font program via
// any of the given FontFile keys.
func hasEmbeddedProgram(desc PDFDict, keys ...string) bool {
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

// validateFontDict checks font dictionaries: embedding (6.3.4), composite fonts
// (6.3.3), subsets (6.3.5) and character encodings (6.3.7).
func validateFontDict(v PDFDict, ctx *ValidationContext) {
	if (v.Entries["Type"] != PDFName{Value: "Font"}) {
		return
	}
	subtype, _ := v.Entries["Subtype"].(PDFName)
	baseFont, _ := v.Entries["BaseFont"].(PDFName)
	subset := subsetTagRe.MatchString(baseFont.Value)
	desc, _ := v.Entries["FontDescriptor"].(PDFDict)

	// 6.3.2: where a font program is embedded, it shall be valid.
	validateFontProgram(v, desc, baseFont.Value, ctx)

	// Invisible-only fonts (render mode 3/7) are never rendered, so glyph
	// coverage/metric checks (6.3.3.2, 6.3.5, 6.3.6) don't apply.
	invisibleOnly := ctx.isInvisibleOnlyFont(v)

	switch subtype.Value {
	case "Type1", "MMType1", "TrueType":
		// 6.3.4: the font program shall be embedded.
		if !hasEmbeddedProgram(desc, "FontFile", "FontFile2", "FontFile3") {
			ctx.Report(Checks.Font.SimpleNotEmbedded, v, fmt.Sprintf("font %s is not embedded", baseFont.Value))
		}
		if subtype.Value == "TrueType" {
			validateTrueTypeEncoding(v, desc, ctx)
			if ff, ok := desc.Entries["FontFile2"].(PDFDict); ok && !invisibleOnly {
				firstChar, fcOK := v.Entries["FirstChar"].(PDFInteger)
				lastChar, lcOK := v.Entries["LastChar"].(PDFInteger)
				widths, wOK := v.Entries["Widths"].(PDFArray)
				if fcOK && lcOK && wOK {
					// 6.3.5: subset glyph-coverage check only applies to subset fonts.
					if subset {
						validateSimpleTrueTypeSubset(v, ff, int(firstChar), int(lastChar), widths, ctx)
					}
					validateSimpleTrueTypeMetrics(v, ff, int(firstChar), int(lastChar), widths, ctx)
				}
			}
		} else if !subset {
			// 6.3.6: advance widths in the embedded font program must match PDF Widths.
			if ff, ok := desc.Entries["FontFile"].(PDFDict); ok && !invisibleOnly {
				firstChar, fcOK := v.Entries["FirstChar"].(PDFInteger)
				lastChar, lcOK := v.Entries["LastChar"].(PDFInteger)
				widths, wOK := v.Entries["Widths"].(PDFArray)
				if fcOK && lcOK && wOK {
					pdfEnc, _ := v.Entries["Encoding"].(PDFName)
					validateType1Metrics(v, ff, int(firstChar), int(lastChar), widths, pdfEnc.Value, ctx)
				}
			}
		} else if subset {
			if desc.Entries != nil && desc.Entries["CharSet"] == nil {
				// 6.3.5: a Type 1 subset descriptor shall include CharSet.
				ctx.Report(Checks.Font.Type1SubsetCharSet, v, "Type 1 subset font descriptor lacks CharSet")
			} else if !invisibleOnly {
				// 6.3.5: every character code with non-zero width must map to a glyph in CharSet.
				firstChar, fcOK := v.Entries["FirstChar"].(PDFInteger)
				lastChar, lcOK := v.Entries["LastChar"].(PDFInteger)
				widths, wOK := v.Entries["Widths"].(PDFArray)
				if fcOK && lcOK && wOK {
					validateType1SubsetCoverage(v, v, desc, int(firstChar), int(lastChar), widths, ctx)
				}
			}
		}

	case "CIDFontType0", "CIDFontType2":
		// 6.3.4: composite font programs shall be embedded.
		if !hasEmbeddedProgram(desc, "FontFile2", "FontFile3") {
			ctx.Report(Checks.Font.CIDNotEmbedded, v, fmt.Sprintf("CID font %s is not embedded", baseFont.Value))
		}
		// 6.3.3.2: CIDFontType2 shall specify CIDToGIDMap.
		if subtype.Value == "CIDFontType2" && v.Entries["CIDToGIDMap"] == nil && !invisibleOnly {
			ctx.Report(Checks.Font.CIDToGIDMapMissing, v, "CIDFontType2 lacks CIDToGIDMap")
		}
		// 6.3.5: a CID subset descriptor shall include CIDSet.
		if subset && desc.Entries != nil && desc.Entries["CIDSet"] == nil {
			ctx.Report(Checks.Font.CIDSubsetCIDSet, v, "CID subset font descriptor lacks CIDSet")
		} else if subset && subtype.Value == "CIDFontType0" && !invisibleOnly {
			if ff, ok := desc.Entries["FontFile3"].(PDFDict); ok {
				validateCIDSetBitmap(v, desc, ff, ctx)
			}
		}
		// 6.3.5 / 6.3.6: glyph coverage (subset only) and metric consistency
		// for embedded CID fonts.
		if w, ok2 := v.Entries["W"].(PDFArray); ok2 && !invisibleOnly {
			switch subtype.Value {
			case "CIDFontType2":
				if ff, ok := desc.Entries["FontFile2"].(PDFDict); ok {
					// 6.3.5: subset glyph-coverage applies only to subset fonts.
					if subset {
						validateCIDTrueTypeSubset(v, ff, w, ctx)
					}
					validateCIDTrueTypeMetrics(v, ff, w, ctx)
				}
			case "CIDFontType0":
				if ff, ok := desc.Entries["FontFile3"].(PDFDict); ok {
					// 6.3.5: subset glyph-coverage applies only to subset fonts.
					if subset {
						validateCIDCFFSubset(v, ff, w, ctx)
					}
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
func validateType3Metrics(v PDFDict, ctx *ValidationContext) {
	firstChar, fcOK := v.Entries["FirstChar"].(PDFInteger)
	lastChar, lcOK := v.Entries["LastChar"].(PDFInteger)
	widths, wOK := v.Entries["Widths"].(PDFArray)
	charProcs, cpOK := v.Entries["CharProcs"].(PDFDict)
	enc, encOK := v.Entries["Encoding"].(PDFDict)
	if !fcOK || !lcOK || !wOK || !cpOK || !encOK {
		return
	}
	diffs, _ := enc.Entries["Differences"].(PDFArray)
	if diffs == nil {
		return
	}

	codeToGlyph := map[int]string{}
	code := 0
	for _, item := range diffs {
		switch d := item.(type) {
		case PDFInteger:
			code = int(d)
		case PDFName:
			codeToGlyph[code] = d.Value
			code++
		}
	}

	for i, w := range widths {
		var pdfWidth float64
		switch wv := w.(type) {
		case PDFInteger:
			pdfWidth = float64(wv)
		case PDFReal:
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
		proc, ok := charProcs.Entries[glyphName].(PDFDict)
		if !ok || !proc.HasStream {
			continue
		}
		data, err := decodeStream(proc)
		if err != nil {
			continue
		}
		procWidth := type3GlyphWidth(data)
		if procWidth < 0 {
			continue // no d0/d1 found
		}
		if abs64(procWidth-pdfWidth) > 1.0 {
			ctx.Report(Checks.Font.AdvanceWidthMismatch, v, fmt.Sprintf("Type3 glyph /%s: Widths entry %g does not match d0/d1 width %g", glyphName, pdfWidth, procWidth))
			return
		}
	}
}

// type3GlyphWidth extracts the wx (horizontal advance) from the first d0 or d1
// operator in a Type3 glyph procedure. Returns -1 if not found.
func type3GlyphWidth(data []byte) float64 {
	cs := newContentScanner(data)
	result := -1.0
	cs.scan(func(op string, operands []PDFValue) {
		if result >= 0 {
			return
		}
		if (op == "d0" || op == "d1") && len(operands) >= 1 {
			switch wv := operands[0].(type) {
			case PDFInteger:
				result = float64(wv)
			case PDFReal:
				result = float64(wv)
			}
		}
	})
	return result
}

// validateTrueTypeEncoding checks symbolic/non-symbolic TrueType encodings (6.3.7).
func validateTrueTypeEncoding(v PDFDict, desc PDFDict, ctx *ValidationContext) {
	flags := 0
	if f, ok := desc.Entries["Flags"].(PDFInteger); ok {
		flags = int(f)
	}
	symbolic := flags&4 != 0

	enc := v.Entries["Encoding"]
	if symbolic {
		// 6.3.7: a symbolic TrueType font shall not define Encoding.
		if enc != nil {
			ctx.Report(Checks.Font.SymbolicTrueTypeEncoding, v, "symbolic TrueType font shall not specify Encoding")
		}
		// 6.3.7: a symbolic TrueType cmap shall contain exactly one subtable.
		if n, ok := trueTypeCmapSubtables(desc); ok && n != 1 {
			ctx.Report(Checks.Font.SymbolicTrueTypeCmap, v, fmt.Sprintf("symbolic TrueType cmap has %d subtables, expected 1", n))
		}
		return
	}

	// Non-symbolic TrueType: Encoding shall be MacRomanEncoding or WinAnsiEncoding.
	name, ok := enc.(PDFName)
	if !ok || (name.Value != "MacRomanEncoding" && name.Value != "WinAnsiEncoding") {
		ctx.Report(Checks.Font.TrueTypeEncoding, v, "non-symbolic TrueType font shall use MacRoman or WinAnsi encoding")
	}
}

// descendantCIDFont returns the descendant CIDFont dictionary of a Type0 font.
func descendantCIDFont(v PDFDict) PDFDict {
	if arr, ok := v.Entries["DescendantFonts"].(PDFArray); ok && len(arr) > 0 {
		if d, ok := arr[0].(PDFDict); ok {
			return d
		}
	}
	return PDFDict{}
}

// validateType0Font checks composite font CMap embedding (6.3.3.3) and
// CIDSystemInfo consistency (6.3.3.1).
func validateType0Font(v PDFDict, ctx *ValidationContext) {
	enc := v.Entries["Encoding"]

	// 6.3.3.3: a CMap shall be one of the predefined CMaps or embedded.
	if name, ok := enc.(PDFName); ok && !predefinedCMaps[name.Value] {
		ctx.Report(Checks.Font.CMapNotEmbedded, v, fmt.Sprintf("CMap /%s is neither predefined nor embedded", name.Value))
	}
	// 6.3.3.3: an embedded CMap's WMode shall be consistent.
	if cmap, ok := enc.(PDFDict); ok {
		validateCMapWMode(v, cmap, ctx)
	}

	// 6.3.3.1: an embedded CMap's CIDSystemInfo shall match the CIDFont's.
	cmap, cmapOK := enc.(PDFDict)
	cid := descendantCIDFont(v)
	if cmapOK && cid.Entries != nil {
		cmapCSI, _ := cmap.Entries["CIDSystemInfo"].(PDFDict)
		cidCSI, _ := cid.Entries["CIDSystemInfo"].(PDFDict)
		if cmapCSI.Entries != nil && cidCSI.Entries != nil {
			if !sameCIDSystemInfo(cmapCSI, cidCSI) {
				ctx.Report(Checks.Font.CIDSystemInfoMismatch, v, "CMap and CIDFont CIDSystemInfo are incompatible")
			}
		}
	}
}

// sameCIDSystemInfo reports whether two CIDSystemInfo dictionaries share Registry
// and Ordering.
func sameCIDSystemInfo(a, b PDFDict) bool {
	return cidInfoField(a, "Registry") == cidInfoField(b, "Registry") &&
		cidInfoField(a, "Ordering") == cidInfoField(b, "Ordering")
}

func cidInfoField(d PDFDict, key string) string {
	if s, ok := d.Entries[key].(PDFString); ok {
		return s.Value
	}
	return ""
}

// validateCMapStream checks an embedded CMap stream for CID values exceeding
// the architectural limit of 65535 (PDF/A-1, 6.1.12 / PDF Reference Table H.1).
func validateCMapStream(v PDFDict, ctx *ValidationContext) {
	if v.Entries["Type"] != (PDFName{Value: "CMap"}) || !v.HasStream {
		return
	}
	data, err := decodeStream(v)
	if err != nil {
		return
	}
	checkCMapCIDLimits(v, data, ctx)
}

// checkCMapCIDLimits scans CMap PostScript content for CID values > 65535.
func checkCMapCIDLimits(obj PDFValue, data []byte, ctx *ValidationContext) {
	tokens := cmapTokenize(data)
	const maxCID = 65535

	inCIDRange := false
	inCIDChar := false
	pos := 0 // position within the current block entry (0-indexed token within triplet/pair)

	for _, tok := range tokens {
		switch tok {
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
					if cid, ok := cmapParseInt(tok); ok && cid > maxCID {
						ctx.Report(Checks.Structure.CMapCIDOutOfRange, obj, fmt.Sprintf("CMap CID value %d exceeds maximum of 65535", cid))
					}
					pos = 0
				}
			} else if inCIDChar {
				// cidchar entries: <code> CID (repeat).
				pos++
				if pos == 2 {
					if cid, ok := cmapParseInt(tok); ok && cid > maxCID {
						ctx.Report(Checks.Structure.CMapCIDOutOfRange, obj, fmt.Sprintf("CMap CID value %d exceeds maximum of 65535", cid))
					}
					pos = 0
				}
			}
		}
	}
}

// cmapTokenize splits CMap PostScript content into tokens, skipping comments.
func cmapTokenize(data []byte) []string {
	var tokens []string
	i := 0
	for i < len(data) {
		for i < len(data) && isWhitespace(data[i]) {
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
		if data[i] == '<' {
			// hex string token: <...> (stop at first >)
			j := i + 1
			for j < len(data) && data[j] != '>' {
				j++
			}
			if j < len(data) {
				j++
			}
			tokens = append(tokens, string(data[i:j]))
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
				if data[j] == '(' {
					depth++
				} else if data[j] == ')' {
					depth--
				}
				j++
			}
			tokens = append(tokens, string(data[i:j]))
			i = j
		} else {
			// regular token: read until whitespace or delimiter
			j := i
			for j < len(data) && !isWhitespace(data[j]) && data[j] != '<' && data[j] != '(' && data[j] != ')' {
				j++
			}
			if j > i {
				tokens = append(tokens, string(data[i:j]))
			}
			i = j
		}
	}
	return tokens
}

// cmapParseInt parses a decimal integer token from a CMap stream.
func cmapParseInt(tok string) (int64, bool) {
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
