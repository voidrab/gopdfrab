package pdfrab

import (
	"fmt"
)

type LevelType int

const (
	Undefined LevelType = iota
	A1_B
)

type Result struct {
	Type   LevelType
	Valid  bool
	Issues map[string]string
}

// Verify processes d to conformance level t.
func (d *Document) Verify(t LevelType) (Result, error) {
	basicResult := Result{
		Type:   t,
		Valid:  false,
		Issues: nil,
	}

	if t == Undefined {
		return basicResult, fmt.Errorf("cannot verify PDF to undefined conformance level")
	}

	issues := make(map[string]string)

	err := d.verifyFileHeader()
	if err != nil {
		issues["6.1.2"] = err.Error()
	}

	if len(issues) > 0 {
		return Result{
			Type:   t,
			Valid:  false,
			Issues: issues,
		}, nil
	}

	return Result{
		Type:   t,
		Valid:  true,
		Issues: nil,
	}, nil
}

// PDF/A-1

// verifyFileHeader verifies PDF/A-1 requirements 6.1.2.
func (d *Document) verifyFileHeader() error {
	buf := make([]byte, 128)
	n, _ := d.file.ReadAt(buf, 0)

	cur := NewCursor(buf[:n])

	header, ok := cur.ReadLine()
	if !ok || header[0] != '%' {
		return fmt.Errorf("invalid PDF header")
	}

	comment, ok := cur.ReadLine()
	if !ok || comment[0] != '%' {
		return fmt.Errorf("header must be followed by comment")
	}

	if len(comment) < 5 {
		return fmt.Errorf("comment line must consist of at least 5 characters")
	}

	for _, byte := range comment[1:] {
		if byte <= 127 {
			return fmt.Errorf("byte values in comment line must be > 127")
		}
	}

	return nil
}
