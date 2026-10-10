package convert

import (
	"github.com/voidrab/gopdfrab/internal/pdf"
)

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
	firstChar, _ := d.Entries.Get("FirstChar").(pdf.PDFInteger)
	widths, _ := d.Entries.Get("Widths").(pdf.PDFArray)
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
