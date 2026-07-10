// Package pdfgen programmatically builds PDF documents -- both structurally
// valid skeletons and deliberately corrupted, "crazy, broken" variants -- for
// fuzzing and stress-testing the parser, verifier, and converter.
//
// It emits PDFs entirely in memory as []byte and never touches disk, so no
// external document files are needed. Given a fixed seed the output is fully
// deterministic and reproducible.
//
// The package depends only on the standard library (notably compress/zlib for
// stream compression) and intentionally does NOT import internal/pdf, so it can
// be imported by fuzz targets in any package without risking an import cycle.
package pdfgen

import (
	"bytes"
	"compress/zlib"
	"fmt"
)

// Builder assembles a PDF byte-for-byte, tracking the byte offset of each
// indirect object as it is written so a classic cross-reference table or a
// /W-encoded cross-reference stream can be computed exactly like a real writer
// would. It is a reusable promotion of the previously test-only pdfBuilder
// (internal/pdf/xrefstream_test.go) and writeMinimalPDF/buildClassicXRefBody
// (internal/pdf/document_test.go) helpers.
type Builder struct {
	buf     bytes.Buffer
	offsets map[int]int64
	order   []int
}

// NewBuilder starts a document with the given header (e.g. "%PDF-1.7\n"). A
// binary-marker comment line is conventionally included by callers after the
// version comment.
func NewBuilder(header string) *Builder {
	b := &Builder{offsets: map[int]int64{}}
	b.buf.WriteString(header)
	return b
}

// Obj writes a non-stream indirect object with framing that satisfies ISO
// 32000 6.1.8 (single LF after "obj" and around "endobj").
func (b *Builder) Obj(num int, body string) {
	b.offsets[num] = int64(b.buf.Len())
	b.order = append(b.order, num)
	fmt.Fprintf(&b.buf, "%d 0 obj\n%s\nendobj\n", num, body)
}

// StreamObj writes an indirect stream object. dictHead is the dictionary
// without its closing ">>" (e.g. "<< /Type /ObjStm /N 3 /First 18"); /Length
// and the closing ">>" are appended automatically with a correct length.
func (b *Builder) StreamObj(num int, dictHead string, raw []byte) {
	b.offsets[num] = int64(b.buf.Len())
	b.order = append(b.order, num)
	fmt.Fprintf(&b.buf, "%d 0 obj\n%s /Length %d >>\nstream\n", num, dictHead, len(raw))
	b.buf.Write(raw)
	b.buf.WriteString("\nendstream\nendobj\n")
}

// OffsetOf returns the byte offset at which object num was written.
func (b *Builder) OffsetOf(num int) int64 { return b.offsets[num] }

// Len returns the current length of the document in bytes (e.g. the offset a
// cross-reference stream about to be written will have).
func (b *Builder) Len() int64 { return int64(b.buf.Len()) }

// Write appends raw bytes to the document (for hand-assembled trailers or
// cross-reference sections).
func (b *Builder) Write(p []byte) { b.buf.Write(p) }

// WriteString appends s to the document.
func (b *Builder) WriteString(s string) { b.buf.WriteString(s) }

// Bytes returns the document assembled so far.
func (b *Builder) Bytes() []byte { return b.buf.Bytes() }

// FinishClassic appends a correct classic cross-reference table covering the
// objects written so far (which must be numbered 1..N contiguously, in order),
// the given trailer dictionary body, and "startxref/%%EOF". It returns the
// full document bytes.
func (b *Builder) FinishClassic(trailerBody string) []byte {
	n := len(b.order)
	xrefOffset := b.buf.Len()
	fmt.Fprintf(&b.buf, "xref\n0 %d\n0000000000 65535 f \n", n+1)
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b.buf, "%010d 00000 n \n", b.offsets[i])
	}
	fmt.Fprintf(&b.buf, "trailer\n%s\nstartxref\n%d\n%%%%EOF", trailerBody, xrefOffset)
	return b.buf.Bytes()
}

// FinishStartxref appends "startxref\n<offset>\n%%EOF" and returns the full
// document bytes. Used when the cross-reference section is a stream object the
// caller has already written.
func (b *Builder) FinishStartxref(startxrefOffset int64) []byte {
	fmt.Fprintf(&b.buf, "startxref\n%d\n%%%%EOF", startxrefOffset)
	return b.buf.Bytes()
}

// deflate zlib-compresses data. Writing to a bytes.Buffer cannot fail, so any
// (impossible) error is treated as fatal programmer error.
func deflate(data []byte) []byte {
	var out bytes.Buffer
	w := zlib.NewWriter(&out)
	if _, err := w.Write(data); err != nil {
		panic("pdfgen: zlib write: " + err.Error())
	}
	if err := w.Close(); err != nil {
		panic("pdfgen: zlib close: " + err.Error())
	}
	return out.Bytes()
}

// beField encodes v as a big-endian field of the given byte width, for
// cross-reference-stream /W entries.
func beField(v int, width int) []byte {
	out := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		out[i] = byte(v)
		v >>= 8
	}
	return out
}
