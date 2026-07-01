package verify

import (
	"fmt"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// xrefHeaderRe matches a well-formed cross-reference subsection header
// ("start count" separated by a single space, no leading white space).
var xrefHeaderRe = regexp.MustCompile(`^[0-9]+ [0-9]+$`)

// Verify verifies d against the checks enabled in profile p.
func Verify(d *pdf.Reader, p *pdf.Profile) (pdf.Result, error) {
	if p == nil {
		return pdf.Result{}, fmt.Errorf("nil profile")
	}
	if p.Level == pdf.Undefined {
		return pdf.Result{Type: p.Level, Valid: false}, fmt.Errorf("cannot verify PDF to undefined conformance level")
	}

	var issues []pdf.PDFError
	if p.Level == pdf.A_1B {
		issues = verifyPdfA1b(d, p)
	}
	issues = filterByProfile(issues, p)

	if len(issues) > 0 {
		return pdf.Result{Type: p.Level, Valid: false, Issues: issues}, nil
	}
	return pdf.Result{Type: p.Level, Valid: true}, nil
}

// VerifyAll opens and verifies multiple PDF files concurrently.
func VerifyAll(paths []string, p *pdf.Profile) ([]pdf.FileResult[pdf.Result], error) {
	results := make([]pdf.FileResult[pdf.Result], len(paths))

	workers := min(runtime.NumCPU(), len(paths))
	if workers < 1 {
		return results, nil
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = verifyFile(paths[i], p)
			}
		}()
	}
	for i := range paths {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	return results, nil
}

// VerifyFile opens, verifies, and closes a single file.
func VerifyFile(path string, p *pdf.Profile) (pdf.Result, error) {
	doc, err := pdf.Open(path)
	if err != nil {
		return pdf.Result{}, err
	}
	defer doc.Close()
	return Verify(doc, p)
}

// VerifyBytes verifies an in-memory PDF.
func VerifyBytes(data []byte, p *pdf.Profile) (pdf.Result, error) {
	doc, err := pdf.OpenBytes(data)
	if err != nil {
		return pdf.Result{}, fmt.Errorf("verify: %w", err)
	}
	defer doc.Close()
	return Verify(doc, p)
}

func verifyFile(path string, p *pdf.Profile) pdf.FileResult[pdf.Result] {
	res, err := VerifyFile(path, p)
	return pdf.FileResult[pdf.Result]{Path: path, Result: res, Err: err}
}

// filterByProfile removes from issues any PDFError whose (clause, subclause)
// pair is registered in the catalog but disabled in p.
func filterByProfile(issues []pdf.PDFError, p *pdf.Profile) []pdf.PDFError {
	out := make([]pdf.PDFError, 0, len(issues))
	for _, e := range issues {
		if p.Allows(e.Check().Clause(), e.Check().Subclause()) {
			out = append(out, e)
		}
	}
	return out
}

// PDF/A-1b (ISO 19005-1:2005)

func verifyPdfA1b(d *pdf.Reader, p *pdf.Profile) []pdf.PDFError {
	issues := []pdf.PDFError{}

	errs := verifyFileHeader(d)
	if errs != nil {
		issues = append(issues, errs...)
	}
	errs = checkLinearizedFileID(d)
	if errs != nil {
		issues = append(issues, errs...)
	}
	errs = verifyFileTrailer(d)
	if errs != nil {
		issues = append(issues, errs...)
	}
	errs = verifyCrossReferenceTable(d)
	if errs != nil {
		issues = append(issues, errs...)
	}

	// Resolve the graph once up front; all subsequent checks work on the
	// resolved graph so no per-check lazy resolve occurs.
	graph, err := d.ResolveGraph()
	if err != nil {
		return append(issues, pdf.NewError(pdf.Checks.Structure.GraphResolutionFailure, []error{err}, 0, nil))
	}

	errs = verifyDocumentInformationDictionary(graph)
	if errs != nil {
		issues = append(issues, errs...)
	}

	pageIndex, err := d.BuildPageIndex(graph)
	if err != nil {
		return append(issues, pdf.NewError(pdf.Checks.Structure.GraphResolutionFailure, []error{err}, 0, nil))
	}

	ctx := &ValidationContext{
		PageIndex: pageIndex,
		reader:    d,
	}
	reachable, invisibleOnly, usedCodes, usedCIDs := ComputeContentUsage(graph, ctx)
	if p.SkipUnreachableXObjects {
		ctx.ReachableXObjectPtrs = reachable
	}
	ctx.SkipUnusedSimpleFonts = p.SkipUnusedSimpleFonts
	ctx.InvisibleOnlyFontPtrs, ctx.UsedCharCodes, ctx.UsedCIDs = invisibleOnly, usedCodes, usedCIDs
	computeColourCoverage(d, ctx)

	verifyDocument(graph, ctx)
	errs = ctx.errs
	if errs != nil {
		issues = append(issues, errs...)
	}
	errs = verifyOptionalContent(d)
	if errs != nil {
		issues = append(issues, errs...)
	}
	errs = verifyOutputIntent(d)
	if errs != nil {
		issues = append(issues, errs...)
	}
	errs = verifyInteractiveForms(d)
	if errs != nil {
		issues = append(issues, errs...)
	}
	errs = verifyXMPMetadata(d)
	if errs != nil {
		issues = append(issues, errs...)
	}
	errs = checkNonCatalogXMPStreams(graph)
	if errs != nil {
		issues = append(issues, errs...)
	}

	errs = verifyAllObjectFraming(d)
	if errs != nil {
		issues = append(issues, errs...)
	}

	issues = append(issues, d.StructErrors()...)
	return issues
}

