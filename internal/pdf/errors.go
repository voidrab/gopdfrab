package pdf

import (
	"fmt"
	"strings"
)

type PDFError struct {
	check Check
	errs  []error
	page  int

	objectRef *PDFRef
	objModel  *ObjModelDetail
}

// ObjModelDetail identifies the Arlington schema location behind an
// object-model finding: the schema type of the reported container and the key
// (or decimal array index) whose constraint was violated.
type ObjModelDetail struct {
	TypeName string
	Key      string
}

// NewError constructs a PDFError reporting a violation of c, found on page
// (0 for document-level) and optionally tied to a specific indirect object.
func NewError(c Check, errs []error, page int, ref *PDFRef) PDFError {
	return PDFError{check: c, errs: errs, page: page, objectRef: ref}
}

// WithObjModelDetail returns a copy of e carrying the Arlington schema
// location d, so object-model fixers can target the offending key directly.
func (e PDFError) WithObjModelDetail(d ObjModelDetail) PDFError {
	e.objModel = &d
	return e
}

func (e PDFError) String() string {
	var b strings.Builder

	b.WriteString("PDF/A violation")
	if e.check.clause != "" {
		b.WriteString(" (")
		b.WriteString(e.check.clause)
		if e.check.subclause > 0 {
			fmt.Fprintf(&b, "/%d", e.check.subclause)
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

// Page returns the 1-based page number this violation was found on, or 0 if
// the violation is document-level (see IsDocumentLevel).
func (e PDFError) Page() int {
	return e.page
}

// IsDocumentLevel reports whether this violation applies to the document as a
// whole rather than to a specific page.
func (e PDFError) IsDocumentLevel() bool {
	return e.page == 0
}

// ObjectRef returns the indirect object this violation was reported against,
// and whether one was recorded. Not every violation is tied to a single object
// (e.g. file-header or trailer issues), in which case ok is false.
func (e PDFError) ObjectRef() (ref PDFRef, ok bool) {
	if e.objectRef == nil {
		return PDFRef{}, false
	}
	return *e.objectRef, true
}

// Messages returns the underlying error messages for this violation.
func (e PDFError) Messages() []string {
	out := make([]string, len(e.errs))
	for i, err := range e.errs {
		out[i] = err.Error()
	}
	return out
}

// Check returns the registered Check this violation corresponds to.
func (e PDFError) Check() Check {
	return e.check
}

// ObjModelDetail returns the Arlington schema location this object-model
// violation was found at, and whether one was recorded. Only findings from the
// objmodel clause carry a detail.
func (e PDFError) ObjModelDetail() (d ObjModelDetail, ok bool) {
	if e.objModel == nil {
		return ObjModelDetail{}, false
	}
	return *e.objModel, true
}
