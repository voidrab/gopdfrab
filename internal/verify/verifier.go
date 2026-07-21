package verify

import (
	"bytes"
	"fmt"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// xrefHeaderRe matches a well-formed cross-reference subsection header
// ("start count" separated by a single space, no leading white space).
var xrefHeaderRe = regexp.MustCompile(`^[0-9]+ [0-9]+$`)

// maxWalkDepth caps the graph walk's recursion. Var, not const, only so tests
// can lower it.
var maxWalkDepth = 1 << 17

// Verify verifies d against the checks enabled in profile p.
func Verify(d *pdf.Reader, p *pdf.Profile) (pdf.Result, error) {
	if p == nil {
		return pdf.Result{}, fmt.Errorf("nil profile")
	}
	if p.Level == pdf.Undefined {
		return pdf.Result{Type: p.Level, Valid: false}, fmt.Errorf("cannot verify PDF to undefined conformance level")
	}

	var issues []pdf.PDFError
	if p.Level == pdf.A_1B || p.Level == pdf.ObjectModel {
		issues = verifyPdfA1b(d, p)
	}
	issues = filterByProfile(issues, p)

	if len(issues) > 0 {
		return pdf.Result{Type: p.Level, Valid: false, Issues: issues}, nil
	}
	return pdf.Result{Type: p.Level, Valid: true}, nil
}

// Parts splits the A-1b issue list by what the checks read. PreStructural
// and PostStructural are byte-level checks against the reader's file and
// xref bytes, in exactly the positions verifyPdfA1b emits them (the header/
// trailer/xref checks lead the list; object framing and the reader's parse
// diagnostics trail it). Graph is everything in between: a function of the
// resolved object graph and its streams only, so on a seeded reader it is a
// deterministic replay of that same graph's previous verification.
type Parts struct {
	PreStructural  []pdf.PDFError
	Graph          []pdf.PDFError
	PostStructural []pdf.PDFError
}

// Issues reassembles the parts in verifyPdfA1b's emission order.
func (pt Parts) Issues() []pdf.PDFError {
	out := make([]pdf.PDFError, 0, len(pt.PreStructural)+len(pt.Graph)+len(pt.PostStructural))
	out = append(out, pt.PreStructural...)
	out = append(out, pt.Graph...)
	out = append(out, pt.PostStructural...)
	return out
}

// filter applies filterByProfile to each part.
func (pt Parts) filter(p *pdf.Profile) Parts {
	return Parts{
		PreStructural:  filterByProfile(pt.PreStructural, p),
		Graph:          filterByProfile(pt.Graph, p),
		PostStructural: filterByProfile(pt.PostStructural, p),
	}
}

// VerifyParts is Verify with the issue list split into Parts, each part
// profile-filtered. Convert's fix loop uses it so its final output
// verification can reuse the last in-loop graph verdicts (see
// serializeAndVerify) while re-running only the byte-level checks.
func VerifyParts(d *pdf.Reader, p *pdf.Profile) (Parts, error) {
	if p == nil {
		return Parts{}, fmt.Errorf("nil profile")
	}
	if p.Level == pdf.Undefined {
		return Parts{}, fmt.Errorf("cannot verify PDF to undefined conformance level")
	}
	var pt Parts
	if p.Level == pdf.A_1B || p.Level == pdf.ObjectModel {
		pt = verifyPdfA1bParts(d, p)
	}
	return pt.filter(p), nil
}

// VerifyStructural runs only the byte-level structural checks against d --
// the leading and trailing families of Parts -- skipping every graph check.
// It exists for convert's final output verification: when the serialized
// graph is byte-for-byte the graph the last in-loop verify already checked,
// only these byte-level checks can produce new findings against the output.
func VerifyStructural(d *pdf.Reader, p *pdf.Profile) (Parts, error) {
	if p == nil {
		return Parts{}, fmt.Errorf("nil profile")
	}
	if p.Level == pdf.Undefined {
		return Parts{}, fmt.Errorf("cannot verify PDF to undefined conformance level")
	}
	var pt Parts
	if (p.Level == pdf.A_1B || p.Level == pdf.ObjectModel) && !p.OnlyObjectModelChecks() {
		pt.PreStructural = structuralPreIssues(d)
		pt.PostStructural = structuralPostIssues(d)
	}
	return pt.filter(p), nil
}

// ResultFromIssues builds the Result Verify would return for an
// already-filtered issue list.
func ResultFromIssues(p *pdf.Profile, issues []pdf.PDFError) pdf.Result {
	if len(issues) > 0 {
		return pdf.Result{Type: p.Level, Valid: false, Issues: issues}
	}
	return pdf.Result{Type: p.Level, Valid: true}
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

// VerifyObjectModel checks d against the generic ISO 32000 object-model
// checks only, independent of any PDF/A conformance level.
func VerifyObjectModel(d *pdf.Reader) (pdf.Result, error) {
	return Verify(d, pdf.ObjectModelOnly())
}

// VerifyObjectModelFile opens, checks, and closes a single file against the
// generic ISO 32000 object-model checks only.
func VerifyObjectModelFile(path string) (pdf.Result, error) {
	doc, err := pdf.Open(path)
	if err != nil {
		return pdf.Result{}, err
	}
	defer doc.Close()
	return VerifyObjectModel(doc)
}

// VerifyObjectModelBytes is VerifyObjectModelFile for an in-memory PDF.
func VerifyObjectModelBytes(data []byte) (pdf.Result, error) {
	doc, err := pdf.OpenBytes(data)
	if err != nil {
		return pdf.Result{}, fmt.Errorf("verify: %w", err)
	}
	defer doc.Close()
	return VerifyObjectModel(doc)
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
	return verifyPdfA1bParts(d, p).Issues()
}

// structuralPreIssues runs the byte-level checks that lead the A-1b issue
// list: file header, linearized ID consistency, trailer, and xref format.
func structuralPreIssues(d *pdf.Reader) []pdf.PDFError {
	issues := []pdf.PDFError{}
	issues = append(issues, verifyFileHeader(d)...)
	issues = append(issues, checkLinearizedFileID(d)...)
	issues = append(issues, verifyFileTrailer(d)...)
	issues = append(issues, verifyCrossReferenceTable(d)...)
	return issues
}

// structuralPostIssues runs the byte-level checks that trail the A-1b issue
// list: object framing and the reader's accumulated parse diagnostics.
func structuralPostIssues(d *pdf.Reader) []pdf.PDFError {
	issues := verifyAllObjectFraming(d)
	return append(issues, d.StructErrors()...)
}

func verifyPdfA1bParts(d *pdf.Reader, p *pdf.Profile) Parts {
	// A profile enabling nothing but the object-model checks skips every
	// PDF/A-specific family up front instead of filtering its findings away
	// at the end, so VerifyObjectModel never decodes content streams, parses
	// font programs, or validates XMP.
	schemaOnly := p.OnlyObjectModelChecks()
	var pt Parts

	if !schemaOnly {
		pt.PreStructural = structuralPreIssues(d)
	}

	issues := []pdf.PDFError{}

	// Resolve the graph once up front; all subsequent checks work on the
	// resolved graph so no per-check lazy resolve occurs.
	graph, err := d.ResolveGraph()
	if err != nil {
		pt.Graph = append(issues, pdf.NewError(pdf.Checks.Structure.GraphResolutionFailure, []error{err}, 0, nil))
		return pt
	}

	if !schemaOnly {
		errs := verifyDocumentInformationDictionary(graph)
		if errs != nil {
			issues = append(issues, errs...)
		}
	}

	pageIndex, err := d.BuildPageIndex(graph)
	if err != nil {
		pt.Graph = append(issues, pdf.NewError(pdf.Checks.Structure.GraphResolutionFailure, []error{err}, 0, nil))
		return pt
	}

	ctx := &ValidationContext{
		PageIndex:  pageIndex,
		reader:     d,
		schemaOnly: schemaOnly,
	}
	if !schemaOnly {
		reachable, invisibleOnly, usedCodes, usedCIDs, complete := ComputeContentUsage(graph, ctx)
		// Every usage-driven optimisation is a suppression: skip unreachable
		// Form XObjects (6.2.3.3, 6.2.10), skip simple fonts never shown
		// (6.3.4), exempt invisible-only fonts and unused CIDs (6.3.3.2,
		// 6.3.5, 6.3.6). If a content stream failed to decode the usage sets
		// are a subset of the truth, so any of those suppressions could hide
		// a real violation. Discard them all and check everything.
		if !complete {
			reachable, invisibleOnly, usedCodes, usedCIDs = nil, nil, nil, nil
		}
		if p.SkipUnreachableXObjects {
			ctx.ReachableXObjectPtrs = reachable
		}
		// Forced off when usage is unknown: simpleFontShown is false for every
		// font once UsedCharCodes is nil, so leaving the flag on would suppress
		// *all* 6.3.4 checks -- the opposite of failing safe. A nil
		// ReachableXObjectPtrs or UsedCharCodes fails safe on its own; this
		// one inverts.
		ctx.SkipUnusedSimpleFonts = p.SkipUnusedSimpleFonts && complete
		ctx.InvisibleOnlyFontPtrs, ctx.UsedCharCodes, ctx.UsedCIDs = invisibleOnly, usedCodes, usedCIDs
		computeColourCoverage(d, ctx)
	}

	verifyDocument(graph, ctx)
	issues = append(issues, ctx.errs...)
	if schemaOnly {
		pt.Graph = issues
		return pt
	}
	errs := verifyOptionalContent(d)
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
	errs = checkNonCatalogXMPStreams(graph, ctx)
	if errs != nil {
		issues = append(issues, errs...)
	}
	pt.Graph = issues

	pt.PostStructural = structuralPostIssues(d)
	return pt
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

	// One bounded tail read replaces the former ten 1-byte ReadAts: the
	// marker must start within the file's last 9 bytes, leaving room for
	// up to four trailing bytes (the historical tolerance). tail[9] maps
	// past end-of-file and stays zero, preserving the exact diagnostic
	// bytes the old byte-by-byte prepend loop reported.
	var tail [10]byte
	d.ReadAt(tail[:], size-9)
	if !bytes.Contains(tail[:9], []byte("%%EOF")) {
		err := pdf.NewError(
			pdf.Checks.Structure.TrailerEOF,
			[]error{fmt.Errorf("no EOF marker found: %v", string(tail[:]))},
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
	cur := pdf.NewCursor(d.WindowAt(offset+d.PDFStart(), 8192))

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

		// The header already matched xrefHeaderRe, so the count is the
		// digits after the single space; Atoi's overflow behavior (0)
		// matches the Sscanf this replaces.
		_, countStr, _ := strings.Cut(line, " ")
		count, _ := strconv.Atoi(countStr)
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
		case nil:
			// A null value is equivalent to the entry being absent.
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
			s = pdf.DecodePDFTextString([]byte(tv.Value))
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

	// typedVisit dedupes schema validation per (node, Arlington type): a node shared
	// between differently-typed paths is re-descended once per new type, so schema
	// coverage does not depend on map iteration order, while every per-node PDF/A
	// check still runs exactly once (on the first visit).
	type typedVisit struct {
		ptr uintptr
		typ string
	}
	visitedTyped := make(map[typedVisit]bool)

	// owner is the nearest enclosing dict, threaded through arrays, so
	// scalar-limit violations are reported against an object fixers can
	// resolve by ref instead of the bare scalar. ownerKey is owner's entry
	// holding node ("" once an array descent makes node not directly
	// addressable). expectedType is the Arlington type name this node should
	// conform to (per the parent key's Link), or "" once the descent has lost
	// track of it; see arlingtonChildType/arlingtonElementType.
	var walk func(node any, owner pdf.PDFValue, ownerKey, expectedType string, depth int)

	walk = func(node any, owner pdf.PDFValue, ownerKey, expectedType string, depth int) {
		if node == nil {
			return
		}
		if depth > maxWalkDepth {
			return
		}

		switch v := node.(type) {
		case pdf.PDFDict:
			// A subtree that lost its schema type can re-anchor when the dict's own
			// /Type (+/Subtype) names identify exactly one Arlington type.
			if expectedType == "" {
				expectedType = selfIdentifiedType(v)
			}
			ptr := pdf.ValuePointer(v.Entries)
			first := !visited[ptr]
			if !first && (expectedType == "" || visitedTyped[typedVisit{ptr, expectedType}]) {
				return
			}
			visited[ptr] = true
			if expectedType != "" {
				visitedTyped[typedVisit{ptr, expectedType}] = true
			}

			if (v.Entries["Type"] == pdf.PDFName{Value: "Page"}) {
				if ref, ok := v.Entries["_ref"].(pdf.PDFRef); ok {
					ctx.CurrentPage = ctx.PageIndex[ref.ObjNum]
				}
			}

			if first && !ctx.schemaOnly {
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
			}
			if expectedType != "" {
				validateAgainstSchema(v, expectedType, ctx)
			}

			keysBase := len(ctx.keyScratch)
			for _, k := range ctx.sortedKeys(v.Entries) {
				val := v.Entries[k]
				if first && !ctx.schemaOnly {
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
				}
				if (!first || ctx.schemaOnly) && !isContainer(val) {
					// Scalars carry no schema, and on a re-descent they were
					// already fully checked on the first visit.
					continue
				}
				var childType string
				if expectedType != "" && k != "_ref" {
					childType = arlingtonChildType(expectedType, k, val)
				}
				// Pass node (v already boxed) to avoid re-boxing v per call.
				walk(val, node, k, childType, depth+1)
			}
			ctx.keyScratch = ctx.keyScratch[:keysBase]
			if !first {
				return
			}

		case pdf.PDFArray:
			ptr := pdf.ValuePointer(v)
			first := !visited[ptr]
			if !first && (expectedType == "" || visitedTyped[typedVisit{ptr, expectedType}]) {
				return
			}
			visited[ptr] = true
			if expectedType != "" {
				visitedTyped[typedVisit{ptr, expectedType}] = true
			}

			if first && !ctx.schemaOnly {
				validateColourSpaceArray(v, ctx)

				// 6.1.12: maximum number of elements in an array is 8191.
				if len(v) > 8191 {
					ctx.Report(
						pdf.Checks.Structure.ArrayTooLarge,
						v,
						fmt.Sprintf("array exceeds 8191 elements: %d", len(v)),
					)
				}
			}
			if expectedType != "" {
				validateArrayAgainstSchema(v, expectedType, owner, ownerKey, ctx)
			}

			for _, item := range v {
				if (!first || ctx.schemaOnly) && !isContainer(item) {
					continue
				}
				var elemType string
				if expectedType != "" {
					elemType = arlingtonElementType(expectedType, item)
				}
				walk(item, owner, "", elemType, depth+1)
			}
			if !first {
				return
			}

		case pdf.PDFHexString:
			if !ctx.schemaOnly {
				// Hexadecimal strings shall contain an even number of non-white-space
				// characters, each in the range 0 to 9, A to F or a to f.
				validateHexString(v, owner, ctx)
			}
		}

		if !ctx.schemaOnly {
			validateArchitecturalLimits(node, owner, ctx)
		}
	}

	walk(graph, nil, "", "FileTrailer", 0)
}

// sortedKeys appends m's keys in sorted order to ctx.keyScratch and returns
// the appended segment, so the walk visits entries deterministically
// regardless of Go map iteration order without allocating per dict. The
// caller must truncate ctx.keyScratch back to its prior length when done
// with the segment; the segment stays readable even if deeper recursion
// grows the scratch onto a new backing array, since it is never written
// again after the sort.
func (ctx *ValidationContext) sortedKeys(m map[string]pdf.PDFValue) []string {
	base := len(ctx.keyScratch)
	for k := range m {
		ctx.keyScratch = append(ctx.keyScratch, k)
	}
	keys := ctx.keyScratch[base:]
	slices.Sort(keys)
	return keys
}

// isContainer reports whether val is a dict or array, i.e. a node that can
// carry an Arlington type on a re-descent.
func isContainer(val pdf.PDFValue) bool {
	switch val.(type) {
	case pdf.PDFDict, pdf.PDFArray:
		return true
	}
	return false
}

// ComputeContentUsage walks the resolved graph once, decoding each page's
// content stream (and any Form XObjects it invokes) at most once via ctx's
// decode cache, and computes two things checks need:
//   - reachable: Entries-map pointers of Form XObjects invoked (via Do) from
//     page content or other reachable Form XObjects.
//   - invisibleOnly, usedCodes, usedCIDs: font usage, as computed by
//     collectFontUsageFromBytes.
//
// complete is false when any content stream failed to decode, meaning the
// sets are a subset of the truth. Since every one of them drives a check
// *suppression*, callers must discard them all in that case.
func ComputeContentUsage(graph pdf.PDFValue, ctx *ValidationContext) (
	reachable map[uintptr]bool,
	invisibleOnly map[uintptr]bool,
	usedCodes, usedCIDs map[uintptr]map[int]bool,
	complete bool,
) {
	complete = true
	reachable = map[uintptr]bool{}
	fu := &fontUsage{
		visible:   map[uintptr]bool{},
		invisible: map[uintptr]bool{},
		usedCodes: map[uintptr]map[int]bool{},
		usedCIDs:  map[uintptr]map[int]bool{},
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
				if !collectContentUsage(ctx, val.Entries["Contents"], resources, reachable, fu) {
					complete = false
				}
				if !collectAnnotAppearanceUsage(ctx, val, reachable, fu) {
					complete = false
				}
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
	return reachable, invisibleOnly, fu.usedCodes, fu.usedCIDs, complete
}

// collectAnnotAppearanceUsage marks XObjects reachable via annotation
// appearance streams so validateContentStreams will scan their colour usage.
func collectAnnotAppearanceUsage(ctx *ValidationContext, page pdf.PDFDict, reachable map[uintptr]bool, fu *fontUsage) bool {
	annots, ok := page.Entries["Annots"].(pdf.PDFArray)
	if !ok {
		return true
	}
	complete := true
	for _, item := range annots {
		annot, ok := item.(pdf.PDFDict)
		if !ok {
			continue
		}
		ap, ok := annot.Entries["AP"].(pdf.PDFDict)
		if !ok {
			continue
		}
		if !collectAPEntryUsage(ctx, ap.Entries["N"], reachable, fu) {
			complete = false
		}
	}
	return complete
}

func collectAPEntryUsage(ctx *ValidationContext, n pdf.PDFValue, reachable map[uintptr]bool, fu *fontUsage) bool {
	complete := true
	switch v := n.(type) {
	case pdf.PDFDict:
		if v.HasStream {
			apRes, _ := v.Entries["Resources"].(pdf.PDFDict)
			complete = collectContentUsage(ctx, v, apRes, reachable, fu)
		} else {
			for k, sv := range v.Entries {
				if k == "_ref" {
					continue
				}
				if sd, ok := sv.(pdf.PDFDict); ok && sd.HasStream {
					apRes, _ := sd.Entries["Resources"].(pdf.PDFDict)
					if !collectContentUsage(ctx, sd, apRes, reachable, fu) {
						complete = false
					}
				}
			}
		}
	}
	return complete
}

// collectContentUsage returns false when any stream it visited could not be
// decoded, leaving the usage sets incomplete.
func collectContentUsage(
	ctx *ValidationContext,
	contents pdf.PDFValue,
	resources pdf.PDFDict,
	reachable map[uintptr]bool,
	fu *fontUsage,
) bool {
	complete := true
	switch v := contents.(type) {
	case pdf.PDFDict:
		if v.HasStream {
			complete = collectUsageFromBytes(ctx, v, resources, reachable, fu)
		}
	case pdf.PDFArray:
		for _, item := range v {
			if d, ok := item.(pdf.PDFDict); ok && d.HasStream {
				if !collectUsageFromBytes(ctx, d, resources, reachable, fu) {
					complete = false
				}
			}
		}
	}
	return complete
}

// fontUsage tracks visible vs. invisible-only rendering per font, plus the
// character codes (simple fonts) and CIDs (Identity-H/V fonts) actually shown.
type fontUsage struct {
	visible   map[uintptr]bool
	invisible map[uintptr]bool
	usedCodes map[uintptr]map[int]bool
	usedCIDs  map[uintptr]map[int]bool
}

// collectUsageFromBytes scans dict's content stream exactly once, tracking
// both Form XObject reachability (via Do) and font usage/visibility (render
// mode, saved/restored across q/Q, and the font set by the most recent Tf) --
// these were previously two independent scans over the same bytes
// (collectReachableFromBytes/collectFontUsageFromBytes), each recursing into
// the same Do-invoked Form XObjects on its own. reachable doubles as the
// single recursion guard for both concerns. Scanning through
// ctx.scanStreamCached (rather than a fresh ContentScanner) means an
// unchanged stream is tokenized once across all of convert's fixer
// iterations, not once per iteration.
// Returns false when dict's stream could not be decoded: the usage sets it
// would have contributed to are then incomplete, which the caller must
// propagate (see ComputeContentUsage).
func collectUsageFromBytes(ctx *ValidationContext, dict pdf.PDFDict, resources pdf.PDFDict, reachable map[uintptr]bool, fu *fontUsage) (ok bool) {
	ops, err := ctx.scanStreamCached(dict)
	if err != nil {
		return false // reported as StreamUndecodable by scanStreamCached
	}
	complete := true
	fonts, _ := resources.Entries["Font"].(pdf.PDFDict)
	xobjects, _ := resources.Entries["XObject"].(pdf.PDFDict)
	renderMode := 0
	var modeStack []int
	var currentFontPtrs []uintptr
	var simpleFontPtr uintptr
	haveSimpleFont := false
	var compositeFontPtr uintptr
	haveCompositeFont := false
	pdf.ReplayOps(ops, func(op string, operands []pdf.PDFValue) {
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
			if !ok {
				return
			}
			ptr := pdf.ValuePointer(xobj.Entries)
			alreadyReachable := reachable[ptr]
			reachable[ptr] = true
			if alreadyReachable || xobj.Entries["Subtype"] != (pdf.PDFName{Value: "Form"}) || !xobj.HasStream {
				return
			}
			subResources, _ := xobj.Entries["Resources"].(pdf.PDFDict)
			if subResources.Entries == nil {
				subResources = resources
			}
			if !collectUsageFromBytes(ctx, xobj, subResources, reachable, fu) {
				complete = false
			}
		}
	})
	return complete
}

// ShownStringBytes returns the decoded bytes of all string operands a
// text-showing operator passes to the font.
func ShownStringBytes(op string, operands []pdf.PDFValue) []byte {
	var out []byte
	appendOperand := func(v pdf.PDFValue) {
		switch s := v.(type) {
		case pdf.PDFString:
			out = append(out, s.Value...)
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

// validateHexString validates requirements outlined in 6.1.6, reporting
// against owner (the enclosing dict or content stream) so fixers can resolve
// the violation by ref.
func validateHexString(v pdf.PDFHexString, owner pdf.PDFValue, ctx *ValidationContext) {
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
		ctx.ReportErrs(pdf.Checks.Structure.HexStringInvalidChar, owner, hexErrs)
	}

	if hexCount%2 != 0 {
		ctx.Report(
			pdf.Checks.Structure.HexStringOddLength,
			owner,
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
	if pdf.HasFilter(v.Entries["Filter"], pdf.FilterLZW) {
		ctx.Report(pdf.Checks.Structure.StreamLZWFilter, v, "stream object uses forbidden LZWDecode filter")
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

// validateArchitecturalLimits validates requirements outlined in 6.1.12.
// Scalar violations are reported against owner (the nearest enclosing dict)
// so fixers can resolve them by ref; composite violations carry their own
// identity and report against themselves.
func validateArchitecturalLimits(node pdf.PDFValue, owner pdf.PDFValue, ctx *ValidationContext) {
	switch v := node.(type) {
	case pdf.PDFName:
		// Maximum length of a name, in bytes: 127
		nameLen := len(v.Value)
		if nameLen > 127 {
			ctx.Report(pdf.Checks.Structure.NameTooLong, owner, fmt.Sprintf(
				"maximum length of name (127) exceeded: %v",
				nameLen,
			))
		}
	case pdf.PDFInteger:
		// 6.1.12: integer values are limited to the 32-bit signed range.
		if v < -2_147_483_648 || v > 2_147_483_647 {
			ctx.Report(pdf.Checks.Structure.IntegerOutOfRange, owner, fmt.Sprintf("integer value exceeded limits: %v", v))
		}
	case pdf.PDFReal:
		// 6.1.12: magnitude of real numbers shall not exceed 32767.
		if v < -32767 || v > 32767 {
			ctx.Report(pdf.Checks.Structure.RealOutOfRange, owner, fmt.Sprintf("real number out of range: %g", float64(v)))
		}
	case pdf.PDFString:
		// 6.1.12: maximum length of a string object is 65535 bytes.
		if len(v.Value) > 65535 {
			ctx.Report(pdf.Checks.Structure.StringTooLong, owner, "string exceeds maximum length of 65535 bytes")
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
	raw, err := d.FullBytes()
	if err != nil {
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
