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

	switch subtype.Value {
	case "Type1", "MMType1", "TrueType":
		// 6.3.4: the font program shall be embedded.
		if !hasEmbeddedProgram(desc, "FontFile", "FontFile2", "FontFile3") {
			ctx.ReportError(v, "6.3.4", 1, fmt.Sprintf("font %s is not embedded", baseFont.Value))
		}
		if subtype.Value == "TrueType" {
			validateTrueTypeEncoding(v, desc, ctx)
		} else if subset && desc.Entries != nil && desc.Entries["CharSet"] == nil {
			// 6.3.5: a Type 1 subset descriptor shall include CharSet.
			ctx.ReportError(v, "6.3.5", 2, "Type 1 subset font descriptor lacks CharSet")
		}

	case "CIDFontType0", "CIDFontType2":
		// 6.3.4: composite font programs shall be embedded.
		if !hasEmbeddedProgram(desc, "FontFile2", "FontFile3") {
			ctx.ReportError(v, "6.3.4", 2, fmt.Sprintf("CID font %s is not embedded", baseFont.Value))
		}
		// 6.3.3.2: CIDFontType2 shall specify CIDToGIDMap.
		if subtype.Value == "CIDFontType2" && v.Entries["CIDToGIDMap"] == nil {
			ctx.ReportError(v, "6.3.3.2", 1, "CIDFontType2 lacks CIDToGIDMap")
		}
		// 6.3.5: a CID subset descriptor shall include CIDSet.
		if subset && desc.Entries != nil && desc.Entries["CIDSet"] == nil {
			ctx.ReportError(v, "6.3.5", 3, "CID subset font descriptor lacks CIDSet")
		}

	case "Type0":
		validateType0Font(v, ctx)
	}
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
			ctx.ReportError(v, "6.3.7", 2, "symbolic TrueType font shall not specify Encoding")
		}
		// 6.3.7: a symbolic TrueType cmap shall contain exactly one subtable.
		if n, ok := trueTypeCmapSubtables(desc); ok && n != 1 {
			ctx.ReportError(v, "6.3.7", 3, fmt.Sprintf("symbolic TrueType cmap has %d subtables, expected 1", n))
		}
		return
	}

	// Non-symbolic TrueType: Encoding shall be MacRomanEncoding or WinAnsiEncoding.
	name, ok := enc.(PDFName)
	if !ok || (name.Value != "MacRomanEncoding" && name.Value != "WinAnsiEncoding") {
		ctx.ReportError(v, "6.3.7", 1, "non-symbolic TrueType font shall use MacRoman or WinAnsi encoding")
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
		ctx.ReportError(v, "6.3.3.3", 1, fmt.Sprintf("CMap /%s is neither predefined nor embedded", name.Value))
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
				ctx.ReportError(v, "6.3.3.1", 1, "CMap and CIDFont CIDSystemInfo are incompatible")
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
