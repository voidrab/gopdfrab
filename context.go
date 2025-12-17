package pdfrab

import "errors"

type ValidationContext struct {
	PageIndex   map[int]int
	CurrentPage int
	errs        []PDFError
}

func (ctx *ValidationContext) report(err PDFError) {
	ctx.errs = append(ctx.errs, err)
}

func (ctx *ValidationContext) PersistError(obj PDFValue, clause string, subclause int, msg string) {
	var ref *PDFRef
	if dict, ok := obj.(PDFDict); ok {
		if r, ok := dict["_ref"].(PDFRef); ok {
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

func (ctx *ValidationContext) PersistErrors(obj PDFValue, clause string, subclause int, errs []error) {
	var ref *PDFRef
	if dict, ok := obj.(PDFDict); ok {
		if r, ok := dict["_ref"].(PDFRef); ok {
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
