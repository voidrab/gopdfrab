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
	Issues []PDFError
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

	var issues = []PDFError{}
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

func (d *Document) verifyPdfA1b() []PDFError {
	issues := []PDFError{}

	errs := d.verifyFileHeader()
	if errs != nil {
		issues = append(issues, errs...)
	}
	errs = d.verifyFileTrailer()
	if errs != nil {
		issues = append(issues, errs...)
	}
	errs = d.verifyCrossReferenceTable()
	if errs != nil {
		issues = append(issues, errs...)
	}
	errs = d.verifyDocumentInformationDictionary()
	if errs != nil {
		issues = append(issues, errs...)
	}

	graph, err := d.ResolveGraph()
	if err != nil {
		return []PDFError{{
			clause:    "6.1.6",
			subclause: 0,
			errs:      []error{err},
			page:      0,
		}}
	}

	pageIndex, err := d.buildPageIndex(graph)
	if err != nil {
		return []PDFError{{
			clause:    "6.1.6",
			subclause: 0,
			errs:      []error{err},
			page:      0,
		}}
	}

	ctx := &ValidationContext{
		PageIndex: pageIndex,
	}

	errs = d.verifyDocument(graph, ctx)
	if errs != nil {
		issues = append(issues, errs...)
	}
	errs = d.verifyIndirectObjects()
	if errs != nil {
		issues = append(issues, errs...)
	}
	errs = d.verifyOptionalContent()
	if errs != nil {
		issues = append(issues, errs...)
	}
	errs = d.verifyOutputIntent()
	if errs != nil {
		issues = append(issues, errs...)
	}
	return issues
}

// 6.1 File Structure

