package writer_test

import (
	"io"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// TestCrasher_WriterDeepNesting verifies that WriteDocument does not crash on
// a deeply nested but acyclic inline array. The pointer-based cycle guard does
// not catch fresh composites, so maxWriteDepth bounds the recursion.
func TestCrasher_WriterDeepNesting(t *testing.T) {
	deep := pdf.PDFValue(pdf.PDFArray{})
	for i := 0; i < 20000; i++ {
		deep = pdf.PDFArray{deep}
	}

	catalog := pdf.NewPDFDict()
	catalog.Entries.Set("Type", pdf.PDFName{Value: "Catalog"})
	catalog.Entries.Set("Junk", deep)
	catalog.SetRef(pdf.PDFRef{ObjNum: 1})

	trailer := pdf.NewPDFDict()
	trailer.Entries.Set("Root", catalog)
	trailer.Entries.Set("Size", pdf.PDFInteger(2))

	// Must not panic or overflow the stack; an error is the expected outcome.
	writer.WriteDocument(io.Discard, trailer, 0)
}
