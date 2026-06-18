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

	// firstPageTrailer is set for linearized PDFs whose main (overflow) trailer
	// at the end of the file lacks /Root.  In those files the complete trailer
	// (containing /Root, /Info, /ID) resides in the first-page section near the
	// start of the file.  effectiveTrailer() returns this field when it is set.
	firstPageTrailer PDFDict

	// structErrs collects document-structure violations (e.g. 6.1.8 object
	// framing) discovered lazily during object resolution. framingChecked
	// deduplicates per object number so repeated resolution does not double-report.
	structErrs     []PDFError
	framingChecked map[int]bool
	streamChecked  map[int]bool
}

// recordStreamFraming records a 6.1.7 stream-framing violation, deduplicated per
// object number and subclause.
func (d *Document) recordStreamFraming(objNum, sub int, msg string) {
	if d.streamChecked == nil {
		d.streamChecked = map[int]bool{}
	}
	key := objNum*1000 + sub
	if d.streamChecked[key] {
		return
	}
	d.streamChecked[key] = true
	d.structErrs = append(d.structErrs, PDFError{
		clause: "6.1.7", subclause: sub, errs: []error{errors.New(msg)}, page: 0,
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
		clause:    "6.1.8",
		subclause: 1,
		errs:      errs,
		page:      0,
	})
}

// Open initializes the PDF document at path.
// It returns a reference and any error encountered.
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

// initializeStructure locates startxref, parses the xref table and, then the trailer. Trailer structure:
//
//	trailer
//		<<
//			key1 value1
//			key2 value2
//	    	…
//	    	keyn valuen
//		>>
//	startxref
//	Byte_offset_of_last_cross-reference_section
//	%%EOF
//
// It returns any error encountered.
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

	if err := d.parseXRefTable(xrefOffset + d.pdfStart); err != nil {
		return fmt.Errorf("failed to parse xref table: %w", err)
	}

	searchBlock := tail[:startXrefIdx]
	trailerIdx := bytes.LastIndex(searchBlock, []byte("trailer"))
	if trailerIdx == -1 {
		return errors.New("trailer keyword not found")
	}

	// Parse the dictionary following "trailer"
	l := NewLexer(bytes.NewReader(searchBlock[trailerIdx:]))

	if tok := l.NextToken(); tok.Value != "trailer" {
		return errors.New("expected 'trailer' keyword")
	}

	trailer, err := parseDictionary(l)
	if err != nil {
		return fmt.Errorf("failed to parse trailer dictionary: %w", err)
	}
	d.trailer = trailer

	// Follow the /Prev chain in the xref to build a complete object table
	// for incrementally-updated PDFs.  Newer revisions already in d.xrefTable
	// take precedence; older entries are added only when missing.
	d.followXRefPrevChain()

	// For linearized PDFs whose main (overflow) trailer lacks /Root, locate the
	// complete first-page trailer so that the verifier can find /Root and /ID.
	if d.trailer.Entries["Root"] == nil {
		d.findAndLoadFirstPageTrailer()
	}

	return nil
}

// followXRefPrevChain walks the /Prev chain from d.trailer and fills in
// d.xrefTable with object offsets from older xref sections. Entries already
// present (from the most-recent revision) are never overwritten.
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
			return
		}
		prev = prevTrailer.Entries["Prev"]
	}
}

// xrefLineRe matches the keyword "xref" at a line boundary.  It captures "xref"
// preceded by the start of input or by a carriage-return or newline (to exclude
// the "xref" suffix inside "startxref", which is always preceded by 't').
// Group 1 captures "xref" itself so its position can be found via loc[1].
var xrefLineRe = regexp.MustCompile(`(?:^|[\r\n])(xref[\r\n])`)

// findAndLoadFirstPageTrailer is called when the main (overflow) trailer lacks
// /Root, indicating a linearized PDF.  It scans the raw file for every
// cross-reference section (identified as "xref" at a line boundary),
// parses each one to fill missing object entries into d.xrefTable, and sets
// d.firstPageTrailer to the first trailer dictionary that contains /Root.
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
		// Keep the first trailer that supplies /Root (the first-page trailer).
		if trailer.Entries["Root"] != nil && d.firstPageTrailer.Entries == nil {
			d.firstPageTrailer = trailer
		}
	}
}

// effectiveTrailer returns the trailer dictionary to use for document-level
// checks (/Root, /ID, /Info, etc.).  For ordinary PDFs this is d.trailer; for
// linearized PDFs whose overflow trailer lacks /Root, it is d.firstPageTrailer.
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
	visited := make(map[int]PDFValue)
	return d.resolveAll(d.effectiveTrailer(), visited)
}

// resolvePath walks a PDF object (map/array/primitive) following a path.
// It supports:
//   - dictionary keys, like "Root", "Pages", "Count"
//   - array indices, like "0", "1", "2"
//
// Starting point 'node' must already be a resolved object, such as the document trailer.
func (d *Document) resolvePath(node PDFValue, path []string) (PDFValue, error) {
	current, err := d.resolveObject(node)
	if err != nil {
		return nil, err
	}

	for _, key := range path {

		current, err = d.resolveObject(current)
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

// resolveAll recursively resolves a PDF object graph, including indirect references.
func (d *Document) resolveAll(obj PDFValue, visited map[int]PDFValue) (PDFValue, error) {
	switch v := obj.(type) {

	// ------------------------
	// Indirect reference
	// ------------------------
	case PDFRef:
		id := v.ObjNum

		if cached, ok := visited[id]; ok {
			return cached, nil
		}

		indirect, err := d.resolveReference(v)
		if err != nil {
			return nil, err
		}

		// Allocate the resolved container and cache it *before* recursing so that
		// cyclic references (e.g. Popup/Parent, Page/Parent) resolve to the same,
		// fully-populated instance rather than a dangling reference.
		switch inner := indirect.(type) {
		case PDFDict:
			out := PDFDict{Entries: make(map[string]PDFValue, len(inner.Entries)), HasStream: inner.HasStream, RawStream: inner.RawStream}
			visited[id] = out
			for k, val := range inner.Entries {
				r, err := d.resolveAll(val, visited)
				if err != nil {
					return nil, err
				}
				out.Entries[k] = r
			}
			return out, nil

		case PDFArray:
			out := make(PDFArray, len(inner))
			visited[id] = out
			for i, elem := range inner {
				r, err := d.resolveAll(elem, visited)
				if err != nil {
					return nil, err
				}
				out[i] = r
			}
			return out, nil

		default:
			visited[id] = inner
			return inner, nil
		}

	// ------------------------
	// Dictionary
	// ------------------------
	case PDFDict:
		out := NewPDFDict()
		out.HasStream = v.HasStream
		out.RawStream = v.RawStream
		for k, val := range v.Entries {
			r, err := d.resolveAll(val, visited)
			if err != nil {
				return nil, err
			}
			out.Entries[k] = r
		}
		return out, nil

	// ------------------------
	// Array
	// ------------------------
	case PDFArray:
		out := make(PDFArray, len(v))
		for i, elem := range v {
			r, err := d.resolveAll(elem, visited)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil

	// ------------------------
	// Primitives
	// ------------------------
	default:
		return v, nil
	}
}
