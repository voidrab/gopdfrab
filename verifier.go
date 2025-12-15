package pdfrab

import (
	"fmt"
	"reflect"
	"slices"
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
	Issues map[string][]error
}

// Verify verifies d to conformance level t.
func (d *Document) Verify(t LevelType) (Result, error) {
	basicResult := Result{
		Type:   t,
		Valid:  false,
		Issues: nil,
	}

	if t == Undefined {
		return basicResult, fmt.Errorf("cannot verify PDF to undefined conformance level")
	}

	var issues = make(map[string][]error)
	if t == A1_B {
		issues = d.verifyPdfA1b()
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

func (d *Document) verifyPdfA1b() map[string][]error {
	issues := make(map[string][]error)

	errs := d.verifyFileHeader()
	if errs != nil {
		issues["6.1.2"] = errs
	}
	errs = d.verifyFileTrailer()
	if errs != nil {
		issues["6.1.3"] = errs
	}
	errs = d.verifyCrossReferenceTable()
	if errs != nil {
		issues["6.1.4"] = errs
	}
	errs = d.verifyDocumentInformationDictionary()
	if errs != nil {
		issues["6.1.5"] = errs
	}
	errs = d.verifyHexStrings()
	if errs != nil {
		issues["6.1.6"] = errs
	}
	errs = d.verifyOptionalContent()
	if errs != nil {
		issues["6.1.13"] = errs
	}
	errs = d.verifyOutputIntent()
	if errs != nil {
		issues["6.2.2"] = errs
	}
	return issues
}

// 6.1 File Structure

// verifyFileHeader verifies requirements outlined in 6.1.2.
func (d *Document) verifyFileHeader() []error {
	buf := make([]byte, 128)
	n, _ := d.file.ReadAt(buf, 0)

	cur := NewCursor(buf[:n])

	errs := []error{}

	header, ok := cur.ReadLine()
	if !ok || header[0] != '%' {
		errs = append(errs, fmt.Errorf("invalid PDF header"))
	}

	comment, ok := cur.ReadLine()
	if !ok || comment[0] != '%' {
		errs = append(errs, fmt.Errorf("header must be followed by comment"))
	}

	if len(comment) < 5 {
		errs = append(errs, fmt.Errorf("comment line must consist of at least 5 characters"))
	}

	for _, byte := range comment[1:] {
		if byte <= 127 {
			errs = append(errs, fmt.Errorf("byte value in comment line must be > 127 but was %v", byte))
		}
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}

// verifyFileTrailer verifies requirements outlined in 6.1.3
func (d *Document) verifyFileTrailer() []error {
	errs := []error{}

	// The file trailer dictionary shall contain the ID keyword.
	if d.trailer["ID"] == nil {
		errs = append(errs, fmt.Errorf("trailer does not contain the required ID keyword"))
	}

	// The keyword Encrypt shall not be used in the trailer dictionary.
	if d.trailer["Encrypt"] != nil {
		errs = append(errs, fmt.Errorf("trailer contains the forbidden Encrypt keyword"))
	}

	// No data shall follow the last end-of-file marker except a single optional end-of-line marker.
	size := d.info.Size()

	found := false
	eof := make([]byte, 0)
	for i := range int64(10) {
		buf := make([]byte, 1)
		d.file.ReadAt(buf, size-i)

		eof = append([]byte{buf[0]}, eof...)
		if strings.HasPrefix(string(eof), "%%EOF") {
			found = true
			break
		}
	}
	if !found {
		errs = append(errs, fmt.Errorf("no EOF found: %v", string(eof)))
	}
	if len(errs) > 0 {
		return errs
	}

	return nil
}

// verifyCrossReferenceTable verifies requirements outlined in 6.1.4
func (d *Document) verifyCrossReferenceTable() []error {
	buf := make([]byte, 128)
	n, _ := d.file.ReadAt(buf, d.xrefOffset)

	cur := NewCursor(buf[:n])

	errs := []error{}

	// The xref keyword and the cross reference subsection header shall be separated by a single EOL marker.
	xRef, ok := cur.ReadLine()
	if !ok || xRef != "xref" {
		errs = append(errs, fmt.Errorf("expected 'xref' keyword"))
	}

	xRefHeader, ok := cur.ReadLine()
	if !ok {
		errs = append(errs, fmt.Errorf("expected cross reference subsection header after xref keyword"))
	}

	// In a cross reference subsection header the starting object number and the range shall be separated by a single SPACE character (20h).
	parts := strings.Fields(xRefHeader)
	if len(parts) != 2 {
		errs = append(errs, fmt.Errorf("cross reference subsection header should consist of two parts"))
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}

// verifyDocumentInformationDictionary verifies requirements outlined in 6.1.5
func (d *Document) verifyDocumentInformationDictionary() []error {
	if d.trailer["Info"] == nil {
		return nil
	}

	metadata, err := d.GetMetadata()
	if err != nil {
		return []error{fmt.Errorf("failed to get document information dictionary")}
	}

	allowedFields := []string{
		"Title",
		"Author",
		"Subject",
		"Keywords",
		"Creator",
		"Producer",
		"CreationDate",
		"ModDate",
		"Trapped",
	}

	errs := []error{}

	// dictionary should only contain allowed fields that have non-empty values
	for k, v := range metadata {
		if !slices.Contains(allowedFields, k) {
			err := fmt.Errorf("disallowed key %v in information dictionary", k)
			errs = append(errs, err)
		}
		if len(v) == 0 {
			err := fmt.Errorf("empty value for key %v in information dictionary", k)
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}

// require scanning of document: 6.1.6, 6.1.7, 6.1.8, 6.1.10, 6.1.11, 6.1.12

// verifyHexStrings verifies requirements outlined in 6.1.6.
func (d *Document) verifyHexStrings() []error {
	root, err := d.ResolveGraph()
	if err != nil {
		return []error{err}
	}

	var errs []error
	visited := make(map[uintptr]bool)

	var walk func(node any)

	walk = func(node any) {
		if node == nil {
			return
		}

		// Detect cycles (important for resolved graphs)
		switch v := node.(type) {
		case PDFDict:
			ptr := pdfValuePointer(v)
			if visited[ptr] {
				return
			}
			visited[ptr] = true

			for _, val := range v {
				walk(val)
			}

		case PDFArray:
			ptr := pdfValuePointer(v)
			if visited[ptr] {
				return
			}
			visited[ptr] = true

			for _, item := range v {
				walk(item)
			}

		case PDFHexString:
			if err := validateHexString(v.Value); err != nil {
				errs = append(errs, err)
			}
		}
	}

	walk(root)

	if len(errs) > 0 {
		return errs
	}
	return nil
}

func pdfValuePointer(v PDFValue) uintptr {
	return reflect.ValueOf(v).Pointer()
}

func validateHexString(hex string) error {
	// Hexadecimal strings shall contain an even number of non-white-space characters, each in the range 0 to 9, A to F or a to f.

	hexCount := 0

	for i := 0; i < len(hex); i++ {
		ch := hex[i]

		if isWhitespace(ch) {
			continue
		}

		if !isHexDigit(ch) {
			return fmt.Errorf("contains non-hex character: '%v'", ch)
		}

		hexCount++
	}

	if hexCount%2 != 0 {
		return fmt.Errorf("contains an odd number of hex digits (%d)", hexCount)
	}

	return nil
}

// verifyOptionalContent verifies requirements outlined in 6.1.13
func (d *Document) verifyOptionalContent() []error {
	_, err := d.ResolveGraphByPath([]string{"Root", "OCProperties"})
	if err == nil {
		return []error{fmt.Errorf("OCProperties are not allowed in document catalog")}
	}
	return nil
}

// 6.2 Graphics

// verifyOutputIntent verifies requirements outlined in 6.2.2
func (d *Document) verifyOutputIntent() []error {
	values, err := d.ResolveGraphByPath([]string{"Root", "OutputIntents"})
	if err != nil {
		// OutputIntents are optional
		//return []error{fmt.Errorf("failed to read OutputIntents: %v", err)}
		return nil
	}

	intents, ok := values.(PDFArray)
	if !ok {
		return []error{fmt.Errorf("OutputIntents object is not an array")}
	}

	errs := []error{}

	var indirectObject any

	for _, v := range intents {
		intent, ok := v.(PDFDict)
		if !ok {
			errs = append(errs, fmt.Errorf("expected OutputIntent to be a PDFDict"))
			continue
		}
		// optional
		// if intent["Type"] != "OutputIntent" {
		// 	errs = append(errs, fmt.Errorf("expected Type was not OutputIntent, but %v", intent["Type"]))
		// }

		s, ok := intent["S"].(PDFName)
		if !ok {
			errs = append(errs, fmt.Errorf("expected S to be a PDFName"))
			continue
		}

		if s.Value != "GTS_PDFA1" {
			errs = append(errs, fmt.Errorf("expected S was not GTS_PDFA1, but %v", intent["S"]))
		}

		if intent["OutputConditionIdentifier"] == nil {
			errs = append(errs, fmt.Errorf("OutputConditionIdentifier is required but was nil"))
			continue
		}

		destOutputProfile := intent["DestOutputProfile"]
		if destOutputProfile == nil {
			// optional?
			//errs = append(errs, fmt.Errorf("DestOutputProfile is required but was nil"))
			continue
		}

		// If a file's OutputIntents array contains more than one entry, then all entries that contain a DestOutputProfile
		// key shall have as the value of that key the same indirect object, which shall be a valid ICC profile stream.
		if indirectObject == nil {
			indirectObject = destOutputProfile
		} else {
			if indirectObject != destOutputProfile {
				errs = append(errs, fmt.Errorf("expected DestOutputProfile to be %v but was %v", indirectObject, destOutputProfile))
				continue
			}
		}

		profile, err := d.resolveObject(destOutputProfile)
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to resolve DestOutputProfile: %v", err))
			continue
		}

		profileMap, ok := profile.(PDFDict)
		if !ok {
			errs = append(errs, fmt.Errorf("unexpected format for DestOutputProfile encountered"))
			continue
		}

		nValue, ok := profileMap["N"].(PDFInteger)
		if !ok {
			errs = append(errs, fmt.Errorf("could not retrieve number of colour components N"))
		}

		// N shall be 1, 3, or 4
		if !slices.Contains([]int{1, 3, 4}, int(nValue)) {
			errs = append(errs, fmt.Errorf("number of colour components N must be 1, 3, or 4"))
		}
	}

	// TODO check if ICC profile stream is valid

	if len(errs) > 0 {
		return errs
	}

	return nil
}

// verifyGeneralColourSpaces verifies requirements outlined in 6.2.3.1
func (d *Document) verifyGeneralColourSpaces() []error {
	// TODO check if document has OutputIntent or direct use of device-independent colour space
	return nil
}
