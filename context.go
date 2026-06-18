package pdfrab

import "errors"

type ValidationContext struct {
	PageIndex   map[int]int
	CurrentPage int
	errs        []PDFError

	// OutputIntent colour-model coverage (6.2.2 / 6.2.3.3). hasOutputIntent is
	// true when a GTS_PDFA1 output intent exists; the *Covered flags indicate the
	// destination profile colour model (N = 1/3/4).
	hasOutputIntent bool
	rgbCovered      bool
	cmykCovered     bool
	grayCovered     bool

	// ReachableXObjectPtrs is the set of Entries-map pointers of Form XObjects
	// that are actually invoked (via Do) from page or other reachable content
	// streams. Nil means unknown (treat everything as reachable).
	ReachableXObjectPtrs map[uintptr]bool

	// InvisibleOnlyFontPtrs is the set of font dictionary Entries-map pointers
	// that are used for text showing exclusively under rendering mode 3 or 7
	// (invisible) somewhere in the document, and never under any other mode.
	// Such fonts are never actually rendered, so 6.3.3.2 (CIDToGIDMap), 6.3.5
	// (glyph coverage) and 6.3.6 (advance width consistency) do not apply.
	InvisibleOnlyFontPtrs map[uintptr]bool

	// UsedCharCodes maps a simple (non-composite) font's Entries-map pointer
	// to the set of single-byte character codes actually passed to a
	// text-showing operator somewhere in the document. A subset font's
	// CharSet only needs to list glyphs "used for rendering" (6.3.5); a code
	// with a non-zero Widths entry that is never actually shown need not be
	// present. Nil for a font means no usage info was collected for it.
	UsedCharCodes map[uintptr]map[int]bool

	// pageResources is the Resources dict of the current page. Default* colour
	// spaces defined at page level are inherited by patterns and Form XObjects
	// that do not define their own Default*.
	pageResources PDFDict
}

// isReachableXObject returns true if v is a Form XObject that is reachable
// from page content via Do operators.  If reachability info is absent,
// everything is considered reachable (safe fallback).
func (ctx *ValidationContext) isReachableXObject(v PDFDict) bool {
	if ctx.ReachableXObjectPtrs == nil {
		return true
	}
	return ctx.ReachableXObjectPtrs[pdfValuePointer(v.Entries)]
}

// isInvisibleOnlyFont returns true if font dictionary v is used for text
// showing only under an invisible rendering mode (3 or 7), and is therefore
// exempt from glyph-coverage and metric-consistency checks (6.3.3.2, 6.3.5,
// 6.3.6). If usage info is absent, the font is treated as visible (checked).
func (ctx *ValidationContext) isInvisibleOnlyFont(v PDFDict) bool {
	if ctx.InvisibleOnlyFontPtrs == nil {
		return false
	}
	return ctx.InvisibleOnlyFontPtrs[pdfValuePointer(v.Entries)]
}

// usedCodesFor returns the set of character codes actually shown for font v,
// and whether usage info was collected for it at all. When known is false,
// callers should fall back to a broader check (e.g. every code with a
// non-zero Widths entry), since the font's content-stream usage could not be
// determined.
func (ctx *ValidationContext) usedCodesFor(v PDFDict) (codes map[int]bool, known bool) {
	if ctx.UsedCharCodes == nil {
		return nil, false
	}
	codes, known = ctx.UsedCharCodes[pdfValuePointer(v.Entries)]
	return codes, known
}

// deviceColourAllowed reports whether a device colour model ("rgb", "cmyk",
// "gray") may be used given the document's OutputIntent coverage (6.2.3.3).
func (ctx *ValidationContext) deviceColourAllowed(model string) bool {
	switch model {
	case "rgb":
		return ctx.rgbCovered
	case "cmyk":
		return ctx.cmykCovered
	case "gray":
		// DeviceGray is permitted in the presence of any PDF/A output intent.
		return ctx.hasOutputIntent
	}
	return true
}

func (ctx *ValidationContext) report(err PDFError) {
	ctx.errs = append(ctx.errs, err)
}

func (ctx *ValidationContext) ReportError(obj PDFValue, clause string, subclause int, msg string) {
	var ref *PDFRef
	if dict, ok := obj.(PDFDict); ok {
		if r, ok := dict.Entries["_ref"].(PDFRef); ok {
			ref = &r
		}
	}

	var page int
	if ctx == nil {
		page = 0
	} else {
		page = ctx.CurrentPage
	}

	err := PDFError{
		clause:    clause,
		subclause: subclause,
		errs:      []error{errors.New(msg)},
		objectRef: ref,
		page:      page,
	}

	ctx.report(err)
}

func (ctx *ValidationContext) ReportErrors(obj PDFValue, clause string, subclause int, errs []error) {
	var ref *PDFRef
	if dict, ok := obj.(PDFDict); ok {
		if r, ok := dict.Entries["_ref"].(PDFRef); ok {
			ref = &r
		}
	}

	var page int
	if ctx == nil {
		page = 0
	} else {
		page = ctx.CurrentPage
	}

	err := PDFError{
		clause:    clause,
		subclause: subclause,
		errs:      errs,
		objectRef: ref,
		page:      page,
	}

	ctx.report(err)
}