func verifyAllObjectFraming(d *pdf.Reader) []pdf.PDFError {
	xrefTable := d.XRefTable()
	if len(xrefTable) == 0 {
		return []pdf.PDFError{pdf.NewError(
			pdf.Checks.Structure.IndirectObjectsExceeded,
			[]error{fmt.Errorf("number of indirect objects exceeds 8,388,607")},
			1,
			nil,
		)}
	}

	for objNum := range d.XRefTable() {
		d.ResolveReference(pdf.PDFRef{ObjNum: objNum})
	}
	return nil
}

// 6.1 File Structure

// pdfVersionRe matches a valid %PDF-N.M header line.
var pdfVersionRe = regexp.MustCompile(`^%PDF-\d\.\d$`)

// verifyFileHeader verifies requirements outlined in 6.1.2.
func verifyFileHeader(d *pdf.Reader) []pdf.PDFError {
	buf := make([]byte, 128)
	n, _ := d.ReadAt(buf, 0)

	cur := pdf.NewCursor(buf[:n])

	errs := []pdf.PDFError{}

	header, ok := cur.ReadLine()
	if !ok || !pdfVersionRe.MatchString(header) {
		errs = append(errs, pdf.NewError(
			pdf.Checks.Structure.FileHeaderSignature,
			[]error{fmt.Errorf("invalid PDF header: %q (must be %%PDF-N.M)", header)},
			1,
			nil,
		))
	}

	comment, ok := cur.ReadLine()
	if !ok || len(comment) == 0 || comment[0] != '%' {
		errs = append(errs, pdf.NewError(
			pdf.Checks.Structure.FileHeaderComment,
			[]error{fmt.Errorf("header must be followed by comment, but was: %v", comment)},
			1,
			nil,
		))
		return errs
	}

	// 6.1.2/3: the comment line (including the leading %) must be at least
	// 5 characters long, i.e. at least 4 bytes must follow the %.
	commentBytes := []byte(comment[1:])
	if len(commentBytes) < 4 {
		errs = append(errs, pdf.NewError(
			pdf.Checks.Structure.FileHeaderCommentLength,
			[]error{fmt.Errorf("comment line must consist of at least 5 characters, but was: %d", len(comment))},
			1,
			nil,
		))
	} else {
		// 6.1.2/4: each of the first four bytes after % must be > 127 (binary
		// indicator); bytes beyond that are unconstrained.
		var badBytes []error
		for _, b := range commentBytes[:4] {
			if b <= 127 {
				badBytes = append(
					badBytes,
					fmt.Errorf("comment line contains ASCII character (0x%02x); all bytes must be > 127", b),
				)
			}
		}
		if len(badBytes) > 0 {
			errs = append(errs, pdf.NewError(pdf.Checks.Structure.FileHeaderCommentBytes, badBytes, 1, nil))
		}
	}

	if len(errs) > 0 {
		return errs
	}

	return nil
}

// verifyFileTrailer verifies requirements outlined in 6.1.3
func verifyFileTrailer(d *pdf.Reader) []pdf.PDFError {
	errs := []pdf.PDFError{}

	// Use the effective trailer: for linearized PDFs with a minimal overflow
	// trailer (no /Root), this is the first-page trailer that holds /ID, /Root, etc.
	eff := d.EffectiveTrailer()

	if eff.Entries["ID"] == nil {
		err := pdf.NewError(
			pdf.Checks.Structure.TrailerID,
			[]error{fmt.Errorf("trailer does not contain the required ID keyword")},
			0,
			nil,
		)
		errs = append(errs, err)
	}

	if eff.Entries["Encrypt"] != nil {
		err := pdf.NewError(
			pdf.Checks.Structure.TrailerEncrypt,
			[]error{fmt.Errorf("trailer contains the forbidden Encrypt keyword")},
			0,
			nil,
		)
		errs = append(errs, err)
	}

	// No data shall follow the last end-of-file marker except a single optional end-of-line marker.
	size := d.Size()

	found := false
	eof := make([]byte, 0)
	for i := range int64(10) {
		buf := make([]byte, 1)
		d.ReadAt(buf, size-i)

		eof = append([]byte{buf[0]}, eof...)
		if strings.HasPrefix(string(eof), "%%EOF") {
			found = true
			break
		}
	}
	if !found {
		err := pdf.NewError(
			pdf.Checks.Structure.TrailerEOF,
			[]error{fmt.Errorf("no EOF marker found: %v", string(eof))},
			0,
			nil,
		)
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return errs
	}

	return nil
}

