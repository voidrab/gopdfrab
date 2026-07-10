package writer_test

import (
	"io"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// TestCrasher_WriterDeepNesting: a deeply nested but acyclic inline array
// recursed discover/writeValue into a stack overflow -- the pointer-based
// cycle guard does not catch fresh composites (fixed: maxWriteDepth). The
// serializer must return an error, not crash.
func TestCrasher_WriterDeepNesting(t *testing.T) {
	deep := pdf.PDFValue(pdf.PDFArray{})
	for i := 0; i < 20000; i++ {
		deep = pdf.PDFArray{deep}
	}

	catalog := pdf.NewPDFDict()
	catalog.Entries["Type"] = pdf.PDFName{Value: "Catalog"}
	catalog.Entries["Junk"] = deep
	catalog.Entries["_ref"] = pdf.PDFRef{ObjNum: 1}

	trailer := pdf.NewPDFDict()
	trailer.Entries["Root"] = catalog
	trailer.Entries["Size"] = pdf.PDFInteger(2)

	// Must not panic / stack-overflow; an error is the expected fixed outcome.
	writer.WriteDocument(io.Discard, trailer)
}
