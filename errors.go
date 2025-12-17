package pdfrab

import (
	"fmt"
	"strings"
)

type PDFError struct {
	clause    string
	subclause int
	errs      []error
	page      int

	objectRef *PDFRef
}

func (e PDFError) String() string {
	var b strings.Builder

	b.WriteString("PDF/A violation")
	if e.clause != "" {
		b.WriteString(" (")
		b.WriteString(e.clause)
		if e.subclause > 0 {
			fmt.Fprintf(&b, "/%d", e.subclause)
		}
		b.WriteString(")")
	}

	if e.page > 0 {
		fmt.Fprintf(&b, ", page %d", e.page)
	} else {
		b.WriteString(", document-level")
	}

	if e.objectRef != nil {
		fmt.Fprintf(&b, ", ref %v", e.objectRef)
	}

	// Error messages
	if len(e.errs) > 0 {
		b.WriteString(": \"")
		for i, err := range e.errs {
			if i > 0 {
				b.WriteString("\"; \"")
			}
			b.WriteString(err.Error())
		}
		b.WriteString("\"")
	}

	return b.String()
}

func (e PDFError) Error() string {
	return e.String()
}
