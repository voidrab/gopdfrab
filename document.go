package pdfrab

import (
	"bytes"
	"errors"
	"fmt"
	"os"
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

	if err := d.parseXRefTable(xrefOffset); err != nil {
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

	return nil
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
		return nil, err
	}

	dict, ok := value.(PDFDict)
	if !ok {
		return nil, errors.New("information object is not a dictionary")
	}

	metadata := make(map[string]string)
	for k, v := range dict {
		if s, ok := v.(PDFString); ok {
			metadata[k] = s.Value
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
// starting from the document trailer.
func (d *Document) ResolveGraphByPath(path []string) (PDFValue, error) {
	if len(path) == 0 {
		return nil, errors.New("path cannot be empty")
	}

	return d.resolvePath(d.trailer, path)
}

// ResolveGraph resolves the PDF object graph,
// starting from the document trailer.
func (d *Document) ResolveGraph() (PDFValue, error) {
	visited := make(map[int]PDFValue)
	return d.resolveAll(d.trailer, visited)
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
			val, found := dict[key]
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

		// Mark as visited *before* recursive resolution to prevent cycles
		visited[id] = indirect

		resolved, err := d.resolveAll(indirect, visited)
		if err != nil {
			return nil, err
		}

		visited[id] = resolved
		return resolved, nil

	// ------------------------
	// Dictionary
	// ------------------------
	case PDFDict:
		out := make(PDFDict, len(v))
		for k, val := range v {
			r, err := d.resolveAll(val, visited)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil

	case PDFStreamDict:
		out := make(PDFStreamDict, len(v))
		for k, val := range v {
			r, err := d.resolveAll(val, visited)
			if err != nil {
				return nil, err
			}
			out[k] = r
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
