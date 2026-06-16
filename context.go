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
