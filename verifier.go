package pdfrab

import (
	"fmt"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
)

// xrefHeaderRe matches a well-formed cross-reference subsection header
// ("start count" separated by a single space, no leading white space).
var xrefHeaderRe = regexp.MustCompile(`^[0-9]+ [0-9]+$`)

type LevelType string

const (
	Undefined LevelType = "undefined"
	A_1B      LevelType = "A-1b"
)

type Result struct {
	Type   LevelType
	Valid  bool
	Issues []PDFError
}

// Verify verifies d to conformance level t.
func (d *Document) Verify(t LevelType) (Result, error) {
	switch t {
	case A_1B:
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
	if p.Level == A_1B {
		issues = d.verifyPdfA1b(p)
	}
	issues = filterByProfile(issues, p)

	if len(issues) > 0 {
		return Result{Type: p.Level, Valid: false, Issues: issues}, nil
	}
	return Result{Type: p.Level, Valid: true}, nil
}

type FileResult struct {
	Path   string
	Result Result
	Err    error
}

// VerifyAll opens and verifies multiple PDF files concurrently against
// conformance level t.
func VerifyAll(paths []string, t LevelType) []FileResult {
	results := make([]FileResult, len(paths))

	workers := min(runtime.NumCPU(), len(paths))
	if workers < 1 {
		return results
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = verifyFile(paths[i], t)
			}
		}()
	}
	for i := range paths {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	return results
}

// verifyFile opens, verifies, and closes a single file.
func verifyFile(path string, t LevelType) FileResult {
	doc, err := Open(path)
	if err != nil {
		return FileResult{Path: path, Err: err}
	}
	defer doc.Close()

	res, err := doc.Verify(t)
	return FileResult{Path: path, Result: res, Err: err}
}

// filterByProfile removes from issues any PDFError whose (clause, subclause)
// pair is registered in the catalog but disabled in p.
func filterByProfile(issues []PDFError, p *Profile) []PDFError {
	out := make([]PDFError, 0, len(issues))
	for _, e := range issues {
		if p.allows(e.check.clause, e.check.subclause) {
			out = append(out, e)
		}
	}
	return out
}

// PDF/A-1b (ISO 19005-1:2005)

func (d *Document) verifyPdfA1b(p *Profile) []PDFError {
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
			check: Checks.Structure.GraphResolutionFailure,
			errs:  []error{err},
			page:  0,
		})
	}

	pageIndex, err := d.buildPageIndex(graph)
	if err != nil {
		return append(issues, PDFError{
			check: Checks.Structure.GraphResolutionFailure,
			errs:  []error{err},
			page:  0,
		})
	}

	ctx := &ValidationContext{
		PageIndex: pageIndex,
	}
	reachable, invisibleOnly, usedCodes, usedCIDs := computeContentUsage(graph, ctx)
	if p.SkipUnreachableXObjects {
		ctx.ReachableXObjectPtrs = reachable
	}
	ctx.InvisibleOnlyFontPtrs, ctx.UsedCharCodes, ctx.UsedCIDs = invisibleOnly, usedCodes, usedCIDs
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

	d.verifyAllObjectFraming()

	if len(d.structErrs) > 0 {
		issues = append(issues, d.structErrs...)
	}
	return issues
}

func (d *Document) verifyAllObjectFraming() {
	for objNum := range d.xrefTable {
		d.resolveReference(PDFRef{ObjNum: objNum})
	}
}

// 6.1 File Structure

// pdfVersionRe matches a valid %PDF-N.M header line.
var pdfVersionRe = regexp.MustCompile(`^%PDF-\d\.\d$`)

