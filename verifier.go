package pdfrab

import (
	"fmt"
	"strings"
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
	err = d.verifyFileTrailer()
	if err != nil {
		issues["6.1.3"] = err.Error()
	}
	err = d.verifyCrossReferenceTable()
	if err != nil {
		issues["6.1.4"] = err.Error()
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

// PDF/A-1b (ISO 19005-1:2005)

// verifyFileHeader verifies requirements outlined in 6.1.2.
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

// verifyFileTrailer verifies requirements outlined in 6.1.3
func (d *Document) verifyFileTrailer() error {
	// The file trailer dictionary shall contain the ID keyword.
	if d.trailer["ID"] == nil {
		return fmt.Errorf("trailer does not contain the required ID keyword")
	}

	// The keyword Encrypt shall not be used in the trailer dictionary.
	if d.trailer["Encrypt"] != nil {
		return fmt.Errorf("trailer contains the forbidden Encrypt keyword")
	}

	// No data shall follow the last end-of-file marker except a single optional end-of-line marker.
	size := d.info.Size()

	eof := make([]byte, 0)
	for i := range int64(10) {
		buf := make([]byte, 1)
		d.file.ReadAt(buf, size-i)

		eof = append([]byte{buf[0]}, eof...)
		if strings.HasPrefix(string(eof), "%%EOF") {
			return nil
		}
	}
	return fmt.Errorf("no EOF found: %v", string(eof))
}

// verifyCrossReferenceTable verifies requirements outlined in 6.1.4
func (d *Document) verifyCrossReferenceTable() error {
	buf := make([]byte, 128)
	n, _ := d.file.ReadAt(buf, d.xrefOffset)

	cur := NewCursor(buf[:n])

	// The xref keyword and the cross reference subsection header shall be separated by a single EOL marker.
	xRef, ok := cur.ReadLine()
	if !ok || xRef != "xref" {
		return fmt.Errorf("expected 'xref' keyword")
	}

	xRefHeader, ok := cur.ReadLine()
	if !ok {
		return fmt.Errorf("expected xRef subsection header")
	}

	// In a cross reference subsection header the starting object number and the range shall be separated by a single SPACE character (20h).
	parts := strings.Fields(xRefHeader)
	if len(parts) != 2 {
		return fmt.Errorf("xRef subsection header should consist of two parts")
	}

	return nil
}
