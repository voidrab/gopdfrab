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

// writeContentStream serializes ops back to content-stream bytes, the
// inverse of NewContentScanner(...).scan. An "INLINEIMAGE" op (the scanner's
// pseudo-operator for a BI...EI sequence) is re-emitted by writing its
// trailing pdf.InlineImageRaw operand's bytes verbatim, ignoring the parsed
// params -- see pdf.InlineImageRaw's doc comment in content.go.
func WriteContentStream(ops []ContentOp) ([]byte, error) {
	var buf bytes.Buffer
	for _, op := range ops {
		if op.Op == "INLINEIMAGE" {
			raw, ok := inlineImageBytes(op.Operands)
			if !ok {
				return nil, fmt.Errorf("content op %q: missing raw inline image data", op.Op)
			}
			buf.Write(raw)
			buf.WriteByte('\n')
			continue
		}
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
