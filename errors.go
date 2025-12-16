package pdfrab

type PDFError struct {
	clause    string
	subclause int
	errs      []error
	page      int
}
