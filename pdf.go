package pdfrab

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Document represents a PDF file.
type Document struct {
	file      *os.File
	info      os.FileInfo
	trailer   map[string]any
	xrefTable map[int]int64
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

	doc := &Document{
		file: f,
		info: info,
	}

	header := make([]byte, 8)
	if _, err := f.ReadAt(header, 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to read header: %w", err)
	}
	if !bytes.HasPrefix(header, []byte("%PDF-")) {
		f.Close()
		return nil, errors.New("invalid file format: missing %PDF header")
	}

	if err := doc.initializeStructure(); err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to parse structure: %w", err)
	}

	return doc, nil
}

// initializeStructure locates startxref, parses the xref table, then the trailer.
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

	contentAfterStartXref := string(tail[startXrefIdx+9:]) // skip "startxref"

	tokens := strings.Fields(contentAfterStartXref)
	if len(tokens) == 0 {
		return errors.New("startxref offset missing")
	}

	xrefOffset, err := strconv.ParseInt(tokens[0], 10, 64)
	if err != nil {
		return fmt.Errorf("could not parse startxref offset: %v", err)
	}

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

	// Consume 'trailer'
	if tok := l.NextToken(); tok.Value != "trailer" {
		return errors.New("expected 'trailer' keyword")
	}

	// Consume Dictionary
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

// parseDictionary consumes tokens to build a map.
// It expects the current token to be '<<'.
func parseDictionary(l *Lexer) (map[string]any, error) {
	dict := make(map[string]any)

	// Ensure we start with '<<'
	tok := l.NextToken()
	if tok.Type != TokenDictStart {
		return nil, fmt.Errorf("expected dictionary start '<<' but was %v", tok.Type)
	}

	for {
		// 1. Read Key (Name)
		keyTok := l.NextToken()
		if keyTok.Type == TokenDictEnd {
			break // End of dictionary '>>'
		}

		// 2. Read Value
		valTok := l.NextToken()

		// Handle indirect references (e.g., "1 0 R")
		if valTok.Type == TokenInteger {
			savedPos := l.pos
			tok2 := l.NextToken()
			tok3 := l.NextToken()

			if tok3.Type == TokenKeyword && tok3.Value == "R" {
				dict[keyTok.Value] = fmt.Sprintf("%s %s R", valTok.Value, tok2.Value)
				continue
			} else {
				// Not a reference, backtrack
				l.pos = savedPos
			}
		}

		dict[keyTok.Value] = valTok.Value
	}
	return dict, nil
}

// GetMetadata extracts info from the Info dictionary.
func (d *Document) GetMetadata() (map[string]string, error) {
	infoRef, ok := d.trailer["Info"].(string)
	if !ok {
		return nil, errors.New("no info dictionary in trailer")
	}

	obj, err := d.resolveObject(infoRef)
	if err != nil {
		return nil, err
	}

	dict, ok := obj.(map[string]any)
	if !ok {
		return nil, errors.New("info object is not a dictionary")
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
	rootRef, ok := d.trailer["Root"].(string)
	if !ok {
		return 0, errors.New("no Root found in trailer")
	}

	rootObj, err := d.resolveObject(rootRef)
	if err != nil {
		return 0, fmt.Errorf("failed to resolve Root: %w", err)
	}
	rootDict := rootObj.(map[string]any)

	pagesRef, ok := rootDict["Pages"].(string)
	if !ok {
		return 0, errors.New("root dictionary missing Pages")
	}

	pagesObj, err := d.resolveObject(pagesRef)
	if err != nil {
		return 0, fmt.Errorf("failed to resolve Pages: %w", err)
	}
	pagesDict := pagesObj.(map[string]any)

	countStr, ok := pagesDict["Count"].(string)
	if !ok {
		return 0, errors.New("pages dictionary missing Count")
	}

	count, err := strconv.Atoi(countStr)
	if err != nil {
		return 0, fmt.Errorf("invalid page count format: %v", err)
	}

	return count, nil
}