// verifyFileHeader verifies requirements outlined in 6.1.2.
func (d *Document) verifyFileHeader() []PDFError {
	buf := make([]byte, 128)
	n, _ := d.file.ReadAt(buf, 0)

	cur := NewCursor(buf[:n])

	errs := []PDFError{}

	header, ok := cur.ReadLine()
	if !ok || !pdfVersionRe.MatchString(header) {
		errs = append(errs, PDFError{
			check: Checks.Structure.FileHeaderSignature,
			errs:  []error{fmt.Errorf("invalid PDF header: %q (must be %%PDF-N.M)", header)},
			page:  1,
		})
	}

	comment, ok := cur.ReadLine()
	if !ok || len(comment) == 0 || comment[0] != '%' {
		errs = append(errs, PDFError{
			check: Checks.Structure.FileHeaderComment,
			errs:  []error{fmt.Errorf("header must be followed by comment, but was: %v", comment)},
			page:  1,
		})
		return errs
	}

	// 6.1.2/3: the comment line (including the leading %) must be at least
	// 5 characters long, i.e. at least 4 bytes must follow the %.
	commentBytes := []byte(comment[1:])
	if len(commentBytes) < 4 {
		errs = append(errs, PDFError{
			check: Checks.Structure.FileHeaderCommentLength,
			errs:  []error{fmt.Errorf("comment line must consist of at least 5 characters, but was: %d", len(comment))},
			page:  1,
		})
	} else {
		// 6.1.2/4: each of the first four bytes after % must be > 127 (binary
		// indicator); bytes beyond that are unconstrained.
		var badBytes []error
		for _, b := range commentBytes[:4] {
			if b <= 127 {
				badBytes = append(badBytes, fmt.Errorf("comment line contains ASCII character (0x%02x); all bytes must be > 127", b))
			}
		}
		if len(badBytes) > 0 {
			errs = append(errs, PDFError{
				check: Checks.Structure.FileHeaderCommentBytes,
				errs:  badBytes,
				page:  1,
			})
		}
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}

// verifyFileTrailer verifies requirements outlined in 6.1.3
func (d *Document) verifyFileTrailer() []PDFError {
	errs := []PDFError{}

	// Use the effective trailer: for linearized PDFs with a minimal overflow
	// trailer (no /Root), this is the first-page trailer that holds /ID, /Root, etc.
	eff := d.effectiveTrailer()

	if eff.Entries["ID"] == nil {
		err := PDFError{
			check: Checks.Structure.TrailerID,
			errs:  []error{fmt.Errorf("trailer does not contain the required ID keyword")},
			page:  0,
		}
		errs = append(errs, err)
	}

	if eff.Entries["Encrypt"] != nil {
		err := PDFError{
			check: Checks.Structure.TrailerEncrypt,
			errs:  []error{fmt.Errorf("trailer contains the forbidden Encrypt keyword")},
			page:  0,
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
			check: Checks.Structure.TrailerEOF,
			errs:  []error{fmt.Errorf("no EOF marker found: %v", string(eof))},
			page:  0,
		}
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return errs
	}

	return nil
}

// verifyCrossReferenceTable verifies requirements outlined in 6.1.4 for the
// current xref section and all prior sections linked via Prev.
func (d *Document) verifyCrossReferenceTable() []PDFError {
	if errs := d.checkXRefSectionFormat(d.xrefOffset); len(errs) > 0 {
		return errs
	}

	visited := map[int64]bool{d.xrefOffset: true}
	prev := d.trailer.Entries["Prev"]
	for {
		prevInt, ok := prev.(PDFInteger)
		if !ok {
			break
		}
		prevOffset := int64(prevInt)
		if visited[prevOffset] {
			break
		}
		visited[prevOffset] = true

		if errs := d.checkXRefSectionFormat(prevOffset); len(errs) > 0 {
			return errs
		}

		prevTrailer, err := d.parseXRefSectionAt(prevOffset+d.pdfStart, false)
		if err != nil {
			break
		}
		prev = prevTrailer.Entries["Prev"]
	}

	return nil
}

// checkXRefSectionFormat reads the xref section at the given file offset and
// validates the xref keyword, all subsection headers, and their format per 6.1.4.
func (d *Document) checkXRefSectionFormat(offset int64) []PDFError {
	buf := make([]byte, 8192)
	n, _ := d.file.ReadAt(buf, offset+d.pdfStart)
	cur := NewCursor(buf[:n])

	// The xref keyword and the cross reference subsection header shall be separated by a single EOL marker.
	xRef, ok := cur.ReadLine()
	if !ok || len(xRef) == 0 || xRef != "xref" {
		return []PDFError{{
			check: Checks.Structure.XRefKeyword,
			errs:  []error{fmt.Errorf("expected 'xref' keyword at offset %d", offset)},
		}}
	}

	// Walk all subsection headers, skipping the entry lines for each subsection.
	// Each xref entry is exactly 20 bytes on the line (10-digit offset + space + 5-digit gen +
	// space + f/n + EOL). ReadLine consumes one entry per call.
	firstHeader := true
	for {
		line, ok := cur.ReadLine()
		if !ok || line == "trailer" {
			break
		}
		if line == "" {
			if firstHeader {
				// 6.1.4: an extra blank line here means no header directly
				// follows the xref keyword.
				return []PDFError{{
					check: Checks.Structure.XRefSubsectionHeader,
					errs:  []error{fmt.Errorf("blank line between 'xref' keyword and first cross reference subsection header")},
				}}
			}
			continue
		}

		// In a cross reference subsection header the starting object number and
		// the range shall be separated by a single SPACE (20h), no leading whitespace.
		if !xrefHeaderRe.MatchString(line) {
			return []PDFError{{
				check: Checks.Structure.XRefSubsectionHeaderFormat,
				errs:  []error{fmt.Errorf("malformed cross reference subsection header: %q", line)},
			}}
		}
		if firstHeader {
			firstHeader = false
		}

		var start, count int
		fmt.Sscanf(line, "%d %d", &start, &count)
		for i := 0; i < count; i++ {
			if _, ok := cur.ReadLine(); !ok {
				break
			}
		}
	}

	if firstHeader {
		return []PDFError{{
			check: Checks.Structure.XRefSubsectionHeader,
			errs:  []error{fmt.Errorf("expected cross reference subsection header after xref keyword at offset %d", offset)},
		}}
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
			check: Checks.Structure.InfoDictUnreadable,
			errs:  []error{fmt.Errorf("failed to get document information dictionary: %v", err)},
			page:  0,
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

	// 6.1.5: standard entries (Table 10.2, PDF Reference 4th ed.) must be
	// text strings or dates, except Trapped which is a name; custom keys are unchecked.
	if infoVal, err := d.ResolveGraphByPath([]string{"Info"}); err == nil {
		if infoDict, ok := infoVal.(PDFDict); ok {
			var typeErrs []error
			for k, v := range infoDict.Entries {
				if k == "_ref" || !slices.Contains(allowedFields, k) {
					continue
				}
				switch v.(type) {
				case PDFString, PDFHexString:
				case PDFName:
					if k != "Trapped" {
						typeErrs = append(typeErrs, fmt.Errorf("entry %v has non-string value", k))
					}
				default:
					typeErrs = append(typeErrs, fmt.Errorf("entry %v has non-string value", k))
				}
			}
			if len(typeErrs) > 0 {
				errs = append(errs, PDFError{check: Checks.Structure.InfoDictXMPMismatch, errs: typeErrs, page: 0})
			}
		}
	}

	// Custom keys are permitted; only entries present in Table 10.2 of
	// PDF Reference 4th ed. are checked for emptiness.
	emptyErrs := []error{}
	for k, v := range metadata {
		if slices.Contains(allowedFields, k) && len(v) == 0 {
			err := fmt.Errorf("empty value for key %v in information dictionary", k)
			emptyErrs = append(emptyErrs, err)
		}
	}

	if len(emptyErrs) > 0 {
		err := PDFError{check: Checks.Structure.InfoDictEmptyValues, errs: emptyErrs, page: 0}
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
			validateCMapStream(v, ctx)

			for k, val := range v.Entries {
				// 6.1.12: a dictionary key shall not exceed 127 bytes after
				// decoding PDF name-escape sequences (#XX).
				if k != "_ref" && len(k) > 127 {
					decoded := decodePDFName(k)
					if len(decoded) > 127 {
						ctx.Report(Checks.Structure.NameTooLong, v, fmt.Sprintf("dictionary key exceeds 127 bytes: %d", len(decoded)))
					}
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
				ctx.Report(Checks.Structure.ArrayTooLarge, v, fmt.Sprintf("array exceeds 8191 elements: %d", len(v)))
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

// computeContentUsage walks the resolved graph once, decoding each page's
// content stream (and any Form XObjects it invokes) at most once via ctx's
// decode cache, and computes two things checks need:
//   - reachable: Entries-map pointers of Form XObjects invoked (via Do) from
//     page content or other reachable Form XObjects.
//   - invisibleOnly, usedCodes, usedCIDs: font usage, as computed by
//     collectFontUsageFromBytes.
func computeContentUsage(graph PDFValue, ctx *ValidationContext) (reachable map[uintptr]bool, invisibleOnly map[uintptr]bool, usedCodes, usedCIDs map[uintptr]map[int]bool) {
	reachable = map[uintptr]bool{}
	fu := &fontUsage{
		visible:     map[uintptr]bool{},
		invisible:   map[uintptr]bool{},
		usedCodes:   map[uintptr]map[int]bool{},
		usedCIDs:    map[uintptr]map[int]bool{},
		visitedXObj: map[uintptr]bool{},
	}
	visitedPtrs := map[uintptr]bool{}

	var walkGraph func(v PDFValue)
	walkGraph = func(v PDFValue) {
		switch val := v.(type) {
		case PDFDict:
			ptr := pdfValuePointer(val.Entries)
			if visitedPtrs[ptr] {
				return
			}
			visitedPtrs[ptr] = true

			if val.Entries["Type"] == (PDFName{Value: "Page"}) {
				resources, _ := val.Entries["Resources"].(PDFDict)
				collectContentUsage(ctx, val.Entries["Contents"], resources, reachable, fu)
				return
			}
			for _, child := range val.Entries {
				walkGraph(child)
			}
		case PDFArray:
			ptr := pdfValuePointer(val)
			if visitedPtrs[ptr] {
				return
			}
			visitedPtrs[ptr] = true
			for _, item := range val {
				walkGraph(item)
			}
		}
	}
	walkGraph(graph)

	invisibleOnly = map[uintptr]bool{}
	for ptr := range fu.invisible {
		if !fu.visible[ptr] {
			invisibleOnly[ptr] = true
		}
	}
	return reachable, invisibleOnly, fu.usedCodes, fu.usedCIDs
}

func collectContentUsage(ctx *ValidationContext, contents PDFValue, resources PDFDict, reachable map[uintptr]bool, fu *fontUsage) {
	switch v := contents.(type) {
	case PDFDict:
		if v.HasStream {
			if data, err := ctx.decodeStreamCached(v); err == nil {
				collectReachableFromBytes(ctx, data, resources, reachable)
				collectFontUsageFromBytes(ctx, data, resources, fu)
			}
		}
	case PDFArray:
		for _, item := range v {
			if d, ok := item.(PDFDict); ok && d.HasStream {
				if data, err := ctx.decodeStreamCached(d); err == nil {
					collectReachableFromBytes(ctx, data, resources, reachable)
					collectFontUsageFromBytes(ctx, data, resources, fu)
				}
			}
		}
	}
}

func collectReachableFromBytes(ctx *ValidationContext, data []byte, resources PDFDict, reachable map[uintptr]bool) {
	xobjects, _ := resources.Entries["XObject"].(PDFDict)
	cs := newContentScanner(data)
	cs.scan(func(op string, operands []PDFValue) {
		if op != "Do" || len(operands) == 0 {
			return
		}
		name, ok := operands[len(operands)-1].(PDFName)
		if !ok || xobjects.Entries == nil {
			return
		}
		xobj, ok := xobjects.Entries[name.Value].(PDFDict)
		if !ok {
			return
		}
		ptr := pdfValuePointer(xobj.Entries)
		if reachable[ptr] {
			return
		}
		reachable[ptr] = true
		if xobj.Entries["Subtype"] == (PDFName{Value: "Form"}) && xobj.HasStream {
			subResources, _ := xobj.Entries["Resources"].(PDFDict)
			if subData, err := ctx.decodeStreamCached(xobj); err == nil {
				collectReachableFromBytes(ctx, subData, subResources, reachable)
			}
		}
	})
}

// fontUsage tracks visible vs. invisible-only rendering per font, plus the
// character codes (simple fonts) and CIDs (Identity-H/V fonts) actually shown.
type fontUsage struct {
	visible     map[uintptr]bool
	invisible   map[uintptr]bool
	usedCodes   map[uintptr]map[int]bool
	usedCIDs    map[uintptr]map[int]bool
	visitedXObj map[uintptr]bool
}

// collectFontUsageFromBytes scans decoded content-stream bytes, tracking the
// rendering mode (Tr, saved/restored across q/Q) and the font set by the most
// recent Tf, and records visibility and shown codes per font.
func collectFontUsageFromBytes(ctx *ValidationContext, data []byte, resources PDFDict, fu *fontUsage) {
	fonts, _ := resources.Entries["Font"].(PDFDict)
	xobjects, _ := resources.Entries["XObject"].(PDFDict)
	cs := newContentScanner(data)
	renderMode := 0
	var modeStack []int
	var currentFontPtrs []uintptr
	var simpleFontPtr uintptr
	haveSimpleFont := false
	var compositeFontPtr uintptr
	haveCompositeFont := false
	cs.scan(func(op string, operands []PDFValue) {
		switch op {
		case "q":
			modeStack = append(modeStack, renderMode)
		case "Q":
			if len(modeStack) > 0 {
				renderMode = modeStack[len(modeStack)-1]
				modeStack = modeStack[:len(modeStack)-1]
			}
		case "Tr":
			if len(operands) > 0 {
				if n, ok := operands[len(operands)-1].(PDFInteger); ok {
					renderMode = int(n)
				}
			}
		case "Tf":
			currentFontPtrs = nil
			haveSimpleFont = false
			haveCompositeFont = false
			if len(operands) >= 2 && fonts.Entries != nil {
				if name, ok := operands[len(operands)-2].(PDFName); ok {
					if fd, ok := fonts.Entries[name.Value].(PDFDict); ok {
						currentFontPtrs = append(currentFontPtrs, pdfValuePointer(fd.Entries))
						// 6.3.3.2/6.3.5/6.3.6 checks run on the descendant
						// CIDFont dict, not the Type0 font selected by Tf.
						if df, ok := fd.Entries["DescendantFonts"].(PDFArray); ok && len(df) > 0 {
							if desc, ok := df[0].(PDFDict); ok {
								currentFontPtrs = append(currentFontPtrs, pdfValuePointer(desc.Entries))
								// Only Identity-H/V map codes directly to CIDs;
								// other CMaps leave usage unknown for the font.
								if enc, ok := fd.Entries["Encoding"].(PDFName); ok &&
									(enc.Value == "Identity-H" || enc.Value == "Identity-V") {
									compositeFontPtr = pdfValuePointer(desc.Entries)
									haveCompositeFont = true
								}
							}
						} else {
							// Simple font: codes shown via Tj/TJ are single bytes.
							simpleFontPtr = pdfValuePointer(fd.Entries)
							haveSimpleFont = true
						}
					}
				}
			}
		case "Tj", "TJ", "'", "\"":
			for _, ptr := range currentFontPtrs {
				if renderMode == 3 || renderMode == 7 {
					fu.invisible[ptr] = true
				} else {
					fu.visible[ptr] = true
				}
			}
			if haveSimpleFont {
				set := fu.usedCodes[simpleFontPtr]
				if set == nil {
					set = map[int]bool{}
					fu.usedCodes[simpleFontPtr] = set
				}
				for _, b := range shownStringBytes(op, operands) {
					set[int(b)] = true
				}
			}
			if haveCompositeFont {
				set := fu.usedCIDs[compositeFontPtr]
				if set == nil {
					set = map[int]bool{}
					fu.usedCIDs[compositeFontPtr] = set
				}
				shown := shownStringBytes(op, operands)
				for i := 0; i+1 < len(shown); i += 2 {
					set[int(shown[i])<<8|int(shown[i+1])] = true
				}
			}
		case "Do":
			if len(operands) == 0 || xobjects.Entries == nil {
				return
			}
			name, ok := operands[len(operands)-1].(PDFName)
			if !ok {
				return
			}
			xobj, ok := xobjects.Entries[name.Value].(PDFDict)
			if !ok || xobj.Entries["Subtype"] != (PDFName{Value: "Form"}) || !xobj.HasStream {
				return
			}
			ptr := pdfValuePointer(xobj.Entries)
			if fu.visitedXObj[ptr] {
				return
			}
			fu.visitedXObj[ptr] = true
			subResources, _ := xobj.Entries["Resources"].(PDFDict)
			if subResources.Entries == nil {
				subResources = resources
			}
			if subData, err := ctx.decodeStreamCached(xobj); err == nil {
				collectFontUsageFromBytes(ctx, subData, subResources, fu)
			}
		}
	})
}

// shownStringBytes returns the decoded bytes of all string operands a
// text-showing operator passes to the font.
func shownStringBytes(op string, operands []PDFValue) []byte {
	var out []byte
	appendOperand := func(v PDFValue) {
		switch s := v.(type) {
		case PDFString:
			out = append(out, decodePDFLiteralStringBytes(s.Value)...)
		case PDFHexString:
			out = append(out, decodePDFHexStringBytes(s.Value)...)
		}
	}
	switch op {
	case "TJ":
		if len(operands) > 0 {
			if arr, ok := operands[len(operands)-1].(PDFArray); ok {
				for _, item := range arr {
					appendOperand(item)
				}
			}
		}
	default: // Tj, ', "
		if len(operands) > 0 {
			appendOperand(operands[len(operands)-1])
		}
	}
	return out
}

// decodePDFLiteralStringBytes decodes a PDF literal string's backslash escape
// sequences into the bytes it represents.
func decodePDFLiteralStringBytes(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c != '\\' {
			out = append(out, c)
			i++
			continue
		}
		i++
		if i >= len(s) {
			break
		}
		switch s[i] {
		case 'n':
			out = append(out, '\n')
			i++
		case 'r':
			out = append(out, '\r')
			i++
		case 't':
			out = append(out, '\t')
			i++
		case 'b':
			out = append(out, '\b')
			i++
		case 'f':
			out = append(out, '\f')
			i++
		case '(', ')', '\\':
			out = append(out, s[i])
			i++
		case '\r':
			i++
			if i < len(s) && s[i] == '\n' {
				i++
			}
		case '\n':
			i++
		default:
			if s[i] >= '0' && s[i] <= '7' {
				v, j := 0, 0
				for j < 3 && i < len(s) && s[i] >= '0' && s[i] <= '7' {
					v = v*8 + int(s[i]-'0')
					i++
					j++
				}
				out = append(out, byte(v))
			} else {
				out = append(out, s[i])
				i++
			}
		}
	}
	return out
}

// decodePDFHexStringBytes decodes a hex string's digit characters into bytes,
// ignoring whitespace and padding a trailing odd nibble with 0.
func decodePDFHexStringBytes(s string) []byte {
	digits := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if hexDigit(s[i]) >= 0 {
			digits = append(digits, s[i])
		}
	}
	if len(digits)%2 != 0 {
		digits = append(digits, '0')
	}
	out := make([]byte, 0, len(digits)/2)
	for i := 0; i < len(digits); i += 2 {
		out = append(out, byte(hexDigit(digits[i])<<4|hexDigit(digits[i+1])))
	}
	return out
}

// decodePDFName decodes PDF name #XX escape sequences and returns the
// resulting byte slice. Unescaped bytes are returned as-is.
func decodePDFName(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		if s[i] == '#' && i+2 < len(s) {
			hi := hexVal(s[i+1])
			lo := hexVal(s[i+2])
			if hi >= 0 && lo >= 0 {
				out = append(out, byte(hi<<4|lo))
				i += 3
				continue
			}
		}
		out = append(out, s[i])
		i++
	}
	return out
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	}
	return -1
}

// validateHexString validates requirements outlined in 6.1.6.
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
		ctx.ReportErrs(Checks.Structure.HexStringInvalidChar, v, hexErrs)
	}

	if hexCount%2 != 0 {
		ctx.Report(Checks.Structure.HexStringOddLength, v, fmt.Sprintf("contains an odd number of hex chars (%d)", hexCount))
	}
}