// verifyFileHeader verifies requirements outlined in 6.1.2.
func (d *Document) verifyFileHeader() []PDFError {
	buf := make([]byte, 128)
	n, _ := d.file.ReadAt(buf, 0)

	cur := NewCursor(buf[:n])

	errs := []PDFError{}

	header, ok := cur.ReadLine()
	if !ok || len(header) == 0 || header[0] != '%' {
		err := PDFError{
			clause:    "6.1.2",
			subclause: 1,
			errs:      []error{fmt.Errorf("invalid PDF header: %v", header)},
			page:      1,
		}
		errs = append(errs, err)
	}

	comment, ok := cur.ReadLine()
	if !ok || len(comment) == 0 || comment[0] != '%' {
		err := PDFError{
			clause:    "6.1.2",
			subclause: 2,
			errs:      []error{fmt.Errorf("header must be followed by comment, but was: %v", comment)},
			page:      1,
		}
		errs = append(errs, err)
		return errs
	}

	s := len(comment)

	if s < 5 {
		err := PDFError{
			clause:    "6.1.2",
			subclause: 3,
			errs:      []error{fmt.Errorf("comment line must consist of at least 5 characters, but was: %v", s)},
			page:      1,
		}
		errs = append(errs, err)
	}

	subErrs := []error{}
	for _, byte := range comment[1:] {
		if byte <= 127 {
			err := fmt.Errorf("byte value in comment line must be > 127 but was %v", byte)
			subErrs = append(subErrs, err)
		}
	}

	if len(subErrs) > 0 {
		err := PDFError{clause: "6.1.2", subclause: 4, errs: subErrs, page: 1}
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}

// verifyFileTrailer verifies requirements outlined in 6.1.3
func (d *Document) verifyFileTrailer() []PDFError {
	errs := []PDFError{}

	// The file trailer dictionary shall contain the ID keyword.
	if d.trailer["ID"] == nil {
		err := PDFError{
			clause:    "6.1.3",
			subclause: 1,
			errs:      []error{fmt.Errorf("trailer does not contain the required ID keyword")},
			page:      0,
		}
		errs = append(errs, err)
	}

	// The keyword Encrypt shall not be used in the trailer dictionary.
	if d.trailer["Encrypt"] != nil {
		err := PDFError{
			clause:    "6.1.3",
			subclause: 2,
			errs:      []error{fmt.Errorf("trailer contains the forbidden Encrypt keyword")},
			page:      0,
		}
		errs = append(errs, err)
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
		err := PDFError{
			clause:    "6.1.3",
			subclause: 3,
			errs:      []error{fmt.Errorf("no EOF marker found: %v", string(eof))},
			page:      0,
		}
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return errs
	}

	return nil
}

// verifyCrossReferenceTable verifies requirements outlined in 6.1.4
func (d *Document) verifyCrossReferenceTable() []PDFError {
	buf := make([]byte, 128)
	n, _ := d.file.ReadAt(buf, d.xrefOffset)

	cur := NewCursor(buf[:n])

	errs := []PDFError{}

	// The xref keyword and the cross reference subsection header shall be separated by a single EOL marker.
	xRef, ok := cur.ReadLine()
	if !ok || len(xRef) == 0 || xRef != "xref" {
		err := PDFError{
			clause:    "6.1.4",
			subclause: 1,
			errs:      []error{fmt.Errorf("expected 'xref' keyword")},
			page:      0,
		}
		errs = append(errs, err)
	}

	xRefHeader, ok := cur.ReadLine()
	if !ok || len(xRefHeader) == 0 {
		err := PDFError{
			clause:    "6.1.4",
			subclause: 2,
			errs:      []error{fmt.Errorf("expected cross reference subsection header after xref keyword")},
			page:      0,
		}
		errs = append(errs, err)
		return errs
	}

	// In a cross reference subsection header the starting object number and the range shall be separated
	// by a single SPACE character (20h).
	parts := strings.Fields(xRefHeader)
	if len(parts) != 2 {
		err := PDFError{
			clause:    "6.1.4",
			subclause: 3,
			errs:      []error{fmt.Errorf("cross reference subsection header should consist of two parts")},
			page:      0,
		}
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}

// verifyDocumentInformationDictionary verifies requirements outlined in 6.1.5
func (d *Document) verifyDocumentInformationDictionary() []PDFError {
	if d.trailer["Info"] == nil {
		return nil
	}

	metadata, err := d.GetMetadata()
	if err != nil {
		return []PDFError{{
			clause:    "6.1.5",
			subclause: 1,
			errs:      []error{fmt.Errorf("failed to get document information dictionary: %v", err)},
			page:      0,
		}}
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

	errs := []PDFError{}

	// dictionary should only contain allowed fields that have non-empty values
	disallowedErrs := []error{}
	emptyErrs := []error{}
	for k, v := range metadata {
		if !slices.Contains(allowedFields, k) {
			err := fmt.Errorf("disallowed key %v in information dictionary", k)
			disallowedErrs = append(disallowedErrs, err)
		}
		if len(v) == 0 {
			err := fmt.Errorf("empty value for key %v in information dictionary", k)
			emptyErrs = append(emptyErrs, err)
		}
	}

	if len(disallowedErrs) > 0 {
		err := PDFError{clause: "6.1.5", subclause: 2, errs: disallowedErrs, page: 0}
		errs = append(errs, err)
	}

	if len(emptyErrs) > 0 {
		err := PDFError{clause: "6.1.5", subclause: 3, errs: emptyErrs, page: 0}
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}

// require scanning of document: 6.1.6, 6.1.7, 6.1.8, 6.1.10, 6.1.11, 6.1.12

// verifyDocument verifies requirements outlined in 6.1.6, 6.1.7.
func (d *Document) verifyDocument(graph PDFValue, ctx *ValidationContext) []PDFError {
	var errs []PDFError
	visited := make(map[uintptr]bool)

	var walk func(node any)

	walk = func(node any) {
		if node == nil {
			return
		}

		switch v := node.(type) {
		case PDFDict:
			ptr := pdfValuePointer(v)
			if visited[ptr] {
				return
			}
			visited[ptr] = true

			if (v["Type"] == PDFName{Value: "Page"}) {
				if ref, ok := v["_ref"].(PDFRef); ok {
					ctx.CurrentPage = ctx.PageIndex[ref.ObjNum]
				}
			}

			// A stream object dictionary shall not contain the F, FFilter, or FDecodeParams keys.
			streamErrs := validateStreamObject(v, ctx)
			if streamErrs != nil {
				errs = append(errs, streamErrs...)
			}

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
			// Hexadecimal strings shall contain an even number of non-white-space characters,
			// each in the range 0 to 9, A to F or a to f.
			hexErrs := validateHexString(v, ctx)
			if hexErrs != nil {
				errs = append(errs, hexErrs...)
			}
		}
	}

	walk(graph)

	if len(errs) > 0 {
		return errs
	}
	return nil
}

func pdfValuePointer(v PDFValue) uintptr {
	return reflect.ValueOf(v).Pointer()
}

// validateHexStrings validates requirements outlined in 6.1.6.
func validateHexString(v PDFHexString, ctx *ValidationContext) []PDFError {
	hexCount := 0

	hexErrs := []error{}

	hex := v.Value
	for i := 0; i < len(hex); i++ {
		ch := hex[i]

		if isWhitespace(ch) {
			continue
		}

		if !isHexDigit(ch) {
			err := fmt.Errorf("contains non-hex character: '%v'", ch)
			hexErrs = append(hexErrs, err)
		}

		hexCount++
	}

	errs := []PDFError{}

	if len(hexErrs) > 0 {
		err := newErrors(ctx, v, "6.1.6", 1, hexErrs)
		errs = append(errs, err)
	}

	if hexCount%2 != 0 {
		err := newError(ctx, v, "6.1.6", 2, fmt.Sprintf("contains an odd number of hex chars (%d)", hexCount))
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}

// validateStreamObject validates requirements outlined in 6.1.7.
func validateStreamObject(v PDFDict, ctx *ValidationContext) []PDFError {
	errs := []PDFError{}

	if v["F"] != nil {
		err := newError(ctx, v, "6.1.7", 1, "stream object contains invalid key F")
		errs = append(errs, err)
	}
	if v["FFilter"] != nil {
		err := newError(ctx, v, "6.1.7", 2, "stream object contains invalid key FFilter")
		errs = append(errs, err)
	}
	if v["FDecodeParams"] != nil {
		err := newError(ctx, v, "6.1.7", 3, "stream object contains invalid key FDecodeParams")
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

// verifyIndirectObjects verifies requirements outlined in 6.1.8
func (d *Document) verifyIndirectObjects() []PDFError {

	return nil
}

// verifyOptionalContent verifies requirements outlined in 6.1.13
func (d *Document) verifyOptionalContent() []PDFError {
	_, err := d.ResolveGraphByPath([]string{"Root", "OCProperties"})
	if err == nil {
		return []PDFError{{
			clause:    "6.1.13",
			subclause: 1,
			errs:      []error{fmt.Errorf("OCProperties not allowed in document catalog")},
			page:      0,
		}}
	}
	return nil
}

// 6.2 Graphics

// verifyOutputIntent verifies requirements outlined in 6.2.2
func (d *Document) verifyOutputIntent() []PDFError {
	values, err := d.ResolveGraphByPath([]string{"Root", "OutputIntents"})
	if err != nil || values == nil {
		// OutputIntents are optional
		//return []error{fmt.Errorf("failed to read OutputIntents: %v", err)}
		return nil
	}

	intents, ok := values.(PDFArray)
	if !ok {
		return []PDFError{{
			clause:    "6.2.2",
			subclause: 1,
			errs:      []error{fmt.Errorf("OutputIntents object is not an array")},
			page:      0,
		}}
	}

	errs := []PDFError{}

	var indirectObject PDFValue

	for _, v := range intents {
		intent, ok := v.(PDFDict)
		if !ok {
			err := PDFError{
				clause:    "6.2.2",
				subclause: 2,
				errs:      []error{fmt.Errorf("expected OutputIntent to be a PDFDict")},
				page:      0,
			}
			errs = append(errs, err)
			continue
		}
		// optional
		// if intent["Type"] != "OutputIntent" {
		// 	errs = append(errs, fmt.Errorf("expected Type was not OutputIntent, but %v", intent["Type"]))
		// }

		s, ok := intent["S"].(PDFName)
		if !ok {
			err := PDFError{
				clause:    "6.2.2",
				subclause: 3,
				errs:      []error{fmt.Errorf("expected S to be a PDFName")},
				page:      0,
			}
			errs = append(errs, err)
			continue
		}

		if s.Value != "GTS_PDFA1" {
			err := PDFError{
				clause:    "6.2.2",
				subclause: 4,
				errs:      []error{fmt.Errorf("expected S was not GTS_PDFA1, but %v", intent["S"])},
				page:      0,
			}
			errs = append(errs, err)
		}

		if intent["OutputConditionIdentifier"] == nil {
			err := PDFError{
				clause:    "6.2.2",
				subclause: 5,
				errs:      []error{fmt.Errorf("OutputConditionIdentifier is required but was nil")},
				page:      0,
			}
			errs = append(errs, err)
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
			if !EqualPDFValue(indirectObject, destOutputProfile) {
				err := PDFError{
					clause:    "6.2.2",
					subclause: 6,
					errs:      []error{fmt.Errorf("expected DestOutputProfile to be %v but was %v", indirectObject, destOutputProfile)},
					page:      0,
				}
				errs = append(errs, err)
				continue
			}
		}

		profile, err := d.resolveObject(destOutputProfile)
		if err != nil {
			err := PDFError{
				clause:    "6.2.2",
				subclause: 7,
				errs:      []error{fmt.Errorf("unable to resolve DestOutputProfile: %v", err)},
				page:      0,
			}
			errs = append(errs, err)
			continue
		}

		profileMap, ok := profile.(PDFStreamDict)
		if !ok {
			err := PDFError{
				clause:    "6.2.2",
				subclause: 8,
				errs:      []error{fmt.Errorf("unexpected format for DestOutputProfile encountered")},
				page:      0,
			}
			errs = append(errs, err)
			continue
		}

		nValue, ok := profileMap["N"].(PDFInteger)
		if !ok {
			err := PDFError{
				clause:    "6.2.2",
				subclause: 9,
				errs:      []error{fmt.Errorf("could not retrieve number of colour components N")},
				page:      0,
			}
			errs = append(errs, err)
			continue
		}

		// N shall be 1, 3, or 4
		if !slices.Contains([]int{1, 3, 4}, int(nValue)) {
			err := PDFError{
				clause:    "6.2.2",
				subclause: 10,
				errs:      []error{fmt.Errorf("number of colour components N must be 1, 3, or 4")},
				page:      0,
			}
			errs = append(errs, err)
		}
	}

	// TODO check if ICC profile stream is valid

	if len(errs) > 0 {
		return errs
	}

	return nil
}

// verifyGeneralColourSpaces verifies requirements outlined in 6.2.3.1
func (d *Document) verifyGeneralColourSpaces() []PDFError {
	// TODO check if document has OutputIntent or direct use of device-independent colour space
	return nil
}
