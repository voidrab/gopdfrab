package pdfrab

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// xrefHeaderRe matches a well-formed cross-reference subsection header
// ("start count" separated by a single space, no leading white space).
var xrefHeaderRe = regexp.MustCompile(`^[0-9]+ [0-9]+$`)

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
	switch t {
	case A1_B:
		return d.VerifyProfile(PDFA_1B)
	default:
		return Result{Type: t, Valid: false}, fmt.Errorf("cannot verify PDF to undefined conformance level")
	}
}

// VerifyProfile verifies d against the checks enabled in profile p.
func (d *Document) VerifyProfile(p *Profile) (Result, error) {
	if p == nil {
		return Result{}, fmt.Errorf("nil profile")
	}
	if p.Level == Undefined {
		return Result{Type: p.Level, Valid: false}, fmt.Errorf("cannot verify PDF to undefined conformance level")
	}

	var issues []PDFError
	if p.Level == A1_B {
		issues = d.verifyPdfA1b()
	}
	issues = filterByProfile(issues, p)

	if len(issues) > 0 {
		return Result{Type: p.Level, Valid: false, Issues: issues}, nil
	}
	return Result{Type: p.Level, Valid: true}, nil
}

// filterByProfile removes from issues any PDFError whose (clause, subclause)
// pair is registered in the catalog but disabled in p.
func filterByProfile(issues []PDFError, p *Profile) []PDFError {
	out := make([]PDFError, 0, len(issues))
	for _, e := range issues {
		if p.allows(e.clause, e.subclause) {
			out = append(out, e)
		}
	}
	return out
}

// PDF/A-1b (ISO 19005-1:2005)

