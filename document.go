package pdfrab

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Document represents a PDF file.
type Document struct {
	file       *os.File
	info       os.FileInfo
	header     []byte
	trailer    PDFDict
	xrefTable  map[int]int64
	xrefOffset int64
	// pdfStart is the byte offset of the "%PDF-" header. Non-zero when the
	// file begins with garbage bytes before the PDF header (6.1.2).
	pdfStart int64

	// firstPageTrailer holds the linearized first-page trailer (with /Root,
	// /Info, /ID) when the main trailer lacks /Root. See effectiveTrailer().
	firstPageTrailer PDFDict

	// structErrs collects document-structure violations (e.g. 6.1.8 object
	// framing) found lazily during resolution; framingChecked dedupes per object.
	structErrs     []PDFError
	framingChecked map[int]bool
	streamChecked  map[int]bool

	objCache map[int]PDFValue

	// compressedXref maps an object number to its location inside a
	// compressed object stream (xref type 2, PDF 1.5+), for objects not
	// found in xrefTable. See xrefstream.go / objstm.go.
	compressedXref map[int]compressedXrefEntry
	// objStmCache memoizes the decoded contents of an object stream, keyed
	// by the object stream's own object number.
	objStmCache map[int][]objStmEntry

	resolvedPtrs  map[uintptr]bool
	resolvedGraph PDFValue
	graphResolved bool
}

// recordStreamFraming records a 6.1.7 stream-framing violation, deduplicated per
// object number and check.
func (d *Document) recordStreamFraming(objNum int, c Check, msg string) {
	if d.streamChecked == nil {
		d.streamChecked = map[int]bool{}
	}
	key := objNum*1000 + c.subclause
	if d.streamChecked[key] {
		return
	}
	d.streamChecked[key] = true
	d.structErrs = append(d.structErrs, PDFError{
		check: c, errs: []error{errors.New(msg)}, page: 0,
	})
}

// recordFraming records 6.1.8 object-framing violations for an object, at most
// once per object number.
func (d *Document) recordFraming(objNum int, errs []error) {
	if len(errs) == 0 {
		return
	}
	if d.framingChecked == nil {
		d.framingChecked = map[int]bool{}
	}
	if d.framingChecked[objNum] {
		return
	}
	d.framingChecked[objNum] = true
	d.structErrs = append(d.structErrs, PDFError{
		check: Checks.Structure.ObjectFraming,
		errs:  errs,
		page:  0,
	})
}

// Open initializes the PDF document at path.
func Open(path string) (*Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	header := make([]byte, 8)
	if _, err := f.ReadAt(header, 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to read header: %w", err)
	}

	doc := &Document{
		file:   f,
		info:   info,
		header: header,
	}

	if err := doc.initializeStructure(); err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to parse structure: %w", err)
	}

	return doc, nil
}