// validateStreamObject validates requirements outlined in 6.1.7 and 6.1.10.
func validateStreamObject(v PDFDict, ctx *ValidationContext) {
	if v.Entries["F"] != nil {
		ctx.Report(Checks.Structure.StreamFileSpec, v, "stream object contains invalid key F")
	}
	if v.Entries["FFilter"] != nil {
		ctx.Report(Checks.Structure.StreamFileFilter, v, "stream object contains invalid key FFilter")
	}
	if v.Entries["FDecodeParms"] != nil {
		ctx.Report(Checks.Structure.StreamFileDecodeParams, v, "stream object contains invalid key FDecodeParms")
	}
	for _, f := range filterNames(v.Entries["Filter"]) {
		if f == "LZWDecode" || f == "LZW" {
			ctx.Report(Checks.Structure.StreamLZWFilter, v, "stream object uses forbidden LZWDecode filter")
		}
	}
}

// validateObject validates requirements outlined in 6.1.11
func validateObject(v PDFDict, ctx *ValidationContext) {
	if v.Entries["EF"] != nil {
		ctx.Report(Checks.Structure.EmbeddedFileSpec, v, "dictionary shall not contain EF key")
	}
	if v.Entries["EmbeddedFiles"] != nil {
		ctx.Report(Checks.Structure.EmbeddedFiles, v, "dictionary shall not contain EmbeddedFiles key")
	}
}

