package pdfa

import "github.com/voidrab/gopdfrab/internal/pdf"

// Issue returns the issue a reader diagnostic raises under the level. ok is
// false when the level permits what the diagnostic records. An issue about
// the file rather than an object is reported on page 1.
func (s Spec) Issue(dg pdf.Diagnostic) (e pdf.PDFError, ok bool) {
	c, ok := diagnosticCheck(dg.Kind)
	if !ok || !c.AppliesTo(s.Level) || dg.Kind == pdf.DiagUsedXRefStream && s.XRefStreams ||
		dg.Kind == pdf.DiagXRefRebuilt && !s.ExactXRefOffsets {
		return pdf.PDFError{}, false
	}
	if dg.Ref == nil {
		return pdf.NewError(c, dg.Errs, 1, nil), true
	}
	// The object goes in the reference, never the page: an object number is
	// not a page number, and Page drives which pages convert rasterizes.
	return pdf.NewError(c, dg.Errs, 0, dg.Ref), true
}

// Issues returns the issues a list of reader diagnostics raises under the
// level, in order.
func (s Spec) Issues(dgs []pdf.Diagnostic) []pdf.PDFError {
	var out []pdf.PDFError
	for _, dg := range dgs {
		if e, ok := s.Issue(dg); ok {
			out = append(out, e)
		}
	}
	return out
}

// diagnosticCheck is the check a diagnostic kind violates, at any level that
// forbids it.
func diagnosticCheck(k pdf.DiagKind) (pdf.Check, bool) {
	st := pdf.Checks.Structure
	switch k {
	case pdf.DiagObjectFraming:
		return st.ObjectFraming, true
	case pdf.DiagStreamKeywordEOL:
		return st.StreamKeywordEOL, true
	case pdf.DiagStreamLengthIncludesEOL:
		return st.StreamLengthIncludesEOL, true
	case pdf.DiagStreamLengthMismatch:
		return st.StreamLengthMismatch, true
	case pdf.DiagEndstreamEOL:
		return st.EndstreamEOL, true
	case pdf.DiagUsedXRefStream:
		return st.XRefStream, true
	case pdf.DiagXRefRebuilt:
		return st.XRefKeyword, true
	case pdf.DiagObjectDegraded, pdf.DiagObjectRecovered, pdf.DiagStringUndecryptable:
		return st.GraphResolutionFailure, true
	}
	return pdf.Check{}, false
}