// initializeStructure locates startxref, then parses the xref table and trailer.
func (d *Document) initializeStructure() error {
	// Detect garbage bytes preceding the %PDF- marker (6.1.2).
	// xref offsets in such files are relative to the PDF content start.
	scanSize := min(d.info.Size(), 1024)
	scanBuf := make([]byte, scanSize)
	if _, err := d.file.ReadAt(scanBuf, 0); err == nil {
		if idx := bytes.Index(scanBuf, []byte("%PDF-")); idx > 0 {
			d.pdfStart = int64(idx)
		}
	}

	tailSize := min(d.info.Size(), int64(1500))

	tailOffset := d.info.Size() - tailSize
	tail := make([]byte, tailSize)
	if _, err := d.file.ReadAt(tail, tailOffset); err != nil {
		return err
	}

	startXrefIdx := bytes.LastIndex(tail, []byte("startxref"))
	if startXrefIdx == -1 {
		return errors.New("startxref not found")
	}

	contentAfterStartXref := string(tail[startXrefIdx+9:])

	tokens := strings.Fields(contentAfterStartXref)
	if len(tokens) == 0 {
		return errors.New("startxref offset missing")
	}

	xrefOffset, err := strconv.ParseInt(tokens[0], 10, 64)
	if err != nil {
		return fmt.Errorf("could not parse startxref offset: %v", err)
	}

	d.xrefOffset = xrefOffset

	xrefErr := d.parseXRefTable(xrefOffset + d.pdfStart)
	if xrefErr != nil && d.pdfStart != 0 {
		// Some malformed (6.1.2) files record xref offsets relative to true
		// byte 0 instead of the "%PDF-" marker; retry unadjusted.
		xrefErr = d.parseXRefTable(xrefOffset)
	}

	// usedXRefStream is set when the classic parser failed but the object at
	// xrefOffset turned out to be a genuine cross-reference stream (PDF
	// 1.5+); its own dictionary then doubles as the trailer, so the literal
	// "trailer" keyword search below is skipped entirely.
	var xrefStreamTrailer PDFDict
	usedXRefStream := false

	if xrefErr != nil {
		// 6.1.4: classic xref table unparseable (e.g. it's actually a
		// cross-reference stream, which PDF/A-1b does not permit). Record the
		// violation, then recover the object table as accurately as possible
		// so an otherwise-valid modern PDF can still be read.
		d.structErrs = append(d.structErrs, PDFError{
			check: Checks.Structure.XRefKeyword,
			errs:  []error{fmt.Errorf("cross-reference table could not be parsed: %v", xrefErr)},
			page:  0,
		})

		d.xrefTable = make(map[int]int64)
		trailer, xsErr := d.tryParseXRefStream(xrefOffset+d.pdfStart, false)
		if xsErr != nil && d.pdfStart != 0 {
			trailer, xsErr = d.tryParseXRefStream(xrefOffset, false)
		}
		if xsErr == nil {
			xrefStreamTrailer, usedXRefStream = trailer, true
		} else if err := d.recoverXRefByBruteForceScan(); err != nil {
			return fmt.Errorf("failed to parse xref table: %w", xrefErr)
		}
	}

	if usedXRefStream {
		d.trailer = xrefStreamTrailer
	} else {
		searchBlock := tail[:startXrefIdx]
		trailerIdx := bytes.LastIndex(searchBlock, []byte("trailer"))
		if trailerIdx == -1 {
			if xrefErr == nil {
				return errors.New("trailer keyword not found")
			}
			// No literal "trailer" keyword and no parseable cross-reference
			// stream either: fall back to locating a brute-force-scanned
			// "/Type /XRef" object to recover /Root.
			trailer, err := d.recoverTrailerFromXRefStream()
			if err != nil {
				return errors.New("trailer keyword not found")
			}
			d.trailer = trailer
		} else {
			l := NewLexer(bytes.NewReader(searchBlock[trailerIdx:]))
			defer l.Release()

			if tok := l.NextToken(); tok.Value != "trailer" {
				return errors.New("expected 'trailer' keyword")
			}

			trailer, err := parseDictionary(l)
			if err != nil {
				return fmt.Errorf("failed to parse trailer dictionary: %w", err)
			}
			d.trailer = trailer
		}
	}

	// Build a complete object table for incrementally-updated PDFs; newer
	// revisions already in d.xrefTable take precedence over older entries.
	d.followXRefPrevChain()

	// Linearized PDFs may have a main trailer lacking /Root; locate the
	// first-page trailer instead so /Root and /ID can be found.
	if d.trailer.Entries["Root"] == nil {
		d.findAndLoadFirstPageTrailer()
	}

	return nil
}

// followXRefPrevChain walks the /Prev chain from d.trailer, filling in
// d.xrefTable from older xref sections without overwriting newer entries.
func (d *Document) followXRefPrevChain() {
	visited := map[int64]bool{d.xrefOffset: true}
	prev := d.trailer.Entries["Prev"]
	for {
		prevInt, ok := prev.(PDFInteger)
		if !ok {
			return
		}
		offset := int64(prevInt) + d.pdfStart
		if visited[offset] {
			return
		}
		visited[offset] = true

		prevTrailer, err := d.parseXRefSectionAt(offset, true /* fillIn */)
		if err != nil {
			// The previous revision may itself be a cross-reference stream
			// rather than a classic table (e.g. a chain of incremental
			// updates that all use xref streams).
			prevTrailer, err = d.tryParseXRefStream(offset, true /* fillIn */)
			if err != nil {
				return
			}
		}
		prev = prevTrailer.Entries["Prev"]
	}
}