// validateArchitecturalLimits validates requirements outlined in 6.1.12
func validateArchitecturalLimits(node PDFValue, ctx *ValidationContext) {
	switch v := node.(type) {
	case PDFName:
		// Maximum length of a name, in bytes: 127
		nameLen := len(v.Value)
		if nameLen > 127 {
			ctx.Report(Checks.Structure.NameTooLong, v, fmt.Sprintf("maximum length of name (127) exceeded: %v", nameLen))
		}
	case PDFInteger:
		// 6.1.12: integer values are limited to the 32-bit signed range.
		if v < -2_147_483_648 || v > 2_147_483_647 {
			ctx.Report(Checks.Structure.IntegerOutOfRange, v, fmt.Sprintf("integer value exceeded limits: %v", v))
		}
	case PDFReal:
		// 6.1.12: magnitude of real numbers shall not exceed 32767.
		if v < -32767 || v > 32767 {
			ctx.Report(Checks.Structure.IntegerOutOfRange, v, fmt.Sprintf("real number out of range: %g", float64(v)))
		}
	case PDFString:
		// 6.1.12: maximum length of a string object is 65535 bytes.
		// The parser stores raw (unescaped) bytes; compute the decoded length.
		if pdfStringDecodedLen(v.Value) > 65535 {
			ctx.Report(Checks.Structure.StringTooLong, v, "string exceeds maximum length of 65535 bytes")
		}
	case PDFDict:
		// Maximum number of entries in a dictionary: 4096
		realCount := len(v.Entries)
		if _, has := v.Entries["_ref"]; has {
			realCount--
		}
		if realCount > 4096 {
			ctx.Report(Checks.Structure.DictTooLarge, v, fmt.Sprintf("dictionary exceeds 4096 entries: %d", realCount))
		}
	}
}

