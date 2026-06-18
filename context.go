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
