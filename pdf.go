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
	trailer    map[string]any
	xrefTable  map[int]int64
	xrefOffset int64
}

// Open initializes the PDF document.
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

// initializeStructure locates startxref, parses the xref table, then the trailer. Trailer structure:
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
	l := NewLexer(searchBlock[trailerIdx:])

	if tok := l.NextToken(); tok.Value != "trailer" {
		return errors.New("expected 'trailer' keyword")
	}

	dict, err := parseDictionary(l)
	if err != nil {
		return fmt.Errorf("failed to parse trailer dictionary: %w", err)
	}
	d.trailer = dict

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

	end := bytes.IndexFunc(rest, func(r rune) bool {
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
	value, err := d.GetValueByPath([]string{"Info"})
	if err != nil {
		return nil, err
	}

	dict, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("information object is not a dictionary")
	}

	metadata := make(map[string]string)
	for k, v := range dict {
		if s, ok := v.(string); ok {
			metadata[k] = s
		}
	}
	return metadata, nil
}

// GetPageCount parses the Page Tree Root to find the Count.
func (d *Document) GetPageCount() (int, error) {
	value, err := d.GetValueByPath([]string{"Root", "Pages", "Count"})
	if err != nil {
		return 0, err
	}
	stringValue, ok := value.(string)
	if !ok {
		return 0, nil
	}
	count, err := strconv.Atoi(stringValue)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// GetValueByPath retrieves a value by walking through nested PDF structures,
// resolving references and supporting wildcards "*" for arrays and dictionaries.
func (d *Document) GetValueByPath(path []string) (any, error) {
	if len(path) == 0 {
		return nil, errors.New("path cannot be empty")
	}

	return d.walkNode(d.trailer, path)
}

// walkNode walks a decoded PDF object (map/array/primitive) following a path.
// Supports:
//   - dictionary keys, like "Root", "Pages", "Count"
//   - array indices, like "0", "1", "2"
//
// Starting point 'node' must already be a resolved object.
func (d *Document) walkNode(node any, path []string) (any, error) {
	current, err := d.resolveObject(node)
	if err != nil {
		return nil, err
	}

	for _, key := range path {

		current, err = d.resolveObject(current)
		if err != nil {
			return nil, err
		}

		if arr, ok := current.([]any); ok {
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

		if dict, ok := current.(map[string]any); ok {
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
