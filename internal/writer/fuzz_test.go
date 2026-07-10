package writer_test

import (
	"io"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfgen"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// FuzzWritePDF drives the serializer directly on graphs parsed from arbitrary
// bytes: it opens the input, resolves the object graph, and writes it back out.
// Invariant: the writer must never panic -- a malformed/partly-resolved graph
// is a defined error path, not a crash.
func FuzzWritePDF(f *testing.F) {
	for _, s := range pdfgen.Seeds() {
		f.Add(s)
	}
	for _, b := range pdfgen.GenerateN(0, 64) {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		r, err := pdf.OpenBytes(data)
		if err != nil {
			return
		}
		defer r.Close()
		writer.WritePDF(r, io.Discard)
	})
}

// FuzzWriteContentStream fuzzes the content-stream serializer with operator
// names and operands (including nested arrays/dicts) built from the input.
func FuzzWriteContentStream(f *testing.F) {
	f.Add([]byte("Tj"), []byte("hello"))
	f.Fuzz(func(t *testing.T, op, operand []byte) {
		if len(op) > 64 || len(operand) > 1<<16 {
			return
		}
		ops := []writer.ContentOp{
			{Op: string(op), Operands: []pdf.PDFValue{pdf.PDFString{Value: string(operand)}}},
			{Op: "TJ", Operands: []pdf.PDFValue{pdf.PDFArray{
				pdf.PDFString{Value: string(operand)},
				pdf.PDFInteger(len(operand)),
				pdf.PDFName{Value: string(op)},
			}}},
		}
		writer.WriteContentStream(ops)
	})
}

// FuzzBuildInlineImageBytes fuzzes inline-image (BI/ID/EI) serialization from
// arbitrary params and pixel data.
func FuzzBuildInlineImageBytes(f *testing.F) {
	f.Add([]byte("data"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<16 {
			return
		}
		params := []pdf.PDFValue{
			pdf.PDFName{Value: "W"}, pdf.PDFInteger(4),
			pdf.PDFName{Value: "H"}, pdf.PDFInteger(4),
			pdf.PDFName{Value: "BPC"}, pdf.PDFInteger(8),
		}
		writer.BuildInlineImageBytes(params, data)
	})
}
