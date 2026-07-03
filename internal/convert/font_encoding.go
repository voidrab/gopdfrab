package convert

import (
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
)

// symbolGlyphNameToUnicode resolves a glyph name via the AGL-ish table first,
// then the Symbol and ZapfDingbats glyph lists (their names are disjoint).
func symbolGlyphNameToUnicode(name string) (uint16, bool) {
	if u, ok := verify.GlyphNameToUnicode(name); ok {
		return u, true
	}
	if u, ok := verify.SymbolGlyphNameUnicode[name]; ok {
		return u, true
	}
	if u, ok := verify.ZapfDingbatsGlyphNameUnicode[name]; ok {
		return u, true
	}
	return 0, false
}

// standardSymbolBuiltinTable returns the built-in encoding of the standard
// Symbol/ZapfDingbats fonts for d's BaseFont, or ok=false for other fonts.
func standardSymbolBuiltinTable(d pdf.PDFDict) ([256]uint16, bool) {
	baseFont, _ := d.Entries["BaseFont"].(pdf.PDFName)
	name := verify.SubsetTagRe.ReplaceAllString(baseFont.Value, "")
	switch name {
	case "Symbol":
		return verify.SymbolToUnicode, true
	case "ZapfDingbats":
		return verify.ZapfDingbatsToUnicode, true
	}
	return [256]uint16{}, false
}

func fontFlagsSymbolic(d pdf.PDFDict) bool {
	desc, ok := d.Entries["FontDescriptor"].(pdf.PDFDict)
	if !ok {
		return false
	}
	flags, _ := desc.Entries["Flags"].(pdf.PDFInteger)
	return flags&4 != 0
}

// originalSimpleFontCodeToUnicode resolves what each character code meant
// under the font's original encoding, before any fixer rewrites it. A zero
// entry means the code has no known meaning; baseKnown distinguishes "the
// encoding is known and the code renders .notdef" from "unknowable".
func originalSimpleFontCodeToUnicode(d pdf.PDFDict) (table [256]uint16, baseKnown bool) {
	applyBase := func(name string) {
		baseKnown = true
		switch name {
		case "WinAnsiEncoding":
			table = verify.WinAnsiToUnicode
		case "MacRomanEncoding":
			table = verify.MacRomanToUnicode
		case "StandardEncoding":
			table = verify.StandardToUnicode
		default:
			baseKnown = false
		}
	}

	switch enc := d.Entries["Encoding"].(type) {
	case pdf.PDFName:
		applyBase(enc.Value)
	case pdf.PDFDict:
		if base, ok := enc.Entries["BaseEncoding"].(pdf.PDFName); ok {
			applyBase(base.Value)
		} else if t, ok := standardSymbolBuiltinTable(d); ok {
			table, baseKnown = t, true
		} else if !fontFlagsSymbolic(d) {
			table, baseKnown = verify.StandardToUnicode, true
		}
		if diffs, ok := enc.Entries["Differences"].(pdf.PDFArray); ok {
			code := 0
			for _, item := range diffs {
				switch v := item.(type) {
				case pdf.PDFInteger:
					code = int(v)
				case pdf.PDFName:
					if code >= 0 && code < 256 {
						u, _ := symbolGlyphNameToUnicode(v.Value)
						table[code] = u
					}
					code++
				}
			}
		}
	default:
		if t, ok := standardSymbolBuiltinTable(d); ok {
			table, baseKnown = t, true
		} else if !fontFlagsSymbolic(d) {
			// Matches the substitution fixer's long-standing assumption for
			// encoding-less non-symbolic fonts.
			table, baseKnown = verify.WinAnsiToUnicode, true
		}
	}

	// A /ToUnicode CMap authoritatively fills codes still unresolved.
	if toUni, ok := d.Entries["ToUnicode"].(pdf.PDFDict); ok && toUni.HasStream {
		if data, err := pdf.DecodeStream(toUni); err == nil {
			for code, u := range parseToUnicodeCMap(data) {
				if code >= 0 && code < 256 && table[code] == 0 {
					table[code] = u
				}
			}
		}
	}
	return table, baseKnown
}

// forEachAssumedUsedCode invokes fn for every code d is assumed to render:
// tracked usage when available, else non-zero-width codes, else every code.
// It stops early and reports false when fn does.
func forEachAssumedUsedCode(d pdf.PDFDict, usedCodes map[uintptr]map[int]bool, fn func(cc int) bool) bool {
	if usedCodes != nil {
		if codes := usedCodes[pdf.ValuePointer(d.Entries)]; codes != nil {
			for cc := range codes {
				if cc >= 0 && cc <= 255 && !fn(cc) {
					return false
				}
			}
			return true
		}
	}
	firstChar, _ := d.Entries["FirstChar"].(pdf.PDFInteger)
	widths, _ := d.Entries["Widths"].(pdf.PDFArray)
	if len(widths) > 0 {
		for i, w := range widths {
			if n, ok := pdf.PDFNumberToInt(w); ok && n > 0 {
				if cc := int(firstChar) + i; cc >= 0 && cc <= 255 && !fn(cc) {
					return false
				}
			}
		}
		return true
	}
	for cc := 0; cc < 256; cc++ {
		if !fn(cc) {
			return false
		}
	}
	return true
}

// encodingRewritePreservesMeaning reports whether declaring finalTable's
// encoding for d keeps origTable's meaning of every code the font is assumed
// to render; a mismatch means the untouched content-stream bytes would
// silently change what the reader sees.
func encodingRewritePreservesMeaning(d pdf.PDFDict, usedCodes map[uintptr]map[int]bool, origTable, finalTable [256]uint16) bool {
	return forEachAssumedUsedCode(d, usedCodes, func(cc int) bool {
		return origTable[cc] == finalTable[cc]
	})
}
