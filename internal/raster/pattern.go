package raster

import (
	"github.com/voidrab/gopdfrab/internal/pdf"
)

// A Pattern colour space names its colour with scn rather than numbers, so
// the ordinary numeric path never fires and the fill colour stays whatever
// the preceding cs left behind -- black. Shading patterns (PatternType 2)
// are now painted through their shading; tiling patterns (PatternType 1) are
// reported as a drop, since a flat black fill would be a silent loss.

// isPatternColorSpace reports whether cs is the Pattern colour space, in
// either the bare-name or [/Pattern base] form.
func isPatternColorSpace(cs pdf.PDFValue) bool {
	switch v := cs.(type) {
	case pdf.PDFName:
		return v.Value == "Pattern"
	case pdf.PDFArray:
		if len(v) == 0 {
			return false
		}
		name, ok := v[0].(pdf.PDFName)
		return ok && name.Value == "Pattern"
	}
	return false
}