// xrefLineRe matches "xref" at a line boundary, capturing it in group 1
// (excludes the "xref" suffix inside "startxref").
var xrefLineRe = regexp.MustCompile(`(?:^|[\r\n])(xref[\r\n])`)

// bruteForceObjRe matches an "N G obj" indirect-object header at a line
// boundary, used to rebuild the object table when xref parsing fails (6.1.4).
var bruteForceObjRe = regexp.MustCompile(`(?:^|[\r\n])(\d+)\s+\d+\s+obj\b`)

// recoverXRefByBruteForceScan rebuilds d.xrefTable by scanning the file for
// "N G obj" headers, used when the xref table cannot be parsed (6.1.4).
// Later occurrences win, matching how a real /Prev chain resolves duplicates.
func (d *Document) recoverXRefByBruteForceScan() error {
	raw := make([]byte, d.info.Size())
	if _, err := d.file.ReadAt(raw, 0); err != nil {
		return err
	}

	d.xrefTable = make(map[int]int64)
	for _, loc := range bruteForceObjRe.FindAllSubmatchIndex(raw, -1) {
		objNum, err := strconv.Atoi(string(raw[loc[2]:loc[3]]))
		if err != nil {
			continue
		}
		d.xrefTable[objNum] = int64(loc[2])
	}
	if len(d.xrefTable) == 0 {
		return errors.New("no indirect objects found")
	}
	return nil
}

// recoverTrailerFromXRefStream finds a brute-force-scanned object declaring
// "/Type /XRef" and returns its dict, recovering /Root, /Info, and /ID.
func (d *Document) recoverTrailerFromXRefStream() (PDFDict, error) {
	for objNum := range d.xrefTable {
		v, err := d.resolveReference(PDFRef{ObjNum: objNum})
		if err != nil {
			continue
		}
		dict, ok := v.(PDFDict)
		if !ok {
			continue
		}
		if dict.Entries["Type"] == (PDFName{Value: "XRef"}) {
			return dict, nil
		}
	}
	return PDFDict{}, errors.New("no cross-reference stream object found")
}

// findAndLoadFirstPageTrailer scans every xref section in a linearized PDF,
// filling d.xrefTable and setting d.firstPageTrailer to the first one with /Root.
func (d *Document) findAndLoadFirstPageTrailer() {
	size := d.info.Size()
	raw := make([]byte, size)
	if _, err := d.file.ReadAt(raw, 0); err != nil {
		return
	}

	for _, loc := range xrefLineRe.FindAllSubmatchIndex(raw, -1) {
		// loc[2] and loc[3] delimit the captured group 1 ("xref\r" or "xref\n").
		// The "xref" keyword itself starts at loc[2].
		offset := int64(loc[2])
		trailer, err := d.parseXRefSectionAt(offset, true /* fillIn */)
		if err != nil {
			continue
		}
		if trailer.Entries["Root"] != nil && d.firstPageTrailer.Entries == nil {
			d.firstPageTrailer = trailer
		}
	}
}

// effectiveTrailer returns d.trailer, or d.firstPageTrailer for linearized
// PDFs whose overflow trailer lacks /Root.
func (d *Document) effectiveTrailer() PDFDict {
	if d.firstPageTrailer.Entries != nil {
		return d.firstPageTrailer
	}
	return d.trailer
}