// pdfStringDecodedLen returns the decoded byte length of a PDF literal string
// that the lexer stored as raw (unescaped) bytes (backslash sequences intact).
func pdfStringDecodedLen(s string) int {
	count := 0
	i := 0
	for i < len(s) {
		if s[i] != '\\' {
			count++
			i++
			continue
		}
		i++ // consume backslash
		if i >= len(s) {
			break
		}
		switch s[i] {
		case 'n', 'r', 't', 'b', 'f', '\\', '(', ')':
			count++
			i++
		case '\r':
			// line continuation: \<CR> or \<CR><LF> → 0 bytes
			i++
			if i < len(s) && s[i] == '\n' {
				i++
			}
		case '\n':
			// line continuation: \<LF> → 0 bytes
			i++
		default:
			// Octal escape: up to 3 octal digits
			if s[i] >= '0' && s[i] <= '7' {
				j := 0
				for j < 3 && i < len(s) && s[i] >= '0' && s[i] <= '7' {
					i++
					j++
				}
				count++
			} else {
				// Unknown escape: treat backslash as literal (shouldn't happen in valid PDF)
				count += 2
				i++
			}
		}
	}
	return count
}

// verifyOptionalContent verifies requirements outlined in 6.1.13
func (d *Document) verifyOptionalContent() []PDFError {
	_, err := d.ResolveGraphByPath([]string{"Root", "OCProperties"})
	if err == nil {
		return []PDFError{{
			check: Checks.Structure.OptionalContent,
			errs:  []error{fmt.Errorf("OCProperties not allowed in document catalog")},
			page:  0,
		}}
	}
	return nil
}