func (d *Document) verifyPdfA1b() []PDFError {
	issues := []PDFError{}

	errs := d.verifyFileHeader()
	if errs != nil {
		issues = append(issues, errs...)
	}
	errs = d.checkLinearizedFileID()
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
		return append(issues, PDFError{
			clause:    "6.1.6",
			subclause: 0,
			errs:      []error{err},
			page:      0,
		})
	}

	pageIndex, err := d.buildPageIndex(graph)
	if err != nil {
		return append(issues, PDFError{
			clause:    "6.1.6",
			subclause: 0,
			errs:      []error{err},
			page:      0,
		})
	}

	ctx := &ValidationContext{
		PageIndex: pageIndex,
	}
	d.computeColourCoverage(ctx)

	d.verifyDocument(graph, ctx)
	errs = ctx.errs
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
	errs = d.verifyInteractiveForms()
	if errs != nil {
		issues = append(issues, errs...)
	}
	errs = d.verifyXMPMetadata()
	if errs != nil {
		issues = append(issues, errs...)
	}

	// Object-framing violations (6.1.8) collected lazily during resolution.
	if len(d.structErrs) > 0 {
		issues = append(issues, d.structErrs...)
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
	if d.trailer.Entries["ID"] == nil {
		err := PDFError{
			clause:    "6.1.3",
			subclause: 1,
			errs:      []error{fmt.Errorf("trailer does not contain the required ID keyword")},
			page:      0,
		}
		errs = append(errs, err)
	}

	// The keyword Encrypt shall not be used in the trailer dictionary.
	if d.trailer.Entries["Encrypt"] != nil {
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
	// by a single SPACE character (20h), with no leading white space.
	if !xrefHeaderRe.MatchString(xRefHeader) {
		err := PDFError{
			clause:    "6.1.4",
			subclause: 3,
			errs:      []error{fmt.Errorf("malformed cross reference subsection header: %q", xRefHeader)},
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
	if d.trailer.Entries["Info"] == nil {
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

// verifyDocument verifies requirements outlined in 6.1.6, 6.1.7, 6.1.11, 6.1.12.
func (d *Document) verifyDocument(graph PDFValue, ctx *ValidationContext) {
	visited := make(map[uintptr]bool)

	var walk func(node any)

	walk = func(node any) {
		if node == nil {
			return
		}

		switch v := node.(type) {
		case PDFDict:
			ptr := pdfValuePointer(v.Entries)
			if visited[ptr] {
				return
			}
			visited[ptr] = true

			if (v.Entries["Type"] == PDFName{Value: "Page"}) {
				if ref, ok := v.Entries["_ref"].(PDFRef); ok {
					ctx.CurrentPage = ctx.PageIndex[ref.ObjNum]
				}
			}

			if v.HasStream {
				validateStreamObject(v, ctx)
			}

			validateObject(v, ctx)
			validateActions(v, ctx)
			validateAdditionalActions(v, ctx)
			validateExtGState(v, ctx)
			validateTransparencyGroup(v, ctx)
			validateXObjectDict(v, ctx)
			validateAnnotation(v, ctx)
			validateFormField(v, ctx)
			validateColourSpaceUsage(v, ctx)
			validateContentStreams(v, ctx)
			validateFontDict(v, ctx)

			for k, val := range v.Entries {
				// 6.1.12: a name used as a dictionary key is also subject to the
				// 127-byte limit.
				if k != "_ref" && len(k) > 127 {
					ctx.ReportError(v, "6.1.12", 1, fmt.Sprintf("dictionary key exceeds 127 bytes: %d", len(k)))
				}
				walk(val)
			}

		case PDFArray:
			ptr := pdfValuePointer(v)
			if visited[ptr] {
				return
			}
			visited[ptr] = true

			validateColourSpaceArray(v, ctx)

			// 6.1.12: maximum number of elements in an array is 8191.
			if len(v) > 8191 {
				ctx.ReportError(v, "6.1.12", 3, fmt.Sprintf("array exceeds 8191 elements: %d", len(v)))
			}

			for _, item := range v {
				walk(item)
			}

		case PDFHexString:
			// Hexadecimal strings shall contain an even number of non-white-space characters,
			// each in the range 0 to 9, A to F or a to f.
			validateHexString(v, ctx)
		}

		validateArchitecturalLimits(node, ctx)
	}

	walk(graph)
}

func pdfValuePointer(v PDFValue) uintptr {
	return reflect.ValueOf(v).Pointer()
}

// validateHexStrings validates requirements outlined in 6.1.6.
func validateHexString(v PDFHexString, ctx *ValidationContext) {
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

	if len(hexErrs) > 0 {
		ctx.ReportErrors(v, "6.1.6", 1, hexErrs)
	}

	if hexCount%2 != 0 {
		ctx.ReportError(v, "6.1.6", 2, fmt.Sprintf("contains an odd number of hex chars (%d)", hexCount))
	}
}

// validateStreamObject validates requirements outlined in 6.1.7 and 6.1.10.
func validateStreamObject(v PDFDict, ctx *ValidationContext) {
	if v.Entries["F"] != nil {
		ctx.ReportError(v, "6.1.7", 1, "stream object contains invalid key F")
	}
	if v.Entries["FFilter"] != nil {
		ctx.ReportError(v, "6.1.7", 2, "stream object contains invalid key FFilter")
	}
	if v.Entries["FDecodeParms"] != nil {
		ctx.ReportError(v, "6.1.7", 3, "stream object contains invalid key FDecodeParms")
	}
	for _, f := range filterNames(v.Entries["Filter"]) {
		if f == "LZWDecode" || f == "LZW" {
			ctx.ReportError(v, "6.1.10", 1, "stream object uses forbidden LZWDecode filter")
		}
	}
}

// validateObject validates requirements outlined in 6.1.11
func validateObject(v PDFDict, ctx *ValidationContext) {
	if v.Entries["EF"] != nil {
		ctx.ReportError(v, "6.1.11", 1, "dictionary shall not contain EF key")
	}
	if v.Entries["EmbeddedFiles"] != nil {
		ctx.ReportError(v, "6.1.11", 2, "dictionary shall not contain EmbeddedFiles key")
	}
}

// validateArchitecturalLimits validates requirements outlined in 6.1.12
func validateArchitecturalLimits(node PDFValue, ctx *ValidationContext) {
	switch v := node.(type) {
	case PDFName:
		// Maximum length of a name, in bytes: 127
		nameLen := len(v.Value)
		if nameLen > 127 {
			ctx.ReportError(v, "6.1.12", 1, fmt.Sprintf("maximum length of name (127) exceeded: %v", nameLen))
		}
	case PDFInteger:
		// Largest integer value; equal to 231 − 1
		// Smallest integer value; equal to −231
		if v < -2_147_483_648 || v > 2_147_483_647 {
			ctx.ReportError(v, "6.1.12", 2, fmt.Sprintf("integer value exceeded limits: %v", v))
		}
		// TODO Maximum number of colorants or tint components in a DeviceN colour space: 32
		// TODO Maximum value of a CID (character identifier): 65535

		// wont implement: real, string (in content stream), indirect object, q/Q nesting
	}
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

		s, ok := intent.Entries["S"].(PDFName)
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
				errs:      []error{fmt.Errorf("expected S was not GTS_PDFA1, but %v", intent.Entries["S"])},
				page:      0,
			}
			errs = append(errs, err)
		}

		if intent.Entries["OutputConditionIdentifier"] == nil {
			err := PDFError{
				clause:    "6.2.2",
				subclause: 5,
				errs:      []error{fmt.Errorf("OutputConditionIdentifier is required but was nil")},
				page:      0,
			}
			errs = append(errs, err)
			continue
		}

		destOutputProfile := intent.Entries["DestOutputProfile"]
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

		profileMap, ok := profile.(PDFDict)
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

		nValue, ok := profileMap.Entries["N"].(PDFInteger)
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

		// 6.2.2: the ICC profile stream shall be a valid ICC.1:2003-09 profile (version ≤ 2.x).
		if profileMap.HasStream {
			if iccErr := validateICCProfileStream(profileMap); iccErr != nil {
				errs = append(errs, PDFError{
					clause:    "6.2.2",
					subclause: 11,
					errs:      []error{iccErr},
					page:      0,
				})
			}
		}
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}

// validateICCProfileStream checks that a DestOutputProfile stream is a valid
// ICC profile version 2.x as required by PDF/A-1 (6.2.2 / ICC.1:2003-09).
func validateICCProfileStream(dict PDFDict) error {
	data, err := decodeStream(dict)
	if err != nil {
		return fmt.Errorf("cannot decode ICC profile stream: %v", err)
	}
	if len(data) < 128 {
		return fmt.Errorf("ICC profile too short (%d bytes)", len(data))
	}
	if string(data[36:40]) != "acsp" {
		return fmt.Errorf("ICC profile missing 'acsp' signature at offset 36")
	}
	major := data[8]
	if major > 2 {
		return fmt.Errorf("ICC profile version %d.x not allowed in PDF/A-1 (must be ≤ 2.x)", major)
	}
	return nil
}

// verifyGeneralColourSpaces verifies requirements outlined in 6.2.3.1
func (d *Document) verifyGeneralColourSpaces() []PDFError {
	// TODO check if document has OutputIntent or direct use of device-independent colour space
	return nil
}

// trailerIDRe finds the first hex string in any /ID array in the file.
var trailerIDRe = regexp.MustCompile(`/ID\s*\[<([0-9A-Fa-f]+)>`)

// checkLinearizedFileID detects the 6.1.3 violation where a linearized PDF has
// different ID[0] values in its first-page and final trailers (ISO 19005-1:2005 §6.1.3).
func (d *Document) checkLinearizedFileID() []PDFError {
	size := d.info.Size()
	raw := make([]byte, size)
	if _, err := d.file.ReadAt(raw, 0); err != nil {
		return nil
	}
	matches := trailerIDRe.FindAllSubmatch(raw, -1)
	if len(matches) < 2 {
		return nil
	}
	first := strings.ToLower(string(matches[0][1]))
	for _, m := range matches[1:] {
		id := strings.ToLower(string(m[1]))
		if id != first {
			return []PDFError{{
				clause:    "6.1.3",
				subclause: 1,
				errs:      []error{fmt.Errorf("linearized PDF: ID[0] (%s) differs from %s in another trailer", first, id)},
				page:      0,
			}}
		}
	}
	return nil
}