func (d *Document) buildPageIndex(graph PDFValue) (map[int]int, error) {
	index := make(map[int]int)

	root := graph.(PDFDict).Entries["Root"]
	if root == nil {
		return nil, fmt.Errorf("dict Root is nil")
	}
	pages := root.(PDFDict).Entries["Pages"]
	if pages == nil {
		return nil, fmt.Errorf("dict Pages is nil")
	}

	pageNum := 0

	var walk func(node PDFValue) error
	walk = func(node PDFValue) error {
		dict, ok := node.(PDFDict)
		if !ok {
			return nil
		}

		if (dict.Entries["Type"] == PDFName{Value: "Page"}) {
			pageNum++
			if ref, ok := dict.Entries["_ref"].(PDFRef); ok {
				index[ref.ObjNum] = pageNum
			}
			return nil
		}

		if kids, ok := dict.Entries["Kids"].(PDFArray); ok {
			for _, kid := range kids {
				if err := walk(kid); err != nil {
					return err
				}
			}
		}
		return nil
	}

	err := walk(pages)
	return index, err
}

// Close ensures the file handle is released.
func (d *Document) Close() error {
	return d.file.Close()
}

// GetVersion extracts the PDF version from the Document header
func (d *Document) GetVersion() (string, error) {
	if !bytes.HasPrefix(d.header, []byte("%PDF-")) {
		return "", errors.New("invalid file format: missing PDF header")
	}

	rest := d.header[len("%PDF-"):]

	end := bytes.LastIndexFunc(rest, func(r rune) bool {
		return r == '\n' || r == '\r' || unicode.IsSpace(r)
	})

	var version string
	if end == -1 {
		version = string(rest)
	} else {
		version = string(rest[:end])
	}

	if version == "" {
		return "", errors.New("invalid PDF header: missing version")
	}

	return version, nil
}

// GetMetadata extracts info from the Info dictionary.
func (d *Document) GetMetadata() (map[string]string, error) {
	value, err := d.ResolveGraphByPath([]string{"Info"})
	if err != nil {
		return nil, fmt.Errorf("no information dictionary found: %v", err)
	}

	dict, ok := value.(PDFDict)
	if !ok {
		return nil, errors.New("information object is not a dictionary")
	}

	metadata := make(map[string]string)
	for k, v := range dict.Entries {
		switch s := v.(type) {
		case PDFString:
			metadata[k] = s.Value
		case PDFHexString:
			// A hex string is a legitimate (if unusual) way to encode info
			// dict text; decode it so XMP-sync comparisons (6.7.3/6.1.5) see
			// the actual text value instead of silently dropping the entry.
			metadata[k] = string(decodePDFHexStringBytes(s.Value))
		}
	}
	return metadata, nil
}

// IsPDFA reports whether the document is valid PDF/A-1b. It is equivalent to
// calling Verify(A_1B) and checking the result's Valid field, for callers who
// only need a yes/no answer.
func (d *Document) IsPDFA() (bool, error) {
	res, err := d.Verify(A_1B)
	if err != nil {
		return false, err
	}
	return res.Valid, nil
}

// XMPMetadata returns the document's raw XMP metadata packet (Root/Metadata),
// decoded and normalised to UTF-8. It returns an error if the document has no
// XMP metadata stream, regardless of whether the document otherwise validates
// as PDF/A.
func (d *Document) XMPMetadata() ([]byte, error) {
	data, _, err := d.rawXMP()
	return data, err
}

// ClaimedConformance returns the PDF/A part and conformance level the
// document's XMP metadata claims (e.g. "1", "B"), read from the pdfaid
// namespace. This reflects what the file claims, not whether it actually
// validates — use Verify or IsPDFA to check actual compliance.
func (d *Document) ClaimedConformance() (part, conformance string, err error) {
	data, _, err := d.rawXMP()
	if err != nil {
		return "", "", err
	}
	xmp := string(data)

	part, hasPart := firstGroup(pdfaPartRe, xmp)
	if !hasPart {
		return "", "", errors.New("no PDF/A part identifier in XMP metadata")
	}
	conformance, hasConf := firstGroup(pdfaConfRe, xmp)
	if !hasConf {
		return part, "", errors.New("no PDF/A conformance level in XMP metadata")
	}
	return part, conformance, nil
}