// 6.2 Graphics

// verifyOutputIntent verifies requirements outlined in 6.2.2
func (d *Document) verifyOutputIntent() []PDFError {
	values, err := d.ResolveGraphByPath([]string{"Root", "OutputIntents"})
	if err != nil || values == nil {
		// OutputIntents are optional.
		return nil
	}

	intents, ok := values.(PDFArray)
	if !ok {
		return []PDFError{{
			check: Checks.Colour.OutputIntentNotArray,
			errs:  []error{fmt.Errorf("OutputIntents object is not an array")},
			page:  0,
		}}
	}

	errs := []PDFError{}

	var indirectObject PDFValue

	for _, v := range intents {
		intent, ok := v.(PDFDict)
		if !ok {
			err := PDFError{
				check: Checks.Colour.OutputIntentNotDict,
				errs:  []error{fmt.Errorf("expected OutputIntent to be a PDFDict")},
				page:  0,
			}
			errs = append(errs, err)
			continue
		}
		s, ok := intent.Entries["S"].(PDFName)
		if !ok {
			err := PDFError{
				check: Checks.Colour.OutputIntentInvalidS,
				errs:  []error{fmt.Errorf("expected S to be a PDFName")},
				page:  0,
			}
			errs = append(errs, err)
			continue
		}

		if s.Value != "GTS_PDFA1" {
			err := PDFError{
				check: Checks.Colour.OutputIntentWrongS,
				errs:  []error{fmt.Errorf("expected S was not GTS_PDFA1, but %v", intent.Entries["S"])},
				page:  0,
			}
			errs = append(errs, err)
		}

		if intent.Entries["OutputConditionIdentifier"] == nil {
			err := PDFError{
				check: Checks.Colour.OutputIntentMissingIdentifier,
				errs:  []error{fmt.Errorf("OutputConditionIdentifier is required but was nil")},
				page:  0,
			}
			errs = append(errs, err)
			continue
		}

		destOutputProfile := intent.Entries["DestOutputProfile"]
		if destOutputProfile == nil {
			// 6.2.2: DestOutputProfile shall be present unless OutputConditionIdentifier
			// names a standard ICC registry profile, which is not the case for "Custom".
			errs = append(errs, PDFError{
				check: Checks.Colour.OutputIntentUnresolvedProfile,
				errs:  []error{fmt.Errorf("DestOutputProfile is required when OutputConditionIdentifier does not specify a standard production condition")},
				page:  0,
			})
			continue
		}

		// If a file's OutputIntents array contains more than one entry, then all entries that contain a DestOutputProfile
		// key shall have as the value of that key the same indirect object, which shall be a valid ICC profile stream.
		if indirectObject == nil {
			indirectObject = destOutputProfile
		} else {
			if !EqualPDFValue(indirectObject, destOutputProfile) {
				err := PDFError{
					check: Checks.Colour.OutputIntentMultipleProfiles,
					errs:  []error{fmt.Errorf("expected DestOutputProfile to be %v but was %v", indirectObject, destOutputProfile)},
					page:  0,
				}
				errs = append(errs, err)
				continue
			}
		}

		profile, err := d.resolveObject(destOutputProfile)
		if err != nil {
			err := PDFError{
				check: Checks.Colour.OutputIntentUnresolvedProfile,
				errs:  []error{fmt.Errorf("unable to resolve DestOutputProfile: %v", err)},
				page:  0,
			}
			errs = append(errs, err)
			continue
		}

		profileMap, ok := profile.(PDFDict)
		if !ok {
			err := PDFError{
				check: Checks.Colour.OutputIntentInvalidProfile,
				errs:  []error{fmt.Errorf("unexpected format for DestOutputProfile encountered")},
				page:  0,
			}
			errs = append(errs, err)
			continue
		}

		nValue, ok := profileMap.Entries["N"].(PDFInteger)
		if !ok {
			err := PDFError{
				check: Checks.Colour.OutputIntentMissingN,
				errs:  []error{fmt.Errorf("could not retrieve number of colour components N")},
				page:  0,
			}
			errs = append(errs, err)
			continue
		}

		// N shall be 1, 3, or 4
		if !slices.Contains([]int{1, 3, 4}, int(nValue)) {
			err := PDFError{
				check: Checks.Colour.OutputIntentInvalidN,
				errs:  []error{fmt.Errorf("number of colour components N must be 1, 3, or 4")},
				page:  0,
			}
			errs = append(errs, err)
		}

		// 6.2.2: the ICC profile stream shall be a valid ICC.1:2003-09 profile (version ≤ 2.x).
		if profileMap.HasStream {
			if iccErr := validateICCProfileStream(profileMap); iccErr != nil {
				errs = append(errs, PDFError{
					check: Checks.Colour.OutputIntentICCVersion,
					errs:  []error{iccErr},
					page:  0,
				})
			}
		}
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}

// iccValidDeviceClasses are the ICC profile device classes permitted in PDF/A-1.
var iccValidDeviceClasses = map[string]bool{
	"scnr": true, "mntr": true, "prtr": true, "link": true,
	"spac": true, "abst": true, "nmcl": true,
}

// iccValidColorSpaces are the ICC color space signatures defined by ICC.1.
var iccValidColorSpaces = map[string]bool{
	"XYZ ": true, "Lab ": true, "Luv ": true, "YCbr": true, "Yxy ": true,
	"RGB ": true, "GRAY": true, "HSV ": true, "HLS ": true, "CMYK": true,
	"CMY ": true, "2CLR": true, "3CLR": true, "4CLR": true, "5CLR": true,
	"6CLR": true, "7CLR": true, "8CLR": true, "9CLR": true, "ACLR": true,
	"BCLR": true, "CCLR": true, "DCLR": true, "ECLR": true, "FCLR": true,
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
	deviceClass := string(data[12:16])
	if !iccValidDeviceClasses[deviceClass] {
		return fmt.Errorf("ICC profile has invalid deviceClass %q", deviceClass)
	}
	colorSpace := string(data[16:20])
	if !iccValidColorSpaces[colorSpace] {
		return fmt.Errorf("ICC profile has invalid colorSpace %q", colorSpace)
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

// checkLinearizedFileID detects the 6.1.3 violation where a linearized PDF's
// first-page and overflow trailers carry different ID[0] values. Applies only
// when the main trailer is minimal (lacks /Root), the mark of a linearized overflow section.
func (d *Document) checkLinearizedFileID() []PDFError {
	// A main trailer with /Root is either an ordinary PDF or an
	// incrementally-updated one; cross-trailer ID consistency does not apply.
	if d.trailer.Entries["Root"] != nil {
		return nil
	}
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
				check: Checks.Structure.TrailerID,
				errs:  []error{fmt.Errorf("linearized PDF: ID[0] (%s) differs from %s in another trailer", first, id)},
				page:  0,
			}}
		}
	}
	return nil
}
