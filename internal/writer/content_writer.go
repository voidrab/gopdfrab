package writer

import (
	"bytes"
	"fmt"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// ContentOp is one operator and its operands, the inverse of the (op,
// operands) pair newContentScanner's scan callback receives.
type ContentOp struct {
	Op       string
	Operands []pdf.PDFValue
}

// ContentStreamWriter serializes operators one at a time, so a rewriter can
// emit while it scans instead of building the whole operator list first. That
// also removes the aliasing hazard accumulating invites: ContentScanner.Scan
// reuses one operand stack, so operands are only valid inside the callback.
//
// Write errors are sticky, so a caller inside a callback that cannot return an
// error may scan to the end and check Err once.
type ContentStreamWriter struct {
	buf bytes.Buffer
	err error
}

// WriteOp appends one operator and its operands, which need only be valid for
// the call.
func (w *ContentStreamWriter) WriteOp(op string, operands []pdf.PDFValue) error {
	if w.err != nil {
		return w.err
	}
	if op == "INLINEIMAGE" {
		raw, ok := inlineImageBytes(operands)
		if !ok {
			w.err = fmt.Errorf("content op %q: missing raw inline image data", op)
			return w.err
		}
		w.buf.Write(raw)
		w.buf.WriteByte('\n')
		return nil
	}
	for i, operand := range operands {
		if i > 0 {
			w.buf.WriteByte(' ')
		}
		if err := writeOperand(&w.buf, operand); err != nil {
			w.err = fmt.Errorf("content op %q: %w", op, err)
			return w.err
		}
	}
	if len(operands) > 0 {
		w.buf.WriteByte(' ')
	}
	w.buf.WriteString(op)
	w.buf.WriteByte('\n')
	return nil
}

// Err reports the first write failure.
func (w *ContentStreamWriter) Err() error { return w.err }

// Bytes returns the serialized stream; meaningful only when Err is nil.
func (w *ContentStreamWriter) Bytes() []byte { return w.buf.Bytes() }

// Reset discards everything written, including a sticky error.
func (w *ContentStreamWriter) Reset() {
	w.buf.Reset()
	w.err = nil
}

// WriteContentStream serializes ops back to content-stream bytes, the
// inverse of NewContentScanner(...).scan. An "INLINEIMAGE" op (the scanner's
// pseudo-operator for a BI...EI sequence) is re-emitted by writing its
// trailing pdf.InlineImageRaw operand's bytes verbatim, ignoring the parsed
// params -- see pdf.InlineImageRaw's doc comment in content.go.
//
// Batch form of ContentStreamWriter; prefer that when the ops come from a scan.
func WriteContentStream(ops []ContentOp) ([]byte, error) {
	var w ContentStreamWriter
	for _, op := range ops {
		if err := w.WriteOp(op.Op, op.Operands); err != nil {
			return nil, err
		}
	}
	return w.Bytes(), nil
}

// inlineImageBytes extracts the trailing pdf.InlineImageRaw operand
// scanInlineImage appends to an "INLINEIMAGE" op's operands.
func inlineImageBytes(operands []pdf.PDFValue) ([]byte, bool) {
	if len(operands) == 0 {
		return nil, false
	}
	raw, ok := operands[len(operands)-1].(pdf.InlineImageRaw)
	return raw.Bytes, ok
}

// BuildInlineImageBytes rebuilds a fresh verbatim "BI...EI" byte span from
// edited params and image data, the inverse of scanInlineImage -- used only
// when a fixer has actually changed an inline image's params or data; an
// untouched image is passed through via its captured pdf.InlineImageRaw.Bytes
// instead, so this canonical (re-spaced) form never appears unless something
// was fixed.
func BuildInlineImageBytes(params []pdf.PDFValue, data []byte) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("BI")
	for _, p := range params {
		buf.WriteByte(' ')
		if err := writeOperand(&buf, p); err != nil {
			return nil, fmt.Errorf("inline image param: %w", err)
		}
	}
	buf.WriteString(" ID ")
	buf.Write(data)
	buf.WriteString(" EI")
	return buf.Bytes(), nil
}