// verifyCrossReferenceTable verifies requirements outlined in 6.1.4 for the
// current xref section and all prior sections linked via Prev.
func verifyCrossReferenceTable(d *pdf.Reader) []pdf.PDFError {
	if errs := checkXRefSectionFormat(d, d.XRefOffset()); len(errs) > 0 {
		return errs
	}

	visited := map[int64]bool{d.XRefOffset(): true}
	prev := d.Trailer().Entries["Prev"]
	for {
		prevInt, ok := prev.(pdf.PDFInteger)
		if !ok {
			break
		}
		prevOffset := int64(prevInt)
		if visited[prevOffset] {
			break
		}
		visited[prevOffset] = true

		if errs := checkXRefSectionFormat(d, prevOffset); len(errs) > 0 {
			return errs
		}

		prevTrailer, err := d.ParseXRefSectionAt(prevOffset+d.PDFStart(), false)
		if err != nil {
			break
		}
		prev = prevTrailer.Entries["Prev"]
	}

	return nil
}

// checkXRefSectionFormat reads the xref section at the given file offset and
// validates the xref keyword, all subsection headers, and their format per 6.1.4.
func checkXRefSectionFormat(d *pdf.Reader, offset int64) []pdf.PDFError {
	buf := make([]byte, 8192)
	n, _ := d.ReadAt(buf, offset+d.PDFStart())
	cur := pdf.NewCursor(buf[:n])

	// The xref keyword and the cross reference subsection header shall be separated by a single EOL marker.
	xRef, ok := cur.ReadLine()
	if !ok || len(xRef) == 0 || xRef != "xref" {
		return []pdf.PDFError{pdf.NewError(
			pdf.Checks.Structure.XRefKeyword,
			[]error{fmt.Errorf("expected 'xref' keyword at offset %d", offset)},
			0,
			nil,
		)}
	}

	// Walk all subsection headers, skipping the entry lines for each subsection.
	// Each xref entry is exactly 20 bytes on the line (10-digit offset + space + 5-digit gen +
	// space + f/n + EOL). ReadLine consumes one entry per call.
	firstHeader := true
	for {
		line, ok := cur.ReadLine()
		// The trailer keyword may stand alone or share its line with the dict
		// ("trailer << ... >>").
		if !ok || line == "trailer" || strings.HasPrefix(line, "trailer ") || strings.HasPrefix(line, "trailer<") {
			break
		}
		if line == "" {
			if firstHeader {
				// 6.1.4: an extra blank line here means no header directly
				// follows the xref keyword.
				return []pdf.PDFError{pdf.NewError(
					pdf.Checks.Structure.XRefSubsectionHeader,
					[]error{fmt.Errorf("blank line between 'xref' keyword and first cross reference subsection header")},
					0,
					nil,
				)}
			}
			continue
		}

		// In a cross reference subsection header the starting object number and
		// the range shall be separated by a single SPACE (20h), no leading whitespace.
		if !xrefHeaderRe.MatchString(line) {
			return []pdf.PDFError{pdf.NewError(
				pdf.Checks.Structure.XRefSubsectionHeaderFormat,
				[]error{fmt.Errorf("malformed cross reference subsection header: %q", line)},
				0,
				nil,
			)}
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
		return []pdf.PDFError{pdf.NewError(
			pdf.Checks.Structure.XRefSubsectionHeader,
			[]error{fmt.Errorf("expected cross reference subsection header after xref keyword at offset %d", offset)},
			0,
			nil,
		)}
	}

	return nil
}

// verifyDocumentInformationDictionary verifies requirements outlined in 6.1.5
func verifyDocumentInformationDictionary(graph pdf.PDFValue) []pdf.PDFError {
	trailer, ok := graph.(pdf.PDFDict)
	if !ok || trailer.Entries["Info"] == nil {
		return nil
	}

	infoDict, ok := trailer.Entries["Info"].(pdf.PDFDict)
	if !ok {
		return []pdf.PDFError{pdf.NewError(
			pdf.Checks.Structure.InfoDictUnreadable,
			[]error{fmt.Errorf("Info entry is not a dictionary")},
			0,
			nil,
		)}
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

	var errs []pdf.PDFError

	// 6.1.5: standard entries (Table 10.2, PDF Reference 4th ed.) must be
	// text strings or dates, except Trapped which is a name; custom keys are unchecked.
	var typeErrs []error
	for k, v := range infoDict.Entries {
		if k == "_ref" || !slices.Contains(allowedFields, k) {
			continue
		}
		switch v.(type) {
		case pdf.PDFString, pdf.PDFHexString:
		case pdf.PDFName:
			if k != "Trapped" {
				typeErrs = append(typeErrs, fmt.Errorf("entry %v has non-string value", k))
			}
		default:
			typeErrs = append(typeErrs, fmt.Errorf("entry %v has non-string value", k))
		}
	}
	if len(typeErrs) > 0 {
		errs = append(errs, pdf.NewError(pdf.Checks.Structure.InfoDictXMPMismatch, typeErrs, 0, nil))
	}

	// Custom keys are permitted; only entries present in Table 10.2 of
	// PDF Reference 4th ed. are checked for emptiness.
	var emptyErrs []error
	for k, v := range infoDict.Entries {
		if k == "_ref" || !slices.Contains(allowedFields, k) {
			continue
		}
		var s string
		switch tv := v.(type) {
		case pdf.PDFString:
			s = pdf.DecodePDFTextString(pdf.DecodePDFLiteralStringBytes(tv.Value))
		case pdf.PDFHexString:
			s = pdf.DecodePDFTextString(pdf.DecodePDFHexStringBytes(tv.Value))
		default:
			continue
		}
		if len(s) == 0 {
			emptyErrs = append(emptyErrs, fmt.Errorf("empty value for key %v in information dictionary", k))
		}
	}
	if len(emptyErrs) > 0 {
		errs = append(errs, pdf.NewError(pdf.Checks.Structure.InfoDictEmptyValues, emptyErrs, 0, nil))
	}

	return errs
}

// verifyDocument verifies the entire document graph, including all pages, resources, and content streams.
func verifyDocument(graph pdf.PDFValue, ctx *ValidationContext) {
	visited := make(map[uintptr]bool)

	var walk func(node any)

	walk = func(node any) {
		if node == nil {
			return
		}

		switch v := node.(type) {
		case pdf.PDFDict:
			ptr := pdf.ValuePointer(v.Entries)
			if visited[ptr] {
				return
			}
			visited[ptr] = true

			if (v.Entries["Type"] == pdf.PDFName{Value: "Page"}) {
				if ref, ok := v.Entries["_ref"].(pdf.PDFRef); ok {
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
			ValidateFontDict(v, ctx)
			validateCMapStream(v, ctx)
			validateViewerPreferences(v, ctx)

			for k, val := range v.Entries {
				// 6.1.12: a dictionary key shall not exceed 127 bytes after
				// decoding PDF name-escape sequences (#XX).
				if k != "_ref" && len(k) > 127 {
					decoded := pdf.DecodePDFName(k)
					if len(decoded) > 127 {
						ctx.Report(
							pdf.Checks.Structure.NameTooLong,
							v,
							fmt.Sprintf("dictionary key exceeds 127 bytes: %d", len(decoded)),
						)
					}
				}
				walk(val)
			}

		case pdf.PDFArray:
			ptr := pdf.ValuePointer(v)
			if visited[ptr] {
				return
			}
			visited[ptr] = true

			validateColourSpaceArray(v, ctx)

			// 6.1.12: maximum number of elements in an array is 8191.
			if len(v) > 8191 {
				ctx.Report(
					pdf.Checks.Structure.ArrayTooLarge,
					v,
					fmt.Sprintf("array exceeds 8191 elements: %d", len(v)),
				)
			}

			for _, item := range v {
				walk(item)
			}

		case pdf.PDFHexString:
			// Hexadecimal strings shall contain an even number of non-white-space characters,
			// each in the range 0 to 9, A to F or a to f.
			validateHexString(v, ctx)
		}

		validateArchitecturalLimits(node, ctx)
	}

	walk(graph)
}

// ComputeContentUsage walks the resolved graph once, decoding each page's
// content stream (and any Form XObjects it invokes) at most once via ctx's
// decode cache, and computes two things checks need:
//   - reachable: Entries-map pointers of Form XObjects invoked (via Do) from
//     page content or other reachable Form XObjects.
//   - invisibleOnly, usedCodes, usedCIDs: font usage, as computed by
//     collectFontUsageFromBytes.
func ComputeContentUsage(graph pdf.PDFValue, ctx *ValidationContext) (
	reachable map[uintptr]bool,
	invisibleOnly map[uintptr]bool,
	usedCodes, usedCIDs map[uintptr]map[int]bool,
) {
	reachable = map[uintptr]bool{}
	fu := &fontUsage{
		visible:     map[uintptr]bool{},
		invisible:   map[uintptr]bool{},
		usedCodes:   map[uintptr]map[int]bool{},
		usedCIDs:    map[uintptr]map[int]bool{},
		visitedXObj: map[uintptr]bool{},
	}
	visitedPtrs := map[uintptr]bool{}

	var walkGraph func(v pdf.PDFValue)
	walkGraph = func(v pdf.PDFValue) {
		switch val := v.(type) {
		case pdf.PDFDict:
			ptr := pdf.ValuePointer(val.Entries)
			if visitedPtrs[ptr] {
				return
			}
			visitedPtrs[ptr] = true

			if val.Entries["Type"] == (pdf.PDFName{Value: "Page"}) {
				resources, _ := val.Entries["Resources"].(pdf.PDFDict)
				collectContentUsage(ctx, val.Entries["Contents"], resources, reachable, fu)
				collectAnnotAppearanceUsage(ctx, val, reachable, fu)
				return
			}
			for _, child := range val.Entries {
				walkGraph(child)
			}
		case pdf.PDFArray:
			ptr := pdf.ValuePointer(val)
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

// collectAnnotAppearanceUsage marks XObjects reachable via annotation
// appearance streams so validateContentStreams will scan their colour usage.
func collectAnnotAppearanceUsage(ctx *ValidationContext, page pdf.PDFDict, reachable map[uintptr]bool, fu *fontUsage) {
	annots, ok := page.Entries["Annots"].(pdf.PDFArray)
	if !ok {
		return
	}
	for _, item := range annots {
		annot, ok := item.(pdf.PDFDict)
		if !ok {
			continue
		}
		ap, ok := annot.Entries["AP"].(pdf.PDFDict)
		if !ok {
			continue
		}
		collectAPEntryUsage(ctx, ap.Entries["N"], reachable, fu)
	}
}

func collectAPEntryUsage(ctx *ValidationContext, n pdf.PDFValue, reachable map[uintptr]bool, fu *fontUsage) {
	switch v := n.(type) {
	case pdf.PDFDict:
		if v.HasStream {
			apRes, _ := v.Entries["Resources"].(pdf.PDFDict)
			collectContentUsage(ctx, v, apRes, reachable, fu)
		} else {
			for k, sv := range v.Entries {
				if k == "_ref" {
					continue
				}
				if sd, ok := sv.(pdf.PDFDict); ok && sd.HasStream {
					apRes, _ := sd.Entries["Resources"].(pdf.PDFDict)
					collectContentUsage(ctx, sd, apRes, reachable, fu)
				}
			}
		}
	}
}

func collectContentUsage(
	ctx *ValidationContext,
	contents pdf.PDFValue,
	resources pdf.PDFDict,
	reachable map[uintptr]bool,
	fu *fontUsage,
) {
	switch v := contents.(type) {
	case pdf.PDFDict:
		if v.HasStream {
			if data, err := ctx.decodeStreamCached(v); err == nil {
				collectReachableFromBytes(ctx, data, resources, reachable)
				collectFontUsageFromBytes(ctx, data, resources, fu)
			}
		}
	case pdf.PDFArray:
		for _, item := range v {
			if d, ok := item.(pdf.PDFDict); ok && d.HasStream {
				if data, err := ctx.decodeStreamCached(d); err == nil {
					collectReachableFromBytes(ctx, data, resources, reachable)
					collectFontUsageFromBytes(ctx, data, resources, fu)
				}
			}
		}
	}
}

func collectReachableFromBytes(ctx *ValidationContext, data []byte, resources pdf.PDFDict, reachable map[uintptr]bool) {
	xobjects, _ := resources.Entries["XObject"].(pdf.PDFDict)
	cs := pdf.NewContentScanner(data)
	cs.Scan(func(op string, operands []pdf.PDFValue) {
		if op != "Do" || len(operands) == 0 {
			return
		}
		name, ok := operands[len(operands)-1].(pdf.PDFName)
		if !ok || xobjects.Entries == nil {
			return
		}
		xobj, ok := xobjects.Entries[name.Value].(pdf.PDFDict)
		if !ok {
			return
		}
		ptr := pdf.ValuePointer(xobj.Entries)
		if reachable[ptr] {
			return
		}
		reachable[ptr] = true
		if xobj.Entries["Subtype"] == (pdf.PDFName{Value: "Form"}) && xobj.HasStream {
			subResources, _ := xobj.Entries["Resources"].(pdf.PDFDict)
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
func collectFontUsageFromBytes(ctx *ValidationContext, data []byte, resources pdf.PDFDict, fu *fontUsage) {
	fonts, _ := resources.Entries["Font"].(pdf.PDFDict)
	xobjects, _ := resources.Entries["XObject"].(pdf.PDFDict)
	cs := pdf.NewContentScanner(data)
	renderMode := 0
	var modeStack []int
	var currentFontPtrs []uintptr
	var simpleFontPtr uintptr
	haveSimpleFont := false
	var compositeFontPtr uintptr
	haveCompositeFont := false
	cs.Scan(func(op string, operands []pdf.PDFValue) {
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
				if n, ok := operands[len(operands)-1].(pdf.PDFInteger); ok {
					renderMode = int(n)
				}
			}
		case "Tf":
			currentFontPtrs = nil
			haveSimpleFont = false
			haveCompositeFont = false
			if len(operands) >= 2 && fonts.Entries != nil {
				if name, ok := operands[len(operands)-2].(pdf.PDFName); ok {
					if fd, ok := fonts.Entries[name.Value].(pdf.PDFDict); ok {
						currentFontPtrs = append(currentFontPtrs, pdf.ValuePointer(fd.Entries))
						// 6.3.3.2/6.3.5/6.3.6 checks run on the descendant
						// CIDFont dict, not the Type0 font selected by Tf.
						if df, ok := fd.Entries["DescendantFonts"].(pdf.PDFArray); ok && len(df) > 0 {
							if desc, ok := df[0].(pdf.PDFDict); ok {
								currentFontPtrs = append(currentFontPtrs, pdf.ValuePointer(desc.Entries))
								// Only Identity-H/V map codes directly to CIDs;
								// other CMaps leave usage unknown for the font.
								if enc, ok := fd.Entries["Encoding"].(pdf.PDFName); ok &&
									(enc.Value == "Identity-H" || enc.Value == "Identity-V") {
									compositeFontPtr = pdf.ValuePointer(desc.Entries)
									haveCompositeFont = true
								}
							}
						} else {
							// Simple font: codes shown via Tj/TJ are single bytes.
							simpleFontPtr = pdf.ValuePointer(fd.Entries)
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
				for _, b := range ShownStringBytes(op, operands) {
					set[int(b)] = true
				}
			}
			if haveCompositeFont {
				set := fu.usedCIDs[compositeFontPtr]
				if set == nil {
					set = map[int]bool{}
					fu.usedCIDs[compositeFontPtr] = set
				}
				shown := ShownStringBytes(op, operands)
				for i := 0; i+1 < len(shown); i += 2 {
					set[int(shown[i])<<8|int(shown[i+1])] = true
				}
			}
		case "Do":
			if len(operands) == 0 || xobjects.Entries == nil {
				return
			}
			name, ok := operands[len(operands)-1].(pdf.PDFName)
			if !ok {
				return
			}
			xobj, ok := xobjects.Entries[name.Value].(pdf.PDFDict)
			if !ok || xobj.Entries["Subtype"] != (pdf.PDFName{Value: "Form"}) || !xobj.HasStream {
				return
			}
			ptr := pdf.ValuePointer(xobj.Entries)
			if fu.visitedXObj[ptr] {
				return
			}
			fu.visitedXObj[ptr] = true
			subResources, _ := xobj.Entries["Resources"].(pdf.PDFDict)
			if subResources.Entries == nil {
				subResources = resources
			}
			if subData, err := ctx.decodeStreamCached(xobj); err == nil {
				collectFontUsageFromBytes(ctx, subData, subResources, fu)
			}
		}
	})
}

// ShownStringBytes returns the decoded bytes of all string operands a
// text-showing operator passes to the font.
func ShownStringBytes(op string, operands []pdf.PDFValue) []byte {
	var out []byte
	appendOperand := func(v pdf.PDFValue) {
		switch s := v.(type) {
		case pdf.PDFString:
			out = append(out, pdf.DecodePDFLiteralStringBytes(s.Value)...)
		case pdf.PDFHexString:
			out = append(out, pdf.DecodePDFHexStringBytes(s.Value)...)
		}
	}
	switch op {
	case "TJ":
		if len(operands) > 0 {
			if arr, ok := operands[len(operands)-1].(pdf.PDFArray); ok {
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

// validateHexString validates requirements outlined in 6.1.6.
func validateHexString(v pdf.PDFHexString, ctx *ValidationContext) {
	hexCount := 0

	hexErrs := []error{}

	hex := v.Value
	for i := 0; i < len(hex); i++ {
		ch := hex[i]

		if pdf.IsWhitespace(ch) {
			continue
		}

		if !pdf.IsHexDigit(ch) {
			err := fmt.Errorf("contains non-hex character: '%v'", ch)
			hexErrs = append(hexErrs, err)
		}

		hexCount++
	}

	if len(hexErrs) > 0 {
		ctx.ReportErrs(pdf.Checks.Structure.HexStringInvalidChar, v, hexErrs)
	}

	if hexCount%2 != 0 {
		ctx.Report(
			pdf.Checks.Structure.HexStringOddLength,
			v,
			fmt.Sprintf("contains an odd number of hex chars (%d)", hexCount),
		)
	}
}

// validateStreamObject validates requirements outlined in 6.1.7 and 6.1.10.
func validateStreamObject(v pdf.PDFDict, ctx *ValidationContext) {
	if v.Entries["F"] != nil {
		ctx.Report(pdf.Checks.Structure.StreamFileSpec, v, "stream object contains invalid key F")
	}
	if v.Entries["FFilter"] != nil {
		ctx.Report(pdf.Checks.Structure.StreamFileFilter, v, "stream object contains invalid key FFilter")
	}
	if v.Entries["FDecodeParms"] != nil {
		ctx.Report(pdf.Checks.Structure.StreamFileDecodeParams, v, "stream object contains invalid key FDecodeParms")
	}
	for _, f := range pdf.FilterNames(v.Entries["Filter"]) {
		if f == "LZWDecode" || f == "LZW" {
			ctx.Report(pdf.Checks.Structure.StreamLZWFilter, v, "stream object uses forbidden LZWDecode filter")
		}
	}
}

// validateObject validates requirements outlined in 6.1.11
func validateObject(v pdf.PDFDict, ctx *ValidationContext) {
	if v.Entries["EF"] != nil {
		ctx.Report(pdf.Checks.Structure.EmbeddedFileSpec, v, "dictionary shall not contain EF key")
	}
	if v.Entries["EmbeddedFiles"] != nil {
		ctx.Report(pdf.Checks.Structure.EmbeddedFiles, v, "dictionary shall not contain EmbeddedFiles key")
	}
}

// validateArchitecturalLimits validates requirements outlined in 6.1.12
func validateArchitecturalLimits(node pdf.PDFValue, ctx *ValidationContext) {
	switch v := node.(type) {
	case pdf.PDFName:
		// Maximum length of a name, in bytes: 127
		nameLen := len(v.Value)
		if nameLen > 127 {
			ctx.Report(pdf.Checks.Structure.NameTooLong, v, fmt.Sprintf(
				"maximum length of name (127) exceeded: %v",
				nameLen,
			))
		}
	case pdf.PDFInteger:
		// 6.1.12: integer values are limited to the 32-bit signed range.
		if v < -2_147_483_648 || v > 2_147_483_647 {
			ctx.Report(pdf.Checks.Structure.IntegerOutOfRange, v, fmt.Sprintf("integer value exceeded limits: %v", v))
		}
	case pdf.PDFReal:
		// 6.1.12: magnitude of real numbers shall not exceed 32767.
		if v < -32767 || v > 32767 {
			ctx.Report(pdf.Checks.Structure.RealOutOfRange, v, fmt.Sprintf("real number out of range: %g", float64(v)))
		}
	case pdf.PDFString:
		// 6.1.12: maximum length of a string object is 65535 bytes.
		// The parser stores raw (unescaped) bytes; compute the decoded length.
		if PDFStringDecodedLen(v.Value) > 65535 {
			ctx.Report(pdf.Checks.Structure.StringTooLong, v, "string exceeds maximum length of 65535 bytes")
		}
	case pdf.PDFDict:
		// 6.1.12: maximum number of entries in a dictionary is 4095.
		realCount := len(v.Entries)
		if _, has := v.Entries["_ref"]; has {
			realCount--
		}
		if realCount > 4095 {
			ctx.Report(pdf.Checks.Structure.DictTooLarge, v, fmt.Sprintf(
				"dictionary exceeds 4095 entries: %d",
				realCount,
			))
		}
	}
}

// PDFStringDecodedLen returns the decoded byte length of a PDF literal string
// that the lexer stored as raw (unescaped) bytes (backslash sequences intact).
func PDFStringDecodedLen(s string) int {
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
func verifyOptionalContent(d *pdf.Reader) []pdf.PDFError {
	_, err := d.ResolveGraphByPath([]string{"Root", "OCProperties"})
	if err == nil {
		return []pdf.PDFError{pdf.NewError(
			pdf.Checks.Structure.OptionalContent,
			[]error{fmt.Errorf("OCProperties not allowed in document catalog")},
			0,
			nil,
		)}
	}
	return nil
}

// 6.2 Graphics

// verifyOutputIntent verifies requirements outlined in 6.2.2
func verifyOutputIntent(d *pdf.Reader) []pdf.PDFError {
	values, err := d.ResolveGraphByPath([]string{"Root", "OutputIntents"})
	if err != nil || values == nil {
		// OutputIntents are optional.
		return nil
	}

	intents, ok := values.(pdf.PDFArray)
	if !ok {
		return []pdf.PDFError{pdf.NewError(
			pdf.Checks.Colour.OutputIntentNotArray,
			[]error{fmt.Errorf("OutputIntents object is not an array")},
			0,
			nil,
		)}
	}

	errs := []pdf.PDFError{}

	var indirectObject pdf.PDFValue

	for _, v := range intents {
		intent, ok := v.(pdf.PDFDict)
		if !ok {
			err := pdf.NewError(
				pdf.Checks.Colour.OutputIntentNotDict,
				[]error{fmt.Errorf("expected OutputIntent to be a pdf.PDFDict")},
				0,
				nil,
			)
			errs = append(errs, err)
			continue
		}
		s, ok := intent.Entries["S"].(pdf.PDFName)
		if !ok {
			err := pdf.NewError(
				pdf.Checks.Colour.OutputIntentInvalidS,
				[]error{fmt.Errorf("expected S to be a pdf.PDFName")},
				0,
				nil,
			)
			errs = append(errs, err)
			continue
		}

		if s.Value != "GTS_PDFA1" {
			err := pdf.NewError(
				pdf.Checks.Colour.OutputIntentWrongS,
				[]error{fmt.Errorf("expected S was not GTS_PDFA1, but %v", intent.Entries["S"])},
				0,
				nil,
			)
			errs = append(errs, err)
		}

		if intent.Entries["OutputConditionIdentifier"] == nil {
			err := pdf.NewError(
				pdf.Checks.Colour.OutputIntentMissingIdentifier,
				[]error{fmt.Errorf("OutputConditionIdentifier is required but was nil")},
				0,
				nil,
			)
			errs = append(errs, err)
			continue
		}

		destOutputProfile := intent.Entries["DestOutputProfile"]
		if destOutputProfile == nil {
			// 6.2.2: DestOutputProfile shall be present unless OutputConditionIdentifier
			// names a standard ICC registry profile, which is not the case for "Custom".
			errs = append(errs, pdf.NewError(
				pdf.Checks.Colour.OutputIntentUnresolvedProfile,
				[]error{fmt.Errorf("DestOutputProfile is required when OutputConditionIdentifier does not specify a standard production condition")},
				0,
				nil,
			))
			continue
		}

		// If a file's OutputIntents array contains more than one entry, then all entries that contain a DestOutputProfile
		// key shall have as the value of that key the same indirect object, which shall be a valid ICC profile stream.
		if indirectObject == nil {
			indirectObject = destOutputProfile
		} else {
			if !pdf.EqualPDFValue(indirectObject, destOutputProfile) {
				err := pdf.NewError(
					pdf.Checks.Colour.OutputIntentMultipleProfiles,
					[]error{fmt.Errorf("expected DestOutputProfile to be %v but was %v", indirectObject, destOutputProfile)},
					0,
					nil,
				)
				errs = append(errs, err)
				continue
			}
		}

		profile, err := d.ResolveObject(destOutputProfile)
		if err != nil {
			err := pdf.NewError(
				pdf.Checks.Colour.OutputIntentUnresolvedProfile,
				[]error{fmt.Errorf("unable to resolve DestOutputProfile: %v", err)},
				0,
				nil,
			)
			errs = append(errs, err)
			continue
		}

		profileMap, ok := profile.(pdf.PDFDict)
		if !ok {
			err := pdf.NewError(
				pdf.Checks.Colour.OutputIntentInvalidProfile,
				[]error{fmt.Errorf("unexpected format for DestOutputProfile encountered")},
				0,
				nil,
			)
			errs = append(errs, err)
			continue
		}

		nValue, ok := profileMap.Entries["N"].(pdf.PDFInteger)
		if !ok {
			err := pdf.NewError(
				pdf.Checks.Colour.OutputIntentMissingN,
				[]error{fmt.Errorf("could not retrieve number of colour components N")},
				0,
				nil,
			)
			errs = append(errs, err)
			continue
		}

		// N shall be 1, 3, or 4
		if !slices.Contains([]int{1, 3, 4}, int(nValue)) {
			err := pdf.NewError(
				pdf.Checks.Colour.OutputIntentInvalidN,
				[]error{fmt.Errorf("number of colour components N must be 1, 3, or 4")},
				0,
				nil,
			)
			errs = append(errs, err)
		}

		// 6.2.2: the ICC profile stream shall be a valid ICC.1:2003-09 profile (version ≤ 2.x).
		if profileMap.HasStream {
			if iccErr := ValidateICCProfileStream(profileMap); iccErr != nil {
				errs = append(errs, *iccErr)
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

// ValidateICCProfileStream checks that a DestOutputProfile stream is a valid
// ICC profile version 2.x as required by PDF/A-1 (6.2.2, 6.2.3 / ICC.1:2003-09).
func ValidateICCProfileStream(dict pdf.PDFDict) *pdf.PDFError {
	data, err := pdf.DecodeStream(dict)
	if err != nil {
		newErr := pdf.NewError(pdf.Checks.Colour.OutputIntentICCVersion, []error{fmt.Errorf("cannot decode ICC profile stream: %v", err)}, 0, nil)
		return &newErr
	}

	if len(data) < 128 {
		newErr := pdf.NewError(pdf.Checks.Colour.OutputIntentICCVersion, []error{fmt.Errorf("ICC profile too short (%d bytes)", len(data))}, 0, nil)
		return &newErr
	}

	// ICC signature ("acsp") at offset 36.
	if string(data[36:40]) != "acsp" {
		newErr := pdf.NewError(pdf.Checks.Colour.OutputIntentICCVersion, []error{fmt.Errorf("ICC profile missing 'acsp' signature at offset 36")}, 0, nil)
		return &newErr
	}

	// Version must be < 3.0 (PDF/A-1 permits ICC v2.x only).
	major := data[8]
	if major >= 3 {
		newErr := pdf.NewError(pdf.Checks.Colour.OutputIntentICCVersion, []error{fmt.Errorf("ICC profile version %d.x not allowed in PDF/A-1 (must be < 3.0)", major)}, 0, nil)
		return &newErr
	}

	// Device class must be one of:
	// prtr (output), mntr (display), scnr (input), spac (colorspace conversion).
	deviceClass := string(data[12:16])
	if !iccValidDeviceClasses[deviceClass] {
		newErr := pdf.NewError(pdf.Checks.Colour.OutputIntentICCVersion, []error{fmt.Errorf("ICC profile has invalid deviceClass %q", deviceClass)}, 0, nil)
		return &newErr
	}

	// Color space must be one of the PDF/A-1 permitted spaces.
	colorSpace := string(data[16:20])
	if !iccValidColorSpaces[colorSpace] {
		newErr := pdf.NewError(pdf.Checks.Colour.OutputIntentICCVersion, []error{fmt.Errorf("ICC profile has invalid colorSpace %q", colorSpace)}, 0, nil)
		return &newErr
	}

	nObj := dict.Entries["N"]
	if nObj == nil {
		newErr := pdf.NewError(pdf.Checks.Colour.ICCBasedComponentsMismatch, []error{fmt.Errorf("ICC profile stream missing required /N entry")}, 0, nil)
		return &newErr
	}

	n, ok := nObj.(pdf.PDFInteger)
	if !ok {
		newErr := pdf.NewError(pdf.Checks.Colour.ICCBasedComponentsMismatch, []error{fmt.Errorf("ICC profile stream /N must be an integer")}, 0, nil)
		return &newErr
	}

	switch {
	case n == 1 && colorSpace == "GRAY":
		// OK
	case n == 3 && (colorSpace == "RGB " || colorSpace == "Lab "):
		// OK
	case n == 4 && colorSpace == "CMYK":
		// OK
	default:
		newErr := pdf.NewError(pdf.Checks.Colour.ICCBasedComponentsMismatch, []error{fmt.Errorf("ICC profile /N=%d does not match profile colorSpace %q", n, colorSpace)}, 0, nil)
		return &newErr
	}

	return nil
}

// trailerIDRe finds the first hex string in any /ID array in the file.
var trailerIDRe = regexp.MustCompile(`/ID\s*\[<([0-9A-Fa-f]+)>`)

// checkLinearizedFileID detects the 6.1.3 violation where a linearized PDF's
// first-page and overflow trailers carry different ID[0] values. Applies only
// when the main trailer is minimal (lacks /Root), the mark of a linearized overflow section.
func checkLinearizedFileID(d *pdf.Reader) []pdf.PDFError {
	// A main trailer with /Root is either an ordinary PDF or an
	// incrementally-updated one; cross-trailer ID consistency does not apply.
	if d.Trailer().Entries["Root"] != nil {
		return nil
	}
	size := d.Size()
	raw := make([]byte, size)
	if _, err := d.ReadAt(raw, 0); err != nil {
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
			return []pdf.PDFError{pdf.NewError(
				pdf.Checks.Structure.TrailerID,
				[]error{fmt.Errorf("linearized PDF: ID[0] (%s) differs from %s in another trailer", first, id)},
				0,
				nil,
			)}
		}
	}
	return nil
}
