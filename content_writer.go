package pdfrab

import (
	"bytes"
	"fmt"
)

// contentOp is one operator and its operands, the inverse of the (op,
// operands) pair newContentScanner's scan callback receives.
type contentOp struct {
	Op       string
	Operands []PDFValue
}

// writeContentStream serializes ops back to content-stream bytes, the
// inverse of newContentScanner(...).scan. Inline images (BI/ID/EI) are not
// supported -- the scanner already discards their binary payload, so there
// is nothing to round-trip.
func writeContentStream(ops []contentOp) ([]byte, error) {
	var buf bytes.Buffer
	for _, op := range ops {
		for i, operand := range op.Operands {
			if i > 0 {
				buf.WriteByte(' ')
			}
			if err := writeOperand(&buf, operand); err != nil {
				return nil, fmt.Errorf("content op %q: %w", op.Op, err)
			}
		}
		if len(op.Operands) > 0 {
			buf.WriteByte(' ')
		}
		buf.WriteString(op.Op)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}