// GetPageCount retrieves the page count.
func (d *Document) GetPageCount() (int, error) {
	value, err := d.ResolveGraphByPath([]string{"Root", "Pages", "Count"})
	if err != nil {
		return 0, err
	}
	count, ok := value.(PDFInteger)
	if !ok {
		return 0, nil
	}

	return int(count), nil
}

// ResolveGraphByPath resolves the PDF object graph by path,
// starting from the effective trailer (firstPageTrailer if set, else trailer).
func (d *Document) ResolveGraphByPath(path []string) (PDFValue, error) {
	if len(path) == 0 {
		return nil, errors.New("path cannot be empty")
	}

	return d.resolvePath(d.effectiveTrailer(), path)
}

// ResolveGraph resolves the PDF object graph,
// starting from the effective trailer (firstPageTrailer if set, else trailer).
func (d *Document) ResolveGraph() (PDFValue, error) {
	if d.graphResolved {
		return d.resolvedGraph, nil
	}
	g, err := d.resolveInPlace(d.effectiveTrailer())
	if err != nil {
		return nil, err
	}
	d.resolvedGraph, d.graphResolved = g, true
	return g, nil
}

// resolvePath walks a PDF object following path elements, which may be
// dictionary keys or array indices. node must already be a resolved object.
func (d *Document) resolvePath(node PDFValue, path []string) (PDFValue, error) {
	current, err := d.resolveObject(node)
	if err != nil {
		return nil, err
	}

	for _, key := range path {

		current, err = d.resolveShallow(current)
		if err != nil {
			return nil, err
		}

		if arr, ok := current.(PDFArray); ok {
			idx, err := strconv.Atoi(key)
			if err != nil {
				return arr, nil
			}
			if idx < 0 || idx >= len(arr) {
				return nil, fmt.Errorf("array index out of range: %d", idx)
			}
			current = arr[idx]
			continue
		}

		if dict, ok := current.(PDFDict); ok {
			val, found := dict.Entries[key]
			if !found {
				return nil, fmt.Errorf("key %q not found in dictionary", key)
			}
			current = val
			continue
		}

		return current, nil
	}

	return d.resolveObject(current)
}

// resolveInPlace returns obj fully resolved.
func (d *Document) resolveInPlace(obj PDFValue) (PDFValue, error) {
	switch v := obj.(type) {
	case PDFRef:
		target, err := d.resolveReference(v)
		if err != nil {
			return nil, err
		}
		return d.resolveInPlace(target)

	case PDFDict:
		ptr := pdfValuePointer(v.Entries)
		if d.resolvedPtrs[ptr] {
			return v, nil
		}
		if d.resolvedPtrs == nil {
			d.resolvedPtrs = map[uintptr]bool{}
		}
		d.resolvedPtrs[ptr] = true // mark before recursing, so cycles terminate
		for k, val := range v.Entries {
			if k == "_ref" {
				continue
			}
			r, err := d.resolveInPlace(val)
			if err != nil {
				// Unmark: this dict did not actually finish resolving (some
				// entries past the failing key are still raw PDFRefs), so a
				// subsequent ResolveGraph call (e.g. a caller retrying after
				// this error) must redo it rather than short-circuit-return
				// the partially-resolved value as if it were complete.
				delete(d.resolvedPtrs, ptr)
				return nil, err
			}
			v.Entries[k] = r
		}
		return v, nil

	case PDFArray:
		ptr := pdfValuePointer(v)
		if d.resolvedPtrs[ptr] {
			return v, nil
		}
		if d.resolvedPtrs == nil {
			d.resolvedPtrs = map[uintptr]bool{}
		}
		d.resolvedPtrs[ptr] = true
		for i, elem := range v {
			r, err := d.resolveInPlace(elem)
			if err != nil {
				delete(d.resolvedPtrs, ptr) // see the PDFDict case above
				return nil, err
			}
			v[i] = r
		}
		return v, nil

	default:
		return v, nil
	}
}
